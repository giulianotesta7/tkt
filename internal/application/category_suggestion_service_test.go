package application

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// Category suggestion composition (issue #416, S1/S3/S5/S8): one service decides
// which engine answers, the decision engine's failures answer the local engine
// instead, and the Settings card's Try it box reports a ranking it did not save.
// The engines below are the real ones over a fake index and a fake decision
// model, so every case states the composition's own rule.

// fakeSuggestionIndex is the local engine's storage port: it answers canned
// candidates for any query and records what it was asked, so a test can prove
// that an engine was called — or that it was never called at all.
type fakeSuggestionIndex struct {
	candidates []CategorySuggestion
	err        error
	texts      []string
	allowed    [][]int64
}

func (f *fakeSuggestionIndex) SuggestCandidates(_ context.Context, text string, allowed []int64) ([]CategorySuggestion, error) {
	f.texts = append(f.texts, text)
	f.allowed = append(f.allowed, append([]int64(nil), allowed...))
	if f.err != nil {
		return nil, f.err
	}
	return append([]CategorySuggestion(nil), f.candidates...), nil
}

// suggestionOffers is the offer set the creation surfaces pass: the categories a
// requester may choose and that can run.
func suggestionOffers() []DecisionCategory {
	return []DecisionCategory{
		{ID: 4, Name: "Printers", Description: "Printers, toner and paper"},
		{ID: 7, Name: "Network", Description: "Wi-Fi and cable"},
	}
}

// suggestionServiceFixture wires the composition over the real engines: the
// settings use case over an in-memory store, the local engine over a fake index,
// and the decision engine over a fake model.
type suggestionServiceFixture struct {
	svc      *CategorySuggestionService
	settings suggestionSettingsFixture
	index    *fakeSuggestionIndex
	model    *fakeDecisionModel
}

func newSuggestionServiceFixture() *suggestionServiceFixture {
	f := &suggestionServiceFixture{
		settings: newSuggestionSettingsFixture(),
		index:    &fakeSuggestionIndex{},
		model:    &fakeDecisionModel{},
	}
	f.svc = NewCategorySuggestionService(f.settings.svc, NewLocalCategorySuggester(f.index), NewDecisionCategorySuggester(f.model))
	return f
}

// storedLocalConfig turns the local engine on, the state a server runs after an
// administrator chose "On this server".
func (f *suggestionServiceFixture) storedLocalConfig() {
	f.settings.store.stored = CategorySuggestionSettings{Engine: CategorySuggestionLocal}
}

// storedModelConfig saves a complete decision-model configuration.
func (f *suggestionServiceFixture) storedModelConfig() {
	f.settings.store.stored = CategorySuggestionSettings{
		Engine:       CategorySuggestionModel,
		EndpointURL:  "https://api.example.com/v1",
		Model:        "gpt-4.1-mini",
		SealedAPIKey: "sealed(" + reverse("sk-stored") + ")",
	}
}

func TestSuggestWithTheFeatureOffAnswersNothingAndCallsNoEngine(t *testing.T) {
	f := newSuggestionServiceFixture()

	got, err := f.svc.Suggest(context.Background(), SuggestInput{
		Title:  "la impresora no imprime",
		Offers: suggestionOffers(),
	})
	if err != nil {
		t.Fatalf("Suggest: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("Suggest = %+v, want nothing with the feature off", got)
	}
	if len(f.index.texts) != 0 {
		t.Errorf("the local index was queried %v, want no engine call at all", f.index.texts)
	}
	if f.model.calls != 0 {
		t.Errorf("the decision model was called %d times, want no engine call at all", f.model.calls)
	}
	if f.svc.Enabled(context.Background()) {
		t.Error("Enabled = true with the feature off, want false")
	}
}

func TestSuggestWithTheLocalEngineAnswersTheConfidentCategory(t *testing.T) {
	f := newSuggestionServiceFixture()
	f.storedLocalConfig()
	f.index.candidates = []CategorySuggestion{{CategoryID: 4, Score: 6, MatchedWords: []string{"impresora", "toner"}}}

	got, err := f.svc.Suggest(context.Background(), SuggestInput{
		Title:       "la impresora no imprime",
		Description: "sale una hoja en blanco",
		Offers:      suggestionOffers(),
	})
	if err != nil {
		t.Fatalf("Suggest: %v", err)
	}
	if len(got) != 1 || got[0].CategoryID != 4 {
		t.Fatalf("Suggest = %+v, want the one confident category", got)
	}
	if got[0].Confidence != 1 {
		t.Errorf("Confidence = %v, want 1 for the only suggestion", got[0].Confidence)
	}
	if fmt.Sprint(got[0].MatchedWords) != fmt.Sprint([]string{"impresora", "toner"}) {
		t.Errorf("MatchedWords = %v, want the words the index attributed", got[0].MatchedWords)
	}
	if !f.svc.Enabled(context.Background()) {
		t.Error("Enabled = false with an engine configured, want true")
	}
	if f.model.calls != 0 {
		t.Errorf("the decision model was called %d times, want the local engine only", f.model.calls)
	}
}

// TestSuggestSplitsConfidenceAcrossTheSuggestions pins the percentage the Settings
// card shows: each suggestion carries the share of the ranking's scores it
// accounts for, so the shares of one answer add up to the whole.
func TestSuggestSplitsConfidenceAcrossTheSuggestions(t *testing.T) {
	f := newSuggestionServiceFixture()
	f.storedLocalConfig()
	f.index.candidates = []CategorySuggestion{
		{CategoryID: 4, Score: 6},
		{CategoryID: 7, Score: 3},
	}

	got, err := f.svc.Suggest(context.Background(), SuggestInput{Title: "impresora y wifi", Offers: suggestionOffers()})
	if err != nil {
		t.Fatalf("Suggest: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Suggest = %+v, want two suggestions", got)
	}
	if got[0].Confidence < 0.66 || got[0].Confidence > 0.67 {
		t.Errorf("first Confidence = %v, want two thirds", got[0].Confidence)
	}
	total := got[0].Confidence + got[1].Confidence
	if total < 0.999 || total > 1.001 {
		t.Errorf("confidence shares add up to %v, want 1", total)
	}
}

// TestSuggestSendsOnlyTheRequesterTextAndTheCategoryCopy is S5's boundary as a
// test: the decision model reads the title, the description, each offered
// category's name and description, and nothing else.
func TestSuggestSendsOnlyTheRequesterTextAndTheCategoryCopy(t *testing.T) {
	f := newSuggestionServiceFixture()
	f.storedModelConfig()
	f.index.candidates = []CategorySuggestion{{CategoryID: 4, Score: 6, MatchedWords: []string{"impresora"}}}
	f.model.answer = DecisionChoiceAnswer{
		Choice:        "4",
		Probabilities: map[string]float64{"4": 0.7, "7": 0.2},
		Confidence:    0.7,
	}

	got, err := f.svc.Suggest(context.Background(), SuggestInput{
		Title:       "la impresora no imprime",
		Description: "sale una hoja en blanco",
		Offers:      suggestionOffers(),
	})
	if err != nil {
		t.Fatalf("Suggest: %v", err)
	}
	if f.model.calls != 1 {
		t.Fatalf("the decision model was called %d times, want exactly 1", f.model.calls)
	}
	if want := "la impresora no imprime\nsale una hoja en blanco"; f.model.gotRequest.Input != want {
		t.Errorf("model input = %q, want the title and description only %q", f.model.gotRequest.Input, want)
	}
	choices := map[string]string{}
	for _, choice := range f.model.gotRequest.Choices {
		choices[choice.Value] = choice.Description
	}
	if len(choices) != 3 || !strings.Contains(choices["4"], "Printers") || !strings.Contains(choices["7"], "Network") {
		t.Errorf("model choices = %v, want one per offered category plus none", choices)
	}
	if f.model.gotConfig.APIKey != "sk-stored" {
		t.Errorf("the model received the key %q, want the unsealed stored one", f.model.gotConfig.APIKey)
	}
	if len(got) != 2 || got[0].CategoryID != 4 {
		t.Fatalf("Suggest = %+v, want the model's ranking", got)
	}
	if got[0].Confidence != 0.7 {
		t.Errorf("Confidence = %v, want the model's probability", got[0].Confidence)
	}
	if fmt.Sprint(got[0].MatchedWords) != fmt.Sprint([]string{"impresora"}) {
		t.Errorf("MatchedWords = %v, want the local engine's words for the same category", got[0].MatchedWords)
	}
	if len(got[1].MatchedWords) != 0 {
		t.Errorf("second MatchedWords = %v, want none when the local engine found none", got[1].MatchedWords)
	}
}

// TestSuggestFallsBackToTheLocalEngineWhenTheModelFails covers every way the
// decision engine can let the requester down: S5 says the answer then comes from
// the local engine and the requester notices nothing.
func TestSuggestFallsBackToTheLocalEngineWhenTheModelFails(t *testing.T) {
	cases := []struct {
		name   string
		answer DecisionChoiceAnswer
		err    error
	}{
		{name: "transport failure", err: errors.New("dial tcp: connection refused")},
		{name: "refusal", answer: DecisionChoiceAnswer{Refused: true}},
		{name: "no category fits", answer: DecisionChoiceAnswer{Choice: "none", Confidence: 0.9}},
		{name: "below the decision threshold", answer: DecisionChoiceAnswer{Choice: "4", Probabilities: map[string]float64{"4": 0.3, "7": 0.2}, Confidence: 0.3}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newSuggestionServiceFixture()
			f.storedModelConfig()
			f.model.answer, f.model.err = tc.answer, tc.err
			f.index.candidates = []CategorySuggestion{{CategoryID: 7, Score: 5, MatchedWords: []string{"wifi"}}}

			got, err := f.svc.Suggest(context.Background(), SuggestInput{Title: "no anda el wifi", Offers: suggestionOffers()})
			if err != nil {
				t.Fatalf("Suggest: %v", err)
			}
			if f.model.calls != 1 {
				t.Fatalf("the decision model was called %d times, want the attempt", f.model.calls)
			}
			if len(got) != 1 || got[0].CategoryID != 7 {
				t.Fatalf("Suggest = %+v, want the local engine's suggestion", got)
			}
			if got[0].Confidence != 1 {
				t.Errorf("Confidence = %v, want the local share, not the model's", got[0].Confidence)
			}
		})
	}
}

// TestSuggestFallsBackWhenTheStoredKeyCannotBeRead covers a database restored
// without its key file: the engine stays on, the local engine answers, and the
// creation surfaces still render their suggestion containers.
func TestSuggestFallsBackWhenTheStoredKeyCannotBeRead(t *testing.T) {
	f := newSuggestionServiceFixture()
	f.storedModelConfig()
	f.settings.sealer.openErr = errors.New("cipher: message authentication failed")
	f.index.candidates = []CategorySuggestion{{CategoryID: 4, Score: 6}}

	if !f.svc.Enabled(context.Background()) {
		t.Fatal("Enabled = false with the decision engine stored, want true: the local engine answers")
	}
	got, err := f.svc.Suggest(context.Background(), SuggestInput{Title: "impresora rota", Offers: suggestionOffers()})
	if err != nil {
		t.Fatalf("Suggest: %v", err)
	}
	if len(got) != 1 || got[0].CategoryID != 4 {
		t.Fatalf("Suggest = %+v, want the local engine's suggestion", got)
	}
	if f.model.calls != 0 {
		t.Errorf("the decision model was called %d times, want none without a key", f.model.calls)
	}
}

// TestSuggestNeverOffersACategoryOutsideTheOfferSet is S3's and S4's boundary:
// only the categories the requester may choose and that can run are ever
// suggested, whatever an engine answers.
func TestSuggestNeverOffersACategoryOutsideTheOfferSet(t *testing.T) {
	f := newSuggestionServiceFixture()
	f.storedLocalConfig()
	f.index.candidates = []CategorySuggestion{
		{CategoryID: 99, Score: 9},
		{CategoryID: 4, Score: 1},
	}

	got, err := f.svc.Suggest(context.Background(), SuggestInput{Title: "impresora", Offers: suggestionOffers()})
	if err != nil {
		t.Fatalf("Suggest: %v", err)
	}
	for _, suggestion := range got {
		if suggestion.CategoryID == 99 {
			t.Fatalf("Suggest = %+v, want no category outside the offer set", got)
		}
	}
	if fmt.Sprint(f.index.allowed[0]) != fmt.Sprint([]int64{4, 7}) {
		t.Errorf("the index was allowed %v, want the offer set", f.index.allowed[0])
	}
}

// TestSuggestBoundsTheTextBeforeAnyEngine keeps a pathological paste from
// becoming an expensive query or an oversized request.
func TestSuggestBoundsTheTextBeforeAnyEngine(t *testing.T) {
	f := newSuggestionServiceFixture()
	f.storedLocalConfig()
	text := strings.Repeat("impresora ", 2000)

	if _, err := f.svc.Suggest(context.Background(), SuggestInput{Description: text, Offers: suggestionOffers()}); err != nil {
		t.Fatalf("Suggest: %v", err)
	}
	if len(f.index.texts) != 1 {
		t.Fatalf("the index was queried %d times, want once", len(f.index.texts))
	}
	if got := len([]rune(f.index.texts[0])); got != CategorySuggestionInputRunes {
		t.Errorf("the engine read %d runes, want the %d-rune bound", got, CategorySuggestionInputRunes)
	}
}

// ---------------------------------------------------------------------------
// Try it
// ---------------------------------------------------------------------------

func TestTryReportsTheRankingEvenBelowTheLocalThreshold(t *testing.T) {
	f := newSuggestionServiceFixture()
	f.index.candidates = []CategorySuggestion{{CategoryID: 4, Score: LocalSuggestionMinScore / 2}}

	result, err := f.svc.Try(context.Background(), suggestionAdmin(), TryInput{
		Settings: CategorySuggestionSettingsInput{Engine: CategorySuggestionLocal},
		Text:     "impresora",
		Offers:   suggestionOffers(),
	})
	if err != nil {
		t.Fatalf("Try: %v", err)
	}
	if len(result.Candidates) != 1 || result.Candidates[0].Name != "Printers" {
		t.Fatalf("Try = %+v, want the ranked candidate with its name", result)
	}
	if result.Candidates[0].Confidence != 1 {
		t.Errorf("Confidence = %v, want the share of the ranking", result.Candidates[0].Confidence)
	}
	if result.Confident {
		t.Error("Confident = true below the local threshold, want false")
	}
	if result.FellBack {
		t.Error("FellBack = true for the local engine, want false")
	}
	if f.settings.store.setCalls != 0 {
		t.Errorf("Try wrote the store %d times, want nothing saved", f.settings.store.setCalls)
	}
}

func TestTryRunsTheSubmittedDecisionConfigurationWithoutSavingIt(t *testing.T) {
	f := newSuggestionServiceFixture()
	f.settings.store.stored = CategorySuggestionSettings{Engine: CategorySuggestionLocal}
	f.model.answer = DecisionChoiceAnswer{Choice: "7", Probabilities: map[string]float64{"7": 0.8}, Confidence: 0.8}

	result, err := f.svc.Try(context.Background(), suggestionAdmin(), TryInput{
		Settings: CategorySuggestionSettingsInput{
			Engine:      CategorySuggestionModel,
			EndpointURL: "https://api.example.com/v1",
			Model:       "gpt-4.1-mini",
			APIKey:      "sk-typed",
		},
		Text:   "no anda el wifi",
		Offers: suggestionOffers(),
	})
	if err != nil {
		t.Fatalf("Try: %v", err)
	}
	if f.model.gotConfig.EndpointURL != "https://api.example.com/v1" || f.model.gotConfig.APIKey != "sk-typed" {
		t.Errorf("the model received %+v, want the submitted endpoint and key", f.model.gotConfig)
	}
	if len(result.Candidates) != 1 || result.Candidates[0].Name != "Network" || result.Candidates[0].Confidence != 0.8 {
		t.Fatalf("Try = %+v, want the model's candidate and probability", result)
	}
	if !result.Confident || result.FellBack {
		t.Errorf("Try = %+v, want a confident model answer", result)
	}
	if f.settings.store.setCalls != 0 || f.settings.store.stored.Engine != CategorySuggestionLocal {
		t.Error("Try saved the submitted configuration, want nothing written")
	}
}

// TestTryKeepsTheStoredKeyWhenTheFieldIsBlank is the form's contract: the key is
// never rendered back, so an administrator who changes only the endpoint tests
// the key they already saved.
func TestTryKeepsTheStoredKeyWhenTheFieldIsBlank(t *testing.T) {
	f := newSuggestionServiceFixture()
	f.storedModelConfig()
	f.model.answer = DecisionChoiceAnswer{Choice: "4", Probabilities: map[string]float64{"4": 0.9}, Confidence: 0.9}

	if _, err := f.svc.Try(context.Background(), suggestionAdmin(), TryInput{
		Settings: CategorySuggestionSettingsInput{
			Engine:      CategorySuggestionModel,
			EndpointURL: "https://api.example.com/v2",
			Model:       "gpt-4.1-mini",
		},
		Text:   "impresora",
		Offers: suggestionOffers(),
	}); err != nil {
		t.Fatalf("Try: %v", err)
	}
	if f.model.gotConfig.APIKey != "sk-stored" {
		t.Errorf("the model received the key %q, want the stored one", f.model.gotConfig.APIKey)
	}
}

// TestTryReportsTheLocalAnswerWhenTheModelCannotAnswer collects the reasons the
// Try it box falls back, including the ones only the form can produce.
func TestTryReportsTheLocalAnswerWhenTheModelCannotAnswer(t *testing.T) {
	cases := []struct {
		name   string
		stored func(*suggestionServiceFixture)
		input  CategorySuggestionSettingsInput
	}{
		{
			name:   "blank endpoint",
			stored: func(f *suggestionServiceFixture) { f.storedModelConfig() },
			input:  CategorySuggestionSettingsInput{Engine: CategorySuggestionModel, Model: "gpt-4.1-mini", APIKey: "sk-typed"},
		},
		{
			name: "blank key with nothing stored",
			stored: func(f *suggestionServiceFixture) {
				f.settings.store.stored = CategorySuggestionSettings{Engine: CategorySuggestionLocal}
			},
			input: CategorySuggestionSettingsInput{Engine: CategorySuggestionModel, EndpointURL: "https://api.example.com/v1", Model: "gpt-4.1-mini"},
		},
		{
			name: "stored key that does not open",
			stored: func(f *suggestionServiceFixture) {
				f.storedModelConfig()
				f.settings.sealer.openErr = errors.New("cipher: message authentication failed")
			},
			input: CategorySuggestionSettingsInput{Engine: CategorySuggestionModel, EndpointURL: "https://api.example.com/v1", Model: "gpt-4.1-mini"},
		},
		{
			name: "model failure",
			stored: func(f *suggestionServiceFixture) {
				f.storedModelConfig()
				f.model.err = errors.New("dial tcp: connection refused")
			},
			input: CategorySuggestionSettingsInput{Engine: CategorySuggestionModel, EndpointURL: "https://api.example.com/v1", Model: "gpt-4.1-mini", APIKey: "sk-typed"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newSuggestionServiceFixture()
			tc.stored(f)
			f.index.candidates = []CategorySuggestion{{CategoryID: 4, Score: 6}}

			result, err := f.svc.Try(context.Background(), suggestionAdmin(), TryInput{Settings: tc.input, Text: "impresora", Offers: suggestionOffers()})
			if err != nil {
				t.Fatalf("Try: %v", err)
			}
			if !result.FellBack {
				t.Fatalf("Try = %+v, want FellBack with the local answer", result)
			}
			if len(result.Candidates) != 1 || result.Candidates[0].Name != "Printers" {
				t.Errorf("Try = %+v, want the local engine's candidate", result)
			}
			if !result.Confident {
				t.Error("Confident = false, want the local engine's own verdict")
			}
		})
	}
}

func TestTryIsGatedOnCapManageSettings(t *testing.T) {
	for _, role := range []domain.Role{domain.RoleUser, domain.RoleAgent} {
		f := newSuggestionServiceFixture()
		f.index.candidates = []CategorySuggestion{{CategoryID: 4, Score: 6}}
		var forbidden *domain.ForbiddenError
		_, err := f.svc.Try(context.Background(), domain.User{ID: 3, Role: role, Active: true}, TryInput{
			Settings: CategorySuggestionSettingsInput{Engine: CategorySuggestionLocal},
			Text:     "impresora",
			Offers:   suggestionOffers(),
		})
		if !errors.As(err, &forbidden) {
			t.Errorf("%s: Try = %v, want ForbiddenError", role, err)
		}
		if len(f.index.texts) != 0 {
			t.Errorf("%s: the engine ran for an actor who may not test it", role)
		}
	}
	for _, actor := range []domain.User{suggestionAdmin(), emailRoot()} {
		f := newSuggestionServiceFixture()
		if _, err := f.svc.Try(context.Background(), actor, TryInput{
			Settings: CategorySuggestionSettingsInput{Engine: CategorySuggestionLocal},
			Text:     "impresora",
			Offers:   suggestionOffers(),
		}); err != nil {
			t.Errorf("%s: Try = %v, want permission", actor.Role, err)
		}
	}
}
