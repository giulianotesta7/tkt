# Tasks: Sync Frontend Contracts

Change `sync-frontend-contracts` is a docs-only synchronization for issue #74. No runtime, template, CSS, JS, test, golden, migration, skill, CI, or dependency file is touched. Every task is completable in one session and is marked complete only after its artifact is written and `openspec validate` proves it.

## Global scope guard (applies to every task)

- Editable files, and ONLY these: `openspec/changes/sync-frontend-contracts/**` (proposal, tasks, and the three delta specs).
- Never edit: any file under `openspec/specs/**`, `openspec/changes/archive/**`, the sibling active change `sync-workflow-polish-contracts`, any Go/template/CSS/JS/test/golden/migration/skill/CI file, or any file outside the `docs/openspec-frontend-contract-sync` worktree.
- Evidence before authoring: each delta is grounded in the implementation and Go tests listed in its traceability section. Code comments alone never authorize a requirement.
- Name collision: `sync-frontend-contracts` does not collide with any existing active or archived change. If a re-run proves otherwise, stop and return blocked.

## T0. Baseline and active-change hygiene

- [x] Confirm the worktree base matches `origin/main` at `3484ee9` and that no canonical `openspec/specs/**` file is dirty.
- [x] Confirm `openspec/changes/sync-frontend-contracts` does not collide with existing active or archived change names.
- [x] Search active changes under `openspec/changes/**` for duplicate requirement identities (`Ticket Detail Presentation`, `Add Comment`, `appearance-settings`) and record the absence.

Acceptance: base hash matches, no canonical file touched, no duplicate requirement identity.

## T1. Ticket detail presentation delta

- [x] Author `openspec/changes/sync-frontend-contracts/specs/ticket-management/spec.md` with MODIFIED `Ticket Detail Presentation` that reproduces the full requirement using the shipped always-visible `Properties` / `Assignment` / `State` presentation, explicitly retiring the stale `<details>`/localStorage contract via `(Previously: ...)` and proving the current structure and read-only metadata/closed behavior without freezing incidental CSS classes.

Acceptance: delta reads against `web/templates/partials/ticket_detail.html`, `internal/adapters/http/golden_test.go:TestTicketDetailPresentationContract` and `TestClosedTicketDetailReadOnly`, and the closed-ticket read-only evidence in `web/templates/pages/tickets_show.html` + `handlers_tickets.go`.

## T2. Closed-ticket comment rejection delta

- [x] Author `openspec/changes/sync-frontend-contracts/specs/comment-timeline/spec.md` with MODIFIED `Add Comment` such that only non-closed accessible tickets accept comments and `resolved`/`closed`/`cancelled` reject before persistence, preserving role/visibility and append-only behavior.

Acceptance: delta traces to `internal/domain/state.go:IsClosed`, `internal/application/comment_service.go:Add`, `internal/application/comment_service_test.go:TestAddCommentOnClosedTicketRejected` and `TestAddCommentOnOpenTicketAccepted`, and `internal/adapters/http/handlers_detail_test.go:TestTicketCommentOnClosedTicketRejected`.

## T3. Appearance settings spec

- [x] Author `openspec/changes/sync-frontend-contracts/specs/appearance-settings/spec.md` with ADDED requirements that exactly match the existing `/settings` behavior: route access and navigation, visible current setting and options, supported update and feedback, persistence after reload, invalid-input/no-write behavior where implemented, and the observable effect on internal-comment presentation.

Acceptance: delta traces to `internal/adapters/http/handlers_settings.go`, `web/templates/pages/settings_index.html`, `internal/application/settings_service.go:AllowedInternalCommentBg` + `DefaultInternalCommentBg`, `internal/adapters/sqlite/settings_store.go` + `migrations/0005_instance_settings.sql`, `internal/adapters/http/middleware_auth.go` + `web/templates/partials/styles.html`, and `web/templates/partials/timeline.html`, with no invented setting beyond the three colors.

## T4. Validate and prove the active change

- [x] Run `openspec validate --all --strict --no-interactive`, `openspec validate --archived --no-interactive`, `openspec show sync-frontend-contracts` (or equivalent change inspection), `git diff --check`, and prove only `openspec/changes/sync-frontend-contracts/**` changed.

Acceptance: both validates pass, change is recognized as active, diff check is clean, and no canonical, runtime, test, or workflow file appears in `git status`.
