package application_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

type catalogServiceStore struct {
	departments             []domain.Department
	desks                   []domain.Desk
	createdDesk             *domain.Desk
	updatedDepartment       *domain.Department
	updatedDesk             *domain.Desk
	searchResults           []domain.CatalogCategory
	searchCalls             []string
	listMemberCalls         []int64
	eligibleMemberCalls     int
	addMemberCalls          []catalogMemberCall
	removeMemberCalls       [][2]int64
	moveCalls               [][2]int64
	err                     error
	searchErr               error
	updateDepartmentCalls   int
	updateDeskCalls         int
	listDeskMembers         []domain.User
	listEligibleDeskMembers []domain.User
}

type catalogMemberCall struct {
	deskID int64
	userID int64
	at     time.Time
}

func (f *catalogServiceStore) ListDepartments(context.Context) ([]domain.CatalogDepartment, error) {
	if f.err != nil {
		return nil, f.err
	}
	return nil, nil
}
func (f *catalogServiceStore) ListDesks(context.Context, int64) ([]domain.CatalogDesk, error) {
	if f.err != nil {
		return nil, f.err
	}
	return nil, nil
}
func (f *catalogServiceStore) ListCatalogCategories(context.Context, int64) ([]domain.CatalogCategory, error) {
	if f.err != nil {
		return nil, f.err
	}
	return nil, nil
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
func (f *catalogServiceStore) CreateDepartment(_ context.Context, d *domain.Department) error {
	if f.err != nil {
		return f.err
	}
	d.ID = int64(len(f.departments) + 1)
	f.departments = append(f.departments, *d)
	return nil
}
func (f *catalogServiceStore) UpdateDepartment(_ context.Context, d *domain.Department) error {
	f.updateDepartmentCalls++
	copy := *d
	f.updatedDepartment = &copy
	return f.err
}
func (f *catalogServiceStore) DeleteDepartment(context.Context, int64) error { return f.err }
func (f *catalogServiceStore) GetDeskByID(_ context.Context, id int64) (*domain.Desk, error) {
	for _, desk := range f.desks {
		if desk.ID == id {
			copy := desk
			return &copy, nil
		}
	}
	return nil, &domain.NotFoundError{Kind: "desk", ID: id}
}
func (f *catalogServiceStore) CreateDesk(_ context.Context, a *domain.Desk) error {
	if f.err != nil {
		return f.err
	}
	a.ID = int64(len(f.desks) + 1)
	f.desks = append(f.desks, *a)
	f.createdDesk = a
	return nil
}
func (f *catalogServiceStore) UpdateDesk(_ context.Context, d *domain.Desk) error {
	f.updateDeskCalls++
	copy := *d
	f.updatedDesk = &copy
	return f.err
}
func (f *catalogServiceStore) DeleteDesk(context.Context, int64) error { return f.err }
func (f *catalogServiceStore) ListDeskMembers(_ context.Context, deskID int64) ([]domain.User, error) {
	f.listMemberCalls = append(f.listMemberCalls, deskID)
	if f.err != nil {
		return nil, f.err
	}
	return f.listDeskMembers, nil
}
func (f *catalogServiceStore) ListEligibleDeskMembers(context.Context) ([]domain.User, error) {
	f.eligibleMemberCalls++
	if f.err != nil {
		return nil, f.err
	}
	return f.listEligibleDeskMembers, nil
}
func (f *catalogServiceStore) AddDeskMember(_ context.Context, deskID, userID int64, at time.Time) error {
	f.addMemberCalls = append(f.addMemberCalls, catalogMemberCall{deskID: deskID, userID: userID, at: at})
	return f.err
}
func (f *catalogServiceStore) RemoveDeskMember(_ context.Context, deskID, userID int64) error {
	f.removeMemberCalls = append(f.removeMemberCalls, [2]int64{deskID, userID})
	return f.err
}
func (f *catalogServiceStore) MoveCategory(_ context.Context, categoryID, deskID int64) error {
	if f.err != nil {
		return f.err
	}
	f.moveCalls = append(f.moveCalls, [2]int64{categoryID, deskID})
	return nil
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

func (*catalogOnlyStore) ListDepartments(context.Context) ([]domain.CatalogDepartment, error) {
	return nil, nil
}
func (*catalogOnlyStore) ListDesks(context.Context, int64) ([]domain.CatalogDesk, error) {
	return nil, nil
}
func (*catalogOnlyStore) ListCatalogCategories(context.Context, int64) ([]domain.CatalogCategory, error) {
	return nil, nil
}
func (*catalogOnlyStore) SearchCatalog(context.Context, string) ([]domain.CatalogCategory, error) {
	return nil, nil
}
func (*catalogOnlyStore) CreateDepartment(context.Context, *domain.Department) error { return nil }
func (*catalogOnlyStore) UpdateDepartment(context.Context, *domain.Department) error { return nil }
func (*catalogOnlyStore) DeleteDepartment(context.Context, int64) error              { return nil }
func (*catalogOnlyStore) MoveCategory(context.Context, int64, int64) error           { return nil }

func TestCatalogServiceTrimsAndStampsHierarchyMutations(t *testing.T) {
	clock := fixedClock()
	store := &catalogServiceStore{}
	svc := application.NewCatalogService(store, newFakeCategoryStore(), clock)
	admin := domain.User{Role: domain.RoleAdmin}

	department, err := svc.CreateDepartmentFor(context.Background(), admin, " Operations ", " description ")
	if err != nil {
		t.Fatal(err)
	}
	if department.Name != "Operations" || department.Description != "description" || !department.CreatedAt.Equal(clock.now) {
		t.Fatalf("department = %+v, want trimmed values and injected time", department)
	}
	desk, err := svc.CreateDeskFor(context.Background(), admin, department.ID, " Support ", " Support description ")
	if err != nil {
		t.Fatal(err)
	}
	if desk.Name != "Support" || desk.Description != "Support description" || desk.DepartmentID == nil || *desk.DepartmentID != department.ID || !desk.CreatedAt.Equal(clock.now) {
		t.Fatalf("desk = %+v, want trimmed values and parent %d", desk, department.ID)
	}
}

func TestCatalogServiceRejectsInvalidHierarchyInputsBeforeStore(t *testing.T) {
	store := &catalogServiceStore{}
	svc := application.NewCatalogService(store, newFakeCategoryStore(), fixedClock())
	admin := domain.User{Role: domain.RoleAdmin}
	ctx := context.Background()

	for _, tc := range []struct {
		name string
		call func() error
	}{
		{"missing desk parent", func() error {
			_, err := svc.CreateDeskFor(ctx, admin, 0, "Support")
			return err
		}},
		{"missing category desk", func() error {
			return svc.MoveCategoryFor(ctx, admin, 1, 0)
		}},
		{"missing category id", func() error {
			return svc.MoveCategoryFor(ctx, admin, 0, 1)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var validation *domain.ValidationError
			if err := tc.call(); !errors.As(err, &validation) {
				t.Fatalf("error = %v, want ValidationError", err)
			}
		})
	}
	if len(store.desks) != 0 || len(store.moveCalls) != 0 {
		t.Fatalf("invalid hierarchy inputs reached store: desks=%d moves=%d", len(store.desks), len(store.moveCalls))
	}
}

func TestCatalogServiceUpdateCategoryRequiresDeskAndPreservesStoreErrors(t *testing.T) {
	categories := newFakeCategoryStore()
	category := categories.seed("Requests")
	svc := application.NewCatalogService(&catalogServiceStore{}, categories, fixedClock())
	admin := domain.User{Role: domain.RoleAdmin}
	ctx := context.Background()

	category.DeskID = 0
	if err := svc.UpdateCategoryFor(ctx, admin, &category); err == nil {
		t.Fatal("category without desk must be rejected")
	}
	category.DeskID = 4
	category.Name = " Requests "
	category.Description = " description "
	if err := svc.UpdateCategoryFor(ctx, admin, &category); err != nil {
		t.Fatal(err)
	}
	stored, err := categories.GetByID(ctx, category.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Name != "Requests" || stored.Description != "description" || stored.DeskID != 4 {
		t.Fatalf("stored category = %+v, want trimmed hierarchy update", stored)
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
		{"departments", func() error { _, err := svc.ListDepartments(ctx); return err }},
		{"desks", func() error { _, err := svc.ListDesks(ctx, 4); return err }},
		{"categories", func() error { _, err := svc.ListCategories(ctx, 8); return err }},
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
		Category:       domain.Category{ID: category.ID, Name: category.Name, DeskID: 8},
		DeskName:       "Support",
		DepartmentID:   4,
		DepartmentName: "Operations",
	}}}
	availability := &catalogAvailability{categories: []domain.Category{{ID: category.ID}}}

	found, err := application.NewCatalogService(store, categories, fixedClock()).FindCategory(ctx, category.ID, availability)
	if err != nil {
		t.Fatal(err)
	}
	if found.ID != category.ID || found.DeskName != "Support" || found.DepartmentID != 4 || found.DepartmentName != "Operations" {
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

func TestCatalogServiceUpdateDepartmentForTrimsAndValidates(t *testing.T) {
	ctx := context.Background()
	for _, role := range []domain.Role{domain.RoleAdmin, domain.RoleRoot} {
		t.Run(string(role), func(t *testing.T) {
			store := &catalogServiceStore{}
			department, err := application.NewCatalogService(store, newFakeCategoryStore(), fixedClock()).UpdateDepartmentFor(ctx, domain.User{Role: role}, 7, " Operations ", " Platform ")
			if err != nil {
				t.Fatal(err)
			}
			if department.ID != 7 || department.Name != "Operations" || department.Description != "Platform" || store.updatedDepartment == nil || *store.updatedDepartment != *department {
				t.Fatalf("department = %+v stored = %+v, want trimmed update", department, store.updatedDepartment)
			}
		})
	}

	store := &catalogServiceStore{}
	_, err := application.NewCatalogService(store, newFakeCategoryStore(), fixedClock()).UpdateDepartmentFor(ctx, domain.User{Role: domain.RoleAdmin}, 7, "  ", "ignored")
	var validation *domain.ValidationError
	if !errors.As(err, &validation) || store.updateDepartmentCalls != 0 {
		t.Fatalf("error = %v update calls=%d, want name validation before mutation", err, store.updateDepartmentCalls)
	}

	want := errors.New("department update failed")
	store.err = want
	if _, err := application.NewCatalogService(store, newFakeCategoryStore(), fixedClock()).UpdateDepartmentFor(ctx, domain.User{Role: domain.RoleAdmin}, 7, "Operations", "Platform"); !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
	store.err = nil
	_, err = application.NewCatalogService(store, newFakeCategoryStore(), fixedClock()).UpdateDepartmentFor(ctx, domain.User{Role: domain.RoleUser}, 7, "Operations", "Platform")
	var forbidden *domain.ForbiddenError
	if !errors.As(err, &forbidden) || store.updateDepartmentCalls != 1 {
		t.Fatalf("error = %v update calls=%d, want forbidden before mutation", err, store.updateDepartmentCalls)
	}
}

func TestCatalogServiceUpdateDeskForPreservesIdentityAndPropagatesStoreErrors(t *testing.T) {
	ctx := context.Background()
	originalDepartment := int64(2)
	store := &catalogServiceStore{desks: []domain.Desk{{ID: 6, Name: "Legacy", Description: "Legacy description", DepartmentID: &originalDepartment}}}
	svc := application.NewCatalogService(store, newFakeCategoryStore(), fixedClock())
	if err := svc.UpdateDeskFor(ctx, domain.User{Role: domain.RoleAdmin}, 6, 4, " Support ", " Updated description "); err != nil {
		t.Fatal(err)
	}
	if store.updatedDesk == nil || store.updatedDesk.ID != 6 || store.updatedDesk.Name != "Support" || store.updatedDesk.Description != "Updated description" || store.updatedDesk.DepartmentID == nil || *store.updatedDesk.DepartmentID != 4 {
		t.Fatalf("updated desk = %+v, want retained identity and updated hierarchy", store.updatedDesk)
	}

	store = &catalogServiceStore{desks: []domain.Desk{{ID: 6, Name: "Legacy", Description: "Legacy description", DepartmentID: &originalDepartment}}}
	svc = application.NewCatalogService(store, newFakeCategoryStore(), fixedClock())
	if err := svc.UpdateDeskFor(ctx, domain.User{Role: domain.RoleRoot}, 6, 4, "Support"); err != nil {
		t.Fatal(err)
	}
	if store.updatedDesk.Description != "Legacy description" {
		t.Fatalf("description = %q, want existing description without replacement", store.updatedDesk.Description)
	}

	store = &catalogServiceStore{desks: []domain.Desk{{ID: 6, Name: "Legacy"}}}
	svc = application.NewCatalogService(store, newFakeCategoryStore(), fixedClock())
	for _, call := range []func() error{
		func() error { return svc.UpdateDeskFor(ctx, domain.User{Role: domain.RoleAdmin}, 0, 4, "Support") },
		func() error { return svc.UpdateDeskFor(ctx, domain.User{Role: domain.RoleAdmin}, 6, 0, "Support") },
		func() error { return svc.UpdateDeskFor(ctx, domain.User{Role: domain.RoleAdmin}, 6, 4, " ") },
	} {
		if err := call(); err == nil {
			t.Fatal("invalid desk update must fail")
		}
	}
	if store.updateDeskCalls != 0 {
		t.Fatalf("invalid desk update reached store %d times", store.updateDeskCalls)
	}

	want := errors.New("update failed")
	store.err = want
	if err := svc.UpdateDeskFor(ctx, domain.User{Role: domain.RoleAdmin}, 6, 4, "Support"); !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
	store.err = nil
	if err := svc.UpdateDeskFor(ctx, domain.User{Role: domain.RoleUser}, 6, 4, "Support"); err == nil || store.updateDeskCalls != 1 {
		t.Fatalf("error = %v update calls=%d, want forbidden before mutation", err, store.updateDeskCalls)
	}

	missing := application.NewCatalogService(&catalogServiceStore{}, newFakeCategoryStore(), fixedClock())
	var notFound *domain.NotFoundError
	if err := missing.UpdateDeskFor(ctx, domain.User{Role: domain.RoleAdmin}, 6, 4, "Support"); !errors.As(err, &notFound) {
		t.Fatalf("error = %v, want missing desk NotFoundError", err)
	}
}

func TestCatalogServiceDeskMembershipForUsesStoreAndAuthorization(t *testing.T) {
	ctx := context.Background()
	clock := fixedClock()
	members := []domain.User{{ID: 10, Name: "Ada"}}
	eligible := []domain.User{{ID: 11, Name: "Lin"}}
	store := &catalogServiceStore{listDeskMembers: members, listEligibleDeskMembers: eligible}
	svc := application.NewCatalogService(store, newFakeCategoryStore(), clock)
	admin := domain.User{Role: domain.RoleAdmin}

	gotMembers, err := svc.ListDeskMembersFor(ctx, admin, 8)
	if err != nil || len(gotMembers) != 1 || gotMembers[0].ID != 10 || len(store.listMemberCalls) != 1 || store.listMemberCalls[0] != 8 {
		t.Fatalf("members = %+v err=%v calls=%v, want desk 8 members", gotMembers, err, store.listMemberCalls)
	}
	gotEligible, err := svc.ListEligibleDeskMembersFor(ctx, admin)
	if err != nil || len(gotEligible) != 1 || gotEligible[0].ID != 11 || store.eligibleMemberCalls != 1 {
		t.Fatalf("eligible = %+v err=%v calls=%d", gotEligible, err, store.eligibleMemberCalls)
	}
	if err := svc.AddDeskMemberFor(ctx, admin, 8, 10); err != nil {
		t.Fatal(err)
	}
	if err := svc.RemoveDeskMemberFor(ctx, admin, 8, 10); err != nil {
		t.Fatal(err)
	}
	if len(store.addMemberCalls) != 1 || store.addMemberCalls[0].deskID != 8 || store.addMemberCalls[0].userID != 10 || !store.addMemberCalls[0].at.Equal(clock.now) || len(store.removeMemberCalls) != 1 || store.removeMemberCalls[0] != [2]int64{8, 10} {
		t.Fatalf("membership calls = add:%+v remove:%v", store.addMemberCalls, store.removeMemberCalls)
	}

	for _, role := range []domain.Role{domain.RoleUser, domain.RoleAgent} {
		t.Run(string(role), func(t *testing.T) {
			denied := &catalogServiceStore{}
			deniedSvc := application.NewCatalogService(denied, newFakeCategoryStore(), clock)
			for _, call := range []func() error{
				func() error { _, err := deniedSvc.ListDeskMembersFor(ctx, domain.User{Role: role}, 8); return err },
				func() error { _, err := deniedSvc.ListEligibleDeskMembersFor(ctx, domain.User{Role: role}); return err },
				func() error { return deniedSvc.AddDeskMemberFor(ctx, domain.User{Role: role}, 8, 10) },
				func() error { return deniedSvc.RemoveDeskMemberFor(ctx, domain.User{Role: role}, 8, 10) },
			} {
				var forbidden *domain.ForbiddenError
				if err := call(); !errors.As(err, &forbidden) {
					t.Fatalf("error = %v, want ForbiddenError", err)
				}
			}
			if len(denied.listMemberCalls) != 0 || denied.eligibleMemberCalls != 0 || len(denied.addMemberCalls) != 0 || len(denied.removeMemberCalls) != 0 {
				t.Fatalf("unauthorized calls reached store: %+v", denied)
			}
		})
	}
}

func TestCatalogServiceRejectsDeskOperationsWithoutDeskStore(t *testing.T) {
	ctx := context.Background()
	svc := application.NewCatalogService(&catalogOnlyStore{}, newFakeCategoryStore(), fixedClock())
	admin := domain.User{Role: domain.RoleAdmin}
	for _, call := range []func() error{
		func() error { return svc.UpdateDeskFor(ctx, admin, 8, 4, "Support") },
		func() error { _, err := svc.ListDeskMembersFor(ctx, admin, 8); return err },
		func() error { _, err := svc.ListEligibleDeskMembersFor(ctx, admin); return err },
		func() error { return svc.AddDeskMemberFor(ctx, admin, 8, 10) },
		func() error { return svc.RemoveDeskMemberFor(ctx, admin, 8, 10) },
	} {
		var forbidden *domain.ForbiddenError
		if err := call(); !errors.As(err, &forbidden) {
			t.Fatalf("error = %v, want forbidden when desk management is not configured", err)
		}
	}
}

func TestCatalogServiceDeskMembershipForPropagatesStoreErrors(t *testing.T) {
	ctx := context.Background()
	want := errors.New("store unavailable")
	admin := domain.User{Role: domain.RoleAdmin}
	for _, tc := range []struct {
		name string
		call func(*application.CatalogService) error
	}{
		{"members", func(s *application.CatalogService) error { _, err := s.ListDeskMembersFor(ctx, admin, 8); return err }},
		{"eligible", func(s *application.CatalogService) error {
			_, err := s.ListEligibleDeskMembersFor(ctx, admin)
			return err
		}},
		{"add", func(s *application.CatalogService) error { return s.AddDeskMemberFor(ctx, admin, 8, 10) }},
		{"remove", func(s *application.CatalogService) error { return s.RemoveDeskMemberFor(ctx, admin, 8, 10) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := application.NewCatalogService(&catalogServiceStore{err: want}, newFakeCategoryStore(), fixedClock())
			if err := tc.call(svc); !errors.Is(err, want) {
				t.Fatalf("error = %v, want %v", err, want)
			}
		})
	}
}

var _ application.CatalogStore = (*catalogServiceStore)(nil)
var _ domain.Clock = (*fakeClock)(nil)
