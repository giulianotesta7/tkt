package httpadapter

import (
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// Issue #344, the whole least_loaded routing matrix as rows a person reads in
// the notification center (S10):
//
//	| routing outcome          | outgoing holder | selected assignee            |
//	| re-selects the holder    | nothing         | state_changed (+ form_waiting)|
//	| moves to someone else    | nothing         | assigned (+ form_waiting)    |
//	| selects the actor        | (the actor)     | nothing                      |
//
// The "form_waiting" half is the S10 row "a workflow form step now waits on this
// person": when the routing leaves the run PENDING on an assignee form, the
// member the routing puts the ticket on is exactly the person who has to act
// next, so they are told (maintainer's decision, e6-batch-2 L20). A plan that
// leaves no form pending, or no least_loaded step at all, keeps the rows it
// always wrote.
//
// Everything runs through the real HTTP completion route, the real runner, the
// real workflow unit of work, and the real notification store: these are rows
// the bell counts, so the assertion is "what did this person gain".

// routingDefinition builds the workflow the fixture category publishes. It
// receives the fixture itself, so a case can staff extra desks with the
// fixture's users, plus the id of the primary routing desk.
type routingDefinition func(f *routingNotificationFixture, deskID int64) domain.WorkflowDefinition

// routingNotificationSetup arranges one row of the matrix. The zero value is
// the common case: both agents staff the routing desk and the ticket already
// belongs to the holder.
type routingNotificationSetup struct {
	// definition builds the workflow the fixture category publishes.
	definition routingDefinition
	// singleMember staffs the desk with the holder only, so the routing has no
	// choice but to select the holder (the "selects the actor" row).
	singleMember bool
	// leaveUnassigned creates the ticket with no assignee, so the routing
	// assigns one for the first time.
	leaveUnassigned bool
}

// routingNotificationFixture is a requester-owned ticket waiting on step 0 (a
// requester or assignee form) inside a category whose definition routes it with
// least_loaded over a desk won by whoever carries less open work.
type routingNotificationFixture struct {
	requester *domain.User
	holder    *domain.User
	other     *domain.User
	ticket    *domain.Ticket
	names     map[int64]string
}

func newRoutingNotificationFixture(t *testing.T, h *harness, setup routingNotificationSetup) *routingNotificationFixture {
	t.Helper()
	f := &routingNotificationFixture{
		requester: seedUserRole(t, h.store, "Rita", "rita-routing-notify@example.com", domain.RoleUser),
		holder:    seedUserRole(t, h.store, "Ana", "ana-routing-notify@example.com", domain.RoleAgent),
		other:     seedUserRole(t, h.store, "Bea", "bea-routing-notify@example.com", domain.RoleAgent),
	}
	f.names = map[int64]string{f.requester.ID: "rita", f.holder.ID: "ana", f.other.ID: "bea"}

	cat, err := h.categories.Create(t.Context(), "Routing notifications")
	if err != nil {
		t.Fatalf("create category: %v", err)
	}
	members := []*domain.User{f.holder}
	if !setup.singleMember {
		members = append(members, f.other)
	}
	// Publish DIRECTLY: the harness's publishWorkflow helper staffs every desk
	// with the admin, which would add a third candidate and make the
	// least-loaded selection depend on a fixture rather than on the load.
	def := setup.definition(f, newRoutingDesk(t, h, "Routing notifications desk", members...))
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
		Title: "Routing notifications", Description: "owned by the requester",
		CategoryID: cat.ID, Priority: domain.PriorityMedium,
	})
	if err != nil {
		t.Fatalf("create ticket: %v", err)
	}
	f.ticket = tkt
	if !setup.leaveUnassigned {
		h.assignTicket(t, f.ticket.ID, f.holder.ID)
	}
	return f
}

// newRoutingDesk creates a desk staffed with the given users, so a routing step
// over it can only select one of them.
func newRoutingDesk(t *testing.T, h *harness, name string, members ...*domain.User) int64 {
	t.Helper()
	desk, err := h.desks.Create(t.Context(), *h.admin, name)
	if err != nil {
		t.Fatalf("create desk %q: %v", name, err)
	}
	for _, u := range members {
		if err := h.desks.AddMember(t.Context(), *h.admin, desk.ID, u.ID); err != nil {
			t.Fatalf("add %q to desk %q: %v", u.Email, name, err)
		}
	}
	return desk.ID
}

// load puts n open tickets on the given agent, so the least-loaded selection has
// a deterministic winner (the lowest open count, then the lowest user id).
func (f *routingNotificationFixture) load(t *testing.T, h *harness, agent *domain.User, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		extra := h.seedTicket(t, "load "+agent.Name+" "+strconv.Itoa(i), nil)
		h.assignTicket(t, extra.ID, agent.ID)
	}
}

// completeStep submits one workflow form step as its actor and returns the
// status the route answered.
func (f *routingNotificationFixture) completeStep(t *testing.T, h *harness, step int, as *domain.User, answers url.Values) int {
	t.Helper()
	rec := h.postFormAs(t,
		"/tickets/"+strconv.FormatInt(f.ticket.ID, 10)+"/workflow/steps/"+strconv.Itoa(step)+"/complete",
		answers, h.sessionFor(t, as.ID))
	return rec.Code
}

// formThenRouting: a requester form, then automatic least_loaded routing.
func formThenRouting(_ *routingNotificationFixture, deskID int64) domain.WorkflowDefinition {
	return domain.WorkflowDefinition{
		{Type: domain.StepForm, Form: &domain.FormStep{
			Actor:  domain.FormActorRequester,
			Fields: []domain.FormField{{Key: "host", Label: "Host", Kind: domain.FieldShortText}},
		}},
		{Type: domain.StepAssignToDesk, AssignToDesk: &domain.AssignToDeskStep{DeskID: deskID, Strategy: domain.StrategyLeastLoaded}},
	}
}

// formThenResolve: a requester form, then an automatic resolve — the control
// case with NO least_loaded step, which must keep the rows it always wrote.
func formThenResolve(_ *routingNotificationFixture, _ int64) domain.WorkflowDefinition {
	return domain.WorkflowDefinition{
		{Type: domain.StepForm, Form: &domain.FormStep{
			Actor:  domain.FormActorRequester,
			Fields: []domain.FormField{{Key: "host", Label: "Host", Kind: domain.FieldShortText}},
		}},
		{Type: domain.StepResolve},
	}
}

// assigneeFormThenRouting: an assignee form, then automatic least_loaded
// routing — the holder's own answer is what runs the routing.
func assigneeFormThenRouting(_ *routingNotificationFixture, deskID int64) domain.WorkflowDefinition {
	return domain.WorkflowDefinition{
		{Type: domain.StepForm, Form: &domain.FormStep{
			Actor:  domain.FormActorAssignee,
			Fields: []domain.FormField{{Key: "host", Label: "Host", Kind: domain.FieldShortText}},
		}},
		{Type: domain.StepAssignToDesk, AssignToDesk: &domain.AssignToDeskStep{DeskID: deskID, Strategy: domain.StrategyLeastLoaded}},
	}
}

// formRoutingThenAssigneeForm: a requester form, automatic least_loaded
// routing, then an ASSIGNEE form the plan leaves pending (step 2).
func formRoutingThenAssigneeForm(_ *routingNotificationFixture, deskID int64) domain.WorkflowDefinition {
	return domain.WorkflowDefinition{
		{Type: domain.StepForm, Form: &domain.FormStep{
			Actor:  domain.FormActorRequester,
			Fields: []domain.FormField{{Key: "host", Label: "Host", Kind: domain.FieldShortText}},
		}},
		{Type: domain.StepAssignToDesk, AssignToDesk: &domain.AssignToDeskStep{DeskID: deskID, Strategy: domain.StrategyLeastLoaded}},
		{Type: domain.StepForm, Form: &domain.FormStep{
			Actor:  domain.FormActorAssignee,
			Fields: []domain.FormField{{Key: "fix", Label: "Fix", Kind: domain.FieldShortText}},
		}},
	}
}

// notificationsFor returns this ticket's stored rows for one recipient, oldest
// first, through the real notification store port.
func notificationsFor(t *testing.T, h *harness, userID, ticketID int64) []domain.Notification {
	t.Helper()
	rows, err := h.store.NotificationStore().ListForUser(t.Context(), userID, 50)
	if err != nil {
		t.Fatalf("list notifications for user %d: %v", userID, err)
	}
	var out []domain.Notification
	for i := len(rows) - 1; i >= 0; i-- { // the port answers newest first
		if rows[i].TicketID == ticketID {
			out = append(out, rows[i])
		}
	}
	return out
}

// snapshot records every fixture person's rows BEFORE the completion, so a test
// can assert exactly what the completion added on top of the fixture's own
// audited assignment.
func (f *routingNotificationFixture) snapshot(t *testing.T, h *harness) map[int64][]string {
	t.Helper()
	out := map[int64][]string{}
	for _, u := range []*domain.User{f.requester, f.holder, f.other} {
		out[u.ID] = f.describe(t, h, u)
	}
	return out
}

// describe renders one person's rows as "<name>: <kind> actor=<name> step=<n>"
// so a test asserts every stored fact of every row with one comparison.
func (f *routingNotificationFixture) describe(t *testing.T, h *harness, user *domain.User) []string {
	t.Helper()
	rows := notificationsFor(t, h, user.ID, f.ticket.ID)
	out := make([]string, 0, len(rows))
	for _, n := range rows {
		actor := "system"
		if n.ActorUserID != nil {
			actor = f.nameOf(*n.ActorUserID)
		}
		step := "-"
		if n.StepIndex != nil {
			step = strconv.Itoa(*n.StepIndex)
		}
		out = append(out, f.nameOf(user.ID)+": "+string(n.Kind)+" actor="+actor+" step="+step)
	}
	return out
}

// added returns the rows this person gained since the snapshot, still in the
// fixture's readable "<name>: <kind> actor=<name> step=<n>" form.
func (f *routingNotificationFixture) added(t *testing.T, h *harness, before map[int64][]string, user *domain.User) []string {
	t.Helper()
	counts := map[string]int{}
	for _, row := range before[user.ID] {
		counts[row]++
	}
	var out []string
	for _, row := range f.describe(t, h, user) {
		if counts[row] > 0 {
			counts[row]--
			continue
		}
		out = append(out, row)
	}
	return out
}

// nameOf maps a user id to its fixture name, so failures read like the matrix.
func (f *routingNotificationFixture) nameOf(userID int64) string {
	if name, ok := f.names[userID]; ok {
		return name
	}
	return strconv.FormatInt(userID, 10)
}

// assertAddedRows fails unless the completion added EXACTLY want for that
// person: rows, actors and pinned steps, nothing more and nothing less. Row
// ORDER is not part of the contract here — two different mechanisms in the plan
// write these rows — so both sides are compared as sets.
func assertAddedRows(t *testing.T, who string, got []string, want ...string) {
	t.Helper()
	sorted := func(rows []string) string {
		copyOf := append([]string(nil), rows...)
		sort.Strings(copyOf)
		return strings.Join(copyOf, " | ")
	}
	if sorted(got) != sorted(want) {
		t.Errorf("%s rows = %q, want %q", who, sorted(got), sorted(want))
	}
}

// persistedTicketState reads the ticket's persisted state (no store port
// exposes the raw row, and this is a structural fact of the fixture).
func persistedTicketState(t *testing.T, h *harness, ticketID int64) string {
	t.Helper()
	return scanOneString(t, h.rawDB(t), `SELECT state FROM tickets WHERE id = ?`, ticketID)
}

// TestLeastLoadedReselectNotifiesTheHolderOnce: the routing keeps the agent who
// already holds the ticket, so nothing was assigned to her — but the state
// change is still news she is owed, and she gets NO redundant "assigned" row.
func TestLeastLoadedReselectNotifiesTheHolderOnce(t *testing.T) {
	h := newHarness(t)
	f := newRoutingNotificationFixture(t, h, routingNotificationSetup{definition: formThenRouting})
	// The other agent is busier, so the routing re-selects the holder.
	f.load(t, h, f.other, 2)
	before := f.snapshot(t, h)

	if code := f.completeStep(t, h, 1, f.requester, url.Values{"answer_0": {"api-01"}}); code != http.StatusOK {
		t.Fatalf("form completion status = %d, want 200", code)
	}

	if got := assigneeOf(t, h, f.ticket.ID); got != f.holder.ID {
		t.Fatalf("assignee = %d, want the re-selected holder %d", got, f.holder.ID)
	}
	if got := persistedTicketState(t, h, f.ticket.ID); got != string(domain.StateInProgress) {
		t.Fatalf("state = %q, want in_progress", got)
	}
	assertAddedRows(t, "holder", f.added(t, h, before, f.holder), "ana: state_changed actor=rita step=-")
	assertAddedRows(t, "other agent", f.added(t, h, before, f.other))
	assertAddedRows(t, "requester", f.added(t, h, before, f.requester))
}

// TestLeastLoadedRerouteNotifiesOnlyTheNewAssignee: the routing moves the
// ticket to the other agent, so the outgoing holder — who may no longer be able
// to open it — learns nothing (S10 rule 2), and the agent the selection picked
// learns the ticket is theirs.
func TestLeastLoadedRerouteNotifiesOnlyTheNewAssignee(t *testing.T) {
	h := newHarness(t)
	f := newRoutingNotificationFixture(t, h, routingNotificationSetup{definition: formThenRouting})
	// An extra open ticket makes the holder the busier agent.
	f.load(t, h, f.holder, 1)
	before := f.snapshot(t, h)

	if code := f.completeStep(t, h, 1, f.requester, url.Values{"answer_0": {"api-01"}}); code != http.StatusOK {
		t.Fatalf("form completion status = %d, want 200", code)
	}

	if got := assigneeOf(t, h, f.ticket.ID); got != f.other.ID {
		t.Fatalf("assignee = %d, want the routed agent %d", got, f.other.ID)
	}
	if got := persistedTicketState(t, h, f.ticket.ID); got != string(domain.StateInProgress) {
		t.Fatalf("state = %q, want in_progress", got)
	}
	assertAddedRows(t, "new assignee", f.added(t, h, before, f.other), "bea: assigned actor=rita step=-")
	assertAddedRows(t, "outgoing holder", f.added(t, h, before, f.holder))
	assertAddedRows(t, "requester", f.added(t, h, before, f.requester))
}

// TestLeastLoadedSelectionOfTheActorNotifiesNobody: the holder answers an
// assignee form and the routing can only re-select her — her own action, so no
// row for her at all (S10 "never the person who performed the action"). The
// requester still learns the state changed.
func TestLeastLoadedSelectionOfTheActorNotifiesNobody(t *testing.T) {
	h := newHarness(t)
	f := newRoutingNotificationFixture(t, h, routingNotificationSetup{definition: assigneeFormThenRouting, singleMember: true})
	before := f.snapshot(t, h)

	if code := f.completeStep(t, h, 1, f.holder, url.Values{"answer_0": {"api-01"}}); code != http.StatusOK {
		t.Fatalf("form completion status = %d, want 200", code)
	}

	if got := assigneeOf(t, h, f.ticket.ID); got != f.holder.ID {
		t.Fatalf("assignee = %d, want the acting holder %d", got, f.holder.ID)
	}
	assertAddedRows(t, "acting holder", f.added(t, h, before, f.holder))
	assertAddedRows(t, "requester", f.added(t, h, before, f.requester), "rita: state_changed actor=ana step=-")
	assertAddedRows(t, "other agent", f.added(t, h, before, f.other))
}

// TestFormThenResolveKeepsTheAssigneeRow is the control: a plan with NO
// least_loaded step writes exactly the rows it always wrote.
func TestFormThenResolveKeepsTheAssigneeRow(t *testing.T) {
	h := newHarness(t)
	f := newRoutingNotificationFixture(t, h, routingNotificationSetup{definition: formThenResolve})
	before := f.snapshot(t, h)

	if code := f.completeStep(t, h, 1, f.requester, url.Values{"answer_0": {"api-01"}}); code != http.StatusOK {
		t.Fatalf("form completion status = %d, want 200", code)
	}

	if got := persistedTicketState(t, h, f.ticket.ID); got != string(domain.StateResolved) {
		t.Fatalf("state = %q, want resolved", got)
	}
	assertAddedRows(t, "assignee", f.added(t, h, before, f.holder), "ana: state_changed actor=rita step=-")
	assertAddedRows(t, "requester", f.added(t, h, before, f.requester))
	assertAddedRows(t, "other agent", f.added(t, h, before, f.other))
}

// TestLeastLoadedReselectNotifiesThePendingAssigneeForm: the routing keeps the
// holder AND leaves an assignee form pending — she is owed the state change and
// the fact that the run now waits on her.
func TestLeastLoadedReselectNotifiesThePendingAssigneeForm(t *testing.T) {
	h := newHarness(t)
	f := newRoutingNotificationFixture(t, h, routingNotificationSetup{definition: formRoutingThenAssigneeForm})
	f.load(t, h, f.other, 2) // the other agent is busier: the routing keeps the holder
	before := f.snapshot(t, h)

	if code := f.completeStep(t, h, 1, f.requester, url.Values{"answer_0": {"api-01"}}); code != http.StatusOK {
		t.Fatalf("form completion status = %d, want 200", code)
	}

	if got := assigneeOf(t, h, f.ticket.ID); got != f.holder.ID {
		t.Fatalf("assignee = %d, want the re-selected holder %d", got, f.holder.ID)
	}
	assertAddedRows(t, "holder", f.added(t, h, before, f.holder),
		"ana: state_changed actor=rita step=-",
		"ana: form_waiting actor=rita step=2",
	)
	assertAddedRows(t, "other agent", f.added(t, h, before, f.other))
	assertAddedRows(t, "requester", f.added(t, h, before, f.requester))
}

// TestLeastLoadedRerouteNotifiesThePendingAssigneeForm pins issue #344's closed
// gap: when the routing MOVES the ticket and leaves an assignee form pending,
// the new assignee is exactly the person who has to act, so she learns both that
// the ticket is hers and that the run waits on her (S10, decision L20). The
// outgoing holder learns nothing.
func TestLeastLoadedRerouteNotifiesThePendingAssigneeForm(t *testing.T) {
	h := newHarness(t)
	f := newRoutingNotificationFixture(t, h, routingNotificationSetup{definition: formRoutingThenAssigneeForm})
	f.load(t, h, f.holder, 1) // the holder is busier: the routing moves the ticket
	before := f.snapshot(t, h)

	if code := f.completeStep(t, h, 1, f.requester, url.Values{"answer_0": {"api-01"}}); code != http.StatusOK {
		t.Fatalf("form completion status = %d, want 200", code)
	}

	if got := assigneeOf(t, h, f.ticket.ID); got != f.other.ID {
		t.Fatalf("assignee = %d, want the routed agent %d", got, f.other.ID)
	}
	if got := persistedTicketState(t, h, f.ticket.ID); got != string(domain.StateInProgress) {
		t.Fatalf("state = %q, want in_progress", got)
	}
	assertAddedRows(t, "new assignee", f.added(t, h, before, f.other),
		"bea: assigned actor=rita step=-",
		"bea: form_waiting actor=rita step=2",
	)
	assertAddedRows(t, "outgoing holder", f.added(t, h, before, f.holder))
	assertAddedRows(t, "requester", f.added(t, h, before, f.requester))
}

// TestLeastLoadedAssignmentNotifiesThePendingAssigneeForm covers the ticket that
// had NO assignee before the routing: the selection is the first holder, and she
// is told both facts the same way — the ticket is hers, and a form waits on her.
func TestLeastLoadedAssignmentNotifiesThePendingAssigneeForm(t *testing.T) {
	h := newHarness(t)
	f := newRoutingNotificationFixture(t, h, routingNotificationSetup{
		definition: formRoutingThenAssigneeForm, leaveUnassigned: true,
	})
	before := f.snapshot(t, h)

	if code := f.completeStep(t, h, 1, f.requester, url.Values{"answer_0": {"api-01"}}); code != http.StatusOK {
		t.Fatalf("form completion status = %d, want 200", code)
	}

	// Both members start empty-handed, so the lowest user id wins.
	if got := assigneeOf(t, h, f.ticket.ID); got != f.holder.ID {
		t.Fatalf("assignee = %d, want the first selected agent %d", got, f.holder.ID)
	}
	assertAddedRows(t, "new assignee", f.added(t, h, before, f.holder),
		"ana: assigned actor=rita step=-",
		"ana: form_waiting actor=rita step=2",
	)
	assertAddedRows(t, "other agent", f.added(t, h, before, f.other))
	assertAddedRows(t, "requester", f.added(t, h, before, f.requester))
}

// TestTwoLeastLoadedStepsNotifyOnlyTheFinalSelection: two routing steps over two
// desks flip the ticket (holder → other → holder) and then leave an assignee form
// pending. Only the member the transaction ENDS UP with may be told a form waits
// on them; a row for the intermediate member would point at a form they cannot
// act on. The stale "assigned" row an intermediate member keeps is a known
// pre-existing follow-up on this branch, so this test deliberately says nothing
// about the assigned rows — only about who is told to act.
func TestTwoLeastLoadedStepsNotifyOnlyTheFinalSelection(t *testing.T) {
	h := newHarness(t)
	// The second desk has a single member (the holder), so the second routing
	// step can only put the ticket back on her after the first step moved it.
	// Two steps over the SAME desk would both read the pre-transaction load and
	// re-select the same member, which is not the case under test.
	f := newRoutingNotificationFixture(t, h, routingNotificationSetup{
		definition: func(f *routingNotificationFixture, deskID int64) domain.WorkflowDefinition {
			escalation := newRoutingDesk(t, h, "Escalation desk", f.holder)
			return domain.WorkflowDefinition{
				{Type: domain.StepForm, Form: &domain.FormStep{
					Actor:  domain.FormActorRequester,
					Fields: []domain.FormField{{Key: "host", Label: "Host", Kind: domain.FieldShortText}},
				}},
				{Type: domain.StepAssignToDesk, AssignToDesk: &domain.AssignToDeskStep{DeskID: deskID, Strategy: domain.StrategyLeastLoaded}},
				{Type: domain.StepAssignToDesk, AssignToDesk: &domain.AssignToDeskStep{DeskID: escalation, Strategy: domain.StrategyLeastLoaded}},
				{Type: domain.StepForm, Form: &domain.FormStep{
					Actor:  domain.FormActorAssignee,
					Fields: []domain.FormField{{Key: "fix", Label: "Fix", Kind: domain.FieldShortText}},
				}},
			}
		},
	})
	f.load(t, h, f.holder, 1) // the first routing step moves the ticket to the other agent
	before := f.snapshot(t, h)

	if code := f.completeStep(t, h, 1, f.requester, url.Values{"answer_0": {"api-01"}}); code != http.StatusOK {
		t.Fatalf("form completion status = %d, want 200", code)
	}

	if got := assigneeOf(t, h, f.ticket.ID); got != f.holder.ID {
		t.Fatalf("assignee = %d, want the final selection %d", got, f.holder.ID)
	}
	var waiting []string
	for _, u := range []*domain.User{f.requester, f.holder, f.other} {
		for _, row := range f.added(t, h, before, u) {
			if strings.Contains(row, ": "+string(domain.NotificationFormWaiting)+" ") {
				waiting = append(waiting, row)
			}
		}
	}
	assertAddedRows(t, "form_waiting", waiting, "ana: form_waiting actor=rita step=3")
}
