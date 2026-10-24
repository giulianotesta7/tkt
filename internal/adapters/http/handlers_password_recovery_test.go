package httpadapter

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// Self-service password recovery, public side (issue #414, S3/S5/S7/S9): the
// sign-in page offers "Forgot your password?" only while the instance switch is
// ON and email delivery is configured, the two public pages always answer the
// same neutral confirmation and never leak the token into a redirect or a
// cache, and a dead link is one indistinguishable refusal.

// emailBaseURL is the instance address the mailed links are built from: the
// stored base URL, never the Host header of the request that asked for it (S7).
const emailBaseURL = "https://tkt.example.test"

// configureEmailDelivery saves a complete, enabled SMTP configuration through
// the real service, the way the Email notifications section saves it, so
// delivery counts as configured (S5).
func configureEmailDelivery(t *testing.T, h *harness) {
	t.Helper()
	root, _ := seedRoot(t, h)
	configureEmailDeliveryAs(t, h, *root)
}

// configureEmailDeliveryAs saves the same configuration with an explicit root
// actor, for a test that already stored the single root account itself.
func configureEmailDeliveryAs(t *testing.T, h *harness, root domain.User) {
	t.Helper()
	err := h.emailSettings.Save(t.Context(), root, application.EmailSettingsInput{
		Enabled:       true,
		Host:          "smtp.tkt.test",
		Port:          587,
		Encryption:    application.EmailEncryptionSTARTTLS,
		Username:      "tkt",
		Password:      "smtp-s3cret",
		FromAddress:   "tkt@tkt.test",
		FromName:      "TKT",
		BaseURL:       emailBaseURL,
		SkipTLSVerify: false,
	})
	if err != nil {
		t.Fatalf("configure email delivery: %v", err)
	}
}

// setRecoverySwitch saves the instance password policy through its use case, so
// a test turns the public recovery path off exactly the way the Security
// section does (S1).
func setRecoverySwitch(t *testing.T, h *harness, recovery bool) {
	t.Helper()
	err := h.passwords.Update(t.Context(), *h.admin, application.PasswordSettingsInput{
		ChangeEnabled:   true,
		RecoveryEnabled: recovery,
		MinLength:       application.DefaultPasswordMinLength,
	})
	if err != nil {
		t.Fatalf("save password policy: %v", err)
	}
}

// forgot submits the public recovery form as a signed-out visitor.
func forgot(t *testing.T, h *harness, email string) *httptest.ResponseRecorder {
	t.Helper()
	return h.postFormAs(t, "/login/forgot", url.Values{"email": {email}}, "")
}

// mailedResetToken reads the single reset email the harness mailer received
// since before, and answers the raw token the link in it carries.
func mailedResetToken(t *testing.T, h *harness, before int) string {
	t.Helper()
	if len(h.mailer.sent) != before+1 {
		t.Fatalf("mailed %d messages, want exactly 1", len(h.mailer.sent)-before)
	}
	return resetTokenFromBody(t, h.mailer.sent[len(h.mailer.sent)-1].Body)
}

// resetTokenFromBody answers the raw token of the recovery link a message body
// carries, failing when the body carries none.
func resetTokenFromBody(t *testing.T, body string) string {
	t.Helper()
	marker := application.PasswordResetPath + "?token="
	i := strings.Index(body, marker)
	if i < 0 {
		t.Fatalf("the mailed body carries no reset link:\n%s", body)
	}
	rest := body[i+len(marker):]
	end := strings.IndexFunc(rest, func(r rune) bool {
		return !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f')
	})
	if end < 0 {
		end = len(rest)
	}
	if end == 0 {
		t.Fatalf("the mailed link carries an empty token:\n%s", body)
	}
	return rest[:end]
}

// requestResetLink asks for a link for email and answers the token it carries.
func requestResetLink(t *testing.T, h *harness, email string) string {
	t.Helper()
	before := len(h.mailer.sent)
	rec := forgot(t, h, email)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /login/forgot = %d, want 200", rec.Code)
	}
	return mailedResetToken(t, h, before)
}

// resetForm submits the public reset form.
func resetForm(t *testing.T, h *harness, token, newPassword, confirmation string) *httptest.ResponseRecorder {
	t.Helper()
	return h.postFormAs(t, "/login/reset", url.Values{
		passwordResetTokenField: {token},
		passwordFieldNew:        {newPassword},
		passwordFieldConfirm:    {confirmation},
	}, "")
}

// assertNoTokenLeak proves the raw token never travels in a redirect Location
// and never appears in a rendered page (S3).
func assertNoTokenLeak(t *testing.T, rec *httptest.ResponseRecorder, token string) {
	t.Helper()
	if token == "" {
		return
	}
	if location := rec.Header().Get("Location"); strings.Contains(location, token) {
		t.Errorf("Location %q carries the reset token", location)
	}
	if strings.Contains(rec.Body.String(), token) && rec.Code != http.StatusOK {
		t.Errorf("a refused page echoes the reset token")
	}
}

// TestRecoveryIsOfferedOnlyWithSwitchAndDelivery proves the four combinations
// of S1 and S5: the sign-in link appears and the public routes answer only when
// the switch is ON AND email delivery is configured. Any other combination
// offers nothing and answers 404 on every recovery route.
func TestRecoveryIsOfferedOnlyWithSwitchAndDelivery(t *testing.T) {
	cases := []struct {
		name     string
		recovery bool
		delivery bool
	}{
		{"switch on, delivery on", true, true},
		{"switch on, delivery off", true, false},
		{"switch off, delivery on", false, true},
		{"switch off, delivery off", false, false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			h.createUser(t, "Ana", "ana@example.com", "s3cret-pass-12")
			if tt.delivery {
				configureEmailDelivery(t, h)
			}
			setRecoverySwitch(t, h, tt.recovery)
			offered := tt.recovery && tt.delivery

			login := doRequest(h.mux, h.mw, http.MethodGet, "/login", nil)
			if login.Code != http.StatusOK {
				t.Fatalf("GET /login = %d, want 200", login.Code)
			}
			body := login.Body.String()
			link := `<a class="auth-link" href="/login/forgot">Forgot your password?</a>`
			switch offered {
			case true:
				if !strings.Contains(body, link) {
					t.Errorf("sign-in page must offer the recovery link, got: %.1200s", body)
				}
			case false:
				if strings.Contains(body, link) {
					t.Errorf("sign-in page must not offer the recovery link")
				}
			}

			want := http.StatusNotFound
			if offered {
				want = http.StatusOK
			}
			pages := map[string]func() *httptest.ResponseRecorder{
				"GET /login/forgot": func() *httptest.ResponseRecorder {
					return doRequest(h.mux, h.mw, http.MethodGet, "/login/forgot", nil)
				},
				"POST /login/forgot": func() *httptest.ResponseRecorder { return forgot(t, h, "ana@example.com") },
			}
			for name, request := range pages {
				rec := request()
				if rec.Code != want {
					t.Errorf("%s = %d, want %d", name, rec.Code, want)
				}
			}
			// The reset page answers the form for a link that works and the
			// refusal state for one that does not, so a bogus token proves which of
			// the two the instance is willing to answer at all.
			resetWant := http.StatusNotFound
			if offered {
				resetWant = http.StatusGone
			}
			for name, rec := range map[string]*httptest.ResponseRecorder{
				"GET /login/reset":  doRequest(h.mux, h.mw, http.MethodGet, "/login/reset?token=abc", nil),
				"POST /login/reset": resetForm(t, h, "abc", "recovered-pass-12", "recovered-pass-12"),
			} {
				if rec.Code != resetWant {
					t.Errorf("%s = %d, want %d", name, rec.Code, resetWant)
				}
			}
			// The routes refuse the whole path, so nothing is mailed in the
			// combinations that offer nothing; the offered one mails the single
			// eligible account its link.
			wantMailed := 0
			if offered {
				wantMailed = 1
			}
			if len(h.mailer.sent) != wantMailed {
				t.Errorf("mailed %d messages, want %d", len(h.mailer.sent), wantMailed)
			}
		})
	}
}

// TestForgotAnswersOneNeutralConfirmation proves S3 and S9 on the wire: a known
// active account, an unknown address, an inactive account and an address inside
// the throttle window all answer the SAME status and the SAME body, only the
// eligible known account is mailed, and the link is built from the stored base
// URL (S7).
func TestForgotAnswersOneNeutralConfirmation(t *testing.T) {
	h := newHarness(t)
	h.createUser(t, "Ana", "ana@example.com", "s3cret-pass-12")
	inactive := h.createUser(t, "Bea", "bea@example.com", "s3cret-pass-12")
	inactive.Active = false
	if err := h.store.UserStore().Update(t.Context(), inactive); err != nil {
		t.Fatalf("deactivate fixture: %v", err)
	}
	configureEmailDelivery(t, h)

	// The eligible account first, so the later request for the same address
	// lands inside the five-minute throttle window.
	answers := map[string]*httptest.ResponseRecorder{
		"known":     forgot(t, h, "ana@example.com"),
		"unknown":   forgot(t, h, "nobody@example.com"),
		"inactive":  forgot(t, h, "bea@example.com"),
		"throttled": forgot(t, h, "ana@example.com"),
	}
	first := answers["known"]
	for name, rec := range answers {
		if rec.Code != http.StatusOK {
			t.Errorf("%s: status = %d, want 200", name, rec.Code)
		}
		if body := rec.Body.String(); body != first.Body.String() {
			t.Errorf("%s: body differs from the known-account answer:\n%.2000s\n--- want ---\n%.2000s", name, body, first.Body.String())
		}
		// Every byte the client can see matches, the complete header map
		// included: a differing header would let one answer be told apart from
		// another even when the body is identical.
		if got, want := len(rec.Header()), len(first.Header()); got != want {
			t.Errorf("%s: %d headers, want %d (%v)", name, got, want, rec.Header())
		}
		for key := range first.Header() {
			if got, want := rec.Header().Get(key), first.Header().Get(key); got != want {
				t.Errorf("%s: header %s = %q, want %q", name, key, got, want)
			}
		}
	}
	for _, want := range []string{
		"Check your email",
		"If an account exists for that email, we sent a link. It is valid for 1 hour.",
	} {
		if !strings.Contains(first.Body.String(), want) {
			t.Errorf("the neutral confirmation must contain %q", want)
		}
	}
	// The one eligible request mailed exactly one message, to the account's own
	// address, and the link is the instance address plus the public path.
	if len(h.mailer.sent) != 1 {
		t.Fatalf("mailed %d messages, want exactly 1 (only the eligible account)", len(h.mailer.sent))
	}
	message := h.mailer.sent[0]
	if message.To != "ana@example.com" {
		t.Errorf("email sent to %q, want the account address", message.To)
	}
	if want := emailBaseURL + application.PasswordResetPath + "?token="; !strings.Contains(message.Body, want) {
		t.Errorf("the mailed link must start with the stored base URL %q:\n%s", want, message.Body)
	}
	assertNoWhitespaceViolations(t, first.Body.String())
}

// TestResetPageOffersTheFormForAUsableLink proves GET /login/reset renders the
// new-password form with the token in a hidden field and the instance minimum as
// help (S3/S4), and that reading the page does not consume the link.
func TestResetPageOffersTheFormForAUsableLink(t *testing.T) {
	h := newHarness(t)
	h.createUser(t, "Ana", "ana@example.com", "s3cret-pass-12")
	configureEmailDelivery(t, h)
	token := requestResetLink(t, h, "ana@example.com")

	rec := doRequest(h.mux, h.mw, http.MethodGet, "/login/reset?token="+token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /login/reset = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		"Choose a new password",
		`action="/login/reset"`,
		`<input type="hidden" name="token" value="` + token + `">`,
		`name="new_password"`,
		`name="confirm_password"`,
		"At least 12 characters.",
		"Save new password",
		`<a href="/login">Back to sign in</a>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("reset page must contain %q, got: %.1500s", want, body)
		}
	}
	// Reading the form did not spend the link.
	again := doRequest(h.mux, h.mw, http.MethodGet, "/login/reset?token="+token, nil)
	if again.Code != http.StatusOK {
		t.Errorf("a second GET = %d, want the form again", again.Code)
	}
	assertNoWhitespaceViolations(t, body)
}

// TestResetPageRefusesADeadLink proves the ONE refusal state for an unknown, an
// expired and an already spent link: the same status, the same copy, and a
// "Request a new link" action that leads back to the public form.
func TestResetPageRefusesADeadLink(t *testing.T) {
	h := newHarness(t)
	h.createUser(t, "Ana", "ana@example.com", "s3cret-pass-12")
	configureEmailDelivery(t, h)

	spent := requestResetLink(t, h, "ana@example.com")
	success := resetForm(t, h, spent, "recovered-pass-12", "recovered-pass-12")
	wantRedirect(t, success, http.StatusSeeOther, "/login?password_changed=1")

	// The throttle window belongs to the account, so a second link for the same
	// address needs the clock to pass it (S9).
	h.clock.Advance(application.PasswordResetThrottle)
	expired := requestResetLink(t, h, "ana@example.com")
	h.clock.Advance(application.PasswordResetTokenTTL + time.Minute)

	cases := map[string]string{
		"unknown": "0000000000000000000000000000000000000000000000000000000000000000",
		"expired": expired,
		"spent":   spent,
	}
	var refusedBody string
	for name, token := range cases {
		rec := doRequest(h.mux, h.mw, http.MethodGet, "/login/reset?token="+token, nil)
		if rec.Code != http.StatusGone {
			t.Errorf("%s: status = %d, want 410", name, rec.Code)
		}
		body := rec.Body.String()
		for _, want := range []string{
			"This link is no longer valid",
			"It expired or was already used. A link is valid for 1 hour and works once.",
			`<a class="btn primary" href="/login/forgot">Request a new link</a>`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("%s: refusal must contain %q, got: %.1500s", name, want, body)
			}
		}
		if strings.Contains(body, `name="token"`) {
			t.Errorf("%s: the refusal state must not render a token field", name)
		}
		if refusedBody == "" {
			refusedBody = body
		} else if body != refusedBody {
			t.Errorf("%s: refusal differs from the other dead links", name)
		}
		assertNoTokenLeak(t, rec, token)
	}
	assertNoWhitespaceViolations(t, refusedBody)
}

// TestResetReplacesThePasswordAndSignsEverySessionOut proves the success path
// (S3): the reset answers 303 to the sign-in page with the changed-password
// flag and no token, the sign-in page renders the banner, the new password
// works, the old one does not, and every session minted before the reset is
// signed out.
func TestResetReplacesThePasswordAndSignsEverySessionOut(t *testing.T) {
	h := newHarness(t)
	h.createUser(t, "Ana", "ana@example.com", "s3cret-pass-12")
	configureEmailDelivery(t, h)

	const oldPassword = "s3cret-pass-12"
	const newPassword = "recovered-pass-12"
	laptop := h.loginCookie(t, "ana@example.com", oldPassword)
	phone := h.loginCookie(t, "ana@example.com", oldPassword)
	if laptop == "" || phone == "" {
		t.Fatal("fixture sessions were not created")
	}

	token := requestResetLink(t, h, "ana@example.com")
	rec := resetForm(t, h, token, newPassword, newPassword)
	wantRedirect(t, rec, http.StatusSeeOther, "/login?password_changed=1")
	assertNoTokenLeak(t, rec, token)
	if len(rec.Result().Cookies()) != 0 {
		t.Errorf("a successful reset must not sign anyone in, got %d cookies", len(rec.Result().Cookies()))
	}

	landing := doRequest(h.mux, h.mw, http.MethodGet, "/login?password_changed=1", nil)
	if landing.Code != http.StatusOK {
		t.Fatalf("GET /login?password_changed=1 = %d, want 200", landing.Code)
	}
	if !strings.Contains(landing.Body.String(), "Your password was changed. Every session was signed out, so sign in again.") {
		t.Errorf("the sign-in page must carry the changed-password banner, got: %.1500s", landing.Body.String())
	}
	// The banner belongs to the flag only: the plain sign-in page stays clean.
	plain := doRequest(h.mux, h.mw, http.MethodGet, "/login", nil)
	if strings.Contains(plain.Body.String(), "Your password was changed.") {
		t.Error("the banner must not render without its flag")
	}

	if cookie := h.loginCookie(t, "ana@example.com", newPassword); cookie == "" {
		t.Error("the new password must sign in")
	}
	if rec := h.postFormAs(t, "/login", authForm("ana@example.com", oldPassword), ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("the old password must be refused, status = %d", rec.Code)
	}
	for name, session := range map[string]string{"laptop": laptop, "phone": phone} {
		refused := doRequest(h.mux, h.mw, http.MethodGet, "/tickets", map[string]string{"Cookie": sessionCookie + "=" + session})
		if refused.Code != http.StatusSeeOther {
			t.Errorf("%s session status = %d, want 303 after the reset", name, refused.Code)
		}
		if location := refused.Header().Get("Location"); location != "/login" {
			t.Errorf("%s session Location = %q, want /login", name, location)
		}
	}
}

// TestResetRefusesAMistypedConfirmation proves a confirmation that does not
// match answers 422 on that field, writes nothing, and leaves the link usable.
func TestResetRefusesAMistypedConfirmation(t *testing.T) {
	h := newHarness(t)
	h.createUser(t, "Ana", "ana@example.com", "s3cret-pass-12")
	configureEmailDelivery(t, h)
	const oldPassword = "s3cret-pass-12"
	token := requestResetLink(t, h, "ana@example.com")

	rec := resetForm(t, h, token, "recovered-pass-12", "recovered-pass-13")
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		"the new passwords do not match",
		`id="confirm_password" name="confirm_password" autocomplete="new-password" aria-invalid="true"`,
		`<input type="hidden" name="token" value="` + token + `">`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("refused form must contain %q, got: %.1500s", want, body)
		}
	}
	if rec := h.postFormAs(t, "/login", authForm("ana@example.com", oldPassword), ""); rec.Code != http.StatusSeeOther {
		t.Error("a refused confirmation must not change the password")
	}

	// The link is still usable after the refusal.
	if again := resetForm(t, h, token, "recovered-pass-12", "recovered-pass-12"); again.Code != http.StatusSeeOther {
		t.Errorf("the link must still work after a refused confirmation, status = %d", again.Code)
	}
}

// TestResetRefusesAShortPasswordAndKeepsTheLink proves the instance minimum is
// enforced on the public form (S4) with its own message, beside the field it
// belongs to, and that a too-short submission leaves the link usable.
func TestResetRefusesAShortPasswordAndKeepsTheLink(t *testing.T) {
	h := newHarness(t)
	h.createUser(t, "Ana", "ana@example.com", "s3cret-pass-12")
	configureEmailDelivery(t, h)
	const oldPassword = "s3cret-pass-12"
	token := requestResetLink(t, h, "ana@example.com")

	rec := resetForm(t, h, token, "too-short", "too-short")
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		"the password must be at least 12 characters long",
		`id="new_password" name="new_password" autocomplete="new-password" aria-describedby="password-min-help" aria-invalid="true"`,
		`<input type="hidden" name="token" value="` + token + `">`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("refused form must contain %q, got: %.1500s", want, body)
		}
	}
	if rec := h.postFormAs(t, "/login", authForm("ana@example.com", oldPassword), ""); rec.Code != http.StatusSeeOther {
		t.Error("a refused password must not change the password")
	}

	if again := resetForm(t, h, token, "recovered-pass-12", "recovered-pass-12"); again.Code != http.StatusSeeOther {
		t.Errorf("the link must still work after a refused password, status = %d", again.Code)
	}
}

// TestResetRefusesASpentLink proves a second submission of the same link is
// refused and changes nothing: the password the first submission stored still
// signs in and the one the refused attempt carried does not.
func TestResetRefusesASpentLink(t *testing.T) {
	h := newHarness(t)
	h.createUser(t, "Ana", "ana@example.com", "s3cret-pass-12")
	configureEmailDelivery(t, h)
	token := requestResetLink(t, h, "ana@example.com")

	if rec := resetForm(t, h, token, "recovered-pass-12", "recovered-pass-12"); rec.Code != http.StatusSeeOther {
		t.Fatalf("first reset status = %d, want 303", rec.Code)
	}
	rec := resetForm(t, h, token, "second-attempt-45", "second-attempt-45")
	if rec.Code != http.StatusGone {
		t.Fatalf("reused link status = %d, want 410: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "This link is no longer valid") {
		t.Errorf("reused link must render the refusal state, got: %.1500s", rec.Body.String())
	}
	assertNoTokenLeak(t, rec, token)
	if cookie := h.loginCookie(t, "ana@example.com", "recovered-pass-12"); cookie == "" {
		t.Error("the stored password must still sign in")
	}
	if rec := h.postFormAs(t, "/login", authForm("ana@example.com", "second-attempt-45"), ""); rec.Code != http.StatusUnauthorized {
		t.Error("a refused reset must not store its password")
	}
}

// TestRecoveryPagesAreNotCacheableAndKeepTheTokenInsideTheSite proves the two
// public pages answer same-origin and no-store on every state they render: the
// token sits in the URL, so no cache may keep the page and no other origin may
// receive the link as a referrer, while the pages themselves load nothing from
// another origin.
func TestRecoveryPagesAreNotCacheableAndKeepTheTokenInsideTheSite(t *testing.T) {
	h := newHarness(t)
	h.createUser(t, "Ana", "ana@example.com", "s3cret-pass-12")
	configureEmailDelivery(t, h)
	token := requestResetLink(t, h, "ana@example.com")

	cases := map[string]*httptest.ResponseRecorder{
		"forgot form":         doRequest(h.mux, h.mw, http.MethodGet, "/login/forgot", nil),
		"forgot confirmation": forgot(t, h, "nobody@example.com"),
		"reset form":          doRequest(h.mux, h.mw, http.MethodGet, "/login/reset?token="+token, nil),
		"reset refusal":       doRequest(h.mux, h.mw, http.MethodGet, "/login/reset?token=dead", nil),
		// The states a submission reaches, not only the pages a GET renders.
		"reset 422 mismatch":  resetForm(t, h, token, "recovered-pass-12", "other-pass-12"),
		"reset 422 short":     resetForm(t, h, token, "short", "short"),
		"reset 410 dead POST": resetForm(t, h, "dead", "recovered-pass-12", "recovered-pass-12"),
		"reset 410 no token":  doRequest(h.mux, h.mw, http.MethodGet, "/login/reset", nil),
	}
	// An instance that offers no recovery answers 404 on both routes and still
	// carries the pair, because the refusal must not become cacheable either.
	setRecoverySwitch(t, h, false)
	cases["forgot 404"] = doRequest(h.mux, h.mw, http.MethodGet, "/login/forgot", nil)
	cases["reset 404"] = doRequest(h.mux, h.mw, http.MethodGet, "/login/reset?token="+token, nil)
	for name, rec := range cases {
		if got := rec.Header().Get("Referrer-Policy"); got != "same-origin" {
			t.Errorf("%s: Referrer-Policy = %q, want same-origin", name, got)
		}
		if got := rec.Header().Get("Cache-Control"); got != "no-store" {
			t.Errorf("%s: Cache-Control = %q, want no-store", name, got)
		}
	}
}

// TestRecoveryPagesRenderSpanishCopy proves both pages are catalogue-rendered
// (S10) and that the Spanish copy is the shipped one.
func TestRecoveryPagesRenderSpanishCopy(t *testing.T) {
	h := newHarness(t)
	h.createUser(t, "Ana", "ana@example.com", "s3cret-pass-12")
	configureEmailDelivery(t, h)
	if err := h.store.SettingsStore().SetInterfaceLanguage(t.Context(), application.InterfaceLanguageSpanish); err != nil {
		t.Fatalf("set interface language: %v", err)
	}
	token := requestResetLink(t, h, "ana@example.com")

	signIn := doRequest(h.mux, h.mw, http.MethodGet, "/login", nil).Body.String()
	if !strings.Contains(signIn, `<a class="auth-link" href="/login/forgot">¿Olvidaste tu contraseña?</a>`) {
		t.Errorf("the Spanish sign-in page must offer the link in Spanish, got: %.1200s", signIn)
	}

	forgotForm := doRequest(h.mux, h.mw, http.MethodGet, "/login/forgot", nil).Body.String()
	for _, want := range []string{"¿Olvidaste tu contraseña?", "Enviar enlace", "Volver a iniciar sesión"} {
		if !strings.Contains(forgotForm, want) {
			t.Errorf("Spanish forgot page must contain %q, got: %.1500s", want, forgotForm)
		}
	}
	confirmation := forgot(t, h, "nobody@example.com").Body.String()
	for _, want := range []string{"Revisa tu correo", "Si existe una cuenta con ese correo, te enviamos un enlace."} {
		if !strings.Contains(confirmation, want) {
			t.Errorf("Spanish confirmation must contain %q, got: %.1500s", want, confirmation)
		}
	}
	reset := doRequest(h.mux, h.mw, http.MethodGet, "/login/reset?token="+token, nil).Body.String()
	for _, want := range []string{"Elige una contraseña nueva", "Al menos 12 caracteres."} {
		if !strings.Contains(reset, want) {
			t.Errorf("Spanish reset page must contain %q, got: %.1500s", want, reset)
		}
	}
	refusal := doRequest(h.mux, h.mw, http.MethodGet, "/login/reset?token=dead", nil).Body.String()
	for _, want := range []string{"Este enlace ya no es válido", "Pedir un enlace nuevo"} {
		if !strings.Contains(refusal, want) {
			t.Errorf("Spanish refusal must contain %q, got: %.1500s", want, refusal)
		}
	}
}

// TestRecoveryRoutesStayUnderThePublicLoginBranch documents the existing session
// rule the recovery pages inherit: /login/* carries no session requirement, so a
// signed-out visitor reaches them, and a signed-in visitor is still sent to
// /tickets exactly as the sign-in page already does.
func TestRecoveryRoutesStayUnderThePublicLoginBranch(t *testing.T) {
	h := newHarness(t)
	configureEmailDelivery(t, h)

	for _, path := range []string{"/login/forgot", "/login/reset?token=abc"} {
		signedOut := doRequest(h.mux, h.mw, http.MethodGet, path, nil)
		if signedOut.Code == http.StatusSeeOther {
			t.Errorf("GET %s must be public, got %d", path, signedOut.Code)
		}
		signedIn := h.get(t, path, false)
		wantRedirect(t, signedIn, http.StatusSeeOther, "/tickets")
	}
}

// TestForgotRefusesACrossSiteSubmission proves the Origin gate covers the public
// POST (D17): a cross-site submission is refused before anything is read, and no
// message leaves the instance.
func TestForgotRefusesACrossSiteSubmission(t *testing.T) {
	h := newHarness(t)
	h.createUser(t, "Ana", "ana@example.com", "s3cret-pass-12")
	configureEmailDelivery(t, h)

	rec := doRequest(h.mux, h.mw, http.MethodPost, "/login/forgot", map[string]string{
		"Origin":       "https://evil.example",
		"Content-Type": "application/x-www-form-urlencoded",
	})
	if rec.Code != http.StatusForbidden {
		t.Errorf("cross-site POST /login/forgot = %d, want 403", rec.Code)
	}
	if len(h.mailer.sent) != 0 {
		t.Errorf("a refused submission mailed %d messages, want 0", len(h.mailer.sent))
	}
}

// postRecovery submits one public recovery form through the middleware with the
// headers a browser would send, so a test can name the Origin on the wire.
func postRecovery(t *testing.T, h *harness, path string, form url.Values, hdr map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.mw.Wrap(h.mux).ServeHTTP(rec, req)
	return rec
}

// TestRecoverySubmissionsRefuseTheNullOrigin proves the two public submissions
// get no exemption from the shared Origin gate (D17): the literal "null" is
// refused exactly like any other foreign origin, nothing is mailed and nothing is
// written, and the link mailed earlier is still usable. A same-origin Origin —
// what a browser sends from the pages as they are served — passes.
func TestRecoverySubmissionsRefuseTheNullOrigin(t *testing.T) {
	h := newHarness(t)
	h.createUser(t, "Ana", "ana@example.com", "s3cret-pass-12")
	configureEmailDelivery(t, h)
	const oldPassword = "s3cret-pass-12"
	token := requestResetLink(t, h, "ana@example.com")
	mailed := len(h.mailer.sent)

	forms := map[string]struct {
		path string
		form url.Values
	}{
		"POST /login/forgot": {path: "/login/forgot", form: url.Values{"email": {"ana@example.com"}}},
		"POST /login/reset": {path: "/login/reset", form: url.Values{
			passwordResetTokenField: {token},
			passwordFieldNew:        {"recovered-pass-12"},
			passwordFieldConfirm:    {"recovered-pass-12"},
		}},
	}
	for name, tc := range forms {
		rec := postRecovery(t, h, tc.path, tc.form, map[string]string{"Origin": "null"})
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s with Origin null = %d, want 403", name, rec.Code)
		}
	}
	if len(h.mailer.sent) != mailed {
		t.Errorf("a refused submission mailed %d messages, want none", len(h.mailer.sent)-mailed)
	}

	// Nothing changed: the mailed link still opens the form and the password
	// those submissions carried was never stored.
	if rec := doRequest(h.mux, h.mw, http.MethodGet, "/login/reset?token="+token, nil); rec.Code != http.StatusOK {
		t.Errorf("GET /login/reset after the refusals = %d, want the form again", rec.Code)
	}
	if rec := h.postFormAs(t, "/login", authForm("ana@example.com", oldPassword), ""); rec.Code != http.StatusSeeOther {
		t.Error("a refused submission must not change the password")
	}
	if rec := h.postFormAs(t, "/login", authForm("ana@example.com", "recovered-pass-12"), ""); rec.Code != http.StatusUnauthorized {
		t.Error("the password a refused submission carried must not sign in")
	}

	// What the browser really sends from these pages: their own origin.
	forgotRec := postRecovery(t, h, "/login/forgot", url.Values{"email": {"ana@example.com"}}, map[string]string{"Origin": "https://example.com"})
	if forgotRec.Code != http.StatusOK {
		t.Errorf("POST /login/forgot from the same origin = %d, want 200", forgotRec.Code)
	}
	if !strings.Contains(forgotRec.Body.String(), "Check your email") {
		t.Errorf("the same-origin submission must reach the neutral confirmation, got: %.1500s", forgotRec.Body.String())
	}
	resetRec := postRecovery(t, h, "/login/reset", forms["POST /login/reset"].form, map[string]string{"Origin": "https://example.com"})
	wantRedirect(t, resetRec, http.StatusSeeOther, "/login?password_changed=1")
}

// recoveryDatabaseDigest answers a digest of the database file, so a refused
// submission can be proved to have written nothing at all — not only "not the
// row the test looked at".
func recoveryDatabaseDigest(t *testing.T, h *harness) string {
	t.Helper()
	body, err := os.ReadFile(h.dbPath)
	if err != nil {
		t.Fatalf("read the database file: %v", err)
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// pendingResetLinks counts the links still waiting to be redeemed. No store port
// exposes the count, so this is the one raw read the recovery tests need.
func pendingResetLinks(t *testing.T, h *harness) int {
	t.Helper()
	var count int
	if err := h.rawDB(t).QueryRow(`SELECT COUNT(*) FROM password_reset_tokens WHERE used_at IS NULL`).Scan(&count); err != nil {
		t.Fatalf("count pending links: %v", err)
	}
	return count
}

// TestRecoveryOriginGateLeavesTheDatabaseUntouched proves the shared Origin gate
// (D17) on both public submissions with the evidence a refusal owes: the database
// file is byte-identical, the pending links are the same, nothing was mailed, and
// the answers were 403 — for the literal "null" a browser sends from a
// no-referrer document and for a foreign origin alike. The page's own origin is
// then accepted and does reset the password.
func TestRecoveryOriginGateLeavesTheDatabaseUntouched(t *testing.T) {
	h := newHarness(t)
	const oldPassword = "s3cret-pass-12"
	h.createUser(t, "Ana", "ana@example.com", oldPassword)
	configureEmailDelivery(t, h)
	token := requestResetLink(t, h, "ana@example.com")

	digestBefore := recoveryDatabaseDigest(t, h)
	linksBefore := pendingResetLinks(t, h)
	mailedBefore := len(h.mailer.sent)
	resetValues := url.Values{
		passwordResetTokenField: {token},
		passwordFieldNew:        {"recovered-pass-12"},
		passwordFieldConfirm:    {"recovered-pass-12"},
	}

	for _, origin := range []string{"null", "https://evil.example"} {
		for name, request := range map[string]struct {
			path string
			form url.Values
		}{
			"POST /login/forgot": {path: "/login/forgot", form: url.Values{"email": {"ana@example.com"}}},
			"POST /login/reset":  {path: "/login/reset", form: resetValues},
		} {
			rec := postRecovery(t, h, request.path, request.form, map[string]string{"Origin": origin})
			if rec.Code != http.StatusForbidden {
				t.Errorf("%s with Origin %q = %d, want 403", name, origin, rec.Code)
			}
		}
	}

	if got := recoveryDatabaseDigest(t, h); got != digestBefore {
		t.Error("a refused submission changed the database file")
	}
	if got := pendingResetLinks(t, h); got != linksBefore {
		t.Errorf("pending links = %d, want %d after the refusals", got, linksBefore)
	}
	if len(h.mailer.sent) != mailedBefore {
		t.Error("a refused submission mailed a message")
	}

	// The origin the pages themselves are served from passes, and the link works.
	wantRedirect(t, postRecovery(t, h, "/login/reset", resetValues, map[string]string{"Origin": "https://example.com"}),
		http.StatusSeeOther, "/login?password_changed=1")
	if h.loginCookie(t, "ana@example.com", "recovered-pass-12") == "" {
		t.Error("the same-origin submission must store the new password")
	}
}

// TestResetLinkStopsWorkingAfterOneHour proves S3's boundary at the surface: a
// link read twice still renders the form, still renders it one minute before the
// hour is up, and is refused as soon as the hour has passed — on the page and on
// the submission alike, with the stored password untouched.
func TestResetLinkStopsWorkingAfterOneHour(t *testing.T) {
	h := newHarness(t)
	const oldPassword = "s3cret-pass-12"
	h.createUser(t, "Ana", "ana@example.com", oldPassword)
	configureEmailDelivery(t, h)
	token := requestResetLink(t, h, "ana@example.com")

	for read := 0; read < 2; read++ {
		if rec := doRequest(h.mux, h.mw, http.MethodGet, "/login/reset?token="+token, nil); rec.Code != http.StatusOK {
			t.Fatalf("GET /login/reset read %d = %d, want the form", read+1, rec.Code)
		}
	}
	h.clock.Advance(59 * time.Minute)
	if rec := doRequest(h.mux, h.mw, http.MethodGet, "/login/reset?token="+token, nil); rec.Code != http.StatusOK {
		t.Errorf("GET /login/reset at 59 minutes = %d, want the form", rec.Code)
	}

	h.clock.Advance(2 * time.Minute)
	if rec := doRequest(h.mux, h.mw, http.MethodGet, "/login/reset?token="+token, nil); rec.Code != http.StatusGone {
		t.Errorf("GET /login/reset at 61 minutes = %d, want the refusal state", rec.Code)
	}
	if rec := resetForm(t, h, token, "recovered-pass-12", "recovered-pass-12"); rec.Code != http.StatusGone {
		t.Errorf("POST /login/reset at 61 minutes = %d, want the refusal state", rec.Code)
	}
	if h.loginCookie(t, "ana@example.com", oldPassword) == "" {
		t.Error("an expired link must not change the password")
	}
}
