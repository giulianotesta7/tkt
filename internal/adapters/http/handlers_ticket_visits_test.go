package httpadapter

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// The "what is new since the last visit" mark (issue #415). These
// cases go through the real routes, the real middleware, the real services and
// the real SQLite store, so what they prove is what a browser receives.
//
// Scope note: another change owns the agent sections' and the requester cards'
// markup (#410, #411), so only the admin/root queue renders the mark here. The
// handler payload is still asserted for every list, because the `Changed` fact
// is filled for all of them and those two surfaces will render it.

// queueRow returns the rendered <tr> of one ticket in the plain queue, so an
// assertion is about that ticket's row and never about a page-wide substring.
func queueRow(t *testing.T, body string, ticketID int64) string {
	t.Helper()
	start := strings.Index(body, fmt.Sprintf(`<tr class="queue-row" data-href="/tickets/%d">`, ticketID))
	if start < 0 {
		t.Fatalf("ticket %d is not in the queue: %.400s", ticketID, body)
	}
	end := strings.Index(body[start:], "</tr>")
	if end < 0 {
		t.Fatalf("ticket %d's row is not terminated: %.400s", ticketID, body[start:])
	}
	return body[start : start+end]
}

// wantMarked asserts the row carries the mark: the dot's class on the title and
// the hidden text a screen reader announces.
func wantMarked(t *testing.T, row string, marked bool) {
	t.Helper()
	if got := strings.Contains(row, `class="queue-title queue-title-changed"`); got != marked {
		t.Errorf("row mark = %v, want %v: %s", got, marked, row)
	}
	if got := strings.Contains(row, `>Updated since your last visit<`); got != marked {
		t.Errorf("row screen-reader text = %v, want %v: %s", got, marked, row)
	}
}

// A ticket somebody else changed carries the mark in the admin queue,
// and opening it clears the mark.
func TestTicketVisitMarkClearsWhenTheViewerOpensTheTicket(t *testing.T) {
	h := newHarness(t)
	// A second staff member is the "somebody else" of these cases: an agent
	// can only reach a ticket already in their scope, which would add an
	// assignment event of its own to the history under test.
	other := seedUserRole(t, h.store, "Ada", "ada@visits.test", domain.RoleAdmin)
	changed := h.seedTicket(t, "printer jams", nil)
	untouched := h.seedTicket(t, "login page down", nil)
	if _, err := h.comments.Add(context.Background(), *other, changed.ID, "looking into it", "public"); err != nil {
		t.Fatalf("add comment: %v", err)
	}

	wantMarked(t, queueRow(t, h.get(t, "/tickets", false).Body.String(), changed.ID), true)
	wantMarked(t, queueRow(t, h.get(t, "/tickets", false).Body.String(), untouched.ID), false)

	if rec := h.get(t, fmt.Sprintf("/tickets/%d", changed.ID), false); rec.Code != http.StatusOK {
		t.Fatalf("opening the ticket: status = %d: %s", rec.Code, rec.Body.String())
	}

	body := h.get(t, "/tickets", false).Body.String()
	wantMarked(t, queueRow(t, body, changed.ID), false)
	wantMarked(t, queueRow(t, body, untouched.ID), false)
}

// The viewer's own ticket and own comment
// never mark, so the queue stays quiet about what the viewer just did.
func TestTicketVisitOwnChangeNeverMarks(t *testing.T) {
	h := newHarness(t)
	mine := h.seedTicket(t, "my own problem", nil)
	if _, err := h.comments.Add(context.Background(), *h.admin, mine.ID, "note to self", "public"); err != nil {
		t.Fatalf("add comment: %v", err)
	}

	wantMarked(t, queueRow(t, h.get(t, "/tickets", false).Body.String(), mine.ID), false)
}

// Staff
// see the mark for an internal comment and the requester who owns the ticket
// does not.
func TestTicketVisitInternalCommentMarksStaffOnly(t *testing.T) {
	h := newHarness(t)
	requester := seedUserRole(t, h.store, "Carol", "carol@visits.test", domain.RoleUser)
	other := seedUserRole(t, h.store, "Ada", "ada@visits.test", domain.RoleAdmin)
	// The requester created the ticket, so nothing but the internal comment is
	// in its history: that is what makes each answer below about visibility.
	tkt, err := h.tickets.Create(context.Background(), *requester, application.CreateTicketInput{
		Title:       "VPN drops",
		Description: "every morning",
		CategoryID:  h.bugCategory.ID,
		Priority:    domain.PriorityMedium,
	})
	if err != nil {
		t.Fatalf("create ticket as requester: %v", err)
	}
	if _, err := h.comments.Add(context.Background(), *other, tkt.ID, "internal note", "internal"); err != nil {
		t.Fatalf("add internal comment: %v", err)
	}

	wantMarked(t, queueRow(t, h.get(t, "/tickets", false).Body.String(), tkt.ID), true)

	// The requester never opened it either, so only the visibility rule can
	// keep the mark off their list. The card markup belongs to #411; the
	// payload proves the fact the card will read.
	data := listDataAs(t, h, *requester)
	if len(data.Tickets) != 1 || data.Tickets[0].ID != tkt.ID {
		t.Fatalf("requester list = %+v, want the one ticket", data.Tickets)
	}
	if data.Tickets[0].Changed {
		t.Error("an internal comment marked the ticket changed for the requester")
	}
}

// The queue renders the mark and the two lists another
// change owns still carry the fact, so those surfaces can render it without a
// second derivation.
func TestTicketVisitChangedIsFilledForEveryList(t *testing.T) {
	h := newHarness(t)
	agent := seedUserRole(t, h.store, "Ana", "ana@visits.test", domain.RoleAgent)
	other := seedUserRole(t, h.store, "Ada", "ada@visits.test", domain.RoleAdmin)
	requester := seedUserRole(t, h.store, "Carol", "carol@visits.test", domain.RoleUser)

	// The admin queue: somebody else changed the ticket.
	queued := h.seedTicket(t, "printer jams", nil)
	if _, err := h.comments.Add(context.Background(), *other, queued.ID, "looking into it", "public"); err != nil {
		t.Fatalf("add comment: %v", err)
	}
	if data := listDataAs(t, h, *h.admin); len(data.Tickets) == 0 || !data.Tickets[0].Changed {
		t.Fatalf("the admin queue payload does not carry the mark: %+v", data.Tickets)
	}

	// The agent sections: the ticket is the agent's own, changed by somebody
	// else.
	assigned := h.seedTicket(t, "VPN drops", nil)
	h.assignTicket(t, assigned.ID, agent.ID)
	if _, err := h.comments.Add(context.Background(), *other, assigned.ID, "any news?", "public"); err != nil {
		t.Fatalf("add comment: %v", err)
	}
	sections := listDataAs(t, h, *agent)
	if len(sections.Assigned.Tickets) != 1 || !sections.Assigned.Tickets[0].Changed {
		t.Fatalf("the agent's assigned section does not carry the mark: %+v", sections.Assigned.Tickets)
	}

	// The requester cards: the requester's own ticket, changed by staff.
	owned := h.seedTicket(t, "my own problem", nil)
	if _, err := h.rawDB(t).Exec(`UPDATE tickets SET requester_user_id = ? WHERE id = ?`, requester.ID, owned.ID); err != nil {
		t.Fatalf("link requester: %v", err)
	}
	if _, err := h.comments.Add(context.Background(), *h.admin, owned.ID, "on its way", "public"); err != nil {
		t.Fatalf("add comment: %v", err)
	}
	if data := listDataAs(t, h, *requester); len(data.Tickets) != 1 || !data.Tickets[0].Changed {
		t.Fatalf("the requester list does not carry the mark: %+v", data.Tickets)
	}
}

// Opening a ticket marks that user's bell rows for
// it read and leaves another ticket's unread.
func TestTicketVisitOpeningATicketReadsItsNotifications(t *testing.T) {
	h := newHarness(t)
	agent := seedUserRole(t, h.store, "Ana", "ana@visits.test", domain.RoleAgent)
	opened := h.seedTicket(t, "printer jams", nil)
	other := h.seedTicket(t, "login page down", nil)
	h.assignTicket(t, opened.ID, agent.ID)
	h.assignTicket(t, other.ID, agent.ID)

	if unread := unreadFor(t, h, agent.ID); unread != 2 {
		t.Fatalf("bell = %d before opening, want 2", unread)
	}
	if rec := h.getAs(t, fmt.Sprintf("/tickets/%d", opened.ID), h.sessionFor(t, agent.ID)); rec.Code != http.StatusOK {
		t.Fatalf("opening the ticket: status = %d: %s", rec.Code, rec.Body.String())
	}

	if unread := unreadFor(t, h, agent.ID); unread != 1 {
		t.Fatalf("bell = %d after opening, want only the other ticket's", unread)
	}
	rows, err := h.store.NotificationStore().ListForUser(context.Background(), agent.ID, 50)
	if err != nil {
		t.Fatalf("list notifications: %v", err)
	}
	for _, n := range rows {
		wantRead := n.TicketID == opened.ID
		if got := n.ReadAt != nil; got != wantRead {
			t.Errorf("notification about ticket %d read = %v, want %v", n.TicketID, got, wantRead)
		}
	}
}

// unreadFor reads the bell count through the port the bell itself reads.
func unreadFor(t *testing.T, h *harness, userID int64) int {
	t.Helper()
	unread, err := h.store.NotificationStore().UnreadCount(context.Background(), userID)
	if err != nil {
		t.Fatalf("unread count for user %d: %v", userID, err)
	}
	return unread
}

// Filtering the queue swaps only the results, so the mark has to be
// recomputed on the HTMX fragment — not only on a full page load.
func TestTicketVisitMarkSurvivesAnHTMXFilterChange(t *testing.T) {
	h := newHarness(t)
	other := seedUserRole(t, h.store, "Ada", "ada@visits.test", domain.RoleAdmin)
	tkt := h.seedTicket(t, "printer jams", nil)
	if _, err := h.comments.Add(context.Background(), *other, tkt.ID, "looking into it", "public"); err != nil {
		t.Fatalf("add comment: %v", err)
	}

	body := h.get(t, "/tickets?state=new&priority=medium", true).Body.String()
	if !strings.Contains(body, `id="ticket-list"`) {
		t.Fatalf("the HTMX response is not the list fragment: %.400s", body)
	}
	wantMarked(t, queueRow(t, body, tkt.ID), true)
}

// listDataAs builds the list payload for an explicit actor through the same
// handler wiring the harness mux uses (the actor-context pattern the detail
// flags test established): the payload is what the surfaces that do not render
// the mark yet (#410, #411) will read.
func listDataAs(t *testing.T, h *harness, actor domain.User) listData {
	t.Helper()
	handlers := visitHandlers(h, h.visits)
	req := httptest.NewRequest(http.MethodGet, "/tickets", nil)
	req = req.WithContext(context.WithValue(req.Context(), ctxKeyUser{}, &actor))
	data, err := handlers.listData(req, filterState{}, 1)
	if err != nil {
		t.Fatalf("listData: %v", err)
	}
	return data
}

// visitHandlers builds the ticket handlers the harness mux uses, so a direct
// call exercises the same wiring.
func visitHandlers(h *harness, visits *application.TicketVisitService) *TicketHandlers {
	return NewTicketHandlers(h.tickets, h.comments, h.search, h.categories, h.users, h.store.DeskStore(), h.workflows, application.NewWorkflowRunner(h.clock), h.store.WorkflowRunStore(), h.store.WorkflowUnitOfWork(), h.renderer).WithVisits(visits)
}

// failingVisitStore is the operational-failure seam: it serves every call from
// the real store except the one the test names, so a store error is proven to
// reach the handler instead of being swallowed into an unmarked page.
type failingVisitStore struct {
	application.TicketVisitStore
	markErr    error
	changedErr error
}

func (f *failingVisitStore) Mark(ctx context.Context, ticketID int64) (application.VisitMark, error) {
	if f.markErr != nil {
		return application.VisitMark{}, f.markErr
	}
	return f.TicketVisitStore.Mark(ctx, ticketID)
}

func (f *failingVisitStore) Changed(ctx context.Context, userID int64, ticketIDs []int64, includeInternal bool) (map[int64]bool, error) {
	if f.changedErr != nil {
		return nil, f.changedErr
	}
	return f.TicketVisitStore.Changed(ctx, userID, ticketIDs, includeInternal)
}

// A visit-store failure propagates exactly like the SLA read beside it: the
// detail refuses with the server error, and the list returns it to its callers
// instead of rendering a page with no marks.
func TestTicketVisitStoreErrorsPropagate(t *testing.T) {
	h := newHarness(t)
	h.seedTicket(t, "printer jams", nil)
	wantErr := errors.New("visit store down")
	actor := *h.admin

	req := func() *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/tickets/1", nil)
		return r.WithContext(context.WithValue(r.Context(), ctxKeyUser{}, &actor))
	}
	failing := &failingVisitStore{TicketVisitStore: h.store.TicketVisitStore(), markErr: wantErr}
	handlers := visitHandlers(h, application.NewTicketVisitService(failing, h.clock))
	if _, status, err := handlers.detailDataFor(req(), 1); !errors.Is(err, wantErr) || status != http.StatusInternalServerError {
		t.Fatalf("detailDataFor: status = %d, err = %v; want 500 and %v", status, err, wantErr)
	}

	failing = &failingVisitStore{TicketVisitStore: h.store.TicketVisitStore(), changedErr: wantErr}
	handlers = visitHandlers(h, application.NewTicketVisitService(failing, h.clock))
	if _, err := handlers.listData(req(), filterState{}, 1); !errors.Is(err, wantErr) {
		t.Fatalf("listData: err = %v, want %v", err, wantErr)
	}

	agent := seedUserRole(t, h.store, "Ana", "ana@visits.test", domain.RoleAgent)
	h.assignTicket(t, 1, agent.ID)
	if _, err := h.comments.Add(context.Background(), *h.admin, 1, "in scope now", "public"); err != nil {
		t.Fatalf("add comment: %v", err)
	}
	agentReq := req()
	agentReq = agentReq.WithContext(context.WithValue(agentReq.Context(), ctxKeyUser{}, agent))
	if _, err := handlers.listData(agentReq, filterState{}, 1); !errors.Is(err, wantErr) {
		t.Fatalf("listData for the agent queue: err = %v, want %v", err, wantErr)
	}
}
