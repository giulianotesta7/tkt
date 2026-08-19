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

The system MUST allow adding a comment to an existing ticket the actor can access, regardless of ticket state, subject to role and visibility rules: role `user` SHALL add only `public` comments to tickets they own; roles `agent`+ SHALL add `public` or `internal` comments to tickets within their scope (assigned tickets for `agent`; any ticket for `admin`/`root`). A comment MUST have a non-empty body and MUST record its author — the logged-in user taken from the session — its visibility, and creation timestamp. (Previously: any logged-in user could add a comment to any existing ticket.)

#### Scenario: Add a public comment

- GIVEN an existing ticket within the actor's scope and a logged-in actor
- WHEN the actor adds a comment with a body and visibility `public`
- THEN the comment is stored with the logged-in user as author, visibility `public`, and its creation timestamp

#### Scenario: Reject empty comment

- GIVEN an existing ticket within the actor's scope
- WHEN the actor adds a comment with an empty body
- THEN the request is rejected with a validation error
- AND no comment is stored

#### Scenario: Comment on a closed ticket

- GIVEN a ticket in state `closed` within the actor's scope
- WHEN the actor adds a comment
- THEN the comment is accepted and stored

#### Scenario: User cannot comment on another's ticket

- GIVEN a ticket owned by user B and a different `user`-role actor A
- WHEN A attempts to add a comment to the ticket
- THEN the request is denied
- AND no comment is stored

#### Scenario: User's internal comment rejected

- GIVEN a `user`-role actor owning a ticket
- WHEN they submit a comment with visibility `internal`
- THEN the request is rejected
- AND no comment is stored
### Requirement: Newest-First Timeline

The timeline MUST preserve newest-first ordering and server-side disclosure. Internal comments MUST use a distinct visual treatment from public comments, without exposing internal content to users.
(Previously: comments and audit events had distinct treatment, but internal-comment styling was unspecified.)

#### Scenario: Internal comment styling
- GIVEN a staff-visible timeline with public and internal comments
- WHEN it renders
- THEN internal and public comments have distinguishable backgrounds
- AND ordering remains newest first

### Requirement: Append-Only Comments

The system MUST NOT provide update or delete operations for comments in the MVP. Comments are immutable after creation.

#### Scenario: No edit or delete available

- GIVEN a stored comment
- WHEN a user attempts to edit or delete it
- THEN the operation is not available and the original comment remains unchanged
