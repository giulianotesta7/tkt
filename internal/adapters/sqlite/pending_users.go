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

// pendingApprovalReason is the role_changes reason recorded when an approval
// grants a role the waiting account did not have.
const pendingApprovalReason = "pending approval"

// pendingUserStore implements application.PendingUserStore over migration 0034
// (issue #419, S3). The pending column is the whole read: an account that waits
// for approval is inactive, so no other user read reaches it. Approval and
// rejection settle the flag without deleting the account or its identities.
type pendingUserStore struct {
	db *sql.DB
}

var _ application.PendingUserStore = (*pendingUserStore)(nil)

func newPendingUserStore(db *sql.DB) *pendingUserStore { return &pendingUserStore{db: db} }

// PendingUserStore returns the Pending tab port (issue #419, S3).
func (s *Store) PendingUserStore() application.PendingUserStore {
	return newPendingUserStore(s.db)
}

// ListPending answers every account waiting for approval, ordered by id, with
// the pending flag set and the role it currently holds (always a requester, the
// role a company sign-in creates).
func (ps *pendingUserStore) ListPending(ctx context.Context) ([]domain.User, error) {
	rows, err := ps.db.QueryContext(ctx,
		`SELECT id, name, email, password_hash, role, active, pending, kind, created_at
		 FROM users WHERE pending = 1 ORDER BY id ASC`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list pending users: %w", err)
	}
	defer rows.Close()
	var out []domain.User
	for rows.Next() {
		u, err := scanUserWithPending(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: scan pending user: %w", err)
		}
		out = append(out, *u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: list pending users: %w", err)
	}
	return out, nil
}

// ListPendingSignIn answers the provider identities held by the accounts that
// wait for approval (issue #419, S3/S12): one row per stored identity, ordered
// by account id and then provider, so the read is deterministic without a
// display decision. The join is what makes it factual — an ordinary account's
// identity never reaches the Pending tab, an account with no identity has no row,
// and only the account id and the provider travel: the issuer, the subject and
// the address last seen at the provider stay in the identity's own record.
func (ps *pendingUserStore) ListPendingSignIn(ctx context.Context) ([]application.PendingSignIn, error) {
	rows, err := ps.db.QueryContext(ctx,
		`SELECT i.user_id, i.provider
		 FROM user_identities i
		 JOIN users u ON u.id = i.user_id
		 WHERE u.pending = 1
		 ORDER BY i.user_id ASC, i.provider ASC`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list pending sign-in methods: %w", err)
	}
	defer rows.Close()
	var out []application.PendingSignIn
	for rows.Next() {
		var row application.PendingSignIn
		var provider string
		if err := rows.Scan(&row.UserID, &provider); err != nil {
			return nil, fmt.Errorf("sqlite: scan pending sign-in method: %w", err)
		}
		row.Provider = application.CompanyProvider(provider)
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: list pending sign-in methods: %w", err)
	}
	return out, nil
}

// ApprovePending turns each named account into a managed user in ONE immediate
// transaction: the pending flag clears and active is set together (the 0034
// CHECK refuses any other order), the chosen role is written, and a role_changes
// row records the grant when the role actually changes.
//
// Every entry is read and guarded before its write, so a stale id, an account
// that is no longer pending, a repeated id, an invalid role, or a root role
// rolls the whole batch back: a bulk approval never leaves half a decision
// behind. Root is refused here as well as in the service, because no approval
// may create or promote one (S9).
func (ps *pendingUserStore) ApprovePending(ctx context.Context, approvals []application.PendingApproval, actorID int64, at time.Time) ([]domain.User, error) {
	if err := application.ValidatePendingApprovals(approvals); err != nil {
		return nil, err
	}
	for _, approval := range approvals {
		if approval.Role == domain.RoleRoot {
			return nil, domain.NewRootProtectedError()
		}
	}
	tx, err := beginImmediate(ctx, ps.db, "approve pending users")
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	approved := make([]domain.User, 0, len(approvals))
	for _, approval := range approvals {
		u, err := scanUserWithPending(tx.QueryRowContext(ctx,
			`SELECT id, name, email, password_hash, role, active, pending, kind, created_at FROM users WHERE id = ?`, approval.ID))
		if errors.Is(err, sql.ErrNoRows) {
			return nil, &domain.NotFoundError{Kind: "user", ID: approval.ID}
		}
		if err != nil {
			return nil, fmt.Errorf("sqlite: read pending user: %w", err)
		}
		if u.Role == domain.RoleRoot {
			return nil, domain.NewRootProtectedError()
		}
		if !u.Pending {
			// The selection is stale: this account was approved, or was never
			// waiting at all, and an approval must not touch it.
			return nil, &domain.NotFoundError{Kind: "user", ID: approval.ID}
		}
		res, err := tx.ExecContext(ctx,
			`UPDATE users SET pending = 0, active = 1, role = ? WHERE id = ? AND role = ? AND pending = 1`,
			string(approval.Role), approval.ID, string(u.Role))
		if err != nil {
			return nil, fmt.Errorf("sqlite: approve pending user: %w", err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return nil, fmt.Errorf("sqlite: approve pending user rows: %w", err)
		}
		if n == 0 {
			return nil, &domain.NotFoundError{Kind: "user", ID: approval.ID}
		}
		if u.Role != approval.Role {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO role_changes (user_id, from_role, to_role, actor_user_id, reason, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
				u.ID, string(u.Role), string(approval.Role), actorID, pendingApprovalReason, formatTime(at)); err != nil {
				return nil, fmt.Errorf("sqlite: audit approval role change: %w", err)
			}
		}
		u.Role = approval.Role
		u.Active = true
		u.Pending = false
		approved = append(approved, *u)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("sqlite: commit pending approvals: %w", err)
	}
	return approved, nil
}
