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
		row := ll.NotifyAssignee.For(42)
		if row.UserID != 42 || row.Kind != domain.NotificationAssigned {
			t.Errorf("filled row = %+v, want an assigned row for the selected member", row)
		}
		if row.ActorUserID == nil || *row.ActorUserID != agent {
			t.Errorf("filled row actor = %v, want the completing agent %d", row.ActorUserID, agent)
		}
		// A row completed for the ACTOR is the one the unit of work drops: the
		// template hands it every fact that decision needs (the recipient) and
		// the fact it filters on (the actor).
		if actorRow := ll.NotifyAssignee.For(agent); actorRow.ActorUserID == nil || *actorRow.ActorUserID != actorRow.UserID {
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
