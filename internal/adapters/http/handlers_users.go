package httpadapter

import (
	"net/http"
	"strconv"
	"strings"
	"time"

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

type usersStatus string

const (
	usersStatusAll         usersStatus = "all"
	usersStatusActive      usersStatus = "active"
	usersStatusDeactivated usersStatus = "deactivated"
)

type usersCounts struct {
	All         int
	Active      int
	Deactivated int
}

type userRowView struct {
	ID          int64
	Initials    string
	Name        string
	Email       string
	RoleLabel   string
	StatusLabel string
	Active      bool
	CreatedAt   time.Time
	CanManage   bool
	EditHref    string
	EditURL     string
	LauncherID  string
}

type userRoleOption struct {
	Value       domain.Role
	Label       string
	Description string
}

type userDrawerData struct {
	Error           string
	ErrorField      string
	UserID          int64
	Values          userFormValues
	RoleOptions     []userRoleOption
	RoleDescription string
	Status          usersStatus
	ListURL         string
	EditURL         string
	HasServerError  bool
}

// usersIndexData is the managed-users list payload. Users remains for
// compatibility with the create/delete form paths while Rows powers the Users
// screen.
type usersIndexData struct {
	pageData
	Error        string
	Users        []domain.User
	Status       usersStatus
	ListURL      string
	Counts       usersCounts
	Rows         []userRowView
	EmptyMessage string
	Drawer       *userDrawerData
}

func normalizeUsersStatus(raw string) usersStatus {
	switch usersStatus(raw) {
	case usersStatusActive:
		return usersStatusActive
	case usersStatusDeactivated:
		return usersStatusDeactivated
	default:
		return usersStatusAll
	}
}

func usersListURL(status usersStatus) string {
	if status == usersStatusAll {
		return "/users"
	}
	return "/users?status=" + string(status)
}

func userRoleLabel(role domain.Role) string {
	switch role {
	case domain.RoleUser:
		return "User"
	case domain.RoleAgent:
		return "Agent"
	case domain.RoleAdmin:
		return "Admin"
	case domain.RoleRoot:
		return "Root"
	default:
		return "Unknown"
	}
}

func roleDescription(role domain.Role) string {
	switch role {
	case domain.RoleUser:
		return "Standard user access."
	case domain.RoleAgent:
		return "Includes User access and agent permissions."
	case domain.RoleAdmin:
		return "Includes Agent access and user management. Only Root can grant this role."
	case domain.RoleRoot:
		return "Highest access, including Admin capabilities. Protected from user-management changes."
	default:
		return ""
	}
}

func usersInitials(name string) string {
	parts := strings.Fields(name)
	if len(parts) == 0 {
		return "?"
	}
	out := strings.ToUpper(parts[0][:1])
	if len(parts) > 1 {
		out += strings.ToUpper(parts[len(parts)-1][:1])
	}
	return out
}

func usersRoleOptions(actor domain.Role) []userRoleOption {
	policy := application.NewPolicy()
	options := make([]userRoleOption, 0, 3)
	for _, role := range []domain.Role{domain.RoleUser, domain.RoleAgent, domain.RoleAdmin} {
		if policy.CanGrantUserRole(actor, role) {
			options = append(options, userRoleOption{Value: role, Label: userRoleLabel(role), Description: roleDescription(role)})
		}
	}
	return options
}

func buildUsersIndexData(actor domain.User, users []domain.User, status usersStatus) usersIndexData {
	status = normalizeUsersStatus(string(status))
	data := usersIndexData{Users: users, Status: status, ListURL: usersListURL(status)}
	policy := application.NewPolicy()
	for _, user := range users {
		data.Counts.All++
		if user.Active {
			data.Counts.Active++
		} else {
			data.Counts.Deactivated++
		}
		if status == usersStatusActive && !user.Active || status == usersStatusDeactivated && user.Active {
			continue
		}
		canManage := policy.CanManageUser(actor.Role, user.Role)
		editURL := "/users/" + strconv.FormatInt(user.ID, 10) + "/edit"
		editHref := editURL
		if status != usersStatusAll {
			editHref += "?status=" + string(status)
		}
		statusLabel := "Deactivated"
		if user.Active {
			statusLabel = "Active"
		}
		row := userRowView{
			ID: user.ID, Initials: usersInitials(user.Name), Name: user.Name, Email: user.Email,
			RoleLabel: userRoleLabel(user.Role), StatusLabel: statusLabel,
			Active: user.Active, CreatedAt: user.CreatedAt, CanManage: canManage, EditHref: editHref, EditURL: editURL,
		}
		if canManage {
			row.LauncherID = "user-launcher-" + strconv.FormatInt(user.ID, 10)
		}
		data.Rows = append(data.Rows, row)
	}
	if len(data.Rows) == 0 {
		switch status {
		case usersStatusActive:
			data.EmptyMessage = "No active users."
		case usersStatusDeactivated:
			data.EmptyMessage = "No deactivated users."
		default:
			data.EmptyMessage = "No users have been created."
		}
	}
	return data
}

func (h *UserHandlers) index(w http.ResponseWriter, r *http.Request) {
	setUsersVary(w)
	if !requireCapability(w, r, application.CapManageUsers) {
		return
	}
	actor := *userFromContext(r.Context())
	data, err := h.usersIndexData(r, actor, normalizeUsersStatus(r.URL.Query().Get("status")))
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	data.UsersAssets = true
	h.renderer.Render(w, r, "users_index", "users_screen", data, http.StatusOK)
}

func setUsersVary(w http.ResponseWriter) {
	w.Header().Add("Vary", "HX-Request")
}

func (h *UserHandlers) usersIndexData(r *http.Request, actor domain.User, status usersStatus) (usersIndexData, error) {
	users, err := h.users.List(r.Context(), actor)
	if err != nil {
		return usersIndexData{}, err
	}
	data := buildUsersIndexData(actor, users, status)
	data.pageData = pageDataFrom(r, "users")
	return data, nil
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

func newUserDrawerData(actor domain.User, user *domain.User, status usersStatus, values userFormValues, msg, field string, hasError bool) userDrawerData {
	return userDrawerData{
		Error:           msg,
		ErrorField:      field,
		UserID:          user.ID,
		Values:          values,
		RoleOptions:     usersRoleOptions(actor.Role),
		RoleDescription: roleDescription(values.Role),
		Status:          status,
		ListURL:         usersListURL(status),
		EditURL:         "/users/" + strconv.FormatInt(user.ID, 10) + "/edit",
		HasServerError:  hasError,
	}
}

func (h *UserHandlers) editForm(w http.ResponseWriter, r *http.Request) {
	setUsersVary(w)
	if !requireCapability(w, r, application.CapManageUsers) {
		return
	}
	id, ok := userID(r)
	if !ok {
		http.Error(w, "invalid user id", http.StatusBadRequest)
		return
	}
	actor := *userFromContext(r.Context())
	u, err := h.users.GetByID(r.Context(), id)
	if err != nil {
		http.Error(w, mapErrorMsg(err), statusFor(err))
		return
	}
	policy := application.NewPolicy()
	targetRole := u.Role
	if targetRole == "" {
		targetRole = domain.RoleUser
	}
	if targetRole == domain.RoleRoot {
		http.Error(w, mapErrorMsg(domain.NewRootProtectedError()), http.StatusForbidden)
		return
	}
	if !policy.CanManageUser(actor.Role, targetRole) {
		http.Error(w, "admin accounts require root", http.StatusForbidden)
		return
	}
	status := normalizeUsersStatus(r.URL.Query().Get("status"))
	drawer := newUserDrawerData(actor, u, status, userFormValues{Name: u.Name, Email: u.Email, Role: u.Role, Active: u.Active}, "", "", false)
	if r.Header.Get("HX-Request") != "" {
		h.renderer.Render(w, r, "users_index", "user_drawer", drawer, http.StatusOK)
		return
	}
	data, err := h.usersIndexData(r, actor, status)
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	data.UsersAssets = true
	data.Drawer = &drawer
	h.renderer.Render(w, r, "users_index", "users_screen", data, http.StatusOK)
}

// update applies the complete non-password managed-user edit atomically.
func (h *UserHandlers) update(w http.ResponseWriter, r *http.Request) {
	setUsersVary(w)
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
			h.renderUsersDrawerError(w, r, id, &domain.ValidationError{Field: "role", Message: "invalid role"})
			return
		}
		u, getErr := h.users.GetByID(r.Context(), id)
		if getErr != nil {
			h.renderUsersDrawerError(w, r, id, getErr)
			return
		}
		role = u.Role
	}
	activeValues := r.Form["active"]
	active := false
	if len(activeValues) > 0 {
		last := activeValues[len(activeValues)-1]
		active = last == "true" || last == "on"
	}
	if _, err := h.users.UpdateManagedUser(r.Context(), *userFromContext(r.Context()), id, application.UpdateManagedUserInput{Name: name, Email: email, Role: role, Active: active}); err != nil {
		h.renderUsersDrawerError(w, r, id, err)
		return
	}
	status := normalizeUsersStatus(r.Form.Get("status"))
	if r.Header.Get("HX-Request") != "" {
		data, err := h.usersIndexData(r, *userFromContext(r.Context()), status)
		if err != nil {
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
		data.UsersAssets = true
		w.Header().Set("HX-Retarget", "#users-root")
		w.Header().Set("HX-Reswap", "outerHTML")
		w.Header().Set("HX-Replace-Url", data.ListURL)
		w.Header().Set("HX-Trigger-After-Swap", "users:saved")
		h.renderer.Render(w, r, "users_index", "users_screen", data, http.StatusOK)
		return
	}
	redirect(w, r, usersListURL(status))
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

// renderUserFormError preserves the create and password form contract.
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

func (h *UserHandlers) renderUsersDrawerError(w http.ResponseWriter, r *http.Request, id int64, err error) {
	status, msg := mapError(err)
	if status == http.StatusInternalServerError {
		http.Error(w, msg, status)
		return
	}
	actor := *userFromContext(r.Context())
	statusFilter := normalizeUsersStatus(r.Form.Get("status"))
	values := userFormValues{
		Name:   r.Form.Get("name"),
		Email:  r.Form.Get("email"),
		Role:   domain.Role(r.Form.Get("role")),
		Active: lastActiveValue(r.Form["active"]),
	}
	u := &domain.User{ID: id, Name: values.Name, Email: values.Email, Role: values.Role, Active: values.Active}
	if current, getErr := h.users.GetByID(r.Context(), id); getErr == nil {
		u = current
	}
	drawer := newUserDrawerData(actor, u, statusFilter, values, msg, "", true)
	if r.Header.Get("HX-Request") != "" {
		w.Header().Set("HX-Retarget", "#users-drawer-host")
		w.Header().Set("HX-Reswap", "outerHTML")
		h.renderer.Render(w, r, "users_index", "user_drawer", drawer, status)
		return
	}
	data, dataErr := h.usersIndexData(r, actor, statusFilter)
	if dataErr != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	data.UsersAssets = true
	data.Drawer = &drawer
	h.renderer.Render(w, r, "users_index", "users_screen", data, status)
}

func lastActiveValue(values []string) bool {
	if len(values) == 0 {
		return false
	}
	last := values[len(values)-1]
	return last == "true" || last == "on"
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
