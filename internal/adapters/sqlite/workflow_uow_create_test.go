package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// PR5 Batch B1 — real SQLite WorkflowVersionStore + WorkflowUnitOfWork.
//
// These tests run against the real modernc driver (newTestDB) and prove the
// adapter re-reads and rechecks, applies ONLY fixed data-only operations in
// literal order (never dispatching by step.Type), and rolls back every write on
// any failure (design S5 all-or-nothing).

// seedPublished inserts an immutable workflow version and points the category's
// current_version_id at it; returns the version id.
func seedPublished(t *testing.T, s *Store, catID int64, def domain.WorkflowDefinition) int64 {
	t.Helper()
	canon, err := def.MarshalCanonical()
	if err != nil {
		t.Fatalf("canonicalize def: %v", err)
	}
	res, err := s.db.ExecContext(context.Background(),
		`INSERT INTO workflow_versions (category_id, version_no, steps_json, published_at) VALUES (?, 1, ?, ?)`,
		catID, string(canon), "2026-08-06T10:00:00Z")
	if err != nil {
		t.Fatalf("insert version: %v", err)
	}
	vid, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("version id: %v", err)
	}
	if _, err := s.db.ExecContext(context.Background(),
		`INSERT INTO category_workflows (category_id, draft_json, current_version_id) VALUES (?, '[]', ?)
		 ON CONFLICT(category_id) DO UPDATE SET current_version_id = excluded.current_version_id`, catID, vid); err != nil {
		t.Fatalf("point current: %v", err)
	}
	return vid
}

// buildCreateInput assembles a concrete CreateTicketWithRunInput for a ticket
// pinned to versionID, with explicit operations/result facts.
func buildCreateInput(catID, versionID, requester int64, wf domain.WorkflowDefinition, ops []application.WorkflowOperation, nextCursor int, nextStatus string, nextState domain.State, completedAt *time.Time) application.CreateTicketWithRunInput {
	now := testClock
	req := requester
	tk := &domain.Ticket{
		Title: "T", Description: "", RequesterName: "Req", RequesterEmail: "r@x",
		RequesterUserID: &req, CategoryID: catID, Priority: domain.PriorityMedium,
		State: domain.StateNew, CreatedAt: now, UpdatedAt: now,
	}
	v := versionID
	tk.WorkflowVersionID = &v
	created := domain.AuditEvent{Actor: "Req", ActorUserID: &req, Action: domain.ActionCreated, CreatedAt: now}
	in := application.CreateTicketWithRunInput{
		CategoryID:        catID,
		ExpectedVersionID: versionID,
		Workflow:          wf.Clone(),
		Ticket:            tk,
		CreatedAudit:      created,
		StartedAt:         now,
		ExpectedCursor:    0,
		ExpectedRunStatus: "active",
		Operations:        ops,
		NextCursor:        nextCursor,
		NextRunStatus:     nextStatus,
		NextTicketState:   nextState,
		CompletedAt:       completedAt,
	}
	return in
}

// auditActionOrder returns (action, from, to) in persisted id order.
func auditActionOrder(t *testing.T, s *Store, ticketID int64) []string {
	t.Helper()
	rows, err := s.db.QueryContext(context.Background(),
		`SELECT action, COALESCE(from_value,''), COALESCE(to_value,'') FROM audit_events WHERE ticket_id=? ORDER BY id`, ticketID)
	if err != nil {
		t.Fatalf("list audits: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var a, f, to string
		if err := rows.Scan(&a, &f, &to); err != nil {
			t.Fatalf("scan audit: %v", err)
		}
		out = append(out, a+"|"+f+"|"+to)
	}
	return out
}

func ticketRow(t *testing.T, s *Store, id int64) (string, *int64, int64) {
	t.Helper()
	var state string
	var assigned sql.NullInt64
	var wid sql.NullInt64
	if err := s.db.QueryRowContext(context.Background(),
		`SELECT state, user_id, workflow_version_id FROM tickets WHERE id=?`, id).Scan(&state, &assigned, &wid); err != nil {
		t.Fatalf("read ticket: %v", err)
	}
	var ap *int64
	if assigned.Valid {
		v := assigned.Int64
		ap = &v
	}
	return state, ap, wid.Int64
}

func runRow(t *testing.T, s *Store, ticketID int64) (int, string, *string) {
	t.Helper()
	var cur int
	var status string
	var comp *string
	if err := s.db.QueryRowContext(context.Background(),
		`SELECT current_step_index, status, completed_at FROM ticket_workflow_runs WHERE ticket_id=?`, ticketID).Scan(&cur, &status, &comp); err != nil {
		t.Fatalf("read run: %v", err)
	}
	return cur, status, comp
}

// seedRun inserts a run row through a real immediate transaction (arrange).
func seedRun(t *testing.T, s *Store, ticketID int64, cursor int, status string, started time.Time) {
	t.Helper()
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin seed run: %v", err)
	}
	defer tx.Rollback()
	if err := insertRunTx(context.Background(), tx, ticketID, cursor, status, started); err != nil {
		t.Fatalf("seed run: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit seed run: %v", err)
	}
}

func assertTotalRollback(t *testing.T, s *Store) {
	t.Helper()
	var tickets, runs, audits, answers int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM tickets`).Scan(&tickets)
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM ticket_workflow_runs`).Scan(&runs)
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM audit_events`).Scan(&audits)
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM ticket_form_answers`).Scan(&answers)
	if tickets != 0 || runs != 0 || audits != 0 || answers != 0 {
		t.Fatalf("rollback failed: tickets=%d runs=%d audits=%d answers=%d, want all 0", tickets, runs, audits, answers)
	}
}

// --- WorkflowVersionStore.GetCurrentVersion ---

func TestWorkflowVersionStore_Current_DraftIsolationAndDeepOwnership(t *testing.T) {
	s := newTestDB(t)
	cat := seedCategory(t, s, "C1")
	ctx := context.Background()

	// No workflow row: unavailable.
	pv, err := s.WorkflowVersionStore().GetCurrentVersion(ctx, cat)
	if err != nil {
		t.Fatalf("GetCurrentVersion(no row): %v", err)
	}
	if pv != nil {
		t.Fatalf("no-row category must be unavailable, got %+v", pv)
	}

	// Draft-only (category_workflows row, NULL current_version_id): still unavailable,
	// and draft_json is NEVER consulted for creation.
	if _, err := s.db.ExecContext(ctx, `INSERT INTO category_workflows (category_id, draft_json, current_version_id) VALUES (?, '[]', NULL)`, cat); err != nil {
		t.Fatalf("seed draft: %v", err)
	}
	pv, err = s.WorkflowVersionStore().GetCurrentVersion(ctx, cat)
	if err != nil {
		t.Fatalf("GetCurrentVersion(draft-only): %v", err)
	}
	if pv != nil {
		t.Fatalf("draft-only category must be unavailable (never reads draft_json), got %+v", pv)
	}

	// Published version: available, correct id + fresh parsed definition.
	def := domain.WorkflowDefinition{{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "do it"}}}
	vid := seedPublished(t, s, cat, def)
	pv, err = s.WorkflowVersionStore().GetCurrentVersion(ctx, cat)
	if err != nil {
		t.Fatalf("GetCurrentVersion(published): %v", err)
	}
	if pv == nil || pv.VersionID != vid || pv.CategoryID != cat {
		t.Fatalf("published snapshot wrong: %+v", pv)
	}

	// Deep ownership: mutating the returned definition must NOT affect the store.
	pv.Workflow[0].ManualTask.Instructions = "CORRUPT"
	pv2, err := s.WorkflowVersionStore().GetCurrentVersion(ctx, cat)
	if err != nil {
		t.Fatalf("GetCurrentVersion(second): %v", err)
	}
	if pv2.Workflow[0].ManualTask.Instructions != "do it" {
		t.Fatalf("returned workflow aliases store memory: got %q want %q", pv2.Workflow[0].ManualTask.Instructions, "do it")
	}
}

func TestWorkflowVersionStore_Current_MissingIsUnavailable(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	pv, err := s.WorkflowVersionStore().GetCurrentVersion(ctx, 999)
	if err != nil {
		t.Fatalf("GetCurrentVersion(missing): %v", err)
	}
	if pv != nil {
		t.Fatalf("missing category must be unavailable, got %+v", pv)
	}
}

// --- CreateTicketWithRun ---

func TestWorkflowUoW_Create_HappyActiveRun(t *testing.T) {
	s := newTestDB(t)
	cat := seedCategory(t, s, "C1")
	req := seedUser(t, s, "Req", "r@x", true)
	def := domain.WorkflowDefinition{{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "do"}}}
	vid := seedPublished(t, s, cat, def)
	ctx := context.Background()

	in := buildCreateInput(cat, vid, req, def, nil, 0, "active", domain.StateNew, nil)
	uow := newWorkflowUnitOfWork(s.db)
	tk, err := uow.CreateTicketWithRun(ctx, in)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	state, assignee, wid := ticketRow(t, s, tk.ID)
	if state != string(domain.StateNew) {
		t.Errorf("ticket state = %s want new", state)
	}
	if assignee != nil {
		t.Errorf("unassigned ticket got assignee %d", *assignee)
	}
	if wid != vid {
		t.Errorf("pinned version = %d want %d", wid, vid)
	}
	cur, status, comp := runRow(t, s, tk.ID)
	if cur != 0 || status != "active" || comp != nil {
		t.Errorf("run = cur %d status %s completed %v, want 0/active/<nil>", cur, status, comp)
	}
	audits := auditActionOrder(t, s, tk.ID)
	if len(audits) != 1 || audits[0] != "created||" {
		t.Fatalf("audits = %v want [created]", audits)
	}
	// Ticket fields (requester) persisted.
	var rq *int64
	_ = s.db.QueryRow(`SELECT requester_user_id FROM tickets WHERE id=?`, tk.ID).Scan(&rq)
	if rq == nil || *rq != req {
		t.Fatalf("requester_user_id = %v want %d", rq, req)
	}
}

func TestWorkflowUoW_Create_InitialTerminalSuccess(t *testing.T) {
	s := newTestDB(t)
	cat := seedCategory(t, s, "C1")
	req := seedUser(t, s, "Req", "r@x", true)
	def := domain.WorkflowDefinition{{Type: domain.StepResolve}}
	vid := seedPublished(t, s, cat, def)
	ctx := context.Background()
	now := testClock
	tr := &domain.AuditEvent{Actor: "workflow", Action: domain.ActionTransition, Field: ptr("state"), FromValue: ptr("new"), ToValue: ptr("resolved"), CreatedAt: now}
	ops := []application.WorkflowOperation{application.TransitionOperation{StepIndex: 0, Audit: *tr}}
	ct := now
	in := buildCreateInput(cat, vid, req, def, ops, 1, "completed", domain.StateResolved, &ct)
	uow := newWorkflowUnitOfWork(s.db)
	tk, err := uow.CreateTicketWithRun(ctx, in)
	if err != nil {
		t.Fatalf("create terminal: %v", err)
	}
	if tk.State != domain.StateResolved {
		t.Fatalf("ticket state = %s want resolved", tk.State)
	}
	state, _, wid := ticketRow(t, s, tk.ID)
	if state != "resolved" || wid != vid {
		t.Fatalf("ticket row = state %s pin %d, want resolved/%d", state, wid, vid)
	}
	cur, status, comp := runRow(t, s, tk.ID)
	if cur != 1 || status != "completed" || comp == nil {
		t.Fatalf("run = cur %d status %s completed %v, want 1/completed/set", cur, status, comp)
	}
	audits := auditActionOrder(t, s, tk.ID)
	if len(audits) != 2 || audits[0] != "created||" || audits[1] != "transition|new|resolved" {
		t.Fatalf("audits = %v want [created, transition new->resolved]", audits)
	}
	var resolved *string
	_ = s.db.QueryRow(`SELECT resolved_at FROM tickets WHERE id=?`, tk.ID).Scan(&resolved)
	if resolved == nil {
		t.Fatal("resolved_at must be stamped on terminal resolve")
	}
}

func TestWorkflowUoW_Create_VersionChangedRollback(t *testing.T) {
	s := newTestDB(t)
	cat := seedCategory(t, s, "C1")
	req := seedUser(t, s, "Req", "r@x", true)
	def := domain.WorkflowDefinition{{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "do"}}}
	vid := seedPublished(t, s, cat, def)
	// Republish: version 2 becomes current.
	canon, _ := def.MarshalCanonical()
	if _, err := s.db.ExecContext(context.Background(),
		`INSERT INTO workflow_versions (category_id, version_no, steps_json, published_at) VALUES (?, 2, ?, ?)`,
		cat, string(canon), "2026-08-06T10:00:00Z"); err != nil {
		t.Fatalf("insert v2: %v", err)
	}
	var v2 int64
	_ = s.db.QueryRow(`SELECT id FROM workflow_versions WHERE category_id=? AND version_no=2`, cat).Scan(&v2)
	if _, err := s.db.ExecContext(context.Background(), `UPDATE category_workflows SET current_version_id=? WHERE category_id=?`, v2, cat); err != nil {
		t.Fatalf("switch current: %v", err)
	}

	in := buildCreateInput(cat, vid, req, def, nil, 0, "active", domain.StateNew, nil)
	_, err := newWorkflowUnitOfWork(s.db).CreateTicketWithRun(context.Background(), in)
	if !errors.Is(err, domain.ErrWorkflowPositionConflict) {
		t.Fatalf("stale create must be ErrWorkflowPositionConflict, got %v", err)
	}
	assertTotalRollback(t, s)
}

func TestWorkflowUoW_Create_IdentityMismatchRollback(t *testing.T) {
	s := newTestDB(t)
	cat := seedCategory(t, s, "C1")
	req := seedUser(t, s, "Req", "r@x", true)
	def := domain.WorkflowDefinition{{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "do"}}}
	vid := seedPublished(t, s, cat, def)
	ctx := context.Background()

	in := buildCreateInput(cat, vid, req, def, nil, 0, "active", domain.StateNew, nil)
	// Ticket CategoryID disagrees with plan CategoryID.
	bad := in
	bad.Ticket.CategoryID = -1
	if _, err := newWorkflowUnitOfWork(s.db).CreateTicketWithRun(ctx, bad); !errors.Is(err, domain.ErrWorkflowPositionConflict) {
		t.Fatalf("category mismatch must conflict, got %v", err)
	}
	assertTotalRollback(t, s)

	// Ticket pinned to a different version than the plan expects.
	bad2 := buildCreateInput(cat, vid, req, def, nil, 0, "active", domain.StateNew, nil)
	w := vid + 100
	bad2.Ticket.WorkflowVersionID = &w
	if _, err := newWorkflowUnitOfWork(s.db).CreateTicketWithRun(ctx, bad2); !errors.Is(err, domain.ErrWorkflowPositionConflict) {
		t.Fatalf("version mismatch must conflict, got %v", err)
	}
	assertTotalRollback(t, s)
}

func TestWorkflowUoW_Create_LeastLoadedStepMismatchRollback(t *testing.T) {
	// A LeastLoadedAssignmentOperation presented at a NON-least-loaded (manual)
	// step is a plan/step contradiction: since PR6 resolves least_loaded only at
	// an assign_to_desk[least_loaded] pinned step, this is rejected with a typed
	// conflict and total rollback (no invented user/audit/state). The dedicated
	// least_loaded empty-desk rollback is TestWorkflowUoW_LeastLoaded_EmptyDeskRollsBack.
	s := newTestDB(t)
	cat := seedCategory(t, s, "C1")
	req := seedUser(t, s, "Req", "r@x", true)
	def := domain.WorkflowDefinition{{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "do"}}}
	vid := seedPublished(t, s, cat, def)
	ctx := context.Background()
	ops := []application.WorkflowOperation{application.LeastLoadedAssignmentOperation{StepIndex: 0, DeskID: 7}}
	in := buildCreateInput(cat, vid, req, def, ops, 1, "active", domain.StateNew, nil)
	_, err := newWorkflowUnitOfWork(s.db).CreateTicketWithRun(ctx, in)
	var wpc *domain.WorkflowPositionConflictError
	if !errors.As(err, &wpc) {
		t.Fatalf("least_loaded at non-least-loaded step must be a typed conflict, got %v", err)
	}
	assertTotalRollback(t, s)
}

func TestWorkflowUoW_Create_OperationFailureRollback(t *testing.T) {
	s := newTestDB(t)
	cat := seedCategory(t, s, "C1")
	req := seedUser(t, s, "Req", "r@x", true)
	def := domain.WorkflowDefinition{{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "do"}}}
	vid := seedPublished(t, s, cat, def)
	ctx := context.Background()
	// First op writes a form answer (a partial planned write), then a nil op
	// forces the unknown-operation failure AFTER partial writes.
	ops := []application.WorkflowOperation{
		application.FormAnswerOperation{StepIndex: 0, AnswersJSON: []byte(`["x"]`), SubmittedByUserID: req, SubmittedAt: testClock},
		nil,
	}
	in := buildCreateInput(cat, vid, req, def, ops, 1, "active", domain.StateNew, nil)
	if _, err := newWorkflowUnitOfWork(s.db).CreateTicketWithRun(ctx, in); err == nil {
		t.Fatal("malformed operation must fail")
	}
	// The earlier answer write and the ticket/audit/run must all roll back.
	assertTotalRollback(t, s)
}

func TestWorkflowUoW_Create_UserPreconditionRollback(t *testing.T) {
	s := newTestDB(t)
	cat := seedCategory(t, s, "C1")
	inactive := seedUser(t, s, "Dead", "dead@x", false)
	def := domain.WorkflowDefinition{{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "do"}}}
	vid := seedPublished(t, s, cat, def)
	ctx := context.Background()
	// Assignee is inactive -> the fixed plan's precondition fails, total rollback.
	now := testClock
	tk := &domain.Ticket{Title: "T", RequesterName: "Req", RequesterEmail: "r@x", CategoryID: cat, Priority: domain.PriorityMedium, State: domain.StateNew, CreatedAt: now, UpdatedAt: now}
	r := inactive
	tk.UserID = &r
	tk.WorkflowVersionID = &vid
	req := inactive
	tk.RequesterUserID = &req
	in := application.CreateTicketWithRunInput{
		CategoryID: cat, ExpectedVersionID: vid, Workflow: def.Clone(), Ticket: tk,
		CreatedAudit: domain.AuditEvent{Actor: "Dead", ActorUserID: &req, Action: domain.ActionCreated, CreatedAt: now},
		StartedAt:    now, ExpectedCursor: 0, ExpectedRunStatus: "active",
		NextCursor: 0, NextRunStatus: "active", NextTicketState: domain.StateNew,
	}
	if _, err := newWorkflowUnitOfWork(s.db).CreateTicketWithRun(ctx, in); !inactiveUserErr(err) {
		t.Fatalf("inactive assignee must be rejected, got %v", err)
	}
	assertTotalRollback(t, s)
}

func inactiveUserErr(err error) bool {
	var iu *domain.InactiveUserError
	return errors.As(err, &iu)
}

// int64dup copies a *int64 into a fresh allocation (nil-safe identity helpers for
// plan construction).
func int64dup(p *int64) *int64 {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

// buildApplyPlan assembles a concrete behaviorally-matching WorkflowMutationPlan
// for a seeded ticket/run: it pins the expected version, the immutable deep
// snapshot, the CURRENT requester/assignee identities from the ticket, the human
// actor facts, the operations, and the final cursor/status/state/assignee plus an
// optional completion timestamp. Tests override one field to force a specific
// conflict being proven rather than assemble every fact by hand.
func buildApplyPlan(tk domain.Ticket, versionID int64, def domain.WorkflowDefinition, actor int64, actorName string, ops []application.WorkflowOperation, nextCursor int, nextStatus string, nextState domain.State, nextAssignee *int64, completedAt *time.Time) application.WorkflowMutationPlan {
	now := testClock
	p := application.WorkflowMutationPlan{
		TicketID:           tk.ID,
		ExpectedVersionID:  versionID,
		Workflow:           def.Clone(),
		RequesterUserID:    int64dup(tk.RequesterUserID),
		AssigneeUserID:     int64dup(tk.UserID),
		ActorUserID:        actor,
		ActorName:          actorName,
		ExpectedCursor:     0,
		ExpectedRunStatus:  "active",
		TicketBeforeState:  tk.State,
		Operations:         ops,
		NextCursor:         nextCursor,
		NextRunStatus:      nextStatus,
		NextTicketState:    nextState,
		NextAssigneeUserID: int64dup(nextAssignee),
	}
	// A complete, authoritative Result (finding 4): the simulated FINAL ticket and
	// run so no active-plan apply ever passes a nil Result to the validator.
	resTicket := tk
	resTicket.State = nextState
	resTicket.UserID = int64dup(nextAssignee)
	p.Result.Run = &application.WorkflowRun{TicketID: tk.ID, CurrentStepIndex: nextCursor, Status: nextStatus, StartedAt: now, CompletedAt: completedAt}
	p.Result.Ticket = &resTicket
	return p
}

// seedPinnedTicket seeds a ticket AND persists its workflow_version_id pin
// (seedTicket drops the pin column). Required for the Apply immutable-fact
// recheck tests.
func seedPinnedTicket(t *testing.T, s *Store, tk domain.Ticket) domain.Ticket {
	t.Helper()
	tk = seedTicket(t, s, tk)
	if tk.WorkflowVersionID != nil {
		if _, err := s.db.ExecContext(context.Background(), `UPDATE tickets SET workflow_version_id=? WHERE id=?`, *tk.WorkflowVersionID, tk.ID); err != nil {
			t.Fatalf("pin ticket: %v", err)
		}
	}
	return tk
}

// seedDeskWithMember seeds a desk and adds memberID as an active member (the
// actor-specific claim membership precondition, design S5/S6); returns the desk id.
func seedDeskWithMember(t *testing.T, s *Store, memberID int64) int64 {
	t.Helper()
	return seedDeskWithMemberNamed(t, s, memberID, "Desk")
}

func seedDeskWithMemberNamed(t *testing.T, s *Store, memberID int64, name string) int64 {
	t.Helper()
	res, err := s.db.ExecContext(context.Background(), `INSERT INTO desks (name, created_at) VALUES (?, ?)`, name, formatTime(testClock))
	if err != nil {
		t.Fatalf("insert desk: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("desk id: %v", err)
	}
	if _, err := s.db.ExecContext(context.Background(), `INSERT INTO desk_members (desk_id, user_id, created_at) VALUES (?, ?, ?)`, id, memberID, formatTime(testClock)); err != nil {
		t.Fatalf("add member: %v", err)
	}
	return id
}

// assertApplyNoWrites asserts a rejected ApplyWorkflowPlan changed nothing: zero
// audits/answers, run cursor/status unchanged, and ticket state/assignee unchanged.
func assertApplyNoWrites(t *testing.T, s *Store, tk domain.Ticket) {
	t.Helper()
	var audits, answers int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE ticket_id=?`, tk.ID).Scan(&audits)
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM ticket_form_answers WHERE ticket_id=?`, tk.ID).Scan(&answers)
	if audits != 0 || answers != 0 {
		t.Fatalf("rejected apply wrote audits=%d answers=%d, want 0/0", audits, answers)
	}
	cur, status, comp := runRow(t, s, tk.ID)
	if cur != 0 || status != "active" || comp != nil {
		t.Fatalf("rejected apply changed run: cur=%d status=%s comp=%v, want 0/active/<nil>", cur, status, comp)
	}
	state, assignee, _ := ticketRow(t, s, tk.ID)
	if state != string(tk.State) || !sameIntPtr(assignee, tk.UserID) {
		t.Fatalf("rejected apply changed ticket: state=%s assignee=%v, want state=%s assignee=%v", state, assignee, tk.State, tk.UserID)
	}
}

// --- ApplyWorkflowPlan ---

func TestWorkflowUoW_Apply_StaleConflictNoWrites(t *testing.T) {
	s := newTestDB(t)
	cat := seedCategory(t, s, "C1")
	req := seedUser(t, s, "Req", "r@x", true)
	agent := seedUser(t, s, "Ag", "a@x", true)
	_ = agent
	def := domain.WorkflowDefinition{{Type: domain.StepForm, Form: &domain.FormStep{Actor: domain.FormActorRequester, Fields: []domain.FormField{{Key: "k", Label: "K", Kind: domain.FieldShortText}}}}}
	vid := seedPublished(t, s, cat, def)
	now := testClock
	v := vid
	tk := seedPinnedTicket(t, s, domain.Ticket{Number: 5, Title: "T", Description: "", RequesterName: "Req", RequesterEmail: "r@x", RequesterUserID: &req, CategoryID: cat, Priority: domain.PriorityMedium, State: domain.StateNew, UserID: &req, CreatedAt: now, UpdatedAt: now, WorkflowVersionID: &v})
	seedRun(t, s, tk.ID, 0, "active", now)
	ctx := context.Background()
	plan := application.WorkflowMutationPlan{
		TicketID:          tk.ID,
		ExpectedVersionID: vid,
		Workflow:          def.Clone(),
		RequesterUserID:   &req,
		AssigneeUserID:    &req,
		ActorUserID:       req,
		ActorName:         "Req",
		ExpectedCursor:    1, // stale: run is at 0
		ExpectedRunStatus: "active",
		TicketBeforeState: domain.StateNew,
		Operations: []application.WorkflowOperation{
			application.FormAnswerOperation{StepIndex: 1, AnswersJSON: []byte(`["y"]`), SubmittedByUserID: req, SubmittedAt: now},
		},
		NextCursor: 2, NextRunStatus: "active", NextTicketState: domain.StateNew, NextAssigneeUserID: &req,
		Result: application.WorkflowExecutionResult{},
	}
	_, err := newWorkflowUnitOfWork(s.db).ApplyWorkflowPlan(ctx, plan)
	if !errors.Is(err, domain.ErrWorkflowPositionConflict) {
		t.Fatalf("stale cursor must conflict, got %v", err)
	}
	var cur, answers, audits int
	_ = s.db.QueryRow(`SELECT current_step_index FROM ticket_workflow_runs WHERE ticket_id=?`, tk.ID).Scan(&cur)
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM ticket_form_answers WHERE ticket_id=?`, tk.ID).Scan(&answers)
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE ticket_id=?`, tk.ID).Scan(&audits)
	if cur != 0 || answers != 0 || audits != 0 {
		t.Fatalf("stale apply wrote: cursor=%d answers=%d audits=%d, want 0/0/0", cur, answers, audits)
	}
}

func TestWorkflowUoW_Apply_ClaimSuccessAuditFacts(t *testing.T) {
	// A coherent single-request completion of an assign_to_desk CLAIM step: the
	// pinned workflow exactly authorizes the claim + new->in_progress transition
	// + workflow_step audits in literal order, on an unassigned new ticket.
	s := newTestDB(t)
	cat := seedCategory(t, s, "C1")
	req := seedUser(t, s, "Req", "r@x", true)
	agent := seedUser(t, s, "Ag", "a@x", true)
	deskID := seedDeskWithMember(t, s, agent)
	def := claimDef(deskID)
	vid := seedPublished(t, s, cat, def)
	now := testClock
	tk := seedPinnedTicket(t, s, domain.Ticket{Number: 6, Title: "T", Description: "", RequesterName: "Req", RequesterEmail: "r@x", RequesterUserID: &req, CategoryID: cat, Priority: domain.PriorityMedium, State: domain.StateNew, CreatedAt: now, UpdatedAt: now, WorkflowVersionID: &vid})
	seedRun(t, s, tk.ID, 0, "active", now)
	ctx := context.Background()
	plan := buildApplyPlan(tk, vid, def, agent, "Ag", applyClaimOps(t, now, tk, agent, deskID), 1, "active", domain.StateInProgress, &agent, nil)
	res, err := newWorkflowUnitOfWork(s.db).ApplyWorkflowPlan(ctx, plan)
	if err != nil {
		t.Fatalf("claim apply: %v", err)
	}
	_ = res
	state, assignee, _ := ticketRow(t, s, tk.ID)
	if state != "in_progress" {
		t.Errorf("ticket state = %s want in_progress", state)
	}
	if assignee == nil || *assignee != agent {
		t.Errorf("assignee = %v want %d", assignee, agent)
	}
	cur, status, comp := runRow(t, s, tk.ID)
	if cur != 1 || status != "active" || comp != nil {
		t.Errorf("run = cur %d status %s completed %v, want 1/active/<nil>", cur, status, comp)
	}
	// Exact literal audit order and facts: the contextual workflow_assignment
	// row (user from->to, desk context), then the new->in_progress transition.
	wantAudits := []string{"workflow_assignment||" + strconv.FormatInt(agent, 10), "transition|new|in_progress"}
	gotAudits := auditActionOrder(t, s, tk.ID)
	if strings.Join(gotAudits, ";") != strings.Join(wantAudits, ";") {
		t.Fatalf("audit order = %v want %v", gotAudits, wantAudits)
	}
	// The assignment audit persists the exact actor/name/id/time and from/to.
	var actor string
	var actorID *int64
	var f, to string
	var desk *int64
	if err := s.db.QueryRow(`SELECT actor, actor_user_id, COALESCE(from_value,''), COALESCE(to_value,''), desk_id FROM audit_events WHERE ticket_id=? AND action='workflow_assignment'`, tk.ID).Scan(&actor, &actorID, &f, &to, &desk); err != nil {
		t.Fatalf("read assignment audit: %v", err)
	}
	if actor != "Ag" || actorID == nil || *actorID != agent || f != "" || to != strconv.FormatInt(agent, 10) {
		t.Fatalf("assignment audit = actor %q id %v from %q to %q", actor, actorID, f, to)
	}
	if desk == nil || *desk != deskID {
		t.Fatalf("assignment audit desk = %v, want %d (structured desk context)", desk, deskID)
	}
	// The new->in_progress transition audit persists exact workflow actor facts
	// (workflow/NULL id, field state, new->in_progress, no reason/note).
	var trActor string
	var trActorID *int64
	var trField, trFrom, trTo, trReason, trNote string
	if err := s.db.QueryRow(`SELECT actor, actor_user_id, COALESCE(field,''), COALESCE(from_value,''), COALESCE(to_value,''), COALESCE(reason,''), COALESCE(note,'') FROM audit_events WHERE ticket_id=? AND action='transition'`, tk.ID).Scan(&trActor, &trActorID, &trField, &trFrom, &trTo, &trReason, &trNote); err != nil {
		t.Fatalf("read transition audit: %v", err)
	}
	if trActor != "workflow" || trActorID != nil || trField != "state" || trFrom != "new" || trTo != "in_progress" || trReason != "" || trNote != "" {
		t.Fatalf("transition audit = actor %q id %v field %q from %q to %q reason %q note %q", trActor, trActorID, trField, trFrom, trTo, trReason, trNote)
	}
	// A claim emits NO separate generic completion audit: the contextual row is
	// the visible completion.
	var wsCount int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE ticket_id=? AND action IN ('workflow_step','workflow_manual_task','workflow_requester_form','workflow_assignee_form')`, tk.ID).Scan(&wsCount); err != nil {
		t.Fatalf("count completion audits: %v", err)
	}
	if wsCount != 0 {
		t.Fatalf("claim wrote %d generic completion audits, want 0 (contextual row only)", wsCount)
	}
	// Every audit created at the single plan timestamp, in the exact order.
	var times []string
	rows, err := s.db.Query(`SELECT created_at FROM audit_events WHERE ticket_id=? ORDER BY id`, tk.ID)
	if err != nil {
		t.Fatalf("list audit times: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var at string
		if err := rows.Scan(&at); err != nil {
			t.Fatalf("scan audit time: %v", err)
		}
		times = append(times, at)
	}
	if len(times) != 2 {
		t.Fatalf("got %d audit times, want 2", len(times))
	}
	for _, at := range times {
		if at != formatTime(now) {
			t.Fatalf("audit time = %q, want %q", at, formatTime(now))
		}
	}
}

func TestWorkflowUoW_Apply_FormAnswerSuccess(t *testing.T) {
	// A coherent single-request completion of a requester FORM step: the pinned
	// form authorizes the answer typed-array write followed by the workflow_step
	// human audit, cursor ->1, state unchanged.
	s := newTestDB(t)
	cat := seedCategory(t, s, "C1")
	req := seedUser(t, s, "Req", "r@x", true)
	def := formDef()
	vid := seedPublished(t, s, cat, def)
	now := testClock
	tk := seedPinnedTicket(t, s, domain.Ticket{Number: 7, Title: "T", Description: "", RequesterName: "Req", RequesterEmail: "r@x", RequesterUserID: &req, CategoryID: cat, Priority: domain.PriorityMedium, State: domain.StateNew, CreatedAt: now, UpdatedAt: now, WorkflowVersionID: &vid})
	seedRun(t, s, tk.ID, 0, "active", now)
	ctx := context.Background()
	plan := buildApplyPlan(tk, vid, def, req, "A", applyFormOps(now, tk, req, `["spec-value"]`), 1, "active", domain.StateNew, nil, nil)
	if _, err := newWorkflowUnitOfWork(s.db).ApplyWorkflowPlan(ctx, plan); err != nil {
		t.Fatalf("form apply: %v", err)
	}
	var step int
	var answers string
	var by *int64
	if err := s.db.QueryRow(`SELECT step_index, answers_json, submitted_by_user_id FROM ticket_form_answers WHERE ticket_id=?`, tk.ID).Scan(&step, &answers, &by); err != nil {
		t.Fatalf("read answer: %v", err)
	}
	if step != 0 || answers != `["spec-value"]` || by == nil || *by != req {
		t.Fatalf("answer = step %d json %s by %v", step, answers, by)
	}
	gotAudits := auditActionOrder(t, s, tk.ID)
	if len(gotAudits) != 1 || gotAudits[0] != "workflow_requester_form||" {
		t.Fatalf("audit order = %v want [workflow_requester_form||]", gotAudits)
	}
	// The single workflow_step audit persists the exact human actor/id, ticket id,
	// timestamp, and no field/from/to/reason/note.
	var wsActor string
	var wsActorID *int64
	var wsTicket int64
	var wsField *string
	var wsFrom *string
	var wsAt string
	if err := s.db.QueryRow(`SELECT actor, actor_user_id, ticket_id, field, from_value, created_at FROM audit_events WHERE ticket_id=? AND action='workflow_requester_form'`, tk.ID).Scan(&wsActor, &wsActorID, &wsTicket, &wsField, &wsFrom, &wsAt); err != nil {
		t.Fatalf("read requester-form audit: %v", err)
	}
	if wsActor != "A" || wsActorID == nil || *wsActorID != req || wsTicket != tk.ID || wsField != nil || wsFrom != nil || wsAt != formatTime(now) {
		t.Fatalf("workflow_step audit = actor %q id %v ticket %d field %v from %v at %q", wsActor, wsActorID, wsTicket, wsField, wsFrom, wsAt)
	}
	// The answer is persisted before the audit (same timestamp) with the submitter.
	var ansBy *int64
	var ansAt string
	if err := s.db.QueryRow(`SELECT submitted_by_user_id, submitted_at FROM ticket_form_answers WHERE ticket_id=?`, tk.ID).Scan(&ansBy, &ansAt); err != nil {
		t.Fatalf("read answer submitted facts: %v", err)
	}
	if ansBy == nil || *ansBy != req || ansAt != formatTime(now) {
		t.Fatalf("answer submitted_by %v at %q, want %d at %q", ansBy, ansAt, req, formatTime(now))
	}
	if cur, status, _ := runRow(t, s, tk.ID); cur != 1 || status != "active" {
		t.Fatalf("run = cur %d status %s, want 1/active", cur, status)
	}
}

func TestWorkflowUoW_Apply_ManualStepAuditSuccess(t *testing.T) {
	// A coherent single-request completion of a MANUAL step: the workflow_step
	// human audit only, cursor ->1, state/assignee unchanged (manual_task writes
	// no answer).
	s := newTestDB(t)
	cat := seedCategory(t, s, "C1")
	req := seedUser(t, s, "Req", "r@x", true)
	agent := seedUser(t, s, "Ag", "a@x", true)
	def := manualTaskDef()
	vid := seedPublished(t, s, cat, def)
	now := testClock
	v := vid
	tk := seedPinnedTicket(t, s, domain.Ticket{Number: 8, Title: "T", Description: "", RequesterName: "Req", RequesterEmail: "r@x", RequesterUserID: &req, CategoryID: cat, Priority: domain.PriorityMedium, State: domain.StateNew, UserID: &agent, CreatedAt: now, UpdatedAt: now, WorkflowVersionID: &v})
	seedRun(t, s, tk.ID, 0, "active", now)
	ctx := context.Background()
	plan := buildApplyPlan(tk, vid, def, agent, "A", applyManualOps(now, tk, agent), 1, "active", domain.StateNew, &agent, nil)
	if _, err := newWorkflowUnitOfWork(s.db).ApplyWorkflowPlan(ctx, plan); err != nil {
		t.Fatalf("manual apply: %v", err)
	}
	gotAudits := auditActionOrder(t, s, tk.ID)
	if len(gotAudits) != 1 || gotAudits[0] != "workflow_manual_task||" {
		t.Fatalf("audit = %v want [workflow_manual_task||]", gotAudits)
	}
	var actor string
	var actorID *int64
	if err := s.db.QueryRow(`SELECT actor, actor_user_id FROM audit_events WHERE ticket_id=? AND action='workflow_manual_task'`, tk.ID).Scan(&actor, &actorID); err != nil {
		t.Fatalf("read step audit: %v", err)
	}
	if actor != "A" || actorID == nil || *actorID != agent {
		t.Fatalf("step audit actor = %q id %v, want A/%d", actor, actorID, agent)
	}
	if cur, status, _ := runRow(t, s, tk.ID); cur != 1 || status != "active" {
		t.Fatalf("run = cur %d status %s, want 1/active", cur, status)
	}
	state, assignee, _ := ticketRow(t, s, tk.ID)
	if state != "new" || assignee == nil || *assignee != agent {
		t.Fatalf("ticket = state %s assignee %v, want new/%d", state, assignee, agent)
	}
}

func TestWorkflowUoW_Apply_TerminalTransitionSuccess(t *testing.T) {
	// A coherent single-request completion of a terminal RESOLVE step: the
	// exact Ticket.Transition audit, a COMPLETED run with CompletedAt >=
	// StartedAt, and resolved_at stamped.
	s := newTestDB(t)
	cat := seedCategory(t, s, "C1")
	req := seedUser(t, s, "Req", "r@x", true)
	agent := seedUser(t, s, "Ag", "a@x", true)
	def := domain.WorkflowDefinition{{Type: domain.StepResolve}}
	vid := seedPublished(t, s, cat, def)
	started := testClock
	v := vid
	tk := seedPinnedTicket(t, s, domain.Ticket{Number: 9, Title: "T", Description: "", RequesterName: "Req", RequesterEmail: "r@x", RequesterUserID: &req, CategoryID: cat, Priority: domain.PriorityMedium, State: domain.StateNew, UserID: &agent, CreatedAt: started, UpdatedAt: started, WorkflowVersionID: &v})
	seedRun(t, s, tk.ID, 0, "active", started)
	ctx := context.Background()
	tr := domain.AuditEvent{TicketID: tk.ID, Actor: "workflow", Action: domain.ActionTransition, Field: ptr("state"), FromValue: ptr("new"), ToValue: ptr("resolved"), CreatedAt: started}
	ops := []application.WorkflowOperation{application.TransitionOperation{StepIndex: 0, Audit: tr}}
	ct := started
	plan := buildApplyPlan(tk, vid, def, agent, "Ag", ops, 1, "completed", domain.StateResolved, &agent, &ct)
	if _, err := newWorkflowUnitOfWork(s.db).ApplyWorkflowPlan(ctx, plan); err != nil {
		t.Fatalf("terminal apply: %v", err)
	}
	state, _, _ := ticketRow(t, s, tk.ID)
	if state != "resolved" {
		t.Fatalf("ticket state = %s want resolved", state)
	}
	var resolved *string
	if err := s.db.QueryRow(`SELECT resolved_at FROM tickets WHERE id=?`, tk.ID).Scan(&resolved); err != nil {
		t.Fatalf("read resolved_at: %v", err)
	}
	if resolved == nil {
		t.Fatal("resolved_at must be stamped on terminal resolve")
	}
	cur, status, comp := runRow(t, s, tk.ID)
	if cur != 1 || status != "completed" || comp == nil {
		t.Fatalf("run = cur %d status %s completed %v, want 1/completed/set", cur, status, comp)
	}
	gotAudits := auditActionOrder(t, s, tk.ID)
	if len(gotAudits) != 1 || gotAudits[0] != "transition|new|resolved" {
		t.Fatalf("audit = %v want [transition|new|resolved]", gotAudits)
	}
}

// --- ApplyWorkflowPlan: immutable-fact recheck + contradiction rejection ---
// (PR5 Batch B1 gatekeeper corrections: reload+compare before writes, reject
// contradictory duplicated facts, return a refreshed persisted result.)

// applyManualClaimOps is superseded by applyClaimOps (same literal sequence).

// manualTaskDef is a pinned single manual_task step — the exact pinned step that
// authorizes a manual workflow_step completion.
func manualTaskDef() domain.WorkflowDefinition {
	return domain.WorkflowDefinition{{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "do"}}}
}

// formDef is a pinned single requester-form step — the exact pinned step that
// authorizes a requester form answer.
func formDef() domain.WorkflowDefinition {
	return domain.WorkflowDefinition{{Type: domain.StepForm, Form: &domain.FormStep{Actor: domain.FormActorRequester, Fields: []domain.FormField{{Key: "k", Label: "K", Kind: domain.FieldShortText}}}}}
}

// coherentManualPlan builds a FULLY coherent manual_step completion plan: a
// single workflow_step audit at step 0 on the current assignee, cursor ->1,
// state/assignee unchanged. It is used as the coherent base for the pure
// immutable-fact recheck conflicts so the overridden field is the sole defect.
func coherentManualPlan(tk domain.Ticket, def domain.WorkflowDefinition, vid int64, actor int64, now time.Time) application.WorkflowMutationPlan {
	nxt := tk.UserID
	return buildApplyPlan(tk, vid, def, actor, "A", applyManualOps(now, tk, actor), 1, "active", tk.State, nxt, nil)
}

func TestWorkflowUoW_Apply_StalePinnedVersionNoWrites(t *testing.T) {
	s := newTestDB(t)
	cat := seedCategory(t, s, "C1")
	req := seedUser(t, s, "Req", "r@x", true)
	agent := seedUser(t, s, "Ag", "a@x", true)
	def := manualTaskDef()
	vid := seedPublished(t, s, cat, def)
	now := testClock
	tk := seedPinnedTicket(t, s, domain.Ticket{Number: 10, Title: "T", Description: "", RequesterName: "Req", RequesterEmail: "r@x", RequesterUserID: &req, CategoryID: cat, Priority: domain.PriorityMedium, State: domain.StateNew, UserID: &agent, CreatedAt: now, UpdatedAt: now, WorkflowVersionID: &vid})
	seedRun(t, s, tk.ID, 0, "active", now)
	ctx := context.Background()
	plan := coherentManualPlan(tk, def, vid, agent, now)
	plan.ExpectedVersionID = vid + 999 // stale pinned version
	_, err := newWorkflowUnitOfWork(s.db).ApplyWorkflowPlan(ctx, plan)
	if !errors.Is(err, domain.ErrWorkflowPositionConflict) {
		t.Fatalf("stale pinned version must conflict, got %v", err)
	}
	assertApplyNoWrites(t, s, tk)
}

func TestWorkflowUoW_Apply_StaleWorkflowContentNoWrites(t *testing.T) {
	s := newTestDB(t)
	cat := seedCategory(t, s, "C1")
	req := seedUser(t, s, "Req", "r@x", true)
	agent := seedUser(t, s, "Ag", "a@x", true)
	def := manualTaskDef()
	vid := seedPublished(t, s, cat, def)
	now := testClock
	tk := seedPinnedTicket(t, s, domain.Ticket{Number: 11, Title: "T", Description: "", RequesterName: "Req", RequesterEmail: "r@x", RequesterUserID: &req, CategoryID: cat, Priority: domain.PriorityMedium, State: domain.StateNew, UserID: &agent, CreatedAt: now, UpdatedAt: now, WorkflowVersionID: &vid})
	seedRun(t, s, tk.ID, 0, "active", now)
	ctx := context.Background()
	plan := coherentManualPlan(tk, def, vid, agent, now)
	// The plan's snapshot disagrees with the persisted immutable steps_json.
	plan.Workflow = domain.WorkflowDefinition{{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "CORRUPT"}}}
	_, err := newWorkflowUnitOfWork(s.db).ApplyWorkflowPlan(ctx, plan)
	if !errors.Is(err, domain.ErrWorkflowPositionConflict) {
		t.Fatalf("stale workflow content must conflict, got %v", err)
	}
	assertApplyNoWrites(t, s, tk)
}

func TestWorkflowUoW_Apply_RequesterMismatchNoWrites(t *testing.T) {
	s := newTestDB(t)
	cat := seedCategory(t, s, "C1")
	req := seedUser(t, s, "Req", "r@x", true)
	agent := seedUser(t, s, "Ag", "a@x", true)
	def := manualTaskDef()
	vid := seedPublished(t, s, cat, def)
	now := testClock
	tk := seedPinnedTicket(t, s, domain.Ticket{Number: 12, Title: "T", Description: "", RequesterName: "Req", RequesterEmail: "r@x", RequesterUserID: &req, CategoryID: cat, Priority: domain.PriorityMedium, State: domain.StateNew, UserID: &agent, CreatedAt: now, UpdatedAt: now, WorkflowVersionID: &vid})
	seedRun(t, s, tk.ID, 0, "active", now)
	ctx := context.Background()
	plan := coherentManualPlan(tk, def, vid, agent, now)
	plan.RequesterUserID = int64dup(&agent) // contradicts persisted requester
	_, err := newWorkflowUnitOfWork(s.db).ApplyWorkflowPlan(ctx, plan)
	if !errors.Is(err, domain.ErrWorkflowPositionConflict) {
		t.Fatalf("requester mismatch must conflict, got %v", err)
	}
	assertApplyNoWrites(t, s, tk)
}

func TestWorkflowUoW_Apply_AssigneeMismatchNoWrites(t *testing.T) {
	s := newTestDB(t)
	cat := seedCategory(t, s, "C1")
	req := seedUser(t, s, "Req", "r@x", true)
	agent := seedUser(t, s, "Ag", "a@x", true)
	def := manualTaskDef()
	vid := seedPublished(t, s, cat, def)
	now := testClock
	tk := seedPinnedTicket(t, s, domain.Ticket{Number: 13, Title: "T", Description: "", RequesterName: "Req", RequesterEmail: "r@x", RequesterUserID: &req, CategoryID: cat, Priority: domain.PriorityMedium, State: domain.StateNew, UserID: &agent, CreatedAt: now, UpdatedAt: now, WorkflowVersionID: &vid})
	seedRun(t, s, tk.ID, 0, "active", now)
	ctx := context.Background()
	plan := coherentManualPlan(tk, def, vid, agent, now)
	plan.AssigneeUserID = int64dup(&req) // contradicts persisted current assignee
	_, err := newWorkflowUnitOfWork(s.db).ApplyWorkflowPlan(ctx, plan)
	if !errors.Is(err, domain.ErrWorkflowPositionConflict) {
		t.Fatalf("assignee mismatch must conflict, got %v", err)
	}
	assertApplyNoWrites(t, s, tk)
}

func TestWorkflowUoW_Apply_NonMemberClaimantNoWrites(t *testing.T) {
	s := newTestDB(t)
	cat := seedCategory(t, s, "C1")
	req := seedUser(t, s, "Req", "r@x", true)
	member := seedUser(t, s, "Mem", "m@x", true)
	outsider := seedUser(t, s, "Out", "o@x", true) // active agent but NOT a desk member
	deskID := seedDeskWithMember(t, s, member)
	def := claimDef(deskID)
	vid := seedPublished(t, s, cat, def)
	now := testClock
	tk := seedPinnedTicket(t, s, domain.Ticket{Number: 14, Title: "T", Description: "", RequesterName: "Req", RequesterEmail: "r@x", RequesterUserID: &req, CategoryID: cat, Priority: domain.PriorityMedium, State: domain.StateNew, CreatedAt: now, UpdatedAt: now, WorkflowVersionID: &vid})
	seedRun(t, s, tk.ID, 0, "active", now)
	ctx := context.Background()
	// The outsider claims the desk it is not a member of; the pinned claim step
	// authorizes the operation, so only the membership precondition fails.
	field := "user"
	from, to := "", strconv.FormatInt(outsider, 10)
	claimAudit := domain.AuditEvent{TicketID: tk.ID, Actor: "Out", ActorUserID: &outsider, Action: domain.ActionWorkflowAssignment, Field: &field, FromValue: &from, ToValue: &to, DeskID: &deskID, CreatedAt: now}
	ops := []application.WorkflowOperation{
		application.ClaimAssignmentOperation{StepIndex: 0, DeskID: deskID, AssigneeUserID: outsider, AssignmentAudit: claimAudit},
	}
	plan := buildApplyPlan(tk, vid, def, outsider, "Out", ops, 1, "active", domain.StateInProgress, &outsider, nil)
	_, err := newWorkflowUnitOfWork(s.db).ApplyWorkflowPlan(ctx, plan)
	if !errors.Is(err, domain.ErrWorkflowPositionConflict) {
		t.Fatalf("non-member claimant must conflict, got %v", err)
	}
	assertApplyNoWrites(t, s, tk)
}

func TestWorkflowUoW_Apply_ContradictoryNextStateNoWrites(t *testing.T) {
	s := newTestDB(t)
	cat := seedCategory(t, s, "C1")
	req := seedUser(t, s, "Req", "r@x", true)
	agent := seedUser(t, s, "Ag", "a@x", true)
	deskID := seedDeskWithMember(t, s, agent)
	def := claimDef(deskID)
	vid := seedPublished(t, s, cat, def)
	now := testClock
	tk := seedPinnedTicket(t, s, domain.Ticket{Number: 15, Title: "T", Description: "", RequesterName: "Req", RequesterEmail: "r@x", RequesterUserID: &req, CategoryID: cat, Priority: domain.PriorityMedium, State: domain.StateNew, CreatedAt: now, UpdatedAt: now, WorkflowVersionID: &vid})
	seedRun(t, s, tk.ID, 0, "active", now)
	ctx := context.Background()
	// A coherent unassigned claim produces in_progress, but the plan declares new.
	plan := buildApplyPlan(tk, vid, def, agent, "Ag", applyClaimOps(t, now, tk, agent, deskID), 1, "active", domain.StateNew, &agent, nil)
	_, err := newWorkflowUnitOfWork(s.db).ApplyWorkflowPlan(ctx, plan)
	if !errors.Is(err, domain.ErrWorkflowPositionConflict) {
		t.Fatalf("contradictory NextTicketState must conflict, got %v", err)
	}
	assertApplyNoWrites(t, s, tk)
}

func TestWorkflowUoW_Apply_ContradictoryAssignmentAuditNoWrites(t *testing.T) {
	s := newTestDB(t)
	cat := seedCategory(t, s, "C1")
	req := seedUser(t, s, "Req", "r@x", true)
	agent := seedUser(t, s, "Ag", "a@x", true)
	deskID := seedDeskWithMember(t, s, agent)
	def := claimDef(deskID)
	vid := seedPublished(t, s, cat, def)
	now := testClock
	tk := seedPinnedTicket(t, s, domain.Ticket{Number: 16, Title: "T", Description: "", RequesterName: "Req", RequesterEmail: "r@x", RequesterUserID: &req, CategoryID: cat, Priority: domain.PriorityMedium, State: domain.StateNew, CreatedAt: now, UpdatedAt: now, WorkflowVersionID: &vid})
	seedRun(t, s, tk.ID, 0, "active", now)
	ctx := context.Background()
	ops := applyClaimOps(t, now, tk, agent, deskID)
	claim := ops[0].(application.ClaimAssignmentOperation)
	from := "someone-else" // audit FromValue contradicts current (unassigned) assignee
	dup := claim
	dup.AssignmentAudit.FromValue = &from
	ops[0] = dup
	plan := buildApplyPlan(tk, vid, def, agent, "Ag", ops, 1, "active", domain.StateInProgress, &agent, nil)
	_, err := newWorkflowUnitOfWork(s.db).ApplyWorkflowPlan(ctx, plan)
	if !errors.Is(err, domain.ErrWorkflowPositionConflict) {
		t.Fatalf("contradictory assignment audit must conflict, got %v", err)
	}
	assertApplyNoWrites(t, s, tk)
}

func TestWorkflowUoW_Apply_ContradictoryCompletionFactsNoWrites(t *testing.T) {
	s := newTestDB(t)
	cat := seedCategory(t, s, "C1")
	req := seedUser(t, s, "Req", "r@x", true)
	agent := seedUser(t, s, "Ag", "a@x", true)
	deskID := seedDeskWithMember(t, s, agent)
	def := claimDef(deskID)
	vid := seedPublished(t, s, cat, def)
	now := testClock
	tk := seedPinnedTicket(t, s, domain.Ticket{Number: 17, Title: "T", Description: "", RequesterName: "Req", RequesterEmail: "r@x", RequesterUserID: &req, CategoryID: cat, Priority: domain.PriorityMedium, State: domain.StateNew, CreatedAt: now, UpdatedAt: now, WorkflowVersionID: &vid})
	seedRun(t, s, tk.ID, 0, "active", now)
	ctx := context.Background()
	// NextRunStatus active but a completion timestamp is declared -> contradiction.
	ct := now
	plan := buildApplyPlan(tk, vid, def, agent, "Ag", applyClaimOps(t, now, tk, agent, deskID), 1, "active", domain.StateInProgress, &agent, &ct)
	_, err := newWorkflowUnitOfWork(s.db).ApplyWorkflowPlan(ctx, plan)
	if !errors.Is(err, domain.ErrWorkflowPositionConflict) {
		t.Fatalf("contradictory completion facts must conflict, got %v", err)
	}
	assertApplyNoWrites(t, s, tk)
}

func TestWorkflowUoW_Apply_ReturnsRefreshedPersistedResult(t *testing.T) {
	s := newTestDB(t)
	cat := seedCategory(t, s, "C1")
	req := seedUser(t, s, "Req", "r@x", true)
	agent := seedUser(t, s, "Ag", "a@x", true)
	deskID := seedDeskWithMember(t, s, agent)
	def := claimDef(deskID)
	vid := seedPublished(t, s, cat, def)
	now := testClock
	tk := seedPinnedTicket(t, s, domain.Ticket{Number: 18, Title: "T", Description: "", RequesterName: "Req", RequesterEmail: "r@x", RequesterUserID: &req, CategoryID: cat, Priority: domain.PriorityMedium, State: domain.StateNew, CreatedAt: now, UpdatedAt: now, WorkflowVersionID: &vid})
	seedRun(t, s, tk.ID, 0, "active", now)
	ctx := context.Background()
	plan := buildApplyPlan(tk, vid, def, agent, "Ag", applyClaimOps(t, now, tk, agent, deskID), 1, "active", domain.StateInProgress, &agent, nil)
	res, err := newWorkflowUnitOfWork(s.db).ApplyWorkflowPlan(ctx, plan)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	// The result is REFRESHED from persisted state, not the caller-provided empty Result.
	if res.Ticket == nil || res.Ticket.State != domain.StateInProgress {
		t.Fatalf("refreshed result ticket = %+v, want persisted State in_progress", res.Ticket)
	}
	if res.Run == nil || res.Run.CurrentStepIndex != 1 || res.Run.Status != "active" {
		t.Fatalf("refreshed result run = %+v, want cursor 1 status active", res.Run)
	}
	state, assignee, _ := ticketRow(t, s, tk.ID)
	if state != "in_progress" || assignee == nil || *assignee != agent {
		t.Fatalf("persisted ticket = state %s assignee %v, want in_progress/%d", state, assignee, agent)
	}
}

func TestWorkflowVersionStore_Current_InvalidDefinitionErrors(t *testing.T) {
	s := newTestDB(t)
	cat := seedCategory(t, s, "C1")
	ctx := context.Background()
	// Insert a JSON-valid but domain-INVALID persisted definition (unknown step
	// type): the DB json check allows it, but domain validation must reject it
	// and it must never escape as a usable current workflow.
	if _, err := s.db.ExecContext(ctx, `INSERT INTO workflow_versions (category_id, version_no, steps_json, published_at) VALUES (?, 1, ?, ?)`, cat, `[{"type":"bogus"}]`, "2026-08-06T10:00:00Z"); err != nil {
		t.Fatalf("insert invalid version: %v", err)
	}
	var vid int64
	_ = s.db.QueryRow(`SELECT id FROM workflow_versions WHERE category_id=? AND version_no=1`, cat).Scan(&vid)
	if _, err := s.db.ExecContext(ctx, `INSERT INTO category_workflows (category_id, draft_json, current_version_id) VALUES (?, '[]', ?) ON CONFLICT(category_id) DO UPDATE SET current_version_id=excluded.current_version_id`, cat, vid); err != nil {
		t.Fatalf("point current: %v", err)
	}
	pv, err := s.WorkflowVersionStore().GetCurrentVersion(ctx, cat)
	if err == nil {
		t.Fatalf("invalid persisted definition must error, got %+v", pv)
	}
}

func TestWorkflowUoW_Apply_InvalidPinnedDefinitionErrors(t *testing.T) {
	s := newTestDB(t)
	cat := seedCategory(t, s, "C1")
	req := seedUser(t, s, "Req", "r@x", true)
	agent := seedUser(t, s, "Ag", "a@x", true)
	ctx := context.Background()
	// Pin a ticket to a version whose immutable steps_json is domain-invalid.
	if _, err := s.db.ExecContext(ctx, `INSERT INTO workflow_versions (category_id, version_no, steps_json, published_at) VALUES (?, 1, ?, ?)`, cat, `[{"type":"bogus"}]`, "2026-08-06T10:00:00Z"); err != nil {
		t.Fatalf("insert invalid version: %v", err)
	}
	var vid int64
	_ = s.db.QueryRow(`SELECT id FROM workflow_versions WHERE category_id=? AND version_no=1`, cat).Scan(&vid)
	now := testClock
	tk := seedPinnedTicket(t, s, domain.Ticket{Number: 20, Title: "T", Description: "", RequesterName: "Req", RequesterEmail: "r@x", RequesterUserID: &req, CategoryID: cat, Priority: domain.PriorityMedium, State: domain.StateNew, UserID: &agent, CreatedAt: now, UpdatedAt: now, WorkflowVersionID: &vid})
	seedRun(t, s, tk.ID, 0, "active", now)
	def := domain.WorkflowDefinition{{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "do"}}}
	plan := buildApplyPlan(tk, vid, def, agent, "Ag", nil, 0, "active", domain.StateNew, &agent, nil)
	_, err := newWorkflowUnitOfWork(s.db).ApplyWorkflowPlan(ctx, plan)
	if err == nil {
		t.Fatal("apply on an invalid pinned definition must error, not succeed silently")
	}
	if errors.Is(err, domain.ErrWorkflowPositionConflict) {
		t.Fatalf("invalid persisted definition is a data/validation error, not a position conflict: %v", err)
	}
	assertApplyNoWrites(t, s, tk)
}

// ---- PR5 SQLite SEMANTIC CORRECTION: strict operation consistency ----

// claimDef builds a pinned workflow whose single step is an assign_to_desk
// claim on deskID — the exact pinned step that authorizes a claim operation.
func claimDef(deskID int64) domain.WorkflowDefinition {
	return domain.WorkflowDefinition{{Type: domain.StepAssignToDesk, AssignToDesk: &domain.AssignToDeskStep{DeskID: deskID, Strategy: domain.StrategyClaim}}}
}

// applyClaimOps returns the coherent claim-operation group for a claim step:
// [contextual claim assignment, new->in_progress transition], all at step 0.
// This is the literal sequence the runner plans for a claim step — the visible
// completion IS the contextual assignment row, so no workflow_step op exists.
func applyClaimOps(t *testing.T, now time.Time, tk domain.Ticket, agent int64, deskID int64) []application.WorkflowOperation {
	t.Helper()
	field := "user"
	from := ""
	to := strconv.FormatInt(agent, 10)
	claimAudit := domain.AuditEvent{TicketID: tk.ID, Actor: "Ag", ActorUserID: &agent, Action: domain.ActionWorkflowAssignment, Field: &field, FromValue: &from, ToValue: &to, DeskID: &deskID, CreatedAt: now}
	tr := &domain.AuditEvent{TicketID: tk.ID, Actor: "workflow", Action: domain.ActionTransition, Field: ptr("state"), FromValue: ptr("new"), ToValue: ptr("in_progress"), CreatedAt: now}
	return []application.WorkflowOperation{
		application.ClaimAssignmentOperation{StepIndex: 0, DeskID: deskID, AssigneeUserID: agent, AssignmentAudit: claimAudit},
		application.TransitionOperation{StepIndex: 0, Audit: *tr},
	}
}

// applyFormOps returns the coherent form-operation group: [form answer,
// workflow_step audit] at step 0.
func applyFormOps(now time.Time, tk domain.Ticket, actor int64, answers string) []application.WorkflowOperation {
	ws := domain.AuditEvent{TicketID: tk.ID, Actor: "A", ActorUserID: &actor, Action: domain.ActionWorkflowRequesterForm, CreatedAt: now}
	return []application.WorkflowOperation{
		application.FormAnswerOperation{StepIndex: 0, AnswersJSON: []byte(answers), SubmittedByUserID: actor, SubmittedAt: now},
		application.WorkflowStepOperation{StepIndex: 0, Audit: ws},
	}
}

// applyManualOps returns the coherent manual-step operation group: a single
// workflow_step audit at step 0 (manual_task completes with no answer/write).
func applyManualOps(now time.Time, tk domain.Ticket, actor int64) []application.WorkflowOperation {
	ws := domain.AuditEvent{TicketID: tk.ID, Actor: "A", ActorUserID: &actor, Action: domain.ActionWorkflowManualTask, CreatedAt: now}
	return []application.WorkflowOperation{application.WorkflowStepOperation{StepIndex: 0, Audit: ws}}
}

func TestWorkflowUoW_Apply_StepTypeMismatchConflict(t *testing.T) {
	// A claim operation MUST be corroborated by a pinned assign_to_desk claim
	// step at that StepIndex. Here the pinned step is manual_task, so the claim
	// is a self-fulfilling contradiction the recheck must reject.
	s := newTestDB(t)
	cat := seedCategory(t, s, "C1")
	req := seedUser(t, s, "Req", "r@x", true)
	agent := seedUser(t, s, "Ag", "a@x", true)
	def := domain.WorkflowDefinition{{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "do"}}}
	vid := seedPublished(t, s, cat, def)
	deskID := seedDeskWithMember(t, s, agent)
	now := testClock
	tk := seedPinnedTicket(t, s, domain.Ticket{Number: 30, Title: "T", Description: "", RequesterName: "Req", RequesterEmail: "r@x", RequesterUserID: &req, CategoryID: cat, Priority: domain.PriorityMedium, State: domain.StateNew, CreatedAt: now, UpdatedAt: now, WorkflowVersionID: &vid})
	seedRun(t, s, tk.ID, 0, "active", now)
	ctx := context.Background()
	ops := applyClaimOps(t, now, tk, agent, deskID)
	plan := buildApplyPlan(tk, vid, def, agent, "Ag", ops, 1, "active", domain.StateInProgress, &agent, nil)
	_, err := newWorkflowUnitOfWork(s.db).ApplyWorkflowPlan(ctx, plan)
	if !errors.Is(err, domain.ErrWorkflowPositionConflict) {
		t.Fatalf("claim at non-claim step must conflict (self-fulfilling def), got %v", err)
	}
	assertApplyNoWrites(t, s, tk)
}

func TestWorkflowUoW_Apply_ClaimDeskMismatchConflict(t *testing.T) {
	// The claim op must name the SAME desk as the pinned assign_to_desk claim step.
	s := newTestDB(t)
	cat := seedCategory(t, s, "C1")
	req := seedUser(t, s, "Req", "r@x", true)
	agent := seedUser(t, s, "Ag", "a@x", true)
	deskID := seedDeskWithMember(t, s, agent)
	otherDesk := seedDeskWithMemberNamed(t, s, agent, "OtherDesk")
	def := claimDef(deskID)
	vid := seedPublished(t, s, cat, def)
	now := testClock
	tk := seedPinnedTicket(t, s, domain.Ticket{Number: 31, Title: "T", Description: "", RequesterName: "Req", RequesterEmail: "r@x", RequesterUserID: &req, CategoryID: cat, Priority: domain.PriorityMedium, State: domain.StateNew, CreatedAt: now, UpdatedAt: now, WorkflowVersionID: &vid})
	seedRun(t, s, tk.ID, 0, "active", now)
	ctx := context.Background()
	// Claim names a different desk than the pinned step's desk.
	field := "user"
	from, to := "", strconv.FormatInt(agent, 10)
	claimAudit := domain.AuditEvent{TicketID: tk.ID, Actor: "Ag", ActorUserID: &agent, Action: domain.ActionWorkflowAssignment, Field: &field, FromValue: &from, ToValue: &to, DeskID: &otherDesk, CreatedAt: now}
	ops := []application.WorkflowOperation{
		application.ClaimAssignmentOperation{StepIndex: 0, DeskID: otherDesk, AssigneeUserID: agent, AssignmentAudit: claimAudit},
		application.TransitionOperation{StepIndex: 0, Audit: domain.AuditEvent{TicketID: tk.ID, Actor: "workflow", Action: domain.ActionTransition, Field: ptr("state"), FromValue: ptr("new"), ToValue: ptr("in_progress"), CreatedAt: now}},
	}
	plan := buildApplyPlan(tk, vid, def, agent, "Ag", ops, 1, "active", domain.StateInProgress, &agent, nil)
	_, err := newWorkflowUnitOfWork(s.db).ApplyWorkflowPlan(ctx, plan)
	if !errors.Is(err, domain.ErrWorkflowPositionConflict) {
		t.Fatalf("claim desk mismatch must conflict, got %v", err)
	}
	assertApplyNoWrites(t, s, tk)
}

func TestWorkflowUoW_Apply_ClaimReasonMismatchConflict(t *testing.T) {
	// A reassignment claim requires a reason and its trimmed Reason must equal
	// the audit Reason exactly.
	s := newTestDB(t)
	cat := seedCategory(t, s, "C1")
	req := seedUser(t, s, "Req", "r@x", true)
	prev := seedUser(t, s, "Prev", "p@x", true)
	agent := seedUser(t, s, "Ag", "a@x", true)
	def := claimDefFunc(seedDeskWithMember(t, s, agent))
	deskID := def[0].AssignToDesk.DeskID
	vid := seedPublished(t, s, cat, def)
	now := testClock
	v := vid
	prevPtr := prev
	agentPtr := agent
	tk := seedPinnedTicket(t, s, domain.Ticket{Number: 32, Title: "T", Description: "", RequesterName: "Req", RequesterEmail: "r@x", RequesterUserID: &req, CategoryID: cat, Priority: domain.PriorityMedium, State: domain.StateNew, CreatedAt: now, UpdatedAt: now, WorkflowVersionID: &v, UserID: &prevPtr})
	seedRun(t, s, tk.ID, 0, "active", now)
	ctx := context.Background()
	field := "user"
	from, to := strconv.FormatInt(prev, 10), strconv.FormatInt(agent, 10)
	trimmed := "hand-off"
	claimAudit := domain.AuditEvent{TicketID: tk.ID, Actor: "Ag", ActorUserID: &agent, Action: domain.ActionWorkflowAssignment, Field: &field, FromValue: &from, ToValue: &to, DeskID: &deskID, Reason: &trimmed, CreatedAt: now}
	ops := []application.WorkflowOperation{
		application.ClaimAssignmentOperation{StepIndex: 0, DeskID: deskID, AssigneeUserID: agent, AssignmentAudit: claimAudit},
		application.TransitionOperation{StepIndex: 0, Audit: domain.AuditEvent{TicketID: tk.ID, Actor: "workflow", Action: domain.ActionTransition, Field: ptr("state"), FromValue: ptr("new"), ToValue: ptr("in_progress"), CreatedAt: now}},
	}
	plan := buildApplyPlan(tk, vid, def, agent, "Ag", ops, 1, "active", domain.StateInProgress, &agentPtr, nil)
	_, err := newWorkflowUnitOfWork(s.db).ApplyWorkflowPlan(ctx, plan)
	if !errors.Is(err, domain.ErrWorkflowPositionConflict) {
		t.Fatalf("claim reason/audit mismatch must conflict, got %v", err)
	}
	assertApplyNoWrites(t, s, tk)
}

func claimDefFunc(deskID int64) domain.WorkflowDefinition {
	return domain.WorkflowDefinition{{Type: domain.StepAssignToDesk, AssignToDesk: &domain.AssignToDeskStep{DeskID: deskID, Strategy: domain.StrategyClaim}}}
}

func TestWorkflowUoW_Apply_TransitionActorUserIDNilConflict(t *testing.T) {
	// A workflow transition audit must stamp actor "workflow" with a NULL actor
	// user id (audit-log spec); a non-nil id is a fabricated fact to reject.
	s := newTestDB(t)
	cat := seedCategory(t, s, "C1")
	req := seedUser(t, s, "Req", "r@x", true)
	agent := seedUser(t, s, "Ag", "a@x", true)
	deskID := seedDeskWithMember(t, s, agent)
	def := claimDef(deskID)
	vid := seedPublished(t, s, cat, def)
	now := testClock
	tk := seedPinnedTicket(t, s, domain.Ticket{Number: 33, Title: "T", Description: "", RequesterName: "Req", RequesterEmail: "r@x", RequesterUserID: &req, CategoryID: cat, Priority: domain.PriorityMedium, State: domain.StateNew, CreatedAt: now, UpdatedAt: now, WorkflowVersionID: &vid})
	seedRun(t, s, tk.ID, 0, "active", now)
	ctx := context.Background()
	ops := applyClaimOps(t, now, tk, agent, deskID)
	tr := ops[1].(application.TransitionOperation)
	tr.Audit.ActorUserID = &req // workflow transition must have NULL actor user id
	ops[1] = tr
	plan := buildApplyPlan(tk, vid, def, agent, "Ag", ops, 1, "active", domain.StateInProgress, &agent, nil)
	_, err := newWorkflowUnitOfWork(s.db).ApplyWorkflowPlan(ctx, plan)
	if !errors.Is(err, domain.ErrWorkflowPositionConflict) {
		t.Fatalf("transition with non-nil actor user id must conflict, got %v", err)
	}
	assertApplyNoWrites(t, s, tk)
}

func TestWorkflowUoW_Apply_WorkflowStepAtTerminalConflict(t *testing.T) {
	// A workflow_step human audit must not be fabricated at a terminal step
	// (resolve/close have no human completion audit).
	s := newTestDB(t)
	cat := seedCategory(t, s, "C1")
	req := seedUser(t, s, "Req", "r@x", true)
	agent := seedUser(t, s, "Ag", "a@x", true)
	def := domain.WorkflowDefinition{{Type: domain.StepResolve}}
	vid := seedPublished(t, s, cat, def)
	now := testClock
	tk := seedPinnedTicket(t, s, domain.Ticket{Number: 34, Title: "T", Description: "", RequesterName: "Req", RequesterEmail: "r@x", RequesterUserID: &req, CategoryID: cat, Priority: domain.PriorityMedium, State: domain.StateNew, UserID: &agent, CreatedAt: now, UpdatedAt: now, WorkflowVersionID: &vid})
	seedRun(t, s, tk.ID, 0, "active", now)
	ctx := context.Background()
	ops := []application.WorkflowOperation{
		application.TransitionOperation{StepIndex: 0, Audit: domain.AuditEvent{TicketID: tk.ID, Actor: "workflow", Action: domain.ActionTransition, Field: ptr("state"), FromValue: ptr("new"), ToValue: ptr("resolved"), CreatedAt: now}},
		application.WorkflowStepOperation{StepIndex: 0, Audit: domain.AuditEvent{TicketID: tk.ID, Actor: "Ag", ActorUserID: &agent, Action: domain.ActionWorkflowStep, CreatedAt: now}},
	}
	ct := now
	plan := buildApplyPlan(tk, vid, def, agent, "Ag", ops, 1, "completed", domain.StateResolved, &agent, &ct)
	_, err := newWorkflowUnitOfWork(s.db).ApplyWorkflowPlan(ctx, plan)
	if !errors.Is(err, domain.ErrWorkflowPositionConflict) {
		t.Fatalf("workflow_step audit at terminal step must conflict, got %v", err)
	}
	assertApplyNoWrites(t, s, tk)
}

func TestWorkflowUoW_Apply_WrongAuditTicketIDConflict(t *testing.T) {
	// Every plan audit must reference the exact ticket id; a nonzero wrong id
	// is a fabricated fact to reject.
	s := newTestDB(t)
	cat := seedCategory(t, s, "C1")
	req := seedUser(t, s, "Req", "r@x", true)
	agent := seedUser(t, s, "Ag", "a@x", true)
	deskID := seedDeskWithMember(t, s, agent)
	def := claimDef(deskID)
	vid := seedPublished(t, s, cat, def)
	now := testClock
	tk := seedPinnedTicket(t, s, domain.Ticket{Number: 35, Title: "T", Description: "", RequesterName: "Req", RequesterEmail: "r@x", RequesterUserID: &req, CategoryID: cat, Priority: domain.PriorityMedium, State: domain.StateNew, CreatedAt: now, UpdatedAt: now, WorkflowVersionID: &vid})
	seedRun(t, s, tk.ID, 0, "active", now)
	ctx := context.Background()
	ops := applyClaimOps(t, now, tk, agent, deskID)
	tr := ops[1].(application.TransitionOperation)
	tr.Audit.TicketID = tk.ID + 999 // wrong ticket id
	ops[1] = tr
	plan := buildApplyPlan(tk, vid, def, agent, "Ag", ops, 1, "active", domain.StateInProgress, &agent, nil)
	_, err := newWorkflowUnitOfWork(s.db).ApplyWorkflowPlan(ctx, plan)
	if !errors.Is(err, domain.ErrWorkflowPositionConflict) {
		t.Fatalf("wrong audit ticket id must conflict, got %v", err)
	}
	assertApplyNoWrites(t, s, tk)
}

func TestWorkflowUoW_Apply_FormAnswerActorMismatchConflict(t *testing.T) {
	// A requester-form step must be answered by the requester; a non-requester
	// submitter is a fabricated fact to reject, even when it is the plan actor.
	s := newTestDB(t)
	cat := seedCategory(t, s, "C1")
	req := seedUser(t, s, "Req", "r@x", true)
	agent := seedUser(t, s, "Ag", "a@x", true)
	def := domain.WorkflowDefinition{{Type: domain.StepForm, Form: &domain.FormStep{Actor: domain.FormActorRequester, Fields: []domain.FormField{{Key: "k", Label: "K", Kind: domain.FieldShortText}}}}}
	vid := seedPublished(t, s, cat, def)
	now := testClock
	tk := seedPinnedTicket(t, s, domain.Ticket{Number: 36, Title: "T", Description: "", RequesterName: "Req", RequesterEmail: "r@x", RequesterUserID: &req, CategoryID: cat, Priority: domain.PriorityMedium, State: domain.StateNew, CreatedAt: now, UpdatedAt: now, WorkflowVersionID: &vid})
	seedRun(t, s, tk.ID, 0, "active", now)
	ctx := context.Background()
	// The agent answers the requester-only form -> actor mismatch vs pinned schema.
	ops := applyFormOps(now, tk, agent, `["x"]`)
	plan := buildApplyPlan(tk, vid, def, agent, "A", ops, 1, "active", domain.StateNew, nil, nil)
	_, err := newWorkflowUnitOfWork(s.db).ApplyWorkflowPlan(ctx, plan)
	if !errors.Is(err, domain.ErrWorkflowPositionConflict) {
		t.Fatalf("form answer by wrong pinned actor must conflict, got %v", err)
	}
	assertApplyNoWrites(t, s, tk)
}

func TestWorkflowUoW_Apply_CompletedBeforeStartedConflict(t *testing.T) {
	// A completed run must not declare completion before the run started.
	s := newTestDB(t)
	cat := seedCategory(t, s, "C1")
	req := seedUser(t, s, "Req", "r@x", true)
	agent := seedUser(t, s, "Ag", "a@x", true)
	def := domain.WorkflowDefinition{{Type: domain.StepResolve}}
	vid := seedPublished(t, s, cat, def)
	started := testClock
	tk := seedPinnedTicket(t, s, domain.Ticket{Number: 37, Title: "T", Description: "", RequesterName: "Req", RequesterEmail: "r@x", RequesterUserID: &req, CategoryID: cat, Priority: domain.PriorityMedium, State: domain.StateNew, UserID: &agent, CreatedAt: started, UpdatedAt: started, WorkflowVersionID: &vid})
	seedRun(t, s, tk.ID, 0, "active", started)
	ctx := context.Background()
	before := started.Add(-time.Hour) // completion before start
	ops := []application.WorkflowOperation{
		application.TransitionOperation{StepIndex: 0, Audit: domain.AuditEvent{TicketID: tk.ID, Actor: "workflow", Action: domain.ActionTransition, Field: ptr("state"), FromValue: ptr("new"), ToValue: ptr("resolved"), CreatedAt: before}},
	}
	plan := buildApplyPlan(tk, vid, def, agent, "Ag", ops, 1, "completed", domain.StateResolved, &agent, &before)
	_, err := newWorkflowUnitOfWork(s.db).ApplyWorkflowPlan(ctx, plan)
	if !errors.Is(err, domain.ErrWorkflowPositionConflict) {
		t.Fatalf("completion before run start must conflict, got %v", err)
	}
	assertApplyNoWrites(t, s, tk)
}

// TestWorkflowUoW_Apply_UserPreconditionConflicts proves the Apply recheck maps
// missing/inactive/wrong-role requester, assignee, and actor to a TYPED
// ErrWorkflowPositionConflict with zero writes — never a NotFound/Inactive/
// Validation error. The base plan is a coherent manual_step completion (single
// workflow_step audit) so each override isolates exactly one precondition.
func TestWorkflowUoW_Apply_UserPreconditionConflicts(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(s *Store, tk *domain.Ticket, p *application.WorkflowMutationPlan) (int64, time.Time)
	}{
		{"missing-requester", func(s *Store, tk *domain.Ticket, p *application.WorkflowMutationPlan) (int64, time.Time) {
			s.db.Exec(`PRAGMA foreign_keys=OFF`)
			req := int64(8801)
			tk.RequesterUserID = &req
			_, _ = s.db.Exec(`UPDATE tickets SET requester_user_id=? WHERE id=?`, req, tk.ID)
			s.db.Exec(`PRAGMA foreign_keys=ON`)
			p.RequesterUserID = &req
			return 0, time.Time{}
		}},
		{"inactive-requester", func(s *Store, tk *domain.Ticket, p *application.WorkflowMutationPlan) (int64, time.Time) {
			u := seedUser(t, s, "DeadR", "deadr@x", false)
			tk.RequesterUserID = &u
			_, _ = s.db.Exec(`UPDATE tickets SET requester_user_id=? WHERE id=?`, u, tk.ID)
			p.RequesterUserID = &u
			return 0, time.Time{}
		}},
		{"missing-assignee", func(s *Store, tk *domain.Ticket, p *application.WorkflowMutationPlan) (int64, time.Time) {
			s.db.Exec(`PRAGMA foreign_keys=OFF`)
			a := int64(8802)
			tk.UserID = &a
			_, _ = s.db.Exec(`UPDATE tickets SET user_id=? WHERE id=?`, a, tk.ID)
			s.db.Exec(`PRAGMA foreign_keys=ON`)
			p.AssigneeUserID = &a
			return 0, time.Time{}
		}},
		{"inactive-assignee", func(s *Store, tk *domain.Ticket, p *application.WorkflowMutationPlan) (int64, time.Time) {
			u := seedUser(t, s, "DeadA", "deada@x", false)
			tk.UserID = &u
			_, _ = s.db.Exec(`UPDATE tickets SET user_id=? WHERE id=?`, u, tk.ID)
			p.AssigneeUserID = &u
			return 0, time.Time{}
		}},
		{"wrong-role-assignee", func(s *Store, tk *domain.Ticket, p *application.WorkflowMutationPlan) (int64, time.Time) {
			u := seedUserRaw(t, s, "Plain", "plain@x", "user")
			tk.UserID = &u
			_, _ = s.db.Exec(`UPDATE tickets SET user_id=? WHERE id=?`, u, tk.ID)
			p.AssigneeUserID = &u
			return 0, time.Time{}
		}},
		{"missing-actor", func(s *Store, tk *domain.Ticket, p *application.WorkflowMutationPlan) (int64, time.Time) {
			return 8803, time.Time{}
		}},
		{"inactive-actor", func(s *Store, tk *domain.Ticket, p *application.WorkflowMutationPlan) (int64, time.Time) {
			u := seedUser(t, s, "DeadAct", "deadact@x", false)
			return u, time.Time{}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestDB(t)
			cat := seedCategory(t, s, "C1")
			req := seedUser(t, s, "Req", "r@x", true)
			agent := seedUser(t, s, "Ag", "a@x", true)
			def := domain.WorkflowDefinition{{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "do"}}}
			vid := seedPublished(t, s, cat, def)
			now := testClock
			tk := seedPinnedTicket(t, s, domain.Ticket{Number: 90, Title: "T", Description: "", RequesterName: "Req", RequesterEmail: "r@x", RequesterUserID: &req, CategoryID: cat, Priority: domain.PriorityMedium, State: domain.StateNew, UserID: &agent, CreatedAt: now, UpdatedAt: now, WorkflowVersionID: &vid})
			seedRun(t, s, tk.ID, 0, "active", now)
			tk.UserID = &agent // default coherent current assignee
			p := buildApplyPlan(tk, vid, def, agent, "A", applyManualOps(now, tk, agent), 1, "active", domain.StateNew, &agent, nil)
			_ = req
			if overrideActor, _ := tc.mutate(s, &tk, &p); overrideActor != 0 {
				p.ActorUserID = overrideActor
				p.ActorName = "A"
				ws := p.Operations[0].(application.WorkflowStepOperation)
				ws.Audit.ActorUserID = &overrideActor
				ws.Audit.Actor = "A"
				p.Operations[0] = ws
			}
			_, err := newWorkflowUnitOfWork(s.db).ApplyWorkflowPlan(ctxForTest(), p)
			if !errors.Is(err, domain.ErrWorkflowPositionConflict) {
				t.Fatalf("%s must map to typed ErrWorkflowPositionConflict, got %v", tc.name, err)
			}
			var audits, answers int
			_ = s.db.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE ticket_id=?`, tk.ID).Scan(&audits)
			_ = s.db.QueryRow(`SELECT COUNT(*) FROM ticket_form_answers WHERE ticket_id=?`, tk.ID).Scan(&answers)
			if audits != 0 || answers != 0 {
				t.Fatalf("%s wrote audits=%d answers=%d, want zero writes", tc.name, audits, answers)
			}
		})
	}
}

func ctxForTest() context.Context { return context.Background() }

// TestWorkflowUoW_Apply_LateCursorCASRollback proves a TRUE late cursor CAS
// failure: a test-scoped SQLite trigger changes the run cursor AFTER the
// pre-write recheck but BEFORE applyCursorCAS, so the CAS matches 0 rows after
// earlier operation writes. The whole transaction (earlier audits) must roll
// back with a typed ErrWorkflowPositionConflict and the connection must stay
// usable afterward.
func TestWorkflowUoW_Apply_LateCursorCASRollback(t *testing.T) {
	s := newTestDB(t)
	cat := seedCategory(t, s, "C1")
	req := seedUser(t, s, "Req", "r@x", true)
	agent := seedUser(t, s, "Ag", "a@x", true)
	deskID := seedDeskWithMember(t, s, agent)
	def := claimDef(deskID)
	vid := seedPublished(t, s, cat, def)
	now := testClock
	tk := seedPinnedTicket(t, s, domain.Ticket{Number: 40, Title: "T", Description: "", RequesterName: "Req", RequesterEmail: "r@x", RequesterUserID: &req, CategoryID: cat, Priority: domain.PriorityMedium, State: domain.StateNew, CreatedAt: now, UpdatedAt: now, WorkflowVersionID: &vid})
	seedRun(t, s, tk.ID, 0, "active", now)
	// Test-scoped schema action:
	// the apply's earlier claim/transition/workflow_step audits move the cursor
	// past the CAS's expected position, forcing RowsAffected=0 after writes.
	if _, err := s.db.Exec(`CREATE TRIGGER trg_cas_bump AFTER INSERT ON audit_events
		BEGIN UPDATE ticket_workflow_runs SET current_step_index = current_step_index + 1000 WHERE ticket_id = NEW.ticket_id; END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}
	ctx := context.Background()
	ops := applyClaimOps(t, now, tk, agent, deskID)
	plan := buildApplyPlan(tk, vid, def, agent, "Ag", ops, 1, "active", domain.StateInProgress, &agent, nil)
	_, err := newWorkflowUnitOfWork(s.db).ApplyWorkflowPlan(ctx, plan)
	if !errors.Is(err, domain.ErrWorkflowPositionConflict) {
		t.Fatalf("late cursor CAS must map to typed conflict, got %v", err)
	}
	// Every earlier audit write must have rolled back.
	var audits int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE ticket_id=?`, tk.ID).Scan(&audits)
	if audits != 0 {
		t.Fatalf("late CAS left %d audits, want 0 (total rollback)", audits)
	}
	// The run cursor must be back at 0 (the trigger's bump also rolled back).
	if cur, status, _ := runRow(t, s, tk.ID); cur != 0 || status != "active" {
		t.Fatalf("after rollback run = cur %d status %s, want 0/active", cur, status)
	}
	state, assignee, _ := ticketRow(t, s, tk.ID)
	if state != "new" || assignee != nil {
		t.Fatalf("after rollback ticket = state %s assignee %v, want new/<nil>", state, assignee)
	}
	// Connection remains usable: a follow-up normal write and read succeed.
	if _, err := s.db.Exec(`UPDATE ticket_workflow_runs SET current_step_index=0 WHERE ticket_id=?`, tk.ID); err != nil {
		t.Fatalf("connection not usable after rollback: %v", err)
	}
	if cur, _, _ := runRow(t, s, tk.ID); cur != 0 {
		t.Fatalf("follow-up write not visible: cursor %d", cur)
	}
}

// TestWorkflowUoW_Apply_LateAuditFailureRollsBackAll proves a late DB failure
// (a test-scoped trigger raising on the final workflow_step audit insert) rolls
// back the earlier claim/transition audits written in the same transaction,
// with the infrastructure error propagated untouched (not flattened to a
// position conflict).
func TestWorkflowUoW_Apply_LateAuditFailureRollsBackAll(t *testing.T) {
	s := newTestDB(t)
	cat := seedCategory(t, s, "C1")
	req := seedUser(t, s, "Req", "r@x", true)
	agent := seedUser(t, s, "Ag", "a@x", true)
	deskID := seedDeskWithMember(t, s, agent)
	def := claimDef(deskID)
	vid := seedPublished(t, s, cat, def)
	now := testClock
	tk := seedPinnedTicket(t, s, domain.Ticket{Number: 41, Title: "T", Description: "", RequesterName: "Req", RequesterEmail: "r@x", RequesterUserID: &req, CategoryID: cat, Priority: domain.PriorityMedium, State: domain.StateNew, CreatedAt: now, UpdatedAt: now, WorkflowVersionID: &vid})
	seedRun(t, s, tk.ID, 0, "active", now)
	// The claim group's LAST audit write is its new->in_progress transition;
	// aborting it must roll back the earlier contextual assignment audit too.
	if _, err := s.db.Exec(`CREATE TRIGGER trg_fail AFTER INSERT ON audit_events
		WHEN NEW.action = 'transition'
		BEGIN SELECT RAISE(ABORT, 'boom'); END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}
	ctx := context.Background()
	ops := applyClaimOps(t, now, tk, agent, deskID)
	plan := buildApplyPlan(tk, vid, def, agent, "Ag", ops, 1, "active", domain.StateInProgress, &agent, nil)
	_, err := newWorkflowUnitOfWork(s.db).ApplyWorkflowPlan(ctx, plan)
	if err == nil {
		t.Fatal("injected late audit failure must fail the apply")
	}
	if errors.Is(err, domain.ErrWorkflowPositionConflict) {
		t.Fatalf("a DB failure must propagate as an infrastructure error, not a position conflict: %v", err)
	}
	var audits int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE ticket_id=?`, tk.ID).Scan(&audits)
	if audits != 0 {
		t.Fatalf("late DB failure left %d audits, want 0 (total rollback)", audits)
	}
	if cur, status, _ := runRow(t, s, tk.ID); cur != 0 || status != "active" {
		t.Fatalf("after rollback run = cur %d status %s, want 0/active", cur, status)
	}
}

// ---- PR5 OPERATION-GRAMMAR correction: frozen remaining failures ----
//
// The following tests pin the EXACT operation-group grammar/progression and the
// create-path zero-ticketid placeholder rule. Each was RED before the correction
// (empty ops could advance a pending human step, a human step could be skipped,
// groups could be malformed/duplicated, NextCursor was not derived from consumed
// steps, form JSON was not corroborated against the pinned positional schema, extra
// audit facts were silently accepted, bad result facts were accepted, and create
// overwrote wrong nonzero audit ticket ids).

// fldShortText builds a short_text form field.
func fldShortText(key string) domain.FormField {
	return domain.FormField{Key: key, Label: key, Kind: domain.FieldShortText}
}

// fldSingleSelect builds a single_select form field with the exact canonical options.
func fldSingleSelect(key string, opts ...string) domain.FormField {
	return domain.FormField{Key: key, Label: key, Kind: domain.FieldSingleSelect, Options: opts}
}

func TestWorkflowUoW_Apply_EmptyOpsCannotAdvanceHuman(t *testing.T) {
	// A pending MANUAL step at cursor 0 cannot be advanced by an empty operation
	// list: the request must actually complete the current step.
	s := newTestDB(t)
	cat := seedCategory(t, s, "C1")
	req := seedUser(t, s, "Req", "r@x", true)
	agent := seedUser(t, s, "Ag", "a@x", true)
	def := manualTaskDef()
	vid := seedPublished(t, s, cat, def)
	now := testClock
	tk := seedPinnedTicket(t, s, domain.Ticket{Number: 200, Title: "T", Description: "", RequesterName: "Req", RequesterEmail: "r@x", RequesterUserID: &req, CategoryID: cat, Priority: domain.PriorityMedium, State: domain.StateNew, UserID: &agent, CreatedAt: now, UpdatedAt: now, WorkflowVersionID: &vid})
	seedRun(t, s, tk.ID, 0, "active", now)
	// Empty ops that claim to advance cursor 0 -> 1 (skipping the pending step).
	plan := buildApplyPlan(tk, vid, def, agent, "A", nil, 1, "active", domain.StateNew, &agent, nil)
	_, err := newWorkflowUnitOfWork(s.db).ApplyWorkflowPlan(context.Background(), plan)
	if !errors.Is(err, domain.ErrWorkflowPositionConflict) {
		t.Fatalf("empty ops advancing a pending human step must conflict, got %v", err)
	}
	assertApplyNoWrites(t, s, tk)
}

func TestWorkflowUoW_Apply_EmptyOpsAtActiveRunNoAdvance(t *testing.T) {
	// Even empty ops with an unchanged cursor are a skipped current human step on
	// an active run and must be rejected (completing nothing is not completing the
	// step).
	s := newTestDB(t)
	cat := seedCategory(t, s, "C1")
	req := seedUser(t, s, "Req", "r@x", true)
	agent := seedUser(t, s, "Ag", "a@x", true)
	def := manualTaskDef()
	vid := seedPublished(t, s, cat, def)
	now := testClock
	tk := seedPinnedTicket(t, s, domain.Ticket{Number: 201, Title: "T", Description: "", RequesterName: "Req", RequesterEmail: "r@x", RequesterUserID: &req, CategoryID: cat, Priority: domain.PriorityMedium, State: domain.StateNew, UserID: &agent, CreatedAt: now, UpdatedAt: now, WorkflowVersionID: &vid})
	seedRun(t, s, tk.ID, 0, "active", now)
	plan := buildApplyPlan(tk, vid, def, agent, "A", nil, 0, "active", domain.StateNew, &agent, nil)
	_, err := newWorkflowUnitOfWork(s.db).ApplyWorkflowPlan(context.Background(), plan)
	if !errors.Is(err, domain.ErrWorkflowPositionConflict) {
		t.Fatalf("empty ops on active run must conflict (no skipped current step), got %v", err)
	}
	assertApplyNoWrites(t, s, tk)
}

func TestWorkflowUoW_Apply_SkippedCurrentHumanStep(t *testing.T) {
	// The request completes step 1 but SKIPS the current step 0 (an earlier manual
	// step): automatic progression must follow in definition order without gaps.
	s := newTestDB(t)
	cat := seedCategory(t, s, "C1")
	req := seedUser(t, s, "Req", "r@x", true)
	agent := seedUser(t, s, "Ag", "a@x", true)
	def := domain.WorkflowDefinition{
		{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "one"}},
		{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "two"}},
	}
	vid := seedPublished(t, s, cat, def)
	now := testClock
	tk := seedPinnedTicket(t, s, domain.Ticket{Number: 202, Title: "T", Description: "", RequesterName: "Req", RequesterEmail: "r@x", RequesterUserID: &req, CategoryID: cat, Priority: domain.PriorityMedium, State: domain.StateNew, UserID: &agent, CreatedAt: now, UpdatedAt: now, WorkflowVersionID: &vid})
	seedRun(t, s, tk.ID, 0, "active", now)
	// Complete ONLY step 1 (skipping the pending step 0).
	audit := domain.AuditEvent{TicketID: tk.ID, Actor: "A", ActorUserID: &agent, Action: domain.ActionWorkflowManualTask, CreatedAt: now}
	ops := []application.WorkflowOperation{application.WorkflowStepOperation{StepIndex: 1, Audit: audit}}
	plan := buildApplyPlan(tk, vid, def, agent, "A", ops, 2, "active", domain.StateNew, &agent, nil)
	_, err := newWorkflowUnitOfWork(s.db).ApplyWorkflowPlan(context.Background(), plan)
	if !errors.Is(err, domain.ErrWorkflowPositionConflict) {
		t.Fatalf("skipping the current human step must conflict, got %v", err)
	}
	assertApplyNoWrites(t, s, tk)
}

func TestWorkflowUoW_Apply_DuplicateGroupOperation(t *testing.T) {
	// A MANUAL step's exact group is a single WorkflowStep audit; a duplicate
	// WorkflowStep at the same index is a malformed group and must be rejected.
	s := newTestDB(t)
	cat := seedCategory(t, s, "C1")
	req := seedUser(t, s, "Req", "r@x", true)
	agent := seedUser(t, s, "Ag", "a@x", true)
	def := manualTaskDef()
	vid := seedPublished(t, s, cat, def)
	now := testClock
	tk := seedPinnedTicket(t, s, domain.Ticket{Number: 203, Title: "T", Description: "", RequesterName: "Req", RequesterEmail: "r@x", RequesterUserID: &req, CategoryID: cat, Priority: domain.PriorityMedium, State: domain.StateNew, UserID: &agent, CreatedAt: now, UpdatedAt: now, WorkflowVersionID: &vid})
	seedRun(t, s, tk.ID, 0, "active", now)
	audit := domain.AuditEvent{TicketID: tk.ID, Actor: "A", ActorUserID: &agent, Action: domain.ActionWorkflowManualTask, CreatedAt: now}
	ops := []application.WorkflowOperation{
		application.WorkflowStepOperation{StepIndex: 0, Audit: audit},
		application.WorkflowStepOperation{StepIndex: 0, Audit: audit},
	}
	plan := buildApplyPlan(tk, vid, def, agent, "A", ops, 1, "active", domain.StateNew, &agent, nil)
	_, err := newWorkflowUnitOfWork(s.db).ApplyWorkflowPlan(context.Background(), plan)
	if !errors.Is(err, domain.ErrWorkflowPositionConflict) {
		t.Fatalf("duplicate group operation must conflict, got %v", err)
	}
	assertApplyNoWrites(t, s, tk)
}

func TestWorkflowUoW_Apply_ClaimGroupMissingAssignment(t *testing.T) {
	// A CLAIM step's exact group begins with the ClaimAssignment; a transition
	// before the claim (or a claim group without its assignment) is malformed.
	s := newTestDB(t)
	cat := seedCategory(t, s, "C1")
	req := seedUser(t, s, "Req", "r@x", true)
	agent := seedUser(t, s, "Ag", "a@x", true)
	deskID := seedDeskWithMember(t, s, agent)
	def := claimDef(deskID)
	vid := seedPublished(t, s, cat, def)
	now := testClock
	tk := seedPinnedTicket(t, s, domain.Ticket{Number: 204, Title: "T", Description: "", RequesterName: "Req", RequesterEmail: "r@x", RequesterUserID: &req, CategoryID: cat, Priority: domain.PriorityMedium, State: domain.StateNew, CreatedAt: now, UpdatedAt: now, WorkflowVersionID: &vid})
	seedRun(t, s, tk.ID, 0, "active", now)
	// Claim group missing the ClaimAssignment: only [transition, workflow_step].
	ops := []application.WorkflowOperation{
		application.TransitionOperation{StepIndex: 0, Audit: domain.AuditEvent{TicketID: tk.ID, Actor: "workflow", Action: domain.ActionTransition, Field: ptr("state"), FromValue: ptr("new"), ToValue: ptr("in_progress"), CreatedAt: now}},
		application.WorkflowStepOperation{StepIndex: 0, Audit: domain.AuditEvent{TicketID: tk.ID, Actor: "Ag", ActorUserID: &agent, Action: domain.ActionWorkflowStep, CreatedAt: now}},
	}
	plan := buildApplyPlan(tk, vid, def, agent, "Ag", ops, 1, "active", domain.StateInProgress, &agent, nil)
	_, err := newWorkflowUnitOfWork(s.db).ApplyWorkflowPlan(context.Background(), plan)
	if !errors.Is(err, domain.ErrWorkflowPositionConflict) {
		t.Fatalf("claim group missing assignment must conflict, got %v", err)
	}
	assertApplyNoWrites(t, s, tk)
}

func TestWorkflowUoW_Apply_NextCursorMismatch(t *testing.T) {
	// Consuming exactly step 0 -> cursor lands at 1; a plan declaring NextCursor 2
	// contradicts the consumed steps and must be rejected.
	s := newTestDB(t)
	cat := seedCategory(t, s, "C1")
	req := seedUser(t, s, "Req", "r@x", true)
	agent := seedUser(t, s, "Ag", "a@x", true)
	def := manualTaskDef()
	vid := seedPublished(t, s, cat, def)
	now := testClock
	tk := seedPinnedTicket(t, s, domain.Ticket{Number: 205, Title: "T", Description: "", RequesterName: "Req", RequesterEmail: "r@x", RequesterUserID: &req, CategoryID: cat, Priority: domain.PriorityMedium, State: domain.StateNew, UserID: &agent, CreatedAt: now, UpdatedAt: now, WorkflowVersionID: &vid})
	seedRun(t, s, tk.ID, 0, "active", now)
	ops := applyManualOps(now, tk, agent)
	plan := buildApplyPlan(tk, vid, def, agent, "A", ops, 2, "active", domain.StateNew, &agent, nil)
	_, err := newWorkflowUnitOfWork(s.db).ApplyWorkflowPlan(context.Background(), plan)
	if !errors.Is(err, domain.ErrWorkflowPositionConflict) {
		t.Fatalf("NextCursor conflicting with consumed steps must be rejected, got %v", err)
	}
	assertApplyNoWrites(t, s, tk)
}

func TestWorkflowUoW_Apply_FormAnswerSchemaRejections(t *testing.T) {
	cases := []struct {
		name    string
		fields  []domain.FormField
		answers string
	}{
		{"malformed-json", []domain.FormField{fldShortText("k")}, `["x`},
		{"wrong-count", []domain.FormField{fldShortText("a"), fldShortText("b")}, `["x"]`},
		{"wrong-type-number", []domain.FormField{fldShortText("k")}, `[123]`},
		{"null-value", []domain.FormField{fldShortText("k")}, `[null]`},
		{"object-value", []domain.FormField{fldShortText("k")}, `[{"k":1}]`},
		{"unknown-select-option", []domain.FormField{fldSingleSelect("c", "a", "b")}, `["nope"]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestDB(t)
			cat := seedCategory(t, s, "C1")
			req := seedUser(t, s, "Req", "r@x", true)
			def := domain.WorkflowDefinition{{Type: domain.StepForm, Form: &domain.FormStep{Actor: domain.FormActorRequester, Fields: tc.fields}}}
			vid := seedPublished(t, s, cat, def)
			now := testClock
			tk := seedPinnedTicket(t, s, domain.Ticket{Number: 206, Title: "T", Description: "", RequesterName: "Req", RequesterEmail: "r@x", RequesterUserID: &req, CategoryID: cat, Priority: domain.PriorityMedium, State: domain.StateNew, CreatedAt: now, UpdatedAt: now, WorkflowVersionID: &vid})
			seedRun(t, s, tk.ID, 0, "active", now)
			ops := applyFormOps(now, tk, req, tc.answers)
			plan := buildApplyPlan(tk, vid, def, req, "A", ops, 1, "active", domain.StateNew, nil, nil)
			_, err := newWorkflowUnitOfWork(s.db).ApplyWorkflowPlan(context.Background(), plan)
			if !errors.Is(err, domain.ErrWorkflowPositionConflict) {
				t.Fatalf("%s must conflict against the pinned positional schema, got %v", tc.name, err)
			}
			assertApplyNoWrites(t, s, tk)
		})
	}
}

func TestWorkflowUoW_Apply_TransitionExtraReasonOrNoteConflict(t *testing.T) {
	// A workflow transition audit must carry NO reason or note (terminal/claim
	// transitions never reopen a closed ticket); a fabricated fact is rejected.
	s := newTestDB(t)
	cat := seedCategory(t, s, "C1")
	req := seedUser(t, s, "Req", "r@x", true)
	agent := seedUser(t, s, "Ag", "a@x", true)
	def := domain.WorkflowDefinition{{Type: domain.StepResolve}}
	vid := seedPublished(t, s, cat, def)
	started := testClock
	tk := seedPinnedTicket(t, s, domain.Ticket{Number: 207, Title: "T", Description: "", RequesterName: "Req", RequesterEmail: "r@x", RequesterUserID: &req, CategoryID: cat, Priority: domain.PriorityMedium, State: domain.StateNew, UserID: &agent, CreatedAt: started, UpdatedAt: started, WorkflowVersionID: &vid})
	seedRun(t, s, tk.ID, 0, "active", started)
	reason := "why"
	tr := domain.AuditEvent{TicketID: tk.ID, Actor: "workflow", Action: domain.ActionTransition, Field: ptr("state"), FromValue: ptr("new"), ToValue: ptr("resolved"), Reason: &reason, CreatedAt: started}
	ops := []application.WorkflowOperation{application.TransitionOperation{StepIndex: 0, Audit: tr}}
	ct := started
	plan := buildApplyPlan(tk, vid, def, agent, "Ag", ops, 1, "completed", domain.StateResolved, &agent, &ct)
	_, err := newWorkflowUnitOfWork(s.db).ApplyWorkflowPlan(context.Background(), plan)
	if !errors.Is(err, domain.ErrWorkflowPositionConflict) {
		t.Fatalf("transition with an extra reason must conflict, got %v", err)
	}
	assertApplyNoWrites(t, s, tk)
}

func TestWorkflowUoW_Apply_WorkflowStepExtraFieldConflict(t *testing.T) {
	// A workflow_step completion audit must carry NO field/from/to/reason/note; an
	// extra fabricated field is rejected.
	s := newTestDB(t)
	cat := seedCategory(t, s, "C1")
	req := seedUser(t, s, "Req", "r@x", true)
	agent := seedUser(t, s, "Ag", "a@x", true)
	def := manualTaskDef()
	vid := seedPublished(t, s, cat, def)
	now := testClock
	tk := seedPinnedTicket(t, s, domain.Ticket{Number: 208, Title: "T", Description: "", RequesterName: "Req", RequesterEmail: "r@x", RequesterUserID: &req, CategoryID: cat, Priority: domain.PriorityMedium, State: domain.StateNew, UserID: &agent, CreatedAt: now, UpdatedAt: now, WorkflowVersionID: &vid})
	seedRun(t, s, tk.ID, 0, "active", now)
	field := "user"
	audit := domain.AuditEvent{TicketID: tk.ID, Actor: "A", ActorUserID: &agent, Action: domain.ActionWorkflowManualTask, Field: &field, CreatedAt: now}
	ops := []application.WorkflowOperation{application.WorkflowStepOperation{StepIndex: 0, Audit: audit}}
	plan := buildApplyPlan(tk, vid, def, agent, "A", ops, 1, "active", domain.StateNew, &agent, nil)
	_, err := newWorkflowUnitOfWork(s.db).ApplyWorkflowPlan(context.Background(), plan)
	if !errors.Is(err, domain.ErrWorkflowPositionConflict) {
		t.Fatalf("workflow_step audit with an extra field must conflict, got %v", err)
	}
	assertApplyNoWrites(t, s, tk)
}

func TestWorkflowUoW_Apply_BadResultRunFacts(t *testing.T) {
	// The caller Result.Run cursor/status must agree with the declared NextCursor/
	// NextRunStatus; a contradictory Result is rejected.
	s := newTestDB(t)
	cat := seedCategory(t, s, "C1")
	req := seedUser(t, s, "Req", "r@x", true)
	agent := seedUser(t, s, "Ag", "a@x", true)
	def := manualTaskDef()
	vid := seedPublished(t, s, cat, def)
	now := testClock
	tk := seedPinnedTicket(t, s, domain.Ticket{Number: 209, Title: "T", Description: "", RequesterName: "Req", RequesterEmail: "r@x", RequesterUserID: &req, CategoryID: cat, Priority: domain.PriorityMedium, State: domain.StateNew, UserID: &agent, CreatedAt: now, UpdatedAt: now, WorkflowVersionID: &vid})
	seedRun(t, s, tk.ID, 0, "active", now)
	plan := buildApplyPlan(tk, vid, def, agent, "A", applyManualOps(now, tk, agent), 1, "active", domain.StateNew, &agent, nil)
	plan.Result.Run.CurrentStepIndex = 5 // contradicts set cursor 1
	_, err := newWorkflowUnitOfWork(s.db).ApplyWorkflowPlan(context.Background(), plan)
	if !errors.Is(err, domain.ErrWorkflowPositionConflict) {
		t.Fatalf("contradictory Result.Run cursor must conflict, got %v", err)
	}
	assertApplyNoWrites(t, s, tk)
}

func TestWorkflowUoW_Apply_BadResultTicketFacts(t *testing.T) {
	// The caller Result.Ticket state/assignee must agree with the simulated final
	// ticket; a contradictory Result is rejected.
	s := newTestDB(t)
	cat := seedCategory(t, s, "C1")
	req := seedUser(t, s, "Req", "r@x", true)
	agent := seedUser(t, s, "Ag", "a@x", true)
	def := manualTaskDef()
	vid := seedPublished(t, s, cat, def)
	now := testClock
	tk := seedPinnedTicket(t, s, domain.Ticket{Number: 210, Title: "T", Description: "", RequesterName: "Req", RequesterEmail: "r@x", RequesterUserID: &req, CategoryID: cat, Priority: domain.PriorityMedium, State: domain.StateNew, UserID: &agent, CreatedAt: now, UpdatedAt: now, WorkflowVersionID: &vid})
	seedRun(t, s, tk.ID, 0, "active", now)
	plan := buildApplyPlan(tk, vid, def, agent, "A", applyManualOps(now, tk, agent), 1, "active", domain.StateNew, &agent, nil)
	resTicket := tk
	resTicket.State = domain.StateResolved // contradicts simulated new
	plan.Result.Ticket = &resTicket
	_, err := newWorkflowUnitOfWork(s.db).ApplyWorkflowPlan(context.Background(), plan)
	if !errors.Is(err, domain.ErrWorkflowPositionConflict) {
		t.Fatalf("contradictory Result.Ticket state must conflict, got %v", err)
	}
	assertApplyNoWrites(t, s, tk)
}

func TestWorkflowUoW_Create_WrongNonzeroCreatedAuditTicketID(t *testing.T) {
	// Creation owns the ticket id; a caller-supplied nonzero created-audit ticket
	// id is a fabricated fact and must be rejected (never silently overwritten).
	s := newTestDB(t)
	cat := seedCategory(t, s, "C1")
	req := seedUser(t, s, "Req", "r@x", true)
	def := manualTaskDef()
	vid := seedPublished(t, s, cat, def)
	in := buildCreateInput(cat, vid, req, def, nil, 0, "active", domain.StateNew, nil)
	in.CreatedAudit.TicketID = 999
	_, err := newWorkflowUnitOfWork(s.db).CreateTicketWithRun(context.Background(), in)
	if !errors.Is(err, domain.ErrWorkflowPositionConflict) {
		t.Fatalf("nonzero created-audit ticket id must conflict, got %v", err)
	}
	assertTotalRollback(t, s)
}

func TestWorkflowUoW_Create_WrongNonzeroTransitionTicketID(t *testing.T) {
	// Creation only accepts a ZERO placeholder transition-audit ticket id; a wrong
	// nonzero id must be rejected, never silently overwritten with the new id.
	s := newTestDB(t)
	cat := seedCategory(t, s, "C1")
	req := seedUser(t, s, "Req", "r@x", true)
	def := domain.WorkflowDefinition{{Type: domain.StepResolve}}
	vid := seedPublished(t, s, cat, def)
	now := testClock
	tr := domain.AuditEvent{TicketID: 999, Actor: "workflow", Action: domain.ActionTransition, Field: ptr("state"), FromValue: ptr("new"), ToValue: ptr("resolved"), CreatedAt: now}
	ops := []application.WorkflowOperation{application.TransitionOperation{StepIndex: 0, Audit: tr}}
	ct := now
	in := buildCreateInput(cat, vid, req, def, ops, 1, "completed", domain.StateResolved, &ct)
	_, err := newWorkflowUnitOfWork(s.db).CreateTicketWithRun(context.Background(), in)
	if !errors.Is(err, domain.ErrWorkflowPositionConflict) {
		t.Fatalf("nonzero transition-audit ticket id must conflict, got %v", err)
	}
	assertTotalRollback(t, s)
}

func TestWorkflowUoW_Create_WrongNonzeroTransitOpAuditZeroAccepted(t *testing.T) {
	// A ZERO transition-audit ticket-id placeholder is accepted and stamped with
	// the store-assigned id, atomically.
	s := newTestDB(t)
	cat := seedCategory(t, s, "C1")
	req := seedUser(t, s, "Req", "r@x", true)
	def := domain.WorkflowDefinition{{Type: domain.StepResolve}}
	vid := seedPublished(t, s, cat, def)
	now := testClock
	tr := domain.AuditEvent{Actor: "workflow", Action: domain.ActionTransition, Field: ptr("state"), FromValue: ptr("new"), ToValue: ptr("resolved"), CreatedAt: now}
	ops := []application.WorkflowOperation{application.TransitionOperation{StepIndex: 0, Audit: tr}}
	ct := now
	in := buildCreateInput(cat, vid, req, def, ops, 1, "completed", domain.StateResolved, &ct)
	tk, err := newWorkflowUnitOfWork(s.db).CreateTicketWithRun(context.Background(), in)
	if err != nil {
		t.Fatalf("create with zero placeholder audit id: %v", err)
	}
	// The persisted transition audit must carry the STORE-assigned ticket id.
	var aid int64
	if err := s.db.QueryRow(`SELECT ticket_id FROM audit_events WHERE ticket_id=? AND action='transition'`, tk.ID).Scan(&aid); err != nil {
		t.Fatalf("read transition audit: %v", err)
	}
	if aid != tk.ID {
		t.Fatalf("persisted transition audit ticket id = %d, want assigned id %d", aid, tk.ID)
	}
	if cur, status, _ := runRow(t, s, tk.ID); cur != 1 || status != "completed" {
		t.Fatalf("run = cur %d status %s, want 1/completed", cur, status)
	}
}

// ---- PR5 final-gate operation-grammar correction (RED then GREEN) ----
//
// The following tests pin the exact corrected grammar demanded by the PR5 final
// gate: prefix-only group consumption (human group + contiguous automatic tail),
// resolve = exactly one transition, close = exact matrix, terminal already-state
// no-op completion, new-state claim MUST carry new->in_progress (in_progress MUST
// NOT), monotonic timestamps, claim audits carry no note, and a REQUIRED non-nil
// Result with exact ticket/run identities.

func TestWorkflowUoW_Apply_HumanThenAutomaticTail(t *testing.T) {
	// A MANUAL human step immediately followed by an automatic RESOLVE completes in
	// the SAME request (runner-valid human->automatic tail). The group parser must
	// consume ONLY each group's prefix and continue with the contiguous automatic
	// group, never requiring a human group to absorb the whole remaining plan.
	s := newTestDB(t)
	cat := seedCategory(t, s, "C1")
	req := seedUser(t, s, "Req", "r@x", true)
	agent := seedUser(t, s, "Ag", "a@x", true)
	def := domain.WorkflowDefinition{
		{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "one"}},
		{Type: domain.StepResolve},
	}
	vid := seedPublished(t, s, cat, def)
	now := testClock
	v := vid
	tk := seedPinnedTicket(t, s, domain.Ticket{Number: 300, Title: "T", Description: "", RequesterName: "Req", RequesterEmail: "r@x", RequesterUserID: &req, CategoryID: cat, Priority: domain.PriorityMedium, State: domain.StateInProgress, UserID: &agent, CreatedAt: now, UpdatedAt: now, WorkflowVersionID: &v})
	seedRun(t, s, tk.ID, 0, "active", now)
	ws := domain.AuditEvent{TicketID: tk.ID, Actor: "A", ActorUserID: &agent, Action: domain.ActionWorkflowManualTask, CreatedAt: now}
	tr := domain.AuditEvent{TicketID: tk.ID, Actor: "workflow", Action: domain.ActionTransition, Field: ptr("state"), FromValue: ptr("in_progress"), ToValue: ptr("resolved"), CreatedAt: now}
	ops := []application.WorkflowOperation{
		application.WorkflowStepOperation{StepIndex: 0, Audit: ws},
		application.TransitionOperation{StepIndex: 1, Audit: tr},
	}
	ct := now
	plan := buildApplyPlan(tk, vid, def, agent, "A", ops, 2, "completed", domain.StateResolved, &agent, &ct)
	if _, err := newWorkflowUnitOfWork(s.db).ApplyWorkflowPlan(context.Background(), plan); err != nil {
		t.Fatalf("human->automatic tail apply must succeed, got %v", err)
	}
	state, _, _ := ticketRow(t, s, tk.ID)
	if state != "resolved" {
		t.Fatalf("ticket state = %s want resolved", state)
	}
	gotAudits := auditActionOrder(t, s, tk.ID)
	want := []string{"workflow_manual_task||", "transition|in_progress|resolved"}
	if strings.Join(gotAudits, ";") != strings.Join(want, ";") {
		t.Fatalf("audit order = %v want %v", gotAudits, want)
	}
	if cur, status, _ := runRow(t, s, tk.ID); cur != 2 || status != "completed" {
		t.Fatalf("run = cur %d status %s, want 2/completed", cur, status)
	}
}

func TestWorkflowUoW_Apply_TerminalAlreadyStateNoop(t *testing.T) {
	// A resolve step on an already-resolved ticket completes as a no-op with NO
	// transition: the runner advances the cursor to len(def) and marks the run
	// completed exactly. Empty ops at an already-terminal step are valid; the same
	// empty plan on a pending HUMAN step is invalid (covered elsewhere).
	s := newTestDB(t)
	cat := seedCategory(t, s, "C1")
	req := seedUser(t, s, "Req", "r@x", true)
	agent := seedUser(t, s, "Ag", "a@x", true)
	def := domain.WorkflowDefinition{{Type: domain.StepResolve}}
	vid := seedPublished(t, s, cat, def)
	now := testClock
	v := vid
	tk := seedPinnedTicket(t, s, domain.Ticket{Number: 301, Title: "T", Description: "", RequesterName: "Req", RequesterEmail: "r@x", RequesterUserID: &req, CategoryID: cat, Priority: domain.PriorityMedium, State: domain.StateResolved, UserID: &agent, CreatedAt: now, UpdatedAt: now, ResolvedAt: &now, WorkflowVersionID: &v})
	seedRun(t, s, tk.ID, 0, "active", now)
	ct := now
	plan := buildApplyPlan(tk, vid, def, agent, "Ag", nil, 1, "completed", domain.StateResolved, &agent, &ct)
	if _, err := newWorkflowUnitOfWork(s.db).ApplyWorkflowPlan(context.Background(), plan); err != nil {
		t.Fatalf("terminal already-state no-op apply must succeed, got %v", err)
	}
	state, _, _ := ticketRow(t, s, tk.ID)
	if state != "resolved" {
		t.Fatalf("ticket state = %s want resolved (unchanged no-op)", state)
	}
	if cur, status, comp := runRow(t, s, tk.ID); cur != 1 || status != "completed" || comp == nil {
		t.Fatalf("run = cur %d status %s completed %v, want 1/completed/set", cur, status, comp)
	}
	var audits int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE ticket_id=?`, tk.ID).Scan(&audits)
	if audits != 0 {
		t.Fatalf("terminal no-op wrote %d audit events, want 0", audits)
	}
}

func TestWorkflowUoW_Apply_ResolveExtraTransitionRejected(t *testing.T) {
	// A resolve step is exactly ONE transition. Two resolve-type transitions in one
	// group is a fabricated group and must be rejected, never silently applied.
	s := newTestDB(t)
	cat := seedCategory(t, s, "C1")
	req := seedUser(t, s, "Req", "r@x", true)
	agent := seedUser(t, s, "Ag", "a@x", true)
	def := domain.WorkflowDefinition{{Type: domain.StepResolve}}
	vid := seedPublished(t, s, cat, def)
	now := testClock
	v := vid
	tk := seedPinnedTicket(t, s, domain.Ticket{Number: 310, Title: "T", Description: "", RequesterName: "Req", RequesterEmail: "r@x", RequesterUserID: &req, CategoryID: cat, Priority: domain.PriorityMedium, State: domain.StateNew, UserID: &agent, CreatedAt: now, UpdatedAt: now, WorkflowVersionID: &v})
	seedRun(t, s, tk.ID, 0, "active", now)
	ops := []application.WorkflowOperation{
		application.TransitionOperation{StepIndex: 0, Audit: domain.AuditEvent{TicketID: tk.ID, Actor: "workflow", Action: domain.ActionTransition, Field: ptr("state"), FromValue: ptr("new"), ToValue: ptr("resolved"), CreatedAt: now}},
		application.TransitionOperation{StepIndex: 0, Audit: domain.AuditEvent{TicketID: tk.ID, Actor: "workflow", Action: domain.ActionTransition, Field: ptr("state"), FromValue: ptr("resolved"), ToValue: ptr("closed"), CreatedAt: now}},
	}
	ct := now
	plan := buildApplyPlan(tk, vid, def, agent, "Ag", ops, 1, "completed", domain.StateClosed, &agent, &ct)
	_, err := newWorkflowUnitOfWork(s.db).ApplyWorkflowPlan(context.Background(), plan)
	if !errors.Is(err, domain.ErrWorkflowPositionConflict) {
		t.Fatalf("resolve with two transitions must conflict, got %v", err)
	}
	assertApplyNoWrites(t, s, tk)
}

func TestWorkflowUoW_Apply_ClaimNewMissingTransitionRejected(t *testing.T) {
	// A claim on a NEW ticket MUST carry the exact new->in_progress transition; an
	// unassigned-new claim that only assigns + completes the step (skipping the
	// transition) is a fabricated group and must be rejected.
	s := newTestDB(t)
	cat := seedCategory(t, s, "C1")
	req := seedUser(t, s, "Req", "r@x", true)
	agent := seedUser(t, s, "Ag", "a@x", true)
	deskID := seedDeskWithMember(t, s, agent)
	def := claimDef(deskID)
	vid := seedPublished(t, s, cat, def)
	now := testClock
	tk := seedPinnedTicket(t, s, domain.Ticket{Number: 311, Title: "T", Description: "", RequesterName: "Req", RequesterEmail: "r@x", RequesterUserID: &req, CategoryID: cat, Priority: domain.PriorityMedium, State: domain.StateNew, CreatedAt: now, UpdatedAt: now, WorkflowVersionID: &vid})
	seedRun(t, s, tk.ID, 0, "active", now)
	full := applyClaimOps(t, now, tk, agent, deskID)
	ops := []application.WorkflowOperation{full[0]} // claim only, NO transition
	// The lying plan declares the ticket STILL new (no transition fired); on a new
	// claim the new->in_progress transition is REQUIRED, so this is rejected.
	plan := buildApplyPlan(tk, vid, def, agent, "Ag", ops, 1, "active", domain.StateNew, &agent, nil)
	_, err := newWorkflowUnitOfWork(s.db).ApplyWorkflowPlan(context.Background(), plan)
	if !errors.Is(err, domain.ErrWorkflowPositionConflict) {
		t.Fatalf("claim on new missing its in-progress transition must conflict, got %v", err)
	}
	assertApplyNoWrites(t, s, tk)
}

func TestWorkflowUoW_Apply_TimestampReversalRejected(t *testing.T) {
	// Plan audit timestamps must be monotonic (>= run start, nondecreasing, and a
	// form answer may not post-date its same-completion workflow_step). A
	// workflow_step audit timestamped EARLIER than the form answer it completes is a
	// fabricated chronology and must be rejected.
	s := newTestDB(t)
	cat := seedCategory(t, s, "C1")
	req := seedUser(t, s, "Req", "r@x", true)
	def := formDef()
	vid := seedPublished(t, s, cat, def)
	now := testClock
	tk := seedPinnedTicket(t, s, domain.Ticket{Number: 320, Title: "T", Description: "", RequesterName: "Req", RequesterEmail: "r@x", RequesterUserID: &req, CategoryID: cat, Priority: domain.PriorityMedium, State: domain.StateNew, CreatedAt: now, UpdatedAt: now, WorkflowVersionID: &vid})
	seedRun(t, s, tk.ID, 0, "active", now)
	// Both op timestamps are >= run start (so the existing per-op "before run start"
	// guard does NOT fire); the reversal is only caught by the nondecreasing-order
	// rule — the workflow_step (now) precedes its form answer (now+1h) in literal
	// order yet is stamped earlier.
	later := now.Add(time.Hour)
	ops := []application.WorkflowOperation{
		application.FormAnswerOperation{StepIndex: 0, AnswersJSON: []byte(`["x"]`), SubmittedByUserID: req, SubmittedAt: later},
		application.WorkflowStepOperation{StepIndex: 0, Audit: domain.AuditEvent{TicketID: tk.ID, Actor: "A", ActorUserID: &req, Action: domain.ActionWorkflowRequesterForm, CreatedAt: now}},
	}
	plan := buildApplyPlan(tk, vid, def, req, "A", ops, 1, "active", domain.StateNew, nil, nil)
	_, err := newWorkflowUnitOfWork(s.db).ApplyWorkflowPlan(context.Background(), plan)
	if !errors.Is(err, domain.ErrWorkflowPositionConflict) {
		t.Fatalf("timestamp reversal must conflict, got %v", err)
	}
	assertApplyNoWrites(t, s, tk)
}

func TestWorkflowUoW_Apply_ClaimNoteRejected(t *testing.T) {
	// A claim assignment audit must carry NO note (audit-log spec); a fabricated
	// note is rejected even when the assignment is otherwise coherent.
	s := newTestDB(t)
	cat := seedCategory(t, s, "C1")
	req := seedUser(t, s, "Req", "r@x", true)
	agent := seedUser(t, s, "Ag", "a@x", true)
	deskID := seedDeskWithMember(t, s, agent)
	def := claimDef(deskID)
	vid := seedPublished(t, s, cat, def)
	now := testClock
	tk := seedPinnedTicket(t, s, domain.Ticket{Number: 330, Title: "T", Description: "", RequesterName: "Req", RequesterEmail: "r@x", RequesterUserID: &req, CategoryID: cat, Priority: domain.PriorityMedium, State: domain.StateNew, CreatedAt: now, UpdatedAt: now, WorkflowVersionID: &vid})
	seedRun(t, s, tk.ID, 0, "active", now)
	ops := applyClaimOps(t, now, tk, agent, deskID)
	claim := ops[0].(application.ClaimAssignmentOperation)
	note := "fabricated"
	claim.AssignmentAudit.Note = &note
	ops[0] = claim
	plan := buildApplyPlan(tk, vid, def, agent, "Ag", ops, 1, "active", domain.StateInProgress, &agent, nil)
	_, err := newWorkflowUnitOfWork(s.db).ApplyWorkflowPlan(context.Background(), plan)
	if !errors.Is(err, domain.ErrWorkflowPositionConflict) {
		t.Fatalf("claim assignment with a note must conflict, got %v", err)
	}
	assertApplyNoWrites(t, s, tk)
}

func TestWorkflowUoW_Apply_ResultFactsRequiredRejected(t *testing.T) {
	// An active plan's Result must be non-nil and carry exact ticket/run identities;
	// a nil Result (or wrong id) is a bypass that must be rejected.
	for _, tc := range []struct {
		name   string
		mutate func(s *Store, tk domain.Ticket, p *application.WorkflowMutationPlan)
	}{
		{"nil-result", func(_ *Store, _ domain.Ticket, p *application.WorkflowMutationPlan) {
			p.Result = application.WorkflowExecutionResult{}
		}},
		{"nil-run", func(_ *Store, _ domain.Ticket, p *application.WorkflowMutationPlan) {
			p.Result.Ticket = nil
		}},
		{"nil-ticket", func(_ *Store, _ domain.Ticket, p *application.WorkflowMutationPlan) {
			p.Result.Run = nil
		}},
		{"wrong-run-ticket-id", func(_ *Store, tk domain.Ticket, p *application.WorkflowMutationPlan) {
			p.Result.Run.TicketID = tk.ID + 999
		}},
		{"wrong-ticket-id", func(_ *Store, tk domain.Ticket, p *application.WorkflowMutationPlan) {
			p.Result.Ticket.ID = tk.ID + 999
		}},
		{"wrong-run-start", func(_ *Store, _ domain.Ticket, p *application.WorkflowMutationPlan) {
			p.Result.Run.StartedAt = p.Result.Run.StartedAt.Add(time.Hour)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestDB(t)
			cat := seedCategory(t, s, "C1")
			req := seedUser(t, s, "Req", "r@x", true)
			agent := seedUser(t, s, "Ag", "a@x", true)
			def := manualTaskDef()
			vid := seedPublished(t, s, cat, def)
			now := testClock
			v := vid
			tk := seedPinnedTicket(t, s, domain.Ticket{Number: 340, Title: "T", Description: "", RequesterName: "Req", RequesterEmail: "r@x", RequesterUserID: &req, CategoryID: cat, Priority: domain.PriorityMedium, State: domain.StateNew, UserID: &agent, CreatedAt: now, UpdatedAt: now, WorkflowVersionID: &v})
			seedRun(t, s, tk.ID, 0, "active", now)
			plan := buildApplyPlan(tk, vid, def, agent, "A", applyManualOps(now, tk, agent), 1, "active", domain.StateNew, &agent, nil)
			tc.mutate(s, tk, &plan)
			_, err := newWorkflowUnitOfWork(s.db).ApplyWorkflowPlan(context.Background(), plan)
			if !errors.Is(err, domain.ErrWorkflowPositionConflict) {
				t.Fatalf("%s must conflict, got %v", tc.name, err)
			}
			assertApplyNoWrites(t, s, tk)
		})
	}
}

// ---- PR5 final terminal-matrix + exhaustive evidence + shared-helper retry ----
// (tasks 5.3 / 5.4 final gate) -----------------------------------------------

// fullAuditRow is the COMPLETE persisted audit row projection, in column order:
// ticket_id, actor, actor_user_id, action, field, from_value, to_value, reason,
// note, created_at. Exhaustive success evidence queries EVERY column — never a
// coarse COUNT/presence-only assertion.
type fullAuditRow struct {
	ticketID    int64
	actor       string
	actorUserID *int64
	action      string
	field       *string
	fromValue   *string
	toValue     *string
	deskID      *int64
	reason      *string
	note        *string
	createdAt   string
}

// readFullAudits returns every audit row for a ticket with ALL columns, in
// persisted id order.
func readFullAudits(t *testing.T, s *Store, ticketID int64) []fullAuditRow {
	t.Helper()
	rows, err := s.db.QueryContext(context.Background(),
		`SELECT ticket_id, actor, actor_user_id, action, field, from_value, to_value, reason, note, created_at
		 FROM audit_events WHERE ticket_id=? ORDER BY id`, ticketID)
	if err != nil {
		t.Fatalf("query full audits: %v", err)
	}
	defer rows.Close()
	var out []fullAuditRow
	for rows.Next() {
		var r fullAuditRow
		var aui sql.NullInt64
		var f, fr, to, rs, nt sql.NullString
		if err := rows.Scan(&r.ticketID, &r.actor, &aui, &r.action, &f, &fr, &to, &rs, &nt, &r.createdAt); err != nil {
			t.Fatalf("scan full audit: %v", err)
		}
		if aui.Valid {
			v := aui.Int64
			r.actorUserID = &v
		}
		if f.Valid {
			v := f.String
			r.field = &v
		}
		if fr.Valid {
			v := fr.String
			r.fromValue = &v
		}
		if to.Valid {
			v := to.String
			r.toValue = &v
		}
		if rs.Valid {
			v := rs.String
			r.reason = &v
		}
		if nt.Valid {
			v := nt.String
			r.note = &v
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate full audits: %v", err)
	}
	return out
}

func sptr(v string) *string { return &v }
func sameSPtr(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// requireFullAudit asserts a full audit row equals the fully-specified expected
// row (every column, nil-aware).
func requireFullAudit(t *testing.T, idx int, got, want fullAuditRow) {
	t.Helper()
	if got.ticketID != want.ticketID {
		t.Fatalf("audit[%d] ticket_id = %d, want %d", idx, got.ticketID, want.ticketID)
	}
	if got.actor != want.actor {
		t.Fatalf("audit[%d] actor = %q, want %q", idx, got.actor, want.actor)
	}
	if !sameIntPtr(got.actorUserID, want.actorUserID) {
		t.Fatalf("audit[%d] actor_user_id = %v, want %v", idx, got.actorUserID, want.actorUserID)
	}
	if got.action != want.action {
		t.Fatalf("audit[%d] action = %q, want %q", idx, got.action, want.action)
	}
	if !sameSPtr(got.field, want.field) {
		t.Fatalf("audit[%d] field = %v, want %v", idx, got.field, want.field)
	}
	if !sameSPtr(got.fromValue, want.fromValue) {
		t.Fatalf("audit[%d] from_value = %v, want %v", idx, got.fromValue, want.fromValue)
	}
	if !sameSPtr(got.toValue, want.toValue) {
		t.Fatalf("audit[%d] to_value = %v, want %v", idx, got.toValue, want.toValue)
	}
	if !sameSPtr(got.reason, want.reason) {
		t.Fatalf("audit[%d] reason = %v, want %v", idx, got.reason, want.reason)
	}
	if !sameSPtr(got.note, want.note) {
		t.Fatalf("audit[%d] note = %v, want %v", idx, got.note, want.note)
	}
	if got.createdAt != want.createdAt {
		t.Fatalf("audit[%d] created_at = %q, want %q", idx, got.createdAt, want.createdAt)
	}
}

// ticketFacts is the full persisted ticket projection for exhaustive evidence.
type ticketFacts struct {
	state      string
	assignee   *int64
	resolvedAt *string
	closedAt   *string
	updatedAt  string
}

func readTicketFacts(t *testing.T, s *Store, id int64) ticketFacts {
	t.Helper()
	var tf ticketFacts
	var r, c sql.NullString
	var aui sql.NullInt64
	if err := s.db.QueryRow(`SELECT state, user_id, resolved_at, closed_at, updated_at FROM tickets WHERE id=?`, id).
		Scan(&tf.state, &aui, &r, &c, &tf.updatedAt); err != nil {
		t.Fatalf("read ticket facts: %v", err)
	}
	if aui.Valid {
		v := aui.Int64
		tf.assignee = &v
	}
	if r.Valid {
		v := r.String
		tf.resolvedAt = &v
	}
	if c.Valid {
		v := c.String
		tf.closedAt = &v
	}
	return tf
}

// runFacts is the full persisted run projection for exhaustive evidence.
type runFacts struct {
	cursor    int
	status    string
	started   string
	completed *string
}

func readRunFacts(t *testing.T, s *Store, ticketID int64) runFacts {
	t.Helper()
	var rf runFacts
	var c sql.NullString
	if err := s.db.QueryRow(`SELECT current_step_index, status, started_at, completed_at FROM ticket_workflow_runs WHERE ticket_id=?`, ticketID).
		Scan(&rf.cursor, &rf.status, &rf.started, &c); err != nil {
		t.Fatalf("read run facts: %v", err)
	}
	if c.Valid {
		v := c.String
		rf.completed = &v
	}
	return rf
}

// requireTicketAndRun asserts the full persisted ticket + run facts.
func requireTicketAndRun(t *testing.T, s *Store, tkID int64, wantTF ticketFacts, wantRF runFacts) {
	t.Helper()
	tf := readTicketFacts(t, s, tkID)
	if tf.state != wantTF.state || !sameIntPtr(tf.assignee, wantTF.assignee) ||
		!sameSPtr(tf.resolvedAt, wantTF.resolvedAt) || !sameSPtr(tf.closedAt, wantTF.closedAt) ||
		tf.updatedAt != wantTF.updatedAt {
		t.Fatalf("ticket facts = state %s assignee %v resolved %v closed %v updated %q, want state %s assignee %v resolved %v closed %v updated %q",
			tf.state, tf.assignee, tf.resolvedAt, tf.closedAt, tf.updatedAt,
			wantTF.state, wantTF.assignee, wantTF.resolvedAt, wantTF.closedAt, wantTF.updatedAt)
	}
	rf := readRunFacts(t, s, tkID)
	if rf.cursor != wantRF.cursor || rf.status != wantRF.status || rf.started != wantRF.started ||
		!sameSPtr(rf.completed, wantRF.completed) {
		t.Fatalf("run facts = cursor %d status %s started %q completed %v, want cursor %d status %s started %q completed %v",
			rf.cursor, rf.status, rf.started, rf.completed, wantRF.cursor, wantRF.status, wantRF.started, wantRF.completed)
	}
}

// mkTransitions builds transition operations for a terminal step from explicit
// (from,to) state pairs, all stamped actor "workflow"/NULL id at testClock.
func mkTransitions(ticketID int64, stepIdx int, pairs ...[2]string) []application.WorkflowOperation {
	var ops []application.WorkflowOperation
	for _, p := range pairs {
		f, to := p[0], p[1]
		ev := domain.AuditEvent{TicketID: ticketID, Actor: "workflow", Action: domain.ActionTransition,
			Field: ptr("state"), FromValue: ptr(f), ToValue: ptr(to), CreatedAt: testClock}
		ops = append(ops, application.TransitionOperation{StepIndex: stepIdx, Audit: ev})
	}
	return ops
}

// TestWorkflowUoW_Apply_TerminalMatrix is the EXACT resolve/close state/step
// matrix over every starting ticket state, including wrong-but-domain-legal
// transitions (a legal Ticket.Transition that is the WRONG outcome for the step)
// and cancelled rejection. Every non-matched transition/state must be a typed
// conflict with zero writes; every matched one persists exactly the expected
// transition and completed-run facts.
func TestWorkflowUoW_Apply_TerminalMatrix(t *testing.T) {
	now := testClock
	nowStr := formatTime(now)
	startedStr := nowStr
	cases := []struct {
		name       string
		stepType   domain.StepType
		startState domain.State
		empty      bool // no-op (already-terminal) path: zero transitions
		fromTo     [][2]string
		nextState  domain.State
		wantErr    bool
		wantErrSub string
	}{
		{"resolve-from-new", domain.StepResolve, domain.StateNew, false, [][2]string{{"new", "resolved"}}, domain.StateResolved, false, ""},
		{"resolve-from-in-progress", domain.StepResolve, domain.StateInProgress, false, [][2]string{{"in_progress", "resolved"}}, domain.StateResolved, false, ""},
		{"resolve-from-resolved-noop", domain.StepResolve, domain.StateResolved, true, nil, domain.StateResolved, false, ""},
		{"resolve-from-closed-noop", domain.StepResolve, domain.StateClosed, true, nil, domain.StateClosed, false, ""},
		{"resolve-from-cancelled-rejected", domain.StepResolve, domain.StateCancelled, true, nil, domain.StateCancelled, true, ""},
		{"resolve-wrong-new-to-in-progress", domain.StepResolve, domain.StateNew, false, [][2]string{{"new", "in_progress"}}, domain.StateInProgress, true, ""},
		{"resolve-wrong-new-to-cancelled", domain.StepResolve, domain.StateNew, false, [][2]string{{"new", "cancelled"}}, domain.StateCancelled, true, ""},
		{"resolve-wrong-in-progress-to-cancelled", domain.StepResolve, domain.StateInProgress, false, [][2]string{{"in_progress", "cancelled"}}, domain.StateCancelled, true, ""},
		{"close-from-new", domain.StepClose, domain.StateNew, false, [][2]string{{"new", "resolved"}, {"resolved", "closed"}}, domain.StateClosed, false, ""},
		{"close-from-in-progress", domain.StepClose, domain.StateInProgress, false, [][2]string{{"in_progress", "resolved"}, {"resolved", "closed"}}, domain.StateClosed, false, ""},
		{"close-from-resolved", domain.StepClose, domain.StateResolved, false, [][2]string{{"resolved", "closed"}}, domain.StateClosed, false, ""},
		{"close-from-closed-noop", domain.StepClose, domain.StateClosed, true, nil, domain.StateClosed, false, ""},
		{"close-from-cancelled-rejected", domain.StepClose, domain.StateCancelled, true, nil, domain.StateCancelled, true, ""},
		{"close-wrong-partial-resolve-only", domain.StepClose, domain.StateNew, false, [][2]string{{"new", "resolved"}}, domain.StateResolved, true, ""},
		{"close-wrong-claim-like", domain.StepClose, domain.StateNew, false, [][2]string{{"new", "in_progress"}, {"in_progress", "resolved"}}, domain.StateResolved, true, ""},
		{"close-wrong-reopen-seq", domain.StepClose, domain.StateResolved, false, [][2]string{{"resolved", "closed"}, {"closed", "in_progress"}}, domain.StateInProgress, true, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestDB(t)
			cat := seedCategory(t, s, "C1")
			req := seedUser(t, s, "Req", "r@x", true)
			agent := seedUser(t, s, "Ag", "a@x", true)
			def := domain.WorkflowDefinition{{Type: tc.stepType}}
			vid := seedPublished(t, s, cat, def)
			v := vid
			var resAt, clAt *time.Time
			switch tc.startState {
			case domain.StateResolved:
				resAt = &now
			case domain.StateClosed:
				resAt = &now // a closed ticket was resolved first in the domain matrix
				clAt = &now
			}
			tk := seedPinnedTicket(t, s, domain.Ticket{Number: 71, Title: "T", Description: "", RequesterName: "Req", RequesterEmail: "r@x", RequesterUserID: &req, CategoryID: cat, Priority: domain.PriorityMedium, State: tc.startState, UserID: &agent, CreatedAt: now, UpdatedAt: now, ResolvedAt: resAt, ClosedAt: clAt, WorkflowVersionID: &v})
			seedRun(t, s, tk.ID, 0, "active", now)
			ctx := context.Background()
			var ops []application.WorkflowOperation
			if !tc.empty {
				ops = mkTransitions(tk.ID, 0, tc.fromTo...)
			}
			ct := now
			plan := buildApplyPlan(tk, vid, def, agent, "Ag", ops, 1, "completed", tc.nextState, &agent, &ct)
			_, err := newWorkflowUnitOfWork(s.db).ApplyWorkflowPlan(ctx, plan)
			if tc.wantErr {
				if !errors.Is(err, domain.ErrWorkflowPositionConflict) {
					t.Fatalf("terminal matrix %s must conflict, got %v", tc.name, err)
				}
				assertApplyNoWrites(t, s, tk)
				return
			}
			if err != nil {
				t.Fatalf("terminal matrix %s must succeed, got %v", tc.name, err)
			}
			// Exhaustive persisted evidence for every success row.
			var wantAudits []fullAuditRow
			for _, p := range tc.fromTo {
				wantAudits = append(wantAudits, fullAuditRow{
					ticketID: tk.ID, actor: "workflow", actorUserID: nil, action: "transition",
					field: sptr("state"), fromValue: sptr(p[0]), toValue: sptr(p[1]),
					reason: nil, note: nil, createdAt: nowStr,
				})
			}
			got := readFullAudits(t, s, tk.ID)
			if len(got) != len(wantAudits) {
				t.Fatalf("terminal matrix %s audits = %d rows, want %d", tc.name, len(got), len(wantAudits))
			}
			for i := range wantAudits {
				requireFullAudit(t, i, got[i], wantAudits[i])
			}
			var resolved, closed *string
			if tc.nextState == domain.StateResolved {
				resolved = sptr(nowStr)
			}
			if tc.nextState == domain.StateClosed {
				resolved = sptr(nowStr)
				closed = sptr(nowStr)
			}
			requireTicketAndRun(t, s, tk.ID,
				ticketFacts{state: string(tc.nextState), assignee: &agent, resolvedAt: resolved, closedAt: closed, updatedAt: nowStr},
				runFacts{cursor: 1, status: "completed", started: startedStr, completed: sptr(nowStr)})
		})
	}
}

// TestWorkflowUoW_Apply_ClaimInProgressRedundantTransitionRejected — UNMASKED:
// two INDEPENDENT in_progress same-person claim plans with run cursor and
// ExpectedCursor ALIGNED so the redundant-transition decision reaches the grammar
// validator (not a stale cursor precheck). The valid claim is the lone contextual
// assignment; a transition placed where the claim's transition slot is checked is
// rejected as "claim transition on non-new step" with zero writes and the message
// surfaced.
func TestWorkflowUoW_Apply_ClaimInProgressRedundantTransitionRejected(t *testing.T) {
	now := testClock
	s := newTestDB(t)
	cat := seedCategory(t, s, "C1")
	req := seedUser(t, s, "Req", "r@x", true)
	agent := seedUser(t, s, "Ag", "a@x", true)
	deskID := seedDeskWithMember(t, s, agent)
	def := claimDef(deskID)
	vid := seedPublished(t, s, cat, def)

	// 1) Valid same-person in_progress claim: [ClaimAssignment(same-person)]
	//    (no redundant transition) succeeds with exactly ONE contextual audit —
	//    the claim applies as a user-field no-op on the copy while the structured
	//    workflow_assignment row (from==to) is still persisted.
	t.Run("valid-same-person-claim-op", func(t *testing.T) {
		v := vid
		tk := seedPinnedTicket(t, s, domain.Ticket{Number: 312, Title: "T", Description: "", RequesterName: "Req", RequesterEmail: "r@x", RequesterUserID: &req, CategoryID: cat, Priority: domain.PriorityMedium, State: domain.StateInProgress, UserID: &agent, CreatedAt: now, UpdatedAt: now, WorkflowVersionID: &v})
		seedRun(t, s, tk.ID, 0, "active", now)
		ops := samePersonClaimOps(tk, deskID, agent, now)
		plan := buildApplyPlan(tk, vid, def, agent, "Ag", ops, 1, "active", domain.StateInProgress, &agent, nil)
		if _, err := newWorkflowUnitOfWork(s.db).ApplyWorkflowPlan(context.Background(), plan); err != nil {
			t.Fatalf("valid in_progress claim (no transition) must succeed, got %v", err)
		}
		got := readFullAudits(t, s, tk.ID)
		if len(got) != 1 {
			t.Fatalf("valid claim audits = %d, want 1", len(got))
		}
		requireFullAudit(t, 0, got[0], fullAuditRow{
			ticketID: tk.ID, actor: "Ag", actorUserID: &agent, action: "workflow_assignment",
			field: ptr("user"), fromValue: ptr(strconv.FormatInt(agent, 10)), toValue: ptr(strconv.FormatInt(agent, 10)),
			reason: nil, note: nil, createdAt: formatTime(now),
		})
		requireTicketAndRun(t, s, tk.ID,
			ticketFacts{state: "in_progress", assignee: &agent, updatedAt: formatTime(now)},
			runFacts{cursor: 1, status: "active", started: formatTime(now)})
	})

	// 2) Redundant transition on a NON-new claim reaches the grammar validator:
	//    a domain-legal in_progress->resolved transition in the claim's transition
	//    slot is rejected as "claim transition on non-new step" with ZERO writes.
	t.Run("redundant-transition-reaches-grammar", func(t *testing.T) {
		v := vid
		tk := seedPinnedTicket(t, s, domain.Ticket{Number: 313, Title: "T", Description: "", RequesterName: "Req", RequesterEmail: "r@x", RequesterUserID: &req, CategoryID: cat, Priority: domain.PriorityMedium, State: domain.StateInProgress, UserID: &agent, CreatedAt: now, UpdatedAt: now, WorkflowVersionID: &v})
		seedRun(t, s, tk.ID, 0, "active", now)
		tr := domain.AuditEvent{TicketID: tk.ID, Actor: "workflow", Action: domain.ActionTransition, Field: ptr("state"), FromValue: ptr("in_progress"), ToValue: ptr("resolved"), CreatedAt: now}
		// The claim group starts with the same-person ClaimAssignmentOperation (the
		// runner always preserves it); a redundant in_progress->resolved transition
		// placed in the claim's transition slot must be rejected by the grammar rule.
		field := "user"
		f := strconv.FormatInt(agent, 10)
		ca := domain.AuditEvent{TicketID: tk.ID, Actor: "Ag", ActorUserID: &agent, Action: domain.ActionWorkflowAssignment, Field: &field, FromValue: &f, ToValue: &f, DeskID: &deskID, CreatedAt: now}
		ops := []application.WorkflowOperation{
			application.ClaimAssignmentOperation{StepIndex: 0, DeskID: deskID, AssigneeUserID: agent, AssignmentAudit: ca},
			application.TransitionOperation{StepIndex: 0, Audit: tr},
		}
		// ExpectedCursor stays 0 == the seeded run cursor 0, so the stale precheck
		// does NOT fire and the grammar validator decides the redundant transition.
		plan := buildApplyPlan(tk, vid, def, agent, "Ag", ops, 1, "active", domain.StateInProgress, &agent, nil)
		_, err := newWorkflowUnitOfWork(s.db).ApplyWorkflowPlan(context.Background(), plan)
		if !errors.Is(err, domain.ErrWorkflowPositionConflict) {
			t.Fatalf("in_progress claim with redundant transition must conflict, got %v", err)
		}
		var wpc *domain.WorkflowPositionConflictError
		if errors.As(err, &wpc) && !strings.Contains(wpc.Message, "claim transition on non-new step") {
			t.Fatalf("redundant transition must be rejected by the grammar rule, got message %q", wpc.Message)
		}
		// Zero writes: no audits, run cursor/status unchanged, ticket unchanged.
		var audits int
		_ = s.db.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE ticket_id=?`, tk.ID).Scan(&audits)
		if audits != 0 {
			t.Fatalf("redundant claim wrote %d audits, want 0", audits)
		}
		if cur, status, _ := runRow(t, s, tk.ID); cur != 0 || status != "active" {
			t.Fatalf("redundant claim changed run: cur=%d status=%s, want 0/active", cur, status)
		}
		requireTicketAndRun(t, s, tk.ID,
			ticketFacts{state: "in_progress", assignee: &agent, updatedAt: formatTime(now)},
			runFacts{cursor: 0, status: "active", started: formatTime(now)})
	})
}

// TestWorkflowUoW_Apply_ExhaustiveClaimAuditEvidence queries EVERY persisted
// audit column for the three-op claim result and the full ticket/run facts.
func TestWorkflowUoW_Apply_ExhaustiveClaimAuditEvidence(t *testing.T) {
	s := newTestDB(t)
	cat := seedCategory(t, s, "C1")
	req := seedUser(t, s, "Req", "r@x", true)
	agent := seedUser(t, s, "Ag", "a@x", true)
	deskID := seedDeskWithMember(t, s, agent)
	def := claimDef(deskID)
	vid := seedPublished(t, s, cat, def)
	now := testClock
	nowStr := formatTime(now)
	tk := seedPinnedTicket(t, s, domain.Ticket{Number: 80, Title: "T", Description: "", RequesterName: "Req", RequesterEmail: "r@x", RequesterUserID: &req, CategoryID: cat, Priority: domain.PriorityMedium, State: domain.StateNew, CreatedAt: now, UpdatedAt: now, WorkflowVersionID: &vid})
	seedRun(t, s, tk.ID, 0, "active", now)
	ctx := context.Background()
	plan := buildApplyPlan(tk, vid, def, agent, "Ag", applyClaimOps(t, now, tk, agent, deskID), 1, "active", domain.StateInProgress, &agent, nil)
	if _, err := newWorkflowUnitOfWork(s.db).ApplyWorkflowPlan(ctx, plan); err != nil {
		t.Fatalf("claim apply: %v", err)
	}
	agentStr := strconv.FormatInt(agent, 10)
	got := readFullAudits(t, s, tk.ID)
	want := []fullAuditRow{
		{ticketID: tk.ID, actor: "Ag", actorUserID: &agent, action: "workflow_assignment", field: sptr("user"), fromValue: sptr(""), toValue: sptr(agentStr), deskID: &deskID, reason: nil, note: nil, createdAt: nowStr},
		{ticketID: tk.ID, actor: "workflow", actorUserID: nil, action: "transition", field: sptr("state"), fromValue: sptr("new"), toValue: sptr("in_progress"), reason: nil, note: nil, createdAt: nowStr},
	}
	if len(got) != len(want) {
		t.Fatalf("claim audits = %d rows, want %d (contextual assignment + transition)", len(got), len(want))
	}
	for i := range want {
		requireFullAudit(t, i, got[i], want[i])
	}
	requireTicketAndRun(t, s, tk.ID,
		ticketFacts{state: "in_progress", assignee: &agent, updatedAt: nowStr},
		runFacts{cursor: 1, status: "active", started: nowStr})
}

// TestWorkflowUoW_Apply_ExhaustiveFormAuditEvidence queries EVERY persisted
// audit column + the full answer row and ticket/run facts for a form completion.
func TestWorkflowUoW_Apply_ExhaustiveFormAuditEvidence(t *testing.T) {
	s := newTestDB(t)
	cat := seedCategory(t, s, "C1")
	req := seedUser(t, s, "Req", "r@x", true)
	def := formDef()
	vid := seedPublished(t, s, cat, def)
	now := testClock
	nowStr := formatTime(now)
	tk := seedPinnedTicket(t, s, domain.Ticket{Number: 81, Title: "T", Description: "", RequesterName: "Req", RequesterEmail: "r@x", RequesterUserID: &req, CategoryID: cat, Priority: domain.PriorityMedium, State: domain.StateNew, CreatedAt: now, UpdatedAt: now, WorkflowVersionID: &vid})
	seedRun(t, s, tk.ID, 0, "active", now)
	ctx := context.Background()
	plan := buildApplyPlan(tk, vid, def, req, "A", applyFormOps(now, tk, req, `["spec-value"]`), 1, "active", domain.StateNew, nil, nil)
	if _, err := newWorkflowUnitOfWork(s.db).ApplyWorkflowPlan(ctx, plan); err != nil {
		t.Fatalf("form apply: %v", err)
	}
	got := readFullAudits(t, s, tk.ID)
	want := []fullAuditRow{
		{ticketID: tk.ID, actor: "A", actorUserID: &req, action: "workflow_requester_form", field: nil, fromValue: nil, toValue: nil, reason: nil, note: nil, createdAt: nowStr},
	}
	if len(got) != len(want) {
		t.Fatalf("form audits = %d rows, want %d", len(got), len(want))
	}
	for i := range want {
		requireFullAudit(t, i, got[i], want[i])
	}
	// Full answer row: step_index, answers_json, submitted_by_user_id, submitted_at.
	var step int
	var answers string
	var by sql.NullInt64
	var at string
	if err := s.db.QueryRow(`SELECT step_index, answers_json, submitted_by_user_id, submitted_at FROM ticket_form_answers WHERE ticket_id=?`, tk.ID).Scan(&step, &answers, &by, &at); err != nil {
		t.Fatalf("read answer row: %v", err)
	}
	if step != 0 || answers != `["spec-value"]` || !by.Valid || by.Int64 != req || at != nowStr {
		t.Fatalf("answer row = step %d json %s by %v at %q, want 0/[\"spec-value\"]/%d/%q", step, answers, by, at, req, nowStr)
	}
	requireTicketAndRun(t, s, tk.ID,
		ticketFacts{state: "new", updatedAt: nowStr},
		runFacts{cursor: 1, status: "active", started: nowStr})
}

// TestWorkflowUoW_Apply_ExhaustiveManualAuditEvidence queries EVERY persisted
// audit column + full ticket/run facts for a manual completion.
func TestWorkflowUoW_Apply_ExhaustiveManualAuditEvidence(t *testing.T) {
	s := newTestDB(t)
	cat := seedCategory(t, s, "C1")
	req := seedUser(t, s, "Req", "r@x", true)
	agent := seedUser(t, s, "Ag", "a@x", true)
	def := manualTaskDef()
	vid := seedPublished(t, s, cat, def)
	now := testClock
	nowStr := formatTime(now)
	v := vid
	tk := seedPinnedTicket(t, s, domain.Ticket{Number: 82, Title: "T", Description: "", RequesterName: "Req", RequesterEmail: "r@x", RequesterUserID: &req, CategoryID: cat, Priority: domain.PriorityMedium, State: domain.StateNew, UserID: &agent, CreatedAt: now, UpdatedAt: now, WorkflowVersionID: &v})
	seedRun(t, s, tk.ID, 0, "active", now)
	ctx := context.Background()
	plan := buildApplyPlan(tk, vid, def, agent, "A", applyManualOps(now, tk, agent), 1, "active", domain.StateNew, &agent, nil)
	if _, err := newWorkflowUnitOfWork(s.db).ApplyWorkflowPlan(ctx, plan); err != nil {
		t.Fatalf("manual apply: %v", err)
	}
	got := readFullAudits(t, s, tk.ID)
	want := []fullAuditRow{
		{ticketID: tk.ID, actor: "A", actorUserID: &agent, action: "workflow_manual_task", field: nil, fromValue: nil, toValue: nil, reason: nil, note: nil, createdAt: nowStr},
	}
	if len(got) != len(want) {
		t.Fatalf("manual audits = %d rows, want %d", len(got), len(want))
	}
	for i := range want {
		requireFullAudit(t, i, got[i], want[i])
	}
	requireTicketAndRun(t, s, tk.ID,
		ticketFacts{state: "new", assignee: &agent, updatedAt: nowStr},
		runFacts{cursor: 1, status: "active", started: nowStr})
}

// TestWorkflowUoW_Apply_ExhaustiveResolveAuditEvidence queries EVERY persisted
// audit column + resolved_at/updated_at + run completion for a resolve from
// in_progress.
func TestWorkflowUoW_Apply_ExhaustiveResolveAuditEvidence(t *testing.T) {
	s := newTestDB(t)
	cat := seedCategory(t, s, "C1")
	req := seedUser(t, s, "Req", "r@x", true)
	agent := seedUser(t, s, "Ag", "a@x", true)
	def := domain.WorkflowDefinition{{Type: domain.StepResolve}}
	vid := seedPublished(t, s, cat, def)
	now := testClock
	nowStr := formatTime(now)
	v := vid
	tk := seedPinnedTicket(t, s, domain.Ticket{Number: 83, Title: "T", Description: "", RequesterName: "Req", RequesterEmail: "r@x", RequesterUserID: &req, CategoryID: cat, Priority: domain.PriorityMedium, State: domain.StateInProgress, UserID: &agent, CreatedAt: now, UpdatedAt: now, WorkflowVersionID: &v})
	seedRun(t, s, tk.ID, 0, "active", now)
	ctx := context.Background()
	ops := mkTransitions(tk.ID, 0, [2]string{"in_progress", "resolved"})
	ct := now
	plan := buildApplyPlan(tk, vid, def, agent, "Ag", ops, 1, "completed", domain.StateResolved, &agent, &ct)
	if _, err := newWorkflowUnitOfWork(s.db).ApplyWorkflowPlan(ctx, plan); err != nil {
		t.Fatalf("resolve apply: %v", err)
	}
	got := readFullAudits(t, s, tk.ID)
	want := []fullAuditRow{
		{ticketID: tk.ID, actor: "workflow", actorUserID: nil, action: "transition", field: sptr("state"), fromValue: sptr("in_progress"), toValue: sptr("resolved"), reason: nil, note: nil, createdAt: nowStr},
	}
	if len(got) != len(want) {
		t.Fatalf("resolve audits = %d rows, want %d", len(got), len(want))
	}
	for i := range want {
		requireFullAudit(t, i, got[i], want[i])
	}
	requireTicketAndRun(t, s, tk.ID,
		ticketFacts{state: "resolved", assignee: &agent, resolvedAt: sptr(nowStr), updatedAt: nowStr},
		runFacts{cursor: 1, status: "completed", started: nowStr, completed: sptr(nowStr)})
}

// TestWorkflowUoW_Apply_ExhaustiveCloseAuditEvidence queries EVERY persisted
// audit column for the two ordered close transitions + closed_at + run.
func TestWorkflowUoW_Apply_ExhaustiveCloseAuditEvidence(t *testing.T) {
	s := newTestDB(t)
	cat := seedCategory(t, s, "C1")
	req := seedUser(t, s, "Req", "r@x", true)
	agent := seedUser(t, s, "Ag", "a@x", true)
	def := domain.WorkflowDefinition{{Type: domain.StepClose}}
	vid := seedPublished(t, s, cat, def)
	now := testClock
	nowStr := formatTime(now)
	v := vid
	tk := seedPinnedTicket(t, s, domain.Ticket{Number: 84, Title: "T", Description: "", RequesterName: "Req", RequesterEmail: "r@x", RequesterUserID: &req, CategoryID: cat, Priority: domain.PriorityMedium, State: domain.StateNew, UserID: &agent, CreatedAt: now, UpdatedAt: now, WorkflowVersionID: &v})
	seedRun(t, s, tk.ID, 0, "active", now)
	ctx := context.Background()
	ops := mkTransitions(tk.ID, 0, [2]string{"new", "resolved"}, [2]string{"resolved", "closed"})
	ct := now
	plan := buildApplyPlan(tk, vid, def, agent, "Ag", ops, 1, "completed", domain.StateClosed, &agent, &ct)
	if _, err := newWorkflowUnitOfWork(s.db).ApplyWorkflowPlan(ctx, plan); err != nil {
		t.Fatalf("close apply: %v", err)
	}
	got := readFullAudits(t, s, tk.ID)
	want := []fullAuditRow{
		{ticketID: tk.ID, actor: "workflow", actorUserID: nil, action: "transition", field: sptr("state"), fromValue: sptr("new"), toValue: sptr("resolved"), reason: nil, note: nil, createdAt: nowStr},
		{ticketID: tk.ID, actor: "workflow", actorUserID: nil, action: "transition", field: sptr("state"), fromValue: sptr("resolved"), toValue: sptr("closed"), reason: nil, note: nil, createdAt: nowStr},
	}
	if len(got) != len(want) {
		t.Fatalf("close audits = %d rows, want %d", len(got), len(want))
	}
	for i := range want {
		requireFullAudit(t, i, got[i], want[i])
	}
	// close from new stamps resolved_at (intermediate resolve) AND closed_at.
	requireTicketAndRun(t, s, tk.ID,
		ticketFacts{state: "closed", assignee: &agent, resolvedAt: sptr(nowStr), closedAt: sptr(nowStr), updatedAt: nowStr},
		runFacts{cursor: 1, status: "completed", started: nowStr, completed: sptr(nowStr)})
}

// TestWorkflowUoW_SamePlanRetryAfterStaleConflict proves the SAME immutable plan
// object: a first Apply fails with a typed stale conflict and ZERO writes; after
// fixing ONLY the stale precondition (the persisted run cursor), retrying the SAME
// plan succeeds with exactly ONE copy of every audit/answer/cursor — no duplicates.
func TestWorkflowUoW_SamePlanRetryAfterStaleConflict(t *testing.T) {
	s := newTestDB(t)
	cat := seedCategory(t, s, "C1")
	req := seedUser(t, s, "Req", "r@x", true)
	agent := seedUser(t, s, "Ag", "a@x", true)
	def := manualTaskDef()
	vid := seedPublished(t, s, cat, def)
	now := testClock
	nowStr := formatTime(now)
	v := vid
	tk := seedPinnedTicket(t, s, domain.Ticket{Number: 85, Title: "T", Description: "", RequesterName: "Req", RequesterEmail: "r@x", RequesterUserID: &req, CategoryID: cat, Priority: domain.PriorityMedium, State: domain.StateNew, UserID: &agent, CreatedAt: now, UpdatedAt: now, WorkflowVersionID: &v})
	// Seed the run at cursor 1 — stale vs the plan's expected cursor 0.
	seedRun(t, s, tk.ID, 1, "active", now)
	ctx := context.Background()
	plan := buildApplyPlan(tk, vid, def, agent, "A", applyManualOps(now, tk, agent), 1, "active", domain.StateNew, &agent, nil)
	uow := newWorkflowUnitOfWork(s.db)

	// First Apply: run is at cursor 1 but the plan expects cursor 0 -> stale conflict.
	if _, err := uow.ApplyWorkflowPlan(ctx, plan); !errors.Is(err, domain.ErrWorkflowPositionConflict) {
		t.Fatalf("first stale apply must conflict, got %v", err)
	}
	// ZERO writes: no audits, no answers, run cursor still 1 (unmoved), state unchanged.
	var audits, answers int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE ticket_id=?`, tk.ID).Scan(&audits)
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM ticket_form_answers WHERE ticket_id=?`, tk.ID).Scan(&answers)
	if audits != 0 || answers != 0 {
		t.Fatalf("first stale apply wrote audits=%d answers=%d, want 0/0", audits, answers)
	}
	if cur, status, _ := runRow(t, s, tk.ID); cur != 1 || status != "active" {
		t.Fatalf("first stale apply moved run: cur=%d status=%s, want 1/active", cur, status)
	}

	// Fix ONLY the stale precondition: realign the persisted run cursor to 0.
	if _, err := s.db.Exec(`UPDATE ticket_workflow_runs SET current_step_index=0 WHERE ticket_id=?`, tk.ID); err != nil {
		t.Fatalf("fix cursor: %v", err)
	}
	// Retry the SAME immutable plan object -> succeeds with exactly one audit,
	// one cursor advance (0 -> 1), state unchanged.
	if _, err := uow.ApplyWorkflowPlan(ctx, plan); err != nil {
		t.Fatalf("retry of the same plan must succeed, got %v", err)
	}
	got := readFullAudits(t, s, tk.ID)
	if len(got) != 1 {
		t.Fatalf("retry produced %d audits, want exactly 1 (no duplicate writes)", len(got))
	}
	requireFullAudit(t, 0, got[0], fullAuditRow{
		ticketID: tk.ID, actor: "A", actorUserID: &agent, action: "workflow_manual_task",
		field: nil, fromValue: nil, toValue: nil, reason: nil, note: nil, createdAt: nowStr,
	})
	var ansAfter int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM ticket_form_answers WHERE ticket_id=?`, tk.ID).Scan(&ansAfter)
	if ansAfter != 0 {
		t.Fatalf("manual retry wrote answers=%d, want 0", ansAfter)
	}
	requireTicketAndRun(t, s, tk.ID,
		ticketFacts{state: "new", assignee: &agent, updatedAt: nowStr},
		runFacts{cursor: 1, status: "active", started: nowStr})
}

// TestWorkflowUoW_Create_ExhaustiveCloseAuditEvidence covers the CREATE auto-advance
// close path (new -> resolved -> closed, two transitions) with full audit + run
// evidence, proving the tightened create automatic-terminal matrix.
func TestWorkflowUoW_Create_ExhaustiveCloseAuditEvidence(t *testing.T) {
	s := newTestDB(t)
	cat := seedCategory(t, s, "C1")
	req := seedUser(t, s, "Req", "r@x", true)
	def := domain.WorkflowDefinition{{Type: domain.StepClose}}
	vid := seedPublished(t, s, cat, def)
	now := testClock
	nowStr := formatTime(now)
	ops := mkTransitions(0, 0, [2]string{"new", "resolved"}, [2]string{"resolved", "closed"})
	ct := now
	in := buildCreateInput(cat, vid, req, def, ops, 1, "completed", domain.StateClosed, &ct)
	tk, err := newWorkflowUnitOfWork(s.db).CreateTicketWithRun(context.Background(), in)
	if err != nil {
		t.Fatalf("create close: %v", err)
	}
	got := readFullAudits(t, s, tk.ID)
	want := []fullAuditRow{
		{ticketID: tk.ID, actor: "Req", actorUserID: &req, action: "created", field: nil, fromValue: nil, toValue: nil, reason: nil, note: nil, createdAt: nowStr},
		{ticketID: tk.ID, actor: "workflow", actorUserID: nil, action: "transition", field: sptr("state"), fromValue: sptr("new"), toValue: sptr("resolved"), reason: nil, note: nil, createdAt: nowStr},
		{ticketID: tk.ID, actor: "workflow", actorUserID: nil, action: "transition", field: sptr("state"), fromValue: sptr("resolved"), toValue: sptr("closed"), reason: nil, note: nil, createdAt: nowStr},
	}
	if len(got) != len(want) {
		t.Fatalf("create close audits = %d rows, want %d", len(got), len(want))
	}
	for i := range want {
		requireFullAudit(t, i, got[i], want[i])
	}
	requireTicketAndRun(t, s, tk.ID,
		ticketFacts{state: "closed", assignee: nil, resolvedAt: sptr(nowStr), closedAt: sptr(nowStr), updatedAt: nowStr},
		runFacts{cursor: 1, status: "completed", started: nowStr, completed: sptr(nowStr)})
}

// ---------------------------------------------------------------
// Blocker 1: create empty-operation path MUST validate the current step's matrix
// instead of early-returning on cursor alone. A terminal automatic step
// auto-advances and completes the run: empty ops are valid ONLY as an
// already-state no-op (resolve from resolved/closed; close from closed). From
// new/in_progress (and resolved for close) a terminal step REQUIRES its exact
// transition(s); cancelled always rejects. A human-pending step remains a valid
// create-time wait.
func TestWorkflowUoW_Create_EmptyOpsTerminalMatrix(t *testing.T) {
	cases := []struct {
		name         string
		stepType     domain.StepType
		ticketState  domain.State
		wantConflict bool
	}{
		{"resolve from new", domain.StepResolve, domain.StateNew, true},
		{"resolve from in_progress", domain.StepResolve, domain.StateInProgress, true},
		{"resolve from cancelled", domain.StepResolve, domain.StateCancelled, true},
		{"resolve from resolved no-op", domain.StepResolve, domain.StateResolved, false},
		{"resolve from closed no-op", domain.StepResolve, domain.StateClosed, false},
		{"close from new", domain.StepClose, domain.StateNew, true},
		{"close from in_progress", domain.StepClose, domain.StateInProgress, true},
		{"close from resolved", domain.StepClose, domain.StateResolved, true},
		{"close from cancelled", domain.StepClose, domain.StateCancelled, true},
		{"close from closed no-op", domain.StepClose, domain.StateClosed, false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestDB(t)
			cat := seedCategory(t, s, "C1")
			req := seedUser(t, s, "Req", "r@x", true)
			def := domain.WorkflowDefinition{{Type: tt.stepType}}
			vid := seedPublished(t, s, cat, def)
			ctx := context.Background()
			now := testClock
			tk := &domain.Ticket{
				Title: "T", Description: "", RequesterName: "Req", RequesterEmail: "r@x",
				RequesterUserID: &req, CategoryID: cat, Priority: domain.PriorityMedium,
				State: tt.ticketState, CreatedAt: now, UpdatedAt: now,
			}
			tk.WorkflowVersionID = &vid
			created := domain.AuditEvent{Actor: "Req", ActorUserID: &req, Action: domain.ActionCreated, CreatedAt: now}
			// A terminal-needing-transition / cancelled case is a wrong "wait" (empty
			// ops, cursor unchanged, active); a valid already-state no-op completes the
			// run over the terminal final step.
			nextCursor := 0
			nextStatus := "active"
			var completedAt *time.Time
			if !tt.wantConflict {
				nextCursor = 1
				nextStatus = "completed"
				completedAt = &now
			}
			in := application.CreateTicketWithRunInput{
				CategoryID: cat, ExpectedVersionID: vid, Workflow: def.Clone(), Ticket: tk,
				CreatedAudit: created, StartedAt: now, ExpectedCursor: 0, ExpectedRunStatus: "active",
				NextCursor: nextCursor, NextRunStatus: nextStatus,
				NextTicketState: tt.ticketState, CompletedAt: completedAt,
			}
			tkOut, err := newWorkflowUnitOfWork(s.db).CreateTicketWithRun(ctx, in)
			if tt.wantConflict {
				if !errors.Is(err, domain.ErrWorkflowPositionConflict) {
					t.Fatalf("empty ops at %s from %s must conflict (or be refused), got %v", tt.stepType, tt.ticketState, err)
				}
				assertTotalRollback(t, s)
				return
			}
			if err != nil {
				t.Fatalf("empty-ops no-op completion %s from %s: %v", tt.stepType, tt.ticketState, err)
			}
			if tkOut.State != tt.ticketState {
				t.Fatalf("no-op ticket state = %s, want %s (unchanged)", tkOut.State, tt.ticketState)
			}
			if got := auditActionOrder(t, s, tkOut.ID); len(got) != 1 || got[0] != "created||" {
				t.Fatalf("no-op audits = %v, want [created]", got)
			}
			cur, status, comp := runRow(t, s, tkOut.ID)
			if cur != 1 || status != "completed" || comp == nil {
				t.Fatalf("no-op run = cur %d status %s completed %v, want 1/completed/set", cur, status, comp)
			}
		})
	}
}

// ---------------------------------------------------------------
// Human-pending (claim/form/manual) empty create: the run starts awaiting the
// FIRST human step and must stay there untouched. Initial waiting facts are
// EXACT — ExpectedCursor==0, ExpectedRunStatus==active, NextCursor==0,
// NextRunStatus==active, CompletedAt nil, NextTicketState unchanged. Every
// forged dimension is rejected independently with a typed conflict + total
// rollback (design S5 all-or-nothing).
func TestWorkflowUoW_Create_EmptyOpsHumanPendingMatrix(t *testing.T) {
	// A two-manual-step definition keeps a forged "cursor > 0" (the human step
	// at index 1) in-range and meaningful instead of falling into the
	// out-of-range branch.
	twoManual := domain.WorkflowDefinition{
		{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "a"}},
		{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "b"}},
	}
	cases := []struct {
		name   string
		def    domain.WorkflowDefinition
		mutate func(*application.CreateTicketWithRunInput, time.Time)
	}{
		{"cursor > 0", twoManual, func(in *application.CreateTicketWithRunInput, _ time.Time) { in.ExpectedCursor = 1 }},
		{"expected run status not active", manualTaskDef(), func(in *application.CreateTicketWithRunInput, _ time.Time) { in.ExpectedRunStatus = "paused" }},
		{"cursor advance", manualTaskDef(), func(in *application.CreateTicketWithRunInput, _ time.Time) { in.NextCursor = 1 }},
		{"completed status", manualTaskDef(), func(in *application.CreateTicketWithRunInput, now time.Time) {
			in.NextRunStatus = "completed"
			in.CompletedAt = &now
		}},
		{"completed time while active", manualTaskDef(), func(in *application.CreateTicketWithRunInput, now time.Time) { in.CompletedAt = &now }},
		{"forged next state", manualTaskDef(), func(in *application.CreateTicketWithRunInput, _ time.Time) {
			in.NextTicketState = domain.StateInProgress
		}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestDB(t)
			cat := seedCategory(t, s, "C1")
			req := seedUser(t, s, "Req", "r@x", true)
			vid := seedPublished(t, s, cat, tt.def)
			in := buildCreateInput(cat, vid, req, tt.def, nil, 0, "active", domain.StateNew, nil)
			tt.mutate(&in, testClock)
			_, err := newWorkflowUnitOfWork(s.db).CreateTicketWithRun(context.Background(), in)
			if !errors.Is(err, domain.ErrWorkflowPositionConflict) {
				t.Fatalf("forged human-pending waiting fact %q must conflict, got %v", tt.name, err)
			}
			assertTotalRollback(t, s)
		})
	}
}

// Positive: an empty create plan whose current step is a human step
// (claim/form/manual) is a valid create-time wait. The run starts and stays at
// cursor 0 active, no completion time, ticket state/assignee unchanged, only
// the created audit persisted.
func TestWorkflowUoW_Create_EmptyOpsHumanPendingAccepted(t *testing.T) {
	cases := []struct {
		name string
	}{
		{"manual"},
		{"form"},
		{"claim"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestDB(t)
			cat := seedCategory(t, s, "C1")
			req := seedUser(t, s, "Req", "r@x", true)
			var def domain.WorkflowDefinition
			switch tt.name {
			case "manual":
				def = manualTaskDef()
			case "form":
				def = formDef()
			case "claim":
				def = claimDef(seedDeskWithMember(t, s, req))
			}
			vid := seedPublished(t, s, cat, def)
			in := buildCreateInput(cat, vid, req, def, nil, 0, "active", domain.StateNew, nil)
			tkOut, err := newWorkflowUnitOfWork(s.db).CreateTicketWithRun(context.Background(), in)
			if err != nil {
				t.Fatalf("human-pending empty create %s: %v", tt.name, err)
			}
			if tkOut.State != domain.StateNew {
				t.Fatalf("human-pending create state = %s, want new (unchanged)", tkOut.State)
			}
			if got := auditActionOrder(t, s, tkOut.ID); len(got) != 1 || got[0] != "created||" {
				t.Fatalf("human-pending audits = %v, want [created]", got)
			}
			cur, status, comp := runRow(t, s, tkOut.ID)
			if cur != 0 || status != "active" || comp != nil {
				t.Fatalf("human-pending run = cur %d status %s completed %v, want 0/active/nil", cur, status, comp)
			}
			state, assigned, _ := ticketRow(t, s, tkOut.ID)
			if state != string(domain.StateNew) || assigned != nil {
				t.Fatalf("human-pending ticket = state %s assigned %v, want new/unassigned", state, assigned)
			}
		})
	}
}

// ---------------------------------------------------------------
// Blocker 2: the created audit is validated FULLY before stamping/persisting.
// Every wrong field — actor, actor user id, action, field/from/to/reason/note
// nil-convention, plan-time chronology, and the zero ticket-id placeholder — is
// rejected independently, never silently overwritten.
func TestWorkflowUoW_Create_CreatedAuditRejectsWrongFields(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*domain.AuditEvent, int64)
	}{
		{"wrong actor", func(a *domain.AuditEvent, _ int64) { a.Actor = "Other" }},
		{"wrong actor user id", func(a *domain.AuditEvent, other int64) { o := other; a.ActorUserID = &o }},
		{"wrong action", func(a *domain.AuditEvent, _ int64) { a.Action = domain.ActionUpdate }},
		{"non-nil field", func(a *domain.AuditEvent, _ int64) { v := "state"; a.Field = &v }},
		{"non-nil from", func(a *domain.AuditEvent, _ int64) { v := "new"; a.FromValue = &v }},
		{"non-nil to", func(a *domain.AuditEvent, _ int64) { v := "resolved"; a.ToValue = &v }},
		{"non-nil reason", func(a *domain.AuditEvent, _ int64) { v := "r"; a.Reason = &v }},
		{"non-nil note", func(a *domain.AuditEvent, _ int64) { v := "n"; a.Note = &v }},
		{"wrong time after plan", func(a *domain.AuditEvent, _ int64) { a.CreatedAt = a.CreatedAt.Add(time.Hour) }},
		{"wrong time before plan", func(a *domain.AuditEvent, _ int64) { a.CreatedAt = a.CreatedAt.Add(-time.Hour) }},
		{"nonzero ticket id", func(a *domain.AuditEvent, _ int64) { a.TicketID = 999 }},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestDB(t)
			cat := seedCategory(t, s, "C1")
			req := seedUser(t, s, "Req", "r@x", true)
			other := seedUser(t, s, "Other", "o@x", true)
			def := manualTaskDef()
			vid := seedPublished(t, s, cat, def)
			in := buildCreateInput(cat, vid, req, def, nil, 0, "active", domain.StateNew, nil)
			tt.mutate(&in.CreatedAudit, other)
			_, err := newWorkflowUnitOfWork(s.db).CreateTicketWithRun(context.Background(), in)
			if !errors.Is(err, domain.ErrWorkflowPositionConflict) {
				t.Fatalf("wrong created-audit %q must conflict, got %v", tt.name, err)
			}
			assertTotalRollback(t, s)
		})
	}
}

// Positive: a valid created audit (zero placeholder) is stamped with the
// store-assigned ticket id and persisted EXACTLY — actor/action/plan-time and the
// nil field/from/to/reason/note convention.
func TestWorkflowUoW_Create_CreatedAuditPersistedExactAfterStamping(t *testing.T) {
	s := newTestDB(t)
	cat := seedCategory(t, s, "C1")
	req := seedUser(t, s, "Req", "r@x", true)
	def := manualTaskDef()
	vid := seedPublished(t, s, cat, def)
	now := testClock
	in := buildCreateInput(cat, vid, req, def, nil, 0, "active", domain.StateNew, nil)
	tk, err := newWorkflowUnitOfWork(s.db).CreateTicketWithRun(context.Background(), in)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	got := readFullAudits(t, s, tk.ID)
	want := []fullAuditRow{
		{ticketID: tk.ID, actor: "Req", actorUserID: &req, action: "created", field: nil, fromValue: nil, toValue: nil, reason: nil, note: nil, createdAt: formatTime(now)},
	}
	if len(got) != len(want) {
		t.Fatalf("audits = %d rows, want %d (created audit only)", len(got), len(want))
	}
	requireFullAudit(t, 0, got[0], want[0])
	// The created audit was stamped with the store-assigned id from a zero placeholder.
	if got[0].ticketID != tk.ID || got[0].ticketID == 0 {
		t.Fatalf("created audit ticket_id = %d, assigned %d, want stamped non-zero", got[0].ticketID, tk.ID)
	}
}
