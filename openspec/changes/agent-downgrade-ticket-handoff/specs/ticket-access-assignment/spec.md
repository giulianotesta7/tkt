# Delta for Ticket Access and Assignment

Scope note: this is the spec-phase delta for change `agent-downgrade-ticket-handoff` (issue #47). It adds the deterministic automatic-reassignment contract exercised by the atomic downgrade handoff. No canonical spec file is edited here.

## ADDED Requirements

### Requirement: Automatic Reassignment on Downgrade

When the atomic downgrade handoff reassigns an open ticket, the system MUST resolve the ticket's desk in priority order: (i) the `desk_id` of the latest audit event for that ticket that carries one (assignment context snapshot), else (ii) the desk of the first `assign_to_desk` step (by step order) in the ticket's pinned workflow version, else (iii) unresolvable. Replacement selection MUST reuse the deterministic least-loaded rule over the resolved desk's membership pool: candidates are desk members with `active = 1` and role in `agent`, `admin`, or `root`; the winner is the candidate with the fewest open (`new`/`in_progress`) tickets counted globally, and ties MUST be broken by the lowest user id. The downgraded account MUST never be a candidate for its own replacement, because its desk memberships are deleted inside the same transaction before selection. When no desk resolves or the resolved desk has no eligible member, the ticket MUST become unassigned. Only open tickets (state `new` or `in_progress`) are reassigned; closed, resolved, and cancelled tickets MUST keep their historical assignment.

#### Scenario: Least-loaded eligible member wins

- GIVEN a downgraded desk member with an open ticket whose desk resolves to a membership pool
- WHEN the handoff selects the replacement
- THEN the ticket is assigned to the pool member with the fewest open tickets counted globally
- AND the selection reuses the deterministic least-loaded rule

#### Scenario: Tie broken by lowest user id

- GIVEN two or more eligible pool members with the same fewest open-ticket count
- WHEN the handoff selects the replacement
- THEN the member with the lowest user id wins

#### Scenario: Downgraded account is never its own replacement

- GIVEN a downgraded desk member whose memberships were deleted inside the handoff transaction
- WHEN the handoff selects the replacement
- THEN the downgraded account is not among the candidates
- AND no ticket remains assigned to the downgraded account among the open set

#### Scenario: No eligible member leaves the ticket unassigned

- GIVEN a resolved desk whose membership pool contains no active `agent`/`admin`/`root` member
- WHEN the handoff processes the open ticket
- THEN the ticket becomes unassigned
- AND the downgrade itself still succeeds

#### Scenario: Desk unresolvable leaves the ticket unassigned

- GIVEN an open ticket whose latest desk-bearing audit event does not exist and whose pinned workflow version has no `assign_to_desk` step
- WHEN the handoff processes the ticket
- THEN the desk is unresolvable and the ticket becomes unassigned
- AND the downgrade itself still succeeds

#### Scenario: Only active agent/admin/root desk members are eligible

- GIVEN a resolved desk pool containing an inactive member and a role-`user` member alongside eligible members
- WHEN the handoff selects the replacement
- THEN only active members with role `agent`, `admin`, or `root` are considered
- AND inactive and role-`user` members are never selected

## Notes

Traceability for `Automatic Reassignment on Downgrade` (evidence paths show today's seams; T2–T4 add the implementation and tests):

| Evidence | Path | What it proves |
|---|---|---|
| Least-loaded selection to reuse | `internal/adapters/sqlite/workflow_uow.go:1523` (`leastLoadedAssigneeTx`) | The deterministic pool rule (fewest open, lowest id tie-break) |
| Documented future desk resolution contract | `openspec/specs/desk-management/spec.md` (`Person-Only Assignment Invariant`) | Least-loaded resolution was the documented contract this implements |
| Desk context in audits | `internal/domain/audit.go:45` (`DeskID`) | The assignment-context snapshot used by resolution priority (i) |
| Pinned `assign_to_desk` steps | `internal/domain` workflow step types + `workflow_uow.go` | Resolution priority (ii) source of desk when audits carry none |
| Open-state filter | `internal/domain` ticket state machine (`new`, `in_progress`) | Only open tickets participate in handoff |
| Store tests for selection | `internal/adapters/sqlite/user_store_test.go` (T2) | Pool eligibility, ordering, tie-break, and fallbacks |
