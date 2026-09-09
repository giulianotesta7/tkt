# Delta for User Management

## ADDED Requirements

### Requirement: User drawer unsaved-change protection
The Users create and edit drawers MUST protect only mutable form values against accidental close. Creation tracking MUST include `name`, `email`, and `password`. Edit tracking MUST include `name`, `email`, `role`, and `active`. Values that are not rendered by the current drawer mode MUST NOT affect dirty state. Reverting every tracked value to its baseline MUST make the drawer clean.

#### Scenario: Dirty User drawer requires an explicit decision
- GIVEN an administrator changes a tracked User drawer value
- WHEN they request close through Close, Cancel, the backdrop, Escape, or browser Back
- THEN the existing Users confirmation modal MUST open with its existing wording and destructive styling
- AND Stay MUST have initial focus
- AND the modal MUST trap focus
- AND Escape or native dialog cancellation MUST act as Stay

#### Scenario: User drawer baseline survives unrelated HTMX settles
- GIVEN a User drawer has unsaved tracked values
- WHEN an unrelated HTMX settle occurs while that drawer remains open
- THEN the drawer MUST remain dirty
- AND a close request MUST still open the confirmation modal

#### Scenario: User validation does not restore plaintext password
- GIVEN User creation is submitted and the server returns a validation error
- WHEN the replacement drawer settles
- THEN it MUST remain dirty with the server-rendered Name and Email values
- AND the submitted plaintext Password MUST NOT be restored or rendered

#### Scenario: Successful User save clears the guard
- GIVEN a User drawer has unsaved tracked values
- WHEN the existing User save succeeds
- THEN the existing save close behavior MUST run without a confirmation
