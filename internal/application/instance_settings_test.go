package application

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// This fake records service calls only. Transactional consumer guards and
// atomic audit behavior are exercised against SQLite, not simulated here.
type instanceTestStore struct {
	value InstanceAddress
	calls int
	actor int64
	at    time.Time
}

func (f *instanceTestStore) GetInstanceAddress(context.Context) (InstanceAddress, error) {
	return f.value, nil
}

func (f *instanceTestStore) SetInstanceAddress(_ context.Context, actor int64, address string, at time.Time) error {
	f.calls++
	f.actor, f.at, f.value.Address = actor, at, address
	return nil
}

func (f *instanceTestStore) ListInstanceAddressEvents(context.Context) ([]InstanceAddressEvent, error) {
	return nil, nil
}

type instanceTestClock struct{ now time.Time }

func (c instanceTestClock) Now() time.Time { return c.now }

func TestInstanceAddressServiceValidationAndAuthorization(t *testing.T) {
	at := time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC)
	store := &instanceTestStore{value: InstanceAddress{Address: "https://old.example/help", PreviousCompanyAddress: "https://company.example/help"}}
	svc := NewInstanceSettingsService(store, instanceTestClock{at})
	root := domain.User{ID: 1, Role: domain.RoleRoot}
	before := *store
	for _, role := range []domain.Role{domain.RoleAdmin, domain.RoleAgent, domain.RoleUser} {
		actor := domain.User{ID: 2, Role: role}
		var forbidden *domain.ForbiddenError
		if err := svc.Save(t.Context(), actor, "https://other.example"); !errors.As(err, &forbidden) {
			t.Errorf("%s Save = %v", role, err)
		}
		if _, err := svc.Get(t.Context(), actor); !errors.As(err, &forbidden) {
			t.Errorf("%s Get = %v", role, err)
		}
	}
	for _, address := range []string{
		"ftp://tkt.example.com", "https://", "https://u:p@tkt.example.com",
		"https://tkt.example.com/?q=1", "https://tkt.example.com/?", "https://tkt.example.com/#x",
		"https://tkt.example.com/with space", "https://tkt.example.com/\npath",
		"https://tkt.example.com/auth/google/callback", "https://tkt.example.com/help/auth/microsoft/callback/",
		"https://tkt.example.com/%61uth/google/callback",
	} {
		var validation *domain.ValidationError
		if err := svc.Save(t.Context(), root, address); !errors.As(err, &validation) || validation.Field != "instance_address" {
			t.Errorf("Save(%q) = %v", address, err)
		}
	}
	if !reflect.DeepEqual(*store, before) {
		t.Fatalf("rejected input changed state/counters: %+v", store)
	}
	for _, address := range []string{"https://tkt.example.com/help", "http://localhost:3000/help", ""} {
		if err := svc.Save(t.Context(), root, address); err != nil {
			t.Fatal(err)
		}
		got, err := svc.Get(t.Context(), root)
		if err != nil || got.Address != address || got.PreviousCompanyAddress != before.value.PreviousCompanyAddress {
			t.Fatalf("Get = %+v, %v", got, err)
		}
	}
	if store.calls != 3 || store.actor != root.ID || !store.at.Equal(at) {
		t.Fatalf("write provenance = %+v", store)
	}
}

func TestInstanceEmailRejectsDifferentLegacyAddress(t *testing.T) {
	f := newEmailSettingsFixture()
	f.store.stored.BaseURL = "https://canonical.example/help"
	err := f.svc.Save(t.Context(), domain.User{Role: domain.RoleRoot}, EmailSettingsInput{BaseURL: "https://stale.example"})
	var validation *domain.ValidationError
	if !errors.As(err, &validation) || validation.Key != "error.legacy_instance_address" {
		t.Fatalf("Save = %v, want legacy address rejection", err)
	}
	if f.store.setCalls != 0 {
		t.Fatalf("rejected save wrote %d times", f.store.setCalls)
	}
}

func TestInstanceEmailDoesNotWriteAddress(t *testing.T) {
	f := newEmailSettingsFixture()
	f.store.stored.BaseURL = "https://canonical.example/help"
	if err := f.svc.Save(t.Context(), domain.User{Role: domain.RoleRoot}, EmailSettingsInput{BaseURL: "https://canonical.example/help"}); err != nil {
		t.Fatal(err)
	}
	got, err := f.svc.Get(t.Context(), domain.User{Role: domain.RoleRoot})
	if err != nil {
		t.Fatal(err)
	}
	if got.BaseURL != "https://canonical.example/help" {
		t.Fatalf("email save overwrote canonical address: %q", got.BaseURL)
	}
	if f.store.setCalls != 1 {
		t.Errorf("writes=%d, want 1", f.store.setCalls)
	}
}
