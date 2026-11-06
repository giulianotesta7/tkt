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
	"github.com/giulianotesta7/tkt/internal/domain"
)

// The redesigned ticket creation (issue #391) has one shared form and two ways
// to reach the category it needs:
//
//   - Single page (the default): GET /tickets/new renders the form directly and
//     the category is a native <select> grouped one <optgroup> per
//     "Department › Desk" (S3).
//   - Two steps: GET /tickets/new renders a service catalog of category cards,
//     and a card links to /tickets/new?category_id=N which renders the same form
//     with the category fixed and a Change link back (S4).
//
// These tests own the SERVER half: which page a request renders, what the
// picker offers, and that a re-render keeps every submitted value. The browser
// half (picker search, live card filtering, the scripting-disabled journeys) is
// e2e's.

// createModeHarness wires the ticket routes the way the process does (catalog
// included) and returns the mux plus the settings store the mode is written
// through.
func createModeHarness(t *testing.T, h *harness) *http.ServeMux {
	t.Helper()
	catalog := application.NewCatalogService(h.store.CatalogStore(), h.store.CategoryStore(), h.clock).WithFolders(h.store.FolderStore())
	mux := http.NewServeMux()
	NewTicketHandlers(h.tickets, h.comments, h.search, h.categories, h.users, h.store.DeskStore(), h.workflows, application.NewWorkflowRunner(h.clock), h.store.WorkflowRunStore(), h.store.WorkflowUnitOfWork(), h.renderer, catalog).Register(mux)
	return mux
}

// getPath runs an authenticated GET through the middleware-wrapped mux and
// returns the body, with HTML entities unescaped so assertions read the text a
// browser renders.
func getPath(t *testing.T, h *harness, mux *http.ServeMux, target string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.Header.Set("Cookie", sessionCookie+"="+h.adminSession.ID)
	rec := httptest.NewRecorder()
	h.mw.Wrap(mux).ServeHTTP(rec, req)
	return rec.Code, html.UnescapeString(rec.Body.String())
}

// postCreate runs an authenticated POST /tickets through the middleware-wrapped
// mux.
func postCreate(t *testing.T, h *harness, mux *http.ServeMux, form url.Values) (int, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/tickets", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Cookie", sessionCookie+"="+h.adminSession.ID)
	rec := httptest.NewRecorder()
	h.mw.Wrap(mux).ServeHTTP(rec, req)
	return rec.Code, html.UnescapeString(rec.Body.String())
}

// setCreationMode writes the instance setting the same way the Settings screen
// does: through the application service, as an administrator.
func setCreationMode(t *testing.T, h *harness, mode string) {
	t.Helper()
	if err := h.settings.SetTicketCreationMode(t.Context(), *h.admin, mode); err != nil {
		t.Fatalf("set ticket creation mode %q: %v", mode, err)
	}
}

// TestCreateModeDefaultRendersTheFormDirectly pins S1 + S3: an instance that
// never chose a mode creates on one page, so GET /tickets/new is the form, not
// the catalog, and the category is a native select the page can submit without
// any script.
func TestCreateModeDefaultRendersTheFormDirectly(t *testing.T) {
	h := newHarness(t)
	mux := createModeHarness(t, h)

	status, body := getPath(t, h, mux, "/tickets/new")
	if status != http.StatusOK {
		t.Fatalf("GET /tickets/new = %d, want 200: %s", status, body)
	}
	for _, want := range []string{
		`class="ticket-create-detail"`,
		`<h1 class="command-title">Create a ticket</h1>`,
		`<select id="category_id" name="category_id"`,
		`required`,
		`<option value="` + strconv.FormatInt(h.bugCategory.ID, 10) + `">Bugs</option>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the single-page create form must contain %q, got: %s", want, body)
		}
	}
	// S2: the empty Timeline card and the Assignment section are gone.
	for _, absent := range []string{
		`class="card ticket-create-timeline"`,
		`class="prop-heading">Assignment</div>`,
	} {
		if strings.Contains(body, absent) {
			t.Errorf("the create form must not render %q, got: %s", absent, body)
		}
	}
	// The two-step catalog is not this page.
	if strings.Contains(body, `class="create-catalog"`) {
		t.Error("single-page mode must not render the service catalog")
	}
}

// pickerView returns the markup around the create form's category select, so a
// failing assertion shows what was rendered instead of only what was expected.
func pickerView(body string) string {
	const marker = `id="category_id"`
	i := strings.Index(body, marker)
	if i < 0 {
		return "(category picker not found)"
	}
	end := i + 1200
	if end > len(body) {
		end = len(body)
	}
	return body[i:end]
}

// TestCreateModePreselectsTheRequestedCategory pins S5: ?category_id=N marks
// that category in the picker, so every link written against the old catalog
// keeps landing on the same choice, in both modes.
func TestCreateModePreselectsTheRequestedCategory(t *testing.T) {
	h := newHarness(t)
	mux := createModeHarness(t, h)
	bugID := strconv.FormatInt(h.bugCategory.ID, 10)

	for _, mode := range []string{application.TicketCreationSingle, application.TicketCreationTwoStep} {
		t.Run(mode, func(t *testing.T) {
			setCreationMode(t, h, mode)
			status, body := getPath(t, h, mux, "/tickets/new?category_id="+bugID)
			if status != http.StatusOK {
				t.Fatalf("GET preselected = %d, want 200: %s", status, body)
			}
			if mode == application.TicketCreationSingle {
				// The picker marks the requested option, so changing it stays possible.
				if !strings.Contains(body, `<option value="`+bugID+`" selected>Bugs</option>`) {
					t.Errorf("the requested category must be selected in the picker, rendered: %s", pickerView(body))
				}
				return
			}
			// Two-step mode fixes the category the card opened and offers Change back.
			if !strings.Contains(body, `name="category_id" value="`+bugID+`"`) {
				t.Errorf("the requested category must be the fixed one, got: %s", body)
			}
			if !strings.Contains(body, `href="/tickets/new">Change</a>`) {
				t.Errorf("the fixed form must offer Change back to the catalog, got: %s", body)
			}
		})
	}
}

// TestCreateModeValidationErrorKeepsEverySubmittedValue pins S6: a rejected
// submission re-renders the same mode's form with the title, the description,
// the priority and the category exactly as posted, in both modes.
func TestCreateModeValidationErrorKeepsEverySubmittedValue(t *testing.T) {
	h := newHarness(t)
	mux := createModeHarness(t, h)
	bugID := strconv.FormatInt(h.bugCategory.ID, 10)

	for _, mode := range []string{application.TicketCreationSingle, application.TicketCreationTwoStep} {
		t.Run(mode, func(t *testing.T) {
			setCreationMode(t, h, mode)
			status, body := postCreate(t, h, mux, url.Values{
				"title":       {"   "},
				"description": {"Kept description"},
				"category_id": {bugID},
				"priority":    {"high"},
			})
			if status != http.StatusUnprocessableEntity {
				t.Fatalf("create with a blank title = %d, want 422: %s", status, body)
			}
			if !strings.Contains(body, domain.ErrMsgTitleRequired) {
				t.Errorf("mode %s: the re-render must show the error, got: %s", mode, body)
			}
			for _, want := range []string{
				`name="title" value="   "`,
				`>Kept description</textarea>`,
				`<option value="high" selected>High</option>`,
			} {
				if !strings.Contains(body, want) {
					t.Errorf("mode %s: the re-render must keep %q, got: %s", mode, want, body)
				}
			}
			// The category is kept in whatever shape that mode submits it: the
			// picker's selected option on one page, the fixed field on two steps.
			if mode == application.TicketCreationTwoStep {
				if !strings.Contains(body, `name="category_id" value="`+bugID+`"`) {
					t.Errorf("the two-step re-render must keep the fixed category, got: %s", body)
				}
			} else if !strings.Contains(body, `<option value="`+bugID+`" selected>Bugs</option>`) {
				t.Errorf("the single-page re-render must keep the selected category, got: %s", pickerView(body))
			}
		})
	}
}

// TestCreateModeSettingPersistsThroughSettings pins S1's write path: the choice
// is made in Settings › Tickets, validated against the closed set, gated on the
// capability that manages settings, and lands back on the section.
func TestCreateModeSettingPersistsThroughSettings(t *testing.T) {
	h := newHarness(t)

	before := h.get(t, "/settings/tickets", false)
	if before.Code != http.StatusOK {
		t.Fatalf("GET /settings/tickets = %d, want 200: %s", before.Code, before.Body.String())
	}
	for _, want := range []string{
		`action="/settings/ticket-creation"`,
		`name="ticket_creation_mode" value="single" checked`,
		`name="ticket_creation_mode" value="two_step"`,
		`Save creation flow`,
	} {
		if !strings.Contains(before.Body.String(), want) {
			t.Errorf("Settings › Tickets must offer the creation choice, missing %q: %s", want, before.Body.String())
		}
	}

	saved := h.postForm(t, "/settings/ticket-creation", url.Values{"ticket_creation_mode": {application.TicketCreationTwoStep}}, false)
	wantRedirect(t, saved, http.StatusSeeOther, "/settings/tickets")
	if got, err := h.settings.GetTicketCreationMode(t.Context()); err != nil || got != application.TicketCreationTwoStep {
		t.Fatalf("stored mode = %q (err %v), want %q", got, err, application.TicketCreationTwoStep)
	}
	after := h.get(t, "/settings/tickets", false).Body.String()
	if !strings.Contains(after, `name="ticket_creation_mode" value="two_step" checked`) {
		t.Errorf("the section must render the stored choice, got: %s", after)
	}

	// An unknown value is refused and changes nothing.
	rejected := h.postForm(t, "/settings/ticket-creation", url.Values{"ticket_creation_mode": {"wizard"}}, false)
	if rejected.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unknown mode = %d, want 422: %s", rejected.Code, rejected.Body.String())
	}
	if !strings.Contains(rejected.Body.String(), "invalid ticket creation mode") {
		t.Errorf("the refusal must say what is wrong, got: %s", rejected.Body.String())
	}
	if got, err := h.settings.GetTicketCreationMode(t.Context()); err != nil || got != application.TicketCreationTwoStep {
		t.Errorf("a refused save changed the stored mode: %q (err %v)", got, err)
	}

	// The capability that manages settings is the one that owns this setting.
	agent := seedUserRole(t, h.store, "Agent", "agent-mode@tkt.test", domain.RoleAgent)
	session := seedSession(t, h.store, agent.ID)
	req := httptest.NewRequest(http.MethodPost, "/settings/ticket-creation", strings.NewReader("ticket_creation_mode=single"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Cookie", sessionCookie+"="+session.ID)
	rec := httptest.NewRecorder()
	h.mw.Wrap(h.mux).ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("agent POST /settings/ticket-creation = %d, want 403", rec.Code)
	}
	if got, err := h.settings.GetTicketCreationMode(t.Context()); err != nil || got != application.TicketCreationTwoStep {
		t.Errorf("a forbidden save changed the stored mode: %q (err %v)", got, err)
	}
}
