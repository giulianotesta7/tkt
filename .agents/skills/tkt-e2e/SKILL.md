---
name: tkt-e2e
description: "Trigger: visible features, critical journeys, browser-observable bugs, or E2E coverage. Maintain versioned Playwright regression tests."
license: MIT
metadata:
  author: "giulianotesta7"
  version: "1.5"
---

## Activation Contract

Activate for visible features, critical journeys, browser-observable bugs, user-facing behavior review, or E2E coverage. Do not activate for backend-only refactors, Go-only tests, CI-only changes, or cosmetic changes covered with equivalent lower-layer tests.

## Hard Rules

- Read the relevant OpenSpec spec and actual routes before assuming behavior.
- Use `server-lifecycle.ts` with an isolated temporary SQLite database and free loopback port. Choose either `seed: false` or `seed: true` per `test.describe`.
- Use Playwright CLI exploration before adding assertions. Inspect accessibility, console, and relevant requests.
- Keep fixtures outside the behavior under test. Keep shared data read-only and tests independent.
- Resolve the exact requested entity or fail with entity, selector, and `page.url()`. Never fall back to the first entity.
- Give each behavior one canonical journey. Update an existing test rather than creating a duplicate or debug spec.
- An HTMX assertion must prove `HX-Request: true`, exact endpoint, method, and status; changed target `innerHTML`; zero main-frame navigation; unchanged `h1` chrome; and the URL or `hx-push-url` contract. Assert the visible domain result separately. Never rely on `hx-*` attributes alone, broad statuses, bypasses, optional assertions, or silent catches. Native forms use ordinary navigation assertions.
- A legitimate `hx-swap="none"` autosave exception may use `assertHtmxNoSwap`: prove `HX-Request: true`, exact endpoint and query, method, status, zero main-frame navigation, and unchanged URL. Do not require target HTML mutation. Assert the persisted effect later.
- Preserve the distinction between structural baselines, representative functional journeys, Go-owned exhaustive validation and authorization, and unused visual regression. Do not claim full frontend coverage from baselines alone.

## Decision Gates

| Condition | Action |
| --- | --- |
| OpenSpec conflicts with implementation | Block and report the discrepancy. |
| Unit or integration tests provide equivalent confidence | Prefer the lower layer. |
| Runtime or isolated server cannot run | Block and report required journeys with sanitized evidence. |
| The journey already exists | Update the canonical test. |

## Execution Steps

1. Consult `e2e/README.md`, OpenSpec, routes, and existing journey ownership.
2. Start the isolated server and inspect the browser with the CLI.
3. Prepare fixtures with seed or helpers, then update the smallest existing test and shared helper.
4. Run the affected spec and the full `npm test` suite from `e2e/`. Preserve trace, screenshot, and report on failure.
5. Close browser sessions and stop the server.

## Output Contract

Report exact files changed, focused and full-suite commands with results, runtime cleanup, unresolved issues, and whether coverage is structural or functional.

## References

- `../../../e2e/README.md`
- `../../../e2e/server-lifecycle.ts`
- `../../../e2e/tests/helpers/htmx.ts`
- `../../../e2e/tests/helpers/network.ts`
- `../../../e2e/tests/helpers/navigation.ts`
- `../../../openspec/`
