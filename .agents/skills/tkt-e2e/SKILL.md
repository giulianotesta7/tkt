---
name: tkt-e2e
description: "Trigger: implementing or changing a visible feature, modifying a critical journey, fixing a browser-observable bug, reviewing user-facing behavior, or adding/updating E2E coverage. Explore browser behavior and maintain versioned Playwright regression tests."
license: MIT
metadata:
  author: "giulianotesta7"
  version: "1.5"
---

## Activation Contract

Activate when the work:
- implements or changes a user-visible feature;
- modifies a critical user journey;
- fixes a bug observable from the browser;
- reviews or validates user-facing behavior;
- adds or updates E2E test coverage.

Do NOT activate for:
- backend-only refactors with no visible behavior change;
- internal test infrastructure changes (Go unit/integration only);
- CI-only configuration changes;
- documentation or copy-only changes with no behavioral impact.

## Hard Rules

- Before assuming behavior, read the relevant OpenSpec spec under `openspec/specs/` or `openspec/changes/*/specs/`. Do NOT invent expected behavior.
- Identify the affected journeys from the spec's scenarios and the application's actual routes.
- Decide whether E2E is warranted: if the behavior can be verified at the unit or integration layer with equivalent confidence, prefer the lower layer. E2E is for full-stack journeys that cross service boundaries, involve HTMX swaps, or require actual browser rendering.
- Start an isolated tkt instance with a temporary SQLite database and a free loopback port before any browser interaction. Use the shared `server-lifecycle.ts` module.

  Two modes — choose ONE per `test.describe`:

  **Empty DB (first-user setup):**
  ```typescript
  import { startServer, stopServer } from "../server-lifecycle.js";
  test.describe("First-User Setup", () => {
    test.beforeAll(async () => { await startServer({ seed: false }); });
    test.afterAll(async () => { await stopServer(); });
  });
  ```

  **Pre-seeded DB (login + ticket journeys):**
  ```typescript
  import { startServer, stopServer } from "../server-lifecycle.js";
  test.describe("Ticket Lifecycle", () => {
    test.beforeAll(async () => { await startServer({ seed: true }); });
    test.afterAll(async () => { await stopServer(); });
  });
  ```

- Use Playwright CLI from the `e2e/` directory for ad-hoc exploration before writing assertions:
  ```bash
  cd e2e
  npm run server:start:empty   # or server:start:seeded
  # URL is printed to stdout
  npm run explore -- open http://127.0.0.1:PORT
  npm run explore -- snapshot
  npm run explore -- console
  npm run explore -- requests
  npm run explore -- screenshot
  npm run explore -- close-all
  npm run server:stop
  ```
- For versioned regression: inspect the interface with accessibility snapshots/selectors, check the console for errors and relevant network requests, then create or update a test in `e2e/tests/`.
- After writing a test, run both the affected test file AND the full E2E suite (`npm test` in `e2e/`).
- On test failure, preserve the Playwright trace, screenshot, and report as failure evidence.
- Close all browser sessions (`npm run explore -- close-all`) before stopping the server.

## Regression Rule

When fixing a browser-observable bug, you MUST add or update a Playwright test that reproduces the original failure and proves the fix, before merging.

## Exclusions

- Unit or integration tests (these belong in the Go test suite under `internal/`).
- Behavioral specs that can be verified entirely through `httptest` and an in-memory store.
- E2E for trivial or cosmetic-only changes where lower-level tests provide sufficient coverage.
- Product or design decisions (these must come from OpenSpec, explicit instructions, or approved designs).

## Decision Gates

| Condition | Result |
| --- | --- |
| Diff affects no visible behavior | SKIP with reason |
| Behavior verifiable at unit/integration layer | Prefer lower layer; SKIP E2E |
| Playwright CLI or test runtime unavailable | BLOCKED after reporting required journeys |
| Isolated server cannot start within timeout | BLOCKED with sanitized logs |
| OpenSpec contradicts implementation | BLOCKED — report discrepancy before creating E2E |
| Existing test covers the affected journey | UPDATE existing test instead of creating a new one |

## How to Explore Interactively

1. Start an isolated server:
   ```bash
   cd e2e
   npm run server:start:empty
   # TKT server ready at http://127.0.0.1:PORT
   ```
   Or with pre-seeded data:
   ```bash
   npm run server:start:seeded
   ```

2. Use the Playwright CLI to explore:
   ```bash
   npm run explore -- open URL
   npm run explore -- snapshot
   npm run explore -- console
   npm run explore -- requests
   npm run explore -- screenshot
   ```

3. Clean up:
   ```bash
   npm run explore -- close-all
   npm run server:stop
   ```

## Permanent HTMX Contract

A HTMX interaction is only covered when the test demonstrates jointly:

1. A request with `HX-Request: true` header, matching the expected endpoint, method and status.
2. The hx-target region's innerHTML changed after the swap.
3. Zero document navigation requests on the main frame during the swap.
4. A non-target chrome region (h1) remained intact.
5. URL unchanged or satisfies the explicit `hx-push-url` contract.
6. The domain-visible result is also asserted (consumer responsibility).

Prohibited:
- Assuming URL unchanged proves absence of reload (navigation events must be checked).
- Using only `hx-*` HTML attributes as proof of a swap.
- Omitting `HX-Request` header validation.
- Using bypass flags, warnings, or catches that make the assertion optional.
- Accepting broad status ranges without an exact contract.
- Confusing a native form submission with an HTMX swap.

A native form submission (no `hx-post`) is tested as an ordinary navigation: request, expected navigation, final URL, visible result — never through `assertHtmxSwap`.

## Permanent False-Positive Rule

No precondition, operation, or mandatory assertion may be nullified by:
- Conditional guards that skip the assertion when a control is absent.
- Silent catches that ignore a request, response or precondition.
- Warnings that substitute for a failing assertion.
- Fallbacks to a different entity (first available link, row, or user).
- Optional checks that can pass without exercising the required behavior.

If the required contract cannot be demonstrated, the test must fail with context.

## Permanent Fixture Rule

Preconditions that are not the behavior under test must be prepared via seed or fixture helpers before the test runs. Shared data must be read-only during execution; no viewport or test may depend on another having run first. When the operation under test IS creating or modifying an entity, the UI is the correct path.

## Permanent Identity Rule

Helpers must resolve exactly the requested entity or fail, indicating entity, selector and URL. They must never silently continue using the first available entity as a fallback.

## Permanent Journey Ownership Rule

Each behavior must have exactly one canonical test. Before creating a new scenario, find and update the existing one. Cross-cutting helpers may be reused, but they do not justify duplicating entire journeys.

## Permanent Coverage Claim Rule

The skill must distinguish between:
- Structural screen baseline (every canonical route visited at both viewports, asserting URL, heading, control, overflow, observability).
- Functional journey (one representative scenario per domain, exercising the UI).
- Validations delegated to Go (exhaustive authorization, state machine, edge cases).
- Visual regression (not used in this suite).

"Full frontend coverage" may not be claimed on structural baselines alone — the distinction must be explicit.

## Decision Gates

| Condition | Result |
| --- | --- |
| Diff affects no visible behavior | SKIP with reason |
| Behavior verifiable at unit/integration layer | Prefer lower layer; SKIP E2E |
| Playwright CLI or test runtime unavailable | BLOCKED after reporting required journeys |
| Isolated server cannot start within timeout | BLOCKED with sanitized logs |
| OpenSpec contradicts implementation | BLOCKED — report discrepancy before creating E2E |
| Existing test covers the affected journey | UPDATE existing test instead of creating a new one |

## Coverage Baseline

Every canonical frontend screen has a structural browser baseline. Selected critical journeys have functional E2E coverage. Domain edge cases and exhaustive authorization remain covered by Go tests.

The current inventory lives in `e2e/README.md` — consult it before adding or modifying coverage. It lists: routes, viewports, roles, responsible test file, functional journey, deliberate exclusions, and Go layer responsible. Add a new route by adding one entry to the data table in `structural.spec.ts`; update the README when the inventory changes.

- **Seeded profile**: all canonical screens at 390px and 1280px (anonymous + authenticated). Dynamic IDs prepared once in `beforeAll` via fixture helpers.
- **Empty profile**: only `/login`, `/setup`, and `/` (onboarding redirects and bootstrap). Authenticated screens are not re-asserted after bootstrap.
- **Functional journeys**: one per domain (tickets, users, desks, categories/workflows, settings, auth, HTMX, roles). Not exhaustive.
- **HTMX swaps**: verified via shared `assertHtmxSwap` helper (HX-Request header, exact status, zero document navigations, target changed, chrome intact, URL contract).
- **Published workflow**: observed via `#workflow-pending` + `.workflow-instruction` in `/tickets/{id}` — no product changes needed.
- **Roles**: minimal matrix with root, admin, agent, user — browser-visible authorization (not HTTP-only).
- **Exclusions**: password change, deactivation, deletion, exhaustive state machine, exhaustive authorization, exhaustive filter combos — all covered by Go tests as documented in `e2e/README.md`.

## References

- `../../../openspec/` — canonical specs and active changes.
- `../../../e2e/` — Playwright tests, config, server-lifecycle, and CLI scripts.
- `../../../e2e/server-lifecycle.ts` — isolated server start/stop for tests.
- `../../../e2e/scripts/start-empty.mjs` — CLI entry point for empty DB.
- `../../../e2e/scripts/start-seeded.mjs` — CLI entry point for seeded DB.
- `../../../e2e/scripts/stop.mjs` — CLI entry point for stop + cleanup.
- `../../../e2e/cmd/seed/main.go` — database seeder for root, category, desk, workflow.
- `../../../e2e/cmd/migrate/main.go` — database migrator for empty-DB tests.
- `../../../cmd/server/main.go` — local server environment and health endpoint.
- `../../../e2e/README.md` — coverage matrix (screen → journeys → specs).
- `../../../e2e/tests/helpers/layout.ts` — shared structural baseline assertion (overflow + console/page errors).
- `../../../e2e/tests/helpers/navigation.ts` — shared UI navigation helpers (create ticket, resolve workflow/category/user hrefs).
- `../../../e2e/tests/helpers/htmx.ts` — shared `assertHtmxSwap` helper for HTMX interaction tests.
- `../../../e2e/tests/helpers/auth.ts` — shared login helpers.