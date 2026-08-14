---
name: ticket-management
status: proposed
change: tkt-mvp
---

# Ticket Management Specification

## Purpose

Defines the ticket aggregate: creation, readable numbering, editable fields, requester information, and lifecycle timestamps. The state machine, comment, audit, and search capabilities build on this aggregate.

## Requirements

### Requirement: Create Ticket

The system MUST create a ticket from a title, description, category, priority, and optional assigned user. The title MUST be non-empty. The category MUST exist in the managed categories. The priority MUST be one of `low`, `medium`, `high`, `critical`. When a user is assigned, the user MUST exist in the managed users and MUST be active. The requester name and email MUST be derived from the creating session user at creation time: the caller cannot supply or edit them, so a ticket can never be filed impersonating someone else. A new ticket MUST start in state `new` and MUST record its creation timestamp.

#### Scenario: Create a valid ticket

- GIVEN an existing category, an active managed user, and priority `high`
- WHEN a logged-in operator creates a ticket with title, description, category, user, and priority
- THEN the ticket is stored with a readable number, state `new`, a creation timestamp
- AND the requester name and email are the creating operator's own

#### Scenario: Requester cannot be supplied or edited

- GIVEN a logged-in operator
- WHEN the operator opens the create form or submits a creation request
- THEN no requester fields are present or accepted
- AND the stored requester is always the operator from the session

#### Scenario: Reject missing title

- GIVEN a creation request without a title
- WHEN the user submits the request
- THEN the request is rejected with a validation error
- AND no ticket is created

#### Scenario: Reject inactive user assignment

- GIVEN a deactivated user
- WHEN the user creates a ticket assigned to that user
- THEN the request is rejected
- AND the ticket is not created

### Requirement: Readable Numbering

The system MUST assign each ticket a unique readable number in `TKT-N` format, where N is a monotonically increasing integer. Concurrent creation MUST NOT produce duplicate numbers at MVP scale.

#### Scenario: Consecutive creation

- GIVEN an existing ticket numbered TKT-1042
- WHEN a new ticket is created
- THEN the new ticket is numbered TKT-1043

#### Scenario: Concurrent creation

- GIVEN two creation requests submitted concurrently
- WHEN both are processed
- THEN each ticket receives a distinct, unique number

### Requirement: Update Ticket Fields

The system MUST allow editing title, description, category, priority, and assigned user. Category, priority, and user edits MUST be validated as in creation. Each edit MUST update the modification timestamp, MUST append an audit event (see Audit Log), and MUST NOT alter `resolved_at` or `closed_at`.

#### Scenario: Edit category

- GIVEN a ticket in state `in_progress` with `resolved_at` empty
- WHEN the user changes its category to another valid category
- THEN the category is updated, the modification timestamp is refreshed, and an audit event is appended

#### Scenario: Edit to invalid priority

- GIVEN a ticket
- WHEN the user sets priority to an unsupported value
- THEN the edit is rejected
- AND no field changes are applied

### Requirement: Lifecycle Timestamps

The system MUST set `resolved_at` and `closed_at` only through state machine transitions (see Ticket State Machine). `created_at` and `updated_at` MUST reflect creation and last modification.

#### Scenario: Timestamps follow transitions only

- GIVEN a ticket resolved then closed via transitions
- WHEN its fields are later edited
- THEN `resolved_at` and `closed_at` remain unchanged

### Requirement: Ticket Detail Presentation

The normal ticket detail UI MUST provide inline editing for title, description, category, priority, and assigned user in a compact Properties sidebar. The same sidebar MUST provide state transition controls. The normal UI MUST NOT link to a separate edit screen, though `GET /tickets/{id}/edit` MAY remain available as a technical fallback. Requester, creation time, and modification time MUST remain read-only compact metadata beneath the ticket title.

All HTTP display timestamps MUST be rendered in UTC as `HH:mm · DD-MM-YYYY` while retaining an RFC3339 value in a semantic `<time datetime="...">` element. Persisted timestamps remain RFC3339 and are not changed by this presentation requirement. Visible state and priority labels MUST use human-readable name case, including `in_progress` rendered as `In Progress`; submitted values, CSS classes, and stored values MUST retain their internal identifiers.

#### Scenario: Edit from the detail page

- GIVEN an existing ticket
- WHEN the operator opens its detail page
- THEN title, description, category, priority, and assigned user controls are available in the Properties sidebar
- AND state transition controls are available in the same sidebar
- AND no visible link to a separate edit page is present

#### Scenario: Render concise semantic metadata

- GIVEN a ticket with requester and timestamps
- WHEN its detail page is rendered
- THEN requester, created time, and updated time appear beneath the title as read-only metadata
- AND each visible timestamp uses `HH:mm · DD-MM-YYYY` UTC text with an RFC3339 `datetime` attribute
