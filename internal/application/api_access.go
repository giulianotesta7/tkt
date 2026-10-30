package application

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// API access (issue #488): service users, the API keys they own, the instance
// switch that turns the API on, and the authentication of a presented token.
//
// A key never belongs to a person. It belongs to a service user, a users row of
// kind 'service' that holds the user or agent role only, so the actor model the
// domain relies on (audit actor, requester, comment author, ticket scope) works
// for a key without a parallel identity.

// Validation messages for the API access screens.
const (
	ErrMsgServiceUserRole        = "a service user is a user or an agent"
	ErrMsgAPIKeyNameRequired     = "a key needs a name"
	ErrMsgAPIKeyExpiryPast       = "the expiry must be in the future"
	ErrMsgAPIKeyInternalRequires = "internal comments need an agent service user"
	ErrMsgAPIKeyOwnerInactive    = "the service user is deactivated"
)

// apiKeyTokenPrefix starts every token, so a leaked key is recognizable by
// secret scanners.
const apiKeyTokenPrefix = "tkt_"

// apiKeyTouchInterval bounds the last-used writes: one per key per minute is
// precise enough for an administrator and keeps a busy integration from writing
// on every request.
const apiKeyTouchInterval = time.Minute

var (
	// ErrAPIUnauthorized answers every credential failure alike: malformed,
	// unknown, wrong secret, revoked, expired, or an owner that is no longer an
	// active service user. One answer for all of them tells a caller nothing
	// about which part was wrong.
	ErrAPIUnauthorized = errors.New("api: invalid credentials")
	// ErrAPIDisabled answers any token while the instance switch is off.
	ErrAPIDisabled = errors.New("api: disabled")
)

// APIAccessStore persists service users, keys, the API switch and their
// administrative history. Every mutation writes its api_admin_events row in the
// same transaction.
type APIAccessStore interface {
	// CreateServiceUser stores u (Kind service), assigning u.ID, and audits it.
	CreateServiceUser(ctx context.Context, u *domain.User, actorID int64) error
	ListServiceUsers(ctx context.Context) ([]domain.User, error)
	// SetServiceUserActive activates or deactivates one service user; a
	// deactivation revokes every live key of that user. NotFound when id is not
	// a service user.
	SetServiceUserActive(ctx context.Context, id int64, active bool, actorID int64, at time.Time) error
	// CreateAPIKey stores k, assigning k.ID, and audits it (actor k.CreatedByUserID).
	CreateAPIKey(ctx context.Context, k *domain.APIKey) error
	ListAPIKeys(ctx context.Context) ([]domain.APIKey, error)
	APIKeyByPublicID(ctx context.Context, publicID string) (*domain.APIKey, error)
	// RevokeAPIKey revokes one live key. NotFound when absent or already revoked.
	RevokeAPIKey(ctx context.Context, id, actorID int64, at time.Time) error
	TouchAPIKey(ctx context.Context, id int64, at time.Time) error
	APIEnabled(ctx context.Context) (bool, error)
	SetAPIEnabled(ctx context.Context, enabled bool, actorID int64, at time.Time) error
}

// ServiceUserView is one service user with every key it owns, newest first.
type ServiceUserView struct {
	User domain.User
	Keys []domain.APIKey
}

// CreateAPIKeyInput is the key creation payload.
type CreateAPIKeyInput struct {
	ServiceUserID int64
	Name          string
	Scopes        []string
	ExpiresAt     *time.Time
}

// APIPrincipal is the identity an authenticated token acts as: its service
// user, the key, its scopes, and the capabilities the role grants narrowed to
// what the scopes allow.
type APIPrincipal struct {
	User         domain.User
	KeyID        int64
	Scopes       []domain.APIKeyScope
	Capabilities Capabilities
}

// HasScope reports whether the key carries scope.
func (p APIPrincipal) HasScope(scope domain.APIKeyScope) bool {
	for _, s := range p.Scopes {
		if s == scope {
			return true
		}
	}
	return false
}

// scopeCapabilities maps each scope to the role capabilities it lets a key use.
// The read scopes grant no capability: reads are gated by HasScope and by the
// role's ticket scope.
var scopeCapabilities = map[domain.APIKeyScope][]Capability{
	domain.ScopeTicketsWrite:     {CapCreateTicket, CapEditTicket, CapAssignTicket},
	domain.ScopeCommentsWrite:    {CapCommentPublic},
	domain.ScopeCommentsInternal: {CapCommentInternal},
}

// APIAccessService owns the API access use cases.
type APIAccessService struct {
	store  APIAccessStore
	users  UserStore
	clock  domain.Clock
	random io.Reader
}

// NewAPIAccessService wires the API access use cases. random is the source of
// key material; nil means crypto/rand.
func NewAPIAccessService(store APIAccessStore, users UserStore, clock domain.Clock, random io.Reader) *APIAccessService {
	if random == nil {
		random = rand.Reader
	}
	return &APIAccessService{store: store, users: users, clock: clock, random: random}
}

func requireAPICapability(actor domain.User, cap Capability) error {
	if !NewPolicy().Capabilities(actor.Role).Require(cap) {
		return domain.NewForbiddenError("API administration is not permitted")
	}
	return nil
}

// CreateServiceUser creates an active service user with a user or agent role.
// It never signs in: the email is a generated, undeliverable address and the
// password hash is empty.
func (s *APIAccessService) CreateServiceUser(ctx context.Context, actor domain.User, name string, role domain.Role) (*domain.User, error) {
	if err := requireAPICapability(actor, CapManageAPI); err != nil {
		return nil, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, &domain.ValidationError{Field: "name", Message: domain.ErrMsgUserNameRequired, Key: "error.name_required"}
	}
	if role != domain.RoleUser && role != domain.RoleAgent {
		return nil, &domain.ValidationError{Field: "role", Message: ErrMsgServiceUserRole, Key: "error.api.service_user_role"}
	}
	tag, err := s.randomHex(8)
	if err != nil {
		return nil, err
	}
	u := &domain.User{
		Name:      name,
		Email:     "service-" + tag + "@service.invalid",
		Role:      role,
		Active:    true,
		Kind:      domain.UserKindService,
		CreatedAt: s.clock.Now(),
	}
	if err := s.store.CreateServiceUser(ctx, u, actor.ID); err != nil {
		return nil, err
	}
	return u, nil
}

// ListServiceUsers returns every service user with its keys.
func (s *APIAccessService) ListServiceUsers(ctx context.Context, actor domain.User) ([]ServiceUserView, error) {
	if err := requireAPICapability(actor, CapManageAPI); err != nil {
		return nil, err
	}
	users, err := s.store.ListServiceUsers(ctx)
	if err != nil {
		return nil, err
	}
	keys, err := s.store.ListAPIKeys(ctx)
	if err != nil {
		return nil, err
	}
	byUser := map[int64][]domain.APIKey{}
	for _, k := range keys {
		byUser[k.UserID] = append(byUser[k.UserID], k)
	}
	out := make([]ServiceUserView, 0, len(users))
	for _, u := range users {
		out = append(out, ServiceUserView{User: u, Keys: byUser[u.ID]})
	}
	return out, nil
}

// SetServiceUserActive deactivates (revoking every live key) or reactivates a
// service user. Reactivation revives no key.
func (s *APIAccessService) SetServiceUserActive(ctx context.Context, actor domain.User, id int64, active bool) error {
	if err := requireAPICapability(actor, CapManageAPI); err != nil {
		return err
	}
	if _, err := s.serviceUser(ctx, id); err != nil {
		return err
	}
	return s.store.SetServiceUserActive(ctx, id, active, actor.ID, s.clock.Now())
}

// CreateAPIKey creates a key for an active service user and returns it with
// its token. The token is answered here once and stored nowhere: only the
// SHA-256 digest of its secret is kept.
func (s *APIAccessService) CreateAPIKey(ctx context.Context, actor domain.User, in CreateAPIKeyInput) (*domain.APIKey, string, error) {
	if err := requireAPICapability(actor, CapManageAPI); err != nil {
		return nil, "", err
	}
	owner, err := s.serviceUser(ctx, in.ServiceUserID)
	if err != nil {
		return nil, "", err
	}
	if !owner.Active {
		return nil, "", &domain.ValidationError{Field: "service_user", Message: ErrMsgAPIKeyOwnerInactive, Key: "error.api.owner_inactive"}
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, "", &domain.ValidationError{Field: "name", Message: ErrMsgAPIKeyNameRequired, Key: "error.api.key_name_required"}
	}
	scopes, err := domain.ParseAPIKeyScopes(in.Scopes)
	if err != nil {
		return nil, "", err
	}
	for _, scope := range scopes {
		if scope == domain.ScopeCommentsInternal && !owner.Role.AtLeast(domain.RoleAgent) {
			return nil, "", &domain.ValidationError{Field: "scopes", Message: ErrMsgAPIKeyInternalRequires, Key: "error.api.internal_requires_agent"}
		}
	}
	now := s.clock.Now()
	if in.ExpiresAt != nil && !in.ExpiresAt.After(now) {
		return nil, "", &domain.ValidationError{Field: "expires_at", Message: ErrMsgAPIKeyExpiryPast, Key: "error.api.expiry_past"}
	}
	publicID, err := s.randomHex(6)
	if err != nil {
		return nil, "", err
	}
	secret, err := s.randomHex(32)
	if err != nil {
		return nil, "", err
	}
	key := &domain.APIKey{
		PublicID:        publicID,
		UserID:          owner.ID,
		Name:            name,
		SecretHash:      hashAPISecret(secret),
		Scopes:          scopes,
		CreatedByUserID: actor.ID,
		CreatedAt:       now,
		ExpiresAt:       in.ExpiresAt,
	}
	if err := s.store.CreateAPIKey(ctx, key); err != nil {
		return nil, "", err
	}
	return key, apiKeyTokenPrefix + publicID + "_" + secret, nil
}

// RevokeAPIKey revokes one live key.
func (s *APIAccessService) RevokeAPIKey(ctx context.Context, actor domain.User, keyID int64) error {
	if err := requireAPICapability(actor, CapManageAPI); err != nil {
		return err
	}
	return s.store.RevokeAPIKey(ctx, keyID, actor.ID, s.clock.Now())
}

// APIEnabled reports the instance switch. Off unless root turned it on.
func (s *APIAccessService) APIEnabled(ctx context.Context) (bool, error) {
	return s.store.APIEnabled(ctx)
}

// SetAPIEnabled switches the API on or off (root only).
func (s *APIAccessService) SetAPIEnabled(ctx context.Context, actor domain.User, enabled bool) error {
	if err := requireAPICapability(actor, CapToggleAPI); err != nil {
		return err
	}
	return s.store.SetAPIEnabled(ctx, enabled, actor.ID, s.clock.Now())
}

// Authenticate resolves a presented token to the principal it acts as. A
// rejected token writes nothing; an accepted one records its use at most once
// per minute.
func (s *APIAccessService) Authenticate(ctx context.Context, token string) (*APIPrincipal, error) {
	enabled, err := s.store.APIEnabled(ctx)
	if err != nil {
		return nil, err
	}
	if !enabled {
		return nil, ErrAPIDisabled
	}
	publicID, secret, ok := parseAPIToken(token)
	if !ok {
		return nil, ErrAPIUnauthorized
	}
	key, err := s.store.APIKeyByPublicID(ctx, publicID)
	if errors.Is(err, domain.ErrNotFound) {
		return nil, ErrAPIUnauthorized
	}
	if err != nil {
		return nil, err
	}
	if subtle.ConstantTimeCompare([]byte(hashAPISecret(secret)), []byte(key.SecretHash)) != 1 {
		return nil, ErrAPIUnauthorized
	}
	now := s.clock.Now()
	if key.RevokedAt != nil || (key.ExpiresAt != nil && !now.Before(*key.ExpiresAt)) {
		return nil, ErrAPIUnauthorized
	}
	owner, err := s.users.GetByID(ctx, key.UserID)
	if errors.Is(err, domain.ErrNotFound) {
		return nil, ErrAPIUnauthorized
	}
	if err != nil {
		return nil, err
	}
	if !owner.IsService() || !owner.Active {
		return nil, ErrAPIUnauthorized
	}
	if key.LastUsedAt == nil || now.Sub(*key.LastUsedAt) >= apiKeyTouchInterval {
		if err := s.store.TouchAPIKey(ctx, key.ID, now); err != nil {
			return nil, err
		}
	}
	return &APIPrincipal{
		User:         *owner,
		KeyID:        key.ID,
		Scopes:       key.Scopes,
		Capabilities: scopedCapabilities(owner.Role, key.Scopes),
	}, nil
}

// scopedCapabilities is the role's capabilities narrowed to what the scopes
// allow: a key can only restrict its service user, never extend it.
func scopedCapabilities(role domain.Role, scopes []domain.APIKeyScope) Capabilities {
	granted := NewPolicy().Capabilities(role)
	caps := map[Capability]bool{}
	for _, scope := range scopes {
		for _, c := range scopeCapabilities[scope] {
			if granted.Require(c) {
				caps[c] = true
			}
		}
	}
	return Capabilities{caps: caps}
}

// serviceUser loads a service user; any other account answers NotFound.
func (s *APIAccessService) serviceUser(ctx context.Context, id int64) (*domain.User, error) {
	u, err := s.users.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if !u.IsService() {
		return nil, &domain.NotFoundError{Kind: "service user", ID: id}
	}
	return u, nil
}

func (s *APIAccessService) randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := io.ReadFull(s.random, b); err != nil {
		return "", fmt.Errorf("api: read key material: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// parseAPIToken splits tkt_<12 hex>_<64 hex>.
func parseAPIToken(token string) (publicID, secret string, ok bool) {
	rest, found := strings.CutPrefix(token, apiKeyTokenPrefix)
	if !found {
		return "", "", false
	}
	publicID, secret, found = strings.Cut(rest, "_")
	if !found || len(publicID) != 12 || len(secret) != 64 || !isLowerHex(publicID) || !isLowerHex(secret) {
		return "", "", false
	}
	return publicID, secret, true
}

func isLowerHex(s string) bool {
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func hashAPISecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}
