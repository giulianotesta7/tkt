-- 0018_user_preferences.sql — per-user preferences (issue #210).
--
-- One row per (user, key). The FIRST key is the default queue order
-- (queue_order), but the table is deliberately generic: a later preference
-- (density, theme) reuses it without a new table.
--
-- There is NO CHECK on `value` on purpose. The closed set is enforced by the
-- application service BEFORE a write, and the READ path fails closed to the
-- default for any stored value outside that set. A CHECK would not add
-- safety here: a hand-edited or restored-from-backup row is exactly the case
-- the service's fail-closed normalization exists for, and a CHECK would make
-- such a row unreadable instead of harmless.
--
-- ON DELETE CASCADE: a deleted user takes their preferences with them, so no
-- orphan row can outlive its owner and no cleanup job is needed. The
-- composite PRIMARY KEY (user_id, key) makes one value per preference per
-- user a storage invariant, not a convention.

CREATE TABLE user_preferences (
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  "key"   TEXT NOT NULL,
  value   TEXT NOT NULL,
  PRIMARY KEY (user_id, "key")
);
