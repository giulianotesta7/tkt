# Proposal: Sync Workflow Polish Contracts

> **Outcome first:** PR0 is a documentation-only recovery. The workflow-polish contracts already implemented and test-proven on `origin/main` (`f6c35081f22d24fdfc4013a6860dff6ce0492afd`) never reached the canonical OpenSpec specs: four specs (`category-workflows`, `ticket-workflow-execution`, `audit-log`, `ticket-management`) carry stale contract text, and `openspec/config.yaml` still describes a pre-implementation project. This change syncs those four specs to the implemented facts, rewrites `openspec/config.yaml` to describe only what exists today, and archives the change afterward. No Go/runtime, template, CSS, JS, test, golden, migration, skill, CI/CD, dependency, coverage, PR #64, or historical archive is touched. Any base CI failure is pre-existing and stays unfixed.

## Intent / Problem

Main's tests prove behavior that the canonical specs do not state. Five tests pin the builder trigger menu polish, checkbox Required semantics, timeline boolean glyphs, runner form decoding, and strict store decoding. A reviewer or future contributor reading the specs would not know these contracts exist. Separately, `openspec/config.yaml` records design-time placeholders (SQLite driver undecided, architecture not yet defined, no e2e, no migrations/goldens) that contradict the implemented tree. This PR recovers the missing contract text and corrects the config facts. It does not change behavior, and it adds no new tests: the five named tests are pre-existing evidence anchors.

### Alternatives considered

| Alternative | Verdict |
|---|---|
| Leave specs stale and config pre-implementation | Rejected, the specs are the contract of record for verification and future work |
| Recover contracts by editing archived change artifacts | Rejected, archives are read-only historical reference |
| Fold all recovered contracts into `Horizontal master-detail workflow builder` | Rejected, that requirement already carries 11 scenarios and would become unreadable |

## Scope

### In scope

- `openspec/config.yaml` rewritten to current facts only (deliverable 1).
- Four canonical spec syncs, attachments to existing canonical requirement identities (deliverable 2): `category-workflows`, `ticket-workflow-execution`, `audit-log`, `ticket-management`.
- Recovery of every user-listed contract (deliverable 3), evidenced by the requirement-to-test matrix over the five named tests (deliverable 4).
- Archive of this change after spec sync and strict validation (deliverable 5).
- Read-only reference to `openspec/changes/archive/2026-08-23-category-workflows/**` for baseline text. No edits.

### Out of scope (non-goals)

- Any change to Go/runtime code, templates, CSS, JS, tests, goldens, migrations, skills, CI/CD, dependencies, or coverage settings.
- Any new test. The five named tests already exist on main and move nowhere.
- Any CI fix. The `.github/workflows/ci.yml` gates do not compile or test OpenSpec markdown, so this PR cannot turn CI red on its own; a red base (`f6c35081`) is inherited from `origin/main` and is pre-existing by definition.
- Any edit under `openspec/changes/archive/` (including `2026-08-23-category-workflows`) and any inspection or modification of the PR #64 worktree.
- Frontmatter normalization on any spec, including the four touched ones. Two of the four already carry YAML frontmatter (`audit-log`, `ticket-management`) and two do not (`category-workflows`, `ticket-workflow-execution`); adding frontmatter to only the touched pair would leave a half-normalized tree, and normalizing all eight frontmatter-less specs would widen a docs-recovery PR into a hygiene PR for no behavioral value. Recorded as a hygiene gap, not fixed here.
- Supplemental polish proven only by non-core tests (final Resolve/Close editor read-only, fields header and right-aligned `+ Add field`, drag indicator containment) unless a user-listed contract directly requires it. None does, so none enters.
- Future policies: no Staticcheck, no new pipelines, no anticipated rules. `strict_tdd: true`, `schema: spec-driven`, `coverage_threshold: 0`, `gofmt`, and `go vet` stay as they are.

## Deliverable 1: `openspec/config.yaml` factual update

The rewritten file describes only implemented facts. Table of deltas against the current file:

| Area | Current text | Required fact |
|---|---|---|
| Go version | `Go 1.25.11` | Keep (go.mod declares `go 1.25.11`) |
| Web stack | `net/http`; `HTMX + html/template` (SSR, no JS framework) | Keep; HTML templates live in `web/templates`, handlers in `internal/adapters/http` |
| JavaScript | absent | Limited to user/workflow interactions: the only shipped asset is `web/templates/static/workflow.js` (drag reorder, menu Escape handling) |
| SQLite | driver choice decided during design | `modernc.org/sqlite` (go.mod pins v1.56.0), pure Go, `CGO_ENABLED=0` |
| Architecture | not yet defined | Layers `domain`, `application`, `adapters/http`, `adapters/sqlite`, all present under `internal/` |
| Docker | multi-stage delivery | Multi-stage build in `golang:1.25` with `CGO_ENABLED=0`, shipping `gcr.io/distroless/static-debian12:nonroot`, UID 65532, `/data` chowned to 65532 |
| Testing | unit/integration only, no migrations/goldens named | SQL migrations (`internal/adapters/sqlite/migrate.go`, migration_0003..0009), golden tests (`internal/adapters/http/testdata/*.golden` via `golden_test.go` with `-update`), suites in `domain`, `application`, `adapters/http`, `adapters/sqlite` |
| e2e | none | Playwright MCP as local visual validation only; no versioned Playwright suite, no browser pipeline, no Playwright dependency |
| Quality | gofmt; go vet | Keep as current facts |
| Verify | `coverage_threshold: 0` | Keep |
| strict_tdd / schema | `schema: spec-driven`; `strict_tdd: true` | Keep |

The `rules.design` entry "Decide SQLite driver (modernc.org/sqlite vs mattn/go-sqlite3) with rationale" is a stale one-time design task. It is replaced by a rule stating the decided fact: the SQLite store persists through `modernc.org/sqlite` and the build stays `CGO_ENABLED=0` for the Distroless static image. The driver question is closed; nothing else in `rules.*` changes.

Target `context:` block (approved text for spec/apply):

```
Project: tkt — Go web application (server-side rendered).
Tech stack: Go 1.25.11; backend with net/http; frontend via html/template with HTMX (SSR, no JS framework); JavaScript limited to user/workflow interactions (drag reorder, menu Escape handling); SQLite storage via modernc.org/sqlite (pure Go, CGO_ENABLED=0); multi-stage Docker build into a Distroless static image running as non-root (UID 65532).
Architecture: layered — domain, application, adapters/http, adapters/sqlite.
Testing: Go stdlib testing; suites in domain, application, adapters/http, and adapters/sqlite; SQL migrations; golden files for rendered HTML; Playwright MCP used locally for visual validation, with no versioned Playwright suite.
Style: gofmt formatting, go vet clean; no external linters installed.
```

## Deliverable 2: canonical spec sync

Four specs, four requirement touch points, zero new identities except one (`category-workflows`). Every attachment uses the existing canonical requirement heading as its identity.

### `category-workflows`

- **New requirement: `Builder Step and Field Menu Presentation`** (identity decision). The menu/trigger polish, Escape behavior, and stable responsive field rows land here rather than inside `Horizontal master-detail workflow builder`, which already carries 11 scenarios; adding six more would destroy its readability. The horizontal master-detail contract itself is already canonical and untouched.
- **Extend: `Step Configuration Validation`** with the checkbox Required semantics MUST and a scenario.

### `ticket-workflow-execution`

- **Extend: `Form Task Completion and Visibility`** with one MUST covering strict typed positional decoding at both the runner and the store, plus the matrix scenarios. Runner and store halves share the MUST because they pin one contract: answers decode strictly, positionally, and typed.

### `audit-log`

- **Extend: `Merged Ticket Activity Timeline`** with the checkbox boolean glyph MUST and a scenario. The requirement already covers the inline `dl`/`dt`/`dd` values and 390px behavior; the glyph rule joins it.

### `ticket-management`

- **Extend: `Current Task Card Presentation`** with the Required-compatibility MUST and a pending-form scenario: a pinned checkbox may carry `Required: true`, the pending control renders the native `required` attribute, and a decodable false or absent answer is accepted at the command layer and stays `false`.

## Deliverable 3: recovered contracts

| User-listed contract | Status | Canonical identity |
|---|---|---|
| Horizontal master-detail | Already canonical, untouched | `Horizontal master-detail workflow builder` |
| Drag/drop and accessible action alternatives | Already canonical, untouched | `Horizontal master-detail workflow builder` (scenarios `Horizontal reorder retains the moved selection`, `Keyboard actions provide reorder and remove fallbacks`, `Typed Add step popover uses allowed types`) |
| 32x32 `⋯` step/field triggers and exact names | Recovered | `Builder Step and Field Menu Presentation` (new, `category-workflows`) |
| Escape closes and refocuses | Recovered | `Builder Step and Field Menu Presentation` |
| Menu positioning without clipping | Recovered, clipped-evidence gap recorded | `Builder Step and Field Menu Presentation` |
| No empty terminal trigger | Recovered | `Builder Step and Field Menu Presentation` |
| Stable responsive field rows | Recovered | `Builder Step and Field Menu Presentation` |
| Required only for compatible types | Recovered | `Step Configuration Validation` |
| Checkbox never admits Required semantically; text→Checkbox normalizes `required=false` | Recovered | `Step Configuration Validation` |
| Checkbox decoding: absent/empty→false, on/true→true, false valid, other values rejected | Recovered | `Form Task Completion and Visibility` |
| Timeline ✓/× with accessible Yes/No and non-color meaning | Recovered | `Merged Ticket Activity Timeline` |
| Checkbox true and false valid in ticket management | Recovered | `Current Task Card Presentation` |

## Deliverable 4: requirement-to-test matrix (the five named tests)

The five tests are pre-existing on main at these exact locations. This PR adds no tests. The matrix is the acceptance evidence for verification: every recovered MUST maps to at least one row, and each row names its gap.

| # | Test (file:line) | Contract the test pins | Recovery target |
|---|---|---|---|
| 1 | `TestCategoryWorkflowBuilder_ThreeDotTriggerPolish` (`internal/adapters/http/handlers_category_workflows_test.go:1647`) | One reusable `.workflow-trigger` style, exactly 32x32, centered `⋯`, no border at rest, gray hover, accent focus ring; exact accessible names `Step actions` per step card and `Field actions` per field row, legacy `Actions for step` forbidden; step trigger upper-right, field trigger fixed in the actions column (`grid-column:4`); Escape closes the open menu and returns focus to its trigger (`event.key !== "Escape"`, `details.open = false`, `summary.focus()`); an immutable terminal step exposes no trigger and no menu, so a terminal-only draft renders zero step menus and zero triggers | `category-workflows` → `Builder Step and Field Menu Presentation` (new), which also carries the stable responsive field-row scenarios (390px single-column stacking `1fr 44px`, no horizontal overflow, field trigger reachable) |
| 2 | `TestCategoryWorkflowBuilder_CheckboxRequiredSemantics` (`internal/adapters/http/handlers_category_workflows_test.go:2031`) | A checkbox field is boolean; the Required control stays available for text/select, is hidden for checkbox; a persisted `required=true` on a checkbox normalizes to non-required on the round trip; changing a required text field to Checkbox clears Required | `category-workflows` → `Step Configuration Validation` (extend) |
| 3 | `TestTimelineRendersCheckboxBooleanGlyphs` (`internal/adapters/http/handlers_category_workflows_test.go:2079`) | Submitted checkbox values render `✓` for true and `×` for false inside `role="img"` spans with `aria-label="Yes"` / `aria-label="No"`; every other field kind keeps its literal value; meaning never relies on color alone | `audit-log` → `Merged Ticket Activity Timeline` (extend) |
| 4 | `TestWorkflowRunner_FormDecoding` (`internal/application/workflow_runner_test.go:125`) | Strict positional decoding matrix at the runner: absent checkbox stores `false` and is valid even when Required; empty-string answer for a required checkbox is valid; `on`, `true`, and JSON boolean `true` decode to true; any other string (e.g. `yes`) is invalid; text values are trimmed; blank text on a required field is invalid; single-select must match a pinned option exactly and a padded value is rejected; unknown, duplicate, ambiguous multi-value, and extra positions beyond the pinned definition are rejected; answers persist as a typed JSON positional array | `ticket-workflow-execution` → `Form Task Completion and Visibility` (extend, runner half of the strict decode MUST) |
| 5 | `TestDecodeWorkflowResponseFields_StrictPinnedTypes` (`internal/adapters/sqlite/workflow_response_store_test.go:91`) | Strict typed decode at the store: wrong answer count rejected; a checkbox must decode from a JSON boolean, a string `"true"` is rejected; a required checkbox with `false` is valid and decodes to `Kind checkbox, Value "false"`; a single-select outside the pinned options is rejected; decode errors do not leak raw persisted values | `ticket-workflow-execution` → `Form Task Completion and Visibility` (store half of the same MUST) plus `ticket-management` → `Current Task Card Presentation` (the Required-compatibility MUST below) |

Cross-requirement corroboration, no separate MUST: the ticket-management Required-compatibility contract (pinned checkbox may carry `Required: true`; native `required` renders; false or absent stays `false`; Required never forces true) is proven primarily by test 5 and corroborated by the test 4 runner matrix and the test 2 builder normalization. `TestAmendment4_CurrentTaskFormRetainsRequiredNativeControls` further corroborates the native-`required` rendering.

## Evidence boundary and recorded gaps

No tests are added, so verification treats the following as the evidence boundary and records each gap explicitly. None is fixed in this PR.

1. Escape close-and-refocus is pinned only by static substring assertions on `workflow.js` source. No behavioral test executes the asset, and the repo has no versioned browser suite (see config: Playwright MCP is local visual validation only).
2. Drag interaction (dragstart/dragover/drop) is not behaviorally tested; Go tests POST the `reorder` action directly and assert markup, CSS text, and JS source strings.
3. The 32x32 hit area, hover, and focus-ring rules are asserted as CSS source text (`cssRuleDeclares`), not rendered geometry.
4. Open-menu clipping at rail edges has no assertion: `.workflow-rail-wrap.menu-open` and the `.up` flip rules exist in `styles.html` but no test references them, so "no clipping" is proven only for the closed rail and the drag indicator. The spec scenario records the intent; verification records the gap.
5. The color-independence half of the glyph contract is only partially proven: `TestTimelineRendersCheckboxBooleanGlyphs` asserts `role="img"`, `aria-label`, glyph characters, and literal values for other kinds, but not the `workflow-bool-true`/`workflow-bool-false` classes or any color-agnostic contrast rule. The spec states the intent (meaning never relies on color alone); verification marks the class/contrast half as unasserted.
6. Reverse conversions for checkbox Required normalization are untested (e.g. single-select to checkbox); only text-to-checkbox is covered.
7. Decode-error non-leak of raw values is asserted only for the select-outside-options case.
8. Missing YAML frontmatter in eight canonical specs, including two of the touched four, is a hygiene gap, deliberately out of scope (see non-goals).

## Decisions (resolved defaults, subject to the proposal question round)

| Decision | Choice | Rationale |
|---|---|---|
| New requirement identity | `Builder Step and Field Menu Presentation` in `category-workflows` | Keeps `Horizontal master-detail workflow builder` (11 scenarios) readable; matches exploration recommendation |
| Frontmatter on touched specs | Out of scope | Half-normalized tree has no behavioral value; scope stays docs-recovery |
| Supplemental polish (final editor, header alignment, drag indicator containment) | Out of scope | Proven only by non-core tests; not required by any user-listed bullet |
| Stale design rule | Replaced with a decided-fact rule | Leaving "decide the driver" contradicts the config cleanup |
| Playwright MCP | Recorded as local visual validation with no versioned suite | Matches the repo state; no browser pipeline exists or is added |
| Appendix: base CI state | Recorded, never fixed here | Any red at base `f6c35081` is inherited and pre-existing |

## Rollback

Revert only the docs/config diff: `openspec/config.yaml` and the four synced specs (`openspec/specs/category-workflows/spec.md`, `openspec/specs/ticket-workflow-execution/spec.md`, `openspec/specs/audit-log/spec.md`, `openspec/specs/ticket-management/spec.md`). Because the PR touches no runtime, template, CSS, JS, test, golden, migration, or dependency, rollback is a plain `git revert` of the PR commit with zero behavioral or data impact. Archived change folders and the PR #64 worktree are outside the revert boundary by construction, since they were never touched.

## Success criteria

1. `openspec/config.yaml` states only current facts (deliverable 1 table) and contains no anticipated Staticcheck, pipeline, or future policy; `strict_tdd`, `schema`, `coverage_threshold: 0`, `gofmt`, and `go vet` are unchanged.
2. The four specs carry every user-listed contract attached to the canonical identities in deliverables 2 and 3; `Horizontal master-detail workflow builder` and all other canonical text outside the four touch points are byte-identical.
3. The requirement-to-test matrix maps all five named tests exactly as listed, with the gaps above recorded and no new tests added.
4. The diff is docs/config only: no Go, template, CSS, JS, test, golden, migration, skill, CI, dependency, coverage, archive, or PR #64 content appears in it. Authored lines stay well under the 400-line budget.
5. Base CI status is re-checked at verify time and labeled inherited if red, never attributed to this change.

## Archive step

After the spec sync lands and strict validation passes, this change is archived: `openspec/changes/sync-workflow-polish-contracts/` moves to a dated path under `openspec/changes/archive/` preserving every artifact (exploration, proposal, specs, verify evidence, this proposal included). Historical archives, including `2026-08-23-category-workflows`, remain untouched.

## Risks

| Risk | Mitigation |
|---|---|
| Config delta drifts into future policy | The deliverable 1 table is the contract; apply must copy facts, never anticipate |
| Recovery text exceeds the spec's evidence | Each MUST is traceable to a named test or an explicitly recorded gap; verification re-checks the matrix |
| Spec drift against the archive | Recoveries attach to canonical text only; the archived `Friendly Vertical Builder` content is never quoted as current |
| Scope creep from supplemental polish | Non-goals and the decisions table name each excluded item explicitly |
| Evidence boundary mistaken for browser proof | Verification treats static CSS/JS assertions as the boundary and records the gaps, per the gap list |