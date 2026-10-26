package httpadapter

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
	"github.com/giulianotesta7/tkt/internal/i18n"
)

// CategoryHandlers implement the category management routes (design "HTTP
// Layer" route table): GET /categories, GET/POST /categories/new + POST
// /categories (create), GET/POST /categories/{id}/edit (rename), POST
// /categories/{id}/delete. Deletes of referenced categories are rejected
// 409 (category-management spec).
const unassignedDepartmentID int64 = -1

type CategoryHandlers struct {
	categories *application.CategoryService
	workflows  *application.WorkflowService
	renderer   *Renderer
	catalog    *application.CatalogService
	appSet     application.SettingsStore
}

// NewCategoryHandlers wires category routes without workflow projections. It
// remains available for focused legacy handler tests.
func NewCategoryHandlers(categories *application.CategoryService, renderer *Renderer) *CategoryHandlers {
	return &CategoryHandlers{categories: categories, renderer: renderer}
}

// NewCategoryHandlersWithWorkflows adds derived workflow badges to the category
// index used by the production and integration composition roots.
func NewCategoryHandlersWithWorkflows(categories *application.CategoryService, workflows *application.WorkflowService, renderer *Renderer, catalogs ...*application.CatalogService) *CategoryHandlers {
	h := &CategoryHandlers{categories: categories, workflows: workflows, renderer: renderer}
	if len(catalogs) > 0 {
		h.catalog = catalogs[0]
	}
	return h
}

// WithSettings supplies the instance settings the catalog tree reads. The only
// setting it needs is whether SLA is enabled at all: with SLA off, a row shows no
// SLA state instead of claiming "Default SLA" for a feature nobody turned on.
func (h *CategoryHandlers) WithSettings(settings application.SettingsStore) *CategoryHandlers {
	h.appSet = settings
	return h
}

// Register mounts the category routes.
func (h *CategoryHandlers) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /categories", h.index)
	mux.HandleFunc("GET /categories/new", h.newForm)
	mux.HandleFunc("POST /categories", h.create)
	mux.HandleFunc("GET /categories/{id}/edit", h.editForm)
	mux.HandleFunc("GET /categories/departments/new", h.newDepartmentForm)
	mux.HandleFunc("GET /categories/departments/{id}/edit", h.editDepartmentForm)
	mux.HandleFunc("GET /categories/desks/new", h.newDeskForm)
	mux.HandleFunc("GET /categories/desks/{id}/edit", h.editDeskForm)
	mux.HandleFunc("POST /categories/{id}/edit", h.update)
	mux.HandleFunc("POST /categories/{id}/delete", h.delete)
	mux.HandleFunc("POST /categories/departments", h.createDepartment)
	mux.HandleFunc("POST /categories/departments/{id}/delete", h.deleteDepartment)
	mux.HandleFunc("POST /categories/departments/{id}/edit", h.updateDepartment)
	mux.HandleFunc("POST /categories/desks", h.createDesk)
	mux.HandleFunc("POST /categories/desks/{id}/delete", h.deleteDesk)
	mux.HandleFunc("POST /categories/desks/{id}/edit", h.updateDesk)
}

// categoriesIndexData is the Catalog administration payload: one tree of
// departments, desks and categories, with the two native filters (search and
// Needs attention) already applied by the handler and the legacy selection
// params reduced to a mark on the matching row.
type categoriesIndexData struct {
	pageData
	Error                string
	View                 string
	Attention            bool
	Tree                 catalogTree
	SelectedDepartmentID int64
	SelectedDeskID       int64
	SearchQuery          string
	SearchClearURL       string
	AttentionURL         string
	Drawer               *categoryDrawerData
}

type categoryViewState struct {
	View         string
	DepartmentID int64
	DeskID       int64
}

type categoryDrawerData struct {
	Kind        string
	ID          int64
	Name        string
	Description string
	// Keywords is the suggestion keywords field the category drawer alone
	// renders (issue #416, S4). A department or a desk has no keywords, so the
	// template asks for the kind before it shows the control.
	Keywords             string
	DepartmentID         int64
	DeskID               int64
	Departments          []domain.CatalogDepartment
	Desks                []domain.CatalogDesk
	Members              []domain.User
	Eligible             []domain.User
	ActionURL            string
	CloseURL             string
	View                 string
	SelectedDepartmentID int64
	SelectedDeskID       int64
	Error                string
	HasServerError       bool
	InvalidName          bool
	InvalidDepartmentID  bool
	InvalidDeskID        bool
	ReadError            error
}

// cannotRunLabel is the ONE place the "cannot run" fact is put into words. The
// admin's categories screen and the requester's ticket picker both need it, and
// two compositions of the same fact would drift into two vocabularies for one
// state — the thing WorkflowSummary exists to prevent.
// categoryBadge is the one line a category row shows for its workflow state.
//
// It is composed here, from facts, rather than in the store, so the wording is a
// presentation concern; and it is the only composition, so no surface invents a
// competing vocabulary. Before this, the store returned one of three words and
// "published with edits not yet live" rendered as the same "Draft" as "never
// published" — two different governance states behind one word.
func categoryBadge(s application.WorkflowSummary) application.Msg {
	switch {
	case s.Version == 0 && !s.HasDraft:
		return application.MsgRef("categories.badge.not_configured")
	case s.Version == 0:
		return application.MsgRef("categories.badge.draft_only")
	case !s.CannotRun.Empty():
		// Published, and unable to move a ticket. Without this the category looked
		// exactly like a healthy one while every ticket filed against it failed.
		// The reason is a catalogue reference (or data), composed as a nested
		// sentence so it follows the instance language.
		return application.MsgRef("categories.badge.cannot_run", application.Nested(s.CannotRun))
	case s.PendingSteps == 1:
		return application.MsgRef("categories.badge.published_one_change", application.Text(strconv.Itoa(s.Version)))
	case s.PendingSteps > 1:
		return application.MsgRef("categories.badge.published_many_changes",
			application.Text(strconv.Itoa(s.Version)), application.Text(strconv.Itoa(s.PendingSteps)))
	default:
		return application.MsgRef("categories.badge.published", application.Text(strconv.Itoa(s.Version)))
	}
}

// categoryStatusLine is categoryBadge plus the attribution suffix the row can
// now show (issue #253): the operator who published the live version, or — when
// nothing is published yet — the operator who last changed the draft. It is the
// same one-line slot and the same one composition, so no surface invents a
// second vocabulary for who/when. A category whose rows predate migration 0020
// (or whose publisher is unknown) keeps the bare badge.
func categoryStatusLine(s application.WorkflowSummary, a application.WorkflowAttribution) application.Msg {
	line := categoryBadge(s)
	switch {
	case s.Version > 0 && a.PublishedByName != "":
		return application.MsgRef("categories.badge.by", application.Nested(line), application.Text(a.PublishedByName))
	case a.DraftUpdatedByName != "":
		return application.MsgRef("categories.badge.edited_by", application.Nested(line), application.Text(a.DraftUpdatedByName))
	default:
		return line
	}
}

func (h *CategoryHandlers) index(w http.ResponseWriter, r *http.Request) {
	setCategoriesVary(w)
	if !requireCapability(w, r, application.CapManageCategories) {
		return
	}
	h.renderIndex(w, r, "", http.StatusOK)
}

// categoryFormData is the create/rename form payload. CategoryID 0 = create.
type categoryFormData struct {
	pageData
	Error       string
	CategoryID  int64
	Name        string
	Description string
	DeskID      int64
	Desks       []domain.CatalogDesk
	Departments []domain.CatalogDepartment
}

func (h *CategoryHandlers) newForm(w http.ResponseWriter, r *http.Request) {
	setCategoriesVary(w)
	if !requireCapability(w, r, application.CapManageCategories) {
		return
	}
	if _, err := categoryStateFromRequest(r); err != nil {
		http.Error(w, mapErrorMsg(r, err), statusFor(err))
		return
	}
	d := h.newDrawerData(r, "category")
	h.renderCategoryDrawer(w, r, d)
}

func (h *CategoryHandlers) newDepartmentForm(w http.ResponseWriter, r *http.Request) {
	setCategoriesVary(w)
	if !requireCapability(w, r, application.CapManageCategories) || h.catalog == nil {
		return
	}
	if _, err := categoryStateFromRequest(r); err != nil {
		http.Error(w, mapErrorMsg(r, err), statusFor(err))
		return
	}
	h.renderCategoryDrawer(w, r, h.newDrawerData(r, "department"))
}

func (h *CategoryHandlers) newDeskForm(w http.ResponseWriter, r *http.Request) {
	setCategoriesVary(w)
	if !requireCapability(w, r, application.CapManageCategories) || h.catalog == nil {
		return
	}
	if _, err := categoryStateFromRequest(r); err != nil {
		http.Error(w, mapErrorMsg(r, err), statusFor(err))
		return
	}
	d := h.newDrawerData(r, "desk")
	if state, stateErr := categoryStateFromRequest(r); stateErr == nil {
		d.DepartmentID = state.DepartmentID
	}
	h.renderCategoryDrawer(w, r, d)
}

func (h *CategoryHandlers) create(w http.ResponseWriter, r *http.Request) {
	if !requireCapability(w, r, application.CapManageCategories) {
		return
	}
	if err := r.ParseForm(); err != nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	state, ok := h.categoryMutationState(w, r)
	if !ok {
		return
	}
	var err error
	if h.catalog != nil {
		deskID, parseErr := requiredCategoryID(r.Form.Get("desk_id"), "desk_id")
		if parseErr != nil {
			h.renderCategoryFormError(w, r, 0, parseErr)
			return
		}
		_, err = h.categories.CreateWithKeywordsFor(r.Context(), *userFromContext(r.Context()), r.Form.Get("name"), r.Form.Get("description"), r.Form.Get("keywords"), deskID)
	} else {
		_, err = h.categories.CreateFor(r.Context(), *userFromContext(r.Context()), r.Form.Get("name"))
	}
	if err != nil {
		h.renderCategoryFormError(w, r, 0, err)
		return
	}
	h.redirectCategoryMutation(w, r, state)
}

func (h *CategoryHandlers) editForm(w http.ResponseWriter, r *http.Request) {
	setCategoriesVary(w)
	if !requireCapability(w, r, application.CapManageCategories) {
		return
	}
	if _, err := categoryStateFromRequest(r); err != nil {
		http.Error(w, mapErrorMsg(r, err), statusFor(err))
		return
	}
	id, ok := categoryID(r)
	if !ok {
		httpError(w, r, "error.invalid_category_id", http.StatusBadRequest)
		return
	}
	c, err := h.categories.GetByID(r.Context(), id)
	if err != nil {
		http.Error(w, mapErrorMsg(r, err), statusFor(err))
		return
	}
	d := h.newDrawerData(r, "category")
	d.ID, d.Name, d.Description, d.Keywords, d.DeskID = id, c.Name, c.Description, c.Keywords, c.DeskID
	for _, desk := range d.Desks {
		if desk.ID == c.DeskID {
			// A legacy desk's stored parent is NULL (DepartmentID 0); the
			// drawer presents it as the virtual Unassigned group so the real
			// location stays preselected instead of silently relocating.
			if desk.DepartmentID == 0 {
				d.DepartmentID = unassignedDepartmentID
			} else {
				d.DepartmentID = desk.DepartmentID
			}
			break
		}
	}
	h.renderCategoryDrawer(w, r, d)
}

func (h *CategoryHandlers) editDepartmentForm(w http.ResponseWriter, r *http.Request) {
	setCategoriesVary(w)
	if !requireCapability(w, r, application.CapManageCategories) || h.catalog == nil {
		return
	}
	if _, err := categoryStateFromRequest(r); err != nil {
		http.Error(w, mapErrorMsg(r, err), statusFor(err))
		return
	}
	id, ok := categoryID(r)
	if !ok {
		httpError(w, r, "error.invalid_department_id", http.StatusBadRequest)
		return
	}
	departments, err := h.catalog.ListDepartments(r.Context())
	if err != nil {
		http.Error(w, mapErrorMsg(r, err), statusFor(err))
		return
	}
	for _, department := range departments {
		if department.ID == id {
			d := h.newDrawerData(r, "department")
			d.ID, d.Name, d.Description = id, department.Name, department.Description
			h.renderCategoryDrawer(w, r, d)
			return
		}
	}
	httpError(w, r, "error.department_not_found", http.StatusNotFound)
}

func (h *CategoryHandlers) editDeskForm(w http.ResponseWriter, r *http.Request) {
	setCategoriesVary(w)
	if !requireCapability(w, r, application.CapManageCategories) || h.catalog == nil {
		return
	}
	if _, err := categoryStateFromRequest(r); err != nil {
		http.Error(w, mapErrorMsg(r, err), statusFor(err))
		return
	}
	id, ok := categoryID(r)
	if !ok {
		httpError(w, r, "error.invalid_desk_id", http.StatusBadRequest)
		return
	}
	departments, err := h.catalog.ListDepartments(r.Context())
	if err != nil {
		http.Error(w, mapErrorMsg(r, err), statusFor(err))
		return
	}
	departmentIDs := make([]int64, 0, len(departments)+1)
	for _, department := range departments {
		departmentIDs = append(departmentIDs, department.ID)
	}
	// A zero DepartmentID is the persisted legacy value; it is presented as
	// the virtual Unassigned group and can only be edited by assigning a real
	// Department before saving.
	departmentIDs = append(departmentIDs, 0)
	for _, departmentID := range departmentIDs {
		desks, listErr := h.catalog.ListDesks(r.Context(), departmentID)
		if listErr != nil {
			http.Error(w, mapErrorMsg(r, listErr), statusFor(listErr))
			return
		}
		for _, desk := range desks {
			if desk.ID == id {
				d := h.newDrawerData(r, "desk")
				d.ID, d.Name, d.Description = id, desk.Name, desk.Description
				if desk.Desk.DepartmentID != nil {
					d.DepartmentID = *desk.Desk.DepartmentID
				} else {
					d.DepartmentID = 0
				}
				if h.catalog != nil {
					d.Members, _ = h.catalog.ListDeskMembersFor(r.Context(), *userFromContext(r.Context()), id)
					d.Eligible, _ = h.catalog.ListEligibleDeskMembersFor(r.Context(), *userFromContext(r.Context()))
					memberIDs := make(map[int64]struct{}, len(d.Members))
					for _, member := range d.Members {
						memberIDs[member.ID] = struct{}{}
					}
					eligible := d.Eligible[:0]
					for _, member := range d.Eligible {
						if _, ok := memberIDs[member.ID]; !ok {
							eligible = append(eligible, member)
						}
					}
					d.Eligible = eligible
				}
				if d.View == "structure" {
					selectedDepartmentID := unassignedDepartmentID
					if desk.Desk.DepartmentID != nil {
						selectedDepartmentID = *desk.Desk.DepartmentID
					}
					d.SelectedDepartmentID, d.SelectedDeskID = selectedDepartmentID, id
					d.CloseURL = categoryStatePath(categoryViewState{View: "structure", DepartmentID: selectedDepartmentID, DeskID: id})
				}
				h.renderCategoryDrawer(w, r, d)
				return
			}
		}
	}
	httpError(w, r, "error.desk_not_found", http.StatusNotFound)
}

func (h *CategoryHandlers) update(w http.ResponseWriter, r *http.Request) {
	if !requireCapability(w, r, application.CapManageCategories) {
		return
	}
	id, ok := categoryID(r)
	if !ok {
		httpError(w, r, "error.invalid_category_id", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	state, ok := h.categoryMutationState(w, r)
	if !ok {
		return
	}
	var err error
	if h.catalog != nil {
		c, getErr := h.categories.GetByID(r.Context(), id)
		if getErr == nil {
			c.Name, c.Description, c.Keywords = r.Form.Get("name"), r.Form.Get("description"), r.Form.Get("keywords")
			deskID, parseErr := requiredCategoryID(r.Form.Get("desk_id"), "desk_id")
			if parseErr != nil {
				h.renderCategoryFormError(w, r, id, parseErr)
				return
			}
			c.DeskID = deskID
			err = h.catalog.UpdateCategoryFor(r.Context(), *userFromContext(r.Context()), c)
		} else {
			err = getErr
		}
	} else {
		_, err = h.categories.RenameFor(r.Context(), *userFromContext(r.Context()), id, r.Form.Get("name"))
	}
	if err != nil {
		h.renderCategoryFormError(w, r, id, err)
		return
	}
	h.redirectCategoryMutation(w, r, state)
}

func (h *CategoryHandlers) delete(w http.ResponseWriter, r *http.Request) {
	if !requireCapability(w, r, application.CapManageCategories) {
		return
	}
	id, ok := categoryID(r)
	if !ok {
		httpError(w, r, "error.invalid_category_id", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	state, ok := h.categoryMutationState(w, r)
	if !ok {
		return
	}
	if err := h.categories.DeleteFor(r.Context(), *userFromContext(r.Context()), id); err != nil {
		h.renderCategoryDeleteRefusal(w, r, err)
		return
	}
	h.redirectCategoryMutation(w, r, state)
}

// renderCatalogFormError re-renders a catalog drawer with submitted values.
func (h *CategoryHandlers) renderCatalogFormError(w http.ResponseWriter, r *http.Request, kind string, id int64, err error) {
	status, msg := mapErrorFor(r, err)
	if status == http.StatusInternalServerError {
		http.Error(w, msg, status)
		return
	}
	d := h.newDrawerData(r, kind)
	if state, stateErr := categoryStateFromValues(r.Form); stateErr == nil {
		d.View, d.DepartmentID, d.DeskID = state.View, state.DepartmentID, state.DeskID
		d.SelectedDepartmentID, d.SelectedDeskID = state.DepartmentID, state.DeskID
		d.CloseURL = categoryStatePath(state)
	}
	d.ID, d.Name, d.Description, d.Keywords, d.Error = id, r.Form.Get("name"), r.Form.Get("description"), r.Form.Get("keywords"), msg
	d.HasServerError = true
	d.markInvalidControl(err)
	h.renderCategoryDrawerStatus(w, r, d, status)
}

// markInvalidControl projects only structured domain errors to controls the
// current drawer renders. Error messages remain display text, never input.
func (d *categoryDrawerData) markInvalidControl(err error) {
	var validation *domain.ValidationError
	if errors.As(err, &validation) {
		switch validation.Field {
		case "name":
			d.InvalidName = true
		case "department_id":
			d.InvalidDepartmentID = d.Kind == "desk"
		case "desk_id":
			d.InvalidDeskID = d.Kind == "category"
		}
		return
	}

	var duplicate *domain.DuplicateError
	if errors.As(err, &duplicate) && duplicate.Kind == d.Kind {
		d.InvalidName = true
	}
}

// renderCategoryFormError re-renders the category drawer with submitted values.
func (h *CategoryHandlers) renderCategoryFormError(w http.ResponseWriter, r *http.Request, id int64, err error) {
	h.renderCatalogFormError(w, r, "category", id, err)
}

// renderCategoryDeleteRefusal keeps the operator on the structure view they
// deleted from, with the same department and desk selected, and reports the
// refusal in the instance language (issue #343, S6). The selection rides in the
// form the control submitted, not in the POST URL, so it has to be re-read from
// there: the previous path rebuilt the index from the URL alone, which dropped
// both ids and left the browser on /categories/desks/{id}/delete.
//
// An HTMX delete swaps the structure region in place and keeps the URL, so the
// refusal keeps the real status with the retarget the script already honours for
// form errors; a native post renders the same view as a whole page.
func (h *CategoryHandlers) renderCategoryDeleteRefusal(w http.ResponseWriter, r *http.Request, err error) {
	status, msg := mapErrorFor(r, err)
	if status == http.StatusInternalServerError {
		http.Error(w, msg, status)
		return
	}
	// The selection comes from the submitted form; parse it here so the
	// helper does not depend on every caller having parsed it first.
	if r.Form == nil {
		_ = r.ParseForm()
	}
	state, stateErr := categoryStateFromValues(r.Form)
	if stateErr != nil {
		http.Error(w, mapErrorMsg(r, stateErr), statusFor(stateErr))
		return
	}
	data, dataErr := h.categoryIndexData(requestWithCategoryState(r, state), msg)
	if dataErr != nil {
		http.Error(w, mapErrorMsg(r, dataErr), statusFor(dataErr))
		return
	}
	if r.Header.Get("HX-Request") == "" {
		h.renderer.Render(w, r, "categories_index", "", data, status)
		return
	}
	w.Header().Set("HX-Retarget", "#categories-background")
	w.Header().Set("HX-Reswap", "outerHTML")
	h.renderer.Render(w, r, "categories_index", "categories_background", data, status)
}

func (h *CategoryHandlers) renderIndex(w http.ResponseWriter, r *http.Request, message string, status int) {
	data, err := h.categoryIndexData(r, message)
	if err != nil {
		http.Error(w, mapErrorMsg(r, err), statusFor(err))
		return
	}
	h.renderer.Render(w, r, "categories_index", "", data, status)
}

func (h *CategoryHandlers) categoryIndexData(r *http.Request, message string) (categoriesIndexData, error) {
	locale := requestLocale(r)
	state, err := categoryStateFromRequest(r)
	if err != nil {
		return categoriesIndexData{}, err
	}
	categories, err := h.categories.List(r.Context())
	if err != nil {
		return categoriesIndexData{}, err
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	attention := r.URL.Query().Get("attention") == "1"
	data := categoriesIndexData{
		pageData:             pageDataFrom(r, "categories"),
		Error:                message,
		View:                 state.View,
		Attention:            attention,
		SelectedDepartmentID: state.DepartmentID,
		SelectedDeskID:       state.DeskID,
		SearchQuery:          query,
	}
	data.CategoryAssets, data.PageFoundationAssets = true, true
	data.SearchClearURL = categoryTreePath("", false)
	data.AttentionURL = categoryTreePath(query, !attention)

	input := catalogTreeInput{
		Locale:       locale,
		Categories:   categories,
		States:       make(map[int64]catalogCategoryState, len(categories)),
		OpenTickets:  make(map[int64]int, len(categories)),
		SLADefaults:  make(map[int64]bool),
		Query:        query,
		Attention:    attention,
		DepartmentID: state.DepartmentID,
		DeskID:       state.DeskID,
	}
	// The badge, the open count and the tone all come from the SAME summary list,
	// so a row's state and its count can never disagree about which category they
	// describe.
	if h.workflows != nil {
		summaries, summaryErr := h.workflows.ListSummaries(r.Context(), *userFromContext(r.Context()))
		if summaryErr != nil {
			return categoriesIndexData{}, summaryErr
		}
		// Attribution is its own read (issue #253), keyed by category id in one
		// query; a store that cannot answer leaves the map empty and every row keeps
		// the bare badge.
		attributions, attributionErr := h.workflows.ListAttribution(r.Context(), *userFromContext(r.Context()))
		if attributionErr != nil {
			return categoriesIndexData{}, attributionErr
		}
		for _, summary := range summaries {
			input.States[summary.CategoryID] = catalogCategoryStateOf(summary, attributions[summary.CategoryID])
			input.OpenTickets[summary.CategoryID] = summary.OpenTickets
		}
	}
	if h.catalog != nil {
		input.Departments, err = h.catalog.ListDepartments(r.Context())
		if err != nil {
			return categoriesIndexData{}, err
		}
		// One bounded read for every desk, with its agent and open-ticket count:
		// the index no longer fans out one ListDesks call per department.
		input.Desks, err = h.catalog.ListTreeDesks(r.Context())
		if err != nil {
			return categoriesIndexData{}, err
		}
		input.SLAEnabled, err = h.slaEnabled(r)
		if err != nil {
			return categoriesIndexData{}, err
		}
		if input.SLAEnabled {
			defaults, listErr := h.catalog.ListDefaultSLACategories(r.Context())
			if listErr != nil {
				return categoriesIndexData{}, listErr
			}
			for _, id := range defaults {
				input.SLADefaults[id] = true
			}
		}
	}
	data.Tree = buildCatalogTree(input)
	return data, nil
}

// slaEnabled reports whether the instance has per-category SLA switched on. A
// handler composed without the settings port (focused legacy tests) answers
// false rather than guessing, so no row claims an SLA state nobody configured.
func (h *CategoryHandlers) slaEnabled(r *http.Request) (bool, error) {
	if h.appSet == nil {
		return false, nil
	}
	return h.appSet.GetSLAEnabled(r.Context())
}

// categoryTreePath is the URL of one filtered catalog view. Both filters are
// plain GET parameters, so search and Needs attention keep working without a
// script and compose by simply appearing together.
func categoryTreePath(query string, attention bool) string {
	values := url.Values{}
	if query != "" {
		values.Set("q", query)
	}
	if attention {
		values.Set("attention", "1")
	}
	if len(values) == 0 {
		return "/categories"
	}
	return "/categories?" + values.Encode()
}

func setCategoriesVary(w http.ResponseWriter) {
	w.Header().Set("Vary", "HX-Request")
}

func categoryStateFromRequest(r *http.Request) (categoryViewState, error) {
	return categoryStateFromValues(r.URL.Query())
}

func categoryStateFromValues(values url.Values) (categoryViewState, error) {
	state := categoryViewState{View: values.Get("view")}
	if state.View == "" {
		state.View = "categories"
	}
	if state.View != "categories" && state.View != "structure" {
		return categoryViewState{}, &domain.ValidationError{Field: "view", Message: "invalid category view", Key: "error.invalid_category_view"}
	}
	var err error
	if state.DepartmentID, err = optionalCategoryID(values.Get("department_id"), "department_id"); err != nil {
		return categoryViewState{}, err
	}
	if state.DeskID, err = optionalCategoryID(values.Get("desk_id"), "desk_id"); err != nil {
		return categoryViewState{}, err
	}
	if state.DeskID != 0 && state.DepartmentID == 0 {
		return categoryViewState{}, &domain.ValidationError{Field: "department_id", Message: "department is required when a desk is selected", Key: "error.department_required_for_desk"}
	}
	return state, nil
}

func categoryDepartmentValue(id int64) string {
	if id == unassignedDepartmentID {
		return "unassigned"
	}
	return strconv.FormatInt(id, 10)
}

func optionalCategoryID(raw, field string) (int64, error) {
	if raw == "" {
		return 0, nil
	}
	if field == "department_id" && raw == "unassigned" {
		return unassignedDepartmentID, nil
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0, &domain.ValidationError{Field: field, Message: "invalid identifier", Key: "error.invalid_identifier"}
	}
	return id, nil
}

func requiredCategoryID(raw, field string) (int64, error) {
	if raw == "" {
		return 0, &domain.ValidationError{Field: field, Message: "identifier is required", Key: "error.identifier_required"}
	}
	return optionalCategoryID(raw, field)
}

func categoryStatePath(state categoryViewState) string {
	if state.View != "structure" {
		return "/categories"
	}
	return categorySearchClearPath(state)
}

// categorySearchClearPath keeps the validated browser selection while removing
// only the search query. Unlike mutation redirects, it retains a categories
// view selection when the request supplied one.
func categorySearchClearPath(state categoryViewState) string {
	values := url.Values{}
	values.Set("view", state.View)
	if state.DepartmentID != 0 {
		values.Set("department_id", categoryDepartmentValue(state.DepartmentID))
	}
	if state.DepartmentID != 0 && state.DeskID != 0 {
		values.Set("desk_id", strconv.FormatInt(state.DeskID, 10))
	}
	return "/categories?" + values.Encode()
}

func requestWithCategoryState(r *http.Request, state categoryViewState) *http.Request {
	clone := r.Clone(r.Context())
	urlCopy := *clone.URL
	clone.URL = &urlCopy
	query := clone.URL.Query()
	query.Del("view")
	query.Del("department_id")
	query.Del("desk_id")
	query.Set("view", state.View)
	if state.DepartmentID != 0 {
		query.Set("department_id", categoryDepartmentValue(state.DepartmentID))
	}
	if state.DepartmentID != 0 && state.DeskID != 0 {
		query.Set("desk_id", strconv.FormatInt(state.DeskID, 10))
	}
	clone.URL.RawQuery = query.Encode()
	return clone
}

func (h *CategoryHandlers) newDrawerData(r *http.Request, kind string) *categoryDrawerData {
	state, _ := categoryStateFromRequest(r)
	d := &categoryDrawerData{Kind: kind, CloseURL: categoryStatePath(state), View: state.View, SelectedDepartmentID: state.DepartmentID, SelectedDeskID: state.DeskID}
	if d.View != "structure" {
		d.View = "categories"
	}
	d.ActionURL = "/categories"
	switch kind {
	case "department":
		d.ActionURL = "/categories/departments"
	case "desk":
		d.ActionURL = "/categories/desks"
	case "category":
		d.ActionURL = "/categories"
	}
	if h.catalog != nil {
		var err error
		d.Departments, err = h.catalog.ListDepartments(r.Context())
		if err != nil {
			d.ReadError = err
			return d
		}
		for _, department := range d.Departments {
			desks, listErr := h.catalog.ListDesks(r.Context(), department.ID)
			if listErr != nil {
				d.ReadError = listErr
				return d
			}
			d.Desks = append(d.Desks, desks...)
		}
		// A category's location is its Desk, and a legacy Desk keeps a NULL
		// parent reachable only through the virtual Unassigned group. The
		// category drawer must offer those Desks and that virtual Department,
		// or editing a legacy category cannot resubmit the location it already
		// has. The virtual group is presentation-only: no Department row is
		// created, and its value is the same "unassigned" sentinel the
		// structure URLs already use.
		if kind == "category" {
			legacyDesks, listErr := h.catalog.ListDesks(r.Context(), 0)
			if listErr != nil {
				d.ReadError = listErr
				return d
			}
			if len(legacyDesks) > 0 {
				d.Desks = append(d.Desks, legacyDesks...)
				d.Departments = append(d.Departments, domain.CatalogDepartment{
					Department: domain.Department{ID: unassignedDepartmentID, Name: i18n.T(requestLocale(r), "categories.unassigned.name"), Description: i18n.T(requestLocale(r), "categories.unassigned.description")},
				})
			}
		}
	}
	d.DepartmentID = state.DepartmentID
	d.DeskID = state.DeskID
	return d
}

func (h *CategoryHandlers) renderCategoryDrawer(w http.ResponseWriter, r *http.Request, data *categoryDrawerData) {
	h.renderCategoryDrawerStatus(w, r, data, http.StatusOK)
}

func (h *CategoryHandlers) renderCategoryDrawerStatus(w http.ResponseWriter, r *http.Request, data *categoryDrawerData, status int) {
	setCategoriesVary(w)
	if data.ID != 0 {
		switch data.Kind {
		case "department":
			data.ActionURL = "/categories/departments/" + strconv.FormatInt(data.ID, 10) + "/edit"
		case "desk":
			data.ActionURL = "/categories/desks/" + strconv.FormatInt(data.ID, 10) + "/edit"
		default:
			data.ActionURL = "/categories/" + strconv.FormatInt(data.ID, 10) + "/edit"
		}
	}
	if data.ReadError != nil {
		http.Error(w, mapErrorMsg(r, data.ReadError), statusFor(data.ReadError))
		return
	}
	if r.Header.Get("HX-Request") != "" {
		w.Header().Set("HX-Retarget", "#category-drawer-host")
		w.Header().Set("HX-Reswap", "outerHTML")
		h.renderer.Render(w, r, "categories_index", "category_drawer", data, status)
		return
	}
	index, err := h.categoryIndexData(r, "")
	if err != nil {
		http.Error(w, mapErrorMsg(r, err), statusFor(err))
		return
	}
	index.Drawer = data
	h.renderer.Render(w, r, "categories_index", "", index, status)
}

// categoryMutationState validates the view state a category, department or
// desk mutation re-renders after it succeeds. It runs BEFORE the write, so a
// malformed state (issue #345: a desk without its department) is refused with
// nothing stored instead of answering an error for a committed change. It
// answers the refusal itself and reports false.
func (h *CategoryHandlers) categoryMutationState(w http.ResponseWriter, r *http.Request) (categoryViewState, bool) {
	if r.Form == nil {
		_ = r.ParseForm()
	}
	state, err := categoryStateFromValues(r.Form)
	if err != nil {
		http.Error(w, mapErrorMsg(r, err), statusFor(err))
		return categoryViewState{}, false
	}
	return state, true
}

func (h *CategoryHandlers) redirectCategoryMutation(w http.ResponseWriter, r *http.Request, state categoryViewState) bool {
	if r.Header.Get("HX-Request") == "" {
		saveFeedback(w, r, saveFeedbackSaved, saveFeedbackSuccess)
		redirect(w, r, categoryStatePath(state))
		return true
	}
	data, err := h.categoryIndexData(requestWithCategoryState(r, state), "")
	if err != nil {
		h.renderCategoryRefreshError(w, r, state)
		return true
	}
	saveFeedback(w, r, saveFeedbackSaved, saveFeedbackSuccess)
	w.Header().Set("HX-Retarget", "#categories-background")
	w.Header().Set("HX-Reswap", "outerHTML")
	w.Header().Set("HX-Push-Url", categoryStatePath(state))
	w.Header().Set("HX-Trigger-After-Swap", "categories:saved")
	h.renderer.Render(w, r, "categories_index", "categories_background", data, http.StatusOK)
	return true
}

// renderCategoryRefreshError reports a committed mutation separately from a
// failed post-commit projection refresh. It deliberately returns a successful
// swap, so HTMX does not treat the response as a failed mutation and retry it.
func (h *CategoryHandlers) renderCategoryRefreshError(w http.ResponseWriter, r *http.Request, state categoryViewState) {
	message := i18n.T(requestLocale(r), "categories.refresh_error")
	if r.Header.Get("HX-Request") == "" {
		redirect(w, r, categoryStatePath(state))
		return
	}
	w.Header().Set("HX-Retarget", "#categories-background")
	w.Header().Set("HX-Reswap", "outerHTML")
	w.Header().Set("HX-Push-Url", categoryStatePath(state))
	w.Header().Set("HX-Trigger-After-Swap", "categories:saved")
	data := categoriesIndexData{
		pageData:             pageDataFrom(r, "categories"),
		Error:                message,
		View:                 state.View,
		SelectedDepartmentID: state.DepartmentID,
		SelectedDeskID:       state.DeskID,
	}
	data.CategoryAssets, data.PageFoundationAssets = true, true
	h.renderer.Render(w, r, "categories_index", "categories_background", data, http.StatusOK)
}

func (h *CategoryHandlers) createDepartment(w http.ResponseWriter, r *http.Request) {
	if !requireCapability(w, r, application.CapManageCategories) || h.catalog == nil {
		return
	}
	if err := r.ParseForm(); err != nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	state, ok := h.categoryMutationState(w, r)
	if !ok {
		return
	}
	if _, err := h.catalog.CreateDepartmentFor(r.Context(), *userFromContext(r.Context()), r.Form.Get("name"), r.Form.Get("description")); err != nil {
		h.renderCatalogFormError(w, r, "department", 0, err)
		return
	}
	h.redirectCategoryMutation(w, r, state)
}

func (h *CategoryHandlers) updateDepartment(w http.ResponseWriter, r *http.Request) {
	if !requireCapability(w, r, application.CapManageCategories) || h.catalog == nil {
		return
	}
	id, ok := categoryID(r)
	if !ok {
		httpError(w, r, "error.invalid_department_id", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	state, ok := h.categoryMutationState(w, r)
	if !ok {
		return
	}
	if _, err := h.catalog.UpdateDepartmentFor(r.Context(), *userFromContext(r.Context()), id, r.Form.Get("name"), r.Form.Get("description")); err != nil {
		h.renderCatalogFormError(w, r, "department", id, err)
		return
	}
	h.redirectCategoryMutation(w, r, state)
}

func (h *CategoryHandlers) deleteDepartment(w http.ResponseWriter, r *http.Request) {
	if !requireCapability(w, r, application.CapManageCategories) || h.catalog == nil {
		return
	}
	id, ok := categoryID(r)
	if !ok {
		httpError(w, r, "error.invalid_department_id", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	state, ok := h.categoryMutationState(w, r)
	if !ok {
		return
	}
	if err := h.catalog.DeleteDepartmentFor(r.Context(), *userFromContext(r.Context()), id); err != nil {
		h.renderCategoryDeleteRefusal(w, r, err)
		return
	}
	h.redirectCategoryMutation(w, r, state)
}

func (h *CategoryHandlers) createDesk(w http.ResponseWriter, r *http.Request) {
	if !requireCapability(w, r, application.CapManageCategories) || h.catalog == nil {
		return
	}
	if err := r.ParseForm(); err != nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	state, ok := h.categoryMutationState(w, r)
	if !ok {
		return
	}
	departmentID, parseErr := requiredCategoryID(r.Form.Get("department_id"), "department_id")
	if parseErr != nil {
		h.renderCatalogFormError(w, r, "desk", 0, parseErr)
		return
	}
	if _, err := h.catalog.CreateDeskFor(r.Context(), *userFromContext(r.Context()), departmentID, r.Form.Get("name"), r.Form.Get("description")); err != nil {
		h.renderCatalogFormError(w, r, "desk", 0, err)
		return
	}
	h.redirectCategoryMutation(w, r, state)
}

func (h *CategoryHandlers) updateDesk(w http.ResponseWriter, r *http.Request) {
	if !requireCapability(w, r, application.CapManageCategories) || h.catalog == nil {
		return
	}
	id, ok := categoryID(r)
	if !ok {
		httpError(w, r, "error.invalid_desk_id", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	state, ok := h.categoryMutationState(w, r)
	if !ok {
		return
	}
	departmentID, parseErr := requiredCategoryID(r.Form.Get("department_id"), "department_id")
	if parseErr != nil {
		h.renderCatalogFormError(w, r, "desk", id, parseErr)
		return
	}
	if err := h.catalog.UpdateDeskFor(r.Context(), *userFromContext(r.Context()), id, departmentID, r.Form.Get("name"), r.Form.Get("description")); err != nil {
		h.renderCatalogFormError(w, r, "desk", id, err)
		return
	}
	h.redirectCategoryMutation(w, r, state)
}

func (h *CategoryHandlers) deleteDesk(w http.ResponseWriter, r *http.Request) {
	if !requireCapability(w, r, application.CapManageCategories) || h.catalog == nil {
		return
	}
	id, ok := categoryID(r)
	if !ok {
		httpError(w, r, "error.invalid_desk_id", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	state, ok := h.categoryMutationState(w, r)
	if !ok {
		return
	}
	if err := h.catalog.DeleteDeskFor(r.Context(), *userFromContext(r.Context()), id); err != nil {
		h.renderCategoryDeleteRefusal(w, r, err)
		return
	}
	h.redirectCategoryMutation(w, r, state)
}

func categoryID(r *http.Request) (int64, bool) {
	id := parseID(r.PathValue("id"))
	return id, id != 0
}
