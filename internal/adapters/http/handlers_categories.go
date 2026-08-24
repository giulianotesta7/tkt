package httpadapter

import (
	"net/http"

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
}

// NewCategoryHandlers wires category routes without workflow projections. It
// remains available for focused legacy handler tests.
func NewCategoryHandlers(categories *application.CategoryService, renderer *Renderer) *CategoryHandlers {
	return &CategoryHandlers{categories: categories, renderer: renderer}
}

// NewCategoryHandlersWithWorkflows adds derived workflow badges to the category
// index used by the production and integration composition roots.
func NewCategoryHandlersWithWorkflows(categories *application.CategoryService, workflows *application.WorkflowService, renderer *Renderer) *CategoryHandlers {
	return &CategoryHandlers{categories: categories, workflows: workflows, renderer: renderer}
}

// Register mounts the category routes.
func (h *CategoryHandlers) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /categories", h.index)
	mux.HandleFunc("GET /categories/new", h.newForm)
	mux.HandleFunc("POST /categories", h.create)
	mux.HandleFunc("GET /categories/{id}/edit", h.editForm)
	mux.HandleFunc("POST /categories/{id}/edit", h.update)
	mux.HandleFunc("POST /categories/{id}/delete", h.delete)
}

// categoriesIndexData is the category list payload; Error carries a
// rejected-delete message (409 re-render).
type categoriesIndexData struct {
	pageData
	Error      string
	Categories []domain.Category
	Badges     map[int64]string
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
	Error      string
	CategoryID int64
	Name       string
}

func (h *CategoryHandlers) newForm(w http.ResponseWriter, r *http.Request) {
	if !requireCapability(w, r, application.CapManageCategories) {
		return
	}
	data := categoryFormData{pageData: pageDataFrom(r, "categories")}
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
	if _, err := h.categories.CreateFor(r.Context(), *userFromContext(r.Context()), r.Form.Get("name")); err != nil {
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
	data := categoryFormData{pageData: pageDataFrom(r, "categories"), CategoryID: id, Name: c.Name}
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
	if _, err := h.categories.RenameFor(r.Context(), *userFromContext(r.Context()), id, r.Form.Get("name")); err != nil {
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
		pageData:   pageDataFrom(r, "categories"),
		Error:      msg,
		CategoryID: id,
		Name:       r.Form.Get("name"),
	}
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
	h.renderer.Render(w, r, "categories_index", "", data, status)
}

func categoryID(r *http.Request) (int64, bool) {
	id := parseID(r.PathValue("id"))
	return id, id != 0
}
