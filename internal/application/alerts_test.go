package application_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// The operational alert pass (issue #408, specs S1-S8): the ASSIGNED agent
// learns that a ticket crossed an SLA threshold or stopped moving. Every test
// drives the pass with an injected clock and asserts the notifications the bell
// would show. No sleeps, no wall clock.

// alertHarness wires the pass with its collaborators: the candidate read, the
// idempotent write, the user lookup that resolves the recipient, and the
// settings that carry the working calendar.
type alertHarness struct {
	svc      *application.AlertService
	store    *fakeAlertStore
	tx       *fakeAlertUnitOfWork
	users    *fakeUserStore
	clock    *fakeClock
	settings *fakeSLASettingsStore
	agent    domain.User
}

func newAlertHarness() *alertHarness {
	clock := fixedClock()
	users := newFakeUserStore()
	tx := &fakeAlertUnitOfWork{}
	store := &fakeAlertStore{tx: tx}
	settings := &fakeSLASettingsStore{calendar: domain.DefaultSLACalendar()}
	h := &alertHarness{
		store: store, tx: tx, users: users, clock: clock, settings: settings,
		agent: users.seedRole("Bob", "bob@example.com", domain.RoleAgent, true),
	}
	h.svc = application.NewAlertService(store, tx, users, settings, clock)
	return h
}

// assigned builds an open ticket assigned to the harness agent.
func (h *alertHarness) assigned(id int64) domain.Ticket {
	agent := h.agent.ID
	return domain.Ticket{ID: id, Number: int(id), Title: "printer down",
		State: domain.StateInProgress, Priority: domain.PriorityHigh, UserID: &agent}
}

// at moves the injected clock to the given instant and runs one pass.
func (h *alertHarness) passAt(t *testing.T, now time.Time) {
	t.Helper()
	h.clock.now = now
	if err := h.svc.Pass(context.Background()); err != nil {
		t.Fatalf("pass: %v", err)
	}
}

func (h *alertHarness) kinds() []domain.NotificationKind {
	var out []domain.NotificationKind
	for _, n := range h.tx.notifications {
		out = append(out, n.Kind)
	}
	return out
}

func alertStart() time.Time { return time.Date(2026, 3, 2, 9, 0, 0, 0, time.UTC) }

func alertFrozen(started time.Time) *domain.TicketSLA {
	return &domain.TicketSLA{
		StartedAt:            started,
		FirstResponseSeconds: 3600,
		ResolveSeconds:       7200,
		WarnFirstResponseAt:  started.Add(1 * time.Hour),
		DueFirstResponseAt:   started.Add(2 * time.Hour),
		WarnResolveAt:        started.Add(3 * time.Hour),
		DueResolveAt:         started.Add(4 * time.Hour),
	}
}

// S1/S3: a milestone that reaches its frozen warning instant tells the
// assignee, and only the assignee.
func TestAlertPassTellsTheAssigneeWhenAMilestoneIsAtRisk(t *testing.T) {
	h := newAlertHarness()
	started := alertStart()
	h.store.candidates = []application.AlertCandidate{{
		Ticket: h.assigned(1), SLA: alertFrozen(started), LastActivityAt: started,
	}}
	h.passAt(t, started.Add(time.Hour))

	if got := h.kinds(); len(got) != 1 || got[0] != domain.NotificationSLAFirstResponseAtRisk {
		t.Fatalf("kinds = %v, want one %q", got, domain.NotificationSLAFirstResponseAtRisk)
	}
	n := h.tx.notifications[0]
	if n.UserID != h.agent.ID {
		t.Errorf("recipient = %d, want the assignee %d", n.UserID, h.agent.ID)
	}
	if n.ActorUserID != nil {
		t.Errorf("actor = %v, want nil: nobody performed an alert", *n.ActorUserID)
	}
	if n.TicketID != 1 {
		t.Errorf("ticket = %d, want 1", n.TicketID)
	}
}

// S7: the condition persists across passes, so every later pass must find the
// alert already recorded and write nothing.
func TestAlertPassNeverRepeatsOneSLACondition(t *testing.T) {
	h := newAlertHarness()
	started := alertStart()
	h.store.candidates = []application.AlertCandidate{{
		Ticket: h.assigned(1), SLA: alertFrozen(started), LastActivityAt: started,
	}}
	h.passAt(t, started.Add(time.Hour))
	h.passAt(t, started.Add(90*time.Minute))
	h.passAt(t, started.Add(100*time.Minute))

	if got := h.kinds(); len(got) != 1 {
		t.Fatalf("kinds = %v, want exactly one row across three passes", got)
	}
}

// S1: crossing the due instant is its own condition, so the ticket is alerted
// about again — once — with the breach kind.
func TestAlertPassReportsTheBreachAfterTheRisk(t *testing.T) {
	h := newAlertHarness()
	started := alertStart()
	h.store.candidates = []application.AlertCandidate{{
		Ticket: h.assigned(1), SLA: alertFrozen(started), LastActivityAt: started,
	}}
	h.passAt(t, started.Add(time.Hour))
	h.passAt(t, started.Add(2*time.Hour))

	want := []domain.NotificationKind{
		domain.NotificationSLAFirstResponseAtRisk,
		domain.NotificationSLAFirstResponseBreached,
	}
	got := h.kinds()
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("kinds = %v, want %v", got, want)
	}
}

// S3: the assigned agent is the ONLY recipient. A ticket nobody is assigned to
// produces nothing, even when its commitment is long breached.
func TestAlertPassIgnoresTicketsWithNoAssignee(t *testing.T) {
	h := newAlertHarness()
	started := alertStart()
	unassigned := h.assigned(1)
	unassigned.UserID = nil
	h.store.candidates = []application.AlertCandidate{{
		Ticket: unassigned, SLA: alertFrozen(started), LastActivityAt: started,
	}}
	h.passAt(t, started.Add(10*time.Hour))

	if got := h.kinds(); len(got) != 0 {
		t.Fatalf("kinds = %v, want none for an unassigned ticket", got)
	}
}

// S4: a deactivated assignee cannot open the ticket, so the existing recipient
// invariants drop the row and the pass records nothing.
func TestAlertPassTellsNobodyWhenTheAssigneeCannotOpenTheTicket(t *testing.T) {
	h := newAlertHarness()
	h.users.users[h.agent.ID].Active = false
	started := alertStart()
	h.store.candidates = []application.AlertCandidate{{
		Ticket: h.assigned(1), SLA: alertFrozen(started), LastActivityAt: started,
	}}
	h.passAt(t, started.Add(10*time.Hour))

	if got := h.kinds(); len(got) != 0 {
		t.Fatalf("kinds = %v, want none for an unreachable assignee", got)
	}
}

// S2: a ticket with no frozen commitment is never retroactively in trouble.
func TestAlertPassIgnoresTicketsWithoutACommitment(t *testing.T) {
	h := newAlertHarness()
	started := alertStart()
	h.store.candidates = []application.AlertCandidate{{
		Ticket: h.assigned(1), SLA: nil, LastActivityAt: started,
	}}
	h.passAt(t, started.Add(10*time.Hour))

	for _, k := range h.kinds() {
		if k != domain.NotificationNoMovement {
			t.Fatalf("kind %q was reported for a ticket with no commitment", k)
		}
	}
}

// S5/S6: two working days of silence alert the assignee once.
func TestAlertPassReportsATicketWithNoMovement(t *testing.T) {
	h := newAlertHarness()
	// Monday 10:00; two working days later is Wednesday 10:00.
	lastActivity := time.Date(2026, 3, 2, 10, 0, 0, 0, time.UTC)
	h.store.candidates = []application.AlertCandidate{{
		Ticket: h.assigned(1), LastActivityAt: lastActivity,
	}}
	h.passAt(t, lastActivity.AddDate(0, 0, 2).Add(-time.Minute))
	if got := h.kinds(); len(got) != 0 {
		t.Fatalf("kinds = %v, want none one minute before the window closes", got)
	}
	h.passAt(t, lastActivity.AddDate(0, 0, 2))
	h.passAt(t, lastActivity.AddDate(0, 0, 3))

	if got := h.kinds(); len(got) != 1 || got[0] != domain.NotificationNoMovement {
		t.Fatalf("kinds = %v, want exactly one %q", got, domain.NotificationNoMovement)
	}
}

// S6: fresh activity restarts the window, and the NEXT silence is alerted
// about again — the alert is scoped to its period, never to the ticket.
func TestAlertPassReportsNoMovementAgainAfterFreshActivity(t *testing.T) {
	h := newAlertHarness()
	lastActivity := time.Date(2026, 3, 2, 10, 0, 0, 0, time.UTC)
	h.store.candidates = []application.AlertCandidate{{
		Ticket: h.assigned(1), LastActivityAt: lastActivity,
	}}
	h.passAt(t, lastActivity.AddDate(0, 0, 2))

	// The agent answers: the candidate now reports a newer activity instant.
	replied := lastActivity.AddDate(0, 0, 3)
	h.store.candidates[0].LastActivityAt = replied
	h.passAt(t, replied.Add(time.Hour))
	if got := h.kinds(); len(got) != 1 {
		t.Fatalf("kinds = %v, want the window to have restarted", got)
	}
	// The new deadline is two WORKING days after the reply, which the weekend
	// pushes into the following week; asking the rule avoids re-deriving it.
	h.passAt(t, domain.NoMovementAt(replied, domain.DefaultSLACalendar()))

	want := []domain.NotificationKind{domain.NotificationNoMovement, domain.NotificationNoMovement}
	if got := h.kinds(); len(got) != len(want) {
		t.Fatalf("kinds = %v, want %v", got, want)
	}
}

// S5: a ticket waiting on its requester accumulates no inactivity — the
// silence is the requester's, and the hold has its own reminder and expiry.
func TestAlertPassNeverReportsNoMovementWhileHeld(t *testing.T) {
	h := newAlertHarness()
	lastActivity := time.Date(2026, 3, 2, 10, 0, 0, 0, time.UTC)
	held := h.assigned(1)
	held.OnHoldSince = &lastActivity
	h.store.candidates = []application.AlertCandidate{{
		Ticket: held, LastActivityAt: lastActivity,
	}}
	h.passAt(t, lastActivity.AddDate(0, 0, 10))

	if got := h.kinds(); len(got) != 0 {
		t.Fatalf("kinds = %v, want none while the ticket waits for the requester", got)
	}
}

// S8: a failing read aborts the pass instead of alerting on partial
// information; the next tick starts over.
func TestAlertPassAbortsWhenTheCandidateReadFails(t *testing.T) {
	h := newAlertHarness()
	boom := errors.New("database is gone")
	h.store.err = boom
	h.clock.now = alertStart()

	err := h.svc.Pass(context.Background())
	if !errors.Is(err, boom) {
		t.Fatalf("Pass error = %v, want it to wrap %v", err, boom)
	}
	if got := h.kinds(); len(got) != 0 {
		t.Fatalf("kinds = %v, want none after a failed read", got)
	}
}

// S8: the pass keyset-pages through every candidate, so a full first page
// never hides the tickets behind it.
func TestAlertPassWalksEveryPageOfCandidates(t *testing.T) {
	h := newAlertHarness()
	started := alertStart()
	for id := int64(1); id <= application.AlertCandidatesPageSize+5; id++ {
		h.store.candidates = append(h.store.candidates, application.AlertCandidate{
			Ticket: h.assigned(id), SLA: alertFrozen(started), LastActivityAt: started,
		})
	}
	h.passAt(t, started.Add(10*time.Hour))

	// Every candidate is long past both commitments, so each one is alerted
	// about twice; what this case proves is that no ticket was left behind by
	// the paging, so it counts the DISTINCT tickets reached.
	alerted := map[int64]bool{}
	for _, n := range h.tx.notifications {
		alerted[n.TicketID] = true
	}
	if len(alerted) != application.AlertCandidatesPageSize+5 {
		t.Fatalf("alerted tickets = %d, want %d", len(alerted), application.AlertCandidatesPageSize+5)
	}
}
