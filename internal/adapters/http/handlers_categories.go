package httpadapter

import (
	"net/http"
	"strconv"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// CategoryHandlers implement the category management routes (design "HTTP
// Layer" route table): GET /categories, GET/POST /categories/new + POST
// /categories (create), GET/POST /categories/{id}/edit (rename), POST
// /categories/{id}/delete. Deletes of referenced categories are rejected
// 409 (category-management spec).
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
	mux.HandleFunc("POST /categories/{id}/edit", h.update)
	mux.HandleFunc("POST /categories/{id}/delete", h.delete)
	mux.HandleFunc("POST /categories/departments", h.createDepartment)
	mux.HandleFunc("POST /categories/departments/{id}/delete", h.deleteDepartment)
	mux.HandleFunc("POST /categories/departments/{id}/edit", h.updateDepartment)
	mux.HandleFunc("POST /categories/areas", h.createArea)
	mux.HandleFunc("POST /categories/areas/{id}/delete", h.deleteArea)
	mux.HandleFunc("POST /categories/areas/{id}/edit", h.updateArea)
}

// categoriesIndexData is the category list payload; Error carries a
// rejected-delete message (409 re-render).
type categoriesIndexData struct {
	pageData
	Error       string
	Categories  []domain.Category
	Badges      map[int64]string
	Departments []domain.CatalogDepartment
	Areas       []domain.CatalogArea
}

func (h *CategoryHandlers) index(w http.ResponseWriter, r *http.Request) {
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
	AreaID      int64
	Areas       []domain.CatalogArea
	Departments []domain.CatalogDepartment
}

func (h *CategoryHandlers) newForm(w http.ResponseWriter, r *http.Request) {
	if !requireCapability(w, r, application.CapManageCategories) {
		return
	}
	data := categoryFormData{pageData: pageDataFrom(r, "categories")}
	h.populateCategoryForm(r, &data)
	h.renderer.Render(w, r, "categories_new", "category_form", data, http.StatusOK)
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
		areaID, _ := strconv.ParseInt(r.Form.Get("area_id"), 10, 64)
		_, err = h.categories.CreateWithDescriptionFor(r.Context(), *userFromContext(r.Context()), r.Form.Get("name"), r.Form.Get("description"), areaID)
	} else {
		_, err = h.categories.CreateFor(r.Context(), *userFromContext(r.Context()), r.Form.Get("name"))
	}
	if err != nil {
		h.renderCategoryFormError(w, r, 0, err)
		return
	}
	redirect(w, r, "/categories")
}

func (h *CategoryHandlers) editForm(w http.ResponseWriter, r *http.Request) {
	if !requireCapability(w, r, application.CapManageCategories) {
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
	data := categoryFormData{pageData: pageDataFrom(r, "categories"), CategoryID: id, Name: c.Name, Description: c.Description, AreaID: c.AreaID}
	h.populateCategoryForm(r, &data)
	h.renderer.Render(w, r, "categories_new", "category_form", data, http.StatusOK)
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
			c.AreaID, _ = strconv.ParseInt(r.Form.Get("area_id"), 10, 64)
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
	redirect(w, r, "/categories")
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
	if err := h.categories.DeleteFor(r.Context(), *userFromContext(r.Context()), id); err != nil {
		status, msg := mapError(err)
		if status == http.StatusInternalServerError {
			http.Error(w, msg, status)
			return
		}
		h.renderCategoriesIndexError(w, r, msg, status)
		return
	}
	redirect(w, r, "/categories")
}

// renderCategoryFormError re-renders the category form with the submitted
// name and the mapped status.
func (h *CategoryHandlers) renderCategoryFormError(w http.ResponseWriter, r *http.Request, id int64, err error) {
	status, msg := mapError(err)
	if status == http.StatusInternalServerError {
		http.Error(w, msg, status)
		return
	}
	data := categoryFormData{
		pageData:    pageDataFrom(r, "categories"),
		Error:       msg,
		CategoryID:  id,
		Name:        r.Form.Get("name"),
		Description: r.Form.Get("description"),
	}
	data.AreaID, _ = strconv.ParseInt(r.Form.Get("area_id"), 10, 64)
	h.populateCategoryForm(r, &data)
	h.renderer.Render(w, r, "categories_new", "category_form", data, status)
}

// renderCategoriesIndexError re-renders the category list with an inline
// error (rejected delete).
func (h *CategoryHandlers) renderCategoriesIndexError(w http.ResponseWriter, r *http.Request, msg string, status int) {
	h.renderIndex(w, r, msg, status)
}

func (h *CategoryHandlers) renderIndex(w http.ResponseWriter, r *http.Request, message string, status int) {
	categories, err := h.categories.List(r.Context())
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	badges := make(map[int64]string)
	if h.workflows != nil {
		summaries, err := h.workflows.ListSummaries(r.Context(), *userFromContext(r.Context()))
		if err != nil {
			http.Error(w, mapErrorMsg(err), statusFor(err))
			return
		}
		for _, summary := range summaries {
			badges[summary.CategoryID] = summary.Badge
		}
	}
	data := categoriesIndexData{pageData: pageDataFrom(r, "categories"), Error: message, Categories: categories, Badges: badges}
	if h.catalog != nil {
		data.Departments, _ = h.catalog.ListDepartments(r.Context())
		for _, department := range data.Departments {
			areas, _ := h.catalog.ListAreas(r.Context(), department.ID)
			data.Areas = append(data.Areas, areas...)
		}
	}
	h.renderer.Render(w, r, "categories_index", "", data, status)
}

func (h *CategoryHandlers) populateCategoryForm(r *http.Request, data *categoryFormData) {
	if h.catalog == nil {
		return
	}
	data.Departments, _ = h.catalog.ListDepartments(r.Context())
	for _, department := range data.Departments {
		areas, _ := h.catalog.ListAreas(r.Context(), department.ID)
		data.Areas = append(data.Areas, areas...)
	}
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
		h.renderCategoriesIndexError(w, r, mapErrorMsg(err), statusFor(err))
		return
	}
	redirect(w, r, "/categories")
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
		h.renderCategoriesIndexError(w, r, mapErrorMsg(err), statusFor(err))
		return
	}
	redirect(w, r, "/categories")
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
	if err := h.catalog.DeleteDepartmentFor(r.Context(), *userFromContext(r.Context()), id); err != nil {
		h.renderCategoriesIndexError(w, r, mapErrorMsg(err), statusFor(err))
		return
	}
	redirect(w, r, "/categories")
}

func (h *CategoryHandlers) createArea(w http.ResponseWriter, r *http.Request) {
	if !requireCapability(w, r, application.CapManageCategories) || h.catalog == nil {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Internal server error", 500)
		return
	}
	departmentID, _ := strconv.ParseInt(r.Form.Get("department_id"), 10, 64)
	if _, err := h.catalog.CreateAreaFor(r.Context(), *userFromContext(r.Context()), departmentID, r.Form.Get("name"), r.Form.Get("description")); err != nil {
		h.renderCategoriesIndexError(w, r, mapErrorMsg(err), statusFor(err))
		return
	}
	redirect(w, r, "/categories")
}

func (h *CategoryHandlers) updateArea(w http.ResponseWriter, r *http.Request) {
	if !requireCapability(w, r, application.CapManageCategories) || h.catalog == nil {
		return
	}
	id, ok := categoryID(r)
	if !ok {
		http.Error(w, "invalid area id", 400)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Internal server error", 500)
		return
	}
	departmentID, _ := strconv.ParseInt(r.Form.Get("department_id"), 10, 64)
	if _, err := h.catalog.UpdateAreaFor(r.Context(), *userFromContext(r.Context()), id, departmentID, r.Form.Get("name"), r.Form.Get("description")); err != nil {
		h.renderCategoriesIndexError(w, r, mapErrorMsg(err), statusFor(err))
		return
	}
	redirect(w, r, "/categories")
}

func (h *CategoryHandlers) deleteArea(w http.ResponseWriter, r *http.Request) {
	if !requireCapability(w, r, application.CapManageCategories) || h.catalog == nil {
		return
	}
	id, ok := categoryID(r)
	if !ok {
		http.Error(w, "invalid area id", 400)
		return
	}
	if err := h.catalog.DeleteAreaFor(r.Context(), *userFromContext(r.Context()), id); err != nil {
		h.renderCategoriesIndexError(w, r, mapErrorMsg(err), statusFor(err))
		return
	}
	redirect(w, r, "/categories")
}

func categoryID(r *http.Request) (int64, bool) {
	id := parseID(r.PathValue("id"))
	return id, id != 0
}
