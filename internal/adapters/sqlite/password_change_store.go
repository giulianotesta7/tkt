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

// ChangePasswordAndSignOutOtherSessions replaces userID's password hash, voids
// the user's pending recovery links and deletes every session of that user
// except keepSessionID, in ONE immediate transaction: either the new password is
// stored AND the other devices are signed out AND no older link can still set a
// password, or neither happens. Sessions of other users are never selected.
//
// The hash write runs FIRST and is a compare-and-set on the hash the caller
// verified the current password against, so it is the one statement that decides
// both refusals: an id no user row matches answers ErrNotFound, and a row whose
// hash has already been replaced answers the current-password refusal, because
// the proof this submission carries is stale. Either way not a single session or
// pending link is touched: the transaction never gets that far. The kept session
// is then resolved inside the same transaction and must belong to that user: a
// request naming another account's session (or one the table no longer holds) is
// refused, and the hash write it travelled with rolls back, because the
// alternative is signing the actor out of every device including the one in use.
func (us *passwordChangeStore) ChangePasswordAndSignOutOtherSessions(ctx context.Context, userID int64, expectedPasswordHash, passwordHash, keepSessionID string) error {
	tx, err := beginImmediate(ctx, us.db, "change password and sign out other sessions")
	if err != nil {
		return err
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, `UPDATE users SET password_hash = ? WHERE id = ? AND password_hash = ?`, passwordHash, userID, expectedPasswordHash)
	if err != nil {
		return fmt.Errorf("sqlite: change password hash: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("sqlite: change password hash rows: %w", err)
	}
	if n == 0 {
		// A compare-and-set that matches no row has two possible reasons, and they
		// are not the same answer to the person: the account is gone, or someone
		// else replaced the password between this request's verification and this
		// write. The row read tells them apart without writing anything.
		var storedHash string
		err = tx.QueryRowContext(ctx, `SELECT password_hash FROM users WHERE id = ?`, userID).Scan(&storedHash)
		if errors.Is(err, sql.ErrNoRows) {
			return &domain.NotFoundError{Kind: "user", ID: userID}
		}
		if err != nil {
			return fmt.Errorf("sqlite: read the password hash the change expected: %w", err)
		}
		return application.CurrentPasswordRefusal()
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

	// The user's pending recovery links go in this same transaction (S2): a link
	// mailed before the change must never set a password after it. Spent links
	// stay as history.
	if _, err := tx.ExecContext(ctx, `DELETE FROM password_reset_tokens WHERE user_id = ? AND used_at IS NULL`, userID); err != nil {
		return fmt.Errorf("sqlite: void the pending password reset links: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ? AND id <> ?`, userID, keepSessionID); err != nil {
		return fmt.Errorf("sqlite: sign out the other sessions: %w", err)
	}
	return tx.Commit()
}
