package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
)

// ticketVisitStore is the SQLite adapter of application.TicketVisitStore
// (issue #415, migration 0030).
type ticketVisitStore struct {
	db *sql.DB
}

func newTicketVisitStore(db *sql.DB) *ticketVisitStore { return &ticketVisitStore{db: db} }

// Mark reads the newest audit event and comment ids of the ticket. A ticket
// with no history answers zero marks.
func (vs *ticketVisitStore) Mark(ctx context.Context, ticketID int64) (application.VisitMark, error) {
	var mark application.VisitMark
	if err := vs.db.QueryRowContext(ctx,
		`SELECT (SELECT COALESCE(MAX(id), 0) FROM audit_events WHERE ticket_id = ?),
		        (SELECT COALESCE(MAX(id), 0) FROM comments WHERE ticket_id = ?)`,
		ticketID, ticketID).Scan(&mark.AuditID, &mark.CommentID); err != nil {
		return application.VisitMark{}, fmt.Errorf("sqlite: ticket visit mark: %w", err)
	}
	return mark, nil
}

// RecordVisit upserts the visit with MAX on both marks, so a slower
// concurrent open never lowers them, and marks the reader's unread
// notifications about the ticket read in the same transaction.
func (vs *ticketVisitStore) RecordVisit(ctx context.Context, userID, ticketID int64, mark application.VisitMark, at time.Time) error {
	tx, err := vs.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlite: begin ticket visit: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO ticket_visits (user_id, ticket_id, audit_seen_id, comment_seen_id, visited_at) VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT (user_id, ticket_id) DO UPDATE SET
		   audit_seen_id = MAX(audit_seen_id, excluded.audit_seen_id),
		   comment_seen_id = MAX(comment_seen_id, excluded.comment_seen_id),
		   visited_at = excluded.visited_at`,
		userID, ticketID, mark.AuditID, mark.CommentID, formatTime(at)); err != nil {
		return fmt.Errorf("sqlite: record ticket visit: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE notifications SET read_at = ? WHERE user_id = ? AND ticket_id = ? AND read_at IS NULL`,
		formatTime(at), userID, ticketID); err != nil {
		return fmt.Errorf("sqlite: read ticket notifications: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlite: commit ticket visit: %w", err)
	}
	return nil
}

// Changed answers one query per page: each ticket id is tested for an audit
// event or a visible comment past the reader's marks (zero when never opened)
// whose actor is not the reader. A NULL actor is an automatic or legacy action
// and counts.
func (vs *ticketVisitStore) Changed(ctx context.Context, userID int64, ticketIDs []int64, includeInternal bool) (map[int64]bool, error) {
	out := map[int64]bool{}
	if len(ticketIDs) == 0 {
		return out, nil
	}
	args := make([]any, 0, len(ticketIDs)+4)
	args = append(args, userID, userID, userID, includeInternal)
	for _, id := range ticketIDs {
		args = append(args, id)
	}
	rows, err := vs.db.QueryContext(ctx,
		`SELECT t.id FROM tickets t
		 LEFT JOIN ticket_visits v ON v.ticket_id = t.id AND v.user_id = ?
		 WHERE (EXISTS (SELECT 1 FROM audit_events a
		          WHERE a.ticket_id = t.id AND a.id > COALESCE(v.audit_seen_id, 0)
		            AND (a.actor_user_id IS NULL OR a.actor_user_id <> ?))
		     OR EXISTS (SELECT 1 FROM comments c
		          WHERE c.ticket_id = t.id AND c.id > COALESCE(v.comment_seen_id, 0)
		            AND (c.author_user_id IS NULL OR c.author_user_id <> ?)
		            AND (? OR c.visibility = 'public')))
		   AND t.id IN (?`+strings.Repeat(", ?", len(ticketIDs)-1)+`)`, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: changed tickets: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("sqlite: scan changed ticket: %w", err)
		}
		out[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: changed tickets: %w", err)
	}
	return out, nil
}
