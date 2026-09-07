package httpadapter

import (
	"net/http"
	"net/url"
	"strconv"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
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

// categoriesIndexData is the canonical Categories/Structure administration
// payload. The raw slices remain available for backwards-compatible fragments;
// the view slices provide resolved hierarchy context for the polished shell.
type categoriesIndexData struct {
	pageData
	Error                string
	View                 string
	Categories           []domain.Category
	CategoryRows         []catalogCategoryRow
	Badges               map[int64]string
	Departments          []domain.CatalogDepartment
	Desks                []domain.CatalogDesk
	StructureDepartments []catalogDepartmentView
	StructureDesks       []catalogDeskView
	StructureCategories  []catalogCategoryRow
	SelectedDepartmentID int64
	SelectedDeskID       int64
	Drawer               *categoryDrawerData
}

type catalogDepartmentView struct {
	domain.CatalogDepartment
}

type catalogDeskView struct {
	domain.CatalogDesk
}

type catalogCategoryRow struct {
	domain.Category
	DepartmentName string
	DeskName       string
	HierarchyPath  string
}

type categoryViewState struct {
	View         string
	DepartmentID int64
	DeskID       int64
}

type categoryDrawerData struct {
	Kind                 string
	ID                   int64
	Name                 string
	Description          string
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
	ReadError            error
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
		http.Error(w, mapErrorMsg(err), statusFor(err))
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
		http.Error(w, mapErrorMsg(err), statusFor(err))
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
		http.Error(w, mapErrorMsg(err), statusFor(err))
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
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	var err error
	if h.catalog != nil {
		deskID, parseErr := requiredCategoryID(r.Form.Get("desk_id"), "desk_id")
		if parseErr != nil {
			h.renderCategoryFormError(w, r, 0, parseErr)
			return
		}
		_, err = h.categories.CreateWithDescriptionFor(r.Context(), *userFromContext(r.Context()), r.Form.Get("name"), r.Form.Get("description"), deskID)
	} else {
		_, err = h.categories.CreateFor(r.Context(), *userFromContext(r.Context()), r.Form.Get("name"))
	}
	if err != nil {
		h.renderCategoryFormError(w, r, 0, err)
		return
	}
	h.redirectCategoryMutation(w, r)
}

func (h *CategoryHandlers) editForm(w http.ResponseWriter, r *http.Request) {
	setCategoriesVary(w)
	if !requireCapability(w, r, application.CapManageCategories) {
		return
	}
	if _, err := categoryStateFromRequest(r); err != nil {
		http.Error(w, mapErrorMsg(err), statusFor(err))
		return
	}
	id, ok := categoryID(r)
	if !ok {
		http.Error(w, "invalid category id", http.StatusBadRequest)
		return
	}
	c, err := h.categories.GetByID(r.Context(), id)
	if err != nil {
		http.Error(w, mapErrorMsg(err), statusFor(err))
		return
	}
	d := h.newDrawerData(r, "category")
	d.ID, d.Name, d.Description, d.DeskID = id, c.Name, c.Description, c.DeskID
	for _, desk := range d.Desks {
		if desk.ID == c.DeskID {
			d.DepartmentID = desk.DepartmentID
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
		http.Error(w, mapErrorMsg(err), statusFor(err))
		return
	}
	id, ok := categoryID(r)
	if !ok {
		http.Error(w, "invalid department id", http.StatusBadRequest)
		return
	}
	departments, err := h.catalog.ListDepartments(r.Context())
	if err != nil {
		http.Error(w, mapErrorMsg(err), statusFor(err))
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
	http.Error(w, "department not found", http.StatusNotFound)
}

func (h *CategoryHandlers) editDeskForm(w http.ResponseWriter, r *http.Request) {
	setCategoriesVary(w)
	if !requireCapability(w, r, application.CapManageCategories) || h.catalog == nil {
		return
	}
	if _, err := categoryStateFromRequest(r); err != nil {
		http.Error(w, mapErrorMsg(err), statusFor(err))
		return
	}
	id, ok := categoryID(r)
	if !ok {
		http.Error(w, "invalid desk id", http.StatusBadRequest)
		return
	}
	departments, err := h.catalog.ListDepartments(r.Context())
	if err != nil {
		http.Error(w, mapErrorMsg(err), statusFor(err))
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
			http.Error(w, mapErrorMsg(listErr), statusFor(listErr))
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
	http.Error(w, "desk not found", http.StatusNotFound)
}

func (h *CategoryHandlers) update(w http.ResponseWriter, r *http.Request) {
	if !requireCapability(w, r, application.CapManageCategories) {
		return
	}
	id, ok := categoryID(r)
	if !ok {
		http.Error(w, "invalid category id", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	var err error
	if h.catalog != nil {
		c, getErr := h.categories.GetByID(r.Context(), id)
		if getErr == nil {
			c.Name, c.Description = r.Form.Get("name"), r.Form.Get("description")
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
	h.redirectCategoryMutation(w, r)
}

func (h *CategoryHandlers) delete(w http.ResponseWriter, r *http.Request) {
	if !requireCapability(w, r, application.CapManageCategories) {
		return
	}
	id, ok := categoryID(r)
	if !ok {
		http.Error(w, "invalid category id", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	if err := h.categories.DeleteFor(r.Context(), *userFromContext(r.Context()), id); err != nil {
		status, msg := mapError(err)
		if status == http.StatusInternalServerError {
			http.Error(w, msg, status)
			return
		}
		h.renderCategoriesIndexError(w, r, msg, status)
		return
	}
	h.redirectCategoryMutation(w, r)
}

// renderCatalogFormError re-renders a catalog drawer with submitted values.
func (h *CategoryHandlers) renderCatalogFormError(w http.ResponseWriter, r *http.Request, kind string, id int64, err error) {
	status, msg := mapError(err)
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
	d.ID, d.Name, d.Description, d.Error = id, r.Form.Get("name"), r.Form.Get("description"), msg
	d.HasServerError = true
	h.renderCategoryDrawerStatus(w, r, d, status)
}

// renderCategoryFormError re-renders the category drawer with submitted values.
func (h *CategoryHandlers) renderCategoryFormError(w http.ResponseWriter, r *http.Request, id int64, err error) {
	h.renderCatalogFormError(w, r, "category", id, err)
}

// renderCategoriesIndexError re-renders the category list with an inline
// error (rejected delete).
func (h *CategoryHandlers) renderCategoriesIndexError(w http.ResponseWriter, r *http.Request, msg string, status int) {
	h.renderIndex(w, r, msg, status)
}

func (h *CategoryHandlers) renderIndex(w http.ResponseWriter, r *http.Request, message string, status int) {
	data, err := h.categoryIndexData(r, message)
	if err != nil {
		http.Error(w, mapErrorMsg(err), statusFor(err))
		return
	}
	h.renderer.Render(w, r, "categories_index", "", data, status)
}

func (h *CategoryHandlers) categoryIndexData(r *http.Request, message string) (categoriesIndexData, error) {
	categories, err := h.categories.List(r.Context())
	if err != nil {
		return categoriesIndexData{}, err
	}
	state, err := categoryStateFromRequest(r)
	if err != nil {
		return categoriesIndexData{}, err
	}
	data := categoriesIndexData{pageData: pageDataFrom(r, "categories"), Error: message, View: state.View}
	data.CategoryAssets, data.PageFoundationAssets = true, true
	data.SelectedDepartmentID, data.SelectedDeskID = state.DepartmentID, state.DeskID
	data.Badges = make(map[int64]string)
	if h.workflows != nil {
		summaries, summaryErr := h.workflows.ListSummaries(r.Context(), *userFromContext(r.Context()))
		if summaryErr != nil {
			return categoriesIndexData{}, summaryErr
		}
		for _, summary := range summaries {
			data.Badges[summary.CategoryID] = summary.Badge
		}
	}
	if h.catalog == nil {
		// Focused legacy handler tests may omit the catalog projection. Keep the
		// unified renderer useful by exposing their categories in the final
		// column; production composition always supplies the catalog service.
		data.SelectedDepartmentID = 1
		data.SelectedDeskID = 1
		for _, category := range categories {
			row := catalogCategoryRow{Category: category}
			data.CategoryRows = append(data.CategoryRows, row)
			data.StructureCategories = append(data.StructureCategories, row)
		}
		return data, nil
	}
	data.Departments, err = h.catalog.ListDepartments(r.Context())
	if err != nil {
		return categoriesIndexData{}, err
	}
	departmentNames := make(map[int64]string, len(data.Departments)+1)
	deskNames := make(map[int64]string)
	deskDepartmentNames := make(map[int64]string)
	for _, department := range data.Departments {
		departmentNames[department.ID] = department.Name
		desks, listErr := h.catalog.ListDesks(r.Context(), department.ID)
		if listErr != nil {
			return categoriesIndexData{}, listErr
		}
		data.Desks = append(data.Desks, desks...)
		data.StructureDepartments = append(data.StructureDepartments, catalogDepartmentView{CatalogDepartment: department})
		for _, desk := range desks {
			deskNames[desk.ID] = desk.Name
			deskDepartmentNames[desk.ID] = department.Name
		}
	}
	// Legacy desks keep their NULL parent in storage. Expose them through a
	// virtual group only; no Department row is created for this presentation.
	unassigned, err := h.catalog.ListDesks(r.Context(), 0)
	if err != nil {
		return categoriesIndexData{}, err
	}
	if len(unassigned) > 0 {
		data.Desks = append(data.Desks, unassigned...)
		departmentNames[unassignedDepartmentID] = "Unassigned"
		var categoryCount int
		for _, desk := range unassigned {
			deskNames[desk.ID] = desk.Name
			deskDepartmentNames[desk.ID] = "Unassigned"
			categoryCount += desk.CategoryCount
		}
		data.StructureDepartments = append(data.StructureDepartments, catalogDepartmentView{CatalogDepartment: domain.CatalogDepartment{
			Department: domain.Department{ID: unassignedDepartmentID, Name: "Unassigned", Description: "Desks without a department"},
			DeskCount:  len(unassigned), CategoryCount: categoryCount,
		}})
	}
	if data.SelectedDepartmentID != 0 {
		if _, ok := departmentNames[data.SelectedDepartmentID]; !ok {
			data.SelectedDepartmentID = 0
			data.SelectedDeskID = 0
		}
	}
	if data.SelectedDeskID != 0 {
		desk, ok := deskNames[data.SelectedDeskID]
		if !ok || desk == "" {
			data.SelectedDeskID = 0
		} else {
			for _, candidate := range data.Desks {
				matchesDepartment := candidate.DepartmentID == data.SelectedDepartmentID
				if data.SelectedDepartmentID == unassignedDepartmentID {
					matchesDepartment = candidate.DepartmentID == 0
				}
				if candidate.ID == data.SelectedDeskID && !matchesDepartment {
					data.SelectedDeskID = 0
					break
				}
			}
		}
	}
	for _, category := range categories {
		row := catalogCategoryRow{Category: category, DepartmentName: deskDepartmentNames[category.DeskID], DeskName: deskNames[category.DeskID]}
		if row.DepartmentName != "" {
			row.HierarchyPath = row.DepartmentName + " / " + row.DeskName
		}
		data.CategoryRows = append(data.CategoryRows, row)
	}
	// data.Desks is the complete desktop projection assembled above, including
	// desks from every Department and the virtual Unassigned group. An explicit
	// Department selection narrows that projection; the zero selection keeps all
	// desks visible without overloading ListDesks(0)'s legacy-only meaning.
	for _, desk := range data.Desks {
		matchesDepartment := data.SelectedDepartmentID == 0 || desk.DepartmentID == data.SelectedDepartmentID
		if data.SelectedDepartmentID == unassignedDepartmentID {
			matchesDepartment = desk.DepartmentID == 0
		}
		if matchesDepartment {
			data.StructureDesks = append(data.StructureDesks, catalogDeskView{CatalogDesk: desk})
		}
	}
	if data.SelectedDeskID != 0 {
		catalogCategories, listErr := h.catalog.ListCategories(r.Context(), data.SelectedDeskID)
		if listErr != nil {
			return categoriesIndexData{}, listErr
		}
		for _, category := range catalogCategories {
			data.StructureCategories = append(data.StructureCategories, catalogCategoryRow{Category: category.Category, DepartmentName: category.DepartmentName, DeskName: category.DeskName, HierarchyPath: category.DepartmentName + " / " + category.DeskName})
		}
	}
	return data, nil
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
		return categoryViewState{}, &domain.ValidationError{Field: "view", Message: "invalid category view"}
	}
	var err error
	if state.DepartmentID, err = optionalCategoryID(values.Get("department_id"), "department_id"); err != nil {
		return categoryViewState{}, err
	}
	if state.DeskID, err = optionalCategoryID(values.Get("desk_id"), "desk_id"); err != nil {
		return categoryViewState{}, err
	}
	if state.DeskID != 0 && state.DepartmentID == 0 {
		return categoryViewState{}, &domain.ValidationError{Field: "department_id", Message: "department is required when a desk is selected"}
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
		return 0, &domain.ValidationError{Field: field, Message: "invalid identifier"}
	}
	return id, nil
}

func requiredCategoryID(raw, field string) (int64, error) {
	if raw == "" {
		return 0, &domain.ValidationError{Field: field, Message: "identifier is required"}
	}
	return optionalCategoryID(raw, field)
}

func categoryStatePath(state categoryViewState) string {
	if state.View != "structure" {
		return "/categories"
	}
	values := url.Values{}
	values.Set("view", "structure")
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
		http.Error(w, mapErrorMsg(data.ReadError), statusFor(data.ReadError))
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
		http.Error(w, mapErrorMsg(err), statusFor(err))
		return
	}
	index.Drawer = data
	h.renderer.Render(w, r, "categories_index", "", index, status)
}

func (h *CategoryHandlers) redirectCategoryMutation(w http.ResponseWriter, r *http.Request) bool {
	if r.Form == nil {
		_ = r.ParseForm()
	}
	state, err := categoryStateFromValues(r.Form)
	if err != nil {
		http.Error(w, mapErrorMsg(err), statusFor(err))
		return true
	}
	if r.Header.Get("HX-Request") == "" {
		redirect(w, r, categoryStatePath(state))
		return true
	}
	data, err := h.categoryIndexData(requestWithCategoryState(r, state), "")
	if err != nil {
		h.renderCategoryRefreshError(w, r, state)
		return true
	}
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
	message := "Changes saved, but the category list could not be refreshed. Reload the page to see the saved changes."
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
		http.Error(w, "Internal server error", 500)
		return
	}
	if _, err := h.catalog.CreateDepartmentFor(r.Context(), *userFromContext(r.Context()), r.Form.Get("name"), r.Form.Get("description")); err != nil {
		h.renderCatalogFormError(w, r, "department", 0, err)
		return
	}
	h.redirectCategoryMutation(w, r)
}

func (h *CategoryHandlers) updateDepartment(w http.ResponseWriter, r *http.Request) {
	if !requireCapability(w, r, application.CapManageCategories) || h.catalog == nil {
		return
	}
	id, ok := categoryID(r)
	if !ok {
		http.Error(w, "invalid department id", 400)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Internal server error", 500)
		return
	}
	if _, err := h.catalog.UpdateDepartmentFor(r.Context(), *userFromContext(r.Context()), id, r.Form.Get("name"), r.Form.Get("description")); err != nil {
		h.renderCatalogFormError(w, r, "department", id, err)
		return
	}
	h.redirectCategoryMutation(w, r)
}

func (h *CategoryHandlers) deleteDepartment(w http.ResponseWriter, r *http.Request) {
	if !requireCapability(w, r, application.CapManageCategories) || h.catalog == nil {
		return
	}
	id, ok := categoryID(r)
	if !ok {
		http.Error(w, "invalid department id", 400)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Internal server error", 500)
		return
	}
	if err := h.catalog.DeleteDepartmentFor(r.Context(), *userFromContext(r.Context()), id); err != nil {
		h.renderCategoriesIndexError(w, r, mapErrorMsg(err), statusFor(err))
		return
	}
	h.redirectCategoryMutation(w, r)
}

func (h *CategoryHandlers) createDesk(w http.ResponseWriter, r *http.Request) {
	if !requireCapability(w, r, application.CapManageCategories) || h.catalog == nil {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Internal server error", 500)
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
	h.redirectCategoryMutation(w, r)
}

func (h *CategoryHandlers) updateDesk(w http.ResponseWriter, r *http.Request) {
	if !requireCapability(w, r, application.CapManageCategories) || h.catalog == nil {
		return
	}
	id, ok := categoryID(r)
	if !ok {
		http.Error(w, "invalid desk id", 400)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Internal server error", 500)
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
	h.redirectCategoryMutation(w, r)
}

func (h *CategoryHandlers) deleteDesk(w http.ResponseWriter, r *http.Request) {
	if !requireCapability(w, r, application.CapManageCategories) || h.catalog == nil {
		return
	}
	id, ok := categoryID(r)
	if !ok {
		http.Error(w, "invalid desk id", 400)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Internal server error", 500)
		return
	}
	if err := h.catalog.DeleteDeskFor(r.Context(), *userFromContext(r.Context()), id); err != nil {
		h.renderCategoriesIndexError(w, r, mapErrorMsg(err), statusFor(err))
		return
	}
	h.redirectCategoryMutation(w, r)
}

func categoryID(r *http.Request) (int64, bool) {
	id := parseID(r.PathValue("id"))
	return id, id != 0
}
