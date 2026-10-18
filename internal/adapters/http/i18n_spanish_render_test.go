package httpadapter

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/web/templates"
)

// Spanish rendering per area (issue #335, S8): a Spanish instance renders the
// catalogue of every migrated surface, not a half-translated page. Each case
// names copy a user reads, so a surface that regresses to English fails here.

// renderLocalized renders one page or fragment in an explicit locale through
// the real embedded template tree.
func renderLocalized(t *testing.T, locale, page, fragment string, data any) string {
	t.Helper()
	r, err := newRendererWithLanguage(templates.FS, stubInterfaceLanguage{language: locale})
	if err != nil {
		t.Fatalf("newRendererWithLanguage(%q): %v", locale, err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/localized", nil)
	req.Header.Set("HX-Request", "true")
	r.Render(rec, req, page, fragment, data, http.StatusOK)
	if rec.Code != http.StatusOK {
		t.Fatalf("render %s/%s in %q: status %d body %s", page, fragment, locale, rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

// TestSpanishTicketsQueue renders the staff queue fragments in Spanish: the
// table headers, the priority label from the catalogue, and the empty state.
func TestSpanishTicketsQueue(t *testing.T) {
	body := renderLocalized(t, "es", "tickets_index", "ticket_list", fixtureListData())
	for _, want := range []string{"Título", "Prioridad", "Actualizado", "Alta"} {
		if !strings.Contains(body, want) {
			t.Errorf("Spanish queue must render %q, got: %.800s", want, body)
		}
	}
	for _, unwanted := range []string{">Title<", ">Priority<", ">Updated<", ">High<"} {
		if strings.Contains(body, unwanted) {
			t.Errorf("Spanish queue must not render the English %q, got: %.800s", unwanted, body)
		}
	}
}

// TestSpanishFilterBarAndPagination renders the filter bar and the pagination
// summary in Spanish, including the interpolated counts.
func TestSpanishFilterBarAndPagination(t *testing.T) {
	list := fixtureListData()
	filters := renderLocalized(t, "es", "tickets_index", "filter_form", list)
	for _, want := range []string{"Todos los estados", "Todas las prioridades", "Cualquiera", "Ordenar por", "Aplicar", "Limpiar filtros"} {
		if !strings.Contains(filters, want) {
			t.Errorf("Spanish filter bar must render %q, got: %.1200s", want, filters)
		}
	}

	list.Pages = 3
	list.Page = 2
	list.Total = 25
	list.PrevHref = "/tickets?page=1"
	list.NextHref = "/tickets?page=3"
	pagination := renderLocalized(t, "es", "tickets_index", "pagination", list)
	if !strings.Contains(pagination, "Página 2 de 3 · 25 tickets") {
		t.Errorf("Spanish pagination summary is wrong: %.600s", pagination)
	}
	if !strings.Contains(pagination, "← Anterior") || !strings.Contains(pagination, "Siguiente →") {
		t.Errorf("Spanish pagination controls are wrong: %.600s", pagination)
	}
}

// TestSpanishTicketDetail renders the staff detail surface in Spanish: the
// property labels, the state controls, and the reopen-reason field.
func TestSpanishTicketDetail(t *testing.T) {
	data := fixtureDetailData()
	body := renderLocalized(t, "es", "tickets_show", "ticket_detail", data)
	for _, want := range []string{"Propiedades", "Solicitante", "Prioridad", "Asignación", "Responsable", "Estado", "Mover a", "Actividad"} {
		if !strings.Contains(body, want) {
			t.Errorf("Spanish ticket detail must render %q, got: %.800s", want, body)
		}
	}
	if strings.Contains(body, ">Properties<") || strings.Contains(body, ">Timeline<") {
		t.Errorf("Spanish ticket detail must not render the English labels: %.800s", body)
	}
}

// TestSpanishTicketForm renders the ticket creation form in Spanish.
func TestSpanishTicketForm(t *testing.T) {
	body := renderLocalized(t, "es", "tickets_new", "ticket_form", fixtureTicketFormData())
	for _, want := range []string{"Crear un ticket", "Describe tu solicitud.", "Título", "Descripción", "Crear ticket"} {
		if !strings.Contains(body, want) {
			t.Errorf("Spanish ticket form must render %q, got: %.800s", want, body)
		}
	}
}

// TestSpanishAuthShell renders the login page in Spanish: the auth shell copy
// and the form labels both come from the catalogue.
func TestSpanishAuthShell(t *testing.T) {
	r, err := newRendererWithLanguage(templates.FS, stubInterfaceLanguage{language: "es"})
	if err != nil {
		t.Fatalf("newRendererWithLanguage: %v", err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	r.Render(rec, req, "login", "", loginData{}, http.StatusOK)

	body := rec.Body.String()
	for _, want := range []string{`<html lang="es">`, "Te damos la bienvenida a tkt", "Inicia sesión en tkt", "Correo electrónico", "Contraseña", "Iniciar sesión"} {
		if !strings.Contains(body, want) {
			t.Errorf("Spanish login page must render %q, got: %.600s", want, body)
		}
	}
	if strings.Contains(body, "Welcome to tkt") || strings.Contains(body, ">Sign in<") {
		t.Errorf("Spanish login page must not render English copy")
	}
}

// TestLocalizedScriptCarriesTheInstanceLanguage proves the browser scripts get
// their copy from the server (S8's JS half): a Spanish instance is served the
// Spanish catalogue as the window.tktI18n prologue, and the script itself holds
// no English message.
func TestLocalizedScriptCarriesTheInstanceLanguage(t *testing.T) {
	serve := func(locale string) string {
		mux := http.NewServeMux()
		RegisterStatic(mux, stubInterfaceLanguage{language: locale})
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/static/save-feedback.js", nil)
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET save-feedback.js (%s) = %d", locale, rec.Code)
		}
		return rec.Body.String()
	}

	english := serve("en")
	if !strings.Contains(english, "Unable to save changes. Please try again.") {
		t.Errorf("English script prologue must carry the English message")
	}
	spanish := serve("es")
	if !strings.Contains(spanish, "No se pudieron guardar los cambios. Intenta de nuevo.") {
		t.Errorf("Spanish script prologue must carry the Spanish message")
	}
	if strings.Contains(spanish, "Unable to save changes") {
		t.Errorf("Spanish script must not carry the English message")
	}
	for _, body := range []string{english, spanish} {
		if !strings.Contains(body, "window.tktI18n") {
			t.Errorf("every localized script must ship the window.tktI18n prologue")
		}
		if strings.Contains(body, `= "Unable to save changes`) {
			t.Errorf("the script must not keep the English literal: %.400s", body[:200])
		}
	}
}
