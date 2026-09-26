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
	for _, want := range []string{"Login page down", "TKT-1", "Bugs", "Checking now", "Timeline", "Properties", "id=\"ticket-title\"", "<h2>Description</h2>", "Test description"} {
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
	// Merged timeline DESC: the transition (newer) renders before created.
	// Match the actor-first event narrative lines (not bare words).
	createdEvent := "created the ticket"
	transitionEvent := "moved the ticket to in progress"
	if !(strings.Index(body, transitionEvent) < strings.Index(body, createdEvent)) {
		t.Errorf("merged timeline must be newest-first (transition before created), got: %s", body)
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
	agent := h.createUser(t, "Agent", "agent@tkt.test", "secret")
	ticket := h.seedTicket(t, "Assigned work", nil)
	h.assignTicket(t, ticket.ID, agent.ID)
	session := h.loginCookie(t, agent.Email, "secret")
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
func TestTicketCommentsNewestFirst(t *testing.T) {
	h := newHarness(t)
	h.seedTicket(t, "Login page down", nil)
	for _, body := range []string{"first", "second", "third"} {
		if _, err := h.comments.Add(t.Context(), *h.admin, 1, body, "public"); err != nil {
			t.Fatalf("seed comment %q: %v", body, err)
		}
	}

	rec := h.get(t, "/tickets/1", false)
	body := rec.Body.String()
	if !(strings.Index(body, "third") < strings.Index(body, "second") && strings.Index(body, "second") < strings.Index(body, "first")) {
		t.Errorf("comments must render newest first, got: %s", body)
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
	beto := h.createUser(t, "Beto", "beto@example.com", "secret")
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
	beto := h.createUser(t, "Beto", "beto@example.com", "secret")
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
	selected := `<option value="` + strconv.FormatInt(beto.ID, 10) + `" selected>`
	if !strings.Contains(body, selected) {
		t.Errorf("error re-render must keep the assigned user selected, want %q, got: %s", selected, body)
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

var (
	htmlNameAttrRE  = regexp.MustCompile(`name="([^"]+)"`)
	htmlValueAttrRE = regexp.MustCompile(`value="([^"]*)"`)
)

// formFieldNames returns the name attributes of every control in a rendered
// form block, in document order, so a test can build its POST body from the
// shipped markup instead of guessing the fields — a re-added hidden sibling
// shows up here.
func formFieldNames(form string) []string {
	matches := htmlNameAttrRE.FindAllStringSubmatch(form, -1)
	names := make([]string, 0, len(matches))
	for _, m := range matches {
		names = append(names, m[1])
	}
	return names
}

// renderedFormValues builds the POST body a browser would send for the
// rendered form block: every named control becomes a key, a select
// contributes its selected (or first) option value, and other inputs
// contribute their value attribute.
func renderedFormValues(t *testing.T, form string) url.Values {
	t.Helper()
	values := url.Values{}
	for _, name := range formFieldNames(form) {
		values.Set(name, renderedControlValue(form, name))
	}
	return values
}

func renderedControlValue(form, name string) string {
	at := strings.Index(form, `name="`+name+`"`)
	if at < 0 {
		return ""
	}
	tagStart := strings.LastIndex(form[:at], "<")
	if tagStart < 0 {
		return ""
	}
	tag := form[tagStart:]
	if strings.HasPrefix(tag, "<select") {
		body := tag
		if end := strings.Index(body, "</select>"); end >= 0 {
			body = body[:end]
		}
		for _, chunk := range strings.Split(body, "<option") {
			if !strings.Contains(chunk, "selected") {
				continue
			}
			if m := htmlValueAttrRE.FindStringSubmatch(chunk); m != nil {
				return m[1]
			}
		}
		if first := strings.Index(body, "<option"); first >= 0 {
			if m := htmlValueAttrRE.FindStringSubmatch(body[first:]); m != nil {
				return m[1]
			}
		}
		return ""
	}
	if m := htmlValueAttrRE.FindStringSubmatch(tag); m != nil {
		return m[1]
	}
	return ""
}

// TestTicketDetailEditControlsRequireExplicitSubmit proves the rendered detail
// fragment no longer mutates on `change`: neither `requestSubmit` nor `onchange`
// survives, each mutating form carries ONLY its own field, and the priority,
// assignment and transition forms each expose an explicit submit button.
func TestTicketDetailEditControlsRequireExplicitSubmit(t *testing.T) {
	h := newHarness(t)
	h.seedTicket(t, "Login page down", nil)
	body := h.get(t, "/tickets/1", true).Body.String()

	for _, banned := range []string{"requestSubmit", "onchange"} {
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

	for _, tc := range []struct{ name, marker string }{
		{"priority edit", `id="ticket-priority"`},
		{"assignment", `id="assign-user"`},
		{"state transition", `id="ticket-state"`},
	} {
		block := renderedFormBlock(t, body, tc.marker)
		if !strings.Contains(block, "<button") || !strings.Contains(block, `type="submit"`) {
			t.Errorf("%s form must contain an explicit submit button, got: %s", tc.name, block)
		}
	}
}

// TestTicketDetailPriorityFormPostsOnlyItsOwnFields derives its POST body from
// the RENDERED priority form and submits it: the stored title and its audit
// trail stay untouched, proving applying the priority can never ship a sibling
// field again.
func TestTicketDetailPriorityFormPostsOnlyItsOwnFields(t *testing.T) {
	h := newHarness(t)
	h.seedTicket(t, "Login page down", nil)
	body := h.get(t, "/tickets/1", true).Body.String()
	block := renderedFormBlock(t, body, `id="ticket-priority"`)

	if names := formFieldNames(block); len(names) != 1 || names[0] != "priority" {
		t.Fatalf("priority form must expose exactly the priority field, got %v (markup: %s)", names, block)
	}

	form := renderedFormValues(t, block)
	form.Set("priority", "critical")
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

// TestTicketDetailStateApplyIsAFormChild proves the transition Apply button is
// a direct child of the state form — the form's last child — and not nested
// inside the hidden reopen-reason field.
func TestTicketDetailStateApplyIsAFormChild(t *testing.T) {
	h := newHarness(t)
	h.seedTicket(t, "Login page down", nil)
	body := h.get(t, "/tickets/1", true).Body.String()
	block := renderedFormBlock(t, body, `id="ticket-state"`)

	fieldStart := strings.Index(block, `<div id="state-reason-field"`)
	if fieldStart < 0 {
		t.Fatalf("state form must keep the reason field, got: %s", block)
	}
	fieldCloseRel := strings.Index(block[fieldStart:], "</div>")
	if fieldCloseRel < 0 {
		t.Fatalf("state reason field must close, got: %s", block)
	}
	fieldClose := fieldStart + fieldCloseRel
	apply := strings.Index(block, `id="state-apply"`)
	if apply < 0 {
		t.Fatalf("state form must render the Apply button, got: %s", block)
	}
	if apply < fieldClose {
		t.Fatalf("state Apply button must be a direct child of the form, not nested inside #state-reason-field, got: %s", block)
	}
	tail := block[fieldClose+len("</div>") : strings.Index(block, "</form>")]
	tail = strings.TrimSpace(tail)
	if !strings.HasPrefix(tail, "<button") || !strings.HasSuffix(tail, "Apply</button>") {
		t.Fatalf("state Apply button must be the form's last child, got: %s", block)
	}
}

// TestTicketEditTimelineResolvesAssignedUserName proves the assignment
// event resolves the assigned user's name on the timeline — the assignment
// now flows through POST /tickets/{id}/assign (S4: the single assignment
// path).
func TestTicketEditTimelineResolvesAssignedUserName(t *testing.T) {
	h := newHarness(t)
	beto := h.createUser(t, "Beto", "beto@example.com", "secret")
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
	beto := h.createUser(t, "Beto", "beto@example.com", "secret")
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
	beto := h.createUser(t, "Beto", "beto@example.com", "secret")
	carla := h.createUser(t, "Carla", "carla@example.com", "secret")
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
	beto := h.createUser(t, "Beto", "beto@example.com", "secret")
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
	beto := h.createUser(t, "Beto", "beto@example.com", "secret")
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
	if p := slaPanelFor(domain.SLAProjection{Overall: domain.SLANone}); p != nil {
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
	if got := slaMilestoneViewFor("Response", pending).Remaining; got != "2h 30m" {
		t.Errorf("pending remaining = %q, want %q", got, "2h 30m")
	}
	overdue := domain.SLAMilestoneStatus{TargetSeconds: 9000, Remaining: -30 * time.Minute}
	if got := slaMilestoneViewFor("Response", overdue).Remaining; got != "30m overdue" {
		t.Errorf("overdue remaining = %q, want %q", got, "30m overdue")
	}
	achievedAt := goldenT1
	achieved := domain.SLAMilestoneStatus{TargetSeconds: 9000, AchievedAt: &achievedAt}
	if got := slaMilestoneViewFor("Response", achieved).Remaining; got != "" {
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
