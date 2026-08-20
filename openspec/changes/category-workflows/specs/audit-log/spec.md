# Delta for Audit Log

## MODIFIED Requirements

### Requirement: Transition Audit Events

The system MUST append an audit event for every state transition, recording actor, action (from state → to state), and timestamp. A manual transition MUST preserve the authenticated actor's user ID. An automatic workflow transition MUST use actor `workflow`, actor user ID NULL, action `transition`, field `state`, the actual from/to values, and no reason. The ticket timeline MUST render that automatic actor as `Workflow` without resolving a user record.
(Previously: every transition actor was the logged-in user, while genuine system actions used the fixed actor `sistema`.)

#### Scenario: Transition recorded

- GIVEN a ticket in state `new`
- WHEN it transitions to `in_progress`
- THEN an audit event is appended with the transition action and timestamp

#### Scenario: Actor comes from session

- GIVEN a logged-in user
- WHEN that user performs a transition
- THEN the audit event records that user as actor
- AND preserves that user's actor ID

#### Scenario: Automatic workflow transition is attributed

- GIVEN an automatic workflow step transitions a ticket from `in_progress` to `resolved`
- WHEN the transition commits
- THEN its audit event has actor `workflow`, actor user ID NULL, action `transition`, field `state`, and the actual state values
- AND the timeline labels the actor `Workflow`

### Requirement: Field Change Audit Events

The system MUST append an audit event for every field change, recording actor, field, from_value, to_value, and timestamp. A manual field change MUST preserve the authenticated actor's user ID. An automatic workflow field change MUST use actor `workflow` and actor user ID NULL.
(Previously: every field-change actor was the logged-in user from the session.)

#### Scenario: Field change recorded

- GIVEN a ticket with priority `medium`
- WHEN its priority changes to `high`
- THEN an audit event is appended with field `priority`, from `medium`, to `high`, and timestamp

#### Scenario: Actor from session for field edits

- GIVEN a logged-in user
- WHEN that user edits a ticket field
- THEN the audit event records that user as actor
- AND preserves that user's actor ID

#### Scenario: Automatic workflow assignment is attributed

- GIVEN `least_loaded` automatically selects a person for an unassigned ticket
- WHEN the assignment commits
- THEN the assignment audit uses actor `workflow` and actor user ID NULL

## ADDED Requirements

### Requirement: Atomic Workflow Audit Sets

Workflow-driven assignment and lifecycle mutations MUST commit with all corresponding audit events in the same atomic operation. A successful assignment of a `new` ticket MUST persist the person, the `new` to `in_progress` transition, and both audit events together. A `close_ticket` step starting from `new` or `in_progress` MUST persist both lifecycle transitions and two transition audit events together. A failed operation MUST persist neither mutations nor their events, and a completed no-op MUST NOT create a false transition event.

#### Scenario: Assignment and transition audits commit together

- GIVEN a `new` ticket reaches a desk-assignment step
- WHEN a person is successfully assigned
- THEN the person assignment and `new` to `in_progress` transition are persisted with both audit events atomically

#### Scenario: Automatic close has two audits

- GIVEN an `in_progress` ticket reaches `close_ticket`
- WHEN the automatic step succeeds
- THEN one audit records `in_progress` to `resolved`
- AND one audit records `resolved` to `closed`
- AND both events commit with both transitions atomically

#### Scenario: Closed no-op has no transition audit

- GIVEN a ticket already in `closed` reaches an applicable terminal completion check
- WHEN the workflow marks the step complete without a state change
- THEN no new transition audit event is appended
