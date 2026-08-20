package domain

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

type StepType string

const (
	StepAssignToDesk StepType = "assign_to_desk"
	StepForm         StepType = "form"
	StepManualTask   StepType = "manual_task"
	StepResolve      StepType = "resolve_ticket"
	StepClose        StepType = "close_ticket"
)

type AssignmentStrategy string

const (
	StrategyClaim       AssignmentStrategy = "claim"
	StrategyLeastLoaded AssignmentStrategy = "least_loaded"
)

type FormActor string

const (
	FormActorRequester FormActor = "requester"
	FormActorAssignee  FormActor = "assignee"
)

type FieldKind string

const (
	FieldShortText    FieldKind = "short_text"
	FieldLongText     FieldKind = "long_text"
	FieldCheckbox     FieldKind = "checkbox"
	FieldSingleSelect FieldKind = "single_select"
)

type WorkflowDefinition []WorkflowStep

type WorkflowStep struct {
	Type         StepType          `json:"type"`
	AssignToDesk *AssignToDeskStep `json:"assign_to_desk,omitempty"`
	Form         *FormStep         `json:"form,omitempty"`
	ManualTask   *ManualTaskStep   `json:"manual_task,omitempty"`
}
type AssignToDeskStep struct {
	DeskID   int64              `json:"desk_id"`
	Strategy AssignmentStrategy `json:"strategy"`
}
type FormStep struct {
	Actor  FormActor   `json:"actor"`
	Fields []FormField `json:"fields"`
}
type ManualTaskStep struct {
	Instructions string `json:"instructions"`
}
type FormField struct {
	Key      string    `json:"key"`
	Label    string    `json:"label"`
	Kind     FieldKind `json:"kind"`
	Required bool      `json:"required"`
	Options  []string  `json:"options,omitempty"`
}
type WorkflowValidationIssue struct {
	Step    int
	Field   string
	Message string
}

func (d WorkflowDefinition) Validate() []WorkflowValidationIssue {
	var issues []WorkflowValidationIssue
	add := func(s int, f, m string) {
		issues = append(issues, WorkflowValidationIssue{Step: s, Field: f, Message: m})
	}
	if len(d) == 0 {
		add(1, "steps", "workflow must have at least one step")
		return issues
	}
	tc, tp := 0, -1
	for i, s := range d {
		if s.Type == StepResolve || s.Type == StepClose {
			tc++
			tp = i
		}
	}
	if tc > 1 {
		add(tp+1, "type", "workflow must have at most one terminal step")
	}
	hr, hc := false, false
	for _, s := range d {
		if s.Type == StepResolve {
			hr = true
		}
		if s.Type == StepClose {
			hc = true
		}
	}
	if hr && hc {
		add(tp+1, "type", "resolve_ticket and close_ticket are mutually exclusive")
	}
	if tc == 1 && tp != len(d)-1 {
		add(tp+1, "type", "terminal step must be the final step")
	}
	seen := map[string]int{}
	for i, s := range d {
		n := i + 1
		switch s.Type {
		case StepAssignToDesk, StepForm, StepManualTask, StepResolve, StepClose:
		default:
			add(n, "type", fmt.Sprintf("Step %d: unknown step type %q", n, s.Type))
			continue
		}
		switch s.Type {
		case StepAssignToDesk:
			if s.AssignToDesk == nil || s.Form != nil || s.ManualTask != nil {
				add(n, "type", fmt.Sprintf("Step %d: assign_to_desk requires exactly assign_to_desk config", n))
				continue
			}
		case StepForm:
			if s.Form == nil || s.AssignToDesk != nil || s.ManualTask != nil {
				add(n, "type", fmt.Sprintf("Step %d: form requires exactly form config", n))
				continue
			}
		case StepManualTask:
			if s.ManualTask == nil || s.AssignToDesk != nil || s.Form != nil {
				add(n, "type", fmt.Sprintf("Step %d: manual_task requires exactly manual_task config", n))
				continue
			}
		case StepResolve, StepClose:
			if s.AssignToDesk != nil || s.Form != nil || s.ManualTask != nil {
				add(n, "type", fmt.Sprintf("Step %d: terminal step must have no config", n))
				continue
			}
		}
		switch s.Type {
		case StepAssignToDesk:
			validateAssign(n, s.AssignToDesk, add)
		case StepForm:
			validateForm(n, s.Form, seen, add)
		case StepManualTask:
			validateManual(n, s.ManualTask, add)
		}
	}
	return issues
}
func validateAssign(n int, ad *AssignToDeskStep, add func(int, string, string)) {
	if ad.DeskID <= 0 {
		add(n, "desk_id", fmt.Sprintf("Step %d: choose a desk", n))
	}
	if ad.Strategy != StrategyClaim && ad.Strategy != StrategyLeastLoaded {
		add(n, "strategy", fmt.Sprintf("Step %d: strategy must be claim or least_loaded", n))
	}
}
func validateForm(n int, f *FormStep, seen map[string]int, add func(int, string, string)) {
	a := FormActor(strings.TrimSpace(string(f.Actor)))
	if a != FormActorRequester && a != FormActorAssignee {
		add(n, "actor", fmt.Sprintf("Step %d: form actor must be requester or assignee", n))
	}
	if len(f.Fields) == 0 {
		add(n, "fields", fmt.Sprintf("Step %d: form must have at least one field", n))
	}
	for _, fld := range f.Fields {
		k, l := strings.TrimSpace(fld.Key), strings.TrimSpace(fld.Label)
		if k == "" {
			add(n, "key", fmt.Sprintf("Step %d: field key is required", n))
		}
		if l == "" {
			add(n, "label", fmt.Sprintf("Step %d: field label is required", n))
		}
		if k != "" {
			if p, ok := seen[k]; ok {
				add(n, "key", fmt.Sprintf("Step %d: duplicate field key %q (also in step %d)", n, k, p))
			} else {
				seen[k] = n
			}
		}
		switch fld.Kind {
		case FieldShortText, FieldLongText, FieldCheckbox, FieldSingleSelect:
		default:
			add(n, "kind", fmt.Sprintf("Step %d: unknown field kind %q", n, fld.Kind))
			continue
		}
		if fld.Kind != FieldSingleSelect && len(fld.Options) > 0 {
			add(n, "options", fmt.Sprintf("Step %d: options are only allowed for single_select", n))
		}
		if fld.Kind == FieldSingleSelect {
			tr := []string{}
			for _, o := range fld.Options {
				tr = append(tr, strings.TrimSpace(o))
			}
			ne := []string{}
			for _, o := range tr {
				if o != "" {
					ne = append(ne, o)
				}
			}
			if len(ne) < 2 {
				add(n, "options", fmt.Sprintf("Step %d: single_select requires at least two options", n))
			} else {
				m := map[string]bool{}
				for _, o := range ne {
					if m[o] {
						add(n, "options", fmt.Sprintf("Step %d: duplicate option %q", n, o))
						break
					}
					m[o] = true
				}
				for _, o := range tr {
					if o == "" {
						add(n, "options", fmt.Sprintf("Step %d: option must not be empty", n))
						break
					}
				}
			}
		}
	}
}
func validateManual(n int, m *ManualTaskStep, add func(int, string, string)) {
	if strings.TrimSpace(m.Instructions) == "" {
		add(n, "instructions", fmt.Sprintf("Step %d: instructions are required", n))
	}
}
func (d WorkflowDefinition) normalizedCopy() WorkflowDefinition {
	o := make(WorkflowDefinition, len(d))
	for i, s := range d {
		ns := WorkflowStep{Type: StepType(strings.TrimSpace(string(s.Type)))}
		if s.AssignToDesk != nil {
			ns.AssignToDesk = &AssignToDeskStep{DeskID: s.AssignToDesk.DeskID, Strategy: AssignmentStrategy(strings.TrimSpace(string(s.AssignToDesk.Strategy)))}
		}
		if s.Form != nil {
			nf := FormStep{Actor: FormActor(strings.TrimSpace(string(s.Form.Actor)))}
			for _, f := range s.Form.Fields {
				nf.Fields = append(nf.Fields, FormField{Key: strings.TrimSpace(f.Key), Label: strings.TrimSpace(f.Label), Kind: FieldKind(strings.TrimSpace(string(f.Kind))), Required: f.Required, Options: trimOpts(f.Options, f.Kind)})
			}
			ns.Form = &nf
		}
		if s.ManualTask != nil {
			ns.ManualTask = &ManualTaskStep{Instructions: strings.TrimSpace(s.ManualTask.Instructions)}
		}
		o[i] = ns
	}
	return o
}
func trimOpts(o []string, k FieldKind) []string {
	if k != FieldSingleSelect {
		return nil
	}
	if o == nil {
		return nil
	}
	r := make([]string, len(o))
	for i, v := range o {
		r[i] = strings.TrimSpace(v)
	}
	return r
}
func (d WorkflowDefinition) MarshalCanonical() ([]byte, error) {
	return json.Marshal(d.normalizedCopy())
}
func ParseWorkflowDefinition(b []byte) (WorkflowDefinition, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	var d WorkflowDefinition
	if err := dec.Decode(&d); err != nil {
		return nil, err
	}
	if dec.More() {
		return nil, fmt.Errorf("trailing data")
	}
	return d, nil
}
