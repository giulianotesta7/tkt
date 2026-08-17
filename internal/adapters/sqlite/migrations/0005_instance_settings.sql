-- 0005_instance_settings.sql — single-row instance appearance settings.
--
-- The settings table holds one keyed row per instance setting. This
-- migration seeds the internal-comment background default; the store falls
-- back to the same default when the row is absent (service-level
-- validation keeps persisted values within the allowed set).

CREATE TABLE IF NOT EXISTS settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

INSERT OR IGNORE INTO settings (key, value) VALUES ('internal_comment_bg', '#E8EEFF');