package httpadapter

import (
	"encoding/json"
	"errors"
	"log"
	"math"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// The MCP authorization server over HTTP (issue #493, S3-S6). Two halves:
//
//   - Branch serves the machine endpoints (the two metadata documents, client
//     registration and the token endpoint) before the session middleware: MCP
//     clients call them from anywhere, carry no cookie, and send no same-origin
//     Origin, so they get CORS instead of the browser gates.
//   - Register adds the consent page, which runs after the session middleware:
//     a person must be signed in to approve a client, and the approval POST is
//     covered by the existing same-origin gate.
//
// Everything answers 404 while the MCP switch is off or the public URL is unset.

const (
	// maxOAuthBodyBytes caps the machine endpoints' bodies. A registration or a
	// token request is a few hundred bytes; anyone may call them.
	maxOAuthBodyBytes = 64 << 10
	// The registration bucket is process-wide: registration is anonymous, so
	// this bounds how fast the clients table can grow, whoever is asking.
	oauthRegisterBurst       = 30
	oauthRegisterRefillEvery = 2 * time.Second
)

const (
	pathProtectedResource    = "/.well-known/oauth-protected-resource"
	pathProtectedResourceMCP = "/.well-known/oauth-protected-resource/mcp"
	pathAuthorizationServer  = "/.well-known/oauth-authorization-server"
	pathOAuthRegister        = "/oauth/register"
	pathOAuthToken           = "/oauth/token"
)

// oauthScopeLabels names the catalogue key that tells a person, on the consent
// page, what each scope lets the client do as them.
var oauthScopeLabels = map[domain.APIKeyScope]string{
	domain.ScopeCatalogRead:      "oauth.scope.catalog_read",
	domain.ScopeTicketsRead:      "oauth.scope.tickets_read",
	domain.ScopeTicketsWrite:     "oauth.scope.tickets_write",
	domain.ScopeCommentsWrite:    "oauth.scope.comments_write",
	domain.ScopeCommentsInternal: "oauth.scope.comments_internal",
}

// OAuthHandlers serves the authorization server endpoints.
type OAuthHandlers struct {
	oauth    *application.OAuthService
	renderer *Renderer
	clock    domain.Clock

	mu             sync.Mutex
	registerTokens float64
	registerAt     time.Time
}

// NewOAuthHandlers wires the authorization server over the OAuth use cases.
func NewOAuthHandlers(oauth *application.OAuthService, renderer *Renderer, clock domain.Clock) *OAuthHandlers {
	return &OAuthHandlers{oauth: oauth, renderer: renderer, clock: clock, registerTokens: oauthRegisterBurst, registerAt: clock.Now()}
}

// Branch routes the machine endpoints before browser sessions; every other
// request goes to browser untouched.
func (h *OAuthHandlers) Branch(browser http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case pathProtectedResource, pathProtectedResourceMCP, pathAuthorizationServer, pathOAuthRegister, pathOAuthToken:
			h.serveMachine(w, r)
		default:
			browser.ServeHTTP(w, r)
		}
	})
}

// Register adds the consent routes; they run behind the session middleware.
func (h *OAuthHandlers) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /oauth/authorize", refuseFraming(h.authorize))
	mux.HandleFunc("POST /oauth/authorize", refuseFraming(h.decide))
}

// refuseFraming keeps the consent page out of any frame (RFC 6819 §4.4.1.9):
// a page on another site that framed it could trick the person into approving.
func refuseFraming(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "frame-ancestors 'none'")
		next(w, r)
	}
}

func (h *OAuthHandlers) serveMachine(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	ep, err := h.oauth.Endpoints(r.Context())
	if err != nil {
		writeOAuthFailure(w, err)
		return
	}
	method := http.MethodPost
	if r.URL.Path != pathOAuthRegister && r.URL.Path != pathOAuthToken {
		method = http.MethodGet
	}
	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Methods", method+", OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, MCP-Protocol-Version")
		w.Header().Set("Access-Control-Max-Age", "86400")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != method && !(method == http.MethodGet && r.Method == http.MethodHead) {
		w.Header().Set("Allow", method+", OPTIONS")
		writeOAuthJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method_not_allowed"})
		return
	}
	switch r.URL.Path {
	case pathOAuthRegister:
		h.register(w, r)
	case pathOAuthToken:
		h.token(w, r)
	case pathAuthorizationServer:
		writeOAuthJSON(w, http.StatusOK, authorizationServerMetadata{
			Issuer:                            ep.Issuer,
			AuthorizationEndpoint:             ep.AuthorizationEndpoint,
			TokenEndpoint:                     ep.TokenEndpoint,
			RegistrationEndpoint:              ep.RegistrationEndpoint,
			ResponseTypesSupported:            []string{"code"},
			GrantTypesSupported:               []string{"authorization_code", "refresh_token"},
			CodeChallengeMethodsSupported:     []string{"S256"},
			TokenEndpointAuthMethodsSupported: []string{"none"},
			ScopesSupported:                   ep.ScopesSupported,
			AuthorizationResponseISSSupported: true,
		})
	default:
		writeOAuthJSON(w, http.StatusOK, protectedResourceMetadata{
			Resource:               ep.Resource,
			AuthorizationServers:   []string{ep.Issuer},
			ScopesSupported:        ep.ScopesSupported,
			BearerMethodsSupported: []string{"header"},
			ResourceName:           "tkt",
		})
	}
}

// protectedResourceMetadata is RFC 9728.
type protectedResourceMetadata struct {
	Resource               string               `json:"resource"`
	AuthorizationServers   []string             `json:"authorization_servers"`
	ScopesSupported        []domain.APIKeyScope `json:"scopes_supported"`
	BearerMethodsSupported []string             `json:"bearer_methods_supported"`
	ResourceName           string               `json:"resource_name"`
}

// authorizationServerMetadata is RFC 8414, with RFC 9207's iss parameter.
type authorizationServerMetadata struct {
	Issuer                            string               `json:"issuer"`
	AuthorizationEndpoint             string               `json:"authorization_endpoint"`
	TokenEndpoint                     string               `json:"token_endpoint"`
	RegistrationEndpoint              string               `json:"registration_endpoint"`
	ResponseTypesSupported            []string             `json:"response_types_supported"`
	GrantTypesSupported               []string             `json:"grant_types_supported"`
	CodeChallengeMethodsSupported     []string             `json:"code_challenge_methods_supported"`
	TokenEndpointAuthMethodsSupported []string             `json:"token_endpoint_auth_methods_supported"`
	ScopesSupported                   []domain.APIKeyScope `json:"scopes_supported"`
	AuthorizationResponseISSSupported bool                 `json:"authorization_response_iss_parameter_supported"`
}

// clientRegistrationJSON is the RFC 7591 request and response body. Unknown
// request members are ignored, as the RFC asks.
type clientRegistrationJSON struct {
	ClientID                string   `json:"client_id,omitempty"`
	ClientIDIssuedAt        int64    `json:"client_id_issued_at,omitempty"`
	ClientName              string   `json:"client_name"`
	RedirectURIs            []string `json:"redirect_uris"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
}

func (h *OAuthHandlers) register(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if retry := h.takeRegistration(); retry > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(retry))
		writeOAuthError(w, http.StatusTooManyRequests, "slow_down", "too many registrations, retry later")
		return
	}
	if mediaType(r) != "application/json" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_client_metadata", "the registration must be a JSON document")
		return
	}
	var in clientRegistrationJSON
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxOAuthBodyBytes)).Decode(&in); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeOAuthError(w, http.StatusRequestEntityTooLarge, "invalid_request", "the request body is too large")
			return
		}
		writeOAuthError(w, http.StatusBadRequest, "invalid_client_metadata", "the registration is not valid JSON")
		return
	}
	client, err := h.oauth.RegisterClient(r.Context(), application.ClientRegistration{
		ClientName:              in.ClientName,
		RedirectURIs:            in.RedirectURIs,
		GrantTypes:              in.GrantTypes,
		ResponseTypes:           in.ResponseTypes,
		TokenEndpointAuthMethod: in.TokenEndpointAuthMethod,
	})
	if err != nil {
		writeOAuthFailure(w, err)
		return
	}
	out := clientRegistrationJSON{
		ClientID:                client.ClientID,
		ClientIDIssuedAt:        client.CreatedAt.Unix(),
		ClientName:              client.Name,
		RedirectURIs:            client.RedirectURIs,
		GrantTypes:              in.GrantTypes,
		ResponseTypes:           in.ResponseTypes,
		TokenEndpointAuthMethod: "none",
	}
	if len(out.GrantTypes) == 0 {
		out.GrantTypes = []string{"authorization_code", "refresh_token"}
	}
	if len(out.ResponseTypes) == 0 {
		out.ResponseTypes = []string{"code"}
	}
	writeOAuthJSON(w, http.StatusCreated, out)
}

// takeRegistration spends one registration token and answers 0, or answers the
// seconds until the next token. The bucket holds oauthRegisterBurst tokens and
// regains one every oauthRegisterRefillEvery.
func (h *OAuthHandlers) takeRegistration() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := h.clock.Now()
	if now.After(h.registerAt) {
		h.registerTokens = math.Min(oauthRegisterBurst, h.registerTokens+float64(now.Sub(h.registerAt))/float64(oauthRegisterRefillEvery))
		h.registerAt = now
	}
	if h.registerTokens < 1 {
		return int(math.Ceil((1 - h.registerTokens) * oauthRegisterRefillEvery.Seconds()))
	}
	h.registerTokens--
	return 0
}

// tokenResponseJSON is the RFC 6749 section 5.1 response.
type tokenResponseJSON struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
}

func (h *OAuthHandlers) token(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	if mediaType(r) != "application/x-www-form-urlencoded" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "the token request must be form-encoded")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxOAuthBodyBytes)
	if err := r.ParseForm(); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeOAuthError(w, http.StatusRequestEntityTooLarge, "invalid_request", "the request body is too large")
			return
		}
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "the request body is not a valid form")
		return
	}
	// Only the body counts: a token request never travels in the URL.
	form := r.PostForm
	resp, err := h.oauth.Token(r.Context(), application.TokenRequest{
		GrantType:    form.Get("grant_type"),
		Code:         form.Get("code"),
		RedirectURI:  form.Get("redirect_uri"),
		ClientID:     form.Get("client_id"),
		CodeVerifier: form.Get("code_verifier"),
		RefreshToken: form.Get("refresh_token"),
		Resource:     form.Get("resource"),
		Scope:        form.Get("scope"),
	})
	if err != nil {
		writeOAuthFailure(w, err)
		return
	}
	writeOAuthJSON(w, http.StatusOK, tokenResponseJSON{
		AccessToken:  resp.AccessToken,
		TokenType:    resp.TokenType,
		ExpiresIn:    resp.ExpiresIn,
		RefreshToken: resp.RefreshToken,
		Scope:        resp.Scope,
	})
}

// mediaType answers the request's media type without parameters.
func mediaType(r *http.Request) string {
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		return ""
	}
	return mt
}

// writeOAuthFailure answers a use-case error on a machine endpoint: 404 while
// MCP is off, the RFC error for an OAuthError (401 for invalid_client), and a
// bare server_error for anything else.
func writeOAuthFailure(w http.ResponseWriter, err error) {
	var oe *application.OAuthError
	switch {
	case errors.Is(err, application.ErrMCPDisabled):
		writeOAuthJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
	case errors.As(err, &oe):
		status := http.StatusBadRequest
		if oe.Code == "invalid_client" {
			status = http.StatusUnauthorized
		}
		writeOAuthError(w, status, oe.Code, oe.Description)
	default:
		log.Printf("httpadapter: oauth endpoint: %v", err)
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "")
	}
}

func writeOAuthError(w http.ResponseWriter, status int, code, description string) {
	writeOAuthJSON(w, status, struct {
		Error       string `json:"error"`
		Description string `json:"error_description,omitempty"`
	}{code, description})
}

func writeOAuthJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// oauthHiddenField is one authorization parameter the consent form posts back.
type oauthHiddenField struct{ Name, Value string }

// oauthScopeView is one requested scope with the catalogue key of its
// description.
type oauthScopeView struct {
	Scope domain.APIKeyScope
	Label string
}

// oauthConsentData is the consent page. ClientName is whatever the client
// registered, so the template must only ever print it escaped.
type oauthConsentData struct {
	pageData
	ClientName   string
	RedirectHost string
	Scopes       []oauthScopeView
	Fields       []oauthHiddenField
}

// oauthErrorData is the page an untrusted authorization request answers.
type oauthErrorData struct {
	pageData
	Code string
}

// oauthHiddenFields answers the parameters the consent form posts back, in a
// fixed order. The POST validates them again from scratch: nothing the GET
// decided is trusted.
func oauthHiddenFields(req application.AuthorizeRequest) []oauthHiddenField {
	return []oauthHiddenField{
		{"client_id", req.ClientID},
		{"redirect_uri", req.RedirectURI},
		{"response_type", req.ResponseType},
		{"state", req.State},
		{"code_challenge", req.CodeChallenge},
		{"code_challenge_method", req.CodeChallengeMethod},
		{"scope", req.Scope},
		{"resource", req.Resource},
	}
}

func authorizeRequestFrom(v url.Values) application.AuthorizeRequest {
	return application.AuthorizeRequest{
		ClientID:            v.Get("client_id"),
		RedirectURI:         v.Get("redirect_uri"),
		ResponseType:        v.Get("response_type"),
		State:               v.Get("state"),
		CodeChallenge:       v.Get("code_challenge"),
		CodeChallengeMethod: v.Get("code_challenge_method"),
		Scope:               v.Get("scope"),
		Resource:            v.Get("resource"),
	}
}

// prepare validates an authorization request and answers it when it cannot
// go on: 404 while off, the error page for an untrusted client or redirect,
// and a redirect back to the client for any other error. ok is false once the
// response is written.
func (h *OAuthHandlers) prepare(w http.ResponseWriter, r *http.Request, req application.AuthorizeRequest) (*application.PendingAuthorization, bool) {
	p, err := h.oauth.PrepareAuthorization(r.Context(), req)
	if err == nil {
		return p, true
	}
	var untrusted *application.AuthorizeError
	var oe *application.OAuthError
	switch {
	case errors.Is(err, application.ErrMCPDisabled):
		httpError(w, r, "error.page_not_found", http.StatusNotFound)
	case errors.As(err, &untrusted):
		h.renderError(w, r, untrusted.Code)
	case errors.As(err, &oe) && p != nil:
		redirectToClient(w, application.ErrorRedirect(*p, oe))
	default:
		log.Printf("httpadapter: oauth authorize: %v", err)
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
	}
	return nil, false
}

func (h *OAuthHandlers) authorize(w http.ResponseWriter, r *http.Request) {
	req := authorizeRequestFrom(r.URL.Query())
	p, ok := h.prepare(w, r, req)
	if !ok {
		return
	}
	scopes := make([]oauthScopeView, 0, len(p.Scopes))
	for _, s := range p.Scopes {
		scopes = append(scopes, oauthScopeView{Scope: s, Label: oauthScopeLabels[s]})
	}
	h.renderer.Render(w, r, "oauth_consent", "", oauthConsentData{
		pageData:     pageDataFrom(r, ""),
		ClientName:   p.Client.Name,
		RedirectHost: redirectHost(p.RedirectURI),
		Scopes:       scopes,
		Fields:       oauthHiddenFields(req),
	}, http.StatusOK)
}

func (h *OAuthHandlers) decide(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		httpError(w, r, "error.bad_request", http.StatusBadRequest)
		return
	}
	p, ok := h.prepare(w, r, authorizeRequestFrom(r.PostForm))
	if !ok {
		return
	}
	switch r.PostForm.Get("decision") {
	case "approve":
		to, err := h.oauth.Approve(r.Context(), *userFromContext(r.Context()), *p)
		if err != nil {
			var forbidden *domain.ForbiddenError
			if errors.As(err, &forbidden) {
				httpError(w, r, "error.forbidden", http.StatusForbidden)
				return
			}
			if errors.Is(err, application.ErrMCPDisabled) {
				httpError(w, r, "error.page_not_found", http.StatusNotFound)
				return
			}
			log.Printf("httpadapter: oauth approve: %v", err)
			httpError(w, r, "error.internal_server", http.StatusInternalServerError)
			return
		}
		redirectToClient(w, to)
	case "deny":
		redirectToClient(w, application.DenyRedirect(*p))
	default:
		h.renderError(w, r, "invalid_request")
	}
}

func (h *OAuthHandlers) renderError(w http.ResponseWriter, r *http.Request, code string) {
	h.renderer.Render(w, r, "oauth_error", "", oauthErrorData{pageData: pageDataFrom(r, ""), Code: code}, http.StatusBadRequest)
}

// redirectToClient answers a 303 to the client's redirect URI exactly as built.
// It may carry a private-use scheme (cursor://…), so the Location is written as
// is rather than resolved against the request.
func redirectToClient(w http.ResponseWriter, to string) {
	w.Header().Set("Location", to)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusSeeOther)
}

// redirectHost answers what the consent page names as the return address: the
// host of a web redirect, and the scheme with its host for a private-use one.
func redirectHost(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	switch {
	case u.Scheme == "http" || u.Scheme == "https":
		return u.Host
	case u.Host != "":
		return u.Scheme + "://" + u.Host
	default:
		return u.Scheme + ":"
	}
}
