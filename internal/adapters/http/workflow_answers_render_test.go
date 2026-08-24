package httpadapter

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

func TestWorkflowAnswersRenderDefinitionListAndEscapesValues(t *testing.T) {
	renderer := NewRenderer()
	req := httptest.NewRequest("GET", "/tickets/1", nil)
	req.Header.Set("HX-Request", "true")
	response := httptest.NewRecorder()
	data := struct{ View *application.TicketView }{View: &application.TicketView{WorkflowResponses: []application.WorkflowResponse{{
		StepIndex: 0, SubmittedAt: time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC),
		Fields: []application.WorkflowResponseField{{Label: "Server <name>", Value: "<b>api-01</b>"}},
	}}}}

	renderer.Render(response, req, "", "workflow_answers", data, 200)
	body := response.Body.String()
	if response.Code != 200 {
		t.Fatalf("status = %d, body = %s", response.Code, body)
	}
	if !strings.Contains(body, `<dl class="workflow-responses">`) || !strings.Contains(body, `<dt>Server &lt;name&gt;</dt>`) || !strings.Contains(body, `<dd>&lt;b&gt;api-01&lt;/b&gt;</dd>`) {
		t.Fatalf("workflow responses must render escaped dt/dd definition-list pairs: %s", body)
	}
	if strings.Contains(body, "<b>api-01</b>") {
		t.Fatalf("response value rendered as raw HTML: %s", body)
	}
}

func TestWorkflowAnswersOmittedWhenAbsent(t *testing.T) {
	renderer := NewRenderer()
	req := httptest.NewRequest("GET", "/tickets/1", nil)
	req.Header.Set("HX-Request", "true")
	response := httptest.NewRecorder()
	renderer.Render(response, req, "", "workflow_answers", struct{ View *application.TicketView }{View: &application.TicketView{}}, 200)
	if body := response.Body.String(); response.Code != 200 || strings.Contains(body, "Workflow responses") {
		t.Fatalf("empty/legacy response projection rendered a card: %s", body)
	}
}

func TestWorkflowStepTimelineDoesNotRenderAnswerContent(t *testing.T) {
	renderer := NewRenderer()
	req := httptest.NewRequest("GET", "/tickets/1", nil)
	req.Header.Set("HX-Request", "true")
	response := httptest.NewRecorder()
	secret := `answer <b>must-not-leak</b>`
	data := struct{ View *application.TicketView }{View: &application.TicketView{Timeline: []application.TimelineItem{{
		Event:          &domain.AuditEvent{Actor: "Requester", Action: domain.ActionWorkflowStep, Note: &secret, CreatedAt: time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)},
		Summary:        "Completed workflow step",
		ActorLabel:     "Requester",
		SuppressDetail: true,
	}}}}

	renderer.Render(response, req, "", "timeline", data, 200)
	if body := response.Body.String(); strings.Contains(body, "must-not-leak") || strings.Contains(body, "Reason:") {
		t.Fatalf("workflow step timeline leaked content: %s", body)
	}
}

// PR10 task 10.2 — a form completion item renders its pinned labels/values
// inline as an escaped semantic definition list INSIDE that event entry.
func TestTimelineFormEventRendersEscapedInlineDefinitionList(t *testing.T) {
	renderer := NewRenderer()
	req := httptest.NewRequest("GET", "/tickets/1", nil)
	req.Header.Set("HX-Request", "true")
	response := httptest.NewRecorder()
	data := struct{ View *application.TicketView }{View: &application.TicketView{Timeline: []application.TimelineItem{{
		Event:          &domain.AuditEvent{Actor: "Ada", Action: domain.ActionWorkflowRequesterForm, CreatedAt: time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)},
		Summary:        "Submitted request details",
		ActorLabel:     "Ada",
		SuppressDetail: true,
		StepFields:     []application.WorkflowResponseField{{Label: "Server <name>", Value: "<b>api-01</b>"}},
	}}}}

	renderer.Render(response, req, "", "timeline", data, 200)
	body := response.Body.String()
	if response.Code != 200 {
		t.Fatalf("status = %d, body = %s", response.Code, body)
	}
	for _, want := range []string{
		`<dl class="workflow-responses">`,
		`<dt>Server &lt;name&gt;</dt>`,
		`<dd>&lt;b&gt;api-01&lt;/b&gt;</dd>`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("form event must render escaped inline definition list, missing %q in: %s", want, body)
		}
	}
	if strings.Contains(body, "<b>api-01</b>") {
		t.Fatalf("answer value rendered as raw HTML inside the event: %s", body)
	}
}

// PR10 task 10.2 — a manual completion item renders its contextual pinned
// instruction escaped within that same event entry.
func TestTimelineManualEventRendersEscapedPinnedInstruction(t *testing.T) {
	renderer := NewRenderer()
	req := httptest.NewRequest("GET", "/tickets/1", nil)
	req.Header.Set("HX-Request", "true")
	response := httptest.NewRecorder()
	data := struct{ View *application.TicketView }{View: &application.TicketView{Timeline: []application.TimelineItem{{
		Event:           &domain.AuditEvent{Actor: "Beto", Action: domain.ActionWorkflowManualTask, CreatedAt: time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)},
		Summary:         "Completed task",
		ActorLabel:      "Beto",
		SuppressDetail:  true,
		StepInstruction: `<script>alert(1)</script>`,
	}}}}

	renderer.Render(response, req, "", "timeline", data, 200)
	body := response.Body.String()
	if !strings.Contains(body, `&lt;script&gt;alert(1)&lt;/script&gt;`) {
		t.Fatalf("manual event must render the escaped pinned instruction: %s", body)
	}
	if strings.Contains(body, "<script>") {
		t.Fatalf("instruction rendered as raw HTML inside the event: %s", body)
	}
}

// WB.6 (Amendment 2) — a manual completion event renders its stored solution
// INSIDE the existing event entry only when non-empty, as escaped plain text;
// the completing actor's name and the event timestamp stay on the entry.
func TestTimelineManualEventRendersEscapedSolutionWhenPresent(t *testing.T) {
	renderer := NewRenderer()
	req := httptest.NewRequest("GET", "/tickets/1", nil)
	req.Header.Set("HX-Request", "true")
	response := httptest.NewRecorder()
	data := struct{ View *application.TicketView }{View: &application.TicketView{Timeline: []application.TimelineItem{{
		Event:           &domain.AuditEvent{Actor: "Beto", Action: domain.ActionWorkflowManualTask, CreatedAt: time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)},
		Summary:         "Completed task",
		ActorLabel:      "Beto",
		SuppressDetail:  true,
		StepInstruction: "inspect the server",
		StepSolution:    `<b>reseat</b> the cable & reboot`,
	}}}}

	renderer.Render(response, req, "", "timeline", data, 200)
	body := response.Body.String()
	if response.Code != 200 {
		t.Fatalf("status = %d, body = %s", response.Code, body)
	}
	for _, want := range []string{
		`<p class="workflow-instruction">inspect the server</p>`,
		`&lt;b&gt;reseat&lt;/b&gt; the cable &amp; reboot`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("manual event must render instruction followed by the escaped solution, missing %q in: %s", want, body)
		}
	}
	if strings.Contains(body, "<b>reseat</b>") || strings.Contains(body, "<script") {
		t.Fatalf("solution must never render as raw HTML inside the event: %s", body)
	}
	// Attribution stays with the existing completion event.
	if !strings.Contains(body, "· Beto") {
		t.Fatalf("solution entry must keep the completing actor attribution: %s", body)
	}
}

// WB.6 (Amendment 2) — an empty/legacy completion renders the pinned
// instruction ALONE with no empty solution block or placeholder.
func TestTimelineManualEventOmitsEmptySolution(t *testing.T) {
	renderer := NewRenderer()
	req := httptest.NewRequest("GET", "/tickets/1", nil)
	req.Header.Set("HX-Request", "true")
	response := httptest.NewRecorder()
	data := struct{ View *application.TicketView }{View: &application.TicketView{Timeline: []application.TimelineItem{{
		Event:           &domain.AuditEvent{Actor: "Beto", Action: domain.ActionWorkflowManualTask, CreatedAt: time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)},
		Summary:         "Completed task",
		ActorLabel:      "Beto",
		SuppressDetail:  true,
		StepInstruction: "inspect the server",
		StepSolution:    "",
	}}}}

	renderer.Render(response, req, "", "timeline", data, 200)
	body := response.Body.String()
	if !strings.Contains(body, `<p class="workflow-instruction">inspect the server</p>`) {
		t.Fatalf("instruction must still render for legacy completions: %s", body)
	}
	if strings.Contains(body, `class="workflow-solution"`) || strings.Contains(body, "No solution") || strings.Contains(body, "—") && !strings.Contains(body, "workflow-instruction") {
		t.Fatalf("empty solution must render no block or placeholder: %s", body)
	}
}

// WB.6 (Amendment 2) — form events never carry a solution block even if a
// hostile caller populated the field: only manual events bind StepSolution.
func TestTimelineFormEventNeverRendersSolutionBlock(t *testing.T) {
	renderer := NewRenderer()
	req := httptest.NewRequest("GET", "/tickets/1", nil)
	req.Header.Set("HX-Request", "true")
	response := httptest.NewRecorder()
	data := struct{ View *application.TicketView }{View: &application.TicketView{Timeline: []application.TimelineItem{{
		Event:          &domain.AuditEvent{Actor: "Ada", Action: domain.ActionWorkflowRequesterForm, CreatedAt: time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)},
		Summary:        "Submitted request details",
		ActorLabel:     "Ada",
		SuppressDetail: true,
		StepFields:     []application.WorkflowResponseField{{Label: "Host", Value: "api-01"}},
		StepSolution:   "should not render",
	}}}}

	renderer.Render(response, req, "", "timeline", data, 200)
	if body := response.Body.String(); strings.Contains(body, "should not render") {
		t.Fatalf("form event leaked a solution value: %s", body)
	}
}
