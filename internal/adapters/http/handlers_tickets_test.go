package httpadapter

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// ticketForm builds a valid create-ticket form; mod may override fields.
func ticketForm(mod func(url.Values)) url.Values {
	f := url.Values{
		"title":           {"Login page down"},
		"description":     {"The login form 500s on submit"},
		"requester_name":  {"Ana Torres"},
		"requester_email": {"ana@example.com"},
		"category_id":     {"1"},
		"priority":        {"high"},
	}
	if mod != nil {
		mod(f)
	}
	return f
}

// Amendment 2 (WB): creation is strictly unassigned. The mere PRESENCE of a
// user_id or assignee_id parameter — ANY value, including the empties a stale
// cached form submits — must be rejected with the typed assignee validation
// error through renderCreateError (422) with ZERO ticket/pin/run/audit rows,
// for every role. No silent drop, no normalization, no hidden direct path.
func TestCreateTicket_UnassignedOnly(t *testing.T) {
	const wantMsg = "tickets are created unassigned — assignment happens later through the category flow"

	roles := []struct {
		name string
		role domain.Role
	}{
		{"user", domain.RoleUser},
		{"agent", domain.RoleAgent},
		{"admin", domain.RoleAdmin},
		{"root", domain.RoleRoot},
	}
	params := []string{"user_id", "assignee_id"}
	values := []struct {
		name  string
		value func(h *harness) string
	}{
		{"empty value (stale cached form)", func(*harness) string { return "" }},
		{"populated valid staff id", func(h *harness) string { return strconv.FormatInt(h.admin.ID, 10) }},
		{"populated unknown id", func(*harness) string { return "9999" }},
	}

	for _, role := range roles {
		t.Run("role="+role.name, func(t *testing.T) {
			for _, param := range params {
				for _, v := range values {
					t.Run(param+"/"+v.name, func(t *testing.T) {
						h := newHarness(t)
						actor := seedUserRole(t, h.store, role.name, role.name+"-creator@tkt.test", role.role)
						sess := seedSession(t, h.store, actor.ID)

						form := ticketForm(func(f url.Values) {
							f.Set("category_id", strconv.FormatInt(h.bugCategory.ID, 10))
							f.Set(param, v.value(h))
						})
						rec := h.postFormAs(t, "/tickets", form, sess.ID)

						if rec.Code != http.StatusUnprocessableEntity {
							t.Errorf("status = %d, want 422 (body %.300s)", rec.Code, rec.Body.String())
						}
						if !strings.Contains(rec.Body.String(), wantMsg) {
							t.Errorf("re-render must carry the typed assignee message %q, got: %.400s", wantMsg, rec.Body.String())
						}

						// A rejected creation persists NOTHING: no ticket, no pin
						// (tickets row), no run, no audit of any kind.
						db := h.rawDB(t)
						for _, probe := range []struct {
							what  string
							query string
						}{
							{"tickets", "SELECT COUNT(*) FROM tickets"},
							{"workflow runs", "SELECT COUNT(*) FROM ticket_workflow_runs"},
							{"audit events", "SELECT COUNT(*) FROM audit_events"},
						} {
							if n := scanOneInt(t, db, probe.query); n != 0 {
								t.Errorf("%s rows = %d, want 0 (rejected creation must write nothing)", probe.what, n)
							}
						}
					})
				}
			}
		})
	}
}

// TestCreateTicket_UnassignedOnly_Precedence proves the assignee presence
// check runs BEFORE any other binding/validation: an assignee-carrying form
// that ALSO lacks a title answers the assignee message, not the title one.
func TestCreateTicket_UnassignedOnly_Precedence(t *testing.T) {
	h := newHarness(t)
	form := ticketForm(func(f url.Values) {
		f.Set("title", "   ")
		f.Set("user_id", strconv.FormatInt(h.admin.ID, 10))
	})
	rec := h.postForm(t, "/tickets", form, false)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "tickets are created unassigned") {
		t.Errorf("assignee presence must win over later validation, got: %.400s", rec.Body.String())
	}
}

// TestCreateTicket_UnassignedOnly_PositiveControl proves the rejection is not
// over-broad: the same form WITHOUT any assignee parameter still creates an
// unassigned ticket with its active pinned run.
func TestCreateTicket_UnassignedOnly_PositiveControl(t *testing.T) {
	h := newHarness(t)
	rec := h.postForm(t, "/tickets", ticketForm(func(f url.Values) {
		f.Set("category_id", strconv.FormatInt(h.bugCategory.ID, 10))
	}), false)
	wantRedirect(t, rec, http.StatusSeeOther, "/tickets")

	view, err := h.tickets.GetByID(t.Context(), *h.admin, 1)
	if err != nil {
		t.Fatalf("created ticket must be readable: %v", err)
	}
	if view.Ticket.UserID != nil {
		t.Errorf("every created ticket starts unassigned, got assignee %d", *view.Ticket.UserID)
	}
	db := h.rawDB(t)
	if n := scanOneInt(t, db, "SELECT COUNT(*) FROM ticket_workflow_runs WHERE ticket_id=1"); n != 1 {
		t.Errorf("workflow runs for created ticket = %d, want 1 active run", n)
	}
}

// TestRenderNewTicketFormNoAssigneeControl proves the create form renders NO
// assignee selector or input for ANY role (Amendment 2 WB.3), while the
// detail-page assignment control keeps its own separate plumbing.
func TestRenderNewTicketFormNoAssigneeControl(t *testing.T) {
	roles := []domain.Role{domain.RoleUser, domain.RoleAgent, domain.RoleAdmin, domain.RoleRoot}
	for _, role := range roles {
		t.Run(string(role), func(t *testing.T) {
			h := newHarness(t)
			if role != domain.RoleAdmin {
				actor := seedUserRole(t, h.store, string(role), string(role)+"@tkt.test", role)
				sess := seedSession(t, h.store, actor.ID)
				req := httptest.NewRequest(http.MethodGet, "/tickets/new", nil)
				req.Header.Set("Cookie", sessionCookie+"="+sess.ID)
				rec := httptest.NewRecorder()
				h.mw.Wrap(h.mux).ServeHTTP(rec, req)
				assertNewFormHasNoAssignee(t, rec)
				return
			}
			assertNewFormHasNoAssignee(t, h.get(t, "/tickets/new", false))
		})
	}

	// The detail-page assignment flow keeps its own select (POST
	// /tickets/{id}/assign plumbing stays untouched).
	h := newHarness(t)
	tkt := h.seedTicket(t, "detail assign control", nil)
	detail := h.get(t, "/tickets/"+strconv.FormatInt(tkt.ID, 10), false)
	if detail.Code != http.StatusOK || !strings.Contains(detail.Body.String(), `id="assign-user"`) {
		t.Errorf("detail-page assignment UI must stay unaffected (status %d)", detail.Code)
	}
}

func assertNewFormHasNoAssignee(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, absent := range []string{`Assigned user`, `id="user_id"`, `name="user_id"`} {
		if strings.Contains(body, absent) {
			t.Errorf("create form must not render assignee control %q for any role, got: %.500s", absent, body)
		}
	}
}

// TestRootRedirectsToTickets proves GET / redirects 303 to /tickets.
func TestRootRedirectsToTickets(t *testing.T) {
	h := newHarness(t)
	rec := h.get(t, "/", false)
	wantRedirect(t, rec, http.StatusSeeOther, "/tickets")
}

// TestTicketsIndexEmpty proves the empty list renders the index page with
// the empty state.
func TestTicketsIndexEmpty(t *testing.T) {
	h := newHarness(t)
	rec := h.get(t, "/tickets", false)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "<!DOCTYPE html>") {
		t.Error("full page must include the shell")
	}
	for _, want := range []string{"No tickets yet", "0 tickets"} {
		if !strings.Contains(body, want) {
			t.Errorf("unfiltered empty list must show %q, got: %s", want, body)
		}
	}
	if strings.Contains(body, `class="pagination"`) {
		t.Errorf("unfiltered empty list must hide pagination, got: %s", body)
	}
}

// TestTicketsIndexRows proves seeded tickets render with readable numbers
// (TKT-N) and titles in newest-first order.
func TestTicketsIndexFilteredEmpty(t *testing.T) {
	h := newHarness(t)
	rec := h.get(t, "/tickets?q=absent", false)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"No tickets match your filters", `href="/tickets"`, `aria-label="Clear filters"`} {
		if !strings.Contains(body, want) {
			t.Errorf("filtered empty list must contain %q, got: %s", want, body)
		}
	}
	if strings.Contains(body, "No tickets yet") {
		t.Errorf("filtered empty list must not use the unfiltered copy, got: %s", body)
	}
}

func TestTicketsPaginationHiddenForSingleResult(t *testing.T) {
	h := newHarness(t)
	h.seedTicket(t, "Only ticket", nil)

	body := h.get(t, "/tickets", false).Body.String()
	if !strings.Contains(body, "1 ticket") {
		t.Errorf("single result must use singular scoped total, got: %s", body)
	}
	if strings.Contains(body, `class="pagination"`) {
		t.Errorf("single result must hide pagination, got: %s", body)
	}
}

func TestTicketsPaginationHrefPreservesQuery(t *testing.T) {
	f := filterState{
		State:      domain.StateInProgress,
		Priority:   domain.PriorityHigh,
		CategoryID: "12",
		UserID:     "34",
		Q:          "network outage",
	}
	want := "/tickets?category_id=12&page=3&priority=high&q=network+outage&state=in_progress&user_id=34"
	if got := listHref(f, 3); got != want {
		t.Errorf("listHref = %q, want %q", got, want)
	}
}

// TestParseFiltersSort (issue #211, PR 4, T5) proves each accepted sort value
// sets exactly its query flag and that an unknown, empty, or absent value
// silently selects the default newest-first order: a sort is not a filter, so
// it never produces a 422 or an error page (threat matrix).
func TestParseFiltersSort(t *testing.T) {
	for _, tc := range []struct {
		name         string
		query        string
		wantSort     string
		wantPriority bool
		wantUrgency  bool
	}{
		{name: "newest", query: "?sort=newest", wantSort: sortNewest},
		{name: "priority", query: "?sort=priority", wantSort: sortPriority, wantPriority: true},
		{name: "urgency", query: "?sort=urgency", wantSort: sortUrgency, wantUrgency: true},
		{name: "unknown", query: "?sort=bogus", wantSort: sortNewest},
		{name: "empty", query: "?sort=", wantSort: sortNewest},
		{name: "absent", query: "", wantSort: sortNewest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := parseFilters(httptest.NewRequest(http.MethodGet, "/tickets"+tc.query, nil))
			if f.Sort != tc.wantSort {
				t.Errorf("Sort = %q, want %q", f.Sort, tc.wantSort)
			}
			q := f.query()
			if q.SortByPriority != tc.wantPriority || q.SortByUrgency != tc.wantUrgency {
				t.Errorf("query flags = (priority %t, urgency %t), want (priority %t, urgency %t)",
					q.SortByPriority, q.SortByUrgency, tc.wantPriority, tc.wantUrgency)
			}
		})
	}
}

// TestTicketHrefsPreserveSort (issue #211, PR 4, T5) proves both href
// builders keep an explicit non-default ordering so paging and the agent
// queue preserve the chosen order, while the default newest-first order is
// never written into a URL — no explicit choice keeps generated URLs
// byte-identical to before.
func TestTicketHrefsPreserveSort(t *testing.T) {
	f := filterState{Sort: sortUrgency}
	if got, want := listHref(f, 2), "/tickets?page=2&sort=urgency"; got != want {
		t.Errorf("listHref = %q, want %q", got, want)
	}
	if got, want := agentListHref(f, 2, 1), "/tickets?assigned_page=2&sort=urgency"; got != want {
		t.Errorf("agentListHref = %q, want %q", got, want)
	}
	if got := ticketFilterValues(f).Get("sort"); got != sortUrgency {
		t.Errorf("ticketFilterValues sort = %q, want %q", got, sortUrgency)
	}

	for _, tc := range []struct {
		name string
		sort string
	}{
		{name: "empty default", sort: ""},
		{name: "explicit newest", sort: sortNewest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := filterState{Sort: tc.sort}
			if got := listHref(f, 1); got != "/tickets" {
				t.Errorf("listHref = %q, want /tickets (default must not write sort)", got)
			}
			if got := ticketFilterValues(f).Get("sort"); got != "" {
				t.Errorf("ticketFilterValues sort = %q, want empty", got)
			}
		})
	}
}

// TestTicketsIndexSortRoundTrip (issue #211, PR 4, T5) proves the visible
// order control round-trips: ?sort=urgency selects the Urgency option and
// survives into the pagination hrefs, an unknown value is silently ignored
// (never echoed as a selected option), and no explicit sort keeps the default
// newest-first selection with clean hrefs.
func TestTicketsIndexSortRoundTrip(t *testing.T) {
	h := newHarness(t)
	for i := 0; i < 11; i++ {
		h.seedTicket(t, "Paged ticket "+string(rune('A'+i)), nil)
	}

	urgency := h.get(t, "/tickets?sort=urgency", false).Body.String()
	if !strings.Contains(urgency, `<option value="urgency" selected>Urgency</option>`) {
		t.Errorf("sort=urgency must mark the Urgency option selected, got: %s", urgency)
	}
	if !strings.Contains(urgency, `page=2&amp;sort=urgency`) {
		t.Errorf("pagination hrefs must carry sort=urgency, got: %s", urgency)
	}

	unknown := h.get(t, "/tickets?sort=bogus", false).Body.String()
	for _, absent := range []string{`value="bogus"`, `sort=bogus`} {
		if strings.Contains(unknown, absent) {
			t.Errorf("unknown sort must be ignored, not echoed as %q, got: %s", absent, unknown)
		}
	}
	if !strings.Contains(unknown, `<option value="newest" selected>Newest first</option>`) {
		t.Errorf("unknown sort must select the default newest-first option, got: %s", unknown)
	}

	plain := h.get(t, "/tickets", false).Body.String()
	if !strings.Contains(plain, `<option value="newest" selected>Newest first</option>`) {
		t.Errorf("default sort must mark Newest first selected, got: %s", plain)
	}
	if strings.Contains(plain, "sort=urgency") || strings.Contains(plain, "sort=priority") {
		t.Errorf("no explicit sort must not write a sort into any URL, got: %s", plain)
	}
}

// TestTicketsIndexSortByUrgency (issue #211, PR 4, T5) proves the parsed sort
// reaches the query layer. SLA is enabled through the real settings route so
// each create freezes a real commitment (a fresh commitment is on_track with
// a pending milestone). The high-priority ticket with a PENDING response is
// due before the critical ticket whose response was MET (it then waits on its
// resolve deadline), so the urgency order is the reverse of both the
// newest-first order and the priority order.
func TestTicketsIndexSortByUrgency(t *testing.T) {
	h := newHarness(t)
	form := slaPanelForm("80")
	form.Set("sla_enabled", "1")
	if rec := h.postForm(t, "/settings/sla", form, false); rec.Code != http.StatusSeeOther {
		t.Fatalf("enable SLA: status = %d, want %d", rec.Code, http.StatusSeeOther)
	}

	urgent := h.seedTicket(t, "Urgent pending response", func(in *application.CreateTicketInput) {
		in.Priority = domain.PriorityHigh
	})
	relaxed := h.seedTicket(t, "Relaxed met response", func(in *application.CreateTicketInput) {
		in.Priority = domain.PriorityCritical
	})
	if _, err := h.comments.Add(t.Context(), *h.admin, relaxed.ID, "On it", "public"); err != nil {
		t.Fatalf("freeze a met first response: %v", err)
	}

	ordered := func(body string, first, second *domain.Ticket) bool {
		i, j := strings.Index(body, first.Title), strings.Index(body, second.Title)
		if i < 0 || j < 0 {
			t.Fatalf("list must render %q and %q, got: %s", first.Title, second.Title, body)
		}
		return i < j
	}

	urgency := h.get(t, "/tickets?sort=urgency", false).Body.String()
	if got := strings.Count(urgency, `class="badge on_track"`); got < 2 {
		t.Fatalf("both tickets must carry a frozen on_track commitment, got %d badges: %s", got, urgency)
	}
	if !ordered(urgency, urgent, relaxed) {
		t.Errorf("?sort=urgency must return the urgent-first order, got: %s", urgency)
	}
	if body := h.get(t, "/tickets", false).Body.String(); !ordered(body, relaxed, urgent) {
		t.Errorf("no sort must keep the newest-first order, got: %s", body)
	}
}

func TestTicketsIndexRows(t *testing.T) {
	h := newHarness(t)
	first := h.seedTicket(t, "First ticket", nil)
	second := h.seedTicket(t, "Second ticket", nil)
	_ = first
	_ = second

	rec := h.get(t, "/tickets", false)
	body := rec.Body.String()

	if !strings.Contains(body, "First ticket") || !strings.Contains(body, "Second ticket") {
		t.Errorf("list must show both tickets, got: %s", body)
	}
	if !strings.Contains(body, "TKT-1") || !strings.Contains(body, "TKT-2") {
		t.Errorf("list must render readable TKT-N numbers, got: %s", body)
	}
	// Newest first (id DESC tiebreak).
	if strings.Index(body, "Second ticket") > strings.Index(body, "First ticket") {
		t.Errorf("newest ticket must sort first, got: %s", body)
	}
}

// TestTicketsIndexHXFragment proves GET /tickets with HX-Request returns only
// the canonical filtered list fragment, without obsolete OOB summary chips.
func TestTicketsIndexHXFragment(t *testing.T) {
	h := newHarness(t)
	h.seedTicket(t, "First ticket", nil)

	rec := h.get(t, "/tickets", true)

	body := rec.Body.String()
	if strings.Contains(body, "<html>") {
		t.Errorf("HX list must not contain <html>, got: %s", body)
	}
	if !strings.Contains(body, "First ticket") {
		t.Errorf("fragment must render the rows, got: %s", body)
	}
	if strings.Contains(body, `hx-swap-oob`) || strings.Contains(body, `id="chips"`) {
		t.Errorf("HX list must not carry obsolete summary chip markup, got: %s", body)
	}
}

// TestTicketsIndexStateFilter proves state filtering (ticket-search spec):
// only matching tickets render and the filter bar remains the active surface.
func TestTicketsIndexStateFilter(t *testing.T) {
	h := newHarness(t)
	a := h.seedTicket(t, "Resolved one", nil)
	h.seedTransition(t, a.ID, domain.StateResolved, "")
	b := h.seedTicket(t, "Cancelled one", nil)
	h.seedTransition(t, b.ID, domain.StateCancelled, "")
	h.seedTicket(t, "Fresh one", nil)

	rec := h.get(t, "/tickets?state=resolved", false)
	body := rec.Body.String()

	if !strings.Contains(body, "Resolved one") {
		t.Errorf("filtered list must include the resolved ticket, got: %s", body)
	}
	if strings.Contains(body, "Fresh one") || strings.Contains(body, "Cancelled one") {
		t.Errorf("filtered list must exclude non-matching tickets, got: %s", body)
	}
	if !strings.Contains(body, `<option value="resolved" selected>Resolved</option>`) {
		t.Errorf("state filter must remain selected with a human label, got: %s", body)
	}
}

func TestTicketsIndexUsesHumanLabelsAndIDHeading(t *testing.T) {
	h := newHarness(t)
	tkt := h.seedTicket(t, "Login page down", nil)
	h.seedTransition(t, tkt.ID, domain.StateInProgress, "")

	body := h.get(t, "/tickets", false).Body.String()
	for _, want := range []string{"<th>ID</th>", ">In Progress</span>", `<span class="ticket-priority-value">Medium</span>`} {
		if !strings.Contains(body, want) {
			t.Errorf("ticket list must contain %q, got: %s", want, body)
		}
	}
	if strings.Contains(body, ">in_progress<") {
		t.Errorf("ticket list must not expose internal enum labels, got: %s", body)
	}
}

// TestTicketsSearchText proves the FTS text filter (q) narrows the list.
func TestTicketsSearchText(t *testing.T) {
	h := newHarness(t)
	h.seedTicket(t, "Network outage", nil)
	h.seedTicket(t, "Printer jam", nil)

	rec := h.get(t, "/tickets?q=network", false)
	body := rec.Body.String()

	if !strings.Contains(body, "Network outage") {
		t.Errorf("search must match the title, got: %s", body)
	}
	if strings.Contains(body, "Printer jam") {
		t.Errorf("search must exclude non-matching tickets, got: %s", body)
	}
}

// TestTicketsIndexRoleSearchControls proves the S2 compact search is the
// single visible text-search control for every role. Admin/root retain advanced
// filters while agent/user role actors receive only the compact control.
func TestTicketsIndexRoleSearchControls(t *testing.T) {
	h := newHarness(t)

	tests := []struct {
		name                string
		role                domain.Role
		wantAdvancedFilters bool
	}{
		{name: "agent", role: domain.RoleAgent, wantAdvancedFilters: false},
		{name: "admin", role: domain.RoleAdmin, wantAdvancedFilters: true},
		{name: "root", role: domain.RoleRoot, wantAdvancedFilters: true},
		{name: "user", role: domain.RoleUser, wantAdvancedFilters: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actor := seedUserRole(t, h.store, tt.name, tt.name+"@example.com", tt.role)
			session := seedSession(t, h.store, actor.ID)
			rec := doRequest(h.mux, h.mw, http.MethodGet, "/tickets?q=printer", map[string]string{
				"Cookie": sessionCookie + "=" + session.ID,
			})
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}

			body := rec.Body.String()
			if got := strings.Count(body, `class="ticket-search search-field"`); got != 1 {
				t.Errorf("compact search controls = %d, want 1, got: %s", got, body)
			}
			if !strings.Contains(body, `<svg class="search-icon" viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" focusable="false">`) {
				t.Errorf("compact search must render the shared decorative search icon, got: %s", body)
			}
			if got := strings.Count(body, `type="search"`); got != 1 {
				t.Errorf("visible q controls = %d, want 1, got: %s", got, body)
			}
			gotAdvancedFilters := strings.Contains(body, `name="state"`)
			if gotAdvancedFilters != tt.wantAdvancedFilters {
				t.Errorf("advanced filters = %t, want %t, got: %s", gotAdvancedFilters, tt.wantAdvancedFilters, body)
			}
		})
	}
}

// TestTicketsSearchUserRoleDoesNotLeakMatchingTickets proves compact searches
// preserve the existing server-side own-ticket scope for user-role actors.
func TestTicketsSearchUserRoleDoesNotLeakMatchingTickets(t *testing.T) {
	h := newHarness(t)
	user := seedUserRole(t, h.store, "Ula", "ula@example.com", domain.RoleUser)
	session := seedSession(t, h.store, user.ID)

	if _, err := h.tickets.Create(t.Context(), *user, application.CreateTicketInput{
		Title: "Shared printer issue", CategoryID: h.bugCategory.ID, Priority: domain.PriorityMedium,
	}); err != nil {
		t.Fatalf("create user ticket: %v", err)
	}
	if _, err := h.tickets.Create(t.Context(), *h.admin, application.CreateTicketInput{
		Title: "Shared printer issue for admin", CategoryID: h.bugCategory.ID, Priority: domain.PriorityMedium,
	}); err != nil {
		t.Fatalf("create other ticket: %v", err)
	}

	rec := doRequest(h.mux, h.mw, http.MethodGet, "/tickets?q=shared+printer", map[string]string{
		"Cookie": sessionCookie + "=" + session.ID,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Shared printer issue") {
		t.Errorf("user search must include own matching ticket, got: %s", body)
	}
	if strings.Contains(body, "Shared printer issue for admin") {
		t.Errorf("user search must exclude another user's matching ticket, got: %s", body)
	}
	if !strings.Contains(body, "1 ticket") || strings.Contains(body, "2 tickets") {
		t.Errorf("user scoped result count must describe only the visible ticket, got: %s", body)
	}
}

// TestTicketsSearchByNumber proves the search box matches the ticket ID
// (TKT-N) as well as the title.
func TestTicketsSearchByNumber(t *testing.T) {
	h := newHarness(t)
	h.seedTicket(t, "Network outage", nil)
	h.seedTicket(t, "Printer jam", nil)

	body := h.get(t, "/tickets?q=TKT-2", false).Body.String()
	if !strings.Contains(body, "Printer jam") {
		t.Errorf("search by TKT-2 must match the ticket, got: %s", body)
	}
	if strings.Contains(body, "Network outage") {
		t.Errorf("search by TKT-2 must exclude other tickets, got: %s", body)
	}
}

// TestTicketsSearchSpecialCharsNo500 proves FTS syntax characters in q never
// error (threat matrix): invalid input degrades to no text filter.
func TestTicketsSearchSpecialCharsNo500(t *testing.T) {
	h := newHarness(t)
	h.seedTicket(t, "Network outage", nil)

	for _, q := range []string{`"`, `(`, `*`, `:`, `a OR b`} {
		rec := h.get(t, "/tickets?q="+url.QueryEscape(q), false)
		if rec.Code != http.StatusOK {
			t.Errorf("q=%q status = %d, want 200", q, rec.Code)
		}
	}
}

// TestTicketsPaginationProves stable pagination: 25 tickets → page 1 has 10,
// page 3 has 5, and the page footer reports "Page X of 3" (D2).
func TestTicketsPagination(t *testing.T) {
	h := newHarness(t)
	for i := 0; i < 25; i++ {
		h.seedTicket(t, "Ticket "+string(rune('A'+i)), nil)
	}

	page1 := h.get(t, "/tickets", false)
	if !strings.Contains(page1.Body.String(), "Page 1 of 3") {
		t.Errorf("page footer must read Page 1 of 3, got: %s", page1.Body.String())
	}
	// 10 rows on page 1: rows are <tr> inside .queue; count occurrences of
	// the class="num" cell.
	if got := strings.Count(page1.Body.String(), `class="num"`); got != 10 {
		t.Errorf("page 1 must show 10 tickets, got %d", got)
	}

	page3 := h.get(t, "/tickets?page=3", false)
	if !strings.Contains(page3.Body.String(), "Page 3 of 3") {
		t.Errorf("page footer must read Page 3 of 3, got: %s", page3.Body.String())
	}
	if got := strings.Count(page3.Body.String(), `class="num"`); got != 5 {
		t.Errorf("page 3 must show 5 tickets, got %d", got)
	}

	// Pages do not overlap: the newest ticket (Ticket Y) appears only on
	// page 1, the oldest (Ticket A) only on page 3.
	if strings.Contains(page3.Body.String(), "Ticket Y") {
		t.Errorf("page 3 must not repeat page-1 tickets, got: %s", page3.Body.String())
	}
	if !strings.Contains(page3.Body.String(), "Ticket A") {
		t.Errorf("page 3 must hold the oldest tickets, got: %s", page3.Body.String())
	}
}

// TestTicketsNewFormRenders proves GET /tickets/new serves the create form
// with category options.
// TestTicketsIndexUserScopeOwnOnly proves a user-role actor's list shows
// ONLY their own tickets, never another requester's (ticket-access spec:
// user SHALL access only tickets they created).
func TestTicketsIndexUserScopeOwnOnly(t *testing.T) {
	h := newHarness(t)
	user := seedUserRole(t, h.store, "Ula", "ula@example.com", domain.RoleUser)
	sess := seedSession(t, h.store, user.ID)

	// Ula's own ticket via the real service (session requester persisted).
	if _, err := h.tickets.Create(t.Context(), *user, application.CreateTicketInput{
		Title: "Ula's ticket", CategoryID: h.bugCategory.ID, Priority: domain.PriorityMedium,
	}); err != nil {
		t.Fatalf("create ula ticket: %v", err)
	}
	// Admin's ticket — must never appear in Ula's list.
	if _, err := h.tickets.Create(t.Context(), *h.admin, application.CreateTicketInput{
		Title: "Admin's ticket", CategoryID: h.bugCategory.ID, Priority: domain.PriorityMedium,
	}); err != nil {
		t.Fatalf("create admin ticket: %v", err)
	}

	rec := doRequest(h.mux, h.mw, "GET", "/tickets", map[string]string{"Cookie": sessionCookie + "=" + sess.ID})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Ula&#39;s ticket") {
		t.Errorf("Ula's list must show her own ticket, got: %s", body)
	}
	if strings.Contains(body, "Admin&#39;s ticket") {
		t.Errorf("Ula's list must NOT show the admin's ticket, got: %s", body)
	}
}

// TestTicketsIndexAgentScopeAssignedOnly proves an agent-role actor's list
// shows ONLY their assigned tickets — never unassigned ones and never
// another agent's (ticket-access spec: agent SHALL access only assigned).
func TestTicketsIndexAgentScopeAssignedOnly(t *testing.T) {
	h := newHarness(t)
	agent := seedUserRole(t, h.store, "Xylo", "xylo@example.com", domain.RoleAgent)
	sess := seedSession(t, h.store, agent.ID)

	if _, err := h.tickets.Create(t.Context(), *h.admin, application.CreateTicketInput{
		Title: "Mine", CategoryID: h.bugCategory.ID, Priority: domain.PriorityMedium,
	}); err != nil {
		t.Fatalf("create unassigned ticket: %v", err)
	}
	h.assignTicket(t, 1, agent.ID)
	if _, err := h.tickets.Create(t.Context(), *h.admin, application.CreateTicketInput{
		Title: "Not mine", CategoryID: h.bugCategory.ID, Priority: domain.PriorityMedium,
	}); err != nil {
		t.Fatalf("create unassigned ticket: %v", err)
	}

	rec := doRequest(h.mux, h.mw, "GET", "/tickets", map[string]string{"Cookie": sessionCookie + "=" + sess.ID})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Mine") {
		t.Errorf("agent's list must show their assigned ticket, got: %s", body)
	}
	if strings.Contains(body, "Not mine") {
		t.Errorf("agent's list must NOT show unassigned tickets, got: %s", body)
	}
}

// TestTicketsIndexRootFullQueue proves a root-role actor sees the full
// queue like admin (ticket-access spec: admin/root full queue).
func TestTicketsIndexRootFullQueue(t *testing.T) {
	h := newHarness(t)
	root := seedUserRole(t, h.store, "Root", "root@example.com", domain.RoleRoot)
	sess := seedSession(t, h.store, root.ID)
	h.seedTicket(t, "seed one", nil)
	h.seedTicket(t, "seed two", nil)

	rec := doRequest(h.mux, h.mw, "GET", "/tickets", map[string]string{"Cookie": sessionCookie + "=" + sess.ID})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "seed one") || !strings.Contains(body, "seed two") {
		t.Errorf("root must see the full queue, got: %s", body)
	}
}

// TestTicketShowUserDeniedOthersTicket proves the detail route is scoped:
// a user-role actor requesting another user's ticket gets 404 — the ticket
// is indistinguishable from a missing one (ticket-access spec: direct
// request for another's ticket is denied).
func TestTicketShowUserDeniedOthersTicket(t *testing.T) {
	h := newHarness(t)
	user := seedUserRole(t, h.store, "Ula", "ula@example.com", domain.RoleUser)
	sess := seedSession(t, h.store, user.ID)

	// Admin creates the ticket; it is NOT Ula's.
	if _, err := h.tickets.Create(t.Context(), *h.admin, application.CreateTicketInput{
		Title: "Admin's private ticket", CategoryID: h.bugCategory.ID, Priority: domain.PriorityMedium,
	}); err != nil {
		t.Fatalf("create ticket: %v", err)
	}

	rec := doRequest(h.mux, h.mw, "GET", "/tickets/1", map[string]string{"Cookie": sessionCookie + "=" + sess.ID})
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (out-of-scope detail must be denied)", rec.Code)
	}
}

// TestTicketShowAgentDeniedUnassigned proves an agent cannot open an
// unassigned ticket by direct lookup (agent scope = assigned only).
func TestTicketShowAgentDeniedUnassigned(t *testing.T) {
	h := newHarness(t)
	agent := seedUserRole(t, h.store, "Xylo", "xylo@example.com", domain.RoleAgent)
	sess := seedSession(t, h.store, agent.ID)

	if _, err := h.tickets.Create(t.Context(), *h.admin, application.CreateTicketInput{
		Title: "Unassigned", CategoryID: h.bugCategory.ID, Priority: domain.PriorityMedium,
	}); err != nil {
		t.Fatalf("create ticket: %v", err)
	}

	rec := doRequest(h.mux, h.mw, "GET", "/tickets/1", map[string]string{"Cookie": sessionCookie + "=" + sess.ID})
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (unassigned ticket out of agent scope)", rec.Code)
	}
}

func TestTicketsNewFormRenders(t *testing.T) {
	h := newHarness(t)
	rec := h.get(t, "/tickets/new", false)

	body := rec.Body.String()
	if !strings.Contains(body, "New ticket") {
		t.Errorf("new form must render, got: %s", body)
	}
	if !strings.Contains(body, "Bugs") {
		t.Errorf("form must offer the category options, got: %s", body)
	}
}

// TestTicketCreateSuccessFullPage proves POST /tickets (full request) stores
// the ticket with a readable number, state new, and redirects 303 to the
// detail page (create-ticket spec).
func TestTicketCreateSuccessFullPage(t *testing.T) {
	h := newHarness(t)
	form := ticketForm(func(f url.Values) { f.Set("category_id", strconv.FormatInt(h.bugCategory.ID, 10)) })

	rec := h.postForm(t, "/tickets", form, false)

	wantRedirect(t, rec, http.StatusSeeOther, "/tickets")

	view, err := h.tickets.GetByID(t.Context(), *h.admin, 1)
	if err != nil {
		t.Fatalf("created ticket must be readable: %v", err)
	}
	if view.Ticket.Number != 1 {
		t.Errorf("number = %d, want 1 (first ticket)", view.Ticket.Number)
	}
	if view.Ticket.State != domain.StateNew {
		t.Errorf("state = %q, want new", view.Ticket.State)
	}
	if view.Ticket.Title != "Login page down" {
		t.Errorf("title = %q, want Login page down", view.Ticket.Title)
	}
}

// TestTicketCreateHXFragment proves the HX create path returns the
// ticket_list fragment instead of redirecting.
func TestTicketCreateHXFragment(t *testing.T) {
	h := newHarness(t)
	form := ticketForm(func(f url.Values) { f.Set("category_id", strconv.FormatInt(h.bugCategory.ID, 10)) })

	rec := h.postForm(t, "/tickets", form, true)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "<html>") {
		t.Errorf("HX create must return the fragment, got: %s", body)
	}
	if !strings.Contains(body, "Login page down") {
		t.Errorf("fragment must show the refreshed list with the new ticket, got: %s", body)
	}
	if strings.Contains(body, `hx-swap-oob`) {
		t.Errorf("fragment must not carry obsolete OOB markup, got: %s", body)
	}
}

// TestTicketCreateMissingTitle422 proves the empty-title rejection: 422 with
// the English message re-rendered on the form, no ticket stored.
func TestTicketCreateMissingTitle422(t *testing.T) {
	h := newHarness(t)
	form := ticketForm(func(f url.Values) { f.Set("title", "   ") })

	rec := h.postForm(t, "/tickets", form, false)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, domain.ErrMsgTitleRequired) {
		t.Errorf("re-render must show %q, got: %s", domain.ErrMsgTitleRequired, body)
	}
	for _, want := range []string{`name="title" value="   "`, `The login form 500s on submit`, `<option value="high" selected>High</option>`} {
		if !strings.Contains(body, want) {
			t.Errorf("422 re-render must preserve submitted value %q, got: %s", want, body)
		}
	}
	_, err := h.tickets.GetByID(t.Context(), *h.admin, 1)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("no ticket may be stored, GetByID err = %v (want ErrNotFound)", err)
	}
}

// TestTicketCreateInvalidCategory422 proves a missing/non-numeric category
// is a 422 (validation), not a 404.
func TestTicketCreateInvalidCategory422(t *testing.T) {
	h := newHarness(t)
	for _, cat := range []string{"", "abc"} {
		form := ticketForm(func(f url.Values) { f.Set("category_id", cat) })
		rec := h.postForm(t, "/tickets", form, false)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("category_id=%q status = %d, want 422", cat, rec.Code)
		}
	}
}

// TestTicketCreateInvalidPriority422 proves an unsupported priority value is
// rejected with 422 (invalid-priority spec).
func TestTicketCreateInvalidPriority422(t *testing.T) {
	h := newHarness(t)
	form := ticketForm(func(f url.Values) {
		f.Set("category_id", strconv.FormatInt(h.bugCategory.ID, 10))
		f.Set("priority", "urgent")
	})

	rec := h.postForm(t, "/tickets", form, false)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), domain.ErrMsgInvalidPriority) {
		t.Errorf("re-render must show %q, got: %s", domain.ErrMsgInvalidPriority, rec.Body.String())
	}
}

// TestTicketNewFormHasNoRequesterFields proves the create form does not
// expose requester inputs at all (ticket-management spec: requester is
// always the session operator — no impersonation vector).
func TestTicketNewFormHasNoRequesterFields(t *testing.T) {
	h := newHarness(t)
	rec := h.get(t, "/tickets/new", false)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "requester_name") || strings.Contains(body, "requester_email") {
		t.Errorf("create form must not expose requester fields, got: %s", body)
	}
}

// TestTicketCreateDerivesRequesterFromSession proves a ticket created via
// the form stores the SESSION operator as requester, even when a forged
// request posts requester_* values (ticket-management spec: derived from
// session, never caller-supplied).
func TestTicketCreateDerivesRequesterFromSession(t *testing.T) {
	h := newHarness(t)
	rec := h.postForm(t, "/tickets", url.Values{
		"title":           {"Forged requester attempt"},
		"category_id":     {"1"},
		"priority":        {"medium"},
		"requester_name":  {"Evil Mallory"},
		"requester_email": {"mallory@example.com"},
	}, false)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	detail := h.get(t, "/tickets/1", false).Body.String()
	if !strings.Contains(detail, `>Requester</span>`) || !strings.Contains(detail, "Admin") {
		t.Errorf("requester must be the session operator shown in the sidebar, got: %s", detail)
	}
	if strings.Contains(detail, "Evil Mallory") {
		t.Errorf("requester must NOT be the forged value, got: %s", detail)
	}
}

// TestTicketCreateIgnoresForgedRequesterUserID proves a forged
// requester_user_id form value is ignored: the stored requester user id is
// the session user's, never the caller-supplied one (ticket-access spec:
// requester identity MUST NOT be supplied by any caller).
func TestTicketCreateIgnoresForgedRequesterUserID(t *testing.T) {
	h := newHarness(t)
	user := seedUserRole(t, h.store, "Ula", "ula@example.com", domain.RoleUser)
	sess := seedSession(t, h.store, user.ID)

	rec := h.postFormAs(t, "/tickets", url.Values{
		"title":             {"Forged requester id"},
		"category_id":       {strconv.FormatInt(h.bugCategory.ID, 10)},
		"priority":          {"medium"},
		"requester_user_id": {strconv.FormatInt(h.admin.ID, 10)},
	}, sess.ID)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	view, err := h.tickets.GetByID(t.Context(), *h.admin, 1)
	if err != nil {
		t.Fatalf("created ticket must be readable: %v", err)
	}
	if view.Ticket.RequesterUserID == nil || *view.Ticket.RequesterUserID != user.ID {
		t.Errorf("requester_user_id = %v, want session user %d (forged value ignored)", view.Ticket.RequesterUserID, user.ID)
	}
}

// TestTicketCreateUserRoleRejectsAssignment proves a user-role actor
// posting an assignee gets 422 and no ticket is stored (ticket-management
// spec: assignment inputs rejected for role user).
func TestTicketCreateUserRoleRejectsAssignment(t *testing.T) {
	h := newHarness(t)
	user := seedUserRole(t, h.store, "Ula", "ula@example.com", domain.RoleUser)
	sess := seedSession(t, h.store, user.ID)
	beto := h.createUser(t, "Beto", "beto@example.com", "secret")

	form := ticketForm(func(f url.Values) {
		f.Set("category_id", strconv.FormatInt(h.bugCategory.ID, 10))
		f.Set("user_id", strconv.FormatInt(beto.ID, 10))
	})
	rec := h.postFormAs(t, "/tickets", form, sess.ID)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", rec.Code)
	}
	_, err := h.tickets.GetByID(t.Context(), *h.admin, 1)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("no ticket may be stored, GetByID err = %v (want ErrNotFound)", err)
	}
}

// --- Issue #123: compact operational metrics summary on GET /tickets ---
// metricsSummaryHTML returns the rendered summary section, or "" when absent.
func metricsSummaryHTML(t *testing.T, body string) string {
	t.Helper()
	start := strings.Index(body, `<section id="ticket-metrics"`)
	if start < 0 {
		return ""
	}
	end := strings.Index(body[start:], "</section>")
	if end < 0 {
		t.Fatalf("summary section never closes: %s", body)
	}
	return body[start : start+end+len("</section>")]
}

// TestTicketsIndexMetricsSummaryRoleVisibility proves the fixed-week summary
// renders for admin/root only.
func TestTicketsIndexMetricsSummaryRoleVisibility(t *testing.T) {
	h := newHarness(t)
	for _, tt := range []struct {
		name        string
		role        domain.Role
		wantSummary bool
	}{
		{"admin", domain.RoleAdmin, true},
		{"root", domain.RoleRoot, true},
		{"agent", domain.RoleAgent, false},
		{"user", domain.RoleUser, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			actor := seedUserRole(t, h.store, tt.name, tt.name+"-metrics@example.com", tt.role)
			rec := doRequest(h.mux, h.mw, http.MethodGet, "/tickets", map[string]string{
				"Cookie": sessionCookie + "=" + seedSession(t, h.store, actor.ID).ID,
			})
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			body := rec.Body.String()
			if got := metricsSummaryHTML(t, body) != ""; got != tt.wantSummary {
				t.Fatalf("%s: summary present = %t, want %t, got: %s", tt.name, got, tt.wantSummary, body)
			}
			if !tt.wantSummary && strings.Contains(body, "Operational summary") {
				t.Errorf("%s page must not contain aggregate metrics copy, got: %s", tt.name, body)
			}
		})
	}
}

// TestTicketsIndexMetricsSummaryContract proves the admin summary is the
// fixed CURRENT UTC Monday-Sunday week, DOM-owned outside #tickets-screen.
func TestTicketsIndexMetricsSummaryContract(t *testing.T) {
	h := newHarness(t)
	rec := h.get(t, "/tickets?state=new&q=printer&hack=1", false)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	summary := metricsSummaryHTML(t, body)
	if summary == "" {
		t.Fatalf("admin full page must render the summary, got: %s", body)
	}

	// Fixed current UTC week from the harness clock: state&q&hack are irrelevant.
	weekStart := time.Date(fixedNow.UTC().Year(), fixedNow.UTC().Month(), fixedNow.UTC().Day(), 0, 0, 0, 0, time.UTC)
	weekStart = weekStart.AddDate(0, 0, -(int(weekStart.Weekday())+6)%7)
	if !strings.Contains(summary, `data-week-start="`+weekStart.Format("2006-01-02")+`"`) ||
		!strings.Contains(summary, `data-week-end="`+weekStart.AddDate(0, 0, 6).Format("2006-01-02")+`"`) {
		t.Errorf("summary must carry the fixed current UTC week %s, got: %s", weekStart.Format("2006-01-02"), summary)
	}
	if got := strings.Count(summary, `class="ticket-metrics-card"`); got != 4 {
		t.Errorf("summary cards = %d, want 4, got: %s", got, summary)
	}
	for _, label := range []string{"Pending tickets", "Unassigned tickets", "Resolved this week", "Mean resolution time"} {
		if !strings.Contains(summary, label) {
			t.Errorf("summary must show card label %q, got: %s", label, summary)
		}
	}
	// No controls or extra copy: no date selector, form, Apply, or notes.
	for _, forbidden := range []string{"<form", `type="date"`, `type="submit"`, "Apply", "Timezone", "excluded", "ticket-metrics-note"} {
		if strings.Contains(summary, forbidden) {
			t.Errorf("summary must not contain %q, got: %s", forbidden, summary)
		}
	}
	// Safe return link: recognized values re-encoded, unknown dropped.
	wantHref := `/tickets/metrics?return=` + url.QueryEscape("/tickets?q=printer&state=new")
	if !strings.Contains(summary, `href="`+wantHref+`"`) {
		t.Errorf("View metrics href = want %q, got: %s", wantHref, summary)
	}
	// DOM ownership: header, summary, screen as siblings; PR137 search once.
	headerAt := strings.Index(body, `class="page-foundation tickets-header"`)
	summaryAt := strings.Index(body, `<section id="ticket-metrics"`)
	screenAt := strings.Index(body, `<section id="tickets-screen"`)
	if !(headerAt >= 0 && headerAt < summaryAt && summaryAt < screenAt) {
		t.Errorf("DOM order must be header < summary < tickets-screen (%d < %d < %d)", headerAt, summaryAt, screenAt)
	}
	if got := strings.Count(body, `class="ticket-search search-field"`); got != 1 {
		t.Errorf("ticket search controls = %d, want 1, got: %s", got, body)
	}
}

// TestTicketsIndexMetricsSummaryHXSwapOmitted proves HX list renders never carry the summary.
func TestTicketsIndexMetricsSummaryHXSwapOmitted(t *testing.T) {
	h := newHarness(t)
	rec := h.get(t, "/tickets", true)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if body := rec.Body.String(); strings.Contains(body, `<section id="ticket-metrics"`) || strings.Contains(body, "Operational summary") {
		t.Errorf("HX fragment must not contain the summary, got: %s", body)
	}
}

// TestMetricsReturnHrefSafety proves the return URL only carries recognized, safely re-encoded parameters.
func TestMetricsReturnHrefSafety(t *testing.T) {
	for _, tt := range []struct{ raw, want string }{
		{"/tickets", "/tickets"},
		{"/tickets?state=new&q=printer&hack=<script>", "/tickets?q=printer&state=new"},
		{"/tickets?priority=high", "/tickets?priority=high"},
		{"/tickets?state=nope", "/tickets"},
		{"/tickets?page=3", "/tickets?page=3"},
		{"/tickets?page=1", "/tickets"},
		{"/users?state=new", "/tickets"},
		{"https://evil.example/tickets?state=new", "/tickets"},
		{"//evil.example/tickets?state=new", "/tickets"},
		{"/tickets?state=new&%zz", "/tickets?state=new"}, // malformed pair silently dropped
	} {
		if got := metricsReturnHref(tt.raw); got != tt.want {
			t.Errorf("metricsReturnHref(%q) = %q, want %q", tt.raw, got, tt.want)
		}
	}
}

// TestTicketMetricsStaticAsset proves the summary stylesheet is served as CSS.
func TestTicketMetricsStaticAsset(t *testing.T) {
	h := newHarness(t)
	rec := httptest.NewRecorder()
	h.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/static/ticket_metrics.css", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "text/css; charset=utf-8" {
		t.Errorf("Content-Type = %q, want text/css; charset=utf-8", got)
	}
	for _, want := range []string{".ticket-metrics-summary", ".ticket-metrics-cards", "@media(max-width:640px)"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("stylesheet must contain %q, got: %s", want, rec.Body.String())
		}
	}
}

// TestTicketMetricsStaticScript proves the summary sync script is served as
// JavaScript and mirrors the current list query into the View metrics link.
func TestTicketMetricsStaticScript(t *testing.T) {
	h := newHarness(t)
	rec := httptest.NewRecorder()
	h.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/static/ticket_metrics.js", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "text/javascript; charset=utf-8" {
		t.Errorf("Content-Type = %q, want text/javascript; charset=utf-8", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
		t.Errorf("Cache-Control = %q, want no-cache", got)
	}
	for _, want := range []string{"syncViewMetricsLink", "/tickets/metrics?return=", "htmx:afterSwap", "htmx:pushedIntoHistory", "htmx:historyRestore"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("script must contain %q, got: %s", want, rec.Body.String())
		}
	}
}

// TestTicketListSLACellMarkup (issue #211, PR 4) pins the two SLA cell
// shapes in the plain staff table: an at_risk row renders the badge plus the
// pending response deadline, and a row whose ticket has no frozen commitment
// renders an EMPTY cell — never a "no SLA" badge.
func TestTicketListSLACellMarkup(t *testing.T) {
	body := renderGolden(t, "tickets_index", "ticket_list", fixtureListData(), true)

	atRisk := `<td data-label="SLA"><span class="badge at_risk">At Risk</span> <span class="cell-muted">Response <time datetime="2026-08-06T11:30:00Z">11:30 · 06-08-2026</time></span></td>`
	if !strings.Contains(body, atRisk) {
		t.Errorf("at_risk row must render the badge and pending deadline %q, got: %s", atRisk, body)
	}
	if !strings.Contains(body, `<td data-label="SLA"></td>`) {
		t.Errorf("a ticket with no frozen SLA must render an empty SLA cell, got: %s", body)
	}
	for _, absent := range []string{`>No SLA<`, `badge none`} {
		if strings.Contains(body, absent) {
			t.Errorf("no-SLA row must not render %q, got: %s", absent, body)
		}
	}
}

// TestTicketsIndexSLABadgeIsStaffOnly (issue #211, PR 4) proves the SLA badge
// and the outstanding deadline render in the two STAFF ticket lists (the
// admin table and the assigned agent queue) and are ABSENT from the requester
// list, which must stay SLA-blind. The harness enables SLA and creates a
// frozen commitment through the real service, so the projection comes from a
// real commitment, not a hand-built store.
func TestTicketsIndexSLABadgeIsStaffOnly(t *testing.T) {
	h := newHarness(t)

	// Enable SLA through the real settings route so the create path freezes
	// the commitment against the category matrix the migration materialized.
	form := slaPanelForm("80")
	form.Set("sla_enabled", "1")
	if rec := h.postForm(t, "/settings/sla", form, false); rec.Code != http.StatusSeeOther {
		t.Fatalf("enable SLA: status = %d, want %d", rec.Code, http.StatusSeeOther)
	}

	// A staff ticket that the admin table and the assigned agent queue render.
	staffTicket := h.seedTicket(t, "Freeze me", nil)
	agent := seedUserRole(t, h.store, "Ava", "ava-sla@example.com", domain.RoleAgent)
	agentSession := seedSession(t, h.store, agent.ID)
	h.assignTicket(t, staffTicket.ID, agent.ID)

	// A requester-owned ticket so the requester list is non-empty.
	requester := seedUserRole(t, h.store, "Rosa", "rosa-sla@example.com", domain.RoleUser)
	requesterSession := seedSession(t, h.store, requester.ID)
	requesterTicket, err := h.tickets.Create(t.Context(), *requester, application.CreateTicketInput{
		Title: "Requester request", CategoryID: h.bugCategory.ID, Priority: domain.PriorityMedium,
	})
	if err != nil {
		t.Fatalf("create requester ticket: %v", err)
	}

	// A fresh commitment is on_track with a pending response deadline.
	const badge = `<span class="badge on_track">On Track</span>`

	adminBody := h.get(t, "/tickets", false).Body.String()
	if !strings.Contains(adminBody, "<th>SLA</th>") {
		t.Errorf("admin list must render the SLA column header, got: %s", adminBody)
	}
	if !strings.Contains(adminBody, badge) {
		t.Errorf("admin list must render the on_track SLA badge, got: %s", adminBody)
	}
	if !strings.Contains(adminBody, "Response <time ") {
		t.Errorf("admin list must render the pending response deadline, got: %s", adminBody)
	}

	agentRec := doRequest(h.mux, h.mw, http.MethodGet, "/tickets", map[string]string{
		"Cookie": sessionCookie + "=" + agentSession.ID,
	})
	if agentRec.Code != http.StatusOK {
		t.Fatalf("agent tickets status = %d, want 200", agentRec.Code)
	}
	agentBody := agentRec.Body.String()
	if !strings.Contains(agentBody, badge) {
		t.Errorf("agent list must render the on_track SLA badge, got: %s", agentBody)
	}
	if !strings.Contains(agentBody, "Response <time ") {
		t.Errorf("agent list must render the pending response deadline, got: %s", agentBody)
	}

	requesterRec := doRequest(h.mux, h.mw, http.MethodGet, "/tickets", map[string]string{
		"Cookie": sessionCookie + "=" + requesterSession.ID,
	})
	if requesterRec.Code != http.StatusOK {
		t.Fatalf("requester tickets status = %d, want 200", requesterRec.Code)
	}
	requesterBody := requesterRec.Body.String()
	if !strings.Contains(requesterBody, requesterTicket.Title) {
		t.Fatalf("requester list must render its own ticket, got: %s", requesterBody)
	}
	for _, absent := range []string{
		"<th>SLA</th>",
		`class="badge on_track"`,
		`class="badge at_risk"`,
		`class="badge breached"`,
		`class="badge met"`,
	} {
		if strings.Contains(requesterBody, absent) {
			t.Errorf("LEAK: requester list must not render SLA markup %q, got: %s", absent, requesterBody)
		}
	}
}
