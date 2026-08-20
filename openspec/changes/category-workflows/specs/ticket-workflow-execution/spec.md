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

### Requirement: Person-Only Desk Routing

An `assign_to_desk` step MUST resolve a ticket assignee to an active person with role `agent`, `admin`, or `root` who belongs to the configured desk; a desk MUST never be stored as the assignee. For strategy `claim`, the task MUST remain pending until an eligible desk member claims it. A pending claim MUST leave a `new` ticket in `new`. A successful claim MUST assign the claimant, and a role `agent` MUST claim only for themselves. Reassignment from one person to another MUST retain the existing non-empty reason requirement. For strategy `least_loaded`, the system MUST automatically choose the eligible desk member with the fewest assigned tickets in states `new` or `in_progress` across all categories; other states MUST be excluded and a tie MUST select the lower user ID.

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

#### Scenario: Agent cannot claim for another person

- GIVEN agent A is eligible to claim a desk task
- WHEN agent A attempts to assign the claim to agent B
- THEN the request is denied
- AND the claim remains pending

#### Scenario: Reassignment retains reason requirement

- GIVEN a ticket is assigned to person A and reaches a claim step for another desk
- WHEN eligible person B attempts to claim without the reason required for A-to-B reassignment
- THEN reassignment is rejected
- AND the task remains pending

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

A `form[requester]` task MUST accept answers only from the authenticated requester. A `form[assignee]` task MUST accept answers only from the current assignee. Submitted values MUST be validated against the form's pinned field definitions before the task advances. Answers MUST be stored as workflow-task answers rather than comments or audit notes. Completed answers MUST be read-only, and every answer submitted by an assignee MUST be visible to the requester.

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
- THEN the completed answers are visible as read-only content

#### Scenario: Non-actor cannot submit form

- GIVEN a pending requester form
- WHEN an assignee, admin, or root who is not the requester submits answers
- THEN completion is denied
- AND no answers or cursor change are persisted

### Requirement: Manual Task Completion

A `manual_task` MUST be completed only by the ticket's current assignee. Completion MUST preserve the completing actor's user ID in its audit data. `admin` and `root` MUST NOT override this rule except by first becoming current assignee through audited self-reassignment.

#### Scenario: Current assignee marks task done

- GIVEN an `in_progress` ticket with a pending manual task assigned to agent A
- WHEN agent A marks the task done
- THEN the task completes, the actor ID is preserved, and the run advances

#### Scenario: Admin cannot bypass assignment

- GIVEN a pending manual task assigned to agent A
- WHEN an admin who is not the current assignee marks it done
- THEN completion is denied
- AND the task remains pending

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
