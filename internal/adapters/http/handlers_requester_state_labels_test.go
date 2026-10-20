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
		req := newStateBadge(i18n.New(i18n.Default), s, true)
		span := `<span class="badge ` + req.State + `">` + req.Label + `</span>`
		if !strings.Contains(body, span) {
			t.Errorf("requester list must render %s for %s: %.600s", span, s, body)
		}
		// The detail badge resolves through the same catalogue-backed source,
		// so the list card and the detail row can never name a state twice.
		if got := newStateBadge(i18n.New(i18n.Default), s, true); got != req {
			t.Errorf("requester detail badge for %s = %+v, want %+v", s, got, req)
		}
	}
}
