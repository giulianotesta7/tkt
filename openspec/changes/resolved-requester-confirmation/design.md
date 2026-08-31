# Design: resolved = awaiting requester confirmation

Change `resolved-requester-confirmation` (issue #55). Design phase for the spec deltas
already written in `specs/*/spec.md`. All code references are valid in worktree
`feat/55-resolved-requester-confirmation` (HEAD 120c3a5); code facts were re-verified
against `workflow_uow.go`, `ticket_service.go`, `handlers_tickets.go`, and
`migrations/` this session; everything else cites `explore.md`.

Core architectural stance, used consistently across every decision below: **the
domain state matrix does not change** (`resolved → closed` and `resolved →
in_progress` stay legal in `state.go:29-44`); **the application policy layer decides
WHO may take each path**. This matches the existing split (`CapEditTicket` lives in
`TicketService.Transition`, not in the domain) and is explicitly anticipated by
`explore.md` §8.4. The only domain struct change is the audit attribution field (D1).

---

## D1. Closure attribution: new `closure_via` attribute on the closure transition audit event

**Decision.** Attribution rides on the transition audit event itself as a new nullable
domain field `AuditEvent.ClosureVia *string`, persisted as `audit_events.closure_via
TEXT NULL` (migration 0010, D2). Typed constants in `internal/domain/audit.go`:

- `ClosureViaRequesterConfirmation = "requester_confirmation"` — stamped by
  `TicketService.ConfirmResolution` (D3/D5) on the `resolved → closed` event.
- `ClosureViaManualAgent = "manual_agent"` — stamped by `TicketService.Transition`
  when a requester-NULL ticket is manually closed (D6).
- Workflow-terminal closures: **`ClosureVia` stays NULL and nothing in the workflow
  pipeline changes.** The workflow path is already attributed by the existing actor
  convention (`actor = "workflow"`, `ActorUserID = NULL`, action `transition`,
  field `state`) — and the audit-log delta explicitly requires exactly that: *"the
  audit history shows the closure attributed to the workflow-terminal path AND the
  transition audit events keep the existing workflow actor convention."*

The three closure paths are then distinguishable with zero ambiguity:
`closure_via='requester_confirmation'` / `closure_via='manual_agent'` / human-free
`actor='workflow'` + `ActorUserID IS NULL` + `closure_via IS NULL`. Two paths are
never recorded indistinguishably (audit-log delta, Closure Attribution requirement).

**Why this and not the alternatives:**

| Alternative | Rejected because |
|---|---|
| Distinct new `Action` values per closure path (e.g. `requester_confirmation` as an action) | The audit-log delta requires "at least one **transition** audit event records the entry into `closed`"; replacing the action breaks that reading, and `validateTransitionOp` pins `a.Action == domain.ActionTransition` for every workflow transition (`workflow_uow.go:1131-1137`) — changing workflow closure actions would force runner + UoW + terminal-matrix tests to change in lockstep for zero behavioral gain. |
| `closed_via` column on `tickets` | It is a mutable denormalized snapshot, not the audit trail. A reopen from `closed` then re-close would overwrite it; history would live in exactly one mutable cell. The delta's requirement is about the **audit history** being readable. |
| Attribution metadata inside workflow events only (e.g. new column stamped on all closure events including workflow) | Directly violates the pinned workflow audit shape: `validateTransitionOp` re-proves exact actor/reason/note facts on every workflow plan; stamping a new non-NULL value on workflow closure audits ripples into the validator and its tests for no benefit. |

The mechanism reuses two existing precedents: semantic distinctions already live in
`Action` values *for non-transition events* (`workflow_requester_form`, etc.,
`audit.go:9-14`), and per-event human attribution is already stamped by the service
after `t.Transition` returns (`event.Actor = actor.Name`, `ticket_service.go:299`) —
stamping `ClosureVia` there follows the identical pattern.

**Lockstep changes if the audit event shape changes (mandatory list).** Adding the
field touches exactly these, and nothing else:

1. `internal/domain/audit.go` — add `ClosureVia *string` + the two constants.
2. `internal/adapters/sqlite/ticket_store.go` — `appendAuditEventsTx`
   (`ticket_store.go:295-300`) persists `closure_via` (NULL when nil).
3. `internal/adapters/sqlite/workflow_uow.go` — `validateTransitionOp`
   (`workflow_uow.go:1131+`) gains one **additive assertion**: workflow transition
   audits must carry `ClosureVia == nil` ("workflow closure audit attribution
   mismatch" conflict). This locks the convention cheaply and prevents accidental
   via-stamping inside workflow plans.
4. `internal/application/workflow_runner.go` — **no change** (runner never sets
   `ClosureVia`; the nil-assertion in 3 is the guard).
5. Tests in lockstep: `internal/domain/state_test.go` audit-shape assertions
   (transition events default `ClosureVia` nil; service-stamped paths covered at the
   application layer), `workflow_uow_terminal_test.go` (workflow closure audits
   assert nil `ClosureVia`), migration 0010 test (D2).

Queryability: "all requester-confirmed closures" is a plain
`WHERE action='transition' AND to_value='closed' AND closure_via='requester_confirmation'`;
no index is added now (no feature queries it yet — noted as a future follow-up).

## D2. Migration 0010 — additive, forward-only, no backfill

**Decision.** `internal/adapters/sqlite/migrations/0010_audit_closure_via.sql`:

```sql
-- Closure attribution (issue #55): distinguishes requester-confirmation closure
-- from manual agent closure on closure transition events. Additive forward-only
-- column; workflow-terminal closures intentionally leave it NULL and remain
-- attributed by the existing workflow actor convention. No backfill: closures
-- recorded before this change were written under a policy with no attribution
-- concept, and fabricating closure_via values for them would corrupt history.
ALTER TABLE audit_events ADD COLUMN closure_via TEXT;
```

Rationale:

- **Additive-only DDL** (single `ADD COLUMN`), mirroring the `0007_audit_desk_id` /
  `0008_audit_event_step_index` precedent of extending `audit_events`; `0009`
  confirms the repo convention of forward-only additive migrations with explicit
  design notes in the file header.
- **No backfill** — new semantics affect future transitions only. Historical
  `to_value='closed'` events predate attribution; backfilling would have to guess
  the path (some were workflow closures, some manual) and would fabricate provenance.
  Old rows read back with `closure_via IS NULL`, which the reading convention
  (D1) treats as "pre-attribution history; use the actor convention".
- **No down migration** — consistent with every migration in the directory (there
  is no DOWN section anywhere; tests reference files directly,
  `explore.md` §3.1).
- The `state` CHECK in `0001_init.sql:26-40` is untouched (no new state values).
- Test: `migration_0010_test.go` following `migration_0008/0009_test.go` pattern —
  column exists, legacy rows read NULL, forward-only application, idempotence not
  required (runner applies once).

## D3. Confirmation endpoints + HTMX

**Decision.** One new route mirroring the mutation-handler conventions of
`handlers_tickets.go:58-69`:

```
POST /tickets/{id}/confirmation     h.confirmation    form: decision=confirm|reject
```

- Single endpoint, explicit `decision` form field (`confirm` | `reject`), matching
  how `transition` selects its variant via a form field; unknown `decision` → 422
  validation error, no write.
- Handler dispatches to two new use cases: `TicketService.ConfirmResolution(ctx,
  actor, ticketID)` and `TicketService.RejectResolution(ctx, actor, ticketID)`
  (D5). Two explicit service methods, one shared gate.
- **Authorization predicate (the requireFormActor precedent).** The gate is an
  **identity predicate, not a role capability**: `internal/application/policy.go`
  gains

  ```go
  // isTicketRequester reports whether the actor is the persisted ticket's
  // requester (identity check, no role bypass — the requireFormActor precedent
  // at workflow_runner.go:~198-214). This is deliberately NOT a Capability:
  // capabilities are role-keyed and would wrongly authorize agents/admins,
  // whom the role-authorization delta explicitly denies confirmation.
  func isTicketRequester(actor domain.User, t *domain.Ticket) bool {
      return t.RequesterUserID != nil && *t.RequesterUserID == actor.ID
  }
  ```

  Gate order in both service methods (server-side, before any write, per the
  role-authorization delta): scoped read (`GetByID` with the existing `scopedQuery`
  — the requester can read their own ticket, an out-of-scope role user gets
  ErrNotFound, which satisfies "denied") → `isTicketRequester` else
  `ForbiddenError(ErrMsgNotTicketRequester)` (an agent/admin/root can read the
  ticket and must get 403, per the delta) → state guard via `t.Transition` (confirm
  on non-`resolved` is rejected by the state machine with no write, per the
  state-machine delta). `ErrMsgNotTicketRequester` is the one new error message.
- **Response pattern** — identical to the existing `transition` handler
  (`handlers_tickets.go:688-709`): on success call
  `h.afterMutation(w, r, id, "ticket_detail")`, which already implements the D6
  HTMX convention (HX-Request → `ticket_detail` fragment re-render; else 303
  redirect to `/tickets/{id}`). Errors flow through the existing
  `h.renderDetailError`. No new response machinery.
- Role-`user` requests never reach `Transition`'s `CapEditTicket` gate — the
  confirmation methods are separate entry points, which is precisely the carve-out
  the role-authorization delta requires; every other role-`user` mutation stays
  prohibited (nothing else changed in `NewPolicy().Capabilities`).

## D4. Workflow detachment on rejection

**Decision.** Detachment is a field nil-ing on the domain ticket persisted by the
**existing full-row update**, inside the **same unit of work** as the transition and
its audit event. No new store method, no new SQL statement.

`updateTicketTx` writes the full row from `ticketColumns`
(`ticket_store.go:41,109-115`), which includes `workflow_version_id`
(`explore.md` §3.2). So `RejectResolution`:

1. scoped read → `isTicketRequester` gate (D3);
2. `event, err := t.Transition(domain.StateInProgress, "", now)` — clears
   `ResolvedAt` (existing `ticket.go:61-73` behavior, exactly the ticket-management
   delta "Rejection clears resolved_at");
3. `t.WorkflowVersionID = nil` — application-layer decision, mirroring how the
   service already stamps event fields post-`Transition`;
4. stamp `event.Actor`/`ActorUserID` (requester);
5. ONE `s.tx.Update` call persists ticket row (now `workflow_version_id = NULL`) +
   audit event atomically. Failure of any part rolls back everything.

This satisfies "rejection is a manual transition, not a workflow operation"
(workflow-execution delta): no workflow plan, no cursor/step/answer mutation, the
audit is actor-stamped by the requester, not `"workflow"`.

**Runner guard against post-detachment workflow steps.** After detachment the run
row still exists but the ticket is unpinned. Guards, in order:

- The rejection path itself never enters workflow machinery (manual UoW only — the
  same shape `explore.md` §3.3 already recommends for non-workflow transitions).
- In-flight plans fail on recheck: `ApplyWorkflowPlan`'s load-plan-recheck must
  include the pinned-version fact — the expected ticket `workflow_version_id`
  equals the run's version. If that fact is already in the recheck set, detachment
  automatically fails concurrent plans with `ErrWorkflowPositionConflict`; if it is
  not, apply adds one fact check to the recheck plus a test (one-line guard, typed
  conflict "workflow pin mismatch"). **Apply-phase verification step:** confirm
  which case holds before implementing.
- Presentation guard: `pendingFor` / `claimFor` (`handlers_tickets.go:~470+`)
  treat a ticket with `WorkflowVersionID == nil` as no pending actions
  (`workflowPending{Active:false}` / `workflowClaim{Active:false}`), so a detached
  ticket renders no workflow card.
- Non-terminal guard unchanged: `workflow_runner.go:52-55` still refuses
  non-terminal step completion on `IsClosed` tickets; it is unaffected because a
  rejected ticket leaves `resolved` before anything else can run.

## D5. Domain shape: confirm/reject flow through `Ticket.Transition`; matrix unchanged

**Decision.** No new domain methods, no matrix change, no new states. The matrix
(`state.go:29-44`) keeps `resolved → closed` and `resolved → in_progress` legal —
exactly what the state-machine delta's Allowed transitions block lists. The
**conditionality lives in the application layer**:

- `ConfirmResolution` → `t.Transition(closed, "", now)` after the identity gate;
  stamps `ClosedAt` and keeps `ResolvedAt` (existing `ticket.go:61-73` — matches the
  ticket-management delta "Confirmation closure stamps closed_at AND resolved_at
  remains set"); stamps `ClosureVia = ClosureViaRequesterConfirmation` on the event.
- `RejectResolution` → `t.Transition(in_progress, "", now)` + detachment (D4).
- Manual `TicketService.Transition` gains exactly **one** new policy check before
  calling `t.Transition`: if `from == resolved && to == closed &&
  t.RequesterUserID != nil` → `ForbiddenError(ErrMsgClosureRequiresConfirmation)`
  for every role (state-machine delta: "A manual resolved → closed transition on a
  ticket that has a requester MUST be rejected for every actor"; role-authorization
  delta: "manual closure of the resolved ticket MUST be denied for every actor").
- Requester-NULL manual closure needs **no new code**: existing
  `CapEditTicket` + scoped read + legal matrix transition suffice, and the service
  stamps `ClosureVia = ClosureViaManualAgent` on any resulting
  `resolved → closed` event.
- Lifecycle timestamps need **no code change**: entering `closed` stamps
  `ClosedAt` without clearing `ResolvedAt`; entering `in_progress` clears
  `ResolvedAt` (`ticket.go:61-73`) — verbatim the delta's timestamp semantics.
- The domain tests that pin the audit shape (`state_test.go:167-172`) see only the
  additive `ClosureVia` field (nil by default from plain `Transition`); the
  matrix table rows for `resolved` stay as-is.

Why not new domain methods (e.g. `Confirm()`/`Reject()`): they would duplicate
`Transition`'s legality/stamp/audit logic and split the single enforcement point
the state-machine spec mandates ("validate every state transition against the
allowed matrix"); the deltas describe confirm/reject as *transitions* with policy
conditions, and policy is this codebase's application-layer concern.

## D6. Reopen scoping

**Decision.** Minimal rule, fully consistent with the deltas:

- `agent` (assigned, via scoped read) / `admin` / `root` reopening `resolved →
  in_progress` goes through the existing `Transition` path with **no detachment**
  (`WorkflowVersionID` untouched — neither `t.Transition` nor the service nils it
  on this path). This is the state-machine delta's "agent reopen MUST NOT detach".
- The requester's exit from `resolved` is **only** the rejection path (D4, with
  detachment); role `user` still cannot call generic `Transition` (`CapEditTicket`).
- `closed → in_progress` reopen keeps requiring a reason for everyone (unchanged);
  the reopen-reason UI (`data-needs-reason`) is untouched.
- Manual agent closure remains legal **only** for requester-NULL tickets (the one
  new gate in D5 blocks the rest). No combined `WorkflowVersionID == nil` policy
  split is introduced — the discriminator is `RequesterUserID`, per the proposal's
  confirmed decision 5.

## D7. UI/UX on the detail page

**Decision.** All changes are in `detailDataFor` flags + `partials/ticket_detail.html`;
`allowedNext` stays a pure function and is filtered at the call site.

- New `detailData` fields:
  - `CanConfirm bool` — `state == resolved && isTicketRequester(actor, t)`
    (presentation mirror of the service gate). Drives the **confirmation control**:
    a two-button form in the **State section of the Properties sidebar**
    (`confirm` primary + `reject` destructive, one form posting
    `decision=confirm|reject` to `POST /tickets/{id}/confirmation`, `hx-post` →
    `#ticket-detail` swap per D6 convention). Rendered **only** for the
    authenticated requester of a `resolved` ticket (delta: "MUST NOT render for
    any other actor").
  - Comment-form visibility: replace the single `Closed` boolean's comment gating
    with an explicit `CanComment bool` =
    `!IsClosed(state) || (state == resolved && isTicketRequester(actor, t))` —
    plus the requester-NULL exclusion (requester can't exist when
    `RequesterUserID == nil`, so the identity predicate is false and the form
    stays hidden for everyone, per the comment-timeline delta). The existing
    `Closed` flag keeps hiding edit/properties/assignment controls unchanged
    (resolved stays read-only for those, for everyone).
- `allowedNext` filtering in `detailDataFor` (presentation only; the service is the
  enforcement point):
  - `resolved` + requester exists → drop the `closed` target for **every** actor;
    keep `in_progress` reopen for authorized agents (agent+/CapEditTicket
    holders); the requester sees no Move-to at all (role `user`), only the
    confirmation control.
  - `resolved` + requester NULL → keep both `closed` and reopen for authorized
    agents (delta: "MUST offer closed … in addition to the reopen").
  - All other states unchanged.
- **Agent view of a requester-owned resolved ticket**: no close control (filtered
  Move-to), reopen available, no comment form, no edit/assignment controls
  (existing `Closed` behavior), no confirmation control.
- **Requester view of their resolved ticket**: confirmation control + comment form
  (public visibility per existing role-`user` rule); everything else read-only.
- Lifecycle meta `<dl>` (`ticket_detail.html:112-119`) needs no structural change —
  `Resolved`/`Closed` timestamps render from the existing fields, and D5 guarantees
  the stamping semantics.
- **Golden regeneration plan**: after template edits run
  `go test ./internal/adapters/http/ -run TestGolden -update`
  (`golden_test.go:191-195+`). Existing `ticket_detail` goldens regenerate for the
  filtered Move-to; **add** golden cases: (a) requester-owned `resolved` viewed by
  the requester (confirmation control + comment form, no Move-to), (b) same ticket
  viewed by an agent (reopen only, no comment form), (c) requester-NULL `resolved`
  viewed by an agent (close + reopen). `state_badge`/`timeline` goldens are
  unaffected unless markup there changes (it should not).

## D8. E2E plan (Playwright, per the tkt-e2e skill's journey style)

Extend `e2e/tests/ticket-detail.spec.ts` (and `tickets.spec.ts` where the
new→in_progress→resolved journey is driven) with three journeys:

1. **Requester confirms → closed.** Agent creates ticket for a requester user,
   moves new → in_progress → resolved; requester logs in, sees the confirmation
   control on the detail page (and the comment form), confirms; assert state badge
   `closed`, lifecycle meta shows `Closed` timestamp while `Resolved` remains,
   timeline shows the closure event attributed to the requester (not
   `"workflow"`), and the Move-to close control is gone.
2. **Requester rejects → manual in_progress.** Category with a `resolve_ticket`
   terminal workflow; run completes leaving `resolved`; requester rejects; assert
   state `in_progress`, `resolved_at` cleared from lifecycle meta, workflow
   Pending Actions card absent (detached), timeline shows the requester-attributed
   reopen, and the ticket continues as a manual ticket (no workflow step executes).
3. **Agent close blocked when requester exists.** Requester-owned `resolved`
   ticket viewed by an agent: Move-to offers reopen but not `closed`; a direct
   `POST /tickets/{id}/transition {to: closed}` is denied, the error renders via
   the existing detail-error path, and the state remains `resolved`.

Update the existing journey in `ticket-detail.spec.ts:56-97` that drives
resolved→closed via the transition endpoint (it now violates the closure gate): use
either the requester-confirm flow or a requester-NULL ticket for the close leg.
Update coverage rows in `e2e/README.md:29,48,50`.

## D9. Test plan (strict TDD — RED first, table-driven, one scenario per delta row)

Runner: `go test ./...`. Each delta scenario maps to a named test case; write the
failing test before the implementation in the same file.

| Delta capability → scenario | Go test |
|---|---|
| ticket-state-machine / Agent cannot close requester-owned resolved | `internal/application/ticket_confirmation_test.go`: manual `resolved→closed` with requester → Forbidden for agent/admin/root; state unchanged |
| ticket-state-machine / requester-NULL manual closure | same file: requester-NULL `resolved→closed` by assigned agent/admin → OK, event `ClosureVia=manual_agent` |
| ticket-state-machine / user cannot transition (unchanged) | existing rows stay green (regression) |
| Requester Confirmation Closure / confirm closes | `ticket_confirmation_test.go`: role-user requester on own `resolved` → closed; `closed_at` set; `resolved_at` kept; event `ClosureVia=requester_confirmation`, actor = requester |
| … / confirmation on non-resolved rejected | confirm on `new/in_progress/closed` → state-machine error, no audit row |
| … / confirm by non-requester denied | agent/admin/root/other-user → Forbidden (agent/admin) or NotFound (other role user), no write |
| … / requester rejection (pinned) | reject → `in_progress`, `WorkflowVersionID == nil`, `resolved_at` cleared, audit actor = requester, no workflow ops |
| … / requester rejection (manual ticket) | reject → `in_progress`, no workflow mutation |
| … / agent reopen keeps pin | agent reopen `resolved→in_progress` → `WorkflowVersionID` intact |
| ticket-workflow-execution / resolve leaves awaiting confirmation | `workflow_runner_terminal_test.go`: existing matrices green + new case asserting run completes with ticket still `resolved` |
| … / close_ticket preserved | existing terminal matrix rows unchanged (regression) |
| Requester Rejection Detaches / in-flight plan fails | `workflow_uow_terminal_test.go` or uow test: plan recheck on a detached ticket → `ErrWorkflowPositionConflict` (or pin-mismatch conflict) |
| audit-log / three paths distinguishable | `ticket_confirmation_test.go` + `workflow_uow_terminal_test.go`: assert `closure_via` values per path; workflow closure events keep `actor="workflow"`, `ActorUserID IS NULL`, `ClosureVia == nil` |
| comment-timeline / resolved requester-only | `comment_service_test.go`: requester public comment on `resolved` OK; non-requester rejected; requester-NULL `resolved` rejected for all; `closed`/`cancelled` reject everyone (existing rows) |
| role-authorization / carve-out scope | `ticket_confirmation_test.go`: role user edit/assign/other transitions still denied while confirm/reject allowed |
| migration 0010 | `migration_0010_test.go`: column present, legacy rows NULL (D2) |
| UoW validator lockstep | `workflow_uow_terminal_test.go`: `validateTransitionOp` nil-`ClosureVia` assertion on workflow audits (D1.3) |
| ticket-management presentation | `handlers_tickets_test.go`: confirmation route auth matrix (requester OK; others 403/404; bad decision 422); `allowedNext` filtering per D7 |
| goldens | `golden_test.go` + regenerated/added `testdata/*.golden` (D7) |
| e2e | three journeys (D8) + README rows |

RED-first sequencing: domain/audit field tests → service gate tests → store/UoW →
handler/goldens → e2e. All green before verify phase.

---

## Traceability map

| Spec delta (capability) | Design section | Tests |
|---|---|---|
| ticket-state-machine: conditional closure, requester paths, role carve-out | D5, D6, D3, D1 | D9 rows (state machine, closure gate) |
| ticket-state-machine: Requester Confirmation Closure (added) | D3, D4, D5, D6 | D9 (confirmation/rejection/reopen rows) |
| ticket-workflow-execution: resolved = waiting; close_ticket preserved; rejection detaches | D5, D4, D1 | D9 (runner regression, detachment, in-flight plan) |
| audit-log: Closure Attribution (added) | D1, D2 | D9 (attribution rows, UoW lockstep, migration) |
| ticket-management: timestamps + detail presentation | D5 (timestamps = existing code), D7 | D9 (presentation, goldens) |
| role-authorization: requester carve-out (added) | D3 (identity predicate), D5 | D9 (carve-out rows, handlers auth matrix) |
| comment-timeline: resolved requester-only | D3 (predicate reuse), D7 (form visibility) | D9 (comment rows) |

## Deliverables (apply phase)

Created:
- `internal/adapters/sqlite/migrations/0010_audit_closure_via.sql`
- `internal/adapters/sqlite/migration_0010_test.go`
- `internal/application/ticket_confirmation_test.go`
- golden testdata additions (requester-view cases, D7)

Modified:
- `internal/domain/audit.go` — `ClosureVia` field + constants (D1)
- `internal/application/policy.go` — `isTicketRequester` (D3)
- `internal/application/ticket_service.go` — `ConfirmResolution`,
  `RejectResolution`, manual-closure gate + `ClosureVia` stamping in `Transition` (D5, D6, D4)
- `internal/application/comment_service.go` — resolved requester-only carve-out (D7/D3)
- `internal/application/workflow_runner.go` — only if the pin-recheck fact is missing (D4; verify first)
- `internal/adapters/sqlite/ticket_store.go` — `appendAuditEventsTx` persists `closure_via` (D1)
- `internal/adapters/sqlite/workflow_uow.go` — `validateTransitionOp` nil-`ClosureVia` assertion (D1)
- `internal/adapters/http/handlers_tickets.go` — route + `confirmation` handler, `detailData` flags, `allowedNext` filtering (D3, D7)
- `web/templates/partials/ticket_detail.html` — confirmation control, `CanComment` gating, Move-to filtering renders (D7)
- tests: `state_test.go` (audit shape), `workflow_uow_terminal_test.go`,
  `comment_service_test.go`, `handlers_tickets_test.go`, golden testdata updates
- `e2e/tests/ticket-detail.spec.ts`, `e2e/README.md` (D8)

Explicitly unchanged: domain matrix and `Ticket.Transition` signature
(`state.go`, `ticket.go` mechanics), workflow `close_ticket`/`resolve_ticket`
matrices (runner + UoW terminal tests), `0001` state CHECK, auth middleware.

## Rollback boundary

Per the proposal's rollback plan: the rollback boundary is **code revert with the
migration left in place**. Migration 0010 is additive-only (one nullable column);
after a code revert it is dormant — legacy rows read `closure_via IS NULL`, new
rows written before the revert carry inert string values that no rolled-back code
reads. Detached tickets (`workflow_version_id = NULL`) keep their state and are not
re-attached by anything. No destructive DDL, no down migration, no data purge. A
pure `git revert` of the branch restores the pre-change matrix, read-only `resolved`,
and unrestricted agent closure; the pre-change suites (domain matrix table, terminal
matrices, existing e2e journeys) pass unchanged except for the dormant column.
