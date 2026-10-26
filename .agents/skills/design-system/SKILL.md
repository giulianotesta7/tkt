---
name: design-system
description: "Trigger: templates, CSS, layout, visual components, responsive behavior, accessibility, or visible HTMX interactions. Extend TKT's approved design patterns, theme tokens, and measured checks."
license: MIT
metadata:
  author: "giulianotesta7"
  version: "2.0"
---

## Activation Contract

Activate for HTML templates, CSS, layout, visual components, navigation, responsive
behavior, keyboard/focus accessibility, ARIA, or visible HTMX interactions.
Skip backend-only, documentation-only, and nonvisual tooling changes.

## Hard Rules

- Read `./references/foundations.md` before visual edits; preserve its token, contrast,
  typography, shape, button, and enforcement rules.
- Extend the closest integrated pattern in `./references/patterns.md`; mockups and
  historical decisions do not override current production behavior.
- Use theme-aware tokens for surfaces, ink, accent, semantic states, elevation, and
  scrims. Check light and dark themes; white is the light canvas, not a global rule.
- Prioritize flow, hierarchy, grouping, and control-to-content mapping before ornament.
  Avoid redundant containers, labels, and explanatory copy.
- Preserve capabilities, permissions, configured distinctions, validation, submission
  semantics, keyboard access, and existing no-script paths.
- Scope screen-specific departures. Find every consumer before editing shared classes
  or tokens; a Users drawer change must not silently restyle a desk drawer.
- Treat screen geometry and field choices as local examples, not universal mandates.

## Decision Gates

| Situation | Action |
| --- | --- |
| An integrated pattern fits | Reuse its structure and existing component family. |
| Placement, content, or behavior is undefined | Ask; do not invent a product rule. |
| A calmer layout would remove a capability or warning | Keep it; fix hierarchy instead. |
| Shared styling must change | Inspect consumers and verify affected surfaces. |
| Screenshot suggests a token or clipping defect | Measure computed styles and dimensions before changing it. |

## Execution Steps

1. Identify the governing requirement, closest pattern, and affected shared consumers.
2. Apply the remediation order in the foundations; keep primary, secondary, and
   destructive actions distinguishable.
3. Check narrow and wide layouts, both themes, and applicable hover, focus, pressed,
   disabled, loading, empty, error, and unsaved states.
4. Run applicable golden, layout, and browser checks through the referenced project
   skills. Read computed styles, font loading, actual-surface contrast, and overflow;
   a screenshot alone is not proof.

## Output Contract

Report reused patterns, changed surfaces and shared consumers, exact checks and results,
and unresolved design decisions or missing evidence.

## References

- `./references/foundations.md` — visual contracts, remediation order, and enforcement.
- `./references/patterns.md` — integrated composition and interaction examples.
- `../e2e-playwright/SKILL.md` — browser regression requirements.
- `../go-tests/SKILL.md` — applicable Go and golden checks.
- `../engram-governance/SKILL.md` — behavioral requirements and decision records.
