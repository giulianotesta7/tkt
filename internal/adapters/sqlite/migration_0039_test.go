package sqlite

import (
	"context"
	"testing"
)

func TestMigration0039Addresses(t *testing.T) {
	for _, tc := range []struct{ name, email, company, address, conflict string }{
		{"none", "", "", "", ""},
		{"email", "https://email.example/help", "", "https://email.example/help", ""},
		{"company", "", "https://company.example/help", "https://company.example/help", ""},
		{"equal", "https://same.example/help", "https://same.example/help", "https://same.example/help", ""},
		{"different", "https://email.example/help", "https://company.example/help", "https://email.example/help", "https://company.example/help"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			s, err := openDSN(testDSN(t))
			if err != nil {
				t.Fatal(err)
			}
			s.db.SetMaxOpenConns(1)
			t.Cleanup(func() { _ = s.Close() })
			if err := migrate(ctx, s.db, migrationsBefore(t, "0039_instance_address.sql", false)); err != nil {
				t.Fatal(err)
			}
			seedUserRaw(t, s, "Root", "root@example.com", "root")
			for key, value := range map[string]string{"email_base_url": tc.email, "company_signin_address": tc.company} {
				if value != "" {
					if _, err := s.db.Exec(`INSERT INTO settings (key, value) VALUES (?, ?)`, key, value); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := s.Migrate(ctx); err != nil {
				t.Fatal(err)
			}
			if err := s.Migrate(ctx); err != nil {
				t.Fatal(err)
			}
			ss := newSettingsStore(s.db)
			for key, want := range map[string]string{"instance_address": tc.address, "instance_address_company_conflict": tc.conflict} {
				got, err := ss.settingValue(ctx, key)
				if err != nil || got != want {
					t.Errorf("%s = %q, %v; want %q", key, got, err, want)
				}
			}
			var legacy, version int
			if err := s.db.QueryRow(`SELECT COUNT(*) FROM settings WHERE key IN ('email_base_url', 'company_signin_address')`).Scan(&legacy); err != nil {
				t.Fatal(err)
			}
			if err := s.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version=39`).Scan(&version); err != nil {
				t.Fatal(err)
			}
			if legacy != 0 || version != 1 {
				t.Errorf("legacy=%d version count=%d", legacy, version)
			}
		})
	}
}
