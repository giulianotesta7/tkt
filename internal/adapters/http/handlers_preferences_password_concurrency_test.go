package httpadapter

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sync/atomic"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
)

// Two own-password submissions for one account, interleaved at the write seam
// (issue #414). The section verifies the current password and the minimum
// before the store is called, so without a compare-and-set two submissions both
// pass that verification and the second one silently replaces the password the
// first one already stored — signing the winner out of the account it just
// changed.
//
// The interleaving is deterministic: the gated store pauses each request AFTER
// its current-password verification and lets the test choose the order, so no
// sleep, poll, or retry loop is involved, and exactly one order is exercised.
//
// gatedPasswordChangeStore delegates every write to the real store; the only
// thing it controls is WHEN that call runs.
type gatedPasswordChangeStore struct {
	inner    application.PasswordChangeStore
	arrivals chan int
	release  [2]chan struct{}
	calls    atomic.Int32
}

func newGatedPasswordChangeStore(inner application.PasswordChangeStore) *gatedPasswordChangeStore {
	return &gatedPasswordChangeStore{
		inner:    inner,
		arrivals: make(chan int, 2),
		release:  [2]chan struct{}{make(chan struct{}), make(chan struct{})},
	}
}

func (s *gatedPasswordChangeStore) ChangePasswordAndSignOutOtherSessions(ctx context.Context, userID int64, expectedPasswordHash, passwordHash, keepSessionID string) error {
	n := int(s.calls.Add(1)) - 1
	s.arrivals <- n
	<-s.release[n]
	return s.inner.ChangePasswordAndSignOutOtherSessions(ctx, userID, expectedPasswordHash, passwordHash, keepSessionID)
}

// waitForArrival blocks until the submission numbered want has reached the write
// seam, so the test never guesses at scheduling.
func (s *gatedPasswordChangeStore) waitForArrival(t *testing.T, want int) {
	t.Helper()
	if got := <-s.arrivals; got != want {
		t.Fatalf("the submission reaching the write seam is %d, want %d", got, want)
	}
}

// allow lets the submission numbered n run its store call.
func (s *gatedPasswordChangeStore) allow(n int) { close(s.release[n]) }

// resetTokenDigest answers the storage digest of a raw recovery token, so a test
// can ask the reset port whether the mailed link is still redeemable.
func resetTokenDigest(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// TestPreferencesPasswordConcurrentChangesKeepOneWinner proves one submission
// wins the race and the other is refused: the winning password is the one
// stored and able to sign in, the stale submission answers the current-password
// refusal and leaves the database byte-identical, the session making the
// winning request keeps working, the account's other session is signed out, and
// another account loses neither a session nor its pending recovery link.
//
// The two subtests differ only in where the losing submission comes from: a
// sibling session of the same account (whose keeper the winner deleted) and the
// very session the winner kept. Both must be refused as a stale current
// password, because both requests were already authenticated when they arrived.
func TestPreferencesPasswordConcurrentChangesKeepOneWinner(t *testing.T) {
	const (
		winnerPassword = "next-pass-12-x"
		stalePassword  = "stale-pass-12-x-x"
	)
	for _, tc := range []struct {
		name        string
		sameSession bool
	}{
		{name: "different keep sessions"},
		{name: "same keep session", sameSession: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			member := ownPasswordUser(t, h, "owner@example.com")
			otherUser := ownPasswordUser(t, h, "unrelated@example.com")
			winnerSession := ownPasswordSession(t, h, member.Email)
			sibling := ownPasswordSession(t, h, member.Email)
			loserSession := sibling
			if tc.sameSession {
				loserSession = winnerSession
			}
			otherSession := ownPasswordSession(t, h, otherUser.Email)
			// Both accounts hold a pending recovery link, so the winner can only
			// void its own and the losing submission must void neither.
			configureEmailDelivery(t, h)
			memberToken := requestResetLink(t, h, member.Email)
			otherToken := requestResetLink(t, h, otherUser.Email)
			if got := pendingResetLinks(t, h); got != 2 {
				t.Fatalf("pending links before the race = %d, want 2", got)
			}
			before := storedOwnPasswordHash(t, h, member.ID)

			gate := newGatedPasswordChangeStore(h.store.PasswordChangeStore())
			h.users.WithPasswordChangeStore(gate)
			submit := func(sessionID, next string) chan *httptest.ResponseRecorder {
				out := make(chan *httptest.ResponseRecorder, 1)
				go func() {
					out <- h.postFormAs(t, "/preferences/password", ownPasswordForm(func(f url.Values) {
						f.Set("new_password", next)
						f.Set("confirm_password", next)
					}), sessionID)
				}()
				return out
			}

			winningResult := submit(winnerSession, winnerPassword)
			gate.waitForArrival(t, 0)
			staleResult := submit(loserSession, stalePassword)
			gate.waitForArrival(t, 1)
			gate.allow(0)
			winning := <-winningResult
			if winning.Code != http.StatusSeeOther {
				t.Fatalf("the submission that ran first = %d, want 303: %.800s", winning.Code, winning.Body.String())
			}
			if loc := winning.Header().Get("Location"); loc != "/preferences" {
				t.Fatalf("the winning submission redirects to %q, want /preferences", loc)
			}

			// Everything the winning submission committed is captured before the
			// stale one runs, so the refusal can be proved to have written nothing.
			winnerHash := storedOwnPasswordHash(t, h, member.ID)
			if !application.VerifyPassword(winnerHash, winnerPassword) {
				t.Fatalf("the first submission did not store its password")
			}
			if winnerHash == before {
				t.Fatal("the stored hash is unchanged, want the first submission's password")
			}
			dbBefore, err := os.ReadFile(h.dbPath)
			if err != nil {
				t.Fatalf("read the database file: %v", err)
			}
			walBefore, _ := os.ReadFile(h.dbPath + "-wal")
			gate.allow(1)
			stale := <-staleResult
			dbAfter, err := os.ReadFile(h.dbPath)
			if err != nil {
				t.Fatalf("read the database file: %v", err)
			}
			walAfter, _ := os.ReadFile(h.dbPath + "-wal")

			if stale.Code != http.StatusUnprocessableEntity {
				t.Errorf("the stale submission = %d, want 422: %.800s", stale.Code, stale.Body.String())
			}
			// The refusal comes before any write, so the winner's password and the
			// stored bytes the winner committed survive the losing submission.
			if got := storedOwnPasswordHash(t, h, member.ID); got != winnerHash {
				t.Error("the stale submission overwrote the winning password")
			}
			if !bytes.Equal(dbBefore, dbAfter) || !bytes.Equal(walBefore, walAfter) {
				t.Error("the stale submission changed the database or its WAL bytes")
			}
			if body := stale.Body.String(); !containsAll(body, "the current password is incorrect") {
				t.Errorf("the stale submission must answer the current-password refusal, got: %.1500s", body)
			} else {
				assertOnlyFieldIsInvalid(t, body, "current_password")
			}
			if body := stale.Body.String(); containsAny(body, stalePassword, winnerPassword) {
				t.Error("the refused page echoed a submitted password")
			}
			// Both submissions reached the store, and exactly one of them won.
			if got := gate.calls.Load(); got != 2 {
				t.Errorf("store calls = %d, want 2 (both submissions reach the write seam)", got)
			}
			if want := 1; pendingResetLinks(t, h) != want {
				t.Errorf("pending links after the race = %d, want %d (only the other account's)", pendingResetLinks(t, h), want)
			}
			if usable, err := h.store.PasswordResetStore().PasswordResetTokenUsable(t.Context(), resetTokenDigest(memberToken), h.clock.Now()); err != nil || usable {
				t.Errorf("the account's own link after the winner = %v, %v; want voided", usable, err)
			}
			if usable, err := h.store.PasswordResetStore().PasswordResetTokenUsable(t.Context(), resetTokenDigest(otherToken), h.clock.Now()); err != nil || !usable {
				t.Errorf("the other account's link after the winner = %v, %v; want still redeemable", usable, err)
			}
			if !sessionAuthenticates(t, h, winnerSession) {
				t.Error("the session that changed the password was signed out")
			}
			if sessionAuthenticates(t, h, sibling) {
				t.Error("a session of the account outlived the change")
			}
			if !sessionAuthenticates(t, h, otherSession) {
				t.Error("another account's session was signed out")
			}
			if h.loginCookie(t, member.Email, winnerPassword) == "" {
				t.Error("the winning password does not sign in")
			}
			if h.loginCookie(t, member.Email, stalePassword) != "" {
				t.Error("the stale password signs in")
			}
			if h.loginCookie(t, member.Email, ownPasswordCurrent) != "" {
				t.Error("the previous password still signs in")
			}
		})
	}
}

// containsAll reports whether body carries every want string.
func containsAll(body string, want ...string) bool {
	for _, s := range want {
		if !bytes.Contains([]byte(body), []byte(s)) {
			return false
		}
	}
	return true
}

// containsAny reports whether body carries any of the values.
func containsAny(body string, values ...string) bool {
	for _, v := range values {
		if bytes.Contains([]byte(body), []byte(v)) {
			return true
		}
	}
	return false
}
