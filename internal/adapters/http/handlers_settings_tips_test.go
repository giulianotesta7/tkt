package httpadapter

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// Field help behind a toggletip (issue #383, S11): every field explanation in
// every Settings section is a "?" button beside its label, the explanation
// lives in a role="tooltip" element the button describes, and the two
// exceptions stay visible — the skip-verification warning (a security rule that
// must be read without asking) and the "a password is saved" state.

// sectionBodyFor fetches one settings section as the given session.
func sectionBodyFor(t *testing.T, h *harness, path, sessionID string) string {
	t.Helper()
	rec := getAs(t, h, path, sessionID)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200: %s", path, rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

// tooltipText returns the content of the tooltip element with the given id.
func tooltipText(body, id string) string {
	start := strings.Index(body, `id="help-tip-`+id+`"`)
	if start < 0 {
		return ""
	}
	rest := body[start:]
	open := strings.Index(rest, ">")
	if open < 0 {
		return ""
	}
	return rest[open+1 : open+1+strings.Index(rest[open+1:], "</span>")]
}

// TestSettingsFieldHelpIsBehindAToggletip proves the component and its wiring:
// each explanation is a described tooltip of a real button, addressed by its
// field's label, and it is no longer a paragraph under the field.
func TestSettingsFieldHelpIsBehindAToggletip(t *testing.T) {
	h := newHarness(t)
	_, rootSession := seedRoot(t, h)

	cases := []struct {
		path  string
		id    string
		label string
		help  string
	}{
		{"/settings/organization", "org-logo", "Logo", "Square, 256 × 256 px recommended (128 × 128 px at least). PNG, up to 256 KiB."},
		{"/settings/signin", "signin-title", "Title", "Leave it empty to use the default text."},
		{"/settings/signin", "signin-image", "Background image", "1600 × 1800 px recommended. Smaller screens crop the edges, so keep the subject centered."},
		{"/settings/tickets", "sla-warning", "Warning threshold", "At risk once this share of the target has elapsed."},
		{"/settings/tickets", "hold-auto-close-days", "Close tickets on hold after N working days without a reply", "Empty means off: the instance never closes a held ticket on its own. Working days follow the SLA working calendar."},
		{"/settings/calendar", "calendar-days", "Working days", "At least one day."},
		{"/settings/calendar", "calendar-timezone", "Timezone", "IANA name"},
		{"/settings/email", "email-username", "Username", "Leave it empty if your server accepts mail"},
		{"/settings/email", "email-password", "Password", "Stored encrypted and never shown again."},
		{"/settings/email", "email-from-address", "Sender address", "The address notifications are sent from"},
		{"/settings/email", "email-from-name", "Sender name", "Shown as the sender in the mail client."},
		{"/settings/email", "email-base-url", "TKT address", "The public address of this instance"},
	}
	bodies := map[string]string{}
	for _, tc := range cases {
		body, ok := bodies[tc.path]
		if !ok {
			body = sectionBodyFor(t, h, tc.path, rootSession.ID)
			bodies[tc.path] = body
		}
		for _, want := range []string{
			`class="help-tip" type="button" aria-label="More about ` + tc.label + `" aria-describedby="help-tip-` + tc.id + `" aria-expanded="false" data-help-tip>?`,
			`role="tooltip" id="help-tip-` + tc.id + `"`,
			`<p class="help">` + tc.help,
		} {
			present := strings.Contains(body, want)
			// The explanation text must live in the tooltip and nowhere as a
			// paragraph; the other two markers must be present.
			if strings.HasPrefix(want, `<p class="help">`) {
				if present {
					t.Errorf("%s: %q must not render as a visible paragraph", tc.path, tc.help)
				}
				continue
			}
			if !present {
				t.Errorf("%s: the section must render %q", tc.path, want)
			}
		}
		if got := tooltipText(body, tc.id); !strings.Contains(got, tc.help) {
			t.Errorf("%s: the tooltip for %q reads %q, want it to carry %q", tc.path, tc.id, got, tc.help)
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
		`<p class="help">Warning: the connection is still encrypted`,
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
		`aria-label="Más sobre Usuario"`,
	} {
		if !strings.Contains(spanish, want) {
			t.Errorf("the Spanish email section must contain %q, got: %s", want, spanish)
		}
	}
}

// TestToggletipEscapesItsOwnCopy proves the tooltip renders catalogue text as
// text: it is a span, never markup, so a translation can never become a tag.
func TestToggletipEscapesItsOwnCopy(t *testing.T) {
	h := newHarness(t)

	body := sectionGet(t, h, "/settings/organization")
	if strings.Contains(body, `role="tooltip" id="help-tip-org-logo"><`) {
		t.Errorf("the tooltip content must be text, not markup")
	}
	if !strings.Contains(body, `aria-expanded="false"`) {
		t.Errorf("a toggletip starts collapsed")
	}
	// The control is a real button, so keyboard and assistive technology reach
	// it; it is never an anchor with no destination or a click-handled span.
	if strings.Contains(body, `<span class="help-tip"`) {
		t.Errorf("the toggletip control must be a button")
	}
}

// TestToggletipMarkupAndScriptAreShared proves one component serves every
// section: the stylesheet and the script carry the control once, and every
// section that has field help loads them.
func TestToggletipMarkupAndScriptAreShared(t *testing.T) {
	h := newHarness(t)

	for _, path := range []string{"/settings/organization", "/settings/signin", "/settings/tickets", "/settings/calendar"} {
		body := sectionGet(t, h, path)
		for _, want := range []string{`/static/settings.css`, `/static/settings.js`} {
			if !strings.Contains(body, want) {
				t.Errorf("%s must load %q, got: %s", path, want, body)
			}
		}
	}
	// One rule set, not one per section.
	css := readStaticAsset(t, h, "/static/settings.css")
	if got := strings.Count(css, ".help-tip {"); got != 1 {
		t.Errorf("the stylesheet must carry the control once, found %d", got)
	}
	js := readStaticAsset(t, h, "/static/settings.js")
	if !strings.Contains(js, `[data-help-tip]`) {
		t.Errorf("the shared script must own the toggletip behaviour")
	}
}

// readStaticAsset fetches one embedded asset from the harness's own mux.
func readStaticAsset(t *testing.T, h *harness, path string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	h.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200", path, rec.Code)
	}
	return rec.Body.String()
}
