package httpadapter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// TestDetailDataConfirmationFlags pins the detail-payload presentation flags
// for requester confirmation (D7, requester-confirmation delta): CanConfirm is
// true only for the authenticated requester of a resolved ticket; CanComment
// re-opens the comment form for that requester only (the service stays the
// enforcement point — these are presentation mirrors, comment-timeline delta);
// and the Move-to list is filtered at the detailDataFor call site: on a
// requester-owned resolved ticket the `closed` target is dropped for every
// actor (only the requester may close, and they use the confirmation control,
// not Move-to), while a requester-NULL resolved ticket keeps both `closed` and
// the reopen for authorized agents (state-machine delta).
func TestDetailDataConfirmationFlags(t *testing.T) {
	// seedResolvedFor drives a freshly created ticket to resolved through the
	// real service, optionally re-pointing its requester (nil = legacy
	// requester-NULL) and optionally assigning an agent, mirroring the 4.1
	// endpoint fixture.
	seedResolvedFor := func(t *testing.T, h *harness, requester, assignTo *domain.User) {
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
	}

	// detailAs builds the detail payload for an explicit actor (the
	// deskRequest pattern: session-actor context, no middleware) through the
	// same handler wiring the harness mux uses.
	detailAs := func(t *testing.T, h *harness, actor domain.User) detailData {
		t.Helper()
		handlers := NewTicketHandlers(h.tickets, h.comments, h.search, h.categories, h.users, h.store.DeskStore(), h.workflows, application.NewWorkflowRunner(h.clock), h.store.WorkflowRunStore(), h.store.WorkflowUnitOfWork(), h.renderer)
		req := httptest.NewRequest(http.MethodGet, "/tickets/1", nil)
		req = req.WithContext(context.WithValue(req.Context(), ctxKeyUser{}, &actor))
		data, status, err := handlers.detailDataFor(req, 1)
		if err != nil || status != 0 {
			t.Fatalf("detailDataFor: status=%d err=%v", status, err)
		}
		return data
	}

	t.Run("requester sees both flags and Move-to drops closed on own resolved ticket", func(t *testing.T) {
		h := newHarness(t)
		requester := seedUserRole(t, h.store, "Rosa", "rosa@tkt.test", domain.RoleUser)
		seedResolvedFor(t, h, requester, nil)
		data := detailAs(t, h, *requester)

		if !data.CanConfirm {
			t.Errorf("CanConfirm = false, want true for the requester of a resolved ticket")
		}
		if !data.CanComment {
			t.Errorf("CanComment = false, want true for the requester of a resolved ticket")
		}
		if len(data.Next) != 1 || data.Next[0].To != domain.StateInProgress || !data.Next[0].NeedsReason {
			t.Errorf("Next = %+v, want exactly the in_progress reopen (NeedsReason, closed dropped for requester-owned)", data.Next)
		}
	})

	t.Run("agent sees no flags and reopen-only Move-to on requester-owned resolved ticket", func(t *testing.T) {
		h := newHarness(t)
		requester := seedUserRole(t, h.store, "Rosa", "rosa@tkt.test", domain.RoleUser)
		agent := seedUserRole(t, h.store, "Ada", "ada@tkt.test", domain.RoleAgent)
		seedResolvedFor(t, h, requester, agent)
		data := detailAs(t, h, *agent)

		if data.CanConfirm {
			t.Errorf("CanConfirm = true, want false for a non-requester agent")
		}
		if data.CanComment {
			t.Errorf("CanComment = true, want false for an agent on a resolved ticket")
		}
		if len(data.Next) != 1 || data.Next[0].To != domain.StateInProgress || !data.Next[0].NeedsReason {
			t.Errorf("Next = %+v, want exactly the in_progress reopen (closed dropped for requester-owned, any actor)", data.Next)
		}
	})

	t.Run("requester-NULL resolved keeps closed and reopen with both flags hidden", func(t *testing.T) {
		h := newHarness(t)
		agent := seedUserRole(t, h.store, "Ada", "ada@tkt.test", domain.RoleAgent)
		seedResolvedFor(t, h, nil, agent)
		data := detailAs(t, h, *agent)

		if data.CanConfirm {
			t.Errorf("CanConfirm = true, want false with no requester (agent cannot confirm)")
		}
		if data.CanComment {
			t.Errorf("CanComment = true, want false for an agent on a requester-NULL resolved ticket")
		}
		if len(data.Next) != 2 || data.Next[0].To != domain.StateClosed || data.Next[0].NeedsReason || data.Next[1].To != domain.StateInProgress || !data.Next[1].NeedsReason {
			t.Errorf("Next = %+v, want closed then in_progress reopen (requester-NULL keeps manual closure)", data.Next)
		}
	})

	t.Run("other states are unchanged by the filter", func(t *testing.T) {
		h := newHarness(t)
		tkt := h.seedTicket(t, "Fresh ticket", nil)
		if len(tkt.State) == 0 {
			t.Fatal("seeded ticket has no state")
		}
		data := detailAs(t, h, *h.admin)

		if len(data.Next) != 3 || data.Next[0].To != domain.StateInProgress || data.Next[1].To != domain.StateResolved || data.Next[2].To != domain.StateCancelled {
			t.Errorf("Next = %+v, want the unfiltered new-state targets (in_progress, resolved, cancelled)", data.Next)
		}
		if data.CanConfirm {
			t.Errorf("CanConfirm = true on a new ticket, want false (confirmation is resolved-only)")
		}
		if !data.CanComment {
			t.Errorf("CanComment = false on a new ticket, want true (open states stay commentable for everyone)")
		}
	})
}
