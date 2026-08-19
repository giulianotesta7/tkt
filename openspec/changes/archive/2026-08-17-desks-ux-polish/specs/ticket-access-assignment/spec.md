# Delta for Ticket Access and Assignment

## MODIFIED Requirements

### Requirement: Person-Only Assignment

Assignment MUST continue to target only a person with role `agent`, `admin`, or `root`; desk membership MUST NOT alter assignment semantics. No group/desk assignment field or persisted desk assignment MAY be introduced.
(Previously: the prohibition referred to groups rather than desks.)

#### Scenario: Desk assignment rejected
- GIVEN a desk and an accessible ticket
- WHEN a caller submits the desk as assignee
- THEN the request is rejected and the person assignee remains unchanged

#### Scenario: Person assignment preserved
- GIVEN a ticket assigned to an agent before migration
- WHEN migration 0004 runs
- THEN the assignment remains attached to the same person and ticket
