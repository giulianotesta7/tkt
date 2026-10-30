package sqlite

import (
	"context"
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
