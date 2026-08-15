## Exploration: roles-and-views

### Current State

The application authenticates every non-public request but does not authorize capabilities. `SessionMiddleware` resolves the session's `UserID` to a full `domain.User`; every active user then reaches the same ticket, user, and category routes. `pageData` exposes only `NavActive` and `CurrentUser`, and `web/templates/base.html` always renders Tickets, Users, and Categories.

#### 1. Comment visibility

Comments are separate append-only rows, not audit events. `comments` stores `ticket_id`, an author name snapshot, body, and timestamp; `audit_events` is a separate append-only table. `ViewBuilder.TicketView` loads both streams and `mergeTimeline` presents them newest-first through `TimelineItem`. `CommentService.Add` accepts any existing ticket and derives the author name from the session user; neither reads nor writes apply ticket ownership or visibility rules.

An internal/public flag fits additively on `comments` (for example a constrained visibility value), then flows through `domain.Comment`, `CommentStore`, `CommentService.Add/ListByTicket`, `ViewBuilder`, handler form parsing, and timeline templates. Filtering MUST occur before comments reach a user-facing `TicketView`; hiding only timeline markup would leak through service/store callers and search indexing. Because `0002_fts.sql` indexes comment bodies through triggers even though the current search box is title/ID-only, future comment search must also enforce visibility.

#### 2. Ticket assignment and ownership

`tickets.user_id` already means assignee, not requester (`NULL` means unassigned). Creation and inline editing can assign any active user, and assignment changes are audited. The current model stores requester identity only as mutable-looking name/email snapshots (`requester_name`, `requester_email`) derived from the creating session; it stores no requester user foreign key. `TicketQuery.UserID` and `buildTicketWhere` filter assignee only.

"Own tickets" therefore cannot be enforced robustly with the current schema. Email matching is unsafe because admins can change email and historical rows keep the old snapshot. The model needs a distinct immutable `requester_user_id` foreign key while retaining requester name/email snapshots for historical display. Agent assignment can continue using the existing `user_id`, but assignment inputs and lists must be restricted to active `agent`, `admin`, or `root` users. Agent list scope should normally be assigned-to-self; admin/root can see the full queue, including unassigned tickets.

#### 3. State transitions

The domain matrix currently allows: `new -> in_progress|resolved|cancelled`; `in_progress -> resolved|cancelled`; `resolved -> closed|in_progress`; `closed -> in_progress` with a required reason; and no transition from `cancelled`. Timestamp and audit behavior are centralized in `Ticket.Transition`; handlers merely expose `allowedNext` and call the service.

Keep this state machine as the legality source and layer actor authorization above it. A coherent role policy is: users do not transition tickets; assigned agents may move their assigned tickets through legal transitions; admins/root may transition any ticket. Whether agents may act on unassigned tickets or self-assign is product policy that should be made explicit in the proposal/spec rather than encoded implicitly in the matrix.

#### 4. Account, role, and configuration management

The existing administrative surface already provides full user CRUD, password reset-by-edit, activation/deactivation, and category CRUD. Today every user can access it. The admin surface can naturally own role assignment and category management. Role promotion/demotion should be an explicit use case with an actor, not a generic unrestricted `UserService.Update` field.

"App configuration" has no persisted model or route today. Runtime configuration is limited to `TKT_DB_PATH`, `TKT_LISTEN`, fixed page size, and server timeout constants; these are deployment concerns and should not be exposed casually through the UI. The concrete in-app configuration visible in the current product is category management. Any broader settings (default priority, assignment policy, ticket numbering, branding, retention, email, etc.) require separate requirements and storage and should remain out of this change unless explicitly selected.

#### 5. Root protections

Current guards are referential only: assigned users cannot be deleted; their deactivation remains allowed; unreferenced users can be deleted after their sessions are removed. There is no last-user, self-deactivation, self-deletion, last-admin, or bootstrap-user protection. Categories similarly cannot be deleted while referenced.

Root needs an immutable identity/role rule: no actor, including root, may delete, deactivate, or demote the root account; non-root actors may not grant or revoke root; and root should not be creatable through normal user creation. Admins must not edit root's role/status, and authorization must be enforced in application use cases as well as HTTP routes. The setup operation must create root atomically with the first-user condition to avoid two concurrent bootstrap requests producing an ordinary account or multiple roots.

#### 6. Migration path

Migrations are embedded from `internal/adapters/sqlite/migrations/*.sql`, sorted by filename, parsed by leading numeric version, and applied once per version in individual transactions recorded in `schema_migrations`. Existing versions are `0001_init.sql` and `0002_fts.sql`; the next additive migration is `0003_roles_and_views.sql`.

The migration needs at least `users.role`, `tickets.requester_user_id`, and comment visibility. Existing comment rows should backfill as public. Existing ticket requester ownership cannot always be reconstructed reliably: matching `requester_email` to the current unique user email is possible only when a matching account still exists and has not changed email. Unmatched tickets require an explicit compatibility policy (for example admin/root-only visibility until claimed), never silent attribution to an arbitrary user.

Existing installations also cannot prove that the lowest currently present user is the original `/setup` user because the current system permits deleting an unreferenced first user. For installations where that user still exists, `MIN(id)` is the best available root candidate and remaining users should backfill to `agent` to preserve current operational access. Ambiguous databases need an explicit operator-selected root migration path or startup failure; silently choosing a replacement root is a privilege-escalation risk.

#### 7. Role-specific views and server-side gates

The shell must hide Users and Categories from user/agent roles and may expose an admin/configuration section to admin/root, but navigation is only presentation. Every ticket list/detail/create/comment/update/transition route and every user/category route needs server-side authorization. Prefer policy checks at application use-case boundaries, with HTTP middleware/handler guards for early 403/404 responses; this prevents alternate handlers or future adapters from bypassing policy.

User view: create unassigned tickets, list/detail only tickets where `requester_user_id` is self, and add/read public comments on those tickets. Agent view: list assigned-to-self tickets, read public and internal comments, add either visibility, change legal states, and edit assignment/priority only if policy permits. Admin/root view: full ticket queue and agent capabilities, user role/status management, and category/configuration management. Templates requiring capability flags include the shell nav, ticket create form assignment controls, list filters, detail properties editor, transition controls, comment visibility controls, timeline items, and user/category pages.

#### 8. Test and golden impact

Strict TDD should start at domain/application boundaries, then stores, then HTTP. Primary tests affected or expanded are `internal/domain/{state,ticket}_test.go`; `internal/application/{user,auth,ticket,comment,views,search}_service_test.go` plus fakes; `internal/adapters/sqlite/{sqlite,user,ticket,timeline,search}_store_test.go`; and `internal/adapters/http/{middleware_auth,handlers_auth,handlers_admin,handlers_tickets,handlers_detail,harness,render,golden}_test.go`. New scenarios must prove direct URL/form access is denied, not merely hidden.

Likely golden changes include `render_full_page`, `tickets_index`, `tickets_new`, `tickets_show`, `ticket_form`, `ticket_list`, `ticket_detail`, `comment_form`, `timeline`, `users_index`, `users_new`, and `user_form`; category goldens may change if the shell or admin framing changes. Goldens must be regenerated only through the repository's `-update` path, inspected, and rerun without `-update`. The final required suite remains `go test ./... -count=1`.

### Affected Areas
- `internal/domain/user.go` — add the role model and capability vocabulary.
- `internal/domain/ticket.go`, `internal/domain/state.go` — distinguish requester ownership from assignment while preserving transition legality.
- `internal/domain/comment.go` — carry public/internal visibility.
- `internal/application/{ports,user_service,ticket_service,comment_service,search_service,views}.go` — enforce policy before data reaches stores or views.
- `internal/adapters/sqlite/migrations/0003_roles_and_views.sql` — additive schema and controlled backfill.
- `internal/adapters/sqlite/{user,ticket,comment}_store.go`, `filters.go` — project and filter roles, ownership, assignment, and visibility.
- `internal/adapters/http/middleware_auth.go`, `handlers_{auth,tickets,users,categories}.go` — server-side route/use-case authorization and root bootstrap.
- `internal/adapters/http/render.go`, `web/templates/base.html`, `web/templates/partials/` — capability-aware shell and controls.
- `internal/**/*_test.go`, `internal/adapters/http/testdata/*.golden` — role matrix, migration, leakage, and rendered-view coverage.

### Approaches
1. **Role enum plus centralized application policies** — store one constrained role per user, add requester identity and comment visibility, and make actor-aware use cases apply ownership/capability predicates.
   - Pros: preserves the current simple architecture; policy is testable outside HTTP; server-side enforcement is reusable; additive migration is feasible.
   - Cons: touches most read/write paths; requires explicit migration decisions for historical ownership and root selection.
   - Effort: High

2. **HTTP-only route guards and template conditionals** — authorize in handlers/middleware while leaving application services and data queries mostly unchanged.
   - Pros: fewer initial structural changes; quick visible role differentiation.
   - Cons: unsafe because service/store callers can bypass policy; risks loading internal comments before hiding them; cannot robustly implement requester ownership without schema changes.
   - Effort: Medium initially, High remediation risk

### Recommendation
Use approach 1. Define a small closed `Role` enum and explicit policy predicates, persist immutable requester ownership separately from assignee identity, and filter comments/tickets before view composition. Keep sessions carrying only `UserID`; resolving the user on every request already picks up role changes immediately. Treat categories as the concrete current app configuration surface and defer a generic settings subsystem. Preserve the existing state machine and authorize who may invoke it rather than duplicating transitions per role.

Before implementation, the proposal/spec must resolve two migration policies: how an operator selects root when the original setup user is no longer identifiable, and how unmatched historical requester snapshots are exposed. It must also state whether agents may self-assign or work unassigned tickets.

### Risks
- **CRITICAL — historical ownership cannot be inferred safely:** `tickets` has requester name/email snapshots but no requester user ID (`internal/adapters/sqlite/migrations/0001_init.sql`, `internal/domain/ticket.go`). Incorrect backfill could expose tickets to the wrong user.
- **CRITICAL — root identity may be unknowable:** current user deletion has no bootstrap-user guard (`internal/application/user_service.go`, `internal/adapters/sqlite/user_store.go`). `MIN(id)` is not proof that the surviving user was first.
- **CRITICAL — UI-only gating would leak data:** `ViewBuilder` currently loads all comments and audit entries before rendering (`internal/application/views.go`), and every route shares one auth shell (`internal/adapters/http/middleware_auth.go`).
- **WARNING — assignment and requester concepts currently overlap in product language:** `tickets.user_id` is definitively assignee while requester is snapshot-only; queries filter only assignee (`internal/adapters/sqlite/filters.go`).
- **WARNING — generic app configuration is undefined:** only categories have an in-app management model; deployment environment and timeout settings are not suitable defaults for an admin UI (`cmd/server/main.go`).
- **WARNING — broad golden churn can obscure authorization defects:** capability behavior needs focused service/handler assertions before updating `internal/adapters/http/testdata/*.golden`.

### Ready for Proposal
Yes, with the proposal explicitly surfacing the root-selection, unmatched-requester migration, and agent self-assignment decisions. The implementation is expected to exceed the 400-line review budget and should be planned as reviewable slices during tasks/apply under the `ask-on-risk` delivery strategy.
