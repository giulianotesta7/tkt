package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
)

// Migration 0031 tests (category suggestions — issue #416, S4): the administrator's
// per-category keywords, the content-ful FTS5 index the local engine ranks over,
// and the trigram index it tolerates typos with. These tests pin the column
// shape, both tokenizers, the trigger set that keeps the indexes exact through
// every catalog write, the backfill a pre-0031 instance receives, and the version
// bookkeeping.
//
// The indexes are content-ful on purpose (the 0002 header records the contentless
// pitfalls): a content-ful FTS5 table accepts a plain DELETE ... WHERE rowid, so a
// rename, a move or a delete removes exactly the superseded row and cannot leave
// stale terms searchable.

// pre0031Migrations returns the embedded migrations strictly before 0031, so the
// upgrade-path test can seed a pre-0031 database and then apply the real full set
// (pattern of pre0026Migrations).
func pre0031Migrations(t *testing.T) fstest.MapFS {
	t.Helper()
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		t.Fatalf("read embedded migrations: %v", err)
	}
	out := fstest.MapFS{}
	for _, entry := range entries {
		if strings.Compare(entry.Name(), "0031_category_suggestions.sql") >= 0 {
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

// suggestionRow is one row of the suggestion index as the engine reads it.
type suggestionRow struct {
	ID          int64
	Name        string
	Keywords    string
	Description string
	Desk        sql.NullString
	Department  sql.NullString
}

// readSuggestionIndex returns the whole main index ordered by rowid, so a test
// compares the indexed facts exactly instead of one column at a time.
func readSuggestionIndex(t *testing.T, s *Store) []suggestionRow {
	t.Helper()
	rows, err := s.db.Query(`SELECT rowid, name, keywords, description, desk, department
		FROM category_suggest_fts ORDER BY rowid`)
	if err != nil {
		t.Fatalf("read category_suggest_fts: %v", err)
	}
	defer rows.Close()
	var out []suggestionRow
	for rows.Next() {
		var row suggestionRow
		if err := rows.Scan(&row.ID, &row.Name, &row.Keywords, &row.Description, &row.Desk, &row.Department); err != nil {
			t.Fatalf("scan category_suggest_fts: %v", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate category_suggest_fts: %v", err)
	}
	return out
}

// readTrigramIndex returns the trigram index as "rowid:name|keywords" strings.
func readTrigramIndex(t *testing.T, s *Store) []string {
	t.Helper()
	rows, err := s.db.Query(`SELECT rowid, name, keywords FROM category_suggest_trigram ORDER BY rowid`)
	if err != nil {
		t.Fatalf("read category_suggest_trigram: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var (
			id             int64
			name, keywords string
		)
		if err := rows.Scan(&id, &name, &keywords); err != nil {
			t.Fatalf("scan category_suggest_trigram: %v", err)
		}
		out = append(out, fmt.Sprintf("%d:%s|%s", id, name, keywords))
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate category_suggest_trigram: %v", err)
	}
	return out
}

// seedCatalogDepartment inserts a department and returns its id. 0011 already
// seeds the "General" compatibility department and desk, so the test names its
// own rather than choosing ids that are already taken.
func seedCatalogDepartment(t *testing.T, s *Store, name string) int64 {
	t.Helper()
	res, err := s.db.Exec(`INSERT INTO departments (name, description, created_at) VALUES (?, '', '2026-08-06T10:00:00Z')`, name)
	if err != nil {
		t.Fatalf("seed department %q: %v", name, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("seed department %q id: %v", name, err)
	}
	return id
}

// seedCatalogDesk inserts a desk under the department and returns its id.
func seedCatalogDesk(t *testing.T, s *Store, name string, departmentID int64) int64 {
	t.Helper()
	res, err := s.db.Exec(`INSERT INTO desks (name, description, department_id, created_at) VALUES (?, '', ?, '2026-08-06T10:00:00Z')`,
		name, departmentID)
	if err != nil {
		t.Fatalf("seed desk %q: %v", name, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("seed desk %q id: %v", name, err)
	}
	return id
}

// seedCatalogCategory arranges one category through direct SQL, which is what
// the triggers have to follow: the store is not in the path a migration is
// proven against.
func seedCatalogCategory(t *testing.T, s *Store, id int64, name, description, keywords string, deskID int64) {
	t.Helper()
	if _, err := s.db.Exec(`INSERT INTO categories (id, name, description, keywords, desk_id, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`, id, name, description, keywords, deskID, "2026-08-06T10:00:00Z"); err != nil {
		t.Fatalf("seed category %q: %v", name, err)
	}
}

// TestMigration0031KeywordsColumn pins the column the catalog drawer edits: TEXT,
// NOT NULL, empty by default, so an existing category reads back with no keywords
// and a create that sends none never stores NULL.
func TestMigration0031KeywordsColumn(t *testing.T) {
	s := newTestDB(t)

	var (
		columnType string
		notNull    int
		defaultVal sql.NullString
	)
	if err := s.db.QueryRow(`SELECT type, "notnull", dflt_value FROM pragma_table_info('categories') WHERE name='keywords'`).
		Scan(&columnType, &notNull, &defaultVal); err != nil {
		t.Fatalf("pragma_table_info('categories') keywords: %v", err)
	}
	if columnType != "TEXT" {
		t.Errorf("keywords type = %q, want TEXT", columnType)
	}
	if notNull != 1 {
		t.Errorf("keywords notnull = %d, want 1", notNull)
	}
	if !defaultVal.Valid || defaultVal.String != "''" {
		t.Errorf("keywords default = %v, want ''", defaultVal)
	}

	// A row inserted without the column carries the default, not NULL.
	deskID := seedCatalogDesk(t, s, "Service desk", seedCatalogDepartment(t, s, "Tecnología"))
	if _, err := s.db.Exec(`INSERT INTO categories (name, desk_id, created_at) VALUES ('Defaulted', ?, '2026-08-06T10:00:00Z')`, deskID); err != nil {
		t.Fatalf("insert category without keywords: %v", err)
	}
	var keywords sql.NullString
	if err := s.db.QueryRow(`SELECT keywords FROM categories WHERE name='Defaulted'`).Scan(&keywords); err != nil {
		t.Fatalf("read default keywords: %v", err)
	}
	if !keywords.Valid || keywords.String != "" {
		t.Errorf("keywords = %v, want an empty string (never NULL)", keywords)
	}
}

// TestMigration0031IndexTablesExist pins both indexes, the vocabulary table the
// engine checks query words against, and the tokenizers they were built with:
// unicode61 with full diacritic removal for ranking, trigram for typos.
func TestMigration0031IndexTablesExist(t *testing.T) {
	s := newTestDB(t)

	for _, name := range []string{"category_suggest_fts", "category_suggest_trigram", "category_suggest_vocab"} {
		var (
			kind string
			sql  string
		)
		if err := s.db.QueryRow(`SELECT type, sql FROM sqlite_master WHERE name = ?`, name).Scan(&kind, &sql); err != nil {
			t.Fatalf("read sqlite_master for %s: %v", name, err)
		}
		if kind != "table" {
			t.Errorf("%s type = %q, want table", name, kind)
		}
		switch name {
		case "category_suggest_fts":
			for _, want := range []string{"fts5", "name", "keywords", "description", "desk", "department", "unicode61 remove_diacritics 2"} {
				if !strings.Contains(sql, want) {
					t.Errorf("category_suggest_fts declaration %q is missing %q", sql, want)
				}
			}
		case "category_suggest_trigram":
			for _, want := range []string{"name", "keywords", "trigram remove_diacritics 1"} {
				if !strings.Contains(sql, want) {
					t.Errorf("category_suggest_trigram declaration %q is missing %q", sql, want)
				}
			}
		}
	}

	// The tokenizer is proven by behavior, not only by its declaration: an
	// accented, capitalized indexed word is matched by an accentless lowercase
	// query, which is what the Go-side normalization relies on.
	departmentID := seedCatalogDepartment(t, s, "Tecnología")
	deskID := seedCatalogDesk(t, s, "Mesa de ayuda", departmentID)
	seedCatalogCategory(t, s, 1, "Conexión", "Problemas de red", "wifi", deskID)

	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM category_suggest_fts WHERE category_suggest_fts MATCH '"conexion"'`).Scan(&n); err != nil {
		t.Fatalf("match accentless query: %v", err)
	}
	if n != 1 {
		t.Errorf("accentless match on an accented name found %d rows, want 1 (unicode61 remove_diacritics 2)", n)
	}
	// The trigram index answers a fragment of the same word.
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM category_suggest_trigram WHERE category_suggest_trigram MATCH '"con"'`).Scan(&n); err != nil {
		t.Fatalf("trigram match: %v", err)
	}
	if n != 1 {
		t.Errorf("trigram match found %d rows, want 1", n)
	}
	var term string
	if err := s.db.QueryRow(`SELECT term FROM category_suggest_vocab WHERE term = 'conexion'`).Scan(&term); err != nil {
		t.Fatalf("vocabulary lookup: %v", err)
	}
	if term != "conexion" {
		t.Errorf("vocabulary term = %q, want %q", term, "conexion")
	}
}

// TestMigration0031IndexFollowsCategoryWrites proves every category write path
// leaves both indexes exact: create, rename, keyword edit, description edit and
// delete. A superseded term must not stay searchable.
func TestMigration0031IndexFollowsCategoryWrites(t *testing.T) {
	s := newTestDB(t)
	departmentID := seedCatalogDepartment(t, s, "Tecnología")
	deskID := seedCatalogDesk(t, s, "Service desk", departmentID)

	seedCatalogCategory(t, s, 1, "Printers", "Office printer problems", "impresora, tóner", deskID)
	got := readSuggestionIndex(t, s)
	want := []suggestionRow{{ID: 1, Name: "Printers", Keywords: "impresora, tóner", Description: "Office printer problems",
		Desk: sql.NullString{String: "Service desk", Valid: true}, Department: sql.NullString{String: "Tecnología", Valid: true}}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("index after insert = %v, want %v", got, want)
	}
	if got := readTrigramIndex(t, s); fmt.Sprint(got) != fmt.Sprint([]string{"1:Printers|impresora, tóner"}) {
		t.Errorf("trigram index after insert = %v", got)
	}

	// Rename: the old name is gone from both indexes.
	if _, err := s.db.Exec(`UPDATE categories SET name='Printer support' WHERE id=1`); err != nil {
		t.Fatalf("rename category: %v", err)
	}
	got = readSuggestionIndex(t, s)
	if len(got) != 1 || got[0].Name != "Printer support" {
		t.Fatalf("index after rename = %v, want the new name only", got)
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM category_suggest_fts WHERE category_suggest_fts MATCH '"printers"'`).Scan(&n); err != nil {
		t.Fatalf("match superseded name: %v", err)
	}
	if n != 0 {
		t.Errorf("the superseded name is still searchable (%d rows), want 0", n)
	}
	if got := readTrigramIndex(t, s); fmt.Sprint(got) != fmt.Sprint([]string{"1:Printer support|impresora, tóner"}) {
		t.Errorf("trigram index after rename = %v", got)
	}

	// Keyword edit: the new keywords are searchable, the removed ones are not.
	if _, err := s.db.Exec(`UPDATE categories SET keywords='impresora, toner, escáner' WHERE id=1`); err != nil {
		t.Fatalf("edit keywords: %v", err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM category_suggest_fts WHERE category_suggest_fts MATCH '"escaner"'`).Scan(&n); err != nil {
		t.Fatalf("match new keyword: %v", err)
	}
	if n != 1 {
		t.Errorf("new keyword found %d rows, want 1", n)
	}
	if got := readTrigramIndex(t, s); fmt.Sprint(got) != fmt.Sprint([]string{"1:Printer support|impresora, toner, escáner"}) {
		t.Errorf("trigram index after keyword edit = %v", got)
	}

	// Description edit is indexed too (the ranking reads it at the lowest weight).
	if _, err := s.db.Exec(`UPDATE categories SET description='Paper jams and drivers' WHERE id=1`); err != nil {
		t.Fatalf("edit description: %v", err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM category_suggest_fts WHERE category_suggest_fts MATCH '"jams"'`).Scan(&n); err != nil {
		t.Fatalf("match new description word: %v", err)
	}
	if n != 1 {
		t.Errorf("new description word found %d rows, want 1", n)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM category_suggest_fts WHERE category_suggest_fts MATCH '"problems"'`).Scan(&n); err != nil {
		t.Fatalf("match superseded description word: %v", err)
	}
	if n != 0 {
		t.Errorf("the superseded description word is still searchable (%d rows), want 0", n)
	}

	// Delete: nothing survives in either index.
	if _, err := s.db.Exec(`DELETE FROM categories WHERE id=1`); err != nil {
		t.Fatalf("delete category: %v", err)
	}
	if got := readSuggestionIndex(t, s); len(got) != 0 {
		t.Errorf("index after delete = %v, want empty", got)
	}
	if got := readTrigramIndex(t, s); len(got) != 0 {
		t.Errorf("trigram index after delete = %v, want empty", got)
	}
}

// TestMigration0031IndexFollowsCatalogMoves proves the index follows the catalog
// around a category: moving it to another desk, renaming that desk, and renaming
// the department above it. A stale desk or department name would rank a category
// by a place it no longer sits in.
func TestMigration0031IndexFollowsCatalogMoves(t *testing.T) {
	s := newTestDB(t)
	technologyID := seedCatalogDepartment(t, s, "Tecnología")
	peopleID := seedCatalogDepartment(t, s, "Personas")
	serviceDeskID := seedCatalogDesk(t, s, "Service desk", technologyID)
	peopleDeskID := seedCatalogDesk(t, s, "People", peopleID)
	seedCatalogCategory(t, s, 1, "Payroll", "Salary questions", "nómina", serviceDeskID)

	if _, err := s.db.Exec(`UPDATE categories SET desk_id=? WHERE id=1`, peopleDeskID); err != nil {
		t.Fatalf("move category: %v", err)
	}
	got := readSuggestionIndex(t, s)
	if len(got) != 1 || got[0].Desk.String != "People" || got[0].Department.String != "Personas" {
		t.Fatalf("index after move = %v, want Desk People and Department Personas", got)
	}

	if _, err := s.db.Exec(`UPDATE desks SET name='Human resources' WHERE id=?`, peopleDeskID); err != nil {
		t.Fatalf("rename desk: %v", err)
	}
	got = readSuggestionIndex(t, s)
	if len(got) != 1 || got[0].Desk.String != "Human resources" {
		t.Fatalf("index after desk rename = %v, want the desk's new name", got)
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM category_suggest_fts WHERE category_suggest_fts MATCH '"people"'`).Scan(&n); err != nil {
		t.Fatalf("match superseded desk name: %v", err)
	}
	if n != 0 {
		t.Errorf("the superseded desk name is still searchable (%d rows), want 0", n)
	}

	if _, err := s.db.Exec(`UPDATE departments SET name='People operations' WHERE id=?`, peopleID); err != nil {
		t.Fatalf("rename department: %v", err)
	}
	got = readSuggestionIndex(t, s)
	if len(got) != 1 || got[0].Department.String != "People operations" {
		t.Fatalf("index after department rename = %v, want the department's new name", got)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM category_suggest_fts WHERE category_suggest_fts MATCH '"personas"'`).Scan(&n); err != nil {
		t.Fatalf("match superseded department name: %v", err)
	}
	if n != 0 {
		t.Errorf("the superseded department name is still searchable (%d rows), want 0", n)
	}
}

// TestMigration0031UpgradePathBackfillsExistingCategories proves a pre-0031
// instance upgrades cleanly: every category it already had is indexed with the
// desk and department it sits in, its keywords arrive empty, and the version is
// recorded once.
func TestMigration0031UpgradePathBackfillsExistingCategories(t *testing.T) {
	ctx := context.Background()
	pre, err := openDSN(testDSN(t))
	if err != nil {
		t.Fatalf("open pre-0031 db: %v", err)
	}
	pre.db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = pre.db.Close() })
	if err := migrate(ctx, pre.db, pre0031Migrations(t)); err != nil {
		t.Fatalf("migrate pre-0031 db: %v", err)
	}

	departmentID := seedCatalogDepartment(t, pre, "Tecnología")
	deskID := seedCatalogDesk(t, pre, "Service desk", departmentID)
	if _, err := pre.db.ExecContext(ctx,
		`INSERT INTO categories (id, name, description, desk_id, created_at) VALUES (7, 'Impresoras', 'La impresora del piso', ?, '2026-08-06T10:00:00Z')`, deskID); err != nil {
		t.Fatalf("seed pre-0031 category: %v", err)
	}

	if err := migrate(ctx, pre.db, migrationsFS); err != nil {
		t.Fatalf("upgrade pre-0031 db: %v", err)
	}

	var (
		name, keywords, description, desk, department string
	)
	if err := pre.db.QueryRowContext(ctx, `SELECT name, keywords, description, desk, department
		FROM category_suggest_fts WHERE rowid = 7`).Scan(&name, &keywords, &description, &desk, &department); err != nil {
		t.Fatalf("read backfilled index row: %v", err)
	}
	if name != "Impresoras" || description != "La impresora del piso" || desk != "Service desk" || department != "Tecnología" {
		t.Errorf("backfilled row = %q/%q/%q/%q, want the category's own facts", name, keywords, description, desk)
	}
	if keywords != "" {
		t.Errorf("backfilled keywords = %q, want empty (0031 invents none)", keywords)
	}
	var trigram int
	if err := pre.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM category_suggest_trigram WHERE rowid = 7`).Scan(&trigram); err != nil {
		t.Fatalf("read backfilled trigram row: %v", err)
	}
	if trigram != 1 {
		t.Errorf("backfilled trigram rows = %d, want 1", trigram)
	}
	// The stored category keeps its data and gains the empty column.
	var storedName string
	if err := pre.db.QueryRowContext(ctx, `SELECT name FROM categories WHERE id = 7`).Scan(&storedName); err != nil {
		t.Fatalf("read upgraded category: %v", err)
	}
	if storedName != "Impresoras" {
		t.Errorf("category name after upgrade = %q, want it untouched", storedName)
	}

	var recorded int
	if err := pre.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version = 31`).Scan(&recorded); err != nil {
		t.Fatalf("schema_migrations read: %v", err)
	}
	if recorded != 1 {
		t.Errorf("schema_migrations version = 31 rows = %d, want 1", recorded)
	}
}
