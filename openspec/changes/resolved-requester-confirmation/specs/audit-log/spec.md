# Delta for Audit Log

Scope note: this is the spec-phase delta for change `resolved-requester-confirmation` (issue #55). It adds a behavioral closure-attribution requirement: the audit trail MUST distinguish how a ticket was closed. The exact attribution mechanism (schema shape, actor conventions, or audit fields) is a design decision and is deliberately not specified here; existing actor conventions and `No Silent Mutations` are unchanged by this delta.

## ADDED Requirements

### Requirement: Closure Attribution

Every closure of a ticket (a transition into `closed`) MUST be recorded in the audit trail so that a reader of the audit history can determine which closure path closed the ticket. The system MUST distinguish at least these closure paths: closure by requester confirmation, closure by a workflow terminal `close_ticket` step, and manual agent closure of a requester-less ticket (requester user ID NULL). Two different closure paths MUST NOT be recorded indistinguishably. Every closure MUST still produce its transition audit event or events, and No Silent Mutations MUST continue to hold for every closure path.

#### Scenario: Requester-confirmation closure is distinguishable

- GIVEN a requester-owned ticket in `resolved`
- WHEN the requester confirms the resolution and the ticket closes
- THEN the audit history shows the closure attributed to the requester-confirmation path
- AND it is distinguishable from a workflow-terminal closure of the same transition

#### Scenario: Manual agent closure of a requester-less ticket is distinguishable

- GIVEN a `resolved` ticket with requester user ID NULL
- WHEN an authorized agent closes it manually
- THEN the audit history shows the closure attributed to a manual agent closure

#### Scenario: Workflow-terminal closure is distinguishable

- GIVEN a `resolved` ticket reaches a `close_ticket` terminal step
- WHEN the workflow closes the ticket
- THEN the audit history shows the closure attributed to the workflow-terminal path
- AND the transition audit events keep the existing workflow actor convention

#### Scenario: Every closure path remains audited

- GIVEN any of the three closure paths
- WHEN a ticket enters `closed`
- THEN at least one transition audit event records the entry into `closed`
