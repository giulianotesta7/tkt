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
	res, err := s.db.ExecContext(ctx, `INSERT INTO desks (name, description, created_at) VALUES (?, ?, ?)`, g.Name, g.Description, formatTime(g.CreatedAt))
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
	res, err := s.db.ExecContext(ctx, `UPDATE desks SET name = ?, description = ? WHERE id = ?`, g.Name, g.Description, g.ID)
	if err != nil {
		if isUniqueViolation(err) {
			return &domain.DuplicateError{Kind: "desk", Name: g.Name}
		}
		return fmt.Errorf("sqlite: update desk: %w", err)
	}
	return rowsFound(res, "desk", g.ID)
}

func (s *deskStore) Delete(ctx context.Context, id int64) error {
	// Hold SQLite's writer reservation across the reference reads and delete.
	// Workflow/audit writers cannot insert a reference between the check and
	// deletion, even when they use another connection.
	tx, err := beginImmediate(ctx, s.db, "delete desk")
	if err != nil {
		return fmt.Errorf("sqlite: delete desk begin: %w", err)
	}
	defer tx.Rollback()
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM desks WHERE id = ?)`, id).Scan(&exists); err != nil {
		return fmt.Errorf("sqlite: delete desk existence: %w", err)
	}
	if !exists {
		return &domain.NotFoundError{Kind: "desk", ID: id}
	}
	var audited bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM audit_events WHERE desk_id = ?)`, id).Scan(&audited); err != nil {
		return fmt.Errorf("sqlite: delete desk audit references: %w", err)
	}
	if audited {
		return &domain.ReferencedError{Kind: "desk", ID: id}
	}
	referenced, err := workflowReferencesDesk(ctx, tx, id)
	if err != nil {
		return err
	}
	if referenced {
		return &domain.ReferencedError{Kind: "desk", ID: id}
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM desks WHERE id = ?`, id)
	if err != nil {
		// Every remaining reference to a desk cascades or is set to NULL since
		// categories stopped naming one (#491); a violation here would be a new
		// restricting reference, so it still reads as "in use", never as a fault.
		if isForeignKeyViolation(err) {
			return &domain.ReferencedError{Kind: "desk", ID: id}
		}
		return fmt.Errorf("sqlite: delete desk: %w", err)
	}
	if err := rowsFound(res, "desk", id); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlite: delete desk commit: %w", err)
	}
	return nil
}

// All immutable versions matter, not just the current pointer: older versions
// can still be pinned by tickets. Decode with the domain parser rather than
// matching JSON text (which could confuse IDs or instructional prose). Broken
// stored JSON fails closed, without deleting a potentially referenced desk.
func workflowReferencesDesk(ctx context.Context, tx *sql.Tx, id int64) (bool, error) {
	rows, err := tx.QueryContext(ctx, `SELECT draft_json FROM category_workflows WHERE draft_json IS NOT NULL
		UNION ALL SELECT steps_json FROM workflow_versions`)
	if err != nil {
		return false, fmt.Errorf("sqlite: delete desk workflow references: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return false, fmt.Errorf("sqlite: delete desk scan workflow: %w", err)
		}
		def, err := domain.ParseWorkflowDefinition(raw)
		if err != nil {
			return false, fmt.Errorf("sqlite: delete desk parse workflow: %w", err)
		}
		for _, step := range def {
			if step.Type == domain.StepAssignToDesk && step.AssignToDesk != nil && step.AssignToDesk.DeskID == id {
				return true, nil
			}
		}
	}
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("sqlite: delete desk workflow rows: %w", err)
	}
	return false, nil
}

func (s *deskStore) GetByID(ctx context.Context, id int64) (*domain.Desk, error) {
	g, err := scanDesk(s.db.QueryRowContext(ctx, `SELECT id, name, description, created_at FROM desks WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, &domain.NotFoundError{Kind: "desk", ID: id}
	}
	return g, err
}

func (s *deskStore) List(ctx context.Context) ([]domain.Desk, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, description, created_at FROM desks ORDER BY id ASC`)
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
	if err := scan.Scan(&g.ID, &g.Name, &g.Description, &createdAt); err != nil {
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
