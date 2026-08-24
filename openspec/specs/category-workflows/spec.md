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

### Requirement: Friendly Vertical Builder

The workflow builder MUST present a top header with its existing actions and a responsive two-panel information architecture: the semantic vertical numbered-list editor on the left and a read-only vertical linear-flow preview on the right at desktop widths. The editor MUST show only fields relevant to the selected step type. An authorized actor MUST be able to add, move, remove, preview, and publish steps. The read-only preview MUST be derived from the submitted/server-rendered current draft, and MAY use restrained static visual connectors solely to clarify sequence. It MUST NOT become a graph, canvas, interactive node editor, branching control, drag-and-drop surface, client-side state store, or new workflow semantic. At narrow widths the panels MUST stack without horizontal overflow. Reordering MUST be keyboard accessible, and every builder action and validation result MUST remain usable through a full-page request when HTMX is unavailable; HTMX and full-page responses MUST preserve equivalent editor and preview state. Validation errors MUST appear inline in plain language.

#### Scenario: Contextual step fields

- GIVEN an authorized actor is editing a draft
- WHEN they select `form` as a step type
- THEN only form-specific actor and field controls are shown for that step

#### Scenario: Keyboard reordering

- GIVEN a draft with at least two steps
- WHEN a keyboard user moves the second step upward
- THEN the numbered order updates
- AND focus remains usable for the next builder action

#### Scenario: Preview does not publish

- GIVEN an edited draft and an older active published version
- WHEN an authorized actor previews the draft
- THEN a read-only ordered summary of the draft is shown
- AND the older published version remains active

#### Scenario: Read-only linear flow preview mirrors the draft

- GIVEN an authorized actor edits a linear draft with multiple ordered steps
- WHEN the builder renders at desktop width
- THEN the semantic ordered editor appears beside a read-only vertical flow preview
- AND the preview reflects the same submitted/server-rendered linear order
- AND the preview exposes no drag-and-drop, branching, node editing, or independent mutation control

#### Scenario: Builder stacks without overflow on narrow screens

- GIVEN an authorized actor opens the builder at 390px wide
- WHEN the editor and read-only preview render
- THEN the panels stack in a readable order without horizontal scrolling
- AND native controls, add/reorder/remove, preview, publish, visible focus, and live announcements remain usable

#### Scenario: Full-page validation fallback

- GIVEN HTMX is unavailable and the draft contains an invalid step
- WHEN an authorized actor submits Publish
- THEN the full builder page is rendered with the same inline validation error
- AND the editor and read-only preview preserve the submitted draft state
- AND no version is created

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
