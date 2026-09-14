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

func TestTicketsIndexUserShowsOnlyOwnOpenRequestsInPlainLanguage(t *testing.T) {
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
		{closed, domain.StateClosed},
		{cancelled, domain.StateCancelled},
	} {
		if _, err := h.rawDB(t).Exec(`UPDATE tickets SET state = ? WHERE id = ?`, update.state, update.ticket.ID); err != nil {
			t.Fatalf("set %q state: %v", update.ticket.Title, err)
		}
	}

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
			if !strings.Contains(body, "2 tickets") {
				t.Errorf("hx=%t page=%d user count must remain 2: %s", hx, page, body)
			}
			if page == 1 {
				for _, want := range []string{newTicket.Title, inProgress.Title, "Received", "In progress", "View request", `class="card user-request-card"`, "Printer report with steps to reproduce", `class="user-request-content"`, `class="user-request-head"`, `class="user-request-description"`} {
					if !strings.Contains(body, want) {
						t.Errorf("hx=%t user list missing %q: %s", hx, want, body)
					}
				}
				if strings.Contains(body, "<table") {
					t.Errorf("hx=%t user list must render cards, not a table: %s", hx, body)
				}
			}
			for _, absent := range []string{resolved.Title, closed.Title, cancelled.Title, otherOpen.Title, "<th>Priority</th>", "<th>State</th>", "Needs attention", "Complete details", "History"} {
				if strings.Contains(body, absent) {
					t.Errorf("hx=%t page=%d user list must exclude %q: %s", hx, page, absent, body)
				}
			}
		}
	}
}

// TestTicketsIndexUserCardGridPresentation freezes the grid wrapper contract
// for the requester card list (issue #122 grid/density refinement): cards and
// the empty state live inside one .user-ticket-grid wrapper and the pagination
// nav renders as the wrapper's immediate sibling, never as a grid cell.
func TestTicketsIndexUserCardGridPresentation(t *testing.T) {
	h := newHarness(t)
	user := seedUserRole(t, h.store, "Greta", "greta@example.com", domain.RoleUser)
	sess := seedSession(t, h.store, user.ID)

	// 12 open tickets force Pages=2 so the pagination nav renders; one card
	// carries a long description that must wrap inside its grid cell.
	for index := 0; index < 12; index++ {
		if _, err := h.tickets.Create(t.Context(), *user, application.CreateTicketInput{
			Title: fmt.Sprintf("Grid probe ticket %02d", index), CategoryID: h.bugCategory.ID, Priority: domain.PriorityMedium,
		}); err != nil {
			t.Fatalf("create grid probe %d: %v", index, err)
		}
	}
	if _, err := h.rawDB(t).Exec(`UPDATE tickets SET description = ? WHERE id = (SELECT MIN(id) FROM tickets)`, "A deliberately long description that must wrap safely inside its card cell without overlapping neighbouring cards in the grid row"); err != nil {
		t.Fatalf("set long description: %v", err)
	}

	headers := map[string]string{"Cookie": sessionCookie + "=" + sess.ID}
	rec := doRequest(h.mux, h.mw, http.MethodGet, "/tickets", headers)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	gridOpen := strings.Index(body, `class="user-ticket-grid"`)
	firstCard := strings.Index(body, `class="card user-request-card"`)
	navIdx := strings.Index(body, `class="pagination"`)
	if gridOpen < 0 {
		t.Errorf("user list must wrap its cards in a .user-ticket-grid wrapper: %s", body)
	}
	if firstCard < 0 || gridOpen > firstCard {
		t.Errorf("user grid wrapper must open before the first card (grid=%d card=%d)", gridOpen, firstCard)
	}
	if navIdx < 0 {
		t.Errorf("12 tickets must paginate (Pages>1), pagination nav missing: %s", body)
	}
	if !strings.Contains(body, `</div><nav class="pagination"`) {
		t.Errorf("pagination must render as the immediate sibling after the grid wrapper closes, never inside it: %s", body)
	}
	if strings.Contains(body, "user-request-empty") {
		t.Errorf("populated user grid must not render the empty-state card: %s", body)
	}

	// Empty and search-empty states span the whole grid as one full-width card.
	other := seedUserRole(t, h.store, "Nora", "nora@example.com", domain.RoleUser)
	otherSess := seedSession(t, h.store, other.ID)
	for _, tc := range []struct {
		name   string
		query  string
		absent string
	}{
		{name: "empty", query: "", absent: "No requests yet"},
		{name: "search-empty", query: "?q=zzzabsent", absent: "No requests match your search"},
	} {
		rec := doRequest(h.mux, h.mw, http.MethodGet, "/tickets"+tc.query, map[string]string{"Cookie": sessionCookie + "=" + otherSess.ID})
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, want 200", tc.name, rec.Code)
		}
		body := rec.Body.String()
		if !strings.Contains(body, tc.absent) {
			t.Errorf("%s: missing copy %q: %s", tc.name, tc.absent, body)
		}
		if !strings.Contains(body, `class="card user-request-card user-request-empty"`) {
			t.Errorf("%s: empty state must be a full-width .user-request-empty card: %s", tc.name, body)
		}
	}
}

func TestTicketsIndexUserExcludesClosedStatesDespiteForgedFilters(t *testing.T) {
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

	for _, hx := range []bool{false, true} {
		headers := map[string]string{"Cookie": sessionCookie + "=" + sess.ID}
		if hx {
			headers["HX-Request"] = "true"
		}
		rec := doRequest(h.mux, h.mw, http.MethodGet, "/tickets?state=resolved&priority=critical&category_id=999&user_id=999", headers)
		if rec.Code != http.StatusOK {
			t.Fatalf("hx=%t status = %d, want 200", hx, rec.Code)
		}
		body := rec.Body.String()
		if !strings.Contains(body, open.Title) {
			t.Errorf("hx=%t user list must retain open own ticket, got: %s", hx, body)
		}
		if strings.Contains(body, resolved.Title) {
			t.Errorf("hx=%t user list must exclude resolved own ticket despite forged filters, got: %s", hx, body)
		}
	}
}
