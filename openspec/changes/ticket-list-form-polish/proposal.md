# Proposal: Ticket list and form polish

## Intent

Make ticket list filtering a single coherent toolbar and make selected-category ticket creation use the existing ticket-detail layout in creation mode, without changing ticket business rules, authorization, persistence, or routes.

## Scope

- Show the actor-scoped result count beneath the Tickets heading.
- Submit every visible list control together, preserve queries across pagination, and keep list state correct after HTMX and full-page navigation.
- Improve ticket table and toolbar responsiveness while keeping all metadata available on mobile.
- Distinguish empty queues from filtered empty results and hide pagination when there is one page.
- Refine the selected-category form copy and route display while keeping native validation, submitted values, and creation redirect behavior.
- Present the selected form in the existing ticket-detail two-column layout: an editable title header, description card, empty timeline, and unboxed Properties rail with an editable native priority control.

## Non-goals

- Changes to ticket search, authorization, persistence, validation, or creation routes.
- New filters, role capabilities, dependencies, global styling changes, or creation-time requester and assignment controls.
- Changes to automatic workflow assignment or a promise that a new ticket will remain unassigned after creation.

## Rollback

Revert the creation-mode detail-layout markup, selected-form layout overrides, catalog hierarchy ink overrides, and their tests without undoing the already-approved ticket-list polish. Existing handlers, search service, routes, and persistence remain unchanged.
