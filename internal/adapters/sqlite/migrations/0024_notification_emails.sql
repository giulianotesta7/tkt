-- 0024_notification_emails.sql — email delivery state for the notification
-- center (issue #362, spec S12). Additive only: it creates ONE new table and
-- ONE index, and changes no existing table and no existing row. There is NO
-- backfill: a notification written before 0024 gets no email row, so nothing
-- old is ever mailed retroactively — the sender only ever claims rows that
-- exist here.
--
-- Why its own table (S12): delivery is a delivery fact (status, attempts,
-- backoff, last error) whose lifecycle runs OUTSIDE the change's transaction
-- (S6, S13), while `notifications` (0021) stays the append-only record of what
-- happened. Keeping them apart means a retry updates a delivery row, never the
-- record of the change.
--
-- Why there is NO FOREIGN KEY on notification_id: `notifications` is rebuilt
-- by the documented recipe for adding a notification kind (CREATE the new
-- table / copy the rows / DROP the old / RENAME, which #352's 0023 does). With
-- ON DELETE CASCADE that DROP would delete every delivery row, and with a
-- restrictive edge it would fail the rebuild — either way every future kind
-- would have to know about this table. Integrity is kept by the store instead:
-- ClaimDue only ever returns rows that JOIN a live notification, and it
-- deletes the orphan rows (a notification cascaded away with its user, ticket,
-- or comment) inside the same claim transaction. A row here is therefore never
-- a claim on a notification that no longer exists.
--
-- What each column answers:
--   notification_id  WHICH notification this delivery is about. The PRIMARY
--                    KEY makes "one delivery row per notification" a storage
--                    invariant, which is what makes "never send twice" true
--                    across restarts (S6).
--   status           where the delivery is in its small, closed lifecycle:
--                    pending → sending → sent | skipped | failed, or
--                    sending → pending for a retry (application.
--                    EmailDeliveryStatus). 'sending' IS the claim: one sender
--                    owns the row until it settles it, and a row found there
--                    at startup is failed rather than re-sent (S14).
--   attempts         how many deliveries were tried, so the next backoff step
--                    is derived from stored state and survives a restart
--                    (S13, S14). Never negative.
--   next_attempt_at  WHEN the row is due, RFC3339 UTC TEXT (D7) like every
--                    other instant, so ORDER BY sorts lexicographically.
--                    Seeded from the notification's created_at, so a new row
--                    is due immediately; a retry moves it forward by its
--                    backoff step (S14).
--   claimed_at       WHEN the current (or last) claim happened; NULL before
--                    the first claim. Evidence that a process took the row.
--   sent_at          WHEN the SMTP server accepted it; NULL unless sent.
--   skip_reason      WHY it was deliberately not sent — one of the
--                    application.EmailSkip* reasons (email off, recipient
--                    opted out, recipient unavailable, ticket out of reach).
--                    An operator can tell a skip from a failure without
--                    reading logs (S14).
--   last_error       the last SMTP or store failure text, for the operator.
--                    Never message content and never a password (S8).
--   created_at       WHEN the delivery row was written: inside the change's
--                    own transaction, so it equals the notification's
--                    created_at.
--   updated_at       WHEN the row last changed state.
--
-- The index serves the only hot read of this table: the due pending rows in
-- claim order (status filter, next_attempt_at ordering).

CREATE TABLE notification_emails (
  notification_id INTEGER PRIMARY KEY,
  status TEXT NOT NULL CHECK(status IN ('pending', 'sending', 'sent', 'skipped', 'failed')),
  attempts INTEGER NOT NULL DEFAULT 0 CHECK(attempts >= 0),
  next_attempt_at TEXT NOT NULL,
  claimed_at TEXT,
  sent_at TEXT,
  skip_reason TEXT,
  last_error TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL);

CREATE INDEX idx_notification_emails_due ON notification_emails(status, next_attempt_at);
