# Proposal: Desks UX Polish

## Intent

Adopt **Desks** terminology and simplify ticket, user, and authentication workflows without weakening role, access, or disclosure guarantees.

## Scope

### In Scope
- Full Groups→Desks rename across routes, code, persistence, UI, tests, and current specs.
- Role-aware ticket, user, detail, and login refinements.

### Out of Scope
- Desk assignment, load balancing, or changes to person-only membership/assignment.
- New search endpoints, search semantics, roles, authorization rules, or edits to archived OpenSpec artifacts.

## User Stories and Acceptance Criteria

1. **Desks:** Admins manage Desks at `/desks`; active surfaces use Desk terminology, while records and IDs survive upgrade.
2. **Ticket search:** Every role gets an ID/title search beside “New ticket.” Staff retain the full filter bar; users see no filter bar and results remain limited to their tickets.
3. **Role editing:** Admin/root change eligible roles only at `/users/{id}/edit`; list controls and `POST /users/{id}/role` are removed, and combined edits succeed or fail atomically.
4. **Passwords:** Changes use only `POST /users/{id}/password`; general edit contains no password field or echoed secret.
5. **Account status:** Editing presents an explicit “Deactivate user” or “Reactivate user” action, preserving root and role protections.
6. **Navigation:** Desks uses an accessible desk/table SVG instead of a letter icon.
7. **Ticket detail:** Details, Assignment, and State are compact, expanded-by-default `<details>` cards persisted in localStorage. “PROPERTIES” becomes “Details”; staff use an “Internal comment” checkbox; internal comments are visually distinct; server enforcement is unchanged.
8. **Login:** The sentence “Use your work email and password.” is absent.

## Capabilities

### New Capabilities
None.

### Modified Capabilities
- `group-management`: rename capability and requirements to Desk terminology; preserve person-only membership.
- `role-specific-views`, `ticket-search`: define role-specific controls and shared scoped search.
- `user-management`: relocate role, password, and active-state workflows.
- `ticket-management`, `comment-visibility`: specify persistent detail cards and comment presentation.
- `auth-entry-experience`: remove the obsolete login copy.

## Migration and Delivery

Migration `0004_desks.sql` transactionally renames `groups`→`desks` and `group_members`→`desk_members`, recreates desk-named membership triggers, and verifies foreign keys, indexes, IDs, rows, and agent-plus constraints. Membership and ticket assignment remain person-only.

Slices: (1) persisted rename, (2) ticket-list role UX, (3) user-management forms, (4) ticket-detail and auth polish. Each follows strict TDD.

## Risks and Rollback

- **High:** SQLite rename/trigger/FK drift. Prove 0003 upgrade and invariants before release.
- **Medium:** Combined edits require design of atomic validation/transaction boundaries.
- **Low:** localStorage key/version and responsive search placement remain design decisions.

Before migration, roll back binaries. After 0004, use a tested inverse transaction restoring tables, triggers, and foreign-key checks without discarding rows.

## Success Criteria

- [ ] All eight acceptance criteria pass role-aware handler, in-memory SQLite, and golden tests.
- [ ] `gofmt`, `go vet ./...`, and `go test ./...` pass; archived specs remain byte-unchanged.
