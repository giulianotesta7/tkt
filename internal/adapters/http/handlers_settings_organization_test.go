package httpadapter

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// The Organization section (issue #383, S3/S4): the identity form is multipart
// because the name, the help desk name, the support email and an optional logo
// are one submission. Every rejection stores nothing, an unsubmitted file keeps
// the stored logo, and the remove route is its own endpoint.

// organizationLogoPNG is a valid logo: the PNG magic bytes plus filler, so the
// magic check is what accepts it and no test depends on real pixels.
var organizationLogoPNG = append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 48)...)

// postOrganization submits the identity form as a multipart request, the shape
// a browser sends for this form. logo nil means no file part at all, which is
// what an untouched file input sends.
func postOrganization(t *testing.T, h *harness, fields url.Values, logo []byte) *httptest.ResponseRecorder {
	t.Helper()
	return postOrganizationAs(t, h, fields, logo, h.adminSession.ID)
}

func postOrganizationAs(t *testing.T, h *harness, fields url.Values, logo []byte, sessionID string) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	for name, values := range fields {
		for _, value := range values {
			if err := w.WriteField(name, value); err != nil {
				t.Fatalf("write multipart field %q: %v", name, err)
			}
		}
	}
	if logo != nil {
		part, err := w.CreateFormFile(organizationLogoField, "logo.png")
		if err != nil {
			t.Fatalf("create logo part: %v", err)
		}
		if _, err := part.Write(logo); err != nil {
			t.Fatalf("write logo part: %v", err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/settings/organization", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Cookie", sessionCookie+"="+sessionID)
	rec := httptest.NewRecorder()
	h.mw.Wrap(h.mux).ServeHTTP(rec, req)
	return rec
}

// storedOrganization reads the identity through the store port.
func storedOrganization(t *testing.T, h *harness) domain.Organization {
	t.Helper()
	org, err := h.store.OrganizationStore().GetOrganization(context.Background())
	if err != nil {
		t.Fatalf("read stored organization: %v", err)
	}
	return org
}

func organizationFields(name, helpDesk, supportEmail string) url.Values {
	return url.Values{"name": {name}, "help_desk": {helpDesk}, "support_email": {supportEmail}}
}

// TestOrganizationSectionShowsTheStoredIdentity proves the section renders what
// is stored, with the monogram standing in for an absent logo.
func TestOrganizationSectionShowsTheStoredIdentity(t *testing.T) {
	h := newHarness(t)
	setOrganization(t, h, domain.Organization{Name: "Acme Support", HelpDesk: "Service desk", SupportEmail: "support@acme.test"})

	body := sectionGet(t, h, "/settings/organization")
	for _, want := range []string{
		`value="Acme Support"`,
		`id="org_help_desk" name="help_desk" value="Service desk"`,
		`id="org_support_email" name="support_email" value="support@acme.test"`,
		`<span class="settings-logo-mark">AS</span>`,
		`type="file" id="org_logo" name="logo" accept="image/png"`,
		`256 × 256 px recommended (128 × 128 px at least). PNG, up to 256 KiB.`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the organization section must contain %q, got: %s", want, body)
		}
	}
	if strings.Contains(body, "Remove logo") {
		t.Errorf("an instance with no logo must not offer to remove one, got: %s", body)
	}
}

// TestOrganizationSavePersistsTheIdentityAndTheLogo proves the happy path: one
// multipart submit stores the three text fields and the logo, redirects back to
// the section, and the following GET renders them.
func TestOrganizationSavePersistsTheIdentityAndTheLogo(t *testing.T) {
	h := newHarness(t)

	rec := postOrganization(t, h, organizationFields(" Acme Support ", " Service desk ", " support@acme.test "), organizationLogoPNG)

	wantRedirect(t, rec, http.StatusSeeOther, "/settings/organization")

	stored := storedOrganization(t, h)
	if stored.Name != "Acme Support" || stored.HelpDesk != "Service desk" || stored.SupportEmail != "support@acme.test" {
		t.Errorf("stored identity = %+v, want the trimmed values", stored)
	}
	if !stored.HasLogo() || stored.LogoMIME != domain.OrganizationLogoContentType {
		t.Errorf("stored logo = (%d bytes, %q), want the submitted PNG", len(stored.Logo), stored.LogoMIME)
	}

	body := sectionGet(t, h, "/settings/organization")
	for _, want := range []string{
		`<span class="settings-logo-mark"><img src="/brand/logo?v=`,
		`Remove logo`,
		`class="brand-name">Acme Support<`,
		`class="brand-product">Service desk<`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the section after a save must contain %q, got: %s", want, body)
		}
	}
}

// TestOrganizationSaveKeepsTheLogoWhenNoFileIsSubmitted proves the documented
// default of the file field: editing the name does not cost the instance its
// mark.
func TestOrganizationSaveKeepsTheLogoWhenNoFileIsSubmitted(t *testing.T) {
	h := newHarness(t)
	setOrganization(t, h, domain.Organization{Name: "Acme", HelpDesk: "Help desk", Logo: organizationLogoPNG, LogoMIME: domain.OrganizationLogoContentType})

	rec := postOrganization(t, h, organizationFields("Acme Support", "Service desk", ""), nil)
	wantRedirect(t, rec, http.StatusSeeOther, "/settings/organization")

	stored := storedOrganization(t, h)
	if stored.Name != "Acme Support" {
		t.Errorf("name = %q, want the submitted name", stored.Name)
	}
	if !stored.HasLogo() {
		t.Error("a text-only save must keep the stored logo")
	}
}

// TestOrganizationSaveRejectionsStoreNothing proves each documented refusal,
// its status, its catalogue message, and that nothing was written: not the
// text fields, not the logo.
func TestOrganizationSaveRejectionsStoreNothing(t *testing.T) {
	cases := []struct {
		name      string
		fields    url.Values
		logo      []byte
		wantCode  int
		wantError string
	}{
		{
			name:      "empty name",
			fields:    organizationFields("   ", "Service desk", ""),
			wantCode:  http.StatusUnprocessableEntity,
			wantError: "enter the organization name",
		},
		{
			name:      "invalid support email",
			fields:    organizationFields("Acme", "Service desk", "not-an-address"),
			wantCode:  http.StatusUnprocessableEntity,
			wantError: "the support email must be a single email address, like support@example.com",
		},
		{
			name:      "a text file named png",
			fields:    organizationFields("Acme", "Service desk", ""),
			logo:      []byte("this is not a png"),
			wantCode:  http.StatusUnprocessableEntity,
			wantError: "the logo must be a PNG image",
		},
		{
			name:      "an oversized logo",
			fields:    organizationFields("Acme", "Service desk", ""),
			logo:      append(append([]byte{}, organizationLogoPNG...), make([]byte, domain.MaxOrganizationLogoBytes)...),
			wantCode:  http.StatusRequestEntityTooLarge,
			wantError: "the logo must be 256 KiB or smaller",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			seeded := domain.Organization{Name: "Before", HelpDesk: "Old desk", SupportEmail: "before@acme.test", Logo: organizationLogoPNG, LogoMIME: domain.OrganizationLogoContentType}
			setOrganization(t, h, seeded)

			rec := postOrganization(t, h, tc.fields, tc.logo)

			if rec.Code != tc.wantCode {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.wantCode, rec.Body.String())
			}
			body := rec.Body.String()
			if !strings.Contains(body, `class="error-banner" role="alert"`) {
				t.Errorf("a rejection must render the error banner, got: %s", body)
			}
			if !strings.Contains(body, tc.wantError) {
				t.Errorf("the rejection must read %q, got: %s", tc.wantError, body)
			}
			// The rejection re-renders the section it belongs to, with the
			// submitted text so one bad field does not cost the others.
			if !strings.Contains(body, `action="/settings/organization"`) {
				t.Errorf("a rejection must re-render the organization section, got: %s", body)
			}
			// Nothing stored: the seeded identity is intact, logo included.
			if got := storedOrganization(t, h); got.Name != seeded.Name || got.HelpDesk != seeded.HelpDesk ||
				got.SupportEmail != seeded.SupportEmail || !got.HasLogo() {
				t.Errorf("stored identity after a rejection = %+v, want the seeded one untouched", got)
			}
			if got := rec.Header().Get("X-Save-Feedback"); got != "" {
				t.Errorf("a rejection must not emit save feedback, got %q", got)
			}
		})
	}
}

// TestOrganizationRejectionEchoesTheSubmittedText proves a rejected save keeps
// what the administrator typed, so a bad email does not cost the name.
func TestOrganizationRejectionEchoesTheSubmittedText(t *testing.T) {
	h := newHarness(t)

	rec := postOrganization(t, h, organizationFields("Acme Support", "Service desk", "broken"), nil)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`value="Acme Support"`,
		`value="Service desk"`,
		`value="broken"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the rejection must echo %q, got: %s", want, body)
		}
	}
}

// TestOrganizationLogoRemovalIsItsOwnEndpoint proves the remove route: it needs
// no other field, redirects back to the section, and leaves the name alone.
func TestOrganizationLogoRemovalIsItsOwnEndpoint(t *testing.T) {
	h := newHarness(t)
	setOrganization(t, h, domain.Organization{Name: "Acme", HelpDesk: "Service desk", Logo: organizationLogoPNG, LogoMIME: domain.OrganizationLogoContentType})

	rec := h.postForm(t, "/settings/organization/logo/remove", url.Values{}, false)

	wantRedirect(t, rec, http.StatusSeeOther, "/settings/organization")

	stored := storedOrganization(t, h)
	if stored.HasLogo() {
		t.Errorf("the logo survived the remove route: %d bytes", len(stored.Logo))
	}
	if stored.Name != "Acme" || stored.HelpDesk != "Service desk" {
		t.Errorf("the remove route changed the identity: %+v", stored)
	}
	body := sectionGet(t, h, "/settings/organization")
	if strings.Contains(body, "Remove logo") {
		t.Errorf("the section must stop offering a removal once the logo is gone, got: %s", body)
	}
	if !strings.Contains(body, `<span class="settings-logo-mark">A</span>`) {
		t.Errorf("the section must fall back to the monogram, got: %s", body)
	}
}

// TestOrganizationLogoRouteServesTheSavedLogo proves the identity and the logo
// route agree: the bytes a save stored are the bytes the shell's image URL
// answers with.
func TestOrganizationLogoRouteServesTheSavedLogo(t *testing.T) {
	h := newHarness(t)

	rec := postOrganization(t, h, organizationFields("Acme", "Service desk", ""), organizationLogoPNG)
	wantRedirect(t, rec, http.StatusSeeOther, "/settings/organization")

	logo := h.get(t, "/brand/logo", false)
	if logo.Code != http.StatusOK {
		t.Fatalf("GET /brand/logo after a save = %d, want 200", logo.Code)
	}
	if !bytes.Equal(logo.Body.Bytes(), organizationLogoPNG) {
		t.Errorf("served logo is not the saved one")
	}
}

// TestOrganizationSaveIsGatedForNonAdmins proves the form keeps the capability
// every other settings route has: an agent and a requester are refused and
// store nothing, even though they are authenticated.
func TestOrganizationSaveIsGatedForNonAdmins(t *testing.T) {
	for _, role := range []domain.Role{domain.RoleAgent, domain.RoleUser} {
		t.Run(string(role), func(t *testing.T) {
			h := newHarness(t)
			actor := seedUserRole(t, h.store, string(role), string(role)+"-org@tkt.test", role)
			session := seedSession(t, h.store, actor.ID)
			seeded := domain.Organization{Name: "Before", HelpDesk: "Old desk"}
			setOrganization(t, h, seeded)

			rec := postOrganizationAs(t, h, organizationFields("After", "New desk", ""), organizationLogoPNG, session.ID)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("POST /settings/organization as %s = %d, want 403", role, rec.Code)
			}
			if got := storedOrganization(t, h); got.Name != seeded.Name || got.HasLogo() {
				t.Errorf("a denied save stored %+v, want the seeded identity untouched", got)
			}

			// The remove route is gated the same way.
			remove := h.postFormAs(t, "/settings/organization/logo/remove", url.Values{}, session.ID)
			if remove.Code != http.StatusForbidden {
				t.Errorf("POST /settings/organization/logo/remove as %s = %d, want 403", role, remove.Code)
			}
		})
	}
}

// TestOrganizationSaveRequiresNoDescriptiveCopy proves S8 on this section: the
// form states the rule the user cannot infer (the PNG cap) and nothing else.
func TestOrganizationSaveRequiresNoDescriptiveCopy(t *testing.T) {
	h := newHarness(t)

	body := sectionGet(t, h, "/settings/organization")
	if strings.Contains(body, "This is the name") || strings.Contains(body, "shown across") {
		t.Errorf("the organization section must not explain the obvious, got: %s", body)
	}
	for _, rule := range []string{"256 × 256 px recommended", "PNG, up to 256 KiB."} {
		if !strings.Contains(body, rule) {
			t.Errorf("the logo rule %q is not inferable and must stay, got: %s", rule, body)
		}
	}
}
