package application_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// Company sign-in turns password sign-in off for everyone but root (issue
// #419, S4/S5): an administrator offers Google Workspace or Microsoft Entra,
// and passwords stay as root's break-glass. Every refusal here happens AFTER
// the password was verified, so it can never enumerate accounts, and none of
// them writes a session, a link or a hash.

// companySignInSwitch answers a CompanySignInSettingsStore fixed at one value
// of the password sign-in switch; the rest of the configuration keeps its
// shipped defaults, so the tests vary exactly one fact.
func companySignInSwitch(t *testing.T, enabled bool) *fakeCompanySignInStore {
	t.Helper()
	store := newFakeCompanySignInStore()
	settings := application.DefaultCompanySignInSettings()
	settings.PasswordSignInEnabled = enabled
	if err := store.SetCompanySignIn(context.Background(), settings); err != nil {
		t.Fatalf("store the company sign-in switch: %v", err)
	}
	return store
}

// seedRoleWithPassword inserts an account with an explicit role and a REAL
// bcrypt hash, so the sign-in switch can be exercised for root and for an
// ordinary account through the same login path.
func seedRoleWithPassword(t *testing.T, users *fakeUserStore, name, email string, role domain.Role, password string) domain.User {
	t.Helper()
	u := users.seedRole(name, email, role, true)
	hash, err := application.HashPassword(password)
	if err != nil {
		t.Fatalf("hash the seed password for %s: %v", email, err)
	}
	u.PasswordHash = hash
	if err := users.Update(context.Background(), &u); err != nil {
		t.Fatalf("store the seed password for %s: %v", email, err)
	}
	return u
}

// TestLoginRefusesValidNonRootPasswordWhilePasswordSignInIsOff is S4: the
// password is verified first, and only then does a non-root account learn that
// this path is off — with its own refusal and no session. A wrong password
// keeps the ONE generic error, so the refusal cannot enumerate accounts.
func TestLoginRefusesValidNonRootPasswordWhilePasswordSignInIsOff(t *testing.T) {
	clock := fixedClock()
	users := newFakeUserStore()
	sessions := newFakeSessionStore(clock)
	svc := application.NewAuthService(users, sessions, clock).WithCompanySignIn(companySignInSwitch(t, false))
	ana := createUserWithPassword(users, "Ana", "ana@example.com", "s3cret-pass")

	_, err := svc.Login(context.Background(), ana.Email, "s3cret-pass")
	var off *application.PasswordSignInOffError
	if !errors.As(err, &off) {
		t.Fatalf("Login with a valid non-root password = %v, want PasswordSignInOffError", err)
	}
	if err.Error() != application.ErrMsgPasswordSignInOff {
		t.Fatalf("refusal message = %q, want %q", err.Error(), application.ErrMsgPasswordSignInOff)
	}
	if len(sessions.sessions) != 0 {
		t.Fatalf("refused login created %d sessions, want none", len(sessions.sessions))
	}

	_, err = svc.Login(context.Background(), ana.Email, "wrong-password")
	var invalid *application.InvalidCredentialsError
	if !errors.As(err, &invalid) {
		t.Fatalf("Login with a wrong password = %v, want the generic InvalidCredentialsError", err)
	}
	if len(sessions.sessions) != 0 {
		t.Fatalf("refused login created %d sessions, want none", len(sessions.sessions))
	}
}

// TestLoginKeepsPasswordSignInForRootWhileItIsOff is S4's break-glass: root
// still signs in with a password while the switch keeps everyone else out.
func TestLoginKeepsPasswordSignInForRootWhileItIsOff(t *testing.T) {
	clock := fixedClock()
	users := newFakeUserStore()
	sessions := newFakeSessionStore(clock)
	svc := application.NewAuthService(users, sessions, clock).WithCompanySignIn(companySignInSwitch(t, false))
	root := seedRoleWithPassword(t, users, "Root", "root@example.com", domain.RoleRoot, "root-pass-12-x")

	session, err := svc.Login(context.Background(), root.Email, "root-pass-12-x")
	if err != nil {
		t.Fatalf("root Login = %v, want a session", err)
	}
	if session.UserID != root.ID {
		t.Fatalf("session user = %d, want root %d", session.UserID, root.ID)
	}
	if len(sessions.sessions) != 1 {
		t.Fatalf("root login created %d sessions, want 1", len(sessions.sessions))
	}
}

// TestLoginFailsClosedWhenTheCompanySignInSettingsCannotBeRead proves a broken
// settings read is an operational failure, never a silent allow: the switch
// decides whether a password may open a session, so an unreadable switch
// refuses it, exactly as an unreadable password policy refuses a write.
func TestLoginFailsClosedWhenTheCompanySignInSettingsCannotBeRead(t *testing.T) {
	clock := fixedClock()
	users := newFakeUserStore()
	sessions := newFakeSessionStore(clock)
	store := newFakeCompanySignInStore()
	store.getErr = errors.New("settings unavailable")
	svc := application.NewAuthService(users, sessions, clock).WithCompanySignIn(store)
	ana := createUserWithPassword(users, "Ana", "ana@example.com", "s3cret-pass")

	_, err := svc.Login(context.Background(), ana.Email, "s3cret-pass")
	if err == nil {
		t.Fatal("Login with an unreadable company sign-in switch = nil, want an error")
	}
	var (
		off     *application.PasswordSignInOffError
		invalid *application.InvalidCredentialsError
	)
	if errors.As(err, &off) || errors.As(err, &invalid) {
		t.Fatalf("Login error = %v, want the store failure itself", err)
	}
	if len(sessions.sessions) != 0 {
		t.Fatalf("failed login created %d sessions, want none", len(sessions.sessions))
	}
}

// TestLoginKeepsPasswordSignInWhileTheSwitchIsOn is the regression guard for
// S4: wiring the switch and leaving it ON changes nothing about sign-in.
func TestLoginKeepsPasswordSignInWhileTheSwitchIsOn(t *testing.T) {
	clock := fixedClock()
	users := newFakeUserStore()
	sessions := newFakeSessionStore(clock)
	svc := application.NewAuthService(users, sessions, clock).WithCompanySignIn(companySignInSwitch(t, true))
	ana := createUserWithPassword(users, "Ana", "ana@example.com", "s3cret-pass")

	session, err := svc.Login(context.Background(), ana.Email, "s3cret-pass")
	if err != nil {
		t.Fatalf("Login with the switch on = %v, want a session", err)
	}
	if session.UserID != ana.ID {
		t.Fatalf("session user = %d, want %d", session.UserID, ana.ID)
	}
}

// TestPasswordSignInOffSendsNoRecoveryLinkToANonRootAccount is S5: the public
// recovery form still answers its neutral result, and nothing is mailed or
// stored for an account that could not use a password anyway.
func TestPasswordSignInOffSendsNoRecoveryLinkToANonRootAccount(t *testing.T) {
	f := newResetFixture(t)
	f.svc = application.NewPasswordResetService(f.users, f.tokens, f.policy, f.config, f.mailer, senderRenderer{}, f.dispatcher.dispatch, f.clock).
		WithCompanySignIn(companySignInSwitch(t, false))

	if err := f.svc.RequestPasswordReset(context.Background(), f.ana.Email); err != nil {
		t.Fatalf("RequestPasswordReset = %v, want the neutral nil", err)
	}
	if len(f.mailer.sent) != 0 {
		t.Fatalf("mailed %d messages, want none", len(f.mailer.sent))
	}
	if len(f.tokens.tokens) != 0 {
		t.Fatalf("stored %d links, want none", len(f.tokens.tokens))
	}
}

// TestPasswordSignInOffKeepsRecoveryForRoot is the other half of S5: root's
// break-glass still includes a mailed link.
func TestPasswordSignInOffKeepsRecoveryForRoot(t *testing.T) {
	f := newResetFixture(t)
	f.svc = application.NewPasswordResetService(f.users, f.tokens, f.policy, f.config, f.mailer, senderRenderer{}, f.dispatcher.dispatch, f.clock).
		WithCompanySignIn(companySignInSwitch(t, false))
	root := seedRoleWithPassword(t, f.users, "Root", "root@example.com", domain.RoleRoot, ownCurrentPassword)

	if err := f.svc.RequestPasswordReset(context.Background(), root.Email); err != nil {
		t.Fatalf("RequestPasswordReset(root) = %v, want the neutral nil", err)
	}
	if len(f.mailer.sent) != 1 {
		t.Fatalf("mailed %d messages for root, want exactly 1", len(f.mailer.sent))
	}
	if len(f.tokens.tokens) != 1 {
		t.Fatalf("stored %d links for root, want exactly 1", len(f.tokens.tokens))
	}
}

// TestPasswordSignInOffRefusesOwnPasswordChangeForNonRoot is S5's Preferences
// half: the signed-in account is refused before anything is verified, hashed
// or written, so the stored hash and every session stay exactly as they were.
func TestPasswordSignInOffRefusesOwnPasswordChangeForNonRoot(t *testing.T) {
	f := newOwnPasswordFixture(t)
	f.svc = application.NewUserService(f.users, f.clock).
		WithPasswordSettings(f.policy).
		WithPasswordChangeStore(f.changes).
		WithCompanySignIn(companySignInSwitch(t, false))
	before := f.state(t, f.actor)

	err := f.svc.ChangeOwnPassword(context.Background(), f.actor, "sess-current", ownCurrentPassword, ownNextPassword)
	var forbidden *domain.ForbiddenError
	if !errors.As(err, &forbidden) {
		t.Fatalf("ChangeOwnPassword for a non-root account = %v, want ForbiddenError", err)
	}
	if got := f.state(t, f.actor); !reflect.DeepEqual(got, before) {
		t.Fatalf("refused change mutated the account: %+v, want %+v", got, before)
	}
}

// TestPasswordSignInOffKeepsOwnPasswordChangeForRoot is the other half of that
// rule: root changes its own password while the switch keeps everyone else out.
func TestPasswordSignInOffKeepsOwnPasswordChangeForRoot(t *testing.T) {
	f := newOwnPasswordFixture(t)
	f.svc = application.NewUserService(f.users, f.clock).
		WithPasswordSettings(f.policy).
		WithPasswordChangeStore(f.changes).
		WithCompanySignIn(companySignInSwitch(t, false))
	root := seedRoleWithPassword(t, f.users, "Root", "root@example.com", domain.RoleRoot, ownCurrentPassword)
	session := &domain.Session{ID: "sess-root", UserID: root.ID, ExpiresAt: f.clock.Now().Add(time.Hour)}
	if err := f.sessions.Create(context.Background(), session); err != nil {
		t.Fatalf("seed the root session: %v", err)
	}

	if err := f.svc.ChangeOwnPassword(context.Background(), root, "sess-root", ownCurrentPassword, ownNextPassword); err != nil {
		t.Fatalf("ChangeOwnPassword(root) = %v, want nil", err)
	}
	stored, err := f.users.GetByID(context.Background(), root.ID)
	if err != nil {
		t.Fatalf("read the stored root: %v", err)
	}
	if !application.VerifyPassword(stored.PasswordHash, ownNextPassword) {
		t.Fatal("root's password was not replaced")
	}
}

// TestOwnPasswordChangeKeepsWorkingWhileTheSwitchIsOn is the regression guard
// for S5: wiring the switch and leaving it ON changes nothing.
func TestOwnPasswordChangeKeepsWorkingWhileTheSwitchIsOn(t *testing.T) {
	f := newOwnPasswordFixture(t)
	f.svc = application.NewUserService(f.users, f.clock).
		WithPasswordSettings(f.policy).
		WithPasswordChangeStore(f.changes).
		WithCompanySignIn(companySignInSwitch(t, true))

	if err := f.svc.ChangeOwnPassword(context.Background(), f.actor, "sess-current", ownCurrentPassword, ownNextPassword); err != nil {
		t.Fatalf("ChangeOwnPassword with the switch on = %v, want nil", err)
	}
	stored, err := f.users.GetByID(context.Background(), f.actor.ID)
	if err != nil {
		t.Fatalf("read the stored account: %v", err)
	}
	if !application.VerifyPassword(stored.PasswordHash, ownNextPassword) {
		t.Fatal("the password was not replaced")
	}
}

// TestPasswordSignInOffRefusesALinkIssuedToANonRootAccount closes the other
// door of S5: a link mailed before password sign-in was turned off reads as
// unusable and cannot set a password, and the refusal changes nothing — the
// stored hash, the sessions and the link stay as they were.
func TestPasswordSignInOffRefusesALinkIssuedToANonRootAccount(t *testing.T) {
	f := newResetFixture(t)
	ctx := context.Background()
	raw := f.requestToken(t, "ana@example.com")
	before := f.users.users[f.ana.ID].PasswordHash
	sessionsBefore := storedSessionIDs(f, f.ana)
	f.svc.WithCompanySignIn(companySignInSwitch(t, false))

	if usable, err := f.svc.CheckPasswordResetToken(ctx, raw); err != nil || usable {
		t.Fatalf("CheckPasswordResetToken = %v, %v; want false with the switch off", usable, err)
	}
	var linkErr *application.PasswordResetLinkError
	if err := f.svc.ResetPassword(ctx, raw, resetPassword); !errors.As(err, &linkErr) {
		t.Fatalf("ResetPassword = %v, want PasswordResetLinkError", err)
	}
	if f.users.users[f.ana.ID].PasswordHash != before {
		t.Error("the refused reset changed the stored hash")
	}
	if got := storedSessionIDs(f, f.ana); !reflect.DeepEqual(got, sessionsBefore) {
		t.Errorf("sessions = %v, want %v unchanged", got, sessionsBefore)
	}
	if f.tokens.tokens[0].usedAt != nil {
		t.Error("the refused reset spent the link")
	}
}

// TestPasswordSignInOffKeepsRootResetLinks is root's break-glass: its link
// still resets the password while password sign-in is off.
func TestPasswordSignInOffKeepsRootResetLinks(t *testing.T) {
	f := newResetFixture(t)
	ctx := context.Background()
	root := seedRoleWithPassword(t, f.users, "Root", "root@example.com", domain.RoleRoot, ownCurrentPassword)
	raw := f.requestToken(t, root.Email)
	f.svc.WithCompanySignIn(companySignInSwitch(t, false))

	if usable, err := f.svc.CheckPasswordResetToken(ctx, raw); err != nil || !usable {
		t.Fatalf("CheckPasswordResetToken(root) = %v, %v; want true", usable, err)
	}
	if err := f.svc.ResetPassword(ctx, raw, resetPassword); err != nil {
		t.Fatalf("ResetPassword(root) = %v, want nil", err)
	}
	if !application.VerifyPassword(f.users.users[root.ID].PasswordHash, resetPassword) {
		t.Error("root's new password was not stored")
	}
}
