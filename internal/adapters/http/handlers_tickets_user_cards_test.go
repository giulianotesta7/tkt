package httpadapter

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// This file owns the requester list's presentation contract. The section split
// (waiting / open / closed, their cursors, and the search spanning them) lives
// in handlers_tickets_user_sections_test.go; here the list's shape, its rows and
// its empty states are pinned.

func TestTicketsIndexUserShowsOnlyOwnRequestsInPlainLanguage(t *testing.T) {
	h := newHarness(t)
	user := seedUserRole(t, h.store, "Ula", "ula@example.com", domain.RoleUser)
	other := seedUserRole(t, h.store, "Omar", "omar@example.com", domain.RoleUser)
	sess := seedSession(t, h.store, user.ID)

	create := func(actor *domain.User, title string) *domain.Ticket {
		ticket, err := h.tickets.Create(t.Context(), *actor, application.CreateTicketInput{
			Title: title, CategoryID: h.bugCategory.ID, Priority: domain.PriorityMedium,
		})
		if err != nil {
			t.Fatalf("create %q: %v", title, err)
		}
		return ticket
	}
	newTicket := create(user, "Visible received request")
	if _, err := h.rawDB(t).Exec(`UPDATE tickets SET description = ? WHERE id = ?`, "Printer report with steps to reproduce", newTicket.ID); err != nil {
		t.Fatalf("set description: %v", err)
	}
	inProgress := create(user, "Visible in-progress request")
	resolved := create(user, "Visible resolved request")
	closed := create(user, "Visible closed request")
	cancelled := create(user, "Visible cancelled request")
	otherOpen := create(other, "Visible other-owner request")
	for _, update := range []struct {
		ticket *domain.Ticket
		state  domain.State
	}{
		{inProgress, domain.StateInProgress},
		{resolved, domain.StateResolved},
		{cancelled, domain.StateCancelled},
	} {
		if _, err := h.rawDB(t).Exec(`UPDATE tickets SET state = ? WHERE id = ?`, update.state, update.ticket.ID); err != nil {
			t.Fatalf("set %q state: %v", update.ticket.Title, update.state)
		}
	}
	h.seedTransition(t, closed.ID, domain.StateResolved, "")
	closeViaRequester(t, h, user, closed.ID)

	for _, hx := range []bool{false, true} {
		for _, page := range []int{1, 2} {
			headers := map[string]string{"Cookie": sessionCookie + "=" + sess.ID}
			if hx {
				headers["HX-Request"] = "true"
			}
			rec := doRequest(h.mux, h.mw, http.MethodGet, "/tickets?q=visible&state=resolved&priority=critical&category_id=999&user_id=999&page="+strconv.Itoa(page), headers)
			if rec.Code != http.StatusOK {
				t.Fatalf("hx=%t page=%d status = %d, want 200", hx, page, rec.Code)
			}
			body := rec.Body.String()
			if !strings.Contains(body, "2 open tickets") {
				t.Errorf("hx=%t page=%d the header must count the requester's open work: %s", hx, page, body)
			}
			waiting, openSection, closedSection := requesterSections(t, body)
			if waiting != "" {
				t.Errorf("hx=%t page=%d no ticket is on hold, so the waiting section must not render: %s", hx, page, waiting)
			}
			if page == 1 {
				for _, want := range []string{newTicket.Title, inProgress.Title, "Received", "In progress", "Printer report with steps to reproduce", `class="req"`, `class="req-desc"`, `class="req-meta"`} {
					if !strings.Contains(openSection, want) {
						t.Errorf("hx=%t requester open section missing %q: %s", hx, want, body)
					}
				}
			}
			// The closed work is reachable but folded: resolved and closed own
			// their own section, never the open one.
			if !strings.Contains(closedSection, resolved.Title) || !strings.Contains(closedSection, closed.Title) {
				t.Errorf("hx=%t page=%d resolved and closed requests belong in the closed section: %s", hx, page, body)
			}
			if strings.Contains(body, "<table") {
				t.Errorf("hx=%t requester list must render rows, not a table: %s", hx, body)
			}
			if strings.Contains(body, "View request") {
				t.Errorf("hx=%t the duplicated View request link must be gone: %s", hx, body)
			}
			// The absence guard scans the rendered document without its head: the
			// shell head carries the htmx config key refreshOnHistoryMiss (issue
			// #347), and no token below is ever legitimately part of the head.
			markup := body
			if headEnd := strings.Index(markup, "</head>"); headEnd >= 0 {
				markup = markup[headEnd:]
			}
			for _, absent := range []string{cancelled.Title, otherOpen.Title, "<th>Priority</th>", "<th>State</th>", "Needs attention", "Complete details", "History"} {
				if strings.Contains(markup, absent) {
					t.Errorf("hx=%t page=%d requester list must exclude %q: %s", hx, page, absent, body)
				}
			}
		}
	}
}

// TestTicketsIndexUserListPresentation freezes the list wrapper contract for the
// requester list (issue #411, S2/S4): rows are a full-width hairline list, each
// section's pagination is the list's own sibling and never a row, and the empty
// state spans the list with its primary New ticket action.
func TestTicketsIndexUserListPresentation(t *testing.T) {
	h := newHarness(t)
	user := seedUserRole(t, h.store, "Greta", "greta@example.com", domain.RoleUser)
	sess := seedSession(t, h.store, user.ID)

	// 12 open tickets force the open section to paginate; one row carries a
	// long description that must truncate inside its own row.
	for index := 0; index < 12; index++ {
		if _, err := h.tickets.Create(t.Context(), *user, application.CreateTicketInput{
			Title: fmt.Sprintf("List probe ticket %02d", index), CategoryID: h.bugCategory.ID, Priority: domain.PriorityMedium,
		}); err != nil {
			t.Fatalf("create list probe %d: %v", index, err)
		}
	}
	if _, err := h.rawDB(t).Exec(`UPDATE tickets SET description = ? WHERE id = (SELECT MAX(id) FROM tickets)`, "A deliberately long description that must stay on one truncated line inside its row instead of stretching the list"); err != nil {
		t.Fatalf("set long description: %v", err)
	}

	body := ticketsIndexAs(t, h, sess.ID, "?q=probe", false)
	_, openSection, _ := requesterSections(t, body)
	listOpen := strings.Index(openSection, `class="req-list"`)
	firstRow := strings.Index(openSection, `class="req"`)
	if listOpen < 0 {
		t.Fatalf("the open section must wrap its rows in a .req-list: %s", body)
	}
	if firstRow < 0 || listOpen > firstRow {
		t.Errorf("the list wrapper must open before the first row (list=%d row=%d)", listOpen, firstRow)
	}
	listEnd, navAt := strings.Index(openSection, "</ul>"), strings.Index(openSection, `<nav class="pagination"`)
	if listEnd < 0 || navAt < listEnd {
		t.Errorf("12 open tickets must paginate with the nav after the list, never inside a row (list=%d nav=%d): %s", listEnd, navAt, body)
	}
	if strings.Contains(openSection, "req-empty") {
		t.Errorf("a populated open section must not render the empty state: %s", body)
	}
	// A row carries the ticket's description as its one truncated line (S2).
	mustContain(t, openSection, "A deliberately long description that must stay on one truncated line")

	// Empty and search-empty states render the same full-width block with the
	// primary New ticket action.
	otherUser := seedUserRole(t, h.store, "Nora", "nora@example.com", domain.RoleUser)
	otherSess := seedSession(t, h.store, otherUser.ID)
	for _, tc := range []struct {
		name   string
		query  string
		copy   string
		absent string
	}{
		{name: "empty", query: "", copy: "No requests yet", absent: "No requests match your search"},
		{name: "search-empty", query: "?q=zzzabsent", copy: "No requests match your search", absent: "No requests yet"},
	} {
		body := ticketsIndexAs(t, h, otherSess.ID, tc.query, false)
		_, openSection, _ := requesterSections(t, body)
		mustContain(t, openSection, tc.copy, `class="req-empty"`, `href="/tickets/new"`)
		if strings.Contains(openSection, tc.absent) {
			t.Errorf("%s: the open section must render only %q: %s", tc.name, tc.copy, body)
		}
	}
}

func TestTicketsIndexUserForbiddenFiltersCannotChooseASection(t *testing.T) {
	h := newHarness(t)
	user := seedUserRole(t, h.store, "Ula", "ula@example.com", domain.RoleUser)
	sess := seedSession(t, h.store, user.ID)

	open, err := h.tickets.Create(t.Context(), *user, application.CreateTicketInput{
		Title: "Still open", CategoryID: h.bugCategory.ID, Priority: domain.PriorityMedium,
	})
	if err != nil {
		t.Fatalf("create open ticket: %v", err)
	}
	resolved, err := h.tickets.Create(t.Context(), *user, application.CreateTicketInput{
		Title: "Already resolved", CategoryID: h.bugCategory.ID, Priority: domain.PriorityMedium,
	})
	if err != nil {
		t.Fatalf("create resolved ticket: %v", err)
	}
	h.seedTransition(t, resolved.ID, domain.StateResolved, "")

	// A forged filter set must not reach into another section: the open ticket
	// stays in the open section and the resolved one stays in the closed
	// section, whatever state, priority, category or assignee the URL claims.
	for _, hx := range []bool{false, true} {
		body := ticketsIndexAs(t, h, sess.ID, "?state=resolved&priority=critical&category_id=999&user_id=999", hx)
		waiting, openSection, closedSection := requesterSections(t, body)
		if waiting != "" {
			t.Errorf("hx=%t a forged state filter must not create a waiting section: %s", hx, waiting)
		}
		mustContain(t, openSection, "Open · 1", open.Title)
		if strings.Contains(openSection, resolved.Title) {
			t.Errorf("hx=%t a forged state filter must not move a resolved own ticket into the open section: %s", hx, body)
		}
		mustContain(t, closedSection, "Closed · 1", resolved.Title)
	}
}
