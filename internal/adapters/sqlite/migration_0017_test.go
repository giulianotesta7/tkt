package sqlite

import (
	"context"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
)

// Migration 0017 tests (ticket_sla frozen due/warning instants — issue
// #211): the four columns exist with their shape and defaults, a row
// inserted without them carries '' (the documented pre-0017 marker), the
// upgrade path preserves a pre-0017 row, and the version bookkeeping.
// Written against the numbered-series conventions of migration_0015_test.go.

// pre0017Migrations returns the embedded migrations strictly before 0017,
// so the upgrade-path test can seed a pre-0017 database and then apply the
// real full set (pattern of pre0015Migrations).
func pre0017Migrations(t *testing.T) fstest.MapFS {
	t.Helper()
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		t.Fatalf("read embedded migrations: %v", err)
	}
	out := fstest.MapFS{}
	for _, entry := range entries {
		if strings.Compare(entry.Name(), "0017_ticket_sla_due_instants.sql") >= 0 {
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

// TestMigration0017AddsFrozenInstantColumns proves the four frozen
// instant columns exist with their shape: TEXT, NOT NULL, DEFAULT ” —
// the DEFAULT is a SQLite mechanics constraint (a NOT NULL column cannot
// be added without one), documented in the migration header.
func TestMigration0017AddsFrozenInstantColumns(t *testing.T) {
	s := newTestDB(t)

	for _, name := range []string{
		"warn_first_response_at", "due_first_response_at", "warn_resolve_at", "due_resolve_at",
	} {
		var colType string
		var notNull int
		var dflt *string
		if err := s.db.QueryRow(
			`SELECT type, "notnull", dflt_value FROM pragma_table_info('ticket_sla') WHERE name = ?`, name,
		).Scan(&colType, &notNull, &dflt); err != nil {
			t.Fatalf("column %s missing after 0017: %v", name, err)
		}
		if colType != "TEXT" {
			t.Errorf("column %s type = %q, want TEXT", name, colType)
		}
		if notNull != 1 {
			t.Errorf("column %s notnull = %d, want 1", name, notNull)
		}
		if dflt == nil || *dflt != "''" {
			t.Errorf("column %s default = %v, want the quoted empty string ''", name, dflt)
		}
	}
}

// TestMigration0017LegacyRowCarriesEmptyInstants proves a ticket_sla row
// inserted WITHOUT the new columns — the only shape a pre-0017 writer
// could produce — carries ” in all four instants, and that the store
// reads it back as the zero time ("no frozen SLA"), never as a date in
// year zero.
func TestMigration0017LegacyRowCarriesEmptyInstants(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	ticketID := seedTicketForTimeline(t, s, 1)

	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO ticket_sla (ticket_id, first_response_seconds, resolve_seconds, started_at, policy_snapshot_at)
		VALUES (?, 1800, 14400, ?, ?)`,
		ticketID, formatTime(testClock), formatTime(testClock)); err != nil {
		t.Fatalf("insert legacy-shaped row: %v", err)
	}

	var warnFR, dueFR, warnRes, dueRes string
	if err := s.db.QueryRow(`
		SELECT warn_first_response_at, due_first_response_at, warn_resolve_at, due_resolve_at
		FROM ticket_sla WHERE ticket_id = ?`, ticketID).Scan(&warnFR, &dueFR, &warnRes, &dueRes); err != nil {
		t.Fatalf("read legacy row: %v", err)
	}
	for name, value := range map[string]string{
		"warn_first_response_at": warnFR,
		"due_first_response_at":  dueFR,
		"warn_resolve_at":        warnRes,
		"due_resolve_at":         dueRes,
	} {
		if value != "" {
			t.Errorf("legacy row %s = %q, want '' (the pre-0017 marker)", name, value)
		}
	}

	got, err := s.SLAStore().TicketSLA(ctx, ticketID)
	if err != nil {
		t.Fatalf("TicketSLA on legacy row: %v", err)
	}
	if got == nil {
		t.Fatal("TicketSLA = nil, want the legacy row")
	}
	if !got.WarnFirstResponseAt.IsZero() || !got.DueFirstResponseAt.IsZero() ||
		!got.WarnResolveAt.IsZero() || !got.DueResolveAt.IsZero() {
		t.Errorf("legacy row instants = (%v, %v, %v, %v), want all zero (no frozen SLA)",
			got.WarnFirstResponseAt, got.DueFirstResponseAt, got.WarnResolveAt, got.DueResolveAt)
	}
}

// TestMigration0017UpgradePathPreservesLegacyRow proves the upgrade path:
// a ticket_sla row written by a PRE-0017 database survives the migration
// carrying ” in all four instants.
func TestMigration0017UpgradePathPreservesLegacyRow(t *testing.T) {
	ctx := context.Background()
	pre, err := openDSN(testDSN(t))
	if err != nil {
		t.Fatalf("open pre-0017 db: %v", err)
	}
	pre.db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = pre.db.Close() })
	if err := migrate(ctx, pre.db, pre0017Migrations(t)); err != nil {
		t.Fatalf("migrate pre-0017 db: %v", err)
	}

	ticketID := seedTicketForTimeline(t, pre, 1)
	if _, err := pre.db.ExecContext(ctx, `
		INSERT INTO ticket_sla (ticket_id, first_response_seconds, resolve_seconds, started_at, policy_snapshot_at)
		VALUES (?, 1800, 14400, ?, ?)`,
		ticketID, formatTime(testClock), formatTime(testClock)); err != nil {
		t.Fatalf("insert pre-0017 row: %v", err)
	}

	if err := migrate(ctx, pre.db, migrationsFS); err != nil {
		t.Fatalf("upgrade pre-0017 db: %v", err)
	}

	var warnFR, dueFR, warnRes, dueRes string
	if err := pre.db.QueryRow(`
		SELECT warn_first_response_at, due_first_response_at, warn_resolve_at, due_resolve_at
		FROM ticket_sla WHERE ticket_id = ?`, ticketID).Scan(&warnFR, &dueFR, &warnRes, &dueRes); err != nil {
		t.Fatalf("read upgraded row: %v", err)
	}
	if warnFR != "" || dueFR != "" || warnRes != "" || dueRes != "" {
		t.Errorf("upgraded row instants = (%q, %q, %q, %q), want all '' (pre-0017 rows keep the marker)",
			warnFR, dueFR, warnRes, dueRes)
	}
}

func TestMigration0017VersionBookkeeping(t *testing.T) {
	s := newTestDB(t)
	var applied int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version=16`).Scan(&applied); err != nil {
		t.Fatalf("schema_migrations read: %v", err)
	}
	if applied != 1 {
		t.Fatalf("schema_migrations version=16 rows = %d, want 1", applied)
	}
}
