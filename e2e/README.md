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
- HTMX session refusals: a request the session gate refuses because the session is gone answers `HX-Redirect` with an empty 204 — never a 3xx, which htmx's XHR would follow and swap into the requesting region. Two journeys prove it, and both are asserted at document level because the pre-#347 answer passed the weaker ones: htmx follows a 303 transparently and pushes the redirect target, so the `/login` URL, the login heading, and a login form appearing are all true of login markup injected into the still-open tickets page too. What discriminates is that the login document *is* the page: `body > main.auth-entry` exists, no authenticated chrome (`nav a`) survives, and the form is not inside the ticket list region. The second journey covers an htmx history restore: htmx's own cache-miss loader only swaps a 2xx/3xx response and never reads `HX-Redirect`, so a refusal there used to leave a stale page at the tickets URL; `htmx.config.refreshOnHistoryMiss` makes htmx reload instead, and that full load goes through the ordinary 303 to `/login`.
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
| Settings | `/settings` | root; admin without the Email section | Settings — appearance, SLA and email persist | `tests/settings.spec.ts` | SMTP validation, sealed password, root-only email capability (`handlers_settings_email_test.go`) |
| Notifications | `/notifications` | every signed-in role | Notifications — assignee reads one, then marks all read | `tests/notifications.spec.ts` | recipient policy, atomicity, cross-user scoping, internal-comment visibility (`handlers_notifications_test.go`) |
| Ticket metrics | `/tickets/metrics` | admin, root | Tickets — metrics dashboard, filters, and SLA attainment | `tests/ticket-metrics.spec.ts` | period/window math and authorization (`handlers_ticket_metrics_test.go`, `render_metrics_test.go`) |

## Functional journeys

| Journey | Browser-evidenced behavior | Test file |
|---|---|---|
| Auth — setup, login, logout, gates | bootstrap on empty base, seeded login, `/setup` with users, `/` redirect, auth gate, and a session that dies during an HTMX interaction: GET `/tickets` is refused with `HX-Redirect: /login` and an empty 204, and the login document replaces the page — `main.auth-entry` under the body, no rail nav, no login form inside the ticket list region. A second journey covers a refused HTMX history restore whose cache entry is missing: `refreshOnHistoryMiss` reloads the page, the reload 303s to `/login`, and the result is the login document rather than a stale tickets page at the restored URL. | `tests/auth.spec.ts` |
| Tickets — creation, list, detail | create via UI, visible in list, open detail | `tests/tickets.spec.ts` |
| Tickets — search filter and pagination | HTMX swap on `#tickets-screen`: Apply sends the visible query controls together, Enter submits the same toolbar, an impossible term shows `No tickets match your filters`, and Clear restores the full list. A multi-page query preserves its controls through Next, reload, and 390px metadata rows. Each swap proves GET `/tickets`, 200, `HX-Request: true`, zero main-frame navigation, target mutation, and the pushed query URL. | `tests/tickets.spec.ts` |
| Tickets — on hold while waiting for the requester | an agent's blank reason is refused by the required field and a whitespace reason by the server (422, nothing persisted); the reason is typed in a dialog opened from the SLA section, a refused reason reopens it with the server's message, and the hold swaps in through POST `/tickets/{id}/hold` with the notice and Resume in the SLA section, the reason, the `On hold` chip, and a paused resolution SLA while first response keeps counting; the requester sees `This ticket is waiting for your reply` with the reason; Resume restores both countdowns; the requester's comment on a new hold ends it (native POST, 303); resolving a held ticket ends the hold. | `tests/ticket-hold.spec.ts` |
| Tickets — public comment | native POST (the comment form has no `hx-post`): 303 response, navigation to detail, comment in timeline, persists after reload | `tests/tickets.spec.ts` |
| Tickets — attachments on creation and comments | a requester creates a ticket with a PNG (multipart native POST) and the description lists it with its human size; an agent adds a staff-only comment with a `.txt` and a public comment with a `.pdf` (each a native 303 POST); the requester's detail lists the PNG and the PDF, renders no internal comment, no `.txt` name and no link to its URL, and the file input plus both attachment lists keep no clipping and no horizontal overflow at 1280px and 390px; the PDF download resolves with the original suggested filename and byte-identical content, the same URL answered with `attachment`/`nosniff` headers, the staff-only file's URL answers 404 to the requester, and a `.png` whose bytes are text is refused with the localized `cannot be attached` message while no ticket, file or extra attachment is written. | `tests/ticket-attachments.spec.ts` |
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
| Categories/Structure — desk administration | desk creation/editing stays in the unified drawer; membership uses the compatibility member routes; `/desks` GET redirects to `/categories`. The canonical progressive drill-down journey also distinguishes no desk selection, a selected empty desk, and a populated desk at 1280px and 390px: native GET 200, preserved row/ARIA selection and URL context, mobile Back navigation, and no stale guidance or category results. | `tests/categories.spec.ts`, `tests/desks.spec.ts` |
| Categories/Structure — header actions | The canonical contextual-actions journey measures header/title-text/action containment and separation at 1040, 1081, 1120, 390 and 1280px, preserves all desktop creation controls and mobile drill-down, and proves the New department drawer remains usable through a GET 200 HTMX swap with unchanged chrome and no document navigation. | `tests/categories.spec.ts` |
| Categories — global hierarchy search | native `GET /categories?q` searches department, desk, and category names. The query survives reload, result links select the hierarchy without `q`, Clear keeps the selected branch, and the category result works at 390px. | `tests/categories.spec.ts` |
| Categories/workflows — save feedback | At 1280px and touch-enabled 390px, Saved never overlaps Publish; real click/tap publishes while feedback is visible, with exact HTMX POST/swap evidence and Published v1 persisting after reload. Dismiss emits no mutation, leaves the builder and draft revision unchanged, and publishes nothing. The shared live region, coalescing and five-second timer remain covered. | `tests/categories.spec.ts` |
| Categories/workflows — integrated | create category → open workflow → add Manual task (count+1, `#save-feedback` toast `Saved`, `assertHtmxSwap` on `/categories/{id}/workflow`) → edit Instructions with zero autosave POSTs → explicit Save (toast `Saved`) → remove (count-1) → re-add → edit Instructions → publish (POST 200, toast `Published`) → reload persistence → create ticket with category → `#workflow-pending` + `.workflow-checklist-name` show `Handle the ticket` on ticket detail | `tests/categories.spec.ts` |
| Categories/workflows — exit guards | while dirty, breadcrumb/rail links and browser Back prompt `Leave without saving?` (Stay preserves values/URL/focus, Escape stays, Discard leaves without persisting); reload while dirty uses native `beforeunload` only; a reverted field or a successful Save clears the guard | `tests/categories.spec.ts` |
| Settings — appearance | three radios, `:checked` assertion, Violet persists after reload, back to Blue | `tests/settings.spec.ts` |
| Settings — SLA panel | the panel renders the seeded warning percent and default target matrix, an edited percent and target persist after reload, and a rejected save renders the error banner, echoes the submitted values, and stores nothing | `tests/settings.spec.ts` |
| Settings — interface language | switch the instance to Español from Settings and prove `<html lang="es">` plus Spanish copy on Settings, the tickets list and the ticket detail (state badge, and the period range carrying a Spanish month), the save toast already reading the new language, and persistence across a reload; then switch back to English and prove the detail, list and period range are restored | `tests/settings.spec.ts` |
| Settings — email configuration (issue #362) | root fills the Email section (SMTP server `127.0.0.1`, port `1`, encryption None, no credentials, sender address and name, TKT address), saves it, sees `Saved`, and reads the same values back after a reload — with the password field empty and no "a password is saved" hint while nothing is sealed; the filled section also passes the structural checks at 390px. An administrator's `/settings` renders the panels they own and no Email heading, field, or `Send test email` action. | `tests/settings.spec.ts` |
| Settings — test email and saved password (issue #362) | `Send test email` against a stored configuration whose server refuses the connection (credentials + STARTTLS over `127.0.0.1:1`) re-renders the page with the localized banner naming the refusal and never the password, which appears nowhere in the document; with credentials and STARTTLS saved, a reload shows an empty password field, the "a password is saved" hint, and the stored username and mode. | `tests/settings.spec.ts` |
| Preferences — email opt-out (issue #362) | a requester finds `Receive notifications by email` checked (on by default), turns it off, sees `Saved`, finds it still off after a reload, turns it back on and finds it on again; the queue-order preference sharing the page keeps rendering. | `tests/settings.spec.ts` |
| Categories — SLA targets | the specific category is resolved by name through its `Configure SLA` menu item, its four materialized rows render, an edited target persists after reload | `tests/categories.spec.ts` |
| Tickets — operational metrics summary | always-visible compact summary fixed to the current UTC week: it sits outside `#tickets-screen`, so it survives the list search HTMX swap and a reload of the pushed query, and the View metrics link mirrors the current list query (`return` starts with `/tickets` and carries the searched term). The metrics detail page shows five dashboard cards, each with its own View data disclosure table, workload grouping by agent/desk (By desk re-renders through the same filter form), and a 2x2 grid at 1280px stacking at 390px with no horizontal overflow. | `tests/ticket-metrics.spec.ts` |
| Tickets — SLA attainment panel | the fifth dashboard card aggregates the frozen commitments of tickets created in the selected period: with SLA enabled, a committed ticket produces numeric milestone counts and a rate whose `(N decided)` denominator is visible; the grouping selector re-renders the table by the four canonical priorities and by the created category; a period with no commitment states `No tickets created in the selected period carry a frozen commitment.` with no count lines. Each journey restores the instance-wide SLA switch. | `tests/ticket-metrics.spec.ts` |
| HTMX — users tabs | swap on `#users-root` via Deactivated tab: `assertHtmxSwap` proves request, status, zero navigation, region change, URL gains `?status=deactivated` per `hx-push-url` | `tests/htmx.spec.ts` |
| HTMX — workflow builder | add-step swap on `#workflow-builder` (mechanism-level; the functional journey lives in `categories.spec.ts`) | `tests/htmx.spec.ts` |
| Notifications — assigned, opened, marked read (issue #344) | as root: create an agent, create two tickets and assign both to that agent; as the agent: the rail bell's accessible name carries the unread count (`Notifications, 2 unread`), 45s of fast-forwarded browser time asks `/notifications/count` again, the bell opens the panel as an HTMX swap into `#notification-panel-host` (GET `/notifications`, 200, zero navigation, unchanged URL) which also refreshes the count, the panel lists both tickets with `Assigned to you`, the acting operator and the `Unread` marker, clicking one row POSTs `/notifications/{id}/read` and lands on the ticket detail with the badge down to 1, and `Mark all as read` lands on `/notifications` with no badge, no `Unread` marker and the rows still listed | `tests/notifications.spec.ts` |
| Roles — minimal matrix | root via bootstrap (empty), admin creates category, agent creates ticket + admin screens Forbidden (browser-visible), user creates ticket + internal checkbox hidden + admin Forbidden | `tests/roles.spec.ts` |
| Roles — requester SLA blindness | a `user`-role requester's list and detail page carry no SLA badge, column header or section while still rendering their own content, and the same ticket shows the SLA panel to staff | `tests/roles.spec.ts` |

The role matrix exercises real actors (root, admin, agent, user) without a Cartesian product. Exhaustive authorization stays in Go (`handlers_admin_test.go`, `authorization.go`).

## Exclusions delegated to Go

- Password change, deactivation/reactivation, user deletion, exhaustive role protections: `handlers_users*.go`, `user_reactivate_test.go`.
- Exhaustive ticket state machine, comment rejection status codes, workflow step validations: `handlers_comment_test.go`, `handlers_tickets*.go`, `handlers_category_workflows_test.go`.
- Search filter combinations, assignment, workflow edge cases: `handlers_tickets_test.go`, `ticket-search` spec.
- Exhaustive 403 checks for every managed route: `handlers_admin_test.go`, `authorization.go`.
- SMTP email configuration: field validation, the credentials-need-encryption rule, sealing and never rendering the password, the test-email targeting, and the per-user email preference write: `handlers_settings_email_test.go`, `handlers_preferences_email_test.go`.

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
