-- 0004_desks.sql — persisted Groups-to-Desks terminology rename.
--
-- This migration deliberately renames the existing tables and membership
-- column in place. SQLite preserves rows, keys, indexes, and sqlite_sequence
-- through these DDL operations; only the three trigger names and messages
-- require explicit replacement.

DROP TRIGGER trg_group_members_no_user;
DROP TRIGGER trg_group_members_no_user_update;
DROP TRIGGER trg_users_no_group_member_downgrade;

ALTER TABLE groups RENAME TO desks;
ALTER TABLE group_members RENAME TO desk_members;
ALTER TABLE desk_members RENAME COLUMN group_id TO desk_id;

CREATE TRIGGER trg_desk_members_no_user
BEFORE INSERT ON desk_members
WHEN (SELECT role FROM users WHERE id = NEW.user_id) = 'user'
BEGIN
  SELECT RAISE(ABORT, 'user role cannot be a desk member');
END;

CREATE TRIGGER trg_desk_members_no_user_update
BEFORE UPDATE OF user_id ON desk_members
WHEN (SELECT role FROM users WHERE id = NEW.user_id) = 'user'
BEGIN
  SELECT RAISE(ABORT, 'user role cannot be a desk member');
END;

CREATE TRIGGER trg_users_no_desk_member_downgrade
BEFORE UPDATE OF role ON users
WHEN NEW.role = 'user' AND EXISTS (SELECT 1 FROM desk_members WHERE user_id = NEW.id)
BEGIN
  SELECT RAISE(ABORT, 'desk members cannot be downgraded to user');
END;
