// Package decisions asks a decision model which category should handle a ticket
// description (issue #416, S5). It speaks the OpenAI Decisions API over
// net/http, so one integration covers OpenAI directly and OpenAI-compatible
// gateways: POST <endpoint>/decisions with one "choice" question, authenticated
// with a bearer key.
//
// The package owns the wire format only. The thresholds, the fallback to the
// local engine, and the text that is sent belong to the application layer. The
// API key never appears in a returned error.
// Call failures retain cancellation and deadline classification without
// returning remote-controlled error details.
package decisions

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"strings"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
)

// RequestTimeout bounds one call. It matches the two seconds S5 allows before
// the suggestion comes from the local engine; the request also carries the
// caller's context, so an earlier deadline wins.
const RequestTimeout = 2 * time.Second

// maxResponseBytes bounds how much of the answer is read, so a misbehaving
// endpoint cannot make TKT buffer an unbounded body.
const maxResponseBytes = 1 << 20

// The question this package asks. The name is how the answer is matched back,
// and the instructions are what the model reads, not rendered copy.
const (
	questionType         = "choice"
	questionName         = "category"
	questionInstructions = "Which help-desk category should handle this request?"
	decisionsPath        = "/decisions"
)

// Client implements application.DecisionModel over net/http.
type Client struct {
	http *http.Client
}

var _ application.DecisionModel = (*Client)(nil)

// NewClient returns the production client.
func NewClient() *Client {
	return &Client{http: &http.Client{
		Timeout: RequestTimeout,
		// A redirect is refused instead of followed: the Authorization header
		// must never travel to another host.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

// newClientWithTransport is the seam tests use to answer without a socket.
func newClientWithTransport(rt http.RoundTripper) *Client {
	c := NewClient()
	c.http.Transport = rt
	return c
}

// choice and question are the request's wire shape.
type choice struct {
	Value       string `json:"value"`
	Description string `json:"description"`
}

type question struct {
	Type         string   `json:"type"`
	Name         string   `json:"name"`
	Instructions string   `json:"instructions"`
	Choices      []choice `json:"choices"`
}

// decisionsRequest is exactly the documented request body: the model, the input,
// and the questions. Nothing else is sent (S5).
type decisionsRequest struct {
	Model     string     `json:"model"`
	Input     string     `json:"input"`
	Questions []question `json:"questions"`
}

// probability and answer are the response's wire shape.
type probability struct {
	Value       string  `json:"value"`
	Probability float64 `json:"probability"`
}

type answer struct {
	Type          string        `json:"type"`
	Name          string        `json:"name"`
	Choice        string        `json:"choice"`
	Probabilities []probability `json:"probabilities"`
	Confidence    float64       `json:"confidence"`
}

type decisionsResponse struct {
	Answers []answer `json:"answers"`
}

// Choose asks the model one choice question and maps its answer back. Every
// failure — transport, non-2xx status, malformed body, a missing answer, an
// answer that names a value that was never offered — is an error, so the caller
// falls back to the local engine. The returned error never contains the key.
func (c *Client) Choose(ctx context.Context, cfg application.DecisionModelConfig, req application.DecisionChoiceRequest) (application.DecisionChoiceAnswer, error) {
	body, err := json.Marshal(decisionsRequest{
		Model: cfg.Model,
		Input: req.Input,
		Questions: []question{{
			Type:         questionType,
			Name:         questionName,
			Instructions: questionInstructions,
			Choices:      toWireChoices(req.Choices),
		}},
	})
	if err != nil {
		return application.DecisionChoiceAnswer{}, fmt.Errorf("decisions: encode request: %w", err)
	}

	// The settings validation already requires https, but the key must not
	// leave over cleartext even when a stored endpoint bypassed it.
	if endpoint, err := neturl.Parse(cfg.EndpointURL); err != nil || endpoint.Scheme != "https" || endpoint.Host == "" {
		return application.DecisionChoiceAnswer{}, errors.New("decisions: the endpoint must be an https URL with a host")
	}
	url := strings.TrimRight(cfg.EndpointURL, "/") + decisionsPath
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		// url.Error carries the URL, never the key.
		return application.DecisionChoiceAnswer{}, fmt.Errorf("decisions: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+cfg.APIKey)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		// Do can quote server-controlled redirect headers containing the key.
		// Preserve context classification, never the original error chain.
		switch {
		case errors.Is(err, context.Canceled):
			err = context.Canceled
		case errors.Is(err, context.DeadlineExceeded):
			err = context.DeadlineExceeded
		default:
			err = errors.New("request failed")
		}
		return application.DecisionChoiceAnswer{}, fmt.Errorf("decisions: call the model: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return application.DecisionChoiceAnswer{}, fmt.Errorf("decisions: the model answered with status %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return application.DecisionChoiceAnswer{}, fmt.Errorf("decisions: read the answer: %w", err)
	}
	if len(raw) > maxResponseBytes {
		return application.DecisionChoiceAnswer{}, fmt.Errorf("decisions: the answer exceeds %d bytes", maxResponseBytes)
	}
	var decoded decisionsResponse
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return application.DecisionChoiceAnswer{}, fmt.Errorf("decisions: decode the answer: %w", err)
	}
	return toAnswer(decoded, req.Choices)
}

func toWireChoices(choices []application.DecisionChoice) []choice {
	out := make([]choice, 0, len(choices))
	for _, c := range choices {
		out = append(out, choice{Value: c.Value, Description: c.Description})
	}
	return out
}

// toAnswer finds this question's answer and validates it against the choices
// that were offered.
func toAnswer(resp decisionsResponse, offered []application.DecisionChoice) (application.DecisionChoiceAnswer, error) {
	if len(resp.Answers) == 0 {
		return application.DecisionChoiceAnswer{}, errors.New("decisions: the answer carries no answers")
	}
	// A refusal is the whole answer, whatever it is named: the documented refusal
	// shape need not carry the question name.
	for _, a := range resp.Answers {
		if a.Type == "refusal" {
			return application.DecisionChoiceAnswer{Refused: true}, nil
		}
	}
	var found *answer
	for i := range resp.Answers {
		if resp.Answers[i].Name == questionName {
			found = &resp.Answers[i]
			break
		}
	}
	if found == nil {
		return application.DecisionChoiceAnswer{}, fmt.Errorf("decisions: the answer carries no %q answer", questionName)
	}
	offeredValues := make(map[string]bool, len(offered))
	for _, c := range offered {
		offeredValues[c.Value] = true
	}
	if !offeredValues[found.Choice] {
		return application.DecisionChoiceAnswer{}, errors.New("decisions: the model chose a value that was not offered")
	}
	out := application.DecisionChoiceAnswer{
		Choice:     found.Choice,
		Confidence: found.Confidence,
	}
	if len(found.Probabilities) > 0 {
		out.Probabilities = make(map[string]float64, len(found.Probabilities))
		for _, p := range found.Probabilities {
			out.Probabilities[p.Value] = p.Probability
		}
	}
	return out, nil
}
