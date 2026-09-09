# Delta for Desk Management

## ADDED Requirements

### Requirement: Desk form and membership state remain distinct
The Desk edit drawer MUST protect pending Desk form values without treating an unsubmitted Add member selector as a form change. Discarding pending Desk form values MUST NOT roll back a membership mutation that already persisted.

#### Scenario: Unsubmitted member selection is clean
- GIVEN an administrator opens a Desk edit drawer
- WHEN they change only the Add member selector without submitting it
- THEN the main Desk form MUST remain clean
- AND a close request MUST use the normal close behavior without a confirmation

#### Scenario: Persisted membership survives form discard
- GIVEN an administrator persists a Desk member add or remove while the Desk form has pending tracked values
- WHEN they discard the pending Desk form values
- THEN the drawer MUST close without posting the Desk form
- AND the persisted membership change MUST remain when the Desk is opened again

### Requirement: Desk profile actions remain visually grouped
The Desk edit drawer MUST keep Cancel and Save changes visually grouped with the Desk profile form. The Members section MUST follow with whitespace and its heading, not a horizontal divider between profile actions and Members. The existing Users drawer typography, palette, and button tokens MUST remain unchanged. The Members section MUST state that member changes save immediately.

#### Scenario: Desk actions remain separate from immediate membership changes
- GIVEN an administrator opens the Desk edit drawer at desktop or 390px width
- THEN the profile actions MUST remain visibly associated with the Department, Name, and Description fields
- AND Members MUST follow after whitespace with its heading
- AND the drawer MUST show the text `Member changes are saved immediately.`
- AND member add and remove controls MUST remain available without changing their existing request behavior
