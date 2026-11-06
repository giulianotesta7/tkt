package httpadapter

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// The screen-level defects from the read-only UX audit (issue #343, S1-S6).
// One test per spec: each names the observable effect the rule promises, so a
// surface that regresses fails here instead of in a screenshot review.

// renderAuditPage renders one whole page in an explicit locale without the HX
// header: the path a browser takes, so the shell (title, skip link, rail) is
// part of the assertion.
func renderAuditPage(t *testing.T, locale, page string, data any) string {
	t.Helper()
	r := newTestRenderer(t, stubInterfaceLanguage{language: locale})
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
		{"tickets", "tickets_index", fixtureListData(), "Tickets · Help desk"},
		{"new ticket", "tickets_new", fixtureTicketFormData(), "New ticket · Help desk"},
		{"users", "users_index", fixtureUsersIndexData(), "Users · Help desk"},
		{"settings", "settings_organization", fixtureSettingsOrganizationData(), "Settings · Help desk"},
		{"categories", "categories_index", fixtureCategoriesIndexData(), "Catalog · Help desk"},
		{"category SLA", "category_sla", fixtureCategorySLAData(), "Category SLA · Help desk"},
		{"category workflow", "category_workflow", mobileBuilderPageData(), "Category workflow · Help desk"},
		{"ticket metrics", "ticket_metrics_detail", data, "Ticket metrics · Help desk"},
		{"ticket detail", "tickets_show", fixtureDetailData(), "TKT-1 · Login page down · Help desk"},
		{"login", "login", loginData{}, "Sign in · Help desk"},
		{"setup", "setup", setupData{}, "Create account · Help desk"},
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
	if spanish != "Tickets · Help desk" {
		t.Errorf("es <title> = %q, want the Spanish catalogue's title with the help desk name", spanish)
	}
	escaping := fixtureDetailData()
	escaping.View.Ticket.Title = `VPN <b>drops</b> & reconnects`
	got := documentTitle(t, renderAuditPage(t, "en", "tickets_show", escaping))
	want := "TKT-1 · VPN &lt;b&gt;drops&lt;/b&gt; &amp; reconnects · Help desk"
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
	folder := seedFolder(t, h, nil, "Operations")
	desk, err := h.desks.Create(t.Context(), *h.admin, "Service desk")
	if err != nil {
		t.Fatalf("create desk: %v", err)
	}
	id := func(value int64) string { return strconv.FormatInt(value, 10) }
	bugs := id(h.bugCategory.ID)
	cases := []struct {
		mux  *http.ServeMux
		path string
		want string
	}{
		{h.mux, "/users", "Users · Help desk"},
		{h.mux, "/users/new", "New user · Help desk"},
		{h.mux, "/users/" + id(user.ID) + "/edit", "Edit user · Help desk"},
		{h.mux, "/categories", "Catalog · Help desk"},
		{h.mux, "/categories/new", "New category · Help desk"},
		{h.mux, "/categories/" + bugs + "/edit", "Edit category · Help desk"},
		{h.mux, "/categories/" + bugs + "/move", "Move “Bugs” · Help desk"},
		{h.mux, "/categories/folders/new", "New folder · Help desk"},
		{h.mux, "/categories/folders/" + id(folder) + "/edit", "Edit folder · Help desk"},
		{h.mux, "/categories/folders/" + id(folder) + "/move", "Move “Operations” · Help desk"},
		{h.mux, "/desks", "Desks · Help desk"},
		{h.mux, "/desks/new", "New desk · Help desk"},
		{h.mux, "/desks/" + id(desk.ID) + "/edit", "Edit desk · Help desk"},
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
	if got := documentTitle(t, rejected.Body.String()); got != "Edit user · Help desk" {
		t.Errorf("rejected user edit <title> = %q, want %q", got, "Edit user · Help desk")
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

// TestS5SkipLinkAndCurrentRailLink is S5: a skip link reaches the main content
// and is visible on focus, and the rail link for the current section carries
// aria-current so assistive technology can name the location.
func TestS5SkipLinkAndCurrentRailLink(t *testing.T) {
	body := renderAuditPage(t, "en", "settings_organization", fixtureSettingsOrganizationData())
	page, stylesheet := splitStylesheet(t, "audit343_s5", body)

	if !strings.Contains(page, `<a class="skip-link" href="#main-content">Skip to main content</a>`) {
		t.Errorf("the shell must render a skip link to the main content")
	}
	if !strings.Contains(page, `<main class="main" id="main-content">`) {
		t.Errorf("the main region must carry the skip link's target id")
	}
	// The skip link is the first thing a keyboard reaches: it precedes the sidebar.
	if strings.Index(page, `class="skip-link"`) > strings.Index(page, `class="sidebar"`) {
		t.Errorf("the skip link must come before the sidebar in document order")
	}
	if !cssRuleDeclares(stylesheet, ".skip-link{", "position:absolute") ||
		!strings.Contains(stylesheet, ".skip-link:focus{") {
		t.Errorf("the skip link must be hidden until it takes focus, got:\n%s", stylesheet)
	}

	current := `<a class="sidebar-link active" href="/settings" data-tip="Settings" aria-current="page">`
	if !strings.Contains(page, current) {
		t.Errorf("the active sidebar link must carry aria-current=\"page\", want %q", current)
	}
	// The rail link is current once, and the settings sub-navigation marks the
	// active section once (issue #383, S2); the mobile tab bar never duplicates
	// the settings section, so the page carries exactly those two marks.
	if got := strings.Count(page, `aria-current="page"`); got != 2 {
		t.Errorf("the rail link and the active section must be current, got %d marks", got)
	}
	if !strings.Contains(page, `href="/settings/organization" aria-current="page"`) {
		t.Errorf("the active settings section must carry aria-current")
	}
	if strings.Contains(page, `href="/tickets" data-tip="Tickets" aria-current`) {
		t.Errorf("an inactive sidebar link must not carry aria-current")
	}

	spanish := renderAuditPage(t, "es", "settings_organization", fixtureSettingsOrganizationData())
	if !strings.Contains(spanish, `>Saltar al contenido principal</a>`) {
		t.Errorf("the skip link copy must follow the instance language")
	}
}

// TestS6DeleteControlsAskBeforeDeleting is the markup half of S6's
// confirmation: every direct delete control is a confirmation trigger that
// names its row, so a single click cannot reach the server, and the page ships
// one dialog whose destructive action is styled apart from cancel.
func TestS6DeleteControlsAskBeforeDeleting(t *testing.T) {
	h := newHarness(t)
	folder := seedFolder(t, h, nil, "Operations")
	if _, err := h.categories.CreateInFolderFor(t.Context(), *h.admin, "Desk incidents", "", "", &folder); err != nil {
		t.Fatalf("create category: %v", err)
	}
	rec := h.get(t, "/categories?folder="+strconv.FormatInt(folder, 10), false)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET folder = %d, want 200", rec.Code)
	}
	body := rec.Body.String()

	for _, want := range []string{
		`data-category-delete="folder" data-delete-confirm="Delete the folder “Operations”?"`,
		`data-category-delete="category" data-delete-confirm="Delete the category “Desk incidents”?"`,
		`hx-post="/categories/folders/` + strconv.FormatInt(folder, 10) + `/delete" hx-target="#categories-background" hx-swap="outerHTML"`,
		`<dialog id="category-delete-dialog" aria-labelledby="category-delete-title" aria-describedby="category-delete-copy">`,
		`data-category-delete-confirm`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the folder view is missing the confirmation contract %q", want)
		}
	}
	// The dialog names what will be deleted at click time, so its own static
	// copy stays generic and its body explains the consequence.
	if !strings.Contains(body, `id="category-delete-copy">This cannot be undone.</p>`) {
		t.Errorf("the confirmation dialog must explain that the delete is permanent")
	}
}
