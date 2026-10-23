package i18n

import (
	"strings"
	"testing"
)

// The maintainer's copy decisions (L29/L30) for the Spanish catalogue: Spanish
// says "ticket" for a ticket, the assignment strategies are named as nouns, and
// the settings SLA title carries its (SLA) suffix.

// --- Maintainer copy decisions (S19/S20/S21) ---

// TestSpanishTicketVocabularyAndStrategyNames pins the maintainer's copy
// decisions (L29/L30): Spanish says "ticket" for a ticket (an HTTP request
// keeps "solicitud"), the assignment strategies are named as nouns while the
// agent verb stays, and the settings SLA title carries the (SLA) suffix in both
// locales.
func TestSpanishTicketVocabularyAndStrategyNames(t *testing.T) {
	es := New(Spanish)
	for key, want := range map[string]string{
		"base.nav.tickets":                          "Tickets",
		"auth.login.title":                          "Inicia sesión en tkt",
		"tickets.checklist.meta.requester_received": "Tu ticket se recibió y está en cola para soporte.",
		"tickets.list.user_empty":                   "Todavía no hay tickets",
		"tickets.list.user_no_match":                "Ningún ticket coincide con tu búsqueda",
		"workflow.editor.strategy.claim":            "Autoasignación",
		"workflow.editor.strategy.least_loaded":     "Por menor carga",
		"tickets.agent.claim_ticket":                "Tomar ticket",
		"tickets.agent.claimable":                   "Disponibles para tomar",
		"settings.sla.title":                        "Acuerdos de nivel de servicio (SLA)",
	} {
		if got := es.T(key); got != want {
			t.Errorf("es %q = %q, want %q", key, got, want)
		}
	}
	if got := New(English).T("settings.sla.title"); got != "Service level agreements (SLA)" {
		t.Errorf("en settings.sla.title = %q, want %q", got, "Service level agreements (SLA)")
	}
	// A ticket is never a "solicitud" in Spanish; the three HTTP-request
	// sentences are the only ones allowed to keep the word.
	httpRequestKeys := map[string]bool{
		"error.bad_request":             true,
		"error.request_too_large":       true,
		"users.deactivate_confirm.body": true,
	}
	for key, message := range catalogues[Spanish] {
		if !httpRequestKeys[key] && strings.Contains(strings.ToLower(message), "solicitud") {
			t.Errorf("es %q still says solicitud for a ticket: %q", key, message)
		}
	}
	// The strategy error names the visible strategies, never the identifiers.
	if got := es.Tf("error.workflow.strategy", 1); got != "Paso 1: la estrategia debe ser Autoasignación o Por menor carga" {
		t.Errorf("es strategy error = %q", got)
	}
	if got := New(English).Tf("error.workflow.strategy", 1); got != "Step 1: strategy must be claim or least_loaded" {
		t.Errorf("en strategy error = %q, want the historical bytes", got)
	}
}

// --- Category suggestion keywords (issue #416, S4) ---

// TestCategoryKeywordsCopy pins the copy of the drawer field that feeds the local
// suggestion engine, in both locales. The sentences are fixed: an administrator
// learned the field and its rule from them, so a "clearer" rewrite changes what
// the field says without changing what it does.
func TestCategoryKeywordsCopy(t *testing.T) {
	en := New(English)
	if got, want := en.T("categories.drawer.keywords"), "Keywords for suggestions"; got != want {
		t.Errorf("en label = %q, want %q", got, want)
	}
	if got, want := en.T("categories.drawer.keywords_help"),
		"Words people use for this category. Comma separated; accents and capitals do not matter."; got != want {
		t.Errorf("en help = %q, want the fixed sentence %q", got, want)
	}

	es := New(Spanish)
	if got, want := es.T("categories.drawer.keywords"), "Palabras clave para sugerencias"; got != want {
		t.Errorf("es label = %q, want %q", got, want)
	}
	if got, want := es.T("categories.drawer.keywords_help"),
		"Palabras que la gente usa para esta categoría. Separadas por comas; los acentos y las mayúsculas no importan."; got != want {
		t.Errorf("es help = %q, want %q", got, want)
	}
	// The refusal copy names the bound the server enforces, in both locales.
	for _, locale := range []string{English, Spanish} {
		if got := New(locale).T("error.category_keywords_invalid"); !strings.Contains(got, "500") {
			t.Errorf("%s refusal copy = %q, want it to name the 500-character bound", locale, got)
		}
	}
}
