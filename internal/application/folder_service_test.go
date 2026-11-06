package application_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// FolderService (issue #484): only actors who manage categories change the
// folder tree, names are trimmed and required before the store is touched, and
// the store's refusals (duplicate sibling, cycle, non-empty delete) reach the
// caller unchanged.

func newFolderService() (*application.FolderService, *fakeFolderStore) {
	store := newFakeFolderStore()
	return application.NewFolderService(store, fixedClock()), store
}

func TestFolderServiceRequiresCategoryManagement(t *testing.T) {
	ctx := context.Background()
	svc, store := newFolderService()
	existing := store.seed(nil, "IT services")
	for _, role := range []domain.Role{domain.RoleUser, domain.RoleAgent} {
		actor := domain.User{ID: 9, Role: role}
		var forbidden *domain.ForbiddenError
		checks := map[string]error{}
		_, checks["create"] = svc.CreateFor(ctx, actor, nil, "Hardware", "")
		_, checks["update"] = svc.UpdateFor(ctx, actor, existing, "Renamed", "")
		checks["move"] = svc.MoveFor(ctx, actor, existing, nil)
		checks["delete"] = svc.DeleteFor(ctx, actor, existing)
		checks["place"] = svc.PlaceCategoryFor(ctx, actor, 1, &existing)
		for op, err := range checks {
			if !errors.As(err, &forbidden) {
				t.Errorf("%s as %s: err = %v, want forbidden", op, role, err)
			}
		}
	}
	if store.writes != 0 {
		t.Errorf("forbidden calls wrote %d times, want 0", store.writes)
	}
}

func TestFolderServiceCreateTrimsStampsAndValidates(t *testing.T) {
	ctx := context.Background()
	svc, store := newFolderService()
	admin := domain.User{ID: 1, Role: domain.RoleAdmin}
	parent := store.seed(nil, "IT services")

	f, err := svc.CreateFor(ctx, admin, &parent, "  Hardware  ", "  Laptops and docks ")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	want := domain.Folder{ID: f.ID, ParentID: &parent, Name: "Hardware", Description: "Laptops and docks", CreatedAt: fixedClock().Now()}
	if !reflect.DeepEqual(*f, want) {
		t.Errorf("created = %+v, want %+v", *f, want)
	}

	writes := store.writes
	for _, name := range []string{"", "   "} {
		_, err := svc.CreateFor(ctx, admin, nil, name, "")
		var v *domain.ValidationError
		if !errors.As(err, &v) || v.Field != "name" || v.Message != "folder name is required" || v.Key != "error.folder_name_required" {
			t.Errorf("create %q: err = %#v, want the folder name validation", name, err)
		}
	}
	if _, err := svc.UpdateFor(ctx, admin, parent, " ", ""); err == nil {
		t.Errorf("update with a blank name succeeded")
	}
	if store.writes != writes {
		t.Errorf("invalid input reached the store (%d writes)", store.writes-writes)
	}
}

func TestFolderServicePassesStoreRefusalsThrough(t *testing.T) {
	ctx := context.Background()
	svc, store := newFolderService()
	admin := domain.User{ID: 1, Role: domain.RoleAdmin}
	it := store.seed(nil, "IT services")
	hw := store.seed(&it, "Hardware")

	if _, err := svc.CreateFor(ctx, admin, &it, "hardware", ""); !errors.Is(err, domain.ErrDuplicate) {
		t.Errorf("duplicate sibling: err = %v", err)
	}
	if err := svc.MoveFor(ctx, admin, it, &hw); !errors.Is(err, domain.ErrFolderCycle) {
		t.Errorf("cycle: err = %v", err)
	}
	if err := svc.DeleteFor(ctx, admin, it); !errors.Is(err, domain.ErrReferenced) {
		t.Errorf("delete non-empty: err = %v", err)
	}
	updated, err := svc.UpdateFor(ctx, admin, hw, " Equipment ", " Desks ")
	if err != nil || updated.Name != "Equipment" || updated.Description != "Desks" {
		t.Errorf("update = %+v, %v", updated, err)
	}
	if err := svc.MoveFor(ctx, admin, hw, nil); err != nil {
		t.Errorf("move to top level: %v", err)
	}
	if err := svc.PlaceCategoryFor(ctx, admin, 7, &hw); err != nil || store.places[7] == nil || *store.places[7] != hw {
		t.Errorf("place category: %v, places=%v", err, store.places)
	}
	if err := svc.DeleteFor(ctx, admin, it); err != nil {
		t.Errorf("delete emptied folder: %v", err)
	}
}

func TestFolderServiceTreeReadsEveryFolder(t *testing.T) {
	ctx := context.Background()
	svc, store := newFolderService()
	it := store.seed(nil, "IT services")
	store.seed(&it, "Hardware")
	tree, err := svc.Tree(ctx)
	if err != nil {
		t.Fatalf("tree: %v", err)
	}
	if got := tree.PathNames(&store.ids[1]); !reflect.DeepEqual(got, []string{"IT services", "Hardware"}) {
		t.Errorf("path = %q", got)
	}
	store.listErr = errors.New("disk on fire")
	if _, err := svc.Tree(ctx); err == nil {
		t.Errorf("tree hid a store error")
	}
}

// TestCategoryServiceCreateInFolderFor: a new category is created in the folder
// the drawer chose, in the same write (issue #484); only category managers may.
func TestCategoryServiceCreateInFolderFor(t *testing.T) {
	ctx := context.Background()
	store := newFakeCategoryStore()
	svc := application.NewCategoryService(store, fixedClock())
	folder := int64(7)
	admin := domain.User{ID: 1, Role: domain.RoleAdmin}

	c, err := svc.CreateInFolderFor(ctx, admin, " Laptop repair ", "Broken laptop", "screen, battery", &folder)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	stored, _ := store.GetByID(ctx, c.ID)
	if stored.Name != "Laptop repair" || stored.FolderID == nil || *stored.FolderID != folder || stored.Keywords != "screen, battery" {
		t.Errorf("stored = %+v", stored)
	}
	top, err := svc.CreateInFolderFor(ctx, admin, "General question", "", "", nil)
	if err != nil || top.FolderID != nil {
		t.Errorf("top-level create = %+v, %v", top, err)
	}
	var forbidden *domain.ForbiddenError
	if _, err := svc.CreateInFolderFor(ctx, domain.User{ID: 2, Role: domain.RoleAgent}, "Sneaky", "", "", &folder); !errors.As(err, &forbidden) {
		t.Errorf("agent create: err = %v, want forbidden", err)
	}
}
