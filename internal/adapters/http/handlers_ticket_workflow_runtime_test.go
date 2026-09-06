package httpadapter

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// PR9 task 9.1 RED contract — ticket HTTP runtime + published-only options.
//
// Every test named TestTicketWorkflowRuntime_* drives the mux as a browser over
// HTTP and is RED on the current tree: the published-only create filter, the
// honest /workflow/steps/{position}/complete route, and the Pending Actions
// card are absent, so these assertions fail with a 404 or a missing-control
// behavior mismatch rather than a compile error. Nothing here references a
// production symbol that does not exist yet.

// mustHaveCompletionRoute proves the completion route is REGISTERED (not a
// 404) and answered with the design status. Until PR9 lands the route is
// unregistered, so reporting 404 is the exact RED signal: absent handler, not
// a compile error and not a wrong-status branch.
func mustHaveCompletionRoute(t *testing.T, rec *httptest.ResponseRecorder, want int, what string) {
	t.Helper()
	// An unregistered PR9 route surfaces as 404 (no path) or 405 (path matched by
	// a different-method pattern, e.g. the GET ticket-detail wildcard) on the
	// current tree. Either way the completion handler is absent: report it as the
	// RED signal rather than a wrong-status branch.
	if rec.Code == http.StatusNotFound || rec.Code == http.StatusMethodNotAllowed {
		t.Errorf("%s: completion route is NOT registered (%d) — PR9 handler absent (got %d, want %d)", what, rec.Code, rec.Code, want)
		return
	}
	if rec.Code != want {
		t.Errorf("%s: status = %d, want %d", what, rec.Code, want)
	}
}

// TestTicketWorkflowRuntime_CreateOptionsPublishedOnly proves GET /tickets/new
// filters category options through WorkflowStore.ListAvailableCategories: an
// existing category without a published workflow is absent for the acting role
// while a published category remains listable. RED: today collectOptions lists
// every category, so the unpublished option leaks.
func TestTicketWorkflowRuntime_CreateOptionsPublishedOnly(t *testing.T) {
	h := newHarness(t)
	noWf, err := h.categories.Create(t.Context(), "UnpublishedCat")
	if err != nil {
		t.Fatalf("create unpublished category: %v", err)
	}

	rec := h.get(t, "/tickets/new", false)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	// Match on the option VALUE (category option elements can carry template
	// whitespace before the closing >), which is unambiguous to the category id.
	publishedValue := `value="` + strconv.FormatInt(h.bugCategory.ID, 10) + `"`
	if !strings.Contains(body, publishedValue) {
		t.Errorf("published category option must remain listable, missing %q", publishedValue)
	}
	unpublishedValue := `value="` + strconv.FormatInt(noWf.ID, 10) + `"`
	if strings.Contains(body, unpublishedValue) {
		t.Errorf("unpublished category must be filtered out of create options, found %q", unpublishedValue)
	}
}

// TestTicketWorkflowRuntime_CreateUnavailableCategory422Exact locks the exact
// 422 category contract for POST /tickets with an unavailable category and
// proves no ticket/audit/run rows are written. This assertion already holds
// from the workflow-aware create path; it pins the message for PR9.
func TestTicketWorkflowRuntime_CreateUnavailableCategory422Exact(t *testing.T) {
	h := newHarness(t)
	noWf, err := h.categories.Create(t.Context(), "NoWorkflow")
	if err != nil {
		t.Fatalf("create category: %v", err)
	}
	form := ticketForm(func(f url.Values) { f.Set("category_id", strconv.FormatInt(noWf.ID, 10)) })
	rec := h.postForm(t, "/tickets", form, false)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), domain.ErrMsgCategoryWorkflowUnavailable) {
		t.Errorf("re-render must carry %q, got: %s", domain.ErrMsgCategoryWorkflowUnavailable, rec.Body.String())
	}
	db := h.rawDB(t)
	if n := scanOneInt(t, db, "SELECT COUNT(*) FROM tickets"); n != 0 {
		t.Errorf("tickets rows = %d, want 0", n)
	}
	if n := scanOneInt(t, db, "SELECT COUNT(*) FROM ticket_workflow_runs"); n != 0 {
		t.Errorf("ticket_workflow_runs rows = %d, want 0", n)
	}
}

// TestTicketWorkflowRuntime_CompletionPositionConflict422 proves the ONLY
// completion route POST /tickets/{id}/workflow/steps/{position}/complete maps
// stale/missing/non-positive/mismatched one-based positions to a typed
// ErrWorkflowPositionConflict → 422 with no writes. RED: the route is not
// registered yet, so every sub-case 404s.
func TestTicketWorkflowRuntime_CompletionPositionConflict422(t *testing.T) {
	h := newHarness(t)
	// Bugs has a published manual_task run: active at cursor 0, one step.
	tkt := h.seedTicket(t, "position conflict", nil)
	id := strconv.FormatInt(tkt.ID, 10)

	cases := []struct {
		name string
		pos  string
	}{
		{"nonpositive position", "0"},            // one-based; 0 is not a valid position
		{"mismatched later position", "2"},       // beyond the single pinned step
		{"missing (out-of-range) position", "9"}, // a step that does not exist
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := h.postForm(t, "/tickets/"+id+"/workflow/steps/"+tc.pos+"/complete", url.Values{}, false)
			mustHaveCompletionRoute(t, rec, http.StatusUnprocessableEntity, tc.name)
		})
	}
}

// TestTicketWorkflowRuntime_CompletionClaimHasNoCallerFields proves a claim
// completion posts no assignee or reason; the authenticated actor is the sole
// claimant for the pinned step.
func TestTicketWorkflowRuntime_CompletionClaimHasNoCallerFields(t *testing.T) {
	h := newHarness(t)
	desk, err := h.desks.Create(t.Context(), *h.admin, "Network")
	if err != nil {
		t.Fatalf("create desk: %v", err)
	}
	cat, err := h.categories.Create(t.Context(), "Claims")
	if err != nil {
		t.Fatalf("create category: %v", err)
	}
	h.publishWorkflow(t, cat.ID, domain.WorkflowDefinition{{
		Type:         domain.StepAssignToDesk,
		AssignToDesk: &domain.AssignToDeskStep{DeskID: desk.ID, Strategy: domain.StrategyClaim},
	}})
	tkt := h.seedTicket(t, "claim me", func(in *application.CreateTicketInput) { in.CategoryID = cat.ID })
	id := strconv.FormatInt(tkt.ID, 10)

	// The actor (harness admin) must be a positioned claimant: add them as a
	// member of the claim desk so the persisted actor predicate passes.
	if err := h.desks.AddMember(t.Context(), *h.admin, desk.ID, h.admin.ID); err != nil {
		t.Fatalf("add admin as desk member: %v", err)
	}
	rec := h.postForm(t, "/tickets/"+id+"/workflow/steps/1/complete", url.Values{}, false)
	mustHaveCompletionRoute(t, rec, http.StatusOK, "claim posts no caller fields")
}

// TestTicketWorkflowRuntime_CompletionFormPositionalAnswers proves a requester
// form completion posts raw answer_<zeroPosition> values decoded against the
// pinned field definitions; unknown/duplicate/extra/ambiguous positions are
// rejected before any write. RED: no completion route exists, so the valid form
// submission 404s.
func TestTicketWorkflowRuntime_CompletionFormPositionalAnswers(t *testing.T) {
	h := newHarness(t)
	cat, err := h.categories.Create(t.Context(), "Forms")
	if err != nil {
		t.Fatalf("create category: %v", err)
	}
	fdef := domain.WorkflowDefinition{{
		Type: domain.StepForm,
		Form: &domain.FormStep{
			Actor:  domain.FormActorRequester,
			Fields: []domain.FormField{{Key: "host", Label: "Host", Kind: domain.FieldShortText, Required: true}},
		},
	}}
	h.publishWorkflow(t, cat.ID, fdef)
	tkt := h.seedTicket(t, "form ticket", func(in *application.CreateTicketInput) { in.CategoryID = cat.ID })
	id := strconv.FormatInt(tkt.ID, 10)

	// Zero-based answer position 0 carries the raw submitted value.
	rec := h.postForm(t, "/tickets/"+id+"/workflow/steps/1/complete", url.Values{"answer_0": {"api-01"}}, false)
	mustHaveCompletionRoute(t, rec, http.StatusOK, "requester form completes with answer_0")

	// Unknown/extra answer positions must be a plain 422 before any write.
	bad := h.postForm(t, "/tickets/"+id+"/workflow/steps/1/complete", url.Values{"answer_5": {"x"}}, false)
	mustHaveCompletionRoute(t, bad, http.StatusUnprocessableEntity, "unknown position rejected before write")
}

// TestTicketWorkflowRuntime_CompletionManualNoMetadata proves a manual_task
// completion posts no completion metadata (no answers, no assignee) — the
// current assignee simply marks the step done. RED: no route, so it 404s.
func TestTicketWorkflowRuntime_CompletionManualNoMetadata(t *testing.T) {
	h := newHarness(t)
	// Bugs is simpleManualDef: a manual_task is the pinned current step. The
	// actor must be the current assignee to complete it (persisted actor
	// predicate); seed the ticket assigned to the harness admin through the
	// audited Assign service path (Amendment 2: creation is unassigned-only).
	adminID := h.admin.ID
	tkt := h.seedTicket(t, "manual step", nil)
	h.assignTicket(t, tkt.ID, adminID)
	id := strconv.FormatInt(tkt.ID, 10)

	// Empty form: manual completion intentionally posts no metadata fields.
	rec := h.postForm(t, "/tickets/"+id+"/workflow/steps/1/complete", url.Values{}, false)
	mustHaveCompletionRoute(t, rec, http.StatusOK, "manual_task completion posts no metadata")

	// A forged answer must never be accepted as manual metadata.
	forged := h.postForm(t, "/tickets/"+id+"/workflow/steps/1/complete", url.Values{"answer_0": {"nope"}}, false)
	mustHaveCompletionRoute(t, forged, http.StatusUnprocessableEntity, "manual_task ignores forged metadata")
}

// TestTicketWorkflowRuntime_PendingActionsInsideTimelineForActiveRun proves an
// active run renders the current task as the first timeline item when the
// persisted actor predicate passes.
func TestTicketWorkflowRuntime_PendingActionsInsideTimelineForActiveRun(t *testing.T) {
	h := newHarness(t)
	tkt := h.seedTicket(t, "active run", nil)
	h.assignTicket(t, tkt.ID, h.admin.ID)
	id := strconv.FormatInt(tkt.ID, 10)

	rec := h.get(t, "/tickets/"+id, false)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	timeline := strings.Index(body, `<div id="timeline">`)
	pending := strings.Index(body, `class="timeline-entry workflow-pending workflow-pending-action"`)
	if timeline < 0 || pending < 0 {
		t.Errorf("active run must render its current task inside Timeline: %.400s", body)
	} else if pending < timeline {
		t.Errorf("current task must render inside Timeline: %.500s", body)
	}
	if !strings.Contains(body, `<h3 id="current-task-title">CURRENT TASK</h3>`) {
		t.Errorf("authorized active run must render CURRENT TASK: %.500s", body)
	}
	// Amendment 2 (WB.5): ordered-list numbering is removed from the pending
	// timeline item everywhere ticket-facing.
	if strings.Contains(body, "workflow-pending-list") || strings.Contains(body, `<ol`) {
		t.Errorf("pending timeline item must NOT use ordered-list numbering: %.500s", body)
	}
}

// TestTicketWorkflowRuntime_LegacyUnpinnedReadableNoVersionExposure proves an
// unpinned legacy ticket (no run, no pin) stays readable, renders no Pending
// Actions card, and exposes no internal workflow version/pin/technical cursor
// or version browser to the requester. This guard holds today; it locks PR9's
// no-leakage contract.
func TestTicketWorkflowRuntime_LegacyUnpinnedReadableNoVersionExposure(t *testing.T) {
	h := newHarness(t)
	legacy := &domain.Ticket{
		Title:           "Legacy unpinned",
		Description:     "Created before workflow support",
		RequesterName:   h.admin.Name,
		RequesterEmail:  h.admin.Email,
		RequesterUserID: &h.admin.ID,
		CategoryID:      h.bugCategory.ID,
		Priority:        domain.PriorityMedium,
		State:           domain.StateNew,
		CreatedAt:       fixedNow,
		UpdatedAt:       fixedNow,
	}
	if err := h.store.TicketStore().Create(t.Context(), legacy); err != nil {
		t.Fatalf("seed legacy unpinned ticket: %v", err)
	}
	pin, ok := scanOneNullableInt(t, h.rawDB(t), "SELECT workflow_version_id FROM tickets WHERE id=?", legacy.ID)
	if ok || pin != 0 {
		t.Fatalf("legacy fixture must be unpinned, got pin=(%d, %v)", pin, ok)
	}

	rec := h.get(t, "/tickets/"+strconv.FormatInt(legacy.ID, 10), false)
	if rec.Code != http.StatusOK {
		t.Fatalf("legacy unpinned ticket must stay readable, status = %d", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "Pending Actions") {
		t.Errorf("legacy ticket without a run must show no Pending Actions card: %.300s", body)
	}
	for _, leak := range []string{"Workflow v", "workflow_version", "version browser", "current_step"} {
		if strings.Contains(body, leak) {
			t.Errorf("detail must not expose internal workflow %q to the requester: %.300s", leak, body)
		}
	}
}

// countSolutions returns the ticket_manual_solutions row count for a ticket
// (Amendment 2 transport probes).
func countSolutions(t *testing.T, h *harness, ticketID int64) int64 {
	t.Helper()
	return scanOneInt(t, h.rawDB(t), "SELECT COUNT(*) FROM ticket_manual_solutions WHERE ticket_id=?", ticketID)
}

// TestCompleteWorkflow_SolutionBound proves the Amendment 2 solution transport:
// completeWorkflow extracts the OPTIONAL solution field, trims surrounding
// whitespace, treats whitespace-only as absent, and rejects a trimmed value
// above 2,000 characters with a typed ValidationError{Field: "solution"} → 422
// BEFORE planning (zero writes). Forged answer_*/assignee_id/user_id keys keep
// their existing rejections; a non-manual step receiving a non-empty solution
// is a plan contradiction rejected with zero writes.
func TestCompleteWorkflow_SolutionBound(t *testing.T) {
	t.Run("2001 chars rejected 422 before planning, zero writes", func(t *testing.T) {
		h := newHarness(t)
		tkt := h.seedTicket(t, "bound reject", nil)
		h.assignTicket(t, tkt.ID, h.admin.ID)
		id := strconv.FormatInt(tkt.ID, 10)

		rec := h.postForm(t, "/tickets/"+id+"/workflow/steps/1/complete",
			url.Values{"solution": {strings.Repeat("a", 2001)}}, false)
		mustHaveCompletionRoute(t, rec, http.StatusUnprocessableEntity, "oversized solution must be a typed 422")
		if !strings.Contains(rec.Body.String(), domain.ErrMsgSolutionTooLong) {
			t.Errorf("422 must carry %q, got: %.400s", domain.ErrMsgSolutionTooLong, rec.Body.String())
		}
		assertNoWorkflowWrites(t, h, tkt.ID)
		if n := countSolutions(t, h, tkt.ID); n != 0 {
			t.Errorf("solution rows = %d, want 0 (rejection must not persist)", n)
		}
	})

	t.Run("exactly 2000 chars completes and stores the trimmed value", func(t *testing.T) {
		h := newHarness(t)
		tkt := h.seedTicket(t, "boundary accept", nil)
		h.assignTicket(t, tkt.ID, h.admin.ID)
		id := strconv.FormatInt(tkt.ID, 10)

		rec := h.postForm(t, "/tickets/"+id+"/workflow/steps/1/complete",
			url.Values{"solution": {"  " + strings.Repeat("a", 2000) + "  "}}, false)
		mustHaveCompletionRoute(t, rec, http.StatusOK, "2000-char trimmed solution is accepted")
		if n := countSolutions(t, h, tkt.ID); n != 1 {
			t.Fatalf("solution rows = %d, want 1", n)
		}
		if got := scanOneString(t, h.rawDB(t), "SELECT solution FROM ticket_manual_solutions WHERE ticket_id=?", tkt.ID); got != strings.Repeat("a", 2000) {
			t.Errorf("stored solution must be the trimmed value, got len %d", len(got))
		}
	})

	t.Run("whitespace-only means absent and stores nothing", func(t *testing.T) {
		h := newHarness(t)
		tkt := h.seedTicket(t, "whitespace only", nil)
		h.assignTicket(t, tkt.ID, h.admin.ID)
		id := strconv.FormatInt(tkt.ID, 10)

		rec := h.postForm(t, "/tickets/"+id+"/workflow/steps/1/complete",
			url.Values{"solution": {"   \n\t  "}}, false)
		mustHaveCompletionRoute(t, rec, http.StatusOK, "whitespace-only solution completes normally")
		if n := countSolutions(t, h, tkt.ID); n != 0 {
			t.Errorf("solution rows = %d, want 0 (whitespace-only is absent)", n)
		}
	})

	t.Run("claim completion carrying a solution is a plan contradiction", func(t *testing.T) {
		h := newHarness(t)
		desk, err := h.desks.Create(t.Context(), *h.admin, "Network")
		if err != nil {
			t.Fatalf("create desk: %v", err)
		}
		cat, err := h.categories.Create(t.Context(), "ClaimSolution")
		if err != nil {
			t.Fatalf("create category: %v", err)
		}
		h.publishWorkflow(t, cat.ID, domain.WorkflowDefinition{{
			Type:         domain.StepAssignToDesk,
			AssignToDesk: &domain.AssignToDeskStep{DeskID: desk.ID, Strategy: domain.StrategyClaim},
		}})
		tkt := h.seedTicket(t, "claim with solution", func(in *application.CreateTicketInput) { in.CategoryID = cat.ID })
		if err := h.desks.AddMember(t.Context(), *h.admin, desk.ID, h.admin.ID); err != nil {
			t.Fatalf("add admin as desk member: %v", err)
		}

		rec := h.postForm(t, "/tickets/"+strconv.FormatInt(tkt.ID, 10)+"/workflow/steps/1/complete",
			url.Values{"reason": {"i take it"}, "solution": {"rack"}}, false)
		mustHaveCompletionRoute(t, rec, http.StatusUnprocessableEntity, "non-manual step must not accept a solution")
		assertNoWorkflowWrites(t, h, tkt.ID)
	})

	t.Run("forged assignee key on manual completion keeps its rejection", func(t *testing.T) {
		h := newHarness(t)
		tkt := h.seedTicket(t, "forged assignee", nil)
		h.assignTicket(t, tkt.ID, h.admin.ID)

		rec := h.postForm(t, "/tickets/"+strconv.FormatInt(tkt.ID, 10)+"/workflow/steps/1/complete",
			url.Values{"assignee_id": {"9999"}, "solution": {"rack"}}, false)
		mustHaveCompletionRoute(t, rec, http.StatusUnprocessableEntity, "manual_task ignores forged metadata")
		assertNoWorkflowWrites(t, h, tkt.ID)
	})
}

// pendingManualFixture publishes a single manual_task step whose pinned
// instruction carries markup-sensitive text and seeds a ticket whose REQUESTER
// is a plain user-role user (readable in ScopeOwned) assigned to the harness
// admin through the audited Assign path (Amendment 2 creation is
// unassigned-only). Returns the ticket and its requester.
func pendingManualFixture(t *testing.T, h *harness, instruction string) (*domain.Ticket, *domain.User) {
	t.Helper()
	cat, err := h.categories.Create(t.Context(), "PendingManual")
	if err != nil {
		t.Fatalf("create category: %v", err)
	}
	h.publishWorkflow(t, cat.ID, domain.WorkflowDefinition{{
		Type:       domain.StepManualTask,
		ManualTask: &domain.ManualTaskStep{Instructions: instruction},
	}})
	requester := seedUserRole(t, h.store, "Pending Rita", "pending-rita@tkt.test", domain.RoleUser)
	tkt, err := h.tickets.Create(t.Context(), *requester, application.CreateTicketInput{
		Title: "pending manual presentation", Description: "d", CategoryID: cat.ID, Priority: domain.PriorityMedium,
	})
	if err != nil {
		t.Fatalf("seed pending manual ticket: %v", err)
	}
	h.assignTicket(t, tkt.ID, h.admin.ID)
	return tkt, requester
}

// TestPendingActions_Presentation locks the Amendment 2 pending contract: the
// card leads with the step's PINNED instruction verbatim and escaped, uses no
// ordered-list numbering, never renders the generic `Mark the current task as
// complete.` copy, offers the optional solution textarea on manual tasks, and
// keeps GET rendering strictly read-only.
func TestPendingActions_Presentation(t *testing.T) {
	const pinnedInstruction = `Rack & stack <b>carefully</b> — check cables`
	const genericCopy = "Mark the current task as complete."

	t.Run("manual task leads with escaped pinned instruction and optional solution", func(t *testing.T) {
		h := newHarness(t)
		tkt, _ := pendingManualFixture(t, h, pinnedInstruction)

		before := auditCount(t, h, tkt.ID)
		rec := h.get(t, "/tickets/"+strconv.FormatInt(tkt.ID, 10), false)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		body := rec.Body.String()

		// The instruction leads VERBATIM and ESCAPED — markup stays literal.
		if !strings.Contains(body, "Rack &amp; stack &lt;b&gt;carefully&lt;/b&gt; — check cables") {
			t.Errorf("pending card must lead with the escaped pinned instruction: %.600s", body)
		}
		if strings.Contains(body, "<b>carefully</b>") {
			t.Errorf("pinned instruction must not render as raw HTML: %.600s", body)
		}

		// No ordered-list numbering in the pending card.
		if strings.Contains(body, `<ol`) || strings.Contains(body, "workflow-pending-list") {
			t.Errorf("pending card must not use ordered-list numbering: %.600s", body)
		}

		// The generic completion copy is gone from every ticket-facing page.
		if strings.Contains(body, genericCopy) {
			t.Errorf("generic copy %q must never render: %.600s", genericCopy, body)
		}

		// Optional solution textarea labeled for the assignee + unchanged
		// role-gated submit control.
		for _, want := range []string{`name="solution"`, `Solution (optional)`, `>Complete</button>`} {
			if !strings.Contains(body, want) {
				t.Errorf("manual pending card must render %q, got: %.600s", want, body)
			}
		}

		// GET rendering is strictly read-only: no cursor movement, no new
		// audits, no solutions.
		if after := auditCount(t, h, tkt.ID); after != before {
			t.Errorf("GET appended audit rows: before=%d after=%d", before, after)
		}
		if n := countSolutions(t, h, tkt.ID); n != 0 {
			t.Errorf("GET persisted solution rows = %d, want 0", n)
		}
		if cur := scanOneInt(t, h.rawDB(t), "SELECT current_step_index FROM ticket_workflow_runs WHERE ticket_id=?", tkt.ID); cur != 0 {
			t.Errorf("GET moved the run cursor to %d, want 0", cur)
		}
	})

	t.Run("instruction comes from the pinned snapshot not the live draft", func(t *testing.T) {
		h := newHarness(t)
		tkt, _ := pendingManualFixture(t, h, pinnedInstruction)

		// Publish a NEWER version with different instructions while the run
		// stays pinned to the original; the pending card must keep showing
		// the pinned text it already loaded from the execution snapshot.
		cat, err := h.categories.List(t.Context())
		if err != nil {
			t.Fatalf("list categories: %v", err)
		}
		var catID int64
		for _, c := range cat {
			if c.ID != h.bugCategory.ID {
				catID = c.ID
			}
		}
		h.publishWorkflow(t, catID, domain.WorkflowDefinition{{
			Type:       domain.StepManualTask,
			ManualTask: &domain.ManualTaskStep{Instructions: "NEW DRAFT INSTRUCTIONS"},
		}})

		body := h.get(t, "/tickets/"+strconv.FormatInt(tkt.ID, 10), false).Body.String()
		if !strings.Contains(body, "Rack &amp; stack") {
			t.Errorf("pending card must lead with the PINNED instruction, got: %.600s", body)
		}
		if strings.Contains(body, "NEW DRAFT INSTRUCTIONS") {
			t.Errorf("pending card must never read the live draft: %.600s", body)
		}
	})

	t.Run("non-assignee sees no actionable control or solution textarea", func(t *testing.T) {
		h := newHarness(t)
		tkt, requester := pendingManualFixture(t, h, pinnedInstruction)
		// The REQUESTER can read the ticket (ScopeOwned) but is not the
		// current assignee, so no completion control may render.
		sess := seedSession(t, h.store, requester.ID)

		req := httptest.NewRequest(http.MethodGet, "/tickets/"+strconv.FormatInt(tkt.ID, 10), nil)
		req.Header.Set("Cookie", sessionCookie+"="+sess.ID)
		rec := httptest.NewRecorder()
		h.mw.Wrap(h.mux).ServeHTTP(rec, req)
		body := rec.Body.String()

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if strings.Contains(body, genericCopy) || strings.Contains(body, `<ol`) {
			t.Errorf("unauthorized view must stay free of generic copy and numbering: %.600s", body)
		}
		if strings.Contains(body, `name="solution"`) {
			t.Errorf("non-assignee must see no solution textarea: %.600s", body)
		}
		if strings.Contains(body, `/workflow/steps/1/complete`) {
			t.Errorf("non-assignee must see no completion control: %.600s", body)
		}
		if strings.Contains(body, `class="card current-task-card"`) || strings.Contains(body, `<h2 id="current-task-title">Current task</h2>`) {
			t.Errorf("non-assignee must see no actionable current-task card: %.600s", body)
		}
		for _, want := range []string{
			`class="timeline-entry workflow-pending workflow-pending-info"`,
			"IN PROGRESS",
			"Admin is handling this task.",
			"Updates will appear here when complete.",
		} {
			if !strings.Contains(body, want) {
				t.Errorf("unauthorized view must render %q: %.600s", want, body)
			}
		}
		for _, forbidden := range []string{
			pinnedInstruction,
			"Complete this task",
			"Awaiting another participant to complete the current step.",
			`name="solution"`,
			`/workflow/steps/1/complete`,
		} {
			if strings.Contains(body, forbidden) {
				t.Errorf("unauthorized view must not render %q: %.600s", forbidden, body)
			}
		}
	})

	t.Run("form step keeps contextual pinned fields without numbering", func(t *testing.T) {
		h := newHarness(t)
		cat, err := h.categories.Create(t.Context(), "PendingForm")
		if err != nil {
			t.Fatalf("create category: %v", err)
		}
		h.publishFormWorkflow(t, cat.ID, domain.FormActorRequester,
			domain.FormField{Key: "host", Label: "Host", Kind: domain.FieldShortText, Required: true})
		req2 := seedUserRole(t, h.store, "Requester Rita", "rita@tkt.test", domain.RoleUser)
		sess := seedSession(t, h.store, req2.ID)
		tkt, err := h.tickets.Create(t.Context(), *req2, application.CreateTicketInput{
			Title: "form pending", Description: "d", CategoryID: cat.ID, Priority: domain.PriorityMedium,
		})
		if err != nil {
			t.Fatalf("seed form ticket: %v", err)
		}

		req3 := httptest.NewRequest(http.MethodGet, "/tickets/"+strconv.FormatInt(tkt.ID, 10), nil)
		req3.Header.Set("Cookie", sessionCookie+"="+sess.ID)
		rec := httptest.NewRecorder()
		h.mw.Wrap(h.mux).ServeHTTP(rec, req3)
		body := rec.Body.String()

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if !strings.Contains(body, `name="answer_0"`) || !strings.Contains(body, "Host") {
			t.Errorf("form step must keep its contextual pinned field: %.600s", body)
		}
		if strings.Contains(body, `<ol`) || strings.Contains(body, genericCopy) {
			t.Errorf("form pending card must not use numbering or generic copy: %.600s", body)
		}
	})
}

// auditCount returns the total audit_events row count for a ticket.
func auditCount(t *testing.T, h *harness, ticketID int64) int64 {
	t.Helper()
	return scanOneInt(t, h.rawDB(t), "SELECT COUNT(*) FROM audit_events WHERE ticket_id=?", ticketID)
}
