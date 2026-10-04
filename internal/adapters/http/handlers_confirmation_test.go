package httpadapter

import (
	"net/http"
	"net/url"
	"strconv"
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

// Issue #264: a requester rejection detaches the workflow pin by design, but
// it must not lose the ticket's history. The persisted `tickets.user_id` is
// untouched, the `ticket_manual_solutions` row survives, and BOTH the
// completed-task event (historical solution + responsible person) and the
// Assignee row must still render them — for staff and for the requester —
// on a fresh request. The reopened ticket stays a manual ticket: no pin, no
// pending plan.
func TestRejectResolutionKeepsHistoricalSolutionAndAssignee(t *testing.T) {
	const solution = "restarted the billing worker"
	h := newHarness(t)
	requester := seedUserRole(t, h.store, "Rita", "rita@tkt.test", domain.RoleUser)
	tkt, err := h.tickets.Create(t.Context(), *requester, application.CreateTicketInput{
		Title: "billing broken", Description: "d", CategoryID: h.bugCategory.ID, Priority: domain.PriorityMedium,
	})
	if err != nil {
		t.Fatalf("create requester ticket: %v", err)
	}
	h.assignTicket(t, tkt.ID, h.admin.ID)
	id := strconv.FormatInt(tkt.ID, 10)
	if rec := h.postForm(t, "/tickets/"+id+"/workflow/steps/1/complete", url.Values{"solution": {solution}}, false); rec.Code != http.StatusOK {
		t.Fatalf("complete manual step = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	h.seedTransition(t, tkt.ID, domain.StateInProgress, "")
	h.seedTransition(t, tkt.ID, domain.StateResolved, "")

	sess := seedSession(t, h.store, requester.ID)
	if rec := h.postFormAs(t, "/tickets/"+id+"/confirmation", url.Values{"decision": {"reject"}}, sess.ID); rec.Code != http.StatusSeeOther {
		t.Fatalf("reject = %d, want 303: %s", rec.Code, rec.Body.String())
	}

	// The detach is deliberate and unchanged: nil pin, no pending plan.
	db := h.rawDB(t)
	if pin := scanNullInt(t, db, `SELECT workflow_version_id FROM tickets WHERE id = ?`, tkt.ID); pin.Valid {
		t.Fatalf("workflow_version_id = %d, want NULL after rejection (detached)", pin.Int64)
	}
	if got := scanNullInt(t, db, `SELECT user_id FROM tickets WHERE id = ?`, tkt.ID); !got.Valid || got.Int64 != h.admin.ID {
		t.Fatalf("persisted user_id = %v, want the retained assignee %d", got, h.admin.ID)
	}
	if stored := scanOneString(t, db, `SELECT solution FROM ticket_manual_solutions WHERE ticket_id = ? AND step_index = 0`, tkt.ID); stored != solution {
		t.Fatalf("persisted solution = %q, want %q", stored, solution)
	}

	// The completed-task event must keep rendering the historical solution AND
	// the person who completed it, in both sessions.
	for _, tc := range []struct {
		name    string
		body    string
		wantRow string
	}{
		{
			name: "staff detail",
			body: h.get(t, "/tickets/"+id, false).Body.String(),
			// A staff actor keeps the assign control; the retained assignee is
			// the selected option, never the Unassigned placeholder.
			wantRow: `<option value="1" selected>Admin</option>`,
		},
		{
			name:    "requester detail",
			body:    doRequest(h.mux, h.mw, http.MethodGet, "/tickets/"+id, map[string]string{"Cookie": sessionCookie + "=" + sess.ID}).Body.String(),
			wantRow: `<span id="assign-user-value" class="prop-value">Admin</span>`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, want := range []string{
				`<div class="timeline-entry timeline-event timeline-manual">`,
				`<div class="timeline-manual-heading">`,
				`<strong class="timeline-actor">Admin</strong> <span class="timeline-action">completed the task</span>`,
				`<dt>Solution</dt>`,
				`<dd>` + solution + `</dd>`,
				tc.wantRow,
			} {
				if !strings.Contains(tc.body, want) {
					t.Errorf("reopened detail must keep %q, got: %s", want, tc.body)
				}
			}
			if strings.Contains(tc.body, `class="workflow-pending`) {
				t.Errorf("a detached ticket must render no pending plan: %s", tc.body)
			}
		})
	}
}
