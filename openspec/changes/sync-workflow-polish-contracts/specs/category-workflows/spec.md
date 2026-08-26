# Delta for Category Workflows

Scope note: this is the spec-phase delta for change `sync-workflow-polish-contracts`. The `openspec/config.yaml` update is a separate in-scope task of the same change and is not produced here. Sync into canonical `openspec/specs/` and archival are later phases; this file writes delta text only and does not edit canonical specs or config.

## ADDED Requirements

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

## MODIFIED Requirements

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

### Traceability

| Test | Path | What it proves |
|---|---|---|
| `TestCategoryWorkflowBuilder_ThreeDotTriggerPolish` | `internal/adapters/http/handlers_category_workflows_test.go:1647` | Shared `workflow-trigger` style (32x32, centered `⋯`, no border at rest, gray hover, accent focus ring); exact `Step actions` and `Field actions` names and legacy name absent; upper-right step trigger, field trigger `grid-column:4`; Escape close-and-refocus strings in `workflow.js`; terminal-only draft renders zero triggers and menus |
| `TestCategoryWorkflowBuilder_CheckboxRequiredSemantics` | `internal/adapters/http/handlers_category_workflows_test.go:2031` | Required control present for text/select and hidden for checkbox; persisted checkbox `required=true` normalizes to false; changing a required text field to Checkbox clears Required |

### Evidence boundary and gaps

- The trigger hit area, hover, focus ring, positioning, and terminal rules are asserted as CSS and markup source text (`cssRuleDeclares`, string counts), not rendered geometry.
- Escape close-and-refocus is pinned only by static substring assertions on `web/templates/static/workflow.js`; no behavioral test executes the asset, and the repo has no versioned browser suite.
- The open-menu no-clipping rule has no direct assertion: `.workflow-rail-wrap.menu-open` and the `.up` flip rules exist in `styles.html` but no test references them. The scenario records the intent; verification records the gap.
- Responsive field-row stacking (`1fr 44px` at 390px) is asserted by the supplemental test `TestCategoryWorkflowBuilder_MobileStyles_WrapNarrow`, not among the five named tests; the five-test matrix covers only the `grid-column:4` placement half.
- Reverse checkbox Required normalization (for example single-select to checkbox) is untested; only text-to-checkbox is covered.
