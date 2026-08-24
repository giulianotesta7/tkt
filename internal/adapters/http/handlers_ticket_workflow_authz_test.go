package httpadapter

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// PR9 task 9.2 TRIANGULATE — forged + stale + XSS edge on the 9.1 GREEN
// runtime. Every test here is a behavior probe: authorization must come from
// PERSISTED ticket/run facts (ticket requester, current assignee, run cursor),
// never from request claims (a forged assignee_id/reason/actor is ignored).
// All names match the focused command
// `-run 'TestTicketWorkflow_Authz|TestTicketWorkflow_Stale'`.

// helper: publish a single requester/assignee form workflow for a category.
func (h *harness) publishFormWorkflow(t *testing.T, catID int64, actor domain.FormActor, fields ...domain.FormField) {
	t.Helper()
	h.publishWorkflow(t, catID, domain.WorkflowDefinition{{
		Type: domain.StepForm,
		Form: &domain.FormStep{Actor: actor, Fields: fields},
	}})
}

// sessionFor returns a live session token for a seeded user.
func (h *harness) sessionFor(t *testing.T, userID int64) string {
	t.Helper()
	return seedSession(t, h.store, userID).ID
}

// assertNoWorkflowWrites proves a denied/rejected completion persisted nothing:
// no answer row, no completion audit of ANY closed workflow action, and an
// unchanged run cursor.
func assertNoWorkflowWrites(t *testing.T, h *harness, ticketID int64) {
	t.Helper()
	db := h.rawDB(t)
	if n := scanOneInt(t, db, "SELECT COUNT(*) FROM ticket_form_answers WHERE ticket_id=?", ticketID); n != 0 {
		t.Errorf("answer rows = %d, want 0 (denial must not write answers)", n)
	}
	if n := scanOneInt(t, db, "SELECT COUNT(*) FROM audit_events WHERE ticket_id=? AND action IN ('workflow_step','workflow_manual_task','workflow_requester_form','workflow_assignee_form','workflow_assignment')", ticketID); n != 0 {
		t.Errorf("workflow completion audits = %d, want 0 (denial must not write completion audits)", n)
	}
	if cur := scanOneInt(t, db, "SELECT current_step_index FROM ticket_workflow_runs WHERE ticket_id=?", ticketID); cur != 0 {
		t.Errorf("run cursor = %d, want 0 (denial must not advance the run)", cur)
	}
}

// TestTicketWorkflow_Authz_RequesterFormDeniedNonRequester proves a forged
// form[requester] completion by someone who is NOT the ticket requester is
// denied 403 with no writes, while the actual requester may complete — the
// decision comes from the PERSISTED requester_user_id, not any request claim.
func TestTicketWorkflow_Authz_RequesterFormDeniedNonRequester(t *testing.T) {
	h := newHarness(t)
	cat, err := h.categories.Create(t.Context(), "ReqOnly")
	if err != nil {
		t.Fatalf("create category: %v", err)
	}
	h.publishFormWorkflow(t, cat.ID, domain.FormActorRequester,
		domain.FormField{Key: "host", Label: "Host", Kind: domain.FieldShortText, Required: true})

	// Requester is a plain user-role user (ScopeOwned); the harness admin is a
	// DIFFERENT user who is not the requester and must NOT be able to complete.
	req := seedUserRole(t, h.store, "Requester Alice", "alice@tkt.test", domain.RoleUser)
	tkt, err := h.tickets.Create(t.Context(), *req, application.CreateTicketInput{
		Title: "requester owned", Description: "d", CategoryID: cat.ID, Priority: domain.PriorityMedium,
	})
	if err != nil {
		t.Fatalf("create requester ticket: %v", err)
	}
	id := strconv.FormatInt(tkt.ID, 10)
	if tkt.RequesterUserID == nil || *tkt.RequesterUserID != req.ID {
		t.Fatalf("requester fixture must be owned by alice, got %v", tkt.RequesterUserID)
	}

	// Non-requester (admin) posting to form[requester] is denied, writes nothing.
	rec := h.postForm(t, "/tickets/"+id+"/workflow/steps/1/complete", url.Values{"answer_0": {"x"}}, false)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-requester status = %d, want 403 (body %.300s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "form requires the ticket requester") {
		t.Errorf("403 must carry plain requester message, body: %.300s", rec.Body.String())
	}
	assertNoWorkflowWrites(t, h, tkt.ID)

	// Positive control: the actual requester may complete their own form (200).
	ok := h.postFormAs(t, "/tickets/"+id+"/workflow/steps/1/complete", url.Values{"answer_0": {"api-01"}}, h.sessionFor(t, req.ID))
	if ok.Code != http.StatusOK {
		t.Fatalf("requester completion status = %d, want 200 (body %.300s)", ok.Code, ok.Body.String())
	}
}

// TestTicketWorkflow_Authz_AssigneeFormDeniedNonAssignee proves a forged
// form[assignee] completion by a non-assignee is denied 403 with no writes,
// while the current assignee may complete — the decision comes from the
// PERSISTED tickets.user_id, never a submitted assignee_id.
func TestTicketWorkflow_Authz_AssigneeFormDeniedNonAssignee(t *testing.T) {
	h := newHarness(t)
	cat, err := h.categories.Create(t.Context(), "AssigneeOnly")
	if err != nil {
		t.Fatalf("create category: %v", err)
	}
	h.publishFormWorkflow(t, cat.ID, domain.FormActorAssignee,
		domain.FormField{Key: "env", Label: "Environment", Kind: domain.FieldShortText, Required: true})

	assignee := h.createUser(t, "Assignee Bob", "bob@tkt.test", "secret")
	tkt := h.seedTicket(t, "assignee form", func(in *application.CreateTicketInput) { in.CategoryID = cat.ID })
	h.assignTicket(t, tkt.ID, assignee.ID)
	id := strconv.FormatInt(tkt.ID, 10)

	// The harness admin is NOT the assignee; even a forged assignee_id must be
	// ignored (authz from persisted facts). Denied 403 with no writes.
	rec := h.postForm(t, "/tickets/"+id+"/workflow/steps/1/complete",
		url.Values{"answer_0": {"prod"}, "assignee_id": {strconv.FormatInt(h.admin.ID, 10)}}, false)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-assignee status = %d, want 403 (body %.300s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "form requires the current assignee") {
		t.Errorf("403 must carry plain assignee message, body: %.300s", rec.Body.String())
	}
	assertNoWorkflowWrites(t, h, tkt.ID)

	// Positive control: the actual assignee completes (200).
	ok := h.postFormAs(t, "/tickets/"+id+"/workflow/steps/1/complete", url.Values{"answer_0": {"prod"}}, h.sessionFor(t, assignee.ID))
	if ok.Code != http.StatusOK {
		t.Fatalf("assignee completion status = %d, want 200 (body %.300s)", ok.Code, ok.Body.String())
	}
}

// TestTicketWorkflow_Authz_ManualTaskDeniedNonAssignee proves a manual_task
// completion by a non-assignee is denied 403 with no writes; only the current
// assignee may mark the step done.
func TestTicketWorkflow_Authz_ManualTaskDeniedNonAssignee(t *testing.T) {
	h := newHarness(t)
	cat, err := h.categories.Create(t.Context(), "ManualOnly")
	if err != nil {
		t.Fatalf("create category: %v", err)
	}
	h.publishWorkflow(t, cat.ID, domain.WorkflowDefinition{{
		Type:       domain.StepManualTask,
		ManualTask: &domain.ManualTaskStep{Instructions: "inspect the server"},
	}})

	assignee := h.createUser(t, "Assignee Carol", "carol@tkt.test", "secret")
	tkt := h.seedTicket(t, "manual task", func(in *application.CreateTicketInput) { in.CategoryID = cat.ID })
	h.assignTicket(t, tkt.ID, assignee.ID)
	id := strconv.FormatInt(tkt.ID, 10)

	// Non-assignee (admin) posting manual completion is denied 403, no writes.
	rec := h.postForm(t, "/tickets/"+id+"/workflow/steps/1/complete", url.Values{}, false)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-assignee status = %d, want 403 (body %.300s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "manual_task requires the current assignee") {
		t.Errorf("403 must carry plain manual message, body: %.300s", rec.Body.String())
	}
	assertNoWorkflowWrites(t, h, tkt.ID)

	// Positive control: the actual assignee completes the manual step (200).
	ok := h.postFormAs(t, "/tickets/"+id+"/workflow/steps/1/complete", url.Values{}, h.sessionFor(t, assignee.ID))
	if ok.Code != http.StatusOK {
		t.Fatalf("assignee manual completion status = %d, want 200 (body %.300s)", ok.Code, ok.Body.String())
	}
}

// TestTicketWorkflow_Authz_PositionalAnswerRejectedBeforeWrite proves unknown/
// duplicate/extra/ambiguous answer positions are rejected with a plain 422
// BEFORE any write: no answer row is stored and the run cursor stays put.
func TestTicketWorkflow_Authz_PositionalAnswerRejectedBeforeWrite(t *testing.T) {
	h := newHarness(t)
	cat, err := h.categories.Create(t.Context(), "RejectAns")
	if err != nil {
		t.Fatalf("create category: %v", err)
	}
	h.publishFormWorkflow(t, cat.ID, domain.FormActorRequester,
		domain.FormField{Key: "host", Label: "Host", Kind: domain.FieldShortText, Required: true})

	req := seedUserRole(t, h.store, "Requester Dan", "dan@tkt.test", domain.RoleUser)
	tkt, err := h.tickets.Create(t.Context(), *req, application.CreateTicketInput{
		Title: "reject answers", Description: "d", CategoryID: cat.ID, Priority: domain.PriorityMedium,
	})
	if err != nil {
		t.Fatalf("create ticket: %v", err)
	}
	id := strconv.FormatInt(tkt.ID, 10)
	sess := h.sessionFor(t, req.ID)

	cases := []struct {
		name string
		form url.Values
		want string // expected plain per-step rejection text fragment
	}{
		{"unknown/extra position", url.Values{"answer_9": {"x"}}, "unknown position 9"},
		{"duplicate position", url.Values{"answer_0": {"a"}, "answer_00": {"b"}}, "duplicate position 0"},
		{"ambiguous repeated value", url.Values{"answer_0": {"a", "b"}}, "ambiguous values for position 0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := h.postFormAs(t, "/tickets/"+id+"/workflow/steps/1/complete", tc.form, sess)
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422 (body %.200s)", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tc.want) {
				t.Errorf("422 must carry plain rejection %q, body: %.300s", tc.want, rec.Body.String())
			}
		})
	}

	// None of the rejected attempts wrote an answer row or advanced the cursor.
	// (The valid-answer positive control lives in the 9.1 runtime suite.)
	db := h.rawDB(t)
	if n := scanOneInt(t, db, "SELECT COUNT(*) FROM ticket_form_answers WHERE ticket_id=?", tkt.ID); n != 0 {
		t.Errorf("answer rows = %d, want 0 (rejected attempts must not write)", n)
	}
	if cur := scanOneInt(t, db, "SELECT current_step_index FROM ticket_workflow_runs WHERE ticket_id=?", tkt.ID); cur != 0 {
		t.Errorf("run cursor = %d, want 0 (rejected attempts must not advance)", cur)
	}
}

// TestTicketWorkflow_Authz_XSSAnswerStoredTypedRenderedEscaped proves an
// answer_0 XSS payload is stored as a TYPED JSON string (never raw HTML) and
// rendered HTML-escaped in the completed-form card (never reflected raw).
func TestTicketWorkflow_Authz_XSSAnswerStoredTypedRenderedEscaped(t *testing.T) {
	h := newHarness(t)
	cat, err := h.categories.Create(t.Context(), "XSS")
	if err != nil {
		t.Fatalf("create category: %v", err)
	}
	h.publishFormWorkflow(t, cat.ID, domain.FormActorRequester,
		domain.FormField{Key: "host", Label: "Host", Kind: domain.FieldShortText, Required: true})

	req := seedUserRole(t, h.store, "Requester Eve", "eve@tkt.test", domain.RoleUser)
	tkt, err := h.tickets.Create(t.Context(), *req, application.CreateTicketInput{
		Title: "xss ticket", Description: "d", CategoryID: cat.ID, Priority: domain.PriorityMedium,
	})
	if err != nil {
		t.Fatalf("create ticket: %v", err)
	}
	id := strconv.FormatInt(tkt.ID, 10)
	payload := `<script>alert('xss')</script>`

	// Completing the form with the XSS payload succeeds (200) and the run finishes.
	rec := h.postFormAs(t, "/tickets/"+id+"/workflow/steps/1/complete", url.Values{"answer_0": {payload}}, h.sessionFor(t, req.ID))
	if rec.Code != http.StatusOK {
		t.Fatalf("xss completion status = %d, want 200 (body %.200s)", rec.Code, rec.Body.String())
	}

	// Stored as a TYPED JSON string. The persisted bytes are JSON-escaped at the
	// encoding layer too (go's encoding/json HTML-escapes < > & to \u003c etc.), so
	// the invariant is that the value DECODES to the exact typed payload — it is a
	// JSON string, never a raw HTML fragment sitting in the page state.
	stored := scanOneString(t, h.rawDB(t), "SELECT answers_json FROM ticket_form_answers WHERE ticket_id=? AND step_index=0", tkt.ID)
	var decoded []string
	if err := json.Unmarshal([]byte(stored), &decoded); err != nil {
		t.Fatalf("stored answers_json must be valid JSON array: %v (raw %q)", err, stored)
	}
	if len(decoded) != 1 || decoded[0] != payload {
		t.Errorf("answer stored as typed string, decoded got %#v, want [%q]", decoded, payload)
	}

	// Rendered HTML-escaped: the escaped glyphs appear and the RAW payload never does.
	view := h.get(t, "/tickets/"+id, false)
	if view.Code != http.StatusOK {
		t.Fatalf("detail status = %d, want 200", view.Code)
	}
	body := view.Body.String()
	if !strings.Contains(body, "&lt;script&gt;") {
		t.Errorf("completed-form card must render the XSS value HTML-escaped, body: %.400s", body)
	}
	if strings.Contains(body, payload) {
		t.Errorf("completed-form card must NOT render the raw XSS payload, body: %.400s", body)
	}
	if !strings.Contains(body, `<dl class="workflow-responses">`) {
		t.Errorf("completed forms must render responses inline in the timeline, body: %.400s", body)
	}
	if !strings.Contains(body, `<dt>Host</dt>`) {
		t.Errorf("completed forms must render the pinned field label inline, body: %.400s", body)
	}
	if strings.Contains(body, `<ol class="workflow-response-steps">`) {
		t.Errorf("ticket detail must not render the removed standalone responses card, body: %.400s", body)
	}
}

// TestTicketWorkflow_Authz_PlainEnglishPerStepError proves a required-field
// omission surfaces as plain per-step English ('Step 1: host is required'),
// not a technical/internal message.
func TestTicketWorkflow_Authz_PlainEnglishPerStepError(t *testing.T) {
	h := newHarness(t)
	cat, err := h.categories.Create(t.Context(), "PlainMsg")
	if err != nil {
		t.Fatalf("create category: %v", err)
	}
	h.publishFormWorkflow(t, cat.ID, domain.FormActorRequester,
		domain.FormField{Key: "host", Label: "Host", Kind: domain.FieldShortText, Required: true})

	req := seedUserRole(t, h.store, "Requester Frank", "frank@tkt.test", domain.RoleUser)
	tkt, err := h.tickets.Create(t.Context(), *req, application.CreateTicketInput{
		Title: "plain msg", Description: "d", CategoryID: cat.ID, Priority: domain.PriorityMedium,
	})
	if err != nil {
		t.Fatalf("create ticket: %v", err)
	}
	id := strconv.FormatInt(tkt.ID, 10)

	// Blank required field → 422 with the exact per-step English message.
	rec := h.postFormAs(t, "/tickets/"+id+"/workflow/steps/1/complete", url.Values{"answer_0": {"   "}}, h.sessionFor(t, req.ID))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (body %.200s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Step 1: host is required") {
		t.Errorf("422 must carry plain per-step English 'Step 1: host is required', body: %.300s", rec.Body.String())
	}
}

// TestTicketWorkflow_Stale_AdvancedPosition422NoCursorChange proves a forged/
// already-advanced one-based position on a run that has moved past it returns a
// typed 422 and changes neither the run cursor nor the audit trail.
func TestTicketWorkflow_Stale_AdvancedPosition422NoCursorChange(t *testing.T) {
	h := newHarness(t)
	cat, err := h.categories.Create(t.Context(), "StalePos")
	if err != nil {
		t.Fatalf("create category: %v", err)
	}
	// Two requester form steps: completing step 0 advances the run to step 1.
	h.publishWorkflow(t, cat.ID, domain.WorkflowDefinition{
		{Type: domain.StepForm, Form: &domain.FormStep{Actor: domain.FormActorRequester, Fields: []domain.FormField{
			{Key: "host", Label: "Host", Kind: domain.FieldShortText, Required: true}}}},
		{Type: domain.StepForm, Form: &domain.FormStep{Actor: domain.FormActorRequester, Fields: []domain.FormField{
			{Key: "region", Label: "Region", Kind: domain.FieldShortText, Required: true}}}},
	})

	req := seedUserRole(t, h.store, "Requester Grace", "grace@tkt.test", domain.RoleUser)
	tkt, err := h.tickets.Create(t.Context(), *req, application.CreateTicketInput{
		Title: "stale pos", Description: "d", CategoryID: cat.ID, Priority: domain.PriorityMedium,
	})
	if err != nil {
		t.Fatalf("create ticket: %v", err)
	}
	id := strconv.FormatInt(tkt.ID, 10)
	db := h.rawDB(t)
	sess := h.sessionFor(t, req.ID)

	// Advance the run to cursor 1 by completing step 0 (position 1).
	ok := h.postFormAs(t, "/tickets/"+id+"/workflow/steps/1/complete", url.Values{"answer_0": {"api-01"}}, sess)
	if ok.Code != http.StatusOK {
		t.Fatalf("first completion status = %d, want 200 (body %.200s)", ok.Code, ok.Body.String())
	}
	if cur := scanOneInt(t, db, "SELECT current_step_index FROM ticket_workflow_runs WHERE ticket_id=?", tkt.ID); cur != 1 {
		t.Fatalf("run cursor after first completion = %d, want 1", cur)
	}

	// Re-posting the now-stale one-based position (1, already consumed) → 422,
	// cursor unchanged, no additional answer row, no additional completion audit.
	rec := h.postFormAs(t, "/tickets/"+id+"/workflow/steps/1/complete", url.Values{"answer_0": {"forged"}}, sess)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("stale position status = %d, want 422 (body %.200s)", rec.Code, rec.Body.String())
	}
	if cur := scanOneInt(t, db, "SELECT current_step_index FROM ticket_workflow_runs WHERE ticket_id=?", tkt.ID); cur != 1 {
		t.Errorf("stale 422 must not move the cursor: got %d, want 1", cur)
	}
	if n := scanOneInt(t, db, "SELECT COUNT(*) FROM ticket_form_answers WHERE ticket_id=?", tkt.ID); n != 1 {
		t.Errorf("answer rows = %d, want 1 (only the first real completion)", n)
	}
	if n := scanOneInt(t, db, "SELECT COUNT(*) FROM audit_events WHERE ticket_id=? AND action IN ('workflow_step','workflow_manual_task','workflow_requester_form','workflow_assignee_form')", tkt.ID); n != 1 {
		t.Errorf("completion audits = %d, want 1 (stale 422 must not add an audit)", n)
	}
}
