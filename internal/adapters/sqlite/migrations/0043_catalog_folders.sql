-- 0043_catalog_folders.sql — a folder tree of categories (issue #484).
--
-- The catalog stops being the fixed Department -> Desk -> Category hierarchy.
-- Folders nest as deep as an administrator wants (parent_id, NULL = top level)
-- and a category sits in at most one folder (categories.folder_id, NULL = top
-- level). Desks stay as teams and keep owning categories through desk_id; this
-- migration is additive, so departments and desks.department_id are still here
-- and a later migration removes them once nothing reads them.
--
-- Invariants the database owns, not only the application:
--   * a folder's name is unique among its siblings, case-insensitively
--     (idx_folders_sibling_name, created only after the backfill below);
--   * a folder never becomes its own ancestor (trg_folders_no_cycle). The guard
--     walks the new parent's ancestors with a recursive CTE, which SQLite runs
--     inside triggers, so a move that would close a loop aborts and changes
--     nothing whichever path wrote it;
--   * a folder that still holds a folder or a category cannot be deleted: both
--     references are plain foreign keys without ON DELETE, so the delete fails.
--
-- Backfill (option A, lossless): every department becomes a top-level folder,
-- every desk becomes a folder inside its department's folder (a legacy desk
-- with no department becomes a top-level folder), and every category moves into
-- its desk's folder. Category, desk, membership, workflow, SLA and ticket rows
-- are not rewritten. Folder ids are assigned explicitly through a mapping table
-- so no step has to find a folder again by name.
--
-- Sibling names are compared through name_key, the name case-folded in Go
-- (domain.FolderNameKey): SQLite's NOCASE folds ASCII only, and "ÁREA" and
-- "área" must be the same name. SQL cannot compute that key, so this script
-- copies the catalog and leaves name_key empty, and a Go step that runs right
-- after it IN THE SAME TRANSACTION (migrate_0043.go) computes every key,
-- resolves collisions in the existing data, creates idx_folders_sibling_name
-- and drops the mapping table. Existing names were unique only case-sensitively
-- and departments and legacy desks now share the top level, so siblings can
-- collide ("IT" and "it"); the upgrade must not abort on such data, so the
-- later row (by folder id) gets " (<department or desk id>)" appended until
-- its key is free among its siblings.

CREATE TABLE folders (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  parent_id INTEGER REFERENCES folders(id),
  name TEXT NOT NULL CHECK(length(trim(name)) > 0),
  name_key TEXT NOT NULL DEFAULT '',
  description TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  CHECK(parent_id IS NULL OR parent_id <> id)
);

CREATE INDEX idx_folders_parent ON folders(parent_id, id);

ALTER TABLE categories ADD COLUMN folder_id INTEGER REFERENCES folders(id);

CREATE INDEX idx_categories_folder ON categories(folder_id, id);

CREATE TRIGGER trg_folders_no_cycle
BEFORE UPDATE OF parent_id ON folders
WHEN NEW.parent_id IS NOT NULL AND (
  NEW.parent_id = NEW.id OR NEW.id IN (
    WITH RECURSIVE up(id) AS (
      SELECT NEW.parent_id
      UNION
      SELECT f.parent_id FROM folders f JOIN up ON f.id = up.id WHERE f.parent_id IS NOT NULL)
    SELECT id FROM up))
BEGIN
  SELECT RAISE(ABORT, 'folder cycle');
END;

CREATE TABLE folder_backfill_map (
  folder_id INTEGER PRIMARY KEY,
  kind TEXT NOT NULL,
  source_id INTEGER NOT NULL
);

INSERT INTO folder_backfill_map (folder_id, kind, source_id)
SELECT row_number() OVER (ORDER BY id), 'department', id FROM departments;

INSERT INTO folder_backfill_map (folder_id, kind, source_id)
SELECT (SELECT count(*) FROM departments) + row_number() OVER (ORDER BY id), 'desk', id FROM desks;

INSERT INTO folders (id, parent_id, name, description, created_at)
SELECT m.folder_id, NULL, d.name, d.description, d.created_at
  FROM folder_backfill_map m JOIN departments d ON d.id = m.source_id
 WHERE m.kind = 'department';

INSERT INTO folders (id, parent_id, name, description, created_at)
SELECT m.folder_id, pm.folder_id, ds.name, ds.description, ds.created_at
  FROM folder_backfill_map m
  JOIN desks ds ON ds.id = m.source_id
  LEFT JOIN folder_backfill_map pm ON pm.kind = 'department' AND pm.source_id = ds.department_id
 WHERE m.kind = 'desk';

UPDATE categories
   SET folder_id = (SELECT m.folder_id FROM folder_backfill_map m WHERE m.kind = 'desk' AND m.source_id = categories.desk_id);

-- The folders copied from the seeded "General" department and its "General"
-- desk, recorded by id while the copy still knows its source. Migration 0045
-- removes them when they are left empty (S17); recording the origin here means
-- it never has to guess it from a name an administrator could reuse. Folder
-- ids are AUTOINCREMENT, so a recorded id never comes back as another folder.
CREATE TABLE folder_seed_copies (
  folder_id INTEGER PRIMARY KEY
);

INSERT INTO folder_seed_copies (folder_id)
SELECT m.folder_id
  FROM folder_backfill_map m JOIN departments d ON d.id = m.source_id
 WHERE m.kind = 'department' AND d.name = 'General'
UNION
SELECT m.folder_id
  FROM folder_backfill_map m
  JOIN desks ds ON ds.id = m.source_id
  JOIN departments d ON d.id = ds.department_id
 WHERE m.kind = 'desk' AND d.name = 'General' AND lower(ds.name) = 'general';

-- folder_backfill_map and idx_folders_sibling_name: see migrate_0043.go.
