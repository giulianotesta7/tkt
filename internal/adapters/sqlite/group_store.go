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

type groupStore struct{ db *sql.DB }

var _ application.GroupStore = (*groupStore)(nil)

func newGroupStore(db *sql.DB) *groupStore { return &groupStore{db: db} }

func (s *groupStore) Create(ctx context.Context, g *domain.Group) error {
	res, err := s.db.ExecContext(ctx, `INSERT INTO groups (name, created_at) VALUES (?, ?)`, g.Name, formatTime(g.CreatedAt))
	if err != nil {
		if isUniqueViolation(err) {
			return &domain.DuplicateError{Kind: "group", Name: g.Name}
		}
		return fmt.Errorf("sqlite: create group: %w", err)
	}
	g.ID, err = res.LastInsertId()
	if err != nil {
		return fmt.Errorf("sqlite: group id: %w", err)
	}
	return nil
}

func (s *groupStore) Update(ctx context.Context, g *domain.Group) error {
	res, err := s.db.ExecContext(ctx, `UPDATE groups SET name = ? WHERE id = ?`, g.Name, g.ID)
	if err != nil {
		if isUniqueViolation(err) {
			return &domain.DuplicateError{Kind: "group", Name: g.Name}
		}
		return fmt.Errorf("sqlite: update group: %w", err)
	}
	return rowsFound(res, "group", g.ID)
}

func (s *groupStore) Delete(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM groups WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("sqlite: delete group: %w", err)
	}
	return rowsFound(res, "group", id)
}

func (s *groupStore) GetByID(ctx context.Context, id int64) (*domain.Group, error) {
	g, err := scanGroup(s.db.QueryRowContext(ctx, `SELECT id, name, created_at FROM groups WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, &domain.NotFoundError{Kind: "group", ID: id}
	}
	return g, err
}

func (s *groupStore) List(ctx context.Context) ([]domain.Group, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, created_at FROM groups ORDER BY id ASC`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list groups: %w", err)
	}
	defer rows.Close()
	var groups []domain.Group
	for rows.Next() {
		g, err := scanGroup(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: scan group: %w", err)
		}
		groups = append(groups, *g)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: list groups: %w", err)
	}
	return groups, nil
}

func (s *groupStore) AddMember(ctx context.Context, groupID, userID int64, createdAt time.Time) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO group_members (group_id, user_id, created_at) VALUES (?, ?, ?)`, groupID, userID, formatTime(createdAt))
	if err != nil {
		if isUniqueViolation(err) {
			return &domain.DuplicateError{Kind: "group member", Name: fmt.Sprintf("%d:%d", groupID, userID)}
		}
		return fmt.Errorf("sqlite: add group member: %w", err)
	}
	return nil
}

func (s *groupStore) RemoveMember(ctx context.Context, groupID, userID int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM group_members WHERE group_id = ? AND user_id = ?`, groupID, userID)
	if err != nil {
		return fmt.Errorf("sqlite: remove group member: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("sqlite: remove group member rows: %w", err)
	}
	if n == 0 {
		return &domain.NotFoundError{Kind: "group member", ID: userID}
	}
	return nil
}

func (s *groupStore) ListMembers(ctx context.Context, groupID int64) ([]domain.User, error) {
	if _, err := s.GetByID(ctx, groupID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT u.id, u.name, u.email, u.password_hash, u.role, u.active, u.created_at
		FROM group_members gm JOIN users u ON u.id = gm.user_id WHERE gm.group_id = ? ORDER BY gm.created_at ASC, u.id ASC`, groupID)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list group members: %w", err)
	}
	defer rows.Close()
	var users []domain.User
	for rows.Next() {
		u, err := scanUserFrom(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: scan group member: %w", err)
		}
		users = append(users, *u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: list group members: %w", err)
	}
	return users, nil
}

func scanGroup(scan rowScanner) (*domain.Group, error) {
	var g domain.Group
	var createdAt string
	if err := scan.Scan(&g.ID, &g.Name, &createdAt); err != nil {
		return nil, err
	}
	var err error
	g.CreatedAt, err = time.Parse(timeLayout, createdAt)
	if err != nil {
		return nil, fmt.Errorf("sqlite: parse group created_at %q: %w", createdAt, err)
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
