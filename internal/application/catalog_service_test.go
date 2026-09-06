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
	departments []domain.Department
	desks       []domain.Desk
	createdDesk *domain.Desk
	moveCalls   [][2]int64
	err         error
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
func (f *catalogServiceStore) SearchCatalog(context.Context, string) ([]domain.CatalogCategory, error) {
	if f.err != nil {
		return nil, f.err
	}
	return nil, nil
}
func (f *catalogServiceStore) CreateDepartment(_ context.Context, d *domain.Department) error {
	if f.err != nil {
		return f.err
	}
	d.ID = int64(len(f.departments) + 1)
	f.departments = append(f.departments, *d)
	return nil
}
func (f *catalogServiceStore) UpdateDepartment(context.Context, *domain.Department) error {
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
func (f *catalogServiceStore) UpdateDesk(context.Context, *domain.Desk) error { return f.err }
func (f *catalogServiceStore) DeleteDesk(context.Context, int64) error        { return f.err }
func (f *catalogServiceStore) ListDeskMembers(context.Context, int64) ([]domain.User, error) {
	return nil, f.err
}
func (f *catalogServiceStore) ListEligibleDeskMembers(context.Context) ([]domain.User, error) {
	return nil, f.err
}
func (f *catalogServiceStore) AddDeskMember(context.Context, int64, int64, time.Time) error {
	return f.err
}
func (f *catalogServiceStore) RemoveDeskMember(context.Context, int64, int64) error { return f.err }
func (f *catalogServiceStore) MoveCategory(_ context.Context, categoryID, deskID int64) error {
	if f.err != nil {
		return f.err
	}
	f.moveCalls = append(f.moveCalls, [2]int64{categoryID, deskID})
	return nil
}

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

var _ application.CatalogStore = (*catalogServiceStore)(nil)
var _ domain.Clock = (*fakeClock)(nil)
