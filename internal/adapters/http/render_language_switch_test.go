package httpadapter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

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
