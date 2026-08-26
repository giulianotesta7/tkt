# Apply Progress: sync-workflow-polish-contracts

Phase: apply (docs/config recovery)
Status: 8 of 8 implementation tasks complete; archive (T8) deliberately NOT performed — archive is a later `sdd-archive` lifecycle phase after verify and sync per orchestrator instruction.
Artifact store: openspec (file-based). No previous apply-progress existed; this is the first apply batch.

## Structured status consumed

- `applyState`: ready (native status reported apply ready with 9 pending tasks at dispatch).
- Base: `f6c35081f22d24fdfc4013a6860dff6ce0492afd` = `origin/main` (`feat(workflows): polish category workflow editor (#61)`).
- Delivery: single-pr strategy, chain strategy `pending` (not needed: `Chained PRs recommended: No`).
- `actionContext`: no workspace-planning mode, no allowedEditRoots warning, no unsafe edit roots. Worktree is the authorized private worktree `pr0-openspec-workflow-polish-sync`.
- Review Workload Guard: `Decision needed before apply: No`; `Chained PRs recommended: No`; `400-line budget risk: Low`. No delivery decision required.

## Strict TDD handling

STRICT TDD MODE IS ACTIVE (`strict_tdd: true` in config; runner `go test ./...`), but this change is documentation/config-only: no Go/runtime, template, CSS, JS, test, golden, migration, skill, CI, or dependency file changes. Per the delegated instruction, no Go tests, builds, or Playwright runs were executed. Runtime harness evidence is `N/A` with the explicit reason that no executable/runtime files change.

### TDD Cycle Evidence

| Task | RED | GREEN | Test run | Evidence |
|---|---|---|---|---|
| T1 config rewrite | n/a (docs/config) | n/a | not run | Diff vs `origin/main` shows exactly the design's three delta regions: context block, `layers.e2e` line, one `rules.design` rule; everything else byte-identical. Runtime `N/A` (config text only). |
| T2 `category-workflows` sync | n/a (docs) | n/a | not run | ADDED `Builder Step and Field Menu Presentation` block byte-identical to delta; MODIFIED `Step Configuration Validation` paragraph + 2 scenarios byte-identical to delta; horizontal master-detail block (11 scenarios) byte-identical. Runtime `N/A`. |
| T3 `ticket-workflow-execution` sync | n/a (docs) | n/a | not run | Strict typed positional decoding paragraph + exactly 7 new scenarios appended after `Non-actor cannot submit form`; 4 pre-existing scenarios appear once (verified: single occurrence). Runtime `N/A`. |
| T4 `audit-log` sync | n/a (docs) | n/a | not run | Glyph paragraph + exactly 2 new scenarios appended after `Completed form results stay readable at 390px`; frontmatter unchanged. Runtime `N/A`. |
| T5 `ticket-management` sync | n/a (docs) | n/a | not run | Required-compatibility paragraph + exactly 1 new scenario appended after `Historical activity remains merged and ordered`; frontmatter unchanged. Runtime `N/A`. |
| T6 matrix/gaps retention | n/a (docs) | n/a | not run | Verified all five tests (:1647, :2031, :2079, :125, :91) and all eight gaps retained across proposal/design/deltas. No standalone matrix doc created. Runtime `N/A`. |
| T7 validation gate | n/a (docs) | n/a | not run | See Validation evidence below. Runtime `N/A` by design. |

## Completed tasks and evidence

### T0. Read-only baseline and scope guard — DONE

- `git rev-parse HEAD` = `f6c35081f22d24fdfc4013a6860dff6ce0492afd`, matches `origin/main`.
- PR #64 worktree: untouched; not inspected or modified (separate worktree; no path in our diff touches it).
- `openspec/changes/archive/` (incl. `2026-08-23-category-workflows/`): `git diff origin/main --stat -- openspec/changes/archive/` is EMPTY — byte-identical, unmodified.
- Base CI observed read-only via `gh run list --branch main --limit 3`: run for `f6c35081...` (#61) has `conclusion: failure`. Recorded as PRE-EXISTING / INHERITED from base; not fixed, not run, not debugged.
- Evidence: runtime harness `N/A` (CI state observed, not executed).

### T1. Rewrite `openspec/config.yaml` — DONE

- Wrote the exact design target file (Deliverable 1).
- `git diff origin/main -- openspec/config.yaml` shows exactly the three intended regions:
  1. `context:` block replaced with the approved verbatim facts (Go 1.25.11; net/http; html/template + HTMX SSR no JS framework; JS limited to user/workflow interactions; modernc.org/sqlite pure-Go CGO_ENABLED=0; multi-stage Distroless non-root UID 65532; layered architecture; migrations/goldens/suites; Playwright MCP local only).
  2. `testing.layers.e2e` line → `Playwright MCP local visual validation only; no versioned Playwright suite`.
  3. Only `rules.design` first entry replaced: stale "Decide SQLite driver ... with rationale" → "SQLite store persists through modernc.org/sqlite; builds stay CGO_ENABLED=0 for the Distroless static image".
- Byte-identical kept: `schema: spec-driven`, `strict_tdd: true`, `coverage_threshold: 0`, gofmt, go vet, all other `rules.*`, runner/commands/unit/integration/quality.
- No Staticcheck, no new pipelines, no future policy added (verified by diff: only 3 regions changed, 12 insertions / 6 deletions).
- Evidence: runtime harness `N/A` (config text only).

### T2. Sync `category-workflows` — DONE

- ADDED `### Requirement: Builder Step and Field Menu Presentation` immediately after `Horizontal master-detail workflow builder`, before `Additive Workflow Adoption`: 8 MUST bullets (includes the single-MUST cross-reference that presentation MUST NOT remove keyboard reorder/remove fallbacks or drag reorder; task text says "7 MUST bullets" but the approved delta carries 8 bullets — copied verbatim from the delta, which is the authoritative source) + 7 scenarios. Block verified byte-identical to the delta ADDED section.
- Horizontal master-detail contract: byte-identical, 11 scenarios preserved, NOT duplicated (verified via block comparison of `origin/main` slice vs current slice).
- MODIFIED `Step Configuration Validation`: appended the checkbox-Required paragraph + `(Previously: ...)` annotation after the existing paragraph; appended 2 new scenarios (`Checkbox Required is available only for compatible kinds`, `Changing a required text field to Checkbox clears Required`) after `Unknown form actor is rejected`. Byte-identical to delta.
- No duplicate headings/scenarios (verified: 0 duplicates).
- Evidence: runtime harness `N/A`; contract matched to `TestCategoryWorkflowBuilder_ThreeDotTriggerPolish` (:1647) and `TestCategoryWorkflowBuilder_CheckboxRequiredSemantics` (:2031) read-only in T7.

### T3. Sync `ticket-workflow-execution` — DONE

- MODIFIED `Form Task Completion and Visibility`: appended the strict typed positional decoding paragraph + `(Previously: ...)` annotation after the existing paragraph; appended exactly 7 new scenarios after `Non-actor cannot submit form`. All byte-identical to the delta.
- The 4 pre-existing scenarios (`Requester completes supported fields`, `Invalid field answer does not advance`, `Assignee answer is requester-visible`, `Non-actor cannot submit form`) remain single-occurrence — NOT duplicated (verified via scenario count and duplicate check: none).
- Evidence: runtime harness `N/A`; matched to `TestWorkflowRunner_FormDecoding` (:125) and `TestDecodeWorkflowResponseFields_StrictPinnedTypes` (:91).

### T4. Sync `audit-log` — DONE

- MODIFIED `Merged Ticket Activity Timeline`: appended the glyph paragraph (`✓`/`×` in `role="img"` spans with accessible names `Yes`/`No`, literal values for other kinds, meaning never relies on color alone) + `(Previously: ...)` annotation after the existing paragraph; appended exactly 2 new scenarios after `Completed form results stay readable at 390px`. Byte-identical to delta.
- 8 pre-existing scenarios not duplicated (count: 0 duplicates); YAML frontmatter unchanged (diff shows only additions).
- Evidence: runtime harness `N/A`; matched to `TestTimelineRendersCheckboxBooleanGlyphs` (:2079).

### T5. Sync `ticket-management` — DONE

- MODIFIED `Current Task Card Presentation`: appended the Required-compatibility paragraph + `(Previously: ...)` annotation after the existing paragraph; appended exactly 1 new scenario (`Pending checkbox accepts false or absent regardless of Required`) after `Historical activity remains merged and ordered`. Byte-identical to delta.
- 3 pre-existing scenarios not duplicated; YAML frontmatter unchanged.
- Evidence: runtime harness `N/A`; matched to `TestDecodeWorkflowResponseFields_StrictPinnedTypes` (:91) with corroboration from tests :125 and :2031 (documented in design Deliverable 3).

### T6. Retain five-test matrix and evidence gaps — DONE

- Programmatic scan of the change folder confirmed the exact five-test matrix (each test name co-located with its `:line` anchor in exploration/proposal/design/tasks/deltas) and all eight evidence gaps (present in proposal and/or design and/or deltas).
- Standalone `requirement-to-test-matrix.md` NOT created — matrix and all eight gaps are already retained across existing change-folder artifacts, per task acceptance.
- No test file created or modified.
- Evidence: runtime harness `N/A` (methodology text; test names verified read-only in T7).

### T7. Docs-only validation gate — DONE (with one honest caveat)

- `openspec validate --all --strict`: **NOT RUN — the `openspec` CLI is not installed** (checked `which openspec`, `~/.npm-global/bin`, `~/.local/bin`, `find`, and `npx --no-install openspec`; unavailable). Per instruction, we do NOT claim strict validation passed. Remaining read-only checks below substitute where possible.
- `openspec status`: NOT RUN (same CLI unavailability).
- `git diff --check origin/main`: passed (exit 0, no whitespace errors).
- Changed-path allowlist exact: `openspec/config.yaml`, `openspec/specs/category-workflows/spec.md`, `openspec/specs/ticket-workflow-execution/spec.md`, `openspec/specs/audit-log/spec.md`, `openspec/specs/ticket-management/spec.md` (M) + `openspec/changes/sync-workflow-polish-contracts/` (untracked change dir). NOTHING else (verified: no non-openspec change anywhere; archive tree diff empty).
- Five-test anchors resolved exactly (read-only grep):
  - `TestCategoryWorkflowBuilder_ThreeDotTriggerPolish` @ `internal/adapters/http/handlers_category_workflows_test.go:1647` ✓
  - `TestCategoryWorkflowBuilder_CheckboxRequiredSemantics` @ `...handlers_category_workflows_test.go:2031` ✓
  - `TestTimelineRendersCheckboxBooleanGlyphs` @ `...handlers_category_workflows_test.go:2079` ✓
  - `TestWorkflowRunner_FormDecoding` @ `internal/application/workflow_runner_test.go:125` ✓
  - `TestDecodeWorkflowResponseFields_StrictPinnedTypes` @ `internal/adapters/sqlite/workflow_response_store_test.go:91` ✓
- No duplicate canonical headings/scenarios (0 duplicates in all four synced specs).
- Master-detail block byte-identical vs `origin/main`; new block placement verified (after master-detail, before Additive).
- Diff is pure addition for specs (0 deletions) and config shows only the 3 intended regions: no runtime/template/CSS/JS/test/golden/migration/skill/CI/dependency/coverage content.
- Base CI red recorded as pre-existing/inherited (`#61` at `f6c35081...` shows failure). Never fixed.
- Evidence: validation + diff commands are the verification evidence; runtime harness `N/A` by design.

### T8. Archive the change — NOT PERFORMED (later lifecycle phase)

- Archive is a later `sdd-archive` phase after verify and sync. Do NOT archive in apply.
- The change directory remains at `openspec/changes/sync-workflow-polish-contracts/` with exploration.md, proposal.md, design.md, tasks.md, specs/ (four delta files), and this apply-progress.md.

## Files changed (this apply phase)

- `openspec/config.yaml` (modified)
- `openspec/specs/category-workflows/spec.md` (modified)
- `openspec/specs/ticket-workflow-execution/spec.md` (modified)
- `openspec/specs/audit-log/spec.md` (modified)
- `openspec/specs/ticket-management/spec.md` (modified)
- `openspec/changes/sync-workflow-polish-contracts/tasks.md` (implementation checkboxes T0–T7 → `[x]`; T8 is an uncounted archive lifecycle heading)
- `openspec/changes/sync-workflow-polish-contracts/apply-progress.md` (this file)

No other path touched. No Go test/build/Playwright run executed.

## Test commands run

- None executed (docs-only PR; strict-TDD runtime evidence `N/A`).
- Read-only/validation commands run: `git diff --check origin/main` (pass), `git diff origin/main` review, `git status --porcelain` allowlist, `gh run list --branch main` (CI state observation), Python byte-compare scripts (delta vs canonical block identity, duplicate scan, five-test matrix/gap scan).

## Deviations from design

1. `openspec validate --all --strict` and `openspec status` could not run: the `openspec` CLI is not installed in this environment. Recorded as unclaimable; verify phase should attempt with the CLI available or record the same constraint.
2. Task text described the ADDED requirement as "7 MUST bullets"; the approved delta carries 8 MUST bullets. Copied verbatim from the delta (authoritative source). The 8th bullet is the required single-MUST cross-reference.
3. No standalone `requirement-to-test-matrix.md` created: T6 acceptance says create only if the matrix/gaps are missing; they are fully retained across existing artifacts.

## Remaining lifecycle phase

`openspec/changes/sync-workflow-polish-contracts/` remains to be moved by the later `sdd-archive` phase after independent verification and canonical sync. There are no unchecked implementation tasks; the native dispatcher can now route to verification.

## Workload / PR boundary

- Estimated authored change: 171 insertions / 6 deletions across 5 files (config 12/6, category-workflows +81, ticket-workflow-execution +56, audit-log +18, ticket-management +10). Well under the 400-line budget.
- Single PR, single work unit: docs-and-config recovery. `Chained PRs recommended: No`; `Decision needed before apply: No`.
- Rollback boundary: revert `openspec/config.yaml` + the four canonical specs; archive rename pair comes later with archive phase. Historical archives and PR #64 worktree outside the boundary by construction.

## Next step

`sdd-verify` runs the validation plan (re-attempt `openspec validate --all --strict` / `openspec status` if the CLI is available; re-check allowlist, whitespace, five-test anchors, and base CI state) and produces the verify report. After verify and sync, `sdd-archive` moves this change directory to `openspec/changes/archive/<date>-sync-workflow-polish-contracts/`.