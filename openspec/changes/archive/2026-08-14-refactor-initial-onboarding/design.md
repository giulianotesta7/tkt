# Design: Refactor Initial Onboarding

## Technical Approach

Keep the existing `auth.html` shell selected by `shellFor("login"|"setup")`, handler payloads, and full/HX render paths. Refactor only the shared auth shell, page copy, and auth-scoped CSS to reproduce the approved desktop/mobile composition. Setup and login share one presentation panel; their forms remain page-owned `content` blocks so validation re-renders and password handling are unchanged.

## Architecture Decisions

| Decision | Alternatives | Rationale |
|---|---|---|
| Retain one shared auth shell and page-specific forms | Separate setup/login shells; handler-selected variants | Existing renderer already isolates auth pages and naturally renders `content` for HX requests. Shared presentation prevents identity drift without changing Go contracts. |
| Use semantic presentation markup with decorative primitives hidden | CSS pseudo-content only; hide the whole panel from assistive technology | Heading, welcome text, lifecycle explanations, and principles are meaningful content. Only connectors, step markers, and ornamental geometry receive `aria-hidden="true"`; no controls or data-like links are introduced. |
| Scope selectors beneath `.auth-entry` and switch at `900px` | Global utility changes; retain the current auth-only `640px` collapse | A dedicated root prevents application-screen regressions. `@media (max-width:900px)` matches the approved single-column behavior: compact brand first, journey/principles hidden, fluid form width, and `overflow-x:clip` with width constraints. |
| Extend focused HTTP tests and add auth full-page goldens | CSS-only review; browser E2E | The repository has deterministic Go golden support but no browser runner. Focused assertions prove contracts/copy; setup and login goldens freeze shell integration. Layout remains reviewable against `design/initial-onboarding.op`. |
| Keep `modernc.org/sqlite` | Switch to `mattn/go-sqlite3` | The existing pure-Go store, migrations, tests, and `CGO_ENABLED=0` delivery already depend on modernc. This presentation-only change must not touch persistence. |

## Data / Render Flow

```text
GET/POST /setup or /login → AuthHandlers → existing loginData/setupData
    → Renderer.Render → auth.html + page content → scoped HTML/CSS response
POST validation failure ───────────────────────────────┘ (same status/values)
```

Full requests execute `auth.html`; HX requests continue executing only the page `content` block. No route, middleware, service, role, session, payload, or bootstrap path changes.

## File Changes

| File | Action | Description |
|---|---|---|
| `web/templates/auth.html` | Modify | Add `.auth-entry` semantic shell, compact logo/welcome, lifecycle list (`Received`, `Assigned`, `Resolved`), and the three approved principles; remove the duplicate footer and prohibited copy. |
| `web/templates/pages/setup.html` | Modify | Use `Set up tkt`, `Create the first account for your support team. This only happens once.`, `Create account`, and `Your password is stored securely.` while preserving fields and bindings. |
| `web/templates/pages/login.html` | Modify | Retain login behavior and form contract while sharing the new shell and removing technical footnote copy. |
| `web/templates/partials/styles.html` | Modify | Replace the auth section with `.auth-entry`-scoped desktop/card/focus/responsive rules; leave application selectors unchanged. |
| `internal/adapters/http/handlers_auth_test.go` | Modify | Add table-driven copy, prohibited-copy, form-attribute, safe re-render, and password non-echo assertions for both routes. |
| `internal/adapters/http/golden_test.go` | Modify | Add deterministic setup/login full-page golden cases. |
| `internal/adapters/http/testdata/auth_setup.golden` | Create | Approved setup shell snapshot. |
| `internal/adapters/http/testdata/auth_login.golden` | Create | Approved login shell snapshot. |
| Existing full-page goldens | Modify | Regenerate snapshots affected by the shared stylesheet bytes; inspect diffs and rerun without `-update`. |

## Interfaces / Contracts

- Setup remains `POST /setup` with `name`, `email`, `password`; labels and `autocomplete="name|email|new-password"` remain. Login retains `email`, `password`, and `current-password`.
- Submitted name/email values re-render; password inputs never receive a `value`. Error banners retain `role="alert"`; `:focus-visible` remains high-contrast. Existing button/loading behavior is not removed or replaced.
- Approved presentation copy appears once. “Your data, your control — Self-hosted and managed on your infrastructure.” is the third principle, not a footer.

## Testing Strategy

| Layer | What | Approach |
|---|---|---|
| Focused HTTP | Copy, absence list, labels/autocomplete, errors, retained values, password non-echo, redirects/bootstrap | Table-driven `httptest` cases in `handlers_auth_test.go`. |
| Render/golden | Shared shell on setup and login, semantic landmarks, auth-scoped CSS | Generate with `go test ./internal/adapters/http -run 'TestGolden(Auth|...)' -update`, inspect, then rerun without `-update`. |
| Regression | All handlers/templates | `go test ./...`, `go vet ./...`, and `go build ./...`; manually compare desktop 1440×900 and mobile 390×844 against the approved `.op` because no browser E2E exists. |

## Threat Matrix

N/A — no routing, shell, subprocess, VCS/PR automation, executable-file classification, or process-integration boundary changes.

## Migration / Rollout

No migration or feature flag is required. Deploy templates and tests atomically; rollback reverts those files and goldens together.

## Open Questions

None.
