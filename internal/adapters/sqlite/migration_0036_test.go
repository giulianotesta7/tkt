package sqlite

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// Migration 0036 tests (root credentials — issue #414): the root account
// rotates or recovers its own password, and nothing else about it moves. The
// guard migration 0003 installed refused EVERY update to a root row, so the two
// password paths (the Preferences change and the recovery link) could not store
// their hash. This migration narrows that one guard to permit the password hash
// alone; every other field, the identity included, stays immutable, a mixed
// update is refused whole, and root can still never be deleted.

// pre0036Migrations is the embedded set strictly before 0036, so the upgrade
// test can build the schema an existing instance has, seed it, and then apply the
// real full set over those rows.
func pre0036Migrations(t *testing.T) fstest.MapFS {
	t.Helper()
	return migrationsBefore(t, "0036_root_password_rotation.sql", false)
}

// storedUserRow is one users row as the guards see it: every column migration
// 0036 has to protect, so a test can compare the row before and after a refused
// write instead of checking one field.
type storedUserRow struct {
	id           int64
	name         string
	email        string
	passwordHash string
	active       int
	role         string
	createdAt    string
}

// readStoredUser reads every column of one user row.
func readStoredUser(t *testing.T, s *Store, id int64) storedUserRow {
	t.Helper()
	var row storedUserRow
	if err := s.db.QueryRow(`SELECT id, name, email, password_hash, active, role, created_at FROM users WHERE id = ?`, id).
		Scan(&row.id, &row.name, &row.email, &row.passwordHash, &row.active, &row.role, &row.createdAt); err != nil {
		t.Fatalf("read user %d: %v", id, err)
	}
	return row
}

// schemaObjects dumps every schema object except the migration bookkeeping, so a
// test can prove a migration changed exactly the one object it is allowed to.
func schemaObjects(t *testing.T, s *Store) []string {
	t.Helper()
	rows, err := s.db.Query(`SELECT type||'|'||name||'|'||COALESCE(tbl_name,'')||'|'||COALESCE(sql,'')
		FROM sqlite_master WHERE name <> 'schema_migrations' ORDER BY type, name`)
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

// assertRootImmutabilityRefused runs one update that must be refused by the root
// guard, and proves the root row is byte-identical afterwards.
func assertRootImmutabilityRefused(t *testing.T, s *Store, name, sql string, args ...any) {
	t.Helper()
	before := readStoredUser(t, s, args[len(args)-1].(int64))
	_, err := s.db.Exec(sql, args...)
	if err == nil {
		t.Fatalf("%s: root update succeeded, want the immutability guard to abort it", name)
	}
	if !strings.Contains(err.Error(), "root account is immutable") {
		t.Errorf("%s: error = %v, want the root immutability refusal", name, err)
	}
	if after := readStoredUser(t, s, before.id); after != before {
		t.Errorf("%s: the refused write changed the root row:\nbefore=%+v\nafter=%+v", name, before, after)
	}
}

// TestMigration0036RootPasswordRotation proves the allowed half and the guarded
// half on the full schema: the password hash may change, every other root field
// and the root identity may not, a mixed update is refused whole, root can never
// be deleted, and an ordinary account keeps its own writes.
func TestMigration0036RootPasswordRotation(t *testing.T) {
	ctx := context.Background()
	s := newTestDB(t)

	rootID := seedUserRaw(t, s, "Root", "root@example.com", "root")
	agentID := seedUserRole(t, s, "Agent", "agent@example.com", true, domain.RoleAgent)
	before := readStoredUser(t, s, rootID)

	// Root rotates its own credential.
	if _, err := s.db.ExecContext(ctx, `UPDATE users SET password_hash = 'rotated-hash' WHERE id = ?`, rootID); err != nil {
		t.Fatalf("root password update = %v, want success", err)
	}
	rotated := readStoredUser(t, s, rootID)
	if rotated.passwordHash != "rotated-hash" {
		t.Errorf("root password hash = %q, want the rotated one", rotated.passwordHash)
	}
	if rotated.id != before.id || rotated.name != before.name || rotated.email != before.email ||
		rotated.active != before.active || rotated.role != before.role || rotated.createdAt != before.createdAt {
		t.Errorf("the allowed password write moved another root field:\nbefore=%+v\nafter=%+v", before, rotated)
	}

	// Every other field, the identity included, stays immutable.
	for _, refused := range []struct {
		name string
		sql  string
		args []any
	}{
		{"rename", `UPDATE users SET name = 'Hacker' WHERE id = ?`, []any{rootID}},
		{"change email", `UPDATE users SET email = 'hack@example.com' WHERE id = ?`, []any{rootID}},
		{"deactivate", `UPDATE users SET active = 0 WHERE id = ?`, []any{rootID}},
		{"demote", `UPDATE users SET role = 'admin' WHERE id = ?`, []any{rootID}},
		{"rewrite created_at", `UPDATE users SET created_at = '2020-01-01T00:00:00Z' WHERE id = ?`, []any{rootID}},
		{"move the identity", `UPDATE users SET id = ? WHERE id = ?`, []any{rootID + 1000, rootID}},
		{"rotate and rename together", `UPDATE users SET password_hash = 'sneaky-hash', name = 'Hacker' WHERE id = ?`, []any{rootID}},
	} {
		assertRootImmutabilityRefused(t, s, refused.name, refused.sql, refused.args...)
	}

	// Root cannot be deleted either.
	if _, err := s.db.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, rootID); err == nil {
		t.Fatal("root delete succeeded, want the immutability guard to abort it")
	}
	if after := readStoredUser(t, s, rootID); after != rotated {
		t.Errorf("the refused delete changed the root row:\nbefore=%+v\nafter=%+v", rotated, after)
	}

	// The guards this migration does not own are still in place.
	for _, object := range []string{
		"trg_users_root_immutable_update",
		"trg_users_root_immutable_delete",
		"trg_users_no_desk_member_downgrade",
	} {
		assertSchemaObjectPresent(t, s, object)
	}

	// An ordinary account keeps its own writes: the narrowed guard belongs to
	// the root row alone.
	if _, err := s.db.ExecContext(ctx, `UPDATE users SET password_hash = 'agent-hash' WHERE id = ?`, agentID); err != nil {
		t.Fatalf("non-root password update = %v, want success", err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE users SET name = 'Renamed' WHERE id = ?`, agentID); err != nil {
		t.Fatalf("non-root rename = %v, want success", err)
	}
}

// TestMigration0036UpgradesAnExistingDatabase proves the upgrade path: a database
// that already applied 0030 refuses the root password write, the same database
// after the upgrade keeps every row and every schema object except the narrowed
// guard, and then permits the password hash while refusing the mixed update and
// the delete.
func TestMigration0036UpgradesAnExistingDatabase(t *testing.T) {
	ctx := context.Background()
	s, err := openDSN(testDSN(t))
	if err != nil {
		t.Fatalf("open pre-0036 db: %v", err)
	}
	s.db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = s.db.Close() })
	if err := migrate(ctx, s.db, pre0036Migrations(t)); err != nil {
		t.Fatalf("migrate pre-0036 db: %v", err)
	}

	rootID := seedUserRaw(t, s, "Root", "root@example.com", "root")
	agentID := seedUserRole(t, s, "Agent", "agent@example.com", true, domain.RoleAgent)
	before := readStoredUser(t, s, rootID)

	// The state this migration corrects: a pre-0036 instance refuses even the
	// password hash.
	if _, err := s.db.ExecContext(ctx, `UPDATE users SET password_hash = 'rotated-hash' WHERE id = ?`, rootID); err == nil {
		t.Fatal("the pre-0036 schema accepted a root password update")
	}
	objectsBefore := schemaObjects(t, s)

	// Apply the real full set over the existing rows: the runner skips what is
	// already recorded and applies 0036.
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("upgrade to the full set: %v", err)
	}

	var recorded int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version = 36`).Scan(&recorded); err != nil {
		t.Fatalf("read the recorded version: %v", err)
	}
	if recorded != 1 {
		t.Errorf("schema_migrations holds %d rows for version 36, want 1", recorded)
	}

	// The upgrade changed exactly one schema object: the root update guard.
	objectsAfter := schemaObjects(t, s)
	withoutGuard := func(objects []string) []string {
		kept := make([]string, 0, len(objects))
		for _, object := range objects {
			if strings.HasPrefix(object, "trigger|trg_users_root_immutable_update|") {
				continue
			}
			kept = append(kept, object)
		}
		return kept
	}
	if before, after := withoutGuard(objectsBefore), withoutGuard(objectsAfter); !reflect.DeepEqual(before, after) {
		t.Errorf("0036 changed schema objects other than the root update guard:\nbefore=%v\nafter=%v", before, after)
	}

	// The rows survive the upgrade untouched.
	if after := readStoredUser(t, s, rootID); after != before {
		t.Errorf("the upgrade changed the root row:\nbefore=%+v\nafter=%+v", before, after)
	}

	// The narrowed guard permits the password hash alone, atomically.
	if _, err := s.db.ExecContext(ctx, `UPDATE users SET password_hash = 'rotated-hash' WHERE id = ?`, rootID); err != nil {
		t.Fatalf("root password update after the upgrade = %v, want success", err)
	}
	rotated := readStoredUser(t, s, rootID)
	if rotated.passwordHash != "rotated-hash" {
		t.Errorf("root password hash = %q, want the rotated one", rotated.passwordHash)
	}
	assertRootImmutabilityRefused(t, s, "rotate and rename together",
		`UPDATE users SET password_hash = 'sneaky-hash', name = 'Hacker' WHERE id = ?`, rootID)
	assertRootImmutabilityRefused(t, s, "rename", `UPDATE users SET name = 'Hacker' WHERE id = ?`, rootID)
	assertRootImmutabilityRefused(t, s, "change email", `UPDATE users SET email = 'hack@example.com' WHERE id = ?`, rootID)
	assertRootImmutabilityRefused(t, s, "deactivate", `UPDATE users SET active = 0 WHERE id = ?`, rootID)
	assertRootImmutabilityRefused(t, s, "demote", `UPDATE users SET role = 'admin' WHERE id = ?`, rootID)
	assertRootImmutabilityRefused(t, s, "rewrite created_at", `UPDATE users SET created_at = '2020-01-01T00:00:00Z' WHERE id = ?`, rootID)
	assertRootImmutabilityRefused(t, s, "move the identity", `UPDATE users SET id = ? WHERE id = ?`, rootID+1000, rootID)
	if _, err := s.db.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, rootID); err == nil {
		t.Fatal("root delete after the upgrade succeeded, want the immutability guard to abort it")
	}
	if after := readStoredUser(t, s, rootID); after != rotated {
		t.Errorf("a refused write after the upgrade changed the root row:\nbefore=%+v\nafter=%+v", rotated, after)
	}

	// The other account kept through the upgrade is still writable.
	if _, err := s.db.ExecContext(ctx, `UPDATE users SET password_hash = 'agent-hash' WHERE id = ?`, agentID); err != nil {
		t.Fatalf("non-root password update after the upgrade = %v, want success", err)
	}
}
