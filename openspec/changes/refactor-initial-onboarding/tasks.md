# Tasks: Refactor Initial Onboarding

## Review Workload Forecast

| Field | Value |
|-------|-------|
| Estimated changed lines | 520–700 authored lines, plus generated golden snapshots |
| 400-line budget risk | High |
| Chained PRs recommended | Yes |
| Suggested split | PR 1: RED tests; PR 2: templates/CSS and GREEN tests; PR 3: goldens and verification |
| Delivery strategy | ask-on-risk |
| Chain strategy | stacked-to-main |

Decision needed before apply: Yes
Chained PRs recommended: Yes
Chain strategy: stacked-to-main
400-line budget risk: High

### Suggested Work Units

| Unit | Goal | Likely PR | Focused test command | Runtime harness | Rollback boundary |
|---|---|---|---|---|---|
| 1 | Establish auth contract and copy RED coverage | PR 1 | `go test ./internal/adapters/http -run 'Test(Auth|Golden)' -count=1` | N/A: tests prove HTTP behavior | `handlers_auth_test.go` additions |
| 2 | Implement shared shell, page copy, and scoped responsive CSS | PR 2 | `go test ./internal/adapters/http -count=1` | Manual compare 1440×900 and 390×844 | auth templates and auth CSS only |
| 3 | Freeze rendered output and complete verification | PR 3 | `go test ./... -count=1 && go vet ./... && go build ./...` | Repeat both approved viewport comparisons | auth goldens and golden-test wiring |

## Phase 1: RED Tests

- [x] 1.1 In `internal/adapters/http/handlers_auth_test.go`, add table-driven failing assertions for exact approved setup/login copy, prohibited-copy absence, labels, autocomplete, and unchanged forms/contracts.
- [x] 1.2 Add failing invalid setup/login cases proving submitted name/email re-render, password non-echo, errors/loading state, and unchanged first-user bootstrap/login redirect and active-role behavior.
- [x] 1.3 Add failing full-page setup/login golden cases in `internal/adapters/http/golden_test.go`; identify existing full-page goldens whose bytes will change from shared CSS.

## Phase 2: GREEN Presentation

- [x] 2.1 Refactor `web/templates/auth.html` into the approved semantic `.auth-entry` shell: tkt logo/welcome, conceptual Received/Assigned/Resolved journey, three principles, decorative semantics, and one footer.
- [x] 2.2 Update `web/templates/pages/setup.html` and `login.html` with approved copy while preserving bindings, fields, endpoints, payloads, labels, autocomplete, validation, and HX/full render contracts.
- [x] 2.3 Replace only auth rules in `web/templates/partials/styles.html`: split/card hierarchy, 900px single-column behavior, focus-visible/WCAG AA contrast, no overflow, no motion, and no JS/dependencies.

## Phase 3: REFACTOR and Regression

- [x] 3.1 Regenerate `auth_setup.golden`, `auth_login.golden`, and affected existing full-page goldens through the repository `-update` path; inspect diffs, then rerun without `-update`.
- [x] 3.2 Run focused tests, `go test ./... -count=1`, CI-equivalent race tests, `go vet ./...`, and `go build ./...`; confirm no backend/persistence/role/endpoint/payload changes.
- [x] 3.3 Manually compare `design/initial-onboarding.op` at 1440×900 and 390×844; verify no horizontal overflow, visible focus, hidden mobile decoration, and no duplicate footer.
