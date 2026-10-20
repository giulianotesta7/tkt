package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// notificationStore implements application.NotificationStore (issue #344,
// slice B1): the read/write half of the in-app notification center. The
// append half lives in appendNotificationsTx, because notification rows are
// written by the ticket and workflow units of work on THEIR transaction, never
// on a connection of their own (spec S11).
type notificationStore struct {
	db *sql.DB
}

var _ application.NotificationStore = (*notificationStore)(nil)

func newNotificationStore(db *sql.DB) *notificationStore { return &notificationStore{db: db} }

// notificationColumns is the projection shared by every notification read.
const notificationColumns = `id, user_id, ticket_id, kind, actor_user_id, from_value, to_value, comment_id, step_index, created_at, read_at`

// defaultNotificationListLimit bounds ListForUser when a caller asks for a
// non-positive limit. The panel always passes an explicit window; the default
// exists so a zero value can never turn into an unbounded scan of the whole
// table.
const defaultNotificationListLimit = 50

// appendNotificationsTx inserts the notifications inside the caller's
// transaction, in batch order (issue #344, spec S11). It mirrors
// appendAuditEventsTx: the ticket and workflow units of work call it right
// next to the audit append, so the rows commit or roll back with the change
// they describe. An empty batch is a no-op — a change whose recipients are
// only the actor writes no notification.
//
// Every row is validated before the first insert, so a policy bug that
// produces an unrenderable or self-addressed notification fails the batch
// (and therefore the whole unit of work) instead of persisting it.
func appendNotificationsTx(ctx context.Context, tx *sql.Tx, notifications []domain.Notification) error {
	for _, n := range notifications {
		if err := n.Validate(); err != nil {
			return err
		}
	}
	for _, n := range notifications {
		if _, err := tx.ExecContext(ctx, `INSERT INTO notifications (user_id, ticket_id, kind, actor_user_id, from_value, to_value, comment_id, step_index, created_at, read_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			n.UserID, n.TicketID, string(n.Kind), nullableInt64(n.ActorUserID),
			nullableString(n.FromValue), nullableString(n.ToValue), nullableInt64(n.CommentID),
			nullableInt(n.StepIndex), formatTime(n.CreatedAt), formatTimePtr(n.ReadAt)); err != nil {
			return fmt.Errorf("sqlite: append notification: %w", err)
		}
	}
	return nil
}

// ListForUser returns the recipient's notifications newest first. created_at
// DESC orders by day and instant (D7: RFC3339 sorts lexicographically); the id
// DESC tiebreak keeps one batch's rows newest-last-inserted first and keeps
// page boundaries stable.
func (ns *notificationStore) ListForUser(ctx context.Context, userID int64, limit int) ([]domain.Notification, error) {
	if limit <= 0 {
		limit = defaultNotificationListLimit
	}
	rows, err := ns.db.QueryContext(ctx,
		`SELECT `+notificationColumns+` FROM notifications
		 WHERE user_id = ? ORDER BY created_at DESC, id DESC LIMIT ?`, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list notifications: %w", err)
	}
	defer rows.Close()

	var out []domain.Notification
	for rows.Next() {
		n, err := scanNotificationFrom(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *n)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: list notifications: %w", err)
	}
	return out, nil
}

// UnreadCount returns how many of the recipient's notifications are unread.
func (ns *notificationStore) UnreadCount(ctx context.Context, userID int64) (int, error) {
	var count int
	if err := ns.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM notifications WHERE user_id = ? AND read_at IS NULL`, userID).Scan(&count); err != nil {
		return 0, fmt.Errorf("sqlite: count unread notifications: %w", err)
	}
	return count, nil
}

// MarkRead marks ONE of the recipient's notifications read. The user_id
// predicate is the row-scoping guard: a row owned by someone else matches
// nothing, so the caller gets domain.ErrNotFound — exactly like a missing id —
// and no cross-user write is possible. COALESCE keeps the FIRST read instant,
// so re-opening a notification never moves the "when" the panel shows.
func (ns *notificationStore) MarkRead(ctx context.Context, userID, id int64, at time.Time) error {
	res, err := ns.db.ExecContext(ctx,
		`UPDATE notifications SET read_at = COALESCE(read_at, ?) WHERE id = ? AND user_id = ?`,
		formatTime(at), id, userID)
	if err != nil {
		return fmt.Errorf("sqlite: mark notification read: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("sqlite: mark notification read: %w", err)
	}
	if affected == 0 {
		return &domain.NotFoundError{Kind: "notification", ID: id}
	}
	return nil
}

// MarkAllRead marks every unread notification of the recipient read in one
// statement. The user_id predicate keeps other users' rows out of it, and the
// read_at IS NULL predicate preserves the instant of rows already read.
func (ns *notificationStore) MarkAllRead(ctx context.Context, userID int64, at time.Time) error {
	if _, err := ns.db.ExecContext(ctx,
		`UPDATE notifications SET read_at = ? WHERE user_id = ? AND read_at IS NULL`,
		formatTime(at), userID); err != nil {
		return fmt.Errorf("sqlite: mark all notifications read: %w", err)
	}
	return nil
}

// scanNotificationFrom projects one row into a domain.Notification. The
// nullable facts (actor, change values, comment, step, read instant) map to
// nil pointers; every instant is parsed from the persisted RFC3339 form (D7).
func scanNotificationFrom(scan rowScanner) (*domain.Notification, error) {
	var (
		n         domain.Notification
		kind      string
		actor     sql.NullInt64
		fromValue sql.NullString
		toValue   sql.NullString
		commentID sql.NullInt64
		stepIndex sql.NullInt64
		createdAt string
		readAt    sql.NullString
	)
	if err := scan.Scan(&n.ID, &n.UserID, &n.TicketID, &kind, &actor, &fromValue, &toValue,
		&commentID, &stepIndex, &createdAt, &readAt); err != nil {
		return nil, fmt.Errorf("sqlite: scan notification: %w", err)
	}
	n.Kind = domain.NotificationKind(kind)
	if actor.Valid {
		v := actor.Int64
		n.ActorUserID = &v
	}
	if fromValue.Valid {
		v := fromValue.String
		n.FromValue = &v
	}
	if toValue.Valid {
		v := toValue.String
		n.ToValue = &v
	}
	if commentID.Valid {
		v := commentID.Int64
		n.CommentID = &v
	}
	if stepIndex.Valid {
		v := int(stepIndex.Int64)
		n.StepIndex = &v
	}
	var err error
	if n.CreatedAt, err = time.Parse(timeLayout, createdAt); err != nil {
		return nil, fmt.Errorf("sqlite: parse notification created_at %q: %w", createdAt, err)
	}
	if readAt.Valid {
		v, err := time.Parse(timeLayout, readAt.String)
		if err != nil {
			return nil, fmt.Errorf("sqlite: parse notification read_at %q: %w", readAt.String, err)
		}
		n.ReadAt = &v
	}
	return &n, nil
}
