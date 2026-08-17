---
name: ticket-search
status: proposed
change: tkt-mvp
---

# Ticket Search Specification

## Purpose

Defines list and search behavior: composable filters, FTS5 full-text search over ticket titles plus exact ticket ID (TKT-N) matching, pagination, ordering, and the canonical filter bar.

## Requirements

### Requirement: Composable Filters

The system MUST filter tickets by state, priority, category, and assigned user, and MUST compose all active filters with AND semantics. The actor's ticket access scope (see Ticket Access and Assignment) MUST be applied before any filter: `user` searches own tickets, `agent` assigned tickets, `admin`/`root` the full queue. An empty filter set MUST return all tickets within the actor's scope and never tickets outside it. (Previously: an empty filter set returned all tickets with no access-scope restriction.)

#### Scenario: Filter composition

- GIVEN tickets across states, priorities, categories, and users, within an `admin`'s scope
- WHEN the admin filters by state `resolved`, priority `high`, category "Bugs", and a specific user
- THEN only tickets matching all four conditions are returned

#### Scenario: Empty filters respect actor scope

- GIVEN tickets created by actors A and B
- WHEN A, a `user`-role actor, lists tickets with no filters
- THEN only tickets created by A are returned
- AND no tickets created by B appear

#### Scenario: Agent search is scoped to assignment

- GIVEN tickets assigned to agents X and Y
- WHEN agent X searches by a title term matching one of Y's tickets
- THEN Y's ticket is not returned
- AND only X's assigned tickets can match
### Requirement: Search by ID or Title

The system MUST expose the existing `GET /tickets?q=` ID/title search beside “New ticket” for every role. Search MUST remain scoped by actor access and MUST NOT match descriptions or comments.
(Previously: search was defined through the canonical filter bar without requiring an all-role compact entry point.)

#### Scenario: Every role searches
- GIVEN an authenticated actor of any role
- WHEN the ticket list renders
- THEN an ID/title search control appears beside “New ticket” and submits through `/tickets?q`

#### Scenario: Search remains scoped
- GIVEN a user-owned ticket and another user’s matching ticket
- WHEN a `user` searches the matching term
- THEN only their own matching ticket is returned

### Requirement: Priority Ordering

Priority ordering MUST follow `critical` > `high` > `medium` > `low` for sorting and filter display.

#### Scenario: Priority sort

- GIVEN tickets with priorities `low`, `critical`, `medium`, `high`
- WHEN the list is sorted by priority
- THEN the order is `critical`, `high`, `medium`, `low`

### Requirement: Pagination and Ordering

The system MUST paginate list and search results with a deterministic default ordering (newest first by creation) and stable page boundaries.

#### Scenario: Stable pagination

- GIVEN 25 matching tickets with a page size of 10
- WHEN pages are requested
- THEN page 1 returns 10 tickets, page 3 returns 5, and pages do not overlap

### Requirement: Canonical Filter Surface

Staff roles MUST retain the full filter bar for text, state, priority, category, and assigned user. The `user` role MUST NOT receive that filter bar; its compact search MUST remain limited to its own-ticket scope. No role MAY render duplicate text-search controls.
(Previously: one filter bar was canonical for every role.)

#### Scenario: Staff retain filters
- GIVEN an `agent`, `admin`, or `root`
- WHEN the ticket list renders
- THEN the full filter bar remains available

#### Scenario: User loses filters
- GIVEN a `user` actor
- WHEN the ticket list renders and searches
- THEN the filter bar is absent and results remain own-ticket scoped
