# Comment Visibility Specification

## Purpose

Defines public/internal comment creation privileges and server-side disclosure rules. Internal comments are confidential: they MUST be filtered out before any `user`-role view is composed.

## Requirements

### Requirement: Comment Visibility Model

Every comment MUST carry exactly one visibility value: `public` or `internal`. Role `user` SHALL create and read only `public` comments on tickets they own; roles `agent`+ SHALL create both `public` and `internal` comments. Role `user` MUST NOT create `internal` comments.

#### Scenario: User creates public comment

- GIVEN a `user`-role actor owning a ticket
- WHEN they add a comment with visibility `public`
- THEN the comment is stored as `public`

#### Scenario: Agent creates internal comment

- GIVEN an `agent`-role actor on a ticket within scope
- WHEN they add a comment with visibility `internal`
- THEN the comment is stored as `internal`

#### Scenario: User cannot create internal

- GIVEN a `user`-role actor
- WHEN they submit a comment marked `internal`
- THEN the request is rejected

### Requirement: Server-Side Visibility Filtering

The system MUST return comments per actor: `user` SHALL receive only `public` comments on tickets they own and MUST NOT receive `internal` comment content under any circumstance; roles `agent`+ SHALL receive both public and internal comments within their ticket scope. Filtering MUST be enforced server-side at application and store boundaries BEFORE any view composition; template-level hiding MUST NOT be the enforcement mechanism.

#### Scenario: User never sees internal content

- GIVEN a ticket owned by a `user`-role actor that has both public and internal comments
- WHEN the user opens the ticket detail
- THEN only public comments are shown
- AND no internal body text appears anywhere in the response

#### Scenario: Agent sees public and internal

- GIVEN a ticket assigned to an `agent` with internal comments
- WHEN the agent opens the ticket detail
- THEN both public and internal comments appear in the timeline

#### Scenario: Filtering precedes composition

- GIVEN an internal comment on a ticket the actor can otherwise access
- WHEN any service, store, or read path returns comments for a `user`-role actor
- THEN internal rows are absent from every returned collection — they are not merely hidden in markup

### Requirement: Legacy Comment Backfill

Existing comment rows MUST backfill to visibility `public` so no historical conversation becomes unintentionally hidden.

#### Scenario: Legacy comments become public

- GIVEN existing comments stored without a visibility value
- WHEN the migration runs
- THEN each comment is stored with visibility `public`