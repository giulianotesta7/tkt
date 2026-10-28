package httpadapter

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// sectionBodyFor fetches one settings section as the given session.
func sectionBodyFor(t *testing.T, h *harness, path, sessionID string) string {
	t.Helper()
	rec := getAs(t, h, path, sessionID)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200: %s", path, rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

// TestSettingsFieldHelpIsVisible proves the maintainer's L5 decision for the
// tabs-and-rows design: every field explanation that used to hide behind a "?"
// toggletip (issue #383, S11) is now visible text — a row description or a
// help paragraph under its field — and no settings section renders a toggletip.
func TestSettingsFieldHelpIsVisible(t *testing.T) {
	h := newHarness(t)
	_, rootSession := seedRoot(t, h)

	cases := []struct {
		path string
		help string
	}{
		{"/settings/organization", "Square, 256 × 256 px recommended (128 × 128 px at least). PNG, up to 256 KiB."},
		{"/settings/signin", "Leave it empty to use the default text."},
		{"/settings/signin", "1600 × 1800 px recommended. Smaller screens crop the edges, so keep the subject centered."},
		{"/settings/tickets", "At risk once this share of the target has elapsed."},
		{"/settings/tickets", "Empty means off: the instance never closes a held ticket on its own. Working days follow the SLA working calendar."},
		{"/settings/calendar", "At least one day."},
		{"/settings/calendar", "IANA name"},
		{"/settings/email", "Leave it empty if your server accepts mail"},
		{"/settings/email", "Stored encrypted and never shown again."},
		{"/settings/email", "The address notifications are sent from, and the name shown in the mail client."},
		{"/settings/email", "The public address of this instance"},
	}
	bodies := map[string]string{}
	for _, tc := range cases {
		body, ok := bodies[tc.path]
		if !ok {
			body = sectionBodyFor(t, h, tc.path, rootSession.ID)
			bodies[tc.path] = body
			if strings.Contains(body, `data-help-tip`) || strings.Contains(body, `role="tooltip"`) {
				t.Errorf("%s: a settings section must not render a toggletip", tc.path)
			}
		}
		paragraph := regexp.MustCompile(`<p( class="help")?>` + regexp.QuoteMeta(strings.ReplaceAll(tc.help, "&", "&amp;")))
		if !paragraph.MatchString(body) {
			t.Errorf("%s: %q must render as a visible description or help paragraph", tc.path, tc.help)
		}
	}
}

// TestSettingsObviousHelpIsDeleted proves the explanations the interface makes
// by itself are gone: the port's default (the field shows 25), the email
// switch's consequence (the switch says it), the notifications transport (the
// section title says it), and the SMTP server's address (the label names it) —
// which also drops that field's toggletip.
func TestSettingsObviousHelpIsDeleted(t *testing.T) {
	h := newHarness(t)
	_, rootSession := seedRoot(t, h)

	body := sectionBodyFor(t, h, "/settings/email", rootSession.ID)
	for _, gone := range []string{"25 by default", "While off, TKT sends nothing", "TKT sends its notifications to your team through this SMTP server", "Host name or IP address"} {
		if strings.Contains(body, gone) {
			t.Errorf("the email section must not carry the obvious copy %q", gone)
		}
	}
	if strings.Contains(body, `id="help-tip-email-host"`) {
		t.Error("the SMTP server field must not carry a toggletip")
	}
	// The label and the control keep working without one.
	if !strings.Contains(body, `<label for="email_host">SMTP server</label>`) {
		t.Error("the SMTP server field must keep its label")
	}
}

// TestSettingsSecurityAndStateHelpStaysVisible proves the two exceptions: the
// skip-verification warning keeps its paragraph, and a stored password's state
// keeps its note.
func TestSettingsSecurityAndStateHelpStaysVisible(t *testing.T) {
	h := newHarness(t)
	root, rootSession := seedRoot(t, h)

	// A stored password is what the state note reports.
	rec := h.postFormAs(t, "/settings/email", emailSettingsForm(func(form url.Values) {
		form.Set("email_encryption", "starttls")
		form.Set("email_username", "notifications")
		form.Set("email_password", "secret")
	}), rootSession.ID)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("saving the email configuration = %d, want 303: %s", rec.Code, rec.Body.String())
	}

	body := sectionBodyFor(t, h, "/settings/email", rootSession.ID)
	for _, want := range []string{
		`<p>Warning: the connection is still encrypted`,
		`<p class="help">A password is saved; leave empty to keep it.</p>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the email section must keep %q visible, got: %s", want, body)
		}
	}
	_ = root
}

// TestSettingsEmailSectionIsNamedEmailNotifications proves the rename reaches
// the nav entry, the heading and the save bar, in both locales.
func TestSettingsEmailSectionIsNamedEmailNotifications(t *testing.T) {
	h := newHarness(t)
	_, rootSession := seedRoot(t, h)

	english := sectionBodyFor(t, h, "/settings/email", rootSession.ID)
	for _, want := range []string{
		`href="/settings/email" aria-current="page">Email notifications</a>`,
		`<h2 class="settings-section-title">Delivery</h2>`,
		`<h2 class="settings-section-title">SMTP server</h2>`,
		`Save email notifications`,
	} {
		if !strings.Contains(english, want) {
			t.Errorf("the English email section must contain %q, got: %s", want, english)
		}
	}

	if err := h.store.SettingsStore().SetInterfaceLanguage(t.Context(), "es"); err != nil {
		t.Fatalf("seed Spanish instance: %v", err)
	}
	spanish := sectionBodyFor(t, h, "/settings/email", rootSession.ID)
	for _, want := range []string{
		`Notificaciones por email</a>`,
		`<h2 class="settings-section-title">Entrega</h2>`,
		`<h2 class="settings-section-title">Servidor SMTP</h2>`,
		`Guardar notificaciones por email`,
		`<p class="help">Déjalo vacío si tu servidor acepta correo sin autenticación.</p>`,
	} {
		if !strings.Contains(spanish, want) {
			t.Errorf("the Spanish email section must contain %q, got: %s", want, spanish)
		}
	}
}
