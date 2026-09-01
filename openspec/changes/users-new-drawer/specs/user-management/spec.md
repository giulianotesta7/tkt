# Delta for User Management

Scope note: this change implements issue #96, moving managed-user creation into the existing Users edit drawer. The legacy `/users/new` and `POST /users` routes remain available, and no canonical spec file is edited here.

## MODIFIED Requirements

### Requirement: Create User

Roles `admin` and `root` MUST create users (name, email, password), with non-empty name/email, unique email, bcrypt-only password storage, and new users created as active role `user` with a unique identifier. Roles `user` and `agent` MUST NOT create users. The `/users/new` route MUST continue to serve the creation flow: a normal request renders the Users shell with the creation drawer open, while an HTMX request renders only the drawer fragment. The Users screen's “New user” launcher MUST open that same drawer without navigating to a separate page. Creation MUST preserve the existing authorization, validation, uniqueness, password handling, and success semantics.

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

#### Scenario: Open the creation drawer from the Users screen

- GIVEN an authorized `admin` or `root` is viewing `/users`
- WHEN they activate “New user”
- THEN the existing Users drawer opens through the HTMX drawer target
- AND the URL becomes `/users/new`
- AND the drawer is labelled “User details” and “New user”
- AND it does not render a separate summary paragraph

#### Scenario: Direct creation route remains available

- GIVEN an authorized `admin` or `root`
- WHEN they request `GET /users/new`
- THEN the response is the Users shell with the creation drawer open
- AND an HTMX request to the same route returns only the drawer fragment
- AND the drawer form posts to `POST /users`

#### Scenario: Creation drawer fields and defaults

- GIVEN the creation drawer is open
- THEN it contains required Name, Email, and Password fields
- AND it contains the copy “New accounts are created with the User role. You can change the role after creation.”
- AND it does not contain role or account-status controls
- AND a successful submission creates an active `user`-role account

#### Scenario: Create user from the drawer with HTMX

- GIVEN valid creation fields in the drawer
- WHEN the form is submitted through HTMX
- THEN `POST /users` creates the account
- AND the response status is `200`
- AND the response targets `#users-root` with an `outerHTML` swap
- AND the response triggers `users:saved` after the swap
- AND the drawer host is empty after the list refresh

#### Scenario: Normal creation submission remains a redirect

- GIVEN valid creation fields submitted without HTMX
- WHEN `POST /users` is processed
- THEN the account is created with the existing semantics
- AND the response redirects to `/users`

#### Scenario: Creation validation keeps the drawer open

- GIVEN invalid creation fields or a duplicate email
- WHEN the drawer form is submitted
- THEN the response preserves the existing validation or uniqueness status
- AND an HTMX response targets `#users-drawer-host` with an `outerHTML` swap
- AND the drawer remains labelled “New user”
- AND submitted Name and Email values are preserved
- AND the submitted plaintext Password is not rendered
- AND no role or account-status controls are rendered

## Notes

Traceability for this delta (the implementation and tests update these seams):

| Evidence | Path | What it proves |
|---|---|---|
| Users route and creation handler | `internal/adapters/http/handlers_users.go` | `/users/new`, `POST /users`, authorization, redirect, HTMX success, and typed drawer errors remain wired |
| Shared drawer | `web/templates/partials/user_drawer.html` | Create and edit modes share the drawer while create mode omits edit-only controls |
| Users launcher and shell | `web/templates/partials/users_screen.html` | “New user” opens the drawer and preserves list targeting/history attributes |
| Drawer controller | `web/templates/static/users.js` | Launcher tracking and create-mode password dirty-state participate in existing drawer lifecycle behavior |
| HTTP contract tests | `internal/adapters/http/users_creation_drawer_test.go` | Normal/HTMX route, success, validation, uniqueness, defaults, and safe-value contracts |
| Golden and asset tests | `internal/adapters/http/testdata/users_index.golden`, `internal/adapters/http/users_static_test.go` | Users shell output and conditional asset loading remain deterministic |
| E2E journeys | `e2e/tests/users.spec.ts`, `e2e/tests/helpers/auth.ts`, `e2e/tests/structural.spec.ts` | Creation is exercised from the Users drawer while legacy route coverage remains available |
