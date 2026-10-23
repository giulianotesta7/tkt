package httpadapter

import (
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// The approved redesign (issue #395, option A) turns the workflow builder into
// a lateral step sequence beside the selected step's editor. Go cannot measure
// layout, so these tests
// freeze the contract the browser needs: the rendered structure, the class
// names the stylesheet scopes, and the copy every control reads.

// lateralBuilderPageData is the lateral-layout fixture: a published workflow
// with a draft, four steps where the selected middle step can move both ways,
// and two clone sources. Steps come from the SAME projection the handler uses,
// so the snapshot cannot drift from the real view model.
func lateralBuilderPageData() workflowBuilderData {
	ana := domain.User{ID: 1, Name: "Ana Torres", Email: "ana@example.com", Active: true, Role: domain.RoleAdmin, CreatedAt: goldenT0}
	desks := []domain.Desk{{ID: 1, Name: "Support", CreatedAt: goldenT0}}
	draft := domain.WorkflowDefinition{
		{Type: domain.StepAssignToDesk, AssignToDesk: &domain.AssignToDeskStep{DeskID: 1, Strategy: domain.StrategyLeastLoaded}},
		{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "Verify connectivity"}},
		{Type: domain.StepForm, Form: &domain.FormStep{Actor: domain.FormActorRequester, Fields: []domain.FormField{{Key: "serial", Label: "Serial", Kind: domain.FieldShortText}}}},
		{Type: domain.StepResolve},
	}
	return workflowBuilderData{
		pageData: pageData{
			NavActive:            "categories",
			CurrentUser:          ana,
			CanManageCategories:  true,
			PageFoundationAssets: true,
			WorkflowAssets:       true,
		},
		CategoryID:           1,
		CategoryName:         "VPN access",
		Draft:                draft,
		Steps:                workflowStepViews(draft, 1, desks),
		Desks:                desks,
		SelectedStepIndex:    1,
		SelectedStepPosition: 2,
		HasSelection:         true,
		HasFinal:             true,
		DraftRevision:        7,
		CloneSources:         []domain.Category{{ID: 2, Name: "Printer support", CreatedAt: goldenT0}, {ID: 3, Name: "Account access", CreatedAt: goldenT0}},
		LiveVersion:          3,
		PublishedByName:      "Ana Torres",
		DraftUpdatedByName:   "Marco Ruiz",
	}
}

// TestWorkflowBuilderLateralLayout pins the approved A composition: the step
// sequence (with its own count and the Add step control) sits BEFORE the
// selected step's editor, both are siblings in one two-pane block, and the
// block is still inside the builder form so every existing field, snapshot and
// action submits exactly as before.
func TestWorkflowBuilderLateralLayout(t *testing.T) {
	body := renderGolden(t, "category_workflow", "", lateralBuilderPageData(), false)

	formStart := strings.Index(body, `id="workflow-form"`)
	if formStart < 0 {
		t.Fatalf("builder form missing: %.400s", body)
	}
	formEnd := strings.Index(body[formStart:], "</form>")
	if formEnd < 0 {
		t.Fatalf("builder form is not closed: %.400s", body)
	}
	formEnd += formStart

	panels := strings.Index(body, `class="workflow-builder-panels"`)
	pane := strings.Index(body, `class="workflow-rail-wrap"`)
	editor := strings.Index(body, `<section class="workflow-editor-panel"`)
	switch {
	case panels < 0:
		t.Fatalf("builder must render the two-pane block, got: %.600s", body)
	case pane < 0 || editor < 0:
		t.Fatalf("the two panes must render, pane=%d editor=%d", pane, editor)
	case !(panels < pane && pane < editor):
		t.Errorf("the step sequence must come before the editor, got panels=%d pane=%d editor=%d", panels, pane, editor)
	case panels < formStart || panels > formEnd:
		t.Errorf("both panes must stay inside the builder form, got panels=%d form=[%d,%d]", panels, formStart, formEnd)
	}

	// The sequence is a vertical list of steps: one list item per step, the
	// Add step control stays reachable from it, and the terminal keeps its
	// badge. Nothing here moved to a second control set.
	if got := strings.Count(body[:formEnd], `class="workflow-step-card-link"`); got != 4 {
		t.Errorf("the sequence must list every step once, got %d cards", got)
	}
	if !strings.Contains(body, `class="workflow-add-popover"`) {
		t.Errorf("the sequence must keep the Add step control, got: %.600s", body)
	}
	if !strings.Contains(body, `workflow-final-badge">Final</span>`) {
		t.Errorf("the terminal step must keep its Final badge, got: %.600s", body)
	}

	// The pane label states how many steps the sequence holds, through the
	// catalogue's plural message rather than a literal.
	if !strings.Contains(body, "4 steps") {
		t.Errorf("the sequence must state its step count, got: %.600s", body)
	}
	if strings.Contains(body, "%!d") {
		t.Errorf("the step count must interpolate as a number, got: %.600s", body)
	}

	// One step reads "1 step".
	single := lateralBuilderPageData()
	single.Draft = single.Draft[len(single.Draft)-1:]
	single.Steps = workflowStepViews(single.Draft, 0, single.Desks)
	single.SelectedStepIndex = 0
	single.SelectedStepPosition = 1
	singleBody := renderGolden(t, "category_workflow", "", single, false)
	if !strings.Contains(singleBody, "1 step<") && !strings.Contains(singleBody, ">1 step") {
		t.Errorf("a one-step workflow must read \"1 step\", got: %.600s", singleBody)
	}
}

// TestWorkflowBuilderVersionBar proves the published version, the draft author,
// the transient success live region and the clone control share one version bar
// above the two panes, and that the clone control is a form of its own: it must
// stay OUTSIDE the builder form, or its select would join the dirty-state
// baseline and every choice of a clone source would look like an unsaved edit.
func TestWorkflowBuilderVersionBar(t *testing.T) {
	body := renderGolden(t, "category_workflow", "", lateralBuilderPageData(), false)

	bar := strings.Index(body, `class="workflow-version-bar"`)
	if bar < 0 {
		t.Fatalf("builder must render one version bar, got: %.600s", body)
	}
	if bar > strings.Index(body, `class="workflow-builder-panels"`) {
		t.Errorf("the version bar must sit above the two panes")
	}
	for _, want := range []string{
		`class="workflow-badge workflow-badge-published" data-workflow-version>`,
		`class="workflow-badge workflow-badge-draft" data-workflow-draft-author>`,
		`data-workflow-version>Published v3 · by Ana Torres`,
		`data-workflow-draft-author>Draft last edited by Marco Ruiz`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("version bar must contain %q, got: %.900s", want, body)
		}
	}

	clone := strings.Index(body, `/workflow/clone"`)
	if clone < 0 {
		t.Fatalf("the clone control must render, got: %.600s", body)
	}
	if clone < bar {
		t.Errorf("the clone control belongs in the version bar, got clone=%d bar=%d", clone, bar)
	}
	formStart := strings.Index(body, `id="workflow-form"`)
	if clone > formStart {
		t.Errorf("the clone form must stay outside the builder form, got clone=%d form=%d", clone, formStart)
	}
	for _, want := range []string{`name="source_category_id"`, `>Printer support</option>`} {
		if !strings.Contains(body, want) {
			t.Errorf("the clone control must keep %q, got: %.600s", want, body)
		}
	}

	// The success live region keeps its own element and role, so the transient
	// "Saved." announcement is not a version fact.
	live := lateralBuilderPageData()
	live.Live = "Saved."
	liveBody := renderGolden(t, "category_workflow", "", live, false)
	if !strings.Contains(liveBody, `class="workflow-live" role="status" aria-live="polite" data-workflow-live`) {
		t.Errorf("the live region must keep its role and hook, got: %.600s", liveBody)
	}
	if !strings.Contains(liveBody, `data-workflow-live>Saved.`) {
		t.Errorf("the live region must carry the transient message, got: %.600s", liveBody)
	}
}

// TestWorkflowBuilderVerticalReorderCopy pins the direction words the vertical
// sequence now uses. The action VALUES stay move_up/move_down/move_left, so the
// public reorder contract is unchanged; only the visible words follow the axis.
func TestWorkflowBuilderVerticalReorderCopy(t *testing.T) {
	data := lateralBuilderPageData()
	for _, tc := range []struct{ locale, up, down string }{
		{"en", ">Move up</button>", ">Move down</button>"},
		{"es", ">Mover arriba</button>", ">Mover abajo</button>"},
	} {
		body := renderLocalized(t, tc.locale, "category_workflow", "", data)
		for _, want := range []string{tc.up, tc.down} {
			if !strings.Contains(body, want) {
				t.Errorf("%s: vertical reorder must read %q, got: %.900s", tc.locale, want, body)
			}
		}
		for _, gone := range []string{">Move left</button>", ">Move right</button>", ">Mover a la izquierda</button>", ">Mover a la derecha</button>"} {
			if strings.Contains(body, gone) {
				t.Errorf("%s: the horizontal wording %q must be gone", tc.locale, gone)
			}
		}
	}
	// The action values and their query index contract are untouched.
	body := renderLocalized(t, "en", "category_workflow", "", data)
	for _, want := range []string{`name="action" value="move_up"`, `name="action" value="move_down"`, `?step_index=1&amp;selected_step_index=1`} {
		if !strings.Contains(body, want) {
			t.Errorf("the reorder action contract must stay %q, got: %.900s", want, body)
		}
	}
}

// TestWorkflowLateralLayoutCSS freezes the stylesheet half of the approved A
// layout on the same class names the markup uses: a two-column pane grid that
// collapses to one column on a phone, a vertical sequence that no longer
// scrolls sideways, a horizontal insertion indicator, and the divider each pane
// draws. The previous horizontal geometry (horizontal rail, side insertion bar)
// must be gone, or the screen is neither layout.
func TestWorkflowLateralLayoutCSS(t *testing.T) {
	body := renderGolden(t, "category_workflow", "", mobileBuilderPageData(), false)
	style := extractStyleBlock(t, body)
	for _, tc := range []struct {
		name, sel, decl string
	}{
		{"sequence beside the editor", ".workflow-builder-panels{", "grid-template-columns:minmax(0,320px) minmax(0,1fr)"},
		{"the sequence is vertical", ".workflow-step-rail{", "flex-direction:column"},
		{"the sequence no longer scrolls sideways", ".workflow-step-rail{", "overflow:visible"},
		{"each step fills the pane", ".workflow-step-card{", "width:100%"},
		{"the insertion mark is horizontal", ".workflow-drag-indicator{", "height:3px"},
		{"the insertion mark spans the pane", ".workflow-drag-indicator{", "right:8px"},
		{"the steps pane draws its divider", ".workflow-rail-wrap{", "border-right:1px solid var(--line)"},
		{"the position becomes a chip", ".workflow-step-position{", "min-width:24px"},
	} {
		if !cssRuleDeclares(style, tc.sel, tc.decl) {
			t.Errorf("%s: %s must declare %s", tc.name, tc.sel, tc.decl)
		}
	}
	// The horizontal geometry is not merely unused: it is gone.
	for _, gone := range []string{"overflow-x:auto;flex-wrap:nowrap", "indicator.style.left"} {
		if strings.Contains(style, gone) {
			t.Errorf("the horizontal layout must not survive: found %q", gone)
		}
	}
	if rule := cssRuleForSelectors(style, ".workflow-drag-indicator"); strings.Contains(rule, "width:3px") {
		t.Errorf("the insertion mark must not keep the vertical bar geometry, got: %s", rule)
	}

	mobile := extractMediaBlock(t, style, "max-width:640px")
	if mobile == "" {
		t.Fatal("the shared stylesheet must keep its max-width:640 block")
	}
	for _, tc := range []struct {
		name, sel, decl string
	}{
		{"the panes stack on a phone", ".workflow-builder-panels{", "grid-template-columns:1fr"},
		{"the sequence sits above the editor", ".workflow-rail-wrap{", "border-bottom:1px solid var(--line)"},
	} {
		if !cssRuleDeclares(mobile, tc.sel, tc.decl) {
			t.Errorf("%s: %s must declare %s under 640px", tc.name, tc.sel, tc.decl)
		}
	}
}

// TestWorkflowBuilderPageHasNoWhitespaceOnlyLines keeps the lateral layout
// inside amendment 4's machine check: the page ships no whitespace-only line and
// no line ending in a space or tab, so a template indentation change cannot
// leave blank output behind on a screen the check would reject.
func TestWorkflowBuilderPageHasNoWhitespaceOnlyLines(t *testing.T) {
	for _, tc := range []struct {
		name string
		page string
		data any
	}{
		{"builder", "category_workflow", lateralBuilderPageData()},
	} {
		body := renderGolden(t, tc.page, "", tc.data, false)
		for lineNumber, line := range strings.Split(body, "\n") {
			if (line != "" && strings.TrimSpace(line) == "") || strings.HasSuffix(line, " ") || strings.HasSuffix(line, "\t") {
				t.Errorf("%s page has a whitespace-violating line %d: %q", tc.name, lineNumber+1, line)
			}
		}
	}
}
