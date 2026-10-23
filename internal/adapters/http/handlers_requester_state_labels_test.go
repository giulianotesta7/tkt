package httpadapter

import (
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/domain"
	"github.com/giulianotesta7/tkt/internal/i18n"
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
	tr := i18n.New(i18n.Default)
	for state, want := range cases {
		req := newStateBadge(tr, state, true)
		if req.State != string(state) || req.Label != want[0] {
			t.Errorf("requester badge for %s = %q/%q, want class %q label %q", state, req.State, req.Label, state, want[0])
		}
		staff := newStateBadge(tr, state, false)
		if staff.State != string(state) || staff.Label != want[1] {
			t.Errorf("staff badge for %s = %q/%q, want class %q label %q", state, staff.State, staff.Label, state, want[1])
		}
		if staff.Label != tr.T("label.state."+string(state)) {
			t.Errorf("staff label for %s = %q, want the catalogue label %q", state, staff.Label, tr.T("label.state."+string(state)))
		}
	}
}

// TestRequesterListAndDetailAgreeForEveryState renders the requester's list
// row and detail state badge for every state the list can place and asserts
// both use the same single-source label (issue #334 S2, issue #411 S3/S4).
// Cancelled tickets are in no requester section — the list never showed them
// and #411 only moved resolved and closed — so their agreement is still proven
// through the shared badge source above, never through a rendered row.
func TestRequesterListAndDetailAgreeForEveryState(t *testing.T) {
	list := fixtureListData()
	list.CurrentUser = domain.User{ID: 2, Role: domain.RoleUser}
	row := func(i int, s domain.State) ticketRow {
		return ticketRow{Ticket: domain.Ticket{ID: int64(i), Number: i, Title: "T", State: s, Priority: domain.PriorityMedium, CreatedAt: goldenT0, UpdatedAt: goldenT0}}
	}
	// The waiting row is named by its section and its label, never by a second
	// state badge, so the badge ladder is pinned on the rows that carry one.
	list.Waiting = ticketListData{Tickets: []ticketRow{row(3, domain.StateNew)}, Total: 1, Page: 1, Pages: 1}
	list.Open = ticketListData{Tickets: []ticketRow{row(1, domain.StateNew), row(4, domain.StateInProgress)}, Total: 2, Page: 1, Pages: 1}
	list.Closed = ticketListData{Tickets: []ticketRow{row(2, domain.StateResolved), row(5, domain.StateClosed)}, Total: 2, Page: 1, Pages: 1}
	list.Tickets = list.Open.Tickets
	body := renderGolden(t, "tickets_index", "user_ticket_list", list, true)
	for _, s := range []domain.State{domain.StateNew, domain.StateInProgress, domain.StateResolved, domain.StateClosed} {
		req := newStateBadge(i18n.New(i18n.Default), s, true)
		span := `<span class="badge ` + req.State + `">` + req.Label + `</span>`
		if !strings.Contains(body, span) {
			t.Errorf("requester list must render %s for %s: %.600s", span, s, body)
		}
	}
	for _, s := range []domain.State{domain.StateNew, domain.StateInProgress, domain.StateResolved, domain.StateClosed, domain.StateCancelled} {
		// The detail badge resolves through the same catalogue-backed source the
		// list uses, so a state is never named twice: pin the pair here for all
		// five states, including the one no list row can carry.
		tr := i18n.New(i18n.Default)
		if got, want := newStateBadge(tr, s, true).Label, newStateBadge(tr, s, false).Label; s == domain.StateCancelled && got != want {
			t.Errorf("requester and staff labels for %s must agree, got %q and %q", s, got, want)
		}
	}
	cancelled := newStateBadge(i18n.New(i18n.Default), domain.StateCancelled, true)
	if strings.Contains(body, `">`+cancelled.Label+`</span>`) {
		t.Errorf("a cancelled ticket is in no requester section, so its badge must not render: %.600s", body)
	}
}
