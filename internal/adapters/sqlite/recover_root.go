package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// RecoverRoot is the one-shot operator-selected root recovery (design
// "Persistence and Recovery"; role-authorization "Operator-Selected Root
// Recovery"). Everything runs in ONE immediate transaction (_txlock=immediate):
//
//  1. verify NO root exists — when a root is present there is nothing to
//     recover and a second root must never appear (fail closed);
//  2. verify the selected user exists (fail closed on unknown ids);
//  3. activate and promote that user to root, recording the recovery in
//     role_changes with actor NULL (an operator action, no session actor)
//     and the explicit reason.
//
// The promotion is a plain role write on a NON-root row, so the root-
// immutability triggers (which fire only when OLD.role = 'root') do not
// interfere. Recovery never guesses: the operator's id is the only input.
func (us *userStore) RecoverRoot(ctx context.Context, id int64) (*domain.User, error) {
	tx, err := us.db.BeginTx(ctx, nil) // _txlock=immediate → BEGIN IMMEDIATE
	if err != nil {
		return nil, fmt.Errorf("sqlite: begin recover root: %w", err)
	}
	defer tx.Rollback()

	var rootCount int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE role = 'root'`).Scan(&rootCount); err != nil {
		return nil, fmt.Errorf("sqlite: recover count roots: %w", err)
	}
	if rootCount > 0 {
		return nil, errors.New("sqlite: a root already exists; recovery refused")
	}

	var (
		u         domain.User
		fromRole  string
		activeInt int64
	)
	if err := tx.QueryRowContext(ctx,
		`SELECT id, name, email, role, active FROM users WHERE id = ?`, id).
		Scan(&u.ID, &u.Name, &u.Email, &fromRole, &activeInt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, &domain.NotFoundError{Kind: "user", ID: id}
		}
		return nil, fmt.Errorf("sqlite: recover select user: %w", err)
	}
	u.Role = domain.Role(fromRole)
	u.Active = activeInt == 1

	if _, err := tx.ExecContext(ctx, `UPDATE users SET role = 'root', active = 1 WHERE id = ?`, id); err != nil {
		return nil, fmt.Errorf("sqlite: recover promote: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO role_changes (user_id, from_role, to_role, actor_user_id, reason, created_at)
		 VALUES (?, ?, 'root', NULL, 'operator-selected root recovery', ?)`,
		id, fromRole, time.Now().UTC().Format(timeLayout)); err != nil {
		return nil, fmt.Errorf("sqlite: recover audit: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("sqlite: commit recover root: %w", err)
	}

	u.Role = domain.RoleRoot
	u.Active = true
	return &u, nil
}
