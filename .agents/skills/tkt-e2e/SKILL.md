---
name: tkt-e2e
description: "Trigger: implementing or changing a visible feature, modifying a critical journey, fixing a browser-observable bug, reviewing user-facing behavior, or adding/updating E2E coverage. Explore browser behavior and maintain versioned Playwright regression tests."
license: MIT
metadata:
  author: "giulianotesta7"
  version: "1.1"
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
- Start an isolated tkt instance with a temporary SQLite database and a free loopback port before any browser interaction. Use the shared `server-lifecycle.ts` module:
  ```typescript
  import { startServer, stopServer } from "../server-lifecycle.js";

  test.beforeAll(async () => {
    // Empty migrated DB (for first-user setup):
    await startServer({ seed: false });
    // Or pre-seeded DB (for login + ticket journeys):
    await startServer({ seed: true });
  });
  test.afterAll(async () => {
    await stopServer();
  });
  ```
- Use Playwright CLI from the `e2e/` directory for ad-hoc exploration before writing assertions:
  ```bash
  cd e2e
  # Start the server with your preferred DB state
  npm run explore -- open http://127.0.0.1:PORT
  npm run explore -- snapshot
  npm run explore -- console
  npm run explore -- requests
  npm run explore -- screenshot
  npm run explore -- close-all
  ```
- For versioned regression: inspect the interface with accessibility snapshots/selectors, check the console for errors and relevant network requests, then create or update a test in `e2e/tests/`.
- After writing a test, run both the affected test file AND the full E2E suite (`npm test` in `e2e/`).
- On test failure, preserve the Playwright trace, screenshot, and report as failure evidence.
- Close all browser sessions and clean up the server process after each test.

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

1. Start a server from the `e2e/` tests directory or from the project root:
   ```bash
   cd e2e
   go run ./migrate.go --db=/tmp/tkt-e2e.db
   go run ../cmd/server &
   # or for a seeded instance:
   go run ./seed.go --db=/tmp/tkt-e2e.db
   ```
2. Use the explore script with Playwright CLI commands:
   ```bash
   npm run explore -- open http://127.0.0.1:PORT
   npm run explore -- snapshot
   npm run explore -- console
   npm run explore -- requests
   npm run explore -- screenshot
   npm run explore -- close-all
   ```
3. Stop the server and clean up:
   ```bash
   kill %1
   rm -f /tmp/tkt-e2e.db /tmp/tkt-e2e.db-wal /tmp/tkt-e2e.db-shm
   ```

## References

- `../../../openspec/` — canonical specs and active changes.
- `../../../e2e/` — Playwright tests, config, server-lifecycle, and CLI scripts.
- `../../../e2e/server-lifecycle.ts` — isolated server start/stop for tests.
- `../../../e2e/cmd/seed/main.go` — database seeder for root, category, desk, workflow.
- `../../../e2e/cmd/migrate/main.go` — database migrator for empty-DB tests.
- `../../../cmd/server/main.go` — local server environment and health endpoint.