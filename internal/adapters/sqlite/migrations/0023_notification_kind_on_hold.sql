-- 0023_notification_kind_on_hold.sql — the on_hold notification kind (issue
-- #352): the requester learns their ticket waits for their reply.
--
-- 0021 pins notifications.kind with a CHECK over the closed vocabulary, and
-- SQLite cannot alter a CHECK in place, so adding one kind rebuilds the table.
-- The rebuild changes ONLY that list: the columns, their types, defaults,
-- foreign keys and cascades, and idx_notifications_recipient are copied from
-- 0021 unchanged, and every existing row is copied with its id, so a stored
-- notification reads back exactly as before (migration_0023_test.go compares
-- the schema and the rows before and after).
--
-- Why the sqlite_sequence step: AUTOINCREMENT promises a deleted id is never
-- reused (a comment deleted with ON DELETE CASCADE takes its notifications
-- with it). A fresh table would restart its sequence at the largest SURVIVING
-- id, so the counter of the old table is carried over before it is dropped;
-- DROP removes the old counter and RENAME carries the new one.
--
-- Nothing references notifications by foreign key, so dropping it is safe with
-- foreign keys enforced, and the migration runner applies this file in one
-- transaction: a failure leaves the 0021 table untouched.

CREATE TABLE notifications_new (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  ticket_id INTEGER NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
  kind TEXT NOT NULL CHECK(kind IN (
    'assigned',
    'state_changed',
    'resolved_awaiting_confirmation',
    'resolution_confirmed',
    'resolution_rejected',
    'priority_changed',
    'comment_public',
    'comment_internal',
    'form_waiting',
    'on_hold')),
  actor_user_id INTEGER REFERENCES users(id),
  from_value TEXT,
  to_value TEXT,
  comment_id INTEGER REFERENCES comments(id) ON DELETE CASCADE,
  step_index INTEGER,
  created_at TEXT NOT NULL,
  read_at TEXT);

INSERT INTO notifications_new (id, user_id, ticket_id, kind, actor_user_id, from_value, to_value, comment_id, step_index, created_at, read_at)
  SELECT id, user_id, ticket_id, kind, actor_user_id, from_value, to_value, comment_id, step_index, created_at, read_at
  FROM notifications;

DELETE FROM sqlite_sequence WHERE name = 'notifications_new';
INSERT INTO sqlite_sequence (name, seq)
  SELECT 'notifications_new', seq FROM sqlite_sequence WHERE name = 'notifications';

DROP TABLE notifications;
ALTER TABLE notifications_new RENAME TO notifications;

CREATE INDEX idx_notifications_recipient ON notifications(user_id, read_at, created_at);
