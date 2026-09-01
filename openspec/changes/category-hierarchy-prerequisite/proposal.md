# Proposal: Category hierarchy prerequisite

## Intent

TKT currently stores categories as a flat, name-only list. Issue #103 authorizes a three-level requester catalog: Departments contain Areas, and Areas contain Categories. This change adds the domain and persistence prerequisite for that catalog while preserving existing ticket, workflow, publication, and authorization behavior.

The Categories drawer polish from issue #98 is a separate follow-up change. This prerequisite MUST NOT implement or redefine the drawer.

## Scope

### In scope

- Persist Departments with unique names and descriptions.
- Persist Areas with names and descriptions, each belonging to exactly one Department.
- Enforce Area-name uniqueness within its Department.
- Persist Categories with required names and descriptions, each belonging to exactly one Area.
- Preserve global category-name uniqueness.
- Store only `area_id` on Category. Derive Department through Area; never duplicate `department_id` on Category.
- Migrate existing categories deterministically under Department `General` and Area `General`.
- Reject deletion of Departments with Areas and Areas with Categories without cascading.
- Keep existing ticket, workflow, publication, and authorization behavior unchanged.
- Allow Root and Admin to manage the hierarchy through the existing server-side category-management capability. Agent and User remain consumers only.
- Represent the requester hierarchy as Departments → Areas → Categories, with title and description only and no configurable icons.

### Out of scope

- The Categories creation drawer, drawer JavaScript/CSS, or the issue #98 presentation polish.
- Changes to workflow definitions, publication lifecycle, ticket mutation, or role capabilities.
- Configurable catalog icons.
- Arbitrary-depth nesting, generic folders, or a separate workflow administration surface.

## Acceptance criteria

1. The database contains Departments, Areas, and Categories with the relationships and uniqueness rules above.
2. `categories` stores `area_id` and does not store `department_id`.
3. Existing category IDs, names, timestamps, ticket references, workflow references, and publication state survive migration.
4. Existing categories resolve through `General` Department → `General` Area.
5. Parent deletion rejects non-empty parents and never cascades children.
6. Admin/root mutations are server-authorized; agent/user mutations are denied.
7. Existing workflow availability and ticket behavior remain unchanged.
8. The prerequisite change is independently testable and can be merged before the Categories drawer change.

## Migration and rollback

Use a forward-only, transactional migration following the existing SQLite migration conventions. Preserve existing rows and foreign-key references. Seed exactly one `General` Department and one `General` Area, then assign all existing categories to that Area and give migrated categories an empty description where no prior description exists.

There is no down migration. If release rollback is required, restore the prior application image and database backup using the repository rollback procedure. The migration must fail closed without leaving partial state.

## Risks

- Rebuilding the categories table can affect foreign keys. The migration must be tested against tickets and workflow rows and preserve their references.
- New hierarchy data must not accidentally alter workflow availability. Keep category availability based on the existing published-workflow rule only.
- The later drawer change must consume this model rather than introduce duplicate Department or Area logic.

## Traceability

- Governing issue: #103 (`status:approved`, `type:feature`, `area:tickets`, `area:categories-workflows`).
- Follow-up UI issue: #98 (`status:approved`, `type:feature`, `area:categories-workflows`).
- Canonical contracts: `category-management`, `category-workflows`, `role-authorization`, `ticket-management`, and `desk-management`.
