package httpadapter

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// TestTicketShowRendersDetail proves GET /tickets/{id} renders the detail
// page: number, title, category, comments, and the Activity (audit) panel.
func TestTicketShowRendersDetail(t *testing.T) {
	h := newHarness(t)
	tkt := h.seedTicket(t, "Login page down", nil)
	h.seedTransition(t, tkt.ID, domain.StateInProgress, "")
	if _, err := h.comments.Add(t.Context(), *h.admin, tkt.ID, "Checking now"); err != nil {
		t.Fatalf("seed comment: %v", err)
	}

	rec := h.get(t, "/tickets/1", false)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"Login page down", "TKT-1", "Bugs", "Checking now", "Timeline", "Properties", "Save properties"} {
		if !strings.Contains(body, want) {
			t.Errorf("detail page must contain %q, got: %s", want, body)
		}
	}
	if strings.Contains(body, `href="/tickets/1/edit"`) {
		t.Errorf("detail page must not expose the fallback edit screen, got: %s", body)
	}
	for _, want := range []string{`action="/tickets/1/edit"`, `id="ticket-priority"`, `id="ticket-user"`} {
		if !strings.Contains(body, want) {
			t.Errorf("inline properties form must contain %q, got: %s", want, body)
		}
	}
	for _, banned := range []string{`id="ticket-title"`, `id="ticket-description"`, `id="ticket-category"`, `name="title"`, `name="description"`, `name="category_id"`} {
		if strings.Contains(body, banned) {
			t.Errorf("title/description/category must not be editable on detail, found %q in: %s", banned, body)
		}
	}
	// Merged timeline DESC: the transition (newer) renders before created.
	// Match the timeline event markers (not bare words — "transition" also
	// appears in the inline CSS rules).
	createdEvent := `<span class="dot created"></span>`
	transitionEvent := `<span class="dot transition"></span>`
	if !(strings.Index(body, transitionEvent) < strings.Index(body, createdEvent)) {
		t.Errorf("merged timeline must be newest-first (transition before created), got: %s", body)
	}
}

func TestTicketShowRendersConciseSemanticMetadata(t *testing.T) {
	h := newHarness(t)
	h.seedTicket(t, "Login page down", nil)

	body := h.get(t, "/tickets/1", false).Body.String()
	if !strings.Contains(body, "Requester: Admin &lt;admin@tkt.test&gt;") {
		t.Errorf("requester metadata must come from the session-derived ticket data, got: %s", body)
	}
	if got := strings.Count(body, `<time datetime="`); got < 3 {
		t.Errorf("detail metadata and timeline must use semantic time elements, got %d: %s", got, body)
	}
	if !strings.Contains(body, " · ") {
		t.Errorf("display timestamps must use the human UTC separator, got: %s", body)
	}
}

func TestTicketTimelineDifferentiatesCommentsAndAuditEvents(t *testing.T) {
	h := newHarness(t)
	tkt := h.seedTicket(t, "Login page down", nil)
	h.seedTransition(t, tkt.ID, domain.StateInProgress, "")
	if _, err := h.comments.Add(t.Context(), *h.admin, tkt.ID, "Checking now"); err != nil {
		t.Fatalf("seed comment: %v", err)
	}

	body := h.get(t, "/tickets/1", false).Body.String()
	for _, want := range []string{`class="timeline-entry timeline-comment"`, `class="timeline-entry timeline-event"`, "New → In Progress"} {
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

// TestTicketCommentOnClosedTicket proves comments are accepted on a closed
// ticket (comment-timeline spec).
func TestTicketCommentOnClosedTicket(t *testing.T) {
	h := newHarness(t)
	tkt := h.seedTicket(t, "Login page down", nil)
	for _, to := range []domain.State{domain.StateInProgress, domain.StateResolved, domain.StateClosed} {
		h.seedTransition(t, tkt.ID, to, "")
	}

	rec := h.postForm(t, "/tickets/1/comments", url.Values{"body": {"Late note"}}, false)

	wantRedirect(t, rec, http.StatusSeeOther, "/tickets/1")
	view, err := h.tickets.GetByID(t.Context(), *h.admin, 1)
	if err != nil || len(view.Comments) != 1 {
		t.Errorf("comment on closed ticket must be stored (len=%d, err=%v)", len(view.Comments), err)
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
		if _, err := h.comments.Add(t.Context(), *h.admin, 1, body); err != nil {
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
// updates only priority/assignment, ignores forged immutable fields
// (title/description/category), appends the audit event, and redirects.
func TestTicketEditUpdatesPriorityAndAudits(t *testing.T) {
	h := newHarness(t)
	h.seedTicket(t, "Login page down", nil)

	form := url.Values{
		"title":       {"Login page is back"},
		"description": {"Fixed the 500"},
		"category_id": {"999"}, // nonexistent: must be ignored, not applied
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
	if view.Ticket.Title != "Login page down" {
		t.Errorf("title = %q, want immutable original", view.Ticket.Title)
	}
	if view.Ticket.CategoryID != h.bugCategory.ID {
		t.Errorf("category = %d, want immutable original", view.Ticket.CategoryID)
	}
	var fields []string
	for _, ev := range view.AuditEvents {
		if ev.Field != nil {
			fields = append(fields, *ev.Field)
		}
	}
	joined := strings.Join(fields, ",")
	if !strings.Contains(joined, "priority") {
		t.Errorf("audit must record the priority change, got %v", fields)
	}
	if strings.Contains(joined, "title") || strings.Contains(joined, "category") || strings.Contains(joined, "description") {
		t.Errorf("audit must not record immutable field changes, got %v", fields)
	}
}

// TestTicketEditInvalidPriority422 proves an unsupported priority on edit is
// rejected 422 with no changes applied.
func TestTicketEditInvalidPriority422(t *testing.T) {
	h := newHarness(t)
	h.seedTicket(t, "Login page down", nil)

	form := url.Values{"priority": {"urgent"}}
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

// TestTicketEditUnassign proves clearing the assignment via the edit form
// works.
func TestTicketEditUnassign(t *testing.T) {
	h := newHarness(t)
	beto := h.createUser(t, "Beto", "beto@example.com", "secret")
	h.seedTicket(t, "Login page down", nil)
	// Assign beto through an edit, then unassign through another edit.
	form := url.Values{
		"priority": {"medium"},
		"user_id":  {strconv.FormatInt(beto.ID, 10)},
	}
	if rec := h.postForm(t, "/tickets/1/edit", form, false); rec.Code != http.StatusSeeOther {
		t.Fatalf("assign edit status = %d", rec.Code)
	}
	view, err := h.tickets.GetByID(t.Context(), *h.admin, 1)
	if err != nil || view.AssignedUser == nil || view.AssignedUser.ID != beto.ID {
		t.Fatalf("assignment failed: %+v err=%v", view.AssignedUser, err)
	}

	clearForm := url.Values{"priority": {"medium"}, "user_id": {""}}
	rec := h.postForm(t, "/tickets/1/edit", clearForm, false)
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

	form := url.Values{"priority": {"high"}}
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

func TestTicketEditTimelineResolvesAssignedUserName(t *testing.T) {
	h := newHarness(t)
	beto := h.createUser(t, "Beto", "beto@example.com", "secret")
	h.seedTicket(t, "Login page down", nil)

	form := url.Values{
		"priority": {"medium"},
		"user_id":  {strconv.FormatInt(beto.ID, 10)},
	}
	rec := h.postForm(t, "/tickets/1/edit", form, false)
	wantRedirect(t, rec, http.StatusSeeOther, "/tickets/1")

	body := h.get(t, "/tickets/1", false).Body.String()
	if !strings.Contains(body, "Update · Assigned To · Unassigned → Beto") {
		t.Errorf("assignment event must resolve user names, got: %s", body)
	}
	if strings.Contains(body, "Assigned To · Unassigned → "+strconv.FormatInt(beto.ID, 10)) {
		t.Errorf("assignment event must not expose the user id, got: %s", body)
	}
}
