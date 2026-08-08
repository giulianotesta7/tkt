package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// commentStore implements application.CommentStore (task 4.3): the
// append-only comment timeline (comment-timeline spec). There is no update
// or delete path — the port surface is the guard.
type commentStore struct {
	db *sql.DB
}

var _ application.CommentStore = (*commentStore)(nil)

func newCommentStore(db *sql.DB) *commentStore { return &commentStore{db: db} }

// Add stores c, assigning c.ID. A comment referencing an unknown ticket or
// with an empty body fails the FK / CHECK constraint.
func (cs *commentStore) Add(ctx context.Context, c *domain.Comment) error {
	res, err := cs.db.ExecContext(ctx, `INSERT INTO comments (ticket_id, author, body, created_at)
		VALUES (?, ?, ?, ?)`,
		c.TicketID, c.Author, c.Body, formatTime(c.CreatedAt))
	if err != nil {
		return fmt.Errorf("sqlite: add comment: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("sqlite: comment id: %w", err)
	}
	c.ID = id
	return nil
}

// ListByTicket returns the ticket's comments in creation order (ASC); the
// id tiebreak preserves insertion order when timestamps are equal.
func (cs *commentStore) ListByTicket(ctx context.Context, ticketID int64) ([]domain.Comment, error) {
	rows, err := cs.db.QueryContext(ctx,
		`SELECT id, ticket_id, author, body, created_at FROM comments
		 WHERE ticket_id = ? ORDER BY created_at ASC, id ASC`, ticketID)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list comments: %w", err)
	}
	defer rows.Close()

	var out []domain.Comment
	for rows.Next() {
		var c domain.Comment
		var createdAt string
		if err := rows.Scan(&c.ID, &c.TicketID, &c.Author, &c.Body, &createdAt); err != nil {
			return nil, fmt.Errorf("sqlite: scan comment: %w", err)
		}
		if c.CreatedAt, err = time.Parse(timeLayout, createdAt); err != nil {
			return nil, fmt.Errorf("sqlite: parse comment created_at %q: %w", createdAt, err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: list comments: %w", err)
	}
	return out, nil
}
