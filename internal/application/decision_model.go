package application

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"
)

// Decision-engine category suggestions (issue #416, S5/S8): the external engine
// asks a decision model which category should handle what the requester wrote,
// through the OpenAI Decisions API (a "choice" question over the categories).
// The suggestion is advisory: the requester always chooses (S3), and any failure
// falls back to the local engine, which is the caller's decision.

// DecisionModelTimeout bounds one decision-model call (S5): a model that takes
// longer than this is treated as unavailable, and that suggestion comes from the
// local engine instead.
const DecisionModelTimeout = 2 * time.Second

// Decision-engine thresholds (S8): these numbers belong to the decision engine
// only and are never shared with the local engine's own ranking constants.
const (
	// DecisionSuggestionTopThreshold is the probability the best choice must
	// reach. Below it the model itself is not sure, and S3 says nothing is
	// suggested: half is the boundary between "more likely than not" and a
	// coin flip.
	DecisionSuggestionTopThreshold = 0.5
	// DecisionSuggestionMinProbability is the probability a second or third
	// choice must still reach: one chance in five is worth offering, and the
	// long tail below it is noise.
	DecisionSuggestionMinProbability = 0.2
	// DecisionSuggestionMax is how many suggestions are offered, matching the
	// three the pickers show (S4).
	DecisionSuggestionMax = 3
)

// decisionNoneChoice is the value of the offered "none of these" choice: a
// model that picks it says no category fits, so the local engine answers.
const decisionNoneChoice = "none"

// decisionNoneDescription is the copy the model reads for the "none" choice. It
// is sent to the model, not rendered, so it is not catalogue copy.
const decisionNoneDescription = "None of these categories fits the request."

// ErrDecisionUnavailable reports that the decision engine cannot answer this
// request: the model failed, timed out, refused, chose no category, answered
// with an unknown choice, or was not confident enough. The caller then takes the
// suggestion from the local engine (S5).
var ErrDecisionUnavailable = errors.New("decision model unavailable")

// DecisionModelConfig is what one decision-model call needs. String keeps the
// API key out of every log line and error.
type DecisionModelConfig struct {
	EndpointURL string
	Model       string
	APIKey      string
}

// String never prints the API key.
func (c DecisionModelConfig) String() string {
	return fmt.Sprintf("{endpoint=%s model=%q api_key=%t}", c.EndpointURL, c.Model, c.APIKey != "")
}

// GoString covers %#v, which bypasses String.
func (c DecisionModelConfig) GoString() string { return c.String() }

// DecisionChoice is one option offered to the model. Value is what comes back;
// Description is what the model reads.
type DecisionChoice struct {
	Value       string
	Description string
}

// DecisionChoiceRequest is one decision question: the text to classify and the
// choices to classify it into. The question's name and instructions belong to
// the adapter that speaks the wire format.
type DecisionChoiceRequest struct {
	Input   string
	Choices []DecisionChoice
}

// DecisionChoiceAnswer is the model's answer: the chosen value, the probability
// it gives each value, its overall confidence, and whether it refused to answer.
type DecisionChoiceAnswer struct {
	Choice        string
	Probabilities map[string]float64
	Confidence    float64
	Refused       bool
}

// DecisionModel answers one choice question. The adapter implements the wire
// format; this package owns the thresholds and the fallback rule.
type DecisionModel interface {
	Choose(ctx context.Context, cfg DecisionModelConfig, req DecisionChoiceRequest) (DecisionChoiceAnswer, error)
}

// DecisionCategory is a category the suggester may offer: only its identity and
// its catalogue copy, never the requester (S5).
type DecisionCategory struct {
	ID          int64
	Name        string
	Description string
}

// DecisionSuggestion is one suggestion: the category and how likely the model
// thinks it is.
type DecisionSuggestion struct {
	CategoryID  int64
	Probability float64
}

// DecisionCategorySuggester asks the decision model which category fits one
// description. It owns the question it asks and the thresholds it applies; the
// configuration comes from the settings on every call, so a saved change applies
// without a restart.
type DecisionCategorySuggester struct {
	model DecisionModel
}

// NewDecisionCategorySuggester wires the suggester to a decision model.
func NewDecisionCategorySuggester(model DecisionModel) *DecisionCategorySuggester {
	return &DecisionCategorySuggester{model: model}
}

// Suggest returns up to three suggestions ordered by probability. It answers
// ErrDecisionUnavailable, and no suggestion, in every case where the caller must
// fall back to the local engine: a port error, the two-second timeout, a
// refusal, the "none" choice, an unknown value, or a best probability below the
// decision threshold.
func (s *DecisionCategorySuggester) Suggest(ctx context.Context, cfg DecisionModelConfig, text string, categories []DecisionCategory) ([]DecisionSuggestion, error) {
	if len(categories) == 0 {
		return nil, fmt.Errorf("%w: no categories to choose from", ErrDecisionUnavailable)
	}
	choices := make([]DecisionChoice, 0, len(categories)+1)
	byValue := make(map[string]int64, len(categories))
	for _, c := range categories {
		value := strconv.FormatInt(c.ID, 10)
		byValue[value] = c.ID
		choices = append(choices, DecisionChoice{Value: value, Description: c.Name + ": " + c.Description})
	}
	choices = append(choices, DecisionChoice{Value: decisionNoneChoice, Description: decisionNoneDescription})

	callCtx, cancel := context.WithTimeout(ctx, DecisionModelTimeout)
	defer cancel()
	answer, err := s.model.Choose(callCtx, cfg, DecisionChoiceRequest{Input: text, Choices: choices})
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrDecisionUnavailable, err)
	}
	switch {
	case answer.Refused:
		return nil, fmt.Errorf("%w: the model refused to answer", ErrDecisionUnavailable)
	case answer.Choice == decisionNoneChoice:
		return nil, fmt.Errorf("%w: no category fits the request", ErrDecisionUnavailable)
	}
	chosenID, ok := byValue[answer.Choice]
	if !ok {
		return nil, fmt.Errorf("%w: the model chose an unknown category", ErrDecisionUnavailable)
	}

	suggestions := make([]DecisionSuggestion, 0, len(answer.Probabilities))
	for value, probability := range answer.Probabilities {
		if value == decisionNoneChoice {
			continue
		}
		id, ok := byValue[value]
		if !ok {
			return nil, fmt.Errorf("%w: the model answered with an unknown category", ErrDecisionUnavailable)
		}
		suggestions = append(suggestions, DecisionSuggestion{CategoryID: id, Probability: probability})
	}
	if len(suggestions) == 0 {
		// A model that answers with a choice and a confidence but no
		// per-choice probabilities still says how sure it is.
		suggestions = append(suggestions, DecisionSuggestion{CategoryID: chosenID, Probability: answer.Confidence})
	}
	sort.Slice(suggestions, func(i, j int) bool {
		if suggestions[i].Probability != suggestions[j].Probability {
			return suggestions[i].Probability > suggestions[j].Probability
		}
		return suggestions[i].CategoryID < suggestions[j].CategoryID
	})
	if suggestions[0].Probability < DecisionSuggestionTopThreshold {
		return nil, fmt.Errorf("%w: the best probability %g is below the decision threshold", ErrDecisionUnavailable, suggestions[0].Probability)
	}
	out := make([]DecisionSuggestion, 0, DecisionSuggestionMax)
	for _, s := range suggestions {
		if len(out) == DecisionSuggestionMax {
			break
		}
		// The list is ordered by probability, so the first entry below the floor
		// ends it: nothing after it reaches the floor either.
		if s.Probability < DecisionSuggestionMinProbability {
			break
		}
		out = append(out, s)
	}
	return out, nil
}
