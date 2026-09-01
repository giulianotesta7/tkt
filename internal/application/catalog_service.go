package application

import (
	"context"
	"strings"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// CatalogService owns the fixed-depth hierarchy while CategoryService keeps
// the existing category and workflow APIs compatible.
type CatalogService struct {
	catalog    CatalogStore
	categories CategoryStore
	clock      domain.Clock
}

func NewCatalogService(catalog CatalogStore, categories CategoryStore, clock domain.Clock) *CatalogService {
	return &CatalogService{catalog: catalog, categories: categories, clock: clock}
}

func (s *CatalogService) ListDepartments(ctx context.Context) ([]domain.CatalogDepartment, error) {
	return s.catalog.ListDepartments(ctx)
}

func (s *CatalogService) ListAreas(ctx context.Context, departmentID int64) ([]domain.CatalogArea, error) {
	return s.catalog.ListAreas(ctx, departmentID)
}

func (s *CatalogService) ListCategories(ctx context.Context, areaID int64) ([]domain.CatalogCategory, error) {
	return s.catalog.ListCatalogCategories(ctx, areaID)
}

func (s *CatalogService) Search(ctx context.Context, query string) ([]domain.CatalogCategory, error) {
	return s.catalog.SearchCatalog(ctx, strings.TrimSpace(query))
}

func (s *CatalogService) FindCategory(ctx context.Context, id int64) (*domain.CatalogCategory, error) {
	c, err := s.categories.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	matches, err := s.catalog.SearchCatalog(ctx, c.Name)
	if err != nil {
		return nil, err
	}
	for _, match := range matches {
		if match.ID == id {
			return &match, nil
		}
	}
	return nil, &domain.NotFoundError{Kind: "category", ID: id}
}

func (s *CatalogService) CreateDepartmentFor(ctx context.Context, actor domain.User, name, description string) (*domain.Department, error) {
	if !canManageCatalog(actor) {
		return nil, domain.NewForbiddenError("category management is not permitted")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, &domain.ValidationError{Field: "name", Message: domain.ErrMsgCategoryNameRequired}
	}
	d := &domain.Department{Name: name, Description: strings.TrimSpace(description), CreatedAt: s.clock.Now()}
	if err := s.catalog.CreateDepartment(ctx, d); err != nil {
		return nil, err
	}
	return d, nil
}

func (s *CatalogService) UpdateDepartmentFor(ctx context.Context, actor domain.User, id int64, name, description string) (*domain.Department, error) {
	if !canManageCatalog(actor) {
		return nil, domain.NewForbiddenError("category management is not permitted")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, &domain.ValidationError{Field: "name", Message: domain.ErrMsgCategoryNameRequired}
	}
	d := &domain.Department{ID: id, Name: name, Description: strings.TrimSpace(description)}
	if err := s.catalog.UpdateDepartment(ctx, d); err != nil {
		return nil, err
	}
	return d, nil
}

func (s *CatalogService) DeleteDepartmentFor(ctx context.Context, actor domain.User, id int64) error {
	if !canManageCatalog(actor) {
		return domain.NewForbiddenError("category management is not permitted")
	}
	return s.catalog.DeleteDepartment(ctx, id)
}

func (s *CatalogService) CreateAreaFor(ctx context.Context, actor domain.User, departmentID int64, name, description string) (*domain.Area, error) {
	if !canManageCatalog(actor) {
		return nil, domain.NewForbiddenError("category management is not permitted")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, &domain.ValidationError{Field: "name", Message: domain.ErrMsgCategoryNameRequired}
	}
	a := &domain.Area{DepartmentID: departmentID, Name: name, Description: strings.TrimSpace(description), CreatedAt: s.clock.Now()}
	if err := s.catalog.CreateArea(ctx, a); err != nil {
		return nil, err
	}
	return a, nil
}

func (s *CatalogService) UpdateAreaFor(ctx context.Context, actor domain.User, id int64, departmentID int64, name, description string) (*domain.Area, error) {
	if !canManageCatalog(actor) {
		return nil, domain.NewForbiddenError("category management is not permitted")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, &domain.ValidationError{Field: "name", Message: domain.ErrMsgCategoryNameRequired}
	}
	a := &domain.Area{ID: id, DepartmentID: departmentID, Name: name, Description: strings.TrimSpace(description)}
	if err := s.catalog.UpdateArea(ctx, a); err != nil {
		return nil, err
	}
	return a, nil
}

func (s *CatalogService) DeleteAreaFor(ctx context.Context, actor domain.User, id int64) error {
	if !canManageCatalog(actor) {
		return domain.NewForbiddenError("category management is not permitted")
	}
	return s.catalog.DeleteArea(ctx, id)
}

func (s *CatalogService) MoveCategoryFor(ctx context.Context, actor domain.User, categoryID, areaID int64) error {
	if !canManageCatalog(actor) {
		return domain.NewForbiddenError("category management is not permitted")
	}
	return s.catalog.MoveCategory(ctx, categoryID, areaID)
}

func (s *CatalogService) UpdateCategoryFor(ctx context.Context, actor domain.User, c *domain.Category) error {
	if !canManageCatalog(actor) {
		return domain.NewForbiddenError("category management is not permitted")
	}
	c.Name = strings.TrimSpace(c.Name)
	c.Description = strings.TrimSpace(c.Description)
	if c.Name == "" {
		return &domain.ValidationError{Field: "name", Message: domain.ErrMsgCategoryNameRequired}
	}
	if c.Description == "" {
		return &domain.ValidationError{Field: "description", Message: "category description is required"}
	}
	return s.categories.Update(ctx, c)
}

func canManageCatalog(actor domain.User) bool {
	return NewPolicy().Capabilities(actor.Role).Require(CapManageCategories)
}
