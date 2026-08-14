# Design: tkt — Ticket Management MVP

## Technical Approach

Hexagonal-lite SSR app (Go 1.25.11, stdlib `net/http`, html/template + HTMX, modernc.org/sqlite v1.56.0). Pure domain (state machine, invariants) → application use cases behind store ports → sqlite/http adapters. Every spec requirement lands in exactly one layer: transitions/validations in `internal/domain`, orchestration/audit/auth in `internal/application`, persistence in `internal/adapters/sqlite` (embedded SQL migrations + FTS5 triggers), presentation in `internal/adapters/http` (HX-aware rendering, session middleware). Authentication: server-side sessions backed by a SQLite `sessions` table, bcrypt password hashing, first-user bootstrap. Strict TDD (`go test ./...`), injected clock, golden files.

## Architecture Decisions

| # | Decision | Choice | Alternatives | Rationale |
|---|----------|--------|--------------|-----------|
| D1 | SQLite driver | `modernc.org/sqlite` v1.56.0 pinned | mattn/go-sqlite3 (cgo) | Pure Go: `CGO_ENABLED=0` static build, distroless runtime, no gcc; FTS5 verified live in exploration. Perf delta (~1.5–2×) irrelevant at MVP scale. Store port keeps mattn swappable. |
| D2 | Pagination | `LIMIT/OFFSET`, page size FIXED at 10 (no `TKT_PAGE_SIZE` env) | Keyset (`created_at < cursor`); configurable size | Page-number UI fits offset; stable tiebreaker `id DESC` guarantees no overlap. Fixed 10 keeps the MVP surface small and deterministic. |
| D3 | FTS sync | Contentless FTS5 (`content=''`) + AFTER triggers on tickets/comments | App-level dual write; manual rebuild | Triggers are atomic with the write transaction — edit→search consistency guaranteed, zero store code; contentless avoids duplicated storage; `snippet()` not needed (title/desc rendered from `tickets`). |
| D4 | FTS query construction | Tokenize user input, double-quote each token (escape `"`), join `AND` | Raw `MATCH ?` pass-through | Raw user text is FTS5 syntax — `"a OR b`, `(x`, `*` would error or change semantics. Quoted-AND matches words only; invalid input degrades to no text filter, never a 500. |
| D5 | Error mapping | Typed domain errors (English messages, single source in domain) → status table in http adapter | Spanish errors; generic 500s | UI language is English; status mapping centralized in one `mapError` function; typed errors carry structured data (From/To, Field, ID). Login failures map to a single generic 401 (no user enumeration). |
| D6 | HTMX contract | `HX-Request` header present → render fragment only; absent → full page | Fragments only; full page only | Same handler serves both; browser refresh works; golden files cover both render paths; no client router. |
| D7 | Timestamps | Persist RFC3339 UTC TEXT; display `15:04 · 02-01-2006` UTC inside semantic `<time datetime="RFC3339">`; `domain.Clock` injected | SQLite `datetime()`; `time.Now()` inline | RFC3339 persistence sorts lexicographically and preserves machine semantics; human text is concise; deterministic render helpers never call `time.Now()`. |
| D8 | Numbering | `_txlock=immediate` DSN + `COALESCE(MAX(number),0)+1` in create txn, UNIQUE backstop + retry (3×) | SQLite AUTOINCREMENT seq column | `_txlock=immediate` serializes writers (modernc DSN param) → race-free MAX+1; UNIQUE + retry as belt-and-suspenders; `number` independent of internal `id`. |
| D9 | Routing | stdlib `http.ServeMux` Go 1.22 method+path patterns | chi/gorilla/mux | Zero deps; `GET /tickets/{id}` + `POST /tickets/{id}/transition` natively; fully testable. |
| D10 | Migrations | Embedded versioned SQL (`NNNN_name.sql`) + ~60-line runner + `schema_migrations` table | goose/golang-migrate | Zero deps; transactional apply; additive-only; FK pragma via DSN (single `sqlite.Open` — no footguns). |
| D11 | Priority ordering | `CASE priority WHEN 'critical' THEN 4 …` in ORDER BY | Rank column in schema | Single shared SQL fragment constant in the adapter; no schema duplication; CHECK keeps values honest. |
| D12 | Healthcheck | `-healthcheck` binary flag (DB `SELECT 1`, exit 0/1) + `GET /healthz` endpoint | busybox wget/curl | Distroless static has no shell or curl; exec-form HEALTHCHECK runs the binary directly. `/healthz` is exempt from the session middleware (probe without login). |
| D13 | View composition | Store ports return `domain` types; application composes `TicketView` and enriches `TimelineItem` labels via ref lookups | Store returns joined/presentation views; templates parse IDs | Ports and immutable audit rows stay domain-typed. The application resolves historical user/category IDs to names with per-view caches and safe unknown-reference labels, keeping templates presentation-only. |
| D14 | Sessions | Opaque random token (32 bytes) in cookie → server-side `sessions` table in SQLite; TTL 24h fixed | Signed cookie (HMAC); JWT; in-memory map | Server-side rows give real logout (delete row) and instant deactivation enforcement — a signed cookie/JWT cannot be revoked and needs a secret to manage; in-memory map loses sessions on restart and breaks multi-replica. Opaque token ⇒ no `TKT_SESSION_SECRET` env var, no signing deps. Single-instance MVP scale; row lookup is one indexed PK read. |
| D15 | Password hashing | bcrypt via `golang.org/x/crypto/bcrypt`, default cost 10 | SHA-256; argon2id | Per-user salt; `CompareHashAndPassword` does constant-time comparison; no key-management burden. Argon2id's tuning complexity buys little at MVP scale; cost 10 is the library default (verify-only DB work per login). |
| D16 | First-user bootstrap | `GET/POST /setup` offered only while `users` table is empty; middleware redirects all routes → `/setup` when empty | Seeded default admin (env/DDL) | No hardcoded credentials in the image or DB; the operator creates the first account through the UI; first user is a regular user (no roles). Flow disappears once ≥1 user exists — the system is never locked out, and bootstrap is never a backdoor. |
| D17 | CSRF on HTMX forms | `SameSite=Strict` cookie + `Origin` header check on all unsafe methods (POST) | CSRF token library (gorilla/csrf) | The app is same-origin by design (HTMX + SSR, no third-party embeds); Strict blocks cross-site cookie sending entirely; Origin check rejects cross-site forged forms even if Strict is bypassed (browser quirks). No token plumbing in templates/JS at MVP scale. |

## Data Flow

```
Browser (HTMX) ──GET/POST──▶ http.ServeMux (Go 1.22 patterns)
        │                              │ requireSession middleware:
        │                              │  no/expired session → 303 /login
        │                              │  users table empty → 303 /setup
        │                              ▼
        │                 application use case (service)
        │                              │ domain invariants (state machine,
        │                              │ validation, reopen rules)
        │                              ▼
        │                       domain aggregate
        │                              │ store ports (interfaces)
        │                              ▼
        │                     sqlite adapter (BEGIN IMMEDIATE txn,
        │                     triggers sync FTS5 atomically)
        │                              │
        │                              ▼
        │                        SQLite (WAL)
        │                              │
        └── HTML fragment (HX-Request) ◀── html/template (go:embed) ──┘
            or full page (no HX-Request)
```

Login flow: `POST /login` (email+password) → `AuthService.Login` → `UserStore.GetByEmail` + `bcrypt.CompareHashAndPassword` → `SessionStore.Create` (random token, user_id, expires_at=now+24h) → `Set-Cookie` (HttpOnly, Secure, SameSite=Strict) → 303 `/tickets`. Wrong password / unknown email → same generic 401 error on the login form. `POST /logout` → `SessionStore.Delete(token)` + cookie clear → 303 `/login`.

Actor wiring: use cases receive the session user (resolved by middleware) and stamp `AuditEvent.Actor` / `Comment.Author` with it. No optional author form field, no `"sistema"` fallback except genuine system actions (none in MVP).

Reopen flow (`closed → in_progress`): handler → `TicketService.Transition` → `t.Transition(to, reason, now)` validates table + reason (domain), clears `resolved_at`+`closed_at`, returns `*AuditEvent{action:'transition', from:'closed', to:'in_progress', note:reason}` → use case stamps actor from session, persists ticket + audit event in one store transaction → fragment re-render. No path touches SQL before the domain says the transition is legal.

## Package Layout

```
cmd/server/                     # composition root: env, sqlite.Open, Migrate, wire, serve; -healthcheck flag
internal/domain/                # PURE — zero imports beyond stdlib; no I/O
  ticket.go state.go priority.go comment.go audit.go user.go category.go
  errors.go                     # typed errors + English message constants
  clock.go                      # Clock interface
internal/application/           # imports domain only; use cases + ports
  ports.go                      # store interfaces, TicketQuery, Page
  ticket_service.go comment_service.go user_service.go auth_service.go category_service.go
  search_service.go views.go password.go    # password.go: bcrypt Hash/Verify (isolated, testable)
internal/adapters/sqlite/       # imports application+domain; modernc driver
  sqlite.go                     # Open(): THE single DSN + pragma + _txlock=immediate
  migrate.go                    # go:embed runner; schema_migrations
  migrations/0001_init.sql 0002_fts.sql
  ticket_store.go comment_store.go audit_store.go user_store.go session_store.go
  category_store.go search_store.go
internal/adapters/http/         # imports application+domain; net/http
  server.go router.go render.go errors.go middleware_auth.go
  handlers_tickets.go handlers_users.go handlers_auth.go handlers_categories.go
web/templates/                  # go:embed via tiny embed package
  templates.go                  # //go:embed pages/*.html partials/*.html; var FS embed.FS
  base.html pages/*.html partials/*.html     # + login.html setup.html
Dockerfile .dockerignore docker-compose.yml go.mod go.sum
```

Dependency direction: `cmd/server → http → application → domain`; `sqlite → application → domain`. Domain never imports adapters; adapters only via ports. Note: `//go:embed` cannot cross package dirs, so `web/templates/templates.go` is the embed package the http adapter imports.

## Domain Model

```go
type State string
const (StateNew State = "new"; StateInProgress State = "in_progress"
       StateResolved State = "resolved"; StateClosed State = "closed"
       StateCancelled State = "cancelled")

type Priority string
const (PriorityLow Priority = "low"; PriorityMedium = "medium"
       PriorityHigh = "high"; PriorityCritical = "critical")
func (p Priority) Rank() int // critical=4 … low=1 (tests)

type Ticket struct {
  ID int64; Number int
  Title, Description, RequesterName, RequesterEmail string
  CategoryID int64; UserID *int64              // nil = unassigned; FK users.id
  Priority Priority; State State
  CreatedAt, UpdatedAt time.Time
  ResolvedAt, ClosedAt *time.Time              // set ONLY here
}
func (t *Ticket) Transition(to State, reason string, now time.Time) (*AuditEvent, error)
func (t *Ticket) ApplyUpdate(u TicketUpdate, now time.Time) ([]AuditEvent, error) // audit only for changed fields

type AuditEvent struct { TicketID int64; Actor, Action string; Field, FromValue, ToValue, Note *string; CreatedAt time.Time }
type Comment struct { ID, TicketID int64; Author, Body string; CreatedAt time.Time } // Author = session user
type User struct { ID int64; Name, Email, PasswordHash string; Active bool; CreatedAt time.Time } // no role field
type Session struct { ID string; UserID int64; ExpiresAt time.Time }  // ID = opaque 32-byte random token
type Category struct { ID int64; Name string; CreatedAt time.Time }
type Clock interface{ Now() time.Time }
```

Transition table (`var transitions map[State]map[State]bool`): `new→{in_progress,resolved,cancelled}`; `in_progress→{resolved,cancelled}`; `resolved→{closed,in_progress}` (reopen, no reason); `closed→{in_progress}` (reopen, **reason required**); `cancelled→{}` (terminal).

Timestamp semantics inside `Transition` only: →`resolved` sets `ResolvedAt`; →`closed` sets `ClosedAt`; `resolved→in_progress` clears `ResolvedAt`; `closed→in_progress` clears **both**; edits never touch either. Reopen reason required ⇒ `ReopenReasonRequiredError`; illegal pair ⇒ `InvalidTransitionError{From,To}`.

Errors (domain, English message constants): `ValidationError{Field,Message}` (422), `InvalidTransitionError` (422), `ReopenReasonRequiredError` (422), `InactiveUserError` (422), `InvalidPriorityError` (422), `NotFoundError{Kind,ID}` (404), `DuplicateError{Kind,Name}` (409), `ReferencedError{Kind,ID}` (409), `InvalidCredentialsError` (application-level, single generic 401 for wrong password and unknown email).

## Store Ports (`internal/application/ports.go`)

```go
type TicketStore interface {
  Create(ctx, t *domain.Ticket) error            // assigns Number atomically (MAX+1 in txn)
  Update(ctx, t *domain.Ticket) error            // fields + state + timestamps
  GetByID(ctx, id int64) (*domain.Ticket, error) // ErrNotFound
  List(ctx, q TicketQuery, p Page) ([]domain.Ticket, error)
  Count(ctx, q TicketQuery) (int, error)
  CountsByState(ctx, q TicketQuery) (map[domain.State]int, error)      // retained aggregate capability
  CountsByPriority(ctx, q TicketQuery) (map[domain.Priority]int, error)
}
type SearchStore interface {
  Search(ctx, q TicketQuery, p Page) ([]domain.Ticket, error)
  SearchCount(ctx, q TicketQuery) (int, error)
}
type CommentStore interface {
  Add(ctx, c *domain.Comment) error
  ListByTicket(ctx, ticketID int64) ([]domain.Comment, error) // ASC by created_at
}
type AuditStore interface {
  Append(ctx, events ...domain.AuditEvent) error
  ListByTicket(ctx, ticketID int64) ([]domain.AuditEvent, error) // ASC
}
type UserStore interface {
  Create(ctx, u *domain.User) error              // ErrDuplicate on email
  Update(ctx, u *domain.User) error
  Delete(ctx, id int64) error                    // ErrReferenced if assigned to tickets
  GetByID(ctx, id int64) (*domain.User, error)   // incl. inactive (historical display)
  GetByEmail(ctx, email string) (*domain.User, error) // ErrNotFound
  Count(ctx) (int, error)                        // first-user bootstrap check
  List(ctx) ([]domain.User, error); ListActive(ctx) ([]domain.User, error)
}
type SessionStore interface {
  Create(ctx, s *domain.Session) error
  GetByID(ctx, id string) (*domain.Session, error) // ErrNotFound if missing/expired
  Delete(ctx, id string) error                      // logout + deactivation sweep
}
type CategoryStore interface {
  Create(ctx, c *domain.Category) error      // ErrDuplicate on name
  Update(ctx, c *domain.Category) error; Delete(ctx, id int64) error // ErrReferenced
  GetByID(ctx, id int64) (*domain.Category, error); List(ctx) ([]domain.Category, error)
}
type TicketQuery struct { State *domain.State; Priority *domain.Priority
  CategoryID, UserID *int64; Text string }
type Page struct { Offset, Limit int } // Limit FIXED 10 — no env knob (D2)
```

`AuthService` (application): `Login(ctx, email, password) (Session, error)` — GetByEmail → inactive → generic error → bcrypt verify → new opaque token session; `Logout(ctx, sessionID)`; `UserCount(ctx)` for bootstrap gating. `password.go` wraps `bcrypt.GenerateFromPassword` (cost 10) / `bcrypt.CompareHashAndPassword`.

## SQLite Schema

`0001_init.sql`:

```sql
CREATE TABLE users (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL CHECK(length(trim(name))>0),
  email TEXT NOT NULL UNIQUE CHECK(length(trim(email))>0),
  password_hash TEXT NOT NULL,
  active INTEGER NOT NULL DEFAULT 1 CHECK(active IN (0,1)),
  created_at TEXT NOT NULL);
CREATE TABLE sessions (
  id TEXT PRIMARY KEY,                    -- opaque 32-byte random token (hex)
  user_id INTEGER NOT NULL REFERENCES users(id),
  created_at TEXT NOT NULL, expires_at TEXT NOT NULL);
CREATE INDEX idx_sessions_expires ON sessions(expires_at);
CREATE TABLE categories (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL UNIQUE CHECK(length(trim(name))>0), created_at TEXT NOT NULL);
CREATE TABLE tickets (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  number INTEGER NOT NULL UNIQUE,
  title TEXT NOT NULL CHECK(length(trim(title))>0),
  description TEXT NOT NULL DEFAULT '',
  requester_name TEXT NOT NULL, requester_email TEXT NOT NULL,
  category_id INTEGER NOT NULL REFERENCES categories(id),
  priority TEXT NOT NULL CHECK(priority IN ('low','medium','high','critical')),
  state TEXT NOT NULL CHECK(state IN ('new','in_progress','resolved','closed','cancelled')),
  user_id INTEGER REFERENCES users(id),   -- assigned user; nil = unassigned
  created_at TEXT NOT NULL, updated_at TEXT NOT NULL, resolved_at TEXT, closed_at TEXT);
CREATE INDEX idx_tickets_state_created ON tickets(state, created_at DESC);
CREATE INDEX idx_tickets_priority_created ON tickets(priority, created_at DESC);
CREATE INDEX idx_tickets_category ON tickets(category_id);
CREATE INDEX idx_tickets_user ON tickets(user_id);
CREATE TABLE comments (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  ticket_id INTEGER NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
  author TEXT NOT NULL, body TEXT NOT NULL CHECK(length(trim(body))>0),
  created_at TEXT NOT NULL);
CREATE INDEX idx_comments_ticket ON comments(ticket_id, created_at);
CREATE TABLE audit_events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  ticket_id INTEGER NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
  actor TEXT NOT NULL, action TEXT NOT NULL,           -- 'created'|'transition'|'update'
  field TEXT, from_value TEXT, to_value TEXT, note TEXT,  -- note = reopen reason
  created_at TEXT NOT NULL);
CREATE INDEX idx_audit_ticket ON audit_events(ticket_id, created_at);
CREATE TABLE schema_migrations (
  version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL);
```

`0002_fts.sql` (contentless FTS5 + sync triggers; `tickets_fts.rowid` = ticket id):

```sql
CREATE VIRTUAL TABLE tickets_fts USING fts5(title, description, comments, content='', tokenize='unicode61');
CREATE TRIGGER trg_tickets_ai AFTER INSERT ON tickets BEGIN
  INSERT INTO tickets_fts(rowid,title,description,comments)
  VALUES (NEW.id,NEW.title,NEW.description,''); END;
CREATE TRIGGER trg_tickets_ad AFTER DELETE ON tickets BEGIN
  INSERT INTO tickets_fts(tickets_fts,rowid,title,description,comments)
  VALUES ('delete',OLD.id,OLD.title,OLD.description,''); END;
CREATE TRIGGER trg_tickets_au AFTER UPDATE OF title, description ON tickets BEGIN
  INSERT INTO tickets_fts(tickets_fts,rowid,title,description,comments)
  VALUES ('delete',OLD.id,OLD.title,OLD.description,'');
  INSERT INTO tickets_fts(rowid,title,description,comments)
  SELECT id,title,description,COALESCE((SELECT group_concat(body,' ') FROM comments WHERE ticket_id=id),'')
  FROM tickets WHERE id=NEW.id; END;
CREATE TRIGGER trg_comments_ai AFTER INSERT ON comments BEGIN
  INSERT INTO tickets_fts(tickets_fts,rowid,title,description,comments)
  VALUES ('delete',NEW.ticket_id,'','','');
  INSERT INTO tickets_fts(rowid,title,description,comments)
  SELECT id,title,description,COALESCE((SELECT group_concat(body,' ') FROM comments WHERE ticket_id=id),'')
  FROM tickets WHERE id=NEW.ticket_id; END;
CREATE TRIGGER trg_comments_ad AFTER DELETE ON comments BEGIN
  INSERT INTO tickets_fts(tickets_fts,rowid,title,description,comments)
  VALUES ('delete',OLD.ticket_id,'','','');
  INSERT INTO tickets_fts(rowid,title,description,comments)
  SELECT id,title,description,COALESCE((SELECT group_concat(body,' ') FROM comments WHERE ticket_id=id),'')
  FROM tickets WHERE id=OLD.ticket_id; END;
```

`group_concat(body,' ')` keeps each comment tokenized in the shared `comments` column; per-row rebuild on write is atomic with the txn (D3). Indexes serve filter+sort (`state`/`priority` + `created_at DESC`) and ref lookups; `number`, `users.email`, and `categories.name` get UNIQUE auto-indexes.

**Migrations**: `migrate.go` embeds `migrations/*.sql`, sorts by leading version, applies each in a transaction (`BEGIN IMMEDIATE`), records version in `schema_migrations`; rerun = no-op; failure = fatal exit (container restart). FK enforcement comes from the **single** DSN in `sqlite.go`:

```
file:app.db?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_txlock=immediate
```

Tests use `file::memory:?cache=shared` + `db.SetMaxOpenConns(1)` (single-pool semantics → no "no such table" flakes).

## Search / Filters

One query builder (shared constant SQL fragments) composes: `state AND priority AND category AND user` as `t.state=?`-style clauses plus optional text: `t.id IN (SELECT rowid FROM tickets_fts WHERE tickets_fts MATCH ?)`. Empty filter set → plain `List` (all tickets). Text: `q` → split on whitespace → each token double-quoted with `"` escaped → joined `AND`; empty result → no text clause. Ordering: default `ORDER BY t.created_at DESC, t.id DESC`; priority sort uses D11 CASE fragment. Chips: `CountsByState`/`CountsByPriority` reuse the same filter clauses (no pagination) — they reflect the filtered result set. Count + page queries share the builder so pagination boundaries are stable (page size fixed 10, D2).

## HTTP Layer

Session middleware (`middleware_auth.go`): wraps the mux; reads cookie token → `SessionStore.GetByID`; missing/expired → 303 `/login`; `UserStore.Count()==0` → all routes except `/setup*` redirect to 303 `/setup` (first-user bootstrap, D16). Exempt routes: `/login*`, `/setup*`, `/healthz`. Authenticated user hitting `/login` → 303 `/tickets`. Logged-in user context flows into handlers as request context value.

| Method | Path | Handler | Success response (HX / full) |
|---|---|---|---|
| GET | `/login` | Login form | page `login` |
| POST | `/login` | Authenticate (`email`,`password`) | 303 → `/tickets`; generic 401 re-renders form |
| POST | `/logout` | Destroy session + clear cookie | 303 → `/login` |
| GET | `/setup` | First-user form (only when users empty) | page `setup` |
| POST | `/setup` | Create first user (`name`,`email`,`password`) | 303 → `/login` |
| GET | `/` | redirect → `/tickets` | 303 |
| GET | `/tickets` | List (filters `state,priority,category_id,user_id,q,page`) | fragment `ticket_list` / page `tickets_index` |
| GET | `/tickets/new` | New form | fragment `ticket_form` / page `tickets_new` |
| POST | `/tickets` | Create | fragment `ticket_list` / 303 → detail |
| GET | `/tickets/{id}` | Show (timeline+comments+audit) | fragment `ticket_detail` / page `tickets_show` |
| GET | `/tickets/{id}/edit` | Technical fallback edit form (not linked in normal UI) | fragment `ticket_edit_form` / page `tickets_edit` |
| POST | `/tickets/{id}/edit` | Inline Properties update | fragment detail / 303 → detail |
| POST | `/tickets/{id}/transition` | Transition (form: `to`, `reason`) | fragment detail / 303 |
| POST | `/tickets/{id}/comments` | Add comment | fragment `comment_list` (prepend) / 303 |
| GET | `/users`, `/categories` | Lists | pages `users_index`/`categories_index` |
| GET/POST | `/users/new`, `/users`, `/users/{id}/edit`, `/users/{id}/delete` | CRUD (name, email, password; deactivate via edit) | forms/fragments; delete 409 → message |
| GET/POST | `/categories/new`, `/categories`, `/categories/{id}/edit`, `/categories/{id}/delete` | CRUD | same contract |
| GET | `/healthz` | 200 "ok" (no session required) | plain text |

Handler responsibilities: parse form → trim → call service → render. Non-numeric `{id}` → 400. Errors via `mapError` (D5): `ValidationError|InvalidTransitionError|ReopenReasonRequiredError|InactiveUserError|InvalidPriorityError → 422`; `NotFoundError → 404`; `DuplicateError|ReferencedError → 409`; `InvalidCredentialsError → 401` (single generic message); unknown → 500 "Internal server error". Error responses re-render the originating form fragment with an inline error block (HTMX swaps it) — status code travels in the response.

Cookie: `tkt_session=<token>`; `HttpOnly`, `Secure` (production behind TLS; dev flag documented), `SameSite=Strict`, `Path=/`, no explicit `Expires` (session cookie; server TTL 24h enforced via `expires_at`). Expired `sessions` rows purged lazily on lookup and at startup. Deactivating a user deletes their active sessions (D14) — next request is logged out.

`render(w,r,page,fragment,data,status)`: `HX-Request` header → execute `fragment` only; absent → execute `page` (which embeds the fragment + `base.html` layout). Templates: `base.html` (shell), `pages/*.html` (full pages, incl. `login`, `setup`), `partials/*.html` (fragments including `ticket_list`, `ticket_form`, `ticket_detail`, `ticket_edit_form`, `comment_form`, `timeline`, `filter_form`, `pagination`, `state_badge`, `timestamp`, and admin forms). The filter bar and pagination swap only `#ticket-list`; no OOB renderer is required.

UI copy vs. domain vocabulary (decision): underlying records keep the domain/spec name **audit events** (`AuditEvent`, `AuditStore`, spec `audit-log`) while the merged UI section is **Timeline**. The application enriches `TicketView.Timeline` with action, field, and from/to labels; `timeline.html` never interprets stored IDs. Immutable `TicketView.AuditEvents` remain available as the underlying contract.

## Testing Strategy

| Layer | What | Approach |
|---|---|---|
| Domain unit | 5×5 transition matrix (all 25 pairs: allowed/denied + timestamp set/cleared), reopen reason rules, cancelled terminal, edits never touch `resolved_at`/`closed_at`, priority rank order, error types | Table-driven `t.Run` per pair; injected fixed clock |
| Application unit | Create (number, active-user check, category-exists), transition→audit (no silent mutation, actor from session), reopen reason, empty-title/comment rejection, user/category delete-referenced, duplicate email/category, filter composition, pagination 25/10 → 10/10/5, login (wrong password, unknown email, deactivated user → same generic error), logout deletes session | Port fakes (in-memory map implementations); assert audit append counts |
| Password hashing | Hash → verify ok; wrong password → false; two hashes of same password differ (per-user salt); empty password rejected | Unit on `application/password.go` with real bcrypt |
| Store integration | Real modernc on `file::memory:?cache=shared` (SetMaxOpenConns(1)) + migrations per test: sequential numbers 1042→1043, concurrent create (2 goroutines, distinct numbers), FK enforced (bad category/user_id → error), user/category delete-referenced → ErrReferenced, email UNIQUE → ErrDuplicate, session create/get/delete + expiry lookup, migrations rerun no-op | `newTestDB(t)` helper; real driver |
| FTS5 consistency | Title edit "Old"→"New": search "Old" empty / "New" hits; comment add → searchable; filters AND-compose with text; FTS special chars (`"`, `(`, `*`, `:`) in `q` → no error, sane results | Integration on real driver |
| Auth HTTP flow | `httptest` + real store: POST `/login` correct → 303 + `Set-Cookie`; wrong password/unknown email/deactivated → 401 generic; cookie then authorized on `/tickets`; logout → session row gone, next request 303 `/login` | `httptest.NewRecorder` + cookie jar assertions |
| Session middleware | No cookie → 303 `/login`; expired/forged token → 303 `/login`; valid cookie → 200; empty users table → all routes 303 `/setup`; `/setup` creates first user → 303 `/login`; with users present `/setup` unavailable | Table-driven with `httptest.NewServer`/`NewRecorder` |
| HTTP handlers | All routes: create/transition/comment happy paths, 422 English errors, 404 unknown id, 400 non-numeric id, HX-Request → fragment (no `<html>`), no header → full page, `/healthz` 200 | `httptest.NewRecorder` + real in-memory store + real templates |
| Golden files | Each fragment with frozen fixtures (injected clock, fixed order) vs `testdata/*.golden`; `-update` regenerates, rerun without | Golden file test per template; `-update` flag |

E2E: none (config). Docker smoke: `docker build` + run + healthcheck verified in verify phase.

## Threat Matrix

| Boundary | Applicability | Design response | Planned RED tests |
|---|---|---|---|
| Documentation-like paths | N/A — no executable-markdown/git-file classification in this change | — | — |
| Git repository selection | N/A — no VCS automation; SDD artifacts only | — | — |
| Commit state | N/A — no git command execution | — | — |
| Push state | N/A — no git push/PR automation in this change | — | — |
| PR commands | N/A — PR delivery handled by orchestrator, not by app code | — | — |
| HTTP routing / path params | **Applicable** — Go 1.22 ServeMux `{id}` + user text into FTS5 MATCH | Non-numeric `{id}` → 400; parameterized SQL everywhere; D4 quoting for FTS | Non-numeric id → 400; FTS syntax chars in `q` → 200/empty, no 500; unknown filter values → ignored (defaults) |
| Session cookie / auth boundary | **Applicable** — login, middleware, opaque session tokens | HttpOnly + Secure + SameSite=Strict cookie; fresh opaque token per login; generic login errors (no user enumeration); 24h server-side TTL; logout deletes row; deactivation kills sessions; Origin check on unsafe methods (D17) | Wrong password / unknown email / deactivated → same generic 401; no cookie → 303 `/login`; expired session → 303 `/login`; logout → next request unauthenticated; cross-site Origin on POST → 403 |

## Migration / Rollout

Greenfield — no data migration. Migrations 0001/0002 run transactionally at startup before serving; additive only; failure = fatal. `users`/`sessions` replace the earlier `agents` schema — no legacy rows exist (greenfield, amendment approved before implementation). Expired sessions: lazy purge on lookup + startup sweep. Rollback = drop branch/container (main stays at bootstrap commit); store port isolates driver swap. No feature flags.

## File Changes

All Create (greenfield): `go.mod`/`go.sum` (modernc v1.56.0 + golang.org/x/crypto pins); `cmd/server/main.go`; `internal/domain/{ticket,state,priority,comment,audit,user,category,errors,clock}.go`; `internal/application/{ports,ticket_service,comment_service,user_service,auth_service,category_service,search_service,views,password}.go`; `internal/adapters/sqlite/{sqlite,migrate,ticket_store,comment_store,audit_store,user_store,session_store,category_store,search_store}.go` + `migrations/{0001_init,0002_fts}.sql`; `internal/adapters/http/{server,router,render,errors,middleware_auth,handlers_tickets,handlers_users,handlers_auth,handlers_categories}.go`; `web/templates/{templates.go,base.html}` + 12 `pages/*.html` (incl. `login`, `setup`) + 12 `partials/*.html`; `Dockerfile`, `.dockerignore`, `docker-compose.yml`; per-layer `_test.go` + `testdata/*.golden`; this `design.md`.

Docker: no structural change — same static multi-stage build. No `TKT_SESSION_SECRET` env var: session tokens are opaque random values stored server-side (D14); the only runtime env vars remain DB path and listen address.

## Open Questions

None. Previously open:
- Actor identity — **SOLVED**: actor/author always comes from the session; `sistema` reserved for genuine system actions (none in MVP).
- Page size (`TKT_PAGE_SIZE`) — **SOLVED**: fixed at 10, not configurable.
