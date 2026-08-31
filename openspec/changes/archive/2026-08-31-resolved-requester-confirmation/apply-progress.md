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
| 4.2 | RED = build-fail on the new test file (`detailData has no field CanConfirm/CanComment` ×8, then arg-type fixes in the test itself) — missing behavior visible at compile boundary; 3 core scenario bodies were asserting the not-yet-existing flags | 4/4 subtests PASS (requester/agent/requester-NULL/other-states) after fixing one wrong test expectation (CanComment is true on open states by definition); full suite all packages ok; vet/gofmt/openspec clean | one test-expectation fix, no production refactor needed |

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

## PR 4 / Task 4.2 — `detailData` flags + `allowedNext` call-site filtering (commit `feat(http): requester-conditional confirmation flags and allowedNext`)

Strict TDD held: RED = compile-fail before any production change.

**RED:** new `internal/adapters/http/handlers_detail_flags_test.go` (`TestDetailDataConfirmationFlags`, 4 subtests)
failed to build — `detailData has no field or method CanConfirm/CanComment` (8 occurrences). The three core
scenario bodies already asserted the missing flags; the fourth (other-states regression) was written alongside.

**GREEN (production, +47 / −1 lines in `handlers_tickets.go`):**
- `detailData`: new `CanConfirm` + `CanComment` bool fields with delta-reference doc comments (D7;
  comment-timeline delta).
- `isRequester(actor, t)` — presentation mirror of application's unexported `isTicketRequester` (identity
  check, no role bypass; the application layer stays the enforcement point).
- `filteredNext(next, t)` — call-site filter next to `allowedNext` (which stays pure): on
  `resolved` + requester present it drops the `closed` target for EVERY actor (the requester closes via the
  confirmation control; role user sees no Move-to at all since role `user` has no transition capability and
  agents keep only the reopen); requester-NULL and all other states pass through unchanged.
- `detailDataFor` wiring: `CanConfirm = state==resolved && requester`; `CanComment = !closed || (state==resolved
  && requester)` — matching D7 verbatim, including the requester-NULL exclusion (identity predicate false ⇒
  no carve-out).

**Test-harness notes:** unit-call pattern via `context.WithValue(ctxKeyUser{}, &actor)` (deskRequest precedent);
fixture `seedResolvedFor` mirrors the 4.1 `seedResolved` shape (raw SQL requester pin or `makeLegacy`, real
service transitions). During GREEN one TEST expectation was corrected, not production code: on open states
`CanComment` must be TRUE (`!closed` for everyone) — the subtest initially asserted false.

**Gates:** focused run 4/4 PASS · `go test ./... -count=1` all packages ok (goldens untouched: template still
renders off `Closed`; the `CanComment` switch is 4.3's edit surface) · `go vet ./...` clean · `gofmt -l internal/`
empty · `openspec validate --all --strict` 17/17 · `--archived` 7/7.

**Files:** `internal/adapters/http/handlers_tickets.go`, `internal/adapters/http/handlers_detail_flags_test.go`
(new), `openspec/changes/resolved-requester-confirmation/tasks.md` (4.2 → [x]), `apply-progress.md` (this section).

**Scope held:** no template edits (4.3), no e2e (5.x), `allowedNext` itself unmodified.
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

## PR 4 slice — Task 4.1 Confirmation route + handler (BLOCKED before commit)

Worktree: `feat/55-resolved-requester-confirmation` @ a80049b. Allowed surfaces: `handlers_tickets.go`, `handlers_tickets_test.go`/`handlers_detail_test.go`, tasks.md, apply-progress.md. New test file `handlers_confirmation_test.go` (transition tests live in `handlers_detail_test.go`, but a sibling file keeps this endpoint's matrix self-contained; same package `httpadapter`).

### RED evidence (TestConfirmationEndpoint, `handlers_confirmation_test.go`)
- First run: build fail (`undefined: application.ErrMsgNotTicketRequester` — test needed the constant import) then all route-dependent subtests **405 Method Not Allowed** — no route existed. The unauthenticated row was pre-green by design (session middleware redirects anonymous POSTs to `/login` before any handler runs).
- Fixture corrections during RED (test-only, before GREEN):
  1. `seedUserRole(..., "admin@tkt.test")` collided with the harness's preset admin — separate fixture emails (`adm@tkt.test`).
  2. `assignTicket` was called AFTER driving the ticket to resolved → `closed tickets cannot be modified`; assignment now happens inside `seedResolved` while the ticket is still `new`.
- Final RED: 10/11 subtests failing on the missing route (405 / no write); 1 pre-green by design.

### GREEN evidence (handlers_tickets.go only)
- Route: `mux.HandleFunc("POST /tickets/{id}/confirmation", h.confirmation)` immediately after the transition route (Register, ~:67).
- Handler: `confirmation(w, r)` = ticketID 400 guard → ParseForm → `actor := *userFromContext(r.Context())` → `decision := r.Form.Get("decision")`; `"confirm"` → `h.tickets.ConfirmResolution`, `"reject"` → `h.tickets.RejectResolution`, anything else → `h.renderDetailError(..., &domain.ValidationError{Field: "decision", ...})` (422 via the existing validation mapping); on use-case error → `h.renderDetailError`; on success → `h.afterMutation(w, r, id, "ticket_detail")` (HX → `ticket_detail` fragment; full → 303 `/tickets/{id}`). One additive `context` import.
- Focused run after GREEN: `TestConfirmationEndpoint` — 10/11 PASS; the single remaining failure is the REAL defect below, not a handler gap.

### 🔴 BLOCKER — spec/code contradiction found by the 4.1 HTTP test (outside this slice's edit surfaces)
`TestConfirmationEndpoint/requester_rejects_own_resolved_ticket_detaches_the_workflow` fails at the SQL level:
`workflow_version_id = 2, want NULL after rejection (detached)`.

Root cause (verified in-tree at a80049b):
- `internal/adapters/sqlite/ticket_store.go:110-124` `updateTicketTx` SET clause lists `title, description, requester_name, requester_email, requester_user_id, category_id, priority, state, user_id, created_at, updated_at, resolved_at, closed_at` — **`workflow_version_id` is NOT in the SET clause**.
- `createTicketTx` (ticket_store.go:67-89) also omits it; only the workflow-UoW create writes the pin (`workflow_uow.go:1607-1613`, comment: "distinct from createTicketTx only because it also writes workflow_version_id").
- Therefore `RejectResolution`'s `t.WorkflowVersionID = nil` (ticket_service.go:383) is a pure in-memory change: `s.tx.Update` → `unitOfWork.Update` → `updateTicketTx` cannot persist NULL. The returned aggregate reports the detach; the stored row keeps the pin. Audit event and state persist correctly (those columns ARE in the SET clause) — only the detachment is lost.

This contradicts:
- design.md D4 (`openspec/changes/resolved-requester-confirmation/design.md:161-163`): "updateTicketTx writes the full row from ticketColumns … which includes workflow_version_id (explore.md §3.2)" — the design cites `ticketColumns` (the SELECT projection, :41) but the UPDATE statement is a separate, narrower column list.
- The workflow-execution delta scenario "rejection detaches the workflow pin" (persisted effect).
- 3.3's GREEN evidence: the sqlite-level round-trip was never actually proven for Update (only the fake store full-row-copies; the 3.3 note "mirroring the SQLite pin persistence" holds for the fake, not the real store). Task 2.2(a)'s raw-SQL detach + 1.2's recheck evidence are unaffected (they exercise ApplyWorkflowPlan's pin reload, not updateTicketTx).

Minimal fix (2 lines, file `internal/adapters/sqlite/ticket_store.go` — OUTSIDE this slice's allowed edit roots):
- `updateTicketTx`: add `, workflow_version_id = ?` to the SET clause + bind `nullableInt64(t.WorkflowVersionID)`.
- Optionally one sqlite round-trip test (detach via RejectResolution/Update then re-read the row). Strict TDD for that fix would be RED on the current tree (the pin persists) — exactly the state my test pinned.

### Why blocked (contract)
`applyState` equivalent: the 4.1 acceptance ("go test ./... green") is unmet by exactly one subtest, whose fix requires editing a file outside the delegated edit surfaces ("internal/adapters/http/handlers_tickets.go, ..._test.go, tasks.md, apply-progress.md — nothing else"). Committing now would (a) leave the suite red, (b) commit a test that documents a defect while the defect ships. Per the governance skill ("if implementation, tests, and specs contradict each other, BLOCK the work unit"), 4.1 is NOT committed; the tree holds only the new test file + handler changes.

### Gates snapshot (end of slice, pre-commit)
- `GOTOOLCHAIN=go1.25.14 go test ./internal/adapters/http -run TestConfirmationEndpoint -count=1`: FAIL — 1 subtest (the detachment defect above); 10/11 pass
- `go test ./... -count=1`: FAIL — only that same subtest; domain/application/sqlite/cmd all ok
- `go vet ./...`: clean; `gofmt -l internal/`: empty (both checked on the current tree)
- openspec validate: NOT re-run pre-block (no spec files touched by 4.1; last green 17/17 after 3.4)

### Files touched (uncommitted, staged-ready)
- `internal/adapters/http/handlers_tickets.go` (route + confirmation handler + context import)
- `internal/adapters/http/handlers_confirmation_test.go` (NEW — RED-first auth matrix)
- `openspec/changes/resolved-requester-confirmation/apply-progress.md` (this section)
- tasks.md 4.1 left `[ ]` (task incomplete until the blocker is resolved and gates pass)

### Unblock path for the orchestrator
1. Extend edit surfaces to include `internal/adapters/sqlite/ticket_store.go` (+ optional store test) and re-delegate 4.1 completion; OR fold the 2-line fix into a dedicated PR-4 slice task (recommended: RED sqlite round-trip → the 2-line SET-clause fix → full suite).
2. Re-run gates, then commit `feat(http): requester confirmation endpoint` (test + handler together, per work-unit-commits) and mark 4.1 `[x]`.

### 4.1 — Confirmation route + handler (commits `bc8265a`, `b978ab5`)
- RED: new `handlers_confirmation_test.go` full auth matrix (requester confirm→303+closed; reject→303+in_progress+pin NULL; agent/admin/root→403 ErrMsgNotTicketRequester; unrelated role-user→404; missing/unknown decision→422 no-write; unauthenticated→303 login). All failures were a 405 route-missing (pre-green unauthenticated row).
- **Blocked → resolved**: the reject subtest exposed a REAL production defect — `updateTicketTx` (ticket_store.go) omitted `workflow_version_id` from its SET clause (design D4 cited the SELECT projection `ticketColumns`, not the UPDATE list), so the detachment never persisted in SQLite. Fixed: `updateTicketTx` now writes `workflow_version_id` (nullable bind). The failing scan was a test bug (`scanOneInt` can't read NULL) → added `scanNullInt` helper. Full matrix green.
- GREEN: route + `confirmation` handler (decision dispatch → ConfirmResolution/RejectResolution; unknown decision → 422 ValidationError; success → afterMutation). Gates: go test ./... green, vet clean, gofmt clean, openspec 17/17.
- Note: this validates the design's D4 premise was wrong (cited SELECT not UPDATE); the 3.3 sqlite detachment round-trip is now proven by this HTTP test. Re-verify the application-layer stored-row claim was via the fake (full-row copy), now covered by the real store round-trip.

### 4.2 — detailData flags + allowedNext filtering (commit `0942bca`)
- RED: `handlers_detail_flags_test.go` (TestDetailDataConfirmationFlags) — compile failure: `detailData has no field CanConfirm/CanComment`.
- GREEN: `CanConfirm`/`CanComment` fields; local `isRequester` mirror; `filteredNext` at the call site (allowedNext stays pure): resolved+requester drops `closed`, keeps reopen; requester-NULL keeps closed+reopen; other states unchanged. One test-expectation fix (CanComment true on open states — production untouched).
- Gates green; goldens untouched (template still renders off `Closed`; CanComment switch is 4.3).

### 4.3 — Template control + golden regeneration (commit `4016cd9`)
- RED: confirmation panel not rendered; comment form gating on Closed broke requester-in-resolved.
- GREEN: `resolution-confirmation` panel (eyebrow + check + bold question + helper + two buttons posting to /tickets/{id}/confirmation) in the conversation column, rendered on `CanConfirm`; comment form gated on `CanComment`; CSS block for the green-tinted panel in styles.html.
- **Two test-side fixes during GREEN**: (1) `closedDetailData` fixture did not reset `CanComment` -> closed tickets rendered the comment form (fixed: set CanComment=false); (2) trailing-whitespace gate failed on tickets_show because the `{{if .CanConfirm}}` block left indented whitespace when not rendered (fixed: control directives at column 0, per the template's existing pattern).
- Goldens regenerated (shared stylesheet change ripples to all page goldens — expected: the new CSS block inlines in every render); added 3 new golden cases (requester-resolved requester view, agent view, legacy-resolved agent view). Re-run without -update: stable.
- Gates: go test ./... green, vet clean, gofmt clean, openspec 17/17.

### 5.1 — Rework the existing resolved→closed journey
- Reworked `e2e/tests/ticket-detail.spec.ts` "comment form hidden on closed states" so the requester-owned resolved ticket can NO LONGER be closed via the state transition (issue #55 gate): the Move-to control offers only the reopen (in_progress), never `closed`, and the state stays `resolved`. Comment-form-hidden on resolved/closed/cancelled assertion preserved. Direct-POST (403) rejection is exhaustively covered by Go tests (spec header note). Cancelled leg unchanged (terminal).
- Validation: spec parses/type-checks under `npx playwright test --list` (test listed at ticket-detail.spec.ts:56). Browser/node_modules installed in the worktree for list/parse validation. The full Playwright run happens in CI (the shared stylesheet/golden change already covered by unit tests).
- README coverage row updated (`e2e/README.md:50`).

### 5.1 — Rework the existing resolved→closed journey
- `ticket-detail.spec.ts` leg resolved→closed reworked: the requester-owned resolved ticket can no longer be closed via Move-to (gate blocks `resolved → closed` with a requester) → the journey now asserts Move-to offers no `closed` (only `in_progress` reopen) and comment form remains hidden; cancelled leg unchanged.
- Coverage row in `e2e/README.md:50` updated accordingly.
- `npx playwright test --list` validates the new spec compiles and lists the renamed journey.

### 5.2 — New requester journeys (commit pending 5.2 block)
- New `e2e/tests/ticket-confirmation.spec.ts` with 3 journeys: confirm → closed (panel gone, Closed badge), reject → manual in_progress (detached), blocked close (agent view). Uses `createUserAsAdmin`/`loginAs`/`createTicketViaUi` + helper `requesterOwnedResolvedTicket` (requester creates, admin drives to resolved, login as requester). Asserts the resolution-confirmation panel + HTMX swaps on `/tickets/{id}/confirmation`.
- Validation: `npx playwright test --list` lists all 3 new journeys (type-check via Playwright parse) — the full suite requires a running browser/server and runs in CI.
- README coverage rows added (ticket-confirmation journeys + HTMX confirmation provenance).

### 5.2 — New requester journeys (commit `5bc4d9f` + 5.1-5.2 file batch)
- New `e2e/tests/ticket-confirmation.spec.ts` (3 journeys: confirm → closed with panel gone + Closed badge; reject → in_progress detached; blocked close for non-requester). Uses the same user-creation + ticket-ownership helper as 3.2/3.x (requester creates, staff drives to resolved, login as requester).
- Validation: `npx playwright test --list` lists all 3 new journeys (parse/type-check via Playwright); the full suite requires a running browser/server in the worktree — CI runs it.
- README rows added (functional + HTMX confirmation provenance).

### 5.3 — Final verification gate (verify-report.md)
- Verification performed fresh per the spec deltas; every requirement × scenario has a listed filing above. Report written to `openspec/changes/resolved-requester-confirmation/verify-report.md`.
- Gates (fresh, captured 2026-08-31 on branch HEAD `5b7b3ee`): `go test ./...` 0 (all packages ok), `go build ./...` 0, `go vet ./...` 0, `gofmt -l internal/ web/` 0, `openspec validate --all --strict` 17/17, `npx playwright test --list` lists the new journeys (3 in ticket-confirmation + reworked closed-states), browser run CI-only.
- Judgement Day: APPROVED (CRITICAL JD-A-001 fixed + regression round 2 verified).
- Next: `sdd-sync` + `sdd-archive`.
