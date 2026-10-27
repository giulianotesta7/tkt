package application

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// Email delivery of notifications (issue #362). Every notification row is
// written with a pending delivery row in the SAME transaction as the change
// (S6); this sender delivers them in the background, outside that
// transaction, one claimed row at a time.

// EmailDeliveryStatus is the state of one notification's email.
type EmailDeliveryStatus string

const (
	// EmailPending waits for its first attempt or its next retry.
	EmailPending EmailDeliveryStatus = "pending"
	// EmailSending is claimed by a sender. A row found in this state at
	// startup was interrupted mid-send and becomes failed, never re-sent:
	// at most once (S6 "never sends twice").
	EmailSending EmailDeliveryStatus = "sending"
	// EmailSent was accepted by the SMTP server.
	EmailSent EmailDeliveryStatus = "sent"
	// EmailSkipped was deliberately not sent (see the EmailSkip reasons).
	EmailSkipped EmailDeliveryStatus = "skipped"
	// EmailFailed exhausted its retries or failed permanently.
	EmailFailed EmailDeliveryStatus = "failed"
)

// Skip reasons, stored with a skipped row so an operator can tell why.
const (
	EmailSkipDisabled             = "email_disabled"
	EmailSkipOptedOut             = "recipient_opted_out"
	EmailSkipRecipientUnavailable = "recipient_unavailable"
	EmailSkipOutOfReach           = "ticket_out_of_reach"
)

// EmailRetryBackoff is the wait before each retry after a transient SMTP
// failure: five retries, then the row fails (S14).
var EmailRetryBackoff = []time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute, time.Hour, 6 * time.Hour}

var (
	// ErrDeliveryPermanent marks a failure no retry can fix (a 5xx reply).
	ErrDeliveryPermanent = errors.New("email delivery failed permanently")
	// ErrDeliveryOutcomeUnknown marks a failure after the message was handed
	// over, when the server may have accepted it. Retrying could send it
	// twice, so the row fails instead.
	ErrDeliveryOutcomeUnknown = errors.New("email delivery outcome unknown")
)

// PendingEmail is one claimed delivery: the notification and the attempts
// already made.
type PendingEmail struct {
	Notification domain.Notification
	Attempts     int
}

// EmailOutbox persists the delivery state of notification emails. Every
// Mark*/Reschedule call applies only to a row this sender claimed.
type EmailOutbox interface {
	// AbandonInFlight fails every row left 'sending' by a stopped process.
	AbandonInFlight(ctx context.Context, at time.Time) (int, error)
	// SkipPending skips every pending row (email is off).
	SkipPending(ctx context.Context, at time.Time, reason string) (int, error)
	// ClaimDue atomically moves up to limit due pending rows to 'sending'
	// and returns them, oldest first. A row is claimed by one caller only.
	ClaimDue(ctx context.Context, now time.Time, limit int) ([]PendingEmail, error)
	MarkSent(ctx context.Context, notificationID int64, at time.Time) error
	MarkSkipped(ctx context.Context, notificationID int64, at time.Time, reason string) error
	MarkFailed(ctx context.Context, notificationID int64, at time.Time, attempts int, lastError string) error
	// Reschedule returns a claimed row to pending for a retry at next.
	Reschedule(ctx context.Context, notificationID int64, attempts int, next time.Time, lastError string) error
}

// EmailPreferences answers each user's email opt-out (S5); a user with no
// stored preference receives email.
type EmailPreferences interface {
	EmailNotificationsEnabled(ctx context.Context, userID int64) (bool, error)
}

// DeliveryConfigSource answers the configuration for one pass;
// EmailSettingsService implements it.
type DeliveryConfigSource interface {
	DeliveryConfig(ctx context.Context) (DeliveryConfig, bool, error)
}

// EmailUserReader and EmailTicketReader are the read halves of the user and
// ticket stores the sender needs.
type EmailUserReader interface {
	GetByID(ctx context.Context, id int64) (*domain.User, error)
}

type EmailTicketReader interface {
	GetByID(ctx context.Context, id int64, q TicketQuery) (*domain.Ticket, error)
}

// emailClaimBatch bounds one pass, so a backlog drains over several passes
// instead of holding one long loop.
const emailClaimBatch = 20

// NotificationEmailSender delivers pending notification emails.
type NotificationEmailSender struct {
	outbox              EmailOutbox
	config              DeliveryConfigSource
	users               EmailUserReader
	tickets             EmailTicketReader
	prefs               EmailPreferences
	mailer              Mailer
	renderer            MessageRenderer
	clock               domain.Clock
	languagePreferences LanguagePreferencesStore
}

// NewNotificationEmailSender wires the sender.
func NewNotificationEmailSender(outbox EmailOutbox, config DeliveryConfigSource, users EmailUserReader, tickets EmailTicketReader, prefs EmailPreferences, mailer Mailer, renderer MessageRenderer, clock domain.Clock) *NotificationEmailSender {
	return &NotificationEmailSender{outbox: outbox, config: config, users: users, tickets: tickets, prefs: prefs, mailer: mailer, renderer: renderer, clock: clock}
}

// WithLanguagePreferences reads the recipient's choice at delivery.
func (s *NotificationEmailSender) WithLanguagePreferences(p LanguagePreferencesStore) *NotificationEmailSender {
	s.languagePreferences = p
	return s
}

// Recover runs once at startup, before the first pass: rows a previous
// process left 'sending' may or may not have been delivered, so they fail
// rather than risk a second copy.
func (s *NotificationEmailSender) Recover(ctx context.Context) error {
	n, err := s.outbox.AbandonInFlight(ctx, s.clock.Now())
	if err != nil {
		return err
	}
	if n > 0 {
		log.Printf("email: %d notification email(s) interrupted by a restart were marked failed", n)
	}
	return nil
}

// Run recovers, then delivers every interval until ctx ends.
func (s *NotificationEmailSender) Run(ctx context.Context, interval time.Duration) {
	if err := s.Recover(ctx); err != nil {
		log.Printf("email: recover: %v", err)
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var lastErr string
	for {
		if err := s.Pass(ctx); ctx.Err() == nil && ShouldLogPassError(&lastErr, err) {
			log.Printf("email: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// ShouldLogPassError reports whether a pass error is worth a log line: the
// first occurrence of an error is, a repeat of the previous pass's error is
// not (a restored database without its key would otherwise log every
// interval), and a clean pass resets it so a recurrence is reported again.
func ShouldLogPassError(last *string, err error) bool {
	if err == nil {
		*last = ""
		return false
	}
	if err.Error() == *last {
		return false
	}
	*last = err.Error()
	return true
}

// Pass delivers the due rows of one batch. The configuration is read on
// every pass, so a saved change applies without a restart (S4). While email
// is off, pending rows are skipped, so turning it on later does not flood
// inboxes with old notifications (S14).
func (s *NotificationEmailSender) Pass(ctx context.Context) error {
	cfg, ok, err := s.config.DeliveryConfig(ctx)
	if err != nil {
		return fmt.Errorf("read email configuration: %w", err)
	}
	now := s.clock.Now()
	if !ok {
		_, err := s.outbox.SkipPending(ctx, now, EmailSkipDisabled)
		return err
	}
	claimed, err := s.outbox.ClaimDue(ctx, now, emailClaimBatch)
	if err != nil {
		return fmt.Errorf("claim notification emails: %w", err)
	}
	var errs []error
	for _, p := range claimed {
		if err := s.deliver(ctx, cfg, p); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// deliver settles one claimed row: skipped, sent, rescheduled, or failed.
func (s *NotificationEmailSender) deliver(ctx context.Context, cfg DeliveryConfig, p PendingEmail) error {
	n := p.Notification
	email, skip, err := s.compose(ctx, cfg, n)
	if err != nil {
		// A store failure while preparing counts as a transient attempt.
		return s.failAttempt(ctx, p, err)
	}
	if skip != "" {
		return s.outbox.MarkSkipped(ctx, n.ID, s.clock.Now(), skip)
	}
	if err := s.mailer.Send(ctx, cfg.SMTP, email); err != nil {
		return s.failAttempt(ctx, p, err)
	}
	return s.outbox.MarkSent(ctx, n.ID, s.clock.Now())
}

func (s *NotificationEmailSender) failAttempt(ctx context.Context, p PendingEmail, cause error) error {
	attempts := p.Attempts + 1
	detail := cause.Error()
	now := s.clock.Now()
	final := errors.Is(cause, ErrDeliveryPermanent) || errors.Is(cause, ErrDeliveryOutcomeUnknown) ||
		attempts > len(EmailRetryBackoff)
	if final {
		return s.outbox.MarkFailed(ctx, p.Notification.ID, now, attempts, detail)
	}
	return s.outbox.Reschedule(ctx, p.Notification.ID, attempts, now.Add(EmailRetryBackoff[attempts-1]), detail)
}

// compose re-checks, at send time, everything the panel re-checks at read
// time (S13): the recipient still exists, is active and wants email, can
// still open the ticket, and — for an internal comment — may still see
// internal comments. It then renders the plain-text email (S11) in the
// recipient language with a link to the ticket (S7, S10). The comment body is
// never included: the email says what happened, the ticket shows it.
func (s *NotificationEmailSender) compose(ctx context.Context, cfg DeliveryConfig, n domain.Notification) (OutgoingEmail, string, error) {
	recipient, err := s.users.GetByID(ctx, n.UserID)
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return OutgoingEmail{}, EmailSkipRecipientUnavailable, nil
	case err != nil:
		return OutgoingEmail{}, "", err
	case !recipient.Active || !recipient.Role.Valid() || strings.TrimSpace(recipient.Email) == "":
		return OutgoingEmail{}, EmailSkipRecipientUnavailable, nil
	}
	enabled, err := s.prefs.EmailNotificationsEnabled(ctx, recipient.ID)
	if err != nil {
		return OutgoingEmail{}, "", err
	}
	if !enabled {
		return OutgoingEmail{}, EmailSkipOptedOut, nil
	}
	ticket, err := s.tickets.GetByID(ctx, n.TicketID, NotificationTicketQuery(*recipient))
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return OutgoingEmail{}, EmailSkipOutOfReach, nil
	case err != nil:
		return OutgoingEmail{}, "", err
	}
	if requiresInternalReader(n.Kind) && !NewPolicy().Capabilities(recipient.Role).Require(CapCommentInternal) {
		return OutgoingEmail{}, EmailSkipOutOfReach, nil
	}
	actor, err := s.actorMessage(ctx, n.ActorUserID)
	if err != nil {
		return OutgoingEmail{}, "", err
	}

	language, err := recipientEmailLanguage(ctx, s.languagePreferences, recipient.ID, cfg.Language)
	if err != nil {
		return OutgoingEmail{}, "", err
	}
	render := func(m Msg) string { return s.renderer.Render(language, m) }
	link := cfg.BaseURL + "/tickets/" + strconv.FormatInt(ticket.ID, 10)
	body := strings.Join([]string{
		render(NotificationMessage(n)),
		render(MsgRef("email.notification.actor", actor)),
		render(MsgRef("email.notification.ticket", Text(strconv.Itoa(ticket.Number)), Text(ticket.Title))),
		render(MsgRef("email.notification.link", Text(link))),
		"--\n" + render(MsgRef("email.notification.footer")),
	}, "\n\n")
	return OutgoingEmail{
		To:      recipient.Email,
		Subject: render(MsgRef("email.notification.subject", Text(strconv.Itoa(ticket.Number)), Text(ticket.Title))),
		Body:    body + "\n",
	}, "", nil
}

// actorMessage names who caused the notification, as the panel does.
func (s *NotificationEmailSender) actorMessage(ctx context.Context, actorID *int64) (Arg, error) {
	if actorID == nil {
		return Nested(MsgRef("notifications.actor.automatic")), nil
	}
	u, err := s.users.GetByID(ctx, *actorID)
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return Nested(MsgRef("notifications.actor.unknown")), nil
	case err != nil:
		return Arg{}, err
	}
	return Text(u.Name), nil
}

// NotificationMessage is the one sentence a notification reads as, in the
// panel and in the email alike (issue #362 reuses the #344 copy).
func NotificationMessage(n domain.Notification) Msg {
	switch n.Kind {
	case domain.NotificationAssigned:
		return MsgRef("notifications.kind.assigned")
	case domain.NotificationStateChanged:
		return MsgRef("notifications.kind.state_changed",
			notificationFact("state", n.FromValue), notificationFact("state", n.ToValue))
	case domain.NotificationResolvedAwaitingConfirmation:
		return MsgRef("notifications.kind.resolved_awaiting_confirmation")
	case domain.NotificationResolutionConfirmed:
		return MsgRef("notifications.kind.resolution_confirmed")
	case domain.NotificationResolutionRejected:
		return MsgRef("notifications.kind.resolution_rejected")
	case domain.NotificationPriorityChanged:
		return MsgRef("notifications.kind.priority_changed",
			notificationFact("priority", n.FromValue), notificationFact("priority", n.ToValue))
	case domain.NotificationCommentPublic:
		return MsgRef("notifications.kind.comment_public")
	case domain.NotificationCommentInternal:
		return MsgRef("notifications.kind.comment_internal")
	case domain.NotificationFormWaiting:
		return MsgRef("notifications.kind.form_waiting")
	case domain.NotificationOnHold:
		return MsgRef("notifications.kind.on_hold")
	case domain.NotificationHoldReminder:
		// Issue #353 (S3): one sentence, the panel's and the email's alike.
		return MsgRef("notifications.kind.hold_reminder")
	case domain.NotificationHoldClosed:
		return MsgRef("notifications.kind.hold_closed")
	case domain.NotificationSLAFirstResponseAtRisk:
		// Issue #408: each alert condition has its own sentence, so the reader
		// knows which commitment is in trouble without opening the ticket.
		return MsgRef("notifications.kind.sla_first_response_at_risk")
	case domain.NotificationSLAFirstResponseBreached:
		return MsgRef("notifications.kind.sla_first_response_breached")
	case domain.NotificationSLAResolutionAtRisk:
		return MsgRef("notifications.kind.sla_resolution_at_risk")
	case domain.NotificationSLAResolutionBreached:
		return MsgRef("notifications.kind.sla_resolution_breached")
	case domain.NotificationNoMovement:
		return MsgRef("notifications.kind.no_movement")
	case domain.NotificationAssigneeChanged:
		// Issue #418: a follower learns who the ticket went to. The new
		// assignee's display name is a snapshot in to_value, interpolated as a
		// literal (it is data, not copy); a NULL or empty value means nobody
		// holds the ticket now, so the sentence says that instead of naming an
		// empty slot.
		if n.ToValue == nil || *n.ToValue == "" {
			return MsgRef("notifications.kind.unassigned")
		}
		return MsgRef("notifications.kind.assignee_changed", Text(*n.ToValue))
	case domain.NotificationMentioned:
		// Issue #418: the person named in a comment. The comment is
		// opened from the ticket, so the sentence carries no body and no name.
		return MsgRef("notifications.kind.mentioned")
	default:
		// A kind no catalogue names cannot be stored (the domain refuses it),
		// and the sentence still says something happened instead of printing
		// an identifier the reader cannot use.
		return MsgRef("notifications.kind.unknown")
	}
}

// notificationFact names one stored change value through its label namespace
// (label.state.* / label.priority.*), so the sentence speaks the instance
// language. A kind stored without its fact says "unknown" rather than leaving
// the sentence with an empty slot.
func notificationFact(kind string, value *string) Arg {
	if value == nil || *value == "" {
		return Copy("notifications.value.unknown")
	}
	return Copy("label." + kind + "." + *value)
}
