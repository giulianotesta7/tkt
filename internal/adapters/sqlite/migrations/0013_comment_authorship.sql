-- 0013_comment_authorship.sql — comment authorship: the acting session
-- user's id and a role snapshot per comment (issue #211, per-category SLA
-- prerequisite). The "first response" SLA milestone must identify the first
-- PUBLIC comment authored by an agent+, which the free-text `author` display
-- name cannot prove.
--
-- All DDL is additive and both columns are NULLABLE with no default: every
-- pre-existing comment keeps NULL authorship — the author of a historical row
-- is never guessed. author_role is a SNAPSHOT of the author's role taken at
-- write time (never a live join against users.role): a later role change must
-- not rewrite what a historical comment was.

ALTER TABLE comments ADD COLUMN author_user_id INTEGER REFERENCES users(id);
ALTER TABLE comments ADD COLUMN author_role TEXT CHECK(author_role IN ('user','agent','admin','root'));
