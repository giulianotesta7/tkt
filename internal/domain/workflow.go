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
	// Reason is the same finding WITHOUT the step prefix, for a surface that names
	// the step itself: a badge, a table row, a summary line. Message is what the
	// builder shows beside a step; Reason is what a one-line label can carry.
	Reason string
	// ReasonKey and ReasonArgs name the same one-line Reason as structured data
	// for the presentation layer, exactly as Key/Args do for Message: a badged
	// surface follows the instance language while the domain keeps the English
	// Reason as its source. An issue built without a ReasonKey renders its
	// Reason verbatim.
	ReasonKey  string
	ReasonArgs []any
	// Key and Args name the same finding as structured data: Key is a catalogue
	// entry every shipped locale carries and Args are the values its sentence
	// interpolates (a step position, a field key, a desk name). The English
	// Message stays the domain's single source of the wording, and the
	// presentation layer resolves the key through the active instance language;
	// an issue built without a key renders its Message verbatim (issue #335, S8).
	Key  string
	Args []any
}

// issueAdder records one validation finding. key names the catalogue entry for
// the same finding (empty when the message is internal detail); message is the
// English wording; args interpolate the localized sentence.
type issueAdder func(step int, field, key, message string, args ...any)

func (d WorkflowDefinition) Validate() []WorkflowValidationIssue {
	var issues []WorkflowValidationIssue
	add := issueAdder(func(s int, f, key, m string, args ...any) {
		issues = append(issues, WorkflowValidationIssue{Step: s, Field: f, Message: m, Key: key, Args: args})
	})
	// addKeyed records a fixed finding that has a catalogue key: the English
	// message stays the domain's wording and the key lets a localized surface
	// render the same finding.
	addKeyed := func(s int, f, key, m string) {
		issues = append(issues, WorkflowValidationIssue{Step: s, Field: f, Message: m, Key: key})
	}
	if len(d) == 0 {
		addKeyed(1, "steps", "error.workflow_min_steps", "workflow must have at least one step")
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
		addKeyed(tp+1, "type", "error.workflow_max_terminal", "workflow must have at most one terminal step")
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
		addKeyed(tp+1, "type", "error.workflow_terminal_exclusive", "resolve_ticket and close_ticket are mutually exclusive")
	}
	if tc == 1 && tp != len(d)-1 {
		addKeyed(tp+1, "type", "error.workflow_terminal_last", "terminal step must be the final step")
	}
	seen := map[string]int{}
	for i, s := range d {
		n := i + 1
		switch s.Type {
		case StepAssignToDesk, StepForm, StepManualTask, StepResolve, StepClose:
		default:
			add(n, "type", "error.workflow.unknown_step_type", fmt.Sprintf("Step %d: unknown step type %q", n, s.Type), n, string(s.Type))
			continue
		}
		switch s.Type {
		case StepAssignToDesk:
			if s.AssignToDesk == nil || s.Form != nil || s.ManualTask != nil {
				add(n, "type", "error.workflow.assign_config", fmt.Sprintf("Step %d: assign_to_desk requires exactly assign_to_desk config", n), n)
				continue
			}
		case StepForm:
			if s.Form == nil || s.AssignToDesk != nil || s.ManualTask != nil {
				add(n, "type", "error.workflow.form_config", fmt.Sprintf("Step %d: form requires exactly form config", n), n)
				continue
			}
		case StepManualTask:
			if s.ManualTask == nil || s.AssignToDesk != nil || s.Form != nil {
				add(n, "type", "error.workflow.manual_config", fmt.Sprintf("Step %d: manual_task requires exactly manual_task config", n), n)
				continue
			}
		case StepResolve, StepClose:
			if s.AssignToDesk != nil || s.Form != nil || s.ManualTask != nil {
				add(n, "type", "error.workflow.terminal_config", fmt.Sprintf("Step %d: terminal step must have no config", n), n)
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
	block := func(step int, field, key, msg, reasonKey, reason string, reasonArgs []any, args ...any) {
		a.Blockers = append(a.Blockers, WorkflowValidationIssue{
			Step: step, Field: field, Message: msg, Key: key, Args: args,
			Reason: reason, ReasonKey: reasonKey, ReasonArgs: reasonArgs})
	}
	warn := func(step int, field, key, msg, reasonKey, reason string, reasonArgs []any, args ...any) {
		a.Warnings = append(a.Warnings, WorkflowValidationIssue{
			Step: step, Field: field, Message: msg, Key: key, Args: args,
			Reason: reason, ReasonKey: reasonKey, ReasonArgs: reasonArgs})
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
				block(n, "desk_id", "error.workflow.choose_desk",
					fmt.Sprintf("Step %d: choose a desk", n),
					"error.workflow_reason.choose_desk", "choose a desk",
					nil,
					n)
				continue
			}
			if members == 0 {
				block(n, "desk_id", "error.workflow.no_members",
					fmt.Sprintf(
						"Step %d: nobody can take it — %s has no active members. Add someone to %s, or choose another desk.",
						n, name, name),
					"error.workflow_reason.no_members",
					fmt.Sprintf("%s has no active members", name),
					[]any{name},
					n, name, name)
			}
		case StepManualTask:
			if !assigned {
				warn(n, "type", "error.workflow.needs_assignment",
					fmt.Sprintf(
						"Step %d: every ticket waits here until someone assigns it — no earlier step puts a person on it.", n),
					"error.workflow_reason.needs_assignment", "needs someone assigned first",
					nil,
					n)
			}
		case StepForm:
			if s.Form != nil && s.Form.Actor == FormActorAssignee && !assigned {
				warn(n, "actor", "error.workflow.needs_assignment",
					fmt.Sprintf(
						"Step %d: every ticket waits here until someone assigns it — no earlier step puts a person on it.", n),
					"error.workflow_reason.needs_assignment", "needs someone assigned first",
					nil,
					n)
			}
		case StepResolve, StepClose:
			// A terminal ends the run, so nothing after it is ever reached.
			return a
		}
	}
	return a
}

// CountChanges reports how many STEPS differ between two definitions, comparing
// canonical bytes by position and counting a size difference by its magnitude.
//
// The unit is deliberately one step rather than one leaf field: it is what a
// summary line can state honestly ("1 unpublished change") instead of pretending
// a byte diff is a meaningful business quantity. Two definitions that differ only
// in whitespace canonicalize the same way and count as no change at all.
func CountChanges(a, b WorkflowDefinition) int {
	ca, errA := a.MarshalCanonical()
	cb, errB := b.MarshalCanonical()
	if errA != nil || errB != nil {
		// Unmarshalable definitions cannot be compared. Report a difference so a
		// caller never reads "no changes" out of two unknown documents.
		return 1
	}
	var da, db []json.RawMessage
	if len(ca) > 0 {
		_ = json.Unmarshal(ca, &da)
	}
	if len(cb) > 0 {
		_ = json.Unmarshal(cb, &db)
	}
	n := 0
	if len(da) > len(db) {
		n += len(da) - len(db)
	}
	if len(db) > len(da) {
		n += len(db) - len(da)
	}
	for i := 0; i < len(da) && i < len(db); i++ {
		if !bytes.Equal(da[i], db[i]) {
			n++
		}
	}
	return n
}

func validateAssign(n int, ad *AssignToDeskStep, add issueAdder) {
	if ad.DeskID <= 0 {
		add(n, "desk_id", "error.workflow.choose_desk", fmt.Sprintf("Step %d: choose a desk", n), n)
	}
	if ad.Strategy != StrategyClaim && ad.Strategy != StrategyLeastLoaded {
		add(n, "strategy", "error.workflow.strategy", fmt.Sprintf("Step %d: strategy must be claim or least_loaded", n), n)
	}
}
func validateForm(n int, f *FormStep, seen map[string]int, add issueAdder) {
	a := FormActor(strings.TrimSpace(string(f.Actor)))
	if a != FormActorRequester && a != FormActorAssignee {
		add(n, "actor", "error.workflow.form_actor", fmt.Sprintf("Step %d: form actor must be requester or assignee", n), n)
	}
	if len(f.Fields) == 0 {
		add(n, "fields", "error.workflow.form_fields", fmt.Sprintf("Step %d: form must have at least one field", n), n)
	}
	for _, fld := range f.Fields {
		k, l := strings.TrimSpace(fld.Key), strings.TrimSpace(fld.Label)
		if k == "" {
			add(n, "key", "error.workflow.field_key_required", fmt.Sprintf("Step %d: field key is required", n), n)
		}
		if l == "" {
			add(n, "label", "error.workflow.field_label_required", fmt.Sprintf("Step %d: field label is required", n), n)
		}
		if k != "" {
			if p, ok := seen[k]; ok {
				add(n, "key", "error.workflow.duplicate_field_key", fmt.Sprintf("Step %d: duplicate field key %q (also in step %d)", n, k, p), n, k, p)
			} else {
				seen[k] = n
			}
		}
		switch fld.Kind {
		case FieldShortText, FieldLongText, FieldCheckbox, FieldSingleSelect:
		default:
			add(n, "kind", "error.workflow.unknown_field_kind", fmt.Sprintf("Step %d: unknown field kind %q", n, fld.Kind), n, string(fld.Kind))
			continue
		}
		if fld.Kind != FieldSingleSelect && len(fld.Options) > 0 {
			add(n, "options", "error.workflow.options_not_allowed", fmt.Sprintf("Step %d: options are only allowed for single_select", n), n)
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
				add(n, "options", "error.workflow.select_options", fmt.Sprintf("Step %d: single_select requires at least two options", n), n)
			} else {
				m := map[string]bool{}
				for _, o := range ne {
					if m[o] {
						add(n, "options", "error.workflow.duplicate_option", fmt.Sprintf("Step %d: duplicate option %q", n, o), n, o)
						break
					}
					m[o] = true
				}
				for _, o := range tr {
					if o == "" {
						add(n, "options", "error.workflow.empty_option", fmt.Sprintf("Step %d: option must not be empty", n), n)
						break
					}
				}
			}
		}
	}
}
func validateManual(n int, m *ManualTaskStep, add issueAdder) {
	if strings.TrimSpace(m.Instructions) == "" {
		add(n, "instructions", "error.workflow.instructions_required", fmt.Sprintf("Step %d: instructions are required", n), n)
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
