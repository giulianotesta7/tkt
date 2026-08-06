package application_test

import (
	"context"
	"errors"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

func newCategoryService() (*application.CategoryService, *fakeCategoryStore, *fakeClock) {
	clock := fixedClock()
	categories := newFakeCategoryStore()
	return application.NewCategoryService(categories, clock), categories, clock
}

func TestCategoryCreateAndList(t *testing.T) {
	svc, _, clock := newCategoryService()

	c, err := svc.Create(context.Background(), "Bugs")
	if err != nil {
		t.Fatalf("Create: unexpected error: %v", err)
	}
	if c.ID == 0 || c.Name != "Bugs" {
		t.Fatalf("Create: category must be stored, got %+v", c)
	}
	if !c.CreatedAt.Equal(clock.now) {
		t.Fatalf("Create: timestamp must come from the injected clock, got %v", c.CreatedAt)
	}

	list, err := svc.List(context.Background())
	if err != nil {
		t.Fatalf("List: unexpected error: %v", err)
	}
	if len(list) != 1 || list[0].Name != "Bugs" {
		t.Fatalf("List: created category must be listed, got %+v", list)
	}
}

func TestCategoryCreateRejectsDuplicateAndEmptyName(t *testing.T) {
	svc, categories, _ := newCategoryService()
	categories.seed("Bugs")

	if _, err := svc.Create(context.Background(), "Bugs"); err == nil {
		t.Fatal("Create: duplicate name must be rejected")
	} else {
		var derr *domain.DuplicateError
		if !errors.As(err, &derr) || derr.Kind != "category" {
			t.Fatalf("Create: duplicate name must be a DuplicateError(kind=category), got %v", err)
		}
	}

	_, err := svc.Create(context.Background(), "   ")
	var verr *domain.ValidationError
	if !errors.As(err, &verr) || verr.Field != "name" {
		t.Fatalf("Create: empty name must be a ValidationError on field name, got %v", err)
	}
	if len(categories.categories) != 1 {
		t.Fatal("Create: rejected names must not store categories")
	}
}

func TestCategoryRenameAndFreeOldName(t *testing.T) {
	svc, categories, _ := newCategoryService()
	bugs := categories.seed("Bugs")

	renamed, err := svc.Rename(context.Background(), bugs.ID, "Defects")
	if err != nil {
		t.Fatalf("Rename: unexpected error: %v", err)
	}
	if renamed.Name != "Defects" {
		t.Fatalf("Rename: category must be stored with the new name, got %q", renamed.Name)
	}
	// "Bugs" is free for future use.
	if _, err := svc.Create(context.Background(), "Bugs"); err != nil {
		t.Fatalf("Create: old name must be free after rename, got %v", err)
	}
}

func TestCategoryRenameToDuplicateRejected(t *testing.T) {
	svc, categories, _ := newCategoryService()
	bugs := categories.seed("Bugs")
	support := categories.seed("Support")

	_, err := svc.Rename(context.Background(), support.ID, "Bugs")
	var derr *domain.DuplicateError
	if !errors.As(err, &derr) || derr.Kind != "category" {
		t.Fatalf("Rename: rename to duplicate must be a DuplicateError(kind=category), got %v", err)
	}
	stored, _ := categories.GetByID(context.Background(), support.ID)
	if stored.Name != "Support" {
		t.Fatalf("Rename: rejected rename must not change the stored name, got %q", stored.Name)
	}
	if bugs.Name != "Bugs" {
		t.Fatal("Rename: rejected rename must not touch the other category")
	}
}

func TestCategoryDeleteReferencedRejected(t *testing.T) {
	svc, categories, _ := newCategoryService()
	bugs := categories.seed("Bugs")
	categories.markReferenced(bugs.ID) // used by a ticket

	err := svc.Delete(context.Background(), bugs.ID)
	var rerr *domain.ReferencedError
	if !errors.As(err, &rerr) || rerr.Kind != "category" {
		t.Fatalf("Delete: referenced category must be a ReferencedError(kind=category), got %v", err)
	}
	if len(categories.categories) != 1 {
		t.Fatal("Delete: referenced category must not be removed")
	}
}

func TestCategoryDeleteUnreferencedRemoves(t *testing.T) {
	svc, categories, _ := newCategoryService()
	bugs := categories.seed("Bugs")

	if err := svc.Delete(context.Background(), bugs.ID); err != nil {
		t.Fatalf("Delete: unreferenced category must be deletable, got %v", err)
	}
	if len(categories.categories) != 0 {
		t.Fatal("Delete: category must be removed from the list")
	}
}
