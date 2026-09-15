package httpadapter

import (
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// assertNoDuplicateIDs fails when a document repeats an id attribute.
func assertNoDuplicateIDs(t *testing.T, body string) {
	t.Helper()
	seen := map[string]bool{}
	for _, m := range regexp.MustCompile(`id="([^"]+)"`).FindAllStringSubmatch(body, -1) {
		if seen[m[1]] {
			t.Fatalf("duplicate id %q in document", m[1])
		}
		seen[m[1]] = true
	}
}
func TestMetricsReturnHrefConstrainsToListOrigin(t *testing.T) {
	for _, tc := range [][3]string{
		{"empty", "", "/tickets"},
		{"absolute url", "https://evil.example/tickets?q=x", "/tickets"},
		{"foreign path", "/users?status=deactivated", "/tickets"},
		{"subpath", "/tickets/12", "/tickets"},
		{"recognized kept", "/tickets?state=new&priority=high&category_id=2&user_id=3&q=vpn&page=4", "/tickets?category_id=2&page=4&priority=high&q=vpn&state=new&user_id=3"},
		{"unknown dropped", "/tickets?q=x&evil=1&metrics_start=2026-01-01", "/tickets?q=x"},
		{"invalid dropped", "/tickets?state=garbage&category_id=abc&page=0&q=x", "/tickets?q=x"},
	} {
		if got := metricsReturnHref(tc[1]); got != tc[2] {
			t.Fatalf("%s: metricsReturnHref(%q) = %q, want %q", tc[0], tc[1], got, tc[2])
		}
	}
}

// The dedicated /tickets/metrics page (issue #123 detail slice) on one harness:
// full/HX rendering, validation contracts with HX recovery, safe back/clear/
// hidden-return links, line-card structure with one View data table, and the
// admin/root-only authorization matrix.
func TestTicketMetricsHTTPDetailPage(t *testing.T) {
	h := newHarness(t)
	for _, tc := range [][3]string{
		{"malformed", "metrics_start=bad&metrics_end=2026-03-01", "YYYY-MM-DD"},
		{"only start", "metrics_start=2026-03-01", "choose both metrics dates"},
		{"only end", "metrics_end=2026-03-01", "choose both metrics dates"},
		{"inverted", "metrics_start=2026-03-05&metrics_end=2026-03-01", "metrics end date must not be before the start date"},
	} {
		rec := h.get(t, "/tickets/metrics?"+tc[1], true)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `role="alert"`) || !strings.Contains(rec.Body.String(), tc[2]) || strings.Contains(rec.Body.String(), "ticket-metrics-grid") {
			t.Fatalf("%s: HX validation must show %q with 200 and no stale results, got %d: %s", tc[0], tc[2], rec.Code, rec.Body.String())
		}
	}
	// Non-HX validation keeps the mapped 422 status in the full page shell.
	if shell := h.get(t, "/tickets/metrics?metrics_start=2026-03-01", false); shell.Code != http.StatusUnprocessableEntity || !strings.Contains(shell.Body.String(), `role="alert"`) || !strings.Contains(shell.Body.String(), "choose both metrics dates") {
		t.Fatalf("non-HX validation must return 422 in the full shell: %d %s", shell.Code, shell.Body.String())
	}
	if recovered := h.get(t, "/tickets/metrics?metrics_start=2026-03-01&metrics_end=2026-03-02", true); recovered.Code != http.StatusOK || !strings.Contains(recovered.Body.String(), "ticket-metrics-grid") {
		t.Fatalf("valid HX filter must recover the detail fragment: %d %s", recovered.Code, recovered.Body.String())
	}
	full := h.get(t, "/tickets/metrics?return=%2Ftickets%3Fstate%3Dnew%26q%3Dvpn%26page%3D2%26evil%3D1", false)
	if full.Code != http.StatusOK {
		t.Fatalf("full metrics = %d", full.Code)
	}
	body := full.Body.String()
	for _, want := range []string{
		`id="ticket-metrics-detail-content"`, "Ticket metrics", `href="/tickets?page=2&amp;q=vpn&amp;state=new">Back to tickets`,
		`<input type="hidden" name="return" value="/tickets?page=2&amp;q=vpn&amp;state=new">`,
		`href="/tickets/metrics?return=%2Ftickets%3Fpage%3D2%26q%3Dvpn%26state%3Dnew"`, "Timezone: UTC", "Partial week",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("full page must contain %q: %s", want, body)
		}
	}
	if strings.Contains(body, `id="tickets-screen"`) || strings.Contains(body, "ticket-metrics-cards") {
		t.Fatalf("full page must be the dedicated detail page, not the list: %s", body)
	}
	assertNoDuplicateIDs(t, body)
	// With a ticket inside the default window the line card renders the chart,
	// legend, precomputed totals, and the single native View data table — and
	// none of the later-slice cards or controls.
	h.seedTicket(t, "week ticket", nil)
	card := h.get(t, "/tickets/metrics", false)
	if card.Code != http.StatusOK {
		t.Fatalf("metrics page = %d", card.Code)
	}
	cardBody := card.Body.String()
	for _, want := range []string{
		`<h2>Created vs resolved</h2>`, "Weekly events within the selected period", "metrics-legend-created", "metrics-legend-resolved", "Created 1 · Resolved 0",
		`<details class="ticket-metrics-data">`, ">View data</summary>", "<caption>Weekly ticket totals</caption>", `scope="col">Created`, `scope="col">Resolved`,
		`role="img"`, `class="metrics-line metrics-created"`, `class="metrics-line metrics-resolved"`,
	} {
		if !strings.Contains(cardBody, want) {
			t.Fatalf("line card must contain %q: %s", want, cardBody)
		}
	}
	for _, forbidden := range []string{
		"Age of pending tickets", "Pending workload", "Resolution time distribution",
		`name="metrics_group"`, "metrics-hist-bar", "metrics-bar-unassigned",
	} {
		if strings.Contains(cardBody, forbidden) {
			t.Fatalf("this slice must not render %q", forbidden)
		}
	}
	if got := strings.Count(cardBody, "View data"); got != 1 {
		t.Fatalf("exactly one View data disclosure, got %d", got)
	}
	assertNoDuplicateIDs(t, cardBody)
	if hx := h.get(t, "/tickets/metrics", true); hx.Code != http.StatusOK || strings.Contains(hx.Body.String(), "<html") || !strings.Contains(hx.Body.String(), `id="ticket-metrics-detail-content"`) {
		t.Fatalf("HX metrics must return only the detail fragment: %d %s", hx.Code, hx.Body.String())
	}
	if anonymous := doRequest(h.mux, h.mw, http.MethodGet, "/tickets/metrics", nil); anonymous.Code != http.StatusSeeOther {
		t.Errorf("anonymous status = %d, want the existing login redirect", anonymous.Code)
	}
	for _, role := range []domain.Role{domain.RoleUser, domain.RoleAgent} {
		actor := seedUserRole(t, h.store, string(role), string(role)+"@metrics.test", role)
		session := seedSession(t, h.store, actor.ID)
		for _, hxReq := range []bool{true, false} {
			headers := map[string]string{"Cookie": sessionCookie + "=" + session.ID}
			if hxReq {
				headers["HX-Request"] = "true"
			}
			rec := doRequest(h.mux, h.mw, http.MethodGet, "/tickets/metrics", headers)
			if rec.Code != http.StatusForbidden || strings.Contains(rec.Body.String(), "ticket-metrics-grid") {
				t.Errorf("HX=%v role %q status = %d, want 403 without metrics content", hxReq, role, rec.Code)
			}
		}
	}
}
