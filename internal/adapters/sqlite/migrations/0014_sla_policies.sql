-- 0014_sla_policies.sql — per-category SLA configuration (issue #211).
--
-- sla_defaults is the global default matrix: one row per priority, each
-- carrying both milestone targets in INTEGER SECONDS (never text, so a
-- later move to business-hours calendars is a computation change and not a
-- schema change).
--
-- sla_policies is a category's MATERIALIZED matrix (4 priorities x 2
-- milestones). It is intentionally EMPTY after this migration: rows are
-- copied from the defaults per category at creation time, so there is no
-- NULL-fallback chain at read time. ON DELETE CASCADE on category_id is
-- required: category_store.go hard-deletes categories, and without the
-- cascade every deleted category would strand orphan policy rows.
--
-- The two settings keys seed the instance-level SLA configuration.
-- sla_enabled seeds OFF on purpose: a migration must not silently turn on
-- a customer-facing commitment for an existing installation. Enabling it
-- is an explicit admin action.
--
-- sla_enabled_at is deliberately NOT seeded. It records when the feature was
-- first switched on, and seeding it with the migration instant would claim an
-- activation that never happened — the feature is off. Absent, it reads back
-- the zero time until the first enable stamps it.

CREATE TABLE sla_defaults (
  priority               TEXT PRIMARY KEY CHECK(priority IN ('low','medium','high','critical')),
  first_response_seconds INTEGER NOT NULL CHECK(first_response_seconds >= 60),
  resolve_seconds        INTEGER NOT NULL CHECK(resolve_seconds >= 60)
);

CREATE TABLE sla_policies (
  category_id            INTEGER NOT NULL REFERENCES categories(id) ON DELETE CASCADE,
  priority               TEXT NOT NULL CHECK(priority IN ('low','medium','high','critical')),
  first_response_seconds INTEGER NOT NULL CHECK(first_response_seconds >= 60),
  resolve_seconds        INTEGER NOT NULL CHECK(resolve_seconds >= 60),
  PRIMARY KEY (category_id, priority)
);

INSERT INTO sla_defaults (priority, first_response_seconds, resolve_seconds) VALUES
  ('critical', 1800, 14400),
  ('high', 3600, 28800),
  ('medium', 14400, 86400),
  ('low', 28800, 259200);

INSERT OR IGNORE INTO settings (key, value) VALUES
  ('sla_enabled', '0'),
  ('sla_warning_percent', '80');
