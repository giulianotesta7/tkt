package httpadapter

import (
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// Contextual workflow timeline HTTP contract: a completed claim step renders
// exactly ONE assignment main line — `Assigned to <strong>{person}</strong> ·
// <strong>{desk}</strong>` — from persisted facts, degrades to Unknown desk
// after the desk is deleted, keeps the A→B reason visible, and labels the
// least_loaded actor as Workflow.

// seedClaimCategory publishes a single assign_to_desk workflow for a fresh
// category and seeds an unassigned ticket pinned to it.
func seedClaimCategory(t *testing.T, h *harness, deskID int64, strategy domain.AssignmentStrategy) *domain.Ticket {
	t.Helper()
	cat, err := h.categories.Create(t.Context(), "Timeline-"+string(strategy))
	if err != nil {
		t.Fatalf("create category: %v", err)
	}
	h.publishWorkflow(t, cat.ID, domain.WorkflowDefinition{{
		Type:         domain.StepAssignToDesk,
		AssignToDesk: &domain.AssignToDeskStep{DeskID: deskID, Strategy: strategy},
	}})
	return h.seedTicket(t, "timeline claim", func(in *application.CreateTicketInput) { in.CategoryID = cat.ID })
}

// stripTags removes markup so assertions can target VISIBLE copy: attribute
// values (class names, hx URLs) are dropped with their tags while text content
// survives. It keeps the no-workflow-terminology contract about what a reader
// sees instead of internal identifiers.
func stripTags(html string) string {
	html = regexp.MustCompile(`(?is)<style[^>]*>.*?</style\s*>`).ReplaceAllString(html, "")
	html = regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`).ReplaceAllString(html, "")
	return regexp.MustCompile(`<[^>]*>`).ReplaceAllString(html, "")
}

func TestTicketWorkflowTimelineClaimRendersExactAssignmentLine(t *testing.T) {
	h := newHarness(t)
	desk, err := h.desks.Create(t.Context(), *h.admin, "Network")
	if err != nil {
		t.Fatalf("create desk: %v", err)
	}
	tkt := seedClaimCategory(t, h, desk.ID, domain.StrategyClaim)
	if err := h.desks.AddMember(t.Context(), *h.admin, desk.ID, h.admin.ID); err != nil {
		t.Fatalf("add admin as desk member: %v", err)
	}

	rec := h.postForm(t, "/tickets/"+strconv.FormatInt(tkt.ID, 10)+"/workflow/steps/1/complete", url.Values{}, false)
	if rec.Code != http.StatusOK {
		t.Fatalf("claim completion status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	want := `Assigned to <strong>Admin</strong> · <strong>Network</strong>`
	if !strings.Contains(body, want) {
		t.Errorf("completion render must carry %q, got: %s", want, body)
	}
	if n := strings.Count(body, want); n != 1 {
		t.Errorf("assignment main line must render exactly once, got %d: %s", n, body)
	}
	if strings.Contains(body, `st-`+strconv.FormatInt(h.admin.ID, 10)) {
		t.Errorf("assignment entry must not derive an st-user-id class from ToValue, got: %s", body)
	}

	// The stored page renders the same line.
	page := h.get(t, "/tickets/"+strconv.FormatInt(tkt.ID, 10), false)
	if !strings.Contains(page.Body.String(), want) {
		t.Errorf("detail page must carry %q, got: %s", want, page.Body.String())
	}

	// Deleting the desk keeps history and degrades to Unknown desk (migration
	// 0007 FK ON DELETE SET NULL).
	if err := h.desks.Delete(t.Context(), *h.admin, desk.ID); err != nil {
		t.Fatalf("delete desk: %v", err)
	}
	after := h.get(t, "/tickets/"+strconv.FormatInt(tkt.ID, 10), false).Body.String()
	wantUnknown := `Assigned to <strong>Admin</strong> · <strong>Unknown desk</strong>`
	if !strings.Contains(after, wantUnknown) {
		t.Errorf("deleted desk must degrade to %q, got: %s", wantUnknown, after)
	}
}

func TestTicketWorkflowTimelineClaimReassignmentIsReasonless(t *testing.T) {
	h := newHarness(t)
	desk, err := h.desks.Create(t.Context(), *h.admin, "Network")
	if err != nil {
		t.Fatalf("create desk: %v", err)
	}
	tkt := seedClaimCategory(t, h, desk.ID, domain.StrategyClaim)
	beto := h.createUser(t, "Beto", "beto@example.com", "secret")

	// A→B claim: Beto owns the ticket first, then the admin claims it.
	id := strconv.FormatInt(tkt.ID, 10)
	if rec := h.postForm(t, "/tickets/"+id+"/assign", url.Values{"user_id": {strconv.FormatInt(beto.ID, 10)}}, false); rec.Code != http.StatusSeeOther {
		t.Fatalf("initial assign status = %d, want 303", rec.Code)
	}
	if err := h.desks.AddMember(t.Context(), *h.admin, desk.ID, h.admin.ID); err != nil {
		t.Fatalf("add admin as desk member: %v", err)
	}
	rec := h.postForm(t, "/tickets/"+id+"/workflow/steps/1/complete", url.Values{}, false)
	if rec.Code != http.StatusOK {
		t.Fatalf("A→B claim status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `Assigned to <strong>Admin</strong> · <strong>Network</strong>`) {
		t.Errorf("A→B claim must render the exact assignment line, got: %s", body)
	}
	if strings.Contains(body, `Reason: taking over`) {
		t.Errorf("workflow A→B claim must not persist a reason, got: %s", body)
	}
}

// TestTicketWorkflowTimelineLeastLoadedAutomaticAssignmentOmitsActor proves the
// automatic least_loaded assignment renders its structured line with NO actor
// text (audit-log spec: no actor label may use the word `workflow`) and leaves
// no dangling `·` separator in the when-line.
func TestTicketWorkflowTimelineLeastLoadedAutomaticAssignmentOmitsActor(t *testing.T) {
	h := newHarness(t)
	desk, err := h.desks.Create(t.Context(), *h.admin, "Network")
	if err != nil {
		t.Fatalf("create desk: %v", err)
	}
	// least_loaded resolves automatically at CREATE time, so the desk must
	// have a member before the ticket is seeded; no human completion happens.
	if err := h.desks.AddMember(t.Context(), *h.admin, desk.ID, h.admin.ID); err != nil {
		t.Fatalf("add admin as desk member: %v", err)
	}
	tkt := seedClaimCategory(t, h, desk.ID, domain.StrategyLeastLoaded)

	body := h.get(t, "/tickets/"+strconv.FormatInt(tkt.ID, 10), false).Body.String()
	if !strings.Contains(body, `Assigned to <strong>Admin</strong> · <strong>Network</strong>`) {
		t.Errorf("least_loaded must render the exact assignment line, got: %s", body)
	}
	if strings.Contains(stripTags(body), "workflow") {
		t.Errorf("no ticket-facing copy or actor label may contain workflow, got: %s", stripTags(body))
	}
	if strings.Contains(body, "· </div>") {
		t.Errorf("an omitted actor must leave no dangling separator: %s", body)
	}
}

// TestTicketWorkflowTimelineDetailCopyAvoidsWorkflowTerminology asserts the
// rendered detail over a pending manual step carries no visible copy containing
// `workflow` anywhere (ticket-workflow-execution spec: ticket-facing copy and
// pending explanatory text use neutral wording).
func TestTicketWorkflowTimelineDetailCopyAvoidsWorkflowTerminology(t *testing.T) {
	h := newHarness(t)
	tkt := h.seedTicket(t, "pending manual copy", nil)

	body := h.get(t, "/tickets/"+strconv.FormatInt(tkt.ID, 10), false).Body.String()
	if !strings.Contains(body, "Current task") {
		t.Fatalf("current task card must render for the active run: %s", body)
	}
	if text := stripTags(body); strings.Contains(text, "workflow") {
		t.Errorf("rendered detail copy contains workflow terminology: %s", text)
	}
}

// WB.6 (Amendment 2) — a manual completion's stored solution renders INSIDE
// its completion event as escaped plain text, attributed/timestamped by that
// event; a completion without a solution renders the instruction alone with
// no empty block; newest-first ordering and the no-visible-`workflow`-copy
// rule are preserved.
func TestWorkflowStepTimelineManualSolutionRendersInsideEvent(t *testing.T) {
	h := newHarness(t)
	cat, err := h.categories.Create(t.Context(), "SolvedManual")
	if err != nil {
		t.Fatalf("create category: %v", err)
	}
	h.publishWorkflow(t, cat.ID, domain.WorkflowDefinition{{
		Type:       domain.StepManualTask,
		ManualTask: &domain.ManualTaskStep{Instructions: "inspect the server"},
	}})

	// Solved ticket: requester-owned, assigned to admin, completed WITH a
	// markup-laden solution; an older comment locks newest-first ordering.
	requester := seedUserRole(t, h.store, "Solvetta", "solvetta@tkt.test", domain.RoleUser)
	solved, err := h.tickets.Create(t.Context(), *requester, application.CreateTicketInput{
		Title: "solved manual", Description: "d", CategoryID: cat.ID, Priority: domain.PriorityMedium,
	})
	if err != nil {
		t.Fatalf("seed solved ticket: %v", err)
	}
	h.assignTicket(t, solved.ID, h.admin.ID)
	if _, err := h.comments.Add(t.Context(), *h.admin, solved.ID, "older comment first", "public"); err != nil {
		t.Fatalf("seed comment: %v", err)
	}
	// The harness clock is FIXED: a service-stamped comment ties with the
	// completion event at the same second, and the preserved
	// comments-before-events tie rule (views.go) would then legitimately
	// render it above the event. Backdate the comment deterministically so
	// the strict newest-first assertion below proves ordering, not tie luck.
	if _, err := h.rawDB(t).ExecContext(t.Context(),
		`UPDATE comments SET created_at = ? WHERE ticket_id = ? AND body = 'older comment first'`,
		time.Now().UTC().Add(-time.Hour).Format(time.RFC3339), solved.ID); err != nil {
		t.Fatalf("backdate seeded comment: %v", err)
	}
	sid := strconv.FormatInt(solved.ID, 10)
	rec := h.postForm(t, "/tickets/"+sid+"/workflow/steps/1/complete",
		url.Values{"solution": {"<b>reseat</b> the cable & reboot"}}, false)
	if rec.Code != http.StatusOK {
		t.Fatalf("completion status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	body := h.get(t, "/tickets/"+sid, false).Body.String()
	if !strings.Contains(body, `<p class="workflow-solution">&lt;b&gt;reseat&lt;/b&gt; the cable &amp; reboot</p>`) {
		t.Errorf("solution must render escaped inside its event, got: %s", body)
	}
	if strings.Contains(body, "<b>reseat</b>") {
		t.Errorf("solution rendered as raw HTML: %s", body)
	}
	if !strings.Contains(body, `<p class="workflow-instruction">inspect the server</p>`) {
		t.Errorf("the event keeps its pinned instruction: %s", body)
	}
	if idx := strings.Index(body, "workflow-solution"); idx >= 0 {
		entry := body[max(0, idx-400) : idx+200]
		if !strings.Contains(entry, "· Admin") || !strings.Contains(entry, `class="when"`) {
			t.Errorf("solution must be attributed by the existing completion event entry: %s", entry)
		}
	}
	// Newest-first: the older comment sits BELOW the newer completion event.
	eventIdx := strings.Index(body, "workflow-solution")
	commentIdx := strings.Index(body, "older comment first")
	if eventIdx < 0 || commentIdx < 0 || commentIdx < eventIdx {
		t.Errorf("newest-first ordering broken (event at %d, older comment at %d)", eventIdx, commentIdx)
	}
	if text := stripTags(body); strings.Contains(text, "workflow") {
		t.Errorf("no ticket-facing copy may contain workflow, got: %s", text)
	}

	// Unsolved twin: same flow WITHOUT a solution renders the instruction
	// alone — no solution block and no placeholder anywhere.
	unsolvedReq := seedUserRole(t, h.store, "Unsolvetta", "unsolvetta@tkt.test", domain.RoleUser)
	unsolved, err := h.tickets.Create(t.Context(), *unsolvedReq, application.CreateTicketInput{
		Title: "unsolved manual", Description: "d", CategoryID: cat.ID, Priority: domain.PriorityMedium,
	})
	if err != nil {
		t.Fatalf("seed unsolved ticket: %v", err)
	}
	h.assignTicket(t, unsolved.ID, h.admin.ID)
	uid := strconv.FormatInt(unsolved.ID, 10)
	if rec := h.postForm(t, "/tickets/"+uid+"/workflow/steps/1/complete", url.Values{}, false); rec.Code != http.StatusOK {
		t.Fatalf("unsolved completion status = %d, want 200", rec.Code)
	}
	// Tie rule (WB.6): a comment stamped in the SAME second as the completion
	// event — here guaranteed by the fixed harness clock — renders BEFORE the
	// event, per the preserved comments-before-events tie behavior.
	if _, err := h.comments.Add(t.Context(), *h.admin, unsolved.ID, "tied same-second comment", "public"); err != nil {
		t.Fatalf("seed tied comment: %v", err)
	}
	ubody := h.get(t, "/tickets/"+uid, false).Body.String()
	if !strings.Contains(ubody, `<p class="workflow-instruction">inspect the server</p>`) {
		t.Errorf("legacy-style completion still shows its instruction: %s", ubody)
	}
	tieComment := strings.Index(ubody, "tied same-second comment")
	tieEvent := strings.Index(ubody, `class="timeline-entry timeline-event`)
	if tieComment < 0 || tieEvent < 0 || tieEvent < tieComment {
		t.Errorf("same-second tie must render the comment before the event (comment at %d, event at %d)", tieComment, tieEvent)
	}
	if strings.Contains(ubody, "workflow-solution") || strings.Contains(stripTags(ubody), "No solution") {
		t.Errorf("empty completion must render no solution block or placeholder: %s", ubody)
	}
}
