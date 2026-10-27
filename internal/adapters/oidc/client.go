// Package oidc is the OpenID Connect client of the company sign-in flow (issue
// #419, S11). Discovery, the key set, and ID token verification come from
// github.com/coreos/go-oidc/v3; the authorization URL and the code exchange come
// from golang.org/x/oauth2. The adapter adds only what neither library decides:
// which issuer belongs to which provider, the scope set, the Google domain hint,
// the RS256-only verifier, the nonce check, and the reduction of the verified
// claims to the ones the sign-in decision reads.
//
// It also decides what a failure says. The libraries' own messages repeat the
// provider's text — an error_description, an audience, an advertised issuer, the
// body of a key set — and a provider may put anything there, including the client
// secret. So a failure leaves this package as one of a few fixed reasons that
// describe its shape, and the provider's words never travel with it.
package oidc

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	gooidc "github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

const (
	// requestTimeout bounds one provider round trip (discovery, keys, or the code
	// exchange), so an unresponsive provider fails a sign-in instead of holding
	// the handler open.
	requestTimeout = 10 * time.Second
	// googleIssuer is Google's issuer, which is also its discovery address.
	googleIssuer = "https://accounts.google.com"
	// signingAlgorithm is the only ID token algorithm accepted. Microsoft and
	// Google both sign with RS256, and restricting the verifier to it is what
	// keeps a token in another algorithm from being considered at all.
	signingAlgorithm = "RS256"
	// scopes are the three every flow asks for: the subject, the email, and the
	// profile name.
	scopeEmail   = "email"
	scopeProfile = "profile"
)

// microsoftIssuer is the v2.0 authority of one directory. The tenant is part of
// the issuer, which is what makes the configured organization the only one whose
// tokens verify.
func microsoftIssuer(tenant string) string {
	return "https://login.microsoftonline.com/" + tenant + "/v2.0"
}

// Client drives the OpenID Connect flow of the two company providers. One
// discovered provider is cached per issuer, so the discovery document and the
// key set are read once per installation and the library refreshes the keys when
// a token names one it does not have.
type Client struct {
	http  *http.Client
	clock domain.Clock

	mu        sync.Mutex
	providers map[string]*gooidc.Provider
}

var _ application.CompanyIdentityProvider = (*Client)(nil)

// NewClient wires the client against one HTTP client and the clock the token
// lifetimes are compared with. The HTTP client carries every request of the
// flow: discovery, the key set, and the code exchange.
func NewClient(httpClient *http.Client, clock domain.Clock) *Client {
	switch {
	case httpClient == nil:
		httpClient = &http.Client{Timeout: 2 * requestTimeout}
	case httpClient.Timeout == 0:
		// go-oidc reads the key set on a context that ignores cancellation, so the
		// client's own timeout is the only bound on that request: a client without
		// one would let a key fetch outlive the sign-in that triggered it. Bound a
		// copy rather than mutating the caller's client.
		bounded := *httpClient
		bounded.Timeout = 2 * requestTimeout
		httpClient = &bounded
	}
	return &Client{http: httpClient, clock: clock, providers: map[string]*gooidc.Provider{}}
}

// providerName answers the closed-list name of the provider. It is this
// program's own word, never text the provider wrote, so a failure may report it.
func providerName(provider application.CompanyProvider) string {
	switch provider {
	case application.ProviderGoogle:
		return "google"
	case application.ProviderMicrosoft:
		return "microsoft"
	}
	return "unknown"
}

// issuer answers the provider's issuer, refusing a provider outside the closed
// list and a Microsoft flow without a tenant.
func (c *Client) issuer(creds application.CompanyProviderCredentials) (string, error) {
	switch creds.Provider {
	case application.ProviderGoogle:
		return googleIssuer, nil
	case application.ProviderMicrosoft:
		tenant := strings.TrimSpace(creds.TenantID)
		if tenant == "" {
			return "", errors.New("oidc: the Microsoft tenant is required")
		}
		return microsoftIssuer(tenant), nil
	}
	return "", fmt.Errorf("oidc: unsupported provider %q", creds.Provider)
}

// provider answers the discovered provider of one issuer: its endpoints, its
// supported algorithms, and its key set. The first call reads the discovery
// document; every later sign-in reuses it.
func (c *Client) provider(ctx context.Context, provider application.CompanyProvider, issuer string) (*gooidc.Provider, error) {
	c.mu.Lock()
	discovered, cached := c.providers[issuer]
	c.mu.Unlock()
	if cached {
		return discovered, nil
	}

	discoveryCtx, cancel := context.WithTimeout(gooidc.ClientContext(ctx, c.http), requestTimeout)
	defer cancel()
	discovered, err := gooidc.NewProvider(discoveryCtx, issuer)
	if err != nil {
		return nil, discoveryFailure(provider, err)
	}
	// A provider that publishes no usable endpoint or no key set cannot run a
	// flow, and refusing it here is clearer than sending a request to an empty
	// URL later.
	endpoint := discovered.Endpoint()
	if endpoint.AuthURL == "" || endpoint.TokenURL == "" {
		return nil, fmt.Errorf("oidc: %s publishes no authorization or token endpoint", providerName(provider))
	}
	var published struct {
		JWKSURI string `json:"jwks_uri"`
	}
	if err := discovered.Claims(&published); err != nil {
		return nil, fmt.Errorf("oidc: the metadata of %s is not readable", providerName(provider))
	}
	if published.JWKSURI == "" {
		return nil, fmt.Errorf("oidc: %s publishes no key set", providerName(provider))
	}

	c.mu.Lock()
	c.providers[issuer] = discovered
	c.mu.Unlock()
	return discovered, nil
}

// discoveryFailure reports a discovery read that failed as one of two fixed
// reasons. The library's message repeats the document's body and the issuer it
// advertises, and both belong to the provider, so the cause is dropped instead
// of wrapped: a caller reads the reason and never the provider's string.
func discoveryFailure(provider application.CompanyProvider, err error) error {
	var mismatch *gooidc.IssuerMismatchError
	if errors.As(err, &mismatch) {
		return fmt.Errorf("oidc: the discovery document of %s advertises another issuer", providerName(provider))
	}
	return fmt.Errorf("oidc: the discovery document of %s could not be read", providerName(provider))
}

// oauthConfig is the OAuth2 client of one provider for one installation.
//
// The endpoint's auth style is pinned to the request body: the flow sends the
// client secret as client_secret_post, which is what the configuration describes
// and what the provider registrations here use. Left to autodetect, the library
// would first try HTTP Basic and only fall back on an error.
func (c *Client) oauthConfig(discovered *gooidc.Provider, creds application.CompanyProviderCredentials) *oauth2.Config {
	endpoint := discovered.Endpoint()
	endpoint.AuthStyle = oauth2.AuthStyleInParams
	return &oauth2.Config{
		ClientID:     creds.ClientID,
		ClientSecret: creds.ClientSecret,
		Endpoint:     endpoint,
		RedirectURL:  creds.RedirectURI,
		Scopes:       []string{gooidc.ScopeOpenID, scopeEmail, scopeProfile},
	}
}

// AuthorizationURL answers the provider's authorization endpoint URL for one
// attempt: the code flow with a PKCE S256 challenge, the state, the nonce, the
// three scopes, the client, the callback, and the optional Google domain hint.
func (c *Client) AuthorizationURL(ctx context.Context, creds application.CompanyProviderCredentials, req application.CompanyAuthorizationRequest) (string, error) {
	issuer, err := c.issuer(creds)
	if err != nil {
		return "", err
	}
	if creds.ClientID == "" {
		return "", errors.New("oidc: the client id is required")
	}
	if req.State == "" || req.Nonce == "" || req.CodeVerifier == "" {
		return "", errors.New("oidc: the authorization request needs a state, a nonce and a PKCE verifier")
	}
	discovered, err := c.provider(ctx, creds.Provider, issuer)
	if err != nil {
		return "", err
	}
	options := []oauth2.AuthCodeOption{
		oauth2.S256ChallengeOption(req.CodeVerifier),
		gooidc.Nonce(req.Nonce),
	}
	if creds.Provider == application.ProviderGoogle && req.DomainHint != "" {
		options = append(options, oauth2.SetAuthURLParam("hd", req.DomainHint))
	}
	return c.oauthConfig(discovered, creds).AuthCodeURL(req.State, options...), nil
}

// Exchange redeems the authorization code and answers the verified claims of the
// ID token it returns. A code the provider refuses, a token that does not
// verify, and an answer that carries no usable token all wrap
// ErrCompanyProviderRejected: the flow refuses the callback. A provider that
// cannot be reached is an operational error instead. Neither kind carries the
// provider's own text: each reports a fixed reason.
func (c *Client) Exchange(ctx context.Context, creds application.CompanyProviderCredentials, req application.CompanyAuthorizationRequest, code string) (application.CompanyIdentityClaims, error) {
	issuer, err := c.issuer(creds)
	if err != nil {
		return application.CompanyIdentityClaims{}, err
	}
	if code == "" {
		return application.CompanyIdentityClaims{}, rejected("the callback carried no authorization code")
	}
	discovered, err := c.provider(ctx, creds.Provider, issuer)
	if err != nil {
		return application.CompanyIdentityClaims{}, err
	}

	exchangeCtx, cancel := context.WithTimeout(gooidc.ClientContext(ctx, c.http), requestTimeout)
	defer cancel()
	token, err := c.oauthConfig(discovered, creds).Exchange(exchangeCtx, code, oauth2.VerifierOption(req.CodeVerifier))
	if err != nil {
		var retrieve *oauth2.RetrieveError
		if errors.As(err, &retrieve) {
			// The provider refused the code: report the status, which is an
			// integer, and never its error document, whose description and URI are
			// text the provider writes.
			return application.CompanyIdentityClaims{}, rejected("the token endpoint refused the code%s", refusalStatus(retrieve))
		}
		return application.CompanyIdentityClaims{}, fmt.Errorf("oidc: the token exchange with %s failed", providerName(creds.Provider))
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		return application.CompanyIdentityClaims{}, rejected("the token answer carries no ID token")
	}

	verifyCtx, cancelVerify := context.WithTimeout(gooidc.ClientContext(ctx, c.http), requestTimeout)
	defer cancelVerify()
	verifier := discovered.Verifier(&gooidc.Config{
		ClientID:             creds.ClientID,
		SupportedSigningAlgs: []string{signingAlgorithm},
		Now:                  c.clock.Now,
	})
	idToken, err := verifier.Verify(verifyCtx, rawIDToken)
	if err != nil {
		return application.CompanyIdentityClaims{}, rejected("%s", verificationFailure(err))
	}
	if idToken.Nonce != req.Nonce {
		return application.CompanyIdentityClaims{}, rejected("the ID token nonce does not match the attempt")
	}
	var claims struct {
		Email             string `json:"email"`
		EmailVerified     bool   `json:"email_verified"`
		PreferredUsername string `json:"preferred_username"`
		HostedDomain      string `json:"hd"`
		TenantID          string `json:"tid"`
		Name              string `json:"name"`
	}
	if err := idToken.Claims(&claims); err != nil {
		return application.CompanyIdentityClaims{}, rejected("the ID token claims are not readable")
	}
	identity := application.CompanyIdentityClaims{
		Issuer:        idToken.Issuer,
		Subject:       idToken.Subject,
		Audience:      idToken.Audience,
		Email:         claims.Email,
		EmailVerified: claims.EmailVerified,
		HostedDomain:  claims.HostedDomain,
		TenantID:      claims.TenantID,
		Name:          claims.Name,
		Nonce:         idToken.Nonce,
	}
	// A Microsoft token carries the address in preferred_username when the
	// email claim is absent. Google's contract is the email claim itself, so the
	// fallback stays inside the provider that documents it.
	if identity.Email == "" && creds.Provider == application.ProviderMicrosoft {
		identity.Email = claims.PreferredUsername
	}
	return identity, nil
}

// refusalStatus reports the HTTP status of a refused token request. The status
// is an integer the HTTP stack produces rather than a string the provider writes,
// so it is the one detail of a refusal that may accompany the reason.
func refusalStatus(retrieve *oauth2.RetrieveError) string {
	if retrieve.Response == nil {
		return ""
	}
	return fmt.Sprintf(" with status %d", retrieve.Response.StatusCode)
}

// verificationFailure reduces a failed verification to a fixed reason. The
// library's message repeats the token's audience and issuer and, when the key set
// cannot be read, the provider's own body: every one of those is text the
// provider controls.
func verificationFailure(err error) string {
	var expired *gooidc.TokenExpiredError
	if errors.As(err, &expired) {
		return "the ID token is expired"
	}
	return "the ID token did not verify"
}

// rejected reports an answer the flow must refuse. It wraps
// ErrCompanyProviderRejected so the caller can tell a refused answer from an
// operational failure.
func rejected(format string, args ...any) error {
	return fmt.Errorf("oidc: "+format+": %w", append(args, application.ErrCompanyProviderRejected)...)
}
