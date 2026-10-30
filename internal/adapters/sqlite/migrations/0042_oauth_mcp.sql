-- 0042_oauth_mcp.sql — OAuth 2.1 grants for MCP clients acting as a person
-- (issue #493).
--
-- tkt is the authorization server for its own /mcp endpoint. An MCP client
-- registers itself (Dynamic Client Registration), a person approves it on a
-- consent screen, and the client receives tokens that act as that person.
-- Service users (0037) never take part: an OAuth grant belongs to a person.
--
-- oauth_clients
--   * client_id is the public identifier the client presents; clients are
--     public (no secret), so nothing else is stored to authenticate them.
--   * redirect_uris is a newline-separated, non-empty list checked by the
--     service layer before it is stored.
--   * Registration is unauthenticated, so a client nobody ever approved is
--     pruned by the service; an approved one stays while its grants name it.
--
-- oauth_codes
--   * One authorization code per approved consent, stored as the SHA-256 hex
--     digest of the code. It binds the person, client, redirect URI, PKCE S256
--     challenge, scopes, and resource, and is spent once (used_at).
--   * grant_id names the grant the code was exchanged for, so a second
--     presentation of the code can revoke that grant (RFC 6749 §4.1.2).
--
-- oauth_grants
--   * One person's approval of one client, created when its code is
--     exchanged. scopes is a space-separated subset of the v1 vocabulary
--     (never tickets:all, checked by the service); resource is the /mcp URL the
--     tokens are bound to.
--   * Revoked rather than deleted, so audit events keep naming it.
--     revoked_by_user_id is the person or administrator who revoked it; NULL
--     means tkt revoked it (refresh token reuse, deactivation).
--
-- oauth_tokens
--   * Access and refresh tokens of a grant, stored as SHA-256 hex digests.
--     A refresh token is rotated: its used_at marks the rotation, and a second
--     presentation revokes the grant.
--
-- The guards below keep service users out at the database, as 0037 does for
-- the other people paths, and deactivating a person revokes every live grant
-- of that person whichever code path flips users.active. The trigger stamps the
-- database's UTC clock in the same RFC 3339 layout the store writes.
--
-- audit_events.oauth_grant_id attributes a ticket event to the grant whose
-- token made the request, as api_key_id does for API keys; the actor remains
-- the person.
--
-- api_admin_events gains the MCP switch actions. SQLite cannot alter a CHECK
-- in place, so the table is rebuilt with every column, row, id, and its
-- AUTOINCREMENT counter copied unchanged (the 0023 pattern). Nothing references
-- api_admin_events by foreign key, so dropping it is safe with foreign keys
-- enforced. The MCP switch lives in settings under 'mcp_enabled'; absent means
-- off.

CREATE TABLE oauth_clients (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  client_id     TEXT NOT NULL UNIQUE CHECK(length(client_id) > 0),
  name          TEXT NOT NULL CHECK(length(trim(name)) > 0),
  redirect_uris TEXT NOT NULL CHECK(length(trim(redirect_uris)) > 0),
  created_at    TEXT NOT NULL);

CREATE TABLE oauth_grants (
  id                 INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id            INTEGER NOT NULL REFERENCES users(id),
  client_id          INTEGER NOT NULL REFERENCES oauth_clients(id),
  scopes             TEXT NOT NULL CHECK(length(trim(scopes)) > 0),
  resource           TEXT NOT NULL CHECK(length(resource) > 0),
  created_at         TEXT NOT NULL,
  last_used_at       TEXT,
  revoked_at         TEXT,
  revoked_by_user_id INTEGER REFERENCES users(id));

CREATE INDEX idx_oauth_grants_user ON oauth_grants(user_id);
CREATE INDEX idx_oauth_grants_client ON oauth_grants(client_id);

CREATE TABLE oauth_codes (
  id             INTEGER PRIMARY KEY AUTOINCREMENT,
  code_hash      TEXT NOT NULL UNIQUE CHECK(length(code_hash) = 64),
  client_id      INTEGER NOT NULL REFERENCES oauth_clients(id),
  user_id        INTEGER NOT NULL REFERENCES users(id),
  redirect_uri   TEXT NOT NULL CHECK(length(redirect_uri) > 0),
  code_challenge TEXT NOT NULL CHECK(length(code_challenge) = 43),
  scopes         TEXT NOT NULL CHECK(length(trim(scopes)) > 0),
  resource       TEXT NOT NULL CHECK(length(resource) > 0),
  created_at     TEXT NOT NULL,
  expires_at     TEXT NOT NULL,
  used_at        TEXT,
  grant_id       INTEGER REFERENCES oauth_grants(id));

CREATE INDEX idx_oauth_codes_client ON oauth_codes(client_id);

CREATE TABLE oauth_tokens (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  grant_id   INTEGER NOT NULL REFERENCES oauth_grants(id),
  kind       TEXT NOT NULL CHECK(kind IN ('access','refresh')),
  token_hash TEXT NOT NULL UNIQUE CHECK(length(token_hash) = 64),
  created_at TEXT NOT NULL,
  expires_at TEXT NOT NULL,
  used_at    TEXT);

CREATE INDEX idx_oauth_tokens_grant ON oauth_tokens(grant_id, kind);

CREATE TRIGGER trg_oauth_grants_person_only
BEFORE INSERT ON oauth_grants
WHEN (SELECT kind FROM users WHERE id = NEW.user_id) IS NOT 'person'
BEGIN
  SELECT RAISE(ABORT, 'an OAuth grant belongs to a person');
END;

CREATE TRIGGER trg_oauth_grants_identity_immutable
BEFORE UPDATE OF user_id, client_id, scopes, resource ON oauth_grants
WHEN NEW.user_id IS NOT OLD.user_id
  OR NEW.client_id IS NOT OLD.client_id
  OR NEW.scopes IS NOT OLD.scopes
  OR NEW.resource IS NOT OLD.resource
BEGIN
  SELECT RAISE(ABORT, 'an OAuth grant identity is immutable');
END;

CREATE TRIGGER trg_oauth_codes_person_only
BEFORE INSERT ON oauth_codes
WHEN (SELECT kind FROM users WHERE id = NEW.user_id) IS NOT 'person'
BEGIN
  SELECT RAISE(ABORT, 'an OAuth grant belongs to a person');
END;

CREATE TRIGGER trg_users_deactivation_revokes_oauth_grants
AFTER UPDATE OF active ON users
WHEN OLD.active = 1 AND NEW.active = 0
BEGIN
  UPDATE oauth_grants SET revoked_at = strftime('%Y-%m-%dT%H:%M:%SZ', 'now')
  WHERE user_id = NEW.id AND revoked_at IS NULL;
END;

ALTER TABLE audit_events ADD COLUMN oauth_grant_id INTEGER REFERENCES oauth_grants(id);

CREATE TABLE api_admin_events_new (
  id              INTEGER PRIMARY KEY AUTOINCREMENT,
  action          TEXT NOT NULL CHECK(action IN (
                    'service_user_created',
                    'service_user_deactivated',
                    'service_user_reactivated',
                    'api_key_created',
                    'api_key_revoked',
                    'api_enabled',
                    'api_disabled',
                    'mcp_enabled',
                    'mcp_disabled')),
  actor_user_id   INTEGER NOT NULL REFERENCES users(id),
  service_user_id INTEGER REFERENCES users(id),
  api_key_id      INTEGER REFERENCES api_keys(id),
  created_at      TEXT NOT NULL);

INSERT INTO api_admin_events_new (id, action, actor_user_id, service_user_id, api_key_id, created_at)
  SELECT id, action, actor_user_id, service_user_id, api_key_id, created_at FROM api_admin_events;

DELETE FROM sqlite_sequence WHERE name = 'api_admin_events_new';
INSERT INTO sqlite_sequence (name, seq)
  SELECT 'api_admin_events_new', seq FROM sqlite_sequence WHERE name = 'api_admin_events';

DROP TABLE api_admin_events;
ALTER TABLE api_admin_events_new RENAME TO api_admin_events;

CREATE INDEX idx_api_admin_events_created ON api_admin_events(created_at);
