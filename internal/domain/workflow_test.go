package domain_test

import (
	"encoding/json"
	"testing"

	"github.com/giulianotesta7/tkt/internal/domain"
)

func TestWorkflow_Validate_Empty(t *testing.T) {
	var d domain.WorkflowDefinition
	if len(d.Validate()) == 0 {
		t.Fatal("empty must be rejected")
	}
}
func TestWorkflow_Validate_TerminalMutualExclusion(t *testing.T) {
	d := domain.WorkflowDefinition{{Type: domain.StepResolve}, {Type: domain.StepClose}}
	if len(d.Validate()) == 0 {
		t.Fatal("resolve+close must be rejected")
	}
}
func TestWorkflow_Validate_DuplicateKey(t *testing.T) {
	d := domain.WorkflowDefinition{
		{Type: domain.StepForm, Form: &domain.FormStep{Actor: domain.FormActorRequester, Fields: []domain.FormField{{Key: "k1", Label: "L1", Kind: domain.FieldShortText}}}},
		{Type: domain.StepForm, Form: &domain.FormStep{Actor: domain.FormActorAssignee, Fields: []domain.FormField{{Key: "k1", Label: "L2", Kind: domain.FieldShortText}}}},
	}
	if len(d.Validate()) == 0 {
		t.Fatal("duplicate key must be rejected")
	}
}
func TestWorkflow_CanonicalBytesEqualAfterTrim(t *testing.T) {
	a := domain.WorkflowDefinition{{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "  Do it  "}}}
	b := domain.WorkflowDefinition{{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "Do it"}}}
	ba, _ := a.MarshalCanonical()
	bb, _ := b.MarshalCanonical()
	if string(ba) != string(bb) {
		t.Fatalf("canonical must be equal after trim: %s vs %s", ba, bb)
	}
	if _, err := domain.ParseWorkflowDefinition([]byte(`[{"type":"manual_task","unknown_field":1}]`)); err == nil {
		t.Fatal("unknown fields must be rejected")
	}
	_ = json.RawMessage{}
}
func TestWorkflow_Triangulate_Edges(t *testing.T) {
	cs := []struct {
		n string
		d domain.WorkflowDefinition
		v bool
	}{
		{"whitespace key", domain.WorkflowDefinition{{Type: domain.StepForm, Form: &domain.FormStep{Actor: domain.FormActorRequester, Fields: []domain.FormField{{Key: "   ", Label: "L", Kind: domain.FieldShortText}}}}}, false},
		{"dup key trim", domain.WorkflowDefinition{{Type: domain.StepForm, Form: &domain.FormStep{Actor: domain.FormActorRequester, Fields: []domain.FormField{{Key: "k1 ", Label: "L1", Kind: domain.FieldShortText}}}}, {Type: domain.StepForm, Form: &domain.FormStep{Actor: domain.FormActorRequester, Fields: []domain.FormField{{Key: " k1", Label: "L2", Kind: domain.FieldShortText}}}}}, false},
		{"dup option trim", domain.WorkflowDefinition{{Type: domain.StepForm, Form: &domain.FormStep{Actor: domain.FormActorRequester, Fields: []domain.FormField{{Key: "k", Label: "L", Kind: domain.FieldSingleSelect, Options: []string{" a ", "a"}}}}}}, false},
		{"resolve not last", domain.WorkflowDefinition{{Type: domain.StepResolve}, {Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "do"}}}, false},
		{"empty", domain.WorkflowDefinition{}, false},
		{"valid", domain.WorkflowDefinition{{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "Do it"}}}, true},
	}
	for _, c := range cs {
		t.Run(c.n, func(t *testing.T) {
			ok := len(c.d.Validate()) == 0
			if ok != c.v {
				t.Fatalf("%s: got valid=%v want %v issues=%v", c.n, ok, c.v, c.d.Validate())
			}
		})
	}
}
func TestWorkflow_Validate_ClosedTypesAndRules(t *testing.T) {
	cs := []struct {
		n string
		d domain.WorkflowDefinition
		v bool
	}{
		{"unknown type", domain.WorkflowDefinition{{Type: "unknown"}}, false},
		{"assign missing desk", domain.WorkflowDefinition{{Type: domain.StepAssignToDesk, AssignToDesk: &domain.AssignToDeskStep{DeskID: 0, Strategy: domain.StrategyClaim}}}, false},
		{"assign bad strategy", domain.WorkflowDefinition{{Type: domain.StepAssignToDesk, AssignToDesk: &domain.AssignToDeskStep{DeskID: 1, Strategy: "random"}}}, false},
		{"form bad actor", domain.WorkflowDefinition{{Type: domain.StepForm, Form: &domain.FormStep{Actor: "other", Fields: []domain.FormField{{Key: "k", Label: "L", Kind: domain.FieldShortText}}}}}, false},
		{"form empty fields", domain.WorkflowDefinition{{Type: domain.StepForm, Form: &domain.FormStep{Actor: domain.FormActorRequester}}}, false},
		{"select <2", domain.WorkflowDefinition{{Type: domain.StepForm, Form: &domain.FormStep{Actor: domain.FormActorRequester, Fields: []domain.FormField{{Key: "k", Label: "L", Kind: domain.FieldSingleSelect, Options: []string{"only"}}}}}}, false},
		{"options on non-select", domain.WorkflowDefinition{{Type: domain.StepForm, Form: &domain.FormStep{Actor: domain.FormActorRequester, Fields: []domain.FormField{{Key: "k", Label: "L", Kind: domain.FieldShortText, Options: []string{"x"}}}}}}, false},
		{"manual empty", domain.WorkflowDefinition{{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "   "}}}, false},
		{"terminal not final", domain.WorkflowDefinition{{Type: domain.StepResolve}, {Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "do"}}}, false},
		{"valid minimal", domain.WorkflowDefinition{{Type: domain.StepAssignToDesk, AssignToDesk: &domain.AssignToDeskStep{DeskID: 1, Strategy: domain.StrategyClaim}}}, true},
	}
	for _, c := range cs {
		t.Run(c.n, func(t *testing.T) {
			ok := len(c.d.Validate()) == 0
			if ok != c.v {
				t.Fatalf("%s: got valid=%v want %v issues=%v", c.n, ok, c.v, c.d.Validate())
			}
		})
	}
}
