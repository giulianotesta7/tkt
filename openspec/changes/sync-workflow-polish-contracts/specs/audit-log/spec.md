# Delta for Audit Log

Scope note: this is the spec-phase delta for change `sync-workflow-polish-contracts`. The `openspec/config.yaml` update is a separate in-scope task of the same change and is not produced here. Sync into canonical `openspec/specs/` and archival are later phases; this file writes delta text only and does not edit canonical specs or config.

## MODIFIED Requirements

### Requirement: Merged Ticket Activity Timeline

The ticket detail MUST present ONE merged activity timeline, newest-first, containing comments, assignments, every completed category-flow step, and state transitions; a separate ticket-facing responses card for completed steps MUST NOT be rendered. A form completion timeline item MUST render its pinned submitted field labels and values inline as a definition list inside that item, using the immutable pinned labels joined by the persisted step index; answer values remain stored only in `ticket_form_answers` and MUST NOT be duplicated into audit notes or full-text search. A manual completion timeline item MUST render its contextual pinned instruction from the immutable pinned version at the persisted step index, and MUST additionally render the assignee's submitted solution only when that solution is non-empty; solutions remain stored only with their workflow task records and MUST NOT be duplicated into audit note/reason fields or full-text search. Completed form results MUST use the approved restrained treatment consistent with tkt: semantic `dl`/`dt`/`dd` markup with clear per-field grouping separated by hairlines, fixed-width muted labels whose long values wrap inside the entry instead of overflowing it, single-column stacking at narrow viewports such as 390px with no horizontal overflow at any width, plainly visible keyboard focus states and sufficient contrast, HTML escaping of every label and value as plain text, and no technical `workflow` wording anywhere ticket-facing. Human events MUST keep their attributed actor names while automatic events omit actor text, and all existing behavior MUST be preserved: exact timestamps, newest-first ordering, internal-comment visibility, and comments-before-events ordering on same-second ties.

A submitted checkbox value MUST render inside the inline definition list as `✓` for true and `×` for false, each inside a `role="img"` span with the accessible name `Yes` or `No` respectively. Every other field kind MUST keep its literal submitted value. The meaning of the rendered value MUST NOT rely on color alone.
(Previously: the requirement specified the merged timeline, inline definition list rendering, pinned instruction and solution rendering, restrained styling, escaping, and ordering, but did not state the checkbox boolean glyph rendering.)

#### Scenario: Merged timeline replaces the separate responses card

- GIVEN a ticket has comments, an assignment, a completed form step, and a resolved transition
- WHEN the detail page renders
- THEN one newest-first timeline contains all of them
- AND no standalone responses section appears

#### Scenario: Form answers render inline under their own event

- GIVEN a completed form step with pinned fields `Server name` (short text) and checkbox `Urgent`
- WHEN its timeline item renders
- THEN the item shows a definition list pairing those immutable labels with the submitted escaped values
- AND the values appear nowhere in audit notes or search documents

#### Scenario: Manual completion shows its pinned instruction

- GIVEN a completed manual step whose pinned definition carries instructions
- WHEN its timeline item renders
- THEN the item shows that instruction text verbatim and escaped

#### Scenario: Automatic events show no actor text

- GIVEN the timeline contains an automatic resolve transition
- WHEN the item renders
- THEN no actor label or copy containing `workflow` is shown
- AND human-authored items in the same timeline still show their actor names

#### Scenario: Same-second ties keep comments before events

- GIVEN a comment and a step-completion audit share an identical timestamp
- WHEN the timeline orders them
- THEN the comment appears before the event
- AND overall order remains newest-first

#### Scenario: Manual item omits an absent solution

- GIVEN a completed manual task whose completion carried no non-empty solution
- WHEN its timeline item renders
- THEN the item shows the pinned instruction with its actor and timestamp
- AND no empty or placeholder solution block is rendered

#### Scenario: Non-empty manual solution renders escaped and attributed

- GIVEN a completed manual task whose non-empty solution contains markup-like text
- WHEN its timeline item renders for any viewer authorized to see the ticket
- THEN the item shows the pinned instruction followed by the solution as escaped plain text
- AND the item keeps the completing actor's name and the event timestamp
- AND no submitted markup executes or changes page structure

#### Scenario: Manual solution stays out of audit notes and search indexes

- GIVEN a completed manual task whose solution contains a distinctive marker string
- WHEN the persisted audit row and full-text search documents are inspected
- THEN the marker appears in neither the audit note nor reason fields nor any indexed document
- AND the solution exists only in the workflow task record tied to the persisted step index

#### Scenario: Completed form results stay readable at 390px

- GIVEN a completed form step with long submitted values viewed at a 390px-wide viewport
- WHEN its timeline item renders
- THEN each label/value pair stacks in a single column without horizontal overflow
- AND long values wrap inside the entry while labels stay clearly grouped
- AND interactive elements show plainly visible keyboard focus states with sufficient contrast
- AND all rendered labels and values are escaped plain text

#### Scenario: Checkbox boolean values render with accessible glyphs

- GIVEN a completed form whose submitted values include a checkbox answered true and a checkbox answered false
- WHEN its timeline item renders
- THEN the true value renders `✓` inside a `role="img"` span with the accessible name `Yes`
- AND the false value renders `×` inside a `role="img"` span with the accessible name `No`
- AND no submitted markup executes or changes page structure

#### Scenario: Non-checkbox values keep their literal text

- GIVEN a completed form whose submitted values include a short-text field
- WHEN its timeline item renders
- THEN the short-text value renders verbatim as plain escaped text
- AND neither the `✓` nor the `×` glyph replaces it

### Traceability

| Test | Path | What it proves |
|---|---|---|
| `TestTimelineRendersCheckboxBooleanGlyphs` | `internal/adapters/http/handlers_category_workflows_test.go:2079` | Submitted checkbox values render `✓` for true and `×` for false inside `role="img"` spans with `aria-label="Yes"` / `aria-label="No"`; every other field kind keeps its literal value |

### Evidence boundary and gaps

- The color-independence half of the glyph contract is only partially proven. `TestTimelineRendersCheckboxBooleanGlyphs` asserts `role="img"`, `aria-label`, the glyph characters, and literal values for other kinds, but not the `workflow-bool-true`/`workflow-bool-false` classes or any color-agnostic contrast rule. The scenario states the intent; verification records the class/contrast half as unasserted.