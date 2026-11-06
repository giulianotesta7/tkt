package httpadapter

import (
	"net/http"
	"net/http/httptest"
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

	catalog := application.NewCatalogService(h.store.CatalogStore(), h.store.CategoryStore(), h.clock).WithFolders(h.store.FolderStore())
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

func TestAmendment4_CurrentStepChecklistPreservesManualCompletionMarkup(t *testing.T) {
	h := newHarness(t)
	ticket, _ := pendingManualFixture(t, h, "Check the cable run")

	rec := h.get(t, "/tickets/"+strconv.FormatInt(ticket.ID, 10), false)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET ticket = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`class="timeline-entry workflow-pending workflow-pending-action"`,
		`<h3 id="workflow-steps-title">Steps</h3>`,
		`<span class="workflow-checklist-count">0 of 1 done</span>`,
		`role="progressbar" aria-valuemin="0" aria-valuemax="1" aria-valuenow="0"`,
		`class="workflow-checklist-step is-current"`,
		`<span class="workflow-checklist-name">Check the cable run</span>`,
		`<label class="visually-hidden" for="solution">Solution (optional)</label>`,
		`placeholder="Solution (optional)"`,
		`action="/tickets/` + strconv.FormatInt(ticket.ID, 10) + `/workflow/steps/1/complete"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("current step checklist missing %q", want)
		}
	}
	if strings.Contains(body, "<h2>Pending Actions</h2>") {
		t.Error("the step checklist must not render a Pending Actions heading")
	}
	if strings.Contains(body, "Current task") {
		t.Error("the single Current task card must be replaced by the checklist")
	}
	style := extractStyleBlock(t, body)
	if !cssRuleDeclares(style, ".workflow-checklist-progress{", "height:3px") {
		t.Error("the progress bar must stay the thin 3px track")
	}
	if !cssRuleDeclares(style, ".workflow-checklist-progress{", "background:var(--hover)") {
		t.Error("the progress track must use the neutral hover token, not an accent state fill")
	}
	if !cssRuleDeclares(style, ".workflow-checklist-progress > span{", "background:var(--ink-soft)") {
		t.Error("the progress fill must use the neutral ink-soft token")
	}
}

func TestAmendment4_CurrentStepFormRetainsRequiredNativeControls(t *testing.T) {
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
		{name: "category_sla", page: "category_sla", data: fixtureCategorySLAData()},
		{name: "settings_organization", page: "settings_organization", data: fixtureSettingsOrganizationData()},
		{name: "settings_signin", page: "settings_signin", data: fixtureSettingsSigninData()},
		{name: "settings_language", page: "settings_language", data: fixtureSettingsLanguageData()},
		{name: "settings_tickets", page: "settings_tickets", data: fixtureSettingsTicketsData()},
		{name: "settings_calendar", page: "settings_calendar", data: fixtureSettingsCalendarData()},
		{name: "settings_email", page: "settings_email", data: fixtureSettingsEmailData()},
		{name: "settings_security", page: "settings_security", data: fixtureSettingsSecurityData()},
		{name: "notifications", page: "notifications", data: fixtureNotificationsPanelData()},
		{name: "notification_panel", page: "notifications", target: "notification_panel", data: fixtureNotificationsPanelData(), fragment: true},
		{name: "ticket_form", page: "tickets_new", target: "ticket_form", data: fixtureTicketFormData(), fragment: true},
		{name: "ticket_list", page: "tickets_index", target: "ticket_list", data: fixtureListData(), fragment: true},
		{name: "tickets_index", page: "tickets_index", data: fixtureListData()},
		{name: "tickets_index_user", page: "tickets_index", data: userTickets},
		{name: "tickets_new", page: "tickets_new", data: fixtureTicketFormData()},
		{name: "tickets_show", page: "tickets_show", data: fixtureDetailData()},
		{name: "ticket_detail_follows", page: "tickets_show", target: "ticket_detail", data: fixtureDetailFollowData(), fragment: true},
		{name: "comment_form_mentions_empty", page: "tickets_show", target: "comment_form", data: fixtureDetailMentionsEmptyData(), fragment: true},
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

func TestAmendment4_BuilderRendersMasterDetail(t *testing.T) {
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
	rec := h.postForm(t, path, builderFieldForm("select_step", defToSteps(draft)...), false)
	if rec.Code != http.StatusOK {
		t.Fatalf("backend select_step action = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{`class="workflow-step-rail"`, `class="workflow-step-card"`, `class="workflow-editor-panel"`, "Inspect intake", "Resolve request", "Drag to reorder."} {
		if !strings.Contains(body, want) {
			t.Errorf("builder layout missing %q", want)
		}
	}
}
