package httpadapter

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

// The category drawer's suggestion keywords (issue #416, S4): the field an
// administrator fills so the local engine can match a requester's words, wired
// through create, edit and the error re-render. These are HTTP-layer facts — what
// the drawer offers, what a submit stores, and what a refusal shows — so they run
// against the real harness, database and templates.

// keywordsFormValues builds the category drawer form with a keywords value.
func keywordsFormValues(t *testing.T, name, keywords string, departmentID, deskID int64) url.Values {
	t.Helper()
	return url.Values{
		"name":          {name},
		"description":   {"Requests about " + name},
		"keywords":      {keywords},
		"department_id": {strconv.FormatInt(departmentID, 10)},
		"desk_id":       {strconv.FormatInt(deskID, 10)},
	}
}

// TestCategoryDrawerOffersSuggestionKeywords proves the drawer renders the field
// with the approved copy, for a category only: a department and a desk have no
// keywords.
func TestCategoryDrawerOffersSuggestionKeywords(t *testing.T) {
	h := newHarness(t)
	departmentID, _ := h.seededDesk(t)

	rec := h.get(t, "/categories/new?view=structure", true)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`name="keywords"`,
		"Keywords for suggestions",
		"Words people use for this category. Comma separated; accents and capitals do not matter.",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the category drawer must render %q", want)
		}
	}

	for _, path := range []string{
		"/categories/departments/new?view=structure",
		"/categories/desks/new?view=structure&department_id=" + strconv.FormatInt(departmentID, 10),
	} {
		rec := h.get(t, path, true)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d, want 200", path, rec.Code)
		}
		if strings.Contains(rec.Body.String(), `name="keywords"`) {
			t.Errorf("%s must not offer suggestion keywords", path)
		}
	}
}

// TestCategoryKeywordsAreStoredShownAndUpdated proves the whole round trip: create
// stores the keywords as typed, the edit drawer shows them back, and an update
// replaces them.
func TestCategoryKeywordsAreStoredShownAndUpdated(t *testing.T) {
	h := newHarness(t)
	departmentID, deskID := h.seededDesk(t)

	create := h.postForm(t, "/categories", keywordsFormValues(t, "Printers", " impresora, tóner ", departmentID, deskID), false)
	wantRedirect(t, create, http.StatusSeeOther, "/categories")

	created, ok := categoryNamed(t, h, "Printers")
	if !ok {
		t.Fatal("created category must be readable")
	}
	if created.Keywords != "impresora, tóner" {
		t.Fatalf("stored keywords = %q, want them trimmed and otherwise as typed", created.Keywords)
	}

	edit := h.get(t, "/categories/"+strconv.FormatInt(created.ID, 10)+"/edit?view=structure", true)
	if edit.Code != http.StatusOK {
		t.Fatalf("edit status = %d, want 200", edit.Code)
	}
	if !strings.Contains(edit.Body.String(), `value="impresora, tóner"`) {
		t.Errorf("the edit drawer must show the stored keywords, got: %s", edit.Body.String())
	}

	update := h.postForm(t, "/categories/"+strconv.FormatInt(created.ID, 10)+"/edit",
		keywordsFormValues(t, "Printers", "impresora, escáner", departmentID, deskID), false)
	wantRedirect(t, update, http.StatusSeeOther, "/categories")
	updated, err := h.categories.GetByID(t.Context(), created.ID)
	if err != nil {
		t.Fatalf("read updated category: %v", err)
	}
	if updated.Keywords != "impresora, escáner" {
		t.Fatalf("updated keywords = %q, want the new value", updated.Keywords)
	}
}

// TestCategoryKeywordsBlankIsAccepted proves the field is optional: an
// administrator who does not use keywords still saves the category.
func TestCategoryKeywordsBlankIsAccepted(t *testing.T) {
	h := newHarness(t)
	departmentID, deskID := h.seededDesk(t)

	rec := h.postForm(t, "/categories", keywordsFormValues(t, "Printers", "   ", departmentID, deskID), false)
	wantRedirect(t, rec, http.StatusSeeOther, "/categories")

	created, ok := categoryNamed(t, h, "Printers")
	if !ok {
		t.Fatal("created category must be readable")
	}
	if created.Keywords != "" {
		t.Fatalf("stored keywords = %q, want empty", created.Keywords)
	}
}

// TestCategoryKeywordsRefusalKeepsTheSubmittedValue proves a refused edit re-renders
// the drawer with the submitted keywords, so a long paste is corrected instead of
// lost. The bound is server-side: the drawer's maxlength is a hint a form post can
// bypass.
func TestCategoryKeywordsRefusalKeepsTheSubmittedValue(t *testing.T) {
	h := newHarness(t)
	departmentID, deskID := h.seededDesk(t)

	tooLong := strings.Repeat("a", 501)
	rec := h.postForm(t, "/categories", keywordsFormValues(t, "Printers", tooLong, departmentID, deskID), false)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "500 characters or fewer") {
		t.Errorf("the re-render must explain the bound, got: %s", body)
	}
	if !strings.Contains(body, `value="`+tooLong+`"`) {
		t.Error("the re-render must keep the submitted keywords")
	}
	if _, ok := categoryNamed(t, h, "Printers"); ok {
		t.Error("a refused create must not store the category")
	}
}
