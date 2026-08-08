-- 0001_init.sql — initial schema (design "SQLite Schema", tkt-mvp).
-- Timestamps are ISO-8601 UTC TEXT (D7): RFC3339 sorts lexicographically
-- in chronological order. All DDL here is additive; migrations are recorded
-- in schema_migrations by the runner (migrate.go).

CREATE TABLE users (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL CHECK(length(trim(name))>0),
  email TEXT NOT NULL UNIQUE CHECK(length(trim(email))>0),
  password_hash TEXT NOT NULL,
  active INTEGER NOT NULL DEFAULT 1 CHECK(active IN (0,1)),
  created_at TEXT NOT NULL);

CREATE TABLE sessions (
  id TEXT PRIMARY KEY,                    -- opaque 32-byte random token (hex)
  user_id INTEGER NOT NULL REFERENCES users(id),
  created_at TEXT NOT NULL,
  expires_at TEXT NOT NULL);
CREATE INDEX idx_sessions_expires ON sessions(expires_at);

CREATE TABLE categories (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL UNIQUE CHECK(length(trim(name))>0),
  created_at TEXT NOT NULL);

CREATE TABLE tickets (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  number INTEGER NOT NULL UNIQUE,
  title TEXT NOT NULL CHECK(length(trim(title))>0),
  description TEXT NOT NULL DEFAULT '',
  requester_name TEXT NOT NULL,
  requester_email TEXT NOT NULL,
  category_id INTEGER NOT NULL REFERENCES categories(id),
  priority TEXT NOT NULL CHECK(priority IN ('low','medium','high','critical')),
  state TEXT NOT NULL CHECK(state IN ('new','in_progress','resolved','closed','cancelled')),
  user_id INTEGER REFERENCES users(id),   -- assigned user; NULL = unassigned
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  resolved_at TEXT,
  closed_at TEXT);
CREATE INDEX idx_tickets_state_created ON tickets(state, created_at DESC);
CREATE INDEX idx_tickets_priority_created ON tickets(priority, created_at DESC);
CREATE INDEX idx_tickets_category ON tickets(category_id);
CREATE INDEX idx_tickets_user ON tickets(user_id);

CREATE TABLE comments (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  ticket_id INTEGER NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
  author TEXT NOT NULL,
  body TEXT NOT NULL CHECK(length(trim(body))>0),
  created_at TEXT NOT NULL);
CREATE INDEX idx_comments_ticket ON comments(ticket_id, created_at);

CREATE TABLE audit_events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  ticket_id INTEGER NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
  actor TEXT NOT NULL,
  action TEXT NOT NULL,                   -- 'created'|'transition'|'update'
  field TEXT,
  from_value TEXT,
  to_value TEXT,
  note TEXT,                              -- reopen reason for closed -> in_progress
  created_at TEXT NOT NULL);
CREATE INDEX idx_audit_ticket ON audit_events(ticket_id, created_at);

-- Version ledger for the migration runner. The runner bootstraps this table
-- before the first migration, so IF NOT EXISTS keeps the init script
-- idempotent with the runner (design lists schema_migrations in the schema).
CREATE TABLE IF NOT EXISTS schema_migrations (
  version INTEGER PRIMARY KEY,
  applied_at TEXT NOT NULL);
