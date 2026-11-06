package sqlite

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/giulianotesta7/tkt/internal/domain"
)

func TestMigration0047RemovesOnlyDeskSuggestions(t *testing.T) {
	ctx := context.Background()
	s, err := openDSN(testDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	s.db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = s.db.Close() })
	if err := migrate(ctx, s.db, migrationsBefore(t, "0047_suggestions_without_category_desk.sql", false)); err != nil {
		t.Fatal(err)
	}
	d := &domain.Desk{Name: "Zeppelin Nebula", CreatedAt: testClock}
	if err := s.DeskStore().Create(ctx, d); err != nil {
		t.Fatal(err)
	}
	c := &domain.Category{Name: "Printers", Keywords: "impresora", Description: "toner", CreatedAt: testClock}
	createLegacyCategory(t, s, c, d.ID)
	// This category makes the desk word known independently, exposing any stale
	// desk attribution in the reader even after the index stops indexing desks.
	other := &domain.Category{Name: "Zeppelin", CreatedAt: testClock}
	if err := s.CategoryStore().Create(ctx, other); err != nil {
		t.Fatal(err)
	}
	assertLegacyDesk0047(t, s, c.ID)
	beforeDesk := categoryDeskRaw(t, s, c.ID)
	before, err := s.CategoryStore().List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := migrate(ctx, s.db, migrationsBefore(t, "0047_suggestions_without_category_desk.sql", true)); err != nil {
		t.Fatal(err)
	}
	after, err := s.CategoryStore().List(ctx)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("category preservation: %v, %v", after, err)
	}
	if afterDesk := categoryDeskRaw(t, s, c.ID); afterDesk != beforeDesk || afterDesk.Int64 != d.ID {
		t.Fatalf("legacy desk = %+v, want %d preserved", afterDesk, d.ID)
	}
	assertSuggestion0047(t, s, c.ID, "zeppelin", "")
	assertSuggestion0047(t, s, c.ID, "nebula", "")
	if n := countRows(t, s, `SELECT COUNT(*) FROM category_suggest_vocab WHERE term='nebula'`); n != 0 {
		t.Fatal("stale desk vocabulary")
	}
	assertSuggestion0047(t, s, c.ID, "printers zeppelin", "printers")
	assertSuggestion0047(t, s, c.ID, "impresra", "impresora")
	assertSuggestion0047(t, s, c.ID, "toner", "toner")
	for _, name := range []string{"trg_category_suggest_desk_au", "trg_category_suggest_desk_ad"} {
		assertSchemaObjectAbsent(t, s, name)
	}
	for _, name := range []string{"trg_category_suggest_ad", "trg_category_suggest_folder_au"} {
		assertSchemaObjectPresent(t, s, name)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM category_suggest_fts WHERE desk <> ''`); n != 0 {
		t.Fatalf("nonempty desk rows: %d", n)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM schema_migrations WHERE version=47`); n != 1 {
		t.Fatalf("version count: %d", n)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM pragma_foreign_key_list('categories') WHERE "from"='desk_id' AND "table"='desks'`); n != 1 {
		t.Fatal("desk FK lost")
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM pragma_table_info('category_suggest_fts')`); n != 5 {
		t.Fatal("FTS shape changed")
	}
	if err := migrate(ctx, s.db, migrationsBefore(t, "0047_suggestions_without_category_desk.sql", true)); err != nil {
		t.Fatal(err)
	}

	d.Name = "Quasar"
	if err := s.DeskStore().Update(ctx, d); err != nil {
		t.Fatal(err)
	}
	assertSuggestion0047(t, s, c.ID, "quasar", "")
	// The existing category association still prevents deleting its desk.
	if err := s.DeskStore().Delete(ctx, d.ID); err == nil {
		t.Fatal("referenced desk deleted")
	}
	assertSuggestion0047(t, s, c.ID, "printers", "printers")
	c.Name, c.Keywords = "Scanners", "digitalizar"
	if err := s.CategoryStore().Update(ctx, c); err != nil {
		t.Fatal(err)
	}
	assertSuggestion0047(t, s, c.ID, "printers impresora quasar", "")
	assertSuggestion0047(t, s, c.ID, "scanners digitalizar", "scanners|digitalizar")
	fresh := &domain.Category{Name: "Laptops", CreatedAt: testClock}
	createLegacyCategory(t, s, fresh, d.ID)
	assertSuggestion0047(t, s, fresh.ID, "quasar", "")
	assertSuggestion0047(t, s, fresh.ID, "laptops", "laptops")

	fs := newFolderStore(s.db)
	root := createFolder(t, fs, nil, "Hardware")
	child := createFolder(t, fs, &root, "Devices")
	parent := createFolder(t, fs, nil, "Equipment")
	if err := fs.PlaceCategory(ctx, c.ID, &child); err != nil {
		t.Fatal(err)
	}
	assertSuggestion0047(t, s, c.ID, "hardware devices", "hardware|devices")
	if err := fs.UpdateFolder(ctx, &domain.Folder{ID: root, Name: "Technology"}); err != nil {
		t.Fatal(err)
	}
	assertSuggestion0047(t, s, c.ID, "hardware", "")
	assertSuggestion0047(t, s, c.ID, "technology", "technology")
	if err := fs.MoveFolder(ctx, child, &parent); err != nil {
		t.Fatal(err)
	}
	assertSuggestion0047(t, s, c.ID, "technology", "")
	assertSuggestion0047(t, s, c.ID, "equipment devices", "equipment|devices")
	if err := s.CategoryStore().Delete(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	assertSuggestion0047(t, s, c.ID, "scanners digitalizar equipment", "")
	if err := s.CategoryStore().Delete(ctx, fresh.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeskStore().Delete(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	assertSuggestion0047(t, s, other.ID, "zeppelin", "zeppelin")
}

func assertSuggestion0047(t *testing.T, s *Store, id int64, query, want string) {
	t.Helper()
	got, err := s.CategorySuggestionIndex().SuggestCandidates(context.Background(), query, []int64{id})
	if err != nil {
		t.Fatal(err)
	}
	if want == "" {
		if len(got) != 0 {
			t.Fatalf("query %q: %+v, want no candidates", query, got)
		}
		return
	}
	if len(got) != 1 || got[0].CategoryID != id || strings.Join(got[0].MatchedWords, "|") != want {
		t.Fatalf("query %q: %+v, want %q", query, got, want)
	}
}

func assertLegacyDesk0047(t *testing.T, s *Store, id int64) {
	t.Helper()
	got, err := s.CategorySuggestionIndex().SuggestCandidates(context.Background(), "zeppelin", []int64{id})
	if err != nil || len(got) != 1 || got[0].CategoryID != id {
		t.Fatalf("legacy desk index: %+v, %v", got, err)
	}
}

func TestMigration0047Rollback(t *testing.T) {
	ctx := context.Background()
	s, err := openDSN(testDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	s.db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = s.db.Close() })
	if err := migrate(ctx, s.db, migrationsBefore(t, "0047_suggestions_without_category_desk.sql", false)); err != nil {
		t.Fatal(err)
	}
	d := &domain.Desk{Name: "Zeppelin", CreatedAt: testClock}
	if err := s.DeskStore().Create(ctx, d); err != nil {
		t.Fatal(err)
	}
	c := &domain.Category{Name: "Printers", CreatedAt: testClock}
	createLegacyCategory(t, s, c, d.ID)
	data, err := migrationsFS.ReadFile("migrations/0047_suggestions_without_category_desk.sql")
	if err != nil {
		t.Fatal(err)
	}
	broken := fstest.MapFS{"migrations/0047_broken.sql": &fstest.MapFile{Data: append(data, []byte("\nINVALID SQL;")...)}}
	if err := migrate(ctx, s.db, broken); err == nil {
		t.Fatal("broken migration succeeded")
	}
	assertLegacyDesk0047(t, s, c.ID)
	assertSchemaObjectPresent(t, s, "trg_category_suggest_desk_au")
	assertSchemaObjectPresent(t, s, "trg_category_suggest_desk_ad")
	if n := countRows(t, s, `SELECT COUNT(*) FROM schema_migrations WHERE version=47`); n != 0 {
		t.Fatal("failed migration recorded")
	}
}
