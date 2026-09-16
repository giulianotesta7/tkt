# Project Agent Instructions

## Skills index

Load relevant skills BEFORE writing code. Skills encode project rules that are cheaper to respect than to fix later. Loading a skill after the change is designed means reworking the change.

### How to use

1. Before starting a change, read the local skills below and load every skill whose Activation Contract matches the planned work.
2. Follow the loaded skill's activation contract: some skills activate for any change, others only for template, E2E, or spec-impacting work.
3. When two skills apply, honor both. If their rules ever conflict, stop and surface the conflict instead of picking one silently.
4. Keep skills current: when a new rule becomes authoritative, update the skill file and this index in the same change.

### Local skills

Project skills live in `.agents/skills/` and are named for the suite or layer they govern, not for this repository. Name a skill for what it proves (`go-tests`, `e2e-playwright`), not for the stack alone.

| Skill | Path |
| --- | --- |
| `issue-governance` | `.agents/skills/issue-governance/SKILL.md` |
| `ux-ui` | `.agents/skills/ux-ui/SKILL.md` |
| `e2e-playwright` | `.agents/skills/e2e-playwright/SKILL.md` |
| `go-tests` | `.agents/skills/go-tests/SKILL.md` |
| `engram-governance` | `.agents/skills/engram-governance/SKILL.md` |

Load the skill file. Its Activation Contract is authoritative for when the skill applies.

### Behavioral change records in Engram

For behavior-changing work, load `engram-governance`. Scope reads and writes to `project: "tkt"`, then locate canonical requirements and active deltas with descriptive context and search. Existing migration memories remain preserved history, not operational prerequisites.

Engram is machine-local unless a maintainer exports and imports a verified project-scoped export. CI cannot inspect it. Agents must report unavailable or conflicting requirements instead of inferring them.

### Workflow gates (issue-first)

1. Every change (feature, bug, docs, chore) needs a GitHub issue BEFORE implementation starts. No issue, no work.
2. Implementation MUST NOT begin until the issue carries `status:approved` (all types, not just features).
3. Labels come only from the canonical taxonomy in `issue-governance`; never invent new ones. If a needed label is missing, block and ask the maintainer.
4. When work is delivered, PRs link back to the governing issue.
