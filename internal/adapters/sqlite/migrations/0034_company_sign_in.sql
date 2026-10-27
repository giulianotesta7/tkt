-- 0034_company_sign_in.sql — company sign-in: provider identity links, the
-- pending approval state, the in-flight authorization rows, and the ticketless
-- account announcement (issue #419, S3/S7/S10).
--
-- Employees sign in with their Google Workspace or Microsoft Entra account.
-- That needs three additive facts: which provider identity an account is
-- linked to, an account state for someone a first sign-in created who is not
-- approved yet, and a short-lived row per authorization in progress. Apart
-- from one documented rebuild of the notifications table (below), this
-- migration only ADDS, so an upgraded instance keeps every account, session,
-- ticket and comment exactly as it was.
--
-- Why users.pending is a column of its own: the role column is a closed set
-- ('user','agent','admin','root') whose CHECK lives in 0003, so a new role
-- value would mean rebuilding users. Pending is a separate fact, and it is
-- spelled as "inactive and waiting": the CHECK refuses a row that is pending
-- AND active, so every guard that already excludes an inactive account — the
-- password login, session creation, the assignee pickers — excludes a pending
-- one without being touched. Approval is the pair pending = 0 with active = 1;
-- rejection leaves the account inactive with pending = 0, which is exactly a
-- deactivated account. The column is appended by ALTER TABLE ADD COLUMN with a
-- NOT NULL default, so every existing row reads 0 and nobody is asked for an
-- approval they never applied for. SQLite checks an added CHECK against the
-- rows already there, and the default satisfies it for every one of them.
--
-- user_identities answers, per row:
--   id              the row's own identity, referenced by nothing.
--   user_id         the account this provider identity signs in. ON DELETE
--                   CASCADE: an account that disappears takes its links with
--                   it, so no identity can outlive the account it opens.
--   provider        'google' or 'microsoft'. The list is closed (S1): a
--                   generic OpenID Connect provider is out of scope.
--   issuer          the iss claim of the ID token, stored rather than derived,
--                   so the row records what the provider actually said.
--   subject         the sub claim: the stable identifier of the person AT that
--                   issuer. From the first sign-in on this pair — never the
--                   email — is what the account is recognised by (S7), so a
--                   later email change at the provider links to the same row.
--   email           the email last seen at the provider, kept for
--                   administration and refreshed on every sign-in. It is not a
--                   key: the pair above is.
--   created_at      when the link was made.
--   last_sign_in_at when this identity last signed in.
--
--   UNIQUE(issuer, subject) is the resolution key: one provider identity is one
--   account. UNIQUE(user_id, provider) is the linking rule: one account holds
--   at most one identity per provider, so a second Google subject cannot claim
--   an account a Google identity already reaches. Both uniques are also the
--   only indexes this table needs — they serve the two reads that exist (by
--   identity, and by account and provider) — so no redundant index is added.
--
-- oidc_sign_in_attempts answers, per row:
--   state_hash     the SHA-256 hex digest of the flow's random state, never the
--                  state itself, so a leaked database yields no attempt anyone
--                  can present. PRIMARY KEY: one attempt per state, and the
--                  callback deletes the row, which is what makes it single use.
--                  Spelled NOT NULL because SQLite does not imply it for a
--                  TEXT primary key.
--   provider       'google' or 'microsoft': which flow started it, so a
--                  callback cannot complete an attempt of the other provider.
--   nonce          the value the ID token must echo back.
--   code_verifier  the PKCE verifier whose S256 challenge the authorization
--                  request carried; the code exchange presents it.
--   created_at     when the flow started, in the persisted ISO-8601 UTC form.
--   expires_at     when the attempt stops working (ten minutes after
--                  created_at). Expired rows are purged when the next flow
--                  starts, which is the read the index serves.
--
-- No row of any of the three is seeded: a fresh instance offers password
-- sign-in only until an administrator configures a provider.
--
-- The notifications rebuild. An account created by a company sign-in waits for
-- approval, and the administrator has to learn about it in the bell. That
-- notice is the one notification that is not about a ticket, and the table
-- 0021 created cannot express it: ticket_id is NOT NULL and every kind is a
-- ticket event. SQLite cannot relax a NOT NULL or change a CHECK in place, so
-- 0034 produces the table's sixth shape — the initial 0021 table and the
-- rebuilds of 0023, 0027, 0028 and 0033 came before it — by applying the recipe
-- 0023 documented:
--   * The kind CHECK lists every value from 0021 plus the ones 0023, 0027, 0028
--     and 0033 added (0033 added 'mentioned' and 'assignee_changed', and warns
--     that a later rebuild must carry them), and finally
--     company_account_pending.
--   * ticket_id becomes nullable: NULL is the truthful state of a notice that
--     names no ticket. A ticket notification still requires a ticket exactly
--     as before, and a stored 0 is impossible because the foreign key would
--     reject it.
--   * pending_user_id is the account being announced, cascading with it: a
--     rejected account that is deleted takes its notice with it, so the bell
--     never points at a missing account. It is NULL for every ticket kind.
--   * A table CHECK closes the shape: the account kind carries a POSITIVE
--     account and no ticket, and every other kind carries a ticket and no
--     account. The positivity mirrors the domain rule; the foreign key alone
--     would accept a 0 or negative id if a row with that id existed, and a
--     bare "pending_user_id > 0" would let NULL through because a CHECK only
--     fails on FALSE, not on NULL. It deliberately says nothing about
--     actor_user_id, which is optional for both shapes (an administrator may
--     be recorded, the system may not).
--   * Every existing row is copied with its id, and the sqlite_sequence
--     counter is carried over before the old table is dropped, so the
--     AUTOINCREMENT promise that a deleted id is never reused holds across
--     the rebuild.
--   * idx_notifications_recipient is recreated unchanged: the bell count and
--     the newest-first list read exactly as before.
-- The rebuild touches no other object. notification_emails (0024) is not
-- referenced by a foreign key precisely so this rebuild cannot cascade the
-- delivery rows away, and those rows keep pointing at the same notification
-- ids. Nothing references notifications by foreign key.

ALTER TABLE users ADD COLUMN pending INTEGER NOT NULL DEFAULT 0
  CHECK(pending IN (0,1) AND (pending = 0 OR active = 0));

CREATE TABLE user_identities (
  id              INTEGER PRIMARY KEY,
  user_id         INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  provider        TEXT NOT NULL CHECK(provider IN ('google','microsoft')),
  issuer          TEXT NOT NULL CHECK(length(trim(issuer)) > 0),
  subject         TEXT NOT NULL CHECK(length(trim(subject)) > 0),
  email           TEXT NOT NULL DEFAULT '',
  created_at      TEXT NOT NULL,
  last_sign_in_at TEXT NOT NULL,
  UNIQUE(issuer, subject),
  UNIQUE(user_id, provider));

-- A TEXT PRIMARY KEY is not implicitly NOT NULL in SQLite, and an attempt
-- without a state hash would be a row nothing can address: it is refused
-- explicitly.
CREATE TABLE oidc_sign_in_attempts (
  state_hash    TEXT PRIMARY KEY NOT NULL CHECK(length(state_hash) > 0),
  provider      TEXT NOT NULL CHECK(provider IN ('google','microsoft')),
  nonce         TEXT NOT NULL CHECK(length(nonce) > 0),
  code_verifier TEXT NOT NULL CHECK(length(code_verifier) > 0),
  created_at    TEXT NOT NULL,
  expires_at    TEXT NOT NULL);

CREATE INDEX idx_oidc_sign_in_attempts_expires ON oidc_sign_in_attempts(expires_at);

-- The ticketless account announcement, applying the notifications rebuild
-- recipe of 0023/0027/0028 with one extra change: the ticket becomes an
-- optional subject next to the pending account.
CREATE TABLE notifications_new (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  ticket_id INTEGER REFERENCES tickets(id) ON DELETE CASCADE,
  pending_user_id INTEGER REFERENCES users(id) ON DELETE CASCADE,
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
    'assignee_changed',
    'company_account_pending')),
  actor_user_id INTEGER REFERENCES users(id),
  from_value TEXT,
  to_value TEXT,
  comment_id INTEGER REFERENCES comments(id) ON DELETE CASCADE,
  step_index INTEGER,
  created_at TEXT NOT NULL,
  read_at TEXT,
  CHECK (
    (kind = 'company_account_pending'
      AND ticket_id IS NULL
      AND pending_user_id IS NOT NULL
      AND pending_user_id > 0
      AND from_value IS NULL
      AND to_value IS NULL
      AND comment_id IS NULL
      AND step_index IS NULL)
    OR (kind <> 'company_account_pending'
      AND ticket_id IS NOT NULL
      AND pending_user_id IS NULL)));

INSERT INTO notifications_new (id, user_id, ticket_id, pending_user_id, kind, actor_user_id, from_value, to_value, comment_id, step_index, created_at, read_at)
  SELECT id, user_id, ticket_id, NULL, kind, actor_user_id, from_value, to_value, comment_id, step_index, created_at, read_at
  FROM notifications;

DELETE FROM sqlite_sequence WHERE name = 'notifications_new';
INSERT INTO sqlite_sequence (name, seq)
  SELECT 'notifications_new', seq FROM sqlite_sequence WHERE name = 'notifications';

DROP TABLE notifications;
ALTER TABLE notifications_new RENAME TO notifications;

CREATE INDEX idx_notifications_recipient ON notifications(user_id, read_at, created_at);
