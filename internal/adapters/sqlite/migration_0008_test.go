package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// TestMigration0008 proves the audit_events.step_index column contract (PR10
// step-indexed audit correlation): the column exists, is NULLABLE with no
// default and no backfill, round-trips nil-safe through the fixed audit store
// insert/read columns, semantic workflow events carry their sealed zero-based
// index while transition/non-flow rows stay NULL, and legacy raw rows written
// without the column read back as NULL.
func TestMigration0008(t *testing.T) {
	s := newTestDB(t)

	// The column exists and is nullable.
	var nn, pk int
	var dflt *string
	if err := s.db.QueryRow(`SELECT "notnull", dflt_value, pk FROM pragma_table_info(?) WHERE name = ?`, "audit_events", "step_index").Scan(&nn, &dflt, &pk); err != nil {
		t.Fatalf("audit_events.step_index missing: %v", err)
	}
	if nn != 0 {
		t.Fatal("audit_events.step_index must be nullable")
	}
	if dflt != nil {
		t.Fatalf("audit_events.step_index must have no default, got %v", *dflt)
	}

	ctx := context.Background()
	now := time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC)

	cat := seedCategory(t, s, "Mig8Cat")
	req := seedUser(t, s, "Req", "mig8@x", true)
	tk := seedTicket(t, s, domain.Ticket{Number: 901, Title: "T", CategoryID: cat, Priority: domain.PriorityMedium, State: domain.StateNew, RequesterName: "Req", RequesterEmail: "mig8@x", RequesterUserID: &req, CreatedAt: now, UpdatedAt: now})

	// Round-trip: one semantic event WITH its sealed zero-based step index and
	// one transition row WITHOUT any index, through the store port.
	step2 := 2
	semantic := domain.AuditEvent{TicketID: tk.ID, Actor: "Ada", ActorUserID: &req, Action: domain.ActionWorkflowRequesterForm, StepIndex: &step2, CreatedAt: now}
	transition := domain.AuditEvent{TicketID: tk.ID, Actor: "workflow", Action: domain.ActionTransition, Field: ptr("state"), FromValue: ptr("new"), ToValue: ptr("in_progress"), CreatedAt: now}
	if err := s.AuditStore().Append(ctx, semantic, transition); err != nil {
		t.Fatalf("append audits: %v", err)
	}

	// A legacy-style raw row (pre-0008 shape, no step_index value) reads back NULL.
	if _, err := s.db.ExecContext(ctx, `INSERT INTO audit_events (ticket_id, actor, action, created_at) VALUES (?, 'Legacy', 'created', ?)`, tk.ID, formatTime(now)); err != nil {
		t.Fatalf("raw legacy insert: %v", err)
	}

	events, err := s.AuditStore().ListByTicket(ctx, tk.ID)
	if err != nil {
		t.Fatalf("list audits: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("events = %d, want 3", len(events))
	}
	var semanticGot, transitionGot, legacyGot *domain.AuditEvent
	for i := range events {
		switch events[i].Action {
		case domain.ActionWorkflowRequesterForm:
			semanticGot = &events[i]
		case domain.ActionTransition:
			transitionGot = &events[i]
		case domain.ActionCreated:
			legacyGot = &events[i]
		}
	}
	if semanticGot == nil || semanticGot.StepIndex == nil || *semanticGot.StepIndex != 2 {
		t.Fatalf("semantic event StepIndex = %v, want 2", semanticGot)
	}
	if transitionGot == nil || transitionGot.StepIndex != nil {
		t.Fatalf("transition event StepIndex = %v, want nil (transitions carry no step context)", transitionGot)
	}
	if legacyGot == nil || legacyGot.StepIndex != nil {
		t.Fatalf("legacy raw row StepIndex = %v, want nil (no backfill)", legacyGot)
	}

	// Migration bookkeeping: version 8 applied exactly once.
	var applied int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version=8`).Scan(&applied); err != nil {
		t.Fatalf("schema_migrations read: %v", err)
	}
	if applied != 1 {
		t.Fatalf("schema_migrations version=8 rows = %d, want 1", applied)
	}
}
