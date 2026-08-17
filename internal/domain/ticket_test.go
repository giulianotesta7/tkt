package domain_test

import (
	"errors"
	"reflect"
	"strconv"
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
		Title:      "Original title",
		CategoryID: 1,
		Priority:   domain.PriorityMedium,
		State:      domain.StateInProgress,
		CreatedAt:  time.Date(2026, 8, 5, 9, 0, 0, 0, time.UTC),
		UpdatedAt:  time.Date(2026, 8, 5, 9, 0, 0, 0, time.UTC),
	}
}

func TestApplyUpdateTitleAndPriorityChanged(t *testing.T) {
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	tt := baseTicket()

	events, err := tt.ApplyUpdate(domain.TicketUpdate{Title: strPtr("Renamed"), Priority: priorityPtr(domain.PriorityHigh)}, now)

	if err != nil {
		t.Fatalf("valid edit must succeed, got %v", err)
	}
	if tt.Title != "Renamed" || tt.Priority != domain.PriorityHigh {
		t.Fatalf("changed fields must be applied, got title=%q priority=%s", tt.Title, tt.Priority)
	}
	if tt.CategoryID != 1 {
		t.Fatalf("the category is immutable after creation and must stay, got %d", tt.CategoryID)
	}
	if !tt.UpdatedAt.Equal(now) {
		t.Fatalf("updated_at must be refreshed on change, got %v", tt.UpdatedAt)
	}
	if len(events) != 2 {
		t.Fatalf("one audit event per changed field expected (2), got %d", len(events))
	}
	e0, e1 := events[0], events[1]
	if e0.Action != domain.ActionUpdate || e0.Field == nil || *e0.Field != "title" {
		t.Fatalf("audit event 0 must describe the title update, got action=%q field=%v", e0.Action, e0.Field)
	}
	if e1.Field == nil || *e1.Field != "priority" {
		t.Fatalf("audit event 1 must describe the priority update, got field=%v", e1.Field)
	}
	if e0.FromValue == nil || *e0.FromValue != "Original title" || e0.ToValue == nil || *e0.ToValue != "Renamed" {
		t.Fatalf("title audit must record the change, got from=%v to=%v", e0.FromValue, e0.ToValue)
	}
	if e1.FromValue == nil || *e1.FromValue != string(domain.PriorityMedium) || e1.ToValue == nil || *e1.ToValue != string(domain.PriorityHigh) {
		t.Fatalf("priority audit must record the change, got from=%v to=%v", e1.FromValue, e1.ToValue)
	}
	if e1.TicketID != tt.ID || !e1.CreatedAt.Equal(now) {
		t.Fatalf("audit event must reference ticket %d at the injected time", tt.ID)
	}
	// Edits never touch lifecycle timestamps or state.
	if tt.ResolvedAt != nil || tt.ClosedAt != nil {
		t.Fatalf("field edit must not touch resolved_at/closed_at, got %v / %v", tt.ResolvedAt, tt.ClosedAt)
	}
	if tt.State != domain.StateInProgress {
		t.Fatalf("field edit must not change state, got %s", tt.State)
	}
}

func TestApplyUpdateInvalidPriorityNoChanges(t *testing.T) {
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	tt := baseTicket()
	before := *tt

	events, err := tt.ApplyUpdate(domain.TicketUpdate{Priority: priorityPtr("urgent")}, now)

	if err == nil {
		t.Fatal("unsupported priority must be rejected")
	}
	var ipe *domain.InvalidPriorityError
	if !errors.As(err, &ipe) {
		t.Fatalf("want *InvalidPriorityError, got %T", err)
	}
	if !strings.Contains(err.Error(), domain.ErrMsgInvalidPriority) {
		t.Fatalf("error must carry the English message %q, got %q", domain.ErrMsgInvalidPriority, err.Error())
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
		Title:      "Before",
		State:      domain.StateClosed,
		ResolvedAt: &stamp,
		ClosedAt:   &stamp,
		UpdatedAt:  stamp,
	}

	events, err := tt.ApplyUpdate(domain.TicketUpdate{Title: strPtr("After")}, now)

	if err != nil {
		t.Fatalf("title edit must succeed, got %v", err)
	}
	if tt.Title != "After" {
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

	// Title and priority change; nothing else is touched.
	events, err := tt.ApplyUpdate(domain.TicketUpdate{
		Title:    strPtr("New title"),
		Priority: priorityPtr(domain.PriorityHigh),
	}, now)

	if err != nil {
		t.Fatalf("edit must succeed, got %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("only changed fields must be audited, got %d events: %+v", len(events), events)
	}
	if *events[0].Field != "title" || *events[1].Field != "priority" {
		t.Fatalf("audit fields must be [title priority] in order, got %v %v", *events[0].Field, *events[1].Field)
	}
	if tt.Title != "New title" || tt.Priority != domain.PriorityHigh || tt.CategoryID != 1 {
		t.Fatalf("changed fields must apply, the immutable category must stay, got title=%q priority=%s category=%d",
			tt.Title, tt.Priority, tt.CategoryID)
	}
}

func TestApplyUpdateNoChangeNoAudit(t *testing.T) {
	previous := time.Date(2026, 8, 5, 9, 0, 0, 0, time.UTC)
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	tt := baseTicket()

	// Same value -> no mutation, no audit, no refresh.
	events, err := tt.ApplyUpdate(domain.TicketUpdate{Title: strPtr("Original title")}, now)

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
		t.Fatalf("error must carry the title field + English message, got %+v", err)
	}
	if !reflect.DeepEqual(*tt, before) {
		t.Fatalf("rejected edit must leave the ticket unchanged, got %+v", *tt)
	}
}

func TestPriorityRank(t *testing.T) {
	if got := domain.PriorityCritical.Rank(); got != 4 {
		t.Fatalf("critical rank must be 4, got %d", got)
	}
	if got := domain.PriorityHigh.Rank(); got != 3 {
		t.Fatalf("high rank must be 3, got %d", got)
	}
	if got := domain.PriorityMedium.Rank(); got != 2 {
		t.Fatalf("medium rank must be 2, got %d", got)
	}
	if got := domain.PriorityLow.Rank(); got != 1 {
		t.Fatalf("low rank must be 1, got %d", got)
	}
}

func TestApplyUpdateClearUserID(t *testing.T) {
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	tt := baseTicket()
	tt.UserID = int64Ptr(7)

	events, err := tt.ApplyUpdate(domain.TicketUpdate{ClearUserID: true}, now)

	if err != nil {
		t.Fatalf("clearing the assignment must succeed, got %v", err)
	}
	if tt.UserID != nil {
		t.Fatalf("assignment must be cleared, got %v", *tt.UserID)
	}
	if !tt.UpdatedAt.Equal(now) {
		t.Fatalf("updated_at must be refreshed on unassignment, got %v", tt.UpdatedAt)
	}
	if len(events) != 1 {
		t.Fatalf("exactly one audit event expected, got %d", len(events))
	}
	if events[0].Field == nil || *events[0].Field != "user" {
		t.Fatalf("audit event must name field %q, got %v", "user", events[0].Field)
	}
	if events[0].FromValue == nil || *events[0].FromValue != "7" || events[0].ToValue == nil || *events[0].ToValue != "" {
		t.Fatalf("audit event must record 7 -> \"\", got from=%v to=%v", events[0].FromValue, events[0].ToValue)
	}
}

func TestApplyUpdateClearUnassignedIsNoOp(t *testing.T) {
	previous := time.Date(2026, 8, 5, 9, 0, 0, 0, time.UTC)
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	tt := baseTicket() // unassigned

	events, err := tt.ApplyUpdate(domain.TicketUpdate{ClearUserID: true}, now)

	if err != nil {
		t.Fatalf("clearing an already-clear assignment must succeed, got %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("no-op unassignment must not append audit events, got %+v", events)
	}
	if !tt.UpdatedAt.Equal(previous) {
		t.Fatalf("no-op unassignment must not refresh updated_at, got %v", tt.UpdatedAt)
	}
}

func TestApplyUpdateConflictingUserAssignmentRejected(t *testing.T) {
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	tt := baseTicket()
	tt.UserID = int64Ptr(7)
	before := *tt

	events, err := tt.ApplyUpdate(domain.TicketUpdate{ClearUserID: true, UserID: int64Ptr(9)}, now)

	if err == nil {
		t.Fatal("ClearUserID together with UserID must be rejected as ambiguous")
	}
	var ve *domain.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("want *ValidationError, got %T", err)
	}
	if ve.Field != "user" || !strings.Contains(err.Error(), domain.ErrMsgConflictingUserAssignment) {
		t.Fatalf("error must carry the user field + English message, got %+v", err)
	}
	if !reflect.DeepEqual(*tt, before) {
		t.Fatalf("rejected update must leave the ticket completely unchanged:\nbefore=%+v\nafter =%+v", before, *tt)
	}
	if events != nil {
		t.Fatalf("rejected update must not append audit events, got %+v", events)
	}
}

func TestApplyUpdateUserAssignment(t *testing.T) {
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		name     string
		current  *int64
		assign   int64
		wantFrom string
	}{
		{name: "assign from unassigned", current: nil, assign: 7, wantFrom: ""},
		{name: "reassign to another user", current: int64Ptr(7), assign: 8, wantFrom: "7"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tt := baseTicket()
			tt.UserID = tc.current

			events, err := tt.ApplyUpdate(domain.TicketUpdate{UserID: int64Ptr(tc.assign)}, now)

			if err != nil {
				t.Fatalf("assignment must succeed, got %v", err)
			}
			if tt.UserID == nil || *tt.UserID != tc.assign {
				t.Fatalf("ticket must be assigned to %d, got %v", tc.assign, tt.UserID)
			}
			if !tt.UpdatedAt.Equal(now) {
				t.Fatalf("updated_at must be refreshed on assignment, got %v", tt.UpdatedAt)
			}
			if len(events) != 1 {
				t.Fatalf("exactly one audit event expected, got %d", len(events))
			}
			e := events[0]
			if e.Field == nil || *e.Field != "user" {
				t.Fatalf("audit event must name field %q, got %v", "user", e.Field)
			}
			wantTo := strconv.FormatInt(tc.assign, 10)
			if e.FromValue == nil || *e.FromValue != tc.wantFrom || e.ToValue == nil || *e.ToValue != wantTo {
				t.Fatalf("audit event must record %q -> %q, got from=%v to=%v", tc.wantFrom, wantTo, e.FromValue, e.ToValue)
			}
		})
	}
}
