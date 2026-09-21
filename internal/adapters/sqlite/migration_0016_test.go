package sqlite

import (
	"context"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
)

// RED tests for migration 0016 (category SLA matrix materialization —
// issue #211): the AFTER INSERT trigger that copies sla_defaults into
// sla_policies for every newly created category, the one-time backfill
// for categories that already exist, the idempotence of that backfill,
// and the version bookkeeping. Written against the migration that does
// not exist yet: every assertion fails until 0016 lands (strict TDD RED).

// pre0016Migrations returns the embedded migrations strictly before 0016,
// so the upgrade-path test can seed a pre-0016 database and then apply the
// real full set (pattern of pre0012Migrations).
func pre0016Migrations(t *testing.T) fstest.MapFS {
	t.Helper()
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		t.Fatalf("read embedded migrations: %v", err)
	}
	out := fstest.MapFS{}
	for _, entry := range entries {
		if strings.Compare(entry.Name(), "0016_sla_matrix_trigger.sql") >= 0 {
			continue
		}
		blob, err := fs.ReadFile(migrationsFS, "migrations/"+entry.Name())
		if err != nil {
			t.Fatalf("read %s: %v", entry.Name(), err)
		}
		out["migrations/"+entry.Name()] = &fstest.MapFile{Data: blob}
	}
	return out
}

func TestMigration0016TriggerExists(t *testing.T) {
	s := newTestDB(t)

	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='trigger' AND name='trg_categories_sla_matrix'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("trigger trg_categories_sla_matrix missing after 0016")
	}
}

// TestMigration0016NewCategoryGetsDefaultMatrix proves a category created
// AFTER the migration is materialized by the trigger: exactly 4 matrix
// rows whose values equal sla_defaults.
func TestMigration0016NewCategoryGetsDefaultMatrix(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()

	catID := seedCategory(t, s, "Bugs")
	// The trigger has no ordering contract; compare as a set keyed by
	// priority against the exact default matrix.
	got := map[string]struct {
		firstResponseSeconds int
		resolveSeconds       int
	}{}
	rows, err := s.db.Query(`SELECT priority, first_response_seconds, resolve_seconds FROM sla_policies WHERE category_id = ?`, catID)
	if err != nil {
		t.Fatalf("read category matrix: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var priority string
		var fr, res int
		if err := rows.Scan(&priority, &fr, &res); err != nil {
			t.Fatal(err)
		}
		got[priority] = struct {
			firstResponseSeconds int
			resolveSeconds       int
		}{fr, res}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(got) != len(slaDefaultSeeds) {
		t.Fatalf("trigger materialized %d rows, want %d", len(got), len(slaDefaultSeeds))
	}
	for _, want := range slaDefaultSeeds {
		g, ok := got[string(want.priority)]
		if !ok {
			t.Errorf("matrix missing priority %s", want.priority)
			continue
		}
		if g.firstResponseSeconds != want.firstResponseSeconds || g.resolveSeconds != want.resolveSeconds {
			t.Errorf("matrix[%s] = (%d, %d), want (%d, %d)",
				want.priority, g.firstResponseSeconds, g.resolveSeconds,
				want.firstResponseSeconds, want.resolveSeconds)
		}
	}
	_ = ctx
}

// TestMigration0016TriggerReadsCurrentDefaults proves the trigger reads
// the CURRENT sla_defaults rows, not a snapshot baked in at migration
// time: changing the defaults before creating a category changes the
// materialized rows, while categories materialized earlier keep theirs.
func TestMigration0016TriggerReadsCurrentDefaults(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()

	earlyID := seedCategory(t, s, "Early")
	if _, err := s.db.ExecContext(ctx,
		`UPDATE sla_defaults SET first_response_seconds = 900, resolve_seconds = 7200 WHERE priority = 'critical'`); err != nil {
		t.Fatalf("update defaults: %v", err)
	}
	lateID := seedCategory(t, s, "Late")

	var fr, res int
	if err := s.db.QueryRow(`SELECT first_response_seconds, resolve_seconds FROM sla_policies WHERE category_id = ? AND priority = 'critical'`, lateID).
		Scan(&fr, &res); err != nil {
		t.Fatalf("read late category critical row: %v", err)
	}
	if fr != 900 || res != 7200 {
		t.Errorf("late category critical row = (%d, %d), want (900, 7200) — trigger must read current defaults", fr, res)
	}

	if err := s.db.QueryRow(`SELECT first_response_seconds, resolve_seconds FROM sla_policies WHERE category_id = ? AND priority = 'critical'`, earlyID).
		Scan(&fr, &res); err != nil {
		t.Fatalf("read early category critical row: %v", err)
	}
	if fr != 1800 || res != 14400 {
		t.Errorf("early category critical row = (%d, %d), want (1800, 14400) — a defaults edit never rewrites materialized history", fr, res)
	}
}

// TestMigration0016BackfillCoversPreExistingCategories proves the
// upgrade path: a category created by a PRE-0016 migration receives its 4
// materialized rows from the one-time backfill when 0016 is applied.
func TestMigration0016BackfillCoversPreExistingCategories(t *testing.T) {
	ctx := context.Background()
	pre, err := openDSN(testDSN(t))
	if err != nil {
		t.Fatalf("open pre-0016 db: %v", err)
	}
	pre.db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = pre.db.Close() })
	if err := migrate(ctx, pre.db, pre0016Migrations(t)); err != nil {
		t.Fatalf("migrate pre-0016 db: %v", err)
	}

	// A category that exists before 0016 lands: the trigger cannot have
	// seen it, only the backfill can cover it.
	catID := seedCategory(t, pre, "Legacy")
	var before int
	if err := pre.db.QueryRow(`SELECT COUNT(*) FROM sla_policies WHERE category_id = ?`, catID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if before != 0 {
		t.Fatalf("pre-0016 category matrix rows = %d, want 0 (sla_policies is empty before 0016)", before)
	}

	if err := migrate(ctx, pre.db, migrationsFS); err != nil {
		t.Fatalf("upgrade pre-0016 db: %v", err)
	}

	var after int
	if err := pre.db.QueryRow(`SELECT COUNT(*) FROM sla_policies WHERE category_id = ?`, catID).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != len(slaDefaultSeeds) {
		t.Fatalf("backfilled matrix rows = %d, want %d", after, len(slaDefaultSeeds))
	}
}

// TestMigration0016BackfillIsIdempotent proves the INSERT OR IGNORE
// backfill adds nothing when its SELECT would only duplicate existing
// rows: re-running the statement against an already-materialized
// category leaves the matrix at exactly 4 rows.
func TestMigration0016BackfillIsIdempotent(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()

	catID := seedCategory(t, s, "Bugs")
	if _, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO sla_policies (category_id, priority, first_response_seconds, resolve_seconds)
		 SELECT c.id, d.priority, d.first_response_seconds, d.resolve_seconds
		 FROM categories c CROSS JOIN sla_defaults d`); err != nil {
		t.Fatalf("re-run backfill: %v", err)
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sla_policies WHERE category_id = ?`, catID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != len(slaDefaultSeeds) {
		t.Fatalf("matrix rows after backfill re-run = %d, want %d (INSERT OR IGNORE no-op)", n, len(slaDefaultSeeds))
	}
}

func TestMigration0016VersionBookkeeping(t *testing.T) {
	s := newTestDB(t)
	var applied int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version=15`).Scan(&applied); err != nil {
		t.Fatalf("schema_migrations read: %v", err)
	}
	if applied != 1 {
		t.Fatalf("schema_migrations version=15 rows = %d, want 1", applied)
	}
}
