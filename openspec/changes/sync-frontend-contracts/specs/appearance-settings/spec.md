# Delta for Appearance Settings

Scope note: this is the spec-phase delta for change `sync-frontend-contracts` (issue #74). It adds the missing canonical spec `appearance-settings` for the existing `/settings` appearance panel. No runtime, template, test, or migration is introduced here.

## ADDED Requirements

### Requirement: Appearance Settings Access and Navigation

Only actors holding `CapManageUsers` (roles `admin` and `root`) SHALL access `GET /settings` and `POST /settings/appearance`; any other authenticated role MUST receive 403 and no settings mutation. The shell rail navigation MUST show a Settings link only for `admin`/`root` shells and MUST NOT show it for other roles.

#### Scenario: Admin can open Settings

- GIVEN an authenticated actor with role `admin` or `root`
- WHEN the actor navigates to `GET /settings`
- THEN the response is 200 and the page title is `Settings` with the subtitle `Instance appearance` and an `Appearance` panel

#### Scenario: Non-admin cannot open Settings

- GIVEN an authenticated actor with role `user` or `agent`
- WHEN the actor navigates to `GET /settings` or posts to `POST /settings/appearance`
- THEN the response is 403 and no setting is changed
- AND the shell rail does not show a link to `/settings`

### Requirement: Internal Comment Background Selection

`GET /settings` MUST render the current internal-comment background color as the selected radio in the Appearance panel, alongside the supported options. The supported colors MUST be exactly `#E8EEFF` (Blue), `#EFE9FB` (Violet), and `#FFF6DC` (Yellow) in that canonical order; green is not offered. The shell CSS MUST carry the same authoritative color as the CSS variable `--internal-comment-bg`.

#### Scenario: Settings shows the current color and the three options

- GIVEN the seeded instance with no prior update
- WHEN `GET /settings` renders for an admin
- THEN the panel shows three radios named `internal_comment_bg` with values `#E8EEFF`, `#EFE9FB`, `#FFF6DC` labeled `Blue`, `Violet`, `Yellow`
- AND `#E8EEFF` is the selected radio
- AND the shell `<style>` contains `--internal-comment-bg:#E8EEFF;`

### Requirement: Update Internal Comment Background

An authorized actor MAY update the internal-comment background by posting `internal_comment_bg` to `POST /settings/appearance` with one of the supported colors. On a supported value the server MUST persist the color and respond with a 303 redirect to `/settings`; the next `GET /settings` MUST render the newly selected radio and the updated shell CSS variable. Posting an unsupported color MUST re-render the Settings page with status 422 and an inline `error-banner`, and MUST NOT change the persisted color.

#### Scenario: Supported color persists and survives reload

- GIVEN an admin on `GET /settings` seeing the seeded `#E8EEFF`
- WHEN the admin posts `internal_comment_bg=#EFE9FB` to `POST /settings/appearance`
- THEN the response is 303 to `/settings`
- AND the persisted setting is `#EFE9FB`
- AND the following `GET /settings` renders `value="#EFE9FB" checked` and `--internal-comment-bg:#EFE9FB;`

#### Scenario: Unsupported color is rejected with no write

- GIVEN an admin on `GET /settings` with the persisted color `#E8EEFF`
- WHEN the admin posts `internal_comment_bg=#123456` to `POST /settings/appearance`
- THEN the response is 422 and the body contains `error-banner`
- AND the persisted color remains `#E8EEFF`
- AND a missing settings row falls back to the default `#E8EEFF` on read

### Requirement: Internal Comment Presentation Effect

Internal comments in staff-visible ticket timelines MUST use the configured internal-comment background color via the CSS variable `--internal-comment-bg`. The timeline MUST distinguish internal and public comments without relying on color alone in the prose, and MUST NOT expose internal content to `user`-role views.

#### Scenario: Internal comment styled with the configured background

- GIVEN a ticket timeline visible to staff containing one `public` and one `internal` comment
- WHEN the ticket detail page renders
- THEN the `internal` comment entry carries `timeline-comment internal` and the stylesheet applies `background:var(--internal-comment-bg)` with the current configured color
- AND ordering remains newest-first and user-role detail views still exclude internal content at the store boundary

## Notes

Traceability for `appearance-settings`:

| Evidence | Path | What it proves |
|---|---|---|
| Routes and capability gate | `internal/adapters/http/handlers_settings.go:Register` + `requireCapability(CapManageUsers)` | `GET /settings` and `POST /settings/appearance` exist and are gated |
| Panel markup and options | `web/templates/pages/settings_index.html` + `handlers_settings.go:appearanceOptions` | Panel renders three radios `Blue`/`Violet`/`Yellow` mapping to hex values |
| Allowed set and default | `internal/application/settings_service.go:AllowedInternalCommentBg` + `DefaultInternalCommentBg` | Exactly three allowed colors, default `#E8EEFF` |
| Capability + validation before store | `settings_service.go:SetInternalCommentBg` + `settings_service_test.go` | Non-admin and invalid color rejected before store |
| Migration seeds default, fallback | `migrations/0005_instance_settings.sql` + `settings_store.go` + `settings_store_test.go` | Seeds default, roundtrip, absent fallback |
| Per-request shell stamping | `middleware_auth.go` + `styles.html:9` | Shell reads color per request and renders `--internal-comment-bg` |
| Effect on timeline | `timeline.html` + `styles.html:.timeline-comment.internal` + `golden_test.go:TestTicketDetailPresentationContract` | Internal entries styled via `var(--internal-comment-bg)` |
| HTTP behavior proven | `handlers_settings_test.go:TestSettings*` | Index, persist, invalid, non-admin, rail link |
