# Delta for Desk Management

Scope note: this is the spec-phase delta for change `agent-downgrade-ticket-handoff` (issue #47). It modifies the `Desk Membership` requirement so the role-`user` exclusion is upheld automatically by the atomic downgrade handoff instead of a rejecting trigger. No canonical spec file is edited here.

## MODIFIED Requirements

### Requirement: Desk Membership

Desk membership MUST be N:N and restricted to roles `agent`, `admin`, and `root`. Role `user` MUST NOT be a member of any desk, and this invariant is upheld automatically: when a managed role change targets role `user` for an account holding desk memberships, the atomic downgrade handoff removes those memberships inside the same transaction as the role change. Only `admin` and `root` MUST add or remove members. No agent-facing desk management views exist in this iteration.
(Previously: the system rejected downgrading a desk member via the trigger `trg_users_no_desk_member_downgrade` abort, surfacing a generic server error.)

#### Scenario: Admin adds an agent member

- GIVEN an `admin`, a desk, and an `agent` account
- WHEN the admin adds the agent to the desk
- THEN the membership is stored and the desk lists the agent

#### Scenario: User cannot be a member

- GIVEN an `admin` and a `user`-role account
- WHEN the admin attempts to add the user to a desk
- THEN the membership is rejected

#### Scenario: Membership removal

- GIVEN an agent listed as a desk member
- WHEN an `admin` or `root` removes them
- THEN the membership is removed without affecting the account

#### Scenario: Downgraded member's memberships removed

- GIVEN an `agent`-role account holding desk memberships
- WHEN a managed role change targets role `user` for that account
- THEN the desk memberships are removed atomically as part of the downgrade
- AND the account keeps its other attributes (identity, email, active state) unchanged

#### Scenario: After downgrade no desk_members row references a role-user account

- GIVEN a completed downgrade of an account that held desk memberships
- WHEN the `desk_members` table is inspected
- THEN no row references the now role-`user` account
- AND the trigger `trg_users_no_desk_member_downgrade` was never hit because the memberships were removed first inside the same transaction

## Notes

Traceability for `Desk Membership` (evidence paths show today's seams; T2–T4 add the implementation and tests):

| Evidence | Path | What it proves |
|---|---|---|
| Trigger that aborts downgrades today | `internal/adapters/sqlite/migrations/0004_desks.sql:30` (`trg_users_no_desk_member_downgrade`) | The rejection behavior this delta retires via automatic removal |
| Membership table | `internal/adapters/sqlite/migrations/0004_desks.sql` (`desk_members`) | Rows removed by the atomic operation |
| Managed edit route | `internal/application/user_service.go:104` (`UserService.UpdateManagedUser`) | The service path that routes through the atomic operation |
| Store tests for the atomic delete | `internal/adapters/sqlite/user_store_test.go` (T2) | Memberships deleted and role flipped in one transaction |
