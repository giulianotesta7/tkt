package application

import (
	"context"
	"strings"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// FolderService manages the catalog's folder tree (issue #484). Every change
// requires the category-management capability and is checked before the store
// is touched; names and descriptions are trimmed, and a blank name is refused.
// The tree's own rules — sibling-unique names, no cycles, no delete while a
// folder holds anything — are the store's, and its typed refusals reach the
// caller unchanged.
type FolderService struct {
	folders FolderStore
	clock   domain.Clock
}

func NewFolderService(folders FolderStore, clock domain.Clock) *FolderService {
	return &FolderService{folders: folders, clock: clock}
}

// Tree reads every folder into a CatalogTree. It carries names only, so the
// requester catalog and the administration screen read the same tree.
func (s *FolderService) Tree(ctx context.Context) (domain.CatalogTree, error) {
	folders, err := s.folders.ListFolders(ctx)
	if err != nil {
		return domain.CatalogTree{}, err
	}
	return domain.NewCatalogTree(folders), nil
}

// CategoryFolders maps every category id to its folder id (nil = top level).
func (s *FolderService) CategoryFolders(ctx context.Context) (map[int64]*int64, error) {
	return s.folders.CategoryFolders(ctx)
}

func (s *FolderService) CreateFor(ctx context.Context, actor domain.User, parentID *int64, name, description string) (*domain.Folder, error) {
	if !canManageCatalog(actor) {
		return nil, domain.NewForbiddenError("category management is not permitted")
	}
	name, err := folderName(name)
	if err != nil {
		return nil, err
	}
	f := &domain.Folder{ParentID: parentID, Name: name, Description: strings.TrimSpace(description), CreatedAt: s.clock.Now()}
	if err := s.folders.CreateFolder(ctx, f); err != nil {
		return nil, err
	}
	return f, nil
}

// UpdateFor renames a folder and rewrites its description; it never moves it.
func (s *FolderService) UpdateFor(ctx context.Context, actor domain.User, id int64, name, description string) (*domain.Folder, error) {
	if !canManageCatalog(actor) {
		return nil, domain.NewForbiddenError("category management is not permitted")
	}
	name, err := folderName(name)
	if err != nil {
		return nil, err
	}
	f := &domain.Folder{ID: id, Name: name, Description: strings.TrimSpace(description)}
	if err := s.folders.UpdateFolder(ctx, f); err != nil {
		return nil, err
	}
	return s.folders.GetFolder(ctx, id)
}

// MoveFor puts a folder under parentID, or at the top level with nil. Moving
// never touches the categories inside, their workflows or their tickets.
func (s *FolderService) MoveFor(ctx context.Context, actor domain.User, id int64, parentID *int64) error {
	if !canManageCatalog(actor) {
		return domain.NewForbiddenError("category management is not permitted")
	}
	return s.folders.MoveFolder(ctx, id, parentID)
}

// DeleteFor removes an empty folder; a folder that still holds a folder or a
// category is refused by the store with a ReferencedError.
func (s *FolderService) DeleteFor(ctx context.Context, actor domain.User, id int64) error {
	if !canManageCatalog(actor) {
		return domain.NewForbiddenError("category management is not permitted")
	}
	return s.folders.DeleteFolder(ctx, id)
}

// PlaceCategoryFor puts a category in a folder, or at the top level with nil.
func (s *FolderService) PlaceCategoryFor(ctx context.Context, actor domain.User, categoryID int64, folderID *int64) error {
	if !canManageCatalog(actor) {
		return domain.NewForbiddenError("category management is not permitted")
	}
	return s.folders.PlaceCategory(ctx, categoryID, folderID)
}

func folderName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", &domain.ValidationError{Field: "name", Message: domain.ErrMsgFolderNameRequired, Key: "error.folder_name_required"}
	}
	return name, nil
}
