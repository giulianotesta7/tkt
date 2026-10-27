package httpadapter

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// Settings SLA written durations (issue #395, S10): the instance defaults
// take the same one-field-per-target duration as the category screen, while
// the panel keeps its enabled flag, warning percent and one atomic save.

func compactSettingsSLAForm(percent string) url.Values {
	form := url.Values{"sla_warning_percent": {percent}, "sla_enabled": {"1"}}
	for _, priority := range []string{"critical", "high", "medium", "low"} {
		form.Set("first_response_duration_"+priority, "1h 30m 20s")
		form.Set("resolve_duration_"+priority, "90m")
	}
	return form
}

func TestSettingsSLACompactDurationsPersistAndRender(t *testing.T) {
	h := newHarness(t)

	rec := h.postForm(t, "/settings/sla", compactSettingsSLAForm("70"), false)

	wantRedirect(t, rec, http.StatusSeeOther, "/settings/tickets")
	want := make([]domain.SLAPolicy, 0, 4)
	for _, priority := range []domain.Priority{domain.PriorityCritical, domain.PriorityHigh, domain.PriorityMedium, domain.PriorityLow} {
		want = append(want, domain.SLAPolicy{Priority: priority, FirstResponseSeconds: 5420, ResolveSeconds: 5400})
	}
	assertSLASettingsEqual(t, h, true, 70, want)

	body := sectionGet(t, h, "/settings/tickets")
	for _, wantText := range []string{
		`name="first_response_duration_critical" value="1h 30m 20s"`,
		`name="resolve_duration_low" value="1h 30m"`,
		`<p id="first_response_duration_critical-reading" class="sla-duration-reading">1 h 30 min 20 s</p>`,
		`<script src="` + mustAssetURL(t, "/static/sla_duration.js") + `" defer></script>`,
	} {
		if !strings.Contains(body, wantText) {
			t.Errorf("Settings SLA panel must render %q", wantText)
		}
	}
	if got := strings.Count(body, `class="sla-duration-input"`); got != 8 {
		t.Errorf("Settings SLA panel duration inputs = %d, want 8", got)
	}
	if strings.Contains(body, `name="first_response_h_critical"`) {
		t.Error("Settings SLA panel must not render the hour/minute/second boxes")
	}
}

func TestSettingsSLACompactInvalidIsAtomicAndEchoesPanel(t *testing.T) {
	for index, bad := range []string{"", "30", "1h 2h", "-1h", "1.5h", "1h junk", "999999999999999999999999999999h", "59s"} {
		t.Run("case-"+strconv.Itoa(index), func(t *testing.T) {
			h := newHarness(t)
			wantEnabled, wantPercent, wantDefaults := slaSettingsFixture(t, h)
			form := compactSettingsSLAForm("66")
			form.Set("resolve_duration_high", bad)

			rec := h.postForm(t, "/settings/sla", form, false)

			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("invalid duration %q status = %d, want 422", bad, rec.Code)
			}
			if rec.Header().Get("Location") != "" || rec.Header().Get("X-Save-Feedback") != "" {
				t.Error("a rejected panel must not redirect or announce a save")
			}
			body := rec.Body.String()
			for _, wantText := range []string{
				`name="resolve_duration_high" value="` + bad + `"`,
				`name="first_response_duration_critical" value="1h 30m 20s"`,
				`name="sla_warning_percent" class="sla-warning-input" min="1" max="99" value="66"`,
				`name="sla_enabled" value="1" checked`,
				`aria-invalid="true"`,
				`role="alert"`,
			} {
				if !strings.Contains(body, wantText) {
					t.Errorf("rejected panel must echo %q", wantText)
				}
			}
			assertSLASettingsEqual(t, h, wantEnabled, wantPercent, wantDefaults)
		})
	}
}

func TestSettingsSLACompactMissingFieldCannotFallBackToLegacy(t *testing.T) {
	h := newHarness(t)
	wantEnabled, wantPercent, wantDefaults := slaSettingsFixture(t, h)
	form := slaPanelForm("70")
	for key, values := range compactSettingsSLAForm("70") {
		form[key] = values
	}
	form.Del("resolve_duration_low")

	rec := h.postForm(t, "/settings/sla", form, false)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("partial compact panel status = %d, want 422", rec.Code)
	}
	assertSLASettingsEqual(t, h, wantEnabled, wantPercent, wantDefaults)
}

func TestSettingsSLADefaultsLeaveCategoriesUnchanged(t *testing.T) {
	h := newHarness(t)
	before, err := h.store.SLAStore().ListByCategory(t.Context(), h.bugCategory.ID)
	if err != nil {
		t.Fatalf("read category matrix: %v", err)
	}

	wantRedirect(t, h.postForm(t, "/settings/sla", compactSettingsSLAForm("70"), false), http.StatusSeeOther, "/settings/tickets")

	after, err := h.store.SLAStore().ListByCategory(t.Context(), h.bugCategory.ID)
	if err != nil {
		t.Fatalf("read category matrix: %v", err)
	}
	if len(after) != len(before) {
		t.Fatalf("category matrix length = %d, want %d", len(after), len(before))
	}
	for i := range before {
		if after[i] != before[i] {
			t.Errorf("instance defaults must not rewrite an existing category: row %d = %+v, want %+v", i, after[i], before[i])
		}
	}
}
