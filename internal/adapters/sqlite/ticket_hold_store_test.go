package sqlite

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// Ticket hold persistence (issue #352): the hold columns of migration 0022
// are written only by TicketUnitOfWork.UpdateHold, under a compare-and-set
// on on_hold_since, together with the shifted resolution SLA, the optional
// requester comment, and the audit events — one transaction.

// anyTicket is the unrestricted ticket read: an EMPTY TicketQuery is the
// no-access scope ("0 = 1" in scopeClause, ticket-access spec), so a store
// read that means "whatever is stored" must say ScopeAll explicitly.
var anyTicket = application.TicketQuery{Scope: application.ScopeAll}

// holdFixture seeds an open ticket with a frozen SLA and returns it read
// back through the store.
func holdFixture(t *testing.T, s *Store) (*domain.Ticket, domain.TicketSLA) {
	t.Helper()
	ctx := context.Background()
	id := seedTicketForTimeline(t, s, 1)
	frozen := domain.TicketSLA{
		FirstResponseSeconds: 1800,
		ResolveSeconds:       14400,
		WarnFirstResponseAt:  testClock.Add(24 * time.Minute),
		DueFirstResponseAt:   testClock.Add(30 * time.Minute),
		WarnResolveAt:        testClock.Add(192 * time.Minute),
		DueResolveAt:         testClock.Add(4 * time.Hour),
		StartedAt:            testClock,
		PolicySnapshotAt:     testClock,
	}
	if err := s.SLAStore().InsertTicketSLA(ctx, id, frozen); err != nil {
		t.Fatalf("insert ticket sla: %v", err)
	}
	tk, err := s.TicketStore().GetByID(ctx, id, anyTicket)
	if err != nil {
		t.Fatalf("get ticket: %v", err)
	}
	return tk, frozen
}

func holdEvent(ticketID int64, action string, at time.Time) domain.AuditEvent {
	field := "on_hold"
	return domain.AuditEvent{TicketID: ticketID, Actor: "Ana", Action: action, Field: &field, CreatedAt: at}
}

func countRows(t *testing.T, s *Store, query string, args ...any) int {
	t.Helper()
	var n int
	if err := s.db.QueryRowContext(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", query, err)
	}
	return n
}

func putOnHold(t *testing.T, s *Store, tk *domain.Ticket, at time.Time, reason string) {
	t.Helper()
	ev, err := tk.PutOnHold(reason, at)
	if err != nil {
		t.Fatalf("PutOnHold: %v", err)
	}
	if err := s.TicketUnitOfWork().UpdateHold(context.Background(), application.HoldWrite{Ticket: tk, Events: []domain.AuditEvent{*ev}}); err != nil {
		t.Fatalf("UpdateHold put on hold: %v", err)
	}
}

func TestUpdateHoldPutsTicketOnHold(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	tk, frozen := holdFixture(t, s)
	at := testClock.Add(time.Hour)
	putOnHold(t, s, tk, at, "waiting for the serial number")

	got, err := s.TicketStore().GetByID(ctx, tk.ID, anyTicket)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.OnHoldSince == nil || !got.OnHoldSince.Equal(at) || got.OnHoldReason != "waiting for the serial number" {
		t.Errorf("hold = %v %q, want %v %q", got.OnHoldSince, got.OnHoldReason, at, "waiting for the serial number")
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM audit_events WHERE ticket_id=? AND action='hold' AND note='waiting for the serial number'`, tk.ID); n != 1 {
		t.Errorf("hold audit rows = %d, want 1", n)
	}
	sla, err := s.SLAStore().TicketSLA(ctx, tk.ID)
	if err != nil || sla == nil || *sla != frozen {
		t.Errorf("putting on hold changed the frozen SLA: %+v, %v", sla, err)
	}
}

func TestUpdateHoldResumeShiftsResolutionSLA(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	tk, frozen := holdFixture(t, s)
	pausedAt := testClock.Add(time.Hour)
	putOnHold(t, s, tk, pausedAt, "waiting")

	since := *tk.OnHoldSince
	resumedAt := pausedAt.Add(24 * time.Hour)
	if _, _, err := tk.ResumeFromHold(resumedAt); err != nil {
		t.Fatalf("ResumeFromHold: %v", err)
	}
	shifted := frozen
	shifted.WarnResolveAt = frozen.WarnResolveAt.Add(24 * time.Hour)
	shifted.DueResolveAt = frozen.DueResolveAt.Add(24 * time.Hour)
	if err := s.TicketUnitOfWork().UpdateHold(ctx, application.HoldWrite{
		Ticket: tk, ExpectedOnHoldSince: &since, ShiftedSLA: &shifted,
		Events: []domain.AuditEvent{holdEvent(tk.ID, domain.ActionResume, resumedAt)},
	}); err != nil {
		t.Fatalf("UpdateHold resume: %v", err)
	}

	got, _ := s.TicketStore().GetByID(ctx, tk.ID, anyTicket)
	if got.OnHoldSince != nil || got.OnHoldReason != "" {
		t.Errorf("hold not cleared: %v %q", got.OnHoldSince, got.OnHoldReason)
	}
	sla, _ := s.SLAStore().TicketSLA(ctx, tk.ID)
	if sla == nil || *sla != shifted {
		t.Errorf("sla = %+v, want %+v (only the resolution instants move)", sla, shifted)
	}
}

func TestUpdateHoldStaleExpectationWritesNothing(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	tk, frozen := holdFixture(t, s)
	putOnHold(t, s, tk, testClock.Add(time.Hour), "waiting")

	// A second actor read the ticket before the hold and now writes a hold
	// of its own from that stale read: the stored hold is not nil any more.
	stale, _ := s.TicketStore().GetByID(ctx, tk.ID, anyTicket)
	stale.OnHoldSince = nil
	stale.OnHoldReason = ""
	ev, err := stale.PutOnHold("another reason", testClock.Add(2*time.Hour))
	if err != nil {
		t.Fatalf("PutOnHold: %v", err)
	}
	shifted := frozen
	shifted.DueResolveAt = frozen.DueResolveAt.Add(time.Hour)
	auditBefore := countRows(t, s, `SELECT COUNT(*) FROM audit_events WHERE ticket_id=?`, tk.ID)

	err = s.TicketUnitOfWork().UpdateHold(ctx, application.HoldWrite{
		Ticket: stale, ShiftedSLA: &shifted, Events: []domain.AuditEvent{*ev},
	})
	var conflict *domain.HoldConflictError
	if !errors.As(err, &conflict) || !errors.Is(err, domain.ErrHoldConflict) {
		t.Fatalf("err = %v, want HoldConflictError", err)
	}
	got, _ := s.TicketStore().GetByID(ctx, tk.ID, anyTicket)
	if got.OnHoldReason != "waiting" || !got.OnHoldSince.Equal(testClock.Add(time.Hour)) {
		t.Errorf("hold overwritten: %v %q", got.OnHoldSince, got.OnHoldReason)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM audit_events WHERE ticket_id=?`, tk.ID); n != auditBefore {
		t.Errorf("audit rows = %d, want %d", n, auditBefore)
	}
	if sla, _ := s.SLAStore().TicketSLA(ctx, tk.ID); sla == nil || *sla != frozen {
		t.Errorf("sla changed by a refused write: %+v", sla)
	}
}

func TestUpdateHoldIsAtomic(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	tk, frozen := holdFixture(t, s)
	putOnHold(t, s, tk, testClock.Add(time.Hour), "waiting")
	since := *tk.OnHoldSince
	at := testClock.Add(3 * time.Hour)
	if _, _, err := tk.ResumeFromHold(at); err != nil {
		t.Fatalf("ResumeFromHold: %v", err)
	}
	shifted := frozen
	shifted.DueResolveAt = frozen.DueResolveAt.Add(2 * time.Hour)

	// The audit append fails: the cleared hold and the shifted SLA both roll
	// back with it.
	if _, err := s.db.ExecContext(ctx, `CREATE TRIGGER trg_test_fail_resume BEFORE INSERT ON audit_events
		WHEN NEW.action = 'resume' BEGIN SELECT RAISE(ABORT, 'injected resume failure'); END`); err != nil {
		t.Fatalf("inject: %v", err)
	}
	err := s.TicketUnitOfWork().UpdateHold(ctx, application.HoldWrite{
		Ticket: tk, ExpectedOnHoldSince: &since, ShiftedSLA: &shifted,
		Events: []domain.AuditEvent{holdEvent(tk.ID, domain.ActionResume, at)},
	})
	if err == nil {
		t.Fatal("UpdateHold succeeded despite the injected audit failure")
	}
	got, _ := s.TicketStore().GetByID(ctx, tk.ID, anyTicket)
	if got.OnHoldSince == nil {
		t.Error("hold cleared by a rolled-back write")
	}
	if sla, _ := s.SLAStore().TicketSLA(ctx, tk.ID); sla == nil || *sla != frozen {
		t.Errorf("sla changed by a rolled-back write: %+v", sla)
	}

	if _, err := s.db.ExecContext(ctx, `DROP TRIGGER trg_test_fail_resume`); err != nil {
		t.Fatalf("drop trigger: %v", err)
	}
	if err := s.TicketUnitOfWork().UpdateHold(ctx, application.HoldWrite{
		Ticket: tk, ExpectedOnHoldSince: &since, ShiftedSLA: &shifted,
		Events: []domain.AuditEvent{holdEvent(tk.ID, domain.ActionResume, at)},
	}); err != nil {
		t.Fatalf("UpdateHold: %v", err)
	}
	got, _ = s.TicketStore().GetByID(ctx, tk.ID, anyTicket)
	if got.OnHoldSince != nil {
		t.Errorf("hold still set after the successful retry: %v", got.OnHoldSince)
	}
	if sla, _ := s.SLAStore().TicketSLA(ctx, tk.ID); sla == nil || *sla != shifted {
		t.Errorf("sla = %+v, want %+v after the successful retry", sla, shifted)
	}
}

// TestUpdateNeverWritesHoldColumns pins that the generic update path owns
// no hold state: a stale ticket read before the hold keeps the hold intact
// when it writes an ordinary edit.
func TestUpdateNeverWritesHoldColumns(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	tk, _ := holdFixture(t, s)
	stale := *tk
	putOnHold(t, s, tk, testClock.Add(time.Hour), "waiting")

	stale.Title = "renamed from a stale read"
	if err := s.TicketUnitOfWork().Update(ctx, application.TicketMutation{Ticket: &stale}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, _ := s.TicketStore().GetByID(ctx, tk.ID, anyTicket)
	if got.Title != "renamed from a stale read" {
		t.Errorf("title = %q", got.Title)
	}
	if got.OnHoldSince == nil || got.OnHoldReason != "waiting" {
		t.Errorf("stale update cleared the hold: %v %q", got.OnHoldSince, got.OnHoldReason)
	}
}

// TestUpdateRefusesTerminalStateOverAHold pins the store half of the
// no-terminal-ticket-on-hold invariant: a stale read that missed the hold
// cannot resolve the ticket behind it.
func TestUpdateRefusesTerminalStateOverAHold(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	tk, _ := holdFixture(t, s)
	stale := *tk
	putOnHold(t, s, tk, testClock.Add(time.Hour), "waiting")

	ev, err := stale.Transition(domain.StateResolved, "", testClock.Add(2*time.Hour))
	if err != nil {
		t.Fatalf("Transition: %v", err)
	}
	auditBefore := countRows(t, s, `SELECT COUNT(*) FROM audit_events WHERE ticket_id=?`, tk.ID)
	err = s.TicketUnitOfWork().Update(ctx, application.TicketMutation{Ticket: &stale, Events: []domain.AuditEvent{*ev}})
	if !errors.Is(err, domain.ErrHoldConflict) {
		t.Fatalf("err = %v, want ErrHoldConflict", err)
	}
	got, _ := s.TicketStore().GetByID(ctx, tk.ID, anyTicket)
	if got.State != domain.StateNew || got.ResolvedAt != nil {
		t.Errorf("state = %q resolved_at = %v, want untouched", got.State, got.ResolvedAt)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM audit_events WHERE ticket_id=?`, tk.ID); n != auditBefore {
		t.Errorf("audit rows = %d, want %d", n, auditBefore)
	}
}

func TestSLAMilestonesCarryThePauseInstant(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	tk, _ := holdFixture(t, s)
	other := seedTicketForTimelineInCategory(t, s, 2, tk.CategoryID)
	at := testClock.Add(time.Hour)
	putOnHold(t, s, tk, at, "waiting")

	m, err := s.SLAStore().Milestones(ctx, tk.ID)
	if err != nil {
		t.Fatalf("Milestones: %v", err)
	}
	if m.ResolvePausedAt == nil || !m.ResolvePausedAt.Equal(at) {
		t.Errorf("single ResolvePausedAt = %v, want %v", m.ResolvePausedAt, at)
	}
	batch, err := s.SLAStore().MilestonesFor(ctx, []int64{tk.ID, other})
	if err != nil {
		t.Fatalf("MilestonesFor: %v", err)
	}
	if p := batch[tk.ID].ResolvePausedAt; p == nil || !p.Equal(at) {
		t.Errorf("batch ResolvePausedAt = %v, want %v", p, at)
	}
	if p := batch[other].ResolvePausedAt; p != nil {
		t.Errorf("ticket not on hold has ResolvePausedAt %v", p)
	}
	if single, _ := s.SLAStore().Milestones(ctx, other); single.ResolvePausedAt != nil {
		t.Errorf("ticket not on hold has single ResolvePausedAt %v", single.ResolvePausedAt)
	}
}

func seedTicketForTimelineInCategory(t *testing.T, s *Store, number int, categoryID int64) int64 {
	t.Helper()
	return seedTicket(t, s, domain.Ticket{Number: number, Title: "other ticket",
		CategoryID: categoryID, Priority: domain.PriorityMedium, State: domain.StateNew,
		CreatedAt: testClock, UpdatedAt: testClock}).ID
}

// TestUpdateHoldCommitsNotificationsWithTheHold pins the #344 rule for the hold
// path (issue #352, S7): the requester's on_hold notification commits with the
// hold, and a hold refused by the compare-and-set leaves no notification.
func TestUpdateHoldCommitsNotificationsWithTheHold(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	tk, _ := holdFixture(t, s)
	requester := seedUser(t, s, "Carol", "carol@example.com", true)
	stale := *tk
	at := testClock.Add(time.Hour)
	ev, err := tk.PutOnHold("send the serial number", at)
	if err != nil {
		t.Fatalf("PutOnHold: %v", err)
	}
	notification := domain.Notification{UserID: requester, TicketID: tk.ID, Kind: domain.NotificationOnHold, CreatedAt: at}
	if err := s.TicketUnitOfWork().UpdateHold(ctx, application.HoldWrite{
		Ticket: tk, Events: []domain.AuditEvent{*ev}, Notifications: []domain.Notification{notification},
	}); err != nil {
		t.Fatalf("UpdateHold: %v", err)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM notifications WHERE user_id=? AND ticket_id=? AND kind='on_hold'`, requester, tk.ID); n != 1 {
		t.Fatalf("on_hold notifications = %d, want 1", n)
	}

	// A second hold from a read taken before the first one loses the race.
	ev2, err := stale.PutOnHold("another reason", at.Add(time.Minute))
	if err != nil {
		t.Fatalf("PutOnHold: %v", err)
	}
	err = s.TicketUnitOfWork().UpdateHold(ctx, application.HoldWrite{
		Ticket: &stale, Events: []domain.AuditEvent{*ev2}, Notifications: []domain.Notification{notification},
	})
	if !errors.Is(err, domain.ErrHoldConflict) {
		t.Fatalf("err = %v, want ErrHoldConflict", err)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM notifications WHERE ticket_id=?`, tk.ID); n != 1 {
		t.Errorf("notifications after a refused hold = %d, want still 1", n)
	}
}
