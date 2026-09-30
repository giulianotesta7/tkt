package httpadapter

import (
	"html"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// assignToDeskDef routes a ticket to one desk. The desk must have an eligible
// member for the definition to be publishable, which is what lets this test
// publish a HEALTHY category and then break it by emptying the desk.
func assignToDeskDef(deskID int64) domain.WorkflowDefinition {
	return domain.WorkflowDefinition{{
		Type:         domain.StepAssignToDesk,
		AssignToDesk: &domain.AssignToDeskStep{DeskID: deskID, Strategy: domain.StrategyLeastLoaded},
	}}
}

// catalogPickerPage renders the requester's picker for one desk.
func catalogPickerPage(t *testing.T, h *harness, mux *http.ServeMux, query string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/tickets/new?"+query, nil)
	req.Header.Set("Cookie", sessionCookie+"="+h.adminSession.ID)
	rec := httptest.NewRecorder()
	h.mw.Wrap(mux).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("catalog = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	// html/template escapes "'" to "&#39;", so assertions read the text as a
	// browser renders it rather than the entity soup in the response.
	return html.UnescapeString(rec.Body.String())
}

// pickerRow returns the markup around one picker row, so a failing assertion
// shows what was actually rendered instead of only what was expected.
func pickerRow(body, marker string) string {
	i := strings.Index(body, marker)
	if i < 0 {
		return "(marker not found)"
	}
	end := i + 300
	if end > len(body) {
		end = len(body)
	}
	return body[i:end]
}

// TestCatalogUnavailableCategoryStaysVisibleAndExplainsItself pins issue #239.
//
// A published category that cannot move a ticket must not be offered like a
// healthy one, and must not be silently removed either. Hiding it would leave
// the picker's category column mute: the empty state ("No published categories
// in this desk yet.") is server-rendered only when NOTHING is published, so
// reusing it here would state the opposite of the truth — the category IS
// published, it just cannot run. So it stays, as a non-link, carrying the same
// reason the admin's categories screen shows.
func TestCatalogUnavailableCategoryStaysVisibleAndExplainsItself(t *testing.T) {
	h := newHarness(t)
	catalog := application.NewCatalogService(h.store.CatalogStore(), h.store.CategoryStore(), h.clock)
	mux := http.NewServeMux()
	NewTicketHandlers(h.tickets, h.comments, h.search, h.categories, h.users, h.store.DeskStore(), h.workflows, application.NewWorkflowRunner(h.clock), h.store.WorkflowRunStore(), h.store.WorkflowUnitOfWork(), h.renderer, catalog).Register(mux)

	department, err := catalog.CreateDepartmentFor(t.Context(), *h.admin, "Operations", "Operations department")
	if err != nil {
		t.Fatalf("create department: %v", err)
	}
	desk, err := catalog.CreateDeskFor(t.Context(), *h.admin, department.ID, "Support")
	if err != nil {
		t.Fatalf("create desk: %v", err)
	}
	category, err := h.categories.CreateWithDescriptionFor(t.Context(), *h.admin, "Onboarding", "New hire setup", desk.ID)
	if err != nil {
		t.Fatalf("create category: %v", err)
	}
	// publishWorkflow staffs the desk first, so the definition is publishable and
	// the category starts out healthy.
	h.publishWorkflow(t, category.ID, assignToDeskDef(desk.ID))

	deskQuery := "department_id=" + strconv.FormatInt(department.ID, 10) + "&desk_id=" + strconv.FormatInt(desk.ID, 10)
	categoryRef := "category_id=" + strconv.FormatInt(category.ID, 10)

	healthy := catalogPickerPage(t, h, mux, deskQuery)
	if !strings.Contains(healthy, `class="catalog-category" href=`) || !strings.Contains(healthy, categoryRef) {
		t.Fatalf("a runnable category must be offered as a link:\n%s", healthy)
	}
	if strings.Contains(healthy, "catalog-category unavailable") {
		t.Error("a runnable category must not render the unavailable state")
	}

	// The anomaly this whole issue is about: the desk loses its last member, so
	// the already-published version can no longer move a ticket.
	if err := h.desks.RemoveMember(t.Context(), *h.admin, desk.ID, h.admin.ID); err != nil {
		t.Fatalf("remove member: %v", err)
	}

	broken := catalogPickerPage(t, h, mux, deskQuery)
	if !strings.Contains(broken, "Onboarding") {
		t.Fatal("an unrunnable category must stay visible in the picker, not vanish")
	}
	if strings.Contains(broken, categoryRef) {
		t.Error("an unrunnable category must not link into a form whose submit the create guard refuses")
	}
	if !strings.Contains(broken, `class="catalog-category unavailable"`) {
		t.Error("an unrunnable category must render the unavailable state")
	}
	if want := "Can't run · Support has no active members"; !strings.Contains(broken, want) {
		t.Errorf("unrunnable row must carry its reason %q, rendered: %s", want, pickerRow(broken, "catalog-category unavailable"))
	}
	if strings.Contains(broken, "No published categories") {
		t.Error("the empty state must not claim nothing is published while a published category is on screen")
	}

	// Search reaches the same fact through the same summary, so the result must
	// not offer a dead end either.
	searched := catalogPickerPage(t, h, mux, "q=Onboarding")
	if !strings.Contains(searched, `class="catalog-result unavailable"`) {
		t.Error("a search result for an unrunnable category must carry the unavailable state")
	}
	if want := "Can't run · Support has no active members"; !strings.Contains(searched, want) {
		t.Errorf("search result must carry its reason %q, rendered: %s", want, pickerRow(searched, "catalog-result unavailable"))
	}
	if strings.Contains(searched, categoryRef) {
		t.Error("a search result for an unrunnable category must not be a link")
	}
}

// TestCatalogUnpublishedCategoryStaysAbsent pins the rule that predates #239:
// with no published version the category is not offered at all. That is a normal
// setup state rather than an anomaly, so it does not get the unavailable row —
// it stays out of the picker entirely.
func TestCatalogUnpublishedCategoryStaysAbsent(t *testing.T) {
	h := newHarness(t)
	catalog := application.NewCatalogService(h.store.CatalogStore(), h.store.CategoryStore(), h.clock)
	mux := http.NewServeMux()
	NewTicketHandlers(h.tickets, h.comments, h.search, h.categories, h.users, h.store.DeskStore(), h.workflows, application.NewWorkflowRunner(h.clock), h.store.WorkflowRunStore(), h.store.WorkflowUnitOfWork(), h.renderer, catalog).Register(mux)

	department, err := catalog.CreateDepartmentFor(t.Context(), *h.admin, "Operations", "Operations department")
	if err != nil {
		t.Fatalf("create department: %v", err)
	}
	desk, err := catalog.CreateDeskFor(t.Context(), *h.admin, department.ID, "Support")
	if err != nil {
		t.Fatalf("create desk: %v", err)
	}
	draftOnly, err := h.categories.CreateWithDescriptionFor(t.Context(), *h.admin, "Refunds", "Money back", desk.ID)
	if err != nil {
		t.Fatalf("create category: %v", err)
	}
	if err := h.workflows.SaveDraft(t.Context(), *h.admin, draftOnly.ID, assignToDeskDef(desk.ID)); err != nil {
		t.Fatalf("save draft: %v", err)
	}

	body := catalogPickerPage(t, h, mux, "department_id="+strconv.FormatInt(department.ID, 10)+"&desk_id="+strconv.FormatInt(desk.ID, 10))
	if strings.Contains(body, "Refunds") {
		t.Error("a category with no published version must stay out of the picker")
	}
	if !strings.Contains(body, "No published categories") {
		t.Error("a desk with nothing published must still show the empty state")
	}
}
