package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// Issue #419 (S3): the account-pending notification is the one kind of the
// notification center that is not about a ticket. These tests pin the storage
// contract the later wiring depends on: the row round-trips with a NULL ticket
// and its account subject, the bell reads it like any other row, nothing is
// mailed for it, and every ticket kind keeps storing exactly what it stored
// before.

// accountNotification is the arrangement of a pending-account announcement
// addressed to recipient.
func accountNotification(pendingUserID, recipient int64) domain.Notification {
	return domain.Notification{
		UserID:        recipient,
		Kind:          domain.NotificationCompanyAccountPending,
		PendingUserID: &pendingUserID,
		CreatedAt:     testClock,
	}
}

// accountNotificationStoreFixture seeds an administrator, a pending account
// waiting for approval, and one ticket, so a test can compare the ticketless
// announcement with the ticket kinds in the same store.
func accountNotificationStoreFixture(t *testing.T, s *Store) (admin, waiting, ticketID int64) {
	t.Helper()
	admin = seedUser(t, s, "Ada", "ada@example.com", true)
	waiting = seedPendingUser(t, s, "Wanda", "wanda@example.com")
	cat := seedCategory(t, s, "Account notices")
	tk := seedTicket(t, s, domain.Ticket{Number: 41, Title: "ticket notice", CategoryID: cat,
		Priority: domain.PriorityMedium, State: domain.StateNew, CreatedAt: testClock, UpdatedAt: testClock})
	return admin, waiting, tk.ID
}

// TestAccountNotificationRoundTripsWithoutATicket proves the account
// announcement is stored and read back as the bell expects: no ticket, its
// pending account as the subject, and the same read/mark behavior every
// notification has.
func TestAccountNotificationRoundTripsWithoutATicket(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	admin, waiting, _ := accountNotificationStoreFixture(t, s)

	insertNotifications(t, s, accountNotification(waiting, admin))

	rows, err := s.NotificationStore().ListForUser(ctx, admin, 10)
	if err != nil {
		t.Fatalf("ListForUser: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("ListForUser returned %d rows, want the one account announcement", len(rows))
	}
	got := rows[0]
	if got.Kind != domain.NotificationCompanyAccountPending {
		t.Errorf("kind = %q, want %q", got.Kind, domain.NotificationCompanyAccountPending)
	}
	if got.TicketID != 0 {
		t.Errorf("ticket_id = %d, want 0 — the account kind names no ticket", got.TicketID)
	}
	if got.PendingUserID == nil || *got.PendingUserID != waiting {
		t.Errorf("pending_user_id = %v, want %d", got.PendingUserID, waiting)
	}
	if got.ActorUserID != nil || got.FromValue != nil || got.ToValue != nil ||
		got.CommentID != nil || got.StepIndex != nil {
		t.Errorf("account announcement carries ticket facts: %+v", got)
	}
	if got.ReadAt != nil {
		t.Errorf("fresh account announcement read_at = %v, want nil", *got.ReadAt)
	}

	// The absent ticket is stored as NULL, not as the id 0: a stored 0 would
	// mean a ticket that does not exist, and the ticket foreign key could
	// never accept it.
	var ticketID sql.NullInt64
	var pendingID sql.NullInt64
	if err := s.db.QueryRowContext(ctx,
		`SELECT ticket_id, pending_user_id FROM notifications WHERE id = ?`, got.ID).Scan(&ticketID, &pendingID); err != nil {
		t.Fatalf("read the stored row: %v", err)
	}
	if ticketID.Valid {
		t.Errorf("stored ticket_id = %d, want NULL", ticketID.Int64)
	}
	if !pendingID.Valid || pendingID.Int64 != waiting {
		t.Errorf("stored pending_user_id = %v, want %d", pendingID, waiting)
	}

	if count, err := s.NotificationStore().UnreadCount(ctx, admin); err != nil {
		t.Fatalf("UnreadCount: %v", err)
	} else if count != 1 {
		t.Errorf("UnreadCount = %d, want 1", count)
	}
	at := testClock.Add(5 * time.Minute)
	if err := s.NotificationStore().MarkRead(ctx, admin, got.ID, at); err != nil {
		t.Fatalf("MarkRead: %v", err)
	}
	if readAt := readAtOf(t, s, admin, got.ID); readAt == nil || !readAt.Equal(at) {
		t.Errorf("stored read_at = %v, want %v", readAt, at)
	}
}

// TestAccountNotificationOwesNoTicketEmail proves the one deliberate
// difference between the new kind and the existing ones: a ticket
// notification owes an email delivery row in the same transaction, an account
// announcement owes none, because no ticket email copy exists for it. The
// ticket kinds keep enqueueing exactly as before.
func TestAccountNotificationOwesNoTicketEmail(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	admin, waiting, ticketID := accountNotificationStoreFixture(t, s)

	insertNotifications(t, s,
		accountNotification(waiting, admin),
		domain.Notification{UserID: admin, TicketID: ticketID, Kind: domain.NotificationAssigned,
			CreatedAt: testClock.Add(time.Minute)},
	)

	rows, err := s.NotificationStore().ListForUser(ctx, admin, 10)
	if err != nil {
		t.Fatalf("ListForUser: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("ListForUser returned %d rows, want 2", len(rows))
	}
	for _, n := range rows {
		var count int
		if err := s.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM notification_emails WHERE notification_id = ?`, n.ID).Scan(&count); err != nil {
			t.Fatalf("count notification_emails for %d: %v", n.ID, err)
		}
		switch n.Kind {
		case domain.NotificationCompanyAccountPending:
			if count != 0 {
				t.Errorf("account announcement %d has %d email delivery rows, want 0", n.ID, count)
			}
		case domain.NotificationAssigned:
			if count != 1 {
				t.Errorf("ticket notification %d has %d email delivery rows, want 1", n.ID, count)
			}
			var status, nextAttempt string
			if err := s.db.QueryRowContext(ctx,
				`SELECT status, next_attempt_at FROM notification_emails WHERE notification_id = ?`, n.ID).
				Scan(&status, &nextAttempt); err != nil {
				t.Fatalf("read the ticket delivery row: %v", err)
			}
			if status != "pending" {
				t.Errorf("ticket delivery status = %q, want pending", status)
			}
			if want := formatTime(n.CreatedAt); nextAttempt != want {
				t.Errorf("ticket delivery next_attempt_at = %q, want the notification instant %q", nextAttempt, want)
			}
		}
	}
}

// TestAccountNotificationCascadesWithThePendingAccount proves the announcement
// cannot outlive the account it announces: a rejected and deleted account
// takes its notice with it, so the bell never points at a missing account.
func TestAccountNotificationCascadesWithThePendingAccount(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	admin, waiting, _ := accountNotificationStoreFixture(t, s)
	insertNotifications(t, s, accountNotification(waiting, admin))

	if _, err := s.db.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, waiting); err != nil {
		t.Fatalf("delete the pending account: %v", err)
	}
	var remaining int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM notifications WHERE user_id = ?`, admin).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Errorf("%d account announcements survived the account delete, want 0 (ON DELETE CASCADE)", remaining)
	}
	if count, err := s.NotificationStore().UnreadCount(ctx, admin); err != nil {
		t.Fatalf("UnreadCount: %v", err)
	} else if count != 0 {
		t.Errorf("UnreadCount after the account delete = %d, want 0", count)
	}
}

// TestAccountNotificationRefusalsLeaveTheStoreUntouched proves every rejected
// write — a domain invariant or a column constraint — changes neither the
// database and WAL bytes nor the notification and delivery counters.
func TestAccountNotificationRefusalsLeaveTheStoreUntouched(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notifications.db")
	s, err := OpenForTests(path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.db.Close()
	s.db.SetMaxOpenConns(1)
	ctx := context.Background()
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	admin, waiting, ticketID := accountNotificationStoreFixture(t, s)
	insertNotifications(t, s,
		accountNotification(waiting, admin),
		domain.Notification{UserID: admin, TicketID: ticketID, Kind: domain.NotificationAssigned,
			CreatedAt: testClock.Add(time.Minute)},
	)

	bytes := func() [32]byte {
		t.Helper()
		var all []byte
		for _, suffix := range []string{"", "-wal"} {
			b, err := os.ReadFile(path + suffix)
			if err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			all = append(all, b...)
		}
		return sha256.Sum256(all)
	}
	counts := func() (notifications, deliveries int) {
		t.Helper()
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM notifications`).Scan(&notifications); err != nil {
			t.Fatal(err)
		}
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM notification_emails`).Scan(&deliveries); err != nil {
			t.Fatal(err)
		}
		return notifications, deliveries
	}
	rejectAppend := func(n domain.Notification) {
		t.Helper()
		beforeBytes := bytes()
		beforeNotifications, beforeDeliveries := counts()
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatalf("begin tx: %v", err)
		}
		defer tx.Rollback()
		appendErr := appendNotificationsTx(ctx, tx, []domain.Notification{n})
		var ie *domain.NotificationInvariantError
		if !errors.As(appendErr, &ie) {
			t.Fatalf("appendNotificationsTx = %v, want a *domain.NotificationInvariantError", appendErr)
		}
		if err := tx.Rollback(); err != nil {
			t.Fatalf("rollback: %v", err)
		}
		if after := bytes(); after != beforeBytes {
			t.Error("rejected append changed DB/WAL bytes")
		}
		if n, d := counts(); n != beforeNotifications || d != beforeDeliveries {
			t.Errorf("rejected append changed counters: notifications %d->%d, deliveries %d->%d",
				beforeNotifications, n, beforeDeliveries, d)
		}
	}
	rejectExec := func(q string) {
		t.Helper()
		beforeBytes := bytes()
		beforeNotifications, beforeDeliveries := counts()
		if _, err := s.db.ExecContext(ctx, q); err == nil || !isConstraint(err) {
			t.Fatalf("expected constraint rejection: %s: %v", q, err)
		}
		if after := bytes(); after != beforeBytes {
			t.Error("rejected statement changed DB/WAL bytes")
		}
		if n, d := counts(); n != beforeNotifications || d != beforeDeliveries {
			t.Errorf("rejected statement changed counters: notifications %d->%d, deliveries %d->%d",
				beforeNotifications, n, beforeDeliveries, d)
		}
	}

	// Domain refusals: the account kind is closed, so a ticket, a missing or
	// non-positive subject, and ticket facts are all refused before insert.
	withTicket := accountNotification(waiting, admin)
	withTicket.TicketID = ticketID
	rejectAppend(withTicket)
	withoutSubject := accountNotification(waiting, admin)
	withoutSubject.PendingUserID = nil
	rejectAppend(withoutSubject)
	zeroSubject := accountNotification(waiting, admin)
	zero := int64(0)
	zeroSubject.PendingUserID = &zero
	rejectAppend(zeroSubject)
	withStep := accountNotification(waiting, admin)
	step := 1
	withStep.StepIndex = &step
	rejectAppend(withStep)
	withComment := accountNotification(waiting, admin)
	commentID := int64(1)
	withComment.CommentID = &commentID
	rejectAppend(withComment)
	accountAsTicket := domain.Notification{UserID: admin, TicketID: ticketID,
		Kind: domain.NotificationAssigned, PendingUserID: &waiting, CreatedAt: testClock}
	rejectAppend(accountAsTicket)

	// Column refusals: the schema refuses the same shapes even when the Go
	// guard is bypassed, so no other writer can store one.
	rejectInsert := func(ticketID, pendingID any, kind string, commentID any) {
		t.Helper()
		rejectExec(fmt.Sprintf(`INSERT INTO notifications (user_id, ticket_id, pending_user_id, kind, comment_id, created_at)
			VALUES (%d, %s, %s, '%s', %s, '%s')`,
			admin, sqlValue(ticketID), sqlValue(pendingID), kind, sqlValue(commentID), formatTime(testClock)))
	}
	rejectInsert(ticketID, nil, string(domain.NotificationCompanyAccountPending), nil)
	rejectInsert(nil, nil, string(domain.NotificationCompanyAccountPending), nil)
	rejectInsert(nil, -1, string(domain.NotificationCompanyAccountPending), nil)
	rejectInsert(nil, waiting, string(domain.NotificationAssigned), nil)
	rejectInsert(nil, nil, string(domain.NotificationAssigned), nil)
	rejectInsert(nil, waiting, string(domain.NotificationCompanyAccountPending), 1)
	rejectInsert(nil, 999, string(domain.NotificationCompanyAccountPending), nil)
	rejectInsert(ticketID, waiting, string(domain.NotificationCompanyAccountPending), nil)
}

// sqlValue renders one bind value for a raw INSERT statement, so a table-level
// constraint can be probed directly.
func sqlValue(v any) string {
	switch t := v.(type) {
	case nil:
		return "NULL"
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	default:
		return fmt.Sprint(t)
	}
}
