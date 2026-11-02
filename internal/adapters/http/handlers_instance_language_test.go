package httpadapter

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/version"
)

func TestInstanceAboutShowsRunningReleaseReadOnly(t *testing.T) {
	previous := version.Release
	version.Release = "v1.2.3"
	t.Cleanup(func() { version.Release = previous })
	h := newHarness(t)
	_, root := seedRoot(t, h)
	body := sectionBodyFor(t, h, "/settings/instance", root.ID)
	if !strings.Contains(body, "<p>v1.2.3</p>") || strings.Contains(body, `name="version"`) {
		t.Error("About must display the running release as read-only text")
	}
}

func TestInstanceLanguageRoutesAndAbout(t *testing.T) {
	h := newHarness(t)
	_, root := seedRoot(t, h)
	body := sectionBodyFor(t, h, "/settings/instance", root.ID)
	for _, want := range []string{`action="/settings/instance/language"`, `name="interface_language"`, "About", "tkt version"} {
		if !strings.Contains(body, want) {
			t.Errorf("Instance missing %q", want)
		}
	}
	if strings.Contains(body, `href="/settings/language"`) {
		t.Error("obsolete Language navigation still visible")
	}
	wantRedirect(t, getAs(t, h, "/settings/language", root.ID), http.StatusSeeOther, "/settings/instance")
	form := url.Values{"interface_language": {"es"}}
	for _, session := range []string{root.ID, h.adminSession.ID} {
		rec := h.postFormAs(t, "/settings/language", form, session)
		if rec.Code != http.StatusMethodNotAllowed && rec.Code != http.StatusNotFound {
			t.Errorf("obsolete POST = %d, want 404 or 405", rec.Code)
		}
	}
	if rec := h.postForm(t, "/settings/instance/language", form, false); rec.Code != http.StatusForbidden {
		t.Errorf("admin new language POST = %d", rec.Code)
	}
	language, err := h.store.SettingsStore().GetInterfaceLanguage(t.Context())
	if err != nil || language != "en" {
		t.Fatalf("denied/obsolete writes changed language = %q, %v", language, err)
	}
	wantRedirect(t, h.postFormAs(t, "/settings/instance/language", form, root.ID), http.StatusSeeOther, "/settings/instance")
	body = sectionBodyFor(t, h, "/settings/instance", root.ID)
	if !strings.Contains(body, `<html lang="es">`) || !strings.Contains(body, `value="es" checked`) {
		t.Error("saved instance language does not render")
	}
}
