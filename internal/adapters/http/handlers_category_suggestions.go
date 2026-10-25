package httpadapter

import (
	"math"
	"net/http"
	"net/url"
	"strings"

	"github.com/giulianotesta7/tkt/internal/application"
)

// Category suggestions over HTTP (issue #416, S2/S3/S6): the fragment both
// creation screens refresh, and the Settings card that chooses the engine.
//
// The fragment answers whatever the requester has typed and never fails on an
// engine: an engine that cannot answer produces the EMPTY fragment, which is the
// same thing an engine that found nothing produces, and the requester keeps the
// catalog they have always had (S3). The Settings routes are instance
// configuration and are gated on CapManageSettings at this boundary, exactly as
// the rest of the Tickets & SLA section is.

// The form fields of the Settings card. They are named for the card so they can
// share the section with the appearance, SLA and hold-close forms without a
// collision.
const (
	suggestionEngineField   = "suggestion_engine"
	suggestionEndpointField = "suggestion_endpoint"
	suggestionModelField    = "suggestion_model"
	suggestionAPIKeyField   = "suggestion_api_key"
	suggestionTryField      = "suggestion_try"
)

// suggestionView is one suggested category as both fragments render it.
type suggestionView struct {
	ID int64
	// Name is the category's name.
	Name string
	// Caption is the "Department · Desk" the catalog shows under every card, so
	// a suggestion reads like the catalog entry it stands for.
	Caption string
	// MatchedWords are the words the engine matched, in the order they were
	// written. The two-step catalog shows them; the chips do not.
	MatchedWords []string
}

// categorySuggestionsData is the payload both suggestion fragments read.
type categorySuggestionsData struct {
	Suggestions []suggestionView
}

// categorySuggestionMatchesShown is how many matched words a suggested tile
// lists. The catalog's caption is one line, and beyond a handful of words the
// list stops reading as an explanation.
const categorySuggestionMatchesShown = 3

// suggestionViews pairs each suggestion with the offer it came from, so a
// fragment can show the category's name and its place in the catalog without a
// second read.
func suggestionViews(suggestions []application.SuggestedCategory, offers []catalogCategoryEntry) []suggestionView {
	byID := make(map[int64]catalogCategoryEntry, len(offers))
	for _, offer := range offers {
		byID[offer.ID] = offer
	}
	out := make([]suggestionView, 0, len(suggestions))
	for _, suggestion := range suggestions {
		offer, ok := byID[suggestion.CategoryID]
		if !ok {
			continue
		}
		words := suggestion.MatchedWords
		if len(words) > categorySuggestionMatchesShown {
			words = words[:categorySuggestionMatchesShown]
		}
		out = append(out, suggestionView{
			ID:           suggestion.CategoryID,
			Name:         offer.Name,
			Caption:      catalogCaption(offer),
			MatchedWords: words,
		})
	}
	return out
}

// catalogCaption is the "Department · Desk" line a suggestion and a catalog card
// both carry.
func catalogCaption(offer catalogCategoryEntry) string {
	switch {
	case offer.DepartmentName == "":
		return offer.DeskName
	case offer.DeskName == "":
		return offer.DepartmentName
	}
	return offer.DepartmentName + " · " + offer.DeskName
}

// ---------------------------------------------------------------- the ticket side

// WithCategorySuggestions wires the suggestion composition behind the creation
// surfaces and the fragment endpoint. A handler built without it renders both
// creation screens exactly as they were before the feature (S7).
func (h *TicketHandlers) WithCategorySuggestions(suggestions *application.CategorySuggestionService) *TicketHandlers {
	h.suggestions = suggestions
	return h
}

// suggestionsEnabled reports whether a creation surface renders its suggestion
// containers (S7). With the feature off, and for a handler that was built without
// the composition, both screens render byte for byte what they rendered before.
func (h *TicketHandlers) suggestionsEnabled(r *http.Request) bool {
	return h.suggestions != nil && h.suggestions.Enabled(r.Context())
}

// suggestionEntries is the offer set a suggestion may name: the requester's own
// offer set (choosableCategories) without the published categories that cannot
// run. A category whose submit the create guard would refuse is never suggested.
func (h *TicketHandlers) suggestionEntries(r *http.Request) ([]catalogCategoryEntry, error) {
	// createOptions is the ONE place that turns the catalog into the requester's
	// offer set with its fallback for a handler built without the catalog
	// service; a second expression of it would be a second rule.
	_, offers, err := h.createOptions(r)
	if err != nil {
		return nil, err
	}
	out := make([]catalogCategoryEntry, 0, len(offers))
	for _, offer := range offers {
		if offer.Unavailable {
			continue
		}
		out = append(out, offer)
	}
	return out, nil
}

// SuggestionOffers answers the categories a suggestion may name, which is what
// the Settings card's Try it box needs to show real candidates and real
// percentages.
//
// It is the same offer set the creation screens use, never a second rule, so the
// box an administrator tests against cannot disagree with what a requester sees.
func (h *TicketHandlers) SuggestionOffers(r *http.Request) ([]application.DecisionCategory, error) {
	entries, err := h.suggestionEntries(r)
	if err != nil {
		return nil, err
	}
	return suggestionDecisionOffers(entries), nil
}

// suggestionDecisionOffers reduces the offer set to what the suggestion engines
// read: the identity and the catalogue copy, never the requester (S5).
func suggestionDecisionOffers(entries []catalogCategoryEntry) []application.DecisionCategory {
	out := make([]application.DecisionCategory, 0, len(entries))
	for _, entry := range entries {
		out = append(out, application.DecisionCategory{ID: entry.ID, Name: entry.Name, Description: entry.Description})
	}
	return out
}

// suggestCategories answers the fragment a creation surface swaps in.
//
// The engine is whatever the instance configured, and a failure answers the empty
// fragment rather than an error: the requester keeps the catalog (S3), and an
// engine that cannot answer never turns into a 500 on a page a requester is
// writing a ticket on.
func (h *TicketHandlers) suggestCategories(w http.ResponseWriter, r *http.Request) {
	values := r.URL.Query()
	mode := values.Get("mode")
	in := application.SuggestInput{Title: values.Get("title"), Description: values.Get("description")}
	fragment := "category_suggestion_chips"
	if mode == application.TicketCreationTwoStep {
		// The two-step search box is the only text the requester wrote there, so
		// it is the description and there is no title.
		in = application.SuggestInput{Description: values.Get("q")}
		fragment = "category_suggestion_tiles"
	}
	data := categorySuggestionsData{}
	if h.suggestions != nil {
		if entries, err := h.suggestionEntries(r); err == nil && len(entries) > 0 {
			in.Offers = suggestionDecisionOffers(entries)
			if suggestions, err := h.suggestions.Suggest(r.Context(), in); err == nil {
				data.Suggestions = suggestionViews(suggestions, entries)
			}
		}
	}
	h.renderer.RenderFragment(w, r, fragment, data, http.StatusOK)
}

// catalogSuggestions answers the two-step catalog's Suggested group for the query
// the requester typed. An empty query suggests nothing: there is no text to read.
// The catalog hands over every card it shows, including the disabled ones, so the
// categories that cannot run are dropped here, as suggestionEntries does for the
// live fragment: a suggestion is never a link the create guard would refuse.
func (h *TicketHandlers) catalogSuggestions(r *http.Request, query string, catalog []catalogCategoryEntry) []suggestionView {
	entries := make([]catalogCategoryEntry, 0, len(catalog))
	for _, entry := range catalog {
		if !entry.Unavailable {
			entries = append(entries, entry)
		}
	}
	if !h.suggestionsEnabled(r) || strings.TrimSpace(query) == "" || len(entries) == 0 {
		return nil
	}
	suggestions, err := h.suggestions.Suggest(r.Context(), application.SuggestInput{
		Description: query,
		Offers:      suggestionDecisionOffers(entries),
	})
	if err != nil {
		return nil
	}
	return suggestionViews(suggestions, entries)
}

// suggestionIDs is the set of categories a Suggested group already names.
func suggestionIDs(suggestions []suggestionView) map[int64]struct{} {
	ids := make(map[int64]struct{}, len(suggestions))
	for _, suggestion := range suggestions {
		ids[suggestion.ID] = struct{}{}
	}
	return ids
}

// hideSuggestedCategories takes the categories the Suggested group names out of
// their department section, so one category is offered once. A section left with
// no card at all disappears with its last card.
func hideSuggestedCategories(sections []createCatalogSection, suggested map[int64]struct{}) []createCatalogSection {
	if len(suggested) == 0 {
		return sections
	}
	for i := range sections {
		visible := 0
		for j := range sections[i].Cards {
			card := &sections[i].Cards[j]
			if _, ok := suggested[card.ID]; ok {
				card.Hidden = true
			}
			if !card.Hidden {
				visible++
			}
		}
		sections[i].Hidden = visible == 0
	}
	return sections
}

// -------------------------------------------------------------- the settings side

// CategorySuggestionOffers is the port the Settings card's Try it box reads its
// categories through. The ticket handlers own the offer rule, so the box cannot
// test against a catalog the requesters do not have.
type CategorySuggestionOffers interface {
	SuggestionOffers(r *http.Request) ([]application.DecisionCategory, error)
}

// WithCategorySuggestions wires the category-suggestion card and its two routes:
// the save that stores the engine, and the Try it box that runs a configuration
// without saving it.
func (h *SettingsHandlers) WithCategorySuggestions(settings *application.CategorySuggestionSettingsService, suggestions *application.CategorySuggestionService, offers CategorySuggestionOffers) *SettingsHandlers {
	h.categorySuggestions = settings
	h.suggestionTester = suggestions
	h.suggestionOffers = offers
	return h
}

// categorySuggestionEngineOption is one engine choice: the stored value and the
// catalogue keys of its label and its sentence.
type categorySuggestionEngineOption struct {
	Value    application.CategorySuggestionEngine
	Label    string
	Help     string
	Selected bool
}

// categorySuggestionsCardData is the "Category suggestions" card payload.
type categorySuggestionsCardData struct {
	Engines     []categorySuggestionEngineOption
	Engine      application.CategorySuggestionEngine
	EndpointURL string
	Model       string
	// APIKeySet says whether a key is stored. The key itself never reaches a
	// template (S5): the field renders empty and the help sentence says how it is
	// stored.
	APIKeySet bool
}

// ModelSelected reports whether the decision-model fields render at all.
func (c categorySuggestionsCardData) ModelSelected() bool {
	return c.Engine == application.CategorySuggestionModel
}

// WithAPIKeyPlaceholder reports whether the key field shows the stored-secret
// placeholder.
func (c categorySuggestionsCardData) WithAPIKeyPlaceholder() bool {
	return c.APIKeySet
}

// categorySuggestionEngineOptions lists the three engines in the order the card
// offers them (S1), with the active one marked. The keys are literals, exactly as
// the email section spells its own choices: the i18n coverage check reads them from
// here, so a key renamed in a catalogue fails instead of rendering raw.
func categorySuggestionEngineOptions(selected application.CategorySuggestionEngine) []categorySuggestionEngineOption {
	labels := map[application.CategorySuggestionEngine]string{
		application.CategorySuggestionOff:   "settings.suggestions.engine.off",
		application.CategorySuggestionLocal: "settings.suggestions.engine.local",
		application.CategorySuggestionModel: "settings.suggestions.engine.model",
	}
	helps := map[application.CategorySuggestionEngine]string{
		application.CategorySuggestionOff:   "settings.suggestions.engine.off_help",
		application.CategorySuggestionLocal: "settings.suggestions.engine.local_help",
		application.CategorySuggestionModel: "settings.suggestions.engine.model_help",
	}
	engines := application.AllowedCategorySuggestionEngines()
	out := make([]categorySuggestionEngineOption, 0, len(engines))
	for _, engine := range engines {
		out = append(out, categorySuggestionEngineOption{
			Value:    engine,
			Label:    labels[engine],
			Help:     helps[engine],
			Selected: engine == selected,
		})
	}
	return out
}

// categorySuggestionsCard builds the card from the stored configuration.
//
// A read failure renders the card in its shipped state — off — instead of failing
// the whole section: every other payload of the section is read the same way and
// a store that cannot answer has already failed the page above this call.
func (h *SettingsHandlers) categorySuggestionsCard(r *http.Request) categorySuggestionsCardData {
	card := categorySuggestionsCardData{
		Engine:  application.DefaultCategorySuggestionEngine,
		Engines: categorySuggestionEngineOptions(application.DefaultCategorySuggestionEngine),
	}
	if h.categorySuggestions == nil {
		return card
	}
	stored, err := h.categorySuggestions.Get(r.Context(), *userFromContext(r.Context()))
	if err != nil {
		return card
	}
	card.Engine = stored.Engine
	card.EndpointURL = stored.EndpointURL
	card.Model = stored.Model
	card.APIKeySet = stored.APIKeySet
	card.Engines = categorySuggestionEngineOptions(stored.Engine)
	return card
}

// categorySuggestionSettingsFrom reads one submission of the card.
func categorySuggestionSettingsFrom(form url.Values) application.CategorySuggestionSettingsInput {
	return application.CategorySuggestionSettingsInput{
		Engine:      application.CategorySuggestionEngine(strings.TrimSpace(form.Get(suggestionEngineField))),
		EndpointURL: form.Get(suggestionEndpointField),
		Model:       form.Get(suggestionModelField),
		APIKey:      form.Get(suggestionAPIKeyField),
	}
}

// updateCategorySuggestions persists the engine choice and, for the decision
// model, the endpoint, the model and the key (S1/S5). One application call
// applies the whole card, so a rejected submission writes nothing (#345); the
// refusal re-renders the section with the SUBMITTED values, because a rejected
// save must never cost the administrator what they typed — and never with the
// key, which no read path may print.
func (h *SettingsHandlers) updateCategorySuggestions(w http.ResponseWriter, r *http.Request) {
	if !requireCapability(w, r, application.CapManageSettings) {
		return
	}
	if err := r.ParseForm(); err != nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	if h.categorySuggestions == nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	submitted := categorySuggestionSettingsFrom(r.Form)
	err := h.categorySuggestions.Save(r.Context(), *userFromContext(r.Context()), submitted)
	if err != nil {
		status, msg := mapErrorFor(r, err)
		if status == http.StatusInternalServerError {
			http.Error(w, msg, status)
			return
		}
		h.renderTickets(w, r, status, msg, func(data *settingsTicketsData) {
			data.Suggestions = h.rejectedCategorySuggestions(r, submitted)
		})
		return
	}
	saveFeedback(w, r, saveFeedbackSaved, saveFeedbackSuccess)
	redirect(w, r, settingsSectionHref(settingsSectionTickets))
}

// rejectedCategorySuggestions is the card rendered by a refused save: the values
// that were submitted, and only whether a key is already stored.
func (h *SettingsHandlers) rejectedCategorySuggestions(r *http.Request, submitted application.CategorySuggestionSettingsInput) categorySuggestionsCardData {
	card := categorySuggestionsCardData{
		Engines:     categorySuggestionEngineOptions(submitted.Engine),
		Engine:      submitted.Engine,
		EndpointURL: submitted.EndpointURL,
		Model:       submitted.Model,
	}
	if stored, err := h.categorySuggestions.Get(r.Context(), *userFromContext(r.Context())); err == nil {
		card.APIKeySet = stored.APIKeySet
	}
	return card
}

// categorySuggestionTryCandidate is one row of the Try it result.
type categorySuggestionTryCandidate struct {
	Name    string
	Percent int
}

// categorySuggestionTryView is the Try it result as the card renders it.
type categorySuggestionTryView struct {
	Candidates []categorySuggestionTryCandidate
	Confident  bool
	// FellBack marks the answer that came from the local engine because the
	// decision model could not answer (S5).
	FellBack bool
}

// categorySuggestionPercent turns a 0..1 confidence into the whole percentage the
// card prints.
func categorySuggestionPercent(confidence float64) int {
	if confidence < 0 {
		confidence = 0
	}
	if confidence > 1 {
		confidence = 1
	}
	return int(math.Round(confidence * 100))
}

// categorySuggestionTryViewFrom maps one Try result to the rendered rows.
func categorySuggestionTryViewFrom(result application.TryResult) categorySuggestionTryView {
	view := categorySuggestionTryView{Confident: result.Confident, FellBack: result.FellBack}
	for _, candidate := range result.Candidates {
		view.Candidates = append(view.Candidates, categorySuggestionTryCandidate{
			Name:    candidate.Name,
			Percent: categorySuggestionPercent(candidate.Confidence),
		})
	}
	return view
}

// tryCategorySuggestions runs the configuration the card SUBMITS, without saving
// it, and swaps the result into the card (S6). A blank API key means the stored
// one, because the key is never rendered back.
func (h *SettingsHandlers) tryCategorySuggestions(w http.ResponseWriter, r *http.Request) {
	if !requireCapability(w, r, application.CapManageSettings) {
		return
	}
	if err := r.ParseForm(); err != nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	if h.suggestionTester == nil || h.suggestionOffers == nil {
		h.renderer.RenderFragment(w, r, "category_suggestion_try", categorySuggestionTryView{}, http.StatusOK)
		return
	}
	offers, err := h.suggestionOffers.SuggestionOffers(r)
	if err != nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	result, err := h.suggestionTester.Try(r.Context(), *userFromContext(r.Context()), application.TryInput{
		Settings: categorySuggestionSettingsFrom(r.Form),
		Text:     r.Form.Get(suggestionTryField),
		Offers:   offers,
	})
	if err != nil {
		status, msg := mapErrorFor(r, err)
		http.Error(w, msg, status)
		return
	}
	h.renderer.RenderFragment(w, r, "category_suggestion_try", categorySuggestionTryViewFrom(result), http.StatusOK)
}
