# Proposal: Current Task Timeline Projection

> **Outcome first:** For GitHub issue #95, an active workflow step is visible as the first item in the ticket Timeline. A participant who can act receives the existing completion controls; other participants see a compact informational status showing who is handling the step and where updates will appear.

## Intent / Problem

The pending workflow step was rendered as a standalone card above the Timeline. That separated the current state from the ticket's activity stream and made it difficult to understand how the active task related to historical events. The current step is a live projection, not a persisted audit event, so it must appear first without being duplicated in history.

## Scope

- Render the active pending step inside the existing Timeline container.
- Preserve server-side authorization and the existing POST completion route.
- Show authorized manual/form controls in a compact actionable Timeline item.
- Show passive viewers a compact `IN PROGRESS` item with the actual participant name and `Updates will appear here when complete.`
- Keep completed comments and audit events below the active projection in their existing order.
- Remove the unnecessary pending-detail toggle and its persistence/UI surface.
- Preserve the existing internal-comment appearance color setting and its validation.

## Out of scope

- Workflow execution, authorization, endpoints, or persisted history changes.
- Creating a historical audit event for the active projection.
- Changes to canonical specs under `openspec/specs/**`.

## Deliverable

The ticket-management delta updates the pending workflow presentation contract and regression coverage. The appearance-settings change is not part of this work; the existing internal-comment color setting remains unchanged.

## Presentation refinement

All timeline entries use the same actor-first, sentence-case narrative: `Julius completed the task`, `Giuliano submitted request details`, `Julius added an internal comment`, `Giuliano added a public comment`, and `Julius assigned the ticket to Ana`. Assignment entries retain the desk as additional context, while automatic workflow actors remain omitted. Timestamps are metadata only and never repeat the actor. A completed manual task is static visible markup with no disclosure or interaction semantics. Its heading leads with a discrete green check, the actor, and the completion narrative; its definition-list body always includes `TASK` and includes `SOLUTION` only when the stored solution is non-empty, followed by timestamp metadata. Pinned task text and submitted values remain escaped plain text.
