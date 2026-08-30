# Ticket Access and Assignment Specification

## Purpose

Defines immutable requester ownership, per-role ticket access scopes, person-only assignment with audited reasons, and legacy ownership backfill.

## Requirements

### Requirement: Requester Ownership

The system MUST persist the creating session user as an immutable `requester_user_id` foreign key on every new ticket, alongside the requester name/email snapshots. The requester identity MUST NOT be supplied or edited by any caller. Tickets created by role `user` MUST start unassigned.

#### Scenario: Creator persisted and ticket unassigned

- GIVEN a logged-in `user`-role actor
- WHEN they create a ticket
- THEN the ticket stores their user ID as requester
- AND the assignee is empty

#### Scenario: Caller cannot supply requester

- GIVEN any ticket creation request that includes requester fields
- WHEN it is submitted
- THEN the supplied requester fields are ignored or rejected
- AND the stored requester is always the session user

### Requirement: Ticket Access Scope

The system MUST scope lists, detail, search, and direct lookup to the actor before data is returned: `user` SHALL access only tickets they created (requester = self); `agent` SHALL access tickets assigned to them; `admin` and `root` SHALL access the full queue, including unassigned tickets. An empty filter set MUST return all tickets within the actor's scope, never outside it.

#### Scenario: User sees only own tickets

- GIVEN tickets created by actors A and B
- WHEN A lists tickets or directly requests B's ticket
- THEN A sees only their own tickets
- AND the direct request for B's ticket is denied

#### Scenario: Agent sees only assigned tickets

- GIVEN unassigned tickets and tickets assigned to agents X and Y
- WHEN X lists tickets
- THEN X sees only tickets assigned to X, never unassigned or Y's tickets

#### Scenario: Admin sees full queue

- GIVEN unassigned tickets and tickets assigned to agents
- WHEN an `admin` lists tickets with empty filters
- THEN every ticket is returned

### Requirement: Person-Only Assignment

Assignment MUST continue to target only a person with role `agent`, `admin`, or `root`; desk membership MUST NOT alter assignment semantics. No group/desk assignment field or persisted desk assignment MAY be introduced.
(Previously: the prohibition referred to groups rather than desks.)

#### Scenario: Desk assignment rejected
- GIVEN a desk and an accessible ticket
- WHEN a caller submits the desk as assignee
- THEN the request is rejected and the person assignee remains unchanged

#### Scenario: Person assignment preserved
- GIVEN a ticket assigned to an agent before migration
- WHEN migration 0004 runs
- THEN the assignment remains attached to the same person and ticket

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

### Requirement: Legacy Ownership Backfill

The system MUST backfill requester ownership on legacy tickets only from reliable audit evidence (for example an unambiguous creation event whose actor survives). Tickets without a reliable creator MUST remain visible to roles `agent`+ only and MUST NEVER be attributed to a guessed user.

#### Scenario: Reliable evidence backfills owner

- GIVEN a legacy ticket whose creation event names a surviving unique user
- WHEN migration runs
- THEN that user becomes the requester and can see the ticket

#### Scenario: Unmatched legacy ticket stays agent-only

- GIVEN a legacy ticket whose creator cannot be derived reliably
- WHEN migration runs
- THEN no requester is assigned
- AND only roles `agent`+ can view the ticket
