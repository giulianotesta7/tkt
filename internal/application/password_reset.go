package application

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log"
	"strings"
	"time"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// Self-service password recovery (issue #414, S3/S5/S7/S8/S9): a user who
// forgot their password asks for a link by email, the instance mails a
// single-use link that works for one hour, and using it stores the new password
// and signs every session of that account out. The constants below are its
// numbers; the service documents the rules.
const (
	// PasswordResetTokenTTL is how long a mailed link stays usable (S3): one
	// hour after it was issued.
	PasswordResetTokenTTL = time.Hour
	// PasswordResetThrottle is the shortest gap between two links for the same
	// account (S9): one email per account every five minutes. A request exactly
	// at the boundary is allowed.
	PasswordResetThrottle = 5 * time.Minute
	// PasswordResetSendTimeout bounds one delivery attempt once it runs off the
	// request goroutine, so a stalled mail server cannot hold the send forever.
	PasswordResetSendTimeout = 30 * time.Second
	// PasswordResetPath is the public page a mailed link opens.
	PasswordResetPath = "/login/reset"

	// passwordResetTokenBytes is the entropy of a reset token: 32 random bytes,
	// the same order as a session id, hex-encoded into the link.
	passwordResetTokenBytes = 32
)

// ErrMsgPasswordResetLinkInvalid is the ONE refusal for a reset link that cannot
// be used (issue #414, S3). Unknown, already spent and expired links all answer
// it, so a visitor holding a stale link learns nothing about another one.
const ErrMsgPasswordResetLinkInvalid = "this password reset link is no longer valid"

// PasswordResetLinkError is the application-level refusal of an unusable reset
// link. It carries no detail on purpose: the three ways a link dies are
// indistinguishable from the outside.
type PasswordResetLinkError struct{}

func (e *PasswordResetLinkError) Error() string { return ErrMsgPasswordResetLinkInvalid }

// PasswordResetDispatch hands one prepared send to the runner that executes it.
// Production passes a function that starts a goroutine, so the request answers
// as soon as the message is ready; a test passes one that runs the send where
// it stands. A nil dispatcher runs the send inline, so a composition root that
// forgot to wire one still delivers.
type PasswordResetDispatch func(send func())

// PasswordResetService implements the self-service recovery use cases.
//
// Three rules shape it. The request answers ONE neutral result, so it can never
// be used to learn whether an address belongs to an active account, whether the
// recovery switch is on, or whether email is configured (S3/S5/S9). The database
// only ever holds the SHA-256 digest of a token, never the token a link carries
// (S8), and the link is built from the stored instance address rather than from
// the request that asked for it (S7). The delivery is handed to a dispatcher, so
// the caller's answer does not depend on how long the mail server takes and the
// two kinds of address cannot be told apart by timing either.
//
// Every read it needs is a port of its own family: the users it resolves, the
// links it issues and redeems, the instance policy that says whether the path
// is offered and how long a new password must be, and the email configuration
// that says whether a message can be delivered at all.
type PasswordResetService struct {
	users    UserStore
	tokens   PasswordResetStore
	policy   PasswordSettingsStore
	email    DeliveryConfigSource
	mailer   Mailer
	renderer MessageRenderer
	dispatch PasswordResetDispatch
	clock    domain.Clock
	// companySignIn, when wired, carries the instance switch that keeps
	// password sign-in — and therefore recovery — to root alone (issue #419,
	// S5).
	companySignIn CompanySignInSettingsStore
}

// NewPasswordResetService wires the recovery use cases.
func NewPasswordResetService(users UserStore, tokens PasswordResetStore, policy PasswordSettingsStore, email DeliveryConfigSource, mailer Mailer, renderer MessageRenderer, dispatch PasswordResetDispatch, clock domain.Clock) *PasswordResetService {
	return &PasswordResetService{
		users:    users,
		tokens:   tokens,
		policy:   policy,
		email:    email,
		mailer:   mailer,
		renderer: renderer,
		dispatch: dispatch,
		clock:    clock,
	}
}

// WithCompanySignIn wires the company sign-in configuration recovery reads
// (issue #419, S5). Without it password sign-in — and recovery with it — stays
// available to every account.
func (s *PasswordResetService) WithCompanySignIn(store CompanySignInSettingsStore) *PasswordResetService {
	s.companySignIn = store
	return s
}

// passwordPolicy answers the instance policy, defaulting to the shipped one
// (both paths ON, minimum 12) when no store is wired. A store failure is
// returned, never defaulted away: a write must not proceed under a policy
// nobody read.
func (s *PasswordResetService) passwordPolicy(ctx context.Context) (PasswordSettings, error) {
	if s.policy == nil {
		return DefaultPasswordSettings(), nil
	}
	return s.policy.GetPasswordSettings(ctx)
}

// RequestPasswordReset asks for a link to be mailed to email (S3).
//
// The answer is the SAME nil for every case a visitor must not be able to tell
// apart: an unknown address, an inactive account, the recovery switch OFF
// (S1), password sign-in off for this account (#419, S5), email delivery not
// configured (S5), and a request inside the throttle window (S9). Only a store
// failure answers an error, because that is a broken instance rather than a
// fact about the address. Nothing is mailed and no link is stored in any of the
// neutral cases.
//
// On the path that does send, the token is 32 random bytes rendered as hex, the
// database receives only its SHA-256 digest (S8), the link is built from the
// stored base URL (S7), and the message is rendered in the instance language
// and handed to the dispatcher so it leaves the request goroutine.
func (s *PasswordResetService) RequestPasswordReset(ctx context.Context, email string) error {
	policy, err := s.passwordPolicy(ctx)
	if err != nil {
		return err
	}
	if !policy.RecoveryEnabled {
		return nil
	}
	address := strings.TrimSpace(email)
	if address == "" {
		return nil
	}
	// A nil store or mailer is a composition-root defect, and the neutral answer
	// is still the right one for the visitor: nothing can be issued or mailed.
	if s.tokens == nil || s.email == nil || s.mailer == nil {
		return nil
	}
	user, err := s.users.GetByEmail(ctx, address)
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return nil
	case err != nil:
		return err
	case !user.Active:
		return nil
	}
	// A verified non-root account has no password path to recover while
	// password sign-in is off (issue #419, S5), so nothing is mailed and the
	// answer stays the neutral one. Root keeps its link.
	allowed, err := passwordSignInAllowed(ctx, s.companySignIn, user.Role)
	if err != nil {
		return err
	}
	if !allowed {
		return nil
	}
	cfg, ok, err := s.email.DeliveryConfig(ctx)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	now := s.clock.Now()
	last, err := s.tokens.LastPasswordResetRequest(ctx, user.ID)
	if err != nil {
		return err
	}
	if last != nil && now.Sub(*last) < PasswordResetThrottle {
		return nil
	}
	raw, err := newPasswordResetSecret()
	if err != nil {
		return err
	}
	if err := s.tokens.IssuePasswordResetToken(ctx, user.ID, passwordResetTokenHash(raw), now, now.Add(PasswordResetTokenTTL)); err != nil {
		return err
	}

	link := strings.TrimRight(cfg.BaseURL, "/") + PasswordResetPath + "?token=" + raw
	render := func(m Msg) string { return s.renderer.Render(cfg.Language, m) }
	message := OutgoingEmail{
		// The account's own stored address, never the submitted string: the
		// visitor typed something that resolved to this account, and the link
		// belongs to the account.
		To:      user.Email,
		Subject: render(MsgRef("email.password_reset.subject")),
		Body:    resetEmailBody(render, user.Email, link),
	}
	s.dispatchSend(ctx, cfg.SMTP, message, raw)
	return nil
}

// resetEmailBody composes the plain-text message: one short paragraph per line
// group, with the link on its own line under the sentence that introduces it.
// The copy lives in the catalogues, so the language is the instance's and an
// instance can translate it; the body itself carries no English.
func resetEmailBody(render func(Msg) string, address, link string) string {
	return strings.Join([]string{
		render(MsgRef("email.password_reset.intro", Text(address))),
		render(MsgRef("email.password_reset.action")) + "\n" + render(MsgRef("email.password_reset.link", Text(link))),
		render(MsgRef("email.password_reset.expiry")),
		render(MsgRef("email.password_reset.ignore")),
		render(MsgRef("email.password_reset.footer")),
	}, "\n\n") + "\n"
}

// dispatchSend hands one send to the dispatcher. The send detaches from the
// request context (a cancelled request must not silently drop a link that was
// already stored) and bounds itself, so a stalled mail server cannot keep it
// alive forever. A failure is logged with the token and the address redacted:
// the instance operator needs to know delivery broke, and the log is not a safe
// place for a working credential.
func (s *PasswordResetService) dispatchSend(ctx context.Context, cfg SMTPConfig, message OutgoingEmail, raw string) {
	send := func() {
		sendCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), PasswordResetSendTimeout)
		defer cancel()
		if err := s.mailer.Send(sendCtx, cfg, message); err != nil {
			log.Printf("password reset: sending the email failed: %s", redactResetSecrets(err.Error(), raw, message.To))
		}
	}
	if s.dispatch == nil {
		send()
		return
	}
	s.dispatch(send)
}

// redactResetSecrets removes every occurrence of the given values from a log
// detail, so a transport error that echoes the recipient back cannot leak the
// address and no failure path can leak the token.
func redactResetSecrets(detail string, secrets ...string) string {
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		detail = strings.ReplaceAll(detail, secret, "[redacted]")
	}
	return detail
}

// CheckPasswordResetToken reports whether raw can still be redeemed, without
// consuming it (S3). It is what the reset page asks before rendering the form:
// unknown, spent and expired all answer false, so the page shows one state.
func (s *PasswordResetService) CheckPasswordResetToken(ctx context.Context, raw string) (bool, error) {
	if s.tokens == nil {
		return false, errors.New("the password reset store is not wired")
	}
	if strings.TrimSpace(raw) == "" {
		return false, nil
	}
	allowed, err := s.linkOwnerMayUsePasswords(ctx, passwordResetTokenHash(raw))
	if err != nil || !allowed {
		return false, err
	}
	return s.tokens.PasswordResetTokenUsable(ctx, passwordResetTokenHash(raw), s.clock.Now())
}

// linkOwnerMayUsePasswords answers whether the account a link belongs to may
// still set a password (issue #419, S5). While password sign-in is on, the
// answer is yes without reading the link. While it is off, only root's link
// stays usable: a link mailed to anyone else before the switch went off reads
// exactly like an expired one. An unusable link answers yes here, so the
// ordinary refusal that follows stays the one every unusable link gets.
func (s *PasswordResetService) linkOwnerMayUsePasswords(ctx context.Context, tokenHash string) (bool, error) {
	on, err := companySignInPasswordEnabled(ctx, s.companySignIn)
	if err != nil || on {
		return on, err
	}
	userID, ok, err := s.tokens.PasswordResetTokenOwner(ctx, tokenHash, s.clock.Now())
	if err != nil || !ok {
		return !ok, err
	}
	owner, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return false, err
	}
	return owner.Role == domain.RoleRoot, nil
}

// ResetPassword stores newPassword under the account the link belongs to (S3).
//
// The order of the guards is deliberate. The instance policy is read first: the
// recovery switch OFF refuses the whole path (S1) with nothing read or written.
// The new password is then validated against the configured minimum BEFORE the
// link is touched (S4), so a too-short submission leaves the link usable and
// the visitor can try again. Only then is the link redeemed, in ONE transaction
// that marks it spent, voids the account's other pending links and deletes
// EVERY session of that account — using the link signs the account out
// everywhere.
//
// An unknown, spent or expired link answers PasswordResetLinkError and changes
// nothing.
func (s *PasswordResetService) ResetPassword(ctx context.Context, raw, newPassword string) error {
	policy, err := s.passwordPolicy(ctx)
	if err != nil {
		return err
	}
	if !policy.RecoveryEnabled {
		return domain.NewForbiddenError("resetting a password is disabled on this instance")
	}
	if err := ValidateNewPassword(newPassword, policy.MinLength); err != nil {
		return err
	}
	if s.tokens == nil {
		return errors.New("the password reset store is not wired")
	}
	allowed, err := s.linkOwnerMayUsePasswords(ctx, passwordResetTokenHash(raw))
	if err != nil {
		return err
	}
	if !allowed {
		return &PasswordResetLinkError{}
	}
	hash, err := HashPassword(newPassword)
	if err != nil {
		return err
	}
	err = s.tokens.RedeemPasswordResetToken(ctx, passwordResetTokenHash(raw), hash, s.clock.Now())
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return &PasswordResetLinkError{}
	case err != nil:
		return err
	}
	return nil
}

// newPasswordResetSecret answers the raw token a mailed link carries: 32 random
// bytes rendered as hex.
func newPasswordResetSecret() (string, error) {
	buf := make([]byte, passwordResetTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// passwordResetTokenHash answers the stored form of a reset token (S8): the
// SHA-256 hex digest of what the link carries. The raw value is never persisted
// and never logged, so a copy of the database yields no working link.
func passwordResetTokenHash(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
