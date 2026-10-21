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

// Issue #345: every category, department and desk mutation re-renders the
// operator's view from the submitted view state (view, department_id,
// desk_id). That state used to be validated only after the write, so a
// malformed state answered 422 for a change that was already committed. These
// tests pin that an invalid view state is refused before anything is written.

func categoryMutationHarness(t *testing.T) (*harness, http.Handler, *application.CatalogService) {
	t.Helper()
	h := newHarness(t)
	catalog := application.NewCatalogService(h.store.CatalogStore(), h.store.CategoryStore(), h.clock)
	mux := http.NewServeMux()
	NewCategoryHandlersWithWorkflows(h.categories, h.workflows, h.renderer, catalog).Register(mux)
	return h, h.mw.Wrap(mux), catalog
}

func postCategoryMutation(t *testing.T, h *harness, handler http.Handler, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Cookie", sessionCookie+"="+h.adminSession.ID)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func TestCategoryCreateWithADeskButNoDepartmentWritesNothing(t *testing.T) {
	h, handler, catalog := categoryMutationHarness(t)
	department, err := catalog.CreateDepartmentFor(t.Context(), *h.admin, "Operations", "")
	if err != nil {
		t.Fatalf("create department: %v", err)
	}
	desk, err := catalog.CreateDeskFor(t.Context(), *h.admin, department.ID, "Service desk")
	if err != nil {
		t.Fatalf("create desk: %v", err)
	}
	before := scanOneInt(t, h.rawDB(t), `SELECT COUNT(*) FROM categories`)

	for _, htmx := range []bool{false, true} {
		form := url.Values{"name": {"Probe"}, "desk_id": {strconv.FormatInt(desk.ID, 10)}}
		req := httptest.NewRequest(http.MethodPost, "/categories", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Cookie", sessionCookie+"="+h.adminSession.ID)
		if htmx {
			req.Header.Set("HX-Request", "true")
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("htmx=%v: status = %d, want 422", htmx, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "department is required when a desk is selected") {
			t.Errorf("htmx=%v: body = %q, want the department error", htmx, rec.Body.String())
		}
	}
	if after := scanOneInt(t, h.rawDB(t), `SELECT COUNT(*) FROM categories`); after != before {
		t.Errorf("categories = %d after a refused create, want %d: the 422 must not accompany a write", after, before)
	}
	if n := scanOneInt(t, h.rawDB(t), `SELECT COUNT(*) FROM categories WHERE name = 'Probe'`); n != 0 {
		t.Errorf("the refused category was stored (%d rows)", n)
	}
}

func TestCategoryUpdateWithAnInvalidViewStateWritesNothing(t *testing.T) {
	h, handler, catalog := categoryMutationHarness(t)
	department, err := catalog.CreateDepartmentFor(t.Context(), *h.admin, "Operations", "")
	if err != nil {
		t.Fatalf("create department: %v", err)
	}
	desk, err := catalog.CreateDeskFor(t.Context(), *h.admin, department.ID, "Service desk")
	if err != nil {
		t.Fatalf("create desk: %v", err)
	}
	category, err := h.categories.CreateWithDescriptionFor(t.Context(), *h.admin, "Original", "", desk.ID)
	if err != nil {
		t.Fatalf("create category: %v", err)
	}
	rec := postCategoryMutation(t, h, handler, "/categories/"+strconv.FormatInt(category.ID, 10)+"/edit",
		url.Values{"name": {"Renamed"}, "desk_id": {strconv.FormatInt(desk.ID, 10)}})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", rec.Code)
	}
	if got, _ := h.categories.GetByID(t.Context(), category.ID); got == nil || got.Name != "Original" {
		t.Errorf("category after a refused update = %+v, want the name unchanged", got)
	}
}

func TestDepartmentCreateWithAnInvalidViewStateWritesNothing(t *testing.T) {
	h, handler, _ := categoryMutationHarness(t)
	before := scanOneInt(t, h.rawDB(t), `SELECT COUNT(*) FROM departments`)
	rec := postCategoryMutation(t, h, handler, "/categories/departments",
		url.Values{"name": {"Finance"}, "view": {"nonsense"}})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", rec.Code)
	}
	if after := scanOneInt(t, h.rawDB(t), `SELECT COUNT(*) FROM departments`); after != before {
		t.Errorf("departments = %d after a refused create, want %d", after, before)
	}
}

// PRESERVE: a well-formed create still succeeds and redirects to the view the
// operator was on.
func TestCategoryCreateWithDepartmentAndDeskStillSucceeds(t *testing.T) {
	h, handler, catalog := categoryMutationHarness(t)
	department, err := catalog.CreateDepartmentFor(t.Context(), *h.admin, "Operations", "")
	if err != nil {
		t.Fatalf("create department: %v", err)
	}
	desk, err := catalog.CreateDeskFor(t.Context(), *h.admin, department.ID, "Service desk")
	if err != nil {
		t.Fatalf("create desk: %v", err)
	}
	rec := postCategoryMutation(t, h, handler, "/categories", url.Values{
		"name": {"Probe"}, "view": {"structure"},
		"department_id": {strconv.FormatInt(department.ID, 10)}, "desk_id": {strconv.FormatInt(desk.ID, 10)},
	})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303: %s", rec.Code, rec.Body.String())
	}
	want := "/categories?department_id=" + strconv.FormatInt(department.ID, 10) + "&desk_id=" + strconv.FormatInt(desk.ID, 10) + "&view=structure"
	if loc := rec.Header().Get("Location"); loc != want {
		t.Errorf("Location = %q, want %q", loc, want)
	}
	if n := scanOneInt(t, h.rawDB(t), `SELECT COUNT(*) FROM categories WHERE name = 'Probe'`); n != 1 {
		t.Errorf("stored categories named Probe = %d, want 1", n)
	}
}
