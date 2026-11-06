package httpadapter

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

func TestCatalogInlineRenameAndDetails(t *testing.T) {
	h := newHarness(t)
	parent := seedFolder(t, h, nil, "Parent")
	folder := seedFolder(t, h, &parent, "Folder")
	service := application.NewFolderService(h.store.FolderStore(), testClock{})
	_, err := service.UpdateFor(t.Context(), *h.admin, folder, "Folder", "Folder description")
	if err != nil {
		t.Fatal(err)
	}
	c, err := h.categories.CreateInFolderFor(t.Context(), *h.admin, "Category", "Description", "suggestion", &folder)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path, kind, name string
		entity           int64
	}{
		{"/categories/" + id(c.ID), "category", "New category", c.ID},
		{"/categories/folders/" + id(folder), "folder", "New folder", folder},
	} {
		rec := h.postForm(t, tc.path+"/rename", url.Values{"inline": {"1"}, "name": {tc.name}}, false)
		var event map[string]map[string]any
		err := json.Unmarshal([]byte(rec.Header().Get("HX-Trigger")), &event)
		e := event["catalog:renamed"]
		if rec.Code != 200 || err != nil || len(event) != 1 || e["kind"] != tc.kind || e["id"] != float64(tc.entity) || e["name"] != tc.name || e["message"] != "Renamed to '"+tc.name+"'" {
			t.Errorf("rename %s: %d %#v %v", tc.path, rec.Code, event, err)
		}
	}
	updated, err := h.categories.GetByID(t.Context(), c.ID)
	if err != nil || updated.Description != "Description" || updated.Keywords != "suggestion" || folderValue(updated.FolderID) != folder {
		t.Errorf("category changed: %#v %v", updated, err)
	}
	tree, err := service.Tree(t.Context())
	f, found := tree.Folder(folder)
	if err != nil || !found || f.Description != "Folder description" || folderValue(f.ParentID) != parent {
		t.Errorf("folder changed: %#v %v", f, err)
	}
	for _, tc := range []struct {
		path  string
		wants []string
	}{
		{"/categories/" + id(c.ID) + "/details", []string{"Description", "suggestion", `name="inline" value="1"`, `<label for="details-name-` + id(c.ID) + `">Name</label><input id="details-name-` + id(c.ID) + `" name="name" value="New category" required>`, `name="folder_id" value="` + id(folder) + `"`, "/categories/" + id(c.ID) + "/workflow", "/categories/" + id(c.ID) + "/sla", "/categories/" + id(c.ID) + "/edit", "/categories/" + id(c.ID) + "/delete", "data-details-cancel"}},
		{"/categories/folders/" + id(folder) + "/details", []string{"Folder description", "New folder", "disabled", "This folder isn&#39;t empty", "/categories/folders/" + id(folder) + "/edit"}},
	} {
		rec := h.get(t, tc.path, false)
		if rec.Code != 200 {
			t.Errorf("details %s = %d", tc.path, rec.Code)
		}
		for _, want := range tc.wants {
			if !strings.Contains(rec.Body.String(), want) {
				t.Errorf("%s missing %q", tc.path, want)
			}
		}
	}
	empty := seedFolder(t, h, nil, "Empty")
	body := h.get(t, "/categories/folders/"+id(empty)+"/details", false).Body.String()
	if strings.Contains(body, "disabled") || !strings.Contains(body, "/categories/folders/"+id(empty)+"/delete") {
		t.Errorf("empty folder details: %s", body)
	}
	for _, tc := range []struct {
		path, name string
		status     int
		field      string
	}{
		{"/categories/" + id(c.ID) + "/rename", "", 422, "name"},
		{"/categories/folders/" + id(folder) + "/rename", "Parent", 200, ""},
		{"/categories/folders/" + id(empty) + "/rename", "Parent", 409, "name"},
		{"/categories/999999/rename", "Missing", 404, ""},
	} {
		rec := h.postForm(t, tc.path, url.Values{"inline": {"1"}, "name": {tc.name}}, false)
		if rec.Code != tc.status {
			t.Errorf("rename refusal %s = %d", tc.path, rec.Code)
		}
		if tc.status != 200 {
			var body struct {
				Error struct {
					Field string `json:"field"`
				} `json:"error"`
			}
			if json.Unmarshal(rec.Body.Bytes(), &body) != nil || body.Error.Field != tc.field {
				t.Errorf("refusal body %s", rec.Body.String())
			}
		}
	}
	updated, _ = h.categories.GetByID(t.Context(), c.ID)
	if updated.Name != "New category" {
		t.Error("refusal changed category")
	}
	wantRedirect(t, h.postForm(t, "/categories/folders/"+id(empty)+"/rename", url.Values{"name": {"Renamed"}}, false), http.StatusSeeOther, "/categories")
	if got := scanOneString(t, h.rawDB(t), `SELECT name FROM folders WHERE id = ?`, empty); got != "Renamed" {
		t.Errorf("non-inline rename stored %q, want Renamed", got)
	}
	emptyName := "Renamed"
	agent := seedUserRole(t, h.store, "Agent", "inline-agent@tkt.test", domain.RoleAgent)
	session := seedSession(t, h.store, agent.ID)
	for _, path := range []string{"/categories/" + id(c.ID) + "/details", "/categories/folders/" + id(folder) + "/details"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Cookie", sessionCookie+"="+session.ID)
		rec := httptest.NewRecorder()
		h.mw.Wrap(h.mux).ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("agent %s = %d", path, rec.Code)
		}
	}
	// An agent cannot rename either, and the refused write changes nothing.
	for _, path := range []string{"/categories/" + id(c.ID) + "/rename", "/categories/folders/" + id(empty) + "/rename"} {
		if rec := h.postFormAs(t, path, url.Values{"inline": {"1"}, "name": {"Agent rename"}}, session.ID); rec.Code != http.StatusForbidden {
			t.Errorf("agent rename %s = %d", path, rec.Code)
		}
	}
	if updated, _ = h.categories.GetByID(t.Context(), c.ID); updated.Name != "New category" {
		t.Errorf("agent rename changed the category to %q", updated.Name)
	}
	if got := scanOneString(t, h.rawDB(t), `SELECT name FROM folders WHERE id = ?`, empty); got != emptyName {
		t.Errorf("refused renames changed the folder from %q to %q", emptyName, got)
	}
}

func TestCatalogInlineUnicodeRename(t *testing.T) {
	h := newHarness(t)
	folder := seedFolder(t, h, nil, "Rename folder")
	category, err := h.categories.CreateInFolderFor(t.Context(), *h.admin, "Rename category", "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path, kind, name string
		entity           int64
	}{
		{"/categories/" + id(category.ID) + "/rename", "category", "Categoría 😀", category.ID},
		{"/categories/folders/" + id(folder) + "/rename", "folder", "Carpeta 😀", folder},
	} {
		rec := h.postForm(t, tc.path, url.Values{"inline": {"1"}, "name": {tc.name}}, false)
		trigger := rec.Header().Get("HX-Trigger")
		for _, b := range []byte(trigger) {
			if b >= 0x80 {
				t.Errorf("HX-Trigger must be ASCII-only: %q", trigger)
				break
			}
		}
		var events map[string]map[string]any
		err := json.Unmarshal([]byte(trigger), &events)
		e := events["catalog:renamed"]
		if rec.Code != http.StatusOK || err != nil || len(events) != 1 || e["kind"] != tc.kind || e["id"] != float64(tc.entity) || e["name"] != tc.name || e["message"] != "Renamed to '"+tc.name+"'" {
			t.Errorf("rename %s: %d %#v %v", tc.path, rec.Code, events, err)
		}
	}
}

func TestCatalogInlineMoveTopAndSpanish(t *testing.T) {
	h := newHarness(t)
	parent := seedFolder(t, h, nil, "People")
	folder := seedFolder(t, h, &parent, "Payroll")
	path := "/categories/folders/" + id(folder) + "/move"
	rec := h.postForm(t, path, url.Values{"inline": {"1"}, "folder_id": {"0"}}, false)
	var event map[string]map[string]any
	if json.Unmarshal([]byte(rec.Header().Get("HX-Trigger")), &event) != nil || event["catalog:moved"]["path"] != "" || event["catalog:moved"]["message"] != "Moved 'Payroll' to the top level" || event["catalog:moved"]["fromFolderID"] != float64(parent) {
		t.Errorf("top move: %d %#v", rec.Code, event)
	}
	if err := h.store.SettingsStore().SetInterfaceLanguage(t.Context(), application.InterfaceLanguageSpanish); err != nil {
		t.Fatal(err)
	}
	rec = h.postForm(t, path, url.Values{"inline": {"1"}, "folder_id": {id(parent)}}, false)
	if json.Unmarshal([]byte(rec.Header().Get("HX-Trigger")), &event) != nil || event["catalog:moved"]["message"] != "'Payroll' movido a People" {
		t.Errorf("Spanish move: %d %#v", rec.Code, event)
	}
}
