package httpadapter

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// Issue #344 regression on the least_loaded routing rows (S10 "state changed →
// requester AND assignee"). The assignee of a least_loaded step exists only
// inside the transaction, so the rows that describe the PRE-PLAN assignee are
// resolved against the selection the transaction actually made:
//
//	| in-tx selection        | outgoing assignee       | selected assignee |
//	| re-selects the holder  | keeps state_changed etc.| no "assigned"     |
//	| moves to someone else  | nothing                 | "assigned"        |
//
// Both cases go through the real HTTP completion route, the real runner, the
// real workflow unit of work, and the real notification store.

// leastLoadedFormDef is a workflow whose first (and only human) step is a
// requester form, followed by automatic least_loaded routing. Submitting the
// form is what triggers the routing.
func leastLoadedFormDef(deskID int64) domain.WorkflowDefinition {
	return domain.WorkflowDefinition{
		{Type: domain.StepForm, Form: &domain.FormStep{
			Actor:  domain.FormActorRequester,
			Fields: []domain.FormField{{Key: "host", Label: "Host", Kind: domain.FieldShortText}},
		}},
		{Type: domain.StepAssignToDesk, AssignToDesk: &domain.AssignToDeskStep{DeskID: deskID, Strategy: domain.StrategyLeastLoaded}},
	}
}

// routingFixture is a ticket owned by a requester, sitting on a requester form
// inside a category whose definition routes it with least_loaded across a desk
// with exactly two agents (so the selection is decided by their open load).
type routingFixture struct {
	requester *domain.User
	holder    *domain.User
	other     *domain.User
	deskID    int64
	ticket    *domain.Ticket
}

func newRoutingFixture(t *testing.T, h *harness) *routingFixture {
	t.Helper()
	f := &routingFixture{
		requester: seedUserRole(t, h.store, "Rita", "rita-routing@example.com", domain.RoleUser),
		holder:    seedUserRole(t, h.store, "Ana", "ana-routing@example.com", domain.RoleAgent),
		other:     seedUserRole(t, h.store, "Bea", "bea-routing@example.com", domain.RoleAgent),
	}
	cat, err := h.categories.Create(t.Context(), "Routing")
	if err != nil {
		t.Fatalf("create category: %v", err)
	}
	desk, err := h.desks.Create(t.Context(), *h.admin, "Routing desk")
	if err != nil {
		t.Fatalf("create desk: %v", err)
	}
	f.deskID = desk.ID
	for _, u := range []*domain.User{f.holder, f.other} {
		if err := h.desks.AddMember(t.Context(), *h.admin, desk.ID, u.ID); err != nil {
			t.Fatalf("add desk member %q: %v", u.Email, err)
		}
	}
	// Publish DIRECTLY: the harness's publishWorkflow helper staffs every desk
	// with the admin, which would add a third candidate and make the
	// least-loaded selection depend on a fixture rather than on the load.
	def := leastLoadedFormDef(desk.ID)
	canonical, err := def.MarshalCanonical()
	if err != nil {
		t.Fatalf("canonical workflow: %v", err)
	}
	if _, issues, err := h.store.WorkflowStore().Publish(t.Context(), cat.ID, canonical, nil); err != nil {
		t.Fatalf("publish workflow: %v", err)
	} else if len(issues) > 0 {
		t.Fatalf("publish workflow rejected: %v", issues)
	}

	tkt, err := h.tickets.Create(t.Context(), *f.requester, application.CreateTicketInput{
		Title: "Routing regression", Description: "owned by the requester",
		CategoryID: cat.ID, Priority: domain.PriorityMedium,
	})
	if err != nil {
		t.Fatalf("create ticket: %v", err)
	}
	f.ticket = tkt
	h.assignTicket(t, f.ticket.ID, f.holder.ID)
	return f
}

// load puts n open tickets on the given agent, so the least-loaded selection has
// a deterministic winner (the lowest open count, then the lowest user id).
func (f *routingFixture) load(t *testing.T, h *harness, agent *domain.User, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		extra := h.seedTicket(t, "load "+agent.Name+" "+strconv.Itoa(i), nil)
		h.assignTicket(t, extra.ID, agent.ID)
	}
}

// submit posts the requester form as its requester — the actor whose completion
// runs the routing step.
func (f *routingFixture) submit(t *testing.T, h *harness) *httptest.ResponseRecorder {
	t.Helper()
	rec := h.postFormAs(t, "/tickets/"+strconv.FormatInt(f.ticket.ID, 10)+"/workflow/steps/1/complete",
		url.Values{"answer_0": {"api-01"}}, h.sessionFor(t, f.requester.ID))
	return rec
}

// assigneeOf reads the persisted assignee of the ticket.
func assigneeOf(t *testing.T, h *harness, ticketID int64) int64 {
	t.Helper()
	return scanOneInt(t, h.rawDB(t), `SELECT COALESCE(user_id, 0) FROM tickets WHERE id = ?`, ticketID)
}

// ticketNotificationKinds returns this ticket's notification kinds for one
// recipient, oldest first. Narrowed to the ticket on purpose: the fixture's own
// assignments and loads write their own rows, and this test is about what the
// completion adds.
func ticketNotificationKinds(t *testing.T, h *harness, userID, ticketID int64) []string {
	t.Helper()
	rows, err := h.rawDB(t).Query(`SELECT kind FROM notifications WHERE user_id = ? AND ticket_id = ? ORDER BY id ASC`, userID, ticketID)
	if err != nil {
		t.Fatalf("read notifications: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var kind string
		if err := rows.Scan(&kind); err != nil {
			t.Fatalf("scan notification: %v", err)
		}
		out = append(out, kind)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read notifications: %v", err)
	}
	return out
}

// TestLeastLoadedReselectKeepsTheStateChangedRow: the routing step re-selects
// the agent who already holds the ticket, so nothing is assigned — but the state
// change is still news that agent is owed (S10 "state changed → requester and
// assignee"). The pre-plan assignee's rows are resolved against the in-tx
// selection, so a re-selection keeps them; the bell gets one honest row, not a
// state change replaced by a redundant "assigned".
func TestLeastLoadedReselectKeepsTheStateChangedRow(t *testing.T) {
	h := newHarness(t)
	f := newRoutingFixture(t, h)
	// Two open tickets for the other agent make the current holder (one open
	// ticket: this one) the least loaded member, so the routing re-selects her.
	f.load(t, h, f.other, 2)

	before := ticketNotificationKinds(t, h, f.holder.ID, f.ticket.ID)
	if rec := f.submit(t, h); rec.Code != http.StatusOK {
		t.Fatalf("form completion status = %d, want 200 (body %.300s)", rec.Code, rec.Body.String())
	}

	if got := assigneeOf(t, h, f.ticket.ID); got != f.holder.ID {
		t.Fatalf("assignee = %d, want the re-selected holder %d", got, f.holder.ID)
	}
	after := ticketNotificationKinds(t, h, f.holder.ID, f.ticket.ID)
	if len(after) != len(before)+1 {
		t.Fatalf("holder notifications = %v, want exactly one new row on top of %v", after, before)
	}
	if got := after[len(before):]; len(got) != 1 || got[0] != string(domain.NotificationStateChanged) {
		t.Fatalf("new holder rows = %v, want [state_changed] (no redundant assigned)", got)
	}
	// The requester is the actor, and the other agent was never involved.
	if got := ticketNotificationKinds(t, h, f.requester.ID, f.ticket.ID); len(got) != 0 {
		t.Errorf("requester notifications = %v, want none (the requester acted)", got)
	}
	if got := ticketNotificationKinds(t, h, f.other.ID, f.ticket.ID); len(got) != 0 {
		t.Errorf("other agent notifications = %v, want none (untouched by the routing)", got)
	}
}

// TestRerouteDoesNotNotifyTheOutgoingAssignee: the routing step moves the
// ticket to the other agent, so the outgoing holder — who may no longer be able
// to open it — learns nothing (S10 rule 2), and the agent the selection picked
// gets the "assigned" row.
func TestRerouteDoesNotNotifyTheOutgoingAssignee(t *testing.T) {
	h := newHarness(t)
	f := newRoutingFixture(t, h)
	// An extra open ticket on the holder makes her the busier agent, so the
	// routing picks the other one.
	f.load(t, h, f.holder, 1)

	before := ticketNotificationKinds(t, h, f.holder.ID, f.ticket.ID)
	if rec := f.submit(t, h); rec.Code != http.StatusOK {
		t.Fatalf("form completion status = %d, want 200 (body %.300s)", rec.Code, rec.Body.String())
	}

	if got := assigneeOf(t, h, f.ticket.ID); got != f.other.ID {
		t.Fatalf("assignee = %d, want the routed agent %d", got, f.other.ID)
	}
	if after := ticketNotificationKinds(t, h, f.holder.ID, f.ticket.ID); len(after) != len(before) {
		t.Fatalf("outgoing holder notifications = %v, want no new row on top of %v", after, before)
	}
	if got := ticketNotificationKinds(t, h, f.other.ID, f.ticket.ID); len(got) != 1 || got[0] != string(domain.NotificationAssigned) {
		t.Fatalf("selected agent notifications = %v, want [assigned]", got)
	}
	if got := ticketNotificationKinds(t, h, f.requester.ID, f.ticket.ID); len(got) != 0 {
		t.Errorf("requester notifications = %v, want none (the requester acted)", got)
	}
}
