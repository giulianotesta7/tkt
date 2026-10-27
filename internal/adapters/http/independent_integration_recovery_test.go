package httpadapter

import (
	"crypto/sha256"
	"net/http"
	"net/url"
	"os"
	"testing"
)

// S4/S5/S11: production-style recovery wiring must honor the company switch.
func TestIndependentIntegrationRecoverySwitch(t *testing.T) {
	h := newHarness(t)
	root := seedLoginReadyRoot(t, h)
	configureEmailDeliveryAs(t, h, *root)
	member := ownPasswordUser(t, h, "seam-member@example.com")
	token := requestResetLink(t, h, member.Email)
	// Use the administrator's real public form, including a usable provider.
	rec := h.postForm(t, "/settings/security/company", companySignInForm(func(f url.Values) {
		f.Del("password_signin_enabled")
	}), false)
	wantRedirect(t, rec, http.StatusSeeOther, "/settings/security")
	fingerprint := func() [2][32]byte {
		t.Helper()
		db, err := os.ReadFile(h.dbPath)
		if err != nil {
			t.Fatal(err)
		}
		wal, err := os.ReadFile(h.dbPath + "-wal")
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		return [2][32]byte{sha256.Sum256(db), sha256.Sum256(wal)}
	}
	before, sent := fingerprint(), len(h.mailer.sent)
	rec = forgot(t, h, member.Email)
	if rec.Code != http.StatusOK {
		t.Errorf("neutral forgot status=%d want200", rec.Code)
	}
	if len(h.mailer.sent) != sent {
		t.Error("password-off nonroot recovery sent mail")
	}
	if fingerprint() != before {
		t.Error("password-off nonroot recovery changed DB/WAL")
	}
	rec = resetForm(t, h, token, "seam-replaced-pass-12", "seam-replaced-pass-12")
	if rec.Code != http.StatusGone {
		t.Errorf("password-off nonroot old link status=%d want410; Location=%q", rec.Code, rec.Header().Get("Location"))
	}
	if fingerprint() != before {
		t.Error("password-off nonroot old link changed DB/WAL")
	}
	// Root's break-glass recovery remains usable under the identical switch.
	rootToken := requestResetLink(t, h, root.Email)
	rec = resetForm(t, h, rootToken, rootPasswordNext, rootPasswordNext)
	wantRedirect(t, rec, http.StatusSeeOther, "/login?password_changed=1")
	assertRootIdentityUnchanged(t, h, root)
}

// S5: a first request while password sign-in is off must not create a link.
func TestIndependentIntegrationRecoveryRequestOff(t *testing.T) {
	h := newHarness(t)
	root := seedLoginReadyRoot(t, h)
	configureEmailDeliveryAs(t, h, *root)
	member := ownPasswordUser(t, h, "fresh-seam-member@example.com")
	rec := h.postForm(t, "/settings/security/company", companySignInForm(func(f url.Values) {
		f.Del("password_signin_enabled")
	}), false)
	wantRedirect(t, rec, http.StatusSeeOther, "/settings/security")
	beforeMail, beforeTokens := len(h.mailer.sent), pendingResetLinks(t, h)
	db, err := os.ReadFile(h.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	wal, err := os.ReadFile(h.dbPath + "-wal")
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	rec = forgot(t, h, member.Email)
	if rec.Code != http.StatusOK {
		t.Errorf("neutral forgot status=%d want200", rec.Code)
	}
	if len(h.mailer.sent) != beforeMail || pendingResetLinks(t, h) != beforeTokens {
		t.Errorf("password-off fresh nonroot request: mail delta=%d token delta=%d; want0/0", len(h.mailer.sent)-beforeMail, pendingResetLinks(t, h)-beforeTokens)
	}
	afterDB, err := os.ReadFile(h.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	afterWAL, err := os.ReadFile(h.dbPath + "-wal")
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if sha256.Sum256(db) != sha256.Sum256(afterDB) || sha256.Sum256(wal) != sha256.Sum256(afterWAL) {
		t.Error("password-off fresh nonroot recovery changed DB/WAL")
	}
}
