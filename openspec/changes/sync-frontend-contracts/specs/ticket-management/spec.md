# Delta for Ticket Management

Scope note: this is the spec-phase delta for change `sync-frontend-contracts` (issue #74). It syncs canonical `openspec/specs/ticket-management/spec.md` to the shipped frontend contract. No canonical file, runtime, template, test, or migration is edited here.

## MODIFIED Requirements

### Requirement: Ticket Detail Presentation

The ticket detail UI MUST present an always-visible `Properties` sidebar with three sections headed `Properties`, `Assignment`, and `State`. Each section MUST be visible on first render without user interaction. The `Properties` section MUST show the ticket's read-only metadata: Requester and Category, plus the read-only timestamps. The `Assignment` section MUST show the current assignee (or Unassigned) and the assignment control when authorized. The `State` section MUST show the current state badge and the Move-to control when a transition exists. The page MUST NOT use native `<details><summary>` cards named Details, Assignment, and State, and MUST NOT store or restore expansion state in `localStorage`. The detail page MUST render the `Properties` heading and MUST NOT render `PROPERTIES` as an all-caps substitute title.
(Previously: the UI presented compact native `<details><summary>` cards named Details, Assignment, and State, expanded by default with expansion state stored in localStorage and restored after reload — the permanently open Properties sidebar and its controls already existed as the desired prior state.)

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

- GIVEN a ticket in state `resolved`, `closed`, or `cancelled` viewed by an actor who can read it
- WHEN the ticket detail page renders
- THEN the `Properties` section still shows Requester and Category
- AND the `Assignment` section shows the current assignee as read-only text
- AND the `State` section still shows the current state badge
- AND the page does not render a comment form, an inline title edit control, a priority selector, or an assignee selector

#### Scenario: Reopen affordance matches the state machine

- GIVEN a closed ticket in state `resolved` or `closed`
- WHEN the ticket detail page renders
- THEN the `State` section offers the `Move to` transition control
- AND GIVEN a ticket in state `cancelled` the `Move to` control is absent because the state is terminal

## Notes

Traceability for `Ticket Detail Presentation`:

| Evidence | Path | What it proves |
|---|---|---|
| Always-visible sidebar structure | `web/templates/partials/ticket_detail.html:1` | `cards` layout with `evidence` sidebar carrying three `prop-section` blocks headed `Properties`, `Assignment`, `State` |
| No `<details>` / no `localStorage` | `web/templates/partials/ticket_detail.html` (full file search) | No `<details>` element and no `tkt:ticket-detail:collapsed:v1` key |
| Read-only closed rendering | `web/templates/partials/ticket_detail.html:13` + golden `internal/adapters/http/golden_test.go:TestClosedTicketDetailReadOnly` | Closed branch renders read-only assignee and hides title/priority/assignee/comment controls |
| Contract negatively asserts stale cards and storage | `internal/adapters/http/golden_test.go:TestTicketDetailPresentationContract` | Asserts absence of `<details open id="details"` and `tkt:ticket-detail:collapsed:v1`, presence of `prop-heading` sections |
