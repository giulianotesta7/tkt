package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// RejectPending settles a waiting account without deleting its external identity.
// Rechecking the target role and pending flag under the immediate transaction
// protects both the management scope and an approval/rejection racing this batch.
func (ps *pendingUserStore) RejectPending(ctx context.Context, ids []int64, manageableRoles []domain.Role) ([]domain.User, error) {
	if err := application.ValidatePendingRejection(ids); err != nil {
		return nil, err
	}
	tx, err := beginImmediate(ctx, ps.db, "reject pending users")
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rejected := make([]domain.User, 0, len(ids))
	for _, id := range ids {
		u, err := scanUserWithPending(tx.QueryRowContext(ctx,
			`SELECT id, name, email, password_hash, role, active, pending, created_at FROM users WHERE id = ?`, id))
		if errors.Is(err, sql.ErrNoRows) {
			return nil, &domain.NotFoundError{Kind: "user", ID: id}
		}
		if err != nil {
			return nil, fmt.Errorf("sqlite: read rejected pending user: %w", err)
		}
		if u.Role == domain.RoleRoot {
			return nil, domain.NewRootProtectedError()
		}
		if !u.Pending {
			return nil, &domain.NotFoundError{Kind: "user", ID: id}
		}
		if !slices.Contains(manageableRoles, u.Role) {
			return nil, domain.NewForbiddenError("admin accounts require root")
		}
		res, err := tx.ExecContext(ctx,
			`UPDATE users SET pending = 0, active = 0 WHERE id = ? AND pending = 1 AND role = ?`, id, string(u.Role))
		if err != nil {
			return nil, fmt.Errorf("sqlite: reject pending user: %w", err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return nil, fmt.Errorf("sqlite: reject pending user rows: %w", err)
		}
		if n != 1 {
			return nil, &domain.NotFoundError{Kind: "user", ID: id}
		}
		u.Pending, u.Active = false, false
		rejected = append(rejected, *u)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("sqlite: commit pending rejections: %w", err)
	}
	return rejected, nil
}
