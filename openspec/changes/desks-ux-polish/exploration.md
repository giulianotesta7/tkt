## Exploration: desks-ux-polish

### Executive summary

The requested work is feasible on the current SSR/HTMX architecture, but the Groups→Desks item is a persisted-domain rename, not a template-only change. The current code has no `desks` concept and no existing migration after 0003. Ticket ID/title search already exists and is the current `q` control; the user-role change is primarily presentation plus explicit form semantics, not a new search backend.

### Item findings

1. **Groups → Desks, full rename**
   - Current implementation: `internal/domain/group.go:5-20`; `internal/application/ports.go:170-182`; `internal/application/group_service.go`; `internal/adapters/sqlite/group_store.go:14-143`; `internal/adapters/http/handlers_groups.go:11-168`; wiring in `cmd/server/main.go:110,124`; tests in `internal/application/group_service_test.go`, `internal/adapters/sqlite/group_store_test.go`, `internal/adapters/http/handlers_groups_test.go`, `handlers_admin_test.go`; templates `web/templates/pages/groups_index.html`; shell fields in `handlers_tickets.go:61,573-576`; archived/current specs and runbook references under `openspec/specs/group-management/` and `openspec/changes/archive/2026-08-16-roles-and-views/`.
   - Occurrences include `GroupHandlers`, `GroupService`, `GroupStore`, `domain.Group`, `groupStore`, `/groups`, `groups_index`, `groups`, `group_members`, `group_id`, group error kinds/messages, test names, comments, and spec headings. The archive is immutable audit history and should not be rewritten; current main specs need a deliberate rename delta.
   - Migration: add `0004_desks.sql`. Prefer `ALTER TABLE groups RENAME TO desks; ALTER TABLE group_members RENAME TO desk_members` in one immediate migration transaction, preserving IDs, rows, and AUTOINCREMENT behavior. SQLite normally rewrites dependent trigger SQL during table rename, but the migration MUST verify/recreate the three membership triggers with desk names (especially the no-user-membership trigger and downgrade trigger) and verify foreign keys/indexes. A new table plus copy is riskier: it can alter row IDs, require explicit sequence handling, and temporarily duplicate constraints.
   - FTS: no FTS table references groups; `0002_fts.sql` indexes ticket title/description/comments only. No FTS migration is needed. Ticket `group_id` is not persisted; forged group assignment is rejected in `handlers_tickets.go:573-576`, so that validation must become `desk_id`/desk wording even though no assignment schema changes.
   - Tests/goldens: rename/update group unit, store, handler, harness wiring, admin route-matrix assertions, all template output containing Groups, and any golden containing navigation or group page. Add migration-upgrade coverage from a 0003 database, row preservation, trigger invariants, and idempotent rerun.

2. **User-role tickets UX and search**
   - Current implementation: `handlers_tickets.go:116-209,231-275` parses `q,state,priority,category_id,user_id`; `tickets_index.html` always renders `filter_form`; `partials/filter_form.html:3-16` already labels search “Search by ID or title”. `filters.go:60-111` implements scoped ID/title OR semantics; `application/ports.go:70-78`, `search_service.go`, and `sqlite/search_store.go` provide the port. `0002_fts.sql`/`filters.go` show the current FTS path.
   - Minimal change: retain `GET /tickets?q=...` and the existing SearchStore port; render a separate compact search form beside “New ticket” for user-role, and suppress the filter bar for that role. Decide whether agents/admins/root keep the canonical filter bar and whether the compact search is shared by all roles; the least disruptive option is user-only compact search, existing bar for staff.
   - Tests/goldens: role-specific view tests, ticket list/search handler tests (`handlers_tickets_test.go:140-169`), `filter_form.golden`, `tickets_index.golden`, `ticket_list.golden`, and user-role no-disclosure assertions. No migration.
   - Unknown: if the compact control is shared, duplicate `q` controls must not be rendered simultaneously; clarify responsive/HTMX target behavior.

3. **Role editing in `/users/{id}/edit`**
   - Current implementation: route `handlers_users.go:31-35`; list data/render is `index` and `users_index.html`; row-level form posts `/users/{id}/role`. `ChangeRole` remains a distinct audited use case in `user_service.go` and `user_store.go`.
   - Change: remove row combobox/form, add role select to edit data/form, and have the edit handler coordinate ordinary update plus role change. Preserve root/admin authorization and audit semantics; do not fold role persistence into ordinary `UserStore.Update` without an explicit audit.
   - Tests/goldens: `handlers_admin_test.go:512-533`, management route matrix, users index/form goldens, user service/store role tests. No migration.
   - Risk: a combined submit can partially apply identity changes before a role failure unless the handler validates/authorizes first or a new atomic application use case is introduced. Proposal/design should decide atomicity.

4. **Dedicated password endpoint**
   - Current implementation: `handlers_users.go:125-149` reads optional password from the edit form; `UpdateUserInput.Password` and bcrypt hashing are in `user_service.go:92-154`; `user_store.go:61-84` persists all fields.
   - Change: remove password from the edit form/update payload, add `POST /users/{id}/password`, and expose a dedicated service operation that loads/protects the target, validates non-empty password, hashes it, and updates only the password (or uses a store-specific method). Keep password absent from re-rendered values and add a separate form/confirmation UX.
   - Tests/goldens: admin handler update/password tests, user service tests, password tests, user form golden, new endpoint integration tests. No migration.
   - Risk: concurrent edits and root/admin protection must match existing Update rules; a store method is safer than loading/modifying a full user in the handler.

5. **Active control clarity**
   - Current implementation: `handlers_users.go:144`, `user_form.html:18-22` use checkbox `active` with label “Active” and explanatory footnote; unchecked means false.
   - Change: explicit action label such as “Deactivate user” when active and “Reactivate user” when inactive, while preserving a stable submitted boolean contract. The form re-render path at `handlers_users.go:210-224` must preserve state.
   - Tests/goldens: admin deactivation tests (`handlers_admin_test.go:189-205`), user form and users index goldens, root-protection tests. No migration.
   - Unknown: whether the product wants a single toggle label or separate POST actions; decide in proposal/spec.

6. **Desks icon**
   - Current implementation: `web/templates/base.html:28-29` renders literal `G`; the current category icon is an inline folder SVG at lines 23-25.
   - Change: replace the literal with an accessible inline table/desk SVG; update href/title/aria-label/nav active value to `/desks`/“Desks”. No backend migration beyond rename slice.
   - Tests/goldens: shell/navigation assertions and all full-page goldens. No separate migration.

7. **Ticket detail cards and comments**
   - Current implementation: `partials/ticket_detail.html:34-116` has three always-open `.card` blocks: Properties, Assignment, State. Comment visibility is a select in `partials/comment_form.html:8-17`; internal badge is in `timeline.html:8-14`; base styles are in `partials/styles.html:104+` and timeline-specific rules need inspection/extension.
   - Change: use native `<details><summary>` cards (progressive, no JS) and rename Properties heading to “Details”. Replace the visibility select with an `Internal comment` checkbox that maps checked to `visibility=internal`; retain the hidden public value for users and server-side rejection. Add a visually distinct internal-comment background class in timeline/style rules.
   - Tests/goldens: detail/comment handler tests, `ticket_detail.golden`, `tickets_show.golden`, `comment_form.golden`, `timeline.golden`, plus internal-comment visibility assertions. No migration.
   - Risk: checkbox absence is indistinguishable from forged public input unless handler normalization remains explicit; preserve server-side capability enforcement.

8. **Login copy**
   - Current implementation: `web/templates/pages/login.html:4` contains “Use your work email and password.”
   - Change: remove only that lead paragraph unless the approved auth spec requires replacement copy.
   - Tests/goldens: `auth_login.golden`, auth handler/golden tests, and auth-entry-experience spec delta if user-facing copy is normative. No migration.

### Affected areas

- HTTP: `internal/adapters/http/handlers_groups.go`, `handlers_tickets.go`, `handlers_users.go`, `handlers_auth.go`, related tests.
- Application/domain: `internal/application/group_service.go`, `ports.go`, `user_service.go`, `domain/group.go`.
- SQLite: `group_store.go`, `sqlite.go`, `migrations/0004_*.sql`, migration tests; no FTS schema change.
- Templates/styles: `web/templates/base.html`, groups/desks page, users/index and user form, tickets index/filter/detail/comment/timeline/login, styles.
- Specs: current `openspec/specs/group-management`, `role-specific-views`, `ticket-search`, `ticket-management`, `comment-visibility`, `comment-timeline`, `user-management`, `auth-entry-experience`; archived material should remain unchanged but may be referenced in the proposal/runbook.

### Slice proposal

1. **Persisted Desks rename** — ~250–360 authored lines plus mechanical renames/goldens; includes 0004 migration, domain/application/adapter/HTTP/template/wiring/tests and main spec rename delta. Keep under 400 by excluding unrelated UX.
2. **Ticket list role UX** — ~100–180 lines; user-role filter suppression, compact ID/title search, role tests/goldens/spec delta. Existing search backend should remain unchanged.
3. **User management forms** — ~220–350 lines; role moved to edit, dedicated password endpoint/use case, explicit active action, atomicity tests/goldens/spec delta. This is the highest application-risk slice.
4. **Ticket detail and auth polish** — ~160–280 lines; collapsible cards, checkbox comment visibility, internal timeline styling, login copy, goldens/tests/spec deltas.

### Recommendation

Proceed to proposal. Make the migration rename strategy, combined user-edit atomicity, and scope of the compact ticket search explicit decisions. Use native HTML details/summary and preserve existing server-side authorization and SearchStore behavior.

### Risks and unknowns

- A destructive source rename can leave archived references, comments, error kinds, test names, and OpenSpec terminology inconsistent; archive content must remain immutable while current specs receive an intentional rename.
- SQLite trigger SQL and foreign-key metadata must be verified after table renames; migration upgrade tests are mandatory.
- Combining role, identity, active state, and password operations risks partial updates; define transaction/use-case boundaries before implementation.
- Existing ticket-search spec says the canonical filter bar includes text; suppressing it for users requires a role-specific-view/ticket-search clarification rather than silently violating the current wording.
- Native checkbox semantics and HTMX fragment rendering need golden coverage for checked/unchecked and user/staff variants.

### Ready for proposal

Yes. The proposal should record the four decisions above and preserve the current `q` SearchStore path rather than inventing a new endpoint.
