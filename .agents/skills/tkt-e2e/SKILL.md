---
name: tkt-e2e
description: "Trigger: implementing or changing a visible feature, modifying a critical journey, fixing a browser-observable bug, reviewing user-facing behavior, or adding/updating E2E coverage. Explore browser behavior and maintain versioned Playwright regression tests."
license: MIT
metadata:
  author: "giulianotesta7"
  version: "1.3"
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

## Coverage Baseline (frontend coverage, issue #78)

Versioned regression now covers every canonical screen at 390px and 1280px (no document-level horizontal overflow, zero console/page errors via `e2e/tests/helpers/layout.ts`):

- `/tickets` — list + filters (HTMX `hx-get` → `#ticket-list`)
- `/tickets/{id}` — detail with Properties sidebar, timeline, comments (rejection on resolved/closed/cancelled), state transitions, HTMX `hx-target="#ticket-detail"`
- `/desks` — list, create, rename, delete, membership add/remove
- `/categories` — index with workflow badge, create/rename/delete, workflow builder rail + `hx-target="#workflow-builder"` (add/remove step, publish)
- `/users` — mobile 390px in-panel scroll baseline + desktop baseline (existing `users.spec.ts`)
- `/settings` — appearance panel (3 swatches), persist on POST `/settings/appearance`

Journey groups (see `e2e/README.md` matrix for spec mapping): `desks.spec.ts`, `categories.spec.ts`, `settings.spec.ts`, `ticket-detail.spec.ts`, `htmx.spec.ts` (partial swaps without full reload), `roles.spec.ts` (admin vs `user` gates via seeded data), `structural.spec.ts` (shared baseline helper across all canonical routes).

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