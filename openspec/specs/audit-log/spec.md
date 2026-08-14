---
name: audit-log
status: proposed
change: tkt-mvp
---

# Audit Log Specification

## Purpose

Defines the append-only audit trail covering every state transition and field change. There are no silent mutations. The actor of every event is the logged-in user from the session.

## Requirements

### Requirement: Transition Audit Events

The system MUST append an audit event for every state transition, recording actor, action (from state → to state), and timestamp. The actor MUST be the logged-in user taken from the session. Genuine system actions, if any, MUST use the fixed actor `sistema`.

#### Scenario: Transition recorded

- GIVEN a ticket in state `new`
- WHEN it transitions to `in_progress`
- THEN an audit event is appended with the transition action and timestamp

#### Scenario: Actor comes from session

- GIVEN a logged-in user
- WHEN that user performs a transition
- THEN the audit event records that user as actor

### Requirement: Field Change Audit Events

The system MUST append an audit event for every field change, recording actor, field, from_value, to_value, and timestamp. The actor MUST be the logged-in user taken from the session.

#### Scenario: Field change recorded

- GIVEN a ticket with priority `medium`
- WHEN its priority changes to `high`
- THEN an audit event is appended with field `priority`, from `medium`, to `high`, and timestamp

#### Scenario: Actor from session for field edits

- GIVEN a logged-in user
- WHEN that user edits a ticket field
- THEN the audit event records that user as actor

### Requirement: No Silent Mutations

Every state transition and every field change MUST produce an audit event. The system MUST NOT apply a mutation without recording it.

#### Scenario: Every mutation audited

- GIVEN a ticket
- WHEN one transition and two field edits occur
- THEN exactly three corresponding audit events exist, in occurrence order

### Requirement: Audit History Retrieval

The system MUST expose each ticket's audit events in chronological occurrence order at the storage boundary. The ticket detail presentation MUST merge those events with comments into a newest-first timeline and visually distinguish audit events from agent comments.

#### Scenario: History order

- GIVEN multiple audit events for one ticket
- WHEN the history is retrieved
- THEN the events are returned in the order they occurred

#### Scenario: Audit events in merged presentation timeline

- GIVEN audit events and comments on one ticket
- WHEN the ticket detail timeline is rendered
- THEN all entries are merged newest first
- AND audit events have system styling distinct from comments
