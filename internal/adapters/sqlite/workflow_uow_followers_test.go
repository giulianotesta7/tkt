package sqlite

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"
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

// Followers of a workflow CLAIM (issue #418). A claim assigns the ticket to the
// acting agent and moves the run past the claim step in the same transaction, so
// every other member of that desk loses the read scope the follower row was
// admitted on. Eligibility is therefore judged against the assignee, state and
// workflow position the plan leaves behind, with the resulting desk's real
// membership deciding the claim arm.

// claimFollowersFixture seeds a ticket whose pinned workflow waits on a claim of
// desk A followed by the tail the caller names, plus the followers every claim
// assertion uses: a second agent of desk A, an agent of desk A and desk B, an
// agent of no desk, and an administrator. It plans the claim completion and
// returns everything the assertions read.
type claimFollowersFixture struct {
	s         *Store
	ticket    domain.Ticket
	plan      application.WorkflowMutationPlan
	requester int64
	claimant  int64
	mate      int64
	both      int64
	stale     int64
	admin     int64
}

func newClaimFollowersFixture(t *testing.T, tail func(deskB int64) []domain.WorkflowStep) *claimFollowersFixture {
	t.Helper()
	s := newTestDB(t)
	ctx := context.Background()
	f := &claimFollowersFixture{s: s}
	cat := seedCategory(t, s, "Claim with followers")
	f.requester = seedUserRole(t, s, "Rita", "rita-claim@example.com", true, domain.RoleUser)
	f.claimant = seedUserRole(t, s, "Cid", "cid-claim@example.com", true, domain.RoleAgent)
	f.mate = seedUserRole(t, s, "Olga", "olga-claim@example.com", true, domain.RoleAgent)
	f.both = seedUserRole(t, s, "Bea", "bea-claim@example.com", true, domain.RoleAgent)
	f.stale = seedUserRole(t, s, "Sam", "sam-claim@example.com", true, domain.RoleAgent)
	f.admin = seedUserRole(t, s, "Adela", "adela-claim@example.com", true, domain.RoleAdmin)
	deskA := seedDeskWithMemberNamed(t, s, f.claimant, "Claim desk A")
	deskB := seedDeskWithMemberNamed(t, s, f.both, "Claim desk B")
	addDeskMember(t, s, deskA, f.mate)
	addDeskMember(t, s, deskA, f.both)
	def := append([]domain.WorkflowStep{{Type: domain.StepAssignToDesk,
		AssignToDesk: &domain.AssignToDeskStep{DeskID: deskA, Strategy: domain.StrategyClaim}}}, tail(deskB)...)
	versionID := seedPublished(t, s, cat, def)
	f.ticket = seedPinnedTicket(t, s, domain.Ticket{Number: 41, Title: "Claim", CategoryID: cat,
		Priority: domain.PriorityMedium, State: domain.StateNew, RequesterUserID: &f.requester,
		CreatedAt: testClock, UpdatedAt: testClock, WorkflowVersionID: &versionID})
	seedRun(t, s, f.ticket.ID, 0, "active", testClock)
	for _, id := range []int64{f.mate, f.both, f.stale, f.admin} {
		if err := s.TicketFollowerStore().Follow(ctx, f.ticket.ID, id, testClock); err != nil {
			t.Fatalf("follow %d: %v", id, err)
		}
	}
	followers := application.NewFollowService(s.TicketStore(), s.UserStore(), s.TicketFollowerStore(), terminalClock{now: testClock}).
		WithDesks(s.DeskStore())
	plan, err := application.NewWorkflowRunner(terminalClock{now: testClock}).WithFollowers(followers).
		PlanComplete(ctx, application.WorkflowExecutionSnapshot{
			Ticket:    &f.ticket,
			Run:       &application.WorkflowRun{TicketID: f.ticket.ID, CurrentStepIndex: 0, Status: "active", StartedAt: testClock},
			Workflow:  def,
			Requester: &application.NotificationRecipient{UserID: f.requester, Role: domain.RoleUser, Active: true},
		}, application.CompleteWorkflowCommand{
			TicketID: f.ticket.ID, ActorUserID: f.claimant, ActorName: "Cid", ExpectedPosition: 1,
		})
	if err != nil {
		t.Fatalf("plan claim: %v", err)
	}
	f.plan = plan
	return f
}

// allRows lists every stored row of the fixture ticket as "userID|kind|from|to",
// so an assertion covers the whole fan-out instead of one recipient.
func (f *claimFollowersFixture) allRows(t *testing.T) []string {
	t.Helper()
	rows, err := f.s.db.QueryContext(context.Background(),
		`SELECT user_id, kind, COALESCE(from_value,'<nil>'), COALESCE(to_value,'<nil>') FROM notifications WHERE ticket_id = ? ORDER BY user_id, kind, id`, f.ticket.ID)
	if err != nil {
		t.Fatalf("read notification rows: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var userID int64
		var kind, from, to string
		if err := rows.Scan(&userID, &kind, &from, &to); err != nil {
			t.Fatalf("scan row: %v", err)
		}
		out = append(out, strconv.FormatInt(userID, 10)+"|"+kind+"|"+from+"|"+to)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	return out
}

// resultingReadScope proves through the public store the access the resulting
// ticket grants: the agent read scope the detail route applies.
func (f *claimFollowersFixture) resultingReadScope(t *testing.T, userID int64) bool {
	t.Helper()
	_, err := f.s.TicketStore().GetByID(context.Background(), f.ticket.ID,
		application.TicketQuery{Scope: application.ScopeAssignedOrClaimable, ActorID: userID})
	if err == nil {
		return true
	}
	if errors.Is(err, domain.ErrNotFound) {
		return false
	}
	t.Fatalf("read resulting ticket for %d: %v", userID, err)
	return false
}

// assertRows compares the ticket's whole stored notification set with want.
func (f *claimFollowersFixture) assertRows(t *testing.T, want ...string) {
	t.Helper()
	got := f.allRows(t)
	sort.Strings(want)
	sort.Strings(got)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("stored rows = %v, want %v", got, want)
	}
}

// row renders one expected stored row.
func (f *claimFollowersFixture) row(userID int64, kind, from, to string) string {
	return strconv.FormatInt(userID, 10) + "|" + kind + "|" + from + "|" + to
}

// assertFollowerRowsKept proves every arranged subscription survived the plan.
func (f *claimFollowersFixture) assertFollowerRowsKept(t *testing.T) {
	t.Helper()
	for _, id := range []int64{f.mate, f.both, f.stale, f.admin} {
		if got := followerRowCount(t, f.s, f.ticket.ID, id); got != 1 {
			t.Errorf("follower rows for %d = %d, want the subscription kept", id, got)
		}
	}
}

// TestApplyWorkflowPlanRefusesTheFollowerTheClaimLeavesBehind: the original leak.
// Two agents of the claim desk follow the pending claim, one of them claims it,
// and the run advances to a form. The other agent loses the ticket to that same
// transaction, so they get no planned and no persisted row — state or assignment
// — while the administrator who also follows keeps the state change, the claimant
// hears nothing, and the ticket, cursor, assignee and follower rows persist.
func TestApplyWorkflowPlanRefusesTheFollowerTheClaimLeavesBehind(t *testing.T) {
	f := newClaimFollowersFixture(t, func(int64) []domain.WorkflowStep {
		return []domain.WorkflowStep{{Type: domain.StepForm, Form: &domain.FormStep{
			Actor: domain.FormActorAssignee, Fields: []domain.FormField{{Key: "k", Label: "L", Kind: domain.FieldShortText}}}}}
	})
	ctx := context.Background()
	if !f.resultingReadScope(t, f.mate) || !f.resultingReadScope(t, f.claimant) {
		t.Fatal("fixture: the claim desk's agents must read the claim-pending ticket before the claim")
	}
	if _, err := newWorkflowUnitOfWork(f.s.db).ApplyWorkflowPlan(ctx, f.plan); err != nil {
		t.Fatalf("apply plan: %v", err)
	}

	stored, err := f.s.TicketStore().GetByID(ctx, f.ticket.ID, application.TicketQuery{Scope: application.ScopeAll})
	if err != nil {
		t.Fatalf("read ticket: %v", err)
	}
	if stored.UserID == nil || *stored.UserID != f.claimant {
		t.Fatalf("assignee = %v, want the claimant %d", stored.UserID, f.claimant)
	}
	if stored.State != domain.StateInProgress {
		t.Fatalf("state = %s, want in_progress", stored.State)
	}
	exec, err := f.s.WorkflowRunStore().GetWorkflowExecution(ctx, f.ticket.ID)
	if err != nil || exec == nil || exec.Run == nil {
		t.Fatalf("read run: %v", err)
	}
	if exec.Run.CurrentStepIndex != 1 || exec.Run.Status != "active" {
		t.Fatalf("run = (%d, %s), want the pending form at (1, active)", exec.Run.CurrentStepIndex, exec.Run.Status)
	}
	if f.resultingReadScope(t, f.mate) {
		t.Fatal("the other claim-desk agent still reads the ticket after the claim")
	}

	f.assertRows(t,
		f.row(f.requester, "state_changed", "new", "in_progress"),
		f.row(f.admin, "state_changed", "new", "in_progress"),
	)
	if rows := notificationRowsOf(t, f.s, f.mate); len(rows) != 0 {
		t.Fatalf("the follower the claim leaves behind got %v, want nothing", rows)
	}
	if rows := notificationRowsOf(t, f.s, f.stale); len(rows) != 0 {
		t.Fatalf("the follower with no desk got %v, want nothing", rows)
	}
	if rows := notificationRowsOf(t, f.s, f.claimant); len(rows) != 0 {
		t.Fatalf("the claimant got %v, want nothing", rows)
	}
	f.assertFollowerRowsKept(t)
}

// TestApplyWorkflowPlanKeepsTheFollowerOfTheNextClaimDesk: the claim moves the run
// to a claim on desk B, so the follower of BOTH desks keeps the resulting read
// scope and a legitimate notification, while the follower of the departing desk
// alone and the follower with no desk at all are refused in the same transaction
// despite their stored rows. Membership of the RESULTING desk decides it, not the
// identity of the departing and arriving desks.
func TestApplyWorkflowPlanKeepsTheFollowerOfTheNextClaimDesk(t *testing.T) {
	f := newClaimFollowersFixture(t, func(deskB int64) []domain.WorkflowStep {
		return []domain.WorkflowStep{{Type: domain.StepAssignToDesk,
			AssignToDesk: &domain.AssignToDeskStep{DeskID: deskB, Strategy: domain.StrategyClaim}}}
	})
	ctx := context.Background()
	if _, err := newWorkflowUnitOfWork(f.s.db).ApplyWorkflowPlan(ctx, f.plan); err != nil {
		t.Fatalf("apply plan: %v", err)
	}

	exec, err := f.s.WorkflowRunStore().GetWorkflowExecution(ctx, f.ticket.ID)
	if err != nil || exec == nil || exec.Run == nil {
		t.Fatalf("read run: %v", err)
	}
	if exec.Run.CurrentStepIndex != 1 || exec.Run.Status != "active" {
		t.Fatalf("run = (%d, %s), want the pending claim at (1, active)", exec.Run.CurrentStepIndex, exec.Run.Status)
	}
	if !f.resultingReadScope(t, f.both) {
		t.Fatal("the follower of both claim desks lost the resulting read scope")
	}
	if f.resultingReadScope(t, f.mate) || f.resultingReadScope(t, f.stale) {
		t.Fatal("a follower without the resulting desk still reads the ticket")
	}

	f.assertRows(t,
		f.row(f.requester, "state_changed", "new", "in_progress"),
		f.row(f.both, "state_changed", "new", "in_progress"),
		f.row(f.admin, "state_changed", "new", "in_progress"),
	)
	if rows := notificationRowsOf(t, f.s, f.mate); len(rows) != 0 {
		t.Fatalf("the departing-desk follower got %v, want nothing", rows)
	}
	if rows := notificationRowsOf(t, f.s, f.stale); len(rows) != 0 {
		t.Fatalf("the follower with no desk got %v, want nothing", rows)
	}
	f.assertFollowerRowsKept(t)
}
