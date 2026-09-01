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
	rows, err := cs.db.QueryContext(ctx, `SELECT d.id, d.name, d.description, d.created_at, COUNT(c.id)
		FROM departments d LEFT JOIN areas a ON a.department_id=d.id LEFT JOIN categories c ON c.area_id=a.id
		GROUP BY d.id ORDER BY d.id`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list departments: %w", err)
	}
	defer rows.Close()
	var out []domain.CatalogDepartment
	for rows.Next() {
		var d domain.CatalogDepartment
		var created string
		if err := rows.Scan(&d.ID, &d.Name, &d.Description, &created, &d.CategoryCount); err != nil {
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

func (cs *catalogStore) ListAreas(ctx context.Context, departmentID int64) ([]domain.CatalogArea, error) {
	rows, err := cs.db.QueryContext(ctx, `SELECT a.id, a.department_id, a.name, a.description, a.created_at, COUNT(c.id)
		FROM areas a LEFT JOIN categories c ON c.area_id=a.id WHERE a.department_id=?
		GROUP BY a.id ORDER BY a.id`, departmentID)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list areas: %w", err)
	}
	defer rows.Close()
	var out []domain.CatalogArea
	for rows.Next() {
		var a domain.CatalogArea
		var created string
		if err := rows.Scan(&a.ID, &a.DepartmentID, &a.Name, &a.Description, &created, &a.CategoryCount); err != nil {
			return nil, err
		}
		var parseErr error
		a.CreatedAt, parseErr = parseCatalogTime(created)
		if parseErr != nil {
			return nil, parseErr
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (cs *catalogStore) ListCatalogCategories(ctx context.Context, areaID int64) ([]domain.CatalogCategory, error) {
	rows, err := cs.db.QueryContext(ctx, `SELECT c.id, c.name, c.description, c.area_id, c.created_at, a.name, d.id, d.name
		FROM categories c JOIN areas a ON a.id=c.area_id JOIN departments d ON d.id=a.department_id
		WHERE c.area_id=? ORDER BY c.id`, areaID)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list catalog categories: %w", err)
	}
	defer rows.Close()
	var out []domain.CatalogCategory
	for rows.Next() {
		var c domain.CatalogCategory
		var created string
		if err := rows.Scan(&c.ID, &c.Name, &c.Description, &c.AreaID, &created, &c.AreaName, &c.DepartmentID, &c.DepartmentName); err != nil {
			return nil, err
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

func (cs *catalogStore) SearchCatalog(ctx context.Context, query string) ([]domain.CatalogCategory, error) {
	pattern := "%" + strings.ToLower(strings.TrimSpace(query)) + "%"
	rows, err := cs.db.QueryContext(ctx, `SELECT c.id, c.name, c.description, c.area_id, c.created_at, a.name, d.id, d.name
		FROM categories c JOIN areas a ON a.id=c.area_id JOIN departments d ON d.id=a.department_id
		WHERE lower(c.name) LIKE ? OR lower(c.description) LIKE ? OR lower(a.name) LIKE ? OR lower(d.name) LIKE ?
		ORDER BY d.name, a.name, c.name, c.id`, pattern, pattern, pattern, pattern)
	if err != nil {
		return nil, fmt.Errorf("sqlite: search catalog: %w", err)
	}
	defer rows.Close()
	var out []domain.CatalogCategory
	for rows.Next() {
		var c domain.CatalogCategory
		var created string
		if err := rows.Scan(&c.ID, &c.Name, &c.Description, &c.AreaID, &created, &c.AreaName, &c.DepartmentID, &c.DepartmentName); err != nil {
			return nil, err
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
	if n, _ := res.RowsAffected(); n == 0 {
		return &domain.NotFoundError{Kind: "department", ID: d.ID}
	}
	return nil
}
func (cs *catalogStore) DeleteDepartment(ctx context.Context, id int64) error {
	res, err := cs.db.ExecContext(ctx, `DELETE FROM departments WHERE id=?`, id)
	if err != nil {
		if isForeignKeyViolation(err) {
			return &domain.ReferencedError{Kind: "department", ID: id}
		}
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return &domain.NotFoundError{Kind: "department", ID: id}
	}
	return nil
}
func (cs *catalogStore) CreateArea(ctx context.Context, a *domain.Area) error {
	res, err := cs.db.ExecContext(ctx, `INSERT INTO areas(department_id,name,description,created_at) VALUES(?,?,?,?)`, a.DepartmentID, a.Name, a.Description, formatTime(a.CreatedAt))
	if err != nil {
		if isUniqueViolation(err) {
			return &domain.DuplicateError{Kind: "area", Name: a.Name}
		}
		if isForeignKeyViolation(err) {
			return &domain.NotFoundError{Kind: "department", ID: a.DepartmentID}
		}
		return err
	}
	a.ID, err = res.LastInsertId()
	return err
}
func (cs *catalogStore) UpdateArea(ctx context.Context, a *domain.Area) error {
	res, err := cs.db.ExecContext(ctx, `UPDATE areas SET department_id=?,name=?,description=? WHERE id=?`, a.DepartmentID, a.Name, a.Description, a.ID)
	if err != nil {
		if isUniqueViolation(err) {
			return &domain.DuplicateError{Kind: "area", Name: a.Name}
		}
		if isForeignKeyViolation(err) {
			return &domain.NotFoundError{Kind: "department", ID: a.DepartmentID}
		}
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return &domain.NotFoundError{Kind: "area", ID: a.ID}
	}
	return nil
}
func (cs *catalogStore) DeleteArea(ctx context.Context, id int64) error {
	res, err := cs.db.ExecContext(ctx, `DELETE FROM areas WHERE id=?`, id)
	if err != nil {
		if isForeignKeyViolation(err) {
			return &domain.ReferencedError{Kind: "area", ID: id}
		}
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return &domain.NotFoundError{Kind: "area", ID: id}
	}
	return nil
}
func (cs *catalogStore) MoveCategory(ctx context.Context, categoryID, areaID int64) error {
	res, err := cs.db.ExecContext(ctx, `UPDATE categories SET area_id=? WHERE id=?`, areaID, categoryID)
	if err != nil {
		if isForeignKeyViolation(err) {
			return &domain.NotFoundError{Kind: "area", ID: areaID}
		}
		if isUniqueViolation(err) {
			return &domain.DuplicateError{Kind: "category", Name: "category"}
		}
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return &domain.NotFoundError{Kind: "category", ID: categoryID}
	}
	return nil
}
