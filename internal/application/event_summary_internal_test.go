package application

import (
	"testing"

	"github.com/giulianotesta7/tkt/internal/domain"
)

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
