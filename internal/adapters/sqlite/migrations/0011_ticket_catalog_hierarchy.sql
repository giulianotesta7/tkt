-- 0011_ticket_catalog_hierarchy.sql — Departments -> existing Desks -> Categories.
-- This unpublished migration is corrected in place: it never creates an Area
-- table and preserves all existing Desk, membership, workflow, and ticket IDs.

CREATE TABLE departments (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL UNIQUE CHECK(length(trim(name)) > 0),
  description TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL
);

ALTER TABLE desks ADD COLUMN description TEXT NOT NULL DEFAULT '';
ALTER TABLE desks ADD COLUMN department_id INTEGER REFERENCES departments(id);

INSERT INTO departments (name, description, created_at)
VALUES ('General', 'General ticket requests', strftime('%Y-%m-%dT%H:%M:%SZ', 'now'));

-- The existing General Desk is the deterministic compatibility owner. Empty
-- installations receive one compatibility Desk so legacy category creation and
-- the seeded lifecycle remain valid; no existing Desk ID is rewritten.
INSERT INTO desks (name, department_id, created_at)
SELECT 'General', id, strftime('%Y-%m-%dT%H:%M:%SZ', 'now')
FROM departments WHERE name='General'
  AND NOT EXISTS (SELECT 1 FROM desks WHERE lower(name)='general');

UPDATE desks
SET department_id=(SELECT id FROM departments WHERE name='General')
WHERE lower(name)='general' AND department_id IS NULL;

ALTER TABLE categories ADD COLUMN description TEXT NOT NULL DEFAULT '';
ALTER TABLE categories ADD COLUMN desk_id INTEGER REFERENCES desks(id);

UPDATE categories
SET description = CASE WHEN length(trim(description)) = 0 THEN name ELSE description END,
    desk_id = (SELECT id FROM desks WHERE lower(name)='general' ORDER BY id LIMIT 1)
WHERE desk_id IS NULL;

CREATE INDEX idx_desks_department ON desks(department_id, id);
CREATE INDEX idx_categories_desk ON categories(desk_id, id);

-- Categories are valid only inside the fixed Department -> Desk hierarchy. The
-- trigger is used because SQLite cannot add a NOT NULL FK column in place.
CREATE TRIGGER categories_desk_required_insert
BEFORE INSERT ON categories
WHEN NEW.desk_id IS NULL
BEGIN
  SELECT RAISE(ABORT, 'category desk is required');
END;
CREATE TRIGGER categories_desk_required_update
BEFORE UPDATE OF desk_id ON categories
WHEN NEW.desk_id IS NULL
BEGIN
  SELECT RAISE(ABORT, 'category desk is required');
END;
