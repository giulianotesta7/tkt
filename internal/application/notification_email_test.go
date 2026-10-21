package application_test

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// fakeOutbox is an in-memory notification_emails table. It keeps one row per
// notification with the delivery state the sender drives.
type fakeOutbox struct {
	rows     map[int64]*fakeOutboxRow
	claimErr error
}

type fakeOutboxRow struct {
	n         domain.Notification
	status    application.EmailDeliveryStatus
	attempts  int
	nextAt    time.Time
	reason    string
	lastError string
	sentAt    time.Time
}

func newFakeOutbox() *fakeOutbox { return &fakeOutbox{rows: map[int64]*fakeOutboxRow{}} }

func (f *fakeOutbox) enqueue(n domain.Notification) {
	f.rows[n.ID] = &fakeOutboxRow{n: n, status: application.EmailPending, nextAt: n.CreatedAt}
}

func (f *fakeOutbox) row(id int64) *fakeOutboxRow { return f.rows[id] }

func (f *fakeOutbox) AbandonInFlight(_ context.Context, _ time.Time) (int, error) {
	count := 0
	for _, r := range f.rows {
		if r.status == application.EmailSending {
			r.status, r.lastError = application.EmailFailed, "interrupted"
			count++
		}
	}
	return count, nil
}

func (f *fakeOutbox) SkipPending(_ context.Context, _ time.Time, reason string) (int, error) {
	count := 0
	for _, r := range f.rows {
		if r.status == application.EmailPending {
			r.status, r.reason = application.EmailSkipped, reason
			count++
		}
	}
	return count, nil
}

func (f *fakeOutbox) ClaimDue(_ context.Context, now time.Time, limit int) ([]application.PendingEmail, error) {
	if f.claimErr != nil {
		return nil, f.claimErr
	}
	ids := make([]int64, 0, len(f.rows))
	for id := range f.rows {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	var out []application.PendingEmail
	for _, id := range ids {
		r := f.rows[id]
		if r.status != application.EmailPending || r.nextAt.After(now) || len(out) == limit {
			continue
		}
		r.status = application.EmailSending
		out = append(out, application.PendingEmail{Notification: r.n, Attempts: r.attempts})
	}
	return out, nil
}

func (f *fakeOutbox) claimed(id int64) (*fakeOutboxRow, error) {
	r, ok := f.rows[id]
	if !ok || r.status != application.EmailSending {
		return nil, fmt.Errorf("fakeOutbox: notification %d is not claimed", id)
	}
	return r, nil
}

func (f *fakeOutbox) MarkSent(_ context.Context, id int64, at time.Time) error {
	r, err := f.claimed(id)
	if err != nil {
		return err
	}
	r.status, r.sentAt = application.EmailSent, at
	r.attempts++
	return nil
}

func (f *fakeOutbox) MarkSkipped(_ context.Context, id int64, _ time.Time, reason string) error {
	r, err := f.claimed(id)
	if err != nil {
		return err
	}
	r.status, r.reason = application.EmailSkipped, reason
	return nil
}

func (f *fakeOutbox) MarkFailed(_ context.Context, id int64, _ time.Time, attempts int, lastError string) error {
	r, err := f.claimed(id)
	if err != nil {
		return err
	}
	r.status, r.attempts, r.lastError = application.EmailFailed, attempts, lastError
	return nil
}

func (f *fakeOutbox) Reschedule(_ context.Context, id int64, attempts int, next time.Time, lastError string) error {
	r, err := f.claimed(id)
	if err != nil {
		return err
	}
	r.status, r.attempts, r.nextAt, r.lastError = application.EmailPending, attempts, next, lastError
	return nil
}

type fakeEmailPrefs struct{ off map[int64]bool }

func (f *fakeEmailPrefs) EmailNotificationsEnabled(_ context.Context, userID int64) (bool, error) {
	return !f.off[userID], nil
}

type fakeDeliveryConfig struct {
	cfg application.DeliveryConfig
	ok  bool
	err error
}

func (f *fakeDeliveryConfig) DeliveryConfig(context.Context) (application.DeliveryConfig, bool, error) {
	return f.cfg, f.ok, f.err
}

type senderFixture struct {
	sender  *application.NotificationEmailSender
	outbox  *fakeOutbox
	config  *fakeDeliveryConfig
	mailer  *senderMailer
	users   *fakeUserStore
	tickets *fakeTicketStore
	prefs   *fakeEmailPrefs
	clock   *fakeClock
	agent   domain.User
	req     domain.User
	ticket  domain.Ticket
	nextID  int64
}

func newSenderFixture(t *testing.T) *senderFixture {
	t.Helper()
	f := &senderFixture{
		outbox:  newFakeOutbox(),
		config:  &fakeDeliveryConfig{ok: true, cfg: application.DeliveryConfig{SMTP: application.SMTPConfig{Host: "smtp.example.com", Port: 25, Encryption: application.EmailEncryptionNone, FromAddress: "tkt@example.com"}, BaseURL: "https://tkt.example.com", Language: "en"}},
		mailer:  &senderMailer{},
		users:   newFakeUserStore(),
		tickets: newFakeTicketStore(),
		prefs:   &fakeEmailPrefs{off: map[int64]bool{}},
		clock:   fixedClock(),
	}
	f.agent = f.users.seed("Ana Agent", "ana@example.com", true)
	f.agent.Role = domain.RoleAgent
	f.users.users[f.agent.ID].Role = domain.RoleAgent
	f.req = f.users.seed("Rita Requester", "rita@example.com", true)
	f.req.Role = domain.RoleUser
	f.users.users[f.req.ID].Role = domain.RoleUser
	agentID, reqID := f.agent.ID, f.req.ID
	f.ticket = f.tickets.seed(domain.Ticket{Title: "Printer jams", UserID: &agentID, RequesterUserID: &reqID})
	f.sender = application.NewNotificationEmailSender(f.outbox, f.config, f.users, f.tickets, f.prefs, f.mailer, senderRenderer{}, f.clock)
	return f
}

// notify enqueues one notification to recipient, as appendNotificationsTx
// does in the same transaction as the change.
func (f *senderFixture) notify(recipient int64, kind domain.NotificationKind, actor *int64) int64 {
	f.nextID++
	n := domain.Notification{ID: f.nextID, UserID: recipient, TicketID: f.ticket.ID, Kind: kind, ActorUserID: actor, CreatedAt: f.clock.Now()}
	f.outbox.enqueue(n)
	return n.ID
}

func (f *senderFixture) pass(t *testing.T) {
	t.Helper()
	if err := f.sender.Pass(context.Background()); err != nil {
		t.Fatalf("Pass: %v", err)
	}
}

func TestSenderDeliversOneEmailPerNotificationInTheInstanceLanguage(t *testing.T) {
	f := newSenderFixture(t)
	f.config.cfg.Language = "es"
	agentID := f.agent.ID
	id := f.notify(f.req.ID, domain.NotificationCommentPublic, &agentID)

	f.pass(t)
	if len(f.mailer.sent) != 1 {
		t.Fatalf("sent %d emails, want 1", len(f.mailer.sent))
	}
	got := f.mailer.sent[0]
	if got.email.To != "rita@example.com" {
		t.Fatalf("To = %q", got.email.To)
	}
	if got.email.Subject != fmt.Sprintf("es:email.notification.subject[%d,Printer jams]", f.ticket.Number) {
		t.Fatalf("Subject = %q", got.email.Subject)
	}
	for _, want := range []string{
		"es:notifications.kind.comment_public[]",
		"es:email.notification.actor[Ana Agent]",
		fmt.Sprintf("es:email.notification.link[https://tkt.example.com/tickets/%d]", f.ticket.ID),
		"es:email.notification.footer[]",
	} {
		if !strings.Contains(got.email.Body, want) {
			t.Errorf("Body lacks %q:\n%s", want, got.email.Body)
		}
	}
	if r := f.outbox.row(id); r.status != application.EmailSent || r.attempts != 1 {
		t.Fatalf("row = %+v, want sent after 1 attempt", r)
	}

	// A sent row is never claimed again: the next pass sends nothing.
	f.pass(t)
	if len(f.mailer.sent) != 1 {
		t.Fatalf("a second pass re-sent: %d emails", len(f.mailer.sent))
	}
}

func TestSenderNamesAnAutomaticActor(t *testing.T) {
	f := newSenderFixture(t)
	f.notify(f.agent.ID, domain.NotificationAssigned, nil)
	f.pass(t)
	if len(f.mailer.sent) != 1 || !strings.Contains(f.mailer.sent[0].email.Body, "en:email.notification.actor[en:notifications.actor.automatic[]]") {
		t.Fatalf("automatic actor not named: %+v", f.mailer.sent)
	}
}

func TestSenderSkipsEverythingWhileEmailIsOff(t *testing.T) {
	f := newSenderFixture(t)
	f.config.ok = false
	id := f.notify(f.req.ID, domain.NotificationStateChanged, nil)
	f.pass(t)
	if len(f.mailer.sent) != 0 {
		t.Fatal("sent while email is off")
	}
	if r := f.outbox.row(id); r.status != application.EmailSkipped || r.reason != application.EmailSkipDisabled {
		t.Fatalf("row = %+v, want skipped as disabled", r)
	}
	// Turning email on later does not flood the inbox with the old row.
	f.config.ok = true
	f.pass(t)
	if len(f.mailer.sent) != 0 {
		t.Fatal("a row skipped while off was sent after enabling")
	}
}

func TestSenderRechecksTheRecipientAtSendTime(t *testing.T) {
	cases := map[string]struct {
		arrange func(f *senderFixture) int64
		reason  string
	}{
		"opted out": {func(f *senderFixture) int64 {
			f.prefs.off[f.req.ID] = true
			return f.notify(f.req.ID, domain.NotificationStateChanged, nil)
		}, application.EmailSkipOptedOut},
		"deactivated": {func(f *senderFixture) int64 {
			f.users.users[f.req.ID].Active = false
			return f.notify(f.req.ID, domain.NotificationStateChanged, nil)
		}, application.EmailSkipRecipientUnavailable},
		"deleted": {func(f *senderFixture) int64 {
			return f.notify(9999, domain.NotificationStateChanged, nil)
		}, application.EmailSkipRecipientUnavailable},
		"ticket out of reach": {func(f *senderFixture) int64 {
			// The agent was unassigned after the notification was written.
			f.tickets.tickets[f.ticket.ID].UserID = nil
			return f.notify(f.agent.ID, domain.NotificationAssigned, nil)
		}, application.EmailSkipOutOfReach},
		"internal comment, no longer staff": {func(f *senderFixture) int64 {
			// Demoted to requester while still the ticket's requester: the
			// ticket is in reach, the internal comment is not.
			f.users.users[f.agent.ID].Role = domain.RoleUser
			agentID := f.agent.ID
			f.tickets.tickets[f.ticket.ID].RequesterUserID = &agentID
			return f.notify(f.agent.ID, domain.NotificationCommentInternal, nil)
		}, application.EmailSkipOutOfReach},
	}
	for name, tc := range cases {
		f := newSenderFixture(t)
		id := tc.arrange(f)
		f.pass(t)
		if len(f.mailer.sent) != 0 {
			t.Errorf("%s: sent %d emails", name, len(f.mailer.sent))
		}
		if r := f.outbox.row(id); r.status != application.EmailSkipped || r.reason != tc.reason {
			t.Errorf("%s: row = %+v, want skipped %s", name, r, tc.reason)
		}
	}
}

func TestSenderRetriesWithBackoffThenFails(t *testing.T) {
	f := newSenderFixture(t)
	f.mailer.sendErr = errors.New("421 try again later")
	id := f.notify(f.req.ID, domain.NotificationStateChanged, nil)

	want := []time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute, time.Hour, 6 * time.Hour}
	for i, wait := range want {
		f.pass(t)
		r := f.outbox.row(id)
		if r.status != application.EmailPending || r.attempts != i+1 || !r.nextAt.Equal(f.clock.Now().Add(wait)) {
			t.Fatalf("after attempt %d: %+v, want pending, retry in %v", i+1, r, wait)
		}
		if !strings.Contains(r.lastError, "421") {
			t.Fatalf("lastError = %q", r.lastError)
		}
		// Not yet due: a pass before the backoff elapses sends nothing.
		before := len(f.mailer.sent)
		f.clock.now = f.clock.now.Add(wait - time.Second)
		f.pass(t)
		if len(f.mailer.sent) != before {
			t.Fatalf("attempt %d retried before its backoff", i+1)
		}
		f.clock.now = f.clock.now.Add(time.Second)
	}
	f.pass(t)
	if r := f.outbox.row(id); r.status != application.EmailFailed || r.attempts != 6 {
		t.Fatalf("final row = %+v, want failed after 6 attempts", r)
	}
	if len(f.mailer.sent) != 6 {
		t.Fatalf("attempts = %d, want 6", len(f.mailer.sent))
	}
}

func TestSenderNeverRetriesAPermanentOrUncertainFailure(t *testing.T) {
	for name, err := range map[string]error{
		"permanent": fmt.Errorf("550 mailbox unavailable: %w", application.ErrDeliveryPermanent),
		"uncertain": fmt.Errorf("connection reset after data: %w", application.ErrDeliveryOutcomeUnknown),
	} {
		f := newSenderFixture(t)
		f.mailer.sendErr = err
		id := f.notify(f.req.ID, domain.NotificationStateChanged, nil)
		f.pass(t)
		if r := f.outbox.row(id); r.status != application.EmailFailed || r.attempts != 1 {
			t.Errorf("%s: row = %+v, want failed after 1 attempt", name, r)
		}
	}
}

func TestRecoverFailsRowsInterruptedMidSend(t *testing.T) {
	f := newSenderFixture(t)
	id := f.notify(f.req.ID, domain.NotificationStateChanged, nil)
	// A crash after the claim: the row is left 'sending'.
	if _, err := f.outbox.ClaimDue(context.Background(), f.clock.Now(), 10); err != nil {
		t.Fatal(err)
	}
	if err := f.sender.Recover(context.Background()); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if r := f.outbox.row(id); r.status != application.EmailFailed {
		t.Fatalf("row = %+v, want failed (at most once)", r)
	}
	f.pass(t)
	if len(f.mailer.sent) != 0 {
		t.Fatal("an interrupted row was sent again")
	}
}

func TestSenderLeavesRowsPendingWhenTheConfigurationCannotBeRead(t *testing.T) {
	f := newSenderFixture(t)
	f.config.err = errors.New("password unreadable")
	id := f.notify(f.req.ID, domain.NotificationStateChanged, nil)
	if err := f.sender.Pass(context.Background()); err == nil {
		t.Fatal("Pass hid a configuration error")
	}
	if r := f.outbox.row(id); r.status != application.EmailPending {
		t.Fatalf("row = %+v, want still pending", r)
	}
}

// senderMailer records every send and fails on demand.
type senderMailer struct {
	sent    []senderSent
	sendErr error
}

type senderSent struct {
	config application.SMTPConfig
	email  application.OutgoingEmail
}

func (m *senderMailer) Send(_ context.Context, cfg application.SMTPConfig, email application.OutgoingEmail) error {
	m.sent = append(m.sent, senderSent{config: cfg, email: email})
	return m.sendErr
}

// senderRenderer renders a message as "<lang>:<key>[args]" so a test can
// assert which sentence was chosen and in which language.
type senderRenderer struct{}

func (senderRenderer) Render(language string, m application.Msg) string {
	parts := make([]string, 0, len(m.Args))
	for _, a := range m.Args {
		switch {
		case a.Msg != nil:
			parts = append(parts, senderRenderer{}.Render(language, *a.Msg))
		case a.Key != "":
			parts = append(parts, a.Key)
		default:
			parts = append(parts, a.Value)
		}
	}
	return language + ":" + m.Key + "[" + strings.Join(parts, ",") + "]"
}

// TestNotificationMessageNamesOnHold pins the sentence of the on_hold kind
// (issue #352, S7), shared by the panel and the email: it names its own
// catalogue entry, never the "something happened" fallback.
func TestNotificationMessageNamesOnHold(t *testing.T) {
	got := application.NotificationMessage(domain.Notification{Kind: domain.NotificationOnHold})
	if got.Key != "notifications.kind.on_hold" || len(got.Args) != 0 {
		t.Errorf("NotificationMessage(on_hold) = %+v, want notifications.kind.on_hold", got)
	}
}
