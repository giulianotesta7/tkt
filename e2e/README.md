# E2E frontend coverage

Versioned Playwright regression for canonical frontend screens and selected critical journeys. Chromium only, no visual screenshots.

> Every canonical frontend screen has a structural browser baseline. Selected critical journeys have functional E2E coverage. Domain edge cases and exhaustive authorization remain covered by Go tests.

## What this suite does

- Structural baseline: every canonical route, visited at 390px and 1280px, asserting URL or redirect, heading, primary control, no horizontal overflow, no clipped content, the design-system tokens, and zero console errors, page errors, failed loopback requests, and loopback 5xx responses.
  - Nothing is clipped: for every element that carries text, and every form control that carries a value, an element whose own `overflow` is `hidden`/`clip` may not cut its content off. Exempt by mechanism, each with its reason in `helpers/layout.ts`: an explicit `text-overflow: ellipsis` affordance, a deliberately scrollable region, a container collapsed to a pixel or to zero (the visually-hidden pattern, and the mobile catalog drill-down), and a single-line form control whose value is longer than the whole viewport, where no layout could show it. This is the rule that catches a value hidden with no ellipsis and no other place on the page showing it.
  - The design-system tokens are read from the rendered document, not from the source: the neutral canvas, the muted, faint and hairline rungs, the single accent, the inverted rail canvas, and that the vendored typeface **actually loaded**. The last one matters most: the interface carried three declared families while `document.fonts.size` was 0, and declaring a family is not loading one.
  - Still no visual screenshots, and that is a decision rather than an omission: a baseline image must be regenerated on every intended change and depends on the rendering platform. The clipping and token rules are deterministic and platform-independent, which is what these defects need.
- Functional journeys: representative journeys per domain. Not exhaustive.
- HTMX swaps: proven by request evidence (`HX-Request: true`, exact endpoint, method, status), zero document navigation on the main frame, target region change, chrome intact, URL contract. Never by `hx-*` attributes alone.
- HTMX no-swap requests: `assertHtmxNoSwap` proves `HX-Request: true`, exact endpoint and query, method, status 200, zero main-frame navigation, and unchanged URL for controls using `hx-swap="none"`; the consumer proves the persisted effect later.
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
| Tickets | `/tickets` | root | Tickets — list, search filter, SLA badge/ordering | `tests/tickets.spec.ts` | filter combos (`handlers_tickets_test.go`) |
| New ticket | `/tickets/new` | root | Tickets — creation | `tests/tickets.spec.ts` | validation (`handlers_tickets_test.go`) |
| Ticket detail | `/tickets/{id}` | root | Tickets — detail contract, SLA panel, SLA live countdown | `tests/ticket-detail.spec.ts` | closed-state POST rejection (`handlers_comment_test.go`) |
| Users | `/users` | root | Users — list | `tests/structural.spec.ts` | `handlers_users_view_test.go` |
| New user | `/users/new` | root | Users — creation+edition | `tests/users.spec.ts` | — |
| Edit user | `/users/{id}/edit` | root | Users — edition | `tests/users.spec.ts` | password change, deactivation, deletion (`user_reactivate_test.go`, `handlers_users*.go`) |
| Categories | `/categories` | root | Categories — list | `tests/categories.spec.ts` | — |
| New category | `/categories/new` | root | Categories — creation | `tests/categories.spec.ts` | — |
| Edit category | `/categories/{id}/edit` | root | Categories — rename | `tests/categories.spec.ts` | — |
| Workflow builder | `/categories/{id}/workflow` | root | Categories+Workflows — integrated journey | `tests/categories.spec.ts` | step validations (`handlers_category_workflows_test.go`) |
| Category SLA | `/categories/{id}/sla` | root | Categories — SLA targets | `tests/categories.spec.ts` | target validation and atomicity (`handlers_category_sla_test.go`, `sla_store_test.go`) |
| Desk compatibility | `/desks` GET redirect | root | Categories/Structure — desk CRUD + membership; legacy redirect | `tests/categories.spec.ts`, `tests/desks.spec.ts` | — |
| Settings | `/settings` | root | Settings — appearance and SLA persist | `tests/settings.spec.ts` | — |
| Ticket metrics | `/tickets/metrics` | admin, root | Tickets — metrics dashboard, filters, and SLA attainment | `tests/ticket-metrics.spec.ts` | period/window math and authorization (`handlers_ticket_metrics_test.go`, `render_metrics_test.go`) |

## Functional journeys

| Journey | Browser-evidenced behavior | Test file |
|---|---|---|
| Auth — setup, login, logout, gates | bootstrap on empty base, seeded login, `/setup` with users, `/` redirect, auth gate | `tests/auth.spec.ts` |
| Tickets — creation, list, detail | create via UI, visible in list, open detail | `tests/tickets.spec.ts` |
| Tickets — search filter and pagination | HTMX swap on `#tickets-screen`: Apply sends the visible query controls together, Enter submits the same toolbar, an impossible term shows `No tickets match your filters`, and Clear restores the full list. A multi-page query preserves its controls through Next, reload, and 390px metadata rows. Each swap proves GET `/tickets`, 200, `HX-Request: true`, zero main-frame navigation, target mutation, and the pushed query URL. | `tests/tickets.spec.ts` |
| Tickets — public comment | native POST (the comment form has no `hx-post`): 303 response, navigation to detail, comment in timeline, persists after reload | `tests/tickets.spec.ts` |
| Tickets — transition | HTMX swap: `new → in_progress` with visible state badge, timeline entry, reload persistence | `tests/tickets.spec.ts` |
| Tickets — SLA visibility and ordering | after enabling SLA and creating a ticket, `/tickets` shows the SLA badge plus the pending milestone's due instant, while a ticket created before enabling keeps an empty SLA cell; the Order by control applies urgency and priority (each the reverse of the default newest-first) and the pushed query keeps the choice across a reload | `tests/tickets.spec.ts` |
| Ticket detail — structural contract | Properties sidebar (Requester, Category, State), timeline, description | `tests/ticket-detail.spec.ts` |
| Ticket detail — SLA panel | a committed ticket shows an SLA section with the overall state plus one block per milestone (Response, Resolve), each carrying its frozen target and its due instant | `tests/ticket-detail.spec.ts` |
| Ticket detail — SLA live countdown (issue #211) | with a deterministic client clock installed before navigation, freezing then fast-forwarding the browser clock changes the pending milestone's ticker text while the server's absolute `<time datetime>` is untouched; fast-forwarding past the due instant turns the reading into the stable `overdue` text (never negative), the ticker is `aria-hidden` with a non-empty coarse accessible span; the list page never loads `/static/sla_countdown.js`, and an achieved milestone drops the countdown hook | `tests/ticket-detail.spec.ts` |
| Ticket detail — closed states | comment form hidden on resolved, closed, cancelled; requester-owned close blocked (Move-to offers no `closed`) | `tests/ticket-detail.spec.ts` |
| Tickets — requester confirmation | `resolved` awaiting confirmation: requester confirms (ticket closes, closure-attributed to requester, panel gone), requester rejects (ticket reopens as detached manual `in_progress`), requester-owned resolved viewed by an agent (no panel, Move-to offers reopen but not `closed`) | `tests/ticket-confirmation.spec.ts` |
| HTMX — requester confirmation | provenance journeys (confirm → `closed`, reject → `in_progress`, agent blocked close) each proven by `assertHtmxSwap`: POST `/tickets/{id}/confirmation` (`decision=confirm|reject`), expected 200,`HX-Request` true, `#ticket-detail` fragment, zero navigation | `tests/ticket-confirmation.spec.ts` |
| Ticket detail — priority change | HTMX swap on `#ticket-detail`: `critical` visible after swap, no navigation | `tests/ticket-detail.spec.ts` |
| Users — creation+edition | create user, edit name and role via `/users/{id}/edit`, list reflects change, persists after reload | `tests/users.spec.ts` |
| Users — agent downgrade handoff (issue #47) | downgrade a desk-member agent via `/users/{id}/edit`: 200 HX save (no generic 500), desk membership removed, open ticket reassigned to the remaining eligible member (persisted after reload), handoff audit reason visible in the timeline; unresolvable-desk branch leaves the ticket unassigned | `tests/users.spec.ts` |
| Categories/Structure — desk administration | desk creation/editing stays in the unified drawer; membership uses the compatibility member routes; `/desks` GET redirects to `/categories` | `tests/categories.spec.ts`, `tests/desks.spec.ts` |
| Categories — global hierarchy search | native `GET /categories?q` searches department, desk, and category names. The query survives reload, result links select the hierarchy without `q`, Clear keeps the selected branch, and the category result works at 390px. | `tests/categories.spec.ts` |
| Categories/workflows — integrated | create category → open workflow → add Manual task (count+1, `#save-feedback` toast `Saved`, `assertHtmxSwap` on `/categories/{id}/workflow`) → edit Instructions with zero autosave POSTs → explicit Save (toast `Saved`) → remove (count-1) → re-add → edit Instructions → publish (POST 200, toast `Published`) → reload persistence → create ticket with category → `#workflow-pending` + `.workflow-instruction` show `Handle the ticket` on ticket detail | `tests/categories.spec.ts` |
| Categories/workflows — exit guards | while dirty, breadcrumb/rail links and browser Back prompt `Leave without saving?` (Stay preserves values/URL/focus, Escape stays, Discard leaves without persisting); reload while dirty uses native `beforeunload` only; a reverted field or a successful Save clears the guard | `tests/categories.spec.ts` |
| Settings — appearance | three radios, `:checked` assertion, Violet persists after reload, back to Blue | `tests/settings.spec.ts` |
| Settings — SLA panel | the panel renders the seeded warning percent and default target matrix, an edited percent and target persist after reload, and a rejected save renders the error banner, echoes the submitted values, and stores nothing | `tests/settings.spec.ts` |
| Categories — SLA targets | the specific category is resolved by name through its `Configure SLA` menu item, its four materialized rows render, an edited target persists after reload | `tests/categories.spec.ts` |
| Tickets — operational metrics summary | always-visible compact summary fixed to the current UTC week: it sits outside `#tickets-screen`, so it survives the list search HTMX swap and a reload of the pushed query, and the View metrics link mirrors the current list query (`return` starts with `/tickets` and carries the searched term). The metrics detail page shows five dashboard cards, each with its own View data disclosure table, workload grouping by agent/desk (By desk re-renders through the same filter form), and a 2x2 grid at 1280px stacking at 390px with no horizontal overflow. | `tests/ticket-metrics.spec.ts` |
| Tickets — SLA attainment panel | the fifth dashboard card aggregates the frozen commitments of tickets created in the selected period: with SLA enabled, a committed ticket produces numeric milestone counts and a rate whose `(N decided)` denominator is visible; the grouping selector re-renders the table by the four canonical priorities and by the created category; a period with no commitment states `No tickets created in the selected period carry a frozen commitment.` with no count lines. Each journey restores the instance-wide SLA switch. | `tests/ticket-metrics.spec.ts` |
| HTMX — users tabs | swap on `#users-root` via Deactivated tab: `assertHtmxSwap` proves request, status, zero navigation, region change, URL gains `?status=deactivated` per `hx-push-url` | `tests/htmx.spec.ts` |
| HTMX — workflow builder | add-step swap on `#workflow-builder` (mechanism-level; the functional journey lives in `categories.spec.ts`) | `tests/htmx.spec.ts` |
| Roles — minimal matrix | root via bootstrap (empty), admin creates category, agent creates ticket + admin screens Forbidden (browser-visible), user creates ticket + internal checkbox hidden + admin Forbidden | `tests/roles.spec.ts` |
| Roles — requester SLA blindness | a `user`-role requester's list and detail page carry no SLA badge, column header or section while still rendering their own content, and the same ticket shows the SLA panel to staff | `tests/roles.spec.ts` |

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
