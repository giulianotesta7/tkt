package oidc_test

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/adapters/oidc"
	"github.com/giulianotesta7/tkt/internal/application"
)

// OpenID Connect client (issue #419, S11): the flow is built on the standard
// library, so these tests prove the parts a library would have provided — the
// authorization request, the code exchange, and the ID token verification that
// stands between a provider's answer and a TKT session.
//
// The provider runs in-process: a Transport hands every request to an
// http.Handler, so no socket is opened and no certificate is needed, while the
// client still goes through net/http and the real URLs it uses in production.

// handlerTransport answers every request from an http.Handler instead of the
// network. The handler receives the real URL, so a test can tell the discovery
// and token endpoints of each provider apart.
type handlerTransport struct{ handler http.Handler }

func (t handlerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	rec := httptest.NewRecorder()
	t.handler.ServeHTTP(rec, r)
	resp := rec.Result()
	resp.Request = r
	return resp, nil
}

// testClock is the injected time source of the client.
type testClock struct{ now time.Time }

func (c *testClock) Now() time.Time { return c.now }

func fixedTestClock() *testClock {
	return &testClock{now: time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC)}
}

const (
	googleClientID     = "google-client"
	googleClientSecret = "google-secret"
	microsoftClientID  = "microsoft-client"
	microsoftSecret    = "microsoft-secret"
	microsoftTenant    = "0f8fad5b-d9cb-469f-a165-70867728950e"
	callbackURI        = "https://tkt.example.com/auth/google/callback"
)

// fakeProvider is an OpenID Connect provider served from memory: a discovery
// document, a JWKS, and a token endpoint. It signs the ID token it answers with
// a real RSA key, so the client verifies a real signature.
type fakeProvider struct {
	mu            sync.Mutex
	keys          map[string]*rsa.PrivateKey // published keys, by kid
	signerKid     string
	signerKey     *rsa.PrivateKey
	alg           string
	claims        map[string]any
	tamper        bool
	jwksHits      int
	discoveryHits int
	tokenRequests []url.Values
	requests      []string
	tokenStatus   int
	bodyBytes     int
	omitIDToken   bool
}

func newFakeProvider(t *testing.T) *fakeProvider {
	t.Helper()
	key := mustKey(t)
	return &fakeProvider{
		keys:      map[string]*rsa.PrivateKey{"key-1": key},
		signerKid: "key-1",
		signerKey: key,
		alg:       "RS256",
	}
}

// mustKey generates one RSA key for the in-process provider.
func mustKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	return key
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// signToken builds a compact JWT with the arranged header algorithm and kid.
func (p *fakeProvider) signToken(claims map[string]any) (string, error) {
	header, err := json.Marshal(map[string]any{"alg": p.alg, "typ": "JWT", "kid": p.signerKid})
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	signing := b64(header) + "." + b64(payload)
	if p.alg != "RS256" {
		return signing + "." + b64([]byte("not-a-signature")), nil
	}
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, p.signerKey, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	if p.tamper {
		sig[0] ^= 0xFF
	}
	return signing + "." + b64(sig), nil
}

func (p *fakeProvider) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.requests = append(p.requests, r.URL.String())

	switch {
	case strings.HasSuffix(r.URL.Path, "/.well-known/openid-configuration"):
		p.discoveryHits++
		var issuer, authEndpoint string
		if r.URL.Host == "accounts.google.com" {
			issuer = "https://accounts.google.com"
			authEndpoint = "https://provider.example.com/google/authorize"
		} else {
			issuer = "https://login.microsoftonline.com/" + microsoftTenant + "/v2.0"
			authEndpoint = "https://provider.example.com/microsoft/authorize"
		}
		writeJSON(w, map[string]any{
			"issuer":                                issuer,
			"authorization_endpoint":                authEndpoint,
			"token_endpoint":                        "https://provider.example.com/token",
			"jwks_uri":                              "https://provider.example.com/jwks",
			"response_types_supported":              []string{"code"},
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	case r.URL.Path == "/jwks":
		p.jwksHits++
		keys := make([]map[string]any, 0, len(p.keys))
		for kid, key := range p.keys {
			keys = append(keys, map[string]any{
				"kty": "RSA", "use": "sig", "alg": "RS256", "kid": kid,
				"n": b64(key.N.Bytes()), "e": b64(big.NewInt(int64(key.E)).Bytes()),
			})
		}
		writeJSON(w, map[string]any{"keys": keys})
	case r.URL.Path == "/token":
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		p.tokenRequests = append(p.tokenRequests, r.PostForm)
		if p.tokenStatus != 0 {
			http.Error(w, "provider refused the code", p.tokenStatus)
			return
		}
		if p.bodyBytes > 0 {
			_, _ = w.Write([]byte(strings.Repeat("a", p.bodyBytes)))
			return
		}
		answer := map[string]any{"access_token": "access-token", "token_type": "Bearer", "expires_in": 3600}
		if !p.omitIDToken {
			claims := p.claims
			if claims == nil {
				claims = googleClaims(time.Now())
			}
			token, err := p.signToken(claims)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			answer["id_token"] = token
		}
		writeJSON(w, answer)
	default:
		http.NotFound(w, r)
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// googleClaims is the claim set of one Workspace account inside the domain the
// installation allows.
func googleClaims(now time.Time) map[string]any {
	return map[string]any{
		"iss": "https://accounts.google.com", "sub": "sub-1", "aud": googleClientID,
		"exp": now.Add(time.Hour).Unix(), "iat": now.Unix(), "nonce": "nonce-1",
		"email": "ana@example.com", "email_verified": true, "hd": "example.com", "name": "Ana",
	}
}

func googleCreds() application.CompanyProviderCredentials {
	return application.CompanyProviderCredentials{
		Provider: application.ProviderGoogle, ClientID: googleClientID, ClientSecret: googleClientSecret, RedirectURI: callbackURI,
	}
}

func microsoftCreds() application.CompanyProviderCredentials {
	return application.CompanyProviderCredentials{
		Provider: application.ProviderMicrosoft, ClientID: microsoftClientID, ClientSecret: microsoftSecret,
		TenantID: microsoftTenant, RedirectURI: "https://tkt.example.com/auth/microsoft/callback",
	}
}

func googleRequest() application.CompanyAuthorizationRequest {
	return application.CompanyAuthorizationRequest{State: "state-1", Nonce: "nonce-1", CodeVerifier: "verifier-1", DomainHint: "example.com"}
}

// client wires the client under test against the in-process provider.
func (p *fakeProvider) client(clock *testClock) *oidc.Client {
	return oidc.NewClient(&http.Client{Transport: handlerTransport{handler: p}}, clock)
}

// exchange signed with the provider's arranged claims runs one code exchange.
func (p *fakeProvider) exchange(t *testing.T, clock *testClock, creds application.CompanyProviderCredentials, req application.CompanyAuthorizationRequest) (application.CompanyIdentityClaims, error) {
	t.Helper()
	if p.claims == nil {
		p.claims = googleClaims(clock.Now())
	}
	return p.client(clock).Exchange(context.Background(), creds, req, "auth-code")
}

func assertRejected(t *testing.T, err error, detail string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s = nil error, want the answer refused", detail)
	}
	if !errors.Is(err, application.ErrCompanyProviderRejected) {
		t.Fatalf("%s = %v, want an error wrapping ErrCompanyProviderRejected", detail, err)
	}
}

// TestAuthorizationURLCarriesTheFlowValues proves the authorization request a
// browser is sent to: the code flow with PKCE (S256), the state, the nonce, the
// three scopes, the client and callback, and the Google domain hint.
func TestAuthorizationURLCarriesTheFlowValues(t *testing.T) {
	provider := newFakeProvider(t)
	clock := fixedTestClock()
	client := provider.client(clock)

	raw, err := client.AuthorizationURL(context.Background(), googleCreds(), googleRequest())
	if err != nil {
		t.Fatalf("AuthorizationURL: %v", err)
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse authorization URL %q: %v", raw, err)
	}
	if parsed.Host != "provider.example.com" || parsed.Path != "/google/authorize" {
		t.Errorf("authorization endpoint = %s%s, want the discovery document's", parsed.Host, parsed.Path)
	}
	query := parsed.Query()
	want := map[string]string{
		"response_type":         "code",
		"client_id":             googleClientID,
		"redirect_uri":          callbackURI,
		"scope":                 "openid email profile",
		"state":                 "state-1",
		"nonce":                 "nonce-1",
		"code_challenge_method": "S256",
		"hd":                    "example.com",
	}
	for key, value := range want {
		if got := query.Get(key); got != value {
			t.Errorf("authorization query %s = %q, want %q", key, got, value)
		}
	}
	sum := sha256.Sum256([]byte("verifier-1"))
	if got, wantChallenge := query.Get("code_challenge"), b64(sum[:]); got != wantChallenge {
		t.Errorf("code_challenge = %q, want the S256 digest of the verifier %q", got, wantChallenge)
	}

	if provider.discoveryHits != 1 {
		t.Errorf("discovery reads = %d, want 1", provider.discoveryHits)
	}
	if _, err := client.AuthorizationURL(context.Background(), googleCreds(), googleRequest()); err != nil {
		t.Fatalf("second AuthorizationURL: %v", err)
	}
	if provider.discoveryHits != 1 {
		t.Errorf("discovery reads after a second start = %d, want the cached document", provider.discoveryHits)
	}
}

// TestAuthorizationURLUsesEachProvidersPublishedAddress proves the two providers
// are read from their own metadata: Google from its issuer, Microsoft from the
// configured tenant's v2.0 authority, which is also the only issuer its tokens
// may name.
func TestAuthorizationURLUsesEachProvidersPublishedAddress(t *testing.T) {
	provider := newFakeProvider(t)
	clock := fixedTestClock()
	client := provider.client(clock)

	if _, err := client.AuthorizationURL(context.Background(), googleCreds(), googleRequest()); err != nil {
		t.Fatalf("google AuthorizationURL: %v", err)
	}
	if got := provider.requests[0]; got != "https://accounts.google.com/.well-known/openid-configuration" {
		t.Errorf("google discovery request = %q", got)
	}

	request := googleRequest()
	request.DomainHint = ""
	raw, err := client.AuthorizationURL(context.Background(), microsoftCreds(), request)
	if err != nil {
		t.Fatalf("microsoft AuthorizationURL: %v", err)
	}
	wantDiscovery := "https://login.microsoftonline.com/" + microsoftTenant + "/v2.0/.well-known/openid-configuration"
	if !strings.Contains(strings.Join(provider.requests, " "), wantDiscovery) {
		t.Errorf("discovery requests = %v, want %q among them", provider.requests, wantDiscovery)
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse microsoft authorization URL: %v", err)
	}
	if parsed.Path != "/microsoft/authorize" {
		t.Errorf("microsoft authorization endpoint = %q, want the tenant authority's", parsed.Path)
	}
	if got := parsed.Query().Get("hd"); got != "" {
		t.Errorf("microsoft authorization URL carries hd = %q, want none", got)
	}
}

// TestExchangeRedeemsTheCodeAndReturnsTheVerifiedClaims proves the token
// request (client_secret_post with the PKCE verifier) and the claim set the
// application reads.
func TestExchangeRedeemsTheCodeAndReturnsTheVerifiedClaims(t *testing.T) {
	provider := newFakeProvider(t)
	clock := fixedTestClock()

	claims, err := provider.exchange(t, clock, googleCreds(), googleRequest())
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if claims.Issuer != "https://accounts.google.com" || claims.Subject != "sub-1" {
		t.Errorf("issuer/subject = %q/%q, want the token's", claims.Issuer, claims.Subject)
	}
	if claims.Email != "ana@example.com" || !claims.EmailVerified {
		t.Errorf("email/verified = %q/%v, want ana@example.com and true", claims.Email, claims.EmailVerified)
	}
	if claims.HostedDomain != "example.com" || claims.Name != "Ana" {
		t.Errorf("hd/name = %q/%q, want example.com and Ana", claims.HostedDomain, claims.Name)
	}

	if len(provider.tokenRequests) != 1 {
		t.Fatalf("token requests = %d, want 1", len(provider.tokenRequests))
	}
	form := provider.tokenRequests[0]
	want := map[string]string{
		"grant_type":    "authorization_code",
		"code":          "auth-code",
		"redirect_uri":  callbackURI,
		"client_id":     googleClientID,
		"client_secret": googleClientSecret,
		"code_verifier": "verifier-1",
	}
	for key, value := range want {
		if got := form.Get(key); got != value {
			t.Errorf("token form %s = %q, want %q", key, got, value)
		}
	}
}

// TestExchangeAcceptsBothGoogleIssuerForms proves the issuer check accepts the
// bare and the URL form Google publishes, and nothing else.
func TestExchangeAcceptsBothGoogleIssuerForms(t *testing.T) {
	clock := fixedTestClock()
	for _, issuer := range []string{"accounts.google.com", "https://accounts.google.com"} {
		provider := newFakeProvider(t)
		claims := googleClaims(clock.Now())
		claims["iss"] = issuer
		provider.claims = claims
		if _, err := provider.exchange(t, clock, googleCreds(), googleRequest()); err != nil {
			t.Errorf("Exchange with iss %q = %v, want it accepted", issuer, err)
		}
	}
}

// TestExchangeRejectsATamperedSignature proves the signature is checked: a token
// whose bytes changed after signing is refused.
func TestExchangeRejectsATamperedSignature(t *testing.T) {
	provider := newFakeProvider(t)
	provider.tamper = true
	_, err := provider.exchange(t, fixedTestClock(), googleCreds(), googleRequest())
	assertRejected(t, err, "Exchange of a tampered signature")
}

// TestExchangeRejectsANonRS256Algorithm proves the algorithm is restricted to
// RS256: "none" and the symmetric algorithms a confused deputy would accept are
// refused before the signature is even looked at.
func TestExchangeRejectsANonRS256Algorithm(t *testing.T) {
	for _, alg := range []string{"none", "HS256", "RS512"} {
		provider := newFakeProvider(t)
		provider.alg = alg
		_, err := provider.exchange(t, fixedTestClock(), googleCreds(), googleRequest())
		assertRejected(t, err, "Exchange with alg "+alg)
	}
}

// TestExchangeRejectsAWrongIssuer proves the issuer is an exact match: a token
// from another authority is refused even when everything else is valid, which is
// what keeps a Microsoft token from completing a Google flow.
func TestExchangeRejectsAWrongIssuer(t *testing.T) {
	provider := newFakeProvider(t)
	clock := fixedTestClock()
	claims := googleClaims(clock.Now())
	claims["iss"] = "https://login.microsoftonline.com/" + microsoftTenant + "/v2.0"
	provider.claims = claims

	_, err := provider.exchange(t, clock, googleCreds(), googleRequest())
	assertRejected(t, err, "Exchange with another authority's iss")
}

// TestExchangeRejectsAWrongAudience proves the token is issued for this client:
// an audience that does not contain the client ID, and an authorized party that
// names another client, are both refused.
func TestExchangeRejectsAWrongAudience(t *testing.T) {
	clock := fixedTestClock()
	provider := newFakeProvider(t)
	claims := googleClaims(clock.Now())
	claims["aud"] = "another-client"
	provider.claims = claims
	_, err := provider.exchange(t, clock, googleCreds(), googleRequest())
	assertRejected(t, err, "Exchange with another audience")

	provider = newFakeProvider(t)
	claims = googleClaims(clock.Now())
	claims["aud"] = []string{"another-client", googleClientID}
	claims["azp"] = googleClientID
	provider.claims = claims
	if _, err := provider.exchange(t, clock, googleCreds(), googleRequest()); err != nil {
		t.Errorf("Exchange with the client in a multi-valued audience = %v, want it accepted", err)
	}
}

// TestExchangeRejectsAnExpiredToken proves the token lifetime is compared with
// the injected clock: a token whose expiry is in the past is refused however
// recently it expired, because the verifier applies no leeway of its own.
func TestExchangeRejectsAnExpiredToken(t *testing.T) {
	clock := fixedTestClock()
	for _, offset := range []time.Duration{-time.Minute, -3 * time.Minute, -time.Hour} {
		provider := newFakeProvider(t)
		claims := googleClaims(clock.Now())
		claims["exp"] = clock.Now().Add(offset).Unix()
		provider.claims = claims
		_, err := provider.exchange(t, clock, googleCreds(), googleRequest())
		assertRejected(t, err, "Exchange of a token expired by "+offset.String())
	}

	// The clock is the injected one: the same token is fresh for a clock set
	// before its expiry.
	provider := newFakeProvider(t)
	provider.claims = googleClaims(clock.Now())
	earlier := fixedTestClock()
	earlier.now = clock.Now().Add(-time.Hour)
	if _, err := provider.exchange(t, earlier, googleCreds(), googleRequest()); err != nil {
		t.Errorf("Exchange of a live token against an earlier clock = %v, want it accepted", err)
	}
}

// TestExchangeRejectsAWrongNonce proves the ID token is bound to this attempt: a
// token carrying another nonce is refused, which is what stops a token replayed
// from another flow.
func TestExchangeRejectsAWrongNonce(t *testing.T) {
	provider := newFakeProvider(t)
	clock := fixedTestClock()
	claims := googleClaims(clock.Now())
	claims["nonce"] = "nonce-other"
	provider.claims = claims

	_, err := provider.exchange(t, clock, googleCreds(), googleRequest())
	assertRejected(t, err, "Exchange with another flow's nonce")
}

// TestExchangePicksUpARotatedKey proves key rotation: the key set is cached, and
// a token signed with a key the cache does not have makes the client read the
// keys once more, which is how a rotation lands without a restart.
func TestExchangePicksUpARotatedKey(t *testing.T) {
	provider := newFakeProvider(t)
	clock := fixedTestClock()
	if _, err := provider.exchange(t, clock, googleCreds(), googleRequest()); err != nil {
		t.Fatalf("Exchange with the first key: %v", err)
	}
	if provider.jwksHits != 1 {
		t.Fatalf("JWKS reads after the first sign-in = %d, want 1", provider.jwksHits)
	}

	rotated := mustKey(t)
	provider.keys["key-2"] = rotated
	provider.signerKey = rotated
	provider.signerKid = "key-2"
	if _, err := provider.exchange(t, clock, googleCreds(), googleRequest()); err != nil {
		t.Fatalf("Exchange with the rotated key: %v", err)
	}
	if provider.jwksHits != 2 {
		t.Errorf("JWKS reads after the rotation = %d, want 2 (one refresh)", provider.jwksHits)
	}
}

// TestExchangeRejectsAnUnknownKidThatNeverArrives proves the refresh is bounded:
// a key the provider never publishes is refused after one re-read, so a stream
// of bad tokens cannot keep the client fetching keys.
func TestExchangeRejectsAnUnknownKidThatNeverArrives(t *testing.T) {
	provider := newFakeProvider(t)
	clock := fixedTestClock()
	if _, err := provider.exchange(t, clock, googleCreds(), googleRequest()); err != nil {
		t.Fatalf("Exchange with the published key: %v", err)
	}
	provider.signerKey = mustKey(t)
	provider.signerKid = "key-missing"

	_, err := provider.exchange(t, clock, googleCreds(), googleRequest())
	assertRejected(t, err, "Exchange with an unpublished kid")
	if provider.jwksHits != 2 {
		t.Errorf("JWKS reads = %d, want 2 (one refresh, then refusal)", provider.jwksHits)
	}
}

// TestExchangeRejectsAnUnusableTokenAnswer proves the exchange refuses what it
// cannot verify: a missing ID token, an error status, and a body larger than the
// client will read.
func TestExchangeRejectsAnUnusableTokenAnswer(t *testing.T) {
	clock := fixedTestClock()

	provider := newFakeProvider(t)
	provider.omitIDToken = true
	_, err := provider.exchange(t, clock, googleCreds(), googleRequest())
	assertRejected(t, err, "Exchange without an ID token")

	provider = newFakeProvider(t)
	provider.tokenStatus = http.StatusBadRequest
	_, err = provider.exchange(t, clock, googleCreds(), googleRequest())
	assertRejected(t, err, "Exchange of a refused code")

	provider = newFakeProvider(t)
	provider.bodyBytes = 2 << 20
	if _, err := provider.exchange(t, clock, googleCreds(), googleRequest()); err == nil {
		t.Fatal("Exchange of an oversized answer = nil error, want it refused")
	}
}

// TestExchangeRefusesAnUnsupportedProvider proves the client has exactly two
// providers: anything else is an error, never a guess at an endpoint.
func TestExchangeRefusesAnUnsupportedProvider(t *testing.T) {
	provider := newFakeProvider(t)
	clock := fixedTestClock()
	creds := googleCreds()
	creds.Provider = application.CompanyProvider("ldap")

	if _, err := provider.client(clock).Exchange(context.Background(), creds, googleRequest(), "auth-code"); err == nil {
		t.Fatal("Exchange for an unsupported provider = nil error, want a refusal")
	}
	if _, err := provider.client(clock).AuthorizationURL(context.Background(), creds, googleRequest()); err == nil {
		t.Fatal("AuthorizationURL for an unsupported provider = nil error, want a refusal")
	}
	if len(provider.requests) != 0 {
		t.Errorf("provider requests = %v, want none for an unsupported provider", provider.requests)
	}
}

// TestExchangeRequiresAMicrosoftTenant proves the Microsoft flow is bound to one
// directory: without a tenant there is no authority to read, so no request is
// made.
func TestExchangeRequiresAMicrosoftTenant(t *testing.T) {
	provider := newFakeProvider(t)
	creds := microsoftCreds()
	creds.TenantID = ""

	_, err := provider.client(fixedTestClock()).AuthorizationURL(context.Background(), creds, googleRequest())
	if err == nil {
		t.Fatal("AuthorizationURL without a tenant = nil error, want a refusal")
	}
	if len(provider.requests) != 0 {
		t.Errorf("provider requests = %v, want none", provider.requests)
	}
}

// TestExchangeReportsTheTokenEndpoint proves the token endpoint used is the one
// the discovery document published, not a hardcoded one.
func TestExchangeReportsTheTokenEndpoint(t *testing.T) {
	provider := newFakeProvider(t)
	clock := fixedTestClock()
	if _, err := provider.exchange(t, clock, googleCreds(), googleRequest()); err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	found := false
	for _, request := range provider.requests {
		if request == "https://provider.example.com/token" {
			found = true
		}
	}
	if !found {
		t.Errorf("provider requests = %v, want the published token endpoint among them", provider.requests)
	}
}

// TestExchangeRejectsAnIncompleteDiscoveryDocument proves a provider that does
// not publish what the flow needs is refused instead of being sent a request at
// an empty URL.
func TestExchangeRejectsAnIncompleteDiscoveryDocument(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"issuer": "https://accounts.google.com"})
	})
	client := oidc.NewClient(&http.Client{Transport: handlerTransport{handler: handler}}, fixedTestClock())
	if _, err := client.AuthorizationURL(context.Background(), googleCreds(), googleRequest()); err == nil {
		t.Fatal("AuthorizationURL with an incomplete discovery document = nil error, want a refusal")
	}
}

// TestExchangeRejectsAMalformedToken proves a token that is not three parts is
// refused rather than parsed as something.
func TestExchangeRejectsAMalformedToken(t *testing.T) {
	clock := fixedTestClock()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			writeJSON(w, map[string]any{
				"issuer": "https://accounts.google.com", "authorization_endpoint": "https://provider.example.com/a",
				"token_endpoint": "https://provider.example.com/token", "jwks_uri": "https://provider.example.com/jwks",
			})
		case "/token":
			writeJSON(w, map[string]any{"access_token": "access-token", "id_token": "not-a-jwt"})
		default:
			http.NotFound(w, r)
		}
	})
	client := oidc.NewClient(&http.Client{Transport: handlerTransport{handler: handler}}, clock)
	_, err := client.Exchange(context.Background(), googleCreds(), googleRequest(), "auth-code")
	assertRejected(t, err, "Exchange of a malformed token")
	if !strings.Contains(fmt.Sprint(err), "token") {
		t.Errorf("error = %v, want it to name the token", err)
	}
}
