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

	"github.com/giulianotesta7/tkt/internal/domain"
)

// These integration contracts intentionally exercise the public builder routes
// through the real SQLite-backed HTTP harness. The form's draft value is the
// complete ordered client draft; no server-side builder state is trusted.
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
		for _, want := range []string{`id="workflow-builder"`, "<ol", "<button", `name="action" value="move_up"`, "autofocus", "aria-live"} {
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

	// Re-render must expose the editable controls, ordered.
	body := h.get(t, path, false).Body.String()
	for _, want := range []string{`name="step_0_type"`, `name="step_1_desk"`, `name="step_1_strategy"`, `name="step_2_actor"`, `name="step_2_field_0_key"`} {
		if !strings.Contains(body, want) {
			t.Errorf("re-render missing editable control %s, got: %s", want, body)
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
func TestCategoryWorkflowBuilder_TypeSelectOwnsChangeTriggeredSubmission(t *testing.T) {
	h := newHarness(t)
	category, err := h.categories.Create(t.Context(), "TypeHTMX")
	if err != nil {
		t.Fatalf("create category: %v", err)
	}
	path := "/categories/" + strconv.FormatInt(category.ID, 10) + "/workflow"
	wantRedirect(t, h.postForm(t, path, builderFieldForm("save", buildingSteps()...), false), http.StatusSeeOther, path)

	body := h.get(t, path, false).Body.String()
	tag := stepTypeTag(t, body, "0")
	for _, want := range []string{
		`name="step_0_type"`,
		`hx-trigger="change"`,
		`hx-post="/categories/` + strconv.FormatInt(category.ID, 10) + `/workflow?step_index=0"`,
		`hx-include="closest form"`,
		`hx-vals='{"action":"change_type"}'`,
		`hx-target="#workflow-builder"`,
		`hx-swap="outerHTML"`,
	} {
		if !strings.Contains(tag, want) {
			t.Errorf("step 0 type select must own %q, got: %s", want, tag)
		}
	}
	// Apply must not be a redundant visible action with HTMX active; it remains
	// available only to no-JS clients as the full-page change_type submitter.
	wantFallback := `<noscript><button class="btn ghost small" type="submit" name="action" value="change_type"`
	if !strings.Contains(body, wantFallback) {
		t.Errorf("Apply must remain only as the no-JS change_type fallback, got: %s", body)
	}
	if got := strings.Count(body, `name="action" value="change_type"`); got != len(buildingSteps()) {
		t.Errorf("change_type submitters = %d, want one noscript fallback per step", got)
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
	cid := strconv.FormatInt(category.ID, 10)
	savePost := `hx-post="/categories/` + cid + `/workflow"`
	saveVals := `hx-vals='{"action":"save"}'`
	saveInclude := `hx-include="closest form"`

	t.Run("Save draft button is gone and copy says automatic", func(t *testing.T) {
		if strings.Contains(body, "Save draft") || strings.Contains(body, `value="save"`) {
			t.Errorf("visible Save draft button must be removed entirely, got: %s", body)
		}
		if !strings.Contains(body, "Changes save automatically") {
			t.Errorf("helper copy must say changes save automatically, got: %s", body)
		}
	})

	t.Run("text controls autosave after 600ms without swapping the builder", func(t *testing.T) {
		for _, name := range []string{"step_0_instructions", "step_2_field_0_label", "step_2_field_0_options"} {
			tag := controlTag(t, body, name)
			for _, want := range []string{
				`hx-trigger="input changed delay:600ms"`,
				savePost,
				saveInclude,
				saveVals,
				`hx-swap="none"`,
			} {
				if !strings.Contains(tag, want) {
					t.Errorf("text control %s must carry %q for no-swap 600ms autosave, got: %s", name, want, tag)
				}
			}
		}
	})

	t.Run("single select options use a native single-line input and semicolon transport", func(t *testing.T) {
		tag := controlTag(t, body, "step_2_field_0_options")
		if !strings.HasPrefix(tag, "<input") || !strings.Contains(tag, `type="text"`) || strings.Contains(tag, "<textarea") {
			t.Fatalf("single select Options must be a native single-line text input, got: %s", tag)
		}
		for _, want := range []string{
			`<label for="step_2_field_0_options">Options</label>`,
			"Separate options with semicolons. Semicolons cannot be used in option names.",
			`value="North; South, Buenos Aires, Argentina"`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("single select builder must contain %q, got: %s", want, body)
			}
		}
	})

	t.Run("discrete controls autosave immediately without swapping the builder", func(t *testing.T) {
		for _, name := range []string{"step_1_desk", "step_1_strategy", "step_2_actor", "step_2_field_0_required"} {
			tag := controlTag(t, body, name)
			for _, want := range []string{`hx-trigger="change"`, savePost, saveInclude, saveVals, `hx-swap="none"`} {
				if !strings.Contains(tag, want) {
					t.Errorf("discrete control %s must carry %q for immediate no-swap autosave, got: %s", name, want, tag)
				}
			}
			if strings.Contains(tag, "delay:600ms") {
				t.Errorf("discrete control %s must not debounce, got: %s", name, tag)
			}
		}
	})

	t.Run("structural Field Kind persists immediately and rerenders the builder fragment", func(t *testing.T) {
		tag := controlTag(t, body, "step_2_field_0_kind")
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

	t.Run("existing step Type change is not double-submitted", func(t *testing.T) {
		tag := stepTypeTag(t, body, "0")
		if !strings.Contains(tag, `hx-vals='{"action":"change_type"}'`) || !strings.Contains(tag, `hx-trigger="change"`) {
			t.Errorf("step Type select must keep owning its change_type submission, got: %s", tag)
		}
		if strings.Contains(tag, "delay:600ms") || strings.Contains(tag, `"action":"save"`) {
			t.Errorf("step Type select must not participate in generic autosave triggers (double submit), got: %s", tag)
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

// stepTypeTag extracts the full opening <select ...> tag for step i's type control.
func stepTypeTag(t *testing.T, body, control string) string {
	t.Helper()
	name := `name="step_` + control + `_type"`
	idx := strings.Index(body, name)
	if idx < 0 {
		t.Fatalf("builder must render type control %s, got: %s", name, body)
	}
	open := strings.LastIndex(body[:idx], "<select")
	if open < 0 {
		t.Fatalf("type control %s must be a <select>, got: %s", name, body[:idx])
	}
	end := strings.Index(body[idx:], ">")
	if end < 0 {
		t.Fatalf("type control %s opening tag unterminated", name)
	}
	return body[open : idx+end+1]
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

	// remove_step drops the terminal at index 2.
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
		if len(def) != 2 || def[0].Type != domain.StepManualTask || def[1].Type != domain.StepForm {
			t.Errorf("remove_step result = %v, want [manual_task form]", def)
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
	for _, want := range []string{`id="workflow-builder"`, `name="action" value="move_down"`, `name="step_0_type"`, `aria-live`, `autofocus`} {
		if !strings.Contains(body, want) {
			t.Errorf("HTMX builder response must contain %q, got: %s", want, body)
		}
	}
	if got := strings.Count(body, "autofocus"); got != 1 {
		t.Errorf("moved step must render exactly one focus target, got %d", got)
	}
	if !strings.Contains(body, `name="action" value="move_up" autofocus`) {
		t.Errorf("move_down must focus the moved step's Up control, got: %s", body)
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
		{"step head wraps", ".workflow-step-head{", "flex-wrap:wrap"},
		{"step action row wraps", ".workflow-step-head .row-actions{", "flex-wrap:wrap"},
		{"header action row wraps", ".workflow-builder-head .row-actions{", "flex-wrap:wrap"},
		{"bottom actions wrap", ".workflow-actions{", "flex-wrap:wrap"},
		{"type control can shrink", ".step-type{", "min-width:0"},
		{"type label can shrink", ".step-type-label{", "min-width:0"},
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
	if !cssRuleDeclares(style, ".workflow-builder-head,.workflow-step-head,.workflow-actions{", "display:flex") {
		t.Error("desktop builder header/action flex layout must remain unchanged")
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
