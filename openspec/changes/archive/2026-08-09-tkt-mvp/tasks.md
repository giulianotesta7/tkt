# Tasks: tkt — Ticket Management MVP

## Review Workload Forecast

| Field | Value |
|-------|-------|
| Estimated changed lines | ~4,900 (4,500–5,300) |
| 400-line budget risk | High |
| Chained PRs recommended | Yes |
| Suggested split | PR 1 → PR 2 → PR 3 → PR 4 → PR 5 |
| Delivery strategy | ask-on-risk |
| Chain strategy | pending |

```
Decision needed before apply: Yes
Chained PRs recommended: Yes
Chain strategy: pending
400-line budget risk: High
```

### Suggested Work Units

| Unit | Goal | Likely PR | Focused test command | Runtime harness | Rollback boundary |
|------|------|-----------|----------------------|-----------------|-------------------|
| 1 | Scaffold + pure domain (state machine, invariants) | PR 1 | `go test ./internal/domain/...` | N/A — pure Go, zero I/O; behavior proven via matrix tests | Delete scaffold + `internal/domain/`; nothing else depends |
| 2 | Store ports + application use cases (fakes) | PR 2 | `go test ./internal/application/...` | N/A — port fakes only; bcrypt exercised in unit tests | Revert `internal/application/` |
| 3 | SQLite adapter: schema, migrations runner, FTS5 | PR 3 | `go test ./internal/adapters/sqlite/...` | `go test ./internal/adapters/sqlite/ -run TestFTS5` (real modernc driver, in-memory shared cache) | Revert adapter + `migrations/`; schema additive |
| 4 | HTTP adapter: middleware, handlers, templates, goldens | PR 4 | `go test ./internal/adapters/http/...` | httptest servers cover render paths; real runtime deferred to PR 5 (no composition root yet) | Revert `internal/adapters/http/` + `web/templates/` |
| 5 | Composition root + Docker + healthcheck + verify | PR 5 | `go test ./...` | `docker compose up -d --build` then `curl localhost:8080/healthz` → `ok`; `./server -healthcheck` → exit 0 | Drop container/branch; main stays at bootstrap commit |

## Phase 1: Foundation

| ID | Task | Files touched | Deps | Acceptance (spec) | Commit unit | Est. lines |
|----|------|---------------|------|-------------------|-------------|------------|
| [x] 1.1 | Init module `github.com/giulianotesta7/tkt`; pin modernc.org/sqlite v1.56.0 + golang.org/x/crypto (D1, D15) | `go.mod`, `go.sum`, `.gitignore` | — | `go mod tidy` resolves; `go vet ./...` clean baseline | `chore(scaffold): init go module and pin deps` | 15 |

## Phase 2: Domain (pure — zero imports beyond stdlib)

| ID | Task | Files touched | Deps | Acceptance (spec) | Commit unit | Est. lines |
|----|------|---------------|------|-------------------|-------------|------------|
| [x] 2.1 | RED: 5×5 transition matrix test — all 25 pairs (allowed/denied + timestamp set/clear), reopen reason rules, `cancelled` terminal (state-machine spec) | `internal/domain/state_test.go` | 1.1 | Matrix test fails on unimplemented `Transition` | `test(domain): transition matrix 5×5` (RED commit) | 120 |
| [x] 2.2 | GREEN: types + state machine — `ticket.go`, `state.go`, `priority.go` (+`Rank()`), `errors.go` (English constants, typed errors D5), `clock.go`; implement `Transition()`: →`resolved` sets `ResolvedAt`, →`closed` sets `ClosedAt`, reopen clears per spec | `internal/domain/{ticket,state,priority,errors,clock}.go` | 2.1 | Matrix green; scenarios: valid forward path, invalid `new→closed` rejected state stays, terminal `cancelled`, reopen closed reason required / without reason rejected / resolved no reason, clears `resolved_at` / both | `feat(domain): enforce 5-state transition machine` | 180 |
| [x] 2.3 | RED+GREEN: `ApplyUpdate(u, now)` — audit only for changed fields; edits NEVER touch `resolved_at`/`closed_at` (ticket-management lifecycle + audit-log no-silent-mutations) | `internal/domain/{ticket,audit,comment,user,category}.go`, `ticket_test.go` | 2.2 | Edit category refreshes `updated_at` + 1 audit event; invalid priority rejected no changes; timestamps unchanged after edit; priority `Rank()` critical>high>medium>low (ticket-search) | `feat(domain): field updates with audit-only-changed` | 140 |

## Phase 3: Application (ports + use cases, port fakes in tests)

| ID | Task | Files touched | Deps | Acceptance (spec) | Commit unit | Est. lines |
|----|------|---------------|------|-------------------|-------------|------------|
| [x] 3.1 | Define store ports: `TicketStore`, `SearchStore`, `CommentStore`, `AuditStore`, `UserStore`, `SessionStore`, `CategoryStore`, `TicketQuery`, `Page` (Limit FIXED 10, D2) | `internal/application/ports.go` | 2.3 | Compiles; interfaces match design signatures | `feat(application): define store ports` | 90 |
| [x] 3.2 | RED+GREEN: `password.go` — bcrypt Hash/Verify, cost 10 (D15) | `internal/application/{password.go,password_test.go}` | 3.1 | Hash→verify ok; wrong pw → false; two hashes differ (salt); empty password rejected (user-management create-user) | `feat(application): bcrypt password hashing` | 60 |
| [x] 3.3 | RED+GREEN: `ticket_service.go` — Create (MAX+1 number via store, active-user + category-exists checks, state `new`), Transition (stamps actor from session, persists ticket+audit in one txn, D14), Update, GetByID | `internal/application/{ticket_service.go,ticket_service_test.go}` | 3.1, 3.2 | Create valid ticket stored with number + state `new`; missing title → ValidationError; inactive user assignment rejected; every mutation audited (audit-log scenario: 1 transition + 2 edits = 3 events, actor from session) | `feat(application): ticket use cases` | 300 |
| [x] 3.4 | RED+GREEN: `comment_service.go` + `views.go` — AddComment (author from session, non-empty body, any state), ListByTicket ASC, `TicketView` composition (D13) | `internal/application/{comment_service,views}.go` + tests | 3.3 | Comment stored with session author; empty body rejected no store; comment on `closed` accepted; 3 comments render in creation order (comment-timeline) | `feat(application): comment service + ticket views` | 150 |
| [x] 3.5 | RED+GREEN: `user_service.go` + `auth_service.go` — create/update/delete rules (no hard delete when referenced; deactivate), Login (generic error for wrong pw / unknown email / inactive — no enumeration), Logout, UserCount bootstrap gate | `internal/application/{user_service,auth_service}.go` + tests | 3.1, 3.2 | Duplicate email → DuplicateError; missing password rejected; historical assignment preserved on deactivate; delete referenced → ReferencedError, unreferenced deletable; wrong pw / unknown email / deactivated → SAME generic error, no session; logout deletes session (user-management) | `feat(application): user + auth use cases` | 280 |
| [x] 3.6 | RED+GREEN: `category_service.go` + `search_service.go` — category CRUD rules; filter composition (AND), D4 quoted-AND tokenization, pagination 25/10 → 10/10/5, chips counts | `internal/application/{category_service,search_service}.go` + tests | 3.1 | Duplicate category name rejected; rename to dup rejected; referenced category delete rejected; 4-filter AND composition (ticket-search); FTS chars in `q` never 500; stable pagination no overlap | `feat(application): category + search use cases` | 280 |

## Phase 4: SQLite adapter (real modernc driver)

| ID | Task | Files touched | Deps | Acceptance (spec) | Commit unit | Est. lines |
|----|------|---------------|------|-------------------|-------------|------------|
| [x] 4.1 | RED+GREEN: `sqlite.go` Open — single DSN `file:app.db?_pragma=foreign_keys(1)&journal_mode(WAL)&busy_timeout(5000)&_txlock=immediate` (D1, D8); `migrate.go` embedded runner + `schema_migrations`; `migrations/0001_init.sql` (users, sessions, categories, tickets, comments, audit_events + indexes); `newTestDB(t)` helper `file::memory:?cache=shared` + `SetMaxOpenConns(1)` | `internal/adapters/sqlite/{sqlite,migrate}.go`, `migrations/0001_init.sql`, test helpers | 3.6 | Migrations run transactionally; rerun = no-op; bad FK (category/user_id) → error; shared-cache single pool no "no such table" flakes | `feat(sqlite): open, migrations runner, init schema` | 260 |
| [x] 4.2 | RED+GREEN: `ticket_store.go` — Create `COALESCE(MAX(number),0)+1` in `BEGIN IMMEDIATE` txn + UNIQUE retry (3×), Update, GetByID (NotFound), List/Count/CountsByState/CountsByPriority with shared filter-builder + D11 priority CASE + `ORDER BY created_at DESC, id DESC` | `internal/adapters/sqlite/ticket_store.go` + tests | 4.1 | Sequential numbers 1042→1043; 2-goroutine concurrent create → distinct numbers; FK violation error; chips reflect filtered set | `feat(sqlite): ticket store with atomic numbering` | 320 |
| [x] 4.3 | RED+GREEN: `comment_store.go` + `audit_store.go` — Add/ListByTicket ASC, Append (multi-event), ListByTicket ASC | `internal/adapters/sqlite/{comment_store,audit_store}.go` + tests | 4.1 | Timeline ASC; audit events persisted in occurrence order (audit-log history) | `feat(sqlite): comment + audit stores` | 160 |
| [x] 4.4 | RED+GREEN: `user_store.go` + `session_store.go` — Create (UNIQUE email → Duplicate), Update, Delete (referenced → Referenced), GetByID/GetByEmail/Count/List/ListActive; sessions Create/GetByID (expired → NotFound)/Delete + lazy purge | `internal/adapters/sqlite/{user_store,session_store}.go` + tests | 4.1 | Duplicate email → DuplicateError; delete referenced → ReferencedError; expired session lookup → NotFound; logout deletes row (user-management logout) | `feat(sqlite): user + session stores` | 260 |
| [x] 4.5 | RED+GREEN: `migrations/0002_fts.sql` — contentless FTS5 `tickets_fts` + 4 sync triggers (D3); `search_store.go` Search/SearchCount with `t.id IN (SELECT rowid FROM tickets_fts WHERE tickets_fts MATCH ?)` + D4 quoting | `internal/adapters/sqlite/{search_store.go,migrations/0002_fts.sql}` + tests | 4.2, 4.3 | Title+desc+comment cross-field match; edit "Old"→"New": search "Old" empty, "New" hits; `"`/`(`/`*`/`:` in `q` → no error; text AND-composes with filters | `feat(sqlite): FTS5 + search store` | 300 |
| [x] 4.6 | RED+GREEN: `category_store.go` — Create (UNIQUE name → Duplicate), Update, Delete (referenced → Referenced), GetByID/List; delete guard for referenced categories (category-management) | `internal/adapters/sqlite/category_store.go` + tests | 4.4 | Duplicate category name → DuplicateError; rename to duplicate → DuplicateError; delete referenced → ReferencedError; unreferenced deletable | `feat(sqlite): category store` | 140 |

## Phase 5: HTTP adapter (stdlib ServeMux, D9)

| ID | Task | Files touched | Deps | Acceptance (spec) | Commit unit | Est. lines |
|----|------|---------------|------|-------------------|-------------|------------|
| [x] 5.1 | RED+GREEN: `web/templates/templates.go` (go:embed FS) + `base.html`; `render.go` (HX-Request → fragment, else full page, D6); `errors.go` `mapError` (D5: 422/404/409/401/500); golden-file harness with injected clock (D7) | `web/templates/{templates.go,base.html}`, `internal/adapters/http/{render,errors}.go` + tests, `testdata/*.golden` | 4.5 | `HX-Request` present → fragment only (no `<html>`); absent → full page; Validation→422, NotFound→404, Duplicate/Referenced→409, InvalidCredentials→401 generic, unknown→500 | `feat(http): render + error mapping + golden harness` | 280 |
| [x] 5.2 | RED+GREEN: `middleware_auth.go` — cookie token → `SessionStore.GetByID`; missing/expired → 303 `/login`; `UserCount()==0` → 303 `/setup` except `/setup*`; exempt `/login*`,`/setup*`,`/healthz`; authed user on `/login` → 303 `/tickets`; Origin check on POST (D17) | `internal/adapters/http/middleware_auth.go` + tests | 5.1 | No cookie → 303 `/login`; expired/forged token → 303 `/login`; empty users table → all routes 303 `/setup`; with users, `/setup` unavailable; cross-site Origin POST → 403 (threat matrix) | `feat(http): session middleware + bootstrap gating` | 200 |
| [x] 5.3 | RED+GREEN: `handlers_auth.go` + `login.html`, `setup.html` — GET/POST `/login`, POST `/logout`, GET/POST `/setup` (D16) | `internal/adapters/http/handlers_auth.go`, `web/templates/pages/{login,setup}.html` + tests | 5.2 | Correct login → 303 + `Set-Cookie`; wrong pw / unknown email / deactivated → same generic 401 re-render; logout → session row gone + cookie cleared + next request 303 `/login`; empty users → setup creates first active regular user → 303 `/login` (user-management) | `feat(http): auth handlers + login/setup templates` | 200 |
| [x] 5.4 | RED+GREEN: `handlers_tickets.go` list+create — GET `/tickets` (canonical filters + page), GET `/tickets/new`, POST `/tickets`; templates `tickets_index`, `tickets_new`, partials `ticket_list`, `ticket_form`, `filter_form`, `pagination`, `state_badge`, `timestamp` | `internal/adapters/http/handlers_tickets.go`, `web/templates/{pages,partials}/...` + tests + goldens | 5.3 | Create valid → list refreshed (HX) / 303 → detail (full); 422 English error re-renders form; filters compose; no duplicate summary chips/OOB; pagination 10/10/5; ID heading and human labels | `feat(http): ticket list + create handlers + templates` | 300 |
| [x] 5.5 | RED+GREEN: inline detail+transition+comments — GET `/tickets/{id}`, fallback GET `/tickets/{id}/edit`, POST edit/transition/comments; `ticket_detail` contains the Properties editor and state controls; merged `timeline` is newest-first | `internal/adapters/http/handlers_tickets.go`, `web/templates/{pages,partials}/...` + tests + goldens | 5.4 | Transition happy path + invalid/reopen; 400/404 IDs; comments and audit interleave newest-first with distinct styling; inline edit → 303 → detail; semantic human UTC timestamps | `feat(http): ticket detail + transition + comment handlers + templates` | 320 |
| [x] 5.6 | RED+GREEN: `handlers_users.go` + `handlers_categories.go` — `/users` CRUD (create/update incl. password + deactivate, delete → 409 when referenced), `/categories` CRUD; templates `users_index`, `categories_index`, `user_form`, `category_form` | `internal/adapters/http/{handlers_users,handlers_categories}.go`, `web/templates/...` + tests | 5.3 | User create/update/delete happy paths; duplicate email/name → 409 message; delete referenced user/category → 409; deactivate kills active sessions (D14) | `feat(http): users + categories handlers + templates` | 300 |

## Phase 6: Composition root + delivery

| ID | Task | Files touched | Deps | Acceptance (spec) | Commit unit | Est. lines |
|----|------|---------------|------|-------------------|-------------|------------|
| [x] 6.1 | RED+GREEN: `cmd/server/main.go` — env (DB path, listen addr), `sqlite.Open` → `Migrate` → wire services + http server; GET `/healthz` 200 "ok" exempt from auth (D12); `-healthcheck` flag (DB `SELECT 1`, exit 0/1) | `cmd/server/main.go` + smoke test | 5.6 | `go build ./...` clean; `-healthcheck` exits 0 on healthy DB, 1 on failure | `feat(cmd): composition root + healthcheck` | 150 |
| [x] 6.2 | `Dockerfile` (multi-stage, `CGO_ENABLED=0`, distroless static), `.dockerignore`, `docker-compose.yml` (build, `/data` volume, exec-form HEALTHCHECK using `-healthcheck`) | `Dockerfile`, `.dockerignore`, `docker-compose.yml` | 6.1 | `docker compose up -d --build` boots; healthcheck passes; `curl /healthz` → 200 `ok` (verified in sdd-verify) | `chore(docker): multi-stage build + compose` | 90 |
| [x] 6.3 | Final verification pass: `gofmt -l .` empty, `go vet ./...` clean, `go test ./...` green, goldens regenerated + rerun stable, `go build ./...` | repo-wide (no new code) | 6.2 | All success criteria from proposal: matrix, FTS5, login flows, middleware, bootstrap, Docker healthcheck | `chore(verify): final verification pass` | 0 |

**Total estimated changed lines: ~5,040** (all Create; goldens included in snapshot identity, excluded from authored-risk count).

## Delivery Notes

- Strict TDD: every production task carries its `_test.go` in the same commit (work-unit rule: tests with code).
- Chain strategy not yet chosen (`pending`): orchestrator MUST ask the user (stacked-to-main vs feature-branch-chain vs size:exception) before apply — `Decision needed before apply: Yes`.
- If Feature Branch Chain chosen: PR #1 base = feature/tracker branch; PR #2 base = PR #1 branch; …; only tracker merges to main.
- Migration runner + additive 0001/0002 make rollback a branch/container drop (main stays at bootstrap commit).
