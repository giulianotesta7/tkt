package sqlite

import (
	"context"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// Notification recipients are re-proved against the ticket the plan LEAVES
// BEHIND, inside the transaction that persists the rows (issue #418). A plan
// decides its recipients from the snapshot it was built on, so a membership
// change between the plan and the apply can still name somebody the change
// itself takes out of the ticket's read scope. These probes run the real
// services against the real store: the runner plans, the desk service changes
// the membership, and the unit of work applies the plan.

// claimThenClaimArrangement builds the claim fixture whose run waits on a second
// claim of the desk it returns, so a follower of BOTH desks is eligible when the
// plan is made and can lose that eligibility before the apply.
func claimThenClaimArrangement(t *testing.T) (*claimFollowersFixture, int64) {
	t.Helper()
	var arriving int64
	f := newClaimFollowersFixture(t, func(deskID int64) []domain.WorkflowStep {
		arriving = deskID
		return []domain.WorkflowStep{{Type: domain.StepAssignToDesk,
			AssignToDesk: &domain.AssignToDeskStep{DeskID: deskID, Strategy: domain.StrategyClaim}}}
	})
	return f, arriving
}

// removeArrivingDeskMembership takes one follower out of the desk the plan's run
// waits on, through the real desk service.
func removeArrivingDeskMembership(t *testing.T, f *claimFollowersFixture, deskID, userID int64) {
	t.Helper()
	ctx := context.Background()
	admin, err := f.s.UserStore().GetByID(ctx, f.admin)
	if err != nil {
		t.Fatalf("read the administrator: %v", err)
	}
	desks := application.NewDeskService(f.s.DeskStore(), f.s.UserStore(), terminalClock{now: testClock})
	if err := desks.RemoveMember(ctx, *admin, deskID, userID); err != nil {
		t.Fatalf("remove the follower from the arriving desk: %v", err)
	}
}

// TestApplyWorkflowPlanRefusesTheFollowerRemovedFromTheArrivingDesk is the
// membership-removal window: the both-desk follower is eligible when the plan is
// built, the desk service removes them from the arriving desk, and the apply then
// persists the resulting ticket. They lose the read scope in that same instant,
// so no row for them may be written — while the change itself, the subscription
// and every recipient the resulting ticket still admits persist.
func TestApplyWorkflowPlanRefusesTheFollowerRemovedFromTheArrivingDesk(t *testing.T) {
	f, arriving := claimThenClaimArrangement(t)
	ctx := context.Background()
	if !f.resultingReadScope(t, f.both) {
		t.Fatal("arrangement: the both-desk follower must read the claim-pending ticket")
	}
	removeArrivingDeskMembership(t, f, arriving, f.both)

	result, err := f.s.WorkflowUnitOfWork().ApplyWorkflowPlan(ctx, f.plan)
	if err != nil {
		t.Fatalf("apply plan: %v", err)
	}
	if result.Ticket.State != domain.StateInProgress || result.Ticket.UserID == nil || *result.Ticket.UserID != f.claimant {
		t.Fatalf("result ticket = (%s, %v), want in_progress held by the claimant", result.Ticket.State, result.Ticket.UserID)
	}
	if result.Run.CurrentStepIndex != 1 || result.Run.Status != "active" {
		t.Fatalf("result run = (%d, %s), want the arriving claim at (1, active)", result.Run.CurrentStepIndex, result.Run.Status)
	}
	if f.resultingReadScope(t, f.both) {
		t.Fatal("the removed follower still reads the resulting ticket")
	}

	f.assertRows(t,
		f.row(f.requester, "state_changed", "new", "in_progress"),
		f.row(f.admin, "state_changed", "new", "in_progress"),
	)
	if rows := notificationRowsOf(t, f.s, f.both); len(rows) != 0 {
		t.Fatalf("the removed follower got %v, want nothing", rows)
	}
	if unread, err := f.s.NotificationStore().UnreadCount(ctx, f.both); err != nil {
		t.Fatalf("read the follower's unread count: %v", err)
	} else if unread != 0 {
		t.Fatalf("the removed follower has %d unread notifications, want 0", unread)
	}
	// Losing access is not unfollowing: the subscription survives.
	f.assertFollowerRowsKept(t)
}

// TestApplyWorkflowPlanKeepsTheFollowerOfBothClaimDesks is the same plan without
// the membership change: the follower of both desks reads the resulting ticket,
// so the transaction keeps their row. Eligibility comes from the resulting desk's
// real membership, never from the fact that they follow the ticket.
func TestApplyWorkflowPlanKeepsTheFollowerOfBothClaimDesks(t *testing.T) {
	f, _ := claimThenClaimArrangement(t)
	ctx := context.Background()
	// A row the follower already owns is outside this unit: the guard decides what
	// to insert and never deletes what is stored.
	from, to := string(domain.PriorityLow), string(domain.PriorityHigh)
	insertNotifications(t, f.s, domain.Notification{
		UserID: f.both, TicketID: f.ticket.ID, Kind: domain.NotificationPriorityChanged,
		FromValue: &from, ToValue: &to, CreatedAt: testClock,
	})
	if _, err := f.s.WorkflowUnitOfWork().ApplyWorkflowPlan(ctx, f.plan); err != nil {
		t.Fatalf("apply plan: %v", err)
	}
	if !f.resultingReadScope(t, f.both) {
		t.Fatal("the both-desk follower lost the resulting read scope")
	}
	f.assertRows(t,
		f.row(f.requester, "state_changed", "new", "in_progress"),
		f.row(f.both, "state_changed", "new", "in_progress"),
		f.row(f.both, "priority_changed", "low", "high"),
		f.row(f.admin, "state_changed", "new", "in_progress"),
	)
	if rows := notificationRowsOf(t, f.s, f.both); len(rows) != 2 || rows[0] != "priority_changed|low|high" || rows[1] != "state_changed|new|in_progress" {
		t.Fatalf("the both-desk follower got %v, want the stored row kept plus one state change", rows)
	}
	f.assertFollowerRowsKept(t)
}

// TestApplyWorkflowPlanKeepsTheRoutingRowForTheSelectedMember proves the rows a
// least_loaded step builds inside the transaction are judged the same way: the
// selected member holds the resulting ticket, so the "assigned" row survives,
// while the plan's state rows still reach the recipients the change owes.
func TestApplyWorkflowPlanKeepsTheRoutingRowForTheSelectedMember(t *testing.T) {
	f := newClaimFollowersFixture(t, func(deskID int64) []domain.WorkflowStep {
		return []domain.WorkflowStep{{Type: domain.StepAssignToDesk,
			AssignToDesk: &domain.AssignToDeskStep{DeskID: deskID, Strategy: domain.StrategyLeastLoaded}}}
	})
	ctx := context.Background()
	result, err := f.s.WorkflowUnitOfWork().ApplyWorkflowPlan(ctx, f.plan)
	if err != nil {
		t.Fatalf("apply plan: %v", err)
	}
	if result.Ticket.UserID == nil || *result.Ticket.UserID != f.both {
		t.Fatalf("assignee = %v, want the routing desk's only member %d", result.Ticket.UserID, f.both)
	}
	f.assertRows(t,
		f.row(f.requester, "state_changed", "new", "in_progress"),
		f.row(f.admin, "state_changed", "new", "in_progress"),
		f.row(f.both, "assigned", "<nil>", "<nil>"),
	)
	if rows := notificationRowsOf(t, f.s, f.both); len(rows) != 1 || rows[0] != "assigned|<nil>|<nil>" {
		t.Fatalf("the selected member got %v, want the routing row", rows)
	}
}

// TestApplyWorkflowPlanRollsBackWhenARowIsRefused keeps atomicity intact around
// the new guard: a refused row still aborts the whole apply, so neither the
// recipients the guard admitted nor the ticket and run writes survive.
func TestApplyWorkflowPlanRollsBackWhenARowIsRefused(t *testing.T) {
	f, arriving := claimThenClaimArrangement(t)
	ctx := context.Background()
	removeArrivingDeskMembership(t, f, arriving, f.both)
	actor := f.claimant
	f.plan.Notifications = append(f.plan.Notifications, domain.Notification{
		UserID: actor, TicketID: f.ticket.ID, Kind: domain.NotificationStateChanged, ActorUserID: &actor, CreatedAt: testClock,
	})

	if _, err := f.s.WorkflowUnitOfWork().ApplyWorkflowPlan(ctx, f.plan); err == nil {
		t.Fatal("apply accepted a notification addressed to its own actor")
	}
	for _, id := range []int64{f.requester, f.both, f.admin} {
		if rows := notificationRowsOf(t, f.s, id); len(rows) != 0 {
			t.Fatalf("recipient %d kept %v after the rollback", id, rows)
		}
	}
	stored, err := f.s.TicketStore().GetByID(ctx, f.ticket.ID, application.TicketQuery{Scope: application.ScopeAll})
	if err != nil {
		t.Fatalf("read ticket: %v", err)
	}
	if stored.State != domain.StateNew || stored.UserID != nil {
		t.Fatalf("ticket = (%s, %v), want the pre-plan (new, unassigned)", stored.State, stored.UserID)
	}
	exec, err := f.s.WorkflowRunStore().GetWorkflowExecution(ctx, f.ticket.ID)
	if err != nil || exec == nil || exec.Run == nil {
		t.Fatalf("read run: %v", err)
	}
	if exec.Run.CurrentStepIndex != 0 || exec.Run.Status != "active" {
		t.Fatalf("run = (%d, %s), want the pre-plan (0, active)", exec.Run.CurrentStepIndex, exec.Run.Status)
	}
}
