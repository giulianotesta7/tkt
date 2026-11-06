-- 0047: desk responsibility belongs to workflows, not suggestion context (#491).
-- Keep the five-column FTS schema during the staged category migration. Clearing
-- the content-ful desk column updates its postings and the live fts5vocab view in
-- the same migration transaction. Name/keyword trigram rows are unchanged.
-- Category rows, foreign keys, non-suggestion indexes, category deletion and
-- folder rename/move triggers are deliberately untouched.
DROP TRIGGER trg_category_suggest_ai;
DROP TRIGGER trg_category_suggest_au;
DROP TRIGGER trg_category_suggest_desk_au;
DROP TRIGGER trg_category_suggest_desk_ad;

UPDATE category_suggest_fts SET desk = '';

CREATE TRIGGER trg_category_suggest_ai AFTER INSERT ON categories BEGIN
  INSERT INTO category_suggest_fts(rowid, name, keywords, description, desk, folders)
  VALUES (NEW.id, NEW.name, NEW.keywords, NEW.description, '',
    COALESCE((WITH RECURSIVE up(id, name, parent_id, depth) AS (
        SELECT f.id, f.name, f.parent_id, 0 FROM folders f WHERE f.id = NEW.folder_id
        UNION ALL
        SELECT f.id, f.name, f.parent_id, up.depth + 1 FROM folders f JOIN up ON f.id = up.parent_id)
      SELECT group_concat(name, ' ' ORDER BY depth DESC) FROM up), ''));
  INSERT INTO category_suggest_trigram(rowid, name, keywords)
  VALUES (NEW.id, NEW.name, NEW.keywords);
END;

CREATE TRIGGER trg_category_suggest_au AFTER UPDATE OF name, keywords, description, folder_id ON categories BEGIN
  DELETE FROM category_suggest_fts WHERE rowid = NEW.id;
  INSERT INTO category_suggest_fts(rowid, name, keywords, description, desk, folders)
  VALUES (NEW.id, NEW.name, NEW.keywords, NEW.description, '',
    COALESCE((WITH RECURSIVE up(id, name, parent_id, depth) AS (
        SELECT f.id, f.name, f.parent_id, 0 FROM folders f WHERE f.id = NEW.folder_id
        UNION ALL
        SELECT f.id, f.name, f.parent_id, up.depth + 1 FROM folders f JOIN up ON f.id = up.parent_id)
      SELECT group_concat(name, ' ' ORDER BY depth DESC) FROM up), ''));
  DELETE FROM category_suggest_trigram WHERE rowid = NEW.id;
  INSERT INTO category_suggest_trigram(rowid, name, keywords)
  VALUES (NEW.id, NEW.name, NEW.keywords);
END;
