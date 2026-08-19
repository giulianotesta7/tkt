# Design: Roles and Views

## Technical Approach

Add actor-aware use cases. `application.Policy` derives capability and ticket scope before store calls; scoped methods exclude unauthorized rows. Retain `modernc.org/sqlite` and the immediate-transaction runner.

## Architecture Decisions

| Option | Tradeoff | Decision / spec / slice |
|---|---|---|
| HTTP guards only | Bypassable | Central `policy.go`; handlers fail early. Role Authorization, S1/S7. |
| Role rank vs ACL | Closed model | `domain.Role`, `User.Role`, typed `Capabilities`/`TicketScope`. S1. |
| Scoped reads vs hide later | Port churn; prevents leaks | Actor scope in ticket/search/detail/comment ports. Access/Visibility, S3/S5. |
| Duplicate state rules | Drift risk | Policy authorizes; existing `Ticket.Transition` validates. S4. |
| Group assignees | Violates invariant | Persist groups/members only; tickets retain person ID. S6. |

## Domain, Contracts, and Data Flow

`Ticket` gains immutable `RequesterUserID *int64` while `UserID` remains assignee; `Comment` gains `Visibility` (`public|internal`); `AuditEvent` gains `ActorUserID`/`Reason`; groups have IDs, unique names, and N:N membership. Use cases accept `actor domain.User` and authorize before queries.

    session actor → Policy → scoped store query → ViewBuilder → pageData{Capabilities}
                                comments WHERE visibility → timeline → template

`CommentStore.ListByTicket` receives policy-derived visibility; SQL excludes internal rows for users and application filters before `mergeTimeline`. Agents may claim unassigned→self or reassign their ticket; admin/root assign any agent+ person. A→B requires a trimmed reason; NULL→person does not. Events store session actor ID/snapshot.

## Route Policy

| Routes | Capability |
|---|---|
| `GET /`, `/tickets`, `/tickets/{id}` | user=requester, agent=assignee, admin/root=all |
| `GET /tickets/new`, `POST /tickets` | authenticated; user assignment rejected |
| `POST /tickets/{id}/edit`, `/transition` | assigned agent or admin/root |
| `POST /tickets/{id}/assign` | assignment rule above |
| `POST /tickets/{id}/comments` | public user; public/internal agent+ |
| `/users*` | admin/root; `/users/{id}/role`; admin user↔agent, root also admin |
| `/groups*`, `/categories*` | admin/root |
| `/setup` | public only while empty; atomic root bootstrap |

Templates gate nav/actions by capabilities. Users get “My tickets,” agents assigned queue, admin/root full queue plus management, and root admin controls. Preserve HTMX partial conventions.

## Persistence and Recovery

`0003_roles_and_views.sql` adds constrained `users.role` (default `agent`), indexed nullable requester ID, public-default constrained comment visibility, groups/members, role audit, and audit actor/reason. Backfill requester only when one creation event and snapshot match one unique surviving user; otherwise NULL (agent+-only). Application checks plus DB triggers reject user members/downgrades; a partial unique index permits one root, and triggers reject root update/delete.

Legacy `id=1` is reliable under existing AUTOINCREMENT and becomes root; never use `MIN(id)`. If users exist without ID 1/root, startup fails closed. One-shot `server -recover-root=<user-id>` atomically verifies no root and the selected user, activates/promotes it, records recovery, then exits. `BootstrapRoot` uses `BEGIN IMMEDIATE` and conditional insert, so concurrent `/setup` yields one root.

Future-only contract: group assignment selects the eligible active member with fewest assigned tickets (user-ID tiebreak) and persists that person, never the group.

## File Changes

Create `internal/application/{policy,group_service}.go`, `internal/domain/group.go`, SQLite migration/group store, HTTP group handler, and group templates. Modify domain user/ticket/comment/audit; application ports/services/views; SQLite user/ticket/comment/filter/UoW stores; HTTP handlers/wiring; `cmd/server/main.go`; shell and ticket/user/timeline partials. Tests stay colocated; goldens stay under HTTP `testdata/`.

## Testing Strategy

RED→GREEN→REFACTOR per slice: table-driven policy/root/state/assignment/comment tests; in-memory SQLite migration/backfill/constraint/concurrency tests; `httptest` direct URL/form/HTMX denial and no-query spies; leakage assertions; goldens via `-update`, inspect, rerun. Gates: `gofmt`, `go vet ./...`, `go test ./... -count=1`, `go test -race ./...`.

## Threat Matrix

| Boundary | Applicability / response / RED test |
|---|---|
| HTTP routes | Applicable: full/HTMX/direct requests check capability before query; forged-field/denial tests. |
| Root recovery flag | Applicable: invalid user, existing root, and concurrent recovery fail closed; command/store tests. |
| Documentation-like paths | N/A: no executable classification. |
| Git repository selection | N/A: no Git integration. |
| Commit state | N/A: no commit automation. |
| Push state | N/A: no push automation. |
| PR commands | N/A: no PR command composition. |

## Reviewable Slice Plan

| Slice (authored-line estimate) | Dependency |
|---|---|
| S1 roles, policy, migration/backfills (360) | base |
| S2 atomic setup, recovery, root invariants (330) | S1 |
| S3 requester scopes/search (320) | S1 |
| S4 assignment/state/audit (360) | S3 |
| S5 comment visibility/timeline (280) | S3; parallel with S4 |
| S6 groups persistence/use cases/HTTP (390) | S1; parallel with S3–S5 |
| S7 user/category authorization and role views (380) | S2–S6 |
| S8 goldens, route matrix, race/CI hardening (300; generated goldens excluded) | S7 |

Each slice carries RED tests and is a chained PR under `ask-on-risk`; every PR requires `gentle-ai review`. RDD commits the exact reviewed tree and receipt atomically—no post-review edits.

## Migration / Rollout

Back up SQLite, deploy migration plus binary together, exercise recovery before serving ambiguous legacy DBs; rollback restores the backup and prior binary.

## Open Questions

None.
