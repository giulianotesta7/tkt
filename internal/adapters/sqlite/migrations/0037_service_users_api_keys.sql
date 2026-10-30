-- 0037_service_users_api_keys.sql — service users and the API keys they own
-- (issue #488, part 1).
--
-- An API key never belongs to a person. It belongs to a service user: a users
-- row whose kind is 'service'. Keeping service users in the users table means
-- every foreign key that names an actor (audit events, requesters, comment
-- authors, role changes) keeps working for them without a parallel identity.
--
-- users.kind
--   * 'person' (the default, so every existing row is a person) or 'service'.
--   * A service user only ever holds the user or agent role: the column CHECK
--     reads the role next to it, and a later role change is checked again by the
--     same constraint. Nothing administrative is reachable through a key.
--   * The kind is fixed at creation: a person never becomes a service user and
--     the reverse, so a key can never end up acting as a person.
--
-- The guards below keep a service user out of every path that belongs to people,
-- at the database, so a future code path cannot open them by accident:
--   * no browser session, so nothing can sign in as a service user;
--   * no company sign-in identity and no password recovery link;
--   * no desk membership, so desk assignment (least loaded, claim) never picks
--     one. Desk membership for agent services is decided with the endpoints;
--   * never a ticket's assignee, whichever path writes tickets.user_id.
-- Each guard covers INSERT and the UPDATE that would move an existing row onto
-- a service user.
--
-- api_keys
--   * public_id is the readable <id> of a tkt_<id>_<secret> token, unique so the
--     authenticator finds the row before it compares the secret.
--   * secret_hash is the SHA-256 hex digest of the secret; the secret itself is
--     shown once at creation and stored nowhere.
--   * scopes is a space-separated set from the closed v1 vocabulary; the service
--     layer validates it, the CHECK only refuses an empty set.
--   * A key belongs to a service user only (trigger), and is revoked rather than
--     deleted, so the audit history keeps naming it.
--
-- api_admin_events is the administrative history of the API surface: who
-- created or deactivated a service user, who created or revoked a key, who
-- switched the API on or off. Audit events so far are per ticket, and none of
-- these actions has a ticket.
--
-- The API switch lives in the settings table under 'api_enabled'. Absent means
-- off, so no row is written here.
--
-- Migration 0036 asks a migration that adds a users column to extend the root
-- update guard; the guard is recreated with kind in its list.

ALTER TABLE users ADD COLUMN kind TEXT NOT NULL DEFAULT 'person'
  CHECK(kind IN ('person','service') AND (kind = 'person' OR role IN ('user','agent')));

CREATE TRIGGER trg_users_kind_immutable
BEFORE UPDATE OF kind ON users
WHEN NEW.kind IS NOT OLD.kind
BEGIN
  SELECT RAISE(ABORT, 'user kind is immutable');
END;

DROP TRIGGER trg_users_root_immutable_update;

CREATE TRIGGER trg_users_root_immutable_update
BEFORE UPDATE ON users
WHEN OLD.role = 'root' AND (
  NEW.id IS NOT OLD.id
  OR NEW.name IS NOT OLD.name
  OR NEW.email IS NOT OLD.email
  OR NEW.active IS NOT OLD.active
  OR NEW.role IS NOT OLD.role
  OR NEW.created_at IS NOT OLD.created_at
  OR NEW.kind IS NOT OLD.kind)
BEGIN
  SELECT RAISE(ABORT, 'root account is immutable');
END;

CREATE TRIGGER trg_sessions_no_service_user
BEFORE INSERT ON sessions
WHEN (SELECT kind FROM users WHERE id = NEW.user_id) = 'service'
BEGIN
  SELECT RAISE(ABORT, 'a service user cannot sign in');
END;

CREATE TRIGGER trg_sessions_no_service_user_update
BEFORE UPDATE OF user_id ON sessions
WHEN (SELECT kind FROM users WHERE id = NEW.user_id) = 'service'
BEGIN
  SELECT RAISE(ABORT, 'a service user cannot sign in');
END;

CREATE TRIGGER trg_user_identities_no_service_user
BEFORE INSERT ON user_identities
WHEN (SELECT kind FROM users WHERE id = NEW.user_id) = 'service'
BEGIN
  SELECT RAISE(ABORT, 'a service user cannot sign in');
END;

CREATE TRIGGER trg_user_identities_no_service_user_update
BEFORE UPDATE OF user_id ON user_identities
WHEN (SELECT kind FROM users WHERE id = NEW.user_id) = 'service'
BEGIN
  SELECT RAISE(ABORT, 'a service user cannot sign in');
END;

CREATE TRIGGER trg_password_reset_tokens_no_service_user
BEFORE INSERT ON password_reset_tokens
WHEN (SELECT kind FROM users WHERE id = NEW.user_id) = 'service'
BEGIN
  SELECT RAISE(ABORT, 'a service user has no password');
END;

CREATE TRIGGER trg_password_reset_tokens_no_service_user_update
BEFORE UPDATE OF user_id ON password_reset_tokens
WHEN (SELECT kind FROM users WHERE id = NEW.user_id) = 'service'
BEGIN
  SELECT RAISE(ABORT, 'a service user has no password');
END;

CREATE TRIGGER trg_desk_members_no_service_user
BEFORE INSERT ON desk_members
WHEN (SELECT kind FROM users WHERE id = NEW.user_id) = 'service'
BEGIN
  SELECT RAISE(ABORT, 'a service user cannot be a desk member');
END;

CREATE TRIGGER trg_desk_members_no_service_user_update
BEFORE UPDATE OF user_id ON desk_members
WHEN (SELECT kind FROM users WHERE id = NEW.user_id) = 'service'
BEGIN
  SELECT RAISE(ABORT, 'a service user cannot be a desk member');
END;

CREATE TRIGGER trg_tickets_no_service_assignee
BEFORE INSERT ON tickets
WHEN NEW.user_id IS NOT NULL AND (SELECT kind FROM users WHERE id = NEW.user_id) = 'service'
BEGIN
  SELECT RAISE(ABORT, 'a service user cannot be assigned tickets');
END;

CREATE TRIGGER trg_tickets_no_service_assignee_update
BEFORE UPDATE OF user_id ON tickets
WHEN NEW.user_id IS NOT NULL AND (SELECT kind FROM users WHERE id = NEW.user_id) = 'service'
BEGIN
  SELECT RAISE(ABORT, 'a service user cannot be assigned tickets');
END;

CREATE TABLE api_keys (
  id                 INTEGER PRIMARY KEY AUTOINCREMENT,
  public_id          TEXT NOT NULL UNIQUE CHECK(length(public_id) > 0),
  user_id            INTEGER NOT NULL REFERENCES users(id),
  name               TEXT NOT NULL CHECK(length(trim(name)) > 0),
  secret_hash        TEXT NOT NULL CHECK(length(secret_hash) = 64),
  scopes             TEXT NOT NULL CHECK(length(trim(scopes)) > 0),
  created_by_user_id INTEGER NOT NULL REFERENCES users(id),
  created_at         TEXT NOT NULL,
  expires_at         TEXT,
  last_used_at       TEXT,
  revoked_at         TEXT,
  revoked_by_user_id INTEGER REFERENCES users(id));

CREATE INDEX idx_api_keys_user ON api_keys(user_id);

CREATE TRIGGER trg_api_keys_service_user_only
BEFORE INSERT ON api_keys
WHEN (SELECT kind FROM users WHERE id = NEW.user_id) IS NOT 'service'
BEGIN
  SELECT RAISE(ABORT, 'an API key belongs to a service user');
END;

CREATE TRIGGER trg_api_keys_owner_immutable
BEFORE UPDATE OF user_id, public_id, secret_hash ON api_keys
WHEN NEW.user_id IS NOT OLD.user_id
  OR NEW.public_id IS NOT OLD.public_id
  OR NEW.secret_hash IS NOT OLD.secret_hash
BEGIN
  SELECT RAISE(ABORT, 'an API key identity is immutable');
END;

CREATE TABLE api_admin_events (
  id              INTEGER PRIMARY KEY AUTOINCREMENT,
  action          TEXT NOT NULL CHECK(action IN (
                    'service_user_created',
                    'service_user_deactivated',
                    'service_user_reactivated',
                    'api_key_created',
                    'api_key_revoked',
                    'api_enabled',
                    'api_disabled')),
  actor_user_id   INTEGER NOT NULL REFERENCES users(id),
  service_user_id INTEGER REFERENCES users(id),
  api_key_id      INTEGER REFERENCES api_keys(id),
  created_at      TEXT NOT NULL);

CREATE INDEX idx_api_admin_events_created ON api_admin_events(created_at);
