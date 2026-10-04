package httpadapter

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
)

// catalogMuxRequest drives the category routes through a catalog-aware
// composition (the production wiring), authenticated as the harness admin.
func catalogMuxRequest(t *testing.T, h *harness, mux *http.ServeMux, method, target string, form url.Values, hx bool) *httptest.ResponseRecorder {
	t.Helper()
	var body *strings.Reader
	if form == nil {
		body = strings.NewReader("")
	} else {
		body = strings.NewReader(form.Encode())
	}
	req := httptest.NewRequest(method, target, body)
	req.Header.Set("Cookie", sessionCookie+"="+h.adminSession.ID)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if hx {
		req.Header.Set("HX-Request", "true")
	}
	rec := httptest.NewRecorder()
	h.mw.Wrap(mux).ServeHTTP(rec, req)
	return rec
}

// TestCategoryEditOnLegacyDeskPreservesLocation is issue #266. A legacy Desk
// keeps a NULL department_id and is reachable only through the virtual
// Unassigned group, so the category drawer has to offer that Desk and that
// virtual Department. Otherwise editing a legacy category cannot even
// resubmit the location it already has, and a plain rename silently relocates
// it to the first real Desk.
func TestCategoryEditOnLegacyDeskPreservesLocation(t *testing.T) {
	h := newHarness(t)
	legacyDesk, err := h.desks.CreateWithDescription(t.Context(), *h.admin, "Legacy desk", "Legacy description")
	if err != nil {
		t.Fatalf("create legacy desk: %v", err)
	}
	category, err := h.categories.CreateWithDescriptionFor(t.Context(), *h.admin, "Legacy category", "Legacy description", legacyDesk.ID)
	if err != nil {
		t.Fatalf("create legacy category: %v", err)
	}

	catalog := application.NewCatalogService(h.store.CatalogStore(), h.store.CategoryStore(), h.clock)
	mux := http.NewServeMux()
	NewCategoryHandlersWithWorkflows(h.categories, h.workflows, h.renderer, catalog).Register(mux)

	deskID := strconv.FormatInt(legacyDesk.ID, 10)
	drawerPath := "/categories/" + strconv.FormatInt(category.ID, 10) + "/edit?view=structure&department_id=unassigned&desk_id=" + deskID
	drawer := catalogMuxRequest(t, h, mux, http.MethodGet, drawerPath, nil, false)
	if drawer.Code != http.StatusOK {
		t.Fatalf("legacy category drawer = %d, want 200: %s", drawer.Code, drawer.Body.String())
	}
	body := drawer.Body.String()
	wantDeskOption := `<option value="` + deskID + `" data-department-id="unassigned" selected>`
	if !strings.Contains(body, wantDeskOption) {
		t.Errorf("legacy category drawer must offer its own desk as the selected option %q, got: %s", wantDeskOption, body)
	}
	wantDepartmentOption := `<option value="unassigned" selected>Unassigned</option>`
	if !strings.Contains(body, wantDepartmentOption) {
		t.Errorf("legacy category drawer must preselect the virtual Unassigned department %q, got: %s", wantDepartmentOption, body)
	}

	// A plain rename keeps the desk: the location is part of the submitted
	// form, and the update path stores exactly the desk_id it received.
	form := url.Values{
		"view":          {"structure"},
		"department_id": {"unassigned"},
		"desk_id":       {deskID},
		"name":          {"Legacy category renamed"},
		"description":   {"Legacy description"},
	}
	saved := catalogMuxRequest(t, h, mux, http.MethodPost, "/categories/"+strconv.FormatInt(category.ID, 10)+"/edit", form, true)
	if saved.Code != http.StatusOK {
		t.Fatalf("legacy category save = %d, want 200: %s", saved.Code, saved.Body.String())
	}
	wantPushURL := "/categories?department_id=unassigned&desk_id=" + deskID + "&view=structure"
	if got := saved.Header().Get("HX-Push-Url"); got != wantPushURL {
		t.Errorf("HX-Push-Url = %q, want %q", got, wantPushURL)
	}
	stored, err := h.categories.GetByID(t.Context(), category.ID)
	if err != nil {
		t.Fatalf("read saved category: %v", err)
	}
	if stored.DeskID != legacyDesk.ID {
		t.Fatalf("saved category desk = %d, want %d (no silent relocation)", stored.DeskID, legacyDesk.ID)
	}
	if stored.Name != "Legacy category renamed" {
		t.Fatalf("saved category name = %q, want the rename", stored.Name)
	}
}

// TestNewCategoryFromLegacyDeskKeepsContext is the other half of issue #266:
// the "New category" launcher carries the selected legacy desk, so the create
// drawer must preselect the virtual Unassigned Department and that Desk.
func TestNewCategoryFromLegacyDeskKeepsContext(t *testing.T) {
	h := newHarness(t)
	legacyDesk, err := h.desks.CreateWithDescription(t.Context(), *h.admin, "Legacy desk", "Legacy description")
	if err != nil {
		t.Fatalf("create legacy desk: %v", err)
	}

	catalog := application.NewCatalogService(h.store.CatalogStore(), h.store.CategoryStore(), h.clock)
	mux := http.NewServeMux()
	NewCategoryHandlersWithWorkflows(h.categories, h.workflows, h.renderer, catalog).Register(mux)

	deskID := strconv.FormatInt(legacyDesk.ID, 10)
	drawer := catalogMuxRequest(t, h, mux, http.MethodGet, "/categories/new?view=structure&department_id=unassigned&desk_id="+deskID, nil, false)
	if drawer.Code != http.StatusOK {
		t.Fatalf("legacy new-category drawer = %d, want 200: %s", drawer.Code, drawer.Body.String())
	}
	body := drawer.Body.String()
	if !strings.Contains(body, `<option value="unassigned" selected>Unassigned</option>`) {
		t.Errorf("new-category drawer must preselect the virtual Unassigned department, got: %s", body)
	}
	if !strings.Contains(body, `<option value="`+deskID+`" data-department-id="unassigned" selected>`) {
		t.Errorf("new-category drawer must preselect the launching legacy desk, got: %s", body)
	}

	// Creating from that context stores the legacy desk, not a silent default.
	form := url.Values{
		"view":          {"structure"},
		"department_id": {"unassigned"},
		"desk_id":       {deskID},
		"name":          {"Legacy child category"},
		"description":   {""},
	}
	created := catalogMuxRequest(t, h, mux, http.MethodPost, "/categories", form, true)
	if created.Code != http.StatusOK {
		t.Fatalf("legacy category create = %d, want 200: %s", created.Code, created.Body.String())
	}
	categories, err := h.categories.List(t.Context())
	if err != nil {
		t.Fatalf("list categories: %v", err)
	}
	for _, c := range categories {
		if c.Name != "Legacy child category" {
			continue
		}
		if c.DeskID != legacyDesk.ID {
			t.Fatalf("created category desk = %d, want %d", c.DeskID, legacyDesk.ID)
		}
		return
	}
	t.Fatalf("created legacy child category not found in %+v", categories)
}

// TestCategoryEditOnAssignedDeskPreselectsRealLocation guards the non-legacy
// half of issue #266: the drawer still preselects the real Department and Desk
// for a category that already lives in the hierarchy.
func TestCategoryEditOnAssignedDeskPreselectsRealLocation(t *testing.T) {
	h := newHarness(t)
	catalog := application.NewCatalogService(h.store.CatalogStore(), h.store.CategoryStore(), h.clock)
	department, err := catalog.CreateDepartmentFor(t.Context(), *h.admin, "Operations", "Operations department")
	if err != nil {
		t.Fatalf("create department: %v", err)
	}
	desk, err := catalog.CreateDeskFor(t.Context(), *h.admin, department.ID, "Support")
	if err != nil {
		t.Fatalf("create desk: %v", err)
	}
	category, err := h.categories.CreateWithDescriptionFor(t.Context(), *h.admin, "Requests", "Request category", desk.ID)
	if err != nil {
		t.Fatalf("create category: %v", err)
	}

	mux := http.NewServeMux()
	NewCategoryHandlersWithWorkflows(h.categories, h.workflows, h.renderer, catalog).Register(mux)

	departmentID := strconv.FormatInt(department.ID, 10)
	deskID := strconv.FormatInt(desk.ID, 10)
	drawerPath := "/categories/" + strconv.FormatInt(category.ID, 10) + "/edit?view=structure&department_id=" + departmentID + "&desk_id=" + deskID
	drawer := catalogMuxRequest(t, h, mux, http.MethodGet, drawerPath, nil, false)
	if drawer.Code != http.StatusOK {
		t.Fatalf("assigned category drawer = %d, want 200: %s", drawer.Code, drawer.Body.String())
	}
	body := drawer.Body.String()
	if !strings.Contains(body, `<option value="`+departmentID+`" selected>Operations</option>`) {
		t.Errorf("assigned category drawer must preselect the real department, got: %s", body)
	}
	if !strings.Contains(body, `<option value="`+deskID+`" data-department-id="`+departmentID+`" selected>`) {
		t.Errorf("assigned category drawer must preselect the real desk, got: %s", body)
	}
}
