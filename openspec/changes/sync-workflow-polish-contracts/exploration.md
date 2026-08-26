# Exploration: sync-workflow-polish-contracts

## Answer first

This PR is documentation-only recovery plus one in-scope config update. Four canonical specs carry stale contract text that main's tests already prove, and `openspec/config.yaml` describes a pre-implementation project instead of the implemented one. The PR updates the four specs (`category-workflows`, `ticket-workflow-execution`, `audit-log`, `ticket-management`) with the missing contract text, and updates `openspec/config.yaml` to the current facts. No runtime, template, CSS, JS, test, golden, migration, skill, CI, dependency, or coverage change. No edits to archived change folders. No inspection or modification of the PR #64 worktree.

## Context and base facts

| Fact | Value |
|---|---|
| Change | `sync-workflow-polish-contracts` (PR0, docs + config only) |
| Worktree | `/home/gtesta/Projects/tkt-worktrees/pr0-openspec-workflow-polish-sync` |
| Base | origin/main `f6c35081f22d24fdfc4013a6860dff6ce0492afd` |
| In-scope files | four canonical specs below + `openspec/config.yaml` |
| Out-of-scope | runtime code, templates, CSS, JS, tests, goldens, migrations, skills, CI/CD, dependencies, coverage; all of `openspec/changes/archive/`; the PR #64 worktree |
| Historical reference | `openspec/changes/archive/2026-08-23-category-workflows/**` (read-only, for baseline content only) |

## openspec/config.yaml: in-scope delta

`openspec/config.yaml` is an explicit deliverable of this PR. The current file was written for a pre-implementation project and contradicts the implemented tree on four facts, so it must be rewritten to describe what exists now, nothing more. No future policies: do not anticipate Staticcheck, new pipelines, or any plan beyond the current state.

| Area | Current text (stale) | Required fact |
|---|---|---|
| Go version | `Go 1.25.11` | Keep. `go.mod` declares `go 1.25.11` |
| Web stack | `net/http`; `HTMX + html/template` (SSR, no JS framework) | Keep. Verified in `internal/adapters/http` and `web/templates` |
| JavaScript | absent | JavaScript limited to user/workflow interactions. The only asset is `web/templates/static/workflow.js` (drag reorder, menu Escape handling); nothing else ships JS |
| SQLite | `driver choice: modernc.org/sqlite vs mattn/go-sqlite3 — decided during design` | SQLite via `modernc.org/sqlite`. `go.mod` pins `modernc.org/sqlite v1.56.0`. The design-time driver question is closed; the `design` rule "Decide SQLite driver with rationale" must be dropped or replaced |
| Architecture | `not yet defined (no code). Domain layout established during design phase` | Layers: `domain`, `application`, `adapters/http`, `adapters/sqlite`. Verified: all four packages exist under `internal/` |
| Docker | `Docker multi-stage delivery` | multi-stage Docker with Distroless and non-root user. `Dockerfile` builds in `golang:1.25` with `CGO_ENABLED=0`, ships `gcr.io/distroless/static-debian12:nonroot`, runs as UID 65532, `/data` chowned to 65532 |
| Testing (layers) | unit stdlib; integration `httptest` + in-memory SQLite; e2e none ("no browser tooling detected; golden-file assertions planned for HTML") | SQL migrations, golden tests, and domain/application/HTTP/persistence tests all exist and must be named: migrations live in `internal/adapters/sqlite` (`migrate.go`, migration_0003..0009), goldens in `internal/adapters/http/testdata/*.golden` via `golden_test.go` (`-update` flag), test suites in `domain`, `application`, `adapters/http`, and `adapters/sqlite` |
| e2e | `none (no browser tooling detected...)` | Playwright MCP as local visual validation, with no versioned Playwright suite. The repo has no browser test pipeline or Playwright dependency; do not add one |
| Quality | formatter `gofmt`; linter `go vet` (staticcheck/golangci-lint not installed) | Keep as current facts. No Staticcheck, no new pipelines |
| Verify | `coverage_threshold: 0` | Keep `coverage_threshold: 0` |
| strict_tdd / schema | `schema: spec-driven`; `strict_tdd: true` | Keep |

The stale "decided during design" SQLite rule affects both the context block and the `rules.design` entry. Both must say `modernc.org/sqlite` is the driver, or the rule must be removed.

## Canonical spec inventory

All fifteen canonical specs exist under `openspec/specs/`. Seven carry YAML frontmatter: `user-management`, `ticket-state-machine`, `ticket-search`, `comment-timeline`, `audit-log`, `ticket-management`, `category-management`. Eight start directly with the H1: `category-workflows`, `ticket-workflow-execution`, `desk-management`, `role-authorization`, `comment-visibility`, `auth-entry-experience`, `role-specific-views`, `ticket-access-assignment`.

The mainline specs already carry the synced workflow additions (`Atomic Workflow Audit Sets`, `Step-Indexed Semantic Audit Events`, `Merged Ticket Activity Timeline`, `Contextual Workflow Claim Assignment Event` in audit-log; `Workflow-Based Category Availability` + `Responsive Category Management Index` in category-management; `Responsive Desk Master/Detail Index` + `Existing Desk Operations Remain Authoritative` in desk-management; `Workflow Definition Authorization` + `Workflow Task Actor Authorization` + `Pinned Claim Visibility and Transactional Recheck` in role-authorization; `Pending Workflow Presentation` + `Current Task Card Presentation` + `Claim Assignment Sidebar` in ticket-management). Nothing is absent. The list below is exactly what is stale: implemented, test-proven contract with no canonical requirement text.

## Requirement-to-test matrix (the five named tests)

The five tests named by the user are exact and confirmed at these locations:

| Test | File:line |
|---|---|
| `TestCategoryWorkflowBuilder_ThreeDotTriggerPolish` | `internal/adapters/http/handlers_category_workflows_test.go:1647` |
| `TestCategoryWorkflowBuilder_CheckboxRequiredSemantics` | `internal/adapters/http/handlers_category_workflows_test.go:2031` |
| `TestTimelineRendersCheckboxBooleanGlyphs` | `internal/adapters/http/handlers_category_workflows_test.go:2079` |
| `TestWorkflowRunner_FormDecoding` | `internal/application/workflow_runner_test.go:125` |
| `TestDecodeWorkflowResponseFields_StrictPinnedTypes` | `internal/adapters/sqlite/workflow_response_store_test.go:91` |

| # | Test | Contract the test pins | Recovery target |
|---|---|---|---|
| 1 | `TestCategoryWorkflowBuilder_ThreeDotTriggerPolish` | Shared trigger polish: one reusable `.workflow-trigger` style, exactly 32x32, centered `⋯` glyph, no border at rest, gray hover, accent focus ring; exact accessible names `Step actions` per step card and `Field actions` per field row (legacy `Actions for step` forbidden); step trigger upper-right, field trigger fixed in the actions column (`grid-column:4`); Escape closes the open menu and returns focus to its trigger (`event.key !== "Escape"`, `details.open = false`, `summary.focus()` in `workflow.js`); an immutable terminal step exposes no trigger and no menu, so a terminal-only draft renders zero step menus and zero triggers | `category-workflows`: recommendation is a new requirement (proposal decides identity, e.g. `Builder Step and Field Menu Presentation`) so `Horizontal master-detail workflow builder` stays readable; the Escape and terminal rules live in the same requirement |
| 2 | `TestCategoryWorkflowBuilder_CheckboxRequiredSemantics` | A checkbox field is boolean; the Required control stays available for text/select, is hidden for checkbox; a persisted `required=true` on a checkbox normalizes to non-required on the round trip; changing a required text field to Checkbox clears Required | `category-workflows`: extend `Step Configuration Validation` (field rules live there) with a MUST and a scenario |
| 3 | `TestTimelineRendersCheckboxBooleanGlyphs` | Submitted checkbox values render `✓` for true and `×` for false inside `role="img"` spans with `aria-label="Yes"` / `aria-label="No"`; every other field kind keeps its literal value; meaning never relies on color alone | `audit-log`: extend `Merged Ticket Activity Timeline` with a MUST and a scenario (requirement already covers the inline `dl`/`dt`/`dd` values and 390px behavior) |
| 4 | `TestWorkflowRunner_FormDecoding` | Strict positional form-decoding matrix at the runner: absent checkbox stores `false` and is valid even when Required; empty-string answer for a required checkbox is valid; `on`, `true`, and JSON boolean `true` decode to true; any other string (e.g. `yes`) is invalid; text values are trimmed; blank text on a required field is invalid; single-select must match a pinned option exactly and a padded value is rejected; the answer array shape is strict (unknown position, duplicate position, ambiguous multi-value position, and extra entries beyond the pinned definition all rejected); answers persist as a typed JSON positional array | `ticket-workflow-execution`: extend `Form Task Completion and Visibility` with a MUST on strict typed positional decoding plus the matrix scenarios |
| 5 | `TestDecodeWorkflowResponseFields_StrictPinnedTypes` | Strict typed decode at the store: wrong answer count is rejected; a checkbox must decode from a JSON boolean, a string `"true"` is rejected; a required checkbox with `false` is valid and decodes to `Kind checkbox, Value "false"`; a single-select outside the pinned options is rejected; decode errors do not leak raw persisted values | `ticket-workflow-execution` (decode half of the same MUST as #4) plus the ticket-management Required-compatibility MUST below (#6) |

Contract list is exactly the user's four: `category-workflows`, `ticket-workflow-execution`, `audit-log`, `ticket-management`. No new contracts beyond what the five tests pin.

| # | Contract | Recovery target |
|---|---|---|
| 6 | Required compatibility on the pending form: a pinned checkbox may carry `Required: true`; the pending control renders the native `required` attribute, but a decodable false or absent answer is accepted at the command layer and stays `false`; checkbox Required never forces a true answer | `ticket-management`: extend `Current Task Card Presentation` (pending form scenario). Proven by `TestDecodeWorkflowResponseFields_StrictPinnedTypes` (required checkbox false valid, stays false) and corroborated by the runner matrix (#4) and the builder normalization (#2) |

## Supplemental evidence

The tests below are not part of the five named tests. They corroborate adjacent contracts that are already on main or that the proposal may fold in or leave out. None of them adds a MUST on its own.

| Test (file:line) | What it shows |
|---|---|
| `TestCategoryWorkflowBuilder_MasterDetailSelectionPresentation` (`:129`) | Horizontal master-detail already canonical: selection does not autosave/persist, POST submitter with distinct index |
| `TestAmendment4_BuilderRendersMasterDetailWithoutPreviewUI` (`handlers_amendment4_test.go:179`) | Master-detail layout classes and no Preview UI, already on main |
| `TestCategoryWorkflowBuilder_PolishFormAndFinalEditors` (`:1603`) | Fields header, right-aligned `+ Add field`, compact rows (Label, Kind, Required, field menu), `Remove field`, single-select full-width Options row, final Resolve/Close editor read-only. Candidate polish content, not in the five |
| `TestCategoryWorkflowBuilder_DragResponsiveCSS` (`:2005`) | Rail `overflow-x:auto` containment, drag indicator inert/hidden until drag, grip compaction under 640px. Candidate, not in the five |
| `TestCategoryWorkflowBuilder_MobileStyles_WrapNarrow` (`:1225`) | 390px no-overflow contract; field rows stack `1fr 44px`; headers wrap. Candidate, not in the five |
| `TestCategoryWorkflowBuilder_DragReorder` (`:1833`), `DragMarkup` (`:1947`), `MenuLabelsHorizontal` (`:1560`), `KeyboardActionFallbacks` (`:1757`) | Drag/persist/menu-fallback contracts, already on main |
| `TestAmendment4_CurrentTaskFormRetainsRequiredNativeControls` (`handlers_amendment4_test.go:100`) | Pending form keeps native `required` controls including checkbox; corroborates #6 |

Implementation anchors for the spec/design/tasks phases (read-only reference, no edits):

- `web/templates/partials/workflow_builder.html` (drag handle line 17, step trigger line 19, field row line 62)
- `web/templates/partials/styles.html` (`.workflow-trigger` 32x32 at line 321, `.workflow-field-actions` mobile `grid-column:2;grid-row:3` at line 398, `.workflow-bool` classes at lines 437-439)
- `web/templates/static/workflow.js` (drag indicator, Escape close-and-refocus at lines 133-142)
- `web/templates/partials/timeline.html:19` and `web/templates/partials/workflow_answers.html:10` (bool glyph spans)
- `internal/adapters/sqlite/workflow_response_store.go:156` (`decodeWorkflowResponseFields`)
- `internal/application/workflow_runner.go` (positional decode used by the runner)

## Coverage gaps

No tests are added in this PR. These gaps are recorded so spec/design/tasks and verification know the evidence boundary:

1. Escape close-and-refocus is pinned only as static substring assertions on `workflow.js` source. No behavioral test executes the asset. The repo has no versioned browser suite; `openspec/config.yaml` will record Playwright MCP as local visual validation only.
2. Drag interaction (dragstart/dragover/drop) is not behaviorally tested. Go tests POST the `reorder` action directly and assert markup, CSS text, and JS source strings.
3. The 32x32 hit area, hover, and focus-ring rules are asserted as CSS source text (`cssRuleDeclares`), not rendered geometry.
4. Open-menu clipping at rail edges has no assertion: `.workflow-rail-wrap.menu-open` and the `.up` flip rules exist in `styles.html` but no test references them. "No clipping" is only proven for the closed rail and the drag indicator.
5. The "independent of color" half of the glyph contract is only partially proven: `TestTimelineRendersCheckboxBooleanGlyphs` asserts `role="img"`/`aria-label`/glyph characters and literal values for other kinds, but not the `workflow-bool-true`/`workflow-bool-false` classes (they exist at `styles.html:437-439`) or any color-agnostic contrast rule.
6. Reverse conversions for checkbox Required normalization are untested (e.g. single-select to checkbox); only text-to-checkbox is covered.
7. Decode-error non-leak of raw values is asserted only for the select-outside-options case.
8. Missing YAML frontmatter in eight canonical specs is a hygiene gap, not behavior. Adding frontmatter to the four touched specs is a candidate task for the proposal to approve; fixing the other four is out of scope unless widened.

## CI: pre-existing failure boundary

`.github/workflows/ci.yml` runs on `ubuntu-latest` with four gates: `gofmt -l .`, `go vet ./...`, `go build ./...`, `go test ./... -race -count=1`. None of the four gates compiles or tests OpenSpec markdown, so this PR cannot turn CI red on its own. Any red at base `f6c35081` is inherited from origin/main and out of scope, and the proposal/verify phases should treat it as pre-existing rather than a regression of this change. Two base conditions worth noting, neither fixed here: `go.mod` declares the patch-level directive `go 1.25.11`, resolved by setup-go through `go-version-file` (a toolchain availability dependency), and the race suite is the long gate (a ~327s run is recorded in the archived tasks). This phase has no shell tool, so the gates were not executed here; the verify phase can re-check and label any base failure as inherited.

## Out-of-scope boundaries

- No change to any file under `openspec/changes/archive/` (including `2026-08-23-category-workflows`); those serve as read-only historical reference.
- No change to the PR #64 worktree, and no inspection of it.
- No runtime code, templates, CSS, JS, test, golden, migration, skill, CI, dependency, or coverage change.
- No new tests; the gaps above are recorded, not fixed.
- No CI fix; a base failure is pre-existing by definition.
- The only in-scope files are the four canonical specs plus `openspec/config.yaml` (and optional frontmatter additions if the proposal approves them).

## Risks

| Risk | Note |
|---|---|
| Config delta must stay factual | The rewritten `config.yaml` must describe only current facts (modernc driver, four layers, Distroless non-root, local Playwright MCP). Any anticipated Staticcheck, pipeline, or future policy is out of scope by instruction |
| Supplemental polish items could expand the diff | Field rows, final editor, drag indicator containment, and responsive stacks are proven only by non-core tests. The proposal should scope them explicitly, in or out, so the PR stays within the 400-line budget (config + four specs are well under) |
| Stale design rule left dangling | The `design` rule "Decide SQLite driver with rationale" must be replaced when the driver fact lands; leaving it contradicts the config cleanup |
| Spec drift between archived and current `category-workflows` | The archive still carries `Friendly Vertical Builder` (no drag, preview UI present); canonical replaced it with `Horizontal master-detail workflow builder`. Recoveries attach to canonical text only |
| Frontmatter inconsistency | Normalizing only the four touched specs leaves the tree inconsistent unless the proposal widens the choice to all eight |
| Evidence boundary is static assertions | Spec MUSTs for the trigger and glyph contracts rest on CSS/JS text assertions. Verification should treat those assertions as the evidence boundary, not a browser suite |

## Next step

`sdd-propose` should (1) approve the `config.yaml` delta as in-scope, (2) approve the recovery matrix built on the five exact tests, (3) decide the new requirement identity for the trigger/menu polish versus folded scenarios, (4) decide whether supplemental-proven polish items enter scope, and (5) rule on the frontmatter question. Then `sdd-spec` writes the four spec deltas and `sdd-design` confirms the driver-decision rule replacement.

## Checklist for downstream phases

- [ ] Approve the `config.yaml` delta table (this file, "in-scope delta" section)
- [ ] Use the five exact tests as the requirement-to-test matrix, in the order above
- [ ] Decide new requirement identity for trigger/menu polish (recommended: new requirement in `category-workflows`)
- [ ] Add checkbox-Required normalization to `Step Configuration Validation`
- [ ] Add strict typed positional decoding matrix to `Form Task Completion and Visibility`
- [ ] Add Yes/No glyph MUST and scenario to `Merged Ticket Activity Timeline`
- [ ] Add Required-compatibility/checkbox-false-valid to `Current Task Card Presentation`
- [ ] Decide supplemental-proven candidates (field rows, final editor, drag indicator containment, responsive stacks): in or out
- [ ] Optional: frontmatter on the four touched specs
- [ ] Keep the change docs+config only; do not touch runtime, tests, goldens, migrations, CI, archives, or PR #64