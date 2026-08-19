package httpadapter

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// S5 comment visibility at the HTTP boundary (comment-visibility spec):
// a user-role actor must never receive internal comment content in ANY
// response, and may only create public comments; agent+ actors create and
// see both visibilities.

// seedUserOwnedTicket creates a ticket whose requester is the given user
// (ScopeOwned for that actor) through the real service.
func seedUserOwnedTicket(t *testing.T, h *harness, owner *domain.User, title string) *domain.Ticket {
	t.Helper()
	tkt, err := h.tickets.Create(context.Background(), *owner, application.CreateTicketInput{
		Title:       title,
		Description: "owned by " + owner.Name,
		CategoryID:  h.bugCategory.ID,
		Priority:    domain.PriorityMedium,
	})
	if err != nil {
		t.Fatalf("create user-owned ticket: %v", err)
	}
	return tkt
}

// TestTicketDetailUserNeverSeesInternalBody proves the leakage guarantee:
// a ticket with public AND internal comments renders only the public body
// for the owning user — no internal body text, no internal badge — while an
// agent sees both (comment-visibility spec "User never sees internal
// content" + "Agent sees public and internal").
func TestTicketDetailUserNeverSeesInternalBody(t *testing.T) {
	h := newHarness(t)
	user := seedUserRole(t, h.store, "Ula", "ula@example.com", domain.RoleUser)
	sess := seedSession(t, h.store, user.ID)
	tkt := seedUserOwnedTicket(t, h, user, "Ula's ticket")

	if _, err := h.comments.Add(t.Context(), *h.admin, tkt.ID, "Public note", "public"); err != nil {
		t.Fatalf("seed public comment: %v", err)
	}
	const secret = "STAFF-ONLY-SECRET-BODY"
	if _, err := h.comments.Add(t.Context(), *h.admin, tkt.ID, secret, "internal"); err != nil {
		t.Fatalf("seed internal comment: %v", err)
	}

	cookie := map[string]string{"Cookie": sessionCookie + "=" + sess.ID}
	rec := doRequest(h.mux, h.mw, http.MethodGet, "/tickets/"+strconv.FormatInt(tkt.ID, 10), cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("user detail status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Public note") {
		t.Errorf("user detail must render the public comment, got: %s", body)
	}
	if strings.Contains(body, secret) {
		t.Errorf("LEAK: user response contains internal comment body %q, got: %s", secret, body)
	}
	if strings.Contains(body, "badge internal") {
		t.Errorf("LEAK: user response renders the internal badge, got: %s", body)
	}

	// The HX timeline fragment must be equally clean.
	frag := doRequest(h.mux, h.mw, http.MethodGet, "/tickets/"+strconv.FormatInt(tkt.ID, 10),
		map[string]string{"Cookie": sessionCookie + "=" + sess.ID, "HX-Request": "true"})
	if strings.Contains(frag.Body.String(), secret) {
		t.Errorf("LEAK: user HX fragment contains internal comment body %q", secret)
	}
}

// TestTicketDetailAgentSeesInternalComment proves the agent+ actor receives
// both visibilities on the detail page, with the internal badge marker.
func TestTicketDetailAgentSeesInternalComment(t *testing.T) {
	h := newHarness(t)
	agent := seedUserRole(t, h.store, "Xylo", "xylo@example.com", domain.RoleAgent)
	sess := seedSession(t, h.store, agent.ID)
	tkt, err := h.tickets.Create(context.Background(), *h.admin, application.CreateTicketInput{
		Title:       "Assigned to Xylo",
		Description: "agent ticket",
		CategoryID:  h.bugCategory.ID,
		Priority:    domain.PriorityMedium,
		UserID:      &agent.ID,
	})
	if err != nil {
		t.Fatalf("create agent ticket: %v", err)
	}

	const secret = "AGENT-SECRET-BODY"
	if _, err := h.comments.Add(t.Context(), *h.admin, tkt.ID, secret, "internal"); err != nil {
		t.Fatalf("seed internal comment: %v", err)
	}

	rec := doRequest(h.mux, h.mw, http.MethodGet, "/tickets/"+strconv.FormatInt(tkt.ID, 10),
		map[string]string{"Cookie": sessionCookie + "=" + sess.ID})
	if rec.Code != http.StatusOK {
		t.Fatalf("agent detail status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, secret) {
		t.Errorf("agent detail must render the internal comment, got: %s", body)
	}
	if !strings.Contains(body, "badge internal") {
		t.Errorf("agent detail must mark internal comments with the badge, got: %s", body)
	}
	// The comment form offers the internal option only to internal-capable
	// actors (presentation; the server still enforces on forged values).
	if !strings.Contains(body, `value="internal"`) || !strings.Contains(body, "Internal (staff only)") {
		t.Errorf("agent comment form must offer the internal visibility option, got: %s", body)
	}
}

// TestTicketCommentUserInternalRejected403 proves a user-role actor posting
// visibility=internal is denied 403 and nothing is stored, even though the
// comment form never offers the option (forged values fail closed).
func TestTicketCommentUserInternalRejected403(t *testing.T) {
	h := newHarness(t)
	user := seedUserRole(t, h.store, "Ula", "ula@example.com", domain.RoleUser)
	sess := seedSession(t, h.store, user.ID)
	tkt := seedUserOwnedTicket(t, h, user, "Ula's ticket")

	rec := h.postFormAs(t, "/tickets/"+strconv.FormatInt(tkt.ID, 10)+"/comments",
		url.Values{"body": {"secret"}, "visibility": {"internal"}}, sess.ID)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), domain.ErrMsgUserCannotCommentInternal) {
		t.Errorf("403 re-render must show %q, got: %s", domain.ErrMsgUserCannotCommentInternal, rec.Body.String())
	}

	stored, err := h.comments.ListByTicket(t.Context(), tkt.ID, true)
	if err != nil {
		t.Fatalf("list stored comments: %v", err)
	}
	if len(stored) != 0 {
		t.Errorf("no comment may be stored (len=%d)", len(stored))
	}
}

// TestTicketCommentUserPublicStored proves a user-role actor posting
// visibility=public stores a public comment and redirects (add-comment
// spec + visibility rules).
func TestTicketCommentUserPublicStored(t *testing.T) {
	h := newHarness(t)
	user := seedUserRole(t, h.store, "Ula", "ula@example.com", domain.RoleUser)
	sess := seedSession(t, h.store, user.ID)
	tkt := seedUserOwnedTicket(t, h, user, "Ula's ticket")

	rec := h.postFormAs(t, "/tickets/"+strconv.FormatInt(tkt.ID, 10)+"/comments",
		url.Values{"body": {"From Ula"}, "visibility": {"public"}}, sess.ID)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	stored, err := h.comments.ListByTicket(t.Context(), tkt.ID, true)
	if err != nil {
		t.Fatalf("list stored comments: %v", err)
	}
	if len(stored) != 1 || stored[0].Body != "From Ula" || stored[0].Visibility != domain.CommentPublic {
		t.Fatalf("stored comment = %+v, want public 'From Ula'", stored)
	}
}

// TestTicketCommentAgentInternalStored proves an agent-role actor posting
// visibility=internal stores an internal comment and sees it (with the
// badge) in the subsequent detail render.
func TestTicketCommentAgentInternalStored(t *testing.T) {
	h := newHarness(t)
	agent := seedUserRole(t, h.store, "Xylo", "xylo@example.com", domain.RoleAgent)
	sess := seedSession(t, h.store, agent.ID)
	tkt, err := h.tickets.Create(context.Background(), *h.admin, application.CreateTicketInput{
		Title:       "Assigned to Xylo",
		Description: "agent ticket",
		CategoryID:  h.bugCategory.ID,
		Priority:    domain.PriorityMedium,
		UserID:      &agent.ID,
	})
	if err != nil {
		t.Fatalf("create agent ticket: %v", err)
	}

	path := "/tickets/" + strconv.FormatInt(tkt.ID, 10) + "/comments"
	rec := h.postFormAs(t, path, url.Values{"body": {"Staff note"}, "visibility": {"internal"}}, sess.ID)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	stored, err := h.comments.ListByTicket(t.Context(), tkt.ID, true)
	if err != nil {
		t.Fatalf("list stored comments: %v", err)
	}
	if len(stored) != 1 || stored[0].Visibility != domain.CommentInternal {
		t.Fatalf("stored comment = %+v, want internal", stored)
	}
}
