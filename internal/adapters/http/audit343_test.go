package httpadapter

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
		{"edit user", "users_new", fixtureUserFormData(), "Edit user · tkt"},
		{"settings", "settings_index", fixtureSettingsIndexData(), "Settings · tkt"},
		{"categories", "categories_index", fixtureCategoriesIndexData(), "Categories · tkt"},
		{"edit category", "categories_new", fixtureCategoryFormData(), "Rename category · tkt"},
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
