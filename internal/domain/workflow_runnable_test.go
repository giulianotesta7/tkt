package domain_test

import (
	"testing"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// TestWorkflow_AssessRunnable_Blockers pins what must never publish: shapes that
// cannot move a ticket at all, with no way for an operator to rescue them.
func TestWorkflow_AssessRunnable_Blockers(t *testing.T) {
	lookup := deskLookup(map[int64]deskFacts{
		1: {"Support", 2},
		2: {"Infra", 0},
	})

	cs := []struct {
		name string
		def  domain.WorkflowDefinition
		want []int // one-based step positions that must be blocked
	}{
		{
			"healthy path has no blocker",
			domain.WorkflowDefinition{form(domain.FormActorRequester), assign(1, domain.StrategyClaim), manual(), {Type: domain.StepResolve}},
			nil,
		},
		{
			"a claim step on a staffed desk is runnable",
			domain.WorkflowDefinition{assign(1, domain.StrategyClaim), manual()},
			nil,
		},
		{
			"a least_loaded step on a staffed desk is runnable",
			domain.WorkflowDefinition{assign(1, domain.StrategyLeastLoaded)},
			nil,
		},
		{
			"a desk nobody belongs to, reported once for its own reason",
			domain.WorkflowDefinition{assign(2, domain.StrategyClaim), manual()},
			[]int{1},
		},
		{
			"a desk that does not exist",
			domain.WorkflowDefinition{assign(9, domain.StrategyClaim)},
			[]int{1},
		},
	}

	for _, c := range cs {
		t.Run(c.name, func(t *testing.T) {
			got := c.def.AssessRunnable(lookup)
			if len(got.Blockers) != len(c.want) {
				t.Fatalf("want blockers %v, got %+v", c.want, got.Blockers)
			}
			for i, step := range c.want {
				if got.Blockers[i].Step != step {
					t.Fatalf("blocker %d: want step %d, got %d (%s)", i, step, got.Blockers[i].Step, got.Blockers[i].Message)
				}
				if got.Blockers[i].Message == "" {
					t.Fatalf("blocker %d must carry a message a surface can show", i)
				}
			}
			if got.Runnable() != (len(c.want) == 0) {
				t.Fatalf("Runnable() must agree with the blocker list")
			}
		})
	}
}

// TestWorkflow_AssessRunnable_WarningsNotBlockers is the distinction that keeps a
// legitimate manual process publishable. Creation binds no assignee by
// requirement, so a human step with nothing assigning before it means every
// ticket waits for a person — a warning, never a refusal.
func TestWorkflow_AssessRunnable_WarningsNotBlockers(t *testing.T) {
	lookup := deskLookup(map[int64]deskFacts{1: {"Support", 2}})

	cs := []struct {
		name string
		def  domain.WorkflowDefinition
		warn []int
	}{
		{
			"a lone manual task is publishable and manual",
			domain.WorkflowDefinition{manual()},
			[]int{1},
		},
		{
			"an assignee form with nothing assigning before it",
			domain.WorkflowDefinition{form(domain.FormActorAssignee), {Type: domain.StepResolve}},
			[]int{1},
		},
		{
			"a requester form is not a manual step",
			domain.WorkflowDefinition{form(domain.FormActorRequester), assign(1, domain.StrategyLeastLoaded)},
			nil,
		},
		{
			"an assignment step before the human step removes the warning",
			domain.WorkflowDefinition{assign(1, domain.StrategyClaim), manual()},
			nil,
		},
	}

	for _, c := range cs {
		t.Run(c.name, func(t *testing.T) {
			got := c.def.AssessRunnable(lookup)
			if !got.Runnable() {
				t.Fatalf("these shapes must stay publishable, got blockers %+v", got.Blockers)
			}
			if len(got.Warnings) != len(c.warn) {
				t.Fatalf("want warnings at %v, got %+v", c.warn, got.Warnings)
			}
			for i, step := range c.warn {
				if got.Warnings[i].Step != step {
					t.Fatalf("warning %d: want step %d, got %d", i, step, got.Warnings[i].Step)
				}
			}
		})
	}
}

// TestWorkflow_AssessRunnable_ShapeIsNotItsJob keeps the two validators separate:
// a definition can be well shaped and unrunnable, and the reverse.
func TestWorkflow_AssessRunnable_ShapeIsNotItsJob(t *testing.T) {
	lookup := deskLookup(map[int64]deskFacts{1: {"Support", 3}})

	// Shape is fine — Validate accepts it — and it still needs a person.
	lone := domain.WorkflowDefinition{manual()}
	if len(lone.Validate()) != 0 {
		t.Fatal("a lone manual task is well shaped; Validate must still accept it")
	}
	if len(lone.AssessRunnable(lookup).Warnings) == 0 {
		t.Fatal("a lone manual task must be reported as manual")
	}

	// Membership is fine, yet malformed: the terminal is not last, which is
	// Validate's finding, not this one's.
	malformed := domain.WorkflowDefinition{
		assign(1, domain.StrategyClaim),
		{Type: domain.StepResolve},
		manual(),
	}
	if len(malformed.Validate()) == 0 {
		t.Fatal("a terminal that is not last must be rejected by Validate")
	}
	if got := malformed.AssessRunnable(lookup); len(got.Blockers) != 0 || len(got.Warnings) != 0 {
		t.Fatalf("the scan stops at the terminal, so membership has nothing to say: %+v", got)
	}
}

// ---------------------------------------------------------------- fixtures

type deskFacts struct {
	name    string
	members int
}

func deskLookup(m map[int64]deskFacts) domain.DeskLookup {
	return func(id int64) (string, int, bool) {
		d, ok := m[id]
		if !ok {
			return "", 0, false
		}
		return d.name, d.members, true
	}
}

func form(a domain.FormActor) domain.WorkflowStep {
	return domain.WorkflowStep{Type: domain.StepForm, Form: &domain.FormStep{
		Actor: a, Fields: []domain.FormField{{Key: "k", Label: "L", Kind: domain.FieldShortText}}}}
}

func assign(desk int64, s domain.AssignmentStrategy) domain.WorkflowStep {
	return domain.WorkflowStep{Type: domain.StepAssignToDesk,
		AssignToDesk: &domain.AssignToDeskStep{DeskID: desk, Strategy: s}}
}

func manual() domain.WorkflowStep {
	return domain.WorkflowStep{Type: domain.StepManualTask,
		ManualTask: &domain.ManualTaskStep{Instructions: "Do it"}}
}
