package sqlite

import (
	"context"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
)

// Instance password policy (issue #414, S1/S4): the two switches and the
// minimum length live in the keyed settings table, so no migration is needed —
// an absent row reads back the documented default. Written before the port
// exists — compile-error RED.

// passwordPolicyRows counts how many of the three keys are stored, so a test
// can prove the default needs no row and a save writes exactly three.
func passwordPolicyRows(t *testing.T, s *Store) int {
	t.Helper()
	var rows int
	if err := s.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM settings WHERE key IN (?, ?, ?)`,
		settingsKeyPasswordChangeEnabled, settingsKeyPasswordRecoveryEnabled, settingsKeyPasswordMinLength).Scan(&rows); err != nil {
		t.Fatalf("count password settings rows: %v", err)
	}
	return rows
}

// TestPasswordSettingsStoreDefaultsWithoutRows proves a fresh instance needs no
// stored row: both self-service paths are ON and the minimum is 12 (S1, S4).
func TestPasswordSettingsStoreDefaultsWithoutRows(t *testing.T) {
	s := newTestDB(t)

	if got := passwordPolicyRows(t, s); got != 0 {
		t.Fatalf("password settings rows on a fresh database = %d, want 0 (the default needs no row)", got)
	}
	got, err := s.PasswordSettingsStore().GetPasswordSettings(context.Background())
	if err != nil {
		t.Fatalf("get password settings: %v", err)
	}
	want := application.PasswordSettings{ChangeEnabled: true, RecoveryEnabled: true, MinLength: 12}
	if got != want {
		t.Fatalf("password settings = %+v, want the documented default %+v", got, want)
	}
}

// TestPasswordSettingsStoreRoundTrip proves a save persists all three values
// and reads back exactly what an administrator submitted.
func TestPasswordSettingsStoreRoundTrip(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	st := s.PasswordSettingsStore()

	want := application.PasswordSettings{ChangeEnabled: false, RecoveryEnabled: false, MinLength: application.MinPasswordMinLength}
	if err := st.SetPasswordSettings(ctx, want); err != nil {
		t.Fatalf("set password settings: %v", err)
	}
	got, err := st.GetPasswordSettings(ctx)
	if err != nil {
		t.Fatalf("get password settings: %v", err)
	}
	if got != want {
		t.Fatalf("password settings = %+v, want %+v", got, want)
	}
	if rows := passwordPolicyRows(t, s); rows != 3 {
		t.Errorf("password settings rows = %d, want exactly 3 (one per key, never duplicated)", rows)
	}

	// A second save overwrites in place rather than accumulating rows.
	want = application.PasswordSettings{ChangeEnabled: true, RecoveryEnabled: true, MinLength: application.MaxPasswordMinLength}
	if err := st.SetPasswordSettings(ctx, want); err != nil {
		t.Fatalf("set password settings again: %v", err)
	}
	if got, err := st.GetPasswordSettings(ctx); err != nil || got != want {
		t.Fatalf("password settings after the second save = %+v (err %v), want %+v", got, err, want)
	}
	if rows := passwordPolicyRows(t, s); rows != 3 {
		t.Errorf("password settings rows after the second save = %d, want exactly 3", rows)
	}
}

// TestPasswordSettingsStoreReadsFailSafe covers the hand-edited database the
// port documents: an unparseable or out-of-band minimum reads back the default
// 12, and an unparseable switch reads back ON. A stored value the write path
// refuses is never trusted to weaken or block every password surface.
func TestPasswordSettingsStoreReadsFailSafe(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()

	cases := []struct {
		name         string
		stored       map[string]string
		wantChange   bool
		wantRecovery bool
		wantMin      int
	}{
		{
			name:         "minimum is not a number",
			stored:       map[string]string{settingsKeyPasswordMinLength: "twelve"},
			wantChange:   true,
			wantRecovery: true,
			wantMin:      application.DefaultPasswordMinLength,
		},
		{
			name:         "minimum is below the band",
			stored:       map[string]string{settingsKeyPasswordMinLength: "7"},
			wantChange:   true,
			wantRecovery: true,
			wantMin:      application.DefaultPasswordMinLength,
		},
		{
			name:         "minimum is above the band",
			stored:       map[string]string{settingsKeyPasswordMinLength: "65"},
			wantChange:   true,
			wantRecovery: true,
			wantMin:      application.DefaultPasswordMinLength,
		},
		{
			name:         "minimum is empty",
			stored:       map[string]string{settingsKeyPasswordMinLength: ""},
			wantChange:   true,
			wantRecovery: true,
			wantMin:      application.DefaultPasswordMinLength,
		},
		{
			name: "switches are unparseable",
			stored: map[string]string{
				settingsKeyPasswordChangeEnabled:   "yes",
				settingsKeyPasswordRecoveryEnabled: "",
			},
			wantChange:   true,
			wantRecovery: true,
			wantMin:      application.DefaultPasswordMinLength,
		},
		{
			name: "switches are off",
			stored: map[string]string{
				settingsKeyPasswordChangeEnabled:   "0",
				settingsKeyPasswordRecoveryEnabled: "0",
				settingsKeyPasswordMinLength:       "9",
			},
			wantChange:   false,
			wantRecovery: false,
			wantMin:      9,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for key, value := range tc.stored {
				if _, err := s.db.ExecContext(ctx, `
					INSERT INTO settings (key, value) VALUES (?, ?)
					ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value); err != nil {
					t.Fatalf("store %s=%q: %v", key, value, err)
				}
			}
			got, err := s.PasswordSettingsStore().GetPasswordSettings(ctx)
			if err != nil {
				t.Fatalf("get password settings: %v", err)
			}
			want := application.PasswordSettings{ChangeEnabled: tc.wantChange, RecoveryEnabled: tc.wantRecovery, MinLength: tc.wantMin}
			if got != want {
				t.Fatalf("password settings = %+v, want %+v", got, want)
			}
		})
	}
}

// TestPasswordSettingsStoreSetRollsBackAtomically proves the three values are
// ONE unit: aborting the last write leaves the previous policy standing, never
// the switches of a save the minimum never reached.
func TestPasswordSettingsStoreSetRollsBackAtomically(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	st := s.PasswordSettingsStore()

	seeded := application.PasswordSettings{ChangeEnabled: true, RecoveryEnabled: true, MinLength: 12}
	if err := st.SetPasswordSettings(ctx, seeded); err != nil {
		t.Fatalf("seed policy: %v", err)
	}

	// Abort the LAST write of the three so the transaction has to roll the
	// switches back with it.
	if _, err := s.db.ExecContext(ctx, `
		CREATE TRIGGER abort_password_min_length BEFORE INSERT ON settings
		WHEN NEW.key = '`+settingsKeyPasswordMinLength+`'
		BEGIN SELECT RAISE(ABORT, 'boom'); END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}
	t.Cleanup(func() {
		// context.Background(), NOT t.Context(): t.Context() is cancelled during
		// cleanup, so the drop would fail with "context canceled".
		if _, err := s.db.ExecContext(context.Background(), `DROP TRIGGER abort_password_min_length`); err != nil {
			t.Errorf("drop trigger: %v", err)
		}
	})

	err := st.SetPasswordSettings(ctx, application.PasswordSettings{ChangeEnabled: false, RecoveryEnabled: false, MinLength: 8})
	if err == nil {
		t.Fatal("the save succeeded with its last write aborted, want an error")
	}
	if got, getErr := st.GetPasswordSettings(ctx); getErr != nil || got != seeded {
		t.Errorf("password settings after the rollback = %+v (err %v), want the seeded %+v", got, getErr, seeded)
	}
}
