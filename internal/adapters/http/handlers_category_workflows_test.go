package httpadapter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

func TestCategoryWorkflowBuilder_UsesUsersHeaderFoundationAndCategoryIdentity(t *testing.T) {
	h := newHarness(t)
	category, err := h.categories.Create(t.Context(), "Hardware")
	if err != nil {
		t.Fatalf("create category: %v", err)
	}

	rec := h.get(t, "/categories/"+strconv.FormatInt(category.ID, 10)+"/workflow", false)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`class="page-foundation category-workflow-page"`,
		`<nav class="page-breadcrumb" aria-label="Breadcrumb"><a href="/categories">Categories</a><span aria-hidden="true"> / </span><span>Hardware</span></nav>`,
		`class="page-title">Category workflow</h1>`,
		`class="page-subtitle">Build one ordered path for this category.</p>`,
		`<span class="page-status" role="status">Saved</span>`,
		`class="page-action page-action-primary" type="submit" form="workflow-form" name="action" value="publish">Publish</button>`,
		`<form method="post" action="/categories/`,
		`id="workflow-form"`,
		`/static/users.css`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("workflow header must contain %q, got: %s", want, body)
		}
	}

	if strings.Contains(body, `value="preview"`) || strings.Contains(body, `aria-label="Workflow preview"`) {
		t.Error("workflow screen must not expose preview UI")
	}
	usersBody := h.get(t, "/users", false).Body.String()
	for _, want := range []string{
		`class="users-root"`,
		`class="users-header"`,
		`id="users-list-title"`,
		`class="users-primary-action`,
		`id="users-new-launcher"`,
		`class="users-list"`,
	} {
		if !strings.Contains(usersBody, want) {
			t.Errorf("Users page must contain %q, got: %s", want, usersBody)
		}
	}

	cssRec := h.get(t, "/static/users.css", false)
	if cssRec.Code != http.StatusOK {
		t.Fatalf("users.css status = %d, want 200", cssRec.Code)
	}
	css := cssRec.Body.String()
	for _, tc := range []struct {
		name         string
		selectors    []string
		declarations []string
	}{
		{"header geometry", []string{".users-root .users-header", ".page-foundation .page-header"}, []string{"display:flex", "align-items:flex-start", "justify-content:space-between", "gap:24px"}},
		{"title geometry", []string{".users-root h1", ".page-foundation .page-title"}, []string{"font-size:30px", "line-height:1.1"}},
		{"subtitle styling", []string{".users-root .users-header p", ".page-foundation .page-subtitle"}, []string{"margin:8px 0 0", "color:#667085"}},
		{"primary action geometry", []string{".users-root .users-primary-action", ".page-foundation .page-action-primary"}, []string{"display:inline-flex", "align-items:center", "justify-content:center", "padding:11px 16px"}},
		{"panel surface", []string{".users-root .users-list", ".page-foundation .page-panel"}, []string{"border:1px solid #e2e7ee", "border-radius:10px", "background:#fff"}},
	} {
		assertCSSRuleContains(t, css, tc.name, tc.selectors, tc.declarations)
	}
	statusRule := cssRuleForSelectors(css, ".page-foundation .page-status")
	if statusRule == "" {
		t.Fatal("users.css must define the Saved status text class")
	}
	for _, forbidden := range []string{"border", "background", "padding"} {
		if strings.Contains(statusRule, forbidden) {
			t.Errorf("Saved status rule must not contain button styling %q: %s", forbidden, statusRule)
		}
	}
}

func assertCSSRuleContains(t *testing.T, css, name string, selectors, declarations []string) {
	t.Helper()
	rule := cssRuleForSelectors(css, selectors...)
	if rule == "" {
		t.Errorf("%s must bind selectors %q in one shared rule", name, selectors)
		return
	}
	for _, declaration := range declarations {
		if !strings.Contains(rule, declaration) {
			t.Errorf("%s rule must contain %q, got: %s", name, declaration, rule)
		}
	}
}

func cssRuleForSelectors(css string, selectors ...string) string {
	for _, rawRule := range strings.Split(css, "}") {
		open := strings.LastIndex(rawRule, "{")
		if open < 0 {
			continue
		}
		selectorText := rawRule[:open]
		matches := true
		for _, selector := range selectors {
			if !strings.Contains(selectorText, selector) {
				matches = false
				break
			}
		}
		if matches {
			return rawRule[open+1:]
		}
	}
	return ""
}

// These integration contracts intentionally exercise the public builder routes
// through the real SQLite-backed HTTP harness. The form's draft value is the
// complete ordered client draft; no server-side builder state is trusted.
func TestCategoryWorkflowBuilder_MasterDetailSelectionPresentation(t *testing.T) {
	h := newHarness(t)
	category, err := h.categories.Create(t.Context(), "Master detail")
	if err != nil {
		t.Fatalf("create category: %v", err)
	}
	path := "/categories/" + strconv.FormatInt(category.ID, 10) + "/workflow"
	steps := []bstep{{typ: "manual_task", manual: "First instructions"}, {typ: "manual_task", manual: "Second instructions"}}
	wantRedirect(t, h.postForm(t, path, builderFieldForm("save", steps...), false), http.StatusSeeOther, path)
	selection := builderFieldForm("select_step", bstep{typ: "manual_task", manual: "Unsaved first"}, bstep{typ: "manual_task", manual: "Unsaved second"})
	selection.Set("selection_step_index", "1")
	noJS := h.postForm(t, path+"?action=select_step", selection, false)
	if noJS.Code != http.StatusOK || !strings.Contains(noJS.Body.String(), `name="step_1_instructions"`) || !strings.Contains(noJS.Body.String(), "Unsaved second") {
		t.Fatalf("no-JS selection must render the requested unsaved editor, got %d: %s", noJS.Code, noJS.Body.String())
	}
	persisted := h.persistedDefinition(t, path)
	if len(persisted) != 2 || persisted[0].ManualTask.Instructions != "First instructions" || persisted[1].ManualTask.Instructions != "Second instructions" {
		t.Fatalf("selection must not persist the submitted draft: %+v", persisted)
	}
	body := h.get(t, path+"?selected_step_index=1", true).Body.String()
	fullBody := h.get(t, path+"?selected_step_index=1", false).Body.String()
	if !strings.Contains(body, `name="selected_step_index" value="1"`) || !strings.Contains(body, `name="step_0_snapshot"`) || !strings.Contains(body, `type="submit" name="selection_step_index" value="1"`) || !strings.Contains(body, `hx-post="`+path+`"`) || !strings.Contains(body, `hx-swap="outerHTML show:none"`) || !strings.Contains(body, `hx-push-url="false"`) || strings.Contains(fullBody, `href="`+path+`?selected_step_index=1"`) || strings.Contains(fullBody, `hx-get="`+path+`?selected_step_index=1"`) {
		t.Errorf("selection must use a POST submitter with a distinct target index, got: %s", body)
	}
}

func TestCategoryWorkflowBuilder_SafeGetAuthorizationAndIndex(t *testing.T) {
	h := newHarness(t)
	category, err := h.categories.Create(t.Context(), "Unconfigured")
	if err != nil {
		t.Fatalf("create category: %v", err)
	}
	agent := h.createUser(t, "Agent", "workflow-agent@tkt.test", "secret")
	path := "/categories/" + strconv.FormatInt(category.ID, 10) + "/workflow"

	deniedReq := httptest.NewRequest(http.MethodGet, path, nil).WithContext(context.WithValue(context.Background(), ctxKeyUser{}, agent))
	denied := httptest.NewRecorder()
	h.mux.ServeHTTP(denied, deniedReq)
	if denied.Code != http.StatusForbidden && denied.Code != http.StatusSeeOther {
		t.Fatalf("agent GET status = %d, want 403 or existing redirect denial", denied.Code)
	}

	rec := h.get(t, path, false)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin GET status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `id="workflow-builder"`) {
		t.Errorf("safe GET must render the builder, got: %s", rec.Body.String())
	}
	db := h.rawDB(t)
	if n := scanOneInt(t, db, "SELECT COUNT(*) FROM category_workflows WHERE category_id=?", category.ID); n != 0 {
		t.Errorf("safe GET workflow rows = %d, want 0", n)
	}

	index := h.get(t, "/categories", false)
	if index.Code != http.StatusOK {
		t.Fatalf("category index status = %d, want 200", index.Code)
	}
	body := index.Body.String()
	if !strings.Contains(body, "Configure workflow") {
		t.Errorf("category index must offer workflow configuration, got: %s", body)
	}
	if strings.Contains(body, "Draft") {
		t.Error("GET-only category must not acquire a Draft badge")
	}
}

func TestCategoryWorkflowBuilder_ClosedMutationsPersistCanonicalCompleteDraft(t *testing.T) {
	draft := builderDraft(t, "first", "second")
	// add_step is intentionally excluded: it now appends an editable default step
	// (defect correction), so it is covered by the dedicated add_step contracts.
	actions := []string{"save", "change_type", "add_field", "remove_field", "move_up", "move_down", "remove_step"}

	for _, action := range actions {
		t.Run(action, func(t *testing.T) {
			h := newHarness(t)
			category, err := h.categories.Create(t.Context(), "Mutation "+action)
			if err != nil {
				t.Fatalf("create category: %v", err)
			}
			path := "/categories/" + strconv.FormatInt(category.ID, 10) + "/workflow"
			rec := h.postForm(t, path, builderForm(action, draft), false)
			wantRedirect(t, rec, http.StatusSeeOther, path)

			db := h.rawDB(t)
			var persisted string
			if err := db.QueryRow("SELECT draft_json FROM category_workflows WHERE category_id=?", category.ID).Scan(&persisted); err != nil {
				t.Fatalf("first mutation must create one draft row: %v", err)
			}
			if persisted != draft {
				t.Errorf("persisted canonical draft = %s, want %s", persisted, draft)
			}
		})
	}
}

func TestCategoryWorkflowBuilder_PreviewPublishAndHTMXParity(t *testing.T) {
	t.Run("preview is read-only", func(t *testing.T) {
		h := newHarness(t)
		category, err := h.categories.Create(t.Context(), "Preview")
		if err != nil {
			t.Fatalf("create category: %v", err)
		}
		path := "/categories/" + strconv.FormatInt(category.ID, 10) + "/workflow"
		rec := h.postForm(t, path, builderForm("preview", builderDraft(t, "first", "second")), false)
		if rec.Code != http.StatusOK {
			t.Fatalf("preview status = %d, want 200", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "first") || !strings.Contains(rec.Body.String(), "second") {
			t.Errorf("preview must render the ordered submitted draft, got: %s", rec.Body.String())
		}
		if n := scanOneInt(t, h.rawDB(t), "SELECT COUNT(*) FROM category_workflows WHERE category_id=?", category.ID); n != 0 {
			t.Errorf("preview workflow rows = %d, want 0", n)
		}
	})

	t.Run("invalid publish is a shared 422 with no writes", func(t *testing.T) {
		h := newHarness(t)
		category, err := h.categories.Create(t.Context(), "Invalid")
		if err != nil {
			t.Fatalf("create category: %v", err)
		}
		path := "/categories/" + strconv.FormatInt(category.ID, 10) + "/workflow"
		invalid := builderDraft(t, "")
		full := h.postForm(t, path, builderForm("publish", invalid), false)
		hx := h.postForm(t, path, builderForm("publish", invalid), true)
		for _, tc := range []struct {
			name string
			rec  *httptest.ResponseRecorder
		}{{"full", full}, {"htmx", hx}} {
			if tc.rec.Code != http.StatusUnprocessableEntity {
				t.Errorf("%s invalid publish status = %d, want 422", tc.name, tc.rec.Code)
			}
			if !strings.Contains(tc.rec.Body.String(), `role="alert"`) || !strings.Contains(tc.rec.Body.String(), "Step 1") {
				t.Errorf("%s invalid publish must render the same inline step error, got: %s", tc.name, tc.rec.Body.String())
			}
		}
		db := h.rawDB(t)
		if n := scanOneInt(t, db, "SELECT COUNT(*) FROM category_workflows WHERE category_id=?", category.ID); n != 0 {
			t.Errorf("invalid publish workflow rows = %d, want 0", n)
		}
		if n := scanOneInt(t, db, "SELECT COUNT(*) FROM workflow_versions WHERE category_id=?", category.ID); n != 0 {
			t.Errorf("invalid publish version rows = %d, want 0", n)
		}
	})

	t.Run("valid publish is atomic and htmx swaps the builder", func(t *testing.T) {
		h := newHarness(t)
		category, err := h.categories.Create(t.Context(), "Published")
		if err != nil {
			t.Fatalf("create category: %v", err)
		}
		path := "/categories/" + strconv.FormatInt(category.ID, 10) + "/workflow"
		draft := builderDraft(t, "first", "second")
		full := h.postForm(t, path, builderForm("publish", draft), false)
		wantRedirect(t, full, http.StatusSeeOther, path)
		db := h.rawDB(t)
		if n := scanOneInt(t, db, "SELECT COUNT(*) FROM workflow_versions WHERE category_id=?", category.ID); n != 1 {
			t.Fatalf("published version rows = %d, want 1", n)
		}
		if current, ok := scanOneNullableInt(t, db, "SELECT current_version_id FROM category_workflows WHERE category_id=?", category.ID); !ok || current == 0 {
			t.Errorf("publish must atomically switch the current version, got (%d, %v)", current, ok)
		}

		hx := h.postForm(t, path, builderForm("move_up", draft), true)
		if hx.Code != http.StatusOK {
			t.Fatalf("HTMX mutation status = %d, want 200", hx.Code)
		}
		for _, want := range []string{`id="workflow-builder"`, "<ol", "<button", `name="action" value="move_up"`, `class="workflow-step-card selected"`, "aria-live"} {
			if !strings.Contains(hx.Body.String(), want) {
				t.Errorf("HTMX builder response must contain %q, got: %s", want, hx.Body.String())
			}
		}

		index := h.get(t, "/categories", false)
		if !strings.Contains(index.Body.String(), `>Published</span>`) {
			t.Errorf("category index must derive exactly Published when draft equals the published definition, got: %s", index.Body.String())
		}
		if strings.Contains(index.Body.String(), "Published v") {
			t.Errorf("equal published draft must not show a version number, got: %s", index.Body.String())
		}

		edited := builderDraft(t, "edited")
		wantRedirect(t, h.postForm(t, path, builderForm("save", edited), false), http.StatusSeeOther, path)
		if body := h.get(t, "/categories", false).Body.String(); !strings.Contains(body, "Draft") {
			t.Errorf("category index must derive Draft when draft differs from published version, got: %s", body)
		}
	})
}

// ==== PR8 builder-defect correction: browser-realistic editable controls ====
//
// These contracts drive the builder the way a browser does: real per-step
// form controls (step_<i>_type, step_<i>_instructions, step_<i>_desk, ...)
// carry the complete ordered draft, and each closed action applies on the
// server to that submitted draft using explicit numeric step/field indexes.

// Historical RED — the original no-op builder left an empty draft empty. An
// empty GET form POSTed with action=add_step MUST add, persist, and re-render
// one editable default step. This assertion prevents that defect from
// returning.
func TestCategoryWorkflowBuilder_RED_AddStepFromEmptyAddsEditableDefault(t *testing.T) {
	h := newHarness(t)
	category, err := h.categories.Create(t.Context(), "Empty")
	if err != nil {
		t.Fatalf("create category: %v", err)
	}
	path := "/categories/" + strconv.FormatInt(category.ID, 10) + "/workflow"

	// Safe GET must not create a row (unchanged contract).
	h.get(t, path, false)
	if n := scanOneInt(t, h.rawDB(t), "SELECT COUNT(*) FROM category_workflows WHERE category_id=?", category.ID); n != 0 {
		t.Fatalf("precondition: safe GET must not create a draft row, got %d", n)
	}

	// Browser-realistic Add step carrying the current (empty) draft.
	wantRedirect(t, h.postForm(t, path, builderForm("add_step", "[]"), false), http.StatusSeeOther, path)

	db := h.rawDB(t)
	persisted := scanOneString(t, db, "SELECT draft_json FROM category_workflows WHERE category_id=?", category.ID)
	def, err := domain.ParseWorkflowDefinition([]byte(persisted))
	if err != nil {
		t.Fatalf("persisted draft must parse: %v", err)
	}
	if len(def) != 1 {
		t.Fatalf("add_step must add exactly one default step, persisted %d steps", len(def))
	}
	if def[0].Type != domain.StepManualTask {
		t.Errorf("default step type = %s, want manual_task", def[0].Type)
	}

	// Re-render must expose editable default-step controls, never a hidden JSON trick.
	body := h.get(t, path, false).Body.String()
	for _, want := range []string{`name="step_0_type"`, `name="step_0_instructions"`} {
		if !strings.Contains(body, want) {
			t.Errorf("re-render must expose editable default-step control %s, got: %s", want, body)
		}
	}
	if strings.Contains(body, `name="draft"`) {
		t.Errorf("builder must not use a hidden JSON draft round-trip, got: %s", body)
	}
}

// bfield describes one editable form field submitted through the builder.
type bfield struct {
	key, label, kind, options string
	required                  bool
}

// bstep describes one editable step submitted through the builder.
type bstep struct {
	typ      string
	manual   string
	desk     string
	strategy string
	actor    string
	fields   []bfield
}

// builderFieldForm encodes a complete ordered draft as the real per-step form
// controls a browser submits, plus the closed action.
func builderFieldForm(action string, steps ...bstep) url.Values {
	f := url.Values{"action": {action}}
	for i, s := range steps {
		pi := strconv.Itoa(i)
		f.Set("step_"+pi+"_type", s.typ)
		switch s.typ {
		case "manual_task":
			f.Set("step_"+pi+"_instructions", s.manual)
		case "assign_to_desk":
			f.Set("step_"+pi+"_desk", s.desk)
			f.Set("step_"+pi+"_strategy", s.strategy)
		case "form":
			f.Set("step_"+pi+"_actor", s.actor)
			for j, fd := range s.fields {
				fj := strconv.Itoa(j)
				f.Set("step_"+pi+"_field_"+fj+"_key", fd.key)
				f.Set("step_"+pi+"_field_"+fj+"_label", fd.label)
				f.Set("step_"+pi+"_field_"+fj+"_kind", fd.kind)
				f.Set("step_"+pi+"_field_"+fj+"_options", fd.options)
				if fd.required {
					f.Set("step_"+pi+"_field_"+fj+"_required", "on")
				}
			}
		}
	}
	return f
}

// persistedDefinition reads the canonical draft_json back for a category route
// and parses it into a domain definition.
func (h *harness) persistedDefinition(t *testing.T, path string) domain.WorkflowDefinition {
	t.Helper()
	re := regexp.MustCompile(`/categories/(\d+)/workflow`)
	m := re.FindStringSubmatch(path)
	if m == nil {
		t.Fatalf("parse category id from %q", path)
	}
	id, _ := strconv.ParseInt(m[1], 10, 64)
	raw := scanOneString(t, h.rawDB(t), "SELECT draft_json FROM category_workflows WHERE category_id=?", id)
	def, err := domain.ParseWorkflowDefinition([]byte(raw))
	if err != nil {
		t.Fatalf("parse persisted draft: %v", err)
	}
	return def
}

func buildingSteps() []bstep {
	return []bstep{
		{typ: "manual_task", manual: "a"},
		{typ: "form", actor: "requester", fields: []bfield{{key: "k", label: "Key", kind: "short_text"}}},
		{typ: "close_ticket"},
	}
}

// EditControls proves the visible per-step controls submit complete ordered
// values that round-trip into the persisted canonical draft and re-render.
func TestCategoryWorkflowBuilder_EditControlsSubmitCompleteOrderedValues(t *testing.T) {
	h := newHarness(t)
	category, err := h.categories.Create(t.Context(), "Editable")
	if err != nil {
		t.Fatalf("create category: %v", err)
	}
	path := "/categories/" + strconv.FormatInt(category.ID, 10) + "/workflow"

	steps := []bstep{
		{typ: "manual_task", manual: "first"},
		{typ: "assign_to_desk", desk: "7", strategy: "least_loaded"},
		{typ: "form", actor: "assignee", fields: []bfield{{key: "server", label: "Server", kind: "single_select", options: "eu; us"}}},
	}
	wantRedirect(t, h.postForm(t, path, builderFieldForm("save", steps...), false), http.StatusSeeOther, path)

	def := h.persistedDefinition(t, path)
	if len(def) != 3 {
		t.Fatalf("saved step count = %d, want 3", len(def))
	}
	if def[0].Type != domain.StepManualTask || def[0].ManualTask == nil || def[0].ManualTask.Instructions != "first" {
		t.Errorf("step 0 not manual task 'first': %+v", def[0])
	}
	if def[1].Type != domain.StepAssignToDesk || def[1].AssignToDesk == nil || def[1].AssignToDesk.DeskID != 7 || def[1].AssignToDesk.Strategy != domain.StrategyLeastLoaded {
		t.Errorf("step 1 not assign_to_desk desk 7 least_loaded: %+v", def[1])
	}
	if def[2].Type != domain.StepForm || def[2].Form == nil || def[2].Form.Actor != domain.FormActorAssignee || len(def[2].Form.Fields) != 1 {
		t.Errorf("step 2 not form actor=assignee 1 field: %+v", def[2])
	}
	f := def[2].Form.Fields[0]
	if f.Key != "server" || f.Label != "Server" || f.Kind != domain.FieldSingleSelect || len(f.Options) != 2 || f.Options[0] != "eu" || f.Options[1] != "us" {
		t.Errorf("form field round-trip mismatch: %+v", f)
	}

	// Re-render exposes the editable controls for the selected step only.
	for _, tc := range []struct {
		index int
		want  string
	}{{0, `name="step_0_type"`}, {1, `name="step_1_desk"`}, {1, `name="step_1_strategy"`}, {2, `name="step_2_actor"`}, {2, `name="step_2_field_0_key"`}} {
		body := h.get(t, path+"?selected_step_index="+strconv.Itoa(tc.index), false).Body.String()
		if !strings.Contains(body, tc.want) {
			t.Errorf("re-render missing editable control %s, got: %s", tc.want, body)
		}
	}
}

// RED — field keys become server-owned identity. The builder must stop exposing
// an editable Key control (users edit labels; keys are opaque stable identifiers
// used by validation and runtime responses). Server-side code assigns a
// deterministic opaque field_N sequence key unique across ALL form steps: new
// fields get one automatically, editing the Label never rewrites an existing
// key, the rendered builder carries the stable key only in a hidden input, and
// old/incomplete drafts with empty keys are filled on save.
func TestCategoryWorkflowBuilder_RED_FieldKeysAreServerOwned(t *testing.T) {
	t.Run("add_field assigns the smallest unused field_N across all form steps", func(t *testing.T) {
		h := newHarness(t)
		category, err := h.categories.Create(t.Context(), "AutoKey")
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		path := "/categories/" + strconv.FormatInt(category.ID, 10) + "/workflow"
		steps := []bstep{
			{typ: "form", actor: "requester", fields: []bfield{{key: "a", label: "A", kind: "short_text"}}},
			{typ: "form", actor: "requester", fields: []bfield{{key: "field_1", label: "B", kind: "short_text"}}},
		}
		wantRedirect(t, h.postForm(t, path, builderFieldForm("save", steps...), false), http.StatusSeeOther, path)

		f := builderFieldForm("add_field", steps...)
		f.Set("step_index", "0")
		wantRedirect(t, h.postForm(t, path, f, false), http.StatusSeeOther, path)

		def := h.persistedDefinition(t, path)
		step0, step1 := def[0].Form, def[1].Form
		if step0 == nil || step1 == nil || len(step0.Fields) != 2 {
			t.Fatalf("after add_field step 0 must hold 2 fields: %+v", def)
		}
		if got := step0.Fields[1].Key; got != "field_2" {
			t.Errorf("new field key = %q, want field_2 (smallest unused across both form steps; field_1 is taken by step 1)", got)
		}
		assertUniqueFieldKeys(t, def)

		// Deterministic reuse: removing the auto field and adding again selects
		// the same smallest unused key (field_2 is free again).
		fr := builderFieldForm("remove_field", defToSteps(def)...)
		fr.Set("step_index", "0")
		fr.Set("field_index", "1")
		wantRedirect(t, h.postForm(t, path, fr, false), http.StatusSeeOther, path)

		fa := builderFieldForm("add_field", defToSteps(h.persistedDefinition(t, path))...)
		fa.Set("step_index", "0")
		wantRedirect(t, h.postForm(t, path, fa, false), http.StatusSeeOther, path)
		after := h.persistedDefinition(t, path)
		if got := after[0].Form.Fields[1].Key; got != "field_2" {
			t.Errorf("re-added field key = %q, want field_2 (deterministic reuse of the smallest unused key)", got)
		}
		assertUniqueFieldKeys(t, after)
	})

	t.Run("editing Label preserves the existing stable key", func(t *testing.T) {
		h := newHarness(t)
		category, err := h.categories.Create(t.Context(), "LabelStable")
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		path := "/categories/" + strconv.FormatInt(category.ID, 10) + "/workflow"
		wantRedirect(t, h.postForm(t, path, builderFieldForm("save", bstep{typ: "form", actor: "requester", fields: []bfield{{key: "server", label: "Server", kind: "short_text"}}}), false), http.StatusSeeOther, path)
		edited := []bstep{{typ: "form", actor: "requester", fields: []bfield{{key: "server", label: "Production server", kind: "short_text"}}}}
		wantRedirect(t, h.postForm(t, path, builderFieldForm("save", edited...), false), http.StatusSeeOther, path)
		fields := h.persistedDefinition(t, path)[0].Form.Fields
		if len(fields) != 1 || fields[0].Key != "server" || fields[0].Label != "Production server" {
			t.Errorf("label edit must keep key=server and update label, got %+v", fields)
		}
	})

	t.Run("rendered builder hides Key but round-trips the hidden stable key", func(t *testing.T) {
		h := newHarness(t)
		category, err := h.categories.Create(t.Context(), "HiddenKey")
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		path := "/categories/" + strconv.FormatInt(category.ID, 10) + "/workflow"
		wantRedirect(t, h.postForm(t, path, builderFieldForm("save", bstep{typ: "form", actor: "requester", fields: []bfield{{key: "server", label: "Server", kind: "short_text"}}}), false), http.StatusSeeOther, path)
		body := h.get(t, path, false).Body.String()
		if !strings.Contains(body, `type="hidden" name="step_0_field_0_key" value="server"`) {
			t.Errorf("builder must round-trip the stable key through a hidden input, got: %s", body)
		}
		if strings.Contains(body, `type="text" name="step_0_field_0_key"`) {
			t.Errorf("builder must not render an editable Key textbox, got: %s", body)
		}
		if strings.Contains(body, `label for="step_0_field_0_key"`) {
			t.Errorf("builder must not render a Key label, got: %s", body)
		}
	})

	t.Run("old incomplete drafts get deterministic keys filled on save", func(t *testing.T) {
		h := newHarness(t)
		category, err := h.categories.Create(t.Context(), "FillKeys")
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		path := "/categories/" + strconv.FormatInt(category.ID, 10) + "/workflow"
		// A legacy/incomplete draft may carry empty keys (no hidden value yet);
		// saving must fill them so the draft stays editable and publishable.
		wantRedirect(t, h.postForm(t, path, builderFieldForm("save", bstep{typ: "form", actor: "requester", fields: []bfield{
			{key: "", label: "Name", kind: "short_text"},
			{key: "", label: "Email", kind: "short_text"},
		}}), false), http.StatusSeeOther, path)
		fields := h.persistedDefinition(t, path)[0].Form.Fields
		if len(fields) != 2 || fields[0].Key != "field_1" || fields[1].Key != "field_2" {
			t.Errorf("empty keys must be filled deterministically, got %+v", fields)
		}
	})
}

// assertUniqueFieldKeys fails when any Form field across the whole definition
// has an empty key or a key duplicated by another Form field in any step.
func assertUniqueFieldKeys(t *testing.T, def domain.WorkflowDefinition) {
	t.Helper()
	seen := map[string]bool{}
	for _, s := range def {
		if s.Form == nil {
			continue
		}
		for _, f := range s.Form.Fields {
			if f.Key == "" {
				t.Errorf("Form field %q must have a non-empty key", f.Label)
				continue
			}
			if seen[f.Key] {
				t.Errorf("duplicate field key %q across form steps", f.Key)
			}
			seen[f.Key] = true
		}
	}
}

// defToSteps converts a parsed definition back into the editable per-step
// control submission shape, mirroring how the browser round-trips the builder.
func defToSteps(def domain.WorkflowDefinition) []bstep {
	var out []bstep
	for _, s := range def {
		switch s.Type {
		case domain.StepManualTask:
			out = append(out, bstep{typ: "manual_task", manual: s.ManualTask.Instructions})
		case domain.StepForm:
			bs := bstep{typ: "form", actor: string(s.Form.Actor)}
			for _, f := range s.Form.Fields {
				bs.fields = append(bs.fields, bfield{key: f.Key, label: f.Label, kind: string(f.Kind), options: strings.Join(f.Options, "; "), required: f.Required})
			}
			out = append(out, bs)
		}
	}
	return out
}

// TypeSelectOwnsChangeTriggeredSubmission is the regression for the manual UX
// defect: re-typing a step (e.g. Manual task -> Form) must fire a change-triggered
// HTMX POST carrying action=change_type and the containing form, so the new
// type-specific fields appear without a separate Apply button click. Apply is
// rendered only inside noscript as the full-page fallback.
func TestCategoryWorkflowBuilder_PreservesTypeWithoutVisibleSelector(t *testing.T) {
	h := newHarness(t)
	category, err := h.categories.Create(t.Context(), "Type hidden")
	if err != nil {
		t.Fatalf("create category: %v", err)
	}
	path := "/categories/" + strconv.FormatInt(category.ID, 10) + "/workflow"
	wantRedirect(t, h.postForm(t, path, builderFieldForm("save", buildingSteps()...), false), http.StatusSeeOther, path)

	body := h.get(t, path, false).Body.String()
	// The type is chosen at Add step and shown in the node and editor heading;
	// there must be no visible editable Type <select> or change_type submitter.
	for _, bad := range []string{`class="step-type"`, `<select class="step-type"`, `name="action" value="change_type"`, `hx-vals='{"action":"change_type"}'`} {
		if strings.Contains(body, bad) {
			t.Errorf("type must not be editable via a visible selector, found %q", bad)
		}
	}
	// The selected step still submits its type through a hidden field so the
	// backend can rebuild the full draft while hiding it as a UI control.
	if !strings.Contains(body, `<input type="hidden" name="step_0_type"`) {
		t.Errorf("selected step must carry its type in a hidden input, got: %s", body)
	}
}
func TestWorkflowBuilderValidationAndStepViews(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/categories/1/workflow", nil)
	r.Form = url.Values{"step_0_type": {"manual_task"}, "step_0_instructions": {"keep this step"}, "step_1_snapshot": {"not-json"}}
	if draft, issues := parseBuilderDraft(r); draft != nil || len(issues) != 1 || !strings.Contains(issues[0].Message, "invalid step snapshot") {
		t.Fatalf("malformed snapshot must fail closed: draft=%v issues=%v", draft, issues)
	}
	for _, tc := range []struct {
		name  string
		draft domain.WorkflowDefinition
		index int
		want  bool
	}{
		{"terminal middle", domain.WorkflowDefinition{{Type: domain.StepResolve}, {Type: domain.StepManualTask}}, 0, false},
		{"terminal last", domain.WorkflowDefinition{{Type: domain.StepManualTask}, {Type: domain.StepResolve}}, 1, true},
		{"nonterminal last", domain.WorkflowDefinition{{Type: domain.StepManualTask}, {Type: domain.StepManualTask}}, 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := workflowStepViews(tc.draft, tc.index, nil)[tc.index].Final; got != tc.want {
				t.Fatalf("Final = %t, want %t", got, tc.want)
			}
		})
	}
	views := workflowStepViews(domain.WorkflowDefinition{{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: strings.Repeat("界", 45)}}, {Type: domain.StepResolve}}, 0, nil)
	if views[0].Final || !views[1].Final || !views[1].Last || views[0].Summary != strings.Repeat("界", 41)+"..." {
		t.Fatalf("terminal badge or Unicode summary is wrong: %+v", views)
	}
	assign := workflowStepViews(domain.WorkflowDefinition{{Type: domain.StepAssignToDesk, AssignToDesk: &domain.AssignToDeskStep{DeskID: 7}}}, 0, []domain.Desk{{ID: 7, Name: "Billing"}})
	if assign[0].Summary != "Billing" {
		t.Errorf("assign summary must show the desk name, got %q", assign[0].Summary)
	}
}

// ==== Autosave UX contract ====
//
// RED — Save draft disappears. Free-text controls autosave after 600ms of
// changed input WITHOUT swapping the builder fragment (focus/caret preserved),
// discrete controls autosave immediately, structural Field Kind persists and
// rerenders the builder to reveal/remove Options, step Type stays single-
// submitted on change_type, and form-level queue synchronization makes the
// final user action win over an in-flight autosave.
func TestCategoryWorkflowBuilder_RED_AutosaveMarkupContract(t *testing.T) {
	h := newHarness(t)
	category, err := h.categories.Create(t.Context(), "Autosave")
	if err != nil {
		t.Fatalf("create category: %v", err)
	}
	path := "/categories/" + strconv.FormatInt(category.ID, 10) + "/workflow"
	steps := []bstep{
		{typ: "manual_task", manual: "a"},
		{typ: "assign_to_desk", desk: "7", strategy: "least_loaded"},
		{typ: "form", actor: "requester", fields: []bfield{{key: "server", label: "Server", kind: "single_select", options: "North; South, Buenos Aires, Argentina"}}},
	}
	wantRedirect(t, h.postForm(t, path, builderFieldForm("save", steps...), false), http.StatusSeeOther, path)
	body := h.get(t, path, false).Body.String()
	bodyAt := func(index int) string {
		return h.get(t, path+"?selected_step_index="+strconv.Itoa(index), false).Body.String()
	}
	cid := strconv.FormatInt(category.ID, 10)
	savePost := `hx-post="/categories/` + cid + `/workflow"`
	saveVals := `hx-vals='{"action":"save"}'`
	saveInclude := `hx-include="closest form"`

	t.Run("Save draft button is gone and copy says automatic", func(t *testing.T) {
		if strings.Contains(body, "Save draft") || strings.Contains(body, `value="save"`) {
			t.Errorf("visible Save draft button must be removed entirely, got: %s", body)
		}
		if !strings.Contains(body, "Select a step to configure it. Drag to reorder.") {
			t.Errorf("helper copy must explain selection and reorder, got: %s", body)
		}
	})

	t.Run("text controls autosave after 600ms without swapping the builder", func(t *testing.T) {
		for _, tc := range []struct {
			index int
			name  string
		}{{0, "step_0_instructions"}, {2, "step_2_field_0_label"}, {2, "step_2_field_0_options"}} {
			tag := controlTag(t, bodyAt(tc.index), tc.name)
			for _, want := range []string{
				`hx-trigger="input changed delay:600ms"`,
				savePost,
				saveInclude,
				saveVals,
				`hx-swap="none"`,
			} {
				if !strings.Contains(tag, want) {
					t.Errorf("text control %s must carry %q for no-swap 600ms autosave, got: %s", tc.name, want, tag)
				}
			}
		}
	})

	t.Run("single select options use a native single-line input and semicolon transport", func(t *testing.T) {
		tag := controlTag(t, bodyAt(2), "step_2_field_0_options")
		if !strings.HasPrefix(tag, "<input") || !strings.Contains(tag, `type="text"`) || strings.Contains(tag, "<textarea") {
			t.Fatalf("single select Options must be a native single-line text input, got: %s", tag)
		}
		for _, want := range []string{
			`<label for="step_2_field_0_options">Options</label>`,
			"Separate options with semicolons. Semicolons cannot be used in option names.",
			`value="North; South, Buenos Aires, Argentina"`,
		} {
			if !strings.Contains(bodyAt(2), want) {
				t.Errorf("single select builder must contain %q, got: %s", want, bodyAt(2))
			}
		}
	})

	t.Run("discrete controls autosave immediately without swapping the builder", func(t *testing.T) {
		for _, tc := range []struct {
			index int
			name  string
		}{{1, "step_1_desk"}, {1, "step_1_strategy"}, {2, "step_2_actor"}, {2, "step_2_field_0_required"}} {
			tag := controlTag(t, bodyAt(tc.index), tc.name)
			for _, want := range []string{`hx-trigger="change"`, savePost, saveInclude, saveVals, `hx-swap="none"`} {
				if !strings.Contains(tag, want) {
					t.Errorf("discrete control %s must carry %q for immediate no-swap autosave, got: %s", tc.name, want, tag)
				}
			}
			if strings.Contains(tag, "delay:600ms") {
				t.Errorf("discrete control %s must not debounce, got: %s", tc.name, tag)
			}
		}
	})

	t.Run("structural Field Kind persists immediately and rerenders the builder fragment", func(t *testing.T) {
		tag := controlTag(t, bodyAt(2), "step_2_field_0_kind")
		for _, want := range []string{
			`hx-trigger="change"`,
			savePost,
			saveInclude,
			saveVals,
			`hx-target="#workflow-builder"`,
			`hx-swap="outerHTML"`,
		} {
			if !strings.Contains(tag, want) {
				t.Errorf("field Kind select must carry %q to persist and reveal/remove Options, got: %s", want, tag)
			}
		}
	})

	t.Run("containing form queues requests so the final user action wins", func(t *testing.T) {
		form := formOpenTag(t, body)
		if !strings.Contains(form, `hx-sync="this:queue last"`) {
			t.Errorf("builder form must inherit queue-last synchronization so a stale autosave cannot overwrite a later structural mutation, got: %s", form)
		}
	})

	t.Run("selected step type is a hidden inert field, never a change submitter", func(t *testing.T) {
		hidden := strings.Contains(bodyAt(0), `<input type="hidden" name="step_0_type"`)
		if !hidden {
			t.Errorf("selected step must carry its type as a hidden field, got: %s", bodyAt(0))
		}
		if strings.Contains(bodyAt(0), `hx-vals='{"action":"change_type"}'`) || strings.Contains(bodyAt(0), `<select class="step-type"`) {
			t.Errorf("type must not be editable or double-submitted via an autosave/change control, got: %s", bodyAt(0))
		}
	})
}

// RED — Single Select transport uses a literal semicolon delimiter only. Empty
// segments are ignored, surrounding Unicode whitespace is trimmed, order and
// duplicates are preserved, and commas remain ordinary label characters.
func TestCategoryWorkflowBuilder_RED_SplitOptionsSemicolonGrammar(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{name: "normal split", input: "North; South; Buenos Aires, Argentina", want: []string{"North", "South", "Buenos Aires, Argentina"}},
		{name: "unicode whitespace", input: "\u00a0North\u2003;\u3000South\u00a0", want: []string{"North", "South"}},
		{name: "empty segments", input: ";North;; ;South;", want: []string{"North", "South"}},
		{name: "duplicate preservation", input: "North;North;South", want: []string{"North", "North", "South"}},
		{name: "empty input", input: "", want: nil},
		{name: "comma preservation", input: "Buenos Aires, Argentina;New York, NY", want: []string{"Buenos Aires, Argentina", "New York, NY"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := splitOptions(tt.input); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("splitOptions(%q) = %#v, want %#v", tt.input, got, tt.want)
			}
		})
	}
}

// controlTag extracts the full opening tag of the control carrying name.
func controlTag(t *testing.T, body, name string) string {
	t.Helper()
	marker := `name="` + name + `"`
	idx := strings.Index(body, marker)
	if idx < 0 {
		t.Fatalf("builder must render control %s, got: %s", name, body)
	}
	start := -1
	for _, open := range []string{"<input", "<select", "<textarea"} {
		if p := strings.LastIndex(body[:idx], open); p > start {
			start = p
		}
	}
	if start < 0 {
		t.Fatalf("control %s must be an input/select/textarea, got: %s", name, body[idx-200:idx])
	}
	end := strings.Index(body[idx:], ">")
	if end < 0 {
		t.Fatalf("control %s opening tag unterminated", name)
	}
	return body[start : idx+end+1]
}

// formOpenTag extracts the full opening <form ...> tag of the builder form.
func formOpenTag(t *testing.T, body string) string {
	t.Helper()
	idx := strings.Index(body, `<form method="post" action="/categories/`)
	if idx < 0 {
		t.Fatalf("builder must render a form, got: %s", body)
	}
	end := strings.Index(body[idx:], ">")
	if end < 0 {
		t.Fatalf("builder form opening tag unterminated")
	}
	return body[idx : idx+end+1]
}

// ActionsWithIndexes proves each mutable server action applies on the submitted
// draft using explicit numeric step/field indexes and persists the result.
func TestCategoryWorkflowBuilder_ActionsWithIndexes(t *testing.T) {
	// move_up swaps step 1 (form) with step 0 (manual) -> [form, manual, close].
	t.Run("move_up", func(t *testing.T) {
		h := newHarness(t)
		category, err := h.categories.Create(t.Context(), "Up")
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		path := "/categories/" + strconv.FormatInt(category.ID, 10) + "/workflow"
		wantRedirect(t, h.postForm(t, path, builderFieldForm("save", buildingSteps()...), false), http.StatusSeeOther, path)
		f := builderFieldForm("move_up", buildingSteps()...)
		f.Set("step_index", "1")
		wantRedirect(t, h.postForm(t, path, f, false), http.StatusSeeOther, path)
		def := h.persistedDefinition(t, path)
		if def[0].Type != domain.StepForm || def[1].Type != domain.StepManualTask || def[2].Type != domain.StepClose {
			t.Errorf("move_up order = [%s %s %s], want [form manual_task close_ticket]", def[0].Type, def[1].Type, def[2].Type)
		}
	})

	// move_down swaps step 0 (manual) with step 1 (form) -> [form, manual, close].
	t.Run("move_down", func(t *testing.T) {
		h := newHarness(t)
		category, err := h.categories.Create(t.Context(), "Down")
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		path := "/categories/" + strconv.FormatInt(category.ID, 10) + "/workflow"
		wantRedirect(t, h.postForm(t, path, builderFieldForm("save", buildingSteps()...), false), http.StatusSeeOther, path)
		f := builderFieldForm("move_down", buildingSteps()...)
		f.Set("step_index", "0")
		wantRedirect(t, h.postForm(t, path, f, false), http.StatusSeeOther, path)
		def := h.persistedDefinition(t, path)
		if def[0].Type != domain.StepForm || def[1].Type != domain.StepManualTask || def[2].Type != domain.StepClose {
			t.Errorf("move_down order = [%s %s %s], want [form manual_task close_ticket]", def[0].Type, def[1].Type, def[2].Type)
		}
	})

	// remove_step keeps the final terminal at index 2.
	t.Run("remove_step", func(t *testing.T) {
		h := newHarness(t)
		category, err := h.categories.Create(t.Context(), "RemoveStep")
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		path := "/categories/" + strconv.FormatInt(category.ID, 10) + "/workflow"
		wantRedirect(t, h.postForm(t, path, builderFieldForm("save", buildingSteps()...), false), http.StatusSeeOther, path)
		f := builderFieldForm("remove_step", buildingSteps()...)
		f.Set("step_index", "2")
		wantRedirect(t, h.postForm(t, path, f, false), http.StatusSeeOther, path)
		def := h.persistedDefinition(t, path)
		body := h.get(t, path, false).Body.String()
		if len(def) != 3 || def[0].Type != domain.StepManualTask || def[1].Type != domain.StepForm || def[2].Type != domain.StepClose || strings.Count(body, `name="action" value="remove_step"`) != 2 || strings.Contains(body, `name="action" value="remove_step" disabled`) {
			t.Errorf("final remove_step bypass or markup guard failed: %v", def)
		}
	})

	// change_type re-types step 0 to assign_to_desk, initializes its closed
	// payload, and clears the incompatible manual instructions.
	t.Run("change_type initializes payload and clears incompatible", func(t *testing.T) {
		h := newHarness(t)
		category, err := h.categories.Create(t.Context(), "ChangeType")
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		path := "/categories/" + strconv.FormatInt(category.ID, 10) + "/workflow"
		wantRedirect(t, h.postForm(t, path, builderFieldForm("save", buildingSteps()...), false), http.StatusSeeOther, path)
		f := builderFieldForm("change_type", buildingSteps()...)
		f.Set("step_index", "0")
		f.Set("step_0_type", "assign_to_desk")
		f.Set("step_0_desk", "7")
		f.Set("step_0_strategy", "least_loaded")
		wantRedirect(t, h.postForm(t, path, f, false), http.StatusSeeOther, path)
		def := h.persistedDefinition(t, path)
		s0 := def[0]
		if s0.Type != domain.StepAssignToDesk || s0.AssignToDesk == nil || s0.AssignToDesk.DeskID != 7 || s0.AssignToDesk.Strategy != domain.StrategyLeastLoaded {
			t.Errorf("change_type step 0 = %+v, want assign_to_desk desk 7 least_loaded", s0)
		}
		if s0.ManualTask != nil || s0.Form != nil {
			t.Errorf("change_type must clear incompatible payloads, got manual/form set: %+v", s0)
		}
	})

	// add_field appends a blank field to the form step at index 1.
	t.Run("add_field", func(t *testing.T) {
		h := newHarness(t)
		category, err := h.categories.Create(t.Context(), "AddField")
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		path := "/categories/" + strconv.FormatInt(category.ID, 10) + "/workflow"
		wantRedirect(t, h.postForm(t, path, builderFieldForm("save", buildingSteps()...), false), http.StatusSeeOther, path)
		f := builderFieldForm("add_field", buildingSteps()...)
		f.Set("step_index", "1")
		wantRedirect(t, h.postForm(t, path, f, false), http.StatusSeeOther, path)
		def := h.persistedDefinition(t, path)
		if len(def[1].Form.Fields) != 2 {
			t.Errorf("add_field field count = %d, want 2", len(def[1].Form.Fields))
		}
	})

	// remove_field drops field 0 from the form step at index 1.
	t.Run("remove_field", func(t *testing.T) {
		h := newHarness(t)
		category, err := h.categories.Create(t.Context(), "RemoveField")
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		path := "/categories/" + strconv.FormatInt(category.ID, 10) + "/workflow"
		wantRedirect(t, h.postForm(t, path, builderFieldForm("save", buildingSteps()...), false), http.StatusSeeOther, path)
		f := builderFieldForm("remove_field", buildingSteps()...)
		f.Set("step_index", "1")
		f.Set("field_index", "0")
		wantRedirect(t, h.postForm(t, path, f, false), http.StatusSeeOther, path)
		def := h.persistedDefinition(t, path)
		if len(def[1].Form.Fields) != 0 {
			t.Errorf("remove_field field count = %d, want 0", len(def[1].Form.Fields))
		}
	})

	// add_step persists an intentionally incomplete draft (one default step).
	t.Run("add_step persists one editable default step", func(t *testing.T) {
		h := newHarness(t)
		category, err := h.categories.Create(t.Context(), "AddStep")
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		path := "/categories/" + strconv.FormatInt(category.ID, 10) + "/workflow"
		wantRedirect(t, h.postForm(t, path, builderFieldForm("add_step"), false), http.StatusSeeOther, path)
		def := h.persistedDefinition(t, path)
		if len(def) != 1 || def[0].Type != domain.StepManualTask || def[0].ManualTask == nil {
			t.Errorf("add_step result = %v, want one editable manual_task default", def)
		}
	})
}

// Reorder focus + HTMX uses the same button-specific indexes in both modes.
func TestCategoryWorkflowBuilder_ReorderFocusAndHTMXIndexes(t *testing.T) {
	h := newHarness(t)
	category, err := h.categories.Create(t.Context(), "Focus")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	path := "/categories/" + strconv.FormatInt(category.ID, 10) + "/workflow"

	// Full page move_up: 303 redirect then GET shows persisted reordered draft.
	f := builderFieldForm("move_up", buildingSteps()...)
	f.Set("step_index", "1")
	full := h.postForm(t, path, f, false)
	wantRedirect(t, full, http.StatusSeeOther, path)

	// HTMX move_down uses the same query index and swaps the builder fragment.
	fd := builderFieldForm("move_down", buildingSteps()...)
	fd.Set("step_index", "0")
	hx := h.postForm(t, path, fd, true)
	if hx.Code != http.StatusOK {
		t.Fatalf("HTMX move status = %d, want 200", hx.Code)
	}
	body := hx.Body.String()
	for _, want := range []string{`id="workflow-builder"`, `name="action" value="move_down"`, `name="step_1_type"`, `aria-live`, `class="workflow-step-card selected"`} {
		if !strings.Contains(body, want) {
			t.Errorf("HTMX builder response must contain %q, got: %s", want, body)
		}
	}
	if got := strings.Count(body, `class="workflow-editor-panel"`); got != 1 {
		t.Errorf("HTMX response must render exactly one editor, got %d", got)
	}
}

// Field-based preview stays read-only; invalid/valid publish match the draft-JSON
// contract but driven through the real visible controls.
func TestCategoryWorkflowBuilder_FieldBasedPreviewAndPublish(t *testing.T) {
	t.Run("preview is read-only from fields", func(t *testing.T) {
		h := newHarness(t)
		category, err := h.categories.Create(t.Context(), "FieldPreview")
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		path := "/categories/" + strconv.FormatInt(category.ID, 10) + "/workflow"
		steps := []bstep{
			{typ: "manual_task", manual: "first"},
			{typ: "manual_task", manual: "second"},
		}
		rec := h.postForm(t, path, builderFieldForm("preview", steps...), false)
		if rec.Code != http.StatusOK {
			t.Fatalf("preview status = %d, want 200", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "first") || !strings.Contains(rec.Body.String(), "second") {
			t.Errorf("preview must render the ordered submitted draft, got: %s", rec.Body.String())
		}
		if n := scanOneInt(t, h.rawDB(t), "SELECT COUNT(*) FROM category_workflows WHERE category_id=?", category.ID); n != 0 {
			t.Errorf("preview workflow rows = %d, want 0", n)
		}
	})

	t.Run("invalid publish shows alerts and writes nothing", func(t *testing.T) {
		h := newHarness(t)
		category, err := h.categories.Create(t.Context(), "FieldInvalid")
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		path := "/categories/" + strconv.FormatInt(category.ID, 10) + "/workflow"
		invalid := []bstep{{typ: "manual_task", manual: ""}}
		rec := h.postForm(t, path, builderFieldForm("publish", invalid...), false)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("invalid publish status = %d, want 422", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), `role="alert"`) || !strings.Contains(rec.Body.String(), "Step 1") {
			t.Errorf("invalid publish must render inline step error, got: %s", rec.Body.String())
		}
		if n := scanOneInt(t, h.rawDB(t), "SELECT COUNT(*) FROM category_workflows WHERE category_id=?", category.ID); n != 0 {
			t.Errorf("invalid publish workflow rows = %d, want 0", n)
		}
		if n := scanOneInt(t, h.rawDB(t), "SELECT COUNT(*) FROM workflow_versions WHERE category_id=?", category.ID); n != 0 {
			t.Errorf("invalid publish version rows = %d, want 0", n)
		}
	})

	t.Run("valid publish is atomic from fields", func(t *testing.T) {
		h := newHarness(t)
		category, err := h.categories.Create(t.Context(), "FieldPublish")
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		path := "/categories/" + strconv.FormatInt(category.ID, 10) + "/workflow"
		desk, err := h.desks.Create(t.Context(), *h.admin, "Infrastructure")
		if err != nil {
			t.Fatalf("create desk: %v", err)
		}
		valid := []bstep{
			{typ: "manual_task", manual: "do it"},
			{typ: "assign_to_desk", desk: strconv.FormatInt(desk.ID, 10), strategy: "claim"},
		}
		wantRedirect(t, h.postForm(t, path, builderFieldForm("publish", valid...), false), http.StatusSeeOther, path)
		db := h.rawDB(t)
		if n := scanOneInt(t, db, "SELECT COUNT(*) FROM workflow_versions WHERE category_id=?", category.ID); n != 1 {
			t.Fatalf("published version rows = %d, want 1", n)
		}
		if current, ok := scanOneNullableInt(t, db, "SELECT current_version_id FROM category_workflows WHERE category_id=?", category.ID); !ok || current == 0 {
			t.Errorf("valid publish must switch the current version, got (%d, %v)", current, ok)
		}
	})
}

// RED — HTMX 2.0.4 default response handling swaps only 2xx/3xx and treats
// every 4xx/5xx response as a non-swappable error (responseHandling
// `[23]..` swap / `[45]..` error), so the builder's 422 validation fragment
// would never replace #workflow-builder in a real browser. The builder must
// carry a form-scoped hx-on::before-swap policy that swaps ONLY the expected
// status 422 into the swap target and marks it non-error, without weakening
// other 4xx/5xx handling.
func TestCategoryWorkflowBuilder_RED_HTMX422SwapsIntoBuilder(t *testing.T) {
	const policy = `hx-on::before-swap="if(event.detail.xhr.status === 422){event.detail.shouldSwap = true; event.detail.isError = false}"`

	h := newHarness(t)
	category, err := h.categories.Create(t.Context(), "HTMX 422")
	if err != nil {
		t.Fatalf("create category: %v", err)
	}
	path := "/categories/" + strconv.FormatInt(category.ID, 10) + "/workflow"

	// The swap target the form points at (#workflow-builder) must configure the
	// before-swap policy both on the initial full page and on the 422 fragment
	// the browser must swap in.
	t.Run("full page configures before-swap on the builder target", func(t *testing.T) {
		rec := h.get(t, path, false)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET status = %d, want 200", rec.Code)
		}
		section := builderSectionOpenTag(rec.Body.String())
		if section == "" {
			t.Fatalf("page must render #workflow-builder, got: %s", rec.Body.String())
		}
		if !strings.Contains(section, policy) {
			t.Errorf("full page: #workflow-builder must configure before-swap so status 422 is swapped in and marked non-error; section tag: %s", section)
		}
	})

	t.Run("422 fragment itself carries the policy for the next swap", func(t *testing.T) {
		// Invalid publish renders the 422 validation fragment — the exact response
		// that must be swapped into #workflow-builder in the browser.
		rec := h.postForm(t, path, builderForm("publish", builderDraft(t, "")), true)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("invalid publish status = %d, want 422", rec.Code)
		}
		section := builderSectionOpenTag(rec.Body.String())
		if section == "" {
			t.Fatalf("422 response must render #workflow-builder, got: %s", rec.Body.String())
		}
		if !strings.Contains(section, policy) {
			t.Errorf("422 fragment: #workflow-builder must configure before-swap so status 422 is swapped in and marked non-error; section tag: %s", section)
		}
	})

	t.Run("policy is builder-scoped, not a global 4xx/5xx weakening", func(t *testing.T) {
		body := h.get(t, "/categories", false).Body.String()
		if strings.Contains(body, "hx-on::before-swap") || strings.Contains(body, "shouldSwap") {
			t.Error("unrelated pages must not inherit the builder before-swap policy")
		}
	})
}

// builderSectionOpenTag extracts the rendered #workflow-builder opening tag.
func builderSectionOpenTag(body string) string {
	return regexp.MustCompile(`<section id="workflow-builder"[^>]*>`).FindString(body)
}

// TestCategoryWorkflowBuilder_MobileStyles_WrapNarrow is the rendered-style
// regression for the Playwright-proven 390px overflow (document width 418px:
// .workflow-step-head .row-actions ended at x=418 without wrapping and the
// builder form/ol overflowed). Layout cannot be measured in Go, so the
// regression freezes the CSS contract the real browser needs: the shared
// embedded stylesheet must configure a max-width:640 mobile layout where
// builder headers/action rows wrap or stack, the step type control can
// shrink, the mobile rail/nav cannot impose a residual min-content width,
// and buttons stay keyboard-accessible (wrapped, never hidden).
func TestCategoryWorkflowBuilder_MobileStyles_WrapNarrow(t *testing.T) {
	body := renderGolden(t, "category_workflow", "", mobileBuilderPageData(), false)
	style := extractStyleBlock(t, body)
	mobile := extractMediaBlock(t, style, "max-width:640px")
	if mobile == "" {
		t.Fatal("shared styles must define the max-width:640 mobile block")
	}

	// The Playwright evidence named .workflow-step-head .row-actions ending at
	// x=418 without wrapping. Every builder header/action row must wrap or
	// stack under 640px, the type control must be able to shrink, and the
	// mobile rail/nav must not impose a residual min-content width.
	for _, tc := range []struct {
		name string
		sel  string
		decl string
	}{
		{"builder head wraps", ".workflow-builder-head{", "flex-wrap:wrap"},
		{"editor head wraps", ".workflow-editor-head{", "flex-wrap:wrap"},
		{"step cards stay compact", ".workflow-step-card{", "flex-basis:180px"},
		{"field rows stack in mobile grid", ".workflow-field-row{", "grid-template-columns:1fr 44px"},
		{"mobile rail wraps", ".rail{", "flex-wrap:wrap"},
		{"mobile nav wraps", ".rail-nav{", "flex-wrap:wrap"},
	} {
		if !cssRuleDeclares(mobile, tc.sel, tc.decl) {
			t.Errorf("max-width:640 block must declare %s on %s", tc.decl, tc.name)
		}
	}
	if !cssRuleDeclares(mobile, ".rail-nav{", "min-width:0") {
		t.Error("max-width:640 block must let .rail-nav shrink below its min-content width (no residual rail/nav min-width)")
	}

	// Keyboard accessibility: wrapping keeps controls visible and focusable —
	// nothing in the mobile block may hide controls, and the global
	// :focus-visible outline contract must survive.
	for _, hidden := range []string{"display:none", "visibility:hidden", "pointer-events:none"} {
		if strings.Contains(mobile, hidden) {
			t.Errorf("max-width:640 block must not hide controls (%s present)", hidden)
		}
	}
	if !strings.Contains(style, ":focus-visible{outline:3px solid var(--accent)") {
		t.Error("global :focus-visible outline contract must remain for keyboard users")
	}

	// Desktop is untouched: the base flex rules stay as-is (wrap only under
	// 640px — no redesign).
	if !cssRuleDeclares(style, ".workflow-editor-head{", "display:flex") {
		t.Error("desktop editor flex layout must remain defined")
	}
}

// mobileBuilderPageData builds a full builder-page fixture exercising the
// manual-task and assign-to-desk step controls so the rendered page carries
// the shared stylesheet and the complete builder markup.
func mobileBuilderPageData() workflowBuilderData {
	ana := domain.User{ID: 1, Name: "Ana Torres", Email: "ana@example.com", Active: true, Role: domain.RoleAdmin, CreatedAt: goldenT0}
	return workflowBuilderData{
		pageData: pageData{
			NavActive:           "categories",
			CurrentUser:         ana,
			CanManageUsers:      true,
			CanManageCategories: true,
			CanManageDesks:      true,
		},
		CategoryID: 1,
		Draft: domain.WorkflowDefinition{
			{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "Triage and answer the ticket."}},
			{Type: domain.StepAssignToDesk, AssignToDesk: &domain.AssignToDeskStep{DeskID: 1, Strategy: domain.StrategyClaim}},
		},
		Desks:     []domain.Desk{{ID: 1, Name: "Support", CreatedAt: goldenT0}},
		Live:      "Use Up and Down to change a step position.",
		FocusStep: -1,
	}
}

// extractStyleBlock returns the content of the page's single embedded <style>.
func extractStyleBlock(t *testing.T, body string) string {
	t.Helper()
	start := strings.Index(body, "<style>")
	if start < 0 {
		t.Fatalf("page must embed the shared <style> block")
	}
	start += len("<style>")
	end := strings.Index(body[start:], "</style>")
	if end < 0 {
		t.Fatalf("page must close the shared <style> block")
	}
	return body[start : start+end]
}

// extractMediaBlock returns the body of the first @media (query){...} block.
func extractMediaBlock(t *testing.T, css, query string) string {
	t.Helper()
	marker := "@media (" + query + "){"
	idx := strings.Index(css, marker)
	if idx < 0 {
		return ""
	}
	start := idx + len(marker)
	depth := 1
	for i := start; i < len(css); i++ {
		switch css[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return css[start:i]
			}
		}
	}
	t.Fatalf("unterminated @media (%s) block", query)
	return ""
}

// cssRuleDeclares reports whether css contains a rule whose selector exactly
// matches sel (selector text plus the opening brace) with decl inside its
// declaration block.
func cssRuleDeclares(css, sel, decl string) bool {
	pos := 0
	for {
		idx := strings.Index(css[pos:], sel)
		if idx < 0 {
			return false
		}
		bodyStart := pos + idx + len(sel)
		bodyEnd := matchBrace(css, bodyStart)
		if bodyEnd < 0 {
			return false
		}
		if strings.Contains(css[bodyStart:bodyEnd], decl) {
			return true
		}
		pos = bodyStart
	}
}

// matchBrace returns the index of the closing brace matching the opening
// brace at open, or -1 when the block is unterminated.
func matchBrace(css string, open int) int {
	depth := 1
	for i := open; i < len(css); i++ {
		switch css[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

func builderDraft(t *testing.T, instructions ...string) string {
	t.Helper()
	steps := make(domain.WorkflowDefinition, len(instructions))
	for i, instruction := range instructions {
		steps[i] = domain.WorkflowStep{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: instruction}}
	}
	b, err := steps.MarshalCanonical()
	if err != nil {
		t.Fatalf("canonical draft: %v", err)
	}
	return string(b)
}

func builderForm(action, draft string) url.Values {
	return url.Values{"action": {action}, "draft": {draft}}
}

// ==== PR4: typed Add step + horizontal menu actions ====
//
// add_step_type is an optional presentation-only HTTP input validated against
// the closed domain step-type set; absent or unknown values preserve the
// existing default manual-step behavior, and the value never persists.
func TestCategoryWorkflowBuilder_TypedAddStepTypeValidation(t *testing.T) {
	for _, tc := range []struct {
		name string
		typ  string
		want domain.StepType
	}{
		{name: "manual task", typ: "manual_task", want: domain.StepManualTask},
		{name: "assign to desk", typ: "assign_to_desk", want: domain.StepAssignToDesk},
		{name: "form", typ: "form", want: domain.StepForm},
		{name: "resolve ticket", typ: "resolve_ticket", want: domain.StepResolve},
		{name: "close ticket", typ: "close_ticket", want: domain.StepClose},
		{name: "unknown type falls back to manual", typ: "review_ticket", want: domain.StepManualTask},
		{name: "absent type keeps default manual", typ: "", want: domain.StepManualTask},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			category, err := h.categories.Create(t.Context(), "Typed "+tc.name)
			if err != nil {
				t.Fatalf("create: %v", err)
			}
			path := "/categories/" + strconv.FormatInt(category.ID, 10) + "/workflow"
			f := builderFieldForm("add_step")
			if tc.typ != "" {
				f.Set("add_step_type", tc.typ)
			}
			wantRedirect(t, h.postForm(t, path, f, false), http.StatusSeeOther, path)
			def := h.persistedDefinition(t, path)
			if len(def) != 1 {
				t.Fatalf("typed add must append exactly one step, got %d: %+v", len(def), def)
			}
			if def[0].Type != tc.want {
				t.Errorf("typed add type = %s, want %s", def[0].Type, tc.want)
			}
			switch tc.want {
			case domain.StepAssignToDesk:
				if def[0].AssignToDesk == nil {
					t.Error("assign_to_desk add must initialize its closed payload")
				}
			case domain.StepForm:
				if def[0].Form == nil || def[0].Form.Actor != domain.FormActorRequester {
					t.Error("form add must initialize a requester form payload")
				}
			case domain.StepResolve, domain.StepClose:
				if def[0].AssignToDesk != nil || def[0].Form != nil || def[0].ManualTask != nil {
					t.Error("terminal add must carry no config")
				}
			}
		})
	}
}

// TypedAddPopoverMarkup freezes the anchored popover contract: the + Add step
// control opens a disclosure listing exactly the five closed step types with
// human labels; every choice submits the EXISTING add_step action with the
// optional add_step_type for both HTMX (outerHTML swap, no history push) and the
// full-page no-JS submit on the same endpoint/action.
func TestCategoryWorkflowBuilder_TypedAddPopoverMarkup(t *testing.T) {
	h := newHarness(t)
	category, err := h.categories.Create(t.Context(), "Popover")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	path := "/categories/" + strconv.FormatInt(category.ID, 10) + "/workflow"
	wantRedirect(t, h.postForm(t, path, builderFieldForm("save", bstep{typ: "manual_task", manual: "seed"}), false), http.StatusSeeOther, path)
	body := h.get(t, path, false).Body.String()
	if !strings.Contains(body, `class="workflow-add-popover"`) || !strings.Contains(body, `<summary class="btn ghost">+ Add step</summary>`) {
		t.Fatalf("builder must render the anchored + Add step popover, got: %s", body)
	}
	if got := strings.Count(body, `name="action" value="add_step"`); got != 5 {
		t.Errorf("typed add submitters = %d, want 5", got)
	}
	for _, tc := range []struct {
		typ   string
		label string
	}{
		{"manual_task", "Manual task"},
		{"assign_to_desk", "Assign to desk"},
		{"form", "Form"},
		{"resolve_ticket", "Resolve ticket"},
		{"close_ticket", "Close ticket"},
	} {
		if !strings.Contains(body, "?add_step_type="+tc.typ+"\"") || !strings.Contains(body, ">"+tc.label+"</button>") {
			t.Errorf("popover must offer %q carrying add_step_type=%s, got: %s", tc.label, tc.typ, body)
		}
	}
	if !strings.Contains(body, `type="submit" name="action" value="add_step" formaction="`+path+`?add_step_type=`) || !strings.Contains(body, `hx-include="closest form"`) || !strings.Contains(body, `hx-swap="outerHTML show:none"`) || !strings.Contains(body, `hx-push-url="false"`) {
		t.Errorf("popover choices must work as no-JS submits and HTMX outerHTML swaps without history push, got: %s", body)
	}
}

// TypedAddTerminalProtection freezes scenario protection: a draft that already
// contains a terminal step offers no terminal choice and no insertion position
// after the final step, and crafted add requests leave the draft unchanged; the
// terminal keeps its Final badge and removal guard.
func TestCategoryWorkflowBuilder_TypedAddTerminalProtection(t *testing.T) {
	h := newHarness(t)
	category, err := h.categories.Create(t.Context(), "Terminal guard")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	path := "/categories/" + strconv.FormatInt(category.ID, 10) + "/workflow"
	steps := []bstep{{typ: "manual_task", manual: "first"}, {typ: "close_ticket"}}
	wantRedirect(t, h.postForm(t, path, builderFieldForm("save", steps...), false), http.StatusSeeOther, path)
	body := h.get(t, path, false).Body.String()
	if !strings.Contains(body, `class="workflow-add-popover"`) {
		t.Errorf("final draft must still offer the Add step popover, got: %s", body)
	}
	for _, disallowed := range []string{"add_step_type=resolve_ticket", "add_step_type=close_ticket"} {
		if strings.Contains(body, disallowed) {
			t.Errorf("final draft must not offer terminal add choices, found %q", disallowed)
		}
	}
	for _, want := range []string{`workflow-final-badge">Final</span>`} {
		if !strings.Contains(body, want) {
			t.Errorf("terminal draft must contain %q, got: %s", want, body)
		}
	}
	// The final card must not be draggable and must expose no action menu; only
	// the single editable step has a drag handle, a menu, and a removal.
	if strings.Count(body, `class="workflow-drag-handle"`) != 1 {
		t.Errorf("final draft must expose exactly one drag handle (only the editable step), body=%s", body)
	}
	if got := strings.Count(body, `class="workflow-step-menu"`); got != 1 {
		t.Errorf("final draft must expose exactly one step menu (only the editable step), got %d", got)
	}
	if got := strings.Count(body, `name="action" value="remove_step"`); got != 1 {
		t.Errorf("final draft must expose exactly one removal (only the editable step), got %d", got)
	}
	// Every add (default, terminal-typed, non-terminal) inserts immediately before
	// the final step and keeps the terminal last and final.
	for _, tc := range []struct {
		name string
		typ  string
		want domain.StepType
	}{
		{"default add", "", domain.StepManualTask},
		{"duplicate terminal", "close_ticket", domain.StepManualTask},
		{"non-terminal after final", "form", domain.StepForm},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := builderFieldForm("add_step", steps...)
			if tc.typ != "" {
				f.Set("add_step_type", tc.typ)
			}
			wantRedirect(t, h.postForm(t, path, f, false), http.StatusSeeOther, path)
			def := h.persistedDefinition(t, path)
			if len(def) != 3 || def[2].Type != domain.StepClose || def[1].Type != tc.want {
				t.Errorf("%s: insert must land directly before the final step, got %+v", tc.name, def)
			}
			// Reset to the two-step baseline for the next case.
			wantRedirect(t, h.postForm(t, path, builderFieldForm("save", steps...), false), http.StatusSeeOther, path)
		})
	}
}

// MenuLabelsHorizontal freezes the horizontal rail action names: Move left and
// Move right replace the vertical labels while the backend action values,
// endpoints, and persistence contract stay byte-for-byte unchanged.
func TestCategoryWorkflowBuilder_MenuLabelsHorizontal(t *testing.T) {
	h := newHarness(t)
	category, err := h.categories.Create(t.Context(), "Horizontal menu")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	path := "/categories/" + strconv.FormatInt(category.ID, 10) + "/workflow"
	steps := []bstep{{typ: "manual_task", manual: "a"}, {typ: "manual_task", manual: "b"}}
	wantRedirect(t, h.postForm(t, path, builderFieldForm("save", steps...), false), http.StatusSeeOther, path)
	body := h.get(t, path, false).Body.String()
	if !strings.Contains(body, ">Move left</button>") || !strings.Contains(body, ">Move right</button>") {
		t.Errorf("menu must label horizontal actions Move left/Move right, got: %s", body)
	}
	for _, legacy := range []string{">Move up<", ">Move down<"} {
		if strings.Contains(body, legacy) {
			t.Errorf("menu must not keep vertical label %q, got: %s", legacy, body)
		}
	}
	// Impossible moves are hidden, not disabled: the first card has no Move left,
	// the last card (no final step here) has no Move right.
	if got := strings.Count(body, `name="action" value="move_up"`); got != 1 {
		t.Errorf("move_up submitters = %d, want 1 (only the not-first card)", got)
	}
	if got := strings.Count(body, `name="action" value="move_down"`); got != 1 {
		t.Errorf("move_down submitters = %d, want 1 (only the not-last card)", got)
	}
	if got := strings.Count(body, `name="action" value="remove_step"`); got != 2 {
		t.Errorf("remove_step submitters = %d, want 2 (one per card)", got)
	}
	if strings.Contains(body, `value="move_up" disabled`) || strings.Contains(body, `value="move_down" disabled`) {
		t.Errorf("impossible moves must be hidden rather than disabled, got: %s", body)
	}
}

// KeyboardActionFallbacks freezes the no-drag fallback contract: menu actions
// are real native submit buttons (Enter/Space run the same reorder/remove
// request) for no-JS and HTMX, the page restores visible focus to the selected
// card (or the Add step control when the draft empties), and removal
// recalculates a safe neighbor or clears selection.
// Polish: the Form editor lays fields out in compact rows (Label, Kind, Required,
// and a field menu with Remove field) under a Fields header; selecting a final
// Resolve/Close step renders only its title, description, and helper with no
// editable controls. The Type selector is gone from every editor.
func TestCategoryWorkflowBuilder_PolishFormAndFinalEditors(t *testing.T) {
	h := newHarness(t)
	category, err := h.categories.Create(t.Context(), "Editor polish")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	path := "/categories/" + strconv.FormatInt(category.ID, 10) + "/workflow"
	wantRedirect(t, h.postForm(t, path, builderFieldForm("save", buildingSteps()...), false), http.StatusSeeOther, path)
	// Form editor (index 1): Fields header + right-aligned Add field + compact
	// field row holding Label, Kind, Required, and a field menu with Remove field;
	// no visible Type selector.
	body := h.get(t, path+"?selected_step_index=1", false).Body.String()
	for _, want := range []string{"<h4>Fields</h4>", ">+ Add field</button>", `class="workflow-field-row"`, `name="step_1_field_0_label"`, `name="step_1_field_0_required"`, `class="workflow-field-menu"`, `value="remove_field"`} {
		if !strings.Contains(body, want) {
			t.Errorf("form editor missing %q", want)
		}
	}
	// Final editor (index 2) shows only title/desc/helper and no editable controls.
	body = h.get(t, path+"?selected_step_index=2", false).Body.String()
	editor := body
	if i := strings.Index(editor, `<section class="workflow-editor-panel"`); i >= 0 {
		editor = editor[i:]
		if j := strings.Index(editor, `</section>`); j >= 0 {
			editor = editor[:j]
		}
	}
	for _, want := range []string{"Step 3 · Close ticket", "Runs automatically and must remain final."} {
		if !strings.Contains(editor, want) {
			t.Errorf("final editor missing %q", want)
		}
	}
	for _, bad := range []string{`name="step_2_instructions"`, `<textarea`, `name="action" value="remove_step"`} {
		if strings.Contains(editor, bad) {
			t.Errorf("final editor must not expose editable controls %q", bad)
		}
	}
}

// ThreeDotTriggerPolish freezes the shared trigger contract: step and field
// menus use one reusable .workflow-trigger style (exactly 32x32, centered
// glyph, no border at rest, gray hover, accent focus ring) with the exact
// contextual accessible names, the immutable terminal step stays trigger-less,
// the upper-right placements survive, and the asset closes an open menu on
// Escape and returns focus to the trigger.
func TestCategoryWorkflowBuilder_ThreeDotTriggerPolish(t *testing.T) {
	h := newHarness(t)
	category, err := h.categories.Create(t.Context(), "Trigger polish")
	if err != nil {
		t.Fatalf("create category: %v", err)
	}
	path := "/categories/" + strconv.FormatInt(category.ID, 10) + "/workflow"
	steps := []bstep{
		{typ: "manual_task", manual: "a"},
		{typ: "form", actor: "requester", fields: []bfield{{key: "f0", label: "Text", kind: "short_text"}}},
	}
	wantRedirect(t, h.postForm(t, path, builderFieldForm("save", steps...), false), http.StatusSeeOther, path)
	body := h.get(t, path+"?selected_step_index=1", false).Body.String()
	// Both rail steps and the selected form's field share one trigger class;
	// each carries its contextual accessible name and the centered glyph.
	if got := strings.Count(body, `class="workflow-trigger"`); got != 3 {
		t.Errorf("shared trigger instances = %d, want 3 (two steps + one field), body=%s", got, body)
	}
	if got := strings.Count(body, `aria-label="Step actions"`); got != 2 {
		t.Errorf(`aria-label="Step actions" count = %d, want 2 (one per step card)`, got)
	}
	if got := strings.Count(body, `aria-label="Field actions"`); got != 1 {
		t.Errorf(`aria-label="Field actions" count = %d, want 1 (the form field row)`, got)
	}
	if strings.Contains(body, "Actions for step") {
		t.Errorf("step trigger must use the exact 'Step actions' name, got: %s", body)
	}
	if got := strings.Count(body, "⋯"); got != 3 {
		t.Errorf("centered ellipsis count = %d, want 3", got)
	}
	// Shared style rules: fixed hit area, centered glyph, transparent rest
	// state, gray hover, accent focus ring, and preserved upper-right spots.
	page := renderGolden(t, "category_workflow", "", mobileBuilderPageData(), false)
	style := extractStyleBlock(t, page)
	for _, tc := range []struct {
		name, sel, decl string
	}{
		{"hit area exactly 32x32", ".workflow-trigger{", "width:32px;height:32px"},
		{"glyph centered via flex", ".workflow-trigger{", "display:flex"},
		{"no visible border at rest", ".workflow-trigger{", "border:0"},
		{"transparent at rest", ".workflow-trigger{", "background:transparent"},
		{"gray hover background", ".workflow-trigger:hover{", "background:var(--gray-soft)"},
		{"accent focus ring", ".workflow-trigger:focus-visible{", "outline:3px solid var(--accent)"},
		{"step trigger stays upper-right", ".workflow-step-menu{", "position:absolute"},
		{"field trigger stays in fixed actions column", ".workflow-field-actions{", "grid-column:4"},
	} {
		if !cssRuleDeclares(style, tc.sel, tc.decl) {
			t.Errorf("%s: rule %s must declare %s", tc.name, tc.sel, tc.decl)
		}
	}
	// An immutable terminal step keeps its Final position and exposes no
	// trigger at all, so a final-only draft leaves the editor trigger-less.
	terminal, err := h.categories.Create(t.Context(), "Trigger polish terminal")
	if err != nil {
		t.Fatalf("create terminal category: %v", err)
	}
	tpath := "/categories/" + strconv.FormatInt(terminal.ID, 10) + "/workflow"
	wantRedirect(t, h.postForm(t, tpath, builderFieldForm("save", []bstep{{typ: "close_ticket"}}...), false), http.StatusSeeOther, tpath)
	tbody := h.get(t, tpath, false).Body.String()
	if got := strings.Count(tbody, `class="workflow-step-menu"`); got != 0 {
		t.Errorf("terminal-only draft must render no step menu, got %d", got)
	}
	if got := strings.Count(tbody, `class="workflow-trigger"`); got != 0 {
		t.Errorf("terminal-only draft must render no trigger, got %d", got)
	}
	// The asset keeps the viewport-fixed positioning and adds the narrow
	// Escape contract: closing the open menu and refocusing its trigger.
	asset := h.get(t, "/static/workflow.js", false)
	if asset.Code != http.StatusOK {
		t.Fatalf("workflow asset status = %d, want 200", asset.Code)
	}
	for _, want := range []string{`event.key !== "Escape"`, `closest(".workflow-step-menu, .workflow-field-menu")`, `details.open = false`, `summary.focus()`} {
		if !strings.Contains(asset.Body.String(), want) {
			t.Errorf("workflow asset must contain %q", want)
		}
	}
}

// The final terminal is switched between Resolve and Close through a small select
// with exactly those two options (existing change_type action); other types are
// not offered, and the terminal stays last and non-removable.
func TestCategoryWorkflowBuilder_TerminalSelect(t *testing.T) {
	h := newHarness(t)
	category, err := h.categories.Create(t.Context(), "Terminal select")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	path := "/categories/" + strconv.FormatInt(category.ID, 10) + "/workflow"
	steps := []bstep{{typ: "manual_task", manual: "a"}, {typ: "close_ticket"}}
	wantRedirect(t, h.postForm(t, path, builderFieldForm("save", steps...), false), http.StatusSeeOther, path)
	body := h.get(t, path+"?selected_step_index=1", false).Body.String()
	for _, want := range []string{`name="step_1_type"`, `value="resolve_ticket"`, `value="close_ticket"`, `>Close ticket</option>`} {
		if !strings.Contains(body, want) {
			t.Errorf("terminal select missing %q, got: %s", want, body)
		}
	}
	if strings.Contains(body, `value="manual_task"`) || strings.Contains(body, `value="form"`) || strings.Contains(body, `value="assign_to_desk"`) {
		t.Errorf("terminal select must offer only the two terminal types, got: %s", body)
	}
	// Changing the terminal to Resolve via the existing change_type action.
	f := builderFieldForm("change_type", steps...)
	f.Set("step_index", "1")
	f.Set("step_1_type", "resolve_ticket")
	wantRedirect(t, h.postForm(t, path, f, false), http.StatusSeeOther, path)
	def := h.persistedDefinition(t, path)
	if len(def) != 2 || def[0].Type != domain.StepManualTask || def[1].Type != domain.StepResolve {
		t.Errorf("terminal select must convert Close to Resolve, got %+v", def)
	}
}

func TestCategoryWorkflowBuilder_KeyboardActionFallbacks(t *testing.T) {
	t.Run("menu buttons are native submits with no-JS and HTMX requests", func(t *testing.T) {
		h := newHarness(t)
		category, err := h.categories.Create(t.Context(), "Keyboard")
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		path := "/categories/" + strconv.FormatInt(category.ID, 10) + "/workflow"
		steps := []bstep{{typ: "manual_task", manual: "a"}, {typ: "manual_task", manual: "b"}, {typ: "manual_task", manual: "c"}}
		wantRedirect(t, h.postForm(t, path, builderFieldForm("save", steps...), false), http.StatusSeeOther, path)
		body := h.get(t, path+"?selected_step_index=1", false).Body.String()
		for _, action := range []string{"move_up", "move_down", "remove_step"} {
			if !strings.Contains(body, `type="submit" name="action" value="`+action+`"`) || !strings.Contains(body, `formaction="`+path+`?step_index=`) || !strings.Contains(body, `hx-post="`+path+`?step_index=`) {
				t.Errorf("menu %s must stay a keyboard-operable submit with no-JS formaction and HTMX post, got: %s", action, body)
			}
		}
		if !strings.Contains(body, `.workflow-step-card.selected .workflow-step-card-link`) || !strings.Contains(body, `.workflow-add-step summary`) {
			t.Errorf("page must restore visible focus to the selected card or the Add step control after swaps, got: %s", body)
		}
	})

	t.Run("HTMX remove of the selected step selects a safe neighbor", func(t *testing.T) {
		h := newHarness(t)
		category, err := h.categories.Create(t.Context(), "Safe neighbor")
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		path := "/categories/" + strconv.FormatInt(category.ID, 10) + "/workflow"
		steps := []bstep{{typ: "manual_task", manual: "a"}, {typ: "manual_task", manual: "b"}, {typ: "manual_task", manual: "c"}}
		wantRedirect(t, h.postForm(t, path, builderFieldForm("save", steps...), false), http.StatusSeeOther, path)
		f := builderFieldForm("remove_step", steps...)
		f.Set("step_index", "1")
		f.Set("selected_step_index", "1")
		rec := h.postForm(t, path, f, true)
		if rec.Code != http.StatusOK {
			t.Fatalf("HTMX remove status = %d, want 200", rec.Code)
		}
		body := rec.Body.String()
		for _, want := range []string{`name="selected_step_index" value="1"`, `class="workflow-step-card selected"`, ">c</textarea>", `id="workflow-builder"`} {
			if !strings.Contains(body, want) {
				t.Errorf("remove must select the safe neighbor at index 1, missing %q: %s", want, body)
			}
		}
		if got := strings.Count(body, `class="workflow-editor-panel"`); got != 1 {
			t.Errorf("remove must keep exactly one editor, got %d", got)
		}
	})

	t.Run("HTMX remove of the only step clears selection and shows the empty state", func(t *testing.T) {
		h := newHarness(t)
		category, err := h.categories.Create(t.Context(), "Only step")
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		path := "/categories/" + strconv.FormatInt(category.ID, 10) + "/workflow"
		wantRedirect(t, h.postForm(t, path, builderFieldForm("save", bstep{typ: "manual_task", manual: "only"}), false), http.StatusSeeOther, path)
		f := builderFieldForm("remove_step", bstep{typ: "manual_task", manual: "only"})
		f.Set("step_index", "0")
		f.Set("selected_step_index", "0")
		body := h.postForm(t, path, f, true).Body.String()
		if strings.Contains(body, `class="workflow-editor-panel"`) || strings.Contains(body, `name="selected_step_index"`) {
			t.Errorf("removing the only step must clear selection and the stale editor, got: %s", body)
		}
		for _, want := range []string{"No steps yet. Add a step to begin configuration.", `class="workflow-add-popover"`} {
			if !strings.Contains(body, want) {
				t.Errorf("empty draft must show the empty state and keep Add step, missing %q: %s", want, body)
			}
		}
	})
}

// DragReorder freezes the horizontal reorder contract: the existing POST
// action persists the drag order, recalculates selected_step_index to the
// moved step's destination, and fails closed server-side for terminal moves
// and out-of-range indexes with the inline error and the persisted order
// unchanged.
func TestCategoryWorkflowBuilder_DragReorder(t *testing.T) {
	t.Run("applies a valid order and recalculates selection to the moved destination", func(t *testing.T) {
		h := newHarness(t)
		category, err := h.categories.Create(t.Context(), "Drag")
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		path := "/categories/" + strconv.FormatInt(category.ID, 10) + "/workflow"
		steps := []bstep{{typ: "manual_task", manual: "first"}, {typ: "manual_task", manual: "second"}, {typ: "manual_task", manual: "third"}}
		wantRedirect(t, h.postForm(t, path, builderFieldForm("save", steps...), false), http.StatusSeeOther, path)
		f := builderFieldForm("reorder", steps...)
		f.Set("source_index", "0")
		f.Set("target_index", "2")
		f.Set("selected_step_index", "1")
		rec := h.postForm(t, path, f, true)
		if rec.Code != http.StatusOK {
			t.Fatalf("HTMX reorder status = %d, want 200", rec.Code)
		}
		def := h.persistedDefinition(t, path)
		if def[0].ManualTask.Instructions != "second" || def[1].ManualTask.Instructions != "third" || def[2].ManualTask.Instructions != "first" {
			t.Errorf("reorder order = [%s %s %s], want [second third first]", def[0].ManualTask.Instructions, def[1].ManualTask.Instructions, def[2].ManualTask.Instructions)
		}
		// The dragged step (was index 0) now sits at index 2 and the HTTP layer
		// must follow the selection to its destination.
		for _, want := range []string{`name="selected_step_index" value="2"`, `class="workflow-step-card selected"`} {
			if !strings.Contains(rec.Body.String(), want) {
				t.Errorf("reorder response must carry moved-step selection, missing %q: %s", want, rec.Body.String())
			}
		}
	})

	t.Run("full-page reorder redirects and persists the same order", func(t *testing.T) {
		h := newHarness(t)
		category, err := h.categories.Create(t.Context(), "Drag no-JS")
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		path := "/categories/" + strconv.FormatInt(category.ID, 10) + "/workflow"
		steps := []bstep{{typ: "manual_task", manual: "first"}, {typ: "manual_task", manual: "second"}}
		wantRedirect(t, h.postForm(t, path, builderFieldForm("save", steps...), false), http.StatusSeeOther, path)
		f := builderFieldForm("reorder", steps...)
		f.Set("source_index", "1")
		f.Set("target_index", "0")
		wantRedirect(t, h.postForm(t, path, f, false), http.StatusSeeOther, path)
		def := h.persistedDefinition(t, path)
		if def[0].ManualTask.Instructions != "second" || def[1].ManualTask.Instructions != "first" {
			t.Errorf("no-JS reorder order = [%s %s], want [second first]", def[0].ManualTask.Instructions, def[1].ManualTask.Instructions)
		}
	})

	t.Run("rejects terminal and out-of-range reorders with inline error and unchanged order", func(t *testing.T) {
		steps := buildingSteps() // [manual_task, form, close_ticket]
		for _, tc := range []struct {
			name, source, target string
		}{
			{"terminal source move", "2", "0"},
			{"non-terminal after terminal", "0", "2"},
			{"out of range source", "5", "0"},
			{"out of range target", "0", "9"},
			{"negative source", "-1", "0"},
			{"malformed target", "0", "x"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				h := newHarness(t)
				category, err := h.categories.Create(t.Context(), "Reject "+tc.name)
				if err != nil {
					t.Fatalf("create: %v", err)
				}
				path := "/categories/" + strconv.FormatInt(category.ID, 10) + "/workflow"
				wantRedirect(t, h.postForm(t, path, builderFieldForm("save", steps...), false), http.StatusSeeOther, path)
				before := h.persistedDefinition(t, path)
				f := builderFieldForm("reorder", steps...)
				f.Set("source_index", tc.source)
				f.Set("target_index", tc.target)
				rec := h.postForm(t, path, f, true)
				if rec.Code != http.StatusUnprocessableEntity {
					t.Fatalf("status = %d, want 422", rec.Code)
				}
				if !strings.Contains(rec.Body.String(), `class="error-banner" role="alert"`) {
					t.Errorf("rejected reorder must render an inline validation error, got: %s", rec.Body.String())
				}
				if after := h.persistedDefinition(t, path); !reflect.DeepEqual(after, before) {
					t.Errorf("rejected reorder mutated the persisted draft: before=%+v after=%+v", before, after)
				}
			})
		}
	})

	t.Run("rejects a step after the terminal in an invalid reachable draft", func(t *testing.T) {
		h := newHarness(t)
		category, err := h.categories.Create(t.Context(), "Source after terminal")
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		path := "/categories/" + strconv.FormatInt(category.ID, 10) + "/workflow"
		steps := []bstep{{typ: "manual_task", manual: "before"}, {typ: "close_ticket"}, {typ: "form", actor: "requester", fields: []bfield{{key: "after", label: "After", kind: "short_text"}}}}
		wantRedirect(t, h.postForm(t, path, builderFieldForm("save", steps...), false), http.StatusSeeOther, path)
		before := h.persistedDefinition(t, path)
		f := builderFieldForm("reorder", steps...)
		f.Set("source_index", "2")
		f.Set("target_index", "0")
		if rec := h.postForm(t, path, f, true); rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422", rec.Code)
		}
		if after := h.persistedDefinition(t, path); !reflect.DeepEqual(after, before) {
			t.Errorf("source-after-terminal reorder mutated the persisted draft: before=%+v after=%+v", before, after)
		}
	})
}

// DragMarkup freezes the horizontal drag UI: every non-terminal card exposes
// a draggable grip labeled with its position, the final terminal card exposes
// no grip, and the builder carries exactly one permanent source_index/
// target_index pair plus the reorder submitter, wired only into this page.
func TestCategoryWorkflowBuilder_DragMarkup(t *testing.T) {
	h := newHarness(t)
	category, err := h.categories.Create(t.Context(), "Drag markup")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	path := "/categories/" + strconv.FormatInt(category.ID, 10) + "/workflow"
	wantRedirect(t, h.postForm(t, path, builderFieldForm("save", buildingSteps()...), false), http.StatusSeeOther, path)
	body := h.get(t, path, false).Body.String()
	for _, want := range []string{`class="workflow-drag-handle" draggable="true" aria-label="Drag step 1"`, `class="workflow-drag-handle" draggable="true" aria-label="Drag step 2"`} {
		if !strings.Contains(body, want) {
			t.Errorf("non-terminal cards must expose draggable grips, missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, `aria-label="Drag step 3"`) {
		t.Errorf("terminal card must not expose a drag grip, got: %s", body)
	}
	for _, field := range []string{`name="source_index"`, `name="target_index"`} {
		if got := strings.Count(body, field); got != 1 {
			t.Errorf("builder must expose exactly one permanent %s field, got %d", field, got)
		}
	}
	reorderStart := strings.Index(body, "data-workflow-reorder")
	if reorderStart < 0 {
		t.Fatalf("builder must expose the permanent reorder submitter, got: %s", body)
	}
	buttonStart := strings.LastIndex(body[:reorderStart], "<button")
	tagEnd := strings.Index(body[reorderStart:], ">")
	reorderTag := body[buttonStart : reorderStart+tagEnd+1]
	for _, want := range []string{`type="submit"`, `name="action" value="reorder"`, `class="visually-hidden"`, `aria-hidden="true"`, `tabindex="-1"`, `hx-post="` + path + `"`, `hx-target="#workflow-builder"`, `hx-swap="outerHTML"`, `hx-include="closest form"`} {
		if !strings.Contains(reorderTag, want) {
			t.Errorf("reorder submitter must contain %q, got: %s", want, reorderTag)
		}
	}
	if strings.Contains(reorderTag, " hidden") || strings.Contains(reorderTag, "hx-vals") {
		t.Errorf("reorder submitter must not use hidden state or duplicate hx-vals, got: %s", reorderTag)
	}
	if users := h.get(t, "/users", false).Body.String(); strings.Contains(users, "/static/workflow.js") {
		t.Error("workflow asset must not load on Users")
	}
	asset := h.get(t, "/static/workflow.js", false)
	if asset.Code != http.StatusOK {
		t.Fatalf("workflow asset status = %d, want 200", asset.Code)
	}
	for _, want := range []string{"dragstart", "dragover", "workflow-drag-indicator", "is-dragging", "source_index", "target_index", "data-workflow-reorder", "button.click", "workflow-step-card"} {
		if !strings.Contains(asset.Body.String(), want) {
			t.Errorf("workflow asset must contain %q", want)
		}
	}
	if strings.Contains(asset.Body.String(), "requestSubmit") {
		t.Error("workflow asset must activate the explicit reorder button without form-level requestSubmit")
	}
}

// DragResponsiveCSS freezes the 390px drag polish: the rail keeps its internal
// horizontal overflow so cards and the insertion indicator never spill onto
// the document, the indicator is inert and hidden by default, and the grip
// compacts under 640px.
func TestCategoryWorkflowBuilder_DragResponsiveCSS(t *testing.T) {
	body := renderGolden(t, "category_workflow", "", mobileBuilderPageData(), false)
	style := extractStyleBlock(t, body)
	mobile := extractMediaBlock(t, style, "max-width:640px")
	for _, tc := range []struct {
		name, sel, decl string
	}{
		{"rail scrolls without document overflow", ".workflow-step-rail{", "overflow-x:auto"},
		{"indicator stays inside the rail", ".workflow-drag-indicator{", "pointer-events:none"},
		{"indicator hidden until a drag starts", ".workflow-drag-indicator{", "display:none"},
		{"grip compacts on narrow screens", ".workflow-drag-handle{", "flex-basis:20px"},
	} {
		if tc.sel == ".workflow-drag-handle{" {
			if !cssRuleDeclares(mobile, tc.sel, tc.decl) {
				t.Errorf("max-width:640 block must declare %s on %s", tc.decl, tc.name)
			}
		} else if !cssRuleDeclares(style, tc.sel, tc.decl) {
			t.Errorf("%s must stay declared: %s on %s", tc.name, tc.decl, tc.sel)
		}
	}
}

// Checkbox semantics: a Checkbox field is boolean — Required stays available for
// text/select fields, is hidden for checkbox fields, and any legacy persisted
// required=true on a checkbox is normalized to non-required. The field row keeps
// the actions cell (with the field menu) in a fixed upper-right slot.
func TestCategoryWorkflowBuilder_CheckboxRequiredSemantics(t *testing.T) {
	h := newHarness(t)
	category, err := h.categories.Create(t.Context(), "Checkbox semantics")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	path := "/categories/" + strconv.FormatInt(category.ID, 10) + "/workflow"
	steps := []bstep{
		{typ: "manual_task", manual: "a"},
		{typ: "form", actor: "requester", fields: []bfield{
			{key: "f0", label: "Text", kind: "short_text", required: true},
			{key: "f1", label: "Flag", kind: "checkbox", required: true},
			{key: "f2", label: "Pick", kind: "single_select", options: "A; B", required: true},
		}},
	}
	wantRedirect(t, h.postForm(t, path, builderFieldForm("save", steps...), false), http.StatusSeeOther, path)
	def := h.persistedDefinition(t, path)
	if !def[1].Form.Fields[0].Required || def[1].Form.Fields[1].Required || !def[1].Form.Fields[2].Required {
		t.Fatalf("checkbox required must normalize to false while text/select keep required, got %+v", def[1].Form.Fields)
	}

	body := h.get(t, path+"?selected_step_index=1", false).Body.String()
	for _, want := range []string{`name="step_1_field_0_required"`, `name="step_1_field_2_required"`, `class="workflow-field-actions"`, `aria-label="Field actions"`} {
		if !strings.Contains(body, want) {
			t.Errorf("checkbox semantics editor missing %q", want)
		}
	}
	if strings.Contains(body, `name="step_1_field_1_required"`) {
		t.Errorf("checkbox field must not expose a Required control, got: %s", body)
	}
	if !strings.Contains(body, `class="field workflow-field-options"`) {
		t.Errorf("single-select field must keep its full-width Options row, got: %s", body)
	}

	// Changing a required text field to Checkbox clears Required on the round trip.
	changed := builderFieldForm("save", steps...)
	changed.Set("step_1_field_0_kind", "checkbox")
	changed.Set("step_1_field_0_required", "on")
	wantRedirect(t, h.postForm(t, path, changed, false), http.StatusSeeOther, path)
	after := h.persistedDefinition(t, path)
	if after[1].Form.Fields[0].Kind != domain.FieldCheckbox || after[1].Form.Fields[0].Required {
		t.Errorf("text->checkbox must clear required, got %+v", after[1].Form.Fields[0])
	}
}

// Timeline boolean rendering: submitted checkbox values render as ✓ (true) and
// × (false) with an accessible Yes/No description, while every other field keeps
// its literal value.
func TestTimelineRendersCheckboxBooleanGlyphs(t *testing.T) {
	r := NewRenderer()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/tickets/1", nil)
	req.Header.Set("HX-Request", "true")
	data := detailData{View: &application.TicketView{Timeline: []application.TimelineItem{{
		Event: &domain.AuditEvent{},
		StepFields: []application.WorkflowResponseField{
			{Label: "Opt in", Kind: "checkbox", Value: "true"},
			{Label: "Opt out", Kind: "checkbox", Value: "false"},
			{Label: "Note", Kind: "short_text", Value: "plain text"},
		},
	}}}}
	r.Render(rec, req, "tickets_show", "timeline", data, http.StatusOK)
	body := rec.Body.String()
	for _, want := range []string{`role="img" aria-label="Yes">✓`, `role="img" aria-label="No">×`, "plain text"} {
		if !strings.Contains(body, want) {
			t.Errorf("timeline checkbox rendering missing %q, got: %s", want, body)
		}
	}
}
