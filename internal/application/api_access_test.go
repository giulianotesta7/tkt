package application_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// fakeAPIAccessStore is the in-memory APIAccessStore. Service users live in the
// shared fakeUserStore so the service reads them through its UserStore port, as
// it does over SQLite. Simplifications: no transactions (each call applies
// whole or not at all because nothing here can fail halfway), and audit rows are
// recorded as plain action strings with their actor.
type fakeAPIAccessStore struct {
	users   *fakeUserStore
	keys    []*domain.APIKey
	nextKey int64
	enabled bool
	events  []fakeAPIEvent
	touches int
}

type fakeAPIEvent struct {
	action  string
	actorID int64
}

func newFakeAPIAccessStore(users *fakeUserStore) *fakeAPIAccessStore {
	return &fakeAPIAccessStore{users: users, nextKey: 1}
}

func (f *fakeAPIAccessStore) audit(action string, actorID int64) {
	f.events = append(f.events, fakeAPIEvent{action: action, actorID: actorID})
}

func (f *fakeAPIAccessStore) CreateServiceUser(_ context.Context, u *domain.User, actorID int64) error {
	u.ID = f.users.nextID
	f.users.nextID++
	cp := *u
	f.users.users[u.ID] = &cp
	f.users.byEmail[u.Email] = u.ID
	f.audit("service_user_created", actorID)
	return nil
}

func (f *fakeAPIAccessStore) ListServiceUsers(_ context.Context) ([]domain.User, error) {
	var out []domain.User
	for id := int64(1); id < f.users.nextID; id++ {
		if u, ok := f.users.users[id]; ok && u.IsService() {
			out = append(out, *u)
		}
	}
	return out, nil
}

func (f *fakeAPIAccessStore) SetServiceUserActive(_ context.Context, id int64, active bool, actorID int64, at time.Time) error {
	u, ok := f.users.users[id]
	if !ok || !u.IsService() {
		return &domain.NotFoundError{Kind: "service user", ID: id}
	}
	u.Active = active
	if !active {
		for _, k := range f.keys {
			if k.UserID == id && k.RevokedAt == nil {
				t, by := at, actorID
				k.RevokedAt, k.RevokedByUserID = &t, &by
			}
		}
		f.audit("service_user_deactivated", actorID)
		return nil
	}
	f.audit("service_user_reactivated", actorID)
	return nil
}

func (f *fakeAPIAccessStore) CreateAPIKey(_ context.Context, k *domain.APIKey) error {
	k.ID = f.nextKey
	f.nextKey++
	cp := *k
	f.keys = append(f.keys, &cp)
	f.audit("api_key_created", k.CreatedByUserID)
	return nil
}

func (f *fakeAPIAccessStore) ListAPIKeys(_ context.Context) ([]domain.APIKey, error) {
	var out []domain.APIKey
	for i := len(f.keys) - 1; i >= 0; i-- {
		out = append(out, *f.keys[i])
	}
	return out, nil
}

func (f *fakeAPIAccessStore) APIKeyByPublicID(_ context.Context, publicID string) (*domain.APIKey, error) {
	for _, k := range f.keys {
		if k.PublicID == publicID {
			cp := *k
			return &cp, nil
		}
	}
	return nil, &domain.NotFoundError{Kind: "api key", ID: publicID}
}

func (f *fakeAPIAccessStore) RevokeAPIKey(_ context.Context, id, actorID int64, at time.Time) error {
	for _, k := range f.keys {
		if k.ID == id && k.RevokedAt == nil {
			t, by := at, actorID
			k.RevokedAt, k.RevokedByUserID = &t, &by
			f.audit("api_key_revoked", actorID)
			return nil
		}
	}
	return &domain.NotFoundError{Kind: "api key", ID: id}
}

func (f *fakeAPIAccessStore) TouchAPIKey(_ context.Context, id int64, at time.Time) error {
	for _, k := range f.keys {
		if k.ID == id {
			t := at
			k.LastUsedAt = &t
		}
	}
	f.touches++
	return nil
}

func (f *fakeAPIAccessStore) APIEnabled(_ context.Context) (bool, error) { return f.enabled, nil }

func (f *fakeAPIAccessStore) SetAPIEnabled(_ context.Context, enabled bool, actorID int64, _ time.Time) error {
	f.enabled = enabled
	if enabled {
		f.audit("api_enabled", actorID)
	} else {
		f.audit("api_disabled", actorID)
	}
	return nil
}

var (
	apiAdmin = domain.User{ID: 900, Role: domain.RoleAdmin}
	apiRoot  = domain.User{ID: 901, Role: domain.RoleRoot}
	apiAgent = domain.User{ID: 902, Role: domain.RoleAgent}
)

// newAPIAccess wires the service over the fakes with a deterministic key
// material source: a counting byte sequence, so every draw differs (as public
// ids must) and runs are reproducible.
func newAPIAccess() (*application.APIAccessService, *fakeAPIAccessStore, *fakeUserStore, *fakeClock) {
	clock := fixedClock()
	users := newFakeUserStore()
	store := newFakeAPIAccessStore(users)
	material := make([]byte, 1<<16)
	for i := range material {
		material[i] = byte(i * 7)
	}
	random := bytes.NewReader(material)
	return application.NewAPIAccessService(store, users, clock, random), store, users, clock
}

func isForbidden(err error) bool {
	var f *domain.ForbiddenError
	return errors.As(err, &f)
}

func wantAPIValidation(t *testing.T, name string, err error, field string) {
	t.Helper()
	var v *domain.ValidationError
	if !errors.As(err, &v) || v.Field != field {
		t.Errorf("%s: err = %v, want a validation error on %q", name, err, field)
	}
}

// S2/S4: admin and root create service users with the user or agent role only;
// nobody else creates one, and a refusal writes nothing.
func TestAPIAccessCreateServiceUser(t *testing.T) {
	ctx := context.Background()
	svc, store, users, clock := newAPIAccess()

	u, err := svc.CreateServiceUser(ctx, apiAdmin, "  Monitoring  ", domain.RoleUser)
	if err != nil {
		t.Fatalf("admin create: %v", err)
	}
	if u.Name != "Monitoring" || u.Role != domain.RoleUser || !u.Active || !u.IsService() || u.PasswordHash != "" ||
		!strings.HasSuffix(u.Email, "@service.invalid") || !u.CreatedAt.Equal(clock.Now()) {
		t.Errorf("service user = %+v", u)
	}
	if _, err := svc.CreateServiceUser(ctx, apiRoot, "Triage bot", domain.RoleAgent); err != nil {
		t.Fatalf("root create agent service: %v", err)
	}

	before := len(users.users)
	for _, role := range []domain.Role{domain.RoleAdmin, domain.RoleRoot, ""} {
		_, err := svc.CreateServiceUser(ctx, apiAdmin, "Bot", role)
		wantAPIValidation(t, "role "+string(role), err, "role")
	}
	_, err = svc.CreateServiceUser(ctx, apiAdmin, "   ", domain.RoleUser)
	wantAPIValidation(t, "blank name", err, "name")
	for _, actor := range []domain.User{apiAgent, {ID: 903, Role: domain.RoleUser}} {
		if _, err := svc.CreateServiceUser(ctx, actor, "Bot", domain.RoleUser); !isForbidden(err) {
			t.Errorf("%s create = %v, want forbidden", actor.Role, err)
		}
		if _, err := svc.ListServiceUsers(ctx, actor); !isForbidden(err) {
			t.Errorf("%s list = %v, want forbidden", actor.Role, err)
		}
	}
	if len(users.users) != before {
		t.Errorf("refused creations stored %d users", len(users.users)-before)
	}
	if got := len(store.events); got != 2 {
		t.Errorf("audit rows = %d, want 2 (the two successful creations)", got)
	}
}

// S5/S6: a key is created for an active service user, its token is
// tkt_<12 hex>_<64 hex>, answered once, and only the SHA-256 of the secret is
// stored. Invalid input is refused with nothing stored.
func TestAPIAccessCreateAPIKey(t *testing.T) {
	ctx := context.Background()
	svc, store, users, clock := newAPIAccess()
	bot, _ := svc.CreateServiceUser(ctx, apiAdmin, "Monitoring", domain.RoleUser)
	agentBot, _ := svc.CreateServiceUser(ctx, apiAdmin, "Triage", domain.RoleAgent)
	person := users.seedRole("Ana", "ana@example.com", domain.RoleAgent, true)

	expiry := clock.Now().Add(24 * time.Hour)
	key, token, err := svc.CreateAPIKey(ctx, apiAdmin, application.CreateAPIKeyInput{
		ServiceUserID: bot.ID, Name: " Zabbix ", Scopes: []string{"tickets:write", "catalog:read", "tickets:write"}, ExpiresAt: &expiry,
	})
	if err != nil {
		t.Fatalf("create key: %v", err)
	}
	parts := strings.Split(token, "_")
	if len(parts) != 3 || parts[0] != "tkt" || len(parts[1]) != 12 || len(parts[2]) != 64 {
		t.Fatalf("token = %q, want tkt_<12 hex>_<64 hex>", token)
	}
	sum := sha256.Sum256([]byte(parts[2]))
	if key.SecretHash != hex.EncodeToString(sum[:]) || strings.Contains(key.SecretHash, parts[2]) {
		t.Errorf("stored hash = %q, want the SHA-256 of the secret", key.SecretHash)
	}
	if key.PublicID != parts[1] || key.Name != "Zabbix" || key.UserID != bot.ID || key.CreatedByUserID != apiAdmin.ID ||
		!reflect.DeepEqual(key.Scopes, []domain.APIKeyScope{domain.ScopeCatalogRead, domain.ScopeTicketsWrite}) ||
		key.ExpiresAt == nil || !key.ExpiresAt.Equal(expiry) {
		t.Errorf("key = %+v", key)
	}

	// A second key for the same service user is allowed (rotation).
	if _, _, err := svc.CreateAPIKey(ctx, apiRoot, application.CreateAPIKeyInput{ServiceUserID: bot.ID, Name: "Rotated", Scopes: []string{"tickets:read"}}); err != nil {
		t.Fatalf("second key: %v", err)
	}
	if _, _, err := svc.CreateAPIKey(ctx, apiAdmin, application.CreateAPIKeyInput{ServiceUserID: agentBot.ID, Name: "Bot", Scopes: []string{"comments:internal"}}); err != nil {
		t.Fatalf("internal scope on an agent service: %v", err)
	}

	stored := len(store.keys)
	past := clock.Now()
	for _, c := range []struct {
		name  string
		in    application.CreateAPIKeyInput
		field string
	}{
		{"blank name", application.CreateAPIKeyInput{ServiceUserID: bot.ID, Name: " ", Scopes: []string{"tickets:read"}}, "name"},
		{"no scopes", application.CreateAPIKeyInput{ServiceUserID: bot.ID, Name: "K"}, "scopes"},
		{"unknown scope", application.CreateAPIKeyInput{ServiceUserID: bot.ID, Name: "K", Scopes: []string{"users:manage"}}, "scopes"},
		{"internal on a user service", application.CreateAPIKeyInput{ServiceUserID: bot.ID, Name: "K", Scopes: []string{"comments:internal"}}, "scopes"},
		{"expiry not in the future", application.CreateAPIKeyInput{ServiceUserID: bot.ID, Name: "K", Scopes: []string{"tickets:read"}, ExpiresAt: &past}, "expires_at"},
	} {
		_, token, err := svc.CreateAPIKey(ctx, apiAdmin, c.in)
		wantAPIValidation(t, c.name, err, c.field)
		if token != "" {
			t.Errorf("%s: a refused creation answered a token", c.name)
		}
	}
	if _, _, err := svc.CreateAPIKey(ctx, apiAdmin, application.CreateAPIKeyInput{ServiceUserID: person.ID, Name: "K", Scopes: []string{"tickets:read"}}); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("key for a person = %v, want ErrNotFound", err)
	}
	if _, _, err := svc.CreateAPIKey(ctx, apiAgent, application.CreateAPIKeyInput{ServiceUserID: bot.ID, Name: "K", Scopes: []string{"tickets:read"}}); !isForbidden(err) {
		t.Errorf("agent creates a key = %v, want forbidden", err)
	}
	if err := svc.SetServiceUserActive(ctx, apiAdmin, bot.ID, false); err != nil {
		t.Fatal(err)
	}
	stored = len(store.keys)
	_, _, err = svc.CreateAPIKey(ctx, apiAdmin, application.CreateAPIKeyInput{ServiceUserID: bot.ID, Name: "K", Scopes: []string{"tickets:read"}})
	wantAPIValidation(t, "deactivated owner", err, "service_user")
	if len(store.keys) != stored {
		t.Errorf("refused creations stored %d keys", len(store.keys)-stored)
	}
}

// arrangeLiveKey enables the API and creates one key for a fresh service user.
func arrangeLiveKey(t *testing.T, svc *application.APIAccessService, store *fakeAPIAccessStore, role domain.Role, scopes ...string) (domain.User, *domain.APIKey, string) {
	t.Helper()
	ctx := context.Background()
	store.enabled = true
	bot, err := svc.CreateServiceUser(ctx, apiAdmin, "Bot", role)
	if err != nil {
		t.Fatal(err)
	}
	key, token, err := svc.CreateAPIKey(ctx, apiAdmin, application.CreateAPIKeyInput{ServiceUserID: bot.ID, Name: "K", Scopes: scopes})
	if err != nil {
		t.Fatal(err)
	}
	return *bot, key, token
}

// S8: an accepted token acts as its service user with the role capabilities
// narrowed to the key scopes, and records its use at most once per minute.
func TestAPIAccessAuthenticate(t *testing.T) {
	ctx := context.Background()
	svc, store, _, clock := newAPIAccess()
	bot, key, token := arrangeLiveKey(t, svc, store, domain.RoleAgent, "tickets:read", "comments:write")

	p, err := svc.Authenticate(ctx, token)
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if p.User.ID != bot.ID || p.KeyID != key.ID || !p.HasScope(domain.ScopeTicketsRead) || p.HasScope(domain.ScopeTicketsWrite) {
		t.Errorf("principal = %+v", p)
	}
	if !p.Capabilities.Require(application.CapCommentPublic) {
		t.Error("comments:write on an agent service does not grant public comments")
	}
	for _, c := range []application.Capability{application.CapCommentInternal, application.CapEditTicket, application.CapCreateTicket, application.CapAssignTicket, application.CapManageUsers} {
		if p.Capabilities.Require(c) {
			t.Errorf("principal holds %s, which its scopes do not allow", c)
		}
	}

	// A user-role service with every scope still holds only what the user role grants.
	_, _, userToken := arrangeLiveKey(t, svc, store, domain.RoleUser, "tickets:write", "comments:write")
	up, err := svc.Authenticate(ctx, userToken)
	if err != nil {
		t.Fatal(err)
	}
	if !up.Capabilities.Require(application.CapCreateTicket) || up.Capabilities.Require(application.CapEditTicket) || up.Capabilities.Require(application.CapAssignTicket) {
		t.Errorf("user-role service capabilities exceed the role")
	}

	// Last use: written on first use, not again within the minute, again after it.
	store.touches = 0
	if _, err := svc.Authenticate(ctx, token); err != nil {
		t.Fatal(err)
	}
	if store.touches != 0 {
		t.Errorf("touches within the minute = %d, want 0", store.touches)
	}
	clock.Advance(time.Minute)
	if _, err := svc.Authenticate(ctx, token); err != nil {
		t.Fatal(err)
	}
	if store.touches != 1 {
		t.Errorf("touches after a minute = %d, want 1", store.touches)
	}
}

// S8: every rejection kind answers the same unauthorized error (or the disabled
// error while the switch is off) and writes nothing.
func TestAPIAccessAuthenticateRejects(t *testing.T) {
	ctx := context.Background()
	svc, store, users, clock := newAPIAccess()
	bot, key, token := arrangeLiveKey(t, svc, store, domain.RoleUser, "tickets:read")
	wrongSecret := token[:len(token)-1] + "0"
	if wrongSecret == token {
		wrongSecret = token[:len(token)-1] + "1"
	}

	reject := func(name, tok string, want error) {
		t.Helper()
		before := *store.keys[0]
		eventsBefore := len(store.events)
		p, err := svc.Authenticate(ctx, tok)
		if !errors.Is(err, want) || p != nil {
			t.Errorf("%s: (%v, %v), want (nil, %v)", name, p, err, want)
		}
		if after := *store.keys[0]; !reflect.DeepEqual(after, before) {
			t.Errorf("%s: the rejection changed the key:\nbefore=%+v\nafter=%+v", name, before, after)
		}
		if len(store.events) != eventsBefore {
			t.Errorf("%s: the rejection wrote audit rows", name)
		}
	}
	reject("empty", "", application.ErrAPIUnauthorized)
	reject("no prefix", strings.TrimPrefix(token, "tkt_"), application.ErrAPIUnauthorized)
	reject("upper-case hex", strings.ToUpper(token), application.ErrAPIUnauthorized)
	reject("truncated", token[:len(token)-2], application.ErrAPIUnauthorized)
	reject("unknown public id", "tkt_000000000000_"+strings.Repeat("0", 64), application.ErrAPIUnauthorized)
	reject("wrong secret", wrongSecret, application.ErrAPIUnauthorized)

	store.enabled = false
	reject("API disabled", token, application.ErrAPIDisabled)
	store.enabled = true

	clock.Advance(-time.Hour) // keep "now" before any expiry set below
	expiry := clock.Now().Add(time.Hour)
	store.keys[0].ExpiresAt = &expiry
	clock.Advance(time.Hour)
	reject("expired at the instant", token, application.ErrAPIUnauthorized)
	store.keys[0].ExpiresAt = nil

	users.users[bot.ID].Active = false
	reject("deactivated owner", token, application.ErrAPIUnauthorized)
	users.users[bot.ID].Active = true

	if err := svc.RevokeAPIKey(ctx, apiAdmin, key.ID); err != nil {
		t.Fatal(err)
	}
	reject("revoked", token, application.ErrAPIUnauthorized)
}

// S8/S9: deactivating a service user revokes its live keys and audits;
// reactivation revives no key. Revocation is admin+, audited, and a second
// revocation answers NotFound.
func TestAPIAccessDeactivationAndRevocation(t *testing.T) {
	ctx := context.Background()
	svc, store, users, _ := newAPIAccess()
	bot, key, token := arrangeLiveKey(t, svc, store, domain.RoleUser, "tickets:read")
	person := users.seedRole("Ana", "ana@example.com", domain.RoleAgent, true)

	if err := svc.RevokeAPIKey(ctx, apiAgent, key.ID); !isForbidden(err) {
		t.Errorf("agent revoke = %v, want forbidden", err)
	}
	if err := svc.SetServiceUserActive(ctx, apiAgent, bot.ID, false); !isForbidden(err) {
		t.Errorf("agent deactivate = %v, want forbidden", err)
	}
	if err := svc.SetServiceUserActive(ctx, apiAdmin, person.ID, false); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("deactivate a person through the API screen = %v, want ErrNotFound", err)
	}
	if !users.users[person.ID].Active || store.keys[0].RevokedAt != nil {
		t.Fatal("a refused action changed state")
	}

	if err := svc.SetServiceUserActive(ctx, apiAdmin, bot.ID, false); err != nil {
		t.Fatal(err)
	}
	if store.keys[0].RevokedAt == nil || *store.keys[0].RevokedByUserID != apiAdmin.ID {
		t.Errorf("deactivation left the key live: %+v", store.keys[0])
	}
	if err := svc.SetServiceUserActive(ctx, apiAdmin, bot.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Authenticate(ctx, token); !errors.Is(err, application.ErrAPIUnauthorized) {
		t.Errorf("reactivation revived the key: %v", err)
	}
	if err := svc.RevokeAPIKey(ctx, apiAdmin, key.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("revoking a revoked key = %v, want ErrNotFound", err)
	}

	var actions []string
	for _, e := range store.events {
		actions = append(actions, e.action)
	}
	want := []string{"service_user_created", "api_key_created", "service_user_deactivated", "service_user_reactivated"}
	if !reflect.DeepEqual(actions, want) {
		t.Errorf("audit actions = %v, want %v", actions, want)
	}
}

// S7/S9: the API switch is off by default; only root changes it, and each
// change is audited.
func TestAPIAccessToggle(t *testing.T) {
	ctx := context.Background()
	svc, store, _, _ := newAPIAccess()

	if on, err := svc.APIEnabled(ctx); err != nil || on {
		t.Fatalf("default = (%v, %v), want off", on, err)
	}
	for _, actor := range []domain.User{apiAdmin, apiAgent} {
		if err := svc.SetAPIEnabled(ctx, actor, true); !isForbidden(err) {
			t.Errorf("%s toggle = %v, want forbidden", actor.Role, err)
		}
	}
	if store.enabled || len(store.events) != 0 {
		t.Fatal("a refused toggle changed state")
	}
	if err := svc.SetAPIEnabled(ctx, apiRoot, true); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetAPIEnabled(ctx, apiRoot, false); err != nil {
		t.Fatal(err)
	}
	if len(store.events) != 2 || store.events[0] != (fakeAPIEvent{"api_enabled", apiRoot.ID}) || store.events[1] != (fakeAPIEvent{"api_disabled", apiRoot.ID}) {
		t.Errorf("audit = %+v", store.events)
	}
}

// S5: the list shows every service user with its keys, newest first.
func TestAPIAccessListServiceUsers(t *testing.T) {
	ctx := context.Background()
	svc, store, users, _ := newAPIAccess()
	users.seedRole("Ana", "ana@example.com", domain.RoleAgent, true)
	bot, first, _ := arrangeLiveKey(t, svc, store, domain.RoleUser, "tickets:read")
	second, _, err := svc.CreateAPIKey(ctx, apiAdmin, application.CreateAPIKeyInput{ServiceUserID: bot.ID, Name: "K2", Scopes: []string{"catalog:read"}})
	if err != nil {
		t.Fatal(err)
	}
	other, _ := svc.CreateServiceUser(ctx, apiAdmin, "Other", domain.RoleAgent)

	views, err := svc.ListServiceUsers(ctx, apiAdmin)
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 2 || views[0].User.ID != bot.ID || views[1].User.ID != other.ID {
		t.Fatalf("views = %+v, want the two service users only", views)
	}
	if len(views[0].Keys) != 2 || views[0].Keys[0].ID != second.ID || views[0].Keys[1].ID != first.ID || len(views[1].Keys) != 0 {
		t.Errorf("keys = %+v", views[0].Keys)
	}
}
