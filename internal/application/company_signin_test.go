package application_test

import (
	"context"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// Company sign-in flow (issue #419, T3): an employee arrives with a provider
// identity and the flow decides whether that identity signs in an existing
// account, links one, creates one, or is refused. These tests pin the state and
// attempt rules, the two provider restrictions (S2), the new-account policies
// (S3), email linking and the identity that outlives an email change (S7), the
// deactivated account (S8), and that no sign-in hands out a privileged role
// (S9).

// companyTenant is the Microsoft directory the harness configures.
const companyTenant = "0f8fad5b-d9cb-469f-a165-70867728950e"

// companySignInHarness wires the real service against the in-memory ports: the
// real configuration service over a keyed settings double, the fake identity
// store over the fake user store, the fake provider, the fake session store, and
// the approval-notification recorder.
type companySignInHarness struct {
	service    *application.CompanySignInService
	settings   *fakeCompanySignInStore
	identities *fakeCompanyIdentityStore
	provider   *fakeCompanyIdentityProvider
	users      *fakeUserStore
	sessions   *fakeSessionStore
	notifier   *fakeApprovalNotifier
	clock      *fakeClock
}

func newCompanySignInHarness() *companySignInHarness {
	clock := fixedClock()
	users := newFakeUserStore()
	sessions := newFakeSessionStore(clock)
	settings := newFakeCompanySignInStore()
	settings.settings = application.CompanySignInSettings{
		Address:               "https://tkt.example.com",
		NewAccounts:           application.NewAccountsRequester,
		PasswordSignInEnabled: true,
		Google: application.GoogleSignIn{
			Enabled: true, ClientID: "google-client", SealedSecret: "google:g-secret", Domains: []string{"example.com"},
		},
		Microsoft: application.MicrosoftSignIn{
			Enabled: true, TenantID: companyTenant, ClientID: "ms-client", SealedSecret: "microsoft:m-secret",
		},
	}
	settings.stored = true
	config := application.NewCompanySignInSettingsService(settings, tagSealer{"google"}, tagSealer{"microsoft"})
	identities := newFakeCompanyIdentityStore(users)
	provider := &fakeCompanyIdentityProvider{authURL: "https://accounts.google.com/o/oauth2/v2/auth"}
	notifier := &fakeApprovalNotifier{}
	service := application.NewCompanySignInService(config, identities, provider, sessions, notifier, clock)
	return &companySignInHarness{
		service: service, settings: settings, identities: identities, provider: provider,
		users: users, sessions: sessions, notifier: notifier, clock: clock,
	}
}

// googleClaims is a Workspace account inside the configured domain.
func googleClaims(subject, email string) application.CompanyIdentityClaims {
	return application.CompanyIdentityClaims{
		Issuer: "https://accounts.google.com", Subject: subject, Email: email,
		EmailVerified: true, HostedDomain: "example.com", Name: "Ana",
	}
}

// microsoftClaims is an Entra account inside the configured tenant.
func microsoftClaims(subject, email string) application.CompanyIdentityClaims {
	return application.CompanyIdentityClaims{
		Issuer:  "https://login.microsoftonline.com/" + companyTenant + "/v2.0",
		Subject: subject, Email: email, TenantID: companyTenant, Name: "Ana",
	}
}

func (h *companySignInHarness) start(t *testing.T, p application.CompanyProvider) application.CompanySignInStart {
	t.Helper()
	start, err := h.service.Begin(context.Background(), p)
	if err != nil {
		t.Fatalf("Begin(%s): %v", p, err)
	}
	return start
}

func (h *companySignInHarness) complete(t *testing.T, p application.CompanyProvider, start application.CompanySignInStart) application.CompanySignInOutcome {
	t.Helper()
	out, err := h.service.Complete(context.Background(), application.CompanySignInCallback{
		Provider: p, CookieState: start.State, QueryState: start.State, Code: "auth-code",
	})
	if err != nil {
		t.Fatalf("Complete(%s): %v", p, err)
	}
	return out
}

// signIn runs one whole flow: start it and answer the callback with the claims.
func (h *companySignInHarness) signIn(t *testing.T, p application.CompanyProvider, claims application.CompanyIdentityClaims) application.CompanySignInOutcome {
	t.Helper()
	h.provider.claims = claims
	return h.complete(t, p, h.start(t, p))
}

func assertRefused(t *testing.T, out application.CompanySignInOutcome, reason application.CompanySignInReason) {
	t.Helper()
	if out.Kind != application.CompanySignInRefused || out.Reason != reason {
		t.Fatalf("outcome = %q/%q, want refused/%q", out.Kind, out.Reason, reason)
	}
	if out.Session != nil {
		t.Errorf("refused outcome carried a session %q, want none", out.Session.ID)
	}
}

// assertSessionMatchesThePasswordLoginContract proves the issued session is the
// one AuthService.Login issues: 32 random bytes as hex, expiring one SessionTTL
// from the injected clock, stored server-side.
func (h *companySignInHarness) assertSessionMatchesThePasswordLoginContract(t *testing.T, out application.CompanySignInOutcome) {
	t.Helper()
	if out.Session == nil {
		t.Fatal("no session issued")
	}
	if len(out.Session.ID) != 64 {
		t.Errorf("session id %q has %d chars, want 64 (32 random bytes hex)", out.Session.ID, len(out.Session.ID))
	}
	if _, err := hex.DecodeString(out.Session.ID); err != nil {
		t.Errorf("session id %q is not hex: %v", out.Session.ID, err)
	}
	if want := h.clock.Now().Add(application.SessionTTL); !out.Session.ExpiresAt.Equal(want) {
		t.Errorf("session expiry = %v, want %v (SessionTTL from the injected clock)", out.Session.ExpiresAt, want)
	}
	stored, err := h.sessions.GetByID(context.Background(), out.Session.ID)
	if err != nil {
		t.Fatalf("stored session: %v", err)
	}
	if stored.UserID != out.User.ID {
		t.Errorf("stored session belongs to user %d, want %d", stored.UserID, out.User.ID)
	}
}

// TestCompanySignInBeginStoresOneAttempt proves the start step: one attempt with
// a fresh state, nonce and PKCE verifier, a ten-minute deadline, expired
// attempts purged first, and the state answered to the caller.
func TestCompanySignInBeginStoresOneAttempt(t *testing.T) {
	h := newCompanySignInHarness()
	start := h.start(t, application.ProviderGoogle)

	if start.URL == "" || start.State == "" {
		t.Fatalf("start = %+v, want a URL and a state", start)
	}
	if len(start.State) != 64 {
		t.Errorf("state %q has %d chars, want 64 (32 random bytes hex)", start.State, len(start.State))
	}
	if len(h.identities.attempts) != 1 {
		t.Fatalf("stored attempts = %d, want 1", len(h.identities.attempts))
	}
	var attempt application.CompanySignInAttempt
	for _, a := range h.identities.attempts {
		attempt = a
	}
	if attempt.Provider != application.ProviderGoogle {
		t.Errorf("attempt provider = %q, want google", attempt.Provider)
	}
	if len(attempt.Nonce) != 64 || len(attempt.CodeVerifier) < 43 || len(attempt.CodeVerifier) > 128 {
		t.Errorf("attempt nonce/verifier lengths = %d/%d, want 64 and 43..128", len(attempt.Nonce), len(attempt.CodeVerifier))
	}
	if !attempt.CreatedAt.Equal(h.clock.Now()) {
		t.Errorf("attempt created at %v, want the injected clock %v", attempt.CreatedAt, h.clock.Now())
	}
	if want := h.clock.Now().Add(application.CompanySignInAttemptTTL); !attempt.ExpiresAt.Equal(want) {
		t.Errorf("attempt expires at %v, want %v", attempt.ExpiresAt, want)
	}
	if len(h.identities.purges) != 1 || !h.identities.purges[0].Equal(h.clock.Now()) {
		t.Errorf("purges = %v, want one purge at the injected clock", h.identities.purges)
	}
	// The state itself is never stored: the flow keeps a hash of it.
	if _, storedUnderState := h.identities.attempts[start.State]; storedUnderState {
		t.Error("the attempt is keyed by the raw state, want its SHA-256 hash")
	}

	got := h.provider.requests[0]
	if got.State != start.State || got.Nonce != attempt.Nonce || got.CodeVerifier != attempt.CodeVerifier {
		t.Errorf("provider request = %+v, want the attempt's state, nonce and verifier", got)
	}
}

// TestCompanySignInBeginSendsTheDomainHintForOneGoogleDomain proves the optional
// hd hint: one configured domain is hinted, several are not, and Microsoft is
// never hinted.
func TestCompanySignInBeginSendsTheDomainHintForOneGoogleDomain(t *testing.T) {
	h := newCompanySignInHarness()
	h.start(t, application.ProviderGoogle)
	if got := h.provider.requests[0].DomainHint; got != "example.com" {
		t.Errorf("google hd hint = %q, want example.com", got)
	}

	h.settings.settings.Google.Domains = []string{"example.com", "example.org"}
	h.start(t, application.ProviderGoogle)
	if got := h.provider.requests[1].DomainHint; got != "" {
		t.Errorf("hd hint with two domains = %q, want none", got)
	}

	h.start(t, application.ProviderMicrosoft)
	if got := h.provider.requests[2].DomainHint; got != "" {
		t.Errorf("microsoft hd hint = %q, want none", got)
	}
}

// TestCompanySignInBeginRefusesAProviderThatIsOff proves an instance with no
// provider configured hands out no authorization URL (S1): the default stays
// password sign-in only.
func TestCompanySignInBeginRefusesAProviderThatIsOff(t *testing.T) {
	h := newCompanySignInHarness()
	h.settings.settings.Google.Enabled = false

	start, err := h.service.Begin(context.Background(), application.ProviderGoogle)
	var refusal *application.CompanySignInRefusal
	if !errors.As(err, &refusal) {
		t.Fatalf("Begin with the provider off = %v, want a CompanySignInRefusal", err)
	}
	if refusal.Reason != application.CompanySignInProviderOff {
		t.Errorf("refusal = %q, want provider_off", refusal.Reason)
	}
	if start.URL != "" || start.State != "" {
		t.Errorf("start = %+v, want nothing handed out", start)
	}
	if len(h.identities.attempts) != 0 {
		t.Errorf("stored attempts = %d, want none", len(h.identities.attempts))
	}
}

// TestCompanySignInCallbackMustMatchTheCookieState proves the callback is
// answered only when the state the browser carried and the state the provider
// echoed are the same non-empty value: half a flow is not a sign-in.
func TestCompanySignInCallbackMustMatchTheCookieState(t *testing.T) {
	cases := []struct {
		name        string
		cookieState string
		queryState  string
	}{
		{"different states", "state-a", "state-b"},
		{"no cookie state", "", "state-a"},
		{"no query state", "state-a", ""},
		{"both empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newCompanySignInHarness()
			h.provider.claims = googleClaims("sub-1", "ana@example.com")
			out, err := h.service.Complete(context.Background(), application.CompanySignInCallback{
				Provider: application.ProviderGoogle, CookieState: tc.cookieState, QueryState: tc.queryState, Code: "auth-code",
			})
			if err != nil {
				t.Fatalf("Complete: %v", err)
			}
			assertRefused(t, out, application.CompanySignInInvalidCallback)
			if len(h.provider.codes) != 0 {
				t.Error("the code was exchanged for a callback that did not match its cookie state")
			}
			if len(h.users.users) != 0 {
				t.Error("an account was created for a callback that did not match its cookie state")
			}
		})
	}
}

// TestCompanySignInAttemptIsSingleUse proves a callback consumes its attempt:
// the same state answered twice exchanges the code once, and an unknown state is
// refused.
func TestCompanySignInAttemptIsSingleUse(t *testing.T) {
	h := newCompanySignInHarness()
	h.provider.claims = googleClaims("sub-1", "ana@example.com")
	start := h.start(t, application.ProviderGoogle)

	first := h.complete(t, application.ProviderGoogle, start)
	if first.Kind != application.CompanySignInSignedIn {
		t.Fatalf("first callback = %q/%q, want signed_in", first.Kind, first.Reason)
	}
	second := h.complete(t, application.ProviderGoogle, start)
	assertRefused(t, second, application.CompanySignInInvalidCallback)
	if len(h.provider.codes) != 1 {
		t.Errorf("code exchanges = %d, want 1 (the attempt is single use)", len(h.provider.codes))
	}

	unknown, err := h.service.Complete(context.Background(), application.CompanySignInCallback{
		Provider: application.ProviderGoogle, CookieState: "unknown-state", QueryState: "unknown-state", Code: "auth-code",
	})
	if err != nil {
		t.Fatalf("Complete with an unknown state: %v", err)
	}
	assertRefused(t, unknown, application.CompanySignInInvalidCallback)
}

// TestCompanySignInExpiredAttemptIsRefused proves the attempt's deadline: past
// ten minutes the callback is refused, and the provider is not asked anything.
func TestCompanySignInExpiredAttemptIsRefused(t *testing.T) {
	h := newCompanySignInHarness()
	h.provider.claims = googleClaims("sub-1", "ana@example.com")
	start := h.start(t, application.ProviderGoogle)

	h.clock.Advance(application.CompanySignInAttemptTTL)
	out := h.complete(t, application.ProviderGoogle, start)
	assertRefused(t, out, application.CompanySignInInvalidCallback)
	if len(h.provider.codes) != 0 {
		t.Error("the code was exchanged for an expired attempt")
	}
}

// TestCompanySignInCallbackMustComeFromTheAttemptProvider proves one provider's
// callback cannot complete the other provider's attempt.
func TestCompanySignInCallbackMustComeFromTheAttemptProvider(t *testing.T) {
	h := newCompanySignInHarness()
	h.provider.claims = microsoftClaims("sub-1", "ana@example.com")
	start := h.start(t, application.ProviderGoogle)

	out := h.complete(t, application.ProviderMicrosoft, start)
	assertRefused(t, out, application.CompanySignInInvalidCallback)
	if len(h.provider.codes) != 0 {
		t.Error("a Microsoft callback exchanged the code of a Google attempt")
	}
}

// TestCompanySignInRefusesWhenTheProviderIsTurnedOffMidFlow proves a provider
// disabled between the start and the callback refuses the callback instead of
// signing anyone in.
func TestCompanySignInRefusesWhenTheProviderIsTurnedOffMidFlow(t *testing.T) {
	h := newCompanySignInHarness()
	h.provider.claims = googleClaims("sub-1", "ana@example.com")
	start := h.start(t, application.ProviderGoogle)
	h.settings.settings.Google.Enabled = false

	out := h.complete(t, application.ProviderGoogle, start)
	assertRefused(t, out, application.CompanySignInProviderOff)
	if len(h.users.users) != 0 {
		t.Error("an account was created for a provider that was turned off")
	}
}

// TestCompanySignInRefusesARejectedProviderAnswer proves a provider answer the
// client refuses ends as an invalid callback, while an operational failure is an
// error the caller can report and never a refusal.
func TestCompanySignInRefusesARejectedProviderAnswer(t *testing.T) {
	h := newCompanySignInHarness()
	h.provider.exchangeErr = application.ErrCompanyProviderRejected
	out := h.complete(t, application.ProviderGoogle, h.start(t, application.ProviderGoogle))
	assertRefused(t, out, application.CompanySignInInvalidCallback)
	if len(h.users.users) != 0 {
		t.Error("an account was created from a rejected provider answer")
	}

	h = newCompanySignInHarness()
	start := h.start(t, application.ProviderGoogle)
	h.provider.exchangeErr = errors.New("provider unreachable")
	if _, err := h.service.Complete(context.Background(), application.CompanySignInCallback{
		Provider: application.ProviderGoogle, CookieState: start.State, QueryState: start.State, Code: "auth-code",
	}); err == nil {
		t.Fatal("Complete with an operational exchange failure = nil error, want the failure")
	} else if errors.Is(err, application.ErrCompanyProviderRejected) {
		t.Errorf("operational failure = %v, want it told apart from a rejected answer", err)
	}
}

// TestCompanySignInRefusesAGoogleAccountOutsideTheDomains proves S2: a hosted
// domain the administrator did not list is refused, no account is created, and
// no session is issued.
func TestCompanySignInRefusesAGoogleAccountOutsideTheDomains(t *testing.T) {
	h := newCompanySignInHarness()
	claims := googleClaims("sub-1", "ana@elsewhere.com")
	claims.HostedDomain = "elsewhere.com"

	out := h.signIn(t, application.ProviderGoogle, claims)
	assertRefused(t, out, application.CompanySignInOutsideDomain)
	if len(h.users.users) != 0 {
		t.Error("an account was created for a domain outside the configured list")
	}

	h = newCompanySignInHarness()
	claims = googleClaims("sub-1", "ana@example.com")
	claims.HostedDomain = ""
	out = h.signIn(t, application.ProviderGoogle, claims)
	assertRefused(t, out, application.CompanySignInOutsideDomain)
}

// TestCompanySignInRefusesAGoogleEmailTheProviderDoesNotVouchFor proves S2's
// other half: the domain restriction applies to a verified email only.
func TestCompanySignInRefusesAGoogleEmailTheProviderDoesNotVouchFor(t *testing.T) {
	h := newCompanySignInHarness()
	claims := googleClaims("sub-1", "ana@example.com")
	claims.EmailVerified = false

	assertRefused(t, h.signIn(t, application.ProviderGoogle, claims), application.CompanySignInUnverifiedEmail)
	if len(h.users.users) != 0 {
		t.Error("an account was created for an unverified email")
	}
}

// TestCompanySignInRefusesAMicrosoftAccountOutsideTheTenant proves S2 for
// Microsoft: the tenant claim must be the configured organization.
func TestCompanySignInRefusesAMicrosoftAccountOutsideTheTenant(t *testing.T) {
	h := newCompanySignInHarness()
	claims := microsoftClaims("sub-1", "ana@example.com")
	claims.TenantID = "11111111-2222-3333-4444-555555555555"

	assertRefused(t, h.signIn(t, application.ProviderMicrosoft, claims), application.CompanySignInOutsideTenant)
	if len(h.users.users) != 0 {
		t.Error("an account was created for another organization's account")
	}
}

// TestCompanySignInCreatesARequesterAccount proves S3 and S9: a first sign-in
// with no matching account creates an active requester with no password, links
// the provider identity, and issues the session a password login would.
func TestCompanySignInCreatesARequesterAccount(t *testing.T) {
	h := newCompanySignInHarness()
	out := h.signIn(t, application.ProviderGoogle, googleClaims("sub-1", "Ana@Example.com"))

	if out.Kind != application.CompanySignInSignedIn {
		t.Fatalf("outcome = %q/%q, want signed_in", out.Kind, out.Reason)
	}
	if out.User == nil {
		t.Fatal("no account answered")
	}
	if out.User.Role != domain.RoleUser {
		t.Errorf("created role = %q, want user (nobody becomes agent or admin automatically)", out.User.Role)
	}
	if !out.User.Active || out.User.Pending {
		t.Errorf("created account active/pending = %v/%v, want true/false", out.User.Active, out.User.Pending)
	}
	if out.User.PasswordHash != "" {
		t.Errorf("created account has password hash %q, want none", out.User.PasswordHash)
	}
	if out.User.Email != "ana@example.com" {
		t.Errorf("created email = %q, want the provider's address normalized", out.User.Email)
	}
	if len(h.identities.identities) != 1 {
		t.Fatalf("linked identities = %d, want 1", len(h.identities.identities))
	}
	identity := h.identities.identities[0]
	if identity.UserID != out.User.ID || identity.Provider != application.ProviderGoogle ||
		identity.Issuer != "https://accounts.google.com" || identity.Subject != "sub-1" {
		t.Errorf("identity = %+v, want it linked to the created account", identity)
	}
	h.assertSessionMatchesThePasswordLoginContract(t, out)
	if len(h.notifier.accounts) != 0 {
		t.Errorf("approval announcements = %d, want 0 for a requester", len(h.notifier.accounts))
	}
}

// TestCompanySignInCreatesAPendingAccountWhenThePolicySaysSo proves the pending
// policy (S3): the account is inactive and pending, no session is issued, the
// approval seam is told once, and a second sign-in still gets the pending
// screen.
func TestCompanySignInCreatesAPendingAccountWhenThePolicySaysSo(t *testing.T) {
	h := newCompanySignInHarness()
	h.settings.settings.NewAccounts = application.NewAccountsPending

	out := h.signIn(t, application.ProviderGoogle, googleClaims("sub-1", "ana@example.com"))
	if out.Kind != application.CompanySignInPending {
		t.Fatalf("outcome = %q/%q, want pending", out.Kind, out.Reason)
	}
	if out.Session != nil {
		t.Errorf("pending outcome carried session %q, want none", out.Session.ID)
	}
	if out.User == nil || !out.User.Pending || out.User.Active {
		t.Fatalf("pending account = %+v, want pending and inactive", out.User)
	}
	if out.User.Role != domain.RoleUser {
		t.Errorf("pending role = %q, want user", out.User.Role)
	}
	if len(h.notifier.accounts) != 1 || h.notifier.accounts[0].ID != out.User.ID {
		t.Errorf("approval announcements = %+v, want one about the created account", h.notifier.accounts)
	}
	if len(h.sessions.sessions) != 0 {
		t.Error("a session was issued to a pending account")
	}

	again := h.signIn(t, application.ProviderGoogle, googleClaims("sub-1", "ana@example.com"))
	if again.Kind != application.CompanySignInPending {
		t.Fatalf("second sign-in = %q/%q, want pending", again.Kind, again.Reason)
	}
	if len(h.notifier.accounts) != 1 {
		t.Errorf("approval announcements after the second sign-in = %d, want 1 (announced on creation)", len(h.notifier.accounts))
	}
	if len(h.sessions.sessions) != 0 {
		t.Error("a session was issued on the second pending sign-in")
	}
}

// TestCompanySignInPendingAccountNeverGetsASession proves S3's acceptance for an
// account that was already pending before the provider was configured: the
// sign-in answers the pending screen, links nothing, and issues no session.
func TestCompanySignInPendingAccountNeverGetsASession(t *testing.T) {
	h := newCompanySignInHarness()
	waiting := h.users.seedPending("Ana", "ana@example.com")

	out := h.signIn(t, application.ProviderGoogle, googleClaims("sub-1", "ana@example.com"))
	if out.Kind != application.CompanySignInPending || out.User == nil || out.User.ID != waiting.ID {
		t.Fatalf("outcome = %q/%+v, want the pending account %d", out.Kind, out.User, waiting.ID)
	}
	if len(h.sessions.sessions) != 0 {
		t.Error("a session was issued to a pending account")
	}
	if len(h.identities.identities) != 0 {
		t.Error("a pending account was linked before an administrator approved it")
	}
	if len(h.notifier.accounts) != 0 {
		t.Errorf("approval announcements = %d, want 0 for an account that already existed", len(h.notifier.accounts))
	}
}

// TestCompanySignInLinksAnExistingAccountByEmail proves S7's first half: an
// account with the same email is linked on its first company sign-in and keeps
// its role, whatever the case of the stored address.
func TestCompanySignInLinksAnExistingAccountByEmail(t *testing.T) {
	h := newCompanySignInHarness()
	ana := h.users.seedRole("Ana", "Ana@Example.com", domain.RoleAgent, true)

	out := h.signIn(t, application.ProviderGoogle, googleClaims("sub-1", "ana@example.com"))
	if out.Kind != application.CompanySignInSignedIn {
		t.Fatalf("outcome = %q/%q, want signed_in", out.Kind, out.Reason)
	}
	if out.User.ID != ana.ID {
		t.Errorf("signed in account = %d, want the existing account %d", out.User.ID, ana.ID)
	}
	if out.User.Role != domain.RoleAgent {
		t.Errorf("role after linking = %q, want the kept role agent", out.User.Role)
	}
	if len(h.identities.identities) != 1 || h.identities.identities[0].UserID != ana.ID {
		t.Errorf("identities = %+v, want one linked to the existing account", h.identities.identities)
	}
	h.assertSessionMatchesThePasswordLoginContract(t, out)
}

// TestCompanySignInKeepsTheLinkedAccountWhenTheProviderEmailChanges proves S7's
// acceptance: once identified by issuer and subject, a changed email at the
// provider reaches the same account instead of creating a second one.
func TestCompanySignInKeepsTheLinkedAccountWhenTheProviderEmailChanges(t *testing.T) {
	h := newCompanySignInHarness()
	first := h.signIn(t, application.ProviderGoogle, googleClaims("sub-1", "ana@example.com"))
	if first.Kind != application.CompanySignInSignedIn {
		t.Fatalf("first sign-in = %q/%q, want signed_in", first.Kind, first.Reason)
	}

	second := h.signIn(t, application.ProviderGoogle, googleClaims("sub-1", "ana.married@example.com"))
	if second.Kind != application.CompanySignInSignedIn {
		t.Fatalf("second sign-in = %q/%q, want signed_in", second.Kind, second.Reason)
	}
	if second.User.ID != first.User.ID {
		t.Errorf("second sign-in account = %d, want the same account %d", second.User.ID, first.User.ID)
	}
	if len(h.users.users) != 1 {
		t.Errorf("accounts = %d, want 1 (a changed provider email creates no second account)", len(h.users.users))
	}
	if len(h.identities.identities) != 1 {
		t.Fatalf("identities = %d, want 1", len(h.identities.identities))
	}
	if got := h.identities.identities[0].Email; got != "ana.married@example.com" {
		t.Errorf("identity email = %q, want the address last seen at the provider", got)
	}
	if !h.identities.identities[0].LastSignInAt.Equal(h.clock.Now()) {
		t.Errorf("identity last sign-in = %v, want the injected clock", h.identities.identities[0].LastSignInAt)
	}
}

// TestCompanySignInRefusesADeactivatedAccountWithoutLinking proves S8: a
// deactivated account is refused, is not reactivated, and gains no identity
// that would let the provider keep trying.
func TestCompanySignInRefusesADeactivatedAccountWithoutLinking(t *testing.T) {
	h := newCompanySignInHarness()
	h.users.seedRole("Ana", "ana@example.com", domain.RoleAgent, false)

	assertRefused(t, h.signIn(t, application.ProviderGoogle, googleClaims("sub-1", "ana@example.com")), application.CompanySignInDeactivated)
	if len(h.identities.identities) != 0 {
		t.Error("a deactivated account was linked")
	}
	if len(h.sessions.sessions) != 0 {
		t.Error("a session was issued to a deactivated account")
	}
	for _, u := range h.users.users {
		if u.Active {
			t.Error("a deactivated account was reactivated by a provider sign-in")
		}
	}
}

// TestCompanySignInRefusesAnAmbiguousEmail proves the flow does not choose
// between two accounts that hold the same address in different case.
func TestCompanySignInRefusesAnAmbiguousEmail(t *testing.T) {
	h := newCompanySignInHarness()
	h.users.seedRole("Ana", "ana@example.com", domain.RoleAgent, true)
	h.users.seedRole("Ana", "Ana@Example.com", domain.RoleUser, true)

	assertRefused(t, h.signIn(t, application.ProviderGoogle, googleClaims("sub-1", "ana@example.com")), application.CompanySignInAmbiguousEmail)
	if len(h.identities.identities) != 0 {
		t.Error("an ambiguous email linked an identity")
	}
	if len(h.sessions.sessions) != 0 {
		t.Error("an ambiguous email issued a session")
	}
}

// TestCompanySignInRefusesAnEmailLinkedToAnotherIdentity proves the second
// identity of one provider cannot claim an account the first already reaches.
func TestCompanySignInRefusesAnEmailLinkedToAnotherIdentity(t *testing.T) {
	h := newCompanySignInHarness()
	ana := h.users.seedRole("Ana", "ana@example.com", domain.RoleAgent, true)
	h.identities.seedIdentity(ana.ID, application.ProviderGoogle, "https://accounts.google.com", "sub-1", "ana@example.com")

	assertRefused(t, h.signIn(t, application.ProviderGoogle, googleClaims("sub-2", "ana@example.com")), application.CompanySignInEmailAlreadyLinked)
	if len(h.identities.identities) != 1 {
		t.Errorf("identities = %d, want the one that was already linked", len(h.identities.identities))
	}
}

// TestCompanySignInRetriesTheLookupWhenAnotherSignInWinsTheRace proves the
// concurrent-duplicate rule: when the account and identity were created between
// the lookup and the write, the flow repeats the lookup and signs in the account
// that now exists instead of failing or creating a second one.
func TestCompanySignInRetriesTheLookupWhenAnotherSignInWinsTheRace(t *testing.T) {
	h := newCompanySignInHarness()
	winner := h.users.seedRole("Ana", "ana@example.com", domain.RoleAgent, true)
	h.identities.seedIdentity(winner.ID, application.ProviderGoogle, "https://accounts.google.com", "sub-1", "ana@example.com")
	h.identities.failNextCreate = true

	out := h.signIn(t, application.ProviderGoogle, googleClaims("sub-1", "ana@example.com"))
	if out.Kind != application.CompanySignInSignedIn {
		t.Fatalf("outcome = %q/%q, want signed_in from the retried lookup", out.Kind, out.Reason)
	}
	if out.User.ID != winner.ID {
		t.Errorf("signed in account = %d, want the account created by the winning sign-in %d", out.User.ID, winner.ID)
	}
	if len(h.users.users) != 1 {
		t.Errorf("accounts = %d, want 1 (the losing sign-in creates nothing)", len(h.users.users))
	}
	if len(h.identities.identities) != 1 {
		t.Errorf("identities = %d, want 1", len(h.identities.identities))
	}
}

// TestCompanySignInPassesTheAttemptValuesToTheProvider proves the callback hands
// the provider the attempt's nonce, PKCE verifier and code, so the adapter can
// bind the ID token to this flow.
func TestCompanySignInPassesTheAttemptValuesToTheProvider(t *testing.T) {
	h := newCompanySignInHarness()
	h.provider.claims = googleClaims("sub-1", "ana@example.com")
	start := h.start(t, application.ProviderGoogle)
	var attempt application.CompanySignInAttempt
	for _, a := range h.identities.attempts {
		attempt = a
	}

	h.complete(t, application.ProviderGoogle, start)
	got := h.provider.requests[len(h.provider.requests)-1]
	if got.State != start.State {
		t.Errorf("exchange state = %q, want the flow state", got.State)
	}
	if got.Nonce != attempt.Nonce || got.CodeVerifier != attempt.CodeVerifier {
		t.Errorf("exchange request = %+v, want the attempt's nonce and verifier", got)
	}
	if len(h.provider.codes) != 1 || h.provider.codes[0] != "auth-code" {
		t.Errorf("exchanged codes = %v, want the callback's code", h.provider.codes)
	}
}

// TestCompanySignInSessionIsNotIssuedForRefusals proves the single rule the
// refusals share: no refusal leaves a session behind, whatever its reason.
func TestCompanySignInSessionIsNotIssuedForRefusals(t *testing.T) {
	cases := []struct {
		name    string
		claims  application.CompanyIdentityClaims
		prepare func(*companySignInHarness)
		reason  application.CompanySignInReason
	}{
		{
			name: "outside the domain",
			claims: func() application.CompanyIdentityClaims {
				c := googleClaims("sub-1", "ana@elsewhere.com")
				c.HostedDomain = "elsewhere.com"
				return c
			}(),
			reason: application.CompanySignInOutsideDomain,
		},
		{
			name: "outside the tenant",
			claims: func() application.CompanyIdentityClaims {
				c := microsoftClaims("sub-1", "ana@example.com")
				c.TenantID = "11111111-2222-3333-4444-555555555555"
				return c
			}(),
			reason: application.CompanySignInOutsideTenant,
		},
		{
			name: "unverified email",
			claims: func() application.CompanyIdentityClaims {
				c := googleClaims("sub-1", "ana@example.com")
				c.EmailVerified = false
				return c
			}(),
			reason: application.CompanySignInUnverifiedEmail,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newCompanySignInHarness()
			provider := application.ProviderGoogle
			if tc.reason == application.CompanySignInOutsideTenant {
				provider = application.ProviderMicrosoft
			}
			assertRefused(t, h.signIn(t, provider, tc.claims), tc.reason)
			if len(h.sessions.sessions) != 0 {
				t.Errorf("sessions after a refusal = %d, want 0", len(h.sessions.sessions))
			}
		})
	}
}

// TestCompanySignInOperationalFailureIsReportedNotRefused proves a failure while
// reading the configuration is reported as an error, never turned into a
// refusal that hides it.
func TestCompanySignInOperationalFailureIsReportedNotRefused(t *testing.T) {
	h := newCompanySignInHarness()
	start := h.start(t, application.ProviderGoogle)
	h.settings.getErr = errors.New("settings unavailable")
	if _, err := h.service.Begin(context.Background(), application.ProviderGoogle); err == nil {
		t.Fatal("Begin with unavailable settings = nil error, want the failure")
	} else if strings.Contains(err.Error(), "refused") {
		t.Errorf("Begin error = %v, want the operational failure, not a refusal", err)
	}
	if _, err := h.service.Complete(context.Background(), application.CompanySignInCallback{
		Provider: application.ProviderGoogle, CookieState: start.State, QueryState: start.State, Code: "auth-code",
	}); err == nil {
		t.Fatal("Complete with unavailable settings = nil error, want the failure")
	}
}
