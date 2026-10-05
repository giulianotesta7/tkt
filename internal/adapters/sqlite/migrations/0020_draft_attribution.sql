-- 0020_draft_attribution.sql — who changed a category's draft, and when
-- (issue #253).
--
-- category_workflows gains draft_updated_by_user_id and draft_updated_at: the
-- actor and instant recorded by the SAME compare-and-swap UPDATE that writes
-- the draft bytes and advances draft_revision (issue #254). Attribution is
-- never a second write that could drift from the bytes it describes: the
-- statement that accepts an edit is the statement that names its author, and a
-- refused (stale) edit writes neither.
--
-- Both columns are NULLABLE with no default. A pre-0020 row keeps NULL
-- attribution — the operator of a historical draft is never guessed, exactly
-- like migration 0013 left historical comment authorship NULL. Here NULL and
-- "unknown actor" are the same truthful state, which is the opposite of
-- draft_revision, where 0-and-"unknown" had to be told apart for the guard.
--
-- draft_updated_at is TEXT in the persisted RFC3339 UTC form (timeLayout), the
-- same shape as workflow_versions.published_at, so a draft edit and a publish
-- read back through one parser. The role is NOT snapshotted: attribution here
-- answers "who", not "with what authority", so a live join against users is the
-- right resolution.
--
-- SQLite also permits adding a REFERENCES column only when its default is NULL;
-- nullable-with-no-default is therefore a migration-mechanics constraint as
-- much as a meaningful value.

ALTER TABLE category_workflows ADD COLUMN draft_updated_by_user_id INTEGER REFERENCES users(id);
ALTER TABLE category_workflows ADD COLUMN draft_updated_at TEXT;
