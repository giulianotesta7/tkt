package httpadapter

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

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
	// Match the event summary lines (not bare words).
	createdEvent := "Ticket created"
	transitionEvent := "Moved to In Progress"
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
	ticket := h.seedTicket(t, "Assigned work", func(in *application.CreateTicketInput) { in.UserID = &agent.ID })
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
	for _, want := range []string{`class="timeline-entry timeline-comment"`, `class="timeline-entry timeline-event"`, "Moved to In Progress"} {
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
func TestTicketTransitionFullCycle(t *testing.T) {
	h := newHarness(t)
	h.seedTicket(t, "Login page down", nil)

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
// without a reason is rejected 422 (reopen-reason spec).
func TestTicketTransitionReopenRequiresReason(t *testing.T) {
	h := newHarness(t)
	tkt := h.seedTicket(t, "Login page down", nil)
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
// reason succeeds and the reason lands in the audit note.
func TestTicketTransitionReopenWithReason(t *testing.T) {
	h := newHarness(t)
	tkt := h.seedTicket(t, "Login page down", nil)
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
	if !strings.Contains(body, "Assigned to Beto") {
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
	if !strings.Contains(body, "Assigned to Beto") {
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
