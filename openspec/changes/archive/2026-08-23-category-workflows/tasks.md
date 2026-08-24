# Tasks: Category Workflows — Linear, Published, Pinned

## Review Workload Forecast

| Field | Value |
|-------|-------|
| Estimated changed lines | ~5,400–5,800 authored cumulative (additions+deletions): completed PR1–PR10 actuals ≈4,524 measured at the PR10 close + Amendment 2 ~880–1,280; generated golden/snapshot lines excluded from the authored forecast |
| 400-line budget risk | Accepted at final-PR level |
| Chained PRs recommended | No — maintainer override |
| Commit plan | Preserve PR1 → PR10 work-unit commits; Amendment 2 ships as WA → WB → WC coherent commits with tests and rollback evidence |
| Delivery strategy | exception-ok |
| PR strategy | One direct final PR |

```text
Decision needed before apply: No
Chained PRs recommended: No
Chain strategy: size-exception
400-line budget risk: High
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
>
> Follow-up appended (PR10): step-indexed audit correlation + merged timeline (migration 0008, inline answer/instruction rendering, responses-card removal), est. ~250–400 additional authored lines plus any affected goldens. It ships inside the same ONE final PR under `delivery_strategy=exception-ok`; see the PR10 section below.

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

- [x] **8.1 RED — builder HTTP contract (safe GET, HTMX/full-page parity).** Write `handlers_category_workflows_test.go` with `net/http/httptest` + real SQLite that FAILs: `GET /categories/{id}/workflow` requires `CapManageCategories` (302/403 otherwise), renders in-memory `[]` when no row and does NOT create `category_workflows` row nor `Draft` badge; `POST` with `action=save|add_step|change_type|add_field|remove_field|move_up|move_down|remove_step` carries complete ordered draft, persists canonical draft via first-mutation upsert in one `BEGIN IMMEDIATE`; `action=preview` renders read-only ordered summary of submitted draft with no write; `action=publish` with empty/invalid draft renders 422 inline errors and creates no version; valid publish persists draft+version+switch atomically; category index shows `Configure workflow` + derived badge (none/Draft/Published vN), GET-only viewing shows none. HTMX `HX-Request: true` swaps `#workflow-builder` (`workflow_builder` partial), non-HTMX renders full `category_workflow` page or 303 after success; both render same 422 errors. Keyboard reorder: real `<button>`s, `focus` target on moved step's control after HTMX move, `aria-live` status announces new position, `role="alert"` on errors.
  - *Focused:* `go test ./internal/adapters/http -run 'TestCategoryWorkflowBuilder' -count=1` → FAIL.

- [x] **8.2 GREEN — implement builder handler + templates.** Add `handlers_category_workflows.go` with `GET` safe-read, `POST` closed `action` switch, raw positional draft decoding rejecting missing/duplicate numeric positions before array construction, canonicalization via domain, `WorkflowService` delegation, HTMX vs full-page branching; wire routes in `cmd/server/main.go`; add `category_workflow.html`, `workflow_builder.html`, update `categories_index.html` badge derivation, shared `styles.html`. No top-level nav item.
  - *Evidence:* `go test ./internal/adapters/http -run 'TestCategoryWorkflowBuilder' -count=1 -race` PASS.

- [x] **8.3 REFACTOR — template + handler cleanup.** Extract `parseBuilderDraft(r)` helper; ensure `workflow_builder.html` reuses existing `tkt` visual language (`<ol>` numbered checklist, contextual fields only, terminal rows explain auto-final); no canvas/nodes/connectors.
  - *Evidence:* `go test ./internal/adapters/http -count=1 -race` still PASS.

- [x] **8.4 PR8 gates + rollback.** `gofmt -l .` empty, `go vet ./...`, `go test ./... -count=1 -race`, `go build ./...` — all green. Rollback: revert `handlers_category_workflows.go`, `category_workflow.html`/`workflow_builder.html`, route wiring; previous PRs remain green; builder route disappears, ticket create filter falls back to all categories (existing pinned tickets remain readable).
  - *Authored-line check:* record the exact candidate additions + deletions against the PR7 baseline, including untracked files and excluding only `desks-ux-polish`; the accepted `delivery_strategy=exception-ok` keeps PR8 as one coherent builder work unit even when authored churn is ≥400. `gofmt -l .` must be empty.

**PR8 done when:** Builder is friendly (numbered `<ol>`, contextual fields, keyboard reorder with focus + live region), HTMX/full-page parity, published-only options not yet enforced on ticket create, safe GET with no badge side effect.

---

## PR9 — Ticket runtime HTTP/UI + goldens + isolated Playwright

**Outcome:** Published-only category options on `GET /tickets/new` and `POST /tickets` validation, honest `POST /tickets/{id}/workflow/steps/{position}/complete`, pending controls gated by persisted actor, completed forms read-only, deterministic goldens, isolated local Playwright journeys with cleanup evidence.

**Files:** `internal/adapters/http/handlers_tickets.go`, `internal/adapters/http/handlers_tickets_test.go`, `internal/adapters/http/handlers_detail_test.go`, `internal/adapters/http/harness_test.go`, `internal/adapters/http/golden_test.go`, `web/templates/partials/ticket_form.html`, `web/templates/partials/ticket_detail.html`, `web/templates/partials/workflow_pending.html` (new), `web/templates/partials/workflow_answers.html` (new), `web/templates/partials/timeline.html`, `cmd/server/main.go`, `internal/adapters/http/testdata/*.golden`.

### Tasks

- [x] **9.1 RED/GREEN — ticket HTTP runtime + published-only options.** Write `handlers_tickets_test.go`/`handlers_detail_test.go` cases that FAIL then PASS:
  - `GET /tickets/new` `CategoryStore` options filter via `WorkflowStore.ListAvailableCategories` (unavailable categories absent for all roles); legacy unpinned tickets still listable.
  - `POST /tickets` with unavailable `category_id` re-renders 422 with `category: category is not available for new tickets — publish its workflow first`.
  - `POST /tickets/{id}/workflow/steps/{position}/complete` is the only completion route; `{position}` is one-based, maps to zero-based cursor; stale/missing/non-positive/mismatched returns typed `ErrWorkflowPositionConflict` → 422 with no writes; `claim` posts no assignee ID (only reason); forms post `answer_<zeroPos>` raw values, unknown/duplicate/extra/ambiguous rejected before write; `manual_task` posts no metadata; `least_loaded`/`resolve`/`close` render no button (auto-advanced synchronously).
  - Pending Actions card above timeline appears only for active run, renders control only when persisted actor predicate passes; HTMX success returns `ticket_detail` for `#ticket-detail outerHTML`, non-HTMX 303; errors re-render same page/fragment.
  - Completed forms render read-only `Workflow responses` card zipped with pinned fields; no `Workflow vN` pin exposed to requesters; no version browser.
  - *Focused:* `go test ./internal/adapters/http -run 'TestTicketWorkflowRuntime' -count=1 -race` PASS.

- [x] **9.2 TRIANGULATE — forged + stale + XSS edge.** Cases: non-requester posting to `form[requester]` denied; non-assignee posting to `form[assignee]`/`manual_task` denied; forged `ExpectedPosition` on already-advanced cursor → 422 with no audit; `answer_0` XSS payload stored as typed string and escaped in template (no raw HTML). Verify `422` messages are plain English per-step (`Step 2: choose a desk`).
  - *Evidence:* `go test ./internal/adapters/http -run 'TestTicketWorkflow_Authz|TestTicketWorkflow_Stale' -count=1 -race` PASS.

- [x] **9.3 Goldens — deterministic regeneration.** After handler assertions pass, run `go test ./internal/adapters/http -update -count=1` to regenerate only affected goldens, inspect diff (`git diff --stat internal/adapters/http/testdata/*.golden`), then rerun without `-update` and assert stable: `go test ./internal/adapters/http -count=1 -race`. Keep goldens small and deterministic; isolate service/handler assertions before regenerating; never mask authz defect with golden churn.
  - *Focused:* `go test ./internal/adapters/http -count=1 -race` PASS.
  - *Broader:* `go test ./... -count=1 -race` PASS; `go vet ./...` PASS; `gofmt -l .` empty.

- [x] **9.4 REFACTOR — template + handler cleanup.** Extract `parsePositionalAnswers(r, fieldCount)` helper; ensure `workflow_pending.html`/`workflow_answers.html` reuse existing `tkt` visual language (`<ol>` numbered checklist, contextual fields only, terminal rows explain auto-final); no canvas/nodes/connectors.
  - *Evidence:* `go test ./internal/adapters/http -count=1 -race` still PASS.

- [x] **9.5 Final isolated Playwright E2E (project UX skill).** Run only after all lower gates pass. Follow `ux-ui-e2e-validation` skill strictly:
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

- [x] **9.6 PR9 gates + rollback.** `gofmt -l .` empty, `go vet ./...`, `go test ./... -count=1 -race`, `go build ./...` — all green with goldens stable. Rollback: revert the PR9 ticket handler/wiring/runner/store/port/detail changes, remove the new runtime/authz tests and Pending Actions partial, and restore the PR8 versions of the answers/builder templates and builder focus test. PR1–PR8 remain green; the PR8 builder route remains available, while PR9's ticket completion route/pending controls disappear and ticket creation falls back to all categories (existing pinned tickets remain readable). No golden rollback is required because PR9 changed zero snapshots.
  - *Authored-line check:* record exact additions + deletions against the committed PR8 baseline, including untracked PR9 files and excluding only `desks-ux-polish`; the accepted `delivery_strategy=exception-ok` keeps the ticket runtime, its actor/security tests, and Playwright evidence as one coherent work unit even when authored churn is ≥400. Goldens remain in complete scope but are reported separately; `gofmt -l .` must be empty.

**PR9 done when:** Published-only options enforced, pending controls gated by persisted actor, honest position conflict is 422, goldens deterministic, Playwright journeys PASS with cleanup evidence.

---

## PR10 — Step-indexed audit correlation + merged timeline (follow-up)

**Outcome:** Migration `0008_audit_event_step_index.sql` adds nullable `audit_events.step_index` (no backfill); `AuditEvent.StepIndex *int` is persisted on semantic completion/assignment operations with their sealed zero-based step index, while transition/non-flow/legacy rows stay NULL. The view joins each semantic event to its pinned definition/form response only by that exact persisted index — never by timestamp or order inference. The single newest-first ticket activity timeline contains comments, assignments, every completed category-flow step, and state transitions; the separate ticket-facing `Workflow responses` card and its standalone partial include are removed. A form completion event renders its pinned submitted field labels/values inline as an escaped definition list inside its own timeline item (answers remain only in `ticket_form_answers`, never duplicated into audit note/FTS). A manual completion event renders its contextual pinned instruction. Corrupt/missing context degrades to the safe summary alone with no fabrication; legacy `workflow_step` rows with NULL context remain `Completed step`. Automatic events omit actor text and no ticket-facing copy or actor label mentions `workflow`; admin builder terminology is unchanged. Preserved throughout: comments-before-events same-second tie behavior, internal comment visibility, exact timestamps, human actor attribution, escaping, pinned immutable labels, legacy readability. Delivery remains ONE final PR under `delivery_strategy=exception-ok` — no artificial line cap.

**Files:** `internal/adapters/sqlite/migrations/0008_audit_event_step_index.sql` (new), `internal/adapters/sqlite/migration_0008_test.go` (new), `internal/domain/audit.go`, `internal/adapters/sqlite/workflow_uow.go`, `internal/adapters/sqlite/ticket_store.go` / audit persistence paths, `internal/application/views.go`, `web/templates/partials/timeline.html`, `web/templates/partials/ticket_detail.html`, `web/templates/partials/workflow_pending.html`, `web/templates/partials/workflow_answers.html` (include removed), focused HTTP/view tests, affected goldens.

### Tasks

- [x] **10.1 RED/GREEN — migration 0008 + step-index persistence.** Write `migration_0008_test.go` + UoW/store tests that FAIL first: nullable `step_index` column exists with schema version 0008 and no backfill; semantic form/manual completion and assignment audits persist their sealed zero-based step index atomically with the plan; transition audits, non-flow audits, and pre-0008 rows keep NULL; `AuditEvent.StepIndex *int` round-trips nil-safe.
  - *Focused:* `go test ./internal/adapters/sqlite -run 'TestMigration0008|TestWorkflowUoW.*StepIndex' -count=1 -race` PASS.
- [x] **10.2 RED/GREEN — view join + merged timeline integration.** View tests then template/handler tests that FAIL first: the timeline item for a form completion renders its pinned labels/values inline (escaped `<dl>`) joined strictly by persisted `step_index` against the pinned snapshot and `ticket_form_answers`; manual completion items render the contextual pinned instruction; the standalone responses partial include is removed from ticket detail; automatic events render no actor text; human events keep attributed actor names; comments-before-events same-second tie behavior and internal comment visibility are unchanged; timestamps and escaping unchanged.
  - *Focused:* `go test ./internal/application -run 'TestViews' -count=1 -race` and `go test ./internal/adapters/http -run 'TestTicketWorkflowRuntime|TestWorkflowStepTimeline' -count=1 -race` PASS.
- [x] **10.3 TRIANGULATE — security, legacy, and degradation edges.** Corrupt/out-of-range/missing step index → summary-only rendering, no fabricated labels/values/instructions; legacy NULL-context `workflow_step` rows still read `Completed step`; XSS payloads in pinned labels and submitted values stay escaped; answers never enter FTS or audit notes; no timestamp/order-based correlation exists (identical-timestamp case); no ticket-facing copy contains `workflow` (assert over rendered detail including pending automatic copy); admin builder terminology untouched.
  - *Evidence:* focused table-driven cases pass under `-race`.
- [x] **10.4 REFACTOR + gates + rollback evidence.** Share the step-context resolution helper across view/template paths; remove dead responses-partial code paths without drive-by restyling. Record workload (`git diff --numstat` authored vs generated) and rollback boundary (restore tracked paths to the PR10 base, drop migration 0008 artifacts; forward-only migration noted honestly). Run `gofmt -l .` empty, `go vet ./...`, `go build ./...`.
  - *Authored-line check:* report exact additions+deletions excluding only `desks-ux-polish`; `exception-ok` keeps PR10 coherent regardless of size.
- [x] **10.5 Isolated Playwright journeys.** After all lower gates pass: temp SQLite DB, loopback-only server, bounded `/healthz` poll; verify requester sees assignee answers inline in the timeline (desktop + mobile), manual instruction visible on its event, no standalone responses card, no `Workflow` actor label anywhere ticket-facing, keyboard focus/console/overflow checks; cleanup with PID/temp-artifact evidence. Browser MCP absence → BLOCKED.
- [x] **10.6 Final full race after last executable correction.** Exactly one completed `go test ./... -count=1 -race` run after the last code/template/golden change, plus golden stability rerun if goldens were touched; record command output as the closing gate. The first invocation was invalidated when the 240-second subagent watchdog aborted it before completion; the maintainer authorized one replacement after raising the watchdog to 10 minutes. No further rerun is permitted.
  - *Broader:* one completed replacement `go test ./... -count=1 -race` MUST PASS post-final-correction; preserve the aborted invocation as failed procedural evidence.

**PR10 done when:** Step-index correlation replaces timestamp/order inference everywhere; one merged timeline renders completed steps inline; the responses card is gone; automatic actor text is omitted with no ticket-facing `workflow` wording; legacy/degraded/corrupt contexts render safely; all gates and rollback evidence recorded.

---

## Global gates (run after every PR, and before each PR merge)

- [x] **G1 — strict TDD evidence per behavioral unit.** Every behavioral unit above records RED → GREEN → TRIANGULATE where needed → REFACTOR, with focused test command/evidence, broader gate, harness applicability, and rollback boundary kept with the behavior (not deferred to a later test-only phase).
  - *Focused:* `go test ./internal/domain -count=1 -race` and `go test ./internal/application -count=1 -race` and `go test ./internal/adapters/sqlite -count=1 -race` and `go test ./internal/adapters/http -count=1 -race` as relevant per PR.
  - *Broader:* `go test ./... -count=1 -race` PASS; `go vet ./...` PASS; `gofmt -l .` empty; `go build ./...` PASS.
  - *Goldens:* deterministic; updated only via `-update` after handler assertions, then rerun without flag to prove stability.
  - *Harness:* Playwright only in PR9; earlier PRs record `N/A — no user-observable route` honestly.

- [x] **G2 — four journeys proof.** After PR9, `simple routing`, `new server request`, `AWS access`, `offboarding ending in close` are all green via the Playwright journeys above plus the underlying unit/SQLite/handler tests (no journey relies solely on screenshots).

- [x] **G3 — no design drift.** No graph/branching, no executor registry, no graph tables, no `workflow_steps` normalized table, no task-instance rows, no synthetic `users` workflow row, no draft-on-GET, no FTS over answers, no historic version browser.

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

---

## Amendment 2 — Unassigned-only creation, pinned manual solutions, presentation cleanup (follow-up)

**Outcome:** On top of the completed PR1–PR10 baseline, inside the SAME one final PR under `delivery_strategy=exception-ok`: ticket creation becomes strictly unassigned — presence-based rejection of `user_id`/`assignee_id`, selector removed for every role, `CreateTicketInput.UserID` deleted; pending cards lead with the pinned manual instruction with no ordered-list numbering and no generic `Mark the current task as complete.` copy anywhere ticket-facing; manual completions accept an OPTIONAL solution persisted in `ticket_manual_solutions` (migration `0009`) atomically with completion, cursor, and audit; the merged timeline renders that solution as escaped plain text inside its completion event only when non-empty, with the approved restrained `dl/dt/dd` treatment. Solutions never enter `audit_events.note`/`reason`, comments, or `tickets_fts`.

Strict TDD stays mandatory (`strict_tdd: true`). Each unit follows RED → GREEN → TRIANGULATE → REFACTOR with tests kept beside the production seam they verify. All boxes below start unchecked; nothing is pre-marked complete. Execution order: **WA → WB → WC**; each unit compiles and tests independently before the next starts.

### Workload forecast (Amendment 2 increment; authored additions+deletions, generated goldens excluded)

| Unit | Scope | Est. authored Δ | Split seam if measured >400 |
|------|-------|-----------------|------------------------------|
| WA | Solution persistence: migration 0009, ports, runner stamping, UoW insert, context read, view enrichment | ~340–480 | Detach WA.5–WA.6 (context read + view enrichment) with their response-store/view tests |
| WB | Handlers/presentation: create contract, solution extraction + bound, pending/timeline rendering, goldens | ~380–540 | Detach WB.1–WB.3 (create contract) from WB.4–WB.6 (completion + presentation) |
| WC | Styling/regression: approved tokens, detail goldens, Playwright journeys, closing race | ~160–260 | n/a |
| **Total** | | **~880–1,280** | Cumulative final PR ≈5,400–5,800 authored incl. PR1–PR10 actuals (~4,524 measured) |

The design's nine-boundary discipline carries over verbatim: each unit stays below 400 authored changed lines or splits at the named seam rather than deferring tests; no new architectural layers, services, or public abstractions; every unit measured before commit (`git diff --numstat`, excluding only `desks-ux-polish`).

### WA — Solution persistence (RED → GREEN → TRIANGULATE → REFACTOR)

**Outcome:** An optional trimmed manual-task solution reaches `ticket_manual_solutions` in the same `BEGIN IMMEDIATE` unit as its completion audit and cursor CAS, is readable ONLY through the pinned-context seam by the exact persisted step index, and enriches `TimelineItem.StepSolution` as data — with atomicity, legacy degradation, and audit/comment/FTS non-membership proven by tests living next to each seam.

**Files:** `internal/adapters/sqlite/migrations/0009_ticket_manual_solutions.sql` (new), `internal/adapters/sqlite/migration_0009_test.go` (new), `internal/application/ports.go`, `internal/application/fakes_test.go`, `internal/application/workflow_runner.go`, `internal/application/workflow_runner_test.go`, `internal/adapters/sqlite/workflow_uow.go`, `internal/adapters/sqlite/workflow_uow_solution_test.go` (new), `internal/adapters/sqlite/workflow_response_store.go` + `workflow_response_store_test.go`, `internal/application/views.go`, `internal/application/views_test.go`.

#### Tasks

- [x] **WA.1 RED/GREEN — migration `0009_ticket_manual_solutions.sql` + test.** Table keyed `PRIMARY KEY(ticket_id, step_index)`; `ticket_id → ticket_workflow_runs(id) ON DELETE CASCADE`; `step_index INTEGER NOT NULL CHECK(step_index >= 0)`; `solution TEXT NOT NULL CHECK(length(solution) <= 2000)` mirroring the 2,000-character transport bound as defense in depth; `created_by_user_id INTEGER NOT NULL REFERENCES users(id)`; `created_at TEXT NOT NULL`. Forward-only additive DDL recorded by the existing immediate migration transaction; schema version 0009; NO backfill (pre-amendment manual completions simply have no row); migrations 0006–0008 bytes untouched. Tests assert column shape/checks/FK behavior, version row, no-backfill, and that an already-migrated dev DB gains only this table.
  - *Focused:* `go test ./internal/adapters/sqlite -run 'TestMigration0009' -count=1 -race` PASS.

- [x] **WA.2 RED/GREEN — port surface.** Add `CompleteWorkflowCommand.Solution` (trimmed by the HTTP layer; whitespace-only arrives empty); stamp `Solution` on the sealed `WorkflowStepOperation{StepIndex, Audit, Solution}` — the operation-group grammar stays EXACTLY `[WorkflowStep]`, no new group kind; add `WorkflowStepContext.Solution string`. Update application fakes; no other command/plan fields change.
  - *Focused:* `go test ./internal/application -run 'TestWorkflowRunner' -count=1` FAIL-first on the missing fields, PASS after implementation.

- [x] **WA.3 RED/GREEN — runner manual-branch stamping + contradiction rejection.** `PlanComplete`'s manual branch stamps the submitted solution onto the sealed op; absent/whitespace-only completes normally with an empty op solution; a FORM-step operation carrying a non-empty solution is a plan contradiction rejected before any write (typed error, zero mutations); actor authority unchanged — current assignee only, admin/root only via audited self-assign.
  - *Focused:* `go test ./internal/application -run 'TestWorkflowRunner_Solution|TestWorkflowRunner_FormDecoding' -count=1 -race` PASS.

- [x] **WA.4 RED/GREEN — UoW conditional insert reusing audit facts + atomic rollback.** `applyWorkflowOperations` inserts into `ticket_manual_solutions` ONLY when `op.Solution != ""`, reusing the operation's audit actor-user-id/created-at facts so completion + cursor CAS + audit + solution commit or roll back together in the one `BEGIN IMMEDIATE`. Injected-failure tests prove neither a partial solution nor a completion/cursor/audit remnant survives rollback; empty-solution completions persist no row.
  - *Focused:* `go test ./internal/adapters/sqlite -run 'TestWorkflowUoW.*Solution' -count=1 -race` PASS.

- [x] **WA.5 RED/GREEN — pinned-context read by exact persisted step index.** `workflowResponseStore.WorkflowStepContext` resolves `Solution` ONLY in the manual branch, joining the stored row by the event's exact persisted step index against the immutable pinned definition it already loads. A missing row — no solution submitted, or a legacy pre-`0009` completion — yields an empty `Solution`, never a fabricated placeholder. No other code path queries this table.
  - *Focused:* `go test ./internal/adapters/sqlite -run 'TestWorkflowStepContext' -count=1 -race` PASS.

- [x] **WA.6 RED/GREEN — view enrichment.** The existing enrichment pass copies the stored solution into the new data-only `TimelineItem.StepSolution` alongside `StepInstruction`; corrupt/missing context still degrades to the safe summary alone. Template rendering intentionally lands in WB.6.
  - *Focused:* `go test ./internal/application -run 'TestViews' -count=1 -race` PASS.

- [x] **WA.7 TRIANGULATE — non-membership + boundary edges.** With a distinctive marker string in a stored solution: assert absence from `audit_events.note` AND `audit_events.reason`, absence from comments, and absence from every `tickets_fts` indexed document; a 2,000-character solution is accepted while 2,001 is rejected (CHECK mirror here; transport bound in WB.4); a concurrent duplicate manual completion receives the typed position conflict and exactly one solution row exists.
  - *Focused:* `go test ./internal/adapters/sqlite ./internal/application -run 'Solution' -count=1 -race` PASS.

- [x] **WA.8 REFACTOR + gates + rollback.** Share the conditional-insert helper across form/manual groups only if duplication is real (no callback/generic transaction API). Run `gofmt -l .` (empty), `go vet ./...`, `go build ./...`, focused package races; measure authored churn against the PR10 close. Rollback boundary: drop migration-0009 artifacts, revert ports/runner/UoW/response-store/views deltas plus their tests; PR1–PR10 remain byte-for-byte green; forward-only policy documented for locally migrated dev DBs.
  - *Authored-line check:* `gofmt -l .` empty; report exact additions+deletions.

**WA done when:** the solution persists atomically with its completion, is reachable only through the pinned-context seam, degrades safely for legacy/missing rows, is provably absent from audit notes/reason, comments, and FTS, and `TimelineItem.StepSolution` is populated data-only.

### WB — Handlers/presentation (RED → GREEN → TRIANGULATE → REFACTOR)

**Outcome:** Creation binds no assignee and loudly rejects assignee-carrying requests for every role with zero writes; completion accepts the optional bounded solution; the pending card leads with the pinned instruction without numbering or generic copy; the timeline shows the escaped solution inside its event only when written; affected goldens refreshed through one `-update` cycle then proven stable without it.

**Files:** `internal/adapters/http/handlers_tickets.go`, `internal/adapters/http/handlers_tickets_test.go`, `internal/application/ticket_service.go`, `internal/application/ticket_service_test.go`, `internal/adapters/http/harness_test.go`, `web/templates/partials/ticket_form.html`, `web/templates/partials/workflow_pending.html`, `web/templates/partials/timeline.html`, `internal/adapters/http/testdata/tickets_new.golden`, `testdata/ticket_form.golden`, `testdata/tickets_show.golden`, `testdata/timeline.golden` (affected goldens only).

#### Tasks

- [x] **WB.1 RED — presence-based assignee rejection contract.** Handler tests over the exhaustive role × parameter matrix — `user|agent|admin|root` × `user_id|assignee_id`, each parameter both populated AND empty (stale cached forms submit empties) — that FAIL first: every combination returns the typed `&domain.ValidationError{Field: "assignee", Message: "tickets are created unassigned — assignment happens later through the category flow"}` as 422 through the existing `renderCreateError` path with ZERO ticket/pin/run/audit rows persisted; no normalization, no silent drop, no hidden direct-assignment path.
  - *Evidence:* landed by the interrupted prior worker; verified this run — 4 roles × {`user_id`,`assignee_id`} × {empty, valid staff id, unknown id} all return the typed 422 message with ZERO tickets/runs/audit rows; Precedence + PositiveControl pass. `go test ./internal/adapters/http -run 'Unassigned|Assignee|Solution|Pending|Timeline|Workflow' -count=1 -race` PASS.

- [x] **WB.2 GREEN — handler rejection + `CreateTicketInput.UserID` removal.** The presence check runs before any binding/validation; `CreateTicketInput.UserID` is REMOVED from `internal/application/ticket_service.go` so creation stamps an empty assignee structurally — no application path can smuggle a creation-time assignee. Fix compile fallout by migrating harness seeds that previously created assigned tickets via `Create` to the audited `TicketService.Assign` call. Detail-page audited self-assign plumbing stays untouched.
  - *Evidence:* `CreateTicketInput` has no UserID (ticket_service.go:80); harness seeds migrated to audited `Assign`; `go test ./internal/application -run 'TestTicketService_Create' -count=1` PASS and focused HTTP race PASS this run.

- [x] **WB.3 GREEN — create-form selector removal for all roles.** Remove the `{{if .CanAssign}}` "Assigned user" selector block from `web/templates/partials/ticket_form.html`, plus `ticketFormData.CanAssign` and its `CapAssignTicket` computation in `newForm`/`renderCreateError`. A render-level assertion proves no assignee control exists for any role while detail-page assignment UI is unaffected.
  - *Evidence:* selector block removed from `ticket_form.html`; per-role render probe + detail-page assign control intact; `go test ./internal/adapters/http -run 'Unassigned|TestRenderNewTicketForm' -count=1` PASS.

- [x] **WB.4 RED/GREEN — completion solution extraction + 2,000-char bound.** `completeWorkflow` reads the optional `solution` field, trims surrounding whitespace, treats whitespace-only as absent, and rejects a trimmed value above 2,000 characters with typed `ValidationError{Field: "solution"}` → 422 BEFORE planning (zero writes). Forged `answer_*`/`assignee_id`/`user_id` keys on a completion keep their existing rejections; a completion without a non-empty solution stores nothing and renders no solution block.
  - *Evidence:* `TestCompleteWorkflow_SolutionBound` covers 2001-reject-zero-writes, 2000-boundary trimmed store, whitespace-only-absent, claim+solution contradiction, forged-key rejections; PASS in the focused race this run.

- [x] **WB.5 RED/GREEN — pending card leads with pinned instruction.** `pendingFor` + `web/templates/partials/workflow_pending.html`: ordered-list numbering removed from the Pending Actions card everywhere ticket-facing; a page-level assertion proves the literal `Mark the current task as complete.` never renders. A pending manual task leads with a short neutral lead-in followed by the step's pinned INSTRUCTION verbatim and escaped, read from the execution snapshot `pendingFor` ALREADY loads (the same pinned definition the runner plans against — never the live draft), with the optional `solution` textarea labeled `Solution (optional)` and the unchanged role-gated submit button. Other step kinds keep contextual pinned fields/actions; GET rendering stays read-only.
  - *Evidence:* pending card leads with escaped pinned instruction from the pinned snapshot (pinned-not-draft subtest), no numbering/generic copy, `Solution (optional)` textarea, GET strictly read-only; `TestPendingActions_Presentation` PASS in the focused race this run.

- [x] **WB.6 RED/GREEN — timeline renders solution inside event only when non-empty.** `web/templates/partials/timeline.html` renders `TimelineItem.StepSolution` inside the manual completion event ONLY when non-empty, as escaped ordinary body text attributed/timestamped by the existing completion event; empty/legacy completions render the instruction alone with no empty solution block or placeholder. Assertions lock newest-first ordering, comments-before-events ties, human actor attribution, and the no-ticket-facing-`workflow`-wording rule.
  - *Evidence:* escaped solution renders inside its event only when non-empty; unsolved twin renders no placeholder. This run found one genuine test defect (RED): with the fixed harness clock the seeded comment TIES with the completion event and the preserved comments-before-events rule correctly put it above, breaking the unconditional strict assertion deterministically. Fixed minimally in the test: backdate the seeded comment (-1h raw SQL) so newest-first is proven, plus an explicit same-second tie lock on the unsolved twin (comment above event). No production change. Focused race PASS after fix.

- [x] **WB.7 Goldens — one deterministic `-update` cycle.** Only after all WB handler/view assertions pass: run `go test ./internal/adapters/http -update -count=1` exactly ONCE; inspect `git diff --stat internal/adapters/http/testdata/*.golden` confirming only intended surfaces moved (`tickets_new`, `ticket_form`, `tickets_show`, `timeline` and any full-page dependents); rerun WITHOUT `-update` and require stability. Golden churn never masks an authz or escaping defect.
  - *Focused:* `go test ./internal/adapters/http -count=1 -race` PASS with stable goldens.
  - *Evidence:* ONE `-update` run → ok; `git diff --stat internal/adapters/http/testdata/` = exactly `tickets_new.golden` + `ticket_form.golden`, 1 deletion each (selector remnant); no-`-update` rerun under `-race` → ok 326.979s.

- [x] **WB.8 Gates + rollback.** `gofmt -l .` empty, `go vet ./...`, `go build ./...`, focused HTTP/application races; measure authored churn against the WA close. Rollback boundary: revert handler/service/template/test deltas and restore pre-cycle goldens; PR1–PR10 + WA remain green; rejected-creation behavior returns to its prior contract with no data rollback.
  - *Authored-line check:* `gofmt -l .` empty; report exact additions+deletions.
  - *Evidence:* `gofmt -l .` empty (after formatting two prior-worker test files); `go vet ./...` PASS; `go build ./...` PASS; `git diff --check` clean; focused HTTP/application races PASS; WB tracked authored Δ ≈587 (untracked cumulative 1281 across runtime/authz/timeline tests + pending partial; goldens 2 generated deletions excluded). Full evidence: `/tmp/tkt-pr10-a2-wb-evidence.txt` sha256 645c5c8dcb2b58a11c59edd36cb870d44bc3d422ff9b0643fdf6f966e26bd76e.

**WB done when:** every role × parameter rejection is proven with zero writes, creation is structurally unassigned, the solution bound fires before planning, pending cards lead with pinned instructions free of numbering/generic copy, the timeline shows solutions escaped only when written, and goldens are stable without `-update`.

### WC — Styling/regression + closing gate

**Outcome:** The approved visual contract lands as scoped token additions in `styles.html`; full-page detail goldens lock the treatment; isolated Playwright journeys prove all four amendment journeys end-to-end; ONE final post-correction repository race closes the change alongside the static gates.

**Files:** `web/templates/partials/styles.html`, `internal/adapters/http/testdata/ticket_detail.golden` + `testdata/tickets_show.golden` (full-page detail), Playwright journey evidence under `/tmp` with cleanup proof.

#### Tasks

- [x] **WC.1 RED/GREEN — approved style tokens in `styles.html`.** Scoped additions implementing the APPROVED visual contract `design/category-workflow-refinements.op`: hairline-separated `dl` pairs (`#DDE2EA` 1px), fixed-width muted `dt` (104px desktop, 13px `#7A8391`) whose long `dd` values wrap (`13px/1.45 #252B34`) inside the entry without overflowing it, single-column dt-above-dd stacking below 640px, plainly visible keyboard focus (2px `#315EFF` outline, 2px offset) with preserved contrast — and NOTHING else: no broad restyle, no drive-by template churn.
  - *Focused:* `go test ./internal/adapters/http -count=1 -race` PASS (full-page bytes refreshed only within the WB.7 `-update` cycle).

- [x] **WC.2 Full-page detail goldens.** Detail-page snapshots lock the semantic `dl.workflow-responses` grouping, escaping of every label/value/solution/instruction, solution-inside-event-only-when-non-empty, absence of placeholders, and absence of ticket-facing `workflow` wording; verified stable in the no-`-update` rerun.
  - *Focused:* `go test ./internal/adapters/http -run 'TestGolden' -count=1` PASS.

- [x] **WC.3 Isolated Playwright journeys (project UX skill).** Unique temp SQLite DB + loopback-only free port + bounded `/healthz` poll; desktop and mobile viewports. Journeys: (1) create-without-assignee — requester creates with NO assignee control and a forged `assignee_id` submission shows the visible typed 422 with no ticket created; (2) pending manual card leads with the pinned instruction — no numbering, no generic copy — and completes with an optional solution; (3) solution round-trip rendered escaped only-when-written (compare solved vs unsolved tickets; hostile markup stays literal text); (4) completed-form readability at 390px — dt above dd, no horizontal overflow, plainly visible keyboard focus. Verify console/network cleanliness, keyboard-only flow, URLs, HTMX and ordinary form behavior. Cleanup stops only the launched PID and deletes temp DB/WAL/SHM/logs/screenshots with `ls` proof. Browser MCP unavailable ⇒ BLOCKED — never silently skipped or replaced with screenshots.
  - *Command:* `go run ./cmd/server` isolation harness + Playwright MCP journeys as specified; no external Playwright framework install.

- [x] **WC.4 CLOSING GATE — one final post-correction full-repository race + static gates.** After the LAST executable correction (code/template/CSS/golden): exactly ONE completed `go test ./... -count=1 -race` PASS recorded as the closing gate, plus `gofmt -l .` empty, `go vet ./...`, `go build ./...`, `git diff --check` clean, and a golden-stability confirmation if goldens were touched; preserve command output as evidence; measure final authored churn (`git diff --numstat`, excluding only `desks-ux-polish`). Rollback boundary: restore tracked amendment paths and drop migration-0009 artifacts first in dependency order (forward-only migration noted honestly); PR1–PR10 baseline remains intact and readable.
  - *Broader:* one completed `go test ./... -count=1 -race` MUST PASS post-final-correction; no further invocation after it passes.

**WC done when:** the approved tokens render the `dl` treatment accessibly at desktop and 390px, detail goldens are stable, all four isolated Playwright journeys PASS with isolation/cleanup evidence, and the single closing repository race plus static gates are recorded.

**Amendment 2 done when (spec trace):** create rejects `user_id`/`assignee_id` for every role with zero writes and renders no selector (`ticket-management` delta); pending leads with the pinned instruction sans numbering/generic copy and GET stays read-only (`ticket-management` delta); solutions persist atomically tied to their sealed step index, stay out of notes/reason/comments/FTS, and render escaped only when written (`ticket-workflow-execution` delta); timeline/attribution and merged-timeline presentation rules hold including 390px readability (`audit-log` delta); all gates and measured churn recorded.

---

## Amendment 3 — UI coherence and reasonless pinned-desk claim

**Status:** planned only. This amendment preserves the recorded Amendment 1/2 implementation evidence above; its boxes are intentionally unchecked. Delivery is explicitly deferred until implementation evidence exists. Do not select, create, or describe a PR boundary as part of this amendment.

### Review Workload Forecast

| Field | Value |
|-------|-------|
| Estimated authored change | ~900–1,400 lines plus affected goldens; estimate must be remeasured per completed work unit |
| Dominant risks | authorization/concurrency correctness, server-rendered responsive interaction, HTMX/native-control preservation |
| 400-line budget risk | High; units below are behavioral seams, not delivery commitments |
| Delivery decision | Deferred until after implementation and focused verification |
| Generated artifacts | Goldens remain part of complete validation identity but excluded from authored estimate |

```text
Decision needed before apply: No
Chained PRs recommended: Deferred
Delivery/PR selection: Deferred until implementation evidence
400-line budget risk: High
```

### Work-unit map

| Unit | Depends on | Outcome | Rollback boundary |
|------|------------|---------|-------------------|
| A — Categories + workflow selects | Amendment 2 baseline | Native workflow selects and category index are tkt-consistent, semantic, responsive, and preserve HTMX/autosave | Category/builder templates, styles, handler/view tests, and their goldens only |
| B — Desks master/detail | A only for shared visual tokens, otherwise independent | Existing desk CRUD/member routes receive simple responsive master/detail presentation | Desk templates/styles/handler tests and related goldens only |
| C — Workflow claim semantics/read projection | Existing workflow runtime | Reasonless pinned claim contract and sidebar eligibility/projection are atomic and server-authoritative | Claim command/plan/UoW/view/handler changes and focused tests only |
| D — Claim sidebar + E2E/closing verification | A–C | Sidebar interaction and all responsive/authorization journeys have final focused evidence | Cross-unit test/golden/E2E evidence only; no new product behavior |

### A — Categories + workflow select presentation (RED → GREEN → TRIANGULATE → REFACTOR)

**Outcome:** Workflow-configurator native selects share tkt-consistent presentation while retaining native semantics, HTMX/autosave, keyboard operation, visible high-contrast focus, and narrow-width fit. Categories render a semantic `Category` / `Created` / `Status` / `Actions` table, with an accessible destructive-action overflow and stacked mobile layout without horizontal overflow. The supplied category screenshot is structural reference only; current tkt palette, typography, spacing, and philosophy remain authoritative.

- [x] **A.1 RED — select and category-index semantics.** Add focused handler/template tests that fail for native select identity/name/HTMX preservation, semantic category headers, labelled destructive disclosure, visible focus hooks, and 390px no-overflow structure.
- [x] **A.2 GREEN — minimal builder/category presentation.** Implement only the template/style/view changes required for native select styling and the category table/disclosure/mobile stack; do not introduce custom selects, client authority, or screenshot-derived tokens.
- [x] **A.3 TRIANGULATE — interaction and fallback cases.** Cover keyboard selection/focus, HTMX and ordinary submission/autosave parity, high contrast, destructive disclosure keyboard access, and long category/status content at 390px.
- [x] **A.4 REFACTOR + focused gates.** Keep styles scoped to the affected controls, update only affected goldens after assertions pass, run focused Go tests, and record the unit rollback boundary.

### B — Desks master/detail (RED → GREEN → TRIANGULATE → REFACTOR)

**Outcome:** `/desks` becomes a restrained master/detail admin surface: list member counts, selected detail, new-desk disclosure, rename/delete, add/remove members, and responsive stacking. It uses existing routes and authorization unchanged; the supplied desks screenshot is structural reference only.

- [x] **B.1 RED — master/detail behavior contract.** Add focused handler/template tests that fail for member counts, selected-desk detail, disclosed create form, existing rename/delete/add/remove route targets, and semantic keyboard-accessible controls.
- [x] **B.2 GREEN — minimal responsive desks UI.** Implement server-rendered master/detail presentation with native disclosure and selected state, preserving current desk CRUD/member behavior and tkt tokens.
- [x] **B.3 TRIANGULATE — CRUD/member and narrow-layout edges.** Cover create, select, rename, delete, add member, remove member, direct unauthorized mutation denial, keyboard focus, and 390px stacking/no horizontal overflow.
- [x] **B.4 REFACTOR + focused gates.** Limit styles to the desks surface, update only affected goldens after behavioral assertions, run focused Go tests, and record rollback.

### C — Workflow claim semantics/read projection (RED → GREEN → TRIANGULATE → REFACTOR)

**Outcome:** A current pinned `assign_to_desk[claim]` has no claim/reason form in Pending Actions or the timeline. The Assignment sidebar shows Desk/current Assignee and `Assign to me` only to an active agent/admin/root current member of the pinned desk. Workflow claims are reasonless, including A→B; generic manual reassignment keeps its reason requirement and historical reason rendering. The existing workflow completion route/UoW rechecks pin, cursor, activity, role, and membership in the immediate transaction; first claimant wins; typed failures write nothing; success emits exactly one `Assigned to {person} · {desk}` event and preserves optional `new→in_progress`.

- [x] **C.1 RED — reasonless workflow claim boundary.** Add runner/UoW/handler tests that fail for a workflow claim carrying no reason, true A→B claim without reason, and the unchanged generic `/tickets/{id}/assign`/`TicketService.Assign` reason requirement.
- [x] **C.2 GREEN — remove reason from workflow claim pipeline.** Remove reason only from workflow claim command/operation handling; keep generic manual assignment and historical audit rendering untouched.
- [x] **C.3 RED/GREEN — sidebar projection and eligibility.** Add and satisfy view/handler cases for Desk/current Assignee, eligible visible button, nonmember-hidden button, and no pending/timeline claim form.
- [x] **C.4 TRIANGULATE — authoritative rechecks and concurrency.** Prove stale pinned version/cursor, removed membership, deactivated actor, and lost role each produce typed zero-write failures; prove concurrent claims first-wins and exactly one contextual assignment event, with optional `new→in_progress` still atomic.
- [x] **C.5 REFACTOR + focused gates.** Preserve the closed plan/UoW shape, update affected goldens only after assertions, run focused Go tests, and record rollback.

### D — Claim sidebar + E2E/closing verification

**Outcome:** Focused Go tests and isolated local Playwright prove the Amendment 3 UI and claim contract at desktop and 390px. This unit closes verification only; it does not create a delivery artifact or choose a PR.

- [x] **D.1 Focused Go verification.** Run the focused category/builder, desks CRUD/member, workflow claim authorization/concurrency, ticket-detail/sidebar, and regression tests after A–C are green; retain exact command/results.
- [x] **D.2 Isolated Playwright desktop + 390px.** With a unique temporary SQLite DB and loopback-only server, cover categories table/overflow, builder native selects, full desks CRUD/member flow, eligible claim button, hidden nonmember button, stale membership denial, successful sidebar claim/timeline event, keyboard focus, overflow, console/network checks, and cleanup. Browser MCP unavailability is BLOCKED, never silently skipped.
- [x] **D.3 Closing verification record.** Run only explicitly authorized final static/broad gates after focused evidence; record goldens, console/network, and cleanup truthfully. Delivery remains deferred after this evidence.

**Amendment 3 done when (spec trace):** native configurator selects and categories satisfy `category-workflows`/`category-management`; desks satisfy `desk-management`; sidebar visibility and transactional rechecks satisfy `role-authorization`/`ticket-management`; reasonless workflow claims, manual-assignment reason preservation, first-wins behavior, and exact contextual timeline event satisfy `ticket-workflow-execution`/`audit-log`; all A–D boxes have observed evidence without a delivery decision.

---

## Amendment 4 — Direct destructive actions, current-task cards, and linear-flow preview

**Status:** planned only. This amendment preserves every checked task and all recorded evidence above; all boxes below are intentionally unchecked. It is presentation-only: do not change workflow semantics, mutation routes, server-side authorization, or delivery/PR selection. Delivery remains deferred until implementation evidence exists.

**Interlock:** Amendment 3 D.3 and Amendment 2 WC.4 remain pending. They MUST NOT be checked or treated as closing evidence until every Amendment 4 executable correction, focused/golden proof, and isolated desktop + 390px Playwright evidence below is complete.

### Work-unit map

| Unit | Depends on | Outcome | Rollback boundary |
|------|------------|---------|-------------------|
| E — Direct deletes | Amendment 3 category/desk baseline | Categories and desks expose directly visible native destructive submits without changing existing POST/authorization/error behavior | Category/desk templates, scoped styles, focused handler tests, and affected goldens only |
| F — Current-task cards | Amendment 2 pending/timeline baseline | Current pending form/manual content uses `var(--amber-soft)` card structure without changing native controls or merged history | Ticket-detail/pending templates, scoped styles, focused handler/view tests, and affected goldens only |
| G — Two-panel builder preview | Amendment 3 builder baseline | Ordered editor and read-only linear-flow preview share responsive SSR/HTMX/full-page state | Builder templates, scoped styles, focused handler tests, and affected goldens only |
| H — Focused/golden regression | E–G | Assertions prove preserved contracts before one deterministic golden update and stability rerun | Tests and affected goldens only |
| I — Isolated Playwright | E–H | Local desktop + 390px browser proof covers corrected user journeys and cleanup | Temporary DB/server/log/evidence artifacts only |

### E — Direct destructive actions (RED → GREEN → TRIANGULATE → REFACTOR)

**Outcome:** Category and desk management replace `More actions` with directly visible native submit buttons labelled exactly `Delete category` and `Delete desk`. Existing POST routes, authorization, rejected-delete inline errors, keyboard focus, and responsive behavior remain unchanged; no menu or client-side authority is introduced.

- [x] **E.1 RED — direct-delete behavior contracts.** Add the smallest focused handler/template tests that observe failure before implementation: category and desk pages expose the exact visible labels; the forms retain their existing POST targets; direct unauthorized and rejected deletes remain server-authoritative and render existing inline errors; no `More actions` destructive disclosure remains.
- [x] **E.2 GREEN — render direct native submits.** Make the minimum category/desk template and scoped-style changes needed to expose the existing forms as visible `Delete category` and `Delete desk` submit buttons. Preserve form method/action, authorization, inline-error placement, visible focus, and responsive stacking.
- [x] **E.3 TRIANGULATE/REFACTOR — responsive and keyboard edges.** Prove long names, rejected deletes, keyboard activation/focus, and 390px no-horizontal-overflow behavior; keep the new-desk creation disclosure unchanged. Refactor only within the affected presentation seams while focused tests remain green.

### F — Current-task cards (RED → GREEN → TRIANGULATE → REFACTOR)

**Outcome:** Only the CURRENT pending form/manual content in ticket activity adopts the supplied `Current task` card structure with background exactly `var(--amber-soft)`. Native form semantics, pinned manual instructions, optional solution, completion behavior, and merged historical timeline semantics remain unchanged; no blue or derived background token is introduced.

- [x] **F.1 RED — current-task card contracts.** Add focused handler/view/template tests that fail before implementation for the current pending form and manual task card structure, exact `var(--amber-soft)` token use, retained labels/required/native fields/selects/checkboxes, pinned instructions, optional solution, and unchanged POST/GET behavior.
- [x] **F.2 GREEN — narrow pending-content wrapper.** Implement the smallest server-rendered wrapper and scoped styles around current pending form/manual content. Do not alter field names, required attributes, form actions, submit behavior, authorization, or completion semantics; preserve the existing merged historical event rendering.
- [x] **F.3 TRIANGULATE/REFACTOR — historical and narrow-layout edges.** Prove escaped/manual-solution and validation states remain readable, focus remains visible, 390px has no horizontal overflow, and comments/events retain their existing ordering and semantics. Refactor only after focused tests are green.

### G — Two-panel builder and read-only flow preview (RED → GREEN → TRIANGULATE → REFACTOR)

**Outcome:** The builder has a top header/actions and responsive two-panel desktop layout: semantic ordered editor left, read-only vertical linear-flow preview right. Mobile stacks without horizontal overflow. The preview is server-rendered from the submitted linear draft and may use restrained static connectors only; it adds no graph/canvas/editor behavior, branching, drag-and-drop, JavaScript state, or mutation route.

- [x] **G.1 RED — editor/preview parity contracts.** Add focused handler/template tests that fail before implementation for header/actions, semantic ordered editor, read-only preview mirroring submitted draft order, lack of interactive graph controls, and equivalent HTMX/full-page validation and preview state.
- [x] **G.2 GREEN — responsive server-rendered two panels.** Implement the smallest builder template/style changes for desktop panels and mobile stacking. Preserve native selects, autosave, add/reorder/remove, focus restoration, live announcements, preview, publish, and existing closed POST actions.
- [x] **G.3 TRIANGULATE/REFACTOR — alternate and accessibility cases.** Prove keyboard reorder/focus restoration, live announcements, unsaved preview, inline invalid-publish errors, desktop and 390px no-horizontal-overflow behavior, and no independent preview state or client-side authority. Refactor only while focused tests remain green.

### H — Focused tests and deterministic goldens

- [x] **H.1 Focused regression proof.** After E–G are green, run the affected category/desks/builder/ticket-detail handler and view tests with strict-TDD evidence retained per work unit. Verify existing authorization, rejected-delete, HTMX/full-page, timeline ordering, native-control, and accessibility assertions before any snapshot update.
  - *Evidence:* Expanded focused HTTP matrix including Amendment 3/4, category/desk deletes, workflow authorization, pending/manual/form behavior, merged-timeline ordering, and builder HTMX/reorder/mobile cases passed under `-race` in 61.977s; scoped diff checks were clean. One stale pre-Amendment-4 heading assertion was corrected from `Pending Actions` to `Current task` before the passing rerun.
- [x] **H.2 Golden update and stability.** Update only affected approved goldens after H.1 passes, inspect the scoped golden diff, then rerun without update to prove deterministic stability. Do not use golden churn to mask behavioral, authorization, or overflow regressions.
  - *Evidence:* The final authorized update ran exactly once and was followed by a byte-stable no-update run. Durable-tree comparison proved exactly 15 affected goldens: 69 whitespace-only normalizations plus two indentation-only settings lines, equal line counts, and zero route/auth/fixture/tag/content drift. All 23 goldens are trailing-whitespace-free; the 15-case rendered-output regression, focused Amendment 4 suite, `TestGolden`, and scoped diff checks pass. Recovery evidence: `/tmp/tkt-amendment4-final-golden-validation-evidence.txt`, SHA-256 `4b95e2536ab58383521a9190d94e0cfb1af5d48d68950c21094aa1d6795f755a`.

### I — Isolated Playwright desktop + 390px

- [x] **I.1 Browser evidence.** After E–H pass, follow `ux-ui-e2e-validation` exactly with a unique temporary SQLite database, loopback-only free port, bounded `/healthz` poll, and only the launched server stopped during cleanup. At desktop and 390px verify: directly visible category/desk delete buttons and rejected inline errors; current pending form/manual cards using `var(--amber-soft)` with keyboard focus/native controls; builder header/actions, editor/preview parity, mobile stacking, keyboard reorder/live announcement, preview/publish, HTMX/full-page behavior; console/network cleanliness and no horizontal overflow. Browser MCP unavailability is BLOCKED, never skipped.
- [x] **I.2 Cleanup evidence.** Record isolation, viewports, assertions, console/network result, and cleanup of only the temporary DB sidecars/logs/screenshots and launched PID truthfully.
  - *Evidence:* Parent Playwright MCP fallback passed at 1280×900 and 390×844 after the delegated child lacked MCP. Unique temp SQLite and loopback port 40519 covered category/desk direct deletes and full/HX inline rejections, exact `--amber-soft` manual/form Current task cards, native controls/focus, editor/read-only preview parity, keyboard reorder/live focus restoration, publish/autosave, responsive stacking, overflow, and clean final console/network state. Browser closed; only actual PGID 21946 was stopped; port/root/DB sidecars/log/manifest were removed. `/tmp/tkt-amendment4-playwright-evidence.txt`, SHA-256 `8ccf71d57959fdb3da02f9e3fe65a46c90d94bc55434286a97e9b48455eb5392`.

### Amendment 4 final closing gate

- [x] **A4.1 CLOSING GATE — final verification record.** Only after E–I have complete executable and browser evidence, run the explicitly authorized final static/broad gates and record exact results, golden stability, Playwright console/network/cleanup evidence, and final scoped churn. Only then may Amendment 3 D.3 and Amendment 2 WC.4 be reconsidered; they remain separately pending until their own criteria are satisfied. Delivery/PR selection remains deferred.

**Amendment 4 done when (spec trace):** direct native destructive submits and inline rejected-delete behavior satisfy `category-management` and `desk-management`; current pending form/manual card structure with exact `var(--amber-soft)` and preserved merged historical activity satisfies `ticket-management`; responsive semantic editor plus read-only linear-flow preview satisfies `category-workflows`; strict TDD, focused/golden proof, and isolated desktop + 390px Playwright evidence are complete without selecting delivery.
