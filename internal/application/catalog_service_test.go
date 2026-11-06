package application_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

type catalogServiceStore struct {
	searchResults []domain.CatalogCategory
	searchCalls   []string
	err           error
	searchErr     error
}

func (f *catalogServiceStore) SearchCatalog(_ context.Context, query string) ([]domain.CatalogCategory, error) {
	f.searchCalls = append(f.searchCalls, query)
	if f.searchErr != nil {
		return nil, f.searchErr
	}
	if f.err != nil {
		return nil, f.err
	}
	return f.searchResults, nil
}

type catalogAvailability struct {
	categories []domain.Category
	calls      int
	err        error
}

func (f *catalogAvailability) ListAvailableCategories(context.Context) ([]domain.Category, error) {
	f.calls++
	return f.categories, f.err
}

type catalogOnlyStore struct{}

func (*catalogOnlyStore) SearchCatalog(context.Context, string) ([]domain.CatalogCategory, error) {
	return nil, nil
}

// A category names no desk (issue #491): an update is accepted without one and
// persists the trimmed fields.
func TestCatalogServiceUpdateCategoryWithoutDeskPersistsTrimmedFields(t *testing.T) {
	categories := newFakeCategoryStore()
	category := categories.seed("Requests")
	svc := application.NewCatalogService(&catalogServiceStore{}, categories, fixedClock())
	admin := domain.User{Role: domain.RoleAdmin}
	ctx := context.Background()

	if err := svc.UpdateCategoryFor(ctx, admin, &category); err != nil {
		t.Fatalf("deskless category update: %v", err)
	}
	category.Name = " Requests "
	category.Description = " description "
	if err := svc.UpdateCategoryFor(ctx, admin, &category); err != nil {
		t.Fatal(err)
	}
	stored, err := categories.GetByID(ctx, category.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Name != "Requests" || stored.Description != "description" {
		t.Fatalf("stored category = %+v, want trimmed hierarchy update", stored)
	}
}

func TestCatalogServiceUpdateCategoryPersistsBlankDescription(t *testing.T) {
	categories := newFakeCategoryStore()
	category := categories.seed("Requests")
	category.Description = "Existing description"
	if err := categories.Update(context.Background(), &category); err != nil {
		t.Fatal(err)
	}
	svc := application.NewCatalogService(&catalogServiceStore{}, categories, fixedClock())

	category.Description = "   "
	if err := svc.UpdateCategoryFor(context.Background(), domain.User{Role: domain.RoleAdmin}, &category); err != nil {
		t.Fatalf("UpdateCategoryFor: %v", err)
	}
	stored, err := categories.GetByID(context.Background(), category.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if stored.Description != "" {
		t.Fatalf("stored description = %q, want empty", stored.Description)
	}
}

func TestCatalogServiceReadOperationsPropagateStoreErrors(t *testing.T) {
	want := errors.New("catalog unavailable")
	svc := application.NewCatalogService(&catalogServiceStore{err: want}, newFakeCategoryStore(), fixedClock())
	ctx := context.Background()
	for _, call := range []struct {
		name string
		call func() error
	}{
		{"all categories", func() error { _, err := svc.ListAllCategories(ctx); return err }},
		{"search", func() error { _, err := svc.Search(ctx, "requests"); return err }},
	} {
		t.Run(call.name, func(t *testing.T) {
			if err := call.call(); !errors.Is(err, want) {
				t.Fatalf("error = %v, want %v", err, want)
			}
		})
	}
}

func TestCatalogServiceFindCategoryRequiresPublishedHierarchyMatch(t *testing.T) {
	ctx := context.Background()
	categories := newFakeCategoryStore()
	category := categories.seed("Requests")
	store := &catalogServiceStore{searchResults: []domain.CatalogCategory{{
		Category: domain.Category{ID: category.ID, Name: category.Name, Description: "Hierarchy context"},
	}}}
	availability := &catalogAvailability{categories: []domain.Category{{ID: category.ID}}}

	found, err := application.NewCatalogService(store, categories, fixedClock()).FindCategory(ctx, category.ID, availability)
	if err != nil {
		t.Fatal(err)
	}
	if found.ID != category.ID || found.Description != "Hierarchy context" {
		t.Fatalf("category = %+v, want requested hierarchy context", found)
	}
	if availability.calls != 1 || len(store.searchCalls) != 1 || store.searchCalls[0] != "Requests" {
		t.Fatalf("availability calls=%d search calls=%v, want one published lookup and category search", availability.calls, store.searchCalls)
	}
}

func TestCatalogServiceFindCategoryRejectsUnavailableOrUnmatchedCategories(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name         string
		availability *catalogAvailability
		results      []domain.CatalogCategory
	}{
		{"unpublished", &catalogAvailability{categories: []domain.Category{{ID: 99}}}, nil},
		{"nil availability", nil, nil},
		{"missing hierarchy match", &catalogAvailability{categories: []domain.Category{{ID: 1}}}, []domain.CatalogCategory{{Category: domain.Category{ID: 2}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			categories := newFakeCategoryStore()
			category := categories.seed("Requests")
			store := &catalogServiceStore{searchResults: tc.results}
			var availability application.CategoryAvailability
			if tc.availability != nil {
				availability = tc.availability
			}
			_, err := application.NewCatalogService(store, categories, fixedClock()).FindCategory(ctx, category.ID, availability)
			var notFound *domain.NotFoundError
			if !errors.As(err, &notFound) || notFound.Kind != "category" || notFound.ID != category.ID {
				t.Fatalf("error = %v, want category NotFoundError for %d", err, category.ID)
			}
			if tc.name != "missing hierarchy match" && len(store.searchCalls) != 0 {
				t.Fatalf("unavailable category searched catalog: %v", store.searchCalls)
			}
		})
	}
}

func TestCatalogServiceFindCategoryPropagatesAvailabilityAndSearchErrors(t *testing.T) {
	ctx := context.Background()

	t.Run("availability", func(t *testing.T) {
		categories := newFakeCategoryStore()
		category := categories.seed("Requests")
		store := &catalogServiceStore{}
		want := errors.New("availability unavailable")
		availability := &catalogAvailability{err: want}
		_, err := application.NewCatalogService(store, categories, fixedClock()).FindCategory(ctx, category.ID, availability)
		if !errors.Is(err, want) || len(store.searchCalls) != 0 {
			t.Fatalf("error = %v search calls=%v, want availability error without catalog search", err, store.searchCalls)
		}
	})

	t.Run("search", func(t *testing.T) {
		categories := newFakeCategoryStore()
		category := categories.seed("Requests")
		want := errors.New("catalog unavailable")
		store := &catalogServiceStore{searchErr: want}
		availability := &catalogAvailability{categories: []domain.Category{{ID: category.ID}}}
		_, err := application.NewCatalogService(store, categories, fixedClock()).FindCategory(ctx, category.ID, availability)
		if !errors.Is(err, want) || len(store.searchCalls) != 1 {
			t.Fatalf("error = %v search calls=%v, want catalog error after availability", err, store.searchCalls)
		}
	})
}

func TestCatalogServiceFindCategoryRejectsMissingCategoryBeforeAvailability(t *testing.T) {
	store := &catalogServiceStore{}
	availability := &catalogAvailability{}
	_, err := application.NewCatalogService(store, newFakeCategoryStore(), fixedClock()).FindCategory(context.Background(), 7, availability)
	var notFound *domain.NotFoundError
	if !errors.As(err, &notFound) || notFound.Kind != "category" || notFound.ID != int64(7) || availability.calls != 0 || len(store.searchCalls) != 0 {
		t.Fatalf("error = %v availability calls=%d search calls=%v, want missing category before downstream lookups", err, availability.calls, store.searchCalls)
	}
}

var _ application.CatalogStore = (*catalogServiceStore)(nil)
var _ domain.Clock = (*fakeClock)(nil)

// treeCatalogStore is a catalog store that also answers the bounded tree
// projection (issue #387, S8): the desk health read and the SLA default match.
type treeCatalogStore struct {
	*catalogServiceStore
	desks []domain.CatalogDesk
	sla   []int64
	err   error
}

func (s *treeCatalogStore) ListTreeDesks(context.Context) ([]domain.CatalogDesk, error) {
	return s.desks, s.err
}

func (s *treeCatalogStore) ListDefaultSLACategories(context.Context) ([]int64, error) {
	return s.sla, s.err
}

// The tree projection is an optional port: a store that cannot answer it says so
// instead of reporting an installation with no desks.
func TestCatalogServiceTreeProjectionsRequireTheTreeStore(t *testing.T) {
	svc := application.NewCatalogService(&catalogOnlyStore{}, newFakeCategoryStore(), fixedClock())
	if _, err := svc.ListTreeDesks(context.Background()); err == nil {
		t.Fatal("ListTreeDesks must report a missing projection")
	}
	if _, err := svc.ListDefaultSLACategories(context.Background()); err == nil {
		t.Fatal("ListDefaultSLACategories must report a missing projection")
	}
}

func TestCatalogServiceTreeProjectionsDelegateAndPropagateErrors(t *testing.T) {
	ctx := context.Background()
	want := errors.New("projection unavailable")
	desk := domain.CatalogDesk{Desk: domain.Desk{ID: 7, Name: "Support"}, AgentCount: 2, OpenTickets: 3}
	svc := application.NewCatalogService(&treeCatalogStore{catalogServiceStore: &catalogServiceStore{}, desks: []domain.CatalogDesk{desk}, sla: []int64{5}}, newFakeCategoryStore(), fixedClock())
	desks, err := svc.ListTreeDesks(ctx)
	if err != nil || len(desks) != 1 || desks[0].AgentCount != 2 {
		t.Fatalf("ListTreeDesks = %+v, %v", desks, err)
	}
	defaults, err := svc.ListDefaultSLACategories(ctx)
	if err != nil || len(defaults) != 1 || defaults[0] != 5 {
		t.Fatalf("ListDefaultSLACategories = %+v, %v", defaults, err)
	}

	failing := application.NewCatalogService(&treeCatalogStore{catalogServiceStore: &catalogServiceStore{}, err: want}, newFakeCategoryStore(), fixedClock())
	if _, err := failing.ListTreeDesks(ctx); !errors.Is(err, want) {
		t.Fatalf("ListTreeDesks error = %v, want %v", err, want)
	}
	if _, err := failing.ListDefaultSLACategories(ctx); !errors.Is(err, want) {
		t.Fatalf("ListDefaultSLACategories error = %v, want %v", err, want)
	}
}

// Suggestion keywords on the catalog's category editor (issue #416, S4): the
// manager path stores what the drawer submitted, trimmed, and applies the same
// bound the category service applies.

func TestCatalogServiceUpdateCategoryStoresKeywords(t *testing.T) {
	categories := newFakeCategoryStore()
	category := categories.seed("Printers")
	svc := application.NewCatalogService(&catalogServiceStore{}, categories, fixedClock())

	category.Keywords = "  impresora, tóner "
	if err := svc.UpdateCategoryFor(context.Background(), domain.User{Role: domain.RoleAdmin}, &category); err != nil {
		t.Fatalf("UpdateCategoryFor: %v", err)
	}
	stored, err := categories.GetByID(context.Background(), category.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if stored.Keywords != "impresora, tóner" {
		t.Fatalf("stored keywords = %q, want the trimmed value", stored.Keywords)
	}
}

func TestCatalogServiceUpdateCategoryRefusesUnboundedKeywords(t *testing.T) {
	categories := newFakeCategoryStore()
	category := categories.seed("Printers")
	svc := application.NewCatalogService(&catalogServiceStore{}, categories, fixedClock())

	category.Keywords = strings.Repeat("a", application.MaxCategoryKeywordsRunes+1)
	err := svc.UpdateCategoryFor(context.Background(), domain.User{Role: domain.RoleAdmin}, &category)
	var validation *domain.ValidationError
	if !errors.As(err, &validation) || validation.Field != "keywords" {
		t.Fatalf("UpdateCategoryFor error = %v, want the keywords ValidationError", err)
	}
	stored, err := categories.GetByID(context.Background(), category.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if stored.Keywords != "" {
		t.Fatalf("a refused update must store nothing, keywords = %q", stored.Keywords)
	}
}

// Catalog folders (issue #484, S8/S11): with a folder store the catalog
// reports where each category sits, and search matches a folder name on the
// category's path as well as the category itself.
func catalogWithFolders() (*application.CatalogService, *catalogServiceStore) {
	store := &catalogServiceStore{searchResults: []domain.CatalogCategory{
		{Category: domain.Category{ID: 1, Name: "Figma seat", Description: "Design license"}},
		{Category: domain.Category{ID: 2, Name: "Payroll question", Description: "Payslips"}},
		{Category: domain.Category{ID: 3, Name: "General question", Description: "Anything"}},
	}}
	folders := newFakeFolderStore()
	it := folders.seed(nil, "IT services")
	licenses := folders.seed(&it, "Licencias")
	people := folders.seed(nil, "People & HR")
	folders.places[1] = &licenses
	folders.places[2] = &people
	folders.places[3] = nil
	svc := application.NewCatalogService(store, nil, fixedClock()).WithFolders(folders)
	return svc, store
}

func TestCatalogServiceListAllCategoriesReportsFolderPaths(t *testing.T) {
	svc, _ := catalogWithFolders()
	all, err := svc.ListAllCategories(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	got := map[string]string{}
	for _, c := range all {
		got[c.Name] = strings.Join(c.FolderPath, " › ")
		if (c.FolderID == nil) != (len(c.FolderPath) == 0) {
			t.Errorf("%s: folder id %v disagrees with path %q", c.Name, c.FolderID, c.FolderPath)
		}
	}
	want := map[string]string{"Figma seat": "IT services › Licencias", "Payroll question": "People & HR", "General question": ""}
	for name, path := range want {
		if got[name] != path {
			t.Errorf("%s path = %q, want %q", name, got[name], path)
		}
	}
}

func TestCatalogServiceSearchMatchesFolderNames(t *testing.T) {
	svc, store := catalogWithFolders()
	for query, want := range map[string][]string{
		"licencias":   {"Figma seat"},
		"IT SERVICES": {"Figma seat"},
		"payslips":    {"Payroll question"},
		"people":      {"Payroll question"},
		"procurement": nil,
		"question":    {"Payroll question", "General question"},
		"nothing":     nil,
	} {
		results, err := svc.Search(context.Background(), "  "+query+" ")
		if err != nil {
			t.Fatalf("search %q: %v", query, err)
		}
		var got []string
		for _, c := range results {
			got = append(got, c.Name)
		}
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Errorf("search %q = %q, want %q", query, got, want)
		}
	}
	for _, call := range store.searchCalls {
		if call != "" {
			t.Errorf("with folders the store is read unfiltered, got query %q", call)
		}
	}
}
