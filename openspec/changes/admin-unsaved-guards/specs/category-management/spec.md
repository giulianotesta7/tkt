# Delta for Category Management

## ADDED Requirements

### Requirement: Catalog drawer unsaved-change protection
The Category, Department, and Desk create and edit drawers MUST compare only their kind-specific mutable form values with the baseline captured when that drawer opens. Category tracking MUST include `name`, `description`, `department_id`, and `desk_id`. Department tracking MUST include `name` and `description`. Desk tracking MUST include `name`, `description`, and `department_id`. Reverting every tracked value to its baseline MUST make the drawer clean.

This requirement supersedes the category-only scope restriction in the active `category-unsaved-guard` delta.

#### Scenario: Dirty catalog drawer requires a decision
- GIVEN an administrator changes a tracked value in a Category, Department, or Desk drawer
- WHEN they request close through Close, Cancel, the backdrop, Escape, or browser Back
- THEN the application MUST show the existing native confirmation modal
- AND Stay MUST have initial focus
- AND the modal MUST trap focus
- AND Escape or native dialog cancellation MUST act as Stay

#### Scenario: Stay retains the drawer state
- GIVEN a dirty catalog drawer has opened the confirmation modal
- WHEN the administrator chooses Stay or cancels the modal
- THEN the drawer values and URL MUST remain unchanged
- AND focus MUST return to the prior valid drawer control

#### Scenario: Discard closes without a form submission
- GIVEN a dirty catalog drawer has opened the confirmation modal
- WHEN the administrator chooses Discard changes
- THEN the drawer MUST close without posting its main form
- AND focus MUST return to the launcher or the existing catalog fallback

#### Scenario: Desk membership replacement retains pending Desk fields
- GIVEN an administrator has changed a Desk drawer's name, description, or department
- WHEN a successful add-member or remove-member response replaces that exact Desk drawer
- THEN those pending Desk field values MUST remain in the replacement drawer
- AND the persisted membership mutation MUST remain applied

#### Scenario: Errors and saves retain server semantics
- GIVEN a catalog form submission returns a server-rendered validation error
- WHEN its drawer replacement settles
- THEN the drawer MUST remain dirty with the server-rendered submitted values
- WHEN a catalog form submission succeeds
- THEN its existing successful close behavior MUST run without a confirmation
