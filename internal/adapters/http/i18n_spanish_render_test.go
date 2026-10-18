package httpadapter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
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
	for _, want := range []string{"Categorías", "Nuevo departamento", "Nueva mesa", "Nueva categoría", "Estructura del catálogo", "Abiertos"} {
		if !strings.Contains(body, want) {
			t.Errorf("Spanish catalog must render %q, got: %.800s", want, body)
		}
	}
	// The fixture's badges compose through the catalogue: "Published v1" and
	// "Draft · not published" both speak Spanish on a Spanish instance.
	for _, want := range []string{"Publicado v1", "Borrador · sin publicar"} {
		if !strings.Contains(body, want) {
			t.Errorf("Spanish catalog badge must render %q, got: %.1200s", want, body)
		}
	}
	if strings.Contains(body, "Published v1") || strings.Contains(body, "Not configured") {
		t.Errorf("Spanish catalog must not render an English badge")
	}
}

// TestSpanishSettingsScreen renders the instance settings page in Spanish,
// including the color and weekday labels Go supplies as catalogue keys.
func TestSpanishSettingsScreen(t *testing.T) {
	r, err := newRendererWithLanguage(templates.FS, stubInterfaceLanguage{language: "es"})
	if err != nil {
		t.Fatalf("newRendererWithLanguage: %v", err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/settings", nil)
	r.Render(rec, req, "settings_index", "", fixtureSettingsIndexData(), http.StatusOK)
	if rec.Code != http.StatusOK {
		t.Fatalf("render settings in es: %d %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{"Configuración", "Apariencia", "Acuerdos de nivel de servicio", "Calendario laboral", "Días laborables", "Zona horaria", "Azul", "Lunes"} {
		if !strings.Contains(body, want) {
			t.Errorf("Spanish settings page must render %q, got: %.800s", want, body)
		}
	}
	if strings.Contains(body, ">Appearance<") || strings.Contains(body, ">Monday<") {
		t.Errorf("Spanish settings page must not render English labels")
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
	summary := renderLocalized(t, "es", "ticket_metrics_detail", "ticket_metrics_summary_content", ticketMetricsSummaryData())
	for _, want := range []string{"Resumen operativo", "Tickets pendientes", "Tickets sin asignar", "Resueltos esta semana", "Tiempo medio de resolución", "Ver métricas"} {
		if !strings.Contains(summary, want) {
			t.Errorf("Spanish metrics summary must render %q, got: %.800s", want, summary)
		}
	}
	if strings.Contains(summary, "Operational summary") {
		t.Error("Spanish metrics summary must not render the English heading")
	}

	detail := renderLocalized(t, "es", "ticket_metrics_detail", "ticket_metrics_detail_content", ticketMetricsRenderData())
	for _, want := range []string{
		"Métricas de tickets",
		"Carga operativa y resolución",
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
	body := renderLocalized(t, "es", "tickets_show", "timeline", data)
	for _, want := range []string{
		"agregó un comentario público",
		"asignó el ticket a Ana Torres",
		"movió el ticket a en curso",
		"creó el ticket",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("Spanish timeline must render %q, got: %.1000s", want, body)
		}
	}
	for _, unwanted := range []string{"added a public comment", "moved the ticket to in progress", "created the ticket"} {
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
	data.Steps = []workflowStepView{
		{Index: 0, Position: 1, Step: domain.WorkflowStep{Type: domain.StepResolve}, Final: true, CanMoveLeft: true},
	}
	data.HasFinal = true
	body := renderLocalized(t, "es", "category_workflow", "", data)
	for _, want := range []string{
		"Flujo de la categoría",
		"Construye un recorrido ordenado para esta categoría.",
		"Pasos del flujo",
		"Selecciona un paso para configurarlo. Arrastra para reordenar.",
		"Dar una tarea al agente",
		"Enviar a una mesa",
		"Pedir información",
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
	for _, unwanted := range []string{">Category workflow<", ">Workflow steps<", ">Give the agent a task<"} {
		if strings.Contains(body, unwanted) {
			t.Errorf("Spanish workflow builder must not render the English %q", unwanted)
		}
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
