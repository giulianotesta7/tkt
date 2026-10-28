package httpadapter

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
	"github.com/giulianotesta7/tkt/internal/i18n"
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
	for _, want := range []string{"Ticket", "Categoría", "Asignado", "Prioridad", "Actualizado", "Alta", "Sin asignar"} {
		if !strings.Contains(body, want) {
			t.Errorf("Spanish queue must render %q, got: %.800s", want, body)
		}
	}
	for _, unwanted := range []string{">Category<", ">Assignee<", ">Priority<", ">Updated<", ">High<", ">Unassigned<"} {
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
	for _, want := range []string{"Todos los estados", "Todas las prioridades", "Cualquiera", "Ordenar por", "Filtros", "Orden"} {
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
// property labels, the header actions, and the reopen-reason dialog (issue
// #386, S1/S5: the panel is one Properties card and the state moves to the
// header, so the old "Asignación"/"Estado"/"Mover a" headings are gone).
func TestSpanishTicketDetail(t *testing.T) {
	data := fixtureDetailData()
	body := renderLocalized(t, "es", "tickets_show", "ticket_detail", data)
	for _, want := range []string{"Propiedades", "Solicitante", "Prioridad", "Responsable", "Detalles", "Resolver", "Cancelar", "Actividad"} {
		if !strings.Contains(body, want) {
			t.Errorf("Spanish ticket detail must render %q, got: %.800s", want, body)
		}
	}
	if strings.Contains(body, ">Properties<") || strings.Contains(body, ">Timeline<") {
		t.Errorf("Spanish ticket detail must not render the English labels: %.800s", body)
	}
}

// TestSpanishFollowingAndMentioning renders the follow control, the followers
// row and both mention inputs in Spanish, and the staff-only refusal a
// requester meets on the follow routes (issue #418).
func TestSpanishFollowingAndMentioning(t *testing.T) {
	body := renderLocalized(t, "es", "tickets_show", "ticket_detail", fixtureDetailFollowDataIn(i18n.Spanish))
	for _, want := range []string{"Siguiendo", "Seguidores", "Tú", "Mencionar", "Solo el personal que puede abrir este ticket"} {
		if !strings.Contains(body, want) {
			t.Errorf("Spanish follow surfaces must render %q, got: %.1200s", want, body)
		}
	}
	for _, unwanted := range []string{">Following<", ">Followers<", ">Mention<"} {
		if strings.Contains(body, unwanted) {
			t.Errorf("Spanish follow surfaces must not render the English %q, got: %.1200s", unwanted, body)
		}
	}

	empty := renderLocalized(t, "es", "tickets_show", "comment_form", fixtureDetailMentionsEmptyData())
	for _, want := range []string{
		"Nadie que pueda abrir este ticket coincide.",
		"Escribe @ para mencionar a alguien que puede abrir este ticket.",
	} {
		if !strings.Contains(empty, want) {
			t.Errorf("Spanish empty candidate list must render %q, got: %.1200s", want, empty)
		}
	}

	req := httptest.NewRequest(http.MethodPost, "/tickets/1/follow", nil)
	req = req.WithContext(context.WithValue(req.Context(), ctxKeyInterfaceLanguage{}, "es"))
	if _, got := mapErrorFor(req, domain.NewForbiddenError(application.ErrMsgFollowersStaffOnly)); got != "solo el personal puede seguir un ticket" {
		t.Errorf("Spanish follow refusal = %q", got)
	}
}

// TestSpanishTicketForm renders the ticket creation form in Spanish.
func TestSpanishTicketForm(t *testing.T) {
	body := renderLocalized(t, "es", "tickets_new", "ticket_form", fixtureTicketFormData())
	for _, want := range []string{"Crear un ticket", "Título", "Descripción", "Crear ticket"} {
		if !strings.Contains(body, want) {
			t.Errorf("Spanish ticket form must render %q, got: %.800s", want, body)
		}
	}
	// Issue #380: the heading and the form's own fields say what to do, so the
	// Spanish subtitle is gone too.
	if strings.Contains(body, "Describe tu ticket.") {
		t.Errorf("Spanish ticket form must not render the repeated subtitle, got: %.800s", body)
	}
}

// TestSpanishAuthShell renders the login page in Spanish: the form copy comes
// from the catalogue, and the identity above it is data — the instance name and
// help desk name as they are stored.
func TestSpanishAuthShell(t *testing.T) {
	r, err := newRendererWithLanguage(templates.FS, stubInterfaceLanguage{language: "es"})
	if err != nil {
		t.Fatalf("newRendererWithLanguage: %v", err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	r.Render(rec, req, "login", "", loginData{}, http.StatusOK)

	body := rec.Body.String()
	for _, want := range []string{`<html lang="es">`, "Inicia sesión en tkt", "Correo electrónico", "Contraseña", "Iniciar sesión"} {
		if !strings.Contains(body, want) {
			t.Errorf("Spanish login page must render %q, got: %.600s", want, body)
		}
	}
	if strings.Contains(body, ">Sign in<") || strings.Contains(body, "Welcome to tkt") {
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

// TestSpanishUsersScreen renders the staff user list in Spanish: the columns,
// the status filter, and the role/status labels that come from Go as keys.
func TestSpanishUsersScreen(t *testing.T) {
	body := renderLocalized(t, "es", "users_index", "users_screen", fixtureUsersIndexData())
	for _, want := range []string{"Usuarios", "Nuevo usuario", "Activos", "Desactivados", "Rol", "Estado", "Acciones"} {
		if !strings.Contains(body, want) {
			t.Errorf("Spanish user list must render %q, got: %.800s", want, body)
		}
	}
	for _, unwanted := range []string{">Role<", ">Status<", ">Deactivated<"} {
		if strings.Contains(body, unwanted) {
			t.Errorf("Spanish user list must not render the English %q", unwanted)
		}
	}
}

// TestSpanishUserForm renders the user drawer in Spanish.
func TestSpanishUserForm(t *testing.T) {
	body := renderLocalized(t, "es", "users_new", "user_form", fixtureUserFormData())
	for _, want := range []string{"Nombre", "Correo electrónico", "Rol"} {
		if !strings.Contains(body, want) {
			t.Errorf("Spanish user form must render %q, got: %.600s", want, body)
		}
	}
}

// TestSpanishCategoriesScreen renders the catalog hierarchy in Spanish,
// including a composed workflow badge (the Msg path: key + nested sentence).
func TestSpanishCategoriesScreen(t *testing.T) {
	body := renderLocalized(t, "es", "categories_index", "categories_background", fixtureCategoriesIndexData())
	for _, want := range []string{"Catálogo", "Nuevo departamento", "Nueva mesa", "Nueva categoría", "Estructura del catálogo", "abiertos"} {
		if !strings.Contains(body, want) {
			t.Errorf("Spanish catalog must render %q, got: %.800s", want, body)
		}
	}
	// The fixture's badges compose through the catalogue: "Workflow v1" and
	// "Draft" both speak Spanish on a Spanish instance.
	for _, want := range []string{"Flujo v1", ">Borrador<"} {
		if !strings.Contains(body, want) {
			t.Errorf("Spanish catalog badge must render %q, got: %.1200s", want, body)
		}
	}
	if strings.Contains(body, "Workflow v1") || strings.Contains(body, "No workflow") {
		t.Errorf("Spanish catalog must not render an English badge")
	}
}

// TestSpanishSettingsScreen renders the instance settings sections in Spanish,
// including the color and weekday labels Go supplies as catalogue keys. Since
// issue #383 the surface is one section per URL, so the labels live on the
// sections that own them and this test renders those sections.
func TestSpanishSettingsScreen(t *testing.T) {
	r, err := newRendererWithLanguage(templates.FS, stubInterfaceLanguage{language: "es"})
	if err != nil {
		t.Fatalf("newRendererWithLanguage: %v", err)
	}
	render := func(page string, data any) string {
		t.Helper()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/settings/"+page, nil)
		r.Render(rec, req, page, "", data, http.StatusOK)
		if rec.Code != http.StatusOK {
			t.Fatalf("render %s in es: %d %s", page, rec.Code, rec.Body.String())
		}
		return rec.Body.String()
	}

	tickets := render("settings_tickets", fixtureSettingsTicketsData())
	for _, want := range []string{"Configuración", "Apariencia", "Acuerdos de nivel de servicio (SLA)", "Azul", "Guardar apariencia",
		// The automatic hold close (issue #353) is a form of this section.
		"Cierre automático de tickets en espera", "Cerrar tras N días hábiles sin respuesta", "Guardar el cierre automático",
		// The category-suggestion card (issue #416) names its engines, its model
		// fields and its Try it box in the instance's language too (S10).
		"Sugerencias de categoría", "Desactivadas", "En este servidor", "Modelo de decisión",
		"Probar ahora", "Escribe una descripción que podría escribir quien pide", "Probar", "Guardar las sugerencias de categoría"} {
		if !strings.Contains(tickets, want) {
			t.Errorf("Spanish tickets section must render %q, got: %.800s", want, tickets)
		}
	}
	if strings.Contains(tickets, ">Category suggestions<") || strings.Contains(tickets, ">On this server<") {
		t.Errorf("Spanish tickets section must not render English labels")
	}

	calendar := render("settings_calendar", fixtureSettingsCalendarData())
	for _, want := range []string{"Calendario laboral", "Días laborables", "Zona horaria", "Lunes", "Guardar el calendario laboral"} {
		if !strings.Contains(calendar, want) {
			t.Errorf("Spanish calendar section must render %q, got: %.800s", want, calendar)
		}
	}
	if strings.Contains(calendar, ">Monday<") {
		t.Errorf("Spanish calendar section must not render English weekday labels")
	}

	language := render("settings_language", fixtureSettingsLanguageData())
	for _, want := range []string{"Idioma de la interfaz", "Guardar idioma", "Organización"} {
		if !strings.Contains(language, want) {
			t.Errorf("Spanish language section must render %q, got: %.800s", want, language)
		}
	}
	if strings.Contains(language, ">Interface language<") {
		t.Errorf("Spanish language section must not render English labels")
	}
}

// TestSpanishCategorySuggestions renders both suggestion fragments and the Try
// it result in Spanish (issue #416, S10): the title, the group heading, the
// matched-words label and every result sentence are catalogue copy, so a Spanish
// instance shows no English in the pickers it just turned on.
func TestSpanishCategorySuggestions(t *testing.T) {
	suggestions := categorySuggestionsData{Suggestions: []suggestionView{
		{ID: 4, Name: "Impresoras", Caption: "Tecnología · Service desk", MatchedWords: []string{"impresora", "toner"}},
	}}
	chips := renderLocalized(t, "es", "tickets_new", "category_suggestion_chips", suggestions)
	for _, want := range []string{"Sugeridas a partir de tu descripción", "Impresoras", "Tecnología · Service desk"} {
		if !strings.Contains(chips, want) {
			t.Errorf("Spanish chips must render %q, got: %.800s", want, chips)
		}
	}
	tiles := renderLocalized(t, "es", "tickets_catalog", "category_suggestion_tiles", suggestions)
	for _, want := range []string{"Sugeridas", "Coincide: impresora, toner"} {
		if !strings.Contains(tiles, want) {
			t.Errorf("Spanish tiles must render %q, got: %.800s", want, tiles)
		}
	}
	if strings.Contains(tiles, "Matches:") {
		t.Errorf("Spanish tiles must not render the English label, got: %.800s", tiles)
	}

	tried := renderLocalized(t, "es", "settings_tickets", "category_suggestion_try", categorySuggestionTryView{
		Candidates: []categorySuggestionTryCandidate{{Name: "Impresoras", Percent: 72}},
		Confident:  true,
	})
	for _, want := range []string{"72%", "Quien pide vería estas sugerencias."} {
		if !strings.Contains(tried, want) {
			t.Errorf("Spanish Try it result must render %q, got: %.800s", want, tried)
		}
	}
}

// TestSpanishValidationErrors proves mapErrorFor resolves the mapped copy in the
// request's interface language: one validation error per family (ticket, user,
// category, workflow, SLA) reads Spanish on a Spanish request, and the dynamic
// transition error interpolates its translated states. The English contract of
// mapError is untouched.
func TestSpanishValidationErrors(t *testing.T) {
	request := func(locale string) *http.Request {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		return req.WithContext(context.WithValue(req.Context(), ctxKeyInterfaceLanguage{}, locale))
	}
	es := request("es")
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"ticket", &domain.ValidationError{Field: "title", Message: domain.ErrMsgTitleRequired}, "el título es obligatorio"},
		{"user", &domain.ValidationError{Field: "email", Message: domain.ErrMsgUserEmailRequired}, "el correo electrónico es obligatorio"},
		{"category", &domain.ValidationError{Field: "name", Message: domain.ErrMsgCategoryNameRequired}, "el nombre de la categoría es obligatorio"},
		{"workflow", &domain.ValidationError{Field: "steps", Message: "workflow must have at least one step"}, "el flujo debe tener al menos un paso"},
		{"SLA", &domain.ValidationError{Field: "warning", Message: "the SLA warning percent must be between 1 and 99"}, "el porcentaje de aviso del SLA debe estar entre 1 y 99"},
		// Issue #353: the automatic hold close window reads Spanish too.
		{"hold auto-close", &domain.ValidationError{Field: "hold_auto_close_working_days", Message: "the hold auto-close window must be between 0 and 60 working days", Key: "error.hold_auto_close_days_invalid"}, "la ventana de cierre automático en espera debe estar entre 0 y 60 días hábiles"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, msg := mapErrorFor(es, tc.err)
			if status != http.StatusUnprocessableEntity {
				t.Errorf("mapErrorFor status = %d, want %d", status, http.StatusUnprocessableEntity)
			}
			if msg != tc.want {
				t.Errorf("mapErrorFor(%s) = %q, want %q", tc.name, msg, tc.want)
			}
		})
	}

	transition := domain.NewInvalidTransitionError(domain.StateNew, domain.StateClosed)
	if _, msg := mapErrorFor(es, transition); msg != "no se permite la transición de nuevo a cerrado" {
		t.Errorf("Spanish transition error = %q", msg)
	}

	// English is unchanged: the same call through an English request answers the
	// historical messages byte for byte.
	en := request("en")
	if _, msg := mapErrorFor(en, transition); msg != "transition not allowed from new to closed" {
		t.Errorf("English transition error = %q, want the historical message", msg)
	}
	if _, msg := mapErrorFor(en, &domain.ValidationError{Field: "title", Message: domain.ErrMsgTitleRequired}); msg != domain.ErrMsgTitleRequired {
		t.Errorf("English validation error = %q, want %q", msg, domain.ErrMsgTitleRequired)
	}
}

// TestSpanishMetrics renders the compact summary and the metrics detail page in
// Spanish: every panel heading, filter label, scope line, legend and attainment
// header comes from the catalogue, including the enum-driven group names.
func TestSpanishMetrics(t *testing.T) {
	kpis := renderLocalized(t, "es", "ticket_metrics_detail", "ticket_metrics_kpis", ticketMetricsSummaryData())
	for _, want := range []string{"Creados", "Resueltos", "Pendientes"} {
		if !strings.Contains(kpis, want) {
			t.Errorf("Spanish KPI row must render %q, got: %.800s", want, kpis)
		}
	}
	if strings.Contains(kpis, ">Created<") {
		t.Error("Spanish KPI row must not render the English label")
	}

	detail := renderLocalized(t, "es", "ticket_metrics_detail", "ticket_metrics_detail_content", ticketMetricsRenderData())
	for _, want := range []string{
		"Métricas de tickets",
		"Volver a los tickets",
		"Cargando métricas",
		"Todas las mesas",
		"Todos los agentes",
		"Creados frente a resueltos",
		"Ver datos",
		"Antigüedad de los tickets pendientes",
		"Carga pendiente",
		"Por agente",
		"Por mesa",
		"Carga pendiente por agente actual",
		"Distribución del tiempo de resolución",
		"Cumplimiento del SLA",
		"Cumplimiento del SLA por Prioridad",
		"Primera respuesta",
		"Resolución",
	} {
		if !strings.Contains(detail, want) {
			t.Errorf("Spanish metrics detail must render %q, got: %.1400s", want, detail)
		}
	}
	for _, unwanted := range []string{"Ticket metrics", "Created vs resolved", "SLA attainment"} {
		if strings.Contains(detail, unwanted) {
			t.Errorf("Spanish metrics detail must not render the English %q", unwanted)
		}
	}
	// Issue #380: the heading names the surface, so the repeated subtitle is gone
	// in both locales.
	if strings.Contains(detail, "Carga operativa y resolución") {
		t.Errorf("Spanish metrics detail must not render the repeated subtitle, got: %.1400s", detail)
	}
}

// ticketMetricsSummaryData is a small summary fixture with all four cards set.
func ticketMetricsSummaryData() ticketMetricsData {
	return ticketMetricsData{
		Metrics:                  application.TicketMetrics{Pending: 4, Unassigned: 1, Resolved: 3},
		MeanResolution:           "2 h",
		MeanResolutionIsDuration: true,
		ReturnHref:               "/tickets",
	}
}

// ticketMetricsRenderData is a detail fixture that exercises every panel and the
// enum-driven group names (workload by agent, attainment by priority).
func ticketMetricsRenderData() ticketMetricsDetailData {
	milestone := ticketMetricsAttainmentMilestoneView{Met: 1, Breached: 0, Open: 1, Decided: 1, Rate: "100%"}
	return ticketMetricsDetailData{
		pageData: pageData{NavActive: "tickets"},
		ticketMetricsData: ticketMetricsData{
			Metrics: application.TicketMetrics{
				Pending:    4,
				Unassigned: 1,
				Created:    5,
				Resolved:   3,
				Samples:    3,
				WorkloadBy: "agent",
				Weeks:      []application.TicketMetricsWeek{{Start: time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC), Created: 5, Resolved: 3}},
				Ages:       []application.TicketMetricsBucket{{Label: "0-1 days", Count: 2}},
				Workload:   []application.TicketMetricsWorkload{{Label: "Ana", Count: 2}},
				Histogram:  []application.TicketMetricsBucket{{Label: "0-1 days", Count: 3}},
			},
			MeanResolution:           "2 h",
			MeanResolutionIsDuration: true,
			Attainment: ticketMetricsAttainmentView{
				GroupBy:       "priority",
				Cohort:        2,
				FirstResponse: milestone,
				Resolve:       ticketMetricsAttainmentMilestoneView{Open: 2, Rate: "0%"},
				Groups: []ticketMetricsAttainmentGroupView{
					{Label: "Alta", Tickets: 1, FirstResponse: milestone, Resolve: ticketMetricsAttainmentMilestoneView{Open: 1, Rate: "0%"}},
				},
			},
		},
		BackHref: "/tickets",
	}
}

// TestSpanishTimelineNarrative renders the ticket timeline in Spanish: the
// comment verbs and the audit-event narrative sentences (assignment, state
// move, creation) all come from the catalogue as application.Msg references.
func TestSpanishTimelineNarrative(t *testing.T) {
	data := fixtureDetailData()
	// The golden fixture pins the view model directly, so bind the catalogue
	// references enrichTimeline attaches in production (the application-layer
	// test TestEventSummaryMsg pins the construction itself).
	data.View.Timeline[1].NarrativeMsg = application.MsgRef("tickets.timeline.narrative.assigned_to", application.Text("Ana Torres"))
	data.View.Timeline[2].NarrativeMsg = application.MsgRef("tickets.timeline.narrative.moved_to", application.Copy("tickets.timeline.state.in_progress"))
	data.View.Timeline[3].NarrativeMsg = application.MsgRef("tickets.timeline.narrative.created")
	// A changed field interpolates the FIELD NAME and its VALUE as catalogue
	// messages too (TestTicketViewBuildsTheChangedFieldNarrativeFromTheCatalogue
	// pins the construction), so neither an English field word nor an English
	// priority label leaks into a Spanish sentence.
	data.View.Timeline = append(data.View.Timeline, application.TimelineItem{
		Event:      &data.View.AuditEvents[0],
		ActorLabel: "Admin",
		NarrativeMsg: application.MsgRef("tickets.timeline.narrative.changed_to",
			application.Copy("tickets.timeline.field.priority"), application.Copy("label.priority.low")),
	})
	// The automatic hold close (issue #353, S5) says WHY it closed, in the
	// instance language, and carries no actor label to translate.
	via := domain.ClosureViaHoldExpired
	data.View.Timeline = append(data.View.Timeline, application.TimelineItem{
		Event:        &domain.AuditEvent{Actor: domain.AutomaticActorSystem, Action: domain.ActionTransition, FromValue: strptr("resolved"), ToValue: strptr("closed"), ClosureVia: &via},
		NarrativeMsg: application.MsgRef("tickets.timeline.narrative.closed_hold_expired"),
	})
	body := renderLocalized(t, "es", "tickets_show", "timeline", data)
	for _, want := range []string{
		"agregó un comentario",
		"asignó el ticket a Ana Torres",
		"movió el ticket a en curso",
		"creó el ticket",
		"cambió la prioridad a Baja",
		"cerró el ticket porque el solicitante no respondió",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("Spanish timeline must render %q, got: %.1000s", want, body)
		}
	}
	for _, unwanted := range []string{"added a comment", "moved the ticket to in progress", "created the ticket", "changed priority", " to Low", "closed the ticket because the requester did not reply"} {
		if strings.Contains(body, unwanted) {
			t.Errorf("Spanish timeline must not render the English %q", unwanted)
		}
	}
}

// TestSpanishWorkflowBuilder renders the workflow builder page in Spanish: the
// page chrome, the step-kind labels resolved from the catalogue, the editor and
// dialog copy all follow the instance language. The disabled add options carry
// the catalogue refusal too.
func TestSpanishWorkflowBuilder(t *testing.T) {
	data := mobileBuilderPageData()
	data.Draft = append(data.Draft, domain.WorkflowDefinition{{Type: domain.StepResolve}}...)
	data.Steps = workflowStepViews(data.Draft, 0, data.Desks)
	data.HasFinal = true
	body := renderLocalized(t, "es", "category_workflow", "", data)
	for _, want := range []string{
		"Flujo de la categoría",
		"Pasos del flujo",
		"Selecciona un paso para configurarlo. Arrastra para reordenar.",
		"Dar una tarea al agente",
		"Enviar a una mesa",
		"Pedir información",
		// Step-card facts come from the catalogue too, while the admin-authored
		// instruction and the desk name stay data (S8/S11).
		"Triage and answer the ticket.",
		"Miembros de Support",
		"En curso",
		"Automático",
		"Resuelto",
		">+ Agregar paso</summary>",
		"No se puede agregar · este flujo ya tiene un cierre",
		"¿Quitar este paso?",
		"El paso y sus campos se quitarán del flujo.",
		"¿Guardar los cambios del flujo?",
		"¿Salir sin guardar?",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("Spanish workflow builder must render %q, got: %.1600s", want, body)
		}
	}
	for _, unwanted := range []string{">Category workflow<", ">Workflow steps<", ">Give the agent a task<", ">Members of Support<", ">In progress<", ">Automatic<"} {
		if strings.Contains(body, unwanted) {
			t.Errorf("Spanish workflow builder must not render the English %q", unwanted)
		}
	}
	// Issue #380: the page title and the builder name the job, so the subtitle is
	// gone.
	if strings.Contains(body, "Construye un recorrido ordenado para esta categoría.") {
		t.Errorf("Spanish workflow builder must not render the repeated subtitle, got: %.1600s", body)
	}
}

// TestSpanishWorkflowBuilderIssues renders the builder's validation banners in
// Spanish: a domain validation issue is addressed by a catalogue key (the
// domain keeps the English Message and exposes the finding as structured data),
// the draft-conflict refusal follows the instance language, and an issue that
// carries no key still renders its own text verbatim.
func TestSpanishWorkflowBuilderIssues(t *testing.T) {
	data := mobileBuilderPageData()
	data.Issues = []domain.WorkflowValidationIssue{
		{Step: 1, Field: "steps", Message: "workflow must have at least one step", Key: "error.workflow_min_steps"},
		{Step: 1, Field: "draft", Message: draftConflictMessage, Key: "error.workflow_draft_conflict"},
		{Step: 1, Field: "raw", Message: "raw internal detail"},
	}
	body := renderLocalized(t, "es", "category_workflow", "workflow_builder", data)
	for _, want := range []string{
		"el flujo debe tener al menos un paso",
		"Otra sesión cambió este flujo mientras lo editabas. Tu cambio no se guardó; abajo se muestra el borrador más reciente.",
		"raw internal detail",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("Spanish workflow builder must render %q, got: %.1600s", want, body)
		}
	}
	for _, unwanted := range []string{
		"workflow must have at least one step",
		draftConflictMessage,
	} {
		if strings.Contains(body, unwanted) {
			t.Errorf("Spanish workflow builder must not render the English %q", unwanted)
		}
	}
}

// TestSpanishMetricsCharts renders the age and workload charts in Spanish: the
// chart's accessible label, its visible axis title, and the four fixed age
// bucket labels all come from the catalogue, so no English word survives on a
// Spanish metrics page (S8).
func TestSpanishMetricsCharts(t *testing.T) {
	ages := []application.TicketMetricsBucket{
		{Label: "0–2 days", LabelKey: "ticket_metrics.age.bucket.0_2", Count: 2},
		{Label: "3–7 days", LabelKey: "ticket_metrics.age.bucket.3_7", Count: 1},
		{Label: "8–14 days", LabelKey: "ticket_metrics.age.bucket.8_14", Count: 0},
		{Label: ">14 days", LabelKey: "ticket_metrics.age.bucket.14_plus", Count: 1},
	}
	ageSVG := string(metricsAgeChart(i18n.New("es"), ages))
	for _, want := range []string{
		"0–2 días", "3–7 días", "8–14 días", "&gt;14 días",
		"Tickets pendientes",
		"Tickets pendientes por antigüedad transcurrida completada.",
	} {
		if !strings.Contains(ageSVG, want) {
			t.Errorf("Spanish age chart must render %q: %.1200s", want, ageSVG)
		}
	}
	for _, unwanted := range []string{"0–2 days", "3–7 days", ">14 days", ">Pending tickets<", "by completed elapsed age"} {
		if strings.Contains(ageSVG, unwanted) {
			t.Errorf("Spanish age chart must not render the English %q: %.1200s", unwanted, ageSVG)
		}
	}

	workloadSVG := string(metricsWorkloadChart(i18n.New("es"), []application.TicketMetricsWorkload{{Label: "Ana", Count: 2, Unassigned: false}}, "Carga pendiente"))
	for _, want := range []string{"Carga pendiente. Eje X de 0 a 2. Título del eje X: Tickets pendientes.", ">Tickets pendientes<"} {
		if !strings.Contains(workloadSVG, want) {
			t.Errorf("Spanish workload chart must render %q: %.1200s", want, workloadSVG)
		}
	}
}

// TestSpanishWorkflowChecklist drives the real ticket-detail handler on a
// Spanish instance: the Steps checklist is composed in Go, so this proves the
// step names, the current row's control facts, the pending status lines and the
// automatic-completion attribution all follow the catalogue instead of leaking
// the English literals (S8).
func TestSpanishWorkflowChecklist(t *testing.T) {
	h := newHarness(t)
	if err := h.settings.SetInterfaceLanguage(t.Context(), *h.admin, application.InterfaceLanguageSpanish); err != nil {
		t.Fatalf("set interface language: %v", err)
	}
	cat, err := h.categories.Create(t.Context(), "Lista")
	if err != nil {
		t.Fatalf("create category: %v", err)
	}
	h.publishWorkflow(t, cat.ID, domain.WorkflowDefinition{
		{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "Hacer el trabajo"}},
		{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "Llamar a quien reportó"}},
		{Type: domain.StepResolve},
	})
	tkt := h.seedTicket(t, "lista en español", func(in *application.CreateTicketInput) { in.CategoryID = cat.ID })
	h.assignTicket(t, tkt.ID, h.admin.ID)

	body := h.get(t, "/tickets/"+strconv.FormatInt(tkt.ID, 10), false).Body.String()
	for _, want := range []string{
		"Asignado a Admin · tú",
		"Esperando el paso anterior",
		"Se ejecuta por sí solo",
		"El ticket se marca como resuelto",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("Spanish checklist must render %q, got: %.1600s", want, body)
		}
	}
	for _, unwanted := range []string{
		"Assigned to Admin",
		"Waiting on the step before it",
		"Runs on its own",
		"The ticket is marked resolved",
	} {
		if strings.Contains(body, unwanted) {
			t.Errorf("Spanish checklist must not render the English %q", unwanted)
		}
	}
}

// TestSpanishBadIdentifierErrors proves the plain-text 400 responses for a
// malformed route identifier follow the instance language too, so a Spanish
// instance never answers an English sentence (S8).
func TestSpanishBadIdentifierErrors(t *testing.T) {
	h := newHarness(t)
	if err := h.settings.SetInterfaceLanguage(t.Context(), *h.admin, application.InterfaceLanguageSpanish); err != nil {
		t.Fatalf("set interface language: %v", err)
	}
	for _, tc := range []struct {
		path string
		want string
	}{
		{"/tickets/not-a-number", "id de ticket inválido"},
		{"/users/not-a-number/edit", "id de usuario inválido"},
		{"/categories/not-a-number/edit", "id de categoría inválido"},
	} {
		rec := h.get(t, tc.path, false)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("GET %s = %d, want 400: %s", tc.path, rec.Code, rec.Body.String())
		}
		if body := rec.Body.String(); !strings.Contains(body, tc.want) {
			t.Errorf("GET %s must render %q, got %q", tc.path, tc.want, strings.TrimSpace(body))
		}
	}
}

// TestSpanishKeylessErrorsAndLiteralsFollowTheInstanceLanguage covers the S18
// defect class end to end. Two messages reached a page outside the catalogue: the
// clone refusal the application raises when the target already has a draft, and
// the metrics "No verifiable durations" literal. Both read Spanish on a Spanish
// instance, and English keeps its historical bytes (S13).
func TestSpanishKeylessErrorsAndLiteralsFollowTheInstanceLanguage(t *testing.T) {
	for _, tc := range []struct {
		name         string
		language     string
		draftRefusal string
	}{
		{
			name:         "english",
			language:     application.InterfaceLanguageEnglish,
			draftRefusal: "the target category already has a draft; cloning would overwrite it",
		},
		{
			name:         "spanish",
			language:     application.InterfaceLanguageSpanish,
			draftRefusal: "la categoría de destino ya tiene un borrador; clonar lo sobrescribiría",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			if err := h.settings.SetInterfaceLanguage(t.Context(), *h.admin, tc.language); err != nil {
				t.Fatalf("set interface language: %v", err)
			}
			source, err := h.categories.Create(t.Context(), "Clone source")
			if err != nil {
				t.Fatalf("create source: %v", err)
			}
			h.publishWorkflow(t, source.ID, domain.WorkflowDefinition{{Type: domain.StepResolve}})
			target, err := h.categories.Create(t.Context(), "Clone target")
			if err != nil {
				t.Fatalf("create target: %v", err)
			}
			path := "/categories/" + strconv.FormatInt(target.ID, 10) + "/workflow/clone"
			form := url.Values{"source_category_id": {strconv.FormatInt(source.ID, 10)}}
			if first := h.postBuilder(t, path, form, false); first.Code != http.StatusSeeOther {
				t.Fatalf("first clone = %d, want 303: %s", first.Code, first.Body.String())
			}
			refused := h.postBuilder(t, path, form, false)
			if refused.Code != http.StatusUnprocessableEntity {
				t.Fatalf("clone refusal = %d, want 422: %s", refused.Code, refused.Body.String())
			}
			if body := refused.Body.String(); !strings.Contains(body, tc.draftRefusal) {
				t.Errorf("clone refusal must render %q, got: %.800s", tc.draftRefusal, body)
			}
			if tc.language == application.InterfaceLanguageSpanish && strings.Contains(refused.Body.String(), "already has a draft") {
				t.Errorf("Spanish clone refusal must not render the English sentence: %.800s", refused.Body.String())
			}
		})
	}
}

// TestSpanishSLADetailAndDegradeLabels proves the handler/application literals
// the S18 sweep catalogued follow the instance language: the ticket-detail SLA
// milestone labels and overdue word, and the metrics workload "No desk" degrade
// label. English stays byte-identical through the same catalogue entries.
func TestSpanishSLADetailAndDegradeLabels(t *testing.T) {
	frozen := &domain.TicketSLA{
		FirstResponseSeconds: 14400,
		ResolveSeconds:       86400,
		WarnFirstResponseAt:  goldenT0.Add(3*time.Hour + 12*time.Minute),
		DueFirstResponseAt:   goldenT0.Add(4 * time.Hour),
		WarnResolveAt:        goldenT0.Add(19*time.Hour + 12*time.Minute),
		DueResolveAt:         goldenT0.Add(24 * time.Hour),
		StartedAt:            goldenT0,
		PolicySnapshotAt:     goldenT0,
	}
	now := goldenT0.Add(48 * time.Hour)
	panel := slaPanelFor(i18n.Spanish, domain.ProjectSLA(frozen, domain.SLAMilestones{}, now))
	if panel == nil || len(panel.Milestones) != 2 {
		t.Fatalf("Spanish SLA panel = %+v, want two milestones", panel)
	}
	if got := panel.Milestones[0].Label; got != "Primera respuesta" {
		t.Errorf("Spanish first-response label = %q, want %q", got, "Primera respuesta")
	}
	if got := panel.Milestones[1].Label; got != "Resolución" {
		t.Errorf("Spanish resolve label = %q, want %q", got, "Resolución")
	}
	if got := panel.Milestones[0].Remaining; !strings.Contains(got, "vencido") {
		t.Errorf("Spanish overdue remaining = %q, want the vencido word", got)
	}
	if got := bucketLabel(i18n.New(i18n.Spanish), application.TicketMetricsWorkload{Label: "No desk", LabelKey: "tickets.degrade.no_desk"}); got != "Sin mesa" {
		t.Errorf("Spanish workload no-desk label = %q, want %q", got, "Sin mesa")
	}
	for key, want := range map[string]string{
		"tickets.agent.claim_unavailable":   "Este ticket ya no está disponible para tomarlo.",
		"categories.refresh_error":          "Se guardaron los cambios, pero no se pudo actualizar la lista de categorías. Recarga la página para ver los cambios guardados.",
		"categories.unassigned.name":        "Sin asignar",
		"categories.unassigned.description": "Mesas sin departamento",
	} {
		if got := i18n.T(i18n.Spanish, key); got != want {
			t.Errorf("es %q = %q, want %q", key, got, want)
		}
	}
}

// TestSpanishCatalogStructureSyntheticGroup renders the virtual Unassigned
// department a legacy desk is exposed through in Spanish: the synthetic group
// name and description are chrome, so they follow the instance language even
// though the desks themselves are admin-authored content (S11).
func TestSpanishCatalogStructureSyntheticGroup(t *testing.T) {
	h := newHarness(t)
	if err := h.settings.SetInterfaceLanguage(t.Context(), *h.admin, application.InterfaceLanguageSpanish); err != nil {
		t.Fatalf("set interface language: %v", err)
	}
	legacy, err := h.desks.CreateWithDescription(t.Context(), *h.admin, "Legacy desk", "Legacy description")
	if err != nil {
		t.Fatalf("create legacy desk: %v", err)
	}
	catalog := application.NewCatalogService(h.store.CatalogStore(), h.store.CategoryStore(), h.clock)
	mux := http.NewServeMux()
	NewCategoryHandlersWithWorkflows(h.categories, h.workflows, h.renderer, catalog).Register(mux)
	req := httptest.NewRequest(http.MethodGet, "/categories?view=structure&department_id=unassigned&desk_id="+strconv.FormatInt(legacy.ID, 10), nil)
	req.Header.Set("Cookie", sessionCookie+"="+h.adminSession.ID)
	rec := httptest.NewRecorder()
	h.mw.Wrap(mux).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("spanish legacy structure = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{"Sin asignar", "Mesas sin departamento", "Legacy desk"} {
		if !strings.Contains(body, want) {
			t.Errorf("Spanish catalog structure must render %q, got: %.800s", want, body)
		}
	}
	if strings.Contains(body, ">Unassigned<") || strings.Contains(body, "Desks without a department") {
		t.Errorf("Spanish catalog structure must not render the English synthetic group: %.800s", body)
	}
}

// TestSpanishSetupShell renders the first-account screen in Spanish: the shell
// copy and the form labels/placeholders both come from the catalogue, so no
// English literal survives on the setup path.
func TestSpanishSetupShell(t *testing.T) {
	r, err := newRendererWithLanguage(templates.FS, stubInterfaceLanguage{language: "es"})
	if err != nil {
		t.Fatalf("newRendererWithLanguage: %v", err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/setup", nil)
	r.Render(rec, req, "setup", "", setupData{}, http.StatusOK)
	if rec.Code != http.StatusOK {
		t.Fatalf("render setup in es: %d %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{"Correo electrónico", "Contraseña", "Configura tkt", "tu@ejemplo.com"} {
		if !strings.Contains(body, want) {
			t.Errorf("Spanish setup page must render %q, got: %.600s", want, body)
		}
	}
	for _, unwanted := range []string{">Email<", ">Password<", "you@example.com"} {
		if strings.Contains(body, unwanted) {
			t.Errorf("Spanish setup page must not render the English %q", unwanted)
		}
	}
}

// TestSpanishInterpolatedErrors proves the user-facing errors that interpolate
// a value (an SLA policy, workflow validation, workflow-answer parsing) follow
// the instance language through a structured key and args, while the English
// contract stays the domain's own bytes (S8, S13).
func TestSpanishInterpolatedErrors(t *testing.T) {
	request := func(locale string) *http.Request {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		return req.WithContext(context.WithValue(req.Context(), ctxKeyInterfaceLanguage{}, locale))
	}
	cases := []struct {
		name string
		err  error
		en   string
		es   string
	}{
		{
			"SLA missing policy",
			&domain.ValidationError{Field: "low", Message: `missing SLA policy for priority "low"`, Key: "error.sla_missing_policy", Args: []any{domain.PriorityLow}},
			`missing SLA policy for priority "low"`,
			`falta la política de SLA para la prioridad "Baja"`,
		},
		{
			"SLA first response minimum",
			&domain.ValidationError{Field: "high", Message: `first response target for priority "high" is below 60 seconds`, Key: "error.sla_first_response_min", Args: []any{domain.PriorityHigh, 60}},
			`first response target for priority "high" is below 60 seconds`,
			`el objetivo de primera respuesta para la prioridad "Alta" es inferior a 60 segundos`,
		},
		{
			"workflow step validation",
			&domain.ValidationError{Field: "answers", Message: "Step 2: choose a desk", Key: "error.workflow.choose_desk", Args: []any{2}},
			"Step 2: choose a desk",
			"Paso 2: elige una mesa",
		},
		{
			"workflow answer parsing",
			&domain.ValidationError{Field: "answers", Message: "unknown position 3", Key: "error.answers.unknown_position", Args: []any{3}},
			"unknown position 3",
			"posición desconocida 3",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, got := mapErrorFor(request("es"), tc.err); got != tc.es {
				t.Errorf("Spanish %s = %q, want %q", tc.name, got, tc.es)
			}
			if _, got := mapErrorFor(request("en"), tc.err); got != tc.en {
				t.Errorf("English %s = %q, want the domain bytes %q", tc.name, got, tc.en)
			}
		})
	}
}

// TestSpanishEntityErrors proves NotFound/Duplicate/Referenced errors render the
// labelled entity kind in Spanish and keep the domain bytes in English.
func TestSpanishEntityErrors(t *testing.T) {
	request := func(locale string) *http.Request {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		return req.WithContext(context.WithValue(req.Context(), ctxKeyInterfaceLanguage{}, locale))
	}
	cases := []struct {
		name string
		err  error
		en   string
		es   string
	}{
		{"not found", &domain.NotFoundError{Kind: "ticket", ID: int64(7)}, "ticket not found", "el ticket no existe"},
		{"duplicate", &domain.DuplicateError{Kind: "user", Name: "ana@example.com"}, `user "ana@example.com" already exists`, `el usuario "ana@example.com" ya existe`},
		{"referenced", &domain.ReferencedError{Kind: "category", ID: int64(7)}, "category is referenced and cannot be deleted", "la categoría está en uso y no se puede eliminar"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, got := mapErrorFor(request("es"), tc.err); got != tc.es {
				t.Errorf("Spanish %s = %q, want %q", tc.name, got, tc.es)
			}
			if _, got := mapErrorFor(request("en"), tc.err); got != tc.en {
				t.Errorf("English %s = %q, want the domain bytes %q", tc.name, got, tc.en)
			}
		})
	}
}

// TestSpanishWorkflowValidationIssueArgs proves the per-step Validate findings
// carry a key and args, so a Spanish builder shows the finding in Spanish with
// its step number and field values interpolated.
func TestSpanishWorkflowValidationIssueArgs(t *testing.T) {
	tr := i18n.New("es")
	issue := domain.WorkflowValidationIssue{
		Step: 2, Field: "type",
		Message: "Step 2: unknown step type \"bogus\"",
		Key:     "error.workflow.unknown_step_type",
		Args:    []any{2, "bogus"},
	}
	if got := workflowIssueMessage(tr, issue); got != `Paso 2: tipo de paso desconocido "bogus"` {
		t.Errorf("Spanish workflow issue = %q", got)
	}
	if got := workflowIssueMessage(i18n.New("en"), issue); got != issue.Message {
		t.Errorf("English workflow issue = %q, want the domain message %q", got, issue.Message)
	}
}

// TestSpanishCannotRunReason proves the AssessRunnable reason reaches the
// category badge as a catalogue reference: a Spanish instance reads the reason
// in Spanish, and English is unchanged.
func TestSpanishCannotRunReason(t *testing.T) {
	def := domain.WorkflowDefinition{
		{Type: domain.StepAssignToDesk, AssignToDesk: &domain.AssignToDeskStep{DeskID: 5, Strategy: domain.StrategyClaim}},
	}
	blocker := def.AssessRunnable(func(int64) (string, int, bool) { return "Infra", 0, true }).Blockers
	if len(blocker) != 1 {
		t.Fatalf("want one blocker, got %+v", blocker)
	}
	if blocker[0].ReasonKey != "error.workflow_reason.no_members" || len(blocker[0].ReasonArgs) != 1 {
		t.Fatalf("blocker reason structure = %+v", blocker[0])
	}
	summary := application.WorkflowSummary{Version: 1, CannotRun: application.MsgValues(blocker[0].ReasonKey, blocker[0].ReasonArgs...)}
	if got := resolveMsg(i18n.New("es"), categoryBadge(summary)); got != "No puede ejecutarse · Infra no tiene miembros activos" {
		t.Errorf("Spanish cannot-run badge = %q", got)
	}
	if got := resolveMsg(i18n.New("en"), categoryBadge(summary)); got != "Can't run · Infra has no active members" {
		t.Errorf("English cannot-run badge = %q", got)
	}
}

// TestSpanishDegradeLabels proves the application-layer degrade labels that a
// page shows follow the instance language: an unknown person or desk in a
// workflow-assignment timeline entry reads Spanish, while a resolved name stays
// data (S8/S11).
func TestSpanishDegradeLabels(t *testing.T) {
	data := fixtureDetailData()
	data.View.Timeline = []application.TimelineItem{{
		Event:                &domain.AuditEvent{Actor: "workflow", Action: domain.ActionWorkflowAssignment},
		IsWorkflowAssignment: true,
		NarrativeMsg:         application.MsgRef("tickets.timeline.narrative.assigned"),
		AssignmentPerson:     "Unknown user",
		AssignmentPersonMsg:  application.MsgRef("tickets.degrade.unknown_user"),
		AssignmentDesk:       "Unknown desk",
		AssignmentDeskMsg:    application.MsgRef("tickets.degrade.unknown_desk"),
	}}
	body := renderLocalized(t, "es", "tickets_show", "timeline", data)
	for _, want := range []string{"asignó el ticket a", "Usuario desconocido", "Mesa desconocida"} {
		if !strings.Contains(body, want) {
			t.Errorf("Spanish degrade timeline must render %q, got: %.800s", want, body)
		}
	}
	for _, unwanted := range []string{"Unknown user", "Unknown desk"} {
		if strings.Contains(body, unwanted) {
			t.Errorf("Spanish degrade timeline must not render the English %q", unwanted)
		}
	}
}

// TestSpanishMetricsBucketLabels proves the application-supplied metrics labels
// that are chrome follow the instance language: the resolution histogram bins
// and the genuinely unassigned workload row.
func TestSpanishMetricsBucketLabels(t *testing.T) {
	bin := application.TicketMetricsBucket{Label: "0–<7 days", LabelKey: "ticket_metrics.resolution.bin", LabelArgs: []any{"0", "7"}}
	unassigned := application.TicketMetricsWorkload{Label: "Unassigned (no agent)", LabelKey: "tickets.degrade.unassigned_no_agent"}
	if got := bucketLabel(i18n.New("es"), bin); got != "0–<7 días" {
		t.Errorf("Spanish histogram bin = %q, want %q", got, "0–<7 días")
	}
	if got := bucketLabel(i18n.New("es"), unassigned); got != "Sin asignar (sin agente)" {
		t.Errorf("Spanish unassigned workload = %q, want %q", got, "Sin asignar (sin agente)")
	}
	if got := bucketLabel(i18n.New("en"), bin); got != "0–<7 days" {
		t.Errorf("English histogram bin = %q, want the label bytes", got)
	}
	if got := bucketLabel(i18n.New("en"), application.TicketMetricsWorkload{Label: "Ana", Count: 2}); got != "Ana" {
		t.Errorf("an agent name is data = %q, want %q", got, "Ana")
	}
	group := ticketMetricsAttainmentGroupView{Label: "High", LabelKey: "label.priority.high"}
	if got := bucketLabel(i18n.New("es"), group); got != "Alta" {
		t.Errorf("Spanish attainment priority label = %q, want %q", got, "Alta")
	}
	if got := bucketLabel(i18n.New("en"), group); got != "High" {
		t.Errorf("English attainment priority label = %q, want the English bytes", got)
	}
}

// TestSpanishGenericHTTPErrors proves the generic plain-text bodies a user can
// see follow the instance language while the default locale keeps today's
// bytes: the fallback 500, the capability refusal, and the oversized/body
// refusals.
func TestSpanishGenericHTTPErrors(t *testing.T) {
	request := func(locale string) *http.Request {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		return req.WithContext(context.WithValue(req.Context(), ctxKeyInterfaceLanguage{}, locale))
	}
	if _, got := mapErrorFor(request("es"), errors.New("boom")); got != "Error interno del servidor" {
		t.Errorf("Spanish generic 500 = %q", got)
	}
	if _, got := mapErrorFor(request("en"), errors.New("boom")); got != "Internal server error" {
		t.Errorf("English generic 500 = %q, want the historical bytes", got)
	}
	for _, tc := range []struct {
		key  string
		want string
	}{
		{"error.forbidden", "Prohibido"},
		{"error.request_too_large", "La solicitud es demasiado grande"},
		{"error.bad_request", "Solicitud no válida"},
	} {
		rec := httptest.NewRecorder()
		httpError(rec, request("es"), tc.key, http.StatusBadRequest)
		if got := strings.TrimSpace(rec.Body.String()); got != tc.want {
			t.Errorf("httpError(%s) = %q, want %q", tc.key, got, tc.want)
		}
	}
}

// TestSpanishPluralRendering proves the workload scope uses the Spanish singular.
func TestSpanishPluralRendering(t *testing.T) {
	metrics := ticketMetricsRenderData()
	metrics.Metrics.Pending = 1
	if body := renderLocalized(t, "es", "ticket_metrics_detail", "ticket_metrics_detail_content", metrics); !strings.Contains(body, "Asignación actual · 1 ticket pendiente") {
		t.Errorf("Spanish workload scope must read the singular, got: %.800s", body)
	}
}

// TestSpanishMetricsLineChartChrome proves the created-vs-resolved line chart
// and the resolution histogram carry no hardcoded English chrome on a Spanish
// instance: the accessible label and the visible axis titles follow the
// catalogue, while the English bytes are unchanged.
func TestSpanishMetricsLineChartChrome(t *testing.T) {
	weeks := lineChartWeeks()
	esLine := string(metricsLineChart(i18n.New("es"), weeks))
	for _, unwanted := range []string{"Weekly created tickets", "Y axis title Tickets"} {
		if strings.Contains(esLine, unwanted) {
			t.Errorf("Spanish line chart must not render the English %q: %.1200s", unwanted, esLine)
		}
	}
	for _, want := range []string{"Tickets creados semanalmente", "Título del eje Y"} {
		if !strings.Contains(esLine, want) {
			t.Errorf("Spanish line chart must render %q: %.1200s", want, esLine)
		}
	}
	enLine := string(metricsLineChart(i18n.New("en"), weeks))
	for _, want := range []string{"Weekly created tickets as a continuous line", "Y axis title Tickets."} {
		if !strings.Contains(enLine, want) {
			t.Errorf("English line chart chrome changed, missing %q: %.1200s", want, enLine)
		}
	}

	buckets := []application.TicketMetricsBucket{{LowerDays: 0, UpperDays: 7, Count: 1}}
	esHist := string(metricsHistogramChart(i18n.New("es"), buckets))
	for _, unwanted := range []string{"Resolved tickets by resolution duration", "X axis title Resolution time (days)"} {
		if strings.Contains(esHist, unwanted) {
			t.Errorf("Spanish histogram must not render the English %q: %.1200s", unwanted, esHist)
		}
	}
	for _, want := range []string{"Tickets resueltos por duración de resolución", "Título del eje X"} {
		if !strings.Contains(esHist, want) {
			t.Errorf("Spanish histogram must render %q: %.1200s", want, esHist)
		}
	}
	enHist := string(metricsHistogramChart(i18n.New("en"), buckets))
	if !strings.Contains(enHist, "Resolved tickets by resolution duration in equal-width day bins.") {
		t.Errorf("English histogram chrome changed: %.1200s", enHist)
	}
}

// --- Locale-aware dates (issue #335, S12) ---
//
// Every date surface keeps the shape it renders today and only its month name
// and ordering follow the instance language, so English output stays
// byte-identical (S13) and a Spanish instance reads Spanish months.

// TestSpanishCardTimestampKeepsItsShape renders the compact card timestamp
// ("14:03 · 18 Oct") in Spanish: the clock and the day keep their positions and
// the month name is the Spanish abbreviation.
func TestSpanishCardTimestampKeepsItsShape(t *testing.T) {
	at := time.Date(2026, 10, 18, 14, 3, 0, 0, time.UTC)

	es := renderLocalized(t, "es", "tickets_index", "card_timestamp", at)
	if !strings.Contains(es, "14:03 · 18 oct") {
		t.Errorf("Spanish card timestamp must read %q, got: %s", "14:03 · 18 oct", es)
	}
	// The full date stays the accessible tooltip, unchanged: it carries no month
	// name, so its shape is the same in both locales.
	if !strings.Contains(es, `aria-label="14:03 · 18-10-2026"`) {
		t.Errorf("Spanish card timestamp must keep the numeric full date, got: %s", es)
	}

	en := renderLocalized(t, "en", "tickets_index", "card_timestamp", at)
	if !strings.Contains(en, "14:03 · 18 Oct") {
		t.Errorf("English card timestamp must stay %q, got: %s", "14:03 · 18 Oct", en)
	}
}

// TestSpanishTimestampKeepsItsNumericShape proves a surface whose shape carries
// no month name renders identically in both locales: the ticket list, the
// timeline and the detail header show "14:03 · 18-10-2026" either way.
func TestSpanishTimestampKeepsItsNumericShape(t *testing.T) {
	at := time.Date(2026, 10, 18, 14, 3, 0, 0, time.UTC)

	es := renderLocalized(t, "es", "tickets_index", "timestamp", at)
	en := renderLocalized(t, "en", "tickets_index", "timestamp", at)
	if !strings.Contains(es, "14:03 · 18-10-2026") {
		t.Errorf("Spanish timestamp must keep the numeric shape, got: %s", es)
	}
	if es != en {
		t.Errorf("a monthless timestamp must render identically in both locales:\n es: %s\n en: %s", es, en)
	}
}

// TestSpanishMetricsLineChartWeekLabels renders the weekly axis labels of the
// created-vs-resolved chart: "Sep 14" in English, "14 sep" in Spanish.
func TestSpanishMetricsLineChartWeekLabels(t *testing.T) {
	weeks := []application.TicketMetricsWeek{{Start: time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC), Created: 2, Resolved: 1}}

	es := string(metricsLineChart(i18n.New("es"), weeks))
	if !strings.Contains(es, ">14 sep<") {
		t.Errorf("Spanish chart label must read %q, got: %.1200s", "14 sep", es)
	}
	en := string(metricsLineChart(i18n.New("en"), weeks))
	if !strings.Contains(en, ">Sep 14<") {
		t.Errorf("English chart label must stay %q, got: %.1200s", "Sep 14", en)
	}
}

// TestEveryMonthLabelResolvesInBothLocales walks all twelve months through the
// rendered card timestamp, so a month the catalogue forgot degrades to a number
// and fails here instead of shipping ("15 10" instead of "15 oct").
func TestEveryMonthLabelResolvesInBothLocales(t *testing.T) {
	esMonths := []string{"ene", "feb", "mar", "abr", "may", "jun", "jul", "ago", "sep", "oct", "nov", "dic"}
	enMonths := []string{"Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"}
	for i := range esMonths {
		at := time.Date(2026, time.Month(i+1), 15, 9, 30, 0, 0, time.UTC)

		es := renderLocalized(t, "es", "tickets_index", "card_timestamp", at)
		if want := "09:30 · 15 " + esMonths[i]; !strings.Contains(es, want) {
			t.Errorf("month %d in Spanish must render %q, got: %s", i+1, want, es)
		}
		en := renderLocalized(t, "en", "tickets_index", "card_timestamp", at)
		if want := "09:30 · 15 " + enMonths[i]; !strings.Contains(en, want) {
			t.Errorf("month %d in English must render %q, got: %s", i+1, want, en)
		}
	}
}
