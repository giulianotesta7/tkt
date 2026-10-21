package sqlite

import (
	"context"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// Issue #344 slice B2 verifier blocker, spec S10 "never the person who
// performed the action": a least_loaded step selects its assignee INSIDE the
// transaction, and the selection can land back on the agent whose completion
// triggered the routing (a single-member desk, or that agent being the least
// loaded member). The "assigned" template carried the recipient as unknown but
// the actor as nil, so the filter that drops the actor for every other kind
// could not fire: the agent was told about their own action.
func TestApplyWorkflowPlanSkipsTheActorForLeastLoaded(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	cat := seedCategory(t, s, "Least-Actor")
	requester := seedUserRole(t, s, "Rita", "rita-actor@x", true, domain.RoleUser)
	agent := seedUserRole(t, s, "Ana", "ana-actor@x", true, domain.RoleAgent)
	// A single-member desk: least_loaded routing can only select the agent.
	deskID := seedDeskWithMember(t, s, agent)
	def := domain.WorkflowDefinition{
		{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "Triage"}},
		{Type: domain.StepAssignToDesk, AssignToDesk: &domain.AssignToDeskStep{DeskID: deskID, Strategy: domain.StrategyLeastLoaded}},
	}
	versionID := seedPublished(t, s, cat, def)
	ticket := seedPinnedTicket(t, s, domain.Ticket{Number: 21, Title: "Triage", CategoryID: cat,
		Priority: domain.PriorityMedium, State: domain.StateInProgress, RequesterUserID: &requester,
		UserID: &agent, CreatedAt: testClock, UpdatedAt: testClock, WorkflowVersionID: &versionID})
	seedRun(t, s, ticket.ID, 0, "active", testClock)

	runner := application.NewWorkflowRunner(terminalClock{now: testClock})
	snap := application.WorkflowExecutionSnapshot{
		Ticket:    &ticket,
		Run:       &application.WorkflowRun{TicketID: ticket.ID, CurrentStepIndex: 0, Status: "active", StartedAt: testClock},
		Workflow:  def,
		Requester: &application.NotificationRecipient{UserID: requester, Role: domain.RoleUser, Active: true},
		Assignee:  &application.NotificationRecipient{UserID: agent, Role: domain.RoleAgent, Active: true},
	}
	plan, err := runner.PlanComplete(ctx, snap, application.CompleteWorkflowCommand{
		TicketID: ticket.ID, ActorUserID: agent, ActorName: "Ana", ExpectedPosition: 1,
	})
	if err != nil {
		t.Fatalf("plan complete: %v", err)
	}
	if _, err := newWorkflowUnitOfWork(s.db).ApplyWorkflowPlan(ctx, plan); err != nil {
		t.Fatalf("apply plan: %v", err)
	}

	// The routing itself stays: the agent remains the assignee.
	stored, err := s.TicketStore().GetByID(ctx, ticket.ID, application.TicketQuery{Scope: application.ScopeAll})
	if err != nil {
		t.Fatalf("read ticket: %v", err)
	}
	if stored.UserID == nil || *stored.UserID != agent {
		t.Fatalf("assignee = %v, want the selected agent %d", stored.UserID, agent)
	}
	// ...but the agent is never told about their own action.
	rows, err := s.NotificationStore().ListForUser(ctx, agent, 10)
	if err != nil {
		t.Fatalf("list notifications: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("%d notifications for the acting agent, want none: %+v", len(rows), rows)
	}
	// The requester is not the actor and gets nothing either: the state did not
	// change, and the only S10 row here is the assignment, addressed to the
	// assignee the routing selected.
	if rows, err := s.NotificationStore().ListForUser(ctx, requester, 10); err != nil {
		t.Fatalf("list requester notifications: %v", err)
	} else if len(rows) != 0 {
		t.Fatalf("requester notifications = %+v, want none", rows)
	}
}
