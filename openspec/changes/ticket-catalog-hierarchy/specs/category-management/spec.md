# Delta for Category Management

## ADDED Requirements

### Requirement: Fixed-depth ticket catalog
The catalog MUST contain exactly Departments → Desks → Categories. Departments and Desks are organizational groupings; Categories remain ticket classifications and workflow owners. The final model, schema, routes, handlers, stores, templates, and user-facing copy MUST contain no Area behavior. Department names MUST be globally unique, Desk names MUST preserve the existing global Desk uniqueness constraint, and category-name constraints MUST remain unchanged. Departments, Desks, and Categories MUST persist names, supported descriptions, and parent relationships in SQLite. Categories MUST persist only `desk_id`; workflow ownership MUST remain category-owned.

#### Scenario: Browse the catalog
- GIVEN a requester opens `/tickets/new`
- THEN the catalog shows Departments, Desks, and Categories
- AND selecting a Department and Desk shows available published categories with title, short description, and chevron
- AND no Area terminology or CRUD is presented

#### Scenario: Search with hierarchy context
- GIVEN a category, Desk, or Department matches a search query
- WHEN the requester searches the catalog
- THEN matching categories show their Department and Desk context
- AND selecting a match opens the existing ticket form with that category selected

### Requirement: Ticket creation remains unchanged after selection
The initial ticket creation journey MUST show the catalog before the existing ticket form. Selecting a published category MUST continue to the existing form with the category selected, show the complete Department / Desk path, and provide a return action that changes the category. Title, description, priority, validation, published workflow association, ticket creation behavior, and the absence of Department or Desk snapshots on tickets MUST remain unchanged.

#### Scenario: Create a ticket from the catalog
- GIVEN a published category is selected from its Desk
- WHEN the requester submits the existing form
- THEN the ticket is created with that category and its published workflow version
- AND the ticket stores no Department or Desk snapshot

### Requirement: Catalog administration is unified
Actors with the existing category-management capability MUST be able to create, edit, move, describe, and safely delete Departments, Desks, and Categories in one `/categories` administration view. The view MUST have no tabs, no independent category table, and no second Desk frontend. It MUST present three desktop columns and progressive mobile drill-down. The three column headers MUST always show enabled `New department`, `New desk`, and `New category` actions. Creation and editing MUST use dedicated right-side drawer routes with a usable full-page fallback.

Desk descriptions MUST be optional, persisted end-to-end, and displayed in the Desk list and New/Edit Desk drawers. Category descriptions MUST be optional and persist exactly as submitted, including an empty value, on both category creation and editing. New Desk creation MUST require a real Department. Editing a legacy Desk without a Department MUST require assigning a real Department before saving. Desk member management MUST remain available in the Desk drawer, preserve existing agent-plus membership rules, and preserve members and workflow/routing associations.

#### Scenario: Preserve hierarchy integrity
- GIVEN a Desk contains a category or a Department contains a Desk
- WHEN an administrator attempts to delete that parent
- THEN deletion is rejected and category, workflow, ticket, Desk member, and routing associations remain intact

#### Scenario: Change category ownership in its drawer
- GIVEN an authorized actor opens a Category drawer
- WHEN the actor changes Department
- THEN the Desk options are filtered to that Department before submission
- AND the category is saved only with a valid Desk

#### Scenario: Preserve Desk membership
- GIVEN an authorized administrator opens a Desk drawer
- WHEN they add or remove an eligible Desk member
- THEN the existing membership routes persist the operation and refresh the selected hierarchy context
- AND the unified Desk drawer remains visible without main-frame navigation or URL changes
- AND an HTMX response targets `#category-drawer-host` with `outerHTML` at status 200
- AND a `user` role is rejected as a Desk member

#### Scenario: Accessible administration controls
- GIVEN an authorized actor navigates the administration view with a keyboard
- WHEN they activate a row overflow menu
- THEN its actions are announced with accessible names and remain keyboard-operable
- AND the UI shows no numeric record identifiers or duplicate Selected department/desk labels

### Requirement: Legacy Desks use a virtual Unassigned group
Legacy Desks whose persisted `department_id` is NULL MUST remain visible and selectable in `/categories` under a virtual `Unassigned` group. The virtual group MUST NOT create or persist an artificial Department row. Legacy Desk members, categories, workflow ownership, and routing MUST remain available. The legacy Desk edit drawer MUST display its description and require assignment to a real Department before saving. New Desk forms MUST not offer Unassigned as a Department.

#### Scenario: Select and repair a legacy Desk
- GIVEN a legacy Desk has a NULL `department_id`
- WHEN an administrator selects `Unassigned` and opens that Desk
- THEN the Desk and its categories are shown, members remain manageable, and the Desk drawer has no selected real Department
- WHEN the administrator saves the Desk
- THEN a real Department is required and the same Desk ID and members are preserved

#### Scenario: Reload a legacy drawer route
- GIVEN a legacy Desk with a Category is selected under the virtual `Unassigned` group
- WHEN an administrator opens that Desk or Category edit drawer through HTMX
- THEN the pushed URL uses the literal `department_id=unassigned`
- WHEN the administrator reloads the pushed URL
- THEN the same drawer and entity render without an invalid identifier error

### Requirement: Catalog drawer validation identifies the invalid control
Catalog drawer validation responses MUST project typed domain errors to the control named by the error, without parsing error message text. A `ValidationError` for `name`, `department_id`, or `desk_id` MUST mark the matching control invalid when that control exists in the current Department, Desk, or Category drawer. A `DuplicateError` for a Department, Desk, or Category MUST mark that drawer's name control invalid. Category `department_id` is presentation-only and MUST NOT mark the Category Department control invalid. Descriptions MUST NOT be marked invalid.

#### Scenario: Retry an invalid drawer submission
- GIVEN an administrator submits a Department, Desk, or Category drawer with a typed validation or duplicate error
- WHEN the server re-renders the drawer
- THEN the response preserves submitted values and marks only the matching current control with `aria-invalid="true"`
- AND an HTMX response retains its error status and drawer swap headers
- AND a later fresh drawer response has no stale invalid marker

### Requirement: Compatibility routes
`GET /desks` MUST be redirect-only compatibility to `/categories` for authorized actors. It MUST render no Desk index and MUST not be a second administration UI. Only the existing backend Desk member mutation routes required by the unified Desk drawer may remain under `/desks`; no compatibility route may bypass the Department requirement for Desk creation or editing.

#### Scenario: Use the legacy Desk URL
- GIVEN an authorized actor requests `GET /desks`
- THEN the server redirects to `/categories`
- AND no Desk index template is rendered
- WHEN the unified Desk drawer submits a member mutation
- THEN the existing `/desks/{id}/members` compatibility route persists it without changing the Desk's Department requirement

### Requirement: Responsive administration
At desktop widths the three hierarchy levels MUST be visible together and lists MUST remain independently usable. On initial desktop entry at `/categories` or `/categories?view=structure`, before a Department is selected, the Desks column MUST list every Desk from all real Departments and the virtual `Unassigned` group. Each Desk link MUST preserve its Department context in its navigation route. Desk rows MUST show only the Desk name, optional description, and fixed overflow action; they MUST NOT render a visible Department label or category-count text or element. Categories MUST remain instruction-only until a Desk is selected. After a Department is selected, the Desks column MUST show only that Department's Desks, including only its legacy Desks when the virtual `Unassigned` group is selected. At mobile widths the view MUST show one level at a time with back navigation and preserved Department / Desk context; mobile entry MUST continue to show Departments first and MAY defer Desks until a Department is selected. Rows MUST remain compact and aligned without document-level horizontal overflow. Desktop back links and redundant selected-context labels MUST not be rendered; when no level is selected, the view MUST show only the relevant selection instruction.

#### Scenario: Show all Desks on desktop entry
- GIVEN an authorized actor opens `/categories` at a desktop width without Department or Desk query parameters
- THEN the Desks column shows Desks from real Departments and the virtual `Unassigned` group with Department context in each link
- AND the Categories column shows only the instruction to select a Desk
- WHEN the actor selects a Department
- THEN the Desks column is filtered to that Department

#### Scenario: Use the administration view on mobile
- GIVEN an actor opens `/categories` at a narrow viewport
- THEN only the current hierarchy level is visible
- WHEN the actor selects a Department and then a Desk
- THEN the next level appears with a back action and the selected context is preserved
- AND the document has no horizontal overflow

### Requirement: Compatibility migration
A forward-only unpublished migration MUST reuse the existing Desk table, add optional Desk descriptions, create Departments, add nullable `department_id` to Desks for legacy rows, add category descriptions and Desk ownership, and place existing categories beneath a deterministic Department / Desk compatibility branch while preserving category IDs, ticket references, Desk IDs, Desk membership, workflow versions, and published associations. It MUST be idempotent, MUST NOT create an Area table or migration, MUST NOT generate or rewrite workflows, and MUST NOT add ticket Desk snapshots. Existing legacy Desks with NULL `department_id` MUST remain valid and appear under `Unassigned`.

#### Scenario: Migrate an existing installation
- GIVEN an installation with existing categories, tickets, published workflows, Desks, and Desk members
- WHEN the corrected migration runs
- THEN existing IDs and associations remain unchanged
- AND existing legacy Desks remain under virtual `Unassigned`
- AND rerunning migrations makes no additional changes

## MODIFIED Requirements

### Requirement: Responsive Category Management Index

The managed category index MUST be the unified `/categories` administration view for the Department → Desk → Category hierarchy. It MUST have no independent category table, no second Desk frontend, and no tabs. At desktop widths, Departments, Desks, and Categories MUST appear in three compact, aligned columns. Each column header MUST provide its enabled `New department`, `New desk`, or `New category` action. At mobile widths, the view MUST show one hierarchy level at a time, preserve the selected Department and Desk context, and provide Back navigation without document-level horizontal overflow.

Department, Desk, and Category row actions MUST use accessible overflow menus. Their controls and actions MUST have accessible names, remain keyboard-operable, and preserve visible focus. Category deletion MUST remain a native submit to the existing category-delete POST route. The server MUST remain authoritative for authorization and deletion outcomes. A rejected category deletion MUST re-render its inline feedback in the administration surface. At 390px wide, hierarchy content and available actions MUST remain discoverable without horizontal scrolling. Presentation MUST preserve existing tkt palette, typography, spacing, focus treatment, and the simple user/admin philosophy; screenshot references may inform structure only.

#### Scenario: Manage the unified hierarchy

- GIVEN an authorized actor opens `/categories` at a desktop width
- THEN Departments, Desks, and Categories are shown in three aligned columns
- AND each column header provides its enabled creation action
- AND no independent category table, second Desk frontend, or tabs are rendered
- WHEN the actor selects a Department and then a Desk
- THEN the Categories column shows the selected Desk's categories

#### Scenario: Direct category delete remains server-authoritative

- GIVEN an admin or root views a deletable category in the unified hierarchy
- WHEN they open the category overflow menu with a keyboard and activate `Delete category`
- THEN the existing category-delete POST route handles the request
- AND existing server-side authorization remains authoritative
- AND no client-side mutation authority is required

#### Scenario: Rejected direct delete remains inline

- GIVEN an authorized actor submits `Delete category` from a category overflow menu for a category the server rejects for deletion
- WHEN the existing POST route re-renders the management surface
- THEN the rejection appears inline in that surface
- AND the category overflow action remains available according to the existing authorization and state rules

#### Scenario: Narrow category index remains actionable

- GIVEN an admin or root views the unified hierarchy at 390px wide
- WHEN categories include an action that can delete a category
- THEN the hierarchy content and available actions remain discoverable without horizontal scrolling
- AND the overflow control and its actions are keyboard reachable with visible focus
