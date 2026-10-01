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

// DeskLookup resolves a desk to its name and to the number of members who could
// actually act on a ticket: active users holding agent, admin or root. That is
// the same eligibility the executor's least_loaded selection uses, so the gate
// and the assignment can never disagree about who is available. found=false
// means no such desk.
type DeskLookup func(deskID int64) (name string, members int, found bool)

// RunnableAssessment separates the two ways a well-shaped definition can still
// fail to move tickets, because they have different severities and blocking the
// wrong one forbids a legitimate process.
//
//   - Blockers: the definition can NEVER move a ticket. Publishing it produces a
//     category whose tickets fail on creation (a least_loaded step on an empty
//     desk rolls the whole create back) or can never be claimed by anyone. No
//     amount of operating can rescue it. Publish refuses these.
//   - Warnings: the definition cannot route ON ITS OWN, but a person can unblock
//     it. A human step reached before any assignment step means every ticket of
//     this category waits for someone to put a name on it. That is a real
//     operating model in this app — creation is unassigned-only by requirement,
//     and the executor's own runtime tests seed exactly this shape by assigning
//     the ticket through the audited Assign path. So it is reported, not refused.
//
// The distinction is not cosmetic: it is the difference between a workflow that
// is broken and a workflow that is manual.
type RunnableAssessment struct {
	Blockers []WorkflowValidationIssue
	Warnings []WorkflowValidationIssue
}

// Runnable reports whether nothing prevents the definition from working at all.
func (a RunnableAssessment) Runnable() bool { return len(a.Blockers) == 0 }

// AssessRunnable is the ONLY source of these rules. The publish gate refuses the
// blockers, and the category badge reports them; the builder can show both.
//
// It answers a question Validate does not. Validate checks shape — is the step
// well formed, is the terminal last, are the field keys unique — and a definition
// can pass every one of those and still be unable to move a single ticket.
//
// The issues carry the one-based step position so a surface can anchor each
// message to the step that caused it.
func (d WorkflowDefinition) AssessRunnable(desks DeskLookup) RunnableAssessment {
	var a RunnableAssessment
	block := func(step int, field, msg string) {
		a.Blockers = append(a.Blockers, WorkflowValidationIssue{Step: step, Field: field, Message: msg})
	}
	warn := func(step int, field, msg string) {
		a.Warnings = append(a.Warnings, WorkflowValidationIssue{Step: step, Field: field, Message: msg})
	}

	// assigned reports whether the definition CONTAINS an assignment step before
	// this point. It is about intent to assign, not about the desk being healthy:
	// an assignment step pointing at an empty desk is a blocker on its own, and
	// reporting the human step after it as a second, derivative problem would be
	// one cause wearing two messages.
	assigned := false
	for i, s := range d {
		n := i + 1
		switch s.Type {
		case StepAssignToDesk:
			assigned = true
			if s.AssignToDesk == nil {
				continue // a shape problem, reported by Validate
			}
			name, members, found := desks(s.AssignToDesk.DeskID)
			if !found {
				block(n, "desk_id", fmt.Sprintf("Step %d: choose a desk", n))
				continue
			}
			if members == 0 {
				block(n, "desk_id", fmt.Sprintf(
					"Step %d: nobody can take it — %s has no active members. Add someone to %s, or choose another desk.",
					n, name, name))
			}
		case StepManualTask:
			if !assigned {
				warn(n, "type", fmt.Sprintf(
					"Step %d: every ticket waits here until someone assigns it — no earlier step puts a person on it.", n))
			}
		case StepForm:
			if s.Form != nil && s.Form.Actor == FormActorAssignee && !assigned {
				warn(n, "actor", fmt.Sprintf(
					"Step %d: every ticket waits here until someone assigns it — no earlier step puts a person on it.", n))
			}
		case StepResolve, StepClose:
			// A terminal ends the run, so nothing after it is ever reached.
			return a
		}
	}
	return a
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

// Clone returns a deep copy of d that preserves EVERY nil-vs-empty distinction
// (PR5 second-attempt gate blocker 1): a nil definition stays nil, a non-nil
// empty definition stays a non-nil empty slice, nil Form.Fields stay nil
// (non-nil empty Fields stay non-nil empty), and nil per-field Options stay
// nil (non-nil empty Options stay non-nil empty), while every closed config
// pointer (AssignToDesk/Form/ManualTask), every field value, and every
// Options slice is deep-copied into fresh allocations. The clone shares NO
// step, config pointer, field, or option with the source, so mutating the
// original (or a store/caller-owned object) can never alter a captured
// snapshot. Clone is the application trust boundary's capture mechanism;
// canonical normalization (normalizedCopy) is intentionally NOT built on it so
// historical canonical bytes stay byte-for-byte stable for incomplete drafts.
func (d WorkflowDefinition) Clone() WorkflowDefinition {
	if d == nil {
		return nil
	}
	o := make(WorkflowDefinition, len(d))
	for i, s := range d {
		ns := WorkflowStep{Type: s.Type}
		if s.AssignToDesk != nil {
			ad := *s.AssignToDesk
			ns.AssignToDesk = &ad
		}
		if s.Form != nil {
			nf := FormStep{Actor: s.Form.Actor}
			if s.Form.Fields != nil {
				nf.Fields = make([]FormField, len(s.Form.Fields))
				for j, f := range s.Form.Fields {
					nf.Fields[j] = f
					if f.Options != nil {
						nf.Fields[j].Options = make([]string, len(f.Options))
						copy(nf.Fields[j].Options, f.Options)
					}
				}
			}
			ns.Form = &nf
		}
		if s.ManualTask != nil {
			mt := *s.ManualTask
			ns.ManualTask = &mt
		}
		o[i] = ns
	}
	return o
}

// normalizedCopy is the EXACT historical canonical shape-walk (8350e5a),
// restored verbatim (PR5 second-attempt gate blocker 2). It does NOT delegate
// to Clone because the canonical contract is byte-for-byte stable for
// INCOMPLETE drafts: notably a non-nil empty Form.Fields must canonicalize as
// "fields":null (built with nil-append), never "fields":[] (what Clone's
// nil-preserving copy would emit). Every string is trimmed (type, strategy,
// actor, field key/label/kind, instructions) and options are trimmed only for
// single_select keyed on the pre-trim kind, in the historical order. The
// receiver is never mutated.
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
