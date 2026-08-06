package application_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

func newAuthService() (*application.AuthService, *fakeUserStore, *fakeSessionStore, *fakeClock) {
	clock := fixedClock()
	users := newFakeUserStore()
	sessions := newFakeSessionStore()
	return application.NewAuthService(users, sessions, clock), users, sessions, clock
}

// createUserWithPassword seeds a user whose password hashes to the bcrypt
// hash of plain.
func createUserWithPassword(users *fakeUserStore, name, email, plain string) domain.User {
	hash, err := application.HashPassword(plain)
	if err != nil {
		panic(err)
	}
	u := users.seed(name, email, true)
	u.PasswordHash = hash
	users.Update(context.Background(), &u)
	return u
}

func TestLoginSuccessCreatesFreshSession(t *testing.T) {
	svc, users, sessions, clock := newAuthService()
	createUserWithPassword(users, "Ana", "ana@example.com", "s3cret-pass")

	session, err := svc.Login(context.Background(), "ana@example.com", "s3cret-pass")
	if err != nil {
		t.Fatalf("Login: unexpected error: %v", err)
	}
	if len(session.ID) != 64 {
		t.Fatalf("Login: session token must be 32 random bytes hex-encoded (64 chars), got %q (len %d)", session.ID, len(session.ID))
	}
	if session.UserID == 0 {
		t.Fatal("Login: session must carry the user id")
	}
	wantExpiry := clock.now.Add(24 * time.Hour)
	if !session.ExpiresAt.Equal(wantExpiry) {
		t.Fatalf("Login: session must expire 24h after login, got %v want %v", session.ExpiresAt, wantExpiry)
	}

	stored, err := sessions.GetByID(context.Background(), session.ID)
	if err != nil || stored.UserID != session.UserID {
		t.Fatalf("Login: session must be stored server-side, got %v / %v", stored, err)
	}
}

func TestLoginFailuresShareOneGenericErrorAndNoSession(t *testing.T) {
	svc, users, sessions, _ := newAuthService()
	createUserWithPassword(users, "Ana", "ana@example.com", "s3cret-pass")
	// Deactivated user with otherwise valid credentials.
	inactive := createUserWithPassword(users, "Beto", "beto@example.com", "s3cret-pass")
	inactive.Active = false
	users.Update(context.Background(), &inactive)

	cases := []struct {
		name     string
		email    string
		password string
	}{
		{"wrong password", "ana@example.com", "wrong-pass"},
		{"unknown email", "nobody@example.com", "whatever"},
		{"deactivated user", "beto@example.com", "s3cret-pass"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.Login(context.Background(), tc.email, tc.password)
			var cerr *application.InvalidCredentialsError
			if !errors.As(err, &cerr) {
				t.Fatalf("Login: must fail with InvalidCredentialsError, got %v", err)
			}
			if len(sessions.sessions) != 0 {
				t.Fatal("Login: failed login must not create a session")
			}
		})
	}

	// All three failures expose the SAME generic error (no user enumeration).
	_, e1 := svc.Login(context.Background(), "ana@example.com", "wrong-pass")
	_, e2 := svc.Login(context.Background(), "nobody@example.com", "x")
	_, e3 := svc.Login(context.Background(), "beto@example.com", "s3cret-pass")
	if e1.Error() != e2.Error() || e2.Error() != e3.Error() {
		t.Fatalf("Login: all failures must share the same message, got %q / %q / %q", e1, e2, e3)
	}
}

func TestLogoutDestroysSession(t *testing.T) {
	svc, users, sessions, _ := newAuthService()
	createUserWithPassword(users, "Ana", "ana@example.com", "s3cret-pass")
	session, err := svc.Login(context.Background(), "ana@example.com", "s3cret-pass")
	if err != nil {
		t.Fatalf("Login: unexpected error: %v", err)
	}

	if err := svc.Logout(context.Background(), session.ID); err != nil {
		t.Fatalf("Logout: unexpected error: %v", err)
	}
	if _, err := sessions.GetByID(context.Background(), session.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Logout: session must be destroyed, got %v", err)
	}
}

func TestUserCountBootstrapGate(t *testing.T) {
	svc, users, _, _ := newAuthService()

	n, err := svc.UserCount(context.Background())
	if err != nil {
		t.Fatalf("UserCount: unexpected error: %v", err)
	}
	if n != 0 {
		t.Fatalf("UserCount: empty store must report 0, got %d", n)
	}

	users.seed("Ana", "ana@example.com", true)
	n, err = svc.UserCount(context.Background())
	if err != nil {
		t.Fatalf("UserCount: unexpected error: %v", err)
	}
	if n != 1 {
		t.Fatalf("UserCount: after one user must report 1, got %d", n)
	}
}
