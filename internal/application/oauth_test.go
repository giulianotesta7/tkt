package application_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/url"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// OAuth authorization server for MCP clients (issue #493, L7). The service runs
// over fakeOAuthStore, the shared user fake, the fake clock and a deterministic
// random source.

const (
	oauthBase     = "https://tkt.example.com"
	oauthResource = oauthBase + "/mcp"
	// testVerifier and testChallenge are the RFC 7636 appendix B pair.
	testVerifier  = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	testChallenge = "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
	appRedirect   = "https://app.example.com/cb"
)

var v1Scopes = []domain.APIKeyScope{domain.ScopeCatalogRead, domain.ScopeTicketsRead, domain.ScopeTicketsWrite, domain.ScopeCommentsWrite, domain.ScopeCommentsInternal}

// hashReader is a deterministic random source whose draws never repeat: each
// 32-byte block is the SHA-256 of a counter.
type hashReader struct {
	n   uint64
	buf []byte
}

func (r *hashReader) Read(p []byte) (int, error) {
	for i := range p {
		if len(r.buf) == 0 {
			r.n++
			sum := sha256.Sum256([]byte(strconv.FormatUint(r.n, 10)))
			r.buf = sum[:]
		}
		p[i], r.buf = r.buf[0], r.buf[1:]
	}
	return len(p), nil
}

type oauthFixture struct {
	svc      *application.OAuthService
	store    *fakeOAuthStore
	users    *fakeUserStore
	clock    *fakeClock
	settings *fakePublicURLSettings
	// person is an active agent person.
	person domain.User
}

func newOAuth() *oauthFixture {
	clock := fixedClock()
	users := newFakeUserStore()
	store := newFakeOAuthStore()
	store.enabled = true
	settings := &fakePublicURLSettings{baseURL: oauthBase}
	person := users.seedRole("Ana", "ana@example.com", domain.RoleAgent, true)
	return &oauthFixture{
		svc:      application.NewOAuthService(store, users, settings, clock, &hashReader{}),
		store:    store,
		users:    users,
		clock:    clock,
		settings: settings,
		person:   person,
	}
}

// snapshot captures every stored row count and the id counter, so a rejection
// can be proven to have written nothing.
func (f *oauthFixture) snapshot() [6]int64 {
	return [6]int64{int64(len(f.store.clients)), int64(len(f.store.codes)), int64(len(f.store.grants)), int64(len(f.store.tokens)), f.store.nextID, int64(f.store.touches)}
}

func (f *oauthFixture) register(t *testing.T, uris ...string) *domain.OAuthClient {
	t.Helper()
	c, err := f.svc.RegisterClient(t.Context(), application.ClientRegistration{ClientName: "Claude", RedirectURIs: uris})
	if err != nil {
		t.Fatalf("register %v: %v", uris, err)
	}
	return c
}

func authorizeRequest(c *domain.OAuthClient) application.AuthorizeRequest {
	return application.AuthorizeRequest{
		ClientID:            c.ClientID,
		RedirectURI:         c.RedirectURIs[0],
		ResponseType:        "code",
		State:               "st-1",
		CodeChallenge:       testChallenge,
		CodeChallengeMethod: "S256",
		Resource:            oauthResource,
	}
}

// approve runs prepare and approve for actor and answers the issued code.
func (f *oauthFixture) approve(t *testing.T, actor domain.User, req application.AuthorizeRequest) string {
	t.Helper()
	p, err := f.svc.PrepareAuthorization(t.Context(), req)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	redirect, err := f.svc.Approve(t.Context(), actor, *p)
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	u, err := url.Parse(redirect)
	if err != nil {
		t.Fatal(err)
	}
	code := u.Query().Get("code")
	if code == "" {
		t.Fatalf("approve redirect %q carries no code", redirect)
	}
	return code
}

func codeRequest(c *domain.OAuthClient, code string) application.TokenRequest {
	return application.TokenRequest{GrantType: "authorization_code", Code: code, RedirectURI: c.RedirectURIs[0], ClientID: c.ClientID, CodeVerifier: testVerifier, Resource: oauthResource}
}

// connect registers a client, approves it as actor with scope and exchanges
// the code.
func (f *oauthFixture) connect(t *testing.T, actor domain.User, scope string) (*domain.OAuthClient, *application.TokenResponse) {
	t.Helper()
	c := f.register(t, appRedirect)
	req := authorizeRequest(c)
	req.Scope = scope
	tok, err := f.svc.Token(t.Context(), codeRequest(c, f.approve(t, actor, req)))
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	return c, tok
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func wantOAuthError(t *testing.T, err error, code string) *application.OAuthError {
	t.Helper()
	var oe *application.OAuthError
	if !errors.As(err, &oe) || oe.Code != code {
		t.Fatalf("err = %v; want OAuthError %s", err, code)
	}
	var ae *application.AuthorizeError
	if errors.As(err, &ae) {
		t.Fatalf("err = %v is an AuthorizeError; want a redirectable OAuthError", err)
	}
	return oe
}

func wantAuthorizeError(t *testing.T, err error) {
	t.Helper()
	var ae *application.AuthorizeError
	if !errors.As(err, &ae) {
		t.Fatalf("err = %v; want AuthorizeError", err)
	}
	var oe *application.OAuthError
	if errors.As(err, &oe) {
		t.Fatalf("err = %v also matches a redirectable OAuthError", err)
	}
}

func TestOAuthEndpoints(t *testing.T) {
	f := newOAuth()
	got, err := f.svc.Endpoints(t.Context())
	want := application.OAuthEndpoints{
		Issuer:                oauthBase,
		Resource:              oauthResource,
		AuthorizationEndpoint: oauthBase + "/oauth/authorize",
		TokenEndpoint:         oauthBase + "/oauth/token",
		RegistrationEndpoint:  oauthBase + "/oauth/register",
		ScopesSupported:       v1Scopes,
	}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("Endpoints = %+v, %v; want %+v", got, err, want)
	}

	f.store.enabled = false
	if _, err := f.svc.Endpoints(t.Context()); !errors.Is(err, application.ErrMCPDisabled) {
		t.Fatalf("switch off: err = %v; want ErrMCPDisabled", err)
	}
	f.store.enabled = true
	f.settings.baseURL = ""
	if _, err := f.svc.Endpoints(t.Context()); !errors.Is(err, application.ErrMCPDisabled) {
		t.Fatalf("no public URL: err = %v; want ErrMCPDisabled", err)
	}
}

func TestOAuthSetMCPEnabled(t *testing.T) {
	f := newOAuth()
	f.store.enabled = false

	var forbidden *domain.ForbiddenError
	if err := f.svc.SetMCPEnabled(t.Context(), apiAdmin, true); !errors.As(err, &forbidden) {
		t.Fatalf("admin: err = %v; want ForbiddenError", err)
	}
	if f.store.enabled || len(f.store.events) != 0 {
		t.Fatal("refused admin changed the switch")
	}

	f.settings.baseURL = ""
	var v *domain.ValidationError
	if err := f.svc.SetMCPEnabled(t.Context(), apiRoot, true); !errors.As(err, &v) || v.Field != "instance_address" || v.Key != "error.instance_address_required" || v.Message == "" {
		t.Fatalf("no public URL: err = %v; want mcp_enabled validation", err)
	}
	if f.store.enabled || len(f.store.events) != 0 {
		t.Fatal("refused enable changed the switch")
	}
	if err := f.svc.SetMCPEnabled(t.Context(), apiRoot, false); err != nil || f.store.enabled {
		t.Fatalf("disable without public URL: %v", err)
	}

	f.settings.baseURL = oauthBase
	if err := f.svc.SetMCPEnabled(t.Context(), apiRoot, true); err != nil {
		t.Fatal(err)
	}
	enabled, err := f.svc.MCPEnabled(t.Context())
	if err != nil || !enabled {
		t.Fatalf("MCPEnabled = %v, %v", enabled, err)
	}
	want := []fakeAPIEvent{{"mcp_disabled", apiRoot.ID}, {"mcp_enabled", apiRoot.ID}}
	if !reflect.DeepEqual(f.store.events, want) {
		t.Fatalf("events = %v; want %v", f.store.events, want)
	}
}

func TestOAuthRegisterClientRedirectURIs(t *testing.T) {
	accepted := []string{
		"https://app.example.com/cb",
		"https://vscode.dev/redirect",
		"https://app.example.com/cb?tenant=1",
		"http://localhost/callback",
		"http://localhost:3118/callback",
		"http://127.0.0.1:33418",
		"http://[::1]:8080/cb",
		"cursor://anysphere.cursor-mcp/oauth/callback",
		"com.example.app:/oauth",
	}
	for _, uri := range accepted {
		t.Run("accept "+uri, func(t *testing.T) {
			f := newOAuth()
			c, err := f.svc.RegisterClient(t.Context(), application.ClientRegistration{RedirectURIs: []string{uri}})
			if err != nil || !reflect.DeepEqual(c.RedirectURIs, []string{uri}) {
				t.Fatalf("register %q = %+v, %v", uri, c, err)
			}
		})
	}

	rejected := [][]string{
		{"http://example.com/cb"},
		{"http://localhost.evil.com/cb"},
		{"http://127.0.0.2/cb"},
		{"javascript:alert(1)"},
		{"data:text/html,hi"},
		{"file:///etc/passwd"},
		{"vbscript:msgbox"},
		{"ws://localhost/cb"},
		{"https://app.example.com/cb#frag"},
		{"http://localhost/cb#frag"},
		{"https:///cb"},
		{"relative/cb"},
		{""},
		{"Cursor://anysphere.cursor-mcp/oauth/callback"},
		{appRedirect, "http://example.com/cb"},
		{},
	}
	eleven := make([]string, 11)
	for i := range eleven {
		eleven[i] = appRedirect + "/" + strconv.Itoa(i)
	}
	rejected = append(rejected, eleven)
	for _, uris := range rejected {
		t.Run("reject "+strings.Join(uris, " "), func(t *testing.T) {
			f := newOAuth()
			before := f.snapshot()
			c, err := f.svc.RegisterClient(t.Context(), application.ClientRegistration{RedirectURIs: uris})
			wantOAuthError(t, err, "invalid_redirect_uri")
			if c != nil || f.snapshot() != before {
				t.Fatal("rejected registration stored a client")
			}
		})
	}

	t.Run("ten URIs", func(t *testing.T) {
		f := newOAuth()
		if _, err := f.svc.RegisterClient(t.Context(), application.ClientRegistration{RedirectURIs: eleven[:10]}); err != nil {
			t.Fatal(err)
		}
	})
}

func TestOAuthRegisterClientMetadata(t *testing.T) {
	f := newOAuth()
	c, err := f.svc.RegisterClient(t.Context(), application.ClientRegistration{
		ClientName:              "  Claude Code  ",
		RedirectURIs:            []string{"http://localhost/callback"},
		GrantTypes:              []string{"authorization_code", "refresh_token"},
		ResponseTypes:           []string{"code"},
		TokenEndpointAuthMethod: "none",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^tktcl_[0-9a-f]{32}$`).MatchString(c.ClientID) || c.Name != "Claude Code" || !c.CreatedAt.Equal(f.clock.Now()) || c.ID == 0 {
		t.Fatalf("client = %+v", c)
	}
	stored, err := f.store.ClientByClientID(t.Context(), c.ClientID)
	if err != nil || !reflect.DeepEqual(stored, c) {
		t.Fatalf("stored = %+v, %v; want %+v", stored, err, c)
	}

	for _, name := range []string{"", "   "} {
		c, err := f.svc.RegisterClient(t.Context(), application.ClientRegistration{ClientName: name, RedirectURIs: []string{appRedirect}})
		if err != nil || c.Name != "MCP client" {
			t.Fatalf("name %q = %+v, %v; want MCP client", name, c, err)
		}
	}
	long := strings.Repeat("é", 150)
	c, err = f.svc.RegisterClient(t.Context(), application.ClientRegistration{ClientName: long, RedirectURIs: []string{appRedirect}})
	if err != nil || c.Name != strings.Repeat("é", 100) {
		t.Fatalf("long name = %q, %v; want 100 runes", c.Name, err)
	}

	bad := []application.ClientRegistration{
		{TokenEndpointAuthMethod: "client_secret_basic"},
		{TokenEndpointAuthMethod: "client_secret_post"},
		{GrantTypes: []string{"authorization_code", "client_credentials"}},
		{GrantTypes: []string{"implicit"}},
		{ResponseTypes: []string{"token"}},
		{ResponseTypes: []string{"code", "id_token"}},
	}
	for _, reg := range bad {
		reg.RedirectURIs = []string{appRedirect}
		before := f.snapshot()
		c, err := f.svc.RegisterClient(t.Context(), reg)
		wantOAuthError(t, err, "invalid_client_metadata")
		if c != nil || f.snapshot() != before {
			t.Fatalf("rejected %+v stored a client", reg)
		}
	}

	f.store.enabled = false
	before := f.snapshot()
	if _, err := f.svc.RegisterClient(t.Context(), application.ClientRegistration{RedirectURIs: []string{appRedirect}}); !errors.Is(err, application.ErrMCPDisabled) {
		t.Fatalf("switch off: err = %v; want ErrMCPDisabled", err)
	}
	if f.snapshot() != before {
		t.Fatal("refused registration stored a client")
	}
}

func TestOAuthRegisterClientPrunesUnapproved(t *testing.T) {
	f := newOAuth()
	abandoned := f.register(t, appRedirect)
	used, _ := f.connect(t, f.person, "")
	f.clock.Advance(24*time.Hour + time.Second)
	f.register(t, appRedirect)
	if _, err := f.store.ClientByClientID(t.Context(), abandoned.ClientID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("abandoned client after 24h: err = %v; want pruned", err)
	}
	if _, err := f.store.ClientByClientID(t.Context(), used.ClientID); err != nil {
		t.Fatalf("approved client pruned: %v", err)
	}
}

func TestOAuthPrepareNeverRedirects(t *testing.T) {
	f := newOAuth()
	c := f.register(t, appRedirect)
	cases := map[string]func(*application.AuthorizeRequest){
		"unknown client":        func(r *application.AuthorizeRequest) { r.ClientID = "tktcl_00000000000000000000000000000000" },
		"missing client":        func(r *application.AuthorizeRequest) { r.ClientID = "" },
		"missing redirect":      func(r *application.AuthorizeRequest) { r.RedirectURI = "" },
		"unregistered redirect": func(r *application.AuthorizeRequest) { r.RedirectURI = "https://evil.example.com/cb" },
		"redirect prefix":       func(r *application.AuthorizeRequest) { r.RedirectURI = appRedirect + "/more" },
		// The client error comes first even when everything else is wrong too.
		"unregistered redirect and bad response type": func(r *application.AuthorizeRequest) {
			r.RedirectURI = "https://evil.example.com/cb"
			r.ResponseType = "token"
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			req := authorizeRequest(c)
			mutate(&req)
			p, err := f.svc.PrepareAuthorization(t.Context(), req)
			wantAuthorizeError(t, err)
			if p != nil {
				t.Fatalf("pending = %+v; want none", p)
			}
		})
	}

	f.store.enabled = false
	if _, err := f.svc.PrepareAuthorization(t.Context(), authorizeRequest(c)); !errors.Is(err, application.ErrMCPDisabled) {
		t.Fatalf("switch off: err = %v; want ErrMCPDisabled", err)
	}
}

func TestOAuthPrepareRedirectMatching(t *testing.T) {
	cases := []struct {
		registered, presented string
		match                 bool
	}{
		{"http://localhost/callback", "http://localhost:3118/callback", true},
		{"http://localhost:3118/callback", "http://localhost:4000/callback", true},
		{"http://127.0.0.1:33418", "http://127.0.0.1:51000", true},
		{"http://[::1]/cb", "http://[::1]:5000/cb", true},
		{"http://localhost/cb?x=1", "http://localhost:3118/cb?x=1", true},
		{"http://localhost/callback", "http://localhost:3118/other", false},
		{"http://localhost/cb?x=1", "http://localhost:3118/cb?x=2", false},
		{"http://localhost/cb", "http://localhost:3118/cb?x=1", false},
		{"http://localhost/callback", "http://127.0.0.1:3118/callback", false},
		{appRedirect, appRedirect, true},
		{appRedirect, "https://app.example.com:8443/cb", false},
		{appRedirect, "https://APP.example.com/cb", false},
		{appRedirect, appRedirect + "/", false},
		{"cursor://anysphere.cursor-mcp/oauth/callback", "cursor://anysphere.cursor-mcp/oauth/callback", true},
	}
	for _, tc := range cases {
		t.Run(tc.registered+" vs "+tc.presented, func(t *testing.T) {
			f := newOAuth()
			c := f.register(t, tc.registered)
			req := authorizeRequest(c)
			req.RedirectURI = tc.presented
			p, err := f.svc.PrepareAuthorization(t.Context(), req)
			if !tc.match {
				wantAuthorizeError(t, err)
				return
			}
			if err != nil || p.RedirectURI != tc.presented {
				t.Fatalf("prepare = %+v, %v; want a match keeping %q", p, err, tc.presented)
			}
		})
	}
}

func TestOAuthPrepareRedirectableErrors(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*application.AuthorizeRequest)
		code   string
	}{
		{"response type token", func(r *application.AuthorizeRequest) { r.ResponseType = "token" }, "unsupported_response_type"},
		{"missing response type", func(r *application.AuthorizeRequest) { r.ResponseType = "" }, "unsupported_response_type"},
		{"missing state", func(r *application.AuthorizeRequest) { r.State = "" }, "invalid_request"},
		{"plain challenge", func(r *application.AuthorizeRequest) { r.CodeChallengeMethod = "plain" }, "invalid_request"},
		{"missing challenge method", func(r *application.AuthorizeRequest) { r.CodeChallengeMethod = "" }, "invalid_request"},
		{"missing challenge", func(r *application.AuthorizeRequest) { r.CodeChallenge = "" }, "invalid_request"},
		{"short challenge", func(r *application.AuthorizeRequest) { r.CodeChallenge = testChallenge[:42] }, "invalid_request"},
		{"long challenge", func(r *application.AuthorizeRequest) { r.CodeChallenge = testChallenge + "A" }, "invalid_request"},
		{"padded challenge", func(r *application.AuthorizeRequest) { r.CodeChallenge = testChallenge[:42] + "=" }, "invalid_request"},
		{"other resource", func(r *application.AuthorizeRequest) { r.Resource = "https://other.example.com/mcp" }, "invalid_target"},
		{"issuer as resource", func(r *application.AuthorizeRequest) { r.Resource = oauthBase }, "invalid_target"},
		{"resource path case", func(r *application.AuthorizeRequest) { r.Resource = oauthBase + "/MCP" }, "invalid_target"},
		{"unknown scope", func(r *application.AuthorizeRequest) { r.Scope = "tickets:read admin" }, "invalid_scope"},
		{"tickets:all", func(r *application.AuthorizeRequest) { r.Scope = "tickets:all" }, "invalid_scope"},
		{"tickets:all among others", func(r *application.AuthorizeRequest) { r.Scope = "tickets:read tickets:all" }, "invalid_scope"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newOAuth()
			c := f.register(t, appRedirect+"?tenant=a")
			req := authorizeRequest(c)
			tc.mutate(&req)
			before := f.snapshot()
			p, err := f.svc.PrepareAuthorization(t.Context(), req)
			oe := wantOAuthError(t, err, tc.code)
			if f.snapshot() != before {
				t.Fatal("prepare wrote to the store")
			}
			if p == nil || p.RedirectURI != req.RedirectURI || p.State != req.State || p.Issuer != oauthBase {
				t.Fatalf("pending = %+v; want the redirect target of the error", p)
			}
			u, err := url.Parse(application.ErrorRedirect(*p, oe))
			if err != nil {
				t.Fatal(err)
			}
			q := u.Query()
			if u.Host != "app.example.com" || u.Path != "/cb" || q.Get("tenant") != "a" || q.Get("error") != tc.code || q.Get("state") != req.State || q.Get("iss") != oauthBase || q.Has("code") {
				t.Fatalf("error redirect = %s", u)
			}
		})
	}
}

func TestOAuthPrepareResourceAndScopes(t *testing.T) {
	f := newOAuth()
	c := f.register(t, appRedirect)
	cases := []struct {
		resource, scope string
		scopes          []domain.APIKeyScope
	}{
		{oauthResource, "", v1Scopes},
		{"", "   ", v1Scopes},
		{"HTTPS://TKT.Example.COM/mcp/", "tickets:read catalog:read tickets:read", []domain.APIKeyScope{domain.ScopeCatalogRead, domain.ScopeTicketsRead}},
		{oauthResource + "/", "comments:internal", []domain.APIKeyScope{domain.ScopeCommentsInternal}},
	}
	for _, tc := range cases {
		req := authorizeRequest(c)
		req.Resource, req.Scope = tc.resource, tc.scope
		p, err := f.svc.PrepareAuthorization(t.Context(), req)
		if err != nil {
			t.Fatalf("prepare %q %q: %v", tc.resource, tc.scope, err)
		}
		want := application.PendingAuthorization{Client: *c, RedirectURI: appRedirect, State: "st-1", CodeChallenge: testChallenge, Issuer: oauthBase, Scopes: tc.scopes, Resource: oauthResource}
		if !reflect.DeepEqual(*p, want) {
			t.Fatalf("pending = %+v; want %+v", *p, want)
		}
	}
}

func TestOAuthApprove(t *testing.T) {
	t.Run("issues a code and redirects with code, state and iss", func(t *testing.T) {
		f := newOAuth()
		c := f.register(t, appRedirect+"?tenant=a%20b")
		p, err := f.svc.PrepareAuthorization(t.Context(), authorizeRequest(c))
		if err != nil {
			t.Fatal(err)
		}
		redirect, err := f.svc.Approve(t.Context(), f.person, *p)
		if err != nil {
			t.Fatal(err)
		}
		m := regexp.MustCompile(`^https://app\.example\.com/cb\?tenant=a%20b&code=(tktc_[0-9a-f]{64})&state=st-1&iss=https%3A%2F%2Ftkt\.example\.com$`).FindStringSubmatch(redirect)
		if m == nil {
			t.Fatalf("redirect = %q", redirect)
		}
		if len(f.store.codes) != 1 {
			t.Fatalf("codes = %d; want 1", len(f.store.codes))
		}
		got := *f.store.codes[0]
		now := f.clock.Now()
		want := domain.OAuthCode{ID: got.ID, CodeHash: sha256Hex(m[1]), ClientID: c.ID, UserID: f.person.ID, RedirectURI: p.RedirectURI, CodeChallenge: testChallenge, Scopes: v1Scopes, Resource: oauthResource, CreatedAt: now, ExpiresAt: now.Add(60 * time.Second)}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("stored code = %+v; want %+v", got, want)
		}
	})

	t.Run("refuses service users and inactive people", func(t *testing.T) {
		f := newOAuth()
		c := f.register(t, appRedirect)
		p, err := f.svc.PrepareAuthorization(t.Context(), authorizeRequest(c))
		if err != nil {
			t.Fatal(err)
		}
		inactive := f.users.seedRole("Gone", "gone@example.com", domain.RoleAgent, false)
		for _, actor := range []domain.User{f.users.seedService("bot", domain.RoleAgent), inactive} {
			before := f.snapshot()
			redirect, err := f.svc.Approve(t.Context(), actor, *p)
			var forbidden *domain.ForbiddenError
			if !errors.As(err, &forbidden) || redirect != "" || f.snapshot() != before {
				t.Fatalf("actor %+v: %q, %v; want ForbiddenError and nothing stored", actor, redirect, err)
			}
		}
	})

	t.Run("drops comments:internal below agent", func(t *testing.T) {
		f := newOAuth()
		user := f.users.seedRole("Ula", "ula@example.com", domain.RoleUser, true)
		c := f.register(t, appRedirect)
		f.approve(t, user, authorizeRequest(c))
		want := []domain.APIKeyScope{domain.ScopeCatalogRead, domain.ScopeTicketsRead, domain.ScopeTicketsWrite, domain.ScopeCommentsWrite}
		if got := f.store.codes[0].Scopes; !reflect.DeepEqual(got, want) {
			t.Fatalf("code scopes = %v; want %v", got, want)
		}

		req := authorizeRequest(c)
		req.Scope = "comments:internal"
		p, err := f.svc.PrepareAuthorization(t.Context(), req)
		if err != nil {
			t.Fatal(err)
		}
		before := f.snapshot()
		redirect, err := f.svc.Approve(t.Context(), user, *p)
		if err != nil || redirect != appRedirect+"?error=invalid_scope&state=st-1&iss=https%3A%2F%2Ftkt.example.com" {
			t.Fatalf("nothing left: %q, %v", redirect, err)
		}
		if f.snapshot() != before {
			t.Fatal("an empty approval stored a code")
		}
	})

	t.Run("switch off", func(t *testing.T) {
		f := newOAuth()
		c := f.register(t, appRedirect)
		p, err := f.svc.PrepareAuthorization(t.Context(), authorizeRequest(c))
		if err != nil {
			t.Fatal(err)
		}
		f.store.enabled = false
		if _, err := f.svc.Approve(t.Context(), f.person, *p); !errors.Is(err, application.ErrMCPDisabled) || len(f.store.codes) != 0 {
			t.Fatalf("switch off: err = %v, codes %d", err, len(f.store.codes))
		}
	})
}

func TestOAuthDenyRedirect(t *testing.T) {
	p := application.PendingAuthorization{RedirectURI: appRedirect, State: "s t", Issuer: oauthBase}
	if got, want := application.DenyRedirect(p), appRedirect+"?error=access_denied&state=s+t&iss=https%3A%2F%2Ftkt.example.com"; got != want {
		t.Fatalf("deny = %q; want %q", got, want)
	}
	p.RedirectURI = "http://localhost:3118/callback?x=1"
	if got, want := application.DenyRedirect(p), "http://localhost:3118/callback?x=1&error=access_denied&state=s+t&iss=https%3A%2F%2Ftkt.example.com"; got != want {
		t.Fatalf("deny = %q; want %q", got, want)
	}
}

func TestOAuthHappyPath(t *testing.T) {
	f := newOAuth()
	c := f.register(t, "http://localhost/callback")
	req := authorizeRequest(c)
	req.RedirectURI = "http://localhost:3118/callback"
	req.Scope = "tickets:read comments:write"
	code := f.approve(t, f.person, req)

	tr := codeRequest(c, code)
	tr.RedirectURI = req.RedirectURI
	tr.Resource = "HTTPS://TKT.EXAMPLE.COM/mcp/"
	tok, err := f.svc.Token(t.Context(), tr)
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^tkta_[0-9a-f]{64}$`).MatchString(tok.AccessToken) || !regexp.MustCompile(`^tktr_[0-9a-f]{64}$`).MatchString(tok.RefreshToken) ||
		tok.TokenType != "Bearer" || tok.ExpiresIn != 3600 || tok.Scope != "tickets:read comments:write" {
		t.Fatalf("token response = %+v", tok)
	}
	if len(f.store.grants) != 1 || len(f.store.tokens) != 2 {
		t.Fatalf("grants %d tokens %d; want 1 and 2", len(f.store.grants), len(f.store.tokens))
	}
	grant := *f.store.grants[0]
	now := f.clock.Now()
	wantGrant := domain.OAuthGrant{ID: grant.ID, UserID: f.person.ID, ClientID: c.ID, ClientName: "Claude", Scopes: []domain.APIKeyScope{domain.ScopeTicketsRead, domain.ScopeCommentsWrite}, Resource: oauthResource, CreatedAt: now}
	if !reflect.DeepEqual(grant, wantGrant) {
		t.Fatalf("grant = %+v; want %+v", grant, wantGrant)
	}
	access, refresh := *f.store.tokens[0], *f.store.tokens[1]
	if access.Kind != domain.OAuthTokenAccess || access.TokenHash != sha256Hex(tok.AccessToken) || !access.ExpiresAt.Equal(now.Add(time.Hour)) ||
		refresh.Kind != domain.OAuthTokenRefresh || refresh.TokenHash != sha256Hex(tok.RefreshToken) || !refresh.ExpiresAt.Equal(now.Add(30*24*time.Hour)) {
		t.Fatalf("stored tokens = %+v %+v", access, refresh)
	}

	principal, err := f.svc.Authenticate(t.Context(), tok.AccessToken)
	if err != nil || principal.User.ID != f.person.ID || principal.GrantID != grant.ID || principal.KeyID != 0 || !reflect.DeepEqual(principal.Scopes, wantGrant.Scopes) {
		t.Fatalf("principal = %+v, %v", principal, err)
	}

	f.clock.Advance(10 * time.Minute)
	rotated, err := f.svc.Token(t.Context(), application.TokenRequest{GrantType: "refresh_token", RefreshToken: tok.RefreshToken, ClientID: c.ClientID})
	if err != nil {
		t.Fatal(err)
	}
	if rotated.AccessToken == tok.AccessToken || rotated.RefreshToken == tok.RefreshToken || rotated.Scope != tok.Scope || rotated.ExpiresIn != 3600 {
		t.Fatalf("rotated = %+v", rotated)
	}
	if p, err := f.svc.Authenticate(t.Context(), rotated.AccessToken); err != nil || p.GrantID != grant.ID {
		t.Fatalf("rotated access: %+v, %v", p, err)
	}

	_, err = f.svc.Token(t.Context(), application.TokenRequest{GrantType: "refresh_token", RefreshToken: tok.RefreshToken, ClientID: c.ClientID})
	wantOAuthError(t, err, "invalid_grant")
	if g := f.store.grants[0]; g.RevokedAt == nil || g.RevokedByUserID != nil {
		t.Fatalf("grant after refresh reuse = %+v; want revoked by tkt", g)
	}
	if _, err := f.svc.Authenticate(t.Context(), rotated.AccessToken); !errors.Is(err, application.ErrAPIUnauthorized) {
		t.Fatalf("access after reuse: err = %v; want ErrAPIUnauthorized", err)
	}
	_, err = f.svc.Token(t.Context(), application.TokenRequest{GrantType: "refresh_token", RefreshToken: rotated.RefreshToken, ClientID: c.ClientID})
	wantOAuthError(t, err, "invalid_grant")
}

func TestOAuthTokenCodeInvalidGrant(t *testing.T) {
	cases := []struct {
		name    string
		arrange func(t *testing.T, f *oauthFixture, r *application.TokenRequest)
	}{
		{"unknown code", func(_ *testing.T, _ *oauthFixture, r *application.TokenRequest) {
			r.Code = "tktc_" + strings.Repeat("0", 64)
		}},
		{"malformed code", func(_ *testing.T, _ *oauthFixture, r *application.TokenRequest) { r.Code = "nope" }},
		{"expired at 60s", func(_ *testing.T, f *oauthFixture, _ *application.TokenRequest) { f.clock.Advance(60 * time.Second) }},
		{"other client", func(t *testing.T, f *oauthFixture, r *application.TokenRequest) {
			r.ClientID = f.register(t, appRedirect).ClientID
		}},
		{"redirect mismatch", func(_ *testing.T, _ *oauthFixture, r *application.TokenRequest) {
			r.RedirectURI = appRedirect + "/other"
		}},
		{"missing redirect", func(_ *testing.T, _ *oauthFixture, r *application.TokenRequest) { r.RedirectURI = "" }},
		{"short verifier", func(_ *testing.T, _ *oauthFixture, r *application.TokenRequest) { r.CodeVerifier = testVerifier[:42] }},
		{"long verifier", func(_ *testing.T, _ *oauthFixture, r *application.TokenRequest) {
			r.CodeVerifier = strings.Repeat("a", 129)
		}},
		{"verifier charset", func(_ *testing.T, _ *oauthFixture, r *application.TokenRequest) {
			r.CodeVerifier = testVerifier[:42] + "+"
		}},
		{"wrong verifier", func(_ *testing.T, _ *oauthFixture, r *application.TokenRequest) {
			r.CodeVerifier = strings.Repeat("a", 43)
		}},
		{"resource mismatch", func(_ *testing.T, _ *oauthFixture, r *application.TokenRequest) {
			r.Resource = "https://other.example.com/mcp"
		}},
		{"deactivated owner", func(_ *testing.T, f *oauthFixture, _ *application.TokenRequest) {
			f.users.users[f.person.ID].Active = false
		}},
		{"owner became a service user", func(_ *testing.T, f *oauthFixture, _ *application.TokenRequest) {
			f.users.users[f.person.ID].Kind = domain.UserKindService
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newOAuth()
			c := f.register(t, appRedirect)
			req := codeRequest(c, f.approve(t, f.person, authorizeRequest(c)))
			tc.arrange(t, f, &req)
			tok, err := f.svc.Token(t.Context(), req)
			wantOAuthError(t, err, "invalid_grant")
			if tok != nil || len(f.store.grants) != 0 || len(f.store.tokens) != 0 {
				t.Fatalf("rejected exchange stored a grant: %+v", tok)
			}
		})
	}

	t.Run("accepted at 59s", func(t *testing.T) {
		f := newOAuth()
		c := f.register(t, appRedirect)
		req := codeRequest(c, f.approve(t, f.person, authorizeRequest(c)))
		f.clock.Advance(59 * time.Second)
		req.Resource = ""
		if _, err := f.svc.Token(t.Context(), req); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("a refused code stays spent", func(t *testing.T) {
		f := newOAuth()
		c := f.register(t, appRedirect)
		req := codeRequest(c, f.approve(t, f.person, authorizeRequest(c)))
		bad := req
		bad.CodeVerifier = strings.Repeat("a", 43)
		_, err := f.svc.Token(t.Context(), bad)
		wantOAuthError(t, err, "invalid_grant")
		_, err = f.svc.Token(t.Context(), req)
		wantOAuthError(t, err, "invalid_grant")
	})
}

func TestOAuthTokenReusedCodeRevokesGrant(t *testing.T) {
	f := newOAuth()
	c := f.register(t, appRedirect)
	req := codeRequest(c, f.approve(t, f.person, authorizeRequest(c)))
	tok, err := f.svc.Token(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.svc.Token(t.Context(), req)
	wantOAuthError(t, err, "invalid_grant")
	if g := f.store.grants[0]; g.RevokedAt == nil || g.RevokedByUserID != nil || len(f.store.grants) != 1 {
		t.Fatalf("grant after code reuse = %+v; want revoked by tkt", g)
	}
	if _, err := f.svc.Authenticate(t.Context(), tok.AccessToken); !errors.Is(err, application.ErrAPIUnauthorized) {
		t.Fatalf("access after code reuse: err = %v; want ErrAPIUnauthorized", err)
	}
}

func TestOAuthTokenClientAndGrantType(t *testing.T) {
	f := newOAuth()
	c := f.register(t, appRedirect)
	req := codeRequest(c, f.approve(t, f.person, authorizeRequest(c)))

	for _, id := range []string{"", "tktcl_00000000000000000000000000000000"} {
		bad := req
		bad.ClientID = id
		_, err := f.svc.Token(t.Context(), bad)
		wantOAuthError(t, err, "invalid_client")
		bad.GrantType = "refresh_token"
		_, err = f.svc.Token(t.Context(), bad)
		wantOAuthError(t, err, "invalid_client")
	}
	for _, gt := range []string{"client_credentials", "password", "implicit"} {
		bad := req
		bad.GrantType = gt
		_, err := f.svc.Token(t.Context(), bad)
		wantOAuthError(t, err, "unsupported_grant_type")
	}
	bad := req
	bad.GrantType = ""
	_, err := f.svc.Token(t.Context(), bad)
	wantOAuthError(t, err, "invalid_request")

	f.store.enabled = false
	if _, err := f.svc.Token(t.Context(), req); !errors.Is(err, application.ErrMCPDisabled) {
		t.Fatalf("switch off: err = %v; want ErrMCPDisabled", err)
	}
	f.store.enabled = true
	f.settings.baseURL = ""
	if _, err := f.svc.Token(t.Context(), req); !errors.Is(err, application.ErrMCPDisabled) {
		t.Fatalf("no public URL: err = %v; want ErrMCPDisabled", err)
	}
	f.settings.baseURL = oauthBase

	// None of the refusals above spent the code.
	if _, err := f.svc.Token(t.Context(), req); err != nil {
		t.Fatalf("exchange after refusals: %v", err)
	}
}

func TestOAuthRefresh(t *testing.T) {
	refresh := func(c *domain.OAuthClient, token, scope string) application.TokenRequest {
		return application.TokenRequest{GrantType: "refresh_token", RefreshToken: token, ClientID: c.ClientID, Scope: scope}
	}

	t.Run("scope within the grant", func(t *testing.T) {
		f := newOAuth()
		c, tok := f.connect(t, f.person, "tickets:read comments:write")
		for _, scope := range []string{"tickets:read tickets:write", "tickets:all", "bogus"} {
			before := f.snapshot()
			_, err := f.svc.Token(t.Context(), refresh(c, tok.RefreshToken, scope))
			wantOAuthError(t, err, "invalid_scope")
			if f.snapshot() != before || f.store.tokens[1].UsedAt != nil {
				t.Fatalf("scope %q: refused refresh changed the store", scope)
			}
		}
		got, err := f.svc.Token(t.Context(), refresh(c, tok.RefreshToken, "tickets:read"))
		if err != nil || got.Scope != "tickets:read comments:write" {
			t.Fatalf("narrower scope = %+v, %v", got, err)
		}
	})

	t.Run("thirty days of inactivity", func(t *testing.T) {
		f := newOAuth()
		c, tok := f.connect(t, f.person, "")
		f.clock.Advance(30*24*time.Hour - time.Second)
		next, err := f.svc.Token(t.Context(), refresh(c, tok.RefreshToken, ""))
		if err != nil {
			t.Fatal(err)
		}
		f.clock.Advance(30*24*time.Hour - time.Second)
		if next, err = f.svc.Token(t.Context(), refresh(c, next.RefreshToken, "")); err != nil {
			t.Fatalf("second rotation: %v", err)
		}
		f.clock.Advance(30 * 24 * time.Hour)
		_, err = f.svc.Token(t.Context(), refresh(c, next.RefreshToken, ""))
		wantOAuthError(t, err, "invalid_grant")
	})

	cases := []struct {
		name    string
		arrange func(t *testing.T, f *oauthFixture, c *domain.OAuthClient, tok *application.TokenResponse) application.TokenRequest
	}{
		{"revoked grant", func(t *testing.T, f *oauthFixture, c *domain.OAuthClient, tok *application.TokenResponse) application.TokenRequest {
			if err := f.svc.RevokeGrant(t.Context(), f.person, f.store.grants[0].ID); err != nil {
				t.Fatal(err)
			}
			return refresh(c, tok.RefreshToken, "")
		}},
		{"access token", func(t *testing.T, _ *oauthFixture, c *domain.OAuthClient, tok *application.TokenResponse) application.TokenRequest {
			return refresh(c, tok.AccessToken, "")
		}},
		{"access token secret as refresh", func(t *testing.T, _ *oauthFixture, c *domain.OAuthClient, tok *application.TokenResponse) application.TokenRequest {
			return refresh(c, "tktr_"+strings.TrimPrefix(tok.AccessToken, "tkta_"), "")
		}},
		{"unknown", func(t *testing.T, _ *oauthFixture, c *domain.OAuthClient, _ *application.TokenResponse) application.TokenRequest {
			return refresh(c, "tktr_"+strings.Repeat("0", 64), "")
		}},
		{"malformed", func(t *testing.T, _ *oauthFixture, c *domain.OAuthClient, _ *application.TokenResponse) application.TokenRequest {
			return refresh(c, "tktr_XYZ", "")
		}},
		{"other client", func(t *testing.T, f *oauthFixture, _ *domain.OAuthClient, tok *application.TokenResponse) application.TokenRequest {
			return refresh(f.register(t, appRedirect), tok.RefreshToken, "")
		}},
		{"deactivated owner", func(t *testing.T, f *oauthFixture, c *domain.OAuthClient, tok *application.TokenResponse) application.TokenRequest {
			f.users.users[f.person.ID].Active = false
			return refresh(c, tok.RefreshToken, "")
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newOAuth()
			c, tok := f.connect(t, f.person, "")
			req := tc.arrange(t, f, c, tok)
			before := f.snapshot()
			got, err := f.svc.Token(t.Context(), req)
			wantOAuthError(t, err, "invalid_grant")
			if got != nil || f.snapshot() != before || f.store.tokens[1].UsedAt != nil {
				t.Fatal("refused refresh changed the store")
			}
		})
	}
}

func TestOAuthAuthenticateRejects(t *testing.T) {
	cases := []struct {
		name    string
		arrange func(t *testing.T, f *oauthFixture, tok *application.TokenResponse) string
		want    error
	}{
		{"switch off", func(t *testing.T, f *oauthFixture, tok *application.TokenResponse) string {
			f.store.enabled = false
			return tok.AccessToken
		}, application.ErrMCPDisabled},
		{"no public URL", func(t *testing.T, f *oauthFixture, tok *application.TokenResponse) string {
			f.settings.baseURL = ""
			return tok.AccessToken
		}, application.ErrMCPDisabled},
		{"empty", func(*testing.T, *oauthFixture, *application.TokenResponse) string { return "" }, application.ErrAPIUnauthorized},
		{"malformed", func(*testing.T, *oauthFixture, *application.TokenResponse) string { return "tkta_xyz" }, application.ErrAPIUnauthorized},
		{"uppercase hex", func(t *testing.T, _ *oauthFixture, tok *application.TokenResponse) string {
			return strings.ToUpper(tok.AccessToken)
		}, application.ErrAPIUnauthorized},
		{"api key shape", func(*testing.T, *oauthFixture, *application.TokenResponse) string {
			return "tkt_000000000000_" + strings.Repeat("0", 64)
		}, application.ErrAPIUnauthorized},
		{"unknown", func(*testing.T, *oauthFixture, *application.TokenResponse) string {
			return "tkta_" + strings.Repeat("0", 64)
		}, application.ErrAPIUnauthorized},
		{"refresh token", func(t *testing.T, _ *oauthFixture, tok *application.TokenResponse) string { return tok.RefreshToken }, application.ErrAPIUnauthorized},
		{"refresh secret as access", func(t *testing.T, _ *oauthFixture, tok *application.TokenResponse) string {
			return "tkta_" + strings.TrimPrefix(tok.RefreshToken, "tktr_")
		}, application.ErrAPIUnauthorized},
		{"expired", func(t *testing.T, f *oauthFixture, tok *application.TokenResponse) string {
			f.clock.Advance(time.Hour)
			return tok.AccessToken
		}, application.ErrAPIUnauthorized},
		{"revoked grant", func(t *testing.T, f *oauthFixture, tok *application.TokenResponse) string {
			if err := f.svc.RevokeGrant(t.Context(), f.person, f.store.grants[0].ID); err != nil {
				t.Fatal(err)
			}
			return tok.AccessToken
		}, application.ErrAPIUnauthorized},
		{"public URL changed", func(t *testing.T, f *oauthFixture, tok *application.TokenResponse) string {
			f.settings.baseURL = "https://new.example.com"
			return tok.AccessToken
		}, application.ErrAPIUnauthorized},
		{"inactive owner", func(t *testing.T, f *oauthFixture, tok *application.TokenResponse) string {
			f.users.users[f.person.ID].Active = false
			return tok.AccessToken
		}, application.ErrAPIUnauthorized},
		{"owner became a service user", func(t *testing.T, f *oauthFixture, tok *application.TokenResponse) string {
			f.users.users[f.person.ID].Kind = domain.UserKindService
			return tok.AccessToken
		}, application.ErrAPIUnauthorized},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newOAuth()
			_, tok := f.connect(t, f.person, "")
			token := tc.arrange(t, f, tok)
			before := f.snapshot()
			p, err := f.svc.Authenticate(t.Context(), token)
			if !errors.Is(err, tc.want) || p != nil {
				t.Fatalf("Authenticate = %+v, %v; want %v", p, err, tc.want)
			}
			if f.snapshot() != before || f.store.grants[0].LastUsedAt != nil {
				t.Fatal("a rejected token wrote to the store")
			}
		})
	}

	t.Run("accepted one second before expiry", func(t *testing.T) {
		f := newOAuth()
		_, tok := f.connect(t, f.person, "")
		f.clock.Advance(time.Hour - time.Second)
		if _, err := f.svc.Authenticate(t.Context(), tok.AccessToken); err != nil {
			t.Fatal(err)
		}
	})
}

func TestOAuthAuthenticateCapabilities(t *testing.T) {
	f := newOAuth()
	_, all := f.connect(t, f.person, "")
	_, read := f.connect(t, f.person, "tickets:read")

	p, err := f.svc.Authenticate(t.Context(), all.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p.Scopes, v1Scopes) || !p.Capabilities.Require(application.CapCommentInternal) || !p.Capabilities.Require(application.CapAssignTicket) || p.Capabilities.Require(application.CapManageUsers) {
		t.Fatalf("agent principal = %+v", p)
	}
	p, err = f.svc.Authenticate(t.Context(), read.AccessToken)
	if err != nil || p.Capabilities.Require(application.CapCreateTicket) || p.Capabilities.Require(application.CapCommentPublic) {
		t.Fatalf("read-only principal = %+v, %v", p, err)
	}

	// A person demoted to user after approving keeps the grant's scopes but
	// loses what the role no longer allows.
	f.users.users[f.person.ID].Role = domain.RoleUser
	p, err = f.svc.Authenticate(t.Context(), all.AccessToken)
	if err != nil || p.User.Role != domain.RoleUser || !p.HasScope(domain.ScopeCommentsInternal) ||
		p.Capabilities.Require(application.CapCommentInternal) || p.Capabilities.Require(application.CapAssignTicket) ||
		!p.Capabilities.Require(application.CapCommentPublic) || !p.Capabilities.Require(application.CapCreateTicket) {
		t.Fatalf("demoted principal = %+v, %v", p, err)
	}
}

func TestOAuthAuthenticateTouchesOncePerMinute(t *testing.T) {
	f := newOAuth()
	_, tok := f.connect(t, f.person, "")
	auth := func() {
		t.Helper()
		if _, err := f.svc.Authenticate(t.Context(), tok.AccessToken); err != nil {
			t.Fatal(err)
		}
	}
	start := f.clock.Now()
	auth()
	auth()
	f.clock.Advance(59 * time.Second)
	auth()
	if f.store.touches != 1 || !f.store.grants[0].LastUsedAt.Equal(start) {
		t.Fatalf("touches = %d, last used %v; want 1 at %v", f.store.touches, f.store.grants[0].LastUsedAt, start)
	}
	f.clock.Advance(time.Second)
	auth()
	if f.store.touches != 2 || !f.store.grants[0].LastUsedAt.Equal(f.clock.Now()) {
		t.Fatalf("touches = %d; want 2 after a minute", f.store.touches)
	}
}

func TestOAuthGrantsOwnOnly(t *testing.T) {
	f := newOAuth()
	other := f.users.seedRole("Bea", "bea@example.com", domain.RoleUser, true)
	f.connect(t, f.person, "")
	f.clock.Advance(time.Minute)
	f.connect(t, f.person, "tickets:read")
	f.connect(t, other, "")
	mine, theirs := f.store.grants[1].ID, f.store.grants[2].ID

	grants, err := f.svc.ListGrants(t.Context(), f.person)
	if err != nil || len(grants) != 2 || grants[0].ID != mine || grants[1].ID != f.store.grants[0].ID || grants[0].ClientName != "Claude" {
		t.Fatalf("ListGrants = %+v, %v", grants, err)
	}

	if err := f.svc.RevokeGrant(t.Context(), f.person, theirs); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("revoke someone else's grant: err = %v; want NotFound", err)
	}
	if f.store.grants[2].RevokedAt != nil {
		t.Fatal("someone else's grant was revoked")
	}
	if err := f.svc.RevokeGrant(t.Context(), f.person, mine); err != nil {
		t.Fatal(err)
	}
	if g := f.store.grants[1]; g.RevokedAt == nil || g.RevokedByUserID == nil || *g.RevokedByUserID != f.person.ID {
		t.Fatalf("revoked grant = %+v", g)
	}
	if err := f.svc.RevokeGrant(t.Context(), f.person, mine); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("second revoke: err = %v; want NotFound", err)
	}
	grants, err = f.svc.ListGrants(t.Context(), f.person)
	if err != nil || len(grants) != 1 || grants[0].ID != f.store.grants[0].ID {
		t.Fatalf("ListGrants after revoke = %+v, %v", grants, err)
	}
	grants, err = f.svc.ListGrants(t.Context(), other)
	if err != nil || len(grants) != 1 || grants[0].ID != theirs {
		t.Fatalf("other's grants = %+v, %v", grants, err)
	}
}
