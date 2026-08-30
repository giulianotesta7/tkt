# Delta for User Management

Scope note: this is the spec-phase delta for change `agent-downgrade-ticket-handoff` (issue #47). It adds the atomic downgrade-handoff contract for managed users holding desk memberships. No canonical spec file is edited here.

## ADDED Requirements

### Requirement: Agent Downgrade Ticket Handoff

When a managed role change from `/users/{id}/edit` targets role `user` for an account holding desk memberships, the system MUST perform ONE atomic lifecycle operation inside a single `BEGIN IMMEDIATE` transaction: (a) delete the account's desk memberships, (b) for each open ticket (state `new` or `in_progress`) assigned to the account, ordered by ticket id, perform handoff, (c) update the role with the same guarded UPDATE semantics as every other managed role change, (d) insert the `role_changes` row, and (e) commit — all or nothing. Any failure MUST roll back the role, the memberships, and the ticket reassignments together, leaving the account and its tickets exactly as before. Desk resolution per ticket MUST apply in priority order: (i) the `desk_id` of the latest audit event for that ticket that carries one, else (ii) the desk of the first `assign_to_desk` step (by step order) in the ticket's pinned workflow version, else (iii) unresolvable and the ticket MUST be left unassigned. Replacement selection MUST reuse the deterministic least-loaded rule over the resolved desk's membership pool, and the downgraded account MUST never be its own replacement. This handoff applies uniformly to any managed role change to `user` whose account holds desk memberships (`agent` and `admin` alike). A deactivation (`active = false`) without a role change MUST NOT trigger the handoff and MUST continue to preserve historical assignments. The HTTP user-edit flow MUST surface typed, meaningful feedback instead of a generic server error when the downgrade path is exercised.

(Previously: the trigger `trg_users_no_desk_member_downgrade` aborted every downgrade of a desk member, the HTTP surface returned a generic 500, and removing memberships first would have orphaned open tickets assigned to an account that could no longer process them.)

#### Scenario: Downgrade of a desk member succeeds atomically

- GIVEN an `agent`-role account holding desk memberships and an open ticket assigned to them
- WHEN an `admin` submits a managed role change to `user` at the user edit flow
- THEN the response is a success with no generic server error
- AND the account's role is `user` and no `desk_members` row references the account
- AND the role change is recorded in `role_changes`

#### Scenario: Open tickets reassigned to the least-loaded eligible member

- GIVEN a downgraded desk member with an open ticket whose desk resolves via audit context or pinned workflow
- WHEN the atomic handoff runs
- THEN the ticket is reassigned to the eligible member of the resolved desk with the fewest open tickets
- AND each reassignment records an audit event attributed to the initiating admin with a role-downgrade reason

#### Scenario: Unresolvable desk or empty eligible pool leaves the ticket unassigned

- GIVEN a downgraded desk member with an open ticket whose desk cannot be resolved, or whose resolved desk has no eligible member
- WHEN the atomic handoff runs
- THEN the downgrade still succeeds
- AND that open ticket becomes unassigned

#### Scenario: Closed, resolved, and cancelled tickets preserve historical assignment

- GIVEN a downgraded desk member assigned to tickets in states other than `new` or `in_progress`
- WHEN the atomic handoff runs
- THEN those tickets keep their historical assignment to the downgraded account
- AND no reassignment audit event is recorded for them

#### Scenario: Any failure rolls back role, memberships, and tickets together

- GIVEN a managed role change to `user` for a desk member whose handoff encounters a failure inside the transaction
- WHEN the operation aborts
- THEN the role, the desk memberships, and every ticket assignment remain exactly as before the attempt
- AND no role_changes row and no handoff audit event is persisted

#### Scenario: Deactivation without role change preserves assignments

- GIVEN a desk member with assigned tickets
- WHEN an `admin` deactivates the account without changing its role
- THEN the assignments remain untouched and the account keeps its desk memberships per the existing membership rules
- AND the downgrade handoff does not run

## Notes

Traceability for `Agent Downgrade Ticket Handoff` (evidence paths show today's seams; T2–T4 add the implementation and tests):

| Evidence | Path | What it proves |
|---|---|---|
| Trigger that aborts downgrades today | `internal/adapters/sqlite/migrations/0004_desks.sql:30` (`trg_users_no_desk_member_downgrade`) | The blocking defect this change retires via the atomic operation |
| Managed edit seam the service routes from | `internal/application/user_service.go:104` (`UserService.UpdateManagedUser`) | One entry point for identity, role, and status edits |
| Guarded role UPDATE + role_changes insert today | `internal/adapters/sqlite/user_store.go:110` | Guarded UPDATE semantics and audit the operation must preserve |
| Desk membership table and invariant | `internal/adapters/sqlite/migrations/0004_desks.sql` (`desk_members`) | Rows the operation deletes inside the transaction |
| Open-state filter | `internal/domain` ticket state machine (`new`, `in_progress`) | Only open tickets are handed off |
| HTTP edit handler error mapping | `internal/adapters/http/handlers_users.go:392` | Where the generic 500 originates and where typed feedback lands |
| Least-loaded selection to reuse | `internal/adapters/sqlite/workflow_uow.go:1523` (`leastLoadedAssigneeTx`) | The deterministic pool rule being reused, not reinvented |
| E2E journey | `e2e/` (Playwright) | Admin downgrades a desk member; tickets reassigned/unassigned as specified |
