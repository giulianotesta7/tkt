---
name: issue-governance
description: "Trigger: creating an issue, starting a feature, fixing a bug, labeling, planning work, or any change to the tkt repository. Require a GitHub issue for every change, self-authorized on creation, with the canonical type/area label taxonomy applied best-effort."
license: MIT
metadata:
  author: "giulianotesta7"
  version: "1.0"
---

## Activation Contract

Run when a TKT change:

- is about to start, and no issue exists yet for it;
- requires creating, labeling, or triaging a GitHub issue;
- transitions from planning to implementation;
- reviews whether work in progress is backed by a governed issue.

Do NOT run for:

- read-only Q&A with no repository change.

## Hard Rules

- This repository is issue-first: every change (feature, bug, docs, chore) MUST have a GitHub issue BEFORE implementation starts. No issue, no work. This is the ONLY blocking gate.
- Creating the issue authorizes it. This repository has a single maintainer, so no work ever waits for an approval step; `status:approved` does not gate and never blocks implementation.
- Labels come ONLY from the taxonomy below; the repository contains EXACTLY these 12 labels. Never invent new labels. Apply them best-effort with the closest fit and note the choice; a missing, ambiguous, or imperfect label never blocks or delays work.

### Label Taxonomy

| Group | Rule | Values |
| --- | --- | --- |
| Type | exactly ONE | `type:bug` — incorrect behavior; `type:feature` — visible functionality or improvement; `type:docs` — documentation only; `type:chore` — CI, tooling, tests, skills, templates, or maintenance with no functional change. |
| Area | ONE primary, at most TWO | `area:auth`, `area:tickets`, `area:users`, `area:categories-workflows`, `area:desks`, `area:settings`, `area:tooling`. |
| Status | only when applicable, never a gate | `status:approved` — optional marker. It authorizes nothing and never blocks work. |

- `area:tooling` covers CI, Playwright, skills, Engram change governance, and repository configuration.
- Cardinality rules apply to the GOVERNING issue. PR labels are optional and do not need to duplicate the issue's labels.
- If no area fits a proposed issue, pick the closest fit and note the mismatch in the issue body; never block or delay work over it.

### Issue Title Prefixes

New issue titles should start with one canonical prefix that matches the issue's `type:*` label:

| Title prefix | Label |
| --- | --- |
| `[Feature]` | `type:feature` |
| `[Bug]` | `type:bug` |
| `[Chore]` | `type:chore` |
| `[Docs]` | `type:docs` |

Example: `[Feature] Add ticket export to CSV`.

Labels remain authoritative for filtering, automation, and governance. The prefix is a human-readable aid in issue lists and search results, not a replacement for the label. A missing or wrong prefix is not a labeling violation and MUST NOT become a new decision gate; when prefix and label disagree, the label wins.

The feature request template applies the `[Feature]` prefix automatically. Apply the other prefixes manually when drafting an issue. Do not bulk-edit existing issues to add prefixes; existing titles may keep their current format.

### Why there is no approval gate

The repository has a single maintainer, so the approver and the requester are the same person: an approval step adds latency without adding a decision.

The issue itself is still required, because it is the intent anchor referenced by SDD artifacts, Engram keys, and the `Closes #` line in the pull request template. The rule is uniform across change types on purpose: any rule that has to classify the change (feature versus chore, behavior-changing or not) creates a fresh judgment gate. Do not reintroduce an approval step without an explicit maintainer decision.

## Decision Gates

| Condition | Result |
| --- | --- |
| No issue exists for the requested work | BLOCKED — create the issue first, then start; the issue authorizes itself |
| Issue exists | GO — proceed (respect other project skills) |
| Issue lacks a `type:*` label, or has more than one | GO — apply the best fit or note the correction; never block or delay |
| Issue has zero areas or more than two | GO — apply the best fit or note the correction; never block or delay |
| Issue has no `status:approved` | GO — approval is not a gate in this repository |

## Execution Steps

1. **Check tracking**: search open issues for the requested work. If found, work from that issue; if not, create one (title with the canonical prefix, motivation, acceptance criteria).
2. **Apply labels**: pick exactly one `type:*` and one primary `area:*` (max two). Best-effort is enough — a doubtful label is never a reason to wait.
3. **Implement**: creating the issue authorized the work. Start immediately; never wait for an approval step.
4. **Keep labels truthful**: if scope changes mid-work and a different type/area applies, update the issue labels before continuing.
5. **Close the loop**: when the work is delivered, the issue is the reference for the change; PRs link back to it with `Closes #<n>`.

## Output Contract

Return:

- `issue`: number/URL of the governing issue, or "none";
- `labels_valid`: true / false — with the exact taxonomy deviation if false;
- `gate`: GO or BLOCKED — BLOCKED only when no issue exists for the requested work.

## References

- `../../../AGENTS.md` — project skill registration (this skill is listed there).
- GitHub labels: `gh label list --repo giulianotesta7/tkt`.
