package application_test

import (
	"context"
	"errors"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// Organization identity use cases (issue #383, S3): GetOrganization never
// fails for configuration reasons and answers the documented defaults, every
// write is gated on CapManageSettings, validation runs BEFORE the store is
// reached so a rejected submission changes nothing, and one submission is one
// write.

func organizationActor(role domain.Role) domain.User {
	return domain.User{ID: 42, Name: "Actor", Email: "actor@tkt.test", Role: role, Active: true}
}

func newOrganizationServiceFixture(t *testing.T) (*application.OrganizationService, *fakeOrganizationStore) {
	t.Helper()
	store := newFakeOrganizationStore()
	return application.NewOrganizationService(store), store
}

func TestOrganizationDefaultsWhenNothingWasEverSaved(t *testing.T) {
	svc, _ := newOrganizationServiceFixture(t)

	got, err := svc.GetOrganization(context.Background())
	if err != nil {
		t.Fatalf("GetOrganization: %v", err)
	}
	want := application.Organization{Name: domain.DefaultOrganizationName, HelpDesk: domain.DefaultOrganizationHelpDesk}
	if got.Name != want.Name || got.HelpDesk != want.HelpDesk {
		t.Errorf("organization = %+v, want the documented defaults %+v", got, want)
	}
	if got.HasLogo() {
		t.Error("a fresh instance must not report a logo")
	}
	if monogram := got.Monogram(); monogram != domain.Monogram(domain.DefaultOrganizationName) {
		t.Errorf("monogram = %q, want the name's monogram", monogram)
	}
}

func TestOrganizationUpdatePersistsEveryTextField(t *testing.T) {
	svc, store := newOrganizationServiceFixture(t)

	err := svc.UpdateOrganization(context.Background(), organizationActor(domain.RoleAdmin), application.OrganizationInput{
		Name:         "  Acme Support ",
		HelpDesk:     " Service desk ",
		SupportEmail: " support@acme.test ",
	})
	if err != nil {
		t.Fatalf("UpdateOrganization: %v", err)
	}
	if store.setCalls != 1 {
		t.Fatalf("store writes = %d, want exactly one write for one submission", store.setCalls)
	}
	if store.org.Name != "Acme Support" || store.org.HelpDesk != "Service desk" || store.org.SupportEmail != "support@acme.test" {
		t.Errorf("stored = %+v, want the trimmed values", store.org)
	}
}

func TestOrganizationUpdateKeepsTheStoredLogoWhenNoneIsSubmitted(t *testing.T) {
	svc, store := newOrganizationServiceFixture(t)
	logo := organizationLogoBytes()
	if err := svc.SetLogo(context.Background(), organizationActor(domain.RoleRoot), logo); err != nil {
		t.Fatalf("SetLogo: %v", err)
	}

	if err := svc.UpdateOrganization(context.Background(), organizationActor(domain.RoleAdmin), application.OrganizationInput{Name: "Acme"}); err != nil {
		t.Fatalf("UpdateOrganization: %v", err)
	}
	if !store.org.HasLogo() {
		t.Error("a text-only save must keep the stored logo")
	}
}

func TestOrganizationUpdateWritesTextAndLogoInOneSubmission(t *testing.T) {
	svc, store := newOrganizationServiceFixture(t)
	logo := organizationLogoBytes()

	if err := svc.UpdateOrganization(context.Background(), organizationActor(domain.RoleAdmin), application.OrganizationInput{Name: "Acme", Logo: logo}); err != nil {
		t.Fatalf("UpdateOrganization: %v", err)
	}
	if store.setCalls != 1 {
		t.Errorf("store writes = %d, want one write carrying both the text and the logo", store.setCalls)
	}
	if !store.org.HasLogo() || store.org.Name != "Acme" {
		t.Errorf("stored = %+v, want the name and the logo from one submission", store.org)
	}
}

func TestOrganizationUpdateRejectionsStoreNothing(t *testing.T) {
	cases := []struct {
		name string
		in   application.OrganizationInput
		key  string
	}{
		{"empty name", application.OrganizationInput{Name: "   "}, "error.org_name_required"},
		{"invalid email", application.OrganizationInput{Name: "Acme", SupportEmail: "nope"}, "error.org_support_email_invalid"},
		{"non-png logo", application.OrganizationInput{Name: "Acme", Logo: []byte("not a png")}, "error.org_logo_invalid"},
		{"oversize logo", application.OrganizationInput{Name: "Acme", Logo: oversizeOrganizationLogo()}, "error.org_logo_too_large"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, store := newOrganizationServiceFixture(t)
			err := svc.UpdateOrganization(context.Background(), organizationActor(domain.RoleAdmin), tc.in)
			var validation *domain.ValidationError
			if !errors.As(err, &validation) {
				t.Fatalf("err = %v, want a ValidationError", err)
			}
			if validation.Key != tc.key {
				t.Errorf("key = %q, want %q", validation.Key, tc.key)
			}
			if store.setCalls != 0 {
				t.Errorf("store writes = %d, want zero: a rejected submission changes nothing", store.setCalls)
			}
		})
	}
}

func TestOrganizationLogoUseCases(t *testing.T) {
	svc, store := newOrganizationServiceFixture(t)
	logo := organizationLogoBytes()

	if err := svc.SetLogo(context.Background(), organizationActor(domain.RoleAdmin), logo); err != nil {
		t.Fatalf("SetLogo: %v", err)
	}
	if !store.org.HasLogo() {
		t.Fatal("SetLogo must store the logo")
	}
	if err := svc.SetLogo(context.Background(), organizationActor(domain.RoleAdmin), []byte("nope")); err == nil {
		t.Fatal("SetLogo must refuse bytes that are not a PNG")
	}
	if got, err := svc.GetOrganization(context.Background()); err != nil || !got.HasLogo() {
		t.Errorf("a rejected SetLogo must change nothing, got %+v (%v)", got, err)
	}

	if err := svc.RemoveLogo(context.Background(), organizationActor(domain.RoleAdmin)); err != nil {
		t.Fatalf("RemoveLogo: %v", err)
	}
	if got, err := svc.GetOrganization(context.Background()); err != nil || got.HasLogo() {
		t.Errorf("RemoveLogo must drop the stored logo, got %+v (%v)", got, err)
	}
	// Removing an absent logo is not an error: the outcome the caller asked
	// for is the state it finds.
	if err := svc.RemoveLogo(context.Background(), organizationActor(domain.RoleAdmin)); err != nil {
		t.Errorf("RemoveLogo on a logo-less instance = %v, want nil", err)
	}
}

func TestOrganizationWritesAreGatedOnCapManageSettings(t *testing.T) {
	cases := []struct {
		name   string
		actor  domain.User
		permit bool
	}{
		{"admin", organizationActor(domain.RoleAdmin), true},
		{"root", organizationActor(domain.RoleRoot), true},
		{"agent", organizationActor(domain.RoleAgent), false},
		{"user", organizationActor(domain.RoleUser), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, store := newOrganizationServiceFixture(t)
			logo := organizationLogoBytes()
			for _, write := range []func() error{
				func() error {
					return svc.UpdateOrganization(context.Background(), tc.actor, application.OrganizationInput{Name: "Acme"})
				},
				func() error { return svc.SetLogo(context.Background(), tc.actor, logo) },
				func() error { return svc.RemoveLogo(context.Background(), tc.actor) },
			} {
				err := write()
				if tc.permit {
					if err != nil {
						t.Fatalf("permitted actor refused: %v", err)
					}
					continue
				}
				var forbidden *domain.ForbiddenError
				if !errors.As(err, &forbidden) {
					t.Fatalf("err = %v, want a ForbiddenError", err)
				}
			}
			if !tc.permit && store.setCalls != 0 {
				t.Errorf("store writes = %d, want zero for a denied actor", store.setCalls)
			}
		})
	}
}

// TestOrganizationReadIsNotGated proves the read the request middleware
// performs: the brand is part of the shell every authenticated page renders,
// so GetOrganization answers for any actor and reads even when the caller is
// the shell rather than an administrator.
func TestOrganizationReadIsNotGated(t *testing.T) {
	svc, store := newOrganizationServiceFixture(t)
	store.org = application.Organization{Name: "Acme", HelpDesk: "Service desk"}

	got, err := svc.GetOrganization(context.Background())
	if err != nil {
		t.Fatalf("GetOrganization: %v", err)
	}
	if got.Name != "Acme" {
		t.Errorf("name = %q, want the stored name", got.Name)
	}
}

func TestOrganizationStoreFailuresPropagate(t *testing.T) {
	svc, store := newOrganizationServiceFixture(t)
	boom := errors.New("store down")
	store.getErr = boom
	if _, err := svc.GetOrganization(context.Background()); !errors.Is(err, boom) {
		t.Errorf("GetOrganization err = %v, want the store failure", err)
	}
	store.getErr = nil
	store.setErr = boom
	if err := svc.UpdateOrganization(context.Background(), organizationActor(domain.RoleAdmin), application.OrganizationInput{Name: "Acme"}); !errors.Is(err, boom) {
		t.Errorf("UpdateOrganization err = %v, want the store failure", err)
	}
}

// organizationLogoBytes is a valid PNG logo fixture: the magic bytes plus
// filler, so validation proves the magic and nothing depends on real pixels.
func organizationLogoBytes() []byte {
	return append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 32)...)
}

func oversizeOrganizationLogo() []byte {
	return append(organizationLogoBytes(), make([]byte, domain.MaxOrganizationLogoBytes)...)
}
