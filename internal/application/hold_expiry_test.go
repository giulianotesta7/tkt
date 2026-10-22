package application_test

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// The automatic hold expiry pass (issue #353, specs S1-S5): when the requester
// never replies to a ticket on hold, the instance warns them one working day
// before the deadline (S3) and then closes the ticket itself (S1), telling the
// requester and the assignee (S4). Every test drives the pass with an injected
// clock and asserts what the UI reads: the stored ticket, the audit timeline,
// the notifications, and the frozen SLA. No sleeps, no wall clock.

// holdExpiryCalendar is the instance calendar these tests run on: the shipped
// default (Monday-Friday, 09:00-18:00 UTC), so a weekend is really skipped.
func holdExpiryCalendar() domain.SLACalendar { return domain.DefaultSLACalendar() }

// holdExpiryHarness wires the pass with the collaborators it composes: the
// held-ticket read, the guarded unit of work, the user lookup for notification
// recipients, the SLA service (whose ShiftForHold the close reuses), and the
// settings that carry the window and the calendar.
type holdExpiryHarness struct {
	svc      *application.HoldExpiryService
	store    *fakeHoldExpiryStore
	tickets  *fakeTicketStore
	users    *fakeUserStore
	audits   *fakeAuditStore
	tx       *fakeUnitOfWork
	clock    *fakeClock
	settings *fakeSLASettingsStore
	slaStore *fakeSLAStore
}

func newHoldExpiryHarness() *holdExpiryHarness {
	clock := fixedClock()
	users := newFakeUserStore()
	tickets := newFakeTicketStore()
	audits := newFakeAuditStore()
	tx := newFakeUnitOfWork(tickets, audits)
	slaStore := &fakeSLAStore{frozen: map[int64]*domain.TicketSLA{}}
	settings := &fakeSLASettingsStore{calendar: holdExpiryCalendar()}
	// The unit-of-work fake persists a shifted commitment only when an SLA store
	// is attached (documented simplification in fakes_test.go).
	tx.slas = slaStore
	sla := application.NewSLAService(slaStore, settings, clock)
	store := &fakeHoldExpiryStore{tickets: tickets, tx: tx}
	h := &holdExpiryHarness{
		store: store, tickets: tickets, users: users, audits: audits, tx: tx,
		clock: clock, settings: settings, slaStore: slaStore,
	}
	h.svc = application.NewHoldExpiryService(store, tx, users, sla, settings, clock)
	return h
}

// seedHeld seeds one ticket on hold with a reachable requester and an assigned
// agent, and returns the ticket with both recipients. The hold instant is the
// fixture instant unless a test says otherwise: the arrange step writes the
// stored state directly, never through the pass under test.
func (h *holdExpiryHarness) seedHeld(t *testing.T, state domain.State, since time.Time) (domain.Ticket, domain.User, domain.User) {
	t.Helper()
	requester := h.users.seedRole("Ana", "ana@example.com", domain.RoleUser, true)
	assignee := h.users.seedRole("Bea", "bea@example.com", domain.RoleAgent, true)
	h.users.seedRole("Cid", "cid@example.com", domain.RoleAgent, true) // an unrelated agent: nobody's recipient
	ticket := seededTicket(h.tickets, 1, state)
	ticket.RequesterUserID = &requester.ID
	ticket.UserID = &assignee.ID
	ticket.OnHoldSince = &since
	ticket.OnHoldReason = "waiting for the requester"
	h.tickets.store(&ticket)
	return ticket, requester, assignee
}

func (h *holdExpiryHarness) stored(t *testing.T, id int64) domain.Ticket {
	t.Helper()
	ticket, ok := h.tickets.tickets[id]
	if !ok {
		t.Fatalf("ticket %d is not stored", id)
	}
	return *ticket
}

// notificationsFor returns the committed notification rows of one ticket, in
// commit order.
func (h *holdExpiryHarness) notificationsFor(id int64) []domain.Notification {
	var out []domain.Notification
	for _, n := range h.tx.notifications {
		if n.TicketID == id {
			out = append(out, n)
		}
	}
	return out
}

// at sets the injected clock, so the pass sees exactly the instant under test.
func (h *holdExpiryHarness) at(instant time.Time) { h.clock.now = instant }

// TestHoldExpiryPassDoesNothingWhenDisabled pins the off state: a zero window
// means the instance never closes a held ticket on its own, so a pass writes
// nothing at all.
func TestHoldExpiryPassDoesNothingWhenDisabled(t *testing.T) {
	h := newHoldExpiryHarness()
	since := h.clock.now
	ticket, _, _ := h.seedHeld(t, domain.StateInProgress, since)
	h.settings.holdAutoCloseDays = 0
	h.at(since.Add(30 * 24 * time.Hour)) // far past any window

	if err := h.svc.Pass(context.Background()); err != nil {
		t.Fatalf("Pass: %v", err)
	}
	if h.tx.updateHoldCalls != 0 || h.tx.remindHoldCalls != 0 {
		t.Errorf("disabled pass wrote (%d hold writes, %d reminders), want none", h.tx.updateHoldCalls, h.tx.remindHoldCalls)
	}
	if got := h.stored(t, ticket.ID); got.State != domain.StateInProgress || got.OnHoldSince == nil {
		t.Errorf("ticket = %q on hold %v, want it left exactly as it was", got.State, got.OnHoldSince)
	}
	if len(h.tx.notifications) != 0 {
		t.Errorf("notifications = %d, want none", len(h.tx.notifications))
	}
}

// TestHoldExpiryPassSendsNothingBeforeTheReminderInstant pins the wait: with a
// three-day window, one working day in is neither the deadline nor the
// reminder, so the pass stays silent.
func TestHoldExpiryPassSendsNothingBeforeTheReminderInstant(t *testing.T) {
	h := newHoldExpiryHarness()
	since := time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC) // Monday
	ticket, _, _ := h.seedHeld(t, domain.StateInProgress, since)
	h.settings.holdAutoCloseDays = 3
	h.at(time.Date(2026, 8, 11, 10, 0, 0, 0, time.UTC)) // Tuesday: two working days early

	if err := h.svc.Pass(context.Background()); err != nil {
		t.Fatalf("Pass: %v", err)
	}
	if h.tx.remindHoldCalls != 0 || h.tx.updateHoldCalls != 0 {
		t.Errorf("pass wrote (%d reminders, %d hold writes), want none", h.tx.remindHoldCalls, h.tx.updateHoldCalls)
	}
	if got := h.stored(t, ticket.ID); got.State != domain.StateInProgress {
		t.Errorf("state = %q, want in_progress", got.State)
	}
}

// TestHoldExpiryPassSendsOneReminderPerHold pins S3: the reminder reaches the
// REQUESTER only, as a hold_reminder row, and a second pass over the same hold
// sends nothing more.
func TestHoldExpiryPassSendsOneReminderPerHold(t *testing.T) {
	h := newHoldExpiryHarness()
	since := time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC) // Monday
	ticket, requester, _ := h.seedHeld(t, domain.StateInProgress, since)
	h.settings.holdAutoCloseDays = 3
	// The reminder is one working day before Thursday 10:00: Wednesday 10:00.
	h.at(time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC))

	if err := h.svc.Pass(context.Background()); err != nil {
		t.Fatalf("Pass: %v", err)
	}
	rows := h.notificationsFor(ticket.ID)
	if len(rows) != 1 {
		t.Fatalf("notifications = %d, want the single reminder", len(rows))
	}
	reminder := rows[0]
	if reminder.Kind != domain.NotificationHoldReminder {
		t.Errorf("kind = %q, want %q", reminder.Kind, domain.NotificationHoldReminder)
	}
	if reminder.UserID != requester.ID {
		t.Errorf("recipient = %d, want the requester %d (the assignee is not reminded)", reminder.UserID, requester.ID)
	}
	if reminder.ActorUserID != nil {
		t.Errorf("ActorUserID = %v, want nil (a system action has no user actor)", *reminder.ActorUserID)
	}
	if !reminder.CreatedAt.Equal(h.clock.now) {
		t.Errorf("CreatedAt = %v, want the pass instant %v", reminder.CreatedAt, h.clock.now)
	}
	if reminder.FromValue != nil || reminder.ToValue != nil || reminder.CommentID != nil || reminder.StepIndex != nil {
		t.Errorf("reminder carries change facts (%+v), want kind-only", reminder)
	}
	if got := h.stored(t, ticket.ID); got.State != domain.StateInProgress || got.OnHoldSince == nil {
		t.Errorf("ticket = %q on hold %v, want it untouched by a reminder", got.State, got.OnHoldSince)
	}
	if len(h.audits.events[ticket.ID]) != 0 {
		t.Errorf("audit events = %d, want none (a reminder changes nothing)", len(h.audits.events[ticket.ID]))
	}

	// The next pass, still inside the window, sends nothing: the reminder lives
	// in the store, not in the pass's memory.
	h.at(h.clock.now.Add(time.Hour))
	if err := h.svc.Pass(context.Background()); err != nil {
		t.Fatalf("second Pass: %v", err)
	}
	if got := len(h.notificationsFor(ticket.ID)); got != 1 {
		t.Errorf("notifications after a second pass = %d, want the same single reminder", got)
	}
}

// TestHoldExpiryPassSendsNoReminderForAOneWorkingDayWindow pins the N = 1
// rule: there is no working day between the pause and the deadline, so the
// requester is never warned — the ticket simply closes at the deadline.
func TestHoldExpiryPassSendsNoReminderForAOneWorkingDayWindow(t *testing.T) {
	h := newHoldExpiryHarness()
	since := time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC) // Monday
	ticket, _, _ := h.seedHeld(t, domain.StateInProgress, since)
	h.settings.holdAutoCloseDays = 1
	h.at(since.Add(time.Hour)) // past the pause: were a reminder ever due, it would be now

	if err := h.svc.Pass(context.Background()); err != nil {
		t.Fatalf("Pass: %v", err)
	}
	if h.tx.remindHoldCalls != 0 {
		t.Errorf("reminders = %d for a one-working-day window, want 0", h.tx.remindHoldCalls)
	}
	if len(h.notificationsFor(ticket.ID)) != 0 {
		t.Errorf("notifications = %d, want none", len(h.notificationsFor(ticket.ID)))
	}
	if got := h.stored(t, ticket.ID); got.State != domain.StateInProgress {
		t.Errorf("state = %q, want it still open before the deadline", got.State)
	}
}

// TestHoldExpiryPassWritesNoReminderWithoutAReachableRequester pins the empty
// batch rule: a hold the system cannot tell anyone about is NOT marked as
// reminded, so nothing is written and the next pass tries again.
func TestHoldExpiryPassWritesNoReminderWithoutAReachableRequester(t *testing.T) {
	h := newHoldExpiryHarness()
	since := time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC) // Monday
	assignee := h.users.seedRole("Bea", "bea@example.com", domain.RoleAgent, true)
	ticket := seededTicket(h.tickets, 1, domain.StateInProgress)
	// A legacy ticket with no provable requester user: nobody owns it, so
	// nobody can be told about it.
	ticket.RequesterUserID = nil
	ticket.UserID = &assignee.ID
	ticket.OnHoldSince = &since
	h.tickets.store(&ticket)
	h.settings.holdAutoCloseDays = 2
	h.at(time.Date(2026, 8, 11, 10, 0, 0, 0, time.UTC)) // the reminder instant

	if err := h.svc.Pass(context.Background()); err != nil {
		t.Fatalf("Pass: %v", err)
	}
	if h.tx.remindHoldCalls != 0 {
		t.Errorf("reminders written = %d, want 0 (no reachable requester)", h.tx.remindHoldCalls)
	}
	if got := len(h.notificationsFor(ticket.ID)); got != 0 {
		t.Errorf("notifications = %d, want none", got)
	}
	if got := h.stored(t, ticket.ID); got.OnHoldSince == nil {
		t.Error("the hold was cleared by a reminder that was never sent")
	}
}

// TestHoldExpiryPassClosesAtTheDeadline pins S1/S4/S5 in one pass: at the
// deadline the ticket is resumed, resolved and closed by the system, the
// resolution SLA is shifted by the working time the hold consumed, and the
// requester and the assignee — and nobody else — learn it closed.
func TestHoldExpiryPassClosesAtTheDeadline(t *testing.T) {
	h := newHoldExpiryHarness()
	since := time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC) // Thursday
	ticket, requester, assignee := h.seedHeld(t, domain.StateInProgress, since)
	h.settings.holdAutoCloseDays = 2
	h.slaStore.frozen[ticket.ID] = ptr(holdFrozenSLA(since))
	deadline := time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC) // Monday: two working days later
	h.at(deadline)

	if err := h.svc.Pass(context.Background()); err != nil {
		t.Fatalf("Pass: %v", err)
	}

	got := h.stored(t, ticket.ID)
	if got.State != domain.StateClosed {
		t.Errorf("state = %q, want closed", got.State)
	}
	if got.OnHoldSince != nil || got.OnHoldReason != "" {
		t.Errorf("hold = %v/%q, want it ended by the close", got.OnHoldSince, got.OnHoldReason)
	}
	if got.ResolvedAt == nil || !got.ResolvedAt.Equal(deadline) {
		t.Errorf("ResolvedAt = %v, want %v", got.ResolvedAt, deadline)
	}
	if got.ClosedAt == nil || !got.ClosedAt.Equal(deadline) {
		t.Errorf("ClosedAt = %v, want %v", got.ClosedAt, deadline)
	}

	// The timeline records the whole automatic close and why (S5): the resume,
	// the resolve and the closure, all attributed to the system with no user id.
	events := h.audits.events[ticket.ID]
	if len(events) != 3 {
		t.Fatalf("audit events = %d, want resume + resolve + close", len(events))
	}
	if events[0].Action != domain.ActionResume {
		t.Errorf("event[0] action = %q, want %q", events[0].Action, domain.ActionResume)
	}
	assertAutomaticTransition(t, events[1], "in_progress", "resolved", nil)
	assertAutomaticTransition(t, events[2], "resolved", "closed", ptr(domain.ClosureViaHoldExpired))
	for i, ev := range events {
		if ev.Actor != domain.AutomaticActorSystem {
			t.Errorf("event[%d] actor = %q, want %q", i, ev.Actor, domain.AutomaticActorSystem)
		}
		if ev.ActorUserID != nil {
			t.Errorf("event[%d] actor user id = %d, want nil (nobody performed this)", i, *ev.ActorUserID)
		}
		if !ev.CreatedAt.Equal(deadline) {
			t.Errorf("event[%d] created at = %v, want %v", i, ev.CreatedAt, deadline)
		}
	}

	// S4: the requester and the assignee, once each, and nobody else.
	rows := h.notificationsFor(ticket.ID)
	if len(rows) != 2 {
		t.Fatalf("notifications = %d, want the requester and the assignee", len(rows))
	}
	recipients := []int64{rows[0].UserID, rows[1].UserID}
	sort.Slice(recipients, func(i, j int) bool { return recipients[i] < recipients[j] })
	want := []int64{requester.ID, assignee.ID}
	sort.Slice(want, func(i, j int) bool { return want[i] < want[j] })
	if recipients[0] != want[0] || recipients[1] != want[1] {
		t.Errorf("recipients = %v, want %v", recipients, want)
	}
	for _, n := range rows {
		if n.Kind != domain.NotificationHoldClosed {
			t.Errorf("kind = %q, want %q (no other news is produced by the close)", n.Kind, domain.NotificationHoldClosed)
		}
		if n.ActorUserID != nil {
			t.Errorf("ActorUserID = %d, want nil", *n.ActorUserID)
		}
		if n.FromValue != nil || n.ToValue != nil {
			t.Errorf("hold_closed carries change facts (%q→%q), want kind-only", deref(n.FromValue), deref(n.ToValue))
		}
	}

	// The resolution instants moved by the working time the hold consumed; the
	// first-response instants never move.
	shifted := h.slaStore.frozen[ticket.ID]
	if shifted == nil {
		t.Fatal("no frozen SLA after the close")
	}
	if want := time.Date(2026, 8, 10, 18, 0, 0, 0, time.UTC); !shifted.DueResolveAt.Equal(want) {
		t.Errorf("DueResolveAt = %v, want %v (8 worked hours of Thursday re-applied from Monday 10:00)", shifted.DueResolveAt, want)
	}
	if want := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC); !shifted.WarnResolveAt.Equal(want) {
		t.Errorf("WarnResolveAt = %v, want %v", shifted.WarnResolveAt, want)
	}
	frozen := holdFrozenSLA(since)
	if !shifted.DueFirstResponseAt.Equal(frozen.DueFirstResponseAt) {
		t.Errorf("DueFirstResponseAt = %v, want the frozen %v (the first-response clock never pauses)", shifted.DueFirstResponseAt, frozen.DueFirstResponseAt)
	}
}

// assertAutomaticTransition asserts one automatic transition event: the state
// change and, when the close path requires it, its closure attribution.
func assertAutomaticTransition(t *testing.T, ev domain.AuditEvent, from, to string, closureVia *string) {
	t.Helper()
	if ev.Action != domain.ActionTransition {
		t.Fatalf("action = %q, want %q", ev.Action, domain.ActionTransition)
	}
	if ev.FromValue == nil || *ev.FromValue != from {
		t.Errorf("from = %v, want %q", deref(ev.FromValue), from)
	}
	if ev.ToValue == nil || *ev.ToValue != to {
		t.Errorf("to = %v, want %q", deref(ev.ToValue), to)
	}
	if closureVia == nil {
		if ev.ClosureVia != nil {
			t.Errorf("closure_via = %q, want nil", *ev.ClosureVia)
		}
		return
	}
	if ev.ClosureVia == nil || *ev.ClosureVia != *closureVia {
		t.Errorf("closure_via = %v, want %q", deref(ev.ClosureVia), *closureVia)
	}
}

func deref(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

// TestHoldExpiryPassSkipsTheCloseWhenTheRequesterReplied pins the race S5
// decides in the requester's favour: the pass read a hold the reply already
// ended, so the guarded write refuses it and NOTHING is written — no close, no
// audit, no notification.
func TestHoldExpiryPassSkipsTheCloseWhenTheRequesterReplied(t *testing.T) {
	h := newHoldExpiryHarness()
	since := time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC) // Thursday
	ticket, _, _ := h.seedHeld(t, domain.StateInProgress, since)
	h.settings.holdAutoCloseDays = 2
	h.at(time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC)) // the deadline
	// The requester replied between the pass's read and its write: the stored
	// hold is gone, while the read still carries it.
	h.tickets.store(&domain.Ticket{ID: ticket.ID, Number: ticket.Number, Title: ticket.Title,
		CategoryID: ticket.CategoryID, Priority: ticket.Priority, State: domain.StateInProgress,
		RequesterUserID: ticket.RequesterUserID, UserID: ticket.UserID,
		CreatedAt: ticket.CreatedAt, UpdatedAt: h.clock.now})

	if err := h.svc.Pass(context.Background()); err != nil {
		t.Fatalf("Pass: %v", err)
	}
	got := h.stored(t, ticket.ID)
	if got.State != domain.StateInProgress || got.OnHoldSince != nil {
		t.Errorf("ticket = %q on hold %v, want the reply's outcome untouched", got.State, got.OnHoldSince)
	}
	if len(h.audits.events[ticket.ID]) != 0 {
		t.Errorf("audit events = %d, want none", len(h.audits.events[ticket.ID]))
	}
	if len(h.notificationsFor(ticket.ID)) != 0 {
		t.Errorf("notifications = %d, want none", len(h.notificationsFor(ticket.ID)))
	}
	if h.slaStore.frozen[ticket.ID] != nil {
		t.Error("the SLA was shifted by a close that never applied")
	}
}

// TestHoldExpiryPassClosesANewTicketToo proves the close does not depend on
// the ticket having been worked on: new -> resolved -> closed is legal, so a
// ticket put on hold straight from new closes on the same deadline.
func TestHoldExpiryPassClosesANewTicketToo(t *testing.T) {
	h := newHoldExpiryHarness()
	since := time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC) // Thursday
	ticket, _, _ := h.seedHeld(t, domain.StateNew, since)
	h.settings.holdAutoCloseDays = 1
	h.at(time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)) // Friday: one working day later

	if err := h.svc.Pass(context.Background()); err != nil {
		t.Fatalf("Pass: %v", err)
	}
	if got := h.stored(t, ticket.ID); got.State != domain.StateClosed {
		t.Errorf("state = %q, want closed", got.State)
	}
	events := h.audits.events[ticket.ID]
	if len(events) != 3 {
		t.Fatalf("audit events = %d, want resume + resolve + close", len(events))
	}
	assertAutomaticTransition(t, events[1], "new", "resolved", nil)
	assertAutomaticTransition(t, events[2], "resolved", "closed", ptr(domain.ClosureViaHoldExpired))
}

// TestHoldExpiryPassRespectsWeekends pins that the window counts WORKING days
// on the instance calendar: a Friday hold is reminded on Monday and closed on
// Tuesday, never over the weekend.
func TestHoldExpiryPassRespectsWeekends(t *testing.T) {
	h := newHoldExpiryHarness()
	since := time.Date(2026, 8, 14, 10, 0, 0, 0, time.UTC) // Friday
	ticket, _, _ := h.seedHeld(t, domain.StateInProgress, since)
	h.settings.holdAutoCloseDays = 2

	// Saturday and Sunday: nothing happens at all.
	for _, day := range []time.Time{
		time.Date(2026, 8, 15, 10, 0, 0, 0, time.UTC),
		time.Date(2026, 8, 16, 10, 0, 0, 0, time.UTC),
	} {
		h.at(day)
		if err := h.svc.Pass(context.Background()); err != nil {
			t.Fatalf("Pass on %v: %v", day, err)
		}
	}
	if h.tx.remindHoldCalls != 0 || h.tx.updateHoldCalls != 0 {
		t.Fatalf("the weekend produced %d reminders and %d closes, want none", h.tx.remindHoldCalls, h.tx.updateHoldCalls)
	}

	// Monday 10:00 is one working day in: the reminder is due.
	h.at(time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC))
	if err := h.svc.Pass(context.Background()); err != nil {
		t.Fatalf("Pass on Monday: %v", err)
	}
	if got := len(h.notificationsFor(ticket.ID)); got != 1 {
		t.Fatalf("notifications on Monday = %d, want the reminder", got)
	}
	if got := h.stored(t, ticket.ID); got.State != domain.StateInProgress {
		t.Fatalf("state on Monday = %q, want in_progress", got.State)
	}

	// Tuesday 10:00 is the second working day: the deadline.
	h.at(time.Date(2026, 8, 18, 10, 0, 0, 0, time.UTC))
	if err := h.svc.Pass(context.Background()); err != nil {
		t.Fatalf("Pass on Tuesday: %v", err)
	}
	if got := h.stored(t, ticket.ID); got.State != domain.StateClosed {
		t.Errorf("state on Tuesday = %q, want closed", got.State)
	}
}

// TestHoldExpiryPassAbortsOnAReadFailure pins the failure rule: only the
// requester-replied conflict is swallowed; any other failure aborts the pass
// instead of closing tickets on partial information.
func TestHoldExpiryPassAbortsOnAReadFailure(t *testing.T) {
	h := newHoldExpiryHarness()
	h.settings.holdAutoCloseDays = 2
	h.store.heldErr = errHeldTickets
	if err := h.svc.Pass(context.Background()); !errors.Is(err, errHeldTickets) {
		t.Fatalf("Pass error = %v, want the store failure", err)
	}
}

// TestHoldExpiryRunPassesOnceThenStopsOnCancellation pins the loop's shape
// without a sleep: an already-cancelled context performs one pass and returns,
// so the background runner is the same interval loop the email sender uses.
func TestHoldExpiryRunPassesOnceThenStopsOnCancellation(t *testing.T) {
	h := newHoldExpiryHarness()
	since := time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC)
	ticket, _, _ := h.seedHeld(t, domain.StateInProgress, since)
	h.settings.holdAutoCloseDays = 1
	h.at(time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan struct{})
	go func() {
		h.svc.Run(ctx, time.Minute)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after its context was cancelled")
	}
	if got := h.stored(t, ticket.ID); got.State != domain.StateClosed {
		t.Errorf("state = %q, want the pass to have run once before the loop stopped", got.State)
	}
}

// TestHoldExpiryPassReachesTicketsBeyondTheFirstPage pins that the pass pages
// through every held ticket: a first page full of holds that are not due yet
// must not hide a due one behind it (they never leave the list on their own).
func TestHoldExpiryPassReachesTicketsBeyondTheFirstPage(t *testing.T) {
	h := newHoldExpiryHarness()
	h.settings.holdAutoCloseDays = 2
	deadline := time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC) // Monday
	for i := 0; i < application.HeldTicketsPageSize+20; i++ {
		h.seedHeld(t, domain.StateInProgress, deadline) // held just now: not due
	}
	due, _, _ := h.seedHeld(t, domain.StateInProgress, time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC)) // Thursday
	h.at(deadline)

	if err := h.svc.Pass(context.Background()); err != nil {
		t.Fatalf("Pass: %v", err)
	}
	if got := h.stored(t, due.ID); got.State != domain.StateClosed {
		t.Errorf("due ticket %d state = %q, want closed even behind a full page", due.ID, got.State)
	}
}
