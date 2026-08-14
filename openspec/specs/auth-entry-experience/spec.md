# Auth Entry Experience Specification

## Purpose

Define the branded setup and login entry experience while preserving authentication, bootstrap, accessibility, and responsive behavior.

## Requirements

### Requirement: Branded auth identity and content

The system MUST present setup and login as a unified `tkt` experience using the established palette, compact 34×34 rounded-square blue logo, Geist typography, and approved product-oriented copy. It MUST NOT present prohibited technical, implementation, ticket-state, audit, or hosting-marketing content.

#### Scenario: Setup displays approved copy

- GIVEN a user opens the setup route
- WHEN the page renders
- THEN it shows `Set up tkt`, the approved first-account description, `Create account`, and `Your password is stored securely.`

#### Scenario: Login shares the identity

- GIVEN a user opens the login route
- WHEN the page renders
- THEN it uses the same tkt identity and product presentation while retaining the login form behavior

#### Scenario: Prohibited content is absent

- GIVEN either auth route is rendered
- WHEN its HTML and visible copy are inspected
- THEN Ticket Desk, Server-side ticketing, state/audit wording, implementation stack wording, bcrypt wording, and the rejected promotional blocks are absent

### Requirement: Responsive presentation hierarchy

The system MUST use the approved desktop split composition with a vertically centered, visually dominant 400–460px form surface and a restrained presentation panel. The lifecycle decoration MUST be conceptual, noninteractive, data-free, and hidden at the existing approximately 900px single-column breakpoint. Mobile MUST place compact branding before the form without horizontal overflow.

#### Scenario: Desktop preserves form dominance

- GIVEN a viewport wider than the mobile breakpoint
- WHEN setup renders
- THEN the presentation panel and vertically centered form appear in the approved split, with the form remaining the visual focus

#### Scenario: Mobile removes decoration

- GIVEN a viewport at or below the single-column breakpoint
- WHEN setup or login renders
- THEN compact branding precedes the form, lifecycle decoration is hidden, and the page has no horizontal overflow

### Requirement: Presentation content is user-oriented and decorative

The system MUST show the approved welcome statement, lifecycle concepts (`Received`, `Assigned`, `Resolved`) with short user-oriented explanations, and the three approved product principles. This content MUST be noninteractive and MUST NOT contain concrete customer or ticket data.

#### Scenario: Presentation has approved concepts

- GIVEN the desktop presentation panel is visible
- WHEN its content is inspected
- THEN it contains the approved welcome statement, lifecycle concepts, and all three principles with their approved explanations

#### Scenario: Decoration cannot act as application data

- GIVEN a user views the lifecycle decoration
- WHEN they attempt to interpret or interact with it
- THEN it offers no controls, customer records, ticket records, or application state transitions

### Requirement: Existing auth contracts remain unchanged

The system MUST preserve routes, endpoints, payloads, bootstrap and first-user active-role semantics, validation, submitted name/email re-rendering, password non-echo, loading and error states, labels, autocomplete values (`name`, `email`, `new-password`), keyboard navigation, visible focus, and WCAG AA contrast. It MUST add no JavaScript, animation, or dependency.

#### Scenario: Valid first-account submission bootstraps normally

- GIVEN setup receives valid first-user form data
- WHEN the user submits the form
- THEN the existing endpoint, payload, bootstrap behavior, and active first-user role semantics execute unchanged

#### Scenario: Invalid submission preserves safe feedback

- GIVEN setup or login receives invalid data
- WHEN the form is rendered with validation errors
- THEN errors and submitted name/email values are shown as before, the password is not echoed, and loading/error behavior remains available

#### Scenario: Accessible keyboard form behavior remains

- GIVEN a keyboard-only user navigates either route
- WHEN they focus, fill, submit, or encounter an error
- THEN labels, autocomplete, visible focus, keyboard navigation, and WCAG AA contrast remain usable without animation or client-side dependencies
