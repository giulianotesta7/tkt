package domain_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// fixedClock is a deterministic clock (D7: injected clock — the domain never
// calls time.Now() itself).
type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

// Compile-time contract: the domain must expose an injected clock.
var _ domain.Clock = fixedClock{}

// matrixCase is one cell of the 5x5 transition matrix (all 25 state pairs).
// Timestamp flags describe the effect the SPEC mandates for that pair.
type matrixCase struct {
	from, to domain.State
	allow    bool
	// entering resolved stamps resolved_at; entering closed stamps closed_at.
	setResolved bool
	setClosed   bool
	// reopening resolved clears resolved_at; reopening closed clears both.
	clearResolved bool
	clearBoth     bool
	// reopen from closed requires a non-empty reason recorded in the audit note.
	reason string
}

func newTicketInState(s domain.State, now time.Time) *domain.Ticket {
	t := &domain.Ticket{
		ID:        1,
		Number:    42,
		Title:     "Test ticket",
		State:     s,
		Priority:  domain.PriorityHigh,
		CreatedAt: now,
		UpdatedAt: now,
	}
	switch s {
	case domain.StateResolved:
		t.ResolvedAt = &now
	case domain.StateClosed:
		t.ResolvedAt = &now
		t.ClosedAt = &now
	}
	return t
}

func sameTimePtr(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}

func TestTransitionMatrix(t *testing.T) {
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	clock := fixedClock{now: now}

	cases := []matrixCase{
		// From new.
		{from: domain.StateNew, to: domain.StateNew},
		{from: domain.StateNew, to: domain.StateInProgress, allow: true},
		{from: domain.StateNew, to: domain.StateResolved, allow: true, setResolved: true},
		{from: domain.StateNew, to: domain.StateClosed},
		{from: domain.StateNew, to: domain.StateCancelled, allow: true},
		// From in_progress.
		{from: domain.StateInProgress, to: domain.StateNew},
		{from: domain.StateInProgress, to: domain.StateInProgress},
		{from: domain.StateInProgress, to: domain.StateResolved, allow: true, setResolved: true},
		{from: domain.StateInProgress, to: domain.StateClosed},
		{from: domain.StateInProgress, to: domain.StateCancelled, allow: true},
		// From resolved.
		{from: domain.StateResolved, to: domain.StateNew},
		{from: domain.StateResolved, to: domain.StateInProgress, allow: true, clearResolved: true},
		{from: domain.StateResolved, to: domain.StateResolved},
		{from: domain.StateResolved, to: domain.StateClosed, allow: true, setClosed: true},
		{from: domain.StateResolved, to: domain.StateCancelled},
		// From closed.
		{from: domain.StateClosed, to: domain.StateNew},
		{from: domain.StateClosed, to: domain.StateInProgress, allow: true, clearBoth: true, reason: "reopen to fix"},
		{from: domain.StateClosed, to: domain.StateResolved},
		{from: domain.StateClosed, to: domain.StateClosed},
		{from: domain.StateClosed, to: domain.StateCancelled},
		// From cancelled (terminal: no move out).
		{from: domain.StateCancelled, to: domain.StateNew},
		{from: domain.StateCancelled, to: domain.StateInProgress},
		{from: domain.StateCancelled, to: domain.StateResolved},
		{from: domain.StateCancelled, to: domain.StateClosed},
		{from: domain.StateCancelled, to: domain.StateCancelled},
	}
	if len(cases) != 25 {
		t.Fatalf("matrix must cover all 25 pairs, got %d", len(cases))
	}

	for _, tc := range cases {
		name := string(tc.from) + "-to-" + string(tc.to)
		t.Run(name, func(t *testing.T) {
			tt := newTicketInState(tc.from, now)
			beforeResolved, beforeClosed := tt.ResolvedAt, tt.ClosedAt

			event, err := tt.Transition(tc.to, tc.reason, clock.Now())

			if !tc.allow {
				if err == nil {
					t.Fatalf("Transition(%s -> %s) must be rejected", tc.from, tc.to)
				}
				var ive *domain.InvalidTransitionError
				if !errors.As(err, &ive) {
					t.Fatalf("want *InvalidTransitionError, got %T", err)
				}
				if ive.From != tc.from || ive.To != tc.to {
					t.Fatalf("error must carry from/to, got %q -> %q", ive.From, ive.To)
				}
				if !strings.Contains(err.Error(), domain.ErrMsgTransitionNotAllowed) {
					t.Fatalf("error must carry the English message %q, got %q", domain.ErrMsgTransitionNotAllowed, err.Error())
				}
				if tt.State != tc.from {
					t.Fatalf("state must stay %q on rejection, got %q", tc.from, tt.State)
				}
				if !sameTimePtr(tt.ResolvedAt, beforeResolved) || !sameTimePtr(tt.ClosedAt, beforeClosed) {
					t.Fatalf("timestamps must stay unchanged on rejection: resolved before=%v after=%v, closed before=%v after=%v",
						beforeResolved, tt.ResolvedAt, beforeClosed, tt.ClosedAt)
				}
				if event != nil {
					t.Fatalf("rejected transition must not produce an audit event, got %+v", event)
				}
				return
			}

			if err != nil {
				t.Fatalf("Transition(%s -> %s) must succeed, got %v", tc.from, tc.to, err)
			}
			if tt.State != tc.to {
				t.Fatalf("state must be %q, got %q", tc.to, tt.State)
			}

			// Audit event contract.
			if event == nil {
				t.Fatal("allowed transition must return an audit event")
			}
			if event.Action != domain.ActionTransition {
				t.Fatalf("audit action must be %q, got %q", domain.ActionTransition, event.Action)
			}
			// The changed field for a transition is the state itself (audit-log spec).
			if event.Field == nil || *event.Field != "state" {
				t.Fatalf("transition audit event must name the changed field %q, got %v", "state", event.Field)
			}
			if event.FromValue == nil || *event.FromValue != string(tc.from) {
				t.Fatalf("audit from_value must be %q, got %v", tc.from, event.FromValue)
			}
			if event.ToValue == nil || *event.ToValue != string(tc.to) {
				t.Fatalf("audit to_value must be %q, got %v", tc.to, event.ToValue)
			}
			if !event.CreatedAt.Equal(now) {
				t.Fatalf("audit event must carry the injected timestamp, got %v", event.CreatedAt)
			}
			if event.TicketID != tt.ID {
				t.Fatalf("audit event must reference ticket %d, got %d", tt.ID, event.TicketID)
			}

			// Timestamp semantics (resolution/closure stamps and reopen clears).
			wantResolved := beforeResolved
			switch {
			case tc.setResolved:
				wantResolved = &now
			case tc.clearResolved || tc.clearBoth:
				wantResolved = nil
			}
			wantClosed := beforeClosed
			switch {
			case tc.setClosed:
				wantClosed = &now
			case tc.clearBoth:
				wantClosed = nil
			}
			if !sameTimePtr(tt.ResolvedAt, wantResolved) {
				t.Fatalf("resolved_at must be %v, got %v", wantResolved, tt.ResolvedAt)
			}
			if !sameTimePtr(tt.ClosedAt, wantClosed) {
				t.Fatalf("closed_at must be %v, got %v", wantClosed, tt.ClosedAt)
			}

			// Reopen reason must land in the audit note; other transitions have none.
			if tc.from == domain.StateClosed && tc.to == domain.StateInProgress {
				if event.Note == nil || *event.Note != tc.reason {
					t.Fatalf("reopen from closed must record the reason in the audit note, got %v", event.Note)
				}
			} else if event.Note != nil {
				t.Fatalf("no reason expected for %s -> %s, got %q", tc.from, tc.to, *event.Note)
			}
		})
	}
}

func TestValidForwardPath(t *testing.T) {
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	tt := newTicketInState(domain.StateNew, now)

	var lastTo string
	for _, to := range []domain.State{domain.StateInProgress, domain.StateResolved, domain.StateClosed} {
		event, err := tt.Transition(to, "", now)
		if err != nil {
			t.Fatalf("forward transition to %s failed: %v", to, err)
		}
		lastTo = *event.ToValue
	}

	if tt.State != domain.StateClosed {
		t.Fatalf("final state must be closed, got %s", tt.State)
	}
	if tt.ResolvedAt == nil || !tt.ResolvedAt.Equal(now) {
		t.Fatalf("resolved_at must be stamped when passing resolved, got %v", tt.ResolvedAt)
	}
	if tt.ClosedAt == nil || !tt.ClosedAt.Equal(now) {
		t.Fatalf("closed_at must be stamped when passing closed, got %v", tt.ClosedAt)
	}
	if lastTo != string(domain.StateClosed) {
		t.Fatalf("last audit event must record closed as target, got %s", lastTo)
	}
}

func TestReopenFromClosedWithoutReason(t *testing.T) {
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	tt := newTicketInState(domain.StateClosed, now)

	event, err := tt.Transition(domain.StateInProgress, "", now)

	if err == nil {
		t.Fatal("reopen from closed without a reason must be rejected")
	}
	var rre *domain.ReopenReasonRequiredError
	if !errors.As(err, &rre) {
		t.Fatalf("want *ReopenReasonRequiredError, got %T", err)
	}
	if err.Error() != domain.ErrMsgReopenReasonRequired {
		t.Fatalf("error must carry the English message %q, got %q", domain.ErrMsgReopenReasonRequired, err.Error())
	}
	if tt.State != domain.StateClosed {
		t.Fatalf("state must stay closed on rejected reopen, got %s", tt.State)
	}
	if tt.ResolvedAt == nil || tt.ClosedAt == nil {
		t.Fatal("timestamps must be preserved when the reopen is rejected")
	}
	if event != nil {
		t.Fatalf("rejected reopen must not produce an audit event, got %+v", event)
	}
}

func TestTransitionUpdatedAt(t *testing.T) {
	// ticket-management spec: updated_at MUST reflect the last modification;
	// a transition is a modification, a rejected move is not.
	created := time.Date(2026, 8, 5, 9, 0, 0, 0, time.UTC)
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		name   string
		to     domain.State
		reject bool
	}{
		{name: "allowed transition refreshes updated_at", to: domain.StateInProgress},
		{name: "rejected transition keeps updated_at", to: domain.StateClosed, reject: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tt := &domain.Ticket{ID: 1, Title: "Transition", State: domain.StateNew, CreatedAt: created, UpdatedAt: created}
			_, err := tt.Transition(tc.to, "", now)

			if tc.reject {
				if err == nil {
					t.Fatalf("new -> %s must be rejected", tc.to)
				}
				if !tt.UpdatedAt.Equal(created) {
					t.Fatalf("rejected transition must not change updated_at, got %v", tt.UpdatedAt)
				}
				return
			}
			if err != nil {
				t.Fatalf("valid transition must succeed, got %v", err)
			}
			if !tt.UpdatedAt.Equal(now) {
				t.Fatalf("updated_at must be refreshed to the transition time, got %v", tt.UpdatedAt)
			}
		})
	}
}
