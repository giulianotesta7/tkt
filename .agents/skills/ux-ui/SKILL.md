---
name: ux-ui
description: "Trigger: modifying templates, CSS, layout, visual components, responsive behavior, accessibility, or visible HTMX interactions. Maintain visual and interaction consistency of the existing product."
license: MIT
metadata:
  author: "giulianotesta7"
  version: "2.0"
---

## Activation Contract

Activate when the work modifies:
- HTML templates (`web/templates/`);
- CSS, layout, spacing, or visual tokens;
- visual components, the sidebar rail, or navigation;
- responsive layout or breakpoint behavior;
- keyboard navigation, focus, or visible focus indicators;
- WCAG semantics, roles, or ARIA attributes;
- HTMX-driven visible interactions (swaps, loading states, error feedback).

Do NOT activate for:
- backend-only logic with no template or CSS change;
- OpenSpec documentation-only changes;
- CI, tooling, or infrastructure changes with no visual impact;
- E2E test changes that do not touch templates, CSS, or visual components.

## Hard Rules

Before implementing a visual or interaction change:
- Inspect comparable screens and components in the running application.
- Use the existing classes, design tokens, and component patterns. Do NOT introduce isolated visual values when an equivalent pattern already exists.
- Preserve the existing visual contract:
  - distance between sidebar rail and content area;
  - margins, padding, and spacing scales;
  - heading font size, weight, and line-height;
  - typographic hierarchy (h1→h2→h3→body→small);
  - element widths and alignment within the layout;
  - colors, borders, border-radius, and box-shadows;
  - button, input, table, card, and badge appearance;
  - hover, focus, disabled, loading, empty, and error visual states;
  - responsive behavior down to the mobile breakpoint;
  - keyboard navigation, visible focus ring, and tab order;
  - semantic HTML structure and ARIA labels for accessibility.

## What NOT to Decide

This skill does NOT decide:
- where to place a new feature or functionality;
- which interaction pattern to use;
- what information to show or hide;
- how a feature should work;
- what mobile behavior should be;
- any other material product or design decision.

Those decisions MUST come from an OpenSpec spec, an explicit instruction, an approved design, or an unambiguous existing pattern. If they remain undefined, STOP and report what is undefined.

## Browser Exploration

You MAY use Playwright CLI from the `e2e/` directory to inspect and compare the running interface:
```bash
cd e2e
npm run explore -- open http://127.0.0.1:PORT
npm run explore -- snapshot
npm run explore -- screenshot
npm run explore -- close-all
```

However, creating and maintaining versioned regression E2E tests belongs to the `tkt-e2e` skill, not this one.

## References

- `../../../web/templates/` — all HTML templates and static assets.
- `../../../web/templates/static/users.css` — primary stylesheet.
- `../../../openspec/` — specs that define visual and interaction requirements.