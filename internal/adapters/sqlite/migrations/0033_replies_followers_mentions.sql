-- 0033_replies_followers_mentions.sql — canned replies, ticket followers and
-- the two notification kinds they need (issue #418, roadmap E3-12).
--
-- Three changes in one script, because they ship as one slice of the feature:
--   * canned_replies — a reply an agent reuses, owned by ONE person or by
--     ONE desk, never both and never neither.
--   * ticket_followers — who follows a ticket, one row per (ticket,
--     person). Following grants NOTHING: the fan-out re-checks the ticket READ
--     scope of every follower at notification time, so a follower who
--     loses access stops being notified.
--   * the notifications kind list — 'mentioned' and 'assignee_changed'
--     join the closed vocabulary.
--
-- This is the FIFTH application of the recipe 0021 documented, after 0023, 0027
-- and 0028: the kind vocabulary is pinned by a CHECK, SQLite cannot alter a
-- CHECK in place, so adding kinds rebuilds the table. The rebuild changes ONLY
-- that list: the columns, their types, defaults, foreign keys and cascades, and
-- idx_notifications_recipient are copied from the 0028 table unchanged, and
-- every existing row is copied with its id, so a stored notification reads
-- back exactly as before (migration_0033_test.go compares the schema and the
-- rows before and after).
--
-- Why two kinds and not one: a follower already receives the GENERIC
-- kinds for comments (comment_public / comment_internal), state
-- (state_changed, resolution_confirmed, resolution_rejected, hold_closed) and
-- priority (priority_changed). Assignment had only 'assigned', whose sentence
-- speaks to the new assignee, so a follower needs its own kind — its to_value
-- is the new assignee's display name, and NULL means the ticket is now
-- unassigned. A mention is not "a new comment" either: it is addressed to the
-- person named, carries the comment id (it is a comment kind, so a requester
-- never receives it), and is deduplicated with precedence mentioned >
-- assigned > other so one recipient is notified once per event.
--
-- The sqlite_sequence step, and why dropping the table is safe here, are
-- explained in 0027: AUTOINCREMENT must keep its promise that a deleted id is
-- never reused, and notification_emails (0024) deliberately has no foreign key
-- to this table precisely so this rebuild cannot cascade delivery rows away.
--
-- Merge hazard: any later migration that rebuilds this CHECK must carry
-- 'mentioned' and 'assignee_changed' too, because out-of-order versions apply
-- independently.

CREATE TABLE canned_replies (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  owner_user_id INTEGER REFERENCES users(id) ON DELETE CASCADE,
  desk_id INTEGER REFERENCES desks(id) ON DELETE CASCADE,
  title TEXT NOT NULL CHECK(length(trim(title)) BETWEEN 1 AND 120),
  body TEXT NOT NULL CHECK(length(trim(body)) BETWEEN 1 AND 10000),
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  -- Exactly one owner: a personal reply belongs to its agent, a shared reply
  -- belongs to a desk every member of that desk can use. Both NULL would
  -- be an ownerless row nobody may use; both set would be an ambiguous row
  -- whose reach depends on which column a reader happens to pick.
  CHECK ((owner_user_id IS NULL) <> (desk_id IS NULL)));

CREATE INDEX idx_canned_replies_owner ON canned_replies(owner_user_id);
CREATE INDEX idx_canned_replies_desk ON canned_replies(desk_id);

CREATE TABLE ticket_followers (
  ticket_id INTEGER NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at TEXT NOT NULL,
  -- One row per follower per ticket: following twice is the same relation, and
  -- the primary key is what makes "am I following this?" one lookup. The
  -- fan-out reads followers BY TICKET, which this key already serves.
  PRIMARY KEY (ticket_id, user_id));

-- The other read path: every ticket one person follows.
CREATE INDEX idx_ticket_followers_user ON ticket_followers(user_id);

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
    'no_movement',
    'mentioned',
    'assignee_changed')),
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
