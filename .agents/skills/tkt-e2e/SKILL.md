---
name: tkt-e2e
description: "Trigger: implementing or changing a visible feature, modifying a critical journey, fixing a browser-observable bug, reviewing user-facing behavior, or adding/updating E2E coverage. Explore browser behavior and maintain versioned Playwright regression tests."
license: MIT
metadata:
  author: "giulianotesta7"
  version: "1.0"
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
- Start an isolated tkt instance with a temporary SQLite database and a random loopback-only port before any browser interaction.
- Use Playwright CLI (`npx playwright open`) for ad-hoc exploration of real behavior before writing assertions.
- For versioned regression: inspect the interface with accessibility snapshots/selectors, check the console for errors and relevant network requests, then create or update a test in `e2e/tests/`.
- After writing a test, run both the affected test file AND the full E2E suite (`npm test` in `e2e/`).
- On test failure, preserve the Playwright trace, screenshot, and report as failure evidence.
- Close all browser sessions after each test.
- Clean up the server process and temporary database/files after the suite completes.

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
| Browser MCP or Playwright runtime unavailable | BLOCKED after reporting required journeys |
| Isolated server cannot start within timeout | BLOCKED with sanitized logs |
| OpenSpec contradicts implementation | BLOCKED — report discrepancy before creating E2E |
| Existing test covers the affected journey | UPDATE existing test instead of creating a new one |

## References

- `../../../openspec/` — canonical specs and active changes.
- `../../../e2e/` — Playwright tests, config, and global setup.
- `../../../cmd/server/main.go` — local server environment and health endpoint.