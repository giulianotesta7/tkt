# Delta for Audit Log

This narrow delta adds only the ticket-facing presentation of already-persisted audit events. Audit storage, actor attribution, append-only semantics, and correlation keys remain unchanged.

## ADDED Requirements

### Requirement: Actor-First Timeline Presentation

The ticket detail presentation MUST use one actor-first, sentence-case narrative pattern for every existing event type and comment. Human-authored entries MUST lead with the attributed actor and then the contextual content: a manual completion reads `{actor} completed the task`, a requester form reads `{actor} submitted request details`, an assignee form reads `{actor} submitted work details`, a comment reads `{actor} added a public comment` or `{actor} added an internal comment`, and an assignment reads `{actor} assigned the ticket to {person}` while retaining the assigned desk as additional context. Lifecycle and field-update events MUST use the same actor-first sentence-case pattern. Automatic events MUST omit actor text entirely, including the actor value `workflow`, with no dangling separator. Timestamp metadata MUST contain only the exact event timestamp and MUST NOT repeat the actor. Existing audit rows, authorization, visibility, newest-first ordering, comments-before-events ties, and search/index storage MUST remain unchanged.

A completed manual task with compatible pinned context MUST render as static visible markup with no `details`, `summary`, button, expansion control, open state, or interactive cursor semantics. Its heading MUST contain a discrete green check and actor-first sentence-case completion copy. Its definition-list body MUST always include `TASK`, MUST include `SOLUTION` only when the stored solution is non-empty, and MUST be followed by timestamp metadata. Pinned instructions, solutions, comments, labels, and form values MUST remain escaped plain text.

#### Scenario: Existing event types share actor-first presentation

- GIVEN a timeline containing comments, transitions, updates, workflow assignments, manual completions, requester form completions, and assignee form completions
- WHEN the ticket detail renders
- THEN human entries lead with their attributed actor and sentence-case contextual content
- AND comments use `added a public comment` or `added an internal comment`
- AND manual and form entries use their exact completion narratives
- AND assignments retain both their person target and desk context
- AND automatic entries omit actor text
- AND timestamps do not repeat actors

#### Scenario: Manual completion uses static definition-list markup

- GIVEN a completed manual event with compatible pinned task context
- WHEN its timeline item renders
- THEN it is static visible markup with a discrete green check and actor-first completion copy
- AND it contains no `details`, `summary`, button, expansion control, open state, or interactive cursor semantics
- AND its body is a definition list containing `TASK`
- AND `SOLUTION` appears only for a non-empty stored solution
- AND timestamp metadata follows the definition-list rows without repeating the actor
- AND the pinned instruction and solution are escaped as plain text

#### Scenario: Missing context remains safe

- GIVEN a legacy or inconsistent manual event without compatible pinned context
- WHEN the timeline renders
- THEN only the safe actor-first summary is shown
- AND no task or solution content is fabricated
