package application

import (
	"context"
	"strings"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// CategoryAvailability supplies the categories currently available for new
// tickets, which means categories with a current published workflow.
type CategoryAvailability interface {
	ListAvailableCategories(ctx context.Context) ([]domain.Category, error)
}

// CatalogService owns the fixed Department -> Desk -> Category hierarchy while
// CategoryService and DeskService retain their existing domain responsibilities.
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

func (s *CatalogService) ListDesks(ctx context.Context, departmentID int64) ([]domain.CatalogDesk, error) {
	return s.catalog.ListDesks(ctx, departmentID)
}

func (s *CatalogService) ListCategories(ctx context.Context, deskID int64) ([]domain.CatalogCategory, error) {
	return s.catalog.ListCatalogCategories(ctx, deskID)
}

func (s *CatalogService) Search(ctx context.Context, query string) ([]domain.CatalogCategory, error) {
	return s.catalog.SearchCatalog(ctx, strings.TrimSpace(query))
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

func (s *CatalogService) CreateDeskFor(ctx context.Context, actor domain.User, departmentID int64, name string, descriptions ...string) (*domain.Desk, error) {
	if !canManageCatalog(actor) {
		return nil, domain.NewForbiddenError("category management is not permitted")
	}
	if err := validateCatalogID("department_id", departmentID); err != nil {
		return nil, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, &domain.ValidationError{Field: "name", Message: "desk name is required"}
	}
	store, ok := s.catalog.(CatalogDeskStore)
	if !ok {
		return nil, domain.NewForbiddenError("desk management is not configured")
	}
	d := &domain.Desk{Name: name, Description: optionalDescription(descriptions), CreatedAt: s.clock.Now(), DepartmentID: &departmentID}
	if err := store.CreateDesk(ctx, d); err != nil {
		return nil, err
	}
	return d, nil
}

func (s *CatalogService) UpdateDeskFor(ctx context.Context, actor domain.User, id, departmentID int64, name string, descriptions ...string) error {
	if !canManageCatalog(actor) {
		return domain.NewForbiddenError("category management is not permitted")
	}
	if err := validateCatalogID("desk_id", id); err != nil {
		return err
	}
	if err := validateCatalogID("department_id", departmentID); err != nil {
		return err
	}
	store, ok := s.catalog.(CatalogDeskStore)
	if !ok {
		return domain.NewForbiddenError("desk management is not configured")
	}
	d, err := store.GetDeskByID(ctx, id)
	if err != nil {
		return err
	}
	d.Name = strings.TrimSpace(name)
	if len(descriptions) > 0 {
		d.Description = strings.TrimSpace(descriptions[0])
	}
	if d.Name == "" {
		return &domain.ValidationError{Field: "name", Message: "desk name is required"}
	}
	d.DepartmentID = &departmentID
	return store.UpdateDesk(ctx, d)
}

func (s *CatalogService) DeleteDeskFor(ctx context.Context, actor domain.User, id int64) error {
	if !canManageCatalog(actor) {
		return domain.NewForbiddenError("category management is not permitted")
	}
	store, ok := s.catalog.(CatalogDeskStore)
	if !ok {
		return domain.NewForbiddenError("desk management is not configured")
	}
	return store.DeleteDesk(ctx, id)
}

func (s *CatalogService) ListDeskMembersFor(ctx context.Context, actor domain.User, id int64) ([]domain.User, error) {
	if !canManageCatalog(actor) {
		return nil, domain.NewForbiddenError("category management is not permitted")
	}
	store, ok := s.catalog.(CatalogDeskStore)
	if !ok {
		return nil, domain.NewForbiddenError("desk management is not configured")
	}
	return store.ListDeskMembers(ctx, id)
}

func (s *CatalogService) ListEligibleDeskMembersFor(ctx context.Context, actor domain.User) ([]domain.User, error) {
	if !canManageCatalog(actor) {
		return nil, domain.NewForbiddenError("category management is not permitted")
	}
	store, ok := s.catalog.(CatalogDeskStore)
	if !ok {
		return nil, domain.NewForbiddenError("desk management is not configured")
	}
	return store.ListEligibleDeskMembers(ctx)
}

func (s *CatalogService) AddDeskMemberFor(ctx context.Context, actor domain.User, deskID, userID int64) error {
	if !canManageCatalog(actor) {
		return domain.NewForbiddenError("category management is not permitted")
	}
	store, ok := s.catalog.(CatalogDeskStore)
	if !ok {
		return domain.NewForbiddenError("desk management is not configured")
	}
	return store.AddDeskMember(ctx, deskID, userID, s.clock.Now())
}

func (s *CatalogService) RemoveDeskMemberFor(ctx context.Context, actor domain.User, deskID, userID int64) error {
	if !canManageCatalog(actor) {
		return domain.NewForbiddenError("category management is not permitted")
	}
	store, ok := s.catalog.(CatalogDeskStore)
	if !ok {
		return domain.NewForbiddenError("desk management is not configured")
	}
	return store.RemoveDeskMember(ctx, deskID, userID)
}

func (s *CatalogService) MoveCategoryFor(ctx context.Context, actor domain.User, categoryID, deskID int64) error {
	if !canManageCatalog(actor) {
		return domain.NewForbiddenError("category management is not permitted")
	}
	if err := validateCatalogID("category_id", categoryID); err != nil {
		return err
	}
	if err := validateCatalogID("desk_id", deskID); err != nil {
		return err
	}
	return s.catalog.MoveCategory(ctx, categoryID, deskID)
}

func (s *CatalogService) UpdateCategoryFor(ctx context.Context, actor domain.User, c *domain.Category) error {
	if !canManageCatalog(actor) {
		return domain.NewForbiddenError("category management is not permitted")
	}
	if c == nil {
		return &domain.ValidationError{Field: "category", Message: "category is required"}
	}
	if err := validateCatalogID("desk_id", c.DeskID); err != nil {
		return err
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

func optionalDescription(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return strings.TrimSpace(values[0])
}

func validateCatalogID(field string, id int64) error {
	if id <= 0 {
		return &domain.ValidationError{Field: field, Message: "identifier is required"}
	}
	return nil
}
