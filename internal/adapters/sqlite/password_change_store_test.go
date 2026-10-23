package sqlite

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// Own-password change (issue #414, S2) — the store half. One atomic write
// replaces the user's password hash and deletes every session of that user
// except the one making the request, so a device signed in elsewhere can never
// outlive the password it was signed in with. Written before the port exists —
// compile-error RED.

// seedPasswordSession inserts a session directly (test arrange) so a test owns
// the exact session ids it asserts on.
func seedPasswordSession(t *testing.T, s *Store, id string, userID int64) {
	t.Helper()
	if _, err := s.db.Exec(`INSERT INTO sessions (id, user_id, created_at, expires_at) VALUES (?, ?, ?, ?)`,
		id, userID, "2026-08-06T10:00:00Z", formatTime(time.Now().Add(24*time.Hour))); err != nil {
		t.Fatalf("seed session %s: %v", id, err)
	}
}

// passwordSessionIDs answers the stored session ids of one user, sorted, so a
// test can prove which sessions survived.
func passwordSessionIDs(t *testing.T, s *Store, userID int64) []string {
	t.Helper()
	rows, err := s.db.Query(`SELECT id FROM sessions WHERE user_id = ? ORDER BY id`, userID)
	if err != nil {
		t.Fatalf("read sessions of user %d: %v", userID, err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan session id: %v", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read sessions of user %d: %v", userID, err)
	}
	sort.Strings(ids)
	return ids
}

// passwordHashOf answers the stored hash of one user.
func passwordHashOf(t *testing.T, s *Store, userID int64) string {
	t.Helper()
	var hash string
	if err := s.db.QueryRow(`SELECT password_hash FROM users WHERE id = ?`, userID).Scan(&hash); err != nil {
		t.Fatalf("read password hash of user %d: %v", userID, err)
	}
	return hash
}

// passwordChangeFixture seeds two accounts: the one changing its password with
// three sessions (one of them the request's own) and a second account whose
// session must survive untouched.
type passwordChangeFixture struct {
	store  *Store
	userID int64
	other  int64
}

func newPasswordChangeFixture(t *testing.T) *passwordChangeFixture {
	t.Helper()
	s := newTestDB(t)
	f := &passwordChangeFixture{
		store:  s,
		userID: seedUser(t, s, "Ana", "ana@example.com", true),
		other:  seedUser(t, s, "Bea", "bea@example.com", true),
	}
	for _, seed := range []struct {
		id     string
		userID int64
	}{
		{"sess-current", f.userID},
		{"sess-laptop", f.userID},
		{"sess-phone", f.userID},
		{"sess-bea", f.other},
	} {
		seedPasswordSession(t, s, seed.id, seed.userID)
	}
	return f
}

// TestPasswordChangeStoreReplacesTheHashAndSignsOutTheOtherSessions covers the
// whole success contract: the new hash is stored, the current session is kept,
// the user's other sessions are gone, and another account keeps every session.
func TestPasswordChangeStoreReplacesTheHashAndSignsOutTheOtherSessions(t *testing.T) {
	f := newPasswordChangeFixture(t)
	ctx := context.Background()

	if err := f.store.PasswordChangeStore().ChangePasswordAndSignOutOtherSessions(ctx, f.userID, "new-bcrypt-hash", "sess-current"); err != nil {
		t.Fatalf("ChangePasswordAndSignOutOtherSessions: %v", err)
	}

	if got, want := passwordHashOf(t, f.store, f.userID), "new-bcrypt-hash"; got != want {
		t.Errorf("stored hash = %q, want %q", got, want)
	}
	if got, want := passwordSessionIDs(t, f.store, f.userID), []string{"sess-current"}; !reflect.DeepEqual(got, want) {
		t.Errorf("sessions of the changed account = %v, want %v", got, want)
	}
	if got, want := passwordSessionIDs(t, f.store, f.other), []string{"sess-bea"}; !reflect.DeepEqual(got, want) {
		t.Errorf("sessions of the other account = %v, want %v", got, want)
	}
	if got := passwordHashOf(t, f.store, f.other); got != "bcrypt-hash" {
		t.Errorf("another account's hash = %q, want it untouched", got)
	}
}

// TestPasswordChangeStoreUnknownUserIsNotFoundAndDeletesNothing proves the
// unknown-user refusal cannot delete a session on its way out: the hash write
// is the first statement, so an id no user row matches answers ErrNotFound with
// the session table exactly as it was.
func TestPasswordChangeStoreUnknownUserIsNotFoundAndDeletesNothing(t *testing.T) {
	f := newPasswordChangeFixture(t)
	ctx := context.Background()

	err := f.store.PasswordChangeStore().ChangePasswordAndSignOutOtherSessions(ctx, 4242, "new-bcrypt-hash", "sess-current")
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown user = %v, want ErrNotFound", err)
	}
	if got, want := passwordSessionIDs(t, f.store, f.userID), []string{"sess-current", "sess-laptop", "sess-phone"}; !reflect.DeepEqual(got, want) {
		t.Errorf("sessions after the refused change = %v, want %v", got, want)
	}
	if got := passwordHashOf(t, f.store, f.userID); got != "bcrypt-hash" {
		t.Errorf("stored hash = %q, want it untouched", got)
	}
}

// TestPasswordChangeStoreRollsBackHashAndSessions proves the unit really is
// atomic: with the session delete aborted, the hash write it travelled with is
// rolled back too, so a user is never given a new password while the devices
// signed in with the old one stay signed in.
func TestPasswordChangeStoreRollsBackHashAndSessions(t *testing.T) {
	f := newPasswordChangeFixture(t)
	ctx := context.Background()

	if _, err := f.store.db.ExecContext(ctx, `
		CREATE TRIGGER abort_password_change_signout BEFORE DELETE ON sessions
		BEGIN SELECT RAISE(ABORT, 'injected password change failure'); END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}
	t.Cleanup(func() {
		// context.Background(), NOT t.Context(): t.Context() is cancelled during
		// cleanup, so the drop would fail with "context canceled".
		if _, err := f.store.db.ExecContext(context.Background(), `DROP TRIGGER abort_password_change_signout`); err != nil {
			t.Errorf("drop trigger: %v", err)
		}
	})

	err := f.store.PasswordChangeStore().ChangePasswordAndSignOutOtherSessions(ctx, f.userID, "new-bcrypt-hash", "sess-current")
	if err == nil {
		t.Fatal("the change succeeded with its session delete aborted, want an error")
	}
	if got := passwordHashOf(t, f.store, f.userID); got != "bcrypt-hash" {
		t.Errorf("stored hash after the rollback = %q, want the original", got)
	}
	if got, want := passwordSessionIDs(t, f.store, f.userID), []string{"sess-current", "sess-laptop", "sess-phone"}; !reflect.DeepEqual(got, want) {
		t.Errorf("sessions after the rollback = %v, want %v", got, want)
	}
}
