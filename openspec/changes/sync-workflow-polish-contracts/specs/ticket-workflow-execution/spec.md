# Delta for Ticket Workflow Execution

Scope note: this is the spec-phase delta for change `sync-workflow-polish-contracts`. The `openspec/config.yaml` update is a separate in-scope task of the same change and is not produced here. Sync into canonical `openspec/specs/` and archival are later phases; this file writes delta text only and does not edit canonical specs or config.

## MODIFIED Requirements

### Requirement: Form Task Completion and Visibility

A `form[requester]` task MUST accept answers only from the authenticated requester. A `form[assignee]` task MUST accept answers only from the current assignee. Submitted values MUST be validated against the form's pinned field definitions before the task advances. Answers MUST be stored as workflow-task answers rather than comments or audit notes. Completed answers MUST be read-only, and every answer submitted by an assignee MUST be visible to the requester inline within the merged activity timeline under that step's own completion event.

Form answers MUST decode strictly by pinned field position and type. A checkbox answer MUST decode absent or empty as false, a string `on` or `true` as true, and a JSON boolean `true` as true; a JSON boolean `false` is valid and stays false. Any other checkbox value MUST be rejected. A required checkbox MUST accept a decodable false or absent answer, so Required MUST NOT force a checkbox to be true. Text values MUST be trimmed, and blank text on a required field MUST be invalid. A single-select MUST match a pinned option exactly, and a padded or unknown option MUST be rejected. The answer array MUST match the pinned field count and every position; a wrong count, an unknown position, a duplicate position, an ambiguous multi-value position, or extra entries beyond the pinned definition MUST be rejected. At the storage boundary a checkbox MUST decode only from a JSON boolean, and a JSON string such as `"true"` MUST be rejected. Decode errors MUST NOT leak raw persisted values. Answers MUST persist as a typed JSON positional array.
(Previously: the requirement defined actor ownership, pinned validation before advancement, answer storage outside comments/audit notes, and requester visibility, but did not state the strict positional typed decoding matrix.)

#### Scenario: Requester completes supported fields

- GIVEN a requester form with short text, long text, checkbox, and single-select fields
- WHEN the authenticated requester submits values valid for the pinned definitions
- THEN the answers are stored for that task
- AND the run advances once

#### Scenario: Invalid field answer does not advance

- GIVEN a pending form with required and single-select constraints
- WHEN its authorized actor submits invalid values
- THEN plain field validation errors are returned
- AND no answers are committed and the cursor does not advance

#### Scenario: Assignee answer is requester-visible

- GIVEN the current assignee completes an assignee form
- WHEN the requester later reads the ticket detail
- THEN the completed answers are visible as read-only content inline in the merged activity timeline

#### Scenario: Non-actor cannot submit form

- GIVEN a pending requester form
- WHEN an assignee, admin, or root who is not the requester submits answers
- THEN completion is denied
- AND no answers or cursor change are persisted

#### Scenario: Checkbox decodes strictly

- GIVEN a checkbox field pinned in a form
- WHEN an absent answer, an empty string, `on`, `true`, or a JSON boolean `true` is submitted
- THEN the stored checkbox value is false for absent and empty, and true for `on`, `true`, and JSON boolean `true`
- WHEN any other string such as `yes` is submitted
- THEN decoding is rejected

#### Scenario: Required checkbox accepts false or absent

- GIVEN a pinned checkbox field marked Required
- WHEN the answer is absent, empty, or a JSON boolean `false`
- THEN decoding succeeds
- AND the stored value stays false
- AND no true answer is forced

#### Scenario: Strict positional shape is enforced

- GIVEN a pinned form whose answer array is submitted
- WHEN the array has an unknown position, a duplicate position, an ambiguous multi-value position, extra entries beyond the pinned definition, or a wrong total count
- THEN decoding is rejected
- AND no answers are committed

#### Scenario: Single-select matches a pinned option exactly

- GIVEN a single-select field pinned with options such as `eu-west-1` and `us-east-1`
- WHEN the submitted value is a pinned option
- THEN decoding succeeds
- WHEN the submitted value is unknown or carries padding such as ` eu-west-1 `
- THEN decoding is rejected

#### Scenario: Text values are trimmed and required blanks are invalid

- GIVEN a required text field
- WHEN a value such as `  hello  ` is submitted
- THEN the stored value is trimmed to `hello`
- WHEN only whitespace is submitted
- THEN decoding is rejected

#### Scenario: Answers persist as a typed JSON positional array

- GIVEN a valid answer set for a pinned form
- WHEN the task completes
- THEN the answers persist as a typed JSON positional array in pinned field order
- AND a checkbox persists as a JSON boolean, not a string

#### Scenario: Store decodes checkbox strictly and never leaks raw values

- GIVEN persisted answer bytes for a pinned form
- WHEN a checkbox position holds a JSON string such as `"true"` or the answer count differs from the pinned fields
- THEN decoding is rejected
- AND when a single-select value lies outside the pinned options, the decode error does not expose the raw persisted value

### Traceability

| Test | Path | What it proves |
|---|---|---|
| `TestWorkflowRunner_FormDecoding` | `internal/application/workflow_runner_test.go:125` | Runner-side matrix: absent and empty checkbox decode to false and are valid even when Required; `on` and `true` decode to true; `yes` is rejected; text is trimmed and blank required text is invalid; select must match a pinned option exactly and padded values are rejected; unknown, duplicate, ambiguous multi-value, and beyond-pinned positions are rejected; answers persist as a typed JSON positional array with checkbox as JSON boolean |
| `TestDecodeWorkflowResponseFields_StrictPinnedTypes` | `internal/adapters/sqlite/workflow_response_store_test.go:91` | Store-side strict typed decode: wrong answer count rejected; checkbox decodes only from a JSON boolean and a JSON string `"true"` is rejected; a required checkbox with `false` is valid and decodes to Kind checkbox Value `false`; a single-select outside the pinned options is rejected; decode errors do not leak raw persisted values |

### Evidence boundary and gaps

- Decode-error non-leak of raw values is asserted only for the select-outside-options case.
- The runner and store tests are real Go behavior tests, so this domain's decoding matrix has stronger evidence than the markup/CSS-controlled contracts in `category-workflows` and `audit-log`.