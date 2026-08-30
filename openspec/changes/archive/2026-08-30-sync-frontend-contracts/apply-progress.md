# Apply Progress: sync-frontend-contracts

Change: `sync-frontend-contracts` (issue #74) — docs-only canonical spec sync.
Artifact store: `openspec`. Executor: sdd-apply. Worktree: `issue-55-resolved-closed-split` (branch `feat/55-resolved-requester-confirmation`, HEAD `76df662`).

## Status

COMPLETE — all three canonical syncs executed per `design.md`, all validation gates run, foreign-change accounting satisfied. Ready for `verify`.

## Consumed inputs

| Artifact | Source | Note |
|---|---|---|
| `design.md` | `openspec/changes/sync-frontend-contracts/design.md` | Merge-mechanics authority; preconditions and gates executed verbatim |
| `tasks.md` | same folder | 7 checkbox items across task groups T0–T4, all already `[x]`; apply executes the canonical sync the authoring phase deferred |
| Deltas ×3 | `specs/{ticket-management,comment-timeline,appearance-settings}/spec.md` | Sole source of canonical text |
| `apply-progress.md` (previous) | none | Fresh write (first apply batch) |


## Preconditions (design drift guard) — all PASS

| # | Check | Evidence | Result |
|---|---|---|---|
| P1 | `### Requirement: Ticket Detail Presentation` exactly once in canonical `ticket-management`, still stale | line 136 (sole); region contains `localStorage` (×2) and `<details><summary>` (×1); ancestor `### Requirement: Pending Workflow Presentation` at line 151 matches design | PASS |
| P2 | `### Requirement: Add Comment` exactly once in canonical `comment-timeline`, still stale | line 15 (sole); "regardless of ticket state" (×1); stale scenario text "the comment is accepted and stored" (×1); ancestor `### Requirement: Newest-First Timeline` at line 51 matches design | PASS |
| P3 | `openspec/specs/appearance-settings/spec.md` absent | `test ! -e` OK | PASS |
| P4 | No dirty canonical spec file | `git status --porcelain` showed only pre-existing foreign untracked `openspec/changes/resolved-requester-confirmation/` | PASS |
| P5 | Duplicate-identity guard for the 4 ADDED requirement identities | `grep -r "Requirement: Appearance Settings Access and Navigation"` etc. across `openspec/specs/**` → zero matches before creation; after creation each heading appears exactly once | PASS |

Implementation-anchor spot checks (design out-of-scope guard, re-run at apply): `web/templates/partials/ticket_detail.html` has 3 `prop-section` + 3 `prop-heading`, zero `<details>`, zero `collapsed:v1`; `internal/application/comment_service.go:50` `domain.IsClosed` guard; `internal/adapters/http/handlers_settings.go:28` `POST /settings/appearance`, `:64`/`:87` `requireCapability(CapManageUsers)`; `internal/application/settings_service.go:12` `DefaultInternalCommentBg = "#E8EEFF"`, `:19` allowed set `{"#E8EEFF","#EFE9FB","#FFF6DC"}`. No contradiction found — proceeds.

## Sync 1 — `openspec/specs/ticket-management/spec.md` (MODIFIED, full-block replacement)

- Region replaced: old lines 136–150 (from `### Requirement: Ticket Detail Presentation` through the line before `### Requirement: Pending Workflow Presentation`).
- Anchor: delta region extracted mechanically with `awk '/^### Requirement: Ticket Detail Presentation/{f=1} /^## Notes$/{f=0} f'` from the delta file (1 requirement, 4 scenarios asserted) — delta apparatus (`# Delta for`, scope note, `## MODIFIED Requirements`, `## Notes`) excluded.
- Inserted verbatim, preserving the blank-line separator between the region and `Pending Workflow Presentation`; frontmatter (lines 1–4) byte-identical; every other requirement byte-identical (`cmp` proven: head 1–135 and tail 151→EOF equal to pre-edit copies).
- Result: canonical region lines 136–170 equals the extracted delta block byte-for-byte (`cmp` exit 0); file grew 269 → 290 lines (+21, matching the delta's 35-line block minus the 15-line old region net).
- `git diff -U0` old-line ranges: 138–149 only — all strictly inside the replaced region; exactly one contiguous replacement.

## Sync 2 — `openspec/specs/comment-timeline/spec.md` (MODIFIED, full-block replacement)

- Region replaced: old lines 15–50 (from `### Requirement: Add Comment` through the line before `### Requirement: Newest-First Timeline`).
- Anchor: delta region extracted with `awk '/^### Requirement: Add Comment/{f=1} /^## Notes$/{f=0} f'` (1 requirement, 5 scenarios asserted); apparatus excluded; block normalized to a single trailing newline so the file's existing no-blank-line quirk before `### Requirement: Newest-First Timeline` is preserved (heading now directly follows `- AND no comment is stored`).
- Frontmatter byte-identical; `Newest-First Timeline` and `Append-Only Comments` byte-identical (`cmp` proven).
- Result: canonical region lines 15–53 equals the extracted delta block byte-for-byte (`cmp` exit 0); file grew 69 → 73 lines; stale "regardless of ticket state" contract and stale "the comment is accepted and stored" scenario text are gone.
- `git diff -U0` old-line ranges: 17–36 only — all strictly inside the replaced region; exactly one contiguous replacement.

## Sync 3 — `openspec/specs/appearance-settings/spec.md` (ADDED, new canonical file)

- Created per design skeleton: H1 `# Appearance Settings Specification`, `## Purpose` (the one authored sentence, verbatim from design.md — no RFC 2119 keyword, verified), `## Requirements`, then the four delta requirement blocks (6 scenarios) copied byte-for-byte from `## ADDED Requirements` (wrapper heading and `## Notes` table excluded).
- No YAML frontmatter (design decision; strict validation confirms).
- Byte-exact: file lines 9–67 equal the delta body (`cmp` exit 0). Duplicate-identity guard re-run at apply time: none of the four requirement headings existed anywhere under `openspec/specs/**` before creation.

## Validation gates (exact commands, exit codes)

| Gate | Command | Exit | Result |
|---|---|---|---|
| New spec strict | `openspec validate appearance-settings --type spec --strict --no-interactive` | 0 | "Specification 'appearance-settings' is valid" |
| Change-scoped strict (gate anchor) | `openspec validate sync-frontend-contracts --type change --strict --no-interactive` | 0 | "Change 'sync-frontend-contracts' is valid" |
| Archived | `openspec validate --archived --no-interactive` | 0 | 6 passed, 0 failed (all existing archives still validate; nothing archived touched) |
| Repo-wide strict | `openspec validate --all --strict --no-interactive` | **1 (expected)** | 17 passed, 1 failed — see foreign-change accounting |
| Whitespace | `git diff --check` | 0 | empty output |
| Changed paths | `git status --porcelain` / `git diff --name-status` | 0 | only the two modified canonicals + new untracked `openspec/specs/appearance-settings/` + this change folder's `apply-progress.md`; foreign untracked change pre-existing and untouched |

### Foreign-change accounting (repo-wide `--all --strict`, exit 1)

In that same run's output: `change/sync-frontend-contracts` → `✓`; every canonical spec, including the new `spec/appearance-settings`, → `✓` (all 15 specs pass); the only failing item is `change/resolved-requester-confirmation` (✗) — the known foreign untracked change outside this change's scope (it has no `specs/` directory and touches none of the three domains). No other failing item exists → gate PASSES under the design's accounting rule.

### Duplicate-heading scan

`grep '^### Requirement:' | sort | uniq -d` and same for `^#### Scenario:` on all three canonical files: zero duplicates in each file. `openspec --all --strict` passing all 15 specs independently corroborates.

## TDD / runtime evidence

Strict TDD is declared in `openspec/config.yaml`, but this change is docs-only by design (zero runtime, template, CSS, JS, test, golden, migration, skill, or CI files). Runtime harness: **N/A (docs-only)** — matches the sibling precedent `2026-08-30-sync-workflow-polish-contracts` (T2–T5 all record `Evidence: runtime harness N/A`). No Go test/build/Playwright command applies; the spec-scoped `openspec validate` gates above are the evidence.

## Workload / PR boundary

Docs-only, 3 canonical spec paths + this change folder. Well under the 400-line budget (net +46 lines across the two modified canonicals, +67-line new file, +this progress file). Single PR slice; no chained PRs needed. Parent owns commits, attempts, verify, and archive; nothing committed or archived here.

## Deviations from design

None in substance. One non-substantive note:
1. The delta block's trailing blank line was normalized to exactly one trailing newline so canonical separator conventions (tm: blank line before the next requirement; ct: preserved no-blank-line quirk) hold; this is the design's own stated intent, verified byte-exact at both junctions.
2. None other. The delegation brief's "7 tasks" matches the 7 `[x]` checkbox items in `tasks.md` (T0 carries three checkboxes; T1–T4 one each) — no discrepancy.

## Remaining tasks

None for apply. Next phase per the dependency graph: `verify` (fresh evidence, native `gentle-ai.verify-result/v1` envelope per design's verify plan), then `archive`.

## Key Learnings

- Byte-exact region replacement is safest done by mechanical extraction (`awk` between heading anchors) + `sed`-sliced head/tail + `cmp` proofs of everything *outside* the region — never by retyping content or hand-merging.
- `-U0` diff hunks fragment within one contiguous replacement; the real invariant is "all changed old-line ranges fall inside the replaced region", not "exactly one hunk string".
- Trailing-newline normalization of an extracted block must be decided *before* splicing, because it determines whether the following requirement keeps a blank separator or an existing no-blank-line quirk.
- `cmp` failures are usually a wrong comparison window (off-by-one from a trailing blank line counted or not), not file corruption — check window boundaries before suspecting the splice.
- For docs-only SDD changes, spec-scoped `openspec validate --strict` is a legitimate substitute for runtime TDD evidence when the change's design explicitly defines it as the gate anchor and a sibling archive sets the precedent.
