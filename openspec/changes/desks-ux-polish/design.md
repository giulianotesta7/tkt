# Design: Desks UX Polish

## Technical Approach

Deliver four strict-TDD slices over the existing hexagonal-lite SSR stack. Keep `modernc.org/sqlite`, `GET /tickets?q=`, `SearchService`, actor scoping, HTMX fragment swaps, and server authorization unchanged. Rename the persisted/domain concept in place, then make presentation changes through explicit view-model flags and native HTML controls.

## Architecture Decisions

| Decision | Chosen option and rationale | Rejected |
|---|---|---|
| 0004 rename | One immediate migration: drop the three group-named membership triggers; rename `groups`→`desks`, `group_members`→`desk_members`, and `desk_members.group_id`→`desk_id`; recreate desk-named triggers. In-place DDL preserves rows, IDs, and `sqlite_sequence`; SQLite rewrites the sole membership FK. | Copy/rebuild tables risks IDs, sequence, and constraints; retaining `group_id` violates the full persisted rename. |
| Source rename | After the migration commit, one compiling cross-layer mechanical commit renames `Group`/services/ports/stores/handlers/routes/view models/templates/messages/goldens to Desk. | Per-layer commits leave interfaces and wiring uncompilable; mixing UX changes obscures review. |
| Ticket search | Add shared `partials/ticket_search.html` beside “New ticket” for every role. It is the only visible `q` input. Staff keep a non-text advanced `filter_form` carrying hidden `q`; each form carries the other active values. `ShowAdvancedFilters` is false only for role `user`. | Duplicate `q` inputs; new endpoint/search implementation; capability as authorization for presentation-only filters. |
| Atomic user edit | Replace `Update`+`ChangeRole` orchestration with application use case `UpdateManagedUser` and store port `UpdateManagedUser(...expectedRole, actorID, at)`. The SQLite adapter performs one immediate transaction: guarded identity/role/active update, then `role_changes` insert iff role changed. | Handler transactions leak persistence; sequential calls permit partial success. |
| Password | Add `ChangePassword` plus `UpdatePasswordHash`; `POST /users/{id}/password` validates authorization, non-root target, non-empty secret, hashes it, and updates only the hash. No plaintext re-render or new audit category. | Password in general update or full-row store update. |
| Detail cards | Native open `<details>` with stable IDs `details`, `assignment`, `state`; persist a JSON array of collapsed IDs under `tkt:ticket-detail:collapsed:v1`. A small inline script restores after parse and records `toggle`; no JS means expanded cards still work. | HTMX toggle adds network/state complexity; jsdom adds a test-only JS stack. |
| Comments | Render hidden `visibility=public` plus staff checkbox `internal=1`, labelled “Internal comment” with staff-only help. Handler maps checked to `internal` but passes forged internal input to the existing service for rejection. Add `--internal-comment-bg` and `.timeline-comment.internal`. | Select control; client-only enforcement; silently coercing forged user input. |
| Navigation/auth | Replace the `G` in `base.html` with a 24px outline desk/table SVG (`<path d="M4 4h16v6H4zM5 10v10M19 10v10M3 10h18"/>`, `aria-hidden=true`; link `aria-label="Desks"`). Remove the login lead sentence only. | Letter icon or decorative SVG announced twice; replacement login copy. |

## Data / Migration Plan

`0004_desks.sql` runs inside the existing immediate migration transaction. It does not touch root triggers, `role_changes`, `comments.visibility`, tickets, or assignment. Recreated triggers are `trg_desk_members_no_user`, `trg_desk_members_no_user_update`, and `trg_users_no_desk_member_downgrade` and retain the exact abort semantics.

`migration_0004_test.go` first applies 0001–0003, seeds desks/members plus protected users, then applies 0004. It asserts old tables/triggers are absent; IDs/rows/timestamps survive; `PRAGMA foreign_key_list(desk_members)` targets `desks(id)` from `desk_id`; `PRAGMA index_list/index_info` prove both PK columns and desk-name uniqueness; `PRAGMA foreign_key_check` is empty; the next desk ID advances; insert/update/downgrade/user-membership failures remain enforced; rerun is a no-op. No other table references groups. Rollback uses a tested inverse transaction after reverting the binary.

## Module / UI Changes

| Area | Files |
|---|---|
| Rename | `internal/domain/desk.go`, `internal/application/{desk_service.go,ports.go,policy.go}`, `internal/adapters/sqlite/{desk_store.go,migrations/0004_desks.sql}`, `internal/adapters/http/handlers_desks.go`, `cmd/server/main.go`, `web/templates/{base.html,pages/desks_index.html}` |
| List UX | `handlers_tickets.go`, `pages/tickets_index.html`, `partials/{ticket_search.html,filter_form.html,styles.html}` |
| Users | `user_service.go`, `ports.go`, `user_store.go`, `handlers_users.go`, `pages/users_index.html`, `partials/user_form.html` |
| Detail/auth | `ticket_detail.html`, `comment_form.html`, `timeline.html`, `styles.html`, `pages/login.html` |

Responsive behavior: command actions wrap; below 640px search becomes full-width above the CTA. The status submit action says “Deactivate user” or “Reactivate user” and posts the corresponding target state through the same atomic edit use case.

## Testing Strategy and Delivery Slices

1. **Persisted rename:** migration RED tests; renamed domain/service/store/handler tests; route matrix and all shell/desk goldens.
2. **Ticket list:** application scope tests remain; handler tests prove every role gets one `q`, users get no advanced bar, forged filters cannot widen scope; goldens plus Playwright desktop/mobile search journey.
3. **Users:** service matrix tests, SQLite rollback/audit/password integration tests, httptest for removed role route and password endpoint, form/list goldens, Playwright edit/status/password journeys.
4. **Detail/auth:** comment enforcement unit/integration tests; goldens for details/open/script key/checkbox/internal class/login absence; Playwright collapse→reload persistence and responsive accessibility journey.

Each slice runs `gofmt`, `go vet ./...`, and `go test ./...`; archived OpenSpec bytes remain untouched.

## Threat Matrix

HTTP route names change, so the matrix was assessed. Documentation-like paths, Git repository selection, commit state, push state, and PR commands are all **N/A**: this change neither classifies/executes files nor invokes Git, commits, pushes, or PR tooling. Therefore no process-boundary RED tests apply; HTTP route availability/authorization is covered above.

## Open Questions

None.
