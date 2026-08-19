# Delta for Ticket Management

## MODIFIED Requirements

### Requirement: Create Ticket

The system MUST allow any authenticated actor to create a ticket from a title, description, category, priority, and optional assigned user. The title MUST be non-empty. The category MUST exist in the managed categories. The priority MUST be one of `low`, `medium`, `high`, `critical`. When a user is assigned, the assigned user MUST exist in the managed users, MUST be active, and MUST have role `agent`, `admin`, or `root`. An actor with role `user` MUST create tickets unassigned; assignment inputs MUST be accepted only from roles `agent`+ and MUST be rejected for role `user`. The requester name and email MUST be derived from the creating session user at creation time, and the requester user ID MUST be persisted from the session; the caller cannot supply or edit requester identity. A new ticket MUST start in state `new` and MUST record its creation timestamp. (Previously: any logged-in user could create assigned tickets; requester had no persisted user ID.)

#### Scenario: Create a valid unassigned ticket

- GIVEN an existing category, priority `high`, and a logged-in `user`-role actor
- WHEN the actor creates a ticket with title, description, category, and priority
- THEN the ticket is stored with a readable number, state `new`, a creation timestamp, and an empty assignee
- AND the requester name, email, and user ID are the creating actor's own

#### Scenario: Agent creates an assigned ticket

- GIVEN an existing category, an active `agent`-role user, and a logged-in `agent`
- WHEN the agent creates a ticket assigned to that user
- THEN the ticket is stored with the assignee set

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
- WHEN an `agent` creates a ticket assigned to that user
- THEN the request is rejected
- AND the ticket is not created

#### Scenario: User-role actor cannot assign

- GIVEN a `user`-role actor
- WHEN they submit a creation request with an assignee
- THEN the request is rejected
- AND the ticket is created unassigned or not at all

### Requirement: Update Ticket Fields

The system MUST allow editing title, description, category, priority, and assigned user, restricted by actor role: `agent` SHALL edit tickets assigned to them; `admin` and `root` SHALL edit any ticket; role `user` MUST NOT edit tickets. Category, priority, and user edits MUST be validated as in creation, including agent-plus assignment and reassignment-reason rules (see Ticket Access and Assignment). Each edit MUST update the modification timestamp, MUST append an audit event (see Audit Log), and MUST NOT alter `resolved_at` or `closed_at`. (Previously: any logged-in user could edit any ticket.)

#### Scenario: Edit category

- GIVEN a ticket in state `in_progress` assigned to an `agent`, with `resolved_at` empty
- WHEN the assigned agent changes its category to another valid category
- THEN the category is updated, the modification timestamp is refreshed, and an audit event is appended

#### Scenario: Edit to invalid priority

- GIVEN a ticket assigned to an `agent`
- WHEN the agent sets priority to an unsupported value
- THEN the edit is rejected
- AND no field changes are applied

#### Scenario: Non-assigned actor cannot edit

- GIVEN a ticket assigned to agent X and a `user`-role actor
- WHEN the user attempts to edit the ticket's fields
- THEN the request is denied
- AND no field changes are applied