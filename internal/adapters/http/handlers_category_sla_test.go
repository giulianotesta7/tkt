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

// categorySLADefaultsForm builds the submission that reproduces the seeded
// instance-default matrix (migration 0013), so a row edited from it is the
// only divergent one.
func categorySLADefaultsForm() url.Values {
	targets := map[string][3]string{
		"critical": {"0", "30", "0"},
		"high":     {"1", "0", "0"},
		"medium":   {"4", "0", "0"},
		"low":      {"8", "0", "0"},
	}
	resolves := map[string][3]string{
		"critical": {"4", "0", "0"},
		"high":     {"8", "0", "0"},
		"medium":   {"24", "0", "0"},
		"low":      {"72", "0", "0"},
	}
	form := url.Values{}
	for priority, units := range targets {
		form.Set("first_response_h_"+priority, units[0])
		form.Set("first_response_m_"+priority, units[1])
		form.Set("first_response_s_"+priority, units[2])
		form.Set("resolve_h_"+priority, resolves[priority][0])
		form.Set("resolve_m_"+priority, resolves[priority][1])
		form.Set("resolve_s_"+priority, resolves[priority][2])
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

// TestCategorySLADivergenceMarker proves the shared grid marks a row whose
// targets differ from the instance default for that priority, and leaves a
// matching row unmarked (both directions, so the check is not vacuous).
func TestCategorySLADivergenceMarker(t *testing.T) {
	h := newHarness(t)
	path := "/categories/" + strconv.FormatInt(h.bugCategory.ID, 10) + "/sla"

	form := categorySLADefaultsForm()
	form.Set("resolve_h_high", "9") // high diverges; the other three match
	rec := h.postForm(t, path, form, false)
	wantRedirect(t, rec, http.StatusSeeOther, path)

	body := h.get(t, path, false).Body.String()
	divergent := `<span class="sla-grid-priority">High<span class="sla-diverges">Differs from the instance default</span></span>`
	if !strings.Contains(body, divergent) {
		t.Errorf("divergent high row must render the marker %q, got: %s", divergent, body)
	}
	for _, label := range []string{"Critical", "Medium", "Low"} {
		marked := `<span class="sla-grid-priority">` + label + `<span class="sla-diverges">`
		if strings.Contains(body, marked) {
			t.Errorf("matching %s row must not render the marker, got: %s", label, body)
		}
	}
	if got := strings.Count(body, `class="sla-diverges"`); got != 1 {
		t.Errorf("divergence marker count = %d, want 1", got)
	}
}

// TestCategorySLAResetAppliesInstanceDefaults proves the explicit reset action
// re-applies the instance defaults through the normal validation path and
// leaves the screen rendering them with no divergence marker.
func TestCategorySLAResetAppliesInstanceDefaults(t *testing.T) {
	h := newHarness(t)
	path := "/categories/" + strconv.FormatInt(h.bugCategory.ID, 10) + "/sla"

	if rec := h.postForm(t, path, categorySLAForm(), false); rec.Code != http.StatusSeeOther {
		t.Fatalf("seed divergent save status = %d, want 303", rec.Code)
	}

	// The reset form carries only the explicit action: the submitted matrix
	// must not matter.
	rec := h.postForm(t, path, url.Values{"action": {"reset"}}, false)
	wantRedirect(t, rec, http.StatusSeeOther, path)

	rows, err := h.store.SLAStore().ListByCategory(t.Context(), h.bugCategory.ID)
	if err != nil {
		t.Fatalf("read category matrix: %v", err)
	}
	defaults, err := h.store.SLAStore().ListDefaults(t.Context())
	if err != nil {
		t.Fatalf("read instance defaults: %v", err)
	}
	if len(rows) != len(defaults) {
		t.Fatalf("category matrix length = %d, want %d: %+v", len(rows), len(defaults), rows)
	}
	for i := range defaults {
		if rows[i] != defaults[i] {
			t.Errorf("reset category matrix[%d] = %+v, want instance default %+v", i, rows[i], defaults[i])
		}
	}

	body := h.get(t, path, false).Body.String()
	for _, want := range []string{
		`name="first_response_m_critical" value="30"`,
		`name="resolve_h_high" value="8"`,
		`name="resolve_h_medium" value="24"`,
		`name="resolve_h_low" value="72"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("reset screen must render instance default %q, got: %s", want, body)
		}
	}
	if strings.Contains(body, `class="sla-diverges"`) {
		t.Errorf("after a reset no row may render the divergence marker, got: %s", body)
	}
}

// TestCategorySLANormalSaveDoesNotReset proves the normal save action never
// re-applies the instance defaults: an edited row survives it.
func TestCategorySLANormalSaveDoesNotReset(t *testing.T) {
	h := newHarness(t)
	path := "/categories/" + strconv.FormatInt(h.bugCategory.ID, 10) + "/sla"

	form := categorySLADefaultsForm()
	form.Set("resolve_h_medium", "30")
	form.Set("action", "save")
	rec := h.postForm(t, path, form, false)
	wantRedirect(t, rec, http.StatusSeeOther, path)

	rows, err := h.store.SLAStore().ListByCategory(t.Context(), h.bugCategory.ID)
	if err != nil {
		t.Fatalf("read category matrix: %v", err)
	}
	var medium *domain.SLAPolicy
	for i := range rows {
		if rows[i].Priority == domain.PriorityMedium {
			medium = &rows[i]
		}
	}
	if medium == nil || medium.ResolveSeconds != 30*3600 {
		t.Fatalf("normal save must keep the edited medium resolve, got: %+v", rows)
	}

	body := h.get(t, path, false).Body.String()
	if !strings.Contains(body, `name="resolve_h_medium" value="30"`) {
		t.Errorf("normal save must render the edited value, got: %s", body)
	}
	if strings.Contains(body, `name="resolve_h_medium" value="24"`) {
		t.Errorf("normal save must not reset medium to the instance default, got: %s", body)
	}
}

// TestSLATwoLevelsCopy proves both SLA screens state the two-level model:
// the instance matrix is the default for new categories, while a category's
// own matrix is the operative promise and the frozen tickets keep it.
func TestSLATwoLevelsCopy(t *testing.T) {
	h := newHarness(t)

	categoryBody := h.get(t, "/categories/"+strconv.FormatInt(h.bugCategory.ID, 10)+"/sla", false).Body.String()
	if !strings.Contains(categoryBody, "This category's commitments.") {
		t.Errorf("category SLA page must state whose commitments the matrix holds, got: %s", categoryBody)
	}
	if !strings.Contains(categoryBody, "Tickets already created keep theirs.") {
		t.Errorf("category SLA page must state that created tickets keep their commitments, got: %s", categoryBody)
	}
	if strings.Contains(categoryBody, "Response and resolution commitments for this category.</p>") {
		t.Errorf("category SLA page must not keep the old provenance-free copy, got: %s", categoryBody)
	}

	settingsBody := h.get(t, "/settings", false).Body.String()
	if !strings.Contains(settingsBody, "Editing them does not change existing categories.") {
		t.Errorf("settings page must state that edits do not change existing categories, got: %s", settingsBody)
	}
	if !strings.Contains(settingsBody, "Defaults for new categories.") {
		t.Errorf("settings page must state that the matrix defaults new categories, got: %s", settingsBody)
	}
	if strings.Contains(settingsBody, "Response and resolution commitments carried by new tickets.</p>") {
		t.Errorf("settings page must not keep the old false copy, got: %s", settingsBody)
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
