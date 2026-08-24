---
name: category-management
status: proposed
change: tkt-mvp
---

# Category Management Specification

## Purpose

Defines the managed category list referenced by tickets. Category names are unique.

## Requirements

### Requirement: Create Category

The system MUST allow roles `admin` and `root` to create a category from a name. The name MUST be non-empty and MUST be unique across categories. Duplicate names MUST be rejected. Roles `user` and `agent` MUST NOT create categories. (Previously: the actor restriction was unspecified; category management was reachable by any logged-in user.)

#### Scenario: Create category

- GIVEN a non-empty name that is not in use
- WHEN an `admin` or `root` creates the category
- THEN the category is stored and listed

#### Scenario: Reject duplicate name

- GIVEN an existing category named "Bugs"
- WHEN an `admin` creates another category named "Bugs"
- THEN the creation is rejected with a uniqueness error

#### Scenario: Non-manager denied

- GIVEN a `user`- or `agent`-role actor
- WHEN they attempt to create a category
- THEN the request is denied
### Requirement: Update Category

The system MUST allow roles `admin` and `root` to rename a category. The uniqueness constraint MUST apply to the new name.

#### Scenario: Rename category

- GIVEN an existing category "Bugs"
- WHEN an `admin` renames it to "Defects"
- THEN the category is stored with the new name and "Bugs" is free for future use

#### Scenario: Reject rename to duplicate

- GIVEN categories "Bugs" and "Support"
- WHEN an `admin` renames "Support" to "Bugs"
- THEN the rename is rejected with a uniqueness error
### Requirement: Delete Category

The system MUST NOT delete a category that is referenced by at least one ticket. The system MUST allow roles `admin` and `root` to delete an unreferenced category.

#### Scenario: Reject deletion of referenced category

- GIVEN a category used by an existing ticket
- WHEN an `admin` attempts to delete it
- THEN the deletion is rejected with an integrity error

#### Scenario: Delete unreferenced category

- GIVEN a category with no tickets
- WHEN an `admin` deletes it
- THEN the category is removed from the list

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
