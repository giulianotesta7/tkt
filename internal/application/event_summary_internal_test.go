package application

import (
	"testing"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// TestEventSummaryMsg pins the catalogue references eventSummaryMsg attaches
// to each timeline action: the rendered narrative follows the instance
// language, and a known lifecycle state interpolates a translated placeholder
// instead of its raw code (issue #335, S8).
func TestEventSummaryMsg(t *testing.T) {
	str := func(s string) *string { return &s }
	cases := []struct {
		name    string
		ev      domain.AuditEvent
		key     string
		argKey  string
		argText string
	}{
		{"reopen", domain.AuditEvent{Action: domain.ActionTransition, FromValue: str("resolved"), ToValue: str("in_progress")}, "tickets.timeline.narrative.reopened", "", ""},
		{"known state move", domain.AuditEvent{Action: domain.ActionTransition, FromValue: str("new"), ToValue: str("in_progress")}, "tickets.timeline.narrative.moved_to", "tickets.timeline.state.in_progress", ""},
		{"unknown state falls back to text", domain.AuditEvent{Action: domain.ActionTransition, FromValue: str("new"), ToValue: str("weird")}, "tickets.timeline.narrative.moved_to", "", "weird"},
		{"created", domain.AuditEvent{Action: domain.ActionCreated}, "tickets.timeline.narrative.created", "", ""},
		{"field update", domain.AuditEvent{Action: domain.ActionUpdate, Field: str("title")}, "tickets.timeline.narrative.changed", "tickets.timeline.field.title", ""},
		{"workflow assignment", domain.AuditEvent{Action: domain.ActionWorkflowAssignment}, "tickets.timeline.narrative.assigned", "", ""},
		{"manual task", domain.AuditEvent{Action: domain.ActionWorkflowManualTask}, "tickets.timeline.narrative.completed_task", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := eventSummaryMsg(&tc.ev)
			if got.Key != tc.key {
				t.Fatalf("eventSummaryMsg key = %q, want %q", got.Key, tc.key)
			}
			if tc.argKey == "" && tc.argText == "" {
				if len(got.Args) != 0 {
					t.Fatalf("eventSummaryMsg args = %+v, want none", got.Args)
				}
				return
			}
			if len(got.Args) != 1 {
				t.Fatalf("eventSummaryMsg args = %+v, want one", got.Args)
			}
			if got.Args[0].Key != tc.argKey || got.Args[0].Value != tc.argText {
				t.Errorf("arg = %+v, want key %q text %q", got.Args[0], tc.argKey, tc.argText)
			}
		})
	}
}

func TestEventSummaryReopen(t *testing.T) {
	str := func(s string) *string { return &s }
	cases := []struct {
		name string
		ev   domain.AuditEvent
		want string
	}{
		{"reopen from resolved", domain.AuditEvent{Action: domain.ActionTransition, FromValue: str("resolved"), ToValue: str("in_progress")}, "reopened the ticket"},
		{"reopen from closed", domain.AuditEvent{Action: domain.ActionTransition, FromValue: str("closed"), ToValue: str("in_progress")}, "reopened the ticket"},
		{"in progress", domain.AuditEvent{Action: domain.ActionTransition, FromValue: str("new"), ToValue: str("in_progress")}, "moved the ticket to in progress"},
		{"resolve", domain.AuditEvent{Action: domain.ActionTransition, FromValue: str("in_progress"), ToValue: str("resolved")}, "moved the ticket to resolved"},
		{"close", domain.AuditEvent{Action: domain.ActionTransition, FromValue: str("resolved"), ToValue: str("closed")}, "moved the ticket to closed"},
		{"cancel", domain.AuditEvent{Action: domain.ActionTransition, FromValue: str("in_progress"), ToValue: str("cancelled")}, "moved the ticket to cancelled"},
		{"created", domain.AuditEvent{Action: domain.ActionCreated}, "created the ticket"},
		{"update field", domain.AuditEvent{Action: domain.ActionUpdate, Field: str("title")}, "changed title"},
		{"workflow assignment reads the sentence prefix", domain.AuditEvent{Action: domain.ActionWorkflowAssignment}, "assigned the ticket to"},
		{"workflow manual task", domain.AuditEvent{Action: domain.ActionWorkflowManualTask}, "completed the task"},
		{"workflow requester form", domain.AuditEvent{Action: domain.ActionWorkflowRequesterForm}, "submitted request details"},
		{"workflow assignee form", domain.AuditEvent{Action: domain.ActionWorkflowAssigneeForm}, "submitted work details"},
		{"legacy workflow_step reads as completed the step", domain.AuditEvent{Action: domain.ActionWorkflowStep}, "completed the step"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := eventSummary(&tc.ev); got != tc.want {
				t.Errorf("eventSummary = %q, want %q", got, tc.want)
			}
		})
	}
}
