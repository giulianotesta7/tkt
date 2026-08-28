# Delta for Comment Timeline

Scope note: this is the spec-phase delta for change `sync-frontend-contracts` (issue #74). It syncs canonical `openspec/specs/comment-timeline/spec.md` to the shipped comment-write contract. No canonical file, runtime, template, test, or migration is edited here.

## MODIFIED Requirements

### Requirement: Add Comment

The system MUST allow adding a comment to an existing ticket the actor can access only when that ticket is not in a closed state, subject to role and visibility rules: role `user` SHALL add only `public` comments to tickets they own; roles `agent`+ SHALL add `public` or `internal` comments to tickets within their scope (assigned tickets for `agent`; any ticket for `admin`/`root`). A ticket in state `resolved`, `closed`, or `cancelled` MUST NOT accept a new comment: the comment POST MUST return a rejection with no write and MUST NOT persist a comment (the guard runs at the application boundary before any comment store call, and the HTTP layer maps it to 403). A comment MUST have a non-empty body and MUST record its author — the logged-in user taken from the session — its visibility, and creation timestamp.
(Previously: the system allowed adding a comment to an existing ticket the actor can access regardless of ticket state; any state including closed was described as accepted.)

#### Scenario: Add a public comment

- GIVEN an existing ticket in state `new` or `in_progress` within the actor's scope and a logged-in actor
- WHEN the actor adds a comment with a body and visibility `public`
- THEN the comment is stored with the logged-in user as author, visibility `public`, and its creation timestamp

#### Scenario: Reject empty comment

- GIVEN an existing ticket within the actor's scope
- WHEN the actor adds a comment with an empty body
- THEN the request is rejected with a validation error
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

## Notes

Traceability for `Add Comment`:

| Evidence | Path | What it proves |
|---|---|---|
| Closed predicate | `internal/domain/state.go:IsClosed` | `resolved`, `closed`, `cancelled` are the closed (read-only) states |
| Application guard before store | `internal/application/comment_service.go:Add` | `domain.IsClosed(t.State)` check returns `ForbiddenError(ErrMsgCommentOnClosedTicket)` before `CommentStore.Add` |
| Service rejects, no write | `internal/application/comment_service_test.go:TestAddCommentOnClosedTicketRejected` | `resolved`/`closed`/`cancelled` each denied, zero store calls, nothing stored |
| Service accepts open | `internal/application/comment_service_test.go:TestAddCommentOnOpenTicketAccepted` | `new` and `in_progress` accept writes |
| HTTP maps to 403, no write | `internal/adapters/http/handlers_detail_test.go:TestTicketCommentOnClosedTicketRejected` | `POST /tickets/{id}/comments` on `resolved`/`closed`/`cancelled` is 403 and stores nothing |
