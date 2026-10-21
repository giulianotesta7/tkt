package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
)

// emailOutboxStore implements application.EmailOutbox (issue #362): the
// delivery state of every notification email, written with the notification in
// the change's transaction (appendNotificationsTx) and driven from a background
// sender OUTSIDE that transaction (S6, S13). It never composes or sends an
// email; it only makes the state transitions safe to run concurrently, so
// "never send twice for the same notification" is a storage fact and not a
// timing assumption (S6, S14).
type emailOutboxStore struct {
	db *sql.DB
}

var _ application.EmailOutbox = (*emailOutboxStore)(nil)

func newEmailOutboxStore(db *sql.DB) *emailOutboxStore { return &emailOutboxStore{db: db} }

// EmailOutbox returns the notification email delivery port (issue #362).
func (s *Store) EmailOutbox() application.EmailOutbox { return newEmailOutboxStore(s.db) }

// emailInterruptedError is the last_error stamped on a row a stopped process
// left in 'sending'. The row FAILS instead of returning to pending: the message
// may already have been handed to the SMTP server, and at-most-once forbids
// risking a second copy (S14).
const emailInterruptedError = "interrupted by restart"

// errEmailNotClaimed is returned by a settlement that matched no row in the
// 'sending' state: the caller does not hold this row, so it must not overwrite
// whatever the actual owner is doing with it.
var errEmailNotClaimed = errors.New("notification email is not claimed by this sender")

// AbandonInFlight fails every row a stopped process left in 'sending' (S14).
// One statement, so a restart cannot observe a half-abandoned table: either
// every interrupted row is failed or none is.
func (s *emailOutboxStore) AbandonInFlight(ctx context.Context, at time.Time) (int, error) {
	res, err := s.db.ExecContext(ctx,
		`UPDATE notification_emails SET status = 'failed', last_error = ?, updated_at = ?
		 WHERE status = 'sending'`,
		emailInterruptedError, formatTime(at))
	if err != nil {
		return 0, fmt.Errorf("sqlite: abandon in-flight notification emails: %w", err)
	}
	return affectedRows("abandon in-flight notification emails", res)
}

// SkipPending skips every pending row with reason (S14 "notifications created
// while SMTP is off are skipped"). Rows already claimed are deliberately out of
// reach: a skip must never race a sender that owns the row.
func (s *emailOutboxStore) SkipPending(ctx context.Context, at time.Time, reason string) (int, error) {
	res, err := s.db.ExecContext(ctx,
		`UPDATE notification_emails SET status = 'skipped', skip_reason = ?, updated_at = ?
		 WHERE status = 'pending'`,
		reason, formatTime(at))
	if err != nil {
		return 0, fmt.Errorf("sqlite: skip pending notification emails: %w", err)
	}
	return affectedRows("skip pending notification emails", res)
}

// ClaimDue atomically moves up to limit due pending rows to 'sending' and
// returns them with their notification facts, oldest next_attempt_at first
// (S6, S13).
//
// Everything happens in ONE immediate transaction, so the transaction itself is
// the mutual exclusion: two senders cannot both read a row as pending, and the
// `AND status = 'pending'` guard on each update keeps that true even if a
// future caller reaches the claim outside this transaction. A claimed row is
// owned by exactly one caller until it is settled.
//
// The same transaction first deletes the delivery rows whose notification no
// longer exists. That is where the table's integrity lives, because
// notification_id deliberately carries no foreign key: `notifications` is
// rebuilt to add a kind (#352), and no rebuild may touch delivery state. An
// orphan row could therefore never be sent — it joins nothing — so it is
// removed instead of accumulating.
func (s *emailOutboxStore) ClaimDue(ctx context.Context, now time.Time, limit int) ([]application.PendingEmail, error) {
	if limit <= 0 {
		return nil, nil
	}
	tx, err := beginImmediate(ctx, s.db, "claim notification emails")
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM notification_emails
		 WHERE NOT EXISTS (SELECT 1 FROM notifications n WHERE n.id = notification_emails.notification_id)`); err != nil {
		return nil, fmt.Errorf("sqlite: drop orphan notification emails: %w", err)
	}

	due, err := dueNotificationEmailsTx(ctx, tx, now, limit)
	if err != nil {
		return nil, err
	}
	claimed, err := claimNotificationEmailsTx(ctx, tx, due, now)
	if err != nil {
		return nil, err
	}
	out, err := pendingEmailsTx(ctx, tx, claimed)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("sqlite: commit claim notification emails: %w", err)
	}
	return out, nil
}

// MarkSent records SMTP acceptance. The attempt counter advances here because
// the attempt that succeeded is the one this claim represents.
func (s *emailOutboxStore) MarkSent(ctx context.Context, notificationID int64, at time.Time) error {
	return s.settle(ctx, "mark notification email sent", notificationID,
		`UPDATE notification_emails
		 SET status = 'sent', sent_at = ?, attempts = attempts + 1, updated_at = ?
		 WHERE notification_id = ? AND status = 'sending'`,
		formatTime(at), formatTime(at), notificationID)
}

// MarkSkipped records a deliberate non-delivery with its reason (S14), so an
// operator can tell a skip from a failure.
func (s *emailOutboxStore) MarkSkipped(ctx context.Context, notificationID int64, at time.Time, reason string) error {
	return s.settle(ctx, "mark notification email skipped", notificationID,
		`UPDATE notification_emails
		 SET status = 'skipped', skip_reason = ?, updated_at = ?
		 WHERE notification_id = ? AND status = 'sending'`,
		reason, formatTime(at), notificationID)
}

// MarkFailed records a terminal failure with the attempt count reached and the
// last error (S13, S14). A failed row is never claimed again.
func (s *emailOutboxStore) MarkFailed(ctx context.Context, notificationID int64, at time.Time, attempts int, lastError string) error {
	return s.settle(ctx, "mark notification email failed", notificationID,
		`UPDATE notification_emails
		 SET status = 'failed', attempts = ?, last_error = ?, updated_at = ?
		 WHERE notification_id = ? AND status = 'sending'`,
		attempts, lastError, formatTime(at), notificationID)
}

// Reschedule returns a claimed row to pending for a retry at next (S14). The
// claim is released (claimed_at back to NULL) so the row honestly reads as
// waiting rather than as owned. The port carries no settlement instant, so
// updated_at keeps the row's last known change — the claim that produced this
// attempt — instead of inventing a future one from next.
func (s *emailOutboxStore) Reschedule(ctx context.Context, notificationID int64, attempts int, next time.Time, lastError string) error {
	return s.settle(ctx, "reschedule notification email", notificationID,
		`UPDATE notification_emails
		 SET status = 'pending', attempts = ?, next_attempt_at = ?, last_error = ?,
		     claimed_at = NULL, updated_at = COALESCE(claimed_at, ?)
		 WHERE notification_id = ? AND status = 'sending'`,
		attempts, formatTime(next), lastError, formatTime(next), notificationID)
}

// settle runs one settlement statement and refuses a row this sender does not
// hold. The `status = 'sending'` predicate plus the zero-rows check is the
// whole ownership guard: a stale sender cannot mark another's claim sent, and a
// row that was abandoned, skipped, or failed is not silently rewritten.
func (s *emailOutboxStore) settle(ctx context.Context, op string, notificationID int64, query string, args ...any) error {
	res, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("sqlite: %s %d: %w", op, notificationID, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("sqlite: %s %d: %w", op, notificationID, err)
	}
	if affected == 0 {
		return fmt.Errorf("sqlite: %s %d: %w", op, notificationID, errEmailNotClaimed)
	}
	return nil
}

// affectedRows reports the changed-row count of a bulk transition.
func affectedRows(op string, res sql.Result) (int, error) {
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("sqlite: %s: %w", op, err)
	}
	return int(n), nil
}

// dueCandidate is one pending row the claim is about to take.
type dueCandidate struct {
	notificationID int64
	attempts       int
}

// dueNotificationEmailsTx reads the due pending rows in claim order: the
// earliest next_attempt_at first, and the notification id as the tiebreak, so
// the order is deterministic even when a batch shares one instant.
func dueNotificationEmailsTx(ctx context.Context, tx *sql.Tx, now time.Time, limit int) ([]dueCandidate, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT notification_id, attempts FROM notification_emails
		 WHERE status = 'pending' AND next_attempt_at <= ?
		 ORDER BY next_attempt_at, notification_id LIMIT ?`,
		formatTime(now), limit)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list due notification emails: %w", err)
	}
	defer rows.Close()

	var out []dueCandidate
	for rows.Next() {
		var c dueCandidate
		if err := rows.Scan(&c.notificationID, &c.attempts); err != nil {
			return nil, fmt.Errorf("sqlite: scan due notification email: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: list due notification emails: %w", err)
	}
	return out, nil
}

// claimNotificationEmailsTx moves each candidate to 'sending' with the claim
// instant and returns only the rows this transaction actually took. The
// `AND status = 'pending'` guard makes the claim correct on its own terms: a
// row another writer took between the read and the update is skipped, never
// claimed twice.
func claimNotificationEmailsTx(ctx context.Context, tx *sql.Tx, due []dueCandidate, now time.Time) ([]dueCandidate, error) {
	claimed := make([]dueCandidate, 0, len(due))
	for _, c := range due {
		res, err := tx.ExecContext(ctx,
			`UPDATE notification_emails SET status = 'sending', claimed_at = ?, updated_at = ?
			 WHERE notification_id = ? AND status = 'pending'`,
			formatTime(now), formatTime(now), c.notificationID)
		if err != nil {
			return nil, fmt.Errorf("sqlite: claim notification email %d: %w", c.notificationID, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return nil, fmt.Errorf("sqlite: claim notification email %d: %w", c.notificationID, err)
		}
		if n == 0 {
			continue
		}
		claimed = append(claimed, c)
	}
	return claimed, nil
}

// pendingEmailsTx loads the notification facts of the rows just claimed. A row
// whose notification disappeared can only be an orphan the cleanup above
// missed, so it is returned as no work rather than as a half-populated claim.
func pendingEmailsTx(ctx context.Context, tx *sql.Tx, claimed []dueCandidate) ([]application.PendingEmail, error) {
	out := make([]application.PendingEmail, 0, len(claimed))
	for _, c := range claimed {
		row := tx.QueryRowContext(ctx, `SELECT `+notificationColumns+` FROM notifications WHERE id = ?`, c.notificationID)
		n, err := scanNotificationFrom(row)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("sqlite: read claimed notification %d: %w", c.notificationID, err)
		}
		out = append(out, application.PendingEmail{Notification: *n, Attempts: c.attempts})
	}
	return out, nil
}
