# Exploration: category-workflows

## Current State — evidence with exact paths/symbols

### Category — thin managed entity, no workflow concept

- `internal/domain/category.go:5-9` — `type Category struct { ID, Name, CreatedAt }`; uniqueness only, no `status`, `workflow_id`, `hidden/draft`, or version.
- `internal/application/category_service.go:18-110` — `CategoryService { Create, CreateFor, Rename, RenameFor, Delete, DeleteFor, GetByID, List }`. `CreateFor/RenameFor/DeleteFor` gate `CapManageCategories` (`internal/application/policy.go:43-45`). Delete rejects `ErrReferenced` when tickets use the category (store FK check).
- `internal/application/ports.go:158-169` — `CategoryStore { Create, Update, Delete, GetByID, List }` — no workflow port.
- `internal/adapters/sqlite/migrations/0001_init.sql:21-24` — `categories(id, name UNIQUE, created_at)`. No workflow tables. Later migrations `0003_roles_and_views.sql`, `0004_desks.sql`, `0005_instance_settings.sql` do not touch categories.
- `internal/adapters/http/handlers_categories.go:10-34` — routes `GET /categories`, `GET/POST /categories/new`, `POST /categories`, `GET/POST /categories/{id}/edit`, `POST /categories/{id}/delete`. Presentation `categoriesIndexData / categoryFormData` — name-only.
- `web/templates/templates.go:16` — `pages/*.html` + `partials/*.html` embedded; existing category templates `pages/categories_index.html` / `partials/category_form.html` are name-only; no builder fragment exists.
- `internal/adapters/http/handlers_tickets.go:35-86` — `collectOptions` loads `Categories` for create/filter; `create` parses `category_id` as plain FK and delegates to `TicketService.Create` — no workflow pre-check.

**Conclusion:** category workflows are greenfield — no leftover workflow tables, ports, services, or UI fragments on current `main`. Historical artifacts absent by instruction; `main` is clean except unrelated `openspec/changes/desks-ux-polish/`.

### Ticket — aggregate root, state machine, person-only assignment, draft-ticket creation

- `internal/domain/ticket.go:9-25` — `Ticket { ID, Number, Title, Description, RequesterName/Email, RequesterUserID *int64, CategoryID, UserID *int64, Priority, State, CreatedAt/UpdatedAt, ResolvedAt, ClosedAt }`. `RequesterUserID` immutable, `CategoryID` immutable after creation, `Description` immutable after creation (enforced in `TicketService.Update`).
- `internal/domain/state.go:3-42` — `State = new | in_progress | resolved | closed | cancelled`; `IsClosed = resolved|closed|cancelled`; `transitions` matrix is single source of truth (`new→{in_progress,resolved,cancelled}`, `in_progress→{resolved,cancelled}`, `resolved→{closed,in_progress}`, `closed→{in_progress needs reason}`, `cancelled→{}`); cancelled is terminal, `new` unreachable via transition.
- `internal/domain/ticket.go:27-71` — `Ticket.Transition(to, reason, now)` enforces matrix, stamps `ResolvedAt/ClosedAt`, clears on reopen, requires `reason` for `closed→in_progress`, refreshes `UpdatedAt`, returns `AuditEvent{Field:"state"}`.
- `internal/domain/ticket.go:80-157` — `ApplyUpdate` / `TicketUpdate` only edits `Title, Priority, UserID` via audit; assignment tri-state guarded, `UserID`+`ClearUserID` rejected; `UpdatedAt` touched only on change; closed tickets are read-only outside transitions.
- `internal/application/ticket_service.go:18-80` — `TicketService.Create(ctx, actor, CreateTicketInput{Title,Description,CategoryID,UserID,Priority})` validates non-empty title + `IsValidPriority`, gates assignment to `agent+`, validates `CategoryStore.GetByID`, validates active `agent+` target, stamps `RequesterUserID=&actor.ID`, `State=new`, persists atomically via `TicketUnitOfWork.Create` (ticket + `ActionCreated` audit).
- `internal/application/ticket_service.go:82-149` — `Assign(ctx,actor,ticketID,assigneeID,reason)` gates `CapAssignTicket`, scoped read `assignQuery(actor)` (`ScopeAssignable` for agent = self or unassigned, `ScopeAll` for admin/root), rejects `IsClosed`, validates target active agent+, reason required only for reassignment `person A→B`, no-op on same assignee, writes via `TicketUnitOfWork.Update` with one `ActionUpdate{Field:"user"}`.
- `internal/application/ticket_service.go:151-205` — `Transition` gates `CapEditTicket` (`user` never), scoped read `scopedQuery` (`ScopeOwned` user= requester, `ScopeAssigned` agent=assignee, `ScopeAll` admin/root, `ScopeNone` denies all), delegates to `Ticket.Transition`, stamps actor, `TicketUnitOfWork.Update`. `Update` gates `CapEditTicket`, rejects `UserID/ClearUserID` (forces `Assign`), rejects `IsClosed`, calls `ApplyUpdate`.
- `internal/application/ports.go:13-78` — `TicketStore` (reads, `Create/Update` direct but app never calls them alone) + `TicketUnitOfWork{Create,Update}` atomic (no-silent-mutations: `internal/adapters/sqlite/ticket_store.go:308-417` — `BEGIN IMMEDIATE`, stamp `TicketID`, `appendAuditEventsTx`, unique `number = MAX+1` retry). `TicketQuery` carries `State,Prio,CategoryID,UserID,Text,Numbers,SortByPriority,Scope,ActorID`; `ScopeNone` fails closed.
- `internal/adapters/http/handlers_tickets.go:404-550` — detail `detailData{View *TicketView, Next []transitionTarget, Options, Values, SelectedTo, CanCommentInternal, CanEdit, Closed}`; `allowedNext` is presentation-only, domain is enforcement; `detailDataFor` uses `ViewBuilder.TicketView` + scoped read; `transition/assign/update/addComment` parse form + map `domain` typed errors via `mapError` → status. Assignment form rejects forged `desk_id` as validation error (desk remains queue, not assignee — see `handlers_tickets.go:509-513`): `"desks cannot be ticket assignees"`.
- `web/templates/partials/ticket_detail.html:1-165` — properties (title inline edit when `CanEdit && !Closed`), priority `prop-select`, assignment select over `AssignableUsers`, state `Next` select + `state-reason` reveal script (`syncStateReason`, `data-needs-reason`), `ResolvedAt/ClosedAt` `<dl>`. `Closed==IsClosed`.

### Desks / assignment

- `internal/domain/desk.go:1-13` — `Desk {ID,Name,CreatedAt}`; doc: "deliberately not an assignee: ticket assignment persists a person ID only. If desk-targeted assignment is added later, it must choose the active eligible member with the fewest assigned tickets (user-ID tiebreak) and persist that person."
- `internal/application/ports.go:174-182` — `DeskStore{Create,Update,Delete,GetByID,List,AddMember,RemoveMember,ListMembers}`.
- `internal/application/desk_service.go:10-66` — all methods gate `CapManageDesks` (admin+), `AddMember` validates `Role.AtLeast(agent)`.
- `internal/adapters/sqlite/migrations/0004_desks.sql` — renames `groups→desks`, `group_members→desk_members`, recreates triggers `trg_desk_members_no_user*` and `trg_users_no_desk_member_downgrade`; SQLite triggers enforce `user` role never member.
- `internal/adapters/sqlite/desk_store.go:1-96` — persist desks + membership; `ListMembers` orders `created_at ASC, id ASC`.
- `internal/adapters/http/handlers_desks.go:1-62` — `/desks` index + CRUD + membership; presentation only admin/root via `policy.go:17-38` (`CapManageDesks`).
- Integration point noted for `assign_to_desk`: domain comment is the exact future assign semantics to reuse.

### Audit

- `internal/domain/audit.go:6-32` — `AuditEvent{TicketID,Actor,ActorUserID,Action,Field,FromValue,ToValue,Note,Reason,CreatedAt}`. `Action=created|transition|update` (future workflow actions will need new values).
- `internal/adapters/sqlite/migrations/0001_init.sql:54-64` + `0003_roles_and_views.sql:54-58` — `audit_events(id,ticket_id,actor,action,field,from_value,to_value,note,created_at,actor_user_id,reason)`; `idx_audit_ticket`.
- `internal/adapters/sqlite/ticket_store.go:420-460` — `appendAuditEventsTx` inside `TicketUnitOfWork`; all ticket mutations are single-transaction with audit. `internal/application/views.go:45-175` — `ViewBuilder{TicketView, enrichTimeline, mergeTimeline}` resolves `AuditEvent` actor/field labels, merges timeline newest-first.

### HTTP / HTMX / templates

- `internal/adapters/http/middleware_auth.go:44-135` — `SessionMiddleware` does Origin 403 on POST, `/healthz` + `/static/` bypass, resolves cookie `tkt_session` → `SessionStore.GetByID` → `UserStore.GetByID` → deactivation kills session, stamps `ctxKeyUser` + `ctxKeyInternalCommentBg`.
- `internal/adapters/http/render.go:16-155` — `Renderer {pages map[string]pageSet, fragments *template.Template}` parses `base.html|auth.html` + `pages/*.html` + `partials/*.html`; `Render(w,r,page,fragment,data,status)` buffers, `HX-Request` → fragment or `content` block else shell. `templateFuncs: formatTime/formatDatetime/humanize/ticketNumber/initials` (`displayTimeLayout "15:04 · 02-01-2006"`).
- `web/templates/base.html:13-26` — rail nav `tickets` + conditional `desks` (`CanManageDesks`), HTMX `static/htmx.min.js` deferred, `styles` partial.
- `web/templates/partials/ticket_form.html`, `ticket_detail.html`, `comment_form.html`, `timeline.html`, `pagination.html`, `styles.html` — server-rendered, HTMX swaps `hx-post/hx-target="#ticket-detail" hx-swap="outerHTML"` and `timeline` fragment; closed tickets hide edit/assign/comment forms server-side (`Closed` bool) and client.

### SQLite migration architecture

- `internal/adapters/sqlite/migrate.go:29-200` — `Migrate(ctx)` embeds `migrations/*.sql`, sorts by leading version, `CREATE TABLE IF NOT EXISTS schema_migrations`, per-migration `BEGIN IMMEDIATE` tx + `INSERT schema_migrations(version)`, then idempotent `backfillRolesAndRequesters` (promote `id=1` to root, backfill `requester_user_id` only on provable `created` audit). `timeLayout = RFC3339` persisted as `TEXT`.
- `internal/adapters/sqlite/migrations/0001_init.sql..0005_instance_settings.sql` chain: `0001` users/sessions/categories/tickets/comments/audit_events/schema_migrations; `0002` `tickets_fts` virtual table + triggers; `0003` `users.role`, `tickets.requester_user_id`, `comments.visibility`, `groups/group_members` (later `desks`), `role_changes`, audit `actor_user_id/reason`, immutability triggers; `0004` groups→desks rename; `0005` `settings(key,value)` seeded `internal_comment_bg=#E8EEFF`.
- Pattern for new change: additive `0006_category_workflows.sql`, recorded in `schema_migrations`, separate immediate tx, `CREATE TABLE IF NOT EXISTS`, idempotent backfill.

### Testing architecture (strict_tdd=true)

- `openspec/config.yaml` — `strict_tdd: true`, `test_command: go test ./...`, runner `go test`, layers `unit stdlib | integration net/http/httptest+in-memory SQLite | e2e none (golden HTML)`, checks `gofmt`, `go vet`.
- `internal/application/fakes_test.go` — `fakeTicketStore/UserStore/CategoryStore/DeskStore/AuditStore/CommentStore/UnitOfWork` used for service TDD; real SQLite via `internal/adapters/http/harness_test.go:17-285` (`openTestStore` temp dir, `Migrate`, `seedUser/seedSession/seedTicket`, `newHarness`, `get/postForm`, `*Renderer` + `SessionMiddleware` + full mux + `httptest`).
- `internal/adapters/sqlite/sqlite_test.go`, `backfill_test.go`, `migration_0003/0004_test.go` — migration + backfill regression coverage; golden files `internal/adapters/http/testdata/*.golden` for rendered HTML.
- TDD order established in prior slices: RED at `domain/application` → stores → HTTP handlers/goldens; `go test ./... -count=1 -race` green is gating.

---

## Product / Domain Invariants (agreed) and ambiguous points requiring proposal decisions

### Invariants the proposal MUST codify

- Every *usable* category MUST have a published workflow; a category without a published workflow MUST NOT appear as a ticket-creation choice for non-admin roles (hidden queue). New categories MAY remain hidden drafts while being configured. Draft ≠ published.
- MVP workflows are *strictly linear* and *intentionally minimal*: ordered list of steps, no graph, no branching, no approval, no plugins/DSL/webhooks/timers/generic automation.
- Published versions are *immutable*; a ticket pins `workflow_version_id` active at creation; definition edits produce a new version; in-flight tickets continue against pinned version.
- Step types are closed: `assign_to_desk`, `form`, `manual_task`, `resolve_ticket`, `close_ticket`. `resolve_ticket` and `close_ticket` are distinct automatic steps; a workflow MAY complete without either (ticket stays `in_progress` — `UpdatedAt` only, not lifecycle timestamps).
- `form.actor ∈ {requester, assignee}`; fields ∈ `{short_text, long_text, checkbox, single_select}`; assignee answers are always requester-visible (visibility invariant). Requester form is executed by authenticated requester; assignee form by current assignee (fallback decision needed).
- `assign_to_desk.desk_id` is required with per-step strategy `claim|least_loaded`; desk remains queue/resolver; the ticket is assigned to a *person* (`tickets.user_id`), never a desk. If strategy = `least_loaded`, pick active `agent+` member with fewest `user_id = person` tickets, tiebreak `user_id ASC` (reuse existing domain comment rule).
- Builder UX is vertical numbered list: `select step_type → contextual fields only → add/reorder/remove → preview → publish`. Ignore routing/branch UI. Validation messages are plain English, per-step.
- Existing categories need safe backfill/compat story.
- tkt philosophy gate: exclude any capability not serving the four representative cases.

### Representative-case gate (model probe)

- **Simple Network/Service Desk routing:** `assign_to_desk[d=Network, strategy=claim]` as step 1 then stop. Must be expressible without a form.
- **New server request:** `form[actor=requester]{fields…}` → `assign_to_desk[d=Infra, strategy=least_loaded]` → `manual_task{instructions}` → `form[actor=assignee]` → `resolve_ticket`. Tests end-to-end ordering, pinned version, and requester-visible answers.
- **AWS access:** single `form[requester]` + `assign_to_desk[d=Platform, claim]` + `manual_task` + optional `form[assignee]` + `resolve_ticket`. Tests short workflow with checkbox/select.
- **Multi-desk user offboarding ending in close:** `assign_to_desk[d=HR,claim]` → `manual_task[hr checklist]` → `assign_to_desk[d=IT,claim]` → `manual_task[deprovision]` → `assign_to_desk[d=Finance,claim]` → `close_ticket`. Tests sequential desk queue without branching, distinct close vs resolve.

All four are representable as linear lists of the agreed step types — validates minimal model is sufficient.

### Ambiguous points that STILL require explicit proposal decisions

1. **Draft visibility vs admin preview.** Does hidden-draft mean (a) excluded from `CategoryStore.List` for `user/agent` and from `GET /tickets/new` options, but still listable at `GET /categories` for `admin/root` with "draft" badge, or (b) a separate `category.status` column (`draft|published`) that filters ticket creation? Which roles may view a ticket whose category becomes hidden after creation?
2. **Workflow ownership/permission.** Which capability governs create/edit/publish — reuse `CapManageCategories` (admin/root) or introduce `CapManageWorkflows`? May `agent` preview drafts?
3. **Publish semantics.** Is publish unidirectional or may admin unpublish back to draft when zero published-tickets reference is not at issue? Is re-publish of same content a new version or idempotent no-op? Must publish validate "at least one step"?
4. **Pinned-version execution vs definition edits.** When a draft is edited, do previous published versions stay immutable forever? How to display historic version vs current published? Ticket detail should show `category v{pin}`?
5. **Completion without resolve/close.** After last workflow step completes, ticket state stays `in_progress` with `Closed==false`; who may then manually `Transition` to `resolved/closed` — same `CapEditTicket` scoped rules? Does runner auto-advance through consecutive automatic steps (`resolve→close`)?
6. **Form answer persistence.** Not `comments` (timeline is separate) and not `audit_events.note`. Proposal must place storage: `ticket_workflow_tasks(id, ticket_id, workflow_version_id, step_index, step_type, actor_role, status, produced_at)` + `ticket_form_answers(task_id, field_key, kind, value)` vs JSON `payload TEXT CHECK(json_valid(...))`. Single-select `options` are step config, not answer.
7. **Assignee-answer visibility invariant enforcement.** Store answers with `visibility` implicit? Or writer filter ensures requester can fetch assignee answers via `TicketView` even as `user` role?
8. **`assign_to_desk` execution & claims.** For `claim` steps, what is the pending representation — ticket unassigned with `desk_id` pending, or assigned to first claimant? Which roles may claim — any `agent` member of that desk? Does reassignment mid-workflow require reason via same `Assign` audit path?
9. **`least_loaded` scoring.** Scope counts `tickets.user_id = member` where `state NOT IN (resolved,closed,cancelled)`? Include `new`? ExcludeTickets of other categories? Must be explicit.
10. **Manual/form task completion authority.** Which actors may complete `manual_task` / `form[assignee]` — current `assignee`, any `agent+` on ticket's scope, or admin/root override? What about `form[requester]` when ticket is assigned elsewhere — may assignee still submit requester task?
11. **Validation plain-English contract.** Define per-step validation errors (unknown `desk_id`, empty field key/label, duplicate key within workflow, invalid select options length). Whether ordering gap (`step_index` uniqueness) is enforced in application vs DB.
12. **Compat/backfill for existing categories.** Option A: on `0006` backfill, mark every existing category as `draft` hidden → admin must publish before new tickets; existing tickets unaffected (NULL `workflow_version_id`). Option B: auto-generate trivial published `v1` with single `assign_to_desk[least_loaded first desk]` — but existing installs have heterogeneous desks. Proposal must pick one fail-closed default.
13. **Existing Behavior Preservation.** Ticket create MUST reject `category_id` whose published workflow is absent/with hidden draft (error message + re-render with 422) — new validation branch inside `TicketService.Create` after `CategoryStore.GetByID` and capability-scoped workflow lookup.
14. **Search/FTS interaction.** Do form answers participate in `tickets_fts`? Agreed minimal excludes — keep FTS title/description/comment only.

---

## Smallest coherent architecture options

### Option A — Category-anchored, versioned, linear — *recommended minimal default*

- **Storage:** additive `0006` tables (one immediate tx):
  - `category_workflows(id, category_id UNIQUE WHERE status='published' ? see below, status TEXT CHECK(status IN ('draft','published')), current_version_id FK, created_at, updated_at)`
  - `workflow_versions(id, category_workflow_id, version_no INT, steps_json TEXT CHECK(json_valid…), steps_count INT, created_at, created_by_user_id)` — immutable rows; `steps_json` is canonical ordered array; alternatively normalize to `workflow_steps(version_id, idx, type, config_json)`.
  - `tickets.workflow_version_id INT NULL FK workflow_versions(id)` — pinned at creation; NULL for pre-workflow tickets and for tickets whose category had no published workflow at-time (rejected going forward, but legacy NULL stays).
  - Runtime: `ticket_workflow_runs(id, ticket_id UNIQUE, workflow_version_id, current_step_idx INT, status TEXT CHECK(status IN ('active','completed','cancelled')))` + `ticket_workform_step_states / ticket_form_answers` minimal. Alternatively single `ticket_workflow_tasks` table listed above.
  - Indexes: `workflow_versions(category_workflow_id, version_no UNIQUE)`, `tickets(workflow_version_id)`.
- **Domain:** new `internal/domain/workflow.go` — `Workflow {ID,CategoryID,Status,CurrentVersion}`, `WorkflowVersion{ID, VersionNo, Steps []WorkflowStep}`, `WorkflowStep{Type, Config}` where `Config` is discriminated `AssignToDesk{DeskID, Strategy}`, `Form{Actor, Fields[]Field{Key,Label,Kind,Required, Options?}}`, `ManualTask{Instructions}`, `ResolveTicket{}`, `CloseTicket{}`; `Validate() error` with plain messages; `IsLinear` trivially true. `TicketTransition` untouched — `resolve_ticket/close_ticket` delegate to `Ticket.Transition(resolve)` inside `TicketUnitOfWork.Update` with actor `system-workflow`? Or stamp assignee actor for audit.
- **Application:** `WorkflowService{CreateDraft, AddStep, ReorderStep, RemoveStep, Validate, Preview, Publish}` gated by `CapManageCategories` (or new cap) and `WorkflowStore`; `TicketService.Create` adds workflow lookup `PublishedVersionFor(category_id)` and pins; if `None` → `ValidationError{Field:"category", Message:"category is not available for new tickets — publish its workflow first"}`. New `WorkflowRunService / TicketWorkflowEngine` that on `Create` seeds tasks from pinned steps, advances cursor on task completion (`CompleteForm`, `CompleteManualTask`, `ExecuteAssign`), runs automatic steps (`resolve_ticket/close_ticket`) via single `TicketUnitOfWork.Update` so audit+state atomic. Person-only invariant preserved: `assign_to_desk` computes person via `DeskStore.ListMembers` + `TicketStore.Count(member)` logic matching domain comment tiebreak.
- **UX:** builder under `/categories/{id}/workflow` (GET draft, POST add/reorder/remove, POST publish). `pages/category_workflow.html` + `partials/workflow_builder.html`, `workflow_step.html`. `TicketView` extended with `PendingWorkflowTasks []WorkflowTask` rendered in `ticket_detail.html` as inline "Next action" card with ownership-aware controls (`form[requester]` only for `RequesterUserID==actor`, etc.) using HTMX `hx-post /tickets/{id}/workflow/tasks/{taskID}/complete`.
- **Migrations/backfill:** mark existing categories `draft` (fail-closed) with one empty `category_workflows` row; no seeded version — admin action required. No reindex of `tickets_fts`.
- **Pros:** minimal tables, matches linear requirement exactly, preserves current stack, easy to TDD, publish immutability simple, gap to cancel/resume small.
- **Cons:** `steps_json` is opaque to SQL queries (acceptable for MVP linear). Normalize later if reporting needed.
- **Effort:** medium; fits review budget when sliced.

### Option B — Fully normalized workflow engine with generic step executor

- Same as A but `workflow_steps` fully normalized plus `workflow_run_instances` + `step_instances` state machines, separate `WorkflowEngine` interface with per-type executors, pluggable strategy table for `claim|least_loaded`. Adds `WorkflowExecutionStore` port.
- **Pros:** queryable, extensible to future step kinds.
- **Cons:** over-engineers agreed "if it doesn't serve the four cases, exclude it" rule; more tables/triggers/migrations; larger review surface; invites DSL creep.
- **Effort:** high.

### Option C — Event-sourced / draft-as-json-blob without version table

- Single `categories.workflow_draft_json TEXT` + `categories.published_workflow_json TEXT` + `tickets.workflow_snapshot_json TEXT` (pin copy). No version table.
- **Pros:** fewest tables.
- **Cons:** audit/version history awkward, snapshot divergence, harder to prove pinned immutability, harder to enforce per-step validation in SQL, drift from established versioned-migration style.
- **Effort:** low code but high integrity risk.

**Recommendation:** Option A with `workflow_versions.steps_json` (and optional `workflow_steps` normalized if the team prefers queryable config; both satisfy linear invariant — choose JSON for portability). Treat `steps_json` validation in `domain.WorkflowVersion.Validate` as authoritative; DB `CHECK(json_valid…)` is defense-in-depth only.

---

## UX journey implications

### Friendly builder (admin/root, at `/categories/{id}/workflow`)

- Route lives inside category management IA, not top nav. Category list shows badge `Draft` vs `Published vN`; unpublished category row is muted but reachable via `/categories`.
- Builder is a server-rendered vertical `<ol>` where each `<li>` is a step card. Controls: type `<select>` (five options) immediately swaps contextual fields via HTMX `hx-get /categories/{id}/workflow/steps/new?type=...` → `workflow_step` fragment (no client JS state). Per-step fields:
  - `assign_to_desk`: `desk_id` select from `DeskStore.List` (only admin/root view), `strategy` radio `claim|least_loaded`.
  - `form`: `actor` radio, repeatable field rows `key,label,kind(humanized),required, options (only when kind==single_select)`.
  - `manual_task`: `instructions` textarea + optional `summary` line.
  - `resolve_ticket`/`close_ticket`: explanatory text only + confirmation that they are terminal automatic (but workflow MAY continue — builder warns if they are not last).
- List actions: `+[Add step]` appends, drag-or-up/down `Move`, `Remove` (with confirmation), `Preview` renders read-only ordered summary, `Publish` posts entire ordered payload, server validates linear + per-step invariants and on success creates `workflow_versions` row, marks `category_workflows.current_version_id`, category becomes usable. Validation failures re-render builder with inline `error-banner` per faulty step in plain English (e.g. "Step 2: choose a desk", "Step 3: select options need at least 2 choices").
- Accessibility: numbered `<ol>`, `aria-label` on type selects, keyboard reordering, `localStorage` not needed (builder is form POST, not `details` toggle).

### Ticket-side pending actions (staff vs requester)

- `TicketView.Timeline` keeps merged chronological feed unchanged. New card `Pending Actions` sits above timeline when `ticket_workflow_runs.status==active` and `current_step_idx` task is pending.
- Per actor, at most one task is pending at a time (linear). Rendering gates on `Task.Actor == requester|assignee` vs `actor.ID` and ticket assignment:
  - `assign_to_desk[claim]`: shows "Claim to [Desk N]" button for any `agent+` member of that desk; completes by calling `Assign` path (persists person, audits `ActionUpdate{Field:"user",Reason: workflow task}`). `least_loaded` shows "Assigning…" and resolves server-side instantly (no user action).
  - `form[requester]`: only `domain.Ticket.RequesterUserID==actor.ID` (or admin/root override question for proposal) sees textarea/select/checkbox form; submit via `POST /tickets/{id}/workflow/tasks/{taskId}/complete` → HTMX `hx-swap outerHTML #ticket-detail`.
  - `form[assignee]`: only current `Ticket.UserID==actor.ID` sees form; requester sees read-only of previous assignee answers after completion (always requester-visible invariant).
  - `manual_task`: assignee (or ticket scope per `CapEditTicket`) checks "Mark done".
  - `resolve_ticket/close_ticket`: no button, auto-advances on arrival (server calls `Ticket.Transition(resolve|close)` inside workflow `UnitOfWork`; if `IsClosed` conflict, surface plain error).
- HTMX: `hx-post` on task forms targets `#ticket-detail` outerHTML so Properties/State cards stay consistent; `HX-Request` branch in `Renderer.Render` serves fragment. Non-HTMX fallback 303 to detail.
- `Closed` semantics: after `resolve`/`close`, detail remains read-only (`Closed==true`) but timeline stays visible; pending workflow card disappears (status `completed`).

---

## Migration / testing risks

### `resolved` vs `closed` state semantics (CRITICAL)

- Current `IsClosed` means `resolved`, `closed`, `cancelled` share read-only hides: `ticket_detail.html:CanEdit && !Closed` hides title edit, priority select, assignment, comment form; `TicketService.Update/Assign` reject `IsClosed`; `CommentService.Add` (not shown but gated) rejects on closed tickets. `Transition` is the only mutation allowed, with matrix allowing `resolved→closed`, `resolved→in_progress`, `closed→in_progress (reason)`.
- Workflow introduces *automatic* `resolve_ticket` (`new|in_progress → resolved`) and `close_ticket` (`resolved → closed` or direct `any→closed` for offboarding). Risks:
  - If runner auto-invokes `Transition(StateResolved)` while ticket is already `resolved`, `NewInvalidTransitionError` would bubble as 422 in-line. Runner MUST check current `State` before transition (e.g. `in_progress→resolved` valid, but `new→closed` is *invalid* per matrix — offboarding multi-desk case: tickets start `new`, without an explicit `new→in_progress` transition, an early `close_ticket` step would fail. Proposal must require that workflow either auto-moves `new→in_progress` as implicit first step, or enforce step ordering that guarantees `close` only after `resolved` (validator: `close_ticket` MUST be preceded by `resolve_ticket` or be sole state-change).
  - `ResolvedAt/ClosedAt` are set *only* by `Ticket.Transition` (via `UpdatedAt/ResolvedAt/ClosedAt`). Workflow auto-steps MUST go through `Ticket.Transition`, not direct field writes, to preserve invariant.
  - Audit for workflow auto-steps must stamp a distinguishable `ActorUserID` (NULL vs `system` user) — proposal must decide: `Actor = "workflow"` + `ActorUserID = NULL` vs attributed to completing assignee — because `ViewBuilder.enrichTimeline` resolves user labels from `ActorUserID`; audit filtering depends on it.
  - Reopen reason flow stays: if workflow completes without closing, later manual `closed→in_progress` still needs reason — runner must not swallow.

### Person-only assignment (CRITICAL)

- Domain invariant (`desk.go:1-5`, `handlers_tickets.go:509-513`, `ticket_service.go:82-149`) is ticket `user_id` references `users.id` of `agent+` only; `Desk` is never assignee. `assign_to_desk` MUST persist a concrete person, never desk id, and MUST reuse `least_loaded` definition: `SELECT u.id FROM desk_members JOIN users … WHERE u.active=1 AND u.role IN (agent,admin,root) ORDER BY ticket_count ASC, u.id ASC LIMIT 1`. Failure to honor tiebreak makes tests non-deterministic.
- `least_loaded` scoring scope ambiguity (see ambiguous #9) — if counts include all tickets vs open-only, behavior diverges. Test matrix must lock this with fakes.
- `claim` path MUST reuse `TicketService.Assign` capability gates: any agent member of desk claims, but `agent` role may only self-claim (`Assign: if agent && UserID==nil && *assigneeID != actor.ID → Forbidden`). Proposal must decide: does claim bypass `ReassignReasonRequired` (initial claim never needs reason) but desk reassignment later does?

### SQLite migration risks

- `0006` must be additive, idempotent, `IF NOT EXISTS`, `CHECK` constraints, no trigger rename collision with `0004`. Must register `schema_migrations(6)` in same `BEGIN IMMEDIATE` as DDL.
- Backfill choice drives risk: auto-publish trivial workflow risks surprise desk/target errors; fail-closed draft risks admin surprise ("why can't I create tickets?"). Fail-closed is safer given tkt philosophy (explicit).
- Foreign keys: `tickets.workflow_version_id → workflow_versions.id` must be `REFERENCES …` without `ON DELETE CASCADE`; legacy `NULL` remains valid.
- `steps_json`/`config_json` must add `CHECK(json_valid(...))` but validation authority remains domain `Validate()`, not trigger.
- Search triggers `0002` must NOT need modification; form answers excluded from `tickets_fts` by omission — verify via `sqlite_test.go` expectations on row counts.

### Testing risks (strict TDD)

- Without new tests, coverage of new tables is zero and `go vet` cannot catch domain miswiring. TDD must start at `internal/domain/workflow_test.go` (validate tables + step invariants, invalid desk, invalid actor, invalid select options, reorder integrity, empty workflow rejection, duplicate field key rejection).
- Then `internal/application/workflow_service_test.go` (authz, draft/publish, pinned version immutability with `fakes`) + `ticket_service_test.go` extension (create rejects unavailable category, pins version).
- Then `internal/adapters/sqlite/workflow_store_test.go` + `ticket_workflow_store_test.go` using real `openTestStore` + `Migrate`.
- Finally HTTP: `handlers_categories_test.go` (builder render + HTMX fragments), `handlers_tickets_test.go` (pending action gating per role), `golden_test.go` updates via `-update`. All `go test ./... -race -count=1` must remain green; `gofmt -l .` empty.
- Golden churn is broad (builder + detail pending card) — isolate service/handler assertions before regenerating goldens to avoid masking authz defects.

---

## Scope / non-goals for proposal drafting

### In scope (proposal captures)

- Category hidden-draft vs published-usable lifecycle and listability rules.
- Additive workflow definition: `category_workflows` + versioned `workflow_versions` immutable + `tickets.workflow_version_id` pin.
- Linear ordered step model over five closed types with per-type config validation (plain-English errors), add/reorder/remove, preview, publish.
- Per-step authz for execution: `assign_to_desk[claim|least_loaded]` person-only resolution with documented tiebreak; `form[requester|assignee]` actor rules and visibility invariant; `manual_task` completion authority.
- Workflow runtime cursor (`ticket_workflow_runs` + tasks/answers) and automatic execution of `resolve_ticket/close_ticket` via `Ticket.Transition` atomically with `TicketUnitOfWork`.
- Builder routes under `/categories/{id}/workflow` (admin/root, `CapManageCategories` or new cap), ticket-side pending card on detail, HTMX fragment swaps, full-page fallbacks.
- Backfill: existing categories → draft hidden (fail-closed), existing tickets `workflow_version_id=NULL`, `tickets_fts` untouched.
- Authorization matrix mirroring `policy.go` (`ScopeOwned/Assigned/All`) for builder and per-task completion; server-side gates before any view composition.
- Testing + migration plan (strict TDD, golden regeneration, `go test ./...` gate).

### Non-goals (explicitly excluded)

- No graph editor, branching, conditional routing, approvals, plugins/DSL, webhooks, timers, SLA, or generic automation framework.
- No SLA/timer-driven auto-escalation, no webhook delivery, no plugin marketplace.
- No desk reassignment balancing beyond `least_loaded` scoring stated; no queue metrics/dashboard.
- No FTS indexing of form answers, no email delivery, no retention/archive beyond existing close semantics.
- No generic settings/instance-settings expansion beyond `internal_comment_bg`; no new config screen.
- No change to archived OpenSpec artifacts — pending `desks-ux-polish` remains untouched (`/home/gtesta/Projects/tkt/openspec/changes/desks-ux-polish/`).

---

## Affected areas (exact integration points)

- `internal/domain/category.go` — add `CategoryStatus` concept (or companion `CategoryWorkflow` type) if proposal opts for status column; otherwise keep category thin and add `internal/domain/workflow.go`.
- `internal/domain/workflow.go` **new** — `Workflow/WorkflowVersion/WorkflowStep/Field` types, `Validate`, invariants, plain error messages (`internal/domain/errors.go` extensions: `ErrMsgWorkflow…`).
- `internal/domain/ticket.go` — add `WorkflowVersionID *int64` (pinned). `IsClosed`/transitions unchanged; runner calls `Transition`.
- `internal/application/ports.go` — add `WorkflowStore`, `WorkflowVersionStore`, `TicketWorkflowRunStore` (or unified `WorkflowStore`), extend `CategoryStore` read filters if draft-hiding needed, extend `TicketQuery` only if filtered by workflow presence.
- `internal/application/category_service.go` — `ListAvailableForActor` vs `List` if draft hiding introduces availability filter; otherwise `WorkflowService` owns check.
- `internal/application/workflow_service.go` **new** — draft/publish/validate/preview use cases; publish is the only creator of `workflow_versions`.
- `internal/application/ticket_service.go` — `Create` workflow lookup + pin; new `CompleteWorkflowTask` / `AdvanceWorkflow` use cases (or separate `WorkflowRunner`).
- `internal/application/policy.go` — add `CapManageWorkflows` if decoupled from `CapManageCategories`; extend `Scoped` logic for per-task actor checks (`CanCompleteTask` helper).
- `internal/adapters/sqlite/migrations/0006_category_workflows.sql` **new** — additive DDL + seed backfill.
- `internal/adapters/sqlite/workflow_store.go`, `ticket_store.go` — extend `scanTicketFrom` for `workflow_version_id`; add store implementations, `BEGIN IMMEDIATE` transactions, `retryUnique` for versions.
- `internal/adapters/sqlite/filters.go` — no change expected (FTS stays out).
- `internal/adapters/http/handlers_categories.go` — add `WorkflowHandlers` or extend `CategoryHandlers` with builder routes; `render.go` new page `category_workflow` + `workflow_builder/step/preview` fragments.
- `internal/adapters/http/handlers_tickets.go` — extend `detailData` with `PendingTask`, new `POST /tickets/{id}/workflow/tasks/{taskID}/complete`; gate `assign_to_desk` claim path via existing `Assign` errors.
- `web/templates/pages/*`, `partials/*`, `styles.html` — builder vertical list + ticket pending card; reuse existing `error-banner`, `prop-select`, `btn primary` patterns.
- `cmd/server/main.go` — wire new services/stores.

---

## Recommendation

Proceed on **Option A (category-anchored, versioned, linear — `workflow_versions.steps_json` + pinned `tickets.workflow_version_id` + `ticket_workflow_runs/tasks`)** as the smallest coherent default that satisfies all four representative workflows without introducing graph/automation debt.

Slice delivery under `ask-on-risk` (400-line budget risk: High, expected authored >400 lines) with explicit proposal resolution of the 14 ambiguous points above before `spec` — especially publish visibility (#1), permission (#2), auto-step ordering (#5/#15), completion authority (#10), and backfill (#12).

---

## Readiness

**Ready for proposal: Yes, with proposal explicitly resolving the 14 ambiguous product invariants above.** Implementation is expected to exceed the 400-line review budget and SHOULD be sliced (1: schema+domain+stores, 2: workflow service + pinning, 3: builder UX, 4: ticket runtime + pending card) under `ask-on-risk`.
