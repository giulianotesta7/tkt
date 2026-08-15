package sqlite

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
)

// Tests for Open, the migration runner, and the test-database helper
// (task 4.1). They run against the real modernc driver on shared-cache
// in-memory databases (design "SQLite Schema": file::memory + cache=shared
// + SetMaxOpenConns(1), unique per test so pools never share rows).

var testDBCounter int64

// testDSN builds an isolated named shared-cache in-memory DSN. The unique
// name per test keeps pools from different tests from sharing the same
// process-wide memory database (the classic "no such table" flake source).
func testDSN(t *testing.T) string {
	t.Helper()
	name := fmt.Sprintf("tkt-mvp-test-%d", atomic.AddInt64(&testDBCounter, 1))
	return "file:" + name + "?mode=memory&cache=shared&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_txlock=immediate"
}

// newTestDB opens the shared-cache memory database, applies ALL embedded
// migrations, and keeps a single connection pool (design: single-pool
// semantics → no "no such table" flakes).
func newTestDB(t *testing.T) *Store {
	t.Helper()
	s, err := openDSN(testDSN(t))
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	s.db.SetMaxOpenConns(1)
	t.Cleanup(func() { s.db.Close() })
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate test db: %v", err)
	}
	return s
}

// seedCategory inserts a category directly (test arrange) and returns its id.
func seedCategory(t *testing.T, s *Store, name string) int64 {
	t.Helper()
	res, err := s.db.ExecContext(context.Background(),
		`INSERT INTO categories (name, created_at) VALUES (?, ?)`,
		name, "2026-08-06T10:00:00Z")
	if err != nil {
		t.Fatalf("seed category %q: %v", name, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("seed category %q: last insert id: %v", name, err)
	}
	return id
}

func TestOpenAppliesSingleDSN(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.db.Close() })

	var fk int
	if err := s.db.QueryRow(`PRAGMA foreign_keys`).Scan(&fk); err != nil {
		t.Fatalf("read foreign_keys: %v", err)
	}
	if fk != 1 {
		t.Errorf("foreign_keys = %d, want 1 (D1 pragma via DSN)", fk)
	}

	var jm string
	if err := s.db.QueryRow(`PRAGMA journal_mode`).Scan(&jm); err != nil {
		t.Fatalf("read journal_mode: %v", err)
	}
	if jm != "wal" {
		t.Errorf("journal_mode = %q, want %q", jm, "wal")
	}

	var bt int
	if err := s.db.QueryRow(`PRAGMA busy_timeout`).Scan(&bt); err != nil {
		t.Fatalf("read busy_timeout: %v", err)
	}
	if bt != 5000 {
		t.Errorf("busy_timeout = %d, want 5000", bt)
	}
}

func TestOpenFailsOnUnopenablePath(t *testing.T) {
	// A path under a missing directory cannot be created: open must error,
	// not leave a half-initialized store behind.
	if _, err := Open(filepath.Join(t.TempDir(), "missing", "app.db")); err == nil {
		t.Fatal("Open succeeded, want error for unopenable path")
	}
}

func TestMigrateCreatesSchema(t *testing.T) {
	s := newTestDB(t)

	want := []string{
		"users", "sessions", "categories", "tickets",
		"comments", "audit_events", "groups", "group_members",
		"role_changes", "schema_migrations",
	}
	for _, name := range want {
		var n int
		if err := s.db.QueryRow(
			`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, name,
		).Scan(&n); err != nil {
			t.Fatalf("check table %s: %v", name, err)
		}
		if n != 1 {
			t.Errorf("table %s missing after migrate", name)
		}
	}

	var applied int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&applied); err != nil {
		t.Fatalf("schema_migrations: %v", err)
	}
	if applied != 3 {
		t.Errorf("schema_migrations rows = %d, want 3 (0001_init, 0002_fts, 0003_roles_and_views)", applied)
	}

	rows, err := s.db.Query(`SELECT version FROM schema_migrations ORDER BY version`)
	if err != nil {
		t.Fatalf("read versions: %v", err)
	}
	defer rows.Close()
	var versions []int
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		versions = append(versions, v)
	}
	if len(versions) != 3 || versions[0] != 1 || versions[1] != 2 || versions[2] != 3 {
		t.Errorf("versions = %v, want [1 2 3]", versions)
	}
}

func TestMigrateRerunIsNoOp(t *testing.T) {
	s := newTestDB(t)
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("rerun Migrate: %v", err)
	}
	var applied int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&applied); err != nil {
		t.Fatalf("schema_migrations: %v", err)
	}
	if applied != 3 {
		t.Errorf("rerun recorded %d versions, want 3 (no-op)", applied)
	}
}

func TestMigrateTransactionalRollback(t *testing.T) {
	s, err := openDSN(testDSN(t))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.db.Close() })

	// A broken migration: the first statement creates a table, the second
	// is invalid SQL. A non-transactional runner would leave tkt_broken
	// behind; the runner must roll the whole migration back.
	broken := fstest.MapFS{
		"migrations/0001_broken.sql": &fstest.MapFile{Data: []byte(
			"CREATE TABLE tkt_broken (id INTEGER);\nTHIS IS NOT SQL;")},
	}
	if err := migrate(context.Background(), s.db, broken); err == nil {
		t.Fatal("migrate succeeded, want error from broken migration")
	}

	var n int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='tkt_broken'`,
	).Scan(&n); err != nil {
		t.Fatalf("check partial schema: %v", err)
	}
	if n != 0 {
		t.Error("broken migration left tkt_broken behind — apply is not transactional")
	}

	var recorded int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&recorded); err != nil {
		t.Fatalf("schema_migrations: %v", err)
	}
	if recorded != 0 {
		t.Errorf("broken migration recorded %d versions, want 0", recorded)
	}
}

func TestMigrationVersion(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		want    int
		wantErr bool
	}{
		{name: "zero padded", path: "migrations/0007_add_index.sql", want: 7},
		{name: "single digit", path: "migrations/2_next.sql", want: 2},
		{name: "missing version", path: "migrations/init.sql", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := migrationVersion(tt.path)
			if (err != nil) != tt.wantErr {
				t.Fatalf("migrationVersion(%q) error = %v, wantErr %v", tt.path, err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("migrationVersion(%q) = %d, want %d", tt.path, got, tt.want)
			}
		})
	}
}

func TestMigrateRejectsInvalidFilenameBeforeApplying(t *testing.T) {
	s, err := openDSN(testDSN(t))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.db.Close() })

	migrations := fstest.MapFS{
		"migrations/init.sql": &fstest.MapFile{Data: []byte("CREATE TABLE should_not_exist (id INTEGER)")},
	}
	if err := migrate(context.Background(), s.db, migrations); err == nil {
		t.Fatal("migrate accepted a filename without a leading version")
	}
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name = 'should_not_exist'`).Scan(&count); err != nil {
		t.Fatalf("check unapplied migration: %v", err)
	}
	if count != 0 {
		t.Fatal("invalidly named migration was applied")
	}
}

func TestMigratePropagatesDatabaseFailure(t *testing.T) {
	s, err := openDSN(testDSN(t))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := s.db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := migrate(context.Background(), s.db, fstest.MapFS{}); err == nil || !strings.Contains(err.Error(), "bootstrap schema_migrations") {
		t.Fatalf("migrate closed database error = %v, want bootstrap context", err)
	}
}

func TestMigrationAppliedPropagatesDatabaseFailure(t *testing.T) {
	s, err := openDSN(testDSN(t))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := s.db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := migrationApplied(context.Background(), s.db, 1); err == nil || !strings.Contains(err.Error(), "check version 1") {
		t.Fatalf("migrationApplied closed database error = %v, want version context", err)
	}
}

func TestMigrateReportsUnreadableMigration(t *testing.T) {
	s, err := openDSN(testDSN(t))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.db.Close() })

	migrations := fstest.MapFS{
		"migrations/0001_unreadable.sql": &fstest.MapFile{Mode: fs.ModeDir},
	}
	if err := migrate(context.Background(), s.db, migrations); err == nil || !strings.Contains(err.Error(), "read migrations/0001_unreadable.sql") {
		t.Fatalf("unreadable migration error = %v", err)
	}
}

func TestForeignKeyEnforced(t *testing.T) {
	s := newTestDB(t)

	// Bad category_id must be rejected by the FK pragma, not silently stored.
	_, err := s.db.ExecContext(context.Background(),
		`INSERT INTO tickets (number, title, description, requester_name, requester_email, category_id, priority, state, created_at, updated_at)
		 VALUES (1, 'bad category', '', 'r', 'e', 999, 'medium', 'new', '2026-08-06T10:00:00Z', '2026-08-06T10:00:00Z')`)
	if err == nil {
		t.Fatal("insert with bad category_id succeeded, want FK error")
	}
	if !strings.Contains(err.Error(), "FOREIGN KEY constraint failed") {
		t.Errorf("error = %v, want FOREIGN KEY constraint failed", err)
	}

	// The same insert against a real category must succeed.
	catID := seedCategory(t, s, "Bugs")
	res, err := s.db.ExecContext(context.Background(),
		`INSERT INTO tickets (number, title, description, requester_name, requester_email, category_id, priority, state, created_at, updated_at)
		 VALUES (2, 'valid', '', 'r', 'e', ?, 'medium', 'new', '2026-08-06T10:00:00Z', '2026-08-06T10:00:00Z')`,
		catID)
	if err != nil {
		t.Fatalf("insert with valid category_id: %v", err)
	}
	if id, err := res.LastInsertId(); err != nil || id == 0 {
		t.Errorf("valid insert id = %d, err = %v; want non-zero", id, err)
	}
}

func TestSharedCacheNoSchemaFlakes(t *testing.T) {
	// Two pools on the SAME named shared-cache memory database: the schema
	// migrated through one pool must be visible through the other. This is
	// the single-pool semantics the design requires — a private per-conn
	// memory database would answer "no such table" here.
	dsn := testDSN(t)
	a, err := openDSN(dsn)
	if err != nil {
		t.Fatalf("open pool A: %v", err)
	}
	t.Cleanup(func() { a.db.Close() })
	b, err := openDSN(dsn)
	if err != nil {
		t.Fatalf("open pool B: %v", err)
	}
	t.Cleanup(func() { b.db.Close() })

	if err := a.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate via pool A: %v", err)
	}
	var n int
	if err := b.db.QueryRow(`SELECT COUNT(*) FROM tickets`).Scan(&n); err != nil {
		t.Fatalf("read via pool B: %v", err)
	}
	if n != 0 {
		t.Errorf("tickets via pool B = %d, want 0 (fresh shared db)", n)
	}
}

// TestStorePingClose covers the phase 6 composition-root surface: Ping
// reports a live connection and Close releases it (RDD follow-up surfaced
// by Phase 6).
func TestStorePingClose(t *testing.T) {
	s := newTestDB(t)

	if err := s.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// A second Close is a no-op error in modernc sqlite; Ping after close
	// must fail — the composition root defer must not mask a released db.
	if err := s.Ping(context.Background()); err == nil {
		t.Fatal("Ping after Close must fail")
	}
}
