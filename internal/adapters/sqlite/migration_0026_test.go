package sqlite

import (
	"context"
	"fmt"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
)

// Migration 0026 tests (organization identity — issue #383, S3): the settings
// table stores every instance setting as TEXT, so the organization logo needs
// its own BLOB table. These tests pin the table's shape, the bounds the store
// relies on, and the upgrade path a pre-0026 instance takes: its settings rows
// survive untouched and no logo exists yet.

// pre0026Migrations returns the embedded migrations strictly before 0026, so
// the upgrade-path test can seed a pre-0026 database and then apply the real
// full set (pattern of pre0024Migrations).
func pre0026Migrations(t *testing.T) fstest.MapFS {
	t.Helper()
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		t.Fatalf("read embedded migrations: %v", err)
	}
	out := fstest.MapFS{}
	for _, entry := range entries {
		if strings.Compare(entry.Name(), "0026_organization.sql") >= 0 {
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

// TestMigration0026SettingsBlobsTableShape pins the three columns and their
// nullability: a stored value is a key, the bytes themselves, and the type the
// server answers with, never a NULL in any of them.
func TestMigration0026SettingsBlobsTableShape(t *testing.T) {
	s := newTestDB(t)

	type columnShape struct {
		column  string
		wantTyp string
		notNull int
		pk      int
	}
	want := []columnShape{
		{"key", "TEXT", 0, 1},
		{"value", "BLOB", 1, 0},
		{"mime", "TEXT", 1, 0},
	}
	rows, err := s.db.Query(`SELECT name, type, "notnull", pk FROM pragma_table_info('settings_blobs')`)
	if err != nil {
		t.Fatalf("pragma_table_info('settings_blobs'): %v", err)
	}
	defer rows.Close()
	got := map[string]columnShape{}
	for rows.Next() {
		var (
			name, colType string
			notNull, pk   int
		)
		if err := rows.Scan(&name, &colType, &notNull, &pk); err != nil {
			t.Fatal(err)
		}
		got[name] = columnShape{name, colType, notNull, pk}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Errorf("settings_blobs has %d columns, want %d: %v", len(got), len(want), got)
	}
	for _, w := range want {
		g, ok := got[w.column]
		if !ok {
			t.Errorf("column %s missing after 0026", w.column)
			continue
		}
		if g.wantTyp != w.wantTyp {
			t.Errorf("%s type = %q, want %q", w.column, g.wantTyp, w.wantTyp)
		}
		if g.notNull != w.notNull {
			t.Errorf("%s notnull = %d, want %d", w.column, g.notNull, w.notNull)
		}
		if g.pk != w.pk {
			t.Errorf("%s pk = %d, want %d", w.column, g.pk, w.pk)
		}
	}
}

// TestMigration0026SettingsBlobsBounds proves the CHECKs the store turns into
// refusals: one row per key, a non-empty payload, the 256 KiB logo cap, and a
// named type.
func TestMigration0026SettingsBlobsBounds(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()

	insert := func(key string, value []byte, mime string) error {
		_, err := s.db.ExecContext(ctx, `INSERT INTO settings_blobs (key, value, mime) VALUES (?, ?, ?)`, key, value, mime)
		return err
	}

	if err := insert("org_logo", []byte("\x89PNG\r\n\x1a\n"), "image/png"); err != nil {
		t.Fatalf("valid logo row refused: %v", err)
	}
	if err := insert("org_logo", []byte("\x89PNG\r\n\x1a\n"), "image/png"); err == nil {
		t.Error("second row for the same key succeeded, want PRIMARY KEY failure")
	}
	if err := insert("empty", []byte{}, "image/png"); err == nil {
		t.Error("empty payload accepted, want CHECK failure")
	}
	oversize := make([]byte, 256<<10+1)
	if err := insert("oversize", oversize, "image/png"); err == nil {
		t.Error("payload above the 256 KiB cap accepted, want CHECK failure")
	}
	if atCap := make([]byte, 256<<10); len(atCap) == 0 {
		t.Fatal("unreachable")
	} else if err := insert("at_cap", atCap, "image/png"); err != nil {
		t.Errorf("payload of exactly the cap refused: %v", err)
	}
	if err := insert("untyped", []byte("x"), ""); err == nil {
		t.Error("empty mime accepted, want CHECK failure")
	}
}

// TestMigration0026UpgradePathKeepsInstanceSettings proves a pre-0026 database
// upgrades cleanly: the settings it already stored keep their values, the blob
// table arrives empty (nothing is invented as a logo), and the version is
// recorded.
func TestMigration0026UpgradePathKeepsInstanceSettings(t *testing.T) {
	ctx := context.Background()
	pre, err := openDSN(testDSN(t))
	if err != nil {
		t.Fatalf("open pre-0026 db: %v", err)
	}
	pre.db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = pre.db.Close() })
	if err := migrate(ctx, pre.db, pre0026Migrations(t)); err != nil {
		t.Fatalf("migrate pre-0026 db: %v", err)
	}
	if _, err := pre.db.ExecContext(ctx,
		`INSERT INTO settings (key, value) VALUES ('org_name', 'Old Name')`); err != nil {
		t.Fatalf("seed pre-0026 setting: %v", err)
	}

	if err := migrate(ctx, pre.db, migrationsFS); err != nil {
		t.Fatalf("upgrade pre-0026 db: %v", err)
	}

	var name string
	if err := pre.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = 'org_name'`).Scan(&name); err != nil {
		t.Fatalf("read upgraded setting: %v", err)
	}
	if name != "Old Name" {
		t.Errorf("org_name after upgrade = %q, want it untouched", name)
	}
	var n int
	if err := pre.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM settings_blobs`).Scan(&n); err != nil {
		t.Fatalf("read settings_blobs: %v", err)
	}
	if n != 0 {
		t.Errorf("settings_blobs rows after upgrade = %d, want 0 — 0026 invents no logo", n)
	}
	var recorded int
	if err := pre.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version=26`).Scan(&recorded); err != nil {
		t.Fatalf("schema_migrations read: %v", err)
	}
	if recorded != 1 {
		t.Errorf("schema_migrations rows for 26 = %d, want 1", recorded)
	}
	// Every migration number up to 26 is recorded exactly once, so the series
	// has no gap a future migration could collide with.
	var migrations int
	if err := pre.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version <= 26`).Scan(&migrations); err != nil {
		t.Fatalf("schema_migrations count: %v", err)
	}
	if migrations != 26 {
		t.Errorf("schema_migrations rows up to 26 = %d, want 26 (%s)", migrations, fmt.Sprint(migrations))
	}
}
