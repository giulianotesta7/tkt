---
name: design-system
description: "Trigger: templates, CSS, layout, visual components, responsive behavior, accessibility, or visible HTMX interactions. Govern TKT's design system: its ladders, its components, and the machine checks that enforce them."
license: MIT
metadata:
  author: "giulianotesta7"
  version: "1.0"
---

## Activation Contract

Activate when the work modifies:

- HTML templates under `web/templates/`;
- CSS, layout, spacing, or design tokens;
- visual components, the sidebar rail, or navigation;
- responsive layout or breakpoint behavior;
- keyboard navigation, focus, or visible focus indicators;
- WCAG semantics, roles, or ARIA attributes;
- HTMX-driven visible interactions (swaps, loading states, error feedback).

Do NOT activate for backend logic with no template or CSS change, documentation-only
changes, or tooling and CI work with no visual impact.

## The system

### Canvas and surfaces

White. Separation is drawn with a hairline, never with a fill. `--bg`, `--surface` and
`--card` are all white; a raised surface is white plus a border. A tinted neutral
standing in for a rung is the defect this system replaced.

### One ink ladder, neutral, alpha black

| Token | Value | Role |
| --- | --- | --- |
| --ink | 92% alpha black | body text |
| --ink-soft | 76% | values, softer body |
| --ink-strong | pure black | headings |
| --muted | 62% | labels, metadata, help text |
| --faint | 56% | nonessential glyphs |
| --line, --line-strong | 10%, 18% | hairline, strong border |
| --hover, --pressed | 5%, 9% | interaction fills |

Set ink with alpha rather than the opacity property: on a container, opacity fades every
descendant and pushes supporting text below readable contrast.

The muted and faint rungs sit at 62% and 56% because the band below them does not reach
4.5:1 on white. Both carry real reading text — section labels, table headers, timestamps,
help copy. Before this system, muted measured 3.32:1 on its own tinted canvas and faint
2.43:1. Lift a rung rather than accepting the band when real content lands there.

### One accent

`--accent` is the only brand colour, for the primary action and the focus ring.
`--accent-soft` is its 10% fill and `--accent-strong` its hover. Not for decorative bars,
not for a word inside a data column, not for a second control of equal weight.

### Inverted surfaces carry their own ladder

The rail and the auth presentation panel are deliberately dark and use the `--rail-*`
tokens: `--rail`, `--rail-ink`, `--rail-ink-soft`, `--rail-line`, `--rail-hover` and
`--rail-surface`. Never put an alpha-black ladder on a near-black canvas; the same alpha
buys more contrast there and makes supporting text louder instead of quieter.

### Semantic state survives every pass

`--green`, `--amber` and `--red` with their soft fills encode ticket outcome.
`--priority-medium` with `--green`, `--red` and `--red-strong` encode the four priority
levels. Destructive actions use `--red`. None of these are chrome: neutralising one, or
flattening a rung onto its neighbour, destroys a distinction the interface relies on.
Medium priority keeps its bright step precisely because the darker amber reads the same
as high.

`--internal-comment-bg` is an administrator's instance setting rather than chrome. It
renders a timeline affordance and must be honoured exactly as configured.

### Type

One family, vendored and embedded, and it must actually load: declaring a family is not
loading one. Three dead declarations once sat behind a fallback that differed per
operating system.

- Scale: 12px is the metadata floor, 13px labels and controls, 14px body, headings 20px
  and 24px with a 20px step at the mobile breakpoint.
- Weights: 400, 500 and 600 only.
- Headings must declare their weight. `h1` to `h4` inherit the browser's bold otherwise,
  which is a rung the ladder does not contain.
- Chrome is sentence case. No forced uppercase, and no all-lowercase chrome either. The
  source strings are already sentence case, so removing a transform publishes what the
  authors wrote; where the capitals live in the markup, fix the string.

### Shape

Three radii plus two exceptions: 8px for controls, 12px for shells and cards, 6px for
chips, 999px for pills used by badges and filters, and 50% for a person's mark. A circle
means a person; an 8px square means a control. Two shapes for one concept is the defect.

### One button contract

Every button family — `btn`, `page-action`, the `users-*-action` set, and dialog buttons —
resolves to the same metrics: 36px tall, 8px radius, 14px horizontal padding, 13px at
weight 500. The small variant is 30px with 10px padding and 12px text. Do not add a fourth
family; add a modifier to an existing one.

## The checks that enforce this

Point at these instead of restating them, because they change and prose does not.

| Source | What it enforces |
| --- | --- |
| `internal/adapters/http/golden_test.go` | The inline stylesheet is frozen once in `internal/adapters/http/testdata/stylesheet.golden`. A CSS change rewrites that one file and never a page snapshot, since page snapshots compare markup only. Regenerate with the update flag, then run again without it. |
| `internal/adapters/http/handlers_category_workflows_test.go` | Pins literal declarations in `web/templates/static/users.css`. A design change necessarily rewrites them: keep every assertion, re-point the values, never delete one to make the suite pass. |
| `internal/adapters/http/handlers_amendment4_test.go` | Rejects any rendered line that is whitespace-only or ends in a space or tab. |
| `e2e/tests/helpers/layout.ts` | Every canonical screen asserts that nothing is clipped and that the tokens and the vendored font are live. A new surface inherits both by being added to the baseline table. |
| `.github/workflows/` | The checks that actually run. The E2E job runs the Biome formatter first, deliberately, so an unformatted change fails in seconds rather than minutes. |

### The inline stylesheet cannot hold comments

`web/templates/partials/styles.html` is inlined into every page, and Go's template engine
strips CSS comments inside a style element, leaving the indentation behind as a
whitespace-only line — which the check above rejects. It also rejects any line ending in a
space or tab, so a trailing comment fails too. Put rationale in the external stylesheets,
in the commit message, or in the task record.

### Backticks plus a slash are read as a path

`internal/references` treats a backticked slash-shaped token as a repository path and
fails when it does not resolve. Token names are safe; a CSS value written as a ratio is
not. Keep literal values out of backticks.

## Verify by measuring, not by looking

A page-scale screenshot cannot be trusted for a mark a few pixels wide or a hairline at ten
percent ink, and downscaled captures lie. Two findings during the redesign were wrong until
the computed style was read: a metric card that looked borderless was identical to its
siblings, and three bars that looked blue were already neutral.

- Read the computed value for any state. A hover, focus or pressed rule can lose a
  specificity contest and never fire, which no screenshot reveals.
- Measure clipping as scroll dimensions against client dimensions, and only where the
  element's own overflow is hidden or clipped. With overflow visible the content spills and
  stays readable.
- Validate contrast against the surface a value actually lands on, not against the page.
- Give the narrow viewport its own pass: padding and insets written for a wide column are a
  large fraction of a phone's width.

## Remediation order

Prefer earlier moves over later ones:

1. Page flow, hierarchy, wayfinding, grouping, and the mapping from control to content.
2. Layout, when the composition blocks those goals.
3. Ornament, redundant surfaces, duplicated labels.
4. Spacing, alignment, density.
5. Type, measure, tracking, line height.
6. Tokens, borders, radii, elevation — as shared tokens, never as isolated values.
7. Control states: focus, hover, pressed, disabled, loading, empty, error.
8. Motion, and only after the static hierarchy works.

## Guardrails

- Do not decide where a feature goes, what information to show, or how it behaves. Those
  come from the governing issue or an unambiguous existing pattern; when they are
  undefined, stop and report instead of inventing them.
- Do not remove a capability to make a surface look calmer.
- Do not make destructive and neutral actions indistinguishable.
- Do not quiet a disclosure below the contrast its publisher chose.
- Before changing a token, find every consumer and check what each one uses it for. One
  value that quiets a fill can shout as a border.

## References

- `web/templates/partials/styles.html` — the shared inline stylesheet and the token block.
- `web/templates/static/users.css` — the second stylesheet, folded onto the same tokens.
- `web/templates/static/ticket_metrics.css` — the metrics surface.
- `../engram-governance/SKILL.md` — behavioral artifact governance.
