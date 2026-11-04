# tkt — Agent Skills Index

Before contributing, read [CONTRIBUTING.md](CONTRIBUTING.md) for the issue-first workflow.

Before writing code or starting a change, load the relevant skills below.

## How to Use

1. Match the planned work against the triggers and load every applicable skill.
2. Read each skill file; its Activation Contract determines whether it applies.
3. Follow the rules of all applicable skills. Multiple skills may apply to one change.
4. Repository-local skills take precedence over conflicting user-scope or package-installed skills. If local skills conflict and both cannot be honored, stop and surface the conflict.

Keep skills and this index current: when a new rule becomes authoritative, update the skill file and this index in the same change.

## Skills

| Skill | Trigger | Path |
| --- | --- | --- |
| `issue-governance` | Starting a change; creating, labeling, or triaging issues | `.agents/skills/issue-governance/SKILL.md` |
| `pr-governance` | Preparing, creating, or reviewing a pull request | `.agents/skills/pr-governance/SKILL.md` |
| `design-system` | Templates, styling, layout, accessibility, or visible HTMX interactions | `.agents/skills/design-system/SKILL.md` |
| `worktree-isolation` | Before the first write of any change | `.agents/skills/worktree-isolation/SKILL.md` |
| `e2e-playwright` | Visible changes, critical journeys, browser-observable bugs, or E2E coverage | `.agents/skills/e2e-playwright/SKILL.md` |
| `go-tests` | Go tests or tested packages, domain rules, authorization, migrations, golden fixtures, or Go CI gates | `.agents/skills/go-tests/SKILL.md` |
| `container-governance` | Container image, compose, runtime contract, container CI, or image publishing | `.agents/skills/container-governance/SKILL.md` |
| `engram-governance` | Behavior changes, governed work, decisions, discoveries, memory recall, or session closure | `.agents/skills/engram-governance/SKILL.md` |
| `release-notes` | Drafting or revising release text | `.agents/skills/release-notes/SKILL.md` |
