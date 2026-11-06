package httpadapter

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
	"github.com/giulianotesta7/tkt/internal/i18n"
)

// DeskHandlers owns the /desks screen (issue #484, S15): desks are teams, no
// longer a level of the catalog tree, so they are listed, created, renamed,
// staffed and deleted here.
type DeskHandlers struct {
	desks    *application.DeskService
	catalog  *application.CatalogService
	renderer *Renderer
}

func NewDeskHandlers(desks *application.DeskService, renderer *Renderer) *DeskHandlers {
	return &DeskHandlers{desks: desks, renderer: renderer}
}

// WithCatalog supplies the desk projection the table renders (agents,
// categories and open tickets per desk, in one read).
func (h *DeskHandlers) WithCatalog(catalog *application.CatalogService) *DeskHandlers {
	h.catalog = catalog
	return h
}

func (h *DeskHandlers) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /desks", h.index)
	mux.HandleFunc("GET /desks/new", h.newForm)
	mux.HandleFunc("GET /desks/{id}/edit", h.editForm)
	mux.HandleFunc("POST /desks", h.create)
	mux.HandleFunc("POST /desks/{id}/edit", h.update)
	mux.HandleFunc("POST /desks/{id}/delete", h.delete)
	mux.HandleFunc("POST /desks/{id}/members", h.addMember)
	mux.HandleFunc("POST /desks/{id}/members/{userID}/delete", h.removeMember)
}

// desksPageData is the /desks screen: one row per desk, plus the drawer when a
// no-script request asked for one.
type desksPageData struct {
	pageData
	Rows   []deskPageRow
	Error  string
	Drawer *categoryDrawerData
}

// deskPageRow is one desk with the facts the table shows. NeedsAttention is
// the same rule the catalog applies: nobody can take this desk's tickets.
type deskPageRow struct {
	ID             int64
	Name           string
	Description    string
	AgentCount     int
	CategoryCount  int
	OpenTickets    int
	NeedsAttention bool
}

func (h *DeskHandlers) index(w http.ResponseWriter, r *http.Request) {
	if !requireCapability(w, r, application.CapManageDesks) {
		return
	}
	h.renderPage(w, r, "", nil, http.StatusOK)
}

func (h *DeskHandlers) pageData(r *http.Request, message string) (desksPageData, error) {
	data := desksPageData{pageData: pageDataFrom(r, "desks"), Error: message}
	data.CategoryAssets, data.PageFoundationAssets = true, true
	if h.catalog == nil {
		return data, nil
	}
	desks, err := h.catalog.ListTreeDesks(r.Context())
	if err != nil {
		return data, err
	}
	for _, d := range desks {
		data.Rows = append(data.Rows, deskPageRow{
			ID: d.ID, Name: d.Name, Description: d.Description,
			AgentCount: d.AgentCount, CategoryCount: d.CategoryCount, OpenTickets: d.OpenTickets,
			NeedsAttention: d.AgentCount == 0,
		})
	}
	return data, nil
}

func (h *DeskHandlers) renderPage(w http.ResponseWriter, r *http.Request, message string, drawer *categoryDrawerData, status int) {
	data, err := h.pageData(r, message)
	if err != nil {
		http.Error(w, mapErrorMsg(r, err), statusFor(err))
		return
	}
	data.Drawer = drawer
	h.renderer.Render(w, r, "desks_index", "", data, status)
}

// renderTable answers a successful HTMX mutation with the refreshed table,
// swapped into the page the same way the Categories screen refreshes.
func (h *DeskHandlers) renderTable(w http.ResponseWriter, r *http.Request, message string, status int) {
	data, err := h.pageData(r, message)
	if err != nil {
		http.Error(w, mapErrorMsg(r, err), statusFor(err))
		return
	}
	w.Header().Set("HX-Retarget", "#categories-background")
	w.Header().Set("HX-Reswap", "outerHTML")
	w.Header().Set("HX-Push-Url", "/desks")
	if message == "" {
		saveFeedback(w, r, saveFeedbackSaved, saveFeedbackSuccess)
		w.Header().Set("HX-Trigger-After-Swap", "categories:saved")
	}
	h.renderer.Render(w, r, "desks_index", "desks_background", data, status)
}

// standaloneDrawer is the desk drawer as the desks page shows it: no
// department control, its own action and close URLs, and members for an
// existing desk.
func (h *DeskHandlers) standaloneDrawer(r *http.Request, desk *domain.Desk) (*categoryDrawerData, error) {
	d := &categoryDrawerData{Kind: "desk", Standalone: true, ActionURL: "/desks", CloseURL: "/desks"}
	if desk == nil {
		return d, nil
	}
	actor := userFromContext(r.Context())
	members, err := h.desks.ListMembers(r.Context(), *actor, desk.ID)
	if err != nil {
		return nil, err
	}
	eligible, err := h.desks.ListEligibleMembers(r.Context(), *actor)
	if err != nil {
		return nil, err
	}
	d.ID, d.Name, d.Description, d.DeskID = desk.ID, desk.Name, desk.Description, desk.ID
	d.ActionURL = "/desks/" + strconv.FormatInt(desk.ID, 10) + "/edit"
	d.Members = members
	d.Eligible = notMembers(eligible, members)
	return d, nil
}

func notMembers(eligible, members []domain.User) []domain.User {
	memberIDs := make(map[int64]struct{}, len(members))
	for _, member := range members {
		memberIDs[member.ID] = struct{}{}
	}
	out := make([]domain.User, 0, len(eligible))
	for _, user := range eligible {
		if _, ok := memberIDs[user.ID]; !ok {
			out = append(out, user)
		}
	}
	return out
}

// renderDrawer shows the drawer: in place for HTMX, inside the page otherwise.
func (h *DeskHandlers) renderDrawer(w http.ResponseWriter, r *http.Request, d *categoryDrawerData, status int) {
	setCategoriesVary(w)
	if r.Header.Get("HX-Request") != "" {
		w.Header().Set("HX-Retarget", "#category-drawer-host")
		w.Header().Set("HX-Reswap", "outerHTML")
		h.renderer.Render(w, r, "desks_index", "category_drawer", d, status)
		return
	}
	h.renderPage(w, r, "", d, status)
}

func (h *DeskHandlers) newForm(w http.ResponseWriter, r *http.Request) {
	if !requireCapability(w, r, application.CapManageDesks) {
		return
	}
	d, _ := h.standaloneDrawer(r, nil)
	h.renderDrawer(w, r, d, http.StatusOK)
}

func (h *DeskHandlers) editForm(w http.ResponseWriter, r *http.Request) {
	if !requireCapability(w, r, application.CapManageDesks) {
		return
	}
	id, ok := deskID(r)
	if !ok {
		httpError(w, r, "error.invalid_desk_id", http.StatusBadRequest)
		return
	}
	desk, err := h.desks.GetByID(r.Context(), *userFromContext(r.Context()), id)
	if err != nil {
		http.Error(w, mapErrorMsg(r, err), statusFor(err))
		return
	}
	d, err := h.standaloneDrawer(r, desk)
	if err != nil {
		http.Error(w, mapErrorMsg(r, err), statusFor(err))
		return
	}
	h.renderDrawer(w, r, d, http.StatusOK)
}

// formError re-renders the drawer with what was typed and the refusal.
func (h *DeskHandlers) formError(w http.ResponseWriter, r *http.Request, id int64, err error) {
	status, message := mapErrorFor(r, err)
	if status == http.StatusInternalServerError {
		http.Error(w, message, status)
		return
	}
	var desk *domain.Desk
	if id != 0 {
		desk, _ = h.desks.GetByID(r.Context(), *userFromContext(r.Context()), id)
	}
	d, drawerErr := h.standaloneDrawer(r, desk)
	if drawerErr != nil {
		http.Error(w, mapErrorMsg(r, drawerErr), statusFor(drawerErr))
		return
	}
	d.Name, d.Description = r.Form.Get("name"), r.Form.Get("description")
	d.Error, d.HasServerError = message, true
	var validation *domain.ValidationError
	if errors.As(err, &validation) || errors.Is(err, domain.ErrDuplicate) {
		d.InvalidName = true
	}
	h.renderDrawer(w, r, d, status)
}

func (h *DeskHandlers) afterMutation(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("HX-Request") == "" {
		saveFeedback(w, r, saveFeedbackSaved, saveFeedbackSuccess)
		redirect(w, r, "/desks")
		return
	}
	h.renderTable(w, r, "", http.StatusOK)
}

func (h *DeskHandlers) create(w http.ResponseWriter, r *http.Request) {
	if !requireCapability(w, r, application.CapManageDesks) {
		return
	}
	if err := r.ParseForm(); err != nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	if _, err := h.desks.CreateWithDescription(r.Context(), *userFromContext(r.Context()), r.Form.Get("name"), r.Form.Get("description")); err != nil {
		h.formError(w, r, 0, err)
		return
	}
	h.afterMutation(w, r)
}

// update renames a desk and rewrites its description.
func (h *DeskHandlers) update(w http.ResponseWriter, r *http.Request) {
	if !requireCapability(w, r, application.CapManageDesks) {
		return
	}
	id, ok := deskID(r)
	if !ok {
		httpError(w, r, "error.invalid_desk_id", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	if _, err := h.desks.RenameWithDescription(r.Context(), *userFromContext(r.Context()), id, r.Form.Get("name"), r.Form.Get("description")); err != nil {
		h.formError(w, r, id, err)
		return
	}
	h.afterMutation(w, r)
}

// delete removes an unreferenced desk. Categories, workflow definitions and
// assignment history protect referenced desks, preserving historical identity.
func (h *DeskHandlers) delete(w http.ResponseWriter, r *http.Request) {
	if !requireCapability(w, r, application.CapManageDesks) {
		return
	}
	id, ok := deskID(r)
	if !ok {
		httpError(w, r, "error.invalid_desk_id", http.StatusBadRequest)
		return
	}
	if err := h.desks.Delete(r.Context(), *userFromContext(r.Context()), id); err != nil {
		status, message := mapErrorFor(r, err)
		if errors.Is(err, domain.ErrReferenced) {
			message = i18n.T(requestLocale(r), "desks.delete_in_use")
		}
		if status == http.StatusInternalServerError {
			http.Error(w, message, status)
			return
		}
		if r.Header.Get("HX-Request") == "" {
			h.renderPage(w, r, message, nil, status)
			return
		}
		h.renderTable(w, r, message, status)
		return
	}
	h.afterMutation(w, r)
}

func (h *DeskHandlers) addMember(w http.ResponseWriter, r *http.Request) {
	deskID, ok := deskID(r)
	if !ok {
		httpError(w, r, "error.invalid_desk_id", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	userID := parseID(r.Form.Get("user_id"))
	if userID == 0 {
		if h.renderDeskDrawer(w, r, deskID, i18n.T(requestLocale(r), "error.invalid_user_id"), http.StatusBadRequest) {
			return
		}
		httpError(w, r, "error.invalid_user_id", http.StatusBadRequest)
		return
	}
	if err := h.desks.AddMember(r.Context(), *userFromContext(r.Context()), deskID, userID); err != nil {
		status, message := mapErrorFor(r, err)
		if h.renderDeskDrawer(w, r, deskID, message, status) {
			return
		}
		http.Error(w, message, status)
		return
	}
	// Issue #234: the drawer stays open and re-renders its member list in
	// place, so the refreshed list is the confirmation — no toast.
	if h.renderDeskDrawer(w, r, deskID, "", http.StatusOK) {
		return
	}
	redirectDeskContext(w, r, deskID)
}

func (h *DeskHandlers) removeMember(w http.ResponseWriter, r *http.Request) {
	deskID, ok := deskID(r)
	if !ok {
		httpError(w, r, "error.invalid_desk_id", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	userID := parseID(r.PathValue("userID"))
	if userID == 0 {
		if h.renderDeskDrawer(w, r, deskID, i18n.T(requestLocale(r), "error.invalid_user_id"), http.StatusBadRequest) {
			return
		}
		httpError(w, r, "error.invalid_user_id", http.StatusBadRequest)
		return
	}
	if err := h.desks.RemoveMember(r.Context(), *userFromContext(r.Context()), deskID, userID); err != nil {
		status, message := mapErrorFor(r, err)
		if h.renderDeskDrawer(w, r, deskID, message, status) {
			return
		}
		http.Error(w, message, status)
		return
	}
	// Issue #234: see addMember — the re-rendered member list is the
	// confirmation for the in-place drawer, so no toast.
	if h.renderDeskDrawer(w, r, deskID, "", http.StatusOK) {
		return
	}
	redirectDeskContext(w, r, deskID)
}

// redirectDeskContext returns a no-script membership change to the desks page,
// where the desk drawer lives (issue #484).
func redirectDeskContext(w http.ResponseWriter, r *http.Request, _ int64) {
	redirect(w, r, "/desks")
}

// renderDeskDrawer keeps HTMX membership mutations inside the desk drawer.
func (h *DeskHandlers) renderDeskDrawer(w http.ResponseWriter, r *http.Request, deskID int64, message string, status int) bool {
	if r.Header.Get("HX-Request") == "" || h.renderer == nil {
		return false
	}
	desk, err := h.desks.GetByID(r.Context(), *userFromContext(r.Context()), deskID)
	if err != nil {
		return false
	}
	d, err := h.standaloneDrawer(r, desk)
	if err != nil {
		return false
	}
	d.Error, d.HasServerError = message, message != ""
	h.renderDrawer(w, r, d, status)
	return true
}

func deskID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil && id > 0
}
