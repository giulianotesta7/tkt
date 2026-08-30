# Proposal: Agent Downgrade Ticket Handoff

> **Outcome first:** For GitHub issue #47 (type:bug, area:tickets+area:users), a managed role change targeting role `user` for an account holding desk memberships must become ONE atomic lifecycle operation: delete desk memberships, hand off open assigned tickets, flip the role with today's guarded semantics, and record the role change — all or nothing. Today the SQLite trigger `trg_users_no_desk_member_downgrade` (`internal/adapters/sqlite/migrations/0004_desks.sql:30`) aborts any such downgrade, surfacing a generic 500, and removing memberships first would leave open tickets assigned to an account that can no longer process them.

## Intent / Problem

Two observable defects share one lifecycle seam:

- **Downgrade currently impossible:** the trigger raises ABORT whenever `users.role` moves to `user` while `desk_members` rows exist. The user-edit handler has no typed error mapping for it, so the admin sees a generic `Internal server error` (500).
- **Orphaned open tickets if memberships were removed first:** deleting `desk_members` rows in a separate transaction and then downgrading would leave every open (`new`/`in_progress`) ticket assigned to a role-`user` account that cannot process tickets.

Both defects stem from treating one lifecycle event (downgrade) as several independent writes.

### Alternatives considered

| Alternative | Verdict |
|---|---|
| (a) Keep blocking and require manual membership removal before downgrade | Rejected: broken admin UX (generic 500, no guidance) and still orphaned open tickets on an account that can no longer process them |
| (b) Loop `TicketService.Assign` per ticket from the user service | Rejected: N separate transactions (no atomicity), requires the initiating actor to hold assignment capability for every ticket, and a per-ticket failure mid-loop leaves half-mutated state |
| (c) New sealed `WorkflowOperation` type routed through the workflow unit of work | Rejected: coupled to pinned-run step context, and semantic audit events there mandate a non-NULL `step_index`, while the handoff happens outside any pinned run (step index MUST be NULL per the audit-log spec) |

**Chosen:** a single atomic `UserStore` operation routed explicitly from the service when the target role is `user`, performing membership deletion, per-ticket handoff, guarded role flip, and the `role_changes` insert inside ONE `BEGIN IMMEDIATE` transaction.

## Scope

### In scope

- Service route: `UserService.UpdateManagedUser` routes any managed role change to `user` whose account holds desk memberships through the new atomic store operation (applies to `agent` and `admin` alike — the trigger blocks both; the issue's example is Agent→User).
- New `UserStore` operation: ONE `BEGIN IMMEDIATE` transaction that (a) deletes the account's `desk_members` rows, (b) for each ticket in state `new`/`in_progress` assigned to the account (ordered by ticket id) performs handoff, (c) updates the role with the same guarded UPDATE semantics as today, (d) inserts the `role_changes` row, (e) commits — all or nothing.
- Desk resolution per ticket, in priority order: (i) `desk_id` of the latest audit event for that ticket that carries one (assignment context snapshot), else (ii) the desk of the first `assign_to_desk` step (by step order) in the ticket's pinned workflow version, else (iii) unresolvable → the ticket becomes unassigned.
- Replacement selection reusing the deterministic least-loaded rule over the resolved desk's membership pool: `active=1`, role in `agent`/`admin`/`root`, fewest open (`new`/`in_progress`) tickets globally, lowest user id tie-breaker; the downgraded account is never a candidate because its memberships are already deleted inside the same transaction before selection.
- Handoff audit events: one per reassigned/unassigned open ticket — `ActionUpdate` on field `user` following the existing `Ticket.ApplyUpdate` event convention (from/to values), actor = the initiating admin (actor user ID set), reason identifying the role downgrade, `DeskID` = resolved desk when available, `StepIndex` = NULL (outside any pinned workflow run).
- HTTP drawer flow keeps working: no generic 500; typed error mapping so admins get meaningful feedback.
- Go tests at application/sqlite/http layers plus an E2E journey.
- Four capability deltas under `specs/`: user-management (ADDED), desk-management (MODIFIED), ticket-access-assignment (ADDED), audit-log (ADDED).

### Out of scope (non-goals)

- Schema migrations: `0004_desks.sql` triggers remain as a defense-in-depth guard; the service now pre-empts them by routing through the atomic operation.
- Workflow engine changes (`internal/adapters/sqlite/workflow_uow.go` untouched).
- Deactivation (`active=false` without role change) semantics: untouched; the user-management "Historical assignments preserved" scenario remains valid.
- Canonical `openspec/specs/**` edits: reserved for the archive phase.
- UI redesign of the user drawer; only the downgrade path's error surface changes from generic 500 to typed feedback.

## Deliverable 1: user-management — Agent Downgrade Ticket Handoff (ADDED)

`specs/user-management/spec.md` adds the atomic downgrade-handoff requirement: success removes memberships, reassigns open tickets, flips the role, and records the role change together; any failure rolls back everything together; unresolvable desk context or an empty eligible pool leaves a ticket unassigned; deactivation without role change still preserves assignments.

## Deliverable 2: desk-management — Desk Membership (MODIFIED)

`specs/desk-management/spec.md` modifies `Desk Membership` so the role-`user` exclusion is upheld automatically: the downgrade handoff removes memberships atomically instead of the system rejecting the downgrade via trigger abort.

## Deliverable 3: ticket-access-assignment — Automatic Reassignment on Downgrade (ADDED)

`specs/ticket-access-assignment/spec.md` adds the deterministic reassignment contract: desk resolution priority, least-loaded selection with lowest-id tie-break, eligibility restrictions, and unassignment fallbacks.

## Deliverable 4: audit-log — Downgrade Handoff Audit Events (ADDED)

`specs/audit-log/spec.md` adds the audit contract: every automatic reassignment or unassignment records an assignment audit event with the initiating actor, a role-downgrade reason, `DeskID` when a desk was resolved, and NULL `StepIndex`; the role change is recorded in `role_changes` as today.

## Validation

- `openspec validate --all --strict --no-interactive` MUST pass with the new change recognized.
- `openspec validate --archived --no-interactive` MUST pass.

## Unblocks

Once merged, agents and admins holding desk memberships can be downgraded successfully; open tickets are handed off deterministically with a complete audit trail; the trigger remains as a defense-in-depth guard that the service path no longer hits.
