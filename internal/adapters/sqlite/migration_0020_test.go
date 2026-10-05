package sqlite

import (
	"context"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
)

// Migration 0020 tests (draft attribution — issue #253): the
// category_workflows.draft_updated_by_user_id and draft_updated_at column
// shapes, the users foreign key, the pre-0020 upgrade path preserving an
// existing draft with NULL attribution, and the version bookkeeping. Written
// against the numbered-series conventions of migration_0019_test.go.

// pre0020Migrations returns the embedded migrations strictly before 0020, so
// the upgrade-path test can seed a pre-0020 database and then apply the real
// full set (pattern of pre0019Migrations).
func pre0020Migrations(t *testing.T) fstest.MapFS {
	t.Helper()
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		t.Fatalf("read embedded migrations: %v", err)
	}
	out := fstest.MapFS{}
	for _, entry := range entries {
		if strings.Compare(entry.Name(), "0020_draft_attribution.sql") >= 0 {
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

// TestMigration0020AttributionColumnShape proves both attribution columns exist
// on category_workflows, both are NULLABLE (a pre-0020 row must stay valid),
// draft_updated_at is TEXT, and draft_updated_by_user_id carries the users
// foreign key.
func TestMigration0020AttributionColumnShape(t *testing.T) {
	s := newTestDB(t)

	for _, tc := range []struct {
		column  string
		wantTyp string
	}{
		{"draft_updated_by_user_id", "INTEGER"},
		{"draft_updated_at", "TEXT"},
	} {
		var (
			colType string
			notNull int
			pk      int
			dflt    *string
		)
		if err := s.db.QueryRow(
			`SELECT type, "notnull", pk, dflt_value FROM pragma_table_info('category_workflows') WHERE name = ?`, tc.column,
		).Scan(&colType, &notNull, &pk, &dflt); err != nil {
			t.Fatalf("column %s missing after 0020: %v", tc.column, err)
		}
		if colType != tc.wantTyp {
			t.Errorf("%s type = %q, want %q", tc.column, colType, tc.wantTyp)
		}
		if notNull != 0 {
			t.Errorf("%s notnull = %d, want 0 (pre-0020 rows keep NULL)", tc.column, notNull)
		}
		if pk != 0 {
			t.Errorf("%s pk = %d, want 0 (category_id stays the primary key)", tc.column, pk)
		}
		if dflt != nil {
			t.Errorf("%s default = %v, want none (NULL is the truthful absent value)", tc.column, *dflt)
		}
	}

	// The FK is real: draft_updated_by_user_id references users(id).
	var (
		fkTable string
		fkFrom  string
		fkTo    string
	)
	found := false
	rows, err := s.db.Query(`SELECT "table", "from", "to" FROM pragma_foreign_key_list('category_workflows')`)
	if err != nil {
		t.Fatalf("foreign key list: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		if err := rows.Scan(&fkTable, &fkFrom, &fkTo); err != nil {
			t.Fatal(err)
		}
		if fkFrom == "draft_updated_by_user_id" {
			found = true
			if fkTable != "users" || fkTo != "id" {
				t.Errorf("draft_updated_by_user_id FK = %s(%s), want users(id)", fkTable, fkTo)
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Error("draft_updated_by_user_id has no foreign key to users")
	}
}

// TestMigration0020UpgradePathPreservesExistingDraft proves a pre-0020
// database upgrades cleanly: the columns are absent before, the pre-existing
// draft bytes survive, their attribution reads back NULL (no fabricated
// provenance), and version 20 is recorded.
func TestMigration0020UpgradePathPreservesExistingDraft(t *testing.T) {
	ctx := context.Background()
	pre, err := openDSN(testDSN(t))
	if err != nil {
		t.Fatalf("open pre-0020 db: %v", err)
	}
	pre.db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = pre.db.Close() })
	if err := migrate(ctx, pre.db, pre0020Migrations(t)); err != nil {
		t.Fatalf("migrate pre-0020 db: %v", err)
	}

	for _, column := range []string{"draft_updated_by_user_id", "draft_updated_at"} {
		if columnExists(t, pre, "category_workflows", column) {
			t.Fatalf("%s existed before 0020, want it added by the migration", column)
		}
	}

	cat := seedCategory(t, pre, "pre-0020")
	const draft = `[{"type":"manual_task","manual_task":{"instructions":"written before 0020"}}]`
	if _, err := pre.db.ExecContext(ctx, `INSERT INTO category_workflows(category_id, draft_json) VALUES (?, ?)`, cat, draft); err != nil {
		t.Fatalf("insert pre-0020 draft: %v", err)
	}

	if err := migrate(ctx, pre.db, migrationsFS); err != nil {
		t.Fatalf("upgrade pre-0020 db: %v", err)
	}

	var (
		got     string
		draftBy *int64
		draftAt *string
	)
	if err := pre.db.QueryRowContext(ctx, `SELECT draft_json, draft_updated_by_user_id, draft_updated_at FROM category_workflows WHERE category_id=?`, cat).Scan(&got, &draftBy, &draftAt); err != nil {
		t.Fatalf("read upgraded row: %v", err)
	}
	if got != draft {
		t.Errorf("upgraded draft = %s, want the pre-0020 bytes %s", got, draft)
	}
	if draftBy != nil || draftAt != nil {
		t.Errorf("upgraded attribution = (%v, %v), want (nil, nil) — no backfill, no guessed author", draftBy, draftAt)
	}

	var recorded int
	if err := pre.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version=20`).Scan(&recorded); err != nil {
		t.Fatalf("schema_migrations read: %v", err)
	}
	if recorded != 1 {
		t.Errorf("schema_migrations version=20 rows = %d, want 1", recorded)
	}
}

// TestMigration0020VersionBookkeeping proves the applied version is 20.
func TestMigration0020VersionBookkeeping(t *testing.T) {
	s := newTestDB(t)
	var applied int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version=20`).Scan(&applied); err != nil {
		t.Fatalf("schema_migrations read: %v", err)
	}
	if applied != 1 {
		t.Fatalf("schema_migrations version=20 rows = %d, want 1", applied)
	}
}
