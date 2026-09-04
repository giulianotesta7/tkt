# Tasks: Current Task Timeline Projection

## Phase 1 — Timeline projection

- [x] **1.1 RED — Presentation tests.** Updated Go and Playwright coverage so the active pending projection is the first item inside Timeline; passive viewers see the `IN PROGRESS` participant/update copy without controls or internal instructions; authorized actors retain completion controls.
- [x] **1.2 GREEN — HTTP/template implementation.** Populated the participant name from the persisted requester/assignee identity, composed `timeline_with_pending`, and rendered the actionable/informational pending item inside Timeline without creating an audit event.
- [x] **1.3 REFACTOR — Visual contract.** Used existing timeline spacing, amber tokens, accessible labels, and responsive behavior. Kept the existing internal-comment color setting unchanged.

## Phase 1b — Actor-first event presentation

- [x] **1b.1 RED — Timeline regressions.** Added focused render coverage for exact actor-first sentence-case comments/events, dynamic actor attribution, automatic actor omission, metadata nonduplication, escaped content, static manual markup, and with/without solution rendering.
- [x] **1b.2 GREEN — Shared presentation.** Applied the exact narrative forms for comments, transitions, updates, assignments, and both form completion event types. Assignment target and desk remain visible without an actor separator. Manual completions use static visible markup with a green check, `TASK`, and optional `SOLUTION` metadata.
- [x] **1b.3 REFACTOR — Safe visual contract.** Kept stored audit semantics, authorization, HTMX behavior, ordering, escaping, responsive layout, and console/HTTP observability checks unchanged; completed manual events have no disclosure, rotation, cursor, or focus behavior.

## Phase 2 — Verification

- [x] **2.1** Focused Go workflow/timeline tests pass.
- [x] **2.2** Affected workflow-builder Playwright journey passes at desktop and mobile widths; the full E2E suite remains outside this focused correction.
- [x] **2.3** Strict and archived OpenSpec validation pass; the full Go race suite, vet, build, and Chromium E2E suite pass.

## Completion criteria

- [x] No pending-detail setting, migration key, service method, port method, or settings UI remains in the implementation.
- [x] The active pending projection is inside Timeline and disappears after completion.
- [x] Historical comments/events remain ordered and are not duplicated.
- [x] No commit is created by this work.
