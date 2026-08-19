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
		{"reopen from resolved", domain.AuditEvent{Action: domain.ActionTransition, FromValue: str("resolved"), ToValue: str("in_progress")}, "Ticket Reopened"},
		{"reopen from closed", domain.AuditEvent{Action: domain.ActionTransition, FromValue: str("closed"), ToValue: str("in_progress")}, "Ticket Reopened"},
		{"in progress", domain.AuditEvent{Action: domain.ActionTransition, FromValue: str("new"), ToValue: str("in_progress")}, "Ticket in progress"},
		{"resolve", domain.AuditEvent{Action: domain.ActionTransition, FromValue: str("in_progress"), ToValue: str("resolved")}, "Ticket resolved"},
		{"close", domain.AuditEvent{Action: domain.ActionTransition, FromValue: str("resolved"), ToValue: str("closed")}, "Ticket closed"},
		{"cancel", domain.AuditEvent{Action: domain.ActionTransition, FromValue: str("in_progress"), ToValue: str("cancelled")}, "Ticket cancelled"},
		{"created", domain.AuditEvent{Action: domain.ActionCreated}, "Ticket created"},
		{"update field", domain.AuditEvent{Action: domain.ActionUpdate, Field: str("title")}, "Changed Title"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := eventSummary(&tc.ev); got != tc.want {
				t.Errorf("eventSummary = %q, want %q", got, tc.want)
			}
		})
	}
}
