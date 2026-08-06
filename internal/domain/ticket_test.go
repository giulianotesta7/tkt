package domain_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/domain"
)

func strPtr(s string) *string                        { return &s }
func int64Ptr(v int64) *int64                        { return &v }
func priorityPtr(p domain.Priority) *domain.Priority { return &p }

func baseTicket() *domain.Ticket {
	return &domain.Ticket{
		ID:         9,
		Number:     42,
		Title:      "Título original",
		CategoryID: 1,
		Priority:   domain.PriorityMedia,
		State:      domain.StateEnProgreso,
		CreatedAt:  time.Date(2026, 8, 5, 9, 0, 0, 0, time.UTC),
		UpdatedAt:  time.Date(2026, 8, 5, 9, 0, 0, 0, time.UTC),
	}
}

func TestApplyUpdateCategoryChanged(t *testing.T) {
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	tt := baseTicket()

	events, err := tt.ApplyUpdate(domain.TicketUpdate{CategoryID: int64Ptr(2)}, now)

	if err != nil {
		t.Fatalf("valid category edit must succeed, got %v", err)
	}
	if tt.CategoryID != 2 {
		t.Fatalf("category must be updated, got %d", tt.CategoryID)
	}
	if !tt.UpdatedAt.Equal(now) {
		t.Fatalf("updated_at must be refreshed on change, got %v", tt.UpdatedAt)
	}
	if len(events) != 1 {
		t.Fatalf("exactly one audit event expected, got %d", len(events))
	}
	e := events[0]
	if e.Action != domain.ActionUpdate || e.Field == nil || *e.Field != "category" {
		t.Fatalf("audit event must describe the category update, got action=%q field=%v", e.Action, e.Field)
	}
	if e.FromValue == nil || *e.FromValue != "1" || e.ToValue == nil || *e.ToValue != "2" {
		t.Fatalf("audit event must record 1 -> 2, got from=%v to=%v", e.FromValue, e.ToValue)
	}
	if e.TicketID != tt.ID || !e.CreatedAt.Equal(now) {
		t.Fatalf("audit event must reference ticket %d at the injected time", tt.ID)
	}
	// Edits never touch lifecycle timestamps or state.
	if tt.ResolvedAt != nil || tt.ClosedAt != nil {
		t.Fatalf("field edit must not touch resolved_at/closed_at, got %v / %v", tt.ResolvedAt, tt.ClosedAt)
	}
	if tt.State != domain.StateEnProgreso {
		t.Fatalf("field edit must not change state, got %s", tt.State)
	}
}

func TestApplyUpdateInvalidPriorityNoChanges(t *testing.T) {
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	tt := baseTicket()
	before := *tt

	events, err := tt.ApplyUpdate(domain.TicketUpdate{Priority: priorityPtr("urgente")}, now)

	if err == nil {
		t.Fatal("unsupported priority must be rejected")
	}
	var ipe *domain.InvalidPriorityError
	if !errors.As(err, &ipe) {
		t.Fatalf("want *InvalidPriorityError, got %T", err)
	}
	if !strings.Contains(err.Error(), domain.ErrMsgInvalidPriority) {
		t.Fatalf("error must carry the Spanish message %q, got %q", domain.ErrMsgInvalidPriority, err.Error())
	}
	if !reflect.DeepEqual(*tt, before) {
		t.Fatalf("rejected edit must leave the ticket completely unchanged:\nbefore=%+v\nafter =%+v", before, *tt)
	}
	if events != nil {
		t.Fatalf("rejected edit must not append audit events, got %+v", events)
	}
}

func TestApplyUpdateTimestampsUntouched(t *testing.T) {
	// Spec: "Timestamps follow transitions only" — lifecycle timestamps are
	// immune to field edits.
	stamp := time.Date(2026, 8, 4, 15, 0, 0, 0, time.UTC)
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	tt := &domain.Ticket{
		ID:         10,
		Title:      "Antes",
		State:      domain.StateCerrado,
		ResolvedAt: &stamp,
		ClosedAt:   &stamp,
		UpdatedAt:  stamp,
	}

	events, err := tt.ApplyUpdate(domain.TicketUpdate{Title: strPtr("Después")}, now)

	if err != nil {
		t.Fatalf("title edit must succeed, got %v", err)
	}
	if tt.Title != "Después" {
		t.Fatalf("title must be updated, got %q", tt.Title)
	}
	if tt.ResolvedAt == nil || !tt.ResolvedAt.Equal(stamp) || tt.ClosedAt == nil || !tt.ClosedAt.Equal(stamp) {
		t.Fatalf("resolved_at/closed_at must remain unchanged after edit, got %v / %v", tt.ResolvedAt, tt.ClosedAt)
	}
	if !tt.UpdatedAt.Equal(now) {
		t.Fatalf("updated_at must reflect the edit, got %v", tt.UpdatedAt)
	}
	if len(events) != 1 || events[0].Field == nil || *events[0].Field != "title" {
		t.Fatalf("expected one title audit event, got %+v", events)
	}
}

func TestApplyUpdateAuditsOnlyChangedFields(t *testing.T) {
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	tt := baseTicket()

	// Title and category change; priority keeps its current value on purpose.
	events, err := tt.ApplyUpdate(domain.TicketUpdate{
		Title:      strPtr("Nuevo título"),
		Priority:   priorityPtr(domain.PriorityMedia),
		CategoryID: int64Ptr(2),
	}, now)

	if err != nil {
		t.Fatalf("edit must succeed, got %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("only changed fields must be audited, got %d events: %+v", len(events), events)
	}
	if *events[0].Field != "title" || *events[1].Field != "category" {
		t.Fatalf("audit fields must be [title category] in order, got %v %v", *events[0].Field, *events[1].Field)
	}
	if tt.Title != "Nuevo título" || tt.CategoryID != 2 || tt.Priority != domain.PriorityMedia {
		t.Fatalf("changed fields must apply, unchanged priority must stay, got title=%q category=%d priority=%s",
			tt.Title, tt.CategoryID, tt.Priority)
	}
}

func TestApplyUpdateNoChangeNoAudit(t *testing.T) {
	previous := time.Date(2026, 8, 5, 9, 0, 0, 0, time.UTC)
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	tt := baseTicket()

	// Same value -> no mutation, no audit, no refresh.
	events, err := tt.ApplyUpdate(domain.TicketUpdate{Title: strPtr("Título original")}, now)

	if err != nil {
		t.Fatalf("no-op edit must succeed, got %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("no-op edit must not append audit events, got %+v", events)
	}
	if !tt.UpdatedAt.Equal(previous) {
		t.Fatalf("no-op edit must not refresh updated_at, got %v", tt.UpdatedAt)
	}
}

func TestApplyUpdateEmptyTitleRejected(t *testing.T) {
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	tt := baseTicket()
	before := *tt

	_, err := tt.ApplyUpdate(domain.TicketUpdate{Title: strPtr("   ")}, now)

	if err == nil {
		t.Fatal("blank title must be rejected")
	}
	var ve *domain.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("want *ValidationError, got %T", err)
	}
	if ve.Field != "title" || !strings.Contains(err.Error(), domain.ErrMsgTitleRequired) {
		t.Fatalf("error must carry the title field + Spanish message, got %+v", err)
	}
	if !reflect.DeepEqual(*tt, before) {
		t.Fatalf("rejected edit must leave the ticket unchanged, got %+v", *tt)
	}
}

func TestPriorityRank(t *testing.T) {
	if got := domain.PriorityCritica.Rank(); got != 4 {
		t.Fatalf("critica rank must be 4, got %d", got)
	}
	if got := domain.PriorityAlta.Rank(); got != 3 {
		t.Fatalf("alta rank must be 3, got %d", got)
	}
	if got := domain.PriorityMedia.Rank(); got != 2 {
		t.Fatalf("media rank must be 2, got %d", got)
	}
	if got := domain.PriorityBaja.Rank(); got != 1 {
		t.Fatalf("baja rank must be 1, got %d", got)
	}
}
