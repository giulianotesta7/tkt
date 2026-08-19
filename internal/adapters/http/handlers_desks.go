package httpadapter

import (
	"net/http"
	"strconv"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// DeskHandlers implement admin/root desk CRUD and N:N membership routes.
type DeskHandlers struct {
	desks    *application.DeskService
	renderer *Renderer
}

func NewDeskHandlers(desks *application.DeskService, renderer *Renderer) *DeskHandlers {
	return &DeskHandlers{desks: desks, renderer: renderer}
}

func (h *DeskHandlers) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /desks", h.index)
	mux.HandleFunc("POST /desks", h.create)
	mux.HandleFunc("POST /desks/{id}/edit", h.rename)
	mux.HandleFunc("POST /desks/{id}/delete", h.delete)
	mux.HandleFunc("POST /desks/{id}/members", h.addMember)
	mux.HandleFunc("POST /desks/{id}/members/{userID}/delete", h.removeMember)
}

type deskView struct {
	domain.Desk
	Members []deskMemberView
}

type deskMemberView struct {
	domain.User
	DeskID int64
}

type desksIndexData struct {
	pageData
	Error    string
	Desks    []deskView
	Eligible []domain.User
}

func (h *DeskHandlers) index(w http.ResponseWriter, r *http.Request) {
	h.renderIndex(w, r, "", http.StatusOK)
}

func (h *DeskHandlers) renderIndex(w http.ResponseWriter, r *http.Request, message string, status int) {
	actor := *userFromContext(r.Context())
	desks, err := h.desks.List(r.Context(), actor)
	if err != nil {
		http.Error(w, mapErrorMsg(err), statusFor(err))
		return
	}
	views := make([]deskView, 0, len(desks))
	for _, desk := range desks {
		members, err := h.desks.ListMembers(r.Context(), actor, desk.ID)
		if err != nil {
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
		memberViews := make([]deskMemberView, 0, len(members))
		for _, member := range members {
			memberViews = append(memberViews, deskMemberView{User: member, DeskID: desk.ID})
		}
		views = append(views, deskView{Desk: desk, Members: memberViews})
	}
	eligible, err := h.desks.ListEligibleMembers(r.Context(), actor)
	if err != nil {
		http.Error(w, mapErrorMsg(err), statusFor(err))
		return
	}
	h.renderer.Render(w, r, "desks_index", "", desksIndexData{pageData: pageDataFrom(r, "desks"), Error: message, Desks: views, Eligible: eligible}, status)
}

func (h *DeskHandlers) create(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	if _, err := h.desks.Create(r.Context(), *userFromContext(r.Context()), r.Form.Get("name")); err != nil {
		h.renderIndexError(w, r, err)
		return
	}
	redirect(w, r, "/desks")
}

func (h *DeskHandlers) rename(w http.ResponseWriter, r *http.Request) {
	id, ok := deskID(r)
	if !ok {
		http.Error(w, "invalid desk id", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	if _, err := h.desks.Rename(r.Context(), *userFromContext(r.Context()), id, r.Form.Get("name")); err != nil {
		h.renderIndexError(w, r, err)
		return
	}
	redirect(w, r, "/desks")
}

func (h *DeskHandlers) delete(w http.ResponseWriter, r *http.Request) {
	id, ok := deskID(r)
	if !ok {
		http.Error(w, "invalid desk id", http.StatusBadRequest)
		return
	}
	if err := h.desks.Delete(r.Context(), *userFromContext(r.Context()), id); err != nil {
		h.renderIndexError(w, r, err)
		return
	}
	redirect(w, r, "/desks")
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
	if err := h.desks.AddMember(r.Context(), *userFromContext(r.Context()), deskID, parseID(r.Form.Get("user_id"))); err != nil {
		h.renderIndexError(w, r, err)
		return
	}
	redirect(w, r, "/desks")
}

func (h *DeskHandlers) removeMember(w http.ResponseWriter, r *http.Request) {
	deskID, ok := deskID(r)
	if !ok {
		http.Error(w, "invalid desk id", http.StatusBadRequest)
		return
	}
	userID := parseID(r.PathValue("userID"))
	if userID == 0 {
		http.Error(w, "invalid user id", http.StatusBadRequest)
		return
	}
	if err := h.desks.RemoveMember(r.Context(), *userFromContext(r.Context()), deskID, userID); err != nil {
		h.renderIndexError(w, r, err)
		return
	}
	redirect(w, r, "/desks")
}

func (h *DeskHandlers) renderIndexError(w http.ResponseWriter, r *http.Request, err error) {
	status, message := mapError(err)
	if status == http.StatusInternalServerError || status == http.StatusForbidden {
		http.Error(w, message, status)
		return
	}
	h.renderIndex(w, r, message, status)
}

func deskID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil && id > 0
}
