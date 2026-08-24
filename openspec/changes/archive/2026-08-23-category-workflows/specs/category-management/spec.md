# Delta for Category Management

## ADDED Requirements

### Requirement: Workflow-Based Category Availability

The system MUST make a category available for new tickets only while it has a published workflow version. Ticket-creation category choices MUST exclude categories without a published version for every role. The managed category list MUST show `Published vN` when the current draft matches the published definition and `Draft` when an editable draft differs from the active published definition. A category with no workflow definition MUST have no workflow badge and MUST remain unavailable for new tickets.

#### Scenario: Unconfigured category is unavailable

- GIVEN a category with no workflow definition or published version
- WHEN any authenticated actor opens the new-ticket form
- THEN that category is absent from the category choices

#### Scenario: Published category is available

- GIVEN a category with a current published workflow version
- WHEN any authenticated actor opens the new-ticket form
- THEN that category is available as a category choice

#### Scenario: Draft edits retain published availability

- GIVEN a category with published version 2 and a draft that differs from version 2
- WHEN an `admin` or `root` views the managed category list
- THEN the category is marked `Draft`
- AND it remains available for new tickets through published version 2

#### Scenario: Category with no workflow has no badge

- GIVEN an existing category for which workflow configuration has never begun
- WHEN an `admin` or `root` views the managed category list
- THEN the category has no workflow badge
- AND it remains unavailable for new tickets

### Requirement: Responsive Category Management Index

The managed categories index MUST use a semantic table with `Category`, `Created`, `Status`, and `Actions` headers. The destructive category action MUST be a directly visible native submit button labelled exactly `Delete category`; it MUST use the existing POST route and server-side authorization, and it MUST NOT be hidden behind `More actions`, an overflow/disclosure, or a replacement client-side authority mechanism. Rejected deletes MUST retain their existing inline errors. At narrow widths the same information and actions MUST stack without horizontal overflow, and the visible button MUST remain keyboard reachable with visible focus. Presentation MUST preserve existing tkt palette, typography, spacing, focus treatment, and the simple user/admin philosophy; screenshot references may inform structure only.

#### Scenario: Direct category delete remains server-authoritative

- GIVEN an admin or root views a deletable category
- WHEN they activate the visible `Delete category` submit button
- THEN the existing category-delete POST route handles the request
- AND existing server-side authorization remains authoritative
- AND no `More actions` disclosure or client-side mutation authority is required

#### Scenario: Rejected direct delete remains inline

- GIVEN an authorized actor submits `Delete category` for a category the server rejects for deletion
- WHEN the existing POST route re-renders the management surface
- THEN the rejection appears inline in that surface
- AND the visible `Delete category` control remains available according to the existing authorization and state rules

#### Scenario: Narrow category index remains actionable

- GIVEN an admin or root views the managed categories index at 390px wide
- WHEN categories include an action that can delete a category
- THEN category, created time, status, and actions remain discoverable without horizontal scrolling
- AND the visible `Delete category` control is keyboard reachable with visible focus
