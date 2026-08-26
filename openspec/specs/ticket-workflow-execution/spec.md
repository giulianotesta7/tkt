# Ticket Workflow Execution Specification

## Purpose

Defines deterministic execution of a ticket's pinned linear workflow, including actor-owned tasks, person-only desk routing, lifecycle terminal steps, and representative end-to-end journeys.

## Requirements

### Requirement: Pinned Linear Advancement

A ticket workflow run MUST execute the immutable version pinned when the ticket was created, even after another version is published. The run MUST expose exactly one current step at a time and MUST advance in order only after that step completes. Automatic steps MUST execute when reached without requiring a user control. After the final step, the run MUST become `completed`. Run completion itself MUST NOT change ticket state; without a terminal step, the ticket MUST remain in the state produced by its completed steps.

#### Scenario: In-flight ticket keeps its pinned version

- GIVEN ticket A pins version 1 and version 2 is later published
- WHEN ticket A advances
- THEN it continues through version 1
- AND a newly created ticket pins version 2

#### Scenario: Only one step is pending

- GIVEN an active run with three ordered manual steps
- WHEN the first step is pending
- THEN the later steps are not completable
- WHEN the first step completes
- THEN only the second step becomes current

#### Scenario: Workflow completion does not imply resolution

- GIVEN a workflow containing only a requester form
- WHEN the requester completes the form
- THEN the run becomes `completed`
- AND the ticket remains `new`

#### Scenario: Assignment-only workflow preserves reached state

- GIVEN a `new` ticket whose final step successfully assigns a person
- WHEN the run completes without a terminal step
- THEN the ticket remains `in_progress`

### Requirement: Read-Only Lifecycle Guard

A non-terminal workflow step MUST NOT complete while its ticket is `resolved`, `closed`, or `cancelled`. Form submission, manual completion, claim, and automatic desk assignment MUST be rejected without changing the task, cursor, answers, assignee, ticket, or audits. Automatic terminal steps MUST instead follow their explicit state matrices below.

#### Scenario: Non-terminal completion is rejected after lifecycle closure

- GIVEN a form, manual, or desk-assignment step is pending on a `resolved`, `closed`, or `cancelled` ticket
- WHEN an otherwise authorized actor or automatic strategy attempts completion
- THEN completion is rejected
- AND workflow and ticket data remain unchanged

### Requirement: Stale Completion Positions Are Rejected Without Writes

A completion request MUST identify the current pinned step position; a stale, missing, non-positive, or mismatched position MUST be rejected with the typed position conflict error before any write, persisting no task completion, cursor movement, answer, solution, ticket change, or audit event. Actor authorization gates and automatic-step behavior MUST remain exactly as specified elsewhere in this capability; position guarding never relaxes or replaces them.

#### Scenario: Stale position persists nothing

- GIVEN a run whose cursor already advanced past the submitted position
- WHEN a completion request names that stale position
- THEN the request is rejected with the typed position conflict error
- AND no task, cursor, answer, solution, ticket, or audit mutation occurs

### Requirement: Person-Only Desk Routing

An `assign_to_desk` step MUST resolve a ticket assignee to an active person with role `agent`, `admin`, or `root` who belongs to the configured desk; a desk MUST never be stored as the assignee. For strategy `claim`, the task MUST remain pending until an eligible desk member claims it. A pending claim MUST leave a `new` ticket in `new`. A successful claim MUST assign the authenticated claimant, and a role `agent` MUST claim only for themselves. Workflow claim completion is reasonless, including a person-to-person A→B claim: its command and operation pipeline MUST accept no reason or caller-selected assignee. This exception is limited to the pinned workflow claim route; generic manual reassignment through `POST /tickets/{id}/assign` and `TicketService.Assign` retains its existing non-empty reason requirement. For strategy `least_loaded`, the system MUST automatically choose the eligible desk member with the fewest assigned tickets in states `new` or `in_progress` across all categories; other states MUST be excluded and a tie MUST select the lower user ID.

When either strategy successfully assigns a person to a `new` ticket, the same atomic operation MUST persist that person, execute `Ticket.Transition(in_progress)`, and append both assignment and transition audits. If the ticket is already `in_progress`, assignment MUST NOT create a redundant state transition.

#### Scenario: Pending claim leaves new state unchanged

- GIVEN a `new`, unassigned ticket at a `claim` step
- WHEN no eligible desk member has claimed it
- THEN the task remains pending
- AND the ticket remains `new` and unassigned

#### Scenario: Eligible member claims to self

- GIVEN a `new` ticket pending a claim for desk Network
- AND agent A is an active member of Network
- WHEN agent A claims the task
- THEN the ticket assignee is person A, not the desk
- AND the ticket transitions to `in_progress`
- AND assignment, transition, and both audits commit atomically
- AND the assignment timeline event reads `Assigned to agent A · Network`

#### Scenario: Agent cannot claim for another person

- GIVEN agent A is eligible to claim a desk task
- WHEN agent A attempts to assign the claim to agent B
- THEN the request is denied
- AND the claim remains pending

#### Scenario: Workflow A-to-B claim is reasonless

- GIVEN a ticket is assigned to person A and reaches a pinned claim step for another desk
- AND active eligible person B is currently a member of that pinned desk
- WHEN B submits the workflow completion route without a reason
- THEN B becomes the assignee and the run advances
- AND exactly one contextual assignment event is recorded

#### Scenario: Generic manual reassignment retains reason requirement

- GIVEN a ticket is assigned to person A
- WHEN an authorized actor uses `POST /tickets/{id}/assign` or `TicketService.Assign` to assign person B without a reason
- THEN that manual reassignment is rejected
- AND historical audit reasons remain renderable

#### Scenario: Least-loaded uses global open load and stable tie-break

- GIVEN eligible desk members 7 and 9 each have two assigned tickets in `new` or `in_progress` across multiple categories
- AND member 7 has additional resolved tickets
- WHEN `least_loaded` executes
- THEN resolved tickets do not increase the counted load
- AND member 7 is selected because their user ID is lower

#### Scenario: Assignment on in-progress ticket does not re-transition

- GIVEN an `in_progress` ticket reaches a later desk-assignment step
- WHEN a person is successfully assigned
- THEN the assignee changes through the audited assignment flow
- AND no redundant `in_progress` transition or transition audit is created

### Requirement: Form Task Completion and Visibility

A `form[requester]` task MUST accept answers only from the authenticated requester. A `form[assignee]` task MUST accept answers only from the current assignee. Submitted values MUST be validated against the form's pinned field definitions before the task advances. Answers MUST be stored as workflow-task answers rather than comments or audit notes. Completed answers MUST be read-only, and every answer submitted by an assignee MUST be visible to the requester inline within the merged activity timeline under that step's own completion event.

Form answers MUST decode strictly by pinned field position and type. A checkbox answer MUST decode absent or empty as false, a string `on` or `true` as true, and a JSON boolean `true` as true; a JSON boolean `false` is valid and stays false. Any other checkbox value MUST be rejected. A required checkbox MUST accept a decodable false or absent answer, so Required MUST NOT force a checkbox to be true. Text values MUST be trimmed, and blank text on a required field MUST be invalid. A single-select MUST match a pinned option exactly, and a padded or unknown option MUST be rejected. The answer array MUST match the pinned field count and every position; a wrong count, an unknown position, a duplicate position, an ambiguous multi-value position, or extra entries beyond the pinned definition MUST be rejected. At the storage boundary a checkbox MUST decode only from a JSON boolean, and a JSON string such as `"true"` MUST be rejected. Decode errors MUST NOT leak raw persisted values. Answers MUST persist as a typed JSON positional array.
(Previously: the requirement defined actor ownership, pinned validation before advancement, answer storage outside comments/audit notes, and requester visibility, but did not state the strict positional typed decoding matrix.)

#### Scenario: Requester completes supported fields

- GIVEN a requester form with short text, long text, checkbox, and single-select fields
- WHEN the authenticated requester submits values valid for the pinned definitions
- THEN the answers are stored for that task
- AND the run advances once

#### Scenario: Invalid field answer does not advance

- GIVEN a pending form with required and single-select constraints
- WHEN its authorized actor submits invalid values
- THEN plain field validation errors are returned
- AND no answers are committed and the cursor does not advance

#### Scenario: Assignee answer is requester-visible

- GIVEN the current assignee completes an assignee form
- WHEN the requester later reads the ticket detail
- THEN the completed answers are visible as read-only content inline in the merged activity timeline

#### Scenario: Non-actor cannot submit form

- GIVEN a pending requester form
- WHEN an assignee, admin, or root who is not the requester submits answers
- THEN completion is denied
- AND no answers or cursor change are persisted

#### Scenario: Checkbox decodes strictly

- GIVEN a checkbox field pinned in a form
- WHEN an absent answer, an empty string, `on`, `true`, or a JSON boolean `true` is submitted
- THEN the stored checkbox value is false for absent and empty, and true for `on`, `true`, and JSON boolean `true`
- WHEN any other string such as `yes` is submitted
- THEN decoding is rejected

#### Scenario: Required checkbox accepts false or absent

- GIVEN a pinned checkbox field marked Required
- WHEN the answer is absent, empty, or a JSON boolean `false`
- THEN decoding succeeds
- AND the stored value stays false
- AND no true answer is forced

#### Scenario: Strict positional shape is enforced

- GIVEN a pinned form whose answer array is submitted
- WHEN the array has an unknown position, a duplicate position, an ambiguous multi-value position, extra entries beyond the pinned definition, or a wrong total count
- THEN decoding is rejected
- AND no answers are committed

#### Scenario: Single-select matches a pinned option exactly

- GIVEN a single-select field pinned with options such as `eu-west-1` and `us-east-1`
- WHEN the submitted value is a pinned option
- THEN decoding succeeds
- WHEN the submitted value is unknown or carries padding such as ` eu-west-1 `
- THEN decoding is rejected

#### Scenario: Text values are trimmed and required blanks are invalid

- GIVEN a required text field
- WHEN a value such as `  hello  ` is submitted
- THEN the stored value is trimmed to `hello`
- WHEN only whitespace is submitted
- THEN decoding is rejected

#### Scenario: Answers persist as a typed JSON positional array

- GIVEN a valid answer set for a pinned form
- WHEN the task completes
- THEN the answers persist as a typed JSON positional array in pinned field order
- AND a checkbox persists as a JSON boolean, not a string

#### Scenario: Store decodes checkbox strictly and never leaks raw values

- GIVEN persisted answer bytes for a pinned form
- WHEN a checkbox position holds a JSON string such as `"true"` or the answer count differs from the pinned fields
- THEN decoding is rejected
- AND when a single-select value lies outside the pinned options, the decode error does not expose the raw persisted value

### Requirement: Manual Task Completion

A `manual_task` MUST be completed only by the ticket's current assignee. Completion MUST preserve the completing actor's user ID in its audit data. `admin` and `root` MUST NOT override this rule except by first becoming current assignee through audited self-reassignment.

Completion MUST accept an OPTIONAL solution string submitted with the completion request. An absent, empty, or whitespace-only solution MUST complete the task normally without one. A non-empty solution MUST persist atomically in the same unit of work as that exact ticket/step/actor completion — task completion, cursor advancement, and audit event together — MUST stay tied to the completion's sealed pinned step index, and MUST be stored only with the workflow task record family, never in comments, audit note/reason fields, or full-text search. A failed completion MUST persist neither the completion nor any partial solution. Every actor authorized to view the ticket MUST be able to read the stored solution rendered as escaped plain text.

#### Scenario: Current assignee marks task done

- GIVEN an `in_progress` ticket with a pending manual task assigned to agent A
- WHEN agent A marks the task done
- THEN the task completes, the actor ID is preserved, and the run advances

#### Scenario: Admin cannot bypass assignment

- GIVEN a pending manual task assigned to agent A
- WHEN an admin who is not the current assignee marks it done
- THEN completion is denied
- AND the task remains pending

#### Scenario: Completion without a solution advances normally

- GIVEN the current assignee submits a completion with no solution, an empty string, or whitespace only
- WHEN the completion is processed
- THEN the task completes and the run advances exactly as without a solution
- AND no solution is stored or rendered for the completed task

#### Scenario: Non-empty solution persists atomically with its completion event

- GIVEN the current assignee submits a valid completion carrying a non-empty solution
- WHEN the completion commits
- THEN the completed task, cursor advancement, and audit event recording the actor and sealed step index commit in one atomic unit of work
- AND the solution stays tied to that same pinned step index
- WHEN storage fails at any point during the operation
- THEN neither the completion nor any partial solution is persisted

#### Scenario: Solution renders escaped to authorized viewers

- GIVEN a completed manual task with a non-empty solution containing markup-like text
- WHEN any actor authorized to view the ticket reads the detail page
- THEN the solution appears as escaped plain text attributed to the completion event's actor and timestamp
- AND no submitted markup executes or alters page structure

#### Scenario: Solution stays out of audit notes and search indexes

- GIVEN a completed manual task whose solution contains a distinctive marker string
- WHEN its audit row and full-text search documents are inspected
- THEN the marker appears in neither the audit note nor reason fields nor any indexed document
- AND the solution exists only in the workflow task record

### Requirement: Resolve Ticket Terminal Step

`resolve_ticket` MUST be an automatic standalone final step and MUST invoke `Ticket.Transition(resolved)` for a ticket in `new` or `in_progress`. The transition and its audit MUST be atomic with terminal-step completion. For a ticket already in `resolved` or `closed`, the step MUST complete as a no-op without a transition audit. For a `cancelled` ticket, the step MUST reject and MUST NOT complete. Resolution timestamps MUST be changed only by `Ticket.Transition`.

#### Scenario: Resolve from new

- GIVEN a `new` ticket reaches `resolve_ticket`
- WHEN the automatic step executes
- THEN `Ticket.Transition(resolved)` succeeds
- AND the transition, workflow-attributed audit, and run completion commit atomically

#### Scenario: Resolve from resolved or closed is a no-op

- GIVEN a ticket already in `resolved` or `closed` reaches its pinned `resolve_ticket` completion check
- WHEN the automatic step executes
- THEN the run completes
- AND ticket state and transition audit history remain unchanged

#### Scenario: Resolve from cancelled rejects

- GIVEN a `cancelled` ticket reaches `resolve_ticket`
- WHEN the automatic step executes
- THEN execution is rejected
- AND neither the task nor run is completed

### Requirement: Close Ticket Terminal Step

`close_ticket` MUST be an automatic standalone final step and MUST perform every state change through `Ticket.Transition`. From `new` or `in_progress`, it MUST atomically transition first to `resolved` and then to `closed`, with two corresponding workflow-attributed transition audits. From `resolved`, it MUST transition only to `closed` with one audit. From `closed`, it MUST complete as a no-op without a transition audit. From `cancelled`, it MUST reject without completing. Resolution and closure timestamps MUST be changed only by those transitions.

#### Scenario: Close from new or in-progress

- GIVEN a ticket in `new` or `in_progress` reaches `close_ticket`
- WHEN the automatic step executes
- THEN `Ticket.Transition(resolved)` executes before `Ticket.Transition(closed)`
- AND both transitions, two audits, and run completion commit atomically

#### Scenario: Close from resolved

- GIVEN a `resolved` ticket reaches `close_ticket`
- WHEN the automatic step executes
- THEN only `Ticket.Transition(closed)` executes
- AND one transition audit is appended

#### Scenario: Close from closed is a no-op

- GIVEN a `closed` ticket reaches its pinned `close_ticket` completion check
- WHEN the automatic step executes
- THEN the run completes
- AND no state or transition audit changes

#### Scenario: Close from cancelled rejects

- GIVEN a `cancelled` ticket reaches `close_ticket`
- WHEN the automatic step executes
- THEN execution is rejected
- AND no transition, audit, or run completion is persisted

### Requirement: Representative Linear Journeys

The closed workflow model and executor MUST support the simple-routing, new-server, AWS-access, and multi-desk-offboarding journeys without branching or additional step types.

#### Scenario: Simple routing journey

- GIVEN a workflow containing only `assign_to_desk[Network,claim]`
- WHEN an eligible Network member claims the ticket
- THEN the person is assigned, the ticket moves from `new` to `in_progress`, and the workflow completes
- AND the agent MAY later resolve the ticket through the existing manual lifecycle authorization

#### Scenario: New server journey

- GIVEN a workflow `form[requester]`, `assign_to_desk[Infra,least_loaded]`, `manual_task`, `form[assignee]`, `resolve_ticket`
- WHEN each authorized actor completes the current step in order
- THEN the pinned run completes in `resolved`
- AND the requester can read the assignee's completed answers

#### Scenario: AWS access journey

- GIVEN a workflow `form[requester]`, `assign_to_desk[Platform,claim]`, `manual_task`, optional `form[assignee]`, `resolve_ticket`
- WHEN the requester supplies valid checkbox and single-select answers and the remaining actors complete their steps
- THEN the workflow completes linearly in `resolved`

#### Scenario: Multi-desk offboarding journey

- GIVEN a workflow `assign_to_desk[HR,claim]`, `manual_task`, `assign_to_desk[IT,claim]`, `manual_task`, `assign_to_desk[Finance,claim]`, `close_ticket`
- WHEN each desk member and assignee completes the current step in order
- THEN each assignment persists a person and the run reaches `close_ticket`
- AND `close_ticket` resolves and closes atomically without a preceding `resolve_ticket` step

### Requirement: Completed Steps Render Inside the Merged Timeline

Every completed category-flow step MUST appear in the single merged ticket activity timeline as its own audit event, newest-first; the ticket detail MUST NOT render a separate responses card for completed steps. A form completion event MUST present its pinned submitted field labels and values inline, and a manual completion event MUST present its contextual pinned instruction read from the immutable pinned version at the persisted sealed step index, so a later publication can never alter what an already-completed task shows. When the assignee submitted a non-empty solution, that manual completion event MUST additionally present the solution as escaped plain text; when no non-empty solution exists, the event MUST show the instruction alone with no empty solution placeholder. The event-to-step correlation uses only the persisted sealed step index — never timestamps or order inference — and a missing or inconsistent context renders only the safe summary without fabrication. Existing actor/timestamp/newest-first ordering is preserved. Ticket-facing copy and actor labels MUST NOT mention `workflow`: automatic events omit actor text while human events keep their attributed actor names.

#### Scenario: Completed steps live in the one timeline

- GIVEN a run has completed a requester form, a desk assignment, a manual task, and an automatic resolve
- WHEN the ticket detail renders for any authorized reader
- THEN all completed steps appear inside the single newest-first timeline among comments, assignments, and transitions
- AND no standalone responses card is rendered anywhere on the page

#### Scenario: Requester reads assignee answers inline

- GIVEN the current assignee completed an assignee form
- WHEN the requester reads the ticket detail
- THEN the form completion event in the timeline shows the pinned field labels with the submitted read-only values

#### Scenario: Ticket-facing copy avoids workflow terminology

- GIVEN any ticket detail state including pending automatic steps and completed events
- WHEN the page renders for a requester or agent
- THEN no visible copy or actor label contains the word `workflow`
- AND automatic pending-step explanatory text uses neutral wording

#### Scenario: Manual item shows instruction always and solution only when written

- GIVEN one manual task completed with a non-empty solution and another completed without one
- WHEN their timeline items render for an authorized reader
- THEN both items show their pinned instructions verbatim and escaped
- AND only the solved task's item additionally shows the solution text
- AND the unsolved item shows no empty solution placeholder

#### Scenario: Pinned instruction survives a later publish

- GIVEN a manual task completed against pinned version 2 whose instructions differ from newly published version 3
- WHEN its timeline item renders after the new publication
- THEN the item still shows the version 2 instructions read through the persisted step index
- AND no version 3 wording replaces them
