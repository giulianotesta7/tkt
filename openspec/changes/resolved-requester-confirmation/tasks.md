# Tasks: resolved = awaiting requester confirmation (issue #55)

Implements `design.md` (D1–D9) against the 6 spec deltas. Strict TDD (RED → GREEN, tests before implementation per `openspec/config.yaml`), `go test ./...` after every work unit, one conventional commit per task (work-unit-commits: tests travel with behavior). Skills loaded at apply: `work-unit-commits` (commit splitting), `ux-ui` (`.agents/skills/ux-ui/SKILL.md`, template/golden work), `tkt-e2e` (`.agents/skills/tkt-e2e/SKILL.md`, e2e phase).

## Review Workload Forecast

| Field | Value |
|-------|-------|
| Estimated changed lines | ~1,500–1,700 authored (+ ~600–1,200 generated golden HTML, excluded per work-unit-commits precedent) |
| 400-line budget risk | High |
| Chained PRs recommended | Yes |
| Suggested split | PR 1 (tasks 1.x + 2.x, ~350) → PR 2 (3.1–3.2, ~380) → PR 3 (3.3–3.4, ~280) → PR 4 (4.x, ~355) → PR 5 (5.x, ~260) |
| Delivery strategy | ask-on-risk — risk detected, so STOP and ask before apply |
| Chain strategy | stacked-to-main |

```text
Decision needed before apply: Yes
Chained PRs recommended: Yes
Chain strategy: stacked-to-main
400-line budget risk: High
```

Forecast basis: 6 capabilities × 55 scenarios across domain, application, sqlite (migration + store + UoW), HTTP (route + flags + goldens), templates, and e2e; three test files are new (`migration_0010_test.go`, `ticket_confirmation_test.go`, 3 new golden cases). Authored total exceeds 400 ⇒ per `ask-on-risk`, the orchestrator must ask before apply: accept a 5-PR stacked chain (recommended) or an explicit `size:exception` single PR (only with explicit `size:exception` acceptance — never inferred).

---

## Phase 1 — Domain, audit attribution, migration 0010 (PR 1)

### 1.1 Verify + land the JD-fixed reopen guard (in-tree fix, no re-implementation)
- [x] 1.1 Verify + land the JD-fixed reopen guard (in-tree fix, no re-implementation)
- Files (already modified vs HEAD 120c3a5): `internal/domain/ticket.go:51` (guard narrowed `IsClosed(from)` → `from == StateClosed`), `internal/domain/state_test.go` (`TestReopenFromResolvedWithoutReason` + matrix updates), `internal/application/ticket_service_test.go:770` (`TestTransitionReopenResolvedWithoutReason`, inverted).
- Steps: verification only — `go test ./internal/domain/ ./internal/application/ -run 'Reopen'` (expect 3/3 PASS), then `go test ./...`, `go vet ./...`, `openspec validate --all --strict` (17/17). No code edits.
- Acceptance: suites green; `git status` shows exactly the 3 expected modified files; committed as one commit; working tree clean.
- Commit: `fix(domain): reopen from resolved no longer requires a reason (JD regression fix)`

### 1.2 D4 verify-first: confirm the plan recheck already pins `workflow_version_id`
- [x] 1.2 D4 verify-first: confirm the plan recheck already pins `workflow_version_id`
- Discovery target: `internal/adapters/sqlite/workflow_uow.go` — `ApplyWorkflowPlan` reloads the persisted pin (`:161-169`, `SELECT workflow_version_id FROM tickets`) and `validateMutationPlan` (`:288-290`) rejects `ticket.WorkflowVersionID == nil || *… != in.ExpectedVersionID` with typed `conflict("workflow version mismatch")`.
- Steps: record in apply-progress that a detached ticket (NULL pin) already fails any in-flight `ApplyWorkflowPlan` with `ErrWorkflowPositionConflict` ⇒ **no `workflow_runner.go` change needed** (design D4 branch resolved). Regression proof lands as task 2.2's RED test.
- Acceptance: evidence (quoted lines + outcome) in apply-progress; zero code changes.
- Commit: none (verification-only).

### 1.3 Migration 0010 + domain `ClosureVia` field (RED → GREEN)
- [x] 1.3 Migration 0010 + domain `ClosureVia` field (RED → GREEN)
- RED: new `internal/adapters/sqlite/migration_0010_test.go` (pattern of `migration_0008/0009_test.go`: column exists, legacy rows read `closure_via IS NULL`, forward-only apply, no backfill); `internal/domain/state_test.go` audit-shape cases — plain `Transition` events carry `ClosureVia == nil`.
- GREEN: `internal/adapters/sqlite/migrations/0010_audit_closure_via.sql` (single additive `ALTER TABLE audit_events ADD COLUMN closure_via TEXT;` + design-note header per D2); `internal/domain/audit.go` — `AuditEvent.ClosureVia *string` + constants `ClosureViaRequesterConfirmation = "requester_confirmation"`, `ClosureViaManualAgent = "manual_agent"` (D1).
- Acceptance: `go test ./...` green; `0001` state CHECK untouched; no other migration files touched.
- Commit: `feat(domain): audit closure_via attribution + additive migration 0010`

### 1.4 Persist `closure_via` in `appendAuditEventsTx` (RED → GREEN)
- [x] 1.4 Persist `closure_via` in `appendAuditEventsTx` (RED → GREEN)
- RED: sqlite store test — event with `ClosureVia` set round-trips the value; nil event persists and reads back NULL.
- GREEN: `internal/adapters/sqlite/ticket_store.go` `appendAuditEventsTx` (`:295-300`) binds `closure_via` (NULL when nil).
- Acceptance: `go test ./...` green; existing audit round-trip tests unchanged.
- Commit: `feat(sqlite): persist audit closure_via`

## Phase 2 — Persistence / UoW validators (PR 1, continued)

### 2.1 UoW pins workflow transition audits to nil `ClosureVia` (RED → GREEN)
- [x] 2.1 UoW pins workflow transition audits to nil `ClosureVia` (RED → GREEN)
- RED: `internal/adapters/sqlite/workflow_uow_terminal_test.go` — a plan whose transition audit carries non-nil `ClosureVia` is rejected ("workflow closure audit attribution mismatch"); existing workflow closure audits assert `actor="workflow"`, `ActorUserID IS NULL`, `ClosureVia == nil` (audit-log delta: workflow actor convention preserved).
- GREEN: `internal/adapters/sqlite/workflow_uow.go` `validateTransitionOp` (`:1131+`) gains exactly one additive assertion (D1.3). Runner untouched.
- Acceptance: `go test ./...` green incl. all terminal matrices.
- Commit: `feat(sqlite): UoW rejects closure_via-stamped workflow transition audits`

### 2.2 Detachment conflict + runner "awaiting confirmation" regressions (characterization)
- [x] 2.2 Detachment conflict + runner "awaiting confirmation" regressions (characterization)
- RED: `workflow_uow_terminal_test.go` — (a) detach via direct SQL (`workflow_version_id = NULL`) then `ApplyWorkflowPlan` → typed `ErrWorkflowPositionConflict` "workflow version mismatch" (proves 1.2, no service dependency); (b) `workflow_runner` terminal test: run completes via `resolve_ticket` on a requester-owned ticket and the ticket **remains `resolved`** with no close transition (workflow-execution delta). All existing terminal matrix rows stay green.
- GREEN: expected none — behavior already pinned (1.2). If RED fails, add the single missing fact check to the recheck per D4 and say so in apply-progress.
- Acceptance: both new tests pass; `go test ./...` green.
- Commit: `test(sqlite): pin in-flight-plan conflict on detached tickets + awaiting-confirmation regression`

## Phase 3 — Application services + policy (PR 2 = 3.1–3.2, PR 3 = 3.3–3.4)

### 3.1 Manual-closure gate + `ClosureVia` stamping in `Transition` (RED → GREEN)
- [x] 3.1 Manual-closure gate + `ClosureVia` stamping in `Transition` (RED → GREEN)
- RED: new `internal/application/ticket_confirmation_test.go` — manual `resolved → closed` on a requester-owned ticket denied for `agent` (assigned), `admin`, `root` → `ForbiddenError(ErrMsgClosureRequiresConfirmation)`, state unchanged; requester-NULL `resolved → closed` by assigned agent/admin succeeds with event `ClosureVia = manual_agent` (D5/D6); role-`user` generic transitions still denied (regression).
- GREEN: `internal/application/policy.go` — `isTicketRequester(actor, t)` identity predicate (D3, `requireFormActor` precedent); `internal/application/ticket_service.go` `Transition` gains the single gate `from == resolved && to == closed && t.RequesterUserID != nil → Forbidden` and stamps `ClosureViaManualAgent` on requester-NULL manual closures.
- Acceptance: `go test ./...` green; state machine tests from 1.1 untouched and green.
- Commit: `feat(application): block manual closure of requester-owned resolved tickets`

### 3.2 `ConfirmResolution` (RED → GREEN)
- [x] 3.2 `ConfirmResolution` (RED → GREEN)
- RED: `ticket_confirmation_test.go` — requester (role `user`) confirms own `resolved` → `closed`, `closed_at` stamped, `resolved_at` kept, event `ClosureVia = requester_confirmation`, actor = requester; confirm on `new`/`in_progress`/`closed` → state-machine error, no audit row; confirm by agent/admin/root → Forbidden; by another role-`user` → NotFound; carve-out regression: requester field edits / assignment / other transitions still denied (role-authorization delta).
- GREEN: `TicketService.ConfirmResolution(ctx, actor, ticketID)` — scoped read → `isTicketRequester` else `ForbiddenError(ErrMsgNotTicketRequester)` → `t.Transition(closed, "", now)` → stamp actor + `ClosureVia` → one `s.tx.Update` (D3/D5).
- Acceptance: `go test ./...` green; timestamps scenarios (ticket-management delta) covered.
- Commit: `feat(application): requester confirmation closes the resolution`

### 3.3 `RejectResolution` + workflow detachment (RED → GREEN)
- [ ] 3.3 `RejectResolution` + workflow detachment (RED → GREEN)
- RED: `ticket_confirmation_test.go` — pinned reject → `in_progress`, `WorkflowVersionID == nil`, `resolved_at` cleared, `closed_at` empty, audit actor = requester (never `"workflow"`), no workflow step/cursor/answer mutation (workflow-execution delta); manual-ticket reject → plain `in_progress`; agent reopen `resolved → in_progress` keeps the pin (D6, state-machine delta "agent reopen MUST NOT detach").
- GREEN: `TicketService.RejectResolution` — same gate as 3.2 → `t.Transition(in_progress, "", now)` → `t.WorkflowVersionID = nil` → stamp event → ONE `s.tx.Update` (ticket row + audit atomically, D4).
- Acceptance: `go test ./...` green; no workflow_runner/uow changes.
- Commit: `feat(application): requester rejection detaches the workflow`

### 3.4 Comment carve-out on `resolved` (RED → GREEN)
- [ ] 3.4 Comment carve-out on `resolved` (RED → GREEN)
- RED: `internal/application/comment_service_test.go` — requester public comment on own `resolved` OK; requester `internal` visibility rejected (role rule intact); non-requester (any role) rejected, no write; requester-NULL `resolved` rejected for every actor; `closed`/`cancelled` reject everyone (regression); `new`/`in_progress` rows unchanged (regression).
- GREEN: `internal/application/comment_service.go` — resolved carve-out guarded at the application boundary before any store call (comment-timeline delta; HTTP maps to 403).
- Acceptance: `go test ./...` green.
- Commit: `feat(application): only the requester may comment on a resolved ticket`

## Phase 4 — HTTP, templates, goldens (PR 4) — load `ux-ui` skill

### 4.1 Confirmation route + handler (RED → GREEN)
- [ ] 4.1 Confirmation route + handler (RED → GREEN)
- RED: `internal/adapters/http/handlers_tickets_test.go` auth matrix for `POST /tickets/{id}/confirmation` (`decision=confirm|reject`): requester → success (HX fragment per D6 or 303); agent/admin/root → 403 `ErrMsgNotTicketRequester`; unrelated role-`user` → 404; unknown/missing `decision` → 422, no write; unauthenticated → login redirect.
- GREEN: `handlers_tickets.go` — route beside `:58-69`; `confirmation` handler dispatching to `ConfirmResolution`/`RejectResolution`; response via `h.afterMutation(w, r, id, "ticket_detail")`; errors via `h.renderDetailError`.
- Acceptance: `go test ./...` green.
- Commit: `feat(http): requester confirmation endpoint`

### 4.2 `detailData` flags + `allowedNext` filtering (RED → GREEN)
- [ ] 4.2 `detailData` flags + `allowedNext` filtering (RED → GREEN)
- RED: `handlers_tickets_test.go` — requester-owned `resolved`: `CanConfirm` true only for the requester; `CanComment` requester-only; Move-to drops `closed` for every actor, keeps `in_progress` reopen for authorized agents; requester-NULL `resolved`: agent sees `closed` + reopen; all other states unchanged.
- GREEN: `handlers_tickets.go` `detailDataFor` adds `CanConfirm`/`CanComment` (presentation mirror of `isTicketRequester`, D7); `allowedNext` filtered at the call site (function itself stays pure).
- Acceptance: `go test ./...` green; service remains the enforcement point.
- Commit: `feat(http): confirmation flags + requester-conditional Move-to`

### 4.3 Template control + golden regeneration
- [ ] 4.3 Template control + golden regeneration
- Edit: `web/templates/partials/ticket_detail.html` — confirmation control in the State section (one form, `decision=confirm|reject`; confirm primary + reject destructive; `hx-post` → `#ticket-detailswap` per D6 convention); comment form gated on `CanComment`; existing `Closed` flag keeps hiding edit/assignment controls (visual preservation rules from `ux-ui`: no markup drift outside the two flags; lifecycle meta `:112-119` structurally unchanged).
- Regenerate + extend: `go test ./internal/adapters/http/ -run TestGolden -update`; add golden cases — (a) requester-owned `resolved` viewed by requester (confirm/reject + comment form, no Move-to closed), (b) same viewed by agent (reopen only, no comment form, no confirmation control), (c) requester-NULL `resolved` viewed by agent (close + reopen). `state_badge`/`timeline` goldens must not change.
- Acceptance: goldens deterministic; `go test ./...` green; visual diff limited to the new control + comment gating.
- Commit: `feat(web): requester confirmation control + golden cases`

## Phase 5 — E2E + docs (PR 5) — load `tkt-e2e` skill

### 5.1 Rework the existing resolved→closed journey
- [ ] 5.1 Rework the existing resolved→closed journey
- `e2e/tests/ticket-detail.spec.ts:56-97` drives `resolved → closed` via the transition endpoint — now violates the closure gate. Rework the close leg: requester-confirm flow or a requester-NULL ticket (design D8).
- Acceptance: journey green under `npx playwright test` (versioned suite); no coverage rows lost.
- Commit: `test(e2e): align resolved-to-closed journey with closure attribution`

### 5.2 New requester journeys + README coverage
- [ ] 5.2 New requester journeys + README coverage
- Journey 1 (confirm): agent resolves → requester sees confirmation control + comment form → confirms → badge `closed`, `Closed` timestamp with `Resolved` remaining, timeline closure attributed to the requester (not `"workflow"`), Move-to close control gone.
- Journey 2 (reject): `resolve_ticket` run leaves `resolved` → requester rejects → `in_progress`, `resolved_at` cleared from meta, workflow Pending Actions card absent (detached), ticket continues manual (no further workflow step).
- Journey 3 (blocked close): requester-owned `resolved` viewed by agent → Move-to offers reopen but not `closed`; direct `POST /tickets/{id}/transition {to: closed}` denied, error renders via detail-error path, state stays `resolved`.
- Update `e2e/README.md:29,48,50` coverage rows.
- Acceptance: three journeys green; existing journeys green.
- Commit: `test(e2e): requester confirm/reject journeys + blocked-close coverage`

### 5.3 Final verification gate
- [ ] 5.3 Final verification gate
- `go test ./...`; `go build ./...`; `go vet ./...`; `go test -cover ./...` (≥ 75% threshold); `openspec validate --all --strict` (17/17); full Playwright suite green; staticcheck + govulncheck if available locally.
- Acceptance: all green; no commit unless a fix is required (then one fix commit referencing the failing gate).

---

## Scenario coverage matrix (traceability: design.md map ↔ tasks)

| Delta scenarios | Covering tasks |
|---|---|
| state-machine: Valid forward path; Agent cannot close requester-owned | 3.1, 3.2, 5.2 (J1) |
| state-machine: Invalid transition; Terminal cancelled; User role cannot transition; Agent only assigned | 1.1 (regression) + 3.1/3.2 carve-out rows |
| state-machine: Reopen from closed with/without reason; Reopen from resolved | 1.1 (JD fix verification) |
| confirmation closure: confirm closes; already-closed impossible; requester-NULL agent close; workflow terminal closes; pinned reject; manual reject; agent reopen keeps pin | 3.2, 3.1, 2.2 (runner), 3.3 ×3, 5.2 (J1/J2) |
| workflow-execution: resolve from new / no-op / cancelled-rejects / awaiting-confirmation; close_ticket 5 scenarios | 2.2 (regressions + new awaiting-confirmation pin); close_ticket rows regression in 2.1/2.2 |
| workflow-execution: rejected run leaves manual ticket; manual rejection no workflow op | 3.3 |
| audit-log: three paths distinguishable; every closure audited | 1.3/1.4 (persistence), 3.1/3.2 (stamping), 2.1 (workflow convention), 2.2 |
| ticket-management: timestamps ×3 | 3.2 (stamps/keeps), 3.3 (clears), regression in 3.2 |
| ticket-management: detail presentation ×7 | 4.2 (flags), 4.3 (goldens a/b/c + read-only regressions), 5.2 (e2e) |
| role-authorization: 6 carve-out scenarios | 3.2 (confirm/other's/outside-resolved/agents-denied/others-prohibited), 3.1 (requester-NULL close) |
| comment-timeline: 8 scenarios | 3.4 (+ regressions), 4.3 (form visibility) |

## Key Learnings
- The D4 open question resolved during planning: `ApplyWorkflowPlan` already reloads the persisted pin (`workflow_uow.go:161-169`) and `validateMutationPlan` (`:288-290`) fails detached tickets with a typed conflict — the verify-first task (1.2) is evidence-only, and its regression proof is a characterization test (2.2), not new guard code.
- The JD-mandated guard fix is already in the working tree; planning converts it into a land-and-verify task (1.1) instead of re-planning implementation, keeping the tree clean before PR 1.
- Authored size (~1,500–1,700 lines) is driven by test surface (6 capabilities × 55 scenarios), not implementation volume; golden HTML is excluded from authored count per work-unit-commits precedent, matching the archived category-workflows change.
- Strict TDD ordering follows the dependency chain (domain/audit → store/UoW → application → HTTP → e2e) so every RED test has its enforcement point available in the same or an earlier phase.
