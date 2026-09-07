package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// These methods keep the unified Categories screen on the existing Desk table
// and membership contract. DeskStore remains available to workflow/runtime code.
func (cs *catalogStore) GetDeskByID(ctx context.Context, id int64) (*domain.Desk, error) {
	desk, err := scanDesk(cs.db.QueryRowContext(ctx, `SELECT id, name, description, department_id, created_at FROM desks WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, &domain.NotFoundError{Kind: "desk", ID: id}
	}
	return desk, err
}

func (cs *catalogStore) CreateDesk(ctx context.Context, desk *domain.Desk) error {
	res, err := cs.db.ExecContext(ctx, `INSERT INTO desks(name, description, department_id, created_at) VALUES(?,?,?,?)`, desk.Name, desk.Description, nullableInt64(desk.DepartmentID), formatTime(desk.CreatedAt))
	if err != nil {
		if isUniqueViolation(err) {
			return &domain.DuplicateError{Kind: "desk", Name: desk.Name}
		}
		if isForeignKeyViolation(err) && desk.DepartmentID != nil {
			return &domain.NotFoundError{Kind: "department", ID: *desk.DepartmentID}
		}
		return fmt.Errorf("sqlite: create catalog desk: %w", err)
	}
	desk.ID, err = res.LastInsertId()
	return err
}

func (cs *catalogStore) UpdateDesk(ctx context.Context, desk *domain.Desk) error {
	res, err := cs.db.ExecContext(ctx, `UPDATE desks SET name=?, description=?, department_id=? WHERE id=?`, desk.Name, desk.Description, nullableInt64(desk.DepartmentID), desk.ID)
	if err != nil {
		if isUniqueViolation(err) {
			return &domain.DuplicateError{Kind: "desk", Name: desk.Name}
		}
		return fmt.Errorf("sqlite: update catalog desk: %w", err)
	}
	return rowsFound(res, "desk", desk.ID)
}

func (cs *catalogStore) DeleteDesk(ctx context.Context, id int64) error {
	res, err := cs.db.ExecContext(ctx, `DELETE FROM desks WHERE id=?`, id)
	if err != nil {
		if isForeignKeyViolation(err) {
			return &domain.ReferencedError{Kind: "desk", ID: id}
		}
		return fmt.Errorf("sqlite: delete catalog desk: %w", err)
	}
	return rowsFound(res, "desk", id)
}

func (cs *catalogStore) ListDeskMembers(ctx context.Context, deskID int64) ([]domain.User, error) {
	if _, err := cs.GetDeskByID(ctx, deskID); err != nil {
		return nil, err
	}
	rows, err := cs.db.QueryContext(ctx, `SELECT u.id, u.name, u.email, u.password_hash, u.role, u.active, u.created_at
		FROM desk_members dm JOIN users u ON u.id=dm.user_id
		WHERE dm.desk_id=? ORDER BY dm.created_at ASC, u.id ASC`, deskID)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list catalog desk members: %w", err)
	}
	defer rows.Close()
	var users []domain.User
	for rows.Next() {
		u, err := scanUserFrom(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, *u)
	}
	return users, rows.Err()
}

func (cs *catalogStore) ListEligibleDeskMembers(ctx context.Context) ([]domain.User, error) {
	rows, err := cs.db.QueryContext(ctx, `SELECT id, name, email, password_hash, role, active, created_at FROM users WHERE active=1 AND role IN ('agent','admin','root') ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list eligible catalog desk members: %w", err)
	}
	defer rows.Close()
	var users []domain.User
	for rows.Next() {
		u, err := scanUserFrom(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, *u)
	}
	return users, rows.Err()
}

func (cs *catalogStore) AddDeskMember(ctx context.Context, deskID, userID int64, createdAt time.Time) error {
	_, err := cs.db.ExecContext(ctx, `INSERT INTO desk_members(desk_id,user_id,created_at) VALUES(?,?,?)`, deskID, userID, formatTime(createdAt))
	if err != nil {
		if isUniqueViolation(err) {
			return &domain.DuplicateError{Kind: "desk member", Name: fmt.Sprintf("%d:%d", deskID, userID)}
		}
		return fmt.Errorf("sqlite: add catalog desk member: %w", err)
	}
	return nil
}

func (cs *catalogStore) RemoveDeskMember(ctx context.Context, deskID, userID int64) error {
	res, err := cs.db.ExecContext(ctx, `DELETE FROM desk_members WHERE desk_id=? AND user_id=?`, deskID, userID)
	if err != nil {
		return fmt.Errorf("sqlite: remove catalog desk member: %w", err)
	}
	return rowsFound(res, "desk member", userID)
}
