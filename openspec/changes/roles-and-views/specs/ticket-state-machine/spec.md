# Delta for Ticket State Machine

## MODIFIED Requirements

### Requirement: State Transition Enforcement

The system MUST validate every state transition against the allowed matrix and MUST reject invalid transitions with an error. State changes MUST NOT occur silently. Transition execution MUST be restricted by actor role: role `user` MUST NOT perform transitions of any ticket; role `agent` SHALL transition only tickets assigned to them through legal transitions; roles `admin` and `root` SHALL transition any ticket. Authorization MUST be enforced server-side before the transition is applied. (Previously: any logged-in user could transition any ticket.)

Allowed transitions:

- `new` → `in_progress`, `resolved`, `cancelled`
- `in_progress` → `resolved`, `cancelled`
- `resolved` → `closed`, `in_progress` (reopen)
- `closed` → `in_progress` (reopen)

Invalid: `new` → `closed`; `resolved` → `cancelled`; `closed` → `cancelled`; `closed` → `resolved`; any move into `new`; any move out of `cancelled`.

#### Scenario: Valid forward path

- GIVEN a ticket in state `new` assigned to an `agent`
- WHEN the agent moves it to `in_progress`, then `resolved`, then `closed`
- THEN each transition succeeds and the final state is `closed`

#### Scenario: Invalid transition rejected

- GIVEN a ticket in state `new`
- WHEN an `admin` attempts to move it directly to `closed`
- THEN the transition is rejected with an error
- AND the state remains `new`

#### Scenario: Terminal cancelled

- GIVEN a ticket in state `cancelled`
- WHEN an `admin` attempts any transition out of it
- THEN the transition is rejected

#### Scenario: User role cannot transition

- GIVEN a ticket owned by a `user`-role actor and any legal transition target
- WHEN the user attempts a transition
- THEN the request is denied
- AND the state remains unchanged

#### Scenario: Agent transitions only assigned tickets

- GIVEN a ticket assigned to agent Y and a different `agent` X
- WHEN X attempts a legal transition on the ticket
- THEN the request is denied