package sqlite

import (
	"context"
	"testing"
)

// RED tests for migration 0015 (frozen ticket SLA targets — issue #211):
// the ticket_sla table, its ticket_id primary key, the tickets cascade,
// and the version bookkeeping. Written against the schema that does not
// exist yet: every assertion fails until 0015 lands (strict TDD RED).

func TestMigration0015Columns(t *testing.T) {
	s := newTestDB(t)

	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='ticket_sla'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("table ticket_sla missing after 0015")
	}
	for _, col := range []string{"ticket_id", "first_response_seconds", "resolve_seconds", "started_at", "policy_snapshot_at"} {
		if !columnExists(t, s, "ticket_sla", col) {
			t.Errorf("ticket_sla.%s column missing after 0015", col)
		}
	}
}

func TestMigration0015TicketIDPrimaryKey(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()

	// ticket_id is the PRIMARY KEY: one frozen SLA row per ticket, and a
	// second insert for the same ticket is rejected.
	ticketID := seedTicketForTimeline(t, s, 1)
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO ticket_sla (ticket_id, first_response_seconds, resolve_seconds, started_at, policy_snapshot_at)
		 VALUES (?, 1800, 14400, '2026-08-06T10:00:00Z', '2026-08-06T10:00:00Z')`, ticketID); err != nil {
		t.Fatalf("insert ticket_sla: %v", err)
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO ticket_sla (ticket_id, first_response_seconds, resolve_seconds, started_at, policy_snapshot_at)
		 VALUES (?, 3600, 28800, '2026-08-06T10:00:00Z', '2026-08-06T10:00:00Z')`, ticketID); err == nil {
		t.Fatal("second ticket_sla insert for the same ticket succeeded, want PRIMARY KEY failure")
	}
}

// TestMigration0015TicketCascade proves the ticket_sla FK both exists and
// is declared ON DELETE CASCADE: deleting a ticket must take its frozen
// SLA row with it.
func TestMigration0015TicketCascade(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()

	var table, from, to, onDelete string
	if err := s.db.QueryRow(`SELECT "table", "from", "to", on_delete FROM pragma_foreign_key_list(?) WHERE "from" = 'ticket_id'`, "ticket_sla").
		Scan(&table, &from, &to, &onDelete); err != nil {
		t.Fatalf("pragma_foreign_key_list(ticket_sla): %v", err)
	}
	if table != "tickets" || to != "id" {
		t.Errorf("ticket_sla FK = (%s, %s, %s), want (tickets, ticket_id, id)", table, from, to)
	}
	if onDelete != "CASCADE" {
		t.Fatalf("ticket_sla on_delete = %q, want CASCADE", onDelete)
	}

	// Behavioural proof: a deleted ticket takes its frozen row with it.
	ticketID := seedTicketForTimeline(t, s, 1)
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO ticket_sla (ticket_id, first_response_seconds, resolve_seconds, started_at, policy_snapshot_at)
		 VALUES (?, 1800, 14400, '2026-08-06T10:00:00Z', '2026-08-06T10:00:00Z')`, ticketID); err != nil {
		t.Fatalf("insert ticket_sla: %v", err)
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM tickets WHERE id = ?`, ticketID); err != nil {
		t.Fatalf("delete ticket: %v", err)
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM ticket_sla WHERE ticket_id = ?`, ticketID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("ticket_sla rows after ticket delete = %d, want 0 (cascade)", n)
	}
}

func TestMigration0015VersionBookkeeping(t *testing.T) {
	s := newTestDB(t)
	var applied int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version=14`).Scan(&applied); err != nil {
		t.Fatalf("schema_migrations read: %v", err)
	}
	if applied != 1 {
		t.Fatalf("schema_migrations version=14 rows = %d, want 1", applied)
	}
}
