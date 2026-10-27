package httpadapter

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

func compactCategorySLAForm() url.Values {
	form := url.Values{}
	for _, priority := range []string{"critical", "high", "medium", "low"} {
		form.Set("first_response_duration_"+priority, "1h 30m 20s")
		form.Set("resolve_duration_"+priority, "90m")
	}
	return form
}

func TestCategorySLACompactDurationRoundTrip(t *testing.T) {
	h := newHarness(t)
	path := "/categories/" + strconv.FormatInt(h.bugCategory.ID, 10) + "/sla"
	form := compactCategorySLAForm()
	rec := h.postForm(t, path, form, false)
	wantRedirect(t, rec, http.StatusSeeOther, path)
	body := h.get(t, path, false).Body.String()
	for _, want := range []string{`name="first_response_duration_critical" value="1h 30m 20s"`, `name="resolve_duration_low" value="1h 30m"`} {
		if !strings.Contains(body, want) {
			t.Errorf("compact duration must round-trip %q", want)
		}
	}
	if strings.Count(body, `class="sla-duration-input"`) != 8 {
		t.Error("category must render exactly eight compact inputs")
	}
	if strings.Contains(body, `name="first_response_h_`) {
		t.Error("category must not render legacy hour boxes")
	}
	// Public HTTP round trip proves exact seconds without depending on storage.
	form.Set("first_response_duration_critical", "60s")
	wantRedirect(t, h.postForm(t, path, form, false), http.StatusSeeOther, path)
	if body := h.get(t, path, false).Body.String(); !strings.Contains(body, `name="first_response_duration_critical" value="1m"`) {
		t.Error("60-second boundary must save exactly")
	}
}

func TestCategorySLACompactInvalidIsAtomicAndEchoesText(t *testing.T) {
	for index, bad := range []string{"", "30", "1h 2h", "-1h", "1.5h", "1h junk", "999999999999999999999999999999h", "59s", "0s"} {
		t.Run("case-"+strconv.Itoa(index), func(t *testing.T) {
			h := newHarness(t)
			path := "/categories/" + strconv.FormatInt(h.bugCategory.ID, 10) + "/sla"
			before := h.get(t, path, false).Body.String()
			form := compactCategorySLAForm()
			form.Set("resolve_duration_high", bad)
			rec := h.postForm(t, path, form, false)
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("invalid duration %q status=%d, want422", bad, rec.Code)
			}
			if rec.Header().Get("Location") != "" || rec.Header().Get("X-Save-Feedback") != "" {
				t.Error("rejection must not redirect or announce save")
			}
			for _, want := range []string{`name="resolve_duration_high" value="` + bad + `"`, `name="first_response_duration_critical" value="1h 30m 20s"`, `aria-invalid="true"`} {
				if !strings.Contains(rec.Body.String(), want) {
					t.Errorf("rejection must echo %q", want)
				}
			}
			after := h.get(t, path, false).Body.String()
			if before != after {
				t.Error("rejected compact matrix must leave all public stored targets unchanged")
			}
		})
	}
}

func TestCategorySLACompactMissingFieldCannotFallBackToLegacy(t *testing.T) {
	h := newHarness(t)
	path := "/categories/" + strconv.FormatInt(h.bugCategory.ID, 10) + "/sla"
	form := categorySLAForm()
	for key, values := range compactCategorySLAForm() {
		form[key] = values
	}
	form.Del("resolve_duration_low")
	before := h.get(t, path, false).Body.String()
	rec := h.postForm(t, path, form, false)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("partial compact form status=%d,want422", rec.Code)
	}
	if after := h.get(t, path, false).Body.String(); before != after {
		t.Error("partial compact post must not mutate targets")
	}
}

// TestSLADurationAssetLoadsOnlyOnSLAScreens proves the written-duration
// script is served, refreshes its readings after a Discard reset, and loads on
// the two SLA screens only (issue #395, S9/S10).
func TestSLADurationAssetLoadsOnlyOnSLAScreens(t *testing.T) {
	h := newHarness(t)
	asset := h.get(t, "/static/sla_duration.js", false)
	if asset.Code != http.StatusOK || asset.Header().Get("Content-Type") != "text/javascript; charset=utf-8" {
		t.Fatalf("the duration asset must be served as JavaScript: status=%d", asset.Code)
	}
	for _, want := range []string{"data-sla-durations", "setCustomValidity", "\"reset\""} {
		if !strings.Contains(asset.Body.String(), want) {
			t.Errorf("the duration asset must contain %q", want)
		}
	}
	tag := `<script src="` + mustAssetURL(t, "/static/sla_duration.js") + `" defer></script>`
	categorySLA := "/categories/" + strconv.FormatInt(h.bugCategory.ID, 10) + "/sla"
	for _, path := range []string{categorySLA, "/settings/tickets"} {
		if !strings.Contains(h.get(t, path, false).Body.String(), tag) {
			t.Errorf("%s must load the duration script", path)
		}
	}
	for _, path := range []string{"/settings/organization", "/tickets", "/categories/" + strconv.FormatInt(h.bugCategory.ID, 10) + "/workflow"} {
		if strings.Contains(h.get(t, path, false).Body.String(), "/static/sla_duration.js") {
			t.Errorf("%s must not load the duration script", path)
		}
	}
}

// TestCategorySLACompactSaveLeavesInstanceDefaults proves a category save
// writes that category only: the instance defaults and the panel Settings
// renders from them stay as they were.
func TestCategorySLACompactSaveLeavesInstanceDefaults(t *testing.T) {
	h := newHarness(t)
	wantEnabled, wantPercent, wantDefaults := slaSettingsFixture(t, h)
	before := h.get(t, "/settings/tickets", false).Body.String()
	path := "/categories/" + strconv.FormatInt(h.bugCategory.ID, 10) + "/sla"
	wantRedirect(t, h.postForm(t, path, compactCategorySLAForm(), false), http.StatusSeeOther, path)
	assertSLASettingsEqual(t, h, wantEnabled, wantPercent, wantDefaults)
	if after := h.get(t, "/settings/tickets", false).Body.String(); before != after {
		t.Error("a category save must not change the Settings SLA panel")
	}
}
