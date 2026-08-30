# Proposal: Sync Frontend Contracts

> **Outcome first:** This is a documentation-only synchronization for issue #74. Three frontend contracts already implemented and test-proven on `origin/main` (`3484ee9`) never reached the canonical OpenSpec specs. This change syncs them, touches no runtime, template, CSS, JS, test, golden, migration, skill, CI, or dependency file, and unblocks versioned Playwright frontend coverage once merged.

## Intent / Problem

The canonical specs describe contracts that no longer match the shipped product.

- `ticket-management` still mandates compact native `<details><summary>` cards named Details, Assignment, and State with `localStorage` expansion state. The shipped `/tickets/{id}` renders an always-visible `Properties` sidebar with `Properties`, `Assignment`, and `State` sections and no `<details>` or `localStorage` script.
- `comment-timeline` still promises that any accessible closed ticket accepts comments. The shipped application rejects comments on `resolved`, `closed`, and `cancelled` tickets at the application boundary before any store write, enforced by `domain.IsClosed` and proven by both service and HTTP tests.
- `/settings` and its appearance configuration are live, wired, tested, and navigable, but no canonical `appearance-settings` spec exists.

A reviewer or future contributor reading the specs would build or test against stale contracts. The implementation is authoritative for this synchronization.

### Alternatives considered

| Alternative | Verdict |
|---|---|
| Leave specs stale and author E2E against them | Rejected, E2E would encode the wrong contract and drift from the product |
| Change the product to match the stale `<details>`/localStorage and open-comment text | Rejected, product decisions are confirmed canonical and must not be reversed |
| Fold appearance settings into `comment-visibility` or `role-authorization` | Rejected, appearance is an instance setting with its own route, capability, persistence, and visual effect |

## Scope

### In scope

- Delta `ticket-management`: MODIFIED `Ticket Detail Presentation` to the shipped always-visible `Properties` / `Assignment` / `State` structure, with the stale `<details>`/localStorage contract explicitly retired via the `(Previously: ...)` history convention, and scenarios that prove the visible sections and the read-only metadata/closed behavior without freezing incidental CSS classes.
- Delta `comment-timeline`: MODIFIED `Add Comment` so only non-closed accessible tickets accept comments; `resolved`/`closed`/`cancelled` reject with a rejection before persistence. Preserves the existing role/visibility and append-only rules.
- Delta `appearance-settings`: ADDED requirements that exactly match the existing `/settings` behavior: route access and navigation, visible current setting and options, supported update and feedback, persistence after reload, invalid-input/no-write behavior where implemented, and the observable effect on internal-comment presentation. Full-page and HTMX behavior only where tests prove it.
- Validation of the new active change under `openspec validate --all --strict` and `openspec validate --archived`.

### Out of scope (non-goals)

- Any change to Go runtime, `web/templates/**`, `web/static/**`, CSS, JS, tests, goldens, migrations, `openspec/specs/**` canonical files, `openspec/changes/archive/**`, CI, dependencies, or coverage.
- Any new product behavior, flag, route, or setting beyond the three listed settings colors.
- Any edit to the sibling active change `sync-workflow-polish-contracts` or to the `docs/openspec-frontend-contract-sync` worktree outside `openspec/changes/sync-frontend-contracts/**`.
- A `design.md`: this change has no implementation design.

## Deliverable 1: `ticket-management` — Ticket Detail Presentation sync

The stale `<details>`/localStorage presentation never shipped. The live template `web/templates/partials/ticket_detail.html` renders a `cards` layout with a `conversation` area and an `evidence` sidebar containing three always-visible sections headed `Properties`, `Assignment`, and `State`. Requester and Category remain read-only metadata, timestamps remain read-only, and `resolved`/`closed`/`cancelled` tickets render read-only by hiding every mutation control while keeping that metadata. The golden `TestTicketDetailPresentationContract` negatively asserts the absence of `<details open id="details"` and `tkt:ticket-detail:collapsed:v1`, and `TestClosedTicketDetailReadOnly` proves the closed read-only rendering. No incidental CSS class is frozen.

## Deliverable 2: `comment-timeline` — Closed-ticket comment rejection

The live rule is `domain.IsClosed` (`resolved`, `closed`, `cancelled`). `application.CommentService.Add` checks it after scope lookup and before `CommentStore.Add`, returning `domain.ErrMsgCommentOnClosedTicket` as a `ForbiddenError`. `internal/application/comment_service_test.go:TestAddCommentOnClosedTicketRejected` and `TestAddCommentOnOpenTicketAccepted` prove the boundary. At the HTTP boundary `POST /tickets/{id}/comments` maps it to 403 and stores nothing, proven by `internal/adapters/http/handlers_detail_test.go:TestTicketCommentOnClosedTicketRejected` and the `internal/adapters/http/handlers_comment_test.go` helpers.

## Deliverable 3: `appearance-settings` — New spec for the existing Settings panel

Evidence chain: `internal/adapters/http/handlers_settings.go` (routes `GET /settings` and `POST /settings/appearance`, both gated on `CapManageUsers`); `web/templates/pages/settings_index.html` (Appearance panel with three radio swatches `Blue`/`Violet`/`Yellow` mapping to `#E8EEFF`/`#EFE9FB`/`#FFF6DC`); `internal/application/settings_service.go` (`AllowedInternalCommentBg`, `DefaultInternalCommentBg`, capability and allowed-color validation before the store); `internal/adapters/sqlite/settings_store.go` + `migrations/0005_instance_settings.sql` (migration seeds the default, absent row falls back to the default); `internal/adapters/http/middleware_auth.go` (per-request `InternalCommentBg` stamped into context and rendered into `web/templates/partials/styles.html` as `--internal-comment-bg`); `web/templates/partials/timeline.html` + `styles.html` (`.timeline-comment.internal` uses `var(--internal-comment-bg)`). Tests `internal/application/settings_service_test.go`, `internal/adapters/sqlite/settings_store_test.go`, `internal/adapters/http/handlers_settings_test.go:TestSettings*` and `golden_test.go:TestTicketDetailPresentationContract` prove each leg. Green is intentionally reserved and not offered.

## Validation

- `openspec validate --all --strict --no-interactive` MUST pass with the new change recognized.
- `openspec validate --archived --no-interactive` MUST pass (new change remains active, not archived).
- `git diff --check` MUST be clean.
- Only `openspec/changes/sync-frontend-contracts/**` appears in `git status`.

## Rollback plan

Revert the single docs commit that adds `openspec/changes/sync-frontend-contracts/**`. No data migration, no runtime impact, no coordinated revert. E2E authoring will again be blocked until the sync lands.

## Unblocks

Once merged, the canonical specs will match the shipped frontend contracts, so versioned Playwright coverage can be authored against them without re-litigating the three debts.
