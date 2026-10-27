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

// passwordResetStore implements application.PasswordResetStore (issue #414,
// S3/S8/S9): the single-use recovery links of migration 0029. The links are
// only ever stored as hashes, instants come from the caller, and every write
// that changes more than one row does it in ONE immediate transaction.
type passwordResetStore struct {
	db *sql.DB
}

var _ application.PasswordResetStore = (*passwordResetStore)(nil)

func newPasswordResetStore(db *sql.DB) *passwordResetStore {
	return &passwordResetStore{db: db}
}

// LastPasswordResetRequest answers the newest link instant of one account. The
// column holds the persisted ISO-8601 UTC form, so a MAX over it is the newest
// instant; an account that never asked answers nil. Spent links count, because
// the throttle measures how recently this account was mailed (S9).
func (rs *passwordResetStore) LastPasswordResetRequest(ctx context.Context, userID int64) (*time.Time, error) {
	var newest sql.NullString
	if err := rs.db.QueryRowContext(ctx,
		`SELECT MAX(created_at) FROM password_reset_tokens WHERE user_id = ?`, userID).Scan(&newest); err != nil {
		return nil, fmt.Errorf("sqlite: read the newest password reset request: %w", err)
	}
	if !newest.Valid {
		return nil, nil
	}
	at, err := time.Parse(timeLayout, newest.String)
	if err != nil {
		return nil, fmt.Errorf("sqlite: parse the newest password reset request: %w", err)
	}
	return &at, nil
}

// IssuePasswordResetToken voids the account's pending links and stores the new
// one in ONE immediate transaction: two working links for one account would
// break "a new request voids the older link", and a request that failed
// half-way would leave the account with none. Spent links stay as history.
func (rs *passwordResetStore) IssuePasswordResetToken(ctx context.Context, userID int64, tokenHash string, createdAt, expiresAt time.Time) error {
	tx, err := beginImmediate(ctx, rs.db, "issue password reset token")
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM password_reset_tokens WHERE user_id = ? AND used_at IS NULL`, userID); err != nil {
		return fmt.Errorf("sqlite: void the pending password reset links: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO password_reset_tokens (user_id, token_hash, created_at, expires_at) VALUES (?, ?, ?, ?)`,
		userID, tokenHash, formatTime(createdAt), formatTime(expiresAt)); err != nil {
		return fmt.Errorf("sqlite: store the password reset link: %w", err)
	}
	return tx.Commit()
}

// PasswordResetTokenUsable reports whether the hash can still be redeemed at
// now. It reads one row and consumes nothing, so the reset page can ask before
// rendering its form; unknown, spent and expired all answer false.
func (rs *passwordResetStore) PasswordResetTokenUsable(ctx context.Context, tokenHash string, now time.Time) (bool, error) {
	facts, err := readResetToken(ctx, rs.db, tokenHash)
	if err != nil {
		return false, err
	}
	return facts.usableAt(now), nil
}

// PasswordResetTokenOwner answers the account a still-usable link belongs to
// (issue #419, S5). It reads the same row PasswordResetTokenUsable reads and
// consumes nothing.
func (rs *passwordResetStore) PasswordResetTokenOwner(ctx context.Context, tokenHash string, now time.Time) (int64, bool, error) {
	facts, err := readResetToken(ctx, rs.db, tokenHash)
	if err != nil {
		return 0, false, err
	}
	if !facts.usableAt(now) {
		return 0, false, nil
	}
	return facts.userID, true, nil
}

// RedeemPasswordResetToken spends one link in ONE immediate transaction: the
// new password hash, the spent mark, the voiding of the account's other pending
// links and the deletion of EVERY session of that account commit together
// (S3). The transaction begins immediate, so two redeemals of the same hash
// serialize and the loser reads a link that is already spent.
//
// The unusable cases — unknown hash, already spent, expired — answer
// domain.ErrNotFound and write nothing, because the adapter has no business
// telling them apart and the use case refuses all three the same way. Expiry is
// compared strictly: a link is usable while now is BEFORE its instant, so at
// exactly its expiry it is already refused.
func (rs *passwordResetStore) RedeemPasswordResetToken(ctx context.Context, tokenHash, passwordHash string, now time.Time) error {
	tx, err := beginImmediate(ctx, rs.db, "redeem password reset token")
	if err != nil {
		return err
	}
	defer tx.Rollback()

	facts, err := readResetToken(ctx, tx, tokenHash)
	if err != nil {
		return err
	}
	if !facts.usableAt(now) {
		return domain.ErrNotFound
	}

	res, err := tx.ExecContext(ctx, `UPDATE users SET password_hash = ? WHERE id = ?`, passwordHash, facts.userID)
	if err != nil {
		return fmt.Errorf("sqlite: store the recovered password: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("sqlite: store the recovered password rows: %w", err)
	}
	if n == 0 {
		// The foreign key makes this unreachable (a link cannot outlive its
		// account), so a missing row answers the same refusal instead of
		// committing a spent link that changed nobody's password.
		return domain.ErrNotFound
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE password_reset_tokens SET used_at = ? WHERE token_hash = ?`, formatTime(now), tokenHash); err != nil {
		return fmt.Errorf("sqlite: mark the password reset link spent: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM password_reset_tokens WHERE user_id = ? AND used_at IS NULL`, facts.userID); err != nil {
		return fmt.Errorf("sqlite: void the other password reset links: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, facts.userID); err != nil {
		return fmt.Errorf("sqlite: sign out every session of the account: %w", err)
	}
	return tx.Commit()
}

// rowQueryer is the read half both the port's own read and the redemption's
// in-transaction read need, so the same query answers both.
type rowQueryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// resetTokenFacts is one stored link as the redemption read needs it.
type resetTokenFacts struct {
	userID    int64
	expiresAt time.Time
	spent     bool
	found     bool
}

// usableAt reports whether the link can be redeemed at now: it exists, it was
// never spent, and the instant it stops working has not been reached.
func (f resetTokenFacts) usableAt(now time.Time) bool {
	return f.found && !f.spent && now.Before(f.expiresAt)
}

// readResetToken reads the row behind tokenHash. A hash no row matches is not an
// error: it is an unusable link, which the caller refuses like a spent one.
func readResetToken(ctx context.Context, q rowQueryer, tokenHash string) (resetTokenFacts, error) {
	var (
		userID    int64
		expiresAt string
		usedAt    sql.NullString
	)
	err := q.QueryRowContext(ctx,
		`SELECT user_id, expires_at, used_at FROM password_reset_tokens WHERE token_hash = ?`, tokenHash).
		Scan(&userID, &expiresAt, &usedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return resetTokenFacts{}, nil
	}
	if err != nil {
		return resetTokenFacts{}, fmt.Errorf("sqlite: read the password reset link: %w", err)
	}
	expiry, err := time.Parse(timeLayout, expiresAt)
	if err != nil {
		return resetTokenFacts{}, fmt.Errorf("sqlite: parse the password reset link expiry: %w", err)
	}
	return resetTokenFacts{userID: userID, expiresAt: expiry, spent: usedAt.Valid, found: true}, nil
}
