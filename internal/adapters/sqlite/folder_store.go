package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// folderStore persists the catalog folder tree (issue #484). Migration 0043
// owns the invariants — sibling-unique name keys, no cycles, no delete while a
// folder holds anything — so this adapter only translates the database's
// refusals into the domain's typed errors.
type folderStore struct{ db *sql.DB }

var _ application.FolderStore = (*folderStore)(nil)

func newFolderStore(db *sql.DB) *folderStore { return &folderStore{db: db} }

const folderColumns = `id, parent_id, name, description, created_at`

// Sibling names are unique through name_key (domain.FolderNameKey), which every
// write that sets a name also sets.

func (s *folderStore) ListFolders(ctx context.Context) ([]domain.Folder, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+folderColumns+` FROM folders ORDER BY name COLLATE NOCASE, id`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list folders: %w", err)
	}
	defer rows.Close()
	var out []domain.Folder
	for rows.Next() {
		f, err := scanFolder(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *f)
	}
	return out, rows.Err()
}

func (s *folderStore) GetFolder(ctx context.Context, id int64) (*domain.Folder, error) {
	f, err := scanFolder(s.db.QueryRowContext(ctx, `SELECT `+folderColumns+` FROM folders WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, &domain.NotFoundError{Kind: "folder", ID: id}
	}
	return f, err
}

func (s *folderStore) CreateFolder(ctx context.Context, f *domain.Folder) error {
	res, err := s.db.ExecContext(ctx, `INSERT INTO folders (parent_id, name, name_key, description, created_at) VALUES (?, ?, ?, ?, ?)`,
		nullableInt64(f.ParentID), f.Name, domain.FolderNameKey(f.Name), f.Description, formatTime(f.CreatedAt))
	if err != nil {
		return folderWriteError(err, f.Name, f.ParentID, "create")
	}
	f.ID, err = res.LastInsertId()
	if err != nil {
		return fmt.Errorf("sqlite: folder id: %w", err)
	}
	return nil
}

func (s *folderStore) UpdateFolder(ctx context.Context, f *domain.Folder) error {
	res, err := s.db.ExecContext(ctx, `UPDATE folders SET name = ?, name_key = ?, description = ? WHERE id = ?`, f.Name, domain.FolderNameKey(f.Name), f.Description, f.ID)
	if err != nil {
		return folderWriteError(err, f.Name, nil, "update")
	}
	return rowsFound(res, "folder", f.ID)
}

func (s *folderStore) MoveFolder(ctx context.Context, id int64, parentID *int64) error {
	res, err := s.db.ExecContext(ctx, `UPDATE folders SET parent_id = ? WHERE id = ?`, nullableInt64(parentID), id)
	if err != nil {
		if strings.Contains(err.Error(), "folder cycle") {
			parent := int64(0)
			if parentID != nil {
				parent = *parentID
			}
			return &domain.FolderCycleError{FolderID: id, ParentID: parent}
		}
		if isUniqueViolation(err) {
			name := ""
			if f, getErr := s.GetFolder(ctx, id); getErr == nil {
				name = f.Name
			}
			return &domain.DuplicateError{Kind: "folder", Name: name}
		}
		return folderWriteError(err, "", parentID, "move")
	}
	return rowsFound(res, "folder", id)
}

func (s *folderStore) DeleteFolder(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM folders WHERE id = ?`, id)
	if err != nil {
		if isForeignKeyViolation(err) {
			return &domain.ReferencedError{Kind: "folder", ID: id}
		}
		return fmt.Errorf("sqlite: delete folder: %w", err)
	}
	return rowsFound(res, "folder", id)
}

func (s *folderStore) PlaceCategory(ctx context.Context, categoryID int64, folderID *int64) error {
	res, err := s.db.ExecContext(ctx, `UPDATE categories SET folder_id = ? WHERE id = ?`, nullableInt64(folderID), categoryID)
	if err != nil {
		if isForeignKeyViolation(err) && folderID != nil {
			return &domain.NotFoundError{Kind: "folder", ID: *folderID}
		}
		return fmt.Errorf("sqlite: place category: %w", err)
	}
	return rowsFound(res, "category", categoryID)
}

func (s *folderStore) CategoryFolders(ctx context.Context) (map[int64]*int64, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, folder_id FROM categories ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: category folders: %w", err)
	}
	defer rows.Close()
	out := map[int64]*int64{}
	for rows.Next() {
		var id int64
		var folder sql.NullInt64
		if err := rows.Scan(&id, &folder); err != nil {
			return nil, err
		}
		if folder.Valid {
			v := folder.Int64
			out[id] = &v
		} else {
			out[id] = nil
		}
	}
	return out, rows.Err()
}

// folderWriteError maps an insert or update refusal: a sibling with the same
// name, or a parent that does not exist.
func folderWriteError(err error, name string, parentID *int64, op string) error {
	if isUniqueViolation(err) {
		return &domain.DuplicateError{Kind: "folder", Name: name}
	}
	if isForeignKeyViolation(err) && parentID != nil {
		return &domain.NotFoundError{Kind: "folder", ID: *parentID}
	}
	return fmt.Errorf("sqlite: %s folder: %w", op, err)
}

func scanFolder(row interface{ Scan(...any) error }) (*domain.Folder, error) {
	var f domain.Folder
	var parent sql.NullInt64
	var created string
	if err := row.Scan(&f.ID, &parent, &f.Name, &f.Description, &created); err != nil {
		return nil, err
	}
	if parent.Valid {
		v := parent.Int64
		f.ParentID = &v
	}
	var err error
	f.CreatedAt, err = parseCatalogTime(created)
	if err != nil {
		return nil, err
	}
	return &f, nil
}
