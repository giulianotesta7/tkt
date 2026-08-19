package httpadapter

import (
	"net/http"
	"strconv"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// GroupHandlers implement admin/root group CRUD and N:N membership routes.
type GroupHandlers struct {
	groups   *application.GroupService
	renderer *Renderer
}

func NewGroupHandlers(groups *application.GroupService, renderer *Renderer) *GroupHandlers {
	return &GroupHandlers{groups: groups, renderer: renderer}
}

func (h *GroupHandlers) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /groups", h.index)
	mux.HandleFunc("POST /groups", h.create)
	mux.HandleFunc("POST /groups/{id}/edit", h.rename)
	mux.HandleFunc("POST /groups/{id}/delete", h.delete)
	mux.HandleFunc("POST /groups/{id}/members", h.addMember)
	mux.HandleFunc("POST /groups/{id}/members/{userID}/delete", h.removeMember)
}

type groupView struct {
	domain.Group
	Members []groupMemberView
}

type groupMemberView struct {
	domain.User
	GroupID int64
}

type groupsIndexData struct {
	pageData
	Error    string
	Groups   []groupView
	Eligible []domain.User
}

func (h *GroupHandlers) index(w http.ResponseWriter, r *http.Request) {
	h.renderIndex(w, r, "", http.StatusOK)
}

func (h *GroupHandlers) renderIndex(w http.ResponseWriter, r *http.Request, message string, status int) {
	actor := *userFromContext(r.Context())
	groups, err := h.groups.List(r.Context(), actor)
	if err != nil {
		http.Error(w, mapErrorMsg(err), statusFor(err))
		return
	}
	views := make([]groupView, 0, len(groups))
	for _, group := range groups {
		members, err := h.groups.ListMembers(r.Context(), actor, group.ID)
		if err != nil {
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
		memberViews := make([]groupMemberView, 0, len(members))
		for _, member := range members {
			memberViews = append(memberViews, groupMemberView{User: member, GroupID: group.ID})
		}
		views = append(views, groupView{Group: group, Members: memberViews})
	}
	eligible, err := h.groups.ListEligibleMembers(r.Context(), actor)
	if err != nil {
		http.Error(w, mapErrorMsg(err), statusFor(err))
		return
	}
	h.renderer.Render(w, r, "groups_index", "", groupsIndexData{pageData: pageDataFrom(r, "groups"), Error: message, Groups: views, Eligible: eligible}, status)
}

func (h *GroupHandlers) create(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	if _, err := h.groups.Create(r.Context(), *userFromContext(r.Context()), r.Form.Get("name")); err != nil {
		h.renderIndexError(w, r, err)
		return
	}
	redirect(w, r, "/groups")
}

func (h *GroupHandlers) rename(w http.ResponseWriter, r *http.Request) {
	id, ok := groupID(r)
	if !ok {
		http.Error(w, "invalid group id", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	if _, err := h.groups.Rename(r.Context(), *userFromContext(r.Context()), id, r.Form.Get("name")); err != nil {
		h.renderIndexError(w, r, err)
		return
	}
	redirect(w, r, "/groups")
}

func (h *GroupHandlers) delete(w http.ResponseWriter, r *http.Request) {
	id, ok := groupID(r)
	if !ok {
		http.Error(w, "invalid group id", http.StatusBadRequest)
		return
	}
	if err := h.groups.Delete(r.Context(), *userFromContext(r.Context()), id); err != nil {
		h.renderIndexError(w, r, err)
		return
	}
	redirect(w, r, "/groups")
}

func (h *GroupHandlers) addMember(w http.ResponseWriter, r *http.Request) {
	groupID, ok := groupID(r)
	if !ok {
		http.Error(w, "invalid group id", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	if err := h.groups.AddMember(r.Context(), *userFromContext(r.Context()), groupID, parseID(r.Form.Get("user_id"))); err != nil {
		h.renderIndexError(w, r, err)
		return
	}
	redirect(w, r, "/groups")
}

func (h *GroupHandlers) removeMember(w http.ResponseWriter, r *http.Request) {
	groupID, ok := groupID(r)
	if !ok {
		http.Error(w, "invalid group id", http.StatusBadRequest)
		return
	}
	userID := parseID(r.PathValue("userID"))
	if userID == 0 {
		http.Error(w, "invalid user id", http.StatusBadRequest)
		return
	}
	if err := h.groups.RemoveMember(r.Context(), *userFromContext(r.Context()), groupID, userID); err != nil {
		h.renderIndexError(w, r, err)
		return
	}
	redirect(w, r, "/groups")
}

func (h *GroupHandlers) renderIndexError(w http.ResponseWriter, r *http.Request, err error) {
	status, message := mapError(err)
	if status == http.StatusInternalServerError || status == http.StatusForbidden {
		http.Error(w, message, status)
		return
	}
	h.renderIndex(w, r, message, status)
}

func groupID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil && id > 0
}
