-- 0021_notifications.sql — the in-app notification center (issue #344, slice
-- B1). Additive only: it creates ONE new table and changes no existing table
-- and no existing row (spec S18). There is no backfill and no seeded row — a
-- pre-0021 database simply has an empty notification center.
--
-- The table is modelled on audit_events (0001_init.sql): an append-only
-- record of what happened on a ticket, addressed here to ONE recipient. Rows
-- are written by the unit-of-work inside the transaction of the ticket change
-- they describe (spec S11), so they can never exist without that change.
--
-- What each column answers, justified against the S10 rows:
--   user_id      WHO must learn about it (the recipient; the application
--                computes recipients from policy and roles).
--   ticket_id    WHICH ticket it is about — the panel opens it, and the
--                ticket detail re-checks access on open (S16).
--   kind         WHAT happened, as a closed vocabulary of nine values (one per
--                S10 row). The copy is NEVER stored: it is catalogue-backed
--                and resolved at render time in the instance language (S15).
--   actor_user_id WHO did it. NULLABLE with no default: an automatic
--                workflow action has no user actor (S10 automatic rows), and
--                NULL is that truthful state. Never equal to user_id — nobody
--                is notified about their own action (domain guard).
--   from_value / to_value  the change itself for the kinds that describe one
--                (state_changed, priority_changed), stored as the raw domain
--                values so the catalogue can translate them. NULL elsewhere.
--   comment_id   the comment for comment_public / comment_internal (S10
--                comment rows). The body is never copied here. The FK
--                cascades: a notification cannot outlive the comment it
--                points at, and it is written in the comment's transaction
--                (slice B3).
--   step_index   the zero-based pinned workflow step for form_waiting (S10
--                "a workflow form step now waits on this person"), with the
--                same meaning as audit_events.step_index (migration 0008).
--                NULL for every other kind.
--   created_at   WHEN it happened, ISO-8601 UTC TEXT (D7) like every other
--                instant; the panel groups by day and orders newest first.
--   read_at      WHEN the recipient read it; NULL means unread. One nullable
--                instant is the whole read model: the bell counts NULLs and
--                "mark all as read" sets them.
--
-- Timestamps are TEXT (D7): RFC3339 sorts lexicographically, so ORDER BY
-- created_at DESC needs no parsing.

CREATE TABLE notifications (
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
    'form_waiting')),
  actor_user_id INTEGER REFERENCES users(id),
  from_value TEXT,
  to_value TEXT,
  comment_id INTEGER REFERENCES comments(id) ON DELETE CASCADE,
  step_index INTEGER,
  created_at TEXT NOT NULL,
  read_at TEXT);

-- Serves both read paths of the center (S13, S14): the unread count
-- (user_id, read_at IS NULL) and the newest-first list for one recipient
-- (user_id, created_at DESC). One index covers the recipient scope, the
-- unread filter, and the ordering column.
CREATE INDEX idx_notifications_recipient ON notifications(user_id, read_at, created_at);
