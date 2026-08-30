---
change: sync-workflow-polish-contracts
phase: design
status: completed
artifact_store: openspec
execution_mode: automatic
delivery_strategy: single-pr
---

# Design: Sync Workflow Polish Contracts

## Decision summary

This change is a docs-and-config recovery with no runtime footprint. Five files change: `openspec/config.yaml` and the four canonical specs `category-workflows`, `ticket-workflow-execution`, `audit-log`, `ticket-management`. The four approved deltas under `openspec/changes/sync-workflow-polish-contracts/specs/` are the complete and only source of canonical text changes. Nothing outside the five files is modified, the PR is a single commit well under the 400-line budget, and the change directory is archived after validation.

Key decisions:

| Decision | Choice |
|---|---|
| Canonical change set | Exactly the four approved deltas applied to exactly the four canonical specs, plus the config rewrite |
| New requirement identity | `Builder Step and Field Menu Presentation` (ADDED, `category-workflows`), placed after `Horizontal master-detail workflow builder` |
| Modified requirements | `Step Configuration Validation`; `Form Task Completion and Visibility`; `Merged Ticket Activity Timeline`; `Current Task Card Presentation` |
| Master-detail contract | Preserved byte-identical; not duplicated. The new requirement only cross-references it |
| Delta apparatus | Scope notes, traceability tables, and evidence-boundary sections never enter canonical text; they stay in this change folder |
| "(Previously: ...)" annotations | Retained in the synced requirement text, matching the existing canonical convention in `audit-log` and `ticket-management` |
| Frontmatter normalization | Out of scope; no spec gains or loses YAML frontmatter |
| Supplemental polish contracts | Out of scope; nothing beyond the four deltas enters canonical text |
| Stale SQLite design rule | Replaced with a decided-fact rule in `rules.design` |
| Runtime evidence | None claimed: no Go test, build, or Playwright run is part of this PR |

## Scope and boundaries

| Path | Action |
|---|---|
| `openspec/config.yaml` | Rewritten factual portions only (deliverable 1) |
| `openspec/specs/category-workflows/spec.md` | ADDED requirement + MODIFIED requirement (deliverable 2) |
| `openspec/specs/ticket-workflow-execution/spec.md` | MODIFIED requirement (deliverable 2) |
| `openspec/specs/audit-log/spec.md` | MODIFIED requirement (deliverable 2) |
| `openspec/specs/ticket-management/spec.md` | MODIFIED requirement (deliverable 2) |
| `openspec/changes/sync-workflow-polish-contracts/` | Moved to a dated archive path after validation (deliverable 5) |

Out of scope, by explicit non-goal: Go/runtime code, templates, CSS, JS, tests, goldens, migrations, skills, CI/CD, dependencies, coverage settings, all of `openspec/changes/archive/`, and the PR #64 worktree. No new test is added. No CI failure is fixed; any red at base `f6c35081f22d24fdfc4013a6860dff6ce0492afd` is inherited from `origin/main` and pre-existing by definition.

## Deliverable 1: `openspec/config.yaml` factual rewrite

The file currently describes a pre-implementation project. This PR rewrites only the stale facts. Every kept line stays byte-identical. The required facts come from the exploration verification (go.mod, `internal/` packages, Dockerfile, test trees) and the proposal's approved target text; nothing is anticipated beyond the implemented tree.

### Delta table

| Area | Current text | Required fact |
|---|---|---|
| Go version | `Go 1.25.11` | Keep (go.mod declares `go 1.25.11`) |
| Web stack | `net/http`; `HTMX + html/template` (SSR, no JS framework) | Keep; templates in `web/templates`, handlers in `internal/adapters/http` |
| JavaScript | absent | Limited to user/workflow interactions: the only shipped asset is `web/templates/static/workflow.js` (drag reorder, menu Escape handling) |
| SQLite | `driver choice: modernc.org/sqlite vs mattn/go-sqlite3 — decided during design` | `modernc.org/sqlite` (go.mod pins v1.56.0), pure Go, `CGO_ENABLED=0` |
| Architecture | `not yet defined (no code)` | Layers `domain`, `application`, `adapters/http`, `adapters/sqlite`, all present under `internal/` |
| Docker | `Docker multi-stage delivery` | Multi-stage build in `golang:1.25`, `CGO_ENABLED=0`, ships `gcr.io/distroless/static-debian12:nonroot`, UID 65532, `/data` chowned to 65532 |
| Testing | no migrations/goldens named; `e2e: none (no browser tooling detected...)` | SQL migrations (`internal/adapters/sqlite/migrate.go`, migration_0003..0009), golden tests (`internal/adapters/http/testdata/*.golden` via `golden_test.go` with `-update`), suites in `domain`, `application`, `adapters/http`, `adapters/sqlite`; e2e: Playwright MCP as local visual validation only, no versioned Playwright suite |
| Quality | `gofmt`; `go vet` | Keep as current facts |
| Verify | `coverage_threshold: 0` | Keep |
| strict_tdd / schema | `schema: spec-driven`; `strict_tdd: true` | Keep |

### Target file

Apply writes exactly this file:

```yaml
# openspec/config.yaml
schema: spec-driven

context: |
  Project: tkt — Go web application (server-side rendered).
  Tech stack: Go 1.25.11; backend with net/http; frontend via html/template with HTMX (SSR, no JS framework); JavaScript limited to user/workflow interactions (drag reorder, menu Escape handling); SQLite storage via modernc.org/sqlite (pure Go, CGO_ENABLED=0); multi-stage Docker build into a Distroless static image running as non-root (UID 65532).
  Architecture: layered — domain, application, adapters/http, adapters/sqlite.
  Testing: Go stdlib testing; suites in domain, application, adapters/http, and adapters/sqlite; SQL migrations; golden files for rendered HTML; Playwright MCP used locally for visual validation, with no versioned Playwright suite.
  Style: gofmt formatting, go vet clean; no external linters installed.

strict_tdd: true

testing:
  runner: go test
  test_command: "go test ./..."
  coverage_command: "go test -cover ./..."
  layers:
    unit: stdlib testing
    integration: net/http/httptest + in-memory SQLite
    e2e: Playwright MCP local visual validation only; no versioned Playwright suite
  quality:
    formatter: gofmt
    linter: go vet (staticcheck/golangci-lint not installed)
    type_checker: go vet (compile-time type checking)

rules:
  proposal:
    - Include rollback plan for risky changes
  specs:
    - Use Given/When/Then for scenarios
    - Use RFC 2119 keywords (MUST, SHALL, SHOULD, MAY)
  design:
    - SQLite store persists through modernc.org/sqlite; builds stay CGO_ENABLED=0 for the Distroless static image
    - Document architecture decisions with rationale
  tasks:
    - Group by phase, use hierarchical numbering
    - Keep tasks completable in one session
  apply:
    guidelines:
      - Follow existing code patterns
    tdd: true
    test_command: "go test ./..."
  verify:
    test_command: "go test ./..."
    build_command: "go build ./..."
    coverage_threshold: 0
  archive:
    - Warn before merging destructive deltas
```

Structural decisions:

- The `context:` block is the proposal's approved text, copied verbatim. It carries every required fact: Go version, web stack, JS boundary, driver, layers, Docker, migrations, goldens, suites, and the local-only Playwright MCP role.
- The `testing:` YAML section changes on exactly one line: `layers.e2e` now states the Playwright MCP fact. `unit`, `integration`, `quality`, `runner`, and both commands remain byte-identical because they are factual.
- The stale `design` rule "Decide SQLite driver (modernc.org/sqlite vs mattn/go-sqlite3) with rationale" is a one-time design task and is closed. It is replaced by a decided-fact rule stating the driver and the `CGO_ENABLED=0` constraint that the Distroless static image requires. This is the only `rules.*` change.
- No Staticcheck, no new pipeline, no future policy appears anywhere in the file. The `linter` line keeps its factual parenthetical that staticcheck and golangci-lint are not installed.
- The full-file replacement is a formatting convenience for apply; the delta table above is the semantic contract, and the diff check in validation confirms that only the listed lines differ from the current file.

## Deliverable 2: canonical merge mapping

Apply the four deltas to exactly the four canonical specs. The mapping below is the sync contract. Every requirement, scenario, and sentence in the canonical files outside these touch points stays byte-identical.

### Merge mapping

| Canonical spec | Change | Identity | Sync action |
|---|---|---|---|
| `category-workflows` | ADDED | `Builder Step and Field Menu Presentation` | New `### Requirement:` block inserted after `Horizontal master-detail workflow builder`, before `Additive Workflow Adoption`, under the existing `## Requirements` heading. Full requirement text: 7 MUST bullets plus 7 scenarios from the delta |
| `category-workflows` | MODIFIED | `Step Configuration Validation` | Append one new paragraph (Required control rules: text/select only, hidden for checkbox, persisted `required=true` normalizes to false, text-to-Checkbox clears Required, checkbox is boolean and never carries Required) after the existing paragraph, then append the 2 new scenarios after `Unknown form actor is rejected` |
| `ticket-workflow-execution` | MODIFIED | `Form Task Completion and Visibility` | Append one new paragraph (strict typed positional decoding: absent/empty/on/true/JSON-boolean mapping, required checkbox accepts false or absent, trimmed text, exact select match, strict array shape, JSON-boolean-only at the store, no raw-value leaks, typed JSON positional persistence) after the existing paragraph, then append the 7 new scenarios after `Non-actor cannot submit form` |
| `audit-log` | MODIFIED | `Merged Ticket Activity Timeline` | Append one new paragraph (`✓`/`×` glyph rendering in `role="img"` spans with accessible names `Yes`/`No`, literal values for other kinds, meaning never relies on color alone) after the existing paragraph, then append the 2 new scenarios after `Completed form results stay readable at 390px` |
| `ticket-management` | MODIFIED | `Current Task Card Presentation` | Append one new paragraph (Required applies only to compatible kinds; pinned checkbox MAY carry a legacy Required flag or native `required` control; true and false both valid and decodable; false or absent stays false; Required never forces true) after the existing paragraph, then append the 1 new scenario after `Historical activity remains merged and ordered` |

### Sync rules

- **Scenario additions only.** The delta files list the pre-existing scenarios alongside the new ones for context. The sync adds only the new scenarios; the pre-existing scenarios already live in canonical and are never re-added. The same applies to the existing requirement paragraphs: the deltas restate them for context, the sync appends only the new paragraphs.
- **Requirement text.** The synced requirement text includes the trailing "(Previously: ...)" annotation from the delta. This matches the convention already visible in canonical `audit-log` and `ticket-management`; the annotation documents the additive sync. Existing "(Previously: ...)" annotations elsewhere in the canonicals are untouched.
- **Delta apparatus stays in the change folder.** The `Scope note` header, the `Traceability` tables, and the `Evidence boundary and gaps` sections of the deltas are spec-phase working artifacts. They are preserved in this change directory (and later in the archive) and never copied into canonical specs. Verified: no canonical spec currently contains any of these sections.
- **No frontmatter changes.** `audit-log` and `ticket-management` keep their existing YAML frontmatter; `category-workflows` and `ticket-workflow-execution` keep none. Frontmatter normalization is a recorded hygiene gap, deliberately unfixed.
- **The horizontal master-detail contract is preserved, not duplicated.** `Horizontal master-detail workflow builder` keeps all 11 scenarios byte-identical, including `Horizontal reorder retains the moved selection` and `Keyboard actions provide reorder and remove fallbacks`. The new `Builder Step and Field Menu Presentation` requirement states in one bullet and one scenario that the menu presentation MUST NOT remove those keyboard reorder/remove fallbacks or drag reorder, and that they remain the accessible alternatives to touch-drag. That cross-reference is the non-regression clause; no drag/keyboard contract text is copied into the new requirement.
- **No supplemental contracts.** None of the supplemental polish items proven only by non-core tests (final Resolve/Close editor read-only, fields header and right-aligned `+ Add field`, drag indicator containment) appears in canonical text. The four deltas are the complete content source. The `1fr 44px` responsive field-row bullet and scenario in the ADDED requirement are part of the approved delta and do enter.
- **Identity anchors.** Headings use the exact delta wording: `### Requirement: Builder Step and Field Menu Presentation` and the four existing `### Requirement:` headings as identity anchors. The sync makes no heading renames.

## Deliverable 3: requirement-to-test matrix

The five tests are pre-existing on main at the exact locations below (re-verified read-only in this phase). This PR adds no tests. Every recovered MUST maps to at least one row; each row names its gap.

| # | Test (file:line) | Contract the test pins | Recovery target |
|---|---|---|---|
| 1 | `TestCategoryWorkflowBuilder_ThreeDotTriggerPolish` (`internal/adapters/http/handlers_category_workflows_test.go:1647`) | Shared `.workflow-trigger` style, exactly 32x32, centered `⋯`, no border at rest, gray hover, accent focus ring; exact accessible names `Step actions` per step card and `Field actions` per field row, legacy `Actions for step` forbidden; step trigger upper-right, field trigger fixed in the actions column (`grid-column:4`); Escape closes the open menu and returns focus to its trigger; an immutable terminal step exposes no trigger and no menu, so a terminal-only draft renders zero step menus and zero triggers | `category-workflows`: ADDED `Builder Step and Field Menu Presentation` |
| 2 | `TestCategoryWorkflowBuilder_CheckboxRequiredSemantics` (`internal/adapters/http/handlers_category_workflows_test.go:2031`) | Checkbox field is boolean; Required control stays available for text/select, hidden for checkbox; persisted `required=true` on a checkbox normalizes to non-required on the round trip; changing a required text field to Checkbox clears Required | `category-workflows`: MODIFIED `Step Configuration Validation` |
| 3 | `TestTimelineRendersCheckboxBooleanGlyphs` (`internal/adapters/http/handlers_category_workflows_test.go:2079`) | Submitted checkbox values render `✓` for true and `×` for false inside `role="img"` spans with `aria-label="Yes"` / `aria-label="No"`; every other field kind keeps its literal value; meaning never relies on color alone | `audit-log`: MODIFIED `Merged Ticket Activity Timeline` |
| 4 | `TestWorkflowRunner_FormDecoding` (`internal/application/workflow_runner_test.go:125`) | Strict positional decoding matrix at the runner: absent checkbox stores `false` and is valid even when Required; empty-string answer for a required checkbox is valid; `on`, `true`, and JSON boolean `true` decode to true; any other string is invalid; text values are trimmed; blank text on a required field is invalid; single-select must match a pinned option exactly and padded values are rejected; unknown, duplicate, ambiguous multi-value, and extra positions beyond the pinned definition are rejected; answers persist as a typed JSON positional array | `ticket-workflow-execution`: MODIFIED `Form Task Completion and Visibility` (runner half of the strict decode MUST) |
| 5 | `TestDecodeWorkflowResponseFields_StrictPinnedTypes` (`internal/adapters/sqlite/workflow_response_store_test.go:91`) | Strict typed decode at the store: wrong answer count rejected; checkbox decodes only from a JSON boolean, a string `"true"` is rejected; a required checkbox with `false` is valid and decodes to Kind checkbox Value `"false"`; single-select outside the pinned options is rejected; decode errors do not leak raw persisted values | `ticket-workflow-execution`: `Form Task Completion and Visibility` (store half of the same MUST) plus `ticket-management`: `Current Task Card Presentation` (Required-compatibility MUST) |

Cross-requirement corroboration, no separate MUST: the `ticket-management` Required-compatibility contract is proven primarily by test 5 and corroborated by test 4 (runner matrix) and test 2 (builder normalization). `TestAmendment4_CurrentTaskFormRetainsRequiredNativeControls` corroborates the native-`required` rendering.

## Deliverable 4: recorded coverage gaps

No test is added, so verification treats these as the evidence boundary. Each is recorded, none is fixed.

| # | Gap | Affected contract |
|---|---|---|
| 1 | Escape close-and-refocus is pinned only by static substring assertions on `workflow.js` source; no behavioral test executes the asset, and the repo has no versioned browser suite | `Builder Step and Field Menu Presentation` |
| 2 | Drag interaction (dragstart/dragover/drop) is not behaviorally tested; Go tests POST the `reorder` action directly and assert markup, CSS text, and JS source strings | `Horizontal master-detail workflow builder` (already canonical, not re-recovered) |
| 3 | The 32x32 hit area, hover, and focus-ring rules are asserted as CSS source text (`cssRuleDeclares`), not rendered geometry | `Builder Step and Field Menu Presentation` |
| 4 | Open-menu clipping at rail edges has no assertion; `.workflow-rail-wrap.menu-open` and the `.up` flip rules exist in `styles.html` but no test references them. The scenario records the intent; verification records the gap | `Builder Step and Field Menu Presentation` |
| 5 | Color independence is only partially proven: the glyph test asserts `role="img"`, `aria-label`, glyph characters, and literal values, but not the `workflow-bool-true`/`workflow-bool-false` classes or any color-agnostic contrast rule | `Merged Ticket Activity Timeline` |
| 6 | Reverse checkbox Required normalization (for example single-select to checkbox) is untested; only text-to-checkbox is covered | `Step Configuration Validation` |
| 7 | Decode-error non-leak of raw values is asserted only for the select-outside-options case | `Form Task Completion and Visibility` |
| 8 | Missing YAML frontmatter in eight canonical specs, including two of the four touched, is a hygiene gap, deliberately out of scope | none |

## Validation plan

These checks run in the apply/verify phases. None ran in this phase (design used read-only file inspection only), and none of them executes Go code, starts the server, or drives a browser.

| Check | Command or method | Pass condition |
|---|---|---|
| OpenSpec validation | `openspec validate --all --strict` | All specs, including the four synced ones and the change's delta specs, validate strict |
| Status check | `openspec status` | Change state is consistent before archive: all four delta specs present and valid, no blocked state |
| Diff check | `git diff origin/main...HEAD --stat` then full `git diff` review | Only the intended hunks appear: config facts and the four requirement touch points; no whitespace or formatting noise outside them |
| Five-test existence | Read-only `grep -n "func Test<Name>"` against the five `file:line` anchors | Each of the five test functions exists at its exact documented path and line (lines cannot drift: no runtime file changes in this PR) |
| Changed-path allowlist | `git diff --name-status origin/main...HEAD` | The complete set equals: the 5 modified paths below plus the archive rename pair for this change directory. Any other path fails the gate |
| No runtime claims | Review the diff and the verification evidence | The diff proves docs/config only; the verify report states explicitly that no Go test, build, or Playwright run was executed and why (docs-only PR: runtime harness evidence is `N/A`) |

Changed-path allowlist:

```
openspec/config.yaml
openspec/specs/category-workflows/spec.md
openspec/specs/ticket-workflow-execution/spec.md
openspec/specs/audit-log/spec.md
openspec/specs/ticket-management/spec.md
openspec/changes/sync-workflow-polish-contracts/  (rename pair into openspec/changes/archive/<date>-sync-workflow-polish-contracts/)
```

### Pre-existing CI rule

The base commit is `f6c35081f22d24fdfc4013a6860dff6ce0492afd` on `origin/main`. The CI gates (`gofmt -l .`, `go vet ./...`, `go build ./...`, `go test ./... -race -count=1`) do not compile or test OpenSpec markdown, so this PR cannot turn CI red on its own. At verify time, if CI is red at the base, the verify report labels it inherited and pre-existing and does not fix it.

## Archive plan

After the canonical sync lands and the validation checks above pass:

1. Move only this change directory to a dated path: `openspec/changes/sync-workflow-polish-contracts/` becomes `openspec/changes/archive/<YYYY-MM-DD>-sync-workflow-polish-contracts/` using the archive-date convention already present in `openspec/changes/archive/` (for example `2026-08-23-category-workflows`).
2. The move preserves every artifact present at archive time: `exploration.md`, `proposal.md`, `design.md`, `specs/` (all four delta files), plus the tasks and verify evidence produced by the later phases.
3. `openspec/changes/archive/2026-08-23-category-workflows/` and all other historical archives remain untouched, both during the sync and during this move.
4. Because this PR is docs-only, no runtime attempt acquisition is required: the work-unit evidence records the runtime harness as `N/A` with the reason that no runtime, template, CSS, JS, or dependency file changes.

## Rollback boundary

The rollback boundary is exactly the five sync paths plus the archive move:

- `openspec/config.yaml`
- `openspec/specs/category-workflows/spec.md`
- `openspec/specs/ticket-workflow-execution/spec.md`
- `openspec/specs/audit-log/spec.md`
- `openspec/specs/ticket-management/spec.md`
- the rename pair moving this change directory into the archive

A plain `git revert` of the PR commit restores the pre-change state with zero behavioral or data impact: the PR touches no runtime, template, CSS, JS, test, golden, migration, or dependency content. Archived historical files and the PR #64 worktree are outside the boundary by construction: they were never touched, so reverting cannot affect them.

## Risks

| Risk | Mitigation |
|---|---|
| Config rewrite drifts into future policy | The delta table is the contract; apply copies the facts and the target file exactly, and the diff check verifies only the listed lines changed |
| Sync duplicates pre-existing scenarios or paragraphs | The merge mapping names the append points explicitly and the sync rule is add-only; the diff check rejects any duplicate heading |
| "(Previously: ...)" annotation choice surprises a reviewer | Decision recorded here and in the merge mapping; it follows the convention already present in canonical `audit-log` and `ticket-management` |
| Evidence boundary mistaken for browser proof | The coverage-gap table records each static-assertion boundary; the verify phase re-checks the matrix and gap list |
| CI red at base attributed to this PR | The pre-existing CI rule is part of the validation plan; the verify report labels any inherited failure explicitly |
| Frontmatter half-normalization temptation | Non-goal recorded; the touched specs keep their current frontmatter state |

## Next step

`openspec/tasks` (native dispatcher: `sdd-tasks`) breaks the merge mapping, config replacement, validation, and archive steps into completable tasks for `sdd-apply`, then `sdd-verify` runs the validation plan and produces the verify report. After that, this change is archived per the archive plan. Canonical specs and config are NOT written by this design phase.