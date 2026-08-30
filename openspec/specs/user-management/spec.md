---
name: user-management
status: proposed
change: tkt-mvp
---

# User Management Specification

## Purpose

Defines the managed user list used for ticket assignment and authentication: create, update, deactivate, login/logout with server-side sessions, session-protected routes, and first-user bootstrap. There are NO roles: every logged-in user can perform every operation.

## Requirements

### Requirement: Create User

Roles `admin` and `root` MUST create users (name, email, password), with non-empty name/email, unique email, bcrypt-only password storage, and new users created as active role `user` with a unique identifier. Roles `user` and `agent` MUST NOT create users. (Previously: any logged-in user could create users; roles did not exist.)

#### Scenario: Create user

- GIVEN a name, an email, and a password
- WHEN an `admin` creates the user
- THEN the user is stored as active with role `user` and a unique identifier
- AND only the bcrypt hash of the password is stored

#### Scenario: Reject duplicate email

- GIVEN an existing user with email `ana@example.com`
- WHEN an `admin` creates another user with the same email
- THEN the creation is rejected with a uniqueness error

#### Scenario: Reject missing password

- GIVEN a name and an email but no password
- WHEN an `admin` creates the user
- THEN the request is rejected with a validation error

#### Scenario: Non-admin actor denied

- GIVEN a `user`- or `agent`-role actor
- WHEN they attempt to create a user
- THEN the request is denied
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

### Requirement: Deactivate User

Roles `admin` and `root` MUST deactivate users (active = false). Deactivated users MUST keep historical assignments, MUST NOT be assignable, and MUST NOT log in. The root account MUST NOT be deactivated by any actor; `admin` MUST NOT deactivate `admin`/`root` accounts. (Previously: any logged-in user could deactivate any user.)

#### Scenario: Historical assignments preserved

- GIVEN a deactivated user with an existing assigned ticket
- WHEN that ticket is viewed
- THEN the deactivated user is still shown as assigned

#### Scenario: New assignment rejected

- GIVEN a deactivated user
- WHEN an `agent` creates a ticket assigned to that user
- THEN the assignment is rejected

#### Scenario: Deactivated user cannot log in

- GIVEN a deactivated user with otherwise valid credentials
- WHEN the user attempts to log in
- THEN the login fails with a generic error
- AND no session is created

#### Scenario: Root cannot be deactivated

- GIVEN the root account
- WHEN any actor, including root, attempts to deactivate it
- THEN the request is rejected
- AND root remains active

#### Scenario: Admin cannot deactivate admin accounts

- GIVEN an `admin` and another `admin`-role account
- WHEN the first admin attempts to deactivate the second
- THEN the request is denied
### Requirement: User Deletion

Referenced users MUST NOT be hard-deleted; deactivation is the removal mechanism. `admin` and `root` MUST delete only unreferenced users; the root account MUST NOT be deleted by any actor, and `admin` MUST NOT delete `admin`/`root` accounts. (Previously: any logged-in user could delete an unreferenced user.)

#### Scenario: Referenced user not deletable

- GIVEN a user assigned to existing tickets
- WHEN an `admin` attempts to delete the user
- THEN the deletion is rejected and deactivation is the only removal path

#### Scenario: Unreferenced user deletable

- GIVEN a non-root user with no tickets
- WHEN an `admin` deletes the user
- THEN the user is removed from the managed list

#### Scenario: Root not deletable

- GIVEN the root account
- WHEN any actor, including root, attempts to delete it
- THEN the request is rejected
- AND the root account remains
### Requirement: Agent Downgrade Ticket Handoff

When a managed role change from `/users/{id}/edit` targets role `user` for an account holding desk memberships, the system MUST perform ONE atomic lifecycle operation inside a single `BEGIN IMMEDIATE` transaction: (a) delete the account's desk memberships, (b) for each open ticket (state `new` or `in_progress`) assigned to the account, ordered by ticket id, perform handoff, (c) update the role with the same guarded UPDATE semantics as every other managed role change, (d) insert the `role_changes` row, and (e) commit — all or nothing. Any failure MUST roll back the role, the memberships, and the ticket reassignments together, leaving the account and its tickets exactly as before. Desk resolution per ticket MUST apply in priority order: (i) the `desk_id` of the latest audit event for that ticket that carries one, else (ii) the desk of the first `assign_to_desk` step (by step order) in the ticket's pinned workflow version, else (iii) unresolvable and the ticket MUST be left unassigned. Replacement selection MUST reuse the deterministic least-loaded rule over the resolved desk's membership pool, and the downgraded account MUST never be its own replacement. This handoff applies uniformly to any managed role change to `user` whose account holds desk memberships (`agent` and `admin` alike). A deactivation (`active = false`) without a role change MUST NOT trigger the handoff and MUST continue to preserve historical assignments. The HTTP user-edit flow MUST surface typed, meaningful feedback instead of a generic server error when the downgrade path is exercised.
(Previously: the trigger `trg_users_no_desk_member_downgrade` aborted every downgrade of a desk member, the HTTP surface returned a generic 500, and removing memberships first would have orphaned open tickets assigned to an account that could no longer process them.)

#### Scenario: Downgrade of a desk member succeeds atomically

- GIVEN an `agent`-role account holding desk memberships and an open ticket assigned to them
- WHEN an `admin` submits a managed role change to `user` at the user edit flow
- THEN the response is a success with no generic server error
- AND the account's role is `user` and no `desk_members` row references the account
- AND the role change is recorded in `role_changes`

#### Scenario: Open tickets reassigned to the least-loaded eligible member

- GIVEN a downgraded desk member with an open ticket whose desk resolves via audit context or pinned workflow
- WHEN the atomic handoff runs
- THEN the ticket is reassigned to the eligible member of the resolved desk with the fewest open tickets
- AND each reassignment records an audit event attributed to the initiating admin with a role-downgrade reason

#### Scenario: Unresolvable desk or empty eligible pool leaves the ticket unassigned

- GIVEN a downgraded desk member with an open ticket whose desk cannot be resolved, or whose resolved desk has no eligible member
- WHEN the atomic handoff runs
- THEN the downgrade still succeeds
- AND that open ticket becomes unassigned

#### Scenario: Closed, resolved, and cancelled tickets preserve historical assignment

- GIVEN a downgraded desk member assigned to tickets in states other than `new` or `in_progress`
- WHEN the atomic handoff runs
- THEN those tickets keep their historical assignment to the downgraded account
- AND no reassignment audit event is recorded for them

#### Scenario: Any failure rolls back role, memberships, and tickets together

- GIVEN a managed role change to `user` for a desk member whose handoff encounters a failure inside the transaction
- WHEN the operation aborts
- THEN the role, the desk memberships, and every ticket assignment remain exactly as before the attempt
- AND no role_changes row and no handoff audit event is persisted

#### Scenario: Deactivation without role change preserves assignments

- GIVEN a desk member with assigned tickets
- WHEN an `admin` deactivates the account without changing its role
- THEN the assignments remain untouched and the account keeps its desk memberships per the existing membership rules
- AND the downgrade handoff does not run

### Requirement: Login

The system MUST authenticate a user by email and password, verifying the password against the stored bcrypt hash. Correct credentials from an active user MUST create a fresh server-side session and issue a secure session cookie. Incorrect credentials MUST fail with a generic error that does not reveal whether the email exists.

#### Scenario: Login success

- GIVEN an active user with correct credentials
- WHEN the user submits email and password
- THEN a fresh session is created, a session cookie is issued, and the user is redirected into the application

#### Scenario: Wrong password

- GIVEN an active user
- WHEN the user submits the correct email with a wrong password
- THEN the login fails with a generic error
- AND no session is created

#### Scenario: Unknown email

- GIVEN no user with the submitted email
- WHEN the user submits that email with any password
- THEN the login fails with the same generic error as a wrong password
- AND no session is created

### Requirement: Logout

The system MUST destroy the server-side session on logout and clear the session cookie.

#### Scenario: Logout destroys session

- GIVEN a logged-in user
- WHEN the user logs out
- THEN the session is destroyed, the cookie is cleared, and subsequent requests are unauthenticated

### Requirement: Session-Protected Routes

The system MUST require a valid, unexpired session for all application routes except login and first-user bootstrap. Requests without a valid session MUST be redirected to the login page.

#### Scenario: Unauthenticated request redirected

- GIVEN no valid session
- WHEN the user requests a protected route
- THEN the request is redirected to the login page

#### Scenario: Expired session rejected

- GIVEN a session past its expiry
- WHEN the user requests a protected route
- THEN the request is redirected to the login page
- AND no new session is granted

### Requirement: First-User Bootstrap

When the users table is empty, the system MUST offer first-user creation instead of login and MUST never lock itself out. The first user MUST be created atomically with role `root` (see Role Authorization). The flow MUST NOT be available once a user exists. (Previously: the first user was created as a regular active user with no special privileges.)

#### Scenario: First user is root

- GIVEN an empty users table
- WHEN the first visitor opens the application
- THEN the visitor is offered first-user creation instead of login
- AND the created first user is active with role `root`

#### Scenario: Bootstrap unavailable with users present

- GIVEN at least one existing user
- WHEN a visitor attempts the bootstrap flow
- THEN the flow is not available and the visitor is directed to login

#### Scenario: Never locked out

- GIVEN an empty users table (for example before any /setup completes)
- WHEN the application is used
- THEN first-user creation is always available and a usable root login can be established
