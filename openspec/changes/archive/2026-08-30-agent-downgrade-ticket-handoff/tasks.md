# Tasks: Agent Downgrade Ticket Handoff

Change `agent-downgrade-ticket-handoff` (issue #47, type:bug) makes the managed downgrade of a desk member ONE atomic lifecycle operation: delete desk memberships, hand off open assigned tickets, flip the role with the same guarded semantics, and record the role change — all or nothing. Tasks are marked complete only after their artifact exists and the listed evidence passes.

## Global scope guard (applies to every task)

- Spec-delta work edits ONLY: `openspec/changes/agent-downgrade-ticket-handoff/proposal.md`, `openspec/changes/agent-downgrade-ticket-handoff/tasks.md`, and the four deltas under `openspec/changes/agent-downgrade-ticket-handoff/specs/`.
- Implementation work (T2–T4) edits ONLY the surfaces named in those tasks: the Go application layer (`internal/application/`), sqlite adapter (`internal/adapters/sqlite/`), HTTP handlers (`internal/adapters/http/`), and E2E tests (`e2e/`).
- Never edit: `openspec/specs/**` (canonical specs are synchronized only by the archive phase), `openspec/changes/archive/**`, sibling active changes, migrations, `internal/adapters/sqlite/workflow_uow.go`, templates, CSS/JS, CI, or dependencies.
- The approved design contract is authoritative: do not redesign desk resolution, replacement selection, audit-event shape, or transaction boundaries while implementing. Deviations require a new approved contract.
- Evidence before authoring: every delta requirement traces to the approved contract and the code paths named in its Notes table.

## T0. Baseline and active-change hygiene

- [x] Confirm the worktree base matches `origin/main` at `cdf5cce` and that the working tree is clean before this change's files are created.
- [x] Confirm `openspec/changes/agent-downgrade-ticket-handoff` does not collide with any existing active or archived change name.
- [x] Confirm the baseline gates pass before this delta lands: `openspec validate --all --strict --no-interactive` and `openspec validate --archived --no-interactive` both green at `cdf5cce` (17 items passed).

Acceptance: base hash `cdf5cce`, clean tree, no name collision, baseline validation green.

## T1. Author the spec delta for issue #47

- [x] Author `openspec/changes/agent-downgrade-ticket-handoff/proposal.md` with the outcome-first blockquote, Alternatives considered table ((a) blocking + manual membership removal, (b) per-ticket `TicketService.Assign` loop, (c) new sealed `WorkflowOperation` type — all rejected; chosen: single atomic `UserStore` operation routed explicitly from the service when the target role is `user`), In/Out of scope, one deliverable section per delta, and the validation gate.
- [x] Author `openspec/changes/agent-downgrade-ticket-handoff/specs/user-management/spec.md` with ADDED `Agent Downgrade Ticket Handoff` covering the atomic operation, desk-resolution priority, rollback, and deactivation carve-out, with scenarios for atomic success, least-loaded reassignment, unassignment fallbacks, closed-ticket historical assignment, all-or-nothing rollback, and deactivation preserving assignments.
- [x] Author `openspec/changes/agent-downgrade-ticket-handoff/specs/desk-management/spec.md` with MODIFIED `Desk Membership` upholding the role-`user` exclusion automatically via the atomic handoff (Previously: rejection via trigger abort), with scenarios for membership removal during downgrade and no `desk_members` row referencing a role-`user` account after downgrade.
- [x] Author `openspec/changes/agent-downgrade-ticket-handoff/specs/ticket-access-assignment/spec.md` with ADDED `Automatic Reassignment on Downgrade` covering desk resolution priority and the deterministic least-loaded rule, with scenarios for least-loaded win, lowest-id tie-break, never self-replacement, no-eligible-member and unresolvable-desk unassignment, and eligibility restricted to active `agent`/`admin`/`root` desk members.
- [x] Author `openspec/changes/agent-downgrade-ticket-handoff/specs/audit-log/spec.md` with ADDED `Downgrade Handoff Audit Events` covering per-ticket `ActionUpdate` events on field `user` with initiating actor, role-downgrade reason, `DeskID` when resolved, NULL step index, and the `role_changes` row as today, with scenarios for reassignment fields, unassignment fields, and NULL step index outside pinned runs.
- [x] Validate the change: `openspec validate --all --strict --no-interactive` and `openspec validate --archived --no-interactive` both pass with the new change recognized.

Acceptance: four deltas + proposal exist, follow the `sync-frontend-contracts` format (outcome-first blockquote, Alternatives table, `(Previously: ...)` retirement, traceability Notes tables), and both validations pass.

## T2. Go TDD implementation: application port + service route + sqlite atomic operation

- [x] RED: add a failing application-layer test proving `UserService.UpdateManagedUser` routes a role change to `user` for an account holding desk memberships through the new atomic `UserStore` operation (fake store records the call and the guarded expected role).
- [x] GREEN: extend the `UserStore` port with the atomic downgrade-handoff operation and route from `UserService.UpdateManagedUser` when the target role is `user`; keep all other managed edits on the existing path.
- [x] RED: add failing sqlite store tests for the atomic operation: memberships deleted, open (`new`/`in_progress`) tickets ordered by id reassigned or left unassigned, role flipped via the same guarded UPDATE, `role_changes` row inserted, all inside ONE `BEGIN IMMEDIATE` transaction.
- [x] GREEN: implement the sqlite atomic operation (membership delete → per-ticket handoff → guarded role UPDATE → `role_changes` insert → commit) reusing the deterministic least-loaded rule (`fewest open tickets globally`, lowest user id tie-break, active `agent`/`admin`/`root` pool, downgraded account excluded).
- [x] TRIANGULATE: desk resolution priority per ticket — latest desk-bearing audit event first, else first `assign_to_desk` step (by step order) in the pinned workflow version, else unassigned; closed/resolved/cancelled tickets untouched; failure mid-operation rolls back role, memberships, and tickets together.
- [x] TRIANGULATE: handoff audit events per reassigned/unassigned ticket — `ActionUpdate` on field `user` with from/to, initiating admin actor ID, role-downgrade reason, `DeskID` when resolved, NULL step index; `role_changes` preserved.
- [x] Implement typed error mapping so the trigger's abort condition surfaces as a meaningful domain error, not a raw driver error.

Acceptance: application and sqlite tests cover the atomic path, resolution priority, rollback, and audit shape; `go test ./internal/application/... ./internal/adapters/sqlite/...` passes.

## T3. HTTP drawer error mapping tests

- [x] Map the downgrade path in the user-edit handler: no new mapping needed — the existing drawer error contracts (`mapError`) remain and the downgrade no longer produces any unhandled trigger abort; success follows the existing HX save contract.
- [x] RED→GREEN: HTTP tests proving a successful downgrade of a desk member renders success (`TestUsersDrawerDowngradeNoGeneric500`: 200 HX save contract with retarget/swap/trigger headers, membership removed, open ticket reassigned to the eligible peer). Typed failure paths reuse the existing drawer contracts (validation 422, duplicate 409, stale-role 404) with no partial mutation — unchanged and still green.

Acceptance: `go test ./internal/adapters/http/...` passes; the drawer downgrade flow has no generic-500 path for the trigger condition.

## T4. E2E journey

- [x] Author a Playwright journey in `e2e/tests/users.spec.ts`: two agents join the seeded desk, a fresh category publishes a least_loaded `assign_to_desk` workflow, the created ticket lands on agent A (workflow assignment), the downgrade via `/users/{id}/edit` observes success (200 HX save contract, no generic 500), membership removal in the desks member list, reassignment to the remaining member, reload persistence, and the handoff audit reason in the timeline.
- [x] Add the unassignment branch: an agent with no desk membership downgrades while holding an open ticket with unresolvable desk context (no audit desk row, unpinned workflow) — the downgrade succeeds and the ticket becomes unassigned.

Acceptance: the E2E journey passes against the running app and encodes exactly the delta scenarios.

## T5. Validation gates

- [x] Run `go test ./...` and `go build ./...` — both green (full suite `-count=1` across cmd/server, adapters/http, adapters/sqlite, application, domain; build silent; targeted downgrade tests pass `-race`; gofmt clean; go vet clean).
- [x] Run `openspec validate --all --strict --no-interactive` and `openspec validate --archived --no-interactive` — both green with the change active (18 passed / 5 passed, 0 failed).
- [x] Confirm `git status` shows only the allowed edit surfaces: the six change files, the Go application/sqlite/http implementation surfaces named in T2–T3, and `e2e/` tests (independent verification audit confirmed the exact file set; no canonical spec, migration, workflow-engine, template, or CI file touched).

Acceptance: all gates green; no canonical spec, migration, workflow-engine, or unrelated file touched.
