# Desk Management Specification

## Purpose

Defines desks as named sets of agent-plus personnel with N:N membership, managed by `admin` and `root` only. Desks are the future base for flows; no desk-targeted assignment is implemented in this iteration.

## Requirements

### Requirement: Desk CRUD

Roles `admin` and `root` MUST create, rename, and delete desks. Roles `user` and `agent` MUST NOT manage desks. Desk names MUST be non-empty and unique.

#### Scenario: Desk terminology and routes
- GIVEN an authorized admin
- WHEN the admin opens or submits desk management at `/desks`
- THEN all rendered labels, links, handlers, errors, and persisted table names use “Desk” terminology

#### Scenario: Duplicate desk rejected
- GIVEN an existing desk named “Support”
- WHEN an admin creates another “Support” desk
- THEN the request is rejected with a uniqueness error

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

### Requirement: Responsive Desk Master/Detail Index

The desks index MUST present a simple master/detail management surface. The desk list MUST show each desk name and member count. Selecting a desk MUST reveal its detail with rename, a directly visible native destructive submit button labelled exactly `Delete desk`, add-member, and remove-member controls. `Delete desk` MUST use the existing POST route and server-side authorization, MUST preserve existing rejected-delete inline errors, and MUST NOT be hidden behind `More actions`, an overflow/disclosure, or a replacement client-side authority mechanism. Creating a desk MUST remain available through a disclosed new-desk form. Desktop may show list and detail together; narrow layouts MUST stack them without horizontal overflow and keep every action, including `Delete desk`, keyboard reachable with visible focus. The design MUST preserve existing tkt palette, typography, spacing, focus treatment, and simple user/admin philosophy; screenshot references may inform structure only and MUST NOT override those tokens.

#### Scenario: Admin manages the selected desk

- GIVEN an admin or root opens the desks index with multiple desks
- WHEN they select one desk
- THEN its detail shows the desk name, member list, and member-management controls
- AND the list continues to show member counts for every desk

#### Scenario: Direct desk delete remains server-authoritative

- GIVEN an admin or root views a deletable selected desk
- WHEN they activate the visible `Delete desk` submit button
- THEN the existing desk-delete POST route handles the request
- AND existing server-side authorization remains authoritative
- AND no `More actions` disclosure or client-side mutation authority is required

#### Scenario: Rejected direct delete remains inline

- GIVEN an authorized actor submits `Delete desk` for a desk the server rejects for deletion
- WHEN the existing POST route re-renders the management surface
- THEN the rejection appears inline in that surface
- AND the visible `Delete desk` control remains available according to the existing authorization and state rules

#### Scenario: New desk form stays disclosed until needed

- GIVEN an admin or root opens the desks index
- WHEN they have not chosen to create a desk
- THEN the new-desk form is not expanded
- WHEN they activate its disclosure control
- THEN the existing create form becomes available without changing the route contract

#### Scenario: Narrow desk management stacks without overflow

- GIVEN an admin or root views desks at 390px wide
- WHEN a selected desk has members and management controls
- THEN the list and detail stack in a readable order without horizontal scrolling
- AND every rename, delete, add-member, and remove-member control remains keyboard reachable with visible focus

### Requirement: Existing Desk Operations Remain Authoritative

The master/detail presentation MUST use the existing desk create, rename, delete, add-member, and remove-member routes and their server-side authorization. It MUST NOT introduce client-side authority, new desk roles, or alternate mutation endpoints.

#### Scenario: Existing authorization still gates a direct mutation

- GIVEN an actor without desk-management permission submits an existing desk mutation route directly
- WHEN the server handles the request
- THEN the request is denied by the existing server-side authorization
- AND no desk or membership write occurs
