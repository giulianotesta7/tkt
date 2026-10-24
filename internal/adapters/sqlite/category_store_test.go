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

// TestCategoryKeywordsRoundTrip proves the store carries the administrator's
// suggestion keywords (issue #416, S4) through create, read, list and update, and
// that the value reaches the category-suggestion index the same write feeds.
func TestCategoryKeywordsRoundTrip(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	store := newCategoryStore(s.db)

	c := &domain.Category{Name: "Printers", Description: "Printer problems", Keywords: "impresora, tóner", CreatedAt: testClock}
	if err := store.Create(ctx, c); err != nil {
		t.Fatalf("Create: %v", err)
	}
	stored, err := store.GetByID(ctx, c.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if stored.Keywords != "impresora, tóner" {
		t.Fatalf("GetByID keywords = %q, want the stored value", stored.Keywords)
	}
	listed, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(listed) != 1 || listed[0].Keywords != "impresora, tóner" {
		t.Fatalf("List keywords = %+v, want the stored value", listed)
	}

	stored.Keywords = "impresora, escáner"
	if err := store.Update(ctx, stored); err != nil {
		t.Fatalf("Update: %v", err)
	}
	updated, err := store.GetByID(ctx, c.ID)
	if err != nil {
		t.Fatalf("GetByID after update: %v", err)
	}
	if updated.Keywords != "impresora, escáner" {
		t.Fatalf("updated keywords = %q, want the new value", updated.Keywords)
	}
	// The update also reaches the index, because the same row write fires the
	// migration-0031 trigger.
	var indexed string
	if err := s.db.QueryRow(`SELECT keywords FROM category_suggest_fts WHERE rowid = ?`, c.ID).Scan(&indexed); err != nil {
		t.Fatalf("read indexed keywords: %v", err)
	}
	if indexed != "impresora, escáner" {
		t.Fatalf("indexed keywords = %q, want the new value", indexed)
	}
}

// TestAvailableCategoriesKeepTheirProjection is the preserve test for the category
// projection refactor: ListAvailableCategories selects five columns and shares its
// scan with the category manager, so adding the suggestion keywords must not change
// what the requester's picker reads. It publishes a workflow and asks the picker's
// own port, which is the call a regression would break.
func TestAvailableCategoriesKeepTheirProjection(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	c := seedCategory(t, s, "available")
	desk := seedDeskWithMemberNamed(t, s, seedUserRaw(t, s, "Available Agent", "available@tkt.test", "agent"), "DeskAvailable")
	ws := s.WorkflowStore()
	definition := mustCanon(t, domain.WorkflowDefinition{{Type: domain.StepAssignToDesk,
		AssignToDesk: &domain.AssignToDeskStep{DeskID: desk, Strategy: domain.StrategyClaim}}})
	if _, issues, err := ws.Publish(ctx, c, definition, nil); err != nil || len(issues) != 0 {
		t.Fatalf("Publish: err=%v issues=%v", err, issues)
	}

	available, err := ws.ListAvailableCategories(ctx)
	if err != nil {
		t.Fatalf("ListAvailableCategories: %v", err)
	}
	if len(available) != 1 {
		t.Fatalf("available = %+v, want the one published category", available)
	}
	if available[0].ID != c || available[0].Name != "available" || available[0].DeskID == 0 {
		t.Fatalf("available category = %+v, want the five-column projection intact", available[0])
	}
}
