package httpadapter

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

func TestTicketCatalogDirectSelectionRejectsUnpublishedCategory(t *testing.T) {
	h := newHarness(t)
	unpublished, err := h.categories.Create(t.Context(), "Draft only")
	if err != nil {
		t.Fatalf("create unpublished category: %v", err)
	}
	if err := h.workflows.SaveDraft(t.Context(), *h.admin, unpublished.ID, simpleManualDef()); err != nil {
		t.Fatalf("save draft: %v", err)
	}

	catalog := application.NewCatalogService(h.store.CatalogStore(), h.store.CategoryStore(), h.clock)
	mux := http.NewServeMux()
	NewTicketHandlers(h.tickets, h.comments, h.search, h.categories, h.users, h.store.DeskStore(), h.workflows, application.NewWorkflowRunner(h.clock), h.store.WorkflowRunStore(), h.store.WorkflowUnitOfWork(), h.renderer, catalog).Register(mux)
	request := func(categoryID int64) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/tickets/new?category_id="+strconv.FormatInt(categoryID, 10), nil)
		req.Header.Set("Cookie", sessionCookie+"="+h.adminSession.ID)
		rec := httptest.NewRecorder()
		h.mw.Wrap(mux).ServeHTTP(rec, req)
		return rec
	}

	rec := request(unpublished.ID)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unpublished direct selection = %d, want 404: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "Draft only") {
		t.Fatalf("unpublished category leaked in direct-selection response: %s", rec.Body.String())
	}

	rec = request(h.bugCategory.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("published direct selection = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), h.bugCategory.Name) {
		t.Fatalf("published category missing from direct-selection response: %s", rec.Body.String())
	}
}

func TestAmendment4_StructureDeskSelectionAndMutationPreserveContext(t *testing.T) {
	h := newHarness(t)
	catalog := application.NewCatalogService(h.store.CatalogStore(), h.store.CategoryStore(), h.clock)
	catalogMux := http.NewServeMux()
	NewCategoryHandlersWithWorkflows(h.categories, h.workflows, h.renderer, catalog).Register(catalogMux)
	catalogRequest := func(method, target string, form url.Values, hx bool) *httptest.ResponseRecorder {
		var body *strings.Reader
		if form != nil {
			body = strings.NewReader(form.Encode())
		} else {
			body = strings.NewReader("")
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
		h.mw.Wrap(catalogMux).ServeHTTP(rec, req)
		return rec
	}
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

	structurePath := "/categories?view=structure&department_id=" + strconv.FormatInt(department.ID, 10) + "&desk_id=" + strconv.FormatInt(desk.ID, 10)
	selected := catalogRequest(http.MethodGet, structurePath, nil, true)
	if selected.Code != http.StatusOK {
		t.Fatalf("selected structure = %d, want 200: %s", selected.Code, selected.Body.String())
	}
	body := selected.Body.String()
	for _, want := range []string{"Support", "Requests", "Not configured", "department_id=" + strconv.FormatInt(department.ID, 10), "desk_id=" + strconv.FormatInt(desk.ID, 10)} {
		if !strings.Contains(body, want) {
			t.Errorf("selected structure missing %q", want)
		}
	}

	form := url.Values{"view": {"structure"}, "department_id": {strconv.FormatInt(department.ID, 10)}, "desk_id": {strconv.FormatInt(desk.ID, 10)}, "name": {"Customer Support"}, "description": {"Renamed support desk"}}
	mutated := catalogRequest(http.MethodPost, "/categories/desks/"+strconv.FormatInt(desk.ID, 10)+"/edit", form, true)
	if mutated.Code != http.StatusOK {
		t.Fatalf("HTMX desk update = %d, want 200: %s", mutated.Code, mutated.Body.String())
	}
	if got := mutated.Header().Get("HX-Retarget"); got != "#categories-background" {
		t.Errorf("HX-Retarget = %q, want categories background", got)
	}
	if got := mutated.Header().Get("HX-Redirect"); got != "" {
		t.Errorf("HTMX mutation unexpectedly redirected to %q", got)
	}
	if !strings.Contains(mutated.Body.String(), "Customer Support") || !strings.Contains(mutated.Body.String(), "Requests") {
		t.Error("HTMX mutation did not render the renamed desk and selected category")
	}
	storedDesk, err := h.desks.GetByID(t.Context(), *h.admin, desk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if storedDesk.Description != "Renamed support desk" {
		t.Fatalf("desk description = %q, want persisted drawer description", storedDesk.Description)
	}

	fallback := catalogRequest(http.MethodPost, "/categories/desks/"+strconv.FormatInt(desk.ID, 10)+"/edit", form, false)
	wantLocation := "/categories?department_id=" + strconv.FormatInt(department.ID, 10) + "&desk_id=" + strconv.FormatInt(desk.ID, 10) + "&view=structure"
	wantRedirect(t, fallback, http.StatusSeeOther, wantLocation)
	_ = category
}

func TestAmendment4_LegacyDeskIsVisibleUnderVirtualUnassignedAndCanBeRepaired(t *testing.T) {
	h := newHarness(t)
	legacy, err := h.desks.CreateWithDescription(t.Context(), *h.admin, "Legacy desk", "Legacy description")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.desks.AddMember(t.Context(), *h.admin, legacy.ID, h.admin.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.categories.CreateWithDescriptionFor(t.Context(), *h.admin, "Legacy requests", "Legacy category", legacy.ID); err != nil {
		t.Fatal(err)
	}
	catalog := application.NewCatalogService(h.store.CatalogStore(), h.store.CategoryStore(), h.clock)
	mux := http.NewServeMux()
	NewCategoryHandlersWithWorkflows(h.categories, h.workflows, h.renderer, catalog).Register(mux)
	request := func(method, target string, form url.Values) *httptest.ResponseRecorder {
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
		rec := httptest.NewRecorder()
		h.mw.Wrap(mux).ServeHTTP(rec, req)
		return rec
	}

	path := "/categories?view=structure&department_id=unassigned&desk_id=" + strconv.FormatInt(legacy.ID, 10)
	selected := request(http.MethodGet, path, nil)
	if selected.Code != http.StatusOK {
		t.Fatalf("legacy structure = %d, want 200: %s", selected.Code, selected.Body.String())
	}
	for _, want := range []string{"Unassigned", "Legacy desk", "Legacy requests", "unassigned&desk_id=" + strconv.FormatInt(legacy.ID, 10)} {
		if !strings.Contains(selected.Body.String(), want) {
			t.Errorf("legacy structure missing %q", want)
		}
	}
	editPath := "/categories/desks/" + strconv.FormatInt(legacy.ID, 10) + "/edit?view=structure&department_id=unassigned&desk_id=" + strconv.FormatInt(legacy.ID, 10)
	edit := request(http.MethodGet, editPath, nil)
	if edit.Code != http.StatusOK || !strings.Contains(edit.Body.String(), "Legacy description") {
		t.Fatalf("legacy desk drawer = %d, want description: %s", edit.Code, edit.Body.String())
	}
	if !strings.Contains(edit.Body.String(), `name="department_id"`) || !strings.Contains(edit.Body.String(), `required`) {
		t.Fatal("legacy desk drawer must require a real Department")
	}
	departments, err := catalog.ListDepartments(t.Context())
	if err != nil || len(departments) == 0 {
		t.Fatalf("list Departments = %+v, %v", departments, err)
	}
	form := url.Values{
		"view": {"structure"}, "department_id": {strconv.FormatInt(departments[0].ID, 10)},
		"desk_id": {strconv.FormatInt(legacy.ID, 10)}, "name": {"Repaired desk"},
		"description": {"Repaired description"},
	}
	updated := request(http.MethodPost, "/categories/desks/"+strconv.FormatInt(legacy.ID, 10)+"/edit", form)
	if updated.Code != http.StatusSeeOther || !strings.Contains(updated.Header().Get("Location"), "department_id="+strconv.FormatInt(departments[0].ID, 10)) {
		t.Fatalf("repair legacy desk = %d/%q", updated.Code, updated.Header().Get("Location"))
	}
	stored, err := h.desks.GetByID(t.Context(), *h.admin, legacy.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.DepartmentID == nil || *stored.DepartmentID != departments[0].ID || stored.Description != "Repaired description" {
		t.Fatalf("repaired desk = %+v", stored)
	}
	members, err := h.desks.ListMembers(t.Context(), *h.admin, legacy.ID)
	if err != nil || len(members) != 1 || members[0].ID != h.admin.ID {
		t.Fatalf("repaired desk members = %+v, %v", members, err)
	}
}

func TestAmendment4_DirectDeleteControlsRemainNativeAndServerAuthoritative(t *testing.T) {
	h := newHarness(t)
	catalog := application.NewCatalogService(h.store.CatalogStore(), h.store.CategoryStore(), h.clock)
	department, err := catalog.CreateDepartmentFor(t.Context(), *h.admin, "Operations", "Operations")
	if err != nil {
		t.Fatalf("create department: %v", err)
	}
	desk, err := catalog.CreateDeskFor(t.Context(), *h.admin, department.ID, "Direct delete desk")
	if err != nil {
		t.Fatalf("create desk: %v", err)
	}
	category, err := h.categories.CreateWithDescriptionFor(t.Context(), *h.admin, "Direct delete category", "", desk.ID)
	if err != nil {
		t.Fatalf("create category: %v", err)
	}

	catalogMux := http.NewServeMux()
	NewCategoryHandlersWithWorkflows(h.categories, h.workflows, h.renderer, catalog).Register(catalogMux)
	getCatalog := func(target string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		req.Header.Set("Cookie", sessionCookie+"="+h.adminSession.ID)
		rec := httptest.NewRecorder()
		h.mw.Wrap(catalogMux).ServeHTTP(rec, req)
		return rec
	}
	for _, tc := range []struct {
		name, path, label, action string
	}{
		{"category", "/categories?view=structure&department_id=" + strconv.FormatInt(department.ID, 10) + "&desk_id=" + strconv.FormatInt(desk.ID, 10), "Delete category", "/categories/" + strconv.FormatInt(category.ID, 10) + "/delete"},
		{"desk", "/categories?view=structure&department_id=" + strconv.FormatInt(department.ID, 10) + "&desk_id=" + strconv.FormatInt(desk.ID, 10), "Delete desk", "/categories/desks/" + strconv.FormatInt(desk.ID, 10) + "/delete"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := getCatalog(tc.path)
			if rec.Code != http.StatusOK {
				t.Fatalf("GET %s = %d, want 200", tc.path, rec.Code)
			}
			body := rec.Body.String()
			if tc.name == "category" {
				for _, want := range []string{"category-menu-button", `action="` + tc.action + `"`, `class="category-menu-danger"`, `role="menuitem"`} {
					if !strings.Contains(body, want) {
						t.Errorf("category overflow delete control missing %q", want)
					}
				}
				return
			}
			for _, want := range []string{`action="` + tc.action + `"`, `type="submit">` + tc.label + `</button>`} {
				if !strings.Contains(body, want) {
					t.Errorf("direct delete control missing %q", want)
				}
			}
		})
	}

	referenced := h.seedTicket(t, "category remains server-authoritative", nil)
	categoryDelete := h.postForm(t, "/categories/"+strconv.FormatInt(referenced.CategoryID, 10)+"/delete", url.Values{}, false)
	if categoryDelete.Code != http.StatusConflict || !strings.Contains(categoryDelete.Body.String(), "referenced and cannot be deleted") {
		t.Errorf("rejected category delete = %d/%q, want inline conflict", categoryDelete.Code, categoryDelete.Body.String())
	}

	style := extractStyleBlock(t, getCatalog("/categories?view=structure&department_id="+strconv.FormatInt(department.ID, 10)+"&desk_id="+strconv.FormatInt(desk.ID, 10)).Body.String())
	mobile := extractMediaBlock(t, style, "max-width:640px")
	if !strings.Contains(mobile, ".category-table,.category-table tbody,.category-table tr,.category-table td{display:block") {
		t.Error("mobile category rows must stack rather than overflow")
	}
	if !cssRuleDeclares(mobile, ".desks-layout{", "grid-template-columns:1fr") {
		t.Error("mobile desk list and detail must stack")
	}
	if !strings.Contains(style, ":focus-visible{outline:3px solid var(--accent)") {
		t.Error("direct delete controls must retain the shared visible focus treatment")
	}
}

func TestAmendment4_CurrentTaskCardPreservesManualCompletionMarkup(t *testing.T) {
	h := newHarness(t)
	ticket, _ := pendingManualFixture(t, h, "Check the cable run")

	rec := h.get(t, "/tickets/"+strconv.FormatInt(ticket.ID, 10), false)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET ticket = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`class="timeline-entry workflow-pending workflow-pending-action"`,
		`<h3 id="current-task-title">Current task</h3>`,
		`background:color-mix(in srgb,var(--amber-soft) 18%,var(--card))`,
		"Check the cable run",
		`<label class="visually-hidden" for="solution">Solution (optional)</label>`,
		`placeholder="Solution (optional)"`,
		`action="/tickets/` + strconv.FormatInt(ticket.ID, 10) + `/workflow/steps/1/complete"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("current task card missing %q", want)
		}
	}
	if strings.Contains(body, "<h2>Pending Actions</h2>") {
		t.Error("current task item must not render a Pending Actions heading")
	}
	style := extractStyleBlock(t, body)
	if !cssRuleDeclares(style, ".workflow-pending-action{", "background:color-mix(in srgb,var(--amber-soft) 18%,var(--card))") {
		t.Error("current task item must use the shared amber-soft visual token")
	}
	if !cssRuleDeclares(style, ".workflow-pending-action{", "border-left:3px solid var(--amber)") {
		t.Error("current task item must use the amber left accent")
	}
}

func TestAmendment4_CurrentTaskFormRetainsRequiredNativeControls(t *testing.T) {
	h := newHarness(t)
	category, err := h.categories.Create(t.Context(), "Current task form")
	if err != nil {
		t.Fatalf("create category: %v", err)
	}
	h.publishWorkflow(t, category.ID, domain.WorkflowDefinition{{
		Type: domain.StepForm,
		Form: &domain.FormStep{
			Actor: domain.FormActorRequester,
			Fields: []domain.FormField{
				{Key: "host", Label: "Host", Kind: domain.FieldShortText, Required: true},
				{Key: "region", Label: "Region", Kind: domain.FieldSingleSelect, Options: []string{"EU", "US"}, Required: true},
				{Key: "confirmed", Label: "Confirmed", Kind: domain.FieldCheckbox, Required: true},
			},
		},
	}})
	ticket := h.seedTicket(t, "current task native controls", func(in *application.CreateTicketInput) { in.CategoryID = category.ID })
	body := h.get(t, "/tickets/"+strconv.FormatInt(ticket.ID, 10), false).Body.String()

	for _, tc := range []struct {
		name, want string
		index      int
	}{
		{"short text", `type="text"`, 0},
		{"select", `<select`, 1},
		{"checkbox", `type="checkbox"`, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tag := controlTag(t, body, "answer_"+strconv.Itoa(tc.index))
			if !strings.Contains(tag, tc.want) || !strings.Contains(tag, "required") {
				t.Errorf("%s must remain a required native control, got %s", tc.name, tag)
			}
		})
	}
}

func TestAmendment4_FullPageHasNoTrailingWhitespace(t *testing.T) {
	userTickets := fixtureListData()
	userTickets.CurrentUser.Role = domain.RoleUser
	userTickets.ShowAdvancedFilters = false

	for _, tc := range []struct {
		name     string
		page     string
		target   string
		data     any
		fragment bool
	}{
		{name: "auth_login", page: "login", data: loginData{}},
		{name: "auth_setup", page: "setup", data: setupData{}},
		{name: "categories_index", page: "categories_index", data: fixtureCategoriesIndexData()},
		{name: "categories_new", page: "categories_new", data: fixtureCategoryFormData()},
		{name: "category_form", page: "categories_new", target: "category_form", data: fixtureCategoryFormData(), fragment: true},
		{name: "settings_index", page: "settings_index", data: fixtureSettingsIndexData()},
		{name: "ticket_form", page: "tickets_new", target: "ticket_form", data: fixtureTicketFormData(), fragment: true},
		{name: "ticket_list", page: "tickets_index", target: "ticket_list", data: fixtureListData(), fragment: true},
		{name: "tickets_index", page: "tickets_index", data: fixtureListData()},
		{name: "tickets_index_user", page: "tickets_index", data: userTickets},
		{name: "tickets_new", page: "tickets_new", data: fixtureTicketFormData()},
		{name: "tickets_show", page: "tickets_show", data: fixtureDetailData()},
		{name: "user_form", page: "users_new", target: "user_form", data: fixtureUserFormData(), fragment: true},
		{name: "users_index", page: "users_index", data: fixtureUsersIndexData()},
		{name: "users_new", page: "users_new", data: fixtureUserFormData()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var violations []string
			for lineNumber, line := range strings.Split(renderGolden(t, tc.page, tc.target, tc.data, tc.fragment), "\n") {
				if (line != "" && strings.TrimSpace(line) == "") || strings.HasSuffix(line, " ") || strings.HasSuffix(line, "\t") {
					violations = append(violations, strconv.Itoa(lineNumber+1)+":"+strconv.Quote(line))
				}
			}
			if len(violations) != 0 {
				t.Fatalf("rendered output has %d trailing-whitespace lines: %s", len(violations), strings.Join(violations, ", "))
			}
		})
	}
}

func TestAmendment4_BuilderRendersMasterDetailWithoutPreviewUI(t *testing.T) {
	h := newHarness(t)
	category, err := h.categories.Create(t.Context(), "Linear workflow")
	if err != nil {
		t.Fatalf("create category: %v", err)
	}
	path := "/categories/" + strconv.FormatInt(category.ID, 10) + "/workflow"
	draft := domain.WorkflowDefinition{
		{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "Inspect intake"}},
		{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "Resolve request"}},
	}
	rec := h.postForm(t, path, builderFieldForm("preview", defToSteps(draft)...), false)
	if rec.Code != http.StatusOK {
		t.Fatalf("backend preview action = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{`class="workflow-step-rail"`, `class="workflow-step-card"`, `class="workflow-editor-panel"`, "Inspect intake", "Resolve request", "Drag to reorder."} {
		if !strings.Contains(body, want) {
			t.Errorf("builder layout missing %q", want)
		}
	}
}

func TestAmendment4_CategoryDrawerErrorsVaryAndPreserveSubmittedValues(t *testing.T) {
	h := newHarness(t)
	catalog := application.NewCatalogService(h.store.CatalogStore(), h.store.CategoryStore(), h.clock)
	department, err := catalog.CreateDepartmentFor(t.Context(), *h.admin, "Operations", "Operations")
	if err != nil {
		t.Fatal(err)
	}
	desk, err := catalog.CreateDeskFor(t.Context(), *h.admin, department.ID, "Support")
	if err != nil {
		t.Fatal(err)
	}
	category, err := h.categories.CreateWithDescriptionFor(t.Context(), *h.admin, "Requests", "Requests", desk.ID)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	NewCategoryHandlersWithWorkflows(h.categories, h.workflows, h.renderer, catalog).Register(mux)
	request := func(method, target string, form url.Values, hx bool) *httptest.ResponseRecorder {
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

	getPaths := []string{
		"/categories/new?view=categories",
		"/categories/" + strconv.FormatInt(category.ID, 10) + "/edit?view=categories",
		"/categories/departments/new?view=structure",
		"/categories/departments/" + strconv.FormatInt(department.ID, 10) + "/edit?view=structure&department_id=" + strconv.FormatInt(department.ID, 10),
		"/categories/desks/new?view=structure&department_id=" + strconv.FormatInt(department.ID, 10),
		"/categories/desks/" + strconv.FormatInt(desk.ID, 10) + "/edit?view=structure&department_id=" + strconv.FormatInt(department.ID, 10) + "&desk_id=" + strconv.FormatInt(desk.ID, 10),
	}
	for _, path := range getPaths {
		t.Run("Vary "+path, func(t *testing.T) {
			rec := request(http.MethodGet, path, nil, true)
			if rec.Code != http.StatusOK || rec.Header().Get("Vary") != "HX-Request" {
				t.Fatalf("GET %s = %d/%q, want 200 and Vary HX-Request", path, rec.Code, rec.Header().Get("Vary"))
			}
		})
	}

	invalid := url.Values{"name": {"Submitted category"}, "description": {"Submitted description"}, "desk_id": {""}, "view": {"categories"}}
	rec := request(http.MethodPost, "/categories", invalid, true)
	if rec.Code != http.StatusUnprocessableEntity || rec.Header().Get("HX-Retarget") != "#category-drawer-host" || rec.Header().Get("HX-Reswap") != "outerHTML" {
		t.Fatalf("invalid category = %d/%q: %s", rec.Code, rec.Header().Get("HX-Retarget"), rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `value="Submitted category"`) || !strings.Contains(rec.Body.String(), "Submitted description") {
		t.Fatalf("drawer did not preserve submitted values: %s", rec.Body.String())
	}
}

func TestAmendment4_CatalogDrawerTypedErrorsMarkOnlyTheMatchingControl(t *testing.T) {
	h := newHarness(t)
	catalog := application.NewCatalogService(h.store.CatalogStore(), h.store.CategoryStore(), h.clock)
	operations, err := catalog.CreateDepartmentFor(t.Context(), *h.admin, "Operations", "Operations department")
	if err != nil {
		t.Fatal(err)
	}
	finance, err := catalog.CreateDepartmentFor(t.Context(), *h.admin, "Finance", "Finance department")
	if err != nil {
		t.Fatal(err)
	}
	support, err := catalog.CreateDeskFor(t.Context(), *h.admin, operations.ID, "Support", "Support desk")
	if err != nil {
		t.Fatal(err)
	}
	billing, err := catalog.CreateDeskFor(t.Context(), *h.admin, operations.ID, "Billing", "Billing desk")
	if err != nil {
		t.Fatal(err)
	}
	requests, err := h.categories.CreateWithDescriptionFor(t.Context(), *h.admin, "Requests", "Request category", support.ID)
	if err != nil {
		t.Fatal(err)
	}
	incidents, err := h.categories.CreateWithDescriptionFor(t.Context(), *h.admin, "Incidents", "Incident category", support.ID)
	if err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	NewCategoryHandlersWithWorkflows(h.categories, h.workflows, h.renderer, catalog).Register(mux)
	request := func(method, target string, form url.Values, hx bool) *httptest.ResponseRecorder {
		body := strings.NewReader("")
		if form != nil {
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

	for _, tc := range []struct {
		name, target, freshPath, invalid string
		form                             url.Values
		status                           int
		controls                         []string
	}{
		{
			name:      "department create validation",
			target:    "/categories/departments",
			freshPath: "/categories/departments/new?view=structure",
			invalid:   "name",
			form:      url.Values{"name": {""}, "description": {"Submitted department description"}, "view": {"structure"}},
			status:    http.StatusUnprocessableEntity,
			controls:  []string{"name", "description"},
		},
		{
			name:      "department edit duplicate",
			target:    "/categories/departments/" + strconv.FormatInt(finance.ID, 10) + "/edit",
			freshPath: "/categories/departments/" + strconv.FormatInt(finance.ID, 10) + "/edit?view=structure",
			invalid:   "name",
			form:      url.Values{"name": {operations.Name}, "description": {"Submitted department description"}, "view": {"structure"}},
			status:    http.StatusConflict,
			controls:  []string{"name", "description"},
		},
		{
			name:      "desk create validation",
			target:    "/categories/desks",
			freshPath: "/categories/desks/new?view=structure",
			invalid:   "department_id",
			form:      url.Values{"department_id": {""}, "name": {"Submitted desk"}, "description": {"Submitted desk description"}, "view": {"structure"}},
			status:    http.StatusUnprocessableEntity,
			controls:  []string{"department_id", "name", "description"},
		},
		{
			name:      "desk edit duplicate",
			target:    "/categories/desks/" + strconv.FormatInt(support.ID, 10) + "/edit",
			freshPath: "/categories/desks/" + strconv.FormatInt(support.ID, 10) + "/edit?view=structure&department_id=" + strconv.FormatInt(operations.ID, 10) + "&desk_id=" + strconv.FormatInt(support.ID, 10),
			invalid:   "name",
			form:      url.Values{"department_id": {strconv.FormatInt(operations.ID, 10)}, "desk_id": {strconv.FormatInt(support.ID, 10)}, "name": {billing.Name}, "description": {"Submitted desk description"}, "view": {"structure"}},
			status:    http.StatusConflict,
			controls:  []string{"department_id", "name", "description"},
		},
		{
			name:      "category create validation",
			target:    "/categories",
			freshPath: "/categories/new?view=structure&department_id=" + strconv.FormatInt(operations.ID, 10),
			invalid:   "desk_id",
			form:      url.Values{"department_id": {strconv.FormatInt(operations.ID, 10)}, "desk_id": {""}, "name": {"Submitted category"}, "description": {"Submitted category description"}, "view": {"structure"}},
			status:    http.StatusUnprocessableEntity,
			controls:  []string{"department_id", "desk_id", "name", "description"},
		},
		{
			name:      "category edit duplicate",
			target:    "/categories/" + strconv.FormatInt(incidents.ID, 10) + "/edit",
			freshPath: "/categories/" + strconv.FormatInt(incidents.ID, 10) + "/edit?view=structure&department_id=" + strconv.FormatInt(operations.ID, 10) + "&desk_id=" + strconv.FormatInt(support.ID, 10),
			invalid:   "name",
			form:      url.Values{"department_id": {strconv.FormatInt(operations.ID, 10)}, "desk_id": {strconv.FormatInt(support.ID, 10)}, "name": {requests.Name}, "description": {"Submitted category description"}, "view": {"structure"}},
			status:    http.StatusConflict,
			controls:  []string{"department_id", "desk_id", "name", "description"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, hx := range []bool{true, false} {
				t.Run(map[bool]string{true: "HTMX", false: "ordinary"}[hx], func(t *testing.T) {
					rec := request(http.MethodPost, tc.target, tc.form, hx)
					if rec.Code != tc.status {
						t.Fatalf("POST %s = %d, want %d: %s", tc.target, rec.Code, tc.status, rec.Body.String())
					}
					if got := rec.Header().Get("Vary"); got != "HX-Request" {
						t.Errorf("Vary = %q, want HX-Request", got)
					}
					if hx {
						for key, want := range map[string]string{"HX-Retarget": "#category-drawer-host", "HX-Reswap": "outerHTML"} {
							if got := rec.Header().Get(key); got != want {
								t.Errorf("%s = %q, want %q", key, got, want)
							}
						}
					} else if rec.Header().Get("HX-Retarget") != "" || rec.Header().Get("HX-Reswap") != "" {
						t.Errorf("ordinary response returned HTMX swap headers: %v", rec.Header())
					}

					body := rec.Body.String()
					drawerStart := strings.Index(body, `id="category-drawer-host"`)
					if drawerStart < 0 {
						t.Fatalf("response must render the category drawer: %s", body)
					}
					drawerBody := body[drawerStart:]
					for _, control := range tc.controls {
						tag := controlTag(t, drawerBody, control)
						if control == tc.invalid {
							if !strings.Contains(tag, `aria-invalid="true"`) {
								t.Errorf("%s control must be invalid, got %s", control, tag)
							}
						} else if strings.Contains(tag, `aria-invalid="true"`) {
							t.Errorf("%s control must not be invalid, got %s", control, tag)
						}
					}
					if !strings.Contains(body, "Submitted ") {
						t.Errorf("drawer must preserve submitted values: %s", body)
					}

					fresh := request(http.MethodGet, tc.freshPath, nil, hx)
					if fresh.Code != http.StatusOK {
						t.Fatalf("fresh drawer = %d, want 200: %s", fresh.Code, fresh.Body.String())
					}
					freshBody := fresh.Body.String()
					freshDrawerStart := strings.Index(freshBody, `id="category-drawer-host"`)
					if freshDrawerStart < 0 {
						t.Fatalf("fresh response must render the category drawer: %s", freshBody)
					}
					for _, control := range tc.controls {
						if strings.Contains(controlTag(t, freshBody[freshDrawerStart:], control), `aria-invalid="true"`) {
							t.Errorf("fresh %s control retained an invalid marker", control)
						}
					}
				})
			}
		})
	}
}

func TestIssue129_CatalogDrawerGuidanceMatchesEachEntity(t *testing.T) {
	h := newHarness(t)
	catalog := application.NewCatalogService(h.store.CatalogStore(), h.store.CategoryStore(), h.clock)
	department, err := catalog.CreateDepartmentFor(t.Context(), *h.admin, "Operations", "Operations")
	if err != nil {
		t.Fatal(err)
	}
	desk, err := catalog.CreateDeskFor(t.Context(), *h.admin, department.ID, "Support")
	if err != nil {
		t.Fatal(err)
	}
	category, err := h.categories.CreateWithDescriptionFor(t.Context(), *h.admin, "Requests", "", desk.ID)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	NewCategoryHandlersWithWorkflows(h.categories, h.workflows, h.renderer, catalog).Register(mux)
	request := func(path string, hx bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Cookie", sessionCookie+"="+h.adminSession.ID)
		if hx {
			req.Header.Set("HX-Request", "true")
		}
		rec := httptest.NewRecorder()
		h.mw.Wrap(mux).ServeHTTP(rec, req)
		return rec
	}

	for _, tc := range []struct {
		name, path, kind, description, nameHelp string
		hx                                      []bool
	}{
		{"category create", "/categories/new?view=structure&department_id=" + strconv.FormatInt(department.ID, 10) + "&desk_id=" + strconv.FormatInt(desk.ID, 10), "category", "Use categories to group requests that follow the same workflow.", "Category names must be globally unique.", []bool{false, true}},
		{"category edit", "/categories/" + strconv.FormatInt(category.ID, 10) + "/edit?view=structure&department_id=" + strconv.FormatInt(department.ID, 10) + "&desk_id=" + strconv.FormatInt(desk.ID, 10), "category", "Use categories to group requests that follow the same workflow.", "Category names must be globally unique.", []bool{false, true}},
		{"department create", "/categories/departments/new?view=structure", "department", "Use departments to group desks that support the same part of the organization.", "Department names must be globally unique.", []bool{false}},
		{"department edit", "/categories/departments/" + strconv.FormatInt(department.ID, 10) + "/edit?view=structure&department_id=" + strconv.FormatInt(department.ID, 10), "department", "Use departments to group desks that support the same part of the organization.", "Department names must be globally unique.", []bool{true}},
		{"desk create", "/categories/desks/new?view=structure&department_id=" + strconv.FormatInt(department.ID, 10), "desk", "Use desks to group categories for the team that handles them.", "Desk names must be globally unique.", []bool{true}},
		{"desk edit", "/categories/desks/" + strconv.FormatInt(desk.ID, 10) + "/edit?view=structure&department_id=" + strconv.FormatInt(department.ID, 10) + "&desk_id=" + strconv.FormatInt(desk.ID, 10), "desk", "Use desks to group categories for the team that handles them.", "Desk names must be globally unique.", []bool{false}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, hx := range tc.hx {
				t.Run(map[bool]string{false: "direct", true: "HTMX"}[hx], func(t *testing.T) {
					rec := request(tc.path, hx)
					if rec.Code != http.StatusOK {
						t.Fatalf("GET %s = %d, want 200: %s", tc.path, rec.Code, rec.Body.String())
					}
					body := rec.Body.String()
					descriptionID := tc.kind + "-drawer-description"
					if !strings.Contains(body, `aria-describedby="`+descriptionID+`"`) || !strings.Contains(body, `id="`+descriptionID+`"`) || !strings.Contains(body, tc.description) {
						t.Fatalf("%s drawer must describe its purpose: %s", tc.kind, body)
					}
					nameHelpID := tc.kind + "-name-help"
					name := controlTag(t, body, "name")
					if !strings.Contains(name, `aria-describedby="`+nameHelpID+`"`) || !strings.Contains(body, `id="`+nameHelpID+`"`) || !strings.Contains(body, tc.nameHelp) {
						t.Fatalf("%s name must retain global uniqueness help: %s", tc.kind, body)
					}
				})
			}
		})
	}
}

func TestIssue125_CategoriesSearchesGlobalOwnNamesAndNavigatesToHierarchy(t *testing.T) {
	h := newHarness(t)
	catalog := application.NewCatalogService(h.store.CatalogStore(), h.store.CategoryStore(), h.clock)
	operations, err := catalog.CreateDepartmentFor(t.Context(), *h.admin, "Operations", "Operations department")
	if err != nil {
		t.Fatal(err)
	}
	emptyDepartment, err := catalog.CreateDepartmentFor(t.Context(), *h.admin, "Empty department", "No desks")
	if err != nil {
		t.Fatal(err)
	}
	support, err := catalog.CreateDeskFor(t.Context(), *h.admin, operations.ID, "Support", "Support desk")
	if err != nil {
		t.Fatal(err)
	}
	emptyDesk, err := catalog.CreateDeskFor(t.Context(), *h.admin, operations.ID, "Empty desk", "No categories")
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := h.desks.CreateWithDescription(t.Context(), *h.admin, "Legacy support", "No department")
	if err != nil {
		t.Fatal(err)
	}
	_, err = h.categories.CreateWithDescriptionFor(t.Context(), *h.admin, "Service requests", "Published category", support.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = h.categories.CreateWithDescriptionFor(t.Context(), *h.admin, "Draft requests", "Unpublished category", support.ID)
	if err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	NewCategoryHandlersWithWorkflows(h.categories, h.workflows, h.renderer, catalog).Register(mux)
	request := func(target, sessionID string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		req.Header.Set("Cookie", sessionCookie+"="+sessionID)
		rec := httptest.NewRecorder()
		h.mw.Wrap(mux).ServeHTTP(rec, req)
		return rec
	}
	resultSection := func(t *testing.T, body string) string {
		t.Helper()
		start := strings.Index(body, `id="category-search-results"`)
		if start < 0 {
			t.Fatalf("search results missing: %s", body)
		}
		end := strings.Index(body[start:], `</section>`)
		if end < 0 {
			t.Fatalf("search results are not a complete section: %s", body[start:])
		}
		return body[start : start+end]
	}

	search := request("/categories?view=structure&q=%20%20sUpPoRt%20%20", h.adminSession.ID)
	if search.Code != http.StatusOK {
		t.Fatalf("search = %d, want 200: %s", search.Code, search.Body.String())
	}
	body := search.Body.String()
	if !strings.Contains(body, `id="category-search"`) || !strings.Contains(body, `value="sUpPoRt"`) {
		t.Fatalf("search input must retain the trimmed query: %s", body)
	}
	results := resultSection(t, body)
	for _, want := range []string{
		`data-search-result-kind="desk"`,
		`>Support<`,
		`href="/categories?department_id=` + strconv.FormatInt(operations.ID, 10) + `&amp;desk_id=` + strconv.FormatInt(support.ID, 10) + `&amp;view=structure"`,
	} {
		if !strings.Contains(results, want) {
			t.Errorf("support search missing %q: %s", want, results)
		}
	}
	if strings.Contains(results, `data-search-result-kind="category"`) || strings.Contains(results, "Service requests") {
		t.Errorf("desk search must match its own name only: %s", results)
	}

	categorySearch := request("/categories?q=ReQuEsTs", h.adminSession.ID)
	categoryResults := resultSection(t, categorySearch.Body.String())
	for _, want := range []string{
		`data-search-result-kind="category"`,
		`>Service requests<`,
		`>Draft requests<`,
		`href="/categories?department_id=` + strconv.FormatInt(operations.ID, 10) + `&amp;desk_id=` + strconv.FormatInt(support.ID, 10) + `&amp;view=structure"`,
	} {
		if !strings.Contains(categoryResults, want) {
			t.Errorf("category search missing %q: %s", want, categoryResults)
		}
	}
	if strings.Contains(categoryResults, `data-search-result-kind="desk"`) || strings.Contains(categoryResults, "Support desk") {
		t.Errorf("category search must match its own name only: %s", categoryResults)
	}

	departmentSearch := request("/categories?q=operations", h.adminSession.ID)
	if departmentSearch.Code != http.StatusOK {
		t.Fatalf("department search = %d, want 200: %s", departmentSearch.Code, departmentSearch.Body.String())
	}
	departmentResults := resultSection(t, departmentSearch.Body.String())
	if !strings.Contains(departmentResults, `data-search-result-kind="department"`) || !strings.Contains(departmentResults, `href="/categories?department_id=`+strconv.FormatInt(operations.ID, 10)+`&amp;view=structure"`) {
		t.Errorf("department result must navigate to its structure selection: %s", departmentResults)
	}
	if strings.Contains(departmentResults, `data-search-result-kind="desk"`) || strings.Contains(departmentResults, `data-search-result-kind="category"`) {
		t.Errorf("department search must match its own name only: %s", departmentResults)
	}

	for _, tc := range []struct {
		name, query, kind, location string
	}{
		{"empty department", "empty%20department", "department", "/categories?department_id=" + strconv.FormatInt(emptyDepartment.ID, 10) + "&amp;view=structure"},
		{"empty desk", "empty%20desk", "desk", "/categories?department_id=" + strconv.FormatInt(operations.ID, 10) + "&amp;desk_id=" + strconv.FormatInt(emptyDesk.ID, 10) + "&amp;view=structure"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			section := resultSection(t, request("/categories?q="+tc.query, h.adminSession.ID).Body.String())
			if !strings.Contains(section, `data-search-result-kind="`+tc.kind+`"`) || !strings.Contains(section, `href="`+tc.location+`"`) {
				t.Errorf("empty hierarchy item missing from global search: %s", section)
			}
		})
	}

	legacySearch := request("/categories?q=legacy", h.adminSession.ID)
	legacyResults := resultSection(t, legacySearch.Body.String())
	if !strings.Contains(legacyResults, `data-search-result-kind="desk"`) || !strings.Contains(legacyResults, `href="/categories?department_id=unassigned&amp;desk_id=`+strconv.FormatInt(legacy.ID, 10)+`&amp;view=structure"`) {
		t.Errorf("legacy desk result must retain its unassigned hierarchy path: %s", legacyResults)
	}

	escaped := request("/categories?q=%20%3Cscript%3E%20", h.adminSession.ID)
	if escaped.Code != http.StatusOK || !strings.Contains(escaped.Body.String(), `value="&lt;script&gt;"`) || strings.Contains(escaped.Body.String(), `<script>`) {
		t.Errorf("query must be trimmed and escaped: %d %s", escaped.Code, escaped.Body.String())
	}
	blank := request("/categories?view=structure&q=%20%20", h.adminSession.ID)
	if blank.Code != http.StatusOK || strings.Contains(blank.Body.String(), `id="category-search-results"`) {
		t.Errorf("blank query must not render results: %d %s", blank.Code, blank.Body.String())
	}
	categoriesSelection := request("/categories?view=categories&department_id="+strconv.FormatInt(operations.ID, 10)+"&desk_id="+strconv.FormatInt(support.ID, 10)+"&q=requests", h.adminSession.ID)
	if categoriesSelection.Code != http.StatusOK {
		t.Fatalf("categories selection search = %d, want 200: %s", categoriesSelection.Code, categoriesSelection.Body.String())
	}
	wantClear := `href="/categories?department_id=` + strconv.FormatInt(operations.ID, 10) + `&amp;desk_id=` + strconv.FormatInt(support.ID, 10) + `&amp;view=categories"`
	if !strings.Contains(categoriesSelection.Body.String(), wantClear) {
		t.Errorf("clear must retain the normalized categories selection, missing %q: %s", wantClear, categoriesSelection.Body.String())
	}
	for _, tc := range []struct {
		name, target, wantClear string
	}{
		{
			name:      "nonexistent department",
			target:    "/categories?view=categories&department_id=999999&desk_id=" + strconv.FormatInt(support.ID, 10) + "&q=requests",
			wantClear: `href="/categories?view=categories"`,
		},
		{
			name:      "desk outside selected department",
			target:    "/categories?view=categories&department_id=" + strconv.FormatInt(emptyDepartment.ID, 10) + "&desk_id=" + strconv.FormatInt(support.ID, 10) + "&q=requests",
			wantClear: `href="/categories?department_id=` + strconv.FormatInt(emptyDepartment.ID, 10) + `&amp;view=categories"`,
		},
		{
			name:      "unassigned sentinel with assigned desk",
			target:    "/categories?view=categories&department_id=unassigned&desk_id=" + strconv.FormatInt(support.ID, 10) + "&q=requests",
			wantClear: `href="/categories?department_id=unassigned&amp;view=categories"`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := request(tc.target, h.adminSession.ID)
			if rec.Code != http.StatusOK {
				t.Fatalf("search = %d, want 200: %s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tc.wantClear) {
				t.Errorf("clear retained unnormalized state, missing %q: %s", tc.wantClear, rec.Body.String())
			}
		})
	}
	for _, target := range []string{
		"/categories?view=categories&department_id=not-an-id&q=requests",
		"/categories?view=categories&department_id=-1&q=requests",
	} {
		rec := request(target, h.adminSession.ID)
		if rec.Code != http.StatusUnprocessableEntity || strings.Contains(rec.Body.String(), "Clear search") {
			t.Errorf("invalid state must not render a clear link: %d %s", rec.Code, rec.Body.String())
		}
	}

	agent := seedUserRole(t, h.store, "Agent", "agent-search@tkt.test", domain.RoleAgent)
	user := seedUserRole(t, h.store, "User", "user-search@tkt.test", domain.RoleUser)
	for _, session := range []*domain.Session{seedSession(t, h.store, agent.ID), seedSession(t, h.store, user.ID)} {
		if rec := request("/categories?q=support", session.ID); rec.Code != http.StatusForbidden {
			t.Errorf("non-manager search = %d, want 403: %s", rec.Code, rec.Body.String())
		}
	}
}

func TestAmendment4_CategoryMutationRefreshFailureDoesNotInviteRetry(t *testing.T) {
	h := newHarness(t)
	catalog := application.NewCatalogService(h.store.CatalogStore(), h.store.CategoryStore(), h.clock)
	mux := http.NewServeMux()
	NewCategoryHandlersWithWorkflows(h.categories, h.workflows, h.renderer, catalog).Register(mux)
	if _, err := h.rawDB(t).Exec(`UPDATE departments SET created_at='not-a-timestamp' WHERE name='General'`); err != nil {
		t.Fatal(err)
	}
	form := url.Values{"name": {"Committed department"}, "description": {"Saved despite refresh failure"}, "view": {"structure"}}
	req := httptest.NewRequest(http.MethodPost, "/categories/departments", strings.NewReader(form.Encode()))
	req.Header.Set("Cookie", sessionCookie+"="+h.adminSession.ID)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	rec := httptest.NewRecorder()
	h.mw.Wrap(mux).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("committed mutation with refresh failure = %d, want successful non-retryable response: %s", rec.Code, rec.Body.String())
	}
	for key, want := range map[string]string{"HX-Retarget": "#categories-background", "HX-Reswap": "outerHTML", "HX-Trigger-After-Swap": "categories:saved"} {
		if got := rec.Header().Get(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
	if !strings.Contains(rec.Body.String(), "Changes saved") || !strings.Contains(rec.Body.String(), "Reload the page") {
		t.Fatalf("refresh failure must explain committed state and recovery: %s", rec.Body.String())
	}
	var count int
	if err := h.rawDB(t).QueryRow(`SELECT COUNT(*) FROM departments WHERE name='Committed department'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("committed department rows = %d, want exactly one", count)
	}
}
