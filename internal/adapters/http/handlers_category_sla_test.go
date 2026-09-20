package httpadapter

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// Category SLA configuration screen (handlers_category_sla.go): GET and POST
// /categories/{id}/sla are gated on CapManageCategories, the page renders the
// category's materialized 4-priority x 2-milestone matrix, a valid post
// persists all four rows, a rejected post re-renders 422 echoing the
// SUBMITTED values, and an unknown category id is a not-found.

// categorySLAForm builds a complete valid category matrix submission in the
// shared grid's h/m/s fields.
func categorySLAForm() url.Values {
	targets := map[string][2]string{
		"critical": {"0", "8"},
		"high":     {"1", "8"},
		"medium":   {"2", "16"},
		"low":      {"4", "48"},
	}
	form := url.Values{}
	for priority, hours := range targets {
		form.Set("first_response_h_"+priority, hours[0])
		form.Set("first_response_m_"+priority, "30")
		form.Set("first_response_s_"+priority, "0")
		form.Set("resolve_h_"+priority, hours[1])
		form.Set("resolve_m_"+priority, "0")
		form.Set("resolve_s_"+priority, "0")
	}
	return form
}

func getCategorySLAAs(t *testing.T, h *harness, path, sessionID string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Cookie", sessionCookie+"="+sessionID)
	rec := httptest.NewRecorder()
	h.mw.Wrap(h.mux).ServeHTTP(rec, req)
	return rec
}

// TestCategorySLAIndexShowsMaterializedMatrix proves an admin sees the
// category's four materialized rows with the seeded default targets
// decomposed into the grid's h/m/s units.
func TestCategorySLAIndexShowsMaterializedMatrix(t *testing.T) {
	h := newHarness(t)
	path := "/categories/" + strconv.FormatInt(h.bugCategory.ID, 10) + "/sla"

	rec := h.get(t, path, false)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		`>Category SLA</h1>`,
		`<span>` + h.bugCategory.Name + `</span>`, // breadcrumb names the category
		`action="/categories/` + strconv.FormatInt(h.bugCategory.ID, 10) + `/sla"`,
		// Seeded matrix (migration 0015 backfill): critical 1800s/14400s,
		// high 3600s/28800s, low 28800s/259200s.
		`name="first_response_m_critical" value="30"`,
		`name="resolve_h_critical" value="4"`,
		`name="first_response_h_high" value="1"`,
		`name="first_response_h_low" value="8"`,
		`name="resolve_h_low" value="72"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("category SLA page must contain %q, got: %s", want, body)
		}
	}
	// All four priorities appear once as a row.
	for _, priority := range []string{"critical", "high", "medium", "low"} {
		if got := strings.Count(body, `name="first_response_h_`+priority+`"`); got != 1 {
			t.Errorf("priority %q row count = %d, want 1", priority, got)
		}
	}
	// The primary action submits the grid's form by id.
	if !strings.Contains(body, `class="page-action page-action-primary" type="submit" form="sla-form"`) {
		t.Errorf("category SLA page must bind one primary action to sla-form, got: %s", body)
	}
}

// TestCategorySLADeniedForNonAdmin proves an agent gets 403 on both verbs
// and nothing is written.
func TestCategorySLADeniedForNonAdmin(t *testing.T) {
	h := newHarness(t)
	agent := h.createUser(t, "Agent", "agent@tkt.test", "secret")
	session := h.loginCookie(t, agent.Email, "secret")
	if session == "" {
		t.Fatal("agent login must succeed")
	}
	path := "/categories/" + strconv.FormatInt(h.bugCategory.ID, 10) + "/sla"

	getRec := getCategorySLAAs(t, h, path, session)
	if getRec.Code != http.StatusForbidden {
		t.Errorf("agent GET category SLA = %d, want 403", getRec.Code)
	}

	postRec := h.postFormAs(t, path, categorySLAForm(), session)
	if postRec.Code != http.StatusForbidden {
		t.Errorf("agent POST category SLA = %d, want 403", postRec.Code)
	}

	// Nothing written: the materialized matrix still equals the seeded
	// defaults the harness category was created with.
	rows, err := h.store.SLAStore().ListByCategory(t.Context(), h.bugCategory.ID)
	if err != nil {
		t.Fatalf("read category matrix: %v", err)
	}
	want := []domain.SLAPolicy{
		{Priority: domain.PriorityCritical, FirstResponseSeconds: 1800, ResolveSeconds: 14400},
		{Priority: domain.PriorityHigh, FirstResponseSeconds: 3600, ResolveSeconds: 28800},
		{Priority: domain.PriorityMedium, FirstResponseSeconds: 14400, ResolveSeconds: 86400},
		{Priority: domain.PriorityLow, FirstResponseSeconds: 28800, ResolveSeconds: 259200},
	}
	if len(rows) != len(want) {
		t.Fatalf("category matrix length = %d, want %d: %+v", len(rows), len(want), rows)
	}
	for i := range want {
		if rows[i] != want[i] {
			t.Errorf("category matrix[%d] = %+v, want %+v", i, rows[i], want[i])
		}
	}
}

// TestCategorySLAUnknownCategoryNotFound proves an unknown category id is a
// not-found on both verbs, following the category handlers' behaviour.
func TestCategorySLAUnknownCategoryNotFound(t *testing.T) {
	h := newHarness(t)

	getRec := h.get(t, "/categories/999999/sla", false)
	if getRec.Code != http.StatusNotFound {
		t.Errorf("GET unknown category SLA = %d, want 404", getRec.Code)
	}

	postRec := h.postForm(t, "/categories/999999/sla", categorySLAForm(), false)
	if postRec.Code != http.StatusNotFound {
		t.Errorf("POST unknown category SLA = %d, want 404", postRec.Code)
	}
}

// TestCategorySLAPersistsMatrix proves a valid post persists the four
// submitted rows and redirects 303 back to the screen.
func TestCategorySLAPersistsMatrix(t *testing.T) {
	h := newHarness(t)
	path := "/categories/" + strconv.FormatInt(h.bugCategory.ID, 10) + "/sla"

	rec := h.postForm(t, path, categorySLAForm(), false)

	wantRedirect(t, rec, http.StatusSeeOther, path)

	rows, err := h.store.SLAStore().ListByCategory(t.Context(), h.bugCategory.ID)
	if err != nil {
		t.Fatalf("read category matrix: %v", err)
	}
	want := []domain.SLAPolicy{
		{Priority: domain.PriorityCritical, FirstResponseSeconds: 1800, ResolveSeconds: 28800},
		{Priority: domain.PriorityHigh, FirstResponseSeconds: 5400, ResolveSeconds: 28800},
		{Priority: domain.PriorityMedium, FirstResponseSeconds: 9000, ResolveSeconds: 57600},
		{Priority: domain.PriorityLow, FirstResponseSeconds: 16200, ResolveSeconds: 172800},
	}
	if len(rows) != len(want) {
		t.Fatalf("category matrix length = %d, want %d: %+v", len(rows), len(want), rows)
	}
	for i := range want {
		if rows[i] != want[i] {
			t.Errorf("category matrix[%d] = %+v, want %+v", i, rows[i], want[i])
		}
	}
}

// TestCategorySLARejectsInvalidEchoesSubmitted proves a below-floor target
// re-renders 422 with the error banner echoing the SUBMITTED values (not the
// stored ones), and the stored matrix is unchanged.
func TestCategorySLARejectsInvalidEchoesSubmitted(t *testing.T) {
	h := newHarness(t)
	path := "/categories/" + strconv.FormatInt(h.bugCategory.ID, 10) + "/sla"

	form := categorySLAForm()
	form.Set("first_response_h_critical", "5") // stored value decomposes to 0
	// A below-60-second resolve total for critical: the hours and minutes
	// fields must drop too, or the h*3600 term keeps the total above the floor.
	form.Set("resolve_h_critical", "0")
	form.Set("resolve_m_critical", "0")
	form.Set("resolve_s_critical", "30")
	rec := h.postForm(t, path, form, false)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `error-banner`) {
		t.Errorf("rejected save must render the inline error banner, got: %s", body)
	}
	for _, want := range []string{
		// Echoed SUBMITTED values, not the stored matrix.
		`name="first_response_h_critical" value="5"`,
		`name="resolve_s_critical" value="30"`,
		// The other 22 fields survive the round-trip too.
		`name="first_response_m_high" value="30"`,
		`name="resolve_h_low" value="48"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("rejected save must echo submitted value %q, got: %s", want, body)
		}
	}
	if got := rec.Header().Get("X-Save-Feedback"); got != "" {
		t.Errorf("rejected save must not emit save feedback, got %q", got)
	}

	rows, err := h.store.SLAStore().ListByCategory(t.Context(), h.bugCategory.ID)
	if err != nil {
		t.Fatalf("read category matrix: %v", err)
	}
	want := []domain.SLAPolicy{
		{Priority: domain.PriorityCritical, FirstResponseSeconds: 1800, ResolveSeconds: 14400},
		{Priority: domain.PriorityHigh, FirstResponseSeconds: 3600, ResolveSeconds: 28800},
		{Priority: domain.PriorityMedium, FirstResponseSeconds: 14400, ResolveSeconds: 86400},
		{Priority: domain.PriorityLow, FirstResponseSeconds: 28800, ResolveSeconds: 259200},
	}
	if len(rows) != len(want) {
		t.Fatalf("category matrix length = %d, want %d: %+v", len(rows), len(want), rows)
	}
	for i := range want {
		if rows[i] != want[i] {
			t.Errorf("category matrix[%d] = %+v, want %+v", i, rows[i], want[i])
		}
	}
}

// TestCategorySLAPageWhitespace proves the new page honors the amendment 4
// machine check (no whitespace-only lines, no trailing spaces or tabs) even
// though the canonical check's page list has not been extended here.
func TestCategorySLAPageWhitespace(t *testing.T) {
	body := renderGolden(t, "category_sla", "", fixtureCategorySLAData(), false)
	for lineNumber, line := range strings.Split(body, "\n") {
		if (line != "" && strings.TrimSpace(line) == "") || strings.HasSuffix(line, " ") || strings.HasSuffix(line, "\t") {
			t.Errorf("category SLA page has a whitespace-violating line %d: %q", lineNumber+1, line)
		}
	}
}
