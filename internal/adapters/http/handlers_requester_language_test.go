package httpadapter

import (
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// requesterTicketFixture seeds a role-user requester and a ticket they own in
// the harness's default Bugs category (which carries the harness's published
// manual workflow), returning the requester, their live session and the ticket.
func requesterTicketFixture(t *testing.T, h *harness) (*domain.User, *domain.Session, *domain.Ticket) {
	t.Helper()
	requester := seedUserRole(t, h.store, "Requester Rae", "rae@tkt.test", domain.RoleUser)
	tkt, err := h.tickets.Create(t.Context(), *requester, application.CreateTicketInput{
		Title: "Requester language probe", Description: "d", CategoryID: h.bugCategory.ID, Priority: domain.PriorityMedium,
	})
	if err != nil {
		t.Fatalf("seed requester ticket: %v", err)
	}
	return requester, seedSession(t, h.store, requester.ID), tkt
}

// TestRequesterStateLabelSingleSource covers issue #334 S2: one state label
// per audience, from a single source. The requester reads "Received" for a new
// ticket in their list AND their detail, and the same in_progress label in
// both; staff keep the shared "New" / "In Progress".
func TestRequesterStateLabelSingleSource(t *testing.T) {
	h := newHarness(t)
	_, sess, tkt := requesterTicketFixture(t, h)
	detailPath := "/tickets/" + strconv.FormatInt(tkt.ID, 10)
	cookie := map[string]string{"Cookie": sessionCookie + "=" + sess.ID}

	newList := doRequest(h.mux, h.mw, http.MethodGet, "/tickets", cookie)
	if newList.Code != http.StatusOK {
		t.Fatalf("requester list status = %d, want 200", newList.Code)
	}
	if !strings.Contains(newList.Body.String(), `<span class="badge new">Received</span>`) {
		t.Errorf("requester list must label a new ticket Received: %.600s", newList.Body.String())
	}
	newDetail := doRequest(h.mux, h.mw, http.MethodGet, detailPath, cookie)
	if newDetail.Code != http.StatusOK {
		t.Fatalf("requester detail status = %d, want 200", newDetail.Code)
	}
	if !strings.Contains(newDetail.Body.String(), `<span class="badge new">Received</span>`) {
		t.Errorf("requester detail must label a new ticket Received: %.600s", newDetail.Body.String())
	}
	if strings.Contains(newDetail.Body.String(), `<span class="badge new">New</span>`) {
		t.Errorf("requester detail must not name a new ticket New: %.600s", newDetail.Body.String())
	}
	if staffDetail := h.get(t, detailPath, false); !strings.Contains(staffDetail.Body.String(), `<span class="badge new">New</span>`) {
		t.Errorf("staff detail must keep the shared New label: %.600s", staffDetail.Body.String())
	}

	if _, err := h.tickets.Transition(t.Context(), *h.admin, tkt.ID, domain.StateInProgress, ""); err != nil {
		t.Fatalf("transition ticket: %v", err)
	}
	progressList := doRequest(h.mux, h.mw, http.MethodGet, "/tickets", cookie)
	if progressList.Code != http.StatusOK {
		t.Fatalf("requester list status = %d, want 200", progressList.Code)
	}
	if !strings.Contains(progressList.Body.String(), `<span class="badge in_progress">In progress</span>`) {
		t.Errorf("requester list must label in_progress In progress: %.600s", progressList.Body.String())
	}
	progressDetail := doRequest(h.mux, h.mw, http.MethodGet, detailPath, cookie)
	if progressDetail.Code != http.StatusOK {
		t.Fatalf("requester detail status = %d, want 200", progressDetail.Code)
	}
	if !strings.Contains(progressDetail.Body.String(), `<span class="badge in_progress">In progress</span>`) {
		t.Errorf("requester detail must label in_progress In progress: %.600s", progressDetail.Body.String())
	}
	if staffDetail := h.get(t, detailPath, false); !strings.Contains(staffDetail.Body.String(), `<span class="badge in_progress">In Progress</span>`) {
		t.Errorf("staff detail must keep the shared In Progress label: %.600s", staffDetail.Body.String())
	}
}

// TestRequesterBackLinkNamesOwnList covers issue #334 S3: the requester's back
// link names their own list, while staff keep the team's Queue label.
func TestRequesterBackLinkNamesOwnList(t *testing.T) {
	h := newHarness(t)
	_, sess, tkt := requesterTicketFixture(t, h)
	detailPath := "/tickets/" + strconv.FormatInt(tkt.ID, 10)

	requester := doRequest(h.mux, h.mw, http.MethodGet, detailPath, map[string]string{"Cookie": sessionCookie + "=" + sess.ID})
	if requester.Code != http.StatusOK {
		t.Fatalf("requester detail status = %d, want 200", requester.Code)
	}
	if !strings.Contains(requester.Body.String(), `>← My tickets</a>`) {
		t.Errorf("requester detail back link must name their own list: %.600s", requester.Body.String())
	}
	if strings.Contains(requester.Body.String(), `>← Queue</a>`) {
		t.Errorf("requester detail must not use the team's Queue label: %.600s", requester.Body.String())
	}

	staff := h.get(t, detailPath, false)
	if staff.Code != http.StatusOK {
		t.Fatalf("staff detail status = %d, want 200", staff.Code)
	}
	if !strings.Contains(staff.Body.String(), `>← Queue</a>`) {
		t.Errorf("staff detail must keep the Queue back link: %.600s", staff.Body.String())
	}
	if strings.Contains(staff.Body.String(), `>← My tickets</a>`) {
		t.Errorf("staff detail must not use the requester back link: %.600s", staff.Body.String())
	}
}
