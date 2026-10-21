package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// Issue #362 spec S6/S12, storage half: the pending email row is written in
// the SAME transaction as the notification it belongs to, which is itself in
// the change's transaction. Email delivery never runs inside that transaction
// (the sender claims rows afterwards), but the delivery row is as inseparable
// from the change as the notification is: a change that fails leaves neither
// behind.

// injectEmailFailure aborts every notification_emails insert (test-only
// trigger), the same fault-injection shape injectAuditFailure uses.
func injectEmailFailure(t *testing.T, s *Store) {
	t.Helper()
	if _, err := s.db.ExecContext(context.Background(),
		`CREATE TRIGGER trg_test_fail_email BEFORE INSERT ON notification_emails
		 BEGIN SELECT RAISE(ABORT, 'injected email enqueue failure'); END`); err != nil {
		t.Fatalf("inject email failure trigger: %v", err)
	}
	t.Cleanup(func() {
		if _, err := s.db.ExecContext(context.Background(), `DROP TRIGGER IF EXISTS trg_test_fail_email`); err != nil {
			t.Errorf("drop email failure trigger: %v", err)
		}
	})
}

// TestUnitOfWorkWritesThePendingEmailWithItsNotification is the success half:
// one ticket mutation persists the ticket write, the audit event, the
// notification, and its pending delivery row together — the row seeded from
// the notification's own instant, so it is due immediately.
func TestUnitOfWorkWritesThePendingEmailWithItsNotification(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	requester, _, tk := mutationFixture(t, s)
	at := testClock.Add(time.Minute)
	tk.State = domain.StateInProgress
	tk.UpdatedAt = at

	if err := s.TicketUnitOfWork().Update(ctx, application.TicketMutation{
		Ticket: &tk,
		Events: []domain.AuditEvent{{TicketID: tk.ID, Actor: "Ada", Action: domain.ActionTransition,
			Field: strVal("state"), FromValue: strVal("new"), ToValue: strVal("in_progress"), CreatedAt: at}},
		Notifications: []domain.Notification{{UserID: requester, TicketID: tk.ID,
			Kind: domain.NotificationStateChanged, FromValue: strVal("new"), ToValue: strVal("in_progress"), CreatedAt: at}},
	}); err != nil {
		t.Fatalf("unit update: %v", err)
	}

	rows, err := s.NotificationStore().ListForUser(ctx, requester, 10)
	if err != nil {
		t.Fatalf("list notifications: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("%d notifications, want 1", len(rows))
	}
	delivery := readEmailRow(t, s, rows[0].ID)
	if delivery.status != string(application.EmailPending) {
		t.Errorf("delivery status = %q, want pending", delivery.status)
	}
	if delivery.attempts != 0 {
		t.Errorf("delivery attempts = %d, want 0", delivery.attempts)
	}
	if delivery.nextAt != formatTime(at) {
		t.Errorf("next_attempt_at = %q, want the notification's created_at %s", delivery.nextAt, formatTime(at))
	}
	if delivery.createdAt != formatTime(at) || delivery.updatedAt != formatTime(at) {
		t.Errorf("delivery timestamps = (%q, %q), want both %s", delivery.createdAt, delivery.updatedAt, formatTime(at))
	}
	if delivery.claimedAt.Valid || delivery.sentAt.Valid || delivery.skipReason.Valid || delivery.lastErr.Valid {
		t.Errorf("delivery row = %+v, want every settlement fact NULL before the first attempt", delivery)
	}

	// The row is immediately claimable by the background sender, which is how
	// the change's notification reaches email without delivery running inside
	// the change's transaction (S6).
	claimed, err := s.EmailOutbox().ClaimDue(ctx, at, 10)
	if err != nil {
		t.Fatalf("ClaimDue: %v", err)
	}
	wantIDs(t, claimed, rows[0].ID)
}

// TestUnitOfWorkRollsBackTheChangeWhenTheEmailEnqueueFails is the failure
// half: when the delivery insert fails, the notification, the audit event, and
// the ticket write all roll back with it. Once the failure is removed the very
// same unit writes both rows, proving the trigger was the only obstacle.
func TestUnitOfWorkRollsBackTheChangeWhenTheEmailEnqueueFails(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	requester, _, tk := mutationFixture(t, s)
	at := testClock.Add(time.Minute)
	tk.State = domain.StateInProgress
	tk.UpdatedAt = at
	mutation := application.TicketMutation{
		Ticket: &tk,
		Events: []domain.AuditEvent{{TicketID: tk.ID, Actor: "Ada", Action: domain.ActionTransition,
			Field: strVal("state"), FromValue: strVal("new"), ToValue: strVal("in_progress"), CreatedAt: at}},
		Notifications: []domain.Notification{{UserID: requester, TicketID: tk.ID,
			Kind: domain.NotificationStateChanged, CreatedAt: at}},
	}

	injectEmailFailure(t, s)
	if err := s.TicketUnitOfWork().Update(ctx, mutation); err == nil {
		t.Fatal("unit update succeeded, want the failed delivery insert to fail the whole unit")
	}

	stored, err := s.TicketStore().GetByID(ctx, tk.ID, application.TicketQuery{Scope: application.ScopeAll})
	if err != nil {
		t.Fatalf("read ticket: %v", err)
	}
	if stored.State != domain.StateNew {
		t.Errorf("ticket state = %s, want new (rolled back)", stored.State)
	}
	if events, err := s.AuditStore().ListByTicket(ctx, tk.ID); err != nil {
		t.Fatalf("list audits: %v", err)
	} else if len(events) != 0 {
		t.Errorf("%d audit events survived, want 0", len(events))
	}
	if rows, err := s.NotificationStore().ListForUser(ctx, requester, 10); err != nil {
		t.Fatalf("list notifications: %v", err)
	} else if len(rows) != 0 {
		t.Errorf("notifications = %+v, want none from a rolled-back change", rows)
	}
	var deliveryRows int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM notification_emails`).Scan(&deliveryRows); err != nil {
		t.Fatalf("count delivery rows: %v", err)
	}
	if deliveryRows != 0 {
		t.Errorf("%d delivery rows survived, want 0", deliveryRows)
	}

	if _, err := s.db.ExecContext(ctx, `DROP TRIGGER trg_test_fail_email`); err != nil {
		t.Fatalf("drop email failure trigger: %v", err)
	}
	if err := s.TicketUnitOfWork().Update(ctx, mutation); err != nil {
		t.Fatalf("unit update after removing the failure: %v", err)
	}
	rows, err := s.NotificationStore().ListForUser(ctx, requester, 10)
	if err != nil {
		t.Fatalf("list notifications after retry: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("%d notifications after retry, want 1", len(rows))
	}
	if got := readEmailRow(t, s, rows[0].ID); got.status != string(application.EmailPending) {
		t.Errorf("delivery status after retry = %q, want pending", got.status)
	}
}
