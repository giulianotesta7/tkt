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
	search     *application.SearchService
	categories *application.CategoryService
	users      *application.UserService
	renderer   *Renderer
}

// NewTicketHandlers wires the ticket routes against the ticket, search,
// category, and user use cases plus the renderer.
func NewTicketHandlers(tickets *application.TicketService, search *application.SearchService, categories *application.CategoryService, users *application.UserService, renderer *Renderer) *TicketHandlers {
	return &TicketHandlers{
		tickets:    tickets,
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
	// Task 5.5 registers the detail/edit/transition/comment routes here.
}

// pageData is the shared presentation payload for app-shell pages: the rail
// highlights the active section and the operator chip shows the session
// user.
type pageData struct {
	NavActive   string
	CurrentUser domain.User
}

// pageDataFrom builds the shell payload from the session user.
func pageDataFrom(r *http.Request, nav string) pageData {
	u := userFromContext(r.Context())
	return pageData{NavActive: nav, CurrentUser: *u}
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
	users, err := h.users.List(r.Context())
	if err != nil {
		return options{}, err
	}
	var assignable []domain.User
	for _, u := range users {
		if u.Active {
			assignable = append(assignable, u)
		}
	}
	return options{
		States:          listStates,
		Priorities:      listPriorities,
		Categories:      categories,
		Users:           users,
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

// chip is one summary chip (state or priority) with its count and filter
// link (ticket-search summary-chips spec).
type chip struct {
	Label  string
	Count  int
	Href   string
	Active bool
}

// buildChips assembles the state + priority chips reflecting the filtered
// result set.
func buildChips(f filterState, byState map[domain.State]int, byPriority map[domain.Priority]int) []chip {
	chips := make([]chip, 0, len(listStates)+len(listPriorities))
	for _, s := range listStates {
		with := f
		with.State = s
		chips = append(chips, chip{Label: string(s), Count: byState[s], Href: listHref(with, 1), Active: f.State == s})
	}
	for _, p := range listPriorities {
		with := f
		with.Priority = p
		chips = append(chips, chip{Label: string(p), Count: byPriority[p], Href: listHref(with, 1), Active: f.Priority == p})
	}
	return chips
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
	Chips    []chip
}

func (h *TicketHandlers) index(w http.ResponseWriter, r *http.Request) {
	redirect(w, r, "/tickets")
}

// listData builds the full list payload for the given filters and page.
func (h *TicketHandlers) listData(r *http.Request, f filterState, page int) (listData, error) {
	opts, err := h.collectOptions(r)
	if err != nil {
		return listData{}, err
	}
	res, err := h.search.Search(r.Context(), f.query(), page)
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
		Chips:    buildChips(f, res.ByState, res.ByPriority),
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
	Error   string
	Values  ticketFormValues
	Options options
}

type ticketFormValues struct {
	Title          string
	Description    string
	RequesterName  string
	RequesterEmail string
	CategoryID     string
	UserID         string
	Priority       domain.Priority
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
	}
	h.renderer.Render(w, r, "tickets_new", "ticket_form", data, http.StatusOK)
}

// create parses the create form, calls the use case, and answers per D6:
// HX-Request → refreshed ticket_list fragment (chips OOB); full request →
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
		Title:          r.Form.Get("title"),
		Description:    r.Form.Get("description"),
		RequesterName:  r.Form.Get("requester_name"),
		RequesterEmail: r.Form.Get("requester_email"),
		CategoryID:     categoryID,
		UserID:         userID,
		Priority:       domain.Priority(r.Form.Get("priority")),
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
			Title:          r.Form.Get("title"),
			Description:    r.Form.Get("description"),
			RequesterName:  r.Form.Get("requester_name"),
			RequesterEmail: r.Form.Get("requester_email"),
			CategoryID:     r.Form.Get("category_id"),
			UserID:         r.Form.Get("user_id"),
			Priority:       domain.Priority(r.Form.Get("priority")),
		},
		Options: opts,
	}
	_ = field // the inline banner is a single generic error block
	h.renderer.Render(w, r, "tickets_new", "ticket_form", data, status)
}
