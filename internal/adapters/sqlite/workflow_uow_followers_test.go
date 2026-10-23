package sqlite

import (
	"context"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// Followers of a workflow-routed assignment (issue #418). A least_loaded step
// selects its assignee INSIDE the transaction, so the follow-up rows are written
// there: the selected member learns "assigned", the ticket's followers learn
// "assignee_changed" with the selected person's display name, and the same
// transaction still rolls everything back on a later failure.

func addDeskMember(t *testing.T, s *Store, deskID, userID int64) {
	t.Helper()
	if _, err := s.db.ExecContext(context.Background(),
		`INSERT INTO desk_members (desk_id, user_id, created_at) VALUES (?, ?, ?)`,
		deskID, userID, formatTime(testClock)); err != nil {
		t.Fatalf("add desk member %d: %v", userID, err)
	}
}

// routedTicketFixture seeds a pinned ticket at a manual step followed by
// least_loaded routing, with the given desk and pre-plan assignee, plus one
// follower of the ticket. It returns the ticket, the runner plan and every id the
// assertions name.
type routedFixture struct {
	s         *Store
	ticket    domain.Ticket
	plan      application.WorkflowMutationPlan
	requester int64
	holder    int64
	other     int64
	follower  int64
	deskID    int64
}

func newRoutedFixture(t *testing.T, withSecondMember bool) *routedFixture {
	t.Helper()
	s := newTestDB(t)
	ctx := context.Background()
	f := &routedFixture{s: s}
	cat := seedCategory(t, s, "Routing with followers")
	f.requester = seedUserRole(t, s, "Rita", "rita-routing@example.com", true, domain.RoleUser)
	f.holder = seedUserRole(t, s, "Ana", "ana-routing@example.com", true, domain.RoleAgent)
	f.follower = seedUserRole(t, s, "Bob", "bob-routing@example.com", true, domain.RoleAdmin)
	f.deskID = seedDeskWithMember(t, s, f.holder)
	if withSecondMember {
		f.other = seedUserRole(t, s, "Otto", "otto-routing@example.com", true, domain.RoleAgent)
		addDeskMember(t, s, f.deskID, f.other)
	}
	def := domain.WorkflowDefinition{
		{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "Triage"}},
		{Type: domain.StepAssignToDesk, AssignToDesk: &domain.AssignToDeskStep{DeskID: f.deskID, Strategy: domain.StrategyLeastLoaded}},
	}
	versionID := seedPublished(t, s, cat, def)
	f.ticket = seedPinnedTicket(t, s, domain.Ticket{Number: 31, Title: "Routing", CategoryID: cat,
		Priority: domain.PriorityMedium, State: domain.StateInProgress, RequesterUserID: &f.requester,
		UserID: &f.holder, CreatedAt: testClock, UpdatedAt: testClock, WorkflowVersionID: &versionID})
	seedRun(t, s, f.ticket.ID, 0, "active", testClock)
	if err := s.TicketFollowerStore().Follow(ctx, f.ticket.ID, f.follower, testClock); err != nil {
		t.Fatalf("follow: %v", err)
	}
	// The holder follows the ticket too: routing that moves the ticket away from
	// her must not address her afterwards.
	if err := s.TicketFollowerStore().Follow(ctx, f.ticket.ID, f.holder, testClock); err != nil {
		t.Fatalf("follow (holder): %v", err)
	}

	followSvc := application.NewFollowService(s.TicketStore(), s.UserStore(), s.TicketFollowerStore(), terminalClock{now: testClock})
	runner := application.NewWorkflowRunner(terminalClock{now: testClock}).WithFollowers(followSvc)
	plan, err := runner.PlanComplete(ctx, application.WorkflowExecutionSnapshot{
		Ticket:    &f.ticket,
		Run:       &application.WorkflowRun{TicketID: f.ticket.ID, CurrentStepIndex: 0, Status: "active", StartedAt: testClock},
		Workflow:  def,
		Requester: &application.NotificationRecipient{UserID: f.requester, Role: domain.RoleUser, Active: true},
		Assignee:  &application.NotificationRecipient{UserID: f.holder, Role: domain.RoleAgent, Active: true},
	}, application.CompleteWorkflowCommand{
		TicketID: f.ticket.ID, ActorUserID: f.holder, ActorName: "Ana", ExpectedPosition: 1,
	})
	if err != nil {
		t.Fatalf("plan routing: %v", err)
	}
	f.plan = plan
	return f
}

// rows returns the stored notifications of one recipient for the fixture ticket.
func (f *routedFixture) rows(t *testing.T, userID int64) []domain.Notification {
	t.Helper()
	rows, err := f.s.NotificationStore().ListForUser(context.Background(), userID, 20)
	if err != nil {
		t.Fatalf("list notifications for %d: %v", userID, err)
	}
	var out []domain.Notification
	for _, n := range rows {
		if n.TicketID == f.ticket.ID {
			out = append(out, n)
		}
	}
	return out
}

// TestApplyWorkflowPlanTellsFollowersWhoTheRoutingSelected: the routing moves the
// ticket to the least loaded member, so he is told "assigned" and every other
// follower is told "assignee_changed" naming him — in the same transaction.
func TestApplyWorkflowPlanTellsFollowersWhoTheRoutingSelected(t *testing.T) {
	f := newRoutedFixture(t, true)
	ctx := context.Background()

	if _, err := newWorkflowUnitOfWork(f.s.db).ApplyWorkflowPlan(ctx, f.plan); err != nil {
		t.Fatalf("apply plan: %v", err)
	}

	stored, err := f.s.TicketStore().GetByID(ctx, f.ticket.ID, application.TicketQuery{Scope: application.ScopeAll})
	if err != nil {
		t.Fatalf("read ticket: %v", err)
	}
	if stored.UserID == nil || *stored.UserID != f.other {
		t.Fatalf("assignee = %v, want the least loaded member %d", stored.UserID, f.other)
	}

	got := f.rows(t, f.other)
	if len(got) != 1 || got[0].Kind != domain.NotificationAssigned {
		t.Fatalf("selected member rows = %+v, want one assigned", got)
	}

	got = f.rows(t, f.follower)
	if len(got) != 1 {
		t.Fatalf("follower rows = %+v, want exactly one", got)
	}
	n := got[0]
	if n.Kind != domain.NotificationAssigneeChanged {
		t.Errorf("follower kind = %q, want %q", n.Kind, domain.NotificationAssigneeChanged)
	}
	if n.ToValue == nil || *n.ToValue != "Otto" {
		t.Errorf("follower to_value = %v, want the selected member's display name", n.ToValue)
	}
	if n.ActorUserID == nil || *n.ActorUserID != f.holder {
		t.Errorf("follower actor = %v, want the agent who ran the step %d", n.ActorUserID, f.holder)
	}

	// The outgoing assignee lost the ticket to this change, and she also ran the
	// step: she hears nothing at all.
	if rows := f.rows(t, f.holder); len(rows) != 0 {
		t.Fatalf("outgoing assignee rows = %+v, want none", rows)
	}
}

// TestApplyWorkflowPlanReselectingTheHolderWritesNoAssigneeChanged: a routing step
// that keeps the holder moves nothing, so no follower is told about an
// assignment.
func TestApplyWorkflowPlanReselectingTheHolderWritesNoAssigneeChanged(t *testing.T) {
	f := newRoutedFixture(t, false)
	ctx := context.Background()

	if _, err := newWorkflowUnitOfWork(f.s.db).ApplyWorkflowPlan(ctx, f.plan); err != nil {
		t.Fatalf("apply plan: %v", err)
	}
	if got := f.rows(t, f.follower); len(got) != 0 {
		t.Fatalf("follower rows = %+v, want none", got)
	}
	if got := f.rows(t, f.holder); len(got) != 0 {
		t.Fatalf("holder rows = %+v, want none", got)
	}
	var count int64
	if err := f.s.db.QueryRow(`SELECT COUNT(*) FROM notifications WHERE ticket_id = ?`, f.ticket.ID).Scan(&count); err != nil {
		t.Fatalf("count notifications: %v", err)
	}
	if count != 0 {
		t.Fatalf("%d notifications for a routing step that moved nothing", count)
	}
}

// TestApplyWorkflowPlanNeverTellsTheActorWhoFollows: the row carries the actor, so
// the adapter drops their row even when the carried list names them — the person
// who ran the step is never told about their own action.
func TestApplyWorkflowPlanNeverTellsTheActorWhoFollows(t *testing.T) {
	f := newRoutedFixture(t, true)
	ctx := context.Background()
	// The plan already leaves the outgoing assignee out; this proves the adapter
	// does not depend on that, because the actor is the one person a row must
	// never reach.
	templateFound := false
	for i, op := range f.plan.Operations {
		ll, ok := op.(application.LeastLoadedAssignmentOperation)
		if !ok || ll.NotifyAssignee == nil {
			continue
		}
		ll.NotifyAssignee.Followers = append(ll.NotifyAssignee.Followers, f.holder)
		f.plan.Operations[i] = ll
		templateFound = true
	}
	if !templateFound {
		t.Fatal("plan carries no least_loaded assignee template")
	}

	if _, err := newWorkflowUnitOfWork(f.s.db).ApplyWorkflowPlan(ctx, f.plan); err != nil {
		t.Fatalf("apply plan: %v", err)
	}
	if rows := f.rows(t, f.holder); len(rows) != 0 {
		t.Fatalf("the acting follower rows = %+v, want none", rows)
	}
	if rows := f.rows(t, f.follower); len(rows) != 1 {
		t.Fatalf("the other follower rows = %+v, want one", rows)
	}
}

// TestApplyWorkflowPlanRollsBackTheFollowerRows: a refused notification is the
// LAST write, so the assignment, the audit and the follower rows are already in
// the transaction when it fails — and none of them survives.
func TestApplyWorkflowPlanRollsBackTheFollowerRows(t *testing.T) {
	f := newRoutedFixture(t, true)
	ctx := context.Background()
	// A policy bug that must never be stored: the actor is told about their own
	// action. appendNotificationsTx validates the plan rows AFTER the operations.
	actor := f.holder
	f.plan.Notifications = append(f.plan.Notifications, domain.Notification{
		UserID: actor, TicketID: f.ticket.ID, Kind: domain.NotificationStateChanged, ActorUserID: &actor, CreatedAt: testClock,
	})

	if _, err := newWorkflowUnitOfWork(f.s.db).ApplyWorkflowPlan(ctx, f.plan); err == nil {
		t.Fatal("apply accepted a notification addressed to its own actor")
	}

	if rows := f.rows(t, f.other); len(rows) != 0 {
		t.Fatalf("selected member rows = %+v, want none", rows)
	}
	if rows := f.rows(t, f.follower); len(rows) != 0 {
		t.Fatalf("follower rows = %+v, want none", rows)
	}
	stored, err := f.s.TicketStore().GetByID(ctx, f.ticket.ID, application.TicketQuery{Scope: application.ScopeAll})
	if err != nil {
		t.Fatalf("read ticket: %v", err)
	}
	if stored.UserID == nil || *stored.UserID != f.holder {
		t.Fatalf("assignee = %v, want the pre-plan holder %d", stored.UserID, f.holder)
	}
	var audits int64
	if err := f.s.db.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE ticket_id = ?`, f.ticket.ID).Scan(&audits); err != nil {
		t.Fatalf("count audits: %v", err)
	}
	if audits != 0 {
		t.Fatalf("%d audit rows survived the rollback", audits)
	}
}
