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
		`<button class="page-action" type="submit" form="workflow-form" name="action" value="save">Save</button>`,
		`class="page-action page-action-primary" type="submit" form="workflow-form" name="action" value="publish">Publish</button>`,
		`<form method="post" action="/categories/`,
		`id="workflow-form"`,
		`/static/users.css`,
		// The elevation ladder is defined once in the inline token block; the
		// external users.css resolves the same custom properties on this page.
		`--elevation-raised:`,
		`--elevation-drawer:`,
		`--elevation-modal:`,
		`--scrim:`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("workflow header must contain %q, got: %s", want, body)
		}
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
		{"title geometry", []string{".users-root h1", ".page-foundation .page-title"}, []string{"font-size:24px", "line-height:1.15"}},
		{"subtitle styling", []string{".users-root .users-header p", ".page-foundation .page-subtitle"}, []string{"margin:8px 0 0", "color:var(--muted)"}},
		{"primary action geometry", []string{".users-root .users-primary-action", ".page-foundation .page-action-primary"}, []string{"display:inline-flex", "align-items:center", "justify-content:center", "padding:0 14px"}},
		{"panel surface", []string{".users-root .users-list", ".page-foundation .page-panel"}, []string{"border:1px solid var(--line)", "border-radius:12px", "background:var(--surface)"}},
	} {
		assertCSSRuleContains(t, css, tc.name, tc.selectors, tc.declarations)
	}
	// The elevation ladder is shared: users.css spends the same custom properties
	// the inline token block defines, and no hardcoded alpha-black shadow remains
	// in this file (the definitions live once in the inline block).
	for _, want := range []string{
		"background:var(--surface);box-shadow:var(--elevation-drawer)",
		"color:var(--ink);box-shadow:var(--elevation-modal)",
		"background:var(--scrim)",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("users.css must spend the shared elevation tokens, missing %q", want)
		}
	}
	for _, gone := range []string{"rgb(0 0 0 / 9%)", "rgb(0 0 0 / 12%)", "rgb(0 0 0 / 13%)", "rgb(0 0 0 / 20%)"} {
		if strings.Contains(css, gone) {
			t.Errorf("users.css must not hardcode %q; the elevation tokens own it", gone)
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
	wantRedirect(t, h.postBuilder(t, path, builderFieldForm("save", steps...), false), http.StatusSeeOther, path)
	selection := builderFieldForm("select_step", bstep{typ: "manual_task", manual: "Unsaved first"}, bstep{typ: "manual_task", manual: "Unsaved second"})
	selection.Set("selection_step_index", "1")
	noJS := h.postBuilder(t, path+"?action=select_step", selection, false)
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
	if got := categoryStatusBadge(t, body, category.Name); got != "Not configured" {
		t.Errorf("category %q status = %q, want Not configured", category.Name, got)
	}
}

// TestCategoryBadge_States pins the whole point of the honest badge: the states
// a category row must distinguish, two of which the old three-valued badge
// collapsed into the same word.
func TestCategoryBadge_States(t *testing.T) {
	cs := []struct {
		name string
		in   application.WorkflowSummary
		want string
	}{
		{"not configured", application.WorkflowSummary{}, "Not configured"},
		{"draft never published", application.WorkflowSummary{HasDraft: true}, "Draft · not published"},
		{"published, first version", application.WorkflowSummary{Version: 1, HasDraft: true}, "Published v1"},
		{"published with one unpublished change", application.WorkflowSummary{Version: 2, HasDraft: true, PendingSteps: 1}, "Published v2 · 1 unpublished change"},
		{"published with several", application.WorkflowSummary{Version: 3, HasDraft: true, PendingSteps: 4}, "Published v3 · 4 unpublished changes"},
		{
			"cannot run outranks pending work", application.WorkflowSummary{
				Version: 1, HasDraft: true, PendingSteps: 2, CannotRun: "Infra has no active members",
			},
			"Can't run · Infra has no active members",
		},
	}
	for _, c := range cs {
		t.Run(c.name, func(t *testing.T) {
			if got := categoryBadge(c.in); got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}

// TestStructureLayout_CSSPinned pins the declarations the categories screen's OWN
// inline style block must carry.
//
// That block is gated on {{if .CategoryAssets}}, so the shared stylesheet golden
// never renders it: golden_test.go splits a TICKETS index page and freezes only
// the first inline style block, while the categories rules live in a second one.
// A change to this CSS therefore turns nothing red on its own. Pinning the literal
// declarations here is this repository's convention for the second stylesheet, and
// it is the only automated guard these rules have.
func TestStructureLayout_CSSPinned(t *testing.T) {
	h := newHarness(t)
	body := h.get(t, "/categories", false).Body.String()
	for _, want := range []string{
		// The dense column stops competing with two sparse ones for equal width.
		// Measured before: 383px each, and the longest status left its category
		// name 80px while the status took 224px.
		"grid-template-columns:minmax(0,.72fr) minmax(0,.82fr) minmax(0,1.55fr)",
		// One column per row, so the tail stacks under the text instead of fighting
		// it. Measured before: a 256px desk row gave the desk name 83px.
		".category-structure-row{display:grid;grid-template-columns:minmax(0,1fr)",
		".category-structure-row .category-count,.category-structure-row .category-status-inline{display:block;margin-top:4px}",
		// An inert row must not hover like a link.
		".category-structure-row-static:hover{background:transparent}",
		// The level action keeps its label on one line when the columns narrow.
		".category-level-action{white-space:nowrap;flex-shrink:0}",
		// The dense level is a table: alignment is the whole point, and the numeric
		// column is right-aligned with tabular figures so the counts line up.
		".category-table{width:100%;border-collapse:collapse;table-layout:fixed}",
		".category-table-num{width:9%;text-align:right;font-variant-numeric:tabular-nums",
		// The categories page reuses the `users-root` shell class, so
		// users.css's generic first-column rule (`.users-root td:first-child{
		// width:46%}`) landed on this table. At mobile the shared 88px label
		// column then left the category name ~21px wide, wrapping it per
		// character. This override is scoped to the categories table and the
		// mobile media query, so the desktop first column keeps its 46%.
		".categories-root .category-table td:first-child{width:100%}",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the categories screen's inline styles must contain %q", want)
		}
	}
}

func TestCategoryWorkflowStatusBadge_UsesExactCategoryRow(t *testing.T) {
	// The fixture mirrors the rendered table row: the name is a link in the first
	// cell and the status is its own cell.
	const body = `<td class="category-table-name" data-label="Category"><a class="category-name" href="/categories/1/workflow"><strong>Target category</strong></a><small></small></td><td class="category-table-flow" data-label="Workflow"><span class="category-status-inline">Draft · not published</span></td><td class="category-table-name" data-label="Category"><a class="category-name" href="/categories/2/workflow"><strong>Other category</strong></a><small></small></td><td class="category-table-flow" data-label="Workflow"><span class="category-status-inline">Published v1</span></td>`

	// The point of the test is that a broad substring must not be able to satisfy
	// it: "Published" occurs elsewhere in the body, and the extraction must still
	// return THIS category's own status.
	if !strings.Contains(body, "Published") {
		t.Fatal("fixture must contain a Published substring a broad assertion could be fooled by")
	}
	if got := categoryStatusBadge(t, body, "Target category"); got != "Draft · not published" {
		t.Errorf("Target category status = %q, want Draft · not published", got)
	}
}

func categoryStatusBadge(t *testing.T, body, categoryName string) string {
	t.Helper()
	// The dense level is a table now: the name is a link in the first cell and the
	// status is its own cell, which is what makes the states scannable. The
	// extraction still binds to THIS category's row, never to the first one on the
	// page, and the whitespace between the cells is not assumed.
	pattern := `<td class="category-table-name"[^>]*><a class="category-name" href="/categories/\d+/workflow"><strong>` + regexp.QuoteMeta(categoryName) + `</strong></a><small>[^<]*</small></td>\s*<td class="category-table-flow"[^>]*><span class="category-status-inline">([^<]+)</span></td>`
	matches := regexp.MustCompile(pattern).FindAllStringSubmatch(body, -1)
	if len(matches) != 1 {
		t.Fatalf("category %q must have exactly one status badge row, found %d in: %s", categoryName, len(matches), body)
	}
	return matches[0][1]
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
			rec := h.postBuilder(t, path, builderForm(action, draft), false)
			wantRedirect(t, rec, http.StatusSeeOther, path)
			if got := rec.Header().Get("Set-Cookie"); !strings.Contains(got, saveFeedbackCookie+"=") {
				t.Errorf("native %s must issue a feedback flash, got %q", action, got)
			}
			hx := h.postBuilder(t, path, builderForm(action, draft), true)
			if got := hx.Header().Get("X-Save-Feedback"); !strings.Contains(got, `"save-feedback"`) {
				t.Errorf("HTMX %s must issue feedback metadata, got %q", action, got)
			}

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

func TestCategoryWorkflowBuilder_FeedbackOnlyForPersistedMutations(t *testing.T) {
	mutationCases := []struct {
		name string
		form func() url.Values
	}{
		{"add step", func() url.Values { return builderFieldForm("add_step") }},
		{"reorder", func() url.Values {
			form := builderFieldForm("reorder", bstep{typ: "manual_task", manual: "first"}, bstep{typ: "manual_task", manual: "second"})
			form.Set("source_index", "0")
			form.Set("target_index", "1")
			return form
		}},
		{"publish", func() url.Values { return builderFieldForm("publish", bstep{typ: "manual_task", manual: "publish me"}) }},
	}
	for _, tc := range mutationCases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			category, err := h.categories.Create(t.Context(), "Feedback "+tc.name)
			if err != nil {
				t.Fatalf("create category: %v", err)
			}
			path := "/categories/" + strconv.FormatInt(category.ID, 10) + "/workflow"
			native := h.postBuilder(t, path, tc.form(), false)
			wantRedirect(t, native, http.StatusSeeOther, path)
			if got := native.Header().Get("Set-Cookie"); !strings.Contains(got, saveFeedbackCookie+"=") {
				t.Errorf("native persisted %s must issue feedback, got %q", tc.name, got)
			}
			hx := h.postBuilder(t, path, tc.form(), true)
			if hx.Code != http.StatusOK {
				t.Fatalf("HTMX persisted %s status = %d, want 200", tc.name, hx.Code)
			}
			if got := hx.Header().Get("X-Save-Feedback"); !strings.Contains(got, `"save-feedback"`) {
				t.Errorf("HTMX persisted %s must issue feedback, got %q", tc.name, got)
			}
		})
	}

	for _, action := range []string{"select_step"} {
		t.Run(action+" is read-only", func(t *testing.T) {
			h := newHarness(t)
			category, err := h.categories.Create(t.Context(), "Read only "+action)
			if err != nil {
				t.Fatalf("create category: %v", err)
			}
			path := "/categories/" + strconv.FormatInt(category.ID, 10) + "/workflow"
			form := builderFieldForm(action, bstep{typ: "manual_task", manual: "first"})
			form.Set("selection_step_index", "0")
			native := h.postBuilder(t, path, form, false)
			if native.Code != http.StatusOK {
				t.Fatalf("native %s status = %d, want 200", action, native.Code)
			}
			if got := native.Header().Get("Set-Cookie"); strings.Contains(got, saveFeedbackCookie+"=") {
				t.Errorf("native read-only %s must not issue feedback, got %q", action, got)
			}
			hx := h.postBuilder(t, path, form, true)
			if hx.Code != http.StatusOK {
				t.Fatalf("HTMX %s status = %d, want 200", action, hx.Code)
			}
			if got := hx.Header().Get("X-Save-Feedback"); got != "" {
				t.Errorf("HTMX read-only %s must not issue feedback, got %q", action, got)
			}
		})
	}
}

func TestCategoryWorkflowBuilder_PublishAndHTMXParity(t *testing.T) {
	t.Run("invalid publish is a shared 422 with no writes", func(t *testing.T) {
		h := newHarness(t)
		category, err := h.categories.Create(t.Context(), "Invalid")
		if err != nil {
			t.Fatalf("create category: %v", err)
		}
		path := "/categories/" + strconv.FormatInt(category.ID, 10) + "/workflow"
		invalid := builderDraft(t, "")
		full := h.postBuilder(t, path, builderForm("publish", invalid), false)
		hx := h.postBuilder(t, path, builderForm("publish", invalid), true)
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
		full := h.postBuilder(t, path, builderForm("publish", draft), false)
		wantRedirect(t, full, http.StatusSeeOther, path)
		db := h.rawDB(t)
		if n := scanOneInt(t, db, "SELECT COUNT(*) FROM workflow_versions WHERE category_id=?", category.ID); n != 1 {
			t.Fatalf("published version rows = %d, want 1", n)
		}
		if current, ok := scanOneNullableInt(t, db, "SELECT current_version_id FROM category_workflows WHERE category_id=?", category.ID); !ok || current == 0 {
			t.Errorf("publish must atomically switch the current version, got (%d, %v)", current, ok)
		}

		hx := h.postBuilder(t, path, builderForm("move_up", draft), true)
		if hx.Code != http.StatusOK {
			t.Fatalf("HTMX mutation status = %d, want 200", hx.Code)
		}
		for _, want := range []string{`id="workflow-builder"`, "<ol", "<button", `name="action" value="move_up"`, `class="workflow-step-card selected"`} {
			if !strings.Contains(hx.Body.String(), want) {
				t.Errorf("HTMX builder response must contain %q, got: %s", want, hx.Body.String())
			}
		}

		index := h.get(t, "/categories", false)
		// The badge now names the version. A bare "Published" could not tell a
		// category on its eleventh version from one on its first.
		if got := categoryStatusBadge(t, index.Body.String(), category.Name); got != "Published v1 · by Admin" {
			t.Errorf("category %q status = %q, want Published v1 · by Admin", category.Name, got)
		}

		edited := builderDraft(t, "edited")
		wantRedirect(t, h.postBuilder(t, path, builderForm("save", edited), false), http.StatusSeeOther, path)
		// Both steps moved, so both canonical bytes differ by position; the badge
		// counts differing STEPS, which is what a one-line label can state.
		if got := categoryStatusBadge(t, h.get(t, "/categories", false).Body.String(), category.Name); got != "Published v1 · 2 unpublished changes · by Admin" {
			t.Errorf("category %q status = %q, want the version plus 2 unpublished changes and the publisher", category.Name, got)
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
	wantRedirect(t, h.postBuilder(t, path, builderForm("add_step", "[]"), false), http.StatusSeeOther, path)

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

// postBuilder posts a builder form the way a browser would: it stamps the
// category's CURRENT stored draft revision (0 when the category has no draft
// yet) onto the form, so the request is guarded exactly like a real page load.
// Tests that deliberately simulate a stale, missing or malformed revision set
// the field themselves and call postFormVerbatim directly. A post to a route
// that is not the builder (the clone route) is forwarded to postForm unchanged.
func (h *harness) postBuilder(t *testing.T, path string, form url.Values, hx bool) *httptest.ResponseRecorder {
	t.Helper()
	if id, ok := categoryIDFromWorkflowPath(path); ok {
		form.Set("draft_revision", strconv.FormatInt(h.currentDraftRevision(t, id), 10))
	}
	return h.postForm(t, path, form, hx)
}

var workflowPathID = regexp.MustCompile(`^/categories/(\d+)/workflow(?:$|\?)`)

// categoryIDFromWorkflowPath resolves the category id of a builder route. The
// clone route (/categories/{id}/workflow/clone) deliberately does not match, so
// a clone form is never given a draft revision.
func categoryIDFromWorkflowPath(path string) (int64, bool) {
	m := workflowPathID.FindStringSubmatch(path)
	if m == nil {
		return 0, false
	}
	id, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil {
		return 0, false
	}
	return id, true
}

// postFormVerbatim posts a builder form WITHOUT stamping a draft revision: the
// issue #254 tests use it to submit a stale, missing or malformed revision on
// purpose.
func (h *harness) postFormVerbatim(t *testing.T, path string, form url.Values, hx bool) *httptest.ResponseRecorder {
	t.Helper()
	return h.postForm(t, path, form, hx)
}

// currentDraftRevision reads the category's stored draft revision; a category
// with no draft row reads as 0, which is the revision a fresh builder renders.
func (h *harness) currentDraftRevision(t *testing.T, categoryID int64) int64 {
	t.Helper()
	raw := scanOneString(t, h.rawDB(t), "SELECT COALESCE((SELECT draft_revision FROM category_workflows WHERE category_id=?), 0)", categoryID)
	revision, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		t.Fatalf("parse stored draft revision %q: %v", raw, err)
	}
	return revision
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
	wantRedirect(t, h.postBuilder(t, path, builderFieldForm("save", steps...), false), http.StatusSeeOther, path)

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
		wantRedirect(t, h.postBuilder(t, path, builderFieldForm("save", steps...), false), http.StatusSeeOther, path)

		f := builderFieldForm("add_field", steps...)
		f.Set("step_index", "0")
		wantRedirect(t, h.postBuilder(t, path, f, false), http.StatusSeeOther, path)

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
		wantRedirect(t, h.postBuilder(t, path, fr, false), http.StatusSeeOther, path)

		fa := builderFieldForm("add_field", defToSteps(h.persistedDefinition(t, path))...)
		fa.Set("step_index", "0")
		wantRedirect(t, h.postBuilder(t, path, fa, false), http.StatusSeeOther, path)
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
		wantRedirect(t, h.postBuilder(t, path, builderFieldForm("save", bstep{typ: "form", actor: "requester", fields: []bfield{{key: "server", label: "Server", kind: "short_text"}}}), false), http.StatusSeeOther, path)
		edited := []bstep{{typ: "form", actor: "requester", fields: []bfield{{key: "server", label: "Production server", kind: "short_text"}}}}
		wantRedirect(t, h.postBuilder(t, path, builderFieldForm("save", edited...), false), http.StatusSeeOther, path)
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
		wantRedirect(t, h.postBuilder(t, path, builderFieldForm("save", bstep{typ: "form", actor: "requester", fields: []bfield{{key: "server", label: "Server", kind: "short_text"}}}), false), http.StatusSeeOther, path)
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
		wantRedirect(t, h.postBuilder(t, path, builderFieldForm("save", bstep{typ: "form", actor: "requester", fields: []bfield{
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
// defect: re-typing a step (e.g. manual_task -> form) must fire a change-triggered
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
	wantRedirect(t, h.postBuilder(t, path, builderFieldForm("save", buildingSteps()...), false), http.StatusSeeOther, path)

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
	// Issue #249: the summary is not truncated; the whole instruction reaches
	// the node so the CSS never has to hide it.
	if views[0].Final || !views[1].Final || !views[1].Last || views[0].Summary != strings.Repeat("界", 45) {
		t.Fatalf("terminal badge or full Unicode summary is wrong: %+v", views)
	}
	assign := workflowStepViews(domain.WorkflowDefinition{{Type: domain.StepAssignToDesk, AssignToDesk: &domain.AssignToDeskStep{DeskID: 7}}}, 0, []domain.Desk{{ID: 7, Name: "Billing"}})
	if assign[0].Summary != "Billing" {
		t.Errorf("assign summary must show the desk name, got %q", assign[0].Summary)
	}
}

// NodeVocabulary is issue #249's browser-visible contract: every builder node
// names who acts and the state outcome it produces, and the node shows the
// summary in full instead of letting the CSS ellipsis hide it.
func TestCategoryWorkflowBuilder_NodeVocabulary(t *testing.T) {
	h := newHarness(t)
	category, err := h.categories.Create(t.Context(), "Node vocabulary")
	if err != nil {
		t.Fatalf("create category: %v", err)
	}
	// The assign step routes to desk 1 (the migration-seeded General desk); a
	// desk with no eligible member cannot host an assignment step.
	h.staff(t, 1)
	path := "/categories/" + strconv.FormatInt(category.ID, 10) + "/workflow"
	const instruction = "Provision the account and confirm the welcome email"
	steps := []bstep{
		{typ: "form", actor: "requester", fields: []bfield{{key: "server", label: "Server", kind: "short_text"}}},
		{typ: "manual_task", manual: instruction},
		{typ: "assign_to_desk", desk: "1", strategy: "claim"},
		{typ: "resolve_ticket"},
	}
	wantRedirect(t, h.postBuilder(t, path, builderFieldForm("save", steps...), false), http.StatusSeeOther, path)

	body := h.get(t, path, false).Body.String()
	for _, want := range []string{
		// The whole manual-task instruction survives: no rune cap, no ellipsis.
		`<span class="workflow-step-summary">` + instruction + `</span>`,
		// Every node carries its actor.
		`<span class="workflow-step-actor">Requester</span>`,
		`<span class="workflow-step-actor">Assignee</span>`,
		`<span class="workflow-step-actor">Members of General</span>`,
		`<span class="workflow-step-actor">Automatic</span>`,
		// Routing and the terminal carry their state outcome.
		`<span class="workflow-step-outcome">→ In progress</span>`,
		`<span class="workflow-step-outcome">→ Resolved</span>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("builder node vocabulary missing %q", want)
		}
	}
	// A node must not hide the summary this issue is about.
	if truncated := instruction[:41] + "..."; strings.Contains(body, truncated) {
		t.Errorf("builder must not truncate the summary; found %q", truncated)
	}
}

// NodeSummaryIsNotClipped proves the CSS side of #249: the summary wraps
// instead of drawing an ellipsis, so a node cannot hide the words that name
// who acts and what the ticket becomes.
func TestCategoryWorkflowBuilder_NodeSummaryIsNotClipped(t *testing.T) {
	body := renderGolden(t, "category_workflow", "", mobileBuilderPageData(), false)
	style := extractStyleBlock(t, body)
	rule := cssRuleForSelectors(style, ".workflow-step-summary")
	if rule == "" {
		t.Fatal(".workflow-step-summary must be styled")
	}
	for _, gone := range []string{"text-overflow:ellipsis", "white-space:nowrap", "overflow:hidden"} {
		if strings.Contains(rule, gone) {
			t.Errorf(".workflow-step-summary must not clip its text, found %q in: %s", gone, rule)
		}
	}
	if !strings.Contains(rule, "overflow-wrap:anywhere") {
		t.Errorf(".workflow-step-summary must wrap long words instead of clipping, got: %s", rule)
	}
	// The card no longer pins the summary to a rigid width.
	card := cssRuleForSelectors(style, ".workflow-step-card")
	if strings.Contains(card, "flex:0 0 198px") {
		t.Errorf(".workflow-step-card must not keep the fixed width that forced truncation, got: %s", card)
	}
}

// TestWorkflowStepViewsNodeVocabulary triangulates issue #249 across the
// closed step set from domain/workflow.go: each kind names its actor, and only
// routing and the terminals change the ticket state.
func TestWorkflowStepViewsNodeVocabulary(t *testing.T) {
	desks := []domain.Desk{{ID: 7, Name: "Billing"}}
	for _, tc := range []struct {
		name        string
		step        domain.WorkflowStep
		wantActor   string
		wantOutcome string
	}{
		{"form for the requester", domain.WorkflowStep{Type: domain.StepForm, Form: &domain.FormStep{Actor: domain.FormActorRequester, Fields: []domain.FormField{{Key: "k", Label: "L", Kind: domain.FieldShortText}}}}, "Requester", ""},
		{"form for the assignee", domain.WorkflowStep{Type: domain.StepForm, Form: &domain.FormStep{Actor: domain.FormActorAssignee, Fields: []domain.FormField{{Key: "k", Label: "L", Kind: domain.FieldShortText}}}}, "Assignee", ""},
		{"claim routes to the desk members", domain.WorkflowStep{Type: domain.StepAssignToDesk, AssignToDesk: &domain.AssignToDeskStep{DeskID: 7, Strategy: domain.StrategyClaim}}, "Members of Billing", "In progress"},
		{"least loaded routes automatically", domain.WorkflowStep{Type: domain.StepAssignToDesk, AssignToDesk: &domain.AssignToDeskStep{DeskID: 7, Strategy: domain.StrategyLeastLoaded}}, "Automatic", "In progress"},
		{"manual task is assignee work", domain.WorkflowStep{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "Do it"}}, "Assignee", ""},
		{"resolve is automatic", domain.WorkflowStep{Type: domain.StepResolve}, "Automatic", "Resolved"},
		{"close resolves then closes", domain.WorkflowStep{Type: domain.StepClose}, "Automatic", "Resolved, then closed"},
		{"an unknown kind claims no actor", domain.WorkflowStep{Type: domain.StepType("unknown")}, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			views := workflowStepViews(domain.WorkflowDefinition{tc.step}, 0, desks)
			if views[0].Actor != tc.wantActor || views[0].Outcome != tc.wantOutcome {
				t.Fatalf("actor/outcome = %q/%q, want %q/%q", views[0].Actor, views[0].Outcome, tc.wantActor, tc.wantOutcome)
			}
		})
	}
}

// ==== Explicit save contract (issue #139 WU1) — RED: edits submit nothing on their own; the header Save submits the complete draft ====
func TestCategoryWorkflowBuilder_RED_ExplicitSaveContract(t *testing.T) {
	h := newHarness(t)
	category, err := h.categories.Create(t.Context(), "Explicit save")
	if err != nil {
		t.Fatalf("create category: %v", err)
	}
	// The workflow below routes to desk 1 (the migration-seeded General desk).
	// A desk with no eligible member cannot host an assignment step, so it must
	// be staffed before the publish this test drives through the handler.
	h.staff(t, 1)
	path := "/categories/" + strconv.FormatInt(category.ID, 10) + "/workflow"
	steps := []bstep{
		{typ: "manual_task", manual: "a"},
		{typ: "assign_to_desk", desk: "1", strategy: "least_loaded"},
		{typ: "form", actor: "requester", fields: []bfield{{key: "server", label: "Server", kind: "single_select", options: "North; South, Buenos Aires, Argentina"}}},
		{typ: "close_ticket"},
	}
	wantRedirect(t, h.postBuilder(t, path, builderFieldForm("save", steps...), false), http.StatusSeeOther, path)
	body := h.get(t, path, false).Body.String()
	bodyAt := func(index int) string {
		return h.get(t, path+"?selected_step_index="+strconv.Itoa(index), false).Body.String()
	}
	cid := strconv.FormatInt(category.ID, 10)
	savePost := `hx-post="/categories/` + cid + `/workflow"`

	if !strings.Contains(body, `<button class="page-action" type="submit" form="workflow-form" name="action" value="save">Save</button>`) ||
		strings.Contains(body, `class="page-status"`) ||
		strings.Contains(body, `>Saved<`) {
		t.Errorf("builder header must expose explicit Save with no standing Saved badge, got: %s", body)
	}
	for _, tc := range []struct {
		index int
		name  string
	}{{0, "step_0_instructions"}, {1, "step_1_desk"}, {1, "step_1_strategy"}, {2, "step_2_actor"}, {2, "step_2_field_0_label"}, {2, "step_2_field_0_options"}, {2, "step_2_field_0_required"}, {3, "step_3_type"}} {
		tag := controlTag(t, bodyAt(tc.index), tc.name)
		if strings.Contains(tag, "hx-post") || strings.Contains(tag, "hx-trigger") {
			t.Errorf("control %s must not autosave, got: %s", tc.name, tag)
		}
	}

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

	tag := controlTag(t, bodyAt(2), "step_2_field_0_kind")
	for _, want := range []string{
		`hx-trigger="change"`,
		savePost,
		`hx-vals='{"action":"select_step","selection_step_index":"2"}'`,
		`hx-target="#workflow-builder"`,
		`hx-swap="outerHTML"`,
	} {
		if !strings.Contains(tag, want) {
			t.Errorf("field Kind select must carry %q to re-render Options without persisting, got: %s", want, tag)
		}
	}
	if strings.Contains(tag, `"action":"save"`) {
		t.Errorf("field Kind select must not persist on change, got: %s", tag)
	}

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

	invalid := builderFieldForm("publish",
		bstep{typ: "manual_task", manual: ""}, bstep{typ: "form", actor: "requester", fields: []bfield{{key: "k", label: "Keep me", kind: "short_text"}}})
	invalid.Set("selected_step_index", "1")
	if rec := h.postBuilder(t, path, builderFieldForm("save", steps...), true); rec.Code != http.StatusOK {
		t.Fatalf("HTMX save = %d, want 200: %s", rec.Code, rec.Body.String())
	} else if got := rec.Header().Get("X-Save-Feedback"); !strings.Contains(got, `"message":"Saved"`) || strings.Contains(rec.Body.String(), `role="alert"`) {
		t.Errorf("HTMX save must carry the exact Saved toast copy with no errors, got header %q", got)
	}
	if pub := h.postBuilder(t, path, builderFieldForm("publish", steps...), true); pub.Code != http.StatusOK {
		t.Fatalf("HTMX publish = %d, want 200: %s", pub.Code, pub.Body.String())
	} else if got := pub.Header().Get("X-Save-Feedback"); !strings.Contains(got, `"message":"Published"`) || strings.Contains(pub.Body.String(), `role="alert"`) {
		t.Errorf("HTMX publish must carry the exact Published toast copy with no errors, got header %q", got)
	}
	if fail := h.postBuilder(t, path, invalid, true); fail.Code != http.StatusUnprocessableEntity || !strings.Contains(fail.Body.String(), `role="alert"`) || strings.Contains(fail.Body.String(), `data-workflow-live`) || fail.Header().Get("X-Save-Feedback") != "" || !strings.Contains(fail.Body.String(), `value="Keep me"`) {
		t.Errorf("failed publish = %d, want 422 with errors, preserved values, no success feedback: %s", fail.Code, fail.Body.String())
	}
	nativeSave := h.postBuilder(t, path, builderFieldForm("save", steps...), false)
	wantRedirect(t, nativeSave, http.StatusSeeOther, path)
	if body := h.getWithSaveFeedback(t, path, nativeSave); !strings.Contains(body, `data-feedback-message="Saved"`) {
		t.Errorf("GET after no-JS save must render the exact Saved flash toast, got: %s", body)
	}
	nativePublish := h.postBuilder(t, path, builderFieldForm("publish", steps...), false)
	wantRedirect(t, nativePublish, http.StatusSeeOther, path)
	if body := h.getWithSaveFeedback(t, path, nativePublish); !strings.Contains(body, `data-feedback-message="Published"`) {
		t.Errorf("GET after no-JS publish must render the exact Published flash toast, got: %s", body)
	}
	for _, suffix := range []string{"?status=bogus", ""} {
		if body := h.get(t, path+suffix, false).Body.String(); strings.Contains(body, `data-workflow-live`) || strings.Contains(body, `data-feedback-message="Saved"`) || strings.Contains(body, `data-feedback-message="Published"`) {
			t.Errorf("GET %q must not render a success status, got: %s", suffix, body)
		}
	}
	if rec := h.postBuilder(t, path, invalid, false); rec.Code != http.StatusUnprocessableEntity || strings.Contains(rec.Body.String(), `data-workflow-live`) {
		t.Errorf("failed no-JS publish = %d, want 422 without success feedback: %s", rec.Code, rec.Body.String())
	}
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
		wantRedirect(t, h.postBuilder(t, path, builderFieldForm("save", buildingSteps()...), false), http.StatusSeeOther, path)
		f := builderFieldForm("move_up", buildingSteps()...)
		f.Set("step_index", "1")
		wantRedirect(t, h.postBuilder(t, path, f, false), http.StatusSeeOther, path)
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
		wantRedirect(t, h.postBuilder(t, path, builderFieldForm("save", buildingSteps()...), false), http.StatusSeeOther, path)
		f := builderFieldForm("move_down", buildingSteps()...)
		f.Set("step_index", "0")
		wantRedirect(t, h.postBuilder(t, path, f, false), http.StatusSeeOther, path)
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
		wantRedirect(t, h.postBuilder(t, path, builderFieldForm("save", buildingSteps()...), false), http.StatusSeeOther, path)
		f := builderFieldForm("remove_step", buildingSteps()...)
		f.Set("step_index", "2")
		wantRedirect(t, h.postBuilder(t, path, f, false), http.StatusSeeOther, path)
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
		wantRedirect(t, h.postBuilder(t, path, builderFieldForm("save", buildingSteps()...), false), http.StatusSeeOther, path)
		f := builderFieldForm("change_type", buildingSteps()...)
		f.Set("step_index", "0")
		f.Set("step_0_type", "assign_to_desk")
		f.Set("step_0_desk", "7")
		f.Set("step_0_strategy", "least_loaded")
		wantRedirect(t, h.postBuilder(t, path, f, false), http.StatusSeeOther, path)
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
		wantRedirect(t, h.postBuilder(t, path, builderFieldForm("save", buildingSteps()...), false), http.StatusSeeOther, path)
		f := builderFieldForm("add_field", buildingSteps()...)
		f.Set("step_index", "1")
		wantRedirect(t, h.postBuilder(t, path, f, false), http.StatusSeeOther, path)
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
		wantRedirect(t, h.postBuilder(t, path, builderFieldForm("save", buildingSteps()...), false), http.StatusSeeOther, path)
		f := builderFieldForm("remove_field", buildingSteps()...)
		f.Set("step_index", "1")
		f.Set("field_index", "0")
		wantRedirect(t, h.postBuilder(t, path, f, false), http.StatusSeeOther, path)
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
		wantRedirect(t, h.postBuilder(t, path, builderFieldForm("add_step"), false), http.StatusSeeOther, path)
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
	full := h.postBuilder(t, path, f, false)
	wantRedirect(t, full, http.StatusSeeOther, path)

	// HTMX move_down uses the same query index and swaps the builder fragment.
	fd := builderFieldForm("move_down", buildingSteps()...)
	fd.Set("step_index", "0")
	hx := h.postBuilder(t, path, fd, true)
	if hx.Code != http.StatusOK {
		t.Fatalf("HTMX move status = %d, want 200", hx.Code)
	}
	body := hx.Body.String()
	for _, want := range []string{`id="workflow-builder"`, `name="action" value="move_down"`, `name="step_1_type"`, `class="workflow-step-card selected"`} {
		if !strings.Contains(body, want) {
			t.Errorf("HTMX builder response must contain %q, got: %s", want, body)
		}
	}
	if got := strings.Count(body, `class="workflow-editor-panel"`); got != 1 {
		t.Errorf("HTMX response must render exactly one editor, got %d", got)
	}
}

// Invalid and valid publish match the draft-JSON contract but are driven
// through the real visible controls.
func TestCategoryWorkflowBuilder_FieldBasedPublish(t *testing.T) {
	t.Run("invalid publish shows alerts and writes nothing", func(t *testing.T) {
		h := newHarness(t)
		category, err := h.categories.Create(t.Context(), "FieldInvalid")
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		path := "/categories/" + strconv.FormatInt(category.ID, 10) + "/workflow"
		invalid := []bstep{{typ: "manual_task", manual: ""}}
		rec := h.postBuilder(t, path, builderFieldForm("publish", invalid...), false)
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
		h.staff(t, desk.ID)
		valid := []bstep{
			{typ: "manual_task", manual: "do it"},
			{typ: "assign_to_desk", desk: strconv.FormatInt(desk.ID, 10), strategy: "claim"},
		}
		wantRedirect(t, h.postBuilder(t, path, builderFieldForm("publish", valid...), false), http.StatusSeeOther, path)
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
// and its 409 stale-draft refusal (issue #254) would never replace
// #workflow-builder in a real browser. The builder must carry a form-scoped
// hx-on::before-swap policy that swaps ONLY those two expected statuses into
// the swap target and marks them non-error, without weakening other 4xx/5xx
// handling.
func TestCategoryWorkflowBuilder_RED_HTMX422SwapsIntoBuilder(t *testing.T) {
	const policy = `hx-on::before-swap="if(event.detail.xhr.status === 422 || event.detail.xhr.status === 409){event.detail.shouldSwap = true; event.detail.isError = false}"`

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
		rec := h.postBuilder(t, path, builderForm("publish", builderDraft(t, "")), true)
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

// addOptionButton returns the markup of the one + Add step option that submits
// add_step_type=<stepType>, so a failing assertion shows the option's own tag
// instead of the whole builder body.
func addOptionButton(t *testing.T, body, stepType string) string {
	t.Helper()
	marker := "?add_step_type=" + stepType + `"`
	i := strings.Index(body, marker)
	if i < 0 {
		return "(marker not found)"
	}
	start := strings.LastIndex(body[:i], "<button")
	end := strings.Index(body[i:], "</button>")
	if start < 0 || end < 0 {
		return body[i:]
	}
	return body[start : i+end+len("</button>")]
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

// TestCategoryWorkflowBuilder_AddOptionDisabledConvention pins issue #251's
// disabled-state styling. The add-step option buttons carry no .btn class, so
// the repository's one disabled convention (.btn[disabled]{opacity:.5;
// cursor:not-allowed}) never reached them and they fell back to the browser's
// own greyed look. CSS has no other automated guard here, so the rule is
// pinned literally; the enabled rule must stay byte-for-byte untouched so the
// enabled options do not change.
func TestCategoryWorkflowBuilder_AddOptionDisabledConvention(t *testing.T) {
	body := renderGolden(t, "category_workflow", "", mobileBuilderPageData(), false)
	style := extractStyleBlock(t, body)
	const disabledRule = `.workflow-add-options button[disabled]{opacity:.5;cursor:not-allowed}`
	if !strings.Contains(style, disabledRule) {
		t.Errorf("add-step options must reuse the repository's disabled convention, missing %q", disabledRule)
	}
	const enabledRule = `.workflow-add-options button{justify-content:flex-start;width:100%}`
	if !strings.Contains(style, enabledRule) {
		t.Errorf("the enabled add-step option rule must stay unchanged, missing %q", enabledRule)
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
			wantRedirect(t, h.postBuilder(t, path, f, false), http.StatusSeeOther, path)
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
	wantRedirect(t, h.postBuilder(t, path, builderFieldForm("save", bstep{typ: "manual_task", manual: "seed"}), false), http.StatusSeeOther, path)
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
		{"manual_task", "Give the agent a task"},
		{"assign_to_desk", "Send to a desk"},
		{"form", "Ask for information"},
		{"resolve_ticket", "Mark the ticket resolved"},
		{"close_ticket", "Close the ticket"},
	} {
		if !strings.Contains(body, "?add_step_type="+tc.typ+"\"") || !strings.Contains(body, ">"+tc.label+"</button>") {
			t.Errorf("popover must offer %q carrying add_step_type=%s, got: %s", tc.label, tc.typ, body)
		}
	}
	if !strings.Contains(body, `type="submit" name="action" value="add_step" formaction="`+path+`?add_step_type=`) || !strings.Contains(body, `hx-include="closest form"`) || !strings.Contains(body, `hx-swap="outerHTML show:none"`) || !strings.Contains(body, `hx-push-url="false"`) {
		t.Errorf("popover choices must work as no-JS submits and HTMX outerHTML swaps without history push, got: %s", body)
	}
	// No terminal yet: the two terminal kinds stay enabled, carrying no disabled
	// attribute and no "can't add" reason. This is the working case #251 must
	// not break.
	for _, typ := range []string{"resolve_ticket", "close_ticket"} {
		btn := addOptionButton(t, body, typ)
		if strings.Contains(btn, "disabled") {
			t.Errorf("no-terminal draft must leave %s enabled, got: %s", typ, btn)
		}
		if strings.Contains(btn, "Can't add") {
			t.Errorf("no-terminal draft must not state an add refusal for %s, got: %s", typ, btn)
		}
	}
}

// WorkflowKindVocabulary freezes the builder's plain-language kind copy:
// every kind the picker offers has one label and one explanation, and the
// terminal kinds carry their own sentence instead of sharing a fallback.
// The label is used by the picker, the step card, and the editor heading; the
// explanation only by the editor panel.
func TestWorkflowKindVocabulary(t *testing.T) {
	for _, tc := range []struct {
		typ         domain.StepType
		label       string
		explanation string
	}{
		{domain.StepAssignToDesk, "Send to a desk", "Send the ticket to a desk."},
		{domain.StepForm, "Ask for information", "Ask the requester or the agent for information."},
		{domain.StepManualTask, "Give the agent a task", "The agent follows your instructions to complete the work."},
		{domain.StepResolve, "Mark the ticket resolved", "Mark the ticket resolved automatically."},
		{domain.StepClose, "Close the ticket", "Close the ticket automatically."},
	} {
		if got := workflowTypeLabel(tc.typ); got != tc.label {
			t.Errorf("workflowTypeLabel(%s) = %q, want %q", tc.typ, got, tc.label)
		}
		if got := workflowTypeHelp(tc.typ); got != tc.explanation {
			t.Errorf("workflowTypeHelp(%s) = %q, want %q", tc.typ, got, tc.explanation)
		}
	}
}

// TypedAddTerminalProtection freezes scenario protection: a draft that already
// contains a terminal step keeps the two terminal kinds visible but disabled,
// each carrying the reason, and offers no insertion position after the final
// step, and crafted add requests leave the draft unchanged; the terminal keeps
// its Final badge and removal guard.
func TestCategoryWorkflowBuilder_TypedAddTerminalProtection(t *testing.T) {
	h := newHarness(t)
	category, err := h.categories.Create(t.Context(), "Terminal guard")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	path := "/categories/" + strconv.FormatInt(category.ID, 10) + "/workflow"
	steps := []bstep{{typ: "manual_task", manual: "first"}, {typ: "close_ticket"}}
	wantRedirect(t, h.postBuilder(t, path, builderFieldForm("save", steps...), false), http.StatusSeeOther, path)
	body := h.get(t, path, false).Body.String()
	if !strings.Contains(body, `class="workflow-add-popover"`) {
		t.Errorf("final draft must still offer the Add step popover, got: %s", body)
	}
	// #251: the terminal kinds must stay visible and disabled with the reason,
	// instead of silently vanishing from the menu. Reuse the picker's
	// disabled-with-reason shape (cannotRunLabel): a disabled control whose own
	// copy states why it cannot be used.
	for _, tc := range []struct {
		typ   string
		label string
	}{
		{"resolve_ticket", "Mark the ticket resolved"},
		{"close_ticket", "Close the ticket"},
	} {
		btn := addOptionButton(t, body, tc.typ)
		if btn == "(marker not found)" {
			t.Errorf("final draft must keep the %s option visible, body=%s", tc.label, body)
			continue
		}
		if !strings.Contains(btn, " disabled") {
			t.Errorf("final draft must disable the %s option, got: %s", tc.label, btn)
		}
		if !strings.Contains(btn, "Can't add · this workflow already has an ending") {
			t.Errorf("final draft must state why %s cannot be added, got: %s", tc.label, btn)
		}
	}
	// Only the terminal kinds are blocked: the three non-terminal kinds stay
	// enabled and usable.
	for _, typ := range []string{"manual_task", "assign_to_desk", "form"} {
		if btn := addOptionButton(t, body, typ); strings.Contains(btn, "disabled") {
			t.Errorf("final draft must leave %s enabled, got: %s", typ, btn)
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
			wantRedirect(t, h.postBuilder(t, path, f, false), http.StatusSeeOther, path)
			def := h.persistedDefinition(t, path)
			if len(def) != 3 || def[2].Type != domain.StepClose || def[1].Type != tc.want {
				t.Errorf("%s: insert must land directly before the final step, got %+v", tc.name, def)
			}
			// Reset to the two-step baseline for the next case.
			wantRedirect(t, h.postBuilder(t, path, builderFieldForm("save", steps...), false), http.StatusSeeOther, path)
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
	wantRedirect(t, h.postBuilder(t, path, builderFieldForm("save", steps...), false), http.StatusSeeOther, path)
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
	wantRedirect(t, h.postBuilder(t, path, builderFieldForm("save", buildingSteps()...), false), http.StatusSeeOther, path)
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
	for _, want := range []string{"Step 3 · Close the ticket", "Runs automatically and must remain final."} {
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
	wantRedirect(t, h.postBuilder(t, path, builderFieldForm("save", steps...), false), http.StatusSeeOther, path)
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
	wantRedirect(t, h.postBuilder(t, tpath, builderFieldForm("save", []bstep{{typ: "close_ticket"}}...), false), http.StatusSeeOther, tpath)
	tbody := h.get(t, tpath, false).Body.String()
	if got := strings.Count(tbody, `class="workflow-step-menu"`); got != 0 {
		t.Errorf("terminal-only draft must render no step menu, got %d", got)
	}
	if got := strings.Count(tbody, `class="workflow-trigger"`); got != 0 {
		t.Errorf("terminal-only draft must render no trigger, got %d", got)
	}
	// This asset has no browser test for its keyboard contract, so this pin is
	// its only automated guard. It must describe the contract that now exists:
	// the three dropdowns (step menu, add popover, field menu) share one
	// selector and one close routine, and Escape closes the open dropdown and
	// refocuses its trigger. A literal pin that forbids the correct
	// implementation is a lock, not a guard, so it is repointed at the shared
	// contract instead of the old two-menu matcher.
	asset := h.get(t, "/static/workflow.js", false)
	if asset.Code != http.StatusOK {
		t.Fatalf("workflow asset status = %d, want 200", asset.Code)
	}
	for _, want := range []string{
		`event.key !== "Escape"`,
		`const DROPDOWN_SELECTOR = ".workflow-step-menu, .workflow-add-popover, .workflow-field-menu"`,
		`const closeDropdown = (details, { restoreFocus }) =>`,
		`details.open = false`,
		`details.querySelector("summary")?.focus()`,
	} {
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
	wantRedirect(t, h.postBuilder(t, path, builderFieldForm("save", steps...), false), http.StatusSeeOther, path)
	body := h.get(t, path+"?selected_step_index=1", false).Body.String()
	for _, want := range []string{`name="step_1_type"`, `value="resolve_ticket"`, `value="close_ticket"`, `>Close the ticket</option>`} {
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
	wantRedirect(t, h.postBuilder(t, path, f, false), http.StatusSeeOther, path)
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
		wantRedirect(t, h.postBuilder(t, path, builderFieldForm("save", steps...), false), http.StatusSeeOther, path)
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
		wantRedirect(t, h.postBuilder(t, path, builderFieldForm("save", steps...), false), http.StatusSeeOther, path)
		f := builderFieldForm("remove_step", steps...)
		f.Set("step_index", "1")
		f.Set("selected_step_index", "1")
		rec := h.postBuilder(t, path, f, true)
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
		wantRedirect(t, h.postBuilder(t, path, builderFieldForm("save", bstep{typ: "manual_task", manual: "only"}), false), http.StatusSeeOther, path)
		f := builderFieldForm("remove_step", bstep{typ: "manual_task", manual: "only"})
		f.Set("step_index", "0")
		f.Set("selected_step_index", "0")
		body := h.postBuilder(t, path, f, true).Body.String()
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
		wantRedirect(t, h.postBuilder(t, path, builderFieldForm("save", steps...), false), http.StatusSeeOther, path)
		f := builderFieldForm("reorder", steps...)
		f.Set("source_index", "0")
		f.Set("target_index", "2")
		f.Set("selected_step_index", "1")
		rec := h.postBuilder(t, path, f, true)
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
		wantRedirect(t, h.postBuilder(t, path, builderFieldForm("save", steps...), false), http.StatusSeeOther, path)
		f := builderFieldForm("reorder", steps...)
		f.Set("source_index", "1")
		f.Set("target_index", "0")
		wantRedirect(t, h.postBuilder(t, path, f, false), http.StatusSeeOther, path)
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
				wantRedirect(t, h.postBuilder(t, path, builderFieldForm("save", steps...), false), http.StatusSeeOther, path)
				before := h.persistedDefinition(t, path)
				f := builderFieldForm("reorder", steps...)
				f.Set("source_index", tc.source)
				f.Set("target_index", tc.target)
				rec := h.postBuilder(t, path, f, true)
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
		wantRedirect(t, h.postBuilder(t, path, builderFieldForm("save", steps...), false), http.StatusSeeOther, path)
		before := h.persistedDefinition(t, path)
		f := builderFieldForm("reorder", steps...)
		f.Set("source_index", "2")
		f.Set("target_index", "0")
		if rec := h.postBuilder(t, path, f, true); rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422", rec.Code)
		}
		if after := h.persistedDefinition(t, path); !reflect.DeepEqual(after, before) {
			t.Errorf("source-after-terminal reorder mutated the persisted draft: before=%+v after=%+v", before, after)
		}
	})
}

// DragMarkup freezes the horizontal drag UI: every non-terminal card exposes
// a draggable grip hidden from the accessibility tree, the final terminal card
// exposes no grip, and the builder carries exactly one permanent source_index/
// target_index pair plus the reorder submitter, wired only into this page. The
// grip is pointer-only; the labeled Move left/Move right step-menu actions are
// the accessible reorder path, so the grip must not advertise itself with an
// aria-label that promises a keyboard affordance it cannot provide.
func TestCategoryWorkflowBuilder_DragMarkup(t *testing.T) {
	h := newHarness(t)
	category, err := h.categories.Create(t.Context(), "Drag markup")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	path := "/categories/" + strconv.FormatInt(category.ID, 10) + "/workflow"
	wantRedirect(t, h.postBuilder(t, path, builderFieldForm("save", buildingSteps()...), false), http.StatusSeeOther, path)
	body := h.get(t, path, false).Body.String()
	const gripMarkup = `class="workflow-drag-handle" draggable="true" aria-hidden="true"`
	if got := strings.Count(body, gripMarkup); got != 2 {
		t.Errorf("each non-terminal card must expose one hidden draggable grip, got %d: %s", got, body)
	}
	if strings.Contains(body, `aria-label="Drag step`) {
		t.Errorf("the drag grip must not promise a keyboard affordance; reorder lives in the step menu, got: %s", body)
	}
	if strings.Contains(body, `class="workflow-drag-handle" draggable="true" aria-hidden="true" aria-label`) {
		t.Errorf("the drag grip must not carry an aria-label, got: %s", body)
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

// BusySignalling freezes the builder's loading contract: the form is its own
// htmx indicator so the in-form spinner shows for every builder request, and
// the form carries aria-busy for the duration of each request. htmx's indicator
// class is not exposed to assistive tech, so aria-busy is toggled inline on the
// form itself and never left set after the request settles.
func TestCategoryWorkflowBuilder_BusySignalling(t *testing.T) {
	h := newHarness(t)
	category, err := h.categories.Create(t.Context(), "Busy signalling")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	path := "/categories/" + strconv.FormatInt(category.ID, 10) + "/workflow"
	wantRedirect(t, h.postBuilder(t, path, builderFieldForm("save", buildingSteps()...), false), http.StatusSeeOther, path)
	body := h.get(t, path, false).Body.String()
	for _, want := range []string{
		`hx-indicator="#workflow-form"`,
		`hx-on::before-request="this.setAttribute('aria-busy','true')"`,
		`hx-on::after-request="this.removeAttribute('aria-busy')"`,
		`class="htmx-indicator spinner" aria-hidden="true"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("builder form must expose %q, got: %s", want, body)
		}
	}
	if strings.Contains(body, `aria-busy="true"`) {
		t.Errorf("a settled server render must not carry a stale aria-busy: %s", body)
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
	// The grip inherits the document's vendored Inter family instead of a bare
	// generic sans-serif, which bypassed the embedded face entirely.
	gripRule := cssRuleForSelectors(style, ".workflow-drag-handle")
	if !strings.Contains(gripRule, "font-family:inherit") {
		t.Errorf("grip must inherit the vendored family, got: %s", gripRule)
	}
	if strings.Contains(gripRule, "sans-serif") {
		t.Errorf("grip must not declare a non-vendored generic family, got: %s", gripRule)
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
	wantRedirect(t, h.postBuilder(t, path, builderFieldForm("save", steps...), false), http.StatusSeeOther, path)
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
	wantRedirect(t, h.postBuilder(t, path, changed, false), http.StatusSeeOther, path)
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

// getWithSaveFeedback replays a GET carrying the feedback flash cookie issued
// by a prior native (no-JS) mutation response, so the server-rendered toast
// can be asserted exactly as a real browser follow-up request receives it.
func (h *harness) getWithSaveFeedback(t *testing.T, path string, prior *httptest.ResponseRecorder) string {
	t.Helper()
	value := ""
	for _, c := range prior.Result().Cookies() {
		if c.Name == saveFeedbackCookie {
			value = c.Value
		}
	}
	if value == "" {
		t.Fatalf("prior response issued no %s cookie", saveFeedbackCookie)
	}
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Cookie", sessionCookie+"="+h.adminSession.ID+"; "+saveFeedbackCookie+"="+value)
	rec := httptest.NewRecorder()
	h.mw.Wrap(h.mux).ServeHTTP(rec, req)
	return rec.Body.String()
}

// ==== Reuse a workflow by cloning it into another category (issue #257) ====
//
// The route copies the SOURCE category's PUBLISHED workflow into the TARGET
// category's DRAFT, authorized by the existing category-management capability
// (CapWorkflowAuthor belongs to issue #255 and is deliberately not invented
// here). When the target already has a draft the clone is REFUSED with a
// comprehensible message and the target's bytes are left EXACTLY as they were
// — no merge, no overwrite — and cloning never publishes.
func TestCategoryWorkflowClone_RouteAuthorizationRefusalAndRoundTrip(t *testing.T) {
	h := newHarness(t)
	source, err := h.categories.Create(t.Context(), "Clone source")
	if err != nil {
		t.Fatalf("create source: %v", err)
	}
	sourceDef := domain.WorkflowDefinition{
		{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "Reuse me"}},
		{Type: domain.StepResolve},
	}
	h.publishWorkflow(t, source.ID, sourceDef)
	target, err := h.categories.Create(t.Context(), "Clone target")
	if err != nil {
		t.Fatalf("create target: %v", err)
	}
	path := "/categories/" + strconv.FormatInt(target.ID, 10) + "/workflow/clone"
	targetPath := "/categories/" + strconv.FormatInt(target.ID, 10) + "/workflow"
	form := url.Values{"source_category_id": {strconv.FormatInt(source.ID, 10)}}

	// Authorization: an agent may not clone.
	agent := h.createUser(t, "Clone Agent", "clone-agent@tkt.test", "secret")
	agentSession := seedSession(t, h.store, agent.ID)
	denied := h.postFormAs(t, path, form, agentSession.ID)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("agent clone = %d, want 403: %s", denied.Code, denied.Body.String())
	}
	if n := scanOneInt(t, h.rawDB(t), "SELECT COUNT(*) FROM category_workflows WHERE category_id=?", target.ID); n != 0 {
		t.Fatalf("denied clone created %d workflow rows", n)
	}

	// Success: the source's PUBLISHED definition becomes the target's DRAFT.
	ok := h.postBuilder(t, path, form, false)
	wantRedirect(t, ok, http.StatusSeeOther, targetPath)
	targetDraft := h.persistedDefinition(t, targetPath)
	if len(targetDraft) != len(sourceDef) || targetDraft[0].ManualTask.Instructions != "Reuse me" || targetDraft[1].Type != domain.StepResolve {
		t.Fatalf("cloned target draft = %+v, want %+v", targetDraft, sourceDef)
	}
	// Cloning is not publishing: the target keeps no current version.
	if current, present := scanOneNullableInt(t, h.rawDB(t), "SELECT current_version_id FROM category_workflows WHERE category_id=?", target.ID); present && current != 0 {
		t.Fatalf("clone must not publish, target current_version_id=%d", current)
	}
	// The source's published version is untouched by a clone.
	if sourceCurrent, present := scanOneNullableInt(t, h.rawDB(t), "SELECT current_version_id FROM category_workflows WHERE category_id=?", source.ID); !present || sourceCurrent == 0 {
		t.Fatalf("clone disturbed the source's published version: %d present=%v", sourceCurrent, present)
	}

	// Refusal: the target now has a draft; a second clone is refused with a
	// message and leaves the target's bytes byte-identical.
	before := scanOneString(t, h.rawDB(t), "SELECT draft_json FROM category_workflows WHERE category_id=?", target.ID)
	refused := h.postBuilder(t, path, form, false)
	if refused.Code != http.StatusUnprocessableEntity {
		t.Fatalf("second clone = %d, want 422: %s", refused.Code, refused.Body.String())
	}
	if !strings.Contains(refused.Body.String(), "already has a draft") {
		t.Fatalf("refusal must state the existing draft, got: %s", refused.Body.String())
	}
	after := scanOneString(t, h.rawDB(t), "SELECT draft_json FROM category_workflows WHERE category_id=?", target.ID)
	if after != before {
		t.Fatalf("refused clone changed the target draft:\n got  %s\n want %s", after, before)
	}
}

// The builder page offers the clone control with the published categories as
// sources, and never offers the page's own category as a clone source.
func TestCategoryWorkflowClone_ControlRendersPublishedSources(t *testing.T) {
	h := newHarness(t)
	source, err := h.categories.Create(t.Context(), "Reusable source")
	if err != nil {
		t.Fatalf("create source: %v", err)
	}
	h.publishWorkflow(t, source.ID, simpleManualDef())
	draftOnly, err := h.categories.Create(t.Context(), "Draft only source")
	if err != nil {
		t.Fatalf("create draft-only: %v", err)
	}
	if err := h.workflows.SaveDraft(t.Context(), *h.admin, draftOnly.ID, simpleManualDef()); err != nil {
		t.Fatalf("save draft-only: %v", err)
	}
	target, err := h.categories.Create(t.Context(), "Clone target")
	if err != nil {
		t.Fatalf("create target: %v", err)
	}

	body := h.get(t, "/categories/"+strconv.FormatInt(target.ID, 10)+"/workflow", false).Body.String()
	for _, want := range []string{
		`action="/categories/` + strconv.FormatInt(target.ID, 10) + `/workflow/clone"`,
		`name="source_category_id"`,
		`<option value="` + strconv.FormatInt(source.ID, 10) + `">Reusable source</option>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("clone control must contain %q, got: %s", want, body)
		}
	}
	if strings.Contains(body, `>Draft only source</option>`) {
		t.Error("a category without a published workflow must not be offered as a clone source")
	}
	if strings.Contains(body, `>`+target.Name+`</option>`) {
		t.Error("the page's own category must not be offered as a clone source")
	}
}

// TestCategoryWorkflowBuilder_StaleDraftRevisionIsRefused is the HTTP-layer
// falsification test for issue #254. Two tabs loaded the same category at the
// same revision; tab A saves, and tab B — still carrying the older revision —
// must be refused with 409 instead of silently overwriting tab A's work. The
// refusal re-renders the CURRENT draft and revision with a message that says
// what happened.
func TestCategoryWorkflowBuilder_StaleDraftRevisionIsRefused(t *testing.T) {
	h := newHarness(t)
	category, err := h.categories.Create(t.Context(), "Optimistic lock")
	if err != nil {
		t.Fatalf("create category: %v", err)
	}
	path := "/categories/" + strconv.FormatInt(category.ID, 10) + "/workflow"

	// The builder carries the revision the browser must send back.
	body := h.get(t, path, false).Body.String()
	if !strings.Contains(body, `<input type="hidden" name="draft_revision" value="0">`) {
		t.Fatalf("builder must render the draft revision hidden input, got: %s", body)
	}

	// Tab A saves at revision 0 and wins.
	winner := builderFieldForm("save", bstep{typ: "manual_task", manual: "winner"})
	winner.Set("draft_revision", "0")
	wantRedirect(t, h.postFormVerbatim(t, path, winner, false), http.StatusSeeOther, path)

	// Tab B still carries revision 0: the stale write is refused, and the
	// response is the fragment HTMX swaps in (status 409 marked non-error).
	stale := builderFieldForm("save", bstep{typ: "manual_task", manual: "stale tab"})
	stale.Set("draft_revision", "0")
	rec := h.postFormVerbatim(t, path, stale, true)
	if rec.Code != http.StatusConflict {
		t.Fatalf("stale write status = %d, want 409: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), draftConflictMessage) {
		t.Errorf("refusal must say the draft changed, got: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `<input type="hidden" name="draft_revision" value="1">`) {
		t.Errorf("refusal must re-render the current revision 1, got: %s", rec.Body.String())
	}

	// The winner's bytes survive untouched.
	def := h.persistedDefinition(t, path)
	if len(def) != 1 || def[0].ManualTask == nil || def[0].ManualTask.Instructions != "winner" {
		t.Fatalf("stored draft = %+v, want the winner's single 'winner' step", def)
	}
}

// The positive HTTP path: the revision the page rendered is accepted, the
// write lands, and the response carries the advanced revision.
func TestCategoryWorkflowBuilder_GuardedSaveAdvancesRevision(t *testing.T) {
	h := newHarness(t)
	category, err := h.categories.Create(t.Context(), "Revision advance")
	if err != nil {
		t.Fatalf("create category: %v", err)
	}
	path := "/categories/" + strconv.FormatInt(category.ID, 10) + "/workflow"

	first := builderFieldForm("save", bstep{typ: "manual_task", manual: "one"})
	first.Set("draft_revision", "0")
	wantRedirect(t, h.postFormVerbatim(t, path, first, false), http.StatusSeeOther, path)
	if body := h.get(t, path, false).Body.String(); !strings.Contains(body, `<input type="hidden" name="draft_revision" value="1">`) {
		t.Fatalf("after one guarded write the builder must render revision 1, got: %s", body)
	}

	second := builderFieldForm("save", bstep{typ: "manual_task", manual: "two"})
	second.Set("draft_revision", "1")
	rec := h.postFormVerbatim(t, path, second, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("guarded save at the correct revision = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `<input type="hidden" name="draft_revision" value="2">`) {
		t.Errorf("successful save must re-render revision 2, got: %s", rec.Body.String())
	}
	def := h.persistedDefinition(t, path)
	if len(def) != 1 || def[0].ManualTask == nil || def[0].ManualTask.Instructions != "two" {
		t.Fatalf("stored draft = %+v, want 'two'", def)
	}
}

// A malformed revision is an unusable expectation, not a stale one: it is
// refused on the same fail-closed path as a missing revision, with zero writes.
func TestCategoryWorkflowBuilder_MalformedDraftRevisionIsRejected(t *testing.T) {
	h := newHarness(t)
	category, err := h.categories.Create(t.Context(), "Malformed revision")
	if err != nil {
		t.Fatalf("create category: %v", err)
	}
	path := "/categories/" + strconv.FormatInt(category.ID, 10) + "/workflow"

	form := builderFieldForm("save", bstep{typ: "manual_task", manual: "nope"})
	form.Set("draft_revision", "not-a-number")
	rec := h.postFormVerbatim(t, path, form, false)
	if rec.Code != http.StatusConflict {
		t.Fatalf("malformed revision status = %d, want 409: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), draftConflictMessage) {
		t.Errorf("malformed-revision refusal must say what happened, got: %s", rec.Body.String())
	}
	var n int
	if err := h.rawDB(t).QueryRow("SELECT COUNT(*) FROM category_workflows WHERE category_id=?", category.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("a rejected malformed revision wrote %d rows, want 0", n)
	}
}

// TestCategoryWorkflowBuilder_MissingDraftRevisionIsRefused closes the bypass
// the optimistic lock would otherwise have (issue #254 follow-up): a submission
// that carries no draft_revision at all cannot be applied, because the server
// cannot tell a fresh intent from a stale tab. It fails closed with zero writes,
// on the same refusal path as a stale revision. The builder always renders the
// field, so this is only reachable by a crafted request.
func TestCategoryWorkflowBuilder_MissingDraftRevisionIsRefused(t *testing.T) {
	h := newHarness(t)
	category, err := h.categories.Create(t.Context(), "Missing revision")
	if err != nil {
		t.Fatalf("create category: %v", err)
	}
	path := "/categories/" + strconv.FormatInt(category.ID, 10) + "/workflow"

	seed := builderFieldForm("save", bstep{typ: "manual_task", manual: "original"})
	seed.Set("draft_revision", "0")
	wantRedirect(t, h.postFormVerbatim(t, path, seed, false), http.StatusSeeOther, path)

	// The same mutation WITHOUT the revision field must not touch the draft.
	form := builderFieldForm("save", bstep{typ: "manual_task", manual: "bypass"})
	form.Del("draft_revision")
	rec := h.postFormVerbatim(t, path, form, true)
	if rec.Code != http.StatusConflict {
		t.Fatalf("missing revision status = %d, want 409: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), draftConflictMessage) {
		t.Errorf("missing-revision refusal must say what happened, got: %s", rec.Body.String())
	}
	def := h.persistedDefinition(t, path)
	if len(def) != 1 || def[0].ManualTask == nil || def[0].ManualTask.Instructions != "original" {
		t.Fatalf("stored draft = %+v, want the untouched 'original' step", def)
	}
}

// TestCategoryWorkflowBuilder_StalePublishRefusedAndNewerDraftSurvives is the
// publish-side falsification test for issue #254. Tab A saves a newer draft;
// tab B, still holding the earlier revision, publishes older bytes. The publish
// must be refused with 409, must create NO version, and the newer draft must
// survive byte for byte.
func TestCategoryWorkflowBuilder_StalePublishRefusedAndNewerDraftSurvives(t *testing.T) {
	h := newHarness(t)
	category, err := h.categories.Create(t.Context(), "Stale publish")
	if err != nil {
		t.Fatalf("create category: %v", err)
	}
	path := "/categories/" + strconv.FormatInt(category.ID, 10) + "/workflow"

	// Tab A saves the newer draft at revision 0; the store moves to revision 1.
	newer := builderFieldForm("save", bstep{typ: "manual_task", manual: "newer"})
	newer.Set("draft_revision", "0")
	wantRedirect(t, h.postFormVerbatim(t, path, newer, false), http.StatusSeeOther, path)
	newerBytes := scanOneString(t, h.rawDB(t), "SELECT draft_json FROM category_workflows WHERE category_id=?", category.ID)

	// Tab B still carries revision 0 and publishes its older bytes.
	stale := builderFieldForm("publish", bstep{typ: "manual_task", manual: "stale tab"})
	stale.Set("draft_revision", "0")
	rec := h.postFormVerbatim(t, path, stale, true)
	if rec.Code != http.StatusConflict {
		t.Fatalf("stale publish status = %d, want 409: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), draftConflictMessage) {
		t.Errorf("stale publish refusal must say what happened, got: %s", rec.Body.String())
	}
	if got := scanOneString(t, h.rawDB(t), "SELECT draft_json FROM category_workflows WHERE category_id=?", category.ID); got != newerBytes {
		t.Fatalf("stale publish overwrote the draft:\n got  %s\n want %s", got, newerBytes)
	}
	if n := scanOneString(t, h.rawDB(t), "SELECT COUNT(*) FROM workflow_versions WHERE category_id=?", category.ID); n != "0" {
		t.Fatalf("a refused publish created %s version rows, want 0", n)
	}
}

// A publish at the correct revision succeeds, advances the revision, and a
// later save carrying the pre-publish revision is refused.
func TestCategoryWorkflowBuilder_PublishAdvancesRevision(t *testing.T) {
	h := newHarness(t)
	category, err := h.categories.Create(t.Context(), "Publish advance")
	if err != nil {
		t.Fatalf("create category: %v", err)
	}
	path := "/categories/" + strconv.FormatInt(category.ID, 10) + "/workflow"

	// Save at revision 0 -> stored revision 1.
	save := builderFieldForm("save", bstep{typ: "manual_task", manual: "one"})
	save.Set("draft_revision", "0")
	wantRedirect(t, h.postFormVerbatim(t, path, save, false), http.StatusSeeOther, path)

	// Publish at revision 1 -> success, stored revision 2, exactly one version.
	publish := builderFieldForm("publish", bstep{typ: "manual_task", manual: "one"})
	publish.Set("draft_revision", "1")
	rec := h.postFormVerbatim(t, path, publish, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("publish at the correct revision = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `<input type="hidden" name="draft_revision" value="2">`) {
		t.Errorf("publish must re-render revision 2, got: %s", rec.Body.String())
	}
	if n := scanOneString(t, h.rawDB(t), "SELECT COUNT(*) FROM workflow_versions WHERE category_id=?", category.ID); n != "1" {
		t.Fatalf("publish created %s versions, want 1", n)
	}

	// A save carrying the pre-publish revision 1 is refused; the published
	// bytes survive.
	publishedBytes := scanOneString(t, h.rawDB(t), "SELECT draft_json FROM category_workflows WHERE category_id=?", category.ID)
	staleSave := builderFieldForm("save", bstep{typ: "manual_task", manual: "two"})
	staleSave.Set("draft_revision", "1")
	if rec := h.postFormVerbatim(t, path, staleSave, true); rec.Code != http.StatusConflict {
		t.Fatalf("post-publish save at the old revision = %d, want 409", rec.Code)
	}
	if got := scanOneString(t, h.rawDB(t), "SELECT draft_json FROM category_workflows WHERE category_id=?", category.ID); got != publishedBytes {
		t.Fatalf("post-publish stale save changed the published draft:\n got  %s\n want %s", got, publishedBytes)
	}
}

// TestCategoryWorkflowBuilder_SurfacesStoredVersionFacts is the issue #253
// end-to-end surfacing contract: the builder names who last changed the draft
// and, once published, which version is live, who published it and when. The
// version columns have existed since migration 0006; before this change no
// template or handler read them. The facts render separately from the transient
// success live region, so a conflict or validation page can still name the
// author without faking a success status.
func TestCategoryWorkflowBuilder_SurfacesStoredVersionFacts(t *testing.T) {
	h := newHarness(t)
	category, err := h.categories.Create(t.Context(), "Attributed")
	if err != nil {
		t.Fatalf("create category: %v", err)
	}
	path := "/categories/" + strconv.FormatInt(category.ID, 10) + "/workflow"

	fresh := h.get(t, path, false)
	if fresh.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want 200", fresh.Code)
	}
	for _, absent := range []string{"data-workflow-version", "data-workflow-draft-author"} {
		if strings.Contains(fresh.Body.String(), absent) {
			t.Errorf("a fresh builder must render no stored facts, found %q: %s", absent, fresh.Body.String())
		}
	}

	draft := builderDraft(t, "first", "second")
	wantRedirect(t, h.postBuilder(t, path, builderForm("save", draft), false), http.StatusSeeOther, path)
	saved := h.get(t, path, false).Body.String()
	if !strings.Contains(saved, "data-workflow-draft-author") || !strings.Contains(saved, "Draft last edited by Admin") {
		t.Errorf("saved builder must name the draft editor, got: %s", saved)
	}
	// A draft-only category has no live version to name yet.
	if strings.Contains(saved, "data-workflow-version") {
		t.Errorf("a draft-only builder must not render live version facts, got: %s", saved)
	}

	wantRedirect(t, h.postBuilder(t, path, builderForm("publish", draft), false), http.StatusSeeOther, path)
	published := h.get(t, path, false).Body.String()
	if !strings.Contains(published, "data-workflow-version") || !strings.Contains(published, "Published v1 · by Admin") {
		t.Errorf("published builder must surface the live version and its publisher, got: %s", published)
	}

	index := h.get(t, "/categories", false)
	if got := categoryStatusBadge(t, index.Body.String(), category.Name); got != "Published v1 · by Admin" {
		t.Errorf("category row = %q, want the live version attributed to its publisher", got)
	}
}
