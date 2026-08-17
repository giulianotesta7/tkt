package httpadapter

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
)

// Instance appearance settings (handlers_settings.go): GET /settings is
// admin/root-only and shows the current color; POST /settings/appearance
// persists a valid color, rejects an invalid one with an inline error, and
// the shell CSS follows the stored value.

func TestSettingsIndexRequiresAdmin(t *testing.T) {
	h := newHarness(t)
	user, err := h.users.Create(t.Context(), *h.admin, application.CreateUserInput{Name: "User", Email: "user@tkt.test", Password: "secret"})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	session := h.loginCookie(t, user.Email, "secret")
	if session == "" {
		t.Fatal("user login must succeed")
	}

	req := httptest.NewRequest(http.MethodGet, "/settings", nil)
	req.Header.Set("Cookie", sessionCookie+"="+session)
	rec := httptest.NewRecorder()
	h.mw.Wrap(h.mux).ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("non-admin GET /settings = %d, want 403", rec.Code)
	}
}

func TestSettingsIndexShowsCurrentColor(t *testing.T) {
	h := newHarness(t)

	rec := h.get(t, "/settings", false)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		`href="/settings"`, // rail link present for the admin shell
		`>Settings</h1>`,   // page title
		`Appearance`,       // panel heading
		`name="internal_comment_bg" value="#E8EEFF"`,
		`--internal-comment-bg:#E8EEFF;`, // shell CSS carries the seeded default
	} {
		if !strings.Contains(body, want) {
			t.Errorf("settings page must contain %q, got: %s", want, body)
		}
	}
}

func TestSettingsUpdatePersistsAndRedirects(t *testing.T) {
	h := newHarness(t)

	rec := h.postForm(t, "/settings/appearance", url.Values{"internal_comment_bg": {"#E2F2EA"}}, false)

	wantRedirect(t, rec, http.StatusSeeOther, "/settings")

	got, err := h.store.SettingsStore().GetInternalCommentBg(t.Context())
	if err != nil {
		t.Fatalf("read stored color: %v", err)
	}
	if got != "#E2F2EA" {
		t.Errorf("stored bg = %q, want %q", got, "#E2F2EA")
	}

	// The following GET renders the new color as selected AND in the CSS.
	rec = h.get(t, "/settings", false)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`value="#E2F2EA" checked`,
		`--internal-comment-bg:#E2F2EA;`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("settings page after update must contain %q, got: %s", want, body)
		}
	}
}

func TestSettingsUpdateRejectsInvalidColor(t *testing.T) {
	h := newHarness(t)

	rec := h.postForm(t, "/settings/appearance", url.Values{"internal_comment_bg": {"#123456"}}, false)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `error-banner`) {
		t.Errorf("invalid color must render the inline error banner, got: %s", rec.Body.String())
	}

	got, err := h.store.SettingsStore().GetInternalCommentBg(t.Context())
	if err != nil {
		t.Fatalf("read stored color: %v", err)
	}
	if got != "#E8EEFF" {
		t.Errorf("bg = %q after rejected update, want %q", got, "#E8EEFF")
	}
}

// TestSettingsUpdateDeniedForNonAdmin proves the POST is gated on the same
// capability as the page (server-side check, not markup hiding).
func TestSettingsUpdateDeniedForNonAdmin(t *testing.T) {
	h := newHarness(t)
	user, err := h.users.Create(t.Context(), *h.admin, application.CreateUserInput{Name: "User", Email: "user@tkt.test", Password: "secret"})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	session := h.loginCookie(t, user.Email, "secret")
	if session == "" {
		t.Fatal("user login must succeed")
	}

	req := httptest.NewRequest(http.MethodPost, "/settings/appearance", strings.NewReader(url.Values{"internal_comment_bg": {"#E2F2EA"}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Cookie", sessionCookie+"="+session)
	rec := httptest.NewRecorder()
	h.mw.Wrap(h.mux).ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("non-admin POST /settings/appearance = %d, want 403", rec.Code)
	}
	got, err := h.store.SettingsStore().GetInternalCommentBg(t.Context())
	if err != nil {
		t.Fatalf("read stored color: %v", err)
	}
	if got != "#E8EEFF" {
		t.Errorf("bg = %q after denied update, want %q", got, "#E8EEFF")
	}
}

// TestSettingsRailLinkAdminOnly proves the sidebar link renders only for
// admin/root shells (the capability flag drives it, matching the server
// gate).
func TestSettingsRailLinkAdminOnly(t *testing.T) {
	admin := fixtureUsersIndexData()
	admin.CanManageUsers = true
	if body := renderGolden(t, "users_index", "", admin, false); !strings.Contains(body, `href="/settings"`) {
		t.Error("admin shell must show the settings rail link")
	}

	user := fixtureUsersIndexData()
	if body := renderGolden(t, "users_index", "", user, false); strings.Contains(body, `href="/settings"`) {
		t.Error("non-admin shell must not show the settings rail link")
	}
}
