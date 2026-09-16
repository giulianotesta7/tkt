# Project Agent Instructions

## Skills index

Load relevant skills BEFORE writing code. Skills encode project rules that are cheaper to respect than to fix later. Loading a skill after the change is designed means reworking the change.

### How to use

1. Before starting a change, scan the trigger column below and load every skill whose trigger matches the planned work.
2. Follow the loaded skill's activation contract: some skills activate for any change, others only for template, E2E, or spec-impacting work.
3. When two skills apply, honor both. If their rules ever conflict, stop and surface the conflict instead of picking one silently.
4. Keep skills current: when a new rule becomes authoritative, update the skill file and this index in the same change.

### Local skills

| Skill | Load when | Path |
| --- | --- | --- |
| `issue-governance` | Starting any change, creating or labeling a GitHub issue, or transitioning from planning to implementation. Issue-first: no issue, no work; no `status:approved`, no implementation. Taxonomy and decision gates are authoritative. | `.agents/skills/issue-governance/SKILL.md` |
| `ux-ui` | Modifying templates, CSS, layout, visual components, responsive behavior, or accessibility. Activation, visual-preservation, and decision-boundary rules are authoritative. | `.agents/skills/ux-ui/SKILL.md` |
| `e2e-playwright` | Implementing or changing a visible feature, modifying a critical journey, fixing a browser-observable bug, or adding/updating E2E coverage. Activation contract, regression rule, and decision gates are authoritative. | `.agents/skills/e2e-playwright/SKILL.md` |
| `engram-governance` | Adding or modifying observable behavior, domain rules, authorization, endpoints, forms, flows, persistence, or contracts; governed work; durable decisions, bugfixes, discoveries, configuration or preference changes; memory recall; or session closure. Use native project-scoped Engram search, alignment, and durable bookkeeping. | `.agents/skills/engram-governance/SKILL.md` |

### Behavioral change records in Engram

For behavior-changing work, load `engram-governance`. Scope reads and writes to `project: "tkt"`, then locate canonical requirements and active deltas with descriptive context and search. Existing migration memories remain preserved history, not operational prerequisites.

Engram is machine-local unless a maintainer exports and imports a verified project-scoped export. CI cannot inspect it. Agents must report unavailable or conflicting requirements instead of inferring them.

### Workflow gates (issue-first)

1. Every change (feature, bug, docs, chore) needs a GitHub issue BEFORE implementation starts. No issue, no work.
2. Implementation MUST NOT begin until the issue carries `status:approved` (all types, not just features).
3. Labels come only from the canonical taxonomy in `issue-governance`; never invent new ones. If a needed label is missing, block and ask the maintainer.
4. When work is delivered, PRs link back to the governing issue.
