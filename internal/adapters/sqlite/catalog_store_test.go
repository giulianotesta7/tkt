package sqlite

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/domain"
)

func TestCatalogStorePreservesHierarchyAndUniqueness(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	catalog := s.CatalogStore()
	now := time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC)

	engineering := &domain.Department{Name: "Engineering", Description: "Engineering requests", CreatedAt: now}
	if err := catalog.CreateDepartment(ctx, engineering); err != nil {
		t.Fatalf("create department: %v", err)
	}
	facilities := &domain.Department{Name: "Facilities", Description: "Facilities requests", CreatedAt: now}
	if err := catalog.CreateDepartment(ctx, facilities); err != nil {
		t.Fatalf("create second department: %v", err)
	}

	software := &domain.Area{DepartmentID: engineering.ID, Name: "Software", Description: "Software access", CreatedAt: now}
	if err := catalog.CreateArea(ctx, software); err != nil {
		t.Fatalf("create area: %v", err)
	}
	duplicateArea := &domain.Area{DepartmentID: engineering.ID, Name: "Software", Description: "Duplicate", CreatedAt: now}
	if err := catalog.CreateArea(ctx, duplicateArea); !errors.Is(err, domain.ErrDuplicate) {
		t.Fatalf("duplicate area error = %v, want ErrDuplicate", err)
	}
	otherSoftware := &domain.Area{DepartmentID: facilities.ID, Name: "Software", Description: "Facilities software", CreatedAt: now}
	if err := catalog.CreateArea(ctx, otherSoftware); err != nil {
		t.Fatalf("same area name in another department: %v", err)
	}

	category := &domain.Category{Name: "VPN access", Description: "Remote access", AreaID: software.ID, CreatedAt: now}
	if err := s.CategoryStore().Create(ctx, category); err != nil {
		t.Fatalf("create category: %v", err)
	}
	resolved, err := catalog.ListCatalogCategories(ctx, software.ID)
	if err != nil {
		t.Fatalf("list catalog categories: %v", err)
	}
	if len(resolved) != 1 || resolved[0].ID != category.ID || resolved[0].DepartmentName != engineering.Name || resolved[0].AreaName != software.Name {
		t.Fatalf("resolved category = %+v, want category with Engineering/Software context", resolved)
	}
	if resolved[0].Description != category.Description {
		t.Fatalf("resolved description = %q, want %q", resolved[0].Description, category.Description)
	}

	if err := catalog.DeleteArea(ctx, software.ID); !errors.Is(err, domain.ErrReferenced) {
		t.Fatalf("delete non-empty area error = %v, want ErrReferenced", err)
	}
	if err := catalog.DeleteDepartment(ctx, engineering.ID); !errors.Is(err, domain.ErrReferenced) {
		t.Fatalf("delete non-empty department error = %v, want ErrReferenced", err)
	}
	if err := catalog.MoveCategory(ctx, category.ID, otherSoftware.ID); err != nil {
		t.Fatalf("move category: %v", err)
	}
	if err := catalog.DeleteArea(ctx, software.ID); err != nil {
		t.Fatalf("delete emptied area: %v", err)
	}
	if err := catalog.DeleteDepartment(ctx, engineering.ID); err != nil {
		t.Fatalf("delete emptied department: %v", err)
	}
}

func TestCatalogStoreRejectsNullCategoryArea(t *testing.T) {
	s := newTestDB(t)
	_, err := s.db.ExecContext(context.Background(), `INSERT INTO categories (name, description, created_at) VALUES (?, ?, ?)`, "No area", "invalid", "2026-08-06T10:00:00Z")
	if err == nil {
		t.Fatal("category without area must be rejected")
	}
}
