package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// commentUnitOfWork implements application.CommentUnitOfWork (issue #344 slice
// B3, specs S10/S12, S11's contract): the comment insert and the notifications
// the APPLICATION computed for it commit or roll back together. It follows the
// ticket unit of work's shape — one immediate transaction, the change first,
// the notifications appended next to it, and a deferred rollback that undoes
// everything when any step fails.
type commentUnitOfWork struct {
	db *sql.DB
}

var _ application.CommentUnitOfWork = (*commentUnitOfWork)(nil)

func newCommentUnitOfWork(db *sql.DB) *commentUnitOfWork { return &commentUnitOfWork{db: db} }

// Add inserts m.Comment (assigning its ID) and appends m.Notifications in the
// SAME transaction. The comment id is stamped onto every notification AFTER the
// insert, before the append: the notifications.comment_id foreign key needs the
// comment row, which is precisely why both writes share the transaction.
//
// One invariant is checked here rather than by the generic append: a comment
// notification's actor must BE the comment's author. The comment row already
// references that user (comments.author_user_id), so this keeps a notification
// from naming a user the comment does not — the users-deletion advisory the B1
// verifier raised: an actor reference with no cascade makes a user who is only
// a notification actor undeletable.
func (u *commentUnitOfWork) Add(ctx context.Context, m application.CommentMutation) error {
	if m.Comment == nil {
		return fmt.Errorf("sqlite: add comment unit: no comment")
	}
	tx, err := u.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlite: begin comment unit: %w", err)
	}
	defer tx.Rollback()
	if err := insertCommentTx(ctx, tx, m.Comment); err != nil {
		return err
	}
	for i := range m.Notifications {
		n := &m.Notifications[i]
		if m.Comment.AuthorUserID == nil || n.ActorUserID == nil || *n.ActorUserID != *m.Comment.AuthorUserID {
			return fmt.Errorf("sqlite: add comment unit: notification actor %v is not the comment author %v",
				nullIntDesc(n.ActorUserID), nullIntDesc(m.Comment.AuthorUserID))
		}
		id := m.Comment.ID
		n.CommentID = &id
	}
	if err := appendNotificationsTx(ctx, tx, m.Notifications); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlite: commit comment unit: %w", err)
	}
	return nil
}

// nullIntDesc renders an optional id for an error message.
func nullIntDesc(id *int64) string {
	if id == nil {
		return "NULL"
	}
	return fmt.Sprintf("%d", *id)
}

// commentInserter is the subset of *sql.DB and *sql.Tx the comment insert needs,
// so the plain store path and the unit-of-work path share ONE statement.
type commentInserter interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// insertCommentTx stores the comment on the given transaction, assigning its id.
// It is the ONE insertion the comment timeline has: the plain CommentStore.Add
// (fixtures and direct store use) and the unit of work both go through it, so
// the visibility fallback, the authorship columns, and the NULL role encoding
// cannot drift between the two paths.
func insertCommentTx(ctx context.Context, tx commentInserter, c *domain.Comment) error {
	vis := c.Visibility
	if vis == "" {
		vis = domain.CommentPublic
	}
	var authorRole *string
	if c.AuthorRole != "" {
		role := string(c.AuthorRole)
		authorRole = &role
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO comments (ticket_id, author, body, visibility, created_at, author_user_id, author_role)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		c.TicketID, c.Author, c.Body, string(vis), formatTime(c.CreatedAt),
		nullableInt64(c.AuthorUserID), nullableString(authorRole))
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
