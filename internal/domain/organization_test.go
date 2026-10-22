package domain_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// Organization identity rules (issue #383, S3): the name is required, trimmed
// and 1-60 characters; the help desk name is trimmed, 0-40 characters and an
// empty one means the documented default; the support email is optional and
// must be one address the mail parser accepts; the logo is a PNG of at most
// 256 KiB proven by its magic bytes, never by the bytes' name.

// organizationPNG is a logo whose magic bytes prove it is a PNG.
var organizationPNG = append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 64)...)

func TestNormalizeOrganizationTrimsEveryField(t *testing.T) {
	got, err := domain.NormalizeOrganization(domain.OrganizationInput{
		Name:         "  Acme Support  ",
		HelpDesk:     "  Service desk  ",
		SupportEmail: "  support@acme.test  ",
	})
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if got.Name != "Acme Support" || got.HelpDesk != "Service desk" || got.SupportEmail != "support@acme.test" {
		t.Errorf("normalized = %+v, want every field trimmed", got)
	}
}

func TestNormalizeOrganizationAnswersTheHelpDeskDefault(t *testing.T) {
	got, err := domain.NormalizeOrganization(domain.OrganizationInput{Name: "Acme", HelpDesk: "   "})
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if got.HelpDesk != domain.DefaultOrganizationHelpDesk {
		t.Errorf("help desk = %q, want the default %q", got.HelpDesk, domain.DefaultOrganizationHelpDesk)
	}
	if got.SupportEmail != "" {
		t.Errorf("support email = %q, want it empty when none was submitted", got.SupportEmail)
	}
}

func TestNormalizeOrganizationRejectsInvalidSubmissions(t *testing.T) {
	longName := strings.Repeat("a", domain.MaxOrganizationNameRunes+1)
	longHelpDesk := strings.Repeat("b", domain.MaxOrganizationHelpDeskRunes+1)
	cases := []struct {
		name  string
		in    domain.OrganizationInput
		field string
		key   string
	}{
		{"empty name", domain.OrganizationInput{Name: "   "}, "name", "error.org_name_required"},
		{"name too long", domain.OrganizationInput{Name: longName}, "name", "error.org_name_invalid"},
		{"name with a line break", domain.OrganizationInput{Name: "Acme\nSupport"}, "name", "error.org_name_invalid"},
		{"help desk too long", domain.OrganizationInput{Name: "Acme", HelpDesk: longHelpDesk}, "help_desk", "error.org_helpdesk_name_invalid"},
		{"help desk with a control character", domain.OrganizationInput{Name: "Acme", HelpDesk: "Desk\x07"}, "help_desk", "error.org_helpdesk_name_invalid"},
		{"email without an at sign", domain.OrganizationInput{Name: "Acme", SupportEmail: "support"}, "support_email", "error.org_support_email_invalid"},
		{"email with a display name", domain.OrganizationInput{Name: "Acme", SupportEmail: "Support <support@acme.test>"}, "support_email", "error.org_support_email_invalid"},
		{"email with two addresses", domain.OrganizationInput{Name: "Acme", SupportEmail: "a@acme.test, b@acme.test"}, "support_email", "error.org_support_email_invalid"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := domain.NormalizeOrganization(tc.in)
			var validation *domain.ValidationError
			if !errors.As(err, &validation) {
				t.Fatalf("err = %v, want a ValidationError", err)
			}
			if validation.Field != tc.field {
				t.Errorf("field = %q, want %q", validation.Field, tc.field)
			}
			if validation.Key != tc.key {
				t.Errorf("key = %q, want %q", validation.Key, tc.key)
			}
		})
	}
}

// TestNormalizeOrganizationAcceptsTheBoundaries proves the caps are inclusive:
// a name of exactly 60 characters and a help desk name of exactly 40 are
// accepted, so the limit a form states is the limit the service enforces.
func TestNormalizeOrganizationAcceptsTheBoundaries(t *testing.T) {
	got, err := domain.NormalizeOrganization(domain.OrganizationInput{
		Name:     strings.Repeat("ñ", domain.MaxOrganizationNameRunes),
		HelpDesk: strings.Repeat("á", domain.MaxOrganizationHelpDeskRunes),
	})
	if err != nil {
		t.Fatalf("boundary submission rejected: %v", err)
	}
	if len([]rune(got.Name)) != domain.MaxOrganizationNameRunes {
		t.Errorf("name length = %d runes, want %d", len([]rune(got.Name)), domain.MaxOrganizationNameRunes)
	}
}

func TestMonogramTakesTheFirstLettersOfUpToTwoWords(t *testing.T) {
	cases := map[string]string{
		"tkt":                "T",
		"Acme Support":       "AS",
		"Acme Support Desk":  "AS",
		"  acme   support  ": "AS",
		"":                   "",
		"   ":                "",
		"Ñandú Servicios":    "ÑS",
		"Ácme":               "Á",
		"1st Line":           "1L",
	}
	for name, want := range cases {
		if got := domain.Monogram(name); got != want {
			t.Errorf("Monogram(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestValidateOrganizationLogoAcceptsAPNG(t *testing.T) {
	if err := domain.ValidateOrganizationLogo(organizationPNG); err != nil {
		t.Fatalf("valid PNG rejected: %v", err)
	}
	// Exactly the cap is still a valid logo: the limit is inclusive.
	atCap := append(append([]byte{}, organizationPNG...), make([]byte, domain.MaxOrganizationLogoBytes-len(organizationPNG))...)
	if err := domain.ValidateOrganizationLogo(atCap); err != nil {
		t.Fatalf("logo of exactly %d bytes rejected: %v", domain.MaxOrganizationLogoBytes, err)
	}
}

func TestValidateOrganizationLogoRejectsWhatItCannotProve(t *testing.T) {
	oversize := append(append([]byte{}, organizationPNG...), make([]byte, domain.MaxOrganizationLogoBytes)...)
	cases := []struct {
		name  string
		data  []byte
		key   string
		field string
	}{
		{"empty", nil, "error.org_logo_invalid", "logo"},
		{"not a png", []byte("this is a text file called logo.png"), "error.org_logo_invalid", "logo"},
		{"a jpeg", append([]byte("\xFF\xD8\xFF\xE0"), make([]byte, 16)...), "error.org_logo_invalid", "logo"},
		{"oversize", oversize, "error.org_logo_too_large", "logo"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := domain.ValidateOrganizationLogo(tc.data)
			var validation *domain.ValidationError
			if !errors.As(err, &validation) {
				t.Fatalf("err = %v, want a ValidationError", err)
			}
			if validation.Key != tc.key {
				t.Errorf("key = %q, want %q", validation.Key, tc.key)
			}
			if validation.Field != tc.field {
				t.Errorf("field = %q, want %q", validation.Field, tc.field)
			}
		})
	}
}
