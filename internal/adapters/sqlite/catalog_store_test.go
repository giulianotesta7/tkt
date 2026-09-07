package sqlite

import (
	"context"
	"errors"
	"testing"

	"github.com/giulianotesta7/tkt/internal/domain"
)

func TestCatalogStorePreservesHierarchyAndAssociations(t *testing.T) {
	store := newTestDB(t)
	ctx := context.Background()
	catalog := store.CatalogStore()
	desks := store.DeskStore()
	operations := &domain.Department{Name: "Operations", Description: "Operations", CreatedAt: testClock}
	if err := catalog.CreateDepartment(ctx, operations); err != nil {
		t.Fatal(err)
	}
	second := &domain.Department{Name: "Second", Description: "Second", CreatedAt: testClock}
	if err := catalog.CreateDepartment(ctx, second); err != nil {
		t.Fatal(err)
	}
	firstDesk := &domain.Desk{Name: "Support", CreatedAt: testClock, DepartmentID: &operations.ID}
	if err := desks.Create(ctx, firstDesk); err != nil {
		t.Fatal(err)
	}
	secondDesk := &domain.Desk{Name: "Support 2", CreatedAt: testClock, DepartmentID: &second.ID}
	if err := desks.Create(ctx, secondDesk); err != nil {
		t.Fatal(err)
	}
	category := &domain.Category{Name: "Requests", Description: "Requests", DeskID: firstDesk.ID, CreatedAt: testClock}
	if err := store.CategoryStore().Create(ctx, category); err != nil {
		t.Fatal(err)
	}
	departments, err := catalog.ListDepartments(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, department := range departments {
		if department.ID == operations.ID && (department.DeskCount != 1 || department.CategoryCount != 1) {
			t.Fatalf("operations counts = desks %d/categories %d", department.DeskCount, department.CategoryCount)
		}
	}
	listed, err := catalog.ListCatalogCategories(ctx, firstDesk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].ID != category.ID || listed[0].DepartmentID != operations.ID || listed[0].DeskName != "Support" {
		t.Fatalf("listed category = %+v", listed)
	}
	searched, err := catalog.SearchCatalog(ctx, "requests")
	if err != nil || len(searched) != 1 || searched[0].ID != category.ID {
		t.Fatalf("search result = %+v, err=%v", searched, err)
	}
	if err := catalog.MoveCategory(ctx, category.ID, secondDesk.ID); err != nil {
		t.Fatal(err)
	}
	moved, err := store.CategoryStore().GetByID(ctx, category.ID)
	if err != nil {
		t.Fatal(err)
	}
	if moved.DeskID != secondDesk.ID {
		t.Fatalf("moved category desk = %d, want %d", moved.DeskID, secondDesk.ID)
	}
}

func TestCatalogStoreListsLegacyDesksAsUnassigned(t *testing.T) {
	store := newTestDB(t)
	ctx := context.Background()
	result, err := store.db.ExecContext(ctx, `INSERT INTO desks(name, description, department_id, created_at) VALUES(?, ?, NULL, ?)`, "Legacy desk", "Legacy description", formatTime(testClock))
	if err != nil {
		t.Fatalf("insert legacy desk: %v", err)
	}
	deskID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("legacy desk id: %v", err)
	}
	category := &domain.Category{Name: "Legacy category", Description: "Legacy category description", DeskID: deskID, CreatedAt: testClock}
	if err := store.CategoryStore().Create(ctx, category); err != nil {
		t.Fatalf("create legacy category: %v", err)
	}
	desks, err := store.CatalogStore().ListDesks(ctx, 0)
	if err != nil {
		t.Fatalf("list unassigned desks: %v", err)
	}
	if len(desks) != 1 || desks[0].ID != deskID || desks[0].Description != "Legacy description" || desks[0].DepartmentID != 0 || desks[0].DepartmentName != "Unassigned" || desks[0].CategoryCount != 1 {
		t.Fatalf("unassigned desks = %+v, want legacy desk and category", desks)
	}
}

func TestCatalogStoreRejectsDuplicateAndBrokenReferences(t *testing.T) {
	store := newTestDB(t)
	ctx := context.Background()
	catalog := store.CatalogStore()
	department := &domain.Department{Name: "Operations", CreatedAt: testClock}
	if err := catalog.CreateDepartment(ctx, department); err != nil {
		t.Fatal(err)
	}
	first := &domain.Desk{Name: "Support", CreatedAt: testClock, DepartmentID: &department.ID}
	if err := store.DeskStore().Create(ctx, first); err != nil {
		t.Fatal(err)
	}
	duplicate := &domain.Desk{Name: first.Name, CreatedAt: testClock, DepartmentID: &department.ID}
	if err := store.DeskStore().Create(ctx, duplicate); !errors.Is(err, domain.ErrDuplicate) {
		t.Fatalf("duplicate desk error = %v", err)
	}
	missingDepartment := int64(999999)
	if err := store.DeskStore().Create(ctx, &domain.Desk{Name: "Missing", CreatedAt: testClock, DepartmentID: &missingDepartment}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing department error = %v", err)
	}
	if err := catalog.MoveCategory(ctx, 999999, first.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing category error = %v", err)
	}
	if err := catalog.MoveCategory(ctx, 999999, 999999); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing desk error = %v", err)
	}
	if err := catalog.DeleteDepartment(ctx, department.ID); !errors.Is(err, domain.ErrReferenced) {
		t.Fatalf("referenced department error = %v", err)
	}
}
