package httpadapter

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// NotificationHandlers serve the in-app notification center (issue #344, slice
// B4; specs S13-S16): the bell's unread count, the panel, and the two read
// writes. They mount on the ordinary app shell, so the session middleware has
// already resolved the signed-in user and the Origin gate has already rejected
// a cross-site write — these handlers only read, write, and render.
//
// The bell's count is served by its OWN endpoint and never computed while
// rendering an ordinary page (S13): an ordinary page render gains no
// notification query, and the panel's reads stay on the panel's routes.
type NotificationHandlers struct {
	store    application.NotificationStore
	tickets  application.TicketStore
	users    application.UserStore
	renderer *Renderer
	// clock stamps the read instants the two write routes store, so the panel
	// never reads the wall clock directly (D7).
	clock domain.Clock
}

// NewNotificationHandlers wires the notification routes against the
// notification store (the recipient-scoped read/write port slice B1 installed),
// the ticket store (each row's ticket facts, read through the actor's OWN read
// scope), the user store (the actor's name), and the renderer.
func NewNotificationHandlers(store application.NotificationStore, tickets application.TicketStore, users application.UserStore, renderer *Renderer, clock domain.Clock) *NotificationHandlers {
	return &NotificationHandlers{store: store, tickets: tickets, users: users, renderer: renderer, clock: clock}
}

// Register mounts the notification routes.
//
// The two writes are POSTs and answer a 303: marking a notification read is
// never a GET, so a prefetch, a crawler, or a link preview can never clear
// someone's badge.
func (h *NotificationHandlers) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /notifications", h.panel)
	mux.HandleFunc("GET /notifications/count", h.count)
	mux.HandleFunc("POST /notifications/read-all", h.markAllRead)
	mux.HandleFunc("POST /notifications/{id}/read", h.markRead)
}

// notificationsPanelLimit bounds the panel's window. The bell counts every
// unread row (the badge is exact); the panel shows the newest few, because it is
// a glance at what happened, not an archive — retention and history are out of
// scope (S17).
const notificationsPanelLimit = 20

// notificationBell is the rail bell's payload: how many of the recipient's
// notifications are unread. Zero renders the plain bell and no badge, so the
// number is never a "0" the reader has to interpret.
type notificationBell struct {
	Unread int
}

// notificationRow is ONE rendered notification: the ticket it is about, what
// happened as a catalogue message built from the stored kind and facts (S15),
// who did it, when, and whether it is still unread.
type notificationRow struct {
	ID       int64
	TicketID int64
	// Number is the readable ticket number ("TKT-12" is built from it) and
	// Title its title. Both are zero when the ticket is outside the reader's
	// access scope now: the row is still theirs to read (it was addressed to
	// them), but the panel never repeats facts about a ticket the reader can no
	// longer open (S16).
	Number int
	Title  string
	// Message is the "what happened" sentence; Actor is who did it ("Automatic"
	// when the stored actor is NULL).
	Message   application.Msg
	Actor     application.Msg
	CreatedAt time.Time
	Unread    bool
}

// notificationGroup is one day of notifications: today, yesterday, or the
// locale-formatted date for anything older.
type notificationGroup struct {
	// Kind is "today", "yesterday" or "date". The template answers the first two
	// with their catalogue sentences and the third with the localized date.
	Kind string
	Date time.Time
	Rows []notificationRow
}

// notificationsPanelData is the panel's payload in both of its forms: the
// drawer fragment the bell opens and the full page at GET /notifications.
type notificationsPanelData struct {
	pageData
	Groups []notificationGroup
	// Unread is the recipient's whole unread count, so "Mark all as read" is
	// only offered when it would do something.
	Unread int
}

// count answers the bell's unread count as a small fragment (S13). It is the
// ONLY place an ordinary page learns the number: the shell renders the bell in
// its zero state and the browser fetches the count through the rail wrapper's
// `hx-trigger="load, every 45s [document.visibilityState === 'visible'],
// notifications:changed from:body"`. That is this repository's first polling
// pattern, and it is deliberately three lines in base.html plus this handler:
// htmx owns the timer (no script), the filter keeps a hidden tab from polling,
// and the event is how the panel asks for a refresh after it opens or after a
// row is read. No page render ever computes the count.
func (h *NotificationHandlers) count(w http.ResponseWriter, r *http.Request) {
	actor := *userFromContext(r.Context())
	unread, err := h.store.UnreadCount(r.Context(), actor.ID)
	if err != nil {
		http.Error(w, mapErrorMsg(r, err), statusFor(err))
		return
	}
	h.renderer.RenderFragment(w, r, "notification_bell", notificationBell{Unread: unread}, http.StatusOK)
}

// panel renders the notification center (S14): the newest notifications of the
// signed-in user, grouped by day, as a full page or as the drawer fragment the
// bell opens.
func (h *NotificationHandlers) panel(w http.ResponseWriter, r *http.Request) {
	// GET /notifications answers two representations on one URL — the full page
	// and the drawer fragment the bell opens — so the response names the request
	// header that selects between them. Without it a shared cache could hand a
	// drawer fragment to a navigation or a whole page to the rail's swap, which
	// is the same contract the users and categories routes publish.
	setNotificationsVary(w)
	data, err := h.panelData(r)
	if err != nil {
		http.Error(w, mapErrorMsg(r, err), statusFor(err))
		return
	}
	if r.Header.Get("HX-Request") != "" {
		// Opening the panel refreshes the bell: the count is never derived from
		// this render (S13), the browser asks its own endpoint again.
		w.Header().Set("HX-Trigger-After-Swap", "notifications:changed")
	}
	h.renderer.Render(w, r, "notifications", "notification_panel", data, http.StatusOK)
}

// setNotificationsVary publishes the request header that selects between the
// notification panel's two representations. Only the panel serves a choice; the
// count endpoint has a single representation and is intentionally left alone.
func setNotificationsVary(w http.ResponseWriter) {
	w.Header().Add("Vary", "HX-Request")
}

// panelData reads the panel's two facts — the newest rows and the exact unread
// count — and resolves each row's ticket and actor.
func (h *NotificationHandlers) panelData(r *http.Request) (notificationsPanelData, error) {
	ctx := r.Context()
	actor := *userFromContext(ctx)
	stored, err := h.store.ListForUser(ctx, actor.ID, notificationsPanelLimit)
	if err != nil {
		return notificationsPanelData{}, err
	}
	unread, err := h.store.UnreadCount(ctx, actor.ID)
	if err != nil {
		return notificationsPanelData{}, err
	}
	resolver := notificationFacts{handlers: h, actor: actor, actors: map[int64]string{}, tickets: map[int64]notificationTicket{}}
	rows := make([]notificationRow, 0, len(stored))
	for _, n := range stored {
		row, err := resolver.row(ctx, n)
		if err != nil {
			return notificationsPanelData{}, err
		}
		rows = append(rows, row)
	}
	now := h.clock.Now().UTC()
	return notificationsPanelData{
		pageData: pageDataFrom(r, "notifications"),
		Groups:   notificationGroups(rows, now),
		Unread:   unread,
	}, nil
}

// notificationFacts resolves the data a panel row shows about OTHER records:
// the ticket's number and title, and the actor's name. Both are cached for the
// length of one render, so a panel of twenty rows costs at most one read per
// distinct ticket and one per distinct actor instead of one per row.
type notificationFacts struct {
	handlers *NotificationHandlers
	actor    domain.User
	actors   map[int64]string
	tickets  map[int64]notificationTicket
}

// notificationTicket is one cached ticket identity. The zero value means the
// ticket is outside the reader's reach now (or gone): the row keeps its place
// and its sentence, and opening it lands on the ordinary access-denied path
// (S16). The title is never repeated in that case.
type notificationTicket struct {
	Number int
	Title  string
}

// row resolves one stored notification into its rendered form.
func (f notificationFacts) row(ctx context.Context, n domain.Notification) (notificationRow, error) {
	row := notificationRow{
		ID:        n.ID,
		TicketID:  n.TicketID,
		Message:   notificationMessage(n),
		Actor:     application.MsgRef("notifications.actor.automatic"),
		CreatedAt: n.CreatedAt,
		Unread:    n.ReadAt == nil,
	}
	ticket, cached := f.tickets[n.TicketID]
	if !cached {
		stored, err := f.handlers.tickets.GetByID(ctx, n.TicketID, application.NotificationTicketQuery(f.actor))
		switch {
		case err == nil:
			ticket = notificationTicket{Number: stored.Number, Title: stored.Title}
		case errors.Is(err, domain.ErrNotFound):
			// Leave the zero value: out of reach, and nothing to repeat.
		default:
			return notificationRow{}, err
		}
		f.tickets[n.TicketID] = ticket
	}
	row.Number = ticket.Number
	row.Title = ticket.Title
	if n.ActorUserID == nil {
		return row, nil
	}
	name, ok := f.actors[*n.ActorUserID]
	if !ok {
		u, err := f.handlers.users.GetByID(ctx, *n.ActorUserID)
		switch {
		case err == nil:
			name = u.Name
			f.actors[*n.ActorUserID] = name
		case errors.Is(err, domain.ErrNotFound):
			row.Actor = application.MsgRef("notifications.actor.unknown")
			return row, nil
		default:
			return notificationRow{}, err
		}
	}
	row.Actor = application.Data(name)
	return row, nil
}

// notificationGroups splits the newest-first rows into the day groups the panel
// renders: today, yesterday, and one group per older day. Both instants are UTC,
// the instance's single reference frame (D7), so a group boundary never depends
// on the server's local zone.
func notificationGroups(rows []notificationRow, now time.Time) []notificationGroup {
	today := notificationDay(now)
	yesterday := today.AddDate(0, 0, -1)
	var groups []notificationGroup
	for _, row := range rows {
		day := notificationDay(row.CreatedAt)
		group := notificationGroup{Date: day}
		switch {
		case day.Equal(today):
			group.Kind = "today"
		case day.Equal(yesterday):
			group.Kind = "yesterday"
		default:
			group.Kind = "date"
		}
		if len(groups) > 0 {
			last := &groups[len(groups)-1]
			if last.Kind == group.Kind && last.Date.Equal(day) {
				last.Rows = append(last.Rows, row)
				continue
			}
		}
		group.Rows = []notificationRow{row}
		groups = append(groups, group)
	}
	return groups
}

// notificationDay truncates an instant to its UTC calendar day, which is the
// unit the panel groups by.
func notificationDay(t time.Time) time.Time {
	utc := t.UTC()
	return time.Date(utc.Year(), utc.Month(), utc.Day(), 0, 0, 0, 0, time.UTC)
}

// notificationMessage renders one stored notification's "what happened" from its
// KIND plus its structured facts (S15). The change kinds interpolate the raw
// domain values through the existing label.state / label.priority keys, so a
// Spanish instance reads Spanish state and priority names. A comment kind names
// its VISIBILITY only: the body is never stored on a notification and is never
// read here.
func notificationMessage(n domain.Notification) application.Msg {
	switch n.Kind {
	case domain.NotificationAssigned:
		return application.MsgRef("notifications.kind.assigned")
	case domain.NotificationStateChanged:
		return application.MsgRef("notifications.kind.state_changed",
			notificationFact("state", n.FromValue), notificationFact("state", n.ToValue))
	case domain.NotificationResolvedAwaitingConfirmation:
		return application.MsgRef("notifications.kind.resolved_awaiting_confirmation")
	case domain.NotificationResolutionConfirmed:
		return application.MsgRef("notifications.kind.resolution_confirmed")
	case domain.NotificationResolutionRejected:
		return application.MsgRef("notifications.kind.resolution_rejected")
	case domain.NotificationPriorityChanged:
		return application.MsgRef("notifications.kind.priority_changed",
			notificationFact("priority", n.FromValue), notificationFact("priority", n.ToValue))
	case domain.NotificationCommentPublic:
		return application.MsgRef("notifications.kind.comment_public")
	case domain.NotificationCommentInternal:
		return application.MsgRef("notifications.kind.comment_internal")
	case domain.NotificationFormWaiting:
		return application.MsgRef("notifications.kind.form_waiting")
	default:
		// A kind no catalogue names cannot be stored (the domain refuses it), and
		// the sentence still says something happened instead of printing an
		// identifier the reader cannot use.
		return application.MsgRef("notifications.kind.unknown")
	}
}

// notificationFact names one stored change value through its label namespace
// (label.state.* / label.priority.*), so the sentence speaks the instance
// language. A kind stored without its fact says "unknown" rather than leaving
// the sentence with an empty slot.
func notificationFact(kind string, value *string) application.Arg {
	if value == nil || *value == "" {
		return application.Copy("notifications.value.unknown")
	}
	return application.Copy("label." + kind + "." + *value)
}

// markRead marks ONE notification read (S14 "Clicking one opens the ticket and
// marks it read") and then sends the reader to the ticket. The row is marked
// read BEFORE the redirect, so a ticket the reader can no longer open — the
// detail route re-checks access (S16) — still leaves the notification read
// instead of coming back unread forever.
func (h *NotificationHandlers) markRead(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	id, ok := notificationID(r)
	if !ok {
		httpError(w, r, "error.not_found", http.StatusNotFound)
		return
	}
	actor := *userFromContext(r.Context())
	if err := h.store.MarkRead(r.Context(), actor.ID, id, h.clock.Now()); err != nil {
		http.Error(w, mapErrorMsg(r, err), statusFor(err))
		return
	}
	redirect(w, r, notificationTicketHref(r.Form.Get("ticket_id")))
}

// markAllRead clears the recipient's whole badge in one write (S14 "Mark all as
// read"). It answers the panel's own page, so the reader sees the list they
// acted on, now with nothing unread.
func (h *NotificationHandlers) markAllRead(w http.ResponseWriter, r *http.Request) {
	actor := *userFromContext(r.Context())
	if err := h.store.MarkAllRead(r.Context(), actor.ID, h.clock.Now()); err != nil {
		http.Error(w, mapErrorMsg(r, err), statusFor(err))
		return
	}
	redirect(w, r, "/notifications")
}

// notificationID reads the notification id from the route pattern.
func notificationID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

// notificationTicketHref is the panel row's redirect target: the ticket the row
// is about. The posted id only ever selects among THIS instance's ticket
// routes (the prefix is added here), and the detail route re-checks access, so a
// tampered value can neither leave the instance nor open a ticket the reader
// could not open by typing the URL. A row whose form lost its target falls back
// to the panel.
func notificationTicketHref(raw string) string {
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return "/notifications"
	}
	return "/tickets/" + strconv.FormatInt(id, 10)
}
