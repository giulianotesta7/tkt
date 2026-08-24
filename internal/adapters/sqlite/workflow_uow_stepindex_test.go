package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// PR10 task 10.1 — step-indexed semantic audit persistence. These REAL SQLite
// tests prove:
//   - a least_loaded assignment audit built by the ADAPTER inside the immediate
//     transaction persists its sealed zero-based step index;
//   - plan-carried semantic form/manual audits persist their carried index
//     through appendAuditEventsTx;
//   - transition audits and the created audit stay NULL;
//   - every value round-trips nil-safe through AuditStore.ListByTicket.

func TestWorkflowUoW_LeastLoadedAssignment_StepIndexPersisted(t *testing.T) {
	s := newTestDB(t)
	cat := seedCategory(t, s, "SI-Cat")
	req := seedUserRole(t, s, "Req", "r@x", true, domain.RoleUser)
	agent := seedUserRole(t, s, "Ag", "a@x", true, domain.RoleAgent)
	deskID := seedDeskWithMember(t, s, agent)
	def := leastLoadedDef(deskID)
	vid := seedPublished(t, s, cat, def)
	now := testClock
	in := buildCreateInput(cat, vid, req, def, leastLoadedCreateOps(deskID, now), 1, "completed", domain.StateInProgress, &now)

	tk, err := newWorkflowUnitOfWork(s.db).CreateTicketWithRun(context.Background(), in)
	if err != nil {
		t.Fatalf("least_loaded create: %v", err)
	}
	events, err := s.AuditStore().ListByTicket(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("list audits: %v", err)
	}
	var assignment, transition, created *domain.AuditEvent
	for i := range events {
		switch events[i].Action {
		case domain.ActionWorkflowAssignment:
			assignment = &events[i]
		case domain.ActionTransition:
			transition = &events[i]
		case domain.ActionCreated:
			created = &events[i]
		}
	}
	if assignment == nil || assignment.StepIndex == nil || *assignment.StepIndex != 0 {
		t.Fatalf("least_loaded assignment StepIndex = %v, want sealed 0", assignment)
	}
	if transition == nil || transition.StepIndex != nil {
		t.Fatalf("transition StepIndex = %v, want nil", transition)
	}
	if created == nil || created.StepIndex != nil {
		t.Fatalf("created StepIndex = %v, want nil (non-flow audits carry no index)", created)
	}
}

func TestWorkflowUoW_FormAndManual_StepIndexRoundTrip(t *testing.T) {
	s := newTestDB(t)
	cat := seedCategory(t, s, "SI-Form")
	requester := seedUserRole(t, s, "Requester", "req@x", true, domain.RoleUser)
	agent := seedUserRole(t, s, "Agent", "agent@x", true, domain.RoleAgent)
	def := domain.WorkflowDefinition{
		{Type: domain.StepForm, Form: &domain.FormStep{Actor: domain.FormActorRequester, Fields: []domain.FormField{{Key: "host", Label: "Host", Kind: domain.FieldShortText}}}},
		{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "Rack the server"}},
	}
	versionID := seedPublished(t, s, cat, def)
	now := testClock
	ticket := seedPinnedTicket(t, s, domain.Ticket{Number: 1, Title: "T", RequesterName: "Requester", RequesterEmail: "req@x", RequesterUserID: &requester, CategoryID: cat, Priority: domain.PriorityMedium, State: domain.StateNew, CreatedAt: now, UpdatedAt: now, WorkflowVersionID: &versionID})
	// The manual_task step requires the current assignee (an agent+ user); the
	// fixture assigns an agent directly so both completions run without an
	// intermediate assignment step (index round-trip is the behavior under test).
	ticket.UserID = &agent
	if _, err := s.db.Exec(`UPDATE tickets SET user_id=? WHERE id=?`, agent, ticket.ID); err != nil {
		t.Fatalf("assign ticket: %v", err)
	}
	seedRun(t, s, ticket.ID, 0, "active", now)

	runner := application.NewWorkflowRunner(terminalClock{now: testClock})
	snap := application.WorkflowExecutionSnapshot{
		Ticket:   &ticket,
		Run:      &application.WorkflowRun{TicketID: ticket.ID, CurrentStepIndex: 0, Status: "active", StartedAt: testClock},
		Workflow: def,
	}

	// Step 0: requester form completion.
	formCmd := application.CompleteWorkflowCommand{
		TicketID: ticket.ID, ActorUserID: requester, ActorName: "Requester",
		ExpectedPosition: 1,
		RawAnswers:       application.RawPositionalValues{{Position: 0, Values: []string{"api-01"}}},
	}
	plan, err := runner.PlanComplete(context.Background(), snap, formCmd)
	if err != nil {
		t.Fatalf("plan form: %v", err)
	}
	if _, err := newWorkflowUnitOfWork(s.db).ApplyWorkflowPlan(context.Background(), plan); err != nil {
		t.Fatalf("apply form plan: %v", err)
	}

	// Step 1: manual task by the current assignee.
	snap.Run.CurrentStepIndex = 1
	manualCmd := application.CompleteWorkflowCommand{
		TicketID: ticket.ID, ActorUserID: agent, ActorName: "Agent", ExpectedPosition: 2,
	}
	plan, err = runner.PlanComplete(context.Background(), snap, manualCmd)
	if err != nil {
		t.Fatalf("plan manual: %v", err)
	}
	if _, err := newWorkflowUnitOfWork(s.db).ApplyWorkflowPlan(context.Background(), plan); err != nil {
		t.Fatalf("apply manual plan: %v", err)
	}

	events, err := s.AuditStore().ListByTicket(context.Background(), ticket.ID)
	if err != nil {
		t.Fatalf("list audits: %v", err)
	}
	indexes := map[string]*int{}
	for i := range events {
		indexes[events[i].Action] = events[i].StepIndex
	}
	if got := indexes[domain.ActionWorkflowRequesterForm]; got == nil || *got != 0 {
		t.Fatalf("workflow_requester_form persisted StepIndex = %v, want 0", got)
	}
	if got := indexes[domain.ActionWorkflowManualTask]; got == nil || *got != 1 {
		t.Fatalf("workflow_manual_task persisted StepIndex = %v, want 1", got)
	}
	// The run completed without any transition: nothing else may carry an index.
	for _, ev := range events {
		switch ev.Action {
		case domain.ActionWorkflowRequesterForm, domain.ActionWorkflowManualTask:
		default:
			if ev.StepIndex != nil {
				t.Fatalf("action %q must keep StepIndex NULL, got %d", ev.Action, *ev.StepIndex)
			}
		}
	}
}

// Amendment 2 WA.4 — manual-task solution persistence inside the SAME
// BEGIN IMMEDIATE unit as the completion audit and cursor CAS:
//   - a non-empty trimmed solution inserts exactly one ticket_manual_solutions
//     row reusing the operation's audit actor-user-id/created-at facts;
//   - an empty-solution completion persists NO row;
//   - an injected write failure (the 2,000-char CHECK mirror) rolls back the
//     WHOLE unit — no partial solution, no completion/cursor/audit remnant.

func manualSolutionFixture(t *testing.T, s *Store) (domain.Ticket, int64, domain.WorkflowDefinition) {
	t.Helper()
	cat := seedCategory(t, s, "Solution-Cat")
	assignee := seedUserRole(t, s, "Assignee", "sol-assignee@x", true, domain.RoleAgent)
	def := domain.WorkflowDefinition{
		{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "Rack the server"}},
	}
	versionID := seedPublished(t, s, cat, def)
	now := testClock
	ticket := seedPinnedTicket(t, s, domain.Ticket{Number: 5, Title: "Solved", RequesterName: "Assignee", RequesterEmail: "sol-assignee@x", RequesterUserID: &assignee, CategoryID: cat, Priority: domain.PriorityMedium, State: domain.StateNew, CreatedAt: now, UpdatedAt: now, WorkflowVersionID: &versionID})
	// The manual_task step requires the current assignee; the fixture assigns
	// an agent directly so completion runs without an intermediate step.
	ticket.UserID = &assignee
	if _, err := s.db.Exec(`UPDATE tickets SET user_id=? WHERE id=?`, assignee, ticket.ID); err != nil {
		t.Fatalf("assign ticket: %v", err)
	}
	seedRun(t, s, ticket.ID, 0, "active", now)
	return ticket, assignee, def
}

func manualSolutionPlan(t *testing.T, ticket domain.Ticket, assignee int64, def domain.WorkflowDefinition, solution string) application.WorkflowMutationPlan {
	t.Helper()
	runner := application.NewWorkflowRunner(terminalClock{now: testClock})
	snap := application.WorkflowExecutionSnapshot{
		Ticket:   &ticket,
		Run:      &application.WorkflowRun{TicketID: ticket.ID, CurrentStepIndex: 0, Status: "active", StartedAt: testClock},
		Workflow: def,
	}
	cmd := application.CompleteWorkflowCommand{TicketID: ticket.ID, ActorUserID: assignee, ActorName: "Assignee", ExpectedPosition: 1, Solution: solution}
	plan, err := runner.PlanComplete(context.Background(), snap, cmd)
	if err != nil {
		t.Fatalf("plan manual completion: %v", err)
	}
	return plan
}

func countManualSolutions(t *testing.T, db interface {
	QueryRow(query string, args ...any) *sql.Row
}, ticketID int64) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM ticket_manual_solutions WHERE ticket_id=?`, ticketID).Scan(&n); err != nil {
		t.Fatalf("count solutions: %v", err)
	}
	return n
}

func TestWorkflowUoW_ManualSolution_InsertReusesAuditFacts(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	ticket, assignee, def := manualSolutionFixture(t, s)

	plan := manualSolutionPlan(t, ticket, assignee, def, "  racked u12 in cabinet B7 \t")
	if _, err := newWorkflowUnitOfWork(s.db).ApplyWorkflowPlan(ctx, plan); err != nil {
		t.Fatalf("apply plan with solution: %v", err)
	}

	// Exactly ONE solution row, keyed by the sealed persisted step index,
	// carrying the trimmed value and the AUDIT's actor-id/timestamp facts.
	var (
		stepIndex       int
		solution        string
		createdByUserID int64
		createdAt       string
	)
	if err := s.db.QueryRow(`SELECT step_index, solution, created_by_user_id, created_at FROM ticket_manual_solutions WHERE ticket_id=?`, ticket.ID).
		Scan(&stepIndex, &solution, &createdByUserID, &createdAt); err != nil {
		t.Fatalf("read stored solution: %v", err)
	}
	if stepIndex != 0 || solution != "racked u12 in cabinet B7" || createdByUserID != assignee {
		t.Fatalf("stored solution = (%d, %q, actor %d), want (0, trimmed value, actor %d)", stepIndex, solution, createdByUserID, assignee)
	}

	events, err := s.AuditStore().ListByTicket(ctx, ticket.ID)
	if err != nil {
		t.Fatalf("list audits: %v", err)
	}
	var manual *domain.AuditEvent
	for i := range events {
		if events[i].Action == domain.ActionWorkflowManualTask {
			manual = &events[i]
		}
	}
	if manual == nil {
		t.Fatal("manual completion audit missing")
	}
	if manual.ActorUserID == nil || *manual.ActorUserID != createdByUserID || formatTime(manual.CreatedAt) != createdAt {
		t.Fatalf("solution facts (%d, %s) diverge from audit facts (%v, %s)", createdByUserID, createdAt, manual.ActorUserID, formatTime(manual.CreatedAt))
	}
}

func TestWorkflowUoW_EmptySolutionCompletion_PersistsNoRow(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	ticket, assignee, def := manualSolutionFixture(t, s)

	plan := manualSolutionPlan(t, ticket, assignee, def, "")
	if _, err := newWorkflowUnitOfWork(s.db).ApplyWorkflowPlan(ctx, plan); err != nil {
		t.Fatalf("apply empty-solution plan: %v", err)
	}
	if got := countManualSolutions(t, s.db, ticket.ID); got != 0 {
		t.Fatalf("empty-solution completion persisted %d solution rows, want 0", got)
	}
	// The completion itself still happened: audit written, cursor advanced.
	var cursor int
	var status string
	if err := s.db.QueryRow(`SELECT current_step_index, status FROM ticket_workflow_runs WHERE ticket_id=?`, ticket.ID).Scan(&cursor, &status); err != nil {
		t.Fatalf("read run: %v", err)
	}
	if cursor != 1 || status != "completed" {
		t.Fatalf("run = (%d, %s), want advanced (1, completed)", cursor, status)
	}
}

func TestWorkflowUoW_SolutionFailure_RollsBackWholeUnit(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	ticket, assignee, def := manualSolutionFixture(t, s)

	// The 2,001-character value passes planning (the transport bound lives in
	// the HTTP layer, WB) and is rejected ONLY by the storage CHECK inside the
	// immediate transaction — the injected failure proving atomicity.
	tooLong := strings.Repeat("y", 2001)
	plan := manualSolutionPlan(t, ticket, assignee, def, tooLong)
	if _, err := newWorkflowUnitOfWork(s.db).ApplyWorkflowPlan(ctx, plan); err == nil {
		t.Fatal("oversized solution must fail the unit at the storage CHECK")
	}

	if got := countManualSolutions(t, s.db, ticket.ID); got != 0 {
		t.Fatalf("%d partial solution rows survived rollback, want 0", got)
	}
	events, err := s.AuditStore().ListByTicket(ctx, ticket.ID)
	if err != nil {
		t.Fatalf("list audits: %v", err)
	}
	for i := range events {
		if events[i].Action == domain.ActionWorkflowManualTask {
			t.Fatalf("completion audit survived rollback: %+v", events[i])
		}
	}
	var cursor int
	var status string
	if err := s.db.QueryRow(`SELECT current_step_index, status FROM ticket_workflow_runs WHERE ticket_id=?`, ticket.ID).Scan(&cursor, &status); err != nil {
		t.Fatalf("read run: %v", err)
	}
	if cursor != 0 || status != "active" {
		t.Fatalf("cursor moved despite rollback: (%d, %s), want (0, active)", cursor, status)
	}
}

// Amendment 2 WA.7 TRIANGULATE — membership boundaries of the stored solution,
// proven against the REAL runner→UoW path with a distinctive marker string.

// TestWorkflowUoW_SolutionNonMembershipEverywhere proves the marker exists in
// EXACTLY ONE place: ticket_manual_solutions. It must be absent from every
// audit_events.note AND audit_events.reason value, absent from comments, and
// absent from every tickets_fts indexed document.
func TestWorkflowUoW_SolutionNonMembershipEverywhere(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	ticket, assignee, def := manualSolutionFixture(t, s)

	const marker = "ZZSOLMARKERQ7 unique7f3a"
	plan := manualSolutionPlan(t, ticket, assignee, def, "racked u12 — "+marker+" — verified")
	if _, err := newWorkflowUnitOfWork(s.db).ApplyWorkflowPlan(ctx, plan); err != nil {
		t.Fatalf("apply plan with marker solution: %v", err)
	}

	// Present exactly once, in the workflow task record only.
	if got := countManualSolutions(t, s.db, ticket.ID); got != 1 {
		t.Fatalf("solution rows = %d, want exactly 1", got)
	}
	var stored string
	if err := s.db.QueryRow(`SELECT solution FROM ticket_manual_solutions WHERE ticket_id=?`, ticket.ID).Scan(&stored); err != nil {
		t.Fatalf("read solution: %v", err)
	}
	if !strings.Contains(stored, marker) {
		t.Fatalf("stored solution %q lost the marker", stored)
	}

	// Absent from audit notes AND reasons.
	var leaked int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE note LIKE '%'||?||'%' OR reason LIKE '%'||?||'%'`, marker, marker).Scan(&leaked); err != nil {
		t.Fatalf("scan audit leaks: %v", err)
	}
	if leaked != 0 {
		t.Fatalf("marker found in %d audit note/reason values, want 0", leaked)
	}

	// Absent from comments.
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM comments WHERE body LIKE '%'||?||'%'`, marker).Scan(&leaked); err != nil {
		t.Fatalf("scan comment leaks: %v", err)
	}
	if leaked != 0 {
		t.Fatalf("marker found in %d comments, want 0", leaked)
	}

	// Absent from every full-text indexed document.
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM tickets_fts WHERE tickets_fts MATCH ?`, marker).Scan(&leaked); err != nil {
		t.Fatalf("query fts: %v", err)
	}
	if leaked != 0 {
		t.Fatalf("marker found in %d tickets_fts documents, want 0", leaked)
	}

	// The completion itself is fully intact: audit written, cursor advanced.
	var cursor int
	if err := s.db.QueryRow(`SELECT current_step_index FROM ticket_workflow_runs WHERE ticket_id=?`, ticket.ID).Scan(&cursor); err != nil {
		t.Fatalf("read cursor: %v", err)
	}
	if cursor != 1 {
		t.Fatalf("cursor = %d, want 1 (completion committed)", cursor)
	}
}

// TestWorkflowUoW_SolutionMaxLengthBoundaryRoundTrip drives an EXACTLY
// 2,000-character solution through planning, persistence, and the pinned-context
// read: accepted whole, retrievable byte-for-byte.
func TestWorkflowUoW_SolutionMaxLengthBoundaryRoundTrip(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	ticket, assignee, def := manualSolutionFixture(t, s)

	exactly2000 := strings.Repeat("x", 2000)
	plan := manualSolutionPlan(t, ticket, assignee, def, exactly2000)
	if _, err := newWorkflowUnitOfWork(s.db).ApplyWorkflowPlan(ctx, plan); err != nil {
		t.Fatalf("2,000-char solution must be accepted: %v", err)
	}
	stepCtx, err := newWorkflowResponseStore(s.db).WorkflowStepContext(ctx, ticket.ID, 0)
	if err != nil {
		t.Fatalf("read step context: %v", err)
	}
	if stepCtx == nil || stepCtx.Solution != exactly2000 {
		t.Fatalf("round-trip solution length = %d, want 2000 identical bytes", len(stepCtx.Solution))
	}
}

// TestWorkflowUoW_SolutionConcurrentDuplicateGetsConflictSingleRow simulates two
// interleaved completions of the SAME manual step: the loser receives the typed
// position conflict and exactly ONE solution row survives.
func TestWorkflowUoW_SolutionConcurrentDuplicateGetsConflictSingleRow(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	ticket, assignee, def := manualSolutionFixture(t, s)

	first := manualSolutionPlan(t, ticket, assignee, def, "first submission")
	if _, err := newWorkflowUnitOfWork(s.db).ApplyWorkflowPlan(ctx, first); err != nil {
		t.Fatalf("apply first completion: %v", err)
	}

	// A second actor request planned against the STALE pre-advancement snapshot.
	staleTicket := ticket
	staleTicket.UserID = &assignee
	second := manualSolutionPlan(t, staleTicket, assignee, def, "duplicate submission")
	if _, err := newWorkflowUnitOfWork(s.db).ApplyWorkflowPlan(ctx, second); !errors.Is(err, domain.ErrWorkflowPositionConflict) {
		t.Fatalf("duplicate completion = %v, want typed position conflict", err)
	}
	if got := countManualSolutions(t, s.db, ticket.ID); got != 1 {
		t.Fatalf("solution rows after duplicate = %d, want exactly 1", got)
	}
	var kept string
	if err := s.db.QueryRow(`SELECT solution FROM ticket_manual_solutions WHERE ticket_id=?`, ticket.ID).Scan(&kept); err != nil {
		t.Fatalf("read surviving solution: %v", err)
	}
	if kept != "first submission" {
		t.Fatalf("surviving solution = %q, want the winner's value unchanged", kept)
	}
}
