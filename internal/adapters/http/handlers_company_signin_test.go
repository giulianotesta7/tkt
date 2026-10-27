package httpadapter

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
	"github.com/giulianotesta7/tkt/internal/i18n"
)

// Company sign-in at the HTTP surface (issue #419, S11): the public start and
// callback routes, the cookie that binds an attempt to the browser that started
// it, the sign-in page buttons and their states, and the pending screen.
//
// Everything below runs over the real configuration service, the real identity
// store, the real session store, and the real templates; only the provider is
// faked, because this layer never opens a socket. Every test drives the browser's
// half of the flow explicitly — the start route, the state cookie it set, and
// the callback the provider redirects to — so no assertion depends on a network
// round trip or a sleep.

const (
	// companyAddress is the configured public address of the instance under
	// test. It is deliberately NOT the request's host: the redirect URI is built
	// from this value, never from a Host header (S11).
	companyAddress = "https://help.example.com"
	// companyTenant is the Microsoft directory the configuration allows.
	companyTenant = "0f8fad5b-d9cb-469f-a165-70867728950e"
	// companyAuthURL is the fake provider's authorization endpoint.
	companyAuthURL = "https://provider.example.com/authorize"
)

// fakeCompanyProvider is the CompanyIdentityProvider seam of this layer: it
// answers the authorization URL the flow hands the browser, and the verified
// claims the provider's code exchange would produce. It records what the flow
// presented, so a test can prove the redirect URI, the opened client secret, the
// PKCE verifier, and the code actually travelled.
type fakeCompanyProvider struct {
	// claims is what a successful exchange answers.
	claims application.CompanyIdentityClaims
	// exchangeErr, when set, is answered instead of claims.
	exchangeErr error
	// exchanges counts the redemptions that reached the provider, so a refused
	// callback can prove it never got that far.
	exchanges int
	// lastCreds, lastRequest and lastCode are the values of the last exchange.
	lastCreds   application.CompanyProviderCredentials
	lastRequest application.CompanyAuthorizationRequest
	lastCode    string
}

var _ application.CompanyIdentityProvider = (*fakeCompanyProvider)(nil)

func (f *fakeCompanyProvider) AuthorizationURL(_ context.Context, creds application.CompanyProviderCredentials, req application.CompanyAuthorizationRequest) (string, error) {
	f.lastCreds, f.lastRequest = creds, req
	return companyAuthURL + "?state=" + url.QueryEscape(req.State) + "&nonce=" + url.QueryEscape(req.Nonce), nil
}

func (f *fakeCompanyProvider) Exchange(_ context.Context, creds application.CompanyProviderCredentials, req application.CompanyAuthorizationRequest, code string) (application.CompanyIdentityClaims, error) {
	f.exchanges++
	f.lastCreds, f.lastRequest, f.lastCode = creds, req, code
	if f.exchangeErr != nil {
		return application.CompanyIdentityClaims{}, f.exchangeErr
	}
	claims := f.claims
	// A verified token echoes the nonce the attempt carried; the fake mirrors
	// that contract so the recorded claims look like a real answer.
	if claims.Nonce == "" {
		claims.Nonce = req.Nonce
	}
	return claims, nil
}

// companySignInIn is one complete company sign-in submission with both providers
// enabled: the address the redirect URIs are built from, one allowed Google
// domain, the Microsoft tenant, and a client secret per provider.
func companySignInIn(newAccounts application.NewAccountPolicy) application.CompanySignInInput {
	return application.CompanySignInInput{
		Address:               companyAddress,
		NewAccounts:           newAccounts,
		PasswordSignInEnabled: true,
		Google: application.GoogleSignInInput{
			Enabled: true, ClientID: "google-client-id", ClientSecret: "google-client-secret", Domains: "example.com",
		},
		Microsoft: application.MicrosoftSignInInput{
			Enabled: true, TenantID: companyTenant, ClientID: "microsoft-client-id", ClientSecret: "microsoft-client-secret",
		},
	}
}

// companySignInStore saves one submission through the real settings service, so
// the client secrets are sealed exactly as production seals them.
func companySignInStore(t *testing.T, h *harness, in application.CompanySignInInput) {
	t.Helper()
	if err := h.companySettings.Update(context.Background(), *h.admin, in); err != nil {
		t.Fatalf("store company sign-in settings: %v", err)
	}
}

// googleClaims is a verified Google claim set for one address.
func googleClaims(email string) application.CompanyIdentityClaims {
	return application.CompanyIdentityClaims{
		Issuer:        "https://accounts.google.com",
		Subject:       "google-subject-1",
		Email:         email,
		EmailVerified: true,
		HostedDomain:  "example.com",
		Name:          "Ana Torres",
	}
}

// companyStart runs the start route anonymously and answers the state cookie it
// set plus where it sent the browser.
func companyStart(t *testing.T, h *harness, provider string) (*http.Cookie, string) {
	t.Helper()
	rec := doRequest(h.mux, h.mw, http.MethodGet, "/auth/"+provider+"/start", nil)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("GET /auth/%s/start = %d, want 303: %s", provider, rec.Code, rec.Body.String())
	}
	cookie := findCookie(rec, companyStateCookie)
	if cookie == nil || cookie.Value == "" {
		t.Fatalf("the start route set no %s cookie", companyStateCookie)
	}
	return cookie, rec.Header().Get("Location")
}

// companyCallback runs the callback the provider redirects to, carrying the
// browser's state cookie and the state the provider echoed.
func companyCallback(t *testing.T, h *harness, provider, cookieState, queryState string) *httptest.ResponseRecorder {
	t.Helper()
	target := "/auth/" + provider + "/callback?code=the-code"
	if queryState != "" {
		target += "&state=" + url.QueryEscape(queryState)
	}
	req := httptest.NewRequest(http.MethodGet, target, nil)
	if cookieState != "" {
		req.AddCookie(&http.Cookie{Name: companyStateCookie, Value: cookieState})
	}
	rec := httptest.NewRecorder()
	h.mw.Wrap(h.mux).ServeHTTP(rec, req)
	return rec
}

// storedUserCount reads the users table directly: how many accounts exist is a
// persistence fact no user store port counts (yet).
func storedUserCount(t *testing.T, h *harness) int64 {
	t.Helper()
	return scanOneInt(t, h.rawDB(t), `SELECT COUNT(*) FROM users`)
}

// storedAttemptCount reads the in-flight attempt rows directly: the flow's
// single-use bookkeeping has no counting read.
func storedAttemptCount(t *testing.T, h *harness) int64 {
	t.Helper()
	return scanOneInt(t, h.rawDB(t), `SELECT COUNT(*) FROM oidc_sign_in_attempts`)
}

// TestCompanyStartBindsTheAttemptToTheBrowser proves the start route: the
// browser is sent to the provider with the state the flow minted, and the cookie
// that comes back with it is unreadable by script, bound to that provider's
// callback, short-lived, and same-site-lax so the provider's redirect carries it.
// It also proves the redirect URI comes from the configured address and not from
// the request's Host header (S11).
func TestCompanyStartBindsTheAttemptToTheBrowser(t *testing.T) {
	h := newHarness(t)
	companySignInStore(t, h, companySignInIn(application.NewAccountsRequester))

	cookie, location := companyStart(t, h, "google")

	if !strings.HasPrefix(location, companyAuthURL+"?") {
		t.Fatalf("start sent the browser to %q, want the provider authorization endpoint", location)
	}
	if !strings.Contains(location, "state="+url.QueryEscape(cookie.Value)) {
		t.Errorf("the authorization URL must carry the state the cookie holds, got %q", location)
	}
	if !cookie.HttpOnly {
		t.Error("the state cookie must be HttpOnly")
	}
	if cookie.SameSite != http.SameSiteLaxMode {
		t.Errorf("state cookie SameSite = %v, want Lax", cookie.SameSite)
	}
	if cookie.Path != "/auth/google/callback" {
		t.Errorf("state cookie Path = %q, want /auth/google/callback", cookie.Path)
	}
	if want := int(application.CompanySignInAttemptTTL.Seconds()); cookie.MaxAge != want {
		t.Errorf("state cookie MaxAge = %d, want %d", cookie.MaxAge, want)
	}
	if cookie.Secure {
		t.Error("a plain-HTTP request must not set Secure on the state cookie")
	}
	if got, want := h.companyProvider.lastCreds.RedirectURI, companyAddress+"/auth/google/callback"; got != want {
		t.Errorf("redirect URI = %q, want the configured address %q", got, want)
	}
	if got := h.companyProvider.lastCreds.ClientSecret; got != "google-client-secret" {
		t.Errorf("the flow presented client secret %q, want the sealed stored one", got)
	}
	if h.companyProvider.lastRequest.CodeVerifier == "" || h.companyProvider.lastRequest.Nonce == "" {
		t.Error("the flow must carry a PKCE verifier and a nonce to the provider")
	}
	if got := storedAttemptCount(t, h); got != 1 {
		t.Errorf("stored attempts = %d, want exactly 1", got)
	}
}

// TestCompanyCallbackSignsInAnExistingAccount proves S7 end to end: an account
// with the provider's address is signed in, is linked to the provider identity,
// and gets the session cookie a password login gets.
func TestCompanyCallbackSignsInAnExistingAccount(t *testing.T) {
	h := newHarness(t)
	companySignInStore(t, h, companySignInIn(application.NewAccountsRequester))
	ana := seedUser(t, h.store, "Ana", "ana@example.com")
	// The provider reports the address in another case: the flow links the one
	// account that holds it, not a second account spelled differently.
	h.companyProvider.claims = googleClaims("Ana@Example.com")

	cookie, _ := companyStart(t, h, "google")
	before := storedSessionCount(t, h)
	rec := companyCallback(t, h, "google", cookie.Value, cookie.Value)

	wantRedirect(t, rec, http.StatusSeeOther, "/tickets")
	session := findCookie(rec, sessionCookie)
	if session == nil || session.Value == "" {
		t.Fatal("the signed-in outcome issued no session cookie")
	}
	if got := storedSessionCount(t, h); got != before+1 {
		t.Errorf("sessions = %d, want exactly one more", got-before)
	}
	if got := h.companyProvider.lastCode; got != "the-code" {
		t.Errorf("the flow redeemed code %q, want the callback's code", got)
	}
	_, linked, err := h.store.CompanyIdentityStore().ResolveIdentity(context.Background(), application.ProviderGoogle,
		h.companyProvider.claims.Issuer, h.companyProvider.claims.Subject)
	if err != nil {
		t.Fatalf("resolve the linked identity: %v", err)
	}
	if linked.ID != ana.ID {
		t.Errorf("linked account id = %d, want the existing account %d", linked.ID, ana.ID)
	}
	if cleared := findCookie(rec, companyStateCookie); cleared == nil || cleared.MaxAge >= 0 {
		t.Error("the callback must spend the state cookie")
	}
	if got := storedAttemptCount(t, h); got != 0 {
		t.Errorf("attempts left = %d, want the attempt consumed", got)
	}
}

// TestCompanyCallbackIsSingleUse proves the replay rule (S11): the same callback
// answered twice issues one session and then refuses, leaving the stored data and
// the session count untouched.
func TestCompanyCallbackIsSingleUse(t *testing.T) {
	h := newHarness(t)
	companySignInStore(t, h, companySignInIn(application.NewAccountsRequester))
	seedUser(t, h.store, "Ana", "ana@example.com")
	h.companyProvider.claims = googleClaims("ana@example.com")

	cookie, _ := companyStart(t, h, "google")
	wantRedirect(t, companyCallback(t, h, "google", cookie.Value, cookie.Value), http.StatusSeeOther, "/tickets")
	sessions := storedSessionCount(t, h)

	replay := companyCallback(t, h, "google", cookie.Value, cookie.Value)

	if replay.Code != http.StatusBadRequest {
		t.Fatalf("replayed callback = %d, want 400: %s", replay.Code, replay.Body.String())
	}
	if !strings.Contains(replay.Body.String(), i18n.T(i18n.Default, "auth.company.refused_attempt")) {
		t.Errorf("the replay must render the neutral attempt sentence, got: %.2000s", replay.Body.String())
	}
	if c := findCookie(replay, sessionCookie); c != nil && c.Value != "" {
		t.Errorf("the replayed callback issued a session cookie %q, want none", c.Value)
	}
	if got := storedSessionCount(t, h); got != sessions {
		t.Errorf("sessions = %d, want the replayed callback to write none", got-sessions)
	}
}

// TestCompanyCallbackRefusesAMismatchedStateBeforeRedeeming proves the browser
// binding (S11): a callback whose state does not match the cookie is refused
// before the code is redeemed, so a forged callback can never spend a code.
func TestCompanyCallbackRefusesAMismatchedStateBeforeRedeeming(t *testing.T) {
	h := newHarness(t)
	companySignInStore(t, h, companySignInIn(application.NewAccountsRequester))
	seedUser(t, h.store, "Ana", "ana@example.com")
	h.companyProvider.claims = googleClaims("ana@example.com")

	cookie, _ := companyStart(t, h, "google")
	sessions := storedSessionCount(t, h)

	for name, attempt := range map[string]struct{ cookieState, queryState string }{
		"echoed state differs": {cookieState: cookie.Value, queryState: cookie.Value + "x"},
		"cookie missing":       {cookieState: "", queryState: cookie.Value},
		"state missing":        {cookieState: cookie.Value, queryState: ""},
	} {
		rec := companyCallback(t, h, "google", attempt.cookieState, attempt.queryState)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", name, rec.Code)
		}
		if c := findCookie(rec, sessionCookie); c != nil && c.Value != "" {
			t.Errorf("%s: issued session cookie %q, want none", name, c.Value)
		}
	}
	if h.companyProvider.exchanges != 0 {
		t.Errorf("the provider was asked to redeem %d codes, want none", h.companyProvider.exchanges)
	}
	if got := storedSessionCount(t, h); got != sessions {
		t.Errorf("sessions = %d, want none written", got-sessions)
	}
}

// TestCompanyCallbackRefusesAnExpiredAttempt proves the ten-minute window (S11):
// an attempt that ran out is refused without redeeming its code.
func TestCompanyCallbackRefusesAnExpiredAttempt(t *testing.T) {
	h := newHarness(t)
	companySignInStore(t, h, companySignInIn(application.NewAccountsRequester))
	seedUser(t, h.store, "Ana", "ana@example.com")
	h.companyProvider.claims = googleClaims("ana@example.com")

	cookie, _ := companyStart(t, h, "google")
	// The attempts table stores one row per authorization, so expiring every row
	// expires this attempt: the same comparison the flow makes against its clock.
	if _, err := h.rawDB(t).Exec(`UPDATE oidc_sign_in_attempts SET expires_at = ?`,
		fixedNow.Add(-time.Minute).UTC().Format(time.RFC3339)); err != nil {
		t.Fatalf("expire the attempt: %v", err)
	}

	rec := companyCallback(t, h, "google", cookie.Value, cookie.Value)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expired callback = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), i18n.T(i18n.Default, "auth.company.refused_attempt")) {
		t.Errorf("an expired attempt must render the neutral attempt sentence: %.2000s", rec.Body.String())
	}
	if h.companyProvider.exchanges != 0 {
		t.Errorf("the provider was asked to redeem %d codes, want none", h.companyProvider.exchanges)
	}
	if c := findCookie(rec, sessionCookie); c != nil && c.Value != "" {
		t.Errorf("the expired callback issued a session cookie %q, want none", c.Value)
	}
}

// TestCompanyCallbackRefusesAnAccountOutsideTheAllowedDomain proves S2 for
// Google: an address from another Workspace domain is refused with the neutral
// sentence, creates no account, and issues no session.
func TestCompanyCallbackRefusesAnAccountOutsideTheAllowedDomain(t *testing.T) {
	h := newHarness(t)
	companySignInStore(t, h, companySignInIn(application.NewAccountsRequester))
	h.companyProvider.claims = googleClaims("ana@other.example.com")
	h.companyProvider.claims.HostedDomain = "other.example.com"
	users, sessions := storedUserCount(t, h), storedSessionCount(t, h)

	cookie, _ := companyStart(t, h, "google")
	rec := companyCallback(t, h, "google", cookie.Value, cookie.Value)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("outside-domain callback = %d, want 403: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), i18n.T(i18n.Default, "auth.company.refused_domain")) {
		t.Errorf("the refusal must render the domain sentence, got: %.2000s", rec.Body.String())
	}
	if c := findCookie(rec, sessionCookie); c != nil && c.Value != "" {
		t.Errorf("the refused sign-in issued a session cookie %q, want none", c.Value)
	}
	if got := storedUserCount(t, h); got != users {
		t.Errorf("users = %d, want none created", got-users)
	}
	if got := storedSessionCount(t, h); got != sessions {
		t.Errorf("sessions = %d, want none created", got-sessions)
	}
}

// TestCompanyCallbackRefusesAnAccountOutsideTheTenant proves S2 for Microsoft:
// a token from another directory is refused with the organization sentence.
func TestCompanyCallbackRefusesAnAccountOutsideTheTenant(t *testing.T) {
	h := newHarness(t)
	companySignInStore(t, h, companySignInIn(application.NewAccountsRequester))
	h.companyProvider.claims = application.CompanyIdentityClaims{
		Issuer:   "https://login.microsoftonline.com/11111111-2222-3333-4444-555555555555/v2.0",
		Subject:  "microsoft-subject-1",
		Email:    "ana@other.example.com",
		TenantID: "11111111-2222-3333-4444-555555555555",
		Name:     "Ana Torres",
	}

	cookie, _ := companyStart(t, h, "microsoft")
	rec := companyCallback(t, h, "microsoft", cookie.Value, cookie.Value)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("outside-tenant callback = %d, want 403: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), i18n.T(i18n.Default, "auth.company.refused_tenant")) {
		t.Errorf("the refusal must render the organization sentence, got: %.2000s", rec.Body.String())
	}
	if c := findCookie(rec, sessionCookie); c != nil && c.Value != "" {
		t.Errorf("the refused sign-in issued a session cookie %q, want none", c.Value)
	}
	if got, want := h.companyProvider.lastCreds.RedirectURI, companyAddress+"/auth/microsoft/callback"; got != want {
		t.Errorf("redirect URI = %q, want the configured address %q", got, want)
	}
}

// TestCompanyCallbackCreatesARequester proves the new-account policy and S9: a
// first sign-in creates a requester with no password, never a privileged
// account, and signs it in.
func TestCompanyCallbackCreatesARequester(t *testing.T) {
	h := newHarness(t)
	companySignInStore(t, h, companySignInIn(application.NewAccountsRequester))
	h.companyProvider.claims = googleClaims("newcomer@example.com")

	cookie, _ := companyStart(t, h, "google")
	rec := companyCallback(t, h, "google", cookie.Value, cookie.Value)

	wantRedirect(t, rec, http.StatusSeeOther, "/tickets")
	_, created, err := h.store.CompanyIdentityStore().ResolveIdentity(context.Background(), application.ProviderGoogle,
		h.companyProvider.claims.Issuer, h.companyProvider.claims.Subject)
	if err != nil {
		t.Fatalf("resolve the created identity: %v", err)
	}
	if created.Role != domain.RoleUser {
		t.Errorf("created role = %q, want %q", created.Role, domain.RoleUser)
	}
	if created.Pending || !created.Active {
		t.Errorf("created account active=%v pending=%v, want an active requester", created.Active, created.Pending)
	}
	if created.PasswordHash != "" {
		t.Error("a company sign-in must not store a password the provider flow could not set")
	}
}

// TestCompanyCallbackPendingAccountGetsThePendingScreen is S3 at the surface: a
// pending first sign-in renders the pending card in the auth shell with the way
// back to the form, and issues no session at all.
func TestCompanyCallbackPendingAccountGetsThePendingScreen(t *testing.T) {
	h := newHarness(t)
	companySignInStore(t, h, companySignInIn(application.NewAccountsPending))
	h.companyProvider.claims = googleClaims("newcomer@example.com")
	sessions := storedSessionCount(t, h)

	cookie, _ := companyStart(t, h, "google")
	rec := companyCallback(t, h, "google", cookie.Value, cookie.Value)

	if rec.Code != http.StatusOK {
		t.Fatalf("pending callback = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		`<main class="auth-entry">`,
		i18n.T(i18n.Default, "auth.company.pending_title"),
		i18n.T(i18n.Default, "auth.company.pending_body"),
		i18n.T(i18n.Default, "auth.company.pending_note"),
		`href="/login"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the pending screen must render %q, got: %.2000s", want, body)
		}
	}
	// A pending account already holds the requester role: the screen must say
	// access awaits approval, never that the account has no role.
	if strings.Contains(body, "no role") {
		t.Errorf("the pending screen must not claim the account lacks a role, got: %.2000s", body)
	}
	if c := findCookie(rec, sessionCookie); c != nil && c.Value != "" {
		t.Errorf("a pending account must get no session cookie, got %q", c.Value)
	}
	if got := storedSessionCount(t, h); got != sessions {
		t.Errorf("sessions = %d, want none created for a pending account", got-sessions)
	}
	_, created, err := h.store.CompanyIdentityStore().ResolveIdentity(context.Background(), application.ProviderGoogle,
		h.companyProvider.claims.Issuer, h.companyProvider.claims.Subject)
	if err != nil {
		t.Fatalf("resolve the created identity: %v", err)
	}
	if !created.Pending || created.Active {
		t.Errorf("created account active=%v pending=%v, want a pending account that cannot sign in", created.Active, created.Pending)
	}
}

// TestCompanyCallbackRefusesADeactivatedAccount is S8: the provider never
// reactivates an account, so a deactivated one is refused and stays exactly as it
// was.
func TestCompanyCallbackRefusesADeactivatedAccount(t *testing.T) {
	h := newHarness(t)
	companySignInStore(t, h, companySignInIn(application.NewAccountsRequester))
	retired := &domain.User{Name: "Retired", Email: "retired@example.com", PasswordHash: "not-a-real-hash", Active: false, CreatedAt: fixedNow}
	if err := h.store.UserStore().Create(context.Background(), retired); err != nil {
		t.Fatalf("seed the deactivated account: %v", err)
	}
	h.companyProvider.claims = googleClaims("retired@example.com")
	sessions := storedSessionCount(t, h)

	cookie, _ := companyStart(t, h, "google")
	rec := companyCallback(t, h, "google", cookie.Value, cookie.Value)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("deactivated callback = %d, want 403: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), i18n.T(i18n.Default, "auth.company.refused_deactivated")) {
		t.Errorf("the refusal must render the deactivated sentence, got: %.2000s", rec.Body.String())
	}
	if got := storedSessionCount(t, h); got != sessions {
		t.Errorf("sessions = %d, want none created", got-sessions)
	}
	stored, err := h.store.UserStore().GetByID(context.Background(), retired.ID)
	if err != nil {
		t.Fatalf("read the refused account: %v", err)
	}
	if stored.Active {
		t.Error("signing in must never reactivate a deactivated account")
	}
}

// TestCompanyCallbackKeepsProviderTextOutOfEveryAnswer proves the failure
// hygiene of S11: neither a rejected answer nor an operational failure carries
// the provider's or the library's own words into the page, and only the
// operational one answers 500.
func TestCompanyCallbackKeepsProviderTextOutOfEveryAnswer(t *testing.T) {
	secret := "provider-secret-hunter2"
	for name, tc := range map[string]struct {
		err        error
		wantStatus int
	}{
		"rejected answer": {
			err:        fmt.Errorf("the token endpoint said %s: %w", secret, application.ErrCompanyProviderRejected),
			wantStatus: http.StatusBadRequest,
		},
		"operational failure": {
			err:        errors.New("the token endpoint said " + secret),
			wantStatus: http.StatusInternalServerError,
		},
	} {
		h := newHarness(t)
		companySignInStore(t, h, companySignInIn(application.NewAccountsRequester))
		seedUser(t, h.store, "Ana", "ana@example.com")
		h.companyProvider.claims = googleClaims("ana@example.com")
		h.companyProvider.exchangeErr = tc.err

		cookie, _ := companyStart(t, h, "google")
		rec := companyCallback(t, h, "google", cookie.Value, cookie.Value)

		if rec.Code != tc.wantStatus {
			t.Errorf("%s: status = %d, want %d", name, rec.Code, tc.wantStatus)
		}
		if strings.Contains(rec.Body.String(), secret) {
			t.Errorf("%s: the answer leaked the failure text: %.2000s", name, rec.Body.String())
		}
		if c := findCookie(rec, sessionCookie); c != nil && c.Value != "" {
			t.Errorf("%s: issued a session cookie %q, want none", name, c.Value)
		}
	}
}

// TestCompanyStartRefusesAProviderThatIsOff proves the fail-closed start (S11):
// a provider the instance did not configure answers the neutral "not available"
// sentence and hands out no attempt, and a segment outside the closed provider
// list is not a sign-in route at all.
func TestCompanyStartRefusesAProviderThatIsOff(t *testing.T) {
	h := newHarness(t)
	rec := doRequest(h.mux, h.mw, http.MethodGet, "/auth/google/start", nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("GET /auth/google/start with no configuration = %d, want 403: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), i18n.T(i18n.Default, "auth.company.refused_unavailable")) {
		t.Errorf("the refusal must render the unavailable sentence, got: %.2000s", rec.Body.String())
	}
	if c := findCookie(rec, companyStateCookie); c != nil {
		t.Error("a refused start must set no state cookie")
	}
	if got := storedAttemptCount(t, h); got != 0 {
		t.Errorf("stored attempts = %d, want none", got)
	}

	if rec := doRequest(h.mux, h.mw, http.MethodGet, "/auth/ldap/start", nil); rec.Code != http.StatusNotFound {
		t.Errorf("GET /auth/ldap/start = %d, want 404", rec.Code)
	}
}

// TestCompanyCallbackRefusesWhenTheProviderWasTurnedOff proves the callback
// honouring the stored state (S11): an attempt started before an administrator
// turned the provider off is refused, and its code is never redeemed.
func TestCompanyCallbackRefusesWhenTheProviderWasTurnedOff(t *testing.T) {
	h := newHarness(t)
	companySignInStore(t, h, companySignInIn(application.NewAccountsRequester))
	seedUser(t, h.store, "Ana", "ana@example.com")
	h.companyProvider.claims = googleClaims("ana@example.com")

	cookie, _ := companyStart(t, h, "google")
	turnedOff := companySignInIn(application.NewAccountsRequester)
	turnedOff.Google.Enabled = false
	turnedOff.Google.ClientSecret = ""
	companySignInStore(t, h, turnedOff)

	rec := companyCallback(t, h, "google", cookie.Value, cookie.Value)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("callback with the provider off = %d, want 403: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), i18n.T(i18n.Default, "auth.company.refused_unavailable")) {
		t.Errorf("the refusal must render the unavailable sentence, got: %.2000s", rec.Body.String())
	}
	if h.companyProvider.exchanges != 0 {
		t.Errorf("the provider was asked to redeem %d codes, want none", h.companyProvider.exchanges)
	}
}

// TestLoginPageOffersTheConfiguredProvidersBelowTheSignInButton is S12's part 1
// as the owner picked it: the password form keeps its place, the provider buttons
// follow it, and the centred "or" divider separates the two ways in.
func TestLoginPageOffersTheConfiguredProvidersBelowTheSignInButton(t *testing.T) {
	h := newHarness(t)
	companySignInStore(t, h, companySignInIn(application.NewAccountsRequester))

	rec := doRequest(h.mux, h.mw, http.MethodGet, "/login", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /login = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`<form class="login-form" method="post" action="/login" novalidate>`,
		`name="password"`,
		`class="company-divider"`,
		">" + i18n.T(i18n.Default, "auth.company.divider") + "<",
		`href="/auth/google/start"`,
		`href="/auth/microsoft/start"`,
		i18n.T(i18n.Default, "auth.company.google"),
		i18n.T(i18n.Default, "auth.company.microsoft"),
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the sign-in page must render %q, got: %.2000s", want, body)
		}
	}
	if !appearsBefore(body, `action="/login"`, `class="company-divider"`) {
		t.Error("the divider must come after the password form")
	}
	if !appearsBefore(body, `class="company-divider"`, `href="/auth/google/start"`) {
		t.Error("the provider buttons must come after the divider")
	}
}

// TestLoginPageKeepsTheBreakGlassFormWhenPasswordSignInIsOff is S4 and S12
// together: with password sign-in off the page leads with the providers and keeps
// root's form one disclosure away, so nobody is locked out while a provider
// fails.
func TestLoginPageKeepsTheBreakGlassFormWhenPasswordSignInIsOff(t *testing.T) {
	h := newHarness(t)
	off := companySignInIn(application.NewAccountsRequester)
	off.PasswordSignInEnabled = false
	companySignInStore(t, h, off)

	rec := doRequest(h.mux, h.mw, http.MethodGet, "/login", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /login = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`<details class="company-breakglass">`,
		i18n.T(i18n.Default, "auth.company.password_link"),
		`name="password"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the page must render %q, got: %.2000s", want, body)
		}
	}
	if strings.Contains(body, `class="company-divider"`) {
		t.Error("with password sign-in off there is nothing to divide from")
	}
	if !appearsBefore(body, `href="/auth/google/start"`, `class="company-breakglass"`) {
		t.Error("the break-glass disclosure must follow the provider buttons")
	}
	if !appearsBefore(body, `<summary>`, `name="password"`) {
		t.Error("the password form must sit inside the disclosure")
	}
}

// TestLoginPageCompanyStatesInSpanish renders the same states in Spanish: the
// catalogue carries the divider, the buttons, the pending card, and the refusal
// sentences a Spanish instance answers (S13).
func TestLoginPageCompanyStatesInSpanish(t *testing.T) {
	buttons := []companyProviderButton{
		{Provider: "google", LabelKey: "google"},
		{Provider: "microsoft", LabelKey: "microsoft"},
	}
	body := renderLocalized(t, i18n.Spanish, "login", "", loginData{CompanyProviders: buttons})
	for _, want := range []string{"Continúa con Google", "Continúa con Microsoft", ">o<"} {
		if !strings.Contains(body, want) {
			t.Errorf("the Spanish sign-in page must render %q, got: %.2000s", want, body)
		}
	}

	pending := renderLocalized(t, i18n.Spanish, "login", "", loginData{CompanyPending: true})
	for _, want := range []string{"Tu acceso está pendiente de aprobación", "Volver al inicio de sesión"} {
		if !strings.Contains(pending, want) {
			t.Errorf("the Spanish pending screen must render %q, got: %.2000s", want, pending)
		}
	}
}

// TestCompanyRefusalIsLocalized proves the refusal of a refused callback follows
// the instance language: a Spanish instance answers the Spanish sentence, and the
// page around it is Spanish too (S13).
func TestCompanyRefusalIsLocalized(t *testing.T) {
	h := newHarness(t)
	companySignInStore(t, h, companySignInIn(application.NewAccountsRequester))
	if err := h.settings.SetInterfaceLanguage(t.Context(), *h.admin, application.InterfaceLanguageSpanish); err != nil {
		t.Fatalf("set the interface language: %v", err)
	}
	h.companyProvider.claims = googleClaims("ana@other.example.com")
	h.companyProvider.claims.HostedDomain = "other.example.com"

	cookie, _ := companyStart(t, h, "google")
	rec := companyCallback(t, h, "google", cookie.Value, cookie.Value)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("outside-domain callback = %d, want 403: %s", rec.Code, rec.Body.String())
	}
	for _, want := range []string{
		i18n.T(i18n.Spanish, "auth.company.refused_domain"),
		i18n.T(i18n.Spanish, "auth.login.title"),
	} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("the Spanish refusal must render %q, got: %.2000s", want, rec.Body.String())
		}
	}
	if c := findCookie(rec, sessionCookie); c != nil && c.Value != "" {
		t.Errorf("the refused sign-in issued a session cookie %q, want none", c.Value)
	}
}

// failingCompanyConfig is a configuration that cannot be read: the seam a broken
// settings store hands the sign-in page.
type failingCompanyConfig struct{}

var _ application.CompanySignInConfiguration = failingCompanyConfig{}

func (failingCompanyConfig) Get(context.Context) (application.CompanySignInSettings, error) {
	return application.CompanySignInSettings{}, errors.New("settings store unavailable")
}

func (failingCompanyConfig) ClientSecret(context.Context, application.CompanyProvider) (string, error) {
	return "", errors.New("settings store unavailable")
}

// TestLoginPageOffersNoProviderWhenTheConfigurationCannotBeRead proves the read
// fails closed: a configuration that cannot be read offers no provider button,
// while the password form keeps the sign-in page usable.
func TestLoginPageOffersNoProviderWhenTheConfigurationCannotBeRead(t *testing.T) {
	h := newHarness(t)
	// A stored configuration that WOULD offer both buttons if it were read.
	companySignInStore(t, h, companySignInIn(application.NewAccountsRequester))

	mux := http.NewServeMux()
	NewAuthHandlers(h.auth, h.users, h.renderer).WithCompanySignIn(failingCompanyConfig{}, nil).Register(mux)
	rec := doRequest(mux, h.mw, http.MethodGet, "/login", nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /login with an unreadable configuration = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `name="password"`) {
		t.Errorf("the password form must survive an unreadable configuration, got: %.2000s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), application.CompanySignInPathPrefix) {
		t.Errorf("an unreadable configuration must offer no provider, got: %.2000s", rec.Body.String())
	}
}
