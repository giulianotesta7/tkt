package httpadapter

import (
	"encoding/csv"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// The CSV downloads (issue #409, specs S20-S27). These cases go through the
// real routes, the real middleware and the real services, so what they prove is
// what a browser would receive.

// getCSV performs an authenticated GET and parses the body as CSV, stripping
// the byte-order mark Excel needs.
func getCSV(t *testing.T, rec *httptest.ResponseRecorder) [][]string {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.HasPrefix(body, "\ufeff") {
		t.Fatal("the response has no UTF-8 byte-order mark, so Excel would mangle accented text")
	}
	rows, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(body, "\ufeff"))).ReadAll()
	if err != nil {
		t.Fatalf("parse CSV: %v\n%s", err, body)
	}
	return rows
}

func (h *harness) getAs(t *testing.T, path, session string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Cookie", sessionCookie+"="+session)
	rec := httptest.NewRecorder()
	h.mw.Wrap(h.mux).ServeHTTP(rec, req)
	return rec
}

// S26: the response must present itself as a downloadable CSV file.
func TestTicketExportIsADownloadableCSVFile(t *testing.T) {
	h := newHarness(t)
	h.seedTicket(t, "printer down", nil)

	rec := h.get(t, "/tickets/export.csv", false)
	if got := rec.Header().Get("Content-Type"); got != "text/csv; charset=utf-8" {
		t.Errorf("Content-Type = %q, want a UTF-8 CSV", got)
	}
	if got := rec.Header().Get("Content-Disposition"); !strings.Contains(got, "attachment") || !strings.Contains(got, "tickets.csv") {
		t.Errorf("Content-Disposition = %q, want an attachment named tickets.csv", got)
	}
	rows := getCSV(t, rec)
	if len(rows) < 2 {
		t.Fatalf("rows = %v, want a header and at least one ticket", rows)
	}
	if len(rows[0]) != len(ticketExportColumns) {
		t.Fatalf("header = %v, want %d columns", rows[0], len(ticketExportColumns))
	}
}

// S26: accented Spanish text must survive the round trip unchanged.
func TestTicketExportKeepsAccentedText(t *testing.T) {
	h := newHarness(t)
	h.seedTicket(t, "La impresión está rota", nil)

	rows := getCSV(t, h.get(t, "/tickets/export.csv", false))
	if !strings.Contains(rows[1][1], "La impresión está rota") {
		t.Fatalf("title = %q, want the accented title unchanged", rows[1][1])
	}
}

// S21: the list shows ten rows per page; the export must carry every match.
func TestTicketExportCarriesEveryMatchNotOnePage(t *testing.T) {
	h := newHarness(t)
	const count = application.PageSize + 7
	for i := 0; i < count; i++ {
		h.seedTicket(t, "bulk ticket", nil)
	}

	rows := getCSV(t, h.get(t, "/tickets/export.csv", false))
	if got := len(rows) - 1; got != count {
		t.Fatalf("exported %d tickets, want %d", got, count)
	}
}

// S23: the file must agree with the screen, so the list filters apply.
func TestTicketExportHonoursTheListFilters(t *testing.T) {
	h := newHarness(t)
	h.seedTicket(t, "keep me", nil)
	other := h.seedTicket(t, "filtered out", nil)
	h.seedTransition(t, other.ID, domain.StateInProgress, "")

	rows := getCSV(t, h.get(t, "/tickets/export.csv?state=new", false))
	if got := len(rows) - 1; got != 1 {
		t.Fatalf("exported %d tickets, want only the one matching the filter: %v", got, rows)
	}
	if rows[1][1] != "keep me" {
		t.Errorf("exported %q, want the ticket matching the filter", rows[1][1])
	}
}

// S22: the export is not a second way to read tickets. A requester downloads
// their own tickets and nobody else's.
func TestTicketExportAppliesTheActorScope(t *testing.T) {
	h := newHarness(t)
	requester := seedUserRole(t, h.store, "Carol", "carol@export.test", domain.RoleUser)
	mine := h.seedTicket(t, "my own problem", nil)
	if _, err := h.rawDB(t).Exec(`UPDATE tickets SET requester_user_id = ? WHERE id = ?`, requester.ID, mine.ID); err != nil {
		t.Fatalf("link requester: %v", err)
	}
	h.seedTicket(t, "somebody else's problem", nil)

	rows := getCSV(t, h.getAs(t, "/tickets/export.csv", h.sessionFor(t, requester.ID)))
	if got := len(rows) - 1; got != 1 {
		t.Fatalf("exported %d tickets, want only the requester's own: %v", got, rows)
	}
	if rows[1][1] != "my own problem" {
		t.Errorf("exported %q, want the requester's own ticket", rows[1][1])
	}
	// SLA is staff-only on screen, so it is staff-only in the file too.
	if sla := rows[1][len(rows[1])-1]; sla != "" {
		t.Errorf("SLA column = %q, want it empty for a requester", sla)
	}
}

// S22: the metrics export stays behind the capability that gates the
// dashboard; a requester is refused before any row is written.
func TestMetricsExportRefusesAnActorWithoutTheCapability(t *testing.T) {
	h := newHarness(t)
	requester := seedUserRole(t, h.store, "Carol", "carol@metrics-export.test", domain.RoleUser)

	rec := h.getAs(t, "/tickets/metrics/export.csv", h.sessionFor(t, requester.ID))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Header().Get("Content-Type"), "text/csv") {
		t.Error("a refused export must not answer as a CSV download")
	}
}

// S25: the metrics download carries the dashboard's aggregates in the long
// shape a spreadsheet pivots.
func TestMetricsExportCarriesTheDashboardAggregates(t *testing.T) {
	h := newHarness(t)
	resolved := h.seedTicket(t, "resolved fast", nil)
	h.seedTransition(t, resolved.ID, domain.StateInProgress, "")
	h.seedTransition(t, resolved.ID, domain.StateResolved, "")
	h.seedTicket(t, "still pending", nil)

	rec := h.get(t, "/tickets/metrics/export.csv", false)
	if got := rec.Header().Get("Content-Disposition"); !strings.Contains(got, "metrics.csv") {
		t.Errorf("Content-Disposition = %q, want an attachment named metrics.csv", got)
	}
	rows := getCSV(t, rec)
	if len(rows[0]) != 3 {
		t.Fatalf("header = %v, want three columns", rows[0])
	}
	sections := map[string]bool{}
	labels := map[string]string{}
	for _, row := range rows[1:] {
		sections[row[0]] = true
		labels[row[1]] = row[2]
	}
	for _, want := range []string{"Period", "Summary", "SLA attainment (%)"} {
		if !sections[want] {
			t.Errorf("section %q is missing from the export: %v", want, rows)
		}
	}
	if labels["Pending"] != "1" {
		t.Errorf("Pending = %q, want 1", labels["Pending"])
	}
	if labels["Resolved"] != "1" {
		t.Errorf("Resolved = %q, want 1", labels["Resolved"])
	}
}

// S23/S27: filtering the queue swaps only the results and the tab row
// (hx-select + hx-select-oob), so the download link must live INSIDE the tab
// row; anywhere else it would keep the previous filters and download a
// different set than the screen shows.
func TestTicketExportLinkFollowsAnHTMXFilterChange(t *testing.T) {
	h := newHarness(t)
	h.seedTicket(t, "filtered", nil)

	body := h.get(t, "/tickets?state=new&priority=high", true).Body.String()
	start := strings.Index(body, `id="queue-tabs"`)
	if start < 0 {
		t.Fatalf("the HTMX response carries no tab row: %.400s", body)
	}
	tabs := body[start : start+strings.Index(body[start:], "</nav>")]
	if !strings.Contains(tabs, `href="/tickets/export.csv?priority=high&amp;state=new"`) {
		t.Fatalf("the tab row's download link does not carry the new filters: %s", tabs)
	}
}

// S23: the metrics filters swap the whole detail content, so the download
// link refreshed with it must carry the newly chosen period.
func TestMetricsExportLinkFollowsAnHTMXFilterChange(t *testing.T) {
	h := newHarness(t)

	body := h.get(t, "/tickets/metrics?metrics_start=2026-01-05&metrics_end=2026-01-11", true).Body.String()
	if !strings.Contains(body, `class="ticket-metrics-export" href="/tickets/metrics/export.csv?metrics_attainment_group=total&amp;metrics_end=2026-01-11&amp;metrics_group=agent&amp;metrics_start=2026-01-05"`) {
		t.Fatalf("the refreshed download link does not carry the chosen period: %s", body)
	}
}

// The screens that own the data must offer the download (S27).
func TestExportLinksAppearOnTheScreensThatOwnTheData(t *testing.T) {
	h := newHarness(t)
	h.seedTicket(t, "linked", nil)

	if body := h.get(t, "/tickets", false).Body.String(); !strings.Contains(body, `href="/tickets/export.csv`) {
		t.Error("the ticket queue offers no CSV download")
	}
	if body := h.get(t, "/tickets/metrics", false).Body.String(); !strings.Contains(body, `href="/tickets/metrics/export.csv`) {
		t.Error("the metrics dashboard offers no CSV download")
	}
}
