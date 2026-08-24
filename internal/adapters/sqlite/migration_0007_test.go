package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// TestMigration0007 proves the audit_events.desk_id column contract (contextual
// workflow timeline): the column exists, is NULLABLE, round-trips through the
// fixed audit store insert/read columns, and its desks FK is ON DELETE SET NULL
// so deleting a desk never deletes history — it degrades to "Unknown desk".
func TestMigration0007(t *testing.T) {
	s := newTestDB(t)

	// The column exists and is nullable.
	var nn, pk int
	var dflt *string
	if err := s.db.QueryRow(`SELECT "notnull", dflt_value, pk FROM pragma_table_info(?) WHERE name = ?`, "audit_events", "desk_id").Scan(&nn, &dflt, &pk); err != nil {
		t.Fatalf("audit_events.desk_id missing: %v", err)
	}
	if nn != 0 {
		t.Fatal("audit_events.desk_id must be nullable")
	}

	ctx := context.Background()
	now := time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC)

	// Seed a ticket and a desk to reference.
	cat := seedCategory(t, s, "Mig7Cat")
	req := seedUser(t, s, "Req", "mig7@x", true)
	tk := seedTicket(t, s, domain.Ticket{Number: 900, Title: "T", CategoryID: cat, Priority: domain.PriorityMedium, State: domain.StateNew, RequesterName: "Req", RequesterEmail: "mig7@x", RequesterUserID: &req, CreatedAt: now, UpdatedAt: now})
	res, err := s.db.ExecContext(ctx, `INSERT INTO desks (name, created_at) VALUES ('Mig7Desk', ?)`, formatTime(now))
	if err != nil {
		t.Fatalf("insert desk: %v", err)
	}
	deskID, _ := res.LastInsertId()

	// Round-trip: one contextual row WITH desk context and one legacy-style row
	// WITHOUT it, through the store port.
	withDesk := domain.AuditEvent{TicketID: tk.ID, Actor: "Ag", Action: domain.ActionWorkflowAssignment, Field: ptr("user"), FromValue: ptr(""), ToValue: ptr("1"), DeskID: &deskID, CreatedAt: now}
	withoutDesk := domain.AuditEvent{TicketID: tk.ID, Actor: "Ada", Action: domain.ActionCreated, CreatedAt: now}
	if err := s.AuditStore().Append(ctx, withDesk, withoutDesk); err != nil {
		t.Fatalf("append audits: %v", err)
	}
	events, err := s.AuditStore().ListByTicket(ctx, tk.ID)
	if err != nil {
		t.Fatalf("list audits: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("events = %d, want 2", len(events))
	}
	var contextual, legacy *domain.AuditEvent
	for i := range events {
		switch events[i].Action {
		case domain.ActionWorkflowAssignment:
			contextual = &events[i]
		case domain.ActionCreated:
			legacy = &events[i]
		}
	}
	if contextual == nil || contextual.DeskID == nil || *contextual.DeskID != deskID {
		t.Fatalf("contextual event DeskID = %v, want %d", contextual, deskID)
	}
	if legacy == nil || legacy.DeskID != nil {
		t.Fatalf("legacy event DeskID = %v, want nil", legacy)
	}

	// FK ON DELETE SET NULL: deleting the desk keeps the row but nulls desk_id.
	if _, err := s.db.ExecContext(ctx, `DELETE FROM desks WHERE id=?`, deskID); err != nil {
		t.Fatalf("delete desk: %v", err)
	}
	var n int
	var di any
	if err := s.db.QueryRow(`SELECT COUNT(*), COALESCE(desk_id, 'NULL') FROM audit_events WHERE ticket_id=? AND action='workflow_assignment'`, tk.ID).Scan(&n, &di); err != nil {
		t.Fatalf("read after delete: %v", err)
	}
	if n != 1 {
		t.Fatalf("workflow_assignment rows after desk delete = %d, want 1 (history preserved)", n)
	}
	if di != "NULL" {
		t.Fatalf("desk_id after desk delete = %v, want NULL (ON DELETE SET NULL)", di)
	}

	// Migration bookkeeping: version 7 applied exactly once.
	var applied int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version=7`).Scan(&applied); err != nil {
		t.Fatalf("schema_migrations read: %v", err)
	}
	if applied != 1 {
		t.Fatalf("schema_migrations version=7 rows = %d, want 1", applied)
	}
}
