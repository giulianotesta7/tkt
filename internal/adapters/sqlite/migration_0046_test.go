package sqlite

import (
	"context"
	"reflect"
	"testing"

	"github.com/giulianotesta7/tkt/internal/domain"
)

func TestMigration0046PreservesLegacyRows(t *testing.T) {
	ctx := context.Background()
	s, err := openDSN(testDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	s.db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = s.db.Close() })
	if err := migrate(ctx, s.db, migrationsBefore(t, "0046_allow_categories_without_desk.sql", false)); err != nil {
		t.Fatal(err)
	}
	d := &domain.Desk{Name: "Legacy", Description: "Keep me", CreatedAt: testClock}
	if err := s.DeskStore().Create(ctx, d); err != nil {
		t.Fatal(err)
	}
	c := &domain.Category{Name: "Legacy category", Description: "Keep description", Keywords: "keep", CreatedAt: testClock}
	createLegacyCategory(t, s, c, d.ID)
	beforeDesk := categoryDeskRaw(t, s, c.ID)
	beforeCategories, err := s.CategoryStore().List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	beforeDesks, err := s.DeskStore().List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := migrate(ctx, s.db, migrationsBefore(t, "0046_allow_categories_without_desk.sql", true)); err != nil {
		t.Fatal(err)
	}
	afterCategories, err := s.CategoryStore().List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	afterDesks, err := s.DeskStore().List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(beforeCategories, afterCategories) || !reflect.DeepEqual(beforeDesks, afterDesks) {
		t.Fatal("migration changed legacy rows")
	}
	if afterDesk := categoryDeskRaw(t, s, c.ID); afterDesk != beforeDesk || afterDesk.Int64 != d.ID {
		t.Fatalf("legacy desk = %+v, want %d preserved", afterDesk, d.ID)
	}
	assertSchemaObjectAbsent(t, s, "categories_desk_required_insert")
	assertSchemaObjectAbsent(t, s, "categories_desk_required_update")
	if n := countRows(t, s, `SELECT COUNT(*) FROM schema_migrations WHERE version=46`); n != 1 {
		t.Fatalf("version 46 count = %d", n)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM pragma_table_info('categories') WHERE name='desk_id' AND "notnull"=0`); n != 1 {
		t.Fatal("desk column must remain nullable")
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM pragma_foreign_key_list('categories') WHERE "from"='desk_id' AND "table"='desks'`); n != 1 {
		t.Fatal("desk foreign key lost")
	}
}

func TestCategoryStoreDesklessLifecycle(t *testing.T) {
	for _, noDesks := range []bool{false, true} {
		t.Run(map[bool]string{false: "with General", true: "without desks"}[noDesks], func(t *testing.T) {
			s := newTestDB(t)
			ctx := context.Background()
			if noDesks {
				desks, err := s.DeskStore().List(ctx)
				if err != nil {
					t.Fatal(err)
				}
				for _, d := range desks {
					if err := s.DeskStore().Delete(ctx, d.ID); err != nil {
						t.Fatal(err)
					}
				}
			}
			cs := s.CategoryStore()
			c := &domain.Category{Name: "Deskless", Description: "Find this description", CreatedAt: testClock}
			if err := cs.Create(ctx, c); err != nil {
				t.Fatal(err)
			}
			got, err := cs.GetByID(ctx, c.ID)
			if err != nil || got.ID != c.ID {
				t.Fatalf("get = %+v, %v", got, err)
			}
			all, err := cs.List(ctx)
			if err != nil || len(all) != 1 || all[0].ID != c.ID {
				t.Fatalf("list = %+v, %v", all, err)
			}
			for _, q := range []string{"", "DESKLESS", "description"} {
				found, err := s.CatalogStore().SearchCatalog(ctx, q)
				if err != nil || len(found) != 1 || found[0].ID != c.ID {
					t.Fatalf("search %q = %+v, %v", q, found, err)
				}
			}
			c.Name = "Renamed"
			if err := cs.Update(ctx, c); err != nil {
				t.Fatal(err)
			}
			got, err = cs.GetByID(ctx, c.ID)
			if err != nil || got.Name != "Renamed" {
				t.Fatalf("update = %+v, %v", got, err)
			}
			definition := mustCanon(t, domain.WorkflowDefinition{{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "Handle"}}})
			if _, issues, err := s.WorkflowStore().Publish(ctx, c.ID, definition, nil); err != nil || len(issues) != 0 {
				t.Fatalf("publish: %v %v", err, issues)
			}
			available, err := s.WorkflowStore().ListAvailableCategories(ctx)
			if err != nil || len(available) != 1 || available[0].ID != c.ID {
				t.Fatalf("available = %+v, %v", available, err)
			}
		})
	}
}

// A category names no desk (issue #491): on the 0047 schema, the last one with
// the column, creating through the store writes a NULL legacy desk, an edit
// through the store leaves an existing legacy desk untouched, and the column
// keeps its foreign key until 0048 drops it.
func TestCategoryStoreLegacyDeskAndForeignKey(t *testing.T) {
	s := openThrough0047(t)
	ctx := context.Background()
	d := &domain.Desk{Name: "Explicit", CreatedAt: testClock}
	if err := s.DeskStore().Create(ctx, d); err != nil {
		t.Fatal(err)
	}
	cs := s.CategoryStore()
	created := &domain.Category{Name: "Created", CreatedAt: testClock}
	if err := cs.Create(ctx, created); err != nil {
		t.Fatal(err)
	}
	if desk := categoryDeskRaw(t, s, created.ID); desk.Valid {
		t.Fatalf("create wrote desk %d, want NULL", desk.Int64)
	}
	c := &domain.Category{Name: "Owned", CreatedAt: testClock}
	createLegacyCategory(t, s, c, d.ID)
	c.Name, c.Description, c.Keywords = "Edited", "Edited description", "edited"
	if err := cs.Update(ctx, c); err != nil {
		t.Fatal(err)
	}
	before, err := cs.GetByID(ctx, c.ID)
	if err != nil || before.Name != "Edited" || before.Description != "Edited description" || before.Keywords != "edited" {
		t.Fatalf("legacy update = %+v, %v", before, err)
	}
	if desk := categoryDeskRaw(t, s, c.ID); !desk.Valid || desk.Int64 != d.ID {
		t.Fatalf("legacy desk after update = %+v, want %d", desk, d.ID)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE categories SET desk_id = 99999 WHERE id = ?`, c.ID); err == nil {
		t.Fatal("missing desk accepted")
	}
	after, err := cs.GetByID(ctx, c.ID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("rejected update changed row: %+v, %v", after, err)
	}
	if desk := categoryDeskRaw(t, s, c.ID); !desk.Valid || desk.Int64 != d.ID {
		t.Fatalf("rejected update changed legacy desk: %+v", desk)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO categories (name, desk_id, created_at) VALUES ('Invalid', 99999, ?)`, formatTime(testClock)); err == nil {
		t.Fatal("invalid create accepted")
	}
	all, err := cs.List(ctx)
	if err != nil || len(all) != 2 {
		t.Fatalf("rejected create changed list: %+v, %v", all, err)
	}
}
