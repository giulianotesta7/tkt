package sqlite

import (
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

func seedInstanceAddress(t *testing.T, s *Store, address string) {
	t.Helper()
	root := seedUserRaw(t, s, "Root", "root@example.com", "root")
	if err := s.InstanceSettingsStore().SetInstanceAddress(t.Context(), root, address, time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
}

func TestInstanceAddressAuditAndConsumerGuards(t *testing.T) {
	s := newTestDB(t)
	ctx := t.Context()
	root := seedUserRaw(t, s, "Root", "root@example.com", "root")
	store := s.InstanceSettingsStore()
	at := time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC)
	address := "https://tkt.example.com/help"
	if err := store.SetInstanceAddress(ctx, root, address, at); err != nil {
		t.Fatal(err)
	}
	if err := store.SetInstanceAddress(ctx, root, address, at); err != nil {
		t.Fatal(err)
	}
	events, err := store.ListInstanceAddressEvents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []application.InstanceAddressEvent{{ActorUserID: root, Address: address, CreatedAt: at}}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("audit = %+v, want %+v", events, want)
	}
	// An audit failure must roll the address change back as well.
	if err := store.SetInstanceAddress(ctx, root+999, "https://other.example", at); err == nil {
		t.Fatal("unknown actor accepted")
	}
	for _, consumer := range []string{"email", "google", "microsoft"} {
		t.Run(consumer, func(t *testing.T) {
			email := application.EmailSettings{Port: 25, Encryption: application.EmailEncryptionNone, BaseURL: "https://stale.example"}
			company := application.DefaultCompanySignInSettings()
			company.Address = "https://stale.example"
			email.Enabled = consumer == "email"
			company.Google.Enabled = consumer == "google"
			company.Microsoft.Enabled = consumer == "microsoft"
			if err := s.EmailSettingsStore().SetEmailSettings(ctx, email); err != nil {
				t.Fatal(err)
			}
			if err := s.CompanySignInSettingsStore().SetCompanySignIn(ctx, company); err != nil {
				t.Fatal(err)
			}
			err := store.SetInstanceAddress(ctx, root, "", at)
			var validation *domain.ValidationError
			if !errors.As(err, &validation) || validation.Key != "error.instance_address_in_use" {
				t.Fatalf("clear = %v", err)
			}
			got, err := store.GetInstanceAddress(ctx)
			if err != nil || got.Address != address {
				t.Fatalf("address after refusal = %+v, %v", got, err)
			}
			current, err := store.ListInstanceAddressEvents(ctx)
			if err != nil || !reflect.DeepEqual(current, events) {
				t.Fatalf("audit changed after refusal: %+v, %v", current, err)
			}
			e, err := s.EmailSettingsStore().GetEmailSettings(ctx)
			if err != nil || e.BaseURL != address {
				t.Fatalf("email read = %q, %v", e.BaseURL, err)
			}
			c, err := s.CompanySignInSettingsStore().GetCompanySignIn(ctx)
			if err != nil || c.Address != address {
				t.Fatalf("company read = %q, %v", c.Address, err)
			}
		})
	}
	if err := s.CompanySignInSettingsStore().SetCompanySignIn(ctx, application.DefaultCompanySignInSettings()); err != nil {
		t.Fatal(err)
	}
	if err := store.SetInstanceAddress(ctx, root, "", at); err != nil {
		t.Fatal(err)
	}
	for _, consumer := range []string{"email", "google", "microsoft"} {
		var err error
		if consumer == "email" {
			err = s.EmailSettingsStore().SetEmailSettings(ctx, application.EmailSettings{Enabled: true})
		} else {
			company := application.DefaultCompanySignInSettings()
			company.Google.Enabled = consumer == "google"
			company.Microsoft.Enabled = consumer == "microsoft"
			err = s.CompanySignInSettingsStore().SetCompanySignIn(ctx, company)
		}
		var validation *domain.ValidationError
		if !errors.As(err, &validation) || validation.Key != "error.instance_address_required" {
			t.Errorf("enable %s without address = %v", consumer, err)
		}
	}
	var legacy int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM settings WHERE key IN ('email_base_url', 'company_signin_address')`).Scan(&legacy); err != nil {
		t.Fatal(err)
	}
	if legacy != 0 {
		t.Fatal("consumer saves recreated legacy rows")
	}
}

func TestMigration0039AuditShape(t *testing.T) {
	s := newTestDB(t)
	for name, typ := range map[string]string{"id": "INTEGER", "actor_user_id": "INTEGER", "previous_address": "TEXT", "address": "TEXT", "created_at": "TEXT"} {
		var got string
		var notNull int
		if err := s.db.QueryRow(`SELECT type, "notnull" FROM pragma_table_info('instance_address_events') WHERE name = ?`, name).Scan(&got, &notNull); err != nil {
			t.Fatal(err)
		}
		if got != typ || (name != "id" && notNull != 1) {
			t.Errorf("%s: type=%s notnull=%d", name, got, notNull)
		}
	}
	var table, column, onDelete string
	if err := s.db.QueryRow(`SELECT "table", "to", on_delete FROM pragma_foreign_key_list('instance_address_events') WHERE "from" = 'actor_user_id'`).Scan(&table, &column, &onDelete); err != nil {
		t.Fatal(err)
	}
	if table != "users" || column != "id" || onDelete != "NO ACTION" {
		t.Fatalf("audit foreign key = %s.%s %s", table, column, onDelete)
	}
}

func TestInstanceAddressGuardsMCPSwitch(t *testing.T) {
	s := newTestDB(t)
	ctx := t.Context()
	root := seedUserRaw(t, s, "Root", "root@example.com", "root")
	at := time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC)
	instance, oauth := s.InstanceSettingsStore(), s.OAuthStore()
	requireKey := func(err error, key string) {
		t.Helper()
		var validation *domain.ValidationError
		if !errors.As(err, &validation) || validation.Key != key {
			t.Fatalf("error = %v, want %s", err, key)
		}
	}
	requireKey(oauth.SetMCPEnabled(ctx, true, root, at), "error.instance_address_required")
	if on, err := oauth.MCPEnabled(ctx); err != nil || on {
		t.Fatalf("refused enable left MCP on=%v err=%v", on, err)
	}
	address := "https://tkt.example.com"
	if err := instance.SetInstanceAddress(ctx, root, address, at); err != nil {
		t.Fatal(err)
	}
	if err := oauth.SetMCPEnabled(ctx, true, root, at); err != nil {
		t.Fatal(err)
	}
	requireKey(instance.SetInstanceAddress(ctx, root, "", at), "error.instance_address_in_use")
	if got, err := instance.GetInstanceAddress(ctx); err != nil || got.Address != address {
		t.Fatalf("address after refusal = %+v, %v", got, err)
	}
	// Turning MCP off lifts the guard; the switch never needs an address to go off.
	if err := oauth.SetMCPEnabled(ctx, false, root, at); err != nil {
		t.Fatal(err)
	}
	if err := instance.SetInstanceAddress(ctx, root, "", at); err != nil {
		t.Fatal(err)
	}
	if err := oauth.SetMCPEnabled(ctx, false, root, at); err != nil {
		t.Fatal(err)
	}
}

// TestInstanceAddressMCPEnableClearRace proves enabling MCP and clearing the
// address cannot interleave into "enabled with no address".
func TestInstanceAddressMCPEnableClearRace(t *testing.T) {
	s := newTestDB(t)
	ctx := t.Context()
	root := seedUserRaw(t, s, "Root", "root@example.com", "root")
	at := time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC)
	instance, oauth := s.InstanceSettingsStore(), s.OAuthStore()
	for round := 0; round < 30; round++ {
		if err := instance.SetInstanceAddress(ctx, root, "https://tkt.example.com", at); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); _ = oauth.SetMCPEnabled(ctx, true, root, at) }()
		go func() { defer wg.Done(); _ = instance.SetInstanceAddress(ctx, root, "", at) }()
		wg.Wait()
		on, err := oauth.MCPEnabled(ctx)
		if err != nil {
			t.Fatal(err)
		}
		got, err := instance.GetInstanceAddress(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if on && got.Address == "" {
			t.Fatalf("round %d: MCP enabled with no instance address", round)
		}
		if err := oauth.SetMCPEnabled(ctx, false, root, at); err != nil {
			t.Fatal(err)
		}
	}
}
