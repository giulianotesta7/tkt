# E2E frontend coverage — issue #78

Versioned Playwright regression for canonical frontend screens and selected critical journeys (Chromium only, no visual screenshots).

> Every canonical frontend screen has a structural browser baseline. Selected critical journeys have functional E2E coverage. Domain edge cases and exhaustive authorization remain covered by Go tests.

## What this suite guarantees and what it does not

- **Structural baseline**: every canonical route is visited in a real browser at 390px and 1280px and asserted for URL/redirect, heading/main region, primary accessible control, no document-level horizontal overflow, and zero console/page errors, failed own-requests and own 5xx responses.
- **Functional journeys**: ONE representative journey per domain (tickets, users, desks, categories/workflows, settings, auth, HTMX swaps, minimal role matrix). Not exhaustive.
- **HTMX swaps**: every HTMX interaction (comment, transition, priority, filter, workflow builder, users tabs) uses the shared `assertHtmxSwap` helper which verifies `HX-Request: true` header, 200 response, target region content changed, non-target chrome unchanged, and no document navigation.
- **Published workflow verification**: the published workflow version IS observable in the ticket detail via `#workflow-pending` + `.workflow-instruction` — no product changes needed.
- **Out of scope for E2E**: domain edge cases, exhaustive authorization matrix, exhaustive workflow validations, password change / deactivation / deletion lifecycles, exhaustive filter/transition combinations. These remain covered by Go tests (`go test ./...`).

E2E does not replace unit/integration tests.

## Canonical screen inventory — structural baselines

All screens below are asserted via `e2e/tests/helpers/layout.ts` (`collectObservability` + `assertCanonicalScreen`) at **390px** and **1280px** for **empty-base** (bootstrap — onboarding screens only) and **seeded** profiles. The helper captures `console.error`, `pageerror`, failed loopback requests, and loopback 5xx responses; failures report screen label, URL, and role. External origins are ignored (app is loopback-only).

The structural spec (`structural.spec.ts`) uses a **data-driven table** — adding a new route requires one entry to the array, not copying a block.

| Screen | Route | Viewport(s) | Role(s) | Structural assertions (per screen) | Responsible functional journey | Test file | Deliberate E2E exclusions | Go tests covering exclusions |
|---|---|---|---|---|---|---|---:|---|
| Login | `/login` | 390, 1280 | anonymous (seeded); anonymous→redirect to `/setup` when empty | heading "Sign in to tkt", email control, overflow, observability | Auth — seeded login | `tests/auth.spec.ts`, `tests/structural.spec.ts` | exhaustive credential validation | `handlers_auth_test.go` |
| Setup (empty) | `/setup` | 390, 1280 | anonymous (empty) | heading "Set up tkt", name/email/password controls, overflow, observability | Auth — first-user bootstrap | `tests/auth.spec.ts`, `tests/structural.spec.ts` | — | `handlers_auth_test.go`, `middleware_auth_test.go` |
| Setup (with users) | `/setup` | 390, 1280 | anonymous→`/login`, authenticated→`/tickets` | redirect asserted, heading of target, overflow, observability | Auth — setup-with-users gate | `tests/auth.spec.ts`, `tests/structural.spec.ts` | — | `middleware_auth_test.go` |
| Root redirect | `/` | 390, 1280 | anonymous→`/login` (seeded) / `/setup` (empty); root→`/tickets` | redirect to tickets/login/setup per session, overflow, observability | Auth — root redirect | `tests/auth.spec.ts`, `tests/structural.spec.ts` | — | `handlers_tickets_test.go` |
| Tickets | `/tickets` | 390, 1280 | root (seeded+empty) | h1 Tickets, "New ticket" / search control, overflow, observability | Tickets — list | `tests/tickets.spec.ts`, `tests/structural.spec.ts` | exhaustive filter combos | `handlers_tickets_test.go`, `ticket-search` spec |
| New ticket | `/tickets/new` | 390, 1280 | root | h1 New ticket, Create ticket button, overflow, observability | Tickets — creation | `tests/tickets.spec.ts`, `tests/structural.spec.ts` | exhaustive validation | `handlers_tickets_test.go` |
| Ticket detail | `/tickets/{id}` | 390, 1280 | root | #ticket-detail, comment control, overflow, observability | Tickets — detail/properties/timeline | `tests/ticket-detail.spec.ts`, `tests/structural.spec.ts` | — | `handlers_detail_test.go` |
| Users | `/users` | 390, 1280 | root | h1 Users, New user link, overflow + in-panel scroll at 390px, observability | Users — creation+edition | `tests/users.spec.ts`, `tests/structural.spec.ts` | — | `handlers_users_view_test.go` |
| New user | `/users/new` | 390, 1280 | root | h1 New user, Create user, overflow, observability | Users — creation+edition | `tests/users.spec.ts`, `tests/structural.spec.ts` | — | `handlers_users*.go` |
| Edit user | `/users/{id}/edit` | 390, 1280 | root | h2 Edit user, role select + Save changes, overflow, observability | Users — edition | `tests/users.spec.ts`, `tests/structural.spec.ts` | password change, deactivation/deletion, exhaustive role protections | `user_reactivate_test.go`, `handlers_users*.go` |
| Categories | `/categories` | 390, 1280 | root | h1 Categories, New category, overflow, observability | Categories — list | `tests/categories.spec.ts`, `tests/structural.spec.ts` | — | `handlers_categories*.go` |
| New category | `/categories/new` | 390, 1280 | root | h1 New category, Create category, overflow, observability | Categories — creation | `tests/categories.spec.ts`, `tests/structural.spec.ts` | — | — |
| Edit category | `/categories/{id}/edit` | 390, 1280 | root | h1 Rename category, Save, overflow, observability | Categories — rename | `tests/categories.spec.ts`, `tests/structural.spec.ts` | — | — |
| Workflow builder | `/categories/{id}/workflow` | 390, 1280 | root | h1 Category workflow, #workflow-builder + Publish, overflow, observability | Categories+Workflows — builder integrated journey | `tests/categories.spec.ts`, `tests/structural.spec.ts` | exhaustive step validations | `handlers_category_workflows_test.go`, `category-workflows` spec |
| Desks | `/desks` | 390, 1280 | root | h1 Desks, New desk summary, overflow, observability | Desks — list/create/rename/delete/membership | `tests/desks.spec.ts`, `tests/structural.spec.ts` | — | `handlers_desks_test.go` |
| Settings | `/settings` | 390, 1280 | root | h1 Settings, appearance radios + Save appearance, overflow, observability | Settings — appearance persist | `tests/settings.spec.ts`, `tests/structural.spec.ts` | — | `handlers_settings_test.go` |

## Functional journeys (representative, not exhaustive)

| Journey | What is exercised in the browser | Evidence (not `hx-*` alone) | Test file | Exclusions → Go coverage |
|---|---|---|---|---|
| Auth — setup/login/logout + gate + redirects | first-user bootstrap, seeded login, /setup with users, / redirect, logout→/login gate | redirect asserted + heading of target + persistence | `tests/auth.spec.ts` | — |
| Tickets — creation/list/detail | login→create ticket→verify in list→open detail (General published workflow) | URL and visible title/ticket number | `tests/tickets.spec.ts` | validations → Go |
| Tickets — search filter functionality | search `q` via hx-get → #ticket-list; filtered result visible, empty state on no-match + persistence (swap mechanism verified in htmx.spec.ts shared helper) | filtered results visible, no-match empty state | `tests/tickets.spec.ts` | exhaustive combos → Go |
| Tickets — public comment | fill comment body → POST → timeline contains comment → reload persists (verified via assertHtmxSwap) | timeline content change + persistence | `tests/tickets.spec.ts`, `tests/ticket-detail.spec.ts` | exhaustive visibility → Go |
| Tickets — transition | select #ticket-state → POST /transition → state badge In Progress, timeline entry, reload persists (verified via assertHtmxSwap) | region content changed, state visible | `tests/tickets.spec.ts`, `tests/ticket-detail.spec.ts` | all state edges → Go state-machine tests |
| Tickets — priority change | select #ticket-priority → POST /edit → #ticket-detail re-render (verified via assertHtmxSwap) | region content changed with new priority | `tests/ticket-detail.spec.ts` | — |
| Users — creation+edition | create user via /users/new → edit name+role via /users/{id}/edit → list shows renamed → reload persists | list + edit form reflects change | `tests/users.spec.ts` | password change, deactivation/deletion, exhaustive role checks → Go (`user_reactivate_test`, `handlers_users`) |
| Desks — create/rename/delete + membership add/remove | create desk → rename → delete (each with reload persistence), eligible member select → add → member list visible → remove → gone (reload verifies) | list membership appears/disappears, URL stays | `tests/desks.spec.ts` | — |
| Categories — workflow integrated | create category → open workflow → add Manual task (HTMX swap verified) → configure instructions → remove step → re-add → publish (200, badge Published, reload) → create ticket using category → **verify `#workflow-pending` + `.workflow-instruction`** on ticket detail | builder innerHTML changed, publish 200, `#workflow-pending` visible with instruction text | `tests/categories.spec.ts` | exhaustive workflow validations → Go |
| Settings — appearance | 3 radios visible, checked assertion via `input:checked` (no invalid `hasAttribute`), Violet save → reload shows Violet, back to Blue → persist, complementary form method post | checked value + reload persistence | `tests/settings.spec.ts` | — |
| HTMX — partial swaps | ticket list filter, users tabs (`#users-root`), workflow builder (`#workflow-builder`) — each uses `assertHtmxSwap` which verifies HX-Request header, 200, target content changed, chrome intact, URL unchanged | region innerHTML not equal before/after + header intact + HX-Request header | `tests/htmx.spec.ts` | — |
| Roles — minimal matrix | root bootstrap (empty), admin creates category, agent creates ticket + admin screens show Forbidden (browser), user creates ticket + internal checkbox hidden + admin screens Forbidden | browser-visible Forbidden text on direct navigation, hidden control count 0 | `tests/roles.spec.ts` | exhaustive matrix + HTTP 403 codes → Go (`handlers_admin_test`, `authorization.go`) |

## Exclusions delegated to Go (intentionally not in browser E2E)

- Password change, deactivation/reactivation + session kill (D14), user deletion, exhaustive role-change protections, exhaustive management-route matrix — `internal/adapters/http/handlers_users*.go`, `user_reactivate_test.go`, `handlers_admin_test.go`.
- Exhaustive ticket state machine, comment rejection status codes (403/422) on closed tickets, workflow step validations — `handlers_comment_test.go`, `handlers_tickets*.go`, `handlers_category_workflows_test.go`, `ticket-state-machine`, `category-workflows` specs.
- Ticket search exhaustive filter combos, assignment, exhaustive category/workflow edge cases — `handlers_tickets_test.go`, `ticket-search` spec, `handlers_tickets_workflow_test.go`.
- Exhaustive HTTP 403 checks for every managed route — `handlers_admin_test.go`, `authorization.go`.

## Infra

- Isolated temp SQLite DB per `test.describe` via `server-lifecycle.ts` / `lifecycle-core.js` (loopback-only, cleanup of temp DB + `.e2e-state.json`)
- `playwright.config.ts` — `chromium` only, `workers: 1`, `trace/on-first-retry`
- CI — `.github/workflows/e2e.yml` job `E2E / frontend coverage` (`npm test` in `e2e/`)
- Shared helper in `helpers/htmx.ts` — `assertHtmxSwap` for all HTMX interactions

## How to run

```bash
cd e2e
npm ci
npx playwright test --list
npm test
CI=true npm test -- --repeat-each=2
```

For exploration:

```bash
npm run server:start:seeded
npm run explore -- snapshot
npm run server:stop
```