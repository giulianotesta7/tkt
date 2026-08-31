---
name: tkt-issue-governance
description: "Trigger: creating an issue, starting a feature, fixing a bug, labeling, planning work, or any change to the tkt repository. Enforce issue-first workflow with the canonical type/area/status label taxonomy before any implementation."
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

- This repository is issue-first: every change (feature, bug, docs, chore) MUST have a GitHub issue BEFORE implementation starts. No issue, no work.
- Implementation MUST NOT begin until the issue has `status:approved` (applies to all types, not just features).
- Labels come ONLY from the taxonomy below; the repository contains EXACTLY these 12 labels. Never invent new labels; if a needed label is missing, BLOCK and ask the maintainer instead of creating it.

### Label Taxonomy

| Group | Rule | Values |
| --- | --- | --- |
| Type | exactly ONE | `type:bug` — incorrect behavior; `type:feature` — visible functionality or improvement; `type:docs` — documentation only; `type:chore` — CI, tooling, tests, skills, templates, or maintenance with no functional change. |
| Area | ONE primary, at most TWO | `area:auth`, `area:tickets`, `area:users`, `area:categories-workflows`, `area:desks`, `area:settings`, `area:tooling`. |
| Status | only when applicable | `status:approved` — the maintainer authorized implementation to start. |

- `area:tooling` covers CI, Playwright, skills, OpenSpec governance, and repository configuration.
- Cardinality rules apply to the GOVERNING issue. PR labels are optional and do not need to duplicate the issue's labels.
- If no area fits a proposed issue, the issue is probably mis-scoped: BLOCK and clarify before labeling.

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

The feature request template applies the `[Feature] ` prefix automatically. Apply the other prefixes manually when drafting an issue. Do not bulk-edit existing issues to add prefixes; existing titles may keep their current format.

## Decision Gates

| Condition | Result |
| --- | --- |
| No issue exists for the requested work | BLOCKED — create the issue first, propose title/body/labels, wait for maintainer approval flow |
| Issue lacks a `type:*` label or has more than one | BLOCKED — fix labeling before any work |
| Issue has zero areas or more than two | BLOCKED — fix labeling before any work |
| No `status:approved` on the issue | BLOCKED — implementation must wait for maintainer authorization |
| `status:approved` present and labels valid | GO — proceed (respect other project skills) |

## Execution Steps

1. **Check tracking**: search open issues for the requested work. If found, work from that issue; if not, draft one (title with the canonical prefix, motivation, acceptance criteria).
2. **Propose labels**: pick exactly one `type:*`, one primary `area:*` (max two), and no `status:*` yet.
3. **Wait for approval**: do not implement until the maintainer adds `status:approved`.
4. **Keep labels truthful**: if scope changes mid-work and a different type/area applies, update the issue labels before continuing.
5. **Close the loop**: when the work is delivered, the issue is the reference for the change; PRs link back to it.

## Output Contract

Return:

- `issue`: number/URL of the governing issue, or "none";
- `labels_valid`: true / false — with the exact taxonomy violation if false;
- `approved`: true / false — whether `status:approved` is present;
- `gate`: GO or BLOCKED — with the reason if blocked.

## References

- `../../../AGENTS.md` — project skill registration (this skill is listed there).
- GitHub labels: `gh label list --repo giulianotesta7/tkt`.
