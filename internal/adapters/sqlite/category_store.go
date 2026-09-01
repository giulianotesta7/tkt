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

// categoryStore implements application.CategoryStore (task 4.6): UNIQUE
// name → DuplicateError, delete blocked by ticket references →
// ReferencedError (tickets.category_id FK), GetByID/List.
type categoryStore struct {
	db *sql.DB
}

var _ application.CategoryStore = (*categoryStore)(nil)

func newCategoryStore(db *sql.DB) *categoryStore { return &categoryStore{db: db} }

// Create stores c, assigning c.ID. A duplicate name is a DuplicateError.
func (cs *categoryStore) Create(ctx context.Context, c *domain.Category) error {
	res, err := cs.db.ExecContext(ctx, `INSERT INTO categories (name, description, area_id, created_at)
		VALUES (?, ?, COALESCE(NULLIF(?, 0), (SELECT id FROM areas WHERE name='General' ORDER BY id LIMIT 1)), ?)`,
		c.Name, c.Description, c.AreaID, formatTime(c.CreatedAt))
	if err != nil {
		if isUniqueViolation(err) {
			return &domain.DuplicateError{Kind: "category", Name: c.Name}
		}
		return fmt.Errorf("sqlite: create category: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("sqlite: category id: %w", err)
	}
	c.ID = id
	if c.AreaID == 0 {
		_ = cs.db.QueryRowContext(ctx, `SELECT area_id FROM categories WHERE id=?`, c.ID).Scan(&c.AreaID)
	}
	return nil
}

// Update persists the category (rename). A rename onto an existing name is
// a DuplicateError.
func (cs *categoryStore) Update(ctx context.Context, c *domain.Category) error {
	res, err := cs.db.ExecContext(ctx, `UPDATE categories SET name = ?, description = ?, area_id = COALESCE(NULLIF(?, 0), area_id) WHERE id = ?`,
		c.Name, c.Description, c.AreaID, c.ID)
	if err != nil {
		if isUniqueViolation(err) {
			return &domain.DuplicateError{Kind: "category", Name: c.Name}
		}
		return fmt.Errorf("sqlite: update category: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("sqlite: update category rows: %w", err)
	}
	if n == 0 {
		return &domain.NotFoundError{Kind: "category", ID: c.ID}
	}
	return nil
}

// Delete removes an unreferenced category. A category used by tickets is a
// ReferencedError — the tickets FK fires and the delete is rejected
// (category-management spec).
func (cs *categoryStore) Delete(ctx context.Context, id int64) error {
	res, err := cs.db.ExecContext(ctx, `DELETE FROM categories WHERE id = ?`, id)
	if err != nil {
		if isForeignKeyViolation(err) {
			return &domain.ReferencedError{Kind: "category", ID: id}
		}
		return fmt.Errorf("sqlite: delete category: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("sqlite: delete category rows: %w", err)
	}
	if n == 0 {
		return &domain.NotFoundError{Kind: "category", ID: id}
	}
	return nil
}

// GetByID returns the category; ErrNotFound when absent.
func (cs *categoryStore) GetByID(ctx context.Context, id int64) (*domain.Category, error) {
	c, err := scanCategoryFrom(cs.db.QueryRowContext(ctx,
		`SELECT id, name, description, area_id, created_at FROM categories WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, &domain.NotFoundError{Kind: "category", ID: id}
	}
	if err != nil {
		return nil, err
	}
	return c, nil
}

// List returns all categories ordered by id.
func (cs *categoryStore) List(ctx context.Context) ([]domain.Category, error) {
	rows, err := cs.db.QueryContext(ctx, `SELECT id, name, description, area_id, created_at FROM categories ORDER BY id ASC`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list categories: %w", err)
	}
	defer rows.Close()
	var out []domain.Category
	for rows.Next() {
		c, err := scanCategoryFrom(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: scan category: %w", err)
		}
		out = append(out, *c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: list categories: %w", err)
	}
	return out, nil
}

// scanCategoryFrom projects one row into a domain.Category.
func scanCategoryFrom(scan rowScanner) (*domain.Category, error) {
	var (
		c         domain.Category
		createdAt string
		areaID    sql.NullInt64
	)
	if err := scan.Scan(&c.ID, &c.Name, &c.Description, &areaID, &createdAt); err != nil {
		return nil, err
	}
	if areaID.Valid {
		c.AreaID = areaID.Int64
	}
	var err error
	if c.CreatedAt, err = time.Parse(timeLayout, createdAt); err != nil {
		return nil, fmt.Errorf("sqlite: parse category created_at %q: %w", createdAt, err)
	}
	return &c, nil
}
