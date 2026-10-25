-- 0029_password_reset_tokens.sql — the self-service password recovery links
-- (issue #414, S3/S8/S9).
--
-- A recovery link is a single-use credential with a deadline, so it needs a row
-- of its own: the account it belongs to, the instant it was created (the
-- throttle reads it), the instant it stops working, and the instant it was
-- spent. This migration only CREATES the table and its index; it rewrites no
-- existing row, so an upgraded instance keeps every account, session and ticket
-- exactly as it was and simply has no link outstanding.
--
-- What each column answers:
--   id          the row's own identity, referenced by nothing.
--   user_id     the account the link can reset. ON DELETE CASCADE: an account
--               that disappears takes its links with it, so no row can outlive
--               the password it would change.
--   token_hash  the SHA-256 hex digest of the link's token, never the token
--               itself (S8). A leaked database yields no working link. UNIQUE:
--               one link is one row, so a lookup by hash can never pick the
--               wrong account.
--   created_at  when the link was issued, in the persisted ISO-8601 UTC form.
--               It is what "one email per account every five minutes" measures
--               from (S9).
--   expires_at  when the link stops working (S3: one hour after created_at).
--               Both instants are compared as passed-in values, never against a
--               wall clock read inside the store.
--   used_at     when the link was spent, NULL while it is still redeemable. A
--               used link is refused like an unknown or expired one, and a new
--               request or a password change clears the pending rows instead of
--               writing this column, so "spent" and "void" stay distinguishable.
--
-- The index answers the two reads this table exists for: the newest link of one
-- account (the throttle) and clearing that account's pending rows.

CREATE TABLE password_reset_tokens (
  id INTEGER PRIMARY KEY,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  token_hash TEXT NOT NULL UNIQUE,
  created_at TEXT NOT NULL,
  expires_at TEXT NOT NULL,
  used_at TEXT);

CREATE INDEX idx_password_reset_tokens_user_created ON password_reset_tokens(user_id, created_at);
