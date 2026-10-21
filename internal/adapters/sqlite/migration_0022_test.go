package sqlite

import (
	"context"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
)

// Migration 0022 tests (ticket on hold — issue #352): the tickets.on_hold_since
// and on_hold_reason column shapes, the CHECK pairing them, the pre-0022
// upgrade path leaving every existing ticket off hold, and the version
// bookkeeping. Written against the conventions of migration_0020_test.go.

func pre0022Migrations(t *testing.T) fstest.MapFS {
	t.Helper()
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		t.Fatalf("read embedded migrations: %v", err)
	}
	out := fstest.MapFS{}
	for _, entry := range entries {
		if strings.Compare(entry.Name(), "0022_ticket_hold.sql") >= 0 {
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

func TestMigration0022HoldColumnShape(t *testing.T) {
	s := newTestDB(t)
	for _, tc := range []struct {
		column      string
		wantNotNull int
		wantDefault *string
	}{
		{"on_hold_since", 0, nil},
		{"on_hold_reason", 1, strPtrForMigration("''")},
	} {
		var (
			colType string
			notNull int
			dflt    *string
		)
		if err := s.db.QueryRow(`SELECT type, "notnull", dflt_value FROM pragma_table_info('tickets') WHERE name=?`, tc.column).
			Scan(&colType, &notNull, &dflt); err != nil {
			t.Fatalf("%s: %v", tc.column, err)
		}
		if colType != "TEXT" || notNull != tc.wantNotNull {
			t.Errorf("%s = %s notnull=%d, want TEXT notnull=%d", tc.column, colType, notNull, tc.wantNotNull)
		}
		if (dflt == nil) != (tc.wantDefault == nil) || (dflt != nil && *dflt != *tc.wantDefault) {
			t.Errorf("%s default = %v, want %v", tc.column, dflt, tc.wantDefault)
		}
	}
}

func strPtrForMigration(s string) *string { return &s }

// TestMigration0022CheckPairsSinceAndReason proves a hold always carries a
// reason and a ticket off hold carries none.
func TestMigration0022CheckPairsSinceAndReason(t *testing.T) {
	s := newTestDB(t)
	id := seedTicketForTimeline(t, s, 1)
	for _, tc := range []struct {
		name   string
		since  any
		reason string
		ok     bool
	}{
		{"on hold with a reason", "2026-08-06T11:00:00Z", "waiting", true},
		{"off hold without a reason", nil, "", true},
		{"on hold without a reason", "2026-08-06T11:00:00Z", "", false},
		{"off hold with a reason", nil, "waiting", false},
	} {
		_, err := s.db.Exec(`UPDATE tickets SET on_hold_since=?, on_hold_reason=? WHERE id=?`, tc.since, tc.reason, id)
		if (err == nil) != tc.ok {
			t.Errorf("%s: err = %v, want ok=%v", tc.name, err, tc.ok)
		}
	}
}

func TestMigration0022UpgradePathLeavesTicketsOffHold(t *testing.T) {
	ctx := context.Background()
	pre, err := openDSN(testDSN(t))
	if err != nil {
		t.Fatalf("open pre-0022 db: %v", err)
	}
	pre.db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = pre.db.Close() })
	if err := migrate(ctx, pre.db, pre0022Migrations(t)); err != nil {
		t.Fatalf("migrate pre-0022 db: %v", err)
	}
	for _, column := range []string{"on_hold_since", "on_hold_reason"} {
		if columnExists(t, pre, "tickets", column) {
			t.Fatalf("%s existed before 0022", column)
		}
	}
	cat := seedCategory(t, pre, "pre-0022")
	if _, err := pre.db.ExecContext(ctx, `INSERT INTO tickets (number, title, description, requester_name, requester_email, category_id, priority, state, created_at, updated_at)
		VALUES (1, 'old', '', '', '', ?, 'medium', 'in_progress', '2026-08-06T10:00:00Z', '2026-08-06T10:00:00Z')`, cat); err != nil {
		t.Fatalf("insert pre-0022 ticket: %v", err)
	}
	if err := migrate(ctx, pre.db, migrationsFS); err != nil {
		t.Fatalf("upgrade pre-0022 db: %v", err)
	}
	var since *string
	var reason string
	if err := pre.db.QueryRowContext(ctx, `SELECT on_hold_since, on_hold_reason FROM tickets WHERE number=1`).Scan(&since, &reason); err != nil {
		t.Fatalf("read upgraded ticket: %v", err)
	}
	if since != nil || reason != "" {
		t.Errorf("upgraded ticket hold = (%v, %q), want off hold", since, reason)
	}
	var recorded int
	if err := pre.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version=22`).Scan(&recorded); err != nil {
		t.Fatalf("schema_migrations read: %v", err)
	}
	if recorded != 1 {
		t.Errorf("schema_migrations version=22 rows = %d, want 1", recorded)
	}
}
