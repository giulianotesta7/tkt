package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// passwordChangeStore implements application.PasswordChangeStore (issue #414,
// S2): the signed-in user's own password change, written as ONE immediate
// transaction together with the sign-out of their other sessions.
type passwordChangeStore struct {
	db *sql.DB
}

var _ application.PasswordChangeStore = (*passwordChangeStore)(nil)

func newPasswordChangeStore(db *sql.DB) *passwordChangeStore {
	return &passwordChangeStore{db: db}
}

// ChangePasswordAndSignOutOtherSessions replaces userID's password hash and
// deletes every session of that user except keepSessionID, in ONE immediate
// transaction: either the new password is stored AND the other devices are
// signed out, or neither happens. Sessions of other users are never selected.
//
// The hash write runs FIRST so an id no user row matches answers ErrNotFound
// before a single session is deleted. The kept session is then resolved inside
// the same transaction and must belong to that user: a request naming another
// account's session (or one the table no longer holds) is refused, and the hash
// write it travelled with rolls back, because the alternative is signing the
// actor out of every device including the one in use.
func (us *passwordChangeStore) ChangePasswordAndSignOutOtherSessions(ctx context.Context, userID int64, passwordHash, keepSessionID string) error {
	tx, err := beginImmediate(ctx, us.db, "change password and sign out other sessions")
	if err != nil {
		return err
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, `UPDATE users SET password_hash = ? WHERE id = ?`, passwordHash, userID)
	if err != nil {
		return fmt.Errorf("sqlite: change password hash: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("sqlite: change password hash rows: %w", err)
	}
	if n == 0 {
		return &domain.NotFoundError{Kind: "user", ID: userID}
	}

	var keeper int64
	err = tx.QueryRowContext(ctx, `SELECT user_id FROM sessions WHERE id = ?`, keepSessionID).Scan(&keeper)
	if errors.Is(err, sql.ErrNoRows) {
		return &domain.NotFoundError{Kind: "session", ID: keepSessionID}
	}
	if err != nil {
		return fmt.Errorf("sqlite: read the session making the change: %w", err)
	}
	if keeper != userID {
		return domain.NewForbiddenError("the session making the change belongs to another account")
	}

	// Seam for #414's recovery path (T3, S3): the user's pending reset tokens
	// are voided HERE, in this same transaction, so a link mailed before the
	// change can never set a password after it. The table arrives with
	// migration 0029; it does not exist yet, so nothing is deleted here today.
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ? AND id <> ?`, userID, keepSessionID); err != nil {
		return fmt.Errorf("sqlite: sign out the other sessions: %w", err)
	}
	return tx.Commit()
}
