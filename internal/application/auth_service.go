package application

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// SessionTTL is the server-side session lifetime (D14), fixed at 24h.
const SessionTTL = 24 * time.Hour

// ErrMsgInvalidCredentials is the single generic login error message (D5):
// wrong password, unknown email, and deactivated users all surface the same
// text — no user enumeration.
const ErrMsgInvalidCredentials = "invalid credentials"

// InvalidCredentialsError is the application-level login failure (401,
// single generic message; design D5).
type InvalidCredentialsError struct{}

func (e *InvalidCredentialsError) Error() string { return ErrMsgInvalidCredentials }

// ErrMsgPasswordSignInOff is the refusal a VERIFIED password of a non-root
// account answers once an administrator turns password sign-in off (issue
// #419, S4). It is deliberately its own sentence rather than the generic one:
// the person proved the password, so telling them which way to sign in leaks
// nothing, while a wrong password keeps the generic error.
const ErrMsgPasswordSignInOff = "Password sign-in is turned off. Sign in with your company account."

// PasswordSignInOffError reports that password sign-in is turned off for this
// account. It is only ever returned after the password was verified, so it
// cannot be used to learn whether an account exists.
type PasswordSignInOffError struct{}

func (e *PasswordSignInOffError) Error() string { return ErrMsgPasswordSignInOff }

// companySignInPasswordEnabled reads the instance switch that offers password
// sign-in (issue #419, S4). A composition root that wired nothing answers the
// shipped default — ON — so a missing port never removes a sign-in path. A
// store failure is returned instead of defaulted away: the switch decides
// whether a password may open a session, so it is read like a policy and
// fails closed.
func companySignInPasswordEnabled(ctx context.Context, store CompanySignInSettingsStore) (bool, error) {
	if store == nil {
		return true, nil
	}
	settings, err := store.GetCompanySignIn(ctx)
	if err != nil {
		return false, err
	}
	return settings.PasswordSignInEnabled, nil
}

// passwordSignInAllowed answers whether this account may still sign in with a
// password (issue #419, S4/S5): root always may — it is the break-glass for a
// failing provider, so its answer is decided without reading the switch — and
// every other role follows the instance switch.
func passwordSignInAllowed(ctx context.Context, store CompanySignInSettingsStore, role domain.Role) (bool, error) {
	if role == domain.RoleRoot {
		return true, nil
	}
	return companySignInPasswordEnabled(ctx, store)
}

// AuthService implements authentication (user-management spec): login with
// server-side sessions, logout, and the first-user bootstrap gate (D16).
type AuthService struct {
	users    UserStore
	sessions SessionStore
	clock    domain.Clock
	// companySignIn, when wired, carries the instance switch that keeps
	// password sign-in to root alone (issue #419, S4).
	companySignIn CompanySignInSettingsStore
}

// NewAuthService wires the auth use cases against the given ports.
func NewAuthService(users UserStore, sessions SessionStore, clock domain.Clock) *AuthService {
	return &AuthService{users: users, sessions: sessions, clock: clock}
}

// WithCompanySignIn wires the company sign-in configuration Login reads
// (issue #419, S4). Without it password sign-in stays on.
func (s *AuthService) WithCompanySignIn(store CompanySignInSettingsStore) *AuthService {
	s.companySignIn = store
	return s
}

// Login authenticates email + password against the stored bcrypt hash. Any
// failure (unknown email, wrong password, deactivated user) returns the SAME
// InvalidCredentialsError and creates no session. Success issues a fresh
// opaque session token (32 random bytes, hex) expiring in 24h (D14).
//
// The password is verified BEFORE the company sign-in switch is read (issue
// #419, S4): a wrong password keeps the one generic error whatever the switch
// says, so the dedicated refusal that follows can never enumerate accounts.
// A verified non-root account is then refused with PasswordSignInOffError
// while an administrator keeps password sign-in off, and no session is
// created; root signs in normally, as the break-glass for a failing provider.
func (s *AuthService) Login(ctx context.Context, email, password string) (*domain.Session, error) {
	user, err := s.users.GetByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, &InvalidCredentialsError{}
		}
		return nil, err
	}
	if !user.Active || !VerifyPassword(user.PasswordHash, password) {
		return nil, &InvalidCredentialsError{}
	}
	allowed, err := passwordSignInAllowed(ctx, s.companySignIn, user.Role)
	if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, &PasswordSignInOffError{}
	}

	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		return nil, err
	}
	session := &domain.Session{
		ID:        hex.EncodeToString(token),
		UserID:    user.ID,
		ExpiresAt: s.clock.Now().Add(SessionTTL),
	}
	if err := s.sessions.Create(ctx, session); err != nil {
		return nil, err
	}
	return session, nil
}

// Logout destroys the server-side session (logout spec).
func (s *AuthService) Logout(ctx context.Context, sessionID string) error {
	return s.sessions.Delete(ctx, sessionID)
}

// UserCount reports the number of users for the first-user bootstrap gate
// (D16): the /setup flow exists only while this is 0.
func (s *AuthService) UserCount(ctx context.Context) (int, error) {
	return s.users.Count(ctx)
}
