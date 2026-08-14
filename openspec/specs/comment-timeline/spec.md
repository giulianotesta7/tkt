---
name: comment-timeline
status: proposed
change: tkt-mvp
---

# Comment Timeline Specification

## Purpose

Defines the single comment type and its chronological, append-only timeline on tickets.

## Requirements

### Requirement: Add Comment

The system MUST allow adding a comment to any existing ticket, regardless of ticket state. A comment MUST have a non-empty body and MUST record its author — the logged-in user taken from the session — and creation timestamp.

#### Scenario: Add a comment

- GIVEN an existing ticket and a logged-in user
- WHEN the user adds a comment with a body
- THEN the comment is stored with the logged-in user as author and with its creation timestamp

#### Scenario: Reject empty comment

- GIVEN an existing ticket
- WHEN the user adds a comment with an empty body
- THEN the request is rejected with a validation error
- AND no comment is stored

#### Scenario: Comment on a closed ticket

- GIVEN a ticket in state `closed`
- WHEN the user adds a comment
- THEN the comment is accepted and stored

### Requirement: Newest-First Timeline

The system MUST present a ticket's comments newest first, ordered by creation timestamp descending. The presentation timeline interleaves comments and audit events in one merged, reverse-chronological stream (GitHub-style conversation flow).

Agent comments and system/audit events MUST use visibly distinct background and border treatments. Audit event dots MUST align with their action text. Visible action, field, state, and priority labels MUST be human-readable while their underlying values remain unchanged. Timeline timestamps MUST use semantic `<time>` elements with human-readable UTC text.

Audit fields `user` and `user_id` MUST display as `Assigned To`; their numeric values MUST resolve to managed-user names and an empty value MUST display as `Unassigned`. Audit fields `category` and `category_id` MUST display as `Category` and resolve numeric values to category names. Missing or malformed historical references MUST degrade to `Unknown user` or `Unknown category` without exposing the stored identifier. Other field labels MUST use readable title case with a safe fallback for unknown fields, while non-reference values remain literal.

#### Scenario: Ordering

- GIVEN three comments created at increasing times
- WHEN the timeline is rendered
- THEN the comments appear in reverse creation order (newest first)

#### Scenario: Distinguish comments from audit events

- GIVEN a merged timeline containing a comment and a state transition event
- WHEN the timeline is rendered
- THEN the comment and audit event use distinct visual treatments
- AND the event dot aligns with the action text
- AND `new` to `in_progress` is displayed as `New` to `In Progress`

#### Scenario: Resolve audit reference values

- GIVEN assignment and category audit events containing stored numeric IDs
- WHEN the merged timeline is rendered
- THEN the assignment event shows `Assigned To` with user names or `Unassigned`
- AND the category event shows category names
- AND no numeric user or category ID appears in the event text

### Requirement: Append-Only Comments

The system MUST NOT provide update or delete operations for comments in the MVP. Comments are immutable after creation.

#### Scenario: No edit or delete available

- GIVEN a stored comment
- WHEN a user attempts to edit or delete it
- THEN the operation is not available and the original comment remains unchanged
