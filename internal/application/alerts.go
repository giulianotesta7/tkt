package application

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// Operational alerts (issue #408). An agent used to learn a ticket was in
// trouble only by opening it: the SLA state was already derived and displayed,
// but nothing pushed it, and nothing noticed a ticket that simply stopped
// moving. This file is the use case; the rules are pure arithmetic in
// domain/alerts.go, and the rows are ordinary notifications, so the bell and
// the opt-in email sender both carry them without a second delivery path.
//
// The pass deliberately mirrors HoldExpiryService: same ticker, same keyset
// paging, same one-snapshot-per-batch discipline.

// AlertCandidatesPageSize bounds one read of the pass. The pass pages by id, so
// memory stays bounded however many tickets are open.
const AlertCandidatesPageSize = 100

// AlertService runs the operational alert pass: ONE pass walks the open
// assigned tickets and tells each assignee about the conditions their ticket
// reached. It reads the working calendar on every pass, so a saved change
// applies without a restart — the same rule the hold expiry follows.
type AlertService struct {
	store    AlertStore
	tx       AlertUnitOfWork
	users    UserStore
	settings SettingsStore
	clock    domain.Clock
}

func NewAlertService(store AlertStore, tx AlertUnitOfWork, users UserStore, settings SettingsStore, clock domain.Clock) *AlertService {
	return &AlertService{store: store, tx: tx, users: users, settings: settings, clock: clock}
}

// Run runs the pass every interval until ctx ends, logging a repeated error
// once (ShouldLogPassError) instead of on every tick.
func (s *AlertService) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var lastErr string
	for {
		if err := s.Pass(ctx); ctx.Err() == nil && ShouldLogPassError(&lastErr, err) {
			log.Printf("alerts: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Pass applies the alert rules to the open assigned tickets, one page at a
// time. One clock snapshot and one calendar serve the whole pass, so two
// tickets are judged against the same instant.
//
// Any failure aborts the pass: alerting on partial information is worse than
// alerting a few minutes later, and the next tick starts over.
func (s *AlertService) Pass(ctx context.Context) error {
	calendar, err := s.settings.GetSLACalendar(ctx)
	if err != nil {
		return fmt.Errorf("read the working calendar: %w", err)
	}
	now := s.clock.Now()
	var afterID int64
	for {
		page, err := s.store.AlertCandidates(ctx, afterID, AlertCandidatesPageSize)
		if err != nil {
			return fmt.Errorf("read the alert candidates: %w", err)
		}
		for i := range page {
			if err := s.apply(ctx, &page[i], calendar, now); err != nil {
				return err
			}
		}
		if len(page) < AlertCandidatesPageSize {
			return nil
		}
		afterID = page[len(page)-1].Ticket.ID
	}
}

// apply alerts about ONE candidate. The assignee is resolved first: they are
// the only recipient, so a ticket whose assignee vanished or was deactivated
// produces nothing at all rather than a row nobody can read.
func (s *AlertService) apply(ctx context.Context, c *AlertCandidate, calendar domain.SLACalendar, now time.Time) error {
	assignee, err := recipientFacts(ctx, s.users, c.Ticket.UserID)
	if err != nil {
		return err
	}
	if assignee == nil {
		return nil
	}
	// The SLA conditions are read from the projection the ticket already
	// displays, so an alert can never disagree with the screen.
	for _, kind := range domain.SLAAlertKinds(domain.ProjectSLA(c.SLA, c.Milestones, now)) {
		// An SLA condition is reported once per ticket: the frozen instants
		// do not move back, so there is no later period to re-report.
		if err := s.alert(ctx, &c.Ticket, kind, nil, assignee, now); err != nil {
			return err
		}
	}
	return s.alertNoMovement(ctx, c, assignee, calendar, now)
}

// alertNoMovement reports a ticket that stopped moving. A ticket waiting on its
// requester accumulates no inactivity: the silence is the requester's, and the
// hold already has its own reminder and expiry (issue #353).
//
// The alert is scoped to the CURRENT period of silence, so fresh activity
// restarts the window and allows a later alert, exactly as the maintainer
// chose ("una nueva actividad reinicia el plazo").
func (s *AlertService) alertNoMovement(ctx context.Context, c *AlertCandidate, assignee *NotificationRecipient, calendar domain.SLACalendar, now time.Time) error {
	if c.Ticket.OnHoldSince != nil || c.LastActivityAt.IsZero() {
		return nil
	}
	if now.Before(domain.NoMovementAt(c.LastActivityAt, calendar)) {
		return nil
	}
	since := c.LastActivityAt
	return s.alert(ctx, &c.Ticket, domain.NotificationNoMovement, &since, assignee, now)
}

// alert records ONE condition, once. The cheap read skips the common case (the
// condition persists, so most passes find it already reported); the write
// re-checks the same predicate inside its transaction, which is what actually
// makes it safe. An alert nobody can receive writes nothing and is NOT recorded
// as sent, so a recipient who regains access is still told.
func (s *AlertService) alert(ctx context.Context, t *domain.Ticket, kind domain.NotificationKind, since *time.Time, assignee *NotificationRecipient, now time.Time) error {
	sent, err := s.store.AlertSent(ctx, t.ID, kind, since)
	if err != nil {
		return fmt.Errorf("check the %s alert of ticket %d: %w", kind, t.ID, err)
	}
	if sent {
		return nil
	}
	rows := alertNotifications(t, kind, now, assignee)
	if len(rows) == 0 {
		return nil
	}
	if err := s.tx.AppendAlert(ctx, t.ID, kind, since, rows); err != nil {
		return fmt.Errorf("alert ticket %d about %s: %w", t.ID, kind, err)
	}
	return nil
}
