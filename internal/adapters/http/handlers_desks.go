package httpadapter

import (
	"net/http"
	"strconv"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// DeskHandlers keeps only the compatibility entry point and membership routes
// used by the unified Categories desk drawer. Desk CRUD belongs to the catalog
// handlers so the application has one administration frontend.
type DeskHandlers struct {
	desks    *application.DeskService
	renderer *Renderer
}

func NewDeskHandlers(desks *application.DeskService, renderer *Renderer) *DeskHandlers {
	return &DeskHandlers{desks: desks, renderer: renderer}
}

func (h *DeskHandlers) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /desks", h.index)
	mux.HandleFunc("POST /desks/{id}/members", h.addMember)
	mux.HandleFunc("POST /desks/{id}/members/{userID}/delete", h.removeMember)
}

func (h *DeskHandlers) index(w http.ResponseWriter, r *http.Request) {
	if !requireCapability(w, r, application.CapManageDesks) {
		return
	}
	redirect(w, r, "/categories")
}

func (h *DeskHandlers) addMember(w http.ResponseWriter, r *http.Request) {
	deskID, ok := deskID(r)
	if !ok {
		http.Error(w, "invalid desk id", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	userID := parseID(r.Form.Get("user_id"))
	if userID == 0 {
		if h.renderDeskDrawer(w, r, deskID, "invalid user id", http.StatusBadRequest) {
			return
		}
		http.Error(w, "invalid user id", http.StatusBadRequest)
		return
	}
	if err := h.desks.AddMember(r.Context(), *userFromContext(r.Context()), deskID, userID); err != nil {
		status, message := mapError(err)
		if h.renderDeskDrawer(w, r, deskID, message, status) {
			return
		}
		http.Error(w, message, status)
		return
	}
	if h.renderDeskDrawer(w, r, deskID, "", http.StatusOK) {
		return
	}
	redirectDeskContext(w, r, deskID)
}

func (h *DeskHandlers) removeMember(w http.ResponseWriter, r *http.Request) {
	deskID, ok := deskID(r)
	if !ok {
		http.Error(w, "invalid desk id", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	userID := parseID(r.PathValue("userID"))
	if userID == 0 {
		if h.renderDeskDrawer(w, r, deskID, "invalid user id", http.StatusBadRequest) {
			return
		}
		http.Error(w, "invalid user id", http.StatusBadRequest)
		return
	}
	if err := h.desks.RemoveMember(r.Context(), *userFromContext(r.Context()), deskID, userID); err != nil {
		status, message := mapError(err)
		if h.renderDeskDrawer(w, r, deskID, message, status) {
			return
		}
		http.Error(w, message, status)
		return
	}
	if h.renderDeskDrawer(w, r, deskID, "", http.StatusOK) {
		return
	}
	redirectDeskContext(w, r, deskID)
}

func redirectDeskContext(w http.ResponseWriter, r *http.Request, deskID int64) {
	state := deskContextState(r, deskID, nil)
	redirect(w, r, categoryStatePath(state))
}

// renderDeskDrawer keeps HTMX membership mutations inside the unified drawer.
// The compatibility route still redirects for ordinary form submissions.
func (h *DeskHandlers) renderDeskDrawer(w http.ResponseWriter, r *http.Request, deskID int64, message string, status int) bool {
	if r.Header.Get("HX-Request") == "" || h.renderer == nil {
		return false
	}
	actor := userFromContext(r.Context())
	desk, err := h.desks.GetByID(r.Context(), *actor, deskID)
	if err != nil {
		return false
	}
	members, err := h.desks.ListMembers(r.Context(), *actor, deskID)
	if err != nil {
		return false
	}
	eligible, err := h.desks.ListEligibleMembers(r.Context(), *actor)
	if err != nil {
		return false
	}
	memberIDs := make(map[int64]struct{}, len(members))
	for _, member := range members {
		memberIDs[member.ID] = struct{}{}
	}
	filteredEligible := eligible[:0]
	for _, user := range eligible {
		if _, ok := memberIDs[user.ID]; !ok {
			filteredEligible = append(filteredEligible, user)
		}
	}

	state := deskContextState(r, deskID, desk)
	departmentID := state.DepartmentID
	if departmentID == unassignedDepartmentID {
		departmentID = 0
	}
	data := &categoryDrawerData{
		Kind:                 "desk",
		ID:                   desk.ID,
		Name:                 desk.Name,
		Description:          desk.Description,
		DepartmentID:         departmentID,
		DeskID:               desk.ID,
		Members:              members,
		Eligible:             filteredEligible,
		ActionURL:            "/categories/desks/" + strconv.FormatInt(desk.ID, 10) + "/edit",
		CloseURL:             categoryStatePath(state),
		View:                 "structure",
		SelectedDepartmentID: state.DepartmentID,
		SelectedDeskID:       desk.ID,
		Error:                message,
		HasServerError:       message != "",
	}
	// Membership forms carry the department options so the compatibility handler
	// can preserve the drawer's complete Department selector without owning
	// catalog reads.
	optionIDs := r.Form["department_option_id"]
	optionNames := r.Form["department_option_name"]
	seenDepartments := make(map[int64]struct{}, len(optionIDs))
	for i, rawID := range optionIDs {
		id, parseErr := strconv.ParseInt(rawID, 10, 64)
		if parseErr != nil || id <= 0 {
			continue
		}
		if _, seen := seenDepartments[id]; seen {
			continue
		}
		name := ""
		if i < len(optionNames) {
			name = optionNames[i]
		}
		if name == "" {
			continue
		}
		seenDepartments[id] = struct{}{}
		data.Departments = append(data.Departments, domain.CatalogDepartment{Department: domain.Department{ID: id, Name: name}})
	}
	if len(data.Departments) == 0 && departmentID > 0 {
		if name := r.Form.Get("department_name"); name != "" {
			data.Departments = []domain.CatalogDepartment{{Department: domain.Department{ID: departmentID, Name: name}}}
		}
	}
	setCategoriesVary(w)
	w.Header().Set("HX-Retarget", "#category-drawer-host")
	w.Header().Set("HX-Reswap", "outerHTML")
	h.renderer.Render(w, r, "categories_index", "category_drawer", data, status)
	return true
}

func deskContextState(r *http.Request, deskID int64, desk *domain.Desk) categoryViewState {
	state := categoryViewState{View: "structure", DeskID: deskID}
	switch r.Form.Get("department_id") {
	case "unassigned":
		state.DepartmentID = unassignedDepartmentID
	default:
		state.DepartmentID = parseID(r.Form.Get("department_id"))
	}
	if state.DepartmentID == 0 && desk != nil {
		if desk.DepartmentID != nil {
			state.DepartmentID = *desk.DepartmentID
		} else {
			state.DepartmentID = unassignedDepartmentID
		}
	}
	return state
}

func deskID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil && id > 0
}
