package application

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// MaxCategoryKeywordsRunes bounds the administrator's suggestion keywords
// (issue #416, S4). The field is a short list of the words a requester might
// type, not a second description, and the drawer is a single-line input, so the
// bound keeps one category from drowning the index and the form from accepting
// a paste of unrelated text.
const MaxCategoryKeywordsRunes = 500

// ErrMsgCategoryKeywordsInvalid is the English wording of the one refusal this
// layer owns for the keywords field: it must be a single line within the bound.
// The catalogue key carries the copy a reader sees.
const ErrMsgCategoryKeywordsInvalid = "keywords must be a single line of 500 characters or fewer"

// normalizeCategoryKeywords trims the keywords and enforces the bound above.
// They are stored as the administrator typed them — commas included — because
// the field is a search aid, not a taxonomy: splitting, ranking and folding are
// the local suggestion engine's job, and rewriting the text would hide from the
// administrator what the index actually holds.
func normalizeCategoryKeywords(keywords string) (string, error) {
	trimmed := strings.TrimSpace(keywords)
	if strings.ContainsAny(trimmed, "\n\r") || utf8.RuneCountInString(trimmed) > MaxCategoryKeywordsRunes {
		return "", &domain.ValidationError{
			Field:   "keywords",
			Message: ErrMsgCategoryKeywordsInvalid,
			Key:     "error.category_keywords_invalid",
		}
	}
	return trimmed, nil
}

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

// Create stores a category with a unique non-empty name.
func (s *CategoryService) Create(ctx context.Context, name string) (*domain.Category, error) {
	return s.CreateWithDescription(ctx, name, "", 0)
}

func (s *CategoryService) CreateWithDescription(ctx context.Context, name, description string, deskID int64) (*domain.Category, error) {
	return s.CreateWithKeywords(ctx, name, description, "", deskID)
}

// CreateWithKeywords is CreateWithDescription plus the administrator's
// suggestion keywords (issue #416, S4). The description-only form above stays
// because it is the contract every existing caller and fixture arranged with,
// and an empty keywords field is exactly what it asks for.
func (s *CategoryService) CreateWithKeywords(ctx context.Context, name, description, keywords string, deskID int64) (*domain.Category, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, &domain.ValidationError{Field: "name", Message: domain.ErrMsgCategoryNameRequired, Key: "error.category_name_required"}
	}
	keywords, err := normalizeCategoryKeywords(keywords)
	if err != nil {
		return nil, err
	}
	c := &domain.Category{Name: name, Description: strings.TrimSpace(description), Keywords: keywords, DeskID: deskID, CreatedAt: s.clock.Now()}
	if err := s.categories.Create(ctx, c); err != nil {
		return nil, err
	}
	return c, nil
}

// CreateFor creates a category only for an administrator or root actor.
func (s *CategoryService) CreateFor(ctx context.Context, actor domain.User, name string) (*domain.Category, error) {
	return s.CreateWithDescriptionFor(ctx, actor, name, "", 0)
}

func (s *CategoryService) CreateWithDescriptionFor(ctx context.Context, actor domain.User, name, description string, deskID int64) (*domain.Category, error) {
	return s.CreateWithKeywordsFor(ctx, actor, name, description, "", deskID)
}

// CreateWithKeywordsFor is the authorized form of CreateWithKeywords.
func (s *CategoryService) CreateWithKeywordsFor(ctx context.Context, actor domain.User, name, description, keywords string, deskID int64) (*domain.Category, error) {
	if !NewPolicy().Capabilities(actor.Role).Require(CapManageCategories) {
		return nil, domain.NewForbiddenError("category management is not permitted")
	}
	return s.CreateWithKeywords(ctx, name, description, keywords, deskID)
}

// Rename changes the category's name; the new name must be non-empty and
// unique (store returns a DuplicateError when taken).
func (s *CategoryService) Rename(ctx context.Context, id int64, name string) (*domain.Category, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, &domain.ValidationError{Field: "name", Message: domain.ErrMsgCategoryNameRequired, Key: "error.category_name_required"}
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
