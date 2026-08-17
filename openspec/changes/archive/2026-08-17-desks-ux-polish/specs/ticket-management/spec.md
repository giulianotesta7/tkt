# Delta for Ticket Management

## MODIFIED Requirements

### Requirement: Ticket Detail Presentation

The ticket detail UI MUST present compact native `<details><summary>` cards named Details, Assignment, and State, expanded by default. Expansion state MUST be stored in localStorage and restored after reload. Requester and timestamps remain read-only metadata.
(Previously: a permanently open Properties sidebar contained the fields and state controls.)

#### Scenario: Cards default open
- GIVEN an accessible ticket detail page with no saved preference
- WHEN the page renders
- THEN Details, Assignment, and State are expanded and “PROPERTIES” is absent

#### Scenario: Card state survives reload
- GIVEN a user collapses the Assignment card
- WHEN the page is reloaded
- THEN Assignment remains collapsed using the saved localStorage state
