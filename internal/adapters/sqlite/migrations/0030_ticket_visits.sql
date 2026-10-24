-- 0030_ticket_visits.sql — what changed since the last visit (issue #415).
-- Additive only: it creates ONE new table and changes no existing table and no
-- existing row. There is no backfill: a ticket nobody has opened yet simply has
-- no visit row, and any activity by someone else marks it until it is opened.
--
-- One row per (user, ticket) records how much of the ticket's history the user
-- had on screen the last time they opened it. The marks are row ids, not
-- instants: instants are stored to the second, so an event in the same second
-- as the visit would be ambiguous, while ids are strictly increasing.
--   audit_seen_id    the newest audit_events.id of the ticket when it was read.
--   comment_seen_id  the newest comments.id of the ticket when it was read.
--   visited_at       WHEN the visit happened, ISO-8601 UTC TEXT (D7).
--
-- A ticket is changed for a user when it has an audit event or a comment
-- visible to that user, made by someone else, with an id above the mark. The
-- marks only ever move forward, so two concurrent opens cannot lower them.

CREATE TABLE ticket_visits (
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  ticket_id INTEGER NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
  audit_seen_id INTEGER NOT NULL CHECK(audit_seen_id >= 0),
  comment_seen_id INTEGER NOT NULL CHECK(comment_seen_id >= 0),
  visited_at TEXT NOT NULL,
  PRIMARY KEY (user_id, ticket_id));
