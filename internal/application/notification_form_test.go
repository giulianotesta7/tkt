package application_test

import (
	"context"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// Issue #344 slice B2, form_waiting row (S10 last row): "a workflow form step
// now waits on this person" — the requester for a requester form, the assignee
// for an assignee form, never the person who just acted. Confirmed by the
// maintainer on e6-batch-2 L20: whoever must act is told.
//
// The assignee form reached through automatic least_loaded routing cannot be
// decided here (its recipient exists only inside the transaction), so it travels
// on the routing operation's template; the HTTP layer proves that half end to
// end (workflow_notification_routing_test.go).

func formWaitingKinds(pl application.WorkflowMutationPlan, userID int64) []domain.Notification {
	var out []domain.Notification
	for _, n := range pl.Notifications {
		if n.UserID == userID && n.Kind == domain.NotificationFormWaiting {
			out = append(out, n)
		}
	}
	return out
}

// TestPlanCompleteFormWaitingNotifiesTheRequester: an assignee finishing a
// manual task opens a requester form — the requester now has something to do.
func TestPlanCompleteFormWaitingNotifiesTheRequester(t *testing.T) {
	const requester, agent = 7, 9
	r := application.NewWorkflowRunner(fixedClock())
	def := wf(man(), fm(domain.FormActorRequester, domain.FormField{Key: "host", Label: "Host", Kind: domain.FieldShortText}))
	snapshot := notifySnap(t, domain.StateInProgress, 0, def, requester, agent)

	plan, err := r.PlanComplete(context.Background(), snapshot, application.CompleteWorkflowCommand{
		TicketID: 1, ActorUserID: agent, ActorName: "Ana", ExpectedPosition: 1,
	})
	if err != nil {
		t.Fatalf("PlanComplete: %v", err)
	}
	got := formWaitingKinds(plan, requester)
	if len(got) != 1 {
		t.Fatalf("requester form_waiting rows = %+v, want exactly 1", got)
	}
	n := got[0]
	if n.StepIndex == nil || *n.StepIndex != plan.NextCursor {
		t.Errorf("step index = %v, want the newly current step %d", n.StepIndex, plan.NextCursor)
	}
	if n.ActorUserID == nil || *n.ActorUserID != agent {
		t.Errorf("actor = %v, want the agent who completed the manual task %d", n.ActorUserID, agent)
	}
	if n.TicketID != 1 {
		t.Errorf("ticket = %d, want 1", n.TicketID)
	}
}

// TestPlanCompleteFormWaitingNotifiesTheAssignee: a requester answering their
// form opens an assignee form — the agent now has something to do.
func TestPlanCompleteFormWaitingNotifiesTheAssignee(t *testing.T) {
	const requester, agent = 7, 9
	r := application.NewWorkflowRunner(fixedClock())
	def := wf(
		fm(domain.FormActorRequester, domain.FormField{Key: "host", Label: "Host", Kind: domain.FieldShortText}),
		fm(domain.FormActorAssignee, domain.FormField{Key: "done", Label: "Done", Kind: domain.FieldShortText}),
	)
	snapshot := notifySnap(t, domain.StateInProgress, 0, def, requester, agent)

	plan, err := r.PlanComplete(context.Background(), snapshot, application.CompleteWorkflowCommand{
		TicketID: 1, ActorUserID: requester, ActorName: "Rita", ExpectedPosition: 1,
		RawAnswers: application.RawPositionalValues{{Position: 0, Values: []string{"api-01"}}},
	})
	if err != nil {
		t.Fatalf("PlanComplete: %v", err)
	}
	got := formWaitingKinds(plan, agent)
	if len(got) != 1 {
		t.Fatalf("assignee form_waiting rows = %+v, want exactly 1", got)
	}
	if got[0].StepIndex == nil || *got[0].StepIndex != 1 {
		t.Errorf("step index = %v, want 1", got[0].StepIndex)
	}
	if got[0].ActorUserID == nil || *got[0].ActorUserID != requester {
		t.Errorf("actor = %v, want the requester who answered %d", got[0].ActorUserID, requester)
	}
}

// TestPlanCompleteFormWaitingSkipsTheActor: a form waiting on the person who
// just answered it is their own next action, not news — the actor rule drops it.
func TestPlanCompleteFormWaitingSkipsTheActor(t *testing.T) {
	const requester, agent = 7, 9
	r := application.NewWorkflowRunner(fixedClock())
	field := domain.FormField{Key: "k", Label: "l", Kind: domain.FieldShortText}
	def := wf(fm(domain.FormActorRequester, field), fm(domain.FormActorRequester, field))
	snapshot := notifySnap(t, domain.StateInProgress, 0, def, requester, agent)

	plan, err := r.PlanComplete(context.Background(), snapshot, application.CompleteWorkflowCommand{
		TicketID: 1, ActorUserID: requester, ActorName: "Rita", ExpectedPosition: 1,
		RawAnswers: application.RawPositionalValues{{Position: 0, Values: []string{"a"}}},
	})
	if err != nil {
		t.Fatalf("PlanComplete: %v", err)
	}
	if len(plan.Notifications) != 0 {
		t.Fatalf("plan notifications = %+v, want none — the next form waits on the actor", plan.Notifications)
	}
}
