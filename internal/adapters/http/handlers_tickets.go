package httpadapter

import (
	"net/http"
	"net/url"
	"strconv"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// TicketHandlers implement the ticket list, create, detail, edit, transition
// and comment routes (design "HTTP Layer" route table; tasks 5.4 + 5.5).
// The session user flows in via the middleware context; these handlers only
// parse form input, call the use cases, and render (D6: HX-Request → swap
// fragment, else full page).
type TicketHandlers struct {
	tickets    *application.TicketService
	comments   *application.CommentService
	search     *application.SearchService
	categories *application.CategoryService
	users      *application.UserService
	renderer   *Renderer
}

// NewTicketHandlers wires the ticket routes against the ticket, comment,
// search, category, and user use cases plus the renderer.
func NewTicketHandlers(tickets *application.TicketService, comments *application.CommentService, search *application.SearchService, categories *application.CategoryService, users *application.UserService, renderer *Renderer) *TicketHandlers {
	return &TicketHandlers{
		tickets:    tickets,
		comments:   comments,
		search:     search,
		categories: categories,
		users:      users,
		renderer:   renderer,
	}
}

// Register mounts the ticket routes (D9 method+path patterns). Task 5.4
// covers list + create; 5.5 registers the detail/edit/transition/comment
// routes on the same handlers.
func (h *TicketHandlers) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /", h.index)
	mux.HandleFunc("GET /tickets", h.list)
	mux.HandleFunc("GET /tickets/new", h.newForm)
	mux.HandleFunc("POST /tickets", h.create)
	mux.HandleFunc("GET /tickets/{id}", h.show)
	mux.HandleFunc("POST /tickets/{id}/edit", h.update)
	mux.HandleFunc("POST /tickets/{id}/assign", h.assign)
	mux.HandleFunc("POST /tickets/{id}/transition", h.transition)
	mux.HandleFunc("POST /tickets/{id}/comments", h.addComment)
}

// pageData is the shared presentation payload for app-shell pages: the rail
// highlights the active section and the operator chip shows the session
// user.
type pageData struct {
	NavActive           string
	CurrentUser         domain.User
	CanManageUsers      bool
	CanManageDesks      bool
	CanManageCategories bool
	CanGrantAdmin       bool
}

// pageDataFrom builds the shell payload from the session user.
func pageDataFrom(r *http.Request, nav string) pageData {
	u := userFromContext(r.Context())
	caps := application.NewPolicy().Capabilities(u.Role)
	return pageData{
		NavActive:           nav,
		CurrentUser:         *u,
		CanManageUsers:      caps.Require(application.CapManageUsers),
		CanManageDesks:      caps.Require(application.CapManageDesks),
		CanManageCategories: caps.Require(application.CapManageCategories),
		CanGrantAdmin:       caps.Require(application.CapGrantAdmin),
	}
}

// options carries the selectable lists shared by list filters and forms.
type options struct {
	States          []domain.State
	Priorities      []domain.Priority
	Categories      []domain.Category
	Users           []domain.User // all users (filter dropdown; historical assignment)
	AssignableUsers []domain.User // active users only (assign dropdowns)
}

// listStates and listPriorities fix the display order (D11: critical >
// high > medium > low; states in lifecycle order).
var (
	listStates     = []domain.State{domain.StateNew, domain.StateInProgress, domain.StateResolved, domain.StateClosed, domain.StateCancelled}
	listPriorities = []domain.Priority{domain.PriorityCritical, domain.PriorityHigh, domain.PriorityMedium, domain.PriorityLow}
)

// collectOptions loads the shared option lists.
func (h *TicketHandlers) collectOptions(r *http.Request) (options, error) {
	categories, err := h.categories.List(r.Context())
	if err != nil {
		return options{}, err
	}
	actor := *userFromContext(r.Context())
	assignable, err := h.users.ListAssignable(r.Context(), actor)
	if err != nil {
		return options{}, err
	}
	return options{
		States:          listStates,
		Priorities:      listPriorities,
		Categories:      categories,
		Users:           assignable,
		AssignableUsers: assignable,
	}, nil
}

// filterState is the parsed list filter set (ticket-search spec). Zero
// values mean "no filter"; unknown values are ignored (threat matrix).
type filterState struct {
	State      domain.State
	Priority   domain.Priority
	CategoryID string
	UserID     string
	Q          string
}

// parseFilters reads the query string, ignoring unknown or malformed values.
func parseFilters(r *http.Request) filterState {
	q := r.URL.Query()
	f := filterState{Q: q.Get("q")}
	if s := domain.State(q.Get("state")); validState(s) {
		f.State = s
	}
	if p := domain.Priority(q.Get("priority")); domain.IsValidPriority(p) {
		f.Priority = p
	}
	if id := q.Get("category_id"); id != "" && parseID(id) != 0 {
		f.CategoryID = id
	}
	if id := q.Get("user_id"); id != "" && parseID(id) != 0 {
		f.UserID = id
	}
	return f
}

// validState reports whether s is one of the five machine states.
func validState(s domain.State) bool {
	for _, st := range listStates {
		if st == s {
			return true
		}
	}
	return false
}

// parseID parses a positive integer path/query value; 0 on malformed input.
func parseID(s string) int64 {
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil || id < 1 {
		return 0
	}
	return id
}

// query builds the TicketQuery for the search use case.
func (f filterState) query() application.TicketQuery {
	q := application.TicketQuery{Text: f.Q}
	if f.State != "" {
		s := f.State
		q.State = &s
	}
	if f.Priority != "" {
		p := f.Priority
		q.Priority = &p
	}
	if id := parseID(f.CategoryID); id != 0 {
		q.CategoryID = &id
	}
	if id := parseID(f.UserID); id != 0 {
		q.UserID = &id
	}
	return q
}

// listHref rebuilds the /tickets URL carrying the current filters.
func listHref(f filterState, page int) string {
	v := url.Values{}
	if f.State != "" {
		v.Set("state", string(f.State))
	}
	if f.Priority != "" {
		v.Set("priority", string(f.Priority))
	}
	if f.CategoryID != "" {
		v.Set("category_id", f.CategoryID)
	}
	if f.UserID != "" {
		v.Set("user_id", f.UserID)
	}
	if f.Q != "" {
		v.Set("q", f.Q)
	}
	if page > 1 {
		v.Set("page", strconv.Itoa(page))
	}
	if len(v) == 0 {
		return "/tickets"
	}
	return "/tickets?" + v.Encode()
}

// listData is the tickets index payload (page + HX fragment share it).
type listData struct {
	pageData
	Filters  filterState
	Options  options
	Tickets  []domain.Ticket
	Total    int
	Page     int
	Pages    int
	PrevHref string
	NextHref string
}

func (h *TicketHandlers) index(w http.ResponseWriter, r *http.Request) {
	redirect(w, r, "/tickets")
}

// listData builds the full list payload for the given filters and page.
// The search is scoped to the session actor (ticket-access spec): the
// list shows only tickets within the actor's scope.
func (h *TicketHandlers) listData(r *http.Request, f filterState, page int) (listData, error) {
	opts, err := h.collectOptions(r)
	if err != nil {
		return listData{}, err
	}
	res, err := h.search.Search(r.Context(), *userFromContext(r.Context()), f.query(), page)
	if err != nil {
		return listData{}, err
	}
	pages := (res.Total + application.PageSize - 1) / application.PageSize
	if pages < 1 {
		pages = 1
	}
	data := listData{
		pageData: pageDataFrom(r, "tickets"),
		Filters:  f,
		Options:  opts,
		Tickets:  res.Tickets,
		Total:    res.Total,
		Page:     res.Page,
		Pages:    pages,
	}
	if res.Page > 1 {
		data.PrevHref = listHref(f, res.Page-1)
	}
	if res.Page < pages {
		data.NextHref = listHref(f, res.Page+1)
	}
	return data, nil
}

func (h *TicketHandlers) list(w http.ResponseWriter, r *http.Request) {
	f := parseFilters(r)
	page := int(parseID(r.URL.Query().Get("page")))
	if page < 1 {
		page = 1
	}

	data, err := h.listData(r, f, page)
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	h.renderer.Render(w, r, "tickets_index", "ticket_list", data, http.StatusOK)
}

// ticketFormData is the create-ticket form payload (page + HX fragment
// share it; 422 re-renders carry Error + the submitted values).
type ticketFormData struct {
	pageData
	Error     string
	Values    ticketFormValues
	Options   options
	CanAssign bool
}

type ticketFormValues struct {
	Title       string
	Description string
	CategoryID  string
	UserID      string
	Priority    domain.Priority
}

func (h *TicketHandlers) newForm(w http.ResponseWriter, r *http.Request) {
	opts, err := h.collectOptions(r)
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	data := ticketFormData{
		pageData: pageDataFrom(r, "tickets"),
		Values:   ticketFormValues{Priority: domain.PriorityMedium},
		Options:  opts,
		CanAssign: application.NewPolicy().Capabilities(userFromContext(r.Context()).Role).
			Require(application.CapAssignTicket),
	}
	h.renderer.Render(w, r, "tickets_new", "ticket_form", data, http.StatusOK)
}

// create parses the create form, calls the use case, and answers per D6:
// HX-Request → refreshed ticket_list fragment; full request →
// 303 to the detail page. Errors re-render the form with the mapped status
// (422/409/...) and message.
func (h *TicketHandlers) create(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	actor := *userFromContext(r.Context())

	categoryID := parseID(r.Form.Get("category_id"))
	if categoryID == 0 {
		h.renderCreateError(w, r, "category", "category is required", http.StatusUnprocessableEntity)
		return
	}
	var userID *int64
	if raw := r.Form.Get("user_id"); raw != "" {
		id := parseID(raw)
		if id == 0 {
			h.renderCreateError(w, r, "user", "invalid user", http.StatusUnprocessableEntity)
			return
		}
		userID = &id
	}

	in := application.CreateTicketInput{
		Title:       r.Form.Get("title"),
		Description: r.Form.Get("description"),
		CategoryID:  categoryID,
		UserID:      userID,
		Priority:    domain.Priority(r.Form.Get("priority")),
	}

	_, err := h.tickets.Create(r.Context(), actor, in)
	if err != nil {
		status, msg := mapError(err)
		if status == http.StatusInternalServerError {
			http.Error(w, msg, status)
			return
		}
		h.renderCreateError(w, r, "", msg, status)
		return
	}

	if r.Header.Get("HX-Request") != "" {
		data, err := h.listData(r, filterState{}, 1)
		if err != nil {
			// The ticket is already committed: never report failure and
			// never invite a duplicate-creating retry. Send the client to
			// the list via the HX redirect instead of a misleading 500.
			w.Header().Set("HX-Redirect", "/tickets")
			w.WriteHeader(http.StatusOK)
			return
		}
		h.renderer.Render(w, r, "tickets_index", "ticket_list", data, http.StatusOK)
		return
	}

	// The detail route lands in the next slice commit; a committed ticket
	// must never leave the user on an unregistered 404 path.
	redirect(w, r, "/tickets")
}

// renderCreateError re-renders the create form with an inline error and the
// mapped status (HX → ticket_form fragment; full → tickets_new page).
func (h *TicketHandlers) renderCreateError(w http.ResponseWriter, r *http.Request, field, msg string, status int) {
	opts, err := h.collectOptions(r)
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	data := ticketFormData{
		pageData: pageDataFrom(r, "tickets"),
		Error:    msg,
		Values: ticketFormValues{
			Title:       r.Form.Get("title"),
			Description: r.Form.Get("description"),
			CategoryID:  r.Form.Get("category_id"),
			UserID:      r.Form.Get("user_id"),
			Priority:    domain.Priority(r.Form.Get("priority")),
		},
		Options: opts,
		CanAssign: application.NewPolicy().Capabilities(userFromContext(r.Context()).Role).
			Require(application.CapAssignTicket),
	}
	_ = field // the inline banner is a single generic error block
	h.renderer.Render(w, r, "tickets_new", "ticket_form", data, status)
}

// --- Task 5.5: detail, edit, transition, comments -------------------------

// transitionTarget is one allowed next state shown in the transition panel.
// The table mirrors the domain transition map (design "Domain Model"); the
// domain remains the single enforcement point — this list is presentation.
type transitionTarget struct {
	To          domain.State
	NeedsReason bool // closed -> in_progress requires a reopen reason
}

// allowedNext returns the presentation list of legal next states (design
// transition table).
func allowedNext(s domain.State) []transitionTarget {
	switch s {
	case domain.StateNew:
		return []transitionTarget{{To: domain.StateInProgress}, {To: domain.StateResolved}, {To: domain.StateCancelled}}
	case domain.StateInProgress:
		return []transitionTarget{{To: domain.StateResolved}, {To: domain.StateCancelled}}
	case domain.StateResolved:
		return []transitionTarget{{To: domain.StateClosed}, {To: domain.StateInProgress}}
	case domain.StateClosed:
		return []transitionTarget{{To: domain.StateInProgress, NeedsReason: true}}
	default:
		return nil // cancelled is terminal
	}
}

// detailData is the ticket detail payload (page + HX fragment share it).
type detailData struct {
	pageData
	Error   string
	View    *application.TicketView
	Next    []transitionTarget
	Options options
	Values  ticketFormValues
	// CanCommentInternal is the actor's comment-visibility capability
	// (comment-visibility spec): the comment form offers the internal
	// option only to agent+ actors. This is presentation only — the
	// server-side use case rejects a forged internal value regardless of
	// what the UI shows.
	CanCommentInternal bool
}

// ticketID resolves and validates the {id} path parameter; 0 + false on a
// non-numeric id (handler responsibility: 400).
func ticketID(r *http.Request) (int64, bool) {
	id := parseID(r.PathValue("id"))
	return id, id != 0
}

// detailDataFor loads the composed view (scoped to the session actor:
// out-of-scope tickets are denied as NotFound) and builds the detail
// payload.
func (h *TicketHandlers) detailDataFor(r *http.Request, id int64) (detailData, int, error) {
	actor := *userFromContext(r.Context())
	view, err := h.tickets.GetByID(r.Context(), actor, id)
	if err != nil {
		status, _ := mapError(err)
		return detailData{}, status, err
	}
	opts, err := h.collectOptions(r)
	if err != nil {
		return detailData{}, http.StatusInternalServerError, err
	}
	if view.AssignedUser != nil && !view.AssignedUser.Active {
		opts.AssignableUsers = append(opts.AssignableUsers, *view.AssignedUser)
	}
	values := ticketFormValues{
		Title:       view.Ticket.Title,
		Description: view.Ticket.Description,
		CategoryID:  strconv.FormatInt(view.Ticket.CategoryID, 10),
		Priority:    view.Ticket.Priority,
	}
	if view.Ticket.UserID != nil {
		values.UserID = strconv.FormatInt(*view.Ticket.UserID, 10)
	}
	return detailData{
		pageData:           pageDataFrom(r, "tickets"),
		View:               view,
		Next:               allowedNext(view.Ticket.State),
		Options:            opts,
		Values:             values,
		CanCommentInternal: application.NewPolicy().Capabilities(actor.Role).Require(application.CapCommentInternal),
	}, 0, nil
}

func (h *TicketHandlers) show(w http.ResponseWriter, r *http.Request) {
	id, ok := ticketID(r)
	if !ok {
		http.Error(w, "invalid ticket id", http.StatusBadRequest)
		return
	}
	data, status, err := h.detailDataFor(r, id)
	if err != nil {
		http.Error(w, mapErrorMsg(err), status)
		return
	}
	h.renderer.Render(w, r, "tickets_show", "ticket_detail", data, http.StatusOK)
}

func (h *TicketHandlers) transition(w http.ResponseWriter, r *http.Request) {
	id, ok := ticketID(r)
	if !ok {
		http.Error(w, "invalid ticket id", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	actor := *userFromContext(r.Context())
	to := domain.State(r.Form.Get("to"))
	reason := r.Form.Get("reason")

	_, err := h.tickets.Transition(r.Context(), actor, id, to, reason)
	if err != nil {
		h.renderDetailError(w, r, id, err)
		return
	}
	h.afterMutation(w, r, id, "ticket_detail")
}

func (h *TicketHandlers) addComment(w http.ResponseWriter, r *http.Request) {
	id, ok := ticketID(r)
	if !ok {
		http.Error(w, "invalid ticket id", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	actor := *userFromContext(r.Context())

	// S5: the visibility form field flows to the use case, which enforces
	// the role rules (user public-only; agent+ both). The template renders
	// the selector only for internal-capable actors; a forged internal value
	// from a user-role actor is rejected here regardless of what the UI shows.
	_, err := h.comments.Add(r.Context(), actor, id, r.Form.Get("body"), r.Form.Get("visibility"))
	if err != nil {
		h.renderDetailError(w, r, id, err)
		return
	}
	// HX: swap the merged timeline fragment; full: back to the detail page.
	data, status, err := h.detailDataFor(r, id)
	if err != nil {
		http.Error(w, mapErrorMsg(err), status)
		return
	}
	if r.Header.Get("HX-Request") != "" {
		h.renderer.Render(w, r, "tickets_show", "timeline", data, http.StatusOK)
		return
	}
	redirect(w, r, "/tickets/"+strconv.FormatInt(id, 10))
}

// assign applies the assignment form (assigned-to dropdown + optional
// reason) via the Assign use case, which enforces the person-only rules:
// agent+ actors only, active agent-plus target, reason required only for a
// reassignment (ticket-access spec; approved decision). Empty user_id
// clears the assignment. The form carries no other ticket fields — forged
// values are ignored, matching the requester policy.
func (h *TicketHandlers) assign(w http.ResponseWriter, r *http.Request) {
	id, ok := ticketID(r)
	if !ok {
		http.Error(w, "invalid ticket id", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	// Desks are membership-only in this iteration. Reject a forged desk
	// target instead of treating its missing user_id as an unassignment.
	if r.Form.Get("desk_id") != "" {
		h.renderDetailError(w, r, id, &domain.ValidationError{Field: "desk", Message: "desks cannot be ticket assignees"})
		return
	}
	actor := *userFromContext(r.Context())

	var assigneeID *int64
	if raw := r.Form.Get("user_id"); raw != "" {
		uid := parseID(raw)
		if uid == 0 {
			h.renderDetailError(w, r, id, &domain.ValidationError{Field: "user", Message: "invalid user"})
			return
		}
		assigneeID = &uid
	}

	_, err := h.tickets.Assign(r.Context(), actor, id, assigneeID, r.Form.Get("reason"))
	if err != nil {
		h.renderDetailError(w, r, id, err)
		return
	}
	h.afterMutation(w, r, id, "ticket_detail")
}

// renderDetailError re-renders the detail view with an inline error and the
// mapped status (HX → ticket_detail fragment; full → tickets_show page).
func (h *TicketHandlers) renderDetailError(w http.ResponseWriter, r *http.Request, id int64, err error) {
	status, msg := mapError(err)
	if status == http.StatusInternalServerError {
		http.Error(w, msg, status)
		return
	}
	data, _, dataErr := h.detailDataFor(r, id)
	if dataErr != nil {
		http.Error(w, mapErrorMsg(dataErr), statusFor(dataErr))
		return
	}
	data.Error = msg
	h.renderer.Render(w, r, "tickets_show", "ticket_detail", data, status)
}

// afterMutation answers a successful ticket mutation: HX → re-rendered
// fragment; full → 303 back to the detail page.
func (h *TicketHandlers) afterMutation(w http.ResponseWriter, r *http.Request, id int64, fragment string) {
	data, status, err := h.detailDataFor(r, id)
	if err != nil {
		http.Error(w, mapErrorMsg(err), status)
		return
	}
	if r.Header.Get("HX-Request") != "" {
		h.renderer.Render(w, r, "tickets_show", fragment, data, http.StatusOK)
		return
	}
	redirect(w, r, "/tickets/"+strconv.FormatInt(id, 10))
}

// update applies the inline properties form. Assignment is NOT part of the
// edit flow: it lives on POST /tickets/{id}/assign, where the reason and
// target rules are enforced (S4).
func (h *TicketHandlers) update(w http.ResponseWriter, r *http.Request) {
	id, ok := ticketID(r)
	if !ok {
		http.Error(w, "invalid ticket id", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	actor := *userFromContext(r.Context())

	u := domain.TicketUpdate{}
	title := r.Form.Get("title")
	description := r.Form.Get("description")
	categoryID := parseID(r.Form.Get("category_id"))
	p := domain.Priority(r.Form.Get("priority"))
	u.Title = &title
	u.Description = &description
	if categoryID == 0 {
		h.renderEditError(w, r, id, &domain.ValidationError{Field: "category", Message: "invalid category"})
		return
	}
	u.CategoryID = &categoryID
	u.Priority = &p

	_, err := h.tickets.Update(r.Context(), actor, id, u)
	if err != nil {
		h.renderEditError(w, r, id, err)
		return
	}
	h.afterMutation(w, r, id, "ticket_detail")
}

// renderEditError re-renders the inline detail editor with submitted values.
func (h *TicketHandlers) renderEditError(w http.ResponseWriter, r *http.Request, id int64, err error) {
	status, msg := mapError(err)
	if status == http.StatusInternalServerError {
		http.Error(w, msg, status)
		return
	}
	data, _, dataErr := h.detailDataFor(r, id)
	if dataErr != nil {
		http.Error(w, mapErrorMsg(dataErr), statusFor(dataErr))
		return
	}
	data.Error = msg
	data.Values = ticketFormValues{
		Title:       r.Form.Get("title"),
		Description: r.Form.Get("description"),
		CategoryID:  r.Form.Get("category_id"),
		UserID:      r.Form.Get("user_id"),
		Priority:    domain.Priority(r.Form.Get("priority")),
	}
	h.renderer.Render(w, r, "tickets_show", "ticket_detail", data, status)
}

// mapErrorMsg returns the mapped message; statusFor the mapped status.
func mapErrorMsg(err error) string {
	_, msg := mapError(err)
	return msg
}

func statusFor(err error) int {
	status, _ := mapError(err)
	return status
}
