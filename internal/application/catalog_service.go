package application

import (
	"context"
	"errors"
	"strings"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// CategoryAvailability supplies the categories currently available for new
// tickets, which means categories with a current published workflow.
type CategoryAvailability interface {
	ListAvailableCategories(ctx context.Context) ([]domain.Category, error)
}

// CatalogService reads the catalog (issue #484): every category with its
// responsible desk and its place in the folder tree, the desk projection the
// Categories and Desks screens show, and the category edits the drawer makes.
type CatalogService struct {
	catalog    CatalogStore
	categories CategoryStore
	clock      domain.Clock
	// folders, when set, places every listed category in the folder tree and
	// lets search match the folder names on its path (issue #484).
	folders FolderStore
}

func NewCatalogService(catalog CatalogStore, categories CategoryStore, clock domain.Clock) *CatalogService {
	return &CatalogService{catalog: catalog, categories: categories, clock: clock}
}

// WithFolders makes the catalog report each category's folder and path, and
// makes Search match folder names as well (issue #484, S8/S11).
func (s *CatalogService) WithFolders(folders FolderStore) *CatalogService {
	s.folders = folders
	return s
}

// FolderTree reads the folder tree for the requester catalog; without a
// folder store it is empty and every category sits at the top level.
func (s *CatalogService) FolderTree(ctx context.Context) (domain.CatalogTree, error) {
	if s.folders == nil {
		return domain.NewCatalogTree(nil), nil
	}
	folders, err := s.folders.ListFolders(ctx)
	if err != nil {
		return domain.CatalogTree{}, err
	}
	return domain.NewCatalogTree(folders), nil
}

// placeInFolders sets FolderID and FolderPath on every category. Without a
// folder store the categories are returned untouched.
func (s *CatalogService) placeInFolders(ctx context.Context, categories []domain.CatalogCategory) ([]domain.CatalogCategory, error) {
	if s.folders == nil {
		return categories, nil
	}
	folders, err := s.folders.ListFolders(ctx)
	if err != nil {
		return nil, err
	}
	places, err := s.folders.CategoryFolders(ctx)
	if err != nil {
		return nil, err
	}
	tree := domain.NewCatalogTree(folders)
	for i := range categories {
		folder := places[categories[i].ID]
		categories[i].FolderID = folder
		categories[i].FolderPath = tree.PathNames(folder)
	}
	return categories, nil
}

// ListAllCategories lists every category with its desk and folder path. The
// create surfaces need the NAMES, not just the ids, to group a picker by folder
// path and to caption a catalog card (issue #391, issue #484).
// It reads the same projection the search uses with an empty query, so there is
// no second definition of "the catalog's categories".
func (s *CatalogService) ListAllCategories(ctx context.Context) ([]domain.CatalogCategory, error) {
	all, err := s.catalog.SearchCatalog(ctx, "")
	if err != nil {
		return nil, err
	}
	return s.placeInFolders(ctx, all)
}

// ListTreeDesks is the desk projection: every desk with its category, agent
// and open-ticket counts in one read. A store that
// cannot answer it reports that plainly instead of pretending the installation
// has no desks.
func (s *CatalogService) ListTreeDesks(ctx context.Context) ([]domain.CatalogDesk, error) {
	store, ok := s.catalog.(CatalogTreeStore)
	if !ok {
		return nil, errors.New("catalog tree projection is not configured")
	}
	return store.ListTreeDesks(ctx)
}

// ListDefaultSLACategories reports which categories still run on the instance
// SLA defaults, the single fact the tree shows as Default or Custom SLA.
func (s *CatalogService) ListDefaultSLACategories(ctx context.Context) ([]int64, error) {
	store, ok := s.catalog.(CatalogTreeStore)
	if !ok {
		return nil, errors.New("catalog tree projection is not configured")
	}
	return store.ListDefaultSLACategories(ctx)
}

// Search lists the categories whose name or description contains
// query, case-insensitively. With folders configured it also matches the names
// of the folders on a category's path, so the store is read unfiltered and the
// match happens here, on the same fields for every category (S11).
func (s *CatalogService) Search(ctx context.Context, query string) ([]domain.CatalogCategory, error) {
	query = strings.TrimSpace(query)
	if s.folders == nil {
		return s.catalog.SearchCatalog(ctx, query)
	}
	all, err := s.ListAllCategories(ctx)
	if err != nil {
		return nil, err
	}
	needle := strings.ToLower(query)
	var out []domain.CatalogCategory
	for _, c := range all {
		fields := append([]string{c.Name, c.Description}, c.FolderPath...)
		for _, field := range fields {
			if strings.Contains(strings.ToLower(field), needle) {
				out = append(out, c)
				break
			}
		}
	}
	return out, nil
}

func (s *CatalogService) FindCategory(ctx context.Context, id int64, availability CategoryAvailability) (*domain.CatalogCategory, error) {
	c, err := s.categories.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if availability == nil {
		return nil, &domain.NotFoundError{Kind: "category", ID: id}
	}
	available, err := availability.ListAvailableCategories(ctx)
	if err != nil {
		return nil, err
	}
	found := false
	for _, category := range available {
		if category.ID == id {
			found = true
			break
		}
	}
	if !found {
		return nil, &domain.NotFoundError{Kind: "category", ID: id}
	}
	matches, err := s.catalog.SearchCatalog(ctx, c.Name)
	if err != nil {
		return nil, err
	}
	for _, match := range matches {
		if match.ID == id {
			placed, err := s.placeInFolders(ctx, []domain.CatalogCategory{match})
			if err != nil {
				return nil, err
			}
			return &placed[0], nil
		}
	}
	return nil, &domain.NotFoundError{Kind: "category", ID: id}
}

func (s *CatalogService) UpdateCategoryFor(ctx context.Context, actor domain.User, c *domain.Category) error {
	if !canManageCatalog(actor) {
		return domain.NewForbiddenError("category management is not permitted")
	}
	if c == nil {
		return &domain.ValidationError{Field: "category", Message: "category is required"}
	}
	c.Name = strings.TrimSpace(c.Name)
	c.Description = strings.TrimSpace(c.Description)
	if c.Name == "" {
		return &domain.ValidationError{Field: "name", Message: domain.ErrMsgCategoryNameRequired, Key: "error.category_name_required"}
	}
	keywords, err := normalizeCategoryKeywords(c.Keywords)
	if err != nil {
		return err
	}
	c.Keywords = keywords
	return s.categories.Update(ctx, c)
}

func canManageCatalog(actor domain.User) bool {
	return NewPolicy().Capabilities(actor.Role).Require(CapManageCategories)
}
