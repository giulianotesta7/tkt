# Proposal: tkt — Ticket Management MVP

## Intent
Greenfield ticketing MVP: reliable, simple SSR lifecycle — domain-enforced 5-state machine, readable numbers, comments, audit trail, users + login (no roles), categories, full-text search. Roles deferred to iteration 2; attachments/SLAs out (YAGNI).

## Scope
**In**: ticket aggregate (number, title, description, requester, category, priority, user, timestamps); state machine `new→in_progress→resolved→closed/cancelled`, reopen-with-reason; comments + merged newest-first timeline; audit log (actor from session); users + login (session cookie, bcrypt, first-user bootstrap, route middleware); categories CRUD; canonical filters + FTS5 + pagination (fixed 10) + ordering; inline ticket-detail editing; Docker multi-stage, /data volume, healthcheck.
**Out**: roles/permissions — ALL logged-in users can do everything (iteration 2); attachments, email, SLAs, saved filters, dashboard (deferred).

## Capabilities
**New** (→ `openspec/specs/<name>/spec.md`): ticket-management, ticket-state-machine, comment-timeline, audit-log, user-management, category-management, ticket-search.
**Modified**: None — greenfield (`agent-management` spec → `user-management`).

## Approach
Hexagonal-lite: cmd/server + internal/domain (pure) + internal/application + internal/adapters/{sqlite,http} + web/templates (go:embed). Server-side sessions; bcrypt via golang.org/x/crypto.

| Decision | Choice | Why |
|----------|--------|-----|
| Driver | modernc.org/sqlite v1.56.0 pinned | pure Go, static, FTS5 verified |
| Migrations | embedded SQL + small runner | no framework; FK via DSN |
| FTS5 | tickets_fts + sync triggers | zero-dep full-text |
| Timestamps | ISO-8601 UTC TEXT, injected clock | deterministic goldens |
| Numbering | MAX(number)+1 in BEGIN IMMEDIATE | safe at MVP scale |
| Routing | stdlib ServeMux; HTMX partials | no router lib; testable |
| Sessions | opaque token cookie + SQLite sessions table | server-side logout/deactivation control |
| Password | bcrypt (x/crypto), default cost | per-user salt; constant-time verify |
| Bootstrap | first-user flow when users table empty | never locked out; no seeded creds |

## Affected Areas
| Area | Impact | Description |
|------|--------|-------------|
| go.mod / go.sum | New | module + modernc + x/crypto pins |
| cmd/server/, internal/*, web/templates/ | New | hexagonal-lite layers |
| Dockerfile, docker-compose.yml | New | static multi-stage build |
| openspec/changes/tkt-mvp/ | Modified | scope amendment: users + login (no roles) |

## Risks
| Risk | Likelihood | Mitigation |
|------|------------|------------|
| modernc compile slow / FTS regression | Med | pin v1.56.0; cache layers |
| shared-cache test flakiness | Med | centralized DSN; one pool |
| FK pragma missed | Med | single DSN open path |
| number race | Low | BEGIN IMMEDIATE + retry |
| HTMX contract drift | Med | golden files; frozen fixtures |
| PR over 400 lines | High | chain PRs; see delivery notes |
| Session fixation / cookie theft | Med | fresh session per login; HttpOnly+Secure+SameSite; 24h expiry |
| CSRF on HTMX forms | Med | SameSite=Strict + Origin check on unsafe methods |

## Rollback Plan
Greenfield: main stays at bootstrap commit; revert = drop branch. Additive migrations (0001, 0002) in transactional runner — no destructive ops; `users`/`sessions` tables created in 0001 (no legacy data). Store port isolates driver swap (mattn).

## Dependencies
Go 1.25.11; modernc.org/sqlite v1.56.0; golang.org/x/crypto (bcrypt); Docker; module github.com/giulianotesta7/tkt (confirmed).

## Delivery Strategy Notes
delivery_strategy=ask-on-risk; 400-line budget. Forecast: High — greenfield MVP exceeds budget. Flag: Decision needed before apply: Yes; Chained PRs recommended: Yes (work-unit commits).

## Success Criteria
- [ ] `go test ./...` + `go vet` clean (strict TDD)
- [ ] Transition matrix test passes
- [ ] Golden + FTS5 integration pass
- [ ] Login flow tests: wrong password → generic error; correct → session cookie; logout destroys session
- [ ] Middleware tests: missing/expired session → redirect to login
- [ ] First-user bootstrap tests: empty table → creation offered; never locked out
- [ ] Docker build + healthcheck pass
- [ ] In-scope shipped; out-of-scope untouched
