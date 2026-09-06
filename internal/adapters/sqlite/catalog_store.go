package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

type catalogStore struct{ db *sql.DB }

var _ application.CatalogStore = (*catalogStore)(nil)

func newCatalogStore(db *sql.DB) *catalogStore { return &catalogStore{db: db} }

func (cs *catalogStore) ListDepartments(ctx context.Context) ([]domain.CatalogDepartment, error) {
	rows, err := cs.db.QueryContext(ctx, `SELECT d.id, d.name, d.description, d.created_at,
		COUNT(DISTINCT ds.id), COUNT(DISTINCT c.id)
		FROM departments d
		LEFT JOIN desks ds ON ds.department_id=d.id
		LEFT JOIN categories c ON c.desk_id=ds.id
		GROUP BY d.id ORDER BY d.id`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list departments: %w", err)
	}
	defer rows.Close()
	var out []domain.CatalogDepartment
	for rows.Next() {
		var d domain.CatalogDepartment
		var created string
		if err := rows.Scan(&d.ID, &d.Name, &d.Description, &created, &d.DeskCount, &d.CategoryCount); err != nil {
			return nil, err
		}
		var parseErr error
		d.CreatedAt, parseErr = parseCatalogTime(created)
		if parseErr != nil {
			return nil, parseErr
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (cs *catalogStore) ListDesks(ctx context.Context, departmentID int64) ([]domain.CatalogDesk, error) {
	where := "ds.department_id=?"
	args := []any{departmentID}
	if departmentID == 0 {
		where = "ds.department_id IS NULL"
		args = nil
	}
	rows, err := cs.db.QueryContext(ctx, `SELECT ds.id, ds.name, ds.description, ds.department_id, ds.created_at,
		COUNT(c.id), COALESCE(d.name, 'Unassigned')
		FROM desks ds LEFT JOIN departments d ON d.id=ds.department_id
		LEFT JOIN categories c ON c.desk_id=ds.id
		WHERE `+where+` GROUP BY ds.id ORDER BY ds.id`, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list desks: %w", err)
	}
	defer rows.Close()
	return scanCatalogDesks(rows)
}

func scanCatalogDesks(rows *sql.Rows) ([]domain.CatalogDesk, error) {
	var out []domain.CatalogDesk
	for rows.Next() {
		var d domain.CatalogDesk
		var departmentID sql.NullInt64
		var created string
		if err := rows.Scan(&d.ID, &d.Name, &d.Description, &departmentID, &created, &d.CategoryCount, &d.DepartmentName); err != nil {
			return nil, err
		}
		if departmentID.Valid {
			d.Desk.DepartmentID = &departmentID.Int64
			d.DepartmentID = departmentID.Int64
		}
		var parseErr error
		d.CreatedAt, parseErr = parseCatalogTime(created)
		if parseErr != nil {
			return nil, parseErr
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (cs *catalogStore) ListCatalogCategories(ctx context.Context, deskID int64) ([]domain.CatalogCategory, error) {
	rows, err := cs.db.QueryContext(ctx, `SELECT c.id, c.name, c.description, c.desk_id, c.created_at,
		 ds.name, d.id, COALESCE(d.name, 'Unassigned')
		FROM categories c JOIN desks ds ON ds.id=c.desk_id
		LEFT JOIN departments d ON d.id=ds.department_id
		WHERE c.desk_id=? ORDER BY c.id`, deskID)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list catalog categories: %w", err)
	}
	defer rows.Close()
	return scanCatalogCategories(rows)
}

func (cs *catalogStore) SearchCatalog(ctx context.Context, query string) ([]domain.CatalogCategory, error) {
	pattern := "%" + strings.ToLower(strings.TrimSpace(query)) + "%"
	rows, err := cs.db.QueryContext(ctx, `SELECT c.id, c.name, c.description, c.desk_id, c.created_at,
		ds.name, d.id, COALESCE(d.name, 'Unassigned')
		FROM categories c JOIN desks ds ON ds.id=c.desk_id
		LEFT JOIN departments d ON d.id=ds.department_id
		WHERE lower(c.name) LIKE ? OR lower(c.description) LIKE ? OR lower(ds.name) LIKE ? OR lower(COALESCE(d.name, 'Unassigned')) LIKE ?
		ORDER BY COALESCE(d.name, 'Unassigned'), ds.name, c.name, c.id`, pattern, pattern, pattern, pattern)
	if err != nil {
		return nil, fmt.Errorf("sqlite: search catalog: %w", err)
	}
	defer rows.Close()
	return scanCatalogCategories(rows)
}

func scanCatalogCategories(rows *sql.Rows) ([]domain.CatalogCategory, error) {
	var out []domain.CatalogCategory
	for rows.Next() {
		var c domain.CatalogCategory
		var created string
		var departmentID sql.NullInt64
		if err := rows.Scan(&c.ID, &c.Name, &c.Description, &c.DeskID, &created, &c.DeskName, &departmentID, &c.DepartmentName); err != nil {
			return nil, err
		}
		if departmentID.Valid {
			c.DepartmentID = departmentID.Int64
		}
		var parseErr error
		c.CreatedAt, parseErr = parseCatalogTime(created)
		if parseErr != nil {
			return nil, parseErr
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func parseCatalogTime(value string) (time.Time, error) { return time.Parse(timeLayout, value) }

func (cs *catalogStore) CreateDepartment(ctx context.Context, d *domain.Department) error {
	res, err := cs.db.ExecContext(ctx, `INSERT INTO departments(name, description, created_at) VALUES(?,?,?)`, d.Name, d.Description, formatTime(d.CreatedAt))
	if err != nil {
		if isUniqueViolation(err) {
			return &domain.DuplicateError{Kind: "department", Name: d.Name}
		}
		return err
	}
	d.ID, err = res.LastInsertId()
	return err
}

func (cs *catalogStore) UpdateDepartment(ctx context.Context, d *domain.Department) error {
	res, err := cs.db.ExecContext(ctx, `UPDATE departments SET name=?, description=? WHERE id=?`, d.Name, d.Description, d.ID)
	if err != nil {
		if isUniqueViolation(err) {
			return &domain.DuplicateError{Kind: "department", Name: d.Name}
		}
		return err
	}
	return rowsFound(res, "department", d.ID)
}

func (cs *catalogStore) DeleteDepartment(ctx context.Context, id int64) error {
	res, err := cs.db.ExecContext(ctx, `DELETE FROM departments WHERE id=?`, id)
	if err != nil {
		if isForeignKeyViolation(err) {
			return &domain.ReferencedError{Kind: "department", ID: id}
		}
		return err
	}
	return rowsFound(res, "department", id)
}

func (cs *catalogStore) MoveCategory(ctx context.Context, categoryID, deskID int64) error {
	res, err := cs.db.ExecContext(ctx, `UPDATE categories SET desk_id=? WHERE id=?`, deskID, categoryID)
	if err != nil {
		if isForeignKeyViolation(err) {
			return &domain.NotFoundError{Kind: "desk", ID: deskID}
		}
		return err
	}
	return rowsFound(res, "category", categoryID)
}
