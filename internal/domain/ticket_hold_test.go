package domain_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// Ticket on hold while waiting for the requester (issue #352). The hold is
// an attribute of an open ticket, not a lifecycle state: the state machine
// is untouched, and a pause reason is mandatory.

var holdNow = time.Date(2026, 8, 6, 11, 0, 0, 0, time.UTC)

func TestPutOnHoldRecordsReasonAndInstant(t *testing.T) {
	for _, state := range []domain.State{domain.StateNew, domain.StateInProgress} {
		t.Run(string(state), func(t *testing.T) {
			tt := baseTicket()
			tt.State = state
			ev, err := tt.PutOnHold("  waiting for the laptop serial number  ", holdNow)
			if err != nil {
				t.Fatalf("PutOnHold: %v", err)
			}
			if tt.OnHoldSince == nil || !tt.OnHoldSince.Equal(holdNow) {
				t.Errorf("OnHoldSince = %v, want %v", tt.OnHoldSince, holdNow)
			}
			if tt.OnHoldReason != "waiting for the laptop serial number" {
				t.Errorf("OnHoldReason = %q", tt.OnHoldReason)
			}
			if !tt.IsOnHold() {
				t.Error("IsOnHold = false")
			}
			if tt.State != state {
				t.Errorf("State = %q, want %q (a hold is not a state)", tt.State, state)
			}
			if !tt.UpdatedAt.Equal(holdNow) {
				t.Errorf("UpdatedAt = %v, want %v", tt.UpdatedAt, holdNow)
			}
			want := &domain.AuditEvent{
				TicketID:  tt.ID,
				Action:    domain.ActionHold,
				Field:     strPtr("on_hold"),
				Note:      strPtr("waiting for the laptop serial number"),
				CreatedAt: holdNow,
			}
			if !reflect.DeepEqual(ev, want) {
				t.Errorf("event = %+v, want %+v", ev, want)
			}
		})
	}
}

func TestPutOnHoldRejectionsLeaveTicketUnchanged(t *testing.T) {
	onHold := func() *domain.Ticket {
		tt := baseTicket()
		since := holdNow.Add(-time.Hour)
		tt.OnHoldSince = &since
		tt.OnHoldReason = "earlier reason"
		return tt
	}
	closed := func(s domain.State) func() *domain.Ticket {
		return func() *domain.Ticket { tt := baseTicket(); tt.State = s; return tt }
	}
	tests := []struct {
		name    string
		ticket  func() *domain.Ticket
		reason  string
		wantKey string
		forbid  bool
	}{
		{"empty reason", baseTicket, "", "error.hold_reason_required", false},
		{"blank reason", baseTicket, "   \n ", "error.hold_reason_required", false},
		{"already on hold", onHold, "another reason", "error.ticket_already_on_hold", false},
		{"resolved", closed(domain.StateResolved), "reason", "", true},
		{"closed", closed(domain.StateClosed), "reason", "", true},
		{"cancelled", closed(domain.StateCancelled), "reason", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tt := tc.ticket()
			before := *tt
			ev, err := tt.PutOnHold(tc.reason, holdNow)
			if ev != nil {
				t.Errorf("event = %+v, want nil", ev)
			}
			if tc.forbid {
				var fe *domain.ForbiddenError
				if !errors.As(err, &fe) || fe.Message != domain.ErrMsgClosedTicketReadOnly {
					t.Errorf("err = %v, want ForbiddenError %q", err, domain.ErrMsgClosedTicketReadOnly)
				}
			} else {
				var ve *domain.ValidationError
				if !errors.As(err, &ve) || ve.Key != tc.wantKey {
					t.Errorf("err = %#v, want ValidationError key %q", err, tc.wantKey)
				}
			}
			if !reflect.DeepEqual(*tt, before) {
				t.Errorf("ticket changed:\n got %+v\nwant %+v", *tt, before)
			}
		})
	}
}

func TestResumeFromHoldClearsTheHold(t *testing.T) {
	tt := baseTicket()
	since := holdNow.Add(-2 * time.Hour)
	tt.OnHoldSince = &since
	tt.OnHoldReason = "waiting"
	pausedAt, ev, err := tt.ResumeFromHold(holdNow)
	if err != nil {
		t.Fatalf("ResumeFromHold: %v", err)
	}
	if !pausedAt.Equal(since) {
		t.Errorf("pausedAt = %v, want %v", pausedAt, since)
	}
	if tt.OnHoldSince != nil || tt.OnHoldReason != "" || tt.IsOnHold() {
		t.Errorf("hold not cleared: since=%v reason=%q", tt.OnHoldSince, tt.OnHoldReason)
	}
	if tt.State != domain.StateInProgress || !tt.UpdatedAt.Equal(holdNow) {
		t.Errorf("state/updated = %q / %v", tt.State, tt.UpdatedAt)
	}
	want := &domain.AuditEvent{TicketID: tt.ID, Action: domain.ActionResume, Field: strPtr("on_hold"), CreatedAt: holdNow}
	if !reflect.DeepEqual(ev, want) {
		t.Errorf("event = %+v, want %+v", ev, want)
	}
}

func TestResumeFromHoldRejectsATicketNotOnHold(t *testing.T) {
	tt := baseTicket()
	before := *tt
	_, ev, err := tt.ResumeFromHold(holdNow)
	var ve *domain.ValidationError
	if !errors.As(err, &ve) || ve.Key != "error.ticket_not_on_hold" {
		t.Errorf("err = %#v, want ValidationError key error.ticket_not_on_hold", err)
	}
	if ev != nil || !reflect.DeepEqual(*tt, before) {
		t.Errorf("rejected resume wrote: ev=%+v ticket=%+v", ev, *tt)
	}
}

// TestTransitionIntoTerminalStateRequiresEndingTheHold pins the invariant
// that no terminal ticket is left on hold: every closing path must end the
// hold first (and shift the SLA), so a forgotten path fails loudly instead
// of storing a resolved ticket that still reads as paused.
func TestTransitionIntoTerminalStateRequiresEndingTheHold(t *testing.T) {
	for _, to := range []domain.State{domain.StateResolved, domain.StateCancelled} {
		t.Run(string(to), func(t *testing.T) {
			tt := baseTicket()
			since := holdNow.Add(-time.Hour)
			tt.OnHoldSince = &since
			tt.OnHoldReason = "waiting"
			before := *tt
			ev, err := tt.Transition(to, "", holdNow)
			var ve *domain.ValidationError
			if !errors.As(err, &ve) || ve.Key != "error.ticket_on_hold" {
				t.Errorf("err = %#v, want ValidationError key error.ticket_on_hold", err)
			}
			if ev != nil || !reflect.DeepEqual(*tt, before) {
				t.Errorf("rejected transition wrote: ev=%+v", ev)
			}
		})
	}
}

func TestTransitionBetweenOpenStatesKeepsTheHold(t *testing.T) {
	tt := baseTicket()
	tt.State = domain.StateNew
	since := holdNow.Add(-time.Hour)
	tt.OnHoldSince = &since
	tt.OnHoldReason = "waiting"
	if _, err := tt.Transition(domain.StateInProgress, "", holdNow); err != nil {
		t.Fatalf("Transition: %v", err)
	}
	if !tt.IsOnHold() || tt.OnHoldReason != "waiting" {
		t.Errorf("hold lost on new -> in_progress")
	}
}
