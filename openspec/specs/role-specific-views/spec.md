# Role-Specific Views Specification

## Purpose

Defines capability-gated navigation, forms, and controls per role, and mandates that presentation gating never substitutes for server-side authorization.

## Requirements

### Requirement: Capability-Gated Navigation

The shell MUST show Desks, not Groups, to authorized `admin` and `root` actors, with an accessible desk/table SVG icon. Users MUST see no desk link. Ticket-list controls MUST follow role-specific search/filter rules while server authorization remains unchanged.
(Previously: authorized navigation used Groups terminology and a letter icon.)

#### Scenario: Desk navigation
- GIVEN an admin or root
- WHEN the shell renders
- THEN a `/desks` link labeled “Desks” has an accessible SVG icon

#### Scenario: User has compact ticket search
- GIVEN a user-role actor
- WHEN the ticket list renders
- THEN ID/title search is present and the full filter bar is absent

### Requirement: Presentation Gating Is Not Authorization

For every capability, the system MUST enforce authorization server-side, independent of rendered UI. A denied actor MUST receive 403 or 404 on direct routes, direct form posts, and HTMX fragment requests even when the relevant link or control is hidden.

#### Scenario: Direct route denied despite hidden link

- GIVEN a `user`-role actor whose navigation hides User Management
- WHEN they request the user-management route directly
- THEN the request is denied with 403 or 404
- AND no user data is returned

#### Scenario: Hidden control still denied

- GIVEN an `agent` with no visible category controls
- WHEN they POST a category-creation request directly
- THEN the request is denied

### Requirement: Role-Gated Ticket Controls

Ticket create and detail surfaces MUST reveal assignment, state-transition, and comment-visibility controls only for roles permitted to use them, and MUST reject hidden or forged submission fields server-side.

#### Scenario: User create form has no assignment control

- GIVEN a `user`-role actor on the ticket create form
- WHEN the form renders
- THEN no assignment control is present
- AND any submitted assignment field is rejected server-side

#### Scenario: Agent sees transition controls on assigned tickets

- GIVEN an `agent` with an assigned ticket
- WHEN the ticket detail renders
- THEN legal transition controls and comment visibility options are shown
