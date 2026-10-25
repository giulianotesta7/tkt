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

// cannedReplyStore persists saved replies over the canned_replies table of
// migration 0033 (issue #418). The table guarantees exactly one
// owner through its CHECK; this adapter adds the facts the port promises and
// the CHECK cannot say: an unknown owner is ErrNotFound instead of a raw
// foreign-key failure, and every list is deterministic.
type cannedReplyStore struct{ db *sql.DB }

var _ application.CannedReplyStore = (*cannedReplyStore)(nil)

func newCannedReplyStore(db *sql.DB) *cannedReplyStore { return &cannedReplyStore{db: db} }

// CannedReplyStore returns the saved-reply port (issue #418).
func (s *Store) CannedReplyStore() application.CannedReplyStore {
	return newCannedReplyStore(s.db)
}

// cannedReplyColumns is the projection every read in this file shares, in the
// order scanCannedReply expects. The qualified form exists because ListUsable
// joins desks, which also has an id column.
const (
	cannedReplyColumns          = `id, owner_user_id, desk_id, title, body, created_at, updated_at`
	cannedReplyColumnsQualified = `cr.id, cr.owner_user_id, cr.desk_id, cr.title, cr.body, cr.created_at, cr.updated_at`
)

func (s *cannedReplyStore) Create(ctx context.Context, r *domain.CannedReply) error {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO canned_replies (owner_user_id, desk_id, title, body, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		nullableInt64(r.OwnerUserID), nullableInt64(r.DeskID), r.Title, r.Body, formatTime(r.CreatedAt), formatTime(r.UpdatedAt))
	if err != nil {
		if isForeignKeyViolation(err) {
			// The owner columns are the only foreign keys of the table, so the
			// failing one names the missing row. A reply whose owner does not
			// exist is not a bad request: it is a missing reference.
			if r.OwnerUserID != nil {
				return &domain.NotFoundError{Kind: "user", ID: *r.OwnerUserID}
			}
			return &domain.NotFoundError{Kind: "desk", ID: *r.DeskID}
		}
		return fmt.Errorf("sqlite: create canned reply: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("sqlite: canned reply id: %w", err)
	}
	r.ID = id
	return nil
}

func (s *cannedReplyStore) Update(ctx context.Context, r *domain.CannedReply) error {
	// Ownership is deliberately absent from the SET list: a saved reply never
	// moves between people or desks, so even a stale caller cannot reassign one.
	res, err := s.db.ExecContext(ctx,
		`UPDATE canned_replies SET title = ?, body = ?, updated_at = ? WHERE id = ?`,
		r.Title, r.Body, formatTime(r.UpdatedAt), r.ID)
	if err != nil {
		return fmt.Errorf("sqlite: update canned reply: %w", err)
	}
	return rowsFound(res, "canned reply", r.ID)
}

func (s *cannedReplyStore) Delete(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM canned_replies WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("sqlite: delete canned reply: %w", err)
	}
	return rowsFound(res, "canned reply", id)
}

func (s *cannedReplyStore) GetByID(ctx context.Context, id int64) (*domain.CannedReply, error) {
	r, err := scanCannedReply(s.db.QueryRowContext(ctx, `SELECT `+cannedReplyColumns+` FROM canned_replies WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, &domain.NotFoundError{Kind: "canned reply", ID: id}
	}
	return r, err
}

func (s *cannedReplyStore) ListPersonal(ctx context.Context, ownerUserID int64) ([]domain.CannedReply, error) {
	return s.list(ctx,
		`SELECT `+cannedReplyColumns+` FROM canned_replies WHERE owner_user_id = ? ORDER BY title ASC, id ASC`,
		ownerUserID)
}

func (s *cannedReplyStore) ListByDesk(ctx context.Context, deskID int64) ([]domain.CannedReply, error) {
	return s.list(ctx,
		`SELECT `+cannedReplyColumns+` FROM canned_replies WHERE desk_id = ? ORDER BY title ASC, id ASC`,
		deskID)
}

// ListUsable reads the composer library in ONE query. The membership test is a
// join against desk_members — the real membership table, not a role check — so
// the grant is exactly the row that makes somebody a member of the desk.
// The LEFT JOIN on desks exists only to order desk groups by desk name; a
// personal reply has no desk and sorts first because
// `owner_user_id IS NULL` is 0 for it.
func (s *cannedReplyStore) ListUsable(ctx context.Context, userID int64) ([]domain.CannedReply, error) {
	return s.list(ctx,
		`SELECT `+cannedReplyColumnsQualified+`
		 FROM canned_replies cr
		 LEFT JOIN desks d ON d.id = cr.desk_id
		 WHERE cr.owner_user_id = ?
		    OR EXISTS (SELECT 1 FROM desk_members dm WHERE dm.desk_id = cr.desk_id AND dm.user_id = ?)
		 ORDER BY (cr.owner_user_id IS NULL) ASC, d.name ASC, cr.title ASC, cr.id ASC`,
		userID, userID)
}

// list runs the given read and scans every row, adding the query to any
// failure so a broken read is diagnosable from the error alone.
func (s *cannedReplyStore) list(ctx context.Context, query string, args ...any) ([]domain.CannedReply, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list canned replies: %w", err)
	}
	defer rows.Close()
	var replies []domain.CannedReply
	for rows.Next() {
		r, err := scanCannedReply(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: scan canned reply: %w", err)
		}
		replies = append(replies, *r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: list canned replies: %w", err)
	}
	return replies, nil
}

func scanCannedReply(scan rowScanner) (*domain.CannedReply, error) {
	var r domain.CannedReply
	var ownerUserID, deskID sql.NullInt64
	var createdAt, updatedAt string
	if err := scan.Scan(&r.ID, &ownerUserID, &deskID, &r.Title, &r.Body, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	if ownerUserID.Valid {
		r.OwnerUserID = &ownerUserID.Int64
	}
	if deskID.Valid {
		r.DeskID = &deskID.Int64
	}
	var err error
	if r.CreatedAt, err = time.Parse(timeLayout, createdAt); err != nil {
		return nil, fmt.Errorf("sqlite: parse canned reply created_at %q: %w", createdAt, err)
	}
	if r.UpdatedAt, err = time.Parse(timeLayout, updatedAt); err != nil {
		return nil, fmt.Errorf("sqlite: parse canned reply updated_at %q: %w", updatedAt, err)
	}
	return &r, nil
}
