# Group Management Specification

## Purpose

Defines groups as named sets of agent-plus personnel with N:N membership, managed by `admin` and `root` only. Groups are the future base for flows; no group-targeted assignment is implemented in this iteration.

## Requirements

### Requirement: Group CRUD

Roles `admin` and `root` MUST create, rename, and delete groups. Roles `user` and `agent` MUST NOT manage groups. Group names MUST be non-empty and unique; duplicate names MUST be rejected.

#### Scenario: Admin creates group

- GIVEN an `admin` and a non-empty unused group name
- WHEN the admin creates the group
- THEN the group is stored and listed

#### Scenario: Duplicate group name rejected

- GIVEN an existing group "Support"
- WHEN an `admin` creates another group named "Support"
- THEN the creation is rejected with a uniqueness error

#### Scenario: Agent cannot manage groups

- GIVEN an `agent`-role actor
- WHEN they attempt group creation, rename, or delete
- THEN the request is denied

### Requirement: Group Membership

Group membership MUST be N:N and restricted to roles `agent`, `admin`, and `root`. Role `user` MUST NOT be a member of any group. Only `admin` and `root` MUST add or remove members. No agent-facing group management views exist in this iteration.

#### Scenario: Admin adds an agent member

- GIVEN an `admin`, a group, and an `agent` account
- WHEN the admin adds the agent to the group
- THEN the membership is stored and the group lists the agent

#### Scenario: User cannot be a member

- GIVEN an `admin` and a `user`-role account
- WHEN the admin attempts to add the user to a group
- THEN the membership is rejected

#### Scenario: Membership removal

- GIVEN an agent listed as a group member
- WHEN an `admin` or `root` removes them
- THEN the membership is removed without affecting the account

### Requirement: Person-Only Assignment Invariant

Group-targeted assignment MUST NOT be implemented in this change: a group MUST NOT become a ticket assignee. Documented future contract: a group-targeted assignment SHALL resolve to the group member with the fewest assigned tickets (least-loaded) and SHALL persist that person as the assignee.

#### Scenario: Groups never become assignees

- GIVEN a group with members and a ticket
- WHEN any actor attempts to assign the ticket to the group
- THEN the assignment is rejected
- AND only a person with role `agent`+ can be assigned