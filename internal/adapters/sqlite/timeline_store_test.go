package sqlite

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// Task 4.3: comment store (append-only timeline, ASC) and audit store
// (multi-event append, ASC occurrence order).

func seedTicketForTimeline(t *testing.T, s *Store, number int) int64 {
	t.Helper()
	cat := seedCategory(t, s, "Bugs")
	return seedTicket(t, s, domain.Ticket{Number: number, Title: "timeline ticket",
		CategoryID: cat, Priority: domain.PriorityMedium, State: domain.StateNew,
		CreatedAt: testClock, UpdatedAt: testClock}).ID
}

func TestCommentAddAssignsIDAndPersists(t *testing.T) {
	s := newTestDB(t)
	ticketID := seedTicketForTimeline(t, s, 1)
	ctx := context.Background()

	c := &domain.Comment{TicketID: ticketID, Author: "Ana", Body: "first", CreatedAt: testClock}
	if err := s.CommentStore().Add(ctx, c); err != nil {
		t.Fatalf("add: %v", err)
	}
	if c.ID == 0 {
		t.Error("Add did not assign an id")
	}

	got, err := s.CommentStore().ListByTicket(ctx, ticketID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].Author != "Ana" || got[0].Body != "first" {
		t.Errorf("comment = %+v", got[0])
	}
	if !got[0].CreatedAt.Equal(testClock) {
		t.Errorf("created_at = %v, want %v", got[0].CreatedAt, testClock)
	}
}

func TestCommentListByTicketAscending(t *testing.T) {
	s := newTestDB(t)
	ticketID := seedTicketForTimeline(t, s, 1)
	ctx := context.Background()

	// Creation order with distinct timestamps AND with equal timestamps
	// (the id tiebreak must preserve insertion order).
	for i, body := range []string{"first", "second", "third"} {
		c := &domain.Comment{TicketID: ticketID, Author: "Ana", Body: body,
			CreatedAt: testClock.Add(time.Duration(i) * time.Minute)}
		if err := s.CommentStore().Add(ctx, c); err != nil {
			t.Fatalf("add %s: %v", body, err)
		}
	}
	c := &domain.Comment{TicketID: ticketID, Author: "Bob", Body: "same-time",
		CreatedAt: testClock.Add(time.Minute)}
	if err := s.CommentStore().Add(ctx, c); err != nil {
		t.Fatalf("add same-time: %v", err)
	}

	got, err := s.CommentStore().ListByTicket(ctx, ticketID)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"first", "second", "same-time", "third"}
	for i := range want {
		if got[i].Body != want[i] {
			t.Errorf("comment[%d] = %q, want %q (creation order)", i, got[i].Body, want[i])
		}
	}
}

func TestCommentListScopedToTicket(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	cat := seedCategory(t, s, "Bugs")
	a := seedTicket(t, s, domain.Ticket{Number: 1, Title: "a", CategoryID: cat,
		Priority: domain.PriorityMedium, State: domain.StateNew,
		CreatedAt: testClock, UpdatedAt: testClock}).ID
	b := seedTicket(t, s, domain.Ticket{Number: 2, Title: "b", CategoryID: cat,
		Priority: domain.PriorityMedium, State: domain.StateNew,
		CreatedAt: testClock, UpdatedAt: testClock}).ID

	for _, c := range []domain.Comment{
		{TicketID: a, Author: "Ana", Body: "on a", CreatedAt: testClock},
		{TicketID: b, Author: "Bob", Body: "on b", CreatedAt: testClock},
	} {
		if err := s.CommentStore().Add(ctx, &c); err != nil {
			t.Fatal(err)
		}
	}

	got, err := s.CommentStore().ListByTicket(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Body != "on a" {
		t.Errorf("ticket a comments = %+v, want only [on a]", got)
	}
}

func TestCommentListByTicketEmpty(t *testing.T) {
	s := newTestDB(t)
	got, err := s.CommentStore().ListByTicket(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("len = %d, want 0", len(got))
	}
}

func TestCommentAddRejectsUnknownTicket(t *testing.T) {
	s := newTestDB(t)
	err := s.CommentStore().Add(context.Background(),
		&domain.Comment{TicketID: 999, Author: "Ana", Body: "x", CreatedAt: testClock})
	if err == nil || !isForeignKeyViolation(err) {
		t.Errorf("err = %v, want foreign key violation", err)
	}
}

func TestCommentAddRejectsEmptyBody(t *testing.T) {
	s := newTestDB(t)
	ticketID := seedTicketForTimeline(t, s, 1)
	err := s.CommentStore().Add(context.Background(),
		&domain.Comment{TicketID: ticketID, Author: "Ana", Body: "  ", CreatedAt: testClock})
	if err == nil {
		t.Fatal("empty body accepted, want CHECK constraint error")
	}
	if !strings.Contains(err.Error(), "CHECK") && !strings.Contains(err.Error(), "constraint") {
		t.Errorf("err = %v, want constraint error", err)
	}
}

func TestAuditAppendPersistsMultiEventBatch(t *testing.T) {
	s := newTestDB(t)
	ticketID := seedTicketForTimeline(t, s, 1)
	ctx := context.Background()

	// A mutation batch shares one created_at (same `now`); occurrence
	// order must be preserved by the id tiebreak (audit-log: history in
	// the order the events occurred).
	events := []domain.AuditEvent{
		{TicketID: ticketID, Actor: "Ana", Action: domain.ActionCreated, CreatedAt: testClock},
		{TicketID: ticketID, Actor: "Ana", Action: domain.ActionTransition,
			Field: ptr("state"), FromValue: ptr("new"), ToValue: ptr("in_progress"), CreatedAt: testClock},
		{TicketID: ticketID, Actor: "Bob", Action: domain.ActionUpdate,
			Field: ptr("priority"), FromValue: ptr("medium"), ToValue: ptr("high"),
			Note: ptr("reopen reason"), CreatedAt: testClock},
	}
	if err := s.AuditStore().Append(ctx, events...); err != nil {
		t.Fatalf("append: %v", err)
	}

	got, err := s.AuditStore().ListByTicket(ctx, ticketID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	wantActions := []string{domain.ActionCreated, domain.ActionTransition, domain.ActionUpdate}
	wantFields := []string{"", "state", "priority"}
	for i := range wantActions {
		if got[i].Action != wantActions[i] {
			t.Errorf("event[%d] action = %s, want %s", i, got[i].Action, wantActions[i])
		}
		if got[i].Field == nil && wantFields[i] != "" {
			t.Errorf("event[%d] field = nil, want %s", i, wantFields[i])
		}
		if got[i].Field != nil && *got[i].Field != wantFields[i] {
			t.Errorf("event[%d] field = %s, want %s", i, *got[i].Field, wantFields[i])
		}
	}
	if got[2].Actor != "Bob" || got[2].Note == nil || *got[2].Note != "reopen reason" {
		t.Errorf("event[2] = %+v, want Bob + note", got[2])
	}
	if !got[0].CreatedAt.Equal(testClock) {
		t.Errorf("event[0] created_at = %v, want %v", got[0].CreatedAt, testClock)
	}
}

// TestAuditRoundTripActorUserIDAndReason proves the S4 audit contract
// (design: "Events store session actor ID/snapshot"; migration 0003 adds
// audit_events.actor_user_id + reason): an event's session actor id and
// reason survive a full Append → ListByTicket round trip through the real
// store, and events without them read back as NULL.
func TestAuditRoundTripActorUserIDAndReason(t *testing.T) {
	s := newTestDB(t)
	ticketID := seedTicketForTimeline(t, s, 1)
	ctx := context.Background()

	// The actor_user_id column is a real FK: the actor must be a stored
	// user (migration 0003), so seed one through the port and use its id.
	actor := &domain.User{Name: "Ana", Email: "ana@example.com", Active: true, CreatedAt: testClock}
	if err := s.UserStore().Create(ctx, actor); err != nil {
		t.Fatalf("seed actor: %v", err)
	}
	actorID := actor.ID

	reason := "handoff to second-line"
	if err := s.AuditStore().Append(ctx, domain.AuditEvent{
		TicketID: ticketID, Actor: "Ana", ActorUserID: &actorID,
		Action: domain.ActionUpdate, Field: ptr("user"),
		FromValue: ptr(""), ToValue: ptr("2"),
		Reason: &reason, CreatedAt: testClock,
	}); err != nil {
		t.Fatalf("append with actor_user_id + reason: %v", err)
	}
	// Triangulation: an event WITHOUT actor id / reason must round-trip as
	// NULL, not as empty strings (legacy + backfill events carry no actor id).
	if err := s.AuditStore().Append(ctx, domain.AuditEvent{
		TicketID: ticketID, Actor: "Root", Action: domain.ActionCreated, CreatedAt: testClock,
	}); err != nil {
		t.Fatalf("append without actor fields: %v", err)
	}

	got, err := s.AuditStore().ListByTicket(ctx, ticketID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	first := got[0]
	if first.ActorUserID == nil || *first.ActorUserID != actorID {
		t.Errorf("event[0] ActorUserID = %v, want %d", first.ActorUserID, actorID)
	}
	if first.Reason == nil || *first.Reason != reason {
		t.Errorf("event[0] Reason = %v, want %q", first.Reason, reason)
	}
	if got[1].ActorUserID != nil || got[1].Reason != nil {
		t.Errorf("event[1] must round-trip NULL actor fields, got ActorUserID=%v Reason=%v", got[1].ActorUserID, got[1].Reason)
	}
}

func TestAuditAppendScopedToTicket(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	cat := seedCategory(t, s, "Bugs")
	a := seedTicket(t, s, domain.Ticket{Number: 1, Title: "a", CategoryID: cat,
		Priority: domain.PriorityMedium, State: domain.StateNew,
		CreatedAt: testClock, UpdatedAt: testClock}).ID
	b := seedTicket(t, s, domain.Ticket{Number: 2, Title: "b", CategoryID: cat,
		Priority: domain.PriorityMedium, State: domain.StateNew,
		CreatedAt: testClock, UpdatedAt: testClock}).ID

	if err := s.AuditStore().Append(ctx,
		domain.AuditEvent{TicketID: a, Actor: "Ana", Action: domain.ActionCreated, CreatedAt: testClock},
		domain.AuditEvent{TicketID: b, Actor: "Bob", Action: domain.ActionCreated, CreatedAt: testClock},
	); err != nil {
		t.Fatal(err)
	}

	got, err := s.AuditStore().ListByTicket(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Actor != "Ana" {
		t.Errorf("ticket a events = %+v, want only [Ana]", got)
	}
}

func TestAuditListByTicketEmpty(t *testing.T) {
	s := newTestDB(t)
	got, err := s.AuditStore().ListByTicket(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("len = %d, want 0", len(got))
	}
}

func TestAuditAppendRejectsUnknownTicket(t *testing.T) {
	s := newTestDB(t)
	err := s.AuditStore().Append(context.Background(),
		domain.AuditEvent{TicketID: 999, Actor: "Ana", Action: domain.ActionCreated, CreatedAt: testClock})
	if err == nil || !isForeignKeyViolation(err) {
		t.Errorf("err = %v, want foreign key violation", err)
	}
}
