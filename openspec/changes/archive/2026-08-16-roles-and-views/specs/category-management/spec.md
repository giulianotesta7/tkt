# Delta for Category Management

## MODIFIED Requirements

### Requirement: Create Category

The system MUST allow roles `admin` and `root` to create a category from a name. The name MUST be non-empty and MUST be unique across categories. Duplicate names MUST be rejected. Roles `user` and `agent` MUST NOT create categories. (Previously: the actor restriction was unspecified; category management was reachable by any logged-in user.)

#### Scenario: Create category

- GIVEN a non-empty name that is not in use
- WHEN an `admin` or `root` creates the category
- THEN the category is stored and listed

#### Scenario: Reject duplicate name

- GIVEN an existing category named "Bugs"
- WHEN an `admin` creates another category named "Bugs"
- THEN the creation is rejected with a uniqueness error

#### Scenario: Non-manager denied

- GIVEN a `user`- or `agent`-role actor
- WHEN they attempt to create a category
- THEN the request is denied

### Requirement: Update Category

The system MUST allow roles `admin` and `root` to rename a category. The uniqueness constraint MUST apply to the new name.

#### Scenario: Rename category

- GIVEN an existing category "Bugs"
- WHEN an `admin` renames it to "Defects"
- THEN the category is stored with the new name and "Bugs" is free for future use

#### Scenario: Reject rename to duplicate

- GIVEN categories "Bugs" and "Support"
- WHEN an `admin` renames "Support" to "Bugs"
- THEN the rename is rejected with a uniqueness error

### Requirement: Delete Category

The system MUST NOT delete a category that is referenced by at least one ticket. The system MUST allow roles `admin` and `root` to delete an unreferenced category.

#### Scenario: Reject deletion of referenced category

- GIVEN a category used by an existing ticket
- WHEN an `admin` attempts to delete it
- THEN the deletion is rejected with an integrity error

#### Scenario: Delete unreferenced category

- GIVEN a category with no tickets
- WHEN an `admin` deletes it
- THEN the category is removed from the list