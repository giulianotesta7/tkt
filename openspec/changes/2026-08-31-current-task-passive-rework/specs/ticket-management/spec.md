# Delta for Ticket Management

This change updates only the presentation of an active pending workflow step. Workflow execution, authorization, completion endpoints, HTMX contracts, persisted history, and historical ordering remain unchanged.

## MODIFIED Requirements

### Requirement: Pending Workflow Presentation

When a ticket has an active workflow run, the ticket detail UI MUST show the single current non-claim step as the first item inside the `Timeline` container. A current `assign_to_desk[claim]` step remains projected in the Assignment sidebar. The server MUST render actionable controls only for an actor authorized to complete the step. The pending projection MUST NOT use ordered-list numbering or create a historical audit event. Rendering the detail page MUST remain a read-only GET, and successful completion MUST use the existing POST endpoint and refreshed `ticket-detail` outer swap. Historical comments and audit events MUST remain below the active item in their existing order.

#### Scenario: Authorized participant sees the active step in Timeline

- GIVEN a ticket with a pending manual task or form and an actor authorized to complete it
- WHEN the actor opens ticket detail
- THEN the first item inside `Timeline` is a compact actionable current-step item
- AND a manual task shows `CURRENT TASK`, its pinned instruction, and the existing completion control
- AND a form shows its existing native fields and completion control
- AND the existing completion endpoint and HTMX target remain unchanged

#### Scenario: Requester sees a compact informational active step

- GIVEN a pending task belongs to the assigned agent and the requester cannot complete it
- WHEN the requester opens ticket detail
- THEN the first item inside `Timeline` is a compact informational block
- AND it says `IN PROGRESS` followed by the assigned agent's actual name and `is handling this task.`
- AND it says `Updates will appear here when complete.`
- AND it does not show the internal instruction, completion form, disabled controls, permission messages, or the former standalone pending-card copy

#### Scenario: Other pending actions are role-dependent

- GIVEN a pending form or other actor-dependent workflow action
- WHEN an authorized participant views the ticket
- THEN the participant receives the current action controls
- AND WHEN another participant views the same ticket
- THEN that participant receives only the appropriate informational state and no disabled or actionable controls

#### Scenario: Historical events remain chronological

- GIVEN a ticket has comments or completed workflow events and an active pending step
- WHEN the ticket detail renders
- THEN the active step is the first item in `Timeline`
- AND historical events follow immediately below it with their existing ordering and content
- AND the active step is not duplicated as a historical event

#### Scenario: Completion removes the active projection

- GIVEN an authorized participant completes the current step
- WHEN the existing completion response refreshes ticket detail
- THEN the active projection is removed from `Timeline`
- AND the result remains as the existing historical event according to current behavior

#### Scenario: Timeline remains responsive

- GIVEN the ticket detail is rendered at desktop and mobile widths
- WHEN the active step is visible
- THEN the timeline remains the primary visual container, the actionable or informational item stays compact, and no horizontal overflow occurs

#### Scenario: Authorized actor sees pending action

- GIVEN a ticket with one pending task and an actor authorized for that task
- WHEN the actor opens ticket detail
- THEN the current-step projection appears as the first item inside `Timeline`
- AND the task's completion control is available

#### Scenario: Unauthorized actor sees no actionable control

- GIVEN a ticket with one pending actor-owned task and a readable ticket detail
- WHEN an actor who cannot complete the task opens the detail
- THEN the first Timeline item is informational only
- AND no completion control is rendered for that actor

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
- WHEN the Timeline projection renders
- THEN the pinned instruction appears verbatim and escaped as the actionable item's primary content
- AND the projection shows no ordered-list numbering
- AND the page contains no generic completion prompt

#### Scenario: No numbered or generic pending presentation for any step type

- GIVEN a pending form, desk-assignment, or claim step
- WHEN the ticket detail renders for an authorized viewer
- THEN the current-step presentation uses contextual pinned content without ordered-list numbering
- AND no generic completion prompt replaces the step-specific context

#### Scenario: Detail GET performs no mutations

- GIVEN a ticket with an active run and a pending task
- WHEN any authorized actor loads the detail page via GET
- THEN the task, cursor, answers, solutions, and audit rows are unchanged

### Requirement: Actor-First Timeline Presentation

Every ticket timeline entry MUST use one actor-first narrative pattern. Human comments and human-authored events MUST lead with the attributed actor, followed by the event or comment content; automatic events MUST omit actor text entirely. Timestamps MUST remain metadata and MUST NOT repeat the actor. This presentation applies consistently to comments, lifecycle transitions, field updates, workflow assignments, manual completions, and requester/assignee form completions. A completed manual task with compatible pinned context MUST be static visible markup with a discrete green check and no `details`, `summary`, button, expansion control, open state, or interactive cursor semantics. Its definition-list body MUST include `TASK` and MUST include `SOLUTION` only when the stored solution is non-empty, followed by timestamp metadata. Pinned instructions, solutions, comments, labels, and submitted values MUST remain escaped plain text, and the existing authorization, HTMX, ordering, and responsive contracts MUST remain unchanged.

#### Scenario: Human events lead with their actor

- GIVEN a timeline containing a comment, transition, update, workflow assignment, manual completion, requester form completion, and assignee form completion by human actors
- WHEN the ticket detail renders
- THEN each entry leads with its actor followed by its contextual content
- AND each timestamp contains no duplicated actor

#### Scenario: Automatic events omit actor text

- GIVEN a timeline containing an automatic assignment or transition
- WHEN the ticket detail renders
- THEN the event content is shown without actor text or a dangling separator

#### Scenario: Manual completion exposes static task details

- GIVEN a completed manual task with a pinned instruction and an optional stored solution
- WHEN the timeline item renders
- THEN it is always-visible static markup with a discrete green check and actor-first completion copy
- AND it contains no `details`, `summary`, button, expansion control, open state, or interactive cursor semantics
- AND its definition-list body contains `TASK` and the escaped pinned instruction
- AND it contains `SOLUTION` only when the stored solution is non-empty
- AND the timestamp metadata follows the definition-list rows and contains no duplicated actor

#### Scenario: Manual context remains safely degraded

- GIVEN a legacy or inconsistent manual completion without compatible pinned context
- WHEN the timeline renders
- THEN the safe summary remains readable without fabricated task or solution content
