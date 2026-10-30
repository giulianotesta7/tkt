package httpadapter

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"html/template"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
	"github.com/giulianotesta7/tkt/internal/i18n"
)

// The MCP authorization server over HTTP (issue #493, S2-S6). Every request
// runs through h.handler, the same chain production serves: the API branch, the
// OAuth branch (machine endpoints, before any session), then the session
// middleware and the mux that holds the consent page.

const (
	oauthPublicURL = "https://tkt.example.test"
	oauthResource  = oauthPublicURL + "/mcp"
	oauthRedirect  = "https://app.example.com/callback"
	oauthState     = "st-123"
	oauthVerifier  = "verifier-0123456789-abcdefghijklmnopqrstuvwxyz-ABCDEFG"
)

var oauthAllScopes = []any{"catalog:read", "tickets:read", "tickets:write", "comments:write", "comments:internal"}

// enableMCP sets the public URL and turns the switch on through the root-only
// use case, the way Settings will.
func enableMCP(t *testing.T, h *harness) {
	t.Helper()
	setPublicURL(t, h, oauthPublicURL)
	root, _ := seedRoot(t, h)
	if err := h.oauth.SetMCPEnabled(t.Context(), *root, true); err != nil {
		t.Fatalf("enable MCP: %v", err)
	}
}

func setPublicURL(t *testing.T, h *harness, base string) {
	t.Helper()
	if err := h.store.EmailSettingsStore().SetEmailSettings(t.Context(), application.EmailSettings{BaseURL: base}); err != nil {
		t.Fatalf("set public URL: %v", err)
	}
}

func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// registerOAuthClient registers a client through the real use case.
func registerOAuthClient(t *testing.T, h *harness, name string, redirects ...string) *domain.OAuthClient {
	t.Helper()
	c, err := h.oauth.RegisterClient(t.Context(), application.ClientRegistration{ClientName: name, RedirectURIs: redirects})
	if err != nil {
		t.Fatalf("register client: %v", err)
	}
	return c
}

// authorizeParams is a valid authorization request; mod may change any field.
func authorizeParams(clientID, redirect string, mod func(url.Values)) url.Values {
	v := url.Values{
		"client_id":             {clientID},
		"redirect_uri":          {redirect},
		"response_type":         {"code"},
		"state":                 {oauthState},
		"code_challenge":        {pkceChallenge(oauthVerifier)},
		"code_challenge_method": {"S256"},
		"scope":                 {"tickets:read tickets:write"},
		"resource":              {oauthResource},
	}
	if mod != nil {
		mod(v)
	}
	return v
}

// serve runs one request through the full production chain.
func (h *harness) serve(req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)
	return rec
}

func (h *harness) oauthGet(target, sessionID string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	if sessionID != "" {
		req.Header.Set("Cookie", sessionCookie+"="+sessionID)
	}
	return h.serve(req)
}

func (h *harness) oauthPostForm(target string, form url.Values, sessionID string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if sessionID != "" {
		req.Header.Set("Cookie", sessionCookie+"="+sessionID)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	return h.serve(req)
}

func (h *harness) oauthPostJSON(target, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return h.serve(req)
}

func decodeJSONBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode JSON body %q: %v", rec.Body.String(), err)
	}
	return out
}

// wantOAuthError asserts an RFC 6749 JSON error answer.
func wantOAuthError(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d, want %d: %s", rec.Code, status, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	if got := decodeJSONBody(t, rec)["error"]; got != code {
		t.Errorf("error = %v, want %q", got, code)
	}
}

// approveCode runs the consent approval as the harness admin and answers the
// issued code.
func approveCode(t *testing.T, h *harness, clientID, redirect string) string {
	t.Helper()
	form := authorizeParams(clientID, redirect, nil)
	form.Set("decision", "approve")
	rec := h.oauthPostForm("/oauth/authorize", form, h.adminSession.ID, nil)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("approve status = %d: %s", rec.Code, rec.Body.String())
	}
	loc, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse Location: %v", err)
	}
	return loc.Query().Get("code")
}

func TestOAuthSwitchOffAnswersNotFoundEverywhere(t *testing.T) {
	check := func(t *testing.T, h *harness) {
		t.Helper()
		for _, target := range []string{"/.well-known/oauth-protected-resource", "/.well-known/oauth-protected-resource/mcp", "/.well-known/oauth-authorization-server"} {
			rec := h.oauthGet(target, "")
			if rec.Code != http.StatusNotFound || strings.TrimSpace(rec.Body.String()) != `{"error":"not_found"}` {
				t.Errorf("GET %s = %d %q, want 404 not_found", target, rec.Code, rec.Body.String())
			}
		}
		if rec := h.oauthPostJSON("/oauth/register", `{"redirect_uris":["`+oauthRedirect+`"]}`); rec.Code != http.StatusNotFound || strings.TrimSpace(rec.Body.String()) != `{"error":"not_found"}` {
			t.Errorf("POST /oauth/register = %d %q, want 404", rec.Code, rec.Body.String())
		}
		if rec := h.oauthPostForm("/oauth/token", url.Values{"grant_type": {"authorization_code"}}, "", nil); rec.Code != http.StatusNotFound || strings.TrimSpace(rec.Body.String()) != `{"error":"not_found"}` {
			t.Errorf("POST /oauth/token = %d %q, want 404", rec.Code, rec.Body.String())
		}
		query := authorizeParams("tktcl_x", oauthRedirect, nil)
		if rec := h.oauthGet("/oauth/authorize?"+query.Encode(), h.adminSession.ID); rec.Code != http.StatusNotFound || rec.Header().Get("Location") != "" {
			t.Errorf("GET /oauth/authorize = %d (Location %q), want 404", rec.Code, rec.Header().Get("Location"))
		}
		query.Set("decision", "approve")
		if rec := h.oauthPostForm("/oauth/authorize", query, h.adminSession.ID, nil); rec.Code != http.StatusNotFound || rec.Header().Get("Location") != "" {
			t.Errorf("POST /oauth/authorize = %d (Location %q), want 404", rec.Code, rec.Header().Get("Location"))
		}
	}
	t.Run("switch off", func(t *testing.T) {
		h := newHarness(t)
		setPublicURL(t, h, oauthPublicURL)
		check(t, h)
	})
	t.Run("public URL cleared after the switch went on", func(t *testing.T) {
		h := newHarness(t)
		enableMCP(t, h)
		setPublicURL(t, h, "")
		check(t, h)
	})
}

func TestOAuthMetadataDocuments(t *testing.T) {
	h := newHarness(t)
	enableMCP(t, h)

	wantResource := map[string]any{
		"resource":                 oauthResource,
		"authorization_servers":    []any{oauthPublicURL},
		"scopes_supported":         oauthAllScopes,
		"bearer_methods_supported": []any{"header"},
		"resource_name":            "tkt",
	}
	for _, target := range []string{"/.well-known/oauth-protected-resource", "/.well-known/oauth-protected-resource/mcp"} {
		rec := h.oauthGet(target, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d: %s", target, rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
			t.Errorf("GET %s CORS = %q, want *", target, got)
		}
		if got := rec.Header().Get("Content-Type"); got != "application/json" {
			t.Errorf("GET %s Content-Type = %q", target, got)
		}
		if got := decodeJSONBody(t, rec); !reflect.DeepEqual(got, wantResource) {
			t.Errorf("GET %s = %v, want %v", target, got, wantResource)
		}
	}

	rec := h.oauthGet("/.well-known/oauth-authorization-server", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET AS metadata = %d: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("AS metadata CORS = %q, want *", got)
	}
	wantServer := map[string]any{
		"issuer":                                         oauthPublicURL,
		"authorization_endpoint":                         oauthPublicURL + "/oauth/authorize",
		"token_endpoint":                                 oauthPublicURL + "/oauth/token",
		"registration_endpoint":                          oauthPublicURL + "/oauth/register",
		"response_types_supported":                       []any{"code"},
		"grant_types_supported":                          []any{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":               []any{"S256"},
		"token_endpoint_auth_methods_supported":          []any{"none"},
		"scopes_supported":                               oauthAllScopes,
		"authorization_response_iss_parameter_supported": true,
	}
	if got := decodeJSONBody(t, rec); !reflect.DeepEqual(got, wantServer) {
		t.Errorf("AS metadata = %v, want %v", got, wantServer)
	}

	for _, target := range []string{"/oauth/token", "/oauth/register", "/.well-known/oauth-authorization-server"} {
		req := httptest.NewRequest(http.MethodOptions, target, nil)
		req.Header.Set("Origin", "https://claude.ai")
		req.Header.Set("Access-Control-Request-Method", "POST")
		rec := h.serve(req)
		if rec.Code != http.StatusNoContent {
			t.Errorf("OPTIONS %s = %d, want 204", target, rec.Code)
		}
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
			t.Errorf("OPTIONS %s allow origin = %q", target, got)
		}
		if got := rec.Header().Get("Access-Control-Allow-Headers"); got != "Authorization, Content-Type, MCP-Protocol-Version" {
			t.Errorf("OPTIONS %s allow headers = %q", target, got)
		}
	}

	// A metadata document is read-only.
	if rec := h.oauthPostJSON("/.well-known/oauth-authorization-server", `{}`); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST AS metadata = %d, want 405", rec.Code)
	}
}

func TestOAuthRegisterClient(t *testing.T) {
	h := newHarness(t)
	enableMCP(t, h)

	rec := h.oauthPostJSON("/oauth/register", `{"client_name":"Claude","redirect_uris":["https://claude.ai/api/mcp/auth_callback","http://localhost:4567/callback"],"token_endpoint_auth_method":"none","grant_types":["authorization_code","refresh_token"],"response_types":["code"],"logo_uri":"ignored"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("register = %d: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("register CORS = %q", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("register Cache-Control = %q, want no-store", got)
	}
	body := decodeJSONBody(t, rec)
	clientID, _ := body["client_id"].(string)
	if !strings.HasPrefix(clientID, "tktcl_") {
		t.Fatalf("client_id = %q", clientID)
	}
	stored, err := h.store.OAuthStore().ClientByClientID(t.Context(), clientID)
	if err != nil {
		t.Fatalf("registered client not stored: %v", err)
	}
	want := map[string]any{
		"client_id":                  clientID,
		"client_id_issued_at":        float64(stored.CreatedAt.Unix()),
		"client_name":                "Claude",
		"redirect_uris":              []any{"https://claude.ai/api/mcp/auth_callback", "http://localhost:4567/callback"},
		"grant_types":                []any{"authorization_code", "refresh_token"},
		"response_types":             []any{"code"},
		"token_endpoint_auth_method": "none",
	}
	if !reflect.DeepEqual(body, want) {
		t.Errorf("register body = %v, want %v", body, want)
	}
	if stored.Name != "Claude" || !reflect.DeepEqual(stored.RedirectURIs, []string{"https://claude.ai/api/mcp/auth_callback", "http://localhost:4567/callback"}) {
		t.Errorf("stored client = %+v", stored)
	}

	// The defaults a minimal registration answers.
	rec = h.oauthPostJSON("/oauth/register", `{"redirect_uris":["cursor://anysphere.cursor-mcp/oauth/callback"]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("minimal register = %d: %s", rec.Code, rec.Body.String())
	}
	body = decodeJSONBody(t, rec)
	if body["client_name"] != "MCP client" || !reflect.DeepEqual(body["grant_types"], []any{"authorization_code", "refresh_token"}) || !reflect.DeepEqual(body["response_types"], []any{"code"}) {
		t.Errorf("minimal register body = %v", body)
	}

	wantOAuthError(t, h.oauthPostJSON("/oauth/register", `{"redirect_uris":["http://example.com/callback"]}`), http.StatusBadRequest, "invalid_redirect_uri")
	wantOAuthError(t, h.oauthPostJSON("/oauth/register", `{"redirect_uris":["`+oauthRedirect+`"],"token_endpoint_auth_method":"client_secret_basic"}`), http.StatusBadRequest, "invalid_client_metadata")
	wantOAuthError(t, h.oauthPostJSON("/oauth/register", `{"redirect_uris":`), http.StatusBadRequest, "invalid_client_metadata")

	req := httptest.NewRequest(http.MethodPost, "/oauth/register", strings.NewReader(`redirect_uris=`+url.QueryEscape(oauthRedirect)))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	wantOAuthError(t, h.serve(req), http.StatusBadRequest, "invalid_client_metadata")

	big := `{"client_name":"` + strings.Repeat("a", 64<<10) + `","redirect_uris":["` + oauthRedirect + `"]}`
	rec = h.oauthPostJSON("/oauth/register", big)
	wantOAuthError(t, rec, http.StatusRequestEntityTooLarge, "invalid_request")

	if rec := h.oauthGet("/oauth/register", ""); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET /oauth/register = %d, want 405", rec.Code)
	}
}

func TestOAuthRegisterIsRateLimited(t *testing.T) {
	h := newHarness(t)
	enableMCP(t, h)
	body := `{"redirect_uris":["` + oauthRedirect + `"]}`
	for i := 0; i < 30; i++ {
		if rec := h.oauthPostJSON("/oauth/register", body); rec.Code != http.StatusCreated {
			t.Fatalf("registration %d = %d, want 201 within the burst", i+1, rec.Code)
		}
	}
	rec := h.oauthPostJSON("/oauth/register", body)
	wantOAuthError(t, rec, http.StatusTooManyRequests, "slow_down")
	if got := rec.Header().Get("Retry-After"); got != "2" {
		t.Errorf("Retry-After = %q, want 2", got)
	}
	h.clock.Advance(2 * time.Second)
	if rec := h.oauthPostJSON("/oauth/register", body); rec.Code != http.StatusCreated {
		t.Errorf("after the refill = %d, want 201", rec.Code)
	}
}

func TestOAuthAuthorizeAnonymousGoesToLogin(t *testing.T) {
	h := newHarness(t)
	enableMCP(t, h)
	client := registerOAuthClient(t, h, "Claude", oauthRedirect)
	target := "/oauth/authorize?" + authorizeParams(client.ClientID, oauthRedirect, nil).Encode()
	wantRedirect(t, h.oauthGet(target, ""), http.StatusSeeOther, "/login?next="+url.QueryEscape(target))
}

func TestOAuthAuthorizeShowsTheConsentPage(t *testing.T) {
	h := newHarness(t)
	enableMCP(t, h)
	name := `<script>alert(1)</script> Agent`
	client := registerOAuthClient(t, h, name, oauthRedirect)
	params := authorizeParams(client.ClientID, oauthRedirect, func(v url.Values) {
		v.Set("scope", "catalog:read tickets:read comments:internal")
	})
	rec := h.oauthGet("/oauth/authorize?"+params.Encode(), h.adminSession.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("consent = %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, "<script>alert(1)") {
		t.Fatalf("the client name is rendered unescaped: %s", body)
	}
	for _, want := range []string{
		template.HTMLEscapeString(name),
		"app.example.com",
		h.admin.Name,
		template.HTMLEscapeString(i18n.T(i18n.English, "oauth.scope.catalog_read")),
		template.HTMLEscapeString(i18n.T(i18n.English, "oauth.scope.tickets_read")),
		template.HTMLEscapeString(i18n.T(i18n.English, "oauth.scope.comments_internal")),
		`<form class="preferences-actions" method="post" action="/oauth/authorize">`,
		`<input type="hidden" name="client_id" value="` + client.ClientID + `">`,
		`<input type="hidden" name="state" value="` + oauthState + `">`,
		`name="decision" value="approve"`,
		`name="decision" value="deny"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("consent page lacks %q", want)
		}
	}
	if strings.Contains(body, template.HTMLEscapeString(i18n.T(i18n.English, "oauth.scope.tickets_write"))) {
		t.Errorf("consent page lists a scope the client did not ask for")
	}
}

// TestOAuthConsentPageRefusesFraming pins the clickjacking defence (RFC 6819
// §4.4.1.9): a page on another site must not frame the consent page and trick
// the person into approving. The error page carries it too.
func TestOAuthConsentPageRefusesFraming(t *testing.T) {
	h := newHarness(t)
	enableMCP(t, h)
	client := registerOAuthClient(t, h, "Agent", oauthRedirect)
	for name, target := range map[string]string{
		"consent": "/oauth/authorize?" + authorizeParams(client.ClientID, oauthRedirect, nil).Encode(),
		"error":   "/oauth/authorize?" + authorizeParams("unknown", oauthRedirect, nil).Encode(),
	} {
		rec := h.oauthGet(target, h.adminSession.ID)
		if got := rec.Header().Get("X-Frame-Options"); got != "DENY" {
			t.Errorf("%s: X-Frame-Options = %q, want DENY", name, got)
		}
		if got := rec.Header().Get("Content-Security-Policy"); got != "frame-ancestors 'none'" {
			t.Errorf("%s: Content-Security-Policy = %q, want frame-ancestors 'none'", name, got)
		}
	}
}

func TestOAuthAuthorizeUntrustedRequestShowsAnErrorPage(t *testing.T) {
	h := newHarness(t)
	enableMCP(t, h)
	client := registerOAuthClient(t, h, "Claude", oauthRedirect)
	for name, params := range map[string]url.Values{
		"unknown client":           authorizeParams("tktcl_00000000000000000000000000000000", oauthRedirect, nil),
		"unregistered redirect":    authorizeParams(client.ClientID, "https://evil.example/callback", nil),
		"missing redirect":         authorizeParams(client.ClientID, "", nil),
		"missing client and plain": authorizeParams("", oauthRedirect, func(v url.Values) { v.Set("code_challenge_method", "plain") }),
	} {
		t.Run(name, func(t *testing.T) {
			rec := h.oauthGet("/oauth/authorize?"+params.Encode(), h.adminSession.ID)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
			}
			if loc := rec.Header().Get("Location"); loc != "" {
				t.Fatalf("an untrusted request redirected to %q", loc)
			}
			if !strings.Contains(rec.Body.String(), i18n.T(i18n.English, "oauth.error.title")) {
				t.Errorf("error page lacks its title: %s", rec.Body.String())
			}
		})
	}
}

func TestOAuthAuthorizeRedirectableErrorReturnsToTheClient(t *testing.T) {
	h := newHarness(t)
	enableMCP(t, h)
	client := registerOAuthClient(t, h, "Claude", oauthRedirect)
	for name, tc := range map[string]struct {
		mod  func(url.Values)
		code string
	}{
		"plain PKCE":      {func(v url.Values) { v.Set("code_challenge_method", "plain") }, "invalid_request"},
		"tickets:all":     {func(v url.Values) { v.Set("scope", "tickets:all") }, "invalid_scope"},
		"other resource":  {func(v url.Values) { v.Set("resource", "https://other.example/mcp") }, "invalid_target"},
		"token flow":      {func(v url.Values) { v.Set("response_type", "token") }, "unsupported_response_type"},
		"missing PKCE":    {func(v url.Values) { v.Del("code_challenge") }, "invalid_request"},
		"missing a state": {func(v url.Values) { v.Del("state") }, "invalid_request"},
	} {
		t.Run(name, func(t *testing.T) {
			params := authorizeParams(client.ClientID, oauthRedirect, tc.mod)
			rec := h.oauthGet("/oauth/authorize?"+params.Encode(), h.adminSession.ID)
			if rec.Code != http.StatusSeeOther {
				t.Fatalf("status = %d, want 303: %s", rec.Code, rec.Body.String())
			}
			loc, err := url.Parse(rec.Header().Get("Location"))
			if err != nil {
				t.Fatalf("parse Location: %v", err)
			}
			if got := loc.Scheme + "://" + loc.Host + loc.Path; got != oauthRedirect {
				t.Errorf("redirected to %q, want %q", got, oauthRedirect)
			}
			q := loc.Query()
			if q.Get("error") != tc.code || q.Get("state") != params.Get("state") || q.Get("iss") != oauthPublicURL || q.Get("code") != "" {
				t.Errorf("redirect query = %v, want error=%s state=%q iss=%s", q, tc.code, params.Get("state"), oauthPublicURL)
			}
		})
	}
}

func TestOAuthApproveIssuesACodeTheTokenEndpointExchanges(t *testing.T) {
	h := newHarness(t)
	enableMCP(t, h)
	client := registerOAuthClient(t, h, "Claude", oauthRedirect)

	form := authorizeParams(client.ClientID, oauthRedirect, nil)
	form.Set("decision", "approve")
	rec := h.oauthPostForm("/oauth/authorize", form, h.adminSession.ID, map[string]string{"Origin": "http://example.com"})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("approve = %d: %s", rec.Code, rec.Body.String())
	}
	loc, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse Location: %v", err)
	}
	code := loc.Query().Get("code")
	wantLoc := oauthRedirect + "?code=" + code + "&state=" + oauthState + "&iss=" + url.QueryEscape(oauthPublicURL)
	if !strings.HasPrefix(code, "tktc_") || rec.Header().Get("Location") != wantLoc {
		t.Fatalf("Location = %q, want %q", rec.Header().Get("Location"), wantLoc)
	}

	rec = h.oauthPostForm("/oauth/token", url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {oauthRedirect},
		"client_id":     {client.ClientID},
		"code_verifier": {oauthVerifier},
		"resource":      {oauthResource},
	}, "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("token = %d: %s", rec.Code, rec.Body.String())
	}
	for header, want := range map[string]string{"Cache-Control": "no-store", "Pragma": "no-cache", "Content-Type": "application/json", "Access-Control-Allow-Origin": "*"} {
		if got := rec.Header().Get(header); got != want {
			t.Errorf("token %s = %q, want %q", header, got, want)
		}
	}
	tokens := decodeJSONBody(t, rec)
	access, _ := tokens["access_token"].(string)
	refresh, _ := tokens["refresh_token"].(string)
	if !strings.HasPrefix(access, "tkta_") || !strings.HasPrefix(refresh, "tktr_") || tokens["token_type"] != "Bearer" || tokens["expires_in"] != float64(3600) || tokens["scope"] != "tickets:read tickets:write" || len(tokens) != 5 {
		t.Fatalf("token response = %v", tokens)
	}
	p, err := h.oauth.Authenticate(t.Context(), access)
	if err != nil || p.User.ID != h.admin.ID {
		t.Fatalf("the issued access token does not act as the approver: %v %+v", err, p)
	}

	// Refresh rotates; replaying the spent refresh token revokes the grant.
	refreshForm := func(token string) url.Values {
		return url.Values{"grant_type": {"refresh_token"}, "refresh_token": {token}, "client_id": {client.ClientID}}
	}
	rec = h.oauthPostForm("/oauth/token", refreshForm(refresh), "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("refresh = %d: %s", rec.Code, rec.Body.String())
	}
	rotated := decodeJSONBody(t, rec)
	newRefresh, _ := rotated["refresh_token"].(string)
	newAccess, _ := rotated["access_token"].(string)
	if newRefresh == refresh || !strings.HasPrefix(newRefresh, "tktr_") || newAccess == access {
		t.Fatalf("refresh did not rotate: %v", rotated)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("refresh Cache-Control = %q", rec.Header().Get("Cache-Control"))
	}
	wantOAuthError(t, h.oauthPostForm("/oauth/token", refreshForm(refresh), "", nil), http.StatusBadRequest, "invalid_grant")
	wantOAuthError(t, h.oauthPostForm("/oauth/token", refreshForm(newRefresh), "", nil), http.StatusBadRequest, "invalid_grant")
	if _, err := h.oauth.Authenticate(t.Context(), newAccess); err == nil {
		t.Errorf("a replayed refresh token left the grant usable")
	}

	// The code is single-use.
	rec = h.oauthPostForm("/oauth/token", url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {oauthRedirect}, "client_id": {client.ClientID}, "code_verifier": {oauthVerifier}}, "", nil)
	wantOAuthError(t, rec, http.StatusBadRequest, "invalid_grant")
}

func TestOAuthDenyReturnsAccessDenied(t *testing.T) {
	h := newHarness(t)
	enableMCP(t, h)
	client := registerOAuthClient(t, h, "Claude", oauthRedirect)
	form := authorizeParams(client.ClientID, oauthRedirect, nil)
	form.Set("decision", "deny")
	wantRedirect(t, h.oauthPostForm("/oauth/authorize", form, h.adminSession.ID, nil), http.StatusSeeOther,
		oauthRedirect+"?error=access_denied&state="+oauthState+"&iss="+url.QueryEscape(oauthPublicURL))
}

func TestOAuthApproveRefusesTamperedAndForgedPosts(t *testing.T) {
	h := newHarness(t)
	enableMCP(t, h)
	client := registerOAuthClient(t, h, "Claude", oauthRedirect)

	t.Run("redirect_uri swapped for an unregistered one", func(t *testing.T) {
		form := authorizeParams(client.ClientID, "https://evil.example/callback", nil)
		form.Set("decision", "approve")
		rec := h.oauthPostForm("/oauth/authorize", form, h.adminSession.ID, nil)
		if rec.Code != http.StatusBadRequest || rec.Header().Get("Location") != "" {
			t.Fatalf("tampered approve = %d (Location %q), want 400 and no redirect", rec.Code, rec.Header().Get("Location"))
		}
		if !strings.Contains(rec.Body.String(), i18n.T(i18n.English, "oauth.error.title")) {
			t.Errorf("tampered approve did not render the error page")
		}
	})
	t.Run("cross-site origin", func(t *testing.T) {
		form := authorizeParams(client.ClientID, oauthRedirect, nil)
		form.Set("decision", "approve")
		rec := h.oauthPostForm("/oauth/authorize", form, h.adminSession.ID, map[string]string{"Origin": "https://evil.example"})
		if rec.Code != http.StatusForbidden || rec.Header().Get("Location") != "" {
			t.Fatalf("cross-site approve = %d (Location %q), want 403", rec.Code, rec.Header().Get("Location"))
		}
	})
	t.Run("unknown decision", func(t *testing.T) {
		form := authorizeParams(client.ClientID, oauthRedirect, nil)
		form.Set("decision", "maybe")
		rec := h.oauthPostForm("/oauth/authorize", form, h.adminSession.ID, nil)
		if rec.Code != http.StatusBadRequest || rec.Header().Get("Location") != "" {
			t.Fatalf("unknown decision = %d (Location %q), want 400", rec.Code, rec.Header().Get("Location"))
		}
	})
}

func TestOAuthPrivateUseSchemeRedirect(t *testing.T) {
	h := newHarness(t)
	enableMCP(t, h)
	const cursor = "cursor://anysphere.cursor-mcp/oauth/callback"
	client := registerOAuthClient(t, h, "Cursor", cursor)

	code := approveCode(t, h, client.ClientID, cursor)
	form := authorizeParams(client.ClientID, cursor, nil)
	form.Set("decision", "approve")
	// A second approval issues a second code: assert its Location exactly.
	rec := h.oauthPostForm("/oauth/authorize", form, h.adminSession.ID, nil)
	loc, _ := url.Parse(rec.Header().Get("Location"))
	second := loc.Query().Get("code")
	wantRedirect(t, rec, http.StatusSeeOther, cursor+"?code="+second+"&state="+oauthState+"&iss="+url.QueryEscape(oauthPublicURL))
	if code == "" || second == code {
		t.Errorf("codes = %q, %q", code, second)
	}

	form.Set("decision", "deny")
	wantRedirect(t, h.oauthPostForm("/oauth/authorize", form, h.adminSession.ID, nil), http.StatusSeeOther,
		cursor+"?error=access_denied&state="+oauthState+"&iss="+url.QueryEscape(oauthPublicURL))
}

func TestOAuthTokenErrors(t *testing.T) {
	h := newHarness(t)
	enableMCP(t, h)
	client := registerOAuthClient(t, h, "Claude", oauthRedirect)
	code := approveCode(t, h, client.ClientID, oauthRedirect)

	req := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(`{"grant_type":"authorization_code","code":"`+code+`"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := h.serve(req)
	wantOAuthError(t, rec, http.StatusBadRequest, "invalid_request")
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("error Cache-Control = %q, want no-store", rec.Header().Get("Cache-Control"))
	}

	// A query string is not a token request body.
	rec = h.serve(httptest.NewRequest(http.MethodPost, "/oauth/token?grant_type=authorization_code&code="+code, nil))
	wantOAuthError(t, rec, http.StatusBadRequest, "invalid_request")

	wantOAuthError(t, h.oauthPostForm("/oauth/token", url.Values{"code": {code}}, "", nil), http.StatusBadRequest, "invalid_request")
	wantOAuthError(t, h.oauthPostForm("/oauth/token", url.Values{"grant_type": {"password"}, "client_id": {client.ClientID}}, "", nil), http.StatusBadRequest, "unsupported_grant_type")
	wantOAuthError(t, h.oauthPostForm("/oauth/token", url.Values{"grant_type": {"authorization_code"}, "code": {code}, "client_id": {"tktcl_00000000000000000000000000000000"}, "redirect_uri": {oauthRedirect}, "code_verifier": {oauthVerifier}}, "", nil), http.StatusUnauthorized, "invalid_client")
	// A wrong verifier spends the code and refuses it.
	wantOAuthError(t, h.oauthPostForm("/oauth/token", url.Values{"grant_type": {"authorization_code"}, "code": {code}, "client_id": {client.ClientID}, "redirect_uri": {oauthRedirect}, "code_verifier": {strings.Repeat("x", 43)}}, "", nil), http.StatusBadRequest, "invalid_grant")

	big := url.Values{"grant_type": {"authorization_code"}, "padding": {strings.Repeat("a", 64<<10)}}
	wantOAuthError(t, h.oauthPostForm("/oauth/token", big, "", nil), http.StatusRequestEntityTooLarge, "invalid_request")

	if rec := h.oauthGet("/oauth/token", ""); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET /oauth/token = %d, want 405", rec.Code)
	}
}

// fixtureOAuthConsentData is the frozen consent payload: a client name that
// must be escaped, a loopback return address and every scope.
func fixtureOAuthConsentData() oauthConsentData {
	ana := domain.User{ID: 1, Name: "Ana Torres", Email: "ana@example.com", Role: domain.RoleAgent, Active: true, CreatedAt: goldenT0}
	scopes := make([]oauthScopeView, 0, 5)
	for _, s := range []domain.APIKeyScope{domain.ScopeCatalogRead, domain.ScopeTicketsRead, domain.ScopeTicketsWrite, domain.ScopeCommentsWrite, domain.ScopeCommentsInternal} {
		scopes = append(scopes, oauthScopeView{Scope: s, Label: oauthScopeLabels[s]})
	}
	return oauthConsentData{
		pageData:     pageData{CurrentUser: ana, Brand: goldenBrand()},
		ClientName:   `Claude <Code>`,
		RedirectHost: "localhost:4567",
		Scopes:       scopes,
		Fields: oauthHiddenFields(application.AuthorizeRequest{
			ClientID:            "tktcl_0123456789abcdef0123456789abcdef",
			RedirectURI:         "http://localhost:4567/callback",
			ResponseType:        "code",
			State:               "st-123",
			CodeChallenge:       pkceChallenge(oauthVerifier),
			CodeChallengeMethod: "S256",
			Scope:               "catalog:read tickets:read tickets:write comments:write comments:internal",
			Resource:            oauthResource,
		}),
	}
}

func fixtureOAuthErrorData() oauthErrorData {
	ana := domain.User{ID: 1, Name: "Ana Torres", Email: "ana@example.com", Role: domain.RoleAgent, Active: true, CreatedAt: goldenT0}
	return oauthErrorData{pageData: pageData{CurrentUser: ana, Brand: goldenBrand()}, Code: "invalid_client"}
}

func TestGoldenOAuthConsent(t *testing.T) {
	goldenFullPage(t, "oauth_consent", renderGolden(t, "oauth_consent", "", fixtureOAuthConsentData(), false))
}

func TestGoldenOAuthError(t *testing.T) {
	goldenFullPage(t, "oauth_error", renderGolden(t, "oauth_error", "", fixtureOAuthErrorData(), false))
}

// TestOAuthPagesHaveNoTrailingWhitespace applies the rendered-output rule of
// the design system to both pages, in both languages.
func TestOAuthPagesHaveNoTrailingWhitespace(t *testing.T) {
	for _, page := range []struct {
		name string
		data any
	}{{"oauth_consent", fixtureOAuthConsentData()}, {"oauth_error", fixtureOAuthErrorData()}} {
		for _, locale := range []string{i18n.English, i18n.Spanish} {
			r := newTestRenderer(t, fixedLanguage(locale))
			rec := httptest.NewRecorder()
			r.Render(rec, httptest.NewRequest(http.MethodGet, "/golden", nil), page.name, "", page.data, http.StatusOK)
			if rec.Code != http.StatusOK {
				t.Fatalf("%s/%s render = %d", page.name, locale, rec.Code)
			}
			body := rec.Body.String()
			for n, line := range strings.Split(body, "\n") {
				if (line != "" && strings.TrimSpace(line) == "") || strings.HasSuffix(line, " ") || strings.HasSuffix(line, "\t") {
					t.Errorf("%s/%s line %d has trailing whitespace: %s", page.name, locale, n+1, strconv.Quote(line))
				}
			}
			if !strings.Contains(body, `<html lang="`+locale+`"`) {
				t.Errorf("%s/%s does not declare its language", page.name, locale)
			}
		}
	}
}

// fixedLanguage answers one interface language for every render.
type fixedLanguage string

func (l fixedLanguage) GetInterfaceLanguage(context.Context) (string, error) { return string(l), nil }
