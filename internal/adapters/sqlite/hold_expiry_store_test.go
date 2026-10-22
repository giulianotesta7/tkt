package sqlite

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// The automatic hold expiry store (issue #353): the bounded read of the held
// tickets one pass walks, the "was the reminder already sent for this hold"
// check, and the guarded reminder write. They run against the real modernc
// driver and the real embedded migrations, like every other adapter test.

// holdAt puts a seeded ticket on hold at instant since, in the stored form the
// hold columns use (test arrange: the read path under test must not be the one
// that created its own fixture).
func holdAt(t *testing.T, s *Store, ticketID int64, since time.Time) {
	t.Helper()
	if _, err := s.db.ExecContext(context.Background(),
		`UPDATE tickets SET on_hold_since = ?, on_hold_reason = 'waiting for the requester' WHERE id = ?`,
		formatTime(since), ticketID); err != nil {
		t.Fatalf("hold ticket %d: %v", ticketID, err)
	}
}

// TestHeldTicketsReturnsOnlyOpenHeldTicketsInIDOrder pins the read one pass
// walks: the OPEN tickets that are on hold, ordered by id, and nothing else —
// a ticket that is not on hold is not due, and neither is a ticket that has
// already reached a terminal state.
func TestHeldTicketsReturnsOnlyOpenHeldTicketsInIDOrder(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	cat := seedCategory(t, s, "Bugs")
	seed := func(number int, state domain.State) domain.Ticket {
		return seedTicket(t, s, domain.Ticket{Number: number, Title: fmt.Sprintf("ticket %d", number), CategoryID: cat,
			Priority: domain.PriorityMedium, State: state, CreatedAt: testClock, UpdatedAt: testClock})
	}
	inProgress := seed(1, domain.StateInProgress)
	fresh := seed(2, domain.StateNew)
	seed(3, domain.StateInProgress) // not held: not due
	resolved := seed(4, domain.StateResolved)
	cancelled := seed(5, domain.StateCancelled)
	since := testClock.Add(-48 * time.Hour)
	holdAt(t, s, inProgress.ID, since)
	holdAt(t, s, fresh.ID, since.Add(time.Hour))
	// A terminal row can only carry a hold through a hand-edited database, and
	// the pass must still not resume-and-close a ticket that is already closed.
	holdAt(t, s, resolved.ID, since)
	holdAt(t, s, cancelled.ID, since)

	held, err := s.HoldExpiryStore().HeldTickets(ctx, 0, 100)
	if err != nil {
		t.Fatalf("HeldTickets: %v", err)
	}
	if len(held) != 2 {
		t.Fatalf("HeldTickets = %d rows, want the 2 open held tickets", len(held))
	}
	if held[0].ID != inProgress.ID || held[1].ID != fresh.ID {
		t.Errorf("HeldTickets ids = [%d %d], want [%d %d] (ordered by id)", held[0].ID, held[1].ID, inProgress.ID, fresh.ID)
	}
	if held[0].OnHoldSince == nil || !held[0].OnHoldSince.Equal(since) {
		t.Errorf("OnHoldSince = %v, want %v", held[0].OnHoldSince, since)
	}
	if held[0].OnHoldReason != "waiting for the requester" {
		t.Errorf("OnHoldReason = %q, want the stored reason", held[0].OnHoldReason)
	}
	if held[0].State != domain.StateInProgress || held[1].State != domain.StateNew {
		t.Errorf("states = %q %q, want in_progress new", held[0].State, held[1].State)
	}
}

// TestHeldTicketsPagesByID pins the keyset page the pass walks: at most limit
// tickets with an id above afterID, in id order, so a page full of holds that
// are not due never hides the tickets behind it.
func TestHeldTicketsPagesByID(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	cat := seedCategory(t, s, "Bugs")
	var ids []int64
	for i := 0; i < 5; i++ {
		tk := seedTicket(t, s, domain.Ticket{Number: i + 1, Title: fmt.Sprintf("held %d", i), CategoryID: cat,
			Priority: domain.PriorityLow, State: domain.StateInProgress, CreatedAt: testClock, UpdatedAt: testClock})
		holdAt(t, s, tk.ID, testClock)
		ids = append(ids, tk.ID)
	}
	first, err := s.HoldExpiryStore().HeldTickets(ctx, 0, 2)
	if err != nil || len(first) != 2 || first[0].ID != ids[0] || first[1].ID != ids[1] {
		t.Fatalf("first page = %v, %v, want ids %v", first, err, ids[:2])
	}
	next, err := s.HoldExpiryStore().HeldTickets(ctx, first[1].ID, 2)
	if err != nil || len(next) != 2 || next[0].ID != ids[2] || next[1].ID != ids[3] {
		t.Fatalf("second page = %v, %v, want ids %v", next, err, ids[2:4])
	}
	last, err := s.HoldExpiryStore().HeldTickets(ctx, next[1].ID, 2)
	if err != nil || len(last) != 1 || last[0].ID != ids[4] {
		t.Fatalf("last page = %v, %v, want id %d", last, err, ids[4])
	}
}

// TestReminderSentIsScopedToTheHold pins the once-per-hold check: only a
// hold_reminder row for THIS ticket created at or after the hold instant counts,
// so a reminder from an earlier hold (before a reply resumed it) never
// suppresses the new hold's reminder.
func TestReminderSentIsScopedToTheHold(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	ana, ticketID := notificationFixture(t, s)
	otherCategory := seedCategory(t, s, "Other")
	otherTicketID := seedTicket(t, s, domain.Ticket{Number: 2, Title: "other ticket", CategoryID: otherCategory,
		Priority: domain.PriorityMedium, State: domain.StateNew, CreatedAt: testClock, UpdatedAt: testClock}).ID
	since := testClock.Add(-24 * time.Hour)

	sent, err := s.HoldExpiryStore().ReminderSent(ctx, ticketID, since)
	if err != nil {
		t.Fatalf("ReminderSent with no rows: %v", err)
	}
	if sent {
		t.Error("ReminderSent = true with no notification at all, want false")
	}

	insertNotifications(t, s, domain.Notification{UserID: ana, TicketID: ticketID,
		Kind: domain.NotificationOnHold, CreatedAt: since.Add(time.Hour)})
	sent, err = s.HoldExpiryStore().ReminderSent(ctx, ticketID, since)
	if err != nil {
		t.Fatalf("ReminderSent with another kind: %v", err)
	}
	if sent {
		t.Error("ReminderSent = true for an on_hold row, want false (only a reminder counts)")
	}

	// A reminder of an EARLIER hold (created before this hold began) is not
	// this hold's reminder.
	insertNotifications(t, s, domain.Notification{UserID: ana, TicketID: ticketID,
		Kind: domain.NotificationHoldReminder, CreatedAt: since.Add(-time.Minute)})
	sent, err = s.HoldExpiryStore().ReminderSent(ctx, ticketID, since)
	if err != nil {
		t.Fatalf("ReminderSent with an older reminder: %v", err)
	}
	if sent {
		t.Error("ReminderSent = true for a reminder written before the hold began, want false")
	}

	insertNotifications(t, s, domain.Notification{UserID: ana, TicketID: otherTicketID,
		Kind: domain.NotificationHoldReminder, CreatedAt: since})
	sent, err = s.HoldExpiryStore().ReminderSent(ctx, ticketID, since)
	if err != nil {
		t.Fatalf("ReminderSent with another ticket's reminder: %v", err)
	}
	if sent {
		t.Error("ReminderSent = true for another ticket's reminder, want false")
	}

	// The boundary counts: a reminder written exactly at the hold instant is
	// this hold's reminder.
	insertNotifications(t, s, domain.Notification{UserID: ana, TicketID: ticketID,
		Kind: domain.NotificationHoldReminder, CreatedAt: since})
	sent, err = s.HoldExpiryStore().ReminderSent(ctx, ticketID, since)
	if err != nil {
		t.Fatalf("ReminderSent at the boundary: %v", err)
	}
	if !sent {
		t.Error("ReminderSent = false for a reminder written at the hold instant, want true")
	}
}

// TestRemindHoldAppendsTheNotificationAndKeepsTheHold pins the guarded write:
// while the stored hold is still the one the caller read, the reminder row is
// appended — and the hold columns are untouched, because a reminder changes
// nothing about the hold it describes.
func TestRemindHoldAppendsTheNotificationAndKeepsTheHold(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	cat := seedCategory(t, s, "Bugs")
	requester := seedUser(t, s, "Ana", "ana@example.com", true)
	since := testClock.Add(-24 * time.Hour)
	tk := seedTicket(t, s, domain.Ticket{Number: 1, Title: "held", CategoryID: cat,
		Priority: domain.PriorityMedium, State: domain.StateInProgress,
		RequesterUserID: &requester, CreatedAt: testClock, UpdatedAt: testClock})
	holdAt(t, s, tk.ID, since)

	reminder := domain.Notification{UserID: requester, TicketID: tk.ID,
		Kind: domain.NotificationHoldReminder, CreatedAt: testClock}
	if err := s.TicketUnitOfWork().RemindHold(ctx, tk.ID, since, []domain.Notification{reminder}); err != nil {
		t.Fatalf("RemindHold: %v", err)
	}
	var kind, createdAt string
	if err := s.db.QueryRowContext(ctx, `SELECT kind, created_at FROM notifications WHERE ticket_id = ?`, tk.ID).Scan(&kind, &createdAt); err != nil {
		t.Fatalf("read reminder: %v", err)
	}
	if kind != string(domain.NotificationHoldReminder) {
		t.Errorf("stored kind = %q, want %q", kind, domain.NotificationHoldReminder)
	}
	if createdAt != formatTime(testClock) {
		t.Errorf("stored created_at = %q, want %q", createdAt, formatTime(testClock))
	}
	var holdSince string
	if err := s.db.QueryRowContext(ctx, `SELECT on_hold_since FROM tickets WHERE id = ?`, tk.ID).Scan(&holdSince); err != nil {
		t.Fatalf("read hold: %v", err)
	}
	if holdSince != formatTime(since) {
		t.Errorf("on_hold_since = %q, want the unchanged %q", holdSince, formatTime(since))
	}
	var emails int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM notification_emails`).Scan(&emails); err != nil {
		t.Fatal(err)
	}
	if emails != 1 {
		t.Errorf("notification_emails rows = %d, want the reminder's pending delivery row", emails)
	}
}

// TestRemindHoldStaleHoldWritesNothing pins the compare-and-set half: a hold
// the database already cleared (the requester replied meanwhile) refuses the
// reminder with a *domain.HoldConflictError and writes nothing at all.
func TestRemindHoldStaleHoldWritesNothing(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	cat := seedCategory(t, s, "Bugs")
	requester := seedUser(t, s, "Ana", "ana@example.com", true)
	since := testClock.Add(-24 * time.Hour)
	tk := seedTicket(t, s, domain.Ticket{Number: 1, Title: "held", CategoryID: cat,
		Priority: domain.PriorityMedium, State: domain.StateInProgress,
		RequesterUserID: &requester, CreatedAt: testClock, UpdatedAt: testClock})
	holdAt(t, s, tk.ID, since.Add(time.Minute)) // the stored hold moved on

	reminder := domain.Notification{UserID: requester, TicketID: tk.ID,
		Kind: domain.NotificationHoldReminder, CreatedAt: testClock}
	err := s.TicketUnitOfWork().RemindHold(ctx, tk.ID, since, []domain.Notification{reminder})
	var conflict *domain.HoldConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("RemindHold with a stale hold = %v, want *domain.HoldConflictError", err)
	}
	var notifications int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM notifications`).Scan(&notifications); err != nil {
		t.Fatal(err)
	}
	if notifications != 0 {
		t.Errorf("notifications = %d after a refused reminder, want 0", notifications)
	}
	var emails int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM notification_emails`).Scan(&emails); err != nil {
		t.Fatal(err)
	}
	if emails != 0 {
		t.Errorf("notification_emails = %d after a refused reminder, want 0", emails)
	}
	// A hold cleared between the read and the write (the requester replied) is
	// the same conflict, not a missing ticket.
	if _, err := s.db.ExecContext(ctx, `UPDATE tickets SET on_hold_since = NULL, on_hold_reason = '' WHERE id = ?`, tk.ID); err != nil {
		t.Fatal(err)
	}
	err = s.TicketUnitOfWork().RemindHold(ctx, tk.ID, since, []domain.Notification{reminder})
	if !errors.As(err, &conflict) {
		t.Fatalf("RemindHold after the hold was cleared = %v, want *domain.HoldConflictError", err)
	}
}
