# Delta for Category Management

## ADDED Requirements

### Requirement: Catalog drawer guidance
Department, Desk, and Category create and edit drawers MUST each expose a concise, entity-specific purpose description through the dialog's `aria-describedby`. Each description MUST add context beyond the drawer title.

Each drawer's Name field MUST retain persistent help, referenced through `aria-describedby`, that states the actual rule for that entity: Department, Desk, and Category names are globally unique. The guidance MUST render on direct full-page requests and HTMX drawer responses without changing existing invalid-field mappings, focus behavior, hierarchy rules, optional Description fields, or unsaved-change guards.

#### Scenario: Create with entity-specific guidance
- GIVEN an authorized actor opens a Department, Desk, or Category create drawer directly or through HTMX
- WHEN the drawer renders
- THEN the dialog references that entity's purpose description
- AND the Name field references help that states the entity's global uniqueness rule
- AND existing required hierarchy controls and optional Description field remain unchanged

#### Scenario: Edit with entity-specific guidance
- GIVEN an authorized actor opens a Department, Desk, or Category edit drawer directly or through HTMX
- WHEN the drawer renders
- THEN the dialog references that entity's purpose description
- AND the Name field references help that states the entity's global uniqueness rule
- AND existing validation, focus, and unsaved-change behavior remains unchanged

#### Scenario: Retain Category workflow semantics
- GIVEN an authorized actor opens a Category create or edit drawer
- WHEN the drawer renders
- THEN its description explains that categories group requests that follow the same workflow
- AND its Name help states that Category names are globally unique
