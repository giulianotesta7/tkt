# E2E frontend coverage

Versioned Playwright regression for canonical frontend screens and selected critical journeys. Chromium only, no visual screenshots.

> Every canonical frontend screen has a structural browser baseline. Selected critical journeys have functional E2E coverage. Domain edge cases and exhaustive authorization remain covered by Go tests.

## What this suite does

- Structural baseline: every canonical route, visited at 390px and 1280px, asserting URL or redirect, heading, primary control, no horizontal overflow, and zero console errors, page errors, failed loopback requests, and loopback 5xx responses.
- Functional journeys: representative journeys per domain. Not exhaustive.
- HTMX swaps: proven by request evidence (`HX-Request: true`, exact endpoint, method, status), zero document navigation on the main frame, target region change, chrome intact, URL contract. Never by `hx-*` attributes alone.
- HTMX no-swap autosaves: `assertHtmxNoSwap` proves `HX-Request: true`, exact endpoint and query, method, status 200, zero main-frame navigation, and unchanged URL for controls using `hx-swap="none"`; the consumer proves the persisted effect later.
- Native form submissions (comments, ticket creation) are tested as ordinary navigations, not through `assertHtmxSwap`.
- Out of scope: domain edge cases, exhaustive authorization, exhaustive workflow validation, password change, deactivation, deletion. Covered by Go tests.

E2E does not replace unit or integration tests.

## Screen inventory — structural baselines

All screens below use `e2e/tests/helpers/layout.ts` (`collectObservability` + `assertCanonicalScreen`) at 390px and 1280px. The seeded profile covers the authenticated inventory; the empty profile covers only `/login`, `/setup`, and `/` (onboarding and empty-dependent redirects). Adding a route is one entry in the `structural.spec.ts` data table.

| Screen | Route | Role(s) | Functional journey | Test file | Exclusions (Go layer) |
|---|---|---|---|---|---|
| Login | `/login` | anonymous; empty redirects to `/setup` | Auth — seeded login | `tests/auth.spec.ts` | credential validation (`handlers_auth_test.go`) |
| Setup | `/setup` | anonymous (empty form, seeded redirects to `/login` or `/tickets`) | Auth — bootstrap and gates | `tests/auth.spec.ts` | `handlers_auth_test.go`, `middleware_auth_test.go` |
| Root | `/` | redirects per session state | Auth — `/` redirect | `tests/auth.spec.ts` | `handlers_tickets_test.go` |
| Tickets | `/tickets` | root | Tickets — list, search filter | `tests/tickets.spec.ts` | filter combos (`handlers_tickets_test.go`) |
| New ticket | `/tickets/new` | root | Tickets — creation | `tests/tickets.spec.ts` | validation (`handlers_tickets_test.go`) |
| Ticket detail | `/tickets/{id}` | root | Tickets — detail contract | `tests/ticket-detail.spec.ts` | closed-state POST rejection (`handlers_comment_test.go`) |
| Users | `/users` | root | Users — list | `tests/structural.spec.ts` | `handlers_users_view_test.go` |
| New user | `/users/new` | root | Users — creation+edition | `tests/users.spec.ts` | — |
| Edit user | `/users/{id}/edit` | root | Users — edition | `tests/users.spec.ts` | password change, deactivation, deletion (`user_reactivate_test.go`, `handlers_users*.go`) |
| Categories | `/categories` | root | Categories — list | `tests/categories.spec.ts` | — |
| New category | `/categories/new` | root | Categories — creation | `tests/categories.spec.ts` | — |
| Edit category | `/categories/{id}/edit` | root | Categories — rename | `tests/categories.spec.ts` | — |
| Workflow builder | `/categories/{id}/workflow` | root | Categories+Workflows — integrated journey | `tests/categories.spec.ts` | step validations (`handlers_category_workflows_test.go`) |
| Desks | `/desks` | root | Desks — CRUD + membership | `tests/desks.spec.ts` | — |
| Settings | `/settings` | root | Settings — appearance persist | `tests/settings.spec.ts` | — |

## Functional journeys

| Journey | Browser-evidenced behavior | Test file |
|---|---|---|
| Auth — setup, login, logout, gates | bootstrap on empty base, seeded login, `/setup` with users, `/` redirect, auth gate | `tests/auth.spec.ts` |
| Tickets — creation, list, detail | create via UI, visible in list, open detail | `tests/tickets.spec.ts` |
| Tickets — search filter | HTMX swap on `#ticket-list`: search by unique title (filtered result), impossible term (`No tickets match your filters.`), cleared search (full list). Each swap proven by `assertHtmxSwap`: GET `/tickets` with expected `q`, 200, `HX-Request: true`, zero main-frame navigation, URL unchanged | `tests/tickets.spec.ts` |
| Tickets — public comment | native POST (the comment form has no `hx-post`): 303 response, navigation to detail, comment in timeline, persists after reload | `tests/tickets.spec.ts` |
| Tickets — transition | HTMX swap: `new → in_progress` with visible state badge, timeline entry, reload persistence | `tests/tickets.spec.ts` |
| Ticket detail — structural contract | Properties sidebar (Requester, Category, State), timeline, description | `tests/ticket-detail.spec.ts` |
| Ticket detail — closed states | comment form hidden on resolved, closed, cancelled (browser-visible rejection) | `tests/ticket-detail.spec.ts` |
| Ticket detail — priority change | HTMX swap on `#ticket-detail`: `critical` visible after swap, no navigation | `tests/ticket-detail.spec.ts` |
| Users — creation+edition | create user, edit name and role via `/users/{id}/edit`, list reflects change, persists after reload | `tests/users.spec.ts` |
| Desks — create, rename, delete, membership | each operation executed with visible result and reload persistence | `tests/desks.spec.ts` |
| Categories/workflows — integrated | create category → open workflow → add Manual task (count+1, live region, `assertHtmxSwap` on `/categories/{id}/workflow`) → autosave Instructions with `assertHtmxNoSwap` → remove (count-1) → re-add → autosave Instructions → publish (POST 200, badge Published) → reload persistence → create ticket with category → `#workflow-pending` + `.workflow-instruction` show `Handle the ticket` on ticket detail | `tests/categories.spec.ts` |
| Settings — appearance | three radios, `:checked` assertion, Violet persists after reload, back to Blue | `tests/settings.spec.ts` |
| HTMX — users tabs | swap on `#users-root` via Deactivated tab: `assertHtmxSwap` proves request, status, zero navigation, region change, URL gains `?status=deactivated` per `hx-push-url` | `tests/htmx.spec.ts` |
| HTMX — workflow builder | add-step swap on `#workflow-builder` (mechanism-level; the functional journey lives in `categories.spec.ts`) | `tests/htmx.spec.ts` |
| Roles — minimal matrix | root via bootstrap (empty), admin creates category, agent creates ticket + admin screens Forbidden (browser-visible), user creates ticket + internal checkbox hidden + admin Forbidden | `tests/roles.spec.ts` |

The role matrix exercises real actors (root, admin, agent, user) without a Cartesian product. Exhaustive authorization stays in Go (`handlers_admin_test.go`, `authorization.go`).

## Exclusions delegated to Go

- Password change, deactivation/reactivation, user deletion, exhaustive role protections: `handlers_users*.go`, `user_reactivate_test.go`.
- Exhaustive ticket state machine, comment rejection status codes, workflow step validations: `handlers_comment_test.go`, `handlers_tickets*.go`, `handlers_category_workflows_test.go`.
- Search filter combinations, assignment, workflow edge cases: `handlers_tickets_test.go`, `ticket-search` spec.
- Exhaustive 403 checks for every managed route: `handlers_admin_test.go`, `authorization.go`.

## Infra

- Isolated temp SQLite per `test.describe` via `server-lifecycle.ts` (loopback-only, cleanup of temp DB and state file).
- `playwright.config.ts` — chromium only, one worker, trace on first retry.
- Shared helpers: `helpers/layout.ts` (structural assertions), `helpers/htmx.ts` (`assertHtmxSwap` and `assertHtmxNoSwap`), `helpers/network.ts` (exact native POST responses), `helpers/navigation.ts` (entity-strict navigation), `helpers/auth.ts` (login). Keep one owner per behavior.
- CI: `.github/workflows/e2e.yml`, job `E2E / frontend coverage`.

## Run

```bash
cd e2e
npm ci
npx playwright test --list
npm test
CI=true npm test -- --repeat-each=2
```

Exploration:

```bash
npm run server:start:seeded
npm run explore -- snapshot
npm run explore -- close-all
npm run server:stop
```
