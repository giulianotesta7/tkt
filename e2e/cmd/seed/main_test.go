package main

// The opt-in Pending-tab fixture (issue #419, S12): the seed command can create
// ONE account waiting for approval, with the provider identity a real company
// sign-in would have linked, so the browser journeys that need such a row stop
// skipping. These tests prove the fixture's facts and that it is genuinely
// opt-in: the default seed path stays untouched, and a refused fixture writes
// nothing.

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/adapters/sqlite"
	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// newFixtureStore opens a migrated database in the test's own temporary
// directory, exactly as the command opens the E2E database, and a raw read
// handle for the one count no store port answers (the identity rows).
func newFixtureStore(t *testing.T) (*sqlite.Store, *sql.DB) {
	t.Helper()
	path := t.TempDir() + "/tkt.db"
	store, err := sqlite.Open(path)
	if err != nil {
		t.Fatalf("open the fixture database: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate the fixture database: %v", err)
	}
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("open the fixture read handle: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return store, db
}

func countRows(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", query, err)
	}
	return n
}

// TestParsePendingFixtureAnswersTheClosedSpec pins the command-line shape: a
// three-part "<name>:<email>:<provider>" spec, the closed provider set, and a
// readable address. Nothing else is accepted, so a typo fails the run instead of
// seeding a half-account.
func TestParsePendingFixtureAnswersTheClosedSpec(t *testing.T) {
	for _, tc := range []struct {
		name     string
		spec     string
		wantErr  bool
		wantName string
		wantMail string
		wantProv application.CompanyProvider
	}{
		{name: "google", spec: "Wanda Waiting:wanda@example.com:google", wantName: "Wanda Waiting", wantMail: "wanda@example.com", wantProv: application.ProviderGoogle},
		{name: "microsoft", spec: "Vic Waiting:vic@example.com:microsoft", wantName: "Vic Waiting", wantMail: "vic@example.com", wantProv: application.ProviderMicrosoft},
		{name: "surrounding spaces", spec: " Wanda : wanda@example.com : google ", wantName: "Wanda", wantMail: "wanda@example.com", wantProv: application.ProviderGoogle},
		{name: "missing parts", spec: "Wanda:wanda@example.com", wantErr: true},
		{name: "extra part", spec: "Wanda:wanda@example.com:google:extra", wantErr: true},
		{name: "empty name", spec: ":wanda@example.com:google", wantErr: true},
		{name: "empty email", spec: "Wanda::google", wantErr: true},
		{name: "no address", spec: "Wanda:wanda:google", wantErr: true},
		{name: "unknown provider", spec: "Wanda:wanda@example.com:github", wantErr: true},
		{name: "empty", spec: "", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			name, email, provider, err := parsePendingFixture(tc.spec)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parsePendingFixture(%q) = (%q, %q, %q), want a refusal", tc.spec, name, email, provider)
				}
				return
			}
			if err != nil {
				t.Fatalf("parsePendingFixture(%q): %v", tc.spec, err)
			}
			if name != tc.wantName || email != tc.wantMail || provider != tc.wantProv {
				t.Fatalf("parsePendingFixture(%q) = (%q, %q, %q), want (%q, %q, %q)",
					tc.spec, name, email, provider, tc.wantName, tc.wantMail, tc.wantProv)
			}
		})
	}
}

// TestSeedPendingFixtureCreatesTheWaitingAccountTheSignInWould proves the
// fixture is the row production creates: a requester, inactive and pending, with
// no password and exactly one provider identity — and that it is NOT a managed
// user, so the ordinary lists keep it out.
func TestSeedPendingFixtureCreatesTheWaitingAccountTheSignInWould(t *testing.T) {
	store, db := newFixtureStore(t)
	ctx := context.Background()
	at := time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC)

	got, err := seedPendingFixture(ctx, store, "Wanda Waiting:wanda@example.com:google", at)
	if err != nil {
		t.Fatalf("seedPendingFixture: %v", err)
	}
	if got.User.ID == 0 || got.User.Name != "Wanda Waiting" || got.User.Email != "wanda@example.com" {
		t.Fatalf("fixture account = %+v, want the requested identity", got.User)
	}
	if got.User.Active || !got.User.Pending || got.User.Role != domain.RoleUser {
		t.Errorf("fixture account active=%v pending=%v role=%q, want an inactive pending requester",
			got.User.Active, got.User.Pending, got.User.Role)
	}
	if got.User.PasswordHash != "" {
		t.Errorf("fixture password hash = %q, want none: no provider flow could set one", got.User.PasswordHash)
	}

	waiting, err := store.PendingUserStore().ListPending(ctx)
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	if len(waiting) != 1 || waiting[0].ID != got.User.ID {
		t.Fatalf("pending list = %+v, want only the fixture account %d", waiting, got.User.ID)
	}
	managed, err := store.UserStore().List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(managed) != 0 {
		t.Errorf("managed users = %+v, want none: a waiting account is not a managed user", managed)
	}

	identity, user, err := store.CompanyIdentityStore().ResolveIdentity(ctx, application.ProviderGoogle, got.Identity.Issuer, got.Identity.Subject)
	if err != nil {
		t.Fatalf("ResolveIdentity: %v", err)
	}
	if user.ID != got.User.ID || identity.Provider != application.ProviderGoogle {
		t.Fatalf("resolved identity = %+v / account %+v, want the fixture account with its google identity", identity, user)
	}
	if identity.Email != "wanda@example.com" || identity.Issuer != got.Identity.Issuer || identity.Subject != got.Identity.Subject {
		t.Errorf("identity = %+v, want the address and pair the fixture stored", identity)
	}
	if !strings.HasPrefix(got.Identity.Issuer, "https://accounts.google.com") {
		t.Errorf("google issuer = %q, want the real issuer shape", got.Identity.Issuer)
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM user_identities`); n != 1 {
		t.Errorf("identity rows = %d, want exactly one", n)
	}
}

// TestSeedPendingFixtureKeepsTwoAccountsApart proves the fixture is usable more
// than once in one database: a second provider gets its own account and its own
// identity pair, so the two rows carry different providers truthfully.
func TestSeedPendingFixtureKeepsTwoAccountsApart(t *testing.T) {
	store, _ := newFixtureStore(t)
	ctx := context.Background()
	at := time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC)

	first, err := seedPendingFixture(ctx, store, "Wanda Waiting:wanda@example.com:google", at)
	if err != nil {
		t.Fatalf("seed google fixture: %v", err)
	}
	second, err := seedPendingFixture(ctx, store, "Vic Waiting:vic@example.com:microsoft", at)
	if err != nil {
		t.Fatalf("seed microsoft fixture: %v", err)
	}
	if first.User.ID == second.User.ID {
		t.Fatalf("both fixtures created account %d, want two accounts", first.User.ID)
	}
	if first.Identity.Issuer == second.Identity.Issuer || first.Identity.Subject == second.Identity.Subject {
		t.Errorf("fixture identities share a pair: %+v and %+v", first.Identity, second.Identity)
	}
	if !strings.Contains(second.Identity.Issuer, "login.microsoftonline.com") {
		t.Errorf("microsoft issuer = %q, want the real issuer shape", second.Identity.Issuer)
	}
	waiting, err := store.PendingUserStore().ListPending(ctx)
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	if len(waiting) != 2 {
		t.Fatalf("pending list = %+v, want both fixture accounts", waiting)
	}
}

// TestSeedPendingFixtureRefusesADuplicateWithoutWriting proves a refused fixture
// is a no-op: the duplicate address fails the run and the store keeps exactly the
// rows it already had.
func TestSeedPendingFixtureRefusesADuplicateWithoutWriting(t *testing.T) {
	store, db := newFixtureStore(t)
	ctx := context.Background()
	at := time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC)

	if _, err := seedPendingFixture(ctx, store, "Wanda Waiting:wanda@example.com:google", at); err != nil {
		t.Fatalf("seed fixture: %v", err)
	}
	if _, err := seedPendingFixture(ctx, store, "Wanda Again:wanda@example.com:microsoft", at); err == nil {
		t.Fatal("a duplicate address must be refused, not silently stored twice")
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM users`); n != 1 {
		t.Errorf("users = %d, want the single fixture account", n)
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM user_identities`); n != 1 {
		t.Errorf("identity rows = %d, want the single fixture identity", n)
	}
}
