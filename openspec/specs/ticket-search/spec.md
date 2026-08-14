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

The system MUST filter tickets by state, priority, category, and assigned user, and MUST compose all active filters with AND semantics. An empty filter set MUST return all tickets.

#### Scenario: Filter composition

- GIVEN tickets across states, priorities, categories, and users
- WHEN the user filters by state `resolved`, priority `high`, category "Bugs", and a specific user
- THEN only tickets matching all four conditions are returned

### Requirement: Search by ID or Title

The system MUST provide search over ticket titles (FTS5) and exact ticket IDs (TKT-N). The search box scope is ID or title only: a search term MUST NOT match description or comment bodies. A title term and an ID term compose with OR semantics within the text filter and AND semantics with the other filters. Search results MUST remain consistent with edits: edited titles MUST be searchable and superseded titles MUST NOT remain searchable.

#### Scenario: Search by title only

- GIVEN a ticket whose description contains "timeout" and another whose comment contains "timeout"
- WHEN the user searches for "timeout"
- THEN only tickets whose title contains "timeout" are returned

#### Scenario: Search by ticket ID

- GIVEN a ticket with number 3
- WHEN the user searches for "3" or "TKT-3"
- THEN the ticket is returned even when no title matches

#### Scenario: Search reflects edits

- GIVEN a ticket whose title is edited from "Old" to "New"
- WHEN the user searches for "Old"
- THEN the ticket is no longer returned
- AND searching "New" returns it

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

The ticket list MUST use one filter bar as the canonical filtering surface for text, state, priority, category, and assigned user. The list MUST NOT duplicate state or priority filters as summary chips. HTMX filtering and pagination MUST replace only the ticket list fragment while preserving progressive-enhancement links and form actions. The readable-number column heading MUST be `ID`, while values remain in `TKT-N` format. Visible state and priority labels MUST be human-readable name case while query parameters and option values retain internal identifiers.

#### Scenario: Filter without duplicate summary controls

- GIVEN the ticket queue
- WHEN the list view is rendered or filtered through HTMX
- THEN one filter bar controls the result set
- AND no summary chips are rendered
- AND the table heading is `ID` with values such as `TKT-1`
