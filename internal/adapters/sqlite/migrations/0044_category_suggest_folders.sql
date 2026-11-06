-- 0044_category_suggest_folders.sql — the suggestion index ranks folder paths
-- instead of departments (issue #484, S8).
--
-- 0031 indexed each category with five fields: name, keywords, description,
-- desk, department. Departments are leaving the catalog; what places a category
-- now is its folder path. The engine still suggests CATEGORIES only — a folder is
-- never a candidate — and the path is matching context, weighted like the
-- department was (the weakest signal: every category of a folder shares it).
--
-- FTS5 cannot rename or redefine a column, so the main index and its vocabulary
-- view are rebuilt with the fifth column called `folders`: the names of every
-- folder from the top level down to the category's folder, space separated. The
-- trigram index (name, keywords) is untouched.
--
-- Triggers keep the index a projection of the catalog:
--   * categories INSERT / UPDATE(name, keywords, description, desk_id,
--     folder_id) / DELETE — the row itself, with its desk and folder path;
--   * desks UPDATE(name) / DELETE — unchanged in purpose, rewritten for the new
--     column;
--   * folders UPDATE(name, parent_id) — a rename or a move changes the path of
--     every category in that folder's subtree, so all of them are re-indexed.
--     The subtree and the path are recursive CTEs, which SQLite runs inside
--     triggers. A folder delete needs no trigger: the database refuses to delete
--     a folder that still holds anything.
-- The department triggers are dropped with the old index; department renames no
-- longer change what a requester can match.

DROP TRIGGER trg_category_suggest_ai;
DROP TRIGGER trg_category_suggest_au;
DROP TRIGGER trg_category_suggest_desk_au;
DROP TRIGGER trg_category_suggest_desk_ad;
DROP TRIGGER trg_category_suggest_dept_au;
DROP TRIGGER trg_category_suggest_dept_ad;
DROP TABLE category_suggest_vocab;
DROP TABLE category_suggest_fts;

CREATE VIRTUAL TABLE category_suggest_fts USING fts5(
  name,
  keywords,
  description,
  desk,
  folders,
  tokenize='unicode61 remove_diacritics 2');

CREATE VIRTUAL TABLE category_suggest_vocab USING fts5vocab(category_suggest_fts, 'row');

-- New category: index it with its desk and folder path. The trigram half is
-- unchanged from 0031.
CREATE TRIGGER trg_category_suggest_ai AFTER INSERT ON categories BEGIN
  INSERT INTO category_suggest_fts(rowid, name, keywords, description, desk, folders)
  VALUES (NEW.id, NEW.name, NEW.keywords, NEW.description,
    COALESCE((SELECT ds.name FROM desks ds WHERE ds.id = NEW.desk_id), ''),
    COALESCE((WITH RECURSIVE up(id, name, parent_id, depth) AS (
        SELECT f.id, f.name, f.parent_id, 0 FROM folders f WHERE f.id = NEW.folder_id
        UNION ALL
        SELECT f.id, f.name, f.parent_id, up.depth + 1 FROM folders f JOIN up ON f.id = up.parent_id)
      SELECT group_concat(name, ' ' ORDER BY depth DESC) FROM up), ''));
  INSERT INTO category_suggest_trigram(rowid, name, keywords)
  VALUES (NEW.id, NEW.name, NEW.keywords);
END;

CREATE TRIGGER trg_category_suggest_au AFTER UPDATE OF name, keywords, description, desk_id, folder_id ON categories BEGIN
  DELETE FROM category_suggest_fts WHERE rowid = NEW.id;
  INSERT INTO category_suggest_fts(rowid, name, keywords, description, desk, folders)
  VALUES (NEW.id, NEW.name, NEW.keywords, NEW.description,
    COALESCE((SELECT ds.name FROM desks ds WHERE ds.id = NEW.desk_id), ''),
    COALESCE((WITH RECURSIVE up(id, name, parent_id, depth) AS (
        SELECT f.id, f.name, f.parent_id, 0 FROM folders f WHERE f.id = NEW.folder_id
        UNION ALL
        SELECT f.id, f.name, f.parent_id, up.depth + 1 FROM folders f JOIN up ON f.id = up.parent_id)
      SELECT group_concat(name, ' ' ORDER BY depth DESC) FROM up), ''));
  DELETE FROM category_suggest_trigram WHERE rowid = NEW.id;
  INSERT INTO category_suggest_trigram(rowid, name, keywords)
  VALUES (NEW.id, NEW.name, NEW.keywords);
END;

-- trg_category_suggest_ad (category deleted) from 0031 is kept as is: it only
-- deletes by rowid.

CREATE TRIGGER trg_category_suggest_desk_au AFTER UPDATE OF name ON desks BEGIN
  UPDATE category_suggest_fts SET desk = NEW.name
   WHERE rowid IN (SELECT c.id FROM categories c WHERE c.desk_id = NEW.id);
END;

CREATE TRIGGER trg_category_suggest_desk_ad AFTER DELETE ON desks BEGIN
  DELETE FROM category_suggest_fts WHERE rowid IN (SELECT c.id FROM categories c WHERE c.desk_id = OLD.id);
END;

-- Folder renamed or moved: every category in its subtree has a new path.
CREATE TRIGGER trg_category_suggest_folder_au AFTER UPDATE OF name, parent_id ON folders BEGIN
  UPDATE category_suggest_fts
     SET folders = COALESCE((
       SELECT (WITH RECURSIVE up(id, name, parent_id, depth) AS (
           SELECT f.id, f.name, f.parent_id, 0 FROM folders f WHERE f.id = c.folder_id
           UNION ALL
           SELECT f.id, f.name, f.parent_id, up.depth + 1 FROM folders f JOIN up ON f.id = up.parent_id)
         SELECT group_concat(name, ' ' ORDER BY depth DESC) FROM up)
         FROM categories c WHERE c.id = category_suggest_fts.rowid), '')
   WHERE rowid IN (
     WITH RECURSIVE down(id) AS (
       SELECT NEW.id
       UNION
       SELECT f.id FROM folders f JOIN down ON f.parent_id = down.id)
     SELECT c.id FROM categories c WHERE c.folder_id IN (SELECT id FROM down));
END;

-- Backfill every existing category, inside the migration transaction.
INSERT INTO category_suggest_fts(rowid, name, keywords, description, desk, folders)
SELECT c.id, c.name, c.keywords, c.description, COALESCE(ds.name, ''),
  COALESCE((WITH RECURSIVE up(id, name, parent_id, depth) AS (
      SELECT f.id, f.name, f.parent_id, 0 FROM folders f WHERE f.id = c.folder_id
      UNION ALL
      SELECT f.id, f.name, f.parent_id, up.depth + 1 FROM folders f JOIN up ON f.id = up.parent_id)
    SELECT group_concat(name, ' ' ORDER BY depth DESC) FROM up), '')
  FROM categories c LEFT JOIN desks ds ON ds.id = c.desk_id;
