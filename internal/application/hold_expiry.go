package application

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// Automatic hold expiry (issue #353). A ticket put on hold waits for the
// requester (#352); when the reply never comes the instance warns them one
// working day before the deadline and then closes the ticket itself, telling
// the requester and the assignee (specs S1-S5). This file is the use case: the
// rules themselves are pure working-day arithmetic in domain/hold.go, and the
// writes are the existing guarded hold unit of work, so nothing here invents a
// second way to end a hold.
const (
	// DefaultHoldAutoCloseWorkingDays is the instance default: 0 — the
	// feature is OFF until an administrator chooses a window. Closing tickets
	// without a human is an explicit decision, never a defaulted-on state.
	DefaultHoldAutoCloseWorkingDays = 0
	// MaxHoldAutoCloseWorkingDays bounds the window an administrator may
	// store. A longer window is not a configuration this feature can express:
	// the reminder and the deadline are one working day apart, and a window of
	// months would keep tickets paused long after the requester stopped caring.
	MaxHoldAutoCloseWorkingDays = 60
)

// HoldExpiryService runs the automatic hold expiry (issue #353): ONE pass walks
// the held tickets, warns the ones whose reminder is due, and closes the ones
// whose window ran out. It reads the setting on every pass, so a saved change
// applies without a restart — the same rule the email sender follows.
type HoldExpiryService struct {
	store    HoldExpiryStore
	tx       TicketUnitOfWork
	users    UserStore
	sla      *SLAService
	settings SettingsStore
	clock    domain.Clock
}

// NewHoldExpiryService wires the pass. The SLA collaborator is the same one the
// ticket use cases hold, so closing an expired hold re-applies the paused
// working time to the frozen resolution instants exactly as a manual resume
// does.
func NewHoldExpiryService(store HoldExpiryStore, tx TicketUnitOfWork, users UserStore, sla *SLAService, settings SettingsStore, clock domain.Clock) *HoldExpiryService {
	return &HoldExpiryService{store: store, tx: tx, users: users, sla: sla, settings: settings, clock: clock}
}

// Run runs the pass every interval until ctx ends, logging a repeated error
// once (ShouldLogPassError) instead of on every tick — the same background
// shape as NotificationEmailSender.Run.
func (s *HoldExpiryService) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var lastErr string
	for {
		if err := s.Pass(ctx); ctx.Err() == nil && ShouldLogPassError(&lastErr, err) {
			log.Printf("hold expiry: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Pass applies the expiry rules to the held tickets of one batch.
//
// The setting is read FIRST: a zero window means the feature is off, so the
// pass does not even read the held tickets. Then one clock snapshot and one
// calendar serve the whole batch, so two tickets of the same pass are judged
// against the same instant.
//
// Per ticket: at or after the deadline the ticket is closed automatically; else,
// with a window of at least two working days, at or after the reminder instant
// the requester is warned once per hold. A *domain.HoldConflictError means the
// requester replied between the read and the write — the race S5 decides in
// their favour — so that ticket is skipped silently and the pass continues. Any
// other failure aborts the pass: closing tickets on partial information is worse
// than closing them a few minutes later.
func (s *HoldExpiryService) Pass(ctx context.Context) error {
	days, err := s.settings.GetHoldAutoCloseDays(ctx)
	if err != nil {
		return fmt.Errorf("read the hold auto-close window: %w", err)
	}
	if days <= 0 {
		return nil
	}
	calendar, err := s.settings.GetSLACalendar(ctx)
	if err != nil {
		return fmt.Errorf("read the working calendar: %w", err)
	}
	now := s.clock.Now()
	// Page through every held ticket by id: a hold that is not due stays in the
	// set, so a single bounded read would let a full first page hide the due
	// ones behind it forever.
	var afterID int64
	for {
		held, err := s.store.HeldTickets(ctx, afterID, HeldTicketsPageSize)
		if err != nil {
			return fmt.Errorf("read the held tickets: %w", err)
		}
		for i := range held {
			if err := s.apply(ctx, &held[i], days, calendar, now); err != nil {
				return err
			}
		}
		if len(held) < HeldTicketsPageSize {
			return nil
		}
		afterID = held[len(held)-1].ID
	}
}

// HeldTicketsPageSize bounds one read of the pass (issue #353): the pass pages
// by id, so memory stays bounded however many tickets are on hold.
const HeldTicketsPageSize = 100

// apply applies the rules to ONE held ticket: the deadline wins over the
// reminder, because a pass that runs late must still close on time rather than
// warn about a deadline it already missed.
func (s *HoldExpiryService) apply(ctx context.Context, t *domain.Ticket, days int, calendar domain.SLACalendar, now time.Time) error {
	if t.OnHoldSince == nil {
		return nil
	}
	since := *t.OnHoldSince
	if !now.Before(domain.HoldAutoCloseAt(since, days, calendar)) {
		return s.close(ctx, t, since, now)
	}
	if days < 2 || now.Before(domain.HoldReminderAt(since, days, calendar)) {
		return nil
	}
	return s.remind(ctx, t, since, now)
}

// close ends the expired hold and closes the ticket in ONE hold write (S1, S5):
// the resume re-applies the pause to the frozen resolution SLA, the resolve and
// the closure follow it, and the timeline records all three under the automatic
// actor — nobody performed this, so no user id is stored and the closure is
// attributed hold_expired.
//
// The two transitions deliberately run through the DOMAIN state machine rather
// than TicketService.Transition: that service's manual-closure gate (a manual
// resolved -> closed on a requester-owned ticket is the requester's
// confirmation alone) governs a HUMAN agent's action, and this close is not one.
// The batch is built explicitly, so the close produces hold_closed and nothing
// else — no state_changed, no asking the requester to confirm a resolution they
// never answered.
func (s *HoldExpiryService) close(ctx context.Context, t *domain.Ticket, since, now time.Time) error {
	from := t.State
	pausedAt, resumeEvent, err := t.ResumeFromHold(now)
	if err != nil {
		return err
	}
	shifted, err := s.shiftResolve(ctx, t.ID, pausedAt, now)
	if err != nil {
		return err
	}
	events := make([]domain.AuditEvent, 0, 3)
	resumeEvent.Actor = domain.AutomaticActorSystem
	events = append(events, *resumeEvent)
	resolved, err := t.Transition(domain.StateResolved, "", now)
	if err != nil {
		return err
	}
	resolved.Actor = domain.AutomaticActorSystem
	events = append(events, *resolved)
	closed, err := t.Transition(domain.StateClosed, "", now)
	if err != nil {
		return err
	}
	closed.Actor = domain.AutomaticActorSystem
	via := domain.ClosureViaHoldExpired
	closed.ClosureVia = &via
	events = append(events, *closed)
	requester, assignee, err := ticketRecipients(ctx, s.users, t)
	if err != nil {
		return err
	}
	write := HoldWrite{
		Ticket:              t,
		ExpectedOnHoldSince: &since,
		ShiftedSLA:          shifted,
		Events:              events,
		Notifications:       holdClosedNotifications(t, now, requester, assignee),
	}
	if err := s.tx.UpdateHold(ctx, write); err != nil {
		if isHoldConflict(err) {
			// The requester replied between the read and this write (S5): their
			// reply ended the hold, so there is nothing left to close.
			return nil
		}
		return fmt.Errorf("close ticket %d (%s) after its hold expired: %w", t.ID, from, err)
	}
	return nil
}

// remind warns the requester that the held ticket is about to close (S3),
// once per hold and never when nobody can be reached: with no reachable
// requester the batch is empty and NOTHING is written, so a hold the system
// cannot tell anyone about is not marked as reminded.
func (s *HoldExpiryService) remind(ctx context.Context, t *domain.Ticket, since, now time.Time) error {
	sent, err := s.store.ReminderSent(ctx, t.ID, since)
	if err != nil {
		return fmt.Errorf("check the reminder of ticket %d: %w", t.ID, err)
	}
	if sent {
		return nil
	}
	requester, err := recipientFacts(ctx, s.users, t.RequesterUserID)
	if err != nil {
		return err
	}
	rows := holdReminderNotifications(t, now, requester)
	if len(rows) == 0 {
		return nil
	}
	if err := s.tx.RemindHold(ctx, t.ID, since, rows); err != nil {
		if isHoldConflict(err) {
			return nil
		}
		return fmt.Errorf("remind the requester of ticket %d: %w", t.ID, err)
	}
	return nil
}

// shiftResolve re-applies the working time the hold consumed to the frozen
// resolution instants (S2), reusing the SLA service so an automatic close and a
// manual resume can never shift a commitment differently. Without an SLA
// collaborator (the legacy wiring) there is no commitment to shift.
func (s *HoldExpiryService) shiftResolve(ctx context.Context, ticketID int64, pausedAt, resumedAt time.Time) (*domain.TicketSLA, error) {
	if s.sla == nil {
		return nil, nil
	}
	return s.sla.ShiftForHold(ctx, ticketID, pausedAt, resumedAt)
}

// isHoldConflict reports whether err is the compare-and-set refusal of a hold
// write: the hold the pass read is no longer the stored one.
func isHoldConflict(err error) bool {
	var conflict *domain.HoldConflictError
	return errors.As(err, &conflict)
}
