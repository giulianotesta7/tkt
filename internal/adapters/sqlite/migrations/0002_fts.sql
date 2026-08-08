-- 0002_fts.sql — contentless FTS5 index over tickets (design D3, "SQLite
-- Schema"): title, description, and comment bodies in one shared
-- `comments` column; tickets_fts.rowid = tickets.id. Triggers keep the
-- index atomic with every ticket/comment write; contentless avoids
-- duplicated storage (snippet() is not needed — title/description render
-- from `tickets`).
--
-- DEVIATION from design (discovered against the real modernc driver):
--   * The design's reindex statements used INSERT ... SELECT, which
--     silently indexes NOTHING in a contentless FTS5 table — reindexes use
--     VALUES with scalar subqueries instead (verified working).
--   * The design's comment triggers ran the 'delete' command with empty
--     column values, which is a no-op at best and corrupts the index
--     (SQLITE_CORRUPT 267) at worst — comment triggers reindex WITHOUT a
--     delete (append-only comments only grow; the reindex is exact).
--   * The delete/update triggers pass the literal OLD title/description
--     (empty comments) — the shape verified to remove superseded entries
--     cleanly, so "search reflects edits" holds (superseded content MUST
--     NOT remain searchable).

CREATE VIRTUAL TABLE tickets_fts USING fts5(title, description, comments, content='', tokenize='unicode61');

-- New ticket: index its title + description.
CREATE TRIGGER trg_tickets_ai AFTER INSERT ON tickets BEGIN
  INSERT INTO tickets_fts(rowid,title,description,comments)
  VALUES (NEW.id,NEW.title,NEW.description,''); END;

-- Deleted ticket: remove its indexed title/description entries. Comment
-- terms orphaned by the empty comments value are invisible to every search
-- (queries join back against `tickets`, which no longer has the row).
CREATE TRIGGER trg_tickets_ad AFTER DELETE ON tickets BEGIN
  INSERT INTO tickets_fts(tickets_fts,rowid,title,description,comments)
  VALUES ('delete',OLD.id,OLD.title,OLD.description,''); END;

-- Title/description edit: remove the superseded entries, then reindex the
-- row with its current comments (scalar-subquery VALUES — INSERT ... SELECT
-- does not index in a contentless table).
CREATE TRIGGER trg_tickets_au AFTER UPDATE OF title, description ON tickets BEGIN
  INSERT INTO tickets_fts(tickets_fts,rowid,title,description,comments)
  VALUES ('delete',OLD.id,OLD.title,OLD.description,'');
  INSERT INTO tickets_fts(rowid,title,description,comments)
  VALUES (NEW.id,NEW.title,NEW.description,
    (SELECT COALESCE(group_concat(body,' '),'') FROM comments WHERE ticket_id=NEW.id)); END;

-- New comment: reindex the ticket's row so the comment is searchable. No
-- delete command: comments are append-only, so the reindex is exact and an
-- empty-value 'delete' would corrupt the index.
CREATE TRIGGER trg_comments_ai AFTER INSERT ON comments BEGIN
  INSERT INTO tickets_fts(rowid,title,description,comments)
  VALUES (NEW.ticket_id,
    (SELECT title FROM tickets WHERE id=NEW.ticket_id),
    (SELECT description FROM tickets WHERE id=NEW.ticket_id),
    (SELECT COALESCE(group_concat(body,' '),'') FROM comments WHERE ticket_id=NEW.ticket_id)); END;

-- Deleted comment (cascade on ticket delete): reindex what remains; a
-- no-op when the ticket is gone (the ticket trigger already removed it).
CREATE TRIGGER trg_comments_ad AFTER DELETE ON comments BEGIN
  INSERT INTO tickets_fts(rowid,title,description,comments)
  VALUES (OLD.ticket_id,
    (SELECT title FROM tickets WHERE id=OLD.ticket_id),
    (SELECT description FROM tickets WHERE id=OLD.ticket_id),
    (SELECT COALESCE(group_concat(body,' '),'') FROM comments WHERE ticket_id=OLD.ticket_id)); END;
