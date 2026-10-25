package application_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// Self-service password, T1 (issue #414, S1/S4): the two switches and the
// minimum length are ONE instance configuration behind their own port, so the
// settings fakes never widen, and the minimum is enforced for every NEW
// password. Written before the port and the service exist — compile-error RED.

// TestDefaultPasswordSettingsAreBothSwitchesOnAndTwelve pins the documented
// default (S1: both start ON; S4: 12 unless an administrator changes it), so an
// instance that never saved the section answers exactly this.
func TestDefaultPasswordSettingsAreBothSwitchesOnAndTwelve(t *testing.T) {
	got := application.DefaultPasswordSettings()
	want := application.PasswordSettings{ChangeEnabled: true, RecoveryEnabled: true, MinLength: 12}
	if got != want {
		t.Fatalf("DefaultPasswordSettings() = %+v, want %+v", got, want)
	}
	if application.DefaultPasswordMinLength != 12 {
		t.Errorf("DefaultPasswordMinLength = %d, want 12", application.DefaultPasswordMinLength)
	}
	if application.MinPasswordMinLength != 8 || application.MaxPasswordMinLength != 64 {
		t.Errorf("the configurable band = %d..%d, want 8..64",
			application.MinPasswordMinLength, application.MaxPasswordMinLength)
	}
}

// TestValidateNewPasswordEnforcesTheMinimumInRunes covers the S4 rule at its
// boundary: the minimum counts CHARACTERS, never bytes, so eleven runes are
// refused however many bytes they take and twelve are accepted. A blank
// submission keeps the historical refusal.
func TestValidateNewPasswordEnforcesTheMinimumInRunes(t *testing.T) {
	cases := []struct {
		name     string
		password string
		wantKey  string
		wantArgs []any
	}{
		{"eleven characters", "abcdefghijk", "error.password_too_short", []any{12}},
		// Eleven runes in 22 bytes: a byte-counting rule would accept it.
		{"eleven multibyte runes", strings.Repeat("ñ", 11), "error.password_too_short", []any{12}},
		{"twelve characters", "abcdefghijkl", "", nil},
		{"twelve multibyte runes", strings.Repeat("ñ", 12), "", nil},
		{"blank", "   ", "error.password_required", nil},
		{"empty", "", "error.password_required", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := application.ValidateNewPassword(tc.password, application.DefaultPasswordMinLength)
			if tc.wantKey == "" {
				if err != nil {
					t.Fatalf("ValidateNewPassword(%q) = %v, want accepted", tc.password, err)
				}
				return
			}
			var verr *domain.ValidationError
			if !errors.As(err, &verr) {
				t.Fatalf("ValidateNewPassword(%q) = %v, want a ValidationError", tc.password, err)
			}
			if verr.Field != "password" {
				t.Errorf("field = %q, want %q", verr.Field, "password")
			}
			if verr.Key != tc.wantKey {
				t.Errorf("key = %q, want %q", verr.Key, tc.wantKey)
			}
			if len(verr.Args) != len(tc.wantArgs) {
				t.Fatalf("args = %v, want %v", verr.Args, tc.wantArgs)
			}
			for i, want := range tc.wantArgs {
				if verr.Args[i] != want {
					t.Errorf("arg %d = %v, want %v", i, verr.Args[i], want)
				}
			}
		})
	}
}

// TestValidateNewPasswordRefusesAPasswordOverTheHashByteCeiling covers the one
// length rule that counts BYTES rather than characters (issue #414, S4): the
// hash function accepts at most 72 bytes of input, so a longer password is
// refused as a validation failure instead of reaching it. The boundary is exact,
// a multibyte password is measured in bytes for this rule only, and the blank
// refusal still comes first.
func TestValidateNewPasswordRefusesAPasswordOverTheHashByteCeiling(t *testing.T) {
	cases := []struct {
		name     string
		password string
		wantKey  string
		wantArgs []any
	}{
		{"72 bytes", strings.Repeat("a", 72), "", nil},
		{"73 bytes", strings.Repeat("a", 73), "error.password_too_long", []any{72}},
		// 37 runes in 74 bytes: the character rule accepts it, the byte ceiling
		// does not, because the hash works on bytes.
		{"37 multibyte runes", strings.Repeat("ñ", 37), "error.password_too_long", []any{72}},
		// Blank beats too long: the historical refusal keeps its precedence.
		{"blank over the ceiling", strings.Repeat(" ", 80), "error.password_required", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := application.ValidateNewPassword(tc.password, application.DefaultPasswordMinLength)
			if tc.wantKey == "" {
				if err != nil {
					t.Fatalf("ValidateNewPassword(%d bytes) = %v, want accepted", len(tc.password), err)
				}
				return
			}
			var verr *domain.ValidationError
			if !errors.As(err, &verr) {
				t.Fatalf("ValidateNewPassword(%d bytes) = %v, want a ValidationError", len(tc.password), err)
			}
			if verr.Field != "password" {
				t.Errorf("field = %q, want %q", verr.Field, "password")
			}
			if verr.Key != tc.wantKey {
				t.Errorf("key = %q, want %q", verr.Key, tc.wantKey)
			}
			if len(verr.Args) != len(tc.wantArgs) {
				t.Fatalf("args = %v, want %v", verr.Args, tc.wantArgs)
			}
			for i, want := range tc.wantArgs {
				if verr.Args[i] != want {
					t.Errorf("arg %d = %v, want %v", i, verr.Args[i], want)
				}
			}
		})
	}
}

// TestValidateNewPasswordHonoursAConfiguredMinimum proves the rule reads the
// configured band edge, not the default: a configured 8 accepts eight
// characters and still refuses seven.
func TestValidateNewPasswordHonoursAConfiguredMinimum(t *testing.T) {
	if err := application.ValidateNewPassword("abcdefgh", application.MinPasswordMinLength); err != nil {
		t.Fatalf("eight characters at a configured 8 = %v, want accepted", err)
	}
	if err := application.ValidateNewPassword("abcdefg", application.MinPasswordMinLength); err == nil {
		t.Fatal("seven characters at a configured 8 must be refused")
	}
}

// TestPasswordSettingsServiceGetAnswersTheStoredPolicy proves the read reaches
// the port unchanged, so every surface renders one truth.
func TestPasswordSettingsServiceGetAnswersTheStoredPolicy(t *testing.T) {
	store := newFakePasswordSettingsStore()
	store.settings = application.PasswordSettings{ChangeEnabled: false, RecoveryEnabled: true, MinLength: 9}
	store.stored = true

	got, err := application.NewPasswordSettingsService(store).Get(context.Background())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != store.settings {
		t.Fatalf("Get = %+v, want %+v", got, store.settings)
	}
}

// TestPasswordSettingsServiceUpdateRefusesNonAdmins proves the CapManageSettings
// gate (S1): an agent or a requester changes nothing, and the store is never
// reached.
func TestPasswordSettingsServiceUpdateRefusesNonAdmins(t *testing.T) {
	for _, role := range []domain.Role{domain.RoleAgent, domain.RoleUser} {
		t.Run(string(role), func(t *testing.T) {
			store := newFakePasswordSettingsStore()
			seeded := application.DefaultPasswordSettings()
			seeded.MinLength = 20
			store.settings = seeded
			store.stored = true
			svc := application.NewPasswordSettingsService(store)

			err := svc.Update(context.Background(), domain.User{ID: 7, Role: role},
				application.PasswordSettingsInput{ChangeEnabled: false, RecoveryEnabled: false, MinLength: 9})
			var ferr *domain.ForbiddenError
			if !errors.As(err, &ferr) {
				t.Fatalf("Update as %s = %v, want a ForbiddenError", role, err)
			}
			if store.setCalls != 0 {
				t.Errorf("Update as %s reached the store %d times, want 0", role, store.setCalls)
			}
			if store.settings != seeded {
				t.Errorf("Update as %s changed the stored settings to %+v", role, store.settings)
			}
		})
	}
}

// TestPasswordSettingsServiceUpdateRefusesAMinimumOutsideTheBand covers the
// S4 bound (8-64) on both sides and past it: the refusal is a ValidationError
// naming the field, and nothing is stored.
func TestPasswordSettingsServiceUpdateRefusesAMinimumOutsideTheBand(t *testing.T) {
	store := newFakePasswordSettingsStore()
	seeded := application.DefaultPasswordSettings()
	seeded.MinLength = 20
	store.settings = seeded
	store.stored = true
	svc := application.NewPasswordSettingsService(store)

	for _, minimum := range []int{0, 7, 65, 1000, -1} {
		err := svc.Update(context.Background(), managerActor,
			application.PasswordSettingsInput{ChangeEnabled: true, RecoveryEnabled: true, MinLength: minimum})
		var verr *domain.ValidationError
		if !errors.As(err, &verr) {
			t.Fatalf("Update(minLength=%d) = %v, want a ValidationError", minimum, err)
		}
		if verr.Field != "password_min_length" {
			t.Errorf("Update(minLength=%d) field = %q, want %q", minimum, verr.Field, "password_min_length")
		}
		if verr.Key != "error.password_min_length" {
			t.Errorf("Update(minLength=%d) key = %q, want %q", minimum, verr.Key, "error.password_min_length")
		}
		if len(verr.Args) != 2 || verr.Args[0] != application.MinPasswordMinLength || verr.Args[1] != application.MaxPasswordMinLength {
			t.Errorf("Update(minLength=%d) args = %v, want [8 64]", minimum, verr.Args)
		}
		if store.setCalls != 0 {
			t.Errorf("Update(minLength=%d) reached the store %d times, want 0", minimum, store.setCalls)
		}
		if store.settings != seeded {
			t.Errorf("Update(minLength=%d) changed the stored settings to %+v", minimum, store.settings)
		}
	}
}

// TestPasswordSettingsServiceUpdatePersistsTheBandEdges proves the accepted
// band is inclusive on both ends and that the switches persist with the
// minimum.
func TestPasswordSettingsServiceUpdatePersistsTheBandEdges(t *testing.T) {
	for _, minimum := range []int{application.MinPasswordMinLength, application.MaxPasswordMinLength} {
		store := newFakePasswordSettingsStore()
		svc := application.NewPasswordSettingsService(store)

		in := application.PasswordSettingsInput{ChangeEnabled: false, RecoveryEnabled: true, MinLength: minimum}
		if err := svc.Update(context.Background(), managerActor, in); err != nil {
			t.Fatalf("Update(minLength=%d): %v", minimum, err)
		}
		if store.setCalls != 1 {
			t.Fatalf("Update(minLength=%d) store calls = %d, want 1", minimum, store.setCalls)
		}
		want := application.PasswordSettings{ChangeEnabled: false, RecoveryEnabled: true, MinLength: minimum}
		if store.settings != want {
			t.Errorf("stored settings = %+v, want %+v", store.settings, want)
		}
	}
}

// TestConfiguredMinimumLowersTheFloor proves the stored minimum is the rule,
// not the default: an instance configured at 8 accepts eight characters and
// still refuses seven, on every write path.
func TestConfiguredMinimumLowersTheFloor(t *testing.T) {
	store := newFakePasswordSettingsStore()
	store.settings = application.PasswordSettings{ChangeEnabled: true, RecoveryEnabled: true, MinLength: application.MinPasswordMinLength}
	store.stored = true
	ctx := context.Background()

	svc, users, _ := newUserService()
	svc.WithPasswordSettings(store)

	if _, err := svc.Create(ctx, managerActor, application.CreateUserInput{
		Name: "Ana", Email: "ana@example.com", Password: "abcdefgh",
	}); err != nil {
		t.Fatalf("Create with eight characters at a configured 8: %v, want accepted", err)
	}
	_, err := svc.Create(ctx, managerActor, application.CreateUserInput{
		Name: "Beto", Email: "beto@example.com", Password: "abcdefg",
	})
	shortPasswordValidation(t, err, application.MinPasswordMinLength)
	if len(users.users) != 1 {
		t.Fatalf("stored users = %d, want 1 (only the accepted one)", len(users.users))
	}
}

// TestNewPasswordPathsRefuseWhenThePolicyCannotBeRead covers the fail-closed
// half of S4: a minimum the store could not answer is never replaced by "no
// rule at all". Every path that writes a hash refuses with the store's own
// error and changes nothing — no user is created and no hash is replaced.
func TestNewPasswordPathsRefuseWhenThePolicyCannotBeRead(t *testing.T) {
	boom := errors.New("settings read failed")
	ctx := context.Background()

	for _, tc := range []struct {
		name string
		run  func(*application.UserService) error
	}{
		{"create", func(svc *application.UserService) error {
			_, err := svc.Create(ctx, managerActor, application.CreateUserInput{
				Name: "Ana", Email: "ana@example.com", Password: "s3cret-pass-12",
			})
			return err
		}},
		{"bootstrap root", func(svc *application.UserService) error {
			_, err := svc.BootstrapRoot(ctx, application.CreateUserInput{
				Name: "Root", Email: "root@example.com", Password: "s3cret-pass-12",
			})
			return err
		}},
		{"change password", func(svc *application.UserService) error {
			return svc.ChangePassword(ctx, managerActor, 1, "s3cret-pass-12")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newFakePasswordSettingsStore()
			store.getErr = boom
			svc, users, _ := newUserService()
			svc.WithPasswordSettings(store)
			member := users.seedRole("Member", "member@example.com", domain.RoleUser, true)
			member.PasswordHash = "old-hash"
			if err := users.Update(ctx, &member); err != nil {
				t.Fatal(err)
			}

			if err := tc.run(svc); !errors.Is(err, boom) {
				t.Fatalf("%s with an unreadable policy = %v, want the store error (fail closed)", tc.name, err)
			}
			if len(users.users) != 1 {
				t.Fatalf("%s with an unreadable policy stored %d users, want only the seeded one", tc.name, len(users.users))
			}
			stored, err := users.GetByID(ctx, member.ID)
			if err != nil {
				t.Fatal(err)
			}
			if stored.PasswordHash != "old-hash" {
				t.Errorf("%s with an unreadable policy changed the stored hash to %q", tc.name, stored.PasswordHash)
			}
		})
	}
}
