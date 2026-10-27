---
name: e2e-playwright
description: "Trigger: visible features, critical journeys, browser-observable bugs, or E2E coverage. Maintain versioned Playwright regression tests."
license: MIT
metadata:
  author: "giulianotesta7"
  version: "1.6"
---

## Activation Contract

Activate for changes to visible screens or interactions, including cosmetic changes, as well as critical journeys, browser-observable bugs, user-facing behavior review, or E2E coverage. Do not activate for backend-only refactors, Go-only tests, CI-only changes, or refactors with no effect on the rendered frontend.

## Hard Rules

- Read the relevant Engram behavioral artifacts, actual routes, approved decisions, and existing journey ownership before assuming behavior.
- Use `server-lifecycle.ts` with an isolated temporary SQLite database and free loopback port. Choose either `seed: false` or `seed: true` per `test.describe`.
- For every change to a visible screen or interaction, use Playwright CLI to open the affected screen on an isolated server and verify the change and its relevant interaction or state. Check a narrow viewport when responsive behavior is affected. Inspect accessibility, console, and relevant requests; report what was checked and the results. This browser verification is required even when lower-layer tests provide equivalent automated coverage.
- Use Playwright CLI exploration before adding assertions.
- Keep fixtures outside the behavior under test. Keep shared data read-only and tests independent.
- Resolve the exact requested entity or fail with entity, selector, and `page.url()`. Never fall back to the first entity.
- Give each behavior one canonical journey. Update an existing test rather than creating a duplicate or debug spec.
- An HTMX assertion must prove `HX-Request: true`, exact endpoint, method, and status; changed target `innerHTML`; zero main-frame navigation; unchanged `h1` chrome; and the URL or `hx-push-url` contract. Assert the visible domain result separately. Never rely on `hx-*` attributes alone, broad statuses, bypasses, optional assertions, or silent catches. Native forms use ordinary navigation assertions.
- A legitimate `hx-swap="none"` autosave exception may use `assertHtmxNoSwap`: prove `HX-Request: true`, exact endpoint and query, method, status, zero main-frame navigation, and unchanged URL. Do not require target HTML mutation. Assert the persisted effect later.
- Preserve the distinction between structural baselines, representative functional journeys, Go-owned exhaustive validation and authorization, and unused visual regression. Do not claim full frontend coverage from baselines alone.

## Decision Gates

| Condition | Action |
| --- | --- |
| Engram behavioral artifacts conflict with implementation | Block and report the discrepancy. |
| Unit or integration tests provide equivalent automated confidence | Prefer the lower layer for automated coverage; still perform the required browser verification for visible changes. |
| Browser, runtime, or isolated server cannot run | Mark browser verification blocked and report required checks with sanitized evidence; never claim it passed. |
| The journey already exists | Update the canonical test. |

## Execution Steps

1. Consult `e2e/README.md`, Engram behavioral artifacts, actual routes, approved decisions, and existing journey ownership.
2. Start the isolated server and inspect the browser with the CLI.
3. Prepare fixtures with seed or helpers, then update the smallest existing test and shared helper.
4. Run the affected spec and the full `npm test` suite from `e2e/`. Preserve trace, screenshot, and report on failure.
5. Close browser sessions and stop the server.

## Output Contract

Report exact files changed, the screens, states, interactions and viewports checked with Playwright CLI, relevant console and request findings, focused and full-suite commands with results, runtime cleanup, unresolved issues, and whether coverage is structural or functional. Explicitly report blocked browser verification when it could not be performed.

## References

- `../../../e2e/README.md`
- `../../../e2e/server-lifecycle.ts`
- `../../../e2e/tests/helpers/htmx.ts`
- `../../../e2e/tests/helpers/network.ts`
- `../../../e2e/tests/helpers/navigation.ts`
- `../engram-governance/SKILL.md` — behavioral artifact governance.
