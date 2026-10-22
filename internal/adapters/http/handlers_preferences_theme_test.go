package httpadapter

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/i18n"
)

// Per-user interface theme (issue #378, S1): the Preferences page posts
// `theme=system|light|dark` natively to POST /preferences/theme, which answers
// the same 303 back to /preferences with the shared save toast the queue-order
// form uses. A value outside the closed set is a 422 that leaves the stored
// theme untouched, and the route writes ONLY the theme key: the sibling
// preferences keep their own values.

// storedTheme reads the persisted theme through the store port that owns it
// (never raw SQL: the port already answers the question).
func storedTheme(t *testing.T, h *harness, userID int64) string {
	t.Helper()
	theme, err := h.store.ThemePreferences().GetTheme(context.Background(), userID)
	if err != nil {
		t.Fatalf("read stored theme for user %d: %v", userID, err)
	}
	return theme
}

// TestPreferencesThemeSavePersistsEveryValue proves the whole closed set round
// trips: each explicit choice is stored for the actor, and every save answers
// the native redirect with the shared save feedback (the same cookie channel
// the queue-order form emits), never a re-rendered page.
func TestPreferencesThemeSavePersistsEveryValue(t *testing.T) {
	h := newHarness(t)

	for _, theme := range application.AllowedThemes() {
		rec := h.postForm(t, "/preferences/theme", url.Values{"theme": {theme}}, false)
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("POST /preferences/theme theme=%q = %d, want %d: %s", theme, rec.Code, http.StatusSeeOther, rec.Body.String())
		}
		if loc := rec.Header().Get("Location"); loc != "/preferences" {
			t.Errorf("POST theme=%q Location = %q, want /preferences", theme, loc)
		}
		if cookie := findCookie(rec, saveFeedbackCookie); cookie == nil || cookie.Value == "" {
			t.Errorf("POST theme=%q issued no %s cookie, want the shared save toast", theme, saveFeedbackCookie)
		}
		if got := storedTheme(t, h, h.admin.ID); got != theme {
			t.Errorf("after saving %q: stored theme = %q", theme, got)
		}
	}
}

// TestPreferencesThemeRejectsUnknownValue proves the closed set: anything other
// than the three documented values re-renders the page with the inline error
// (422) and leaves the stored theme exactly as it was.
func TestPreferencesThemeRejectsUnknownValue(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if err := h.store.ThemePreferences().SetTheme(ctx, h.admin.ID, application.ThemeDark); err != nil {
		t.Fatalf("seed dark theme: %v", err)
	}

	for _, value := range []string{"", "garbage", "Dark", "dark ", "true", "auto"} {
		rec := h.postForm(t, "/preferences/theme", url.Values{"theme": {value}}, false)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("POST theme=%q = %d, want %d", value, rec.Code, http.StatusUnprocessableEntity)
			continue
		}
		body := rec.Body.String()
		if !strings.Contains(body, `role="alert"`) {
			t.Errorf("POST theme=%q rendered no error banner, got: %s", value, body)
		}
		if !strings.Contains(body, i18n.T(i18n.Default, "error.invalid_theme")) {
			t.Errorf("POST theme=%q banner = %q, want %q", value, body, i18n.T(i18n.Default, "error.invalid_theme"))
		}
		if got := storedTheme(t, h, h.admin.ID); got != application.ThemeDark {
			t.Errorf("POST theme=%q changed the stored theme to %q, want %q untouched", value, got, application.ThemeDark)
		}
	}
}

// TestPreferencesThemeRequiresAuthentication proves the route sits behind the
// session gate like every other POST: an anonymous request is redirected to
// /login before the handler runs, and nothing is stored.
func TestPreferencesThemeRequiresAuthentication(t *testing.T) {
	h := newHarness(t)

	rec := doRequest(h.mux, h.mw, http.MethodPost, "/preferences/theme", nil)
	wantRedirect(t, rec, http.StatusSeeOther, "/login")
	if got := storedTheme(t, h, h.admin.ID); got != "" {
		t.Errorf("an unauthenticated POST stored the theme %q, want nothing", got)
	}
}

// TestPreferencesExistingSavesNeverTouchTheTheme proves the sibling preference
// routes the theme form now sits beside are unchanged (PRESERVE): the
// queue-order, email, and sidebar saves still answer what they always answered,
// still write only their own key, and never touch the theme.
func TestPreferencesExistingSavesNeverTouchTheTheme(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if err := h.store.ThemePreferences().SetTheme(ctx, h.admin.ID, application.ThemeLight); err != nil {
		t.Fatalf("seed light theme: %v", err)
	}

	// The page itself is unchanged: it still renders both existing forms and
	// stores nothing by being read.
	body := h.get(t, "/preferences", false).Body.String()
	if !strings.Contains(body, `action="/preferences"`) {
		t.Errorf("the queue-order form is gone, got: %s", body)
	}
	if !strings.Contains(body, `action="/preferences/email"`) {
		t.Errorf("the email form is gone, got: %s", body)
	}
	if got := storedTheme(t, h, h.admin.ID); got != application.ThemeLight {
		t.Errorf("reading the page changed the stored theme to %q", got)
	}

	// An unchecked email checkbox is simply absent from the body: email off.
	rec := h.postForm(t, "/preferences/email", url.Values{}, false)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /preferences/email = %d, want %d: %s", rec.Code, http.StatusSeeOther, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "/preferences" {
		t.Errorf("email Location = %q, want /preferences", loc)
	}
	if storedEmailNotifications(t, h, h.admin.ID) {
		t.Error("the email toggle no longer turns email off")
	}
	if got := storedTheme(t, h, h.admin.ID); got != application.ThemeLight {
		t.Errorf("the email save changed the stored theme to %q", got)
	}

	rec = h.postForm(t, "/preferences", url.Values{"queue_order": {application.QueueOrderPriority}}, false)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /preferences = %d, want %d: %s", rec.Code, http.StatusSeeOther, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "/preferences" {
		t.Errorf("queue-order Location = %q, want /preferences", loc)
	}
	if got := storedQueueOrder(t, h, h.admin.ID); got != application.QueueOrderPriority {
		t.Errorf("stored queue order = %q, want %q", got, application.QueueOrderPriority)
	}
	if got := storedTheme(t, h, h.admin.ID); got != application.ThemeLight {
		t.Errorf("the queue-order save changed the stored theme to %q", got)
	}

	rec = h.postForm(t, "/preferences/sidebar", url.Values{"sidebar": {"collapsed"}}, false)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("POST /preferences/sidebar = %d, want %d: %s", rec.Code, http.StatusNoContent, rec.Body.String())
	}
	if !storedSidebarCollapsed(t, h, h.admin.ID) {
		t.Error("the sidebar toggle no longer collapses the shell")
	}
	if got := storedTheme(t, h, h.admin.ID); got != application.ThemeLight {
		t.Errorf("the sidebar save changed the stored theme to %q", got)
	}
}

// themeCardKeys is the theme card's own copy beyond its three option labels
// (issue #378, S1): the title and the submit label the template renders.
var themeCardKeys = []string{"preferences.theme.title", "preferences.theme.save"}

// TestPreferencesThemeCardCopyResolves proves the copy the theme card needs is
// carried by both catalogues (issue #378, S1): the three option labels plus
// the card title and its submit label.
func TestPreferencesThemeCardCopyResolves(t *testing.T) {
	keys := append(themeCardKeys, themeOptionKeys()...)
	if len(keys) != 5 {
		t.Fatalf("theme card keys = %v, want the three option labels plus the title and the save label", keys)
	}
	for _, locale := range []string{i18n.English, i18n.Spanish} {
		tr := i18n.New(locale)
		for _, key := range keys {
			message, ok := tr.Lookup(key)
			if !ok || message == "" {
				t.Errorf("locale %q does not carry %q", locale, key)
			}
		}
	}
	if en, es := i18n.T(i18n.English, "preferences.theme.title"), i18n.T(i18n.Spanish, "preferences.theme.title"); en == es {
		t.Errorf("the theme card title is identical in both locales (%q), want a translation", en)
	}
}

// themeOptionKeys lists the catalogue keys the theme select's options resolve,
// read from the same table the handler renders, so a label that lost its key
// fails here instead of printing a fallback in production.
func themeOptionKeys() []string {
	options := themeOptions()
	keys := make([]string, 0, len(options))
	for _, option := range options {
		keys = append(keys, option.Label)
	}
	return keys
}
