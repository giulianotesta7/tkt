---
name: category-management
status: proposed
change: tkt-mvp
---

# Category Management Specification

## Purpose

Defines the managed category list referenced by tickets. Category names are unique.

## Requirements

### Requirement: Create Category

The system MUST create a category from a name. The name MUST be non-empty and MUST be unique across categories. Duplicate names MUST be rejected.

#### Scenario: Create category

- GIVEN a non-empty name that is not in use
- WHEN the admin creates the category
- THEN the category is stored and listed

#### Scenario: Reject duplicate name

- GIVEN an existing category named "Bugs"
- WHEN the admin creates another category named "Bugs"
- THEN the creation is rejected with a uniqueness error

### Requirement: Update Category

The system MUST allow renaming a category. The uniqueness constraint MUST apply to the new name.

#### Scenario: Rename category

- GIVEN an existing category "Bugs"
- WHEN the admin renames it to "Defects"
- THEN the category is stored with the new name and "Bugs" is free for future use

#### Scenario: Reject rename to duplicate

- GIVEN categories "Bugs" and "Support"
- WHEN the admin renames "Support" to "Bugs"
- THEN the rename is rejected with a uniqueness error

### Requirement: Delete Category

The system MUST NOT delete a category that is referenced by at least one ticket. The system MUST allow deleting an unreferenced category.

#### Scenario: Reject deletion of referenced category

- GIVEN a category used by an existing ticket
- WHEN the admin attempts to delete it
- THEN the deletion is rejected with an integrity error

#### Scenario: Delete unreferenced category

- GIVEN a category with no tickets
- WHEN the admin deletes it
- THEN the category is removed from the list
