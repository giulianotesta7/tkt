# Delta for Comment Visibility

## MODIFIED Requirements

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

## MODIFIED Requirements

### Requirement: Server-Side Visibility Filtering

The system MUST apply the existing server-side visibility filtering before view composition. Internal comments MUST have a visually distinct background in staff timelines and MUST never appear in user responses.
(Previously: filtering was normative but internal-comment presentation was not specified.)

#### Scenario: Distinct internal presentation
- GIVEN a staff timeline containing an internal comment
- WHEN it renders
- THEN the comment has a distinct internal-comment background treatment
