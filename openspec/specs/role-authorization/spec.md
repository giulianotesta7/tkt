# Role Authorization Specification

## Purpose

Defines the four-role hierarchy (`user`, `agent`, `admin`, `root`), centralized server-side policy, the role-management matrix, and invariant protection for root.

## Requirements

### Requirement: Role Hierarchy and Policy

The system MUST assign each user exactly one role from `user`, `agent`, `admin`, `root`. Capabilities MUST follow the hierarchy `user` < `agent` < `admin` < `root`, each role inheriting all lower-role capabilities. Every authorization check MUST be enforced server-side at the application boundary BEFORE any query or view composition; template gating MUST NOT substitute for a server check.

#### Scenario: Hierarchy enforced

- GIVEN actors in roles `user`, `agent`, `admin`, `root`
- WHEN a capability check runs
- THEN `user` and `agent` are denied and `admin` and `root` are allowed
- AND the check completes before any user data is queried

#### Scenario: Direct access denied

- GIVEN a `user`-role actor requesting an admin-only route
- WHEN the request reaches the server
- THEN it is denied with 403 or 404
- AND no restricted data is returned

### Requirement: Root Invariants

The system MUST prevent every actor — including root itself — from deactivating, degrading, editing, or deleting the root account, and from granting or revoking root. Root MUST NOT be creatable through user creation or role-grant flows.

#### Scenario: Nobody touches root

- GIVEN the root account and any actor, including root
- WHEN the actor attempts to deactivate, edit, delete, or demote root
- THEN the action is rejected
- AND the root account remains active with role `root`

#### Scenario: Root role not grantable

- GIVEN an existing root
- WHEN any actor submits a role-grant or user-creation targeting role `root`
- THEN the request is rejected

### Requirement: First-User Root Bootstrap

When the users table is empty, `/setup` MUST atomically create the first user with role `root`. Concurrent bootstrap requests MUST produce exactly one root and never an ordinary account or multiple roots.

#### Scenario: First user is root

- GIVEN an empty users table
- WHEN the first visitor completes `/setup`
- THEN the created user has role `root`
- AND bootstrap is unavailable once the user exists

#### Scenario: Concurrent bootstrap

- GIVEN two simultaneous `/setup` submissions
- WHEN both are processed
- THEN exactly one root is created
- AND the other submission fails without creating an account

### Requirement: Role Management Matrix

The system MUST restrict role management: `admin` SHALL change roles between `user` and `agent` only; `root` SHALL additionally grant and remove `admin`; no actor SHALL create an admin except root. Role changes MUST be explicit, audited use cases with the acting user as actor.

#### Scenario: Admin manages user-agent only

- GIVEN an `admin`
- WHEN the admin promotes an active `user` to `agent` and later demotes them back
- THEN both operations succeed
- AND each change is audited with the admin as actor

#### Scenario: Admin cannot create admin

- GIVEN an `admin`
- WHEN the admin attempts to grant role `admin` to an `agent`
- THEN the request is rejected

#### Scenario: Root grants and removes admin

- GIVEN `root`
- WHEN root grants `admin` to an `agent` and later removes it
- THEN both operations succeed

### Requirement: Operator-Selected Root Recovery

When a legacy database cannot prove which user the original `/setup` created, the system MUST NOT infer a root. Root selection MUST require an explicit operator-selected identity; until provided, migration MUST fail closed.

#### Scenario: Ambiguous legacy root requires operator

- GIVEN a legacy DB whose original setup user cannot be proven
- WHEN migration runs
- THEN no user becomes root automatically
- AND an explicit operator-selected identity is required

#### Scenario: Reliable legacy setup user becomes root

- GIVEN a legacy DB where the original setup user is reliably identifiable
- WHEN migration runs
- THEN that user becomes `root`
- AND remaining users backfill to `agent`