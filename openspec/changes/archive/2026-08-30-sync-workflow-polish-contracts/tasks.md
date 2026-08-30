# Tasks: Sync Workflow Polish Contracts

Change `sync-workflow-polish-contracts` is a docs-and-config recovery, single PR, well under the 400 authored-line budget. Canonical specs are synced to facts that main's tests already prove; `openspec/config.yaml` is rewritten to describe the implemented tree; the change is validated docs-only and archived. No runtime, template, CSS, JS, test, golden, migration, skill, CI, dependency, or coverage change. No new test. No CI fix.

## Review Workload Forecast

| Field | Value |
|-------|-------|
| Estimated changed lines | 180-260 (config ~40, four synced requirement blocks ~120-180, matrix doc ~50) |
| 400-line budget risk | Low |
| Chained PRs recommended | No |
| Suggested split | Single PR |
| Delivery strategy | single-pr |
| Chain strategy | pending |

```text
Decision needed before apply: No
Chained PRs recommended: No
Chain strategy: pending
400-line budget risk: Low
```

Runtime evidence note: this PR changes no runtime, template, CSS, JS, or dependency file, so no Go test, build, or Playwright run is executed. Every apply task records its harness evidence as `N/A`; verification records the same. A base CI red at `f6c35081f22d24fdfc4013a6860dff6ce0492afd` is inherited and pre-existing, never fixed here.

## Global scope guard (applies to every task)

- Editable files, and ONLY these: `openspec/config.yaml`; the four canonical specs `openspec/specs/category-workflows/spec.md`, `openspec/specs/ticket-workflow-execution/spec.md`, `openspec/specs/audit-log/spec.md`, `openspec/specs/ticket-management/spec.md`; the change folder `openspec/changes/sync-workflow-polish-contracts/` (including its archive rename).
- Never edit: any file under `openspec/changes/archive/` (including `2026-08-23-category-workflows`), the PR #64 worktree, or any Go/template/CSS/JS/test/golden/migration/skill/CI file.
- No frontmatter normalization on any spec. `audit-log` and `ticket-management` keep their YAML frontmatter; `category-workflows` and `ticket-workflow-execution` keep none.
- No supplemental polish contracts beyond the four deltas; no Staticcheck, pipeline, or future policy.

## Rollback boundary

Revertable set: `openspec/config.yaml`, the four canonical specs, and the change-folder archive rename pair. `git revert` of the PR restores the pre-change state with zero behavioral/data impact because the diff is docs/config only. Historical archives and the PR #64 worktree are outside the boundary by construction (never touched).

---

## T0. Read-only baseline and scope guard

- [x] Confirm the baseline and scope guard: base hash matches origin/main, PR #64 and history archives untouched, base CI red recorded as inherited.

Verify the starting state without modifying anything.

- Confirm the worktree's working base matches origin/main at `f6c35081f22d24fdfc4013a6860dff6ce0492afd` (`git rev-parse HEAD` / `git log -1 --format=%H`).
- Confirm the PR #64 worktree is untouched and is not inspected or modified.
- Confirm `openspec/changes/archive/` contents (including `2026-08-23-category-workflows/`) are byte-identical and unmodified.
- Capture the base CI state read-only. If CI is red at the base, record it as pre-existing/inherited; do NOT fix, run, or debug it.

Acceptance: base hash matches; PR #64 and historical archives untouched; any base CI red recorded as pre-existing with no attempt to fix.

Rollback: none (read-only) — nothing written.
Evidence: runtime harness `N/A` (no build/test run; CI state is observed, not executed).

## T1. Rewrite `openspec/config.yaml` to the design's factual target

- [x] Rewrite `openspec/config.yaml` to the design's factual target (context block, e2e line, one design rule replaced, everything else byte-identical).

Replace the stale pre-implementation content with the exact target file in design Deliverable 1.

- `context:` block set to the approved verbatim text (Go 1.25.11; net/http; html/template + HTMX, no JS framework; JS limited to user/workflow interactions; modernc.org/sqlite, CGO_ENABLED=0; multi-stage Distroless non-root UID 65532; four layers; migrations; goldens; suites; Playwright MCP local only).
- `testing.layers.e2e` states Playwright MCP local visual validation with no versioned suite.
- Replace only the stale `rules.design` "Decide SQLite driver ... with rationale" rule with the decided-fact rule: `SQLite store persists through modernc.org/sqlite; builds stay CGO_ENABLED=0 for the Distroless static image`.
- Keep byte-identical: `schema: spec-driven`, `strict_tdd: true`, `coverage_threshold: 0`, `gofmt`, `go vet`, all `rules.*` entries other than the one replaced design rule, `testing` runner/commands/unit/integration/quality.
- Add no Staticcheck, pipeline, or future policy anywhere.

Acceptance: the file equals the design's target YAML; the diff vs the current file shows exactly the listed lines changing (context block, e2e line, the one design rule); no future policy present.
Rollback: revert this file only.
Evidence: runtime harness `N/A` (config text only).

## T2. Sync `category-workflows` (ADDED + MODIFIED)

- [x] Sync `category-workflows`: ADD the new builder menu requirement block and MODIFY the step configuration validation requirement.

Apply the delta `specs/category-workflows/spec.md` to canonical `openspec/specs/category-workflows/spec.md`.

- ADD the new `### Requirement: Builder Step and Field Menu Presentation` block (7 MUST bullets + 7 scenarios) immediately after `### Requirement: Horizontal master-detail workflow builder` (canonical line ~114), before `### Requirement: Additive Workflow Adoption`. Copy the requirement text verbatim from the delta, including the single-MUST cross-reference that the presentation MUST NOT remove the keyboard reorder/remove fallbacks or drag reorder. Do NOT copy the horizontal master-detail contract or any of its 11 scenarios into the new requirement.
- MODIFY `### Requirement: Step Configuration Validation` (canonical line ~75): append the checkbox-Required paragraph (Required only for text/single-select, hidden for checkbox, persisted `required=true` normalizes to false, text-to-Checkbox clears Required, checkbox is boolean and never carries Required) after the existing paragraph, then append the 2 new scenarios (`Checkbox Required is available only for compatible kinds`, `Changing a required text field to Checkbox clears Required`) after `Unknown form actor is rejected`.
- Keep all unrelated canonical text byte-identical.

Acceptance: the ADDED block and the two appended scenarios/paragraph land exactly; horizontal master-detail block and its 11 scenarios byte-identical; no duplicate headings.
Rollback: revert `openspec/specs/category-workflows/spec.md`.
Evidence: runtime harness `N/A`; matched to `TestCategoryWorkflowBuilder_ThreeDotTriggerPolish` and `TestCategoryWorkflowBuilder_CheckboxRequiredSemantics`.

## T3. Sync `ticket-workflow-execution` (MODIFIED)

- [x] Sync `ticket-workflow-execution`: append the strict typed positional decoding paragraph and exactly 7 new scenarios.

Apply the delta `specs/ticket-workflow-execution/spec.md` to canonical `openspec/specs/ticket-workflow-execution/spec.md`.

- MODIFY `### Requirement: Form Task Completion and Visibility` (canonical line ~123): append the strict typed positional decoding paragraph (absent/empty/on/true/JSON-boolean mapping, required checkbox accepts false or absent, trimmed text, exact select match, strict array shape, JSON-boolean-only at the store, no raw-value leaks, typed JSON positional persistence) after the existing paragraph, then append the 7 NEW scenarios only (`Checkbox decodes strictly`, `Required checkbox accepts false or absent`, `Strict positional shape is enforced`, `Single-select matches a pinned option exactly`, `Text values are trimmed and required blanks are invalid`, `Answers persist as a typed JSON positional array`, `Store decodes checkbox strictly and never leaks raw values`) after `Non-actor cannot submit form`. Do NOT re-add the 4 pre-existing scenarios the delta lists for context.
- Keep all other canonical text byte-identical.

Acceptance: one new paragraph plus exactly 7 new scenarios appended; the 4 pre-existing scenarios appear once (not duplicated); no other changes.
Rollback: revert `openspec/specs/ticket-workflow-execution/spec.md`.
Evidence: runtime harness `N/A`; matched to `TestWorkflowRunner_FormDecoding` and `TestDecodeWorkflowResponseFields_StrictPinnedTypes`.

## T4. Sync `audit-log` (MODIFIED)

- [x] Sync `audit-log`: append the accessible glyph paragraph and exactly 2 new scenarios.

Apply the delta `specs/audit-log/spec.md` to canonical `openspec/specs/audit-log/spec.md`.

- MODIFY `### Requirement: Merged Ticket Activity Timeline` (canonical line ~144): append the glyph paragraph (`✓`/`×` in `role="img"` spans with accessible names `Yes`/`No`, literal values for other kinds, meaning never relies on color alone) after the existing paragraph, then append the 2 NEW scenarios (`Checkbox boolean values render with accessible glyphs`, `Non-checkbox values keep their literal text`) after `Completed form results stay readable at 390px`. Do NOT re-add the 8 pre-existing scenarios the delta lists for context.
- Keep all other canonical text and the existing YAML frontmatter byte-identical.

Acceptance: one new paragraph plus exactly 2 new scenarios appended; no duplicated scenarios; frontmatter unchanged.
Rollback: revert `openspec/specs/audit-log/spec.md`.
Evidence: runtime harness `N/A`; matched to `TestTimelineRendersCheckboxBooleanGlyphs`.

## T5. Sync `ticket-management` (MODIFIED)

- [x] Sync `ticket-management`: append the Required-compatibility paragraph and exactly 1 new scenario.

Apply the delta `specs/ticket-management/spec.md` to canonical `openspec/specs/ticket-management/spec.md`.

- MODIFY `### Requirement: Current Task Card Presentation` (canonical line ~214): append the Required-compatibility paragraph (Required only for text/single-select; pinned checkbox MAY carry legacy Required flag or native `required` control; true and false both valid and decodable; false or absent stays false; Required never forces true) after the existing paragraph, then append the 1 NEW scenario (`Pending checkbox accepts false or absent regardless of Required`) after `Historical activity remains merged and ordered`. Do NOT re-add the 3 pre-existing scenarios the delta lists for context.
- Keep all other canonical text and the existing YAML frontmatter byte-identical.

Acceptance: one new paragraph plus exactly 1 new scenario appended; no duplicated scenarios; frontmatter unchanged.
Rollback: revert `openspec/specs/ticket-management/spec.md`.
Evidence: runtime harness `N/A`; matched to `TestDecodeWorkflowResponseFields_StrictPinnedTypes`.

## T6. Retain the five-test matrix and evidence gaps in the change folder

- [x] Ensure the five-test matrix and all eight evidence gaps are retained in the change folder (create the standalone doc only if missing).

Ensure deliverable 4 is satisfied at archive time without adding tests.

- Confirm the exact five-test requirement-to-test matrix and all eight evidence gaps are retained across the change-folder artifacts (`proposal.md`, `design.md`, and the four `specs/*/spec.md` deltas).
- If any of the five named tests or any of the eight gaps is not already present in a change-folder artifact, create `openspec/changes/sync-workflow-polish-contracts/requirement-to-test-matrix.md` capturing the full matrix and gap list. Do NOT create it if the matrix and all eight gaps are already retained.

Common matrix rows (exact): `TestCategoryWorkflowBuilder_ThreeDotTriggerPolish` (:1647), `TestCategoryWorkflowBuilder_CheckboxRequiredSemantics` (:2031), `TestTimelineRendersCheckboxBooleanGlyphs` (:2079), `TestWorkflowRunner_FormDecoding` (:125), `TestDecodeWorkflowResponseFields_StrictPinnedTypes` (:91).

Acceptance: at archive time the change folder retains the five-test matrix and all eight evidence gaps (either in existing artifacts or the standalone doc); no test file is created or modified.
Rollback: delete only the standalone matrix doc if created; otherwise none.
Evidence: runtime harness `N/A` (methodology text; the five test names are verified read-only in T7).

## T7. Docs-only validation gate

- [x] Run the docs-only validation gate: strict validation, allowlist check, whitespace check, and read-only test anchor verify.

Validate before archive. No Go or Playwright runtime is executed.

- Run `openspec validate --all --strict`; all specs including the four synced ones and the four deltas validate strict.
- Run `openspec status`; the change is consistent, no blocked state.
- Run `git diff --check` against origin/main; no whitespace errors.
- Inspect the changed-path allowlist (`git diff --name-status origin/main...`): the complete set equals the five modified paths (`openspec/config.yaml` + four canonical specs) plus any change-folder additions; nothing else may appear.
- Read-only verify the five exact test names exist at their documented `file:line` anchors (`grep -n "func Test<Name>"`).
- Confirm no runtime/template/CSS/JS/test/golden/migration/skill/CI/dependency/coverage/content change in the diff.
- If base CI is red, record it as pre-existing/inherited.

Acceptance: strict validation passes; allowlist is exactly the intended set; the five tests resolve at their anchors; no runtime/dependency content changed; base CI state recorded (inherited if red).
Rollback: none (read-only validation).
Evidence: validation + diff commands are the verification evidence; runtime harness `N/A` by design (docs-only PR).

## T8. Archive the change (sdd-archive lifecycle)

Archive is a lifecycle phase, not an implementation task counted by native apply/verify readiness.

Archive only after T7 passes and independent verification/sync complete.

- Move the entire change directory `openspec/changes/sync-workflow-polish-contracts/` to `openspec/changes/archive/<YYYY-MM-DD>-sync-workflow-polish-contracts/` using the archive convention in place (e.g. `2026-08-23-category-workflows`), preserving every artifact present (exploration, proposal, design, tasks, the four `specs/*/spec.md` deltas, the matrix doc if created, and verify evidence).
- Never modify, rename, or touch any existing archive folder.

Acceptance: the whole change directory moves preserving all artifacts; every existing archive folder byte-identical; rollback boundary (the rename pair) documented.
Rollback: move the archive directory back to its change path.
Evidence: runtime harness `N/A`.
