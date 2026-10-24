-- 0034_company_sign_in.sql — company sign-in: provider identity links, the
-- pending approval state, and the in-flight authorization rows (issue #419,
-- S3/S7/S10).
--
-- Employees sign in with their Google Workspace or Microsoft Entra account.
-- That needs three additive facts: which provider identity an account is
-- linked to, an account state for someone a first sign-in created who is not
-- approved yet, and a short-lived row per authorization in progress. This
-- migration only ADDS; it rebuilds no table, so an upgraded instance keeps
-- every account, session, ticket, comment and notification exactly as it was.
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
-- Deliberately not here: the bell notification administrators get when an account
-- waits for approval. A notification without a ticket needs the notifications
-- table to grow a shape it does not have, and rebuilding that table belongs to
-- the unit that lands the notification — with every kind from 0029 onward — not
-- to this one. The flow announces the account through its own seam and stores
-- nothing about it.

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
