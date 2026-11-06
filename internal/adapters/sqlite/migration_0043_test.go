package sqlite

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// Migration 0043 tests (catalog folders — issue #484): the folder tree is
// created with its invariants in the database, and an existing instance's
// Department -> Desk -> Category catalog is copied into it without losing or
// rewriting anything (option A): departments become top-level folders, desks
// become folders inside their department's folder, legacy desks with no
// department become top-level folders, and every category moves into its
// desk's folder.

// openPre0043 opens a database with every migration before 0043 applied, so a
// test can seed the shape an existing instance has before upgrading it.
func openPre0043(t *testing.T) *Store {
	t.Helper()
	s, err := openDSN(testDSN(t))
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	s.db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = s.db.Close() })
	if err := migrate(context.Background(), s.db, migrationsBefore(t, "0043_catalog_folders.sql", false)); err != nil {
		t.Fatalf("migrate before 0043: %v", err)
	}
	return s
}

func upgradeTo0043(t *testing.T, s *Store) {
	t.Helper()
	// 0045 drops the departments these tests compare against, so the upgrade
	// stops before it.
	if err := migrate(context.Background(), s.db, migrationsBefore(t, "0045_drop_departments.sql", false)); err != nil {
		t.Fatalf("upgrade through 0043: %v", err)
	}
}

func mustExec(t *testing.T, s *Store, query string, args ...any) {
	t.Helper()
	if _, err := s.db.Exec(query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

// folderRow is one folders row with its parent resolved to a name, so a test
// can state the expected tree without knowing generated ids.
type folderRow struct {
	Path        string
	Description string
	CreatedAt   string
}

// folderTree reads every folder as "Parent / Child" paths, in path order.
func folderTree(t *testing.T, s *Store) []folderRow {
	t.Helper()
	rows, err := s.db.Query(`WITH RECURSIVE p(id, path) AS (
		SELECT id, name FROM folders WHERE parent_id IS NULL
		UNION ALL
		SELECT f.id, p.path || ' / ' || f.name FROM folders f JOIN p ON f.parent_id = p.id)
		SELECT p.path, f.description, f.created_at FROM p JOIN folders f ON f.id = p.id ORDER BY p.path`)
	if err != nil {
		t.Fatalf("read folder tree: %v", err)
	}
	defer rows.Close()
	var out []folderRow
	for rows.Next() {
		var r folderRow
		if err := rows.Scan(&r.Path, &r.Description, &r.CreatedAt); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// seedCopyPaths lists the paths of the folders 0043 recorded as copies of the
// seeded General department and desk.
func seedCopyPaths(t *testing.T, s *Store) []string {
	t.Helper()
	rows, err := s.db.Query(`WITH RECURSIVE p(id, path) AS (
		SELECT id, name FROM folders WHERE parent_id IS NULL
		UNION ALL
		SELECT f.id, p.path || ' / ' || f.name FROM folders f JOIN p ON f.parent_id = p.id)
		SELECT p.path FROM p JOIN folder_seed_copies c ON c.folder_id = p.id ORDER BY p.path`)
	if err != nil {
		t.Fatalf("read seed copies: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			t.Fatal(err)
		}
		out = append(out, path)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// categoryFolderPaths maps each category name to the path of its folder.
func categoryFolderPaths(t *testing.T, s *Store) map[string]string {
	t.Helper()
	rows, err := s.db.Query(`WITH RECURSIVE p(id, path) AS (
		SELECT id, name FROM folders WHERE parent_id IS NULL
		UNION ALL
		SELECT f.id, p.path || ' / ' || f.name FROM folders f JOIN p ON f.parent_id = p.id)
		SELECT c.name, COALESCE(p.path, '') FROM categories c LEFT JOIN p ON p.id = c.folder_id`)
	if err != nil {
		t.Fatalf("read category folders: %v", err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var name, path string
		if err := rows.Scan(&name, &path); err != nil {
			t.Fatal(err)
		}
		out[name] = path
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// assertFolderNameKeys checks that every folder's name_key is its folded name.
func assertFolderNameKeys(t *testing.T, s *Store) {
	t.Helper()
	rows, err := s.db.Query(`SELECT name, name_key FROM folders`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var name, key string
		if err := rows.Scan(&name, &key); err != nil {
			t.Fatal(err)
		}
		if want := domain.FolderNameKey(name); key != want {
			t.Errorf("folder %q name_key = %q, want %q", name, key, want)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

// TestMigration0043Shape pins the new table, column, indexes and guard.
func TestMigration0043Shape(t *testing.T) {
	s := newTestDB(t)

	type column struct {
		name    string
		typ     string
		notNull int
		pk      int
	}
	rows, err := s.db.Query(`SELECT name, type, "notnull", pk FROM pragma_table_info('folders') ORDER BY cid`)
	if err != nil {
		t.Fatal(err)
	}
	var got []column
	for rows.Next() {
		var c column
		if err := rows.Scan(&c.name, &c.typ, &c.notNull, &c.pk); err != nil {
			t.Fatal(err)
		}
		got = append(got, c)
	}
	rows.Close()
	want := []column{
		{"id", "INTEGER", 0, 1},
		{"parent_id", "INTEGER", 0, 0},
		{"name", "TEXT", 1, 0},
		{"name_key", "TEXT", 1, 0},
		{"description", "TEXT", 1, 0},
		{"created_at", "TEXT", 1, 0},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("folders columns = %+v, want %+v", got, want)
	}

	var typ string
	var notNull int
	if err := s.db.QueryRow(`SELECT type, "notnull" FROM pragma_table_info('categories') WHERE name = 'folder_id'`).Scan(&typ, &notNull); err != nil {
		t.Fatalf("categories.folder_id: %v", err)
	}
	if typ != "INTEGER" || notNull != 0 {
		t.Errorf("categories.folder_id = %s notnull=%d, want nullable INTEGER", typ, notNull)
	}

	for _, fk := range []struct{ table, from string }{{"folders", "parent_id"}, {"categories", "folder_id"}} {
		var table, onDelete string
		if err := s.db.QueryRow(`SELECT "table", on_delete FROM pragma_foreign_key_list(?) WHERE "from" = ?`, fk.table, fk.from).Scan(&table, &onDelete); err != nil {
			t.Fatalf("%s.%s foreign key: %v", fk.table, fk.from, err)
		}
		if table != "folders" || onDelete != "NO ACTION" {
			t.Errorf("%s.%s references %s on delete %s, want folders with NO ACTION", fk.table, fk.from, table, onDelete)
		}
	}

	for _, name := range []string{"idx_folders_sibling_name", "idx_folders_parent", "idx_categories_folder", "trg_folders_no_cycle"} {
		assertSchemaObjectPresent(t, s, name)
	}
	assertSchemaObjectAbsent(t, s, "folder_backfill_map")
	if n := countRows(t, s, `SELECT COUNT(*) FROM schema_migrations WHERE version = 43`); n != 1 {
		t.Errorf("schema_migrations version 43 rows = %d, want 1", n)
	}
}

// TestMigration0043UpgradeCopiesTheCatalog upgrades a seeded pre-0043 catalog
// and checks the exact folder tree, every category's folder, and that nothing
// else moved.
func TestMigration0043UpgradeCopiesTheCatalog(t *testing.T) {
	s := openPre0043(t)

	// 0011 already created department "General" (id 1) holding desk "General".
	mustExec(t, s, `UPDATE departments SET description = 'Everything else', created_at = '2025-01-01T00:00:00Z' WHERE name = 'General'`)
	mustExec(t, s, `UPDATE desks SET description = 'Front line', created_at = '2025-01-02T00:00:00Z' WHERE name = 'General'`)
	mustExec(t, s, `INSERT INTO departments (id, name, description, created_at) VALUES
		(10, 'IT services', 'Laptops and access', '2025-02-01T00:00:00Z'),
		(11, 'Facilities', '', '2025-02-02T00:00:00Z')`)
	mustExec(t, s, `INSERT INTO desks (id, name, description, department_id, created_at) VALUES
		(20, 'Service desk', 'Hardware team', 10, '2025-03-01T00:00:00Z'),
		(21, 'Identity', '', 10, '2025-03-02T00:00:00Z'),
		(22, 'Old desk', 'Before departments', NULL, '2024-12-01T00:00:00Z'),
		(23, 'facilities', '', NULL, '2024-12-02T00:00:00Z')`)
	mustExec(t, s, `INSERT INTO categories (id, name, description, desk_id, created_at) VALUES
		(30, 'Laptop repair', 'Broken laptop', 20, '2025-04-01T00:00:00Z'),
		(31, 'Monitor or dock', 'Screens', 20, '2025-04-02T00:00:00Z'),
		(32, 'Legacy request', 'Old', 22, '2025-04-03T00:00:00Z'),
		(33, 'General question', 'Anything', (SELECT id FROM desks WHERE name = 'General'), '2025-04-04T00:00:00Z')`)
	agent := seedUserRaw(t, s, "Ana", "ana@example.com", "agent")
	mustExec(t, s, `INSERT INTO desk_members (desk_id, user_id, created_at) VALUES (20, ?, '2025-05-01T00:00:00Z')`, agent)

	countsBefore := map[string]int{}
	for _, q := range []string{
		`SELECT COUNT(*) FROM categories`, `SELECT COUNT(*) FROM desks`, `SELECT COUNT(*) FROM departments`,
		`SELECT COUNT(*) FROM desk_members`, `SELECT COUNT(*) FROM category_workflows`, `SELECT COUNT(*) FROM tickets`,
	} {
		countsBefore[q] = countRows(t, s, q)
	}
	var deskOfLaptop int64
	if err := s.db.QueryRow(`SELECT desk_id FROM categories WHERE id = 30`).Scan(&deskOfLaptop); err != nil {
		t.Fatal(err)
	}

	upgradeTo0043(t, s)

	wantTree := []folderRow{
		{"Facilities", "", "2025-02-02T00:00:00Z"},
		{"General", "Everything else", "2025-01-01T00:00:00Z"},
		{"General / General", "Front line", "2025-01-02T00:00:00Z"},
		{"IT services", "Laptops and access", "2025-02-01T00:00:00Z"},
		{"IT services / Identity", "", "2025-03-02T00:00:00Z"},
		{"IT services / Service desk", "Hardware team", "2025-03-01T00:00:00Z"},
		{"Old desk", "Before departments", "2024-12-01T00:00:00Z"},
		// A legacy desk shares the top level with departments: "facilities"
		// collides with "Facilities" case-insensitively and takes its desk id.
		{"facilities (23)", "", "2024-12-02T00:00:00Z"},
	}
	if got := folderTree(t, s); !reflect.DeepEqual(got, wantTree) {
		t.Errorf("folder tree:\n got %+v\nwant %+v", got, wantTree)
	}

	wantPlaces := map[string]string{
		"Laptop repair":    "IT services / Service desk",
		"Monitor or dock":  "IT services / Service desk",
		"Legacy request":   "Old desk",
		"General question": "General / General",
	}
	if got := categoryFolderPaths(t, s); !reflect.DeepEqual(got, wantPlaces) {
		t.Errorf("category folders = %v, want %v", got, wantPlaces)
	}
	// Only the copies of the seeded General department and desk are recorded
	// as such, for 0045 to remove when they are left empty (S17).
	if got := seedCopyPaths(t, s); !reflect.DeepEqual(got, []string{"General", "General / General"}) {
		t.Errorf("seed copies = %q, want the General department and desk copies", got)
	}

	for q, before := range countsBefore {
		if after := countRows(t, s, q); after != before {
			t.Errorf("%s: %d before, %d after", q, before, after)
		}
	}
	var deskAfter int64
	if err := s.db.QueryRow(`SELECT desk_id FROM categories WHERE id = 30`).Scan(&deskAfter); err != nil {
		t.Fatal(err)
	}
	if deskAfter != deskOfLaptop {
		t.Errorf("category 30 desk_id = %d, want %d unchanged", deskAfter, deskOfLaptop)
	}
	assertSchemaObjectAbsent(t, s, "folder_backfill_map")
	assertFolderNameKeys(t, s)
}

// TestMigration0043RepeatedCollision preserves every department even when the
// first generated suffix already names an earlier top-level folder.
func TestMigration0043RepeatedCollision(t *testing.T) {
	s := openPre0043(t)
	mustExec(t, s, `DELETE FROM desks; DELETE FROM departments;
		INSERT INTO departments (id, name, description, created_at) VALUES
		(1, 'IT', '', '2024-01-01T00:00:00Z'), (2, 'it (4)', '', '2024-01-01T00:00:00Z'), (4, 'it', '', '2024-01-01T00:00:00Z')`)

	upgradeTo0043(t, s)

	var got []string
	for _, r := range folderTree(t, s) {
		got = append(got, r.Path)
	}
	want := []string{"IT", "it (4)", "it (4) (4)"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("top-level folders = %q, want %q", got, want)
	}
}

// TestMigration0043Guards proves the database refuses a cyclic move, a sibling
// name clash, and deleting a folder that still holds something — each leaving
// the stored rows unchanged.
func TestMigration0043Guards(t *testing.T) {
	s := newTestDB(t)
	mustExec(t, s, `DELETE FROM categories; DELETE FROM folders;
		INSERT INTO folders (id, parent_id, name, name_key, created_at) VALUES
		(1, NULL, 'IT services', 'it services', '2025-01-01T00:00:00Z'),
		(2, 1, 'Hardware', 'hardware', '2025-01-01T00:00:00Z'),
		(3, 2, 'Laptops', 'laptops', '2025-01-01T00:00:00Z'),
		(4, NULL, 'Empty', 'empty', '2025-01-01T00:00:00Z')`)
	before := folderTree(t, s)

	for _, refused := range []struct {
		name, sql, want string
	}{
		{"self parent", `UPDATE folders SET parent_id = 1 WHERE id = 1`, "folder cycle"},
		{"into a descendant", `UPDATE folders SET parent_id = 3 WHERE id = 1`, "folder cycle"},
		{"into a child", `UPDATE folders SET parent_id = 2 WHERE id = 1`, "folder cycle"},
		// Sibling uniqueness is over name_key, the Go-folded name every writer sets.
		{"sibling clash", `INSERT INTO folders (parent_id, name, name_key, created_at) VALUES (1, 'HARDWARE', 'hardware', '2025-01-01T00:00:00Z')`, "UNIQUE constraint failed"},
		{"top-level clash", `UPDATE folders SET name = 'it SERVICES', name_key = 'it services' WHERE id = 4`, "UNIQUE constraint failed"},
		{"delete with a child folder", `DELETE FROM folders WHERE id = 2`, "FOREIGN KEY constraint failed"},
	} {
		_, err := s.db.Exec(refused.sql)
		if err == nil || !strings.Contains(err.Error(), refused.want) {
			t.Errorf("%s: err = %v, want %q", refused.name, err, refused.want)
		}
		if after := folderTree(t, s); !reflect.DeepEqual(after, before) {
			t.Errorf("%s changed the tree:\n got %+v\nwant %+v", refused.name, after, before)
		}
	}

	id := seedCategory(t, s, "Laptop repair")
	mustExec(t, s, `UPDATE categories SET folder_id = 4 WHERE id = ?`, id)
	if _, err := s.db.Exec(`DELETE FROM folders WHERE id = 4`); err == nil || !strings.Contains(err.Error(), "FOREIGN KEY constraint failed") {
		t.Errorf("delete a folder holding a category: err = %v, want a foreign key refusal", err)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM folders WHERE id = 4`); n != 1 {
		t.Errorf("folder 4 rows = %d, want 1 after the refused delete", n)
	}

	// The same name is fine under different parents, and a legal move works.
	mustExec(t, s, `INSERT INTO folders (parent_id, name, name_key, created_at) VALUES (4, 'Hardware', 'hardware', '2025-01-01T00:00:00Z')`)
	mustExec(t, s, `UPDATE folders SET parent_id = NULL WHERE id = 3`)
}

// TestMigration0043LongCollisionChain is a chain of suffixed names longer than
// any fixed number of renaming passes: every department still becomes a
// folder and the upgrade does not abort.
func TestMigration0043LongCollisionChain(t *testing.T) {
	s := openPre0043(t)
	mustExec(t, s, `DELETE FROM desks; DELETE FROM departments`)
	for i := 1; i <= 7; i++ {
		mustExec(t, s, `INSERT INTO departments (id, name, created_at) VALUES (?, ?, '2024-01-01T00:00:00Z')`, i, "it"+strings.Repeat(" (8)", i-1))
	}
	mustExec(t, s, `INSERT INTO departments (id, name, created_at) VALUES (8, 'IT', '2024-01-01T00:00:00Z')`)

	upgradeTo0043(t, s)

	if got := countRows(t, s, `SELECT COUNT(*) FROM folders`); got != 8 {
		t.Fatalf("folders = %d, want 8", got)
	}
	// "IT" collides with "it" (id 1) and with every suffixed name in the chain,
	// so it takes " (8)" until the name is free.
	if got := countRows(t, s, `SELECT COUNT(*) FROM folders WHERE name = ?`, "IT"+strings.Repeat(" (8)", 7)); got != 1 {
		t.Errorf("IT folder with seven suffixes = %d rows, want 1", got)
	}
	assertFolderNameKeys(t, s)
	if got := countRows(t, s, `SELECT COUNT(DISTINCT name_key) FROM folders`); got != 8 {
		t.Errorf("distinct name keys = %d, want 8", got)
	}
}

// TestMigration0043UnicodeCollision folds case beyond ASCII: "ÁREA" and "área"
// are the same name, so the later one is suffixed instead of kept as a twin.
func TestMigration0043UnicodeCollision(t *testing.T) {
	s := openPre0043(t)
	mustExec(t, s, `DELETE FROM desks; DELETE FROM departments;
		INSERT INTO departments (id, name, description, created_at) VALUES
		(1, 'ÁREA', '', '2024-01-01T00:00:00Z'), (2, 'área', '', '2024-01-01T00:00:00Z')`)

	upgradeTo0043(t, s)

	var got []string
	for _, r := range folderTree(t, s) {
		got = append(got, r.Path)
	}
	if want := []string{"ÁREA", "área (2)"}; !reflect.DeepEqual(got, want) {
		t.Errorf("folders = %q, want %q", got, want)
	}
}
