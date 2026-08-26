# Category Workflows Specification

## Purpose

Defines the minimal admin-managed, linear, versioned workflow attached to a category, including draft publication, validation, builder behavior, and migration boundaries.

## Requirements

### Requirement: Draft and Publish Lifecycle

The system MUST create an empty editable draft lazily when an authorized actor first begins workflow configuration for a category. Editing a draft MUST NOT replace or hide the currently published version. Publishing MUST validate the complete draft and MUST create a new immutable version containing at least one step; publishing identical content MUST still create a new version. The new version MUST become current atomically only after successful validation. The system MUST NOT support unpublishing, a historical-version browser, or a requester-facing workflow-version pin.

#### Scenario: First configuration creates a lazy draft

- GIVEN a category with no workflow definition or version
- WHEN an authorized actor first opens its workflow builder
- THEN an empty editable draft is available
- AND no published version is created

#### Scenario: Published version remains active during edits

- GIVEN a category with published version 3
- WHEN an authorized actor changes its draft
- THEN version 3 remains active for new tickets until another publish succeeds

#### Scenario: Publish creates an immutable version

- GIVEN a valid non-empty draft and published version 3
- WHEN an authorized actor publishes the draft
- THEN immutable version 4 is created and becomes current atomically
- AND later draft edits do not alter version 4

#### Scenario: Empty draft cannot be published

- GIVEN an empty draft
- WHEN an authorized actor attempts to publish it
- THEN publication is rejected with a plain validation error
- AND no version is created

### Requirement: Closed Linear Step Model

A workflow version MUST be a contiguous ordered list containing only `assign_to_desk`, `form`, `manual_task`, `resolve_ticket`, and `close_ticket` steps. Step positions MUST be unique and gap-free. The system MUST NOT permit branching, parallel steps, loops, approvals, conditional routing, or extensible step types. A workflow MAY contain at most one terminal step. `resolve_ticket` and `close_ticket` MUST be mutually exclusive, MUST be the final step when present, and MUST NOT have any following step.

#### Scenario: Valid non-terminal linear workflow

- GIVEN an ordered workflow containing a requester form, desk assignment, and manual task
- WHEN the definition is validated
- THEN the workflow is accepted without requiring a terminal step

#### Scenario: Unknown step type is rejected

- GIVEN a draft containing a step type outside the closed five-type set
- WHEN the draft is validated
- THEN validation is rejected with a plain error identifying the step

#### Scenario: Terminal ordering is enforced

- GIVEN a draft with a step after `resolve_ticket`
- WHEN the draft is validated
- THEN validation is rejected with a plain error identifying the terminal-order violation

#### Scenario: Terminal types are mutually exclusive

- GIVEN a draft containing both `resolve_ticket` and `close_ticket`
- WHEN the draft is validated
- THEN validation is rejected
- AND no version is created

#### Scenario: Gapped ordering is rejected

- GIVEN a draft whose ordered positions are duplicated or contain a gap
- WHEN the draft is validated
- THEN validation is rejected with a plain ordering error

### Requirement: Step Configuration Validation

An `assign_to_desk` step MUST identify an existing desk and MUST use strategy `claim` or `least_loaded`. A `form` step MUST identify actor `requester` or `assignee` and MAY contain only `short_text`, `long_text`, `checkbox`, and `single_select` fields. Every form field key and label MUST be non-empty, and field keys MUST be unique within the workflow. A `single_select` field MUST have at least two non-empty options that are unique within that field. The builder transport grammar MUST use literal semicolons (`;`) as the only option delimiter, trim surrounding Unicode whitespace from each token, ignore empty tokens, preserve order and duplicates during transport parsing, and treat commas as ordinary label characters; semicolons inside labels are unsupported and no quoting or escaping grammar exists. The persisted/runtime representation MUST remain canonical `[]string`, and exact runtime answer equality MUST remain unchanged. Invalid contextual configuration MUST prevent publication and MUST produce plain, step-specific errors.

The Required control MUST be offered only for text and single-select kinds and MUST be hidden for a checkbox field. A persisted `required=true` on a checkbox field MUST normalize to non-required (false) on the round trip. Changing a required text field to Checkbox MUST clear its Required flag. A checkbox field MUST behave as boolean and MUST NOT carry a required value in the persisted definition.
(Previously: the requirement defined field kinds, key/label constraints, single-select options, the transport grammar, and exact answer equality, but did not state the checkbox Required semantics.)

#### Scenario: Invalid desk configuration is rejected

- GIVEN step 2 is an `assign_to_desk` step with a missing or unknown desk or strategy
- WHEN the draft is published
- THEN no version is created
- AND a plain error identifies step 2 and the invalid desk configuration

#### Scenario: Duplicate field key is rejected

- GIVEN two form fields in the workflow use the same key
- WHEN the draft is published
- THEN no version is created
- AND a plain error identifies the duplicate key

#### Scenario: Invalid single-select options are rejected

- GIVEN a `single_select` field has fewer than two non-empty unique options
- WHEN the draft is published
- THEN no version is created
- AND a plain error identifies the affected step and field

#### Scenario: Single-select transport preserves comma labels

- GIVEN the builder input is `North; South; Buenos Aires, Argentina`
- WHEN the submitted options are parsed
- THEN the canonical options are `[]string{"North", "South", "Buenos Aires, Argentina"}` in the same order
- AND commas remain part of the third option rather than acting as delimiters

#### Scenario: Unknown form actor is rejected

- GIVEN a form step whose actor is neither `requester` nor `assignee`
- WHEN the draft is published
- THEN no version is created
- AND a plain error identifies the affected step

#### Scenario: Checkbox Required is available only for compatible kinds

- GIVEN a form step with a text field, a checkbox field, and a single-select field
- WHEN the builder renders the form editor
- THEN the text and single-select fields expose a Required control
- AND the checkbox field exposes no Required control
- AND the checkbox field's persisted Required value is false

#### Scenario: Changing a required text field to Checkbox clears Required

- GIVEN a form where a required text field is switched to Checkbox with its Required flag still set
- WHEN the draft is saved
- THEN the persisted field becomes Kind checkbox with Required false

### Requirement: Horizontal master-detail workflow builder

The workflow builder MUST use the Users page layout primitives and present a horizontal, non-wrapping step rail with a selected-step master-detail editor below it. The rail MUST show compact cards with each step's handle, position, type, dynamic summary, menu, and a neutral `Final` marker for the final step. The domain has no stable step identifier, so the HTTP layer MUST accept the optional presentation-only position `selected_step_index` and MUST NOT treat it as stable identity. Selecting a card MUST update presentation state and MUST NOT autosave or mutate the draft. The detail editor MUST render the existing form and validation for the selected step type. The existing POST endpoint and action set MUST remain unchanged. The HTTP layer MUST accept optional `selected_step_index` and `add_step_type` inputs, MUST validate them, and MUST preserve prior behavior when either is absent. Neither value may enter session state, process memory, domain/application models, persistence, or migrations. The workflow header MUST expose only the Saved state and Publish action. This screen MUST expose no Preview action or Flow preview UI, while the existing backend preview route and action remain intact.

#### Scenario: Selected step opens its existing editor

- GIVEN an authorized actor edits a draft with an existing `form` step
- WHEN the actor selects that step card
- THEN the presentation-only `selected_step_index` updates to that position without representing stable identity
- AND the detail editor shows the existing form-specific fields and controls for that step
- AND no draft field, version, or persistence record changes because of selection alone

#### Scenario: Selection does not autosave

- GIVEN an authorized actor edits a draft with at least two steps
- WHEN the actor selects a different step without submitting a field or form action
- THEN the server receives no autosave mutation for the selection
- AND the optional selected index remains available for the next builder action through presentation-only query or form data

#### Scenario: Rail scrolls horizontally at a narrow width

- GIVEN an authorized actor opens a draft with enough steps to exceed 390px
- WHEN the builder renders in a 390px viewport
- THEN the step rail remains horizontal and non-wrapping
- AND the rail provides horizontal overflow access to every step card
- AND the surrounding page does not require horizontal scrolling to reach the detail editor or page actions

#### Scenario: Typed Add step popover uses allowed types

- GIVEN an authorized actor edits a draft
- WHEN the actor opens Add step
- THEN an anchored popover presents the existing allowed step types `Assign to desk`, `Form`, `Manual task`, `Resolve ticket`, and `Close ticket`
- AND each choice identifies the type that the actor will add
- AND choosing a type submits the existing `add_step` action with an optional `add_step_type` value
- AND the HTTP layer validates that value against the existing closed step-type set
- AND omitting `add_step_type` preserves the existing default manual-step behavior

#### Scenario: Typed Add step protects terminal order

- GIVEN a draft already contains a terminal step
- WHEN an authorized actor opens Add step
- THEN the popover hides terminal choices that the existing rules disallow
- AND the builder offers no insertion position after the terminal step
- AND the terminal step remains final and mutually exclusive under the existing rules

#### Scenario: Removing the selected step chooses a safe neighbor

- GIVEN an authorized actor edits a draft with at least two steps
- AND the selected step has a neighboring step
- WHEN the actor removes the selected step through the existing remove action
- THEN the selected step is removed through the existing request and persistence contract
- AND the HTTP layer recalculates `selected_step_index` to a safe adjacent remaining step
- AND the detail editor shows that newly selected step without showing removed-step state

#### Scenario: Removing the only step clears selection

- GIVEN an authorized actor edits a draft with exactly one selected step
- WHEN the actor removes that step
- THEN the existing remove rules determine whether the draft may be empty
- AND if the draft is empty, presentation-only selection clears
- AND the builder shows its empty state without a stale detail editor
- AND if the existing rules reject the removal, the builder shows the existing validation error and preserves the selected step

#### Scenario: Horizontal reorder retains the moved selection

- GIVEN an authorized actor edits a draft with at least two steps
- AND one step is selected
- WHEN the actor drags that step horizontally to a valid position
- THEN the rail shows a placeholder at the source position and a blue insertion indicator at the target position
- AND the existing reorder request applies the new order
- AND the HTTP layer recalculates `selected_step_index` to the moved step's destination after the reorder
- AND the final step and terminal constraints remain valid

#### Scenario: Terminal step cannot move from the final position

- GIVEN a draft contains a terminal step at the end
- WHEN an authorized actor attempts to move that terminal step before another step or place another step after it
- THEN the builder rejects the invalid reorder through the existing validation and request rules
- AND the terminal step remains final
- AND no invalid order is persisted

#### Scenario: Keyboard actions provide reorder and remove fallbacks

- GIVEN an authorized actor focuses a step card or its action menu
- WHEN the actor invokes Move left, Move right, or Remove with the keyboard
- THEN the corresponding existing reorder or remove request runs without requiring drag input
- AND visible focus remains on a usable builder control
- AND a successful move recalculates the selected index to the moved step's destination
- AND a successful removal selects a safe adjacent step or clears selection when no step remains

#### Scenario: Preview access is removed from this screen

- GIVEN an authorized actor opens the category workflow builder
- WHEN the page renders
- THEN the header contains the Saved state and Publish action only
- AND the page contains no Preview button, Open preview action, or Flow preview UI
- AND the existing backend preview route and action remain available to their existing callers
- AND this requirement introduces no endpoint, action, domain/application model, persistence contract, migration, session state, process-memory state, or dependency change

#### Scenario: Full-page validation fallback

- GIVEN HTMX is unavailable and the draft contains an invalid step
- WHEN an authorized actor submits Publish
- THEN the full builder page renders with the same inline validation error
- AND the horizontal rail preserves the submitted step order and optional presentation-only selected index when that value is valid
- AND the selected step editor preserves the submitted values needed to correct the error
- AND no version is created
- AND the page still exposes only the Saved state and Publish action, with no Preview or Flow preview UI

### Requirement: Builder Step and Field Menu Presentation

Step and field action menus in the workflow builder MUST expose the menu presentation contracts below through one reusable trigger style. This requirement covers only menu presentation polish. The horizontal master-detail layout, drag reordering, and keyboard reorder/remove fallbacks stay specified by `Horizontal master-detail workflow builder` and are not restated here.

- The shared trigger MUST be a 32x32 hit area with a centered `⋯` glyph, no border at rest, a gray hover background, and an accent focus ring.
- A step card trigger MUST render upper-right with the accessible name `Step actions`. A field-row trigger MUST render in the fixed actions column, `grid-column:4` at desktop width, with the accessible name `Field actions`.
- The accessible name `Actions for step` MUST NOT be used.
- Pressing Escape inside an open step or field menu MUST close the menu and return focus to its trigger.
- An open menu MUST position so its content does not clip at the rail or viewport edge.
- An immutable terminal step MUST render no trigger and no menu, and a terminal-only draft MUST render zero step menus and zero triggers.
- Field rows MUST remain stable and responsive: at a 390px viewport they MUST stack as `1fr 44px` rows without horizontal overflow, with the field trigger reachable.
- The menu presentation MUST NOT remove the existing keyboard reorder/remove fallbacks or drag reorder from `Horizontal master-detail workflow builder`; those remain the accessible alternatives to touch-drag.

#### Scenario: Shared trigger polish applies to every step and field menu

- GIVEN a draft with two steps and a selected form step carrying one field
- WHEN the builder renders
- THEN each step card and the field row expose the shared `workflow-trigger` (three instances in total)
- AND each shows the centered `⋯` glyph
- AND the shared style declares a 32x32 hit area, no border at rest, a gray hover background, and an accent focus ring

#### Scenario: Step and field triggers carry their exact accessible names

- GIVEN a draft with two step cards and a selected form field
- WHEN the builder renders
- THEN each step card trigger has the accessible name `Step actions`
- AND the field-row trigger has the accessible name `Field actions`
- AND the accessible name `Actions for step` appears nowhere

#### Scenario: Escape closes the open menu and refocuses its trigger

- GIVEN a step or field menu is open
- WHEN Escape is pressed
- THEN the menu closes to `details.open = false`
- AND focus returns to the trigger via `summary.focus()`
- AND other keys are left unhandled

#### Scenario: Open menu does not clip at the rail or viewport edge

- GIVEN a step menu is open near the rail or viewport edge
- WHEN the page renders the open menu
- THEN the menu content is fully visible and not clipped

#### Scenario: Immutable terminal step exposes no trigger or menu

- GIVEN a draft whose only step is a terminal step such as `close_ticket`
- WHEN the builder renders
- THEN the page renders zero step menus and zero triggers
- AND the terminal step stays in its final position with no action menu

#### Scenario: Field rows stay stable and responsive at 390px

- GIVEN a selected form step with field rows at a 390px viewport
- WHEN the builder renders
- THEN each field row stacks as `1fr 44px` without horizontal overflow
- AND the field trigger remains reachable in the row

#### Scenario: Menus keep accessible alternatives to drag

- GIVEN a step or field card that exposes a menu
- WHEN an actor operates it without touch-drag
- THEN the existing keyboard reorder and remove fallbacks from `Horizontal master-detail workflow builder` stay available
- AND the menu does not become the only way to reorder or remove a step or field

### Requirement: Additive Workflow Adoption

Workflow adoption MUST be additive. Existing categories MUST NOT receive generated drafts or published versions through backfill, and the system MUST NOT create a production compatibility workflow for them. Existing tickets with no pinned workflow version MUST remain readable. A category without a published workflow MUST become available only through explicit configuration and publication.

#### Scenario: Existing category is not backfilled

- GIVEN a category that predates category workflows
- WHEN workflow storage is introduced
- THEN no draft or published version is generated for that category
- AND the category is unavailable for new tickets until explicitly published

#### Scenario: Legacy unpinned ticket remains readable

- GIVEN an existing ticket whose workflow-version pin is NULL
- WHEN an authorized actor opens its detail page
- THEN the ticket remains readable
- AND no compatibility workflow or run is required

### Requirement: Workflow Data Search Boundary

Workflow form answers MUST belong to workflow tasks and MUST remain outside ticket comments and audit notes. Form answers MUST NOT be added to the ticket full-text-search corpus, and introducing workflows MUST NOT require reindexing existing tickets.

#### Scenario: Answer text is excluded from search

- GIVEN a completed workflow form whose answer contains a unique term absent from the ticket's searchable fields
- WHEN an actor searches tickets for that term
- THEN the ticket does not match because of the workflow answer

### Requirement: Native Workflow Configurator Select Presentation

Workflow-configurator step selects MUST remain native semantic `<select>` controls with their existing names, values, HTMX/autosave behavior, and keyboard operation. Their presentation MUST use existing tkt visual tokens, retain clearly visible high-contrast focus, and fit narrow layouts without horizontal overflow. The system MUST NOT replace them with a scripted custom combobox or alter their server-side mutation contract.

#### Scenario: Configurator select remains operable

- GIVEN an admin or root edits a workflow step
- WHEN they operate its native type, desk, actor, strategy, or field-kind select by keyboard
- THEN the selected value participates in the existing HTMX/autosave flow
- AND visible focus remains clear at desktop and 390px widths
- AND no custom control changes the select's native semantics
