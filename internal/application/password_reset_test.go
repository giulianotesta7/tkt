package application_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// Self-service password recovery — the use cases (issue #414, S3/S5/S7/S8/S9).
// Requesting a link answers the SAME neutral result for a known and an unknown
// address, so the form cannot be used to enumerate accounts; a stored link is
// only ever its hash, and the mailed link is built from the instance address
// rather than from the request. Written before the use case exists —
// compile-error RED.

// resetPassword is a new password above the default minimum (S4), so a refusal
// in these tests can only come from the rule under test.
const resetPassword = "recovered-pass-12"

// resetDispatcher records the sends a request hands over. run splices
// "delivered" into the result, so a test can prove the send left the caller's
// goroutine and delivered the message. The zero value runs the send inline,
// like a synchronous runner.
type resetDispatcher struct {
	sends []func()
	run   bool
}

func (d *resetDispatcher) dispatch(send func()) {
	d.sends = append(d.sends, send)
	if d.run {
		send()
	}
}

// resetFixture wires the recovery use cases over in-memory ports.
type resetFixture struct {
	svc        *application.PasswordResetService
	users      *fakeUserStore
	sessions   *fakeSessionStore
	tokens     *fakePasswordResetStore
	policy     *fakePasswordSettingsStore
	config     *fakeDeliveryConfig
	mailer     *senderMailer
	dispatcher *resetDispatcher
	clock      *fakeClock
	ana        domain.User
	bea        domain.User
}

func newResetFixture(t *testing.T) *resetFixture {
	t.Helper()
	clock := fixedClock()
	users := newFakeUserStore()
	sessions := newFakeSessionStore(clock)
	f := &resetFixture{
		users:      users,
		sessions:   sessions,
		tokens:     newFakePasswordResetStore(users, sessions),
		policy:     newFakePasswordSettingsStore(),
		mailer:     &senderMailer{},
		dispatcher: &resetDispatcher{run: true},
		clock:      clock,
		config: &fakeDeliveryConfig{ok: true, cfg: application.DeliveryConfig{
			SMTP:     application.SMTPConfig{Host: "smtp.example.com", Port: 25, Encryption: application.EmailEncryptionNone, FromAddress: "tkt@example.com"},
			BaseURL:  "https://tkt.example.com",
			Language: "en",
		}},
	}
	f.svc = application.NewPasswordResetService(users, f.tokens, f.policy, f.config, f.mailer, senderRenderer{}, f.dispatcher.dispatch, clock)
	f.ana = seedPasswordUser(t, users, "Ana", "ana@example.com", true, ownCurrentPassword)
	f.bea = seedPasswordUser(t, users, "Bea", "bea@example.com", true, ownCurrentPassword)
	for _, seed := range []struct {
		id     string
		userID int64
	}{
		{"sess-laptop", f.ana.ID},
		{"sess-phone", f.ana.ID},
		{"sess-bea", f.bea.ID},
	} {
		session := &domain.Session{ID: seed.id, UserID: seed.userID, ExpiresAt: clock.Now().Add(time.Hour)}
		if err := sessions.Create(context.Background(), session); err != nil {
			t.Fatalf("seed session %s: %v", seed.id, err)
		}
	}
	return f
}

// request asks for a link and fails the test when the neutral answer did not
// hold: the caller may not learn whether an email was sent.
func (f *resetFixture) request(t *testing.T, email string) {
	t.Helper()
	if err := f.svc.RequestPasswordReset(context.Background(), email); err != nil {
		t.Fatalf("RequestPasswordReset(%q) = %v, want nil", email, err)
	}
}

// requestToken asks for a link and answers the raw token the mailed link
// carries.
func (f *resetFixture) requestToken(t *testing.T, email string) string {
	t.Helper()
	before := len(f.mailer.sent)
	f.request(t, email)
	if len(f.mailer.sent) != before+1 {
		t.Fatalf("mailed %d messages, want exactly 1 new one", len(f.mailer.sent)-before)
	}
	return sentLinkToken(t, f.mailer.sent[len(f.mailer.sent)-1].email.Body)
}

// sentLinkToken answers the raw token the given mailed body carries.
func sentLinkToken(t *testing.T, body string) string {
	t.Helper()
	const marker = "?token="
	i := strings.Index(body, marker)
	if i < 0 {
		t.Fatalf("the mailed body carries no link:\n%s", body)
	}
	rest := body[i+len(marker):]
	end := strings.IndexFunc(rest, func(r rune) bool {
		return !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f')
	})
	if end < 0 {
		end = len(rest)
	}
	if end == 0 {
		t.Fatalf("the mailed link carries an empty token:\n%s", body)
	}
	return rest[:end]
}

// storedHashOfToken answers the SHA-256 hex digest the store must hold for raw.
func storedHashOfToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// storedSessionIDs answers one account's stored session ids, sorted.
func storedSessionIDs(f *resetFixture, user domain.User) []string {
	ids := make([]string, 0, len(f.sessions.sessions))
	for id, session := range f.sessions.sessions {
		if session.UserID == user.ID {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

// TestPasswordResetRequestIsNeutralForEveryIneligibleRequest proves the ONE
// silent answer (S3/S5/S9): an unknown address, an inactive account, the
// recovery switch OFF and email delivery not configured all return nil, mail
// nothing, and store no link.
func TestPasswordResetRequestIsNeutralForEveryIneligibleRequest(t *testing.T) {
	tests := []struct {
		name  string
		email string
		drift func(t *testing.T, f *resetFixture)
	}{
		{name: "unknown address", email: "nobody@example.com"},
		{name: "inactive account", email: "bea@example.com", drift: func(t *testing.T, f *resetFixture) {
			t.Helper()
			u := *f.users.users[f.bea.ID]
			u.Active = false
			if err := f.users.Update(context.Background(), &u); err != nil {
				t.Fatalf("deactivate the account: %v", err)
			}
		}},
		{name: "recovery switched off", email: "ana@example.com", drift: func(t *testing.T, f *resetFixture) {
			f.policy.settings = application.PasswordSettings{ChangeEnabled: true, RecoveryEnabled: false, MinLength: 12}
			f.policy.stored = true
		}},
		{name: "delivery not configured", email: "ana@example.com", drift: func(t *testing.T, f *resetFixture) {
			f.config.ok = false
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newResetFixture(t)
			if tt.drift != nil {
				tt.drift(t, f)
			}
			before := len(f.tokens.tokens)
			f.request(t, tt.email)
			if len(f.mailer.sent) != 0 {
				t.Errorf("mailed %d messages, want none", len(f.mailer.sent))
			}
			if got := len(f.tokens.tokens); got != before {
				t.Errorf("stored %d links, want %d", got, before)
			}
		})
	}
}

// TestPasswordResetRequestIsThrottledPerAccount proves S9 for the account that
// already asked: a second request inside the window answers the same neutral
// result, mails nothing and stores nothing.
func TestPasswordResetRequestIsThrottledPerAccount(t *testing.T) {
	f := newResetFixture(t)
	f.requestToken(t, "ana@example.com")
	stored := len(f.tokens.tokens)

	f.clock.Advance(4*time.Minute + 59*time.Second)
	f.request(t, "ana@example.com")
	if got := len(f.mailer.sent); got != 1 {
		t.Errorf("mailed %d messages inside the window, want 1", got)
	}
	if got := len(f.tokens.tokens); got != stored {
		t.Errorf("stored %d links inside the window, want %d", got, stored)
	}

	// The other account has its own window: its first request is not throttled.
	f.requestToken(t, "bea@example.com")
}

// TestPasswordResetRequestStoresOnlyTheHashAndMailsTheInstanceLink proves the
// half a caller never sees: the raw token exists only in the mailed link, the
// store holds its SHA-256 digest, the link starts with the stored base URL
// (S7), and the email goes to the account address in the instance language.
func TestPasswordResetRequestStoresOnlyTheHashAndMailsTheInstanceLink(t *testing.T) {
	f := newResetFixture(t)
	f.config.cfg.Language = "es"

	raw := f.requestToken(t, "ana@example.com")

	if len(f.mailer.sent) != 1 {
		t.Fatalf("mailed %d messages, want exactly 1", len(f.mailer.sent))
	}
	sent := f.mailer.sent[0]
	if sent.email.To != "ana@example.com" {
		t.Errorf("To = %q, want the account address", sent.email.To)
	}
	if sent.config.Host != "smtp.example.com" {
		t.Errorf("sent through %q, want the configured server", sent.config.Host)
	}
	if sent.email.Subject != "es:email.password_reset.subject[]" {
		t.Errorf("Subject = %q, want the subject in the instance language", sent.email.Subject)
	}
	for _, want := range []string{
		"es:email.password_reset.intro[ana@example.com]",
		"es:email.password_reset.action[]",
		"es:email.password_reset.expiry[]",
		"es:email.password_reset.ignore[]",
		"https://tkt.example.com/login/reset?token=",
	} {
		if !strings.Contains(sent.email.Body, want) {
			t.Errorf("Body lacks %q:\n%s", want, sent.email.Body)
		}
	}

	if len(raw) != 64 {
		t.Errorf("raw token = %q (%d characters), want 32 random bytes as 64 hex characters", raw, len(raw))
	}
	if got, want := len(f.tokens.tokens), 1; got != want {
		t.Fatalf("stored %d links, want %d", got, want)
	}
	stored := f.tokens.tokens[0]
	if stored.userID != f.ana.ID {
		t.Errorf("stored link owner = %d, want %d", stored.userID, f.ana.ID)
	}
	if stored.hash == raw {
		t.Error("the store holds the raw token, want only its hash (S8)")
	}
	if want := storedHashOfToken(raw); stored.hash != want {
		t.Errorf("stored hash = %q, want the SHA-256 digest %q", stored.hash, want)
	}
	if !stored.createdAt.Equal(f.clock.Now()) || !stored.expiresAt.Equal(f.clock.Now().Add(time.Hour)) {
		t.Errorf("stored instants = (%v, %v), want one hour apart from now", stored.createdAt, stored.expiresAt)
	}
}

// TestPasswordResetRequestTrimsTheBaseURLTrailingSlash proves the link is built
// from the stored instance address without doubling the separator.
func TestPasswordResetRequestTrimsTheBaseURLTrailingSlash(t *testing.T) {
	f := newResetFixture(t)
	f.config.cfg.BaseURL = "https://tkt.example.com/"

	f.requestToken(t, "ana@example.com")

	if body := f.mailer.sent[0].email.Body; !strings.Contains(body, "https://tkt.example.com/login/reset?token=") {
		t.Errorf("Body lacks the link built from the stored base URL:\n%s", body)
	}
}

// TestPasswordResetRequestHandsTheSendToTheDispatcher proves the send leaves
// the caller: with a dispatcher that only records the work, the request returns
// without mailing anything, and the recorded send delivers the message.
func TestPasswordResetRequestHandsTheSendToTheDispatcher(t *testing.T) {
	f := newResetFixture(t)
	f.dispatcher.run = false

	f.request(t, "ana@example.com")
	if len(f.mailer.sent) != 0 {
		t.Fatalf("the request mailed %d messages itself, want the dispatcher to run it", len(f.mailer.sent))
	}
	if len(f.dispatcher.sends) != 1 {
		t.Fatalf("dispatcher received %d sends, want exactly 1", len(f.dispatcher.sends))
	}
	if len(f.tokens.tokens) != 1 {
		t.Fatalf("stored %d links, want 1", len(f.tokens.tokens))
	}
	f.dispatcher.sends[0]()
	if len(f.mailer.sent) != 1 {
		t.Fatalf("the dispatched send mailed %d messages, want 1", len(f.mailer.sent))
	}
}

// TestPasswordResetRequestReportsOnlyStorageFailures proves the boundary of the
// neutral answer: a store failure is an error the caller must see (something is
// broken), while a delivery failure stays in the background path.
func TestPasswordResetRequestReportsOnlyStorageFailures(t *testing.T) {
	f := newResetFixture(t)
	f.mailer.sendErr = errors.New("smtp: 550 rejected")
	f.request(t, "ana@example.com")

	f = newResetFixture(t)
	f.tokens.issueErr = errors.New("disk full")
	if err := f.svc.RequestPasswordReset(context.Background(), "ana@example.com"); err == nil {
		t.Fatal("a storage failure answered nil, want an error")
	}
}

// TestPasswordResetRequestLogsADeliveryFailureWithoutSecrets proves the failure
// log is useful and safe: it names the failure but never the token or the
// recipient address.
func TestPasswordResetRequestLogsADeliveryFailureWithoutSecrets(t *testing.T) {
	f := newResetFixture(t)
	f.mailer.sendErr = errors.New("smtp: 550 <ana@example.com> rejected")

	var logged bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logged)
	t.Cleanup(func() { log.SetOutput(previous) })

	raw := f.requestToken(t, "ana@example.com")

	line := logged.String()
	if line == "" {
		t.Fatal("a delivery failure logged nothing")
	}
	if strings.Contains(line, "ana@example.com") {
		t.Errorf("the log carries the address: %q", line)
	}
	if strings.Contains(line, raw) {
		t.Errorf("the log carries the raw token: %q", line)
	}
	if !strings.Contains(line, "550") {
		t.Errorf("the log dropped the failure detail: %q", line)
	}
}

// TestPasswordResetRequestVoidsTheOlderLinkAfterTheWindow proves S9: the second
// request inside five minutes is throttled, the one at exactly five minutes is
// allowed, and it voids the older link so only the newest one can be redeemed.
func TestPasswordResetRequestVoidsTheOlderLinkAfterTheWindow(t *testing.T) {
	f := newResetFixture(t)
	ctx := context.Background()

	first := f.requestToken(t, "ana@example.com")

	f.clock.Advance(4 * time.Minute)
	f.request(t, "ana@example.com")
	if got := len(f.mailer.sent); got != 1 {
		t.Fatalf("mailed %d messages inside the window, want 1", got)
	}

	f.clock.Advance(time.Minute) // exactly five minutes after the first request
	second := f.requestToken(t, "ana@example.com")
	if second == first {
		t.Fatal("the second request mailed the same token, want a fresh one")
	}

	usable, err := f.svc.CheckPasswordResetToken(ctx, first)
	if err != nil {
		t.Fatalf("CheckPasswordResetToken(old link): %v", err)
	}
	if usable {
		t.Error("the older link is still usable after a new request, want it voided")
	}
	if usable, err = f.svc.CheckPasswordResetToken(ctx, second); err != nil || !usable {
		t.Fatalf("CheckPasswordResetToken(new link) = %v, %v; want usable", usable, err)
	}
}

// TestCheckPasswordResetTokenLooksTheSameForEveryUnusableLink proves the page
// cannot tell why a link is dead: unknown, spent and expired all answer
// unusable, and a live one answers usable without being consumed.
func TestCheckPasswordResetTokenLooksTheSameForEveryUnusableLink(t *testing.T) {
	ctx := context.Background()

	f := newResetFixture(t)
	raw := f.requestToken(t, "ana@example.com")
	if usable, err := f.svc.CheckPasswordResetToken(ctx, raw); err != nil || !usable {
		t.Fatalf("CheckPasswordResetToken(live link) = %v, %v; want usable", usable, err)
	}
	// Checking a link never consumes it: a second check answers the same.
	if usable, err := f.svc.CheckPasswordResetToken(ctx, raw); err != nil || !usable {
		t.Fatalf("a second check = %v, %v; want usable", usable, err)
	}
	if err := f.svc.ResetPassword(ctx, raw, resetPassword); err != nil {
		t.Fatalf("ResetPassword: %v", err)
	}
	if usable, err := f.svc.CheckPasswordResetToken(ctx, raw); err != nil || usable {
		t.Fatalf("CheckPasswordResetToken(spent link) = %v, %v; want unusable", usable, err)
	}

	expiring := newResetFixture(t)
	expired := expiring.requestToken(t, "ana@example.com")
	expiring.clock.Advance(time.Hour)
	if usable, err := expiring.svc.CheckPasswordResetToken(ctx, expired); err != nil || usable {
		t.Fatalf("CheckPasswordResetToken(at exactly one hour) = %v, %v; want unusable", usable, err)
	}

	for _, raw := range []string{"0123456789abcdef", ""} {
		unknown := newResetFixture(t)
		if usable, err := unknown.svc.CheckPasswordResetToken(ctx, raw); err != nil || usable {
			t.Errorf("CheckPasswordResetToken(%q) = %v, %v; want unusable and no error", raw, usable, err)
		}
	}
}

// TestResetPasswordReplacesThePasswordAndSignsEverySessionOut proves S3: a
// valid link stores the new password, spends the link and deletes EVERY session
// of that account, leaving another account untouched.
func TestResetPasswordReplacesThePasswordAndSignsEverySessionOut(t *testing.T) {
	f := newResetFixture(t)
	ctx := context.Background()

	raw := f.requestToken(t, "ana@example.com")
	if err := f.svc.ResetPassword(ctx, raw, resetPassword); err != nil {
		t.Fatalf("ResetPassword: %v", err)
	}

	stored := f.users.users[f.ana.ID]
	if !application.VerifyPassword(stored.PasswordHash, resetPassword) {
		t.Error("the stored hash does not hold the new password")
	}
	if application.VerifyPassword(stored.PasswordHash, ownCurrentPassword) {
		t.Error("the previous password still verifies after the reset")
	}
	if got := storedSessionIDs(f, f.ana); len(got) != 0 {
		t.Errorf("sessions of the account = %v, want none (S3)", got)
	}
	if got, want := storedSessionIDs(f, f.bea), []string{"sess-bea"}; !reflect.DeepEqual(got, want) {
		t.Errorf("the other account's sessions = %v, want %v", got, want)
	}
	if got := len(f.tokens.tokens); got != 1 {
		t.Fatalf("stored links = %d, want the spent one kept as history", got)
	}
	if f.tokens.tokens[0].usedAt == nil {
		t.Error("the redeemed link is not marked spent")
	}
}

// TestResetPasswordRefusesEveryUnusableLinkIdentically proves an unknown, an
// expired and an already spent link answer the ONE refusal, and that a refused
// reset leaves the password and the sessions exactly as they were.
func TestResetPasswordRefusesEveryUnusableLinkIdentically(t *testing.T) {
	ctx := context.Background()

	unknown := newResetFixture(t)
	unknown.requestToken(t, "ana@example.com")
	checkResetRefusedUnchanged(t, unknown, "0123456789abcdef")

	expired := newResetFixture(t)
	expiredRaw := expired.requestToken(t, "ana@example.com")
	expired.clock.Advance(time.Hour + time.Minute)
	checkResetRefusedUnchanged(t, expired, expiredRaw)

	spent := newResetFixture(t)
	spentRaw := spent.requestToken(t, "ana@example.com")
	if err := spent.svc.ResetPassword(ctx, spentRaw, resetPassword); err != nil {
		t.Fatalf("ResetPassword with a valid link: %v", err)
	}
	afterReset := spent.users.users[spent.ana.ID].PasswordHash
	checkResetRefusedUnchanged(t, spent, spentRaw)
	if got := spent.users.users[spent.ana.ID].PasswordHash; got != afterReset {
		t.Error("reusing a spent link changed the password")
	}
}

// checkResetRefusedUnchanged proves one ResetPassword refusal is the single
// link error and that it changed nothing.
func checkResetRefusedUnchanged(t *testing.T, f *resetFixture, raw string) {
	t.Helper()
	before := f.users.users[f.ana.ID].PasswordHash
	sessions := storedSessionIDs(f, f.ana)

	err := f.svc.ResetPassword(context.Background(), raw, resetPassword)
	var invalid *application.PasswordResetLinkError
	if !errors.As(err, &invalid) {
		t.Fatalf("ResetPassword = %v, want PasswordResetLinkError", err)
	}
	if got := f.users.users[f.ana.ID].PasswordHash; got != before {
		t.Error("a refused reset wrote the password")
	}
	if got := storedSessionIDs(f, f.ana); !reflect.DeepEqual(got, sessions) {
		t.Errorf("sessions after the refusal = %v, want %v", got, sessions)
	}
}

// TestResetPasswordRefusesAShortPasswordAndKeepsTheLink proves the minimum is
// enforced BEFORE the link is spent (S4/S3): a too-short password is refused by
// its field, and the same link still works with an accepted one.
func TestResetPasswordRefusesAShortPasswordAndKeepsTheLink(t *testing.T) {
	f := newResetFixture(t)
	ctx := context.Background()
	raw := f.requestToken(t, "ana@example.com")

	err := f.svc.ResetPassword(ctx, raw, "eleven-char")
	var invalid *domain.ValidationError
	if !errors.As(err, &invalid) || invalid.Key != "error.password_too_short" {
		t.Fatalf("short password = %v, want error.password_too_short", err)
	}
	if f.tokens.tokens[0].usedAt != nil {
		t.Fatal("the refused attempt spent the link")
	}
	if usable, err := f.svc.CheckPasswordResetToken(ctx, raw); err != nil || !usable {
		t.Fatalf("CheckPasswordResetToken after the refusal = %v, %v; want the link still usable", usable, err)
	}
	if err := f.svc.ResetPassword(ctx, raw, resetPassword); err != nil {
		t.Fatalf("ResetPassword after the refusal: %v", err)
	}
}

// TestResetPasswordHonoursTheConfiguredMinimum proves the instance minimum is
// the one enforced, not only the default.
func TestResetPasswordHonoursTheConfiguredMinimum(t *testing.T) {
	f := newResetFixture(t)
	f.policy.settings = application.PasswordSettings{ChangeEnabled: true, RecoveryEnabled: true, MinLength: 16}
	f.policy.stored = true
	raw := f.requestToken(t, "ana@example.com")

	if err := f.svc.ResetPassword(context.Background(), raw, "fifteen-chars-1"); err == nil {
		t.Fatal("15 characters accepted under a minimum of 16")
	}
	if err := f.svc.ResetPassword(context.Background(), raw, "sixteen-chars-12"); err != nil {
		t.Fatalf("16 characters refused under a minimum of 16: %v", err)
	}
}

// TestResetPasswordRefusesWhileTheSwitchIsOff proves S1: with recovery OFF the
// use case refuses and the link stays untouched, so turning the switch back on
// does not meet a link the refused attempt burned.
func TestResetPasswordRefusesWhileTheSwitchIsOff(t *testing.T) {
	f := newResetFixture(t)
	ctx := context.Background()
	raw := f.requestToken(t, "ana@example.com")
	before := f.users.users[f.ana.ID].PasswordHash
	sessions := storedSessionIDs(f, f.ana)
	f.policy.settings = application.PasswordSettings{ChangeEnabled: true, RecoveryEnabled: false, MinLength: 12}
	f.policy.stored = true

	err := f.svc.ResetPassword(ctx, raw, resetPassword)
	var forbidden *domain.ForbiddenError
	if !errors.As(err, &forbidden) {
		t.Fatalf("switch OFF = %v, want a ForbiddenError", err)
	}
	if got := f.users.users[f.ana.ID].PasswordHash; got != before {
		t.Error("switch OFF wrote the password")
	}
	if got := storedSessionIDs(f, f.ana); !reflect.DeepEqual(got, sessions) {
		t.Errorf("switch OFF deleted sessions: %v", got)
	}
	if f.tokens.tokens[0].usedAt != nil {
		t.Error("switch OFF spent the link")
	}
}

// gatedMailer holds every send until the test releases it, so a test can prove
// the request answered while the mail server was still holding the message. The
// channels are the synchronization: no sleep and no timeout takes part.
type gatedMailer struct {
	started chan application.OutgoingEmail
	release chan struct{}
	done    chan struct{}
}

func newGatedMailer() *gatedMailer {
	return &gatedMailer{
		started: make(chan application.OutgoingEmail, 1),
		release: make(chan struct{}),
		done:    make(chan struct{}),
	}
}

func (m *gatedMailer) Send(_ context.Context, _ application.SMTPConfig, email application.OutgoingEmail) error {
	m.started <- email
	<-m.release
	close(m.done)
	return nil
}

// TestAsyncDispatcherAnswersBeforeTheMailServerDoes proves the production
// dispatcher (S3/S9) runs the send on its own goroutine: the request answers
// while the mailer is still holding the message, so how long a mail server takes
// cannot distinguish a known address from an unknown one. The test also rules
// out an inline runner: with one, the answer below would never arrive.
func TestAsyncDispatcherAnswersBeforeTheMailServerDoes(t *testing.T) {
	f := newResetFixture(t)
	mailer := newGatedMailer()
	svc := application.NewPasswordResetService(f.users, f.tokens, f.policy, f.config, mailer, senderRenderer{}, application.AsyncPasswordResetDispatch, f.clock)

	answers := make(chan error, 1)
	go func() { answers <- svc.RequestPasswordReset(context.Background(), "ana@example.com") }()

	sent := <-mailer.started // the send is running on its own goroutine
	if err := <-answers; err != nil {
		t.Fatalf("RequestPasswordReset = %v, want nil", err)
	}
	// The link is stored before the answer, so the visitor could already use it
	// while delivery is still in flight.
	if got := len(f.tokens.tokens); got != 1 {
		t.Errorf("stored %d links, want 1 while the mail server is still sending", got)
	}

	close(mailer.release)
	<-mailer.done
	if sent.To != "ana@example.com" {
		t.Errorf("email sent to %q, want the account address", sent.To)
	}
}
