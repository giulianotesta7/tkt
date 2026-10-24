package sqlite

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// Company sign-in identity store (issue #419): the provider links that outlive
// an email change, the in-flight authorization rows, and the account read that
// carries the pending state. These tests run against the real modernc driver and
// migration 0034, so the port's promises are the table's constraints.

// newCompanyIdentityTestStore opens a migrated database and the identity store
// over it.
func newCompanyIdentityTestStore(t *testing.T) (*Store, *companyIdentityStore) {
	t.Helper()
	s := newTestDB(t)
	return s, newCompanyIdentityStore(s.db)
}

// seedPendingUser inserts an inactive, pending account directly (test arrange).
func seedPendingUser(t *testing.T, s *Store, name, email string) int64 {
	t.Helper()
	res, err := s.db.ExecContext(context.Background(),
		`INSERT INTO users (name, email, password_hash, role, active, pending, created_at) VALUES (?, ?, '', 'user', 0, 1, ?)`,
		name, email, formatTime(testClock))
	if err != nil {
		t.Fatalf("seed pending user %s: %v", email, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("seed pending user %s: %v", email, err)
	}
	return id
}

// TestCompanyIdentityStoreAttemptIsSingleUse proves the attempt row is
// addressed by its state hash and consumed by the callback: the second consume
// finds nothing, and two rows cannot share a hash.
func TestCompanyIdentityStoreAttemptIsSingleUse(t *testing.T) {
	_, store := newCompanyIdentityTestStore(t)
	ctx := context.Background()
	attempt := &application.CompanySignInAttempt{
		StateHash: "hash-a", Provider: application.ProviderGoogle, Nonce: "nonce-a", CodeVerifier: "verifier-a",
		CreatedAt: testClock, ExpiresAt: testClock.Add(10 * time.Minute),
	}
	if err := store.CreateAttempt(ctx, attempt); err != nil {
		t.Fatalf("CreateAttempt: %v", err)
	}
	got, err := store.ConsumeAttempt(ctx, "hash-a")
	if err != nil {
		t.Fatalf("ConsumeAttempt: %v", err)
	}
	if got.Provider != application.ProviderGoogle || got.Nonce != "nonce-a" || got.CodeVerifier != "verifier-a" ||
		!got.ExpiresAt.Equal(testClock.Add(10*time.Minute)) {
		t.Errorf("consumed attempt = %+v, want the stored values", got)
	}
	if _, err := store.ConsumeAttempt(ctx, "hash-a"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("second ConsumeAttempt = %v, want ErrNotFound", err)
	}
	if err := store.CreateAttempt(ctx, attempt); err != nil {
		t.Fatalf("CreateAttempt for a consumed state hash: %v", err)
	}
	duplicate := &application.CompanySignInAttempt{
		StateHash: "hash-a", Provider: application.ProviderMicrosoft, Nonce: "nonce-b", CodeVerifier: "verifier-b",
		CreatedAt: testClock, ExpiresAt: testClock,
	}
	if err := store.CreateAttempt(ctx, duplicate); err == nil {
		t.Error("a second attempt with the same state hash was accepted, want a UNIQUE failure")
	}
}

// TestCompanyIdentityStorePurgesOnlyExpiredAttempts proves the purge boundary:
// an attempt that expires before the given instant goes, one that expires after
// it stays, and a pending flow is never dropped early.
func TestCompanyIdentityStorePurgesOnlyExpiredAttempts(t *testing.T) {
	s, store := newCompanyIdentityTestStore(t)
	ctx := context.Background()
	for _, a := range []struct {
		hash    string
		expires time.Time
	}{
		{"expired", testClock.Add(-time.Minute)},
		{"at-the-boundary", testClock},
		{"live", testClock.Add(time.Minute)},
	} {
		if _, err := s.db.ExecContext(ctx,
			`INSERT INTO oidc_sign_in_attempts (state_hash, provider, nonce, code_verifier, created_at, expires_at)
			 VALUES (?, 'google', 'n', 'v', ?, ?)`, a.hash, formatTime(testClock.Add(-time.Hour)), formatTime(a.expires)); err != nil {
			t.Fatalf("seed attempt %s: %v", a.hash, err)
		}
	}

	purged, err := store.PurgeExpiredAttempts(ctx, testClock)
	if err != nil {
		t.Fatalf("PurgeExpiredAttempts: %v", err)
	}
	if purged != 1 {
		t.Errorf("purged = %d, want 1 (only the attempt that expired before the instant)", purged)
	}
	if _, err := store.ConsumeAttempt(ctx, "at-the-boundary"); err != nil {
		t.Errorf("attempt expiring exactly at the instant was purged: %v", err)
	}
	if _, err := store.ConsumeAttempt(ctx, "live"); err != nil {
		t.Errorf("live attempt was purged: %v", err)
	}
}

// TestCompanyIdentityStoreCreateUserWithIdentityIsAtomic proves the pair is one
// transaction: a conflict on the identity stores no account at all, so a losing
// sign-in leaves nothing behind for the retried lookup to trip over.
func TestCompanyIdentityStoreCreateUserWithIdentityIsAtomic(t *testing.T) {
	s, store := newCompanyIdentityTestStore(t)
	ctx := context.Background()
	existing := seedUser(t, s, "Ana", "ana@example.com", true)
	if err := store.LinkIdentity(ctx, &application.CompanyIdentity{
		UserID: existing, Provider: application.ProviderGoogle, Issuer: "https://accounts.google.com", Subject: "sub-1",
		Email: "ana@example.com", CreatedAt: testClock, LastSignInAt: testClock,
	}); err != nil {
		t.Fatalf("seed identity: %v", err)
	}

	lost := domain.User{Name: "Bo", Email: "bo@example.com", Role: domain.RoleUser, Active: true, CreatedAt: testClock}
	err := store.CreateUserWithIdentity(ctx, &lost, &application.CompanyIdentity{
		Provider: application.ProviderGoogle, Issuer: "https://accounts.google.com", Subject: "sub-1",
		Email: "bo@example.com", CreatedAt: testClock, LastSignInAt: testClock,
	})
	if !errors.Is(err, application.ErrCompanyIdentityTaken) {
		t.Fatalf("CreateUserWithIdentity with a taken identity = %v, want ErrCompanyIdentityTaken", err)
	}
	var users int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE email = 'bo@example.com'`).Scan(&users); err != nil {
		t.Fatal(err)
	}
	if users != 0 {
		t.Errorf("accounts stored by the losing sign-in = %d, want 0 (one transaction)", users)
	}
}

// TestCompanyIdentityStoreCreatesThePendingAccountAndItsLink proves a first
// sign-in under the pending policy stores both rows with the pending flag set,
// and that ResolveIdentity answers the account with that flag.
func TestCompanyIdentityStoreCreatesThePendingAccountAndItsLink(t *testing.T) {
	_, store := newCompanyIdentityTestStore(t)
	ctx := context.Background()
	user := domain.User{Name: "Ana", Email: "ana@example.com", Role: domain.RoleUser, Active: false, Pending: true, CreatedAt: testClock}
	identity := &application.CompanyIdentity{
		Provider: application.ProviderGoogle, Issuer: "https://accounts.google.com", Subject: "sub-1",
		Email: "ana@example.com", CreatedAt: testClock, LastSignInAt: testClock,
	}
	if err := store.CreateUserWithIdentity(ctx, &user, identity); err != nil {
		t.Fatalf("CreateUserWithIdentity: %v", err)
	}
	if user.ID == 0 || identity.UserID != user.ID {
		t.Fatalf("stored ids = user %d, identity %d, want the identity linked to the created account", user.ID, identity.UserID)
	}

	gotIdentity, gotUser, err := store.ResolveIdentity(ctx, application.ProviderGoogle, "https://accounts.google.com", "sub-1")
	if err != nil {
		t.Fatalf("ResolveIdentity: %v", err)
	}
	if !gotUser.Pending || gotUser.Active {
		t.Errorf("resolved account active/pending = %v/%v, want false/true", gotUser.Active, gotUser.Pending)
	}
	if gotUser.Role != domain.RoleUser {
		t.Errorf("resolved role = %q, want user", gotUser.Role)
	}
	if gotUser.PasswordHash != "" {
		t.Errorf("resolved password hash = %q, want none", gotUser.PasswordHash)
	}
	if gotIdentity.Email != "ana@example.com" || gotIdentity.Subject != "sub-1" || gotIdentity.Provider != application.ProviderGoogle {
		t.Errorf("resolved identity = %+v, want the stored link", gotIdentity)
	}

	if _, _, err := store.ResolveIdentity(ctx, application.ProviderGoogle, "https://accounts.google.com", "sub-unknown"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("ResolveIdentity for an unknown subject = %v, want ErrNotFound", err)
	}
}

// TestCompanyIdentityStoreLinksOneIdentityPerProviderPerAccount proves both
// unique rules of the link table: one account holds one identity per provider,
// and one provider identity belongs to one account.
func TestCompanyIdentityStoreLinksOneIdentityPerProviderPerAccount(t *testing.T) {
	s, store := newCompanyIdentityTestStore(t)
	ctx := context.Background()
	ana := seedUser(t, s, "Ana", "ana@example.com", true)
	bea := seedUser(t, s, "Bea", "bea@example.com", true)

	if err := store.LinkIdentity(ctx, &application.CompanyIdentity{
		UserID: ana, Provider: application.ProviderGoogle, Issuer: "https://accounts.google.com", Subject: "sub-ana",
		Email: "ana@example.com", CreatedAt: testClock, LastSignInAt: testClock,
	}); err != nil {
		t.Fatalf("first link: %v", err)
	}
	if err := store.LinkIdentity(ctx, &application.CompanyIdentity{
		UserID: ana, Provider: application.ProviderMicrosoft, Issuer: "https://login.microsoftonline.com/t/v2.0", Subject: "sub-ana-ms",
		Email: "ana@example.com", CreatedAt: testClock, LastSignInAt: testClock,
	}); err != nil {
		t.Errorf("linking the other provider = %v, want it accepted", err)
	}
	if err := store.LinkIdentity(ctx, &application.CompanyIdentity{
		UserID: ana, Provider: application.ProviderGoogle, Issuer: "https://accounts.google.com", Subject: "sub-other",
		Email: "ana@example.com", CreatedAt: testClock, LastSignInAt: testClock,
	}); !errors.Is(err, application.ErrCompanyIdentityTaken) {
		t.Errorf("second Google identity for one account = %v, want ErrCompanyIdentityTaken", err)
	}
	if err := store.LinkIdentity(ctx, &application.CompanyIdentity{
		UserID: bea, Provider: application.ProviderGoogle, Issuer: "https://accounts.google.com", Subject: "sub-ana",
		Email: "bea@example.com", CreatedAt: testClock, LastSignInAt: testClock,
	}); !errors.Is(err, application.ErrCompanyIdentityTaken) {
		t.Errorf("one identity on two accounts = %v, want ErrCompanyIdentityTaken", err)
	}
}

// TestCompanyIdentityStoreUsersByEmailIgnoresCase proves the linking read: it
// matches whatever case the provider reports, and it answers every match so the
// caller can refuse an ambiguity instead of picking one.
func TestCompanyIdentityStoreUsersByEmailIgnoresCase(t *testing.T) {
	s, store := newCompanyIdentityTestStore(t)
	ctx := context.Background()
	ana := seedUser(t, s, "Ana", "ana@example.com", true)

	got, err := store.UsersByEmail(ctx, "ANA@Example.COM")
	if err != nil {
		t.Fatalf("UsersByEmail: %v", err)
	}
	if len(got) != 1 || got[0].ID != ana {
		t.Fatalf("matches = %+v, want the one account %d", got, ana)
	}

	bea := seedUser(t, s, "Bea", "Ana@Example.com", true)
	got, err = store.UsersByEmail(ctx, "ana@example.com")
	if err != nil {
		t.Fatalf("UsersByEmail with two matches: %v", err)
	}
	if len(got) != 2 || got[0].ID != ana || got[1].ID != bea {
		t.Errorf("matches = %+v, want both accounts %d and %d in id order", got, ana, bea)
	}

	none, err := store.UsersByEmail(ctx, "nobody@example.com")
	if err != nil {
		t.Fatalf("UsersByEmail with no match: %v", err)
	}
	if len(none) != 0 {
		t.Errorf("matches = %+v, want none", none)
	}
}

// TestCompanyIdentityStoreTouchIdentityRecordsTheEmailAndInstant proves the
// refresh a repeated sign-in writes, and that an identity that does not exist is
// reported rather than silently ignored.
func TestCompanyIdentityStoreTouchIdentityRecordsTheEmailAndInstant(t *testing.T) {
	_, store := newCompanyIdentityTestStore(t)
	ctx := context.Background()
	user := domain.User{Name: "Ana", Email: "ana@example.com", Role: domain.RoleUser, Active: true, CreatedAt: testClock}
	identity := &application.CompanyIdentity{
		Provider: application.ProviderGoogle, Issuer: "https://accounts.google.com", Subject: "sub-1",
		Email: "ana@example.com", CreatedAt: testClock, LastSignInAt: testClock,
	}
	if err := store.CreateUserWithIdentity(ctx, &user, identity); err != nil {
		t.Fatalf("CreateUserWithIdentity: %v", err)
	}

	later := testClock.Add(48 * time.Hour)
	if err := store.TouchIdentity(ctx, identity.ID, "ana.married@example.com", later); err != nil {
		t.Fatalf("TouchIdentity: %v", err)
	}
	got, _, err := store.ResolveIdentity(ctx, application.ProviderGoogle, "https://accounts.google.com", "sub-1")
	if err != nil {
		t.Fatalf("ResolveIdentity: %v", err)
	}
	if got.Email != "ana.married@example.com" {
		t.Errorf("identity email = %q, want the address last seen", got.Email)
	}
	if !got.LastSignInAt.Equal(later) {
		t.Errorf("identity last sign-in = %v, want %v", got.LastSignInAt, later)
	}
	if !got.CreatedAt.Equal(testClock) {
		t.Errorf("identity created at = %v, want %v (linking is not rewritten)", got.CreatedAt, testClock)
	}
	if err := store.TouchIdentity(ctx, 4242, "x@example.com", later); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("TouchIdentity for a missing identity = %v, want ErrNotFound", err)
	}
}

// TestCompanyIdentityStoreIdentityGoesWithTheAccount proves the link cannot
// outlive its account: deleting the account removes the identity, so nothing can
// sign in to a row that no longer exists.
func TestCompanyIdentityStoreIdentityGoesWithTheAccount(t *testing.T) {
	s, store := newCompanyIdentityTestStore(t)
	ctx := context.Background()
	pending := seedPendingUser(t, s, "Ana", "ana@example.com")
	if err := store.LinkIdentity(ctx, &application.CompanyIdentity{
		UserID: pending, Provider: application.ProviderGoogle, Issuer: "https://accounts.google.com", Subject: "sub-1",
		Email: "ana@example.com", CreatedAt: testClock, LastSignInAt: testClock,
	}); err != nil {
		t.Fatalf("LinkIdentity: %v", err)
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, pending); err != nil {
		t.Fatalf("delete the account: %v", err)
	}
	if _, _, err := store.ResolveIdentity(ctx, application.ProviderGoogle, "https://accounts.google.com", "sub-1"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("ResolveIdentity after the account was deleted = %v, want ErrNotFound (ON DELETE CASCADE)", err)
	}
}

// TestResolveIdentityAnswersTheLinkedAccountID is S7 on the real driver: a
// stored identity resolves to the account it belongs to. The in-memory fake
// cannot catch a lost id, so this read is pinned here.
func TestResolveIdentityAnswersTheLinkedAccountID(t *testing.T) {
	s, store := newCompanyIdentityTestStore(t)
	ctx := context.Background()
	id := seedUser(t, s, "Ana", "ana@example.com", true)
	link := &application.CompanyIdentity{
		UserID: id, Provider: application.ProviderGoogle, Issuer: "https://accounts.google.com", Subject: "subject",
		Email: "ana@example.com", CreatedAt: testClock, LastSignInAt: testClock,
	}
	if err := store.LinkIdentity(ctx, link); err != nil {
		t.Fatalf("LinkIdentity: %v", err)
	}
	got, user, err := store.ResolveIdentity(ctx, application.ProviderGoogle, link.Issuer, link.Subject)
	if err != nil {
		t.Fatalf("ResolveIdentity: %v", err)
	}
	if user.ID != id || got.UserID != id {
		t.Fatalf("resolved user.ID = %d, identity.UserID = %d; want %d", user.ID, got.UserID, id)
	}
}

type repeatSignInClock struct{}

func (repeatSignInClock) Now() time.Time { return testClock }

type repeatSignInConfig struct{}

func (repeatSignInConfig) Get(context.Context) (application.CompanySignInSettings, error) {
	return application.CompanySignInSettings{
		Address: "https://tkt.example.com", NewAccounts: application.NewAccountsRequester,
		Google: application.GoogleSignIn{Enabled: true, ClientID: "client", SealedSecret: "secret", Domains: []string{"example.com"}},
	}, nil
}

func (repeatSignInConfig) ClientSecret(context.Context, application.CompanyProvider) (string, error) {
	return "secret", nil
}

type repeatSignInProvider struct{ email string }

func (*repeatSignInProvider) AuthorizationURL(context.Context, application.CompanyProviderCredentials, application.CompanyAuthorizationRequest) (string, error) {
	return "https://accounts.google.com/auth", nil
}

func (p *repeatSignInProvider) Exchange(context.Context, application.CompanyProviderCredentials, application.CompanyAuthorizationRequest, string) (application.CompanyIdentityClaims, error) {
	return application.CompanyIdentityClaims{
		Issuer: "https://accounts.google.com", Subject: "subject", Email: p.email, EmailVerified: true, HostedDomain: "example.com",
	}, nil
}

// TestRepeatCompanySignInAfterAnEmailChangeKeepsTheAccount is S7 end to end
// on the real store and session table: the second sign-in, after the provider
// changed the email, signs the SAME account in and creates no other.
func TestRepeatCompanySignInAfterAnEmailChangeKeepsTheAccount(t *testing.T) {
	s, store := newCompanyIdentityTestStore(t)
	ctx := context.Background()
	provider := &repeatSignInProvider{email: "ana@example.com"}
	service := application.NewCompanySignInService(repeatSignInConfig{}, store, provider, newSessionStore(s.db), nil, repeatSignInClock{})
	complete := func() (application.CompanySignInOutcome, error) {
		start, err := service.Begin(ctx, application.ProviderGoogle)
		if err != nil {
			return application.CompanySignInOutcome{}, err
		}
		return service.Complete(ctx, application.CompanySignInCallback{
			Provider: application.ProviderGoogle, CookieState: start.State, QueryState: start.State, Code: "code",
		})
	}

	first, err := complete()
	if err != nil || first.Kind != application.CompanySignInSignedIn {
		t.Fatalf("first sign-in = %+v, %v", first, err)
	}
	provider.email = "ana.changed@example.com"
	second, err := complete()
	if err != nil {
		t.Fatalf("repeat sign-in after an email change: %v", err)
	}
	if second.Kind != application.CompanySignInSignedIn || second.User.ID != first.User.ID || second.Session.UserID != first.User.ID {
		t.Fatalf("first = %+v, second = %+v; want the same account signed in", first, second)
	}
	var users int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&users); err != nil {
		t.Fatal(err)
	}
	if users != 1 {
		t.Fatalf("users = %d, want 1", users)
	}
}
