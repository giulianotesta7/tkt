# Delta for Category Management

## ADDED Requirements

### Requirement: Category drawer unsaved-change protection
The Category create and edit drawers MUST protect only unsaved Category form changes. The guard MUST compare `name`, `description`, `department_id`, and `desk_id` with a baseline captured after Department filtering has applied. Returning every tracked value to that baseline MUST make the drawer clean.

Department and Desk drawers, page navigation outside the drawer, and browser unload MUST NOT receive this guard.

#### Scenario: Confirm a dirty close request
- GIVEN an administrator changes a tracked value in a Category create or edit drawer
- WHEN they request close through Close, Cancel, the backdrop, Escape, or browser Back
- THEN the application MUST show a native modal with Stay and Discard actions
- AND Stay MUST be initially focused
- AND the modal MUST trap focus
- AND Escape or native dialog cancellation MUST act as Stay
- AND its heading, explanatory copy, action labels, spacing, focus treatment, and destructive action styling MUST match the existing Users unsaved-change dialog without reusing Users behavior attributes

#### Scenario: Stay preserves the drawer
- GIVEN the unsaved-change modal is open
- WHEN the administrator chooses Stay or presses Escape
- THEN the Category drawer values and drawer URL MUST remain unchanged
- AND focus MUST return to the control that requested the close

#### Scenario: Discard closes a Category drawer
- GIVEN the unsaved-change modal is open
- WHEN the administrator chooses Discard
- THEN the drawer MUST close without submitting the form
- AND the selected catalog context MUST remain visible
- AND focus MUST return to the launcher or the existing Category fallback

#### Scenario: Browser Back preserves the current drawer URL
- GIVEN a dirty Category drawer is open from a history entry
- WHEN the administrator uses browser Back and chooses Stay
- THEN the drawer URL and its query context MUST remain unchanged
- AND the application MUST capture that URL before the browser changes the current location

#### Scenario: Errors and saves retain their existing semantics
- GIVEN a Category mutation returns a server-rendered validation error
- WHEN the replacement drawer settles
- THEN it MUST remain dirty with the submitted values intact
- AND unrelated HTMX settles MUST NOT reset the Category baseline
- WHEN a Category mutation succeeds and emits `categories:saved`
- THEN the drawer MUST close without showing the unsaved-change modal
