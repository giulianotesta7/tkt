-- 0028_notification_kinds_alerts.sql — the operational alert kinds (issue
-- #408): the assigned agent learns that a ticket reached the warning instant
-- of a frozen SLA commitment, that it came due unmet, or that the ticket has
-- had no movement for the inactivity window.
--
-- This is the FOURTH application of the recipe 0021 documented, after 0023 and
-- 0027: the kind vocabulary is pinned by a CHECK, SQLite cannot alter a CHECK
-- in place, so adding kinds rebuilds the table. The rebuild changes ONLY that
-- list: the columns, their types, defaults, foreign keys and cascades, and
-- idx_notifications_recipient are copied from the 0027 table unchanged, and
-- every existing row is copied with its id, so a stored notification reads
-- back exactly as before (migration_0028_test.go compares the schema and the
-- rows before and after).
--
-- Each milestone gets its OWN pair of kinds instead of one kind plus a
-- milestone fact, because "once per ticket per condition" is then a plain
-- lookup by kind — the same shape the hold reminder already dedups with — and
-- because each kind carries copy the reader can act on.
--
-- The sqlite_sequence step, and why dropping the table is safe here, are
-- explained in 0027: AUTOINCREMENT must keep its promise that a deleted id is
-- never reused, and notification_emails (0024) deliberately has no foreign key
-- to this table precisely so this rebuild cannot cascade delivery rows away.

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
    'on_hold',
    'hold_reminder',
    'hold_closed',
    'sla_first_response_at_risk',
    'sla_first_response_breached',
    'sla_resolution_at_risk',
    'sla_resolution_breached',
    'no_movement')),
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
