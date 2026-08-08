package sqlite

import (
	"context"
	"errors"
	"testing"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// Task 4.6: category store — UNIQUE name → Duplicate, delete guard →
// Referenced, GetByID/List (category-management spec).

func TestCategoryCreateAssignsIDAndPersists(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	store := newCategoryStore(s.db)

	c := &domain.Category{Name: "Bugs", CreatedAt: testClock}
	if err := store.Create(ctx, c); err != nil {
		t.Fatalf("create: %v", err)
	}
	if c.ID == 0 {
		t.Error("create did not assign an id")
	}

	got, err := store.GetByID(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Bugs" {
		t.Errorf("category = %+v, want name Bugs", got)
	}
	if !got.CreatedAt.Equal(testClock) {
		t.Errorf("created_at = %v, want %v", got.CreatedAt, testClock)
	}
}

func TestCategoryCreateDuplicateName(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	store := newCategoryStore(s.db)

	if err := store.Create(ctx, &domain.Category{Name: "Bugs", CreatedAt: testClock}); err != nil {
		t.Fatal(err)
	}
	err := store.Create(ctx, &domain.Category{Name: "Bugs", CreatedAt: testClock})
	if !errors.Is(err, domain.ErrDuplicate) {
		t.Fatalf("err = %v, want ErrDuplicate", err)
	}
	var de *domain.DuplicateError
	if !errors.As(err, &de) || de.Kind != "category" || de.Name != "Bugs" {
		t.Errorf("err = %v, want DuplicateError{Kind: category, Name: Bugs}", err)
	}
}

func TestCategoryUpdateRenames(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	store := newCategoryStore(s.db)

	c := &domain.Category{Name: "Bugs", CreatedAt: testClock}
	if err := store.Create(ctx, c); err != nil {
		t.Fatal(err)
	}

	c.Name = "Defects"
	if err := store.Update(ctx, c); err != nil {
		t.Fatalf("update: %v", err)
	}

	got, err := store.GetByID(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Defects" {
		t.Errorf("category = %+v, want name Defects", got)
	}

	// Spec: after the rename "Bugs" is free for future use.
	if err := store.Create(ctx, &domain.Category{Name: "Bugs", CreatedAt: testClock}); err != nil {
		t.Errorf("create after rename: %v (want Bugs free again)", err)
	}
}

func TestCategoryUpdateDuplicateName(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	store := newCategoryStore(s.db)

	bugs := &domain.Category{Name: "Bugs", CreatedAt: testClock}
	support := &domain.Category{Name: "Support", CreatedAt: testClock}
	if err := store.Create(ctx, bugs); err != nil {
		t.Fatal(err)
	}
	if err := store.Create(ctx, support); err != nil {
		t.Fatal(err)
	}

	// Renaming Support to Bugs must be rejected — and Bugs' row must keep
	// its own name.
	support.Name = "Bugs"
	err := store.Update(ctx, support)
	if !errors.Is(err, domain.ErrDuplicate) {
		t.Fatalf("err = %v, want ErrDuplicate", err)
	}
	got, err := store.GetByID(ctx, bugs.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Bugs" {
		t.Errorf("bugs name = %q, want unchanged", got.Name)
	}
}

func TestCategoryUpdateNotFound(t *testing.T) {
	s := newTestDB(t)
	err := newCategoryStore(s.db).Update(context.Background(), &domain.Category{ID: 42, Name: "X"})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestCategoryDeleteUnreferenced(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	store := newCategoryStore(s.db)

	c := &domain.Category{Name: "Bugs", CreatedAt: testClock}
	if err := store.Create(ctx, c); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(ctx, c.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := store.GetByID(ctx, c.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("get after delete = %v, want ErrNotFound", err)
	}
}

func TestCategoryDeleteReferencedByTicket(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	store := newCategoryStore(s.db)

	c := &domain.Category{Name: "Bugs", CreatedAt: testClock}
	if err := store.Create(ctx, c); err != nil {
		t.Fatal(err)
	}
	seedTicket(t, s, domain.Ticket{Number: 1, Title: "uses category", CategoryID: c.ID,
		Priority: domain.PriorityMedium, State: domain.StateNew,
		CreatedAt: testClock, UpdatedAt: testClock})

	err := store.Delete(ctx, c.ID)
	if !errors.Is(err, domain.ErrReferenced) {
		t.Fatalf("err = %v, want ErrReferenced", err)
	}
	var re *domain.ReferencedError
	if !errors.As(err, &re) || re.Kind != "category" || re.ID != c.ID {
		t.Errorf("err = %v, want ReferencedError{Kind: category, ID: %d}", err, c.ID)
	}
	// The referenced category must survive the blocked delete.
	if _, err := store.GetByID(ctx, c.ID); err != nil {
		t.Errorf("category deleted despite reference: %v", err)
	}
}

func TestCategoryDeleteNotFound(t *testing.T) {
	s := newTestDB(t)
	err := newCategoryStore(s.db).Delete(context.Background(), 42)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestCategoryGetByIDNotFound(t *testing.T) {
	s := newTestDB(t)
	_, err := newCategoryStore(s.db).GetByID(context.Background(), 42)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestCategoryList(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	store := newCategoryStore(s.db)

	for _, name := range []string{"Bugs", "Support", "Billing"} {
		if err := store.Create(ctx, &domain.Category{Name: name, CreatedAt: testClock}); err != nil {
			t.Fatal(err)
		}
	}

	all, err := store.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("list len = %d, want 3", len(all))
	}
	want := []string{"Bugs", "Support", "Billing"}
	for i, c := range all {
		if c.Name != want[i] {
			t.Errorf("list[%d] name = %q, want %q (creation order)", i, c.Name, want[i])
		}
	}
}

func TestCategoryListEmpty(t *testing.T) {
	s := newTestDB(t)
	all, err := newCategoryStore(s.db).List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 0 {
		t.Errorf("fresh db list len = %d, want 0", len(all))
	}
}
