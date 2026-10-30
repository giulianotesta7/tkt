package application

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// MCP OAuth (issue #493): tkt is the OAuth 2.1 authorization server of its own
// /mcp endpoint. An MCP client registers itself (RFC 7591), a signed-in person
// approves it, and the client then acts as that person through tokens bound to
// the /mcp URL. Clients are public: PKCE S256 is what binds a code to the
// client that asked for it.

// ErrMsgMCPPublicURLRequired refuses turning MCP on before the public URL is
// set: every OAuth URL and the token audience derive from it.
const ErrMsgMCPPublicURLRequired = "MCP clients need the public URL"

const (
	oauthCodeTTL = 60 * time.Second
	// oauthAccessTTL bounds a leaked access token.
	oauthAccessTTL = time.Hour
	// oauthRefreshTTL restarts on every rotation, so a grant expires after 30
	// days of inactivity.
	oauthRefreshTTL = 30 * 24 * time.Hour
	// oauthClientPruneAge is how long a registered client that nobody approved
	// is kept: registration is anonymous, so abandoned clients must not pile up.
	oauthClientPruneAge     = 24 * time.Hour
	oauthGrantTouchInterval = time.Minute

	oauthAccessPrefix   = "tkta_"
	oauthRefreshPrefix  = "tktr_"
	oauthCodePrefix     = "tktc_"
	oauthClientIDPrefix = "tktcl_"

	defaultOAuthClientName = "MCP client"
	maxOAuthClientName     = 100
	maxOAuthRedirectURIs   = 10
)

// mcpScopes are the scopes an MCP client can be granted. tickets:all is never
// one of them: it widens a service user's reach, and a person already has the
// reach of their role.
var mcpScopes = []domain.APIKeyScope{domain.ScopeCatalogRead, domain.ScopeTicketsRead, domain.ScopeTicketsWrite, domain.ScopeCommentsWrite, domain.ScopeCommentsInternal}

// ErrMCPDisabled answers every MCP and OAuth request while the switch is off
// or the public URL is unset.
var ErrMCPDisabled = errors.New("mcp: disabled")

// OAuthError is an RFC 6749 error a client can act on. Inside an authorization
// request it is answered by redirecting back to the client.
type OAuthError struct {
	Code        string
	Description string
}

func (e *OAuthError) Error() string {
	if e.Description == "" {
		return "oauth: " + e.Code
	}
	return "oauth: " + e.Code + ": " + e.Description
}

// AuthorizeError is an authorization request error that must never redirect:
// the client or its redirect URI is not trusted, so the person sees an error
// page instead.
type AuthorizeError struct{ OAuthError }

func oauthErr(code, description string) *OAuthError {
	return &OAuthError{Code: code, Description: description}
}

// OAuthEndpoints are the absolute URLs the metadata documents advertise.
type OAuthEndpoints struct {
	Issuer                string
	Resource              string
	AuthorizationEndpoint string
	TokenEndpoint         string
	RegistrationEndpoint  string
	ScopesSupported       []domain.APIKeyScope
}

// ClientRegistration is the RFC 7591 registration request.
type ClientRegistration struct {
	ClientName              string
	RedirectURIs            []string
	GrantTypes              []string
	ResponseTypes           []string
	TokenEndpointAuthMethod string
}

// AuthorizeRequest is the authorization request as the client sent it.
type AuthorizeRequest struct {
	ClientID            string
	RedirectURI         string
	ResponseType        string
	State               string
	CodeChallenge       string
	CodeChallengeMethod string
	Scope               string
	Resource            string
}

// PendingAuthorization is a validated authorization request waiting for the
// person's consent. RedirectURI is the URI as presented; Resource is the
// canonical /mcp URL.
type PendingAuthorization struct {
	Client        domain.OAuthClient
	RedirectURI   string
	State         string
	CodeChallenge string
	Issuer        string
	Scopes        []domain.APIKeyScope
	Resource      string
}

// TokenRequest is the token endpoint form.
type TokenRequest struct {
	GrantType    string
	Code         string
	RedirectURI  string
	ClientID     string
	CodeVerifier string
	RefreshToken string
	Resource     string
	Scope        string
}

// TokenResponse is the RFC 6749 token response.
type TokenResponse struct {
	AccessToken  string
	TokenType    string
	ExpiresIn    int
	RefreshToken string
	Scope        string
}

// OAuthService owns the MCP authorization server use cases.
type OAuthService struct {
	store    OAuthStore
	users    UserStore
	settings EmailSettingsStore
	clock    domain.Clock
	random   io.Reader
}

// NewOAuthService wires the authorization server. settings supplies the public
// URL; random is the source of codes and tokens, nil meaning crypto/rand.
func NewOAuthService(store OAuthStore, users UserStore, settings EmailSettingsStore, clock domain.Clock, random io.Reader) *OAuthService {
	if random == nil {
		random = rand.Reader
	}
	return &OAuthService{store: store, users: users, settings: settings, clock: clock, random: random}
}

func (s *OAuthService) publicURL(ctx context.Context) (string, error) {
	settings, err := s.settings.GetEmailSettings(ctx)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(settings.BaseURL, "/"), nil
}

// Endpoints answers the authorization server URLs, or ErrMCPDisabled while the
// switch is off or the public URL is unset.
func (s *OAuthService) Endpoints(ctx context.Context) (OAuthEndpoints, error) {
	enabled, err := s.store.MCPEnabled(ctx)
	if err != nil {
		return OAuthEndpoints{}, err
	}
	if !enabled {
		return OAuthEndpoints{}, ErrMCPDisabled
	}
	base, err := s.publicURL(ctx)
	if err != nil {
		return OAuthEndpoints{}, err
	}
	if base == "" {
		return OAuthEndpoints{}, ErrMCPDisabled
	}
	return OAuthEndpoints{
		Issuer:                base,
		Resource:              base + "/mcp",
		AuthorizationEndpoint: base + "/oauth/authorize",
		TokenEndpoint:         base + "/oauth/token",
		RegistrationEndpoint:  base + "/oauth/register",
		ScopesSupported:       append([]domain.APIKeyScope(nil), mcpScopes...),
	}, nil
}

// MCPEnabled reports the instance switch. Off unless root turned it on.
func (s *OAuthService) MCPEnabled(ctx context.Context) (bool, error) {
	return s.store.MCPEnabled(ctx)
}

// SetMCPEnabled switches MCP clients on or off (root only). It cannot be
// turned on while the public URL is unset.
func (s *OAuthService) SetMCPEnabled(ctx context.Context, actor domain.User, enabled bool) error {
	if err := requireAPICapability(actor, CapToggleAPI); err != nil {
		return err
	}
	if enabled {
		base, err := s.publicURL(ctx)
		if err != nil {
			return err
		}
		if base == "" {
			return &domain.ValidationError{Field: "mcp_enabled", Message: ErrMsgMCPPublicURLRequired, Key: "error.mcp.public_url_required"}
		}
	}
	return s.store.SetMCPEnabled(ctx, enabled, actor.ID, s.clock.Now())
}

// RegisterClient registers a public client (RFC 7591) and prunes the clients
// nobody approved within a day.
func (s *OAuthService) RegisterClient(ctx context.Context, reg ClientRegistration) (*domain.OAuthClient, error) {
	if _, err := s.Endpoints(ctx); err != nil {
		return nil, err
	}
	if n := len(reg.RedirectURIs); n == 0 || n > maxOAuthRedirectURIs {
		return nil, oauthErr("invalid_redirect_uri", fmt.Sprintf("between 1 and %d redirect URIs are required", maxOAuthRedirectURIs))
	}
	for _, uri := range reg.RedirectURIs {
		if !validRedirectURI(uri) {
			return nil, oauthErr("invalid_redirect_uri", "redirect URIs must be https, http on a loopback host, or a private-use scheme")
		}
	}
	if m := reg.TokenEndpointAuthMethod; m != "" && m != "none" {
		return nil, oauthErr("invalid_client_metadata", "only public clients (token_endpoint_auth_method none) are supported")
	}
	for _, gt := range reg.GrantTypes {
		if gt != "authorization_code" && gt != "refresh_token" {
			return nil, oauthErr("invalid_client_metadata", "unsupported grant type "+gt)
		}
	}
	for _, rt := range reg.ResponseTypes {
		if rt != "code" {
			return nil, oauthErr("invalid_client_metadata", "unsupported response type "+rt)
		}
	}
	name := strings.TrimSpace(reg.ClientName)
	if runes := []rune(name); len(runes) > maxOAuthClientName {
		name = strings.TrimSpace(string(runes[:maxOAuthClientName]))
	}
	if name == "" {
		name = defaultOAuthClientName
	}
	id, err := s.randomHex(16)
	if err != nil {
		return nil, err
	}
	now := s.clock.Now()
	c := &domain.OAuthClient{
		ClientID:     oauthClientIDPrefix + id,
		Name:         name,
		RedirectURIs: append([]string(nil), reg.RedirectURIs...),
		CreatedAt:    now,
	}
	if err := s.store.CreateClient(ctx, c, now.Add(-oauthClientPruneAge)); err != nil {
		return nil, err
	}
	return c, nil
}

var privateURIScheme = regexp.MustCompile(`^[a-z][a-z0-9+.-]*$`)

// unsafeRedirectSchemes are refused as private-use schemes: they are either the
// web schemes with their own rules or schemes a browser would execute or load.
var unsafeRedirectSchemes = map[string]bool{
	"http": true, "https": true, "javascript": true, "data": true, "file": true, "vbscript": true,
	"about": true, "blob": true, "ws": true, "wss": true, "ftp": true, "mailto": true,
}

// validRedirectURI accepts https with a host, http on a loopback host (any
// port), or a private-use scheme (RFC 8252). No URI may carry a fragment.
func validRedirectURI(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || !strings.HasPrefix(raw, u.Scheme+":") || strings.Contains(raw, "#") {
		return false
	}
	switch u.Scheme {
	case "https":
		return u.Host != ""
	case "http":
		return isLoopbackHost(u.Host)
	}
	return privateURIScheme.MatchString(u.Scheme) && !unsafeRedirectSchemes[u.Scheme]
}

// isLoopbackHost reports whether host (with an optional port) is exactly
// localhost, 127.0.0.1 or [::1].
func isLoopbackHost(host string) bool {
	switch hostWithoutPort(host) {
	case "localhost", "127.0.0.1", "[::1]":
		return true
	}
	return false
}

func hostWithoutPort(host string) string {
	if i := strings.LastIndex(host, ":"); i >= 0 && !strings.Contains(host[i:], "]") {
		return host[:i]
	}
	return host
}

// matchRedirectURI answers whether presented is one of the registered URIs:
// exactly, or for an http loopback URI with the same host, path and query on
// any port (RFC 8252 section 7.3).
func matchRedirectURI(registered []string, presented string) bool {
	if presented == "" {
		return false
	}
	for _, r := range registered {
		if r == presented {
			return true
		}
	}
	p, err := url.Parse(presented)
	if err != nil || p.Scheme != "http" || !strings.HasPrefix(presented, "http:") || !isLoopbackHost(p.Host) || strings.Contains(presented, "#") {
		return false
	}
	for _, r := range registered {
		u, err := url.Parse(r)
		if err != nil || u.Scheme != "http" {
			continue
		}
		if hostWithoutPort(u.Host) == hostWithoutPort(p.Host) && u.EscapedPath() == p.EscapedPath() && u.RawQuery == p.RawQuery && u.User == nil && p.User == nil {
			return true
		}
	}
	return false
}

// PrepareAuthorization validates an authorization request. An unknown client
// or an unregistered redirect URI answers an AuthorizeError and no pending
// authorization; any later error is an OAuthError answered together with the
// pending authorization that ErrorRedirect sends it back through.
func (s *OAuthService) PrepareAuthorization(ctx context.Context, req AuthorizeRequest) (*PendingAuthorization, error) {
	ep, err := s.Endpoints(ctx)
	if err != nil {
		return nil, err
	}
	if req.ClientID == "" {
		return nil, &AuthorizeError{OAuthError{Code: "invalid_request", Description: "client_id is required"}}
	}
	client, err := s.store.ClientByClientID(ctx, req.ClientID)
	if errors.Is(err, domain.ErrNotFound) {
		return nil, &AuthorizeError{OAuthError{Code: "invalid_client", Description: "unknown client"}}
	}
	if err != nil {
		return nil, err
	}
	if !matchRedirectURI(client.RedirectURIs, req.RedirectURI) {
		return nil, &AuthorizeError{OAuthError{Code: "invalid_request", Description: "redirect_uri is missing or not registered"}}
	}
	p := &PendingAuthorization{
		Client:        *client,
		RedirectURI:   req.RedirectURI,
		State:         req.State,
		CodeChallenge: req.CodeChallenge,
		Issuer:        ep.Issuer,
		Resource:      ep.Resource,
	}
	if req.ResponseType != "code" {
		return p, oauthErr("unsupported_response_type", "response_type must be code")
	}
	if req.State == "" {
		return p, oauthErr("invalid_request", "state is required")
	}
	if req.CodeChallengeMethod != "S256" {
		return p, oauthErr("invalid_request", "code_challenge_method must be S256")
	}
	if len(req.CodeChallenge) != 43 || !isBase64URL(req.CodeChallenge) {
		return p, oauthErr("invalid_request", "code_challenge must be 43 base64url characters")
	}
	if req.Resource != "" && !sameResource(req.Resource, ep.Resource) {
		return p, oauthErr("invalid_target", "resource must be "+ep.Resource)
	}
	scopes, err := parseMCPScopes(req.Scope)
	if err != nil {
		return p, err
	}
	p.Scopes = scopes
	return p, nil
}

// parseMCPScopes answers the space-separated scopes in canonical order; empty
// means every MCP scope.
func parseMCPScopes(raw string) ([]domain.APIKeyScope, error) {
	fields := strings.Fields(raw)
	if len(fields) == 0 {
		return append([]domain.APIKeyScope(nil), mcpScopes...), nil
	}
	for _, f := range fields {
		if domain.APIKeyScope(f) == domain.ScopeTicketsAll {
			return nil, oauthErr("invalid_scope", "tickets:all cannot be granted to an MCP client")
		}
	}
	scopes, err := domain.ParseAPIKeyScopes(fields)
	if err != nil {
		return nil, oauthErr("invalid_scope", "unknown scope")
	}
	return scopes, nil
}

func isBase64URL(s string) bool {
	for _, r := range s {
		if !isAlnum(r) && r != '-' && r != '_' {
			return false
		}
	}
	return true
}

func isAlnum(r rune) bool {
	return (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
}

// validVerifier is RFC 7636 section 4.1: 43 to 128 unreserved characters.
func validVerifier(v string) bool {
	if len(v) < 43 || len(v) > 128 {
		return false
	}
	for _, r := range v {
		if !isAlnum(r) && !strings.ContainsRune("-._~", r) {
			return false
		}
	}
	return true
}

// sameResource compares two resource URLs ignoring the case of the scheme and
// host and a trailing slash.
func sameResource(a, b string) bool {
	na, okA := normalizeResource(a)
	nb, okB := normalizeResource(b)
	return okA && okB && na == nb
}

func normalizeResource(raw string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.Contains(raw, "#") {
		return "", false
	}
	return strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Host) + strings.TrimRight(u.EscapedPath(), "/"), true
}

// Approve records actor's consent and answers the redirect that carries the
// code. Scopes the actor's role cannot use are dropped; when none is left the
// redirect carries invalid_scope instead.
func (s *OAuthService) Approve(ctx context.Context, actor domain.User, p PendingAuthorization) (string, error) {
	if _, err := s.Endpoints(ctx); err != nil {
		return "", err
	}
	if actor.IsService() || !actor.Active {
		return "", domain.NewForbiddenError("only an active person can authorize an MCP client")
	}
	granted := NewPolicy().Capabilities(actor.Role)
	var scopes []domain.APIKeyScope
	for _, scope := range p.Scopes {
		if scopeUsable(granted, scope) {
			scopes = append(scopes, scope)
		}
	}
	if len(scopes) == 0 {
		return ErrorRedirect(p, &OAuthError{Code: "invalid_scope"}), nil
	}
	secret, err := s.randomHex(32)
	if err != nil {
		return "", err
	}
	code := oauthCodePrefix + secret
	now := s.clock.Now()
	if err := s.store.CreateCode(ctx, &domain.OAuthCode{
		CodeHash:      hashAPISecret(code),
		ClientID:      p.Client.ID,
		UserID:        actor.ID,
		RedirectURI:   p.RedirectURI,
		CodeChallenge: p.CodeChallenge,
		Scopes:        scopes,
		Resource:      p.Resource,
		CreatedAt:     now,
		ExpiresAt:     now.Add(oauthCodeTTL),
	}); err != nil {
		return "", err
	}
	return redirectWith(p.RedirectURI, "code", code, "state", p.State, "iss", p.Issuer), nil
}

// scopeUsable reports whether the role can use anything the scope allows. The
// read scopes map to no capability and are always usable.
func scopeUsable(granted Capabilities, scope domain.APIKeyScope) bool {
	caps := scopeCapabilities[scope]
	if len(caps) == 0 {
		return true
	}
	for _, c := range caps {
		if granted.Require(c) {
			return true
		}
	}
	return false
}

// DenyRedirect answers the redirect for a person who declined.
func DenyRedirect(p PendingAuthorization) string {
	return ErrorRedirect(p, &OAuthError{Code: "access_denied"})
}

// ErrorRedirect answers the redirect that reports a redirectable error to the
// client, with state and iss (RFC 9207).
func ErrorRedirect(p PendingAuthorization, e *OAuthError) string {
	return redirectWith(p.RedirectURI, "error", e.Code, "error_description", e.Description, "state", p.State, "iss", p.Issuer)
}

// redirectWith appends the non-empty key/value pairs to uri's query, keeping
// the query it already has and the order given.
func redirectWith(uri string, pairs ...string) string {
	var b strings.Builder
	b.WriteString(uri)
	sep := "?"
	if strings.Contains(uri, "?") {
		sep = "&"
		if strings.HasSuffix(uri, "?") || strings.HasSuffix(uri, "&") {
			sep = ""
		}
	}
	for i := 0; i+1 < len(pairs); i += 2 {
		if pairs[i+1] == "" {
			continue
		}
		b.WriteString(sep)
		b.WriteString(url.QueryEscape(pairs[i]))
		b.WriteString("=")
		b.WriteString(url.QueryEscape(pairs[i+1]))
		sep = "&"
	}
	return b.String()
}

// Token serves the token endpoint: authorization_code and refresh_token.
func (s *OAuthService) Token(ctx context.Context, req TokenRequest) (*TokenResponse, error) {
	ep, err := s.Endpoints(ctx)
	if err != nil {
		return nil, err
	}
	switch req.GrantType {
	case "authorization_code", "refresh_token":
	case "":
		return nil, oauthErr("invalid_request", "grant_type is required")
	default:
		return nil, oauthErr("unsupported_grant_type", "")
	}
	client, err := s.client(ctx, req.ClientID)
	if err != nil {
		return nil, err
	}
	if req.GrantType == "authorization_code" {
		return s.exchangeCode(ctx, client, req)
	}
	return s.refresh(ctx, ep, client, req)
}

func (s *OAuthService) client(ctx context.Context, clientID string) (*domain.OAuthClient, error) {
	if clientID == "" {
		return nil, oauthErr("invalid_client", "client_id is required")
	}
	c, err := s.store.ClientByClientID(ctx, clientID)
	if errors.Is(err, domain.ErrNotFound) {
		return nil, oauthErr("invalid_client", "unknown client")
	}
	return c, err
}

var errInvalidGrant = &OAuthError{Code: "invalid_grant"}

// exchangeCode spends the code before checking it, so a code is never usable
// twice, even after a failed exchange.
func (s *OAuthService) exchangeCode(ctx context.Context, client *domain.OAuthClient, req TokenRequest) (*TokenResponse, error) {
	if !validOAuthSecret(req.Code, oauthCodePrefix) {
		return nil, errInvalidGrant
	}
	now := s.clock.Now()
	code, err := s.store.ConsumeCode(ctx, hashAPISecret(req.Code), now)
	if errors.Is(err, domain.ErrNotFound) || errors.Is(err, ErrOAuthCodeReused) {
		return nil, errInvalidGrant
	}
	if err != nil {
		return nil, err
	}
	if !now.Before(code.ExpiresAt) || code.ClientID != client.ID || code.RedirectURI != req.RedirectURI || !validVerifier(req.CodeVerifier) {
		return nil, errInvalidGrant
	}
	sum := sha256.Sum256([]byte(req.CodeVerifier))
	if subtle.ConstantTimeCompare([]byte(base64.RawURLEncoding.EncodeToString(sum[:])), []byte(code.CodeChallenge)) != 1 {
		return nil, errInvalidGrant
	}
	if req.Resource != "" && !sameResource(req.Resource, code.Resource) {
		return nil, errInvalidGrant
	}
	if ok, err := s.activePerson(ctx, code.UserID); err != nil || !ok {
		if err != nil {
			return nil, err
		}
		return nil, errInvalidGrant
	}
	resp, access, refresh, err := s.mintPair(now, code.Scopes)
	if err != nil {
		return nil, err
	}
	grant := &domain.OAuthGrant{
		UserID:     code.UserID,
		ClientID:   client.ID,
		ClientName: client.Name,
		Scopes:     code.Scopes,
		Resource:   code.Resource,
		CreatedAt:  now,
	}
	if err := s.store.CreateGrant(ctx, grant, code.ID, access, refresh); err != nil {
		return nil, err
	}
	return resp, nil
}

// refresh rotates a refresh token. A spent token is handed to the store, which
// revokes its grant: the token was either stolen or replayed.
func (s *OAuthService) refresh(ctx context.Context, ep OAuthEndpoints, client *domain.OAuthClient, req TokenRequest) (*TokenResponse, error) {
	if !validOAuthSecret(req.RefreshToken, oauthRefreshPrefix) {
		return nil, errInvalidGrant
	}
	now := s.clock.Now()
	token, err := s.store.TokenByHash(ctx, hashAPISecret(req.RefreshToken))
	if errors.Is(err, domain.ErrNotFound) {
		return nil, errInvalidGrant
	}
	if err != nil {
		return nil, err
	}
	if token.Kind != domain.OAuthTokenRefresh {
		return nil, errInvalidGrant
	}
	if token.UsedAt != nil {
		if err := s.store.RotateRefreshToken(ctx, token.ID, now); err != nil && !errors.Is(err, ErrOAuthRefreshReused) {
			return nil, err
		}
		return nil, errInvalidGrant
	}
	grant, err := s.store.GrantByID(ctx, token.GrantID)
	if errors.Is(err, domain.ErrNotFound) {
		return nil, errInvalidGrant
	}
	if err != nil {
		return nil, err
	}
	if !now.Before(token.ExpiresAt) || grant.ClientID != client.ID || grant.RevokedAt != nil {
		return nil, errInvalidGrant
	}
	if ok, err := s.activePerson(ctx, grant.UserID); err != nil || !ok {
		if err != nil {
			return nil, err
		}
		return nil, errInvalidGrant
	}
	if strings.TrimSpace(req.Scope) != "" {
		requested, err := parseMCPScopes(req.Scope)
		if err != nil {
			return nil, err
		}
		if !scopesWithin(requested, grant.Scopes) {
			return nil, oauthErr("invalid_scope", "a refresh cannot widen the granted scopes")
		}
	}
	resp, access, refresh, err := s.mintPair(now, grant.Scopes)
	if err != nil {
		return nil, err
	}
	if err := s.store.RotateRefreshToken(ctx, token.ID, now, access, refresh); err != nil {
		if errors.Is(err, ErrOAuthRefreshReused) {
			return nil, errInvalidGrant
		}
		return nil, err
	}
	return resp, nil
}

func scopesWithin(requested, granted []domain.APIKeyScope) bool {
	for _, r := range requested {
		found := false
		for _, g := range granted {
			found = found || r == g
		}
		if !found {
			return false
		}
	}
	return true
}

// mintPair draws a new access and refresh token for scopes.
func (s *OAuthService) mintPair(now time.Time, scopes []domain.APIKeyScope) (*TokenResponse, *domain.OAuthToken, *domain.OAuthToken, error) {
	accessSecret, err := s.randomHex(32)
	if err != nil {
		return nil, nil, nil, err
	}
	refreshSecret, err := s.randomHex(32)
	if err != nil {
		return nil, nil, nil, err
	}
	resp := &TokenResponse{
		AccessToken:  oauthAccessPrefix + accessSecret,
		TokenType:    "Bearer",
		ExpiresIn:    int(oauthAccessTTL / time.Second),
		RefreshToken: oauthRefreshPrefix + refreshSecret,
		Scope:        joinScopes(scopes),
	}
	access := &domain.OAuthToken{Kind: domain.OAuthTokenAccess, TokenHash: hashAPISecret(resp.AccessToken), CreatedAt: now, ExpiresAt: now.Add(oauthAccessTTL)}
	refresh := &domain.OAuthToken{Kind: domain.OAuthTokenRefresh, TokenHash: hashAPISecret(resp.RefreshToken), CreatedAt: now, ExpiresAt: now.Add(oauthRefreshTTL)}
	return resp, access, refresh, nil
}

func joinScopes(scopes []domain.APIKeyScope) string {
	parts := make([]string, len(scopes))
	for i, s := range scopes {
		parts[i] = string(s)
	}
	return strings.Join(parts, " ")
}

// activePerson reports whether id is an active person; an unknown id is not.
func (s *OAuthService) activePerson(ctx context.Context, id int64) (bool, error) {
	u, err := s.users.GetByID(ctx, id)
	if errors.Is(err, domain.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return u.Active && !u.IsService(), nil
}

// Authenticate resolves an access token presented on /mcp to the person it acts
// for. A rejected token writes nothing; an accepted one records its grant's use
// at most once per minute.
func (s *OAuthService) Authenticate(ctx context.Context, token string) (*APIPrincipal, error) {
	ep, err := s.Endpoints(ctx)
	if err != nil {
		return nil, err
	}
	if !validOAuthSecret(token, oauthAccessPrefix) {
		return nil, ErrAPIUnauthorized
	}
	stored, err := s.store.TokenByHash(ctx, hashAPISecret(token))
	if errors.Is(err, domain.ErrNotFound) {
		return nil, ErrAPIUnauthorized
	}
	if err != nil {
		return nil, err
	}
	now := s.clock.Now()
	if stored.Kind != domain.OAuthTokenAccess || !now.Before(stored.ExpiresAt) {
		return nil, ErrAPIUnauthorized
	}
	grant, err := s.store.GrantByID(ctx, stored.GrantID)
	if errors.Is(err, domain.ErrNotFound) {
		return nil, ErrAPIUnauthorized
	}
	if err != nil {
		return nil, err
	}
	if grant.RevokedAt != nil || grant.Resource != ep.Resource {
		return nil, ErrAPIUnauthorized
	}
	owner, err := s.users.GetByID(ctx, grant.UserID)
	if errors.Is(err, domain.ErrNotFound) {
		return nil, ErrAPIUnauthorized
	}
	if err != nil {
		return nil, err
	}
	if owner.IsService() || !owner.Active {
		return nil, ErrAPIUnauthorized
	}
	if grant.LastUsedAt == nil || now.Sub(*grant.LastUsedAt) >= oauthGrantTouchInterval {
		if err := s.store.TouchGrant(ctx, grant.ID, now); err != nil {
			return nil, err
		}
	}
	return &APIPrincipal{
		User:         *owner,
		GrantID:      grant.ID,
		Scopes:       grant.Scopes,
		Capabilities: scopedCapabilities(owner.Role, grant.Scopes),
	}, nil
}

// ListGrants answers actor's live grants, newest first.
func (s *OAuthService) ListGrants(ctx context.Context, actor domain.User) ([]domain.OAuthGrant, error) {
	return s.store.ListLiveGrants(ctx, actor.ID, s.clock.Now())
}

// RevokeGrant revokes one of actor's own grants; anyone else's is NotFound.
func (s *OAuthService) RevokeGrant(ctx context.Context, actor domain.User, id int64) error {
	return s.store.RevokeGrant(ctx, id, actor.ID, s.clock.Now())
}

func (s *OAuthService) randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := io.ReadFull(s.random, b); err != nil {
		return "", fmt.Errorf("oauth: read token material: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// validOAuthSecret reports whether token is prefix followed by 64 lowercase hex.
func validOAuthSecret(token, prefix string) bool {
	secret, ok := strings.CutPrefix(token, prefix)
	return ok && len(secret) == 64 && isLowerHex(secret)
}
