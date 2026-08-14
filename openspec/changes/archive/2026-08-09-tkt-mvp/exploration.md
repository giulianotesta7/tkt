# Exploration: tkt — Greenfield Go Ticketing System (MVP)

> Phase: explore | Change: `tkt-mvp` | Date: 2026-08-05
> Status: exploration complete — ready for `propose`

## Current State

Greenfield repo. `git` initialized on `main` (commit `0a0849e`, "chore: bootstrap SDD openspec store and gitignore"). No `go.mod`, no source code. Existing content:

- `openspec/config.yaml` — SDD config: schema `spec-driven`, strict TDD (`go test ./...`), SQLite driver decided during design, Docker multi-stage, stdlib testing, gofmt/go vet.
- `openspec/specs/` — empty (no main specs yet).
- `openspec/changes/` — empty (archive/ exists).
- `.gitignore` — already ignores `/bin/`, `*.db`, `*.db-wal`, `*.db-shm`, `.env`. Good foresight: the SQLite files will land in a runtime volume, and `/bin/` is the natural local build output.
- `.atl/` — skill registry cache, not part of the product.

Environment verified:
- Go `1.25.11` (`X:nodwarf5`, linux/amd64) installed locally.
- Docker `29.6.0` available.
- `openspec/config.yaml` requires: decide SQLite driver in design with rationale; proposal must include rollback plan for risky changes; specs use Given/When/Then + RFC 2119.

Scope is the consolidated user-approved MVP (ticket aggregate with readable number, explicit 5-state machine, agents, free-text requester, one comment type, audit log, category CRUD, fixed priorities, search/filters/pagination, list-view summary chips; out of scope: login/roles, attachments, notifications, SLAs, saved filters, dedicated dashboard).

## Affected Areas (all to be created)

- `go.mod` / `go.sum` — module scaffold; module path `github.com/giulianotesta7/tkt` (remote exists).
- `cmd/server/main.go` — composition root: DB open, migrations, handler wiring, server start.
- `internal/domain/` — ticket aggregate, state machine, enums (priority), errors. Zero external deps.
- `internal/application/` — use cases (create/transition/comment/filter services), store port interfaces.
- `internal/adapters/sqlite/` — store implementations + migrations.
- `internal/adapters/http/` — handlers, router (stdlib `http.ServeMux` with method+path patterns, Go 1.22+), rendering.
- `web/templates/` — html/template files for SSR (embedded via `go:embed`).
- `Dockerfile` / `docker-compose.yml` — multi-stage build + runtime with SQLite volume.
- `openspec/changes/tkt-mvp/` — this exploration, then proposal/spec/design/tasks/verify artifacts.

## Approaches

### 1. Project structure: hexagonal-lite (domain / application / adapters)

```
cmd/server/                 # composition root only
internal/domain/            # pure Go: entities, state machine, errors — no I/O, no deps
  ticket.go  state.go  priority.go  errors.go
internal/application/       # use cases: port interfaces (store), business flow, no HTTP/SQL
  tickets.go  comments.go  agents.go  categories.go  search.go
internal/adapters/
  sqlite/                   # store implementations + migrations + go:embed SQL
  http/                     # handlers, router, render, middleware
web/templates/              # html/template files (embedded)
```

- Pros: domain state machine testable with zero mocks; port interfaces let handlers test against an in-memory store; stdlib `net/http` fits naturally as the outermost adapter; matches Go's `internal/` convention to prevent external import; clean seams for later iterations (auth, attachments).
- Cons: more files/indirection than a flat layout for a small app; must resist over-abstraction (one port interface per aggregate, not per method).
- Effort: Medium.

Alternative considered: **flat feature layout** (`internal/tickets/`, `internal/agents/`…) — less ceremony but mixes domain+store+HTTP per feature; state machine invariants harder to isolate; rejected for MVP because the domain-enforced state machine is the heart of the product.

Alternative considered: **full hexagonal with DTOs per layer** — overkill for ~6 aggregates and no external consumers; adds mapping boilerplate the MVP doesn't need.

### 2. SQLite driver: `modernc.org/sqlite` (pure Go) vs `mattn/go-sqlite3` (cgo)

**Verified fact (2026-08-05, in /tmp/tkt-fts5-check): FTS5 works out of the box on `modernc.org/sqlite` v1.56.0** — `CREATE VIRTUAL TABLE ... USING fts5(...)` and `MATCH` queries succeed. This removes the main historical objection to the pure-Go driver.

| | modernc.org/sqlite v1.56.0 | mattn/go-sqlite3 v1.14.49 |
|---|---|---|
| cgo | none — pure Go (CGO_ENABLED=0) | required (CGO_ENABLED=1, needs gcc in build) |
| Static build | trivial: `CGO_ENABLED=0 go build` | needs gcc; dynamic glibc linkage; cross-compile painful |
| Runtime image | `scratch` or distroless static, no libc | distroless base-debian / alpine with libc |
| FTS5 | verified working (v1.56.0) | available, needs build-tag variant flags in some configs |
| DSN | `file:app.db?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)` | `file:app.db?_foreign_keys=on&_journal_mode=WAL` |
| Maturity | mature, active (transpiled SQLite amalgamation) | the classic driver, very battle-tested |
| Perf | ~1.5–2× slower than mattn on heavy workloads (irrelevant at MVP scale) | fastest |

- Docker multi-stage impact: modernc → static binary → `FROM scratch` or `gcr.io/distroless/static-debian12`; no build deps beyond Go. mattn → build stage needs `gcc`/`build-essential`, runtime needs glibc, and any musl/glibc mismatch bites. For "simple but robust" delivery, modernc is the clear fit.
- Recommendation: **modernc.org/sqlite**, with the DSN above (WAL + busy_timeout + foreign_keys per connection). Fallback documented: mattn remains viable if a perf issue ever appears, port = change import + DSN, stores stay behind the port interface.

### 3. State machine: enum + transition table (no external libs)

- Pattern: `type State string` with constants (`nuevo`, `en_progreso`, `resuelto`, `cerrado`, `cancelado`); a `var transitions = map[State]map[State]bool` adjacency table; `func (s State) CanTransitionTo(next State) bool`; the `Ticket.Transition(next, reason, actor)` method validates the table, enforces the mandatory-reason rule for reopen (`cerrado→en_progreso`), sets `resolved_at`/`closed_at` **only** here, and appends an audit event.
- Reject at domain level: return a typed `ErrInvalidTransition{From, To}` — handlers map it to 422 with the Spanish message. Invalid pairs (`nuevo→cerrado`, `resuelto→cancelado`, `cerrado→cancelado`, …) never reach SQL.
- `resolved_at`/`closed_at` are unexported-set fields; cleared on reopen.
- No external lib: a transition table is ~30 lines and fully testable; libs (looplab/fsm, go-state-machine) add dependency without value here.
- TDD: exhaustive table-driven test over all 5×5 pairs asserting allowed/denied; invariant tests (timestamps set only by machine, reopen requires reason, audit row written).
- Effort: Low.

### 4. Schema draft + migrations

```sql
-- 0001_init.sql
CREATE TABLE agents (
  id         INTEGER PRIMARY KEY,
  name       TEXT NOT NULL,
  email      TEXT NOT NULL,
  active     INTEGER NOT NULL DEFAULT 1,          -- 0/1
  created_at TEXT NOT NULL
);

CREATE TABLE categories (
  id         INTEGER PRIMARY KEY,
  name       TEXT NOT NULL UNIQUE,
  created_at TEXT NOT NULL
);

CREATE TABLE tickets (
  id              INTEGER PRIMARY KEY,            -- internal PK
  number          INTEGER NOT NULL UNIQUE,        -- TKT-1042 -> number=1042 (readable, sequential)
  title           TEXT NOT NULL,
  description     TEXT NOT NULL DEFAULT '',
  requester_name  TEXT NOT NULL,
  requester_email TEXT NOT NULL,
  category_id     INTEGER REFERENCES categories(id),
  priority        TEXT NOT NULL CHECK (priority IN ('baja','media','alta','critica')),
  state           TEXT NOT NULL CHECK (state IN ('nuevo','en_progreso','resuelto','cerrado','cancelado')),
  agent_id        INTEGER REFERENCES agents(id),  -- nullable = unassigned
  created_at      TEXT NOT NULL,
  updated_at      TEXT NOT NULL,
  resolved_at     TEXT,                            -- set ONLY by state machine
  closed_at       TEXT                             -- set ONLY by state machine
);
CREATE INDEX idx_tickets_state_created ON tickets(state, created_at DESC);

CREATE TABLE comments (
  id         INTEGER PRIMARY KEY,
  ticket_id  INTEGER NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
  author     TEXT NOT NULL,
  body       TEXT NOT NULL,
  created_at TEXT NOT NULL
);
CREATE INDEX idx_comments_ticket ON comments(ticket_id, created_at);

CREATE TABLE audit_events (
  id         INTEGER PRIMARY KEY,
  ticket_id  INTEGER NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
  actor      TEXT NOT NULL,                        -- agent name / requester name (no users table)
  action     TEXT NOT NULL,                        -- 'transition' | 'update'
  field      TEXT,                                 -- for updates: 'title','priority','agent_id',...
  from_value TEXT,
  to_value   TEXT,
  created_at TEXT NOT NULL
);
CREATE INDEX idx_audit_ticket ON audit_events(ticket_id, created_at);

-- 0002_fts.sql — full-text search over title/description/comments
CREATE VIRTUAL TABLE tickets_fts USING fts5(
  title, description, comments, content='', tokenize='unicode61'
);
-- triggers keep tickets_fts in sync on tickets/comments insert/update/delete
```

Design notes:
- **Numbering**: store `number` (sequential int) separate from `id`; format `TKT-%d` at render. Sequential per session is fine; use `MAX(number)+1` inside the create transaction (simple, robust at this scale; audit/logs reference `id`).
- **Timestamps**: TEXT ISO-8601 UTC (`2006-01-02T15:04:05Z`), set in Go — avoids SQLite `datetime()` TZ surprises and keeps golden files deterministic.
- **Priority ordering** for sorting/filtering: `CHECK` guards values but not order; application maps rank (`baja=1..critica=4`) or uses `CASE` in ORDER BY — decide in design (CASE is zero-code, fine).
- **Migrations**: embedded SQL files via `go:embed` + a ~60-line runner (versioned files `NNNN_name.sql`, `schema_migrations` table, transactional apply, `PRAGMA user_version` optional). No framework, no goose/golang-migrate dependency — matches "simple but robust".
- `PRAGMA foreign_keys=ON` MUST be set per connection (SQLite default is off) — done via DSN `_pragma=foreign_keys(1)`.

### 5. HTMX SSR layout

- One layout (`base.html`) + partials: `ticket_list`, `ticket_form`, `ticket_detail`, `comment_form`, `comment_list`, `audit_timeline`, `filter_form`, `pagination`, `summary_chips`.
- Routing with stdlib `http.ServeMux` method+path patterns (`GET /tickets/{id}`, `POST /tickets/{id}/transition`, …) — Go 1.22+ native, no router lib.
- HTMX flows:
  - Create ticket → `POST /tickets` → `hx-post` + `hx-target` list → swap list + chips + form reset (OOB swap for chips).
  - Transition → `POST /tickets/{id}/transition` → re-render detail card (state badge + buttons swap).
  - Comment → `POST /tickets/{id}/comments` → prepend to timeline.
  - Filters → `GET /tickets?state=&priority=&category=&agent=&q=&page=` → `hx-get` on the filter form, swap list container; pagination via `hx-get` with page param.
- Non-HTMX fallback: same handlers render full pages when `HX-Request` header absent — keeps golden-file testing trivial and browser refresh correct.
- Golden-file testing: render each partial with fixed fixture data (deterministic timestamps) and compare against `testdata/*.golden`; `-update` flag regenerates (per go-testing skill).

### 6. Testing strategy (stdlib only)

| Layer | Pattern | Tooling |
|---|---|---|
| Domain (state machine, priority, number) | table-driven unit tests, exhaustive 5×5 transition matrix | stdlib testing |
| Application (use cases) | unit tests with store-port fakes (small interfaces) | stdlib testing |
| SQLite store | real driver against in-memory SQLite (`file::memory:?cache=shared` per-test, migrations applied) | stdlib + modernc |
| HTTP handlers | `httptest.NewServer`/`NewRecorder` + real in-memory store, assert status/location/HTML fragments | stdlib net/http/httptest |
| Templates | golden files (`-update` flag), fixed fixture data, no `time.Now()` in render path | stdlib |

- In-memory SQLite: use `file::memory:?cache=shared` with a single connection pool (`db.SetMaxOpenConns(1)` semantics or shared-cache DSN) so all statements see one DB.
- Golden files must be deterministic: inject clock, no locale-dependent formatting, sorted iteration order in fixtures.
- Strict TDD active: `go test ./...` must pass; `go vet ./...` clean (config: linter `go vet`, coverage threshold 0).

### 7. Docker multi-stage

```dockerfile
FROM golang:1.25.11 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server

FROM gcr.io/distroless/static-debian12
COPY --from=build /out/server /server
# templates + migrations are go:embed — nothing else to copy
ENV TKT_DB_PATH=/data/tkt.db
VOLUME /data
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=3s CMD ["/server", "-healthcheck"]  # or wget-less probe via /healthz
USER nonroot
ENTRYPOINT ["/server"]
```

- Build: static, no cgo (modernc) → no gcc, no glibc; distroless static is enough.
- Compose: `tkt` service, `tkt-data` named volume on `/data`, `healthcheck` against `/healthz` (either binary flag or the small endpoint), restart unless-stopped. `.gitignore` already excludes `*.db*` — the volume lives outside the repo anyway.
- Healthcheck note: distroless has no `wget`/`curl`; use a tiny `/healthz` endpoint + the binary flag approach or install busybox only if needed. Decide in design.

## Recommendation

1. **Structure**: hexagonal-lite (`cmd/server` + `internal/domain|application|adapters`) — the domain-enforced state machine needs a dependency-free home; ports keep handlers testable; stdlib `net/http` as the outer adapter.
2. **Driver**: `modernc.org/sqlite` v1.56.0 (pure Go). Verified FTS5 support; static Docker builds with zero build deps; perf delta irrelevant at MVP scale.
3. **State machine**: enum + transition table, no libs; timestamps set only inside `Transition`; reopen-with-reason enforced in domain.
4. **Schema/migrations**: embedded versioned SQL + tiny runner; `PRAGMA foreign_keys=ON` via DSN; FTS5 virtual table maintained by triggers.
5. **HTMX SSR**: stdlib ServeMux patterns, partials + OOB swaps, full-page fallback when no `HX-Request`, golden-file tested.
6. **Docker**: `golang:1.25.11` → `distroless/static-debian12`, embedded templates, `/data` volume, healthcheck.

## Risks

- **Greenfield scaffold**: no go.mod yet — `go mod init github.com/giulianotesta7/tkt` + first `go get` of modernc are part of the change; module download latency on first build.
- **modernc.org/sqlite**: pure-Go perf slower than cgo (fine here); FTS5 verified on v1.56.0 but pin the version to avoid regressions; first compile is slow (~minutes) due to its size — cache layers in Docker build.
- **In-memory SQLite in tests**: shared-cache DSN footguns (per-connection DBs) — set pool semantics carefully or tests flake with "no such table".
- **foreign_keys default OFF**: any missed connection DSN drops FK integrity silently — centralize DSN construction.
- **Timestamps**: naive `time.Now()` breaks golden tests and audit determinism — inject a clock.
- **HTMX + partial swap contract**: handlers must consistently answer fragments vs full pages; golden files guard regressions but fixtures must be frozen.
- **Numbering**: `MAX(number)+1` race under concurrency — SQLite single-writer + `BEGIN IMMEDIATE` or UNIQUE retry; simple and safe at MVP scale.
- **Priority ordering**: CHECK constraint gives no order semantics — CASE mapping must be consistent in list + filters.
- **Rollback**: config requires rollback plan in proposal — mitigate with the store-port seam (driver/store swap) and backward-compatible migrations.

## Ready for Proposal

**Yes.** Exploration verified the decisive unknowns (FTS5 on modernc, toolchain versions, repo state). The proposal should confirm: change name `tkt-mvp`, module path `github.com/giulianotesta7/tkt`, driver = modernc, hexagonal-lite structure, and the schema/migration approach above — then move to spec (requirements + Given/When/Then scenarios).
