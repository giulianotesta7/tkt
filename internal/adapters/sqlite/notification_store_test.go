package sqlite

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// Issue #344 slice B1: the notification store and its transaction-level
// append helper. The store answers the bell's unread count, the panel's
// newest-first list, and the read/mark-all writes — always scoped to the
// recipient, so one user's action can never touch another user's rows.

// notificationFixture seeds one user plus one ticket and returns both ids.
func notificationFixture(t *testing.T, s *Store) (userID, ticketID int64) {
	t.Helper()
	userID = seedUser(t, s, "Ana", "ana@example.com", true)
	cat := seedCategory(t, s, "Bugs")
	tk := seedTicket(t, s, domain.Ticket{Number: 1, Title: "notification ticket", CategoryID: cat,
		Priority: domain.PriorityMedium, State: domain.StateNew, CreatedAt: testClock, UpdatedAt: testClock})
	return userID, tk.ID
}

// insertNotifications is the arrange path for every store test: it goes
// through the same tx-level helper B2/B3 will call, inside its own
// transaction, never through hand-written SQL.
func insertNotifications(t *testing.T, s *Store, notifications ...domain.Notification) {
	t.Helper()
	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin arrange tx: %v", err)
	}
	defer tx.Rollback()
	if err := appendNotificationsTx(ctx, tx, notifications); err != nil {
		t.Fatalf("append notifications: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit arrange tx: %v", err)
	}
}

// readAtOf reads one row's stored read_at THROUGH the port, as the recipient:
// the row-scoping guarantee is exactly what ListForUser answers, so no raw SQL
// is needed to assert it.
func readAtOf(t *testing.T, s *Store, userID, id int64) *time.Time {
	t.Helper()
	rows, err := s.NotificationStore().ListForUser(context.Background(), userID, 50)
	if err != nil {
		t.Fatalf("ListForUser(%d): %v", userID, err)
	}
	for _, n := range rows {
		if n.ID == id {
			return n.ReadAt
		}
	}
	t.Fatalf("notification %d not visible to user %d", id, userID)
	return nil
}

func TestNotificationListForUserNewestFirst(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	ana, ticketID := notificationFixture(t, s)
	bob := seedUser(t, s, "Bob", "bob@example.com", true)

	base := testClock
	// The comment facts point at a real comment: the foreign key refuses a
	// notification whose comment does not exist.
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO comments (ticket_id, author, body, created_at) VALUES (?, 'Ana', 'hello', ?)`,
		ticketID, formatTime(base))
	if err != nil {
		t.Fatalf("seed comment: %v", err)
	}
	commentID, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	// Same-instant rows are ordered by id DESC, so insertion order still
	// reads newest-first inside one batch (one ticket change, several rows).
	insertNotifications(t, s,
		domain.Notification{UserID: ana, TicketID: ticketID, Kind: domain.NotificationAssigned,
			CreatedAt: base},
		domain.Notification{UserID: ana, TicketID: ticketID, Kind: domain.NotificationStateChanged,
			FromValue: notifStr("new"), ToValue: notifStr("in_progress"), CreatedAt: base.Add(time.Minute)},
		domain.Notification{UserID: ana, TicketID: ticketID, Kind: domain.NotificationCommentPublic,
			CommentID: &commentID, FromValue: notifStr("ignored"), CreatedAt: base.Add(2 * time.Minute)},
		domain.Notification{UserID: bob, TicketID: ticketID, Kind: domain.NotificationAssigned,
			CreatedAt: base.Add(3 * time.Minute)},
	)

	got, err := s.NotificationStore().ListForUser(ctx, ana, 10)
	if err != nil {
		t.Fatalf("ListForUser: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("ListForUser returned %d rows, want 3 (Bob's row must never leak)", len(got))
	}
	wantKinds := []domain.NotificationKind{
		domain.NotificationCommentPublic,
		domain.NotificationStateChanged,
		domain.NotificationAssigned,
	}
	for i, want := range wantKinds {
		if got[i].Kind != want {
			t.Errorf("row %d kind = %q, want %q (newest first)", i, got[i].Kind, want)
		}
		if got[i].UserID != ana {
			t.Errorf("row %d user_id = %d, want the recipient %d", i, got[i].UserID, ana)
		}
		if got[i].ID == 0 {
			t.Errorf("row %d has no id", i)
		}
	}
	if got[1].FromValue == nil || *got[1].FromValue != "new" || got[1].ToValue == nil || *got[1].ToValue != "in_progress" {
		t.Errorf("state change facts = (%v, %v), want (new, in_progress)", got[1].FromValue, got[1].ToValue)
	}
	if got[0].CommentID == nil || *got[0].CommentID != commentID {
		t.Errorf("comment id = %v, want %d", got[0].CommentID, commentID)
	}
	if got[0].ReadAt != nil {
		t.Errorf("fresh row read_at = %v, want nil", got[0].ReadAt)
	}

	limited, err := s.NotificationStore().ListForUser(ctx, ana, 2)
	if err != nil {
		t.Fatalf("ListForUser limited: %v", err)
	}
	if len(limited) != 2 {
		t.Fatalf("limited ListForUser returned %d rows, want 2", len(limited))
	}
	if limited[0].Kind != domain.NotificationCommentPublic || limited[1].Kind != domain.NotificationStateChanged {
		t.Errorf("limited list = [%s %s], want the two newest", limited[0].Kind, limited[1].Kind)
	}

	empty, err := s.NotificationStore().ListForUser(ctx, seedUser(t, s, "Cara", "cara@example.com", true), 10)
	if err != nil {
		t.Fatalf("ListForUser for a user with no rows: %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("ListForUser without rows = %d rows, want 0", len(empty))
	}
}

func TestNotificationUnreadCount(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	ana, ticketID := notificationFixture(t, s)
	bob := seedUser(t, s, "Bob", "bob@example.com", true)

	insertNotifications(t, s,
		domain.Notification{UserID: ana, TicketID: ticketID, Kind: domain.NotificationAssigned, CreatedAt: testClock},
		domain.Notification{UserID: ana, TicketID: ticketID, Kind: domain.NotificationStateChanged, CreatedAt: testClock.Add(time.Minute)},
		domain.Notification{UserID: bob, TicketID: ticketID, Kind: domain.NotificationAssigned, CreatedAt: testClock.Add(2 * time.Minute)},
	)

	count, err := s.NotificationStore().UnreadCount(ctx, ana)
	if err != nil {
		t.Fatalf("UnreadCount: %v", err)
	}
	if count != 2 {
		t.Errorf("UnreadCount(ana) = %d, want 2 (only her unread rows)", count)
	}

	if err := s.NotificationStore().MarkAllRead(ctx, ana, testClock.Add(time.Hour)); err != nil {
		t.Fatalf("MarkAllRead: %v", err)
	}
	if count, err = s.NotificationStore().UnreadCount(ctx, ana); err != nil {
		t.Fatalf("UnreadCount after mark all: %v", err)
	} else if count != 0 {
		t.Errorf("UnreadCount(ana) after mark all = %d, want 0", count)
	}
	if count, err = s.NotificationStore().UnreadCount(ctx, bob); err != nil {
		t.Fatalf("UnreadCount(bob): %v", err)
	} else if count != 1 {
		t.Errorf("UnreadCount(bob) after ana marked all = %d, want 1 — another user's rows are untouched", count)
	}
}

func TestNotificationMarkRead(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	ana, ticketID := notificationFixture(t, s)
	insertNotifications(t, s,
		domain.Notification{UserID: ana, TicketID: ticketID, Kind: domain.NotificationAssigned, CreatedAt: testClock},
	)
	rows, err := s.NotificationStore().ListForUser(ctx, ana, 10)
	if err != nil || len(rows) != 1 {
		t.Fatalf("ListForUser = %v (%v), want 1 row", rows, err)
	}
	id := rows[0].ID

	readAt := testClock.Add(5 * time.Minute)
	if err := s.NotificationStore().MarkRead(ctx, ana, id, readAt); err != nil {
		t.Fatalf("MarkRead(own): %v", err)
	}
	if got := readAtOf(t, s, ana, id); got == nil || !got.Equal(readAt) {
		t.Fatalf("stored read_at = %v, want %v", got, readAt)
	}
	if count, err := s.NotificationStore().UnreadCount(ctx, ana); err != nil {
		t.Fatalf("UnreadCount: %v", err)
	} else if count != 0 {
		t.Errorf("UnreadCount after MarkRead = %d, want 0", count)
	}

	// Marking again is idempotent and keeps the FIRST instant: the panel can
	// re-open a notification without moving the "when" it shows.
	if err := s.NotificationStore().MarkRead(ctx, ana, id, readAt.Add(time.Hour)); err != nil {
		t.Fatalf("MarkRead again: %v", err)
	}
	if got := readAtOf(t, s, ana, id); got == nil || !got.Equal(readAt) {
		t.Errorf("stored read_at after second mark = %v, want the first instant %v", got, readAt)
	}
}

// TestNotificationMarkReadNeverTouchesAnotherUser is the row-scoping guard:
// Bob's id is a not-found for Ana, exactly like a missing row, and Bob's row
// stays unread.
func TestNotificationMarkReadNeverTouchesAnotherUser(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	_, ticketID := notificationFixture(t, s)
	ana := seedUser(t, s, "Ana", "ana2@example.com", true)
	bob := seedUser(t, s, "Bob", "bob@example.com", true)

	insertNotifications(t, s,
		domain.Notification{UserID: bob, TicketID: ticketID, Kind: domain.NotificationAssigned, CreatedAt: testClock},
	)
	rows, err := s.NotificationStore().ListForUser(ctx, bob, 10)
	if err != nil || len(rows) != 1 {
		t.Fatalf("ListForUser(bob) = %v (%v), want 1 row", rows, err)
	}
	bobID := rows[0].ID

	err = s.NotificationStore().MarkRead(ctx, ana, bobID, testClock.Add(time.Hour))
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("MarkRead(another user's row) = %v, want domain.ErrNotFound", err)
	}
	if got := readAtOf(t, s, bob, bobID); got != nil {
		t.Errorf("Bob's read_at = %v after Ana's attempt, want nil — no cross-user write", *got)
	}
	if count, err := s.NotificationStore().UnreadCount(ctx, bob); err != nil {
		t.Fatalf("UnreadCount(bob): %v", err)
	} else if count != 1 {
		t.Errorf("UnreadCount(bob) = %d after Ana's attempt, want 1", count)
	}

	// A missing id is the same not-found, so the shape never reveals whether
	// some other user's notification exists.
	if err := s.NotificationStore().MarkRead(ctx, ana, bobID+999, testClock.Add(time.Hour)); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("MarkRead(missing id) = %v, want domain.ErrNotFound", err)
	}
}

func TestNotificationMarkAllReadIsScopedToTheRecipient(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	ana, ticketID := notificationFixture(t, s)
	bob := seedUser(t, s, "Bob", "bob@example.com", true)

	insertNotifications(t, s,
		domain.Notification{UserID: ana, TicketID: ticketID, Kind: domain.NotificationAssigned, CreatedAt: testClock},
		domain.Notification{UserID: ana, TicketID: ticketID, Kind: domain.NotificationStateChanged, CreatedAt: testClock.Add(time.Minute)},
		domain.Notification{UserID: bob, TicketID: ticketID, Kind: domain.NotificationAssigned, CreatedAt: testClock.Add(2 * time.Minute)},
	)

	at := testClock.Add(3 * time.Hour)
	if err := s.NotificationStore().MarkAllRead(ctx, ana, at); err != nil {
		t.Fatalf("MarkAllRead: %v", err)
	}
	anaRows, err := s.NotificationStore().ListForUser(ctx, ana, 10)
	if err != nil {
		t.Fatalf("ListForUser(ana): %v", err)
	}
	for _, n := range anaRows {
		if n.ReadAt == nil {
			t.Errorf("ana row %d still unread after MarkAllRead", n.ID)
		} else if !n.ReadAt.Equal(at) {
			t.Errorf("ana row %d read_at = %v, want %v", n.ID, n.ReadAt, at)
		}
	}
	bobRows, err := s.NotificationStore().ListForUser(ctx, bob, 10)
	if err != nil {
		t.Fatalf("ListForUser(bob): %v", err)
	}
	if len(bobRows) != 1 || bobRows[0].ReadAt != nil {
		t.Errorf("bob rows after ana's MarkAllRead = %+v, want one unread row", bobRows)
	}
}

// TestNotificationsCascadeOnTicketDelete proves a notification never outlives
// its ticket: the panel can only open tickets that still exist.
func TestNotificationsCascadeOnTicketDelete(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	ana, ticketID := notificationFixture(t, s)
	insertNotifications(t, s,
		domain.Notification{UserID: ana, TicketID: ticketID, Kind: domain.NotificationAssigned, CreatedAt: testClock},
		domain.Notification{UserID: ana, TicketID: ticketID, Kind: domain.NotificationStateChanged, CreatedAt: testClock.Add(time.Minute)},
	)

	if _, err := s.db.ExecContext(ctx, `DELETE FROM tickets WHERE id = ?`, ticketID); err != nil {
		t.Fatalf("delete ticket: %v", err)
	}
	var remaining int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM notifications WHERE user_id = ?`, ana).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Errorf("%d notifications survived the ticket delete, want 0 (ON DELETE CASCADE)", remaining)
	}
}

// TestAppendNotificationsTxIsTransactional is the S11 half B2/B3 rely on: the
// append runs on the caller's transaction, so a rolled-back ticket change
// leaves no notification behind, and a commit keeps every row of the batch.
func TestAppendNotificationsTxIsTransactional(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	ana, ticketID := notificationFixture(t, s)
	batch := []domain.Notification{
		{UserID: ana, TicketID: ticketID, Kind: domain.NotificationStateChanged, CreatedAt: testClock},
		{UserID: ana, TicketID: ticketID, Kind: domain.NotificationPriorityChanged,
			FromValue: notifStr("medium"), ToValue: notifStr("high"), CreatedAt: testClock},
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	if err := appendNotificationsTx(ctx, tx, batch); err != nil {
		t.Fatalf("appendNotificationsTx: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if count, err := s.NotificationStore().UnreadCount(ctx, ana); err != nil {
		t.Fatalf("UnreadCount after rollback: %v", err)
	} else if count != 0 {
		t.Fatalf("UnreadCount after rollback = %d, want 0 — a failed change leaves no notification", count)
	}

	insertNotifications(t, s, batch...)
	if count, err := s.NotificationStore().UnreadCount(ctx, ana); err != nil {
		t.Fatalf("UnreadCount after commit: %v", err)
	} else if count != 2 {
		t.Fatalf("UnreadCount after commit = %d, want 2", count)
	}
}

// TestAppendNotificationsTxRefusesInvalidRows proves the domain guard runs
// before any insert, so a policy bug cannot persist a self-notification: the
// whole batch is refused and the caller's rollback leaves the table empty.
func TestAppendNotificationsTxRefusesInvalidRows(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	ana, ticketID := notificationFixture(t, s)
	actor := ana

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer tx.Rollback()
	err = appendNotificationsTx(ctx, tx, []domain.Notification{
		{UserID: ana, TicketID: ticketID, Kind: domain.NotificationAssigned, CreatedAt: testClock},
		{UserID: ana, TicketID: ticketID, Kind: domain.NotificationAssigned, ActorUserID: &actor, CreatedAt: testClock},
	})
	if err == nil {
		t.Fatal("appendNotificationsTx accepted a self-notification, want a validation error")
	}
	var ie *domain.NotificationInvariantError
	if !errors.As(err, &ie) || ie.Field != "actor_user_id" {
		t.Errorf("append error = %v, want a *domain.NotificationInvariantError on actor_user_id", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if count, err := s.NotificationStore().UnreadCount(ctx, ana); err != nil {
		t.Fatalf("UnreadCount: %v", err)
	} else if count != 0 {
		t.Errorf("UnreadCount = %d after a refused batch, want 0 — nothing was inserted", count)
	}
}

// TestAppendNotificationsTxEmptyBatchIsNoOp: a ticket change with no
// recipients (the actor did it, or nobody can open the ticket) writes no
// notification row and is not an error.
func TestAppendNotificationsTxEmptyBatchIsNoOp(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	ana, _ := notificationFixture(t, s)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer tx.Rollback()
	if err := appendNotificationsTx(ctx, tx, nil); err != nil {
		t.Fatalf("appendNotificationsTx(nil) = %v, want nil", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if count, err := s.NotificationStore().UnreadCount(ctx, ana); err != nil {
		t.Fatalf("UnreadCount: %v", err)
	} else if count != 0 {
		t.Errorf("UnreadCount = %d for an empty batch, want 0", count)
	}
}

// TestNotificationFactsRoundTrip proves the structured facts survive the write
// and read unchanged: the actor (NULL for automatic events), the workflow step
// index of a form_waiting row, and the change values. Nothing is rendered text
// (S15), so these columns are the whole payload the panel renders from.
func TestNotificationFactsRoundTrip(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	ana, ticketID := notificationFixture(t, s)
	actor := seedUser(t, s, "Bob", "bob@example.com", true)
	step := 2

	insertNotifications(t, s,
		domain.Notification{UserID: ana, TicketID: ticketID, Kind: domain.NotificationFormWaiting,
			ActorUserID: nil, StepIndex: &step, CreatedAt: testClock},
		domain.Notification{UserID: ana, TicketID: ticketID, Kind: domain.NotificationPriorityChanged,
			ActorUserID: &actor, FromValue: notifStr("low"), ToValue: notifStr("critical"),
			CreatedAt: testClock.Add(time.Minute)},
	)

	rows, err := s.NotificationStore().ListForUser(ctx, ana, 10)
	if err != nil {
		t.Fatalf("ListForUser: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("ListForUser returned %d rows, want 2", len(rows))
	}
	priority, form := rows[0], rows[1]
	if priority.ActorUserID == nil || *priority.ActorUserID != actor {
		t.Errorf("priority actor = %v, want %d", priority.ActorUserID, actor)
	}
	if priority.FromValue == nil || *priority.FromValue != "low" || priority.ToValue == nil || *priority.ToValue != "critical" {
		t.Errorf("priority change = (%v, %v), want (low, critical)", priority.FromValue, priority.ToValue)
	}
	if priority.StepIndex != nil {
		t.Errorf("priority step index = %v, want nil", *priority.StepIndex)
	}
	if form.ActorUserID != nil {
		t.Errorf("automatic form_waiting actor = %v, want nil (the system did it)", *form.ActorUserID)
	}
	if form.StepIndex == nil || *form.StepIndex != step {
		t.Errorf("form_waiting step index = %v, want %d", form.StepIndex, step)
	}
}

// notifStr is the local string-pointer helper for this file.
func notifStr(s string) *string { return &s }
