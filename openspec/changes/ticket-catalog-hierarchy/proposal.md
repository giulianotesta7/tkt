# Proposal: Hierarchical ticket catalog

## Intent
Replace the flat category selector at the start of ticket creation with a fixed-depth TKT catalog: Departments contain Desks, and Desks contain ticket Categories. Categories remain the only workflow owners. The existing Desk entity and membership model become the middle level; Area is not part of the final model.

## Scope
- Persist departments, Desk descriptions where needed, and category descriptions with category Desk ownership in SQLite.
- Correct the unpublished hierarchy migration directly so existing categories reuse existing Desks, preserving category IDs and workflow/ticket associations without an Area table or Area migration.
- Provide the requester catalog at `/tickets/new`, searchable across department, Desk, category title, and description; selecting a result opens the normal form with category selected.
- Make `/categories` the single administration screen with Departments, Desks, and Categories columns, contextual drawers, integrated Desk member management, and responsive progressive drill-down.
- Redirect `/desks` to `/categories`; do not maintain a second Desk frontend or duplicate Desk CRUD.
- Preserve ticket fields, validation, published-workflow gating, Desk member operations, workflow Desk routing, and creation transaction semantics.

## Non-goals
Area records or CRUD, generic folders, projects, resources, sectors, arbitrary-depth nesting, category icons, a standalone workflows hierarchy, ticket Desk snapshots, visual regression, or unrelated navigation redesign.

## Decisions
- Department names remain globally unique; Desk names remain globally unique under the existing Desk constraint; category names remain globally unique.
- Departments with child Desks and Desks with child Categories cannot be deleted. Referenced categories remain protected by the existing ticket FK rule.
- Existing categories retain their IDs, descriptions, workflows, tickets, and published associations while being assigned deterministically to an existing General Desk when one exists; no workflows are generated or rewritten.
- Legacy Desks without a department remain valid and are presented under `Unassigned` without changing their stable IDs.
- Search results always include Department and Desk context and use the same catalog/form route rather than a separate workflow.
