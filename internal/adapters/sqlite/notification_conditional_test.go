package sqlite

import (
	"context"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// The ConditionalNotifications contract of issue #344 (S10/S11): a plan that
// routes the ticket with least_loaded cannot know whether the transaction will
// keep the holder or move the ticket, so the rows that describe the holder
// travel as a block keyed to that person. ApplyWorkflowPlan resolves the block
// against the selection it actually made, inside the same transaction:
//
//	| routing outcome       | holder's rows       |
//	| re-selects the holder | written in full     |
//	| moves the ticket      | dropped, not stale  |
//	| any later write fails | rolled back (S11)   |

// conditionalRoutingPlan builds a real runner plan that opens a requester form
// whose answer runs least_loaded routing over a single-member desk holding the
// pre-plan assignee, so the in-transaction selection KEEPS the holder and no
// "assigned" row is written. It returns the plan plus the ids the assertions
// name: the ticket, the holder, and the requester who acted.
func conditionalRoutingPlan(t *testing.T, s *Store) (application.WorkflowMutationPlan, int64, int64, int64) {
	t.Helper()
	ctx := context.Background()
	cat := seedCategory(t, s, "Conditional routing")
	requester := seedUserRole(t, s, "Rita", "rita-conditional@example.com", true, domain.RoleUser)
	holder := seedUserRole(t, s, "Ana", "ana-conditional@example.com", true, domain.RoleAgent)
	deskID := seedDeskWithMember(t, s, holder)
	def := domain.WorkflowDefinition{
		{Type: domain.StepForm, Form: &domain.FormStep{
			Actor:  domain.FormActorRequester,
			Fields: []domain.FormField{{Key: "host", Label: "Host", Kind: domain.FieldShortText}},
		}},
		{Type: domain.StepAssignToDesk, AssignToDesk: &domain.AssignToDeskStep{DeskID: deskID, Strategy: domain.StrategyLeastLoaded}},
	}
	versionID := seedPublished(t, s, cat, def)
	ticket := seedPinnedTicket(t, s, domain.Ticket{Number: 11, Title: "Conditional routing", CategoryID: cat,
		Priority: domain.PriorityMedium, State: domain.StateNew, RequesterUserID: &requester,
		UserID: &holder, CreatedAt: testClock, UpdatedAt: testClock, WorkflowVersionID: &versionID})
	seedRun(t, s, ticket.ID, 0, "active", testClock)

	runner := application.NewWorkflowRunner(terminalClock{now: testClock})
	plan, err := runner.PlanComplete(ctx, application.WorkflowExecutionSnapshot{
		Ticket:    &ticket,
		Run:       &application.WorkflowRun{TicketID: ticket.ID, CurrentStepIndex: 0, Status: "active", StartedAt: testClock},
		Workflow:  def,
		Requester: &application.NotificationRecipient{UserID: requester, Role: domain.RoleUser, Active: true},
		Assignee:  &application.NotificationRecipient{UserID: holder, Role: domain.RoleAgent, Active: true},
	}, application.CompleteWorkflowCommand{
		TicketID: ticket.ID, ActorUserID: requester, ActorName: "Rita", ExpectedPosition: 1,
		RawAnswers: application.RawPositionalValues{{Position: 0, Values: []string{"api-01"}}},
	})
	if err != nil {
		t.Fatalf("plan conditional routing: %v", err)
	}
	return plan, ticket.ID, holder, requester
}

// countTicketNotifications counts every stored notification of one ticket.
func countTicketNotifications(t *testing.T, s *Store, ticketID int64) int64 {
	t.Helper()
	var n int64
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM notifications WHERE ticket_id = ?`, ticketID).Scan(&n); err != nil {
		t.Fatalf("count notifications: %v", err)
	}
	return n
}

// persistedTicketState reads the ticket's persisted state.
func persistedTicketState(t *testing.T, s *Store, ticketID int64) string {
	t.Helper()
	var state string
	if err := s.db.QueryRow(`SELECT state FROM tickets WHERE id = ?`, ticketID).Scan(&state); err != nil {
		t.Fatalf("read ticket state: %v", err)
	}
	return state
}

// TestConditionalAssigneeRowsReleasedForTheKeptHolder: the routing re-selects
// the holder, so the block keyed to her matches the selection and her rows are
// written — the state change she is owed, with the actor who caused it, and
// nothing else (no redundant "assigned": the ticket never left her).
func TestConditionalAssigneeRowsReleasedForTheKeptHolder(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	plan, _, holder, requester := conditionalRoutingPlan(t, s)

	if len(plan.ConditionalNotifications) != 1 {
		t.Fatalf("conditional blocks = %+v, want one for the pre-plan assignee", plan.ConditionalNotifications)
	}
	if got := plan.ConditionalNotifications[0].AssigneeUserID; got != holder {
		t.Fatalf("block key = %d, want the holder %d", got, holder)
	}
	if len(plan.Notifications) != 0 {
		t.Fatalf("unconditional rows = %+v, want none (the requester acted)", plan.Notifications)
	}

	if _, err := newWorkflowUnitOfWork(s.db).ApplyWorkflowPlan(ctx, plan); err != nil {
		t.Fatalf("apply plan: %v", err)
	}
	rows, err := s.NotificationStore().ListForUser(ctx, holder, 10)
	if err != nil {
		t.Fatalf("list holder notifications: %v", err)
	}
	if len(rows) != 1 || rows[0].Kind != domain.NotificationStateChanged {
		t.Fatalf("holder rows = %+v, want exactly one state_changed", rows)
	}
	if rows[0].ActorUserID == nil || *rows[0].ActorUserID != requester {
		t.Errorf("actor = %v, want the requester %d", rows[0].ActorUserID, requester)
	}
}

// TestConditionalAssigneeRowsDroppedWhenTheRoutingMoves: a block keyed to
// somebody the transaction did NOT keep writes nothing at all — the outgoing
// holder may no longer be able to open the ticket (S10 rule 2).
func TestConditionalAssigneeRowsDroppedWhenTheRoutingMoves(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	plan, ticketID, holder, _ := conditionalRoutingPlan(t, s)

	other := seedUserRole(t, s, "Bea", "bea-conditional@example.com", true, domain.RoleAgent)
	plan.ConditionalNotifications[0].AssigneeUserID = other
	if _, err := newWorkflowUnitOfWork(s.db).ApplyWorkflowPlan(ctx, plan); err != nil {
		t.Fatalf("apply plan: %v", err)
	}

	if n := countTicketNotifications(t, s, ticketID); n != 0 {
		t.Fatalf("notifications = %d, want 0 (the block key does not match the selection)", n)
	}
	if rows, err := s.NotificationStore().ListForUser(ctx, holder, 10); err != nil {
		t.Fatalf("list holder notifications: %v", err)
	} else if len(rows) != 0 {
		t.Fatalf("holder rows = %+v, want none", rows)
	}
}

// TestConditionalAssigneeRowsRollBackWithTheTransaction: S11 — a failure AFTER
// the holder's block was released leaves no notification, no ticket write, no
// audit, and an unchanged cursor. The second block is refused by the domain
// invariant (recipient == actor), which is why it can only fail after the first
// block has already been written.
func TestConditionalAssigneeRowsRollBackWithTheTransaction(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	plan, ticketID, holder, requester := conditionalRoutingPlan(t, s)
	plan.ConditionalNotifications = append(plan.ConditionalNotifications, application.ConditionalAssigneeNotification{
		AssigneeUserID: holder,
		Rows: []domain.Notification{{
			UserID: requester, TicketID: ticketID, Kind: domain.NotificationStateChanged,
			ActorUserID: &requester, CreatedAt: testClock,
		}},
	})

	if _, err := newWorkflowUnitOfWork(s.db).ApplyWorkflowPlan(ctx, plan); err == nil {
		t.Fatal("apply succeeded, want the refused conditional row to fail it")
	}
	if n := countTicketNotifications(t, s, ticketID); n != 0 {
		t.Fatalf("notifications = %d, want 0 (rolled back)", n)
	}
	if state := persistedTicketState(t, s, ticketID); state != "new" {
		t.Fatalf("state = %s, want new (rolled back)", state)
	}
	if cursor, status, completedAt := runRow(t, s, ticketID); cursor != 0 || status != "active" || completedAt != nil {
		t.Fatalf("run = (%d,%s,%v), want (0,active,nil)", cursor, status, completedAt)
	}
	if events, err := s.AuditStore().ListByTicket(ctx, ticketID); err != nil {
		t.Fatalf("list audits: %v", err)
	} else if len(events) != 0 {
		t.Fatalf("%d audit events survived, want 0", len(events))
	}
}
