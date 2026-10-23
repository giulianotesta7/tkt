package sqlite

import (
	"context"
	"fmt"
	"io/fs"
	"testing"
	"testing/fstest"
)

// assertSuggestIndexExact fails unless both suggestion indexes equal a join of
// the catalog, carry no duplicated rowid, and pass the FTS5 integrity check.
// The join coalesces a missing place to ” because FTS5 stores ” for NULL.
func assertSuggestIndexExact(t *testing.T, s *Store, label string) {
	t.Helper()
	rows, err := s.db.Query(`SELECT c.id, c.name, c.keywords, c.description,
		COALESCE(ds.name,''), COALESCE(d.name,'')
		FROM categories c LEFT JOIN desks ds ON ds.id = c.desk_id
		LEFT JOIN departments d ON d.id = ds.department_id ORDER BY c.id`)
	if err != nil {
		t.Fatalf("%s: read catalog: %v", label, err)
	}
	var want []suggestionRow
	var wantTrigram []string
	for rows.Next() {
		var r suggestionRow
		if err := rows.Scan(&r.ID, &r.Name, &r.Keywords, &r.Description, &r.Desk, &r.Department); err != nil {
			t.Fatalf("%s: scan catalog: %v", label, err)
		}
		want = append(want, r)
		wantTrigram = append(wantTrigram, fmt.Sprintf("%d:%s|%s", r.ID, r.Name, r.Keywords))
	}
	rows.Close()
	if got := readSuggestionIndex(t, s); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("%s: main index drifted\ngot  %v\nwant %v", label, got, want)
	}
	if got := readTrigramIndex(t, s); fmt.Sprint(got) != fmt.Sprint(wantTrigram) {
		t.Errorf("%s: trigram index drifted\ngot  %v\nwant %v", label, got, wantTrigram)
	}
	for _, q := range []string{
		`SELECT COUNT(*) FROM (SELECT rowid FROM category_suggest_fts GROUP BY rowid HAVING COUNT(*) > 1)`,
		`SELECT COUNT(*) FROM (SELECT rowid FROM category_suggest_trigram GROUP BY rowid HAVING COUNT(*) > 1)`,
	} {
		var n int
		if err := s.db.QueryRow(q).Scan(&n); err != nil {
			t.Fatalf("%s: duplicate check: %v", label, err)
		}
		if n != 0 {
			t.Errorf("%s: %d duplicated rowids", label, n)
		}
	}
	if _, err := s.db.Exec(`INSERT INTO category_suggest_fts(category_suggest_fts, rank) VALUES('integrity-check', 1)`); err != nil {
		t.Errorf("%s: fts integrity-check: %v", label, err)
	}
}

// TestMigration0031IndexExactAcrossCatalogWrites pins the index against a join
// of the catalog after every catalog write, including a desk moving between
// departments, a desk losing its department, the store's
// COALESCE(NULLIF(?,0),desk_id) no-op update, and a delete.
func TestMigration0031IndexExactAcrossCatalogWrites(t *testing.T) {
	s := newTestDB(t)
	tech := seedCatalogDepartment(t, s, "Tecnología")
	people := seedCatalogDepartment(t, s, "Personas")
	d1 := seedCatalogDesk(t, s, "Service desk", tech)
	d2 := seedCatalogDesk(t, s, "People", people)
	seedCatalogCategory(t, s, 100, "Printers", "Office printer problems", "impresora, tóner", d1)
	seedCatalogCategory(t, s, 101, "Payroll", "Salary questions", "nómina", d2)
	assertSuggestIndexExact(t, s, "insert")

	for _, st := range []struct {
		label string
		sql   string
		args  []any
	}{
		{"rename category", `UPDATE categories SET name='Printer support' WHERE id=100`, nil},
		{"edit keywords", `UPDATE categories SET keywords='impresora, toner, escáner' WHERE id=100`, nil},
		{"edit description", `UPDATE categories SET description='Paper jams' WHERE id=100`, nil},
		{"move to another desk", `UPDATE categories SET desk_id=? WHERE id=100`, []any{d2}},
		{"store no-op desk_id", `UPDATE categories SET name=name, description=description, desk_id=COALESCE(NULLIF(?,0),desk_id) WHERE id=100`, []any{0}},
		{"desk rename", `UPDATE desks SET name='IT service desk' WHERE id=?`, []any{d1}},
		{"desk moves between departments", `UPDATE desks SET department_id=? WHERE id=?`, []any{people, d1}},
		{"desk loses its department", `UPDATE desks SET department_id=NULL WHERE id=?`, []any{d2}},
		{"department rename", `UPDATE departments SET name='Technology' WHERE id=?`, []any{tech}},
		{"delete category", `DELETE FROM categories WHERE id=101`, nil},
	} {
		if _, err := s.db.Exec(st.sql, st.args...); err != nil {
			t.Fatalf("%s: %v", st.label, err)
		}
		assertSuggestIndexExact(t, s, st.label)
	}
}

// TestMigration0031RefusedCatalogDeleteLeavesIndexIntact proves a delete the
// foreign keys refuse cannot half-clean the index.
func TestMigration0031RefusedCatalogDeleteLeavesIndexIntact(t *testing.T) {
	s := newTestDB(t)
	tech := seedCatalogDepartment(t, s, "Tecnología")
	d1 := seedCatalogDesk(t, s, "Service desk", tech)
	seedCatalogCategory(t, s, 100, "Printers", "printer problems", "impresora", d1)
	if _, err := s.db.Exec(`DELETE FROM desks WHERE id=?`, d1); err == nil {
		t.Fatalf("desk delete with a category was allowed")
	}
	if _, err := s.db.Exec(`DELETE FROM departments WHERE id=?`, tech); err == nil {
		t.Fatalf("department delete with a desk was allowed")
	}
	if _, err := s.db.Exec(`UPDATE categories SET desk_id=NULL WHERE id=100`); err == nil {
		t.Fatalf("category desk_id=NULL was allowed")
	}
	assertSuggestIndexExact(t, s, "after refused deletes")
}

// TestMigration0031LateNeighbourMigrations proves that a migration numbered
// below 0031 which reaches an instance after 0031 still applies, and that 0031
// is never applied twice. The late neighbour is the highest embedded migration
// below 0031, withheld from the first run and delivered in the second, so the
// test keeps meaning something as the reserved numbers land.
func TestMigration0031LateNeighbourMigrations(t *testing.T) {
	ctx := context.Background()
	s, err := openDSN(testDSN(t))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	s.db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = s.db.Close() })

	names, err := fs.Glob(migrationsFS, "migrations/*.sql")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	late, lateVersion := "", 0
	for _, name := range names {
		version, err := migrationVersion(name)
		if err != nil {
			t.Fatalf("version of %s: %v", name, err)
		}
		if version < 31 && version > lateVersion {
			late, lateVersion = name, version
		}
	}
	first := fstest.MapFS{}
	for _, name := range names {
		if name == late {
			continue
		}
		blob, err := fs.ReadFile(migrationsFS, name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		first[name] = &fstest.MapFile{Data: blob}
	}
	if err := migrate(ctx, s.db, first); err != nil {
		t.Fatalf("migrate without %s: %v", late, err)
	}
	desk := seedCatalogDesk(t, s, "Service desk", seedCatalogDepartment(t, s, "Tecnología"))
	seedCatalogCategory(t, s, 100, "Printers", "printer problems", "impresora", desk)

	var appliedAt string
	if err := s.db.QueryRow(`SELECT applied_at FROM schema_migrations WHERE version = 31`).Scan(&appliedAt); err != nil {
		t.Fatalf("applied_at 31: %v", err)
	}
	if err := migrate(ctx, s.db, migrationsFS); err != nil {
		t.Fatalf("late neighbour %s: %v", late, err)
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, lateVersion).Scan(&n); err != nil || n != 1 {
		t.Errorf("late migration %s not applied (n=%d, err=%v)", late, n, err)
	}
	var again string
	if err := s.db.QueryRow(`SELECT applied_at FROM schema_migrations WHERE version = 31`).Scan(&again); err != nil {
		t.Fatalf("re-read 31: %v", err)
	}
	if again != appliedAt {
		t.Errorf("0031 was re-applied after a neighbour landed")
	}
	assertSuggestIndexExact(t, s, "after the late neighbour")
}
