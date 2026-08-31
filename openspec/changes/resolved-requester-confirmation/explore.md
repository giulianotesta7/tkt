# Exploration: resolved = awaiting requester confirmation (issue #55)

Codebase map for the proposal/design phases. All file:line references are valid in the
worktree `feat/55-resolved-requester-confirmation`. Exploration only — no decisions made
on the open design questions.

---

## 1. Domain layer (internal/domain/)

### 1.1 States and `IsClosed` — internal/domain/state.go

- Five states: `new`, `in_progress`, `resolved`, `closed`, `cancelled` (state.go:8-13).
- `IsClosed(s)` groups **resolved + closed + cancelled** as "closed (read-only)"
  (state.go:18-25). This single predicate drives the entire read-only surface:
  comments, edits, workflow non-terminal guard, and the UI `Closed` flag.
- Transition matrix, `transitions` map (state.go:29-44):
  - `new` → in_progress, resolved, cancelled
  - `in_progress` → resolved, cancelled
  - `resolved` → **closed**, in_progress (reopen, no reason) ← state.go:38-40
  - `closed` → in_progress (reopen, **reason required**)
  - `cancelled` → {} (terminal)
  - The issue's core observation is exactly state.go:39: any agent can manually move
    resolved → closed. There is no notion of "who/what closed it".

### 1.2 Ticket aggregate and `Transition` — internal/domain/ticket.go

- Lifecycle fields: `ResolvedAt *time.Time`, `ClosedAt *time.Time` (ticket.go:32-33).
  Set and cleared ONLY by `Transition` (doc comment ticket.go:9-10; edits never touch
  them, ticket.go:143-145).
- `RequesterUserID *int64` (ticket.go:20-24): immutable creating-session user id; NULL
  = "legacy ticket without a provable creator" (ticket-access semantics). This is the
  direct terrain for the design question on `requester_user_id IS NULL`.
- `WorkflowVersionID *int64` (ticket.go:28): nil = legacy pre-workflow ticket. The
  workflow-vs-manual split the issue asks for already has a persisted discriminator.
- `Transition(to, reason, now)` (ticket.go:39-89):
  - Rejects illegal moves (`InvalidTransitionError`).
  - Reopen from closed requires non-empty reason → recorded in the audit `Note`
    (ticket.go:51-59).
  - Entering resolved stamps `ResolvedAt`; entering closed stamps `ClosedAt`;
    reopen in_progress clears `ResolvedAt` (and `ClosedAt` from closed)
    (ticket.go:61-73). **Entering closed from resolved does NOT clear ResolvedAt.**
  - Refreshes `UpdatedAt` (ticket.go:75).
  - Returns one `AuditEvent{Action: transition, Field: "state", From, To, Note}`
    (ticket.go:76-87). **No closed-via / confirmation attribution exists anywhere in
    the event schema** — this is the audit-trail gap the issue requires us to close.

### 1.3 Audit event model — internal/domain/audit.go

- Actions (audit.go:9-14): `created`, `transition`, `update`, `workflow_step`
  (legacy read-only), `workflow_assignment`, plus workflow manual/form semantic
  actions (`workflow_manual_task`, `workflow_assignee_form`,
  `workflow_requester_form` — referenced in workflow_runner.go:166-168).
- `AuditEvent` fields (audit.go:26+): TicketID, Actor, ActorUserID, Action, Field,
  FromValue, ToValue, Reason, Note, DeskID, StepIndex, CreatedAt.
- Existing actor conventions (audit-log spec): manual = session user + ActorUserID;
  workflow = actor `"workflow"` + NULL ActorUserID. A requester-confirmation audit
  path has no convention yet.

---

## 2. Application layer (internal/application/)

### 2.1 Manual transition — ticket_service.go

- `TicketService.Transition` (ticket_service.go:287-310): single gate
  `NewPolicy().Capabilities(actor.Role).Require(CapEditTicket)` → role `user` can
  NEVER transition (error `ErrMsgUserCannotTransition`); scoped read restricts agents
  to assigned tickets; domain `Transition`; actor stamped; one atomic
  `s.tx.Update` (ticket + audit).
  - **No requester path exists**: today a requester (role user) is structurally
    locked out of any state change. A requester-confirmation capability is a new
    authorization concept beyond CapEditTicket.
- `TicketService.Update` (ticket_service.go:318+): refuses edits on
  `domain.IsClosed` states (ticket_service.go:~348-351) — resolved is read-only
  for field edits.
- `CommentService.Add` (comment_service.go:50-55): refuses comments on
  `IsClosed` tickets — resolved is read-only for comments.

### 2.2 Workflow terminal handling — workflow_runner.go

- `PlanComplete` read-only guard (workflow_runner.go:52-55): a NON-terminal step
  must not complete on an `IsClosed` (resolved/closed/cancelled) ticket; terminal
  steps follow their own matrices.
- `applyTerminal` (workflow_runner.go:~250-296) — the exact matrices:
  - `resolve_ticket`: new/in_progress → resolved (1 audit); resolved/closed →
    completed no-op (no audit); cancelled rejects.
  - `close_ticket`: new/in_progress → resolved **then** closed (2 ordered audits);
    resolved → closed (1 audit); closed → no-op; cancelled rejects.
  - Every planned transition stamps actor `"workflow"`, NULL user id
    (`transition` closure workflow_runner.go:~260-269).
- `advanceAutomatics` (workflow_runner.go:~310-340): walks automatic steps after a
  human completion; terminal steps end the run (`nextCursor = len(snap.Workflow)`).
- `requireFormActor` (workflow_runner.go:~198-214): strict identity for
  requester/assignee form actors — **the existing precedent for an actor-type check
  keyed on `RequesterUserID`** (requester form accepts only the ticket requester,
  no role bypass). Directly reusable pattern for a requester-confirmation action.
- `inProgressTransitionOp` (workflow_runner.go:~220-235): new→in_progress workflow
  transition; audit actor "workflow".

---

## 3. SQLite adapter (internal/adapters/sqlite/)

### 3.1 Schema / migrations

- Migrations are embedded `migrations/0001_init.sql` … `0009_ticket_manual_solutions.sql`;
  latest = **0009**. Next migration would be 0010 (tests reference files directly:
  backfill_test.go:25, sqlite_test.go:208).
- `tickets` DDL (0001_init.sql:26-40): `state TEXT CHECK(state IN
  ('new','in_progress','resolved','closed','cancelled'))`, `resolved_at TEXT`,
  `closed_at TEXT`. **No closed-via / confirmation column exists.**
- `workflow_version_id` added by 0006_category_workflows.sql:23 (nullable).
- `requester_user_id` added by 0003_roles_and_views.sql:21 (nullable, FK users).
- `audit_events` DDL: 0001_init.sql:54-67 (actor, action, field, from_value,
  to_value, note, created_at) + `actor_user_id`/`reason` (0003:56-57) +
  `desk_id` (0007:7) + `step_index` (0008:7, nullable with explicit design note).
- State values are CHECK-constrained in the DDL — any new state value would need a
  migration; the issue does NOT request new states, only reinterpreted semantics.

### 3.2 Persistence — ticket_store.go

- `ticketColumns` (ticket_store.go:41) includes requester_user_id,
  workflow_version_id, resolved_at, closed_at.
- Insert (ticket_store.go:72-76) and `updateTicketTx` (ticket_store.go:109-115) write
  the full row; timestamps scanned back at ticket_store.go:342-356.
- `appendAuditEventsTx` (ticket_store.go:295-300) is the shared audit insert used by
  both the manual unit-of-work and the workflow UoW.

### 3.3 Workflow unit of work — workflow_uow.go

- `ApplyWorkflowPlan` (workflow_uow.go:~140-230): load-plan-recheck; every expected
  fact rechecked before writes; typed `ErrWorkflowPositionConflict` on mismatch.
- Terminal validation mirrors the runner exactly:
  - `validateTerminalMatrix` (workflow_uow.go:~570-590): resolve = exactly 1
    transition ending resolved; close = 1 or 2 ordered transitions ending closed.
  - `terminalNoopValid` (workflow_uow.go:~560): resolve no-op from resolved/closed;
    close no-op from closed.
  - `validateTransitionOp` (workflow_uow.go:~1100+): workflow transition audits MUST
    have actor "workflow", NULL ActorUserID, no reason/note; legality re-proven via
    `Ticket.Transition` on a copy.
- Any new closed-path (e.g. requester confirmation performed inside a run's plan)
  would have to extend these validators; the simpler shape is a non-workflow
  service transition (manual UoW) that never touches the workflow plan machinery.
- Terminal matrix tests: workflow_uow_terminal_test.go:56-64 (sub-test table),
  :172 (no-op expectations).

---

## 4. HTTP / UI layer (internal/adapters/http/, web/templates/)

### 4.1 Routes — handlers_tickets.go:59-68

```
GET  /tickets               list (state filter incl. resolved)
GET  /tickets/{id}          detail (show)
POST /tickets/{id}/edit     field edits (refused on closed states)
POST /tickets/{id}/assign   assignment
POST /tickets/{id}/transition   manual state change
POST /tickets/{id}/comments     comments (refused on closed states)
POST /tickets/{id}/workflow/steps/{position}/complete   workflow completion
```

- No public/unauthenticated routes exist anywhere (auth middleware gates all;
  only /login, /setup, /healthz are special). An email-token or public-link
  confirmation surface has zero existing infrastructure.
- HTMX pattern (D6): `HX-Request` header → swap fragment, else full page; mutations
  respond 303 redirect or fragment re-render; workflow completion pins 200
  (completeWorkflow, handlers_tickets.go:~700+).

### 4.2 Presentation of legal transitions — handlers_tickets.go

- `allowedNext(s)` (handlers_tickets.go:~160-175): presentation mirror of the domain
  matrix. `resolved` renders targets **closed** and in_progress (reopen). This is
  where an agent-facing "Close" button lives; a requester-confirmation control would
  be a separate, role/identity-gated surface.
- `detailData.Closed` (handlers_tickets.go:~395-400): `domain.IsClosed` hides the
  inline edit, properties/assignment controls, and comment form; only the State
  control remains. If resolved gains a requester-only exception, this flag (and the
  template conditions below) is the UI terrain.
- `pendingFor` (handlers_tickets.go:~470+): builds the Pending Actions card;
  for `form[requester]` steps sets `CanAct` when `t.RequesterUserID == actor.ID` —
  **the existing in-app requester-action precedent** (identity-keyed, role-agnostic).

### 4.3 Templates — web/templates/

- `partials/ticket_detail.html:94-110`: the transition form (`select#ticket-state`
  + auto-submit + reveal-on-demand reopen-reason field, `data-needs-reason`
  attribute; `hx-post` → `#ticket-detail` outerHTML swap).
- `partials/ticket_detail.html:112-119`: lifecycle-meta `<dl>` rendering
  `Resolved` / `Closed` timestamps from ResolvedAt/ClosedAt.
- `partials/styles.html:94-95`: `.badge.resolved` (green) vs `.badge.closed` (gray)
  — visual distinction already exists.
- `partials/styles.html:236-237`: timeline colors `st-resolved` / `st-closed`.
- Golden files: internal/adapters/http/golden_test.go (state_badge golden :191,
  ticket_detail goldens :195+; regenerate with `go test -run TestGolden -update`);
  testdata/*.golden include ticket_detail.golden, timeline.golden, state_badge.golden.
  Any template change to the state section regenerates these.

---

## 5. Tests (where behavior is pinned)

| Layer | File | What it pins |
|---|---|---|
| domain | internal/domain/state_test.go | TestIsClosed :63; full transition matrix table :91-117 (resolved→closed allowed, new→closed invalid); reopen-reason; audit event shape :167-172 |
| domain | internal/domain/ticket_test.go | ApplyUpdate never touches lifecycle timestamps |
| application | internal/application/workflow_runner_terminal_test.go | resolve/close matrices, no-ops, ClosedAt stamping :26-202 |
| application | internal/application/comment_service_test.go:92-93 | no comments on resolved/closed |
| application | internal/application/search_service_test.go:109-116 | state filter incl. resolved |
| adapters/sqlite | workflow_uow_terminal_test.go:56-64, :172 | persisted terminal matrices + no-ops |
| adapters/sqlite | migration_0003_test.go, migration_0008/0009_test.go | migration backfills for audit/requester columns — the pattern any 0010 migration test would follow |
| http | handlers_tickets_test.go | list filters :268-279; transitions; scoped access |
| http | handlers_amendment4_test.go | current-task card contracts |
| http | golden_test.go + testdata/*.golden | template snapshots (state badge, detail, timeline) |
| e2e | e2e/tests/tickets.spec.ts:199-234 | transition new→in_progress journey with timeline event |
| e2e | e2e/tests/ticket-detail.spec.ts:56-97 | comment form hidden on closed states; drives new→in_progress→resolved→closed via the transition endpoint |
| e2e | e2e/README.md:29,48,50 | coverage table rows that assert today's resolved/closed behavior |

Key tests that will CHANGE under this feature: the matrix table rows allowing
resolved→closed for everyone (state_test.go:104), `allowedNext` presentation,
e2e ticket-detail resolved→closed step, and every terminal no-op test that relies on
resolved being "already terminal lifecycle state" for the workflow (those stay valid
— workflow close_ticket is the sanctioned path).

---

## 6. Affected specs (delta targets)

Read under openspec/specs/:

1. **ticket-state-machine/spec.md** — primary delta target.
   - `State Transition Enforcement`: matrix lists `resolved → closed` as legal for
     authorized agents/admins (Allowed transitions block + scenarios "Valid forward
     path", "Invalid transition rejected"). A requester-confirmation-only path into
     closed for workflow tickets directly modifies this requirement.
   - `Reopen with Reason`: resolved → in_progress reopen without reason — the
     requester "NO" path can reuse/extend this transition (plus workflow detachment,
     which is new).
   - `Resolution and Closure Timestamps`: resolved_at/closed_at semantics only;
     no attribution requirement — will need extending (closed-via audit).
2. **ticket-workflow-execution/spec.md** — `resolve_ticket` (:261) ends at resolved;
   `close_ticket` (:286) resolves-then-closes; "run completion MUST NOT change ticket
   state" (:11). #55 preserves these as the sanctioned workflow path into closed but
   must state that resolved-after-run is a *waiting* state, and whether a requester
   action may occur while a run is still active.
3. **audit-log/spec.md** — `Transition Audit Events` (:15-18) defines the two actor
   conventions (manual actor/user-id; workflow/"workflow"/NULL). A third attribution
   (requester confirmation) needs either a new convention or an extension of the
   manual one with a distinguishing marker; `No Silent Mutations` (:64) still holds;
   `Atomic Workflow Audit Sets` (:91-113) unchanged.
4. **ticket-management/spec.md** — `Lifecycle Timestamps` (:126-134); closed-state
   read-only behavior referenced from Update/detail presentation; `Ticket Detail
   Presentation` (:138) renders the State section the confirmation control would
   extend.
5. **comment-timeline / closed-ticket read-only** — code comments cite a
   "closed-ticket read-only spec" (comment_service.go:50, ticket_service.go Update);
   the pending change `sync-frontend-contracts` already deltas comment-timeline
   `Add Comment` to reject comments on resolved/closed/cancelled. Whether requester
   confirmation is the single read-only exception affects this delta.
6. **role-specific-views / role-authorization** — role user currently transitions
   nothing (ticket-state-machine enforcement + CapEditTicket gate). Requester
   confirmation is a new authorization concept for role users on their own tickets;
   role-authorization spec's "existing role hierarchy and permitted transitions MUST
   remain unchanged" (:64) will need an explicit carve-out.

### Spec-collision note

Two UNARCHIVED changes exist under openspec/changes/:
`sync-workflow-polish-contracts` and `sync-frontend-contracts`. Both already delta
canonical `ticket-management`, `audit-log`, and `ticket-workflow-execution`.
Neither touches `ticket-state-machine`. Merge-order during archive matters: this
change's deltas must apply on top of their synced canonicals, and the
requester-confirmation wording must be written against the POST-sync text (the
syncs are additive scenario/paragraph appends, so conflicts are unlikely but the
archive order should be decided in design).

---

## 7. Other references to resolved/closed/cancelled semantics

- internal/application/workflow_runner.go:52-55 — non-terminal-step guard on IsClosed.
- internal/application/comment_service.go:50-55 — comment lockout.
- internal/application/ticket_service.go Update — edit lockout.
- internal/adapters/sqlite/workflow_uow.go — terminal matrices + `terminalNoopValid`.
- internal/adapters/http/handlers_tickets.go — `allowedNext`, `detailData.Closed`,
  `listStates` (:105) filter order.
- web/templates/partials/styles.html — badge/timeline styling per state.
- e2e/tests/ticket-detail.spec.ts — closed-state UI rejections.
- grep for `ClosedVia|closed_via|ResolvedVia|resolved_via` returned ZERO hits: no
  prior attribution concept exists anywhere (schema, domain, or docs).

---

## 8. Open design-question terrain (mapped, not decided)

1. **Where does the requester confirm?**
   - In-app precedent exists: requester-keyed form actor (`requireFormActor`,
     workflow_runner.go:~198) and requester-keyed `CanAct` (`pendingFor`,
     handlers_tickets.go:~500). Identity = session user == RequesterUserID.
   - Public-link/email-token path has NO infrastructure: no unauthenticated routes,
     no token tables, no email outbound. Cost difference is large.
2. **Should resolved remain read-only (single exception)?**
   - IsClosed is the single predicate behind 4 read-only surfaces (comments, edits,
     UI `Closed` flag, workflow non-terminal guard). A carve-out touches all four
     layers + specs (comment-timeline, ticket-management, ticket-state-machine).
   - Workflow non-terminal guard (workflow_runner.go:52-55) is independent: a
     resolved ticket with an ACTIVE run cannot complete non-terminal steps — any
     confirmation design must decide whether confirmation can arrive mid-run
     (rare: runs end at resolve_ticket typically, run completes, cursor terminal).
3. **requester_user_id IS NULL** (legacy tickets):
   - Semantics already defined: nil = no provable creator, agent+-only visibility
     (ticket.go:20-24). Workflow close path is unaffected. The design must decide:
     can a requester-confirmation flow ever reach closed for these (presumably no
     requester exists to confirm), and does the manual resolved→closed transition
     remain their only closure path?
4. **Manual resolved→closed by an agent for non-workflow/manual tickets:**
   - Discriminator exists: `WorkflowVersionID == nil` (ticket.go:28) separates
     legacy/manual tickets from pinned ones. A policy split (agents may close
     unpinned tickets manually; pinned tickets require workflow close_ticket or
     requester OK) is expressible at the service/policy layer using this field plus
     the audit attribution. Note the runner/UoW close_ticket matrices
     (workflow_runner.go:~296, workflow_uow.go:~570) already treat resolved→closed
     as the workflow's own final step — the domain matrix itself may not need to
     change if the gate is authorization-level (open question for design).

### Cross-cutting facts design must honor

- `Transition` from resolved → closed does NOT clear `ResolvedAt` (ticket.go:61-73);
  closed tickets keep both timestamps. Any "confirmed" semantics ride on top.
- Workflow transition audits are pinned by validators to actor "workflow"/NULL/
  no-reason (workflow_uow.go `validateTransitionOp`); a requester confirmation must
  use a DIFFERENT audit shape or a new action value, or those validators/specs must
  change.
- The 0001 DDL CHECK constrains `state` values; no new state value is proposed.
- Golden tests freeze the detail/timeline templates; UI changes regenerate goldens
  (`go test -run TestGolden -update`).
- e2e ticket-detail.spec.ts actively drives resolved→closed via the transition
  endpoint — this journey changes if the manual path is restricted.

---

## Risk notes

- **Schema gap**: no closed-via attribution column or audit convention exists;
  audit-trail requirement needs a migration (0010) or an audit-action/note
  convention decision.
- **Authorization novelty**: first role-user mutation path; policy layer
  (CapEditTicket) does not express "requester of THIS ticket" — needs either a new
  capability or an identity-based predicate like requireFormActor.
- **Validator fan-out**: workflow UoW validators pin exact audit shapes; any change
  to how close audits look (e.g. adding confirmation markers) ripples into
  workflow_uow.go validators and their terminal tests.
- **Spec sync timing**: two unarchived doc-sync changes share canonical specs;
  archive order + delta base must be pinned in design.
- **E2E/golden churn**: visible UI changes (confirmation control, state copy)
  regen golden files and touch e2e journeys that assert today's read-only UI.
