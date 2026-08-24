package httpadapter

import (
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// TicketHandlers implement the ticket list, create, detail, edit, transition
// and comment routes (design "HTTP Layer" route table; tasks 5.4 + 5.5).
// The session user flows in via the middleware context; these handlers only
// parse form input, call the use cases, and render (D6: HX-Request → swap
// fragment, else full page).
type TicketHandlers struct {
	tickets      *application.TicketService
	comments     *application.CommentService
	search       *application.SearchService
	categories   *application.CategoryService
	users        *application.UserService
	desks        application.DeskStore
	workflows    *application.WorkflowService
	runner       *application.WorkflowRunner
	workflowRuns application.WorkflowRunStore
	workflowTx   application.WorkflowUnitOfWork
	renderer     *Renderer
}

// NewTicketHandlers wires the ticket routes against the ticket, comment,
// search, category, and user use cases plus the renderer. The workflow ports
// (runner, run snapshot, unit of work) drive the honest completion route and
// the published-only create-option filter (design S9); workflows provides
// ListAvailableCategories.
func NewTicketHandlers(tickets *application.TicketService, comments *application.CommentService, search *application.SearchService, categories *application.CategoryService, users *application.UserService, desks application.DeskStore, workflows *application.WorkflowService, runner *application.WorkflowRunner, workflowRuns application.WorkflowRunStore, workflowTx application.WorkflowUnitOfWork, renderer *Renderer) *TicketHandlers {
	return &TicketHandlers{
		tickets:      tickets,
		comments:     comments,
		search:       search,
		categories:   categories,
		users:        users,
		desks:        desks,
		workflows:    workflows,
		runner:       runner,
		workflowRuns: workflowRuns,
		workflowTx:   workflowTx,
		renderer:     renderer,
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
	mux.HandleFunc("POST /tickets/{id}/workflow/steps/{position}/complete", h.completeWorkflow)
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
	UsersAssets         bool
	InternalCommentBg   string
}

// pageDataFrom builds the shell payload from the session user. The
// internal-comment background comes from the per-request settings read the
// middleware stamps into the context (appearance-settings spec); "" leaves
// the stylesheet default in place.
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
		InternalCommentBg:   internalCommentBgFrom(r.Context()),
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
	Filters             filterState
	Options             options
	Tickets             []domain.Ticket
	Total               int
	Page                int
	Pages               int
	PrevHref            string
	NextHref            string
	ShowAdvancedFilters bool
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
		pageData:            pageDataFrom(r, "tickets"),
		Filters:             f,
		Options:             opts,
		Tickets:             res.Tickets,
		Total:               res.Total,
		Page:                res.Page,
		Pages:               pages,
		ShowAdvancedFilters: userFromContext(r.Context()).Role != domain.RoleUser,
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
// share it; 422 re-renders carry Error + the submitted values). There is no
// CanAssign flag: Amendment 2 removed creation-time assignment entirely, so
// the form renders no assignee control for ANY role.
type ticketFormData struct {
	pageData
	Error   string
	Values  ticketFormValues
	Options options
}

type ticketFormValues struct {
	Title       string
	Description string
	CategoryID  string
	UserID      string
	Priority    domain.Priority
}

func (h *TicketHandlers) newForm(w http.ResponseWriter, r *http.Request) {
	opts, err := h.createOptions(r)
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

// createOptions builds the create-form option lists with categories filtered
// through WorkflowStore.ListAvailableCategories (design S9 published-only).
// Unpublished/draft-only categories are absent for every role; legacy and
// historical tickets remain filterable on the list screen because listData
// keeps using collectOptions (all categories). The POST path repeats the
// availability check and still answers the exact 422 category message.
func (h *TicketHandlers) createOptions(r *http.Request) (options, error) {
	opts, err := h.collectOptions(r)
	if err != nil {
		return options{}, err
	}
	available, err := h.workflows.ListAvailableCategories(r.Context())
	if err != nil {
		return options{}, err
	}
	opts.Categories = available
	return opts, nil
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
	// Amendment 2: creation binds NO assignee for ANY role. The mere PRESENCE
	// of a user_id or assignee_id parameter — any value, including empty,
	// which is exactly what a stale cached form submits — is rejected with
	// the typed assignee validation error BEFORE any binding/validation and
	// with ZERO writes (no ticket, pin, run, or audit results).
	if r.Form.Has("user_id") || r.Form.Has("assignee_id") {
		h.renderCreateError(w, r, "assignee", domain.ErrMsgCreateUnassignedOnly, http.StatusUnprocessableEntity)
		return
	}
	actor := *userFromContext(r.Context())

	categoryID := parseID(r.Form.Get("category_id"))
	if categoryID == 0 {
		h.renderCreateError(w, r, "category", "category is required", http.StatusUnprocessableEntity)
		return
	}

	in := application.CreateTicketInput{
		Title:       r.Form.Get("title"),
		Description: r.Form.Get("description"),
		CategoryID:  categoryID,
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
	opts, err := h.createOptions(r)
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
			Priority:    domain.Priority(r.Form.Get("priority")),
		},
		Options: opts,
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
		return []transitionTarget{{To: domain.StateClosed}, {To: domain.StateInProgress, NeedsReason: true}}
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
	// SelectedTo carries the transition target submitted by the user so an
	// error re-render (e.g. a blank reopen reason, 422) preserves the select
	// choice instead of resetting it to the "Select…" placeholder.
	SelectedTo string
	// CanCommentInternal is the actor's comment-visibility capability
	// (comment-visibility spec): the comment form offers the internal
	// option only to agent+ actors. This is presentation only — the
	// server-side use case rejects a forged internal value regardless of
	// what the UI shows.
	CanCommentInternal bool
	// CanEdit reports whether the actor may edit this ticket's inline
	// properties (CapEditTicket) on an open ticket. Presentation only; the
	// server-side use case enforces the actor/ticket authorization.
	CanEdit bool
	// Closed reports whether the ticket is in a closed (read-only) state —
	// resolved, closed, or cancelled. A closed ticket hides the inline edit,
	// the properties/assignment controls, and the comment form; only the
	// State control (with its reopen transition) remains (closed-ticket
	// read-only spec). The server-side use cases also reject mutations on
	// closed tickets regardless of what the UI shows.
	Closed bool
	// Pending carries the workflow Pending Actions card state for the current
	// pinned step (design S9): it is populated only for an active run and
	// never exposes a workflow version, pin, or technical cursor.
	Pending workflowPending
	Claim   workflowClaim
}

// workflowPending is the presentation payload for the Pending Actions card
// above the timeline. Active is false (no card rendered) for legacy unpinned
// tickets and completed runs. For an active run it names the current step
// kind, whether the acting session user may complete it (persisted actor
// predicate), and — for a manual task — the step's immutable PINNED
// instruction read from the execution snapshot pendingFor already loads (the
// same pinned definition the runner plans against, never the live draft).
// Automatic steps (least_loaded / resolve / close) render no button.
type workflowClaim struct {
	Active   bool
	Position int
	DeskName string
	CanAct   bool
}

type workflowPending struct {
	Active      bool
	Position    int
	Kind        string // claim | form | manual | auto
	Instruction string // pinned manual-task instruction (Amendment 2)
	Fields      []domain.FormField
	CanAct      bool
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
		Title:      view.Ticket.Title,
		CategoryID: strconv.FormatInt(view.Ticket.CategoryID, 10),
		Priority:   view.Ticket.Priority,
	}
	if view.Ticket.UserID != nil {
		values.UserID = strconv.FormatInt(*view.Ticket.UserID, 10)
	}
	closed := domain.IsClosed(view.Ticket.State)
	return detailData{
		pageData:           pageDataFrom(r, "tickets"),
		View:               view,
		Next:               allowedNext(view.Ticket.State),
		Options:            opts,
		Values:             values,
		CanCommentInternal: application.NewPolicy().Capabilities(actor.Role).Require(application.CapCommentInternal),
		CanEdit:            application.NewPolicy().Capabilities(actor.Role).Require(application.CapEditTicket) && !closed,
		Closed:             closed,
		Pending:            h.pendingFor(r, id, actor, view.Ticket),
		Claim:              h.claimFor(r, id, actor),
	}, 0, nil
}

// pendingFor builds the Pending Actions card state for the current pinned
// step of an active run. It returns Active=false (no card) for legacy
// unpinned tickets and non-active/completed runs. The persisted actor
// predicate decides CanAct: manual_task/form[assignee] require the current
// assignee, form[requester] the ticket requester, and a claim is server-
// enforced on submit (membership is rechecked by the unit of work).
// Automatic steps (least_loaded and the resolve/close terminal steps) render
// as pending but with no button — they advance synchronously with the plan.
func (h *TicketHandlers) pendingFor(r *http.Request, id int64, actor domain.User, t *domain.Ticket) workflowPending {
	snap, err := h.workflowRuns.GetWorkflowExecution(r.Context(), id)
	if err != nil || snap == nil || snap.Run == nil || snap.Run.Status != "active" {
		return workflowPending{}
	}
	cur := snap.Run.CurrentStepIndex
	if cur < 0 || cur >= len(snap.Workflow) {
		return workflowPending{}
	}
	step := snap.Workflow[cur]
	wp := workflowPending{Active: true, Position: cur + 1}
	switch step.Type {
	case domain.StepAssignToDesk:
		if step.AssignToDesk == nil {
			return workflowPending{}
		}
		if step.AssignToDesk.Strategy != domain.StrategyClaim {
			wp.Kind = "auto"
			return wp
		}
		// Claim controls live in the Assignment sidebar, never Pending Actions.
		return workflowPending{}
	case domain.StepManualTask:
		wp.Kind = "manual"
		if step.ManualTask == nil {
			return workflowPending{}
		}
		// Amendment 2 (WB.5): the pinned instruction leads the card. It comes
		// verbatim from the execution snapshot's immutable pinned step.
		wp.Instruction = step.ManualTask.Instructions
		wp.CanAct = t.UserID != nil && *t.UserID == actor.ID
	case domain.StepForm:
		if step.Form == nil {
			return workflowPending{}
		}
		wp.Kind = "form"
		wp.Fields = step.Form.Fields
		if step.Form.Actor == domain.FormActorRequester {
			wp.CanAct = t.RequesterUserID != nil && *t.RequesterUserID == actor.ID
		} else {
			wp.CanAct = t.UserID != nil && *t.UserID == actor.ID
		}
	case domain.StepResolve, domain.StepClose:
		wp.Kind = "auto"
	default:
		return workflowPending{}
	}
	return wp
}

// claimFor derives the sidebar projection from the current pinned step. It is
// presentation only: ApplyWorkflowPlan repeats all actor, pin, cursor, role, and
// membership checks under its immediate transaction before writes.
func (h *TicketHandlers) claimFor(r *http.Request, id int64, actor domain.User) workflowClaim {
	snap, err := h.workflowRuns.GetWorkflowExecution(r.Context(), id)
	if err != nil || snap == nil || snap.Run == nil || snap.Run.Status != "active" {
		return workflowClaim{}
	}
	cur := snap.Run.CurrentStepIndex
	if cur < 0 || cur >= len(snap.Workflow) {
		return workflowClaim{}
	}
	step := snap.Workflow[cur]
	if step.Type != domain.StepAssignToDesk || step.AssignToDesk == nil || step.AssignToDesk.Strategy != domain.StrategyClaim {
		return workflowClaim{}
	}
	claim := workflowClaim{Active: true, Position: cur + 1, DeskName: "Unknown desk"}
	if h.desks == nil {
		return claim
	}
	desk, err := h.desks.GetByID(r.Context(), step.AssignToDesk.DeskID)
	if err == nil && desk != nil {
		claim.DeskName = desk.Name
	}
	if !actor.Active || !actor.Role.AtLeast(domain.RoleAgent) {
		return claim
	}
	members, err := h.desks.ListMembers(r.Context(), step.AssignToDesk.DeskID)
	if err != nil {
		return claim
	}
	for _, member := range members {
		if member.ID == actor.ID && member.Active && member.Role.AtLeast(domain.RoleAgent) {
			claim.CanAct = true
			break
		}
	}
	return claim
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

	visibility := r.Form.Get("visibility")
	if r.Form.Get("internal") != "" {
		visibility = string(domain.CommentInternal)
	}
	_, err := h.comments.Add(r.Context(), actor, id, r.Form.Get("body"), visibility)
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
	// Preserve the transition the user picked so a reopen-reason 422 keeps the
	// option selected and the reason group revealed after the outerHTML swap.
	data.SelectedTo = r.Form.Get("to")
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
	p := domain.Priority(r.Form.Get("priority"))
	u.Title = &title
	// The description and the category are immutable after creation: the
	// edit form carries no fields for them, and forged ones are deliberately
	// never read — mirroring how forged assignment fields are ignored on
	// this route.
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
		Title:      r.Form.Get("title"),
		CategoryID: r.Form.Get("category_id"),
		UserID:     r.Form.Get("user_id"),
		Priority:   domain.Priority(r.Form.Get("priority")),
	}
	h.renderer.Render(w, r, "tickets_show", "ticket_detail", data, status)
}

// maxSolutionLength is the Amendment 2 transport bound for a trimmed
// manual-task solution; the SQLite CHECK mirrors it as defense in depth.
const maxSolutionLength = 2000

// completeWorkflow implements the ONLY completion route
// POST /tickets/{id}/workflow/steps/{position}/complete (design S9). The
// {position} is one-based and maps to the zero-based cursor through the
// WorkflowRunner; stale/missing/non-positive/mismatched positions return the
// typed ErrWorkflowPositionConflict mapped to 422 with NO writes. The closed
// input grammar is validated against the current pinned step before any
// write: a claim posts only a reason, a manual step posts no metadata, and a
// form posts raw answer_<zeroPos> values (unknown/duplicate/extra/ambiguous
// rejected). Planning and persistence reuse the runner + unit-of-work
// contracts.
func (h *TicketHandlers) completeWorkflow(w http.ResponseWriter, r *http.Request) {
	id, ok := ticketID(r)
	if !ok {
		http.Error(w, "invalid ticket id", http.StatusBadRequest)
		return
	}
	pos, err := strconv.Atoi(r.PathValue("position"))
	if err != nil || pos < 1 {
		h.renderDetailError(w, r, id, domain.NewWorkflowPositionConflictError("workflow position conflict"))
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	actor := *userFromContext(r.Context())

	// Authorize the ticket read through the scoped view (404 for out-of-scope)
	// before loading the execution snapshot.
	if _, err := h.tickets.GetByID(r.Context(), actor, id); err != nil {
		http.Error(w, mapErrorMsg(err), statusFor(err))
		return
	}
	snap, err := h.workflowRuns.GetWorkflowExecution(r.Context(), id)
	if err != nil {
		http.Error(w, mapErrorMsg(err), statusFor(err))
		return
	}
	if snap == nil {
		h.renderDetailError(w, r, id, domain.NewWorkflowPositionConflictError("workflow position conflict"))
		return
	}

	// Amendment 2 (WB.4): the OPTIONAL plain-text solution is extracted and
	// bounded BEFORE planning — trimmed by the handler so whitespace-only
	// arrives empty, and a trimmed value above 2,000 characters is a typed
	// 422 ValidationError{Field: "solution"} with zero writes.
	solution := strings.TrimSpace(r.Form.Get("solution"))
	if len(solution) > maxSolutionLength {
		h.renderDetailError(w, r, id, &domain.ValidationError{Field: "solution", Message: domain.ErrMsgSolutionTooLong})
		return
	}

	raw, err := h.workflowFormAnswers(snap, r.Form)
	if err != nil {
		h.renderDetailError(w, r, id, err)
		return
	}
	cmd := application.CompleteWorkflowCommand{
		TicketID: id, ActorUserID: actor.ID, ActorName: actor.Name,
		ExpectedPosition: pos, RawAnswers: raw, Solution: solution,
	}
	plan, err := h.runner.PlanComplete(r.Context(), *snap, cmd)
	if err != nil {
		h.renderDetailError(w, r, id, err)
		return
	}
	if _, err := h.workflowTx.ApplyWorkflowPlan(r.Context(), plan); err != nil {
		h.renderDetailError(w, r, id, err)
		return
	}

	// Completion success answers 200 in both modes (the PR9 runtime contract
	// pins 200 rather than the mutation routes' 303; HTMX swaps the
	// #ticket-detail outerHTML fragment, full renders the tickets_show page).
	data, status, err := h.detailDataFor(r, id)
	if err != nil {
		http.Error(w, mapErrorMsg(err), status)
		return
	}
	h.renderer.Render(w, r, "tickets_show", "ticket_detail", data, http.StatusOK)
}

// workflowFormAnswers validates the closed completion grammar against the
// current pinned step and returns the raw positional answer values (or nil
// for steps that take no answers). Forged fields are rejected with a 422
// before any plan/write, never silently dropped.
func (h *TicketHandlers) workflowFormAnswers(snap *application.WorkflowExecutionSnapshot, form url.Values) (application.RawPositionalValues, error) {
	cur := snap.Run.CurrentStepIndex
	if cur < 0 || cur >= len(snap.Workflow) {
		return nil, domain.NewWorkflowPositionConflictError("workflow position conflict")
	}
	step := snap.Workflow[cur]
	switch step.Type {
	case domain.StepManualTask:
		if hasWorkflowMeta(form) {
			return nil, &domain.ValidationError{Field: "answers", Message: "manual_task ignores metadata"}
		}
		return nil, nil
	case domain.StepAssignToDesk:
		if step.AssignToDesk == nil {
			return nil, &domain.ValidationError{Field: "type", Message: "assign_to_desk requires config"}
		}
		if step.AssignToDesk.Strategy != domain.StrategyClaim {
			return nil, &domain.ValidationError{Field: "state", Message: "automatic step"}
		}
		if hasWorkflowMeta(form) || form.Has("reason") || form.Has("solution") {
			return nil, &domain.ValidationError{Field: "claim", Message: "claim posts no form values"}
		}
		return nil, nil
	case domain.StepForm:
		return parsePositionalAnswers(form, len(step.Form.Fields))
	default:
		// Terminal and unknown steps never accept a human completion form.
		return nil, &domain.ValidationError{Field: "state", Message: "workflow step cannot complete in current ticket state"}
	}
}

// hasWorkflowMeta reports whether the submitted form carries any workflow-
// mutation field beyond the allowed plain reason (design S9: claim/manual
// post no assignee id or raw answers).
func hasWorkflowMeta(form url.Values) bool {
	for k := range form {
		if strings.HasPrefix(k, "answer_") || k == "assignee_id" || k == "user_id" {
			return true
		}
	}
	return false
}

// parsePositionalAnswers reads answer_<zeroPos> values against the pinned
// field count. It rejects malformed, unknown, duplicate, and ambiguous
// positions before the runner plans any write, then returns deterministic
// positional values for typed domain decoding.
func parsePositionalAnswers(form url.Values, fieldCount int) (application.RawPositionalValues, error) {
	var raw application.RawPositionalValues
	for k, vals := range form {
		if !strings.HasPrefix(k, "answer_") {
			continue
		}
		n, err := strconv.Atoi(strings.TrimPrefix(k, "answer_"))
		if err != nil || n < 0 {
			return nil, &domain.ValidationError{Field: "answers", Message: fmt.Sprintf("invalid answer position %q", k)}
		}
		if n >= fieldCount {
			return nil, &domain.ValidationError{Field: "answers", Message: fmt.Sprintf("unknown position %d", n)}
		}
		raw = append(raw, application.RawPositionalValue{Position: n, Values: vals})
	}
	sort.Slice(raw, func(i, j int) bool { return raw[i].Position < raw[j].Position })
	seen := make(map[int]struct{}, len(raw))
	for _, value := range raw {
		if _, ok := seen[value.Position]; ok {
			return nil, &domain.ValidationError{Field: "answers", Message: fmt.Sprintf("duplicate position %d", value.Position)}
		}
		seen[value.Position] = struct{}{}
		if len(value.Values) > 1 {
			return nil, &domain.ValidationError{Field: "answers", Message: fmt.Sprintf("ambiguous values for position %d", value.Position)}
		}
	}
	return raw, nil
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
