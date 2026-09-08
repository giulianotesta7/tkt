package httpadapter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
)

func TestCatalogCategoryDescriptionCanBeBlankOnCreateAndEdit(t *testing.T) {
	h := newHarness(t)
	catalog := application.NewCatalogService(h.store.CatalogStore(), h.store.CategoryStore(), h.clock)
	department, err := catalog.CreateDepartmentFor(t.Context(), *h.admin, "Operations", "")
	if err != nil {
		t.Fatalf("Create department: %v", err)
	}
	desk, err := h.desks.CreateInDepartment(t.Context(), *h.admin, department.ID, "Support")
	if err != nil {
		t.Fatalf("Create desk: %v", err)
	}
	handler := NewCategoryHandlersWithWorkflows(h.categories, h.workflows, h.renderer, catalog)
	mux := http.NewServeMux()
	handler.Register(mux)
	deskID := strconv.FormatInt(desk.ID, 10)

	departmentID := strconv.FormatInt(department.ID, 10)
	createForm := url.Values{"name": {"Blank description"}, "description": {""}, "department_id": {departmentID}, "desk_id": {deskID}}
	createRequest := httptest.NewRequest(http.MethodPost, "/categories", strings.NewReader(createForm.Encode()))
	createRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	createRequest = createRequest.WithContext(context.WithValue(createRequest.Context(), ctxKeyUser{}, h.admin))
	createResponse := httptest.NewRecorder()
	mux.ServeHTTP(createResponse, createRequest)
	if createResponse.Code != http.StatusSeeOther {
		t.Fatalf("create status = %d, want 303; body = %s", createResponse.Code, createResponse.Body.String())
	}

	categories, err := h.categories.List(t.Context())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var createdID int64
	for _, category := range categories {
		if category.Name == "Blank description" {
			createdID = category.ID
			if category.Description != "" {
				t.Fatalf("created description = %q, want empty", category.Description)
			}
		}
	}
	if createdID == 0 {
		t.Fatal("created category was not found")
	}

	updateForm := url.Values{"name": {"Blank description"}, "description": {""}, "department_id": {departmentID}, "desk_id": {deskID}}
	updateRequest := httptest.NewRequest(http.MethodPost, "/categories/"+strconv.FormatInt(createdID, 10)+"/edit", strings.NewReader(updateForm.Encode()))
	updateRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	updateRequest = updateRequest.WithContext(context.WithValue(updateRequest.Context(), ctxKeyUser{}, h.admin))
	updateResponse := httptest.NewRecorder()
	mux.ServeHTTP(updateResponse, updateRequest)
	if updateResponse.Code != http.StatusSeeOther {
		t.Fatalf("update status = %d, want 303; body = %s", updateResponse.Code, updateResponse.Body.String())
	}

	updated, err := h.categories.GetByID(t.Context(), createdID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if updated.Description != "" {
		t.Fatalf("updated description = %q, want empty", updated.Description)
	}
	if updated.DeskID != desk.ID {
		t.Fatalf("updated desk ID = %d, want %d", updated.DeskID, desk.ID)
	}
}
