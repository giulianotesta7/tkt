---
name: audit-log
status: proposed
change: tkt-mvp
---

# Audit Log Specification

## Purpose

Defines the append-only audit trail covering every state transition and field change. There are no silent mutations. The actor of every event is the logged-in user from the session.

## Requirements

### Requirement: Transition Audit Events

The system MUST append an audit event for every state transition, recording actor, action (from state → to state), and timestamp. A manual transition MUST preserve the authenticated actor's user ID. An automatic workflow transition MUST use actor `workflow`, actor user ID NULL, action `transition`, field `state`, the actual from/to values, and no reason. The persisted audit MUST retain that automatic actor value, and the ticket timeline MUST omit actor text for it instead of rendering a `Workflow` label; no ticket-facing copy or actor label may use the word `workflow`.
(Previously: every transition actor was the logged-in user, while genuine system actions used the fixed actor `sistema`.)

#### Scenario: Transition recorded

- GIVEN a ticket in state `new`
- WHEN it transitions to `in_progress`
- THEN an audit event is appended with the transition action and timestamp

#### Scenario: Actor comes from session

- GIVEN a logged-in user
- WHEN that user performs a transition
- THEN the audit event records that user as actor
- AND preserves that user's actor ID

#### Scenario: Automatic workflow transition is attributed

- GIVEN an automatic workflow step transitions a ticket from `in_progress` to `resolved`
- WHEN the transition commits
- THEN its audit event has actor `workflow`, actor user ID NULL, action `transition`, field `state`, and the actual state values
- AND the ticket timeline renders that event with no actor text

### Requirement: Field Change Audit Events

The system MUST append an audit event for every field change, recording actor, field, from_value, to_value, and timestamp. A manual field change MUST preserve the authenticated actor's user ID. An automatic workflow field change MUST use actor `workflow` and actor user ID NULL.
(Previously: every field-change actor was the logged-in user from the session.)

#### Scenario: Field change recorded

- GIVEN a ticket with priority `medium`
- WHEN its priority changes to `high`
- THEN an audit event is appended with field `priority`, from `medium`, to `high`, and timestamp

#### Scenario: Actor from session for field edits

- GIVEN a logged-in user
- WHEN that user edits a ticket field
- THEN the audit event records that user as actor
- AND preserves that user's actor ID

#### Scenario: Automatic workflow assignment is attributed

- GIVEN `least_loaded` automatically selects a person for an unassigned ticket
- WHEN the assignment commits
- THEN the assignment audit uses actor `workflow` and actor user ID NULL

### Requirement: No Silent Mutations

Every state transition and every field change MUST produce an audit event. The system MUST NOT apply a mutation without recording it.

#### Scenario: Every mutation audited

- GIVEN a ticket
- WHEN one transition and two field edits occur
- THEN exactly three corresponding audit events exist, in occurrence order
    
### Requirement: Closure Attribution
    
Every closure of a ticket (a transition into `closed`) MUST be recorded in the audit trail so that a reader of the audit history can determine which closure path closed the ticket. The system MUST distinguish at least these closure paths: closure by requester confirmation, closure by a workflow terminal `close_ticket` step, and manual agent closure of a requester-less ticket (requester user ID NULL). Two different closure paths MUST NOT be recorded indistinguishably. Every closure MUST still produce its transition audit event or events, and No Silent Mutations MUST continue to hold for every closure path.
    
#### Scenario: Requester-confirmation closure is distinguishable
    
- GIVEN a requester-owned ticket in `resolved`
- WHEN the requester confirms the resolution and the ticket closes
- THEN the audit history shows the closure attributed to the requester-confirmation path
- AND it is distinguishable from a workflow-terminal closure of the same transition
    
#### Scenario: Manual agent closure of a requester-less ticket is distinguishable
    
- GIVEN a `resolved` ticket with requester user ID NULL
- WHEN an authorized agent closes it manually
- THEN the audit history shows the closure attributed to a manual agent closure
    
#### Scenario: Workflow-terminal closure is distinguishable
    
- GIVEN a `resolved` ticket reaches a `close_ticket` terminal step
- WHEN the workflow closes the ticket
- THEN the audit history shows the closure attributed to the workflow-terminal path
- AND the transition audit events keep the existing workflow actor convention
    
#### Scenario: Every closure path remains audited
    
- GIVEN any of the three closure paths
- WHEN a ticket enters `closed`
- THEN at least one transition audit event records the entry into `closed`
    
### Requirement: Audit History Retrieval

The system MUST expose each ticket's audit events in chronological occurrence order at the storage boundary. The ticket detail presentation MUST merge those events with comments into a newest-first timeline and visually distinguish audit events from agent comments.

#### Scenario: History order

- GIVEN multiple audit events for one ticket
- WHEN the history is retrieved
- THEN the events are returned in the order they occurred

#### Scenario: Audit events in merged presentation timeline

- GIVEN audit events and comments on one ticket
- WHEN the ticket detail timeline is rendered
- THEN all entries are merged newest first
- AND audit events have system styling distinct from comments

### Requirement: Atomic Workflow Audit Sets

Workflow-driven assignment and lifecycle mutations MUST commit with all corresponding audit events in the same atomic operation. A successful assignment of a `new` ticket MUST persist the person, the `new` to `in_progress` transition, and both audit events together. A `close_ticket` step starting from `new` or `in_progress` MUST persist both lifecycle transitions and two transition audit events together. A failed operation MUST persist neither mutations nor their events, and a completed no-op MUST NOT create a false transition event.

#### Scenario: Assignment and transition audits commit together

- GIVEN a `new` ticket reaches a desk-assignment step
- WHEN a person is successfully assigned
- THEN the person assignment and `new` to `in_progress` transition are persisted with both audit events atomically

#### Scenario: Automatic close has two audits

- GIVEN an `in_progress` ticket reaches `close_ticket`
- WHEN the automatic step succeeds
- THEN one audit records `in_progress` to `resolved`
- AND one audit records `resolved` to `closed`
- AND both events commit with both transitions atomically

#### Scenario: Closed no-op has no transition audit

- GIVEN a ticket already in `closed` reaches an applicable terminal completion check
- WHEN the workflow marks the step complete without a state change
- THEN no new transition audit event is appended

### Requirement: Step-Indexed Semantic Audit Events

A semantic category-flow completion or assignment audit MUST persist the sealed zero-based step index of the pinned step that produced it (`audit_events.step_index`, nullable). State-transition audits, non-flow audits, legacy rows, and any event produced outside a pinned run MUST leave the step index NULL. The ticket view MUST correlate a semantic audit event with its pinned step definition or stored form answers only by that exact persisted step index, never by timestamps, row order, or any inferred ordering. A missing or inconsistent step context MUST degrade the timeline item to its safe summary alone without fabricating labels, values, or instructions, and a legacy workflow-step event with NULL step context MUST continue to read as `Completed step`.

#### Scenario: Completion audit carries its sealed step index

- GIVEN a requester submits valid answers for the pinned form at zero-based position 2
- WHEN the completion commits
- THEN its audit event records action `workflow_requester_form`, the authenticated human actor and user ID, and step index 2 exactly as planned

#### Scenario: Legacy event without step context stays readable

- GIVEN a legacy workflow-step audit row with NULL step index predates the column
- WHEN the timeline renders it
- THEN it shows only the summary `Completed step` with no fabricated context

#### Scenario: Inconsistent step context degrades safely

- GIVEN a semantic completion audit whose step index exceeds the pinned snapshot length or whose stored answers are missing
- WHEN the timeline renders the event
- THEN it shows only its safe summary
- AND no field labels, values, or instructions are invented

#### Scenario: Correlation never uses timestamps or order

- GIVEN two audits share an identical timestamp but different persisted step indexes
- WHEN the ticket view builds the timeline
- THEN each semantic event binds only through its own persisted step index

### Requirement: Merged Ticket Activity Timeline

The ticket detail MUST present ONE merged activity timeline, newest-first, containing comments, assignments, every completed category-flow step, and state transitions; a separate ticket-facing responses card for completed steps MUST NOT be rendered. A form completion timeline item MUST render its pinned submitted field labels and values inline as a definition list inside that item, using the immutable pinned labels joined by the persisted step index; answer values remain stored only in `ticket_form_answers` and MUST NOT be duplicated into audit notes or full-text search. A manual completion timeline item MUST render its contextual pinned instruction from the immutable pinned version at the persisted step index, and MUST additionally render the assignee's submitted solution only when that solution is non-empty; solutions remain stored only with their workflow task records and MUST NOT be duplicated into audit note/reason fields or full-text search. Completed form results MUST use the approved restrained treatment consistent with tkt: semantic `dl`/`dt`/`dd` markup with clear per-field grouping separated by hairlines, fixed-width muted labels whose long values wrap inside the entry instead of overflowing it, single-column stacking at narrow viewports such as 390px with no horizontal overflow at any width, plainly visible keyboard focus states and sufficient contrast, HTML escaping of every label and value as plain text, and no technical `workflow` wording anywhere ticket-facing. Human events MUST keep their attributed actor names while automatic events omit actor text, and all existing behavior MUST be preserved: exact timestamps, newest-first ordering, internal-comment visibility, and comments-before-events ordering on same-second ties.

A submitted checkbox value MUST render inside the inline definition list as `✓` for true and `×` for false, each inside a `role="img"` span with the accessible name `Yes` or `No` respectively. Every other field kind MUST keep its literal submitted value. The meaning of the rendered value MUST NOT rely on color alone.
(Previously: the requirement specified the merged timeline, inline definition list rendering, pinned instruction and solution rendering, restrained styling, escaping, and ordering, but did not state the checkbox boolean glyph rendering.)

#### Scenario: Merged timeline replaces the separate responses card

- GIVEN a ticket has comments, an assignment, a completed form step, and a resolved transition
- WHEN the detail page renders
- THEN one newest-first timeline contains all of them
- AND no standalone responses section appears

#### Scenario: Form answers render inline under their own event

- GIVEN a completed form step with pinned fields `Server name` (short text) and checkbox `Urgent`
- WHEN its timeline item renders
- THEN the item shows a definition list pairing those immutable labels with the submitted escaped values
- AND the values appear nowhere in audit notes or search documents

#### Scenario: Manual completion shows its pinned instruction

- GIVEN a completed manual step whose pinned definition carries instructions
- WHEN its timeline item renders
- THEN the item shows that instruction text verbatim and escaped

#### Scenario: Automatic events show no actor text

- GIVEN the timeline contains an automatic resolve transition
- WHEN the item renders
- THEN no actor label or copy containing `workflow` is shown
- AND human-authored items in the same timeline still show their actor names

#### Scenario: Same-second ties keep comments before events

- GIVEN a comment and a step-completion audit share an identical timestamp
- WHEN the timeline orders them
- THEN the comment appears before the event
- AND overall order remains newest-first

#### Scenario: Manual item omits an absent solution

- GIVEN a completed manual task whose completion carried no non-empty solution
- WHEN its timeline item renders
- THEN the item shows the pinned instruction with its actor and timestamp
- AND no empty or placeholder solution block is rendered

#### Scenario: Non-empty manual solution renders escaped and attributed

- GIVEN a completed manual task whose non-empty solution contains markup-like text
- WHEN its timeline item renders for any viewer authorized to see the ticket
- THEN the item shows the pinned instruction followed by the solution as escaped plain text
- AND the item keeps the completing actor's name and the event timestamp
- AND no submitted markup executes or changes page structure

#### Scenario: Manual solution stays out of audit notes and search indexes

- GIVEN a completed manual task whose solution contains a distinctive marker string
- WHEN the persisted audit row and full-text search documents are inspected
- THEN the marker appears in neither the audit note nor reason fields nor any indexed document
- AND the solution exists only in the workflow task record tied to the persisted step index

#### Scenario: Completed form results stay readable at 390px

- GIVEN a completed form step with long submitted values viewed at a 390px-wide viewport
- WHEN its timeline item renders
- THEN each label/value pair stacks in a single column without horizontal overflow
- AND long values wrap inside the entry while labels stay clearly grouped
- AND interactive elements show plainly visible keyboard focus states with sufficient contrast
- AND all rendered labels and values are escaped plain text

#### Scenario: Checkbox boolean values render with accessible glyphs

- GIVEN a completed form whose submitted values include a checkbox answered true and a checkbox answered false
- WHEN its timeline item renders
- THEN the true value renders `✓` inside a `role="img"` span with the accessible name `Yes`
- AND the false value renders `×` inside a `role="img"` span with the accessible name `No`
- AND no submitted markup executes or changes page structure

#### Scenario: Non-checkbox values keep their literal text

- GIVEN a completed form whose submitted values include a short-text field
- WHEN its timeline item renders
- THEN the short-text value renders verbatim as plain escaped text
- AND neither the `✓` nor the `×` glyph replaces it

### Requirement: Downgrade Handoff Audit Events

Every automatic reassignment or unassignment performed by the atomic downgrade handoff MUST record exactly one assignment audit event per affected open ticket, following the existing `Ticket.ApplyUpdate` event convention: action `update` on field `user` with the actual from/to assignee values (to empty when the ticket becomes unassigned). The actor MUST be the initiating admin with the actor user ID set. The reason MUST identify the role downgrade. When a desk was resolved for the ticket, the event MUST carry that `desk_id`; when no desk resolved, `desk_id` MUST be NULL. The step index MUST remain NULL for every handoff event because the handoff occurs outside any pinned workflow run. The role change itself MUST continue to be recorded in `role_changes` with the acting user as today. A failed downgrade MUST persist no handoff audit events.

#### Scenario: Reassignment event fields

- GIVEN a downgrade handoff reassigns an open ticket to an eligible pool member
- WHEN the event is persisted
- THEN it records action `update`, field `user`, the downgraded account as from-value, the replacement as to-value, the initiating admin as actor with actor user ID set, a reason identifying the role downgrade, and the resolved desk id
- AND the event commits inside the same transaction as the reassignment

#### Scenario: Unassignment event fields

- GIVEN a downgrade handoff leaves an open ticket unassigned because no desk resolves or no eligible member exists
- WHEN the event is persisted
- THEN it records action `update`, field `user`, the downgraded account as from-value, an empty to-value, the initiating admin as actor with actor user ID set, a reason identifying the role downgrade, and a NULL desk id when no desk resolved

#### Scenario: Step index NULL outside pinned runs

- GIVEN one or more handoff audit events are persisted during a downgrade
- WHEN the `audit_events` rows are inspected
- THEN every handoff event's step index is NULL
- AND no handoff event is treated as a pinned semantic workflow event by the timeline

### Requirement: Contextual Workflow Claim Assignment Event

A successful pinned `assign_to_desk[claim]` completion MUST append exactly one contextual assignment event in the same atomic operation as the claim cursor movement and any required `new` to `in_progress` transition. Its rendered timeline summary MUST be `Assigned to {person} · {desk}`. A failed or stale claim MUST append no assignment event. This workflow-specific event is reasonless; this requirement MUST NOT alter generic manual reassignment audit reasons, which remain preserved and renderable.

#### Scenario: Successful claim emits one contextual event

- GIVEN active eligible member A claims the current pinned Network desk step
- WHEN the claim commits
- THEN the timeline contains exactly one `Assigned to A · Network` assignment event for that claim
- AND any required state-transition event remains distinct

#### Scenario: Stale claim emits no event

- GIVEN another claimant already advanced the pinned claim cursor
- WHEN a stale claimant submits the completion route
- THEN the claim fails with the typed conflict
- AND no assignment or transition event is appended
