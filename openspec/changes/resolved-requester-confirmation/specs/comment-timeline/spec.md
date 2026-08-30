# Delta for Comment Timeline

Scope note: this is the spec-phase delta for change `resolved-requester-confirmation` (issue #55). It modifies `Add Comment` so that, while a ticket is `resolved`, only its requester may comment; `closed` and `cancelled` still reject everyone, and requester-less `resolved` tickets accept no comments. No canonical file, runtime, template, test, or migration is edited here.

## MODIFIED Requirements

### Requirement: Add Comment

The system MUST allow adding a comment to an existing ticket the actor can access only when that ticket is not in state `closed` or `cancelled`, subject to role and visibility rules: role `user` SHALL add only `public` comments to tickets they own; roles `agent`+ SHALL add `public` or `internal` comments to tickets within their scope (assigned tickets for `agent`; any ticket for `admin`/`root`). A ticket in state `resolved` MUST accept a new comment only from its requester — the authenticated actor whose session user equals the ticket's requester user ID — and role `user` requester comments remain limited to `public` visibility. A `resolved` ticket with requester user ID NULL MUST NOT accept a comment from any actor. A ticket in state `closed` or `cancelled` MUST NOT accept a new comment from any actor. A rejected comment POST MUST return a rejection with no write and MUST NOT persist a comment (the guard runs at the application boundary before any comment store call, and the HTTP layer maps it to 403). A comment MUST have a non-empty body and MUST record its author — the logged-in user taken from the session — its visibility, and creation timestamp.
(Previously: every ticket in `resolved`, `closed`, or `cancelled` rejected all comments; the requester now retains an active voice — commenting — while their `resolved` ticket awaits confirmation.)

#### Scenario: Add a public comment

- GIVEN an existing ticket in state `new` or `in_progress` within the actor's scope and a logged-in actor
- WHEN the actor adds a comment with a body and visibility `public`
- THEN the comment is stored with the logged-in user as author, visibility `public`, and its creation timestamp

#### Scenario: Reject empty comment

- GIVEN an existing ticket within the actor's scope
- WHEN the actor adds a comment with an empty body
- THEN the request is rejected with a validation error
- AND no comment is stored

#### Scenario: Requester comments while the ticket is resolved

- GIVEN a `resolved` ticket with requester user ID R and the logged-in requester R
- WHEN R adds a comment with a non-empty body and visibility `public`
- THEN the comment is stored with R as author
- AND no other ticket state or field changes

#### Scenario: Non-requester comment on a resolved ticket is rejected

- GIVEN a `resolved` ticket with an identifiable requester and an actor whose session user is not that requester
- WHEN that actor adds a comment
- THEN the request is rejected with no write
- AND no comment is stored

#### Scenario: Comment on a requester-less resolved ticket is rejected

- GIVEN a `resolved` ticket with requester user ID NULL
- WHEN any actor adds a comment
- THEN the request is rejected with no write
- AND no comment is stored

#### Scenario: Comment on a closed ticket

- GIVEN a ticket in state `resolved`, `closed`, or `cancelled` within the actor's scope
- WHEN the actor adds a comment
- THEN the request is rejected with a rejection indicating comments on closed tickets are not allowed
- AND no comment is stored
- AND no comment store write occurs

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
