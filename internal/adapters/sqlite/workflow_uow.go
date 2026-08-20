package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// workflowUnitOfWork implements application.WorkflowUnitOfWork over ONE real
// SQLite immediate transaction (design S5). It is deliberately a persistence
// boundary, NOT an extension point: it re-reads and rechecks every expected
// persisted fact, then applies only the fixed writes/audits already decided by
// the application (WorkflowRunner). It never selects a transition, an
// assignment, a step behavior, or a grant — a caller cannot register behavior
// here. The sealed WorkflowOperation value types are applied in literal slice
// order via a closed type switch; there is no callback, function payload, or
// step-type dispatch (no reading step.Type to choose work).
type workflowUnitOfWork struct{ db *sql.DB }

var _ application.WorkflowUnitOfWork = (*workflowUnitOfWork)(nil)

func newWorkflowUnitOfWork(db *sql.DB) *workflowUnitOfWork { return &workflowUnitOfWork{db: db} }

// ErrLeastLoadedUnresolved is the PR5 guard: deterministic least_loaded
// selection is a PR6 feature. If a runner-decided LeastLoadedAssignmentOperation
// is ever presented to this PR (a definition whose automatic walk reaches a
// least_loaded step at creation), the adapter refuses with this typed error and
// rolls back the ENTIRE create — it never invents a user, an audit, or a
// selection without PR6's deterministic global-load ordering.
var ErrLeastLoadedUnresolved = errors.New("least_loaded assignment is unresolved in this PR")

// CreateTicketWithRun persists the ticket (pinned to the exact expected current
// version), the created audit, the fresh active run, and the runner-planned
// initial automatic operations as ONE atomic unit (design S5 all-or-nothing).
// It re-reads the category's current version and immutable steps_json inside the
// immediate transaction, rechecks every expected identity/user fact, and refuses
// a stale plan with NO writes. On any failure (including an intermediate op) the
// whole transaction rolls back — ticket, audit, run, answers, and cursor/state.
func (u *workflowUnitOfWork) CreateTicketWithRun(ctx context.Context, in application.CreateTicketWithRunInput) (*domain.Ticket, error) {
	tx, err := beginImmediate(ctx, u.db, "create ticket with run")
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	// Re-read the category's current version and its immutable steps_json. The
	// adapter never reads draft_json for creation (availability = a published
	// version exists, design S5 / WorkflowVersionStore).
	curVersion, _, err := currentVersionTx(ctx, tx, in.CategoryID)
	if err != nil {
		return nil, err
	}
	if in.Ticket == nil {
		return nil, &domain.ValidationError{Field: "ticket", Message: "create plan missing ticket"}
	}
	// Recheck every duplicated identity fact; reject a mismatch rather than
	// silently choosing one value.
	if curVersion == 0 || curVersion != in.ExpectedVersionID {
		return nil, domain.NewWorkflowPositionConflictError("workflow version changed")
	}
	if in.Ticket.CategoryID != in.CategoryID {
		return nil, domain.NewWorkflowPositionConflictError("workflow identity mismatch")
	}
	if in.Ticket.WorkflowVersionID == nil || *in.Ticket.WorkflowVersionID != in.ExpectedVersionID {
		return nil, domain.NewWorkflowPositionConflictError("workflow version mismatch")
	}
	// The plan's capture of the immutable workflow must equal the persisted
	// steps_json: apply only the exact snapshot the runner planned. recheckSnapshot
	// reloads the pinned/current version's immutable bytes and compares canonical
	// (design S5).
	storedDef, err := recheckSnapshot(ctx, tx, in.ExpectedVersionID, in.Workflow)
	if err != nil {
		return nil, err
	}

	// Recheck requester/assignee user existence/active/role preconditions the
	// fixed plan depends on (design S5). A missing/user/inactive-assignee
	// precondition rolls the whole create back.
	if err := recheckTicketUsersTx(ctx, tx, in.Ticket); err != nil {
		return nil, err
	}

	// Recheck the create operation grammar and result facts BEFORE any write, and
	// enforce that every audit/answer ticket id is a ZERO placeholder (creation
	// owns the id): a caller-supplied wrong nonzero id is rejected, never silently
	// overwritten.
	if err := validateCreateWorkflowOperations(in.Ticket, storedDef, in); err != nil {
		return nil, err
	}

	// Insert the ticket pinned to the exact expected version id.
	if err := createTicketWithRunTx(ctx, tx, in.Ticket, in.ExpectedVersionID); err != nil {
		return nil, err
	}

	// Append the exact created audit (TicketID stamped from the assigned id).
	created := in.CreatedAudit
	created.TicketID = in.Ticket.ID
	if err := appendAuditEventsTx(ctx, tx, created); err != nil {
		return nil, err
	}

	// Insert the run at its initial cursor/status/timestamps.
	if err := insertRunTx(ctx, tx, in.Ticket.ID, in.ExpectedCursor, in.ExpectedRunStatus, in.StartedAt); err != nil {
		return nil, err
	}

	// Apply only the runner-decided fixed operations in literal order. Any error
	// (including an unresolved least_loaded step) rolls back everything.
	if err := applyWorkflowOperations(ctx, tx, in.Ticket, in.Operations); err != nil {
		return nil, err
	}

	// Apply the final fixed result: authoritative ticket state and the
	// cursor/status/complete facts, persisted in the same atomic unit.
	in.Ticket.State = in.NextTicketState
	if err := updateTicketTx(ctx, tx, in.Ticket); err != nil {
		return nil, err
	}
	if err := applyCursorCAS(ctx, tx, in.Ticket.ID, in.ExpectedCursor, in.ExpectedRunStatus, in.NextCursor, in.NextRunStatus, in.CompletedAt); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("sqlite: commit create with run: %w", err)
	}
	return in.Ticket, nil
}

// ApplyWorkflowPlan reloads the ticket, its run, the pinned immutable workflow
// snapshot, and the relevant user/desk-membership preconditions inside one
// immediate transaction, then rejects ANY mismatch with a typed
// ErrWorkflowPositionConflict and ZERO writes (design S5 load-plan-recheck). It
// also rejects contradictory duplicated plan facts instead of overwriting them
// silently: the fixed operations must reproduce exactly the declared next
// state/assignee/completion facts. Only then does it apply the fixed
// data-only operations and the ticket-write + cursor CAS. The returned
// WorkflowExecutionResult is REFRESHED from persisted state after the writes —
// never the caller-provided Result.
func (u *workflowUnitOfWork) ApplyWorkflowPlan(ctx context.Context, in application.WorkflowMutationPlan) (application.WorkflowExecutionResult, error) {
	tx, err := beginImmediate(ctx, u.db, "apply workflow plan")
	if err != nil {
		return application.WorkflowExecutionResult{}, err
	}
	defer tx.Rollback()

	ticket, err := scanTicketFrom(tx.QueryRowContext(ctx, `SELECT `+ticketColumns+` FROM tickets t WHERE t.id=?`, in.TicketID))
	if errors.Is(err, sql.ErrNoRows) {
		return application.WorkflowExecutionResult{}, &domain.NotFoundError{Kind: "ticket", ID: in.TicketID}
	}
	if err != nil {
		return application.WorkflowExecutionResult{}, err
	}

	// Reload the pinned workflow_version_id (the shared ticket projection drops
	// it) so the immutable-fact recheck can compare the persisted pin.
	var pin sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT workflow_version_id FROM tickets WHERE id=?`, in.TicketID).Scan(&pin); err != nil {
		return application.WorkflowExecutionResult{}, fmt.Errorf("sqlite: read ticket pin: %w", err)
	}
	if pin.Valid {
		ticket.WorkflowVersionID = &pin.Int64
	}

	run, err := scanRunRow(ctx, tx, in.TicketID)
	if err != nil {
		return application.WorkflowExecutionResult{}, err
	}

	// Reload and recheck the pinned immutable workflow snapshot by the ticket's
	// pinned version id (never draft_json), comparing the plan's canonical bytes to
	// the persisted steps exactly. An invalid persisted definition is a data error
	// that propagates and never escapes.
	storedDef, err := recheckSnapshot(ctx, tx, pinnedID(ticket), in.Workflow)
	if err != nil {
		return application.WorkflowExecutionResult{}, err
	}

	// Recheck every expected immutable fact and the plan's internal consistency
	// BEFORE any write; any mismatch is a typed conflict with zero rows changed.
	if err := validateMutationPlan(ctx, tx, ticket, run, storedDef, in); err != nil {
		return application.WorkflowExecutionResult{}, err
	}

	// Apply the fixed data-only operations in literal order (audits/answers/
	// assignee/state). The validator proved they reproduce in.NextTicketState.
	if err := applyWorkflowOperations(ctx, tx, ticket, in.Operations); err != nil {
		return application.WorkflowExecutionResult{}, err
	}

	// Persist the final ticket state (proven equal by the validator).
	ticket.State = in.NextTicketState
	if err := updateTicketTx(ctx, tx, ticket); err != nil {
		return application.WorkflowExecutionResult{}, err
	}

	// Cursor/status CAS; completion timestamp only for a completed run. A stale
	// CAS (0 rows) fails the whole apply with no writes by construction.
	var completedAt *time.Time
	if in.NextRunStatus == "completed" && in.Result.Run != nil {
		completedAt = in.Result.Run.CompletedAt
	}
	if err := applyCursorCAS(ctx, tx, in.TicketID, in.ExpectedCursor, in.ExpectedRunStatus, in.NextCursor, in.NextRunStatus, completedAt); err != nil {
		return application.WorkflowExecutionResult{}, err
	}

	if err := tx.Commit(); err != nil {
		return application.WorkflowExecutionResult{}, fmt.Errorf("sqlite: commit apply plan: %w", err)
	}

	// Return a REFRESHED persisted result read back after the writes.
	return refreshWorkflowResult(ctx, u.db, in.TicketID)
}

// pinnedID resolves the ticket's pinned version id from the reloaded ticket.
func pinnedID(t *domain.Ticket) int64 {
	if t == nil || t.WorkflowVersionID == nil {
		return 0
	}
	return *t.WorkflowVersionID
}

// stepsByVersionTx reads the IMMUTABLE steps_json for a given workflow version id
// (never draft_json). A missing version is a data/consistency error.
func stepsByVersionTx(ctx context.Context, tx *sql.Tx, versionID int64) (string, error) {
	var steps string
	err := tx.QueryRowContext(ctx, `SELECT steps_json FROM workflow_versions WHERE id=?`, versionID).Scan(&steps)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("sqlite: pinned workflow version %d not found", versionID)
	}
	if err != nil {
		return "", fmt.Errorf("sqlite: read pinned workflow: %w", err)
	}
	return steps, nil
}

// recheckSnapshot is the SHARED snapshot recheck used by BOTH CreateTicketWithRun
// and ApplyWorkflowPlan (task 5.4). Inside the caller's immediate transaction it
// reloads the immutable steps_json pinned to versionID and verifies the plan's
// captured Workflow canonicalizes to EXACTLY the persisted bytes. A non-empty
// mismatch is a typed ErrWorkflowPositionConflict; an unparsable/stale persisted
// definition is an infrastructure error (never flattened to a conflict). It
// returns the parsed definition the caller then rechecks operations against. It
// takes only concrete plan/version arguments — no callback or generic transaction
// API.
func recheckSnapshot(ctx context.Context, tx *sql.Tx, versionID int64, plan domain.WorkflowDefinition) (domain.WorkflowDefinition, error) {
	steps, err := stepsByVersionTx(ctx, tx, versionID)
	if err != nil {
		return domain.WorkflowDefinition{}, err
	}
	storedDef, err := domain.ParseWorkflowDefinition([]byte(steps))
	if err != nil {
		return domain.WorkflowDefinition{}, fmt.Errorf("sqlite: parse stored workflow: %w", err)
	}
	// An invalid persisted definition is a DATA/validation error, never a position
	// conflict (it is not plan staleness).
	if iss := storedDef.Validate(); len(iss) > 0 {
		return domain.WorkflowDefinition{}, fmt.Errorf("sqlite: invalid stored workflow: %v", iss)
	}
	storedCanon, err := storedDef.MarshalCanonical()
	if err != nil {
		return domain.WorkflowDefinition{}, fmt.Errorf("sqlite: canonicalize stored workflow: %w", err)
	}
	planCanon, err := plan.MarshalCanonical()
	if err != nil {
		return domain.WorkflowDefinition{}, fmt.Errorf("sqlite: canonicalize plan workflow: %w", err)
	}
	if !bytes.Equal(storedCanon, planCanon) {
		return domain.WorkflowDefinition{}, domain.NewWorkflowPositionConflictError("workflow definition mismatch")
	}
	return storedDef, nil
}

// validateMutationPlan reloads nothing (the caller passes already-reloaded state)
// but rechecks EVERY expected immutable fact and validates the plan's operation/
// result consistency BEFORE any write. Any mismatch is a typed
// ErrWorkflowPositionConflict; DB/precondition errors propagate untouched.
func validateMutationPlan(ctx context.Context, tx *sql.Tx, ticket *domain.Ticket, run *application.WorkflowRun, storedDef domain.WorkflowDefinition, in application.WorkflowMutationPlan) error {
	conflict := func(msg string) error { return domain.NewWorkflowPositionConflictError(msg) }

	// Pinned workflow version id must match the plan's expected version.
	if ticket.WorkflowVersionID == nil || *ticket.WorkflowVersionID != in.ExpectedVersionID {
		return conflict("workflow version mismatch")
	}
	// The pinned immutable steps_json must match the plan's snapshot canonical
	// content exactly.
	storedCanon, err := storedDef.MarshalCanonical()
	if err != nil {
		return fmt.Errorf("sqlite: canonicalize pinned workflow: %w", err)
	}
	planCanon, err := in.Workflow.MarshalCanonical()
	if err != nil {
		return fmt.Errorf("sqlite: canonicalize plan workflow: %w", err)
	}
	if !bytes.Equal(storedCanon, planCanon) {
		return conflict("workflow definition mismatch")
	}
	// Requester / assignee CURRENT identity (nil-safe).
	if !sameIntPtr(ticket.RequesterUserID, in.RequesterUserID) {
		return conflict("workflow requester mismatch")
	}
	if !sameIntPtr(ticket.UserID, in.AssigneeUserID) {
		return conflict("workflow assignee mismatch")
	}
	// Run cursor/status + ticket state match the plan's expected facts.
	if run.CurrentStepIndex != in.ExpectedCursor || run.Status != in.ExpectedRunStatus {
		return conflict("workflow position conflict")
	}
	if ticket.State != in.TicketBeforeState {
		return conflict("workflow position conflict")
	}
	// Relevant user preconditions: requester active, current assignee active
	// agent+, and the submitting actor active. Any identity/user mismatch here
	// is a TYPED ErrWorkflowPositionConflict with no writes (a stale plan whose
	// expected identity facts no longer hold); genuine infrastructure/database
	// errors still propagate untouched (they are not flattened to a conflict).
	if err := recheckApplyUsersTx(ctx, tx, ticket); err != nil {
		return err
	}
	if in.ActorUserID != 0 {
		if err := requireApplyActiveActorTx(ctx, tx, in.ActorUserID); err != nil {
			return err
		}
	}
	return validateWorkflowOperations(ctx, tx, ticket, run, storedDef, in)
}

// validateWorkflowOperations checks each sealed operation against the PINNED
// definition and literal order, simulates applying them on a ticket copy, and
// verifies the copy reproduces EXACTLY the plan's declared next
// state/assignee/completion facts. It may inspect the pinned closed step
// definitions to CORROBORATE a fixed operation (a claim needs an assign_to_desk
// claim step, a form answer a form step, a workflow_step a human non-terminal
// step, a transition a claim or terminal step) — applying operation values here
// is still pure validation; EXECUTION never dispatches by step.Type.
func validateWorkflowOperations(ctx context.Context, tx *sql.Tx, ticket *domain.Ticket, run *application.WorkflowRun, def domain.WorkflowDefinition, in application.WorkflowMutationPlan) error {
	conflict := func(msg string) error { return domain.NewWorkflowPositionConflictError(msg) }
	ops := in.Operations

	// Every plan audit/answer timestamp is monotonic (>= run start, nondecreasing
	// in literal order) before any group semantics are considered (PR5 final-gate: no
	// fabricated chronology). Because a FormAnswerOperation is always immediately
	// followed by its same-completion WorkflowStepOperation, the nondecreasing rule
	// also guarantees the form answer never post-dates the workflow_step audit.
	if err := validateOpsMonotonic(conflict, run.StartedAt, ops); err != nil {
		return err
	}

	// A run that is no longer active may carry NO operations and may NOT advance:
	// completing it again is a stale/mismatched position.
	if run.Status != "active" {
		if len(ops) > 0 {
			return conflict("workflow run not active")
		}
		if in.NextCursor != run.CurrentStepIndex || in.NextRunStatus != run.Status {
			return conflict("workflow next cursor mismatch")
		}
		return validateResultFacts(conflict, ticket, run, in)
	}

	// Active run: the first operation MUST target the current (pending) step — a
	// skipped current step is a contradiction. Empty operations are valid ONLY as a
	// terminal already-state no-op (the run completes over an already-terminal final
	// step); a pending human/automatic step can never be "completed" by an empty plan.
	cur := run.CurrentStepIndex
	if cur >= len(def) {
		return conflict("workflow step index out of range")
	}
	if len(ops) == 0 {
		return validateEmptyOpsActiveRun(conflict, ticket, run, def, in)
	}
	if firstIdx, err := opStepIndex(ops[0]); err != nil {
		return err
	} else if firstIdx != cur {
		return conflict("workflow operation before current step")
	}

	// Walk the operation groups in exact definition order. Each group consumes ONLY
	// its own prefix and returns how many operations it used; the loop then continues
	// with the immediately following contiguous groups (a runner-valid human group
	// followed by automatic terminal groups), never assuming a group absorbs the rest
	// of the plan.
	copyTicket := *ticket
	i := 0
	idx := cur
	for i < len(ops) {
		if idx >= len(def) {
			return conflict("workflow step index out of range")
		}
		if first, err := opStepIndex(ops[i]); err != nil {
			return err
		} else if first != idx {
			return conflict("workflow operation before current step")
		}
		step := def[idx]
		n, err := corroborateGroup(ctx, tx, conflict, &copyTicket, step, run, in, idx, ops[i:])
		if err != nil {
			return err
		}
		if n <= 0 {
			return conflict("workflow operation group mismatch")
		}
		i += n
		idx++
	}
	// NextCursor must agree with the steps actually consumed, OR the run legitimately
	// completes over a trailing terminal already-state no-op: the runner advances the
	// cursor to len(def) and marks the run completed when a final terminal step no-ops
	// (resolve when already resolved/closed; close when already closed). idx then lands
	// one short of len(def) on that final terminal step.
	if idx != in.NextCursor {
		last := len(def) - 1
		validNoop := in.NextRunStatus == "completed" && in.NextCursor == len(def) && idx == last &&
			isTerminalStep(def[last]) && terminalNoopValid(def[last], copyTicket.State)
		if !validNoop {
			return conflict("workflow next cursor mismatch")
		}
	}
	return validateResultFacts(conflict, &copyTicket, run, in)
}

// validateEmptyOpsActiveRun handles the ACTIVE-run empty-operation case: only a
// terminal already-state no-op (the run completes over an already-terminal final
// step) is legal. A pending human/automatic step can never be "completed" by an empty
// plan — the request must actually complete the current step.
func validateEmptyOpsActiveRun(conflict func(string) error, ticket *domain.Ticket, run *application.WorkflowRun, def domain.WorkflowDefinition, in application.WorkflowMutationPlan) error {
	cur := run.CurrentStepIndex
	last := len(def) - 1
	if cur == last && isTerminalStep(def[last]) && terminalNoopValid(def[last], ticket.State) &&
		in.NextRunStatus == "completed" && in.NextCursor == len(def) {
		if in.NextTicketState != ticket.State {
			return conflict("workflow next state mismatch")
		}
		return validateResultFacts(conflict, ticket, run, in)
	}
	return conflict("workflow step not completed")
}

// validateOpsMonotonic enforces that every operation's audit/answer timestamp is
// >= run start and nondecreasing in literal order. Because a FormAnswerOperation is
// always immediately followed by its same-completion WorkflowStepOperation, the
// nondecreasing rule also guarantees the form answer does not post-date the
// workflow_step audit.
func validateOpsMonotonic(conflict func(string) error, start time.Time, ops []application.WorkflowOperation) error {
	last := time.Time{}
	for _, op := range ops {
		var at time.Time
		switch v := op.(type) {
		case application.TransitionOperation:
			at = v.Audit.CreatedAt
		case application.ClaimAssignmentOperation:
			at = v.AssignmentAudit.CreatedAt
		case application.WorkflowStepOperation:
			at = v.Audit.CreatedAt
		case application.FormAnswerOperation:
			at = v.SubmittedAt
		case application.LeastLoadedAssignmentOperation:
			continue // refused before persistence; carries no timestamp here
		default:
			return fmt.Errorf("sqlite: unknown workflow operation %T", op)
		}
		if at.Before(start) {
			return conflict("workflow operation before run start")
		}
		if !last.IsZero() && at.Before(last) {
			return conflict("workflow operation timestamp out of order")
		}
		last = at
	}
	return nil
}

// isTerminalStep reports whether a step is a resolve/close terminal step.
func isTerminalStep(s domain.WorkflowStep) bool {
	return s.Type == domain.StepResolve || s.Type == domain.StepClose
}

// terminalNoopValid reports whether a terminal step completes as a no-op (no
// transition) in the given ticket state, mirroring the runner's applyTerminal.
func terminalNoopValid(s domain.WorkflowStep, state domain.State) bool {
	switch s.Type {
	case domain.StepResolve:
		return state == domain.StateResolved || state == domain.StateClosed
	case domain.StepClose:
		return state == domain.StateClosed
	}
	return false
}

// validateTerminalMatrix enforces the EXACT resolve/close matrix (design S7,
// mirroring applyTerminal) after the group's transitions have been applied on the
// ticket copy: resolve is exactly ONE transition ending in resolved from
// new/in_progress; close is two ordered transitions (->resolved ->closed) from
// new/in_progress and exactly one (->closed) from resolved. A
// wrong-but-domain-legal transition — a Ticket.Transition that is legal but whose
// OUTCOME is not the step's (e.g. a resolve producing in_progress/cancelled, or a
// partial close) — is rejected with a typed conflict. The terminal already-state
// no-op (zero transitions) is handled by the enclosing loop, never here.
func validateTerminalMatrix(conflict func(string) error, stepType domain.StepType, startState, finalState domain.State, n int) error {
	if startState == domain.StateCancelled {
		return conflict("transition from cancelled terminal step")
	}
	switch stepType {
	case domain.StepResolve:
		if n != 1 || finalState != domain.StateResolved {
			return conflict("terminal group mismatch")
		}
	case domain.StepClose:
		expected := 2
		if startState == domain.StateResolved {
			expected = 1
		}
		if finalState != domain.StateClosed || n != expected {
			return conflict("terminal group mismatch")
		}
	}
	return nil
}

// corroborateGroup validates ONE pinned step's exact operation group prefix and
// simulates its writes on the ticket copy. The exact group depends on the pinned
// step type:
//
//	claim:      [ClaimAssignment]? [new->in_progress Transition if ticket is new]? [WorkflowStep]
//	form:       [FormAnswer, WorkflowStep]
//	manual:     [WorkflowStep]
//	resolve:    [Transition]  (exactly one)
//	close:      [Transition, (Transition)]  (one or two ordered transitions)
//	least_load: ErrLeastLoadedUnresolved (PR5 refuses selection)
//
// It returns the number of operations consumed by THIS group's prefix only — the
// enclosing loop continues with any contiguous automatic groups. A terminal
// already-state no-op (zero transitions) is handled by the enclosing loop, not
// here. All consumed operations must carry the expected step index (no
// interleaving), and the group shape is exact for what IS present (no extra or
// duplicated operations inside the group). This is pure validation — EXECUTION
// never dispatches by step.Type; the sealed operation values are applied only in
// applyWorkflowOperations.
func corroborateGroup(ctx context.Context, tx *sql.Tx, conflict func(string) error, t *domain.Ticket, step domain.WorkflowStep, run *application.WorkflowRun, in application.WorkflowMutationPlan, expectedIdx int, ops []application.WorkflowOperation) (int, error) {
	if len(ops) == 0 {
		return 0, conflict("workflow operation group missing")
	}
	requireIdx := func(op application.WorkflowOperation) error {
		oi, err := opStepIndex(op)
		if err != nil {
			return err
		}
		if oi != expectedIdx {
			return conflict("workflow step group interleaved")
		}
		return nil
	}

	switch step.Type {
	case domain.StepAssignToDesk:
		if step.AssignToDesk == nil {
			return 0, conflict("claim at non-claim step")
		}
		if step.AssignToDesk.Strategy != domain.StrategyClaim {
			return 0, ErrLeastLoadedUnresolved
		}
		if len(ops) == 0 {
			return 0, conflict("claim group missing")
		}
		n := 0
		// The exact claim group is [ClaimAssignment]? [new->in_progress Transition]? [WorkflowStep].
		// The assignment operation is present only when the claim actually changes the
		// assignee; a same-person claim carries none.
		assignmentRequired := t.UserID == nil || *t.UserID != in.ActorUserID
		if c, ok := ops[0].(application.ClaimAssignmentOperation); ok {
			if err := requireIdx(ops[0]); err != nil {
				return 0, err
			}
			if err := validateClaimOp(ctx, tx, conflict, t, step, run, in, c); err != nil {
				return 0, err
			}
			n++
		} else if assignmentRequired {
			return 0, conflict("claim assignment missing")
		}
		// A claim on a NEW ticket MUST carry the exact new->in_progress transition; a
		// claim on an in_progress/later ticket MUST NOT carry one.
		if t.State == domain.StateNew {
			if n >= len(ops) {
				return 0, conflict("claim on new missing in-progress transition")
			}
			tr, ok := ops[n].(application.TransitionOperation)
			if !ok {
				return 0, conflict("claim group mismatch")
			}
			if err := requireIdx(ops[n]); err != nil {
				return 0, err
			}
			if err := validateTransitionOp(conflict, t, step, in.TicketID, tr); err != nil {
				return 0, err
			}
			n++
		} else if n < len(ops) {
			if _, ok := ops[n].(application.TransitionOperation); ok {
				return 0, conflict("claim transition on non-new step")
			}
		}
		// The human workflow_step completion audit closes the claim group.
		if n >= len(ops) {
			return 0, conflict("claim step missing completion audit")
		}
		ws, ok := ops[n].(application.WorkflowStepOperation)
		if !ok {
			return 0, conflict("claim group mismatch")
		}
		if err := requireIdx(ops[n]); err != nil {
			return 0, err
		}
		if err := validateWorkflowStepOp(conflict, step, run, in, ws); err != nil {
			return 0, err
		}
		n++
		return n, nil

	case domain.StepForm:
		// Exact group prefix [FormAnswer, WorkflowStep]; the enclosing loop consumes
		// only this prefix and continues with any contiguous automatic groups.
		if len(ops) < 2 {
			return 0, conflict("form step missing completion audit")
		}
		fa, ok := ops[0].(application.FormAnswerOperation)
		if !ok {
			return 0, conflict("form group mismatch")
		}
		if err := requireIdx(ops[0]); err != nil {
			return 0, err
		}
		if err := validateFormAnswerOp(conflict, t, step, run, in, fa); err != nil {
			return 0, err
		}
		ws, ok := ops[1].(application.WorkflowStepOperation)
		if !ok {
			return 0, conflict("form group mismatch")
		}
		if err := requireIdx(ops[1]); err != nil {
			return 0, err
		}
		if err := validateWorkflowStepOp(conflict, step, run, in, ws); err != nil {
			return 0, err
		}
		return 2, nil

	case domain.StepManualTask:
		// Exact group [WorkflowStep]; the enclosing loop consumes only this one op
		// and continues with any contiguous automatic groups.
		if len(ops) == 0 {
			return 0, conflict("manual group missing")
		}
		ws, ok := ops[0].(application.WorkflowStepOperation)
		if !ok {
			return 0, conflict("manual group mismatch")
		}
		if err := requireIdx(ops[0]); err != nil {
			return 0, err
		}
		if err := validateWorkflowStepOp(conflict, step, run, in, ws); err != nil {
			return 0, err
		}
		return 1, nil

	case domain.StepResolve, domain.StepClose:
		// Exact terminal transition group consumed as the remainder: resolve is
		// exactly ONE transition; close follows the Ticket.Transition matrix (one or
		// two ordered transitions). A terminal step is final, so no operation may
		// follow it. The terminal already-state no-op (zero transitions) is handled by
		// the enclosing loop, not here. The EXACT terminal outcome matrix (design
		// S7) is enforced via validateTerminalMatrix, rejecting every
		// wrong-but-domain-legal transition (a legal Ticket.Transition whose outcome
		// is NOT the step's).
		n := 0
		startState := t.State
		for n < len(ops) {
			tr, ok := ops[n].(application.TransitionOperation)
			if !ok {
				break
			}
			if err := requireIdx(ops[n]); err != nil {
				return 0, err
			}
			if err := validateTransitionOp(conflict, t, step, in.TicketID, tr); err != nil {
				return 0, err
			}
			n++
		}
		if n == 0 {
			return 0, conflict("terminal group missing transition")
		}
		if err := validateTerminalMatrix(conflict, step.Type, startState, t.State, n); err != nil {
			return 0, err
		}
		if n != len(ops) {
			return 0, conflict("terminal group mismatch")
		}
		return n, nil

	default:
		return 0, conflict("workflow step audit at terminal/unknown step")
	}
}

// validateResultFacts checks the plan's declared final facts (state, assignee,
// completion) against the simulated ticket copy and the caller's runtime facts.
// Any contradiction is a typed ErrWorkflowPositionConflict — never silently
// overwritten.
func validateResultFacts(conflict func(string) error, t *domain.Ticket, run *application.WorkflowRun, in application.WorkflowMutationPlan) error {
	if t.State != in.NextTicketState {
		return conflict("workflow next state mismatch")
	}
	if !sameIntPtr(t.UserID, in.NextAssigneeUserID) {
		return conflict("workflow next assignee mismatch")
	}
	// An active-plan Result is authoritative and REQUIRED — no nil bypass — and must
	// carry the exact ticket/run identity, start time, and final facts that were
	// persisted/expected. The REFRESHED result is read back after the writes; the
	// caller-provided Result must still be coherent.
	if in.Result.Run == nil || in.Result.Ticket == nil {
		return conflict("workflow result facts missing")
	}
	if in.Result.Run.TicketID != in.TicketID {
		return conflict("workflow result run ticket mismatch")
	}
	if in.Result.Ticket.ID != in.TicketID {
		return conflict("workflow result ticket id mismatch")
	}
	if !in.Result.Run.StartedAt.Equal(run.StartedAt) {
		return conflict("workflow result run start mismatch")
	}
	if in.Result.Run.CurrentStepIndex != in.NextCursor || in.Result.Run.Status != in.NextRunStatus {
		return conflict("workflow result run facts mismatch")
	}
	completed := in.NextRunStatus == "completed"
	hasCompletedAt := in.Result.Run.CompletedAt != nil
	if completed != hasCompletedAt {
		return conflict("workflow completion facts mismatch")
	}
	if in.Result.Run.CompletedAt != nil && in.Result.Run.CompletedAt.Before(run.StartedAt) {
		return conflict("workflow completion before run start")
	}
	if in.Result.Ticket.State != t.State || !sameIntPtr(in.Result.Ticket.UserID, t.UserID) {
		return conflict("workflow result ticket facts mismatch")
	}
	return nil
}

// validateCreateWorkflowOperations rechecks the create-time operation grammar and
// final result facts BEFORE any write (design S5 all-or-nothing). Creation only
// auto-advances automatic steps in literal definition order from the starting
// cursor; a human step always stops the walk. Every audit ticket id must be a ZERO
// placeholder (creation assigns the ticket id) — a wrong nonzero id is rejected
// with a typed conflict, never silently overwritten by the create.
func validateCreateWorkflowOperations(t *domain.Ticket, def domain.WorkflowDefinition, in application.CreateTicketWithRunInput) error {
	conflict := func(msg string) error { return domain.NewWorkflowPositionConflictError(msg) }
	// The created audit is validated FULLY (actor/id/action/nil-convention/plan
	// time) BEFORE any stamping or persistence; a wrong fact is rejected with a
	// typed conflict, never silently overwritten by the create.
	if err := validateCreatedAudit(conflict, t, in.CreatedAudit, in.StartedAt); err != nil {
		return err
	}
	ops := in.Operations
	cur := in.ExpectedCursor
	if cur < 0 {
		return conflict("workflow position conflict")
	}
	// Creation transition audits are monotonic: >= run start, nondecreasing in
	// literal order, and never earlier than the created audit.
	lastOpAt := in.CreatedAudit.CreatedAt
	for _, op := range ops {
		if tr, ok := op.(application.TransitionOperation); ok {
			if tr.Audit.CreatedAt.Before(in.StartedAt) {
				return conflict("transition audit before run start")
			}
			if tr.Audit.CreatedAt.Before(lastOpAt) {
				return conflict("transition audit timestamp out of order")
			}
			lastOpAt = tr.Audit.CreatedAt
		}
	}
	// Empty operations: MUST NOT early-return on cursor alone. The current step is
	// validated against its type — a terminal automatic step either completes as an
	// already-state no-op or requires its exact transition(s); a least_loaded step
	// is the PR5 unresolved refusal; a human-pending step is a valid create-time
	// wait (cursor unchanged, active).
	if len(ops) == 0 {
		return validateEmptyOpsCreate(conflict, *t, def, in, cur)
	}

	copyTicket := *t
	i := 0
	idx := cur
	for i < len(ops) {
		if idx >= len(def) {
			return conflict("workflow step index out of range")
		}
		first, err := opStepIndex(ops[i])
		if err != nil {
			return err
		}
		if first != idx {
			return conflict("workflow operation before current step")
		}
		// Creation only auto-advances automatic steps; a least_loaded selection is
		// unresolved in PR5 and a human step (claim/form/manual) always stops.
		switch v := ops[i].(type) {
		case application.LeastLoadedAssignmentOperation:
			return ErrLeastLoadedUnresolved
		case application.TransitionOperation:
			if v.Audit.TicketID != 0 {
				return conflict("transition audit ticket id must be zero placeholder")
			}
		default:
			return conflict("automatic step at human step")
		}
		step := def[idx]
		n, err := corroborateAutomaticGroup(conflict, &copyTicket, step, idx, ops[i:])
		if err != nil {
			return err
		}
		if n <= 0 {
			return conflict("workflow operation group mismatch")
		}
		i += n
		idx++
	}
	if idx != in.NextCursor {
		return conflict("workflow next cursor mismatch")
	}
	return validateCreateResultFacts(conflict, copyTicket, in)
}

// validateCreatedAudit rechecks the created audit's exact fact convention against
// the plan/ticket BEFORE it is stamped with the assigned ticket id and persisted.
// Creation owns the ticket id, so TicketID must be the ZERO placeholder (a wrong
// nonzero id is rejected, never overwritten); actor/actor-user-id must be the human
// requester/session facts from the plan/ticket; the action is created; field/
// from/to/reason/note are all nil (a created audit carries only
// actor/action/time/ticket); and CreatedAt equals the plan creation time exactly.
func validateCreatedAudit(conflict func(string) error, t *domain.Ticket, a domain.AuditEvent, startedAt time.Time) error {
	if a.TicketID != 0 {
		return conflict("created audit ticket id must be zero placeholder")
	}
	if a.Actor != t.RequesterName {
		return conflict("created audit actor mismatch")
	}
	if !sameIntPtr(a.ActorUserID, t.RequesterUserID) {
		return conflict("created audit actor user id mismatch")
	}
	if a.Action != domain.ActionCreated {
		return conflict("created audit action mismatch")
	}
	if a.Field != nil || a.FromValue != nil || a.ToValue != nil || a.Reason != nil || a.Note != nil {
		return conflict("created audit must carry no field/from/to/reason/note")
	}
	if !a.CreatedAt.Equal(startedAt) {
		return conflict("created audit time mismatch")
	}
	return nil
}

// validateEmptyOpsCreate handles the create empty-operation path without an
// early-return: it validates the CURRENT step against its type so a terminal
// automatic step can never be left "waiting" by empty ops. Resolve/close complete
// the run as a no-op ONLY from an already-terminal state (resolve from
// resolved/closed; close from closed); from new/in_progress (and resolved for
// close) the step REQUIRES its exact transition(s) and cancelled always rejects. A
// least_loaded step is the PR5 unresolved refusal. A human-pending step is a valid
// create-time wait (cursor unchanged, active). NextCursor/NextRunStatus/
// NextTicketState/CompletedAt must agree with the chosen outcome.
func validateEmptyOpsCreate(conflict func(string) error, t domain.Ticket, def domain.WorkflowDefinition, in application.CreateTicketWithRunInput, cur int) error {
	if cur < 0 || cur >= len(def) {
		return conflict("workflow step index out of range")
	}
	step := def[cur]
	if isTerminalStep(step) {
		// Terminal: empty ops are valid ONLY as an already-state no-op completion
		// (the run completes over the already-terminal final step). From
		// new/in_progress/even-resolved-for-close the exact transition matrix is
		// REQUIRED, and cancelled always rejects.
		if in.NextRunStatus != "completed" || in.NextCursor != len(def) {
			return conflict("workflow next cursor mismatch")
		}
		if !terminalNoopValid(step, t.State) {
			return conflict("workflow step not completed")
		}
		if in.NextTicketState != t.State {
			return conflict("workflow next state mismatch")
		}
		return validateCreateResultFacts(conflict, t, in)
	}
	if step.Type == domain.StepAssignToDesk && step.AssignToDesk != nil && step.AssignToDesk.Strategy != domain.StrategyClaim {
		return ErrLeastLoadedUnresolved
	}
	// Human-pending (claim/form/manual) create-time wait. The run starts awaiting
	// the FIRST human step and must stay there untouched: the initial waiting
	// facts are EXACT — ExpectedCursor and NextCursor both stay 0, ExpectedRunStatus
	// and NextRunStatus both stay active, CompletedAt is nil, and NextTicketState
	// equals the current state (no state or assignee mutation). A cursor >0 or a
	// moved/advanced cursor, a completed status or completion time, or any forged
	// waiting fact is a typed conflict with total rollback (design S5).
	return validateHumanPendingCreate(conflict, t, in, cur)
}

// validateHumanPendingCreate enforces the exact initial waiting facts for an empty
// create plan whose current step is a human step (claim/form/manual). The run
// begins awaiting the first human step and does not advance or complete: cursor
// stays 0 (ExpectedCursor==0 and NextCursor==0), ExpectedRunStatus/NextRunStatus
// stay active, CompletedAt is nil, and NextTicketState equals the current ticket
// state (no state or assignee mutation). Any forged dimension — a nonzero planned
// cursor, an advancing NextCursor, a completed status, a completion timestamp, or
// an inconsistent next state — is rejected with a typed ErrWorkflowPositionConflict
// and zero writes.
func validateHumanPendingCreate(conflict func(string) error, t domain.Ticket, in application.CreateTicketWithRunInput, cur int) error {
	if cur != 0 {
		return conflict("workflow position conflict")
	}
	if in.ExpectedRunStatus != "active" {
		return conflict("workflow run status mismatch")
	}
	if in.NextRunStatus != "active" {
		return conflict("workflow run status mismatch")
	}
	if in.NextCursor != cur {
		return conflict("workflow next cursor mismatch")
	}
	if in.CompletedAt != nil {
		return conflict("workflow completion facts mismatch")
	}
	if in.NextTicketState != t.State {
		return conflict("workflow next state mismatch")
	}
	return nil
}

// validateCreateResultFacts checks the create plan's declared final state/status/
// completion against the simulated ticket copy. Any contradiction is a typed
// ErrWorkflowPositionConflict with no writes.
func validateCreateResultFacts(conflict func(string) error, t domain.Ticket, in application.CreateTicketWithRunInput) error {
	if t.State != in.NextTicketState {
		return conflict("workflow next state mismatch")
	}
	if in.NextRunStatus != "active" && in.NextRunStatus != "completed" {
		return conflict("workflow run status mismatch")
	}
	completed := in.NextRunStatus == "completed"
	hasCompletedAt := in.CompletedAt != nil
	if completed != hasCompletedAt {
		return conflict("workflow completion facts mismatch")
	}
	if in.CompletedAt != nil && in.CompletedAt.Before(in.StartedAt) {
		return conflict("workflow completion before run start")
	}
	return nil
}

// corroborateAutomaticGroup validates one automatic terminal step's exact
// transition group (one for resolve; one or two for close) on the ticket copy and
// returns the number of operations consumed. Creation audits must use the zero
// ticket-id placeholder, enforced via expectedTicketID=0.
func corroborateAutomaticGroup(conflict func(string) error, t *domain.Ticket, step domain.WorkflowStep, expectedIdx int, ops []application.WorkflowOperation) (int, error) {
	if len(ops) == 0 {
		return 0, conflict("automatic operation group missing")
	}
	n := 0
	startState := t.State
	for n < len(ops) {
		tr, ok := ops[n].(application.TransitionOperation)
		if !ok {
			break
		}
		oi, err := opStepIndex(ops[n])
		if err != nil {
			return 0, err
		}
		if oi != expectedIdx {
			break
		}
		if err := validateTransitionOp(conflict, t, step, 0, tr); err != nil {
			return 0, err
		}
		n++
	}
	if n == 0 {
		return 0, conflict("automatic step group missing transition")
	}
	// resolve is exactly ONE transition to resolved; close is two ordered
	// transitions (new -> resolved -> closed) at creation. A wrong-but-domain-legal
	// automatic transition or a partial close is rejected (exact matrix). A terminal
	// automatic step is final.
	if err := validateTerminalMatrix(conflict, step.Type, startState, t.State, n); err != nil {
		return 0, err
	}
	if n != len(ops) {
		return 0, conflict("automatic step group mismatch")
	}
	return n, nil
}

// opStepIndex extracts the targeted step index from a sealed operation. An
// unknown operation is an application/adaptation contract bug, not a conflict.
func opStepIndex(op application.WorkflowOperation) (int, error) {
	switch v := op.(type) {
	case application.TransitionOperation:
		return v.StepIndex, nil
	case application.ClaimAssignmentOperation:
		return v.StepIndex, nil
	case application.WorkflowStepOperation:
		return v.StepIndex, nil
	case application.FormAnswerOperation:
		return v.StepIndex, nil
	case application.LeastLoadedAssignmentOperation:
		return v.StepIndex, nil
	default:
		return 0, fmt.Errorf("sqlite: unknown workflow operation %T", op)
	}
}

// validateTransitionOp corroborates a transition audit against the pinned step
// and the running state. A transition is legal only as a new->in_progress
// consequence of a claim/least_loaded routing step or as a terminal resolve/
// close lifecycle transition. Workflow transitions stamp actor "workflow" with
// a NULL actor user id and the exact Ticket.Transition facts/order.
func validateTransitionOp(conflict func(string) error, t *domain.Ticket, step domain.WorkflowStep, expectedTicketID int64, v application.TransitionOperation) error {
	a := v.Audit
	if a.Action != domain.ActionTransition {
		return conflict("transition audit action mismatch")
	}
	if a.Field == nil || *a.Field != "state" {
		return conflict("transition audit field mismatch")
	}
	if a.FromValue == nil || *a.FromValue != string(t.State) {
		return conflict("transition audit from-state mismatch")
	}
	if a.ToValue == nil {
		return conflict("transition audit missing to-state")
	}
	if a.TicketID != expectedTicketID {
		return conflict("transition audit ticket mismatch")
	}
	if a.Actor != "workflow" {
		return conflict("transition audit actor mismatch")
	}
	if a.ActorUserID != nil {
		return conflict("transition audit must have nil actor user id")
	}
	// A workflow transition carries no reason or note (domain facts never
	// require them: terminal/claim transitions never reopen a closed ticket).
	if a.Reason != nil || a.Note != nil {
		return conflict("transition audit must carry no reason or note")
	}
	to := domain.State(*a.ToValue)
	ev, err := t.Transition(to, "", a.CreatedAt)
	if err != nil {
		return conflict("transition state mismatch")
	}
	if ev.ToValue == nil || *ev.ToValue != *a.ToValue {
		return conflict("transition audit to-state mismatch")
	}
	// Corroborate the pinned step for the transition kind.
	switch step.Type {
	case domain.StepAssignToDesk:
		if *a.FromValue != "new" || to != domain.StateInProgress {
			return conflict("transition at claim step must be new->in_progress")
		}
	case domain.StepResolve, domain.StepClose:
		// terminal lifecycle transition; legality already proven by Ticket.Transition
	default:
		return conflict("transition at non-terminal non-claim step")
	}
	return nil
}

// validateClaimOp corroborates a known claim against the pinned assign_to_desk
// claim step, requires the claimant to be the active agent+ desk member, and
// checks every assignment audit fact (action/field/ticket/actor/name/from/to/time
// and that the trimmed Reason equals the audit Reason exactly).
func validateClaimOp(ctx context.Context, tx *sql.Tx, conflict func(string) error, t *domain.Ticket, step domain.WorkflowStep, run *application.WorkflowRun, in application.WorkflowMutationPlan, v application.ClaimAssignmentOperation) error {
	if step.Type != domain.StepAssignToDesk || step.AssignToDesk == nil {
		return conflict("claim at non-claim step")
	}
	if step.AssignToDesk.Strategy != domain.StrategyClaim {
		return conflict("claim at least_loaded step")
	}
	if step.AssignToDesk.DeskID != v.DeskID {
		return conflict("claim desk mismatch")
	}
	if v.AssigneeUserID != in.ActorUserID {
		return conflict("claim assignee mismatch")
	}
	if ok, err := claimantEligibleTx(ctx, tx, v.AssigneeUserID); err != nil {
		return err
	} else if !ok {
		return conflict("claim assignee not eligible")
	}
	if ok, err := deskMemberTx(ctx, tx, v.DeskID, v.AssigneeUserID); err != nil {
		return err
	} else if !ok {
		return conflict("claim assignee not a desk member")
	}
	a := v.AssignmentAudit
	if a.Action != domain.ActionUpdate {
		return conflict("assignment audit action mismatch")
	}
	if a.Field == nil || *a.Field != "user" {
		return conflict("assignment audit field mismatch")
	}
	if a.TicketID != in.TicketID {
		return conflict("assignment audit ticket mismatch")
	}
	if a.Actor != in.ActorName {
		return conflict("assignment audit actor name mismatch")
	}
	if a.ActorUserID == nil || *a.ActorUserID != v.AssigneeUserID {
		return conflict("assignment audit actor mismatch")
	}
	var cur string
	if t.UserID != nil {
		cur = strconv.FormatInt(*t.UserID, 10)
	}
	if a.FromValue == nil || *a.FromValue != cur {
		return conflict("assignment audit from mismatch")
	}
	if a.ToValue == nil || *a.ToValue != strconv.FormatInt(v.AssigneeUserID, 10) {
		return conflict("assignment audit to mismatch")
	}
	if a.CreatedAt.Before(run.StartedAt) {
		return conflict("assignment audit before run start")
	}
	trimmed := strings.TrimSpace(v.Reason)
	auditReason := ""
	if a.Reason != nil {
		auditReason = *a.Reason
	}
	if trimmed != auditReason {
		return conflict("assignment audit reason mismatch")
	}
	// A claim assignment audit carries NO note (audit-log spec); a fabricated note
	// is a contradiction, never silently dropped.
	if a.Note != nil {
		return conflict("assignment audit must carry no note")
	}
	if cur != "" && trimmed == "" {
		return conflict("reassignment requires a reason")
	}
	assign := v.AssigneeUserID
	t.UserID = &assign
	t.UpdatedAt = a.CreatedAt
	return nil
}

// validateWorkflowStepOp corroborates a human workflow_step audit against a
// NON-terminal human step (claim, form, manual) and validates the exact human
// actor/id/ticket/time facts.
func validateWorkflowStepOp(conflict func(string) error, step domain.WorkflowStep, run *application.WorkflowRun, in application.WorkflowMutationPlan, v application.WorkflowStepOperation) error {
	a := v.Audit
	if a.Action != domain.ActionWorkflowStep {
		return conflict("workflow step audit action mismatch")
	}
	if a.TicketID != in.TicketID {
		return conflict("workflow step audit ticket mismatch")
	}
	if a.Actor != in.ActorName {
		return conflict("workflow step audit actor mismatch")
	}
	if a.ActorUserID == nil || *a.ActorUserID != in.ActorUserID {
		return conflict("workflow step audit actor id mismatch")
	}
	// The workflow_step completion audit carries ONLY actor/action/ticket/time —
	// no field/from/to/reason/note (those belong to transitions/assignments).
	if a.Field != nil || a.FromValue != nil || a.ToValue != nil || a.Reason != nil || a.Note != nil {
		return conflict("workflow step audit must carry no field/from/to/reason/note")
	}
	if a.CreatedAt.Before(run.StartedAt) {
		return conflict("workflow step before run start")
	}
	switch step.Type {
	case domain.StepAssignToDesk, domain.StepForm, domain.StepManualTask:
	default:
		return conflict("workflow step audit at terminal/unknown step")
	}
	return nil
}

// validateFormAnswerOp corroborates a form answer against the pinned form step:
// the submitter must be the pinned form actor's relationship to the ticket
// (requester or current assignee). The typed positional answers are already
// validated/accepted by the runner — the adapter only rechecks the pinned
// step/schema/actor and persists.
func validateFormAnswerOp(conflict func(string) error, t *domain.Ticket, step domain.WorkflowStep, run *application.WorkflowRun, in application.WorkflowMutationPlan, v application.FormAnswerOperation) error {
	if step.Type != domain.StepForm || step.Form == nil {
		return conflict("form answer at non-form step")
	}
	if v.SubmittedByUserID != in.ActorUserID {
		return conflict("form answer actor mismatch")
	}
	if len(v.AnswersJSON) == 0 {
		return conflict("form answer missing answers")
	}
	if v.SubmittedAt.Before(run.StartedAt) {
		return conflict("form answer before run start")
	}
	switch step.Form.Actor {
	case domain.FormActorRequester:
		if t.RequesterUserID == nil || *t.RequesterUserID != v.SubmittedByUserID {
			return conflict("form answer requires the ticket requester")
		}
	case domain.FormActorAssignee:
		if t.UserID == nil || *t.UserID != v.SubmittedByUserID {
			return conflict("form answer requires the current assignee")
		}
	default:
		return conflict("unknown form actor")
	}
	// Corroborate the positional schema: the persisted answers_json must be a
	// typed positional array that matches the pinned fields exactly (count, kind,
	// required values, and exact single_select canonical options) — the same
	// closed rules the runner already produced/typed/trimmed. Malformed JSON, an
	// object, null, a wrong scalar, a wrong count, or an unknown option is
	// rejected with a typed conflict (never silently persisted).
	return validatePositionalAnswers(conflict, step.Form.Fields, v.AnswersJSON)
}

// validatePositionalAnswers decodes a typed positional answer array and rechecks
// it against the pinned form schema. The accepted values mirror the runner output:
// checkbox → bool, short_text/long_text/single_select → string (already
// trimmed/canonical by the runner).
func validatePositionalAnswers(conflict func(string) error, fields []domain.FormField, raw []byte) error {
	var arr []json.RawMessage
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err := d.Decode(&arr); err != nil {
		return conflict("form answers malformed")
	}
	// Reject trailing content after the array as well.
	if d.More() {
		return conflict("form answers malformed")
	}
	if len(arr) != len(fields) {
		return conflict("form answer count mismatch")
	}
	for i, f := range fields {
		var v any
		if err := json.Unmarshal(arr[i], &v); err != nil {
			return conflict("form answer malformed")
		}
		if v == nil {
			return conflict("form answer must not be null")
		}
		switch f.Kind {
		case domain.FieldCheckbox:
			b, ok := v.(bool)
			if !ok {
				return conflict("form answer must be a boolean")
			}
			if f.Required && !b {
				return conflict("form answer is required")
			}
		case domain.FieldShortText, domain.FieldLongText, domain.FieldSingleSelect:
			s, ok := v.(string)
			if !ok {
				return conflict("form answer must be a string")
			}
			if f.Required && s == "" {
				return conflict("form answer is required")
			}
			if f.Kind == domain.FieldSingleSelect {
				if s != "" {
					found := false
					for _, o := range f.Options {
						if s == o {
							found = true
							break
						}
					}
					if !found {
						return conflict("form answer unknown option")
					}
				}
			}
		default:
			return conflict("form answer unknown field kind")
		}
	}
	return nil
}

// recheckApplyUsersTx verifies the ticket's requester (active) and current
// assignee (active agent+) during an Apply recheck. Any user-precondition
// mismatch relative to the plan's expected identity facts is a TYPED
// ErrWorkflowPositionConflict (never a NotFound/Inactive/Validation error);
// genuine infrastructure/database errors still propagate untouched.
func recheckApplyUsersTx(ctx context.Context, tx *sql.Tx, ticket *domain.Ticket) error {
	if ticket.RequesterUserID != nil {
		if err := requireApplyActiveUserTx(ctx, tx, *ticket.RequesterUserID, "requester"); err != nil {
			return err
		}
	}
	if ticket.UserID != nil {
		if err := requireApplyActiveAgentTx(ctx, tx, *ticket.UserID); err != nil {
			return err
		}
	}
	return nil
}

// requireApplyActiveUserTx is the conflict-typed counterpart used by the Apply
// recheck: a missing or inactive requester/actor is a typed position conflict.
func requireApplyActiveUserTx(ctx context.Context, tx *sql.Tx, id int64, kind string) error {
	var active int
	err := tx.QueryRowContext(ctx, `SELECT active FROM users WHERE id = ?`, id).Scan(&active)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.NewWorkflowPositionConflictError("workflow " + kind + " missing")
	}
	if err != nil {
		return fmt.Errorf("sqlite: read %s user: %w", kind, err)
	}
	if active != 1 {
		return domain.NewWorkflowPositionConflictError("workflow " + kind + " inactive")
	}
	return nil
}

// requireApplyActiveActorTx requires an active submitting actor during the Apply
// recheck, mapping a missing/inactive actor to a typed position conflict.
func requireApplyActiveActorTx(ctx context.Context, tx *sql.Tx, id int64) error {
	return requireApplyActiveUserTx(ctx, tx, id, "actor")
}

// requireApplyActiveAgentTx requires an active agent+ current assignee during
// the Apply recheck, mapping missing/inactive/wrong-role to a typed conflict.
func requireApplyActiveAgentTx(ctx context.Context, tx *sql.Tx, id int64) error {
	var active int
	var role string
	err := tx.QueryRowContext(ctx, `SELECT active, role FROM users WHERE id = ?`, id).Scan(&active, &role)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.NewWorkflowPositionConflictError("workflow assignee missing")
	}
	if err != nil {
		return fmt.Errorf("sqlite: read assignee user: %w", err)
	}
	if active != 1 {
		return domain.NewWorkflowPositionConflictError("workflow assignee inactive")
	}
	if !domain.Role(role).AtLeast(domain.RoleAgent) {
		return domain.NewWorkflowPositionConflictError("workflow assignee wrong role")
	}
	return nil
}

// claimantEligibleTx reports whether a user exists, is active, and holds an
// agent+ role (the assignment-target eligibility rule).
func claimantEligibleTx(ctx context.Context, tx *sql.Tx, userID int64) (bool, error) {
	var active int
	var role string
	err := tx.QueryRowContext(ctx, `SELECT active, role FROM users WHERE id=?`, userID).Scan(&active, &role)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("sqlite: read claimant: %w", err)
	}
	if active != 1 || !domain.Role(role).AtLeast(domain.RoleAgent) {
		return false, nil
	}
	return true, nil
}

// deskMemberTx reports whether userID is an active member of deskID (the
// actor-specific claim membership precondition, NOT a least_loaded selection).
func deskMemberTx(ctx context.Context, tx *sql.Tx, deskID, userID int64) (bool, error) {
	var one int
	err := tx.QueryRowContext(ctx, `SELECT 1 FROM desk_members dm JOIN users u ON u.id = dm.user_id AND u.active = 1 WHERE dm.desk_id=? AND dm.user_id=?`, deskID, userID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("sqlite: check desk membership: %w", err)
	}
	return true, nil
}

// sameIntPtr is the nil-safe identity comparison for plan facts.
func sameIntPtr(a, b *int64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// refreshWorkflowResult reads the persisted ticket and run back AFTER the writes
// (blocker 5: the result is refreshed state, never the caller-provided Result).
func refreshWorkflowResult(ctx context.Context, db *sql.DB, ticketID int64) (application.WorkflowExecutionResult, error) {
	t, err := scanTicketFrom(db.QueryRowContext(ctx, `SELECT `+ticketColumns+` FROM tickets t WHERE t.id=?`, ticketID))
	if errors.Is(err, sql.ErrNoRows) {
		return application.WorkflowExecutionResult{}, &domain.NotFoundError{Kind: "ticket", ID: ticketID}
	}
	if err != nil {
		return application.WorkflowExecutionResult{}, err
	}
	r, err := scanRunRow(ctx, db, ticketID)
	if err != nil {
		return application.WorkflowExecutionResult{}, err
	}
	return application.WorkflowExecutionResult{Ticket: t, Run: r}, nil
}

// currentVersionTx reads the category's current published version id and its
// immutable steps_json. A category with no current version returns (0, "", nil)
// — availability is the caller's check (create rechecks it inside the tx).
func currentVersionTx(ctx context.Context, tx *sql.Tx, categoryID int64) (int64, string, error) {
	var (
		cur   int64
		steps string
	)
	err := tx.QueryRowContext(ctx, `SELECT wv.id, wv.steps_json FROM category_workflows cw
		JOIN workflow_versions wv ON wv.id = cw.current_version_id AND wv.category_id = cw.category_id
		WHERE cw.category_id = ?`, categoryID).Scan(&cur, &steps)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, "", nil
	}
	if err != nil {
		return 0, "", fmt.Errorf("sqlite: read current version: %w", err)
	}
	return cur, steps, nil
}

// createTicketWithRunTx inserts the workflow-pinned ticket inside the caller's
// immediate transaction, assigning store-owned ID and Number (MAX+1, D8). It is
// distinct from createTicketTx only because it also writes workflow_version_id.
func createTicketWithRunTx(ctx context.Context, tx *sql.Tx, t *domain.Ticket, versionID int64) error {
	var number int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(number), 0) + 1 FROM tickets`).Scan(&number); err != nil {
		return fmt.Errorf("sqlite: next ticket number: %w", err)
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO tickets (number, title, description, requester_name, requester_email, requester_user_id, category_id, priority, state, user_id, created_at, updated_at, resolved_at, closed_at, workflow_version_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		number, t.Title, t.Description, t.RequesterName, t.RequesterEmail,
		nullableInt64(t.RequesterUserID), t.CategoryID,
		string(t.Priority), string(t.State), nullableInt64(t.UserID),
		formatTime(t.CreatedAt), formatTime(t.UpdatedAt),
		formatTimePtr(t.ResolvedAt), formatTimePtr(t.ClosedAt), versionID)
	if err != nil {
		return fmt.Errorf("sqlite: insert ticket: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("sqlite: ticket id: %w", err)
	}
	t.ID = id
	t.Number = number
	return nil
}

// insertRunTx inserts the run at its initial cursor/status/timestamps.
func insertRunTx(ctx context.Context, tx *sql.Tx, ticketID int64, cursor int, status string, started time.Time) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO ticket_workflow_runs (ticket_id, current_step_index, status, started_at, completed_at)
		VALUES (?, ?, ?, ?, NULL)`, ticketID, cursor, status, formatTime(started)); err != nil {
		return fmt.Errorf("sqlite: insert run: %w", err)
	}
	return nil
}

// applyCursorCAS advances the run cursor/status and completion timestamp via a
// compare-and-swap on the expected cursor/status. It is the SHARED cursor CAS used
// by BOTH CreateTicketWithRun and ApplyWorkflowPlan (task 5.4). Unchanged rows
// mean the expected cursor/status no longer holds — a typed position conflict with
// no writes (design S5 concurrency: the first commit advances; a stale plan fails).
// Concrete values only: no callback or generic transaction API.
func applyCursorCAS(ctx context.Context, tx *sql.Tx, ticketID int64, expectedCursor int, expectedStatus string, nextCursor int, nextStatus string, completedAt *time.Time) error {
	res, err := tx.ExecContext(ctx, `UPDATE ticket_workflow_runs
		SET current_step_index = ?, status = ?, completed_at = ?
		WHERE ticket_id = ? AND current_step_index = ? AND status = ?`,
		nextCursor, nextStatus, formatTimePtr(completedAt),
		ticketID, expectedCursor, expectedStatus)
	if err != nil {
		return fmt.Errorf("sqlite: update run: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("sqlite: update run rows: %w", err)
	}
	if n == 0 {
		return domain.NewWorkflowPositionConflictError("workflow position conflict")
	}
	return nil
}

// rowQuerier is satisfied by both *sql.DB and *sql.Tx so run rows can be read
// inside a transaction or after commit (refreshed result).
type rowQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// scanRunRow loads one run row by ticket id from any row queryer.
func scanRunRow(ctx context.Context, q rowQuerier, ticketID int64) (*application.WorkflowRun, error) {
	var (
		r       application.WorkflowRun
		status  string
		started string
		comp    sql.NullString
	)
	err := q.QueryRowContext(ctx, `SELECT ticket_id, current_step_index, status, started_at, completed_at
		FROM ticket_workflow_runs WHERE ticket_id = ?`, ticketID).Scan(&r.TicketID, &r.CurrentStepIndex, &status, &started, &comp)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, &domain.NotFoundError{Kind: "workflow run", ID: ticketID}
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite: read run: %w", err)
	}
	r.Status = status
	if r.StartedAt, err = time.Parse(timeLayout, started); err != nil {
		return nil, fmt.Errorf("sqlite: parse run started_at %q: %w", started, err)
	}
	if comp.Valid {
		v, err := time.Parse(timeLayout, comp.String)
		if err != nil {
			return nil, fmt.Errorf("sqlite: parse run completed_at %q: %w", comp.String, err)
		}
		r.CompletedAt = &v
	}
	return &r, nil
}

// recheckTicketUsersTx verifies the ticket's requester and assignee users exist,
// are active, and (for an assignee) hold an agent+ role — the persisted
// preconditions the fixed plan depends on (design S5/S6). Failures roll back.
func recheckTicketUsersTx(ctx context.Context, tx *sql.Tx, t *domain.Ticket) error {
	if t.RequesterUserID != nil {
		if err := requireActiveUserTx(ctx, tx, *t.RequesterUserID, "requester"); err != nil {
			return err
		}
	}
	if t.UserID != nil {
		if err := requireActiveAgentTx(ctx, tx, *t.UserID); err != nil {
			return err
		}
	}
	return nil
}

// requireActiveUserTx ensures a user exists and is active (for a requester).
func requireActiveUserTx(ctx context.Context, tx *sql.Tx, id int64, kind string) error {
	var active int
	err := tx.QueryRowContext(ctx, `SELECT active FROM users WHERE id = ?`, id).Scan(&active)
	if errors.Is(err, sql.ErrNoRows) {
		return &domain.NotFoundError{Kind: "user", ID: id}
	}
	if err != nil {
		return fmt.Errorf("sqlite: read %s user: %w", kind, err)
	}
	if active != 1 {
		return domain.NewInactiveUserError(kind)
	}
	return nil
}

// requireActiveAgentTx ensures a user exists, is active, and holds an agent+
// role (the assignment-target rule, design S5).
func requireActiveAgentTx(ctx context.Context, tx *sql.Tx, id int64) error {
	var active int
	var role string
	err := tx.QueryRowContext(ctx, `SELECT active, role FROM users WHERE id = ?`, id).Scan(&active, &role)
	if errors.Is(err, sql.ErrNoRows) {
		return &domain.NotFoundError{Kind: "user", ID: id}
	}
	if err != nil {
		return fmt.Errorf("sqlite: read assignee user: %w", err)
	}
	if active != 1 {
		return domain.NewInactiveUserError("user")
	}
	if !domain.Role(role).AtLeast(domain.RoleAgent) {
		return &domain.ValidationError{Field: "user", Message: domain.ErrMsgAssignTargetRole}
	}
	return nil
}

// applyWorkflowOperations applies the sealed, ordered, data-only operation list
// (design S5). This is the ONLY closed type switch over WorkflowOperation values
// in the adapter; it never inspects step.Type. A least_loaded operation is the
// PR5 refusal path (ErrLeastLoadedUnresolved → total rollback).
func applyWorkflowOperations(ctx context.Context, tx *sql.Tx, t *domain.Ticket, ops []application.WorkflowOperation) error {
	for _, op := range ops {
		switch v := op.(type) {
		case application.TransitionOperation:
			v.Audit.TicketID = t.ID
			if err := appendAuditEventsTx(ctx, tx, v.Audit); err != nil {
				return err
			}
			t.UpdatedAt = v.Audit.CreatedAt
			if v.Audit.ToValue != nil {
				t.State = domain.State(*v.Audit.ToValue)
				switch t.State {
				case domain.StateResolved:
					ts := v.Audit.CreatedAt
					t.ResolvedAt = &ts
				case domain.StateClosed:
					ts := v.Audit.CreatedAt
					t.ClosedAt = &ts
				}
			}
		case application.WorkflowStepOperation:
			v.Audit.TicketID = t.ID
			if err := appendAuditEventsTx(ctx, tx, v.Audit); err != nil {
				return err
			}
			t.UpdatedAt = v.Audit.CreatedAt
		case application.FormAnswerOperation:
			if err := insertAnswerTx(ctx, tx, t.ID, v.StepIndex, v.AnswersJSON, v.SubmittedByUserID, v.SubmittedAt); err != nil {
				return err
			}
			t.UpdatedAt = v.SubmittedAt
		case application.ClaimAssignmentOperation:
			if err := requireActiveAgentTx(ctx, tx, v.AssigneeUserID); err != nil {
				return err
			}
			t.UserID = &v.AssigneeUserID
			t.UpdatedAt = v.AssignmentAudit.CreatedAt
			v.AssignmentAudit.TicketID = t.ID
			if err := appendAuditEventsTx(ctx, tx, v.AssignmentAudit); err != nil {
				return err
			}
		case application.LeastLoadedAssignmentOperation:
			return ErrLeastLoadedUnresolved
		default:
			return fmt.Errorf("sqlite: unknown workflow operation %T", op)
		}
	}
	return nil
}

// insertAnswerTx persists one form step's typed positional answers with the
// submitting human actor and timestamp.
func insertAnswerTx(ctx context.Context, tx *sql.Tx, ticketID int64, stepIndex int, answers []byte, submittedBy int64, at time.Time) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO ticket_form_answers (ticket_id, step_index, answers_json, submitted_by_user_id, submitted_at)
		VALUES (?, ?, ?, ?, ?)`, ticketID, stepIndex, string(answers), submittedBy, formatTime(at)); err != nil {
		return fmt.Errorf("sqlite: insert form answer: %w", err)
	}
	return nil
}
