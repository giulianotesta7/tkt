package httpadapter

import (
	"net/http"
	"strings"
	"testing"
)

// The workflow builder's browser asset owns the drag geometry and the
// dirty-state guards, and neither has a browser test for its internals: E2E
// proves the journeys, so these pins freeze the mechanism those journeys depend
// on. The approved redesign (issue #395, S3) changed ONE axis and nothing else:
// the drag reads and writes the vertical axis, while the source/target indexes,
// the final-step ceiling, the public reorder action and every guard keep the
// contract they had.
func TestWorkflowAssetVerticalDragGeometry(t *testing.T) {
	h := newHarness(t)
	asset := h.get(t, "/static/workflow.js", false)
	if asset.Code != http.StatusOK {
		t.Fatalf("workflow asset status = %d, want 200", asset.Code)
	}
	body := asset.Body.String()

	// The vertical axis: pointer position, slot midpoints, indicator offset and
	// the autoscroll all use the Y coordinate and the vertical scroll offset.
	for _, want := range []string{
		"event.clientY",
		"rect.top + rect.height / 2",
		"list[destination].offsetTop",
		".scrollTop",
		"scrollBy(0,",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the vertical drag must use %q, got: %.900s", want, body)
		}
	}
	// The horizontal axis is gone, not merely unused: a leftover clientX would
	// silently keep the old geometry on whichever path still called it.
	for _, gone := range []string{"clientX", "scrollLeft", "offsetLeft"} {
		if strings.Contains(body, gone) {
			t.Errorf("the horizontal drag axis must be gone, found %q", gone)
		}
	}

	// The reorder contract itself is unchanged: the slot math, the final-step
	// ceiling, the permanent source/target fields and the explicit reorder
	// submitter are the public action the server validates.
	for _, want := range []string{
		"destination = slot < source ? slot : slot - 1",
		"if (destination > movable - 1) destination = movable - 1",
		"named.source.value = String(source)",
		"named.target.value = String(destination)",
		`form.elements.namedItem("source_index")`,
		`form.elements.namedItem("target_index")`,
		`form.querySelector("[data-workflow-reorder]")`,
		"named.button.click()",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the reorder contract must keep %q, got: %.900s", want, body)
		}
	}
}

// TestWorkflowAssetGuardsUnchanged proves the redesign did not touch the
// machinery S3 requires to survive: the structural action set, the dirty
// baseline, the carried-dirty pin, the dialogs and the exit guards.
func TestWorkflowAssetGuardsUnchanged(t *testing.T) {
	h := newHarness(t)
	body := h.get(t, "/static/workflow.js", false).Body.String()
	for _, want := range []string{
		"const STRUCTURAL_ACTIONS = new Set([",
		`"add_step"`,
		`"remove_step"`,
		`"move_up"`,
		`"move_down"`,
		`"reorder"`,
		`"change_type"`,
		`"add_field"`,
		`"remove_field"`,
		`const PERSISTING_ACTIONS = new Set([...STRUCTURAL_ACTIONS, "save", "publish"])`,
		"carriedDirty = true",
		"function captureBaseline()",
		"function isDirty()",
		"function replayStructural(action, path, params)",
		`const builderForm = () => document.querySelector("#workflow-form")`,
		`window.addEventListener("beforeunload"`,
		`window.addEventListener("popstate"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the guard machinery must keep %q", want)
		}
	}
	// The dirty baseline is still the builder form alone: the clone control now
	// lives inside the builder region, so tracking any control outside
	// #workflow-form would turn choosing a clone source into an unsaved edit.
	for _, gone := range []string{`querySelectorAll("#workflow-builder`, "source_category_id"} {
		if strings.Contains(body, gone) {
			t.Errorf("the dirty baseline must stay on #workflow-form, found %q", gone)
		}
	}
}

// TestWorkflowTemplateScrollRestorationIsVertical proves the page-level swap
// restoration that keeps the sequence where the reader left it also moved to
// the vertical axis, and that adding a step still reveals the new step.
func TestWorkflowTemplateScrollRestorationIsVertical(t *testing.T) {
	page := renderGolden(t, "category_workflow", "", lateralBuilderPageData(), false)
	for _, want := range []string{
		"dataset.workflowRailScrollTop",
		"rail.scrollTop=",
		"scrollIntoView({block:'nearest'})",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the swap handler must restore the vertical position with %q, got: %.900s", want, page)
		}
	}
	for _, gone := range []string{"workflowRailScrollLeft", "rail.scrollLeft", "rail.scrollWidth"} {
		if strings.Contains(page, gone) {
			t.Errorf("the horizontal scroll restoration must be gone, found %q", gone)
		}
	}
}

// TestWorkflowAssetClonePopoverSharesDismissal proves the clone control the
// version bar hosts is dismissed like every other builder dropdown: one shared
// selector owns Escape and the outside click, so an open clone popover cannot
// outlive the interaction that opened it.
func TestWorkflowAssetClonePopoverSharesDismissal(t *testing.T) {
	h := newHarness(t)
	body := h.get(t, "/static/workflow.js", false).Body.String()
	if !strings.Contains(body, `".workflow-step-menu, .workflow-add-popover, .workflow-field-menu, .workflow-clone"`) {
		t.Fatalf("the clone popover must join the shared dropdown selector")
	}
	if !strings.Contains(body, "closeDropdown(open, { restoreFocus: false })") {
		t.Errorf("an outside click must close the open dropdown through the shared routine")
	}
	if !strings.Contains(body, "closeDropdown(details, { restoreFocus: true })") {
		t.Errorf("Escape must close the open dropdown and restore focus through the shared routine")
	}
}
