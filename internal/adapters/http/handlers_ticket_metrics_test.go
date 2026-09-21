package httpadapter

import (
	"bytes"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
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
		{"bad group", "metrics_start=2026-03-01&metrics_end=2026-03-02&metrics_group=desks", "metrics workload group must be agent or desk"},
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
	// legend, precomputed totals, and its View data table.
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
	// The dashboard completes with exactly four panels in order and one native
	// View data disclosure each; no card id may repeat.
	for i, title := range []string{"Created vs resolved", "Age of pending tickets", "Pending workload", "Resolution time distribution", "SLA attainment"} {
		if !strings.Contains(cardBody, ">"+title+"</h2>") {
			t.Fatalf("panel %d must render h2 %q", i, title)
		}
		if after := cardBody[strings.Index(cardBody, ">"+title+"</h2>"):]; strings.Count(after, "</section>") < 5-i-0 {
			t.Fatalf("panel %d (%s) must close before later panels", i, title)
		}
	}
	if got := strings.Count(cardBody, `<section class="ticket-metrics-card ticket-metrics-panel"`); got != 5 {
		t.Fatalf("exactly five dashboard panels, got %d", got)
	}
	if got := strings.Count(cardBody, "View data"); got != 4 {
		t.Fatalf("exactly four View data disclosures, got %d", got)
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

// sectionBody returns the markup of the card <section> whose h2 is title.
func sectionBody(t *testing.T, body, title string) string {
	t.Helper()
	for _, section := range strings.Split(body, "<section") {
		if strings.Contains(section, ">"+title+"</h2>") {
			return section
		}
	}
	t.Fatalf("no card section with h2 %q", title)
	return ""
}

// Empty dataset: each card answers with its distinct scope line and empty
// state, no chart frame renders, no statistics are fabricated, and all four
// View data tables stay reachable with their captions.
func TestTicketMetricsHTTPCardScopesAndEmptyStates(t *testing.T) {
	h := newHarness(t)
	body := h.get(t, "/tickets/metrics", true).Body.String()
	for _, pair := range [][2]string{
		{"Created vs resolved", "Weekly events within the selected period"},
		{"Age of pending tickets", "Current pending workload · Time since creation"},
		{"Pending workload", "Current assignment · 0 pending tickets"},
		{"Resolution time distribution", "Resolved during the selected period"},
	} {
		section := sectionBody(t, body, pair[0])
		if h2 := strings.Index(section, ">"+pair[0]+"</h2>"); h2 < 0 || strings.Index(section, pair[1]) < h2 {
			t.Fatalf("card %q must carry scope %q right after its h2: %s", pair[0], pair[1], section)
		}
	}
	if !strings.Contains(body, "Pending workload: current") {
		t.Fatalf("top note must state the current pending workload scope: %s", body)
	}
	if got := strings.Count(body, "No results for selected period."); got != 2 {
		t.Fatalf("line + resolution cards must show the no-results message, got %d: %s", got, body)
	}
	if got := strings.Count(body, "No current pending tickets."); got != 2 {
		t.Fatalf("age + workload cards must show the current-pending message, got %d: %s", got, body)
	}
	if strings.Contains(body, "<svg") || strings.Contains(body, "90th percentile") {
		t.Fatalf("empty dataset must not render chart frames or fake zero stats: %s", body)
	}
	if got := strings.Count(body, ">View data</summary>"); got != 4 {
		t.Fatalf("every card keeps its View data table, got %d: %s", got, body)
	}
	for _, caption := range []string{
		"Weekly ticket totals",
		"Pending tickets by completed elapsed days",
		"Pending workload by current agent",
		"Resolution duration histogram",
	} {
		if !strings.Contains(body, caption) {
			t.Fatalf("table caption %q must stay reachable: %s", caption, body)
		}
	}
	assertNoDuplicateIDs(t, body)
}

// With pending work and one resolved ticket the age/workload/histogram charts
// render with real data. The grouping radios live INSIDE the workload card,
// stay form-associated with the filter form and include it in their HX
// request; desk grouping re-renders the workload view; the genuine unassigned
// identity keeps its neutral bar; stats show day units above the histogram.
func TestTicketMetricsHTTPPendingChartsAndWorkloadGrouping(t *testing.T) {
	h := newHarness(t)
	agent := h.createUser(t, "Ada <b>&", "agent-escape@metrics.test", "password123")
	h.seedTicket(t, "first pending", nil)
	second := h.seedTicket(t, "second pending", nil)
	h.assignTicket(t, second.ID, agent.ID)
	resolved := h.seedTicket(t, "resolved fast", nil)
	h.seedTransition(t, resolved.ID, domain.StateInProgress, "")
	h.seedTransition(t, resolved.ID, domain.StateResolved, "")
	body := h.get(t, "/tickets/metrics", false).Body.String()
	if strings.Contains(body, "No current pending tickets.") {
		t.Fatalf("pending work must render charts instead of the empty message: %s", body)
	}
	workload := sectionBody(t, body, "Pending workload")
	if !strings.Contains(workload, `class="ticket-metrics-group"`) || !strings.Contains(workload, "By agent") || !strings.Contains(workload, "By desk") {
		t.Fatalf("workload card must contain the grouping control: %s", workload)
	}
	for _, want := range []struct {
		sel  string
		want int
	}{
		{`name="metrics_group"`, 2}, {`name="metrics_attainment_group"`, 3}, {`form="ticket-metrics-filters"`, 5},
		{`hx-include="#ticket-metrics-filters"`, 5}, {`id="ticket-metrics-filters"`, 1},
	} {
		if got := strings.Count(body, want.sel); got != want.want {
			t.Fatalf("expected %d occurrences of %q, got %d: %s", want.want, want.sel, got, body)
		}
	}
	if formStart := strings.Index(body, `<form class="ticket-metrics-filters"`); strings.Contains(body[formStart:strings.Index(body[formStart:], "</form>")+formStart], "metrics_group") {
		t.Fatalf("grouping radios must not remain in the global filter toolbar")
	}
	if !strings.Contains(workload, `class="metrics-bar metrics-bar-unassigned"`) {
		t.Fatalf("genuinely unassigned bar must keep the neutral class: %s", workload)
	}
	if strings.Contains(body, "<b>") || !strings.Contains(body, "Ada &lt;b&gt;&amp;") {
		t.Fatalf("agent label must render escaped in the workload chart: %s", body)
	}
	if !strings.Contains(body, "Mean 0.0 days · Median 0.0 days · 90th percentile 0.0 days") {
		t.Fatalf("stats row must show day units: %s", body)
	}
	if stats, chart := strings.Index(body, "Mean 0.0 days"), strings.Index(body, "metrics-histogram"); stats < 0 || chart < 0 || stats > chart {
		t.Fatalf("stats row must render above the histogram svg: %s", body)
	}
	if !strings.Contains(body, "1 valid · 0 excluded") {
		t.Fatalf("samples/excluded line must stay visible below: %s", body)
	}
	assertNoDuplicateIDs(t, body)

	desk := h.get(t, "/tickets/metrics?metrics_group=desk", false).Body.String()
	if !strings.Contains(desk, "Pending workload by current desk") || !strings.Contains(desk, `form="ticket-metrics-filters" checked`) {
		t.Fatalf("desk grouping must re-render the workload view: %s", desk)
	}
}

// Resolved>0 with zero verifiable durations shows the not-enough-history
// message plus the exclusion count, never fabricated zero statistics. Rendered
// directly as the fragment because the real service cannot produce a resolved
// record without any verifiable duration.
func TestTicketMetricsTemplateNotEnoughHistoryState(t *testing.T) {
	h := newHarness(t)
	data := ticketMetricsDetailData{
		ticketMetricsData: ticketMetricsData{
			Metrics: application.TicketMetrics{WorkloadBy: "agent", Resolved: 2, Excluded: 2},
		},
		BackHref: "/tickets",
	}
	var buf bytes.Buffer
	if err := h.renderer.fragments.ExecuteTemplate(&buf, "ticket_metrics_detail_content", data); err != nil {
		t.Fatalf("render fragment: %v", err)
	}
	body := buf.String()
	if !strings.Contains(body, "Not enough historical data to calculate resolution times.") || !strings.Contains(body, "2 excluded") {
		t.Fatalf("not-enough-history message must carry the exclusion count: %s", body)
	}
	if strings.Contains(body, "Mean ") || strings.Contains(body, "90th percentile") {
		t.Fatalf("must not fabricate zero statistics: %s", body)
	}
	if got := strings.Count(body, "View data"); got != 4 {
		t.Fatalf("View data tables stay reachable in every state, got %d: %s", got, body)
	}
}

// enableMetricsSLA turns SLA on through the real settings route so the create
// path freezes a commitment, using the shared settings form fixture.
func enableMetricsSLA(t *testing.T, h *harness) {
	t.Helper()
	form := slaPanelForm("80")
	form.Set("sla_enabled", "1")
	if rec := h.postForm(t, "/settings/sla", form, false); rec.Code != http.StatusSeeOther {
		t.Fatalf("enable SLA: status = %d, want %d", rec.Code, http.StatusSeeOther)
	}
}

// A ticket created in the period with a frozen commitment makes the SLA
// attainment panel render both milestone blocks with real counts and a visible
// denominator. A freshly created ticket has both milestones OPEN, so the rate
// reads 0% with zero decided — the exact case the denominator exists to keep
// from being misread as total failure.
func TestTicketMetricsHTTPAttainmentPanelCounts(t *testing.T) {
	h := newHarness(t)
	enableMetricsSLA(t, h)
	h.seedTicket(t, "committed", nil)
	section := sectionBody(t, h.get(t, "/tickets/metrics", true).Body.String(), "SLA attainment")
	for _, want := range []string{
		"Tickets created in the selected period that carry a frozen commitment",
		`name="metrics_attainment_group"`,
		"<h3>First response</h3>",
		"<h3>Resolution</h3>",
		"Met 0 · Breached 0 · Open 1",
		"Rate 0% (0 decided)",
		"SLA attainment by Total",
	} {
		if !strings.Contains(section, want) {
			t.Fatalf("attainment panel must contain %q: %s", want, section)
		}
	}
	if strings.Contains(section, "No tickets created in the selected period carry a frozen commitment.") {
		t.Fatalf("a committed cohort must not render the empty state: %s", section)
	}
	if strings.Contains(section, "without a frozen commitment are excluded") {
		t.Fatalf("no commitless created tickets here, so no exclusion note: %s", section)
	}
	if strings.Count(section, "checked") != 1 || !strings.Contains(section, `value="total"`) {
		t.Fatalf("default grouping must select total: %s", section)
	}
}

// With SLA disabled the created ticket freezes nothing, so the period has no
// commitment at all: the panel shows its empty state and the exclusion note,
// and renders NO milestone count or rate — the check is not vacuous because
// both the message and the absence of the count blocks are asserted.
func TestTicketMetricsHTTPAttainmentEmptyState(t *testing.T) {
	h := newHarness(t)
	h.seedTicket(t, "no commitment", nil)
	section := sectionBody(t, h.get(t, "/tickets/metrics", true).Body.String(), "SLA attainment")
	if !strings.Contains(section, "No tickets created in the selected period carry a frozen commitment.") {
		t.Fatalf("empty period must render the attainment empty state: %s", section)
	}
	if !strings.Contains(section, "without a frozen commitment are excluded from these rates (1)") {
		t.Fatalf("empty state must explain the excluded commitless tickets: %s", section)
	}
	for _, absent := range []string{"ticket-metrics-attainment-milestone", "Rate ", "ticket-metrics-attainment-row"} {
		if strings.Contains(section, absent) {
			t.Fatalf("empty period must not render %q: %s", absent, section)
		}
	}
}

// The attainment grouping fails soft like the list sort: an empty or unknown
// value selects total, never an error, and the dashboard still renders.
func TestTicketMetricsHTTPAttainmentGroupingFailsSoft(t *testing.T) {
	h := newHarness(t)
	for _, query := range []string{"", "?metrics_attainment_group=", "?metrics_attainment_group=bogus"} {
		rec := h.get(t, "/tickets/metrics"+query, true)
		if rec.Code != http.StatusOK {
			t.Fatalf("query %q status = %d, want 200", query, rec.Code)
		}
		body := rec.Body.String()
		if strings.Contains(body, `role="alert"`) || !strings.Contains(body, "ticket-metrics-grid") {
			t.Fatalf("query %q must render the dashboard without an alert: %s", query, body)
		}
		section := sectionBody(t, body, "SLA attainment")
		if strings.Count(section, "checked") != 1 || !strings.Contains(section, `value="total"`) {
			t.Fatalf("query %q must select the total grouping: %s", query, section)
		}
	}
}

// Switching the grouping changes the table rows: total is one row, priority is
// always the four canonical groups (empty ones included), category only the
// categories present, ordered by name. Two committed tickets in two categories
// give the category view two rows.
func TestTicketMetricsHTTPAttainmentGroupingRows(t *testing.T) {
	h := newHarness(t)
	enableMetricsSLA(t, h)
	h.seedTicket(t, "bugs committed", nil)
	ops, err := h.categories.Create(t.Context(), "Operations")
	if err != nil {
		t.Fatalf("create category: %v", err)
	}
	h.publishWorkflow(t, ops.ID, simpleManualDef())
	h.seedTicket(t, "ops committed", func(in *application.CreateTicketInput) {
		in.CategoryID = ops.ID
		in.Priority = domain.PriorityHigh
	})

	total := sectionBody(t, h.get(t, "/tickets/metrics", true).Body.String(), "SLA attainment")
	if got := strings.Count(total, "ticket-metrics-attainment-row"); got != 1 {
		t.Fatalf("total grouping must render one row, got %d: %s", got, total)
	}

	priority := sectionBody(t, h.get(t, "/tickets/metrics?metrics_attainment_group=priority", true).Body.String(), "SLA attainment")
	if got := strings.Count(priority, "ticket-metrics-attainment-row"); got != 4 {
		t.Fatalf("priority grouping must render four canonical rows, got %d: %s", got, priority)
	}
	for _, label := range []string{">Critical<", ">High<", ">Medium<", ">Low<"} {
		if !strings.Contains(priority, label) {
			t.Fatalf("priority grouping must render %q: %s", label, priority)
		}
	}
	if strings.Count(priority, "checked") != 1 || !strings.Contains(priority, `value="priority"`) {
		t.Fatalf("priority grouping must be selected: %s", priority)
	}

	category := sectionBody(t, h.get(t, "/tickets/metrics?metrics_attainment_group=category", true).Body.String(), "SLA attainment")
	if got := strings.Count(category, "ticket-metrics-attainment-row"); got != 2 {
		t.Fatalf("category grouping must render the two present categories, got %d: %s", got, category)
	}
	for _, label := range []string{">Bugs<", ">Operations<"} {
		if !strings.Contains(category, label) {
			t.Fatalf("category grouping must render %q: %s", label, category)
		}
	}
}
