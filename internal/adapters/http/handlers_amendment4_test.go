package httpadapter

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

func TestAmendment4_DirectDeleteControlsRemainNativeAndServerAuthoritative(t *testing.T) {
	h := newHarness(t)
	category, err := h.categories.Create(t.Context(), "Direct delete category")
	if err != nil {
		t.Fatalf("create category: %v", err)
	}
	desk, err := h.desks.Create(t.Context(), *h.admin, "Direct delete desk")
	if err != nil {
		t.Fatalf("create desk: %v", err)
	}

	for _, tc := range []struct {
		name, path, label, action string
	}{
		{"category", "/categories", "Delete category", "/categories/" + strconv.FormatInt(category.ID, 10) + "/delete"},
		{"desk", "/desks?desk_id=" + strconv.FormatInt(desk.ID, 10), "Delete desk", "/desks/" + strconv.FormatInt(desk.ID, 10) + "/delete"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := h.get(t, tc.path, false)
			if rec.Code != http.StatusOK {
				t.Fatalf("GET %s = %d, want 200", tc.path, rec.Code)
			}
			body := rec.Body.String()
			for _, want := range []string{`<form method="post" action="` + tc.action + `">`, `type="submit">` + tc.label + `</button>`} {
				if !strings.Contains(body, want) {
					t.Errorf("direct delete control missing %q", want)
				}
			}
			if strings.Contains(body, "More actions") || strings.Contains(body, "category-overflow") || strings.Contains(body, "desk-overflow") {
				t.Errorf("delete control must not be hidden by a disclosure: %.500s", body)
			}
		})
	}

	referenced := h.seedTicket(t, "category remains server-authoritative", nil)
	categoryDelete := h.postForm(t, "/categories/"+strconv.FormatInt(referenced.CategoryID, 10)+"/delete", url.Values{}, false)
	if categoryDelete.Code != http.StatusConflict || !strings.Contains(categoryDelete.Body.String(), "referenced and cannot be deleted") {
		t.Errorf("rejected category delete = %d/%q, want inline conflict", categoryDelete.Code, categoryDelete.Body.String())
	}

	style := extractStyleBlock(t, h.get(t, "/categories", false).Body.String())
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

func TestAmendment4_BuilderRendersLinearPreviewBesideOrderedEditor(t *testing.T) {
	h := newHarness(t)
	category, err := h.categories.Create(t.Context(), "Linear preview")
	if err != nil {
		t.Fatalf("create category: %v", err)
	}
	path := "/categories/" + strconv.FormatInt(category.ID, 10) + "/workflow"
	draft := domain.WorkflowDefinition{
		{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "Inspect intake"}},
		{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "Resolve request"}},
	}
	form := builderFieldForm("preview", defToSteps(draft)...)
	rec := h.postForm(t, path, form, false)
	if rec.Code != http.StatusOK {
		t.Fatalf("preview = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`class="workflow-builder-panels"`,
		`class="workflow-editor-panel"`,
		`class="workflow-flow-preview"`,
		`aria-label="Workflow preview"`,
		"Inspect intake",
		"Resolve request",
		`<ol class="workflow-steps">`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("builder layout missing %q", want)
		}
	}
	if strings.Index(body, "Inspect intake") >= strings.Index(body, "Resolve request") {
		t.Error("read-only preview must retain submitted linear draft order")
	}
	if strings.Contains(body, "canvas") || strings.Contains(body, "drag") {
		t.Error("preview must not expose graph or drag/drop controls")
	}

	style := extractStyleBlock(t, body)
	if !cssRuleDeclares(style, ".workflow-builder-panels{", "grid-template-columns:minmax(0,1fr) minmax(220px,.72fr)") {
		t.Error("desktop builder must use two panels")
	}
	mobile := extractMediaBlock(t, style, "max-width:640px")
	if !cssRuleDeclares(mobile, ".workflow-builder-panels{", "grid-template-columns:1fr") {
		t.Error("mobile builder panels must stack")
	}
}
