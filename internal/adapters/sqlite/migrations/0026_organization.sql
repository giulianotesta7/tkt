-- 0026_organization.sql — the instance's stored binary settings: the
-- organization logo (issue #383, S3) and the sign-in page's background image
-- (issue #383, S9/S10).
--
-- The text facts of both surfaces (org_name, org_helpdesk_name,
-- org_support_email, signin_*) live in the keyed settings table beside the other
-- instance settings and need no schema of their own. Bytes cannot: the settings
-- table's value column is TEXT. This additive table holds them.
--
-- What each column answers:
--   key    the setting name ("org_logo", "signin_background"); the primary key
--          keeps one row per value, so a second save replaces it instead of
--          stacking rows.
--   value  the bytes themselves, proven by their magic bytes before they are
--          written, with a per-key cap restated here so the database refuses an
--          oversized row even if a future caller forgets to: an upload must
--          never be able to write an unbounded blob through a path that skipped
--          validation. "signin_background" is a photograph behind a dark
--          overlay and gets 1 MiB (domain.MaxSigninBackgroundBytes); every
--          other key is a mark rendered at 24-48px and gets 256 KiB
--          (domain.MaxOrganizationLogoBytes).
--   mime   the type the server answers with ("image/png", "image/jpeg"). It is
--          stored rather than hardcoded on the route so the served type is a
--          fact of the row and a future type cannot be answered under the old
--          one's header.
--
-- No migration rewrites an existing row: an upgraded instance keeps its name and
-- has no logo or sign-in background until an administrator uploads one.

CREATE TABLE settings_blobs (
  key   TEXT PRIMARY KEY,
  value BLOB NOT NULL CHECK(
    (key = 'signin_background' AND length(value) BETWEEN 1 AND 1048576) OR
    (key <> 'signin_background' AND length(value) BETWEEN 1 AND 262144)),
  mime  TEXT NOT NULL CHECK(mime <> ''));
