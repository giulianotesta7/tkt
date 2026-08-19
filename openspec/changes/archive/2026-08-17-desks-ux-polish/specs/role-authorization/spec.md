# Delta for Role Authorization

## MODIFIED Requirements

### Requirement: Role Management Matrix

The existing role hierarchy and permitted transitions MUST remain unchanged. Role changes MUST be initiated only from `/users/{id}/edit`, MUST be audited with the acting user, and MUST be atomic with the permitted identity/status edits submitted in that form. The former list combobox and `POST /users/{id}/role` endpoint MUST NOT be available.
(Previously: role changes were explicit list/form actions at a separate endpoint.)

#### Scenario: Role edit from user form
- GIVEN an admin editing an eligible user
- WHEN the admin selects an allowed role and submits `/users/{id}/edit`
- THEN the role changes and the transition is audited

#### Scenario: Former endpoint removed
- GIVEN any actor
- WHEN they POST `/users/{id}/role`
- THEN the endpoint is unavailable or rejected and no role changes
