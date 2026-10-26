-- 0031_category_suggestions.sql — the local category-suggestion index
-- (issue #416, S4). Suggesting the category from what the requester writes
-- needs three things: the administrator's per-category keywords, one indexed
-- document per category, and a second index that tolerates typos.
--
-- `categories.keywords` is the new field the catalog's category drawer edits:
-- comma separated as the administrator typed it, trimmed, empty by default so
-- every category that already exists reads back with no keywords.
--
-- The main index is CONTENT-FUL on purpose. 0002 explains why the ticket index
-- is contentless — it does not need snippet() and it avoids duplicated storage —
-- and documents the two pitfalls that follow: `INSERT ... SELECT` indexes
-- nothing there, and the 'delete' command with empty column values can corrupt
-- the index. Neither applies here, and neither can be worked around: a rename,
-- a move and a delete all have to remove exactly the superseded row, which a
-- content-ful table does with a plain `DELETE ... WHERE rowid = ?`. The stored
-- text is the indexed text, so staleness is impossible by construction and the
-- duplicate storage is one short row per category.
--
-- `category_suggest_fts` holds the five fields the engine ranks over, in weight
-- order: name, keywords, description, desk, department. Its rowid IS the
-- category id, so a candidate needs no join to be identified and an allowed-id
-- filter is a rowid filter. `tokenize='unicode61 remove_diacritics 2'` folds
-- case and strips diacritics on BOTH sides of a search, so "CONEXIÓN" and
-- "conexion" are the same query — the Go side normalizes the query the same way.
--
-- `category_suggest_trigram` holds only name and keywords, tokenized as
-- trigrams. It is the typo path: a query word that the main vocabulary does not
-- know is looked up here as the OR of its trigrams, and the Go side keeps the
-- indexed word with the best trigram similarity. Trigrams are never ranked by
-- here; the similarity is computed in Go because SQLite has no such function.
-- `remove_diacritics 1` matches what the main index does for these two fields.
--
-- `category_suggest_vocab` is the fts5vocab projection of the main index: the
-- Go adapter asks it whether a query word exists at all before it attempts a
-- typo correction, so a known word is never "corrected" into another one.
--
-- The trigger set is what makes the two indexes a projection of the catalog
-- instead of a cache that drifts:
--   * categories INSERT/UPDATE(name, keywords, description, desk_id)/DELETE —
--     the row itself, including the desk and department names it sits under;
--   * desks UPDATE(name, department_id) and UPDATE(name) of the department
--     above it — a rename re-indexes every category below, because the desk and
--     department names are searchable fields;
--   * desks/departments DELETE — the catalog refuses to delete a desk or
--     department that still holds categories, but a trigger that relied on that
--     refusal would leave stale rows behind the day it changed, so both delete
--     paths clean up anyway.
-- A category moved to another desk is an UPDATE OF desk_id, which re-indexes
-- the row with its new place. Nothing else writes these two tables.
--
-- The backfill at the end indexes every category that existed before this
-- migration. It runs inside the migration transaction, so an instance upgrades
-- with a complete index or not at all.

ALTER TABLE categories ADD COLUMN keywords TEXT NOT NULL DEFAULT '';

CREATE VIRTUAL TABLE category_suggest_fts USING fts5(
  name,
  keywords,
  description,
  desk,
  department,
  tokenize='unicode61 remove_diacritics 2');

CREATE VIRTUAL TABLE category_suggest_trigram USING fts5(
  name,
  keywords,
  tokenize='trigram remove_diacritics 1');

CREATE VIRTUAL TABLE category_suggest_vocab USING fts5vocab(category_suggest_fts, 'row');

-- New category: index it with the desk and department it was created in.
CREATE TRIGGER trg_category_suggest_ai AFTER INSERT ON categories BEGIN
  INSERT INTO category_suggest_fts(rowid, name, keywords, description, desk, department)
  VALUES (NEW.id, NEW.name, NEW.keywords, NEW.description,
    COALESCE((SELECT ds.name FROM desks ds WHERE ds.id = NEW.desk_id), ''),
    COALESCE((SELECT d.name FROM departments d JOIN desks ds ON ds.department_id = d.id WHERE ds.id = NEW.desk_id), ''));
  INSERT INTO category_suggest_trigram(rowid, name, keywords)
  VALUES (NEW.id, NEW.name, NEW.keywords);
END;

-- Edited category: drop the superseded row and index the new one. DELETE ...
-- WHERE rowid works because the table is content-ful, so no old term survives.
CREATE TRIGGER trg_category_suggest_au AFTER UPDATE OF name, keywords, description, desk_id ON categories BEGIN
  DELETE FROM category_suggest_fts WHERE rowid = NEW.id;
  INSERT INTO category_suggest_fts(rowid, name, keywords, description, desk, department)
  VALUES (NEW.id, NEW.name, NEW.keywords, NEW.description,
    COALESCE((SELECT ds.name FROM desks ds WHERE ds.id = NEW.desk_id), ''),
    COALESCE((SELECT d.name FROM departments d JOIN desks ds ON ds.department_id = d.id WHERE ds.id = NEW.desk_id), ''));
  DELETE FROM category_suggest_trigram WHERE rowid = NEW.id;
  INSERT INTO category_suggest_trigram(rowid, name, keywords)
  VALUES (NEW.id, NEW.name, NEW.keywords);
END;

-- Deleted category: nothing of it may stay searchable.
CREATE TRIGGER trg_category_suggest_ad AFTER DELETE ON categories BEGIN
  DELETE FROM category_suggest_fts WHERE rowid = OLD.id;
  DELETE FROM category_suggest_trigram WHERE rowid = OLD.id;
END;

-- Desk renamed or moved to another department: every category under it carries
-- that name, so every one of them is re-indexed.
CREATE TRIGGER trg_category_suggest_desk_au AFTER UPDATE OF name, department_id ON desks BEGIN
  DELETE FROM category_suggest_fts WHERE rowid IN (SELECT c.id FROM categories c WHERE c.desk_id = NEW.id);
  INSERT INTO category_suggest_fts(rowid, name, keywords, description, desk, department)
    SELECT c.id, c.name, c.keywords, c.description, NEW.name,
           COALESCE((SELECT d.name FROM departments d WHERE d.id = NEW.department_id), '')
      FROM categories c WHERE c.desk_id = NEW.id;
END;

-- Desk deleted: drop the rows it owned, so no orphan can be matched later.
CREATE TRIGGER trg_category_suggest_desk_ad AFTER DELETE ON desks BEGIN
  DELETE FROM category_suggest_fts WHERE rowid IN (SELECT c.id FROM categories c WHERE c.desk_id = OLD.id);
END;

-- Department renamed: every category under every desk of that department is
-- re-indexed with the new name.
CREATE TRIGGER trg_category_suggest_dept_au AFTER UPDATE OF name ON departments BEGIN
  DELETE FROM category_suggest_fts WHERE rowid IN (
    SELECT c.id FROM categories c JOIN desks ds ON ds.id = c.desk_id WHERE ds.department_id = NEW.id);
  INSERT INTO category_suggest_fts(rowid, name, keywords, description, desk, department)
    SELECT c.id, c.name, c.keywords, c.description, ds.name, NEW.name
      FROM categories c JOIN desks ds ON ds.id = c.desk_id WHERE ds.department_id = NEW.id;
END;

-- Department deleted: same cleanup, so a stale department name cannot rank a
-- category by a place that no longer exists.
CREATE TRIGGER trg_category_suggest_dept_ad AFTER DELETE ON departments BEGIN
  DELETE FROM category_suggest_fts WHERE rowid IN (
    SELECT c.id FROM categories c JOIN desks ds ON ds.id = c.desk_id WHERE ds.department_id = OLD.id);
END;

-- Backfill: every category that already existed is indexed with the desk and
-- department it sits in. A missing place (a desk without a department) is
-- indexed as '' here and in every trigger, so the stored row never depends on
-- which path wrote it.
INSERT INTO category_suggest_fts(rowid, name, keywords, description, desk, department)
  SELECT c.id, c.name, c.keywords, c.description, COALESCE(ds.name, ''), COALESCE(d.name, '')
    FROM categories c
    LEFT JOIN desks ds ON ds.id = c.desk_id
    LEFT JOIN departments d ON d.id = ds.department_id;

INSERT INTO category_suggest_trigram(rowid, name, keywords)
  SELECT c.id, c.name, c.keywords FROM categories c;
