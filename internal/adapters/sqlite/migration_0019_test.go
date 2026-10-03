package sqlite

import (
	"context"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
)

// Migration 0019 tests (draft optimistic lock — issue #254): the
// category_workflows.draft_revision column shape, its non-negative CHECK, the
// pre-0019 upgrade path preserving an existing draft at revision 0, and the
// version bookkeeping. Written against the numbered-series conventions of
// migration_0018_test.go.

// pre0019Migrations returns the embedded migrations strictly before 0019, so
// the upgrade-path test can seed a pre-0019 database and then apply the real
// full set (pattern of pre0018Migrations).
func pre0019Migrations(t *testing.T) fstest.MapFS {
	t.Helper()
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		t.Fatalf("read embedded migrations: %v", err)
	}
	out := fstest.MapFS{}
	for _, entry := range entries {
		if strings.Compare(entry.Name(), "0019_draft_revision.sql") >= 0 {
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

// TestMigration0019DraftRevisionColumnShape proves draft_revision exists on
// category_workflows as a NOT NULL INTEGER with DEFAULT 0, and that it is not
// part of the primary key.
func TestMigration0019DraftRevisionColumnShape(t *testing.T) {
	s := newTestDB(t)

	var (
		colType string
		notNull int
		pk      int
		dflt    *string
	)
	if err := s.db.QueryRow(
		`SELECT type, "notnull", pk, dflt_value FROM pragma_table_info('category_workflows') WHERE name = 'draft_revision'`,
	).Scan(&colType, &notNull, &pk, &dflt); err != nil {
		t.Fatalf("column draft_revision missing after 0019: %v", err)
	}
	if colType != "INTEGER" {
		t.Errorf("draft_revision type = %q, want INTEGER", colType)
	}
	if notNull != 1 {
		t.Errorf("draft_revision notnull = %d, want 1", notNull)
	}
	if pk != 0 {
		t.Errorf("draft_revision pk = %d, want 0 (category_id stays the primary key)", pk)
	}
	if dflt == nil || *dflt != "0" {
		t.Errorf("draft_revision default = %v, want 0", dflt)
	}
	// The pre-existing primary key is untouched: exactly one pk column.
	var pkCols int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('category_workflows') WHERE pk > 0`).Scan(&pkCols); err != nil {
		t.Fatal(err)
	}
	if pkCols != 1 {
		t.Errorf("category_workflows primary key columns = %d, want 1", pkCols)
	}
}

// TestMigration0019DraftRevisionCheckBound proves the CHECK is real: a
// negative revision is rejected while 0 and positive values are accepted.
func TestMigration0019DraftRevisionCheckBound(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	cat := seedCategory(t, s, "rev-check")

	if _, err := s.db.ExecContext(ctx, `INSERT INTO category_workflows(category_id, draft_json) VALUES (?, '[]')`, cat); err != nil {
		t.Fatalf("insert workflow row: %v", err)
	}
	var revision int64
	if err := s.db.QueryRowContext(ctx, `SELECT draft_revision FROM category_workflows WHERE category_id=?`, cat).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	if revision != 0 {
		t.Fatalf("fresh row draft_revision = %d, want DEFAULT 0", revision)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE category_workflows SET draft_revision=-1 WHERE category_id=?`, cat); err == nil {
		t.Fatal("negative draft_revision accepted, want CHECK(draft_revision >= 0) failure")
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE category_workflows SET draft_revision=3 WHERE category_id=?`, cat); err != nil {
		t.Fatalf("positive draft_revision rejected: %v", err)
	}
}

// TestMigration0019UpgradePathPreservesExistingDraft proves a pre-0019
// database upgrades cleanly: the column is absent before, the pre-existing
// draft bytes survive, they read back at revision 0, and version 19 is
// recorded.
func TestMigration0019UpgradePathPreservesExistingDraft(t *testing.T) {
	ctx := context.Background()
	pre, err := openDSN(testDSN(t))
	if err != nil {
		t.Fatalf("open pre-0019 db: %v", err)
	}
	pre.db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = pre.db.Close() })
	if err := migrate(ctx, pre.db, pre0019Migrations(t)); err != nil {
		t.Fatalf("migrate pre-0019 db: %v", err)
	}

	if columnExists(t, pre, "category_workflows", "draft_revision") {
		t.Fatal("draft_revision existed before 0019, want it added by the migration")
	}

	cat := seedCategory(t, pre, "pre-0019")
	const draft = `[{"type":"manual_task","manual_task":{"instructions":"written before 0019"}}]`
	if _, err := pre.db.ExecContext(ctx, `INSERT INTO category_workflows(category_id, draft_json) VALUES (?, ?)`, cat, draft); err != nil {
		t.Fatalf("insert pre-0019 draft: %v", err)
	}

	if err := migrate(ctx, pre.db, migrationsFS); err != nil {
		t.Fatalf("upgrade pre-0019 db: %v", err)
	}

	var (
		got      string
		revision int64
	)
	if err := pre.db.QueryRowContext(ctx, `SELECT draft_json, draft_revision FROM category_workflows WHERE category_id=?`, cat).Scan(&got, &revision); err != nil {
		t.Fatalf("read upgraded row: %v", err)
	}
	if got != draft {
		t.Errorf("upgraded draft = %s, want the pre-0019 bytes %s", got, draft)
	}
	if revision != 0 {
		t.Errorf("upgraded draft_revision = %d, want 0 (the pre-0019 row's only possible value)", revision)
	}

	var recorded int
	if err := pre.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version=19`).Scan(&recorded); err != nil {
		t.Fatalf("schema_migrations read: %v", err)
	}
	if recorded != 1 {
		t.Errorf("schema_migrations version=19 rows = %d, want 1", recorded)
	}
}

// TestMigration0019VersionBookkeeping proves the applied version is 19.
func TestMigration0019VersionBookkeeping(t *testing.T) {
	s := newTestDB(t)
	var applied int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version=19`).Scan(&applied); err != nil {
		t.Fatalf("schema_migrations read: %v", err)
	}
	if applied != 1 {
		t.Fatalf("schema_migrations version=19 rows = %d, want 1", applied)
	}
}
