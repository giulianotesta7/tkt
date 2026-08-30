---
change: sync-frontend-contracts
phase: design
status: completed
artifact_store: openspec
---

# Design: Sync Frontend Contracts

## Decision summary

This change is a docs-only synchronization for issue #74. Exactly three canonical spec files change: `openspec/specs/ticket-management/spec.md` (MODIFIED requirement), `openspec/specs/comment-timeline/spec.md` (MODIFIED requirement), and `openspec/specs/appearance-settings/spec.md` (new canonical file created from the delta). The three approved deltas under `openspec/changes/sync-frontend-contracts/specs/` are the complete and only source of canonical text changes. Nothing else is modified: no runtime, template, CSS, JS, test, golden, migration, skill, CI, dependency, or coverage file, no config rewrite (unlike the archived sibling), and no edit to any archive or to the foreign active change `resolved-requester-confirmation`.

Key decisions:

| Decision | Choice |
|---|---|
| Canonical change set | Exactly the three approved deltas applied to exactly three canonical spec files |
| Modified requirements | `Ticket Detail Presentation` (ticket-management); `Add Comment` (comment-timeline) — both as full-block replacements |
| New canonical spec | `appearance-settings`, created from the ADDED delta with a minimal canonical skeleton (title, Purpose, Requirements) and no YAML frontmatter |
| Replacement semantics | Unlike the sibling's append-only paragraphs, both MODIFIED deltas restate the complete requirement; the sync replaces the whole canonical requirement region (text + scenarios) with the delta block verbatim |
| Delta apparatus | `# Delta for ...` titles, scope notes, `## MODIFIED/ADDED Requirements` wrappers, and `## Notes` traceability tables never enter canonical text; they stay in this change folder and later in the archive |
| "(Previously: ...)" annotations | Retained in the synced requirement text, matching the existing canonical convention |
| Frontmatter | `ticket-management` and `comment-timeline` canonical frontmatter preserved byte-identical; the new `appearance-settings` canonical gets none (matches `category-workflows`/`ticket-workflow-execution` precedent, proven valid under strict validation) |
| Validation anchor | Change-scoped strict validation is the gate anchor; repo-wide `--all` is expected to exit 1 on the foreign untracked change `resolved-requester-confirmation`, accounted explicitly |
| Runtime evidence | None claimed: no Go test, build, or Playwright run is part of this change; runtime harness is `N/A` by design |
| Contradiction rule | If apply or verify finds a delta contradicting current implementation, stop and report blocked — no workaround is designed |

## Verified baseline (design-phase read-only spot-checks)

All traceability anchors named in the three deltas were re-verified read-only on this branch (worktree HEAD `2aa8ac4`, branch `feat/55-resolved-requester-confirmation`). No contradiction with implemented behavior was found, so the deltas still match the shipped product and the design proceeds.

| Delta anchor | Resolved evidence (file:line) | Result |
|---|---|---|
| Always-visible `Properties`/`Assignment`/`State` sidebar | `web/templates/partials/ticket_detail.html:41-42`, `:63-64`, `:85-86` (`prop-section`/`prop-heading` blocks) | resolved |
| No `<details>` / no `localStorage` script | full-file grep of `ticket_detail.html` for `<details` and `collapsed:v1` | zero matches |
| Closed read-only rendering | `ticket_detail.html:11` (`CanEdit`+`not .Closed`), `:33` (comment form hidden when closed), `:51`, `:72`; `internal/adapters/http/handlers_tickets.go:561` (`closed := domain.IsClosed(...)`) | resolved |
| Golden contract tests | `internal/adapters/http/golden_test.go:358` `TestTicketDetailPresentationContract`; `:283` `TestClosedTicketDetailReadOnly` | resolved |
| Closed predicate | `internal/domain/state.go:18` `IsClosed` | resolved |
| Application guard before store | `internal/application/comment_service.go:50-51` (`domain.IsClosed` → `ForbiddenError(ErrMsgCommentOnClosedTicket)`) | resolved |
| Service tests | `internal/application/comment_service_test.go:87` `TestAddCommentOnClosedTicketRejected`; `:126` `TestAddCommentOnOpenTicketAccepted` | resolved |
| HTTP 403 mapping | `internal/adapters/http/handlers_detail_test.go:410` `TestTicketCommentOnClosedTicketRejected` | resolved |
| Settings routes + capability gate | `internal/adapters/http/handlers_settings.go:27-28` (`GET /settings`, `POST /settings/appearance`); `:64`, `:87` (`requireCapability(CapManageUsers)`) | resolved |
| Allowed colors + default | `internal/application/settings_service.go:12` (`DefaultInternalCommentBg = "#E8EEFF"`); `:19` (`{DefaultInternalCommentBg, "#EFE9FB", "#FFF6DC"}`) | resolved |
| Panel radio markup | `web/templates/pages/settings_index.html:15` (`name="internal_comment_bg"`, `checked`) | resolved |
| Per-request shell stamping | `internal/adapters/http/middleware_auth.go:161-169`; `web/templates/partials/styles.html:9` (`--internal-comment-bg`) | resolved |
| Timeline effect | `web/templates/partials/styles.html:192` (`.timeline-comment.internal{background:var(--internal-comment-bg)...}`); `web/templates/partials/timeline.html:5` | resolved |
| Migration seed | `internal/adapters/sqlite/migrations/0005_instance_settings.sql` (the delta's traceability shorthand `migrations/0005_instance_settings.sql` resolves here — path-prefix nuance, not a contradiction) | resolved |
| Settings store | `internal/adapters/sqlite/settings_store.go:30`, `:44` | resolved |
| Settings HTTP tests | `internal/adapters/http/handlers_settings_test.go:18`, `:39`, `:61`, `:92`, `:115`, `:147` (six `TestSettings*`) | resolved |
| Rail link gating | `web/templates/base.html:27` (`{{if .CanManageUsers}}` → `/settings` rail link) | resolved |

Canonical layout facts confirmed this phase: `openspec/specs/comment-timeline/spec.md` exists (stale `Add Comment` still reads "regardless of ticket state" and its `Comment on a closed ticket` scenario says the comment "is accepted and stored"); `openspec/specs/appearance-settings/spec.md` does not exist and is added by this change. Both existing target canonicals carry YAML frontmatter (`name`/`status: proposed`/`change: tkt-mvp`). No active change overlaps the three domains: the only sibling active change, `resolved-requester-confirmation`, has no specs directory and touches none of them.

## Scope and boundaries

| Path | Action |
|---|---|
| `openspec/specs/ticket-management/spec.md` | MODIFIED requirement `Ticket Detail Presentation` (deliverable 1) |
| `openspec/specs/comment-timeline/spec.md` | MODIFIED requirement `Add Comment` (deliverable 2) |
| `openspec/specs/appearance-settings/spec.md` | ADDED — new canonical file (deliverable 3) |
| `openspec/changes/sync-frontend-contracts/` | Gains `apply-progress.md`, `verify-report.md`, `archive-report.md`; then moved to a dated archive path (deliverable 5) |

Out of scope, by explicit non-goal: Go runtime code, `web/templates/**`, `web/static/**`, CSS, JS, tests, goldens, migrations, skills, CI/CD, dependencies, coverage settings, `openspec/config.yaml`, all of `openspec/changes/archive/**`, the foreign active change `openspec/changes/resolved-requester-confirmation/`, and any worktree outside this one. No new test is added; the six `TestSettings*`, the two golden tests, and the comment tests are pre-existing evidence.

## Deliverable 1: canonical sync — `ticket-management` (MODIFIED, full-block replacement)

- Target: `openspec/specs/ticket-management/spec.md`, requirement `### Requirement: Ticket Detail Presentation`.
- Operation: replace the entire canonical requirement region — from the line `### Requirement: Ticket Detail Presentation` through the last line before `### Requirement: Pending Workflow Presentation` — with the delta's `### Requirement: Ticket Detail Presentation` block copied verbatim from `specs/ticket-management/spec.md` (requirement paragraph, `(Previously: ...)` line, and all 4 scenarios: `Cards default open`, `Card state survives reload`, `Closed ticket renders read-only metadata without mutation controls`, `Reopen affordance matches the state machine`).
- Region boundaries: the preceding requirement `### Requirement: Lifecycle Timestamps` and the following `### Requirement: Pending Workflow Presentation` are untouched, including the blank-line separators around the replaced region. The two canonical scenario names that also exist in the delta (`Cards default open`, `Card state survives reload`) are replaced, not duplicated; their old text (expanded `<details>` cards, saved localStorage collapse state) disappears with the region.
- Formatting rule: copy the delta block byte-for-byte, including the delta's blank line between each `#### Scenario:` heading and its `- GIVEN` bullets. Do not reformat to match the old canonical's tighter style, and do not normalize anything else in the file (for example, the existing missing-blank-line quirk before `### Requirement: Readable Numbering` stays as-is).
- Frontmatter: the YAML frontmatter at the top of the file stays byte-identical.
- Byte-identical rule: every other requirement, scenario, and sentence in the file (`Create Ticket`, `Readable Numbering`, `Update Ticket Fields`, `Lifecycle Timestamps`, `Pending Workflow Presentation`, `Current Task Card Presentation`, `Claim Assignment Sidebar`, and the archived sibling's `Current Task Card Presentation` additions) remains byte-identical. The file diff must show exactly one replaced region and nothing else.

## Deliverable 2: canonical sync — `comment-timeline` (MODIFIED, full-block replacement)

- Target: `openspec/specs/comment-timeline/spec.md`, requirement `### Requirement: Add Comment` (the first requirement, directly after `## Purpose`).
- Operation: replace the entire canonical requirement region — from the line `### Requirement: Add Comment` through the last line before `### Requirement: Newest-First Timeline` — with the delta's `### Requirement: Add Comment` block copied verbatim from `specs/comment-timeline/spec.md` (requirement paragraph with the closed-state rejection rule, `(Previously: ...)` line, and all 5 scenarios: `Add a public comment`, `Reject empty comment`, `Comment on a closed ticket`, `User cannot comment on another's ticket`, `User's internal comment rejected`).
- Region boundaries: `### Requirement: Newest-First Timeline` and `### Requirement: Append-Only Comments` are untouched. All five canonical scenario names also exist in the delta, so nothing is duplicated — the stale `Comment on a closed ticket` text ("the comment is accepted and stored") is replaced by the rejection text.
- Frontmatter: the YAML frontmatter at the top of the file stays byte-identical.
- Byte-identical rule: `Newest-First Timeline` and `Append-Only Comments`, including all their scenarios and the file's existing missing-blank-line quirk before `### Requirement: Newest-First Timeline`, remain byte-identical. The file diff must show exactly one replaced region and nothing else.

## Deliverable 3: canonical sync — `appearance-settings` (ADDED, new canonical file)

- Target: `openspec/specs/appearance-settings/spec.md` — the file does not exist and is created.
- Skeleton: no YAML frontmatter (matches the `category-workflows`/`ticket-workflow-execution` precedent; strict validation accepts it). Structure:

  ```markdown
  # Appearance Settings Specification

  ## Purpose

  Defines the instance appearance settings: the admin-only Settings route and navigation, the internal-comment background color selection with its three supported colors, persistence and validation of updates, and the observable effect of the configured color on internal-comment presentation in ticket timelines.

  ## Requirements

  <the four `### Requirement:` blocks from the delta, verbatim>
  ```

- The four requirement blocks (`Appearance Settings Access and Navigation`, `Internal Comment Background Selection`, `Update Internal Comment Background`, `Internal Comment Presentation Effect` — 4 requirements, 6 scenarios total) are copied byte-for-byte from the delta's `## ADDED Requirements` section, without the wrapper heading and without the `## Notes` traceability table. The Purpose text above is the only authored sentence; it adds no contract beyond the delta.
- Duplicate-identity guard: before creating the file, confirm no other canonical spec already defines any of the four requirement identities (search `openspec/specs/**` for the headings). T0 already recorded the absence; re-confirm at apply time.

## Common sync rules and conflict handling

- Verbatim copy: canonical additions/replacements come only from the delta files, byte-for-byte. No paraphrase, no reordering, no reformatting.
- Apparatus exclusion: `# Delta for ...` titles, scope notes, `## MODIFIED/ADDED Requirements` wrappers, and `## Notes` traceability tables never enter canonical text.
- No frontmatter normalization anywhere; the touched canonicals keep their current frontmatter state and the new canonical gets none.
- No duplicate headings: after the sync, a scan of the three canonical files must find each `### Requirement:` and `#### Scenario:` heading exactly once within its file.
- Precondition checks (run before editing; any failure means stop and report blocked — never hand-merge):
  1. `### Requirement: Ticket Detail Presentation` appears exactly once in canonical `ticket-management`, and its region still matches the stale contract the delta replaced (contains `localStorage` and the `<details><summary>` wording).
  2. `### Requirement: Add Comment` appears exactly once in canonical `comment-timeline`, and its region still matches the stale "regardless of ticket state" contract.
  3. `openspec/specs/appearance-settings/spec.md` does not exist.
  4. `git status --porcelain` shows no dirty canonical spec file.
- If canonical text drifted between spec authoring and apply (for example, an interim sync landed), the delta's assumptions no longer hold: stop and report blocked with the observed drift instead of designing a workaround.

## Validation gates (apply phase)

Docs-only gates; no Go, build, or browser command is executed. Runtime harness evidence is `N/A` by design.

| Check | Command | Pass condition |
|---|---|---|
| Change-scoped strict validation (gate anchor) | `openspec validate sync-frontend-contracts --type change --strict --no-interactive` | exit 0 |
| Archived validation | `openspec validate --archived --no-interactive` | exit 0; all existing archives still validate |
| Repo-wide strict validation | `openspec validate --all --strict --no-interactive` | exit 1 is EXPECTED and acceptable only under the foreign-change accounting below |
| Whitespace | `git diff --check` | exit 0, empty output |
| Changed paths | `git status --porcelain` / `git diff --name-status` | only the three canonical paths (tracked modifications) plus this change folder; nothing else |
| Duplicate scan | grep for `### Requirement:` / `#### Scenario:` in the three canonical files | each heading exactly once per file |

Foreign-change accounting (the known caveat): a coexisting foreign untracked change, `openspec/changes/resolved-requester-confirmation/`, makes repo-wide `--all --strict` exit 1. The gate therefore anchors on the change-scoped strict validation (exit 0) plus explicit accounting of the `--all` run: in that same run's output, `change/sync-frontend-contracts` must show a passing (`✓`) result, every canonical spec — including the new `appearance-settings` — must pass, and the only failing item must be `change/resolved-requester-confirmation`. Any other failing item is a real gate failure: stop and report blocked.

## Verify phase plan

Fresh read-only evidence, produced at verify time (not reused from apply):

- Byte capture method: `command 2>&1 | sha256sum` for each evidence command (combined stderr into stdout, piped to `sha256sum`); exit codes captured via `PIPESTATUS[0]`. Re-run each command once to confirm hash reproducibility.
- Evidence commands:
  1. `openspec validate sync-frontend-contracts --type change --strict --no-interactive` (primary; expected exit 0)
  2. `openspec validate --archived --no-interactive` (expected exit 0)
  3. `openspec validate --all --strict --no-interactive` (expected exit 1, foreign-change accounting as above)
  4. `git diff --check` (expected exit 0)
  5. `git status --porcelain` (expected: only the three canonical paths + this change folder)
- Delta counts, counted via `^### Requirement:` and `^#### Scenario:` across the three delta files:

  | Delta | Requirements | Scenarios |
  |---|---|---|
  | `ticket-management` | 1 | 4 |
  | `comment-timeline` | 1 | 5 |
  | `appearance-settings` | 4 | 6 |
  | **Total** | **6** | **15** |

  All 6 requirements and all 15 scenarios must be covered by the committed canonical sync (verified by reading the three canonical files and confirming each delta block landed verbatim at its target region).
- Read-only test-anchor check: confirm each pre-existing evidence test still resolves via `grep -n "func Test..."` (names, not line numbers, are the contract; record resolved `file:line`): `TestTicketDetailPresentationContract`, `TestClosedTicketDetailReadOnly`, `TestAddCommentOnClosedTicketRejected`, `TestAddCommentOnOpenTicketAccepted`, `TestTicketCommentOnClosedTicketRejected`, and the six `TestSettings*` tests. No test is run, added, or modified.
- Native envelope: the verify report must start with a `gentle-ai.verify-result/v1` fenced YAML block containing `evidence_revision` (64-hex sha256 of the HEAD commit object bytes: `git cat-file commit HEAD | sha256sum` — a 40-char git hash is structurally rejected), `verdict`, `blockers: 0`, `critical_findings: 0`, `requirements: 6/6`, `scenarios: 15/15`, `test_command` (the change-scoped strict validation) with `test_exit_code` and `test_output_hash`, and `build_command` (`openspec validate --archived --no-interactive`) with `build_exit_code` and `build_output_hash`. All fields are mandatory.
- Iterate `gentle-ai sdd-verify-validate` until it exits 0; the report body must include the docs-only justification (zero executable content, runtime harness `N/A`) and the foreign-change accounting.

## Archive plan

Preconditions: verify verdict `pass` with the native envelope validated, all tasks checked, no unchecked `- [ ]` implementation task line in any change artifact.

1. Move the entire change directory with `git mv openspec/changes/sync-frontend-contracts openspec/changes/archive/<YYYY-MM-DD>-sync-frontend-contracts/`, where the date is the execution date (`2026-08-30` or later, following the existing archive-date convention).
2. The move preserves every artifact present at archive time: `proposal.md`, `tasks.md`, `design.md`, `specs/` (the three delta directories), `apply-progress.md`, `verify-report.md`, and the new `archive-report.md`.
3. Write `archive-report.md` inside the archived folder before (or as part of) the move, containing: a field table (change name, archive date, artifact store, archive status), what the change delivered (the three canonical syncs, delta totals 6 requirements / 15 scenarios, zero runtime footprint), the verification state (envelope verdict, `evidence_revision`, validation results, foreign-change accounting), the artifacts-read table, the domains-synced table (`ticket-management` MODIFIED, `comment-timeline` MODIFIED, `appearance-settings` ADDED), task completion status, notes (including the resolved migration path nuance), and the archive path mapping.
4. Never modify, rename, or touch any existing archive folder, including `2026-08-30-sync-workflow-polish-contracts`.

## Rollback boundary

The rollback boundary is exactly the three canonical spec paths plus the archive rename pair:

- `openspec/specs/ticket-management/spec.md`
- `openspec/specs/comment-timeline/spec.md`
- `openspec/specs/appearance-settings/spec.md`
- the rename pair moving `openspec/changes/sync-frontend-contracts/` into `openspec/changes/archive/<date>-sync-frontend-contracts/`

A plain `git revert` of the sync and archive commits restores the pre-change state with zero behavioral or data impact: the diff contains no runtime, template, CSS, JS, test, golden, migration, skill, or CI content and no data migration exists. Historical archives and the foreign change directory are outside the boundary by construction (never touched).

## Out-of-scope guard

- No runtime, template, CSS, JS, test, golden, migration, skill, CI, dependency, coverage, or `openspec/config.yaml` file may ever change in this change. The only permitted writes are the three canonical spec paths and files inside `openspec/changes/sync-frontend-contracts/**` (plus its archive move).
- The deltas must match implemented behavior as-of this branch. The design-phase spot-check table above confirms they do at HEAD `2aa8ac4`; apply re-runs the cheap greps before editing, and verify re-checks the anchors read-only.
- Stop condition: if any spot-check at apply or verify time finds a delta contradicting current implementation (an anchor missing, renamed, or behaving differently than the delta states), stop and report blocked with the specific contradiction. Do not edit the delta to match a workaround, do not hand-merge around the drift, and do not touch runtime files to restore the delta's assumption.

## Deliverables (paths the apply phase will produce)

| # | Path | Kind |
|---|---|---|
| 1 | `openspec/specs/ticket-management/spec.md` | modified (one replaced requirement region) |
| 2 | `openspec/specs/comment-timeline/spec.md` | modified (one replaced requirement region) |
| 3 | `openspec/specs/appearance-settings/spec.md` | new canonical file |
| 4 | `openspec/changes/sync-frontend-contracts/apply-progress.md` | new change artifact (apply phase) |
| 5 | `openspec/changes/sync-frontend-contracts/verify-report.md` | new change artifact (verify phase, native envelope) |
| 6 | `openspec/changes/archive/<date>-sync-frontend-contracts/archive-report.md` | new artifact, written into the moved folder (archive phase) |
| 7 | rename pair `openspec/changes/sync-frontend-contracts/` → `openspec/changes/archive/<date>-sync-frontend-contracts/` | archive move (archive phase) |

## Risks

| Risk | Mitigation |
|---|---|
| Full-block replacement touches neighboring requirements | Region boundaries are the adjacent `### Requirement:` headings; the per-file diff must show exactly one replaced region; the duplicate-heading scan catches accidents |
| Delta formatting differs from canonical style (blank line after `#### Scenario:`) | Copy verbatim, no normalization; strict validation accepts it (sibling precedent) |
| New canonical file invents contracts beyond the delta | Only the three/four delta requirement blocks enter; the one authored Purpose sentence adds no MUST/SHALL/MAY clause |
| Repo-wide `--all` red mistaken for a real failure | The foreign-change accounting rule is part of the gate: only `change/resolved-requester-confirmation` may fail, everything else must pass |
| Canonical drift between spec authoring and apply | Precondition checks re-read the anchors; any drift stops the change as blocked |
| Frontmatter temptation on the new file | Recorded decision: no frontmatter, matching half the canonical corpus and strict-validation precedent |
| Evidence anchors drift (line numbers) | Names, not line numbers, are the contract; verify records resolved `file:line` |

## Next step

`apply` (native dispatcher: `sdd-apply`) executes the three canonical syncs per the merge rules above, runs the validation gates, and writes `apply-progress.md`; then `verify` produces the envelope-bearing report; then `archive` performs the move. Canonical specs are NOT written by this design phase, and no `gentle-ai sdd-attempt` call is made here (the parent owns attempts).
