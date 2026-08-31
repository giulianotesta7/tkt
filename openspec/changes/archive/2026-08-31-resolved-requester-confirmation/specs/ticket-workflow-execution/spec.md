# Delta for Ticket Workflow Execution

Scope note: this is the spec-phase delta for change `resolved-requester-confirmation` (issue #55). It states that a run ending in `resolve_ticket` leaves a requester-owned ticket awaiting requester confirmation, preserves `close_ticket` as the sanctioned direct workflow closure path, and adds the workflow-detachment consequence of requester rejection. No canonical file, runtime, template, test, or migration is edited here.

## MODIFIED Requirements

### Requirement: Resolve Ticket Terminal Step

`resolve_ticket` MUST be an automatic standalone final step and MUST invoke `Ticket.Transition(resolved)` for a ticket in `new` or `in_progress`. The transition and its audit MUST be atomic with terminal-step completion. For a ticket already in `resolved` or `closed`, the step MUST complete as a no-op without a transition audit. For a `cancelled` ticket, the step MUST reject and MUST NOT complete. Resolution timestamps MUST be changed only by `Ticket.Transition`. When the run completes with the ticket in `resolved` and the ticket has an identifiable requester, the ticket MUST await requester confirmation: the workflow MUST NOT close it, and the ticket MUST remain `resolved` until the requester confirms, the requester rejects, or — for a ticket with requester user ID NULL — an authorized agent closes it manually. (Previously: the requirement ended the run in `resolved` without stating that a requester-owned ticket then awaits confirmation.)

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

#### Scenario: Resolve leaves a requester-owned ticket awaiting confirmation

- GIVEN a ticket with an identifiable requester in `in_progress` reaches `resolve_ticket`
- WHEN the automatic step executes
- THEN the ticket transitions to `resolved` and the run completes
- AND the ticket remains `resolved` awaiting requester confirmation with no close transition

### Requirement: Close Ticket Terminal Step

`close_ticket` MUST be an automatic standalone final step and MUST perform every state change through `Ticket.Transition`. From `new` or `in_progress`, it MUST atomically transition first to `resolved` and then to `closed`, with two corresponding workflow-attributed transition audits. From `resolved`, it MUST transition only to `closed` with one audit. From `closed`, it MUST complete as a no-op without a transition audit. From `cancelled`, it MUST reject without completing. Resolution and closure timestamps MUST be changed only by those transitions. The `close_ticket` terminal step is a sanctioned direct closure path: it MUST close the ticket without requester confirmation, for tickets with or without a requester. (Previously: the requirement did not state its relationship to requester confirmation.)

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

#### Scenario: Workflow closes a requester-owned ticket directly

- GIVEN a `resolved` ticket with an identifiable requester reaches `close_ticket`
- WHEN the automatic step executes
- THEN the ticket transitions to `closed` without requester confirmation
- AND the audit trail attributes the closure to the workflow-terminal path

## ADDED Requirements

### Requirement: Requester Rejection Detaches the Workflow

When the requester rejects the resolution of a `resolved` ticket, the transition to `in_progress` MUST detach the workflow: the ticket MUST continue as a manual ticket, MUST NOT execute further workflow steps, and MUST NOT re-enter any workflow run. A ticket without a workflow link that is rejected MUST simply return to `in_progress`. Rejection is a manual transition, not a workflow operation: it MUST NOT be attributed to the workflow and MUST NOT execute, complete, or mutate any workflow step, cursor, or answer.

#### Scenario: Rejected run leaves a manual ticket

- GIVEN a workflow-pinned ticket in `resolved` after its `resolve_ticket` step completed the run
- WHEN the requester rejects the resolution
- THEN the ticket returns to `in_progress` with the workflow link detached
- AND no further workflow step executes for that ticket

#### Scenario: Manual ticket rejection performs no workflow operation

- GIVEN a `resolved` ticket with no workflow link
- WHEN the requester rejects the resolution
- THEN the ticket returns to `in_progress`
- AND no workflow step, cursor, or task is created or mutated
