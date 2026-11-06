package sqlite

import (
	"context"
	"reflect"
	"testing"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// Migration 0044 tests (catalog folders — issue #484, S8): the category
// suggestion index ranks each category with its folder path instead of its
// department. The engine still suggests categories only; the path is matching
// context that follows every folder rename and move.

// suggestFolders reads the indexed folder path of every category, by name.
func suggestFolders(t *testing.T, s *Store) map[string]string {
	t.Helper()
	rows, err := s.db.Query(`SELECT c.name, f.folders FROM category_suggest_fts f JOIN categories c ON c.id = f.rowid`)
	if err != nil {
		t.Fatalf("read indexed folders: %v", err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var name, folders string
		if err := rows.Scan(&name, &folders); err != nil {
			t.Fatal(err)
		}
		out[name] = folders
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestMigration0044IndexColumns(t *testing.T) {
	s, err := openDSN(testDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	s.db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = s.db.Close() })
	if err := migrate(context.Background(), s.db, migrationsBefore(t, "0044_category_suggest_folders.sql", true)); err != nil {
		t.Fatal(err)
	}
	rows, err := s.db.Query(`SELECT name FROM pragma_table_info('category_suggest_fts') ORDER BY cid`)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		got = append(got, name)
	}
	rows.Close()
	if want := []string{"name", "keywords", "description", "desk", "folders"}; !reflect.DeepEqual(got, want) {
		t.Errorf("category_suggest_fts columns = %q, want %q", got, want)
	}
	for _, gone := range []string{"trg_category_suggest_dept_au", "trg_category_suggest_dept_ad"} {
		assertSchemaObjectAbsent(t, s, gone)
	}
	for _, present := range []string{"trg_category_suggest_ai", "trg_category_suggest_au", "trg_category_suggest_ad",
		"trg_category_suggest_desk_au", "trg_category_suggest_desk_ad", "trg_category_suggest_folder_au", "category_suggest_vocab"} {
		assertSchemaObjectPresent(t, s, present)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM schema_migrations WHERE version = 44`); n != 1 {
		t.Errorf("schema_migrations version 44 rows = %d, want 1", n)
	}
}

// TestMigration0044IndexFollowsTheTree writes through the folder store and
// checks the indexed path after each change, including a whole subtree moving.
func TestMigration0044IndexFollowsTheTree(t *testing.T) {
	ctx := context.Background()
	s, fs := newFolderTestStore(t)
	it := createFolder(t, fs, nil, "IT services")
	hw := createFolder(t, fs, &it, "Hardware")
	laptops := createFolder(t, fs, &hw, "Laptops")
	people := createFolder(t, fs, nil, "People")
	repair := seedCategory(t, s, "Laptop repair")
	seedCategory(t, s, "General question")

	if got := suggestFolders(t, s)["Laptop repair"]; got != "" {
		t.Errorf("new top-level category indexed folders = %q, want empty", got)
	}
	if err := fs.PlaceCategory(ctx, repair, &laptops); err != nil {
		t.Fatal(err)
	}
	if got := suggestFolders(t, s)["Laptop repair"]; got != "IT services Hardware Laptops" {
		t.Errorf("placed category folders = %q", got)
	}

	steps := []struct {
		name string
		do   func() error
		want string
	}{
		{"rename an ancestor", func() error { return fs.UpdateFolder(ctx, &domain.Folder{ID: it, Name: "Tecnología"}) }, "Tecnología Hardware Laptops"},
		{"move the subtree", func() error { return fs.MoveFolder(ctx, hw, &people) }, "People Hardware Laptops"},
		{"move to the top level", func() error { return fs.MoveFolder(ctx, hw, nil) }, "Hardware Laptops"},
		{"category back to the top level", func() error { return fs.PlaceCategory(ctx, repair, nil) }, ""},
	}
	for _, step := range steps {
		if err := step.do(); err != nil {
			t.Fatalf("%s: %v", step.name, err)
		}
		if got := suggestFolders(t, s)["Laptop repair"]; got != step.want {
			t.Errorf("%s: indexed folders = %q, want %q", step.name, got, step.want)
		}
	}
	if got := suggestFolders(t, s)["General question"]; got != "" {
		t.Errorf("an untouched category changed: %q", got)
	}
}

// TestMigration0044UpgradeBackfills upgrades a pre-0044 database and checks
// every category is indexed with its folder path and nothing else is lost.
func TestMigration0044UpgradeBackfills(t *testing.T) {
	ctx := context.Background()
	s, err := openDSN(testDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	s.db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = s.db.Close() })
	if err := migrate(ctx, s.db, migrationsBefore(t, "0044_category_suggest_folders.sql", false)); err != nil {
		t.Fatalf("migrate before 0044: %v", err)
	}
	mustExec(t, s, `INSERT INTO folders (id, parent_id, name, name_key, created_at) VALUES
		(100, NULL, 'IT services', 'IT SERVICES', '2025-01-01T00:00:00Z'),
		(101, 100, 'Access', 'ACCESS', '2025-01-01T00:00:00Z')`)
	id := seedCategory(t, s, "Password reset")
	mustExec(t, s, `UPDATE categories SET folder_id = 101, keywords = 'locked out' WHERE id = ?`, id)
	before := countRows(t, s, `SELECT COUNT(*) FROM categories`)

	if err := migrate(ctx, s.db, migrationsFS); err != nil {
		t.Fatalf("upgrade through 0044: %v", err)
	}

	if got := suggestFolders(t, s)["Password reset"]; got != "IT services Access" {
		t.Errorf("backfilled folders = %q, want %q", got, "IT services Access")
	}
	if after := countRows(t, s, `SELECT COUNT(*) FROM category_suggest_fts`); after != before {
		t.Errorf("indexed rows = %d, want one per category (%d)", after, before)
	}
	var keywords string
	if err := s.db.QueryRow(`SELECT keywords FROM category_suggest_fts WHERE rowid = ?`, id).Scan(&keywords); err != nil || keywords != "locked out" {
		t.Errorf("indexed keywords = %q, %v", keywords, err)
	}
}

// TestMigration0044SuggestsByFolderWord proves a folder name is matching
// context: a word only the folder path carries still reaches the category, and
// the suggestion is the category, reported with the matched word.
func TestMigration0044SuggestsByFolderWord(t *testing.T) {
	ctx := context.Background()
	s, fs := newFolderTestStore(t)
	licenses := createFolder(t, fs, nil, "Licencias")
	figma := seedCategory(t, s, "Figma seat")
	other := seedCategory(t, s, "Payroll question")
	if err := fs.PlaceCategory(ctx, figma, &licenses); err != nil {
		t.Fatal(err)
	}

	got, err := s.CategorySuggestionIndex().SuggestCandidates(ctx, "necesito licencias", []int64{figma, other})
	if err != nil {
		t.Fatalf("suggest: %v", err)
	}
	if len(got) != 1 || got[0].CategoryID != figma {
		t.Fatalf("suggestions = %+v, want only Figma seat", got)
	}
	if !reflect.DeepEqual(got[0].MatchedWords, []string{"licencias"}) {
		t.Errorf("matched words = %q, want [licencias]", got[0].MatchedWords)
	}
}
