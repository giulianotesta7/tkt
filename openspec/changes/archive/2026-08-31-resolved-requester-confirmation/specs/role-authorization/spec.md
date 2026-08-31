# Delta for Role Authorization

Scope note: this is the spec-phase delta for change `resolved-requester-confirmation` (issue #55). It adds the first role-`user` mutation carve-out: requester-keyed confirmation and rejection on their own `resolved` tickets. The existing role hierarchy and every other role-`user` prohibition stay unchanged. No canonical file, runtime, template, test, or migration is edited here.

## ADDED Requirements

### Requirement: Requester Confirmation Carve-Out

Role `user` MAY confirm or reject the resolution of a ticket, and MAY add a comment to it, only while the ticket is in state `resolved` and only when the authenticated session user equals the ticket's requester user ID. This is the sole mutation carve-out for role `user`: every other role-`user` mutation MUST remain prohibited, including editing fields, changing assignment, performing transitions outside the confirmation paths, confirming another user's ticket, and confirming a ticket that is not `resolved`. Confirmation and rejection MUST be denied for any actor whose session user is not the ticket's requester, regardless of role, including `agent`, `admin`, and `root`. An `agent`, `admin`, or `root` MAY manually close a `resolved` ticket only when its requester user ID is NULL; when a requester exists, manual closure of the `resolved` ticket MUST be denied for every actor. Authorization MUST be enforced server-side from the persisted ticket and the session identity before any write, never from submitted identifiers or hidden controls.

#### Scenario: Requester confirms their own resolved ticket

- GIVEN a `resolved` ticket with requester user ID R and the logged-in user R whose role is `user`
- WHEN R confirms the resolution
- THEN the action is allowed
- AND the ticket transitions to `closed`

#### Scenario: Role user cannot confirm someone else's ticket

- GIVEN a `resolved` ticket with requester user ID R and a different `user`-role actor A
- WHEN A attempts to confirm or reject the resolution
- THEN the request is denied
- AND no state change occurs

#### Scenario: Role user cannot confirm outside the resolved state

- GIVEN a ticket with requester user ID R in a state other than `resolved`
- WHEN the requester R attempts to confirm or reject the resolution
- THEN the request is denied
- AND no state change occurs

#### Scenario: Agents cannot confirm or close a requester-owned resolved ticket

- GIVEN a `resolved` ticket with an identifiable requester
- WHEN an `agent`, `admin`, or `root` attempts to confirm, reject, or manually close it
- THEN every attempt is denied
- AND no state change occurs

#### Scenario: Agent closes a requester-less resolved ticket

- GIVEN a `resolved` ticket with requester user ID NULL
- WHEN an authorized `agent`, `admin`, or `root` closes it
- THEN the action is allowed under the existing transition authorization
- AND the closure is audited

#### Scenario: Other role-user mutations remain prohibited

- GIVEN the requester of a `resolved` ticket with role `user`
- WHEN the requester attempts to edit fields, change assignment, or perform any transition other than confirming or rejecting the resolution
- THEN every attempt is denied
- AND no mutation occurs
