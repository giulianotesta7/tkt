-- 0036_root_password_rotation.sql — root rotates and recovers its own password
-- (issue #414).
--
-- Migration 0003 made the root row completely immutable: no actor, root itself
-- included, could update or delete it. That guard is still right for every field
-- but one. Root must be able to change its own password from Preferences and to
-- recover it through a mailed link, and both paths write users.password_hash, so
-- the old guard turned a legitimate credential rotation into a database error.
--
-- This migration replaces ONLY the update guard. The new one aborts whenever any
-- protected column moves, so the password hash may change alone and root
-- identity, name, email, role, active and created_at stay immutable. The delete
-- guard, the single-root partial index and every other users trigger stay
-- exactly as migrations 0003 and 0004 created them; no row is rewritten and no
-- other table is touched.
--
-- The comparison is `IS NOT` per column rather than `<>`: a NULL on either side
-- would make `<>` evaluate to NULL, which a trigger WHEN clause treats as false
-- and would let the write through. A mixed update — the new hash plus a rename —
-- is refused whole, because the guard reads every column, not only the ones the
-- statement names.
--
-- A future migration that adds a users column must extend this list: a trigger
-- can only enumerate the columns that existed when it was created.

DROP TRIGGER trg_users_root_immutable_update;

CREATE TRIGGER trg_users_root_immutable_update
BEFORE UPDATE ON users
WHEN OLD.role = 'root' AND (
  NEW.id IS NOT OLD.id
  OR NEW.name IS NOT OLD.name
  OR NEW.email IS NOT OLD.email
  OR NEW.active IS NOT OLD.active
  OR NEW.role IS NOT OLD.role
  OR NEW.created_at IS NOT OLD.created_at)
BEGIN
  SELECT RAISE(ABORT, 'root account is immutable');
END;
