package httpadapter

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
)

// TestSpanishNewlyKeyedErrorsFollowTheInstanceLanguage drives the errors the
// test-drive found leaking English through their real HTTP paths under a
// Spanish instance (issue #335, S18). Contributed by the independent verifier.
func TestSpanishNewlyKeyedErrorsFollowTheInstanceLanguage(t *testing.T) {
	h := newHarness(t)
	if err := h.settings.SetInterfaceLanguage(t.Context(), *h.admin, application.InterfaceLanguageSpanish); err != nil {
		t.Fatalf("set interface language: %v", err)
	}
	catalog := application.NewCatalogService(h.store.CatalogStore(), h.store.CategoryStore(), h.clock)
	dep, err := catalog.CreateDepartmentFor(t.Context(), *h.admin, "Soporte", "")
	if err != nil {
		t.Fatalf("create department: %v", err)
	}
	desk, err := catalog.CreateDeskFor(t.Context(), *h.admin, dep.ID, "Mesa")
	if err != nil {
		t.Fatalf("create desk: %v", err)
	}
	tkt := h.seedTicket(t, "probe", func(in *application.CreateTicketInput) { in.CategoryID = h.bugCategory.ID })
	for _, tc := range []struct {
		name, path string
		form       url.Values
		want       string
	}{
		{"empty category name", "/categories", url.Values{"name": {""}, "desk_id": {strconv.FormatInt(desk.ID, 10)}}, "el nombre de la categoría es obligatorio"},
		{"invalid role", "/users/" + strconv.FormatInt(h.admin.ID, 10) + "/edit", url.Values{"name": {"Admin"}, "email": {"admin@tkt.test"}, "role": {"bogus"}}, "rol no válido"},
		{"empty comment body", "/tickets/" + strconv.FormatInt(tkt.ID, 10) + "/comments", url.Values{"body": {""}, "visibility": {"public"}}, "el texto del comentario es obligatorio"},
		{"empty title", "/tickets/" + strconv.FormatInt(tkt.ID, 10) + "/edit", url.Values{"title": {""}}, "el título es obligatorio"},
		{"invalid interface language", "/settings/language", url.Values{"interface_language": {"fr"}}, "idioma de la interfaz no válido"},
		{"unknown timezone", "/settings/calendar", url.Values{"sla_calendar_days": {"1", "2", "3", "4", "5"}, "sla_calendar_start": {"09:00"}, "sla_calendar_end": {"17:00"}, "sla_calendar_timezone": {"Bogus/Zone"}}, "nombre de zona horaria desconocido"},
	} {
		rec := h.postForm(t, tc.path, tc.form, false)
		body := rec.Body.String()
		if !strings.Contains(body, tc.want) {
			t.Errorf("%s: response %d must render %q, got: %.600s", tc.name, rec.Code, tc.want, body)
		}
		if strings.Contains(body, "\"error.") {
			t.Errorf("%s: response rendered a raw key: %.600s", tc.name, body)
		}
	}
	deskMux := http.NewServeMux()
	NewCategoryHandlersWithWorkflows(h.categories, h.workflows, h.renderer, catalog).Register(deskMux)
	deskReq := httptest.NewRequest(http.MethodPost, "/categories/desks", strings.NewReader(url.Values{"department_id": {strconv.FormatInt(dep.ID, 10)}, "name": {""}}.Encode()))
	deskReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	deskReq.Header.Set("Cookie", sessionCookie+"="+h.adminSession.ID)
	deskRec := httptest.NewRecorder()
	h.mw.Wrap(deskMux).ServeHTTP(deskRec, deskReq)
	if body := deskRec.Body.String(); !strings.Contains(body, "el nombre de la mesa es obligatorio") {
		t.Errorf("empty desk name: %d: %.600s", deskRec.Code, body)
	}
	rec := h.get(t, "/tickets/metrics?metrics_start=2026-13-99", false)
	if body := rec.Body.String(); !strings.Contains(body, "las fechas de las métricas deben usar AAAA-MM-DD") {
		t.Errorf("invalid metrics date: %d: %.600s", rec.Code, body)
	}
}
