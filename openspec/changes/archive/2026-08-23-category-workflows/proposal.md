# Proposal: Category Workflows — Linear, Published, Pinned

> **Outcome first:** Admins build a linear, versioned workflow per category (vertical ordered list, no graph). Every category usable for new tickets has a published workflow; categories without one are hidden from creation. Tickets pin the workflow version active at creation, start **unassigned**, and advance through exactly one pending step at a time — assignee selection at creation is removed entirely, and assignment happens only later through the pinned workflow flow. Manual steps carry immutable pinned **Instructions** and accept an **optional solution**; the unified timeline reports what pinned task was completed and, when submitted, the assignee's solution, with a restrained, accessible treatment for completed form results. Four tkt-native cases — simple routing, new server request, AWS access, multi-desk offboarding — must work without adding branching, approvals, automation, or search complexity.

## Intent / Problem

Categories today are name-only (`internal/domain/category.go:5`) with no workflow concept. Ticket creation accepts any `category_id` that exists. Staff then triage manually, which hides routing, required information, and hand-offs. The product needs a minimal, auditable, linear workflow bound to each category that:

- makes required information and desk ownership explicit before work starts,
- routes desk-queue work without assigning to a desk entity,
- keeps requester and assignee responsibilities separable,
- preserves ticket lifecycle (`new → in_progress → resolved → closed`) as an independent state machine,
- stays strictly within the four representative journeys and rejects generic workflow/automation creep.

Without this, every new category relies on tribal knowledge and ad-hoc assignment.

**Amendment 2 (approved product requirements):** Two follow-on product gaps surfaced after the first proposal round. Ticket creation still accepted an optional assignee, which bypassed the fixed flow and stranded desk work outside category routing. And pending/completed step presentation was generic: a meaningless ordered-list numbering with a `Mark the current task as complete.` prompt that hid the pinned instruction context, plus completed form results rendered as undifferentiated text. This amendment closes the creation-time bypass (new tickets are always unassigned), gives manual tasks their pinned instructions plus an optional solution surfaced on the timeline, removes the generic pending presentation, and gives completed form results a restrained, intentional, accessible, tkt-consistent treatment.

### Alternatives considered

| Alternative | Verdict |
|---|---|
| Category-anchored versioned linear model (`workflow_versions.steps_json` + pinned `tickets.workflow_version_id`) | **Chosen** — smallest coherent default satisfying all four cases; exploration Option A. |
| Fully normalized step executor with per-type tables | Rejected — over-engineers; invites DSL/plugin drift against tkt philosophy. |
| Single draft/published JSON blob on `categories` | Rejected — weak version history and pin integrity. |

## User Journeys

### 1. Admin publishes a category workflow (happy path)

1. Admin opens `/categories` → sees badges `Draft` vs `Published vN` for categories that have a workflow (categories without any workflow definition are simply unavailable for new tickets, not shown as draft rows).
2. Admin opens `/categories/{id}/workflow` → vertical numbered `<ol>` builder. If no draft exists yet, an empty draft is created lazily at this point.
3. Selects step type → contextual fields appear (HTMX fragment, no canvas). Adds steps, reorders (up/down), removes, previews read-only ordered summary.
4. Clicks **Publish** → server validates plain-English per-step errors inline; on success creates immutable `vN`, category becomes usable for new tickets. Previously published version stays active until publish succeeds (draft editing never hides published).

**Non-happy:** Publish with 0 steps, empty field keys, duplicate keys, or unknown desk → inline error (`Step 2: choose a desk`, etc.), no version created. HTMX and full-page fallback both re-render errors.

### 2. Requester creates a ticket in a category with a published workflow

1. Requester opens `GET /tickets/new` → `CategoryStore` options filter excludes categories without a published workflow (unavailable). Categories that have never had a workflow configured are simply absent from the list.
2. Submits `POST /tickets` with `title`, `description`, `category_id`, `priority` — **no assignee control and no assignee parameter**. The create form renders no assignee selector for any role, and `TicketService.Create` binds no assignee field; a request that still carries an assignee parameter is rejected with a validation error rather than silently dropped, so no hidden direct-assignment path survives. `TicketService.Create` looks up the published version; if none, rejects `422` with `category: category is not available for new tickets — publish its workflow first` and re-renders form. If published, ticket pins `workflow_version_id`, starts `new` **with an empty assignee**, and seeds a single `active` run with cursor at step 0. Person assignment happens only later through the fixed category flow (`assign_to_desk` claim / least-loaded steps, journey 3).
3. Existing tickets with `workflow_version_id = NULL` (pre-workflow) remain readable; no legacy mode needed.

### 3. Ticket advances through a linear workflow (one pending task at a time)

Detail (`GET /tickets/{id}`) shows a **Pending Actions** card above the timeline when run is `active`:

| Step | Who sees actionable control | What happens on complete |
|---|---|---|
| `assign_to_desk[claim]` | Only an active `agent`/`admin`/`root` current member of that pinned desk sees **Assign to me** in the Assignment sidebar | On a successful reasonless claim, persists a **person** (`tickets.user_id`) and, when the ticket is `new`, transitions it to `in_progress` in the same unit of work. The timeline records exactly one contextual `Assigned to {person} · {desk}` event; `agent` may only self-claim. |
| `assign_to_desk[least_loaded]` | No button; server resolves instantly | Picks the active `agent+` desk member with fewest assigned tickets in `new\|in_progress` (excludes `resolved/closed/cancelled`, tie by lower `user.id`), persists that person and, when the ticket is `new`, transitions it to `in_progress` in the same unit of work with both audits. |
| `form[requester]` | Only authenticated `RequesterUserID == actor` | Submits short/long text, checkbox, single select → stored as answers; visible to requester by invariant. Non-requesters see read-only after completion; no impersonation. |
| `form[assignee]` | Only current `Ticket.UserID == actor` (after admin self-assigns via audited assignment if needed) | Answers stored and always requester-visible. |
| `manual_task` | Current assignee | Pending card leads with the step's pinned **Instructions**; completing posts an **optional solution text** through the same completion event. |
| `resolve_ticket` | No button — automatic, **standalone terminal final step** | Runner calls `Ticket.Transition(resolved)` inside `TicketUnitOfWork` (atomic with audit). From `closed` no-ops as complete; from `cancelled` rejects. Transitions through state machine only. |
| `close_ticket` | No button — automatic, **standalone terminal final step** | Runner resolves and closes atomically when needed: from `new`/`in_progress` atomically executes `Ticket.Transition(resolved)` then `Ticket.Transition(closed)` with two audit events in one unit of work; from `resolved` executes only `closed`; from `closed` no-ops as complete; from `cancelled` rejects. Never bypasses the state machine. |

Terminal steps (`resolve_ticket` and `close_ticket`) are each standalone and final — at most one per workflow and no steps follow either (validator enforced). A separate `resolve_ticket` step before `close_ticket` is neither required nor allowed because terminal steps are final. After the terminal transition(s), run = `completed`; otherwise workflow completion leaves the ticket in its current state. A successful `assign_to_desk` moves `new` to `in_progress`, while a workflow containing only requester forms may complete with the ticket still `new`. HTMX `POST /tickets/{id}/workflow/tasks/{taskId}/complete` swaps `#ticket-detail outerHTML`; non-HTMX falls back to `303`.

Pending and timeline presentation (amendment): the pending card never uses ordered-list numbering and never shows the generic `Mark the current task as complete.` message. The unified timeline reports each completed manual task by its pinned step (instructions read from the immutable pinned version at that step index) and, when the assignee submitted a non-empty solution, the solution as escaped ordinary text — attributed and timestamped by the existing completion event, newest first, never copied into `audit_events.note`, never indexed by `tickets_fts`. Completed form results render as semantic `dl/dt/dd` pairs with a restrained, tkt-consistent treatment; no ticket-facing technical `workflow` wording is used.

### 4. Representative cases (tkt philosophy gate — if it doesn't serve these, exclude it)

- **Simple routing:** `assign_to_desk[d=Network, claim]` only. Valid without a form; a successful claim assigns the person and moves `new` to `in_progress`, then the agent resolves the ticket manually.
- **New server request:** `form[requester]` → `assign_to_desk[d=Infra, least_loaded]` → `manual_task` → `form[assignee]` → `resolve_ticket`. Tests pinning + requester-visible answers.
- **AWS access:** `form[requester]` (checkbox/select) → `assign_to_desk[d=Platform, claim]` → `manual_task` → `form[assignee]`? → `resolve_ticket`.
- **Offboarding (multi-desk, ends in close):** `assign_to_desk[d=HR,claim]` → `manual_task` → `assign_to_desk[d=IT,claim]` → `manual_task` → `assign_to_desk[d=Finance,claim]` → `close_ticket`. Validates sequential desk queue; `close_ticket` is standalone terminal and, when current state is `new` or `in_progress`, resolves and closes atomically when needed (two transitions in one unit of work) without a preceding `resolve_ticket` step.

    Manual steps in these journeys carry pinned instructions and optional solutions on the timeline; none of the journeys needs ordered-list numbering, a generic pending message, or a creation-time assignee.

## Scope

### In Scope

- Category `draft` vs `published` lifecycle and visibility (hidden from `GET /tickets/new` and creation validation; `GET /categories` shows badge for categories with workflows).
- Additive versioned definition: `category_workflows` + immutable `workflow_versions` + pinned `tickets.workflow_version_id` + runtime cursor (`ticket_workflow_runs` + tasks/answers — exact table shape is design detail, but cursor semantics are proposal-level).
- Linear ordered step model over **closed** five types with per-type config, `add/reorder/remove/preview/publish`, plain English inline errors.
- Per-step execution authority and person-only `assign_to_desk` semantics (tiebreak documented).
- `form.actor` and field inventory, assignee-answer visibility invariant.
- Builder route `GET/POST /categories/{id}/workflow` + HTMX fragments, ticket pending card.
- Migration is additive only — no draft rows are created for existing categories; categories without a workflow definition/version are simply unavailable for new tickets. A draft is created lazily when an admin begins configuration at `/categories/{id}/workflow`. Existing tickets keep `workflow_version_id = NULL`; `tickets_fts` untouched.
- Scoped authz mirroring `policy.go` (`ScopeOwned/Assigned/All`).
- **Creation is unassigned-only.** New-ticket form renders no assignee control and `POST /tickets` binds no assignee parameter; every new ticket starts with an empty assignee regardless of actor role. A creation request that includes an assignee parameter is rejected with a validation error (no silent drop, no hidden direct-assignment path). Assignment happens only later through the fixed category flow.
- **`manual_task.instructions` + optional solution.** Manual steps pin non-empty instructions; the pending card shows the pinned Instructions to the current assignee; completion accepts an optional solution text. The timeline reports the completed pinned task (instructions read from the pinned version at that step index) and, when non-empty, the assignee's submitted solution — escaped ordinary text, attributed/timestamped by the existing completion event, tied to the pinned step/index, not copied into `audit_events.note`, not indexed in `tickets_fts`.
- **Pending presentation cleanup.** No ordered-list numbering on pending actions and no generic `Mark the current task as complete.` message anywhere ticket-facing. Manual pending UI leads with the pinned instruction context; other pending forms remain contextual to their pinned fields/actions.
- **Completed form results treatment.** Timeline rendering of completed form results uses a restrained, intentional, accessible treatment consistent with tkt: semantic `dl/dt/dd` pairs, responsive layout, plain-text escaping, newest-first ordering preserved, and no ticket-facing technical `workflow` wording. All of these surfaces remain POST-mutation-only; GET renders them read-only.

### Out of Scope (Non-Goals)

- Graph editor, branching, conditional routing, approvals, parallel steps, loops.
- DSL, plugins, webhooks, timers, SLA, notifications, generic automation.
- FTS over form answers; email delivery; queue metrics/dashboard.
- Changes to `desks-ux-polish` or archived OpenSpec artifacts.
- Production compatibility layer, auto-generated workflows, or backfilled `v1` for existing categories.
- Historical versions list and snapshot-preview UI — published versions remain immutable under the hood, but MVP exposes only current published version status and the editable draft (no historic browser).
- Exposing technical `Workflow vN` pin to requesters on ticket detail — pin is internal integrity, not requester-facing.
- Retaining any assignee selection at ticket creation (including hidden or legacy parameter acceptance), copying manual-task solutions into `audit_events.note` or `tickets_fts`, reverting to numbered/generic pending messages, or reskinning beyond the completed-results treatment.
- Changes to `desks-ux-polish` or archived OpenSpec artifacts (reaffirmed).

## Capabilities Affected

| Capability | Effect |
|---|---|
| `category-management` | **Modified** — category gains `draft/published vN` lifecycle; `List` for ticket creation filters to published; `GET /categories` exposes draft vs published badge for categories with workflows. |
| `category-workflows` | **New** — versioned linear workflow definition, builder, publish, pin, runtime cursor, task completion, automatic `resolve/close` (standalone terminal). |
| `ticket-management` | **Modified** — `TicketService.Create` validates usable category, pins version, and creates tickets unassigned (no assignee control or parameter; assignee-carrying requests rejected); detail view exposes pending workflow task. |
| `ticket-workflow-execution` | **New** — linear advancement, per-actor completion gates, atomic `resolve/close` via `Ticket.Transition` (close resolves and closes atomically when needed); manual steps carry pinned instructions + optional completion solution; timeline entries for completed manual tasks and completed form results with the approved presentation (`dl/dt/dd`, responsive, escaped, newest-first, no technical `workflow` wording). |
| `role-authorization` | **Modified** — reuses `CapManageCategories`; extends scoped checks for task completion (no new `CapManageWorkflows`). |
| `audit-trail` | **Modified** — new workflow actions and clearly attributed automatic state events (see Product Rules); manual-task solutions are never copied into notes, attribution stays with the existing completion event. |
| `ticket-search` (`tickets_fts`) | **Unchanged** — form answers and submitted solutions excluded from FTS by omission. |

## Product Rules (approved decisions + resolved defaults)

### Approved decisions (MUST preserve)

1. **Usable = published.** Every category usable for new tickets MUST have a published workflow; a category without one MUST be excluded from `GET /tickets/new` options and MUST be rejected at `POST /tickets` with a validation error. Categories with no workflow definition/version are simply unavailable for new tickets.
2. **No prod compat layer.** Private repo, no production users. Existing tickets remain readable with `workflow_version_id = NULL`. Do not invent legacy mode or auto-generated workflows. Migration is additive only — no backfill of draft rows for existing categories.
3. **MVP strictly linear.** Ordered list of steps only. Closed step set `assign_to_desk`, `form`, `manual_task`, `resolve_ticket`, `close_ticket`. No branching/approval/graph/DSL/plugins/webhooks/timers/SLA/notifications/generic automation/FTS-over-answers.
4. **Form inventory.** `form.actor ∈ {requester, assignee}`. Fields `short_text | long_text | checkbox | single_select`. `single_select.options` ≥ 2, non-empty, unique within field. Assignee answers are always requester-visible.
5. **`assign_to_desk`.** `desk_id` required, `strategy ∈ {claim, least_loaded}`. Desks are queues/resolvers, never ticket assignees — persist a **person** (`tickets.user_id`). `least_loaded` counts all assigned tickets where `state IN (new, in_progress)` (excludes `resolved/closed/cancelled`) across **all categories**, tiebreak lower `user.id` (matches `internal/domain/desk.go` comment). When assignment succeeds and the ticket is `new`, the same unit of work MUST also call `Ticket.Transition(in_progress)` and persist assignment plus state audits atomically. A pending claim does not change state. A pinned workflow claim is reasonless, including A→B; generic manual reassignment remains reason-required.
6. **No impersonation.** Admin/root MUST NOT complete `form[requester]` or other actor-owned tasks directly. They MAY reassign themselves through the existing audited `Assign` flow, then complete `assignee`-owned tasks. Requester forms remain requester-only.
7. **Workflow vs lifecycle independence.** Completing the workflow alone MUST NOT force a ticket state. Without `resolve_ticket`/`close_ticket`, the ticket remains in the state reached by its steps: normally `in_progress` after successful `assign_to_desk`, but possibly `new` when no assignment occurred. Manual `Transition` remains governed by existing `CapEditTicket` scoped rules.
8. **Distinct standalone terminal steps.** `resolve_ticket` and `close_ticket` are each standalone terminal final steps — at most one per workflow and no steps follow either. `resolve_ticket` transitions `new|in_progress → resolved` via `Ticket.Transition(resolved)`; from `resolved`/`closed` it no-ops as complete; from `cancelled` it rejects. `close_ticket` resolves and closes atomically when needed: from `new`/`in_progress` it atomically executes `Ticket.Transition(resolved)` then `Ticket.Transition(closed)` with two audit events in one unit of work; from `resolved` it executes only `closed`; from `closed` it no-ops as complete; from `cancelled` it rejects. Both go through `Ticket.Transition` — never bypassing the state machine — and stamp `ResolvedAt/ClosedAt` only there. A separate `resolve_ticket` step before `close_ticket` is neither required nor allowed because terminal steps are final.
9. **Immutability + pinning.** Published `workflow_versions` are immutable. Tickets pin `workflow_version_id` active at creation; in-flight tickets continue against pinned version; definition edits produce a new version. Pin is internal integrity and need not be exposed as `Workflow vN` to requesters.
10. **Builder.** Vertical numbered `<ol>` with contextual fields only, `add/reorder/remove`, `preview` (read-only ordered summary of the current draft), `publish`, plain inline validation. No canvas/nodes/connectors/branch UI. MVP shows only current published version status and the editable draft; no historic versions list or snapshot-preview UI.
11. **tkt philosophy gate.** If a capability does not serve the four representative cases, exclude it.
12. **Creation is unassigned-only (amendment).** Ticket creation MUST NOT offer or accept an assignee. The create form renders no assignee selector for any role; `POST /tickets` binds no assignee parameter, and a request carrying one MUST be rejected with a validation error (never silently ignored — no hidden direct-assignment path). Every new ticket starts with an empty assignee. Post-creation assignment happens only through the fixed category flow: `assign_to_desk` steps (claim / least-loaded, rule 5) persist the person. The prior approved audited self-assign (rule 6) is preserved solely as the mechanism by which an actor becomes the current assignee to complete assignee-owned steps; no new or generic assignment surface is added.
13. **`manual_task` pinned instructions + optional solution (amendment).** Each `manual_task` step pins non-empty `instructions` in the immutable published version. The pending card shows those instructions as the primary content to the current assignee (authority per D9); completion posts an OPTIONAL solution text through the same completion event. The event records the task by pinned `step_index`; the unified timeline shows what pinned task was completed (instructions rendered from the pinned version at that index, never the live version) and, when the solution is non-empty, the assignee's solution as escaped ordinary text, attributed and timestamped by the existing completion event. The solution remains tied to the pinned step/index and MUST NOT be copied into `audit_events.note` and MUST NOT be indexed in `tickets_fts`.
14. **Pending presentation (amendment).** Pending actions MUST NOT use ordered-list numbering and MUST NOT show the generic `Mark the current task as complete.` message. Manual pending UI leads with the pinned instruction context; other pending forms remain contextual to their pinned fields/actions.
15. **Completed form results presentation (amendment).** Completed form results on the timeline MUST use a restrained, intentional, accessible treatment consistent with tkt: semantic `dl/dt/dd` markup, responsive behavior, plain-text escaping of all stored values, newest-first ordering, and no ticket-facing technical `workflow` wording — labels name the completed task and its fields, not the workflow engine.

### Resolved defaults (proposal-level choices using simplest coherent default)

| # | Decision | Default (simplest coherent) |
|---|---|---|
| D1 | Permission for builder/publish | **Reuse `CapManageCategories`** (`admin`/`root` only). No new `CapManageWorkflows`. `agent`/`user` cannot preview drafts. |
| D2 | Draft vs published visibility while editing | **Keep published active while editing.** Publishing a new version replaces `current_version_id` atomically; draft edits never hide the published version. Category list shows `Draft` badge when draft differs from published and `Published vN` when published; category with no workflow definition has no badge and is unavailable for new tickets until published, with draft created lazily at `/categories/{id}/workflow`. Ticket creation always reads published. Unpublishing is NOT supported in MVP (one-way `draft → published`; new publish overwrites current). |
| D3 | Publish validation | **Publish MUST validate ≥1 step.** Empty workflow, unknown `desk_id`, empty field `key/label`, duplicate field key within workflow, `single_select` with `<2` options, missing `strategy/desk_id/actor` → plain error, no version created. Re-publish of identical content creates a new version (idempotent no-op is NOT assumed). |
| D4 | Ordering of automatic terminal steps | **`resolve_ticket` and `close_ticket` are each standalone terminal final steps.** Validator enforces: at most one terminal step per workflow; when present, it MUST be the final step and no steps follow it; `resolve_ticket` and `close_ticket` MUST NOT both appear (a separate `resolve` before `close` is neither required nor allowed because terminal steps are final). `close_ticket` when current state is `new` or `in_progress` resolves and closes atomically when needed — runner executes `Transition(resolved)` then `Transition(closed)` with two audit events in one unit of work; from `resolved` only `closed`; from `closed` no-ops; from `cancelled` rejects. Both use `Ticket.Transition` and never bypass the state machine. |
| D5 | Ticket lifecycle after workflow without resolve/close | Workflow completion leaves the ticket in its current state. Creation and requester forms do not change state. A successful `assign_to_desk` atomically moves `new→in_progress` when it resolves a person; if already `in_progress`, state is unchanged. Any later manual `Transition` follows existing `CapEditTicket` + `ScopeAssigned/All` rules. |
| D6 | FTS | **Unchanged.** `tickets_fts` indexes title/description/comments only. No reindex for answers; no search over form answers. |
| D7 | Audit attribution for automatic steps | **Attribute clearly without synthetic user.** `audit_events.actor = 'workflow'` (literal), `actor_user_id = NULL`, `action = 'transition'`, `field = 'state'`, `from_value/to_value` as usual, `reason = NULL`. `ViewBuilder` renders `actor` as `Workflow` system label (no `users` lookup). No synthetic `users` row. Manual steps audit with completing actor's `actor_user_id`. For `close_ticket` from `new`/`in_progress`, two audit events (`new→resolved` and `resolved→closed`) are written atomically in one unit of work. |
| D8 | Form answer storage (constrain without prescribing exact DDL) | Answers belong to workflow tasks, NOT to `comments` or `audit_events.note`. Spec will define `ticket_workflow_tasks` + `ticket_form_answers` (or equivalent) — JSON `payload` alone is allowed only if version pin and visibility invariant are still enforceable. |
| D9 | Completion authority (detailed) | `manual_task` and `form[assignee]`: current `Ticket.UserID == actor` only (admin/root via self-assign). `form[requester]`: `RequesterUserID == actor` only. `assign_to_desk[claim]`: an active `agent`/`admin`/`root` current member of the pinned desk may claim only themselves. The server/UoW rechecks pinned version, cursor, activity, role, and membership in the transaction. Claims are reasonless, including A→B. Least-loaded resolves server-side instantly. `Close`/`resolved` tickets remain read-only for task completion. |
| D10 | Migration / backfill | **Additive only, no backfill.** Migration `0006_category_workflows` creates workflow tables and `tickets.workflow_version_id` column only; it creates NO `category_workflows` or `workflow_versions` rows for existing categories. A category with no workflow definition/version is simply unavailable for new tickets. A draft is created lazily when an admin first opens `GET /categories/{id}/workflow` and begins configuration. Existing tickets keep `workflow_version_id = NULL`. No seeded `v1`. |
| D11 | Version display (MVP) | Published versions are immutable under the hood. MVP builder shows only current published version status (`Published vN` badge) and the editable draft; no historic versions list or snapshot-preview UI. Ticket pin (`workflow_version_id`) is internal integrity — ticket detail need not expose `Workflow vN` to requesters. |
| D12 | Step index integrity | `step_index` uniqueness per version enforced in application `Validate()` and DB unique index; ordering gaps rejected with plain error. |
| D13 | `manual_task.instructions` at publish | **Non-empty instructions required per `manual_task` step.** Rule 14 removes the generic fallback message, so publish MUST reject empty instructions with a plain per-step error (e.g. `Step 3: add instructions for the manual task.`); no generic-message fallback remains. *(amendment default — flagged for confirmation)* |
| D14 | Creation request carrying assignee | **Reject with a form-scoped validation error** (`422` re-render), not silent ignore — loud failure keeps stale/hidden clients from sneaking assignment through. *(amendment default — flagged for confirmation)* |
| D15 | Solution storage | Manual-task solutions live with the completion record in the workflow task/answer family (same constraint family as D8) — never `comments`, never `audit_events.note`, never FTS. Timeline rendering joins the pinned version at `step_index` for instructions. |
| D16 | Numbering removal scope | Removal applies to ticket-facing pending and timeline presentation. The builder keeps ordered, reorderable step authoring — there order is the edited data, not decoration (frontend-design: structural numbering is appropriate when the content actually is a sequence). *(amendment interpretation — flagged for confirmation)* |

## Success Criteria (reviewable)

- [ ] `GET /tickets/new` excludes categories without a published workflow for all roles; `POST /tickets` with such `category_id` re-renders `422` with `category` field message.
- [ ] Builder at `/categories/{id}/workflow` is reachable only with `CapManageCategories`; vertical numbered list supports add/reorder/remove/preview/publish with contextual fields only; validation errors are plain English per step. Builder shows only current published version status and editable draft (no historic list).
- [ ] Publish with 0 steps or invalid step config creates no version and surfaces inline errors.
- [ ] Published versions are immutable; editing a draft does not hide the published version until next publish; new tickets pin published version; in-flight tickets continue against pinned version. Pin is internal.
- [ ] All five step types render and complete under linearity (one pending task at a time) with actor gates from Product Rules (requester vs assignee vs claim vs least-loaded).
- [ ] Both `assign_to_desk` strategies persist a person and atomically transition `new→in_progress` after successful assignment, recording assignment and state audits; a pending claim does not change state. `least_loaded` counts `new|in_progress` only and ties by lower `user.id`.
- [ ] `resolve_ticket` and `close_ticket` are each standalone terminal final steps (at most one per workflow, no steps follow either; validator rejects `resolve` before `close` and any non-terminal placement). `resolve_ticket` executes via `Ticket.Transition(resolved)` (from `closed` no-ops, from `cancelled` rejects). `close_ticket` resolves and closes atomically when needed: from `new`/`in_progress` executes `Transition(resolved)` then `Transition(closed)` with two audit events in one unit of work; from `resolved` only `closed`; from `closed` no-ops; from `cancelled` rejects. Both go through `Ticket.Transition` and never bypass the state machine; `ResolvedAt/ClosedAt` stamped only there.
- [ ] Workflow completion without a terminal step leaves the ticket in its current state: normally `in_progress` after assignment, but potentially `new` when no assignment occurred. Later manual `Transition` still respects `CapEditTicket` scoped rules.
- [ ] `form[assignee]` answers are visible to the requester on detail/timeline; no impersonation path exists for `form[requester]`.
- [ ] Automatic state audits have `actor='workflow'`, `actor_user_id=NULL`, rendered as system label; no synthetic user created. `close_ticket` from `new`/`in_progress` produces two atomic audit events.
- [ ] Existing tickets (`workflow_version_id=NULL`) remain readable; categories without a workflow definition are simply unavailable for new tickets (no draft rows backfilled; draft created lazily on first builder open).
- [ ] `tickets_fts` unchanged; form answers not indexed.
- [ ] Four representative cases verified end-to-end, including offboarding ending directly in `close_ticket` that resolves and closes atomically when needed without a preceding `resolve_ticket` step.
- [ ] `GET /tickets/new` renders no assignee control and `POST /tickets` accepts no assignee for any role; a creation request carrying an assignee parameter is rejected with a validation error and no ticket is created; every successfully created ticket starts with an empty assignee.
- [ ] A pending `manual_task` shows its pinned Instructions; completing posts an OPTIONAL solution; pending presentation has no ordered-list numbering and never shows `Mark the current task as complete.`; other pending forms remain contextual to their pinned fields/actions.
- [ ] The timeline entry for a completed manual task names the pinned task (instructions from the pinned version at that step index — stable across later publishes) and shows the non-empty submitted solution as escaped plain text, attributed/timestamped by the existing completion event; assertions prove the solution appears in neither `audit_events.note` nor `tickets_fts`.
- [ ] Completed form results render as semantic `dl/dt/dd`, responsive, newest-first, all values escaped, with no ticket-facing technical `workflow` wording (verified in detail goldens).
- [ ] `gofmt`, `go vet ./...`, `go test ./... -count=1 -race` green; builder and detail goldens stable.

## Risks and Mitigations

| Risk | Mitigation |
|---|---|
| **`resolved` vs `closed` matrix violation** — `resolve` on already-`resolved` or `close` on `cancelled` surfaces as `ErrInvalidTransition` | Builder validator enforces terminal ordering (D4: at most one terminal final step, no steps after it, mutual exclusion of `resolve`/`close`); runner checks `Ticket.State` before `Transition` and handles atomically: `close` from `new`/`in_progress` does `resolved→closed` as two transitions in one unit of work, from `resolved` only `closed`, from `closed` no-ops, from `cancelled` rejects. Never bypasses state machine. |
| **Person-only invariant broken** — persisting `desk_id` as assignee | `assign_to_desk` always resolves to a person via `DeskStore.ListMembers` + load query; reuse `Assign` path; `handlers_tickets:509` guard stays; tests assert `tickets.user_id` is a person. |
| **Non-deterministic `least_loaded`** | Documented scope (`new|in_progress`, all categories, excludes resolved/closed/cancelled) +`ORDER BY count ASC, user.id ASC`; fake and SQLite tests lock tiebreak. |
| **Impersonation / visibility regression** | Actor gates in application before view composition; `form[requester]` checks `RequesterUserID`; `form[assignee]` checks `Ticket.UserID`; assignee answers included in `TicketView` for `ScopeOwned` readers; admin path requires audited self-assign. |
| **Workflow vs ticket lifecycle confusion** | Run completion itself does not change state. Only successful `assign_to_desk` moves `new→in_progress`, atomically with person assignment; terminal steps resolve/close; all later manual transitions remain scoped. |
| **Golden churn masking authz defects** | Isolate service/handler assertions before regenerating goldens (`-update`); keep `strict_tdd` RED→GREEN order (domain → application → SQLite → HTTP/goldens). |
| **SQLite migration drift / FK breakage** | `0006` additive only, `IF NOT EXISTS`, `CHECK(json_valid(...))`, `BEGIN IMMEDIATE` + `schema_migrations` row in same tx; no backfill rows for existing categories; foreign key `tickets.workflow_version_id → workflow_versions.id` without cascade; `NULL` stays valid. |
| **Hidden assignee path survives removal** — stale/hidden client keeps sending `assignee` and the handler ignores it or, worse, still binds it | `POST /tickets` binds no assignee field; a request carrying one MUST be rejected with a validation error; handler tests assert `422` + no ticket created for assignee-carrying posts from every role. |
| **Solution/instructions injection or leakage** — solution HTML-escaped wrongly, or copied into notes/FTS | Solutions and instructions render as escaped plain text only; timeline tests assert non-membership in `audit_events.note` and `tickets_fts`; golden tests lock escaping. |
| **Pinned instruction drift** — timeline renders instructions from the live published version instead of the pinned one | Timeline joins the immutable pinned version at `step_index`; a test covers a re-publish after completion showing the original pinned instructions. |
| **Presentation regressions** — numbering/generic message return, or completed-results reskin breaks `dl/dt/dd`, responsiveness, or escaping | Detail-page goldens + a11y/responsive checks (keyboard focus, reduced motion, narrow viewport) as part of S4; copy locked by assertions (no `Mark the current task as complete.`). |

## Rollback

This is a **non-production, private repo** (no production users). No compatibility layer or long-lived legacy branch is required.

- **Before merge / before `0006` on shared branch:** revert binary/commit. No data change.
- **After `0006` merged / applied locally:** run inverse migration (new `0007` or ad-hoc) that drops `ticket_form_answers`, `ticket_workflow_tasks`/`runs`, `workflow_versions`, `category_workflows` (reverse dependency order), drops `tickets.workflow_version_id` column via recreate-if-needed (SQLite), and removes `schema_migrations` row for `0006`. Existing `tickets`/`categories` rows survive; `workflow_version_id` data is discarded (acceptable — workflows were published/draft only in dev, and categories without workflows simply remain without definition as before). Existing tickets with `NULL` pin are unaffected. Alternative for single-dev: `rm $TKT_DB_PATH` and re-`Migrate`.
- **No backfill reversal needed** because migration created no draft rows for existing categories — they revert to name-only, unavailable-for-new-tickets until a workflow is published; and existing tickets keep `NULL` pin.
- **Amendment adds no migration surface:** `manual_task.instructions` lives in the pinned `workflow_versions` step JSON and solutions in the workflow task/answer rows — both belong to the same additive `0006` scope already planned. Rolling back the amendment is a code + golden revert within that same schema; existing tickets keep `NULL` pin and remain readable. `desks-ux-polish` artifacts remain untouched.

## Dependencies

- Requires `strict_tdd: true` stack (`go test ./...`, `net/http/httptest` + in-memory SQLite, `gofmt`, `go vet`).
- Builds on `policy.go` (`CapManageCategories`, `ScopeOwned/Assigned/All`), `domain/ticket.go` `Transition`, `TicketUnitOfWork`, `Desk` person-only comment, and HTMX/`Renderer` shell.
- Does not depend on `desks-ux-polish` beyond shared `Desk` model; archived artifacts remain untouched.
- Design must decide SQLite driver (already `modernc.org/sqlite`) and confirm `steps_json` storage; spec must cover Given/When/Then per product rule.

## Review Workload Warning

**Expected authored lines >400 ⇒ 400-line budget WILL be exceeded.** Exploration estimated medium effort but multi-surface (domain, application, SQLite migration/stores, HTTP handlers, templates/goldens). Delivery strategy is `exception-ok`: ONE final PR with an accepted `size:exception`, commits grouped by work unit per the work-unit-commits skill. Amendment 2 shifts roughly 0–50 net authored lines downward in S2 (assignee form/parameter removal) and adds ~100–200 in S4 (instructions/solution/timeline presentation); the overall >400 forecast stands.

**Planned slices (decision needed before `sdd-apply`):**

1. **S1 — Schema + Domain + Stores** (migration `0006`, `domain/workflow.go` validation invariants, `WorkflowStore` + `TicketStore` pin extension, SQLite tests) — ~250–350 lines authored.
2. **S2 — Workflow Service + Pinning** (`WorkflowService` draft/publish/validate/preview, `TicketService.Create` usable-category check + pin + unassigned-only creation with assignee-parameter rejection, application fakes, policy reuse) — ~200–300 lines.
3. **S3 — Builder UX** (`GET/POST /categories/{id}/workflow`, vertical `<ol>` builder, HTMX step fragments, `category_workflow` page + partials, handler + golden tests) — ~250–350 lines.
4. **S4 — Ticket Runtime + Pending Card** (run cursor, `POST /tickets/{id}/workflow/tasks/{taskId}/complete`, `claim`/`least_loaded` execution, `resolve/close` auto-advance, `TicketView` pending card + timeline visibility, manual instructions + optional solution, completed-results `dl/dt/dd` timeline treatment) — ~250–350 lines.

Each slice targets <400 authored lines (goldens excluded). If the user chose `single-pr`, an explicit `size:exception` is required before `apply`.

---
*Source: `openspec/changes/category-workflows/exploration.md` (Options A/B/C, 14 ambiguous points, four representative workflows, UX implications, migration/testing risks). Interactive product-question round already completed (permissions, migration, least-loaded scope, publish defaults resolved). **Amendment 2 (approved):** unassigned-only creation, `manual_task` pinned instructions + optional solution with timeline reporting, pending presentation cleanup, completed form results treatment. Amendment defaults flagged for confirmation before `sdd-design`: D13 (`manual_task.instructions` required at publish), D14 (reject assignee-carrying creation requests), D16 (numbering removal scoped to ticket-facing presentation; the builder keeps ordered step authoring), and the reading that post-creation assignment is exclusively via the fixed flow with the approved audited self-assign (rule 6) retained only for actor-owned completion.

## Amendment 3 — Approved UI and pinned-claim scope

This amendment is additive to the recorded Amendment 2 evidence and supersedes only its conflicting workflow-claim presentation/reason assumptions.

1. Native workflow-configurator step selects remain native, semantic controls and are styled with tkt tokens only; HTMX/autosave, keyboard focus, high contrast, and narrow layouts are preserved.
2. Categories use a semantic `Category` / `Created` / `Status` / `Actions` table with an accessible destructive-action overflow and a no-horizontal-overflow mobile stack. The supplied screenshot is structural reference only.
3. Desks use a simple responsive master/detail surface with list member counts, selected detail, new-desk disclosure, rename/delete, and add/remove-member flows. The supplied screenshot is structural reference only.
4. For a pinned `assign_to_desk[claim]`, Pending Actions/timeline contain no claim or reason form. The Assignment sidebar shows Desk and current Assignee and renders `Assign to me` only to an active `agent`/`admin`/`root` current member of that pinned desk. The existing workflow completion route and UoW are authoritative and recheck pin, cursor, activity, role, and membership in one transaction; first concurrent claim wins and typed stale/membership/activity failures write nothing.
5. Workflow claims are reasonless, including true A→B claims. This removal is limited to the workflow claim command/operation pipeline. Generic `POST /tickets/{id}/assign` and `TicketService.Assign` retain their reason requirement, and historical audit reasons remain visible.
6. A successful claim produces exactly one `Assigned to {person} · {desk}` timeline event plus the existing optional `new→in_progress` transition. Copy stays plain; screenshots never override tkt palette, typography, focus, spacing, or simple product philosophy.

Implementation and delivery remain separate: Amendment 3 tasks define work units and verification, but no PR or delivery decision is made until implementation evidence exists.

## Amendment 4 — Approved focused UI corrections

This amendment is additive to the recorded Amendment 1–3 evidence and changes presentation only; it does not change routes, authorization, workflow semantics, or the delivery decision.

1. Category and desk management replace the `More actions` disclosure with directly visible destructive submit buttons labelled exactly `Delete category` and `Delete desk`. Existing POST routes, server-side authorization, rejected-delete inline errors, keyboard focus, and responsive usability remain authoritative; no client-side authority or replacement menu is introduced.
2. The current pending form and manual task in ticket activity use the supplied `Current task` card structure. Native labels, required semantics, fields/selects/checkboxes, optional manual solution, and submit/completion behavior remain unchanged. The card background uses exactly `var(--amber-soft)`; no new color or blue treatment is introduced. Historical completed timeline events retain their merged-timeline semantics and ordering unless a narrow wrapper is required for visual coherence.
3. The builder adopts the reference information architecture only: top header/actions, a responsive desktop two-panel layout with the semantic ordered editor on the left and a read-only vertical linear-flow preview on the right, and stacked mobile panels with no horizontal overflow. The preview is server-rendered from the submitted linear draft and may use restrained static connectors; it is not a graph, canvas, editor, branching surface, drag-and-drop control, client-side state store, or new workflow semantic.
4. Existing tkt palette, typography, spacing, simplicity, accessibility, SSR/HTMX/full-page parity, autosave, native controls, reorder/add/remove behavior, focus restoration, live announcements, preview, and publish behavior remain authoritative. Screenshot references inform structure only.

Amendment 4 preserves `strict_tdd: true` and mandatory isolated Playwright evidence. Its executable corrections and browser evidence must complete before Amendment 3 D.3 and Amendment 2 WC.4 may be closed; delivery/PR selection remains deferred.
