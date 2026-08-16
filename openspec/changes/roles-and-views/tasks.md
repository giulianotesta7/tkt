# Tasks: Roles and Views

## Review Workload Forecast

~2,700 authored lines (goldens excluded); ask-on-risk: chain ask at apply gate; each slice RED→GREEN→REFACTOR, `go test ./... -count=1`, gofmt, `go vet ./...`; gentle-ai review, atomic commit+receipt.

Decision needed before apply: Yes
Chained PRs recommended: Yes
Chain strategy: pending
400-line budget risk: High

## Work Units (test//harness//rollback)

Harness N/A P1,P3–P6 (httptest+in-memory).

- P1 (S1) roles+policy+backfill // `go test ./internal/... -count=1` // revert S1
- P2 (S2) setup+root recovery // `go test ./cmd/server ./internal/adapters/sqlite -count=1` // `-recover-root=<id>` // revert S2
- P3 (S3) requester scopes/search // `go test ./internal/... -count=1` // revert S3
- P4 (S4) assignment/state/audit // `go test ./internal/... -count=1` // revert S4
- P5 (S5) comment visibility // `go test ./internal/... -count=1` // revert S5
- P6 (S6) groups CRUD // `go test ./internal/... -count=1` // revert S6
- P7 (S7) authz+role views // `go test ./internal/adapters/http -count=1` // `go run ./cmd/server` // revert S7
- P8 (S8) goldens+matrix+race // `go test -race ./... -count=1` // `go run ./cmd/server`+curl // revert S8

Deps: 2,3,6←P1; 4,5←3; 7←2–6; 8←7; P5∥P6; S6 monitored.

## S1

- [x] 1.1 RED `domain/role_test.go` hierarchy denies.
- [x] 1.2 `domain/role.go`+`user.go`: `Role`, `User.Role`.
- [x] 1.3 `application/policy.go`: `Capabilities,Require,TicketScope`.
- [x] 1.4 RED `0003` tests: constraints,unique-root,triggers.
- [x] 1.5 `migrations/0003_roles_and_views.sql`: role,requester,visibility,groups,audit,triggers.
- [x] 1.6 Backfill `migrate.go`: reliable→requester/root, else NULL/fail-closed.
- [x] 1.7 Ops: backup SQLite before deploy (documented in `runbook.md`).
- [x] 1.8 RED+impl `-recover-root`: promote+audit+exit; else fail closed (pulled forward from S2 2.3 after review R3-001: the fail-closed error must not recommend an unimplemented flag).

## S2

- [x] 2.1 RED concurrent `/setup`→one root (`BEGIN IMMEDIATE`).
- [x] 2.2 `BootstrapRoot`.
- [x] 2.3 RED+impl `-recover-root`: promote+audit+exit;else fail closed (implemented in S1 as 1.8).
- [x] 2.4 RED root untouchable, not grantable; startup fails closed.

## S3

- [x] 3.1 `domain/ticket.go`: `RequesterUserID *int64`.
- [x] 3.2 RED create stores session requester; caller fields rejected.
- [x] 3.3 Scope via `ports.go`,`ticket_store.go`,`filters.go`,`search_store.go`.
- [x] 3.4 RED user own-only, agent assigned, admin/root full queue.

## S4

- [x] 4.1 RED initial assign reasonless; reassign requires reason.
- [x] 4.2 Assign: agent+ only, active target.
- [x] 4.3 RED transitions: user denied, agent assigned, admin/root any.
- [x] 4.4 `domain/audit.go`: `ActorUserID,Reason`.

## S5

- [x] 5.1 RED user public-only; internal rejected; agent+ both.
- [x] 5.2 Filter internal pre-composition in comment stores + `views.go`.
- [x] 5.3 RED leakage: no internal body in user responses.
- [x] 5.4 Backfill legacy comments → `public`.

## S6

- [x] 6.1 `domain/group.go`; `sqlite/group_store.go` CRUD+N:N, unique names.
- [x] 6.2 RED admin/root only;user never member.
- [x] 6.3 `group_service.go`, `handlers_groups.go`, group templates.
- [x] 6.4 RED group never assignee; least-loaded contract documented.

## S7

- [x] 7.1 RED admin user↔agent only; root grants admin.
- [ ] 7.2 Role change + `POST /users/{id}/role`; deactivated: no login/assign.
- [x] 7.3 RED categories admin/root-only; forged fields rejected.
- [x] 7.4 Handler+middleware gates; shell/ticket views gating; RED direct denial.

## S8

- [ ] 8.1 Regenerate goldens `-update`, inspect, rerun.
- [ ] 8.2 Route×role matrix; direct/HTMX denial; no-query spies.
- [ ] 8.3 gofmt, `go vet ./...`, `go test -race ./...`.

Non-goals: flows, auto-assignment, group assignees, agent panel, admin-created admins, root mutation.
