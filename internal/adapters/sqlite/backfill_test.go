package sqlite

import (
	"context"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
)

// Task 1.6: RED tests for the legacy backfill in migrate.go
// (role-authorization "Reliable legacy setup user becomes root", ticket-
// access "Legacy Ownership Backfill"). Each test drives a legacy database
// through the pre-0003 schema, seeds pre-role state, then runs the full
// migration set and asserts the backfill outcome. Written before the
// backfill exists: every assertion fails until migrate.go promotes id=1
// and matches requesters from reliable audit evidence.

// legacyMigrations returns a filesystem containing ONLY 0001 and 0002
// (the pre-role schema), so a test can seed legacy state before 0003 and
// the backfill run.
func legacyMigrations(t *testing.T) fs.FS {
	t.Helper()
	m := fstest.MapFS{}
	for _, name := range []string{"migrations/0001_init.sql", "migrations/0002_fts.sql"} {
		b, err := fs.ReadFile(migrationsFS, name)
		if err != nil {
			t.Fatalf("read embedded %s: %v", name, err)
		}
		m[name] = &fstest.MapFile{Data: b}
	}
	return m
}

// seedLegacyTicket inserts a pre-role ticket with a created audit event.
func seedLegacyTicket(t *testing.T, s *Store, number int64, requesterName, requesterEmail string, catID int64, createdEvents int) int64 {
	t.Helper()
	ctx := context.Background()
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO tickets (number, title, description, requester_name, requester_email, category_id, priority, state, created_at, updated_at)
		 VALUES (?, 'legacy', '', ?, ?, ?, 'medium', 'new', '2026-08-06T10:00:00Z', '2026-08-06T10:00:00Z')`,
		number, requesterName, requesterEmail, catID)
	if err != nil {
		t.Fatalf("seed legacy ticket: %v", err)
	}
	ticketID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("last insert id: %v", err)
	}
	for i := 0; i < createdEvents; i++ {
		if _, err := s.db.ExecContext(ctx,
			`INSERT INTO audit_events (ticket_id, actor, action, created_at)
			 VALUES (?, ?, 'created', '2026-08-06T10:00:00Z')`,
			ticketID, requesterName); err != nil {
			t.Fatalf("seed created event: %v", err)
		}
	}
	return ticketID
}

func TestBackfillReliableSetupUserBecomesRoot(t *testing.T) {
	s, err := openDSN(testDSN(t))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.db.Close() })
	s.db.SetMaxOpenConns(1)
	ctx := context.Background()

	// Pre-role database: users 1 (Ana, the original setup user) and 2
	// (Bob), one category, two legacy tickets.
	if err := migrate(ctx, s.db, legacyMigrations(t)); err != nil {
		t.Fatalf("apply legacy migrations: %v", err)
	}
	anaID := seedUser(t, s, "Ana", "ana@example.com", true)
	bobID := seedUser(t, s, "Bob", "bob@example.com", true)
	catID := seedCategory(t, s, "Bugs")
	// Ana's ticket has exactly one creation event naming her.
	anaTicket := seedLegacyTicket(t, s, 1, "Ana", "ana@example.com", catID, 1)
	// Bob's ticket has NO creation event — requester cannot be proven.
	seedLegacyTicket(t, s, 2, "Bob", "bob@example.com", catID, 0)

	// Apply 0003 + backfill.
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("apply 0003 + backfill: %v", err)
	}

	// The reliable id=1 setup user becomes root; the rest stay agent.
	var anaRole, bobRole string
	if err := s.db.QueryRow(`SELECT role FROM users WHERE id = ?`, anaID).Scan(&anaRole); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT role FROM users WHERE id = ?`, bobID).Scan(&bobRole); err != nil {
		t.Fatal(err)
	}
	if anaRole != "root" {
		t.Errorf("ana role = %q, want %q (reliable setup user)", anaRole, "root")
	}
	if bobRole != "agent" {
		t.Errorf("bob role = %q, want %q (remaining users backfill to agent)", bobRole, "agent")
	}

	// The promotion is audited in role_changes.
	var changes int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM role_changes WHERE user_id = ? AND from_role = 'agent' AND to_role = 'root'`,
		anaID).Scan(&changes); err != nil {
		t.Fatal(err)
	}
	if changes != 1 {
		t.Errorf("role_changes promotion rows = %d, want 1", changes)
	}

	// Reliable evidence backfills the requester; unprovable stays NULL.
	var requester any
	if err := s.db.QueryRow(`SELECT requester_user_id FROM tickets WHERE id = ?`, anaTicket).Scan(&requester); err != nil {
		t.Fatal(err)
	}
	if requester == nil {
		t.Error("ana ticket requester_user_id = NULL, want Ana's user id")
	}
	if id, ok := requester.(int64); ok && id != anaID {
		t.Errorf("ana ticket requester_user_id = %d, want %d", id, anaID)
	}
	var bobRequester any
	if err := s.db.QueryRow(`SELECT requester_user_id FROM tickets WHERE id = 2`).Scan(&bobRequester); err != nil {
		t.Fatal(err)
	}
	if bobRequester != nil {
		t.Errorf("unprovable bob ticket requester_user_id = %v, want NULL", bobRequester)
	}
}

func TestBackfillAmbiguousRequesterStaysNull(t *testing.T) {
	s, err := openDSN(testDSN(t))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.db.Close() })
	s.db.SetMaxOpenConns(1)
	ctx := context.Background()

	if err := migrate(ctx, s.db, legacyMigrations(t)); err != nil {
		t.Fatalf("apply legacy migrations: %v", err)
	}
	seedUser(t, s, "Ana", "ana@example.com", true)
	catID := seedCategory(t, s, "Bugs")
	// Two creation events → ambiguous.
	seedLegacyTicket(t, s, 1, "Ana", "ana@example.com", catID, 2)
	// Snapshot names nobody (surviving user renamed) → no match.
	seedLegacyTicket(t, s, 2, "Ana Old", "old@example.com", catID, 1)

	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("apply 0003 + backfill: %v", err)
	}
	for _, want := range []struct {
		ticket int64
		null   bool
	}{{1, true}, {2, true}} {
		var requester any
		if err := s.db.QueryRow(`SELECT requester_user_id FROM tickets WHERE id = ?`, want.ticket).Scan(&requester); err != nil {
			t.Fatal(err)
		}
		if (requester == nil) != want.null {
			t.Errorf("ticket %d requester_user_id = %v, want NULL=%v (fail closed)", want.ticket, requester, want.null)
		}
	}
}

func TestBackfillFailsClosedWithoutReliableRoot(t *testing.T) {
	s, err := openDSN(testDSN(t))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.db.Close() })
	s.db.SetMaxOpenConns(1)
	ctx := context.Background()

	if err := migrate(ctx, s.db, legacyMigrations(t)); err != nil {
		t.Fatalf("apply legacy migrations: %v", err)
	}
	// Users exist, but the original setup user id=1 is gone: no reliable
	// root can be proven, so startup must fail closed (operator selects).
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO users (id, name, email, password_hash, active, created_at)
		 VALUES (2, 'Bob', 'bob@example.com', 'h', 1, '2026-08-06T10:00:00Z')`); err != nil {
		t.Fatalf("seed user without id=1: %v", err)
	}

	err = s.Migrate(ctx)
	if err == nil {
		t.Fatal("migrate succeeded with users but no reliable root, want fail-closed error")
	}
	if !strings.Contains(err.Error(), "recover-root") {
		t.Errorf("fail-closed error = %v, want mention of -recover-root", err)
	}
}

func TestBackfillFreshDatabaseNoUsersIsClean(t *testing.T) {
	s, err := openDSN(testDSN(t))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.db.Close() })
	s.db.SetMaxOpenConns(1)

	// A brand-new database migrates cleanly: no users, no root, no error
	// (the /setup bootstrap creates the root later).
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("fresh migrate: %v", err)
	}
	var roots int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM users WHERE role = 'root'`).Scan(&roots); err != nil {
		t.Fatal(err)
	}
	if roots != 0 {
		t.Errorf("fresh db root count = %d, want 0", roots)
	}
}

func TestBackfillIsIdempotent(t *testing.T) {
	s, err := openDSN(testDSN(t))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.db.Close() })
	s.db.SetMaxOpenConns(1)
	ctx := context.Background()

	if err := migrate(ctx, s.db, legacyMigrations(t)); err != nil {
		t.Fatalf("apply legacy migrations: %v", err)
	}
	anaID := seedUser(t, s, "Ana", "ana@example.com", true)
	catID := seedCategory(t, s, "Bugs")
	seedLegacyTicket(t, s, 1, "Ana", "ana@example.com", catID, 1)

	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("first migrate: %v", err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("second migrate: %v", err)
	}

	var role string
	if err := s.db.QueryRow(`SELECT role FROM users WHERE id = ?`, anaID).Scan(&role); err != nil {
		t.Fatal(err)
	}
	if role != "root" {
		t.Errorf("role after rerun = %q, want %q (no duplicate promotion)", role, "root")
	}
	var changes int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM role_changes WHERE user_id = ?`, anaID).Scan(&changes); err != nil {
		t.Fatal(err)
	}
	if changes != 1 {
		t.Errorf("role_changes rows after rerun = %d, want 1 (idempotent)", changes)
	}
}
