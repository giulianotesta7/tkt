package domain_test

import (
	"testing"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// TestWorkflow_CountChanges pins the unit the category badge reports, because the
// number is user-facing ("Published v2 · 1 unpublished change") and the unit is a
// STEP, not a leaf field: that is what a one-line label can state honestly.
func TestWorkflow_CountChanges(t *testing.T) {
	m := func(instr string) domain.WorkflowStep {
		return domain.WorkflowStep{Type: domain.StepManualTask,
			ManualTask: &domain.ManualTaskStep{Instructions: instr}}
	}
	assign := func(desk int64) domain.WorkflowStep {
		return domain.WorkflowStep{Type: domain.StepAssignToDesk,
			AssignToDesk: &domain.AssignToDeskStep{DeskID: desk, Strategy: domain.StrategyClaim}}
	}

	cs := []struct {
		name string
		a, b domain.WorkflowDefinition
		want int
	}{
		{"identical", domain.WorkflowDefinition{m("a"), m("b")}, domain.WorkflowDefinition{m("a"), m("b")}, 0},
		{
			// Canonical bytes trim, so a whitespace-only edit is not a change the
			// admin needs to publish.
			"whitespace only",
			domain.WorkflowDefinition{m("  a  ")}, domain.WorkflowDefinition{m("a")}, 0,
		},
		{"one step reworded", domain.WorkflowDefinition{m("a"), m("b")}, domain.WorkflowDefinition{m("a"), m("c")}, 1},
		{
			// A reorder moves both positions, so both steps differ by position.
			"reordered",
			domain.WorkflowDefinition{m("a"), m("b")}, domain.WorkflowDefinition{m("b"), m("a")}, 2,
		},
		{"a step removed", domain.WorkflowDefinition{m("a"), m("b")}, domain.WorkflowDefinition{m("a")}, 1},
		{"a step added", domain.WorkflowDefinition{m("a")}, domain.WorkflowDefinition{m("a"), m("b")}, 1},
		{"two steps shortened to one", domain.WorkflowDefinition{m("first"), m("second")}, domain.WorkflowDefinition{m("edited")}, 2},
		{"a replaced routing step", domain.WorkflowDefinition{assign(1)}, domain.WorkflowDefinition{assign(2)}, 1},
		{"empty against empty", domain.WorkflowDefinition{}, domain.WorkflowDefinition{}, 0},
		{"empty against one step", domain.WorkflowDefinition{}, domain.WorkflowDefinition{m("a")}, 1},
	}

	for _, c := range cs {
		t.Run(c.name, func(t *testing.T) {
			if got := domain.CountChanges(c.a, c.b); got != c.want {
				t.Fatalf("CountChanges = %d, want %d", got, c.want)
			}
			// The count is a difference, so it must not depend on the direction.
			if got := domain.CountChanges(c.b, c.a); got != c.want {
				t.Fatalf("CountChanges is not symmetric: %d, want %d", got, c.want)
			}
		})
	}
}
