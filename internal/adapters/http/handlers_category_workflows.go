package httpadapter

import (
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

type workflowBuilderData struct {
	pageData
	CategoryID   int64
	CategoryName string
	Draft        domain.WorkflowDefinition
	Desks        []domain.Desk
	Issues       []domain.WorkflowValidationIssue
	Preview      bool
	Live         string
	FocusStep    int
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
	h.render(w, r, categoryID, draft, desks, nil, false, "", -1, http.StatusOK)
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
	if len(issues) > 0 {
		h.render(w, r, categoryID, draft, h.deskOptions(r), issues, false, "", -1, http.StatusUnprocessableEntity)
		return
	}

	actor := *userFromContext(r.Context())
	desks := h.deskOptions(r)
	action := r.Form.Get("action")
	switch action {
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
		step := defaultStep()
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
		h.afterMutation(w, r, categoryID, result, desks, nil, defaultLive(), -1)
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
		h.render(w, r, categoryID, preview, desks, previewIssues, true, "", -1, status)
	case "publish":
		publishIssues, err := h.workflows.Publish(r.Context(), actor, categoryID, draft)
		if err != nil {
			http.Error(w, mapErrorMsg(err), statusFor(err))
			return
		}
		if len(publishIssues) > 0 {
			h.render(w, r, categoryID, draft, desks, publishIssues, false, "", -1, http.StatusUnprocessableEntity)
			return
		}
		if r.Header.Get("HX-Request") == "" {
			redirect(w, r, r.URL.Path)
			return
		}
		h.render(w, r, categoryID, draft, desks, nil, false, "", -1, http.StatusOK)
	default:
		h.render(w, r, categoryID, draft, desks, []domain.WorkflowValidationIssue{{Step: 1, Field: "action", Message: "unknown workflow action"}}, false, "", -1, http.StatusUnprocessableEntity)
	}
}

// afterMutation persists a successful mutation: full-page requests redirect so
// the GET re-reads the persisted draft; HTMX requests swap the rebuilt fragment.
func (h *CategoryWorkflowHandlers) afterMutation(w http.ResponseWriter, r *http.Request, categoryID int64, draft domain.WorkflowDefinition, desks []domain.Desk, issues []domain.WorkflowValidationIssue, live string, focus int) {
	if r.Header.Get("HX-Request") == "" {
		redirect(w, r, r.URL.Path)
		return
	}
	h.render(w, r, categoryID, draft, desks, issues, false, live, focus, http.StatusOK)
}

func (h *CategoryWorkflowHandlers) deskOptions(r *http.Request) []domain.Desk {
	desks, err := h.desks.List(r.Context(), *userFromContext(r.Context()))
	if err != nil {
		return nil
	}
	return desks
}

func (h *CategoryWorkflowHandlers) render(w http.ResponseWriter, r *http.Request, categoryID int64, draft domain.WorkflowDefinition, desks []domain.Desk, issues []domain.WorkflowValidationIssue, preview bool, live string, focus int, status int) {
	category, err := h.categories.GetByID(r.Context(), categoryID)
	if err != nil {
		http.Error(w, mapErrorMsg(err), statusFor(err))
		return
	}
	data := workflowBuilderData{
		pageData:     pageDataFrom(r, "categories"),
		CategoryID:   categoryID,
		CategoryName: category.Name,
		Draft:        draft,
		Desks:        desks,
		Issues:       issues,
		Preview:      preview,
		Live:         live,
		FocusStep:    focus,
	}
	data.PageFoundationAssets = true
	h.renderer.Render(w, r, "category_workflow", "workflow_builder", data, status)
}

func defaultLive() string { return "Use Up and Down to change a step position." }
func focusLive(pos, total int) string {
	return fmt.Sprintf("Step %d of %d.", pos+1, total)
}

// defaultStep is the step a fresh "Add step" appends: an editable manual task.
func defaultStep() domain.WorkflowStep {
	return domain.WorkflowStep{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{}}
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
	if !ok || i < 0 || i >= len(d) {
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
	return ensureFieldKeys(draftFromFields(r)), nil
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
func draftFromFields(r *http.Request) domain.WorkflowDefinition {
	var d domain.WorkflowDefinition
	for i := 0; ; i++ {
		pi := strconv.Itoa(i)
		typ := r.Form.Get("step_" + pi + "_type")
		if typ == "" {
			break
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
	return d
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
