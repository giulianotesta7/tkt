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

// sessionStore implements application.SessionStore (task 4.4): server-side
// login sessions (D14). GetByID enforces the 24h TTL against wall-clock
// time — the port has no clock parameter and the DB is the single source of
// truth for expiry — and purges the expired row lazily.
type sessionStore struct {
	db *sql.DB
}

var _ application.SessionStore = (*sessionStore)(nil)

func newSessionStore(db *sql.DB) *sessionStore { return &sessionStore{db: db} }

// Create stores s. The domain Session carries no CreatedAt (slice-2
// deviation #4), so the store stamps the NOT NULL column itself — store
// side time is acceptable per D7 (the render path never touches
// time.Now(); this column is write-only).
func (ss *sessionStore) Create(ctx context.Context, s *domain.Session) error {
	if _, err := ss.db.ExecContext(ctx, `INSERT INTO sessions (id, user_id, created_at, expires_at)
		VALUES (?, ?, ?, ?)`,
		s.ID, s.UserID, time.Now().UTC().Format(timeLayout), formatTime(s.ExpiresAt)); err != nil {
		return fmt.Errorf("sqlite: create session: %w", err)
	}
	return nil
}

// GetByID returns the session or ErrNotFound when missing or expired. An
// expired row is purged on lookup (lazy purge, design "Migration /
// Rollout").
func (ss *sessionStore) GetByID(ctx context.Context, id string) (*domain.Session, error) {
	var s domain.Session
	var expiresAt string
	err := ss.db.QueryRowContext(ctx, `SELECT id, user_id, expires_at FROM sessions WHERE id = ?`, id).
		Scan(&s.ID, &s.UserID, &expiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, &domain.NotFoundError{Kind: "session", ID: id}
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite: get session: %w", err)
	}
	if s.ExpiresAt, err = time.Parse(timeLayout, expiresAt); err != nil {
		return nil, fmt.Errorf("sqlite: parse session expires_at %q: %w", expiresAt, err)
	}
	if !time.Now().UTC().Before(s.ExpiresAt) {
		if _, err := ss.db.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, id); err != nil {
			return nil, fmt.Errorf("sqlite: purge expired session: %w", err)
		}
		return nil, &domain.NotFoundError{Kind: "session", ID: id}
	}
	return &s, nil
}

// Delete removes the session (logout). Deleting a missing session is a
// no-op — logout stays idempotent.
func (ss *sessionStore) Delete(ctx context.Context, id string) error {
	if _, err := ss.db.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, id); err != nil {
		return fmt.Errorf("sqlite: delete session: %w", err)
	}
	return nil
}
