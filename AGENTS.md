# Project Agent Instructions

## Skills index

Load relevant skills BEFORE writing code. Skills encode project rules that are cheaper to respect than to fix later. Loading a skill after the change is designed means reworking the change.

### Naming convention

Repository-specific skills use the `tkt-` prefix (for example, `tkt-e2e`). Shared/global skills keep their canonical names without the prefix. The prefix marks what belongs to this repository, so an agent can tell project rules from reusable ones at a glance.

### How to use

1. Before starting a change, scan the trigger column below and load every skill whose trigger matches the planned work.
2. Follow the loaded skill's activation contract: some skills activate for any change, others only for template, E2E, or spec-impacting work.
3. When two skills apply, honor both. If their rules ever conflict, stop and surface the conflict instead of picking one silently.
4. Keep skills current: when a new rule becomes authoritative, update the skill file and this index in the same change.

### Local skills

| Skill | Load when | Path |
| --- | --- | --- |
| `tkt-issue-governance` | Starting any change, creating or labeling a GitHub issue, or transitioning from planning to implementation. Issue-first: no issue, no work; no `status:approved`, no implementation. Taxonomy and decision gates are authoritative. | `.agents/skills/tkt-issue-governance/SKILL.md` |
| `tkt-ux-ui` | Modifying templates, CSS, layout, visual components, responsive behavior, or accessibility. Activation, visual-preservation, and decision-boundary rules are authoritative. | `.agents/skills/tkt-ux-ui/SKILL.md` |
| `tkt-e2e` | Implementing or changing a visible feature, modifying a critical journey, fixing a browser-observable bug, or adding/updating E2E coverage. Activation contract, regression rule, and decision gates are authoritative. | `.agents/skills/tkt-e2e/SKILL.md` |
| `tkt-openspec-change-governance` | Adding or modifying observable behavior, domain rules, authorization, endpoints, forms, flows, persistence, or contracts; implementing an OpenSpec proposal or delta; preparing close or archive of an OpenSpec change. Activation and exclusion rules are authoritative. | `.agents/skills/tkt-openspec-change-governance/SKILL.md` |

### Workflow gates (issue-first)

1. Every change (feature, bug, docs, chore) needs a GitHub issue BEFORE implementation starts. No issue, no work.
2. Implementation MUST NOT begin until the issue carries `status:approved` (all types, not just features).
3. Labels come only from the canonical taxonomy in `tkt-issue-governance`; never invent new ones. If a needed label is missing, block and ask the maintainer.
4. When work is delivered, PRs link back to the governing issue.
