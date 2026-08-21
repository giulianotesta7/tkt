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
		Event:      &domain.AuditEvent{Actor: "Requester", Action: domain.ActionWorkflowStep, Note: &secret, CreatedAt: time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)},
		Summary:    "Completed workflow step",
		ActorLabel: "Requester",
	}}}}

	renderer.Render(response, req, "", "timeline", data, 200)
	if body := response.Body.String(); strings.Contains(body, "must-not-leak") || strings.Contains(body, "Reason:") {
		t.Fatalf("workflow step timeline leaked content: %s", body)
	}
}
