package httpadapter

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// CategoryWorkflowHandlers owns the closed builder HTTP surface. It mutates the
// submitted draft through a closed server-side action dispatch before delegating
// persistence to WorkflowService, and renders real editable per-step controls
// (no JS-dependent hidden JSON round-trip).
type CategoryWorkflowHandlers struct {
	categories *application.CategoryService
	workflows  *application.WorkflowService
	desks      *application.DeskService
	renderer   *Renderer
}

func NewCategoryWorkflowHandlers(categories *application.CategoryService, workflows *application.WorkflowService, desks *application.DeskService, renderer *Renderer) *CategoryWorkflowHandlers {
	return &CategoryWorkflowHandlers{categories: categories, workflows: workflows, desks: desks, renderer: renderer}
}

func (h *CategoryWorkflowHandlers) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /categories/{id}/workflow", h.get)
	mux.HandleFunc("POST /categories/{id}/workflow", h.post)
}

type workflowStepView struct {
	Index    int
	Position int
	Summary  string
	Snapshot string
	Step     domain.WorkflowStep
	Selected bool
	Final    bool
	Last     bool
}
type workflowBuilderData struct {
	pageData
	CategoryID           int64
	CategoryName         string
	Draft                domain.WorkflowDefinition
	Steps                []workflowStepView
	SelectedStepIndex    int
	SelectedStepPosition int
	HasSelection         bool
	Desks                []domain.Desk
	Issues               []domain.WorkflowValidationIssue
	Live                 string
	FocusStep            int
	AddStepAllowed       bool
}

func (h *CategoryWorkflowHandlers) get(w http.ResponseWriter, r *http.Request) {
	if !requireCapability(w, r, application.CapManageCategories) {
		return
	}
	categoryID, ok := categoryID(r)
	if !ok {
		http.Error(w, "invalid category id", http.StatusBadRequest)
		return
	}
	draft, err := h.workflows.GetForBuilder(r.Context(), *userFromContext(r.Context()), categoryID)
	if err != nil {
		http.Error(w, mapErrorMsg(err), statusFor(err))
		return
	}
	desks := h.deskOptions(r)
	h.render(w, r, categoryID, draft, desks, nil, "", selectedStepIndex(r, len(draft)), http.StatusOK)
}

func (h *CategoryWorkflowHandlers) post(w http.ResponseWriter, r *http.Request) {
	if !requireCapability(w, r, application.CapManageCategories) {
		return
	}
	categoryID, ok := categoryID(r)
	if !ok {
		http.Error(w, "invalid category id", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	draft, issues := parseBuilderDraft(r)
	selection := selectedStepIndex(r, len(draft))
	if len(issues) > 0 {
		h.render(w, r, categoryID, draft, h.deskOptions(r), issues, "", selection, http.StatusUnprocessableEntity)
		return
	}

	actor := *userFromContext(r.Context())
	desks := h.deskOptions(r)
	action := r.Form.Get("action")
	switch action {
	case "select_step":
		target, err := strconv.Atoi(r.Form.Get("selection_step_index"))
		if err != nil || target < 0 || target >= len(draft) {
			target = selection
		}
		h.render(w, r, categoryID, draft, desks, nil, "", target, http.StatusOK)
	case "save", "change_type":
		// save persists the reconstructed draft as-is; change_type is already
		// reflected in the reconstructed step payloads (selected closed payload
		// initialized, incompatible payloads dropped).
		if err := h.workflows.SaveDraft(r.Context(), actor, categoryID, draft); err != nil {
			http.Error(w, mapErrorMsg(err), statusFor(err))
			return
		}
		h.afterMutation(w, r, categoryID, draft, desks, nil, defaultLive(), -1)
	case "add_step":
		if hasTerminalStep(draft) {
			// A terminal step must stay final: the only insertion position (append)
			// would follow it, so the builder hides Add step and the server keeps
			// the draft unchanged against crafted requests.
			if err := h.workflows.SaveDraft(r.Context(), actor, categoryID, draft); err != nil {
				http.Error(w, mapErrorMsg(err), statusFor(err))
				return
			}
			h.afterMutation(w, r, categoryID, draft, desks, nil, "The final step ends the workflow.", -1)
			break
		}
		step := typedAddStep(draft, r.Form.Get("add_step_type"))
		if err := h.workflows.AddStep(r.Context(), actor, categoryID, draft, step); err != nil {
			http.Error(w, mapErrorMsg(err), statusFor(err))
			return
		}
		result := append(append(domain.WorkflowDefinition(nil), draft...), step)
		focus := len(result) - 1
		h.afterMutation(w, r, categoryID, result, desks, nil, "Added a step.", focus)
	case "move_up":
		result, focus, mv := localMoveUp(draft, r)
		if mv {
			if err := h.workflows.MoveUp(r.Context(), actor, categoryID, draft, focus+1); err != nil {
				http.Error(w, mapErrorMsg(err), statusFor(err))
				return
			}
		} else if err := h.workflows.SaveDraft(r.Context(), actor, categoryID, draft); err != nil {
			http.Error(w, mapErrorMsg(err), statusFor(err))
			return
		}
		h.afterMutation(w, r, categoryID, result, desks, nil, focusLive(focus, len(result)), focus)
	case "move_down":
		result, focus, mv := localMoveDown(draft, r)
		if mv {
			if err := h.workflows.SaveDraft(r.Context(), actor, categoryID, result); err != nil {
				http.Error(w, mapErrorMsg(err), statusFor(err))
				return
			}
		} else if err := h.workflows.SaveDraft(r.Context(), actor, categoryID, draft); err != nil {
			http.Error(w, mapErrorMsg(err), statusFor(err))
			return
		}
		h.afterMutation(w, r, categoryID, result, desks, nil, focusLive(focus, len(result)), focus)
	case "remove_step":
		result, focus, rm := localRemoveStep(draft, r)
		if rm {
			if err := h.workflows.RemoveStep(r.Context(), actor, categoryID, draft, focus); err != nil {
				http.Error(w, mapErrorMsg(err), statusFor(err))
				return
			}
		} else if err := h.workflows.SaveDraft(r.Context(), actor, categoryID, draft); err != nil {
			http.Error(w, mapErrorMsg(err), statusFor(err))
			return
		}
		h.afterMutation(w, r, categoryID, result, desks, nil, defaultLive(), selectionAfterRemove(focus, len(result)))
	case "reorder":
		result, movedTo, err := localReorder(draft, r)
		if err != nil {
			h.render(w, r, categoryID, draft, desks, []domain.WorkflowValidationIssue{{Step: 1, Field: "steps", Message: err.Error()}}, "", selection, http.StatusUnprocessableEntity)
			return
		}
		if err := h.workflows.SaveDraft(r.Context(), actor, categoryID, result); err != nil {
			http.Error(w, mapErrorMsg(err), statusFor(err))
			return
		}
		h.afterMutation(w, r, categoryID, result, desks, nil, focusLive(movedTo, len(result)), movedTo)
	case "add_field":
		result, idx := localAddField(draft, r)
		if err := h.workflows.SaveDraft(r.Context(), actor, categoryID, result); err != nil {
			http.Error(w, mapErrorMsg(err), statusFor(err))
			return
		}
		h.afterMutation(w, r, categoryID, result, desks, nil, "Added a form field.", idx)
	case "remove_field":
		result, idx := localRemoveField(draft, r)
		if err := h.workflows.SaveDraft(r.Context(), actor, categoryID, result); err != nil {
			http.Error(w, mapErrorMsg(err), statusFor(err))
			return
		}
		h.afterMutation(w, r, categoryID, result, desks, nil, "Removed a form field.", idx)
	case "preview":
		preview, previewIssues, err := h.workflows.Preview(r.Context(), actor, categoryID, draft)
		if err != nil {
			http.Error(w, mapErrorMsg(err), statusFor(err))
			return
		}
		status := http.StatusOK
		if len(previewIssues) > 0 {
			status = http.StatusUnprocessableEntity
		}
		h.render(w, r, categoryID, preview, desks, previewIssues, "", selectedStepIndex(r, len(preview)), status)
	case "publish":
		publishIssues, err := h.workflows.Publish(r.Context(), actor, categoryID, draft)
		if err != nil {
			http.Error(w, mapErrorMsg(err), statusFor(err))
			return
		}
		if len(publishIssues) > 0 {
			h.render(w, r, categoryID, draft, desks, publishIssues, "", selection, http.StatusUnprocessableEntity)
			return
		}
		if r.Header.Get("HX-Request") == "" {
			redirect(w, r, workflowLocation(r, selection))
			return
		}
		h.render(w, r, categoryID, draft, desks, nil, "", selection, http.StatusOK)
	default:
		h.render(w, r, categoryID, draft, desks, []domain.WorkflowValidationIssue{{Step: 1, Field: "action", Message: "unknown workflow action"}}, "", selection, http.StatusUnprocessableEntity)
	}
}

// afterMutation persists a successful mutation: full-page requests redirect so
// the GET re-reads the persisted draft; HTMX requests swap the rebuilt fragment.
func (h *CategoryWorkflowHandlers) afterMutation(w http.ResponseWriter, r *http.Request, categoryID int64, draft domain.WorkflowDefinition, desks []domain.Desk, issues []domain.WorkflowValidationIssue, live string, focus int) {
	if focus < 0 {
		focus = selectedStepIndex(r, len(draft))
	}
	if r.Header.Get("HX-Request") == "" {
		redirect(w, r, workflowLocation(r, focus))
		return
	}
	h.render(w, r, categoryID, draft, desks, issues, live, focus, http.StatusOK)
}

func workflowLocation(r *http.Request, selection int) string {
	if selection < 0 || r.FormValue("selected_step_index") == "" {
		return r.URL.Path
	}
	return r.URL.Path + "?selected_step_index=" + strconv.Itoa(selection)
}
func selectedStepIndex(r *http.Request, total int) int {
	if total == 0 {
		return -1
	}
	raw := r.FormValue("selected_step_index")
	if raw == "" {
		raw = r.URL.Query().Get("selected_step_index")
	}
	index, err := strconv.Atoi(raw)
	if err != nil || index < 0 || index >= total {
		return 0
	}
	return index
}

func selectionAfterRemove(previous, total int) int {
	if total == 0 {
		return -1
	}
	if previous < 0 || previous >= total {
		return 0
	}
	return previous
}
func (h *CategoryWorkflowHandlers) deskOptions(r *http.Request) []domain.Desk {
	desks, err := h.desks.List(r.Context(), *userFromContext(r.Context()))
	if err != nil {
		return nil
	}
	return desks
}

func (h *CategoryWorkflowHandlers) render(w http.ResponseWriter, r *http.Request, categoryID int64, draft domain.WorkflowDefinition, desks []domain.Desk, issues []domain.WorkflowValidationIssue, live string, focus int, status int) {
	category, err := h.categories.GetByID(r.Context(), categoryID)
	if err != nil {
		http.Error(w, mapErrorMsg(err), statusFor(err))
		return
	}
	selection := focus
	if selection < 0 {
		selection = selectedStepIndex(r, len(draft))
	}
	steps := workflowStepViews(draft, selection)
	data := workflowBuilderData{
		pageData:          pageDataFrom(r, "categories"),
		CategoryID:        categoryID,
		CategoryName:      category.Name,
		Draft:             draft,
		Steps:             steps,
		SelectedStepIndex: selection, SelectedStepPosition: selection + 1,
		HasSelection: selection >= 0 && selection < len(steps),
		Desks:        desks,
		Issues:       issues,
		Live:         live, FocusStep: focus,
		AddStepAllowed: !hasTerminalStep(draft),
	}
	data.PageFoundationAssets = true
	data.WorkflowAssets = true
	h.renderer.Render(w, r, "category_workflow", "workflow_builder", data, status)
}

func workflowStepViews(draft domain.WorkflowDefinition, selected int) []workflowStepView {
	views := make([]workflowStepView, 0, len(draft))
	for i, step := range draft {
		raw, _ := json.Marshal(step)
		views = append(views, workflowStepView{Index: i, Position: i + 1, Summary: workflowStepSummary(step), Snapshot: string(raw), Step: step, Selected: i == selected, Final: isTerminalStep(step.Type) && i == len(draft)-1, Last: i == len(draft)-1})
	}
	return views
}

func isTerminalStep(typ domain.StepType) bool {
	return typ == domain.StepResolve || typ == domain.StepClose
}
func workflowStepSummary(step domain.WorkflowStep) string {
	switch step.Type {
	case domain.StepAssignToDesk:
		if step.AssignToDesk != nil && step.AssignToDesk.DeskID > 0 {
			return fmt.Sprintf("Desk %d", step.AssignToDesk.DeskID)
		}
		return "Choose a desk"
	case domain.StepForm:
		if step.Form == nil || len(step.Form.Fields) == 0 {
			return "No fields yet"
		}
		suffix := "s"
		if len(step.Form.Fields) == 1 {
			suffix = ""
		}
		return fmt.Sprintf("%d field%s · %s", len(step.Form.Fields), suffix, step.Form.Actor)
	case domain.StepManualTask:
		if step.ManualTask == nil || strings.TrimSpace(step.ManualTask.Instructions) == "" {
			return "Add instructions"
		}
		text := strings.Join(strings.Fields(step.ManualTask.Instructions), " ")
		runes := []rune(text)
		if len(runes) > 44 {
			return string(runes[:41]) + "..."
		}
		return text
	case domain.StepResolve, domain.StepClose:
		return "Runs automatically"
	default:
		return "Configure this step"
	}
}

func defaultLive() string { return "Use Up and Down to change a step position." }
func focusLive(pos, total int) string {
	return fmt.Sprintf("Step %d of %d.", pos+1, total)
}

// defaultStep is the step a fresh "Add step" appends: an editable manual task.
func defaultStep() domain.WorkflowStep {
	return domain.WorkflowStep{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{}}
}

// typedAddStep builds the step appended by the add_step action from the optional
// presentation-only add_step_type input. The value is strictly validated against
// the closed domain set: absent, empty, or unknown values preserve the existing
// default manual-step behavior, and each accepted type gets its closed payload.
func typedAddStep(d domain.WorkflowDefinition, raw string) domain.WorkflowStep {
	switch typ := domain.StepType(strings.TrimSpace(raw)); typ {
	case domain.StepAssignToDesk:
		return domain.WorkflowStep{Type: domain.StepAssignToDesk, AssignToDesk: &domain.AssignToDeskStep{}}
	case domain.StepForm:
		return domain.WorkflowStep{Type: domain.StepForm, Form: &domain.FormStep{Actor: domain.FormActorRequester}}
	case domain.StepManualTask:
		return defaultStep()
	case domain.StepResolve, domain.StepClose:
		if hasTerminalStep(d) {
			return defaultStep()
		}
		return domain.WorkflowStep{Type: typ}
	default:
		return defaultStep()
	}
}

// hasTerminalStep reports whether the draft already contains a terminal step, in
// which case no further step may follow it (final and mutually exclusive).
func hasTerminalStep(d domain.WorkflowDefinition) bool {
	for _, s := range d {
		if isTerminalStep(s.Type) {
			return true
		}
	}
	return false
}

// strictBuilderIndex rejects any non-canonical or negative index so crafted
// reorder requests fail closed instead of silently defaulting.
var strictBuilderIndex = regexp.MustCompile(`^(0|[1-9][0-9]*)$`)

// strictFormIndex reads key as a single canonical non-negative numeric index.
func strictFormIndex(r *http.Request, key string) (int, error) {
	values, ok := r.Form[key]
	if !ok || len(values) != 1 || !strictBuilderIndex.MatchString(values[0]) {
		return 0, fmt.Errorf("%s must be one non-negative numeric index", key)
	}
	i, err := strconv.Atoi(values[0])
	if err != nil {
		return 0, fmt.Errorf("%s is out of range", key)
	}
	return i, nil
}

func formIndex(r *http.Request, key string) (int, bool) {
	v := r.Form.Get(key)
	if v == "" {
		return 0, false
	}
	i, err := strconv.Atoi(v)
	if err != nil || i < 0 {
		return 0, false
	}
	return i, true
}

func localMoveUp(d domain.WorkflowDefinition, r *http.Request) (result domain.WorkflowDefinition, newPos int, moved bool) {
	i, ok := formIndex(r, "step_index")
	if !ok || i <= 0 || i >= len(d) {
		return d, 0, false
	}
	nd := d.Clone()
	nd[i], nd[i-1] = nd[i-1], nd[i]
	return nd, i - 1, true
}

// localReorder applies a drag reorder: the step at source_index is moved so it
// lands at target_index in the resulting order. A terminal step must stay final
// and mutually exclusive, so nothing may be moved from or into the terminal's
// position or beyond; crafted violations fail closed with an inline error and
// the persisted draft is left unchanged (the caller saves only on success).
func localReorder(d domain.WorkflowDefinition, r *http.Request) (domain.WorkflowDefinition, int, error) {
	source, err := strictFormIndex(r, "source_index")
	if err != nil {
		return d, 0, err
	}
	target, err := strictFormIndex(r, "target_index")
	if err != nil {
		return d, 0, err
	}
	if source >= len(d) || target >= len(d) {
		return d, 0, fmt.Errorf("reorder indexes are out of range")
	}
	terminal := len(d)
	for i, s := range d {
		if isTerminalStep(s.Type) {
			terminal = i
			break
		}
	}
	if source >= terminal {
		return d, 0, fmt.Errorf("steps must remain before the final step")
	}
	if target >= terminal {
		return d, 0, fmt.Errorf("steps must remain before the final step")
	}
	if source == target {
		return d, target, nil
	}
	result := d.Clone()
	step := result[source]
	result = append(result[:source], result[source+1:]...)
	result = append(result, domain.WorkflowStep{})
	copy(result[target+1:], result[target:])
	result[target] = step
	return result, target, nil
}

func localMoveDown(d domain.WorkflowDefinition, r *http.Request) (result domain.WorkflowDefinition, newPos int, moved bool) {
	i, ok := formIndex(r, "step_index")
	if !ok || i < 0 || i+1 >= len(d) {
		return d, 0, false
	}
	nd := d.Clone()
	nd[i], nd[i+1] = nd[i+1], nd[i]
	return nd, i + 1, true
}

func localRemoveStep(d domain.WorkflowDefinition, r *http.Request) (result domain.WorkflowDefinition, removedIdx int, removed bool) {
	i, ok := formIndex(r, "step_index")
	if !ok || i < 0 || i >= len(d) || (i == len(d)-1 && isTerminalStep(d[i].Type)) {
		return d, 0, false
	}
	nd := append(domain.WorkflowDefinition(nil), d[:i]...)
	nd = append(nd, d[i+1:]...)
	return nd, i, true
}

func localAddField(d domain.WorkflowDefinition, r *http.Request) (result domain.WorkflowDefinition, idx int) {
	i, ok := formIndex(r, "step_index")
	if !ok || i < 0 || i >= len(d) || d[i].Form == nil {
		return d, -1
	}
	nd := d.Clone()
	nd[i].Form.Fields = append(nd[i].Form.Fields, domain.FormField{Key: nextFieldKey(nd)})
	return nd, i
}

func localRemoveField(d domain.WorkflowDefinition, r *http.Request) (result domain.WorkflowDefinition, idx int) {
	i, ok := formIndex(r, "step_index")
	f, fok := formIndex(r, "field_index")
	if !ok || !fok || i < 0 || i >= len(d) || d[i].Form == nil || f < 0 || f >= len(d[i].Form.Fields) {
		return d, -1
	}
	nd := d.Clone()
	nd[i].Form.Fields = append(nd[i].Form.Fields[:f], nd[i].Form.Fields[f+1:]...)
	return nd, i
}

var builderStepPosition = regexp.MustCompile(`^step_(\d+)$`)

// parseBuilderDraft reconstructs the complete ordered draft from the submitted
// form. Three representations are accepted, all sharing the same strict
// guarantees: (1) one canonical JSON `draft` document; (2) a legacy positional
// set of individual step JSON values whose numeric positions must be complete
// and unique; (3) the real editable per-step controls (step_<i>_type, ...).
func parseBuilderDraft(r *http.Request) (domain.WorkflowDefinition, []domain.WorkflowValidationIssue) {
	if values, ok := r.Form["draft"]; ok {
		if len(values) != 1 {
			return nil, builderIssue("draft must be submitted exactly once")
		}
		draft, err := domain.ParseWorkflowDefinition([]byte(values[0]))
		if err != nil {
			return nil, builderIssue(err.Error())
		}
		return ensureFieldKeys(draft), nil
	}
	if hasBareStepJSON(r) {
		draft, issues := parsePositionalJSONDraft(r)
		return ensureFieldKeys(draft), issues
	}
	draft, issues := draftFromFields(r)
	return ensureFieldKeys(draft), issues
}

// nextFieldKey returns the smallest deterministic field_N key not already used
// by any Form field in d. Keys are opaque sequence numbers (field_1, field_2,
// ...) rather than label slugs because labels may change or collide; removing a
// field frees its number for the next deterministic reuse.
func nextFieldKey(d domain.WorkflowDefinition) string {
	used := map[string]bool{}
	for _, s := range d {
		if s.Form == nil {
			continue
		}
		for _, f := range s.Form.Fields {
			if k := strings.TrimSpace(f.Key); k != "" {
				used[k] = true
			}
		}
	}
	for n := 1; ; n++ {
		if k := fmt.Sprintf("field_%d", n); !used[k] {
			return k
		}
	}
}

// ensureFieldKeys assigns a deterministic unique field_N key to every Form
// field that lacks one, preserving all existing stable keys (never rewriting
// compatibility data). It returns a cloned definition and is a no-op for drafts
// whose fields all have keys, so old/incomplete drafts stay editable.
func ensureFieldKeys(d domain.WorkflowDefinition) domain.WorkflowDefinition {
	nd := d.Clone()
	for _, s := range nd {
		if s.Form == nil {
			continue
		}
		for j := range s.Form.Fields {
			if strings.TrimSpace(s.Form.Fields[j].Key) == "" {
				s.Form.Fields[j].Key = nextFieldKey(nd)
			}
		}
	}
	return nd
}

// hasBareStepJSON reports whether any submitted key is the legacy bare
// `step_<N>` positional JSON carrier (not the editable step_<N>_* controls).
func hasBareStepJSON(r *http.Request) bool {
	for key := range r.Form {
		if builderStepPosition.MatchString(key) {
			return true
		}
	}
	return false
}

// parsePositionalJSONDraft preserves the PR8 positional completeness contract:
// positions are checked before constructing the array so omissions and
// duplicates cannot silently reorder a draft. Domain parsing then rejects
// unknown JSON fields.
func parsePositionalJSONDraft(r *http.Request) (domain.WorkflowDefinition, []domain.WorkflowValidationIssue) {
	type submittedStep struct {
		position int
		value    string
	}
	steps := make([]submittedStep, 0)
	for key, values := range r.Form {
		if len(key) < len("step_") || key[:len("step_")] != "step_" {
			continue
		}
		match := builderStepPosition.FindStringSubmatch(key)
		if match == nil || len(values) != 1 {
			return nil, builderIssue("workflow step positions must be unique numeric values")
		}
		position, err := strconv.Atoi(match[1])
		if err != nil || position < 0 {
			return nil, builderIssue("workflow step positions must be non-negative numeric values")
		}
		steps = append(steps, submittedStep{position: position, value: values[0]})
	}
	if len(steps) == 0 {
		return domain.WorkflowDefinition{}, nil
	}
	sort.Slice(steps, func(i, j int) bool { return steps[i].position < steps[j].position })
	draft := make(domain.WorkflowDefinition, len(steps))
	for i, step := range steps {
		if step.position != i {
			return nil, builderIssue("workflow step positions must be complete and ordered")
		}
		one, err := domain.ParseWorkflowDefinition([]byte("[" + step.value + "]"))
		if err != nil || len(one) != 1 {
			return nil, builderIssue(fmt.Sprintf("Step %d: invalid step", i+1))
		}
		draft[i] = one[0]
	}
	return draft, nil
}

// draftFromFields reconstructs the draft from the real editable per-step
// controls. Step count is derived from consecutive step_<i>_type values; form
// fields from consecutive step_<i>_field_<j>_* keys. Incomplete drafts are kept
// (publish validation remains authoritative).
func draftFromFields(r *http.Request) (domain.WorkflowDefinition, []domain.WorkflowValidationIssue) {
	var d domain.WorkflowDefinition
	for i := 0; ; i++ {
		pi := strconv.Itoa(i)
		typ := r.Form.Get("step_" + pi + "_type")
		snapshotValues, hasSnapshot := r.Form["step_"+pi+"_snapshot"]
		if typ == "" && !hasSnapshot {
			break
		}
		if typ == "" {
			if len(snapshotValues) != 1 {
				return nil, builderIssue(fmt.Sprintf("Step %d: invalid step snapshot", i+1))
			}
			one, err := domain.ParseWorkflowDefinition([]byte("[" + snapshotValues[0] + "]"))
			if err != nil || len(one) != 1 {
				return nil, builderIssue(fmt.Sprintf("Step %d: invalid step snapshot", i+1))
			}
			d = append(d, one[0])
			continue
		}
		s := domain.WorkflowStep{Type: domain.StepType(typ)}
		switch s.Type {
		case domain.StepManualTask:
			s.ManualTask = &domain.ManualTaskStep{Instructions: r.Form.Get("step_" + pi + "_instructions")}
		case domain.StepAssignToDesk:
			deskID, _ := strconv.ParseInt(r.Form.Get("step_"+pi+"_desk"), 10, 64)
			s.AssignToDesk = &domain.AssignToDeskStep{DeskID: deskID, Strategy: domain.AssignmentStrategy(r.Form.Get("step_" + pi + "_strategy"))}
		case domain.StepForm:
			fs := &domain.FormStep{Actor: domain.FormActor(r.Form.Get("step_" + pi + "_actor"))}
			for j := 0; ; j++ {
				fj := strconv.Itoa(j)
				base := "step_" + pi + "_field_" + fj
				if !r.Form.Has(base+"_key") && !r.Form.Has(base+"_label") && !r.Form.Has(base+"_kind") {
					break
				}
				fs.Fields = append(fs.Fields, domain.FormField{
					Key:      r.Form.Get(base + "_key"),
					Label:    r.Form.Get(base + "_label"),
					Kind:     domain.FieldKind(r.Form.Get(base + "_kind")),
					Required: r.Form.Has(base + "_required"),
					Options:  splitOptions(r.Form.Get(base + "_options")),
				})
			}
			s.Form = fs
		case domain.StepResolve, domain.StepClose:
			// terminal steps carry no context.
		}
		d = append(d, s)
	}
	return d, nil
}

// splitOptions parses the semicolon-separated single_select options input.
func splitOptions(v string) []string {
	if v == "" {
		return nil
	}
	var out []string
	for _, o := range strings.Split(v, ";") {
		if o = strings.TrimSpace(o); o != "" {
			out = append(out, o)
		}
	}
	return out
}

func builderIssue(message string) []domain.WorkflowValidationIssue {
	return []domain.WorkflowValidationIssue{{Step: 1, Field: "steps", Message: "Step 1: " + message}}
}
