# Desk Management Specification

## Purpose

Defines desks as named sets of agent-plus personnel with N:N membership, managed by `admin` and `root` only. Desks are the future base for flows; no desk-targeted assignment is implemented in this iteration.

## Requirements

### Requirement: Desk CRUD

Roles `admin` and `root` MUST create, rename, and delete desks. Roles `user` and `agent` MUST NOT manage desks. Desk names MUST be non-empty and unique; duplicate names MUST be rejected.

#### Scenario: Admin creates desk

- GIVEN an `admin` and a non-empty unused desk name
- WHEN the admin creates the desk
- THEN the desk is stored and listed

#### Scenario: Duplicate desk name rejected

- GIVEN an existing desk "Support"
- WHEN an `admin` creates another desk named "Support"
- THEN the creation is rejected with a uniqueness error

#### Scenario: Agent cannot manage desks

- GIVEN an `agent`-role actor
- WHEN they attempt desk creation, rename, or delete
- THEN the request is denied

### Requirement: Desk Membership

Desk membership MUST be N:N and restricted to roles `agent`, `admin`, and `root`. Role `user` MUST NOT be a member of any desk. Only `admin` and `root` MUST add or remove members. No agent-facing desk management views exist in this iteration.

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

### Requirement: Person-Only Assignment Invariant

Desk-targeted assignment MUST NOT be implemented in this change: a desk MUST NOT become a ticket assignee. Documented future contract: a desk-targeted assignment SHALL resolve to the desk member with the fewest assigned tickets (least-loaded) and SHALL persist that person as the assignee.

#### Scenario: Desks never become assignees

- GIVEN a desk with members and a ticket
- WHEN any actor attempts to assign the ticket to the desk
- THEN the assignment is rejected
- AND only a person with role `agent`+ can be assigned