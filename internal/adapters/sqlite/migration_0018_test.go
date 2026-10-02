package sqlite

import (
	"context"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
)

// Migration 0018 tests (per-user preferences — issue #210): the
// user_preferences table shape, its composite (user_id, key) primary key,
// the ON DELETE CASCADE onto users, the pre-0018 upgrade path, and the
// version bookkeeping. Written against the numbered-series conventions of
// migration_0017_test.go.

// pre0018Migrations returns the embedded migrations strictly before 0018,
// so the upgrade-path test can seed a pre-0018 database and then apply the
// real full set (pattern of pre0017Migrations).
func pre0018Migrations(t *testing.T) fstest.MapFS {
	t.Helper()
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		t.Fatalf("read embedded migrations: %v", err)
	}
	out := fstest.MapFS{}
	for _, entry := range entries {
		if strings.Compare(entry.Name(), "0018_user_preferences.sql") >= 0 {
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

// TestMigration0018TableAndColumns proves the user_preferences table exists
// with its three columns.
func TestMigration0018TableAndColumns(t *testing.T) {
	s := newTestDB(t)

	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='user_preferences'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("table user_preferences missing after 0018")
	}
	for _, col := range []string{"user_id", "key", "value"} {
		if !columnExists(t, s, "user_preferences", col) {
			t.Errorf("user_preferences.%s column missing after 0018", col)
		}
	}
}

// TestMigration0018CompositePrimaryKey proves the key is the PAIR
// (user_id, key) in that order and that both halves are NOT NULL: a second
// row with the same pair is rejected, while a second key for the same user
// is allowed.
func TestMigration0018CompositePrimaryKey(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()

	for _, tc := range []struct {
		column      string
		wantPK      int
		wantNotNull int
	}{
		{"user_id", 1, 1},
		{"key", 2, 1},
		{"value", 0, 1},
	} {
		var pk, notNull int
		if err := s.db.QueryRow(
			`SELECT pk, "notnull" FROM pragma_table_info('user_preferences') WHERE name = ?`, tc.column,
		).Scan(&pk, &notNull); err != nil {
			t.Fatalf("pragma_table_info(user_preferences) %s: %v", tc.column, err)
		}
		if pk != tc.wantPK {
			t.Errorf("column %s pk = %d, want %d", tc.column, pk, tc.wantPK)
		}
		if notNull != tc.wantNotNull {
			t.Errorf("column %s notnull = %d, want %d", tc.column, notNull, tc.wantNotNull)
		}
	}

	userID := seedUser(t, s, "Prefs", "prefs@example.com", true)
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO user_preferences (user_id, "key", value) VALUES (?, 'queue_order', 'priority')`, userID); err != nil {
		t.Fatalf("insert preference: %v", err)
	}
	// The same (user_id, key) is a PRIMARY KEY violation: one value per pair.
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO user_preferences (user_id, "key", value) VALUES (?, 'queue_order', 'newest')`, userID); err == nil {
		t.Fatal("second user_preferences insert for the same (user_id, key) succeeded, want PRIMARY KEY failure")
	}
	// A DIFFERENT key for the same user is allowed — the PK is the pair.
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO user_preferences (user_id, "key", value) VALUES (?, 'other', 'x')`, userID); err != nil {
		t.Fatalf("second key for the same user rejected: %v", err)
	}
}

// TestMigration0018UserCascade proves the FK both exists and is declared ON
// DELETE CASCADE: deleting a user must take their preferences with them.
func TestMigration0018UserCascade(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()

	var table, from, to, onDelete string
	if err := s.db.QueryRow(`SELECT "table", "from", "to", on_delete FROM pragma_foreign_key_list(?) WHERE "from" = 'user_id'`, "user_preferences").
		Scan(&table, &from, &to, &onDelete); err != nil {
		t.Fatalf("pragma_foreign_key_list(user_preferences): %v", err)
	}
	if table != "users" || to != "id" {
		t.Errorf("user_preferences FK = (%s, %s, %s), want (users, user_id, id)", table, from, to)
	}
	if onDelete != "CASCADE" {
		t.Fatalf("user_preferences on_delete = %q, want CASCADE", onDelete)
	}

	// Behavioural proof: a deleted user takes their preference row with it.
	userID := seedUser(t, s, "Cascade", "cascade@example.com", true)
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO user_preferences (user_id, "key", value) VALUES (?, 'queue_order', 'priority')`, userID); err != nil {
		t.Fatalf("insert preference: %v", err)
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, userID); err != nil {
		t.Fatalf("delete user: %v", err)
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM user_preferences WHERE user_id = ?`, userID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("user_preferences rows after user delete = %d, want 0 (cascade)", n)
	}
}

// TestMigration0018UpgradePath proves a pre-0018 database upgrades cleanly:
// the table is absent before, created after, the pre-existing user survives,
// a preference can be written, and version 18 is recorded.
func TestMigration0018UpgradePath(t *testing.T) {
	ctx := context.Background()
	pre, err := openDSN(testDSN(t))
	if err != nil {
		t.Fatalf("open pre-0018 db: %v", err)
	}
	pre.db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = pre.db.Close() })
	if err := migrate(ctx, pre.db, pre0018Migrations(t)); err != nil {
		t.Fatalf("migrate pre-0018 db: %v", err)
	}

	var before int
	if err := pre.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='user_preferences'`).Scan(&before); err != nil {
		t.Fatalf("inspect pre-0018 schema: %v", err)
	}
	if before != 0 {
		t.Fatalf("user_preferences existed before 0018 (%d), want 0", before)
	}

	userID := seedUser(t, pre, "Pre", "pre@example.com", true)

	if err := migrate(ctx, pre.db, migrationsFS); err != nil {
		t.Fatalf("upgrade pre-0018 db: %v", err)
	}

	var n int
	if err := pre.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='user_preferences'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("user_preferences missing after upgrade")
	}
	if _, err := pre.db.ExecContext(ctx,
		`INSERT INTO user_preferences (user_id, "key", value) VALUES (?, 'queue_order', 'priority')`, userID); err != nil {
		t.Fatalf("insert preference after upgrade: %v", err)
	}
	var recorded int
	if err := pre.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version=18`).Scan(&recorded); err != nil {
		t.Fatalf("schema_migrations read: %v", err)
	}
	if recorded != 1 {
		t.Errorf("schema_migrations version=18 rows = %d, want 1", recorded)
	}
}

// TestMigration0018VersionBookkeeping proves the applied version is 18.
func TestMigration0018VersionBookkeeping(t *testing.T) {
	s := newTestDB(t)
	var applied int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version=18`).Scan(&applied); err != nil {
		t.Fatalf("schema_migrations read: %v", err)
	}
	if applied != 1 {
		t.Fatalf("schema_migrations version=18 rows = %d, want 1", applied)
	}
}
