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

// AuthService implements authentication (user-management spec): login with
// server-side sessions, logout, and the first-user bootstrap gate (D16).
type AuthService struct {
	users    UserStore
	sessions SessionStore
	clock    domain.Clock
}

// NewAuthService wires the auth use cases against the given ports.
func NewAuthService(users UserStore, sessions SessionStore, clock domain.Clock) *AuthService {
	return &AuthService{users: users, sessions: sessions, clock: clock}
}

// Login authenticates email + password against the stored bcrypt hash. Any
// failure (unknown email, wrong password, deactivated user) returns the SAME
// InvalidCredentialsError and creates no session. Success issues a fresh
// opaque session token (32 random bytes, hex) expiring in 24h (D14).
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
