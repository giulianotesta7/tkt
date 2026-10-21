-- 0025_ticket_attachments.sql — files attached to tickets and comments (issue
-- #366). Additive only: one new table, no change to an existing row.
--
-- Storage is a BLOB in the database (maintainer decision): an attachment is
-- written in the SAME transaction as the ticket or comment it belongs to, so a
-- refused or failed submission leaves no orphan file, and the instance backup
-- stays one file. Migration 0024 is owned by the email-notification change
-- (#362); this number was agreed with it.
--
-- What each column answers:
--   ticket_id        the ticket the file belongs to. ON DELETE CASCADE: a
--                    file never outlives its ticket.
--   comment_id       the comment carrying the file, or NULL for a file
--                    attached when the ticket was created. ON DELETE CASCADE.
--                    Its visibility (comments.visibility) decides who may see
--                    the file; a creation file is visible to whoever can open
--                    the ticket. The trigger below refuses a comment of
--                    another ticket, so the two keys can never disagree.
--   file_name        the sanitised base name, 1..255 bytes.
--   content_type     the canonical type of the VALIDATED content, never the
--                    one a client claimed.
--   size, data       the file; size is checked against the 10 MB cap and
--                    against the stored bytes themselves.
--   uploader_user_id who attached it (no cascade: history, not ownership).
--   created_at       ISO-8601 UTC TEXT (D7).

CREATE TABLE attachments (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  ticket_id INTEGER NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
  comment_id INTEGER REFERENCES comments(id) ON DELETE CASCADE,
  file_name TEXT NOT NULL CHECK(length(CAST(file_name AS BLOB)) BETWEEN 1 AND 255),
  content_type TEXT NOT NULL CHECK(content_type <> ''),
  size INTEGER NOT NULL CHECK(size BETWEEN 1 AND 10485760),
  data BLOB NOT NULL CHECK(length(data) = size),
  uploader_user_id INTEGER REFERENCES users(id),
  created_at TEXT NOT NULL);

-- Serves the per-ticket list (upload order) and the per-comment grouping.
CREATE INDEX idx_attachments_ticket ON attachments(ticket_id, comment_id, id);

CREATE TRIGGER trg_attachments_comment_ticket
BEFORE INSERT ON attachments
WHEN NEW.comment_id IS NOT NULL
 AND NOT EXISTS (SELECT 1 FROM comments WHERE id = NEW.comment_id AND ticket_id = NEW.ticket_id)
BEGIN
  SELECT RAISE(ABORT, 'attachment comment belongs to another ticket');
END;
