package httpadapter

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// The Security settings section (issue #414, specs S1, S4, S5, S10, S11): one
// section at /settings/security holding the password policy the sign-in and
// Preferences paths read — the two instance switches and the minimum length
// new passwords must meet. The section is gated on CapManageSettings like every
// settings section but the root-only Email one, it saves the three values in one
// submission, and while email delivery is not configured it says so, because
// the recovery switch has nothing to send with (S5).
//
// The password rules themselves (runes counted, admin-set and setup paths)
// belong to the application and sqlite layers; this file proves the surface.

// securityForm is one complete submission of the section; mod may change or
// drop any field to build a rejected or partial one.
func securityForm(mod func(url.Values)) url.Values {
	form := url.Values{
		"password_change_enabled":   {"1"},
		"password_recovery_enabled": {"1"},
		"password_min_length":       {"14"},
	}
	if mod != nil {
		mod(form)
	}
	return form
}

// storedPasswordSettings reads the policy through its own port: a test never
// asserts persistence against a rendered page.
func storedPasswordSettings(t *testing.T, h *harness) application.PasswordSettings {
	t.Helper()
	got, err := h.passwords.Get(t.Context())
	if err != nil {
		t.Fatalf("read password settings: %v", err)
	}
	return got
}

// wantSecurityCheckedSwitch asserts one switch's rendered state. The markup is
// asserted exactly, so a switch that silently loses its checked attribute (or
// its name) fails here instead of passing on a substring of the label.
func wantSecurityCheckedSwitch(t *testing.T, body, name string, checked bool) {
	t.Helper()
	want := `<input type="checkbox" id="` + name + `" name="` + name + `" value="1" aria-labelledby="`
	if !strings.Contains(body, want) {
		t.Fatalf("the section must render the %s switch, got: %.1200s", name, body)
	}
	field := body[strings.Index(body, want):]
	field = field[:strings.Index(field, ">")+1]
	if got := strings.Contains(field, " checked"); got != checked {
		t.Errorf("%s rendered checked = %v, want %v (%s)", name, got, checked, field)
	}
}

// TestSettingsSecurityRendersTheStoredPolicy proves the read half for both
// actors who may open the section (admin and root): a fresh instance renders the
// documented defaults — both switches ON and a minimum of 12 — inside the
// Security section, whose password card is one card of the section so later
// cards can join it.
func TestSettingsSecurityRendersTheStoredPolicy(t *testing.T) {
	h := newHarness(t)
	_, rootSession := seedRoot(t, h)
	admin := seedUserRole(t, h.store, "Security Admin", "security-admin@tkt.test", domain.RoleAdmin)
	adminSession := seedSession(t, h.store, admin.ID)

	for _, tc := range []struct {
		name    string
		session string
	}{
		{name: "admin", session: adminSession.ID},
		{name: "root", session: rootSession.ID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := getAs(t, h, "/settings/security", tc.session)
			if rec.Code != http.StatusOK {
				t.Fatalf("GET /settings/security = %d, want 200: %s", rec.Code, rec.Body.String())
			}
			body := rec.Body.String()
			for _, want := range []string{
				`<section class="settings-group">`,
				`<h2 class="settings-section-title">Passwords</h2>`,
				`<h2 class="settings-section-title">Company sign-in</h2>`,
				`aria-label="Minimum password length"`,
				`action="/settings/security"`,
				"Allow people to change their own password from Preferences",
				"Offer “Forgot your password?” on the sign-in page",
				`id="password_min_length" name="password_min_length" type="number" min="8" max="64" step="1" value="12"`,
				"Between 8 and 64 characters.",
				"Save password settings",
				"Discard",
			} {
				if !strings.Contains(body, want) {
					t.Errorf("GET /settings/security must contain %q, got: %.1500s", want, body)
				}
			}
			// Three instance switches sit in the Passwords card — password sign-in
			// (issue #419), self-service change and recovery. Each provider card
			// heads itself with a switch row carrying its own modifier.
			if got := strings.Count(body, `class="switch-line"`); got != 3 {
				t.Errorf("the section renders %d instance switch lines, want one per instance switch (3)", got)
			}
			if got := strings.Count(body, `class="switch-row settings-provider-head"`); got != 2 {
				t.Errorf("the section renders %d provider enable rows, want one per provider (2)", got)
			}
			wantSecurityCheckedSwitch(t, body, "password_change_enabled", true)
			wantSecurityCheckedSwitch(t, body, "password_recovery_enabled", true)
			// Issue #419: the password sign-in switch belongs to the company
			// sign-in configuration, so it carries that form's association.
			if want := `<input type="checkbox" id="password_signin_enabled" name="password_signin_enabled" value="1" form="company-signin-form" aria-labelledby="password-signin-label" checked>`; !strings.Contains(body, want) {
				t.Errorf("the Passwords card must render the password sign-in switch, got: %.1500s", body)
			}
			if !strings.Contains(body, `class="settings-savebar" data-settings-savebar hidden`) {
				t.Errorf("the section must carry the shared save bar, got: %.1500s", body)
			}
			assertNoWhitespaceViolations(t, body)
		})
	}
}

// TestSettingsSecurityIsGatedOnCapManageSettings proves the gate on both verbs for
// all four roles: the two that hold CapManageSettings open the section and can
// save it, and the two that do not may neither read nor write the instance
// policy — their refusal stores nothing. Each role gets its own instance, so one
// role's save cannot be what another role's assertion sees.
func TestSettingsSecurityIsGatedOnCapManageSettings(t *testing.T) {
	for _, tc := range []struct {
		role   domain.Role
		canMan bool
	}{
		{role: domain.RoleRoot, canMan: true},
		{role: domain.RoleAdmin, canMan: true},
		{role: domain.RoleAgent, canMan: false},
		{role: domain.RoleUser, canMan: false},
	} {
		t.Run(string(tc.role), func(t *testing.T) {
			h := newHarness(t)
			actor := seedUserRole(t, h.store, "Security "+string(tc.role), "security-"+string(tc.role)+"@tkt.test", tc.role)
			session := seedSession(t, h.store, actor.ID)

			get := getAs(t, h, "/settings/security", session.ID)
			rec := h.postFormAs(t, "/settings/security", securityForm(func(form url.Values) {
				form.Del("password_change_enabled")
			}), session.ID)

			if !tc.canMan {
				if get.Code != http.StatusForbidden {
					t.Errorf("%s GET /settings/security = %d, want 403", tc.role, get.Code)
				}
				if rec.Code != http.StatusForbidden {
					t.Errorf("%s POST /settings/security = %d, want 403", tc.role, rec.Code)
				}
				if got, want := storedPasswordSettings(t, h), application.DefaultPasswordSettings(); got != want {
					t.Errorf("%s refused write changed the policy: %+v, want %+v", tc.role, got, want)
				}
				return
			}

			if get.Code != http.StatusOK {
				t.Fatalf("%s GET /settings/security = %d, want 200: %.600s", tc.role, get.Code, get.Body.String())
			}
			wantRedirect(t, rec, http.StatusSeeOther, "/settings/security")
			want := application.PasswordSettings{ChangeEnabled: false, RecoveryEnabled: true, MinLength: 14}
			if got := storedPasswordSettings(t, h); got != want {
				t.Errorf("%s saved write stored %+v, want %+v", tc.role, got, want)
			}
		})
	}
}

// TestSettingsSecuritySavesThePolicy proves the write half: one submission
// stores both switches and the minimum, and it redirects 303 back to the section
// with the shared save feedback.
func TestSettingsSecuritySavesThePolicy(t *testing.T) {
	h := newHarness(t)

	rec := h.postForm(t, "/settings/security", securityForm(nil), false)

	wantRedirect(t, rec, http.StatusSeeOther, "/settings/security")
	if got, want := storedPasswordSettings(t, h), (application.PasswordSettings{ChangeEnabled: true, RecoveryEnabled: true, MinLength: 14}); got != want {
		t.Fatalf("stored policy = %+v, want %+v", got, want)
	}

	// The saved values are what the section renders on the next visit.
	body := sectionGet(t, h, "/settings/security")
	if !strings.Contains(body, `value="14"`) {
		t.Errorf("the saved minimum must render back, got: %.1500s", body)
	}

	// Both switches turn off by being ABSENT: a checkbox the form did not submit
	// is OFF, never "unchanged".
	rec = h.postForm(t, "/settings/security", securityForm(func(form url.Values) {
		form.Del("password_change_enabled")
		form.Del("password_recovery_enabled")
		form.Set("password_min_length", "20")
	}), false)
	wantRedirect(t, rec, http.StatusSeeOther, "/settings/security")
	if got, want := storedPasswordSettings(t, h), (application.PasswordSettings{ChangeEnabled: false, RecoveryEnabled: false, MinLength: 20}); got != want {
		t.Fatalf("stored policy after absent switches = %+v, want %+v", got, want)
	}
	body = sectionGet(t, h, "/settings/security")
	wantSecurityCheckedSwitch(t, body, "password_change_enabled", false)
	wantSecurityCheckedSwitch(t, body, "password_recovery_enabled", false)
}

// TestSettingsSecurityRefusesAMinimumOutsideTheBand proves S4's band: a minimum
// below 8 or above 64, an empty one, whitespace only, and a value that is not a
// whole number at all are each refused with the catalogued sentence (422), the
// submitted values come back so the administrator does not retype them, and
// nothing is stored. Every refusal is the same one, so the form never explains
// which parse failed.
func TestSettingsSecurityRefusesAMinimumOutsideTheBand(t *testing.T) {
	h := newHarness(t)

	for _, minimum := range []string{"7", "65", "", " ", "twelve", "8.5", "13x"} {
		t.Run(fmt.Sprintf("%q", minimum), func(t *testing.T) {
			rec := h.postForm(t, "/settings/security", securityForm(func(form url.Values) {
				form.Set("password_min_length", minimum)
			}), false)

			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("POST minimum %q = %d, want 422: %.800s", minimum, rec.Code, rec.Body.String())
			}
			body := rec.Body.String()
			if !strings.Contains(body, "the minimum password length must be between 8 and 64 characters") {
				t.Errorf("the refusal must carry the catalogued sentence, got: %.1500s", body)
			}
			// The field echoes what was submitted: the value is trimmed first, so a
			// whitespace-only submission comes back as the empty value it really is.
			if !strings.Contains(body, `id="password_min_length" name="password_min_length" type="number" min="8" max="64" step="1" value="`+strings.TrimSpace(minimum)+`"`) {
				t.Errorf("the refusal must echo the submitted minimum %q, got: %.1500s", minimum, body)
			}
			wantSecurityCheckedSwitch(t, body, "password_change_enabled", true)
			wantSecurityCheckedSwitch(t, body, "password_recovery_enabled", true)
			assertNoWhitespaceViolations(t, body)
			if got, want := storedPasswordSettings(t, h), application.DefaultPasswordSettings(); got != want {
				t.Errorf("a refused minimum changed the policy: %+v, want %+v", got, want)
			}
		})
	}
}

// TestSettingsSecurityRendersInSpanish proves S10 for this section: the rail, the
// card, the controls and a refusal all read Spanish on a Spanish instance, and
// no English label survives.
func TestSettingsSecurityRendersInSpanish(t *testing.T) {
	h := newHarness(t)
	root, rootSession := seedRoot(t, h)
	if err := h.settings.SetInterfaceLanguage(t.Context(), *root, application.InterfaceLanguageSpanish); err != nil {
		t.Fatalf("switch the instance to Spanish: %v", err)
	}

	body := getAs(t, h, "/settings/security", rootSession.ID).Body.String()
	for _, want := range []string{
		"Seguridad",
		"Contraseñas",
		"Permite que cada persona cambie su propia contraseña desde Preferencias",
		"Entre 8 y 64 caracteres.",
		"Guardar la configuración de contraseñas",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("Spanish Security section must render %q, got: %.1500s", want, body)
		}
	}
	for _, unwanted := range []string{">Security<", ">Passwords<", "Save password settings"} {
		if strings.Contains(body, unwanted) {
			t.Errorf("Spanish Security section must not render the English %q", unwanted)
		}
	}

	rec := h.postForm(t, "/settings/security", securityForm(func(form url.Values) {
		form.Set("password_min_length", "7")
	}), false)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("Spanish POST minimum 7 = %d, want 422: %.800s", rec.Code, rec.Body.String())
	}
	if want := "la longitud mínima de la contraseña debe estar entre 8 y 64 caracteres"; !strings.Contains(rec.Body.String(), want) {
		t.Errorf("the Spanish refusal must read %q, got: %.1500s", want, rec.Body.String())
	}
}

// TestSettingsSecurityExplainsRecoveryNeedsEmail proves S5 on the surface: while
// email delivery is not configured the recovery row states that the switch has
// no effect, and it links to the Email section only for the actor who can open it
// — the instance's own SMTP credentials are root's. Once delivery is configured
// the note is gone.
func TestSettingsSecurityExplainsRecoveryNeedsEmail(t *testing.T) {
	h := newHarness(t)
	root, rootSession := seedRoot(t, h)
	admin := seedUserRole(t, h.store, "Recovery Admin", "recovery-admin@tkt.test", domain.RoleAdmin)
	adminSession := seedSession(t, h.store, admin.ID)
	const note = "Email notifications are off, so this switch has no effect."

	unconfigured := getAs(t, h, "/settings/security", rootSession.ID).Body.String()
	if !strings.Contains(unconfigured, note) {
		t.Errorf("an instance without email must say the recovery switch has no effect, got: %.1500s", unconfigured)
	}
	if !strings.Contains(unconfigured, `<a href="/settings/email">Set up email notifications</a>`) {
		t.Errorf("root must get the link to the Email section, got: %.1500s", unconfigured)
	}

	adminBody := getAs(t, h, "/settings/security", adminSession.ID).Body.String()
	if !strings.Contains(adminBody, note) {
		t.Errorf("an administrator must read the same explanation, got: %.1500s", adminBody)
	}
	if strings.Contains(adminBody, `<a href="/settings/email">`) {
		t.Errorf("an administrator must not be offered a link to a section they cannot open, got: %.1500s", adminBody)
	}

	if err := h.emailSettings.Save(t.Context(), *root, application.EmailSettingsInput{
		Enabled:     true,
		Host:        "smtp.tkt.test",
		Port:        587,
		Encryption:  application.EmailEncryptionSTARTTLS,
		FromAddress: "tkt@tkt.test",
		BaseURL:     "https://tkt.tkt.test",
	}); err != nil {
		t.Fatalf("configure email delivery: %v", err)
	}
	configured := getAs(t, h, "/settings/security", rootSession.ID).Body.String()
	if strings.Contains(configured, note) {
		t.Errorf("a configured instance must not carry the email note, got: %.1500s", configured)
	}
}

// TestSettingsSecurityNavCarriesTheSection proves the rail entry S11 asks for:
// Security is one more section, listed between Email notifications and the
// Working calendar, and marked as the active one when it is open.
func TestSettingsSecurityNavCarriesTheSection(t *testing.T) {
	h := newHarness(t)
	_, rootSession := seedRoot(t, h)

	body := getAs(t, h, "/settings/security", rootSession.ID).Body.String()
	if !strings.Contains(body, `href="/settings/security" aria-current="page">Security</a>`) {
		t.Errorf("the security section must be the active rail entry, got: %.1500s", body)
	}
	email := strings.Index(body, `href="/settings/email">`)
	security := strings.Index(body, `href="/settings/security"`)
	calendar := strings.Index(body, `href="/settings/calendar"`)
	if email < 0 || security < 0 || calendar < 0 {
		t.Fatalf("the rail must carry the email, security and calendar entries, got: %.1500s", body)
	}
	if !(email < security && security < calendar) {
		t.Errorf("Security must sit between Email notifications and Working calendar, got offsets email=%d security=%d calendar=%d", email, security, calendar)
	}

	// An administrator owns CapManageSettings too, so the entry is theirs.
	admin := seedUserRole(t, h.store, "Nav Security Admin", "nav-security@tkt.test", domain.RoleAdmin)
	adminSession := seedSession(t, h.store, admin.ID)
	if adminBody := getAs(t, h, "/settings/security", adminSession.ID).Body.String(); !strings.Contains(adminBody, `href="/settings/security"`) {
		t.Errorf("an administrator's rail must offer Security, got: %.1500s", adminBody)
	}
}
