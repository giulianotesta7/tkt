package sqlite

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// PR6 — Assignment atomicity + deterministic least_loaded + claim scope.
//
// Task 6.1 core. These REAL SQLite tests prove (design S6):
//   - pending claim leaves the ticket `new` with zero writes;
//   - a successful claim/least_loaded assignment persists the person and, when
//     the ticket is new, the new->in_progress transition + ordered audits
//     atomically (an already in_progress ticket gets no redundant transition);
//     same-person assignment emits no false user audit;
//   - A→B reassignment without a valid reason is rejected with total rollback;
//   - least_loaded selection is deterministic from ALL active agent|admin|root
//     desk members, global new|in_progress load only, COUNT ASC then id ASC, no
//     category predicate, not actor-only, resolved INSIDE the same immediate
//     transaction so assignment+state+audits+cursor are one atomic unit; an
//     empty desk pool rolls the whole submit back with no partial writes;
//   - two stale claimers: the first commit advances the cursor, the second gets
//     ErrWorkflowPositionConflict with zero writes.

func seedUserRole(t *testing.T, s *Store, name, email string, active bool, role domain.Role) int64 {
	t.Helper()
	id := seedUser(t, s, name, email, active)
	if _, err := s.db.ExecContext(context.Background(), `UPDATE users SET role=? WHERE id=?`, string(role), id); err != nil {
		t.Fatalf("set role %s on %d: %v", role, id, err)
	}
	return id
}

// seedLoadTicket inserts a ticket assigned to userID in the given state and
// category. It counts toward the candidate's global new|in_progress load.
func seedLoadTicket(t *testing.T, s *Store, number int, userID, catID int64, state domain.State) int64 {
	t.Helper()
	tk := seedTicket(t, s, domain.Ticket{Number: number, Title: "load", CategoryID: catID, Priority: domain.PriorityMedium, State: state, UserID: &userID, CreatedAt: testClock, UpdatedAt: testClock})
	return tk.ID
}

func leastLoadedDef(deskID int64) domain.WorkflowDefinition {
	return domain.WorkflowDefinition{{Type: domain.StepAssignToDesk, AssignToDesk: &domain.AssignToDeskStep{DeskID: deskID, Strategy: domain.StrategyLeastLoaded}}}
}

// leastLoadedCreateOps is the runner-planned create-time operation sequence for
// a single least_loaded step whose ticket starts new: [least_loaded selection,
// new->in_progress transition]. The transition audit keeps the create zero
// ticket-id placeholder.
func leastLoadedCreateOps(deskID int64, now time.Time) []application.WorkflowOperation {
	field := "state"
	tr := domain.AuditEvent{TicketID: 0, Actor: "workflow", Action: domain.ActionTransition, Field: &field, FromValue: ptr("new"), ToValue: ptr("in_progress"), CreatedAt: now}
	return []application.WorkflowOperation{
		application.LeastLoadedAssignmentOperation{StepIndex: 0, DeskID: deskID},
		application.TransitionOperation{StepIndex: 0, Audit: tr},
	}
}

// TestWorkflowUoW_LeastLoaded_SingleCandidateAtomic proves one active agent desk
// member is selected, the person + new->in_progress + ordered audits + completed
// run persist as ONE atomic create (no partial rows on any failure).
func TestWorkflowUoW_LeastLoaded_SingleCandidateAtomic(t *testing.T) {
	s := newTestDB(t)
	cat := seedCategory(t, s, "C1")
	req := seedUserRole(t, s, "Req", "r@x", true, domain.RoleUser)
	agent := seedUserRole(t, s, "Ag", "a@x", true, domain.RoleAgent)
	deskID := seedDeskWithMember(t, s, agent)
	def := leastLoadedDef(deskID)
	vid := seedPublished(t, s, cat, def)
	now := testClock
	in := buildCreateInput(cat, vid, req, def, leastLoadedCreateOps(deskID, now), 1, "completed", domain.StateInProgress, &now)

	tk, err := newWorkflowUnitOfWork(s.db).CreateTicketWithRun(context.Background(), in)
	if err != nil {
		t.Fatalf("least_loaded create must resolve, got %v", err)
	}
	state, assignee, _ := ticketRow(t, s, tk.ID)
	if state != "in_progress" {
		t.Errorf("ticket state = %s want in_progress", state)
	}
	if assignee == nil || *assignee != agent {
		t.Errorf("assignee = %v want %d", assignee, agent)
	}
	cur, status, comp := runRow(t, s, tk.ID)
	if cur != 1 || status != "completed" || comp == nil {
		t.Errorf("run = cur %d status %s completed %v, want 1/completed/non-nil", cur, status, comp)
	}
	want := []string{"created||", "workflow_assignment||" + strconv.FormatInt(agent, 10), "transition|new|in_progress"}
	if got := auditActionOrder(t, s, tk.ID); strings.Join(got, ";") != strings.Join(want, ";") {
		t.Fatalf("audit order = %v want %v", got, want)
	}
	// The contextual assignment row is a workflow actor with NULL user id and the
	// pinned desk context.
	var actor string
	var actorID *int64
	var fromV, toV string
	var desk *int64
	if err := s.db.QueryRow(`SELECT actor, actor_user_id, COALESCE(from_value,''), COALESCE(to_value,''), desk_id FROM audit_events WHERE ticket_id=? AND action='workflow_assignment'`, tk.ID).Scan(&actor, &actorID, &fromV, &toV, &desk); err != nil {
		t.Fatalf("read assignment audit: %v", err)
	}
	if actor != "workflow" || actorID != nil || fromV != "" || toV != strconv.FormatInt(agent, 10) {
		t.Fatalf("least_loaded assignment audit = actor %q id %v from %q to %q", actor, actorID, fromV, toV)
	}
	if desk == nil || *desk != deskID {
		t.Fatalf("least_loaded assignment audit desk = %v, want %d", desk, deskID)
	}
}

// TestWorkflowUoW_LeastLoaded_GlobalLoadAndTieBreaker proves the deterministic
// pool: ALL active agent|admin|root desk members, global new|in_progress load
// across every category (no category predicate), COUNT ASC then id ASC tie-break,
// and it excludes an inactive member and a memberless agent. It is not actor-only:
// the requester is not a desk member.
func TestWorkflowUoW_LeastLoaded_GlobalLoadAndTieBreaker(t *testing.T) {
	s := newTestDB(t)
	catA := seedCategory(t, s, "CatA")
	catB := seedCategory(t, s, "CatB") // other category: load still counts (no category predicate)
	req := seedUserRole(t, s, "Req", "r@x", true, domain.RoleUser)

	memberless := seedUserRole(t, s, "Free", "free@x", true, domain.RoleAgent) // agent, NOT a member: excluded even with the lowest id + zero load
	lowID := seedUserRole(t, s, "Low", "low@x", true, domain.RoleAgent)        // lower member id
	highID := seedUserRole(t, s, "High", "high@x", true, domain.RoleAdmin)     // higher id, admin
	admin := seedUserRole(t, s, "Adm", "adm@x", true, domain.RoleRoot)         // root member
	inactive := seedUserRole(t, s, "Inac", "in@x", false, domain.RoleAgent)    // inactive member: excluded

	deskID := seedDeskWithMember(t, s, lowID)
	addMemberRaw(t, s, deskID, highID)
	addMemberRaw(t, s, deskID, admin)
	addMemberRaw(t, s, deskID, inactive)

	// Global load tied between lowID(1 new) and highID(1 in_progress) and admin(1):
	// tie-break must pick the LOWEST user id. Place lowID's load in the OTHER
	// category to prove no category predicate.
	seedLoadTicket(t, s, 1, highID, catA, domain.StateInProgress)
	seedLoadTicket(t, s, 2, lowID, catB, domain.StateNew)
	seedLoadTicket(t, s, 3, admin, catA, domain.StateInProgress)
	// Resolved/closed/cancelled load on admin must NOT count, so admin's count
	// stays 1 and does not win by count (it would if resolved counted).
	seedLoadTicket(t, s, 4, admin, catA, domain.StateResolved)
	seedLoadTicket(t, s, 5, admin, catA, domain.StateClosed)
	seedLoadTicket(t, s, 6, admin, catA, domain.StateCancelled)

	def := leastLoadedDef(deskID)
	vid := seedPublished(t, s, catA, def)
	now := testClock
	in := buildCreateInput(catA, vid, req, def, leastLoadedCreateOps(deskID, now), 1, "completed", domain.StateInProgress, &now)

	tk, err := newWorkflowUnitOfWork(s.db).CreateTicketWithRun(context.Background(), in)
	if err != nil {
		t.Fatalf("least_loaded create: %v", err)
	}
	_, assignee, _ := ticketRow(t, s, tk.ID)
	// memberless has the LOWEST id (created first) and zero load, so it would win
	// every tie if it were in the candidate pool — its exclusion proves the pool
	// comes only from desk_members. Among the tied members (count=1 each) the
	// lowest member id (lowID) must win; inactive is excluded.
	if assignee == nil || *assignee == memberless || *assignee != lowID {
		t.Fatalf("least_loaded selected %v, want %d (lowest-desired member id; memberless+inactive excluded)", assignee, lowID)
	}
}

// TestWorkflowUoW_LeastLoaded_TieBreakByID proves equal global load selects the
// lowest user id (ORDER BY COUNT ASC, u.id ASC), even when the higher id is a
// more senior role.
func TestWorkflowUoW_LeastLoaded_TieBreakByID(t *testing.T) {
	s := newTestDB(t)
	cat := seedCategory(t, s, "C1")
	req := seedUserRole(t, s, "Req", "r@x", true, domain.RoleUser)
	a := seedUserRole(t, s, "A", "a@x", true, domain.RoleAdmin)
	b := seedUserRole(t, s, "B", "b@x", true, domain.RoleRoot) // higher id, root
	deskID := seedDeskWithMember(t, s, b)
	addMemberRaw(t, s, deskID, a)
	def := leastLoadedDef(deskID)
	vid := seedPublished(t, s, cat, def)
	// Equal zero load: lowest id (a) must win.
	now := testClock
	in := buildCreateInput(cat, vid, req, def, leastLoadedCreateOps(deskID, now), 1, "completed", domain.StateInProgress, &now)
	tk, err := newWorkflowUnitOfWork(s.db).CreateTicketWithRun(context.Background(), in)
	if err != nil {
		t.Fatalf("least_loaded create: %v", err)
	}
	_, assignee, _ := ticketRow(t, s, tk.ID)
	if assignee == nil || *assignee != a {
		t.Fatalf("tie broke to %v, want %d (lowest id)", assignee, a)
	}
}

// TestWorkflowUoW_LeastLoaded_EmptyDeskRollsBack proves the empty-pool total
// rollback: zero ticket/run/audit rows after an unresolvable selection.
func TestWorkflowUoW_LeastLoaded_EmptyDeskRollsBack(t *testing.T) {
	s := newTestDB(t)
	cat := seedCategory(t, s, "C1")
	req := seedUserRole(t, s, "Req", "r@x", true, domain.RoleUser)
	_, err := s.db.ExecContext(context.Background(), `INSERT INTO desks (name, created_at) VALUES ('Empty', ?)`, formatTime(testClock))
	if err != nil {
		t.Fatalf("insert empty desk: %v", err)
	}
	// desk id 1 with NO members.
	def := leastLoadedDef(1)
	vid := seedPublished(t, s, cat, def)
	now := testClock
	in := buildCreateInput(cat, vid, req, def, leastLoadedCreateOps(1, now), 1, "completed", domain.StateInProgress, &now)
	_, err = newWorkflowUnitOfWork(s.db).CreateTicketWithRun(context.Background(), in)
	if !errors.Is(err, ErrLeastLoadedUnresolved) {
		t.Fatalf("empty desk must fail with ErrLeastLoadedUnresolved, got %v", err)
	}
	assertTotalRollback(t, s)
}

// TestWorkflowUoW_LeastLoaded_EmptyDeskApplyRollsBackAndRetry proves the APPLY-path
// empty-desk contract (design S6, task 6.2): submitting a least_loaded plan for a
// desk with no eligible members fails with the typed ErrLeastLoadedUnresolved and
// the whole plan rolls back (run cursor/status, ticket state/assignee unchanged,
// zero audits/answers); then the SAME immutable plan succeeds exactly once after a
// member joins the desk — proving the connection/transaction stays reusable after
// the rolled-back apply and the shared query path is deterministic on retry.
func TestWorkflowUoW_LeastLoaded_EmptyDeskApplyRollsBackAndRetry(t *testing.T) {
	s := newTestDB(t)
	cat := seedCategory(t, s, "C1")
	req := seedUserRole(t, s, "Req", "r@x", true, domain.RoleUser)
	if _, err := s.db.ExecContext(context.Background(), `INSERT INTO desks (name, created_at) VALUES ('Empty', ?)`, formatTime(testClock)); err != nil {
		t.Fatalf("insert empty desk: %v", err)
	}
	// desk id 1 with NO members.
	def := leastLoadedDef(1)
	vid := seedPublished(t, s, cat, def)
	now := testClock
	tk := seedPinnedTicket(t, s, domain.Ticket{Number: 9, Title: "T", Description: "", RequesterName: "Req", RequesterEmail: "r@x", RequesterUserID: &req, CategoryID: cat, Priority: domain.PriorityMedium, State: domain.StateNew, CreatedAt: now, UpdatedAt: now, WorkflowVersionID: &vid})
	seedRun(t, s, tk.ID, 0, "active", now)
	ops := leastLoadedApplyOps(tk, 1, now)
	plan := buildApplyPlan(tk, vid, def, 0, "", ops, 1, "active", domain.StateInProgress, nil, nil)

	_, err := newWorkflowUnitOfWork(s.db).ApplyWorkflowPlan(context.Background(), plan)
	if !errors.Is(err, ErrLeastLoadedUnresolved) {
		t.Fatalf("empty desk apply must fail with ErrLeastLoadedUnresolved, got %v", err)
	}
	assertApplyNoWrites(t, s, tk)

	// A member joins the previously-empty desk; the SAME immutable plan now resolves
	// deterministically and succeeds exactly once.
	agent := seedUserRole(t, s, "Ag", "a@x", true, domain.RoleAgent)
	if _, err := s.db.ExecContext(context.Background(), `INSERT INTO desk_members (desk_id, user_id, created_at) VALUES (1, ?, ?)`, agent, formatTime(now)); err != nil {
		t.Fatalf("join member: %v", err)
	}
	if _, err := newWorkflowUnitOfWork(s.db).ApplyWorkflowPlan(context.Background(), plan); err != nil {
		t.Fatalf("same plan must succeed after a member joins the desk: %v", err)
	}
	state, assignee, _ := ticketRow(t, s, tk.ID)
	if state != "in_progress" {
		t.Errorf("state = %s want in_progress", state)
	}
	if assignee == nil || *assignee != agent {
		t.Errorf("assignee = %v want %d (least_loaded selects the only member)", assignee, agent)
	}
	if cur, status, _ := runRow(t, s, tk.ID); cur != 1 || status != "active" {
		t.Fatalf("run cursor/status = %d/%s want 1/active", cur, status)
	}
}

// TestWorkflowUoW_ClaimRace_CursorCASAtLeastOneCommit proves two concurrent
// claimers rendering the same action: exactly one commit advances the cursor;
// the other fails with ErrWorkflowPositionConflict and writes nothing.
func TestWorkflowUoW_ClaimRace_CursorCASAtLeastOneCommit(t *testing.T) {
	s := newTestDB(t)
	cat := seedCategory(t, s, "C1")
	req := seedUser(t, s, "Req", "r@x", true)
	agentA := seedUser(t, s, "AgA", "a@x", true)
	agentB := seedUser(t, s, "AgB", "b@x", true)
	deskID := seedDeskWithMember(t, s, agentA)
	addMemberRaw(t, s, deskID, agentB)
	def := claimDef(deskID)
	vid := seedPublished(t, s, cat, def)
	now := testClock
	tk := seedPinnedTicket(t, s, domain.Ticket{Number: 6, Title: "T", Description: "", RequesterName: "Req", RequesterEmail: "r@x", RequesterUserID: &req, CategoryID: cat, Priority: domain.PriorityMedium, State: domain.StateNew, CreatedAt: now, UpdatedAt: now, WorkflowVersionID: &vid})
	seedRun(t, s, tk.ID, 0, "active", now)
	ctx := context.Background()

	planA := buildApplyPlan(tk, vid, def, agentA, "Ag", applyClaimOps(t, now, tk, agentA, deskID), 1, "active", domain.StateInProgress, &agentA, nil)
	planB := buildApplyPlan(tk, vid, def, agentB, "Ag", applyClaimOps(t, now, tk, agentB, deskID), 1, "active", domain.StateInProgress, &agentB, nil)

	var wg sync.WaitGroup
	errCh := make(chan error, 2)
	uow := newWorkflowUnitOfWork(s.db)
	wg.Add(2)
	go func() { defer wg.Done(); _, e := uow.ApplyWorkflowPlan(ctx, planA); errCh <- e }()
	go func() { defer wg.Done(); _, e := uow.ApplyWorkflowPlan(ctx, planB); errCh <- e }()
	wg.Wait()
	close(errCh)

	var conflicts, successes int
	for e := range errCh {
		var wpc *domain.WorkflowPositionConflictError
		if errors.As(e, &wpc) {
			conflicts++
		} else if e == nil {
			successes++
		} else {
			t.Fatalf("unexpected apply error: %v", e)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes=%d conflicts=%d, want 1/1 (exactly one winner, one 422)", successes, conflicts)
	}
	// Exactly ONE commit advanced the cursor to 1; the other wrote nothing.
	cur, status, _ := runRow(t, s, tk.ID)
	if cur != 1 || status != "active" {
		t.Fatalf("run = cur %d status %s, want 1/active", cur, status)
	}
	// The winner's contextual assignment + new->in_progress audits exist once.
	state, assignee, _ := ticketRow(t, s, tk.ID)
	if state != "in_progress" || assignee == nil {
		t.Fatalf("state=%s assignee=%v want in_progress/person", state, assignee)
	}
	if *assignee != agentA && *assignee != agentB {
		t.Fatalf("assignee=%d want one of the claimers", *assignee)
	}
	audits := auditActionOrder(t, s, tk.ID)
	if len(audits) != 2 {
		t.Fatalf("audits=%v want exactly one claim's 2 audits", audits)
	}
}

// TestWorkflowUoW_PendingClaim_NoWrites proves a create whose FIRST human step
// is a claim stays `new`, unassigned, active at cursor 0 with zero assignment/
// state/audit writes beyond the created audit.
func TestWorkflowUoW_PendingClaim_NoWrites(t *testing.T) {
	s := newTestDB(t)
	cat := seedCategory(t, s, "C1")
	req := seedUser(t, s, "Req", "r@x", true)
	agent := seedUser(t, s, "Ag", "a@x", true)
	deskID := seedDeskWithMember(t, s, agent)
	def := claimDef(deskID)
	vid := seedPublished(t, s, cat, def)
	in := buildCreateInput(cat, vid, req, def, nil, 0, "active", domain.StateNew, nil)
	tk, err := newWorkflowUnitOfWork(s.db).CreateTicketWithRun(context.Background(), in)
	if err != nil {
		t.Fatalf("create pending claim: %v", err)
	}
	state, assignee, _ := ticketRow(t, s, tk.ID)
	if state != "new" || assignee != nil {
		t.Fatalf("pending claim state=%s assignee=%v, want new/<nil>", state, assignee)
	}
	cur, status, _ := runRow(t, s, tk.ID)
	if cur != 0 || status != "active" {
		t.Fatalf("pending claim run=cur %d status %s want 0/active", cur, status)
	}
	want := []string{"created||"}
	if got := auditActionOrder(t, s, tk.ID); strings.Join(got, ";") != strings.Join(want, ";") {
		t.Fatalf("pending claim audits = %v want %v (no assignment/state/cursor write)", got, want)
	}
}

// addMemberRaw inserts a desk membership directly (arrange), skipping the applied
// user-role guard only insofar as the test user is always agent+.
func addMemberRaw(t *testing.T, s *Store, deskID, userID int64) {
	t.Helper()
	if _, err := s.db.ExecContext(context.Background(), `INSERT INTO desk_members (desk_id, user_id, created_at) VALUES (?, ?, ?)`, deskID, userID, formatTime(testClock)); err != nil {
		t.Fatalf("add member %d to desk %d: %v", userID, deskID, err)
	}
}

// TestScopeAssignedOrClaimableRead proves the read-scope clause: a ticket is
// readable when assigned to the actor OR when an active run's pinned immutable
// step at the current cursor is an assign_to_desk[claim] step whose desk
// contains the actor (json_extract on the pinned workflow version). A claim
// visibility must NOT widen the read for a non-member, and a non-assigned
// non-claimable ticket stays hidden.
func TestScopeAssignedOrClaimableRead(t *testing.T) {
	s := newTestDB(t)
	cat := seedCategory(t, s, "C1")
	req := seedUser(t, s, "Req", "r@x", true)
	member := seedUser(t, s, "Mbr", "m@x", true)
	other := seedUser(t, s, "Oth", "o@x", true)
	deskID := seedDeskWithMember(t, s, member)
	def := claimDef(deskID)
	vid := seedPublished(t, s, cat, def)
	now := testClock
	tk := seedPinnedTicket(t, s, domain.Ticket{Number: 6, Title: "T", Description: "", RequesterName: "Req", RequesterEmail: "r@x", RequesterUserID: &req, CategoryID: cat, Priority: domain.PriorityMedium, State: domain.StateNew, CreatedAt: now, UpdatedAt: now, WorkflowVersionID: &vid})
	seedRun(t, s, tk.ID, 0, "active", now)
	ctx := context.Background()
	store := s.TicketStore()

	// Claimable: member (not assigned, but a desk member at the pending claim step) reads it.
	if _, err := store.GetByID(ctx, tk.ID, application.TicketQuery{Scope: application.ScopeAssignedOrClaimable, ActorID: member}); err != nil {
		t.Errorf("claimable member must read claim-pending ticket, got %v", err)
	}
	// Assigned reads it too.
	if _, err := store.GetByID(ctx, tk.ID, application.TicketQuery{Scope: application.ScopeAssignedOrClaimable, ActorID: member}); err != nil {
		t.Errorf("member read: %v", err)
	}
	// A non-member agent does NOT read it via claimable scope (no desk membership).
	if _, err := store.GetByID(ctx, tk.ID, application.TicketQuery{Scope: application.ScopeAssignedOrClaimable, ActorID: other}); err == nil {
		t.Errorf("non-member must not read claim-pending ticket via claimable scope")
	}
	// STRICT mutation scope: claim visibility does NOT grant ScopeAssigned — the
	// member is not assigned, so ScopeAssigned must deny (proof that the mutation
	// path cannot edit/comment/transition merely because it could claim).
	if _, err := store.GetByID(ctx, tk.ID, application.TicketQuery{Scope: application.ScopeAssigned, ActorID: member}); err == nil {
		t.Errorf("claimable member must NOT read under strict ScopeAssigned (no edit/transition grant)")
	}
}

// ---- PR6 6.1 narrow correction proofs ----
//
// These tests prove the contextual assignment contract and the claim/scope
// guarantees that close out task 6.1:
//   1. same-person least_loaded: exactly one structured workflow_assignment row
//      (from==to, no fake reason); a new ticket still transitions, an in_progress
//      ticket does not;
//   2. same-person claim: same single-contextual-row rule;
//   3. in_progress A->B claim assignment: the contextual row only, no state audit;
//   4. A->B claim without a valid reason: typed conflict + total rollback;
//   5. a planned claimant that becomes inactive / role-below-agent / loses desk
//      membership before apply: typed conflict + total rollback, and the same
//      immutable plan succeeds only when every precondition is restored;
//   6. the read scope genuinely exercises the assigned OR branch, keeps a
//      NULL-pinned assigned ticket readable, and uses ScopeAssignedOrClaimable for
//      GetByID/List while the mutation scope stays strict ScopeAssigned.

// leastLoadedApplyOps is the apply-path operation group for a single least_loaded
// step (step 0) pinned at cursor 0: [LeastLoaded, (new->in_progress Transition if
// the ticket is still new)]. The transition audit targets the ticket id.
func leastLoadedApplyOps(tk domain.Ticket, deskID int64, now time.Time) []application.WorkflowOperation {
	ops := []application.WorkflowOperation{
		application.LeastLoadedAssignmentOperation{StepIndex: 0, DeskID: deskID},
	}
	if tk.State == domain.StateNew {
		field := "state"
		tr := domain.AuditEvent{TicketID: tk.ID, Actor: "workflow", Action: domain.ActionTransition, Field: &field, FromValue: ptr("new"), ToValue: ptr("in_progress"), CreatedAt: now}
		ops = append(ops, application.TransitionOperation{StepIndex: 0, Audit: tr})
	}
	return ops
}

// samePersonClaimOps is the apply-path group for a claim whose claimant is already
// the ticket assignee. The runner ALWAYS preserves an explicit ClaimAssignmentOperation
// as the authorization fact (even though it applies as a user-field no-op on the
// copy), leaving [ClaimAssignment, new->in_progress Transition] when the ticket is
// new and [ClaimAssignment] when it is in_progress. The contextual same-person row
// carries exact from==to facts and NO reason (no fake reassignment reason).
func samePersonClaimOps(tk domain.Ticket, deskID, agent int64, now time.Time) []application.WorkflowOperation {
	field := "user"
	f := strconv.FormatInt(agent, 10)
	claimAudit := domain.AuditEvent{TicketID: tk.ID, Actor: "Ag", ActorUserID: &agent, Action: domain.ActionWorkflowAssignment, Field: &field, FromValue: &f, ToValue: &f, DeskID: &deskID, CreatedAt: now}
	ops := []application.WorkflowOperation{
		application.ClaimAssignmentOperation{StepIndex: 0, DeskID: deskID, AssigneeUserID: agent, AssignmentAudit: claimAudit},
	}
	if tk.State == domain.StateNew {
		sfield := "state"
		tr := domain.AuditEvent{TicketID: tk.ID, Actor: "workflow", Action: domain.ActionTransition, Field: &sfield, FromValue: ptr("new"), ToValue: ptr("in_progress"), CreatedAt: now}
		ops = append(ops, application.TransitionOperation{StepIndex: 0, Audit: tr})
	}
	return ops
}

// reassignClaimOps builds the reasonless apply-path group for an in-progress
// A->B pinned workflow claim.
func reassignClaimOps(tk domain.Ticket, deskID int64, from, to int64, now time.Time) []application.WorkflowOperation {
	field := "user"
	f := strconv.FormatInt(from, 10)
	toStr := strconv.FormatInt(to, 10)
	audit := domain.AuditEvent{TicketID: tk.ID, Actor: "AgB", ActorUserID: &to, Action: domain.ActionWorkflowAssignment, Field: &field, FromValue: &f, ToValue: &toStr, DeskID: &deskID, CreatedAt: now}
	return []application.WorkflowOperation{
		application.ClaimAssignmentOperation{StepIndex: 0, DeskID: deskID, AssigneeUserID: to, AssignmentAudit: audit},
	}
}

// seedClaimPending seeds a new, unassigned ticket pinned to a claim definition
// with an active run at cursor 0, plus the full claim-operation group for claimant
// b. Returns the ticket, version id, definition and operations for building a plan.
func seedClaimPending(t *testing.T, s *Store, b int64, deskID int64) (domain.Ticket, int64, domain.WorkflowDefinition, []application.WorkflowOperation) {
	t.Helper()
	cat := seedCategory(t, s, "C1")
	req := seedUserRole(t, s, "Req", "r@x", true, domain.RoleUser)
	def := claimDef(deskID)
	vid := seedPublished(t, s, cat, def)
	now := testClock
	tk := seedPinnedTicket(t, s, domain.Ticket{Number: 9, Title: "T", Description: "", RequesterName: "Req", RequesterEmail: "r@x", RequesterUserID: &req, CategoryID: cat, Priority: domain.PriorityMedium, State: domain.StateNew, CreatedAt: now, UpdatedAt: now, WorkflowVersionID: &vid})
	seedRun(t, s, tk.ID, 0, "active", now)
	ops := applyClaimOps(t, now, tk, b, deskID)
	return tk, vid, def, ops
}

// TestWorkflowUoW_LeastLoaded_SamePersonContextualRow proves the approved
// contextual contract: EVERY least_loaded completion writes EXACTLY ONE structured
// workflow_assignment row — including a same-person selection (from==to, no fake
// reason) — while the user field itself stays unchanged. A new ticket still carries
// its new->in_progress transition; an in_progress ticket gets no state change.
func TestWorkflowUoW_LeastLoaded_SamePersonContextualRow(t *testing.T) {
	cases := []struct {
		name         string
		state        domain.State
		wantTrailing []string // expected audit order AFTER the leading contextual row
	}{
		{"new ticket still transitions", domain.StateNew, []string{"transition|new|in_progress"}},
		{"in_progress does not transition", domain.StateInProgress, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestDB(t)
			cat := seedCategory(t, s, "C1")
			req := seedUserRole(t, s, "Req", "r@x", true, domain.RoleUser)
			agent := seedUserRole(t, s, "Ag", "a@x", true, domain.RoleAgent)
			deskID := seedDeskWithMember(t, s, agent)
			def := leastLoadedDef(deskID)
			vid := seedPublished(t, s, cat, def)
			now := testClock
			tk := seedPinnedTicket(t, s, domain.Ticket{Number: 6, Title: "T", Description: "", RequesterName: "Req", RequesterEmail: "r@x", RequesterUserID: &req, CategoryID: cat, Priority: domain.PriorityMedium, State: tc.state, UserID: &agent, CreatedAt: now, UpdatedAt: now, WorkflowVersionID: &vid})
			seedRun(t, s, tk.ID, 0, "active", now)
			nextState := domain.StateInProgress
			plan := buildApplyPlan(tk, vid, def, agent, "Ag", leastLoadedApplyOps(tk, deskID, now), 1, "active", nextState, nil, nil)
			if _, err := newWorkflowUnitOfWork(s.db).ApplyWorkflowPlan(context.Background(), plan); err != nil {
				t.Fatalf("same-person least_loaded apply: %v", err)
			}
			state, assignee, _ := ticketRow(t, s, tk.ID)
			if state != "in_progress" {
				t.Errorf("state = %s want in_progress", state)
			}
			if assignee == nil || *assignee != agent {
				t.Errorf("assignee = %v want %d (unchanged)", assignee, agent)
			}
			// Exactly one contextual row (from==to for the same-person selection), then
			// the transition only when the ticket was new.
			row := "workflow_assignment|" + strconv.FormatInt(agent, 10) + "|" + strconv.FormatInt(agent, 10)
			want := append([]string{row}, tc.wantTrailing...)
			if got := auditActionOrder(t, s, tk.ID); strings.Join(got, ";") != strings.Join(want, ";") {
				t.Fatalf("audits = %v want %v (exactly one contextual row, user field unchanged)", got, want)
			}
		})
	}
}

// TestWorkflowUoW_Claim_SamePersonContextualRow proves the approved contextual
// contract for claims: a claim whose claimant is already the assignee applies as a
// user-field no-op on the copy while writing EXACTLY ONE structured
// workflow_assignment row (from==to, no fake reason), plus the new->in_progress
// transition on a new ticket.
func TestWorkflowUoW_Claim_SamePersonContextualRow(t *testing.T) {
	s := newTestDB(t)
	cat := seedCategory(t, s, "C1")
	req := seedUserRole(t, s, "Req", "r@x", true, domain.RoleUser)
	agent := seedUserRole(t, s, "Ag", "a@x", true, domain.RoleAgent)
	deskID := seedDeskWithMember(t, s, agent)
	def := claimDef(deskID)
	vid := seedPublished(t, s, cat, def)
	now := testClock
	tk := seedPinnedTicket(t, s, domain.Ticket{Number: 6, Title: "T", Description: "", RequesterName: "Req", RequesterEmail: "r@x", RequesterUserID: &req, CategoryID: cat, Priority: domain.PriorityMedium, State: domain.StateNew, UserID: &agent, CreatedAt: now, UpdatedAt: now, WorkflowVersionID: &vid})
	seedRun(t, s, tk.ID, 0, "active", now)
	plan := buildApplyPlan(tk, vid, def, agent, "Ag", samePersonClaimOps(tk, deskID, agent, now), 1, "active", domain.StateInProgress, &agent, nil)
	if _, err := newWorkflowUnitOfWork(s.db).ApplyWorkflowPlan(context.Background(), plan); err != nil {
		t.Fatalf("same-person claim apply: %v", err)
	}
	want := []string{"workflow_assignment|" + strconv.FormatInt(agent, 10) + "|" + strconv.FormatInt(agent, 10), "transition|new|in_progress"}
	if got := auditActionOrder(t, s, tk.ID); strings.Join(got, ";") != strings.Join(want, ";") {
		t.Fatalf("audits = %v want %v (one contextual row + transition)", got, want)
	}
	_, assignee, _ := ticketRow(t, s, tk.ID)
	if assignee == nil || *assignee != agent {
		t.Errorf("assignee = %v want %d unchanged", assignee, agent)
	}
}

// seedSamePersonClaim seeds a NEW ticket already assigned to the agent (so the
// claim is same-person) pinned to a claim definition with an active run at cursor
// 0, and returns the ticket, version id, definition, and the same-person
// operation group. The SAME immutable plan is reused across a broken-precondition
// failure and a full restore.
func seedSamePersonClaim(t *testing.T, s *Store, agent, deskID int64) (domain.Ticket, int64, domain.WorkflowDefinition, []application.WorkflowOperation) {
	t.Helper()
	cat := seedCategory(t, s, "C1")
	req := seedUserRole(t, s, "Req", "r@x", true, domain.RoleUser)
	def := claimDef(deskID)
	vid := seedPublished(t, s, cat, def)
	now := testClock
	tk := seedPinnedTicket(t, s, domain.Ticket{Number: 9, Title: "T", Description: "", RequesterName: "Req", RequesterEmail: "r@x", RequesterUserID: &req, CategoryID: cat, Priority: domain.PriorityMedium, State: domain.StateNew, UserID: &agent, CreatedAt: now, UpdatedAt: now, WorkflowVersionID: &vid})
	seedRun(t, s, tk.ID, 0, "active", now)
	ops := samePersonClaimOps(tk, deskID, agent, now)
	return tk, vid, def, ops
}

// TestWorkflowUoW_Claim_SamePerson_AuthorizationRecheck proves the PR6 same-person
// correction end-to-end with a SINGLE immutable same-person plan: an active,
// eligible, desk-member claimant succeeds with exactly one contextual row; making
// the claimant inactive or losing desk membership before apply is a typed
// ErrWorkflowPositionConflict with total rollback; and the same plan succeeds
// once every precondition is restored.
func TestWorkflowUoW_Claim_SamePerson_AuthorizationRecheck(t *testing.T) {
	t.Run("eligible member succeeds with one contextual row", func(t *testing.T) {
		s := newTestDB(t)
		agent := seedUserRole(t, s, "Ag", "a@x", true, domain.RoleAgent)
		deskID := seedDeskWithMember(t, s, agent)
		tk, vid, def, ops := seedSamePersonClaim(t, s, agent, deskID)
		plan := buildApplyPlan(tk, vid, def, agent, "Ag", ops, 1, "active", domain.StateInProgress, &agent, nil)
		if _, err := newWorkflowUnitOfWork(s.db).ApplyWorkflowPlan(context.Background(), plan); err != nil {
			t.Fatalf("eligible same-person claim must succeed, got %v", err)
		}
		want := []string{"workflow_assignment|" + strconv.FormatInt(agent, 10) + "|" + strconv.FormatInt(agent, 10), "transition|new|in_progress"}
		if got := auditActionOrder(t, s, tk.ID); strings.Join(got, ";") != strings.Join(want, ";") {
			t.Fatalf("audits = %v want %v (one contextual row + transition)", got, want)
		}
		state, assignee, _ := ticketRow(t, s, tk.ID)
		if state != "in_progress" || assignee == nil || *assignee != agent {
			t.Errorf("state=%s assignee=%v want in_progress/%d", state, assignee, agent)
		}
	})

	cases := []struct {
		name   string
		mutate func(t *testing.T, s *Store, agent, deskID int64)
	}{
		{"actor becomes inactive", func(t *testing.T, s *Store, agent, deskID int64) {
			if _, err := s.db.ExecContext(context.Background(), `UPDATE users SET active=0 WHERE id=?`, agent); err != nil {
				t.Fatalf("deactivate claimant: %v", err)
			}
		}},
		{"actor loses desk membership", func(t *testing.T, s *Store, agent, deskID int64) {
			if _, err := s.db.ExecContext(context.Background(), `DELETE FROM desk_members WHERE desk_id=? AND user_id=?`, deskID, agent); err != nil {
				t.Fatalf("remove membership: %v", err)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestDB(t)
			agent := seedUserRole(t, s, "Ag", "a@x", true, domain.RoleAgent)
			deskID := seedDeskWithMember(t, s, agent)
			tk, vid, def, ops := seedSamePersonClaim(t, s, agent, deskID)
			plan := buildApplyPlan(tk, vid, def, agent, "Ag", ops, 1, "active", domain.StateInProgress, &agent, nil)
			tc.mutate(t, s, agent, deskID)
			_, err := newWorkflowUnitOfWork(s.db).ApplyWorkflowPlan(context.Background(), plan)
			var wpc *domain.WorkflowPositionConflictError
			if !errors.As(err, &wpc) {
				t.Fatalf("must be a typed conflict, got %v", err)
			}
			assertApplyNoWrites(t, s, tk)
		})
	}

	t.Run("restore exact preconditions then retry same plan succeeds", func(t *testing.T) {
		s := newTestDB(t)
		agent := seedUserRole(t, s, "Ag", "a@x", true, domain.RoleAgent)
		deskID := seedDeskWithMember(t, s, agent)
		tk, vid, def, ops := seedSamePersonClaim(t, s, agent, deskID)
		plan := buildApplyPlan(tk, vid, def, agent, "Ag", ops, 1, "active", domain.StateInProgress, &agent, nil)
		ctx := context.Background()
		uow := newWorkflowUnitOfWork(s.db)
		// Break both preconditions (drop membership first so the role downgrade is
		// permitted by the DB trigger, matching the A->B precondition test).
		if _, err := s.db.ExecContext(ctx, `DELETE FROM desk_members WHERE desk_id=? AND user_id=?`, deskID, agent); err != nil {
			t.Fatalf("drop membership: %v", err)
		}
		if _, err := s.db.ExecContext(ctx, `UPDATE users SET active=0, role='user' WHERE id=?`, agent); err != nil {
			t.Fatalf("break preconditions: %v", err)
		}
		expectConflict := func(step string) {
			t.Helper()
			_, err := uow.ApplyWorkflowPlan(ctx, plan)
			var wpc *domain.WorkflowPositionConflictError
			if !errors.As(err, &wpc) {
				t.Fatalf("%s: must be a typed conflict, got %v", step, err)
			}
			assertApplyNoWrites(t, s, tk)
		}
		expectConflict("all preconditions broken")
		// Restore role+active first (the DB trigger forbids a 'user'-role desk member),
		// then the same plan must still fail while membership is missing...
		if _, err := s.db.ExecContext(ctx, `UPDATE users SET active=1, role='agent' WHERE id=?`, agent); err != nil {
			t.Fatalf("restore active+role: %v", err)
		}
		expectConflict("role+active restored, membership missing")
		// ...and only after membership is restored (now permitted again) does the
		// same plan succeed.
		if _, err := s.db.ExecContext(ctx, `INSERT INTO desk_members (desk_id, user_id, created_at) VALUES (?, ?, ?)`, deskID, agent, formatTime(testClock)); err != nil {
			t.Fatalf("restore membership: %v", err)
		}
		if _, err := uow.ApplyWorkflowPlan(ctx, plan); err != nil {
			t.Fatalf("same plan must succeed once every precondition is restored: %v", err)
		}
		state, assignee, _ := ticketRow(t, s, tk.ID)
		if state != "in_progress" || assignee == nil || *assignee != agent {
			t.Errorf("state=%s assignee=%v want in_progress/%d", state, assignee, agent)
		}
		prior := strconv.FormatInt(agent, 10)
		if got := auditActionOrder(t, s, tk.ID); strings.Join(got, ";") != strings.Join([]string{"workflow_assignment|" + prior + "|" + prior, "transition|new|in_progress"}, ";") {
			t.Fatalf("audits = %v want contextual row + transition", got)
		}
	})
}

// TestWorkflowUoW_Claim_InProgressReassignUserAuditOnly proves an in_progress
// A->B claim (with a valid reason) writes ONLY the user assignment audit and the
// completion audit — no state/transition audit, since the ticket is not new.
func TestWorkflowUoW_Claim_InProgressReassignUserAuditOnly(t *testing.T) {
	s := newTestDB(t)
	cat := seedCategory(t, s, "C1")
	req := seedUserRole(t, s, "Req", "r@x", true, domain.RoleUser)
	a := seedUserRole(t, s, "AgA", "a@x", true, domain.RoleAgent)
	b := seedUserRole(t, s, "AgB", "b@x", true, domain.RoleAgent)
	deskID := seedDeskWithMember(t, s, a)
	addMemberRaw(t, s, deskID, b)
	def := claimDef(deskID)
	vid := seedPublished(t, s, cat, def)
	now := testClock
	tk := seedPinnedTicket(t, s, domain.Ticket{Number: 7, Title: "T", Description: "", RequesterName: "Req", RequesterEmail: "r@x", RequesterUserID: &req, CategoryID: cat, Priority: domain.PriorityMedium, State: domain.StateInProgress, UserID: &a, CreatedAt: now, UpdatedAt: now, WorkflowVersionID: &vid})
	seedRun(t, s, tk.ID, 0, "active", now)
	plan := buildApplyPlan(tk, vid, def, b, "AgB", reassignClaimOps(tk, deskID, a, b, now), 1, "active", domain.StateInProgress, &b, nil)
	if _, err := newWorkflowUnitOfWork(s.db).ApplyWorkflowPlan(context.Background(), plan); err != nil {
		t.Fatalf("A->B reassign apply: %v", err)
	}
	want := []string{"workflow_assignment|" + strconv.FormatInt(a, 10) + "|" + strconv.FormatInt(b, 10)}
	if got := auditActionOrder(t, s, tk.ID); strings.Join(got, ";") != strings.Join(want, ";") {
		t.Fatalf("audits = %v want %v (user audit only, no state audit)", got, want)
	}
	state, assignee, _ := ticketRow(t, s, tk.ID)
	if state != "in_progress" {
		t.Errorf("state = %s want in_progress (no state change)", state)
	}
	if assignee == nil || *assignee != b {
		t.Errorf("assignee = %v want %d", assignee, b)
	}
}

// TestWorkflowUoW_Claim_ReassignWithoutReasonSucceeds proves an A->B pinned
// claim is reasonless while retaining its atomic assignment event.
func TestWorkflowUoW_Claim_ReassignWithoutReasonSucceeds(t *testing.T) {
	s := newTestDB(t)
	cat := seedCategory(t, s, "C1")
	req := seedUserRole(t, s, "Req", "r@x", true, domain.RoleUser)
	a := seedUserRole(t, s, "AgA", "a@x", true, domain.RoleAgent)
	b := seedUserRole(t, s, "AgB", "b@x", true, domain.RoleAgent)
	deskID := seedDeskWithMember(t, s, a)
	addMemberRaw(t, s, deskID, b)
	def := claimDef(deskID)
	vid := seedPublished(t, s, cat, def)
	now := testClock
	tk := seedPinnedTicket(t, s, domain.Ticket{Number: 7, Title: "T", Description: "", RequesterName: "Req", RequesterEmail: "r@x", RequesterUserID: &req, CategoryID: cat, Priority: domain.PriorityMedium, State: domain.StateInProgress, UserID: &a, CreatedAt: now, UpdatedAt: now, WorkflowVersionID: &vid})
	seedRun(t, s, tk.ID, 0, "active", now)
	plan := buildApplyPlan(tk, vid, def, b, "AgB", reassignClaimOps(tk, deskID, a, b, now), 1, "active", domain.StateInProgress, &b, nil)
	if _, err := newWorkflowUnitOfWork(s.db).ApplyWorkflowPlan(context.Background(), plan); err != nil {
		t.Fatalf("reasonless workflow reassignment must succeed, got %v", err)
	}
	if _, assignee, _ := ticketRow(t, s, tk.ID); assignee == nil || *assignee != b {
		t.Fatalf("reasonless workflow reassignment assignee = %v, want %d", assignee, b)
	}
}

// TestWorkflowUoW_Claim_ClaimantPreconditionRecheck proves each claimant
// precondition change (inactive, role below agent, lost desk membership) is a typed
// conflict with no writes, and that the SAME immutable plan succeeds only when every
// broken precondition is restored.
func TestWorkflowUoW_Claim_ClaimantPreconditionRecheck(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(t *testing.T, s *Store, claimant, deskID int64)
	}{
		{"claimant becomes inactive", func(t *testing.T, s *Store, claimant, deskID int64) {
			if _, err := s.db.ExecContext(context.Background(), `UPDATE users SET active=0 WHERE id=?`, claimant); err != nil {
				t.Fatalf("deactivate claimant: %v", err)
			}
		}},
		{"claimant role below agent", func(t *testing.T, s *Store, claimant, deskID int64) {
			// The DB trigger forbids demoting a CURRENT desk member to 'user', so the
			// membership is dropped first; the apply recheck then still rejects on the
			// role gate (claimantEligibleTx) before the membership gate (deskMemberTx).
			if _, err := s.db.ExecContext(context.Background(), `DELETE FROM desk_members WHERE desk_id=? AND user_id=?`, deskID, claimant); err != nil {
				t.Fatalf("drop membership for demotion: %v", err)
			}
			if _, err := s.db.ExecContext(context.Background(), `UPDATE users SET role='user' WHERE id=?`, claimant); err != nil {
				t.Fatalf("demote claimant: %v", err)
			}
		}},
		{"claimant loses desk membership", func(t *testing.T, s *Store, claimant, deskID int64) {
			if _, err := s.db.ExecContext(context.Background(), `DELETE FROM desk_members WHERE desk_id=? AND user_id=?`, deskID, claimant); err != nil {
				t.Fatalf("remove membership: %v", err)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestDB(t)
			a := seedUserRole(t, s, "AgA", "a@x", true, domain.RoleAgent)
			b := seedUserRole(t, s, "AgB", "b@x", true, domain.RoleAgent)
			deskID := seedDeskWithMember(t, s, a)
			addMemberRaw(t, s, deskID, b)
			tk, vid, def, ops := seedClaimPending(t, s, b, deskID)
			plan := buildApplyPlan(tk, vid, def, b, "Ag", ops, 1, "active", domain.StateInProgress, &b, nil)
			// Mutate the precondition AFTER building the immutable plan.
			tc.mutate(t, s, b, deskID)
			_, err := newWorkflowUnitOfWork(s.db).ApplyWorkflowPlan(context.Background(), plan)
			var wpc *domain.WorkflowPositionConflictError
			if !errors.As(err, &wpc) {
				t.Fatalf("must be a typed conflict, got %v", err)
			}
			assertApplyNoWrites(t, s, tk)
		})
	}
	t.Run("same plan succeeds only when all preconditions restored", func(t *testing.T) {
		s := newTestDB(t)
		a := seedUserRole(t, s, "AgA", "a@x", true, domain.RoleAgent)
		b := seedUserRole(t, s, "AgB", "b@x", true, domain.RoleAgent)
		deskID := seedDeskWithMember(t, s, a)
		addMemberRaw(t, s, deskID, b)
		tk, vid, def, ops := seedClaimPending(t, s, b, deskID)
		plan := buildApplyPlan(tk, vid, def, b, "Ag", ops, 1, "active", domain.StateInProgress, &b, nil)
		ctx := context.Background()
		uow := newWorkflowUnitOfWork(s.db)
		// Break all three preconditions at once (drop membership first so the role
		// downgrade is permitted by the DB trigger).
		if _, err := s.db.ExecContext(ctx, `DELETE FROM desk_members WHERE desk_id=? AND user_id=?`, deskID, b); err != nil {
			t.Fatalf("drop membership: %v", err)
		}
		if _, err := s.db.ExecContext(ctx, `UPDATE users SET active=0, role='user' WHERE id=?`, b); err != nil {
			t.Fatalf("break preconditions: %v", err)
		}
		expectConflict := func(step string) {
			t.Helper()
			_, err := uow.ApplyWorkflowPlan(ctx, plan)
			var wpc *domain.WorkflowPositionConflictError
			if !errors.As(err, &wpc) {
				t.Fatalf("%s: must be a typed conflict, got %v", step, err)
			}
			assertApplyNoWrites(t, s, tk)
		}
		expectConflict("all preconditions broken")
		// Restore exactly one precondition, retry still fails...
		if _, err := s.db.ExecContext(ctx, `UPDATE users SET active=1 WHERE id=?`, b); err != nil {
			t.Fatalf("restore active: %v", err)
		}
		expectConflict("only active restored")
		if _, err := s.db.ExecContext(ctx, `UPDATE users SET role='agent' WHERE id=?`, b); err != nil {
			t.Fatalf("restore role: %v", err)
		}
		expectConflict("active+agent role restored, membership missing")
		// ...and only after membership is restored does the same plan succeed.
		if _, err := s.db.ExecContext(ctx, `INSERT INTO desk_members (desk_id, user_id, created_at) VALUES (?, ?, ?)`, deskID, b, formatTime(testClock)); err != nil {
			t.Fatalf("restore membership: %v", err)
		}
		if _, err := uow.ApplyWorkflowPlan(ctx, plan); err != nil {
			t.Fatalf("same plan must succeed once every precondition is restored: %v", err)
		}
		state, assignee, _ := ticketRow(t, s, tk.ID)
		if state != "in_progress" || assignee == nil || *assignee != b {
			t.Errorf("state=%s assignee=%v want in_progress/%d", state, assignee, b)
		}
	})
}

func containsTicketID(list []domain.Ticket, id int64) bool {
	for _, t := range list {
		if t.ID == id {
			return true
		}
	}
	return false
}

// TestWorkflowUoW_Scope_AssignedBranchNullPinAndList proves the read scope genuinely
// exercises the assigned OR branch (an assigned ticket is readable via the assigned
// half), that a NULL-pinned assigned ticket remains readable (the claimable EXISTS
// short-circuits on the missing pin), and that GetByID/List use
// ScopeAssignedOrClaimable for a claim-pending actor while the mutation scope stays
// strict ScopeAssigned.
func TestWorkflowUoW_Scope_AssignedBranchNullPinAndList(t *testing.T) {
	s := newTestDB(t)
	cat := seedCategory(t, s, "C1")
	req := seedUser(t, s, "Req", "r@x", true)
	member := seedUser(t, s, "Mbr", "m@x", true)
	other := seedUser(t, s, "Oth", "o@x", true)
	deskID := seedDeskWithMember(t, s, member)
	def := claimDef(deskID)
	vid := seedPublished(t, s, cat, def)
	now := testClock
	ctx := context.Background()
	store := s.TicketStore()

	// Assigned OR branch: a claim-pending ticket assigned to the member is readable
	// via the assigned half of ScopeAssignedOrClaimable.
	assigned := seedPinnedTicket(t, s, domain.Ticket{Number: 6, Title: "T", Description: "", RequesterName: "Req", RequesterEmail: "r@x", RequesterUserID: &req, CategoryID: cat, Priority: domain.PriorityMedium, State: domain.StateNew, UserID: &member, CreatedAt: now, UpdatedAt: now, WorkflowVersionID: &vid})
	seedRun(t, s, assigned.ID, 0, "active", now)
	if _, err := store.GetByID(ctx, assigned.ID, application.TicketQuery{Scope: application.ScopeAssignedOrClaimable, ActorID: member}); err != nil {
		t.Errorf("assigned member must read via the assigned branch, got %v", err)
	}
	if _, err := store.GetByID(ctx, assigned.ID, application.TicketQuery{Scope: application.ScopeAssignedOrClaimable, ActorID: other}); err == nil {
		t.Errorf("non-member must not read the assigned ticket")
	}

	// NULL-pinned assigned ticket remains readable: with no workflow_version_id the
	// claimable EXISTS join produces no row, so only the assigned predicate applies —
	// it must still be readable by the assignee and hidden from everyone else.
	nullPin := seedTicket(t, s, domain.Ticket{Number: 7, Title: "T", Description: "", RequesterName: "Req", RequesterEmail: "r@x", RequesterUserID: &req, CategoryID: cat, Priority: domain.PriorityMedium, State: domain.StateInProgress, UserID: &member, CreatedAt: now, UpdatedAt: now})
	if _, err := store.GetByID(ctx, nullPin.ID, application.TicketQuery{Scope: application.ScopeAssignedOrClaimable, ActorID: member}); err != nil {
		t.Errorf("NULL-pinned assigned ticket must remain readable, got %v", err)
	}
	if _, err := store.GetByID(ctx, nullPin.ID, application.TicketQuery{Scope: application.ScopeAssignedOrClaimable, ActorID: other}); err == nil {
		t.Errorf("non-member must NOT read the NULL-pinned ticket")
	}

	// List: a claim-pending (unassigned) ticket is visible to the member under the
	// READ scope but NOT under the strict mutation ScopeAssigned list.
	pending := seedPinnedTicket(t, s, domain.Ticket{Number: 8, Title: "T", Description: "", RequesterName: "Req", RequesterEmail: "r@x", RequesterUserID: &req, CategoryID: cat, Priority: domain.PriorityMedium, State: domain.StateNew, CreatedAt: now, UpdatedAt: now, WorkflowVersionID: &vid})
	seedRun(t, s, pending.ID, 0, "active", now)
	readList, err := store.List(ctx, application.TicketQuery{Scope: application.ScopeAssignedOrClaimable, ActorID: member}, application.Page{Limit: 50})
	if err != nil {
		t.Fatalf("read-scope list: %v", err)
	}
	if !containsTicketID(readList, pending.ID) {
		t.Errorf("read-scope list must include the claim-pending ticket for the member")
	}
	mutList, err := store.List(ctx, application.TicketQuery{Scope: application.ScopeAssigned, ActorID: member}, application.Page{Limit: 50})
	if err != nil {
		t.Fatalf("mutation-scope list: %v", err)
	}
	if containsTicketID(mutList, pending.ID) {
		t.Errorf("strict ScopeAssigned list must NOT include the unassigned claim-pending ticket")
	}
}
