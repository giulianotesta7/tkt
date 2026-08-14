# Exploration: Refactor Initial Onboarding

## Current State

The first-run bootstrap flow is already isolated behind `GET/POST /setup` in `internal/adapters/http/handlers_auth.go`. The middleware exposes `/setup` only while the users table is empty; the handler performs a second user-count defense, renders the `setup` page, forwards the submitted `name`, `email`, and `password` unchanged to `UserService.Create`, maps validation errors back to the same form, and redirects successful creation to `/login`. This preserves the first user's normal active-user behavior and existing authentication flow.

The visual shell is shared by login and setup through `web/templates/auth.html`, with global/auth CSS in `web/templates/partials/styles.html` and form content in `web/templates/pages/setup.html`. The current shell uses an approximately 50/50-to-60/40 grid depending on content constraints, but its copy exposes technical product details: “Ticket Desk”, implementation technologies, state counts, audit behavior, open-source messaging, and bcrypt storage. The setup form already has the required field names, labels, `required`, `autocomplete`, autofocus, and a single primary submit button; the password is intentionally not echoed after validation errors.

Existing HTTP tests in `internal/adapters/http/handlers_auth_test.go` cover empty-table rendering, first-user creation and active status, login after bootstrap, validation re-rendering, and the `/login` redirect. The repository also has deterministic rendered-HTML golden infrastructure in `internal/adapters/http/golden_test.go`, although there is no current setup-page golden fixture. Tests use Go `testing`, `httptest`, and SQLite-backed harnesses; the configured command is `go test ./... -count=1`.

## Affected Areas

- `web/templates/auth.html` — replace technical/open-source shell copy and preserve the shared auth page structure while introducing brand-only identity, product-oriented benefits, and a restrained decorative composition.
- `web/templates/pages/setup.html` — keep the existing form contract and labels while making the first-account form visually primary, centered, and product-oriented; remove bootstrap/storage implementation messaging.
- `web/templates/partials/styles.html` — adjust auth-only layout and responsive rules for a roughly 55–60/40–45 desktop split, a 400–460px form card, centered content, restrained ticket/dashboard decoration, visible focus states, and the existing approximately 900px single-column breakpoint without disturbing the rest of the application styles.
- `internal/adapters/http/handlers_auth.go` — likely no behavior change; verify that template data and error handling remain sufficient for the redesigned page.
- `internal/adapters/http/handlers_auth_test.go` — update or extend presentation assertions only if needed to prove removed technical copy and preserved bootstrap behavior.
- `internal/adapters/http/golden_test.go` and `internal/adapters/http/testdata/` — optional setup-page golden coverage if the existing golden infrastructure is used; regenerate only through the repository’s `-update` path.
- `web/templates/base.html` and `web/templates/pages/login.html` — inspect shared assumptions because `auth.html` is also the login shell; avoid changing login behavior or unrelated copy unless the shared shell makes it unavoidable.

## Approaches

1. **Auth-shell visual refactor with unchanged bootstrap contract** — redesign the shared auth shell and setup page using existing HTML templates and CSS only, retaining `/setup`, form field names, payload extraction, response statuses, redirect behavior, and all current dependencies.
   - Pros: smallest blast radius; preserves the proven handler/middleware path; supports the requested responsive and accessibility constraints without introducing runtime dependencies.
   - Cons: login shares the shell, so the design must remain appropriate for both login and first-run setup; decorative composition must be built from restrained HTML/CSS primitives.
   - Effort: Medium

2. **Introduce a setup-specific shell/template and style namespace** — give first-run setup a dedicated auth layout while leaving login’s current shell mostly intact.
   - Pros: maximum freedom to tailor onboarding copy and composition without coupling login presentation to setup.
   - Cons: duplicates shell structure and responsive behavior; increases template/CSS maintenance and the chance of accessibility or focus regressions; unnecessary unless login must remain visually unrelated.
   - Effort: Medium

## Recommendation

Use the auth-shell visual refactor with unchanged bootstrap contract. Keep the existing shared `auth.html` architecture and add narrowly scoped setup/auth presentation hooks rather than changing handlers or introducing dependencies. The implementation should make `tkt` the only brand, express benefits in user language, use simple decorative ticket/dashboard geometry that does not compete with the form, and preserve the existing semantic form controls and focus behavior. Add focused rendered-HTML assertions or a setup golden only where they fit the current infrastructure; do not broaden test scope beyond the onboarding surface.

## Risks

- Shared `auth.html` changes can unintentionally alter the login page; verify both `/setup` and `/login` renders and avoid behavior changes in `handlers_auth.go`.
- CSS changes in the shared stylesheet can affect application pages; scope new rules under `.auth-layout`/auth-specific classes and run the full Go suite.
- Decorative markup can harm semantics or keyboard navigation if it is interactive or inserted inside the form; keep it presentational and ensure `:focus-visible` remains visible.
- The requested desktop proportions and 400–460px form width must degrade cleanly at the existing ~900px breakpoint and on narrow mobile widths without horizontal overflow.
- Removing copy from the shared shell may require deciding whether login retains a product-benefit panel; this is a presentation decision, not a reason to alter authentication behavior.

## Ready for Proposal

Yes. The behavior boundary, shared template surface, existing accessibility/form contract, responsive constraints, and available test infrastructure are sufficiently understood for proposal/spec/design. The next phase should explicitly define the copy and visual acceptance criteria while treating handler, middleware, endpoint, payload, validation, loading/error, and dependency behavior as MUST-preserve constraints.
