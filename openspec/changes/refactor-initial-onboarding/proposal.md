# Proposal: Refactor Initial Onboarding

## Intent

Replace the technical first-run experience with a focused `tkt` welcome. Setup and login will communicate product benefits through one identity while preserving authentication and bootstrap contracts.

## Scope

### In Scope
- Give setup and login only `tkt` branding and product-benefit copy.
- Present setup in a vertically centered desktop split (55–60% presentation / 40–45% form) with a 400–460px form card that remains the visual focus.
- Add restrained, noninteractive ticket/dashboard decoration; hide it at the existing ~900px single-column breakpoint.
- Use the approved exact setup copy; remove technical, open-source, storage, state, audit, “Ticket Desk,” Go, HTMX, SQLite, and bcrypt messaging.
- Preserve fields, labels, autocomplete, keyboard/focus behavior, WCAG AA, bootstrap behavior, first-user role, endpoints, payloads, validation, loading, and errors.
- Add presentation coverage where current test infrastructure supports it.

### Out of Scope
- Authentication, authorization, persistence, endpoint, payload, or validation changes.
- New dependencies, JavaScript, animation, or interactive decoration.
- Changes to post-login application screens or broader branding work.

## Capabilities

### New Capabilities
- `auth-entry-experience`: Setup and login identity, content, responsiveness, accessibility, decoration, and preserved contracts.

### Modified Capabilities
None; no existing OpenSpec capability specs are present.

## Approach

Refactor shared `auth.html`, scoped CSS, and page copy. Use semantic HTML and CSS-only geometry, keep handlers unchanged, and verify both routes.

## Affected Areas

| Area | Impact | Description |
|------|--------|-------------|
| `web/templates/auth.html` | Modified | Shared `tkt` auth identity and composition |
| `web/templates/pages/setup.html` | Modified | Exact setup copy with preserved form contract |
| `web/templates/pages/login.html` | Modified | Matching identity with unchanged login behavior |
| `web/templates/partials/styles.html` | Modified | Scoped layout, responsive, focus, and overflow rules |
| `internal/adapters/http/handlers_auth_test.go` | Modified | Focused behavior/presentation assertions |
| `internal/adapters/http/testdata/` | Optional | Auth golden fixtures |

## Risks

| Risk | Likelihood | Mitigation |
|------|------------|------------|
| Shared-shell login regression | Medium | Verify setup and login rendering/behavior |
| Mobile overflow or weakened focus | Medium | Scope CSS; test breakpoint, narrow widths, and focus states |
| Decoration competes with form | Low | Keep it noninteractive, restrained, and hidden on mobile |

## Rollback Plan

Revert auth templates, scoped styles, and presentation tests together; no data or API rollback is required.

## Dependencies

- Existing templates, CSS, HTTP tests, and optional golden update path.

## Success Criteria

- [ ] Setup and login show the approved `tkt` identity without prohibited technical content.
- [ ] Setup preserves all current behavior, semantics, accessibility, and form contracts.
- [ ] Desktop and responsive layouts meet the approved hierarchy with no horizontal scroll.
- [ ] Existing and added supported tests pass without new dependencies.
