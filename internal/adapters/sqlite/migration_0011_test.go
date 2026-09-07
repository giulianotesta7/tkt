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

func pre0011Migrations(t *testing.T) fstest.MapFS {
	t.Helper()
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		t.Fatalf("read embedded migrations: %v", err)
	}
	out := fstest.MapFS{}
	for _, entry := range entries {
		if strings.Compare(entry.Name(), "0011_ticket_catalog_hierarchy.sql") >= 0 {
			continue
		}
		blob, err := fs.ReadFile(migrationsFS, "migrations/"+entry.Name())
		if err != nil {
			t.Fatalf("read %s: %v", entry.Name(), err)
		}
		out["migrations/"+entry.Name()] = &fstest.MapFile{Data: blob}
	}
	return out
}

func TestMigration0011CatalogCompatibility(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	for _, table := range []string{"departments", "desks"} {
		var n int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Fatalf("%s table missing", table)
		}
	}
	var areas int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='areas'`).Scan(&areas); err != nil {
		t.Fatal(err)
	}
	if areas != 0 {
		t.Fatal("Area table must not be introduced")
	}
	var notNull int
	if err := s.db.QueryRow(`SELECT "notnull" FROM pragma_table_info('desks') WHERE name='department_id'`).Scan(&notNull); err != nil {
		t.Fatal(err)
	}
	if notNull != 0 {
		t.Fatal("legacy Desk department_id must be nullable")
	}
	var descriptionNotNull int
	if err := s.db.QueryRow(`SELECT "notnull" FROM pragma_table_info('desks') WHERE name='description'`).Scan(&descriptionNotNull); err != nil {
		t.Fatal(err)
	}
	if descriptionNotNull != 1 {
		t.Fatal("Desk description must be present with an empty default")
	}
	var departmentID, deskID int64
	if err := s.db.QueryRow(`SELECT id FROM departments WHERE name='General'`).Scan(&departmentID); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT id FROM desks WHERE department_id=? AND name='General'`, departmentID).Scan(&deskID); err != nil {
		t.Fatal(err)
	}
	cat := &domain.Category{Name: "Migrated-compatible", Description: "Legacy description", CreatedAt: time.Now().UTC()}
	if err := s.CategoryStore().Create(ctx, cat); err != nil {
		t.Fatal(err)
	}
	if cat.DeskID != deskID {
		t.Fatalf("default category desk = %d, want %d", cat.DeskID, deskID)
	}
	var description string
	if err := s.db.QueryRow(`SELECT description FROM categories WHERE id=?`, cat.ID).Scan(&description); err != nil {
		t.Fatal(err)
	}
	if description != cat.Description {
		t.Fatalf("description = %q, want %q", description, cat.Description)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO categories (name, desk_id, created_at) VALUES ('missing-desk', NULL, ?)`, time.Now().UTC().Format(time.RFC3339)); err == nil {
		t.Fatal("category without a desk must be rejected")
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO categories (name, desk_id, created_at) VALUES ('unknown-desk', 999999, ?)`, time.Now().UTC().Format(time.RFC3339)); err == nil {
		t.Fatal("category with an unknown desk must be rejected")
	}
}

func TestMigration0011UpgradePreservesLegacyIDsAndAssociations(t *testing.T) {
	ctx := context.Background()
	pre, err := openDSN(testDSN(t))
	if err != nil {
		t.Fatalf("open pre-0011 db: %v", err)
	}
	pre.db.SetMaxOpenConns(1)
	t.Cleanup(func() { pre.db.Close() })
	if err := migrate(ctx, pre.db, pre0011Migrations(t)); err != nil {
		t.Fatalf("migrate pre-0011 db: %v", err)
	}

	legacyCategoryID := seedCategory(t, pre, "Legacy requests")
	otherCategoryID := seedCategory(t, pre, "Other requests")
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	requesterID := seedUser(t, pre, "Legacy requester", "legacy-migration@example.com", true)
	ticket := seedTicket(t, pre, domain.Ticket{
		Number: 1801, Title: "Legacy ticket", CategoryID: legacyCategoryID,
		Priority: domain.PriorityMedium, State: domain.StateNew,
		RequesterName: "Legacy requester", RequesterEmail: "legacy-migration@example.com",
		RequesterUserID: &requesterID, CreatedAt: now, UpdatedAt: now,
	})
	if _, err := pre.db.ExecContext(ctx, `INSERT INTO workflow_versions (category_id, version_no, steps_json, published_at) VALUES (?, 1, ?, ?)`, legacyCategoryID, `[{"type":"manual_task","manual_task":{"instructions":"Review"}}]`, formatTime(now)); err != nil {
		t.Fatalf("seed legacy workflow version: %v", err)
	}
	var versionID int64
	if err := pre.db.QueryRowContext(ctx, `SELECT id FROM workflow_versions WHERE category_id=? AND version_no=1`, legacyCategoryID).Scan(&versionID); err != nil {
		t.Fatalf("read legacy workflow version: %v", err)
	}
	if _, err := pre.db.ExecContext(ctx, `UPDATE tickets SET workflow_version_id=? WHERE id=?`, versionID, ticket.ID); err != nil {
		t.Fatalf("associate legacy ticket workflow: %v", err)
	}
	if _, err := pre.db.ExecContext(ctx, `INSERT INTO category_workflows (category_id, draft_json, current_version_id) VALUES (?, ?, ?)`, legacyCategoryID, `[{"type":"manual_task"}]`, versionID); err != nil {
		t.Fatalf("seed category workflow: %v", err)
	}

	if err := migrate(ctx, pre.db, migrationsFS); err != nil {
		t.Fatalf("upgrade pre-0011 db: %v", err)
	}
	if err := migrate(ctx, pre.db, migrationsFS); err != nil {
		t.Fatalf("rerun 0011 upgrade: %v", err)
	}

	var migratedLegacy, migratedOther int64
	if err := pre.db.QueryRowContext(ctx, `SELECT id FROM categories WHERE name=?`, "Legacy requests").Scan(&migratedLegacy); err != nil {
		t.Fatal(err)
	}
	if err := pre.db.QueryRowContext(ctx, `SELECT id FROM categories WHERE name=?`, "Other requests").Scan(&migratedOther); err != nil {
		t.Fatal(err)
	}
	if migratedLegacy != legacyCategoryID || migratedOther != otherCategoryID {
		t.Fatalf("category IDs changed: legacy=%d/%d other=%d/%d", migratedLegacy, legacyCategoryID, migratedOther, otherCategoryID)
	}
	var ticketCategory, ticketWorkflow int64
	if err := pre.db.QueryRowContext(ctx, `SELECT category_id, workflow_version_id FROM tickets WHERE id=?`, ticket.ID).Scan(&ticketCategory, &ticketWorkflow); err != nil {
		t.Fatal(err)
	}
	if ticketCategory != legacyCategoryID || ticketWorkflow != versionID {
		t.Fatalf("legacy ticket associations = category %d/workflow %d, want %d/%d", ticketCategory, ticketWorkflow, legacyCategoryID, versionID)
	}
	var workflowCategory int64
	if err := pre.db.QueryRowContext(ctx, `SELECT category_id FROM workflow_versions WHERE id=?`, versionID).Scan(&workflowCategory); err != nil {
		t.Fatal(err)
	}
	if workflowCategory != legacyCategoryID {
		t.Fatalf("workflow category = %d, want %d", workflowCategory, legacyCategoryID)
	}
	var deskID, departmentID int64
	if err := pre.db.QueryRowContext(ctx, `SELECT desk_id FROM categories WHERE id=?`, legacyCategoryID).Scan(&deskID); err != nil {
		t.Fatal(err)
	}
	if err := pre.db.QueryRowContext(ctx, `SELECT department_id FROM desks WHERE id=?`, deskID).Scan(&departmentID); err != nil {
		t.Fatal(err)
	}
	if deskID == 0 || departmentID == 0 {
		t.Fatalf("legacy category was not attached to compatibility hierarchy: department=%d desk=%d", departmentID, deskID)
	}
	var applied int
	if err := pre.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version=11`).Scan(&applied); err != nil {
		t.Fatal(err)
	}
	if applied != 1 {
		t.Fatalf("schema_migrations version=11 rows = %d, want 1", applied)
	}
}
