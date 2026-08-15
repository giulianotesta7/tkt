# Delta for Auth Entry Experience

## MODIFIED Requirements

### Requirement: Existing auth contracts remain unchanged

The system MUST preserve routes, endpoints, payloads, validation, submitted name/email re-rendering, password non-echo, loading and error states, labels, autocomplete values (`name`, `email`, `new-password`), keyboard navigation, visible focus, and WCAG AA contrast. It MUST add no JavaScript, animation, or dependency. The bootstrap role semantics follow Role Authorization: the first user created via `/setup` is created atomically with role `root` rather than as a regular user. (Previously: the first-user bootstrap was described as preserving a regular active first-user role with no special privileges.)

#### Scenario: Valid first-account submission bootstraps root

- GIVEN setup receives valid first-user form data
- WHEN the user submits the form
- THEN the existing endpoint and payload execute unchanged
- AND the created first user is active with role `root` per the Role Authorization specification

#### Scenario: Invalid submission preserves safe feedback

- GIVEN setup or login receives invalid data
- WHEN the form is rendered with validation errors
- THEN errors and submitted name/email values are shown as before, the password is not echoed, and loading/error behavior remains available

#### Scenario: Accessible keyboard form behavior remains

- GIVEN a keyboard-only user navigates either route
- WHEN they focus, fill, submit, or encounter an error
- THEN labels, autocomplete, visible focus, keyboard navigation, and WCAG AA contrast remain usable without animation or client-side dependencies