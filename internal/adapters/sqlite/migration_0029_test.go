package sqlite

import (
	"context"
	"io/fs"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

// Migration 0029 tests (self-service password recovery — issue #414, S3/S8): a
// reset link is stored as its hash with the instants that bound it, so the raw
// token never reaches the database, and an account that disappears takes its
// links with it. These tests pin the table's shape, the foreign key and its
// cascade, the one-row-per-hash rule, the index the throttle reads through, and
// the upgrade path a pre-0029 instance takes.

// pre0029Migrations returns the embedded migrations strictly before 0029, so
// the upgrade-path test can seed a pre-0029 database and then apply the real
// full set (pattern of pre0026Migrations).
func pre0029Migrations(t *testing.T) fstest.MapFS {
	t.Helper()
	return migrationsBefore(t, "0029_password_reset_tokens.sql", false)
}

// through0029Migrations is the embedded set up to and including 0029, so a
// comparison with pre0029Migrations isolates what 0029 itself changed even when
// later migrations exist.
func through0029Migrations(t *testing.T) fstest.MapFS {
	t.Helper()
	return migrationsBefore(t, "0029_password_reset_tokens.sql", true)
}

// migrationsBefore copies the embedded migrations that sort before name, plus
// name itself when inclusive is set.
func migrationsBefore(t *testing.T, name string, inclusive bool) fstest.MapFS {
	t.Helper()
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		t.Fatalf("read embedded migrations: %v", err)
	}
	out := fstest.MapFS{}
	for _, entry := range entries {
		cmp := strings.Compare(entry.Name(), name)
		if cmp > 0 || (cmp == 0 && !inclusive) {
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

// TestMigration0029PasswordResetTokensShape pins the six columns and their
// nullability: the owner, the stored hash, the two instants that bound the
// link, and the instant it was spent — the only nullable one.
func TestMigration0029PasswordResetTokensShape(t *testing.T) {
	s := newTestDB(t)

	type columnShape struct {
		column  string
		wantTyp string
		notNull int
		pk      int
	}
	want := []columnShape{
		{"id", "INTEGER", 0, 1},
		{"user_id", "INTEGER", 1, 0},
		{"token_hash", "TEXT", 1, 0},
		{"created_at", "TEXT", 1, 0},
		{"expires_at", "TEXT", 1, 0},
		{"used_at", "TEXT", 0, 0},
	}
	rows, err := s.db.Query(`SELECT name, type, "notnull", pk FROM pragma_table_info('password_reset_tokens')`)
	if err != nil {
		t.Fatalf("pragma_table_info('password_reset_tokens'): %v", err)
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
		t.Errorf("password_reset_tokens has %d columns, want %d: %v", len(got), len(want), got)
	}
	for _, w := range want {
		g, ok := got[w.column]
		if !ok {
			t.Errorf("column %s missing after 0029", w.column)
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

// TestMigration0029PasswordResetTokensCascadeWithTheAccount proves the owner is
// a real user row and that deleting the account deletes its links: a token that
// outlived its user would be a link nobody can redeem.
func TestMigration0029PasswordResetTokensCascadeWithTheAccount(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	ana := seedUser(t, s, "Ana", "ana@example.com", true)
	bea := seedUser(t, s, "Bea", "bea@example.com", true)

	rows, err := s.db.Query(`SELECT "from", "table", "to", on_delete FROM pragma_foreign_key_list('password_reset_tokens')`)
	if err != nil {
		t.Fatalf("foreign key list: %v", err)
	}
	defer rows.Close()
	seen := 0
	for rows.Next() {
		var from, table, to, onDelete string
		if err := rows.Scan(&from, &table, &to, &onDelete); err != nil {
			t.Fatal(err)
		}
		seen++
		if from != "user_id" || table != "users" || to != "id" {
			t.Errorf("foreign key %s -> %s(%s), want user_id -> users(id)", from, table, to)
		}
		if onDelete != "CASCADE" {
			t.Errorf("ON DELETE = %q, want CASCADE", onDelete)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if seen != 1 {
		t.Fatalf("password_reset_tokens has %d foreign keys, want 1", seen)
	}

	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO password_reset_tokens (user_id, token_hash, created_at, expires_at) VALUES (?, 'hash-ana', ?, ?)`,
		ana, formatTime(testClock), formatTime(testClock.Add(time.Hour))); err != nil {
		t.Fatalf("insert a token for the account: %v", err)
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO password_reset_tokens (user_id, token_hash, created_at, expires_at) VALUES (?, 'hash-bea', ?, ?)`,
		bea, formatTime(testClock), formatTime(testClock.Add(time.Hour))); err != nil {
		t.Fatalf("insert a token for the other account: %v", err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO password_reset_tokens (user_id, token_hash, created_at, expires_at) VALUES (4242, 'hash-ghost', ?, ?)`,
		formatTime(testClock), formatTime(testClock.Add(time.Hour))); err == nil {
		t.Error("a token for an account that does not exist was accepted, want FOREIGN KEY failure")
	}

	if _, err := s.db.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, ana); err != nil {
		t.Fatalf("delete the account: %v", err)
	}
	var remaining int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM password_reset_tokens WHERE user_id = ?`, ana).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Errorf("tokens left after the account was deleted = %d, want 0 (ON DELETE CASCADE)", remaining)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM password_reset_tokens WHERE user_id = ?`, bea).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 1 {
		t.Errorf("the other account's tokens = %d, want 1 (cascade touches one account only)", remaining)
	}
}

// TestMigration0029PasswordResetTokensHashIsUnique proves the stored hash is
// unique across the table, so one link can never be two rows and a lookup by
// hash can never pick the wrong account.
func TestMigration0029PasswordResetTokensHashIsUnique(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	ana := seedUser(t, s, "Ana", "ana@example.com", true)
	bea := seedUser(t, s, "Bea", "bea@example.com", true)

	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO password_reset_tokens (user_id, token_hash, created_at, expires_at) VALUES (?, 'same-hash', ?, ?)`,
		ana, formatTime(testClock), formatTime(testClock.Add(time.Hour))); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO password_reset_tokens (user_id, token_hash, created_at, expires_at) VALUES (?, 'same-hash', ?, ?)`,
		bea, formatTime(testClock), formatTime(testClock.Add(time.Hour))); err == nil {
		t.Error("a second row with the same token_hash was accepted, want UNIQUE failure")
	}
}

// TestMigration0029PasswordResetTokensIndex proves the index the throttle read
// goes through exists with the columns it reads: the newest link of one account.
func TestMigration0029PasswordResetTokensIndex(t *testing.T) {
	s := newTestDB(t)

	rows, err := s.db.Query(`SELECT name FROM pragma_index_info('idx_password_reset_tokens_user_created') ORDER BY seqno`)
	if err != nil {
		t.Fatalf("pragma_index_info: %v", err)
	}
	defer rows.Close()
	var columns []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		columns = append(columns, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(columns, ","); got != "user_id,created_at" {
		t.Errorf("idx_password_reset_tokens_user_created columns = %q, want %q", got, "user_id,created_at")
	}
}

// TestMigration0029UpgradePathKeepsTheInstance proves a pre-0029 database
// upgrades cleanly: the accounts and sessions it already held are untouched,
// the new table arrives empty (no link is invented), and the version is
// recorded exactly once.
func TestMigration0029UpgradePathKeepsTheInstance(t *testing.T) {
	ctx := context.Background()
	pre, err := openDSN(testDSN(t))
	if err != nil {
		t.Fatalf("open pre-0029 db: %v", err)
	}
	pre.db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = pre.db.Close() })
	if err := migrate(ctx, pre.db, pre0029Migrations(t)); err != nil {
		t.Fatalf("migrate pre-0029 db: %v", err)
	}
	ana := seedUser(t, pre, "Ana", "ana@example.com", true)
	seedPasswordSession(t, pre, "sess-before-0029", ana)
	var before string
	if err := pre.db.QueryRowContext(ctx, `SELECT password_hash FROM users WHERE id = ?`, ana).Scan(&before); err != nil {
		t.Fatalf("read the pre-0029 account: %v", err)
	}

	if err := migrate(ctx, pre.db, migrationsFS); err != nil {
		t.Fatalf("upgrade pre-0029 db: %v", err)
	}

	var after string
	if err := pre.db.QueryRowContext(ctx, `SELECT password_hash FROM users WHERE id = ?`, ana).Scan(&after); err != nil {
		t.Fatalf("read the upgraded account: %v", err)
	}
	if after != before {
		t.Errorf("stored hash after the upgrade = %q, want it untouched", after)
	}
	var sessions int
	if err := pre.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions WHERE user_id = ?`, ana).Scan(&sessions); err != nil {
		t.Fatal(err)
	}
	if sessions != 1 {
		t.Errorf("sessions after the upgrade = %d, want 1 (0029 signs nobody out)", sessions)
	}
	var tokens int
	if err := pre.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM password_reset_tokens`).Scan(&tokens); err != nil {
		t.Fatalf("read password_reset_tokens: %v", err)
	}
	if tokens != 0 {
		t.Errorf("password_reset_tokens rows after the upgrade = %d, want 0 — 0029 invents no link", tokens)
	}
	var recorded int
	if err := pre.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version = 29`).Scan(&recorded); err != nil {
		t.Fatalf("schema_migrations read: %v", err)
	}
	if recorded != 1 {
		t.Errorf("schema_migrations rows for 29 = %d, want 1", recorded)
	}
	var migrations int
	if err := pre.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version <= 29`).Scan(&migrations); err != nil {
		t.Fatalf("schema_migrations count: %v", err)
	}
	if migrations != 29 {
		t.Errorf("schema_migrations rows up to 29 = %d, want 29 (the series has no gap)", migrations)
	}
}

// schemaOutsideResetTable dumps every schema object except the reset-token table
// and its index, so a test can prove the migration changed nothing else. Objects
// come in a deterministic order, so two dumps compare directly.
func schemaOutsideResetTable(t *testing.T, s *Store) []string {
	t.Helper()
	rows, err := s.db.Query(`SELECT type||'|'||name||'|'||COALESCE(tbl_name,'')||'|'||COALESCE(sql,'')
		FROM sqlite_master
		WHERE name <> 'password_reset_tokens' AND tbl_name <> 'password_reset_tokens'
		  AND name NOT LIKE 'idx_password_reset_tokens%'
		  AND name <> 'schema_migrations' AND tbl_name <> 'schema_migrations'
		ORDER BY type, name`)
	if err != nil {
		t.Fatalf("schema dump: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var object string
		if err := rows.Scan(&object); err != nil {
			t.Fatal(err)
		}
		out = append(out, object)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("schema dump: %v", err)
	}
	return out
}

// TestMigration0029LeavesEveryOtherSchemaObjectUntouched proves the migration is
// scoped to its own table: every object a pre-0029 instance already had survives
// byte-identically, and no other table gains a column or a trigger that mentions
// the reset links.
func TestMigration0029LeavesEveryOtherSchemaObjectUntouched(t *testing.T) {
	ctx := context.Background()
	pre, err := openDSN(testDSN(t))
	if err != nil {
		t.Fatalf("open pre-0029 db: %v", err)
	}
	pre.db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = pre.db.Close() })
	if err := migrate(ctx, pre.db, pre0029Migrations(t)); err != nil {
		t.Fatalf("migrate pre-0029 db: %v", err)
	}
	before := schemaOutsideResetTable(t, pre)

	upgraded, err := openDSN(testDSN(t))
	if err != nil {
		t.Fatalf("open upgraded db: %v", err)
	}
	upgraded.db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = upgraded.db.Close() })
	if err := migrate(ctx, upgraded.db, through0029Migrations(t)); err != nil {
		t.Fatalf("migrate through 0029: %v", err)
	}

	if after := schemaOutsideResetTable(t, upgraded); !reflect.DeepEqual(after, before) {
		t.Errorf("0029 changed schema objects outside its own table:\nbefore=%v\nafter=%v", before, after)
	}
	var created string
	if err := upgraded.db.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE name = 'password_reset_tokens'`).Scan(&created); err != nil {
		t.Fatalf("the upgraded schema lacks the reset-token table: %v", err)
	}
	if !strings.Contains(created, "CREATE TABLE password_reset_tokens") {
		t.Errorf("unexpected table definition: %q", created)
	}
}
