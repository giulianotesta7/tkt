package httpadapter

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// TestConfirmationEndpoint pins the POST /tickets/{id}/confirmation auth
// matrix (requester-confirmation delta): only the ticket's requester may
// confirm (→ closed) or reject (→ in_progress, workflow detached) while the
// ticket is resolved; every other role is refused (403 / 404 by scope), a
// missing or unknown decision is a 422 with no write, and anonymous visitors
// are bounced to /login by the session middleware.
func TestConfirmationEndpoint(t *testing.T) {
	// seedResolved drives a freshly created ticket to resolved through the
	// real service, optionally re-pointing its requester (admin seeds as
	// requester; pass requester for requester-owned fixtures or nil for
	// legacy requester-NULL ones) and optionally pinning a published
	// workflow version (reject must detach it).
	seedResolved := func(t *testing.T, h *harness, requester *domain.User, pin int64, assignTo *domain.User) {
		t.Helper()
		tkt := h.seedTicket(t, "Login page down", nil)
		if requester != nil {
			if _, err := h.rawDB(t).Exec(`UPDATE tickets SET requester_user_id = ? WHERE id = ?`, requester.ID, tkt.ID); err != nil {
				t.Fatalf("pin requester: %v", err)
			}
		} else {
			h.makeLegacy(t, tkt.ID)
		}
		if assignTo != nil {
			h.assignTicket(t, tkt.ID, assignTo.ID)
		}
		h.seedTransition(t, tkt.ID, domain.StateInProgress, "")
		h.seedTransition(t, tkt.ID, domain.StateResolved, "")
		if pin != 0 {
			if _, err := h.rawDB(t).Exec(`UPDATE tickets SET workflow_version_id = ? WHERE id = ?`, pin, tkt.ID); err != nil {
				t.Fatalf("pin workflow version: %v", err)
			}
		}
	}

	t.Run("requester confirms own resolved ticket", func(t *testing.T) {
		h := newHarness(t)
		requester := seedUserRole(t, h.store, "Rosa", "rosa@tkt.test", domain.RoleUser)
		seedResolved(t, h, requester, 0, nil)
		sess := seedSession(t, h.store, requester.ID)
		rec := h.postFormAs(t, "/tickets/1/confirmation", url.Values{"decision": {"confirm"}}, sess.ID)

		wantRedirect(t, rec, http.StatusSeeOther, "/tickets/1")
		view, err := h.tickets.GetByID(t.Context(), *h.admin, 1)
		if err != nil {
			t.Fatalf("view: %v", err)
		}
		if view.Ticket.State != domain.StateClosed {
			t.Errorf("state = %q, want closed after requester confirmation", view.Ticket.State)
		}
	})

	t.Run("requester rejects own resolved ticket detaches the workflow", func(t *testing.T) {
		h := newHarness(t)
		requester := seedUserRole(t, h.store, "Rosa", "rosa@tkt.test", domain.RoleUser)
		vid := h.publishWorkflow(t, h.bugCategory.ID, simpleManualDef())
		seedResolved(t, h, requester, vid, nil)
		sess := seedSession(t, h.store, requester.ID)
		rec := h.postFormAs(t, "/tickets/1/confirmation", url.Values{"decision": {"reject"}}, sess.ID)

		wantRedirect(t, rec, http.StatusSeeOther, "/tickets/1")
		view, err := h.tickets.GetByID(t.Context(), *h.admin, 1)
		if err != nil {
			t.Fatalf("view: %v", err)
		}
		if view.Ticket.State != domain.StateInProgress {
			t.Errorf("state = %q, want in_progress after requester rejection", view.Ticket.State)
		}
		if got := scanNullInt(t, h.rawDB(t), `SELECT workflow_version_id FROM tickets WHERE id = 1`); got.Valid {
			t.Errorf("workflow_version_id = %d, want NULL after rejection (detached)", got.Int64)
		}
	})

	t.Run("agent admin and root are forbidden", func(t *testing.T) {
		for _, tc := range []struct {
			name   string
			email  string
			role   domain.Role
			assign bool
		}{
			{name: "agent", email: "agent@tkt.test", role: domain.RoleAgent, assign: true},
			{name: "admin", email: "adm@tkt.test", role: domain.RoleAdmin},
			{name: "root", email: "root@tkt.test", role: domain.RoleRoot},
		} {
			t.Run(tc.name, func(t *testing.T) {
				h := newHarness(t)
				actor := seedUserRole(t, h.store, tc.name, tc.email, tc.role)
				var assignTo *domain.User
				if tc.assign {
					assignTo = actor
				}
				seedResolved(t, h, nil, 0, assignTo)
				sess := seedSession(t, h.store, actor.ID)
				rec := h.postFormAs(t, "/tickets/1/confirmation", url.Values{"decision": {"confirm"}}, sess.ID)

				if rec.Code != http.StatusForbidden {
					t.Fatalf("status = %d, want 403", rec.Code)
				}
				if !strings.Contains(rec.Body.String(), application.ErrMsgNotTicketRequester) {
					t.Errorf("body must carry the not-the-requester message, got: %s", rec.Body.String())
				}
				view, err := h.tickets.GetByID(t.Context(), *h.admin, 1)
				if err != nil {
					t.Fatalf("view: %v", err)
				}
				if view.Ticket.State != domain.StateResolved {
					t.Errorf("denied confirmation must not write, state = %q, want resolved", view.Ticket.State)
				}
			})
		}
	})

	t.Run("unrelated role-user is not found", func(t *testing.T) {
		h := newHarness(t)
		outsider := seedUserRole(t, h.store, "Beto", "beto@tkt.test", domain.RoleUser)
		seedResolved(t, h, nil, 0, nil)
		sess := seedSession(t, h.store, outsider.ID)
		rec := h.postFormAs(t, "/tickets/1/confirmation", url.Values{"decision": {"confirm"}}, sess.ID)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404 for an out-of-scope role-user", rec.Code)
		}
	})

	t.Run("missing or unknown decision is a validation error with no write", func(t *testing.T) {
		for name, form := range map[string]url.Values{
			"missing": {},
			"unknown": {"decision": {"maybe"}},
		} {
			t.Run(name, func(t *testing.T) {
				h := newHarness(t)
				seedResolved(t, h, nil, 0, nil)
				rec := h.postForm(t, "/tickets/1/confirmation", form, false)

				if rec.Code != http.StatusUnprocessableEntity {
					t.Fatalf("status = %d, want 422", rec.Code)
				}
				view, err := h.tickets.GetByID(t.Context(), *h.admin, 1)
				if err != nil {
					t.Fatalf("view: %v", err)
				}
				if view.Ticket.State != domain.StateResolved {
					t.Errorf("invalid decision must not write, state = %q, want resolved", view.Ticket.State)
				}
			})
		}
	})

	t.Run("unauthenticated visitor is redirected to login", func(t *testing.T) {
		h := newHarness(t)
		seedResolved(t, h, nil, 0, nil)
		rec := h.postFormAs(t, "/tickets/1/confirmation", url.Values{"decision": {"confirm"}}, "")

		wantRedirect(t, rec, http.StatusSeeOther, "/login")
	})
}
