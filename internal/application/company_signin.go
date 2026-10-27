package application

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// CompanySignInService implements the company sign-in flow (issue #419): an
// employee arrives from the provider with an authorization code, and the flow
// decides whether that provider identity signs in an existing account, links
// one, creates one, or is refused. The configuration, the open client secret,
// the OpenID Connect client, and the identity store are all ports, so the
// decisions are testable without a provider and without a database.
//
// The flow is two steps:
//
//	Begin       answers the provider's authorization URL and the state a cookie
//	            must carry back. It stores one attempt with the nonce and the
//	            PKCE verifier that attempt will need.
//	Complete    consumes that attempt, exchanges the code, and answers exactly
//	            one typed outcome: signed in with a session, pending approval,
//	            or refused with a reason code.
//
// What it never does: create a privileged account (S9 — a new account is
// always a requester), reactivate a deactivated one (S8), or identify an
// account by email once a provider identity is linked (S7).

// CompanySignInAttemptTTL bounds one authorization: ten minutes after it
// starts, the attempt is expired and the callback is refused.
const CompanySignInAttemptTTL = 10 * time.Minute

// CompanySignInReason names why a sign-in ended without a session. The HTTP
// layer maps each code to its own message; the codes themselves carry no
// user-facing text.
type CompanySignInReason string

const (
	// CompanySignInProviderOff refuses because the provider is not configured
	// or not enabled.
	CompanySignInProviderOff CompanySignInReason = "provider_off"
	// CompanySignInOutsideDomain refuses a Google account whose hosted domain
	// is not one the administrator listed (S2).
	CompanySignInOutsideDomain CompanySignInReason = "outside_domain"
	// CompanySignInOutsideTenant refuses a Microsoft account from another
	// organization than the configured tenant (S2).
	CompanySignInOutsideTenant CompanySignInReason = "outside_tenant"
	// CompanySignInUnverifiedEmail refuses a Google account whose email the
	// provider does not vouch for.
	CompanySignInUnverifiedEmail CompanySignInReason = "unverified_email"
	// CompanySignInDeactivated refuses an account that was deactivated; the
	// provider never reactivates it (S8).
	CompanySignInDeactivated CompanySignInReason = "deactivated"
	// CompanySignInAmbiguousEmail refuses when more than one account holds the
	// provider's email; the flow does not guess which one was meant.
	CompanySignInAmbiguousEmail CompanySignInReason = "ambiguous_email"
	// CompanySignInEmailAlreadyLinked refuses when the account the email
	// points at already reaches a different identity of the same provider.
	CompanySignInEmailAlreadyLinked CompanySignInReason = "email_already_linked"
	// CompanySignInInvalidCallback refuses a callback whose state, attempt,
	// code, or ID token cannot be trusted.
	CompanySignInInvalidCallback CompanySignInReason = "invalid_callback"
)

// CompanySignInOutcomeKind is the decision Complete reached.
type CompanySignInOutcomeKind string

const (
	// CompanySignInSignedIn means a session was issued.
	CompanySignInSignedIn CompanySignInOutcomeKind = "signed_in"
	// CompanySignInPending means the account waits for an administrator's
	// approval and no session was issued (S3).
	CompanySignInPending CompanySignInOutcomeKind = "pending"
	// CompanySignInRefused means no session was issued and Reason says why.
	CompanySignInRefused CompanySignInOutcomeKind = "refused"
)

// CompanySignInOutcome is the single answer of the callback: the session that
// was issued, or the pending account, or the reason nothing was issued.
type CompanySignInOutcome struct {
	Kind    CompanySignInOutcomeKind
	Session *domain.Session
	User    *domain.User
	Reason  CompanySignInReason
}

// Refused reports whether nothing was issued.
func (o CompanySignInOutcome) Refused() bool { return o.Kind == CompanySignInRefused }

// CompanySignInStart is what Begin answers: where to send the browser, and the
// state the caller must also set as a cookie so the callback can prove the
// flow started in this browser.
type CompanySignInStart struct {
	URL   string
	State string
}

// CompanySignInCallback is one callback to answer: the provider it claims to be
// from, the state the browser's cookie carried, the state the provider echoed,
// and the authorization code.
type CompanySignInCallback struct {
	Provider    CompanyProvider
	CookieState string
	QueryState  string
	Code        string
}

// CompanySignInRefusal is Begin's typed refusal: the provider that was asked
// for cannot be started, so no URL is handed out. The callback answers its
// refusals as an outcome instead, because there the flow did reach a decision.
type CompanySignInRefusal struct {
	Reason CompanySignInReason
}

// Error reports the refusal code; the message text belongs to the caller.
func (r *CompanySignInRefusal) Error() string {
	return "company sign-in refused: " + string(r.Reason)
}

// CompanyProviderCredentials is everything the OpenID Connect client needs to
// talk to one provider for one installation.
type CompanyProviderCredentials struct {
	Provider     CompanyProvider
	ClientID     string
	ClientSecret string
	// TenantID is the Microsoft directory the installation belongs to.
	TenantID string
	// RedirectURI is the callback address registered at the provider.
	RedirectURI string
}

// CompanyAuthorizationRequest carries the per-flow values of one attempt: the
// state, the nonce the ID token must echo, the PKCE verifier whose challenge
// the authorization request carries, and an optional Google domain hint.
type CompanyAuthorizationRequest struct {
	State        string
	Nonce        string
	CodeVerifier string
	DomainHint   string
}

// CompanyIdentityClaims is the verified claim set of one ID token, reduced to
// the fields the sign-in decision reads. Every value here has already passed
// the client's signature, issuer, audience, lifetime, and nonce checks, so the
// flow never re-verifies the token: it is the port's contract that these claims
// arrive verified, whatever library or implementation produced them.
//
// Audience and Nonce are carried as the values the client verified. The flow
// does not re-check them — the client refused the token already if they were
// wrong — but an implementation that reads them is answering the same contract a
// library-based one does.
type CompanyIdentityClaims struct {
	Issuer  string
	Subject string
	// Audience is the aud claim as verified by the client.
	Audience []string
	Email    string
	// EmailVerified is the provider's statement that it vouches for Email.
	EmailVerified bool
	// HostedDomain is the Google hd claim: the Workspace domain the account
	// belongs to.
	HostedDomain string
	// TenantID is the Microsoft tid claim: the directory the account belongs
	// to.
	TenantID string
	Name     string
	// Nonce is the nonce the verified token echoed.
	Nonce string
}

// CompanyIdentityProvider is the OpenID Connect client the flow drives.
// AuthorizationURL answers where to send the browser; Exchange redeems the code
// and returns the verified claims, or an error wrapping
// ErrCompanyProviderRejected when the provider's answer cannot be trusted.
type CompanyIdentityProvider interface {
	AuthorizationURL(ctx context.Context, creds CompanyProviderCredentials, req CompanyAuthorizationRequest) (string, error)
	Exchange(ctx context.Context, creds CompanyProviderCredentials, req CompanyAuthorizationRequest, code string) (CompanyIdentityClaims, error)
}

// ErrCompanyProviderRejected reports that the provider's answer could not be
// trusted: an unusable code, an unverifiable ID token, or a malformed answer.
// The flow refuses the callback; anything else an exchange returns is an
// operational failure and is returned to the caller as an error.
var ErrCompanyProviderRejected = errors.New("company sign-in: the provider's answer was rejected")

// ErrCompanyIdentityTaken reports that the provider identity, or the account
// it would link, was created concurrently between the lookup and the write.
// The flow answers it by repeating the lookup once.
var ErrCompanyIdentityTaken = errors.New("company sign-in: the provider identity was taken concurrently")

// CompanySignInConfiguration is the stored configuration as the flow reads it:
// the whole set, plus one provider's client secret opened for use. It is
// satisfied by CompanySignInSettingsService.
type CompanySignInConfiguration interface {
	Get(ctx context.Context) (CompanySignInSettings, error)
	ClientSecret(ctx context.Context, p CompanyProvider) (string, error)
}

// CompanySignInApprovalNotifier is told when a first sign-in created an account
// that waits for approval (S3). The in-app notice itself is NOT raised here: the
// flow computes the recipients and the store writes the batch inside the
// account's own transaction, so the notice can never describe an account that
// was rolled back. This seam is the optional post-commit announcement of the
// same fact — for a channel that cannot join that transaction, such as an email
// — and a service wired without a notifier announces nothing beyond the stored
// notices.
//
// An implementation must not store the pending-account notice a second time:
// that row already committed with the account.
type CompanySignInApprovalNotifier interface {
	AccountAwaitingApproval(ctx context.Context, account domain.User) error
}

// CompanySignInService drives the company sign-in flow against its ports.
type CompanySignInService struct {
	config   CompanySignInConfiguration
	store    CompanyIdentityStore
	provider CompanyIdentityProvider
	sessions SessionStore
	notifier CompanySignInApprovalNotifier
	clock    domain.Clock
}

// NewCompanySignInService wires the flow against the configuration, the
// identity store, the OpenID Connect client, the session store, and the
// approval-notification seam. A nil notifier is a valid wiring: it silences the
// announcement, never the flow.
func NewCompanySignInService(config CompanySignInConfiguration, store CompanyIdentityStore, provider CompanyIdentityProvider, sessions SessionStore, notifier CompanySignInApprovalNotifier, clock domain.Clock) *CompanySignInService {
	return &CompanySignInService{config: config, store: store, provider: provider, sessions: sessions, notifier: notifier, clock: clock}
}

// Begin starts one authorization: it refuses a provider that is not
// configured, purges the attempts that expired, stores this attempt with a
// fresh state, nonce and PKCE verifier, and answers the provider's
// authorization URL with the state.
func (s *CompanySignInService) Begin(ctx context.Context, p CompanyProvider) (CompanySignInStart, error) {
	settings, err := s.config.Get(ctx)
	if err != nil {
		return CompanySignInStart{}, err
	}
	if !settings.Configured(p) {
		return CompanySignInStart{}, &CompanySignInRefusal{Reason: CompanySignInProviderOff}
	}
	secret, err := s.config.ClientSecret(ctx, p)
	if err != nil {
		return CompanySignInStart{}, err
	}
	state, err := randomState()
	if err != nil {
		return CompanySignInStart{}, err
	}
	nonce, err := randomState()
	if err != nil {
		return CompanySignInStart{}, err
	}
	verifier, err := randomCodeVerifier()
	if err != nil {
		return CompanySignInStart{}, err
	}

	request := CompanyAuthorizationRequest{State: state, Nonce: nonce, CodeVerifier: verifier}
	if p == ProviderGoogle && len(settings.Google.Domains) == 1 {
		request.DomainHint = settings.Google.Domains[0]
	}
	// The URL is built before anything is stored: a provider that cannot be
	// reached leaves no attempt behind to expire.
	authURL, err := s.provider.AuthorizationURL(ctx, credentialsFor(settings, p, secret), request)
	if err != nil {
		return CompanySignInStart{}, err
	}

	now := s.clock.Now()
	if _, err := s.store.PurgeExpiredAttempts(ctx, now); err != nil {
		return CompanySignInStart{}, err
	}
	attempt := &CompanySignInAttempt{
		StateHash:    hashCompanyState(state),
		Provider:     p,
		Nonce:        nonce,
		CodeVerifier: verifier,
		CreatedAt:    now,
		ExpiresAt:    now.Add(CompanySignInAttemptTTL),
	}
	if err := s.store.CreateAttempt(ctx, attempt); err != nil {
		return CompanySignInStart{}, err
	}
	return CompanySignInStart{URL: authURL, State: state}, nil
}

// Complete answers one callback with exactly one outcome: signed in with a new
// session, pending approval, or refused with a reason.
func (s *CompanySignInService) Complete(ctx context.Context, in CompanySignInCallback) (CompanySignInOutcome, error) {
	if !sameCompanyState(in.CookieState, in.QueryState) {
		return refusedCompanySignIn(CompanySignInInvalidCallback), nil
	}
	attempt, err := s.store.ConsumeAttempt(ctx, hashCompanyState(in.QueryState))
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return refusedCompanySignIn(CompanySignInInvalidCallback), nil
		}
		return CompanySignInOutcome{}, err
	}
	if attempt.Provider != in.Provider || !s.clock.Now().Before(attempt.ExpiresAt) {
		return refusedCompanySignIn(CompanySignInInvalidCallback), nil
	}

	settings, err := s.config.Get(ctx)
	if err != nil {
		return CompanySignInOutcome{}, err
	}
	if !settings.Configured(in.Provider) {
		return refusedCompanySignIn(CompanySignInProviderOff), nil
	}
	secret, err := s.config.ClientSecret(ctx, in.Provider)
	if err != nil {
		return CompanySignInOutcome{}, err
	}
	claims, err := s.provider.Exchange(ctx, credentialsFor(settings, in.Provider, secret), CompanyAuthorizationRequest{
		State:        in.QueryState,
		Nonce:        attempt.Nonce,
		CodeVerifier: attempt.CodeVerifier,
	}, in.Code)
	if err != nil {
		if errors.Is(err, ErrCompanyProviderRejected) {
			return refusedCompanySignIn(CompanySignInInvalidCallback), nil
		}
		return CompanySignInOutcome{}, err
	}
	if reason := companySignInRestriction(settings, in.Provider, claims); reason != "" {
		return refusedCompanySignIn(reason), nil
	}

	resolution, reason, err := s.resolveAccount(ctx, settings, in.Provider, claims)
	if err != nil {
		return CompanySignInOutcome{}, err
	}
	if reason != "" {
		return refusedCompanySignIn(reason), nil
	}
	if resolution.User.Pending {
		if resolution.Created {
			if err := s.announceAwaitingApproval(ctx, resolution.User); err != nil {
				return CompanySignInOutcome{}, err
			}
		}
		return CompanySignInOutcome{Kind: CompanySignInPending, User: &resolution.User}, nil
	}
	if !resolution.User.Active {
		return refusedCompanySignIn(CompanySignInDeactivated), nil
	}

	session, err := s.newSession(ctx, resolution.User.ID)
	if err != nil {
		return CompanySignInOutcome{}, err
	}
	return CompanySignInOutcome{Kind: CompanySignInSignedIn, Session: session, User: &resolution.User}, nil
}

// companyResolution is one attempt at answering which account a provider
// identity belongs to: the account, and whether this sign-in created it.
type companyResolution struct {
	User    domain.User
	Created bool
}

// resolveAccount answers the account a provider identity belongs to, repeating
// the lookup once when the rows were created concurrently (a lost race means
// someone else's sign-in got there first, and its account is the answer).
func (s *CompanySignInService) resolveAccount(ctx context.Context, settings CompanySignInSettings, p CompanyProvider, claims CompanyIdentityClaims) (companyResolution, CompanySignInReason, error) {
	for lostRace := false; ; lostRace = true {
		resolution, reason, err := s.resolveAccountOnce(ctx, settings, p, claims)
		if !errors.Is(err, ErrCompanyIdentityTaken) {
			return resolution, reason, err
		}
		if lostRace {
			// The second attempt found the same conflict, so the account this
			// email points at already reaches another identity of this provider.
			return companyResolution{}, CompanySignInEmailAlreadyLinked, nil
		}
	}
}

// resolveAccountOnce applies the documented resolution order: the provider
// identity first, then the email, then the new-account policy.
func (s *CompanySignInService) resolveAccountOnce(ctx context.Context, settings CompanySignInSettings, p CompanyProvider, claims CompanyIdentityClaims) (companyResolution, CompanySignInReason, error) {
	now := s.clock.Now()
	identity, user, err := s.store.ResolveIdentity(ctx, p, claims.Issuer, claims.Subject)
	switch {
	case err == nil:
		// The account is identified by the provider identity from here on, so a
		// changed email at the provider updates the link and never matches a
		// second account (S7).
		if user.Pending {
			return companyResolution{User: *user}, "", nil
		}
		if !user.Active {
			return companyResolution{}, CompanySignInDeactivated, nil
		}
		if err := s.store.TouchIdentity(ctx, identity.ID, normalizeCompanyEmail(claims.Email), now); err != nil {
			return companyResolution{}, "", err
		}
		return companyResolution{User: *user}, "", nil
	case !errors.Is(err, domain.ErrNotFound):
		return companyResolution{}, "", err
	}

	email := normalizeCompanyEmail(claims.Email)
	if email == "" {
		// Nothing identifies the person, so neither linking nor creating an
		// account can be decided from this answer.
		return companyResolution{}, CompanySignInInvalidCallback, nil
	}
	matches, err := s.store.UsersByEmail(ctx, email)
	if err != nil {
		return companyResolution{}, "", err
	}
	identity = &CompanyIdentity{
		Provider: p, Issuer: claims.Issuer, Subject: claims.Subject, Email: email,
		CreatedAt: now, LastSignInAt: now,
	}

	switch len(matches) {
	case 0:
		user := newCompanyAccount(settings, claims, email, now)
		// The notices commit with the account, so they are planned before the write
		// and travel into its transaction (S3/S11). A requester account is
		// announced to nobody, so its creation reads no roster at all.
		var notices []domain.Notification
		if user.Pending {
			notices, err = s.approvalNotices(ctx, now)
			if err != nil {
				return companyResolution{}, "", err
			}
		}
		if err := s.store.CreateUserWithIdentity(ctx, &user, identity, notices); err != nil {
			return companyResolution{}, "", err
		}
		return companyResolution{User: user, Created: true}, "", nil
	case 1:
		matched := matches[0]
		if matched.Pending {
			// A pending account is not linked until an administrator approves it:
			// the link is what identifies the account later, and the approval is
			// the moment someone decided this account exists.
			return companyResolution{User: matched}, "", nil
		}
		if !matched.Active {
			return companyResolution{}, CompanySignInDeactivated, nil
		}
		identity.UserID = matched.ID
		if err := s.store.LinkIdentity(ctx, identity); err != nil {
			return companyResolution{}, "", err
		}
		return companyResolution{User: matched}, "", nil
	default:
		// Two accounts hold the address in different case; the flow does not
		// choose between them.
		return companyResolution{}, CompanySignInAmbiguousEmail, nil
	}
}

// approvalNotices plans the administrator notices a newly created pending
// account raises, reading the current roster through the store. The read happens
// before the account exists, so the planning is a decision over stored users and
// the injected instant, never over anything the flow invents.
func (s *CompanySignInService) approvalNotices(ctx context.Context, at time.Time) ([]domain.Notification, error) {
	candidates, err := s.store.ActiveUsers(ctx)
	if err != nil {
		return nil, err
	}
	return companyApprovalNotices(candidates, at), nil
}

// companyApprovalNotices is the recipient policy of the pending-account notice
// (S3): one notice per ACTIVE account whose role the policy lets approve a
// waiting account — CapManageUsers, which today is admin and root (S9).
//
// The policy lives here, not in the store: the store answers the roster and the
// application decides who is told. Three facts keep the batch honest. An account
// that is not active cannot approve anything, so it is dropped. The person who
// just arrived IS the account that waits — it has no row yet and a pending row
// is never active — so it can never be a candidate of its own announcement. And
// a candidate the roster names twice is told once: one account, one notice.
func companyApprovalNotices(candidates []domain.User, at time.Time) []domain.Notification {
	policy := NewPolicy()
	seen := make(map[int64]bool, len(candidates))
	notices := make([]domain.Notification, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.ID <= 0 || !candidate.Active || candidate.Pending || seen[candidate.ID] {
			continue
		}
		if !policy.Capabilities(candidate.Role).Require(CapManageUsers) {
			continue
		}
		seen[candidate.ID] = true
		notices = append(notices, domain.Notification{
			UserID:    candidate.ID,
			Kind:      domain.NotificationCompanyAccountPending,
			CreatedAt: at,
		})
	}
	return notices
}

// companySignInRestriction applies the provider's own rule before any account
// is touched (S2): a Google account must come from a listed Workspace domain
// with a verified email, and a Microsoft account from the configured tenant. The
// issuer itself is checked by the OpenID Connect client, which refuses a token
// from any other authority.
func companySignInRestriction(settings CompanySignInSettings, p CompanyProvider, claims CompanyIdentityClaims) CompanySignInReason {
	switch p {
	case ProviderGoogle:
		if !claims.EmailVerified {
			return CompanySignInUnverifiedEmail
		}
		for _, domain := range settings.Google.Domains {
			if strings.EqualFold(domain, claims.HostedDomain) {
				return ""
			}
		}
		return CompanySignInOutsideDomain
	case ProviderMicrosoft:
		if claims.TenantID == "" || !strings.EqualFold(claims.TenantID, settings.Microsoft.TenantID) {
			return CompanySignInOutsideTenant
		}
	}
	return ""
}

// newCompanyAccount is the account a first sign-in creates: always a requester
// (S9), with no password the provider flow could not set, and either active or
// pending per the configured policy (S3).
func newCompanyAccount(settings CompanySignInSettings, claims CompanyIdentityClaims, email string, now time.Time) domain.User {
	name := strings.TrimSpace(claims.Name)
	if name == "" {
		name, _, _ = strings.Cut(email, "@")
	}
	pending := settings.NewAccounts == NewAccountsPending
	return domain.User{
		Name:         name,
		Email:        email,
		PasswordHash: "",
		Role:         domain.RoleUser,
		Active:       !pending,
		Pending:      pending,
		CreatedAt:    now,
	}
}

// newSession issues the session a password login issues (D14): 32 random bytes
// as a hex token, expiring one SessionTTL from the injected clock.
func (s *CompanySignInService) newSession(ctx context.Context, userID int64) (*domain.Session, error) {
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		return nil, err
	}
	session := &domain.Session{
		ID:        hex.EncodeToString(token),
		UserID:    userID,
		ExpiresAt: s.clock.Now().Add(SessionTTL),
	}
	if err := s.sessions.Create(ctx, session); err != nil {
		return nil, err
	}
	return session, nil
}

// announceAwaitingApproval is the optional announcement of a pending first
// sign-in (S3), made AFTER the account and its notices committed. A service
// wired without a notifier announces nothing here; the in-app notice the bell
// renders is the one stored with the account.
func (s *CompanySignInService) announceAwaitingApproval(ctx context.Context, account domain.User) error {
	if s.notifier == nil {
		return nil
	}
	return s.notifier.AccountAwaitingApproval(ctx, account)
}

// credentialsFor assembles the client credentials of one provider from the
// stored configuration and the opened secret.
func credentialsFor(settings CompanySignInSettings, p CompanyProvider, secret string) CompanyProviderCredentials {
	creds := CompanyProviderCredentials{Provider: p, ClientSecret: secret, RedirectURI: settings.RedirectURI(p)}
	if p == ProviderMicrosoft {
		creds.ClientID = settings.Microsoft.ClientID
		creds.TenantID = settings.Microsoft.TenantID
		return creds
	}
	creds.ClientID = settings.Google.ClientID
	return creds
}

// refusedCompanySignIn is the outcome of a callback that issued nothing.
func refusedCompanySignIn(reason CompanySignInReason) CompanySignInOutcome {
	return CompanySignInOutcome{Kind: CompanySignInRefused, Reason: reason}
}

// sameCompanyState compares the state the browser's cookie carried with the one
// the provider echoed, in constant time: a callback that did not start in this
// browser is refused before anything is read.
func sameCompanyState(a, b string) bool {
	if a == "" || b == "" || len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// hashCompanyState is what the attempt table is keyed by: the SHA-256 hex digest
// of the state, so the stored rows can never be replayed as a state.
func hashCompanyState(state string) string {
	sum := sha256.Sum256([]byte(state))
	return hex.EncodeToString(sum[:])
}

// randomState answers 32 random bytes as hex: the state and the nonce.
func randomState() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// randomCodeVerifier answers a PKCE verifier: 32 random bytes in the URL-safe
// alphabet without padding, 43 characters, inside the 43..128 the specification
// allows.
func randomCodeVerifier() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// normalizeCompanyEmail is the form the flow compares and stores: trimmed and
// lowercased, so the same person's address matches whatever case a provider
// reports.
func normalizeCompanyEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}
