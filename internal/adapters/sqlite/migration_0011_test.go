package sqlite

import (
	"context"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/giulianotesta7/tkt/internal/domain"
)

func TestMigration0011CatalogCompatibility(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	for _, table := range []string{"departments", "areas"} {
		var n int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Fatalf("%s table missing", table)
		}
	}
	var departmentID, areaID int64
	if err := s.db.QueryRow(`SELECT id FROM departments WHERE name='General'`).Scan(&departmentID); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT id FROM areas WHERE department_id=? AND name='General'`, departmentID).Scan(&areaID); err != nil {
		t.Fatal(err)
	}
	cat := &domain.Category{Name: "Migrated-compatible", Description: "Legacy description", CreatedAt: time.Now().UTC()}
	if err := s.CategoryStore().Create(ctx, cat); err != nil {
		t.Fatal(err)
	}
	if cat.AreaID != areaID {
		t.Fatalf("default category area = %d, want %d", cat.AreaID, areaID)
	}
	var description string
	if err := s.db.QueryRow(`SELECT description FROM categories WHERE id=?`, cat.ID).Scan(&description); err != nil {
		t.Fatal(err)
	}
	if description != cat.Description {
		t.Fatalf("description = %q, want %q", description, cat.Description)
	}
}

func TestMigration0011MigratesExistingCategories(t *testing.T) {
	ctx := context.Background()
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		t.Fatalf("read embedded migrations: %v", err)
	}
	pre0011FS := fstest.MapFS{}
	for _, entry := range entries {
		if strings.Compare(entry.Name(), "0011_ticket_catalog_hierarchy.sql") >= 0 {
			continue
		}
		blob, err := fs.ReadFile(migrationsFS, "migrations/"+entry.Name())
		if err != nil {
			t.Fatalf("read %s: %v", entry.Name(), err)
		}
		pre0011FS["migrations/"+entry.Name()] = &fstest.MapFile{Data: blob}
	}

	pre, err := openDSN(testDSN(t))
	if err != nil {
		t.Fatalf("open pre-0011 database: %v", err)
	}
	pre.db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = pre.db.Close() })
	if err := migrate(ctx, pre.db, pre0011FS); err != nil {
		t.Fatalf("migrate pre-0011 database: %v", err)
	}

	createdAt := "2026-08-06T10:00:00Z"
	if _, err := pre.db.ExecContext(ctx, `INSERT INTO categories (name, created_at) VALUES (?, ?)`, "Legacy catalog", createdAt); err != nil {
		t.Fatalf("insert legacy category: %v", err)
	}
	var categoryID int64
	if err := pre.db.QueryRowContext(ctx, `SELECT id FROM categories WHERE name=?`, "Legacy catalog").Scan(&categoryID); err != nil {
		t.Fatalf("read legacy category: %v", err)
	}
	if _, err := pre.db.ExecContext(ctx, `INSERT INTO tickets (number,title,description,requester_name,requester_email,category_id,priority,state,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?,?)`, 991, "Legacy ticket", "", "Requester", "requester@example.com", categoryID, "medium", "new", createdAt, createdAt); err != nil {
		t.Fatalf("insert legacy ticket: %v", err)
	}
	if _, err := pre.db.ExecContext(ctx, `INSERT INTO workflow_versions (category_id,version_no,steps_json,published_at) VALUES (?,?,?,?)`, categoryID, 1, `[{"type":"manual_task","manual_task":{"instructions":"handle"}}]`, createdAt); err != nil {
		t.Fatalf("insert legacy workflow: %v", err)
	}

	if err := migrate(ctx, pre.db, migrationsFS); err != nil {
		t.Fatalf("upgrade to 0011: %v", err)
	}

	var departmentID, areaID int64
	if err := pre.db.QueryRowContext(ctx, `SELECT id FROM departments WHERE name='General'`).Scan(&departmentID); err != nil {
		t.Fatalf("read General department: %v", err)
	}
	if err := pre.db.QueryRowContext(ctx, `SELECT id FROM areas WHERE department_id=? AND name='General'`, departmentID).Scan(&areaID); err != nil {
		t.Fatalf("read General area: %v", err)
	}
	var migratedAreaID int64
	var description, migratedCreatedAt string
	if err := pre.db.QueryRowContext(ctx, `SELECT area_id, description, created_at FROM categories WHERE id=?`, categoryID).Scan(&migratedAreaID, &description, &migratedCreatedAt); err != nil {
		t.Fatalf("read migrated category: %v", err)
	}
	if migratedAreaID != areaID {
		t.Fatalf("migrated area_id = %d, want %d", migratedAreaID, areaID)
	}
	if description != "" {
		t.Fatalf("migrated description = %q, want empty string", description)
	}
	if migratedCreatedAt != createdAt {
		t.Fatalf("migrated created_at = %q, want %q", migratedCreatedAt, createdAt)
	}
	var ticketCategoryID, workflowCategoryID int64
	if err := pre.db.QueryRowContext(ctx, `SELECT category_id FROM tickets WHERE number=991`).Scan(&ticketCategoryID); err != nil {
		t.Fatalf("read ticket reference: %v", err)
	}
	if err := pre.db.QueryRowContext(ctx, `SELECT category_id FROM workflow_versions WHERE category_id=?`, categoryID).Scan(&workflowCategoryID); err != nil {
		t.Fatalf("read workflow reference: %v", err)
	}
	if ticketCategoryID != categoryID || workflowCategoryID != categoryID {
		t.Fatalf("references changed: ticket=%d workflow=%d category=%d", ticketCategoryID, workflowCategoryID, categoryID)
	}
}
