package domain_test

import (
	"reflect"
	"testing"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// allShapesDefinition builds a WorkflowDefinition touching every nested
// pointer/slice shape of the closed union: an assign_to_desk step, a form step
// with nested Fields (short_text with nil Options and single_select with
// Options), a manual_task step, and a config-less terminal step.
func allShapesDefinition() domain.WorkflowDefinition {
	return domain.WorkflowDefinition{
		{Type: domain.StepAssignToDesk, AssignToDesk: &domain.AssignToDeskStep{DeskID: 3, Strategy: domain.StrategyLeastLoaded}},
		{Type: domain.StepForm, Form: &domain.FormStep{Actor: domain.FormActorRequester, Fields: []domain.FormField{
			{Key: "reason", Label: "Reason", Kind: domain.FieldShortText, Required: true},
			{Key: "priority", Label: "Priority", Kind: domain.FieldSingleSelect, Options: []string{"low", "high"}},
		}}},
		{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "Provision the server"}},
		{Type: domain.StepResolve},
	}
}

// TestWorkflowDefinition_CloneDeepCopiesAllShapes proves the authoritative deep
// copy covers every closed step/config payload and nested form fields/options:
// the clone preserves all values, shares NO step, config pointer, field, or
// option with the source (identity checks), and survives in-place mutation of
// every nested shape without leaking into the original (value checks).
func TestWorkflowDefinition_CloneDeepCopiesAllShapes(t *testing.T) {
	def := allShapesDefinition()
	cp := def.Clone()

	if !reflect.DeepEqual(cp, def) {
		t.Fatalf("Clone must preserve every value:\n got  %#v\n want %#v", cp, def)
	}
	// Pointer/slice identity: every nested shape must be a distinct allocation.
	if &cp[0] == &def[0] || &cp[1] == &def[1] || &cp[2] == &def[2] || &cp[3] == &def[3] {
		t.Fatal("Clone must allocate a fresh step slice")
	}
	if cp[0].AssignToDesk == def[0].AssignToDesk {
		t.Fatal("Clone must deep-copy the AssignToDesk pointer")
	}
	if cp[1].Form == def[1].Form {
		t.Fatal("Clone must deep-copy the Form pointer")
	}
	if &cp[1].Form.Fields[0] == &def[1].Form.Fields[0] || &cp[1].Form.Fields[1] == &def[1].Form.Fields[1] {
		t.Fatal("Clone must deep-copy the nested Fields slice values")
	}
	if &cp[1].Form.Fields[1].Options[0] == &def[1].Form.Fields[1].Options[0] {
		t.Fatal("Clone must deep-copy the nested Options slice")
	}
	if cp[2].ManualTask == def[2].ManualTask {
		t.Fatal("Clone must deep-copy the ManualTask pointer")
	}

	// In-place mutation of EVERY shape in the clone must not leak into source.
	mutateEveryShape(cp)
	if !reflect.DeepEqual(def, allShapesDefinition()) {
		t.Fatal("mutating the clone must not alter the source definition")
	}
}

// TestWorkflowDefinition_ClonePreservesNilVsEmpty proves Clone preserves EVERY
// nil-vs-empty distinction while still deep-copying (PR5 second-attempt gate
// blocker 1): a nil definition clones to nil (never a non-nil empty slice), a
// non-nil empty definition clones to a non-nil empty slice, nil Form.Fields
// stay nil while non-nil empty Fields stay non-nil empty, nil per-field
// Options stay nil while non-nil empty Options stay non-nil empty, and every
// config pointer is still deep-copied into fresh allocations.
// reflect.DeepEqual (which distinguishes nil from empty) must hold for every
// variant. RED on Batch A: Clone(nil) returned a non-nil empty definition.
func TestWorkflowDefinition_ClonePreservesNilVsEmpty(t *testing.T) {
	var nilDef domain.WorkflowDefinition
	if cp := nilDef.Clone(); cp != nil {
		t.Fatalf("Clone(nil) must stay nil, got %#v", cp)
	}

	emptyDef := domain.WorkflowDefinition{}
	cp := emptyDef.Clone()
	if cp == nil {
		t.Fatal("Clone of a non-nil empty definition must stay non-nil empty, not nil")
	}
	if !reflect.DeepEqual(cp, emptyDef) {
		t.Fatal("Clone must preserve the non-nil empty top level")
	}

	// Every nested nil-vs-empty distinction plus config-pointer preservation.
	def := domain.WorkflowDefinition{
		{Type: domain.StepAssignToDesk, AssignToDesk: &domain.AssignToDeskStep{DeskID: 3, Strategy: domain.StrategyLeastLoaded}},
		{Type: domain.StepForm, Form: &domain.FormStep{Actor: domain.FormActorRequester, Fields: nil}},
		{Type: domain.StepForm, Form: &domain.FormStep{Actor: domain.FormActorAssignee, Fields: []domain.FormField{}}},
		{Type: domain.StepForm, Form: &domain.FormStep{Actor: domain.FormActorRequester, Fields: []domain.FormField{
			{Key: "k", Label: "L", Kind: domain.FieldSingleSelect, Options: nil},
			{Key: "k2", Label: "L2", Kind: domain.FieldSingleSelect, Options: []string{}},
		}}},
	}
	cp2 := def.Clone()
	if !reflect.DeepEqual(cp2, def) {
		t.Fatalf("Clone must preserve every nil-vs-empty value:\n got  %#v\n want %#v", cp2, def)
	}
	if cp2[0].AssignToDesk == def[0].AssignToDesk || cp2[1].Form == def[1].Form ||
		cp2[2].Form == def[2].Form || cp2[3].Form == def[3].Form {
		t.Fatal("Clone must deep-copy every config pointer even alongside nil/empty slices")
	}
	if cp2[1].Form.Fields != nil {
		t.Fatal("Clone must keep nil Form.Fields nil")
	}
	if cp2[2].Form.Fields == nil {
		t.Fatal("Clone must keep non-nil empty Form.Fields non-nil empty")
	}
	if &cp2[3].Form.Fields[0] == &def[3].Form.Fields[0] || &cp2[3].Form.Fields[1] == &def[3].Form.Fields[1] {
		t.Fatal("Clone must deep-copy field values")
	}
	if cp2[3].Form.Fields[0].Options != nil {
		t.Fatal("Clone must keep nil Options nil")
	}
	if cp2[3].Form.Fields[1].Options == nil {
		t.Fatal("Clone must keep non-nil empty Options non-nil empty")
	}
	if !reflect.DeepEqual(cp2[3].Form.Fields[1].Options, []string{}) {
		t.Fatal("Clone must preserve the empty Options value")
	}
}

// TestWorkflowDefinition_CanonicalEmptyFieldsNullHistorical pins the EXACT
// historical canonical bytes (8350e5a normalizedCopy) for an incomplete draft
// (PR5 second-attempt gate blocker 2): a non-nil empty Form.Fields must
// canonicalize as "fields":null — NOT "fields":[] — because safe draft
// persistence depends on the historical canonical shape; Batch A regressed
// this by re-expressing normalizedCopy over Clone (which preserves the
// non-nil empty slice). Canonical normalization must also never mutate the
// receiver: the non-nil empty Fields set stays non-nil empty and the draft
// stays DeepEqual to its original after MarshalCanonical.
func TestWorkflowDefinition_CanonicalEmptyFieldsNullHistorical(t *testing.T) {
	cs := []struct {
		name  string
		draft domain.WorkflowDefinition
		want  string
	}{
		{
			name:  "non-nil empty fields stays fields null",
			draft: domain.WorkflowDefinition{{Type: domain.StepForm, Form: &domain.FormStep{Actor: domain.FormActorRequester, Fields: []domain.FormField{}}}},
			want:  `[{"type":"form","form":{"actor":"requester","fields":null}}]`,
		},
		{
			name: "short_text field keeps historical bytes",
			draft: domain.WorkflowDefinition{{Type: domain.StepForm, Form: &domain.FormStep{Actor: domain.FormActorRequester, Fields: []domain.FormField{
				{Key: "k", Label: "L", Kind: domain.FieldShortText, Required: true},
			}}}},
			want: `[{"type":"form","form":{"actor":"requester","fields":[{"key":"k","label":"L","kind":"short_text","required":true}]}}]`,
		},
	}
	for _, c := range cs {
		t.Run(c.name, func(t *testing.T) {
			draft := c.draft
			b, err := draft.MarshalCanonical()
			if err != nil {
				t.Fatalf("MarshalCanonical: %v", err)
			}
			if string(b) != c.want {
				t.Fatalf("historical canonical bytes must be restored:\n got  %s\n want %s", b, c.want)
			}
			// Canonical normalization must not mutate the input draft.
			if !reflect.DeepEqual(draft, c.draft) {
				t.Fatalf("MarshalCanonical must leave the draft definition unmutated:\n got  %#v\n want %#v", draft, c.draft)
			}
		})
	}
	draft := domain.WorkflowDefinition{{Type: domain.StepForm, Form: &domain.FormStep{Actor: domain.FormActorRequester, Fields: []domain.FormField{}}}}
	if _, err := draft.MarshalCanonical(); err != nil {
		t.Fatalf("MarshalCanonical: %v", err)
	}
	if draft[0].Form.Fields == nil {
		t.Fatal("MarshalCanonical must not nil out a non-nil empty Form.Fields in the receiver")
	}
}

// mutateEveryShape mutates every nested shape of the union through the clone,
// mirroring the application mutation regression's coverage.
func mutateEveryShape(cp domain.WorkflowDefinition) {
	cp[0].Type = domain.StepManualTask
	cp[0].AssignToDesk.DeskID = 999
	cp[0].AssignToDesk.Strategy = domain.StrategyClaim
	cp[1].Type = domain.StepResolve
	cp[1].Form.Actor = domain.FormActorAssignee
	cp[1].Form.Fields[0].Key = "hacked"
	cp[1].Form.Fields[0].Label = "Hacked"
	cp[1].Form.Fields[0].Kind = domain.FieldCheckbox
	cp[1].Form.Fields[0].Required = false
	cp[1].Form.Fields[0].Options = append(cp[1].Form.Fields[0].Options, "leaked")
	cp[1].Form.Fields[1].Options[0] = "hacked"
	cp[1].Form.Fields = append(cp[1].Form.Fields, domain.FormField{Key: "extra", Label: "Extra", Kind: domain.FieldShortText})
	cp[2].Type = domain.StepAssignToDesk
	cp[2].ManualTask.Instructions = "HACKED"
	cp[3].Type = domain.StepClose
}
