---
name: ticket-state-machine
status: proposed
change: tkt-mvp
---

# Ticket State Machine Specification

## Purpose

Defines the domain-enforced five-state lifecycle (`new`, `in_progress`, `resolved`, `closed`, `cancelled`) and the exact allowed transitions. The machine MUST be enforced by the domain, not the UI.

## Requirements

### Requirement: State Transition Enforcement

The system MUST validate every state transition against the allowed matrix and MUST reject invalid transitions with an error. State changes MUST NOT occur silently. Transition execution MUST be restricted by actor role: role `user` MUST NOT perform transitions of any ticket; role `agent` SHALL transition only tickets assigned to them through legal transitions; roles `admin` and `root` SHALL transition any ticket. Authorization MUST be enforced server-side before the transition is applied. (Previously: any logged-in user could transition any ticket.)

Allowed transitions:

- `new` → `in_progress`, `resolved`, `cancelled`
- `in_progress` → `resolved`, `cancelled`
- `resolved` → `closed`, `in_progress` (reopen)
- `closed` → `in_progress` (reopen)

Invalid: `new` → `closed`; `resolved` → `cancelled`; `closed` → `cancelled`; `closed` → `resolved`; any move into `new`; any move out of `cancelled`.

#### Scenario: Valid forward path

- GIVEN a ticket in state `new` assigned to an `agent`
- WHEN the agent moves it to `in_progress`, then `resolved`, then `closed`
- THEN each transition succeeds and the final state is `closed`

#### Scenario: Invalid transition rejected

- GIVEN a ticket in state `new`
- WHEN an `admin` attempts to move it directly to `closed`
- THEN the transition is rejected with an error
- AND the state remains `new`

#### Scenario: Terminal cancelled

- GIVEN a ticket in state `cancelled`
- WHEN an `admin` attempts any transition out of it
- THEN the transition is rejected

#### Scenario: User role cannot transition

- GIVEN a ticket owned by a `user`-role actor and any legal transition target
- WHEN the user attempts a transition
- THEN the request is denied
- AND the state remains unchanged

#### Scenario: Agent transitions only assigned tickets

- GIVEN a ticket assigned to agent Y and a different `agent` X
- WHEN X attempts a legal transition on the ticket
- THEN the request is denied
### Requirement: Reopen with Reason

The system MUST allow `resolved` → `in_progress` and `closed` → `in_progress` (reopen). Reopen from `closed` MUST require a non-empty reason. The reason MUST be recorded in the audit event for that transition. Reopen from `resolved` MUST NOT require a reason.

#### Scenario: Reopen from closed with reason

- GIVEN a closed ticket
- WHEN the user reopens it to `in_progress` providing a reason
- THEN the transition succeeds and the reason is recorded in the audit event

#### Scenario: Reopen from closed without reason

- GIVEN a closed ticket
- WHEN the user attempts to reopen it without a reason
- THEN the transition is rejected with an error

#### Scenario: Reopen from resolved

- GIVEN a resolved ticket
- WHEN the user reopens it to `in_progress`
- THEN the transition succeeds and no reason is required

### Requirement: Resolution and Closure Timestamps

The system MUST set `resolved_at` when a ticket enters `resolved` and `closed_at` when it enters `closed`. Reopen from `resolved` MUST clear `resolved_at`. Reopen from `closed` MUST clear both `resolved_at` and `closed_at`.

#### Scenario: Reopen clears resolved_at

- GIVEN a resolved ticket with `resolved_at` set
- WHEN the user reopens it to `in_progress`
- THEN `resolved_at` is cleared
- AND `closed_at` remains empty

#### Scenario: Reopen from closed clears both

- GIVEN a closed ticket with `resolved_at` and `closed_at` set
- WHEN the user reopens it to `in_progress` with a reason
- THEN both `resolved_at` and `closed_at` are cleared
