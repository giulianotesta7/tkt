-- 0045_drop_departments.sql — departments leave the catalog (issue #484).
--
-- Migration 0043 copied every department and desk into the folder tree, and
-- nothing reads departments since: the catalog is folders, and a desk is a team
-- a category names as responsible. This migration removes what is left:
--
--   * the empty folders 0043 copied from the seeded "General" department and
--     its "General" desk (S17). Migration 0011 seeds that pair on every
--     installation, so without this a fresh instance starts with two empty
--     folders flagged under Needs attention. 0043 recorded those copies by id
--     in folder_seed_copies, so a folder an administrator created is never
--     mistaken for one, whatever its name, place, description or date. A copy
--     goes only while it still has its seeded name and holds no folder and no
--     category: first the child, then the top-level copy it may leave empty.
--     Anything an administrator put in them or renamed stays. The General DESK
--     stays: it is a team and categories may name it. folder_seed_copies has
--     done its job and is dropped;
--   * desks.department_id, its index, and the departments table. DROP COLUMN
--     works in place here because nothing but the dropped index refers to the
--     column, so no desk row, membership, category or ticket is rewritten.
--
-- The whole migration runs in one transaction: an instance upgrades completely
-- or not at all.

DELETE FROM folders
 WHERE id IN (SELECT folder_id FROM folder_seed_copies)
   AND parent_id IS NOT NULL
   AND name = 'General'
   AND NOT EXISTS (SELECT 1 FROM folders c WHERE c.parent_id = folders.id)
   AND NOT EXISTS (SELECT 1 FROM categories c WHERE c.folder_id = folders.id);

DELETE FROM folders
 WHERE id IN (SELECT folder_id FROM folder_seed_copies)
   AND parent_id IS NULL
   AND name = 'General'
   AND NOT EXISTS (SELECT 1 FROM folders c WHERE c.parent_id = folders.id)
   AND NOT EXISTS (SELECT 1 FROM categories c WHERE c.folder_id = folders.id);

DROP TABLE folder_seed_copies;
DROP INDEX idx_desks_department;
ALTER TABLE desks DROP COLUMN department_id;
DROP TABLE departments;
