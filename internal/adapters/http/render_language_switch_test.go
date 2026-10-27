package httpadapter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
	"github.com/giulianotesta7/tkt/internal/i18n"
)

func TestPersonalLanguageIntegration(t *testing.T) {
	h := newHarness(t)
	store := h.store.PreferencesStore().(application.LanguagePreferencesStore)
	if wired, ok := any(h.mw).(interface {
		WithLanguagePreferences(application.LanguagePreferencesStore) *SessionMiddleware
	}); ok {
		wired.WithLanguagePreferences(store)
	}
	other := seedUserRole(t, h.store, "Other", "locale-other@tkt.test", domain.RoleAgent)
	session := seedSession(t, h.store, other.ID)
	if err := store.SetLanguage(t.Context(), other.ID, "es"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, cookie, path, marker string }{
		{"personal page", session.ID, "/preferences", `<html lang="es">`},
		{"inherited page", h.adminSession.ID, "/preferences", `<html lang="en">`},
		{"anonymous login", "", "/login", `<html lang="en">`},
		{"personal script", session.ID, "/static/users.js", i18n.Script("es")},
		{"inherited script", h.adminSession.ID, "/static/users.js", i18n.Script("en")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := doRequest(h.mux, h.mw, http.MethodGet, tc.path, map[string]string{"Cookie": sessionCookie + "=" + tc.cookie})
			if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), tc.marker) {
				t.Errorf("status %d: missing locale marker", rec.Code)
			}
			if strings.HasPrefix(tc.path, "/static/") && tc.cookie != "" && rec.Header().Get("Cache-Control") != "private, no-store" {
				t.Errorf("cache control = %q", rec.Header().Get("Cache-Control"))
			}
		})
	}
	if err := store.SetLanguage(t.Context(), h.admin.ID, "en"); err != nil {
		t.Fatal(err)
	}
	rec := h.postForm(t, "/settings/language", url.Values{"interface_language": {"es"}}, false)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("settings save: %d", rec.Code)
	}
	if body := h.get(t, "/settings/language", false).Body.String(); !strings.Contains(body, `<html lang="en">`) {
		t.Error("settings changed personal language")
	}
	rec = h.postForm(t, "/preferences/language", url.Values{"language": {"es"}}, false)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("preference save: %d", rec.Code)
	}
	req := httptest.NewRequest(http.MethodGet, "/preferences", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: h.adminSession.ID})
	for _, c := range rec.Result().Cookies() {
		req.AddCookie(c)
	}
	page := httptest.NewRecorder()
	h.mw.Wrap(h.mux).ServeHTTP(page, req)
	if !strings.Contains(page.Body.String(), `data-feedback-message="Guardado"`) {
		t.Error("preference feedback not Spanish")
	}
}

// mutableInterfaceLanguage stands in for the instance settings store and lets a
// test change the active language between two renders, proving the renderer
// resolves it per request instead of caching the first one (issue #335, S9/S15).
type mutableInterfaceLanguage struct{ language string }

func (m *mutableInterfaceLanguage) GetInterfaceLanguage(context.Context) (string, error) {
	return m.language, nil
}

func TestRenderAppliesLanguageChangesWithoutRebuilding(t *testing.T) {
	src := &mutableInterfaceLanguage{language: "en"}
	r, err := newRendererWithLanguage(localizedShellFS(t), src)
	if err != nil {
		t.Fatalf("newRendererWithLanguage: %v", err)
	}
	render := func() string {
		rec := httptest.NewRecorder()
		r.Render(rec, httptest.NewRequest(http.MethodGet, "/login", nil), "login", "", loginData{}, http.StatusOK)
		return rec.Body.String()
	}
	before := render()
	if !strings.Contains(before, `<html lang="en">`) || !strings.Contains(before, "you@example.com") {
		t.Fatalf("first render must be English, got: %s", firstLine(before))
	}
	src.language = "es"
	after := render()
	if !strings.Contains(after, `<html lang="es">`) {
		t.Errorf("after changing the setting, lang must follow it, got: %s", firstLine(after))
	}
	if !strings.Contains(after, "tu@ejemplo.com") {
		t.Errorf("after changing the setting, copy must follow it, got: %s", after)
	}
	if strings.Contains(after, "you@example.com") {
		t.Errorf("after changing the setting, the previous language must not linger, got: %s", after)
	}
}
