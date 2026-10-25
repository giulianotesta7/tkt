package httpadapter

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
	"github.com/giulianotesta7/tkt/internal/i18n"
)

// TestTicketShowRendersDetail proves GET /tickets/{id} renders the detail
// page: number, title, category, comments, and the Activity (audit) panel.
func TestTicketShowRendersDetail(t *testing.T) {
	h := newHarness(t)
	tkt := h.seedTicket(t, "Login page down", nil)
	h.seedTransition(t, tkt.ID, domain.StateInProgress, "")
	if _, err := h.comments.Add(t.Context(), *h.admin, tkt.ID, "Checking now", "public"); err != nil {
		t.Fatalf("seed comment: %v", err)
	}

	rec := h.get(t, "/tickets/1", false)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"Login page down", "TKT-1", "Bugs", "Checking now", "Activity", "Properties", "id=\"ticket-title\"", "<h2>Description</h2>", "Test description"} {
		if !strings.Contains(body, want) {
			t.Errorf("detail page must contain %q, got: %s", want, body)
		}
	}
	if strings.Contains(body, `href="/tickets/1/edit"`) {
		t.Errorf("detail page must not expose the fallback edit screen, got: %s", body)
	}
	for _, want := range []string{`action="/tickets/1/edit"`, `id="ticket-priority"`} {
		if !strings.Contains(body, want) {
			t.Errorf("inline properties form must contain %q, got: %s", want, body)
		}
	}
	for _, want := range []string{`action="/tickets/1/assign"`, `id="assign-user"`} {
		if !strings.Contains(body, want) {
			t.Errorf("assignment form must contain %q, got: %s", want, body)
		}
	}
	for _, want := range []string{`id="ticket-title"`, `name="title"`, `id="ticket-priority"`} {
		if !strings.Contains(body, want) {
			t.Errorf("title/priority must be editable on detail, missing %q in: %s", want, body)
		}
	}
	// The description and the category are immutable after creation: the
	// edit form must not present either (the read-only card above is the
	// only description surface, and the category shows as read-only
	// metadata — no select, no form field).
	if strings.Contains(body, `name="description"`) || strings.Contains(body, `id="ticket-description"`) {
		t.Errorf("detail edit form must not render a description field (immutable after creation), got: %s", body)
	}
	if strings.Contains(body, `name="category_id"`) || strings.Contains(body, `<select id="ticket-category"`) {
		t.Errorf("detail edit form must not render a category control (immutable after creation), got: %s", body)
	}
	if !strings.Contains(body, `id="ticket-category-value"`) {
		t.Errorf("the category must stay visible as read-only metadata, got: %s", body)
	}
	// Merged timeline ASC (issue #386, S3): the feed reads top to bottom into
	// the composer, so the oldest entry renders before the newest one. Match
	// the actor-first event narrative lines (not bare words).
	createdEvent := "created the ticket"
	transitionEvent := "moved the ticket to in progress"
	if !(strings.Index(body, createdEvent) < strings.Index(body, transitionEvent)) {
		t.Errorf("merged timeline must be oldest-first (created before transition), got: %s", body)
	}
}

func TestTicketCommentCheckboxMapsInternalAndRejectsUserForgery(t *testing.T) {
	h := newHarness(t)
	tkt := h.seedTicket(t, "Checkbox visibility", nil)
	adminSession := seedSession(t, h.store, h.admin.ID)

	staffRec := h.postFormAs(t, "/tickets/1/comments", url.Values{
		"body":       {"Internal update"},
		"visibility": {"public"},
		"internal":   {"1"},
	}, adminSession.ID)
	if staffRec.Code != http.StatusSeeOther {
		t.Fatalf("staff status = %d, want 303", staffRec.Code)
	}
	comments, err := h.comments.ListByTicket(t.Context(), tkt.ID, true)
	if err != nil {
		t.Fatalf("list staff comments: %v", err)
	}
	if len(comments) != 1 || comments[0].Visibility != domain.CommentInternal {
		t.Fatalf("staff checkbox must store one internal comment, got: %+v", comments)
	}

	user := seedUserRole(t, h.store, "Ula", "ula@example.com", domain.RoleUser)
	userSession := seedSession(t, h.store, user.ID)
	userTicket, err := h.tickets.Create(t.Context(), *user, application.CreateTicketInput{
		Title: "User visibility", CategoryID: h.bugCategory.ID, Priority: domain.PriorityMedium,
	})
	if err != nil {
		t.Fatalf("create user ticket: %v", err)
	}
	forgedRec := h.postFormAs(t, "/tickets/"+strconv.FormatInt(userTicket.ID, 10)+"/comments", url.Values{
		"body":       {"Forged internal update"},
		"visibility": {"public"},
		"internal":   {"1"},
	}, userSession.ID)
	if forgedRec.Code != http.StatusForbidden {
		t.Fatalf("forged user status = %d, want 403", forgedRec.Code)
	}
	userComments, err := h.comments.ListByTicket(t.Context(), userTicket.ID, true)
	if err != nil {
		t.Fatalf("list user comments: %v", err)
	}
	if len(userComments) != 0 {
		t.Fatalf("forged user input must not store an internal comment, got: %+v", userComments)
	}
}

func TestTicketShowRendersConciseSemanticMetadata(t *testing.T) {
	h := newHarness(t)
	h.seedTicket(t, "Login page down", nil)

	body := h.get(t, "/tickets/1", false).Body.String()
	if !strings.Contains(body, `>Requester</span>`) || !strings.Contains(body, "Admin") {
		t.Errorf("requester must appear as a property row in the sidebar, got: %s", body)
	}
	if strings.Contains(body, "Requester:") {
		t.Errorf("requester must be a sidebar property row, not header metadata, got: %s", body)
	}
	if got := strings.Count(body, `<time datetime="`); got < 3 {
		t.Errorf("detail metadata and timeline must use semantic time elements, got %d: %s", got, body)
	}
	if !strings.Contains(body, " · ") {
		t.Errorf("display timestamps must use the human UTC separator, got: %s", body)
	}
}

func TestAssignedAgentSeesTicketControls(t *testing.T) {
	h := newHarness(t)
	agent := h.createUser(t, "Agent", "agent@tkt.test", "s3cret-pass-12")
	ticket := h.seedTicket(t, "Assigned work", nil)
	h.assignTicket(t, ticket.ID, agent.ID)
	session := h.loginCookie(t, agent.Email, "s3cret-pass-12")
	if session == "" {
		t.Fatal("agent login must succeed")
	}
	req := httptest.NewRequest(http.MethodGet, "/tickets/"+strconv.FormatInt(ticket.ID, 10), nil)
	req.Header.Set("Cookie", sessionCookie+"="+session)
	rec := httptest.NewRecorder()
	h.mw.Wrap(h.mux).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("assigned agent detail = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{`action="/tickets/1/transition"`, `name="to"`, `name="visibility"`} {
		if !strings.Contains(body, want) {
			t.Errorf("assigned agent controls must include %q", want)
		}
	}
}

func TestTicketTimelineDifferentiatesCommentsAndAuditEvents(t *testing.T) {
	h := newHarness(t)
	tkt := h.seedTicket(t, "Login page down", nil)
	h.seedTransition(t, tkt.ID, domain.StateInProgress, "")
	if _, err := h.comments.Add(t.Context(), *h.admin, tkt.ID, "Checking now", "public"); err != nil {
		t.Fatalf("seed comment: %v", err)
	}

	body := h.get(t, "/tickets/1", false).Body.String()
	for _, want := range []string{`class="timeline-entry timeline-comment"`, `class="timeline-entry timeline-event`, "moved the ticket to in progress", "st-in_progress"} {
		if !strings.Contains(body, want) {
			t.Errorf("timeline must contain %q, got: %s", want, body)
		}
	}
	if strings.Contains(body, "new → in_progress") {
		t.Errorf("timeline must not expose internal state values, got: %s", body)
	}
}

// TestTicketShowNonNumericID400 proves a non-numeric {id} is a 400
// (threat matrix).
func TestTicketShowNonNumericID400(t *testing.T) {
	h := newHarness(t)
	rec := h.get(t, "/tickets/abc", false)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

// TestTicketShowUnknownID404 proves an unknown ticket id is a 404.
func TestTicketShowUnknownID404(t *testing.T) {
	h := newHarness(t)
	rec := h.get(t, "/tickets/999", false)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

// TestTicketShowHXFragment proves the HX detail path returns the
// ticket_detail fragment only.
func TestTicketShowHXFragment(t *testing.T) {
	h := newHarness(t)
	h.seedTicket(t, "Login page down", nil)

	rec := h.get(t, "/tickets/1", true)

	body := rec.Body.String()
	if strings.Contains(body, "<!DOCTYPE html>") {
		t.Errorf("HX detail must not contain the shell, got: %s", body)
	}
	if !strings.Contains(body, "Login page down") {
		t.Errorf("fragment must render the detail, got: %s", body)
	}
}

// TestTicketTransitionHappyPath proves new → in_progress records the
// transition with the session actor and redirects 303 to the detail page.
func TestTicketTransitionHappyPath(t *testing.T) {
	h := newHarness(t)
	h.seedTicket(t, "Login page down", nil)

	rec := h.postForm(t, "/tickets/1/transition", url.Values{"to": {"in_progress"}}, false)

	wantRedirect(t, rec, http.StatusSeeOther, "/tickets/1")

	view, err := h.tickets.GetByID(t.Context(), *h.admin, 1)
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	if view.Ticket.State != domain.StateInProgress {
		t.Errorf("state = %q, want in_progress", view.Ticket.State)
	}
	if len(view.AuditEvents) != 2 {
		t.Fatalf("audit events = %d, want 2 (created + transition)", len(view.AuditEvents))
	}
	ev := view.AuditEvents[1]
	if ev.Action != domain.ActionTransition || ev.Actor != h.admin.Name {
		t.Errorf("transition event = %+v, want action=transition actor=%q", ev, h.admin.Name)
	}
}

// TestTicketTransitionFullCycle proves the full forward path
// new → in_progress → resolved → closed, closing stamps closed_at.
// Uses a legacy (requester-NULL) ticket: manual closure is the
// requester-NULL exception (issue #55); requester-owned closures go
// through the confirmation flow covered in its own tests.
func TestTicketTransitionFullCycle(t *testing.T) {
	h := newHarness(t)
	tkt := h.seedTicket(t, "Login page down", nil)
	h.makeLegacy(t, tkt.ID)

	for _, to := range []domain.State{domain.StateInProgress, domain.StateResolved, domain.StateClosed} {
		rec := h.postForm(t, "/tickets/1/transition", url.Values{"to": {string(to)}}, false)
		wantRedirect(t, rec, http.StatusSeeOther, "/tickets/1")
	}

	view, err := h.tickets.GetByID(t.Context(), *h.admin, 1)
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	if view.Ticket.State != domain.StateClosed {
		t.Errorf("state = %q, want closed", view.Ticket.State)
	}
	if view.Ticket.ClosedAt == nil {
		t.Error("closed_at must be stamped by the closed transition")
	}
}

// TestTicketTransitionInvalid422 proves an illegal pair (new → closed) is
// rejected 422 with the transition-not-allowed message.
func TestTicketTransitionInvalid422(t *testing.T) {
	h := newHarness(t)
	h.seedTicket(t, "Login page down", nil)

	rec := h.postForm(t, "/tickets/1/transition", url.Values{"to": {"closed"}}, false)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "transition not allowed from new to closed") {
		t.Errorf("re-render must show the transition message, got: %s", rec.Body.String())
	}
	view, err := h.tickets.GetByID(t.Context(), *h.admin, 1)
	if err != nil || view.Ticket.State != domain.StateNew {
		t.Errorf("rejected transition must leave the state unchanged (state=%q, err=%v)", view.Ticket.State, err)
	}
}

// TestTicketTransitionReopenRequiresReason proves closed → in_progress
// without a reason is rejected 422 (reopen-reason spec) on a legacy
// (requester-NULL) ticket.
func TestTicketTransitionReopenRequiresReason(t *testing.T) {
	h := newHarness(t)
	tkt := h.seedTicket(t, "Login page down", nil)
	h.makeLegacy(t, tkt.ID)
	for _, to := range []domain.State{domain.StateInProgress, domain.StateResolved, domain.StateClosed} {
		h.seedTransition(t, tkt.ID, to, "")
	}

	rec := h.postForm(t, "/tickets/1/transition", url.Values{"to": {"in_progress"}}, false)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), domain.ErrMsgReopenReasonRequired) {
		t.Errorf("re-render must show %q, got: %s", domain.ErrMsgReopenReasonRequired, rec.Body.String())
	}
}

// TestTicketTransitionReopenWithReason proves the closed reopen with a
// reason succeeds and the reason lands in the audit note — legacy
// (requester-NULL) ticket.
func TestTicketTransitionReopenWithReason(t *testing.T) {
	h := newHarness(t)
	tkt := h.seedTicket(t, "Login page down", nil)
	h.makeLegacy(t, tkt.ID)
	for _, to := range []domain.State{domain.StateInProgress, domain.StateResolved, domain.StateClosed} {
		h.seedTransition(t, tkt.ID, to, "")
	}

	rec := h.postForm(t, "/tickets/1/transition", url.Values{"to": {"in_progress"}, "reason": {"fix deployed"}}, false)

	wantRedirect(t, rec, http.StatusSeeOther, "/tickets/1")
	view, err := h.tickets.GetByID(t.Context(), *h.admin, 1)
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	if view.Ticket.State != domain.StateInProgress {
		t.Errorf("state = %q, want in_progress", view.Ticket.State)
	}
	last := view.AuditEvents[len(view.AuditEvents)-1]
	if last.Note == nil || *last.Note != "fix deployed" {
		t.Errorf("reopen note = %v, want fix deployed", last.Note)
	}
	if view.Ticket.ClosedAt != nil || view.Ticket.ResolvedAt != nil {
		t.Error("reopen must clear resolved_at and closed_at")
	}
	// The reopen reason surfaces in the timeline as an explicitly labeled line.
	page := h.get(t, "/tickets/1", false)
	if !strings.Contains(page.Body.String(), "Reason: fix deployed") {
		t.Errorf("timeline must label the reopen reason as %q, got: %s", "Reason: fix deployed", page.Body.String())
	}
	// A reopen reads as "reopened the ticket", not "moved the ticket to in progress".
	if !strings.Contains(page.Body.String(), "reopened the ticket") {
		t.Errorf("timeline must summarize a reopen as %q, got: %s", "reopened the ticket", page.Body.String())
	}
}

// TestTicketTransitionHXFragment proves the HX transition path returns the
// updated ticket_detail fragment.
func TestTicketTransitionHXFragment(t *testing.T) {
	h := newHarness(t)
	h.seedTicket(t, "Login page down", nil)

	rec := h.postForm(t, "/tickets/1/transition", url.Values{"to": {"in_progress"}}, true)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "<!DOCTYPE html>") {
		t.Errorf("HX transition must return the fragment, got: %s", body)
	}
	if !strings.Contains(body, "In Progress") || strings.Contains(body, ">in_progress<") {
		t.Errorf("fragment must show the humanized new state, got: %s", body)
	}
}

// TestTicketCommentAdd proves POST /tickets/{id}/comments stores the comment
// with the session user as author and redirects 303 (add-comment spec).
func TestTicketCommentAdd(t *testing.T) {
	h := newHarness(t)
	h.seedTicket(t, "Login page down", nil)

	rec := h.postForm(t, "/tickets/1/comments", url.Values{"body": {"Checking now"}}, false)

	wantRedirect(t, rec, http.StatusSeeOther, "/tickets/1")
	view, err := h.tickets.GetByID(t.Context(), *h.admin, 1)
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	if len(view.Comments) != 1 {
		t.Fatalf("comments = %d, want 1", len(view.Comments))
	}
	if view.Comments[0].Author != h.admin.Name || view.Comments[0].Body != "Checking now" {
		t.Errorf("comment = %+v, want author %q body Checking now", view.Comments[0], h.admin.Name)
	}
}

// TestTicketCommentEmptyBody422 proves an empty comment body is rejected 422
// and nothing is stored.
func TestTicketCommentEmptyBody422(t *testing.T) {
	h := newHarness(t)
	h.seedTicket(t, "Login page down", nil)

	rec := h.postForm(t, "/tickets/1/comments", url.Values{"body": {"   "}}, false)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), domain.ErrMsgCommentBodyRequired) {
		t.Errorf("re-render must show %q, got: %s", domain.ErrMsgCommentBodyRequired, rec.Body.String())
	}
	view, err := h.tickets.GetByID(t.Context(), *h.admin, 1)
	if err != nil || len(view.Comments) != 0 {
		t.Errorf("no comment may be stored (len=%d, err=%v)", len(view.Comments), err)
	}
}

// TestTicketCommentOnClosedTicketRejected proves comments are REJECTED on a
// closed (resolved/closed/cancelled) ticket with a 403 ForbiddenError and
// nothing stored — enforced at the application boundary, so a forged POST
// cannot append to a closed ticket (closed-ticket read-only spec).
func TestTicketCommentOnClosedTicketRejected(t *testing.T) {
	for _, to := range []domain.State{domain.StateResolved, domain.StateClosed, domain.StateCancelled} {
		t.Run(string(to), func(t *testing.T) {
			h := newHarness(t)
			tkt := h.seedTicket(t, "Login page down", nil)
			// Legacy (requester-NULL) ticket: the closed-state walk below must
			// stay legal under the #55 closure gate, which only restricts
			// requester-owned tickets.
			h.makeLegacy(t, tkt.ID)
			// Walk the legal transition path to the closed target: closed must
			// be reached via in_progress -> resolved -> closed (matrix).
			path := []domain.State{to}
			if to == domain.StateClosed {
				path = []domain.State{domain.StateInProgress, domain.StateResolved, domain.StateClosed}
			}
			for _, step := range path {
				h.seedTransition(t, tkt.ID, step, "")
			}

			rec := h.postForm(t, "/tickets/1/comments", url.Values{"body": {"Late note"}}, false)

			if rec.Code != http.StatusForbidden {
				t.Errorf("status = %d, want 403", rec.Code)
			}
			if !strings.Contains(rec.Body.String(), domain.ErrMsgCommentOnClosedTicket) {
				t.Errorf("response must show %q, got: %s", domain.ErrMsgCommentOnClosedTicket, rec.Body.String())
			}
			view, err := h.tickets.GetByID(t.Context(), *h.admin, 1)
			if err != nil || len(view.Comments) != 0 {
				t.Errorf("comment on closed ticket must NOT be stored (len=%d, err=%v)", len(view.Comments), err)
			}
		})
	}
}

// TestTicketCommentHXFragment proves the HX comment path returns the
// merged timeline fragment carrying the new comment.
func TestTicketCommentHXFragment(t *testing.T) {
	h := newHarness(t)
	h.seedTicket(t, "Login page down", nil)

	rec := h.postForm(t, "/tickets/1/comments", url.Values{"body": {"Checking now"}}, true)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "<!DOCTYPE html>") {
		t.Errorf("HX comment must return the fragment, got: %s", body)
	}
	if !strings.Contains(body, "Checking now") {
		t.Errorf("timeline fragment must carry the new comment, got: %s", body)
	}
}

// TestTicketCommentsNewestFirst proves the rendered timeline shows
// comments newest first (comment-timeline spec: reverse-chronological).
//
// The ordering is proved over the timeline region alone, never over the whole
// document: every ticket page emits the shared inline stylesheet in <head>, so
// an unscoped strings.Index can be broken by a token outside the timeline
// (a CSS selector such as :first-child) while the rendering stays correct. The
// TestTicketCommentsOldestFirst proves the rendered timeline shows comments
// oldest first (issue #386, S3: the approved mockup reads top to bottom into
// the composer).
//
// The ordering is proved over the timeline region alone, never over the whole
// document: every ticket page emits the shared inline stylesheet in <head>, so
// an unscoped strings.Index can be broken by a token outside the timeline
// (a CSS selector such as :first-child) while the rendering stays correct. The
// seeded bodies are therefore distinctive tokens, not bare ordinal words.
func TestTicketCommentsOldestFirst(t *testing.T) {
	h := newHarness(t)
	h.seedTicket(t, "Login page down", nil)
	for _, body := range []string{"timeline-comment-first", "timeline-comment-second", "timeline-comment-third"} {
		if _, err := h.comments.Add(t.Context(), *h.admin, 1, body, "public"); err != nil {
			t.Fatalf("seed comment %q: %v", body, err)
		}
	}

	rec := h.get(t, "/tickets/1", false)
	body := rec.Body.String()

	start := strings.Index(body, `<div id="timeline">`)
	if start < 0 {
		t.Fatalf("ticket detail must render the Timeline, got: %s", body)
	}
	timeline := body[start:]
	if end := strings.Index(timeline, `<div class="evidence">`); end >= 0 {
		timeline = timeline[:end]
	}

	// Adjacent pairs, so the failure names the pair that inverted.
	for _, pair := range []struct{ older, newer string }{
		{"timeline-comment-first", "timeline-comment-second"},
		{"timeline-comment-second", "timeline-comment-third"},
	} {
		olderAt, newerAt := strings.Index(timeline, pair.older), strings.Index(timeline, pair.newer)
		if olderAt < 0 || newerAt < 0 {
			t.Fatalf("timeline must carry %q and %q, got: %s", pair.older, pair.newer, timeline)
		}
		if olderAt > newerAt {
			t.Errorf("%q must render before %q (oldest first), got: %s", pair.older, pair.newer, timeline)
		}
	}
}

// TestTicketTimelineGrammar proves issue #386's S3 grammar: ONE shape for every
// entry — a head line with the actor in ink, the verb in muted copy and the
// relative time right-aligned (machine datetime, absolute instant in title) —
// and comments as a bordered block under their own head line. Internal notes
// keep their own background and say who may read them; the coloured verbs and
// the monospace times are gone.
func TestTicketTimelineGrammar(t *testing.T) {
	h := newHarness(t)
	tkt := h.seedTicket(t, "Grammar", nil)
	h.seedTransition(t, tkt.ID, domain.StateInProgress, "")
	if _, err := h.comments.Add(t.Context(), *h.admin, tkt.ID, "public-grammar-token", "public"); err != nil {
		t.Fatalf("seed public comment: %v", err)
	}
	if _, err := h.comments.Add(t.Context(), *h.admin, tkt.ID, "internal-grammar-token", "internal"); err != nil {
		t.Fatalf("seed internal comment: %v", err)
	}

	body := h.get(t, "/tickets/1", true).Body.String()
	start := strings.Index(body, `<div id="timeline">`)
	if start < 0 {
		t.Fatalf("detail must render #timeline, got: %s", body)
	}
	timeline := body[start:]
	if end := strings.Index(timeline, `id="detail-properties"`); end >= 0 {
		timeline = timeline[:end]
	}

	// One head line per entry, and every head carries a semantic time with the
	// absolute instant in its title.
	entries := strings.Count(timeline, `<article class="timeline-entry`)
	heads := strings.Count(timeline, `class="timeline-entry-head"`)
	if entries == 0 || entries != heads {
		t.Errorf("every entry must carry exactly one head line (entries=%d heads=%d)\n%s", entries, heads, timeline)
	}
	if times := strings.Count(timeline, `<time datetime="`); times < entries {
		t.Errorf("every entry must carry a semantic time (entries=%d times=%d)\n%s", entries, times, timeline)
	}
	if !strings.Contains(timeline, `title="`) {
		t.Errorf("the relative time must carry the absolute instant in its title\n%s", timeline)
	}
	// Relative time, judged against the render's clock snapshot.
	if !strings.Contains(timeline, "just now") {
		t.Errorf("the timeline must phrase its instants relatively\n%s", timeline)
	}

	// One comment grammar: the bordered block sits under its own head line.
	if !strings.Contains(timeline, `class="timeline-body"`) {
		t.Errorf("a comment body must render as a block under its head line\n%s", timeline)
	}
	if strings.Contains(timeline, `class="event-icon"`) {
		t.Errorf("the per-entry glyph is gone: the rail carries the dots\n%s", timeline)
	}
	if strings.Contains(timeline, `class="main"`) {
		t.Errorf("events must be one line, not a nested body block\n%s", timeline)
	}

	// Internal notes keep their affordance and say who may read them.
	if !strings.Contains(timeline, `class="timeline-entry timeline-comment internal"`) {
		t.Errorf("an internal comment must keep its own class\n%s", timeline)
	}
	if !strings.Contains(timeline, `Internal · staff only`) {
		t.Errorf("an internal comment must label its audience\n%s", timeline)
	}
	// The internal note is invisible to a requester: the store read filters it,
	// so the label cannot leak either.
	requester := seedUserRole(t, h.store, "Rosa", "rosa-grammar@tkt.test", domain.RoleUser)
	sess := seedSession(t, h.store, requester.ID)
	if _, err := h.rawDB(t).Exec(`UPDATE tickets SET requester_user_id = ? WHERE id = ?`, requester.ID, tkt.ID); err != nil {
		t.Fatalf("pin requester: %v", err)
	}
	requesterBody := getDetailAs(t, h, "/tickets/1", sess.ID, true).Body.String()
	for _, absent := range []string{"internal-grammar-token", "Internal · staff only"} {
		if strings.Contains(requesterBody, absent) {
			t.Errorf("a requester must never read the staff-only note (%q)\n%s", absent, requesterBody)
		}
	}
	if !strings.Contains(requesterBody, "public-grammar-token") {
		t.Errorf("a requester still reads the public comment\n%s", requesterBody)
	}
}

// TestTicketDetailBodyOrder proves issue #386's S2 order: the description card
// first, then the activity region, then the properties, then the requester
// confirmation (which stays above the composer), and the composer last.
func TestTicketDetailBodyOrder(t *testing.T) {
	h := newHarness(t)
	requester := seedUserRole(t, h.store, "Rosa", "rosa-order@tkt.test", domain.RoleUser)
	sess := seedSession(t, h.store, requester.ID)
	tkt := createTicketFor(t, h, requester, "Printer jam")
	h.seedTransition(t, tkt.ID, domain.StateInProgress, "")
	h.seedTransition(t, tkt.ID, domain.StateResolved, "")

	body := getDetailAs(t, h, ticketPath(tkt.ID), sess.ID, true).Body.String()
	markers := []string{
		`<h2>Description</h2>`,
		`id="timeline"`,
		`id="detail-properties"`,
		`class="card resolution-confirmation"`,
		`class="card comment-form"`,
	}
	previous := -1
	for _, marker := range markers {
		at := strings.Index(body, marker)
		if at < 0 {
			t.Fatalf("body must render %q, got: %s", marker, body)
		}
		if at < previous {
			t.Errorf("body order is wrong: %q must follow the previous marker\n%s", marker, body)
		}
		previous = at
	}
	// The workflow checklist stays the first block of the activity region
	// (today's #timeline contract, S2/L3).
	activity := body[strings.Index(body, `id="timeline"`):]
	if pending := strings.Index(activity, `id="workflow-pending"`); pending >= 0 {
		firstEntry := strings.Index(activity, `class="timeline-entry`)
		if firstEntry >= 0 && pending > firstEntry {
			t.Errorf("the checklist must stay the first block of #timeline, got: %s", activity[:firstEntry])
		}
	}
}

// TestTicketComposer proves issue #386's S4 composer: the draft-retaining
// textarea keeps its ids, the internal-note switch keeps the same field, the
// attachments become a drop zone wrapping the same file input, and the submit
// is Reply — all on today's native POST.
func TestTicketComposer(t *testing.T) {
	h := newHarness(t)
	h.seedTicket(t, "Composer", nil)
	body := h.get(t, "/tickets/1", true).Body.String()

	for _, want := range []string{
		`<form class="card comment-form" method="post" action="/tickets/1/comments" enctype="multipart/form-data">`,
		`<textarea name="body" id="comment-body" hx-preserve="true"`,
		`<input id="comment-internal" type="checkbox" name="internal" value="1" hx-preserve="true">`,
		`Internal comment`,
		`<label class="comment-dropzone" for="comment-attachments">`,
		`<input type="file" id="comment-attachments" name="attachments" multiple`,
		`<span class="comment-dropzone-files" data-dropzone-files></span>`,
		`>Reply</button>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("composer must contain %q, got: %s", want, body)
		}
	}
	for _, absent := range []string{`>Add comment</button>`, `Add comment</h2>`, `hx-post="/tickets/1/comments"`} {
		if strings.Contains(body, absent) {
			t.Errorf("composer must not keep %q, got: %s", absent, body)
		}
	}
}

// TestTicketEditUpdatesPriorityAndAudits proves POST /tickets/{id}/edit
// updates the editable fields (title, priority), appends one audit event per
// change, and redirects. The category is immutable after creation — a forged
// category_id in the POST is ignored (stored category unchanged, no category
// audit), exactly like the description.
func TestTicketEditUpdatesPriorityAndAudits(t *testing.T) {
	h := newHarness(t)
	h.seedTicket(t, "Login page down", nil)
	support, err := h.categories.Create(t.Context(), "Support")
	if err != nil {
		t.Fatalf("create category: %v", err)
	}

	form := url.Values{
		"title":       {"Login page is back"},
		"description": {"Fixed the 500"},
		"category_id": {strconv.FormatInt(support.ID, 10)},
		"priority":    {"critical"},
	}
	rec := h.postForm(t, "/tickets/1/edit", form, false)

	wantRedirect(t, rec, http.StatusSeeOther, "/tickets/1")

	view, err := h.tickets.GetByID(t.Context(), *h.admin, 1)
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	if view.Ticket.Priority != domain.PriorityCritical {
		t.Errorf("priority = %q, want critical", view.Ticket.Priority)
	}
	if view.Ticket.Title != "Login page is back" {
		t.Errorf("title = %q, want the submitted title", view.Ticket.Title)
	}
	// The description and the category are immutable after creation: the
	// forged form fields are ignored and the stored values survive.
	if view.Ticket.Description != "Test description" {
		t.Errorf("description must stay immutable, got %q", view.Ticket.Description)
	}
	if view.Ticket.CategoryID != h.bugCategory.ID {
		t.Errorf("category must stay immutable, got %d want %d", view.Ticket.CategoryID, h.bugCategory.ID)
	}
	var fields []string
	for _, ev := range view.AuditEvents {
		if ev.Field != nil {
			fields = append(fields, *ev.Field)
		}
	}
	joined := strings.Join(fields, ",")
	for _, field := range []string{"title", "priority"} {
		if !strings.Contains(joined, field) {
			t.Errorf("audit must record %s change, got %v", field, fields)
		}
	}
	for _, immutable := range []string{"category", "description"} {
		if strings.Contains(joined, immutable) {
			t.Errorf("audit must not record a %s change (immutable field), got %v", immutable, fields)
		}
	}
}

// TestTicketEditInvalidPriority422 proves an unsupported priority on edit is
// rejected 422 with no changes applied.
func TestTicketEditInvalidPriority422(t *testing.T) {
	h := newHarness(t)
	h.seedTicket(t, "Login page down", nil)

	form := url.Values{"title": {"Login page down"}, "description": {""}, "category_id": {"1"}, "priority": {"urgent"}}
	rec := h.postForm(t, "/tickets/1/edit", form, false)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), domain.ErrMsgInvalidPriority) {
		t.Errorf("re-render must show %q, got: %s", domain.ErrMsgInvalidPriority, rec.Body.String())
	}
	view, err := h.tickets.GetByID(t.Context(), *h.admin, 1)
	if err != nil || view.Ticket.Priority != domain.PriorityMedium || view.Ticket.Title != "Login page down" {
		t.Errorf("rejected edit must change nothing (title=%q priority=%q err=%v)", view.Ticket.Title, view.Ticket.Priority, err)
	}
}

// TestTicketAssignClearsAssignment proves clearing the assignment through
// the assign form works (the S4 replacement for the old edit-form unassign).
func TestTicketEditUnassign(t *testing.T) {
	h := newHarness(t)
	beto := h.createUser(t, "Beto", "beto@example.com", "s3cret-pass-12")
	h.seedTicket(t, "Login page down", nil)
	// Assign beto through the assign route, then unassign through it again.
	form := url.Values{
		"user_id": {strconv.FormatInt(beto.ID, 10)},
	}
	if rec := h.postForm(t, "/tickets/1/assign", form, false); rec.Code != http.StatusSeeOther {
		t.Fatalf("assign status = %d", rec.Code)
	}
	view, err := h.tickets.GetByID(t.Context(), *h.admin, 1)
	if err != nil || view.AssignedUser == nil || view.AssignedUser.ID != beto.ID {
		t.Fatalf("assignment failed: %+v err=%v", view.AssignedUser, err)
	}

	clearForm := url.Values{"user_id": {""}}
	rec := h.postForm(t, "/tickets/1/assign", clearForm, false)
	wantRedirect(t, rec, http.StatusSeeOther, "/tickets/1")

	view, err = h.tickets.GetByID(t.Context(), *h.admin, 1)
	if err != nil || view.AssignedUser != nil {
		t.Errorf("unassign failed: assigned=%+v err=%v", view.AssignedUser, err)
	}
}

// TestTicketEditHXFragment proves the HX edit path returns the updated
// ticket_detail fragment.
func TestTicketEditHXFragment(t *testing.T) {
	h := newHarness(t)
	h.seedTicket(t, "Login page down", nil)

	form := url.Values{"title": {"Login page restored"}, "description": {"Fixed"}, "category_id": {"1"}, "priority": {"high"}}
	rec := h.postForm(t, "/tickets/1/edit", form, true)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "<!DOCTYPE html>") {
		t.Errorf("HX edit must return the fragment, got: %s", body)
	}
	if !strings.Contains(body, `id="ticket-priority"`) || !strings.Contains(body, "High") {
		t.Errorf("fragment must show the updated priority, got: %s", body)
	}
}

// TestTicketEditAppliesOnlyCarriedFields proves POST /tickets/{id}/edit
// applies ONLY the fields the request carries: a priority-only request leaves
// the title (and its audit trail) untouched, and a title-only request leaves
// the priority untouched (#232).
func TestTicketEditAppliesOnlyCarriedFields(t *testing.T) {
	cases := []struct {
		name         string
		form         url.Values
		wantTitle    string
		wantPriority domain.Priority
		wantField    string
		dropField    string
	}{
		{
			name:         "priority only",
			form:         url.Values{"priority": {"high"}},
			wantTitle:    "Login page down",
			wantPriority: domain.PriorityHigh,
			wantField:    "priority",
			dropField:    "title",
		},
		{
			name:         "title only",
			form:         url.Values{"title": {"Login page restored"}},
			wantTitle:    "Login page restored",
			wantPriority: domain.PriorityMedium,
			wantField:    "title",
			dropField:    "priority",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.seedTicket(t, "Login page down", nil)

			rec := h.postForm(t, "/tickets/1/edit", tc.form, true)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			if strings.Contains(rec.Body.String(), "<!DOCTYPE html>") {
				t.Errorf("HX edit must return the fragment, got: %s", rec.Body.String())
			}

			view, err := h.tickets.GetByID(t.Context(), *h.admin, 1)
			if err != nil {
				t.Fatalf("view: %v", err)
			}
			if view.Ticket.Title != tc.wantTitle {
				t.Errorf("title = %q, want %q", view.Ticket.Title, tc.wantTitle)
			}
			if view.Ticket.Priority != tc.wantPriority {
				t.Errorf("priority = %q, want %q", view.Ticket.Priority, tc.wantPriority)
			}
			changed := map[string]int{}
			for _, ev := range view.AuditEvents {
				if ev.Field != nil {
					changed[*ev.Field]++
				}
			}
			if changed[tc.wantField] != 1 {
				t.Errorf("audit must record exactly one %s change, got %v", tc.wantField, changed)
			}
			if changed[tc.dropField] != 0 {
				t.Errorf("audit must record no %s change (field not carried), got %v", tc.dropField, changed)
			}
		})
	}
}

// TestTicketEditRejectsRequestCarryingNeitherField proves an edit request that
// carries no editable field is rejected through the inline error path as a
// mapped ValidationError (422) instead of being reported as a successful
// no-op, and writes nothing.
func TestTicketEditRejectsRequestCarryingNeitherField(t *testing.T) {
	h := newHarness(t)
	h.seedTicket(t, "Login page down", nil)

	rec := h.postForm(t, "/tickets/1/edit", url.Values{"description": {"forged"}, "category_id": {"1"}}, true)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "provide a title or a priority") {
		t.Errorf("re-render must show the mapped validation message, got: %s", body)
	}
	if strings.Contains(body, "<!DOCTYPE html>") {
		t.Errorf("rejection must render the fragment, not a full page, got: %s", body)
	}
	view, err := h.tickets.GetByID(t.Context(), *h.admin, 1)
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	if view.Ticket.Title != "Login page down" || view.Ticket.Priority != domain.PriorityMedium {
		t.Errorf("rejected edit must change nothing (title=%q priority=%q)", view.Ticket.Title, view.Ticket.Priority)
	}
	if len(view.AuditEvents) != 1 { // the creation event only
		t.Errorf("rejected edit must append no audit event, got %d events", len(view.AuditEvents))
	}
}

// TestTicketEditErrorRerenderPreservesUncarriedValues proves change 2: an
// error re-render for a priority-only request keeps the persisted title in the
// #ticket-title input and keeps the assigned user selected, instead of
// blanking the input and resetting the selector to "Unassigned".
func TestTicketEditErrorRerenderPreservesUncarriedValues(t *testing.T) {
	h := newHarness(t)
	beto := h.createUser(t, "Beto", "beto@example.com", "s3cret-pass-12")
	h.seedTicket(t, "Login page down", nil)
	h.assignTicket(t, 1, beto.ID)

	rec := h.postForm(t, "/tickets/1/edit", url.Values{"priority": {"urgent"}}, true)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, domain.ErrMsgInvalidPriority) {
		t.Errorf("re-render must show %q, got: %s", domain.ErrMsgInvalidPriority, body)
	}
	if !strings.Contains(body, `id="ticket-title" name="title" value="Login page down"`) {
		t.Errorf("error re-render must keep the persisted title, got: %s", body)
	}
	selected := `name="user_id" value="` + strconv.FormatInt(beto.ID, 10) + `" aria-selected="true"`
	if !strings.Contains(body, selected) {
		t.Errorf("error re-render must keep the assigned user marked, want %q, got: %s", selected, body)
	}
}

// TestTicketEditEmptyTitleStillRejected proves change 1 did not soften the
// existing rule: a request that DOES carry an empty title is still rejected as
// a title validation error.
func TestTicketEditEmptyTitleStillRejected(t *testing.T) {
	h := newHarness(t)
	h.seedTicket(t, "Login page down", nil)

	rec := h.postForm(t, "/tickets/1/edit", url.Values{"title": {""}, "priority": {"high"}}, true)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), domain.ErrMsgTitleRequired) {
		t.Errorf("re-render must show %q, got: %s", domain.ErrMsgTitleRequired, rec.Body.String())
	}
	view, err := h.tickets.GetByID(t.Context(), *h.admin, 1)
	if err != nil || view.Ticket.Title != "Login page down" || view.Ticket.Priority != domain.PriorityMedium {
		t.Errorf("rejected blank title must change nothing (title=%q priority=%q err=%v)", view.Ticket.Title, view.Ticket.Priority, err)
	}
}

// --- #232: rendered detail controls require an explicit Apply -------------

// renderedFormBlock returns the first <form>...</form> block in body whose
// opening tag contains marker. It walks back from marker to the nearest
// "<form" so the returned block is the whole form, and fails when the marker
// is absent or its form is unterminated.
func renderedFormBlock(t *testing.T, body, marker string) string {
	t.Helper()
	at := strings.Index(body, marker)
	if at < 0 {
		t.Fatalf("rendered fragment must contain %q, got: %s", marker, body)
	}
	open := strings.LastIndex(body[:at], "<form")
	if open < 0 {
		t.Fatalf("%q is not inside a <form> block, got: %s", marker, body)
	}
	rest := body[open:]
	end := strings.Index(rest, "</form>")
	if end < 0 {
		t.Fatalf("form containing %q has no closing tag, got: %s", marker, body)
	}
	return rest[:end+len("</form>")]
}

var htmlNameAttrRE = regexp.MustCompile(`name="([^"]+)"`)

// formFieldNames returns the name attributes of every control in a rendered
// form block, in document order, so a test can assert exactly which fields the
// shipped markup offers — a re-added hidden sibling shows up here, and so does
// a picker whose options each carry the one field the form posts.
func formFieldNames(form string) []string {
	matches := htmlNameAttrRE.FindAllStringSubmatch(form, -1)
	names := make([]string, 0, len(matches))
	for _, m := range matches {
		names = append(names, m[1])
	}
	return names
}

// TestTicketDetailEditControlsRequireExplicitSubmit proves the rendered detail
// fragment no longer mutates on `change`: no `onchange`, `hx-trigger`, or
// external `form=` association survives, each mutating form carries ONLY its
// own field, and the priority, assignment and transition forms each expose an
// explicit submit button.
//
// The `requestSubmit` ban this test used to carry held while no script needed
// to submit a form programmatically. The title guard introduced the one
// legitimate use: a discard or a consented save submits the pending form from
// a dialog button, which is an explicit act, never a change-driven autosave.
//
// The `onchange` ban proves the change-alone contract at the attribute level,
// and the `hx-trigger` ban closes the equivalent vacuity gap: an htmx
// `hx-trigger="change"` autosave carries no `onchange` attribute and would
// otherwise pass every rendered-markup assertion. The `form=` ban closes a
// second gap: a hidden sibling associated with a form from OUTSIDE the block
// renderedFormBlock scans would be submitted by a browser but excluded from
// the derived POST body, so the two would silently disagree.
func TestTicketDetailEditControlsRequireExplicitSubmit(t *testing.T) {
	h := newHarness(t)
	h.seedTicket(t, "Login page down", nil)
	body := h.get(t, "/tickets/1", true).Body.String()

	for _, banned := range []string{"onchange", "hx-trigger", `form="`} {
		if strings.Contains(body, banned) {
			t.Errorf("detail fragment must not contain %q, got: %s", banned, body)
		}
	}

	titleForm := renderedFormBlock(t, body, `id="ticket-title"`)
	if strings.Contains(titleForm, `name="priority"`) {
		t.Errorf("title form must not carry a priority field, got: %s", titleForm)
	}
	priorityForm := renderedFormBlock(t, body, `id="ticket-priority"`)
	if strings.Contains(priorityForm, `name="title"`) {
		t.Errorf("priority form must not carry a hidden title field, got: %s", priorityForm)
	}

	// Issue #386 (S5): the picker options ARE the submit buttons. Each form
	// still ships an explicit `type="submit"` button per value, so a pick is a
	// native POST and never a change-driven autosave.
	for _, tc := range []struct{ name, marker string }{
		{"priority edit", `id="ticket-priority"`},
		{"assignment", `id="assign-user"`},
	} {
		block := renderedFormBlock(t, body, tc.marker)
		if !strings.Contains(block, `<button type="submit" role="option"`) {
			t.Errorf("%s form must contain the option submit buttons, got: %s", tc.name, block)
		}
		if strings.Contains(block, ">Apply</button>") {
			t.Errorf("%s form must not keep an Apply button (S5), got: %s", tc.name, block)
		}
	}

	// The header transition actions are forms with their own explicit submit
	// button, one per reachable target.
	transition := renderedFormBlock(t, body, `value="in_progress"`)
	if !strings.Contains(transition, `action="/tickets/1/transition"`) ||
		!strings.Contains(transition, `<button class="btn primary" type="submit">Start</button>`) {
		t.Errorf("header transition form must post the transition with an explicit submit, got: %s", transition)
	}
}

// --- #232: the inline title guard against a sibling Apply ------------------

// TestTicketDetailTitleFormAndGuard proves the rendered detail ships the
// explicit title Save button (hidden until the title is dirty) and the title
// guard dialog, and that the title form still exposes exactly its own field so
// a sibling Apply cannot ship the title by accident (#232).
func TestTicketDetailTitleFormAndGuard(t *testing.T) {
	h := newHarness(t)
	h.seedTicket(t, "Login page down", nil)
	body := h.get(t, "/tickets/1", true).Body.String()

	titleForm := renderedFormBlock(t, body, `id="ticket-title"`)
	if names := formFieldNames(titleForm); len(names) != 1 || names[0] != "title" {
		t.Fatalf("title form must expose exactly the title field, got %v (markup: %s)", names, titleForm)
	}
	if !strings.Contains(titleForm, `class="btn small title-save" type="submit" hidden>Save</button>`) {
		t.Errorf("title form must render the hidden Save button, got: %s", titleForm)
	}

	dialogAt := strings.Index(body, `id="ticket-title-dialog"`)
	if dialogAt < 0 {
		t.Fatalf("detail fragment must render #ticket-title-dialog, got: %s", body)
	}
	dialogEnd := strings.Index(body[dialogAt:], "</dialog>")
	if dialogEnd < 0 {
		t.Fatalf("title guard dialog must be terminated, got: %s", body[dialogAt:])
	}
	dialog := body[dialogAt : dialogAt+dialogEnd]
	for _, hook := range []string{"data-title-discard", "data-title-save-continue"} {
		if !strings.Contains(dialog, hook) {
			t.Errorf("title guard dialog must carry %q, got: %s", hook, dialog)
		}
	}
}

// TestTicketDetailPriorityFormPostsOnlyItsOwnFields derives its POST body from
// the RENDERED priority picker and submits it: the stored title and its audit
// trail stay untouched, proving picking a priority can never ship a sibling
// field again. Issue #386 (S5) keeps #232's contract with the picker: the only
// named controls in the form are the option submit buttons, one per priority,
// and a pick sends exactly the chosen value.
func TestTicketDetailPriorityFormPostsOnlyItsOwnFields(t *testing.T) {
	h := newHarness(t)
	h.seedTicket(t, "Login page down", nil)
	body := h.get(t, "/tickets/1", true).Body.String()
	block := renderedFormBlock(t, body, `id="ticket-priority"`)

	if names := formFieldNames(block); len(names) != len(listPriorities) {
		t.Fatalf("priority form must expose one control per priority, got %v (markup: %s)", names, block)
	}
	for _, name := range formFieldNames(block) {
		if name != "priority" {
			t.Fatalf("priority form must only carry its own field, got %q (markup: %s)", name, block)
		}
	}
	if !strings.Contains(block, `name="priority" value="critical"`) {
		t.Fatalf("priority form must carry one submit option per priority, got: %s", block)
	}

	form := url.Values{"priority": {"critical"}}
	rec := h.postForm(t, "/tickets/1/edit", form, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("priority-only edit status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	view, err := h.tickets.GetByID(t.Context(), *h.admin, 1)
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	if view.Ticket.Title != "Login page down" {
		t.Errorf("priority-only edit changed the title to %q", view.Ticket.Title)
	}
	if view.Ticket.Priority != domain.PriorityCritical {
		t.Errorf("priority = %q, want critical", view.Ticket.Priority)
	}
	for _, ev := range view.AuditEvents {
		if ev.Field != nil && *ev.Field == "title" {
			t.Errorf("priority-only edit must not append a title audit event, got: %+v", ev)
		}
	}
}

// TestTicketDetailReasonDialog proves a reason-requiring transition is behind a
// dialog instead of an inline field (issue #386, S1): the header button opens
// it, the form posts the SAME `to` and the SAME `reason` field the inline
// control used, the reason stays required, and the server validation is
// untouched (a blank reason is still the same 422).
func TestTicketDetailReasonDialog(t *testing.T) {
	h := newHarness(t)
	tkt := h.seedTicket(t, "Login page down", nil)
	h.makeLegacy(t, tkt.ID)
	h.seedTransition(t, tkt.ID, domain.StateInProgress, "")
	h.seedTransition(t, tkt.ID, domain.StateResolved, "")
	body := h.get(t, "/tickets/1", true).Body.String()

	// The header button opens the dialog and never submits on its own.
	for _, want := range []string{
		`<button class="btn secondary" type="button" data-transition-open="in_progress" aria-haspopup="dialog" aria-controls="ticket-transition-dialog">Reopen</button>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("reason-requiring transition must render its dialog opener, missing %q, got: %s", want, body)
		}
	}

	dialogAt := strings.Index(body, `<dialog id="ticket-transition-dialog"`)
	if dialogAt < 0 {
		t.Fatalf("detail must render #ticket-transition-dialog, got: %s", body)
	}
	dialog := body[dialogAt:]
	dialog = dialog[:strings.Index(dialog, "</dialog>")]
	for _, want := range []string{
		`aria-labelledby="ticket-transition-dialog-title"`,
		`action="/tickets/1/transition"`,
		`hx-post="/tickets/1/transition"`,
		`name="to" value="in_progress"`,
		`id="state-reason"`,
		`name="reason"`,
		`required`,
		`data-transition-cancel`,
	} {
		if !strings.Contains(dialog, want) {
			t.Errorf("reason dialog must contain %q, got: %s", want, dialog)
		}
	}
	if strings.Contains(body, `id="state-reason-field"`) || strings.Contains(body, `id="ticket-state"`) {
		t.Errorf("the inline Move-to select and its reason field must be gone, got: %s", body)
	}

	// The server validation is unchanged: a closed ticket reopened without a
	// reason is still the same 422.
	h.seedTransition(t, tkt.ID, domain.StateClosed, "")
	rec := h.postForm(t, "/tickets/1/transition", url.Values{"to": {"in_progress"}}, false)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), domain.ErrMsgReopenReasonRequired) {
		t.Errorf("re-render must show %q, got: %s", domain.ErrMsgReopenReasonRequired, rec.Body.String())
	}
}

// renderedTransitionAction is the exact header form one no-reason transition
// renders: a native POST of `to` to the existing route through HTMX, with the
// catalogue verb and the weight S1 assigns it.
func renderedTransitionAction(id int64, to domain.State, label, class string) string {
	action := "/tickets/" + strconv.FormatInt(id, 10) + "/transition"
	return `<form class="detail-action" method="post" action="` + action + `" hx-post="` + action +
		`" hx-target="#ticket-detail" hx-swap="outerHTML"><input type="hidden" name="to" value="` +
		string(to) + `"><button class="btn ` + class + `" type="submit">` + label + `</button></form>`
}

// renderedReasonOpener is the header button that opens the reason dialog: it
// never submits on its own, so a reopen reason is asked for before the act.
func renderedReasonOpener(to domain.State, label, class string) string {
	return `<button class="btn ` + class + `" type="button" data-transition-open="` + string(to) +
		`" aria-haspopup="dialog" aria-controls="ticket-transition-dialog">` + label + `</button>`
}

// TestTicketDetailHeaderTransitionActions pins S1's header action set against
// the state machine and both staff audiences: one button per transition the
// actor may take today (exactly filteredNext for a capable actor), labelled by
// the catalogue verb, the forward move primary, Cancel a quiet danger button,
// the rest secondary, and a reason-requiring transition behind the dialog.
func TestTicketDetailHeaderTransitionActions(t *testing.T) {
	for _, tc := range []struct {
		state domain.State
		path  []domain.State
		want  []string
	}{
		{domain.StateNew, nil, []string{
			renderedTransitionAction(1, domain.StateInProgress, "Start", "primary"),
			renderedTransitionAction(1, domain.StateResolved, "Resolve", "secondary"),
			renderedTransitionAction(1, domain.StateCancelled, "Cancel", "danger"),
		}},
		{domain.StateInProgress, []domain.State{domain.StateInProgress}, []string{
			renderedTransitionAction(1, domain.StateResolved, "Resolve", "primary"),
			renderedTransitionAction(1, domain.StateCancelled, "Cancel", "danger"),
		}},
		{domain.StateResolved, []domain.State{domain.StateInProgress, domain.StateResolved}, []string{
			renderedTransitionAction(1, domain.StateClosed, "Close", "primary"),
			renderedReasonOpener(domain.StateInProgress, "Reopen", "secondary"),
		}},
		{domain.StateClosed, []domain.State{domain.StateInProgress, domain.StateResolved, domain.StateClosed}, []string{
			renderedReasonOpener(domain.StateInProgress, "Reopen", "primary"),
		}},
		{domain.StateCancelled, []domain.State{domain.StateCancelled}, nil},
	} {
		t.Run(string(tc.state), func(t *testing.T) {
			h := newHarness(t)
			tkt := h.seedTicket(t, "Header actions", nil)
			h.makeLegacy(t, tkt.ID)
			for _, step := range tc.path {
				h.seedTransition(t, tkt.ID, step, "")
			}

			body := h.get(t, "/tickets/1", true).Body.String()
			for _, want := range tc.want {
				if !strings.Contains(body, want) {
					t.Errorf("header must render the action %q, got: %s", want, body)
				}
			}
			// The inline select and its Apply are gone for every state.
			for _, absent := range []string{`id="ticket-state"`, `id="state-apply"`, "Move to"} {
				if strings.Contains(body, absent) {
					t.Errorf("the Move-to select must be gone, still found %q, got: %s", absent, body)
				}
			}
			// A terminal ticket offers no action at all.
			if len(tc.want) == 0 {
				for _, absent := range []string{`data-transition-open=`, `action="/tickets/1/transition"`} {
					if strings.Contains(body, absent) {
						t.Errorf("a terminal ticket must offer no transition, still found %q, got: %s", absent, body)
					}
				}
			}
		})
	}

	// A requester holds no transition capability: no header transition at all.
	t.Run("requester", func(t *testing.T) {
		h := newHarness(t)
		requester := seedUserRole(t, h.store, "Rosa", "rosa-actions@tkt.test", domain.RoleUser)
		sess := seedSession(t, h.store, requester.ID)
		tkt := createTicketFor(t, h, requester, "Printer jam")
		body := getDetailAs(t, h, ticketPath(tkt.ID), sess.ID, true).Body.String()
		for _, absent := range []string{`data-transition-open=`, `/transition"`} {
			if strings.Contains(body, absent) {
				t.Errorf("a requester must not be offered a transition, still found %q, got: %s", absent, body)
			}
		}
	})

	// An assigned agent keeps the same action set its capability grants.
	t.Run("assigned agent", func(t *testing.T) {
		h := newHarness(t)
		agent := h.createUser(t, "Agent", "agent-actions@tkt.test", "s3cret-pass-12")
		tkt := h.seedTicket(t, "Header actions", nil)
		h.assignTicket(t, tkt.ID, agent.ID)
		session := h.loginCookie(t, agent.Email, "s3cret-pass-12")
		if session == "" {
			t.Fatal("agent login must succeed")
		}
		req := httptest.NewRequest(http.MethodGet, ticketPath(tkt.ID), nil)
		req.Header.Set("Cookie", sessionCookie+"="+session)
		rec := httptest.NewRecorder()
		h.mw.Wrap(h.mux).ServeHTTP(rec, req)
		body := rec.Body.String()
		for _, want := range []string{
			renderedTransitionAction(tkt.ID, domain.StateInProgress, "Start", "primary"),
			renderedTransitionAction(tkt.ID, domain.StateResolved, "Resolve", "secondary"),
			renderedTransitionAction(tkt.ID, domain.StateCancelled, "Cancel", "danger"),
		} {
			if !strings.Contains(body, want) {
				t.Errorf("assigned agent header must render %q, got: %s", want, body)
			}
		}
	})
}

// TestTicketDetailAssignToMe proves S1's header shortcut posts the EXISTING
// assign route for the acting user, and only where it can succeed: an open,
// unassigned ticket. The workflow claim keeps its own route and wins when it
// applies, so one ticket never shows two "Assign to me" actions.
func TestTicketDetailAssignToMe(t *testing.T) {
	h := newHarness(t)
	tkt := h.seedTicket(t, "Shortcut", nil)
	self := strconv.FormatInt(h.admin.ID, 10)
	want := `<form class="detail-action" method="post" action="/tickets/1/assign" hx-post="/tickets/1/assign" hx-target="#ticket-detail" hx-swap="outerHTML"><input type="hidden" name="user_id" value="` + self + `"><button class="btn" type="submit">Assign to me</button></form>`
	body := h.get(t, "/tickets/1", true).Body.String()
	if !strings.Contains(body, want) {
		t.Fatalf("an unassigned open ticket must offer Assign to me for the acting user, want %q, got: %s", want, body)
	}

	// Once assigned, the shortcut is gone (the picker is the way to change it).
	agent := h.createUser(t, "Agent", "agent-shortcut@tkt.test", "s3cret-pass-12")
	h.assignTicket(t, tkt.ID, agent.ID)
	if body := h.get(t, "/tickets/1", true).Body.String(); strings.Contains(body, ">Assign to me</button>") {
		t.Errorf("an assigned ticket must not offer Assign to me, got: %s", body)
	}

	// A requester never gets the staff shortcut.
	requester := seedUserRole(t, h.store, "Rosa", "rosa-shortcut@tkt.test", domain.RoleUser)
	sess := seedSession(t, h.store, requester.ID)
	ownTicket := createTicketFor(t, h, requester, "Printer jam")
	if body := getDetailAs(t, h, ticketPath(ownTicket.ID), sess.ID, true).Body.String(); strings.Contains(body, "Assign to me") {
		t.Errorf("a requester must not be offered Assign to me, got: %s", body)
	}
}

// TestTicketDetailOverflowActions proves S1's overflow menu holds the hold
// controls, reusing the existing hold dialog and the existing resume form, and
// that a terminal or requester ticket offers neither.
func TestTicketDetailOverflowActions(t *testing.T) {
	h := newHarness(t)
	tkt := h.seedTicket(t, "Overflow", nil)
	h.seedTransition(t, tkt.ID, domain.StateInProgress, "")

	body := h.get(t, "/tickets/1", true).Body.String()
	for _, want := range []string{
		`id="ticket-actions-overflow"`,
		`aria-label="More actions"`,
		`data-hold-open`,
		`aria-controls="ticket-hold-dialog"`,
		`id="ticket-hold-dialog"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("an open ticket must carry the hold control in its overflow menu, missing %q, got: %s", want, body)
		}
	}
	if strings.Contains(body, `id="ticket-actions-overflow-more"`) {
		t.Errorf("no extra ids are needed in the overflow menu, got: %s", body)
	}
	// The hold trigger left the SLA section: the panel keeps its content.
	if strings.Contains(body, `class="sla-hold-row"`) {
		t.Errorf("the hold trigger must live in the header overflow, not the SLA section, got: %s", body)
	}

	// A held ticket swaps the hold item for the resume form.
	holdViaService(t, h, tkt.ID, "waiting for the VPN logs")
	held := h.get(t, "/tickets/1", true).Body.String()
	for _, want := range []string{
		`id="ticket-actions-overflow"`,
		`<form class="hold-resume" method="post" action="/tickets/1/resume" hx-post="/tickets/1/resume" hx-target="#ticket-detail" hx-swap="outerHTML"><button class="btn" role="menuitem" type="submit">Resume</button></form>`,
	} {
		if !strings.Contains(held, want) {
			t.Errorf("a held ticket must offer Resume in the overflow, missing %q, got: %s", want, held)
		}
	}
	if strings.Contains(held, `id="hold-reason"`) {
		t.Errorf("a held ticket must not offer the hold dialog, got: %s", held)
	}
}

// TestTicketDetailOverflowHiddenForRequesterAndTerminal proves the overflow
// menu never renders a control the actor or the state cannot use.
func TestTicketDetailOverflowHiddenForRequesterAndTerminal(t *testing.T) {
	h := newHarness(t)
	requester := seedUserRole(t, h.store, "Rosa", "rosa-overflow@tkt.test", domain.RoleUser)
	sess := seedSession(t, h.store, requester.ID)
	ownTicket := createTicketFor(t, h, requester, "Printer jam")
	body := getDetailAs(t, h, ticketPath(ownTicket.ID), sess.ID, true).Body.String()
	for _, absent := range []string{`id="ticket-actions-overflow"`, "Put on hold"} {
		if strings.Contains(body, absent) {
			t.Errorf("a requester must not get the overflow menu (%q), got: %s", absent, body)
		}
	}

	legacy := h.seedTicket(t, "Terminal", nil)
	h.makeLegacy(t, legacy.ID)
	h.seedTransition(t, legacy.ID, domain.StateCancelled, "")
	cancelled := h.get(t, "/tickets/"+strconv.FormatInt(legacy.ID, 10), true).Body.String()
	for _, absent := range []string{`id="ticket-actions-overflow"`, "Put on hold", `class="hold-resume"`} {
		if strings.Contains(cancelled, absent) {
			t.Errorf("a terminal ticket must not get the overflow menu (%q), got: %s", absent, cancelled)
		}
	}
}

// TestTicketDetailHeaderMetaLine proves S1's one meta line: the requester, the
// relative creation reading with the absolute instant in its title, and the
// category path. A zero Now snapshot (every golden fixture) renders the
// absolute instant instead, so fixtures stay frozen.
func TestTicketDetailHeaderMetaLine(t *testing.T) {
	h := newHarness(t)
	tkt := h.seedTicket(t, "Meta line", nil)
	view, err := h.tickets.GetByID(t.Context(), *h.admin, tkt.ID)
	if err != nil {
		t.Fatalf("view: %v", err)
	}

	body := h.get(t, "/tickets/1", true).Body.String()
	for _, want := range []string{
		`<p class="detail-meta">Opened by <span class="detail-meta-requester">` + view.Ticket.RequesterName + `</span> · <time datetime="` + formatDatetime(view.Ticket.CreatedAt) + `" title="` + formatDisplayTime(view.Ticket.CreatedAt) + `">`,
		`</time> · <span class="detail-meta-category">Bugs</span></p>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("header meta line must contain %q, got: %s", want, body)
		}
	}
	// The handler stamps the render's clock, so the reading is relative.
	if !strings.Contains(body, "just now") {
		t.Errorf("a stamped clock must render the creation reading relatively, got: %s", body)
	}
}

// TestTicketDetailListboxProperties pins S5's property panel: Requester,
// Assignee, Priority, Category and Desk, the two editable ones as listbox
// buttons whose options are submit buttons of the existing forms (no Apply,
// no onchange), the assignee picker filtered by a search field, and an
// already-assigned inactive user still visible.
func TestTicketDetailListboxProperties(t *testing.T) {
	h := newHarness(t)
	agent := h.createUser(t, "Ann Agent", "ann-listbox@tkt.test", "s3cret-pass-12")
	tkt := h.seedTicket(t, "Listbox", nil)
	h.assignTicket(t, tkt.ID, agent.ID)
	// Deactivate the assignee: the assigned person stays visible (today's rule).
	agent.Active = false
	if err := h.store.UserStore().Update(t.Context(), agent); err != nil {
		t.Fatalf("deactivate assignee: %v", err)
	}

	body := h.get(t, "/tickets/1", true).Body.String()
	for _, want := range []string{
		`<details class="detail-properties" id="detail-properties" open>`,
		`<summary class="detail-properties-summary"><span>Properties</span>`,
		`<span class="prop-label">Requester</span>`,
		`<span class="prop-label">Assignee</span>`,
		`<span class="prop-label">Priority</span>`,
		`<span class="prop-label">Category</span>`,
		`<span class="prop-label">Desk</span>`,
		`<div class="prop-heading">Details</div>`,
		// The two pickers: a button with the listbox affordances.
		`<button id="ticket-priority" type="button" class="listbox-button" aria-haspopup="listbox" aria-expanded="false" aria-controls="ticket-priority-options">`,
		`<button id="assign-user" type="button" class="listbox-button" aria-haspopup="listbox" aria-expanded="false" aria-controls="assign-user-options">`,
		// Options are submit buttons of the existing forms.
		`<button type="submit" role="option" class="listbox-option" name="priority" value="critical" aria-selected="false">`,
		`<button type="submit" role="option" class="listbox-option" name="priority" value="medium" aria-selected="true">`,
		`<button type="submit" role="option" class="listbox-option" name="user_id" value="" aria-selected="false">`,
		`<button type="submit" role="option" class="listbox-option" name="user_id" value="` + strconv.FormatInt(agent.ID, 10) + `" aria-selected="true">`,
		// The inactive assignee stays listed and labelled.
		`<span class="listbox-inactive">(inactive)</span>`,
		// The assignee popover filters with its own search field.
		`<input type="search" class="listbox-search" placeholder="Filter assignees" aria-label="Filter assignees" autocomplete="off">`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("listbox properties must contain %q, got: %s", want, body)
		}
	}
	// Issue #386 (S5/L3): the picker still saves with scripting off, because its
	// options are submit buttons. The popover is closed by the stylesheet and
	// opened by the script, so the no-script fallback can render the same
	// buttons inline without a second copy in the markup.
	for _, want := range []string{
		`<div class="listbox-popover" id="ticket-priority-options">`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the picker popover must render closed without a duplicate list, missing %q, got: %s", want, body)
		}
	}
	for _, absent := range []string{"onchange", `hx-trigger`, `>Apply</button>`} {
		if strings.Contains(body, absent) {
			t.Errorf("property controls must keep #232's explicit act, still found %q, got: %s", absent, body)
		}
	}
}

// TestTicketDetailPropertiesReadOnlyForRequester proves S6: the same panel
// without staff controls renders the stored values read-only under today's
// ids, so nothing the server will refuse is offered.
func TestTicketDetailPropertiesReadOnlyForRequester(t *testing.T) {
	h := newHarness(t)
	requester := seedUserRole(t, h.store, "Rosa", "rosa-readonly@tkt.test", domain.RoleUser)
	sess := seedSession(t, h.store, requester.ID)
	tkt := createTicketFor(t, h, requester, "Printer jam")

	body := getDetailAs(t, h, ticketPath(tkt.ID), sess.ID, true).Body.String()
	for _, want := range []string{
		`<span id="ticket-priority-value" class="prop-value">Medium</span>`,
		`<span id="assign-user-value" class="prop-value">Unassigned</span>`,
		`<span id="ticket-category-value" class="prop-value">Bugs</span>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("requester properties must keep the read-only value %q, got: %s", want, body)
		}
	}
	for _, absent := range []string{`class="listbox-button"`, `class="listbox-option"`} {
		if strings.Contains(body, absent) {
			t.Errorf("a requester must not get the picker (%q), got: %s", absent, body)
		}
	}
}

// TestTicketEditTimelineResolvesAssignedUserName proves the assignment
// event resolves the assigned user's name on the timeline — the assignment
// now flows through POST /tickets/{id}/assign (S4: the single assignment
// path).
func TestTicketEditTimelineResolvesAssignedUserName(t *testing.T) {
	h := newHarness(t)
	beto := h.createUser(t, "Beto", "beto@example.com", "s3cret-pass-12")
	h.seedTicket(t, "Login page down", nil)

	form := url.Values{
		"user_id": {strconv.FormatInt(beto.ID, 10)},
	}
	rec := h.postForm(t, "/tickets/1/assign", form, false)
	wantRedirect(t, rec, http.StatusSeeOther, "/tickets/1")

	body := h.get(t, "/tickets/1", false).Body.String()
	if !strings.Contains(body, "assigned the ticket to Beto") {
		t.Errorf("assignment event must resolve user names, got: %s", body)
	}
	if strings.Contains(body, "Assigned To · Unassigned → "+strconv.FormatInt(beto.ID, 10)) {
		t.Errorf("assignment event must not expose the user id, got: %s", body)
	}
}

// --- S4: assignment + transition authorization (runtime harness) -----------

// TestTicketAssignInitialHappyPath proves POST /tickets/{id}/assign assigns
// an active agent-plus person to an unassigned ticket WITHOUT a reason and
// records the assignment event with the session actor (spec: "Initial
// assignment without reason").
func TestTicketAssignInitialHappyPath(t *testing.T) {
	h := newHarness(t)
	beto := h.createUser(t, "Beto", "beto@example.com", "s3cret-pass-12")
	h.seedTicket(t, "Login page down", nil)

	rec := h.postForm(t, "/tickets/1/assign", url.Values{"user_id": {strconv.FormatInt(beto.ID, 10)}}, false)
	wantRedirect(t, rec, http.StatusSeeOther, "/tickets/1")

	view, err := h.tickets.GetByID(t.Context(), *h.admin, 1)
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	if view.AssignedUser == nil || view.AssignedUser.ID != beto.ID {
		t.Fatalf("assigned = %+v, want beto", view.AssignedUser)
	}
	// Assignment audit event: session actor id, no reason.
	assignEv := view.AuditEvents[len(view.AuditEvents)-1]
	if assignEv.Reason != nil {
		t.Errorf("initial assignment must record no reason, got %q", *assignEv.Reason)
	}
	if assignEv.ActorUserID == nil || *assignEv.ActorUserID != h.admin.ID {
		t.Errorf("assignment event ActorUserID = %v, want session admin %d", assignEv.ActorUserID, h.admin.ID)
	}
	body := h.get(t, "/tickets/1", false).Body.String()
	if !strings.Contains(body, "assigned the ticket to Beto") {
		t.Errorf("timeline must resolve the assignee name, got: %s", body)
	}
}

// TestTicketAssignReassignRequiresReason proves a reassignment (A → B)
// without a reason is rejected 422 and the assignment stays; with a reason
// it succeeds and the reason is shown in the timeline (approved decision:
// reason required only for reassignment).
func TestTicketAssignReassignRequiresReason(t *testing.T) {
	h := newHarness(t)
	beto := h.createUser(t, "Beto", "beto@example.com", "s3cret-pass-12")
	carla := h.createUser(t, "Carla", "carla@example.com", "s3cret-pass-12")
	h.seedTicket(t, "Login page down", nil)
	if rec := h.postForm(t, "/tickets/1/assign", url.Values{"user_id": {strconv.FormatInt(beto.ID, 10)}}, false); rec.Code != http.StatusSeeOther {
		t.Fatalf("initial assign status = %d, want 303", rec.Code)
	}

	// Reassignment without a reason: 422 + message, assignment unchanged.
	rec := h.postForm(t, "/tickets/1/assign", url.Values{"user_id": {strconv.FormatInt(carla.ID, 10)}}, false)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), domain.ErrMsgReassignReasonRequired) {
		t.Errorf("re-render must show %q, got: %s", domain.ErrMsgReassignReasonRequired, rec.Body.String())
	}
	view, _ := h.tickets.GetByID(t.Context(), *h.admin, 1)
	if view.AssignedUser == nil || view.AssignedUser.ID != beto.ID {
		t.Fatalf("rejected reassignment must keep beto, got %+v", view.AssignedUser)
	}

	// Reassignment with a reason: succeeds, reason rendered in the timeline.
	rec = h.postForm(t, "/tickets/1/assign", url.Values{"user_id": {strconv.FormatInt(carla.ID, 10)}, "reason": {"handoff to second-line"}}, false)
	wantRedirect(t, rec, http.StatusSeeOther, "/tickets/1")
	view, _ = h.tickets.GetByID(t.Context(), *h.admin, 1)
	if view.AssignedUser == nil || view.AssignedUser.ID != carla.ID {
		t.Fatalf("reassigned = %+v, want carla", view.AssignedUser)
	}
	body := h.get(t, "/tickets/1", false).Body.String()
	if !strings.Contains(body, "handoff to second-line") {
		t.Errorf("timeline must render the reassignment reason, got: %s", body)
	}
}

// TestTicketAssignUnassign proves clearing the assignment via the assign
// form (empty user_id) works and is audited (person → unassigned).
func TestTicketAssignUnassign(t *testing.T) {
	h := newHarness(t)
	beto := h.createUser(t, "Beto", "beto@example.com", "s3cret-pass-12")
	h.seedTicket(t, "Login page down", nil)
	if rec := h.postForm(t, "/tickets/1/assign", url.Values{"user_id": {strconv.FormatInt(beto.ID, 10)}}, false); rec.Code != http.StatusSeeOther {
		t.Fatalf("assign status = %d", rec.Code)
	}
	view, err := h.tickets.GetByID(t.Context(), *h.admin, 1)
	if err != nil || view.AssignedUser == nil || view.AssignedUser.ID != beto.ID {
		t.Fatalf("assignment failed: %+v err=%v", view.AssignedUser, err)
	}

	rec := h.postForm(t, "/tickets/1/assign", url.Values{"user_id": {""}}, false)
	wantRedirect(t, rec, http.StatusSeeOther, "/tickets/1")

	view, err = h.tickets.GetByID(t.Context(), *h.admin, 1)
	if err != nil || view.AssignedUser != nil {
		t.Errorf("unassign failed: assigned=%+v err=%v", view.AssignedUser, err)
	}
}

// TestTicketAssignUserRoleDenied proves a user-role actor cannot assign
// (spec: "User role cannot assign") — 422 with the dedicated message, even
// when posting a valid agent-plus target (the capability gate fires before
// any target or ticket logic).
func TestTicketAssignUserRoleDenied(t *testing.T) {
	h := newHarness(t)
	user := seedUserRole(t, h.store, "Ula", "ula@example.com", domain.RoleUser)
	sess := seedSession(t, h.store, user.ID)
	beto := h.createUser(t, "Beto", "beto@example.com", "s3cret-pass-12")
	rec := h.postFormAs(t, "/tickets", url.Values{
		"title":       {"My ticket"},
		"category_id": {strconv.FormatInt(h.bugCategory.ID, 10)},
		"priority":    {"medium"},
	}, sess.ID)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("create status = %d, want 303", rec.Code)
	}

	rec = h.postFormAs(t, "/tickets/1/assign", url.Values{"user_id": {strconv.FormatInt(beto.ID, 10)}}, sess.ID)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), domain.ErrMsgUserRoleCannotAssign) {
		t.Errorf("re-render must show %q, got: %s", domain.ErrMsgUserRoleCannotAssign, rec.Body.String())
	}
	view, err := h.tickets.GetByID(t.Context(), *h.admin, 1)
	if err != nil || view.AssignedUser != nil {
		t.Errorf("denied assign must leave the ticket unassigned, got %+v err=%v", view.AssignedUser, err)
	}
}

// TestTicketAssignTargetUserRoleRejected proves the assignment target must
// be agent-plus: an active user-role account is rejected 422 (spec:
// "Assignment target must be agent-plus").
func TestTicketAssignTargetUserRoleRejected(t *testing.T) {
	h := newHarness(t)
	user := seedUserRole(t, h.store, "Ula", "ula@example.com", domain.RoleUser)
	h.seedTicket(t, "Login page down", nil)

	rec := h.postForm(t, "/tickets/1/assign", url.Values{"user_id": {strconv.FormatInt(user.ID, 10)}}, false)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), domain.ErrMsgAssignTargetRole) {
		t.Errorf("re-render must show %q, got: %s", domain.ErrMsgAssignTargetRole, rec.Body.String())
	}
	view, _ := h.tickets.GetByID(t.Context(), *h.admin, 1)
	if view.AssignedUser != nil {
		t.Errorf("rejected target must leave the ticket unassigned, got %+v", view.AssignedUser)
	}
}

// TestTicketTransitionUserDenied proves a user-role actor gets 403 when
// transitioning their own ticket and the state stays unchanged (spec: "User
// role cannot transition"; design: server-side enforcement before state
// change).
func TestTicketTransitionUserDenied(t *testing.T) {
	h := newHarness(t)
	user := seedUserRole(t, h.store, "Ula", "ula@example.com", domain.RoleUser)
	sess := seedSession(t, h.store, user.ID)

	rec := h.postFormAs(t, "/tickets", url.Values{
		"title":       {"My ticket"},
		"category_id": {strconv.FormatInt(h.bugCategory.ID, 10)},
		"priority":    {"medium"},
	}, sess.ID)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("create status = %d, want 303", rec.Code)
	}

	rec = h.postFormAs(t, "/tickets/1/transition", url.Values{"to": {"in_progress"}}, sess.ID)
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), domain.ErrMsgUserCannotTransition) {
		t.Errorf("re-render must show %q, got: %s", domain.ErrMsgUserCannotTransition, rec.Body.String())
	}
	view, err := h.tickets.GetByID(t.Context(), *h.admin, 1)
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	if view.Ticket.State != domain.StateNew {
		t.Errorf("denied transition must leave state %q, got %q", domain.StateNew, view.Ticket.State)
	}
}

// getDetailAs runs an authenticated GET as an explicit session actor through
// the middleware-wrapped mux (the detail-page counterpart of postFormAs).
func getDetailAs(t *testing.T, h *harness, path, sessionID string, hx bool) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Cookie", sessionCookie+"="+sessionID)
	if hx {
		req.Header.Set("HX-Request", "true")
	}
	rec := httptest.NewRecorder()
	h.mw.Wrap(h.mux).ServeHTTP(rec, req)
	return rec
}

// TestTicketDetailRequesterHidesUnusableMutationControls (issue #263) proves a
// user-role actor viewing their OWN open ticket is never shown a mutation
// control the server will reject: no priority form, no assign form, and no
// transition form. The read-only property values stay visible, and the
// requester's legitimate resolution path (the confirmation panel) is owned by a
// separate section. The server authority is unchanged and remains the
// enforcement point; this test only proves the presentation does not offer an
// action that can only 403.
//
// The capable actor is asserted in the same test so the fix cannot regress the
// agent/admin surface: an admin still gets all three controls on the ticket.
func TestTicketDetailRequesterHidesUnusableMutationControls(t *testing.T) {
	h := newHarness(t)
	user := seedUserRole(t, h.store, "Ula", "ula@example.com", domain.RoleUser)
	sess := seedSession(t, h.store, user.ID)

	rec := h.postFormAs(t, "/tickets", url.Values{
		"title":       {"My ticket"},
		"category_id": {strconv.FormatInt(h.bugCategory.ID, 10)},
		"priority":    {"high"},
	}, sess.ID)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("create status = %d, want 303", rec.Code)
	}

	body := getDetailAs(t, h, "/tickets/1", sess.ID, true).Body.String()
	for _, absent := range []string{`id="ticket-priority"`, `id="assign-user"`, `action="/tickets/1/transition"`} {
		if strings.Contains(body, absent) {
			t.Errorf("a requester's own ticket must not render the unusable control %q, got: %s", absent, body)
		}
	}
	for _, want := range []string{`id="ticket-priority-value"`, `id="assign-user-value"`, `>High<`} {
		if !strings.Contains(body, want) {
			t.Errorf("requester detail must keep the read-only value %q, got: %s", want, body)
		}
	}

	// The capable actor keeps all three controls on the same ticket.
	capable := h.get(t, "/tickets/1", true).Body.String()
	for _, want := range []string{`id="ticket-priority"`, `id="assign-user"`, `action="/tickets/1/transition"`} {
		if !strings.Contains(capable, want) {
			t.Errorf("a capable actor must keep the control %q, got: %s", want, capable)
		}
	}
}

// TestClosedTicketDetailKeepsReopenForCapableActor (issue #263) proves the
// capable-actor half of the closed-ticket contract through the REAL handler:
// a CLOSED ticket still offers an agent/admin the reopen control, because
// CanTransition (CapEditTicket) is deliberately NOT gated on the closed state.
// A naive CanEdit gate — which folds `!closed` into the capability — would
// have removed the only remaining mutation on a closed ticket and destroyed
// the agent reopen. TestClosedTicketDetailReadOnly only proves the read-only
// half, and it builds detailData by hand; this test drives detailDataFor and
// the HTTP handler, so the seam between the flag and the rendering is real.
//
// The same page is then requested as the requester who owns the ticket: a
// `user` actor holds no CapEditTicket, so the Move-to control must be absent.
func TestClosedTicketDetailKeepsReopenForCapableActor(t *testing.T) {
	h := newHarness(t)
	requester := seedUserRole(t, h.store, "Rae", "rae@example.com", domain.RoleUser)
	sess := seedSession(t, h.store, requester.ID)

	// The requester creates their own ticket through the real HTTP create path,
	// so the closure below can take the requester-confirmation route.
	rec := h.postFormAs(t, "/tickets", url.Values{
		"title":       {"Close then reopen"},
		"category_id": {strconv.FormatInt(h.bugCategory.ID, 10)},
		"priority":    {"medium"},
	}, sess.ID)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("create status = %d, want 303", rec.Code)
	}

	// Admin drives new → in_progress → resolved through the real service; the
	// requester then confirms, the audited path to closed (a requester-owned
	// resolved ticket cannot be closed manually by any actor).
	if _, err := h.tickets.Transition(t.Context(), *h.admin, 1, domain.StateInProgress, ""); err != nil {
		t.Fatalf("transition to in_progress: %v", err)
	}
	if _, err := h.tickets.Transition(t.Context(), *h.admin, 1, domain.StateResolved, ""); err != nil {
		t.Fatalf("transition to resolved: %v", err)
	}
	if _, err := h.tickets.ConfirmResolution(t.Context(), *requester, 1); err != nil {
		t.Fatalf("requester confirm: %v", err)
	}
	view, err := h.tickets.GetByID(t.Context(), *h.admin, 1)
	if err != nil {
		t.Fatalf("view after confirm: %v", err)
	}
	if view.Ticket.State != domain.StateClosed {
		t.Fatalf("state = %q, want closed", view.Ticket.State)
	}

	// Capable actor (admin) GET: the reopen control is present and offers
	// in_progress. This is the behaviour a naive CanEdit gate destroys.
	capable := h.get(t, "/tickets/1", true).Body.String()
	for _, want := range []string{
		`data-transition-open="in_progress"`,
		`>Reopen</button>`,
		`name="to" value="in_progress"`,
	} {
		if !strings.Contains(capable, want) {
			t.Errorf("closed ticket must keep the reopen control %q for a capable actor, got: %s", want, capable)
		}
	}

	// Requester GET: the same closed ticket offers no transition control.
	requesterBody := getDetailAs(t, h, "/tickets/1", sess.ID, true).Body.String()
	if strings.Contains(requesterBody, `data-transition-open=`) || strings.Contains(requesterBody, `/tickets/1/transition`) {
		t.Errorf("a requester must not be offered the closed-ticket reopen control, got: %s", requesterBody)
	}
}

// TestTicketDetailSLAPanelStaffOnly (issue #211, PR 4) proves the milestone
// panel renders on a STAFF detail page and is ABSENT from a requester's page
// even when that requester's own ticket carries a frozen commitment. The
// commitment is created through the real service (SLA enabled via the settings
// route), so the projection comes from a real freeze, not a hand-built store.
func TestTicketDetailSLAPanelStaffOnly(t *testing.T) {
	h := newHarness(t)

	// Enable SLA through the real settings route so the create path freezes a
	// commitment against the category matrix the migration materialized.
	form := slaPanelForm("80")
	form.Set("sla_enabled", "1")
	if rec := h.postForm(t, "/settings/sla", form, false); rec.Code != http.StatusSeeOther {
		t.Fatalf("enable SLA: status = %d, want %d", rec.Code, http.StatusSeeOther)
	}

	staffTicket := h.seedTicket(t, "Freeze me", nil)
	frozen, err := h.store.SLAStore().TicketSLA(t.Context(), staffTicket.ID)
	if err != nil {
		t.Fatalf("read frozen commitment: %v", err)
	}
	if frozen == nil {
		t.Fatalf("enabled SLA must freeze a commitment on the staff ticket")
	}

	body := h.get(t, "/tickets/"+strconv.FormatInt(staffTicket.ID, 10), false).Body.String()

	// The section heading names the SLA and carries no state of its own; each
	// milestone renders one row with its label, its state dot and the time
	// left; the frozen due instant survives as the <time datetime> the
	// countdown reads.
	for _, want := range []string{
		`<div class="prop-heading">SLA</div>`,
		`<span class="prop-label">First response</span>`,
		`<span class="prop-label">Resolve</span>`,
		`datetime="` + formatDatetime(frozen.DueFirstResponseAt) + `"`,
		`datetime="` + formatDatetime(frozen.DueResolveAt) + `"`,
		// PR 6: the panel anchors the client clock and both PENDING due rows
		// carry the countdown hook; the page loads the countdown script.
		`data-server-now="`,
		`<time datetime="` + formatDatetime(frozen.DueFirstResponseAt) + `" data-sla-countdown>`,
		`<time datetime="` + formatDatetime(frozen.DueResolveAt) + `" data-sla-countdown>`,
		`<script src="/static/sla_countdown.js" defer></script>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("staff detail must contain %q, got: %s", want, body)
		}
	}

	// The panel states each milestone's STATE and the time left, and nothing
	// else: the target, due and achieved rows were removed by decision (the
	// due instant survives as the <time datetime> the countdown reads).
	for _, absent := range []string{`>Target<`, `>Achieved<`, `>Remaining<`} {
		if strings.Contains(body, absent) {
			t.Errorf("the SLA panel must not render the %q row any more, got: %s", absent, body)
		}
	}

	// A requester-owned ticket, created through the real service, also freezes
	// a commitment (SLA is on) — yet the requester page must not render the
	// panel, and the handler must not even read the projection for a `user`.
	requester := seedUserRole(t, h.store, "Rosa", "rosa-detail@example.com", domain.RoleUser)
	requesterSession := seedSession(t, h.store, requester.ID)
	requesterTicket, err := h.tickets.Create(t.Context(), *requester, application.CreateTicketInput{
		Title: "Requester request", CategoryID: h.bugCategory.ID, Priority: domain.PriorityMedium,
	})
	if err != nil {
		t.Fatalf("create requester ticket: %v", err)
	}
	requesterFrozen, err := h.store.SLAStore().TicketSLA(t.Context(), requesterTicket.ID)
	if err != nil {
		t.Fatalf("read requester frozen commitment: %v", err)
	}
	if requesterFrozen == nil {
		t.Fatalf("the requester ticket must carry a frozen commitment so its page is not vacuously SLA-free")
	}

	requesterRec := doRequest(h.mux, h.mw, http.MethodGet, "/tickets/"+strconv.FormatInt(requesterTicket.ID, 10), map[string]string{
		"Cookie": sessionCookie + "=" + requesterSession.ID,
	})
	if requesterRec.Code != http.StatusOK {
		t.Fatalf("requester detail status = %d, want 200", requesterRec.Code)
	}
	requesterBody := requesterRec.Body.String()
	if !strings.Contains(requesterBody, "Requester request") {
		t.Fatalf("requester detail must render its own ticket, got: %s", requesterBody)
	}
	for _, absent := range []string{
		`<div class="prop-heading">SLA `,
		`<div class="prop-heading">Response `,
		`<div class="prop-heading">Resolve `,
		`<span class="prop-label">Target</span>`,
		`<span class="prop-label">Remaining</span>`,
		`class="sla-dot at_risk"`,
		`data-server-now`,
		`data-sla-countdown`,
		`/static/sla_countdown.js`,
	} {
		if strings.Contains(requesterBody, absent) {
			t.Errorf("LEAK: requester detail must not render SLA markup %q, got: %s", absent, requesterBody)
		}
	}
}

// TestTicketDetailSLAPanelAbsentWithoutFrozenSLA (issue #211, PR 4) proves a
// ticket with no frozen commitment renders no panel: the harness leaves SLA
// disabled (its default), so the created ticket freezes nothing and the detail
// page stays SLA-free.
func TestTicketDetailSLAPanelAbsentWithoutFrozenSLA(t *testing.T) {
	h := newHarness(t)
	tkt := h.seedTicket(t, "No commitment", nil)

	frozen, err := h.store.SLAStore().TicketSLA(t.Context(), tkt.ID)
	if err != nil {
		t.Fatalf("read frozen commitment: %v", err)
	}
	if frozen != nil {
		t.Fatalf("SLA disabled must freeze no commitment, got: %+v", frozen)
	}

	body := h.get(t, "/tickets/"+strconv.FormatInt(tkt.ID, 10), false).Body.String()
	if !strings.Contains(body, "No commitment") {
		t.Fatalf("detail must render the ticket, got: %s", body)
	}
	for _, absent := range []string{
		`<div class="prop-heading">SLA `,
		`<span class="prop-label">Target</span>`,
		`<span class="prop-label">Remaining</span>`,
	} {
		if strings.Contains(body, absent) {
			t.Errorf("no frozen SLA must render no panel, found %q in: %s", absent, body)
		}
	}
}

// TestSLAPanelAbsentWithoutFrozenCommitment pins the panel gate directly: a
// projection with no frozen commitment (legacy ticket, or SLA disabled at
// creation) yields no panel, so the section cannot render an empty shell.
func TestSLAPanelAbsentWithoutFrozenCommitment(t *testing.T) {
	if p := slaPanelFor(i18n.English, domain.SLAProjection{Overall: domain.SLANone}); p != nil {
		t.Errorf("no frozen commitment must yield no panel, got: %+v", p)
	}
}

// TestSLADurationLabel pins the pre-formatted duration copy the detail panel
// prints (issue #211): the largest non-zero unit carries the next smaller one,
// seconds survive only when they do not divide evenly into minutes, and a
// negative value is prefixed with a sign. The template FuncMap has no
// formatter, so this helper is the single source of the copy.
func TestSLADurationLabel(t *testing.T) {
	for _, tc := range []struct {
		seconds int
		want    string
	}{
		{14400, "4h 0m"},
		{9000, "2h 30m"},
		{1800, "30m"},
		{45, "45s"},
		{5430, "1h 30m 30s"},
		{0, "0s"},
		{-1800, "-30m"},
	} {
		if got := slaDurationLabel(tc.seconds); got != tc.want {
			t.Errorf("slaDurationLabel(%d) = %q, want %q", tc.seconds, got, tc.want)
		}
	}
}

// TestSLAMilestoneRemainingLabel pins the remaining copy: "2h 30m" while
// pending, "30m overdue" once the remainder is negative, and EMPTY once the
// milestone is achieved (the panel drops the row).
func TestSLAMilestoneRemainingLabel(t *testing.T) {
	pending := domain.SLAMilestoneStatus{TargetSeconds: 9000, Remaining: 2*time.Hour + 30*time.Minute}
	if got := slaMilestoneViewFor(i18n.English, "Response", pending).Remaining; got != "2h 30m" {
		t.Errorf("pending remaining = %q, want %q", got, "2h 30m")
	}
	overdue := domain.SLAMilestoneStatus{TargetSeconds: 9000, Remaining: -30 * time.Minute}
	if got := slaMilestoneViewFor(i18n.English, "Response", overdue).Remaining; got != "30m overdue" {
		t.Errorf("overdue remaining = %q, want %q", got, "30m overdue")
	}
	achievedAt := goldenT1
	achieved := domain.SLAMilestoneStatus{TargetSeconds: 9000, AchievedAt: &achievedAt}
	if got := slaMilestoneViewFor(i18n.English, "Response", achieved).Remaining; got != "" {
		t.Errorf("achieved remaining = %q, want empty", got)
	}
}

// TestTicketDetailSLACountdownHooks (issue #211, PR 6) pins the render-side
// contract of the live countdown on the detail fragment: the panel carries
// the projection instant the client derives its clock offset from, the PENDING
// milestone's due <time> carries the countdown hook, and the ACHIEVED
// milestone keeps the plain timestamp and never ticks. The fixture instants
// are literals, so nothing here depends on a wall clock.
func TestTicketDetailSLACountdownHooks(t *testing.T) {
	body := renderGolden(t, "tickets_show", "ticket_detail", fixtureDetailData(), true)

	for _, want := range []string{
		// The skew anchor: the literal ProjectedAt the panel was projected at.
		`<div class="prop-section" data-server-now="2026-08-07T06:00:00Z">`,
		// The pending (Resolve) milestone's due instant carries the hook.
		`<time datetime="2026-08-07T10:00:00Z" data-sla-countdown>`,
		// The ticker carries the server's remaining time as its INITIAL value,
		// so a browser with no JavaScript still reads the truth. It ticks every
		// second, so assistive tech ignores it; the coarse span is the
		// accessible one and is visually hidden.
		`<span class="sla-countdown-ticker" aria-hidden="true">`,
		`<span class="sla-countdown-coarse visually-hidden">10:00 · 07-08-2026</span>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("pending milestone countdown must render %q, got: %s", want, body)
		}
	}

	// The first response was MET: the panel shows its label and its state badge
	// and no time row at all, and it must never carry the tick hook.
	if strings.Contains(body, `<time datetime="2026-08-06T14:00:00Z" data-sla-countdown`) {
		t.Errorf("achieved milestone must not carry the countdown hook, got: %s", body)
	}
	if strings.Contains(body, `<time datetime="2026-08-06T14:00:00Z"`) {
		t.Errorf("an achieved milestone renders no time row any more, got: %s", body)
	}
	for _, want := range []string{`<span class="prop-label">First response</span>`, `<span class="sla-state"><span class="sla-dot met" aria-hidden="true"></span><span class="sla-state-word">Met</span></span>`} {
		if !strings.Contains(body, want) {
			t.Errorf("the achieved milestone must still render %q, got: %s", want, body)
		}
	}
}

// TestTicketDetailSLACountdownAssetGating pins that the countdown script is
// loaded ONLY on a detail page that actually renders a countdown, and that a
// page without a frozen commitment never requests it. The two renders differ
// only in the panel, so the gate is the only cause.
func TestTicketDetailSLACountdownAssetGating(t *testing.T) {
	const asset = `<script src="/static/sla_countdown.js" defer></script>`

	withPanel := renderGolden(t, "tickets_show", "", fixtureDetailData(), false)
	if !strings.Contains(withPanel, asset) {
		t.Errorf("a detail page with an SLA panel must load the countdown script, got: %s", withPanel)
	}

	withoutPanel := fixtureDetailData()
	withoutPanel.SLA = nil
	withoutPanel.SLACountdownAssets = false
	body := renderGolden(t, "tickets_show", "", withoutPanel, false)
	if strings.Contains(body, "/static/sla_countdown.js") {
		t.Errorf("a detail page with no SLA panel must not load the countdown script, got: %s", body)
	}
}
