# Delta for Desk Management

## RENAMED Requirements

### Requirement: Group CRUD → Desk CRUD

Roles `admin` and `root` MUST create, rename, and delete desks. Roles `user` and `agent` MUST NOT manage desks. Desk names MUST be non-empty and unique.

#### Scenario: Desk terminology and routes
- GIVEN an authorized admin
- WHEN the admin opens or submits desk management at `/desks`
- THEN all rendered labels, links, handlers, errors, and persisted table names use “Desk” terminology

#### Scenario: Duplicate desk rejected
- GIVEN an existing desk named “Support”
- WHEN an admin creates another “Support” desk
- THEN the request is rejected with a uniqueness error

## MODIFIED Requirements

### Requirement: Group Membership

Desk membership MUST be N:N and restricted to roles `agent`, `admin`, and `root`. Role `user` MUST NOT be a member. Only `admin` and `root` MUST add or remove members. Membership and ticket assignment MUST remain person-only.
(Previously: membership was attached to groups, with group terminology.)

#### Scenario: Membership survives migration
- GIVEN a 0003 database containing groups and group_members rows
- WHEN migration 0004 runs
- THEN desks and desk_members contain the same rows and IDs
- AND membership triggers, foreign keys, and indexes are valid

#### Scenario: User cannot join a desk
- GIVEN an admin and a `user` account
- WHEN the admin attempts to add the user to a desk
- THEN the membership is rejected

#### Scenario: Desk cannot be assigned
- GIVEN a desk and a ticket
- WHEN an actor attempts desk assignment
- THEN the request is rejected and a person-only assignee is unchanged
