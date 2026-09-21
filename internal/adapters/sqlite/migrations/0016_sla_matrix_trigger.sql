-- 0016_sla_matrix_trigger.sql — category SLA matrix materialization (issue
-- #211). sla_policies (migration 0014) was intentionally left EMPTY: a
-- category's 4-priority x 2-milestone matrix is a DERIVED invariant of the
-- category, and SQLite cannot express it declaratively (no FK or CHECK can
-- copy rows), so this migration materializes it two ways.
--
-- A TRIGGER, not a Go call: the matrix must exist the instant a category
-- exists. Doing the copy in a Go constructor would need a CategoryService
-- change rippling through every category-creation call site (including the
-- e2e seeder) and would still leave a window where a category has no
-- matrix; an AFTER INSERT trigger closes that window at the storage layer,
-- the same house pattern 0011 uses where a declarative constraint cannot
-- apply. A policy row is still upsertable per category afterwards (custom
-- targets), and the cascade from 0013 removes the rows with the category.
--
-- The one-time backfill copies the defaults for every category that
-- already exists when this migration runs; INSERT OR IGNORE makes it
-- idempotent against any rows already present. It never rewrites a
-- materialized matrix afterwards: a later defaults edit changes only
-- categories created from that point on.

INSERT OR IGNORE INTO sla_policies (category_id, priority, first_response_seconds, resolve_seconds)
SELECT c.id, d.priority, d.first_response_seconds, d.resolve_seconds
FROM categories c CROSS JOIN sla_defaults d;

CREATE TRIGGER trg_categories_sla_matrix
AFTER INSERT ON categories
BEGIN
  INSERT INTO sla_policies (category_id, priority, first_response_seconds, resolve_seconds)
  SELECT NEW.id, priority, first_response_seconds, resolve_seconds FROM sla_defaults;
END;
