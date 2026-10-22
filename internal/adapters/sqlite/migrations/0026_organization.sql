-- 0026_organization.sql — the instance organization identity's logo (issue
-- #383, S3).
--
-- The three text facts of the identity (org_name, org_helpdesk_name,
-- org_support_email) live in the keyed settings table beside the other
-- instance settings and need no schema of their own. The logo cannot: the
-- settings table's value column is TEXT, and a logo is binary. This additive
-- table holds those bytes.
--
-- What each column answers:
--   key    the setting name ("org_logo"); the primary key keeps one row per
--          value, so a second save replaces it instead of stacking rows.
--   value  the bytes themselves: a PNG proven by its magic bytes before it is
--          written, bounded at 256 KiB — the cap domain.MaxOrganizationLogoBytes
--          enforces, restated here so the database refuses an oversized row
--          even if a future caller forgets to.
--   mime   the type the server answers with ("image/png"). It is stored rather
--          than hardcoded on the route so the served type is a fact of the row
--          and a future type cannot be answered under the old one's header.
--
-- No migration rewrites an existing row: an upgraded instance keeps its name
-- and has no logo until an administrator uploads one.

CREATE TABLE settings_blobs (
  key   TEXT PRIMARY KEY,
  value BLOB NOT NULL CHECK(length(value) BETWEEN 1 AND 262144),
  mime  TEXT NOT NULL CHECK(mime <> ''));
