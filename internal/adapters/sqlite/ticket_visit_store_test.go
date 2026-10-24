package sqlite

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// Issue #415: the visit store remembers how much of a ticket's history a user
// had on screen and answers which tickets changed for them since. A change is
// an audit event or a comment by someone else; internal comments count only
// for readers who may see them; a ticket never opened counts from creation.

type visitFixture struct {
	s        *Store
	ana, bob int64
	tickets  []int64
}

func newVisitFixture(t *testing.T, tickets int) visitFixture {
	t.Helper()
	s := newTestDB(t)
	f := visitFixture{s: s,
		ana: seedUser(t, s, "Ana", "ana@example.com", true),
		bob: seedUser(t, s, "Bob", "bob@example.com", true)}
	cat := seedCategory(t, s, "Bugs")
	for i := 1; i <= tickets; i++ {
		tk := seedTicket(t, s, domain.Ticket{Number: i, Title: "visit ticket", CategoryID: cat,
			Priority: domain.PriorityMedium, State: domain.StateNew, CreatedAt: testClock, UpdatedAt: testClock})
		f.tickets = append(f.tickets, tk.ID)
	}
	return f
}

// event appends one audit event by actor (nil for an automatic action).
func (f visitFixture) event(t *testing.T, ticketID int64, actor *int64) {
	t.Helper()
	if _, err := f.s.db.ExecContext(context.Background(),
		`INSERT INTO audit_events (ticket_id, actor, action, actor_user_id, created_at) VALUES (?, 'x', 'update', ?, ?)`,
		ticketID, nullableInt64(actor), formatTime(testClock)); err != nil {
		t.Fatalf("seed audit event: %v", err)
	}
}

// comment appends one comment by author with the given visibility.
func (f visitFixture) comment(t *testing.T, ticketID, author int64, visibility string) int64 {
	t.Helper()
	res, err := f.s.db.ExecContext(context.Background(),
		`INSERT INTO comments (ticket_id, author, body, visibility, author_user_id, created_at) VALUES (?, 'x', 'hello', ?, ?, ?)`,
		ticketID, visibility, author, formatTime(testClock))
	if err != nil {
		t.Fatalf("seed comment: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("comment id: %v", err)
	}
	return id
}

// visit records a visit by user with the ticket's current mark.
func (f visitFixture) visit(t *testing.T, user, ticketID int64) {
	t.Helper()
	ctx := context.Background()
	store := f.s.TicketVisitStore()
	mark, err := store.Mark(ctx, ticketID)
	if err != nil {
		t.Fatalf("Mark(%d): %v", ticketID, err)
	}
	if err := store.RecordVisit(ctx, user, ticketID, mark, testClock); err != nil {
		t.Fatalf("RecordVisit(%d, %d): %v", user, ticketID, err)
	}
}

func (f visitFixture) changed(t *testing.T, user int64, includeInternal bool) map[int64]bool {
	t.Helper()
	got, err := f.s.TicketVisitStore().Changed(context.Background(), user, f.tickets, includeInternal)
	if err != nil {
		t.Fatalf("Changed(%d): %v", user, err)
	}
	return got
}

func TestVisitNeverOpenedTicketCountsActivityBySomeoneElse(t *testing.T) {
	f := newVisitFixture(t, 3)
	f.event(t, f.tickets[0], ptr(f.bob)) // Bob's change: new for Ana
	f.event(t, f.tickets[1], ptr(f.ana)) // Ana's own change: never new for her
	// tickets[2] has no history at all.

	want := map[int64]bool{f.tickets[0]: true}
	if got := f.changed(t, f.ana, true); !reflect.DeepEqual(got, want) {
		t.Fatalf("Ana changed = %v, want %v", got, want)
	}
	want = map[int64]bool{f.tickets[1]: true}
	if got := f.changed(t, f.bob, true); !reflect.DeepEqual(got, want) {
		t.Fatalf("Bob changed = %v, want %v", got, want)
	}
}

func TestVisitClearsTheMarkUntilSomeoneElseActsAgain(t *testing.T) {
	f := newVisitFixture(t, 1)
	tk := f.tickets[0]
	f.comment(t, tk, f.bob, "public")
	f.visit(t, f.ana, tk)
	if got := f.changed(t, f.ana, true); len(got) != 0 {
		t.Fatalf("after the visit changed = %v, want none", got)
	}

	f.event(t, tk, ptr(f.ana))
	f.comment(t, tk, f.ana, "public")
	if got := f.changed(t, f.ana, true); len(got) != 0 {
		t.Fatalf("after Ana's own activity changed = %v, want none", got)
	}

	f.event(t, tk, nil) // an automatic action has no actor and still counts
	if got := f.changed(t, f.ana, true); !got[tk] {
		t.Fatalf("after an automatic action changed = %v, want ticket %d", got, tk)
	}
	f.visit(t, f.ana, tk)
	f.comment(t, tk, f.bob, "public")
	if got := f.changed(t, f.ana, true); !got[tk] {
		t.Fatalf("after Bob's comment changed = %v, want ticket %d", got, tk)
	}
}

func TestVisitInternalCommentsCountOnlyForReadersWhoSeeThem(t *testing.T) {
	f := newVisitFixture(t, 1)
	tk := f.tickets[0]
	f.visit(t, f.ana, tk)
	f.comment(t, tk, f.bob, "internal")

	if got := f.changed(t, f.ana, false); len(got) != 0 {
		t.Fatalf("requester view changed = %v, want none for an internal note", got)
	}
	if got := f.changed(t, f.ana, true); !got[tk] {
		t.Fatalf("staff view changed = %v, want ticket %d", got, tk)
	}
}

func TestVisitMarksOnlyThatTicketsNotificationsOfThatUserRead(t *testing.T) {
	f := newVisitFixture(t, 2)
	insertNotifications(t, f.s,
		domain.Notification{UserID: f.ana, TicketID: f.tickets[0], Kind: domain.NotificationAssigned, ActorUserID: &f.bob, CreatedAt: testClock},
		domain.Notification{UserID: f.ana, TicketID: f.tickets[1], Kind: domain.NotificationAssigned, ActorUserID: &f.bob, CreatedAt: testClock},
		domain.Notification{UserID: f.bob, TicketID: f.tickets[0], Kind: domain.NotificationAssigned, ActorUserID: &f.ana, CreatedAt: testClock},
	)
	visitedAt := testClock.Add(time.Hour)
	ctx := context.Background()
	mark, err := f.s.TicketVisitStore().Mark(ctx, f.tickets[0])
	if err != nil {
		t.Fatalf("Mark: %v", err)
	}
	if err := f.s.TicketVisitStore().RecordVisit(ctx, f.ana, f.tickets[0], mark, visitedAt); err != nil {
		t.Fatalf("RecordVisit: %v", err)
	}

	unread := func(user int64) map[int64]bool {
		rows, err := f.s.NotificationStore().ListForUser(ctx, user, 50)
		if err != nil {
			t.Fatalf("ListForUser: %v", err)
		}
		out := map[int64]bool{}
		for _, n := range rows {
			if n.ReadAt == nil {
				out[n.TicketID] = true
			} else if !n.ReadAt.Equal(visitedAt) {
				t.Fatalf("notification %d read at %v, want %v", n.ID, n.ReadAt, visitedAt)
			}
		}
		return out
	}
	if got, want := unread(f.ana), map[int64]bool{f.tickets[1]: true}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Ana unread tickets = %v, want %v", got, want)
	}
	if got, want := unread(f.bob), map[int64]bool{f.tickets[0]: true}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Bob unread tickets = %v, want %v", got, want)
	}
}

func TestVisitMarkNeverMovesBackwards(t *testing.T) {
	f := newVisitFixture(t, 1)
	tk := f.tickets[0]
	ctx := context.Background()
	store := f.s.TicketVisitStore()
	stale, err := store.Mark(ctx, tk)
	if err != nil {
		t.Fatalf("Mark: %v", err)
	}
	f.comment(t, tk, f.bob, "public")
	f.visit(t, f.ana, tk)
	// A slower concurrent open that read the ticket before Bob's comment
	// finishes last: it must not bring Bob's comment back as new.
	if err := store.RecordVisit(ctx, f.ana, tk, stale, testClock); err != nil {
		t.Fatalf("RecordVisit(stale): %v", err)
	}
	if got := f.changed(t, f.ana, true); len(got) != 0 {
		t.Fatalf("changed = %v, want none after a stale visit", got)
	}
}

func TestVisitChangedWithNoTicketsIsEmpty(t *testing.T) {
	f := newVisitFixture(t, 0)
	got, err := f.s.TicketVisitStore().Changed(context.Background(), f.ana, nil, true)
	if err != nil || len(got) != 0 {
		t.Fatalf("Changed(nil) = %v, %v; want empty, nil", got, err)
	}
}
