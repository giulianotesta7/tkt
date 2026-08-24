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

The existing role hierarchy and permitted transitions MUST remain unchanged. Role changes MUST be initiated only from `/users/{id}/edit`, MUST be audited with the acting user, and MUST be atomic with the permitted identity/status edits submitted in that form. The former list combobox and `POST /users/{id}/role` endpoint MUST NOT be available.
(Previously: role changes were explicit list/form actions at a separate endpoint.)

#### Scenario: Role edit from user form
- GIVEN an admin editing an eligible user
- WHEN the admin selects an allowed role and submits `/users/{id}/edit`
- THEN the role changes and the transition is audited

#### Scenario: Former endpoint removed
- GIVEN any actor
- WHEN they POST `/users/{id}/role`
- THEN the endpoint is unavailable or rejected and no role changes

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

### Requirement: Workflow Definition Authorization

The system MUST authorize workflow draft access, editing, preview, and publication through the existing `CapManageCategories` capability. Only `admin` and `root` actors SHALL perform those actions. `user` and `agent` actors MUST NOT access draft definitions or builder actions. Authorization MUST be enforced server-side before draft or workflow data is queried or changed.

#### Scenario: Category manager configures a workflow

- GIVEN an `admin` or `root` actor
- WHEN the actor opens, edits, previews, or publishes a category workflow
- THEN the action is allowed through `CapManageCategories`

#### Scenario: Agent cannot preview a draft

- GIVEN an `agent` actor and a category with an editable draft
- WHEN the agent requests the builder or preview directly
- THEN the request is denied
- AND no draft data is returned

### Requirement: Workflow Task Actor Authorization

The system MUST authorize task completion from the current persisted ticket and task state, not from submitted actor identifiers or hidden controls. A `form[requester]` task MUST be completed only by the authenticated requester. A `form[assignee]` or `manual_task` MUST be completed only by the current assignee. A `claim` task MUST be completed only by an active `agent`, `admin`, or `root` who is a member of the target desk, and an `agent` MUST claim only for themselves. `admin` and `root` actors MUST NOT impersonate requester or assignee task actors. Recovery for an assignee-owned task MUST require the admin or root to reassign the ticket to themselves through the existing audited assignment flow before completion.

#### Scenario: Requester-only form is enforced

- GIVEN a pending requester form and an authenticated actor who is not the ticket requester
- WHEN that actor submits the task endpoint directly
- THEN completion is denied
- AND no answer or cursor change is persisted

#### Scenario: Current assignee completes an assignee task

- GIVEN a pending assignee form or manual task and the ticket is assigned to agent A
- WHEN agent A completes the task
- THEN completion is allowed

#### Scenario: Former assignee is denied

- GIVEN a pending assignee-owned task and a ticket reassigned from agent A to agent B
- WHEN agent A attempts completion
- THEN completion is denied
- AND the task remains pending

#### Scenario: Admin recovery uses audited self-reassignment

- GIVEN a pending assignee-owned task assigned to another person
- WHEN an `admin` attempts to complete it without first assigning the ticket to themselves
- THEN completion is denied
- WHEN the admin completes the existing audited self-reassignment flow and retries
- THEN the admin is treated as the current assignee and MAY complete the task

#### Scenario: Desk membership is enforced for claim

- GIVEN a pending claim for desk X and an active agent who is not a member of desk X
- WHEN the agent submits the claim endpoint directly
- THEN completion is denied
- AND the ticket remains unassigned by that claim

### Requirement: Pinned Claim Visibility and Transactional Recheck

For a current pinned `assign_to_desk[claim]` step, the Assignment sidebar MUST render `Assign to me` only for an active `agent`, `admin`, or `root` actor who is currently a member of that pinned desk. The server MUST NOT trust this projection: inside the workflow completion transaction it MUST recheck the pinned version, active run/current cursor, actor activity, actor role, and current desk membership before writing. Pending Actions and the timeline MUST render no claim or reason form. A stale cursor, removed membership, deactivated actor, or lost eligible role MUST return its typed failure with zero writes.

#### Scenario: Removed member cannot use a stale claim button

- GIVEN an eligible desk member rendered `Assign to me`
- AND that membership is removed before the member submits the workflow completion route
- WHEN the member submits the stale request
- THEN the server returns the typed authorization or conflict failure
- AND no assignment, cursor, state, or audit write occurs
