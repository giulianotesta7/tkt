-- 0048: categories stop naming a desk; workflow steps own responsibility (#491).
-- The legacy category -> desk pairs are archived, with the desk's name at
-- migration time, before the column goes. The archive deliberately has no
-- foreign keys so it survives the later deletion of a category or a desk.
CREATE TABLE category_desk_archive (
  category_id INTEGER PRIMARY KEY,
  desk_id     INTEGER NOT NULL,
  desk_name   TEXT NOT NULL,
  archived_at TEXT NOT NULL
);

INSERT INTO category_desk_archive (category_id, desk_id, desk_name, archived_at)
SELECT c.id, c.desk_id, COALESCE(d.name, ''), strftime('%Y-%m-%dT%H:%M:%SZ', 'now')
FROM categories c LEFT JOIN desks d ON d.id = c.desk_id
WHERE c.desk_id IS NOT NULL;

-- The index is the only object that blocks the drop; the column-level
-- REFERENCES goes with the column.
DROP INDEX idx_categories_desk;
ALTER TABLE categories DROP COLUMN desk_id;
