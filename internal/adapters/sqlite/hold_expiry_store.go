package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// holdExpiryStore implements application.HoldExpiryStore (issue #353): the two
// reads the automatic hold expiry pass performs. It never writes — closing a
// hold goes through TicketUnitOfWork.UpdateHold and the reminder through
// TicketUnitOfWork.RemindHold, so every write carries its own guard.
type holdExpiryStore struct {
	db *sql.DB
}

var _ application.HoldExpiryStore = (*holdExpiryStore)(nil)

func newHoldExpiryStore(db *sql.DB) *holdExpiryStore { return &holdExpiryStore{db: db} }

// HeldTickets returns the open tickets that are on hold, ordered by id so a
// pass is deterministic, and bounded per pass. "Open" is the ticket state
// vocabulary (domain.ClosedStates), never a second list: a ticket that already
// reached a terminal state has nothing left to close and is skipped even if a
// hand-edited row still carries a hold instant.
func (h *holdExpiryStore) HeldTickets(ctx context.Context, afterID int64, limit int) ([]domain.Ticket, error) {
	closed := domain.ClosedStates()
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(closed)), ",")
	args := make([]any, 0, len(closed)+1)
	for _, state := range closed {
		args = append(args, string(state))
	}
	args = append(args, afterID, limit)
	rows, err := h.db.QueryContext(ctx, `SELECT `+ticketColumns+` FROM tickets t
		WHERE t.on_hold_since IS NOT NULL AND t.state NOT IN (`+placeholders+`) AND t.id > ?
		ORDER BY t.id LIMIT ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list held tickets: %w", err)
	}
	defer rows.Close()
	var out []domain.Ticket
	for rows.Next() {
		t, err := scanTicketFrom(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: scan held ticket: %w", err)
		}
		out = append(out, *t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: list held tickets: %w", err)
	}
	return out, nil
}

// ReminderSent reports whether a hold_reminder notification for the ticket
// exists at or after the instant the hold began: that is the whole
// once-per-hold rule, read from the notification the pass wrote (never from a
// separate counter that could drift from it). created_at is RFC3339 UTC TEXT
// (D7), so the comparison is a plain, index-friendly string comparison.
func (h *holdExpiryStore) ReminderSent(ctx context.Context, ticketID int64, since time.Time) (bool, error) {
	var sent bool
	err := h.db.QueryRowContext(ctx, `SELECT EXISTS(
		SELECT 1 FROM notifications
		WHERE ticket_id = ? AND kind = ? AND created_at >= ?)`,
		ticketID, string(domain.NotificationHoldReminder), formatTime(since)).Scan(&sent)
	if err != nil {
		return false, fmt.Errorf("sqlite: check hold reminder: %w", err)
	}
	return sent, nil
}
