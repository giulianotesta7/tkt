package httpadapter

import (
	"net/http"
	"strconv"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// UserHandlers implement the managed-user routes (design "HTTP Layer" route
// table): GET /users, GET/POST /users/new + /users (create), GET/POST
// /users/{id}/edit (update incl. password + deactivate), POST
// /users/{id}/delete. Deactivation through the edit form is the D14 hook:
// the middleware destroys the deactivated user's sessions on their next
// request.
type UserHandlers struct {
	users    *application.UserService
	renderer *Renderer
}

// NewUserHandlers wires the user routes against the user use cases and the
// renderer.
func NewUserHandlers(users *application.UserService, renderer *Renderer) *UserHandlers {
	return &UserHandlers{users: users, renderer: renderer}
}

// Register mounts the user routes.
func (h *UserHandlers) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /users", h.index)
	mux.HandleFunc("GET /users/new", h.newForm)
	mux.HandleFunc("POST /users", h.create)
	mux.HandleFunc("GET /users/{id}/edit", h.editForm)
	mux.HandleFunc("POST /users/{id}/edit", h.update)
	mux.HandleFunc("POST /users/{id}/password", h.changePassword)
	mux.HandleFunc("POST /users/{id}/delete", h.delete)
}

// usersIndexData is the managed-users list payload; Error carries a
// rejected-delete message (409 re-render).
type usersIndexData struct {
	pageData
	Error string
	Users []domain.User
}

func (h *UserHandlers) index(w http.ResponseWriter, r *http.Request) {
	if !requireCapability(w, r, application.CapManageUsers) {
		return
	}
	actor := *userFromContext(r.Context())
	users, err := h.users.List(r.Context(), actor)
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	data := usersIndexData{pageData: pageDataFrom(r, "users"), Users: users}
	h.renderer.Render(w, r, "users_index", "", data, http.StatusOK)
}

// userFormData is the create/edit form payload. UserID 0 = create.
type userFormData struct {
	pageData
	Error  string
	UserID int64
	Values userFormValues
}

type userFormValues struct {
	Name   string
	Email  string
	Role   domain.Role
	Active bool
}

func (h *UserHandlers) newForm(w http.ResponseWriter, r *http.Request) {
	if !requireCapability(w, r, application.CapManageUsers) {
		return
	}
	data := userFormData{pageData: pageDataFrom(r, "users"), Values: userFormValues{Active: true}}
	h.renderer.Render(w, r, "users_new", "user_form", data, http.StatusOK)
}

func (h *UserHandlers) create(w http.ResponseWriter, r *http.Request) {
	if !requireCapability(w, r, application.CapManageUsers) {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	_, err := h.users.Create(r.Context(), *userFromContext(r.Context()), application.CreateUserInput{
		Name:     r.Form.Get("name"),
		Email:    r.Form.Get("email"),
		Password: r.Form.Get("password"),
	})
	if err != nil {
		h.renderUserFormError(w, r, 0, err)
		return
	}
	redirect(w, r, "/users")
}

func (h *UserHandlers) editForm(w http.ResponseWriter, r *http.Request) {
	if !requireCapability(w, r, application.CapManageUsers) {
		return
	}
	id, ok := userID(r)
	if !ok {
		http.Error(w, "invalid user id", http.StatusBadRequest)
		return
	}
	u, err := h.users.GetByID(r.Context(), id)
	if err != nil {
		http.Error(w, mapErrorMsg(err), statusFor(err))
		return
	}
	data := userFormData{
		pageData: pageDataFrom(r, "users"),
		UserID:   id,
		Values:   userFormValues{Name: u.Name, Email: u.Email, Role: u.Role, Active: u.Active},
	}
	h.renderer.Render(w, r, "users_new", "user_form", data, http.StatusOK)
}

// update applies the complete non-password managed-user edit atomically.
func (h *UserHandlers) update(w http.ResponseWriter, r *http.Request) {
	if !requireCapability(w, r, application.CapManageUsers) {
		return
	}
	id, ok := userID(r)
	if !ok {
		http.Error(w, "invalid user id", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	name := r.Form.Get("name")
	email := r.Form.Get("email")
	role, err := domain.ParseRole(r.Form.Get("role"))
	if err != nil {
		if r.Form.Get("role") != "" {
			h.renderUserFormError(w, r, id, &domain.ValidationError{Field: "role", Message: "invalid role"})
			return
		}
		u, getErr := h.users.GetByID(r.Context(), id)
		if getErr != nil {
			h.renderUserFormError(w, r, id, getErr)
			return
		}
		role = u.Role
	}
	active := r.Form.Get("active") == "true" || r.Form.Get("active") == "on"
	if _, err := h.users.UpdateManagedUser(r.Context(), *userFromContext(r.Context()), id, application.UpdateManagedUserInput{Name: name, Email: email, Role: role, Active: active}); err != nil {
		h.renderUserFormError(w, r, id, err)
		return
	}
	redirect(w, r, "/users")
}

func (h *UserHandlers) changePassword(w http.ResponseWriter, r *http.Request) {
	if !requireCapability(w, r, application.CapManageUsers) {
		return
	}
	id, ok := userID(r)
	if !ok {
		http.Error(w, "invalid user id", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	if err := h.users.ChangePassword(r.Context(), *userFromContext(r.Context()), id, r.Form.Get("password")); err != nil {
		h.renderUserFormError(w, r, id, err)
		return
	}
	redirect(w, r, "/users/"+strconv.FormatInt(id, 10)+"/edit")
}

func (h *UserHandlers) delete(w http.ResponseWriter, r *http.Request) {
	if !requireCapability(w, r, application.CapManageUsers) {
		return
	}
	id, ok := userID(r)
	if !ok {
		http.Error(w, "invalid user id", http.StatusBadRequest)
		return
	}
	if err := h.users.Delete(r.Context(), *userFromContext(r.Context()), id); err != nil {
		status, msg := mapError(err)
		if status == http.StatusInternalServerError {
			http.Error(w, msg, status)
			return
		}
		h.renderUsersIndexError(w, r, msg, status)
		return
	}
	redirect(w, r, "/users")
}

// renderUserFormError re-renders the user form with the submitted values
// and the mapped status (HX → user_form fragment; full → users_new page).
func (h *UserHandlers) renderUserFormError(w http.ResponseWriter, r *http.Request, id int64, err error) {
	status, msg := mapError(err)
	if status == http.StatusInternalServerError {
		http.Error(w, msg, status)
		return
	}
	data := userFormData{
		pageData: pageDataFrom(r, "users"),
		Error:    msg,
		UserID:   id,
		Values: userFormValues{
			Name:   r.Form.Get("name"),
			Email:  r.Form.Get("email"),
			Role:   domain.Role(r.Form.Get("role")),
			Active: r.Form.Get("active") == "true",
		},
	}
	h.renderer.Render(w, r, "users_new", "user_form", data, status)
}

// renderUsersIndexError re-renders the users list with an inline error
// (rejected delete; HX → content fragment, full → page).
func (h *UserHandlers) renderUsersIndexError(w http.ResponseWriter, r *http.Request, msg string, status int) {
	users, err := h.users.List(r.Context(), *userFromContext(r.Context()))
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	data := usersIndexData{pageData: pageDataFrom(r, "users"), Error: msg, Users: users}
	h.renderer.Render(w, r, "users_index", "", data, status)
}

func userID(r *http.Request) (int64, bool) {
	id := parseID(r.PathValue("id"))
	return id, id != 0
}
