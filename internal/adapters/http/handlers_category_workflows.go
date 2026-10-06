package httpadapter

import (
	"encoding/json"
	"errors"
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
	mux.HandleFunc("POST /categories/{id}/workflow/clone", h.clone)
}

type workflowStepView struct {
	Index    int
	Position int
	Summary  string
	// Actor names who performs the step and Outcome names the state the
	// ticket reaches, so a node reads as a builder node rather than a label
	// plus a truncated string (issue #249). Both come from the step itself.
	Actor    string
	Outcome  string
	Snapshot string
	Step     domain.WorkflowStep
	Selected bool
	Final    bool
	Last     bool

	CanMoveLeft  bool
	CanMoveRight bool
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
	HasFinal             bool
	// DraftRevision is the optimistic-lock expectation the builder carries back
	// on submit (issue #254). The field is always rendered: a submission without
	// it is refused rather than applied unguarded.
	DraftRevision int64
	// LiveVersion and the two names/instants are the stored facts surfaced
	// instead of staying invisible (issue #253): which version is live, who
	// published it and when, and who last changed the draft and when. They are
	// display-only; an absent fact renders nothing rather than failing the page.
	LiveVersion        int
	PublishedByName    string
	PublishedAt        string
	DraftUpdatedByName string
	DraftUpdatedAt     string
	// CloneSources are the OTHER categories with a published workflow the
	// builder may clone from (issue #257). The page's own category is excluded.
	CloneSources []domain.Category
	// CloneError carries a refused clone's comprehensible message back to the
	// page; it is display text only and never input.
	CloneError string
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
	draft, revision, err := h.workflows.GetForBuilder(r.Context(), *userFromContext(r.Context()), categoryID)
	if err != nil {
		http.Error(w, mapErrorMsg(err), statusFor(err))
		return
	}
	desks := h.deskOptions(r)
	h.render(w, r, categoryID, draft, revision, desks, nil, "", selectedStepIndex(r, len(draft)), http.StatusOK)
}

// clone copies a source category's published workflow into this category's
// draft (issue #257). Authorization reuses the existing category-management
// capability: the separate CapWorkflowAuthor capability is issue #255's lane
// and has not landed, so it is deliberately not invented here.
func (h *CategoryWorkflowHandlers) clone(w http.ResponseWriter, r *http.Request) {
	if !requireCapability(w, r, application.CapManageCategories) {
		return
	}
	targetID, ok := categoryID(r)
	if !ok {
		http.Error(w, "invalid category id", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	sourceID, err := strconv.ParseInt(r.Form.Get("source_category_id"), 10, 64)
	if err != nil || sourceID <= 0 {
		h.renderCloneRefused(w, r, targetID, "choose a source category to clone from", http.StatusUnprocessableEntity)
		return
	}
	if err := h.workflows.Clone(r.Context(), *userFromContext(r.Context()), sourceID, targetID); err != nil {
		h.renderCloneRefused(w, r, targetID, mapErrorMsg(err), statusFor(err))
		return
	}
	saveFeedback(w, r, saveFeedbackSaved, saveFeedbackSuccess)
	redirect(w, r, "/categories/"+strconv.FormatInt(targetID, 10)+"/workflow")
}

// renderCloneRefused re-renders the target's builder with the refusal message
// next to the clone control, leaving the target's own draft exactly as it was.
func (h *CategoryWorkflowHandlers) renderCloneRefused(w http.ResponseWriter, r *http.Request, targetID int64, message string, status int) {
	draft, revision, err := h.workflows.GetForBuilder(r.Context(), *userFromContext(r.Context()), targetID)
	if err != nil {
		http.Error(w, mapErrorMsg(err), statusFor(err))
		return
	}
	h.renderBuilder(w, r, targetID, draft, revision, h.deskOptions(r), nil, "", selectedStepIndex(r, len(draft)), message, status)
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
	action := r.Form.Get("action")
	revision, revisionErr := submittedRevision(r)
	if revisionErr != nil {
		// A missing or unusable expectation is not "any revision": a mutation
		// must never be applied without one, so it fails closed with ZERO writes
		// on the same refusal path as a stale revision. select_step persists
		// nothing, so it renders the submitted draft against the CURRENT stored
		// revision — the only defensible base for a page whose submission named
		// none — and its next save is still compared-and-swapped. The real
		// builder always renders the field; only a crafted or legacy request
		// reaches this branch.
		if action != "select_step" {
			h.renderDraftConflict(w, r, categoryID, selection)
			return
		}
		_, current, err := h.workflows.GetForBuilder(r.Context(), *userFromContext(r.Context()), categoryID)
		if err != nil {
			http.Error(w, mapErrorMsg(err), statusFor(err))
			return
		}
		revision = current
	}
	if len(issues) > 0 {
		h.render(w, r, categoryID, draft, revision, h.deskOptions(r), issues, "", selection, http.StatusUnprocessableEntity)
		return
	}

	actor := *userFromContext(r.Context())
	desks := h.deskOptions(r)
	switch action {
	case "select_step":
		target, err := strconv.Atoi(r.Form.Get("selection_step_index"))
		if err != nil || target < 0 || target >= len(draft) {
			target = selection
		}
		h.render(w, r, categoryID, draft, revision, desks, nil, "", target, http.StatusOK)
	case "save", "change_type":
		// save persists the reconstructed draft as-is; change_type is already
		// reflected in the reconstructed step payloads (selected closed payload
		// initialized, incompatible payloads dropped).
		if !h.persistBuilderDraft(w, r, categoryID, actor, draft, &revision, selection) {
			return
		}
		h.afterMutation(w, r, categoryID, draft, revision, desks, nil, saveFeedbackSaved, -1)
	case "add_step":
		// A terminal step stays final and last: new steps insert immediately before
		// it (the builder offers the insertion point before the final card), or
		// append when no terminal exists.
		step := typedAddStep(draft, r.Form.Get("add_step_type"))
		result, focus := insertBeforeTerminal(draft, step)
		if !h.persistBuilderDraft(w, r, categoryID, actor, result, &revision, selection) {
			return
		}
		h.afterMutation(w, r, categoryID, result, revision, desks, nil, saveFeedbackSaved, focus)
	case "move_up":
		result, focus, _ := localMoveUp(draft, r)
		if !h.persistBuilderDraft(w, r, categoryID, actor, result, &revision, selection) {
			return
		}
		h.afterMutation(w, r, categoryID, result, revision, desks, nil, saveFeedbackSaved, focus)
	case "move_down":
		result, focus, _ := localMoveDown(draft, r)
		if !h.persistBuilderDraft(w, r, categoryID, actor, result, &revision, selection) {
			return
		}
		h.afterMutation(w, r, categoryID, result, revision, desks, nil, saveFeedbackSaved, focus)
	case "remove_step":
		result, focus, _ := localRemoveStep(draft, r)
		if !h.persistBuilderDraft(w, r, categoryID, actor, result, &revision, selection) {
			return
		}
		h.afterMutation(w, r, categoryID, result, revision, desks, nil, saveFeedbackSaved, selectionAfterRemove(focus, len(result)))
	case "reorder":
		result, movedTo, err := localReorder(draft, r)
		if err != nil {
			h.render(w, r, categoryID, draft, revision, desks, []domain.WorkflowValidationIssue{{Step: 1, Field: "steps", Message: err.Error()}}, "", selection, http.StatusUnprocessableEntity)
			return
		}
		if !h.persistBuilderDraft(w, r, categoryID, actor, result, &revision, selection) {
			return
		}
		h.afterMutation(w, r, categoryID, result, revision, desks, nil, saveFeedbackSaved, movedTo)
	case "add_field":
		result, idx := localAddField(draft, r)
		if !h.persistBuilderDraft(w, r, categoryID, actor, result, &revision, selection) {
			return
		}
		h.afterMutation(w, r, categoryID, result, revision, desks, nil, saveFeedbackSaved, idx)
	case "remove_field":
		result, idx := localRemoveField(draft, r)
		if !h.persistBuilderDraft(w, r, categoryID, actor, result, &revision, selection) {
			return
		}
		h.afterMutation(w, r, categoryID, result, revision, desks, nil, saveFeedbackSaved, idx)
	case "publish":
		// Publish is guarded too (issue #254): the submitted revision must still
		// be the stored one. A stale publish is refused on the same 409 path and
		// creates no version, and a success advances the revision so a later save
		// at the pre-publish revision cannot overwrite the published draft.
		newRevision, publishIssues, err := h.workflows.PublishAtRevision(r.Context(), actor, categoryID, draft, revision)
		if errors.Is(err, application.ErrDraftRevisionConflict) {
			h.renderDraftConflict(w, r, categoryID, selection)
			return
		}
		if err != nil {
			http.Error(w, mapErrorMsg(err), statusFor(err))
			return
		}
		if len(publishIssues) > 0 {
			h.render(w, r, categoryID, draft, revision, desks, publishIssues, "", selection, http.StatusUnprocessableEntity)
			return
		}
		revision = newRevision
		h.afterMutation(w, r, categoryID, draft, revision, desks, nil, saveFeedbackPublished, selection)
	default:
		h.render(w, r, categoryID, draft, revision, desks, []domain.WorkflowValidationIssue{{Step: 1, Field: "action", Message: "unknown workflow action"}}, "", selection, http.StatusUnprocessableEntity)
	}
}

// draftConflictMessage is the user-facing refusal (issue #254). It says what
// happened (someone else changed the workflow), that the submitted change was
// NOT saved, and what the page now shows (the latest draft), so the operator
// can reapply their edit instead of wondering where it went.
const draftConflictMessage = "Another session changed this workflow while you were editing. Your change was not saved; the latest draft is shown below."

// submittedRevision reads the revision a browser carried in the form. The
// field is REQUIRED: absent and malformed both mean the request cannot be
// applied safely, so both are reported as errors and the caller fails closed
// with zero writes. Neither is ever silently treated as stale, fresh, or
// "match whatever is stored" — that last reading is exactly the bypass this
// tightening closes.
func submittedRevision(r *http.Request) (int64, error) {
	values, ok := r.Form["draft_revision"]
	if !ok {
		return 0, fmt.Errorf("draft_revision is required")
	}
	if len(values) != 1 || !strictBuilderIndex.MatchString(values[0]) {
		return 0, fmt.Errorf("draft_revision must be one non-negative numeric revision")
	}
	value, err := strconv.ParseInt(values[0], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("draft_revision is out of range")
	}
	return value, nil
}

// persistBuilderDraft writes one reconstructed draft through the GUARDED path
// and reports the new revision in *revision. A stale revision renders the
// refusal (409) and writes nothing. There is no unguarded branch: a missing
// expectation is refused before this is reached, so every builder mutation is
// compare-and-swapped. It reports whether the caller should continue and render
// the success.
func (h *CategoryWorkflowHandlers) persistBuilderDraft(w http.ResponseWriter, r *http.Request, categoryID int64, actor domain.User, draft domain.WorkflowDefinition, revision *int64, selection int) bool {
	next, err := h.workflows.SaveDraftAtRevision(r.Context(), actor, categoryID, draft, *revision)
	if errors.Is(err, application.ErrDraftRevisionConflict) {
		h.renderDraftConflict(w, r, categoryID, selection)
		return false
	}
	if err != nil {
		http.Error(w, mapErrorMsg(err), statusFor(err))
		return false
	}
	*revision = next
	return true
}

// renderDraftConflict re-renders the builder from the CURRENT stored draft and
// its revision, with the message that says the submitted copy was not saved. It
// writes nothing: the newer writer's bytes stay exactly as they are.
func (h *CategoryWorkflowHandlers) renderDraftConflict(w http.ResponseWriter, r *http.Request, categoryID int64, selection int) {
	fresh, revision, err := h.workflows.GetForBuilder(r.Context(), *userFromContext(r.Context()), categoryID)
	if err != nil {
		http.Error(w, mapErrorMsg(err), statusFor(err))
		return
	}
	h.render(w, r, categoryID, fresh, revision, h.deskOptions(r),
		[]domain.WorkflowValidationIssue{{Step: 1, Field: "draft", Message: draftConflictMessage}},
		"", selection, http.StatusConflict)
}

// afterMutation issues feedback only after persistence succeeds. Full-page
// requests carry it to the redirect; HTMX receives the server-issued header.
func (h *CategoryWorkflowHandlers) afterMutation(w http.ResponseWriter, r *http.Request, categoryID int64, draft domain.WorkflowDefinition, revision int64, desks []domain.Desk, issues []domain.WorkflowValidationIssue, message string, focus int) {
	if focus < 0 {
		focus = selectedStepIndex(r, len(draft))
	}
	saveFeedback(w, r, message, saveFeedbackSuccess)
	if r.Header.Get("HX-Request") == "" {
		redirect(w, r, workflowLocation(r, focus))
		return
	}
	h.render(w, r, categoryID, draft, revision, desks, issues, "", focus, http.StatusOK)
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

func (h *CategoryWorkflowHandlers) render(w http.ResponseWriter, r *http.Request, categoryID int64, draft domain.WorkflowDefinition, revision int64, desks []domain.Desk, issues []domain.WorkflowValidationIssue, live string, focus int, status int) {
	h.renderBuilder(w, r, categoryID, draft, revision, desks, issues, live, focus, "", status)
}

// renderBuilder is render with the optional clone-refusal message (issue #257);
// every existing call site keeps the plain render entry point.
func (h *CategoryWorkflowHandlers) renderBuilder(w http.ResponseWriter, r *http.Request, categoryID int64, draft domain.WorkflowDefinition, revision int64, desks []domain.Desk, issues []domain.WorkflowValidationIssue, live string, focus int, cloneError string, status int) {
	category, err := h.categories.GetByID(r.Context(), categoryID)
	if err != nil {
		http.Error(w, mapErrorMsg(err), statusFor(err))
		return
	}
	selection := focus
	if selection < 0 {
		selection = selectedStepIndex(r, len(draft))
	}
	steps := workflowStepViews(draft, selection, desks)
	// The stored attribution is a display-only read: a store without the
	// capability, or a category with no recorded facts, renders nothing rather
	// than failing a builder that is otherwise fine.
	attribution, _ := h.workflows.GetAttribution(r.Context(), *userFromContext(r.Context()), categoryID)
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
		HasFinal:     hasTerminalStep(draft),
		CloneSources: h.cloneSources(r, categoryID),
		CloneError:   cloneError,

		DraftRevision:      revision,
		LiveVersion:        attribution.Version,
		PublishedByName:    attribution.PublishedByName,
		PublishedAt:        formatDisplayTime(attribution.PublishedAt),
		DraftUpdatedByName: attribution.DraftUpdatedByName,
		DraftUpdatedAt:     formatDisplayTime(attribution.DraftUpdatedAt),
	}
	data.PageFoundationAssets = true
	data.WorkflowAssets = true
	h.renderer.Render(w, r, "category_workflow", "workflow_builder", data, status)
}

// cloneSources lists the other categories with a published workflow, ordered by
// the store's canonical order. A read failure yields no options rather than an
// error: the clone control is an optional authoring aid, and a page that cannot
// list sources must still render the builder.
func (h *CategoryWorkflowHandlers) cloneSources(r *http.Request, targetID int64) []domain.Category {
	categories, err := h.workflows.ListAvailableCategories(r.Context())
	if err != nil {
		return nil
	}
	out := make([]domain.Category, 0, len(categories))
	for _, c := range categories {
		if c.ID != targetID {
			out = append(out, c)
		}
	}
	return out
}

func workflowStepViews(draft domain.WorkflowDefinition, selected int, desks []domain.Desk) []workflowStepView {
	views := make([]workflowStepView, 0, len(draft))
	for i, step := range draft {
		raw, _ := json.Marshal(step)
		canRight := i+1 < len(draft) && !isTerminalStep(draft[i+1].Type)
		views = append(views, workflowStepView{
			Index: i, Position: i + 1,
			Summary:  workflowStepSummary(step, desks),
			Actor:    workflowStepActor(step, desks),
			Outcome:  workflowStepOutcome(step),
			Snapshot: string(raw), Step: step,
			Selected: i == selected,
			Final:    isTerminalStep(step.Type) && i == len(draft)-1,
			Last:     i == len(draft)-1,

			CanMoveLeft:  i > 0 && !isTerminalStep(step.Type),
			CanMoveRight: canRight && !isTerminalStep(step.Type),
		})
	}
	return views
}

func isTerminalStep(typ domain.StepType) bool {
	return typ == domain.StepResolve || typ == domain.StepClose
}
func workflowStepSummary(step domain.WorkflowStep, desks []domain.Desk) string {
	switch step.Type {
	case domain.StepAssignToDesk:
		if step.AssignToDesk != nil && step.AssignToDesk.DeskID > 0 {
			return deskName(step.AssignToDesk.DeskID, desks)
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
		// The actor is a separate node fact now, so the summary carries only
		// what the step collects instead of repeating the actor.
		return fmt.Sprintf("%d field%s", len(step.Form.Fields), suffix)
	case domain.StepManualTask:
		if step.ManualTask == nil || strings.TrimSpace(step.ManualTask.Instructions) == "" {
			return "Add instructions"
		}
		// Issue #249: the whole instruction reaches the node; the layout wraps
		// it rather than the model cutting it.
		return strings.Join(strings.Fields(step.ManualTask.Instructions), " ")
	case domain.StepResolve, domain.StepClose:
		return "Runs automatically"
	default:
		return "Configure this step"
	}
}

// workflowStepActor names who performs a step, distinct from the automatic
// terminal and least-loaded routing where no person acts.
func workflowStepActor(step domain.WorkflowStep, desks []domain.Desk) string {
	switch step.Type {
	case domain.StepAssignToDesk:
		if step.AssignToDesk != nil && step.AssignToDesk.Strategy == domain.StrategyLeastLoaded {
			return "Automatic"
		}
		if step.AssignToDesk != nil && step.AssignToDesk.DeskID > 0 {
			return "Members of " + deskName(step.AssignToDesk.DeskID, desks)
		}
		return "Members of the desk"
	case domain.StepForm:
		if step.Form != nil && step.Form.Actor == domain.FormActorAssignee {
			return "Assignee"
		}
		return "Requester"
	case domain.StepManualTask:
		return "Assignee"
	case domain.StepResolve, domain.StepClose:
		return "Automatic"
	default:
		return ""
	}
}

// workflowStepOutcome names the state the ticket reaches. Asking and working
// leave the state alone, so they have no outcome; routing moves the ticket into
// progress and the terminals resolve (and close) it.
func workflowStepOutcome(step domain.WorkflowStep) string {
	switch step.Type {
	case domain.StepAssignToDesk:
		return "In progress"
	case domain.StepResolve:
		return "Resolved"
	case domain.StepClose:
		return "Resolved, then closed"
	default:
		return ""
	}
}

// deskName resolves a referenced desk to its display name, falling back to the
// raw id when the desk is not in the option set.
func deskName(id int64, desks []domain.Desk) string {
	for _, d := range desks {
		if d.ID == id {
			return d.Name
		}
	}
	return fmt.Sprintf("Desk %d", id)
}

// insertBeforeTerminal places step directly before the existing terminal (keeping
// it last), or appends when none exists. It returns the new definition and the
// inserted step's focus index.
func insertBeforeTerminal(d domain.WorkflowDefinition, step domain.WorkflowStep) (domain.WorkflowDefinition, int) {
	for i, s := range d {
		if isTerminalStep(s.Type) {
			result := append(domain.WorkflowDefinition(nil), d[:i]...)
			result = append(result, step)
			result = append(result, d[i:]...)
			return result, i
		}
	}
	result := append(append(domain.WorkflowDefinition(nil), d...), step)
	return result, len(result) - 1
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
					Options:  parseOptionLines(r.Form.Get(base + "_options")),
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

// parseOptionLines parses the one-option-per-line single_select options input.
// Each non-blank line, with surrounding whitespace trimmed, is one option in
// submission order; blank lines are skipped, so a trailing newline never adds
// an option, and a semicolon is an ordinary character rather than a separator.
func parseOptionLines(v string) []string {
	var out []string
	for _, line := range strings.Split(v, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	return out
}

func builderIssue(message string) []domain.WorkflowValidationIssue {
	return []domain.WorkflowValidationIssue{{Step: 1, Field: "steps", Message: "Step 1: " + message}}
}
