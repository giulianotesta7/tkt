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
// one actor-first sentence with the persisted target and desk; it degrades to
// Unknown desk after deletion, keeps the A→B reason visible, and omits the
// automatic least_loaded actor.

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
	h.staff(t, desk.ID)

	rec := h.postForm(t, "/tickets/"+strconv.FormatInt(tkt.ID, 10)+"/workflow/steps/1/complete", url.Values{}, false)
	if rec.Code != http.StatusOK {
		t.Fatalf("claim completion status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	want := `assigned the ticket to <strong>Admin</strong> at <strong>Network</strong>`
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
	wantUnknown := `assigned the ticket to <strong>Admin</strong> at <strong>Unknown desk</strong>`
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
	beto := h.createUser(t, "Beto", "beto@example.com", "s3cret-pass-12")

	// A→B claim: Beto owns the ticket first, then the admin claims it.
	id := strconv.FormatInt(tkt.ID, 10)
	if rec := h.postForm(t, "/tickets/"+id+"/assign", url.Values{"user_id": {strconv.FormatInt(beto.ID, 10)}}, false); rec.Code != http.StatusSeeOther {
		t.Fatalf("initial assign status = %d, want 303", rec.Code)
	}
	h.staff(t, desk.ID)
	rec := h.postForm(t, "/tickets/"+id+"/workflow/steps/1/complete", url.Values{}, false)
	if rec.Code != http.StatusOK {
		t.Fatalf("A→B claim status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `assigned the ticket to <strong>Admin</strong> at <strong>Network</strong>`) {
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
	h.staff(t, desk.ID)
	tkt := seedClaimCategory(t, h, desk.ID, domain.StrategyLeastLoaded)

	body := h.get(t, "/tickets/"+strconv.FormatInt(tkt.ID, 10), false).Body.String()
	if !strings.Contains(body, `assigned the ticket to <strong>Admin</strong> at <strong>Network</strong>`) {
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
	h.assignTicket(t, tkt.ID, h.admin.ID)

	body := h.get(t, "/tickets/"+strconv.FormatInt(tkt.ID, 10), false).Body.String()
	if !strings.Contains(body, `class="timeline-entry workflow-pending workflow-pending-action"`) {
		t.Fatalf("current task must render inside Timeline for the active run: %s", body)
	}
	if text := stripTags(body); strings.Contains(text, "workflow") {
		t.Errorf("rendered detail copy contains workflow terminology: %s", text)
	}
}

// WB.6 (Amendment 2) — a manual completion's stored solution renders INSIDE
// its static completion event as escaped plain text, attributed/timestamped by
// that event; a completion without a solution renders the instruction alone
// with no empty block; oldest-first ordering and the no-visible-`workflow`-copy
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
	// markup-laden solution; an older comment locks the ordering.
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
	// the strict ordering assertion below proves order, not tie luck.
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
	for _, want := range []string{
		`<article class="timeline-entry timeline-event timeline-manual">`,
		`<div class="timeline-entry-head">`,
		`<strong class="timeline-actor">You</strong> <span class="timeline-action">completed the task</span>`,
		`<dt>Task</dt>`,
		`<dd>inspect the server</dd>`,
		`<dt>Solution</dt>`,
		`<dd>&lt;b&gt;reseat&lt;/b&gt; the cable &amp; reboot</dd>`,
		`<div class="when"><time`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("static manual event must render escaped task/solution content, missing %q in: %s", want, body)
		}
	}
	if strings.Contains(body, `<details class="timeline-entry timeline-event timeline-manual">`) || strings.Contains(body, `<summary class="timeline-event-summary">`) || strings.Contains(body, "timeline-event-summary") {
		t.Errorf("manual event must not render disclosure or interaction markup: %s", body)
	}
	if strings.Contains(body, "<b>reseat</b>") {
		t.Errorf("solution rendered as raw HTML: %s", body)
	}
	if !strings.Contains(body, `<dt>Task</dt>`) || !strings.Contains(body, `<dd>inspect the server</dd>`) {
		t.Errorf("the event keeps its pinned task details: %s", body)
	}
	if !strings.Contains(body, `<strong class="timeline-actor">You</strong> <span class="timeline-action">completed the task</span>`) {
		t.Errorf("solution must be attributed first by the completion event: %s", body)
	}
	if strings.Contains(body, `class="when"><time`) && strings.Contains(body, `</time> · Admin`) {
		t.Errorf("completion timestamp must not duplicate the actor: %s", body)
	}
	if idx := strings.Index(body, "Solution"); idx >= 0 {
		entry := body[max(0, idx-700) : idx+200]
		if !strings.Contains(entry, `class="when"`) {
			t.Errorf("solution must stay inside the timestamped completion event: %s", entry)
		}
	}
	// Oldest-first (issue #386, S3): the older comment sits ABOVE the newer
	// completion event.
	eventIdx := strings.Index(body, "Solution")
	commentIdx := strings.Index(body, "older comment first")
	if eventIdx < 0 || commentIdx < 0 || eventIdx < commentIdx {
		t.Errorf("oldest-first ordering broken (older comment at %d, event at %d)", commentIdx, eventIdx)
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
	if !strings.Contains(ubody, `<article class="timeline-entry timeline-event timeline-manual">`) || !strings.Contains(ubody, `<div class="timeline-entry-head">`) || !strings.Contains(ubody, `<strong class="timeline-actor">You</strong> <span class="timeline-action">completed the task</span>`) || !strings.Contains(ubody, `<dt>Task</dt>`) || !strings.Contains(ubody, `<dd>inspect the server</dd>`) {
		t.Errorf("static manual event must render actor-first task details: %s", ubody)
	}
	if strings.Contains(ubody, `<details class="timeline-entry timeline-event timeline-manual">`) || strings.Contains(ubody, `<summary class="timeline-event-summary">`) || strings.Contains(ubody, "timeline-event-summary") {
		t.Errorf("manual event must not render disclosure or interaction markup: %s", ubody)
	}
	// The tie rule is preserved and reversed with the feed: the newest-first
	// stream puts the comment before the same-second event, so the oldest-first
	// detail reads the event first.
	tieComment := strings.Index(ubody, "tied same-second comment")
	tieEvent := strings.Index(ubody, `class="timeline-entry timeline-event`)
	if tieComment < 0 || tieEvent < 0 || tieComment < tieEvent {
		t.Errorf("same-second tie must keep its rule under the new order (comment at %d, event at %d)", tieComment, tieEvent)
	}
	if strings.Contains(ubody, "<dt>Solution</dt>") || strings.Contains(stripTags(ubody), "No solution") {
		t.Errorf("empty completion must render no solution block or placeholder: %s", ubody)
	}
}

// stepsRegion returns the rendered Steps card alone, so an assertion about its
// badge or its control can never be satisfied by the Assignment panel, the
// header or the stylesheet.
func stepsRegion(t *testing.T, body string) string {
	t.Helper()
	marker := strings.Index(body, `id="workflow-pending"`)
	if marker < 0 {
		t.Fatalf("the detail must render the Steps card, got: %.600s", body)
	}
	start := strings.LastIndex(body[:marker], "<section")
	if start < 0 {
		t.Fatalf("the Steps card must be a section, got: %.600s", body[marker:])
	}
	end := strings.Index(body[start:], "</section>")
	if end < 0 {
		t.Fatalf("the Steps card must close, got: %.600s", body[start:])
	}
	return body[start : start+end+len("</section>")]
}

// assertNoBlankHTMLines proves the rendered detail keeps the markup contract the
// amendment-4 whitespace check enforces: no whitespace-only line survives the
// template, because the inline stylesheet would publish it as visible space.
func assertNoBlankHTMLLines(t *testing.T, body string) {
	t.Helper()
	for lineNumber, line := range strings.Split(body, "\n") {
		if line != "" && strings.TrimSpace(line) == "" {
			t.Errorf("rendered detail has a whitespace-only line at %d: %q", lineNumber+1, line)
		}
		if strings.HasSuffix(line, " ") || strings.HasSuffix(line, "\t") {
			t.Errorf("rendered detail has trailing whitespace at %d: %q", lineNumber+1, line)
		}
	}
}

// Issue #438 (S4, S8): only the actor who can complete the current step is
// badged "Your turn" and given its control. manual_task belongs to the
// assignee alone — an admin who is not assigned gets nothing — and a
// requester form belongs to the requester or the assignee, never to both.
func TestWorkflowStepsYourTurnOnlyForTheActorWhoCanAct(t *testing.T) {
	t.Run("manual task belongs to the assignee alone", func(t *testing.T) {
		h := newHarness(t)
		tkt, requester := pendingManualFixture(t, h, "Check the cable run")
		id := strconv.FormatInt(tkt.ID, 10)

		mine := stepsRegion(t, h.get(t, "/tickets/"+id, false).Body.String())
		for _, want := range []string{
			`class="timeline-entry workflow-pending workflow-pending-action"`,
			`<span class="workflow-checklist-badge">Your turn</span>`,
			`action="/tickets/` + id + `/workflow/steps/1/complete"`,
			`placeholder="Solution (optional)"`,
			`>Complete</button>`,
		} {
			if !strings.Contains(mine, want) {
				t.Errorf("the assignee's Steps card must carry %q, got: %s", want, mine)
			}
		}

		// S8: an administrator who is NOT the assignee reads the same step and
		// sees who is awaited — never the control.
		outsider := seedUserRole(t, h.store, "Outsider Admin", "outsider-admin-steps@tkt.test", domain.RoleAdmin)
		theirs := stepsRegion(t, h.getAs(t, "/tickets/"+id, h.sessionFor(t, outsider.ID)).Body.String())
		if !strings.Contains(theirs, `class="timeline-entry workflow-pending workflow-pending-info"`) {
			t.Errorf("a non-acting admin must read the passive Steps card, got: %s", theirs)
		}
		if !strings.Contains(theirs, "Assigned to Admin") {
			t.Errorf("a non-acting admin must read who is awaited, got: %s", theirs)
		}
		for _, absent := range []string{"workflow-pending-action", "Your turn", ">Complete</button>", "/workflow/steps/1/complete"} {
			if strings.Contains(theirs, absent) {
				t.Errorf("a non-acting admin must not receive %q, got: %s", absent, theirs)
			}
		}

		// The requester reads the same awaited person and no control at all.
		requesters := stepsRegion(t, h.getAs(t, "/tickets/"+id, h.sessionFor(t, requester.ID)).Body.String())
		for _, absent := range []string{"workflow-pending-action", "Your turn", ">Complete</button>", "name=\"solution\""} {
			if strings.Contains(requesters, absent) {
				t.Errorf("the requester must not receive %q, got: %s", absent, requesters)
			}
		}

		assertNoBlankHTMLLines(t, h.get(t, "/tickets/"+id, false).Body.String())

		// S4: the mark is measured, not eyeballed — the card is outlined in the
		// accent and the badge is an accent-soft pill, from the served stylesheet.
		style := extractStyleBlock(t, h.get(t, "/tickets/"+id, false).Body.String())
		for _, want := range []struct{ sel, decl string }{
			{".detail-steps:has(.workflow-pending-action){", "border-color:var(--accent)"},
			{".detail-steps:has(.workflow-pending-action){", "box-shadow:0 0 0 3px var(--accent-soft)"},
			{".workflow-checklist-badge{", "background:var(--accent-soft)"},
		} {
			if !cssRuleDeclares(style, want.sel, want.decl) {
				t.Errorf("the Steps stylesheet must declare %q in %q", want.decl, want.sel)
			}
		}
	})

	t.Run("requester form belongs to the requester", func(t *testing.T) {
		h := newHarness(t)
		cat, err := h.categories.Create(t.Context(), "StepsRequesterForm")
		if err != nil {
			t.Fatalf("create category: %v", err)
		}
		h.publishFormWorkflow(t, cat.ID, domain.FormActorRequester,
			domain.FormField{Key: "host", Label: "Host", Kind: domain.FieldShortText, Required: true})
		requester := seedUserRole(t, h.store, "Steps Rita", "steps-rita@tkt.test", domain.RoleUser)
		tkt, err := h.tickets.Create(t.Context(), *requester, application.CreateTicketInput{
			Title: "steps requester form", Description: "d", CategoryID: cat.ID, Priority: domain.PriorityMedium,
		})
		if err != nil {
			t.Fatalf("create ticket: %v", err)
		}
		h.assignTicket(t, tkt.ID, h.admin.ID)
		id := strconv.FormatInt(tkt.ID, 10)

		requesters := stepsRegion(t, h.getAs(t, "/tickets/"+id, h.sessionFor(t, requester.ID)).Body.String())
		for _, want := range []string{
			`class="timeline-entry workflow-pending workflow-pending-action"`,
			`<span class="workflow-checklist-badge">Your turn</span>`,
			`>Host</label>`,
			`>Complete</button>`,
		} {
			if !strings.Contains(requesters, want) {
				t.Errorf("the requester's form step must carry %q, got: %s", want, requesters)
			}
		}

		// The assignee owns nothing on a requester form, and the copy names the
		// person the step waits on instead.
		assignees := stepsRegion(t, h.get(t, "/tickets/"+id, false).Body.String())
		for _, absent := range []string{"workflow-pending-action", "Your turn", ">Complete</button>", ">Host</label>"} {
			if strings.Contains(assignees, absent) {
				t.Errorf("the assignee must not receive %q on a requester form, got: %s", absent, assignees)
			}
		}
		if !strings.Contains(assignees, "Waiting on Steps Rita") {
			t.Errorf("the assignee must read who is awaited, got: %s", assignees)
		}
	})

	t.Run("assignee form belongs to the assignee", func(t *testing.T) {
		h := newHarness(t)
		cat, err := h.categories.Create(t.Context(), "StepsAssigneeForm")
		if err != nil {
			t.Fatalf("create category: %v", err)
		}
		h.publishFormWorkflow(t, cat.ID, domain.FormActorAssignee,
			domain.FormField{Key: "host", Label: "Host", Kind: domain.FieldShortText, Required: true})
		requester := seedUserRole(t, h.store, "Steps Rene", "steps-rene@tkt.test", domain.RoleUser)
		tkt, err := h.tickets.Create(t.Context(), *requester, application.CreateTicketInput{
			Title: "steps assignee form", Description: "d", CategoryID: cat.ID, Priority: domain.PriorityMedium,
		})
		if err != nil {
			t.Fatalf("create ticket: %v", err)
		}
		h.assignTicket(t, tkt.ID, h.admin.ID)
		id := strconv.FormatInt(tkt.ID, 10)

		assignees := stepsRegion(t, h.get(t, "/tickets/"+id, false).Body.String())
		for _, want := range []string{
			`class="timeline-entry workflow-pending workflow-pending-action"`,
			`<span class="workflow-checklist-badge">Your turn</span>`,
			`>Host</label>`,
			`>Complete</button>`,
		} {
			if !strings.Contains(assignees, want) {
				t.Errorf("the assignee's form step must carry %q, got: %s", want, assignees)
			}
		}
		requesters := stepsRegion(t, h.getAs(t, "/tickets/"+id, h.sessionFor(t, requester.ID)).Body.String())
		for _, absent := range []string{"workflow-pending-action", "Your turn", ">Complete</button>"} {
			if strings.Contains(requesters, absent) {
				t.Errorf("the requester must not receive %q on an assignee form, got: %s", absent, requesters)
			}
		}
	})
}

// Issue #438 (S7, S8): a claim step offers the SAME claim the Assignment panel
// already offers, inside Steps, to a member of its desk — badged "Your desk",
// with the claim's own label and no completion control. A non-member and the
// requester keep the status line alone.
func TestWorkflowStepsClaimTicketForDeskMembersOnly(t *testing.T) {
	h := newHarness(t)
	desk, err := h.desks.Create(t.Context(), *h.admin, "Network")
	if err != nil {
		t.Fatalf("create desk: %v", err)
	}
	tkt := seedClaimCategory(t, h, desk.ID, domain.StrategyClaim)
	h.staff(t, desk.ID)
	id := strconv.FormatInt(tkt.ID, 10)

	body := h.get(t, "/tickets/"+id, false).Body.String()
	member := stepsRegion(t, body)
	for _, want := range []string{
		`class="timeline-entry workflow-pending workflow-pending-action"`,
		`<span class="workflow-checklist-badge">Your desk</span>`,
		`<button class="btn primary workflow-pending-complete" type="submit">Claim ticket</button>`,
		`action="/tickets/` + id + `/workflow/steps/1/complete"`,
		`>Network has been asked to take it</span>`,
	} {
		if !strings.Contains(member, want) {
			t.Errorf("a desk member's Steps card must carry %q, got: %s", want, member)
		}
	}
	for _, absent := range []string{"Your turn", ">Complete</button>", `name="solution"`} {
		if strings.Contains(member, absent) {
			t.Errorf("a claim is not a manual completion, must not carry %q, got: %s", absent, member)
		}
	}

	// The Assignment panel keeps its own control and both project the SAME
	// action: the header/panel claim is preserved, not replaced.
	if got := strings.Count(body, `action="/tickets/`+id+`/workflow/steps/1/complete"`); got != 2 {
		t.Errorf("the panel and Steps must offer the same claim action exactly once each, got %d in: %.900s", got, body)
	}
	if !strings.Contains(body, `class="prop-value">Network</span>`) {
		t.Errorf("the Assignment panel must keep the desk row, got: %.900s", body)
	}
	if !strings.Contains(body, ">Assign to me</button>") {
		t.Errorf("the existing panel claim control must be preserved, got: %.900s", body)
	}

	// A non-member who can still read the ticket reads who is awaited and gets
	// no control (issue #438, S8).
	outsider := seedUserRole(t, h.store, "Outside Admin", "outside-admin-claim@tkt.test", domain.RoleAdmin)
	theirs := stepsRegion(t, h.getAs(t, "/tickets/"+id, h.sessionFor(t, outsider.ID)).Body.String())
	if !strings.Contains(theirs, `class="timeline-entry workflow-pending workflow-pending-info"`) {
		t.Errorf("a non-member must read the passive Steps card, got: %s", theirs)
	}
	if !strings.Contains(theirs, "Network has been asked to take it") {
		t.Errorf("a non-member must read the status line, got: %s", theirs)
	}
	for _, absent := range []string{"workflow-pending-action", "Your desk", "Claim ticket", "/workflow/steps/1/complete"} {
		if strings.Contains(theirs, absent) {
			t.Errorf("a non-member must not receive %q, got: %s", absent, theirs)
		}
	}

	// The requester who owns the ticket gets the status line and nothing else.
	req := seedUserRole(t, h.store, "Claim Rita", "claim-rita-steps@tkt.test", domain.RoleUser)
	own, err := h.tickets.Create(t.Context(), *req, application.CreateTicketInput{
		Title: "requester claim view", Description: "d", CategoryID: tkt.CategoryID, Priority: domain.PriorityMedium,
	})
	if err != nil {
		t.Fatalf("create requester-owned claim ticket: %v", err)
	}
	ownID := strconv.FormatInt(own.ID, 10)
	requesters := stepsRegion(t, h.getAs(t, "/tickets/"+ownID, h.sessionFor(t, req.ID)).Body.String())
	for _, absent := range []string{"workflow-pending-action", "Your desk", "Claim ticket", "/workflow/steps/1/complete"} {
		if strings.Contains(requesters, absent) {
			t.Errorf("the requester must not receive %q, got: %s", absent, requesters)
		}
	}
	if !strings.Contains(requesters, "Network has been asked to take it") {
		t.Errorf("the requester must read the status line, got: %s", requesters)
	}

	assertNoBlankHTMLLines(t, body)
}

// claimPendingFacts reads the three facts a rejected claim must leave alone
// through the store ports the detail itself reads: the assignment, the run
// cursor, and the actor's claimable count.
func claimPendingFacts(t *testing.T, h *harness, ticketID int64) (assignee int64, cursor int, status string, claimable int) {
	t.Helper()
	view, err := h.tickets.GetByID(t.Context(), *h.admin, ticketID)
	if err != nil {
		t.Fatalf("read ticket %d: %v", ticketID, err)
	}
	if view.Ticket.UserID != nil {
		assignee = *view.Ticket.UserID
	}
	snap, err := h.store.WorkflowRunStore().GetWorkflowExecution(t.Context(), ticketID)
	if err != nil {
		t.Fatalf("read run for %d: %v", ticketID, err)
	}
	if snap == nil || snap.Run == nil {
		t.Fatalf("ticket %d must keep its run", ticketID)
	}
	result, err := h.search.Search(t.Context(), *h.admin, application.TicketQuery{Section: application.TicketSectionClaimable}, 1)
	if err != nil {
		t.Fatalf("read claimable count: %v", err)
	}
	return assignee, snap.Run.CurrentStepIndex, snap.Run.Status, result.Total
}

// Issue #438 (S7): the Steps claim posts the exact route the Assignment panel
// posts, and that route keeps its outcome and its write guard unchanged —
// forbidden input leaves the assignment, the run and the claimable count as
// they were.
func TestWorkflowStepsClaimRouteKeepsOutcomeAndRejectsInput(t *testing.T) {
	h := newHarness(t)
	desk, err := h.desks.Create(t.Context(), *h.admin, "Network")
	if err != nil {
		t.Fatalf("create desk: %v", err)
	}
	tkt := seedClaimCategory(t, h, desk.ID, domain.StrategyClaim)
	h.staff(t, desk.ID)
	id := strconv.FormatInt(tkt.ID, 10)
	path := "/tickets/" + id + "/workflow/steps/1/complete"

	// The claim takes no form values: a forged solution, reason, answer or
	// assignee id is refused before any write, exactly as the panel's claim.
	beforeAssignee, beforeCursor, beforeStatus, beforeClaimable := claimPendingFacts(t, h, tkt.ID)
	for _, tc := range []struct {
		form url.Values
		want string
	}{
		{url.Values{"solution": {"forged"}}, "claim posts no form values"},
		{url.Values{"reason": {"forged"}}, "claim posts no form values"},
		{url.Values{"answer_0": {"forged"}}, "claim posts no form values"},
		{url.Values{"user_id": {strconv.FormatInt(h.admin.ID, 10)}}, "claim posts no form values"},
	} {
		rec := h.postForm(t, path, tc.form, false)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("claim with %v = %d, want 422: %.300s", tc.form, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), tc.want) {
			t.Errorf("claim rejection for %v must carry %q, got: %.300s", tc.form, tc.want, rec.Body.String())
		}
		afterAssignee, afterCursor, afterStatus, afterClaimable := claimPendingFacts(t, h, tkt.ID)
		if afterAssignee != beforeAssignee || afterCursor != beforeCursor || afterStatus != beforeStatus || afterClaimable != beforeClaimable {
			t.Errorf("rejected claim input %v changed the ticket: assignee %d→%d, cursor %d→%d, status %q→%q, claimable %d→%d",
				tc.form, beforeAssignee, afterAssignee, beforeCursor, afterCursor, beforeStatus, afterStatus, beforeClaimable, afterClaimable)
		}
	}

	// A member of the desk submits the claim the new control renders.
	claimed := h.postForm(t, path, url.Values{}, false)
	if claimed.Code != http.StatusOK {
		t.Fatalf("member claim status = %d, want 200: %s", claimed.Code, claimed.Body.String())
	}
	assignee, cursor, status, claimable := claimPendingFacts(t, h, tkt.ID)
	if assignee != h.admin.ID {
		t.Errorf("claim must assign the claimant, got assignee %d", assignee)
	}
	if status == "active" {
		t.Errorf("the single claim step must complete the run, got status %q at cursor %d", status, cursor)
	}
	if claimable != 0 {
		t.Errorf("a claimed ticket must leave the claimable set, got %d", claimable)
	}
	claimedBody := claimed.Body.String()
	if strings.Contains(claimedBody, `id="workflow-pending"`) {
		t.Errorf("the completed claim must drop the Steps card, got: %.600s", claimedBody)
	}
	if !strings.Contains(claimedBody, "assigned the ticket to <strong>Admin</strong> at <strong>Network</strong>") {
		t.Errorf("the claim must keep its exact assignment line, got: %.600s", claimedBody)
	}
}

// Issue #438 (S7, S8): the claim control's absence matches the server — a
// non-member who can read the ticket is refused by the exact same route and
// the pending claim survives untouched.
func TestWorkflowStepsClaimRouteDeniesNonMember(t *testing.T) {
	h := newHarness(t)
	desk, err := h.desks.Create(t.Context(), *h.admin, "Network")
	if err != nil {
		t.Fatalf("create desk: %v", err)
	}
	tkt := seedClaimCategory(t, h, desk.ID, domain.StrategyClaim)
	h.staff(t, desk.ID)
	id := strconv.FormatInt(tkt.ID, 10)
	path := "/tickets/" + id + "/workflow/steps/1/complete"

	outsider := seedUserRole(t, h.store, "Outside Admin", "outside-admin-post-claim@tkt.test", domain.RoleAdmin)
	beforeAssignee, beforeCursor, beforeStatus, beforeClaimable := claimPendingFacts(t, h, tkt.ID)
	rec := h.postFormAs(t, path, url.Values{}, h.sessionFor(t, outsider.ID))
	if rec.Code == http.StatusOK {
		t.Fatalf("a non-member claim must be refused, got 200: %.600s", rec.Body.String())
	}
	afterAssignee, afterCursor, afterStatus, afterClaimable := claimPendingFacts(t, h, tkt.ID)
	if afterAssignee != beforeAssignee || afterCursor != beforeCursor || afterStatus != beforeStatus || afterClaimable != beforeClaimable {
		t.Errorf("a refused non-member claim changed the ticket: assignee %d→%d, cursor %d→%d, status %q→%q, claimable %d→%d",
			beforeAssignee, afterAssignee, beforeCursor, afterCursor, beforeStatus, afterStatus, beforeClaimable, afterClaimable)
	}
}
