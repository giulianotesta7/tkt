package sqlite

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// Issue #344 slice B2, storage half (spec S11): the ticket change, its audit
// events, and the notifications the APPLICATION computed commit or roll back
// TOGETHER. The adapter only inserts rows; it never decides a recipient — with
// one documented exception, the least_loaded selection, which is a persistence
// fact and arrives as a template the adapter fills with the member it chose.

// mutationFixture seeds a new ticket whose requester is a user-role person and
// whose assignee is an agent.
func mutationFixture(t *testing.T, s *Store) (requester, agent int64, tk domain.Ticket) {
	t.Helper()
	requester = seedUserRole(t, s, "Rita", "rita@x", true, domain.RoleUser)
	agent = seedUserRole(t, s, "Ana", "ana@x", true, domain.RoleAgent)
	cat := seedCategory(t, s, "Notif-Cat")
	tk = seedTicket(t, s, domain.Ticket{Number: 1, Title: "VPN drops", CategoryID: cat,
		Priority: domain.PriorityMedium, State: domain.StateNew,
		RequesterUserID: &requester, UserID: &agent, CreatedAt: testClock, UpdatedAt: testClock})
	return requester, agent, tk
}

func strVal(v string) *string { return &v }

// TestUnitOfWorkCommitsTheChangeWithItsNotifications is the success half of
// S11: one call persists the ticket write, the audit event, and the
// notification, so the change and the news about it are inseparable.
func TestUnitOfWorkCommitsTheChangeWithItsNotifications(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	requester, _, tk := mutationFixture(t, s)
	at := testClock.Add(time.Minute)
	tk.State = domain.StateInProgress
	tk.UpdatedAt = at

	err := s.TicketUnitOfWork().Update(ctx, application.TicketMutation{
		Ticket: &tk,
		Events: []domain.AuditEvent{{TicketID: tk.ID, Actor: "Ada", Action: domain.ActionTransition,
			Field: strVal("state"), FromValue: strVal("new"), ToValue: strVal("in_progress"), CreatedAt: at}},
		Notifications: []domain.Notification{{UserID: requester, TicketID: tk.ID,
			Kind: domain.NotificationStateChanged, FromValue: strVal("new"), ToValue: strVal("in_progress"), CreatedAt: at}},
	})
	if err != nil {
		t.Fatalf("unit update: %v", err)
	}

	stored, err := s.TicketStore().GetByID(ctx, tk.ID, application.TicketQuery{Scope: application.ScopeAll})
	if err != nil {
		t.Fatalf("read ticket: %v", err)
	}
	if stored.State != domain.StateInProgress {
		t.Errorf("ticket state = %s, want in_progress", stored.State)
	}
	events, err := s.AuditStore().ListByTicket(ctx, tk.ID)
	if err != nil {
		t.Fatalf("list audits: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("%d audit events, want 1", len(events))
	}
	rows, err := s.NotificationStore().ListForUser(ctx, requester, 10)
	if err != nil {
		t.Fatalf("list notifications: %v", err)
	}
	if len(rows) != 1 || rows[0].Kind != domain.NotificationStateChanged {
		t.Fatalf("notifications = %+v, want the single state change", rows)
	}
}

// TestUnitOfWorkRollsBackTheChangeWhenANotificationIsRefused is the failure
// half of S11 and the acceptance criterion "a change that fails leaves no
// notification, and a notification never exists without its change". The bad
// row is last in the batch, so the failure happens AFTER the ticket write: only
// the shared transaction can undo it.
func TestUnitOfWorkRollsBackTheChangeWhenANotificationIsRefused(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	requester, agent, tk := mutationFixture(t, s)
	at := testClock.Add(time.Minute)
	tk.State = domain.StateInProgress
	tk.UpdatedAt = at

	err := s.TicketUnitOfWork().Update(ctx, application.TicketMutation{
		Ticket: &tk,
		Events: []domain.AuditEvent{{TicketID: tk.ID, Actor: "Ana", Action: domain.ActionTransition,
			Field: strVal("state"), FromValue: strVal("new"), ToValue: strVal("in_progress"), CreatedAt: at}},
		Notifications: []domain.Notification{
			{UserID: requester, TicketID: tk.ID, Kind: domain.NotificationStateChanged, CreatedAt: at},
			// A policy bug: the actor is the recipient. The domain refuses it.
			{UserID: agent, TicketID: tk.ID, Kind: domain.NotificationStateChanged,
				ActorUserID: &agent, CreatedAt: at},
		},
	})
	if err == nil {
		t.Fatal("unit update succeeded, want the refused notification to fail the change")
	}
	var invariant *domain.NotificationInvariantError
	if !errors.As(err, &invariant) {
		t.Fatalf("error = %v, want a *domain.NotificationInvariantError", err)
	}

	stored, err := s.TicketStore().GetByID(ctx, tk.ID, application.TicketQuery{Scope: application.ScopeAll})
	if err != nil {
		t.Fatalf("read ticket: %v", err)
	}
	if stored.State != domain.StateNew {
		t.Errorf("ticket state = %s, want new (rolled back)", stored.State)
	}
	if events, err := s.AuditStore().ListByTicket(ctx, tk.ID); err != nil {
		t.Fatalf("list audits: %v", err)
	} else if len(events) != 0 {
		t.Errorf("%d audit events survived, want 0", len(events))
	}
	for _, userID := range []int64{requester, agent} {
		rows, err := s.NotificationStore().ListForUser(ctx, userID, 10)
		if err != nil {
			t.Fatalf("list notifications: %v", err)
		}
		if len(rows) != 0 {
			t.Errorf("user %d has %+v, want no notification from a rolled-back change", userID, rows)
		}
	}
}

// TestApplyWorkflowPlanInsertsTheComputedNotifications proves the plan path
// carries the application-computed rows into the SAME transaction as the
// operations, the ticket write, and the cursor CAS: an assignee completing a
// manual task that auto-resolves the ticket leaves exactly one notification,
// for the requester, who is the person not acting.
func TestApplyWorkflowPlanInsertsTheComputedNotifications(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	cat := seedCategory(t, s, "Plan-Notif")
	requester := seedUserRole(t, s, "Rita", "rita-plan@x", true, domain.RoleUser)
	agent := seedUserRole(t, s, "Ana", "ana-plan@x", true, domain.RoleAgent)
	def := domain.WorkflowDefinition{
		{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "Rack it"}},
		{Type: domain.StepResolve},
	}
	versionID := seedPublished(t, s, cat, def)
	ticket := seedPinnedTicket(t, s, domain.Ticket{Number: 2, Title: "Rack", CategoryID: cat,
		Priority: domain.PriorityMedium, State: domain.StateNew, RequesterUserID: &requester,
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
	if len(plan.Notifications) != 1 {
		t.Fatalf("plan notifications = %+v, want the requester's resolved-awaiting-confirmation row", plan.Notifications)
	}
	if _, err := newWorkflowUnitOfWork(s.db).ApplyWorkflowPlan(ctx, plan); err != nil {
		t.Fatalf("apply plan: %v", err)
	}

	rows, err := s.NotificationStore().ListForUser(ctx, requester, 10)
	if err != nil {
		t.Fatalf("list notifications: %v", err)
	}
	if len(rows) != 1 || rows[0].Kind != domain.NotificationResolvedAwaitingConfirmation {
		t.Fatalf("requester notifications = %+v, want one resolved_awaiting_confirmation", rows)
	}
	if rows[0].ActorUserID == nil || *rows[0].ActorUserID != agent {
		t.Errorf("actor = %v, want the completing agent %d", rows[0].ActorUserID, agent)
	}
	// The change itself committed with the notification: the ticket is resolved
	// and the run is complete.
	stored, err := s.TicketStore().GetByID(ctx, ticket.ID, application.TicketQuery{Scope: application.ScopeAll})
	if err != nil {
		t.Fatalf("read ticket: %v", err)
	}
	if stored.State != domain.StateResolved {
		t.Errorf("ticket state = %s, want resolved", stored.State)
	}
	if _, status, _ := runRow(t, s, ticket.ID); status != "completed" {
		t.Errorf("run status = %s, want completed", status)
	}
}

// TestApplyWorkflowPlanFillsTheLeastLoadedRecipient proves the ONE recipient the
// application cannot know is filled from the transaction's own selection: the
// automatic least_loaded step notifies the member the desk routing chose — here
// a member who is NOT the acting agent, so the row is written and carries that
// agent as its actor (the human whose completion ran the routing, and the actor
// of the completion's own audit event).
func TestApplyWorkflowPlanFillsTheLeastLoadedRecipient(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	cat := seedCategory(t, s, "Least-Notif")
	requester := seedUserRole(t, s, "Rita", "rita-least@x", true, domain.RoleUser)
	agent := seedUserRole(t, s, "Ana", "ana-least@x", true, domain.RoleAgent)
	chosen := seedUserRole(t, s, "Bea", "bea-least@x", true, domain.RoleAgent)
	deskID := seedDeskWithMember(t, s, chosen)
	def := domain.WorkflowDefinition{
		{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "Triage"}},
		{Type: domain.StepAssignToDesk, AssignToDesk: &domain.AssignToDeskStep{DeskID: deskID, Strategy: domain.StrategyLeastLoaded}},
	}
	versionID := seedPublished(t, s, cat, def)
	ticket := seedPinnedTicket(t, s, domain.Ticket{Number: 3, Title: "Triage", CategoryID: cat,
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
	if len(plan.Notifications) != 0 {
		t.Fatalf("plan notifications = %+v, want none (the state did not change)", plan.Notifications)
	}
	if _, err := newWorkflowUnitOfWork(s.db).ApplyWorkflowPlan(ctx, plan); err != nil {
		t.Fatalf("apply plan: %v", err)
	}

	rows, err := s.NotificationStore().ListForUser(ctx, chosen, 10)
	if err != nil {
		t.Fatalf("list notifications for the selected member: %v", err)
	}
	if len(rows) != 1 || rows[0].Kind != domain.NotificationAssigned {
		t.Fatalf("selected member notifications = %+v, want one assigned row", rows)
	}
	if rows[0].ActorUserID == nil || *rows[0].ActorUserID != agent {
		t.Errorf("actor = %v, want the completing agent %d whose action ran the routing", rows[0].ActorUserID, agent)
	}
	if rows[0].TicketID != ticket.ID {
		t.Errorf("ticket = %d, want %d", rows[0].TicketID, ticket.ID)
	}
}

// TestApplyWorkflowPlanRollsBackWhenANotificationIsRefused is the workflow half
// of the S11 atomicity proof: the plan's notifications are applied inside the
// same immediate transaction as the operations, the ticket write, and the
// cursor CAS, so a refused row leaves the run, the ticket, the audit trail, and
// the notification table exactly as they were.
func TestApplyWorkflowPlanRollsBackWhenANotificationIsRefused(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	cat := seedCategory(t, s, "Plan-Rollback")
	requester := seedUserRole(t, s, "Rita", "rita-rb@x", true, domain.RoleUser)
	agent := seedUserRole(t, s, "Ana", "ana-rb@x", true, domain.RoleAgent)
	def := domain.WorkflowDefinition{
		{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "Rack it"}},
		{Type: domain.StepResolve},
	}
	versionID := seedPublished(t, s, cat, def)
	ticket := seedPinnedTicket(t, s, domain.Ticket{Number: 4, Title: "Rollback", CategoryID: cat,
		Priority: domain.PriorityMedium, State: domain.StateNew, RequesterUserID: &requester,
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
	// A policy bug on TOP of an otherwise valid plan: the actor is addressed.
	plan.Notifications = append(plan.Notifications, domain.Notification{
		UserID: agent, TicketID: ticket.ID, Kind: domain.NotificationStateChanged,
		ActorUserID: &agent, CreatedAt: testClock,
	})

	_, err = newWorkflowUnitOfWork(s.db).ApplyWorkflowPlan(ctx, plan)
	if err == nil {
		t.Fatal("apply plan succeeded, want the refused notification to fail it")
	}
	var invariant *domain.NotificationInvariantError
	if !errors.As(err, &invariant) {
		t.Fatalf("error = %v, want a *domain.NotificationInvariantError", err)
	}

	stored, err := s.TicketStore().GetByID(ctx, ticket.ID, application.TicketQuery{Scope: application.ScopeAll})
	if err != nil {
		t.Fatalf("read ticket: %v", err)
	}
	if stored.State != domain.StateNew {
		t.Errorf("ticket state = %s, want new (rolled back)", stored.State)
	}
	if cursor, status, completedAt := runRow(t, s, ticket.ID); cursor != 0 || status != "active" || completedAt != nil {
		t.Errorf("run = (%d, %s, %v), want (0, active, nil) — the cursor CAS rolled back", cursor, status, completedAt)
	}
	if events, err := s.AuditStore().ListByTicket(ctx, ticket.ID); err != nil {
		t.Fatalf("list audits: %v", err)
	} else if len(events) != 0 {
		t.Errorf("%d audit events survived, want 0", len(events))
	}
	for _, userID := range []int64{requester, agent} {
		if rows, err := s.NotificationStore().ListForUser(ctx, userID, 10); err != nil {
			t.Fatalf("list notifications: %v", err)
		} else if len(rows) != 0 {
			t.Errorf("user %d has %+v, want no notification from a rolled-back plan", userID, rows)
		}
	}
}

// TestWorkflowExecutionSnapshotCarriesRecipientFacts proves the read model the
// notification policy depends on: the snapshot answers the role and active
// state of the requester and assignee, so the APPLICATION can decide "can this
// person open the ticket?" without guessing. A missing person stays nil, and an
// inactive account is reported as inactive rather than dropped silently here —
// the policy is what drops it.
func TestWorkflowExecutionSnapshotCarriesRecipientFacts(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	cat := seedCategory(t, s, "Snapshot-Facts")
	requester := seedUserRole(t, s, "Rita", "rita-snap@x", true, domain.RoleUser)
	agent := seedUserRole(t, s, "Ana", "ana-snap@x", true, domain.RoleAgent)
	def := domain.WorkflowDefinition{{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "Do"}}}
	versionID := seedPublished(t, s, cat, def)
	ticket := seedPinnedTicket(t, s, domain.Ticket{Number: 5, Title: "Facts", CategoryID: cat,
		Priority: domain.PriorityMedium, State: domain.StateNew, RequesterUserID: &requester,
		UserID: &agent, CreatedAt: testClock, UpdatedAt: testClock, WorkflowVersionID: &versionID})
	seedRun(t, s, ticket.ID, 0, "active", testClock)

	snap, err := s.WorkflowRunStore().GetWorkflowExecution(ctx, ticket.ID)
	if err != nil {
		t.Fatalf("GetWorkflowExecution: %v", err)
	}
	if snap.Requester == nil || snap.Requester.UserID != requester ||
		snap.Requester.Role != domain.RoleUser || !snap.Requester.Active {
		t.Fatalf("requester facts = %+v, want (id %d, user, active)", snap.Requester, requester)
	}
	if snap.Assignee == nil || snap.Assignee.UserID != agent ||
		snap.Assignee.Role != domain.RoleAgent || !snap.Assignee.Active {
		t.Fatalf("assignee facts = %+v, want (id %d, agent, active)", snap.Assignee, agent)
	}

	if _, err := s.db.ExecContext(ctx, `UPDATE users SET active = 0 WHERE id = ?`, requester); err != nil {
		t.Fatalf("deactivate requester: %v", err)
	}
	snap, err = s.WorkflowRunStore().GetWorkflowExecution(ctx, ticket.ID)
	if err != nil {
		t.Fatalf("GetWorkflowExecution after deactivation: %v", err)
	}
	if snap.Requester == nil || snap.Requester.Active {
		t.Fatalf("requester facts = %+v, want active=false so the policy drops them", snap.Requester)
	}
}
