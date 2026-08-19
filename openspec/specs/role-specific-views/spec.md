# Role-Specific Views Specification

## Purpose

Defines capability-gated navigation, forms, and controls per role, and mandates that presentation gating never substitutes for server-side authorization.

## Requirements

### Requirement: Capability-Gated Navigation

The application shell MUST render navigation and controls according to role capability: `user` SHALL see ticket creation and own-ticket list/detail only; `agent` SHALL additionally see assigned work, assignment/transition controls, and internal comment capability; `admin` and `root` SHALL additionally see the full queue, user management, desks, and configuration (categories) surfaces. Items denied to a role MUST NOT be rendered for it.

#### Scenario: User shell

- GIVEN a `user`-role actor
- WHEN the shell renders
- THEN only create-ticket and own-tickets navigation appear
- AND Users, Desks, and Categories links are absent

#### Scenario: Admin shell

- GIVEN an `admin`
- WHEN the shell renders
- THEN queue, user management, desks, and categories navigation appear

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