# Ticket Management Specification Delta

## ADDED Requirements

### Requirement: Selected-category creation presentation

When a catalog category is selected, the create-ticket page MUST use the heading `Create a ticket` with the subtitle `Describe your request.`. The form MUST show a compact `Category` route containing Department, Desk, and Category, plus one `Change` link to `/tickets/new`. It MUST NOT render a `TICKET DETAILS` heading or a duplicate catalog return control. The catalog page MUST retain `Choose a category to get started.`.

#### Scenario: Selected category form

- GIVEN an actor selects a catalog category
- WHEN the create-ticket form renders
- THEN it shows the full selected Department / Desk / Category route and one Change link to `/tickets/new`
- AND it preserves the existing Title, Description, Priority, native validation, submitted values, and POST behavior

### Requirement: Selected-category creation detail layout

When a catalog category is selected, the create-ticket page MUST use the existing ticket-detail content distribution: its `minmax(0, 1fr)` main column, 370px Properties rail, 16px gap, container width, background, spacing, and responsive stack breakpoint. It MUST NOT use a separately capped 800px form card or an additional boxed Properties preview. The creation page MUST reuse the detail presentation structure and classes without changing the created-ticket detail template or its behavior.

The native create form MUST put the `Title` entry in the detail-style header area. Its editable `Description` MUST occupy the detail main-column card width, retain vertical resizing, and be followed by a `Timeline` card containing the honest `No activity yet.` empty state. It MUST NOT render a comment composer or `Add comment` section, fictional ticket metadata, State, or Move to controls. The primary `Create ticket` action MUST remain part of the one native POST form and visually belong to the main creation content.

The unboxed Properties rail MUST show the authenticated requester name, the selected Department / Desk / Category route and one `Change` link to `/tickets/new`, and one editable native `Priority` select styled like the detail priority control. Changing Priority locally MUST send no request and cause no navigation, autosave, storage write, or HTMX action. The Assignment / Assignee value MUST read `Not assigned yet` as pre-submission information only. It MUST NOT render or submit requester, `user_id`, or `assignee_id` controls. It MUST NOT render a State section. Server-rendered initial and 422 values MUST preserve the valid selected priority; an invalid posted priority MUST fall back to the select's actual selected option.

#### Scenario: Responsive selected form in creation mode

- GIVEN an actor selects a category with a long Department, Desk, or Category name
- WHEN the form renders at 1536px or 1280px
- THEN the Description main-column left edge and width, Properties rail position and unboxed treatment, and column gap match an actual ticket detail at the same viewport
- AND the Timeline appears below Description with `No activity yet.`
- AND long requester and category values wrap without horizontal overflow
- WHEN the form renders at 390px wide
- THEN the main creation content precedes the Properties rail in the detail-style single-column stack
- AND the Title, Description, Priority, Change, and Create ticket controls remain available

#### Scenario: Native priority and validation retain creation values

- GIVEN the selected category form is open
- WHEN the actor changes Priority from Medium to High
- THEN the right-rail Priority control shows `High`
- AND the browser sends no request and does not navigate
- WHEN the actor posts an invalid title or priority
- THEN the server re-renders the selected route, submitted values, priority selection, and title focus with 422
- AND no assignment or requester control appears

### Requirement: Catalog hierarchy ink

The ticket catalog breadcrumb and search-result hierarchy text MUST use the existing neutral strong-ink token. Item links, selected-row treatment, borders, Change link, and focus behavior remain unchanged.

#### Scenario: Catalog hierarchy stays neutral

- GIVEN the catalog displays a selected Department / Desk breadcrumb or a search result
- WHEN the hierarchy text renders
- THEN its computed color equals the existing neutral strong-ink token
