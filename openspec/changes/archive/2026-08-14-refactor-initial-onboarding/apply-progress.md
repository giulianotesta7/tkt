# Apply Progress: Refactor Initial Onboarding

## Work Unit

- Delivery strategy: chained PRs
- Chain strategy: stacked-to-main
- Current boundary: PR 1 only — establish RED auth contract and copy coverage.
- Status: complete RED slice; production presentation remains intentionally unimplemented.

## Completed Tasks

- [x] 1.1 Table-driven setup/login identity, prohibited-copy, labels, autocomplete, and form-contract RED assertions.
- [x] 1.2 Safe invalid setup/login re-render RED assertions while preserving existing bootstrap, active-user, and redirect coverage.
- [x] 1.3 Setup/login full-page golden RED cases without generating snapshots.

## TDD Cycle Evidence

| Task | Test File | Layer | Safety Net | RED | GREEN | TRIANGULATE | REFACTOR |
|---|---|---|---|---|---|---|---|
| 1.1 | `internal/adapters/http/handlers_auth_test.go` | HTTP integration | `go test ./internal/adapters/http -run 'Test(Auth|Golden)' -count=1` → PASS | Written first; focused command fails on approved copy/prohibited old copy | Intentionally deferred to PR 2 | Setup and login cases | None; RED contract only |
| 1.2 | `internal/adapters/http/handlers_auth_test.go` | HTTP integration | Same command → PASS | Safe re-render coverage extended before presentation work | Existing invalid setup/login, bootstrap, active-user, and redirect tests pass in isolation | Setup and login invalid submissions | None; RED slice only |
| 1.3 | `internal/adapters/http/golden_test.go` | Render/golden | Same command → PASS | Written first; focused command fails only for absent auth snapshots | Intentionally deferred to PR 3 | Setup and login full-page render cases | None; RED slice only |

## Work Unit Evidence

| Evidence | Result |
|---|---|
| Focused test command and exact result | `go test ./internal/adapters/http -run 'Test(AuthEntryCopyAndFormContracts|SetupValidationErrorReRenders|LoginValidationErrorReRendersPreservesSafeValues|GoldenAuth)' -count=1` → exit 1. The only failures are missing approved setup copy and prohibited legacy content, plus deliberately absent `testdata/auth_setup.golden` and `testdata/auth_login.golden`; safe invalid-form assertions pass. |
| Existing behavior isolation | `go test ./internal/adapters/http -run 'Test(LoginSuccess|LoginFailureSameGenericError|SetupCreatesFirstActiveUser|SetupValidationErrorReRenders|LoginValidationErrorReRendersPreservesSafeValues)' -count=1` → exit 0, PASS. |
| Runtime harness | N/A: tests prove HTTP behavior; no runtime boundary in this RED slice. |
| Rollback boundary | Revert `internal/adapters/http/handlers_auth_test.go` and `internal/adapters/http/golden_test.go`; this removes only PR 1's forward-looking RED contract without touching production behavior. |

## Golden Impact Identified

PR 2's shared stylesheet change will alter full-page output for `render_full_page`, `tickets_index`, `tickets_new`, `tickets_show`, `users_index`, `users_new`, `categories_index`, and `categories_new`. PR 3 must regenerate affected snapshots through `-update`, inspect them, and rerun without update mode. Auth snapshots remain intentionally absent until then.

## Files Changed

- `internal/adapters/http/handlers_auth_test.go`
- `internal/adapters/http/golden_test.go`
- `openspec/changes/refactor-initial-onboarding/tasks.md`
- `openspec/changes/refactor-initial-onboarding/apply-progress.md`

---

## Work Unit

- Delivery strategy: chained PRs
- Chain strategy: stacked-to-main
- Current boundary: PR 2 only — shared auth presentation and GREEN contract tests.
- Status: complete; golden snapshots intentionally remain reserved for PR 3.

## Completed Tasks

- [x] 2.1 Implemented the semantic `.auth-entry` shell with a 34×34 tkt mark, approved welcome, conceptual lifecycle, decorative-only marks/connectors, and three approved principles.
- [x] 2.2 Updated setup and login copy without changing forms, endpoints, bindings, autocomplete, error role, or password behavior.
- [x] 2.3 Replaced only auth CSS with a scoped 820/620 desktop split, 440px form surface, 900px mobile collapse, focus treatment, and overflow protection.

## TDD Cycle Evidence

| Task | Test File | Layer | Safety Net | RED | GREEN | TRIANGULATE | REFACTOR |
|---|---|---|---|---|---|---|---|
| 2.1 | `internal/adapters/http/handlers_auth_test.go` | HTTP integration | `go test ./internal/adapters/http -run 'Test(LoginSuccess|LoginFailureSameGenericError|SetupCreatesFirstActiveUser|SetupValidationErrorReRenders|LoginValidationErrorReRendersPreservesSafeValues)' -count=1` → exit 0, PASS | PR 1's `TestAuthEntryCopyAndFormContracts` was written first and failed on missing approved copy/prohibited legacy copy. | `go test ./internal/adapters/http -run 'Test(AuthEntryCopyAndFormContracts|SetupValidationErrorReRenders|LoginValidationErrorReRendersPreservesSafeValues)' -count=1` → exit 0, PASS. | Setup and login render the shared shell through separate handler paths. | Scoped semantic markup; focused tests remain green. |
| 2.2 | `internal/adapters/http/handlers_auth_test.go` | HTTP integration | Same behavior-isolation command → exit 0, PASS | PR 1's setup/login contract rows failed before approved page copy was added. | Same focused GREEN command → exit 0, PASS. | Setup requires name/new-password; login retains current-password and no password echo. | No production refactor beyond approved copy. |
| 2.3 | `internal/adapters/http/handlers_auth_test.go` | HTTP integration + browser runtime | Same behavior-isolation command → exit 0, PASS | PR 1 full-page contract failed before the auth shell and scoped presentation existed. | Focused GREEN command → exit 0, PASS; browser harness passed both required viewports. | Desktop retains journey/principles; 390px hides both and preserves no-overflow single-column form. | Auth rules remain exclusively beneath `.auth-entry`; application selectors unchanged. |

## Work Unit Evidence

| Evidence | Result |
|---|---|
| Focused test command and exact result | `go test ./internal/adapters/http -run 'Test(AuthEntryCopyAndFormContracts|SetupValidationErrorReRenders|LoginValidationErrorReRendersPreservesSafeValues)' -count=1` → exit 0, PASS. |
| Existing behavior isolation | `go test ./internal/adapters/http -run 'Test(LoginSuccess|LoginFailureSameGenericError|SetupCreatesFirstActiveUser|SetupValidationErrorReRenders|LoginValidationErrorReRendersPreservesSafeValues)' -count=1` → exit 0, PASS. |
| Golden status | `go test ./internal/adapters/http -run 'TestGoldenAuth' -count=1` → exit 1 only because `testdata/auth_setup.golden` and `testdata/auth_login.golden` are intentionally absent. No snapshot was generated or updated. |
| Runtime harness | Started `TKT_DB_PATH=/tmp/tkt-auth-entry.db TKT_LISTEN=127.0.0.1:18080 go run ./cmd/server`; Playwright at 1440×900: `scrollWidth=1440`, presentation width 820, form width 440, journey/principles visible, focus rule solid. At 390×844: `scrollWidth=390`, journey/principles `display=none`, form follows compact branding, focus rule solid. Server and `/tmp` artifacts removed afterward. |
| Changed-line count | Auth production files: 87 additions + 38 deletions = 125 authored lines; within the 400-line PR 2 budget. |
| Rollback boundary | Revert `web/templates/auth.html`, `web/templates/pages/setup.html`, `web/templates/pages/login.html`, and the `.auth-entry` section of `web/templates/partials/styles.html`; this removes only PR 2 presentation while retaining PR 1 RED tests and all auth behavior. |

## Files Changed

- `web/templates/auth.html`
- `web/templates/pages/setup.html`
- `web/templates/pages/login.html`
- `web/templates/partials/styles.html` (auth section only)
- `openspec/changes/refactor-initial-onboarding/tasks.md`
- `openspec/changes/refactor-initial-onboarding/apply-progress.md`

## Remaining Intentional RED

- `TestGoldenAuthSetup` and `TestGoldenAuthLogin` remain RED solely for missing snapshots. PR 3 owns snapshot creation/update, affected full-page golden regeneration, and final verification.

---

## Work Unit

- Delivery strategy: chained PRs
- Chain strategy: stacked-to-main
- Current boundary: PR 3 only — auth goldens and final regression/manual evidence.
- Status: blocked before task completion by an existing stale assertion outside the authorized PR 3 scope.

## Partial PR 3 Evidence

- Generated `internal/adapters/http/testdata/auth_setup.golden` and `auth_login.golden` exclusively through the repository `-update` mechanism.
- Regenerated only the identified affected full-page snapshots: `categories_index`, `categories_new`, `tickets_index`, `tickets_new`, `tickets_show`, `users_index`, and `users_new`.
- Re-ran the exact bounded golden set without `-update`: `go test ./internal/adapters/http -run '^(TestGoldenAuthSetup|TestGoldenAuthLogin|TestGoldenFullPage|TestGoldenTicketsIndex|TestGoldenTicketsNew|TestGoldenTicketsShow|TestGoldenUsersIndex|TestGoldenUsersNew|TestGoldenCategoriesIndex|TestGoldenCategoriesNew)$' -count=1 -v` → exit 0; 10/10 PASS.
- Snapshot inspection confirms the auth files contain every approved identity/copy item once and no checked prohibited terms. The seven existing full-page snapshots have byte-identical non-`<style>` markup before and after regeneration; only shared stylesheet bytes changed.
- Focused auth regression command: `go test ./internal/adapters/http -run 'Test(AuthEntryCopyAndFormContracts|SetupValidationErrorReRenders|LoginValidationErrorReRendersPreservesSafeValues|LoginSuccess|LoginFailureSameGenericError|SetupCreatesFirstActiveUser|GoldenAuth)' -count=1` → exit 0, PASS.
- Manual runtime harness: started `TKT_DB_PATH=/tmp/tkt-pr3-auth.db TKT_LISTEN=127.0.0.1:18083 go run ./cmd/server`; a valid POST `/setup` returned `303 Location: /login`. At 1440×900, the presentation measured 820px and the form card 440px; approved copy and one visible footer appeared. At 390×844, `scrollWidth=390`, the form measured 350px, and journey/principles were `display:none`. Browser focus was placed on the Name field and the accessibility snapshot confirmed the complete labeled form. The temporary database, log, browser screenshots, and server process were removed.

## Blocking Regression

`go test ./... -count=1` cannot pass because the existing `TestSetupPageShownWhenEmpty` in `internal/adapters/http/handlers_auth_test.go` still asserts obsolete copy `Create your account`. The approved PR 2 template now correctly renders `Set up tkt`; the test fails only on that stale literal. The assigned PR 3 scope explicitly prohibits edits to this file, so no test or production change was made. Consequently `go test ./... -race -count=1`, `go vet ./...`, and `go build ./...` were not run, and tasks 3.1–3.3 remain unchecked.

## Generated Snapshot Counts

- New auth snapshots: 35,155 generated lines/bytes of snapshot identity (17,722 setup bytes; 17,450 login bytes at generation; normalized final files are 17,705 and 17,433 bytes after test output formatting).
- Affected existing full-page snapshots: 231 additions + 133 deletions, all shared stylesheet bytes.
- Authored PR 3 implementation lines: 0. No golden harness wiring was required.

## Rollback Boundary

Revert the two auth snapshots, seven affected full-page snapshots, and this PR 3 OpenSpec progress entry. No unrelated application behavior, backend contract, or production source was changed by this work unit.

---

## Work Unit

- Delivery strategy: chained PRs
- Chain strategy: stacked-to-main
- Current boundary: PR 3 only — auth goldens and final regression/manual evidence.
- Status: complete after the maintainer-authorized correction of the single stale setup-copy assertion.

## Completed Tasks

- [x] 3.1 Generated `auth_setup.golden` and `auth_login.golden` through the repository `-update` path; regenerated and inspected the seven affected full-page snapshots, then passed the bounded no-update golden suite.
- [x] 3.2 Corrected only `TestSetupPageShownWhenEmpty`'s obsolete expected literal from `Create your account` to `Set up tkt`; passed focused auth tests, the full suite, race suite, vet, build, and whitespace checks.
- [x] 3.3 Reused PR 3's successful 1440×900 and 390×844 browser evidence because this correction changes only a server-test expected literal, not rendered templates, CSS, routes, or runtime behavior.

## TDD Cycle Evidence

| Task | Test File | Layer | Safety Net | RED | GREEN | TRIANGULATE | REFACTOR |
|---|---|---|---|---|---|---|---|
| 3.1 | `internal/adapters/http/golden_test.go` | Render/golden | Prior PR 3 bounded golden run documented snapshots absent before `-update` | PR 1's golden tests were written first and failed solely for absent snapshots | `go test ./internal/adapters/http -run '^(TestGoldenAuthSetup|TestGoldenAuthLogin|TestGoldenFullPage|TestGoldenTicketsIndex|TestGoldenTicketsNew|TestGoldenTicketsShow|TestGoldenUsersIndex|TestGoldenUsersNew|TestGoldenCategoriesIndex|TestGoldenCategoriesNew)$' -count=1 -v` → exit 0, 10/10 PASS | Setup/login plus eight full-page render cases | No code refactor; deterministic fixtures only |
| 3.2 | `internal/adapters/http/handlers_auth_test.go` | HTTP integration | `go test ./internal/adapters/http -run '^TestSetupPageShownWhenEmpty$' -count=1` → exit 1, failed only on obsolete `Create your account` literal | Existing assertion was proven stale against the approved rendered `Set up tkt` copy before correction | Same command → exit 0, PASS; focused and complete regression gates pass | Existing table-driven setup/login contract coverage exercises separate setup and login paths; this bounded fix changes no product logic | One literal replacement; no refactor needed |
| 3.3 | Prior PR 3 browser harness evidence | Runtime/browser | Prior successful runtime harness retained | N/A: no code or test change; prior manual comparison already covered both required viewports | Reuse approved: correction cannot affect rendering | Desktop and mobile routes were both exercised in the retained evidence | No refactor needed |

## Work Unit Evidence

| Evidence | Result |
|---|---|
| Focused stale-assertion safety net | `go test ./internal/adapters/http -run '^TestSetupPageShownWhenEmpty$' -count=1` → exit 1 before correction, then exit 0 after it. |
| No-update golden verification | `go test ./internal/adapters/http -run '^(TestGoldenAuthSetup|TestGoldenAuthLogin|TestGoldenFullPage|TestGoldenTicketsIndex|TestGoldenTicketsNew|TestGoldenTicketsShow|TestGoldenUsersIndex|TestGoldenUsersNew|TestGoldenCategoriesIndex|TestGoldenCategoriesNew)$' -count=1 -v` → exit 0; 10/10 PASS. New auth snapshots have 354 and 349 lines; affected existing snapshots differ only inside `<style>` bytes. |
| Focused auth regression | `go test ./internal/adapters/http -run 'Test(AuthEntryCopyAndFormContracts|SetupPageShownWhenEmpty|SetupValidationErrorReRenders|LoginValidationErrorReRendersPreservesSafeValues|LoginSuccess|LoginFailureSameGenericError|SetupCreatesFirstActiveUser|GoldenAuth)' -count=1` → exit 0, PASS. |
| Full implementation gate | `go test ./... -count=1` → exit 0, PASS. |
| Race gate | `go test ./... -race -count=1` → exit 0, PASS. |
| Static/build gates | `go vet ./...` → exit 0, PASS; `go build ./...` → exit 0, PASS; `git diff --check` → exit 0, clean. |
| Runtime harness | Reused PR 3's successful server/browser evidence: valid `POST /setup` returned `303 /login`; at 1440×900, presentation was 820px and form 440px with one footer; at 390×844, `scrollWidth=390`, form 350px, and journey/principles were hidden. Reuse is valid because the correction is a test-only expected string and cannot alter rendering. No browser was relaunched. |
| Scope confirmation | This correction changed one allowed assertion only. PR 3 adds two auth snapshots and updates seven identified full-page snapshots; no production, backend, persistence, role, route, endpoint, or payload file changed in this correction. |
| Rollback boundary | Revert the `Set up tkt` assertion literal plus the two auth snapshots and seven affected full-page snapshots; this removes the PR 3 verification fixture/correction work without touching unrelated behavior. |

## Changed-Line Accounting

- Maintainer-authorized correction: `internal/adapters/http/handlers_auth_test.go`, one literal replacement (1 deletion, 1 addition) within an existing PR 1 test file.
- PR 3 tracked existing goldens: 231 additions and 133 deletions across seven files; changes are stylesheet bytes only.
- PR 3 new auth goldens: 703 lines total (354 setup, 349 login).
- `internal/adapters/http/handlers_auth_test.go`'s current worktree aggregate is 113 additions and 4 deletions because it includes prior PR 1 contract tests; only the one literal correction belongs to resumed PR 3.

## Files Changed

- `internal/adapters/http/handlers_auth_test.go` — maintainer-authorized stale assertion correction only.
- `internal/adapters/http/testdata/auth_setup.golden` — generated setup snapshot.
- `internal/adapters/http/testdata/auth_login.golden` — generated login snapshot.
- `internal/adapters/http/testdata/categories_index.golden`
- `internal/adapters/http/testdata/categories_new.golden`
- `internal/adapters/http/testdata/tickets_index.golden`
- `internal/adapters/http/testdata/tickets_new.golden`
- `internal/adapters/http/testdata/tickets_show.golden`
- `internal/adapters/http/testdata/users_index.golden`
- `internal/adapters/http/testdata/users_new.golden` — each regenerated only for shared stylesheet bytes.
- `openspec/changes/refactor-initial-onboarding/tasks.md` — marked 3.1–3.3 complete after all gates passed.
- `openspec/changes/refactor-initial-onboarding/apply-progress.md` — merged PR 3 correction and final evidence.
