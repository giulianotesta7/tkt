package application_test

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// Own-password change from Preferences (issue #414, S2). The signed-in user
// proves the current password, the stored hash is replaced, and every OTHER
// session of that account is signed out while the session making the request
// stays in. Every refusal leaves the hash and the sessions exactly as they
// were, so a refused attempt can never half-apply. Written before the use case
// exists — compile-error RED.

// ownCurrentPassword and ownNextPassword are both above the default minimum
// (S4), so a refusal in these tests can only come from the rule under test.
const (
	ownCurrentPassword = "current-pass-12"
	ownNextPassword    = "next-pass-12-x"
)

// seedPasswordUser inserts an active or inactive account with a REAL bcrypt
// hash, because the current-password check verifies it (a placeholder hash
// would never match and every test would read as "wrong password").
func seedPasswordUser(t *testing.T, users *fakeUserStore, name, email string, active bool, password string) domain.User {
	t.Helper()
	u := users.seed(name, email, active)
	hash, err := application.HashPassword(password)
	if err != nil {
		t.Fatalf("hash seed password for %s: %v", email, err)
	}
	u.PasswordHash = hash
	if err := users.Update(context.Background(), &u); err != nil {
		t.Fatalf("store seed password for %s: %v", email, err)
	}
	return u
}

// ownPasswordFixture wires the signed-in user's world: one active account with
// three sessions — "sess-current" is the request's own, two belong to the same
// user and must be signed out — and a second account whose session must never
// be touched.
type ownPasswordFixture struct {
	svc       *application.UserService
	users     *fakeUserStore
	sessions  *fakeSessionStore
	policy    *fakePasswordSettingsStore
	changes   *fakePasswordChangeStore
	clock     *fakeClock
	actor     domain.User
	otherUser domain.User
}

func newOwnPasswordFixture(t *testing.T) *ownPasswordFixture {
	t.Helper()
	clock := fixedClock()
	users := newFakeUserStore()
	sessions := newFakeSessionStore(clock)
	policy := newFakePasswordSettingsStore()
	f := &ownPasswordFixture{
		users:    users,
		sessions: sessions,
		policy:   policy,
		clock:    clock,
		changes:  newFakePasswordChangeStore(users, sessions),
	}
	f.svc = application.NewUserService(users, clock).
		WithPasswordSettings(policy).
		WithPasswordChangeStore(f.changes)
	f.actor = seedPasswordUser(t, users, "Ana", "ana@example.com", true, ownCurrentPassword)
	f.otherUser = seedPasswordUser(t, users, "Bea", "bea@example.com", true, ownCurrentPassword)
	for _, seed := range []struct {
		id     string
		userID int64
	}{
		{"sess-current", f.actor.ID},
		{"sess-laptop", f.actor.ID},
		{"sess-phone", f.actor.ID},
		{"sess-bea", f.otherUser.ID},
	} {
		session := &domain.Session{ID: seed.id, UserID: seed.userID, ExpiresAt: clock.Now().Add(time.Hour)}
		if err := sessions.Create(context.Background(), session); err != nil {
			t.Fatalf("seed session %s: %v", seed.id, err)
		}
	}
	return f
}

// ownPasswordState is every fact a refused change must leave untouched: the
// stored hash and the ids of that user's sessions, sorted.
type ownPasswordState struct {
	hash     string
	sessions []string
}

func (f *ownPasswordFixture) state(t *testing.T, user domain.User) ownPasswordState {
	t.Helper()
	stored, err := f.users.GetByID(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("read the stored user %d: %v", user.ID, err)
	}
	ids := make([]string, 0, len(f.sessions.sessions))
	for id, session := range f.sessions.sessions {
		if session.UserID == user.ID {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ownPasswordState{hash: stored.PasswordHash, sessions: ids}
}

// assertOwnPasswordUnchanged proves a refusal changed nothing at all.
func assertOwnPasswordUnchanged(t *testing.T, f *ownPasswordFixture, want ownPasswordState) {
	t.Helper()
	if got := f.state(t, f.actor); !reflect.DeepEqual(got, want) {
		t.Errorf("the refused change modified the account: hash %q -> %q, sessions %v -> %v",
			want.hash, got.hash, want.sessions, got.sessions)
	}
}

// racingPasswordChangeStore replaces the stored hash immediately before the
// atomic write runs, standing in for a concurrent change that lands between this
// request's current-password verification and its store call. Everything else it
// delegates to the in-memory write double.
type racingPasswordChangeStore struct {
	inner *fakePasswordChangeStore
	hash  string
}

func (r *racingPasswordChangeStore) ChangePasswordAndSignOutOtherSessions(ctx context.Context, userID int64, expectedPasswordHash, passwordHash, keepSessionID string) error {
	if stored, ok := r.inner.users.users[userID]; ok {
		stored.PasswordHash = r.hash
	}
	return r.inner.ChangePasswordAndSignOutOtherSessions(ctx, userID, expectedPasswordHash, passwordHash, keepSessionID)
}

// TestChangeOwnPasswordRefusesWhenTheStoredHashMovedAfterVerification proves the
// use case surfaces the write port's compare-and-set refusal: a submission whose
// current password verified, but whose account was changed by another request
// before the write, answers the SAME current-password field refusal a wrong
// password gets, and the password the other change stored — with every session —
// survives untouched. Without it the losing submission silently replaced the
// winner's password and signed the winner out of the account.
func TestChangeOwnPasswordRefusesWhenTheStoredHashMovedAfterVerification(t *testing.T) {
	f := newOwnPasswordFixture(t)
	winnerHash, err := application.HashPassword("winner-pass-12")
	if err != nil {
		t.Fatalf("hash the winning password: %v", err)
	}
	sessionsBefore := f.state(t, f.actor).sessions
	f.svc.WithPasswordChangeStore(&racingPasswordChangeStore{inner: f.changes, hash: winnerHash})

	err = f.svc.ChangeOwnPassword(context.Background(), f.actor, "sess-current", ownCurrentPassword, ownNextPassword)
	var verr *domain.ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("ChangeOwnPassword after a concurrent change = %v, want a ValidationError", err)
	}
	if verr.Field != "current_password" || verr.Key != "error.current_password_incorrect" {
		t.Errorf("refusal field/key = %q/%q, want current_password/error.current_password_incorrect", verr.Field, verr.Key)
	}
	stored := f.state(t, f.actor)
	if stored.hash != winnerHash {
		t.Error("the losing submission replaced the password the winning change stored")
	}
	if !reflect.DeepEqual(stored.sessions, sessionsBefore) {
		t.Errorf("sessions after the refused change = %v, want %v", stored.sessions, sessionsBefore)
	}
	if f.changes.calls != 1 {
		t.Errorf("the atomic store write ran %d times, want 1", f.changes.calls)
	}
}

// TestChangeOwnPasswordRefusesAForeignCurrentSession covers the store guard at
// the use case: a request naming ANOTHER account's session changes nothing for
// either account. The write path refuses it, and the service must surface that
// refusal instead of reporting a change that never happened.
func TestChangeOwnPasswordRefusesAForeignCurrentSession(t *testing.T) {
	f := newOwnPasswordFixture(t)
	before := f.state(t, f.actor)
	otherBefore := f.state(t, f.otherUser)

	err := f.svc.ChangeOwnPassword(context.Background(), f.actor, "sess-bea", ownCurrentPassword, ownNextPassword)
	var forbidden *domain.ForbiddenError
	if !errors.As(err, &forbidden) {
		t.Fatalf("ChangeOwnPassword with another account's session = %v, want a ForbiddenError", err)
	}
	assertOwnPasswordUnchanged(t, f, before)
	if got := f.state(t, f.otherUser); !reflect.DeepEqual(got, otherBefore) {
		t.Errorf("another account changed: hash %q -> %q, sessions %v -> %v",
			otherBefore.hash, got.hash, otherBefore.sessions, got.sessions)
	}
}

// TestChangeOwnPasswordRefusesWhenTheInstanceSwitchIsOff covers S1 for this
// path: with the change switch OFF the request is refused and nothing is
// written — not the hash, not one session.
func TestChangeOwnPasswordRefusesWhenTheInstanceSwitchIsOff(t *testing.T) {
	f := newOwnPasswordFixture(t)
	f.policy.settings = application.PasswordSettings{
		ChangeEnabled:   false,
		RecoveryEnabled: true,
		MinLength:       application.DefaultPasswordMinLength,
	}
	f.policy.stored = true
	before := f.state(t, f.actor)

	err := f.svc.ChangeOwnPassword(context.Background(), f.actor, "sess-current", ownCurrentPassword, ownNextPassword)
	var forbidden *domain.ForbiddenError
	if !errors.As(err, &forbidden) {
		t.Fatalf("ChangeOwnPassword with the switch OFF = %v, want a ForbiddenError", err)
	}
	if f.changes.calls != 0 {
		t.Errorf("the store write ran %d times with the switch OFF, want 0", f.changes.calls)
	}
	assertOwnPasswordUnchanged(t, f, before)
}

// TestChangeOwnPasswordDefaultsWithoutAPolicyStore proves the documented
// defaults apply when the composition root never wires the policy port: the
// path stays ON and the minimum is 12, so eleven characters are refused and
// twelve are accepted. A missing port must never read as "switched off".
func TestChangeOwnPasswordDefaultsWithoutAPolicyStore(t *testing.T) {
	f := newOwnPasswordFixture(t)
	f.svc.WithPasswordSettings(nil)
	ctx := context.Background()

	before := f.state(t, f.actor)
	err := f.svc.ChangeOwnPassword(ctx, f.actor, "sess-current", ownCurrentPassword, "eleven-char")
	shortPasswordValidation(t, err, application.DefaultPasswordMinLength)
	assertOwnPasswordUnchanged(t, f, before)

	if err := f.svc.ChangeOwnPassword(ctx, f.actor, "sess-current", ownCurrentPassword, ownNextPassword); err != nil {
		t.Fatalf("ChangeOwnPassword with a twelve-character password and no policy port: %v, want accepted", err)
	}
}

// TestChangeOwnPasswordRefusesAWrongCurrentPassword proves S2's "pide la
// actual": a wrong current password is a field-level refusal naming the field,
// and the hash and every session survive untouched.
func TestChangeOwnPasswordRefusesAWrongCurrentPassword(t *testing.T) {
	f := newOwnPasswordFixture(t)
	before := f.state(t, f.actor)

	err := f.svc.ChangeOwnPassword(context.Background(), f.actor, "sess-current", "not-the-current-1", ownNextPassword)
	var verr *domain.ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("ChangeOwnPassword with a wrong current password = %v, want a ValidationError", err)
	}
	if verr.Field != "current_password" {
		t.Errorf("field = %q, want %q", verr.Field, "current_password")
	}
	if verr.Key != "error.current_password_incorrect" {
		t.Errorf("key = %q, want %q", verr.Key, "error.current_password_incorrect")
	}
	if verr.Message != "the current password is incorrect" {
		t.Errorf("message = %q, want the English catalogue wording", verr.Message)
	}
	if f.changes.calls != 0 {
		t.Errorf("the store write ran %d times for a wrong current password, want 0", f.changes.calls)
	}
	assertOwnPasswordUnchanged(t, f, before)
}

// TestChangeOwnPasswordRefusesABlankOrShortNewPassword covers S4 on this path:
// the new secret obeys the same NEW-password rule as every other write, under
// the CONFIGURED minimum, and a refusal stores nothing.
func TestChangeOwnPasswordRefusesABlankOrShortNewPassword(t *testing.T) {
	tests := []struct {
		name       string
		newPass    string
		minimum    int
		wantKey    string
		wantArgSet bool
	}{
		{name: "blank", newPass: "   ", minimum: application.DefaultPasswordMinLength, wantKey: "error.password_required"},
		{name: "below the default", newPass: "eleven-char", minimum: application.DefaultPasswordMinLength, wantKey: "error.password_too_short", wantArgSet: true},
		{name: "below the configured minimum", newPass: "fifteen-chars-x", minimum: 20, wantKey: "error.password_too_short", wantArgSet: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newOwnPasswordFixture(t)
			f.policy.settings = application.PasswordSettings{
				ChangeEnabled:   true,
				RecoveryEnabled: true,
				MinLength:       tc.minimum,
			}
			f.policy.stored = true
			before := f.state(t, f.actor)

			err := f.svc.ChangeOwnPassword(context.Background(), f.actor, "sess-current", ownCurrentPassword, tc.newPass)
			var verr *domain.ValidationError
			if !errors.As(err, &verr) {
				t.Fatalf("ChangeOwnPassword with a refused new password = %v, want a ValidationError", err)
			}
			if verr.Field != "password" {
				t.Errorf("field = %q, want %q", verr.Field, "password")
			}
			if verr.Key != tc.wantKey {
				t.Errorf("key = %q, want %q", verr.Key, tc.wantKey)
			}
			if tc.wantArgSet && (len(verr.Args) != 1 || verr.Args[0] != tc.minimum) {
				t.Errorf("args = %v, want [%d] (the configured minimum)", verr.Args, tc.minimum)
			}
			assertOwnPasswordUnchanged(t, f, before)
		})
	}
}

// TestChangeOwnPasswordReplacesTheHashAndSignsOutTheOtherSessions covers the
// success contract of S2: the new hash replaces the old one and only verifies
// the new secret, the session making the request stays signed in, the user's
// other sessions are gone, and no other account loses a session.
func TestChangeOwnPasswordReplacesTheHashAndSignsOutTheOtherSessions(t *testing.T) {
	f := newOwnPasswordFixture(t)
	before := f.state(t, f.actor)
	otherBefore := f.state(t, f.otherUser)

	if err := f.svc.ChangeOwnPassword(context.Background(), f.actor, "sess-current", ownCurrentPassword, ownNextPassword); err != nil {
		t.Fatalf("ChangeOwnPassword: %v", err)
	}

	stored := f.state(t, f.actor)
	if stored.hash == before.hash {
		t.Fatalf("the stored hash is unchanged (%q), want the new password's hash", stored.hash)
	}
	if !application.VerifyPassword(stored.hash, ownNextPassword) {
		t.Error("the stored hash does not verify the new password")
	}
	if application.VerifyPassword(stored.hash, ownCurrentPassword) {
		t.Error("the stored hash still verifies the OLD password")
	}
	if want := []string{"sess-current"}; !reflect.DeepEqual(stored.sessions, want) {
		t.Errorf("sessions after the change = %v, want %v (the current one kept, the others signed out)", stored.sessions, want)
	}
	if otherAfter := f.state(t, f.otherUser); !reflect.DeepEqual(otherAfter, otherBefore) {
		t.Errorf("another account changed: hash %q -> %q, sessions %v -> %v",
			otherBefore.hash, otherAfter.hash, otherBefore.sessions, otherAfter.sessions)
	}
	if f.changes.calls != 1 {
		t.Errorf("the atomic store write ran %d times, want 1", f.changes.calls)
	}
}

// TestChangeOwnPasswordRefusesAnInactiveOrUnknownAccount proves the actor is
// RELOADED from the store instead of trusted: an account deactivated after
// signing in, and an actor id the store does not know, are both refused with
// nothing written.
func TestChangeOwnPasswordRefusesAnInactiveOrUnknownAccount(t *testing.T) {
	ctx := context.Background()

	t.Run("inactive", func(t *testing.T) {
		f := newOwnPasswordFixture(t)
		deactivated, err := f.users.GetByID(ctx, f.actor.ID)
		if err != nil {
			t.Fatal(err)
		}
		deactivated.Active = false
		if err := f.users.Update(ctx, deactivated); err != nil {
			t.Fatal(err)
		}
		before := f.state(t, f.actor)

		err = f.svc.ChangeOwnPassword(ctx, f.actor, "sess-current", ownCurrentPassword, ownNextPassword)
		var forbidden *domain.ForbiddenError
		if !errors.As(err, &forbidden) {
			t.Fatalf("ChangeOwnPassword for an inactive account = %v, want a ForbiddenError", err)
		}
		if f.changes.calls != 0 {
			t.Errorf("the store write ran %d times for an inactive account, want 0", f.changes.calls)
		}
		assertOwnPasswordUnchanged(t, f, before)
	})

	t.Run("unknown", func(t *testing.T) {
		f := newOwnPasswordFixture(t)
		ghost := domain.User{ID: 4242, Role: domain.RoleUser, Active: true}

		err := f.svc.ChangeOwnPassword(ctx, ghost, "sess-current", ownCurrentPassword, ownNextPassword)
		if !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("ChangeOwnPassword for an unknown account = %v, want ErrNotFound", err)
		}
		if f.changes.calls != 0 {
			t.Errorf("the store write ran %d times for an unknown account, want 0", f.changes.calls)
		}
	})
}

// TestChangeOwnPasswordRefusesWhenThePolicyCannotBeRead proves the fail-closed
// direction every T1 password path already takes: an unreadable policy refuses
// the change instead of assuming the switch is ON.
func TestChangeOwnPasswordRefusesWhenThePolicyCannotBeRead(t *testing.T) {
	f := newOwnPasswordFixture(t)
	boom := errors.New("settings store unavailable")
	f.policy.getErr = boom
	before := f.state(t, f.actor)

	err := f.svc.ChangeOwnPassword(context.Background(), f.actor, "sess-current", ownCurrentPassword, ownNextPassword)
	if !errors.Is(err, boom) {
		t.Fatalf("ChangeOwnPassword with an unreadable policy = %v, want the store error", err)
	}
	if f.changes.calls != 0 {
		t.Errorf("the store write ran %d times with an unreadable policy, want 0", f.changes.calls)
	}
	assertOwnPasswordUnchanged(t, f, before)
}

// TestChangeOwnPasswordKeepsEverySessionWithoutTheCurrentOne proves the request
// must name the session it is made from: without it the store would sign out
// every session, including the one in use (S2 keeps it signed in).
func TestChangeOwnPasswordKeepsEverySessionWithoutTheCurrentOne(t *testing.T) {
	f := newOwnPasswordFixture(t)
	before := f.state(t, f.actor)

	err := f.svc.ChangeOwnPassword(context.Background(), f.actor, "  ", ownCurrentPassword, ownNextPassword)
	var forbidden *domain.ForbiddenError
	if !errors.As(err, &forbidden) {
		t.Fatalf("ChangeOwnPassword without the current session = %v, want a ForbiddenError", err)
	}
	if f.changes.calls != 0 {
		t.Errorf("the store write ran %d times without a current session, want 0", f.changes.calls)
	}
	assertOwnPasswordUnchanged(t, f, before)
}

// TestChangeOwnPasswordRefusesWhenTheWritePathIsNotWired proves the use case
// fails closed instead of panicking when the composition root forgot the store:
// reporting a change that was never persisted is the one outcome it must not
// produce.
func TestChangeOwnPasswordRefusesWhenTheWritePathIsNotWired(t *testing.T) {
	f := newOwnPasswordFixture(t)
	f.svc.WithPasswordChangeStore(nil)
	before := f.state(t, f.actor)

	err := f.svc.ChangeOwnPassword(context.Background(), f.actor, "sess-current", ownCurrentPassword, ownNextPassword)
	if err == nil {
		t.Fatal("ChangeOwnPassword without a write store succeeded, want a refused change")
	}
	if !strings.Contains(err.Error(), "password change store") {
		t.Errorf("error = %v, want it to name the missing store", err)
	}
	assertOwnPasswordUnchanged(t, f, before)
}
