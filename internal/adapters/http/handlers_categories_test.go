package httpadapter

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// A category's workflow steps decide which desk handles it (issue #491, S1), so
// no category form asks for a desk: create works with no desk at all, even when
// no desk exists, and the category lands in its folder.
func TestCategoryFormsCreateWithoutDesk(t *testing.T) {
	h := newHarness(t)
	if err := h.desks.Delete(t.Context(), *h.admin, deskIDNamed(t, h, "General")); err != nil {
		t.Fatal(err)
	}
	if desks, err := h.store.DeskStore().List(t.Context()); err != nil || len(desks) != 0 {
		t.Fatalf("desks = %v %v, want none", desks, err)
	}
	folder := seedFolder(t, h, nil, "IT services")

	for _, path := range []string{"/categories", "/categories/new?folder_id=" + id(folder)} {
		body := h.get(t, path, false).Body.String()
		if strings.Contains(body, `name="desk_id"`) || strings.Contains(body, `catalog-desk-options`) || strings.Contains(body, "Select a desk") {
			t.Errorf("%s still offers a category desk", path)
		}
	}

	rec := h.postForm(t, "/categories", url.Values{"name": {"Password reset"}, "folder_id": {id(folder)}, "folder": {id(folder)}}, false)
	wantRedirect(t, rec, http.StatusSeeOther, "/categories?folder="+id(folder))
	inline := h.postForm(t, "/categories", url.Values{"inline": {"1"}, "name": {"Locked out"}, "folder_id": {id(folder)}}, false)
	var events map[string]map[string]any
	if err := json.Unmarshal([]byte(inline.Header().Get("HX-Trigger")), &events); inline.Code != http.StatusOK || err != nil || events["catalog:created"]["folderID"] != float64(folder) {
		t.Errorf("inline create %d %q %v", inline.Code, inline.Header().Get("HX-Trigger"), err)
	}
	for _, name := range []string{"Password reset", "Locked out"} {
		cat := scanOneInt(t, h.rawDB(t), `SELECT id FROM categories WHERE name = ?`, name)
		if got, _ := scanOneNullableInt(t, h.rawDB(t), `SELECT folder_id FROM categories WHERE id = ?`, cat); got != folder {
			t.Errorf("%s folder = %d, want %d", name, got, folder)
		}
	}
}

// A stale form that still posts desk_id is accepted and the value is ignored.
func TestCategoryFormsIgnoreSubmittedDesk(t *testing.T) {
	h := newHarness(t)
	other, err := h.desks.Create(t.Context(), *h.admin, "Other")
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := h.categories.CreateInFolderFor(t.Context(), *h.admin, "Legacy", "", "", nil)
	if err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{"/categories/" + id(legacy.ID) + "/edit", "/categories/" + id(legacy.ID) + "/details"} {
		if body := h.get(t, path, false).Body.String(); strings.Contains(body, `name="desk_id"`) || strings.Contains(body, "The desk responsible") {
			t.Errorf("%s still offers a category desk", path)
		}
	}

	for _, form := range []url.Values{
		{"name": {"Stale"}, "desk_id": {id(other.ID)}},
		{"name": {"Stale inline"}, "desk_id": {"not-a-desk"}, "inline": {"1"}},
	} {
		rec := h.postForm(t, "/categories", form, false)
		if rec.Code != http.StatusSeeOther && rec.Code != http.StatusOK {
			t.Errorf("create %v = %d %s", form, rec.Code, rec.Body.String())
		}
		if n := scanOneInt(t, h.rawDB(t), `SELECT COUNT(*) FROM categories WHERE name = ?`, form.Get("name")); n != 1 {
			t.Errorf("%s created %d categories, want 1", form.Get("name"), n)
		}
	}

	for _, form := range []url.Values{
		{"name": {"Legacy"}, "description": {"No-script"}},
		{"name": {"Legacy"}, "description": {"Inline"}, "desk_id": {id(other.ID)}, "inline": {"1"}},
	} {
		rec := h.postForm(t, "/categories/"+id(legacy.ID)+"/edit", form, false)
		if rec.Code != http.StatusSeeOther && rec.Code != http.StatusOK {
			t.Errorf("edit %v = %d %s", form, rec.Code, rec.Body.String())
		}
		updated, err := h.categories.GetByID(t.Context(), legacy.ID)
		if err != nil || updated.Description != form.Get("description") {
			t.Errorf("edit %v stored %#v %v", form, updated, err)
		}
	}
}
