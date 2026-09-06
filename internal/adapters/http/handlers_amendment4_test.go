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
		`class="card current-task-card"`,
		`<h2 id="current-task-title">Current task</h2>`,
		`background:color-mix(in srgb,var(--amber-soft) 30%,var(--card))`,
		"Check the cable run",
		`<label for="solution">Solution (optional)</label>`,
		`action="/tickets/` + strconv.FormatInt(ticket.ID, 10) + `/workflow/steps/1/complete"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("current task card missing %q", want)
		}
	}
	if strings.Contains(body, "<h2>Pending Actions</h2>") {
		t.Error("current task card must replace the Pending Actions heading")
	}
	style := extractStyleBlock(t, body)
	if !cssRuleDeclares(style, ".current-task-card{", "background:color-mix(in srgb,var(--amber-soft) 30%,var(--card))") {
		t.Error("current task card must use the exact 30% current-task/card mix")
	}
	if !cssRuleDeclares(style, ".current-task-card{", "border-top:2px solid var(--amber)") {
		t.Error("current task card must use the exact 2px current-task accent top border")
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
