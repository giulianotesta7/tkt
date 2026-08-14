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

The system MUST create a user with a name, an email, and a password. Name and email MUST be non-empty. Email MUST be unique across users. The password MUST NOT be stored in plaintext; the system MUST store only its bcrypt hash. New users MUST be active by default and MUST receive a unique identifier.

#### Scenario: Create user

- GIVEN a name, an email, and a password
- WHEN the admin creates the user
- THEN the user is stored as active with a unique identifier
- AND only the bcrypt hash of the password is stored

#### Scenario: Reject duplicate email

- GIVEN an existing user with email `ana@example.com`
- WHEN the admin creates another user with the same email
- THEN the creation is rejected with a uniqueness error

#### Scenario: Reject missing password

- GIVEN a name and an email but no password
- WHEN the admin creates the user
- THEN the request is rejected with a validation error

### Requirement: Update User

The system MUST allow editing a user's name, email, and password. Email uniqueness MUST apply to the new email. A password change MUST store a new bcrypt hash.

#### Scenario: Update user

- GIVEN an existing user
- WHEN the admin updates its name, email, and password
- THEN the updated values are stored and the new password hash replaces the old one

#### Scenario: Reject update to duplicate email

- GIVEN users `ana@example.com` and `beto@example.com`
- WHEN the admin renames the second user to `ana@example.com`
- THEN the update is rejected with a uniqueness error

### Requirement: Deactivate User

The system MUST support deactivation (active = false). Deactivated users MUST keep their historical ticket assignments unchanged and MUST NOT be assignable to new tickets. Assigning a new ticket to an inactive user MUST be rejected. A deactivated user MUST NOT be able to log in.

#### Scenario: Historical assignments preserved

- GIVEN a deactivated user with an existing assigned ticket
- WHEN that ticket is viewed
- THEN the deactivated user is still shown as assigned

#### Scenario: New assignment rejected

- GIVEN a deactivated user
- WHEN a user creates a ticket assigned to that user
- THEN the assignment is rejected

#### Scenario: Deactivated user cannot log in

- GIVEN a deactivated user with otherwise valid credentials
- WHEN the user attempts to log in
- THEN the login fails with a generic error
- AND no session is created

### Requirement: User Deletion

The system MUST NOT hard-delete a user that is referenced by tickets; deactivation is the removal mechanism for such users. The system MUST allow deleting a user that is not referenced by any ticket.

#### Scenario: Referenced user not deletable

- GIVEN a user assigned to existing tickets
- WHEN an admin attempts to delete the user
- THEN the deletion is rejected and deactivation is the only removal path

#### Scenario: Unreferenced user deletable

- GIVEN a user with no tickets
- WHEN an admin deletes the user
- THEN the user is removed from the managed list

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

When the users table is empty, the system MUST offer first-user creation instead of login and MUST never lock itself out. The first user MUST be created as a regular active user with no special privileges. The bootstrap flow MUST NOT be available once at least one user exists.

#### Scenario: First user created

- GIVEN an empty users table
- WHEN the first visitor opens the application
- THEN the visitor is offered first-user creation instead of login
- AND the created first user is an active regular user

#### Scenario: Bootstrap unavailable with users present

- GIVEN at least one existing user
- WHEN a visitor attempts the bootstrap flow
- THEN the flow is not available and the visitor is directed to login

#### Scenario: Never locked out

- GIVEN an empty users table (e.g., after the last unreferenced user is deleted)
- WHEN the application is used
- THEN first-user creation is always available and a usable login can be established
