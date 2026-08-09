package httpadapter

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

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
	if !strings.Contains(body, "No tickets match your filters.") {
		t.Errorf("empty list must show the empty state, got: %s", body)
	}
}

// TestTicketsIndexRows proves seeded tickets render with readable numbers
// (TKT-N) and titles in newest-first order.
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
	for _, want := range []string{"<th>ID</th>", ">In Progress</span>", ">Medium</td>"} {
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

	view, err := h.tickets.GetByID(t.Context(), 1)
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
	if !strings.Contains(rec.Body.String(), domain.ErrMsgTitleRequired) {
		t.Errorf("re-render must show %q, got: %s", domain.ErrMsgTitleRequired, rec.Body.String())
	}
	_, err := h.tickets.GetByID(t.Context(), 1)
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

// TestTicketCreateRejectsInactiveUser proves assigning an inactive user is
// rejected 422 (inactive-assignment spec).
func TestTicketCreateRejectsInactiveUser(t *testing.T) {
	h := newHarness(t)
	beto := h.createUser(t, "Beto", "beto@example.com", "secret")
	beto.Active = false
	if err := h.store.UserStore().Update(t.Context(), beto); err != nil {
		t.Fatalf("deactivate beto: %v", err)
	}

	form := ticketForm(func(f url.Values) {
		f.Set("category_id", strconv.FormatInt(h.bugCategory.ID, 10))
		f.Set("user_id", strconv.FormatInt(beto.ID, 10))
	})
	rec := h.postForm(t, "/tickets", form, false)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), domain.ErrMsgUserInactive) {
		t.Errorf("re-render must show %q, got: %s", domain.ErrMsgUserInactive, rec.Body.String())
	}
}

// TestTicketCreateAssignsActiveUser proves assigning an active user works
// and the ticket renders assigned.
func TestTicketCreateAssignsActiveUser(t *testing.T) {
	h := newHarness(t)
	beto := h.createUser(t, "Beto", "beto@example.com", "secret")

	form := ticketForm(func(f url.Values) {
		f.Set("category_id", strconv.FormatInt(h.bugCategory.ID, 10))
		f.Set("user_id", strconv.FormatInt(beto.ID, 10))
	})
	rec := h.postForm(t, "/tickets", form, false)

	wantRedirect(t, rec, http.StatusSeeOther, "/tickets")
	view, err := h.tickets.GetByID(t.Context(), 1)
	if err != nil {
		t.Fatalf("ticket must exist: %v", err)
	}
	if view.AssignedUser == nil || view.AssignedUser.ID != beto.ID {
		t.Errorf("assigned user = %+v, want beto", view.AssignedUser)
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
	if !strings.Contains(detail, "Admin &lt;admin@tkt.test&gt;") {
		t.Errorf("requester must be the session operator, got: %s", detail)
	}
}
