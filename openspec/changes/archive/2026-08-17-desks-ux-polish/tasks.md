# Tasks: Desks UX Polish

## Review Workload Forecast

| Slice | Estimated authored lines (goldens excluded) | Budget risk |
|---|---:|---|
| S1 Desks rename | 300–380 | Medium |
| S2 Ticket-list role UX | 120–190 | Low |
| S3 User-management forms | 260–380 | Medium |
| S4 Ticket detail/auth polish | 180–280 | Low |

Chained PRs recommended: Yes (four reviewable slices; S1 blocks the others).
Decision needed before apply: Yes
Chain strategy: pending
400-line budget risk: High (combined change; each slice is targeted below 400)

### Work Units

| Unit | Goal | Focused test command | Runtime harness | Rollback boundary |
|---|---|---|---|---|
| S1 | Migration and compiling Groups→Desks rename | `go test ./internal/adapters/sqlite ./internal/adapters/http ./internal/application` | N/A—repo config has no browser harness; httptest/goldens | 0004, rename commit, and S1 templates/tests |
| S2 | Role-aware search/filter presentation | `go test ./internal/adapters/http -run 'Ticket|Search'` | N/A—httptest/golden boundary | S2 handlers/templates/partials/tests |
| S3 | Atomic user workflows | `go test ./internal/application ./internal/adapters/sqlite ./internal/adapters/http -run 'User|Admin'` | N/A—httptest + in-memory SQLite | S3 use-case/store/handler/forms/tests |
| S4 | Detail cards, comments, timeline, and login copy | `go test ./internal/adapters/http -run 'Ticket|Comment|Auth'` | N/A—httptest/golden boundary | S4 templates/styles/handlers/tests |

Dependency: complete S1 (migration, source rename, wiring, and route names) before S2–S4; S2–S4 are otherwise independent.

## S1: Persisted Desks Rename

- [x] S1.1 RED: add `migration_0004_test.go` proving 0003→0004 row/ID preservation, desk FK/index/trigger invariants, person-only assignment, membership/downgrade rejection, and idempotent rerun.
- [x] S1.2 GREEN: add `migrations/0004_desks.sql` with immediate in-place table/column renames and recreated desk triggers; make migration tests pass.
- [x] S1.3 RED→GREEN: mechanically rename domain/application/SQLite/HTTP symbols, ports, routes, errors, wiring, templates, and tests to Desk; preserve CRUD, uniqueness, membership, and auth at `/desks`.
- [x] S1.4 REFACTOR: update current (not archived) specs/goldens and run `gofmt`, `go vet ./...`, `go test ./...`.

## S2: Ticket-List Role UX

- [x] S2.1 RED: add handler/golden tests for one `/tickets?q` control per role, staff filters, no duplicate search, and user scope against another matching ticket.
- [x] S2.2 GREEN: add `ticket_search.html`, role view-model flag, responsive placement, and user filter suppression while retaining SearchService/scoping.
- [x] S2.3 REFACTOR: update `tickets_index`, `filter_form`, styles, and goldens; run focused tests plus `go vet ./...`.

## S3: User-Management Forms

- [x] S3.1 RED: test atomic edits, forbidden-role rollback, audited transition, removed `/users/{id}/role`, password hash-only update, protected status rejection, and deactivate/reactivate labels.
- [x] S3.2 GREEN: implement `UpdateManagedUser` immediate transaction, `ChangePassword`/`UpdatePasswordHash`, edit-only role select, dedicated password POST, and status action in listed user files.
- [x] S3.3 REFACTOR: remove list controls/password fields, update forms/goldens, and run focused plus full Go checks.

## S4: Ticket Detail and Auth Polish

- [x] S4.1 RED: test default-open cards, localStorage key `tkt:ticket-detail:collapsed:v1`, checkbox normalization/forgery rejection, staff-only styling/newest-first disclosure, and absent login copy.
- [x] S4.2 GREEN: implement native `<details>`, persistence script, hidden public + `internal=1` checkbox, timeline class/background, desk SVG in `base.html`, and login copy removal.
- [x] S4.3 REFACTOR: verify user responses never contain internal comments, refresh all listed goldens, and run `gofmt`, `go vet ./...`, `go test ./...`.
