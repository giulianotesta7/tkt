-- Hierarchical ticket catalog: Departments -> Areas -> Categories.
-- Existing categories are preserved under the deterministic General -> General
-- compatibility branch; their IDs remain stable for ticket/workflow FKs.
CREATE TABLE departments (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL UNIQUE CHECK(length(trim(name)) > 0),
  description TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL
);

CREATE TABLE areas (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  department_id INTEGER NOT NULL REFERENCES departments(id),
  name TEXT NOT NULL CHECK(length(trim(name)) > 0),
  description TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  UNIQUE(department_id, name)
);
CREATE INDEX idx_areas_department ON areas(department_id, id);

INSERT INTO departments (name, description, created_at)
VALUES ('General', 'General ticket requests', strftime('%Y-%m-%dT%H:%M:%SZ', 'now'));
INSERT INTO areas (department_id, name, description, created_at)
SELECT id, 'General', 'General ticket requests', strftime('%Y-%m-%dT%H:%M:%SZ', 'now')
FROM departments WHERE name = 'General';

ALTER TABLE categories ADD COLUMN description TEXT NOT NULL DEFAULT '';
ALTER TABLE categories ADD COLUMN area_id INTEGER REFERENCES areas(id);

UPDATE categories
SET area_id = (SELECT id FROM areas WHERE name = 'General'
               AND department_id = (SELECT id FROM departments WHERE name = 'General'))
WHERE area_id IS NULL;

CREATE INDEX idx_categories_area ON categories(area_id, id);

CREATE TRIGGER categories_area_required_insert
BEFORE INSERT ON categories
WHEN NEW.area_id IS NULL
BEGIN
  SELECT RAISE(ABORT, 'category area is required');
END;

CREATE TRIGGER categories_area_required_update
BEFORE UPDATE OF area_id ON categories
WHEN NEW.area_id IS NULL
BEGIN
  SELECT RAISE(ABORT, 'category area is required');
END;
