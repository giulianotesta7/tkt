# Delta for User Management

## MODIFIED Requirements

### Requirement: Update User

Roles `admin` and `root` MUST edit name, email, role where permitted, and active state at `/users/{id}/edit`. The edit form MUST NOT contain a password field. Role and identity/status edits MUST succeed atomically or make no changes. The root account MUST NOT be edited.
(Previously: role changes were list actions and password could be included in the general edit.)

#### Scenario: Atomic combined edit
- GIVEN an eligible target and a valid name/email plus permitted role
- WHEN an admin submits the edit form
- THEN all requested non-password changes commit together

#### Scenario: Invalid role causes rollback
- GIVEN a valid identity edit and a forbidden role transition
- WHEN the form is submitted
- THEN no identity, role, or active-state change is committed

## ADDED Requirements

### Requirement: Dedicated Password Change
The system MUST accept password changes only at `POST /users/{id}/password`, validate and hash the new password, and MUST NOT echo or persist plaintext.

#### Scenario: Password endpoint
- GIVEN an eligible non-root target and a non-empty password
- WHEN the endpoint is posted
- THEN only the password hash changes

### Requirement: Explicit Account Status Action
The edit form MUST present “Deactivate user” or “Reactivate user” according to current state, preserving root and role protections.

#### Scenario: Status protection
- GIVEN a root or protected admin target
- WHEN a forbidden status action is submitted
- THEN it is rejected and the target state is unchanged
