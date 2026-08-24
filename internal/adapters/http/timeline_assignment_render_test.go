package httpadapter

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// Contextual workflow timeline render contract: the assignment main line is
// exactly `Assigned to <strong>{person}</strong> · <strong>{desk}</strong>`,
// every interpolated value is html/template-escaped ONCE, no st-class is
// derived from the assignment ToValue user id, and workflow-completion events
// suppress their Reason/Note detail lines while an assignment's validated
// reason stays visible.

var assignmentRenderT0 = time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)

func renderTimelineFragment(t *testing.T, items []application.TimelineItem) string {
	t.Helper()
	renderer := NewRenderer()
	req := httptest.NewRequest("GET", "/tickets/1", nil)
	req.Header.Set("HX-Request", "true")
	rec := httptest.NewRecorder()
	data := struct{ View *application.TicketView }{View: &application.TicketView{Timeline: items}}
	renderer.Render(rec, req, "", "timeline", data, 200)
	if rec.Code != 200 {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

func assignmentRenderItem(person, desk string) application.TimelineItem {
	to := "7" // a user id: must never leak into markup or classes
	return application.TimelineItem{
		Event:                &domain.AuditEvent{Actor: "workflow", Action: domain.ActionWorkflowAssignment, ToValue: &to, CreatedAt: assignmentRenderT0},
		Summary:              "Assigned to",
		ActorLabel:           "Workflow",
		IsWorkflowAssignment: true,
		AssignmentPerson:     person,
		AssignmentDesk:       desk,
	}
}

func TestTimelineAssignmentRendersExactStrongMiddleDotStructure(t *testing.T) {
	body := renderTimelineFragment(t, []application.TimelineItem{
		assignmentRenderItem("Ada Torres", "Network"),
	})
	want := `Assigned to <strong>Ada Torres</strong> · <strong>Network</strong>`
	if !strings.Contains(body, want) {
		t.Fatalf("assignment main line must be exactly %q, got: %s", want, body)
	}
}

func TestTimelineAssignmentEscapesHostilePersonAndDeskOnce(t *testing.T) {
	body := renderTimelineFragment(t, []application.TimelineItem{
		assignmentRenderItem(`<script>alert(1)</script>`, `Net & Co "x"`),
	})
	want := `Assigned to <strong>&lt;script&gt;alert(1)&lt;/script&gt;</strong> · <strong>Net &amp; Co &#34;x&#34;</strong>`
	if !strings.Contains(body, want) {
		t.Fatalf("hostile person/desk must escape once into the strong pair, missing %q in: %s", want, body)
	}
	for _, raw := range []string{"<script>", "<strong><script"} {
		if strings.Contains(body, raw) {
			t.Fatalf("raw hostile markup leaked into the timeline: %q in %s", raw, body)
		}
	}
}

func TestTimelineAssignmentDoesNotDeriveStClassFromToValue(t *testing.T) {
	body := renderTimelineFragment(t, []application.TimelineItem{
		assignmentRenderItem("Ada Torres", "Network"),
	})
	if strings.Contains(body, `st-7`) || strings.Contains(body, `st-`) {
		t.Fatalf("assignment entry must not derive an st- class from the ToValue user id: %s", body)
	}
	if !strings.Contains(body, `<div class="timeline-entry timeline-event">`) {
		t.Fatalf("assignment entry class must stay plain timeline-event, got: %s", body)
	}
}

func TestTimelineCompletionEventsSuppressDetailButAssignmentReasonShows(t *testing.T) {
	secret := `answer <b>must-not-leak</b>`
	reason := "handover to second-line"
	completion := func(action string) application.TimelineItem {
		return application.TimelineItem{
			Event:          &domain.AuditEvent{Actor: "Requester", Action: action, Note: &secret, CreatedAt: assignmentRenderT0},
			Summary:        "Completed",
			ActorLabel:     "Requester",
			SuppressDetail: true,
		}
	}
	for _, action := range []string{
		domain.ActionWorkflowStep,
		domain.ActionWorkflowManualTask,
		domain.ActionWorkflowRequesterForm,
		domain.ActionWorkflowAssigneeForm,
	} {
		body := renderTimelineFragment(t, []application.TimelineItem{completion(action)})
		if strings.Contains(body, "must-not-leak") || strings.Contains(body, "Reason:") {
			t.Fatalf("%s detail line must be suppressed, got: %s", action, body)
		}
	}

	item := assignmentRenderItem("Ada Torres", "Network")
	item.Event.Reason = &reason
	body := renderTimelineFragment(t, []application.TimelineItem{item})
	if !strings.Contains(body, `Reason: handover to second-line`) {
		t.Fatalf("a validated A→B assignment reason must render, got: %s", body)
	}
	if strings.Contains(body, reason+" ") && strings.Count(body, reason) != 1 {
		t.Fatalf("assignment reason must render exactly once, got: %s", body)
	}
}
