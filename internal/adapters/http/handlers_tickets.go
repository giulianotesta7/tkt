package httpadapter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
	"github.com/giulianotesta7/tkt/internal/i18n"
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
	catalog      *application.CatalogService
	metrics      *application.TicketMetricsService
	// sla is the OPTIONAL SLA projection read used by the two STAFF ticket
	// lists (issue #211). An UNSET service means "no SLA is rendered in any
	// list": every row's SLA row stays nil and the lists render exactly as
	// before. It is never dereferenced without a nil check, so unset can
	// never panic; WithSLA is what makes the badge appear.
	sla *application.SLAService
	// preferences is the OPTIONAL per-user preference read (issue #210). An
	// UNSET service means "no stored default is ever applied": the list keeps
	// its historical newest-first order. It is only read for the staff queue
	// surfaces and only when the request carries no explicit `sort`.
	preferences *application.PreferencesService
	// attachments is the OPTIONAL attachment read side (issue #366): the
	// ticket-detail listing reads it, and attachmentDownloads authorises the
	// download route. Unset means neither exists, so an unwired handler
	// renders and routes exactly as it did before #366 (WithAttachments is
	// what turns both on).
	attachments         application.AttachmentStore
	attachmentDownloads *application.AttachmentService
	// clock is the OPTIONAL request clock the queue's relative "Updated"
	// reading is judged against (issue #382). Unset leaves listData.Now zero,
	// so every fixture and golden keeps rendering the absolute instant.
	clock domain.Clock
	// savedReplies is the OPTIONAL composer picker (issue #418). Unset means
	// the detail renders no saved-reply picker at all, so a handler built
	// without the service (a focused test, an out-of-band render) behaves
	// exactly as before wiring; WithCannedReplies turns it on.
	savedReplies *application.CannedReplyService
	// follows is the OPTIONAL follow and mention read (issue #418). Unset means
	// the detail renders no follow control, no followers row, no mention list
	// and registers no follow route, so an unwired handler behaves exactly as
	// before wiring; WithFollows turns all four on.
	follows *application.FollowService
	// visits is the OPTIONAL last-visit read/write (issue #415). UNSET means
	// "no mark is ever rendered and no visit is ever recorded": every row's
	// Changed stays false and the detail page writes nothing, so a handler
	// built without WithVisits behaves exactly as it did before #415.
	visits *application.TicketVisitService
}

// NewTicketHandlers wires the ticket routes against the ticket, comment,
// search, category, and user use cases plus the renderer. The workflow ports
// (runner, run snapshot, unit of work) drive the honest completion route; the
// workflows service supplies ListRequesterSummaries, the one viability rule
// the category picker and the create form's fallback select both read.
func NewTicketHandlers(tickets *application.TicketService, comments *application.CommentService, search *application.SearchService, categories *application.CategoryService, users *application.UserService, desks application.DeskStore, workflows *application.WorkflowService, runner *application.WorkflowRunner, workflowRuns application.WorkflowRunStore, workflowTx application.WorkflowUnitOfWork, renderer *Renderer, catalogs ...*application.CatalogService) *TicketHandlers {
	h := &TicketHandlers{
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
	if len(catalogs) > 0 {
		h.catalog = catalogs[0]
	}
	return h
}

// WithSLA wires the optional SLA projection read into the ticket handlers
// (issue #211). It follows the same fluent shape WithMetrics already uses, so
// the constructor keeps the exact signature the five positional call sites
// depend on (two of them unrelated tests); the SLA service arrives through an
// explicit setter instead of a trailing constructor parameter. A nil or unset
// service leaves every staff list's SLA row nil: no badge, no deadline, no
// panic.
func (h *TicketHandlers) WithSLA(sla *application.SLAService) *TicketHandlers {
	h.sla = sla
	return h
}

// WithPreferences wires the optional per-user preferences read into the
// ticket handlers (issue #210). It follows the same fluent shape as WithSLA
// and WithMetrics, so the constructor keeps the positional signature its
// existing call sites depend on. A nil or unset service leaves every list's
// order exactly as parsed — newest first unless the URL says otherwise — so
// existing tests and any future caller that does not opt in are unaffected.
func (h *TicketHandlers) WithPreferences(preferences *application.PreferencesService) *TicketHandlers {
	h.preferences = preferences
	return h
}

// WithClock wires the request clock the queue reads once per list render
// (issue #382). It follows the fluent shape of WithSLA and WithPreferences.
func (h *TicketHandlers) WithClock(clock domain.Clock) *TicketHandlers {
	h.clock = clock
	return h
}

// WithFollows wires the follow and mention use cases into the ticket handlers
// (issue #418): the detail's follow control, its followers row, the composer's
// candidate list, and the two routes that change the subscription. It follows
// the same fluent shape as the setters above, so the constructor keeps the
// positional signature its call sites depend on, and a handler built without it
// renders and routes exactly as it did before.
func (h *TicketHandlers) WithFollows(follows *application.FollowService) *TicketHandlers {
	h.follows = follows
	return h
}

// WithCannedReplies wires the composer's saved-reply picker (issue #418). It
// follows the fluent shape of WithClock, so the constructor keeps the
// positional signature its existing call sites depend on. A nil or unset
// service renders no picker: the comment form is byte-identical to the one
// without saved replies.
func (h *TicketHandlers) WithCannedReplies(replies *application.CannedReplyService) *TicketHandlers {
	h.savedReplies = replies
	return h
}

// WithVisits wires the optional last-visit read and write (issue #415). It
// follows the fluent shape of WithSLA, WithPreferences and WithClock: a nil or
// unset service leaves every row unmarked and records no visit, so the lists
// and the detail page render exactly as they did before, and no store read is
// paid for a mark nobody displays.
func (h *TicketHandlers) WithVisits(visits *application.TicketVisitService) *TicketHandlers {
	h.visits = visits
	return h
}

// Register mounts the ticket routes (D9 method+path patterns). Task 5.4
// covers list + create; 5.5 registers the detail/edit/transition/comment
// routes on the same handlers.
func (h *TicketHandlers) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /", h.index)
	mux.HandleFunc("GET /tickets", h.list)
	mux.HandleFunc("GET /tickets/metrics", h.metricsView)
	// CSV downloads (issue #409). Each one mirrors the screen above it: same
	// filters, same authorization, every match instead of one page.
	mux.HandleFunc("GET /tickets/export.csv", h.exportTickets)
	mux.HandleFunc("GET /tickets/metrics/export.csv", h.exportMetrics)
	mux.HandleFunc("GET /tickets/new", h.newForm)
	mux.HandleFunc("POST /tickets", h.create)
	mux.HandleFunc("GET /tickets/{id}", h.show)
	mux.HandleFunc("POST /tickets/{id}/edit", h.update)
	mux.HandleFunc("POST /tickets/{id}/assign", h.assign)
	mux.HandleFunc("POST /tickets/{id}/transition", h.transition)
	mux.HandleFunc("POST /tickets/{id}/hold", h.hold)
	mux.HandleFunc("POST /tickets/{id}/resume", h.resume)
	mux.HandleFunc("POST /tickets/{id}/confirmation", h.confirmation)
	mux.HandleFunc("POST /tickets/{id}/comments", h.addComment)
	mux.HandleFunc("POST /tickets/{id}/workflow/steps/{position}/complete", h.completeWorkflow)
	// The download route exists only when WithAttachments wired it (issue
	// #366): a handler built without the service answers the mux's 404
	// instead of panicking on a nil service.
	if h.attachmentDownloads != nil {
		mux.HandleFunc("GET /attachments/{id}", h.downloadAttachment)
	}
	// The follow routes exist only when WithFollows wired the use case (issue
	// #418): a handler built without it answers the mux's 404, exactly like it
	// did before the routes existed.
	if h.follows != nil {
		h.registerFollows(mux)
	}
}

// pageData is the shared presentation payload for app-shell pages: the rail
// highlights the active section and the operator chip shows the session
// user.
type pageData struct {
	NavActive         string
	CurrentUser       domain.User
	CanManageUsers    bool
	CanManageSettings bool
	CanManageDesks    bool
	// CanUseSavedReplies reports whether the rail offers the saved-reply
	// manager (issue #418): the internal-comment capability, which is what a
	// role that works inside a ticket holds. Presentation only; every
	// saved-reply route checks the capability again.
	CanUseSavedReplies   bool
	CanManageCategories  bool
	CanGrantAdmin        bool
	UsersAssets          bool
	PageFoundationAssets bool
	WorkflowAssets       bool
	CategoryAssets       bool
	MetricsAssets        bool
	// SettingsAssets loads the settings section stylesheet and the save-bar
	// script (issue #383, S2). It is set only on the settings pages, so no other
	// surface downloads them.
	SettingsAssets bool
	// QueueAssets loads the staff queue's row-navigation script (issue #382).
	QueueAssets bool
	// SLACountdownAssets loads the live SLA countdown script (issue #211,
	// PR 6). It is set only where the ticket detail page is built and only
	// when that ticket carries a frozen commitment, because the panel is the
	// only place that renders a countdown element.
	SLACountdownAssets bool
	// TicketDetailAssets loads the ticket detail's own script (issue #386):
	// the title guard, the property pickers, the hold and reason dialogs, and
	// the phone fold of the properties panel. It is set only where the detail
	// page is built, so no other surface downloads it.
	TicketDetailAssets bool
	// SavedRepliesAssets loads the saved-reply manager's script (issue #418):
	// the placeholder chips and the delete confirmation. It is set only on the
	// manager screen, so no other surface downloads it.
	SavedRepliesAssets bool
	// CreateAssets loads the ticket-creation script (issue #391): the category
	// picker that enhances the native select into a searchable, grouped listbox,
	// and the service catalog's live card filter. It is set only on the two
	// creation surfaces, so no other page downloads it.
	CreateAssets      bool
	InternalCommentBg string
	SaveFeedback      saveFeedbackData
	// SidebarCollapsed is the session user's stored sidebar state (issue #377,
	// S2), stamped once per request by the session middleware. The shell
	// renders it on the first paint, so a collapsed sidebar never flashes
	// expanded while a script catches up.
	SidebarCollapsed bool
	// Theme is the session user's stored interface theme (issue #378, S1/S2),
	// normalized and stamped once per request by the session middleware. The
	// shell turns an explicit choice into the page's data-theme attribute and
	// renders no attribute for "system".
	Theme string
	// Brand is the instance identity the shell renders (issue #383, S5): the
	// name, the help desk name, the monogram or logo mark, and the logo's
	// content hash. It comes from the identity the session middleware read once
	// per request, so pageDataFrom performs no query of its own.
	Brand brandData
	// NotificationBell is the rail bell's INITIAL state (issue #344, S13). It
	// is always the zero value: an ordinary page render never queries the
	// unread count — the bell's own endpoint answers it, and the browser fetches
	// it on load. The field exists so the shell can render the bell in that zero
	// state instead of an empty rail slot, without pageDataFrom reading anything.
	NotificationBell notificationBell
}

// pageDataFrom builds the shell payload from the session user. The
// internal-comment background, the sidebar state, and the theme come from the
// per-request middleware reads stamped into the context (appearance-settings
// spec, issue #377 S2, issue #378 S2); "" leaves the stylesheet default in
// place, an absent sidebar stamp renders the expanded shell, and an absent
// theme stamp resolves to the system default.
func pageDataFrom(r *http.Request, nav string) pageData {
	u := userFromContext(r.Context())
	caps := application.NewPolicy().Capabilities(u.Role)
	return pageData{
		NavActive:           nav,
		CurrentUser:         *u,
		CanManageUsers:      caps.Require(application.CapManageUsers),
		CanManageSettings:   caps.Require(application.CapManageSettings),
		CanManageDesks:      caps.Require(application.CapManageDesks),
		CanUseSavedReplies:  caps.Require(application.CapCommentInternal),
		CanManageCategories: caps.Require(application.CapManageCategories),
		CanGrantAdmin:       caps.Require(application.CapGrantAdmin),
		InternalCommentBg:   internalCommentBgFrom(r.Context()),
		SaveFeedback:        readSaveFeedback(r),
		SidebarCollapsed:    sidebarCollapsedFrom(r.Context()),
		Theme:               themeFrom(r.Context()),
		Brand:               brandFromContext(r.Context()),
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

// The three list orderings the `sort` query parameter accepts (issue #211,
// PR 4). sortNewest is the default order; parseFilters normalizes an absent,
// empty, or unknown value to it so the template always has a concrete value
// to mark `selected`.
const (
	sortNewest   = "newest"
	sortPriority = "priority"
	sortUrgency  = "urgency"
)

// filterState is the parsed list filter set (ticket-search spec). Zero
// values mean "no filter"; unknown values are ignored (threat matrix).
type filterState struct {
	State      domain.State
	Priority   domain.Priority
	CategoryID string
	UserID     string
	Q          string
	// Sort is the chosen list ordering (sortNewest, sortPriority, or
	// sortUrgency). It is always concrete after parseFilters.
	Sort string
	// SortExplicit records whether the request carried a `sort` query key at
	// all. An explicit `sort` (including `sort=newest`) must beat the actor's
	// stored default, so the handler needs to tell "the URL chose newest"
	// apart from "the URL said nothing".
	SortExplicit bool
	// View is the queue tab (issue #382): viewAll, viewMine, viewUnassigned,
	// viewOnHold or viewResolved. It is always concrete after parseFilters;
	// an unknown value selects viewAll, never an error.
	View string
}

// Queue views (issue #382). They are presentation sections of the staff
// queue, composed ON TOP of the actor's scope and the other filters; none of
// them widens what the actor may read.
const (
	viewAll        = "all"
	viewMine       = "mine"
	viewUnassigned = "unassigned"
	viewOnHold     = "on_hold"
	viewResolved   = "resolved"
)

// queueViews lists the tabs in display order.
var queueViews = []string{viewAll, viewMine, viewUnassigned, viewOnHold, viewResolved}

// queueViewLabelKeys maps a view to its catalogue label.
var queueViewLabelKeys = map[string]string{
	viewAll:        "tickets.view.all",
	viewMine:       "tickets.view.mine",
	viewUnassigned: "tickets.view.unassigned",
	viewOnHold:     "tickets.view.on_hold",
	viewResolved:   "tickets.view.resolved",
}

// validView reports whether v names a queue view.
func validView(v string) bool {
	_, ok := queueViewLabelKeys[v]
	return ok
}

// parseFilters reads the query string, ignoring unknown or malformed values.
func parseFilters(r *http.Request) filterState {
	q := r.URL.Query()
	f := filterState{Q: q.Get("q"), Sort: sortNewest, SortExplicit: q.Has("sort"), View: viewAll}
	if v := q.Get("view"); validView(v) {
		f.View = v
	}
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
	// Sort is a presentation choice, not a restriction: an UNKNOWN, empty,
	// or absent value silently selects the default newest-first order
	// instead of erroring. A bad sort must never render a 422 or an error
	// page (threat matrix: unknown values are ignored), so this switch has
	// no rejecting branch and no error return.
	switch s := q.Get("sort"); s {
	case sortPriority, sortUrgency:
		f.Sort = s
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
	switch f.View {
	case viewUnassigned:
		q.Unassigned = true
	case viewOnHold:
		q.OnHold = true
	case viewResolved:
		q.States = []domain.State{domain.StateResolved, domain.StateClosed}
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
	switch f.Sort {
	case sortPriority:
		q.SortByPriority = true
	case sortUrgency:
		q.SortByUrgency = true
	}
	return q
}

// listHref rebuilds the /tickets URL carrying the current filters.
func listHref(f filterState, page int) string {
	v := url.Values{}
	if f.View != "" && f.View != viewAll {
		v.Set("view", f.View)
	}
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
	if f.Sort == sortPriority || f.Sort == sortUrgency {
		v.Set("sort", f.Sort)
	}
	if page > 1 {
		v.Set("page", strconv.Itoa(page))
	}
	if len(v) == 0 {
		return "/tickets"
	}
	return "/tickets?" + v.Encode()
}

// agentListHref rebuilds the /tickets URL for one agent section page while
// keeping the other sections' cursors, so paging one queue never resets the
// two beside it.
func agentListHref(f filterState, assignedPage, donePage, claimablePage int) string {
	v := ticketFilterValues(f)
	if assignedPage > 1 {
		v.Set("assigned_page", strconv.Itoa(assignedPage))
	}
	if donePage > 1 {
		v.Set("done_page", strconv.Itoa(donePage))
	}
	if claimablePage > 1 {
		v.Set("claimable_page", strconv.Itoa(claimablePage))
	}
	if len(v) == 0 {
		return "/tickets"
	}
	return "/tickets?" + v.Encode()
}

func ticketFilterValues(f filterState) url.Values {
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
	if f.Sort == sortPriority || f.Sort == sortUrgency {
		v.Set("sort", f.Sort)
	}
	return v
}

// slaRow is the optional per-row SLA summary carried by BOTH staff ticket
// lists (issue #211). It carries ONE thing: the projection's WORST milestone
// state — whether the ticket is still standing. The milestone, its due instant
// and the time left belong to the ticket's own panel, where there is room to
// name them properly; the list is a triage surface and answers a single
// question. A nil row means the ticket has no frozen commitment and its SLA
// cell renders EMPTY — never a "no SLA" badge.
type slaRow struct {
	State domain.SLAState
}

// slaResponseLabelKey and slaResolveLabelKey name the two milestones in the row
// deadline copy. They are catalogue keys: the visible label follows the instance
// language (S18), and the English catalogue keeps today's bytes.
const (
	slaResponseLabelKey = "sla.detail.first_response"
	slaResolveLabelKey  = "sla.detail.resolve"
)

// slaRowFor derives the optional row summary from one projection. A nil
// projection (unknown id) or a ticket with no frozen commitment yields nil;
// otherwise the row carries the worst of the two milestone states, which is
// the only thing the list says. The urgency ordering keys on the same worst
// state, in SQL, so the list's order and its signal can never disagree.
func slaRowFor(p domain.SLAProjection) *slaRow {
	if p.Frozen == nil {
		return nil
	}
	return &slaRow{State: p.Overall}
}

// slaMilestoneView is one pre-formatted milestone block of the ticket detail
// SLA panel (issue #211). The template only prints fields: Target is the
// frozen target in compact h/m/s units, DueAt and AchievedAt stay time.Time
// so the `timestamp` partial renders a truthful <time datetime>, State drives
// the existing state_badge component, and Remaining is EMPTY once the
// milestone is achieved (a met milestone has no outstanding time).
type slaMilestoneView struct {
	Label string
	State domain.SLAState
	DueAt time.Time
	// Remaining is the pre-formatted time left on a PENDING milestone: the
	// ticker's initial value, so a browser with no JavaScript still reads the
	// truth instead of an empty cell.
	Remaining string
	// Countdown marks a PENDING milestone whose due instant the client turns
	// into a live countdown (issue #211, PR 6). An achieved milestone never
	// ticks, a zero due instant (a legacy row with no frozen target) is not a
	// countdown target either, and a PAUSED milestone holds a frozen remainder
	// — ticking it would show a clock that is deliberately stopped (issue
	// #352).
	Countdown bool
	// Paused marks a pending milestone whose clock is stopped because the
	// ticket waits for the requester (issue #352). The template renders the
	// paused chip INSTEAD of the countdown.
	Paused bool
}

// slaPanelView is the ticket detail SLA section (issue #211). It exists only
// when the ticket carries a frozen commitment; a nil panel renders nothing,
// so a requester or a legacy ticket sees no SLA section at all.
type slaPanelView struct {
	Overall    domain.SLAState
	Milestones []slaMilestoneView
	// ProjectedAt is the instant the projection was taken, stamped onto the
	// panel as data-server-now so the client countdown can derive its clock
	// offset (issue #211, PR 6). A presentation anchor only: it never
	// changes a state.
	ProjectedAt time.Time
}

// slaPanelFor builds the pre-formatted panel from one projection. A nil
// Frozen (no commitment, or the pre-0016 zero-due legacy shape) yields nil —
// absence is a state, never a "no SLA" panel.
func slaPanelFor(locale string, p domain.SLAProjection) *slaPanelView {
	if p.Frozen == nil {
		return nil
	}
	return &slaPanelView{
		Overall:     p.Overall,
		ProjectedAt: p.ProjectedAt,
		Milestones: []slaMilestoneView{
			slaMilestoneViewFor(locale, i18n.T(locale, slaResponseLabelKey), p.FirstResponse),
			slaMilestoneViewFor(locale, i18n.T(locale, slaResolveLabelKey), p.Resolve),
		},
	}
}

// slaMilestoneViewFor pre-formats one milestone. Remaining is populated only
// while the milestone is pending: a negative remainder reads "30m overdue",
// a non-negative one "in 2h 30m", and an achieved milestone carries no
// remaining label at all (the template drops the row).
func slaMilestoneViewFor(locale, label string, m domain.SLAMilestoneStatus) slaMilestoneView {
	v := slaMilestoneView{
		Label:     label,
		State:     m.State,
		DueAt:     m.DueAt,
		Countdown: m.AchievedAt == nil && !m.DueAt.IsZero() && !m.Paused,
		Paused:    m.Paused,
	}
	if m.AchievedAt == nil {
		seconds := int(m.Remaining / time.Second)
		if seconds < 0 {
			v.Remaining = slaDurationLabel(-seconds) + " " + i18n.T(locale, "sla.detail.overdue")
		} else {
			v.Remaining = slaDurationLabel(seconds)
		}
	}
	return v
}

// slaDurationLabel formats a duration in whole seconds as compact h/m/s units
// ("4h 0m", "30m", "45s") for the ticket detail SLA panel. The render path
// must never format a duration itself (no duration or target helper is in the
// template FuncMap), so the handler pre-formats every label here. The largest
// non-zero unit always carries the next smaller one, so an exact four-hour
// target reads "4h 0m" instead of a bare "4h"; a trailing zero unit is
// dropped, and seconds survive only when the value does not divide evenly
// into minutes — nothing is rounded away.
func slaDurationLabel(total int) string {
	if total < 0 {
		return "-" + slaDurationLabel(-total)
	}
	hours, minutes, seconds := total/3600, total%3600/60, total%60
	switch {
	case hours > 0 && seconds > 0:
		return fmt.Sprintf("%dh %dm %ds", hours, minutes, seconds)
	case hours > 0:
		return fmt.Sprintf("%dh %dm", hours, minutes)
	case minutes > 0 && seconds > 0:
		return fmt.Sprintf("%dm %ds", minutes, seconds)
	case minutes > 0:
		return fmt.Sprintf("%dm", minutes)
	default:
		return fmt.Sprintf("%ds", seconds)
	}
}

// ticketRow wraps one staff-list ticket with its batched row context. The
// domain.Ticket is EMBEDDED so every template expression ({{.ID}},
// {{.Title}}, …) keeps resolving. Context carries the agent queue facts
// (desk/task/position) and SLA carries the optional summary; the plain staff
// table passes nil Context and the SLA projection map, so both staff lists
// carry the SAME row shape.
type ticketRow struct {
	domain.Ticket
	Context application.AgentTicketRowContext
	SLA     *slaRow
	// AssigneeName and CategoryName are the row's triage context (issue
	// #382), resolved from the option lists the page already loads; empty
	// when the ticket is unassigned or the category is unknown.
	AssigneeName string
	CategoryName string
	// Changed is whether the row's ticket changed for the viewer since their
	// last visit (issue #415). False when nothing changed, and false on a
	// handler built without WithVisits.
	Changed bool
}

// AwaitingConfirmation reports whether the row is a resolved ticket waiting for
// the requester's decision (issue #411, S8). The requester list renders its two
// Needs-your-action requests differently — a held ticket asks for a reply, a
// resolved one asks whether the issue is solved — so the row answers that
// question instead of leaving the template to compare state strings.
func (r ticketRow) AwaitingConfirmation() bool { return r.State == domain.StateResolved }

// ticketListData is one independently paged ticket section.
type ticketListData struct {
	Tickets        []ticketRow
	Total          int
	Page           int
	Pages          int
	PrevHref       string
	NextHref       string
	HasActiveQuery bool
	// Now is the section's clock snapshot for its relative timestamps. It
	// travels WITH the section because a template invoked with the section as
	// its data cannot reach the page's own Now.
	Now time.Time
	// HasChanged reports that a ticket on this page changed since the viewer
	// last opened it (issue #415), so a folded section can say so unopened.
	HasChanged bool
}

func sectionData(res *application.SearchResult, f filterState, hrefForPage func(int) string) ticketListData {
	pages := (res.Total + application.PageSize - 1) / application.PageSize
	if pages < 1 {
		pages = 1
	}
	data := ticketListData{
		Total:          res.Total,
		Page:           res.Page,
		Pages:          pages,
		HasActiveQuery: f.Q != "",
	}
	if res.Page > 1 {
		data.PrevHref = hrefForPage(res.Page - 1)
	}
	if res.Page < pages {
		data.NextHref = hrefForPage(res.Page + 1)
	}
	return data
}

// agentOpenStates and agentDoneStates split the agent's personal queue by
// lifecycle (issue #410, S4): active work stays in "Assigned to me" and the
// agent's finished work (resolved, closed or cancelled) collapses into its own
// disclosure. The split lives in the query, so each section paginates over its
// own set and the counts never disagree with the rows below them.
var (
	agentOpenStates = []domain.State{domain.StateNew, domain.StateInProgress}
	agentDoneStates = domain.ClosedStates()
)

// agentQueueSections builds the THREE agent queue sections through the shared
// helper used by GET /tickets and list-claim responses: the open and done
// halves of the personal queue plus the claimable queue run as independent
// scoped searches, the union of their page ticket IDs (≤ 30 at PageSize 10) is
// batched through ONE SearchService.AgentQueueContext call AND ONE
// SLAService.ForTickets call (one clock snapshot for the whole page), and
// every ticket is wrapped with its row context and optional SLA row. A missing
// production capability or a store error propagates — rows are never
// fabricated.
func (h *TicketHandlers) agentQueueSections(r *http.Request, f filterState, assignedPage, donePage, claimablePage int) (assigned, done, claimable ticketListData, total int, err error) {
	actor := *userFromContext(r.Context())
	assignedQuery := f.query()
	assignedQuery.Section = application.TicketSectionPersonal
	assignedQuery.States = agentOpenStates
	assignedRes, err := h.search.Search(r.Context(), actor, assignedQuery, assignedPage)
	if err != nil {
		return assigned, done, claimable, 0, err
	}
	doneQuery := f.query()
	doneQuery.Section = application.TicketSectionPersonal
	doneQuery.States = agentDoneStates
	doneRes, err := h.search.Search(r.Context(), actor, doneQuery, donePage)
	if err != nil {
		return assigned, done, claimable, 0, err
	}
	claimableQuery := f.query()
	claimableQuery.Section = application.TicketSectionClaimable
	claimableRes, err := h.search.Search(r.Context(), actor, claimableQuery, claimablePage)
	if err != nil {
		return assigned, done, claimable, 0, err
	}
	union := make([]domain.Ticket, 0, len(assignedRes.Tickets)+len(doneRes.Tickets)+len(claimableRes.Tickets))
	union = append(union, assignedRes.Tickets...)
	union = append(union, doneRes.Tickets...)
	union = append(union, claimableRes.Tickets...)
	contexts, err := h.search.AgentQueueContext(r.Context(), union)
	if err != nil {
		return assigned, done, claimable, 0, err
	}
	// ONE SLA batch over the union of every section's page ids, instead of a
	// per-section or per-ticket read. An unset service leaves slas nil, so
	// every row's SLA row stays nil and no badge renders.
	var slas map[int64]domain.SLAProjection
	if h.sla != nil {
		slas, err = h.sla.ForTickets(r.Context(), ticketIDs(union))
		if err != nil {
			return assigned, done, claimable, 0, err
		}
	}
	// ONE visit batch over the SAME union (issue #415), so a ticket that sits
	// in both sections cannot disagree with itself.
	changed, err := h.changedTickets(r, union)
	if err != nil {
		return assigned, done, claimable, 0, err
	}
	assigned = sectionData(assignedRes, f, func(page int) string {
		return agentListHref(f, page, donePage, claimablePage)
	})
	assigned.Tickets = wrapTicketRows(assignedRes.Tickets, contexts, slas, changed)
	done = sectionData(doneRes, f, func(page int) string {
		return agentListHref(f, assignedPage, page, claimablePage)
	})
	done.Tickets = wrapTicketRows(doneRes.Tickets, contexts, slas, changed)
	done.HasChanged = anyChanged(done.Tickets)
	claimable = sectionData(claimableRes, f, func(page int) string {
		return agentListHref(f, assignedPage, donePage, page)
	})
	claimable.Tickets = wrapTicketRows(claimableRes.Tickets, contexts, slas, changed)
	// ONE clock snapshot for the whole page, handed to every section so the
	// three relative timestamps can never disagree with each other.
	var now time.Time
	if h.clock != nil {
		now = h.clock.Now()
	}
	assigned.Now, done.Now, claimable.Now = now, now, now
	return assigned, done, claimable, assignedRes.Total + doneRes.Total + claimableRes.Total, nil
}

// wrapTicketRows pairs one section's tickets with their batched contexts,
// optional SLA rows, and last-visit marks. The plain staff table reuses it with
// nil contexts, so both staff lists carry the SAME row shape. An unknown id
// degrades to the zero context, a nil SLA row and an unmarked row — never a
// fabricated row.
func wrapTicketRows(tickets []domain.Ticket, contexts map[int64]application.AgentTicketRowContext, slas map[int64]domain.SLAProjection, changed map[int64]bool) []ticketRow {
	rows := make([]ticketRow, len(tickets))
	for i, tk := range tickets {
		rows[i] = ticketRow{Ticket: tk, Context: contexts[tk.ID], SLA: slaRowFor(slas[tk.ID]), Changed: changed[tk.ID]}
	}
	return rows
}

// changedTickets answers ONE last-visit read over one page's tickets (issue
// #415): the batch is per page, never per row. An unset service answers no
// marks and reaches no store; a store error propagates exactly like the SLA
// read beside it, so a page is never rendered with silently missing marks.
func (h *TicketHandlers) changedTickets(r *http.Request, tickets []domain.Ticket) (map[int64]bool, error) {
	if h.visits == nil || len(tickets) == 0 {
		return nil, nil
	}
	return h.visits.Changed(r.Context(), *userFromContext(r.Context()), ticketIDs(tickets))
}

// anyChanged reports whether any row of a page carries the last-visit mark.
func anyChanged(rows []ticketRow) bool {
	for _, row := range rows {
		if row.Changed {
			return true
		}
	}
	return false
}

// ticketIDs extracts one page's ids for the batched SLA read.
func ticketIDs(tickets []domain.Ticket) []int64 {
	ids := make([]int64, len(tickets))
	for i, tk := range tickets {
		ids[i] = tk.ID
	}
	return ids
}

// listData is the tickets index payload (page + HX fragment share it).
type listData struct {
	pageData
	Filters             filterState
	Options             options
	Tickets             []ticketRow
	Total               int
	Page                int
	Pages               int
	PrevHref            string
	NextHref            string
	ShowAdvancedFilters bool
	HasActiveQuery      bool
	AgentView           bool
	Assigned            ticketListData
	Done                ticketListData
	Claimable           ticketListData
	ClaimError          string
	// Action, Open and Closed are the requester list's three sections (issue
	// #411, S3/S4/S8): the combined Needs-your-action set (the on-hold work
	// waiting for the requester's reply plus the resolved tickets awaiting their
	// confirmation), the active work, and the closed work folded at the end.
	// Each carries its own page cursor and its own count. Every other view
	// leaves them zero, and the requester's Total is the open work their header
	// counts.
	Action ticketListData
	Open   ticketListData
	Closed ticketListData
	// Tabs, Chips, ShowSLA and Now are the triage queue's presentation
	// (issue #382): the view tabs with their counts, the removable active
	// filters, whether the SLA column is drawn, and the one clock snapshot
	// the relative "Updated" reading is judged against (zero in fixtures).
	Tabs    []queueTab
	Chips   []queueChip
	ShowSLA bool
	Now     time.Time
	// ClearHref is the current view with every filter removed.
	ClearHref string
	// ExportHref downloads the current filter set as CSV (issue #409).
	ExportHref string
}

// queueTab is one view tab: its label key, href, count and whether it is the
// current one.
type queueTab struct {
	View     string
	LabelKey string
	Href     string
	Count    int
	Active   bool
}

// queueChip is one active filter the user can remove: a label and the href
// of the same query without it.
type queueChip struct {
	Label string
	Href  string
}

func (h *TicketHandlers) index(w http.ResponseWriter, r *http.Request) {
	redirect(w, r, "/tickets")
}

func simplifiedFilters(f filterState) filterState {
	f.State = ""
	f.Priority = ""
	f.CategoryID = ""
	f.UserID = ""
	return f
}

func pageNumber(r *http.Request, key string) int {
	page := int(parseID(r.URL.Query().Get(key)))
	if page < 1 {
		return 1
	}
	return page
}

// requesterListHref rebuilds /tickets carrying the filters and the requester's
// three INDEPENDENT page cursors (issue #411, S4). Each section's next and
// previous links keep the other two cursors where they were, so paging one
// section never resets another, and the search term survives every page.
func requesterListHref(f filterState, openPage, actionPage, closedPage int) string {
	v := ticketFilterValues(f)
	if openPage > 1 {
		v.Set("page", strconv.Itoa(openPage))
	}
	if actionPage > 1 {
		v.Set("action_page", strconv.Itoa(actionPage))
	}
	if closedPage > 1 {
		v.Set("closed_page", strconv.Itoa(closedPage))
	}
	if len(v) == 0 {
		return "/tickets"
	}
	return "/tickets?" + v.Encode()
}

// requesterSection reads ONE requester section through the same scoped search
// the rest of the list uses: the section (never the URL) decides its state set
// and its on-hold half, the text filter applies to all three alike, and its own
// cursor pages it.
func (h *TicketHandlers) requesterSection(r *http.Request, f filterState, actor domain.User, section application.TicketSection, page int, hrefForPage func(int) string) (ticketListData, error) {
	query := viewQuery(f, actor)
	query.Section = section
	res, err := h.search.Search(r.Context(), actor, query, page)
	if err != nil {
		return ticketListData{}, err
	}
	data := sectionData(res, f, hrefForPage)
	// The last-visit mark (issue #415): one batch per section, with the
	// requester's comment visibility, so an internal note never marks a row.
	changed, err := h.changedTickets(r, res.Tickets)
	if err != nil {
		return ticketListData{}, err
	}
	// SLA stays STAFF-ONLY (issue #211): the requester rows carry no SLA row,
	// and no SLA read is made for them.
	data.Tickets = wrapTicketRows(res.Tickets, nil, nil, changed)
	data.HasChanged = anyChanged(data.Tickets)
	return data, nil
}

// requesterListSections builds the requester's three independently paged
// sections (issue #411, S3/S4). The counts come from the same reads that
// produce the rows, so a section header can never disagree with its list.
func (h *TicketHandlers) requesterListSections(r *http.Request, f filterState, openPage, actionPage, closedPage int) (action, open, closed ticketListData, err error) {
	actor := *userFromContext(r.Context())
	action, err = h.requesterSection(r, f, actor, application.TicketSectionRequesterAction, actionPage, func(page int) string {
		return requesterListHref(f, openPage, page, closedPage)
	})
	if err != nil {
		return action, open, closed, err
	}
	open, err = h.requesterSection(r, f, actor, application.TicketSectionRequesterOpen, openPage, func(page int) string {
		return requesterListHref(f, page, actionPage, closedPage)
	})
	if err != nil {
		return action, open, closed, err
	}
	closed, err = h.requesterSection(r, f, actor, application.TicketSectionRequesterClosed, closedPage, func(page int) string {
		return requesterListHref(f, openPage, actionPage, page)
	})
	return action, open, closed, err
}

// requesterListData is the requester payload: three sections, the open-work
// count the header reads, and the one clock snapshot the relative "Updated"
// reading is judged against.
func (h *TicketHandlers) requesterListData(r *http.Request, f filterState, openPage, actionPage, closedPage int) (listData, error) {
	action, open, closed, err := h.requesterListSections(r, f, openPage, actionPage, closedPage)
	if err != nil {
		return listData{}, err
	}
	pageMeta := pageDataFrom(r, "tickets")
	pageMeta.PageFoundationAssets = true
	// The requester's rows use the same plain-click row navigation as the
	// staff queue (issue #411, S2).
	pageMeta.QueueAssets = true
	data := listData{
		pageData:       pageMeta,
		Filters:        f,
		Tickets:        open.Tickets,
		Total:          action.Total + open.Total,
		Page:           open.Page,
		Pages:          open.Pages,
		PrevHref:       open.PrevHref,
		NextHref:       open.NextHref,
		HasActiveQuery: f.Q != "",
		Action:         action,
		Open:           open,
		Closed:         closed,
	}
	if h.clock != nil {
		data.Now = h.clock.Now()
	}
	return data, nil
}

// listData builds the full list payload for the given filters and page.
// The search is scoped to the session actor (ticket-access spec): the
// list shows only tickets within the actor's scope.
func (h *TicketHandlers) listData(r *http.Request, f filterState, page int) (listData, error) {
	actor := *userFromContext(r.Context())
	// Apply the actor's stored default queue order only when the request
	// carries no explicit `sort` (an explicit parameter always wins) and only
	// on the staff queue surfaces: the requester's list is deliberately fixed
	// to its active-work view, so the preference never applies to it. A store
	// failure propagates like every other list read.
	if actor.Role != domain.RoleUser && h.preferences != nil && !f.SortExplicit {
		order, err := h.preferences.GetDefaultQueueOrder(r.Context(), actor)
		if err != nil {
			return listData{}, err
		}
		f.Sort = order
	}
	if actor.Role == domain.RoleAgent {
		f = simplifiedFilters(f)
		pageMeta := pageDataFrom(r, "tickets")
		pageMeta.PageFoundationAssets = true
		pageMeta.QueueAssets = true
		assigned, done, claimable, total, err := h.agentQueueSections(r, f, pageNumber(r, "assigned_page"), pageNumber(r, "done_page"), pageNumber(r, "claimable_page"))
		if err != nil {
			return listData{}, err
		}
		return listData{pageData: pageMeta, Filters: f, Total: total, HasActiveQuery: f.Q != "", AgentView: true, Assigned: assigned, Done: done, Claimable: claimable}, nil
	}
	if actor.Role == domain.RoleUser {
		f = simplifiedFilters(f)
		return h.requesterListData(r, f, page, pageNumber(r, "action_page"), pageNumber(r, "closed_page"))
	}

	opts, err := h.collectOptions(r)
	if err != nil {
		return listData{}, err
	}
	res, err := h.search.Search(r.Context(), actor, viewQuery(f, actor), page)
	if err != nil {
		return listData{}, err
	}
	// SLA is STAFF-ONLY. The requester list renders user_ticket_list.html,
	// which must stay SLA-blind, so a user actor deliberately never triggers
	// this read and every row's SLA row stays nil. For admin/root ONE
	// ForTickets call covers the page with one clock snapshot; a store error
	// propagates and no row is fabricated.
	var slas map[int64]domain.SLAProjection
	if actor.Role != domain.RoleUser && h.sla != nil {
		slas, err = h.sla.ForTickets(r.Context(), ticketIDs(res.Tickets))
		if err != nil {
			return listData{}, err
		}
	}
	// The last-visit mark (issue #415): ONE batch for the page, with the internal-comment visibility the
	// actor's role allows. A store error propagates like the SLA read above.
	changed, err := h.changedTickets(r, res.Tickets)
	if err != nil {
		return listData{}, err
	}
	pages := (res.Total + application.PageSize - 1) / application.PageSize
	if pages < 1 {
		pages = 1
	}
	pageMeta := pageDataFrom(r, "tickets")
	pageMeta.PageFoundationAssets = true
	staff := actor.Role != domain.RoleUser
	pageMeta.QueueAssets = staff
	data := listData{
		pageData:            pageMeta,
		Filters:             f,
		Options:             opts,
		Tickets:             nameTicketRows(wrapTicketRows(res.Tickets, nil, slas, changed), opts),
		Total:               res.Total,
		Page:                res.Page,
		Pages:               pages,
		ShowAdvancedFilters: staff,
		HasActiveQuery:      f.State != "" || f.Priority != "" || f.CategoryID != "" || f.UserID != "" || f.Q != "" || f.View != viewAll,
	}
	if res.Page > 1 {
		data.PrevHref = listHref(f, res.Page-1)
	}
	if res.Page < pages {
		data.NextHref = listHref(f, res.Page+1)
	}
	data.ClearHref = listHref(filterState{View: f.View, Sort: sortNewest}, 1)
	data.ExportHref = ticketExportHref(f)
	if staff {
		data.Chips = queueChips(r, f, opts)
		data.Tabs, err = h.queueTabs(r, actor, f)
		if err != nil {
			return listData{}, err
		}
		data.ShowSLA, err = h.showSLAColumn(r, data.Tickets)
		if err != nil {
			return listData{}, err
		}
		if h.clock != nil {
			data.Now = h.clock.Now()
		}
	}
	return data, nil
}

// viewQuery turns the filters into the store query, binding the "mine" view
// to the acting user: the only view that needs the actor rather than a
// column predicate.
func viewQuery(f filterState, actor domain.User) application.TicketQuery {
	q := f.query()
	if f.View == viewMine {
		id := actor.ID
		q.UserID = &id
	}
	return q
}

// nameTicketRows resolves each row's assignee and category names from the
// option lists the page already loaded (issue #382): no extra store read.
func nameTicketRows(rows []ticketRow, opts options) []ticketRow {
	users := make(map[int64]string, len(opts.Users))
	for _, u := range opts.Users {
		users[u.ID] = u.Name
	}
	categories := make(map[int64]string, len(opts.Categories))
	for _, c := range opts.Categories {
		categories[c.ID] = c.Name
	}
	for i := range rows {
		if rows[i].UserID != nil {
			rows[i].AssigneeName = users[*rows[i].UserID]
		}
		rows[i].CategoryName = categories[rows[i].CategoryID]
	}
	return rows
}

// queueTabs builds the view tabs with the count each one would list under
// the other active filters (issue #382, S1).
func (h *TicketHandlers) queueTabs(r *http.Request, actor domain.User, f filterState) ([]queueTab, error) {
	tabs := make([]queueTab, 0, len(queueViews))
	for _, v := range queueViews {
		tf := f
		tf.View = v
		count, err := h.search.Count(r.Context(), actor, viewQuery(tf, actor))
		if err != nil {
			return nil, err
		}
		tabs = append(tabs, queueTab{View: v, LabelKey: queueViewLabelKeys[v], Href: listHref(tf, 1), Count: count, Active: v == f.View})
	}
	return tabs, nil
}

// queueChips lists the active filters as removable chips (issue #382, S2).
// Each href is the same query without that one key; view and sort survive.
func queueChips(r *http.Request, f filterState, opts options) []queueChip {
	tr := i18n.New(requestLocale(r))
	var chips []queueChip
	add := func(label string, without func(filterState) filterState) {
		chips = append(chips, queueChip{Label: label, Href: listHref(without(f), 1)})
	}
	if f.Q != "" {
		add(tr.Tf("tickets.chip.search", f.Q), func(x filterState) filterState { x.Q = ""; return x })
	}
	if f.State != "" {
		add(labeledValue(tr, "state", f.State), func(x filterState) filterState { x.State = ""; return x })
	}
	if f.Priority != "" {
		add(labeledValue(tr, "priority", f.Priority), func(x filterState) filterState { x.Priority = ""; return x })
	}
	if f.CategoryID != "" {
		label := f.CategoryID
		for _, c := range opts.Categories {
			if strconv.FormatInt(c.ID, 10) == f.CategoryID {
				label = c.Name
			}
		}
		add(label, func(x filterState) filterState { x.CategoryID = ""; return x })
	}
	if f.UserID != "" {
		label := f.UserID
		for _, u := range opts.Users {
			if strconv.FormatInt(u.ID, 10) == f.UserID {
				label = u.Name
			}
		}
		add(label, func(x filterState) filterState { x.UserID = ""; return x })
	}
	return chips
}

// showSLAColumn decides whether the queue draws its SLA column (issue #382,
// S3): when the instance has SLA enabled, or when any row on the page already
// carries a frozen commitment, because disabling the feature never hides a
// commitment that exists.
func (h *TicketHandlers) showSLAColumn(r *http.Request, rows []ticketRow) (bool, error) {
	for _, row := range rows {
		if row.SLA != nil {
			return true, nil
		}
	}
	if h.sla == nil {
		return false, nil
	}
	return h.sla.Enabled(r.Context())
}

func (h *TicketHandlers) list(w http.ResponseWriter, r *http.Request) {
	f := parseFilters(r)
	data, err := h.listData(r, f, pageNumber(r, "page"))
	if err != nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	h.renderer.Render(w, r, "tickets_index", "tickets_screen", data, http.StatusOK)
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
	// CategoryOffers is the create form's OWN category list; the create template
	// reads this and never Options.Categories. It carries the same viability
	// facts the picker shows (Unavailable + Reason) so a native select can render
	// an unrunnable category as a disabled option instead of offering it.
	CategoryOffers []catalogCategoryEntry
	// CategoryGroups is the same offer set grouped one entry per
	// "Department › Desk" (issue #391, S3), which is the shape the picker's
	// <optgroup> headings read.
	CategoryGroups []catalogCategoryGroup
	// Selected is the category the request fixed when the catalog could resolve
	// it. Two-step mode renders it read-only with a Change link; single-page mode
	// only uses it to mark the option, so a handler built without the catalog
	// service still preselects from Values.CategoryID alone.
	Selected *domain.CatalogCategory
	// CategoryFixed turns the category into a fixed value with a Change link
	// instead of a picker. It is true only in two-step mode with a resolved
	// category: that is the form a catalog card opens.
	CategoryFixed bool
	// CatalogHref is the URL the two-step form's Change link returns to. It is
	// empty in single-page mode, where the category is editable in place and
	// there is nothing to go back to.
	CatalogHref string
}

type ticketFormValues struct {
	Title       string
	Description string
	CategoryID  string
	UserID      string
	Priority    domain.Priority
}

func (h *TicketHandlers) newForm(w http.ResponseWriter, r *http.Request) {
	// Issue #391, S1: the instance setting decides how the category is reached,
	// not what the form is. Two-step mode opens on the service catalog; every
	// other request — and every request in single-page mode — renders the shared
	// form, which preselects ?category_id so existing links keep working (S5).
	mode := ticketCreationModeFrom(r.Context())
	categoryID := parseID(r.URL.Query().Get("category_id"))
	if mode == application.TicketCreationTwoStep && categoryID == 0 && h.catalog != nil {
		h.renderCatalog(w, r)
		return
	}
	opts, offers, err := h.createOptions(r)
	if err != nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	page := pageDataFrom(r, "tickets")
	page.PageFoundationAssets = true
	page.CreateAssets = true
	data := ticketFormData{
		pageData:       page,
		Values:         ticketFormValues{Priority: domain.PriorityMedium},
		Options:        opts,
		CategoryOffers: offers,
		CategoryGroups: groupCatalogCategories(offers),
	}
	data.Values.CategoryID = r.URL.Query().Get("category_id")
	if id := parseID(data.Values.CategoryID); id != 0 {
		selected, ok := h.selectCategory(r, w, id)
		if !ok {
			return
		}
		data.Selected = selected
	}
	data.CategoryFixed = mode == application.TicketCreationTwoStep && data.Selected != nil
	if data.CategoryFixed {
		data.CatalogHref = "/tickets/new"
	}
	h.renderer.Render(w, r, "tickets_new", "ticket_form", data, http.StatusOK)
}

// selectCategory resolves a fixed category for the form. The catalog is the
// authority (it refuses an unpublished category, which answers 404 through the
// response writer), and a handler built without it answers (nil, true): the
// picker then marks the option from the posted value, so the same request still
// renders a usable form.
func (h *TicketHandlers) selectCategory(r *http.Request, w http.ResponseWriter, id int64) (*domain.CatalogCategory, bool) {
	if h.catalog == nil {
		return nil, true
	}
	selected, err := h.catalog.FindCategory(r.Context(), id, h.workflows)
	if err != nil {
		http.Error(w, mapErrorMsg(r, err), statusFor(err))
		return nil, false
	}
	return selected, true
}

// catalogCategoryEntry is one row of the requester's category picker.
//
// A category that is published but cannot move a ticket stays VISIBLE and
// explains itself instead of quietly disappearing. Hiding it would leave the
// column mute, and the picker's empty state ("No published categories in this
// desk yet.") is server-rendered only when nothing is published at all — so
// reusing it for a hidden unrunnable category would be a lie: the category IS
// published, it just cannot run. An unavailable row is deliberately not a link,
// so the picker never walks the requester into a form whose submit the create
// guard will refuse.
type catalogCategoryEntry struct {
	domain.CatalogCategory
	// Unavailable marks a published category that cannot move a ticket.
	Unavailable bool
	// Reason is why it cannot, in the requester's words. Empty when it can.
	// It is a catalogue message (issue #335): the presentation layer decides
	// the language, so the entry carries a key plus the analysis reason as a
	// value instead of composed English.
	Reason application.Msg
}

// requesterSummaryIndex indexes the requester-safe workflow summaries by
// category id, so a surface can look up a category's state without a second
// read or a second rule.
func requesterSummaryIndex(summaries []application.WorkflowSummary) map[int64]application.WorkflowSummary {
	index := make(map[int64]application.WorkflowSummary, len(summaries))
	for _, s := range summaries {
		index[s.CategoryID] = s
	}
	return index
}

// offerRunnableCategories is the ONE predicate that decides which categories a
// requester-facing surface offers and how each one is labelled. The category
// picker and the create form's fallback select both call it, so the two
// surfaces can never disagree about whether a category is choosable.
//
// It keeps exactly the categories the requester can act on. No published
// version leaves the category out, unchanged: that is a normal setup state,
// not an anomaly. Published but unable to run keeps it in, marked, so the
// surface can say so instead of walking the requester into the create guard's
// refusal.
func offerRunnableCategories(in []domain.CatalogCategory, summary map[int64]application.WorkflowSummary) []catalogCategoryEntry {
	out := make([]catalogCategoryEntry, 0, len(in))
	for _, c := range in {
		s, ok := summary[c.ID]
		if !ok || s.Version == 0 {
			continue
		}
		reason := application.Msg{}
		if !s.CannotRun.Empty() {
			reason = application.MsgRef("categories.badge.cannot_run", application.Nested(s.CannotRun))
		}
		out = append(out, catalogCategoryEntry{CatalogCategory: c, Unavailable: !s.CannotRun.Empty(), Reason: reason})
	}
	return out
}

// createCatalogCard is one category card of the two-step service catalog
// (issue #391, S4): the category's name, the desk that holds it as the caption,
// and — for a published category that cannot run — the reason it is disabled.
type createCatalogCard struct {
	catalogCategoryEntry
	// Hidden marks a card the server-side ?q search filtered out. It is
	// rendered with the hidden attribute so the no-script path shows only the
	// matches, while the browser applies exactly the same rule live.
	Hidden bool
}

// createCatalogSection is one department of the catalog with its cards.
type createCatalogSection struct {
	ID     int64
	Name   string
	Hidden bool
	Cards  []createCatalogCard
}

// catalogPageData is the two-step catalog payload.
type catalogPageData struct {
	pageData
	Query    string
	Sections []createCatalogSection
	// NoMatch reports a query no card matches, so the page states that instead
	// of rendering an empty catalog.
	NoMatch bool
}

// catalogMatches is the ONE search rule the server and the browser apply
// (issue #391, S4): a card matches when the query appears in the category, its
// desk, or its department name. It is case-insensitive and trims the query, so
// typing with a stray space never empties the catalog.
func catalogMatches(query, category, desk, department string) bool {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return true
	}
	for _, value := range []string{category, desk, department} {
		if strings.Contains(strings.ToLower(value), q) {
			return true
		}
	}
	return false
}

// catalogSections groups the choosable categories into one section per
// department, in catalog order (department, desk, category, as the projection
// answers them), marking the cards and sections a query filters out.
func catalogSections(offers []catalogCategoryEntry, query string) ([]createCatalogSection, bool) {
	sections := make([]createCatalogSection, 0, 4)
	index := make(map[int64]int, 4)
	matches := false
	for _, offer := range offers {
		i, ok := index[offer.DepartmentID]
		if !ok {
			index[offer.DepartmentID] = len(sections)
			sections = append(sections, createCatalogSection{ID: offer.DepartmentID, Name: offer.DepartmentName, Hidden: true})
			i = len(sections) - 1
		}
		hit := catalogMatches(query, offer.Name, offer.DeskName, offer.DepartmentName)
		section := &sections[i]
		section.Cards = append(section.Cards, createCatalogCard{catalogCategoryEntry: offer, Hidden: !hit})
		if hit {
			section.Hidden = false
			matches = true
		}
	}
	return sections, matches
}

// renderCatalog renders the two-step service catalog (issue #391, S4): a search
// box, then one section per department holding category cards. A card links to
// the shared form with its category, and a published category that cannot run
// stays visible, disabled, with its reason — the same rule the picker applies.
func (h *TicketHandlers) renderCatalog(w http.ResponseWriter, r *http.Request) {
	offers, err := h.choosableCategories(r, nil)
	if err != nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	sections, matches := catalogSections(offers, query)
	page := pageDataFrom(r, "tickets")
	// The catalog is a creation surface: it carries the same page foundation
	// (and therefore the same stylesheet) the form does, because the cards and
	// the search are built from the shared control and shell rungs.
	page.PageFoundationAssets = true
	page.CreateAssets = true
	data := catalogPageData{
		pageData: page,
		Query:    query,
		Sections: sections,
		NoMatch:  query != "" && !matches,
	}
	h.renderer.Render(w, r, "tickets_catalog", "ticket_catalog", data, http.StatusOK)
}

// catalogCategories lists every category the catalog knows, with the desk and
// department that hold it, in catalog order. It reads the same projection the
// search does, because the picker and the catalog need the names that make
// their groups, not just the ids.
func (h *TicketHandlers) catalogCategories(r *http.Request, fallback []domain.Category) ([]domain.CatalogCategory, error) {
	if h.catalog != nil {
		return h.catalog.ListAllCategories(r.Context())
	}
	return asCatalogCategories(fallback), nil
}

// choosableCategories is the ONE requester-facing offer set every create surface
// reads (issue #391, S3/S4/S5): the catalog projection, filtered through the same
// viability predicate the picker has always used (offerRunnableCategories over
// ListRequesterSummaries). Unpublished categories are absent, a runnable one is
// offered, and a published one that cannot run is offered as a disabled choice
// with its reason. fallback carries the plain category rows a handler built
// without the catalog service can still offer.
func (h *TicketHandlers) choosableCategories(r *http.Request, fallback []domain.Category) ([]catalogCategoryEntry, error) {
	categories, err := h.catalogCategories(r, fallback)
	if err != nil {
		return nil, err
	}
	summaries, err := h.workflows.ListRequesterSummaries(r.Context())
	if err != nil {
		return nil, err
	}
	return offerRunnableCategories(categories, requesterSummaryIndex(summaries)), nil
}

// catalogCategoryGroup is one optgroup of the create form's category picker
// (issue #391, S3): the "Department › Desk" its categories belong to.
type catalogCategoryGroup struct {
	Label   string
	Options []catalogCategoryEntry
}

// groupCatalogCategories groups the offers by the desk that holds them, keeping
// the catalog's order so the picker reads like the service catalog. A category
// with no desk name (a handler built without the catalog service) lands in one
// unlabelled group, which the picker renders as a plain list.
func groupCatalogCategories(offers []catalogCategoryEntry) []catalogCategoryGroup {
	groups := make([]catalogCategoryGroup, 0, len(offers))
	index := make(map[string]int, len(offers))
	for _, offer := range offers {
		label := offer.DepartmentName
		if offer.DeskName != "" {
			if label != "" {
				label += " › "
			}
			label += offer.DeskName
		}
		i, ok := index[label]
		if !ok {
			index[label] = len(groups)
			groups = append(groups, catalogCategoryGroup{Label: label})
			i = len(groups) - 1
		}
		groups[i].Options = append(groups[i].Options, offer)
	}
	return groups
}

// createOptions builds the create-form option lists. The category list comes
// from the SAME requester-safe predicate the catalog renders
// (choosableCategories), never a second rule: the picker, the catalog and the
// create guard can never disagree about which categories are choosable.
// Unpublished/draft-only categories are absent for every role; legacy and
// historical tickets remain filterable on the list screen because listData
// keeps using collectOptions (all categories). The POST path repeats the
// availability check and still answers the exact 422 category message.
func (h *TicketHandlers) createOptions(r *http.Request) (options, []catalogCategoryEntry, error) {
	opts, err := h.collectOptions(r)
	if err != nil {
		return options{}, nil, err
	}
	offers, err := h.choosableCategories(r, opts.Categories)
	if err != nil {
		return options{}, nil, err
	}
	// The create page reads the offers, never Options.Categories: the filter
	// form on the list screen still carries that list.
	opts.Categories = nil
	return opts, offers, nil
}

// asCatalogCategories lifts the plain category rows the option lists carry into
// the richer catalog shape offerRunnableCategories consumes, with the catalog
// fields left empty because this surface only needs id, name, and description.
func asCatalogCategories(in []domain.Category) []domain.CatalogCategory {
	out := make([]domain.CatalogCategory, 0, len(in))
	for _, c := range in {
		out = append(out, domain.CatalogCategory{Category: c})
	}
	return out
}

// create parses the create form, calls the use case, and answers per D6:
// HX-Request → refreshed ticket_list fragment; full request →
// 303 to the detail page. Errors re-render the form with the mapped status
// (422/409/...) and message.
func (h *TicketHandlers) create(w http.ResponseWriter, r *http.Request) {
	uploads, err := parseTicketForm(r)
	if err != nil {
		renderUploadError(w, r, err)
		return
	}
	// The file parts may have spilled to temp files; every response path below
	// (created, refused, re-rendered) releases them.
	defer releaseMultipartForm(r)
	// Amendment 2: creation binds NO assignee for ANY role. The mere PRESENCE
	// of a user_id or assignee_id parameter — any value, including empty,
	// which is exactly what a stale cached form submits — is rejected with
	// the typed assignee validation error BEFORE any binding/validation and
	// with ZERO writes (no ticket, pin, run, or audit results).
	if r.Form.Has("user_id") || r.Form.Has("assignee_id") {
		h.renderCreateError(w, r, "assignee", i18n.T(requestLocale(r), "error.create_unassigned_only"), http.StatusUnprocessableEntity)
		return
	}
	actor := *userFromContext(r.Context())

	categoryID := parseID(r.Form.Get("category_id"))
	if categoryID == 0 {
		h.renderCreateError(w, r, "category", i18n.T(requestLocale(r), "error.category_required"), http.StatusUnprocessableEntity)
		return
	}

	in := application.CreateTicketInput{
		Title:       r.Form.Get("title"),
		Description: r.Form.Get("description"),
		CategoryID:  categoryID,
		Priority:    domain.Priority(r.Form.Get("priority")),
		// The files of the creation (issue #366) are validated with the rest
		// of the submission and written in the ticket's own transaction.
		Attachments: uploads,
	}

	_, err = h.tickets.Create(r.Context(), actor, in)
	if err != nil {
		status, msg := mapErrorFor(r, err)
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
			setSaveFeedbackCookie(w, r, saveFeedbackData{Message: saveFeedbackSaved, Kind: saveFeedbackSuccess})
			w.Header().Set("HX-Redirect", "/tickets")
			w.WriteHeader(http.StatusOK)
			return
		}
		saveFeedback(w, r, saveFeedbackSaved, saveFeedbackSuccess)
		h.renderer.Render(w, r, "tickets_index", "ticket_list", data, http.StatusOK)
		return
	}

	saveFeedback(w, r, saveFeedbackSaved, saveFeedbackSuccess)
	// The detail route lands in the next slice commit; a committed ticket
	// must never leave the user on an unregistered 404 path.
	redirect(w, r, "/tickets")
}

// renderCreateError re-renders the create form with an inline error and the
// mapped status (HX → ticket_form fragment; full → tickets_new page).
func (h *TicketHandlers) renderCreateError(w http.ResponseWriter, r *http.Request, field, msg string, status int) {
	opts, offers, err := h.createOptions(r)
	if err != nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	page := pageDataFrom(r, "tickets")
	page.PageFoundationAssets = true
	page.CreateAssets = true
	data := ticketFormData{
		pageData: page,
		Error:    msg,
		Values: ticketFormValues{
			Title:       r.Form.Get("title"),
			Description: r.Form.Get("description"),
			CategoryID:  r.Form.Get("category_id"),
			Priority:    domain.Priority(r.Form.Get("priority")),
		},
		Options:        opts,
		CategoryOffers: offers,
		CategoryGroups: groupCatalogCategories(offers),
	}
	// Issue #391, S6: the re-render keeps every submitted value INCLUDING the
	// category, and answers in the SAME mode the form was served in — a refused
	// submission must never move the requester to the other flow.
	mode := ticketCreationModeFrom(r.Context())
	if id := parseID(data.Values.CategoryID); id != 0 {
		selected, ok := h.selectCategory(r, w, id)
		if !ok {
			return
		}
		data.Selected = selected
	}
	data.CategoryFixed = mode == application.TicketCreationTwoStep && data.Selected != nil
	if data.CategoryFixed {
		data.CatalogHref = "/tickets/new"
	}
	_ = field // the inline banner is a single generic error block
	h.renderer.Render(w, r, "tickets_new", "ticket_form", data, status)
}

// --- Task 5.5: detail, edit, transition, comments -------------------------

// transitionTarget is one allowed next state offered by the header (issue
// #386, S1). The table mirrors the domain transition map (design "Domain
// Model"); the domain remains the single enforcement point — this list is
// presentation.
type transitionTarget struct {
	To          domain.State
	NeedsReason bool // closed -> in_progress requires a reopen reason
	// LabelKey is the catalogue verb the header renders: the move reads as an
	// action (Start, Resolve, Close, Reopen, Cancel), never as a state name.
	LabelKey string
	// Weight is the button's place in the header's hierarchy (S1): the forward
	// move is primary, Cancel is the quiet danger button, the rest secondary.
	Weight string
}

// The catalogue verbs a header transition renders (issue #386, S1). They are
// held as literals so the i18n coverage scan sees every key the view model
// hands to the template.
const (
	transitionLabelStart   = "tickets.detail.transition.start"
	transitionLabelResolve = "tickets.detail.transition.resolve"
	transitionLabelClose   = "tickets.detail.transition.close"
	transitionLabelReopen  = "tickets.detail.transition.reopen"
	transitionLabelCancel  = "tickets.detail.transition.cancel"
)

// transitionVerb names the action a move reads as, from the state it leaves and
// the state it reaches (S1: in_progress from new is "Start", in_progress from
// resolved or closed is "Reopen").
func transitionVerb(from, to domain.State) string {
	switch to {
	case domain.StateInProgress:
		if from == domain.StateNew {
			return transitionLabelStart
		}
		return transitionLabelReopen
	case domain.StateResolved:
		return transitionLabelResolve
	case domain.StateClosed:
		return transitionLabelClose
	default: // cancelled
		return transitionLabelCancel
	}
}

// weighTransitions assigns each target its button weight: the first move that is
// not Cancel is the primary action, Cancel is the quiet danger button, and
// everything else is secondary. It runs on the RENDERED list, so a filtered
// ticket (a requester-owned resolved one keeps only the reopen) still has one
// primary action instead of a set of equal buttons.
func weighTransitions(next []transitionTarget) []transitionTarget {
	primary := false
	for i := range next {
		switch {
		case next[i].To == domain.StateCancelled:
			next[i].Weight = "danger"
		case !primary:
			next[i].Weight, primary = "primary", true
		default:
			next[i].Weight = "secondary"
		}
	}
	return next
}

// allowedNext returns the presentation list of legal next states (design
// transition table).
func allowedNext(s domain.State) []transitionTarget {
	return weighTransitions(rawNext(s))
}

// rawNext is the state machine's presentation mirror before the header weights
// are assigned, so the labels can name the verb the move reads as.
func rawNext(s domain.State) []transitionTarget {
	target := func(to domain.State, needsReason bool) transitionTarget {
		return transitionTarget{To: to, NeedsReason: needsReason, LabelKey: transitionVerb(s, to)}
	}
	switch s {
	case domain.StateNew:
		return []transitionTarget{target(domain.StateInProgress, false), target(domain.StateResolved, false), target(domain.StateCancelled, false)}
	case domain.StateInProgress:
		return []transitionTarget{target(domain.StateResolved, false), target(domain.StateCancelled, false)}
	case domain.StateResolved:
		return []transitionTarget{target(domain.StateClosed, false), target(domain.StateInProgress, true)}
	case domain.StateClosed:
		return []transitionTarget{target(domain.StateInProgress, true)}
	default:
		return nil // cancelled is terminal
	}
}

// isRequester is the presentation mirror of the application's unexported
// isTicketRequester (identity check, no role bypass): the actor is the
// persisted ticket's requester. The application layer remains the enforcement
// point; this only drives detail-page flags and Move-to filtering.
func isRequester(actor domain.User, t *domain.Ticket) bool {
	return t.RequesterUserID != nil && *t.RequesterUserID == actor.ID
}

// filteredNext adapts the pure allowedNext list to the requester-confirmation
// presentation rules (D7, call-site filter — allowedNext itself stays pure):
// on a requester-owned resolved ticket the closed target is dropped for every
// actor (closure is exclusively the requester's confirmation control, and a
// role user sees no Move-to at all), while a requester-NULL resolved ticket
// keeps closed + reopen for authorized agents. All other states pass through
// unchanged. The service remains the enforcement point.
func filteredNext(next []transitionTarget, t *domain.Ticket) []transitionTarget {
	if t.State != domain.StateResolved || t.RequesterUserID == nil {
		return next
	}
	filtered := next[:0:0]
	for _, tgt := range next {
		if tgt.To == domain.StateClosed {
			continue
		}
		filtered = append(filtered, tgt)
	}
	// The weights follow the RENDERED list: the surviving reopen becomes the
	// primary action instead of leaving the header with no primary at all.
	return weighTransitions(filtered)
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
	// error re-render (e.g. a blank reopen reason, 422) keeps the reason dialog
	// armed for the same move instead of resetting the header.
	SelectedTo string
	// Now is the render's clock snapshot (issue #386, S3). The header and the
	// timeline phrase their instants relatively against it; a zero snapshot
	// (every golden fixture) renders the absolute instant so fixtures stay
	// frozen.
	Now time.Time
	// CanAssignToMe reports whether the header offers the "Assign to me"
	// shortcut through the ordinary assign route: staff holding CapAssignTicket
	// on an open, unassigned ticket (S1). The workflow claim keeps its own route
	// and wins when it applies, so one ticket never shows two shortcuts.
	CanAssignToMe bool
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
	// CanAssign reports whether the actor may (re)assign this ticket
	// (CapAssignTicket) on an open ticket. Presentation only; the assign use
	// case enforces the actor/ticket authorization.
	CanAssign bool
	// CanTransition reports whether the actor may transition this ticket's
	// state at all (CapEditTicket, which covers editing AND transitioning).
	// Unlike CanEdit it is deliberately NOT gated on the closed state: on a
	// closed ticket the only remaining mutation is the reopen transition,
	// which stays available to every actor holding the capability
	// (closed-ticket read-only spec). Presentation only; the transition use
	// case enforces the actor/ticket authorization.
	CanTransition bool
	// Closed reports whether the ticket is in a closed (read-only) state —
	// resolved, closed, or cancelled. A closed ticket hides the inline edit,
	// the properties/assignment controls, and the comment form; only the
	// State control (with its reopen transition) remains (closed-ticket
	// read-only spec). The server-side use cases also reject mutations on
	// closed tickets regardless of what the UI shows.
	Closed bool
	// CanConfirm reports whether the actor is the authenticated requester of
	// a resolved ticket (presentation mirror of the service's
	// isTicketRequester gate, requester-confirmation delta D7). It drives the
	// confirm/reject control, rendered ONLY for that requester; the
	// server-side ConfirmResolution/RejectResolution use cases enforce the
	// gate regardless of what the UI shows.
	CanConfirm bool
	// CanComment reports whether the comment form renders: open states for
	// everyone, plus the resolved carve-out for the ticket's requester only
	// (comment-timeline delta). Presentation only — the comment use case
	// rejects non-requesters on resolved tickets server-side.
	CanComment bool
	// Pending carries the run's step checklist (issue #245): it is populated
	// only for an active run and never exposes a workflow version, pin, or
	// technical cursor.
	Pending workflowChecklist
	Claim   workflowClaim
	// SLA is the pre-formatted milestones panel (issue #211), present ONLY for
	// a non-`user` actor on a ticket with a frozen commitment. A nil panel
	// hides the section entirely: a requester never receives the projection,
	// and a ticket with no frozen SLA has none to show.
	SLA *slaPanelView
	// RequesterView marks the detail as rendered for the requester audience
	// (role `user`, issue #334): the state badge and the back link use the
	// requester's own vocabulary instead of the support team's. Presentation
	// only; the use cases enforce every reachable behavior.
	RequesterView bool
	// BackHref is the validated "← Queue" target (issue #265): the same-origin
	// list URL the browser came from, reduced to whitelisted list parameters,
	// or /tickets when the navigation carried no usable list origin.
	BackHref string
	// HoldReason carries the pause reason a REJECTED hold request submitted, so
	// the re-rendered form keeps what the agent typed instead of blanking it
	// (issue #352). It is empty on every successful read: the stored reason is
	// read from the ticket itself.
	HoldReason string
	// HoldFailed marks a detail re-rendered for a refused hold request, so the
	// hold dialog reopens with the error inside it instead of hiding the
	// message behind the modal (issue #352).
	HoldFailed bool
	// Attachments are the files attached at ticket creation (issue #366),
	// listed under the description. Each comment's files travel on its own
	// Timeline entry, so both lists are already filtered by the visibility the
	// actor may read: a requester never receives a staff-only file.
	Attachments []domain.Attachment
	// SavedReplies is the composer's saved-reply picker (issue #418): the actor's
	// own replies and the shared replies of the desks they belong to, with the
	// placeholders already filled for THIS ticket. It is nil for a requester and
	// for a ticket with no composer, so the picker renders exactly where a staff
	// member can insert a reply, and no reply of a desk they do not belong to is
	// ever part of it.
	SavedReplies *application.CannedReplyPickerView
	// Follow is the header's follow control (issue #418): present for a staff
	// member who can already open the ticket, and nil for everybody else, so a
	// requester and an out-of-scope agent receive no control at all.
	Follow *detailFollow
	// Followers is the Properties row: everybody following the ticket, marked so
	// the reader recognises themselves. It is nil where the controls are nil, and
	// its People are empty when nobody follows yet.
	Followers *detailFollowers
	// Mentions is the composer's candidate list (issue #418): the staff this
	// ticket's comment may name. It is nil for a requester and for a ticket with
	// no composer, and both mention inputs render it or nothing.
	Mentions *mentionPickerView
}

// QueueHref is the detail's "← Queue" target: the validated list return href
// when the navigation carried one, else the plain queue. The fallback lives
// here so a detailData built without a BackHref (the golden fixtures, an
// out-of-band render) still renders the historical /tickets instead of an
// empty href.
func (d detailData) QueueHref() string {
	if d.BackHref == "" {
		return "/tickets"
	}
	return d.BackHref
}

const ticketQueueReturnHeader = "X-TKT-Queue-Return"

// TimelineOldestFirst is the detail's reading order (issue #386, S3): the
// application stream stays newest-first (its documented contract), and the
// detail reverses it so the feed reads top to bottom into the composer. It
// returns a copy, so the reversal never mutates the view the caller shares with
// the rest of the request.
func (d detailData) TimelineOldestFirst() []application.TimelineItem {
	source := d.View.Timeline
	oldest := make([]application.TimelineItem, len(source))
	for i, item := range source {
		oldest[len(source)-1-i] = item
	}
	return oldest
}

// QueueHeaders carries the validated origin through every whole-detail swap.
// JSON encoding and the template's attribute escaping are separate layers;
// neither arbitrary query text nor a raw request header becomes trusted HTML.
func (d detailData) QueueHeaders() (string, error) {
	headers, err := json.Marshal(map[string]string{ticketQueueReturnHeader: d.QueueHref()})
	return string(headers), err
}

// workflowClaim is the sidebar claim projection for the current pinned step.
type workflowClaim struct {
	Active   bool
	Position int
	DeskName string
	CanAct   bool
}

// workflowChecklist is the presentation payload for a run's progress inside the
// Timeline (issue #245): the honest "N of M done" count, a thin neutral progress
// bar, and one row per pinned step. Active is false (no checklist rendered) for
// legacy unpinned tickets and completed or absent runs. It exposes no workflow
// version, pin, or technical cursor.
type workflowChecklist struct {
	Active  bool
	Done    int
	Total   int
	Percent int
	CanAct  bool
	Rows    []workflowChecklistRow
}

// workflowChecklistRow is one step row. State is done | current | next and
// Glyph is its marker. A done row carries the completion Actor (empty for the
// automatic sentinel, which never renders) and At. A current row carries its
// control facts (Kind, Fields, CanAct) plus an optional Blocked hint. A next
// row carries only its honest Meta label.
type workflowChecklistRow struct {
	Position  int
	Name      string
	State     string
	Glyph     string
	Actor     string
	At        time.Time
	Completed bool
	Meta      string
	Kind      string
	Fields    []domain.FormField
	CanAct    bool
	Blocked   string
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
	// The visit mark is taken BEFORE the authorized read (issue #415): any
	// event or comment written while the ticket is being read is past the mark
	// and therefore stays new, instead of being swallowed by this visit. An
	// unset service takes no mark and records nothing.
	var mark application.VisitMark
	if h.visits != nil {
		seen, markErr := h.visits.Mark(r.Context(), id)
		if markErr != nil {
			return detailData{}, http.StatusInternalServerError, markErr
		}
		mark = seen
	}
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
	pending := h.checklistFor(r, id, actor, view.Ticket, view.AuditEvents, view.AssignedUser)
	// Requester-confirmation presentation flags (D7): identity mirror + the
	// resolved carve-out on the comment form; the services stay the
	// enforcement points for both.
	requester := isRequester(actor, view.Ticket)
	// The actor's internal-comment capability is the one visibility fact the
	// detail needs twice: the comment form offers the internal option with it,
	// and the attachment list is filtered by it (issue #366).
	canReadInternal := application.NewPolicy().Capabilities(actor.Role).Require(application.CapCommentInternal)
	// Move-to filtering at the call site: on a requester-owned resolved ticket
	// the closed target is dropped for every actor (requester uses the
	// confirmation control; agents keep only the reopen). Requester-NULL and
	// all other states keep the unfiltered allowedNext list.
	next := filteredNext(allowedNext(view.Ticket.State), view.Ticket)
	// SLA milestones panel (issue #211): STAFF ONLY. A `user` actor must not
	// trigger the read at all, so the guard short-circuits BEFORE
	// h.sla.ForTicket — a requester's detail page never touches the SLA store
	// and the projection is never computed for them. An unset service (WithSLA
	// never called) leaves the panel nil, exactly like the lists. A service
	// error propagates through the same status mapping as every other detail
	// read; nothing is fabricated.
	var slaPanel *slaPanelView
	if h.sla != nil && actor.Role != domain.RoleUser {
		projection, slaErr := h.sla.ForTicket(r.Context(), id)
		if slaErr != nil {
			return detailData{}, statusFor(slaErr), slaErr
		}
		slaPanel = slaPanelFor(requestLocale(r), projection)
	}
	page := pageDataFrom(r, "tickets")
	// The countdown script belongs to the detail page and only when there is a
	// panel to tick: a ticket with no frozen commitment (or a requester, whose
	// panel stays nil) must not pay for a script it never uses.
	page.SLACountdownAssets = slaPanel != nil
	// The detail's own behaviour (title guard, pickers, hold and reason dialogs,
	// and the phone fold of the properties panel, issue #386) ships as one
	// static file loaded by this page only.
	page.TicketDetailAssets = true
	// The render's clock snapshot: the header and the timeline read relative
	// instants against it. An unset clock leaves it zero, which renders the
	// absolute instant (the queue's rule, issue #382).
	var now time.Time
	if h.clock != nil {
		now = h.clock.Now()
	}
	// The attachment listing reads with the SAME internal-comment visibility the
	// detail view already applied above (issue #366, S3): a requester gets the
	// creation files and the public comments' files, never a staff-only one.
	creationFiles, commentFiles, err := h.ticketAttachments(r, id, canReadInternal)
	if err != nil {
		return detailData{}, statusFor(err), err
	}
	// Stamp each comment's files onto its timeline entry: the listing travels
	// with the comment it belongs to, so the timeline fragment renders it
	// without a second lookup.
	for i := range view.Timeline {
		entry := &view.Timeline[i]
		if entry.IsComment && entry.Comment != nil {
			entry.Attachments = commentFiles[entry.Comment.ID]
		}
	}
	// The composer renders on an open ticket, plus the resolved carve-out for
	// its own requester; the saved-reply picker follows the composer, because a
	// reply can only be inserted where a comment can be written.
	canComment := !closed || (view.Ticket.State == domain.StateResolved && requester)
	// The composer's saved replies (issue #418): STAFF ONLY, and only when the
	// service is wired. A requester never triggers the read at all, so their
	// detail page never touches the saved-reply store and the picker cannot
	// leak. The picker is the service's answer for THIS ticket: the actor's own
	// replies and the desks they belong to, placeholders already filled.
	var savedReplies *application.CannedReplyPickerView
	if h.savedReplies != nil && canReadInternal && canComment {
		picker, pickerErr := h.savedReplies.ForTicket(r.Context(), actor, view.Ticket)
		if pickerErr != nil {
			return detailData{}, statusFor(pickerErr), pickerErr
		}
		savedReplies = picker
	}
	// Following (issue #418): STAFF ONLY. The follow control and the followers
	// row come from the same read the two routes apply, so an agent the ticket is
	// out of reach for receives neither and learns nothing from the page.
	var follow *detailFollow
	var followers *detailFollowers
	if canReadInternal {
		follow, followers, err = h.followStateFor(r, actor, id)
		if err != nil {
			return detailData{}, statusFor(err), err
		}
	}
	// The composer's mention list follows the composer: it is the staff this
	// ticket's comment may name, so a requester — who can write a comment but is
	// never mentioned — and a closed ticket without a composer get none.
	var mentions *mentionPickerView
	if canReadInternal && canComment {
		mentions, err = h.mentionListOf(r, actor, id)
		if err != nil {
			return detailData{}, statusFor(err), err
		}
	}
	queueOrigin := r.Referer()
	// HTMX mutations refer to the detail URL, not the original queue. The
	// inherited header survives whole-detail swaps, including error renders.
	// It is still client input: validate it through the same whitelist as the
	// initial Referer, and rebuild the URL rather than echoing the candidate.
	if r.Header.Get("HX-Request") != "" {
		if explicit := r.Header.Get(ticketQueueReturnHeader); explicit != "" {
			queueOrigin = explicit
		}
	}
	// Only a page that can be shown is a visit, so the visit is recorded
	// after every fallible step above: a failure leaves the mark and the bell
	// as they were. The record also reads the actor's notifications about
	// this ticket.
	if h.visits != nil {
		if err := h.visits.RecordVisit(r.Context(), actor, id, mark); err != nil {
			return detailData{}, http.StatusInternalServerError, err
		}
	}
	return detailData{
		pageData:           page,
		View:               view,
		Next:               next,
		Options:            opts,
		Values:             values,
		Now:                now,
		CanCommentInternal: canReadInternal,
		CanEdit:            application.NewPolicy().Capabilities(actor.Role).Require(application.CapEditTicket) && !closed,
		CanAssign:          application.NewPolicy().Capabilities(actor.Role).Require(application.CapAssignTicket) && !closed,
		// The header shortcut needs an open, unassigned ticket: a ticket that
		// already has an assignee changes hands through the picker, which is the
		// same rule the assign use case enforces for a reassignment reason.
		CanAssignToMe: application.NewPolicy().Capabilities(actor.Role).Require(application.CapAssignTicket) && !closed && view.Ticket.UserID == nil,
		CanTransition: application.NewPolicy().Capabilities(actor.Role).Require(application.CapEditTicket),
		Closed:        closed,
		CanConfirm:    view.Ticket.State == domain.StateResolved && requester,
		CanComment:    canComment,
		Pending:       pending,
		Claim:         h.claimFor(r, id, actor),
		SLA:           slaPanel,
		RequesterView: actor.Role == domain.RoleUser,
		BackHref:      listReturnHref(queueOrigin, r.Host),
		Attachments:   creationFiles,
		SavedReplies:  savedReplies,
		Follow:        follow,
		Followers:     followers,
		Mentions:      mentions,
	}, 0, nil
}

// checklistFor builds the run's step checklist for an active run (issue #245):
// one row per pinned step, reader-facing names, the completed rows attributed
// from the append-only audit trail by the sealed step index, the current row
// with its control, and the pending rows labelled honestly. It returns
// Active=false (no checklist) for legacy unpinned tickets and
// non-active/completed runs, so the Timeline omits it exactly as before. The
// persisted actor predicate decides CanAct: manual_task/form[assignee] require
// the current assignee, form[requester] the ticket requester, and a claim is
// server-enforced on submit (membership is rechecked by the unit of work).
// Automatic steps (least_loaded and the resolve/close terminal steps) render
// with no control — they advance synchronously with the plan.
func (h *TicketHandlers) checklistFor(r *http.Request, id int64, actor domain.User, t *domain.Ticket, events []domain.AuditEvent, assignee *domain.User) workflowChecklist {
	snap, err := h.workflowRuns.GetWorkflowExecution(r.Context(), id)
	if err != nil || snap == nil || snap.Run == nil || snap.Run.Status != "active" {
		return workflowChecklist{}
	}
	cur := snap.Run.CurrentStepIndex
	if cur < 0 || cur >= len(snap.Workflow) {
		return workflowChecklist{}
	}
	tr := i18n.New(requestLocale(r))
	completions := completionAuditsByStep(snap.Workflow, events)
	deskNames := map[int64]string{}
	rows := make([]workflowChecklistRow, 0, len(snap.Workflow))
	for i, step := range snap.Workflow {
		row := workflowChecklistRow{Position: i + 1}
		desk := ""
		if step.Type == domain.StepAssignToDesk {
			desk = h.deskLabel(r, step, deskNames)
		}
		row.Name = checklistStepName(tr, step, desk)
		switch {
		case i < cur:
			row.State, row.Glyph = "done", "✓"
			if ev, ok := completions[i]; ok {
				row.Completed = true
				row.At = ev.CreatedAt
				// The automatic actor sentinel is never rendered as a person: the
				// row says plainly that the step completed on its own.
				if ev.Actor == "" || ev.Actor == "workflow" {
					row.Actor = tr.T("tickets.checklist.meta.completed_automatically")
				} else {
					row.Actor = ev.Actor
				}
			}
		case i == cur:
			row.State, row.Glyph = "current", "●"
			h.fillChecklistCurrent(tr, &row, step, desk, actor, t, assignee)
		default:
			row.State, row.Glyph = "next", "○"
			row.Meta = checklistPendingMeta(tr, step)
		}
		rows = append(rows, row)
	}
	total := len(snap.Workflow)
	percent := 0
	if total > 0 {
		percent = (100*cur + total/2) / total
	}
	return workflowChecklist{
		Active:  true,
		Done:    cur,
		Total:   total,
		Percent: percent,
		CanAct:  rows[cur].CanAct,
		Rows:    rows,
	}
}

// completionAuditsByStep indexes the audit event that COMPLETES each step,
// correlated strictly by the sealed step index AND the step's own completing
// action (application.StepCompletionAction). Occurrence order is not the
// correlation key (domain/audit.go): a contextual workflow_assignment completes
// only an assign_to_desk step, a requester/assignee form action only its
// matching form step, and a manual-task action only a manual step. An event
// that merely bears the index with the wrong action for that step type is never
// the completion.
//
// Tie rule: when one step index carries more than one completing event (a
// resumed or retried run, or a future non-linear execution model), the EARLIEST
// completion wins. The trail is already ordered created_at ASC, id ASC
// (audit_store.go ListByTicket), so the first matching event is the earliest;
// the explicit CreatedAt guard keeps that true even if a caller ever hands the
// events unordered, and a same-timestamp tie keeps the first seen.
func completionAuditsByStep(steps []domain.WorkflowStep, events []domain.AuditEvent) map[int]domain.AuditEvent {
	out := map[int]domain.AuditEvent{}
	for _, ev := range events {
		if ev.StepIndex == nil {
			continue
		}
		idx := *ev.StepIndex
		if idx < 0 || idx >= len(steps) {
			continue
		}
		if application.StepCompletionAction(steps[idx]) != ev.Action {
			continue
		}
		if prev, seen := out[idx]; seen && !ev.CreatedAt.Before(prev.CreatedAt) {
			continue
		}
		out[idx] = ev
	}
	return out
}

// checklistStepName is the task in the reader's words, reusing the builder's
// vocabulary (issue #245). desk is the resolved desk label for routing steps
// and stays data; every other word follows the instance language.
func checklistStepName(tr i18n.Translator, step domain.WorkflowStep, desk string) string {
	switch step.Type {
	case domain.StepForm:
		if step.Form != nil && step.Form.Actor == domain.FormActorRequester {
			return tr.T("tickets.checklist.step.form_requester")
		}
		return tr.T("tickets.checklist.step.form_assignee")
	case domain.StepAssignToDesk:
		if step.AssignToDesk != nil && step.AssignToDesk.Strategy == domain.StrategyClaim {
			return tr.Tf("tickets.checklist.step.claim", desk)
		}
		return tr.Tf("tickets.checklist.step.least_loaded", desk)
	case domain.StepManualTask:
		if step.ManualTask != nil && step.ManualTask.Instructions != "" {
			// Instructions are admin-authored content: never translated (S11).
			return step.ManualTask.Instructions
		}
		return tr.T("tickets.checklist.step.agent_work")
	case domain.StepResolve:
		return tr.T("tickets.checklist.step.resolve")
	case domain.StepClose:
		return tr.T("tickets.checklist.step.close")
	default:
		return tr.T("tickets.checklist.step.generic")
	}
}

// checklistPendingMeta labels a step still waiting behind the current one: an
// automatic step runs on its own, every other step waits for the step before it.
func checklistPendingMeta(tr i18n.Translator, step domain.WorkflowStep) string {
	if step.Type == domain.StepResolve || step.Type == domain.StepClose {
		return tr.T("tickets.checklist.meta.runs_on_its_own")
	}
	if step.Type == domain.StepAssignToDesk && step.AssignToDesk != nil && step.AssignToDesk.Strategy == domain.StrategyLeastLoaded {
		return tr.T("tickets.checklist.meta.runs_on_its_own")
	}
	return tr.T("tickets.checklist.meta.waiting_previous")
}

// fillChecklistCurrent fills the current row's control facts. A claim's control
// stays in the Assignment sidebar, and an automatic step has no control at all;
// both render as the row's honest status line instead. Person and desk names are
// data and interpolate verbatim; every other word comes from the catalogue.
func (h *TicketHandlers) fillChecklistCurrent(tr i18n.Translator, row *workflowChecklistRow, step domain.WorkflowStep, desk string, actor domain.User, t *domain.Ticket, assignee *domain.User) {
	switch step.Type {
	case domain.StepManualTask:
		row.Kind = "manual"
		if t.UserID != nil && *t.UserID == actor.ID {
			row.CanAct = true
		}
		if t.UserID == nil {
			// A support-internal blocker is not the requester's truth (issue
			// #334 S1): their own state is that the request was received and is
			// queued. Leaving Blocked empty also keeps the amber nobody-copy
			// class off the requester's Steps area. Staff keep today's copy.
			if actor.Role == domain.RoleUser {
				row.Meta = tr.T("tickets.checklist.meta.requester_received")
				return
			}
			row.Meta = tr.T("tickets.checklist.meta.nobody_assigned")
			row.Blocked = tr.T("tickets.checklist.meta.blocked_no_agent")
			return
		}
		if assignee != nil {
			row.Meta = tr.Tf("tickets.checklist.meta.assigned_to", assignee.Name)
		} else {
			row.Meta = tr.T("tickets.checklist.meta.assigned")
		}
		if *t.UserID == actor.ID {
			row.Meta += " " + tr.T("tickets.checklist.meta.you")
		}
	case domain.StepForm:
		row.Kind = "form"
		if step.Form != nil {
			row.Fields = step.Form.Fields
			if step.Form.Actor == domain.FormActorRequester {
				row.CanAct = t.RequesterUserID != nil && *t.RequesterUserID == actor.ID
			} else {
				row.CanAct = t.UserID != nil && *t.UserID == actor.ID
			}
		}
		if row.CanAct {
			row.Meta = tr.T("tickets.checklist.meta.waiting_on_you")
			return
		}
		if step.Form != nil && step.Form.Actor == domain.FormActorRequester {
			if t.RequesterName != "" {
				row.Meta = tr.Tf("tickets.checklist.meta.waiting_on", t.RequesterName)
			} else {
				row.Meta = tr.T("tickets.checklist.meta.waiting_on_requester")
			}
		} else if assignee != nil {
			row.Meta = tr.Tf("tickets.checklist.meta.waiting_on", assignee.Name)
		} else {
			row.Meta = tr.T("tickets.checklist.meta.waiting_on_assignee")
		}
	case domain.StepAssignToDesk:
		if step.AssignToDesk != nil && step.AssignToDesk.Strategy == domain.StrategyClaim {
			row.Meta = tr.Tf("tickets.checklist.meta.asked_to_take", desk)
			return
		}
		row.Meta = tr.T("tickets.checklist.meta.runs_on_its_own")
	default:
		row.Meta = tr.T("tickets.checklist.meta.runs_on_its_own")
	}
}

// deskLabel resolves a routing step's desk name with a per-render cache; a
// missing or unreadable desk degrades to "the desk" instead of failing the
// ticket detail read.
func (h *TicketHandlers) deskLabel(r *http.Request, step domain.WorkflowStep, cache map[int64]string) string {
	fallback := i18n.New(requestLocale(r)).T("tickets.checklist.desk_fallback")
	if step.AssignToDesk == nil {
		return fallback
	}
	id := step.AssignToDesk.DeskID
	if name, ok := cache[id]; ok {
		return name
	}
	name := fallback
	if h.desks != nil {
		if desk, err := h.desks.GetByID(r.Context(), id); err == nil && desk != nil {
			name = desk.Name
		}
	}
	cache[id] = name
	return name
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
	claim := workflowClaim{Active: true, Position: cur + 1, DeskName: i18n.T(requestLocale(r), "tickets.degrade.unknown_desk")}
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
		httpError(w, r, "error.invalid_ticket_id", http.StatusBadRequest)
		return
	}
	data, status, err := h.detailDataFor(r, id)
	if err != nil {
		http.Error(w, mapErrorMsg(r, err), status)
		return
	}
	h.renderer.Render(w, r, "tickets_show", "ticket_detail", data, http.StatusOK)
}

func (h *TicketHandlers) transition(w http.ResponseWriter, r *http.Request) {
	id, ok := ticketID(r)
	if !ok {
		httpError(w, r, "error.invalid_ticket_id", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
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

// hold puts the ticket on hold while it waits for the requester (issue #352):
// the form carries the mandatory pause reason and the use case enforces every
// rule (capability, scope, blank reason, already on hold, closed ticket), so a
// refused request writes nothing. The response follows the transition route:
// HX swaps the detail fragment, a full request redirects to the detail page.
func (h *TicketHandlers) hold(w http.ResponseWriter, r *http.Request) {
	id, ok := ticketID(r)
	if !ok {
		httpError(w, r, "error.invalid_ticket_id", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	actor := *userFromContext(r.Context())

	if _, err := h.tickets.PutOnHold(r.Context(), actor, id, r.Form.Get("reason")); err != nil {
		h.renderHoldError(w, r, id, err)
		return
	}
	h.afterMutation(w, r, id, "ticket_detail")
}

// resume takes the ticket off hold (issue #352, S1), re-applying the working
// time spent paused to the frozen resolution instants. A lost compare-and-set
// race answers 409 through the same detail re-render.
func (h *TicketHandlers) resume(w http.ResponseWriter, r *http.Request) {
	id, ok := ticketID(r)
	if !ok {
		httpError(w, r, "error.invalid_ticket_id", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	actor := *userFromContext(r.Context())

	if _, err := h.tickets.ResumeFromHold(r.Context(), actor, id); err != nil {
		h.renderHoldError(w, r, id, err)
		return
	}
	h.afterMutation(w, r, id, "ticket_detail")
}

// renderHoldError re-renders the detail view after a refused hold or resume,
// keeping the pause reason the request carried so the agent does not retype it
// (a full request submits the form natively). Every status the hold use cases
// produce is rendered the same way: the message follows the instance language
// and a 409 keeps the form in place for the retry the copy asks for. An
// unexpected failure keeps the historical plain 500 body.
func (h *TicketHandlers) renderHoldError(w http.ResponseWriter, r *http.Request, id int64, err error) {
	status, msg := mapErrorFor(r, err)
	if status == http.StatusInternalServerError {
		http.Error(w, msg, status)
		return
	}
	data, _, dataErr := h.detailDataFor(r, id)
	if dataErr != nil {
		http.Error(w, mapErrorMsg(r, dataErr), statusFor(dataErr))
		return
	}
	data.Error = msg
	data.HoldReason = r.Form.Get("reason")
	data.HoldFailed = true
	h.renderer.Render(w, r, "tickets_show", "ticket_detail", data, status)
}

// confirmation applies the requester's resolution decision (issue #55):
// confirm closes a requester-owned resolved ticket; reject returns it to
// in_progress and detaches the workflow. Both use cases enforce the
// requester identity themselves — the handler only dispatches the form
// decision and routes responses through the shared detail helpers.
func (h *TicketHandlers) confirmation(w http.ResponseWriter, r *http.Request) {
	id, ok := ticketID(r)
	if !ok {
		httpError(w, r, "error.invalid_ticket_id", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	actor := *userFromContext(r.Context())

	var decide func(context.Context, domain.User, int64) (*domain.Ticket, error)
	switch r.Form.Get("decision") {
	case "confirm":
		decide = h.tickets.ConfirmResolution
	case "reject":
		decide = h.tickets.RejectResolution
	default:
		h.renderDetailError(w, r, id, &domain.ValidationError{Field: "decision", Message: "decision must be confirm or reject", Key: "error.invalid_decision"})
		return
	}

	if _, err := decide(r.Context(), actor, id); err != nil {
		h.renderDetailError(w, r, id, err)
		return
	}
	h.afterMutation(w, r, id, "ticket_detail")
}

func (h *TicketHandlers) addComment(w http.ResponseWriter, r *http.Request) {
	id, ok := ticketID(r)
	if !ok {
		httpError(w, r, "error.invalid_ticket_id", http.StatusBadRequest)
		return
	}
	uploads, err := parseTicketForm(r)
	if err != nil {
		renderUploadError(w, r, err)
		return
	}
	defer releaseMultipartForm(r)
	actor := *userFromContext(r.Context())

	visibility := r.Form.Get("visibility")
	if r.Form.Get("internal") != "" {
		visibility = string(domain.CommentInternal)
	}
	// The comment's files (issue #366) share the comment's visibility and its
	// transaction: a refused file refuses the comment, and the comment can
	// never exist without the files the author sent with it.
	_, err = h.comments.AddWithAttachments(r.Context(), actor, id, r.Form.Get("body"), visibility, uploads)
	if err != nil {
		h.renderDetailError(w, r, id, err)
		return
	}
	// HX: swap the merged timeline fragment; full: back to the detail page.
	data, status, err := h.detailDataFor(r, id)
	if err != nil {
		http.Error(w, mapErrorMsg(r, err), status)
		return
	}
	if r.Header.Get("HX-Request") != "" {
		saveFeedback(w, r, saveFeedbackSaved, saveFeedbackSuccess)
		h.renderer.Render(w, r, "tickets_show", "timeline", data, http.StatusOK)
		return
	}
	saveFeedback(w, r, saveFeedbackSaved, saveFeedbackSuccess)
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
		httpError(w, r, "error.invalid_ticket_id", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	// Desks are membership-only in this iteration. Reject a forged desk
	// target instead of treating its missing user_id as an unassignment.
	if r.Form.Get("desk_id") != "" {
		h.renderDetailError(w, r, id, &domain.ValidationError{Field: "desk", Message: "desks cannot be ticket assignees", Key: "error.desk_not_assignee"})
		return
	}
	actor := *userFromContext(r.Context())

	var assigneeID *int64
	if raw := r.Form.Get("user_id"); raw != "" {
		uid := parseID(raw)
		if uid == 0 {
			h.renderDetailError(w, r, id, &domain.ValidationError{Field: "user", Message: "invalid user", Key: "error.invalid_user"})
			return
		}
		assigneeID = &uid
	}

	_, err := h.tickets.Assign(r.Context(), actor, id, assigneeID, r.Form.Get("reason"))
	if err != nil {
		h.renderDetailError(w, r, id, err)
		return
	}
	// The picker confirms its pick with the shared save toast (issue #386, S5):
	// the listbox menu replaced the explicit Apply, so the pick itself has to
	// report that it was stored.
	saveFeedback(w, r, saveFeedbackSaved, saveFeedbackSuccess)
	h.afterMutation(w, r, id, "ticket_detail")
}

// renderDetailError re-renders the detail view with an inline error and the
// mapped status (HX → ticket_detail fragment; full → tickets_show page).
func (h *TicketHandlers) renderDetailError(w http.ResponseWriter, r *http.Request, id int64, err error) {
	status, msg := mapErrorFor(r, err)
	if status == http.StatusInternalServerError {
		http.Error(w, msg, status)
		return
	}
	data, _, dataErr := h.detailDataFor(r, id)
	if dataErr != nil {
		http.Error(w, mapErrorMsg(r, dataErr), statusFor(dataErr))
		return
	}
	data.Error = msg
	// Preserve the transition the user picked so a reopen-reason 422 keeps the
	// option selected and the reason group revealed after the outerHTML swap.
	data.SelectedTo = r.Form.Get("to")
	h.renderer.Render(w, r, "tickets_show", "ticket_detail", data, status)
}

// afterMutation answers a successful ticket mutation: HX → re-rendered
// fragment; full → 303 back to the detail page. It issues NO save feedback:
// every caller (title/priority edit, assign, transition, resolution
// confirmation) swaps #ticket-detail in place, so the value the actor just
// wrote is already on screen and a toast would only compete with it
// (issue #234). The ticket-create and comment paths are separate call sites
// and keep the shared feedback channel.
func (h *TicketHandlers) afterMutation(w http.ResponseWriter, r *http.Request, id int64, fragment string) {
	data, status, err := h.detailDataFor(r, id)
	if err != nil {
		http.Error(w, mapErrorMsg(r, err), status)
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
		httpError(w, r, "error.invalid_ticket_id", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	actor := *userFromContext(r.Context())

	u := domain.TicketUpdate{}
	// The description and the category are immutable after creation: the
	// edit form carries no fields for them, and forged ones are deliberately
	// never read — mirroring how forged assignment fields are ignored on
	// this route. Title and priority are applied ONLY when this request
	// actually carries them, so each detail control can post its own field
	// without a sibling edit riding along.
	if r.Form.Has("title") {
		title := r.Form.Get("title")
		u.Title = &title
	}
	if r.Form.Has("priority") {
		p := domain.Priority(r.Form.Get("priority"))
		u.Priority = &p
	}
	if u.Title == nil && u.Priority == nil {
		h.renderEditError(w, r, id, &domain.ValidationError{Field: "update", Message: "provide a title or a priority", Key: "error.provide_title_or_priority"})
		return
	}

	_, err := h.tickets.Update(r.Context(), actor, id, u)
	if err != nil {
		h.renderEditError(w, r, id, err)
		return
	}
	// A priority pick confirms itself with the shared save toast (issue #386,
	// S5). The inline title edit stays silent (issue #234): its new value is on
	// screen in the field the actor is typing into.
	if u.Priority != nil {
		saveFeedback(w, r, saveFeedbackSaved, saveFeedbackSuccess)
	}
	h.afterMutation(w, r, id, "ticket_detail")
}

// renderEditError re-renders the inline detail editor with submitted values.
func (h *TicketHandlers) renderEditError(w http.ResponseWriter, r *http.Request, id int64, err error) {
	status, msg := mapErrorFor(r, err)
	if status == http.StatusInternalServerError {
		http.Error(w, msg, status)
		return
	}
	data, _, dataErr := h.detailDataFor(r, id)
	if dataErr != nil {
		http.Error(w, mapErrorMsg(r, dataErr), statusFor(dataErr))
		return
	}
	data.Error = msg
	// detailDataFor seeded Values from the persisted ticket, so a field the
	// request did not carry keeps its stored value. Override only what this
	// form actually carries; the category is immutable and has no detail
	// control, so it is never read here.
	if r.Form.Has("title") {
		data.Values.Title = r.Form.Get("title")
	}
	if r.Form.Has("priority") {
		data.Values.Priority = domain.Priority(r.Form.Get("priority"))
	}
	if r.Form.Has("user_id") {
		data.Values.UserID = r.Form.Get("user_id")
	}
	h.renderer.Render(w, r, "tickets_show", "ticket_detail", data, status)
}

// isAgentQueueClaimRequest identifies an HTMX request from the agent queue.
func isAgentQueueClaimRequest(r *http.Request, actor domain.User) bool {
	return actor.Role == domain.RoleAgent &&
		r.Header.Get("HX-Request") != "" &&
		r.Header.Get("HX-Target") == agentQueueListTargetID
}

// agentQueueCurrentContext recovers q and the three page cursors from
// HX-Current-URL. Missing or malformed values fall back to the first page.
// Successful claims reset every cursor, while conflicts retain the valid
// requested cursors so no section jumps out from under the agent.
func agentQueueCurrentContext(r *http.Request, isConflict bool) (q string, assignedPage, donePage, claimablePage int) {
	raw := r.Header.Get("HX-Current-URL")
	u, err := url.Parse(raw)
	if raw == "" || err != nil || u == nil {
		return "", 1, 1, 1
	}
	qp := u.Query()
	assignedPage, donePage, claimablePage = urlPageNumber(qp, "assigned_page"), urlPageNumber(qp, "done_page"), urlPageNumber(qp, "claimable_page")
	if !isConflict {
		assignedPage, donePage, claimablePage = 1, 1, 1
	}
	return qp.Get("q"), assignedPage, donePage, claimablePage
}

// urlPageNumber returns a positive page number or the first page.
func urlPageNumber(q url.Values, key string) int {
	page := int(parseID(q.Get(key)))
	if page < 1 {
		return 1
	}
	return page
}

func (h *TicketHandlers) renderAgentQueue(w http.ResponseWriter, r *http.Request, claimError string, status int) {
	q, assignedPage, donePage, claimablePage := agentQueueCurrentContext(r, claimError != "")
	filters := simplifiedFilters(filterState{Q: q})
	assigned, done, claimable, total, err := h.agentQueueSections(r, filters, assignedPage, donePage, claimablePage)
	if err != nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	if claimError != "" {
		w.Header().Set("HX-Retarget", agentQueueListTarget)
		w.Header().Set("HX-Reswap", "outerHTML")
	}
	h.renderer.Render(w, r, "tickets_index", "agent_ticket_list", listData{Filters: filters, Total: total, AgentView: true, Assigned: assigned, Done: done, Claimable: claimable, ClaimError: claimError}, status)
}

func (h *TicketHandlers) renderAgentQueueClaimError(w http.ResponseWriter, r *http.Request, actor domain.User, err error) bool {
	status, _ := mapError(err)
	if status != http.StatusUnprocessableEntity || !isAgentQueueClaimRequest(r, actor) {
		return false
	}
	h.renderAgentQueue(w, r, i18n.T(requestLocale(r), "tickets.agent.claim_unavailable"), status)
	return true
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
		httpError(w, r, "error.invalid_ticket_id", http.StatusBadRequest)
		return
	}
	pos, err := strconv.Atoi(r.PathValue("position"))
	if err != nil || pos < 1 {
		h.renderDetailError(w, r, id, domain.NewWorkflowPositionConflictError("workflow position conflict"))
		return
	}
	if err := r.ParseForm(); err != nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	actor := *userFromContext(r.Context())

	// Authorize the ticket read through the scoped view (404 for out-of-scope)
	// before loading the execution snapshot.
	if _, err := h.tickets.GetByID(r.Context(), actor, id); err != nil {
		http.Error(w, mapErrorMsg(r, err), statusFor(err))
		return
	}
	snap, err := h.workflowRuns.GetWorkflowExecution(r.Context(), id)
	if err != nil {
		http.Error(w, mapErrorMsg(r, err), statusFor(err))
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
		h.renderDetailError(w, r, id, &domain.ValidationError{Field: "solution", Message: domain.ErrMsgSolutionTooLong, Key: "error.solution_too_long"})
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
		if h.renderAgentQueueClaimError(w, r, actor, err) {
			return
		}
		h.renderDetailError(w, r, id, err)
		return
	}
	if _, err := h.workflowTx.ApplyWorkflowPlan(r.Context(), plan); err != nil {
		if h.renderAgentQueueClaimError(w, r, actor, err) {
			return
		}
		h.renderDetailError(w, r, id, err)
		return
	}
	if isAgentQueueClaimRequest(r, actor) {
		h.renderAgentQueue(w, r, "", http.StatusOK)
		return
	}

	// Completion success answers 200 in both modes (the PR9 runtime contract
	// pins 200 rather than the mutation routes' 303; HTMX swaps the
	// #ticket-detail outerHTML fragment, full renders the tickets_show page).
	// Issue #234: the completion (including the sidebar claim) swaps
	// #ticket-detail in place, so the re-rendered fragment is the
	// confirmation — no success toast and no native in-response flash.
	data, status, err := h.detailDataFor(r, id)
	if err != nil {
		http.Error(w, mapErrorMsg(r, err), status)
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
			return nil, &domain.ValidationError{Field: "answers", Message: "manual_task ignores metadata", Key: "error.workflow_manual_metadata"}
		}
		return nil, nil
	case domain.StepAssignToDesk:
		if step.AssignToDesk == nil {
			return nil, &domain.ValidationError{Field: "type", Message: "assign_to_desk requires config"}
		}
		if step.AssignToDesk.Strategy != domain.StrategyClaim {
			return nil, &domain.ValidationError{Field: "state", Message: "automatic step", Key: "error.workflow_automatic_step"}
		}
		if hasWorkflowMeta(form) || form.Has("reason") || form.Has("solution") {
			return nil, &domain.ValidationError{Field: "claim", Message: "claim posts no form values", Key: "error.workflow_claim_form"}
		}
		return nil, nil
	case domain.StepForm:
		return parsePositionalAnswers(form, len(step.Form.Fields))
	default:
		// Terminal and unknown steps never accept a human completion form.
		return nil, &domain.ValidationError{Field: "state", Message: "workflow step cannot complete in current ticket state", Key: "error.workflow_step_state"}
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
			return nil, &domain.ValidationError{Field: "answers", Message: fmt.Sprintf("invalid answer position %q", k), Key: "error.answers.invalid_position", Args: []any{k}}
		}
		if n >= fieldCount {
			return nil, &domain.ValidationError{Field: "answers", Message: fmt.Sprintf("unknown position %d", n), Key: "error.answers.unknown_position", Args: []any{n}}
		}
		raw = append(raw, application.RawPositionalValue{Position: n, Values: vals})
	}
	sort.Slice(raw, func(i, j int) bool { return raw[i].Position < raw[j].Position })
	seen := make(map[int]struct{}, len(raw))
	for _, value := range raw {
		if _, ok := seen[value.Position]; ok {
			return nil, &domain.ValidationError{Field: "answers", Message: fmt.Sprintf("duplicate position %d", value.Position), Key: "error.answers.duplicate_position", Args: []any{value.Position}}
		}
		seen[value.Position] = struct{}{}
		if len(value.Values) > 1 {
			return nil, &domain.ValidationError{Field: "answers", Message: fmt.Sprintf("ambiguous values for position %d", value.Position), Key: "error.answers.ambiguous_position", Args: []any{value.Position}}
		}
	}
	return raw, nil
}

// mapErrorMsg returns the mapped message in the request's interface language;
// statusFor the mapped status. mapError keeps its English contract for the
// status table and internal callers.
func mapErrorMsg(r *http.Request, err error) string {
	_, msg := mapErrorFor(r, err)
	return msg
}

func statusFor(err error) int {
	status, _ := mapError(err)
	return status
}
