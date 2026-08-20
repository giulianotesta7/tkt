# Delta for Role Authorization

## ADDED Requirements

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
