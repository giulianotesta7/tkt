package httpadapter

import (
	"archive/zip"
	"encoding/binary"
	"html"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// time.LoadLocation caches ZONEINFO on its first call, including calls during
// package initialization. Start a fresh test process with its fixture configured
// before initialization rather than depending on the host's timezone files.
func TestSettingsCalendarTimezoneCustomStored(t *testing.T) {
	const custom = "Test/Custom_Zone"
	if os.Getenv("TKT_CUSTOM_ZONE_TEST") != "1" {
		archive := filepath.Join(t.TempDir(), "zoneinfo.zip")
		f, err := os.Create(archive)
		if err != nil {
			t.Fatal(err)
		}
		z := zip.NewWriter(f)
		entry, err := z.CreateHeader(&zip.FileHeader{Name: custom, Method: zip.Store})
		if err != nil {
			t.Fatal(err)
		}
		// TZif v1: one fixed-offset type and a four-byte abbreviation.
		data := make([]byte, 44+6+4)
		copy(data, "TZif")
		binary.BigEndian.PutUint32(data[36:40], 1)
		binary.BigEndian.PutUint32(data[40:44], 4)
		binary.BigEndian.PutUint32(data[44:48], 3600)
		copy(data[50:], "TST\x00")
		if _, err := entry.Write(data); err != nil {
			t.Fatal(err)
		}
		if err := z.Close(); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		t.Setenv("ZONEINFO", archive)
		t.Setenv("TKT_CUSTOM_ZONE_TEST", "1")
		cmd := exec.Command(os.Args[0], "-test.run=^TestSettingsCalendarTimezoneCustomStored$", "-test.count=1")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("isolated timezone test: %v\n%s", err, output)
		}
		return
	}

	h := newHarness(t)
	body := sectionGet(t, h, "/settings/calendar")
	if !strings.Contains(body, `name="sla_calendar_timezone" value="UTC"`) {
		t.Fatal("default timezone is not UTC")
	}
	form := url.Values{"sla_calendar_days": {"1"}, "sla_calendar_start": {"09:00"}, "sla_calendar_end": {"17:00"}}
	for _, zone := range []string{"UTC", "America/Argentina/Buenos_Aires", custom} {
		form.Set("sla_calendar_timezone", zone)
		rec := h.postForm(t, "/settings/calendar", form, false)
		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/settings/calendar" {
			t.Fatalf("save %q: status %d, location %q", zone, rec.Code, rec.Header().Get("Location"))
		}
		cal, err := h.sla.Calendar(t.Context())
		if err != nil || cal.Location == nil || cal.Location.String() != zone {
			t.Fatalf("read %q: calendar %v, error %v", zone, cal.Location, err)
		}
	}
	body = sectionGet(t, h, "/settings/calendar")
	if !strings.Contains(body, `name="sla_calendar_timezone" value="`+custom+`"`) || strings.Contains(body, `data-timezone-option="`+custom+`"`) {
		t.Fatal("custom stored zone must render as the current value, not a suggestion")
	}
	before := readStoredCalendar(t, h)
	form.Set("sla_calendar_timezone", "Not/A_Real_Zone")
	rec := h.postForm(t, "/settings/calendar", form, false)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "error-banner") {
		t.Fatalf("invalid timezone response = %d", rec.Code)
	}
	assertCalendarEqual(t, readStoredCalendar(t, h), before)
}

func TestSettingsCalendarTimezoneSuggestions(t *testing.T) {
	h := newHarness(t)
	body := sectionGet(t, h, "/settings/calendar")
	if !strings.Contains(body, `name="sla_calendar_timezone" value="UTC"`) {
		t.Error("the default timezone must be UTC")
	}
	matches := regexp.MustCompile(`data-timezone-option="([^"]+)"`).FindAllStringSubmatch(body, -1)
	if len(matches) < 400 {
		t.Fatalf("expected an IANA suggestion catalogue, got %d options", len(matches))
	}
	if matches[0][1] != "UTC" {
		t.Fatalf("first suggestion = %q, want UTC", matches[0][1])
	}
	seen := map[string]bool{}
	form := url.Values{"sla_calendar_days": {"1"}, "sla_calendar_start": {"09:00"}, "sla_calendar_end": {"17:00"}}
	for _, match := range matches {
		zone := html.UnescapeString(match[1])
		if seen[zone] {
			t.Fatalf("duplicate suggestion %q", zone)
		}
		seen[zone] = true
		form.Set("sla_calendar_timezone", zone)
		rec := h.postForm(t, "/settings/calendar", form, false)
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("suggestion %q rejected: %d", zone, rec.Code)
		}
	}
	form.Set("sla_calendar_timezone", "Not/A_Real_Zone")
	before := readStoredCalendar(t, h)
	rec := h.postForm(t, "/settings/calendar", form, false)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "error-banner") {
		t.Fatalf("invalid timezone response = %d", rec.Code)
	}
	assertCalendarEqual(t, readStoredCalendar(t, h), before)
}
