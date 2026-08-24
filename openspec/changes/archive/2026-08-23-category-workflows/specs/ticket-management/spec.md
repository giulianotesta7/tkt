# Delta for Ticket Management

## MODIFIED Requirements

### Requirement: Create Ticket

The system MUST allow any authenticated actor to create a ticket from a title, description, category, and priority only. Creation MUST NOT offer or accept an assignee for ANY role: the create form MUST render no assignee control for any role, the create handler MUST bind no assignee parameter, and a creation request that still carries an assignee parameter MUST be rejected with an explicit assignee validation error — never silently ignored or dropped — regardless of actor role, including `admin` and `root`. No ticket, workflow pin, or workflow run MAY result from a rejected creation request, and every successfully created ticket MUST start with an empty assignee. Person assignment happens only later through the ticket's pinned category workflow flow. The title MUST be non-empty. The description MUST be supplied at creation and MUST be immutable afterwards (see Update Ticket Fields). The category MUST exist and MUST have a current published workflow version. A category without a published workflow MUST be rejected as unavailable for new tickets with a category validation error. The priority MUST be one of `low`, `medium`, `high`, `critical`. The requester name and email MUST be derived from the creating session user at creation time, and the requester user ID MUST be persisted from the session; the caller cannot supply or edit requester identity. A new ticket MUST start in state `new`, MUST record its creation timestamp, MUST pin the current published workflow version, and MUST initiate one active linear workflow run at its first step. Ticket creation, version pinning, and run initialization MUST succeed or fail together.
(Previously: creation accepted an optional assigned user that roles `agent`+ could set while role `user` was rejected — creation-time assignment is now removed entirely.)

#### Scenario: Create a valid unassigned ticket

- GIVEN an existing category with a published workflow, priority `high`, and a logged-in actor of any role
- WHEN the actor creates a ticket with title, description, category, and priority
- THEN the ticket is stored with a readable number, state `new`, a creation timestamp, and an empty assignee
- AND the requester name, email, and user ID are the creating actor's own
- AND the ticket pins the category's current published workflow version
- AND one active workflow run starts at the first step

#### Scenario: Create form renders no assignee control for any role

- GIVEN the new-ticket form as viewed by a `user`, `agent`, `admin`, or `root`
- WHEN the form renders
- THEN it contains no assignee selector or assignee input
- AND the submission accepts only title, description, category, and priority

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

#### Scenario: Assignee-carrying creation requests are rejected for every role

- GIVEN an available category and actors of role `user`, `agent`, `admin`, and `root`
- WHEN any of them submits a creation request that carries an assignee parameter
- THEN the request is rejected with an explicit assignee validation error
- AND no ticket, workflow pin, or workflow run is created
- AND no ticket is created with the parameter silently ignored

#### Scenario: Every created ticket starts unassigned

- GIVEN actors of every role creating tickets through the supported create flow
- WHEN creation succeeds
- THEN each resulting ticket has an empty assignee
- AND person assignment happens only later through the ticket's pinned category workflow flow

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

When a ticket has an active workflow run, the ticket detail UI MUST show a `Pending Actions` card above the timeline for the single current non-claim step. A current `assign_to_desk[claim]` step is instead projected in the Assignment sidebar as specified below. The server MUST render an actionable control only for an actor authorized to complete that step. Pending presentation MUST NOT use ordered-list numbering, and no ticket-facing copy MAY contain the generic message `Mark the current task as complete.` For a pending manual task, the card MUST lead with that step's immutable pinned non-empty instruction, shown verbatim and escaped to the current assignee; other pending steps MUST remain contextual to their pinned fields and actions. Rendering the detail page MUST remain a read-only GET: it MUST NOT complete tasks, advance the cursor, mutate answers or solutions, or append audit events. Completed form answers MUST remain visible as read-only content to authorized ticket readers, and assignee-provided answers MUST always be visible to the requester. The detail page MUST remain readable for legacy tickets without a workflow run and MUST NOT expose the internal workflow-version pin to requesters.

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
- AND no task, answer, solution, ticket, or cursor mutation occurs

#### Scenario: Pending manual task leads with its pinned instruction

- GIVEN a pending manual task whose pinned definition carries non-empty instructions and the current assignee viewing the detail page
- WHEN the Pending Actions card renders
- THEN the pinned instruction appears verbatim and escaped as the card's primary content
- AND the card shows no ordered-list numbering
- AND the page contains no `Mark the current task as complete.` copy

#### Scenario: No numbered or generic pending presentation for any step type

- GIVEN a pending form, desk-assignment, or claim step
- WHEN the Pending Actions card renders for an authorized viewer
- THEN the card uses contextual pinned content without ordered-list numbering
- AND no generic completion prompt replaces the step-specific context

#### Scenario: Detail GET performs no mutations

- GIVEN a ticket with an active run and a pending task
- WHEN any authorized actor loads the detail page via GET
- THEN the task, cursor, answers, solutions, and audit rows are unchanged

### Requirement: Current Task Card Presentation

The CURRENT pending form and manual task in the ticket activity area MUST use the supplied `Current task` card structure while preserving existing server-rendered behavior. The card background MUST use exactly `var(--amber-soft)`; it MUST NOT derive a new color or use blue. Pending forms MUST retain their labels, required semantics, native text fields/selects/checkboxes, validation rendering, and existing submit behavior. Pending manual tasks MUST retain pinned instructions, the optional solution field, and existing completion behavior. The card MUST preserve keyboard focus and responsive usability. Completed historical events MUST remain in the existing merged timeline with their current ordering and semantics unless a narrow wrapper is needed solely for visual coherence. GET rendering remains read-only and all mutations remain on the existing POST completion route.

#### Scenario: Pending form retains native semantics inside the current-task card

- GIVEN an authorized actor views a ticket with a current pending form task
- WHEN the ticket activity area renders
- THEN the form appears in the `Current task` card using background `var(--amber-soft)`
- AND its labels, required semantics, native fields, selects, checkboxes, validation rendering, and submit behavior remain unchanged
- AND the card remains keyboard usable without horizontal overflow at 390px wide

#### Scenario: Pending manual task retains pinned completion behavior inside the current-task card

- GIVEN the current assignee views a ticket with a pending manual task
- WHEN the ticket activity area renders
- THEN pinned instructions and the optional solution field appear in the `Current task` card using background `var(--amber-soft)`
- AND the existing completion route and authorization remain authoritative
- AND no blue or derived background color is used

#### Scenario: Historical activity remains merged and ordered

- GIVEN a ticket has completed form, manual-task, comment, assignment, and transition events
- WHEN the current task card is introduced for a pending task
- THEN completed historical events remain in the merged timeline with their existing ordering and semantics
- AND the presentation introduces no new client-side mutation authority

### Requirement: Claim Assignment Sidebar

For the current pinned `assign_to_desk[claim]` step, ticket detail MUST show the pinned Desk and current Assignee in the Assignment sidebar. The Pending Actions card and timeline MUST NOT contain a claim or reason form. The sidebar MUST render `Assign to me` only for an active `agent`, `admin`, or `root` who is currently a member of that pinned desk; it MUST not render for a nonmember. The button MUST post through the existing workflow-completion route, not through generic manual reassignment. Copy MUST remain plain and minimal.

#### Scenario: Eligible member sees the sidebar claim control

- GIVEN a ticket's current pinned step is `assign_to_desk[claim]` for Network
- AND an active Network member with role `agent`, `admin`, or `root` views the ticket
- WHEN the Assignment sidebar renders
- THEN it shows Network, the current Assignee, and `Assign to me`
- AND neither Pending Actions nor the timeline contains a claim or reason form

#### Scenario: Nonmember does not see the sidebar claim control

- GIVEN a ticket's current pinned step is a claim for Network
- AND an otherwise active eligible-role actor is not a current Network member
- WHEN the actor views the ticket
- THEN `Assign to me` is absent
