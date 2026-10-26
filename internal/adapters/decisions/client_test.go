package decisions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
)

// The decision client (issue #416, S5): tests answer through an injected
// RoundTripper, never a listening socket (go-tests rule 10), and assert the wire
// body byte for byte — the request must carry the model, the input, and the
// choice question, and nothing else.

const testAPIKey = "sk-test-key"

// roundTripFunc adapts a function to http.RoundTripper.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// recordedRequest is what the transport saw, captured before the body is read.
type recordedRequest struct {
	method string
	url    string
	header http.Header
	body   []byte
	calls  int
}

// respondWith returns a transport that records the request and answers with the
// given status and body.
func respondWith(rec *recordedRequest, status int, body string, header map[string]string) *Client {
	return newClientWithTransport(roundTripFunc(func(r *http.Request) (*http.Response, error) {
		rec.calls++
		rec.method = r.Method
		rec.url = r.URL.String()
		rec.header = r.Header.Clone()
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		rec.body = raw
		h := http.Header{}
		for k, v := range header {
			h.Set(k, v)
		}
		return &http.Response{StatusCode: status, Status: http.StatusText(status), Header: h, Body: io.NopCloser(strings.NewReader(body))}, nil
	}))
}

func testConfig() application.DecisionModelConfig {
	return application.DecisionModelConfig{EndpointURL: "https://api.example.com/v1", Model: "gpt-4.1-mini", APIKey: testAPIKey}
}

func testRequest() application.DecisionChoiceRequest {
	return application.DecisionChoiceRequest{
		Input: "The laptop screen flickers",
		Choices: []application.DecisionChoice{
			{Value: "7", Description: "Hardware: laptops and screens"},
			{Value: "8", Description: "Access: accounts and passwords"},
			{Value: "none", Description: "None of these categories fits the request."},
		},
	}
}

func TestChooseSendsTheDocumentedDecisionsRequest(t *testing.T) {
	rec := &recordedRequest{}
	client := respondWith(rec, http.StatusOK, `{"answers":[{"type":"choice","name":"category","choice":"7","probabilities":[{"value":"7","probability":0.95}],"confidence":0.93}]}`, map[string]string{"Content-Type": "application/json"})
	if _, err := client.Choose(context.Background(), testConfig(), testRequest()); err != nil {
		t.Fatalf("Choose: %v", err)
	}

	if rec.method != http.MethodPost {
		t.Errorf("method = %q, want POST", rec.method)
	}
	if rec.url != "https://api.example.com/v1/decisions" {
		t.Errorf("url = %q, want the endpoint plus /decisions", rec.url)
	}
	if got := rec.header.Get("Authorization"); got != "Bearer "+testAPIKey {
		t.Errorf("Authorization = %q, want the bearer key", got)
	}
	if got := rec.header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	if strings.Contains(string(rec.body), testAPIKey) {
		t.Error("the request body carries the API key")
	}

	// The body carries the model, the input, and the questions, and nothing
	// else (S5: no requester identity, no attachments, no history).
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(rec.body, &decoded); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(decoded) != 3 {
		t.Fatalf("body keys = %v, want exactly model, input, questions", keysOf(decoded))
	}
	for _, key := range []string{"model", "input", "questions"} {
		if _, ok := decoded[key]; !ok {
			t.Errorf("body is missing %q", key)
		}
	}

	var questions []struct {
		Type         string `json:"type"`
		Name         string `json:"name"`
		Instructions string `json:"instructions"`
		Choices      []struct {
			Value       string `json:"value"`
			Description string `json:"description"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(decoded["questions"], &questions); err != nil {
		t.Fatalf("decode questions: %v", err)
	}
	if len(questions) != 1 {
		t.Fatalf("questions = %d, want one", len(questions))
	}
	q := questions[0]
	if q.Type != "choice" || q.Name != "category" || q.Instructions != "Which help-desk category should handle this request?" {
		t.Fatalf("question = %+v, want the documented choice question", q)
	}
	if len(q.Choices) != 3 {
		t.Fatalf("choices = %+v, want the offered ones", q.Choices)
	}
	if q.Choices[0].Value != "7" || q.Choices[0].Description != "Hardware: laptops and screens" {
		t.Fatalf("first choice = %+v, want the offered value and description", q.Choices[0])
	}
}

func keysOf(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestChooseParsesTheAnswer(t *testing.T) {
	rec := &recordedRequest{}
	client := respondWith(rec, http.StatusOK, `{"answers":[{"type":"choice","name":"category","choice":"8","probabilities":[{"value":"7","probability":0.1},{"value":"8","probability":0.8},{"value":"none","probability":0.1}],"confidence":0.88}]}`, nil)
	got, err := client.Choose(context.Background(), testConfig(), testRequest())
	if err != nil {
		t.Fatalf("Choose: %v", err)
	}
	if got.Choice != "8" || got.Confidence != 0.88 || got.Refused {
		t.Fatalf("answer = %+v, want choice 8 with confidence 0.88", got)
	}
	if got.Probabilities["7"] != 0.1 || got.Probabilities["8"] != 0.8 || got.Probabilities["none"] != 0.1 {
		t.Fatalf("probabilities = %+v", got.Probabilities)
	}
}

func TestChooseReportsARefusal(t *testing.T) {
	// The documented refusal shape need not carry the question name, so both forms
	// are a refusal.
	for _, body := range []string{
		`{"answers":[{"type":"refusal","name":"category"}]}`,
		`{"answers":[{"type":"refusal"}]}`,
	} {
		rec := &recordedRequest{}
		client := respondWith(rec, http.StatusOK, body, nil)
		got, err := client.Choose(context.Background(), testConfig(), testRequest())
		if err != nil {
			t.Fatalf("Choose(%s): %v", body, err)
		}
		if !got.Refused {
			t.Fatalf("answer for %s = %+v, want a refusal", body, got)
		}
	}
}

func TestChooseRejectsEveryUnusableAnswer(t *testing.T) {
	cases := map[string]struct {
		status int
		body   string
	}{
		"non-2xx":        {status: http.StatusUnauthorized, body: `{"error":"invalid api key"}`},
		"server error":   {status: http.StatusBadGateway, body: `bad gateway`},
		"malformed":      {status: http.StatusOK, body: `{"answers":[`},
		"no answers":     {status: http.StatusOK, body: `{}`},
		"other question": {status: http.StatusOK, body: `{"answers":[{"type":"choice","name":"sentiment","choice":"7"}]}`},
		"unknown choice": {status: http.StatusOK, body: `{"answers":[{"type":"choice","name":"category","choice":"99"}]}`},
		"body too large": {status: http.StatusOK, body: `{"answers":[{"type":"choice","name":"category","choice":"7","probabilities":[{"value":"7","probability":0.9}]}]}` + strings.Repeat(" ", maxResponseBytes)},
	}
	for name, tc := range cases {
		rec := &recordedRequest{}
		client := respondWith(rec, tc.status, tc.body, nil)
		_, err := client.Choose(context.Background(), testConfig(), testRequest())
		if err == nil {
			t.Errorf("%s: Choose = nil error, want a failure", name)
			continue
		}
		if strings.Contains(err.Error(), testAPIKey) {
			t.Errorf("%s: the error carries the API key: %s", name, err)
		}
	}
}

func TestChooseHonorsTheContextDeadline(t *testing.T) {
	rec := &recordedRequest{}
	client := newClientWithTransport(roundTripFunc(func(r *http.Request) (*http.Response, error) {
		rec.calls++
		if err := r.Context().Err(); err != nil {
			return nil, err
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
	}))
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	_, err := client.Choose(ctx, testConfig(), testRequest())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Choose with an expired context = %v, want context.DeadlineExceeded", err)
	}
	if strings.Contains(err.Error(), testAPIKey) {
		t.Fatal("the context error carries the API key")
	}
}

func TestClientRefusesRedirectsAndBoundsTheTimeout(t *testing.T) {
	rec := &recordedRequest{}
	client := newClientWithTransport(roundTripFunc(func(r *http.Request) (*http.Response, error) {
		rec.calls++
		return &http.Response{
			StatusCode: http.StatusFound,
			Header:     http.Header{"Location": []string{"https://evil.example.com/decisions"}},
			Body:       io.NopCloser(strings.NewReader("")),
		}, nil
	}))
	if _, err := client.Choose(context.Background(), testConfig(), testRequest()); err == nil {
		t.Fatal("Choose followed a redirect, want a failure")
	}
	if rec.calls != 1 {
		t.Fatalf("the transport was called %d times, want exactly once: the Authorization header must never follow", rec.calls)
	}
	if NewClient().http.Timeout != 2*time.Second || RequestTimeout != 2*time.Second {
		t.Fatalf("client timeout = %v, want 2s (S5)", NewClient().http.Timeout)
	}
}

// TestChooseRefusesAnEndpointThatIsNotHTTPS proves the key never leaves over
// cleartext even when the stored endpoint bypassed the settings validation (a
// hand-edited database): the request is refused before the transport is called.
func TestChooseRefusesAnEndpointThatIsNotHTTPS(t *testing.T) {
	for _, endpoint := range []string{"http://api.example.com/v1", "ftp://api.example.com/v1", "/v1", "https:///v1"} {
		rec := &recordedRequest{}
		client := respondWith(rec, http.StatusOK, `{"answers":[]}`, nil)
		cfg := testConfig()
		cfg.EndpointURL = endpoint
		_, err := client.Choose(context.Background(), cfg, testRequest())
		if err == nil {
			t.Fatalf("Choose(%q) = nil error, want a refusal", endpoint)
		}
		if rec.calls != 0 {
			t.Fatalf("Choose(%q) called the transport %d times, want 0", endpoint, rec.calls)
		}
		if strings.Contains(err.Error(), testAPIKey) {
			t.Fatalf("Choose(%q) error carries the key: %v", endpoint, err)
		}
	}
}

func TestChooseMalformedRedirectNeverDisclosesTheKey(t *testing.T) {
	rec := &recordedRequest{}
	client := respondWith(rec, http.StatusFound, "", map[string]string{
		"Location": "https://other.example.invalid/%zz/" + testAPIKey,
	})
	got, err := client.Choose(context.Background(), testConfig(), testRequest())
	if err == nil {
		t.Fatal("Choose accepted a malformed redirect, want a failure")
	}
	if got.Choice != "" || got.Confidence != 0 || got.Refused || len(got.Probabilities) != 0 {
		t.Errorf("Choose returned a partial answer on failure: %+v", got)
	}
	if rec.calls != 1 {
		t.Fatalf("transport calls = %d, want one: the bearer key must not follow a redirect", rec.calls)
	}
	if rec.header.Get("Authorization") != "Bearer "+testAPIKey {
		t.Fatal("the initial request did not carry the fake bearer key")
	}
	for cause := err; cause != nil; cause = errors.Unwrap(cause) {
		for _, format := range []string{"%v", "%+v", "%#v"} {
			if strings.Contains(fmt.Sprintf(format, cause), testAPIKey) {
				t.Errorf("returned error chain exposes the fake bearer key using %s", format)
			}
		}
	}
}
