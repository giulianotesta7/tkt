---
name: go-tests
description: "Trigger: Go tests, backend behavior, authorization, domain rules, migrations, golden fixtures, or Go CI evidence. Govern the five Go test layers of this repository and the gates that prove them."
license: MIT
metadata:
  author: "giulianotesta7"
  version: "1.0"
---

## Activation Contract

Activate when the work touches:

- any `*_test.go` file, or any package that owns tests (`internal/domain`, `internal/application`, `internal/adapters/sqlite`, `internal/adapters/http`, `cmd/server`);
- domain rules, application policies, authorization, or validation that Go owns exhaustively;
- a schema change, an embedded migration, or the numbered migration test series;
- a golden fixture under `internal/adapters/http/testdata/`;
- the Go gates in `.github/workflows/quality.yml`, `coverage.yml`, or `security.yml`.

Do NOT activate for:

- browser journeys, visual baselines, or HTMX evidence (that is `e2e-playwright`);
- Engram artifact bookkeeping on its own (that is `engram-governance`);
- formatting, tooling, or infrastructure changes with no test-behavior impact.

## Layer Map

Five layers, each with a different reality. Identify the layer before touching it; the conventions below only hold if the layer is right.

| Layer | Package | Real vs faked |
| --- | --- | --- |
| Domain | `internal/domain` | Pure values and rules. No database, no HTTP, no fakes |
| Application | `internal/application` | Real services over in-memory port fakes in `fakes_test.go`. No SQLite |
| SQLite adapter | `internal/adapters/sqlite` | Real `modernc.org/sqlite`, real embedded migrations, isolated shared-cache in-memory DSN per test, single connection pool |
| HTTP | `internal/adapters/http` | Real file-backed database with all migrations, every real service, real renderer, real routes, real session middleware. Requests through `httptest.NewRequest` and `mw.Wrap(mux).ServeHTTP` |
| Process | `cmd/server` | Built binary executed as a child process with an environment-configured database path |

## Hard Rules

1. **Never add `t.Parallel()` to a test that shares a store or harness.** No test in this repository uses it. Isolation comes from one fresh database or harness per test, and the sqlite package names its DSN per test precisely because pooled or shared memory databases produced `no such table` flakes.
2. **HTTP handler tests run against a real file-backed SQLite database with all migrations applied**, through `newHarness`, `newEmptyHarness`, or `openTestStore`. Never a hand-built store, never a faked persistence port. The harness is file-backed because the http package cannot reach the sqlite package-private test helpers, and because the real driver and pragmas match the production path. The only interface fakes in this package simulate operational failure at a seam.
3. **Adapter and migration tests exercise the real `modernc` driver and the real embedded migration set.** Assert schema through `pragma_table_info`, `pragma_foreign_key_list`, and `schema_migrations`. Never create a table by hand and never reuse a long-lived development database; both falsify the version and shape assertions.
4. **Arrange fixtures through the real service or the audited path.** `seedTicket` for creation, `assignTicket` for assignment, `seedTransition` for the state machine, `publishWorkflow` before any ticket-create test on a category, `createUser` when login has to work. Creation no longer accepts an assignee, so an assigned fixture must go through the audited assign path. The generic harness skips bcrypt on purpose, so adding real hashing to every fixture pays a cost the repository routed around.
5. **Byte-level HTML snapshots go through `goldenFile` and `testdata/*.golden`.** Everything else asserts with `strings.Contains` over `renderGolden` output. Never write inline full-body equality; it bypasses the update and stability workflow and reports a whole-document diff with no golden to regenerate.
6. **Golden order is update-then-prove.** Fix the handler and its own assertions first, then `go test ./internal/adapters/http -run TestGolden -update`, then re-run without `-update`. A golden regenerated to turn a red test green is not evidence.
7. **Golden fixtures use literal frozen instants.** Rendered output never depends on `time.Now()`.
8. **Every service under test gets an injected clock**: `testClock` with `fixedNow` in the HTTP layer, `fakeClock` with `fixedClock()` in application. Wall-clock `time.Now()` is allowed only where expiry or TTL is the subject, such as session lifetime.
9. **No `time.Sleep`, polling, or timing-based synchronization.** Concurrent cases use `sync.WaitGroup` plus channels. This repository has zero sleeps and zero retry loops in tests, including its deliberate concurrency and race cases.
10. **Go tests never open a listening socket.** Use `httptest.NewRequest` and `NewRecorder` against the wrapped mux. Process-level behavior belongs to `cmd/server` through the built binary.
11. **Raw-SQL assertions in the HTTP layer are allowed only for facts no store port exposes**, and go through `rawDB` plus the `scanOne*` helpers. If a store port already answers the question, use the port.
12. **A schema-changing change ships a `migration_00NN_test.go`** asserting column shape, foreign keys and cascades, check bounds, version bookkeeping, and the pre-migration upgrade path. The series is numbered; keep the next number.
13. **Closing evidence is the CI-equivalent gate set defined by `.github/workflows/quality.yml`.** Run those exact commands. The workflow file is authoritative; this rule does not restate its flags.
14. **Coverage is one global `go tool cover -func` total.** `.github/workflows/coverage.yml` defines the threshold and the command; use them as written there. There is no per-package minimum and no exclusion list. A green coverage run is not race evidence: that workflow does not pass `-race`, so only the race run is.

## Decision Gates

| Condition | Action |
| --- | --- |
| The behavior is a browser journey, a visual claim, or HTMX request provenance | Belongs to `e2e-playwright`. Do not duplicate it in Go |
| The behavior is exhaustive authorization, domain edge cases, or workflow validation | Belongs here. The Playwright suite delegates it to this layer by name |
| An equivalent lower-layer test already proves the behavior | Prefer the lower layer. Do not add a heavier test for the same confidence |
| A store port already exposes the asserted fact | Use the port instead of `rawDB` |
| A schema change ships without a `migration_00NN_test.go` | Block. The numbered series is expected |
| A new fake silently simplifies a port contract | Block until the simplification is documented in `fakes_test.go`, as the existing fakes do |
| `go test ./... -race` cannot run | Block and report the required checks with a sanitized failure |

## Execution Steps

1. Identify the layer from the layer map, then read the existing tests in that package before writing. In this repository the conventions live in file comments, not in prose docs.
2. Reuse the existing harness, helpers, and seeds. Add a helper only when nothing existing can express the arrangement.
3. Update the smallest existing test that owns the behavior instead of creating a parallel file. Keep one canonical test per contract.
4. Run the focused package first, then the full set: `go test ./internal/... -race -count=1`.
5. Run the closing gates from rule 13, and the coverage command from rule 14 whenever the change touches tested code.
6. Report the layer touched, the exact commands with their results, and any gate you did not run.

## Output Contract

Report: the layer and package touched, files changed, the focused and full commands with results, the closing gate results, the coverage total when measured, and anything skipped or blocked. State plainly when a gate was not run, and never present a coverage number as race evidence.

## References

- `../../../internal/adapters/http/harness_test.go` — HTTP harness, seeds, and the rationale comments behind them
- `../../../internal/adapters/http/golden_test.go` — golden harness and the update-then-prove workflow
- `../../../internal/adapters/sqlite/sqlite_test.go` — test DSN, migration application, and single-pool semantics
- `../../../internal/application/fakes_test.go` — port fakes and their documented simplifications
- `../../../cmd/server/main_test.go` — built-binary process tests
- `../../../e2e/README.md` — the layer boundary and what is delegated to Go
- `../../../.github/workflows/quality.yml` — formatting, tidy, vet, build, and race
- `../../../.github/workflows/coverage.yml` — the global 75.0 threshold
- `../../../.github/workflows/security.yml` — staticcheck and govulncheck
- `../e2e-playwright/SKILL.md` — the browser layer this one is the counterpart to
- `../engram-governance/SKILL.md` — behavioral artifact governance
