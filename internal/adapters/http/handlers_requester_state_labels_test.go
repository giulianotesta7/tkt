package httpadapter

import (
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// TestStateLabelPerAudienceAllStates pins the shared label source for every
// ticket state and both audiences (issue #334 S2 / S4).
func TestStateLabelPerAudienceAllStates(t *testing.T) {
	cases := map[domain.State][2]string{
		domain.StateNew:        {"Received", "New"},
		domain.StateInProgress: {"In progress", "In Progress"},
		domain.StateResolved:   {"Resolved", "Resolved"},
		domain.StateClosed:     {"Closed", "Closed"},
		domain.StateCancelled:  {"Cancelled", "Cancelled"},
	}
	for state, want := range cases {
		req := newStateBadge(state, true)
		if req.State != string(state) || req.Label != want[0] {
			t.Errorf("requester badge for %s = %q/%q, want class %q label %q", state, req.State, req.Label, state, want[0])
		}
		staff := newStateBadge(state, false)
		if staff.State != string(state) || staff.Label != want[1] {
			t.Errorf("staff badge for %s = %q/%q, want class %q label %q", state, staff.State, staff.Label, state, want[1])
		}
		if staff.Label != humanizeLabel(state) {
			t.Errorf("staff label for %s = %q, want humanize %q", state, staff.Label, humanizeLabel(state))
		}
	}
}

// TestRequesterListAndDetailAgreeForEveryState renders the requester's list
// card and detail state badge for every state and asserts both use the same
// single-source label (issue #334 S2).
func TestRequesterListAndDetailAgreeForEveryState(t *testing.T) {
	states := []domain.State{domain.StateNew, domain.StateInProgress, domain.StateResolved, domain.StateClosed, domain.StateCancelled}
	list := fixtureListData()
	list.CurrentUser = domain.User{ID: 2, Role: domain.RoleUser}
	var rows []ticketRow
	for i, s := range states {
		rows = append(rows, ticketRow{Ticket: domain.Ticket{ID: int64(i + 1), Number: i + 1, Title: "T", State: s, Priority: domain.PriorityMedium, CreatedAt: goldenT0, UpdatedAt: goldenT0}})
	}
	list.Tickets = rows
	body := renderGolden(t, "tickets_index", "user_ticket_list", list, true)
	for _, s := range states {
		req := newStateBadge(s, true)
		span := `<span class="badge ` + req.State + `">` + req.Label + `</span>`
		if !strings.Contains(body, span) {
			t.Errorf("requester list must render %s for %s: %.600s", span, s, body)
		}
		d := detailData{RequesterView: true, View: &application.TicketView{Ticket: &domain.Ticket{ID: 1, Number: 1, State: s}}}
		if got := d.StateBadge(); got != req {
			t.Errorf("requester detail badge for %s = %+v, want %+v", s, got, req)
		}
	}
}
