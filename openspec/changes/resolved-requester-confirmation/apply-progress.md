# Apply Progress — resolved-requester-confirmation (stacked-to-main)

Worktree: `feat/55-resolved-requester-confirmation` @ /home/gtesta/Projects/tkt-worktrees/issue-55-resolved-closed-split
PR 1 scope = tasks 1.1–2.2 (Phase 1 + Phase 2) — complete. PR 2 slice = task 3.1 (Phase 3 first half); 3.2–3.4 remain.
Strict TDD: RED → GREEN per task.
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

### 2.2 — Detachment conflict + runner "awaiting confirmation" regressions (commit `6119ec5`)
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
| 3.1 | stage 1 compile-fail (`undefined: application.ErrMsgClosureRequiresConfirmation`) + stage 2 behavioral (agent+admin denied cases `got <nil>`; requester-NULL case missing `manual_agent` stamp) | 3/3 subtests PASS; focused run ok; vet/gofmt/openspec clean | n/a |
| 3.2 | compile-fail (`undefined: h.svc.ConfirmResolution`) then seed-helper invariant fix; confirm rows RED before GREEN | confirm matrix PASS; full suite ok | n/a |
| 3.3 | compile-fail: `h.svc.RejectResolution undefined` (3 call sites) | 3/3 subtests PASS (detach+reopen, manual plain reopen, agent-keeps-pin); full suite all packages ok; vet/gofmt clean | n/a |
| 3.4 | behavioral RED: requester-public-on-resolved got `comments on closed tickets are not allowed` (only failing row; denial rows pre-green by design — see matrix notes in 3.3/3.4 sections) | carve-out matrix PASS; full suite all packages ok; vet clean; openspec 17/17 | gofmt alignment slip caught by gate, amended same task |

## Gates snapshot (end of slice — updated per task)
- `go test ./... -count=1`: all packages ok after 2.2; after 3.1 application/domain/sqlite ok but `internal/adapters/http` has 4 pre-existing tests failing on the new gate (see Deviations — outside this slice's edit roots)
- `go test ./... -count=1 -race`: pending final run
- `go vet ./...`: clean (after 1.1, re-confirmed after 2.2 and 3.1)
- `go build ./...`: pending final run
- `gofmt -l .`: empty after 2.2, re-confirmed after 3.1 (excluding openspec/ markdown)
- `openspec validate --all --strict`: 17/17 (re-confirmed after 2.2 and 3.1); `--archived` 7/7

## PR 2 / Task 3.1 — Manual-closure gate + `ClosureVia` stamping (commit `feat(application): block manual closure of requester-owned resolved tickets`)

Strict TDD held: RED captured at both stages before any gate/stamping code.

**RED stage 1 (compile):** new `internal/application/ticket_confirmation_test.go` (3 subtests,
`TestManualClosureRequiresConfirmation`) failed to build — `undefined: application.ErrMsgClosureRequiresConfirmation`.

**RED stage 2 (behavioral, after adding only the inert error-message constant to policy.go):**
- `assigned_agent_denied_on_requester-owned` → `got <nil>` (no gate yet)
- `admin_denied_on_requester-owned` → `got <nil>`
- `requester-NULL_closes_manually_via_assigned_agent` → `closure event must record closure_via "manual_agent", got <nil>` (no stamping yet)

**GREEN (implementation, +34 lines):**
- `internal/application/policy.go`: `ErrMsgClosureRequiresConfirmation` const + `isTicketRequester(actor, t)` identity predicate (D3; `t.RequesterUserID != nil && *t.RequesterUserID == actor.ID`).
- `internal/application/ticket_service.go` `Transition`: gate `from == resolved && to == closed && RequesterUserID != nil` → `ForbiddenError(ErrMsgClosureRequiresConfirmation)` BEFORE `t.Transition` (no write); after the transition, a `resolved → closed` event is stamped `event.ClosureVia = &domain.ClosureViaManualAgent` before the single `s.tx.Update`.
- Test helper `seedResolvedTicket` reuses `seededTicket` + direct store Update to pin `RequesterUserID`/`UserID` (harness pattern from `TestAssignReassignRequiresReason`).

**Gates:** focused run `go test ./internal/application -run TestManualClosureRequiresConfirmation -count=1` → ok · `go test ./... -count=1` → application/domain/sqlite all ok; HTTP failures documented below · `go vet ./...` clean · `gofmt -l` clean · `openspec validate --all --strict` → 17/17 · `--archived` → 7/7.

**Deviation — pre-existing HTTP tests now fail on the new gate (expected per D8):** 4 tests in
`internal/adapters/http/handlers_detail_test.go` drive `resolved → closed` on requester-owned tickets
(the harness `seedTicket` always creates via the admin session, so every seeded ticket has a requester):
`TestTicketTransitionFullCycle`, `TestTicketTransitionReopenRequiresReason`, `TestTicketTransitionReopenWithReason`,
`TestTicketCommentOnClosedTicketRejected/closed`. Fixing them needs the seed/ticket fixture to be requester-NULL —
an edit to `internal/adapters/http/*_test.go`, which is OUTSIDE this slice's allowed edit roots
("internal/application/*, tasks.md, apply-progress.md — nothing else"). NOT fixed here by contract; PR 2's second
half (task 3.2) or the orchestrator must adjust these 4 tests before `go test ./...` is fully green.
Design D8 already anticipates reworking the resolved→closed journeys (5.1 covers e2e; this is the http test-layer twin).

## Deviations
- Task 2.2 GREEN was **not** "expected none": the detachment RED test exposed a real gap (1.2's evidence verified the fact in `validateMutationPlan` but missed that `recheckSnapshot` runs first and errors un-typed on a detached pin). Fixed with the single missing fact check in `workflow_uow.go` exactly as design D4 anticipated ("apply adds one fact check to the recheck"). Documented in 2.2 section above.
- Test (b) lives in `workflow_runner_terminal_test.go` (not `workflow_runner_test.go`): its helpers (`stampedSnap`, `wf`, `res`, `cmdFor`) are local to that file.
- Tasks 1.4/2.1 checkboxes were found unchecked at slice start despite committed evidence (`a7d63ba`, `fe7b1e9`); reconciled to `[x]` per the persisted-task contract.
- Task 3.1 leaves `internal/adapters/http` tests red on the new gate (see PR 2 / Task 3.1 section above): the 4 failing tests are the known policy fallout; the fix belongs to a slice whose edit roots include `internal/adapters/http/*_test.go`. Acceptance criterion of 3.1 ("go test ./... green; state machine tests from 1.1 untouched and green") is met for domain + application; the residual HTTP red is a fixture-adjustment task, not an implementation gap — flagged for the orchestrator before PR 2 completes.

### 3.1 — Manual-closure gate + ClosureVia stamping (commit `85ff361`)
- RED→GREEN per delegation; plus HTTP fixtures repaired: 4 tests that walked requester-owned tickets to closed via the service (now blocked by the gate) converted to requester-NULL fixtures via the new `makeLegacy` harness helper (commit `6ccd1bf`). Full suite green after the fixture fix.

### 3.2 — ConfirmResolution
- RED: new cases in `ticket_confirmation_test.go` (requester confirms -> closed + closure_via=requester_confirmation + actor=requester; non-resolved state-machine rejections with no audit; agent/admin/root Forbidden ErrMsgNotTicketRequester; out-of-scope role user NotFound). Compile-RED on missing method, then one assertion RED: the seed helper did not stamp resolved_at (fixture invariant) — fixed in the helper.
- GREEN: `TicketService.ConfirmResolution` (scoped read -> isTicketRequester -> Transition(closed,"") -> stamp actor + ClosureViaRequesterConfirmation -> one tx.Update); `ErrMsgNotTicketRequester` added to policy.go.
- Gates: go test ./... green (all packages), targeted reopen/confirm runs green.

### 3.3 — RejectResolution + workflow detachment (commit `e2eff94`)
Strict TDD: compile-RED then GREEN; `seedPinnedResolvedTicket` helper added (pins `WorkflowVersionID` via a second direct store Update — the fake full-row-copies on `store`, mirroring the SQLite pin persistence).

**RED:** `h.svc.RejectResolution undefined` (3 call sites, build fail).

**GREEN (+34 lines, `ticket_service.go` only):** `RejectResolution` = scoped read → `isTicketRequester` gate (same as ConfirmResolution, `ForbiddenError(ErrMsgNotTicketRequester)`) → `t.Transition(StateInProgress, "", now)` (domain clears `ResolvedAt`, reason-free) → `t.WorkflowVersionID = nil` → stamp `event.Actor`/`ActorUserID` (requester; no ClosureVia, no workflow actor) → ONE `s.tx.Update` (row + audit atomically). No workflow_runner/uow changes (D4 held). Also documented on `Transition`: generic path never detaches the pin (agent reopen MUST NOT detach).

**Assertions covered:** pinned reject → in_progress + `WorkflowVersionID == nil` (returned aggregate AND stored row) + `ResolvedAt` cleared + `ClosedAt` nil + exactly 1 event (`actor=requester`, `ActorUserID=requester.ID`, never `"workflow"`, resolved→in_progress); manual reject → plain in_progress; agent reopen `resolved→in_progress` via `Transition` keeps pin `== 7`.

### 3.4 — Comment carve-out on resolved (commit `dda2bea`)
Strict TDD: behavioral RED (pre-green denial rows by design), then GREEN.

**RED (3 matrix corrections during RED, documented honestly):**
1. First matrix naively used unassigned agent/other-user for the non-requester + requester-NULL rows → those hit the scope wall (NotFound, from `matchesQuery`: agent=ScopeAssigned, user=ScopeOwned) instead of the guard. Unassigned agents can never reach a state guard on an unassigned ticket — scope-first is the existing policy order.
2. Second pass asserted admin (ScopeAll) as out-of-scope → also corrected: admin is always in scope, reaches the guard.
3. Final matrix: in-scope rows (assigned agent, admin, root) assert `ForbiddenError(ErrMsgCommentOnClosedTicket)` with no comment write; out-of-scope rows (unassigned agent, role-user on someone else's/requester-NULL) assert NotFound from the scoped read — same no-write outcome, earlier wall, no existence leak.

Final RED: exactly one failing row — `requester public comment on own resolved ticket` got `comments on closed tickets are not allowed` — the carve-out gap itself; every denial row pinned current behavior.

**GREEN (comment_service.go guard only):**
```go
if domain.IsClosed(t.State) {
    if t.State == domain.StateResolved && isTicketRequester(actor, t) {
        // carve-out: fall through to visibility/author rules
    } else {
        return nil, domain.NewForbiddenError(domain.ErrMsgCommentOnClosedTicket)
    }
}
```
Requester-NULL falls to the else branch automatically (identity predicate false without a requester). Role rule untouched: the `internal` visibility gate runs BEFORE the state guard, so requester `internal` on resolved still → `ErrMsgUserCannotCommentInternal`.

**Gates (per task):** 3.3: full `go test ./... -count=1` all packages ok; vet clean; gofmt clean; commit `e2eff94`. 3.4: focused + full `go test ./... -count=1` all packages ok; vet clean; openspec 17/17 + archived 7/7. One process slip: 3.4's first commit landed with a gofmt-dirty test file (alignment-only diff); caught by the gate, `gofmt -w` + `--amend` within the same task, re-tested, re-committed as `dda2bea` — no separate fix commit needed.

## Deviations (PR 3 slice)
