# Proposal: Guide catalog drawer naming

## Intent
Help administrators name Departments, Desks, and Categories without changing catalog behavior.

## Scope
- Describe each catalog drawer's purpose through an entity-specific accessible description.
- Keep persistent Name help for every drawer that states its entity name is globally unique.
- Render the guidance on create and edit routes, in direct full-page responses and HTMX drawer swaps.
- Preserve optional descriptions, hierarchy validation, existing invalid-field behavior, unsaved-change guards, and native fallback.

## Non-goals
- New workflow help, record counts, Area terminology, required descriptions, or changes to global uniqueness rules.

## Rollback
Remove the drawer description and Name-help markup. Existing server validation and drawer routes remain unchanged.
