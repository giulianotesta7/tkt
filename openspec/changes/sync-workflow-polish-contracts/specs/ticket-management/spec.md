# Delta for Ticket Management

Scope note: this is the spec-phase delta for change `sync-workflow-polish-contracts`. The `openspec/config.yaml` update is a separate in-scope task of the same change and is not produced here. Sync into canonical `openspec/specs/` and archival are later phases; this file writes delta text only and does not edit canonical specs or config.

## MODIFIED Requirements

### Requirement: Current Task Card Presentation

The CURRENT pending form and manual task in the ticket activity area MUST use the supplied `Current task` card structure while preserving existing server-rendered behavior. The card background MUST use exactly `color-mix(in srgb,var(--amber-soft) 30%,var(--card))`, and its contour MUST include exactly `border-top:2px solid var(--amber)`. The card MUST retain its existing server-rendered behavior without arbitrary or unpaired color input. Pending forms MUST retain their labels, required semantics, native text fields/selects/checkboxes, validation rendering, and existing submit behavior. Pending manual tasks MUST retain pinned instructions, the optional solution field, and existing completion behavior. The card MUST preserve keyboard focus and responsive usability. Completed historical events MUST remain in the existing merged timeline with their current ordering and semantics unless a narrow wrapper is needed solely for visual coherence. GET rendering remains read-only and all mutations remain on the existing POST completion route.

Required MUST apply only to compatible field types, the text and single-select kinds. A pinned checkbox MAY carry a legacy Required flag or render the native `required` control, but a true and a false answer MUST both remain valid and decodable, and a false or absent answer MUST stay false; checkbox Required MUST NOT force a true answer.
(Previously: the requirement specified the card palette, native form control retention, manual task retention, keyboard and responsive usability, and the read-only GET / POST mutation boundary, but did not state the Required-compatibility rule for checkboxes.)

#### Scenario: Pending form retains native semantics inside the current-task card

- GIVEN an authorized actor views a ticket with a current pending form task
- WHEN the ticket activity area renders
- THEN the form appears in the `Current task` card using background `color-mix(in srgb,var(--amber-soft) 30%,var(--card))`
- AND the card contour includes exactly `border-top:2px solid var(--amber)`
- AND its labels, required semantics, native fields, selects, checkboxes, validation rendering, and submit behavior remain unchanged
- AND the card remains keyboard usable without horizontal overflow at 390px wide

#### Scenario: Pending manual task retains pinned completion behavior inside the current-task card

- GIVEN the current assignee views a ticket with a pending manual task
- WHEN the ticket activity area renders
- THEN pinned instructions and the optional solution field appear in the `Current task` card using background `color-mix(in srgb,var(--amber-soft) 30%,var(--card))`
- AND the card contour includes exactly `border-top:2px solid var(--amber)`
- AND the existing completion route and authorization remain authoritative
- AND no arbitrary or unpaired color input can alter the card palette

#### Scenario: Historical activity remains merged and ordered

- GIVEN a ticket has completed form, manual-task, comment, assignment, and transition events
- WHEN the current task card is introduced for a pending task
- THEN completed historical events remain in the merged timeline with their existing ordering and semantics
- AND the presentation introduces no new client-side mutation authority

#### Scenario: Pending checkbox accepts false or absent regardless of Required

- GIVEN a pending form with a pinned checkbox that carries `Required: true` and renders the native `required` control
- WHEN the authorized actor submits an absent or a false answer
- THEN the command layer accepts the completion and the stored checkbox value stays false
- AND no true answer is forced, and the run advances once

### Traceability

| Test | Path | What it proves |
|---|---|---|
| `TestDecodeWorkflowResponseFields_StrictPinnedTypes` | `internal/adapters/sqlite/workflow_response_store_test.go:91` | A pinned checkbox with `Required: true` and a `false` answer is valid and decodes to Kind checkbox Value `false`; checkbox Required never forces a true answer |

Cross-requirement corroboration, no separate MUST: the Required-compatibility contract is corroborated by the runner matrix in `TestWorkflowRunner_FormDecoding` (absent/empty checkbox valid even when Required) and the builder normalization in `TestCategoryWorkflowBuilder_CheckboxRequiredSemantics`. `TestAmendment4_CurrentTaskFormRetainsRequiredNativeControls` further corroborates the native-`required` rendering.