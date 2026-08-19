# Proposal: Roles and Views

## Intent

`tkt` authenticates users but grants every active account every capability. Add roles, ownership, confidential comments, and groups so actors receive only required data and actions.

## Scope

### In Scope
- Add `Role` (`user`, `agent`, `admin`, `root`). `/setup` atomically creates the first user as `root`; other existing users backfill to `agent`, preserving current access.
- Enforce centralized application policies before queries, view composition, or rendering; template gates are presentation only.
- Persist requester ownership separately from person-only assignment. Tickets start unassigned; agent+ may self-assign/reassign to agent+ only with an audited reason.
- Add public/internal comment visibility, filtering internal content before user views are composed.
- Add admin/root group CRUD and N:N agent+ membership; capability-gate navigation, actions, role management, and categories.

### Out of Scope
- Flows, auto-assignment, group assignees, agent group panels, generic settings, user-visible internal comments, admin-created admins, or root mutation.

## Capabilities

### New Capabilities
- `role-authorization`: Hierarchy, root invariants, and server-side policy.
- `ticket-access-assignment`: Ownership, queues, person assignment, and audit.
- `comment-visibility`: Public/internal creation and disclosure.
- `group-management`: Group CRUD, membership, and future least-loaded contract.
- `role-specific-views`: Capability-gated SSR/HTMX surfaces.

### Modified Capabilities
- `user-management`: role-aware create/update/deactivate/delete; root untouchable; first-user bootstrap creates root.
- `ticket-management`: requester user ID persisted; user-role unassigned creation; agent+ assignment and edit authorization.
- `ticket-state-machine`: transition execution restricted to agent+ (assigned agents; any ticket for admin/root).
- `comment-timeline`: comment access and visibility rules; internal rejected for user.
- `category-management`: management restricted to admin/root.
- `ticket-search`: actor scope applied before filters; empty filters return scope, never all tickets.
- `auth-entry-experience`: bootstrap first user is root per role-authorization.

## Key Decisions and Approach

- Users create unassigned tickets, access/comment publicly on owned tickets, and cannot transition state.
- Agents see assigned work, comment publicly/internally, and transition legal states; admin/root see the full queue.
- Admin manages user↔agent, groups, and categories. Root also manages admins. Nobody may edit, deactivate, degrade, or delete root.
- Derive legacy ownership only from reliable audit evidence; unmatched tickets remain agent+-only. Future group targeting selects the eligible member with fewest assignments while persisting a person.

## Affected Areas

| Area | Impact |
|---|---|
| `internal/domain/`, `internal/application/` | Roles, policy, ownership, visibility, groups |
| `internal/adapters/sqlite/` | Migration, backfills, filtered queries |
| `internal/adapters/http/`, `web/templates/` | Server gates and views |
| Tests and goldens | Role and leakage coverage |

## Risks

- **CRITICAL:** unsafe owner/root inference could grant access; never guess.
- **CRITICAL:** UI-only filtering could leak internal data; application and query/view filtering are mandatory.
- **WARNING:** this cross-layer change exceeds 400 review lines; plan slices.

## Rollback Plan

Back up SQLite before migration; rollback restores that snapshot and the previous binary.

## Dependencies and Open Questions

- Strict TDD: `go test ./... -count=1`.
- Design must define operator-selected root recovery when a legacy setup user cannot be proven.

## Success Criteria

- [ ] Direct-access and rendered-view tests prove role boundaries and root invariants.
- [ ] Ownership, assignment audit, visibility, groups, and migrations pass the full suite.
