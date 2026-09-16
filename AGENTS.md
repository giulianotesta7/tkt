# Project Agent Instructions

## Skills index

Load relevant skills BEFORE writing code. Skills encode project rules that are cheaper to respect than to fix later. Loading a skill after the change is designed means reworking the change.

### How to use

1. Before starting a change, read the local skills below and load every skill whose Activation Contract matches the planned work.
2. Follow the loaded skill's activation contract: some skills activate for any change, others only for template, E2E, or spec-impacting work.
3. Skills under `.agents/skills/` are authoritative for this repository. When one of them conflicts with a user-scope or package-installed skill, the project skill wins: apply it and do not reconcile the two.
4. When two project skills conflict and you cannot honor both, stop and surface the conflict instead of picking one silently.
5. Keep skills current: when a new rule becomes authoritative, update the skill file and this index in the same change.

### Local skills

Project skills live in `.agents/skills/` and are named for the suite or layer they govern, not for this repository. Name a skill for what it proves (`go-tests`, `e2e-playwright`), not for the stack alone.

| Skill | Path |
| --- | --- |
| `issue-governance` | `.agents/skills/issue-governance/SKILL.md` |
| `pr-governance` | `.agents/skills/pr-governance/SKILL.md` |
| `ux-ui` | `.agents/skills/ux-ui/SKILL.md` |
| `e2e-playwright` | `.agents/skills/e2e-playwright/SKILL.md` |
| `go-tests` | `.agents/skills/go-tests/SKILL.md` |
| `container-governance` | `.agents/skills/container-governance/SKILL.md` |
| `engram-governance` | `.agents/skills/engram-governance/SKILL.md` |

Load the skill file. Its Activation Contract is authoritative for when the skill applies.

### Behavioral change records in Engram

For behavior-changing work, load `engram-governance`. Scope reads and writes to `project: "tkt"`, then locate canonical requirements and active deltas with descriptive context and search. Existing migration memories remain preserved history, not operational prerequisites.

Engram is machine-local unless a maintainer exports and imports a verified project-scoped export. CI cannot inspect it. Agents must report unavailable or conflicting requirements instead of inferring them.

### Workflow gates (issue-first)

1. Every change (feature, bug, docs, chore) needs a GitHub issue BEFORE implementation starts. No issue, no work. This is the only blocking gate.
2. Creating the issue authorizes it. There is no approval round trip: this repository has a single maintainer, so `status:approved` never gates and never blocks implementation.
3. Labels come only from the canonical taxonomy in `issue-governance`; never invent new ones. Apply them best-effort with the closest fit and note the choice; a missing or ambiguous label never blocks or delays work.
4. When work is delivered, PRs link back to the governing issue.
