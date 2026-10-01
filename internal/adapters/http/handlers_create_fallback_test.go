package httpadapter

import (
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
)

// TestCreateFallbackCategorySelectUsesThePickerRule pins issue #247.
//
// The create form has two branches. The normal one shows the chosen category
// read-only, so an unrunnable category was already handled there by the picker.
// The fallback branch — rendered when NO category is selected, which POST
// /tickets with an empty category_id reaches through renderCreateError —
// still built its own <select> from the narrower published-only rule
// (ListAvailableCategories), so it offered a category that cannot move a
// ticket and whose submit the create guard refuses.
//
// The fallback select must read the SAME requester-safe rule the picker does:
// an unpublished category stays absent, a published-and-runnable category is a
// normal option, and a published-but-unrunnable category is present, disabled,
// and carries the reason.
func TestCreateFallbackCategorySelectUsesThePickerRule(t *testing.T) {
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
	unrunnable, err := h.categories.CreateWithDescriptionFor(t.Context(), *h.admin, "Onboarding", "New hire setup", desk.ID)
	if err != nil {
		t.Fatalf("create category: %v", err)
	}
	// publishWorkflow staffs the desk first, so the category starts out healthy.
	h.publishWorkflow(t, unrunnable.ID, assignToDeskDef(desk.ID))
	// The desk loses its last member, so the already-published version can no
	// longer move a ticket.
	if err := h.desks.RemoveMember(t.Context(), *h.admin, desk.ID, h.admin.ID); err != nil {
		t.Fatalf("remove member: %v", err)
	}
	// A category with no published version, which must stay absent.
	unpublished, err := h.categories.CreateWithDescriptionFor(t.Context(), *h.admin, "Refunds", "Money back", desk.ID)
	if err != nil {
		t.Fatalf("create unpublished category: %v", err)
	}

	// POST /tickets with NO category_id is exactly the reachable path that
	// re-renders the fallback branch: parseID("") == 0 leaves Selected nil.
	form := url.Values{"title": {"Untitled"}, "description": {"probe"}, "priority": {"medium"}}
	req := httptest.NewRequest(http.MethodPost, "/tickets", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Cookie", sessionCookie+"="+h.adminSession.ID)
	rec := httptest.NewRecorder()
	h.mw.Wrap(mux).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422: %s", rec.Code, rec.Body.String())
	}
	body := html.UnescapeString(rec.Body.String())

	// Published but unable to run: present, disabled, and it says why.
	unrunnableValue := strconv.FormatInt(unrunnable.ID, 10)
	unrunnableOption := `<option value="` + unrunnableValue + `" disabled>`
	if !strings.Contains(body, unrunnableOption) {
		t.Errorf("unrunnable category must be a disabled option %q, rendered: %s", unrunnableOption, fallbackSelect(body))
	}
	if want := "Can't run · Support has no active members"; !strings.Contains(body, want) {
		t.Errorf("unrunnable option must carry its reason %q, rendered: %s", want, fallbackSelect(body))
	}

	// Published and runnable: a normal, selectable option.
	healthyOption := `<option value="` + strconv.FormatInt(h.bugCategory.ID, 10) + `">`
	if !strings.Contains(body, healthyOption) {
		t.Errorf("runnable category must stay selectable, missing %q, rendered: %s", healthyOption, fallbackSelect(body))
	}

	// Never published: absent, exactly as before.
	if strings.Contains(body, `value="`+strconv.FormatInt(unpublished.ID, 10)+`"`) {
		t.Errorf("an unpublished category must stay out of the fallback select, rendered: %s", fallbackSelect(body))
	}
}

// fallbackSelect returns the markup around the create form's category select,
// so a failing assertion shows what was actually rendered.
func fallbackSelect(body string) string {
	const marker = `id="category_id"`
	i := strings.Index(body, marker)
	if i < 0 {
		return "(fallback select not found)"
	}
	end := i + 500
	if end > len(body) {
		end = len(body)
	}
	return body[i:end]
}
