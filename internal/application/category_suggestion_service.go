package application

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// Category suggestions, the composition (issue #416, S1/S5/S8): one service
// answers "which categories fit what this requester wrote" for both creation
// screens and for the Settings card's Try it box, and it is the only place that
// decides which engine runs.
//
// The engines below it stay independent: LocalCategorySuggester owns the local
// algorithm's thresholds and DecisionCategorySuggester owns the decision
// engine's (S8), neither ever reads the other's numbers. What lives here is the
// choice between them, the fallback S5 requires, and the two facts a caller
// needs to know about an answer — is the requester going to see it, and did the
// decision model fail.

// CategorySuggestionInputRunes bounds the text any engine reads, in runes. A
// description is a paragraph; this is generous for one and still keeps a
// pathological paste from becoming an expensive index query or an oversized
// model request.
const CategorySuggestionInputRunes = 4000

// SuggestedCategory is one suggestion as a surface renders it: the category, the
// words the engine matched, and how likely the suggestion is on a 0..1 scale.
type SuggestedCategory struct {
	// CategoryID is always one of the offered categories.
	CategoryID int64
	// MatchedWords are the normalized words the text matched for this category,
	// in the order they were written. They are what the two-step catalog shows
	// under "Matches". Empty when nothing could be attributed.
	MatchedWords []string
	// Confidence is 0..1. The local engine reports the share of the suggestions'
	// scores this category carries, so a single clear match reads 1; the decision
	// engine reports the probability the model gave the choice.
	Confidence float64
}

// SuggestInput is one requester's text for the suggestion engines.
//
// The single-page form supplies both fields; the two-step catalog's search box
// supplies its query as the description and no title, because that box is the
// only text the requester wrote there.
type SuggestInput struct {
	Title       string
	Description string
	// Offers is the set of categories the requester may choose and that can run:
	// the same offer set the pickers and the catalog render, without the
	// categories marked unavailable. A category outside it is never suggested.
	Offers []DecisionCategory
}

// CategorySuggestionService answers category suggestions for one text.
type CategorySuggestionService struct {
	settings *CategorySuggestionSettingsService
	local    *LocalCategorySuggester
	decision *DecisionCategorySuggester
}

// NewCategorySuggestionService wires the composition over the settings use case
// and the two engines.
func NewCategorySuggestionService(settings *CategorySuggestionSettingsService, local *LocalCategorySuggester, decision *DecisionCategorySuggester) *CategorySuggestionService {
	return &CategorySuggestionService{settings: settings, local: local, decision: decision}
}

// Enabled reports whether a creation surface should render its suggestion
// containers at all (S7).
//
// It answers true for the decision engine even when its stored key cannot be
// read, because that request is answered by the local engine (S5) and the
// requester still sees suggestions. Any other failure answers false: a surface
// that cannot know the engine renders as it does today, which is the safe
// direction (S3).
func (s *CategorySuggestionService) Enabled(ctx context.Context) bool {
	if s == nil || s.settings == nil {
		return false
	}
	cfg, err := s.settings.Config(ctx)
	if err != nil {
		return errors.Is(err, ErrCategorySuggestionKeyUnreadable)
	}
	return cfg.Engine != CategorySuggestionOff
}

// Suggest answers the suggestions for one requester text, best first, at most
// three, and only from in.Offers.
//
// The engine is the configured one. With the feature off nothing is suggested
// and no engine is called at all. With the decision engine, anything that keeps
// the model from answering — a transport failure, the two-second bound, a
// refusal, a doubt, an unreadable key — answers the LOCAL engine's suggestions
// instead, and the requester notices nothing (S5). A model that answers that no
// category fits suggests nothing at all.
func (s *CategorySuggestionService) Suggest(ctx context.Context, in SuggestInput) ([]SuggestedCategory, error) {
	if s == nil || s.settings == nil || len(in.Offers) == 0 {
		return nil, nil
	}
	text := SuggestionText(in)
	cfg, err := s.settings.Config(ctx)
	if err != nil {
		if errors.Is(err, ErrCategorySuggestionKeyUnreadable) {
			return s.requesterSuggestions(ctx, text, in.Offers)
		}
		return nil, err
	}
	if cfg.Engine == CategorySuggestionOff {
		return nil, nil
	}
	if cfg.Engine == CategorySuggestionModel {
		if suggestions, ok := s.modelSuggestions(ctx, cfg, text, in.Offers); ok {
			return suggestions, nil
		}
	}
	return s.requesterSuggestions(ctx, text, in.Offers)
}

// requesterSuggestions is the local engine's answer as a requester sees it:
// nothing at all unless the engine is confident, because S3 says the requester
// chooses and an unsure guess is worse than the catalog.
func (s *CategorySuggestionService) requesterSuggestions(ctx context.Context, text string, offers []DecisionCategory) ([]SuggestedCategory, error) {
	ranked, confident, err := s.localRank(ctx, text, offers)
	if err != nil {
		return nil, err
	}
	if !confident {
		return nil, nil
	}
	return ranked, nil
}

// modelSuggestions asks the decision engine. ok is false whenever the caller
// must fall back to the local engine: a failure, a refusal, the two-second
// bound or a doubt (S5). A "none of these" answer is not one of them: the model
// answered, so ok is true with no suggestion and the requester chooses from the
// catalog.
//
// The matched words of a model suggestion are the ones the local engine finds
// for the same category: the model reports probabilities, not the words that
// led to them, and the two-step catalog shows words.
func (s *CategorySuggestionService) modelSuggestions(ctx context.Context, cfg CategorySuggestionConfig, text string, offers []DecisionCategory) ([]SuggestedCategory, bool) {
	decided, err := s.decision.Suggest(ctx, DecisionModelConfig{
		EndpointURL: cfg.EndpointURL,
		Model:       cfg.Model,
		APIKey:      cfg.APIKey,
	}, text, offers)
	if errors.Is(err, ErrDecisionNoCategoryFits) {
		return nil, true
	}
	if err != nil {
		return nil, false
	}
	words := matchedWordsByCategory(ctx, s.local, text, offers)
	out := make([]SuggestedCategory, 0, len(decided))
	for _, suggestion := range decided {
		out = append(out, SuggestedCategory{
			CategoryID:   suggestion.CategoryID,
			MatchedWords: words[suggestion.CategoryID],
			Confidence:   suggestion.Probability,
		})
	}
	return out, true
}

// matchedWordsByCategory attributes the local engine's matched words to category
// ids, whatever the local engine's confidence: this asks what the index found,
// not whether it would have suggested it.
func matchedWordsByCategory(ctx context.Context, local *LocalCategorySuggester, text string, offers []DecisionCategory) map[int64][]string {
	words := make(map[int64][]string)
	if local == nil {
		return words
	}
	// A failed attribution costs the words, never the suggestion: the model
	// already answered, and S5's fallback is about the answer, not about this.
	ranked, _, err := local.Rank(ctx, text, offerIDs(offers))
	if err != nil {
		return words
	}
	for _, candidate := range ranked {
		words[candidate.CategoryID] = candidate.MatchedWords
	}
	return words
}

// localRank maps the local engine's candidates to suggestions. Each one carries
// the share of the ranking's scores it accounts for, so the shares of one answer
// add up to 100 percent and a single clear match reads as a certainty.
func (s *CategorySuggestionService) localRank(ctx context.Context, text string, offers []DecisionCategory) ([]SuggestedCategory, bool, error) {
	if s.local == nil {
		return nil, false, nil
	}
	ranked, confident, err := s.local.Rank(ctx, text, offerIDs(offers))
	if err != nil {
		return nil, false, err
	}
	if len(ranked) == 0 {
		return nil, false, nil
	}
	total := 0.0
	for _, candidate := range ranked {
		if candidate.Score > 0 {
			total += candidate.Score
		}
	}
	out := make([]SuggestedCategory, 0, len(ranked))
	for _, candidate := range ranked {
		suggestion := SuggestedCategory{CategoryID: candidate.CategoryID, MatchedWords: candidate.MatchedWords}
		if total > 0 {
			suggestion.Confidence = candidate.Score / total
		}
		out = append(out, suggestion)
	}
	return out, confident, nil
}

// offerIDs is the allowed set the local index query is restricted to.
func offerIDs(offers []DecisionCategory) []int64 {
	ids := make([]int64, 0, len(offers))
	for _, offer := range offers {
		ids = append(ids, offer.ID)
	}
	return ids
}

// SuggestionText is the ONE text the engines read: what the requester wrote
// about the problem, bounded to CategorySuggestionInputRunes.
//
// Nothing else about the requester is ever part of it. The name, the email, the
// attachments and the ticket history are not here and must never be: with the
// decision engine this text leaves the instance (S5).
func SuggestionText(in SuggestInput) string {
	text := strings.TrimSpace(in.Title)
	description := strings.TrimSpace(in.Description)
	if description != "" {
		if text != "" {
			text += "\n"
		}
		text += description
	}
	return boundSuggestionText(text)
}

// boundSuggestionText truncates text to the input bound, on a rune boundary so a
// multi-byte character is never cut in half.
func boundSuggestionText(text string) string {
	if utf8.RuneCountInString(text) <= CategorySuggestionInputRunes {
		return text
	}
	runes := []rune(text)
	return string(runes[:CategorySuggestionInputRunes])
}

// ---------------------------------------------------------------------------
// The Settings card's Try it box (S6)
// ---------------------------------------------------------------------------

// TryInput is one submission of the Settings card's Try it box: the engine
// configuration the form shows — not yet saved — and one sample text.
type TryInput struct {
	Settings CategorySuggestionSettingsInput
	Text     string
	Offers   []DecisionCategory
}

// TriedCategory is one row of the Try it result.
type TriedCategory struct {
	CategoryID int64
	// Name is the category's name as its catalogue row spells it.
	Name string
	// Confidence is the same 0..1 figure SuggestedCategory carries.
	Confidence float64
}

// TryResult is what the Try it box reports: the ranked candidates, whether a
// requester would actually see them, and whether the decision model failed and
// the local engine answered instead.
type TryResult struct {
	Candidates []TriedCategory
	Confident  bool
	FellBack   bool
}

// Try runs the submitted configuration without saving it and reports what it
// found. It is gated on CapManageSettings, the capability that owns the card.
//
// A blank API key means the stored one: the form never carries a key back, so an
// administrator who only changed the endpoint tests the key they already saved.
// Every reason the model cannot answer — a blank endpoint or model, an
// unreadable stored key, a transport failure, the two-second bound, a refusal —
// reports the local engine's answer with FellBack set, which is exactly what a
// requester would have got (S5). A "none of these" answer reports no candidate
// and no fallback, because the requester would see nothing.
func (s *CategorySuggestionService) Try(ctx context.Context, actor domain.User, in TryInput) (TryResult, error) {
	if err := requireManageSettings(actor, "category suggestion settings are not permitted"); err != nil {
		return TryResult{}, err
	}
	text := boundSuggestionText(strings.TrimSpace(in.Text))
	if in.Settings.Engine != CategorySuggestionModel {
		return s.reportLocal(ctx, text, in.Offers), nil
	}
	cfg, ok := s.submittedModelConfig(ctx, in.Settings)
	if !ok {
		result := s.reportLocal(ctx, text, in.Offers)
		result.FellBack = true
		return result, nil
	}
	suggestions, modelAnswered := s.modelSuggestions(ctx, cfg, text, in.Offers)
	if modelAnswered {
		return TryResult{Candidates: triedCategories(suggestions, in.Offers), Confident: true}, nil
	}
	result := s.reportLocal(ctx, text, in.Offers)
	result.FellBack = true
	return result, nil
}

// reportLocal is the local engine's answer for the Try it box: the ranking even
// when it is not confident, because the card's job is to show the administrator
// what the engine found and then say whether requesters would see it.
func (s *CategorySuggestionService) reportLocal(ctx context.Context, text string, offers []DecisionCategory) TryResult {
	suggestions, confident, err := s.localRank(ctx, text, offers)
	if err != nil {
		// The Try it box reports an empty result rather than an error: an index
		// that cannot answer is a configuration fact the administrator sees as
		// "no category matched", and the save path owns reporting failures.
		return TryResult{}
	}
	return TryResult{Candidates: triedCategories(suggestions, offers), Confident: confident}
}

// submittedModelConfig resolves the configuration the form would run.
//
// The endpoint and the model are what the administrator just typed; the key is
// the submitted one, or the stored one when the field is empty. A configuration
// that cannot run at all — a blank field, no stored key to fall back on, a
// stored key that does not open — reports ok false.
func (s *CategorySuggestionService) submittedModelConfig(ctx context.Context, in CategorySuggestionSettingsInput) (CategorySuggestionConfig, bool) {
	cfg := CategorySuggestionConfig{
		Engine:      CategorySuggestionModel,
		EndpointURL: strings.TrimRight(strings.TrimSpace(in.EndpointURL), "/"),
		Model:       strings.TrimSpace(in.Model),
		APIKey:      strings.TrimSpace(in.APIKey),
	}
	if cfg.EndpointURL == "" || cfg.Model == "" {
		return CategorySuggestionConfig{}, false
	}
	if cfg.APIKey == "" {
		// The stored configuration is the only thing that holds a sealed key, and
		// it is also the only thing that can open it: Config answers the stored
		// engine, and it unseals the key only when that engine is the decision
		// model. A key that cannot be opened leaves the model unable to answer.
		stored, err := s.settings.Config(ctx)
		if err != nil || stored.Engine != CategorySuggestionModel {
			return CategorySuggestionConfig{}, false
		}
		cfg.APIKey = stored.APIKey
	}
	return cfg, true
}

// triedCategories names the candidates for the Try it rows. A candidate is
// always one of the offers, because every engine answers from that set, but the
// name is looked up rather than assumed so a row can never print a raw id.
func triedCategories(suggestions []SuggestedCategory, offers []DecisionCategory) []TriedCategory {
	names := make(map[int64]string, len(offers))
	for _, offer := range offers {
		names[offer.ID] = offer.Name
	}
	out := make([]TriedCategory, 0, len(suggestions))
	for _, suggestion := range suggestions {
		name, ok := names[suggestion.CategoryID]
		if !ok {
			continue
		}
		out = append(out, TriedCategory{CategoryID: suggestion.CategoryID, Name: name, Confidence: suggestion.Confidence})
	}
	return out
}
