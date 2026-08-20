# Delta for Ticket Management

## MODIFIED Requirements

### Requirement: Create Ticket

The system MUST allow any authenticated actor to create a ticket from a title, description, category, priority, and optional assigned user. The title MUST be non-empty. The description MUST be supplied at creation and MUST be immutable afterwards (see Update Ticket Fields). The category MUST exist and MUST have a current published workflow version. A category without a published workflow MUST be rejected as unavailable for new tickets with a category validation error. The priority MUST be one of `low`, `medium`, `high`, `critical`. When a user is assigned, the assigned user MUST exist in the managed users, MUST be active, and MUST have role `agent`, `admin`, or `root`. An actor with role `user` MUST create tickets unassigned; assignment inputs MUST be accepted only from roles `agent`+ and MUST be rejected for role `user`. The requester name and email MUST be derived from the creating session user at creation time, and the requester user ID MUST be persisted from the session; the caller cannot supply or edit requester identity. A new ticket MUST start in state `new`, MUST record its creation timestamp, MUST pin the current published workflow version, and MUST initiate one active linear workflow run at its first step. Ticket creation, version pinning, and run initialization MUST succeed or fail together.
(Previously: creation required only an existing managed category and did not pin or initiate a workflow.)

#### Scenario: Create a valid unassigned ticket

- GIVEN an existing category with a published workflow, priority `high`, and a logged-in `user`-role actor
- WHEN the actor creates a ticket with title, description, category, and priority
- THEN the ticket is stored with a readable number, state `new`, a creation timestamp, and an empty assignee
- AND the requester name, email, and user ID are the creating actor's own
- AND the ticket pins the category's current published workflow version
- AND one active workflow run starts at the first step

#### Scenario: Agent creates an assigned ticket

- GIVEN an existing category with a published workflow, an active `agent`-role user, and a logged-in `agent`
- WHEN the agent creates a ticket assigned to that user
- THEN the ticket is stored with the assignee set
- AND the ticket pins the current published workflow version

#### Scenario: Requester cannot be supplied or edited

- GIVEN a logged-in actor
- WHEN the actor opens the create form or submits a creation request
- THEN no requester fields are present or accepted
- AND the stored requester is always the actor from the session

#### Scenario: Reject missing title

- GIVEN a creation request without a title
- WHEN the user submits the request
- THEN the request is rejected with a validation error
- AND no ticket is created

#### Scenario: Reject inactive user assignment

- GIVEN a deactivated user
- WHEN an `agent` creates a ticket assigned to that user in an available category
- THEN the request is rejected
- AND the ticket is not created

#### Scenario: User-role actor cannot assign

- GIVEN a `user`-role actor
- WHEN they submit a creation request with an assignee in an available category
- THEN the request is rejected
- AND the ticket is created unassigned or not at all

#### Scenario: Reject category without published workflow

- GIVEN an existing category with no current published workflow
- WHEN an actor submits a new ticket for that category
- THEN the request is rejected with status 422
- AND the form is rendered with `category: category is not available for new tickets — publish its workflow first`
- AND no ticket, pin, or workflow run is created

#### Scenario: Creation pins the version observed atomically

- GIVEN a category whose current published version changes while a ticket is being created
- WHEN creation succeeds
- THEN the ticket and its run reference one valid version that was current for that atomic creation
- AND no partial ticket or run exists

## ADDED Requirements

### Requirement: Pending Workflow Presentation

When a ticket has an active workflow run, the ticket detail UI MUST show a `Pending Actions` card above the timeline for the single current step. The server MUST render an actionable control only for an actor authorized to complete that step. Completed form answers MUST remain visible as read-only content to authorized ticket readers, and assignee-provided answers MUST always be visible to the requester. The detail page MUST remain readable for legacy tickets without a workflow run and MUST NOT expose the internal workflow-version pin to requesters.

#### Scenario: Authorized actor sees pending action

- GIVEN a ticket with one pending task and an actor authorized for that task
- WHEN the actor opens ticket detail
- THEN `Pending Actions` appears above the timeline
- AND the task's completion control is available

#### Scenario: Unauthorized actor sees no actionable control

- GIVEN a ticket with one pending actor-owned task and a readable ticket detail
- WHEN an actor who cannot complete the task opens the detail
- THEN no completion control is rendered for that actor

#### Scenario: Requester sees completed assignee answers

- GIVEN an assignee has completed a workflow form
- WHEN the ticket requester opens the detail page
- THEN those answers are shown as read-only content

#### Scenario: HTMX completion refreshes detail

- GIVEN an authorized actor submits a valid pending action with an HTMX request
- WHEN completion succeeds
- THEN the server returns the refreshed `ticket-detail` fragment for an outer replacement

#### Scenario: Full-page completion fallback redirects

- GIVEN an authorized actor submits a valid pending action without HTMX
- WHEN completion succeeds
- THEN the server responds with a 303 redirect to ticket detail

#### Scenario: Server rejects forged completion

- GIVEN an actor cannot complete the pending task
- WHEN the actor posts directly to its completion endpoint
- THEN the server denies the request
- AND no task, answer, ticket, or cursor mutation occurs
