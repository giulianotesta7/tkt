package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

func TestAuditAPIKeyContext(t *testing.T) {
	s := newTestDB(t)
	actor := seedUserRaw(t, s, "Agent", "agent@example.com", "agent")
	svc := seedServiceUserRaw(t, s, "Bot", "bot@service.invalid", "user")
	if err := insertAPIKeyRaw(s, "audit-key", svc, actor); err != nil {
		t.Fatal(err)
	}
	key, err := s.APIAccessStore().APIKeyByPublicID(t.Context(), "audit-key")
	if err != nil {
		t.Fatal(err)
	}
	cat := seedCategory(t, s, "Support")
	ticket := seedLoadTicket(t, s, 901, actor, cat, domain.StateNew)
	event := domain.AuditEvent{TicketID: ticket, Actor: "Bot", ActorUserID: &svc, Action: domain.ActionUpdate, CreatedAt: time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC)}
	for _, ctx := range []context.Context{application.WithAPIKeyID(t.Context(), key.ID), t.Context()} {
		if err := s.AuditStore().Append(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	events, err := s.AuditStore().ListByTicket(t.Context(), ticket)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("events=%d", len(events))
	}
	if events[0].APIKeyID == nil || *events[0].APIKeyID != key.ID {
		t.Fatalf("key attribution=%v", events[0].APIKeyID)
	}
	if events[1].APIKeyID != nil {
		t.Fatal("browser event has API key")
	}
	for _, e := range events {
		if e.ActorUserID == nil || *e.ActorUserID != svc || e.Actor != event.Actor || e.Action != event.Action || !e.CreatedAt.Equal(event.CreatedAt) {
			t.Fatalf("event changed: %+v", e)
		}
	}
}

func TestAuditOAuthGrantContext(t *testing.T) {
	s := newTestDB(t)
	person := seedUserRaw(t, s, "Ana", "ana@example.com", "agent")
	grant := insertOAuthGrantRaw(t, s, person, insertOAuthClientRaw(t, s, "client-1"))
	cat := seedCategory(t, s, "Support")
	ticket := seedLoadTicket(t, s, 901, person, cat, domain.StateNew)
	event := domain.AuditEvent{TicketID: ticket, Actor: "Ana", ActorUserID: &person, Action: domain.ActionUpdate, CreatedAt: time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC)}
	for _, ctx := range []context.Context{application.WithOAuthGrantID(t.Context(), grant), t.Context()} {
		if err := s.AuditStore().Append(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := s.db.Query(`SELECT oauth_grant_id, api_key_id, actor_user_id FROM audit_events WHERE ticket_id = ? ORDER BY id`, ticket)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var grantID, keyID, actor sql.NullInt64
		if err := rows.Scan(&grantID, &keyID, &actor); err != nil {
			t.Fatal(err)
		}
		got = append(got, fmt.Sprintf("grant=%v key=%v actor=%v", grantID, keyID, actor))
	}
	want := []string{
		fmt.Sprintf("grant=%v key=%v actor=%v", sql.NullInt64{Int64: grant, Valid: true}, sql.NullInt64{}, sql.NullInt64{Int64: person, Valid: true}),
		fmt.Sprintf("grant=%v key=%v actor=%v", sql.NullInt64{}, sql.NullInt64{}, sql.NullInt64{Int64: person, Valid: true}),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("audit provenance = %q, want %q", got, want)
	}
}
