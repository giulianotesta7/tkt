package sqlite

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
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

// TestPasswordChangeStoreRefusesAForeignKeepSession proves the kept session must
// belong to the account being changed. Without that guard a request naming
// ANOTHER user's session deletes every session of the actor, the one in use
// included, so the person changing their password signs themselves out and the
// foreign session survives untouched. The refusal writes nothing at all: the
// hash update that opened the transaction rolls back with the check.
func TestPasswordChangeStoreRefusesAForeignKeepSession(t *testing.T) {
	f := newPasswordChangeFixture(t)
	ctx := context.Background()

	err := f.store.PasswordChangeStore().ChangePasswordAndSignOutOtherSessions(ctx, f.userID, "new-bcrypt-hash", "sess-bea")
	var forbidden *domain.ForbiddenError
	if !errors.As(err, &forbidden) {
		t.Fatalf("a foreign keep session = %v, want a ForbiddenError", err)
	}
	if got := passwordHashOf(t, f.store, f.userID); got != "bcrypt-hash" {
		t.Errorf("stored hash = %q, want it untouched", got)
	}
	if got, want := passwordSessionIDs(t, f.store, f.userID), []string{"sess-current", "sess-laptop", "sess-phone"}; !reflect.DeepEqual(got, want) {
		t.Errorf("sessions of the changed account = %v, want %v", got, want)
	}
	if got, want := passwordSessionIDs(t, f.store, f.other), []string{"sess-bea"}; !reflect.DeepEqual(got, want) {
		t.Errorf("sessions of the other account = %v, want %v", got, want)
	}
}

// TestPasswordChangeStoreRefusesAnUnknownKeepSession proves a keep session no
// row matches is refused too, so a session that vanished between the gate and
// the write can never turn a password change into a full sign-out.
func TestPasswordChangeStoreRefusesAnUnknownKeepSession(t *testing.T) {
	f := newPasswordChangeFixture(t)
	ctx := context.Background()

	err := f.store.PasswordChangeStore().ChangePasswordAndSignOutOtherSessions(ctx, f.userID, "new-bcrypt-hash", "sess-gone")
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("an unknown keep session = %v, want ErrNotFound", err)
	}
	if got := passwordHashOf(t, f.store, f.userID); got != "bcrypt-hash" {
		t.Errorf("stored hash = %q, want it untouched", got)
	}
	if got, want := passwordSessionIDs(t, f.store, f.userID), []string{"sess-current", "sess-laptop", "sess-phone"}; !reflect.DeepEqual(got, want) {
		t.Errorf("sessions of the changed account = %v, want %v", got, want)
	}
}

// passwordChangeUser seeds an account whose stored hash really is the bcrypt
// hash of plain, so the service can verify the current password. The generic
// seedUser fixture stores a placeholder hash on purpose.
func passwordChangeUser(t *testing.T, s *Store, name, email, plain string, active bool) int64 {
	t.Helper()
	hash, err := application.HashPassword(plain)
	if err != nil {
		t.Fatalf("hash the password of %s: %v", email, err)
	}
	id := seedUser(t, s, name, email, active)
	if _, err := s.db.Exec(`UPDATE users SET password_hash = ? WHERE id = ?`, hash, id); err != nil {
		t.Fatalf("store the hash of %s: %v", email, err)
	}
	return id
}

// passwordChangeClock stamps the sessions the service mints. It stays on the
// wall clock because the session store enforces its TTL against it.
func passwordChangeClock() domain.Clock { return fixedTestClock{now: time.Now()} }

type fixedTestClock struct{ now time.Time }

func (c fixedTestClock) Now() time.Time { return c.now }

// passwordChangeService wires the real user use cases over this store, with the
// policy and the own-password write both in place.
func passwordChangeService(s *Store) *application.UserService {
	return application.NewUserService(s.UserStore(), passwordChangeClock()).
		WithPasswordSettings(s.PasswordSettingsStore()).
		WithPasswordChangeStore(s.PasswordChangeStore())
}

// passwordChangeActor reloads one account the way the session middleware hands
// it to a handler: identity first, never a trusted hash.
func passwordChangeActor(t *testing.T, s *Store, id int64) domain.User {
	t.Helper()
	user, err := s.UserStore().GetByID(context.Background(), id)
	if err != nil {
		t.Fatalf("reload account %d: %v", id, err)
	}
	return *user
}

// passwordChangePair seeds two accounts with real hashes: the one changing its
// password with two sessions (the request's own and one more) and a second one
// whose session must survive.
func passwordChangePair(t *testing.T, s *Store) (ana, bea int64) {
	t.Helper()
	ana = passwordChangeUser(t, s, "Ana", "ana@example.com", "current-pass-12", true)
	bea = passwordChangeUser(t, s, "Bea", "bea@example.com", "current-pass-12", true)
	seedPasswordSession(t, s, "sess-current", ana)
	seedPasswordSession(t, s, "sess-laptop", ana)
	seedPasswordSession(t, s, "sess-bea", bea)
	return ana, bea
}

// TestPasswordChangeThroughTheService covers S2 and S4 end to end on the real
// store: a wrong current password and a short new one change nothing, and a
// successful change replaces the hash and signs out only the other sessions of
// that account.
func TestPasswordChangeThroughTheService(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	ana, bea := passwordChangePair(t, s)
	svc := passwordChangeService(s)
	actor := passwordChangeActor(t, s, ana)
	oldHash, beaHash := passwordHashOf(t, s, ana), passwordHashOf(t, s, bea)

	err := svc.ChangeOwnPassword(ctx, actor, "sess-current", "wrong-current-1", "next-pass-12-x")
	var invalid *domain.ValidationError
	if !errors.As(err, &invalid) || invalid.Key != "error.current_password_incorrect" {
		t.Fatalf("wrong current password = %v, want error.current_password_incorrect", err)
	}
	if got := passwordHashOf(t, s, ana); got != oldHash {
		t.Errorf("the hash changed on a refused attempt: %q", got)
	}
	if got, want := passwordSessionIDs(t, s, ana), []string{"sess-current", "sess-laptop"}; !reflect.DeepEqual(got, want) {
		t.Errorf("sessions after the refusal = %v, want %v", got, want)
	}

	// S4: the new password obeys the default minimum of 12 characters.
	if err := svc.ChangeOwnPassword(ctx, actor, "sess-current", "current-pass-12", "eleven-char"); err == nil {
		t.Fatal("an 11-character new password was accepted under the default minimum")
	} else if invalid, ok := err.(*domain.ValidationError); !ok || invalid.Key != "error.password_too_short" {
		t.Errorf("short new password = %v, want error.password_too_short", err)
	}

	if err := svc.ChangeOwnPassword(ctx, actor, "sess-current", "current-pass-12", "next-pass-12-x"); err != nil {
		t.Fatalf("own password change: %v", err)
	}
	hash := passwordHashOf(t, s, ana)
	if !application.VerifyPassword(hash, "next-pass-12-x") || application.VerifyPassword(hash, "current-pass-12") {
		t.Error("the stored hash does not hold exactly the new password")
	}
	if got, want := passwordSessionIDs(t, s, ana), []string{"sess-current"}; !reflect.DeepEqual(got, want) {
		t.Errorf("own sessions = %v, want %v", got, want)
	}
	if got, want := passwordSessionIDs(t, s, bea), []string{"sess-bea"}; !reflect.DeepEqual(got, want) {
		t.Errorf("another account's sessions = %v, want %v", got, want)
	}
	if got := passwordHashOf(t, s, bea); got != beaHash {
		t.Error("another account's hash changed")
	}
}

// TestPasswordChangeRefusedWhileTheSwitchIsOff proves S1 against the real
// settings store: with the switch OFF the use case refuses and writes nothing.
func TestPasswordChangeRefusedWhileTheSwitchIsOff(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	ana, _ := passwordChangePair(t, s)
	if err := s.PasswordSettingsStore().SetPasswordSettings(ctx, application.PasswordSettings{
		ChangeEnabled: false, RecoveryEnabled: true, MinLength: 12,
	}); err != nil {
		t.Fatalf("store the policy: %v", err)
	}
	svc := passwordChangeService(s)
	actor := passwordChangeActor(t, s, ana)
	before, sessions := passwordHashOf(t, s, ana), passwordSessionIDs(t, s, ana)

	err := svc.ChangeOwnPassword(ctx, actor, "sess-current", "current-pass-12", "next-pass-12-x")
	var forbidden *domain.ForbiddenError
	if !errors.As(err, &forbidden) {
		t.Fatalf("switch OFF = %v, want a ForbiddenError", err)
	}
	if got := passwordHashOf(t, s, ana); got != before {
		t.Error("switch OFF wrote the hash")
	}
	if got := passwordSessionIDs(t, s, ana); !reflect.DeepEqual(got, sessions) {
		t.Error("switch OFF deleted sessions")
	}
}

// TestPasswordChangeDefaultsWithoutAPolicyStore proves the documented defaults
// survive a composition root that forgot the policy port: both paths stay ON
// and the minimum stays 12, never relaxed to anything.
func TestPasswordChangeDefaultsWithoutAPolicyStore(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	ana, _ := passwordChangePair(t, s)
	svc := application.NewUserService(s.UserStore(), passwordChangeClock()).
		WithPasswordChangeStore(s.PasswordChangeStore())
	actor := passwordChangeActor(t, s, ana)

	if err := svc.ChangeOwnPassword(ctx, actor, "sess-current", "current-pass-12", "eleven-char"); err == nil {
		t.Fatal("11 characters accepted without a policy store; the default is 12")
	}
	if err := svc.ChangeOwnPassword(ctx, actor, "sess-current", "current-pass-12", "twelve-chars"); err != nil {
		t.Fatalf("12 characters refused without a policy store: %v", err)
	}
}

// TestPasswordChangeHonoursAConfiguredMinimum proves S4's configured minimum is
// the one enforced: 15 characters are refused under a minimum of 16, and a
// password stored before the increase keeps verifying at sign-in.
func TestPasswordChangeHonoursAConfiguredMinimum(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	ana := passwordChangeUser(t, s, "Ana", "ana@example.com", "short-legacy", true)
	seedPasswordSession(t, s, "sess-current", ana)
	if err := s.PasswordSettingsStore().SetPasswordSettings(ctx, application.PasswordSettings{
		ChangeEnabled: true, RecoveryEnabled: true, MinLength: 16,
	}); err != nil {
		t.Fatalf("store the policy: %v", err)
	}
	svc := passwordChangeService(s)
	actor := passwordChangeActor(t, s, ana)

	if err := svc.ChangeOwnPassword(ctx, actor, "sess-current", "short-legacy", "fifteen-chars-1"); err == nil {
		t.Fatal("15 characters accepted under a minimum of 16")
	}
	if err := svc.ChangeOwnPassword(ctx, actor, "sess-current", "short-legacy", "sixteen-chars-12"); err != nil {
		t.Fatalf("16 characters refused under a minimum of 16: %v", err)
	}
	// The stored password before the change keeps its old length but really is
	// gone once a new one is stored.
	if application.VerifyPassword(passwordHashOf(t, s, ana), "short-legacy") {
		t.Error("the previous password still verifies after the change")
	}
}

// TestPasswordChangeRefusalsWriteNothing proves the guard order: a blank session
// id, an inactive account and an actor whose row vanished all change nothing.
func TestPasswordChangeRefusalsWriteNothing(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	ana, bea := passwordChangePair(t, s)
	svc := passwordChangeService(s)
	actor := passwordChangeActor(t, s, ana)
	hash, sessions := passwordHashOf(t, s, ana), passwordSessionIDs(t, s, ana)

	var forbidden *domain.ForbiddenError
	if err := svc.ChangeOwnPassword(ctx, actor, "   ", "current-pass-12", "next-pass-12-x"); !errors.As(err, &forbidden) {
		t.Errorf("blank current session = %v, want a ForbiddenError", err)
	}
	if _, err := s.db.Exec(`UPDATE users SET active = 0 WHERE id = ?`, ana); err != nil {
		t.Fatal(err)
	}
	if err := svc.ChangeOwnPassword(ctx, actor, "sess-current", "current-pass-12", "next-pass-12-x"); !errors.As(err, &forbidden) {
		t.Errorf("inactive account = %v, want a ForbiddenError", err)
	}
	if err := svc.ChangeOwnPassword(ctx, domain.User{ID: 4242, Active: true}, "sess-current", "current-pass-12", "next-pass-12-x"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("vanished actor = %v, want ErrNotFound", err)
	}
	if got := passwordHashOf(t, s, ana); got != hash {
		t.Error("a refusal wrote the hash")
	}
	if got := passwordSessionIDs(t, s, ana); !reflect.DeepEqual(got, sessions) {
		t.Error("a refusal deleted sessions")
	}
	if got, want := passwordSessionIDs(t, s, bea), []string{"sess-bea"}; !reflect.DeepEqual(got, want) {
		t.Error("a refusal touched another account's sessions")
	}
}

// TestPasswordChangeConcurrentChangesAndLogins proves the atomic unit under
// concurrency: own-password changes racing each other and a sign-in never leave
// a torn state and never surface a lock error. A lost race answers the same
// current-password refusal a sequential retry would.
func TestPasswordChangeConcurrentChangesAndLogins(t *testing.T) {
	s, err := openDSN(testDSN(t))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	s.db.SetMaxOpenConns(8)
	t.Cleanup(func() { s.db.Close() })
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	ctx := context.Background()
	ana := passwordChangeUser(t, s, "Ana", "ana@example.com", "current-pass-12", true)
	for _, id := range []string{"sess-current", "sess-laptop", "sess-phone"} {
		seedPasswordSession(t, s, id, ana)
	}
	svc := passwordChangeService(s)
	actor := passwordChangeActor(t, s, ana)

	var mu sync.Mutex
	var failures []string
	record := func(err error) {
		if err == nil {
			return
		}
		var invalid *domain.ValidationError
		if errors.As(err, &invalid) && invalid.Key == "error.current_password_incorrect" {
			return // another change won the race first: expected
		}
		mu.Lock()
		failures = append(failures, err.Error())
		mu.Unlock()
	}
	var wg sync.WaitGroup
	for _, next := range []string{"concurrent-a-12", "concurrent-b-12", "concurrent-c-12"} {
		wg.Add(1)
		go func(next string) {
			defer wg.Done()
			record(svc.ChangeOwnPassword(ctx, actor, "sess-current", "current-pass-12", next))
		}(next)
	}
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := "sess-login-" + strings.Repeat("x", i+1)
			record(s.SessionStore().Create(ctx, &domain.Session{ID: id, UserID: ana, ExpiresAt: time.Now().Add(time.Hour)}))
		}(i)
	}
	wg.Wait()
	if len(failures) > 0 {
		t.Fatalf("concurrent errors: %v", failures)
	}

	hash := passwordHashOf(t, s, ana)
	if application.VerifyPassword(hash, "current-pass-12") {
		t.Error("the previous password still verifies after the concurrent changes")
	}
	matches := 0
	for _, next := range []string{"concurrent-a-12", "concurrent-b-12", "concurrent-c-12"} {
		if application.VerifyPassword(hash, next) {
			matches++
		}
	}
	if matches != 1 {
		t.Errorf("the stored hash verifies %d of the submitted passwords, want exactly 1", matches)
	}
	sessions := passwordSessionIDs(t, s, ana)
	if !hasSessionID(sessions, "sess-current") {
		t.Errorf("the session in use was signed out: %v", sessions)
	}
	for _, gone := range []string{"sess-laptop", "sess-phone"} {
		if hasSessionID(sessions, gone) {
			t.Errorf("session %s survived the change: %v", gone, sessions)
		}
	}
}

// hasSessionID reports whether id is one of the stored session ids.
func hasSessionID(ids []string, id string) bool {
	for _, stored := range ids {
		if stored == id {
			return true
		}
	}
	return false
}

// TestPasswordChangeVoidsPendingResetTokens proves the other half of the
// transaction (issue #414, S2): a password change voids the account's
// pending recovery links, because a link mailed before the change must never
// set a password after it. Spent links stay as history and another account's
// pending link is untouched.
func TestPasswordChangeVoidsPendingResetTokens(t *testing.T) {
	f := newPasswordChangeFixture(t)
	ctx := context.Background()
	expires := testClock.Add(time.Hour)
	seedResetToken(t, f.store, f.userID, "hash-pending", testClock.Add(-time.Minute), expires, nil)
	seedResetToken(t, f.store, f.userID, "hash-spent", testClock.Add(-time.Hour), expires, ptr(testClock.Add(-30*time.Minute)))
	seedResetToken(t, f.store, f.other, "hash-other", testClock.Add(-time.Minute), expires, nil)

	if err := f.store.PasswordChangeStore().ChangePasswordAndSignOutOtherSessions(ctx, f.userID, "new-bcrypt-hash", "sess-current"); err != nil {
		t.Fatalf("ChangePasswordAndSignOutOtherSessions: %v", err)
	}

	if got, want := resetTokenHashes(t, f.store, f.userID), []string{"hash-spent"}; !reflect.DeepEqual(got, want) {
		t.Errorf("stored links of the account = %v, want %v (the pending one voided, the spent one kept)", got, want)
	}
	if got, want := resetTokenHashes(t, f.store, f.other), []string{"hash-other"}; !reflect.DeepEqual(got, want) {
		t.Errorf("the other account's links = %v, want %v", got, want)
	}
}
