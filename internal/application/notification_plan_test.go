package application_test

import (
	"context"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// Issue #344 slice B2, workflow half: the plan the runner hands to the unit of
// work carries the notifications the APPLICATION computed for the change it
// describes (S10/S11). Written before the plan computed them (strict TDD RED).
//
// In this path the actor is always the requester or the assignee (they are the
// person a form or manual step waits on), so the S10 actor rule means exactly
// ONE of the two is told about a workflow-driven state change — the other is
// the person who just acted.

// notifySnap builds a snapshot whose ticket names a requester and an assignee,
// carrying the policy facts (role + active) the notification decision needs.
func notifySnap(t *testing.T, st domain.State, cur int, def domain.WorkflowDefinition, requester, assignee int64) application.WorkflowExecutionSnapshot {
	t.Helper()
	s := snapWith(st, cur, def, ptr(requester), ptr(assignee))
	s.Requester = &application.NotificationRecipient{UserID: requester, Role: domain.RoleUser, Active: true}
	s.Assignee = &application.NotificationRecipient{UserID: assignee, Role: domain.RoleAgent, Active: true}
	return s
}

func kindsFor(pl application.WorkflowMutationPlan, userID int64) []domain.NotificationKind {
	var out []domain.NotificationKind
	for _, n := range pl.Notifications {
		if n.UserID == userID {
			out = append(out, n.Kind)
		}
	}
	return out
}

// TestPlanCompleteNotifiesTheRequesterAboutTheStateChange: an agent claims a
// new ticket. The claim is the actor's own action (no assigned row), the state
// change is news for the requester.
func TestPlanCompleteNotifiesTheRequesterAboutTheStateChange(t *testing.T) {
	const requester, agent = 7, 9
	r := application.NewWorkflowRunner(fixedClock())
	snapshot := notifySnap(t, domain.StateNew, 0, wf(claim(1)), requester, 0)
	snapshot.Ticket.UserID = nil

	plan, err := r.PlanComplete(context.Background(), snapshot, application.CompleteWorkflowCommand{
		TicketID: 1, ActorUserID: agent, ActorName: "Ana", ExpectedPosition: 1,
	})
	if err != nil {
		t.Fatalf("PlanComplete: %v", err)
	}
	if len(plan.Notifications) != 1 {
		t.Fatalf("plan notifications = %+v, want exactly the requester's state change", plan.Notifications)
	}
	n := plan.Notifications[0]
	if n.UserID != requester || n.Kind != domain.NotificationStateChanged {
		t.Fatalf("notification = %+v, want requester %d with a state change", n, requester)
	}
	if n.FromValue == nil || *n.FromValue != "new" || n.ToValue == nil || *n.ToValue != "in_progress" {
		t.Errorf("state facts = (%v, %v), want (new, in_progress)", n.FromValue, n.ToValue)
	}
	if n.ActorUserID == nil || *n.ActorUserID != agent {
		t.Errorf("actor = %v, want the claiming agent %d", n.ActorUserID, agent)
	}
	if got := kindsFor(plan, agent); len(got) != 0 {
		t.Errorf("the claiming agent was told about their own claim: %v", got)
	}
}

// TestPlanCompleteResolveAsksTheRequesterToConfirm: an assignee form followed by
// an automatic resolve must ask the requester to confirm, not report a bare
// state change.
func TestPlanCompleteResolveAsksTheRequesterToConfirm(t *testing.T) {
	const requester, agent = 7, 9
	r := application.NewWorkflowRunner(fixedClock())
	def := wf(fm(domain.FormActorAssignee, domain.FormField{Key: "k", Label: "l", Kind: domain.FieldShortText}), res())
	snapshot := notifySnap(t, domain.StateInProgress, 0, def, requester, agent)

	plan, err := r.PlanComplete(context.Background(), snapshot, application.CompleteWorkflowCommand{
		TicketID: 1, ActorUserID: agent, ActorName: "Ana", ExpectedPosition: 1,
		RawAnswers: application.RawPositionalValues{{Position: 0, Values: []string{"done"}}},
	})
	if err != nil {
		t.Fatalf("PlanComplete: %v", err)
	}
	if plan.NextTicketState != domain.StateResolved {
		t.Fatalf("next state = %s, want resolved", plan.NextTicketState)
	}
	got := kindsFor(plan, requester)
	if len(got) != 1 || got[0] != domain.NotificationResolvedAwaitingConfirmation {
		t.Fatalf("requester kinds = %v, want [resolved_awaiting_confirmation]", got)
	}
}

// TestPlanCompleteLeastLoadedCarriesTheAssigneeTemplate: the application cannot
// name the member a least_loaded step selects (the selection happens inside the
// transaction), so the operation carries the notification TEMPLATE and the unit
// of work fills the recipient it selected. The template DOES carry the human
// actor whose completion ran this routing step: the selection can land back on
// that person, and the unit of work can only drop the row once it knows the
// recipient (S10 "never the person who performed the action").
func TestPlanCompleteLeastLoadedCarriesTheAssigneeTemplate(t *testing.T) {
	const requester, agent = 7, 9
	r := application.NewWorkflowRunner(fixedClock())
	snapshot := notifySnap(t, domain.StateInProgress, 0, wf(man(), least(1)), requester, agent)

	plan, err := r.PlanComplete(context.Background(), snapshot, application.CompleteWorkflowCommand{
		TicketID: 1, ActorUserID: agent, ActorName: "Ana", ExpectedPosition: 1,
	})
	if err != nil {
		t.Fatalf("PlanComplete: %v", err)
	}
	var templates int
	for _, op := range plan.Operations {
		ll, ok := op.(application.LeastLoadedAssignmentOperation)
		if !ok {
			continue
		}
		if ll.NotifyAssignee == nil {
			t.Fatal("least_loaded operation carries no assignee notification")
		}
		templates++
		if ll.NotifyAssignee.TicketID != 1 {
			t.Errorf("template ticket = %d, want 1", ll.NotifyAssignee.TicketID)
		}
		if ll.NotifyAssignee.ActorUserID == nil || *ll.NotifyAssignee.ActorUserID != agent {
			t.Errorf("template actor = %v, want the completing agent %d", ll.NotifyAssignee.ActorUserID, agent)
		}
		row := ll.NotifyAssignee.Assigned(42)
		if row.UserID != 42 || row.Kind != domain.NotificationAssigned {
			t.Errorf("filled row = %+v, want an assigned row for the selected member", row)
		}
		if row.ActorUserID == nil || *row.ActorUserID != agent {
			t.Errorf("filled row actor = %v, want the completing agent %d", row.ActorUserID, agent)
		}
		// A row completed for the ACTOR is the one the unit of work drops: the
		// template hands it every fact that decision needs (the recipient) and
		// the fact it filters on (the actor).
		if actorRow := ll.NotifyAssignee.Assigned(agent); actorRow.ActorUserID == nil || *actorRow.ActorUserID != actorRow.UserID {
			t.Errorf("actor-addressed row = %+v, want it to carry its own recipient as actor", actorRow)
		}
	}
	if templates != 1 {
		t.Fatalf("%d least_loaded templates, want 1", templates)
	}
	// The ticket was already in_progress, so nothing else was announced.
	if len(plan.Notifications) != 0 {
		t.Errorf("plan notifications = %+v, want none (no state change)", plan.Notifications)
	}
}

// TestPlanCompleteLeastLoadedDefersTheAssigneeRows: a plan that routes the
// ticket with least_loaded cannot know whether the ticket keeps its holder or
// moves — the selection only exists inside the transaction — so the rows that
// describe the snapshot's assignee travel as a CONDITIONAL block keyed to that
// person. The unit of work releases them on a re-selection and drops them on a
// reroute; the state-change row is not addressed to the requester here because
// the requester is the actor who answered the form.
func TestPlanCompleteLeastLoadedDefersTheAssigneeRows(t *testing.T) {
	const requester, agent = 7, 9
	r := application.NewWorkflowRunner(fixedClock())
	// The REQUESTER answers the first form; the run then routes the ticket with
	// least_loaded, which also moves it out of "new".
	def := wf(fm(domain.FormActorRequester, domain.FormField{Key: "host", Label: "Host", Kind: domain.FieldShortText}),
		least(1))
	snapshot := notifySnap(t, domain.StateNew, 0, def, requester, agent)

	plan, err := r.PlanComplete(context.Background(), snapshot, application.CompleteWorkflowCommand{
		TicketID: 1, ActorUserID: requester, ActorName: "Rita", ExpectedPosition: 1,
		RawAnswers: application.RawPositionalValues{{Position: 0, Values: []string{"api-01"}}},
	})
	if err != nil {
		t.Fatalf("PlanComplete: %v", err)
	}
	if plan.NextTicketState != domain.StateInProgress {
		t.Fatalf("next state = %s, want in_progress (the fixture must change state)", plan.NextTicketState)
	}
	if len(plan.Notifications) != 0 {
		t.Fatalf("plan notifications = %+v, want none (the requester acted)", plan.Notifications)
	}
	if len(plan.ConditionalNotifications) != 1 {
		t.Fatalf("conditional blocks = %+v, want one for the pre-plan assignee", plan.ConditionalNotifications)
	}
	c := plan.ConditionalNotifications[0]
	if c.AssigneeUserID != agent {
		t.Fatalf("conditional recipient = %d, want the pre-plan assignee %d", c.AssigneeUserID, agent)
	}
	if len(c.Rows) != 1 || c.Rows[0].Kind != domain.NotificationStateChanged {
		t.Fatalf("conditional rows = %+v, want [state_changed]", c.Rows)
	}
	if c.Rows[0].UserID != agent || c.Rows[0].TicketID != 1 {
		t.Errorf("row = %+v, want the assignee's row on the ticket", c.Rows[0])
	}
	// The selected member still gets the assigned row the operation carries.
	var templates int
	for _, op := range plan.Operations {
		if ll, ok := op.(application.LeastLoadedAssignmentOperation); ok && ll.NotifyAssignee != nil {
			templates++
		}
	}
	if templates != 1 {
		t.Fatalf("%d least_loaded templates, want 1", templates)
	}
}

// TestPlanCompleteWithoutRecipientFactsNotifiesNobody pins the fail-closed
// default: a snapshot that cannot prove who may open the ticket produces no
// notification at all, and the plan still applies.
func TestPlanCompleteWithoutRecipientFactsNotifiesNobody(t *testing.T) {
	r := application.NewWorkflowRunner(fixedClock())
	snapshot := snapWith(domain.StateNew, 0, wf(claim(1)), ptr(int64(7)), nil)
	snapshot.Ticket.UserID = nil

	plan, err := r.PlanComplete(context.Background(), snapshot, application.CompleteWorkflowCommand{
		TicketID: 1, ActorUserID: 9, ActorName: "Ana", ExpectedPosition: 1,
	})
	if err != nil {
		t.Fatalf("PlanComplete: %v", err)
	}
	if len(plan.Notifications) != 0 {
		t.Fatalf("plan notifications = %+v, want none without recipient facts", plan.Notifications)
	}
}

// followerRunner wires the runner against the follower ports: a ticket that
// exists in the ticket store (id 1, the snapshot's ticket) with one follower.
func followerRunner(t *testing.T, requester, agent int64) (*application.WorkflowRunner, *fakeFollowerStore, domain.User) {
	t.Helper()
	clock := fixedClock()
	users := newFakeUserStore()
	follower := users.seedRole("Flo", "flo@example.com", domain.RoleAdmin, true)
	tickets := newFakeTicketStore()
	tickets.seed(domain.Ticket{
		Title: "VPN drops", CategoryID: 1, RequesterUserID: ptr(requester), UserID: ptr(agent),
		Priority: domain.PriorityMedium, State: domain.StateInProgress, CreatedAt: clock.now, UpdatedAt: clock.now,
	})
	followers := newFakeFollowerStore(users)
	followers.seedFollow(1, follower.ID)
	return application.NewWorkflowRunner(clock).WithFollowers(application.NewFollowService(tickets, users, followers, clock)), followers, follower
}

// TestPlanCompleteTellsTheTicketsFollowers: a workflow-driven state change is
// news for the ticket's followers too, while the requester keeps the
// confirmation request and the acting assignee hears nothing.
func TestPlanCompleteTellsTheTicketsFollowers(t *testing.T) {
	const requester, agent = 7, 9
	runner, _, follower := followerRunner(t, requester, agent)
	snapshot := notifySnap(t, domain.StateInProgress, 0, wf(man(), res()), requester, agent)

	plan, err := runner.PlanComplete(context.Background(), snapshot, application.CompleteWorkflowCommand{
		TicketID: 1, ActorUserID: agent, ActorName: "Ana", ExpectedPosition: 1,
	})
	if err != nil {
		t.Fatalf("PlanComplete: %v", err)
	}
	if len(plan.Notifications) != 2 {
		t.Fatalf("plan notifications = %+v, want the requester's confirmation request and the follower's state change", plan.Notifications)
	}
	if got := kindsFor(plan, follower.ID); len(got) != 1 || got[0] != domain.NotificationStateChanged {
		t.Errorf("follower kinds = %v, want [state_changed]", got)
	}
	if got := kindsFor(plan, requester); len(got) != 1 || got[0] != domain.NotificationResolvedAwaitingConfirmation {
		t.Errorf("requester kinds = %v, want [resolved_awaiting_confirmation]", got)
	}
	if got := kindsFor(plan, agent); len(got) != 0 {
		t.Errorf("the acting assignee kinds = %v, want none", got)
	}
}

// TestPlanCompleteDoesNotFanOutFormWaiting: a step left waiting on the assignee
// is not a follower event, so the plan addresses only the assignee.
func TestPlanCompleteDoesNotFanOutFormWaiting(t *testing.T) {
	const requester, agent = 7, 9
	runner, _, follower := followerRunner(t, requester, agent)
	def := wf(man(), fm(domain.FormActorAssignee, domain.FormField{Key: "k", Label: "l", Kind: domain.FieldShortText}))
	snapshot := notifySnap(t, domain.StateInProgress, 0, def, requester, agent)

	plan, err := runner.PlanComplete(context.Background(), snapshot, application.CompleteWorkflowCommand{
		TicketID: 1, ActorUserID: agent, ActorName: "Ana", ExpectedPosition: 1,
	})
	if err != nil {
		t.Fatalf("PlanComplete: %v", err)
	}
	if got := kindsFor(plan, follower.ID); len(got) != 0 {
		t.Errorf("follower kinds = %v, want none (a waiting form is not a follower event)", got)
	}
}

// leastLoadedTemplate returns the assignee template of the last least_loaded
// operation in a plan.
func leastLoadedTemplate(t *testing.T, plan application.WorkflowMutationPlan) *application.AssigneeNotification {
	t.Helper()
	var template *application.AssigneeNotification
	for _, op := range plan.Operations {
		if ll, ok := op.(application.LeastLoadedAssignmentOperation); ok {
			template = ll.NotifyAssignee
		}
	}
	if template == nil {
		t.Fatal("plan carries no least_loaded assignee template")
	}
	return template
}

// TestPlanCompleteCarriesFollowersForLeastLoadedRouting: a routing step selects
// its assignee inside the transaction, so the plan hands the adapter the ids of
// the followers who must learn the outcome — already proven able to open the
// ticket, and without the outgoing assignee, who loses the ticket to the change.
func TestPlanCompleteCarriesFollowersForLeastLoadedRouting(t *testing.T) {
	const requester, agent = 7, 9
	runner, followers, follower := followerRunner(t, requester, agent)
	// The current holder follows the ticket too: a routing step that moves the
	// ticket away from them must not address them afterwards.
	followers.seedFollow(1, agent)

	snapshot := notifySnap(t, domain.StateInProgress, 0, wf(man(), least(1)), requester, agent)
	plan, err := runner.PlanComplete(context.Background(), snapshot, application.CompleteWorkflowCommand{
		TicketID: 1, ActorUserID: agent, ActorName: "Ana", ExpectedPosition: 1,
	})
	if err != nil {
		t.Fatalf("PlanComplete: %v", err)
	}
	template := leastLoadedTemplate(t, plan)
	if len(template.Followers) != 1 || template.Followers[0] != follower.ID {
		t.Fatalf("template followers = %v, want only the follower %d (the outgoing assignee is excluded)", template.Followers, follower.ID)
	}
	// The ticket kept its holder in this plan's view and the actor is that holder,
	// so nothing else is addressed.
	if len(plan.Notifications) != 0 {
		t.Fatalf("plan notifications = %+v, want none", plan.Notifications)
	}
}

// TestPlanCompleteTellsFollowersAboutAClaim: a claim puts the ticket on a person
// the plan knows — the acting agent — so the follower rows are decided here, the
// way a manual assignment decides them.
func TestPlanCompleteTellsFollowersAboutAClaim(t *testing.T) {
	const requester, holder = 7, 9
	const claimant = 5
	runner, _, follower := followerRunner(t, requester, holder)
	snapshot := notifySnap(t, domain.StateInProgress, 0, wf(claim(1)), requester, holder)

	plan, err := runner.PlanComplete(context.Background(), snapshot, application.CompleteWorkflowCommand{
		TicketID: 1, ActorUserID: claimant, ActorName: "Cid", ExpectedPosition: 1,
	})
	if err != nil {
		t.Fatalf("PlanComplete: %v", err)
	}
	got := plan.Notifications
	if len(got) != 1 {
		t.Fatalf("plan notifications = %+v, want exactly the follower's assignment row", got)
	}
	if got[0].UserID != follower.ID || got[0].Kind != domain.NotificationAssigneeChanged {
		t.Fatalf("notification = %+v, want an assignee_changed for the follower %d", got[0], follower.ID)
	}
	if got[0].ToValue == nil || *got[0].ToValue != "Cid" {
		t.Errorf("to = %v, want the claimant's display name", got[0].ToValue)
	}
	if got[0].ActorUserID == nil || *got[0].ActorUserID != claimant {
		t.Errorf("actor = %v, want the claimant %d", got[0].ActorUserID, claimant)
	}
}

// TestPlanCompleteTellsFollowersOfAClaimedNewTicketOnce: the same completion also
// moves the ticket's state, and the follower hears those two facts as one row —
// the row the plan already writes for them wins.
func TestPlanCompleteTellsFollowersOfAClaimedNewTicketOnce(t *testing.T) {
	const requester, claimant = 7, 5
	runner, _, follower := followerRunner(t, requester, 9)
	snapshot := notifySnap(t, domain.StateNew, 0, wf(claim(1)), requester, 0)
	snapshot.Ticket.UserID = nil

	plan, err := runner.PlanComplete(context.Background(), snapshot, application.CompleteWorkflowCommand{
		TicketID: 1, ActorUserID: claimant, ActorName: "Cid", ExpectedPosition: 1,
	})
	if err != nil {
		t.Fatalf("PlanComplete: %v", err)
	}
	kinds := kindsFor(plan, follower.ID)
	if len(kinds) != 1 || kinds[0] != domain.NotificationStateChanged {
		t.Fatalf("follower kinds = %v, want one state_changed", kinds)
	}
	if kinds := kindsFor(plan, requester); len(kinds) != 1 || kinds[0] != domain.NotificationStateChanged {
		t.Errorf("requester kinds = %v, want one state_changed", kinds)
	}
	if kinds := kindsFor(plan, claimant); len(kinds) != 0 {
		t.Errorf("the claiming agent kinds = %v, want none", kinds)
	}
}

// TestPlanCompleteClaimByTheHolderTellsFollowersNothing: a claim that re-selects
// the current holder moves nothing, so no follower learns about an assignment
// that did not happen.
func TestPlanCompleteClaimByTheHolderTellsFollowersNothing(t *testing.T) {
	const requester, holder = 7, 9
	runner, _, follower := followerRunner(t, requester, holder)
	snapshot := notifySnap(t, domain.StateInProgress, 0, wf(claim(1)), requester, holder)

	plan, err := runner.PlanComplete(context.Background(), snapshot, application.CompleteWorkflowCommand{
		TicketID: 1, ActorUserID: holder, ActorName: "Ana", ExpectedPosition: 1,
	})
	if err != nil {
		t.Fatalf("PlanComplete: %v", err)
	}
	for _, n := range plan.Notifications {
		if n.Kind == domain.NotificationAssigneeChanged {
			t.Fatalf("plan carries an assignee_changed row for a claim that moved nothing: %+v", n)
		}
	}
	_ = follower
}
