# Apply Progress — resolved-requester-confirmation (PR 1 of 5, stacked-to-main)

Worktree: `feat/55-resolved-requester-confirmation` @ /home/gtesta/Projects/tkt-worktrees/issue-55-resolved-closed-split
PR 1 scope = tasks 1.1–2.2 (Phase 1 + Phase 2). Strict TDD: RED → GREEN per task.
Toolchain: `GOTOOLCHAIN=go1.25.14` (go.mod requires ≥ 1.25.14; local go 1.25.11 otherwise refuses).

## Per-task progress

### 1.1 — JD-fixed reopen guard landed (commit `24fbb8a`)
Verification-only (files were already modified in-tree by Judgment Day):
- `go test ./internal/domain/ ./internal/application/ -run 'Reopen' -count=1` → ok (targets: `TestReopenFromResolvedWithoutReason`, `TestTransitionReopenResolvedWithoutReason`, `TestTransitionReopenClosedRequiresReason` PASS; reopen matrix regression green)
- `go test ./... -count=1` → all packages ok
- `go vet ./...` → clean
- `openspec validate --all --strict --no-interactive` → Totals: 17 passed, 0 failed
- `git status` before commit showed exactly the 3 expected modified files
- Committed: `24fbb8a fix(domain): reopen from resolved no longer requires a reason (JD regression fix)`

### 1.2 — D4 verify-first: plan recheck already pins `workflow_version_id` (no code changes)
Evidence (workflow_uow.go @ HEAD 24fbb8a):
- `ApplyWorkflowPlan` reloads the persisted pin at :161-169:
  ```go
  var pin sql.NullInt64
  if err := tx.QueryRowContext(ctx, `SELECT workflow_version_id FROM tickets WHERE id=?`, in.TicketID).Scan(&pin); err != nil { ... }
  if pin.Valid { ticket.WorkflowVersionID = &pin.Int64 }
  ```
- `validateMutationPlan` at :288-290:
  ```go
  if ticket.WorkflowVersionID == nil || *ticket.WorkflowVersionID != in.ExpectedVersionID {
      return conflict("workflow version mismatch")
  }
  ```
- Outcome: a detached ticket (NULL pin → `ticket.WorkflowVersionID == nil`) fails any in-flight
  `ApplyWorkflowPlan` with typed `ErrWorkflowPositionConflict` ("workflow version mismatch")
  BEFORE any write. **D4 branch resolved: no `workflow_runner.go` change needed.**
  Regression proof lands as task 2.2's RED characterization test.

### 2.2 — Detachment conflict + runner "awaiting confirmation" regressions (commit pending)
Strict TDD held even for characterization: test (a) came out genuinely RED, not pre-green.

**RED evidence — (a) detachment conflict (`TestWorkflowUoW_DetachedTicketPlanFailsWithVersionConflict`):**
- Expected (per 1.2/delegation): typed `ErrWorkflowPositionConflict` "workflow version mismatch".
- Actual first run: `apply on detached ticket = sqlite: pinned workflow version 0 not found, want typed workflow position conflict` → **FAIL**.
- Cause: the 1.2 evidence missed check ordering. `ApplyWorkflowPlan` calls `recheckSnapshot(ctx, tx, pinnedID(ticket), in.Workflow)` (workflow_uow.go ~:178) BEFORE `validateMutationPlan`; on a detached ticket `pinnedID` is 0, so `stepsByVersionTx` returned an infrastructure error ("pinned workflow version 0 not found") instead of the plan-staleness conflict. The nil-pin fact in `validateMutationPlan` (:288-290) was unreachable on the detached path.
- GREEN (D4 anticipated branch: "apply adds one fact check to the recheck"): added the single missing persisted-pin fact check in `ApplyWorkflowPlan` immediately after the pin reload (`workflow_uow.go`), returning `NewWorkflowPositionConflictError("workflow version mismatch")` before `recheckSnapshot`. One additive guard, no other statements touched; same message as the validator's, so all conflict tests (stale version, content, requester/assignee mismatches) stay green.
- After GREEN: `apply` returns typed `*domain.WorkflowPositionConflictError` (via `errors.As`) with "workflow version mismatch"; `assertApplyNoWrites` confirms zero writes (audits/answers 0, run 0/active/<nil>, state/assignee/pin unchanged).

**GREEN evidence — (b) runner awaiting-confirmation regression (`TestWorkflowRunner_ResolveLeavesAwaitingConfirmation`, in `workflow_runner_terminal_test.go` — helpers `stampedSnap`/`wf`/`res`/`cmdFor` are application_test-local):**
- Pre-green as expected: `resolve_ticket` terminal on a requester-owned ticket (snap seeds `RequesterUserID: 1`) plans `NextTicketState/Result.Ticket.State == resolved`, NO `TransitionOperation` with `to_value='closed'`, `ClosedAt == nil`, `ResolvedAt` stamped by the resolve transition, run completes (cursor 1/completed). Runner closes nothing — closure of requester-owned tickets is exclusively the requester's confirmation path (3.2).
- Existing terminal matrix rows (`TestWorkflowRunner_TerminalMatrix`, `TestWorkflowUoW_TerminalPersistedMatrix`) stayed green in the full run.

**Gates:** `go test ./... -count=1` all packages ok · `go vet ./...` clean · `gofmt -l` (excluding openspec/) empty · `openspec validate --all --strict --no-interactive` → Totals: 17 passed, 0 failed · `openspec validate --archived` → 7 passed, 0 failed.

**Files:** `internal/adapters/sqlite/workflow_uow.go` (one guard, 5 lines), `internal/adapters/sqlite/workflow_uow_terminal_test.go` (test a), `internal/application/workflow_runner_terminal_test.go` (test b).

## TDD Cycle Evidence

| Task | RED | GREEN | Refactor |
|------|-----|-------|----------|
| 1.1 | n/a (verify + land pre-existing JD fix) | reopen suite 3/3 PASS; full suite ok | n/a |
| 1.2 | n/a (evidence-only task) | n/a — zero code changes by design | n/a |
| 1.3 | migration 0010 + audit-shape cases fail pre-GREEN | migration test green; full suite ok | n/a |
| 1.4 | closure_via round-trip fails pre-GREEN | round-trip green; full suite ok | n/a |
| 2.1 | via-stamped audit accepted pre-GREEN | validator rejection green; matrices green | n/a |
| 2.2 (a) | **genuine RED**: `sqlite: pinned workflow version 0 not found` (not typed conflict) — recheck ordering gap; fixed by the single D4 fact check in `ApplyWorkflowPlan` | typed conflict "workflow version mismatch" + `assertApplyNoWrites` | n/a |
| 2.2 (b) | n/a (characterization: pre-green by design — pins runner's non-closing resolve) | resolve leaves resolved/no close/no closed_at; terminal matrices green | n/a |

## Gates snapshot (end of slice — updated per task)
- `go test ./... -count=1 -race`: pending final run
- `go vet ./...`: clean (after 1.1, re-confirmed after 2.2)
- `go build ./...`: pending final run
- `gofmt -l .`: empty after 2.2 (excluding openspec/ markdown)
- `openspec validate --all --strict`: 17/17 (re-confirmed after 2.2); `--archived` 7/7

## Deviations
- Task 2.2 GREEN was **not** "expected none": the detachment RED test exposed a real gap (1.2's evidence verified the fact in `validateMutationPlan` but missed that `recheckSnapshot` runs first and errors un-typed on a detached pin). Fixed with the single missing fact check in `workflow_uow.go` exactly as design D4 anticipated ("apply adds one fact check to the recheck"). Documented in 2.2 section above.
- Test (b) lives in `workflow_runner_terminal_test.go` (not `workflow_runner_test.go`): its helpers (`stampedSnap`, `wf`, `res`, `cmdFor`) are local to that file.
- Tasks 1.4/2.1 checkboxes were found unchecked at slice start despite committed evidence (`a7d63ba`, `fe7b1e9`); reconciled to `[x]` per the persisted-task contract.
