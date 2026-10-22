package httpadapter

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// Email settings on the Settings screen (issue #362, T3; specs S1-S4, S8,
// S10, S14, S15, S16): the Email section is root-only, it saves the generic
// SMTP configuration (default port 25, none/STARTTLS/TLS, optional
// credentials that need encryption, the public TKT address), it never renders
// the stored password back, and "send test email" sends one message to the
// acting root's own address and shows the outcome — including the server's own
// reply when the send fails.

// seedRoot seeds a root user with a live session and returns both: the
// harness admin an ordinary administrator, and the email section is root-only.
func seedRoot(t *testing.T, h *harness) (*domain.User, *domain.Session) {
	t.Helper()
	root := seedUserRole(t, h.store, "Root", "root@tkt.test", domain.RoleRoot)
	return root, seedSession(t, h.store, root.ID)
}

// emailSettingsForm is one complete, valid email submission; mod may change or
// remove any field to build a rejected or partial one.
func emailSettingsForm(mod func(url.Values)) url.Values {
	form := url.Values{
		"email_enabled":      {"1"},
		"email_host":         {"smtp.tkt.test"},
		"email_port":         {"587"},
		"email_encryption":   {"starttls"},
		"email_username":     {"tkt-smtp"},
		"email_password":     {"smtp-s3cret"},
		"email_from_address": {"tkt@tkt.test"},
		"email_from_name":    {"TKT"},
		"email_base_url":     {"https://tkt.tkt.test"},
	}
	if mod != nil {
		mod(form)
	}
	return form
}

// getAs runs an authenticated GET for an explicit session.
func getAs(t *testing.T, h *harness, path, sessionID string) *httptest.ResponseRecorder {
	t.Helper()
	return doRequest(h.mux, h.mw, http.MethodGet, path, map[string]string{"Cookie": sessionCookie + "=" + sessionID})
}

// assertNoWhitespaceViolations enforces the design-system line contract (no
// whitespace-only line, no trailing space or tab) on the section this task
// adds, which the page-level guard does not cover because its fixture is not
// a root.
func assertNoWhitespaceViolations(t *testing.T, body string) {
	t.Helper()
	for lineNumber, line := range strings.Split(body, "\n") {
		if (line != "" && strings.TrimSpace(line) == "") || strings.HasSuffix(line, " ") || strings.HasSuffix(line, "\t") {
			t.Errorf("rendered line %d breaks the whitespace contract: %q", lineNumber+1, line)
		}
	}
}

// TestSettingsEmailSectionIsRootOnly proves the section renders for the root
// who may manage email and never for an administrator: the capability, not
// the markup, decides.
func TestSettingsEmailSectionIsRootOnly(t *testing.T) {
	h := newHarness(t)
	_, rootSession := seedRoot(t, h)

	rec := getAs(t, h, "/settings/email", rootSession.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("root GET /settings = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		`<h2 class="settings-section-title">Email notifications</h2>`,
		`action="/settings/email"`,
		`formaction="/settings/email/test"`,
		`id="email_host" name="email_host" value=""`,
		`id="email_port" name="email_port" inputmode="numeric" value="25"`,
		`id="email_encryption" name="email_encryption"`,
		`<option value="none" selected>None</option>`,
		`<option value="starttls">STARTTLS</option>`,
		`<option value="tls">TLS</option>`,
		`name="email_skip_tls_verify" value="1">`,
		"Do not verify the certificate", // S16: the option
		"Warning",                       // S16: its on-screen warning
		`id="email_username" name="email_username" value=""`,
		`id="email_password" name="email_password" value=""`,
		"Stored encrypted and never shown again.",
		`id="email_from_address" name="email_from_address" value=""`,
		`id="email_from_name" name="email_from_name" value=""`,
		`id="email_base_url" name="email_base_url" value=""`, // S10
		"https://tkt.example.com",                            // S10: the shape the field asks for
		"Sends one email to root@tkt.test",
		"Save email notifications",
		"Send test email",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("root settings page must contain %q, got: %.1500s", want, body)
		}
	}
	assertNoWhitespaceViolations(t, body)

	adminBody := h.get(t, "/settings/email", false).Body.String()
	for _, absent := range []string{
		`action="/settings/email"`,
		`name="email_host"`,
		`name="email_password"`,
		"Send test email",
	} {
		if strings.Contains(adminBody, absent) {
			t.Errorf("administrator settings page must not contain %q, got: %.1500s", absent, adminBody)
		}
	}
}

// TestSettingsEmailRoutesRejectEveryoneButRoot proves the two email routes are
// gated at the HTTP boundary for every other role: nothing is stored and
// nothing is sent.
func TestSettingsEmailRoutesRejectEveryoneButRoot(t *testing.T) {
	h := newHarness(t)
	_, rootSession := seedRoot(t, h)

	agent := seedUserRole(t, h.store, "Agent", "agent@tkt.test", domain.RoleAgent)
	user := seedUserRole(t, h.store, "User", "user@tkt.test", domain.RoleUser)
	for _, tc := range []struct {
		name      string
		sessionID string
	}{
		{"admin", h.adminSession.ID},
		{"agent", seedSession(t, h.store, agent.ID).ID},
		{"user", seedSession(t, h.store, user.ID).ID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := h.postFormAs(t, "/settings/email", emailSettingsForm(nil), tc.sessionID)
			if rec.Code != http.StatusForbidden {
				t.Errorf("%s POST /settings/email = %d, want 403", tc.name, rec.Code)
			}
			rec = h.postFormAs(t, "/settings/email/test", url.Values{}, tc.sessionID)
			if rec.Code != http.StatusForbidden {
				t.Errorf("%s POST /settings/email/test = %d, want 403", tc.name, rec.Code)
			}
		})
	}

	stored, err := h.store.EmailSettingsStore().GetEmailSettings(t.Context())
	if err != nil {
		t.Fatalf("read stored email settings: %v", err)
	}
	if stored.Enabled || stored.Host != "" || stored.SealedPassword != "" {
		t.Errorf("a rejected role must store nothing, got %+v", stored)
	}
	if len(h.mailer.sent) != 0 {
		t.Errorf("a rejected role must send nothing, sent %d", len(h.mailer.sent))
	}

	// The root the same harness holds still reaches the section, so the
	// refusals above are the capability and not a broken route.
	if rec := getAs(t, h, "/settings/email", rootSession.ID); rec.Code != http.StatusOK {
		t.Errorf("root GET /settings = %d, want 200", rec.Code)
	}
}

// TestSettingsEmailSavePersistsAndRedirects proves a valid submission is
// stored whole, sealed, and rendered back without the password (S1-S4, S8).
func TestSettingsEmailSavePersistsAndRedirects(t *testing.T) {
	h := newHarness(t)
	root, rootSession := seedRoot(t, h)

	rec := h.postFormAs(t, "/settings/email", emailSettingsForm(func(f url.Values) {
		f.Set("email_skip_tls_verify", "1")
	}), rootSession.ID)
	wantRedirect(t, rec, http.StatusSeeOther, "/settings/email")

	stored, err := h.store.EmailSettingsStore().GetEmailSettings(t.Context())
	if err != nil {
		t.Fatalf("read stored email settings: %v", err)
	}
	if !stored.Enabled || stored.Host != "smtp.tkt.test" || stored.Port != 587 {
		t.Errorf("stored connection = %+v, want enabled smtp.tkt.test:587", stored)
	}
	if stored.Encryption != application.EmailEncryptionSTARTTLS || !stored.SkipTLSVerify {
		t.Errorf("stored encryption = %q skip-verify = %t, want starttls/true", stored.Encryption, stored.SkipTLSVerify)
	}
	if stored.Username != "tkt-smtp" || stored.FromAddress != "tkt@tkt.test" || stored.FromName != "TKT" {
		t.Errorf("stored sender/credentials = %+v", stored)
	}
	if stored.BaseURL != "https://tkt.tkt.test" {
		t.Errorf("stored base URL = %q, want https://tkt.tkt.test", stored.BaseURL)
	}
	if stored.SealedPassword == "" || strings.Contains(stored.SealedPassword, "smtp-s3cret") {
		t.Errorf("stored password must be sealed, got %q", stored.SealedPassword)
	}

	// The read use case never hands the sealed value back (S8).
	got, err := h.emailSettings.Get(t.Context(), *root)
	if err != nil {
		t.Fatalf("get email settings as root: %v", err)
	}
	if !got.PasswordSet || got.SealedPassword != "" {
		t.Errorf("Get = %+v, want PasswordSet=true and no sealed value", got)
	}

	// The next render shows the stored configuration and no password.
	rec = getAs(t, h, "/settings/email", rootSession.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("root GET /settings = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`name="email_enabled" value="1" checked`,
		`id="email_host" name="email_host" value="smtp.tkt.test"`,
		`id="email_port" name="email_port" inputmode="numeric" value="587"`,
		`<option value="starttls" selected>STARTTLS</option>`,
		`name="email_skip_tls_verify" value="1" checked`,
		`id="email_username" name="email_username" value="tkt-smtp"`,
		`id="email_from_address" name="email_from_address" value="tkt@tkt.test"`,
		`id="email_from_name" name="email_from_name" value="TKT"`,
		`id="email_base_url" name="email_base_url" value="https://tkt.tkt.test"`,
		"A password is saved; leave empty to keep it.",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("settings page after save must contain %q, got: %.1500s", want, body)
		}
	}
	for _, secret := range []string{"smtp-s3cret", stored.SealedPassword} {
		if strings.Contains(body, secret) {
			t.Errorf("settings page must never render the password (%q), got: %.1500s", secret, body)
		}
	}
	if !strings.Contains(body, `id="email_password" name="email_password" value=""`) {
		t.Errorf("the password input must always render empty, got: %.1500s", body)
	}
}

// TestSettingsEmailRejectedSaveWritesNothing proves validation (S10, S15) runs
// before the write: a rejected submission stores nothing, re-renders the
// submitted values, and still never echoes the password.
func TestSettingsEmailRejectedSaveWritesNothing(t *testing.T) {
	for _, tc := range []struct {
		name   string
		modify func(url.Values)
		want   string
	}{
		{"missing host", func(f url.Values) { f.Del("email_host") }, "enter the SMTP server to turn email on"},
		{"missing TKT address", func(f url.Values) { f.Del("email_base_url") }, "enter the TKT address to turn email on"},
		{"credentials without encryption", func(f url.Values) { f.Set("email_encryption", "none") }, "a username and password need STARTTLS or TLS"},
		{"unreadable port", func(f url.Values) { f.Set("email_port", "not-a-port") }, "the port must be a number from 1 to 65535"},
		{"explicit port zero", func(f url.Values) { f.Set("email_port", "0") }, "the port must be a number from 1 to 65535"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			_, rootSession := seedRoot(t, h)

			rec := h.postFormAs(t, "/settings/email", emailSettingsForm(tc.modify), rootSession.ID)
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("rejected save = %d, want 422: %.800s", rec.Code, rec.Body.String())
			}
			body := rec.Body.String()
			if !strings.Contains(body, tc.want) {
				t.Errorf("rejected save must show %q, got: %.1500s", tc.want, body)
			}
			if strings.Contains(body, `"error.`) {
				t.Errorf("rejected save must not render a raw catalogue key: %.800s", body)
			}
			// The submitted values come back so one bad field costs nothing...
			if !strings.Contains(body, `id="email_from_address" name="email_from_address" value="tkt@tkt.test"`) {
				t.Errorf("rejected save must echo the submitted sender address, got: %.1500s", body)
			}
			// ...and the password never does.
			if strings.Contains(body, "smtp-s3cret") {
				t.Errorf("rejected save must not echo the submitted password, got: %.1500s", body)
			}
			if !strings.Contains(body, `id="email_password" name="email_password" value=""`) {
				t.Errorf("rejected save must render an empty password field, got: %.1500s", body)
			}

			stored, err := h.store.EmailSettingsStore().GetEmailSettings(t.Context())
			if err != nil {
				t.Fatalf("read stored email settings: %v", err)
			}
			if stored.Enabled || stored.Host != "" || stored.SealedPassword != "" {
				t.Errorf("a rejected save must write nothing, got %+v", stored)
			}
		})
	}
}

// TestSettingsEmailKeepsTheStoredPasswordOnAnEmptyField proves the empty
// password field keeps what is stored (S8) and the submit accepts no
// credentials at all (S2): the send proves which password the service
// unsealed.
func TestSettingsEmailKeepsTheStoredPasswordOnAnEmptyField(t *testing.T) {
	h := newHarness(t)
	_, rootSession := seedRoot(t, h)

	if rec := h.postFormAs(t, "/settings/email", emailSettingsForm(nil), rootSession.ID); rec.Code != http.StatusSeeOther {
		t.Fatalf("first save = %d, want 303: %.800s", rec.Code, rec.Body.String())
	}
	// The second submission leaves the password field empty and moves the host.
	rec := h.postFormAs(t, "/settings/email", emailSettingsForm(func(f url.Values) {
		f.Del("email_password")
		f.Set("email_host", "smtp2.tkt.test")
	}), rootSession.ID)
	wantRedirect(t, rec, http.StatusSeeOther, "/settings/email")

	stored, err := h.store.EmailSettingsStore().GetEmailSettings(t.Context())
	if err != nil {
		t.Fatalf("read stored email settings: %v", err)
	}
	if stored.Host != "smtp2.tkt.test" {
		t.Errorf("stored host = %q, want smtp2.tkt.test", stored.Host)
	}

	rec = h.postFormAs(t, "/settings/email/test", url.Values{}, rootSession.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("test email = %d, want 200: %.800s", rec.Code, rec.Body.String())
	}
	if h.mailer.cfg.Password != "smtp-s3cret" {
		t.Errorf("test email password = %q, want the kept smtp-s3cret", h.mailer.cfg.Password)
	}
	if h.mailer.cfg.Host != "smtp2.tkt.test" {
		t.Errorf("test email host = %q, want the saved smtp2.tkt.test", h.mailer.cfg.Host)
	}
}

// TestSettingsEmailRemovingTheUsernameRemovesTheCredentials proves an open
// relay (S2) is saved without credentials.
func TestSettingsEmailRemovingTheUsernameRemovesTheCredentials(t *testing.T) {
	h := newHarness(t)
	_, rootSession := seedRoot(t, h)

	if rec := h.postFormAs(t, "/settings/email", emailSettingsForm(nil), rootSession.ID); rec.Code != http.StatusSeeOther {
		t.Fatalf("first save = %d, want 303: %.800s", rec.Code, rec.Body.String())
	}
	rec := h.postFormAs(t, "/settings/email", emailSettingsForm(func(f url.Values) {
		f.Del("email_username")
		f.Del("email_password")
		f.Set("email_encryption", "none")
	}), rootSession.ID)
	wantRedirect(t, rec, http.StatusSeeOther, "/settings/email")

	stored, err := h.store.EmailSettingsStore().GetEmailSettings(t.Context())
	if err != nil {
		t.Fatalf("read stored email settings: %v", err)
	}
	if stored.Username != "" || stored.SealedPassword != "" {
		t.Errorf("removing the username must remove the credentials, got %+v", stored)
	}
	if stored.Encryption != application.EmailEncryptionNone {
		t.Errorf("stored encryption = %q, want none", stored.Encryption)
	}
}

// TestSettingsEmailTestSendsToTheRootsOwnAddress proves the test action sends
// one message to the acting root and reports success on the page (S14).
func TestSettingsEmailTestSendsToTheRootsOwnAddress(t *testing.T) {
	h := newHarness(t)
	root, rootSession := seedRoot(t, h)

	if rec := h.postFormAs(t, "/settings/email", emailSettingsForm(nil), rootSession.ID); rec.Code != http.StatusSeeOther {
		t.Fatalf("save = %d, want 303: %.800s", rec.Code, rec.Body.String())
	}

	rec := h.postFormAs(t, "/settings/email/test", url.Values{}, rootSession.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("test email = %d, want 200: %.800s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Test email sent to "+root.Email+".") {
		t.Errorf("test email must report success with the root's own address, got: %.1500s", body)
	}
	if len(h.mailer.sent) != 1 {
		t.Fatalf("sends = %d, want exactly 1", len(h.mailer.sent))
	}
	sent := h.mailer.sent[0]
	if sent.To != root.Email {
		t.Errorf("test email recipient = %q, want the acting root %q", sent.To, root.Email)
	}
	if sent.Subject == "" || sent.Body == "" {
		t.Errorf("test email must carry subject and body, got %+v", sent)
	}
	if h.mailer.cfg.Username != "tkt-smtp" || h.mailer.cfg.Password != "smtp-s3cret" {
		t.Errorf("test email must use the stored credentials, got %+v", h.mailer.cfg)
	}
	if strings.Contains(body, "smtp-s3cret") {
		t.Errorf("the settings page must never render the password: %.1500s", body)
	}
}

// TestSettingsEmailTestReportsTheServerReply proves a refused send shows the
// SMTP reply (S14) without leaking the password.
func TestSettingsEmailTestReportsTheServerReply(t *testing.T) {
	h := newHarness(t)
	_, rootSession := seedRoot(t, h)
	h.mailer.err = errors.New("550 5.7.1 relay access denied")

	if rec := h.postFormAs(t, "/settings/email", emailSettingsForm(nil), rootSession.ID); rec.Code != http.StatusSeeOther {
		t.Fatalf("save = %d, want 303: %.800s", rec.Code, rec.Body.String())
	}

	rec := h.postFormAs(t, "/settings/email/test", url.Values{}, rootSession.ID)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("refused test email = %d, want 502: %.800s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{"The mail server refused the test email", "550 5.7.1 relay access denied"} {
		if !strings.Contains(body, want) {
			t.Errorf("refused test email must show %q, got: %.1500s", want, body)
		}
	}
	if strings.Contains(body, "smtp-s3cret") {
		t.Errorf("the delivery detail must never carry the password: %.1500s", body)
	}
}

// TestSettingsEmailTestRefusedUntilConfigured proves the test action asks for
// a saved server and sender instead of sending with a half-empty
// configuration.
func TestSettingsEmailTestRefusedUntilConfigured(t *testing.T) {
	h := newHarness(t)
	_, rootSession := seedRoot(t, h)

	rec := h.postFormAs(t, "/settings/email/test", url.Values{}, rootSession.ID)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unconfigured test email = %d, want 422: %.800s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "save an SMTP server and a sender address before sending a test email") {
		t.Errorf("unconfigured test email must explain what is missing, got: %.1500s", rec.Body.String())
	}
	if len(h.mailer.sent) != 0 {
		t.Errorf("nothing may be sent for an unconfigured server, sent %d", len(h.mailer.sent))
	}
}

// TestSettingsEmailSectionDisabledSaveIsAllowed proves a root can keep email
// off while filling the form in: the required fields only bind when sending is
// on, and the default port applies to an empty one (S1).
func TestSettingsEmailSectionDisabledSaveIsAllowed(t *testing.T) {
	h := newHarness(t)
	_, rootSession := seedRoot(t, h)

	rec := h.postFormAs(t, "/settings/email", url.Values{
		"email_host":            {"smtp.tkt.test"},
		"email_port":            {""},
		"email_encryption":      {"none"},
		"email_from_address":    {""},
		"email_base_url":        {""},
		"email_from_name":       {""},
		"email_username":        {""},
		"email_password":        {""},
		"email_skip_tls_verify": {""},
	}, rootSession.ID)
	wantRedirect(t, rec, http.StatusSeeOther, "/settings/email")

	stored, err := h.store.EmailSettingsStore().GetEmailSettings(t.Context())
	if err != nil {
		t.Fatalf("read stored email settings: %v", err)
	}
	if stored.Enabled {
		t.Errorf("stored enabled = true, want false")
	}
	if stored.Port != application.DefaultSMTPPort {
		t.Errorf("stored port = %d, want the default %d", stored.Port, application.DefaultSMTPPort)
	}
	if stored.Host != "smtp.tkt.test" {
		t.Errorf("stored host = %q, want smtp.tkt.test", stored.Host)
	}
}

// TestSettingsEmailErrorsFollowTheInstanceLanguage proves the section's copy —
// validation errors included — follows the instance language (S7 of issue
// #335 applies to every surface).
func TestSettingsEmailErrorsFollowTheInstanceLanguage(t *testing.T) {
	h := newHarness(t)
	root, rootSession := seedRoot(t, h)
	if err := h.settings.SetInterfaceLanguage(t.Context(), *root, application.InterfaceLanguageSpanish); err != nil {
		t.Fatalf("set interface language: %v", err)
	}

	rec := h.postFormAs(t, "/settings/email", emailSettingsForm(func(f url.Values) { f.Del("email_host") }), rootSession.ID)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("rejected save = %d, want 422: %.800s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{"Configuración", "Notificaciones por email", "ingresa el servidor SMTP para activar el email", "Guardar notificaciones por email"} {
		if !strings.Contains(body, want) {
			t.Errorf("Spanish settings page must render %q, got: %.1500s", want, body)
		}
	}
	if strings.Contains(body, `"error.`) {
		t.Errorf("rejected save must not render a raw catalogue key: %.800s", body)
	}

	// The refused-send sentence interpolates the server's reply, localized.
	if rec := h.postFormAs(t, "/settings/email", emailSettingsForm(nil), rootSession.ID); rec.Code != http.StatusSeeOther {
		t.Fatalf("save = %d, want 303: %.800s", rec.Code, rec.Body.String())
	}
	h.mailer.err = errors.New("550 5.7.1 relay access denied")
	rec = h.postFormAs(t, "/settings/email/test", url.Values{}, rootSession.ID)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("refused test email = %d, want 502: %.800s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); !strings.Contains(body, "550 5.7.1 relay access denied") {
		t.Errorf("refused test email must show the server reply, got: %.1500s", body)
	}
}

// TestSettingsEmailSectionKeepsItsSiblingsReachable proves the root's Email
// section is one settings section among the others rather than a page of its
// own: the section rail reaches every other section, and the section itself
// renders only its own controls (issue #383, S1/S2).
func TestSettingsEmailSectionKeepsItsSiblingsReachable(t *testing.T) {
	h := newHarness(t)
	_, rootSession := seedRoot(t, h)

	rec := getAs(t, h, "/settings/email", rootSession.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("root GET /settings/email = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`href="/settings/organization">Organization</a>`,
		`href="/settings/tickets">Tickets &amp; SLA</a>`,
		`href="/settings/email" aria-current="page">Email notifications</a>`,
		`<h2 class="settings-section-title">Email notifications</h2>`,
		`id="email_host" name="email_host"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the root's email section must contain %q, got: %.1500s", want, body)
		}
	}
	for _, unwanted := range []string{`name="internal_comment_bg"`, `min="1" max="99" value="80"`, `id="sla_calendar_start"`} {
		if strings.Contains(body, unwanted) {
			t.Errorf("the email section must not render another section's %q", unwanted)
		}
	}
}
