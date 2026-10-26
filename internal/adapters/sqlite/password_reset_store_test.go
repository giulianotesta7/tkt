package sqlite

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// Self-service password recovery — the store half (issue #414, S3/S7/S8/S9).
// Issuing a link voids the account's pending ones in ONE transaction; redeeming
// one looks it up by hash, refuses an unknown, spent or expired hash, replaces
// the password, marks the link spent, voids the other pending ones and signs
// every session of that account out — all or nothing. Written before the port
// exists — compile-error RED.

// seedResetToken inserts one reset-token row directly (test arrange) so a test
// owns the exact hash and instants it asserts on.
func seedResetToken(t *testing.T, s *Store, userID int64, hash string, createdAt, expiresAt time.Time, usedAt *time.Time) {
	t.Helper()
	if _, err := s.db.Exec(
		`INSERT INTO password_reset_tokens (user_id, token_hash, created_at, expires_at, used_at) VALUES (?, ?, ?, ?, ?)`,
		userID, hash, formatTime(createdAt), formatTime(expiresAt), formatTimePtr(usedAt)); err != nil {
		t.Fatalf("seed reset token %s: %v", hash, err)
	}
}

// resetTokenRow is one stored row as a test reads it back.
type resetTokenRow struct {
	Hash      string
	CreatedAt string
	ExpiresAt string
	UsedAt    *string
}

// resetTokenRows answers one account's stored rows, ordered by the hash, so a
// test can prove exactly which links survived a write.
func resetTokenRows(t *testing.T, s *Store, userID int64) []resetTokenRow {
	t.Helper()
	rows, err := s.db.Query(`SELECT token_hash, created_at, expires_at, used_at FROM password_reset_tokens WHERE user_id = ? ORDER BY token_hash`, userID)
	if err != nil {
		t.Fatalf("read reset tokens of user %d: %v", userID, err)
	}
	defer rows.Close()
	var out []resetTokenRow
	for rows.Next() {
		var (
			row    resetTokenRow
			usedAt *string
		)
		if err := rows.Scan(&row.Hash, &row.CreatedAt, &row.ExpiresAt, &usedAt); err != nil {
			t.Fatalf("scan reset token: %v", err)
		}
		row.UsedAt = usedAt
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read reset tokens of user %d: %v", userID, err)
	}
	return out
}

// resetTokenHashes answers the stored hashes of one account, sorted.
func resetTokenHashes(t *testing.T, s *Store, userID int64) []string {
	t.Helper()
	rows := resetTokenRows(t, s, userID)
	hashes := make([]string, 0, len(rows))
	for _, row := range rows {
		hashes = append(hashes, row.Hash)
	}
	sort.Strings(hashes)
	return hashes
}

// TestPasswordResetStoreIssueVoidsThePendingLinks proves one request leaves the
// account with exactly one redeemable link: the pending rows it had are voided
// (removed, so they can never be redeemed) while a spent row stays as history
// and another account keeps its own pending link untouched.
func TestPasswordResetStoreIssueVoidsThePendingLinks(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	ana := seedUser(t, s, "Ana", "ana@example.com", true)
	bea := seedUser(t, s, "Bea", "bea@example.com", true)
	spent := testClock.Add(-30 * time.Minute)
	seedResetToken(t, s, ana, "hash-spent", testClock.Add(-40*time.Minute), testClock.Add(-40*time.Minute).Add(time.Hour), &spent)
	seedResetToken(t, s, ana, "hash-old", testClock.Add(-10*time.Minute), testClock.Add(-10*time.Minute).Add(time.Hour), nil)
	seedResetToken(t, s, bea, "hash-bea", testClock.Add(-10*time.Minute), testClock.Add(-10*time.Minute).Add(time.Hour), nil)

	if err := s.PasswordResetStore().IssuePasswordResetToken(ctx, ana, "hash-new", testClock, testClock.Add(time.Hour)); err != nil {
		t.Fatalf("IssuePasswordResetToken: %v", err)
	}

	if got, want := resetTokenHashes(t, s, ana), []string{"hash-new", "hash-spent"}; !reflect.DeepEqual(got, want) {
		t.Errorf("stored hashes for the account = %v, want %v (pending ones voided, the spent row kept)", got, want)
	}
	if got, want := resetTokenHashes(t, s, bea), []string{"hash-bea"}; !reflect.DeepEqual(got, want) {
		t.Errorf("the other account's hashes = %v, want %v", got, want)
	}
	for _, row := range resetTokenRows(t, s, ana) {
		if row.Hash != "hash-new" {
			continue
		}
		if row.CreatedAt != formatTime(testClock) || row.ExpiresAt != formatTime(testClock.Add(time.Hour)) {
			t.Errorf("stored instants = (%s, %s), want (%s, %s)", row.CreatedAt, row.ExpiresAt,
				formatTime(testClock), formatTime(testClock.Add(time.Hour)))
		}
		if row.UsedAt != nil {
			t.Errorf("a fresh link reads as spent: used_at = %v", *row.UsedAt)
		}
	}
}

// TestPasswordResetStoreLastRequestInstants covers the throttle read: no link
// answers nothing at all, and with several rows the newest creation instant is
// the one returned, spent rows included.
func TestPasswordResetStoreLastRequestInstants(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	ana := seedUser(t, s, "Ana", "ana@example.com", true)
	bea := seedUser(t, s, "Bea", "bea@example.com", true)

	last, err := s.PasswordResetStore().LastPasswordResetRequest(ctx, ana)
	if err != nil {
		t.Fatalf("LastPasswordResetRequest on an account with no link: %v", err)
	}
	if last != nil {
		t.Fatalf("last request instant = %v, want nil when the account never asked", last)
	}

	newest := testClock.Add(-2 * time.Minute)
	seedResetToken(t, s, ana, "hash-older", testClock.Add(-20*time.Minute), testClock.Add(-20*time.Minute).Add(time.Hour), nil)
	seedResetToken(t, s, ana, "hash-newer", newest, newest.Add(time.Hour), nil)
	seedResetToken(t, s, bea, "hash-bea", testClock.Add(-time.Minute), testClock.Add(-time.Minute).Add(time.Hour), nil)

	last, err = s.PasswordResetStore().LastPasswordResetRequest(ctx, ana)
	if err != nil {
		t.Fatalf("LastPasswordResetRequest: %v", err)
	}
	if last == nil || !last.Equal(newest) {
		t.Fatalf("last request instant = %v, want %v (the newest of that account only)", last, newest)
	}
}

// TestPasswordResetStoreRedeemReplacesThePasswordOnce proves the whole success
// contract in one transaction: the new hash is stored, the redeemed link is
// marked spent, the account's other pending links are voided, and EVERY session
// of that account is deleted (S3) while another account keeps its own link and
// sessions.
func TestPasswordResetStoreRedeemReplacesThePasswordOnce(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	ana := seedUser(t, s, "Ana", "ana@example.com", true)
	bea := seedUser(t, s, "Bea", "bea@example.com", true)
	for _, seed := range []struct {
		id     string
		userID int64
	}{
		{"sess-laptop", ana},
		{"sess-phone", ana},
		{"sess-bea", bea},
	} {
		seedPasswordSession(t, s, seed.id, seed.userID)
	}
	expires := testClock.Add(time.Hour)
	seedResetToken(t, s, ana, "hash-redeemed", testClock.Add(-time.Minute), expires, nil)
	seedResetToken(t, s, ana, "hash-pending", testClock.Add(-time.Minute), expires, nil)
	seedResetToken(t, s, ana, "hash-spent", testClock.Add(-time.Hour), expires, ptr(testClock.Add(-30*time.Minute)))
	seedResetToken(t, s, bea, "hash-bea", testClock.Add(-time.Minute), expires, nil)

	if err := s.PasswordResetStore().RedeemPasswordResetToken(ctx, "hash-redeemed", "new-bcrypt-hash", testClock); err != nil {
		t.Fatalf("RedeemPasswordResetToken: %v", err)
	}

	if got, want := passwordHashOf(t, s, ana), "new-bcrypt-hash"; got != want {
		t.Errorf("stored hash = %q, want %q", got, want)
	}
	if got := passwordSessionIDs(t, s, ana); len(got) != 0 {
		t.Errorf("sessions of the account after the reset = %v, want none (S3)", got)
	}
	if got, want := passwordSessionIDs(t, s, bea), []string{"sess-bea"}; !reflect.DeepEqual(got, want) {
		t.Errorf("the other account's sessions = %v, want %v", got, want)
	}
	if got, want := resetTokenHashes(t, s, ana), []string{"hash-redeemed", "hash-spent"}; !reflect.DeepEqual(got, want) {
		t.Errorf("stored hashes after the reset = %v, want %v (the others voided)", got, want)
	}
	for _, row := range resetTokenRows(t, s, ana) {
		if row.Hash != "hash-redeemed" {
			continue
		}
		if row.UsedAt == nil || *row.UsedAt != formatTime(testClock) {
			t.Errorf("redeemed row used_at = %v, want %s", row.UsedAt, formatTime(testClock))
		}
	}
	if got, want := resetTokenHashes(t, s, bea), []string{"hash-bea"}; !reflect.DeepEqual(got, want) {
		t.Errorf("the other account's hashes = %v, want %v", got, want)
	}
	if got := passwordHashOf(t, s, bea); got != "bcrypt-hash" {
		t.Errorf("the other account's hash = %q, want it untouched", got)
	}
}

// TestPasswordResetStoreRedeemRefusesUnusableLinks proves an unknown, spent or
// expired hash answers the same refusal and writes nothing: the password, the
// sessions and the stored rows are exactly as they were. The expiry boundary is
// exclusive — a link is usable while now is strictly before its expiry, so at
// exactly one hour it is already refused.
func TestPasswordResetStoreRedeemRefusesUnusableLinks(t *testing.T) {
	tests := []struct {
		name     string
		hash     string
		seedUsed *time.Time
		expires  time.Time
		now      time.Time
	}{
		{name: "unknown hash", hash: "hash-unknown", expires: testClock.Add(time.Hour), now: testClock},
		{name: "already spent", hash: "hash-spent", seedUsed: ptr(testClock.Add(-time.Minute)), expires: testClock.Add(time.Hour), now: testClock},
		{name: "expired", hash: "hash-live", expires: testClock.Add(time.Hour), now: testClock.Add(time.Hour + time.Minute)},
		{name: "exactly at the expiry instant", hash: "hash-live", expires: testClock.Add(time.Hour), now: testClock.Add(time.Hour)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestDB(t)
			ctx := context.Background()
			ana := seedUser(t, s, "Ana", "ana@example.com", true)
			seedPasswordSession(t, s, "sess-laptop", ana)
			seedResetToken(t, s, ana, "hash-live", testClock.Add(-time.Minute), tt.expires, tt.seedUsed)
			seedResetToken(t, s, ana, "hash-other", testClock.Add(-time.Minute), testClock.Add(time.Hour), nil)
			before := resetTokenRows(t, s, ana)

			err := s.PasswordResetStore().RedeemPasswordResetToken(ctx, tt.hash, "new-bcrypt-hash", tt.now)
			if !errors.Is(err, domain.ErrNotFound) {
				t.Fatalf("redeem %q = %v, want ErrNotFound", tt.hash, err)
			}
			if got := passwordHashOf(t, s, ana); got != "bcrypt-hash" {
				t.Errorf("stored hash after the refusal = %q, want it untouched", got)
			}
			if got, want := passwordSessionIDs(t, s, ana), []string{"sess-laptop"}; !reflect.DeepEqual(got, want) {
				t.Errorf("sessions after the refusal = %v, want %v", got, want)
			}
			if got := resetTokenRows(t, s, ana); !reflect.DeepEqual(got, before) {
				t.Errorf("stored rows after the refusal = %v, want %v", got, before)
			}
		})
	}
}

// TestPasswordResetStoreRedeemRollsBackOnAMidwayFailure proves the unit really
// is atomic: with the session delete aborted, the password write and the spent
// mark it travelled with are rolled back too, and the account's other pending
// link survives, so a link is never half redeemed.
func TestPasswordResetStoreRedeemRollsBackOnAMidwayFailure(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	ana := seedUser(t, s, "Ana", "ana@example.com", true)
	seedPasswordSession(t, s, "sess-laptop", ana)
	seedResetToken(t, s, ana, "hash-live", testClock.Add(-time.Minute), testClock.Add(time.Hour), nil)
	seedResetToken(t, s, ana, "hash-other", testClock.Add(-time.Minute), testClock.Add(time.Hour), nil)
	before := resetTokenRows(t, s, ana)

	if _, err := s.db.ExecContext(ctx, `
		CREATE TRIGGER abort_password_reset_signout BEFORE DELETE ON sessions
		BEGIN SELECT RAISE(ABORT, 'injected reset sign-out failure'); END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}
	t.Cleanup(func() {
		// context.Background(), NOT t.Context(): t.Context() is cancelled during
		// cleanup, so the drop would fail with "context canceled".
		if _, err := s.db.ExecContext(context.Background(), `DROP TRIGGER abort_password_reset_signout`); err != nil {
			t.Errorf("drop trigger: %v", err)
		}
	})

	err := s.PasswordResetStore().RedeemPasswordResetToken(ctx, "hash-live", "new-bcrypt-hash", testClock)
	if err == nil {
		t.Fatal("the redeem succeeded with its session delete aborted, want an error")
	}
	if got := passwordHashOf(t, s, ana); got != "bcrypt-hash" {
		t.Errorf("stored hash after the rollback = %q, want the original", got)
	}
	if got, want := passwordSessionIDs(t, s, ana), []string{"sess-laptop"}; !reflect.DeepEqual(got, want) {
		t.Errorf("sessions after the rollback = %v, want %v", got, want)
	}
	if got := resetTokenRows(t, s, ana); !reflect.DeepEqual(got, before) {
		t.Errorf("stored rows after the rollback = %v, want %v", got, before)
	}
}

// TestPasswordResetStoreRedeemsOneLinkExactlyOnceConcurrently proves the
// transaction under concurrency: two redeemals of the same hash racing each
// other replace the password exactly once, so the loser meets a spent link
// instead of overwriting the winner's password, and every session is gone.
func TestPasswordResetStoreRedeemsOneLinkExactlyOnceConcurrently(t *testing.T) {
	s, err := openDSN(testDSN(t))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	s.db.SetMaxOpenConns(8)
	t.Cleanup(func() { s.db.Close() })
	ctx := context.Background()
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	ana := seedUser(t, s, "Ana", "ana@example.com", true)
	for _, id := range []string{"sess-laptop", "sess-phone"} {
		seedPasswordSession(t, s, id, ana)
	}
	seedResetToken(t, s, ana, "hash-live", testClock.Add(-time.Minute), testClock.Add(time.Hour), nil)

	hashes := []string{"hash-from-a", "hash-from-b"}
	results := make([]error, len(hashes))
	var wg sync.WaitGroup
	for i, hash := range hashes {
		wg.Add(1)
		go func(i int, hash string) {
			defer wg.Done()
			results[i] = s.PasswordResetStore().RedeemPasswordResetToken(ctx, "hash-live", hash, testClock)
		}(i, hash)
	}
	wg.Wait()

	winners := 0
	for i, err := range results {
		switch {
		case err == nil:
			winners++
		case errors.Is(err, domain.ErrNotFound):
			// The loser read a link that was already spent: expected.
		default:
			t.Fatalf("redeem %d failed with an unexpected error: %v", i, err)
		}
	}
	if winners != 1 {
		t.Fatalf("%d redeemals of one link succeeded, want exactly 1", winners)
	}
	stored := passwordHashOf(t, s, ana)
	if stored != hashes[0] && stored != hashes[1] {
		t.Errorf("stored hash = %q, want one of the submitted hashes", stored)
	}
	if got, want := resetTokenHashes(t, s, ana), []string{"hash-live"}; !reflect.DeepEqual(got, want) {
		t.Errorf("stored hashes = %v, want %v", got, want)
	}
	if got := passwordSessionIDs(t, s, ana); len(got) != 0 {
		t.Errorf("sessions after the reset = %v, want none", got)
	}
}

// resetServiceClock is the injected clock these tests hand the recovery use
// cases, so a link's instants are the test's and never the wall clock's.
type resetServiceClock struct{ now time.Time }

func (c resetServiceClock) Now() time.Time { return c.now }

// resetServicePolicy answers one fixed instance policy.
type resetServicePolicy struct{ settings application.PasswordSettings }

func (p resetServicePolicy) GetPasswordSettings(context.Context) (application.PasswordSettings, error) {
	return p.settings, nil
}

func (p resetServicePolicy) SetPasswordSettings(context.Context, application.PasswordSettings) error {
	return nil
}

// resetServiceDelivery answers one fixed, complete delivery configuration.
type resetServiceDelivery struct{ config application.DeliveryConfig }

func (d resetServiceDelivery) DeliveryConfig(context.Context) (application.DeliveryConfig, bool, error) {
	return d.config, true, nil
}

// resetServiceMailer records the messages the recovery use case would send.
type resetServiceMailer struct{ sent []application.OutgoingEmail }

func (m *resetServiceMailer) Send(_ context.Context, _ application.SMTPConfig, email application.OutgoingEmail) error {
	m.sent = append(m.sent, email)
	return nil
}

// resetServiceRenderer answers "key(arg,arg)" so a test can read the mailed link
// back out of the body.
type resetServiceRenderer struct{}

func (resetServiceRenderer) Render(_ string, message application.Msg) string {
	args := make([]string, 0, len(message.Args))
	for _, arg := range message.Args {
		args = append(args, arg.Value)
	}
	return message.Key + "(" + strings.Join(args, ",") + ")"
}

// resetLinkToken answers the token the recovery link in a mailed body carries.
func resetLinkToken(t *testing.T, body string) string {
	t.Helper()
	marker := application.PasswordResetPath + "?token="
	i := strings.Index(body, marker)
	if i < 0 {
		t.Fatalf("the mailed body carries no reset link:\n%s", body)
	}
	rest := body[i+len(marker):]
	end := strings.IndexFunc(rest, func(r rune) bool {
		return !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f')
	})
	if end < 0 {
		end = len(rest)
	}
	if end == 0 {
		t.Fatalf("the mailed link carries an empty token:\n%s", body)
	}
	return rest[:end]
}

// issueResetLink asks for a recovery link through the real use case and answers
// the raw token its mailed link carries.
func issueResetLink(t *testing.T, s *Store, mailer *resetServiceMailer, email string) string {
	t.Helper()
	svc := application.NewPasswordResetService(
		s.UserStore(), s.PasswordResetStore(),
		resetServicePolicy{settings: application.DefaultPasswordSettings()},
		resetServiceDelivery{config: application.DeliveryConfig{
			SMTP:     application.SMTPConfig{Host: "smtp.example.com", Port: 25, FromAddress: "tkt@example.com"},
			BaseURL:  "https://tkt.example.com",
			Language: "en",
		}},
		mailer, resetServiceRenderer{}, func(send func()) { send() },
		resetServiceClock{now: testClock})
	before := len(mailer.sent)
	if err := svc.RequestPasswordReset(context.Background(), email); err != nil {
		t.Fatalf("RequestPasswordReset(%q): %v", email, err)
	}
	if len(mailer.sent) != before+1 {
		t.Fatalf("mailed %d messages, want exactly 1", len(mailer.sent)-before)
	}
	return resetLinkToken(t, mailer.sent[len(mailer.sent)-1].Body)
}

// databaseFiles answers every byte of the database a test opened, WAL and shared
// memory sidecars included, so a test can search what really reached the disk.
func databaseFiles(t *testing.T, path string) []byte {
	t.Helper()
	paths, err := filepath.Glob(path + "*")
	if err != nil {
		t.Fatalf("list the database files of %s: %v", path, err)
	}
	var blob []byte
	for _, file := range paths {
		if strings.HasSuffix(file, "-shm") {
			continue // shared-memory index, no row data
		}
		part, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		blob = append(blob, part...)
	}
	return blob
}

// TestPasswordResetStoreNeverWritesTheRawTokenToTheDatabaseFile proves S8 on the
// bytes a copy of the instance would contain: the mailed token is only ever
// stored as its SHA-256 digest, so the database file yields no working link.
func TestPasswordResetStoreNeverWritesTheRawTokenToTheDatabaseFile(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "app.db")
	s, err := OpenForTests(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.db.Close() })
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	ana := seedUser(t, s, "Ana", "ana@example.com", true)
	mailer := &resetServiceMailer{}
	raw := issueResetLink(t, s, mailer, "ana@example.com")
	if len(raw) != 64 {
		t.Fatalf("token %q (%d characters), want 32 random bytes as 64 hex characters", raw, len(raw))
	}
	if _, err := s.db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}

	blob := databaseFiles(t, path)
	if strings.Contains(string(blob), raw) {
		t.Error("the raw token reached the database files, want only its digest")
	}
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(raw)))
	if !strings.Contains(string(blob), digest) {
		t.Fatalf("the digest %q is absent from the database files, so this test searched the wrong bytes", digest)
	}
	var stored string
	if err := s.db.QueryRowContext(ctx, `SELECT token_hash FROM password_reset_tokens WHERE user_id = ?`, ana).Scan(&stored); err != nil {
		t.Fatalf("read the stored link: %v", err)
	}
	if stored != digest {
		t.Errorf("stored token_hash = %q, want the SHA-256 digest %q", stored, digest)
	}
}

// TestPasswordResetStoreRedeemsOneLinkExactlyOnceOnTheFileDSN proves single use
// under the production file DSN (WAL, busy_timeout, immediate transactions) at
// increasing fan-in: exactly one concurrent redemption wins, the losers meet a
// spent link instead of a lock error, and every session of the account is gone.
func TestPasswordResetStoreRedeemsOneLinkExactlyOnceOnTheFileDSN(t *testing.T) {
	for _, fanIn := range []int{2, 4, 8, 16} {
		t.Run(fmt.Sprintf("fan-in-%d", fanIn), func(t *testing.T) {
			ctx := context.Background()
			s, err := OpenForTests(filepath.Join(t.TempDir(), "app.db"))
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			t.Cleanup(func() { _ = s.db.Close() })
			if err := s.Migrate(ctx); err != nil {
				t.Fatalf("migrate: %v", err)
			}
			ana := seedUser(t, s, "Ana", "ana@example.com", true)
			seedPasswordSession(t, s, "sess-laptop", ana)
			seedResetToken(t, s, ana, "hash-live", testClock.Add(-time.Minute), testClock.Add(time.Hour), nil)

			errs := make([]error, fanIn)
			start := make(chan struct{})
			var wg sync.WaitGroup
			for i := 0; i < fanIn; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					<-start
					errs[i] = s.PasswordResetStore().RedeemPasswordResetToken(ctx, "hash-live", fmt.Sprintf("hash-%d", i), testClock)
				}(i)
			}
			close(start)
			wg.Wait()

			winners := 0
			for i, err := range errs {
				switch {
				case err == nil:
					winners++
				case errors.Is(err, domain.ErrNotFound):
					// The loser met a link that was already spent: expected.
				default:
					t.Errorf("redeem %d failed with an unexpected error: %v", i, err)
				}
			}
			if winners != 1 {
				t.Errorf("%d of %d concurrent redemptions succeeded, want exactly 1", winners, fanIn)
			}
			if got := passwordSessionIDs(t, s, ana); len(got) != 0 {
				t.Errorf("sessions after the redemptions = %v, want none", got)
			}
		})
	}
}

// TestPasswordResetStoreOwnerAnswersOnlyUsableLinks pins the read company
// sign-in uses to keep a non-root link closed while password sign-in is off
// (issue #419, S5): a usable link names its account, and an unknown, spent or
// expired one names nobody.
func TestPasswordResetStoreOwnerAnswersOnlyUsableLinks(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	ana := seedUser(t, s, "Ana", "ana@example.com", true)
	spent := testClock.Add(-5 * time.Minute)
	seedResetToken(t, s, ana, "hash-usable", testClock.Add(-10*time.Minute), testClock.Add(50*time.Minute), nil)
	seedResetToken(t, s, ana, "hash-spent", testClock.Add(-10*time.Minute), testClock.Add(50*time.Minute), &spent)
	seedResetToken(t, s, ana, "hash-expired", testClock.Add(-2*time.Hour), testClock, nil)

	owner, ok, err := s.PasswordResetStore().PasswordResetTokenOwner(ctx, "hash-usable", testClock)
	if err != nil || !ok || owner != ana {
		t.Fatalf("owner of the usable link = %d, %v, %v; want %d, true", owner, ok, err, ana)
	}
	for _, hash := range []string{"hash-spent", "hash-expired", "hash-unknown"} {
		if owner, ok, err := s.PasswordResetStore().PasswordResetTokenOwner(ctx, hash, testClock); err != nil || ok || owner != 0 {
			t.Errorf("owner of %s = %d, %v, %v; want nobody", hash, owner, ok, err)
		}
	}
}
