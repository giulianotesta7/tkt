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

// PR10 task 10.2 — WorkflowStepContext resolves ONE pinned step's presentation
// context joined ONLY by the exact persisted step index (never timestamps or
// row order): form steps decode their answers through the immutable pinned
// definition, manual steps carry the pinned instruction, and missing,
// out-of-range, or unpinned contexts degrade to (nil, nil) while corrupt
// persisted answers fail closed.
func TestWorkflowResponseStore_WorkflowStepContextJoinsExactIndex(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	category := seedCategory(t, s, "Step contexts")
	requester := seedUser(t, s, "Requester", "requester@example.com", true)
	def := domain.WorkflowDefinition{
		{Type: domain.StepForm, Form: &domain.FormStep{Actor: domain.FormActorRequester, Fields: []domain.FormField{
			{Key: "region", Label: "Pinned region", Kind: domain.FieldSingleSelect, Options: []string{"eu-west-1", "us-east-1"}},
			{Key: "approved", Label: "Approved", Kind: domain.FieldCheckbox},
		}}},
		{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "Rack the server"}},
		{Type: domain.StepResolve},
	}
	versionID := seedPublished(t, s, category, def)
	ticket := seedPinnedTicket(t, s, domain.Ticket{Number: 1, Title: "T", RequesterName: "Requester", RequesterEmail: "requester@example.com", RequesterUserID: &requester, CategoryID: category, Priority: domain.PriorityMedium, State: domain.StateNew, CreatedAt: testClock, UpdatedAt: testClock, WorkflowVersionID: &versionID})
	seedRun(t, s, ticket.ID, 0, "active", testClock)
	if _, err := s.db.Exec(`INSERT INTO ticket_form_answers(ticket_id, step_index, answers_json, submitted_by_user_id, submitted_at) VALUES (?, 0, ?, ?, ?)`,
		ticket.ID, `["eu-west-1",true]`, requester, formatTime(testClock)); err != nil {
		t.Fatalf("seed answers: %v", err)
	}
	// A second pinned ticket with NO answers row proves the missing-row
	// degradation; a legacy ticket with no pin proves the unpinned degradation.
	noAnswers := seedPinnedTicket(t, s, domain.Ticket{Number: 2, Title: "No answers", RequesterName: "Requester", RequesterEmail: "requester@example.com", RequesterUserID: &requester, CategoryID: category, Priority: domain.PriorityMedium, State: domain.StateNew, CreatedAt: testClock, UpdatedAt: testClock, WorkflowVersionID: &versionID})
	legacy := seedTicket(t, s, domain.Ticket{Number: 3, Title: "Legacy", RequesterName: "Requester", RequesterEmail: "requester@example.com", RequesterUserID: &requester, CategoryID: category, Priority: domain.PriorityMedium, State: domain.StateNew, CreatedAt: testClock, UpdatedAt: testClock})

	store := newWorkflowResponseStore(s.db)

	stepCtx, err := store.WorkflowStepContext(ctx, ticket.ID, 0)
	if err != nil {
		t.Fatalf("form step context: %v", err)
	}
	if stepCtx == nil || stepCtx.Kind != "form" || stepCtx.FormActor != domain.FormActorRequester {
		t.Fatalf("form step context = %+v, want a requester form context", stepCtx)
	}
	if len(stepCtx.Fields) != 2 || stepCtx.Fields[0].Label != "Pinned region" || stepCtx.Fields[0].Value != "eu-west-1" || stepCtx.Fields[1].Value != "true" {
		t.Fatalf("decoded fields = %+v, want pinned labels with typed values", stepCtx.Fields)
	}

	stepCtx, err = store.WorkflowStepContext(ctx, ticket.ID, 1)
	if err != nil {
		t.Fatalf("manual step context: %v", err)
	}
	if stepCtx == nil || stepCtx.Kind != "manual" || stepCtx.Instruction != "Rack the server" {
		t.Fatalf("manual step context = %+v, want the pinned instruction", stepCtx)
	}

	for _, index := range []int{3, -1} {
		stepCtx, err := store.WorkflowStepContext(ctx, ticket.ID, index)
		if err != nil || stepCtx != nil {
			t.Errorf("out-of-range index %d = (%v, %v), want (nil, nil) degradation", index, stepCtx, err)
		}
	}
	if stepCtx, err := store.WorkflowStepContext(ctx, noAnswers.ID, 0); err != nil || stepCtx != nil {
		t.Errorf("missing answers row = (%v, %v), want (nil, nil) degradation", stepCtx, err)
	}
	if stepCtx, err := store.WorkflowStepContext(ctx, legacy.ID, 0); err != nil || stepCtx != nil {
		t.Errorf("unpinned legacy ticket = (%v, %v), want (nil, nil) degradation", stepCtx, err)
	}

	// A semantically corrupt persisted answers row (valid JSON array per the
	// storage CHECK, but mismatching the pinned field count) must fail closed.
	if _, err := s.db.Exec(`UPDATE ticket_form_answers SET answers_json='["only-one"]' WHERE ticket_id=? AND step_index=0`, ticket.ID); err != nil {
		t.Fatalf("corrupt answers: %v", err)
	}
	if _, err := store.WorkflowStepContext(ctx, ticket.ID, 0); err == nil {
		t.Fatal("corrupt persisted answers must fail closed")
	}
}

// Amendment 2 WA.5 — the manual branch of WorkflowStepContext joins the stored
// ticket_manual_solutions row by the event's EXACT persisted step index against
// the immutable pinned definition; a missing or legacy pre-0009 completion
// yields an EMPTY solution (never a fabricated placeholder); the form branch
// never touches the table.
func TestWorkflowStepContext_ManualSolutionJoinsExactPersistedIndex(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	category := seedCategory(t, s, "Solutions")
	requester := seedUserRole(t, s, "Requester", "sol-read@x", true, domain.RoleUser)
	def := domain.WorkflowDefinition{
		{Type: domain.StepForm, Form: &domain.FormStep{Actor: domain.FormActorRequester, Fields: []domain.FormField{
			{Key: "host", Label: "Host", Kind: domain.FieldShortText},
		}}},
		{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "Rack the server"}},
	}
	versionID := seedPublished(t, s, category, def)
	now := testClock
	solved := seedPinnedTicket(t, s, domain.Ticket{Number: 1, Title: "Solved", RequesterName: "Requester", RequesterEmail: "sol-read@x", RequesterUserID: &requester, CategoryID: category, Priority: domain.PriorityMedium, State: domain.StateNew, CreatedAt: now, UpdatedAt: now, WorkflowVersionID: &versionID})
	seedRun(t, s, solved.ID, 2, "active", now)
	if _, err := s.db.Exec(`INSERT INTO ticket_manual_solutions (ticket_id, step_index, solution, created_by_user_id, created_at) VALUES (?, 1, ?, ?, ?)`,
		solved.ID, "<script>alert(1)</script> racked", requester, formatTime(now)); err != nil {
		t.Fatalf("seed solution: %v", err)
	}

	store := newWorkflowResponseStore(s.db)

	stepCtx, err := store.WorkflowStepContext(ctx, solved.ID, 1)
	if err != nil {
		t.Fatalf("manual step context: %v", err)
	}
	if stepCtx == nil || stepCtx.Kind != "manual" || stepCtx.Instruction != "Rack the server" {
		t.Fatalf("manual context = %+v, want pinned instruction", stepCtx)
	}
	if stepCtx.Solution != "<script>alert(1)</script> racked" {
		t.Fatalf("manual context Solution = %q, want the stored value joined by exact index", stepCtx.Solution)
	}

	// The form branch NEVER joins the table: even with a row physically keyed
	// at the form step's index, the form context carries no solution.
	if _, err := s.db.Exec(`INSERT INTO ticket_form_answers (ticket_id, step_index, answers_json, submitted_by_user_id, submitted_at) VALUES (?, 0, '["api-01"]', ?, ?)`,
		solved.ID, requester, formatTime(now)); err != nil {
		t.Fatalf("seed answers: %v", err)
	}
	if _, err := s.db.Exec(`INSERT INTO ticket_manual_solutions (ticket_id, step_index, solution, created_by_user_id, created_at) VALUES (?, 0, 'stray', ?, ?)`,
		solved.ID, requester, formatTime(now)); err != nil {
		t.Fatalf("seed stray row: %v", err)
	}
	formCtx, err := store.WorkflowStepContext(ctx, solved.ID, 0)
	if err != nil {
		t.Fatalf("form step context: %v", err)
	}
	if formCtx == nil || formCtx.Kind != "form" || formCtx.Solution != "" {
		t.Fatalf("form context = %+v, want kind form with empty Solution (table untouched outside the manual branch)", formCtx)
	}

	// Missing row (no solution submitted) and a legacy pre-0009 completion
	// both degrade to an EMPTY solution on an otherwise identical context.
	unsolved := seedPinnedTicket(t, s, domain.Ticket{Number: 2, Title: "Unsolved", RequesterName: "Requester", RequesterEmail: "sol-read@x", RequesterUserID: &requester, CategoryID: category, Priority: domain.PriorityMedium, State: domain.StateNew, CreatedAt: now, UpdatedAt: now, WorkflowVersionID: &versionID})
	seedRun(t, s, unsolved.ID, 2, "active", now)
	legacyCtx, err := store.WorkflowStepContext(ctx, unsolved.ID, 1)
	if err != nil {
		t.Fatalf("legacy manual context: %v", err)
	}
	if legacyCtx == nil || legacyCtx.Kind != "manual" || legacyCtx.Instruction != "Rack the server" || legacyCtx.Solution != "" {
		t.Fatalf("legacy context = %+v, want instruction alone with empty Solution (no placeholder)", legacyCtx)
	}
}
