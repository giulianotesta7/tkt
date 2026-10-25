package httpadapter

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// Category suggestions over HTTP (issue #416, S1/S2/S3/S5/S6/S7): the fragment
// both creation screens refresh, the server-rendered two-step catalog, and the
// Settings card. These tests run against the real harness — real database, real
// renderer, real routes — and fake only the decision model at its port, so no
// socket is ever opened (go-tests rule 10).

// fakeDecisionModel answers the decision engine at its port, recording what it
// was asked. timeout reports the deadline the suggester set the way a model that
// ran out of time would, without waiting for it (go-tests rule 9: no sleeps).
type fakeDecisionModel struct {
	answer  application.DecisionChoiceAnswer
	err     error
	timeout bool
	calls   int
	gotCfg  application.DecisionModelConfig
	gotReq  application.DecisionChoiceRequest
}

var _ application.DecisionModel = (*fakeDecisionModel)(nil)

func (f *fakeDecisionModel) Choose(ctx context.Context, cfg application.DecisionModelConfig, req application.DecisionChoiceRequest) (application.DecisionChoiceAnswer, error) {
	f.calls++
	f.gotCfg = cfg
	f.gotReq = req
	if f.timeout {
		return application.DecisionChoiceAnswer{}, context.DeadlineExceeded
	}
	if f.err != nil {
		return application.DecisionChoiceAnswer{}, f.err
	}
	return f.answer, nil
}

// suggestionHarness wires the ticket routes the way the process does, with the
// catalog service and the suggestion composition, and returns the mux. The
// harness's own mux wires the settings routes.
func suggestionHarness(t *testing.T, h *harness) *http.ServeMux {
	t.Helper()
	catalog := application.NewCatalogService(h.store.CatalogStore(), h.store.CategoryStore(), h.clock)
	mux := http.NewServeMux()
	NewTicketHandlers(h.tickets, h.comments, h.search, h.categories, h.users, h.store.DeskStore(), h.workflows, application.NewWorkflowRunner(h.clock), h.store.WorkflowRunStore(), h.store.WorkflowUnitOfWork(), h.renderer, catalog).WithCategorySuggestions(h.suggestionSvc).Register(mux)
	return mux
}

// enableLocalSuggestions turns the local engine on, the state an instance runs
// after an administrator chose "On this server".
func enableLocalSuggestions(t *testing.T, h *harness) {
	t.Helper()
	if err := h.suggestionSettings.Save(t.Context(), *h.admin, application.CategorySuggestionSettingsInput{Engine: application.CategorySuggestionLocal}); err != nil {
		t.Fatalf("enable the local engine: %v", err)
	}
}

// enableModelSuggestions saves a complete decision-model configuration.
func enableModelSuggestions(t *testing.T, h *harness, endpoint string) {
	t.Helper()
	err := h.suggestionSettings.Save(t.Context(), *h.admin, application.CategorySuggestionSettingsInput{
		Engine:      application.CategorySuggestionModel,
		EndpointURL: endpoint,
		Model:       "gpt-4.1-mini",
		APIKey:      "sk-test-key",
	})
	if err != nil {
		t.Fatalf("enable the decision engine: %v", err)
	}
}

// suggestionCatalog fills the catalog with the categories a small help desk
// actually has. Ranked suggestions need a corpus to rank against — the index
// weighs a word by how rare it is — so a two-category database cannot produce a
// confident suggestion at all.
func suggestionCatalog(t *testing.T, h *harness) {
	t.Helper()
	for _, category := range []struct{ name, keywords string }{
		{"Printers", "impresora, toner, imprimir"},
		{"Network & Wi-Fi", "wifi, red, internet, conexion, vpn"},
		{"Payroll", "sueldo, nomina, salario"},
		{"Vacation & leave", "vacaciones, licencia, permiso"},
		{"Access & permissions", "acceso, contrasena, permisos"},
		{"Laptops & computers", "laptop, notebook, computadora"},
	} {
		suggestionCategory(t, h, category.name, category.keywords)
	}
}

// suggestionCategory creates a category in the seeded desk with the words a
// requester would use, and publishes a workflow for it so it is offered.
func suggestionCategory(t *testing.T, h *harness, name, keywords string) domain.Category {
	t.Helper()
	departmentID, deskID := h.seededDesk(t)
	rec := h.postForm(t, "/categories", keywordsFormValues(t, name, keywords, departmentID, deskID), false)
	wantRedirect(t, rec, http.StatusSeeOther, "/categories")
	created, ok := categoryNamed(t, h, name)
	if !ok {
		t.Fatalf("category %q must be readable", name)
	}
	h.publishWorkflow(t, created.ID, simpleManualDef())
	return created
}

// suggestionFragment asks the fragment endpoint the way the browser does.
func suggestionFragment(t *testing.T, h *harness, mux *http.ServeMux, query url.Values) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/tickets/suggestions?"+query.Encode(), nil)
	req.Header.Set("Cookie", sessionCookie+"="+h.adminSession.ID)
	req.Header.Set("HX-Request", "true")
	rec := httptest.NewRecorder()
	h.mw.Wrap(mux).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /tickets/suggestions = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	return htmlUnescape(rec.Body.String())
}

// htmlUnescape reads a rendered body the way a browser shows it.
func htmlUnescape(body string) string {
	return strings.NewReplacer("&#39;", "'", "&amp;", "&", "&quot;", `"`, "&lt;", "<", "&gt;", ">").Replace(body)
}

// TestCategorySuggestionsOffLeavesBothCreationScreensAlone is S7: the feature
// starts off, nothing is suggested, and both creation screens render exactly as
// they do without the feature.
func TestCategorySuggestionsOffLeavesBothCreationScreensAlone(t *testing.T) {
	h := newHarness(t)
	mux := suggestionHarness(t, h)
	suggestionCatalog(t, h)

	form := suggestionFragment(t, h, mux, url.Values{"mode": {application.TicketCreationSingle}, "title": {"la impresora no imprime"}})
	if strings.TrimSpace(form) != "" {
		t.Fatalf("the fragment answered %q with the feature off, want nothing", form)
	}
	// The stylesheet is inlined into every page, so the assertions below name the
	// markup a browser would run, never a class name the CSS also carries.
	_, single := getPath(t, h, mux, "/tickets/new")
	if strings.Contains(single, `class="create-suggestions"`) || strings.Contains(single, `hx-get="/tickets/suggestions`) {
		t.Error("the single-page form must render no suggestion container with the feature off")
	}

	setCreationMode(t, h, application.TicketCreationTwoStep)
	_, catalog := getPath(t, h, mux, "/tickets/new")
	if strings.Contains(catalog, `id="create-catalog-suggestions"`) || strings.Contains(catalog, `hx-get="/tickets/suggestions`) {
		t.Error("the catalog must render no suggested group with the feature off")
	}
	if !strings.Contains(catalog, `placeholder="Search categories, desks, or departments"`) {
		t.Error("the catalog search keeps its own placeholder with the feature off")
	}
	if h.decisionModel.calls != 0 {
		t.Errorf("the decision model was called %d times with the feature off, want never", h.decisionModel.calls)
	}
}

// TestCategorySuggestionsSinglePageOffersTheMatchingCategory is S2's first half:
// the fragment the form refreshes shows up to three chips with the category, the
// desk it belongs to, and a title that says where they came from.
func TestCategorySuggestionsSinglePageOffersTheMatchingCategory(t *testing.T) {
	h := newHarness(t)
	mux := suggestionHarness(t, h)
	suggestionCatalog(t, h)
	printers, ok := categoryNamed(t, h, "Printers")
	if !ok {
		t.Fatal("the catalog fixture must create Printers")
	}
	enableLocalSuggestions(t, h)

	body := suggestionFragment(t, h, mux, url.Values{
		"mode":        {application.TicketCreationSingle},
		"title":       {"la impresora no imprime"},
		"description": {"sale una hoja en blanco"},
	})
	for _, want := range []string{
		"Suggested from your description",
		`data-suggest-category="` + strconv.FormatInt(printers.ID, 10) + `"`,
		"Printers",
		"General · General",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the single-page fragment must render %q, got: %s", want, body)
		}
	}

	// The form carries the container the fragment refreshes and the htmx call
	// that refreshes it, from both text fields.
	_, form := getPath(t, h, mux, "/tickets/new")

	// The block draws its hairline ONCE: the live host the form owns is an
	// invisible region of its own class, and the fragment's container is the only
	// box that carries the rule. Two boxes with the same rule is the doubled
	// divider an owner sees the moment a suggestion appears.
	if got := strings.Count(body, `class="create-suggestions"`); got != 1 {
		t.Errorf("the fragment must render one suggestion block, got %d: %s", got, body)
	}
	if strings.Contains(form, `class="create-suggestions"`) {
		t.Error("the live host must not carry the styled suggestion block, or the two hairlines stack")
	}
	if got := strings.Count(form, `class="create-suggestions-live"`); got != 1 {
		t.Errorf("the form must render one suggestion host, got %d", got)
	}
	for _, want := range []string{
		`hx-get="/tickets/suggestions?mode=single"`,
		`aria-live="polite"`,
		`hx-include="#title, #description"`,
		`hx-trigger="input changed delay:500ms from:#title, input changed delay:500ms from:#description"`,
	} {
		if !strings.Contains(form, want) {
			t.Errorf("the single-page form must render %q, got: %s", want, form)
		}
	}
}

// TestCategorySuggestionsLeavesTheUnrelatedTextAlone is S3: nothing matching is
// nothing suggested, not a guess.
func TestCategorySuggestionsLeavesTheUnrelatedTextAlone(t *testing.T) {
	h := newHarness(t)
	mux := suggestionHarness(t, h)
	suggestionCatalog(t, h)
	enableLocalSuggestions(t, h)

	body := suggestionFragment(t, h, mux, url.Values{
		"mode":        {application.TicketCreationSingle},
		"description": {"no encuentro mi tarjeta de transporte"},
	})
	if strings.TrimSpace(body) != "" {
		t.Fatalf("the fragment answered %q for text that matches nothing, want nothing", body)
	}
}

// TestCategorySuggestionsToleratesATypo proves the trigram index is reachable
// from the fragment: a misspelled keyword still finds its category.
func TestCategorySuggestionsToleratesATypo(t *testing.T) {
	h := newHarness(t)
	mux := suggestionHarness(t, h)
	suggestionCatalog(t, h)
	enableLocalSuggestions(t, h)

	body := suggestionFragment(t, h, mux, url.Values{
		"mode":        {application.TicketCreationSingle},
		"description": {"la impresro no imprime"},
	})
	if !strings.Contains(body, "Printers") {
		t.Fatalf("a typo must still reach its category, got: %s", body)
	}
}

// TestCategorySuggestionsTwoStepGroupRendersOverTheCatalog is S2's second half on
// the no-script path: ?q renders a Suggested group above the departments, with
// the matched words, and the suggested category is not repeated below.
func TestCategorySuggestionsTwoStepGroupRendersOverTheCatalog(t *testing.T) {
	h := newHarness(t)
	mux := suggestionHarness(t, h)
	suggestionCatalog(t, h)
	printers, ok := categoryNamed(t, h, "Printers")
	if !ok {
		t.Fatal("the catalog fixture must create Printers")
	}
	enableLocalSuggestions(t, h)
	setCreationMode(t, h, application.TicketCreationTwoStep)

	_, body := getPath(t, h, mux, "/tickets/new?q=la+impresora+no+imprime")
	if !strings.Contains(body, `placeholder="Describe your problem or search categories"`) {
		t.Error("the catalog search must accept a description with the engine on")
	}
	if !strings.Contains(body, `class="create-catalog-suggested"`) {
		t.Fatalf("the catalog must render the suggested group, got: %s", body)
	}
	suggested := body[strings.Index(body, `class="create-catalog-suggested"`):]
	suggested = suggested[:strings.Index(suggested, "create-catalog-department-group")]
	if !strings.Contains(suggested, "Suggested") || !strings.Contains(suggested, "Printers") {
		t.Errorf("the suggested group must name the category, got: %s", suggested)
	}
	if !strings.Contains(suggested, "Matches: impresora") {
		t.Errorf("the suggested tile must list the matched words, got: %s", suggested)
	}
	// The department section must not repeat the category it just suggested: its
	// card is still in the markup, hidden, exactly as the live filter would hide it.
	ref := "/tickets/new?category_id=" + strconv.FormatInt(printers.ID, 10)
	if !strings.Contains(body, `href="`+ref+`" hidden>`) {
		t.Error("the suggested category's department card must render hidden")
	}
	if strings.Contains(body, "No category matched") {
		t.Error("a query with a confident suggestion must not claim nothing matched")
	}
}

// TestCategorySuggestionsKeepTheCatalogVisibleWithASuggestion is the rule the
// suggested group changes: a description that names no card keeps the whole
// catalog below the group, and the no-match message stays away.
func TestCategorySuggestionsKeepTheCatalogVisibleWithASuggestion(t *testing.T) {
	h := newHarness(t)
	mux := suggestionHarness(t, h)
	suggestionCatalog(t, h)
	runnable, ok := categoryNamed(t, h, "Network & Wi-Fi")
	if !ok {
		t.Fatal("the catalog fixture must create the network category")
	}
	enableLocalSuggestions(t, h)
	setCreationMode(t, h, application.TicketCreationTwoStep)

	_, body := getPath(t, h, mux, "/tickets/new?q=la+impresora+no+imprime")
	if strings.Contains(body, "No category matched") {
		t.Fatal("a confident suggestion must replace the no-match message")
	}
	if !strings.Contains(body, ">Network &amp; Wi-Fi</span>") && !strings.Contains(body, ">Network & Wi-Fi</span>") {
		t.Errorf("the catalog below the group must stay visible, got: %s", body)
	}
	if !strings.Contains(body, `href="/tickets/new?category_id=`+strconv.FormatInt(runnable.ID, 10)+`"`) {
		t.Error("a category that was not suggested must keep its card")
	}

	// Without a confident suggestion the same query behaves exactly as today.
	body = suggestionFragment(t, h, mux, url.Values{"mode": {application.TicketCreationTwoStep}, "q": {"la impresora no imprime"}})
	if !strings.Contains(body, "create-catalog-suggested-title") {
		t.Errorf("the live fragment must render the suggested group, got: %s", body)
	}
}

// TestCategorySuggestionsLiveFragmentIsEmptyWithoutAConfidentSuggestion pins the
// live half of S3: the swap target empties itself instead of leaving stale
// suggestions on screen.
func TestCategorySuggestionsLiveFragmentIsEmptyWithoutAConfidentSuggestion(t *testing.T) {
	h := newHarness(t)
	mux := suggestionHarness(t, h)
	suggestionCatalog(t, h)
	enableLocalSuggestions(t, h)

	body := suggestionFragment(t, h, mux, url.Values{"mode": {application.TicketCreationTwoStep}, "q": {"no encuentro mi tarjeta de transporte"}})
	if strings.TrimSpace(body) != "" {
		t.Fatalf("the catalog fragment answered %q, want nothing", body)
	}
}

// TestCategorySuggestionsNeverNameAnUnavailableOrUnpublishedCategory is the offer
// set's boundary: a category the requester cannot actually open is never the
// answer, however well the text matches it.
func TestCategorySuggestionsNeverNameAnUnavailableOrUnpublishedCategory(t *testing.T) {
	h := newHarness(t)
	mux := suggestionHarness(t, h)
	catalog := application.NewCatalogService(h.store.CatalogStore(), h.store.CategoryStore(), h.clock)
	department, err := catalog.CreateDepartmentFor(t.Context(), *h.admin, "Operations", "Operations department")
	if err != nil {
		t.Fatalf("create department: %v", err)
	}
	desk, err := catalog.CreateDeskFor(t.Context(), *h.admin, department.ID, "Support")
	if err != nil {
		t.Fatalf("create desk: %v", err)
	}
	draft, err := h.categories.CreateWithKeywordsFor(t.Context(), *h.admin, "Impresoras rotas", "impresora, toner", "impresora, toner", desk.ID)
	if err != nil {
		t.Fatalf("create unpublished category: %v", err)
	}
	if err := h.workflows.SaveDraft(t.Context(), *h.admin, draft.ID, assignToDeskDef(desk.ID)); err != nil {
		t.Fatalf("save draft: %v", err)
	}
	broken, err := h.categories.CreateWithKeywordsFor(t.Context(), *h.admin, "Reparaciones", "impresora, toner", "impresora, toner", desk.ID)
	if err != nil {
		t.Fatalf("create unrunnable category: %v", err)
	}
	h.publishWorkflow(t, broken.ID, assignToDeskDef(desk.ID))
	if err := h.desks.RemoveMember(t.Context(), *h.admin, desk.ID, h.admin.ID); err != nil {
		t.Fatalf("remove member: %v", err)
	}
	enableLocalSuggestions(t, h)

	body := suggestionFragment(t, h, mux, url.Values{"mode": {application.TicketCreationSingle}, "description": {"la impresora no imprime"}})
	for _, unwanted := range []string{"Impresoras rotas", "Reparaciones"} {
		if strings.Contains(body, unwanted) {
			t.Errorf("the fragment must not suggest %q, got: %s", unwanted, body)
		}
	}
}

// TestCategorySuggestionsFallBackToTheLocalEngine is S5: a decision model that
// fails, refuses or runs out of time never costs the requester the suggestion.
func TestCategorySuggestionsFallBackToTheLocalEngine(t *testing.T) {
	cases := []struct {
		name     string
		arrange  func(*fakeDecisionModel)
		decision string
	}{
		{name: "transport failure", arrange: func(m *fakeDecisionModel) { m.err = errors.New("dial tcp: connection refused") }},
		{name: "refusal", arrange: func(m *fakeDecisionModel) { m.answer = application.DecisionChoiceAnswer{Refused: true} }},
		{name: "timeout", arrange: func(m *fakeDecisionModel) { m.timeout = true }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			mux := suggestionHarness(t, h)
			suggestionCatalog(t, h)
			enableModelSuggestions(t, h, "https://decisions.example.com/v1")
			tc.arrange(h.decisionModel)

			body := suggestionFragment(t, h, mux, url.Values{"mode": {application.TicketCreationSingle}, "description": {"la impresora no imprime"}})
			if h.decisionModel.calls == 0 {
				t.Fatal("the decision model must be attempted before falling back")
			}
			if !strings.Contains(body, "Printers") {
				t.Fatalf("the local engine must answer when the model cannot, got: %s", body)
			}
		})
	}
}

// TestCategorySuggestionsSendOnlyWhatS5Allows is the privacy boundary as a test:
// the request carries the requester's words and each category's name and
// description, and nothing else about the ticket or the requester.
func TestCategorySuggestionsSendOnlyWhatS5Allows(t *testing.T) {
	h := newHarness(t)
	mux := suggestionHarness(t, h)
	suggestionCatalog(t, h)
	printers, ok := categoryNamed(t, h, "Printers")
	if !ok {
		t.Fatal("the catalog fixture must create Printers")
	}
	enableModelSuggestions(t, h, "https://decisions.example.com/v1")
	h.decisionModel.answer = application.DecisionChoiceAnswer{
		Choice:        strconv.FormatInt(printers.ID, 10),
		Probabilities: map[string]float64{strconv.FormatInt(printers.ID, 10): 0.8},
		Confidence:    0.8,
	}

	body := suggestionFragment(t, h, mux, url.Values{
		"mode":        {application.TicketCreationSingle},
		"title":       {"la impresora no imprime"},
		"description": {"sale una hoja en blanco"},
	})
	if !strings.Contains(body, "Printers") {
		t.Fatalf("the model's suggestion must reach the fragment, got: %s", body)
	}
	if want := "la impresora no imprime\nsale una hoja en blanco"; h.decisionModel.gotReq.Input != want {
		t.Errorf("the model read %q, want the title and the description only", h.decisionModel.gotReq.Input)
	}
	// Each choice is one offered category: its name, then its description.
	catalog, err := h.categories.List(t.Context())
	if err != nil {
		t.Fatalf("list categories: %v", err)
	}
	byID := make(map[string]domain.Category, len(catalog))
	for _, category := range catalog {
		byID[strconv.FormatInt(category.ID, 10)] = category
	}
	offered := 0
	for _, choice := range h.decisionModel.gotReq.Choices {
		if choice.Value == "none" {
			continue
		}
		offered++
		category, ok := byID[choice.Value]
		if !ok {
			t.Errorf("a choice offered the category %q, which the requester was not offered", choice.Value)
			continue
		}
		if !strings.HasPrefix(choice.Description, category.Name+": "+category.Description) {
			t.Errorf("a choice carried %q, want the name and description of %q", choice.Description, category.Name)
		}
	}
	if offered != len(catalog) {
		t.Errorf("the model was offered %d categories, want the %d published and runnable ones", offered, len(catalog))
	}
	joined := h.decisionModel.gotReq.Input
	for _, choice := range h.decisionModel.gotReq.Choices {
		joined += choice.Description
	}
	for _, unwanted := range []string{h.admin.Name, h.admin.Email} {
		if strings.Contains(joined, unwanted) {
			t.Errorf("the model request carried %q, want no requester identity", unwanted)
		}
	}
}

// TestCategorySuggestionsAreForRequesters is the endpoint's authorization: a
// plain requester is the one who writes a ticket, so the fragment answers them.
func TestCategorySuggestionsAreForRequesters(t *testing.T) {
	h := newHarness(t)
	mux := suggestionHarness(t, h)
	suggestionCatalog(t, h)
	enableLocalSuggestions(t, h)
	user, err := h.users.Create(t.Context(), *h.admin, application.CreateUserInput{Name: "Carol", Email: "carol@tkt.test", Password: "SuperSecret42!"})
	if err != nil {
		t.Fatalf("create requester: %v", err)
	}
	session := h.loginCookie(t, user.Email, "SuperSecret42!")
	if session == "" {
		t.Fatal("the requester must be able to sign in")
	}

	req := httptest.NewRequest(http.MethodGet, "/tickets/suggestions?mode=single&description=la+impresora+no+imprime", nil)
	req.Header.Set("Cookie", sessionCookie+"="+session)
	req.Header.Set("HX-Request", "true")
	rec := httptest.NewRecorder()
	h.mw.Wrap(mux).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("a requester's GET = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Printers") {
		t.Errorf("a requester must see the suggestion, got: %s", rec.Body.String())
	}

	// An anonymous request never reaches the fragment: the session middleware
	// sends it to the sign-in page, exactly as GET /tickets/new does.
	anonymous := httptest.NewRecorder()
	h.mw.Wrap(mux).ServeHTTP(anonymous, httptest.NewRequest(http.MethodGet, "/tickets/suggestions?mode=single&description=x", nil))
	if anonymous.Code != http.StatusSeeOther {
		t.Errorf("an anonymous GET = %d, want the sign-in redirect", anonymous.Code)
	}
}

// ---------------------------------------------------------------------------
// Settings card (S6)
// ---------------------------------------------------------------------------

// TestCategorySuggestionsSettingsCardRendersTheThreeEngines pins the card an
// administrator meets: the group title, its help sentence, the three choices with
// their own sentences, and the Try it box.
func TestCategorySuggestionsSettingsCardRendersTheThreeEngines(t *testing.T) {
	h := newHarness(t)

	body := sectionGet(t, h, "/settings/tickets")
	for _, want := range []string{
		"Category suggestions",
		"Suggest up to three categories from what the requester writes. Requesters always choose; with no confident suggestion they use the catalog as today.",
		`name="suggestion_engine" value="off" checked`,
		"Requesters choose the category themselves, as today.",
		`name="suggestion_engine" value="local"`,
		"Matches the description against category names, descriptions and keywords. Nothing leaves TKT.",
		`name="suggestion_engine" value="model"`,
		"Falls back to",
		`action="/settings/category-suggestions"`,
		`data-settings-savebar`,
		"Save category suggestions",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the card must render %q", want)
		}
	}
	// Off hides both the model fields and the Try it box.
	if strings.Contains(body, `id="suggestion_endpoint"`) && !strings.Contains(body, `data-show-when="suggestion_engine=model"`) {
		t.Error("the model fields must be revealed by the engine choice")
	}
	if strings.Contains(body, "Type a description a requester might write") && !strings.Contains(body, `data-show-when="suggestion_engine!=off"`) {
		t.Error("the Try it box must follow the engine choice")
	}
}

// TestCategorySuggestionsSettingsSavePersistsAndNeverRendersTheKey walks the
// save: the engine is stored, the key is sealed, and no read path prints it.
func TestCategorySuggestionsSettingsSavePersistsAndNeverRendersTheKey(t *testing.T) {
	h := newHarness(t)

	rec := h.postForm(t, "/settings/category-suggestions", url.Values{
		"suggestion_engine":   {"model"},
		"suggestion_endpoint": {"https://decisions.example.com/v1"},
		"suggestion_model":    {"gpt-4.1-mini"},
		"suggestion_api_key":  {"sk-secret-value"},
	}, false)
	wantRedirect(t, rec, http.StatusSeeOther, "/settings/tickets")

	stored, err := h.suggestionSettings.Get(t.Context(), *h.admin)
	if err != nil {
		t.Fatalf("read the stored settings: %v", err)
	}
	if stored.Engine != application.CategorySuggestionModel || stored.EndpointURL != "https://decisions.example.com/v1" || stored.Model != "gpt-4.1-mini" {
		t.Fatalf("stored settings = %+v, want the submitted configuration", stored)
	}
	if !stored.APIKeySet {
		t.Error("APIKeySet = false after saving a key, want true")
	}

	body := sectionGet(t, h, "/settings/tickets")
	if strings.Contains(body, "sk-secret-value") {
		t.Error("the page must never render the API key back")
	}
	if !strings.Contains(body, `id="suggestion_endpoint" name="suggestion_endpoint" value="https://decisions.example.com/v1"`) {
		t.Error("the page must render the stored endpoint")
	}
	if !strings.Contains(body, `name="suggestion_api_key" value=""`) {
		t.Error("the API key field must start empty")
	}
	if !strings.Contains(body, "••••••••") {
		t.Error("a stored key must be marked the way the other stored secrets are")
	}
	if !strings.Contains(body, "Stored encrypted, like the SMTP password.") {
		t.Error("the key field must say how it is stored")
	}
	if !strings.Contains(body, "Sent:") || !strings.Contains(body, "Never sent:") {
		t.Error("the card must state what leaves the instance")
	}
}

// TestCategorySuggestionsSettingsRefusalChangesNothingAndEchoesTheForm keeps a
// rejected save from costing the administrator what they typed, without ever
// echoing the key.
func TestCategorySuggestionsSettingsRefusalChangesNothingAndEchoesTheForm(t *testing.T) {
	h := newHarness(t)

	rec := h.postForm(t, "/settings/category-suggestions", url.Values{
		"suggestion_engine":   {"model"},
		"suggestion_endpoint": {"http://insecure.example.com/v1"},
		"suggestion_model":    {"gpt-4.1-mini"},
		"suggestion_api_key":  {"sk-secret-value"},
	}, false)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "the decision model address must be an https URL") {
		t.Errorf("the re-render must explain the refusal, got: %s", body)
	}
	if !strings.Contains(body, `value="http://insecure.example.com/v1"`) {
		t.Error("the re-render must keep the submitted endpoint")
	}
	if strings.Contains(body, "sk-secret-value") {
		t.Error("the re-render must never echo the API key")
	}
	stored, err := h.suggestionSettings.Get(t.Context(), *h.admin)
	if err != nil {
		t.Fatalf("read the stored settings: %v", err)
	}
	if stored.Engine != application.CategorySuggestionOff || stored.APIKeySet {
		t.Errorf("a refused save stored %+v, want the configuration unchanged", stored)
	}
}

// TestCategorySuggestionsRoutesRefuseNonAdmins is the capability boundary: the
// card, its save and its test are instance configuration.
func TestCategorySuggestionsRoutesRefuseNonAdmins(t *testing.T) {
	h := newHarness(t)
	user, err := h.users.Create(t.Context(), *h.admin, application.CreateUserInput{Name: "Carol", Email: "carol@tkt.test", Password: "SuperSecret42!"})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	session := h.loginCookie(t, user.Email, "SuperSecret42!")

	save := h.postFormAs(t, "/settings/category-suggestions", url.Values{"suggestion_engine": {"local"}}, session)
	if save.Code != http.StatusForbidden {
		t.Errorf("non-admin save = %d, want 403", save.Code)
	}
	try := h.postFormAs(t, "/settings/category-suggestions/try", url.Values{
		"suggestion_engine": {"local"},
		"suggestion_try":    {"impresora"},
	}, session)
	if try.Code != http.StatusForbidden {
		t.Errorf("non-admin try = %d, want 403", try.Code)
	}
	stored, err := h.suggestionSettings.Get(t.Context(), *h.admin)
	if err != nil {
		t.Fatalf("read the stored settings: %v", err)
	}
	if stored.Engine != application.CategorySuggestionOff {
		t.Errorf("a refused save stored the engine %q, want off", stored.Engine)
	}
}

// TestCategorySuggestionsTryReportsCandidatesWithPercentages pins the Try it box:
// one row per candidate with its share, then the sentence a requester's outcome
// deserves.
func TestCategorySuggestionsTryReportsCandidatesWithPercentages(t *testing.T) {
	h := newHarness(t)
	suggestionCatalog(t, h)

	rec := h.postForm(t, "/settings/category-suggestions/try", url.Values{
		"suggestion_engine": {"local"},
		"suggestion_try":    {"la impresora no imprime"},
	}, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("try = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Printers") || !strings.Contains(body, "100%") {
		t.Fatalf("try must report the candidate with its percentage, got: %s", body)
	}
	if !strings.Contains(body, "Requesters would see these suggestions.") {
		t.Errorf("try must say requesters would see them, got: %s", body)
	}

	// Nothing matching reports the empty state, not an empty box.
	rec = h.postForm(t, "/settings/category-suggestions/try", url.Values{
		"suggestion_engine": {"local"},
		"suggestion_try":    {"no encuentro mi tarjeta de transporte"},
	}, true)
	if !strings.Contains(rec.Body.String(), "No category matched: requesters would choose from the catalog.") {
		t.Errorf("try must report the empty state, got: %s", rec.Body.String())
	}
	if stored, err := h.suggestionSettings.Get(t.Context(), *h.admin); err != nil || stored.Engine != application.CategorySuggestionOff {
		t.Error("the Try it box must save nothing")
	}
}

// TestCategorySuggestionsTrySaysWhenTheModelFellBack keeps the Try it box honest:
// a model that cannot answer is reported, not hidden.
func TestCategorySuggestionsTrySaysWhenTheModelFellBack(t *testing.T) {
	h := newHarness(t)
	suggestionCatalog(t, h)
	h.decisionModel.err = errors.New("dial tcp: connection refused")

	rec := h.postForm(t, "/settings/category-suggestions/try", url.Values{
		"suggestion_engine":   {"model"},
		"suggestion_endpoint": {"https://decisions.example.com/v1"},
		"suggestion_model":    {"gpt-4.1-mini"},
		"suggestion_api_key":  {"sk-secret-value"},
		"suggestion_try":      {"la impresora no imprime"},
	}, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("try = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "The decision model did not answer; these come from On this server.") {
		t.Errorf("try must report the fallback, got: %s", body)
	}
	if strings.Contains(body, "sk-secret-value") {
		t.Error("try must never echo the API key")
	}
}

// TestCategorySuggestionTrySentences renders the Try it result in every state it
// can be in, because the sentence IS the answer: the card has to say whether a
// requester would see these suggestions, and whether the decision model answered
// at all.
func TestCategorySuggestionTrySentences(t *testing.T) {
	cases := []struct {
		name string
		view categorySuggestionTryView
		want []string
		omit []string
	}{
		{
			name: "a confident answer",
			view: categorySuggestionTryView{Candidates: []categorySuggestionTryCandidate{{Name: "Printers", Percent: 72}}, Confident: true},
			want: []string{"Printers", "width:72%", "72%", "Requesters would see these suggestions."},
			omit: []string{"Below the confidence threshold", "did not answer"},
		},
		{
			name: "below the confidence threshold",
			view: categorySuggestionTryView{Candidates: []categorySuggestionTryCandidate{{Name: "Printers", Percent: 31}}},
			want: []string{"31%", "Below the confidence threshold: requesters would choose from the catalog."},
			omit: []string{"Requesters would see these suggestions."},
		},
		{
			name: "the model fell back",
			view: categorySuggestionTryView{Candidates: []categorySuggestionTryCandidate{{Name: "Printers", Percent: 100}}, Confident: true, FellBack: true},
			want: []string{"Requesters would see these suggestions.", "The decision model did not answer; these come from On this server."},
		},
		{
			name: "nothing matched",
			view: categorySuggestionTryView{},
			want: []string{"No category matched: requesters would choose from the catalog."},
			omit: []string{"Requesters would see these suggestions."},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := renderGolden(t, "settings_tickets", "category_suggestion_try", tc.view, true)
			for _, want := range tc.want {
				if !strings.Contains(body, want) {
					t.Errorf("the result must render %q, got: %s", want, body)
				}
			}
			for _, omit := range tc.omit {
				if strings.Contains(body, omit) {
					t.Errorf("the result must not render %q, got: %s", omit, body)
				}
			}
		})
	}
}

// TestCategorySuggestionsTwoStepCatalogNeverSuggestsACategoryThatCannotRun pins
// the server-rendered half of the offer rule: a published category that cannot
// run keeps its disabled card and is never a Suggested link, exactly as the live
// fragment already guarantees.
func TestCategorySuggestionsTwoStepCatalogNeverSuggestsACategoryThatCannotRun(t *testing.T) {
	h := newHarness(t)
	mux := suggestionHarness(t, h)
	suggestionCatalog(t, h)
	catalog := application.NewCatalogService(h.store.CatalogStore(), h.store.CategoryStore(), h.clock)
	department, err := catalog.CreateDepartmentFor(t.Context(), *h.admin, "Engineering", "Engineering department")
	if err != nil {
		t.Fatalf("create department: %v", err)
	}
	desk, err := catalog.CreateDeskFor(t.Context(), *h.admin, department.ID, "Plotting")
	if err != nil {
		t.Fatalf("create desk: %v", err)
	}
	broken, err := h.categories.CreateWithKeywordsFor(t.Context(), *h.admin, "Plotters", "Large format plotters", "plotter, gran formato", desk.ID)
	if err != nil {
		t.Fatalf("create category: %v", err)
	}
	h.publishWorkflow(t, broken.ID, assignToDeskDef(desk.ID))
	if err := h.desks.RemoveMember(t.Context(), *h.admin, desk.ID, h.admin.ID); err != nil {
		t.Fatalf("remove member: %v", err)
	}
	enableLocalSuggestions(t, h)
	setCreationMode(t, h, application.TicketCreationTwoStep)

	_, page := getPath(t, h, mux, "/tickets/new?q=el+plotter+de+gran+formato+no+anda")
	if strings.Contains(page, `data-suggest-category="`+strconv.FormatInt(broken.ID, 10)+`"`) {
		t.Errorf("the two-step catalog must not suggest the category that cannot run, got: %s", page)
	}
	if !strings.Contains(page, "Plotters") {
		t.Errorf("the category that cannot run must keep its disabled card, got: %s", page)
	}
}
