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

type deskStore struct{ db *sql.DB }

var _ application.DeskStore = (*deskStore)(nil)

func newDeskStore(db *sql.DB) *deskStore { return &deskStore{db: db} }

func (s *deskStore) Create(ctx context.Context, g *domain.Desk) error {
	res, err := s.db.ExecContext(ctx, `INSERT INTO desks (name, created_at) VALUES (?, ?)`, g.Name, formatTime(g.CreatedAt))
	if err != nil {
		if isUniqueViolation(err) {
			return &domain.DuplicateError{Kind: "desk", Name: g.Name}
		}
		return fmt.Errorf("sqlite: create desk: %w", err)
	}
	g.ID, err = res.LastInsertId()
	if err != nil {
		return fmt.Errorf("sqlite: desk id: %w", err)
	}
	return nil
}

func (s *deskStore) Update(ctx context.Context, g *domain.Desk) error {
	res, err := s.db.ExecContext(ctx, `UPDATE desks SET name = ? WHERE id = ?`, g.Name, g.ID)
	if err != nil {
		if isUniqueViolation(err) {
			return &domain.DuplicateError{Kind: "desk", Name: g.Name}
		}
		return fmt.Errorf("sqlite: update desk: %w", err)
	}
	return rowsFound(res, "desk", g.ID)
}

func (s *deskStore) Delete(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM desks WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("sqlite: delete desk: %w", err)
	}
	return rowsFound(res, "desk", id)
}

func (s *deskStore) GetByID(ctx context.Context, id int64) (*domain.Desk, error) {
	g, err := scanDesk(s.db.QueryRowContext(ctx, `SELECT id, name, created_at FROM desks WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, &domain.NotFoundError{Kind: "desk", ID: id}
	}
	return g, err
}

func (s *deskStore) List(ctx context.Context) ([]domain.Desk, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, created_at FROM desks ORDER BY id ASC`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list desks: %w", err)
	}
	defer rows.Close()
	var desks []domain.Desk
	for rows.Next() {
		g, err := scanDesk(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: scan desk: %w", err)
		}
		desks = append(desks, *g)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: list desks: %w", err)
	}
	return desks, nil
}

func (s *deskStore) AddMember(ctx context.Context, deskID, userID int64, createdAt time.Time) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO desk_members (desk_id, user_id, created_at) VALUES (?, ?, ?)`, deskID, userID, formatTime(createdAt))
	if err != nil {
		if isUniqueViolation(err) {
			return &domain.DuplicateError{Kind: "desk member", Name: fmt.Sprintf("%d:%d", deskID, userID)}
		}
		return fmt.Errorf("sqlite: add desk member: %w", err)
	}
	return nil
}

func (s *deskStore) RemoveMember(ctx context.Context, deskID, userID int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM desk_members WHERE desk_id = ? AND user_id = ?`, deskID, userID)
	if err != nil {
		return fmt.Errorf("sqlite: remove desk member: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("sqlite: remove desk member rows: %w", err)
	}
	if n == 0 {
		return &domain.NotFoundError{Kind: "desk member", ID: userID}
	}
	return nil
}

func (s *deskStore) ListMembers(ctx context.Context, deskID int64) ([]domain.User, error) {
	if _, err := s.GetByID(ctx, deskID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT u.id, u.name, u.email, u.password_hash, u.role, u.active, u.created_at
		FROM desk_members gm JOIN users u ON u.id = gm.user_id WHERE gm.desk_id = ? ORDER BY gm.created_at ASC, u.id ASC`, deskID)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list desk members: %w", err)
	}
	defer rows.Close()
	var users []domain.User
	for rows.Next() {
		u, err := scanUserFrom(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: scan desk member: %w", err)
		}
		users = append(users, *u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: list desk members: %w", err)
	}
	return users, nil
}

func scanDesk(scan rowScanner) (*domain.Desk, error) {
	var g domain.Desk
	var createdAt string
	if err := scan.Scan(&g.ID, &g.Name, &createdAt); err != nil {
		return nil, err
	}
	var err error
	g.CreatedAt, err = time.Parse(timeLayout, createdAt)
	if err != nil {
		return nil, fmt.Errorf("sqlite: parse desk created_at %q: %w", createdAt, err)
	}
	return &g, nil
}

func rowsFound(res sql.Result, kind string, id int64) error {
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("sqlite: %s rows: %w", kind, err)
	}
	if n == 0 {
		return &domain.NotFoundError{Kind: kind, ID: id}
	}
	return nil
}
