package sqlite

import (
	"context"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/domain"
)

func TestWorkflowResponseStore_PinnedDefinitionAndMalformedIndexes(t *testing.T) {
	s := newTestDB(t)
	category := seedCategory(t, s, "Infrastructure")
	requester := seedUser(t, s, "Requester", "requester@example.com", true)
	v1 := domain.WorkflowDefinition{{Type: domain.StepForm, Form: &domain.FormStep{Actor: domain.FormActorRequester, Fields: []domain.FormField{
		{Key: "region", Label: "Pinned v1 region", Kind: domain.FieldSingleSelect, Options: []string{"eu-west-1", "us-east-1"}},
		{Key: "approved", Label: "Approved", Kind: domain.FieldCheckbox},
	}}}}
	versionID := seedPublished(t, s, category, v1)
	v2 := domain.WorkflowDefinition{{Type: domain.StepForm, Form: &domain.FormStep{Actor: domain.FormActorRequester, Fields: []domain.FormField{{Key: "region", Label: "Published v2 region", Kind: domain.FieldSingleSelect, Options: []string{"ap-south-1", "us-east-1"}}}}}}
	v2JSON, err := v2.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO workflow_versions(category_id, version_no, steps_json, published_at) VALUES (?, 2, ?, ?)`, category, string(v2JSON), "2026-08-07T10:00:00Z"); err != nil {
		t.Fatal(err)
	}
	ticket := seedPinnedTicket(t, s, domain.Ticket{Number: 1, Title: "T", RequesterName: "Requester", RequesterEmail: "requester@example.com", RequesterUserID: &requester, CategoryID: category, Priority: domain.PriorityMedium, State: domain.StateNew, CreatedAt: testClock, UpdatedAt: testClock, WorkflowVersionID: &versionID})
	seedRun(t, s, ticket.ID, 0, "active", testClock)
	if _, err := s.db.Exec(`INSERT INTO ticket_form_answers(ticket_id, step_index, answers_json, submitted_by_user_id, submitted_at) VALUES (?, 0, ?, ?, ?)`, ticket.ID, `["eu-west-1",true]`, requester, formatTime(testClock)); err != nil {
		t.Fatal(err)
	}

	responses, err := s.WorkflowResponseStore().ListWorkflowResponses(context.Background(), ticket.ID)
	if err != nil {
		t.Fatalf("ListWorkflowResponses: %v", err)
	}
	if len(responses) != 1 || responses[0].Fields[0].Label != "Pinned v1 region" || responses[0].Fields[0].Value != "eu-west-1" {
		t.Fatalf("responses = %+v", responses)
	}

	if _, err := s.db.Exec(`UPDATE ticket_form_answers SET answers_json=? WHERE ticket_id=? AND step_index=0`, `["outside-pinned-options",true]`, ticket.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.WorkflowResponseStore().ListWorkflowResponses(context.Background(), ticket.ID); err == nil || !strings.Contains(err.Error(), "outside pinned options") {
		t.Fatalf("out-of-definition single_select must fail safely, got %v", err)
	}
	if _, err := s.db.Exec(`UPDATE ticket_form_answers SET answers_json=? WHERE ticket_id=? AND step_index=0`, `["eu-west-1",true]`, ticket.ID); err != nil {
		t.Fatal(err)
	}

	if _, err := s.db.Exec(`INSERT INTO ticket_form_answers(ticket_id, step_index, answers_json, submitted_by_user_id, submitted_at) VALUES (?, 99, ?, ?, ?)`, ticket.ID, `["ignored"]`, requester, formatTime(testClock)); err != nil {
		t.Fatal(err)
	}
	_, err = s.WorkflowResponseStore().ListWorkflowResponses(context.Background(), ticket.ID)
	if err == nil || !strings.Contains(err.Error(), "step index") {
		t.Fatalf("malformed persisted index must fail safely, got %v", err)
	}
	if _, err := s.db.Exec(`PRAGMA ignore_check_constraints=ON`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO ticket_form_answers(ticket_id, step_index, answers_json, submitted_by_user_id, submitted_at) VALUES (?, -1, ?, ?, ?)`, ticket.ID, `["ignored"]`, requester, formatTime(testClock)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.WorkflowResponseStore().ListWorkflowResponses(context.Background(), ticket.ID); err == nil || !strings.Contains(err.Error(), "out of pinned definition bounds") {
		t.Fatalf("negative persisted index must fail safely, got %v", err)
	}
}

func TestWorkflowResponseStore_RejectsDuplicatePersistedRows(t *testing.T) {
	s := newTestDB(t)
	category := seedCategory(t, s, "Infrastructure")
	requester := seedUser(t, s, "Requester", "requester@example.com", true)
	workflow := domain.WorkflowDefinition{{Type: domain.StepForm, Form: &domain.FormStep{Actor: domain.FormActorRequester, Fields: []domain.FormField{{Key: "host", Label: "Host", Kind: domain.FieldShortText}}}}}
	versionID := seedPublished(t, s, category, workflow)
	ticket := seedPinnedTicket(t, s, domain.Ticket{Number: 1, Title: "T", RequesterName: "Requester", RequesterEmail: "requester@example.com", RequesterUserID: &requester, CategoryID: category, Priority: domain.PriorityMedium, State: domain.StateNew, CreatedAt: testClock, UpdatedAt: testClock, WorkflowVersionID: &versionID})

	if _, err := s.db.Exec(`DROP TABLE ticket_form_answers; CREATE TABLE ticket_form_answers (ticket_id INTEGER, step_index INTEGER, answers_json TEXT, submitted_by_user_id INTEGER, submitted_at TEXT)`); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := s.db.Exec(`INSERT INTO ticket_form_answers(ticket_id, step_index, answers_json, submitted_by_user_id, submitted_at) VALUES (?, 0, ?, ?, ?)`, ticket.ID, `["api-01"]`, requester, formatTime(testClock)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.WorkflowResponseStore().ListWorkflowResponses(context.Background(), ticket.ID); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate persisted response rows must fail safely, got %v", err)
	}
}

func TestDecodeWorkflowResponseFields_StrictPinnedTypes(t *testing.T) {
	definition := []domain.FormField{
		{Label: "Name", Kind: domain.FieldShortText, Required: true},
		{Label: "Approved", Kind: domain.FieldCheckbox, Required: true},
		{Label: "Region", Kind: domain.FieldSingleSelect, Options: []string{"eu-west-1", "us-east-1"}},
	}
	for _, tt := range []struct {
		name   string
		raw    string
		want   string
		absent string
	}{
		{"valid typed positional values", `["api-01",true,"eu-west-1"]`, "", ""},
		{"wrong answer count", `["api-01",true]`, "answer count", ""},
		{"checkbox string", `["api-01","true","eu-west-1"]`, "boolean", ""},
		{"required checkbox false", `["api-01",false,"eu-west-1"]`, "required checkbox", ""},
		{"select outside pinned options", `["api-01",true,"secret-option"]`, "outside pinned options", "secret-option"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := decodeWorkflowResponseFields(definition, []byte(tt.raw))
			if tt.want == "" && err != nil {
				t.Fatalf("decode: %v", err)
			}
			if tt.want != "" && (err == nil || !strings.Contains(err.Error(), tt.want)) {
				t.Fatalf("decode error = %v, want %q", err, tt.want)
			}
			if err != nil && tt.absent != "" && strings.Contains(err.Error(), tt.absent) {
				t.Fatalf("decode error exposed raw value: %v", err)
			}
		})
	}
}
