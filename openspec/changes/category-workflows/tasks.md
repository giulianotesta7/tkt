# Tasks: Category Workflows — Linear, Published, Pinned

## Review Workload Forecast

| Field | Value |
|-------|-------|
| Estimated changed lines | ~2,900–3,650 authored (additions+deletions) + ~420 generated golden/snapshot lines |
| 400-line budget risk | Accepted at final-PR level |
| Chained PRs recommended | No — maintainer override |
| Commit plan | Preserve PR1 → PR9 work-unit commits with tests and rollback evidence |
| Delivery strategy | exception-ok |
| PR strategy | One direct final PR |

```text
Decision needed before apply: Resolved — one direct final PR selected
Chained PRs recommended: No
Delivery strategy: exception-ok
400-line PR split threshold: Waived by maintainer
```

> Delivery decision superseded: create one direct final PR for the complete `category-workflows` change, regardless of total authored size. Keep coherent work-unit commits, strict TDD evidence, independent semantic verification, isolated rollback boundaries, and bounded native apply attempts. The size waiver removes only PR splitting by line count; it does not permit scope drift or mixed-responsibility implementation attempts.

### Forecast detail (authored vs generated)

| Work unit | Scope | Est. authored Δ | Est. generated | Key files that drive count |
|-----------|-------|-----------------|----------------|----------------------------|
| PR1 — Domain + Migration 0006 | Closed enums, validation, migration DDL + trigger | 350–480 | 0 | `internal/domain/workflow.go`, `workflow_test.go`, `migrations/0006_category_workflows.sql`, `migration_0006_test.go` |
| PR2 — Definition persistence (WorkflowStore) | Optional draft reads, first-mutation upsert, atomic publish/switch, available query | 320–420 | 0 | `adapters/sqlite/workflow_store.go`, `_test.go`, `adapters/sqlite/sqlite.go` |
| PR3 — Definition service (WorkflowService) | Capability-first draft lifecycle, preview read-only, publish orchestration, badges | 300–400 | 0 | `application/workflow_service.go`, `_test.go`, `application/ports.go`, `fakes_test.go` |
| PR4 — Runner planner (WorkflowRunner) | Closed loop/switch, ExpectedPosition, positional decoding, terminal matrices via domain | 380–500 | 0 | `application/workflow_runner.go`, `_test.go`, `application/views.go` |
| PR5 — Create+pin UoW (atomic ticket create) | Create+pin+run all-or-nothing, fixed-plan recheck, initial auto-advance | 320–420 | 0 | `application/ticket_service.go`, `adapters/sqlite/workflow_uow.go`, `workflow_uow_create_test.go` |
| PR6 — Assignment + claim scope | Claim visibility vs strict mutation, deterministic least_loaded query, assignment audit | 320–420 | 0 | `application/policy.go`, `adapters/sqlite/filters.go`, `workflow_uow_assignment_test.go` |
| PR7 — Terminal persistence + timeline | Resolve/close matrices, two-audit close, workflow actor label, answers card | 280–380 | 0 | `adapters/sqlite/workflow_uow_terminal_test.go`, `application/views.go`, `web/templates/partials/timeline.html` |
| PR8 — Builder HTTP/UI | Safe GET, first-mutating POST, preview, publish, vertical builder, HTMX/full-page parity | 340–450 | 0 | `handlers_category_workflows.go`, `_test.go`, `web/templates/pages/category_workflow.html`, `partials/workflow_builder.html` |
| PR9 — Ticket runtime UI + goldens + Playwright | Pending controls, published-only create options, typed answers, goldens, isolated E2E | 380–500 | ~420 | `handlers_tickets.go`, `handlers_tickets_test.go`, `web/templates/partials/workflow_pending.html`, `testdata/*.golden` |
| **Total** | | **~2,900–3,650 authored** | **~420 generated** | Goldens excluded from 400-line budget but included in scope/evidence |

> All estimates count additions+deletions (authored lines = added + deleted, not net). Handwritten table-driven tests, SQLite transaction tests, and handler tests drive >45% of the total; earlier 1,520 estimate undercounted test code. Generated goldens are deterministic fixtures updated only via `-update` after handler assertions pass. Estimates are ranges, not promises.

### Per-slice range and mandatory actual-line check

Each work unit remains measured before apply and commit so native attempt budgets, verification scope, and rollback boundaries stay honest:

```bash
# from the current work-unit base:
git diff --numstat <base>...HEAD | awk '{a+=$1; d+=$2} END {print "authored:", a+d, "added:", a, "deleted:", d}'
git diff --stat <base>...HEAD
gofmt -l .  # must print nothing
```

Actual size no longer forces another PR split. Keep tests with the behavior they verify, use one coherent commit per work unit, and reject scope mixing even when the final PR is large.

### Uncertainty note

Forecast includes handwritten RED/GREEN/TRIANGULATE coverage, SQLite transaction tests, handler parity tests, and generated goldens. The maintainer accepted the final PR review load; honest per-work-unit measurement still governs bounded execution and verification evidence.

---

## Work-unit map

| Work unit | Depends on | One-line outcome | Commit boundary |
| --------- | ---------- | ---------------- | --------------- |
| PR1 S1 | — | Typed closed model validates, migration 0006 applies with no backfill, immutability trigger lives | Cohesive + independently green |
| PR2 S2 | PR1 | Builder persistence works: safe GET (no row), first POST upsert, atomic publish, `ListAvailableCategories` | Cohesive + independently green |
| PR3 S3 | PR2 | WorkflowService enforces `CapManageCategories`, GET never writes, preview read-only, publish valid non-empty | Cohesive + independently green |
| PR4 S4 | PR1 | Runner plans from pinned snapshot: Position 1→0 conflict, raw positional decoding, terminal matrices via `Ticket.Transition` | Cohesive + independently green |
| PR5 S5 | PR1–PR4 | Ticket create pins+starts run atomically; fixed-plan UoW rechecks and applies without dispatch | Cohesive + independently green |
| PR6 S6 | PR5 | Claim visibility (`ScopeAssignedOrClaimable`) read-only; mutation scopes stay strict; deterministic least_loaded | Cohesive + independently green |
| PR7 S7 | PR5–PR6 | Terminal 2-audit close ordered and atomic; workflow actor renders as `Workflow`; answers card | Cohesive + independently green |
| PR8 S8 | PR1–PR3 | Friendly builder at `GET/POST /categories/{id}/workflow`, HTMX/full-page parity, keyboard reorder | Cohesive + independently green |
| PR9 S9 | PR1–PR8 | Ticket runtime pending/answers, published-only options, deterministic goldens, isolated Playwright | Cohesive + independently green |

Each work unit ships tests with behavior and must pass `gofmt -l .`, `go vet ./...`, `go test ./... -count=1 -race`, and `go build ./...` before the next work unit. All commits accumulate into one final PR.

---

## PR1 — Typed closed workflow model + migration 0006

**Outcome:** `internal/domain/workflow.go` defines `StepType`, `AssignmentStrategy`, `FormActor`, `FieldKind`, `WorkflowDefinition`/`WorkflowStep`/`FormField` structs with exact JSON tags; `Validate()` returns ordered `WorkflowValidationIssue{Step, Field, Message}`; `migrations/0006_category_workflows.sql` adds additive tables, checks, and immutability trigger; no draft/version rows are backfilled; legacy NULL-pinned tickets remain readable.

**Files:** `internal/domain/workflow.go` (new), `internal/domain/workflow_test.go` (new), `internal/domain/ticket.go` (pin field `WorkflowVersionID *int64` if not present), `internal/domain/audit.go` (`ActionWorkflowStep`), `internal/domain/errors.go` (`ErrWorkflowPositionConflict`), `internal/adapters/sqlite/migrations/0006_category_workflows.sql` (new), `internal/adapters/sqlite/migration_0006_test.go` (new), `internal/adapters/sqlite/sqlite.go` (no logic change, ensure Migrate picks up 0006).

### Tasks

- [x] **1.1 RED — domain validation contract.** Write `internal/domain/workflow_test.go` table-driven tests that FAIL without implementation: ≥1 step required; reject unknown step type; terminal at-most-one, mutual exclusion `resolve` vs `close`, final-only; `assign_to_desk` desk_id>0, strategy `claim|least_loaded`; `form` actor `requester|assignee`, ≥1 field, non-empty key/label trimmed, keys unique across workflow, `single_select` ≥2 non-empty unique options, options rejected on non-select kinds, `manual_task` instructions non-empty; trim before validate/canonicalize; reject unknown JSON fields on decode; canonical `encoding/json` bytes are byte-equal. Include `TestWorkflow_Validate_Empty`, `TestWorkflow_Validate_TerminalMutualExclusion`, `TestWorkflow_Validate_DuplicateKey`, `TestWorkflow_CanonicalBytesEqualAfterTrim`.
  - *Evidence:* `go test ./internal/domain -run 'TestWorkflow_Validate' -count=1` → FAIL (undefined type / failing cases).

- [x] **1.2 GREEN — implement the closed workflow domain model.** Add closed enums (`StepAssignToDesk`, `StepForm`, `StepManualTask`, `StepResolve`, `StepClose`), `WorkflowDefinition`, `WorkflowStep{Type, AssignToDesk, Form, ManualTask}`, `AssignToDeskStep`, `FormStep`, `ManualTaskStep`, `FormField`, and `WorkflowValidationIssue`; implement the 10 validation rules, canonical JSON, and unknown-field rejection. Keep the model data-only: no step interface, executor registry, ticket pin, persistence error, or workflow audit action in PR1a.
  - *Evidence:* `go test ./internal/domain -run 'TestWorkflow' -count=1` → PASS; `gofmt -l .` empty; `go vet ./internal/domain` clean.

- [x] **1.3 TRIANGULATE — edge refinements.** Add cases: whitespace-only key/label rejected; duplicate key after trimming; `single_select` duplicate option after trimming; `resolve` not last rejected; empty `steps_json` array rejected by domain; republish identical bytes still considered valid (domain does not dedupe). Verify domain does NOT own advancement.
  - *Evidence:* `go test ./internal/domain -count=1 -race` PASS.

- [x] **1.4 REFACTOR — small, no behavior change.** Extract helpers `validateAssign`, `validateForm`, `validateManual`; keep the public surface minimal; leave `Ticket` and `Ticket.Transition` untouched in PR1a.
  - *Evidence:* `go test ./internal/domain -count=1 -race` still PASS.

- [x] **1.5 RED/GREEN — migration 0006 and pin/audit/error integration (PR1b).** Restore the verified PR1b backup; add `ActionWorkflowStep = "workflow_step"` to `audit.go`, `ErrWorkflowPositionConflict` to `errors.go`, and nullable `WorkflowVersionID *int64` to `Ticket`; update migration-version assertions. Write `internal/adapters/sqlite/migration_0006_test.go` using `newTestDB` and add `migrations/0006_category_workflows.sql`. Assert workflow tables/checks, nullable ticket pin FK, immutable-version trigger, no backfill, legacy NULL-pin readability, unchanged FTS, and schema version 0006. Keep the PR1b implementation and handwritten tests below 400 authored lines.
  - *Focused:* `go test ./internal/adapters/sqlite -run 'TestMigration0006' -count=1` PASS.
  - *Broader:* `go test ./... -count=1 -race` PASS; `go vet ./...` PASS; `go build ./...` PASS.

- [x] **1.6 PR1b gates + rollback.** Run `gofmt -l .` (must be empty), `go vet ./...`, `go test ./... -count=1 -race`, and `go build ./...`. Verify PR1b rollback removes migration/integration fields while leaving PR1a domain model intact; existing tickets/categories survive because no backfill occurs. Runtime harness: `N/A — no user-observable route`.
  - *Authored-line check:* `git diff --numstat main...HEAD` authored <400 or split domain vs migration; `gofmt -l .` empty.

**PR1 done when:** domain validation covers all closed-type/terminal/field rules with plain messages; migration is additive, no backfill, trigger enforced, FTS untouched; broader suite green.

---

## PR2 — Definition persistence (WorkflowStore)

**Outcome:** Safe draft reads, first-mutating-POST lazy persistence, atomic publish with current-pointer switch, and available-category queries. No ticket execution.

**Files:** `internal/application/ports.go` (new `WorkflowStore` + `WorkflowRunStore` + `WorkflowUnitOfWork` minimal for this slice or stub), `internal/adapters/sqlite/workflow_store.go` (new), `internal/adapters/sqlite/workflow_store_test.go` (new), `internal/adapters/sqlite/sqlite.go`, `internal/adapters/sqlite/category_store_test.go` (badge cases).

### Tasks

- [x] **2.1 RED — WorkflowStore contract.** Write `workflow_store_test.go` (real `modernc.org/sqlite` via `newTestDB`) cases that FAIL: `GetDraft` on absent row returns `nil, nil` not error and performs no INSERT; `ListSummaries` derives badge `none|Draft|Published vN` correctly (no row→none, draft≠published→Draft, equal→Published vN, GET-only viewing never creates Draft); `UpsertDraft` on no row does `INSERT … ON CONFLICT DO NOTHING` + `UPDATE` in one `BEGIN IMMEDIATE` and stores canonical JSON; `Publish` with empty/invalid draft returns plain issues and creates no version; valid publish inserts `workflow_versions` with `MAX(version_no)+1`, updates `draft_json` and `current_version_id` atomically; republish identical bytes creates new version; `ListAvailableCategories` returns only categories with `current_version_id NOT NULL`; published rows reject UPDATE via trigger.
  - *Focused:* `go test ./internal/adapters/sqlite -run 'TestWorkflowStore' -count=1` → FAIL.

- [x] **2.2 GREEN — implement `workflow_store.go`.** Implement `WorkflowStore` per design: optional draft read, first-mutation upsert, one-transaction publish (validate via domain, recheck desk existence `SELECT 1 FROM desks`), availability query; keep `steps_json`/`draft_json` as canonical bytes; no step dispatch.
  - *Evidence:* `go test ./internal/adapters/sqlite -run 'TestWorkflowStore' -count=1 -race` PASS.

- [x] **2.3 TRIANGULATE — store edge cases.** Concurrent first-POST upsert (two goroutines publishing same category) yields one row, no duplicate; `GetDraft` after `Publish` with equal bytes still shows `Published vN`; deleting a category cascades its workflow data; `FOREIGN KEY` `tickets.workflow_version_id` stays NULL-valid.
  - *Evidence:* `go test ./internal/adapters/sqlite -run 'TestWorkflowStore' -count=1 -race` PASS.

- [x] **2.4 PR2 gates + rollback.** `gofmt -l .` empty, `go vet ./...`, `go test ./... -count=1 -race` (unit + sqlite), `go build ./...`. Rollback: revert `workflow_store.go` + ports additions; PR1 migration stays, no data loss beyond dropping `category_workflows`/`workflow_versions` rows (acceptable dev-only). Runtime harness: `N/A — no user-observable route`.
  - *Authored-line check:* `git diff --numstat main...HEAD` authored <400; `gofmt -l .` empty.

**PR2 done when:** GET never creates row/badge; first POST lazily creates; publish is atomic and rechecks desks; availability filters correctly.

---

## PR3 — Definition service (WorkflowService use cases)

**Outcome:** `WorkflowService` enforces `CapManageCategories` before any read/write; GET never writes; preview is read-only; drafts canonicalized before persist.

**Files:** `internal/application/workflow_service.go` (new), `internal/application/workflow_service_test.go` (new), `internal/application/fakes_test.go`, `internal/application/policy_test.go`, `internal/application/ports.go`.

### Tasks

- [x] **3.1 RED — WorkflowService use cases.** Write `workflow_service_test.go` with fakes: `GetForBuilder` requires `CapManageCategories`, returns in-memory `[]` when no row and does NOT call store write; `SaveDraft`/`AddStep`/`MoveUp`/`RemoveStep` canonicalize submitted draft, then call store upsert; `Preview` validates in memory, returns read-only ordered summary, never writes; `Publish` requires valid non-empty submitted draft, requires `CapManageCategories`, returns plain `[]WorkflowValidationIssue`; publish while draft differs keeps available version active until commit; non-admin `agent`/`user` cannot preview drafts. Use `fakes_test.go` stores.
  - *Focused:* `go test ./internal/application -run 'TestWorkflowService' -count=1` → FAIL.

- [x] **3.2 GREEN + REFACTOR — implement `workflow_service.go`.** Implement `WorkflowService{store, categories, desks}` with capability-first checks (`policy.CanManageCategories`), safe-read path, mutating draft actions that apply closed action to canonical definition then persist, `Preview` no-write, `Publish` orchestration, category summaries with derived badges, `ListAvailableCategories`. Keep historic-version browsing out of scope. Refactor to share canonicalization helper.
  - *Evidence:* `go test ./internal/application -run 'TestWorkflowService' -count=1 -race` PASS; `go test ./internal/adapters/sqlite -count=1 -race` PASS.

- [x] **3.3 PR3 gates + rollback.** `gofmt -l .` empty, `go vet ./...`, `go test ./... -count=1 -race`, `go build ./...`. Rollback: revert `workflow_service.go` + ports additions; broader suite stays green. Runtime harness: `N/A — no user-observable route`.
  - *Authored-line check:* `git diff --numstat main...HEAD` authored <400; `gofmt -l .` empty.

**PR3 done when:** Capability enforced before any read/write; preview is read-only; publish is atomic and rechecks desks; availability filters correctly.

---

## PR4 — WorkflowRunner: closed loop/switch, positional answers, honest position conflict

**Outcome:** Application-owned `WorkflowRunner` loads `WorkflowExecutionSnapshot`, checks one-based `ExpectedPosition` → zero-based cursor, walks pinned snapshot via closed `switch`, decodes raw positional form values against pinned field definitions, produces typed positional JSON array, and emits a concrete `WorkflowMutationPlan` / `CreateTicketWorkflowPlan`. Terminal matrices call `Ticket.Transition` on an in-memory copy; runner never touches SQL.

**Review workload decision:** PR4b historically required a bounded 800-line native exception after two semantically rejected ≤400 candidates. The maintainer has now superseded the PR-level slicing strategy for all remaining work: every coherent work-unit commit accumulates into one direct final PR, regardless of total size. Native apply attempts remain explicitly bounded; strict TDD, complete regression coverage, independent semantic verification, rollback evidence, and no scope creep remain mandatory.

**Files:** `internal/application/workflow_runner.go` (new), `internal/application/workflow_runner_test.go` (new), `internal/application/ports.go` (`WorkflowRunStore`, `WorkflowUnitOfWork` plan structs, `CompleteWorkflowCommand{ExpectedPosition, RawAnswers}`, `RawPositionalValue`), `internal/application/fakes_test.go`, `internal/application/views.go`, `internal/application/views_test.go`.

### Tasks

- [x] **4.1 RED — runner plan shape + position conflict.** Write `workflow_runner_test.go` (fakes) that FAILs: runner converts `ExpectedPosition` 1-based to cursor 0-based and returns typed `ErrWorkflowPositionConflict` on stale/missing/non-positive/mismatched position with no writes; non-terminal step on `resolved|closed|cancelled` rejects before planning; `assign_to_desk[claim]` plans with claimant as person (no desk stored); `least_loaded` plans a fixed assignment request (no SQL yet); `Ticket.Transition` invoked for `new→in_progress` on successful claim, for `resolve`/`close` matrices, stamps only there.
  - *Focused:* `go test ./internal/application -run 'TestWorkflowRunner_Position' -count=1` → FAIL.

- [x] **4.2 GREEN — implement runner skeleton + position + lifecycle guard.** Add port types `RawPositionalValue`, `RawPositionalValues`, `CompleteWorkflowCommand`, plan structs (`WorkflowMutationPlan` with expected persisted facts, optional assignment request, already-decided transitions, audits, cursor/status; `CreateTicketWorkflowPlan`; `WorkflowExecutionResult` data-only), `WorkflowExecutionSnapshot`. Implement `WorkflowRunner.PlanComplete` up to position check, active-run check, load-snapshot, lifecycle guard, and `claim`/`least_loaded` assignment intent; call `domain.Ticket.Transition` for assignment-triggered `new→in_progress`.
  - *Evidence:* `go test ./internal/application -run 'TestWorkflowRunner' -count=1` partial PASS.

- [x] **4.3 RED/GREEN — raw positional typed answer decoding.** Add table tests for positional matrix that FAIL then PASS: `checkbox` absent/empty→false, `on`/`true`→true, any other non-empty rejected; required checkbox false rejected; `short_text`/`long_text` trimmed, required blank rejected; `single_select` empty→empty (optional), non-empty must exactly equal canonical option, required empty rejected; unknown position, duplicate position, non-numeric/negative, extra position beyond pinned count, more than one value for same position all rejected; one value persisted per pinned field including false/empty; persisted `answers_json` is positional typed array `["api-01", true, "eu-west-1"]` not string map.
  - *Focused:* `go test ./internal/application -run 'TestWorkflowRunner_FormDecoding' -count=1 -race` PASS.

- [x] **4.4 RED/GREEN — terminal matrices + auto-advance loop.** Add cases: `resolve_ticket` from `new`/`in_progress` plans `Transition(resolved)` + workflow audit + completed run; from `resolved`/`closed` plans completed run no-op; from `cancelled` rejects; `close_ticket` from `new`/`in_progress` plans two ordered transitions `resolved` then `closed` + two audits; from `resolved` one audit; from `closed` no-op; from `cancelled` rejects. Verify loop: plan includes human completion + all immediately following automatic `least_loaded`/`resolve`/`close` without requiring extra user action; `manual_task` stops with one pending; cursor==len(steps) completes run without state change; `WorkflowExecutionResult` carries no functions/registrations.
  - *Evidence:* `go test ./internal/application -run 'TestWorkflowRunner_Terminal' -count=1 -race` PASS.

- [x] **4.5 TRIANGULATE — actor gates + no-impersonation.** Cases: `form[requester]` only `RequesterUserID==actor`; `form[assignee]`/`manual_task` only `Ticket.UserID==actor`; admin/root cannot complete `form[requester]` even if assignee; must audited self-assign first via existing `TicketService.Assign` path (runner rejects without that); `agent` claim to other person rejected. Verify runner derives actor from snapshot, never from submitted `RawAnswers` keys/types.
  - *Evidence:* `go test ./internal/application -run TestWorkflowRunner -count=1 -race` PASS (63 subtests, strict ID equality, claim always actor, no impersonation, reason gates — `TestWorkflowRunner_ActorAndClaim` + `TestWorkflowRunner_OrderedOperations` cover the actor/reason/identity matrix within the PR4b operation contract)

- [x] **4.6 REFACTOR + PR4 gates + rollback.** Extract `decodePositionalAnswers(snapshot, raw)` helper; keep `switch` closed (no registry). Run `gofmt -l .` empty, `go vet ./...`, `go test ./... -count=1 -race`, `go build ./...`. Rollback: revert `workflow_runner.go` + port plan types; PR1–PR3 remain green; no DB writes yet so no data rollback needed. Runtime harness: `N/A — no user-observable route`.
  - *Authored-line check:* pending final PR4c completion. The combined 396-line candidate and narrower 400-line corrective candidate both passed executable gates but failed independent semantic verification; accepted PR4b reached 689 authored lines. The final PR has no line-based split threshold, while PR4c still requires an explicit bounded native objective, strict TDD, semantic verification, rollback evidence, and all remaining PR4 gates.

**PR4 done when:** Honest `ExpectedPosition` conflict is typed 422; positional decoding matches design table exactly; all actor/terminal matrices go through `Ticket.Transition`; plan is data-only and finite.

---

## PR5 — Atomic create+pin+run (fixed-plan UoW core)

**Outcome:** `TicketService.Create` atomically creates ticket, pins `workflow_version_id`, creates `ticket_workflow_runs`, and applies initial automatic advancement; `WorkflowUnitOfWork.ApplyWorkflowPlan`/`CreateTicketWithRun` do `BEGIN IMMEDIATE`, reload/recheck every precondition, execute only fixed writes/CAS/audits. No assignment-scope or terminal audit rendering yet.

**Files:** `internal/application/ticket_service.go`, `internal/application/ticket_service_test.go`, `internal/adapters/sqlite/ticket_store.go`, `internal/adapters/sqlite/ticket_store_test.go`, `internal/adapters/sqlite/workflow_uow.go` (new), `internal/adapters/sqlite/workflow_uow_create_test.go` (new), `internal/application/fakes_test.go`.

### Tasks

- [x] **5.1 RED — TicketService create+pin+run.** Write `ticket_service_test.go` (fakes) that FAILs: `Create` with `category_id` lacking published version returns `422 ValidationError{Field:"category", Message:"category is not available for new tickets — publish its workflow first"}` and creates nothing; with published version, service asks `WorkflowRunner` to plan initial automatic advancement before submitting `CreateTicketWithRun` plan; plan rechecks version remains current in UoW; automatic `least_loaded` failure rolls back entire creation (no ticket/row/audit/run); in-flight ticket keeps pinned version after new publish. Legacy NULL-pin tickets still readable via existing scopes.
  - *Batch A note (application orchestration only):* RED + GREEN + race PASS for the service contract in `ticket_service_test.go`/fakes (7 focused tests). The SQLite `WorkflowUnitOfWork` atomicity proof (task 5.3 wording) is **unimplemented/unproven in Batch A** — persistence atomicity remains Batch B's responsibility.
  - *Batch A second-attempt correction:* PASS under independent verification. `Clone` preserves every nil-vs-empty shape, canonical normalization is byte-identical to `8350e5a`, `TicketService` captures the untrusted published workflow exactly once before deriving independent runner/persistence snapshots, and only the honest full-Batch-A rollback is claimed.
  - *Focused:* `go test ./internal/application -run 'TestTicketService_CreateWithWorkflow' -count=1 -race` PASS.

- [x] **5.2 GREEN — implement create path.** Modify `ticket_service.go` to use `WorkflowRunner` + `WorkflowUnitOfWork.CreateTicketWithRun` instead of `TicketUnitOfWork.Create` when workflow exists; keep `TicketService.Assign` guard for claim reassignment reason; ensure `tickets.workflow_version_id` pinned. Keep `TicketUnitOfWork` for manual mutations.
  - *Batch A note:* `Create` routes through `createWithWorkflow` (workflow-wired constructor `NewTicketServiceWithWorkflowCreate`) when the workflow ports are wired; the plain `NewTicketService` keeps the legacy path so `cmd/server` and the HTTP harness compile unchanged until Batch B lands the SQLite `WorkflowVersionStore`/`WorkflowUnitOfWork` adapters. No SQLite implementation exists yet — wiring is fake-served in Batch A.
  - *Evidence:* `go test ./internal/application -run 'TestTicketService_CreateWithWorkflow' -count=1 -race` PASS; `go test ./internal/application -count=1 -race` PASS (all existing tests preserved).

- [x] **5.3 RED/GREEN — UoW fixed-plan application (core).** Write `workflow_uow_create_test.go` (real SQLite) that FAILs then passes: `CreateTicketWithRun` does one `BEGIN IMMEDIATE`, re-reads category/current version, rechecks any active agent+ assignee precondition, inserts ticket with `workflow_version_id`, appends created audit, inserts run `active` at cursor 0, applies runner-planned initial auto steps; any failure rolls back all writes. `ApplyWorkflowPlan` reloads ticket/run/snapshot/users/memberships per plan's expected facts, rejects mismatch with `ErrWorkflowPositionConflict` and no writes, then performs fixed writes/CAS/audits for form/manual/assignment cases. Assert adapter never dispatches by step type — it only rechecks/applies the plan.
  - *Focused:* `go test ./internal/adapters/sqlite -run 'TestWorkflowUoW_Create' -count=1 -race` PASS.
  - *FINAL PR5 operation-grammar correction:* prefix-only group parsing (human group + contiguous automatic tail), resolve = one transition / close exact matrix, terminal already-state no-op, new-state claim MUST carry new->in_progress (in_progress MUST NOT), monotonic timestamps, claim audits carry no note, and a REQUIRED non-nil Result with exact ticket/run identities. `go test ./internal/adapters/sqlite -run 'TestWorkflowUoW|TestWorkflowVersionStore' -count=1` PASS (58 funcs → all PASS).

- [x] **5.4 TRIANGULATE + REFACTOR.** Edge: same plan retried after stale conflict still rechecks correctly; basic form/manual audit ordering stays stable. Refactor UoW to share `recheckSnapshot` + `applyCursorCAS` helpers; no callback/generic transaction API.
  - *Evidence:* independent gate PASS: same immutable plan stale→zero writes→cursor-only fix→single success; `recheckSnapshot`/`applyCursorCAS` shared by Create/Apply; exact form/manual ordering and terminal/no-op matrices pass under race.

- [x] **5.5 PR5 gates + rollback.** `gofmt -l .` empty, `go vet ./...`, `go test ./... -count=1 -race`, `go build ./...`. Rollback: restore all PR5 tracked paths to `8350e5a`, remove the five PR5 new Go files, and thereby restore production + HTTP harness wiring to the legacy constructor; PR1–PR4 remain intact, legacy NULL-pin tickets remain readable, and test-database workflow rows require no production data rollback. Runtime harness: production `POST /tickets` composition is verified through `httptest` + real SQLite; no separate live-server E2E is required in PR5.
  - *Authored-line check:* 6,397 authored Go lines against `8350e5a`; final-PR size does not force a split under `exception-ok`. `gofmt -l .` remains empty.

**PR5 done when:** Create+pin+run is all-or-nothing; fixed-plan writes survive stale recheck; adapter applies plan without step dispatch; FTS untouched.

---

## PR6 — Assignment atomicity + claim scope + deterministic least_loaded

**Outcome:** `least_loaded` selection pool is ALL active `agent|admin|root` users joined through `desk_members dm WHERE dm.desk_id=?`; global ticket load is `LEFT JOIN tickets t ON t.user_id=u.id AND t.state IN ('new','in_progress')`; grouped by `u.id`; ordered `COUNT(t.id) ASC, u.id ASC`; no category predicate. Claim membership check remains actor-specific and separate (actor is active and `desk_members(desk_id, actor)` exists). Pending `claim` leaves `new` unchanged; successful assignment is person-only and atomic with state+audit.

**Files:** `internal/application/policy.go`, `internal/application/policy_test.go`, `internal/application/ticket_service.go`, `internal/application/comment_service.go` (scope check), `internal/adapters/sqlite/ticket_store.go`, `internal/adapters/sqlite/workflow_uow.go`, `internal/adapters/sqlite/workflow_uow_assignment_test.go` (new), `internal/adapters/sqlite/filters.go`, `internal/adapters/sqlite/search_store_test.go`, `internal/application/views.go`.

### Tasks

- [x] **6.1 RED — assignment atomicity + deterministic least_loaded + scope.** Write `workflow_uow_assignment_test.go` + `policy_test.go` cases that FAIL then PASS:
  - Pending `claim` leaves `new` unchanged, no write.
  - Successful `claim` by eligible `agent+` desk member persists person `tickets.user_id`, and when `new` also `Transition(in_progress)` + both audits atomically; `in_progress` assignment creates no redundant transition; same-person assignment creates no false field-change audit (but may still create state audit if `new`); A→B reassignment without reason rejected.
  - `least_loaded` selection pool is ALL active `agent|admin|root` users joined through `desk_members dm WHERE dm.desk_id=?`; global load is `LEFT JOIN tickets t ON t.user_id=u.id AND t.state IN ('new','in_progress')`; `GROUP BY u.id`; order `COUNT(t.id) ASC, u.id ASC`; no category predicate; executes inside same `BEGIN IMMEDIATE` so concurrent assignments see committed load; claim membership check remains actor-specific and separate.
  - Claim races: two claimers render same action, first commit advances cursor, second gets `ErrWorkflowPositionConflict` 422 with no writes.
  - Scope: `ScopeAssignedOrClaimable` read query returns assigned **or** active run at pinned `assign_to_desk[claim]` whose desk contains actor (`json_extract(pinned steps, cursor).desk_id` + `desk_members`); strict mutation helpers (`CanEdit`, `CanComment`, `CanTransition`, generic assignment) retain `ScopeAssigned`/`ScopeAll` only.
  - *Focused:* `go test ./internal/adapters/sqlite -run 'TestWorkflowUoW_Assignment|TestScope' -count=1 -race` PASS; `go test ./internal/application -run 'TestPolicy_Scope' -count=1 -race` PASS.

- [x] **6.2 TRIANGULATE + REFACTOR.** Edge: least-loaded with empty desk → typed error, entire plan rolls back, cursor unchanged; `manual_task` completion on `resolved` rejects; same plan retried after membership fix succeeds. Refactor UoW to share deterministic least_loaded query builder; no callback/generic transaction API.
  - *Evidence:* independent gate PASS: exact empty-desk Apply rollback/retry, resolved manual-task validation, membership repair retry, and single shared deterministic `leastLoadedAssigneeTx`; three focused tests pass under race. Full repository race remains task 6.3.

- [x] **6.3 PR6 gates + rollback.** `gofmt -l .` empty, `go vet ./...`, `go test ./... -count=1 -race`, `go build ./...`. Rollback: restore every tracked PR6 code and OpenSpec artifact path to `eaca426` and remove `workflow_uow_assignment_test.go`; PR1–PR5 remain, NULL-pinned tickets stay readable, and no production-data rollback is required. Runtime harness: no new route/template is introduced; read visibility and mutation authorization are exercised through policy + real SQLite integration tests.
  - *Authored-line check:* measure the complete coherent PR6 Go delta against `eaca426`, including the untracked assignment test; final-PR `exception-ok` means size does not force a split. `gofmt -l .` remains empty.

**PR6 done when:** Selection pool includes ALL desk members (not only actor), global `new|in_progress` load ordered `COUNT ASC, u.id ASC` with no category filter, inside `BEGIN IMMEDIATE`; claim visibility does not grant edit; FTS untouched.

---

## PR7 — Terminal persistence + audit/timeline

**Outcome:** `resolve`/`close` matrices persisted via application-decided transitions; `close` from open writes two ordered `workflow` audits; requester sees typed answers via `Workflow responses` card; `workflow` actor renders as `Workflow`.

**Files:** `internal/adapters/sqlite/workflow_uow.go`, `internal/adapters/sqlite/workflow_uow_terminal_test.go` (new), `internal/application/workflow_runner.go`, `internal/application/workflow_runner_test.go`, `internal/application/views.go`, `internal/application/event_summary_internal_test.go`, `web/templates/partials/timeline.html`, `internal/application/fakes_test.go`.

### Tasks

- [x] **7.1 RED/GREEN — terminal persistence + audit/timeline.** Write `workflow_uow_terminal_test.go` + view tests that FAIL then PASS: `resolve` from `new`/`in_progress` writes `workflow` actor (`actor='workflow', actor_user_id=NULL, action='transition', field='state'`) + completed run atomically; from `resolved`/`closed` completes run with no false transition; from `cancelled` rejects with no writes. `close` from `new`/`in_progress` writes two ordered `workflow` audits (`new→resolved`, `resolved→closed`) + completed run; from `resolved` one audit; from `closed` no-op; from `cancelled` rejects. Add `domain.ActionWorkflowStep` audit for form/manual completions (no answer content). `TimelineItem.ActorLabel` renders `workflow` as `Workflow` without user lookup; completed forms decoded as typed positional arrays zipped with pinned fields appear in `Workflow responses` card for every authorized reader; FTS `tickets_fts` still excludes answers; `close`'s two audits are ordered and atomic.
  - *Focused:* `go test ./internal/adapters/sqlite -run 'TestWorkflowUoW_Terminal' -count=1 -race` PASS; `go test ./internal/application -run 'TestViews_WorkflowTimeline' -count=1 -race` PASS.

- [x] **7.2 TRIANGULATE + REFACTOR.** Edge: `manual_task` completion on `resolved` rejects; same plan retried after stale conflict handled. Refactor to share terminal audit helpers; keep FTS exclusion explicit.
  - *Evidence:* `go test ./... -count=1 -race` PASS.

- [x] **7.3 PR7 gates + rollback.** `gofmt -l .` empty, `go vet ./...`, `go test ./... -count=1 -race`, `go build ./...`. Rollback: revert terminal branch + timeline partial; previous PRs remain green. Runtime harness: seeded-detail Playwright MCP PASS at desktop/mobile with isolated temporary SQLite and loopback-only server.
  - *Authored-line check:* report exact PR7 churn; the maintainer-approved final-PR `size:exception` replaces the stale `<400` slice cap. Current measured candidate: 923 authored lines; `gofmt -l .` empty.

**PR7 done when:** Terminal audits are ordered and workflow-attributed; completed forms render correctly; FTS untouched; stale-state rejection has no writes.

---

## PR8 — Friendly builder HTTP/HTMX/full-page UI

**Outcome:** Vertical numbered `<ol>` builder at `GET/POST /categories/{id}/workflow`, published-only badge derived from `draft_json` vs `current_version_id`, HTMX/full-page parity, keyboard reorder, preview read-only, publish valid non-empty.

**Files:** `internal/adapters/http/handlers_category_workflows.go` (new), `internal/adapters/http/handlers_category_workflows_test.go` (new), `internal/adapters/http/handlers_categories.go`, `web/templates/pages/category_workflow.html` (new), `web/templates/pages/categories_index.html`, `web/templates/partials/workflow_builder.html` (new), `web/templates/partials/styles.html`, `cmd/server/main.go` (route wiring).

### Tasks

- [ ] **8.1 RED — builder HTTP contract (safe GET, HTMX/full-page parity).** Write `handlers_category_workflows_test.go` with `net/http/httptest` + real SQLite that FAILs: `GET /categories/{id}/workflow` requires `CapManageCategories` (302/403 otherwise), renders in-memory `[]` when no row and does NOT create `category_workflows` row nor `Draft` badge; `POST` with `action=save|add_step|change_type|add_field|remove_field|move_up|move_down|remove_step` carries complete ordered draft, persists canonical draft via first-mutation upsert in one `BEGIN IMMEDIATE`; `action=preview` renders read-only ordered summary of submitted draft with no write; `action=publish` with empty/invalid draft renders 422 inline errors and creates no version; valid publish persists draft+version+switch atomically; category index shows `Configure workflow` + derived badge (none/Draft/Published vN), GET-only viewing shows none. HTMX `HX-Request: true` swaps `#workflow-builder` (`workflow_builder` partial), non-HTMX renders full `category_workflow` page or 303 after success; both render same 422 errors. Keyboard reorder: real `<button>`s, `focus` target on moved step's control after HTMX move, `aria-live` status announces new position, `role="alert"` on errors.
  - *Focused:* `go test ./internal/adapters/http -run 'TestCategoryWorkflowBuilder' -count=1` → FAIL.

- [ ] **8.2 GREEN — implement builder handler + templates.** Add `handlers_category_workflows.go` with `GET` safe-read, `POST` closed `action` switch, raw positional draft decoding rejecting missing/duplicate numeric positions before array construction, canonicalization via domain, `WorkflowService` delegation, HTMX vs full-page branching; wire routes in `cmd/server/main.go`; add `category_workflow.html`, `workflow_builder.html`, update `categories_index.html` badge derivation, shared `styles.html`. No top-level nav item.
  - *Evidence:* `go test ./internal/adapters/http -run 'TestCategoryWorkflowBuilder' -count=1 -race` PASS.

- [ ] **8.3 REFACTOR — template + handler cleanup.** Extract `parseBuilderDraft(r)` helper; ensure `workflow_builder.html` reuses existing `tkt` visual language (`<ol>` numbered checklist, contextual fields only, terminal rows explain auto-final); no canvas/nodes/connectors.
  - *Evidence:* `go test ./internal/adapters/http -count=1 -race` still PASS.

- [ ] **8.4 PR8 gates + rollback.** `gofmt -l .` empty, `go vet ./...`, `go test ./... -count=1 -race`, `go build ./...` — all green. Rollback: revert `handlers_category_workflows.go`, `category_workflow.html`/`workflow_builder.html`, route wiring; previous PRs remain green; builder route disappears, ticket create filter falls back to all categories (existing pinned tickets remain readable).
  - *Authored-line check:* `git diff --numstat main...HEAD` authored 340–450 range → if ≥400 split handler vs template/test seam before review; `gofmt -l .` empty.

**PR8 done when:** Builder is friendly (numbered `<ol>`, contextual fields, keyboard reorder with focus + live region), HTMX/full-page parity, published-only options not yet enforced on ticket create, safe GET with no badge side effect.

---

## PR9 — Ticket runtime HTTP/UI + goldens + isolated Playwright

**Outcome:** Published-only category options on `GET /tickets/new` and `POST /tickets` validation, honest `POST /tickets/{id}/workflow/steps/{position}/complete`, pending controls gated by persisted actor, completed forms read-only, deterministic goldens, isolated local Playwright journeys with cleanup evidence.

**Files:** `internal/adapters/http/handlers_tickets.go`, `internal/adapters/http/handlers_tickets_test.go`, `internal/adapters/http/handlers_detail_test.go`, `internal/adapters/http/harness_test.go`, `internal/adapters/http/golden_test.go`, `web/templates/partials/ticket_form.html`, `web/templates/partials/ticket_detail.html`, `web/templates/partials/workflow_pending.html` (new), `web/templates/partials/workflow_answers.html` (new), `web/templates/partials/timeline.html`, `cmd/server/main.go`, `internal/adapters/http/testdata/*.golden`.

### Tasks

- [ ] **9.1 RED/GREEN — ticket HTTP runtime + published-only options.** Write `handlers_tickets_test.go`/`handlers_detail_test.go` cases that FAIL then PASS:
  - `GET /tickets/new` `CategoryStore` options filter via `WorkflowStore.ListAvailableCategories` (unavailable categories absent for all roles); legacy unpinned tickets still listable.
  - `POST /tickets` with unavailable `category_id` re-renders 422 with `category: category is not available for new tickets — publish its workflow first`.
  - `POST /tickets/{id}/workflow/steps/{position}/complete` is the only completion route; `{position}` is one-based, maps to zero-based cursor; stale/missing/non-positive/mismatched returns typed `ErrWorkflowPositionConflict` → 422 with no writes; `claim` posts no assignee ID (only reason); forms post `answer_<zeroPos>` raw values, unknown/duplicate/extra/ambiguous rejected before write; `manual_task` posts no metadata; `least_loaded`/`resolve`/`close` render no button (auto-advanced synchronously).
  - Pending Actions card above timeline appears only for active run, renders control only when persisted actor predicate passes; HTMX success returns `ticket_detail` for `#ticket-detail outerHTML`, non-HTMX 303; errors re-render same page/fragment.
  - Completed forms render read-only `Workflow responses` card zipped with pinned fields; no `Workflow vN` pin exposed to requesters; no version browser.
  - *Focused:* `go test ./internal/adapters/http -run 'TestTicketWorkflowRuntime' -count=1 -race` PASS.

- [ ] **9.2 TRIANGULATE — forged + stale + XSS edge.** Cases: non-requester posting to `form[requester]` denied; non-assignee posting to `form[assignee]`/`manual_task` denied; forged `ExpectedPosition` on already-advanced cursor → 422 with no audit; `answer_0` XSS payload stored as typed string and escaped in template (no raw HTML). Verify `422` messages are plain English per-step (`Step 2: choose a desk`).
  - *Evidence:* `go test ./internal/adapters/http -run 'TestTicketWorkflow_Authz|TestTicketWorkflow_Stale' -count=1 -race` PASS.

- [ ] **9.3 Goldens — deterministic regeneration.** After handler assertions pass, run `go test ./internal/adapters/http -update -count=1` to regenerate only affected goldens, inspect diff (`git diff --stat internal/adapters/http/testdata/*.golden`), then rerun without `-update` and assert stable: `go test ./internal/adapters/http -count=1 -race`. Keep goldens small and deterministic; isolate service/handler assertions before regenerating; never mask authz defect with golden churn.
  - *Focused:* `go test ./internal/adapters/http -count=1 -race` PASS.
  - *Broader:* `go test ./... -count=1 -race` PASS; `go vet ./...` PASS; `gofmt -l .` empty.

- [ ] **9.4 REFACTOR — template + handler cleanup.** Extract `parsePositionalAnswers(r, fieldCount)` helper; ensure `workflow_pending.html`/`workflow_answers.html` reuse existing `tkt` visual language (`<ol>` numbered checklist, contextual fields only, terminal rows explain auto-final); no canvas/nodes/connectors.
  - *Evidence:* `go test ./internal/adapters/http -count=1 -race` still PASS.

- [ ] **9.5 Final isolated Playwright E2E (project UX skill).** Run only after all lower gates pass. Follow `ux-ui-e2e-validation` skill strictly:
  - **Isolation:** `TKT_DB_PATH=$(mktemp -d)/e2e.db`, `TKT_LISTEN=127.0.0.1:<free-port>` (find free port, never touch dev/prod DB), `go run ./cmd/server` as loopback-only.
  - **Poll `/healthz` bounded timeout; record logs without credentials.**
  - **Journeys (smallest meaningful set, desktop + mobile viewports):**
    1. *Builder lifecycle:* admin GETs unconfigured builder → no `Draft` badge, no row; first mutating `add_step` → draft appears; contextual fields; keyboard reorder (Tab + Enter/Space on Up/Down) → focus stays on moved step's control, `aria-live` announces; invalid publish (0 steps / empty key / duplicate key) → inline `role="alert"` error, no version; preview → read-only ordered summary, published stays active, no extra write; valid publish → `Published vN` badge, category appears in `GET /tickets/new`.
    2. *Requester create + pin:* requester creates ticket in published category → pins version; completes `form[requester]` with canonical mappings (text trimmed, checkbox absent/empty/`on`/`true`, select option); after newer draft/version published, in-flight ticket still advances on pinned version.
    3. *Desk claim + assignee work:* eligible `agent` claims `Network` desk task → detail shows `Assigned to <person>`, state `new→in_progress` with workflow audit; current assignee completes `manual_task` + `form[assignee]`; requester sees typed answers read-only; workflow-attributed `resolve` shows `Workflow` actor.
    4. *Stale position + least_loaded + offboarding close:* stale `POST …/steps/{position}/complete` with old position → 422, no audit/cursor change; `least_loaded` picks global-lowest `new|in_progress` count (ALL active `agent|admin|root` members of desk, `LEFT JOIN tickets t ON t.user_id=u.id AND t.state IN ('new','in_progress')`, `GROUP BY u.id`, `ORDER BY COUNT(t.id) ASC, u.id ASC`, no category filter), tie by lower `user.id`, inside same transaction; multi-desk offboarding `HR→IT→Finance→close_ticket` ends directly in `close` with two ordered `workflow` audits (`resolved` then `closed`) and no preceding `resolve_ticket` step.
  - **Per journey verify:** page/URL, semantic `<ol>` content, outcome, keyboard-only flow + visible focus, console errors, failed requests, horizontal overflow (layout check).
  - **Evidence:** screenshots as evidence only, Playwright `aria` snapshots for actions.
  - **Cleanup:** stop only launched PID, delete temp DB + sidecars, logs, screenshots unless retention requested; verify cleanup with `ls` on temp paths.
  - **Gates:** Browser MCP unavailable → `BLOCKED`; server not ready → `BLOCKED` with sanitized logs; excluded-only diff → `SKIP`. Never claim PASS after failure, never kill unrelated processes, never replace lower-level tests.
  - *Command:* `go run ./cmd/server` isolation harness + Playwright MCP journeys as above; no external Playwright test framework install.

- [ ] **9.6 PR9 gates + rollback.** `gofmt -l .` empty, `go vet ./...`, `go test ./... -count=1 -race`, `go build ./...` — all green with goldens stable. Rollback: revert `handlers_tickets.go`/`handlers_detail` changes, `workflow_pending.html`/`workflow_answers.html`, and golden snapshots; previous PRs (PR1–PR8) remain green; builder route disappears, ticket create filter falls back to all categories (existing tickets with pin remain readable).
  - *Authored-line check:* `git diff --numstat main...HEAD` authored 380–500 range → if ≥400 split runtime handler vs golden/Playwright seam; `gofmt -l .` empty (goldens excluded from authored budget but included in review scope).

**PR9 done when:** Published-only options enforced, pending controls gated by persisted actor, honest position conflict is 422, goldens deterministic, Playwright journeys PASS with cleanup evidence.

---

## Global gates (run after every PR, and before each PR merge)

- [ ] **G1 — strict TDD evidence per behavioral unit.** Every behavioral unit above records RED → GREEN → TRIANGULATE where needed → REFACTOR, with focused test command/evidence, broader gate, harness applicability, and rollback boundary kept with the behavior (not deferred to a later test-only phase).
  - *Focused:* `go test ./internal/domain -count=1 -race` and `go test ./internal/application -count=1 -race` and `go test ./internal/adapters/sqlite -count=1 -race` and `go test ./internal/adapters/http -count=1 -race` as relevant per PR.
  - *Broader:* `go test ./... -count=1 -race` PASS; `go vet ./...` PASS; `gofmt -l .` empty; `go build ./...` PASS.
  - *Goldens:* deterministic; updated only via `-update` after handler assertions, then rerun without flag to prove stability.
  - *Harness:* Playwright only in PR9; earlier PRs record `N/A — no user-observable route` honestly.

- [ ] **G2 — four journeys proof.** After PR9, `simple routing`, `new server request`, `AWS access`, `offboarding ending in close` are all green via the Playwright journeys above plus the underlying unit/SQLite/handler tests (no journey relies solely on screenshots).

- [ ] **G3 — no design drift.** No graph/branching, no executor registry, no graph tables, no `workflow_steps` normalized table, no task-instance rows, no synthetic `users` workflow row, no draft-on-GET, no FTS over answers, no historic version browser.

---

## Execution order

```text
PR1 (domain + 0006) → PR2 (store) → PR3 (service) → PR4 (runner) → PR5 (create+pin UoW) → PR6 (assignment+claim scope) → PR7 (terminal+tline) → PR8 (builder HTTP/UI) → PR9 (ticket runtime + goldens + Playwright)
```

Each PR is independently reviewable (clear start/finish, verification, rollback) and must be green before the next starts. PR5 and PR6 are kept separate because combining them would exceed 400 authored lines and blur application-decision vs SQLite-recheck vs deterministic least_loaded ownership; PR4 and PR5 are separate for the same reason (runner planner vs UoW recheck). If any PR's actual authored line count (additions+deletions) reaches ~360–400, split further at the test-with-behavior seam noted above before review.

## References

- `openspec/changes/category-workflows/proposal.md` — product rules D1–D12, four journeys, scope/non-goals
- `openspec/changes/category-workflows/specs/category-workflows/spec.md` — draft/publish, closed model, validation, builder, adoption, FTS
- `openspec/changes/category-workflows/specs/ticket-workflow-execution/spec.md` — pinning, person-only routing, forms/manual, resolve/close matrices, representative journeys
- `openspec/changes/category-workflows/design.md` — S1–S9 boundaries, safe-read/lazy POST, plan/UoW contract, positional decoding, least_loaded SQL (`desk_members dm WHERE dm.desk_id=?` + `LEFT JOIN tickets t ON t.user_id=u.id AND t.state IN ('new','in_progress')` + `GROUP BY u.id` + `ORDER BY COUNT(t.id) ASC, u.id ASC` + no category predicate), `ScopeAssignedOrClaimable`, audit rendering, HTTP routes
- `openspec/config.yaml` — `strict_tdd: true`, `go test ./...`, `gofmt -l .` empty, `go vet`
- Project skills: `gentle-ai` (harness), `go-testing` (table-driven, `t.TempDir`, golden determinism), `work-unit-commits` (tests with behavior), `ux-ui-e2e-validation` (isolated Playwright, temp DB/port, cleanup)
