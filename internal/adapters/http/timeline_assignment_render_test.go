package httpadapter

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// Contextual workflow timeline render contract: human entries use one
// actor-first, sentence-case narrative; automatic entries omit their actor.
// Assignment target and desk remain visible, every interpolated value is
// html/template-escaped ONCE, and no st-class is derived from a user id.

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
		Summary:              "assigned the ticket to",
		ActorLabel:           "Workflow",
		IsWorkflowAssignment: true,
		AssignmentPerson:     person,
		AssignmentDesk:       desk,
	}
}

func TestTimelineAssignmentRendersActorFirstSentenceWithTargetAndDesk(t *testing.T) {
	body := renderTimelineFragment(t, []application.TimelineItem{
		assignmentRenderItem("Ada Torres", "Network"),
	})
	want := `assigned the ticket to <strong>Ada Torres</strong> at <strong>Network</strong>`
	if !strings.Contains(body, want) {
		t.Fatalf("assignment main line must be exactly %q, got: %s", want, body)
	}
}

func TestTimelineEventsUseActorFirstNarrative(t *testing.T) {
	state := "resolved"
	priority := "high"
	to := "7"
	comment := &domain.Comment{Author: "Ada", Body: "investigating", CreatedAt: assignmentRenderT0}
	items := []application.TimelineItem{
		{IsComment: true, Comment: comment},
		{IsComment: true, Comment: &domain.Comment{Author: "Ada", Body: "internal note", Visibility: domain.CommentInternal, CreatedAt: assignmentRenderT0}},
		{Event: &domain.AuditEvent{Actor: "Ada", Action: domain.ActionTransition, ToValue: &state, CreatedAt: assignmentRenderT0}, Summary: "moved the ticket to resolved", ActorLabel: "Ada", StateClass: "resolved"},
		{Event: &domain.AuditEvent{Actor: "Ada", Action: domain.ActionUpdate, ToValue: &priority, CreatedAt: assignmentRenderT0}, Summary: "changed priority to high", ActorLabel: "Ada"},
		{Event: &domain.AuditEvent{Actor: "Ada", Action: domain.ActionWorkflowAssignment, ToValue: &to, CreatedAt: assignmentRenderT0}, IsWorkflowAssignment: true, ActorLabel: "Ada", Summary: "assigned the ticket to", AssignmentPerson: "Beto", AssignmentDesk: "Network"},
		{Event: &domain.AuditEvent{Actor: "Ada", Action: domain.ActionWorkflowRequesterForm, CreatedAt: assignmentRenderT0}, Summary: "submitted request details", ActorLabel: "Ada", StepFields: []application.WorkflowResponseField{{Label: "Server", Value: "api-01"}}},
	}
	body := renderTimelineFragment(t, items)
	for _, want := range []string{
		`<strong class="timeline-actor">Ada</strong> added a public comment`,
		`<strong class="timeline-actor">Ada</strong> added an internal comment`,
		`<strong class="timeline-actor">Ada</strong> <span class="timeline-action">moved the ticket to resolved</span>`,
		`<strong class="timeline-actor">Ada</strong> <span class="timeline-action">changed priority to high</span>`,
		`<strong class="timeline-actor">Ada</strong> <span class="timeline-action">assigned the ticket to <strong>Beto</strong> at <strong>Network</strong></span>`,
		`<strong class="timeline-actor">Ada</strong> <span class="timeline-action">submitted request details</span>`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("actor-first timeline narrative must contain %q, got: %s", want, body)
		}
	}
	if strings.Contains(body, `</time> · Ada`) {
		t.Fatalf("actor must not be duplicated in timestamp metadata: %s", body)
	}
}

func TestTimelineAssignmentEscapesHostilePersonAndDeskOnce(t *testing.T) {
	body := renderTimelineFragment(t, []application.TimelineItem{
		assignmentRenderItem(`<script>alert(1)</script>`, `Net & Co "x"`),
	})
	want := `assigned the ticket to <strong>&lt;script&gt;alert(1)&lt;/script&gt;</strong> at <strong>Net &amp; Co &#34;x&#34;</strong>`
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
			Summary:        "completed the task",
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
