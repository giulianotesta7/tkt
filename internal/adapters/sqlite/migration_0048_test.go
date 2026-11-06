package sqlite

import (
	"context"
	"fmt"
	"reflect"
	"regexp"
	"testing"
	"testing/fstest"

	"github.com/giulianotesta7/tkt/internal/domain"
)

const migration0048 = "0048_drop_category_desk.sql"

// openThrough0047 opens a database migrated up to, but not including, 0048: the
// last schema in which categories still carry a legacy desk_id.
func openThrough0047(t *testing.T) *Store {
	t.Helper()
	s, err := openDSN(testDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	s.db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = s.db.Close() })
	if err := migrate(context.Background(), s.db, migrationsBefore(t, migration0048, false)); err != nil {
		t.Fatalf("upgrade through 0047: %v", err)
	}
	return s
}

// rows0048 renders every row of query as text, in query order.
func rows0048(t *testing.T, s *Store, query string) []string {
	t.Helper()
	rows, err := s.db.Query(query)
	if err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		out = append(out, fmt.Sprintf("%v", vals))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// preserved0048 lists every row 0048 must leave untouched. Categories are read
// without desk_id, the only column the migration removes.
var preserved0048 = []string{
	`SELECT id, name, description, keywords, folder_id, created_at FROM categories ORDER BY id`,
	`SELECT * FROM desks ORDER BY id`,
	`SELECT * FROM folders ORDER BY id`,
	`SELECT * FROM tickets ORDER BY id`,
	`SELECT * FROM workflow_versions ORDER BY id`,
	`SELECT * FROM category_workflows ORDER BY category_id`,
	`SELECT * FROM ticket_workflow_runs ORDER BY ticket_id`,
	`SELECT * FROM sla_policies ORDER BY category_id, priority`,
	`SELECT * FROM ticket_sla ORDER BY ticket_id`,
	`SELECT rowid, name, keywords, description, desk, folders FROM category_suggest_fts ORDER BY rowid`,
	`SELECT rowid, name, keywords FROM category_suggest_trigram ORDER BY rowid`,
}

func TestMigration0048ArchivesCategoryDesks(t *testing.T) {
	ctx := context.Background()
	s := openThrough0047(t)
	support := &domain.Desk{Name: "Support", CreatedAt: testClock}
	network := &domain.Desk{Name: "Old network", CreatedAt: testClock}
	for _, d := range []*domain.Desk{support, network} {
		if err := s.DeskStore().Create(ctx, d); err != nil {
			t.Fatal(err)
		}
	}
	hardware := createFolder(t, newFolderStore(s.db), nil, "Hardware")
	printers := &domain.Category{Name: "Printers", Description: "toner", Keywords: "impresora", FolderID: &hardware, CreatedAt: testClock}
	createLegacyCategory(t, s, printers, support.ID)
	routers := &domain.Category{Name: "Routers", CreatedAt: testClock}
	createLegacyCategory(t, s, routers, network.ID)
	deskless := &domain.Category{Name: "Deskless", CreatedAt: testClock}
	if err := s.CategoryStore().Create(ctx, deskless); err != nil {
		t.Fatal(err)
	}
	// The archive keeps the desk's name at migration time, not its first name.
	network.Name = "Network"
	if err := s.DeskStore().Update(ctx, network); err != nil {
		t.Fatal(err)
	}
	version := seedPublished(t, s, printers.ID, domain.WorkflowDefinition{{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "Handle"}}})
	tk := seedTicket(t, s, domain.Ticket{Number: 1, Title: "Jammed", CategoryID: printers.ID,
		Priority: domain.PriorityHigh, State: domain.StateNew, CreatedAt: testClock, UpdatedAt: testClock})
	mustExec(t, s, `UPDATE tickets SET workflow_version_id = ? WHERE id = ?`, version, tk.ID)
	mustExec(t, s, `INSERT INTO ticket_workflow_runs (ticket_id, current_step_index, status, started_at) VALUES (?, 0, 'active', ?)`, tk.ID, formatTime(testClock))
	mustExec(t, s, `INSERT INTO ticket_sla (ticket_id, first_response_seconds, resolve_seconds, started_at, policy_snapshot_at) VALUES (?, 3600, 7200, ?, ?)`, tk.ID, formatTime(testClock), formatTime(testClock))

	before := map[string][]string{}
	for _, q := range preserved0048 {
		before[q] = rows0048(t, s, q)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	for _, q := range preserved0048 {
		if after := rows0048(t, s, q); !reflect.DeepEqual(before[q], after) {
			t.Errorf("%s changed:\nbefore %v\nafter  %v", q, before[q], after)
		}
	}
	archived := rows0048(t, s, `SELECT category_id, desk_id, desk_name FROM category_desk_archive ORDER BY category_id`)
	want := []string{fmt.Sprintf("[%d %d Support]", printers.ID, support.ID), fmt.Sprintf("[%d %d Network]", routers.ID, network.ID)}
	if !reflect.DeepEqual(archived, want) {
		t.Fatalf("archive = %v, want %v", archived, want)
	}
	utc := regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$`)
	for _, at := range rows0048(t, s, `SELECT archived_at FROM category_desk_archive`) {
		if !utc.MatchString(at[1 : len(at)-1]) {
			t.Errorf("archived_at %s is not UTC RFC3339", at)
		}
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM pragma_table_info('categories') WHERE name='desk_id'`); n != 0 {
		t.Fatal("categories.desk_id still present")
	}
	assertSchemaObjectAbsent(t, s, "idx_categories_desk")
	if n := countRows(t, s, `SELECT COUNT(*) FROM pragma_foreign_key_list('category_desk_archive')`); n != 0 {
		t.Fatal("archive must not reference categories or desks")
	}
	if fk := rows0048(t, s, `PRAGMA foreign_key_check`); len(fk) != 0 {
		t.Fatalf("foreign_key_check: %v", fk)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM schema_migrations WHERE version=48`); n != 1 {
		t.Fatalf("version 48 count = %d", n)
	}

	// Suggestion and SLA matrix triggers still fire on the rebuilt shape.
	scanners := &domain.Category{Name: "Scanners", Keywords: "digitalizar", CreatedAt: testClock}
	if err := s.CategoryStore().Create(ctx, scanners); err != nil {
		t.Fatal(err)
	}
	assertSuggestion0047(t, s, scanners.ID, "scanners digitalizar", "scanners|digitalizar")
	if got, defaults := countRows(t, s, `SELECT COUNT(*) FROM sla_policies WHERE category_id=?`, scanners.ID), countRows(t, s, `SELECT COUNT(*) FROM sla_defaults`); got != defaults || got == 0 {
		t.Fatalf("SLA matrix rows = %d, want %d", got, defaults)
	}
	scanners.Name = "Copiers"
	if err := s.CategoryStore().Update(ctx, scanners); err != nil {
		t.Fatal(err)
	}
	assertSuggestion0047(t, s, scanners.ID, "scanners", "")
	assertSuggestion0047(t, s, scanners.ID, "copiers", "copiers")
	if err := s.CategoryStore().Delete(ctx, scanners.ID); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM category_suggest_fts WHERE rowid=?`, scanners.ID); n != 0 {
		t.Fatal("deleted category still indexed")
	}

	// The archive outlives the category and the desk it names.
	if err := s.CategoryStore().Delete(ctx, routers.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeskStore().Delete(ctx, network.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeskStore().Delete(ctx, support.ID); err != nil {
		t.Fatal(err)
	}
	if got := rows0048(t, s, `SELECT category_id, desk_id, desk_name FROM category_desk_archive ORDER BY category_id`); !reflect.DeepEqual(got, want) {
		t.Fatalf("archive after deletes = %v, want %v", got, want)
	}
}

func TestMigration0048FreshInstall(t *testing.T) {
	s := newTestDB(t)
	if n := countRows(t, s, `SELECT COUNT(*) FROM category_desk_archive`); n != 0 {
		t.Fatalf("fresh archive rows = %d", n)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM pragma_table_info('categories') WHERE name='desk_id'`); n != 0 {
		t.Fatal("fresh categories.desk_id present")
	}
	assertSchemaObjectAbsent(t, s, "idx_categories_desk")
}

func TestMigration0048Rollback(t *testing.T) {
	ctx := context.Background()
	s := openThrough0047(t)
	d := &domain.Desk{Name: "Support", CreatedAt: testClock}
	if err := s.DeskStore().Create(ctx, d); err != nil {
		t.Fatal(err)
	}
	c := &domain.Category{Name: "Printers", CreatedAt: testClock}
	createLegacyCategory(t, s, c, d.ID)
	data, err := migrationsFS.ReadFile("migrations/" + migration0048)
	if err != nil {
		t.Fatal(err)
	}
	broken := fstest.MapFS{"migrations/0048_broken.sql": &fstest.MapFile{Data: append(data, []byte("\nINVALID SQL;")...)}}
	if err := migrate(ctx, s.db, broken); err == nil {
		t.Fatal("broken migration succeeded")
	}
	if desk := categoryDeskRaw(t, s, c.ID); !desk.Valid || desk.Int64 != d.ID {
		t.Fatalf("legacy desk = %+v, want %d", desk, d.ID)
	}
	assertSchemaObjectPresent(t, s, "idx_categories_desk")
	assertSchemaObjectAbsent(t, s, "category_desk_archive")
	if n := countRows(t, s, `SELECT COUNT(*) FROM schema_migrations WHERE version=48`); n != 0 {
		t.Fatal("failed migration recorded")
	}
}
