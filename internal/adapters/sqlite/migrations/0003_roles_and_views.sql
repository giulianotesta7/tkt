-- 0003_roles_and_views.sql — roles, requester ownership, comment
-- visibility, groups, role audit, and audit actor/reason (design
-- "Persistence and Recovery"; role-authorization, ticket-access,
-- comment-visibility, group-management specs).
--
-- All DDL is additive; legacy rows are normalized by the Go backfill in
-- migrate.go (root from reliable id=1, requester from reliable audit
-- evidence), never guessed here.

-- users.role: closed hierarchy user < agent < admin < root. Legacy rows
-- default to 'agent' (design: remaining users backfill to agent); the
-- application assigns explicit roles to new rows. The partial unique index
-- permits AT MOST ONE root row; the triggers below make root immutable.
ALTER TABLE users ADD COLUMN role TEXT NOT NULL DEFAULT 'agent'
  CHECK(role IN ('user','agent','admin','root'));

CREATE UNIQUE INDEX idx_users_one_root ON users(role) WHERE role = 'root';

-- tickets.requester_user_id: immutable creating-session user (ticket-access
-- spec). NULL until proven by backfill; NULL tickets are agent+-only.
ALTER TABLE tickets ADD COLUMN requester_user_id INTEGER REFERENCES users(id);
CREATE INDEX idx_tickets_requester ON tickets(requester_user_id);

-- comments.visibility: public|internal; legacy rows backfill to 'public'
-- via the default so no historical conversation becomes hidden.
ALTER TABLE comments ADD COLUMN visibility TEXT NOT NULL DEFAULT 'public'
  CHECK(visibility IN ('public','internal'));

-- groups + N:N memberships (group-management spec): named sets of
-- agent-plus personnel, unique non-empty names. The store lands in S6; the
-- user-membership invariant is enforced by triggers right now.
CREATE TABLE groups (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL UNIQUE CHECK(length(trim(name))>0),
  created_at TEXT NOT NULL);

CREATE TABLE group_members (
  group_id INTEGER NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
  user_id INTEGER NOT NULL REFERENCES users(id),
  created_at TEXT NOT NULL,
  PRIMARY KEY (group_id, user_id));

-- role_changes: append-only role-management audit (role-authorization spec
-- "explicit, audited use cases"; the acting user is the actor).
CREATE TABLE role_changes (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id INTEGER NOT NULL REFERENCES users(id),
  from_role TEXT NOT NULL CHECK(from_role IN ('user','agent','admin','root')),
  to_role TEXT NOT NULL CHECK(to_role IN ('user','agent','admin','root')),
  actor_user_id INTEGER REFERENCES users(id),
  reason TEXT,
  created_at TEXT NOT NULL);

-- audit actor/reason: the acting session user and reassignment/reopen
-- reasons (ticket-access, ticket-management specs; consumed from S4).
ALTER TABLE audit_events ADD COLUMN actor_user_id INTEGER REFERENCES users(id);
ALTER TABLE audit_events ADD COLUMN reason TEXT;

-- Root invariants (role-authorization spec): no actor — including root
-- itself — may update or delete the root account. The backfill/recovery
-- flows promote a NON-root row to root, so they are unaffected.
CREATE TRIGGER trg_users_root_immutable_update
BEFORE UPDATE ON users
WHEN OLD.role = 'root'
BEGIN
  SELECT RAISE(ABORT, 'root account is immutable');
END;

CREATE TRIGGER trg_users_root_immutable_delete
BEFORE DELETE ON users
WHEN OLD.role = 'root'
BEGIN
  SELECT RAISE(ABORT, 'root account is immutable');
END;

-- Group invariants (group-management spec): role 'user' is never a member,
-- and no member may be downgraded to 'user'. Application checks plus DB
-- triggers (design "Persistence and Recovery").
CREATE TRIGGER trg_group_members_no_user
BEFORE INSERT ON group_members
WHEN (SELECT role FROM users WHERE id = NEW.user_id) = 'user'
BEGIN
  SELECT RAISE(ABORT, 'user role cannot be a group member');
END;

CREATE TRIGGER trg_group_members_no_user_update
BEFORE UPDATE OF user_id ON group_members
WHEN (SELECT role FROM users WHERE id = NEW.user_id) = 'user'
BEGIN
  SELECT RAISE(ABORT, 'user role cannot be a group member');
END;

CREATE TRIGGER trg_users_no_group_member_downgrade
BEFORE UPDATE OF role ON users
WHEN NEW.role = 'user' AND EXISTS (SELECT 1 FROM group_members WHERE user_id = NEW.id)
BEGIN
  SELECT RAISE(ABORT, 'group members cannot be downgraded to user');
END;
