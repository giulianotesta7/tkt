---
name: pr-governance
description: "Trigger: creating, preparing, or reviewing a pull request in this repository. Apply this repository's own PR rules: issue linkage with no approval label, the branch prefixes actually in use, and the checks that really run."
license: MIT
metadata:
  author: "giulianotesta7"
  version: "1.0"
---

## Activation Contract

Run when a TKT change:

- is about to be pushed on a branch that will become a pull request;
- needs a pull request created, edited, or prepared for review;
- reviews whether an open pull request meets this repository's rules.

Do NOT run for:

- read-only Q&A with no repository change;
- local work that will not become a pull request.

## Precedence

The generic `branch-pr` (user scope) and `gentle-ai-branch-pr` (package) skills
describe the gentle-pi repository, not this one. They require shellcheck over
`scripts/*.sh`, plus `type:refactor` and `type:breaking-change` labels, plus three
PR-validation workflow checks. None of those exist here. `AGENTS.md` makes project
skills authoritative: apply this file and do not reconcile the two.

## Hard Rules

- Every pull request links its governing issue with `Closes #<n>`. The issue is
  required by `.agents/skills/issue-governance/SKILL.md`; it needs no
  `status:approved` label and no approval step.
- Labels on the pull request are optional. Do not add a `type:*` label to satisfy a
  rule; the governing issue's labels carry the classification.
- The pull request body follows `.github/PULL_REQUEST_TEMPLATE.md`.
- Every required check must pass before merge. The check definitions live under
  `.github/workflows/`; point at them and do not restate them.

## Branch Naming

Format: type/slug — lowercase, no spaces.

| Prefix | Use for |
| --- | --- |
| feat/ | user-facing capability |
| fix/ | incorrect behavior |
| chore/ | tooling, skills, repository configuration |
| test/ | test-only change |
| docs/ | documentation only |

These are the prefixes this repository actually uses; do not invent others.
Include the governing issue number in the slug when one exists, so a feature
branch for issue 123 reads feat/123-metrics-link-sync.

## What Proves A Pull Request Here

Point at the sources instead of repeating them, because duplicated rules drift:

- `.github/workflows/` — the checks that actually run
- `.agents/skills/go-tests/SKILL.md` — the Go test layers
- `.agents/skills/e2e-playwright/SKILL.md` — browser regression coverage
- `.agents/skills/issue-governance/SKILL.md` — issue requirement and label taxonomy

There is no shellcheck step, no scripts directory, and no PR-validation workflow
in this repository. Never report a check as missing when it was never configured.

## Commit Format

Conventional commits with a scope, matching the history: chore(skills) and
feat(tickets) are the usual shapes.

Pull requests are squash-merged, so the pull request title becomes the commit
message on the main branch. Write the title as a valid conventional commit and do
not rely on the individual branch commits surviving.

## Output Contract

Return:

- `pr`: number/URL of the pull request, or "none";
- `issue_linked`: true / false — with the body line that links it;
- `branch_valid`: true / false — with the prefix that broke the naming rule;
- `gate`: GO or BLOCKED — BLOCKED when the pull request does not link its
  governing issue, or when a required check fails or is pending.

## References

- `AGENTS.md` — skill registration, workflow gates, and skill precedence.
- `.agents/skills/issue-governance/SKILL.md` — issue requirement, label taxonomy, and decision gates.
- `.github/PULL_REQUEST_TEMPLATE.md` — the pull request body contract.
