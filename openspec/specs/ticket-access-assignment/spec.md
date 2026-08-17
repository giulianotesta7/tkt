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
