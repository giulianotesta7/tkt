# Archive Report: Refactor Initial Onboarding

- **Change**: `refactor-initial-onboarding`
- **Archived**: `2026-08-14` → `openspec/changes/archive/2026-08-14-refactor-initial-onboarding/`
- **Artifact store**: OpenSpec (file-based)
- **Archive mode**: standard (no warnings override; no partial archive)

## Final State at Close

| Gate | Result |
|------|--------|
| Implementation tasks | 9/9 complete, none unchecked |
| Verification | `verdict: pass` — 4/4 requirements, 10/10 scenarios, 0 blockers, 0 CRITICAL findings (`go test ./... -count=1` exit 0) |
| Native review receipt | `reviewGate: allow` — lineage `review-69d3e3bff7bd911c`, high tier, 4R lenses (review-risk, review-resilience, review-readability, review-reliability), `terminal_state: approved`, `evidence_outcome: passed` |
| Delivery | PR #34, branch `feat/initial-onboarding-refactor`, commit `6b43aec` ("feat(auth): rebrand setup and login entry experience") |

## Specs Synced to Source of Truth

- **Domain**: `auth-entry-experience` — new capability, no prior main spec.
- **Action**: Created `openspec/specs/auth-entry-experience/spec.md` by mechanical `cp` (full-spec copy, not a delta merge; no ADDED/MODIFIED/REMOVED/RENAMED deltas applied). Verified byte-identical via `diff -r` (empty output, exit 0).
- `rules.archive` ("Warn before merging destructive deltas"): not triggered — additive new capability, no destructive merge.

## Archive Integrity (Mechanical Copy Contract)

- Pre-move recursive snapshot taken under `mktemp -d` before the move.
- Move performed with `git mv` (files git-tracked); source directory confirmed absent afterward.
- `diff -r` snapshot vs archived folder: **empty output, exit 0** — byte-identical.
- `archive-report.md` is additive-only and excluded from the comparison (it did not exist in the source snapshot).

## Archived Artifacts

- proposal.md — present
- exploration.md — present
- specs/auth-entry-experience/spec.md — present
- design.md — present
- tasks.md — present (9/9 tasks complete, no unchecked implementation tasks)
- apply-progress.md — present (intermediate snapshot; superseded by final state above)
- verify-report.md — present (verdict: pass; authoritative for verification)
- archive-report.md — this file

## Non-Blocking Follow-ups (from 4R review, WARNING severity — NOT fixed, do not block this archive)

Two WARNING findings were admitted by the native review and remain open as follow-ups. They are not CRITICAL, were not treated as blockers, and were not fixed before archive:

1. **R2-001 (readability)** — `internal/adapters/http/golden_test.go:65-66`: obsolete RED-state comment on the auth golden tests; it incorrectly claims snapshots are absent and must be approved without the `-update` mechanism, obscuring the current maintenance workflow.
2. **R3-001 (reliability)** — `web/templates/partials/styles.html:199-247`: desktop grid minimum columns total 1260px while the single-column override applies only at `max-width: 900px`, leaving the 901–1259px viewport band with clipped content (masked by `overflow-x: clip`); the 901–1259px boundary is unproved by responsive evidence (1440/900/390 only).

Recommended handling: address both in a follow-up change; re-run `sdd-verify` and the review pipeline for any fix that touches templates/CSS.

## Sources Consulted

- `verify-report.md` — verification verdict and counts (authoritative).
- `.git/gentle-ai/review-transactions/v2/review-69d3e3bff7bd911c/review-receipt.json` — review gate receipt (`terminal_state: approved`, `evidence_outcome: passed`).
- `.git/gentle-ai/review-transactions/v2/review-69d3e3bff7bd911c/reviewer-results/02-review-readability.json` — finding R2-001.
- `.git/gentle-ai/review-transactions/v2/review-69d3e3bff7bd911c/reviewer-results/03-review-reliability.json` — finding R3-001.
- `tasks.md` — task completion state.
- Orchestrator final-state facts (PR #34, commit `6b43aec`) — delivery evidence.

## Cycle Status

SDD cycle complete: planned, implemented, verified, reviewed, delivered, and archived.
