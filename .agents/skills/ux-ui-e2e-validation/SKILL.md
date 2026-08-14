---
name: ux-ui-e2e-validation
description: "Trigger: functional UX/UI changes, forms, navigation, HTMX, validation, responsive usability. Validate local user journeys with Playwright E2E."
license: MIT
metadata:
  author: "giulianotesta7"
  version: "1.0"
---

## Activation Contract

Run after focused/unit/integration tests when new or modified user-observable behavior affects flows, forms, controls, navigation, keyboard interaction, states, HTMX swaps, validation, visible permissions, or responsive usability. Include style/layout changes that may affect overflow, focus, visibility, hit targets, or interaction. Do not run for copy-only, docs/comments, golden-only, test-only, formatting, or interaction-neutral cosmetic changes.

## Hard Rules

- Test local tkt only; never accept staging or deployed URLs.
- Use a unique temporary SQLite DB and loopback-only free port. Preserve the environment; never touch dev/prod data.
- Seed through public UI/HTTP behavior or an established safe test fixture; first-run checks require an empty DB.
- Use Playwright accessibility snapshots/selectors for actions. Screenshots are evidence only. Avoid unsafe browser code; use it only when safe MCP tools cannot express a required check and state the justification.
- Use the available Playwright MCP runtime; install no external dependency or Playwright test framework solely for this check.
- Never replace lower-level tests, claim PASS after a failure, kill unrelated processes, or stop a reused server.

## Decision Gates

| Condition | Result |
| --- | --- |
| Excluded-only diff | SKIP with reason |
| Browser MCP unavailable | BLOCKED after reporting required journeys |
| Isolated local server cannot start or become ready within timeout | BLOCKED with sanitized logs |
| Existing project-owned server is proven loopback-only and uses an isolated temp DB | Reuse; do not stop it |
| Otherwise | Launch `go run ./cmd/server` with `TKT_DB_PATH=<temp>` and `TKT_LISTEN=127.0.0.1:<free-port>` |

## Execution Steps

1. Derive the smallest meaningful affected journeys from diff/spec/task context; cover happy path and relevant failure or edge state.
2. Create temp DB/log/evidence paths, choose a free localhost port, launch or safely reuse tkt, and poll `/healthz` with a bounded timeout. Record logs without credentials or source content.
3. At relevant desktop/mobile viewports, verify page/URL, semantic content, outcome, applicable keyboard-only flow and visible focus, console errors, relevant failed requests, and horizontal overflow when layout is affected.
4. On failure, preserve truthful FAIL evidence during reporting. Then stop only the launched PID and remove temp DB sidecars, logs, and screenshots unless retention was requested; verify cleanup.

## Output Contract

Return applicability and reason; journeys; server/DB isolation; viewports; assertions/evidence; PASS, FAIL, or BLOCKED; cleanup evidence; and follow-up. For failures include reproduction, expected/actual, viewport, console, and network evidence.

## References

- `../../../cmd/server/main.go` — local server environment and health endpoint.
