# E2E frontend coverage — issue #78

Versioned Playwright regression for every canonical frontend screen (Chromium only, no visual screenshots).

## Canonical screens & baselines

Every screen is asserted at **390px** and **1280px**:

- `html.scrollWidth <= viewport` (no document-level horizontal overflow)
- zero `console.error` and zero `pageerror`

Shared assertion lives in `e2e/tests/helpers/layout.ts` (`assertNoHorizontalOverflow` / `assertCanonicalScreen`); no copy-paste.

| Screen | Route | Key journey assertions | Spec |
|---|---|---|---|
| Tickets | `/tickets` | empty state, list with HTMX `hx-get` → `#ticket-list`, filters (state/priority/category/assignee) | `tests/tickets.spec.ts`, `tests/htmx.spec.ts`, `tests/structural.spec.ts` |
| Ticket detail | `/tickets/{id}` | Properties sidebar (Requester/Category/State), timeline, comment create + rejection on resolved/closed/cancelled, state transitions (`new→in_progress→resolved→closed`, `new→cancelled`), HTMX `hx-target="#ticket-detail"` | `tests/ticket-detail.spec.ts`, `tests/htmx.spec.ts`, `tests/structural.spec.ts` |
| Desks | `/desks` | list seeded desk, create/rename/delete, membership add/remove | `tests/desks.spec.ts`, `tests/structural.spec.ts` |
| Categories | `/categories` | index + badge, create/rename/delete | `tests/categories.spec.ts`, `tests/structural.spec.ts` |
| Workflow builder | `/categories/{id}/workflow` | rail with step cards, `hx-target="#workflow-builder"`, add/remove step, live region, publish | `tests/categories.spec.ts`, `tests/htmx.spec.ts`, `tests/structural.spec.ts` |
| Users | `/users` | mobile 390px in-panel scroll + desktop baselines, tabs/search HTMX partial swap | `tests/users.spec.ts` (existing), `tests/htmx.spec.ts`, `tests/structural.spec.ts` |
| Settings | `/settings` | appearance panel (Blue/Violet/Yellow), `POST /settings/appearance` persist | `tests/settings.spec.ts`, `tests/structural.spec.ts` |
| Auth | `/login`, `/setup` | first-user setup, seeded login, logout, auth gates | `tests/auth.spec.ts` |

## Journey groups

- **Desks** — `tests/desks.spec.ts`
- **Categories + builder** — `tests/categories.spec.ts`
- **Settings appearance** — `tests/settings.spec.ts`
- **Ticket detail/comments** — `tests/ticket-detail.spec.ts`
- **HTMX partial swaps** — `tests/htmx.spec.ts` (ticket list filters, users tabs/search, builder, ticket detail)
- **Role-scoped** — `tests/roles.spec.ts` (admin vs `user` via seeded data; capability gates `CapManageUsers/Categories/Desks`, `CapCommentInternal`)
- **Structural baselines** — `tests/structural.spec.ts` (shared helper across all canonical routes; collects observability via `collectObservability`)

## Infra

- Isolated temp SQLite DB per `test.describe` via `server-lifecycle.ts` / `lifecycle-core.js` (loopback-only, cleanup of temp DB + `.e2e-state.json`)
- `playwright.config.ts` — `chromium` only, `workers: 1`, `trace/on-first-retry`
- CI — `.github/workflows/e2e.yml` job `E2E / frontend coverage` (`npm test` in `e2e/`)

## How to run

```bash
cd e2e
npm test
```

For exploration:

```bash
npm run server:start:seeded
npm run explore -- snapshot
npm run server:stop
```
