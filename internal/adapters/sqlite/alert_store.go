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

// Operational alerts (issue #408): the reads the alert pass performs and the
// one idempotent write it makes. The SLA facts are NOT read with SQL of their
// own — the batch commitment and milestone readers of the SLA store answer
// them, so an alert and the SLA the ticket displays can never drift apart.

// alertStore implements application.AlertStore.
type alertStore struct {
	db  *sql.DB
	sla *slaStore
}

var _ application.AlertStore = (*alertStore)(nil)

func newAlertStore(db *sql.DB) *alertStore { return &alertStore{db: db, sla: newSLAStore(db)} }

// AlertCandidates returns one page of the OPEN tickets that have an assignee,
// ordered by id, each with its frozen commitment, its observed milestones and
// the instant it last moved.
//
// "Open" is the ticket state vocabulary (domain.ClosedStates), never a second
// list: a resolved, closed or cancelled ticket has nothing left to chase.
// A ticket with no assignee is excluded at the source, because the assigned
// agent is the only recipient an alert has.
func (a *alertStore) AlertCandidates(ctx context.Context, afterID int64, limit int) ([]application.AlertCandidate, error) {
	closed := domain.ClosedStates()
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(closed)), ",")
	args := make([]any, 0, len(closed)+2)
	for _, state := range closed {
		args = append(args, string(state))
	}
	args = append(args, afterID, limit)
	rows, err := a.db.QueryContext(ctx, `SELECT `+ticketColumns+` FROM tickets t
		WHERE t.user_id IS NOT NULL AND t.state NOT IN (`+placeholders+`) AND t.id > ?
		ORDER BY t.id LIMIT ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list alert candidates: %w", err)
	}
	defer rows.Close()
	var tickets []domain.Ticket
	for rows.Next() {
		t, err := scanTicketFrom(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: scan alert candidate: %w", err)
		}
		tickets = append(tickets, *t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: list alert candidates: %w", err)
	}
	if len(tickets) == 0 {
		return nil, nil
	}
	ids := make([]int64, 0, len(tickets))
	for i := range tickets {
		ids = append(ids, tickets[i].ID)
	}
	frozen, err := a.sla.TicketSLAs(ctx, ids)
	if err != nil {
		return nil, err
	}
	milestones, err := a.sla.MilestonesFor(ctx, ids)
	if err != nil {
		return nil, err
	}
	comments, err := a.lastCommentAt(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make([]application.AlertCandidate, 0, len(tickets))
	for i := range tickets {
		t := tickets[i]
		out = append(out, application.AlertCandidate{
			Ticket:         t,
			SLA:            frozen[t.ID],
			Milestones:     milestones[t.ID],
			LastActivityAt: latestInstant(t.UpdatedAt, comments[t.ID]),
		})
	}
	return out, nil
}

// lastCommentAt reads the newest comment instant of each ticket. Comment
// visibility is deliberately NOT filtered: an internal note is movement on the
// ticket just as much as a public reply, and this answers "did anything
// happen", not "was the requester answered".
func (a *alertStore) lastCommentAt(ctx context.Context, ids []int64) (map[int64]time.Time, error) {
	out := map[int64]time.Time{}
	placeholders, args := slaChunkArgs(ids)
	rows, err := a.db.QueryContext(ctx, `SELECT ticket_id, MAX(created_at) FROM comments
		WHERE ticket_id IN (`+placeholders+`) GROUP BY ticket_id`, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: read last comment instants: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var ticketID int64
		var at sql.NullString
		if err := rows.Scan(&ticketID, &at); err != nil {
			return nil, fmt.Errorf("sqlite: scan last comment instant: %w", err)
		}
		parsed, err := parseSLAMilestone(at, "last comment")
		if err != nil {
			return nil, err
		}
		if parsed != nil {
			out[ticketID] = *parsed
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: read last comment instants: %w", err)
	}
	return out, nil
}

// latestInstant returns the later of two instants, ignoring zero values.
func latestInstant(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

// AlertSent reports whether this ticket was already alerted about this
// condition. A nil since asks "ever" (an SLA threshold is crossed once and
// never un-crossed); a non-nil since scopes the question to the current period
// of silence, which is what lets a new inactivity alert follow fresh activity.
//
// created_at is RFC3339 UTC TEXT (D7), so the comparison is a plain,
// index-friendly string comparison.
func (a *alertStore) AlertSent(ctx context.Context, ticketID int64, kind domain.NotificationKind, since *time.Time) (bool, error) {
	sent, err := alertSentTx(ctx, a.db, ticketID, kind, since)
	if err != nil {
		return false, fmt.Errorf("sqlite: check %s alert: %w", kind, err)
	}
	return sent, nil
}

// alertSentQuerier is the subset of *sql.DB and *sql.Tx the predicate needs, so
// the read and the write evaluate the SAME statement — the read outside any
// transaction to skip cheaply, the write inside its own to be correct.
type alertSentQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func alertSentTx(ctx context.Context, q alertSentQuerier, ticketID int64, kind domain.NotificationKind, since *time.Time) (bool, error) {
	statement := `SELECT EXISTS(SELECT 1 FROM notifications WHERE ticket_id = ? AND kind = ?`
	args := []any{ticketID, string(kind)}
	if since != nil {
		statement += ` AND created_at >= ?`
		args = append(args, formatTime(*since))
	}
	statement += `)`
	var sent bool
	if err := q.QueryRowContext(ctx, statement, args...).Scan(&sent); err != nil {
		return false, err
	}
	return sent, nil
}

// alertUnitOfWork implements application.AlertUnitOfWork.
type alertUnitOfWork struct {
	db *sql.DB
}

var _ application.AlertUnitOfWork = (*alertUnitOfWork)(nil)

func newAlertUnitOfWork(db *sql.DB) *alertUnitOfWork { return &alertUnitOfWork{db: db} }

// AppendAlert appends the rows of one alert, and only when the same condition
// has not been recorded yet. The check runs INSIDE the immediate transaction
// that writes, so two overlapping passes cannot both decide the alert is
// missing: whichever commits second finds the row and writes nothing.
//
// An already-recorded alert is a silent no-op: it is the normal outcome of a
// condition that persists across passes, not a failure.
func (u *alertUnitOfWork) AppendAlert(ctx context.Context, ticketID int64, kind domain.NotificationKind, since *time.Time, notifications []domain.Notification) error {
	if len(notifications) == 0 {
		return nil
	}
	tx, err := beginImmediate(ctx, u.db, "alert unit")
	if err != nil {
		return err
	}
	defer tx.Rollback()
	sent, err := alertSentTx(ctx, tx, ticketID, kind, since)
	if err != nil {
		return fmt.Errorf("sqlite: check %s alert: %w", kind, err)
	}
	if sent {
		return nil
	}
	if err := appendNotificationsTx(ctx, tx, notifications); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlite: commit alert unit: %w", err)
	}
	return nil
}
