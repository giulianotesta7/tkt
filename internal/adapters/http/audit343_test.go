package httpadapter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
	"github.com/giulianotesta7/tkt/web/templates"
)

// The screen-level defects from the read-only UX audit (issue #343, S1-S6).
// One test per spec: each names the observable effect the rule promises, so a
// surface that regresses fails here instead of in a screenshot review.

// renderAuditPage renders one whole page in an explicit locale without the HX
// header: the path a browser takes, so the shell (title, skip link, rail) is
// part of the assertion.
func renderAuditPage(t *testing.T, locale, page string, data any) string {
	t.Helper()
	r, err := newRendererWithLanguage(templates.FS, stubInterfaceLanguage{language: locale})
	if err != nil {
		t.Fatalf("newRendererWithLanguage(%q): %v", locale, err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/audit", nil)
	r.Render(rec, req, page, "", data, http.StatusOK)
	if rec.Code != http.StatusOK {
		t.Fatalf("render %s in %q: status %d body %s", page, locale, rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

// documentTitle returns the <title> element's text, or "" when the page did not
// render exactly one.
func documentTitle(t *testing.T, body string) string {
	t.Helper()
	const open, close = "<title>", "</title>"
	start := strings.Index(body, open)
	if start < 0 {
		t.Fatalf("page renders no <title>: %.400s", body)
	}
	rest := body[start+len(open):]
	end := strings.Index(rest, close)
	if end < 0 {
		t.Fatalf("page renders an unterminated <title>: %.400s", body)
	}
	if strings.Count(body, open) != 1 {
		t.Fatalf("page renders more than one <title>: %.400s", body)
	}
	return rest[:end]
}

// TestS1EveryScreenNamesItselfInTheDocumentTitle is S1: each screen's browser
// tab, history entry, and screen-reader announcement names the screen, in the
// instance language, instead of every page sharing one title.
func TestS1EveryScreenNamesItselfInTheDocumentTitle(t *testing.T) {
	data := ticketMetricsRenderData()
	cases := []struct {
		name string
		page string
		data any
		want string
	}{
		{"tickets", "tickets_index", fixtureListData(), "Tickets · tkt"},
		{"new ticket", "tickets_new", fixtureTicketFormData(), "New ticket · tkt"},
		{"users", "users_index", fixtureUsersIndexData(), "Users · tkt"},
		{"settings", "settings_index", fixtureSettingsIndexData(), "Settings · tkt"},
		{"categories", "categories_index", fixtureCategoriesIndexData(), "Categories · tkt"},
		{"category SLA", "category_sla", fixtureCategorySLAData(), "Category SLA · tkt"},
		{"category workflow", "category_workflow", mobileBuilderPageData(), "Category workflow · tkt"},
		{"ticket metrics", "ticket_metrics_detail", data, "Ticket metrics · tkt"},
		{"ticket detail", "tickets_show", fixtureDetailData(), "TKT-1 · Login page down · tkt"},
		{"login", "login", loginData{}, "Sign in · tkt"},
		{"setup", "setup", setupData{}, "Create account · tkt"},
	}

	seen := map[string]string{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := documentTitle(t, renderAuditPage(t, "en", tc.page, tc.data))
			if got != tc.want {
				t.Errorf("<title> = %q, want %q", got, tc.want)
			}
		})
		if other, ok := seen[tc.want]; ok {
			t.Errorf("%s and %s share the title %q, so a tab strip cannot tell them apart", other, tc.name, tc.want)
		}
		seen[tc.want] = tc.name
	}

	// The instance language decides the title, and the ticket title is data:
	// it is escaped, never trusted into the document.
	spanish := documentTitle(t, renderAuditPage(t, "es", "tickets_index", fixtureListData()))
	if spanish != "Tickets · tkt" {
		t.Errorf("es <title> = %q, want the Spanish catalogue's title", spanish)
	}
	escaping := fixtureDetailData()
	escaping.View.Ticket.Title = `VPN <b>drops</b> & reconnects`
	got := documentTitle(t, renderAuditPage(t, "en", "tickets_show", escaping))
	want := "TKT-1 · VPN &lt;b&gt;drops&lt;/b&gt; &amp; reconnects · tkt"
	if got != want {
		t.Errorf("ticket <title> = %q, want the escaped title %q", got, want)
	}
}

// TestS1EveryRouteNamesItsScreen proves S1 where a user actually meets it: the
// document a route returns. The users and categories list pages are also the
// drawer hosts (/users/new, /users/{id}/edit, /categories/{kind}/new and their
// edit routes), so a title block that only knows the list leaves every drawer
// screen sharing one name — which is exactly how the first version of this fix
// passed a template-level test and still shipped the audited defect.
func TestS1EveryRouteNamesItsScreen(t *testing.T) {
	h := newHarness(t)
	user := h.createUser(t, "Dana Requester", "dana@tkt.test", "SuperSecret42!")
	catalog := application.NewCatalogService(h.store.CatalogStore(), h.store.CategoryStore(), h.clock)
	department, err := catalog.CreateDepartmentFor(t.Context(), *h.admin, "Operations", "Operations")
	if err != nil {
		t.Fatalf("create department: %v", err)
	}
	desk, err := catalog.CreateDeskFor(t.Context(), *h.admin, department.ID, "Service desk")
	if err != nil {
		t.Fatalf("create desk: %v", err)
	}
	// The harness registers the category handlers without the catalog
	// projection, so the desk and department drawers need the production
	// composition; the users routes come from the harness mux.
	catalogMux := http.NewServeMux()
	NewCategoryHandlersWithWorkflows(h.categories, h.workflows, h.renderer, catalog).Register(catalogMux)

	id := func(value int64) string { return strconv.FormatInt(value, 10) }
	structure := "?view=structure&department_id=" + id(department.ID)
	cases := []struct {
		mux  *http.ServeMux
		path string
		want string
	}{
		{h.mux, "/users", "Users · tkt"},
		{h.mux, "/users/new", "New user · tkt"},
		{h.mux, "/users/" + id(user.ID) + "/edit", "Edit user · tkt"},
		{catalogMux, "/categories?view=structure", "Categories · tkt"},
		{catalogMux, "/categories/new?view=structure", "New category · tkt"},
		{catalogMux, "/categories/" + id(h.bugCategory.ID) + "/edit?view=structure", "Rename category · tkt"},
		{catalogMux, "/categories/desks/new" + structure, "New desk · tkt"},
		{catalogMux, "/categories/desks/" + id(desk.ID) + "/edit" + structure + "&desk_id=" + id(desk.ID), "Edit desk · tkt"},
		{catalogMux, "/categories/departments/new?view=structure", "New department · tkt"},
		{catalogMux, "/categories/departments/" + id(department.ID) + "/edit?view=structure&department_id=" + id(department.ID), "Edit department · tkt"},
	}
	seen := map[string]string{}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		req.Header.Set("Cookie", sessionCookie+"="+h.adminSession.ID)
		rec := httptest.NewRecorder()
		h.mw.Wrap(tc.mux).ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200: %.400s", tc.path, rec.Code, rec.Body.String())
		}
		if got := documentTitle(t, rec.Body.String()); got != tc.want {
			t.Errorf("GET %s <title> = %q, want %q", tc.path, got, tc.want)
		}
		if other, ok := seen[tc.want]; ok {
			t.Errorf("GET %s and %s share the title %q", other, tc.path, tc.want)
		}
		seen[tc.want] = tc.path
	}

	// The standalone user form is a drawer-fragment host, and one route still
	// reaches it as a document: a native (non-HTMX) POST to the edit route that
	// fails validation re-renders the form page. Its title has to name the edit
	// screen it just failed to save, not the list behind it.
	rejected := h.postForm(t, "/users/"+id(user.ID)+"/edit", url.Values{
		"name":     {""},
		"email":    {"dana@tkt.test"},
		"role":     {string(domain.RoleUser)},
		"password": {"SuperSecret42!"},
	}, false)
	if rejected.Code == http.StatusOK || rejected.Code == http.StatusFound {
		t.Fatalf("POST /users/{id}/edit with an empty name = %d, want a rejected form", rejected.Code)
	}
	if got := documentTitle(t, rejected.Body.String()); got != "Edit user · tkt" {
		t.Errorf("rejected user edit <title> = %q, want %q", got, "Edit user · tkt")
	}
}

// TestS2TicketDetailHasOneHeadingNamingTheTicket is S2: the detail page always
// has exactly one h1, it names the ticket in every state, and the editable
// title never clips silently — it carries the full value and an ellipsis
// affordance the layout baseline recognises.
func TestS2TicketDetailHasOneHeadingNamingTheTicket(t *testing.T) {
	longTitle := "VPN drops every ten minutes on the corporate network at home"
	editable := fixtureDetailData()
	editable.View.Ticket.Title = longTitle
	editable.Values.Title = longTitle

	for _, tc := range []struct {
		name string
		data detailData
	}{
		{"editable", editable},
		{"closed", func() detailData {
			d := closedDetailData(domain.StateClosed)
			d.View.Ticket.Title = longTitle
			return d
		}()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := renderAuditPage(t, "en", "tickets_show", tc.data)
			page, stylesheet := splitStylesheet(t, "audit343_s2", body)
			if got := strings.Count(page, "<h1"); got != 1 {
				t.Fatalf("ticket detail renders %d h1 elements, want exactly one:\n%s", got, page)
			}
			heading := section(page, "<h1", "</h1>")
			if !strings.Contains(heading, longTitle) {
				t.Errorf("the h1 must name the ticket, got %q", heading)
			}
			if !strings.Contains(stylesheet, ".title-edit .title-input{") {
				t.Fatalf("the editable title needs its own style rule, got:\n%s", stylesheet)
			}
			if !cssRuleDeclares(stylesheet, ".title-edit .title-input{", "text-overflow:ellipsis") {
				t.Errorf("the editable title must show an ellipsis instead of clipping silently")
			}
		})
	}

	body := renderAuditPage(t, "en", "tickets_show", editable)
	if want := `title="` + longTitle + `"`; !strings.Contains(body, want) {
		t.Errorf("the editable title must carry the full value in its title attribute, want %q", want)
	}
}

// section returns the first element starting at open whose content follows it,
// up to the first close. It is enough for an h1 whose content carries no nested
// h1.
func section(body, open, close string) string {
	start := strings.Index(body, open)
	if start < 0 {
		return ""
	}
	rest := body[start:]
	end := strings.Index(rest, close)
	if end < 0 {
		return rest
	}
	return rest[:end]
}

// TestS3DescriptionKeepsTheAuthorsLineBreaks is S3: the description the author
// typed keeps its line breaks, so "1. Connect" and "2. Wait" are two lines on
// the page exactly as they are in the stored ticket.
func TestS3DescriptionKeepsTheAuthorsLineBreaks(t *testing.T) {
	data := fixtureDetailData()
	data.View.Ticket.Description = "Steps to reproduce:\n1. Connect\n2. Wait"
	body := renderAuditPage(t, "en", "tickets_show", data)
	page, stylesheet := splitStylesheet(t, "audit343_s3", body)

	description := section(page, `<p class="ticket-description">`, "</p>")
	if !strings.Contains(description, "Steps to reproduce:\n1. Connect\n2. Wait") {
		t.Errorf("the description must keep its line breaks, got %q", description)
	}
	if !cssRuleDeclares(stylesheet, ".ticket-description{", "white-space:pre-line") {
		t.Errorf("the description paragraph must render its line breaks, got:\n%s", stylesheet)
	}
}

// TestS4EnglishReadsOneAccountAndTwoAccounts is S4: the English plural has a
// singular form, while Spanish keeps the pair it already had.
func TestS4EnglishReadsOneAccountAndTwoAccounts(t *testing.T) {
	one := fixtureUsersIndexData()
	one.Counts = usersCounts{All: 1, Active: 1}
	body := renderAuditPage(t, "en", "users_index", one)
	if !strings.Contains(body, "1 account<") {
		t.Errorf("one user must read %q, got: %.800s", "1 account", body)
	}
	if strings.Contains(body, "1 accounts") {
		t.Errorf("one user must never read %q, got: %.800s", "1 accounts", body)
	}

	two := fixtureUsersIndexData()
	two.Counts = usersCounts{All: 2, Active: 2}
	if body := renderAuditPage(t, "en", "users_index", two); !strings.Contains(body, "2 accounts<") {
		t.Errorf("two users must read %q, got: %.800s", "2 accounts", body)
	}

	spanish := fixtureUsersIndexData()
	spanish.Counts = usersCounts{All: 1, Active: 1}
	if body := renderAuditPage(t, "es", "users_index", spanish); !strings.Contains(body, "1 cuenta<") {
		t.Errorf("es one user must read %q, got: %.800s", "1 cuenta", body)
	}
}

// TestS5SkipLinkAndCurrentRailLink is S5: a skip link reaches the main content
// and is visible on focus, and the rail link for the current section carries
// aria-current so assistive technology can name the location.
func TestS5SkipLinkAndCurrentRailLink(t *testing.T) {
	body := renderAuditPage(t, "en", "settings_index", fixtureSettingsIndexData())
	page, stylesheet := splitStylesheet(t, "audit343_s5", body)

	if !strings.Contains(page, `<a class="skip-link" href="#main-content">Skip to main content</a>`) {
		t.Errorf("the shell must render a skip link to the main content")
	}
	if !strings.Contains(page, `<main class="main" id="main-content">`) {
		t.Errorf("the main region must carry the skip link's target id")
	}
	// The skip link is the first thing a keyboard reaches: it precedes the rail.
	if strings.Index(page, `class="skip-link"`) > strings.Index(page, `class="rail"`) {
		t.Errorf("the skip link must come before the rail in document order")
	}
	if !cssRuleDeclares(stylesheet, ".skip-link{", "position:absolute") ||
		!strings.Contains(stylesheet, ".skip-link:focus{") {
		t.Errorf("the skip link must be hidden until it takes focus, got:\n%s", stylesheet)
	}

	current := `<a class="rail-link active" href="/settings" title="Settings" aria-label="Settings" aria-current="page">`
	if !strings.Contains(page, current) {
		t.Errorf("the active rail link must carry aria-current=\"page\", want %q", current)
	}
	if got := strings.Count(page, `aria-current="page"`); got != 1 {
		t.Errorf("exactly one rail link may be current, got %d", got)
	}
	if strings.Contains(page, `href="/tickets" title="Tickets" aria-label="Tickets" aria-current`) {
		t.Errorf("an inactive rail link must not carry aria-current")
	}

	spanish := renderAuditPage(t, "es", "settings_index", fixtureSettingsIndexData())
	if !strings.Contains(spanish, `>Saltar al contenido principal</a>`) {
		t.Errorf("the skip link copy must follow the instance language")
	}
}

// TestS6RefusedDeleteKeepsTheStructureSelection is S6's server half: a delete
// the store refuses answers with the translated error and the structure view
// the operator was on — same department, same desk — instead of a landing page
// on the POST URL. The confirmation dialog itself is browser behaviour and is
// covered by e2e/tests/categories.spec.ts.
func TestS6RefusedDeleteKeepsTheStructureSelection(t *testing.T) {
	h := newHarness(t)
	catalog := application.NewCatalogService(h.store.CatalogStore(), h.store.CategoryStore(), h.clock)
	department, err := catalog.CreateDepartmentFor(t.Context(), *h.admin, "Operations", "Operations")
	if err != nil {
		t.Fatalf("create department: %v", err)
	}
	desk, err := catalog.CreateDeskFor(t.Context(), *h.admin, department.ID, "Service desk")
	if err != nil {
		t.Fatalf("create desk: %v", err)
	}
	// A category in the desk makes the desk referenced, so the store refuses
	// the delete exactly as the audit's reproduction step describes.
	if _, err := h.categories.CreateWithDescriptionFor(t.Context(), *h.admin, "Desk incidents", "", desk.ID); err != nil {
		t.Fatalf("create category: %v", err)
	}

	mux := http.NewServeMux()
	NewCategoryHandlersWithWorkflows(h.categories, h.workflows, h.renderer, catalog).Register(mux)
	post := func(hx bool, form url.Values) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost,
			"/categories/desks/"+strconv.FormatInt(desk.ID, 10)+"/delete",
			strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Cookie", sessionCookie+"="+h.adminSession.ID)
		if hx {
			req.Header.Set("HX-Request", "true")
		}
		rec := httptest.NewRecorder()
		h.mw.Wrap(mux).ServeHTTP(rec, req)
		return rec
	}
	selection := url.Values{
		"view":          {"structure"},
		"department_id": {strconv.FormatInt(department.ID, 10)},
		"desk_id":       {strconv.FormatInt(desk.ID, 10)},
	}

	rec := post(true, selection)
	if rec.Code != http.StatusConflict {
		t.Errorf("refused desk delete = %d, want 409: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("HX-Retarget"); got != "#categories-background" {
		t.Errorf("HX-Retarget = %q, want the structure region", got)
	}
	if got := rec.Header().Get("HX-Reswap"); got != "outerHTML" {
		t.Errorf("HX-Reswap = %q, want outerHTML", got)
	}
	fragment := rec.Body.String()
	for _, want := range []string{
		`<div class="error-banner" role="alert">desk is referenced and cannot be deleted</div>`,
		`href="/categories?view=structure&department_id=` + strconv.FormatInt(department.ID, 10) + `&desk_id=` + strconv.FormatInt(desk.ID, 10) + `" aria-current="true"`,
		`<strong>Service desk</strong>`,
	} {
		if !strings.Contains(fragment, want) {
			t.Errorf("the refused delete must keep the structure selection, missing %q in: %.1200s", want, fragment)
		}
	}
	if strings.Contains(fragment, "<html") {
		t.Errorf("an HTMX refusal must swap the structure region, not a whole page")
	}

	// The same refusal in a Spanish instance renders the translated error, and
	// the native (no-HTML-override) path keeps the selection too.
	if err := h.settings.SetInterfaceLanguage(t.Context(), *h.admin, application.InterfaceLanguageSpanish); err != nil {
		t.Fatalf("set interface language: %v", err)
	}
	spanish := post(false, selection)
	if spanish.Code != http.StatusConflict {
		t.Errorf("native refused delete = %d, want 409", spanish.Code)
	}
	if body := spanish.Body.String(); !strings.Contains(body, "la mesa está en uso y no se puede eliminar") {
		t.Errorf("the refusal must be translated, got: %.800s", body)
	}
	if body := spanish.Body.String(); !strings.Contains(body, `aria-current="true"`) {
		t.Errorf("the native refusal must keep the structure selection, got: %.800s", body)
	}
}

// TestS6DeleteControlsAskBeforeDeleting is the markup half of S6's
// confirmation: every direct delete control is a confirmation trigger that
// names its row, so a single click cannot reach the server, and the page ships
// one dialog whose destructive action is styled apart from cancel.
func TestS6DeleteControlsAskBeforeDeleting(t *testing.T) {
	h := newHarness(t)
	catalog := application.NewCatalogService(h.store.CatalogStore(), h.store.CategoryStore(), h.clock)
	department, err := catalog.CreateDepartmentFor(t.Context(), *h.admin, "Operations", "Operations")
	if err != nil {
		t.Fatalf("create department: %v", err)
	}
	desk, err := catalog.CreateDeskFor(t.Context(), *h.admin, department.ID, "Service desk")
	if err != nil {
		t.Fatalf("create desk: %v", err)
	}
	if _, err := h.categories.CreateWithDescriptionFor(t.Context(), *h.admin, "Desk incidents", "", desk.ID); err != nil {
		t.Fatalf("create category: %v", err)
	}
	mux := http.NewServeMux()
	NewCategoryHandlersWithWorkflows(h.categories, h.workflows, h.renderer, catalog).Register(mux)
	req := httptest.NewRequest(http.MethodGet,
		"/categories?view=structure&department_id="+strconv.FormatInt(department.ID, 10)+"&desk_id="+strconv.FormatInt(desk.ID, 10), nil)
	req.Header.Set("Cookie", sessionCookie+"="+h.adminSession.ID)
	rec := httptest.NewRecorder()
	h.mw.Wrap(mux).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET structure = %d, want 200", rec.Code)
	}
	body := rec.Body.String()

	for _, want := range []string{
		`data-category-delete="department" data-delete-confirm="Delete the department “Operations”?"`,
		`data-category-delete="desk" data-delete-confirm="Delete the desk “Service desk”?"`,
		`data-category-delete="category" data-delete-confirm="Delete the category “Desk incidents”?"`,
		`hx-post="/categories/desks/` + strconv.FormatInt(desk.ID, 10) + `/delete" hx-target="#categories-background" hx-swap="outerHTML"`,
		`<dialog id="category-delete-dialog" aria-labelledby="category-delete-title" aria-describedby="category-delete-copy">`,
		`data-category-delete-confirm`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the structure view is missing the confirmation contract %q", want)
		}
	}
	// The dialog names what will be deleted at click time, so its own static
	// copy stays generic and its body explains the consequence.
	if !strings.Contains(body, `id="category-delete-copy">This cannot be undone.</p>`) {
		t.Errorf("the confirmation dialog must explain that the delete is permanent")
	}
}

// TestS6RefusalHelperParsesTheFormItself pins that the refusal renderer reads
// the operator's selection even when its caller did not parse the form first,
// so a future delete path cannot silently drop the structure selection
// (native review R3-002 on #343).
func TestS6RefusalHelperParsesTheFormItself(t *testing.T) {
	h := newHarness(t)
	catalog := application.NewCatalogService(h.store.CatalogStore(), h.store.CategoryStore(), h.clock)
	department, err := catalog.CreateDepartmentFor(t.Context(), *h.admin, "Operations", "Operations")
	if err != nil {
		t.Fatalf("create department: %v", err)
	}
	desk, err := catalog.CreateDeskFor(t.Context(), *h.admin, department.ID, "Service desk")
	if err != nil {
		t.Fatalf("create desk: %v", err)
	}
	handlers := NewCategoryHandlersWithWorkflows(h.categories, h.workflows, h.renderer, catalog)
	selection := url.Values{
		"view":          {"structure"},
		"department_id": {strconv.FormatInt(department.ID, 10)},
		"desk_id":       {strconv.FormatInt(desk.ID, 10)},
	}
	req := httptest.NewRequest(http.MethodPost, "/categories/desks/"+strconv.FormatInt(desk.ID, 10)+"/delete",
		strings.NewReader(selection.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	req = req.WithContext(context.WithValue(req.Context(), ctxKeyUser{}, h.admin))
	rec := httptest.NewRecorder()
	handlers.renderCategoryDeleteRefusal(rec, req, &domain.ReferencedError{Kind: "desk", ID: desk.ID})
	if rec.Code != http.StatusConflict {
		t.Fatalf("refusal status = %d, want 409: %.600s", rec.Code, rec.Body.String())
	}
	want := `href="/categories?view=structure&department_id=` + strconv.FormatInt(department.ID, 10) +
		`&desk_id=` + strconv.FormatInt(desk.ID, 10) + `" aria-current="true"`
	if !strings.Contains(rec.Body.String(), want) {
		t.Errorf("an unparsed refusal request must keep the structure selection, missing %q in: %.1200s", want, rec.Body.String())
	}
}

// TestStandaloneCategoryFormPageUsesTheShellTitle pins that the standalone
// category form page has no title of its own: it only hosts the drawer
// fragment, every category drawer screen is titled through its route
// (TestS1EveryRouteNamesItsScreen), and categories_new.golden holds the shell
// fallback on purpose (native review R3-003 on #343).
func TestStandaloneCategoryFormPageUsesTheShellTitle(t *testing.T) {
	if got := documentTitle(t, renderAuditPage(t, "en", "categories_new", fixtureCategoryFormData())); got != "tkt — Ticket Desk" {
		t.Errorf("standalone category form <title> = %q, want the shell fallback", got)
	}
}
