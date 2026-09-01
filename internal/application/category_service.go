package application

import (
	"context"
	"strings"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// CategoryService implements the category management use cases
// (category-management spec): unique non-empty names, rename, and delete
// guarded by ticket references (the store enforces both).
type CategoryService struct {
	categories CategoryStore
	clock      domain.Clock
}

// NewCategoryService wires the category use cases against the given ports.
func NewCategoryService(categories CategoryStore, clock domain.Clock) *CategoryService {
	return &CategoryService{categories: categories, clock: clock}
}

// Create stores a legacy category with a unique non-empty name. Legacy callers
// are placed in the compatibility General area and use the name as description.
func (s *CategoryService) Create(ctx context.Context, name string) (*domain.Category, error) {
	return s.create(ctx, name, name, 0, true)
}

// CreateWithDescription stores a category in an explicit Area. New callers must
// provide both the owning Area and a non-empty description.
func (s *CategoryService) CreateWithDescription(ctx context.Context, name, description string, areaID int64) (*domain.Category, error) {
	return s.create(ctx, name, description, areaID, false)
}

func (s *CategoryService) create(ctx context.Context, name, description string, areaID int64, legacy bool) (*domain.Category, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, &domain.ValidationError{Field: "name", Message: domain.ErrMsgCategoryNameRequired}
	}
	description = strings.TrimSpace(description)
	if !legacy && areaID == 0 {
		return nil, &domain.ValidationError{Field: "area_id", Message: domain.ErrMsgCategoryAreaRequired}
	}
	if !legacy && description == "" {
		return nil, &domain.ValidationError{Field: "description", Message: domain.ErrMsgCategoryDescriptionRequired}
	}
	c := &domain.Category{Name: name, Description: description, AreaID: areaID, CreatedAt: s.clock.Now()}
	if err := s.categories.Create(ctx, c); err != nil {
		return nil, err
	}
	return c, nil
}

// CreateFor creates a legacy category only for an administrator or root actor.
func (s *CategoryService) CreateFor(ctx context.Context, actor domain.User, name string) (*domain.Category, error) {
	if !NewPolicy().Capabilities(actor.Role).Require(CapManageCategories) {
		return nil, domain.NewForbiddenError("category management is not permitted")
	}
	return s.Create(ctx, name)
}

func (s *CategoryService) CreateWithDescriptionFor(ctx context.Context, actor domain.User, name, description string, areaID int64) (*domain.Category, error) {
	if !NewPolicy().Capabilities(actor.Role).Require(CapManageCategories) {
		return nil, domain.NewForbiddenError("category management is not permitted")
	}
	return s.CreateWithDescription(ctx, name, description, areaID)
}

// Rename changes the category's name; the new name must be non-empty and
// unique (store returns a DuplicateError when taken).
func (s *CategoryService) Rename(ctx context.Context, id int64, name string) (*domain.Category, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, &domain.ValidationError{Field: "name", Message: domain.ErrMsgCategoryNameRequired}
	}
	c, err := s.categories.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	c.Name = name
	if err := s.categories.Update(ctx, c); err != nil {
		return nil, err
	}
	return c, nil
}

// RenameFor renames a category only for an administrator or root actor.
func (s *CategoryService) RenameFor(ctx context.Context, actor domain.User, id int64, name string) (*domain.Category, error) {
	if !NewPolicy().Capabilities(actor.Role).Require(CapManageCategories) {
		return nil, domain.NewForbiddenError("category management is not permitted")
	}
	return s.Rename(ctx, id, name)
}

// Delete removes an unreferenced category; a referenced category is rejected
// with a ReferencedError.
func (s *CategoryService) Delete(ctx context.Context, id int64) error {
	return s.categories.Delete(ctx, id)
}

// DeleteFor deletes a category only for an administrator or root actor.
func (s *CategoryService) DeleteFor(ctx context.Context, actor domain.User, id int64) error {
	if !NewPolicy().Capabilities(actor.Role).Require(CapManageCategories) {
		return domain.NewForbiddenError("category management is not permitted")
	}
	return s.Delete(ctx, id)
}

// GetByID returns the category or a NotFoundError.
func (s *CategoryService) GetByID(ctx context.Context, id int64) (*domain.Category, error) {
	return s.categories.GetByID(ctx, id)
}

// List returns all categories.
func (s *CategoryService) List(ctx context.Context) ([]domain.Category, error) {
	return s.categories.List(ctx)
}
