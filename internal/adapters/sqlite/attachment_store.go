package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// attachmentStore reads ticket attachments (issue #366). It never writes:
// insertAttachmentsTx is called by the units of work that create a ticket or
// a comment, inside their transaction.
type attachmentStore struct {
	db *sql.DB
}

var _ application.AttachmentStore = (*attachmentStore)(nil)

func newAttachmentStore(db *sql.DB) *attachmentStore { return &attachmentStore{db: db} }

// ListByTicket returns the ticket's attachments in upload order without their
// data. includeInternal=false drops the files of internal comments in SQL, the
// same boundary the comment list applies.
func (as *attachmentStore) ListByTicket(ctx context.Context, ticketID int64, includeInternal bool) ([]domain.Attachment, error) {
	query := `SELECT a.id, a.ticket_id, a.comment_id, a.file_name, a.content_type, a.size, a.uploader_user_id, a.created_at
		FROM attachments a LEFT JOIN comments c ON c.id = a.comment_id
		WHERE a.ticket_id = ?`
	if !includeInternal {
		query += ` AND (a.comment_id IS NULL OR c.visibility = 'public')`
	}
	query += ` ORDER BY a.id`
	rows, err := as.db.QueryContext(ctx, query, ticketID)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list attachments: %w", err)
	}
	defer rows.Close()
	out := []domain.Attachment{}
	for rows.Next() {
		var a domain.Attachment
		var commentID, uploader sql.NullInt64
		var createdAt string
		if err := rows.Scan(&a.ID, &a.TicketID, &commentID, &a.FileName, &a.ContentType, &a.Size, &uploader, &createdAt); err != nil {
			return nil, fmt.Errorf("sqlite: scan attachment: %w", err)
		}
		if err := fillAttachment(&a, commentID, uploader, createdAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: list attachments: %w", err)
	}
	return out, nil
}

// Get returns one attachment with its data and the visibility of its comment
// (empty for a creation file), so the caller can authorise the download.
func (as *attachmentStore) Get(ctx context.Context, id int64) (*application.StoredAttachment, error) {
	var out application.StoredAttachment
	var commentID, uploader sql.NullInt64
	var createdAt string
	var visibility sql.NullString
	err := as.db.QueryRowContext(ctx, `SELECT a.id, a.ticket_id, a.comment_id, a.file_name, a.content_type, a.size, a.data, a.uploader_user_id, a.created_at, c.visibility
		FROM attachments a LEFT JOIN comments c ON c.id = a.comment_id
		WHERE a.id = ?`, id).
		Scan(&out.ID, &out.TicketID, &commentID, &out.FileName, &out.ContentType, &out.Size, &out.Data, &uploader, &createdAt, &visibility)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, &domain.NotFoundError{Kind: "attachment", ID: id}
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite: get attachment: %w", err)
	}
	if err := fillAttachment(&out.Attachment, commentID, uploader, createdAt); err != nil {
		return nil, err
	}
	if visibility.Valid {
		out.Visibility = domain.CommentVisibility(visibility.String)
	}
	return &out, nil
}

func fillAttachment(a *domain.Attachment, commentID, uploader sql.NullInt64, createdAt string) error {
	if commentID.Valid {
		v := commentID.Int64
		a.CommentID = &v
	}
	if uploader.Valid {
		v := uploader.Int64
		a.UploaderUserID = &v
	}
	t, err := time.Parse(timeLayout, createdAt)
	if err != nil {
		return fmt.Errorf("sqlite: parse attachment created_at %q: %w", createdAt, err)
	}
	a.CreatedAt = t
	return nil
}

// insertAttachmentsTx stores a submission's files inside the caller's
// transaction, stamping the ticket and (for a comment's files) the comment,
// and assigning each its id. The schema refuses a comment of another ticket.
func insertAttachmentsTx(ctx context.Context, tx *sql.Tx, ticketID int64, commentID *int64, files []domain.Attachment) error {
	for i := range files {
		a := &files[i]
		a.TicketID = ticketID
		a.CommentID = commentID
		res, err := tx.ExecContext(ctx, `INSERT INTO attachments (ticket_id, comment_id, file_name, content_type, size, data, uploader_user_id, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			ticketID, nullableInt64(commentID), a.FileName, a.ContentType, a.Size, a.Data,
			nullableInt64(a.UploaderUserID), formatTime(a.CreatedAt))
		if err != nil {
			return fmt.Errorf("sqlite: insert attachment %q: %w", a.FileName, err)
		}
		if a.ID, err = res.LastInsertId(); err != nil {
			return fmt.Errorf("sqlite: attachment id: %w", err)
		}
	}
	return nil
}
