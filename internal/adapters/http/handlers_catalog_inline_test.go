package httpadapter

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
)

func TestCatalogInlineWrites(t *testing.T) {
	h := newHarness(t)
	parent := seedFolder(t, h, nil, "People á")
	child := seedFolder(t, h, &parent, "東京 😀")
	folder := seedFolder(t, h, nil, "Folder")
	category, err := h.categories.CreateInFolderFor(t.Context(), *h.admin, "Category", "description", "keywords", nil)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		path                       string
		form                       url.Values
		event, kind, name, message string
		entity                     int64
		folder, from               int64
		pathName                   string
	}{
		{"/categories", url.Values{"name": {"Created categoría 😀"}}, "catalog:created", "category", "Created categoría 😀", "Created category 'Created categoría 😀'", 0, 0, 0, ""},
		{"/categories/folders", url.Values{"name": {"Created carpeta 😀"}}, "catalog:created", "folder", "Created carpeta 😀", "Created folder 'Created carpeta 😀'", 0, 0, 0, ""},
		{"/categories/" + id(category.ID) + "/edit", url.Values{"name": {"Categoría 😀"}, "description": {"Changed"}}, "catalog:saved", "category", "Categoría 😀", "Saved 'Categoría 😀'", category.ID, 0, 0, ""},
		{"/categories/folders/" + id(folder) + "/edit", url.Values{"name": {"Carpeta 😀"}, "description": {"Changed"}}, "catalog:saved", "folder", "Carpeta 😀", "Saved 'Carpeta 😀'", folder, 0, 0, ""},
		{"/categories/" + id(category.ID) + "/move", url.Values{"folder_id": {id(child)}}, "catalog:moved", "category", "Categoría 😀", "Moved 'Categoría 😀' to People á › 東京 😀", category.ID, child, 0, "People á › 東京 😀"},
		{"/categories/folders/" + id(folder) + "/move", url.Values{"folder_id": {id(parent)}}, "catalog:moved", "folder", "Carpeta 😀", "Moved 'Carpeta 😀' to People á", folder, parent, 0, "People á"},
		{"/categories/" + id(category.ID) + "/delete", url.Values{}, "catalog:deleted", "category", "Categoría 😀", "Deleted 'Categoría 😀'", category.ID, 0, 0, ""},
		{"/categories/folders/" + id(folder) + "/delete", url.Values{}, "catalog:deleted", "folder", "Carpeta 😀", "Deleted 'Carpeta 😀'", folder, 0, 0, ""},
	}
	for _, tc := range cases {
		t.Run(tc.event+"/"+tc.kind+"/"+tc.path, func(t *testing.T) {
			tc.form.Set("inline", "1")
			rec := h.postForm(t, tc.path, tc.form, false)
			if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html") || !strings.Contains(rec.Body.String(), `id="categories-background"`) {
				t.Errorf("response %d %q %.100s", rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
			}
			trigger := rec.Header().Get("HX-Trigger")
			for _, b := range []byte(trigger) {
				if b >= 0x80 {
					t.Errorf("HX-Trigger must be ASCII-only: %q", trigger)
					break
				}
			}
			var events map[string]map[string]any
			if err := json.Unmarshal([]byte(trigger), &events); err != nil {
				t.Errorf("trigger: %v", err)
				return
			}
			e := events[tc.event]
			if len(events) != 1 || e["kind"] != tc.kind || e["name"] != tc.name || e["message"] != tc.message || e["id"] == nil || (tc.entity != 0 && e["id"] != float64(tc.entity)) {
				t.Errorf("event %#v", events)
			}
			if tc.event == "catalog:moved" && (e["folderID"] != float64(tc.folder) || e["fromFolderID"] != float64(tc.from) || e["path"] != tc.pathName) {
				t.Errorf("move %#v", e)
			}
			if tc.event == "catalog:created" && e["folderID"] != float64(tc.folder) {
				t.Errorf("create %#v", e)
			}
		})
	}
}

func TestCatalogInlineRefusals(t *testing.T) {
	h := newHarness(t)
	parent := seedFolder(t, h, nil, "Parent")
	child := seedFolder(t, h, &parent, "Child")
	cases := []struct {
		path    string
		form    url.Values
		status  int
		field   string
		message string
	}{
		{"/categories/folders", url.Values{"name": {"Parent"}}, 409, "name", "A folder with that name already exists here."},
		{"/categories", url.Values{"name": {h.bugCategory.Name}}, 409, "name", "A category with that name already exists."},
		{"/categories/folders/" + id(parent) + "/move", url.Values{"folder_id": {id(child)}}, 422, "folder_id", "A folder can't move into itself or one of its subfolders."},
		{"/categories/folders/" + id(parent) + "/delete", url.Values{}, 409, "", "This folder isn't empty. Move or delete its folders and categories first."},
	}
	before := folderCount(t, h)
	categories, err := h.categories.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			tc.form.Set("inline", "1")
			rec := h.postForm(t, tc.path, tc.form, false)
			var body struct {
				Error struct {
					Message string `json:"message"`
					Field   string `json:"field"`
				} `json:"error"`
			}
			err := json.Unmarshal(rec.Body.Bytes(), &body)
			if rec.Code != tc.status || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") || err != nil || body.Error.Message != tc.message || body.Error.Field != tc.field {
				t.Errorf("refusal %d %s %s: %v", rec.Code, rec.Header().Get("Content-Type"), rec.Body.String(), err)
			}
			after, e := h.categories.List(t.Context())
			if e != nil || len(after) != len(categories) || folderCount(t, h) != before || folderParent(t, h, child) != parent {
				t.Error("refusal changed stored rows")
			}
		})
	}
}

func TestCatalogInlineDuplicateSpanish(t *testing.T) {
	h := newHarness(t)
	seedFolder(t, h, nil, "People")
	if err := h.store.SettingsStore().SetInterfaceLanguage(t.Context(), application.InterfaceLanguageSpanish); err != nil {
		t.Fatal(err)
	}
	rec := h.postForm(t, "/categories/folders", url.Values{"name": {"People"}, "inline": {"1"}}, false)
	var body struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || rec.Code != 409 || body.Error.Message != "Ya existe una carpeta con ese nombre aquí." {
		t.Errorf("refusal %d %s: %v", rec.Code, rec.Body.String(), err)
	}
}

func TestCatalogInlinePreservesNonInlineCreate(t *testing.T) {
	h := newHarness(t)
	rec := h.postForm(t, "/categories/folders", url.Values{"name": {"Ordinary"}}, false)
	wantRedirect(t, rec, http.StatusSeeOther, "/categories")
}
