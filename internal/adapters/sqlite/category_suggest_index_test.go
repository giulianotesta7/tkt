package sqlite

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// Local category-suggestion index (issue #416, S4). These tests run against the
// real modernc driver, the real embedded migrations and the real catalog stores:
// the index is a projection of the catalog, so a hand-built row set would prove
// nothing about triggers, tokenizers or ranking.
//
// The catalog below is deliberately bilingual — Spanish names, keywords and
// descriptions beside English ones — because a requester writes in the instance's
// language while an administrator may name a category in either.

var suggestionSeedTime = time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC)

// newSuggestionCatalog builds the nine-category catalog the cases below rank
// against and returns it with each category's id by name.
func newSuggestionCatalog(t *testing.T) (*Store, map[string]int64) {
	t.Helper()
	ctx := context.Background()
	s := newTestDB(t)
	catalog, desks, categories := s.CatalogStore(), s.DeskStore(), s.CategoryStore()

	department := func(name string) int64 {
		t.Helper()
		d := &domain.Department{Name: name, CreatedAt: suggestionSeedTime}
		if err := catalog.CreateDepartment(ctx, d); err != nil {
			t.Fatalf("create department %q: %v", name, err)
		}
		return d.ID
	}
	desk := func(name string, departmentID int64) int64 {
		t.Helper()
		d := &domain.Desk{Name: name, CreatedAt: suggestionSeedTime, DepartmentID: &departmentID}
		if err := desks.Create(ctx, d); err != nil {
			t.Fatalf("create desk %q: %v", name, err)
		}
		return d.ID
	}

	technology := department("Tecnología")
	people := department("Personas")
	finance := department("Finanzas")
	serviceDesk := desk("Service desk", technology)
	itSupport := desk("IT support", technology)
	peopleDesk := desk("People", people)
	financeDesk := desk("Finance", finance)

	rows := []struct {
		name, description, keywords string
		deskID                      int64
	}{
		{"Printers", "Problems with the office printer: paper jams, toner and drivers", "impresora, toner, imprimir, printer", serviceDesk},
		{"Network & Wi-Fi", "Wifi and internet access for the office", "wifi, red, internet, conexión, vpn", itSupport},
		{"Laptops & computers", "Laptop requests, hardware repair and replacements", "laptop, computadora, notebook, hardware", serviceDesk},
		{"Payroll", "Payroll and salary questions", "nómina, sueldo, salario, payroll", peopleDesk},
		{"Vacation & leave", "Vacation days, sick leave and time off requests", "vacaciones, licencia, permiso, ausencia", peopleDesk},
		{"Expenses & reimbursement", "Travel expenses and reimbursement of receipts", "gastos, reintegro, viáticos", financeDesk},
		{"Invoices & billing", "Invoices from providers and billing questions", "factura, facturación, cobro", financeDesk},
		{"Access & permissions", "Access to systems and permission changes", "acceso, permisos, contraseña", itSupport},
		{"Onboarding", "New hire onboarding and first day setup", "ingreso, alta, incorporación", peopleDesk},
	}
	ids := make(map[string]int64, len(rows))
	for _, row := range rows {
		category := &domain.Category{Name: row.name, Description: row.description, Keywords: row.keywords, DeskID: row.deskID, CreatedAt: suggestionSeedTime}
		if err := categories.Create(ctx, category); err != nil {
			t.Fatalf("create category %q: %v", row.name, err)
		}
		ids[row.name] = category.ID
	}
	return s, ids
}

// allIDs returns every catalog id, which is what the requester's picker allows on
// a fully published catalog.
func allIDs(ids map[string]int64) []int64 {
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		out = append(out, id)
	}
	return out
}

// suggestedNames runs the local engine end to end and returns the suggested
// category names best first.
func suggestedNames(t *testing.T, s *Store, text string, allowed []int64) []string {
	t.Helper()
	suggester := application.NewLocalCategorySuggester(s.CategorySuggestionIndex())
	suggestions, err := suggester.Suggest(context.Background(), text, allowed)
	if err != nil {
		t.Fatalf("Suggest(%q): %v", text, err)
	}
	names := make([]string, len(suggestions))
	for index, suggestion := range suggestions {
		category, err := s.CategoryStore().GetByID(context.Background(), suggestion.CategoryID)
		if err != nil {
			t.Fatalf("read suggested category %d: %v", suggestion.CategoryID, err)
		}
		names[index] = category.Name
	}
	return names
}

// TestCategorySuggestionIndexSuggestsWhatTheTextNames is the acceptance criterion
// of S4: a description that shares words or keywords with a category suggests it,
// through the indexed name, keyword or description, and capitals and accents do
// not matter on either side.
func TestCategorySuggestionIndexSuggestsWhatTheTextNames(t *testing.T) {
	s, ids := newSuggestionCatalog(t)
	allowed := allIDs(ids)

	cases := []struct {
		name string
		text string
		want string
	}{
		{name: "a keyword", text: "la impresora del tercer piso no anda", want: "Printers"},
		{name: "an accented keyword typed without accents", text: "necesito la conexion a la red", want: "Network & Wi-Fi"},
		{name: "an accented keyword typed in capitals", text: "CONEXIÓN", want: "Network & Wi-Fi"},
		{name: "a description word", text: "problems with my laptop battery", want: "Laptops & computers"},
		{name: "an accented name typed without accents", text: "cuando me pagan la nomina", want: "Payroll"},
		{name: "an accented description word typed without accents", text: "quiero pedir viaticos para un viaje", want: "Expenses & reimbursement"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := suggestedNames(t, s, testCase.text, allowed)
			if len(got) == 0 {
				t.Fatalf("Suggest(%q) = nothing, want %q", testCase.text, testCase.want)
			}
			if got[0] != testCase.want {
				t.Fatalf("Suggest(%q) = %v, want %q first", testCase.text, got, testCase.want)
			}
		})
	}
}

// TestCategorySuggestionIndexToleratesATypoOfAKeyword proves the trigram path: a
// keyword typed with a missing letter still reaches its category.
func TestCategorySuggestionIndexToleratesATypoOfAKeyword(t *testing.T) {
	s, ids := newSuggestionCatalog(t)

	got := suggestedNames(t, s, "impresra", allIDs(ids))
	if len(got) == 0 || got[0] != "Printers" {
		t.Fatalf("Suggest(typo) = %v, want Printers", got)
	}
	// The typo is reported as the word the index actually holds, because that is the
	// word the requester's text reached.
	suggester := application.NewLocalCategorySuggester(s.CategorySuggestionIndex())
	suggestions, err := suggester.Suggest(context.Background(), "impresra", allIDs(ids))
	if err != nil {
		t.Fatalf("Suggest: %v", err)
	}
	if len(suggestions) != 1 || fmt.Sprint(suggestions[0].MatchedWords) != fmt.Sprint([]string{"impresora"}) {
		t.Fatalf("matched words = %+v, want the corrected word", suggestions)
	}
}

// TestCategorySuggestionIndexSuggestsNothingForUnrelatedText is the other half of
// S4 and all of S3: text that names no category suggests nothing, so the catalog
// behaves exactly as it does today.
func TestCategorySuggestionIndexSuggestsNothingForUnrelatedText(t *testing.T) {
	s, ids := newSuggestionCatalog(t)

	for _, text := range []string{
		"el clima de mañana estará nublado",
		"the meeting is about something",
		"zzz qqq xxx",
	} {
		if got := suggestedNames(t, s, text, allIDs(ids)); len(got) != 0 {
			t.Errorf("Suggest(%q) = %v, want nothing", text, got)
		}
	}
}

// TestCategorySuggestionIndexIgnoresAWordEveryCategoryShares proves the ranking's
// refusal to suggest on a non-discriminating word: a term present in every
// category has no IDF left, scores nothing, and the local engine keeps quiet.
func TestCategorySuggestionIndexIgnoresAWordEveryCategoryShares(t *testing.T) {
	ctx := context.Background()
	s := newTestDB(t)
	catalog, deskStore, categories := s.CatalogStore(), s.DeskStore(), s.CategoryStore()
	department := &domain.Department{Name: "General support", CreatedAt: suggestionSeedTime}
	if err := catalog.CreateDepartment(ctx, department); err != nil {
		t.Fatalf("create department: %v", err)
	}
	desk := &domain.Desk{Name: "Helpdesk", CreatedAt: suggestionSeedTime, DepartmentID: &department.ID}
	if err := deskStore.Create(ctx, desk); err != nil {
		t.Fatalf("create desk: %v", err)
	}
	var allowed []int64
	for _, name := range []string{"Printers", "Payroll", "Onboarding"} {
		category := &domain.Category{Name: name, Description: "soporte general para el equipo", Keywords: "soporte, ayuda", DeskID: desk.ID, CreatedAt: suggestionSeedTime}
		if err := categories.Create(ctx, category); err != nil {
			t.Fatalf("create category %q: %v", name, err)
		}
		allowed = append(allowed, category.ID)
	}

	if got := suggestedNames(t, s, "necesito soporte y ayuda", allowed); len(got) != 0 {
		t.Fatalf("Suggest = %v, want nothing for words every category shares", got)
	}
}

// TestCategorySuggestionIndexRespectsTheAllowedSet proves a category the requester
// cannot pick is never suggested, even when it matches best: the picker only offers
// categories with a published workflow.
func TestCategorySuggestionIndexRespectsTheAllowedSet(t *testing.T) {
	s, ids := newSuggestionCatalog(t)
	disallowed := ids["Printers"]

	filtered := make([]int64, 0, len(ids))
	for _, id := range allIDs(ids) {
		if id != disallowed {
			filtered = append(filtered, id)
		}
	}

	got := suggestedNames(t, s, "la impresora no imprime", filtered)
	for _, name := range got {
		if name == "Printers" {
			t.Fatalf("Suggest %v returned a category outside the allowed set", got)
		}
	}
}

// TestCategorySuggestionIndexNeverSuggestsMoreThanThree proves the ceiling end to
// end: a broad description that names several categories still offers at most
// three, and only categories whose score is in the same league as the best one.
func TestCategorySuggestionIndexNeverSuggestsMoreThanThree(t *testing.T) {
	s, ids := newSuggestionCatalog(t)

	for _, text := range []string{
		"impresora, wifi, laptop, nomina, vacaciones, factura, permisos, onboarding, gastos",
		"no funciona nada en la oficina: la red, la impresora y la computadora",
	} {
		got := suggestedNames(t, s, text, allIDs(ids))
		if len(got) > application.CategorySuggestionLimit {
			t.Fatalf("Suggest(%q) = %v, want at most %d", text, got, application.CategorySuggestionLimit)
		}
	}
}

// TestCategorySuggestionIndexReflectsKeywordEditsImmediately proves the index is a
// projection of the catalog rather than a cache: an administrator's keyword edit
// changes what the very next suggestion matches, both ways.
func TestCategorySuggestionIndexReflectsKeywordEditsImmediately(t *testing.T) {
	ctx := context.Background()
	s, ids := newSuggestionCatalog(t)
	payroll, err := s.CategoryStore().GetByID(ctx, ids["Payroll"])
	if err != nil {
		t.Fatalf("read payroll category: %v", err)
	}

	if got := suggestedNames(t, s, "no me pagaron el aguinaldo", allIDs(ids)); len(got) != 0 {
		t.Fatalf("Suggest before the edit = %v, want nothing", got)
	}

	payroll.Keywords = "nómina, sueldo, aguinaldo, bono"
	if err := s.CategoryStore().Update(ctx, payroll); err != nil {
		t.Fatalf("update keywords: %v", err)
	}
	if got := suggestedNames(t, s, "no me pagaron el aguinaldo", allIDs(ids)); len(got) == 0 || got[0] != "Payroll" {
		t.Fatalf("Suggest after the edit = %v, want Payroll", got)
	}

	// Removing the keyword takes the match away again: no stale term survives.
	payroll.Keywords = "nómina, sueldo"
	if err := s.CategoryStore().Update(ctx, payroll); err != nil {
		t.Fatalf("update keywords: %v", err)
	}
	if got := suggestedNames(t, s, "no me pagaron el aguinaldo", allIDs(ids)); len(got) != 0 {
		t.Fatalf("Suggest after removing the keyword = %v, want nothing", got)
	}
}

// TestCategorySuggestionIndexReportsEveryMatchedWord proves the words a picker
// labels a suggestion with: the normalized query words that category's indexed text
// contains, in the order the requester wrote them, and none of the ones it does not.
func TestCategorySuggestionIndexReportsEveryMatchedWord(t *testing.T) {
	s, ids := newSuggestionCatalog(t)

	suggestions, err := s.CategorySuggestionIndex().SuggestCandidates(context.Background(),
		"La IMPRESORA hace ruido, el tóner está vacío y la red anda mal", []int64{ids["Printers"], ids["Network & Wi-Fi"]})
	if err != nil {
		t.Fatalf("SuggestCandidates: %v", err)
	}
	if len(suggestions) == 0 {
		t.Fatal("SuggestCandidates returned nothing for a description that names two categories")
	}
	words := make(map[int64]string, len(suggestions))
	for _, suggestion := range suggestions {
		words[suggestion.CategoryID] = strings.Join(suggestion.MatchedWords, "|")
	}
	if got := words[ids["Printers"]]; got != "impresora|toner" {
		t.Errorf("Printers matched words = %q, want %q", got, "impresora|toner")
	}
	if got := words[ids["Network & Wi-Fi"]]; got != "red" {
		t.Errorf("Network matched words = %q, want %q", got, "red")
	}
}
