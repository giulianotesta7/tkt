package httpadapter

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/adapters/sqlite"
)

// TestOpenTestStoreMatchesAFreshMigration proves that a store opened from the
// migrated template (issue #485) has exactly the schema and the migration
// bookkeeping of a database migrated from scratch, so copying the template
// changes the setup cost and nothing a test can observe.
func TestOpenTestStoreMatchesAFreshMigration(t *testing.T) {
	copiedPath := t.TempDir() + "/app.db"
	openTestStoreAt(t, copiedPath)

	freshPath := t.TempDir() + "/fresh.db"
	fresh, err := sqlite.OpenForTests(freshPath)
	if err != nil {
		t.Fatalf("open fresh store: %v", err)
	}
	t.Cleanup(func() { _ = fresh.Close() })
	if err := fresh.Migrate(t.Context()); err != nil {
		t.Fatalf("migrate fresh store: %v", err)
	}

	describe := func(path string) (schema, versions string) {
		t.Helper()
		db, err := sql.Open("sqlite", "file:"+path)
		if err != nil {
			t.Fatalf("open raw db %s: %v", path, err)
		}
		defer db.Close()
		schema = scanOneString(t, db, `SELECT group_concat(type || ' ' || name || ' ' || coalesce(sql, ''), char(10)) FROM (SELECT type, name, sql FROM sqlite_master ORDER BY type, name)`)
		versions = scanOneString(t, db, `SELECT group_concat(version, ',') FROM (SELECT version FROM schema_migrations ORDER BY version)`)
		return schema, versions
	}
	copiedSchema, copiedVersions := describe(copiedPath)
	freshSchema, freshVersions := describe(freshPath)
	if copiedSchema != freshSchema {
		t.Errorf("schema of the copied template differs from a fresh migration:\ncopied:\n%s\nfresh:\n%s", copiedSchema, freshSchema)
	}
	if copiedVersions != freshVersions || copiedVersions == "" {
		t.Errorf("schema_migrations = %q, want %q", copiedVersions, freshVersions)
	}
}

// TestOpenTestStoreGivesEveryTestItsOwnDatabase proves the template copies are
// independent: a write through one store is invisible to the next.
func TestOpenTestStoreGivesEveryTestItsOwnDatabase(t *testing.T) {
	first := openTestStore(t)
	seedUser(t, first, "Ana", "ana@example.com")

	second := openTestStore(t)
	if _, err := second.UserStore().GetByEmail(t.Context(), "ana@example.com"); err == nil {
		t.Fatal("a user written through one test store is visible through another")
	}
}

// TestNewTestRendererSharesTheParseButNotTheLanguage proves newTestRenderer
// reuses one parsed tree while every renderer keeps its own language source,
// and that it still builds every field NewRendererWithLanguage builds.
func TestNewTestRendererSharesTheParseButNotTheLanguage(t *testing.T) {
	english := newTestRenderer(t, stubInterfaceLanguage{language: "en"})
	spanish := newTestRenderer(t, stubInterfaceLanguage{language: "es"})

	if english.fallback == nil || english.fallback != spanish.fallback || english.fragments != spanish.fragments {
		t.Fatal("test renderers must share one parsed template tree")
	}

	render := func(r *Renderer) string {
		t.Helper()
		rec := httptest.NewRecorder()
		r.Render(rec, httptest.NewRequest(http.MethodGet, "/login", nil), "login", "", loginData{}, http.StatusOK)
		if rec.Code != http.StatusOK {
			t.Fatalf("render login: status %d", rec.Code)
		}
		return rec.Body.String()
	}
	if body := render(english); !strings.Contains(body, `<html lang="en"`) {
		t.Errorf("english renderer did not render English: %.200s", body)
	}
	if body := render(spanish); !strings.Contains(body, `<html lang="es"`) {
		t.Errorf("spanish renderer did not render Spanish: %.200s", body)
	}

	// newTestRenderer copies Renderer's fields by name. A new field must be
	// wired there too, or test renderers silently drift from production ones.
	if got := reflect.TypeFor[Renderer]().NumField(); got != 5 {
		t.Fatalf("Renderer has %d fields; update newTestRenderer and this count", got)
	}
}
