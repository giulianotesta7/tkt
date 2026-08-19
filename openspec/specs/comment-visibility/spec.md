# Comment Visibility Specification

## Purpose

Defines public/internal comment creation privileges and server-side disclosure rules. Internal comments are confidential: they MUST be filtered out before any `user`-role view is composed.

## Requirements

### Requirement: Comment Visibility Model

Every comment MUST retain exactly `public` or `internal`. Roles `agent`+ MAY select internal visibility through an “Internal comment” checkbox; role `user` MUST NOT create internal comments. The server MUST normalize and enforce visibility regardless of checkbox or forged fields.
(Previously: the form used a visibility select.)

#### Scenario: Internal checkbox
- GIVEN an agent composing a comment
- WHEN the agent checks “Internal comment” and submits
- THEN the stored visibility is `internal`

#### Scenario: User forgery rejected
- GIVEN a user submits an internal value while the checkbox is absent or forged
- WHEN the server processes the comment
- THEN creation is rejected and no internal comment is stored

### Requirement: Server-Side Visibility Filtering

The system MUST apply the existing server-side visibility filtering before view composition. Internal comments MUST have a visually distinct background in staff timelines and MUST never appear in user responses.
(Previously: filtering was normative but internal-comment presentation was not specified.)

#### Scenario: Distinct internal presentation
- GIVEN a staff timeline containing an internal comment
- WHEN it renders
- THEN the comment has a distinct internal-comment background treatment

### Requirement: Legacy Comment Backfill

Existing comment rows MUST backfill to visibility `public` so no historical conversation becomes unintentionally hidden.

#### Scenario: Legacy comments become public

- GIVEN existing comments stored without a visibility value
- WHEN the migration runs
- THEN each comment is stored with visibility `public`
