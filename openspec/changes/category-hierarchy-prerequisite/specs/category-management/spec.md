# Delta for Category Management

This change implements the domain prerequisite for issue #103. The Categories drawer and issue #98 presentation polish are explicitly out of scope and MUST be implemented only after this change is merged.

## ADDED Requirements

### Requirement: Catalog hierarchy

The managed catalog MUST contain exactly three levels: Departments, Areas, and Categories. A Department MUST have a non-empty unique name and a description. An Area MUST belong to exactly one Department, MUST have a non-empty name and a description, and its name MUST be unique within its Department. A Category MUST belong to exactly one Area, MUST have a non-empty name and description, and its name MUST remain unique across all categories. A Category MUST store only `area_id`; its Department MUST be derived through its Area. The system MUST NOT store `department_id` on Category. Catalog items MUST expose only a title and description and MUST NOT support configurable icons.

#### Scenario: Create a Department

- GIVEN an `admin` or `root` actor
- WHEN they create a Department with a unique non-empty name and a description
- THEN the Department is stored and available to the catalog

#### Scenario: Reject a duplicate Department name

- GIVEN an existing Department named `General`
- WHEN an `admin` creates another Department named `General`
- THEN creation is rejected with a uniqueness error

#### Scenario: Create an Area within a Department

- GIVEN an existing Department
- WHEN an `admin` or `root` creates an Area with a non-empty name and description
- THEN the Area belongs to exactly that Department

#### Scenario: Enforce Area-name scope

- GIVEN Department `General` contains an Area named `Software`
- WHEN an `admin` creates another `Software` Area in `General`
- THEN creation is rejected with a uniqueness error
- AND when the actor creates `Software` in another Department, creation succeeds

#### Scenario: Create a Category within an Area

- GIVEN an existing Area
- WHEN an `admin` or `root` creates a Category with a non-empty globally unique name and description
- THEN the Category belongs to that Area
- AND its Department resolves through the Area

#### Scenario: Preserve global category uniqueness

- GIVEN an existing Category named `Bugs`
- WHEN an `admin` creates another Category named `Bugs` in any Area
- THEN creation is rejected with a uniqueness error

### Requirement: Safe hierarchy deletion

The system MUST allow `admin` and `root` to delete an empty Department or Area. It MUST reject deletion of a Department that has Areas and an Area that has Categories. Parent deletion MUST NOT cascade to children or orphan them. The existing rule that prevents deleting a category referenced by a ticket MUST remain unchanged.

#### Scenario: Reject non-empty Department deletion

- GIVEN a Department containing an Area
- WHEN an `admin` attempts to delete the Department
- THEN deletion is rejected with an integrity error
- AND the Department and Area remain

#### Scenario: Reject non-empty Area deletion

- GIVEN an Area containing a Category
- WHEN an `admin` attempts to delete the Area
- THEN deletion is rejected with an integrity error
- AND the Area and Category remain

#### Scenario: Delete empty parents without cascading

- GIVEN an empty Area and an empty Department
- WHEN an authorized actor deletes them
- THEN only the explicitly selected empty node is removed
- AND no other hierarchy row is removed

### Requirement: Deterministic existing-category migration

When hierarchy persistence is introduced, the migration MUST create exactly one Department named `General` and exactly one Area named `General` belonging to it. Every existing category MUST be assigned to that Area. Existing category IDs, names, and timestamps MUST be preserved. Migrated descriptions MUST be non-null and default to the empty string when no prior description exists. Existing ticket and workflow references MUST continue to resolve to the same categories, and no ticket, workflow, or publication row may be changed. The migration MUST be transactional and MUST fail closed without partial state.

#### Scenario: Migrate existing categories under General

- GIVEN categories, tickets, and workflow rows from before hierarchy support
- WHEN the migration completes
- THEN all categories belong to `General` Area under `General` Department
- AND category IDs, names, and timestamps are unchanged
- AND ticket and workflow references still resolve to those categories

#### Scenario: Migration failure is atomic

- GIVEN a hierarchy migration step cannot complete
- WHEN the migration runs
- THEN the migration aborts
- AND the database has no partial hierarchy state

### Requirement: Hierarchy authorization and consumption

`admin` and `root` MUST manage Departments, Areas, and Categories through the existing `CapManageCategories` server-side capability. `agent` and `user` MUST NOT mutate any hierarchy level and MUST only consume the resulting catalog according to the existing authorization contract. The requester-facing hierarchy MUST be Departments → Areas → Categories. Adding hierarchy MUST NOT change ticket mutation authorization, workflow authorization, publication lifecycle, or category availability semantics.

#### Scenario: Admin manages hierarchy

- GIVEN an authorized `admin` or `root`
- WHEN they create, update, or delete an allowed hierarchy item
- THEN the existing category-management authorization permits the operation

#### Scenario: Agent and User cannot mutate hierarchy

- GIVEN an `agent` or `user`
- WHEN they attempt a hierarchy mutation directly
- THEN the server denies the request
- AND no hierarchy row changes

#### Scenario: Requester consumes the hierarchy top-down

- GIVEN a requester chooses a category for a ticket
- WHEN the catalog is presented
- THEN choices are narrowed in the order Department, Area, Category
- AND each item shows only its title and description

#### Scenario: Existing workflow behavior remains unchanged

- GIVEN categories with existing drafts, published workflow versions, or no workflow
- WHEN hierarchy support is enabled
- THEN availability still depends only on the existing published-workflow rule
- AND workflow and ticket authorization remain unchanged
