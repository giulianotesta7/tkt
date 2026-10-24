package application

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// Decision-engine suggestions (issue #416, S5/S8): the suggester asks the model
// one choice question over the offered categories, applies its OWN thresholds,
// and answers ErrDecisionUnavailable — never a guess — whenever the caller must
// fall back to the local engine.

func decisionCategories() []DecisionCategory {
	return []DecisionCategory{
		{ID: 1, Name: "Hardware", Description: "Laptops, screens, and printers"},
		{ID: 2, Name: "Access", Description: "Accounts and passwords"},
		{ID: 3, Name: "Network", Description: "Wi-Fi and VPN"},
		{ID: 4, Name: "Billing", Description: "Invoices"},
	}
}

func decisionConfig() DecisionModelConfig {
	return DecisionModelConfig{EndpointURL: "https://api.example.com/v1", Model: "gpt-4.1-mini", APIKey: "sk-test-key"}
}

func TestDecisionSuggesterReturnsUpToThreeByProbability(t *testing.T) {
	f := &fakeDecisionModel{answer: DecisionChoiceAnswer{
		Choice:        "1",
		Probabilities: map[string]float64{"1": 0.7, "2": 0.3, "3": 0.19, "4": 0.05, "none": 0.01},
		Confidence:    0.9,
	}}
	suggester := NewDecisionCategorySuggester(f)
	got, err := suggester.Suggest(context.Background(), decisionConfig(), "my laptop screen is broken", decisionCategories())
	if err != nil {
		t.Fatalf("Suggest: %v", err)
	}
	want := []DecisionSuggestion{{CategoryID: 1, Probability: 0.7}, {CategoryID: 2, Probability: 0.3}}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("Suggest = %+v, want %+v", got, want)
	}
}

func TestDecisionSuggesterOffersAtMostThree(t *testing.T) {
	f := &fakeDecisionModel{answer: DecisionChoiceAnswer{
		Choice:        "1",
		Probabilities: map[string]float64{"1": 0.4, "2": 0.3, "3": 0.2, "4": 0.1},
	}}
	// 0.4 is below the top threshold, so the model is not confident enough.
	if _, err := NewDecisionCategorySuggester(f).Suggest(context.Background(), decisionConfig(), "anything", decisionCategories()); !errors.Is(err, ErrDecisionUnavailable) {
		t.Fatalf("Suggest with a top of 0.4 = %v, want ErrDecisionUnavailable", err)
	}

	f.answer = DecisionChoiceAnswer{
		Choice:        "1",
		Probabilities: map[string]float64{"1": 0.55, "2": 0.4, "3": 0.35, "4": 0.3},
	}
	got, err := NewDecisionCategorySuggester(f).Suggest(context.Background(), decisionConfig(), "anything", decisionCategories())
	if err != nil {
		t.Fatalf("Suggest: %v", err)
	}
	if len(got) != DecisionSuggestionMax {
		t.Fatalf("Suggest returned %d suggestions, want at most %d: %+v", len(got), DecisionSuggestionMax, got)
	}
	for i := 1; i < len(got); i++ {
		if got[i-1].Probability < got[i].Probability {
			t.Fatalf("suggestions are not ordered by probability: %+v", got)
		}
	}
}

func TestDecisionSuggesterUsesTheConfidenceWhenNoProbabilitiesAreGiven(t *testing.T) {
	f := &fakeDecisionModel{answer: DecisionChoiceAnswer{Choice: "2", Confidence: 0.8}}
	got, err := NewDecisionCategorySuggester(f).Suggest(context.Background(), decisionConfig(), "I cannot log in", decisionCategories())
	if err != nil {
		t.Fatalf("Suggest: %v", err)
	}
	if len(got) != 1 || got[0].CategoryID != 2 || got[0].Probability != 0.8 {
		t.Fatalf("Suggest = %+v, want the chosen category at its confidence", got)
	}
}

func TestDecisionSuggesterFallsBackToTheLocalEngine(t *testing.T) {
	// Each case is one reason the caller must run the local engine instead.
	cases := map[string]struct {
		answer     DecisionChoiceAnswer
		err        error
		categories []DecisionCategory
	}{
		"port error":            {err: errors.New("connection reset")},
		"timeout":               {err: context.DeadlineExceeded},
		"refusal":               {answer: DecisionChoiceAnswer{Refused: true}},
		"top below threshold":   {answer: DecisionChoiceAnswer{Choice: "1", Probabilities: map[string]float64{"1": 0.49, "2": 0.4}}},
		"unknown choice":        {answer: DecisionChoiceAnswer{Choice: "99", Probabilities: map[string]float64{"1": 0.9}}},
		"unknown value":         {answer: DecisionChoiceAnswer{Choice: "1", Probabilities: map[string]float64{"1": 0.9, "99": 0.1}}},
		"only the none choice":  {answer: DecisionChoiceAnswer{Choice: "1", Probabilities: map[string]float64{"none": 0.9}}},
		"no categories offered": {answer: DecisionChoiceAnswer{Choice: "1", Probabilities: map[string]float64{"1": 0.9}}, categories: []DecisionCategory{}},
	}
	for name, tc := range cases {
		f := &fakeDecisionModel{answer: tc.answer, err: tc.err}
		categories := tc.categories
		if categories == nil {
			categories = decisionCategories()
		}
		got, err := NewDecisionCategorySuggester(f).Suggest(context.Background(), decisionConfig(), "anything", categories)
		if !errors.Is(err, ErrDecisionUnavailable) {
			t.Errorf("%s: Suggest = %v, want ErrDecisionUnavailable", name, err)
		}
		if len(got) != 0 {
			t.Errorf("%s: Suggest returned %+v alongside the fallback", name, got)
		}
		wantCalls := 1
		if name == "no categories offered" {
			// Nothing to ask about: the call is not even made.
			wantCalls = 0
		}
		if f.calls != wantCalls {
			t.Errorf("%s: the model was called %d times, want %d", name, f.calls, wantCalls)
		}
	}
}

func TestDecisionSuggesterBoundsTheCallAtTwoSeconds(t *testing.T) {
	f := &fakeDecisionModel{answer: DecisionChoiceAnswer{Choice: "1", Probabilities: map[string]float64{"1": 0.9}}}
	start := time.Now()
	if _, err := NewDecisionCategorySuggester(f).Suggest(context.Background(), decisionConfig(), "anything", decisionCategories()); err != nil {
		t.Fatalf("Suggest: %v", err)
	}
	if !f.hasDeadline {
		t.Fatal("the model was called without a deadline; S5 bounds the call at two seconds")
	}
	// The deadline is read after the clock read above, so a small allowance covers
	// that gap without weakening the two-second bound.
	const allowance = 100 * time.Millisecond
	if remaining := f.gotDeadline.Sub(start); remaining <= 0 || remaining > DecisionModelTimeout+allowance {
		t.Fatalf("deadline leaves %v, want no more than %v (S5)", remaining, DecisionModelTimeout)
	}
	if DecisionModelTimeout != 2*time.Second {
		t.Fatalf("DecisionModelTimeout = %v, want 2s (S5)", DecisionModelTimeout)
	}
}

func TestDecisionSuggesterSendsOnlyTheTextAndTheCategoryCopy(t *testing.T) {
	cfg := decisionConfig()
	f := &fakeDecisionModel{answer: DecisionChoiceAnswer{Choice: "1", Probabilities: map[string]float64{"1": 0.9}}}
	const text = "The laptop screen flickers when I open the lid."
	if _, err := NewDecisionCategorySuggester(f).Suggest(context.Background(), cfg, text, decisionCategories()); err != nil {
		t.Fatalf("Suggest: %v", err)
	}
	if f.gotConfig != cfg {
		t.Fatalf("model config = %+v, want the one passed in", f.gotConfig)
	}
	if f.gotRequest.Input != text {
		t.Fatalf("Input = %q, want exactly the supplied text", f.gotRequest.Input)
	}
	want := []DecisionChoice{
		{Value: "1", Description: "Hardware: Laptops, screens, and printers"},
		{Value: "2", Description: "Access: Accounts and passwords"},
		{Value: "3", Description: "Network: Wi-Fi and VPN"},
		{Value: "4", Description: "Billing: Invoices"},
		{Value: "none", Description: "None of these categories fits the request."},
	}
	if len(f.gotRequest.Choices) != len(want) {
		t.Fatalf("Choices = %+v, want %d entries", f.gotRequest.Choices, len(want))
	}
	for i, c := range f.gotRequest.Choices {
		if c != want[i] {
			t.Fatalf("choice %d = %+v, want %+v", i, c, want[i])
		}
	}
}

func TestDecisionErrorsNeverFormatTheKey(t *testing.T) {
	f := &fakeDecisionModel{err: errors.New("the model is down")}
	_, err := NewDecisionCategorySuggester(f).Suggest(context.Background(), decisionConfig(), "anything", decisionCategories())
	if err == nil {
		t.Fatal("Suggest succeeded although the model failed")
	}
	for _, verb := range []string{"%v", "%+v", "%#v", "%s"} {
		if got := fmt.Sprintf(verb, err); strings.Contains(got, "sk-test-key") {
			t.Errorf("%s leaks the api key: %s", verb, got)
		}
	}
}

// TestDecisionSuggesterReportsThatNoCategoryFits pins the "none" answer: the
// model says none of the offered categories fits, which is an answer and not a
// failure, so it is reported apart from ErrDecisionUnavailable and the caller
// suggests nothing instead of asking the local engine.
func TestDecisionSuggesterReportsThatNoCategoryFits(t *testing.T) {
	f := &fakeDecisionModel{answer: DecisionChoiceAnswer{Choice: "none", Probabilities: map[string]float64{"none": 0.99}}}
	got, err := NewDecisionCategorySuggester(f).Suggest(context.Background(), decisionConfig(), "anything", decisionCategories())
	if !errors.Is(err, ErrDecisionNoCategoryFits) {
		t.Fatalf("Suggest error = %v, want ErrDecisionNoCategoryFits", err)
	}
	if errors.Is(err, ErrDecisionUnavailable) {
		t.Fatalf("Suggest error = %v, must not read as unavailable (that would fall back)", err)
	}
	if len(got) != 0 {
		t.Fatalf("Suggest = %+v, want no suggestion", got)
	}
}
