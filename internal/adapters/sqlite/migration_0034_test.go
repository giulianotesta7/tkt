package sqlite

import (
	"context"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

// Migration 0034 tests (company sign-in — issue #419, S3/S7): a provider
// identity is a row of its own, a first company sign-in can create an account
// that waits for approval without becoming a new role, and an in-flight
// authorization is a single-use row with a deadline. These tests pin the
// pending column and its check, the identity table's shape, unique pairs and
// cascade, the attempt table's primary key, the index the purge reads through,
// the version bookkeeping, and the upgrade path a pre-0034 instance takes.

// pre0034Migrations returns the embedded migrations strictly before 0034, so
// the upgrade-path test can seed a pre-0034 database and then apply the real
// full set (pattern of pre0029Migrations).
func pre0034Migrations(t *testing.T) fstest.MapFS {
	t.Helper()
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		t.Fatalf("read embedded migrations: %v", err)
	}
	out := fstest.MapFS{}
	for _, entry := range entries {
		if strings.Compare(entry.Name(), "0034_company_sign_in.sql") >= 0 {
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

// columnShape is one pragma_table_info row: the persisted type, the NOT NULL
// flag, and the position in the primary key.
type columnShape struct {
	column  string
	wantTyp string
	notNull int
	pk      int
}

// readColumnShapes reads the column shape of one table through the pragma the
// adapter tests assert with.
func readColumnShapes(t *testing.T, s *Store, table string) map[string]columnShape {
	t.Helper()
	rows, err := s.db.Query(`SELECT name, type, "notnull", pk FROM pragma_table_info('` + table + `')`)
	if err != nil {
		t.Fatalf("pragma_table_info(%q): %v", table, err)
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
	return got
}

// assertColumnShapes compares the read shapes against the expected ones.
func assertColumnShapes(t *testing.T, table string, got map[string]columnShape, want []columnShape) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s has %d columns, want %d: %v", table, len(got), len(want), got)
	}
	for _, w := range want {
		g, ok := got[w.column]
		if !ok {
			t.Errorf("column %s missing from %s", w.column, table)
			continue
		}
		if g.wantTyp != w.wantTyp {
			t.Errorf("%s.%s type = %q, want %q", table, w.column, g.wantTyp, w.wantTyp)
		}
		if g.notNull != w.notNull {
			t.Errorf("%s.%s notnull = %d, want %d", table, w.column, g.notNull, w.notNull)
		}
		if g.pk != w.pk {
			t.Errorf("%s.%s pk = %d, want %d", table, w.column, g.pk, w.pk)
		}
	}
}

// TestMigration0034UsersPendingIsAnAddedColumn proves the pending state arrives
// as an appended column with a NOT NULL default, not as a users rebuild: every
// pre-0034 column keeps its position, so an upgraded instance's rows and
// indexes are the ones it already had.
func TestMigration0034UsersPendingIsAnAddedColumn(t *testing.T) {
	s := newTestDB(t)
	got := readColumnShapes(t, s, "users")

	want := map[string]string{
		"id": "INTEGER", "name": "TEXT", "email": "TEXT", "password_hash": "TEXT",
		"active": "INTEGER", "created_at": "TEXT", "role": "TEXT", "pending": "INTEGER",
	}
	if len(got) != len(want) {
		t.Fatalf("users has %d columns, want %d: %v", len(got), len(want), got)
	}
	for column, typ := range want {
		if _, ok := got[column]; !ok {
			t.Errorf("column %s missing from users", column)
			continue
		}
		if got[column].wantTyp != typ {
			t.Errorf("users.%s type = %q, want %q", column, got[column].wantTyp, typ)
		}
	}
	if p := got["pending"]; p.notNull != 1 || p.pk != 0 {
		t.Errorf("users.pending notnull/pk = %d/%d, want 1/0", p.notNull, p.pk)
	}

	// The appended column is last: ALTER TABLE ADD COLUMN appends, a rebuild
	// would renumber the original columns.
	last := ""
	lastCID := -1
	rows, err := s.db.Query(`SELECT name, cid FROM pragma_table_info('users')`)
	if err != nil {
		t.Fatalf("pragma_table_info('users'): %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			name string
			cid  int
		)
		if err := rows.Scan(&name, &cid); err != nil {
			t.Fatal(err)
		}
		if cid > lastCID {
			last, lastCID = name, cid
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if last != "pending" {
		t.Errorf("last users column = %q, want pending — 0034 appends, it does not rebuild", last)
	}

	var dflt string
	if err := s.db.QueryRow(`SELECT dflt_value FROM pragma_table_info('users') WHERE name = 'pending'`).Scan(&dflt); err != nil {
		t.Fatalf("read users.pending default: %v", err)
	}
	if dflt != "0" {
		t.Errorf("users.pending default = %q, want 0", dflt)
	}
}

// TestMigration0034PendingIsInactiveOnly proves the check bounds: pending is
// 0 or 1, and a pending account is never active — a pending row that could
// still sign in would defeat the approval it waits for.
func TestMigration0034PendingIsInactiveOnly(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()

	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO users (name, email, password_hash, active, pending, created_at) VALUES ('Bo', 'bo@example.com', '', 0, 2, ?)`,
		formatTime(testClock)); !isConstraint(err) {
		t.Errorf("pending = 2 accepted (err = %v), want a CHECK failure", err)
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO users (name, email, password_hash, active, pending, created_at) VALUES ('Cy', 'cy@example.com', '', 1, 1, ?)`,
		formatTime(testClock)); !isConstraint(err) {
		t.Errorf("pending = 1 with active = 1 accepted (err = %v), want a CHECK failure", err)
	}

	waiting := seedUser(t, s, "Di", "di@example.com", false)
	if _, err := s.db.ExecContext(ctx, `UPDATE users SET active = 1 WHERE id = ?`, waiting); err != nil {
		t.Fatalf("activate a non-pending account: %v", err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE users SET pending = 1 WHERE id = ?`, waiting); !isConstraint(err) {
		t.Errorf("pending = 1 on an active account accepted (err = %v), want a CHECK failure", err)
	}

	res, err := s.db.ExecContext(ctx,
		`INSERT INTO users (name, email, password_hash, active, pending, created_at) VALUES ('Ed', 'ed@example.com', '', 0, 1, ?)`,
		formatTime(testClock))
	if err != nil {
		t.Fatalf("insert a pending account: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE users SET pending = 0, active = 1 WHERE id = ?`, id); err != nil {
		t.Fatalf("approve a pending account (pending 0 with active 1): %v", err)
	}
	var pending, active int
	if err := s.db.QueryRowContext(ctx, `SELECT pending, active FROM users WHERE id = ?`, id).Scan(&pending, &active); err != nil {
		t.Fatal(err)
	}
	if pending != 0 || active != 1 {
		t.Errorf("approved account pending/active = %d/%d, want 0/1", pending, active)
	}
}

// TestMigration0034UserIdentitiesShape pins the identity link: the account it
// belongs to, the provider, the issuer and subject the account is identified
// by, the email last seen at the provider, and the two instants.
func TestMigration0034UserIdentitiesShape(t *testing.T) {
	s := newTestDB(t)
	got := readColumnShapes(t, s, "user_identities")
	assertColumnShapes(t, "user_identities", got, []columnShape{
		{"id", "INTEGER", 0, 1},
		{"user_id", "INTEGER", 1, 0},
		{"provider", "TEXT", 1, 0},
		{"issuer", "TEXT", 1, 0},
		{"subject", "TEXT", 1, 0},
		{"email", "TEXT", 1, 0},
		{"created_at", "TEXT", 1, 0},
		{"last_sign_in_at", "TEXT", 1, 0},
	})
}

// TestMigration0034UserIdentitiesCascadeWithTheAccount proves the link points
// at a real account and disappears with it, so no identity can outlive the
// account it signs in.
func TestMigration0034UserIdentitiesCascadeWithTheAccount(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	ana := seedUser(t, s, "Ana", "ana@example.com", true)
	bea := seedUser(t, s, "Bea", "bea@example.com", true)

	rows, err := s.db.Query(`SELECT "from", "table", "to", on_delete FROM pragma_foreign_key_list('user_identities')`)
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
		t.Fatalf("user_identities has %d foreign keys, want 1", seen)
	}

	insert := func(userID int64, provider, subject string) error {
		_, err := s.db.ExecContext(ctx,
			`INSERT INTO user_identities (user_id, provider, issuer, subject, email, created_at, last_sign_in_at)
			 VALUES (?, ?, 'https://idp.example.com', ?, 'x@example.com', ?, ?)`,
			userID, provider, subject, formatTime(testClock), formatTime(testClock))
		return err
	}
	if err := insert(ana, "google", "sub-ana"); err != nil {
		t.Fatalf("insert an identity: %v", err)
	}
	if err := insert(bea, "google", "sub-bea"); err != nil {
		t.Fatalf("insert an identity for the other account: %v", err)
	}
	if err := insert(4242, "google", "sub-ghost"); !isForeignKeyViolation(err) {
		t.Errorf("an identity for an account that does not exist accepted (err = %v), want FOREIGN KEY failure", err)
	}

	if _, err := s.db.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, ana); err != nil {
		t.Fatalf("delete the account: %v", err)
	}
	var remaining int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM user_identities WHERE user_id = ?`, ana).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Errorf("identities left after the account was deleted = %d, want 0 (ON DELETE CASCADE)", remaining)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM user_identities WHERE user_id = ?`, bea).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 1 {
		t.Errorf("the other account's identities = %d, want 1 (cascade touches one account only)", remaining)
	}
}

// TestMigration0034UserIdentitiesUniques proves both identity rules: one
// account per provider identity (issuer + subject) and one identity per
// provider per account. The first keeps a repeated sign-in on the same
// account; the second keeps two subjects of one provider from claiming it.
func TestMigration0034UserIdentitiesUniques(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	ana := seedUser(t, s, "Ana", "ana@example.com", true)
	bea := seedUser(t, s, "Bea", "bea@example.com", true)

	insert := func(userID int64, provider, subject string) error {
		_, err := s.db.ExecContext(ctx,
			`INSERT INTO user_identities (user_id, provider, issuer, subject, email, created_at, last_sign_in_at)
			 VALUES (?, ?, 'https://idp.example.com', ?, 'x@example.com', ?, ?)`,
			userID, provider, subject, formatTime(testClock), formatTime(testClock))
		return err
	}
	if err := insert(ana, "google", "sub-ana"); err != nil {
		t.Fatalf("first identity: %v", err)
	}
	if err := insert(bea, "google", "sub-ana"); !isUniqueViolation(err) {
		t.Errorf("the same (issuer, subject) on another account accepted (err = %v), want UNIQUE failure", err)
	}
	if err := insert(ana, "google", "sub-ana-renamed"); !isUniqueViolation(err) {
		t.Errorf("a second Google identity for one account accepted (err = %v), want UNIQUE failure", err)
	}
	if err := insert(ana, "microsoft", "sub-ana-ms"); err != nil {
		t.Errorf("one account linking both providers = %v, want it accepted", err)
	}
}

// TestMigration0034AttemptsShape proves the in-flight authorization is one row
// per state hash with the values the callback needs: the provider, the nonce to
// compare against the ID token, the PKCE verifier, and its deadline.
func TestMigration0034AttemptsShape(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	got := readColumnShapes(t, s, "oidc_sign_in_attempts")
	assertColumnShapes(t, "oidc_sign_in_attempts", got, []columnShape{
		{"state_hash", "TEXT", 1, 1},
		{"provider", "TEXT", 1, 0},
		{"nonce", "TEXT", 1, 0},
		{"code_verifier", "TEXT", 1, 0},
		{"created_at", "TEXT", 1, 0},
		{"expires_at", "TEXT", 1, 0},
	})

	insert := func(stateHash string) error {
		_, err := s.db.ExecContext(ctx,
			`INSERT INTO oidc_sign_in_attempts (state_hash, provider, nonce, code_verifier, created_at, expires_at)
			 VALUES (?, 'google', 'nonce', 'verifier', ?, ?)`,
			stateHash, formatTime(testClock), formatTime(testClock.Add(10*time.Minute)))
		return err
	}
	if err := insert("state-hash-a"); err != nil {
		t.Fatalf("insert an attempt: %v", err)
	}
	if err := insert("state-hash-a"); !isUniqueViolation(err) {
		t.Errorf("a second attempt for the same state hash accepted (err = %v), want UNIQUE failure", err)
	}
	if err := insert("state-hash-b"); err != nil {
		t.Errorf("a second attempt for another state = %v, want it accepted", err)
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO oidc_sign_in_attempts (state_hash, provider, nonce, code_verifier, created_at, expires_at)
		 VALUES ('state-hash-c', 'ldap', 'nonce', 'verifier', ?, ?)`,
		formatTime(testClock), formatTime(testClock)); !isConstraint(err) {
		t.Errorf("attempt for an unsupported provider accepted (err = %v), want a CHECK failure", err)
	}
}

// TestMigration0034AttemptsPurgeIndex proves the index the expired-attempt
// purge reads through exists on the column it compares.
func TestMigration0034AttemptsPurgeIndex(t *testing.T) {
	s := newTestDB(t)
	rows, err := s.db.Query(`SELECT name FROM pragma_index_info('idx_oidc_sign_in_attempts_expires') ORDER BY seqno`)
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
	if got := strings.Join(columns, ","); got != "expires_at" {
		t.Errorf("idx_oidc_sign_in_attempts_expires columns = %q, want %q", got, "expires_at")
	}
}

// TestMigration0034UpgradePathKeepsTheInstance proves a pre-0034 database
// upgrades cleanly: every account it held is not pending (nobody is asked for
// an approval they never applied for), no identity or attempt is invented, the
// version is recorded exactly once, and the series has one row per migration.
func TestMigration0034UpgradePathKeepsTheInstance(t *testing.T) {
	ctx := context.Background()
	pre, err := openDSN(testDSN(t))
	if err != nil {
		t.Fatalf("open pre-0034 db: %v", err)
	}
	pre.db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = pre.db.Close() })
	if err := migrate(ctx, pre.db, pre0034Migrations(t)); err != nil {
		t.Fatalf("migrate pre-0034 db: %v", err)
	}
	ana := seedUser(t, pre, "Ana", "ana@example.com", true)
	seedPasswordSession(t, pre, "sess-before-0034", ana)

	if err := migrate(ctx, pre.db, migrationsFS); err != nil {
		t.Fatalf("upgrade pre-0034 db: %v", err)
	}

	var pending int
	if err := pre.db.QueryRowContext(ctx, `SELECT pending FROM users WHERE id = ?`, ana).Scan(&pending); err != nil {
		t.Fatalf("read the upgraded account: %v", err)
	}
	if pending != 0 {
		t.Errorf("pending after the upgrade = %d, want 0 — an existing account is not waiting for approval", pending)
	}
	var sessions int
	if err := pre.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions WHERE user_id = ?`, ana).Scan(&sessions); err != nil {
		t.Fatal(err)
	}
	if sessions != 1 {
		t.Errorf("sessions after the upgrade = %d, want 1 (0034 signs nobody out)", sessions)
	}
	var identities, attempts int
	if err := pre.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM user_identities`).Scan(&identities); err != nil {
		t.Fatalf("read user_identities: %v", err)
	}
	if identities != 0 {
		t.Errorf("user_identities rows after the upgrade = %d, want 0 — 0034 invents no link", identities)
	}
	if err := pre.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM oidc_sign_in_attempts`).Scan(&attempts); err != nil {
		t.Fatalf("read oidc_sign_in_attempts: %v", err)
	}
	if attempts != 0 {
		t.Errorf("oidc_sign_in_attempts rows after the upgrade = %d, want 0", attempts)
	}

	var recorded int
	if err := pre.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version = 34`).Scan(&recorded); err != nil {
		t.Fatalf("schema_migrations read: %v", err)
	}
	if recorded != 1 {
		t.Errorf("schema_migrations rows for 34 = %d, want 1", recorded)
	}
	entries, err := fs.Glob(migrationsFS, "migrations/*.sql")
	if err != nil {
		t.Fatalf("list embedded migrations: %v", err)
	}
	var applied int
	if err := pre.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&applied); err != nil {
		t.Fatalf("schema_migrations count: %v", err)
	}
	if applied != len(entries) {
		t.Errorf("schema_migrations rows = %d, want one per embedded migration (%d)", applied, len(entries))
	}
}
