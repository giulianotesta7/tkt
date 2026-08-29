# Project Agent Instructions

## Project Skills

- Load `.agents/skills/issue-governance/SKILL.md` when starting any change, creating or labeling a GitHub issue, or transitioning from planning to implementation. This repository is issue-first: no issue, no work; no `status:approved`, no implementation. Its taxonomy and decision gates are authoritative.

- Load `.agents/skills/ux-ui/SKILL.md` when modifying templates, CSS, layout, visual components, responsive behavior, or accessibility. Its activation rules, visual-preservation rules, and decision-boundary rules are authoritative.
- Load `.agents/skills/tkt-e2e/SKILL.md` when implementing or changing a visible feature, modifying a critical journey, fixing a browser-observable bug, or adding/updating E2E coverage. Its activation contract, regression rule, and decision gates are authoritative.
- Load `.agents/skills/openspec-change-governance/SKILL.md` when a change adds or modifies observable behavior, domain rules, authorization, endpoints, forms, flows, persistence, or contracts; implements an OpenSpec proposal or delta; or prepares close or archive of an OpenSpec change. Its activation and exclusion rules are authoritative.
