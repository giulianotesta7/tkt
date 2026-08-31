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
    
The system MUST validate every state transition against the allowed matrix and MUST reject invalid transitions with an error. State changes MUST NOT occur silently. For every ticket with an identifiable requester (requester user ID not NULL), state `resolved` MUST mean that the requester's confirmation is awaited; it MUST NOT be treated as a terminal state. The `resolved` → `closed` transition is conditional: it MUST be applied only through one of the closure paths defined in Requester Confirmation Closure — requester confirmation, manual agent closure of a requester-less ticket, or a workflow terminal `close_ticket` step. A manual `resolved` → `closed` transition on a ticket that has a requester MUST be rejected for every actor. Transition execution MUST be restricted by actor role: role `user` MUST NOT perform transitions of any ticket, except the requester confirmation and rejection paths defined in Requester Confirmation Closure, which are available only to that ticket's requester; role `agent` SHALL transition only tickets assigned to them through legal transitions; roles `admin` and `root` SHALL transition any ticket, still subject to the requester-confirmation closure condition. Authorization MUST be enforced server-side before the transition is applied. (Previously: any authorized agent could manually move `resolved` → `closed`, `resolved` carried no confirmation semantics, and role `user` had no transition exception.)
    
Allowed transitions:
    
- `new` → `in_progress`, `resolved`, `cancelled`
- `in_progress` → `resolved`, `cancelled`
- `resolved` → `closed` (only via a defined closure path), `in_progress` (requester rejection with workflow detachment, or agent reopen)
- `closed` → `in_progress` (reopen)
    
Invalid: `new` → `closed`; `resolved` → `cancelled`; `closed` → `cancelled`; `closed` → `resolved`; any move into `new`; any move out of `cancelled`; any `resolved` → `closed` transition not routed through one of the defined closure paths.
    
#### Scenario: Valid forward path
    
- GIVEN a ticket in state `new` assigned to an `agent` with an identifiable requester
- WHEN the agent moves it to `in_progress`, then to `resolved`, and the requester confirms the resolution
- THEN each transition succeeds and the final state is `closed`
- AND the closure is recorded through the requester-confirmation path
    
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
    
- GIVEN a ticket in any state and a `user`-role actor who is not that ticket's requester
- WHEN the user attempts any transition
- THEN the request is denied
- AND the state remains unchanged
    
#### Scenario: Agent transitions only assigned tickets
    
- GIVEN a ticket assigned to agent Y and a different `agent` X
- WHEN X attempts a legal transition on the ticket other than a requester-confirmation path
- THEN the request is denied
- AND the state remains unchanged
    
#### Scenario: Agent cannot close a requester-owned resolved ticket
    
- GIVEN a `resolved` ticket with an identifiable requester
- WHEN an `agent`, `admin`, or `root` attempts a manual transition to `closed`
- THEN the transition is rejected with an error
- AND the state remains `resolved`
    
### Requirement: Reopen with Reason
    
The system MUST allow `resolved` → `in_progress` and `closed` → `in_progress` (reopen). Reopen from `closed` MUST require a non-empty reason. The reason MUST be recorded in the audit event for that transition. Reopen from `resolved` MUST NOT require a reason. A reopen from `resolved` performed by the requester as a rejection of the resolution MUST detach the workflow link as defined in Requester Confirmation Closure; a reopen from `resolved` performed by an agent MUST NOT detach the workflow link. Reopen from `closed` MUST continue to require a non-empty reason regardless of who reopens. (Previously: reopen from `resolved` carried no distinction between the requester rejection and the agent reopen, and no workflow-detachment semantics existed.)
    
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
    
### Requirement: Requester Confirmation Closure
    
A `resolved` ticket with an identifiable requester MUST become `closed` only when that requester confirms the resolution. The requester confirmation MUST be available only to the authenticated actor whose session user equals the ticket's requester user ID, only while the ticket is in `resolved`, and MUST be enforced server-side before any write. A requester confirmation on a ticket that is not in `resolved` MUST be rejected by the state guard with no state change. A `resolved` ticket with requester user ID NULL MAY be closed manually by an authorized `agent`, `admin`, or `root`; this manual agent closure MUST be audited (see Audit Log). A workflow terminal `close_ticket` step MAY close a `resolved` ticket directly without requester confirmation. When the requester rejects the resolution, the ticket MUST transition `resolved` → `in_progress`: for a workflow-pinned ticket the workflow link MUST be detached so the ticket continues as a manual ticket, and for an already-manual ticket the transition MUST return it to `in_progress` without other changes. An agent reopen of a `resolved` ticket MUST NOT detach the workflow link.
    
#### Scenario: Requester confirms and the ticket closes
    
- GIVEN a `resolved` ticket with requester user ID R
- WHEN the authenticated requester R confirms the resolution
- THEN the ticket transitions to `closed`
- AND `closed_at` is stamped and the audit trail records the requester-confirmation closure path
    
#### Scenario: Confirmation on an already-closed ticket is impossible
    
- GIVEN a `closed` ticket
- WHEN the requester attempts to confirm the resolution
- THEN the request is rejected by the state guard
- AND the state remains `closed` with no new transition audit
    
#### Scenario: Agent closes a requester-less resolved ticket
    
- GIVEN a `resolved` ticket with requester user ID NULL
- WHEN an authorized `agent`, `admin`, or `root` transitions it to `closed`
- THEN the transition succeeds
- AND the audit trail records a manual agent closure
    
#### Scenario: Workflow terminal step closes a resolved ticket directly
    
- GIVEN a `resolved` ticket reaches a workflow `close_ticket` terminal step
- WHEN the automatic step executes
- THEN the ticket transitions to `closed` without requester confirmation
- AND the audit trail records the workflow-terminal closure path
    
#### Scenario: Requester rejection returns a workflow-pinned ticket to manual in-progress
    
- GIVEN a `resolved` ticket pinned to a workflow version
- WHEN the authenticated requester rejects the resolution
- THEN the ticket transitions to `in_progress`
- AND the workflow link is detached so the ticket continues as a manual ticket
    
#### Scenario: Requester rejection of an already-manual ticket
    
- GIVEN a `resolved` ticket with no workflow link
- WHEN the authenticated requester rejects the resolution
- THEN the ticket transitions to `in_progress`
- AND the ticket remains a manual ticket
    
#### Scenario: Agent reopen does not detach the workflow
    
- GIVEN a `resolved` workflow-pinned ticket
- WHEN an authorized agent reopens it to `in_progress`
- THEN the transition succeeds
- AND the workflow link remains attached

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
