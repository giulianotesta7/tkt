package httpadapter

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// simpleManualDef is a pinned single manual_task step: a human-pending workflow
// whose creation leaves the ticket in "new" with an active run (no automatic
// advancement). It is the workhorse fixture for tests that need a category to be
// available for new tickets without mutating the ticket's state.
func simpleManualDef() domain.WorkflowDefinition {
	return domain.WorkflowDefinition{{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "handle"}}}
}

// TestTicketCreateCategoryWithoutWorkflow422 proves a category with no current
// published workflow is unavailable for new tickets (design S5 availability = a
// published version exists): create answers the exact 422 category contract and
// persists NO ticket, audit, or run rows.
func TestTicketCreateCategoryWithoutWorkflow422(t *testing.T) {
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
		t.Errorf("re-render must show %q, got: %s", domain.ErrMsgCategoryWorkflowUnavailable, rec.Body.String())
	}

	_, err = h.tickets.GetByID(t.Context(), *h.admin, 1)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("no ticket may be stored, GetByID(1) err = %v (want ErrNotFound)", err)
	}
	db := h.rawDB(t)
	if n := scanOneInt(t, db, "SELECT COUNT(*) FROM audit_events"); n != 0 {
		t.Errorf("audit_events rows = %d, want 0", n)
	}
	if n := scanOneInt(t, db, "SELECT COUNT(*) FROM ticket_workflow_runs"); n != 0 {
		t.Errorf("ticket_workflow_runs rows = %d, want 0", n)
	}
}

// TestTicketCreatePublishedPinsVersionPersistsAtomically proves a published
// workflow makes its category available and that a successful create persists
// the ticket pinned to the exact current version, the created audit, and the
// active initial run together (all-or-nothing, design S5).
func TestTicketCreatePublishedPinsVersionPersistsAtomically(t *testing.T) {
	h := newHarness(t)
	vid := h.publishWorkflow(t, h.bugCategory.ID, simpleManualDef())

	form := ticketForm(func(f url.Values) { f.Set("category_id", strconv.FormatInt(h.bugCategory.ID, 10)) })
	rec := h.postForm(t, "/tickets", form, false)
	wantRedirect(t, rec, http.StatusSeeOther, "/tickets")

	view, err := h.tickets.GetByID(t.Context(), *h.admin, 1)
	if err != nil {
		t.Fatalf("created ticket must be readable: %v", err)
	}
	tid := view.Ticket.ID
	db := h.rawDB(t)

	pin, ok := scanOneNullableInt(t, db, "SELECT workflow_version_id FROM tickets WHERE id=?", tid)
	if !ok || pin != vid {
		t.Errorf("ticket pin = (%d, %v), want (%d, true)", pin, ok, vid)
	}
	if n := scanOneInt(t, db, "SELECT COUNT(*) FROM ticket_workflow_runs WHERE ticket_id=?", tid); n != 1 {
		t.Errorf("run rows = %d, want 1", n)
	}
	if status := scanOneString(t, db, "SELECT status FROM ticket_workflow_runs WHERE ticket_id=?", tid); status != "active" {
		t.Errorf("run status = %q, want active", status)
	}
	if n := scanOneInt(t, db, "SELECT COUNT(*) FROM audit_events WHERE ticket_id=? AND action='created'", tid); n != 1 {
		t.Errorf("created audit rows = %d, want 1", n)
	}
}

// TestTicketCreateLeastLoadedPublishRefused proves the GATE. A workflow whose
// assignment step routes to a desk nobody belongs to is refused at publish:
// every ticket filed against that category would fail on creation.
func TestTicketCreateLeastLoadedPublishRefused(t *testing.T) {
	h := newHarness(t)
	desk, err := h.desks.Create(t.Context(), *h.admin, "Support")
	if err != nil {
		t.Fatalf("create desk: %v", err)
	}
	cat, err := h.categories.Create(t.Context(), "Ops")
	if err != nil {
		t.Fatalf("create category: %v", err)
	}
	def := domain.WorkflowDefinition{{
		Type:         domain.StepAssignToDesk,
		AssignToDesk: &domain.AssignToDeskStep{DeskID: desk.ID, Strategy: domain.StrategyLeastLoaded},
	}}
	b, err := def.MarshalCanonical()
	if err != nil {
		t.Fatalf("canonical: %v", err)
	}
	vid, iss, err := h.store.WorkflowStore().Publish(t.Context(), cat.ID, b, nil)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if vid != 0 || len(iss) == 0 {
		t.Fatalf("an unrunnable workflow must be refused at publish, got vid=%d issues=%v", vid, iss)
	}
	if n := scanOneInt(t, h.rawDB(t), "SELECT COUNT(*) FROM workflow_versions WHERE category_id=?", cat.ID); n != 0 {
		t.Errorf("a refused publish must write no version, got %d", n)
	}
}

// TestTicketCreateLeastLoadedFailureRollsBack proves the RUNTIME guard still
// holds, because the gate cannot cover everything: a workflow that was healthy
// when it was published becomes unrunnable the moment the desk loses its last
// member. That create must roll back with no partial rows, and it must surface
// the TYPED error — this test used to assert a bare 500, which is the defect.
func TestTicketCreateLeastLoadedFailureRollsBack(t *testing.T) {
	h := newHarness(t)
	desk, err := h.desks.Create(t.Context(), *h.admin, "Support")
	if err != nil {
		t.Fatalf("create desk: %v", err)
	}
	h.staff(t, desk.ID) // healthy at publish time
	ops, err := h.categories.Create(t.Context(), "Ops")
	if err != nil {
		t.Fatalf("create category: %v", err)
	}
	h.publishWorkflow(t, ops.ID, domain.WorkflowDefinition{{
		Type:         domain.StepAssignToDesk,
		AssignToDesk: &domain.AssignToDeskStep{DeskID: desk.ID, Strategy: domain.StrategyLeastLoaded},
	}})
	if err := h.desks.RemoveMember(t.Context(), *h.admin, desk.ID, h.admin.ID); err != nil {
		t.Fatalf("remove the last desk member: %v", err)
	}

	form := ticketForm(func(f url.Values) { f.Set("category_id", strconv.FormatInt(ops.ID, 10)) })
	rec := h.postForm(t, "/tickets", form, false)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d — a typed error, never a bare 500", rec.Code, http.StatusConflict)
	}
	if !strings.Contains(rec.Body.String(), domain.ErrMsgCategoryNotRunnable) {
		t.Errorf("the requester must be told why, got %q", rec.Body.String())
	}
	db := h.rawDB(t)
	if n := scanOneInt(t, db, "SELECT COUNT(*) FROM tickets"); n != 0 {
		t.Errorf("tickets rows = %d, want 0 (rollback)", n)
	}
	if n := scanOneInt(t, db, "SELECT COUNT(*) FROM audit_events"); n != 0 {
		t.Errorf("audit_events rows = %d, want 0 (rollback)", n)
	}
	if n := scanOneInt(t, db, "SELECT COUNT(*) FROM ticket_workflow_runs"); n != 0 {
		t.Errorf("ticket_workflow_runs rows = %d, want 0 (rollback)", n)
	}
}

// TestTicketCreateLaterPublishDoesNotChangePin proves a ticket pins the EXACT
// version current at creation: publishing a later version afterwards never
// rewrites the already-created ticket's pin (design S5 immutability).
func TestTicketCreateLaterPublishDoesNotChangePin(t *testing.T) {
	h := newHarness(t)
	v1 := h.publishWorkflow(t, h.bugCategory.ID, simpleManualDef())

	form := ticketForm(func(f url.Values) { f.Set("category_id", strconv.FormatInt(h.bugCategory.ID, 10)) })
	rec := h.postForm(t, "/tickets", form, false)
	wantRedirect(t, rec, http.StatusSeeOther, "/tickets")

	view, err := h.tickets.GetByID(t.Context(), *h.admin, 1)
	if err != nil {
		t.Fatalf("created ticket must be readable: %v", err)
	}
	tid := view.Ticket.ID

	// Publish a later version for the same category (new version id).
	v2 := h.publishWorkflow(t, h.bugCategory.ID, simpleManualDef())
	if v2 == v1 {
		t.Fatal("test requires a distinct later version id")
	}

	db := h.rawDB(t)
	pin, ok := scanOneNullableInt(t, db, "SELECT workflow_version_id FROM tickets WHERE id=?", tid)
	if !ok || pin != v1 {
		t.Errorf("ticket pin = (%d, %v), want v1 (%d, true) after a later publish", pin, ok, v1)
	}
}
