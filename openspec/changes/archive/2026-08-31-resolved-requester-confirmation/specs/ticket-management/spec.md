# Delta for Ticket Management

Scope note: this is the spec-phase delta for change `resolved-requester-confirmation` (issue #55). It names the confirmation-path lifecycle timestamp semantics explicitly, and extends ticket detail presentation with the requester confirmation control and requester-conditional Move-to targets on `resolved` tickets. No canonical file, runtime, template, test, or migration is edited here.

## MODIFIED Requirements

### Requirement: Lifecycle Timestamps

The system MUST set `resolved_at` and `closed_at` only through state machine transitions (see Ticket State Machine). `created_at` and `updated_at` MUST reflect creation and last modification. Requester confirmation and manual agent closure of a requester-less ticket are state machine transitions into `closed` and MUST stamp `closed_at`. A requester rejection returning the ticket to `in_progress` is a reopen from `resolved` and MUST clear `resolved_at`. (Previously: lifecycle timestamps were defined only generically through state machine transitions and did not name the confirmation paths.)

#### Scenario: Timestamps follow transitions only

- GIVEN a ticket resolved then closed via transitions
- WHEN its fields are later edited
- THEN `resolved_at` and `closed_at` remain unchanged

#### Scenario: Confirmation closure stamps closed_at

- GIVEN a `resolved` ticket with `resolved_at` set
- WHEN the requester confirms the resolution
- THEN `closed_at` is set
- AND `resolved_at` remains set

#### Scenario: Rejection clears resolved_at

- GIVEN a `resolved` ticket with `resolved_at` set
- WHEN the requester rejects the resolution
- THEN `resolved_at` is cleared
- AND `closed_at` remains empty

### Requirement: Ticket Detail Presentation

The ticket detail UI MUST present an always-visible `Properties` sidebar with three sections headed `Properties`, `Assignment`, and `State`. Each section MUST be visible on first render without user interaction. The `Properties` section MUST show the ticket's read-only metadata: Requester and Category, plus the read-only timestamps. The `Assignment` section MUST show the current assignee (or Unassigned) and the assignment control when authorized. The `State` section MUST show the current state badge and the Move-to control when a transition exists. The page MUST NOT use native `<details><summary>` cards named Details, Assignment, and State, and MUST NOT store or restore expansion state in `localStorage`. The detail page MUST render the `Properties` heading and MUST NOT render `PROPERTIES` as an all-caps substitute title. On a `resolved` ticket with an identifiable requester, the page MUST additionally present the requester's confirmation control — allowing the requester to confirm or reject the resolution — and MUST present the comment form to the requester; the Move-to control MUST NOT offer `closed` to any actor on such a ticket, and MAY offer the `in_progress` reopen to authorized agents. On a `resolved` ticket with requester user ID NULL, the Move-to control MUST offer `closed` to authorized agents in addition to the reopen. The confirmation control MUST render only for the authenticated requester and MUST NOT render for any other actor. (Previously: the State section offered the same Move-to transitions on every `resolved` ticket regardless of requester, no confirmation control existed, and no comment form appeared on any `resolved` ticket for anyone.)

#### Scenario: Cards default open

- GIVEN an accessible ticket detail page for a non-closed ticket with no saved preference
- WHEN the page renders
- THEN the `Properties`, `Assignment`, and `State` sections are visible without interaction and the response contains no `localStorage` key for ticket-detail collapse
- AND the page does not render Details, Assignment, or State as collapsible `<details>` cards

#### Scenario: Card state survives reload

- GIVEN a ticket detail page rendered for any accessible ticket
- WHEN the page is reloaded
- THEN the `Properties`, `Assignment`, and `State` sections remain visible as on first render without consulting `localStorage`
- AND the response contains no `tkt:ticket-detail:collapsed:v1` script or `localStorage` read/write for expansion state

#### Scenario: Closed ticket renders read-only metadata without mutation controls

- GIVEN a ticket in state `resolved`, `closed`, or `cancelled` viewed by an actor who can read it and who is not that ticket's requester
- WHEN the ticket detail page renders
- THEN the `Properties` section still shows Requester and Category
- AND the `Assignment` section shows the current assignee as read-only text
- AND the `State` section still shows the current state badge
- AND the page does not render a comment form, an inline title edit control, a priority selector, or an assignee selector

#### Scenario: Resolved ticket renders read-only for actors other than the requester

- GIVEN a `resolved` ticket with an identifiable requester viewed by an actor who is not that requester
- WHEN the ticket detail page renders
- THEN the page renders no comment form, no inline title edit control, no priority selector, and no assignee selector
- AND no confirmation control is rendered for that actor
- AND the `State` section offers the reopen transition to authorized agents and no `closed` target

#### Scenario: Resolved ticket renders the confirmation control for the requester

- GIVEN a `resolved` ticket with an identifiable requester
- WHEN the authenticated requester views the ticket detail page
- THEN the page renders the confirmation control with confirm and reject actions
- AND the page renders the comment form for the requester
- AND no Move-to `closed` control is rendered

#### Scenario: Requester-less resolved ticket offers the agent close control

- GIVEN a `resolved` ticket with requester user ID NULL viewed by an authorized agent
- WHEN the ticket detail page renders
- THEN the `State` section offers both the reopen and the `closed` Move-to targets

#### Scenario: Reopen affordance matches the state machine

- GIVEN a ticket in state `closed`
- WHEN the ticket detail page renders for an authorized agent
- THEN the `State` section offers the `Move to` reopen transition requiring a reason
- AND GIVEN a ticket in state `cancelled` the `Move to` control is absent because the state is terminal
