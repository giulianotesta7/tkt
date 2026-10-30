package sqlite

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// API access store tests (issue #488): every administrative mutation and its
// api_admin_events row commit together, deactivation revokes the live keys of
// that service user only, and the API switch defaults to off.

func itoa(v int64) string { return strconv.FormatInt(v, 10) }

func apiAdminActions(t *testing.T, s *Store) []string {
	t.Helper()
	rows, err := s.db.Query(`SELECT action || ':' || actor_user_id || ':' || COALESCE(service_user_id, '') || ':' || COALESCE(api_key_id, '')
		FROM api_admin_events ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			t.Fatal(err)
		}
		out = append(out, a)
	}
	return out
}

func TestAPIAccessStoreLifecycle(t *testing.T) {
	ctx := context.Background()
	s := newTestDB(t)
	store := s.APIAccessStore()
	adminID := seedUserRaw(t, s, "Admin", "admin@example.com", "admin")

	bot := &domain.User{Name: "Monitoring", Email: "service-1@service.invalid", Role: domain.RoleUser, Active: true, CreatedAt: testClock}
	if err := store.CreateServiceUser(ctx, bot, adminID); err != nil {
		t.Fatalf("create service user: %v", err)
	}
	other := &domain.User{Name: "Other", Email: "service-2@service.invalid", Role: domain.RoleAgent, Active: true, CreatedAt: testClock}
	if err := store.CreateServiceUser(ctx, other, adminID); err != nil {
		t.Fatal(err)
	}
	if got, err := s.UserStore().GetByID(ctx, bot.ID); err != nil || !got.IsService() || got.PasswordHash != "" {
		t.Fatalf("stored service user = %+v (err %v)", got, err)
	}
	listed, err := store.ListServiceUsers(ctx)
	if err != nil || len(listed) != 2 {
		t.Fatalf("ListServiceUsers = %+v (err %v)", listed, err)
	}

	expiry := testClock.Add(48 * time.Hour)
	key := &domain.APIKey{PublicID: "a1b2c3d4e5f6", UserID: bot.ID, Name: "Zabbix", SecretHash: strings.Repeat("f", 64),
		Scopes: []domain.APIKeyScope{domain.ScopeCatalogRead, domain.ScopeTicketsWrite}, CreatedByUserID: adminID, CreatedAt: testClock, ExpiresAt: &expiry}
	if err := store.CreateAPIKey(ctx, key); err != nil {
		t.Fatalf("create key: %v", err)
	}
	otherKey := &domain.APIKey{PublicID: "0f0f0f0f0f0f", UserID: other.ID, Name: "Bot", SecretHash: strings.Repeat("e", 64),
		Scopes: []domain.APIKeyScope{domain.ScopeTicketsRead}, CreatedByUserID: adminID, CreatedAt: testClock}
	if err := store.CreateAPIKey(ctx, otherKey); err != nil {
		t.Fatal(err)
	}

	got, err := store.APIKeyByPublicID(ctx, "a1b2c3d4e5f6")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != key.ID || got.UserID != bot.ID || got.SecretHash != key.SecretHash || !reflect.DeepEqual(got.Scopes, key.Scopes) ||
		got.ExpiresAt == nil || !got.ExpiresAt.Equal(expiry) || got.LastUsedAt != nil || got.RevokedAt != nil {
		t.Errorf("key round trip = %+v", got)
	}
	if _, err := store.APIKeyByPublicID(ctx, "missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("unknown public id = %v, want ErrNotFound", err)
	}
	keys, err := store.ListAPIKeys(ctx)
	if err != nil || len(keys) != 2 || keys[0].ID != otherKey.ID {
		t.Errorf("ListAPIKeys = %+v (err %v), want newest first", keys, err)
	}

	used := testClock.Add(time.Hour)
	if err := store.TouchAPIKey(ctx, key.ID, used); err != nil {
		t.Fatal(err)
	}
	if got, _ := store.APIKeyByPublicID(ctx, "a1b2c3d4e5f6"); got.LastUsedAt == nil || !got.LastUsedAt.Equal(used) {
		t.Errorf("last used = %v, want %v", got.LastUsedAt, used)
	}

	// Deactivation revokes this service user's live keys only.
	at := testClock.Add(2 * time.Hour)
	if err := store.SetServiceUserActive(ctx, bot.ID, false, adminID, at); err != nil {
		t.Fatal(err)
	}
	if got, _ := store.APIKeyByPublicID(ctx, "a1b2c3d4e5f6"); got.RevokedAt == nil || !got.RevokedAt.Equal(at) || got.RevokedByUserID == nil || *got.RevokedByUserID != adminID {
		t.Errorf("deactivation left the key live: %+v", got)
	}
	if got, _ := store.APIKeyByPublicID(ctx, "0f0f0f0f0f0f"); got.RevokedAt != nil {
		t.Error("deactivation revoked another service user's key")
	}
	if err := store.SetServiceUserActive(ctx, bot.ID, true, adminID, at); err != nil {
		t.Fatal(err)
	}
	if got, _ := store.APIKeyByPublicID(ctx, "a1b2c3d4e5f6"); got.RevokedAt == nil {
		t.Error("reactivation revived a key")
	}
	if err := store.SetServiceUserActive(ctx, adminID, false, adminID, at); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("deactivating a person = %v, want ErrNotFound", err)
	}

	if err := store.RevokeAPIKey(ctx, otherKey.ID, adminID, at); err != nil {
		t.Fatal(err)
	}
	if err := store.RevokeAPIKey(ctx, otherKey.ID, adminID, at); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("second revocation = %v, want ErrNotFound", err)
	}

	if on, err := store.APIEnabled(ctx); err != nil || on {
		t.Fatalf("API switch default = (%v, %v), want off", on, err)
	}
	if err := store.SetAPIEnabled(ctx, true, adminID, at); err != nil {
		t.Fatal(err)
	}
	if on, _ := store.APIEnabled(ctx); !on {
		t.Error("API switch did not turn on")
	}
	if err := store.SetAPIEnabled(ctx, false, adminID, at); err != nil {
		t.Fatal(err)
	}
	if on, _ := store.APIEnabled(ctx); on {
		t.Error("API switch did not turn off")
	}

	a, b, k1, k2 := itoa(adminID), itoa(bot.ID), itoa(key.ID), itoa(otherKey.ID)
	want := []string{
		"service_user_created:" + a + ":" + b + ":",
		"service_user_created:" + a + ":" + itoa(other.ID) + ":",
		"api_key_created:" + a + ":" + b + ":" + k1,
		"api_key_created:" + a + ":" + itoa(other.ID) + ":" + k2,
		"service_user_deactivated:" + a + ":" + b + ":",
		"service_user_reactivated:" + a + ":" + b + ":",
		"api_key_revoked:" + a + ":" + itoa(other.ID) + ":" + k2,
		"api_enabled:" + a + "::",
		"api_disabled:" + a + "::",
	}
	if got := apiAdminActions(t, s); !reflect.DeepEqual(got, want) {
		t.Errorf("admin events:\n got %v\nwant %v", got, want)
	}
}

// A refused key insert (owned by a person) rolls its audit row back with it.
func TestAPIAccessStoreRefusedKeyWritesNoAudit(t *testing.T) {
	ctx := context.Background()
	s := newTestDB(t)
	adminID := seedUserRaw(t, s, "Admin", "admin@example.com", "admin")
	err := s.APIAccessStore().CreateAPIKey(ctx, &domain.APIKey{PublicID: "a1b2c3d4e5f6", UserID: adminID, Name: "K",
		SecretHash: strings.Repeat("f", 64), Scopes: []domain.APIKeyScope{domain.ScopeTicketsRead}, CreatedByUserID: adminID, CreatedAt: testClock})
	if err == nil {
		t.Fatal("key for a person stored")
	}
	if got := apiAdminActions(t, s); len(got) != 0 {
		t.Errorf("admin events = %v, want none", got)
	}
}
