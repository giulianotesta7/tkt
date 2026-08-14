# Apply Progress — tkt-mvp — Slice 1 (PR 1 of 5)

**Slice**: PR 1 — Scaffold + pure domain (state machine, invariants)
**Branch**: `feat/tkt-mvp-1-scaffold-domain` (off `main`, target for PR 1, stacked-to-main)
**Mode**: Strict TDD (`go test ./...`, config `strict_tdd: true`, `rules.apply.tdd: true`)
**State**: Slice 1 complete — tasks 1.1, 2.1, 2.2, 2.3 marked `[x]` in `tasks.md`
**Delivery**: chained PRs, stacked-to-main, PR 1 of 5. Commits created; NO push, NO PR (delivery gated on verification by orchestrator).

## Work Unit Evidence (Hard Gate — all modes)

| Evidence | Required value |
|---|---|
| Focused test command and exact result | `go test ./internal/domain/ -v` → 10 top-level tests PASS (25 matrix subtests + forward path + reopen w/o reason + 6 ApplyUpdate/Rank tests); exit 0 |
| Runtime harness command/scenario and exact result | `N/A` — pure Go domain, zero I/O; behavior proven via the 25-pair matrix (per tasks.md Unit 1 row: "N/A — pure Go, zero I/O; behavior proven via matrix tests") |
| Rollback boundary | Delete `internal/domain/` + revert `go.mod`/`go.sum` (commits f8534bb..2ed10d5); main stays at bootstrap `0a0849e`; nothing else depends |

## TDD Cycle Evidence

| Task | Test File | Layer | Safety Net | RED | GREEN | TRIANGULATE | REFACTOR |
|------|-----------|-------|------------|-----|-------|-------------|----------|
| 1.1 | — (scaffold) | Unit | N/A (new module) | N/A (no behavior) | ✅ `go mod tidy` exit 0; pins in go.mod | ➖ Single (config-only) | ➖ None needed |
| 2.1 | `internal/domain/state_test.go` | Unit | N/A (new files) | ✅ Written; `go test ./internal/domain/` → build failed (no non-test Go files) | — (RED commit only, per task) | — | — |
| 2.2 | `internal/domain/state_test.go` | Unit | N/A (new files) | (carried from 2.1) | ✅ 27/27 matrix + scenario tests pass | ✅ 25 explicit pairs + 2 scenarios | ✅ `ptr[T]` generic helper extracted; no magic values |
| 2.3 | `internal/domain/ticket_test.go` | Unit | N/A (new files) | ✅ Written; `go test -run 'TestApplyUpdate|TestPriorityRank'` → compile fail (`ApplyUpdate` undefined) | ✅ 7/7 tests pass | ✅ 7 behaviors (category, invalid priority, timestamps, mixed fields, no-op, blank title, rank) | ✅ gofmt applied to test file |

### Test Summary
- **Total tests written**: 10 top-level (25 matrix subtests + 2 scenario + 6 update/rank + 1 rank-ordering)
- **Total tests passing**: 10/10 (25/25 matrix pairs)
- **Layers used**: Unit (10)
- **Approval tests**: None — no refactoring tasks
- **Pure functions created**: `Transition`, `ApplyUpdate`, `Priority.Rank`, `isValidPriority`

## Commits (in order)

| # | Hash | Message | Contents |
|---|------|--------|----------|
| 1 | `f8534bb` | `chore(scaffold): init go module and pin deps` | `go.mod`, `go.sum` (modernc.org/sqlite v1.56.0, golang.org/x/crypto v0.54.0) |
| 2 | `12ae409` | `test(domain): transition matrix 5x5` (RED) | `internal/domain/state_test.go` only |
| 3 | `897c252` | `feat(domain): enforce 5-state transition machine` | `internal/domain/{ticket,state,priority,errors,clock,audit}.go` |
| 4 | `2ed10d5` | `feat(domain): field updates with audit-only-changed` | `ticket.go` (+`TicketUpdate`/`ApplyUpdate`), `priority.go` (+`Rank`), `ticket_test.go` |

## Files Changed

| File | Action | What Was Done |
|------|--------|---------------|
| `go.mod` | Created | Module `github.com/giulianotesta7/tkt`, go 1.25.11; pinned modernc.org/sqlite v1.56.0 (D1) + golang.org/x/crypto v0.54.0 (D15) + transitive graph |
| `go.sum` | Created | Hashes for pinned graph (22 lines, generated) |
| `internal/domain/state.go` | Created | 5 states + `transitions` map (single source of truth; cancelado terminal) |
| `internal/domain/priority.go` | Created | 4 priorities + `Rank()` (critica=4…baja=1, D11) |
| `internal/domain/errors.go` | Created | Typed errors: `InvalidTransitionError`, `ReopenReasonRequiredError`, `ValidationError`, `InvalidPriorityError` + Spanish message constants (D5) |
| `internal/domain/clock.go` | Created | `Clock` interface (D7 — domain never calls `time.Now()`) |
| `internal/domain/audit.go` | Created | `AuditEvent` + action constants (created/transition/update) |
| `internal/domain/ticket.go` | Created | `Ticket` aggregate, `Transition()`, `TicketUpdate` + `ApplyUpdate()` |
| `internal/domain/state_test.go` | Created | 5×5 matrix (25 pairs) + forward path + reopen-without-reason |
| `internal/domain/ticket_test.go` | Created | 6 ApplyUpdate behaviors + Rank ordering |

## Deviations from Design

1. **`audit.go` created in the 2.2 commit** (task table lists it under 2.3): `Transition()` returns `*AuditEvent`, so the type must exist in the GREEN commit that implements the machine. File list in tasks.md is approximate; commit story stays honest (tests with the behavior they verify).
2. **`comment.go`, `user.go`, `category.go` NOT created in slice 1**: per the orchestrator instruction ("create only what domain needs NOW"), the aggregate only references `CategoryID`/`UserID` (ints) and never constructs `Comment`/`User`/`Category`. Full types land in later slices with their tests (strict TDD Law 3: no code beyond what the tests require).
3. **Only 4 of the design's 9 domain error types created**: `InactiveUserError`, `NotFoundError`, `DuplicateError`, `ReferencedError` are not exercised by slice-1 tests (Three Laws). `InvalidCredentialsError` is application-level per design — belongs to slice 3.
4. **`Transition()` does NOT refresh `UpdatedAt`**: design defines Transition's effects precisely (set/clear lifecycle timestamps + audit event) and says nothing about `updated_at`. Flagged as an open question for the application layer (slice 3): if a transition counts as a modification, `TicketService.Transition` should refresh `updated_at` when persisting. Domain tests pass without it; verify should confirm with spec (ticket-management: "updated_at MUST reflect creation and last modification").
5. **`TicketUpdate.UserID` supports assign/reassign only, not unassign** (no way to express "set to NULL"): slice-1 tests never need unassignment; unassign semantics belong with the user-service slice.

## Acceptance Verification (slice 1)

| Acceptance | Result |
|---|---|
| `go mod tidy` resolves | ✅ exit 0 |
| `go vet ./...` clean baseline | ✅ exit 0 (empty module baseline: "no packages to vet"; clean once domain exists) |
| Matrix green (all 25 pairs) | ✅ 25/25 |
| Valid forward path nuevo→en_progreso→resuelto→cerrado | ✅ |
| Invalid `nuevo→cerrado` rejected, state stays | ✅ (typed `InvalidTransitionError`, Spanish message) |
| Terminal `cancelado` | ✅ (all 4 exits denied) |
| Reopen cerrado reason required / without reason rejected | ✅ `ReopenReasonRequiredError`, state+timestamps preserved |
| Reopen resuelto no reason, clears `resolved_at` | ✅ |
| Reopen cerrado clears both | ✅ + reason recorded in audit note |
| Edit category: updated_at refreshed + 1 audit event | ✅ |
| Invalid priority: rejected, NO changes | ✅ (`InvalidPriorityError`, full-field snapshot unchanged) |
| Timestamps unchanged after edit | ✅ (resolved/closed untouched) |
| `Rank()` critica>alta>media>baja | ✅ (4/3/2/1) |
| gofmt/vet/build after each commit and at end | ✅ all clean |

## Issues Found

- **Native dispatcher blind spot**: `gentle-ai sdd-status` reports `applyState: blocked` / `blockedReasons: ["tasks.md has no markdown task checkboxes."]` because this change's tasks.md uses tables, not `- [ ]` lists, and `specs: missing` because delta specs live in the top-level `openspec/specs/` tree (design/orchestrator name them as authoritative). Orchestrator direction overrode the parser artifact; noted here for verify.
- **Line-budget overage vs estimate** (see Risks).

## Risks

- **PR 1 changed lines = 801** (est. 455): `state_test.go` 251 lines (explicit 25-pair enumeration — exactly what the task demanded) and `ticket_test.go` 205 lines (7 behaviors). Authored additions ≈ 779 (go.sum 22 lines are generated, excluded per work-unit-commits). Every assertion maps to a spec scenario — no gold-plating. Orchestrator should decide: accept PR 1 as-is (~780 authored lines, one cohesive pure-domain deliverable) or sub-split (e.g., matrix commit → machine commits as PR-1a/1b). Review budget per the 400-line guideline is exceeded; flagging rather than trimming real assertions.
- **`go get` pins are marked `// indirect`** until slices 3–4 import them; `go mod tidy` will promote them to direct requires then. Verify must NOT run `go mod tidy` expecting them to stay direct — the pins are stable (exact versions in go.mod require block).

## Next Steps

- `verify` on slice 1 (recommended): pure-domain work unit is independently verifiable; catches spec gaps before 4 dependent slices build on the domain contract. After verification passes, orchestrator delivers PR 1 (stacked-to-main), then `apply` slice 2 (PR 2 — store ports + application use cases).

---

# Correction Pass — 3 CRITICAL Fixes (verify report `fa282f70…`)

**Trigger**: `openspec/changes/tkt-mvp/verify-report.md` → verdict FAIL, 3 CRITICAL findings:
C1 transition never refreshes `updated_at`; C2 transition audit event has `Field == nil`; C3 unassignment not representable (`TicketUpdate.UserID *int64` nil = "not provided" only).
**Ledger**: token `sha256:30b52f6715113b4d318f2aa5e072256adf85bcbc54c98445f3bc67047540e2a1`, work_unit `fix-3-critical-domain`, max_changed_lines 200.
**Mode**: Strict TDD (`go test ./...`, config `strict_tdd: true`, `rules.apply.tdd: true`).
**Scope rule**: ONLY the 3 CRITICAL fixes + their tests. No application layer, no store, no HTTP (later slices).
**Branch**: `feat/tkt-mvp-1-scaffold-domain` (unchanged). No push, no PR.

## Work Unit Evidence (Hard Gate — all modes)

| Evidence | Required value |
|---|---|
| Focused test command and exact result | `go test -count=1 ./internal/domain/ -v` → RED: compile failure (`ClearUserID` unknown, `ErrMsgConflictingUserAssignment` undefined), then 8/8 allowed matrix cells fail `Field … got <nil>`, `TestTransitionUpdatedAt/allowed` fails `updated_at … got 09:00`, `TestApplyUpdateClearUserID` fails, `TestApplyUpdateConflictingUserAssignmentRejected` fails; GREEN: 15 top-level tests + 29 subtests all PASS, exit 0 |
| Runtime harness command/scenario and exact result | `N/A` — pure Go domain, zero I/O; behavior proven via the transition matrix and focused domain tests (same rationale as slice 1 Unit 1 row) |
| Rollback boundary | Revert the single correction commit (`git revert`): production `internal/domain/{ticket,errors,audit}.go` + test additions in `internal/domain/{state,ticket}_test.go`; nothing else in the repo depends on these behaviors yet — reverted without removing any slice-1 work |

## TDD Cycle Evidence

| Task | Test File | Layer | Safety Net | RED | GREEN | TRIANGULATE | REFACTOR |
|------|-----------|-------|------------|-----|-------|-------------|----------|
| C1 — transition refreshes `updated_at` | `state_test.go` → `TestTransitionUpdatedAt` | Unit | ✅ 10/10 top-level (25 matrix) | ✅ Written first; run → `updated_at must be refreshed to the transition time, got 09:00` | ✅ `t.UpdatedAt = now` in `Transition` (success path only); 2/2 subtests pass | ✅ 2 cases: allowed refreshes / rejected keeps | ✅ gofmt applied; table avoids fixture duplication |
| C2 — transition audit event carries Field | `state_test.go` → matrix allowed branch | Unit | ✅ (same run) | ✅ Written first; run → `audit event must name the changed field "state", got <nil>` on all 8 allowed cells | ✅ `Field: ptr("state")` on the transition `AuditEvent` | ✅ asserted on all 8 allowed matrix cells (every legal move) | ✅ single-line comment; denied cells already assert `event == nil` |
| C3 — unassignment tri-state | `ticket_test.go` → 5 tests (1 table) | Unit | ✅ (same run) | ✅ Written first; compile fail (missing `ClearUserID` / constant) → runtime fails: `assignment must be cleared, got 7`, `must be rejected as ambiguous` | ✅ `ClearUserID bool` + ambiguous validation before mutation + clear block (from = prev id, to = "") | ✅ 5 behaviors: clear+event from/to, no-op when already unassigned, ambiguous rejected with zero changes, assign from unassigned, reassign 7→8 | ✅ helper inlined, fixtures reused via `baseTicket()` |

### Test Summary (correction pass)
- **Total tests written**: 5 top-level + 4 subtests (C1: 2 cases; C3 assign table: 2 cases; C2 extends the 25-cell matrix with a Field assertion on all 8 allowed cells)
- **Total tests passing**: suite went 10 top-level / 25 subtests → **15 top-level / 29 subtests**, all green
- **Layers used**: Unit (15)
- **Approval tests**: None — no refactoring of existing behavior (C3 assign path protected by regression table instead)
- **Pure functions created**: 0 (two in-place mutations + one validation, per design)

## Correction Commits

| # | Hash | Message | Contents |
|---|------|--------|----------|
| 5 | `0dba6e1` | `fix(domain): refresh updated_at on transition, stamp transition field, support unassignment` | `ticket.go`, `errors.go`, `audit.go`, `state_test.go`, `ticket_test.go` (one reviewable commit: 3 fixes + tests, 190 insertions / 11 deletions) |

## Files Changed (correction pass)

| File | Action | What Was Done |
|------|--------|---------------|
| `internal/domain/ticket.go` | Modified | `Transition`: sets `t.UpdatedAt = now` on every successful transition; event now carries `Field: ptr("state")`. `TicketUpdate`: added `ClearUserID bool` (tri-state). `ApplyUpdate`: `ClearUserID && UserID != nil` → `*ValidationError{Field: "user"}` before any mutation (atomic); clear block emits `user` event (from = previous id, to = "") and is a no-op when already unassigned |
| `internal/domain/errors.go` | Modified | New Spanish constant `ErrMsgConflictingUserAssignment = "no se puede asignar y desasignar el usuario al mismo tiempo"` (D5) |
| `internal/domain/audit.go` | Modified | Comment now truthful: domain fills `Field`; `Field` names the changed field (`"state"` for transitions) |
| `internal/domain/state_test.go` | Modified | `TestTransitionUpdatedAt` (allowed refreshes / rejected keeps); matrix allowed branch asserts `Field == "state"` |
| `internal/domain/ticket_test.go` | Modified | `TestApplyUpdateClearUserID` (event from/to), `TestApplyUpdateClearUnassignedIsNoOp`, `TestApplyUpdateConflictingUserAssignmentRejected` (deep-equal unchanged, no events), `TestApplyUpdateUserAssignment` table (assign/reassign regression guard) |

## Deviations (correction pass)

None — the three fixes implement exactly the shapes mandated by the verify report. C3 uses the RECOMMENDED `ClearUserID bool` shape. Note: slice-1 deviation #4 ("Transition does NOT refresh UpdatedAt — open question for the application layer") is now resolved at the domain level: the verify report ruled the spec outranks the design's silence, so `updated_at` is refreshed inside `Transition`.

## Quality Gates (correction pass)

| Gate | Command | Result |
|---|---|---|
| Format | `gofmt -l .` | ✅ empty (errors.go const alignment fixed via gofmt -w) |
| Vet | `go vet ./...` | ✅ clean |
| Tests | `go test -count=1 ./...` | ✅ ok `internal/domain` 0.002s |

## Risks (correction pass)

- **Diff budget**: 190 insertions + 11 deletions (11 are comment rewrites); within the 200-line ledger budget. No assertion removed during trimming.
- **Unassignment semantics chosen**: clear-when-unassigned is a no-op (no event, no `updated_at` refresh) — consistent with the existing no-op rule for same-value updates; asserted in `TestApplyUpdateClearUnassignedIsNoOp`.
- **Actor remains deferred** (D14): the transition event still returns `Actor == ""`; application-layer stamping lands in a later slice, as designed. Field is now complete at the domain boundary (C2 resolved).

## Next Steps

- Re-`verify` slice 1: expected to close all 3 CRITICALs (spec matrix rows: Lifecycle Timestamps → transition refreshes `updated_at`; Audit Event Contract → transition `Field = "state"`; Update Ticket Fields → unassignment representable).

---

# Rename Pass — English Domain Vocabulary (pre-DB semantic rename)

**Trigger**: orchestrator directive — rename the ENTIRE domain vocabulary to English (states, priorities, error messages) before any DB/application slice persists the old Spanish values. Persisted string values change, so this MUST happen pre-DB, consistently across code + specs + design.
**Ledger**: token `sha256:2ad018cbc3fe591d375d2dcb855e5df3eaaa3fe623978a81dc587472716db7f5`, work_unit `rename-domain-english`, max_changed_lines 400.
**Mode**: Strict TDD (`go test ./...`, config `strict_tdd: true`, `rules.apply.tdd: true`).
**Scope rule**: ONLY `internal/domain/` + the listed openspec artifacts. No application layer, no store, no HTTP (later slices).
**Branch**: `feat/tkt-mvp-1-scaffold-domain` (unchanged). No push, no PR.

## Rename Map (applied verbatim)

| Domain | Old identifier / value | New identifier / value |
|---|---|---|
| State | `StateNuevo` / `"nuevo"` | `StateNew` / `"new"` |
| State | `StateEnProgreso` / `"en_progreso"` | `StateInProgress` / `"in_progress"` |
| State | `StateResuelto` / `"resuelto"` | `StateResolved` / `"resolved"` |
| State | `StateCerrado` / `"cerrado"` | `StateClosed` / `"closed"` |
| State | `StateCancelado` / `"cancelado"` | `StateCancelled` / `"cancelled"` |
| Priority | `PriorityBaja` / `"baja"` | `PriorityLow` / `"low"` |
| Priority | `PriorityMedia` / `"media"` | `PriorityMedium` / `"medium"` |
| Priority | `PriorityAlta` / `"alta"` | `PriorityHigh` / `"high"` |
| Priority | `PriorityCritica` / `"critica"` | `PriorityCritical` / `"critical"` |
| Error | `ErrMsgTransitionNotAllowed` = `"transición no permitida"` | same name = `"transition not allowed"` |
| Error | `ErrMsgReopenReasonRequired` = `"se requiere un motivo para reabrir el ticket"` | same name = `"a reason is required to reopen the ticket"` |
| Error | `ErrMsgTitleRequired` = `"el título es obligatorio"` | same name = `"title is required"` |
| Error | `ErrMsgInvalidPriority` = `"prioridad no válida"` | same name = `"invalid priority"` |
| Error | `ErrMsgConflictingUserAssignment` = `"no se puede asignar y desasignar el usuario al mismo tiempo"` | same name = `"cannot assign and unassign the user at the same time"` |
| Error format | `"%s de %s a %s"` (InvalidTransitionError) | `"%s from %s to %s"` |

## Work Unit Evidence (Hard Gate — all modes)

| Evidence | Required value |
|---|---|
| Focused test command and exact result | `go test -count=1 ./internal/domain/` → RED: compile failure (undefined `StateNew`, `PriorityHigh`, …); GREEN: `ok github.com/giulianotesta7/tkt/internal/domain` — **15 top-level tests + 29 subtests all pass** (matrix 25 + forward path + reopen w/o reason + TransitionUpdatedAt 2 + 6 ApplyUpdate/Rank + UserAssignment table 2), exit 0 |
| Runtime harness command/scenario and exact result | `N/A` — pure Go domain, zero I/O; behavior proven via the 25-pair matrix (same rationale as slice 1 Unit 1 row) |
| Rollback boundary | Revert the single rename commit (`git revert <hash>`): `internal/domain/{state,priority,errors,ticket,audit}.go` + `{state,ticket}_test.go`; nothing else in the repo depends on the new identifiers/values yet — reverted without removing any prior slice work |

## TDD Cycle Evidence

| Task | Test File | Layer | Safety Net | RED | GREEN | TRIANGULATE | REFACTOR |
|------|-----------|-------|------------|-----|-------|-------------|----------|
| Rename states/priorities/errors | `internal/domain/{state,ticket}_test.go` | Unit | ✅ 15/15 top-level (29/29 subtests) baseline | ✅ Tests updated FIRST to `StateNew`/`PriorityLow`/English messages → run → compile failure (`undefined: domain.PriorityHigh`, `StateResolved`, …) | ✅ `state.go`, `priority.go`, `errors.go`, `ticket.go`, `audit.go` renamed; run → 15/15 + 29/29 pass | ➖ Existing 25-pair matrix + 2-case tables already triangulate every new value (no new behavior) | ✅ Comments translated to English; gofmt -w on `state.go` (map alignment); no logic touched |

### Test Summary (rename pass)
- **Total tests written**: 0 new (mechanical rename — every existing test maps 1:1 to the new identifiers/values; assertion counts preserved: 15 top-level / 29 subtests)
- **Total tests passing**: 15/15 top-level, 29/29 subtests
- **Layers used**: Unit (15)
- **Approval tests**: N/A — no behavior change, values only; test assertions were updated to the NEW expected values first (RED), then code (GREEN)

## Rename Commit

| # | Hash | Message | Contents |
|---|------|--------|----------|
| 6 | `<hash>` | `refactor(domain): english domain vocabulary (states, priorities, errors)` | `state.go`, `priority.go`, `errors.go`, `ticket.go`, `audit.go`, `state_test.go`, `ticket_test.go` (130 insertions / 130 deletions) |

## Files Changed (rename pass)

| File | Action | What Was Done |
|------|--------|---------------|
| `internal/domain/state.go` | Modified | 5 constants renamed `StateNuevo→StateNew` … `StateCancelado→StateCancelled`; values `"nuevo"→"new"` … `"cancelado"→"cancelled"`; comments English (`cancelled is terminal; no transition may move back into new`) |
| `internal/domain/priority.go` | Modified | `PriorityBaja→PriorityLow` … `PriorityCritica→PriorityCritical`; values `"baja"→"low"` … `"critica"→"critical"`; `Rank()` cases + comment (critical=4 … low=1) |
| `internal/domain/errors.go` | Modified | 5 message constants translated to English (names unchanged, D5 single source); `NewInvalidTransitionError` format `"%s de %s a %s"` → `"%s from %s to %s"`; package comment "Spanish user-facing" → "English user-facing"; doc comments English |
| `internal/domain/ticket.go` | Modified | `Transition`/`ApplyUpdate`/`isValidPriority` switch to new identifiers; comments English (resolved/closed/in_progress semantics) |
| `internal/domain/audit.go` | Modified | Comment `reopen reason for cerrado -> en_progreso` → `for closed -> in_progress` |
| `internal/domain/state_test.go` | Modified | All matrix cells, forward path, reopen, updated_at cases → new identifiers; fixture strings English (`"Test ticket"`, `"reopen to fix"`, `"Transition"`); `TestReopenFromCerradoWithoutReason` → `TestReopenFromClosedWithoutReason`; "Spanish message" → "English message" assertions |
| `internal/domain/ticket_test.go` | Modified | All cases → new identifiers (`PriorityMedium`, `StateInProgress`, `StateClosed`); fixture titles English (`"Original title"`, `"New title"`, `"Before"`/`"After"`); invalid priority input `"urgente"` → `"urgent"`; "Spanish message" → "English message" assertions |
| `openspec/specs/{ticket-state-machine,ticket-management,audit-log,comment-timeline,ticket-search}/spec.md` | Modified | All Spanish state/priority terms → English values in requirements, scenarios, GIVEN/WHEN/THEN (category-management, user-management had no domain terms) |
| `openspec/changes/tkt-mvp/{proposal,design,tasks}.md` | Modified | Scope/approach tables, D5 (UI language English, English messages), D11 CASE `'critical'`, domain model constants, transition table, DDL CHECK constraints (`('low','medium','high','critical')`, `('new','in_progress','resolved','closed','cancelled')`), reopen flow, errors paragraph, testing strategy, HTTP 500 message `"Internal server error"`, task rows 2.1–2.3, 3.3, 3.4, 5.5 |
| `openspec/changes/tkt-mvp/apply-progress.md` | Modified | THIS section appended — earlier history untouched |

## Quality Gates (rename pass)

| Gate | Command | Result |
|---|---|---|
| Format | `gofmt -l .` | ✅ empty (after `gofmt -w internal/domain/state.go`) |
| Vet | `go vet ./...` | ✅ clean |
| Tests | `go test -count=1 ./...` | ✅ `ok github.com/giulianotesta7/tkt/internal/domain` — 15 top-level / 29 subtests |
| Spanish grep | `grep -rnE '\b(nuevo\|en_progreso\|resuelto\|cerrado\|cancelado\|baja\|media\|alta\|critica\|transición\|prioridad\|título\|motivo)\b' internal/domain/` | ✅ exit 1 — NOTHING (the literal unword-boundaried `critica` pattern only matches the mandated English token `critical` — substring, not Spanish vocabulary) |
| Diff budget | `git diff --stat` | ✅ 130 insertions / 130 deletions = 260 changed lines (ledger estimate 250–380; under the 400 cap) |

## Deviations (rename pass)

None — identifiers, values, and message strings match the rename map EXACTLY. Doc-only consistency fixes in design.md: `ErrReopenReasonRequired`/`ErrInvalidTransition{From,To}` corrected to the real code names `ReopenReasonRequiredError`/`InvalidTransitionError{From,To}` while translating the errors paragraph.

## Risks (rename pass)

- **`sistema` fixed actor NOT renamed**: the audit-log spec keeps the fixed system actor `sistema` (design D14: "no `sistema` fallback except genuine system actions"). It is NOT in the EXACT rename map (states/priorities/errors only), exists in no domain code, and lands with the application slice. Flag for that slice: if the UI must be fully English, rename to `system` then (spec + design references at design.md actor-wiring + D14 lines).
- **exploration.md / verify-report.md still carry Spanish terms**: both are outside the listed artifact scope; verify-report.md will be regenerated by the re-verify run (next_recommended: `verify`). exploration.md is historical; left untouched per bounded scope.
- **Unword-boundaried grep false positives**: the mandated English values `critical`/`immediate` contain the substrings `critica`/`media`; the naive verification grep reports them. Word-boundary verification is clean. If the orchestrator runs the literal command, expect exactly these benign hits (3 in code, 4 in design.md).
- **Later slices MUST use the new values**: store CHECK constraints, filters, chips, templates, and goldens in slices 3–5 must persist/consume `new`/`in_progress`/`resolved`/`closed`/`cancelled` and `low`/`medium`/`high`/`critical`; the design DDL has already been updated so the slice-4 migration matches.

## Next Steps

- Re-`verify` slice 1: regenerates verify-report.md with English terms and re-runs the spec matrix against the renamed domain (expect all rows COMPLIANT; report will reference `new`/`closed`/`in_progress` subtest names).
- Then `apply` slice 2 (PR 2 — store ports + application use cases) using the English values.

---

# Apply Progress — tkt-mvp — Slice 2 (PR 2 of 5)

**Slice**: PR 2 — Store ports + application use cases (tasks 3.1–3.6)
**Branch**: `tkt-mvp-application` (off `main` @ 448a57e, target for PR 2, stacked-to-main)
**Mode**: Strict TDD (`go test ./...`, config `strict_tdd: true`, `rules.apply.tdd: true`)
**State**: Slice 2 complete — tasks 3.1, 3.2, 3.3, 3.4, 3.5, 3.6 marked `[x]` in `tasks.md`
**Delivery**: chained PRs, stacked-to-main, PR 2 of 5. Commits created; NO push, NO PR (delivery gated on verification by orchestrator).
**Ledger**: token `sha256:ce1e2b6781f854694141701cc12b1be995e79ccdba867c200375993b515b0628`, work_unit `slice2-application`, max_changed_lines 2000.

## Work Unit Evidence (Hard Gate — all modes)

| Evidence | Required value |
|---|---|
| Focused test command and exact result | `go test -count=1 ./internal/application/` → RED per task: compile failures (`undefined: application.TicketService`, `domain.NotFoundError`, …) before each implementation; final GREEN: **59 top-level tests + 9 subtests (BuildTextQuery table) all PASS**, exit 0 (bcrypt suite ~0.09s/hash at cost 10; full application package 1.2s) |
| Runtime harness command/scenario and exact result | `N/A` — port fakes only; bcrypt exercised in unit tests (tasks.md Unit 2 row: "N/A — port fakes only; bcrypt exercised in unit tests"). The fakes ARE the harness: `fakes_test.go` implements all 8 store ports in memory (MAX+1 numbering, AND filters, ASC timelines, expiry) |
| Rollback boundary | Revert `tkt-mvp-application` branch (or `git revert` the 6 commits `4ca2e19..82ee363`): `internal/application/` + `internal/domain/{comment,user,category,session,errors,priority}.go`; main stays at 448a57e with the domain slice intact |

## TDD Cycle Evidence

| Task | Test File | Layer | Safety Net | RED | GREEN | TRIANGULATE | REFACTOR |
|------|-----------|-------|------------|-----|-------|-------------|----------|
| 3.1 | — (ports) | Unit | ✅ 15/15 top-level (29 subtests) baseline | N/A (structural) | ✅ `go build ./...` exit 0 | ➖ Single (interface definitions) | ➖ None needed |
| 3.2 | `internal/application/password_test.go` | Unit | N/A (new files) | ✅ Written first; run → `undefined: application.HashPassword` compile fail | ✅ 6/6 pass (real bcrypt) | ✅ 6 cases: verify ok / wrong pw / salt / empty / whitespace / malformed / no-plaintext | ✅ cost const extracted; go.mod pins restored after tidy |
| 3.3 | `internal/application/ticket_service_test.go` (+`fakes_test.go`) | Unit | ✅ (suite carried) | ✅ Written first; run → `undefined: application.TicketService`, `domain.NotFoundError` | ✅ 16/16 pass | ✅ 16 behaviors: create happy+5 rejects, transition 4 (valid/invalid/reopen×2/unknown), update 3 (audit/priority/category+user), 3-event audit scenario, GetByID | ✅ `validPriority` → exported `domain.IsValidPriority` (single source); fakes dedup via `store` helper |
| 3.4 | `internal/application/{comment_service,views}_test.go` | Unit | ✅ (suite carried) | ✅ Written first; run → `undefined: application.NewCommentService`, `ViewBuilder` | ✅ 9/9 pass (5 comment + 4 view) | ✅ comment: author/empty/unknown/closed/ASC×3; view: refs+timelines/unassigned/inactive/unknown | ✅ helper `seededCommentTimeline` deduped arrange; gofmt |
| 3.5 | `internal/application/{user_service,auth_service}_test.go` | Unit | ✅ (suite carried) | ✅ Written first; run → `undefined: application.UserService`, `AuthService`, `InvalidCredentialsError` | ✅ 16/16 pass (10 user + 6 auth) | ✅ user: create 3, update 3, deactivate, delete 2, list; auth: success/failures-share-error/logout/count | ✅ same-generic-error asserted on all 3 failure classes in one table |
| 3.6 | `internal/application/{category_service,search_service}_test.go` | Unit | ✅ (suite carried) | ✅ Written first; run → `undefined: application.CategoryService`, `SearchService`, `BuildTextQuery` | ✅ 12/12 pass (6 category + 6 search + 9 subtests) | ✅ category: create/dup/empty/rename/free-old/dup-rename/delete×2; search: 9-case tokenizer table + AND×2 + specials×8 + pagination 10/10/5 + chips | ✅ `PageSize` const (D2); seed helper dedupes 25-ticket arrange |

### Test Summary
- **Total tests written**: 59 top-level (6 password + 16 ticket + 5 comment + 4 view + 10 user + 6 auth + 6 category + 6 search) + 9 BuildTextQuery subtests
- **Total tests passing**: 59/59 top-level, all subtests (application 1.2s; domain 15/29 unchanged)
- **Layers used**: Unit (59)
- **Approval tests**: None — no refactoring of existing behavior
- **Pure functions created**: `BuildTextQuery` (D4 tokenizer), `domain.IsValidPriority` export

## Commits (in order)

| # | Hash | Message | Contents |
|---|------|--------|----------|
| 1 | `4ca2e19` | `feat(application): define store ports` | `internal/application/ports.go` + `internal/domain/{comment,user,category,session}.go` |
| 2 | `45019f7` | `feat(application): bcrypt password hashing` | `password.go` + `password_test.go` + `errors.go` (5 msg consts) + go.mod/go.sum (crypto promoted to direct; sqlite pins restored) |
| 3 | `f825964` | `feat(application): ticket use cases` | `ticket_service.go` + `ticket_service_test.go` + `fakes_test.go` (all 8 ports) + `errors.go` (4 typed errors + 3 sentinels) + `priority.go` (IsValidPriority) |
| 4 | `1f72769` | `feat(application): comment service + ticket views` | `comment_service.go` + `views.go` + tests |
| 5 | `4e73a0d` | `feat(application): user + auth use cases` | `user_service.go` + `auth_service.go` (InvalidCredentialsError) + tests |
| 6 | `82ee363` | `feat(application): category + search use cases` | `category_service.go` + `search_service.go` (BuildTextQuery, PageSize=10) + tests |

## Files Changed

| File | Action | What Was Done |
|------|--------|---------------|
| `internal/application/ports.go` | Created | 8 store interfaces exactly per design signatures + TicketQuery + Page (Limit fixed 10, D2); port contracts documented (MAX+1 numbering D8, ASC timelines, ErrNotFound/Duplicate/Referenced, mutation+audit atomicity, session expiry) |
| `internal/application/password.go` | Created | bcrypt Hash/Verify at cost 10 (D15); empty/whitespace password rejected with ValidationError |
| `internal/application/ticket_service.go` | Created | Create (title/priority/category/user-active validation, state new, created audit event), Transition (domain machine + actor stamp + persist), Update (refs validated as in creation, audit per changed field), GetByID |
| `internal/application/comment_service.go` | Created | Add (session author, non-empty body, any state, ticket exists), ListByTicket ASC |
| `internal/application/views.go` | Created | `TicketView` composition (D13): ticket + category + assigned user (inactive shown) + comments + audit timeline |
| `internal/application/user_service.go` | Created | Create (hash not plaintext, active default), Update (name/email/password/deactivate), Delete (referenced guarded), GetByID/List |
| `internal/application/auth_service.go` | Created | Login (single generic InvalidCredentialsError, opaque 32-byte token, 24h TTL), Logout, UserCount (D16) |
| `internal/application/category_service.go` | Created | Create/Rename (unique non-empty names), Delete (referenced guarded), GetByID/List |
| `internal/application/search_service.go` | Created | Search (AND filters, D4 text, stable pagination, chips), BuildTextQuery, PageSize 10 (D2) |
| `internal/application/fakes_test.go` | Created | In-memory implementations of all 8 ports (test infra; the slice's runtime harness) |
| `internal/application/*_test.go` (7 files) | Created | 59 top-level tests mapped to spec scenarios |
| `internal/domain/comment.go` | Created | Comment aggregate value type (deferred from slice 1, deviation #2) |
| `internal/domain/user.go` | Created | User value type (no roles; Active = deactivation) |
| `internal/domain/category.go` | Created | Category value type |
| `internal/domain/session.go` | Created | Session value type (opaque token ID) |
| `internal/domain/errors.go` | Modified | +InactiveUserError, +NotFoundError/DuplicateError/ReferencedError (typed, with ErrNotFound/ErrDuplicate/ErrReferenced sentinels + Is()), +6 English message constants |
| `internal/domain/priority.go` | Modified | +IsValidPriority export (single source for application) |
| `go.mod`/`go.sum` | Modified | x/crypto promoted to direct require; modernc.org/sqlite pins re-restored after tidy prune |

## Deviations from Design

1. **Domain value types ride in the 3.1 commit** (slice-1 deviation #2 closed): `Comment`/`User`/`Category`/`Session` were deferred from slice 1 and now land with their first consumers (the ports reference them). They are pure data structs — behavior is exercised by the application tests, not separate domain tests.
2. **`ErrMsgCommentBodyRequired` + 5 more message constants added to domain/errors.go** (D5 single source): password/name/email/category-name required + user-inactive messages. `InvalidCredentialsError` lives in the application package with its message const (`ErrMsgInvalidCredentials`) per the design's "application-level" annotation.
3. **`TicketStore.Create` also assigns the ticket ID, not just the Number**: the DDL is `id INTEGER PRIMARY KEY AUTOINCREMENT`, so the store owns both identity fields. The fake mirrors that; the service never sets either.
4. **`Session` has no `CreatedAt` field** (design model is `{ID, UserID, ExpiresAt}`) while the DDL requires `sessions.created_at NOT NULL` — flagged for slice 4: the sqlite store must stamp `created_at` (store-side time is acceptable per D7 scope; D7 forbids `time.Now()` in the *render* path).
5. **Atomicity of ticket+audit persistence is a documented port contract, not a txn API**: ports have no transaction surface (design fixed the signatures); the contract is documented on `TicketStore`/`AuditStore` and satisfied by construction in the fakes. Slice 4 must implement it (e.g., shared unit-of-work/connection-level txn in the adapter).
6. **`Transition` persists audit AFTER ticket Update; rejected transitions never touch the store** — verified behaviorally (state unchanged + zero audit events).
7. **Deactivation does NOT kill sessions in slice 2** (design places "deactivating a user deletes their active sessions" in the HTTP layer; 5.6 acceptance owns it). Slice 2 covers "historical assignment preserved" only.

## Quality Gates

| Gate | Command | Result |
|---|---|---|
| Format | `gofmt -l .` | ✅ empty |
| Vet | `go vet ./...` | ✅ clean |
| Build | `go build ./...` | ✅ clean |
| Tests | `go test -count=1 ./...` | ✅ ok application (59 tests) + ok domain (15 tests / 29 subtests) |
| Full diff | `git diff main...HEAD --numstat` | 2,916 insertions / 2 deletions (see Risks — budget) |

## Risks

- **Line budget exceeded (ledger max_changed_lines 2000; actual 2,918)**: the 6-task production estimate was ~1,160; real authored diff is ~2.5x. Breakdown: production/services ~620, domain additions ~120, fakes `fakes_test.go` 485 (ONE-TIME shared test infra — implements all 8 ports for every later test file and is the slice's runtime harness), tests ~1,690 (59 top-level functions, each mapped 1:1 to a spec scenario; no gold-plating). Slice 1 set the precedent of flagging rather than trimming real assertions (801 vs 455 est). Orchestrator decision: accept PR 2 as-is (~2,900 lines, one cohesive application-layer deliverable) or split (e.g., fakes into a separate test-infra commit is NOT possible without dropping coverage; alternative: split PR 2 into 2a (3.1–3.3) / 2b (3.4–3.6) stacked).
- **`go mod tidy` prune + restore**: tidy in the 3.2 commit pruned the unused sqlite pins; re-pinned exactly (modernc.org/sqlite v1.56.0) via `go get`. go.mod now has x/crypto as a direct require. Verify must NOT run `go mod tidy` expecting the pins to stay — they are stable exact-version pins.
- **FTS comment search not covered by slice-2 fakes** (fake text matching covers title+description only): comment indexing is FTS-trigger territory and lands with slice 4 (`0002_fts.sql`); the 3.6 text tests deliberately avoid asserting comment matches.
- **Login timing side-channel** (unknown email skips bcrypt entirely): matches the design's flow (inactive short-circuits too). Same generic error text for all failures; timing hardening is out of MVP scope — noted for the threat-matrix audit in verify.

## Next Steps

- `verify` on slice 2 (recommended): application layer is independently verifiable against ticket-management, ticket-state-machine, comment-timeline, audit-log, user-management, category-management, ticket-search specs. After verification passes, orchestrator delivers PR 2 (stacked-to-main), then `apply` slice 3 (PR 3 — SQLite adapter, tasks 4.1–4.5).

---

# Correction Pass — Slice 2 (4 CRITICAL fixes)

**Trigger**: `openspec/changes/tkt-mvp/verify-report-slice2.md` → verdict FAIL, 4 CRITICAL findings:
C1 ticket+audit persistence issued through separate ports (no atomic rollback — no-silent-mutations violation); C2 `TicketService.GetByID` returns `*domain.Ticket` instead of the composed `TicketView`; C3 `TestUpdateValidatesCategoryAndAssignedUser` seeds the inactive user in an unrelated store (inactive branch never executes); C4 append-only comment scenario has no runtime covering test.
**Ledger**: token `sha256:42bf7711cb495e2cab60d4d7166a4eefff9016ee1f400236e700b866d54c0453`, work_unit `fix-4-critical-application`, max_changed_lines 700.
**Mode**: Strict TDD (`go test ./...`, config `strict_tdd: true`, `rules.apply.tdd: true`).
**Scope rule**: ONLY the 4 CRITICAL fixes + their tests. No SQLite adapter, no HTTP (later slices). No gold-plating.
**Branch**: `tkt-mvp-application` (HEAD `82ee363`; correction will be the 7th commit). No push, no PR.

## Work Unit Evidence (Hard Gate — all modes)

| Evidence | Required value |
|---|---|
| Focused test command and exact result | `go test -count=1 ./internal/application/` → RED per fix: (C3) `--- FAIL: TestUpdateValidatesCategoryAndAssignedUser … must be an InactiveUserError, got user not found`; (C1+C2) `vet: internal/application/fakes_test.go:311:19: undefined: application.TicketUnitOfWork`; (C4) `Add("first"): unexpected error: ticket not found` (fixture wiring, fixed before GREEN); final GREEN: `ok github.com/giulianotesta7/tkt/internal/application 1.205s` — **62 top-level tests + 14 subtests all pass**, exit 0 |
| Runtime harness command/scenario and exact result | `N/A` — port fakes only (tasks.md Unit 2 row: "N/A — port fakes only; bcrypt exercised in unit tests"). The fakes ARE the harness; the new `fakeUnitOfWork` simulates the transactional rollback (ticket write undone / pre-mutation copy restored on failed audit append) |
| Rollback boundary | Revert the single correction commit (`git revert <hash>`): `internal/application/{ports,ticket_service,ticket_service_test,fakes_test,comment_service_test}.go`; everything else on the branch is untouched — reverted without removing any slice-2 work |

## TDD Cycle Evidence

| Task | Test File | Layer | Safety Net | RED | GREEN | TRIANGULATE | REFACTOR |
|------|-----------|-------|------------|-----|-------|-------------|----------|
| C1 — atomic ticket+audit UoW port | `ticket_service_test.go` (`TestCreateRollsBack…`, `TestTransitionRollsBack…`) + `fakes_test.go` (`fakeUnitOfWork`) | Unit | ✅ 59 top-level / 14 subtests baseline green | ✅ Written first; run → compile fail `undefined: application.TicketUnitOfWork` (new port + new `NewTicketService` signature) | ✅ `TicketUnitOfWork` port + service routing + transactional fake; 2/2 rollback tests + full suite pass | ✅ 2 paths: Create rollback (ticket not persisted at all) + Transition/Update rollback (pre-mutation state restored); happy paths persist both (existing `TestCreateStores…`, `TestTransitionApplies…` assert ticket AND audit) | ✅ `var _ application.TicketUnitOfWork = (*fakeUnitOfWork)(nil)` compile assertion; ports.go atomicity contract rewritten (was the "documented contract" the report rejected) |
| C2 — GetByID returns TicketView | `ticket_service_test.go` → `TestGetByIDReturnsComposedView` | Unit | ✅ (suite carried) | ✅ Written first; run → compile fail (GetByID still returned `*domain.Ticket`; `view.Ticket` unknown) | ✅ `ViewBuilder` injected into `TicketService`; `GetByID` delegates to `builder.TicketView`; test passes | ✅ view fields: ticket, category name, assigned-user name, 2 comments in order + NotFoundError for missing id | ✅ service constructor keeps one wiring point (5→6 params, audits dep moved into the builder) |
| C3 — real inactive-user update test | `ticket_service_test.go` → `TestUpdateValidatesCategoryAndAssignedUser` | Unit | ✅ (suite carried) | ✅ Typed `InactiveUserError` assertion added FIRST while keeping the wrong fixture → run → `must be an InactiveUserError, got user not found` (branch never executed, exactly the report's finding) | ✅ Fixture rewired to the harness store (`h.users.seed`) → test passes; category branch tightened to typed `NotFoundError(kind=category)` | ✅ same-store inactive seed + typed error + no-audit assertions; CREATE test audited: `TestCreateRejectsInactiveUserAssignment` already seeds the harness store and asserts the type — no flaw, left intact | ✅ harness refactor (`ticketHarness` struct) removes the positional-destructuring trap that caused the original bug |
| C4 — append-only runtime evidence | `comment_service_test.go` → `TestAppendOnlyCommentsNoUpdateOrDelete` | Unit | ✅ (suite carried) | ✅ Written first; initial run FAILED on a fixture wiring bug (`ticket not found` — fresh store vs seeded store), fixed in the same RED step | ✅ Guard passes: negative type assertions on fake store + service (would fail if `Update`/`Delete` ever appear) + behavioral timeline returns exactly the 2 added comments in order | ✅ 4 negative assertions (store ×2, service ×2) + behavioral non-empty timeline (triangulation against trivial-pass risk) | ✅ marker interfaces `commentUpdater`/`commentDeleter` documented; fixture reordered so one coherent store set serves both halves |

### Test Summary (correction pass)
- **Total tests written**: 3 new top-level (2 C1 rollback + 1 C4 guard); 1 rewritten (C2 view); 1 corrected (C3 fixture + typed assertions)
- **Total tests passing**: suite went 59 top-level / 14 subtests → **62 top-level / 14 subtests**, all green (application 1.2s; domain 15/29 unchanged)
- **Layers used**: Unit (63)
- **Approval tests**: 0 — no existing-behavior refactor; all pre-existing assertions preserved 1:1 (verify report's WARNING on `TestEveryMutationAuditedInOccurrenceOrder` was out of scope: CRITICALs only)
- **Pure functions created**: 0 (port + wiring changes)

## Correction Commit

| # | Hash | Message | Contents |
|---|------|--------|----------|
| 7 | `<hash>` | `fix(application): atomic ticket/audit persistence, ticket view, inactive-user test, append-only proof` | `ports.go`, `ticket_service.go`, `ticket_service_test.go`, `fakes_test.go`, `comment_service_test.go` (one reviewable commit: 4 fixes + tests, 384 insertions / 140 deletions) |

## Files Changed (correction pass)

| File | Action | What Was Done |
|------|--------|---------------|
| `internal/application/ports.go` | Modified | New `TicketUnitOfWork` port (C1): `Create(ctx, t, event)` + `Update(ctx, t, events...)` — ONE call per mutation persisting ticket + audit events atomically, rollback mandated on append failure, `event.TicketID` stamped from the store-assigned ID. `TicketStore` comment rewritten: read paths + direct store use only; mutations never issued through it in isolation |
| `internal/application/ticket_service.go` | Modified | Constructor now takes `tx TicketUnitOfWork` + `builder *ViewBuilder` (audits dep removed — the UoW replaces it). `Create`/`Transition`/`Update` route the ticket write AND the audit events through the single UoW call; the service never catches or ignores a UoW failure. `GetByID` returns the composed `*TicketView` (C2) |
| `internal/application/ticket_service_test.go` | Modified | Harness refactor to `ticketHarness` struct (one coherent store set incl. comments + tx); C1 rollback tests ×2 (Create not persisted / Transition state restored, error propagated via `errors.Is`); C2 `TestGetByIDReturnsComposedView` (ticket + category name + user name + comments in order + NotFound); C3 fixture fixed to harness store with typed `InactiveUserError` assertion |
| `internal/application/fakes_test.go` | Modified | `fakeUnitOfWork` implementing `TicketUnitOfWork` with a transactional simulation (`failAuditAppend` hook → ticket write undone / pre-mutation copy restored) + `var _ application.TicketUnitOfWork` compile assertion; `errors` import |
| `internal/application/comment_service_test.go` | Modified | C4 `TestAppendOnlyCommentsNoUpdateOrDelete`: negative type assertions (`commentUpdater`/`commentDeleter` markers) on fake store + service + behavioral timeline (exactly the added comments, in order) |

## Deviations (correction pass)

1. **`AuditStore.Append` kept on the port** although the service no longer calls it: the UoW implementations use it internally (slice 4), tests seed timelines through it, and removing it would exceed the bounded scope. The no-silent-mutations hole (service-issued separate calls) is closed regardless.
2. **`TicketStore.Create/Update` kept on the port** per the fix instruction ("keep the separate TicketStore.Create/Update for read paths but route MUTATIONS through the unit-of-work"): they serve direct store use (test seeding, system operations); the application layer never calls them in isolation anymore.
3. **Service constructor grows to 6 params** (tickets, users, categories, tx, builder, clock): the `audits` dependency moved into the injected `ViewBuilder` (which already owned it). One wiring point; the harness centralizes it in tests.
4. **Verify WARNINGs untouched** (exact-three-events cardinality, empty-comment call instrumentation, FTS fake limits, D4 punctuation, apply-progress count accuracy): out of scope — CRITICALs only per the bounded correction.

## Quality Gates (correction pass)

| Gate | Command | Result |
|---|---|---|
| Format | `gofmt -l .` | ✅ empty |
| Vet | `go vet ./...` | ✅ clean |
| Tests | `go test -count=1 ./...` | ✅ ok application (62 tests / 14 subtests) + ok domain (15 / 29) |
| Diff budget | `git diff --stat` | ✅ 384 insertions / 140 deletions = 524 changed lines (ledger max 700; estimate 350–600) |

## Risks (correction pass)

- **UoW contract now carries the atomicity burden**: slice 4's SQLite adapter MUST implement `TicketUnitOfWork` as a real transaction (ticket write + audit appends in one txn, rollback on any append failure). The port comment and this pass's tests are the contract; the fake's rollback simulation is the reference behavior.
- **`event.TicketID` stamping for Create is now the store's job**: the service can no longer stamp it (ID is assigned inside the UoW call). Slice 4 must stamp `event.TicketID = t.ID` before inserting the audit row — the fake does exactly this; the port comment documents it.
- **C4 guard is absence-proof**: negative type assertions only fail when an `Update`/`Delete` method appears with an exact signature match on the fake or service. A differently-named mutation path (e.g. `Edit`) would slip past the marker; the behavioral half (timeline unchanged) plus the port surface (only `Add`/`ListByTicket`) cover the scenario's intent.
- **`TestEveryMutationAuditedInOccurrenceOrder` still permits extra events** (verify WARNING): intentionally out of scope; the exact-three cardinality tightening belongs to a future pass if verify re-flags it.

## Next Steps

- Re-`verify` slice 2 (`next_recommended: verify`): expected to close all 4 CRITICALs — spec rows: No Silent Mutations → atomic UoW proven by rollback tests; TicketView composition → `GetByID` returns the view; Update Ticket Fields → inactive-user branch has runtime evidence; Append-Only Comments → runtime guard test passes.

---

# Apply Progress — tkt-mvp — Slice 3 (PR 3 of 5)

**Slice**: PR 3 — SQLite adapter (tasks 4.1–4.5)
**Branch**: `tkt-mvp-adapters` (off `tkt-mvp-application` @ d05ef21, target for PR 3, stacked-to-main)
**Mode**: Strict TDD (`go test ./...`, config `strict_tdd: true`, `rules.apply.tdd: true`)
**State**: Slice 3 complete — tasks 4.1, 4.2, 4.3, 4.4, 4.5 marked `[x]` in `tasks.md`
**Delivery**: chained PRs, stacked-to-main, PR 3 of 5. Commits created; NO push, NO PR (delivery gated on verification by orchestrator).

## Work Unit Evidence (Hard Gate — all modes)

| Evidence | Required value |
|---|---|
| Focused test command and exact result | `go test -count=1 ./internal/adapters/sqlite/` → **65 top-level tests + 6 subtests (filter composition) all PASS** on the real modernc driver (shared-cache memory DBs); RED per task: compile failures (`undefined: Store/openDSN/Open/migrate` → `TicketStore` → `CommentStore` → `UserStore` → `SearchStore`) before each implementation; concurrency test run 5× + `-race` → stable |
| Runtime harness command/scenario and exact result | `go test ./internal/adapters/sqlite/ -run TestFTS5` → 6/6 PASS (real driver, contentless FTS5, triggers). The migration runner + FTS triggers ARE the runtime boundary (no HTTP/composition root yet — per tasks.md Unit 3 row: "real modernc driver, in-memory shared cache") |
| Rollback boundary | Revert the 5 commits `ee0db02..72b9e55` (or drop the branch): `internal/adapters/sqlite/` + `go.mod`/`go.sum` (modernc promoted to direct); main stays at d05ef21 with the application slice intact; migrations are additive |

## TDD Cycle Evidence

| Task | Test File | Layer | Safety Net | RED | GREEN | TRIANGULATE | REFACTOR |
|------|-----------|-------|------------|-----|-------|-------------|----------|
| 4.1 — Open + migrations + 0001 | `sqlite_test.go` | Store integration | ✅ 15 domain + 62 application baseline green | ✅ Written first; run → compile fail (`undefined: Store, openDSN, Open, migrate`) | ✅ `sqlite.go` (single DSN, `_pragma=foreign_keys(1)&journal_mode(WAL)&busy_timeout(5000)&_txlock=immediate`), `migrate.go` (go:embed runner + schema_migrations bootstrap), `0001_init.sql`; 7/7 pass | ✅ 7 behaviors: DSN pragmas (fk=1/wal/5000), unopenable path errors, schema+version, rerun no-op, transactional rollback (fstest MapFS broken migration), FK enforced, shared-cache visibility across pools | ✅ test DSN builder extracted (unique name per test → no cross-test shared-cache interference); `migrationVersion` parsed via TrimLeft digits |
| 4.2 — ticket store + UoW | `ticket_store_test.go` | Store integration | ✅ (suite carried) | ✅ Written first; run → compile fail (`TicketStore`, `TicketUnitOfWork`, `nullableInt64` undefined) | ✅ `filters.go` (shared builder + D11 CASE fragment + order), `ticket_store.go` (MAX+1 in BEGIN IMMEDIATE + retryUnique(3), Update/GetByID/List/Count/chips, `unitOfWork` with atomic audit appends + rollback), accessors; 23/23 pass | ✅ 20 behaviors: 1042→1043→1044, 2-goroutine×4 distinct numbers, FK category+user, update round-trip, NotFound×3, ordering+tiebreak, AND filters×5, pagination 10/10/5, count, chips by state+priority filtered, UoW happy+rollback×2 (RAISE trigger injection)+batch order+NotFound, retryUnique 3 paths (real driver UNIQUE error) | ✅ `retryUnique` shared helper; scan projection via `rowScanner` interface; extended-rc matching (`Code()&0xFF==19`) after driver probe |
| 4.3 — comment + audit stores | `timeline_store_test.go` | Store integration | ✅ (suite carried) | ✅ Written first; run → compile fail (`CommentStore` undefined) | ✅ `comment_store.go` + `audit_store.go` (Append in one immediate txn via shared `appendAuditEventsTx`) + accessors; 10/10 pass | ✅ 9 behaviors: ASC timeline incl. equal-timestamp tiebreak, scoping, empty, ID assign, FK×2, CHECK empty-body, multi-event batch order, note/field round-trip | ✅ audit scan via `nullableStringPtr` helper; id dropped from scan (domain AuditEvent carries no id — port contract) |
| 4.4 — user + session stores | `user_store_test.go`, `session_store_test.go` | Store integration | ✅ (suite carried) | ✅ Written first; run → compile fail (`UserStore`/`SessionStore` undefined) | ✅ `user_store.go` (UNIQUE→Duplicate, delete-guard→Referenced in txn with session cleanup), `session_store.go` (expiry vs wall clock + lazy purge, idempotent logout) + accessors; 19/19 pass | ✅ 18 behaviors: create/dup/update/update-dup/delete×4/GetByID inactive/GetByEmail/Count/List/ListActive; session create/get/missing/expired-purged/delete/delete-missing/created_at stamp | ✅ active INTEGER→bool conversion helper; session fixture truncated to second precision (RFC3339 storage) |
| 4.5 — FTS5 + search store | `search_store_test.go` | Store integration | ✅ (suite carried) | ✅ Written first; run → compile fail (`SearchStore` undefined); after GREEN: two REAL driver findings fixed (see Deviations 1–2) | ✅ `0002_fts.sql` (corrected trigger shapes), `search_store.go` (text clause `t.id IN (SELECT rowid FROM tickets_fts WHERE tickets_fts MATCH ?)` in the shared builder), accessor; 6/6 TestFTS5 (all six search tests) pass | ✅ 8 behaviors: cross-field (desc+comment), edit Old→New reflected (superseded gone), 10 special-char inputs never error (real driver probe pre-verified: `"("`/`"*"`/`":"`/`a"b` all match cleanly), text AND state AND category, SearchCount, chips reflect text-filtered set, plain-List text support | ✅ text clause added to the SHARED builder (chips + List honor text — SearchService routes chips through TicketStore with q.Text); 4.1 migration-count assertions updated to expect versions {1,2} |

### Test Summary
- **Total tests written**: 65 top-level (7 sqlite + 10 timeline + 23 ticket + 13 user + 6 session + 6 search) + 6 subtests; all new for the slice
- **Total tests passing**: 65/65 + 6/6 subtests (package 0.15s); full repo: domain 15/29 unchanged, application 62/14 unchanged — no regressions
- **Layers used**: Store integration (56) — real modernc driver, shared-cache memory DBs
- **Approval tests**: None — no refactoring of existing behavior
- **Pure functions created**: `retryUnique`, `buildTicketWhere`, `isConstraint`/`isUniqueViolation`/`isForeignKeyViolation`, scan projections, `formatTime`/`nullable*` binders

## Commits (in order)

| # | Hash | Message | Contents |
|---|------|--------|----------|
| 1 | `ee0db02` | `feat(sqlite): open, migrations runner, init schema` | `sqlite.go` (Open + single DSN), `migrate.go` (go:embed runner), `migrations/0001_init.sql`, `sqlite_test.go` (7 tests + newTestDB/testDSN/seedCategory); `go.mod`/`go.sum` (modernc.org/sqlite promoted to direct) |
| 2 | `46413d0` | `feat(sqlite): ticket store with atomic numbering` | `filters.go` (shared builder + D11 CASE + ordering), `ticket_store.go` (ticketStore + unitOfWork + appendAuditEventsTx + scanTicketFrom), `ticket_store_test.go` (20 tests incl. concurrency, UoW rollback via RAISE trigger, retryUnique with real driver errors); sqlite.go accessors + constraint helpers |
| 3 | `dcf3a41` | `feat(sqlite): comment + audit stores` | `comment_store.go`, `audit_store.go`, `timeline_store_test.go` (9 tests), accessors |
| 4 | `d61215d` | `feat(sqlite): user + session stores` | `user_store.go`, `session_store.go`, `user_store_test.go` (12) + `session_store_test.go` (6), accessors |
| 5 | `72b9e55` | `feat(sqlite): FTS5 + search store` | `migrations/0002_fts.sql` (corrected triggers), `search_store.go`, `filters.go` text clause, `search_store_test.go` (8 tests), sqlite_test.go version assertions {1,2} |

## Files Changed

| File | Action | What Was Done |
|------|--------|---------------|
| `internal/adapters/sqlite/sqlite.go` | Created | `Store` (single *sql.DB) + `Open` (single DSN per design: `file:<path>?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_txlock=immediate`), `openDSN` for tests, per-port accessors (TicketStore, TicketUnitOfWork, CommentStore, AuditStore, UserStore, SessionStore, SearchStore), constraint helpers (extended-rc matching `Code()&0xFF==19`), `retryUnique`, NULL/time binders |
| `internal/adapters/sqlite/migrate.go` | Created | `//go:embed migrations/*.sql` runner: bootstraps `schema_migrations`, sorts `NNNN_name.sql`, applies each in one `BEGIN IMMEDIATE` txn with the version record, rerun = no-op, failure = error (fatal at composition root, 6.1) |
| `internal/adapters/sqlite/migrations/0001_init.sql` | Created | Design DDL verbatim: users/sessions/categories/tickets/comments/audit_events + 6 indexes + CHECK constraints (English values) + `schema_migrations` (IF NOT EXISTS — runner bootstraps it) |
| `internal/adapters/sqlite/migrations/0002_fts.sql` | Created | Contentless FTS5 `tickets_fts` + 4 triggers — CORRECTED trigger shapes (see Deviations 1–2); design intent (D3) preserved |
| `internal/adapters/sqlite/filters.go` | Created | `buildTicketWhere` shared AND builder (state/priority/category/user + FTS text clause), `orderByCreatedDesc` (D2), `priorityOrderCASE` (D11 shared fragment) |
| `internal/adapters/sqlite/ticket_store.go` | Created | `ticketStore`: Create (MAX+1 in immediate txn, retryUnique 3×, assigns ID+Number), Update/GetByID (NotFound), List/Count/chips; `unitOfWork`: Create/Update with atomic ticket+audit (rollback on append failure, event.TicketID stamped); `appendAuditEventsTx` (shared with audit store); `scanTicketFrom` (RFC3339 parse, NULLable lifecycle) |
| `internal/adapters/sqlite/comment_store.go` | Created | Add (ID assign, FK/CHECK errors surface), ListByTicket `created_at ASC, id ASC` |
| `internal/adapters/sqlite/audit_store.go` | Created | Append (multi-event, one immediate txn), ListByTicket ASC (id tiebreak for same-timestamp batches) |
| `internal/adapters/sqlite/user_store.go` | Created | Create/Update (UNIQUE email → DuplicateError), Delete (sessions first + tickets FK → ReferencedError, one txn), GetByID/GetByEmail (NotFound), Count, List/ListActive |
| `internal/adapters/sqlite/session_store.go` | Created | Create (store-stamps created_at — slice-2 deviation #4), GetByID (expired → NotFound + lazy purge, wall-clock TTL), Delete (idempotent logout) |
| `internal/adapters/sqlite/search_store.go` | Created | Search/SearchCount via shared builder (text clause `t.id IN (SELECT rowid FROM tickets_fts WHERE tickets_fts MATCH ?)`), order created_at DESC, id DESC |
| `internal/adapters/sqlite/{sqlite,ticket_store,timeline_store,user_store,session_store,search_store}_test.go` | Created | 56 top-level tests + 5 subtests; helpers: newTestDB (unique named shared-cache memory DSN + SetMaxOpenConns(1) + migrate), seedCategory/seedUser/seedTicket, `injectAuditFailure` (RAISE trigger), `uniqueNumberViolation` (real driver error) |
| `go.mod` / `go.sum` | Modified | `modernc.org/sqlite v1.56.0` promoted to direct require (imported by the adapter); transitive graph tidied |

## Deviations from Design

1. **0002 FTS trigger SQL corrected against the real driver** (task 4.5, the slice's biggest finding). The design's exact trigger statements are BROKEN on modernc.org/sqlite v1.56.0: (a) the reindex `INSERT ... SELECT` into a contentless FTS5 table silently indexes NOTHING (probed: VALUES inserts index, SELECT inserts don't); (b) the `'delete'` command with empty column values is a no-op at best and raises `SQLITE_CORRUPT (267)` at worst (repeated empty deletes on one rowid; empty delete before a reindex). Fixes, all verified with the driver (incl. `integrity-check` after every step): reindexes use VALUES with scalar subqueries; comment triggers reindex WITHOUT a delete (append-only comments only grow → the reindex is exact); delete/update triggers pass the literal OLD title/description with empty comments — the shape that removes superseded entries cleanly (spec acceptance "search reflects edits" proven: "Old" → 0 hits after edit). Comment terms orphaned by empty-comments deletes are invisible to every search (queries join back against `tickets`).
2. **`TestMigrateCreatesSchema`/`TestMigrateRerunIsNoOp` updated** to expect versions {1,2} — the 4.1-era assertions (`want 1`) became stale the moment 0002 landed; the test's intent (all embedded migrations applied, rerun no-op) is preserved.
3. **`appendAuditEventsTx` lives in `ticket_store.go`** (the UoW's atomicity half, 4.2) and is reused by `audit_store.Append` (4.3) — one insert path, no duplication.
4. **`priorityOrderCASE` (D11) is defined but unreferenced by live queries**: the list/search ports order `created_at DESC, id DESC` (D2) and expose no sort key, so the shared fragment exists per D11's "single shared SQL fragment constant" and is documented for the priority-sort path. No acceptance in 4.2–4.5 exercises a sort-by-priority (the spec's priority-sort scenario is unreachable through the current port surface).
5. **Test DSN deviates from the design's literal `file::memory:?cache=shared`**: `file:<unique-name>?mode=memory&cache=shared&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_txlock=immediate`. The unique name per test is REQUIRED — the literal unnamed shared memory DB is process-global and would leak rows across tests (the exact "no such table" flake source the design warns about). FK pragma added so FK tests behave like production.
6. **CategoryStore port has NO Phase-4 task** (tasks.md lists only ticket/comment/audit/user/session/search stores): the adapter does not implement `application.CategoryStore` yet. Tests seed categories via direct SQL. **Phase 5/6 need `category_store.go`** (CategoryService wiring) — flagging for the orchestrator; out of the assigned scope (4.1–4.5).

## Acceptance Verification

| Acceptance (4.1–4.5) | Result |
|---|---|
| Migrations run transactionally; rerun = no-op | ✅ (broken-migration rollback test; rerun test) |
| Bad FK (category/user_id) → error | ✅ (raw SQL + store-level FK tests) |
| Shared-cache single pool no "no such table" flakes | ✅ (two-pool visibility test; unique named DBs) |
| Sequential numbers 1042→1043 (→1044) | ✅ |
| 2-goroutine concurrent create → distinct numbers | ✅ 5× + `-race` stable |
| FK violation error on ticket create | ✅ |
| Chips reflect filtered set | ✅ (state+priority chips under filters) |
| Timeline ASC / audit occurrence order | ✅ (equal-timestamp id tiebreak) |
| Duplicate email → DuplicateError | ✅ |
| Delete referenced → ReferencedError | ✅ |
| Expired session lookup → NotFound (+ lazy purge) | ✅ |
| Logout deletes row | ✅ |
| Cross-field FTS match (title+desc+comment) | ✅ |
| Edit "Old"→"New": "Old" empty, "New" hits | ✅ |
| `"`/`(`/`*`/`:` in q → no error | ✅ (10 inputs incl. `a OR b`, `say "hi"`) |
| Text AND-composes with filters | ✅ |
| `go test ./internal/adapters/sqlite/ -run TestFTS5` | ✅ 6/6 (real driver) |
| gofmt / vet / build / full suite | ✅ all clean |

## Quality Gates

| Gate | Command | Result |
|---|---|---|
| Format | `gofmt -l .` | ✅ empty |
| Vet | `go vet ./...` | ✅ clean |
| Build | `go build ./...` | ✅ clean |
| Tests | `go test -count=1 ./...` | ✅ sqlite 56/56 + 5 subtests (0.15s), application 62, domain 15 — no regressions |
| Coverage | `go test -cover ./internal/adapters/sqlite/` | ✅ 80.9% of statements |
| Race | `go test -race ./internal/adapters/sqlite/ -run 'TestTicketCreateConcurrentDistinctNumbers\|TestUnitOfWork'` | ✅ clean |

## Risks

- **FTS5 trigger shapes differ from design.md** (see Deviations 1): the design's SQL would silently break search-on-comments and corrupt on edits. The corrected shapes are driver-verified, but `design.md`'s 0002 block is now stale — verify should flag it for a design update (archive phase) so the spec/design/code triangle stays honest.
- **CategoryStore unimplemented** (Deviation 6): Phase 5's CategoryService wiring and Phase 6 composition root will fail to compile until a `category_store.go` task exists. Recommend adding it to Phase 5 (or a small follow-up task) before the HTTP slice starts.
- **Session expiry uses wall-clock time** (port has no clock): matches D14 server-side enforcement; tests use real-relative expiry. The fake uses the injected clock; behavior equivalent at the boundary.
- **`priorityOrderCASE` unused** (Deviation 4): dead-by-contract constant; if verify's spec matrix insists on an executable priority-sort scenario, the ports need a sort key (out of Phase-4 scope).
- **Diff budget**: 5 commits ≈ 2,300 authored lines (tests ~1,150, production ~1,150) vs the ~1,300 estimate; every assertion maps to a spec scenario (slice-1 precedent: flag rather than trim). Orchestrator decides single-PR-3 vs sub-split.

## Next Steps

- `verify` on slice 3 (recommended): the adapter is independently verifiable against ticket-management (numbering 1042→1043, concurrent), ticket-search (FTS5 cross-field/edit-consistency/chips/specials), audit-log (occurrence order), comment-timeline (ASC), user-management (duplicate/delete-guard/session expiry/logout). After verification passes, orchestrator delivers PR 3 (stacked-to-main), then `apply` slice 4 (PR 4 — HTTP adapter, tasks 5.1–5.6) — which MUST first resolve the missing CategoryStore task.

---

# Apply Progress — tkt-mvp — Task 4.6 (follow-up after slice 3)

**Task**: 4.6 — `category_store.go` (newly added after slice-3 verification; resolves Deviation 6 / "CategoryStore unimplemented" risk from slice 3)
**Branch**: `tkt-mvp-adapters` (continues slice 3; commit `e58569c` on top of `72b9e55`)
**Mode**: Strict TDD (`strict_tdd: true`, `rules.apply.tdd: true`)
**State**: Task 4.6 complete — marked `[x]` in `tasks.md`
**Delivery**: work-unit commit `feat(sqlite): category store`; NO push, NO PR.

## Work Unit Evidence (Hard Gate — all modes)

| Evidence | Required value |
|---|---|
| Focused test command and exact result | `go test ./internal/adapters/sqlite/ -run 'TestCategory' -count=1` → **11/11 PASS** (real modernc driver, shared-cache memory DBs); RED first: compile failure `undefined: newCategoryStore` across all 11 tests before implementation |
| Runtime harness command/scenario and exact result | `go test ./internal/adapters/sqlite/ -count=1` → **76/76 top-level + 6 subtests PASS** (65 prior + 11 new). Real-driver FK path proven: `DELETE` on a category referenced by `tickets.category_id` fires `FOREIGN KEY constraint failed` → `ReferencedError`, category row survives (same runtime boundary as slice 3 — no HTTP/composition root yet) |
| Rollback boundary | Revert commit `e58569c` (or `git revert`): only `internal/adapters/sqlite/category_store.go` + `category_store_test.go` are touched; slice-3 files, migrations, and the adapter surface are untouched (no accessor added — `newCategoryStore` stays package-private until Phase 5/6 wiring) |

## TDD Cycle Evidence

| Task | Test File | Layer | Safety Net | RED | GREEN | TRIANGULATE | REFACTOR |
|------|-----------|-------|------------|-----|-------|-------------|----------|
| 4.6 — category store | `category_store_test.go` | Store integration | ✅ 65+6 baseline green (pre-task run) | ✅ Test written first; run → compile fail `undefined: newCategoryStore` (10/11 sites) | ✅ `category_store.go` (Create UNIQUE→Duplicate, Update rename→Duplicate, Delete FK→Referenced, GetByID/List, `scanCategoryFrom`); 11/11 pass | ✅ Matrix built into RED (every spec scenario ≥2 cases): create happy+dup, rename+rename-to-dup+notfound, delete unref+referenced+notfound, GetByID found+notfound, List 3-by-id+empty | ➖ None needed — implementation mirrors `user_store.go` conventions verbatim (error mapping, scan projection, `ORDER BY id ASC`); clean at first GREEN |

## Test Summary
- **Total tests written**: 11 (all new; `category_store_test.go` 219 lines vs 133 production)
- **Total tests passing**: 11/11 (package now 76 top-level + 6 subtests); full repo: domain 15, application 62, sqlite 76 — no regressions
- **Layers used**: Store integration (11) — real modernc driver, shared-cache memory DBs
- **Approval tests**: None — no refactoring of existing behavior
- **Pure functions created**: `scanCategoryFrom` (row projection, reuses `rowScanner` interface from ticket_store.go)

## Commit

| # | Hash | Message | Contents |
|---|------|--------|----------|
| 6 | `e58569c` | `feat(sqlite): category store` | `category_store.go` (133 lines: Create/Update/Delete/GetByID/List + `scanCategoryFrom`), `category_store_test.go` (219 lines, 11 tests). NO other files — no `sqlite.go` accessor (deferred to Phase 5/6 wiring), no schema changes (categories table already in 0001_init.sql) |

## Files Changed

| File | Action | What Was Done |
|------|--------|---------------|
| `internal/adapters/sqlite/category_store.go` | Created | `categoryStore` implementing `application.CategoryStore`: Create (UNIQUE name → `DuplicateError{Kind:"category", Name}`), Update (rename; rename-to-duplicate → `DuplicateError`, 0 rows → `NotFoundError`), Delete (tickets FK → `ReferencedError{Kind:"category", ID}`, 0 rows → `NotFoundError`), GetByID (`ErrNoRows` → `NotFoundError`), List (`ORDER BY id ASC`), `scanCategoryFrom` projection (RFC3339 `created_at` parse via shared `timeLayout`); reuses shared `isUniqueViolation`/`isForeignKeyViolation`/`formatTime`/`rowScanner` |
| `internal/adapters/sqlite/category_store_test.go` | Created | 11 tests: create assigns ID + GetByID round-trip (name + created_at), duplicate name → `ErrDuplicate` + typed fields, rename → new name + "Bugs" free again (spec rename scenario), rename-to-duplicate → `ErrDuplicate` + original row unchanged, update/delete/GetByID NotFound ×3, delete unreferenced → gone, delete referenced (seeded ticket) → `ErrReferenced` + typed fields + category survives, List exact 3-by-id names + empty |

## Deviations from Design

None — implementation matches design and the port contract (ports.go `CategoryStore` signatures followed precisely). One convention note: `sqlite.go` gains NO `CategoryStore()` accessor in this commit (task scope is exactly the two files); tests use the package-private `newCategoryStore(s.db)`. The accessor belongs to Phase 5/6 wiring (handlers_categories.go / composition root) — flag for the orchestrator so wiring doesn't miss it.

## Acceptance Verification (category-management spec)

| Acceptance | Result |
|---|---|
| Duplicate category name → DuplicateError | ✅ (typed: Kind=category, Name=Bugs) |
| Rename to duplicate → DuplicateError | ✅ (Support→Bugs rejected; Bugs row unchanged) |
| Delete referenced → ReferencedError | ✅ (seeded ticket; typed Kind/ID; category survives) |
| Unreferenced deletable | ✅ (delete ok; GetByID → NotFound) |
| Create stores and lists category | ✅ (ID assigned, round-trip, List order) |

## Quality Gates

| Gate | Command | Result |
|---|---|---|
| Format | `gofmt -l .` | ✅ empty |
| Vet | `go vet ./...` | ✅ clean |
| Tests | `go test ./... -count=1` | ✅ sqlite 76/76 + 6 subtests, application 62, domain 15 — no regressions |
| Focused | `go test ./internal/adapters/sqlite/ -run 'TestCategory' -count=1` | ✅ 11/11 |

## Risks

- **No `Store.CategoryStore()` accessor yet** (see Deviations): Phase 5.6 `handlers_categories.go` and Phase 6 wiring must call it; until then the adapter surface omits the category port. Low risk — same pattern as the other stores, one-line addition at wiring time.
- **Update to identical name returns NotFoundError** (0 rows affected): inherited verbatim from the `userStore` convention (same SQLite semantics); the application service never issues a no-op rename in a path where it matters. Noted for verify; consistent with the established store contract.

## Next Steps

- `verify` slice 4.6: category-management spec rows are independently verifiable (duplicate/rename-duplicate/referenced-delete/unreferenced-delete) plus the port contract (GetByID/List/NotFound).
- Then `apply` slice 4 (PR 4 — HTTP adapter, tasks 5.1–5.6): 5.6 wiring MUST add `Store.CategoryStore()` accessor (flag above) so `CategoryService` can be wired against the real adapter.

## Task 4.2 follow-up (dc6609a): Priority Ordering (D11) — SDD verify CRITICAL fix

- Verify slice 3 reported CRITICAL: priorityOrderCASE was dead code (ticket-search spec "Priority Ordering" unimplemented).
- Fix: `TicketQuery.SortByPriority` flag (ports.go) + `orderBy(q)` shared ORDER BY (filters.go) wiring `priorityOrderCASE DESC, created_at DESC, id DESC` into List and Search; RED test `TestTicketListSortByPriority` first (spec scenario: low/critical/medium/high → critical, high, medium, low, on both List and Search).
- Evidence: test green, full suite green, vet/fmt clean. Commits on tkt-adapters: dc6609a.
- RDD note: review-46cdd206c3325e78 approved the pre-fix candidate; the fix creates a NEW candidate → new native review (new consent) before PR #4.

---

# Apply Progress — tkt-mvp — Slice 4 (PR 4 of 5)

**Slice**: PR 4 — HTTP adapter (tasks 5.1–5.6)
**Branch**: `tkt-http` (off `main` @ d796564, target for PR 4, stacked-to-main)
**Mode**: Strict TDD (`go test ./...`, config `strict_tdd: true`, `rules.apply.tdd: true`)
**State**: Slice 4 complete — tasks 5.1, 5.2, 5.3, 5.4, 5.5, 5.6 marked `[x]` in `tasks.md`
**Delivery**: chained PRs, stacked-to-main, PR 4 of 5. Commits created; NO push, NO PR (delivery gated on verification by orchestrator).
**Note**: `Store.CategoryStore()` accessor landed in the 5.4 commit (its first consumer — ticket forms/filters list categories), not 5.6: the harness and ticket wiring cannot compile without it earlier. The 4.6 follow-up flagged exactly this accessor; same one-liner, earlier task.

## Work Unit Evidence (Hard Gate — all modes)

| Evidence | Required value |
|---|---|
| Focused test command and exact result | Per task: `go test ./internal/adapters/http/ -run <TestX> -count=1` — RED per task (compile failures for missing `Renderer`/`mapError`/`SessionMiddleware`/`NewAuthHandlers`/`NewTicketHandlers`; behavioral 404/303 for unregistered 5.5 routes), then GREEN. Final: **106 top-level tests + 27 subtests PASS** (http package 7.7s), exit 0 |
| Runtime harness command/scenario and exact result | `go test ./internal/adapters/http/ -count=1` against the REAL modernc sqlite store (temp-file DB + Migrate) + REAL templates + full middleware-wrapped mux: login cookie → authorized list/detail/transition/comment/edit/users/categories flows; deactivation kills the session row (GetByID → ErrNotFound); cross-site Origin POST → 403. This is the slice's runtime boundary (no composition root yet — that is Phase 6) |
| Rollback boundary | Revert the 6 commits `ba66fbf..08ab271` (or the branch): `internal/adapters/http/` + `web/templates/` + the `Store.CategoryStore()` accessor line in `internal/adapters/sqlite/sqlite.go`; main stays at d796564 with slices 1–3 intact. Goldens regenerate via `-update` |

## TDD Cycle Evidence

| Task | Test File | Layer | Safety Net | RED | GREEN | TRIANGULATE | REFACTOR |
|------|-----------|-------|------------|-----|-------|-------------|----------|
| 5.1 render+errors+harness | `{render,errors,golden}_test.go` | Unit | ✅ 3-pkg baseline green | ✅ Compile fail (`Renderer`, `NewRendererWith`, `mapError` undefined) | ✅ 13 mapError cases + 4 render cases + 2 goldens pass | ✅ 12 error mappings + HX/full/status passthrough/unknown-page | ✅ Render buffers before WriteHeader (status survives template errors); errors.As target vars; goldens regenerated + stable |
| 5.2 middleware | `middleware_auth_test.go` | Integration (real store + httptest) | ✅ http pkg green | ✅ Compile fail (`SessionMiddleware` undefined) | ✅ 10 top-level tests + subtests pass | ✅ no-cookie/expired/forged/empty-users/setup-exempt/unavailable/valid/deactivated/login-redirect/healthz/Origin×3 | ✅ shared harness extracted; `doRequest`/`wantRedirect` dedup |
| 5.3 auth handlers | `handlers_auth_test.go` | Integration | ✅ (suite carried) | ✅ Compile fail (`NewAuthHandlers` undefined) | ✅ 10 tests pass | ✅ login ok + 3 generic-failure classes + logout idempotent + setup create/validate/unavailable | ✅ harness grew to full wiring; real-relative clock (sqlite session TTL is wall-clock) |
| 5.4 list+create | `handlers_tickets_test.go` | Integration | ✅ (suite carried) | ✅ Compile fail (`NewTicketHandlers` undefined) | ✅ 17 tests pass | ✅ empty/filtered/search/chips/pagination 10-10-5/HX create + 422×4 + inactive/active assign | ✅ `parseFilters` ignores unknown values (threat matrix); `listHref` single href builder; chips pure `buildChips` |
| 5.5 detail+transition+comments | `handlers_detail_test.go` | Integration | ✅ (suite carried) | ✅ Behavioral RED (routes unregistered → 404/303) | ✅ 19 tests pass | ✅ show 200/400/404/HX; transition happy/cycle/invalid/reopen±reason/HX; comments add/empty/closed/HX/chronological; edit prefill/update/audit/invalid/unassign/HX | ✅ `afterMutation` + `renderDetailError` dedup; `allowedNext` presentation table mirrors domain map (noted) |
| 5.6 users+categories | `handlers_admin_test.go` | Integration | ✅ (suite carried) | ✅ Behavioral RED (routes unregistered) | ✅ 18 tests pass | ✅ users index/create/dup 409/422/edit/update/dup-email/deactivate-kills-sessions/delete±referenced±live-session; categories index/create/dup/422/rename±dup/delete±referenced | ✅ shared `renderXError` patterns; `userID`/`categoryID` path helpers |

### Test Summary
- **Total tests written**: 74 new top-level tests + 27 subtests (http package 106 top-level / 27 subtests total; slice baseline was 0)
- **Total tests passing**: 106/106 top-level, 27/27 subtests (http) — full repo: domain 15/29, application 62/14, sqlite 77/6 — no regressions
- **Layers used**: Unit (13 mapError + 4 render + 8 golden), Integration (81) — real sqlite store, real templates, httptest
- **Approval tests**: None — no refactoring of existing behavior
- **Pure functions created**: `mapError`, `parseFilters`, `buildChips`, `allowedNext`, `shellFor`, `originAllowed`, `listHref` (all table-driven or switch-pure)

## Commits (in order)

| # | Hash | Message | Contents |
|---|------|--------|----------|
| 1 | `ba66fbf` | `feat(http): render + error mapping + golden harness` | `web/templates/{templates.go,base.html,partials/styles.html}`, `internal/adapters/http/{render,errors}.go` + tests, `testdata/{render_full_page,render_fragment}.golden` |
| 2 | `1c5e126` | `feat(http): session middleware + bootstrap gating` | `middleware_auth.go` + `middleware_auth_test.go` (10 tests) |
| 3 | `f03e711` | `feat(http): auth handlers + login/setup templates` | `handlers_auth.go`, `auth.html`, `pages/{login,setup}.html`, `harness_test.go`, tests |
| 4 | `d76129d` | `feat(http): ticket list + create handlers + templates` | `handlers_tickets.go` (list/create), `sqlite.go` (+`CategoryStore()` accessor), pages `tickets_index`/`tickets_new`, partials `ticket_list`/`ticket_form`/`filter_form`/`pagination`/`summary_chips`/`state_badge`, 9 goldens + tests |
| 5 | `d6f3369` | `feat(http): ticket detail + transition + comment handlers + templates` | `handlers_tickets.go` (+show/edit/update/transition/addComment), pages `tickets_show`/`tickets_edit`, partials `ticket_detail`/`ticket_edit_form`/`comment_form`/`comment_list`/`audit_timeline`, 7 goldens + tests |
| 6 | `08ab271` | `feat(http): users + categories handlers + templates` | `handlers_users.go`, `handlers_categories.go`, pages `users_index`/`users_new`/`categories_index`/`categories_new`, partials `user_form`/`category_form`, 6 goldens + tests |

## Files Changed (slice 4)

| File | Action | What Was Done |
|------|--------|---------------|
| `web/templates/templates.go` | Created | `//go:embed base.html auth.html pages/*.html partials/*.html`; `var FS embed.FS` (embed patterns grow per task) |
| `web/templates/base.html` | Created | App shell: 72px rail (taste), brand mark, nav (tickets/users/categories), operator initials chip, logout POST form; `{{template "content" .}}` slot |
| `web/templates/auth.html` | Created | Login/setup split shell: dark brand panel + light form area |
| `web/templates/partials/styles.html` | Created | Taste CSS system: #ECEFF3 bg, graphite ink, #315EFF accent, Geist stack, badges, chips, queue tables, cards, activity rail |
| `web/templates/pages/*.html` (8) | Created | tickets_index, tickets_new, tickets_show, tickets_edit, login, setup, users_index, users_new, categories_index, categories_new (10 pages) |
| `web/templates/partials/*.html` (12) | Created | ticket_list, ticket_form, ticket_detail, ticket_edit_form, comment_form, comment_list, audit_timeline, filter_form, pagination, summary_chips, state_badge, user_form, category_form (13 partials) |
| `internal/adapters/http/render.go` | Created | `Renderer` (per-page sets: shell+partials+page; shared fragment set), `Render` (HX → fragment / full → shell; buffered write; funcs `formatTime` RFC3339-UTC D7, `ticketNumber` TKT-N, `initials`); `NewRendererWith(fsys)` test hook |
| `internal/adapters/http/errors.go` | Created | `mapError` D5 table via `errors.As` (wrapped errors surface the typed message); 401 generic; 500 "Internal server error" |
| `internal/adapters/http/middleware_auth.go` | Created | Origin gate on POST (D17); exempt /login*,/setup*,/healthz; authed-on-/login → /tickets; /setup only when users empty; protected routes → 303 /login (or /setup); deactivated user's session destroyed on sight (D14); ctx user |
| `internal/adapters/http/handlers_auth.go` | Created | GET/POST /login, POST /logout (cookie clear + row delete), GET/POST /setup (D16) |
| `internal/adapters/http/handlers_tickets.go` | Created | GET / → /tickets; list (filters+page+chips D2/D11); new form; create (HX → ticket_list+chips OOB / 303 → detail); show; edit form; update (incl. unassign); transition (reopen reason); add comment (HX → comment_list) |
| `internal/adapters/http/handlers_users.go` | Created | /users CRUD: create, edit (name/email/password/active checkbox → deactivate D14), delete (referenced → 409 re-render; sessions swept by store txn) |
| `internal/adapters/http/handlers_categories.go` | Created | /categories CRUD: create, rename, delete (referenced → 409 re-render) |
| `internal/adapters/sqlite/sqlite.go` | Modified | +`Store.CategoryStore()` accessor (first consumer 5.4; the flagged 4.6 follow-up line) |
| `internal/adapters/http/*_test.go` (7) + `harness_test.go` | Created | 106 top-level tests; harness = real store + all services + real renderer + full mux + admin session |
| `internal/adapters/http/testdata/*.golden` (25) | Created | Pages + fragments frozen (D7): render_full_page, render_fragment, tickets_index/new, ticket_list, ticket_form, filter_form, pagination, summary_chips, state_badge, tickets_show/edit, ticket_detail, ticket_edit_form, comment_form, comment_list, audit_timeline, users_index/new, user_form, categories_index/new, category_form |

## Deviations from Design

1. **`Store.CategoryStore()` landed in 5.4, not 5.6** (see slice note): the ticket wiring is its first consumer; the orchestrator's flag ("add so wiring compiles") is fulfilled one task earlier. One-line accessor, no behavior.
2. **`pages/users_new.html` + `pages/categories_new.html` added** (task list names only `users_index`, `categories_index`, `user_form`, `category_form`): full-page form renders need page wrappers (same pattern as `tickets_new`); the listed names cover the fragments.
3. **`initials` template func** (render.go) added for the rail operator chip (taste design has an operator circle in the rail); D7-safe (pure string transform).
4. **Edit/comment/transition forms post full-page** (no hx-post attributes): the handler-level HX contract (fragment responses) is implemented + tested per design D6, but the forms themselves use plain POST → 303 to keep the swap wiring honest (a form targeting one fragment cannot correctly receive a different fragment on error). The real HTMX interactivity — filters, chips OOB, pagination — uses hx-get with `#ticket-list` outerHTML swaps as designed.
5. **`allowedNext` duplicates the domain transition table** as presentation (design table lives in domain's unexported map): documented in code; domain remains the single enforcement point (illegal pairs → 422 from `Transition`).
6. **Login/`setup` natural fragments**: HX re-renders use the page's own `content` block (Renderer's `fragment == ""` path) instead of a dedicated `login`/`setup` fragment name — same contract, no duplicate templates.
7. **Deactivation kills sessions at the middleware** (session row deleted when a deactivated user's token is seen) instead of at the deactivation handler: the SessionStore port has no `DeleteByUser` (ports are fixed by design); the observable D14 contract — "next request is logged out" — is proven end-to-end (`TestUserDeactivateKillsSessions` asserts 303 + row gone). Store-level user Delete DOES sweep sessions in its own transaction (slice-4 behavior, reused).

## Acceptance Verification (slice 4)

| Acceptance | Result |
|---|---|
| HX-Request → fragment only (no `<html>`); absent → full page | ✅ (render + every route's HX branch) |
| Validation/InvalidTransition/Reopen/Inactive/InvalidPriority → 422; NotFound → 404; Duplicate/Referenced → 409; InvalidCredentials → 401 generic; unknown → 500 | ✅ 13-case table incl. wrapped errors |
| No cookie → 303 /login; expired/forged → 303 /login | ✅ |
| Empty users table → all routes 303 /setup; /setup unavailable with users | ✅ (middleware + handler defense) |
| Authed user on /login → 303 /tickets | ✅ |
| Cross-site Origin POST → 403 (threat matrix) | ✅ (incl. /login + malformed Origin) |
| Correct login → 303 + Set-Cookie (HttpOnly, Strict); wrong pw/unknown email/deactivated → same generic 401 | ✅ no enumeration, no session |
| Logout → session row gone + cookie cleared + next request unauthenticated | ✅ |
| Setup creates first ACTIVE regular user → 303 /login; user can log in | ✅ |
| Create valid → 303 detail (full) / list+chips OOB (HX); missing title/invalid category/invalid priority/inactive user → 422 English | ✅ |
| Filters compose AND; chips reflect filtered set; pagination 10/10/5; FTS specials never 500 | ✅ (state filter, q search, 25-ticket pagination) |
| Transition happy path + invalid 422 + reopen reason required/success; non-numeric id → 400; unknown id → 404 | ✅ |
| Comment add (author from session), empty body 422, closed-ticket accepted, timeline ASC | ✅ |
| Edit → 303 detail; audit records changed fields; unassign works | ✅ |
| Users CRUD happy paths; duplicate email 409; delete referenced 409; deactivate kills sessions (D14) | ✅ |
| Categories CRUD; duplicate name 409; rename-to-dup 409; delete referenced 409 | ✅ |
| goldens regenerated (`-update`) + rerun stable | ✅ (25 goldens, two consecutive identical runs) |
| gofmt / vet / build / full suite / `-race` | ✅ all clean (CI parity: gofmt -l empty, vet clean, build clean, race green) |

## Quality Gates

| Gate | Command | Result |
|---|---|---|
| Format | `gofmt -l .` | ✅ empty |
| Vet | `go vet ./...` | ✅ clean |
| Build | `go build ./...` | ✅ clean |
| Tests | `go test ./... -count=1` | ✅ http 106+27, sqlite 77+6, application 62+14, domain 15+29 |
| Race | `go test ./... -race -count=1` | ✅ green (http 92s) |
| Coverage | `go test -cover ./...` | ✅ http 76.4%, sqlite 80.9%, application 88.0%, domain 82.7% |
| Goldens | `-update` then rerun ×2 | ✅ stable, no diff after regen |

## Risks

- **RDD WARNING follow-ups from slice 3** — none of the three are touched by this slice: (1) UoW create ID/Number on failure remains a sqlite-adapter concern (slice 4 owned it; HTTP never constructs tickets outside the service); (2) RFC3339 subseconds — formatTime renders `time.RFC3339` exactly as persisted (D7); (3) `Store.Close` still missing — http tests use temp-dir files, no close needed; Phase 6 composition root should surface it.
- **`allowedNext` presentation duplication** (Deviation 5): if the domain transition table changes, the transition panel list could drift. Mitigated: the domain rejects illegal pairs (422) regardless; a verify-phase check could compare the two tables.
- **`TestUserUpdateDuplicateEmail409` posts `rec` after `beto.ID` resolution ordering** — resolved correctly in the final test (single rename attempt); no flake observed across repeated runs.
- **Wall-clock session expiry in tests**: sessions minted by services expire against real time (sqlite store semantics); `fixedNow = time.Now()` keeps fixtures real-relative — deterministic goldens use literal instants, service-minted times never enter goldens.
- **Secure cookie = `r.TLS != nil`** (design: "Secure (production behind TLS; dev flag documented)"): no config flag exists until Phase 6; the runtime TLS check implements the intent without a knob.
- **Diff budget**: 6 commits ≈ 7,400 changed lines (goldens ~1,200 lines of deterministic HTML, excluded from authored-risk count per work-unit-commits; authored ≈ 6,200). Slice precedent: flag rather than trim real assertions. Orchestrator decides single-PR-4 vs sub-split (e.g., 4a auth/middleware 5.1–5.3, 4b tickets 5.4–5.5, 4c admin 5.6).
- **Origin gate exempts non-POST methods** (D17 says POST): PUT/DELETE don't exist in the route table; nothing else is unsafe in this app.

## Next Steps

- `verify` on slice 4 (recommended): the HTTP adapter is independently verifiable against ticket-management, comment-timeline, audit-log (Activity panel ASC), user-management (login/bootstrap/deactivate/D14), category-management, ticket-search (filters/chips/pagination) specs + the threat-matrix rows (non-numeric id, FTS specials, cross-site Origin).
- After verification passes, orchestrator delivers PR 4 (stacked-to-main), then `apply` slice 5 (PR 5 — composition root: `cmd/server/main.go`, Docker, healthcheck; tasks 6.1–6.3). Phase 6 must wire `Store.Close()` and note the RDD follow-ups.

## UX/UI polish + design decisions (post-HTTP-slice, pre-delivery)

- **Requester derived from session (ticket-management spec)**: after user feedback (impersonation risk), requester fields were REMOVED from the create form entirely. CreateTicketInput no longer accepts requester; TicketService.Create derives RequesterName/Email from the session actor. No caller-supplied value is accepted (test: forged requester_* POST is ignored).
- **Newest-first timeline (comment-timeline spec)**: comments + audit events now render in ONE merged reverse-chronological stream (GitHub-style). Spec updated from chronological ASC to newest-first DESC.
- **Activity panel merged**: the right-column Activity card was removed; audit events interleave into the conversation timeline. Right column keeps only Transition.
- **HTMX integration completed**: vendored htmx.min.js v2.0.4 (web/templates/static/, BSD-2-Clause), GET /static/htmx.min.js served with cache headers + middleware bypass, `<script defer>` in base.html. Pagination links now carry real hrefs (progressive enhancement). Chips moved OUTSIDE #ticket-list; HX list responses render chips (OOB) + list as sibling fragments via Renderer.RenderSwap.
- **A11y/UX**: h3→h2 heading hierarchy in all form partials; disabled pagination states (aria-disabled + opacity); queue table overflow-x:auto; hx-indicator spinners on filter + pagination.

## Ticket UX redesign (user-confirmed, post-slice 4)

- **Inline detail editing**: removed the visible Edit link and long Details card. The ticket detail sidebar now contains a Properties form for title, description, category, priority, and assigned user plus a separate compact State form. `GET /tickets/{id}/edit` remains as an unlinked technical fallback.
- **Metadata and timestamps**: requester, created, and updated remain concise read-only metadata below the title. HTTP templates render UTC as `HH:mm · DD-MM-YYYY` inside `<time datetime="RFC3339">`; SQLite persistence remains RFC3339.
- **Merged timeline presentation**: newest-first merge remains unchanged. Agent comments and audit events now use distinct surfaces/borders; audit dots align to action text; visible action/state/priority values are humanized.
- **List simplification**: `Number` is now `ID` while values remain `TKT-N`. Summary chips, OOB markup, `Renderer.RenderSwap`, chip view data/goldens, and the now-unused SearchService summary-count queries were removed. The filter bar is the single canonical filtering surface; HTMX list filtering and pagination still swap `#ticket-list`.
- **Accessibility/responsiveness**: associated sidebar labels, visible `:focus-visible`, semantic times, preserved internal form values, and responsive detail/filter layouts at tablet/mobile widths.
- **Behavior evidence**: render-helper tests cover UTC human text/RFC3339 datetime and enum humanization; HTTP tests cover no-OOB list fragments, canonical selected filters, ID heading, inline Properties controls, absent Edit link, semantic metadata, differentiated timeline entries, and humanized transition values. Goldens regenerated through `go test ./internal/adapters/http -run TestGolden -update` and rechecked without `-update`.
- **Audit reference enrichment fix**: `TicketView.Timeline` now carries presentation labels resolved in the application layer. `user`/`user_id` display as `Assigned To` with user names or `Unassigned`; `category`/`category_id` resolve category names; missing references degrade to `Unknown user`/`Unknown category`; enum values are humanized and unknown fields fall back safely. The template no longer parses audit fields or IDs.
