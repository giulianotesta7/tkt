package sqlite

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// Task 4.4: session store — create, lookup with expiry enforcement + lazy
// purge, logout delete. Expiry is enforced against wall-clock time: the
// port has no clock parameter, and the DB is the single source of truth for
// the server-side TTL (D14).

func newSession(expiresAt time.Time) *domain.Session {
	return &domain.Session{ID: "tok-123", UserID: 1, ExpiresAt: expiresAt}
}

func TestSessionCreateAndGetByID(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	userID := seedUser(t, s, "Ana", "ana@example.com", true)

	sess := newSession(time.Now().Add(24 * time.Hour).Truncate(time.Second))
	sess.UserID = userID
	if err := s.SessionStore().Create(ctx, sess); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := s.SessionStore().GetByID(ctx, sess.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ID != sess.ID || got.UserID != userID || !got.ExpiresAt.Equal(sess.ExpiresAt) {
		t.Errorf("session = %+v", got)
	}
}

func TestSessionCreateStampsCreatedAt(t *testing.T) {
	// The domain Session carries no CreatedAt (slice-2 deviation #4); the
	// store stamps the NOT NULL column itself (store-side time, D7 scope).
	s := newTestDB(t)
	userID := seedUser(t, s, "Ana", "ana@example.com", true)
	sess := newSession(time.Now().Add(24 * time.Hour))
	sess.UserID = userID
	if err := s.SessionStore().Create(context.Background(), sess); err != nil {
		t.Fatal(err)
	}
	var createdAt string
	if err := s.db.QueryRow(`SELECT created_at FROM sessions WHERE id = ?`, sess.ID).Scan(&createdAt); err != nil {
		t.Fatalf("read created_at: %v", err)
	}
	if createdAt == "" {
		t.Error("created_at not stamped")
	}
}

func TestSessionGetByIDMissing(t *testing.T) {
	s := newTestDB(t)
	_, err := s.SessionStore().GetByID(context.Background(), "no-such-token")
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestSessionGetByIDExpiredIsNotFoundAndPurged(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	userID := seedUser(t, s, "Ana", "ana@example.com", true)

	sess := newSession(time.Now().Add(-time.Hour)) // already expired
	sess.UserID = userID
	if err := s.SessionStore().Create(ctx, sess); err != nil {
		t.Fatal(err)
	}

	if _, err := s.SessionStore().GetByID(ctx, sess.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expired lookup = %v, want ErrNotFound", err)
	}
	// Lazy purge: the expired row is gone after the lookup.
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE id = ?`, sess.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Error("expired session row not purged on lookup")
	}
}

func TestSessionDeleteRemovesRow(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	userID := seedUser(t, s, "Ana", "ana@example.com", true)

	sess := newSession(time.Now().Add(24 * time.Hour))
	sess.UserID = userID
	if err := s.SessionStore().Create(ctx, sess); err != nil {
		t.Fatal(err)
	}
	if err := s.SessionStore().Delete(ctx, sess.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.SessionStore().GetByID(ctx, sess.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("get after logout = %v, want ErrNotFound", err)
	}
}

func TestSessionDeleteMissingIsNoError(t *testing.T) {
	// Logout of an already-gone session must not fail (idempotent logout).
	s := newTestDB(t)
	if err := s.SessionStore().Delete(context.Background(), "no-such-token"); err != nil {
		t.Errorf("delete missing = %v, want nil", err)
	}
}

func TestSessionCreateRejectsUnknownUser(t *testing.T) {
	s := newTestDB(t)
	sess := &domain.Session{ID: "orphan", UserID: 999, ExpiresAt: time.Now().Add(time.Hour)}
	if err := s.SessionStore().Create(context.Background(), sess); err == nil {
		t.Fatal("create session for unknown user succeeded, want foreign-key error")
	}
}

func TestSessionGetByIDRejectsMalformedExpiry(t *testing.T) {
	s := newTestDB(t)
	userID := seedUser(t, s, "Ana", "ana@example.com", true)
	if _, err := s.db.Exec(`INSERT INTO sessions (id, user_id, created_at, expires_at) VALUES (?, ?, ?, ?)`,
		"bad-expiry", userID, "2026-08-06T10:00:00Z", "not-a-time"); err != nil {
		t.Fatalf("seed malformed session: %v", err)
	}
	if _, err := s.SessionStore().GetByID(context.Background(), "bad-expiry"); err == nil || !strings.Contains(err.Error(), `parse session expires_at "not-a-time"`) {
		t.Fatalf("malformed expiry error = %v", err)
	}
}

func TestSessionStorePropagatesDatabaseErrors(t *testing.T) {
	s := newTestDB(t)
	if err := s.db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	tests := []struct {
		name string
		call func() error
	}{
		{name: "create", call: func() error {
			return s.SessionStore().Create(context.Background(), newSession(time.Now().Add(time.Hour)))
		}},
		{name: "get", call: func() error { _, err := s.SessionStore().GetByID(context.Background(), "token"); return err }},
		{name: "delete", call: func() error { return s.SessionStore().Delete(context.Background(), "token") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.call(); err == nil {
				t.Fatal("operation on closed database succeeded")
			}
		})
	}
}
