package httpadapter

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
	"github.com/giulianotesta7/tkt/internal/i18n"
)

// CSV export (issue #409). Two downloads, each from the screen that already
// owns its data and its filters: the ticket list and the metrics dashboard.
//
// Neither export is a second way to read tickets. The ticket export goes
// through the SAME scoped walk the list uses, and the metrics export through
// the metrics service, which authorizes before it reads anything. A user who
// cannot see a row on screen cannot download it either.

// exportTimeLayout is how an instant is written into a CSV cell. RFC3339 keeps
// the zone explicit, so a spreadsheet in another zone cannot silently reinterpret
// it as local time.
const exportTimeLayout = time.RFC3339

// exportChunkSize is how many rows are buffered before the SLA states of that
// chunk are resolved in one read. It bounds the memory an export holds while
// keeping the number of SLA reads proportional to the result, not to the rows.
const exportChunkSize = 500

// beginCSV starts a CSV download: the status is committed here, BEFORE any row
// is written, because a response that already carries bytes can no longer
// become an error page.
//
// The byte-order mark is what makes Excel read the file as UTF-8; without it
// the accented Spanish text of a ticket title arrives as mojibake (S26).
func beginCSV(w http.ResponseWriter, filename string, header []string) (*csv.Writer, error) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte("\uFEFF")); err != nil {
		return nil, err
	}
	out := csv.NewWriter(w)
	if err := out.Write(header); err != nil {
		return nil, err
	}
	return out, nil
}

// ticketExportColumns are the catalogue keys of the ticket export header, in
// column order. They are the facts the list already works with; comments,
// internal notes and attachments are deliberately absent (S24).
var ticketExportColumns = []string{
	"export.tickets.column.number",
	"export.tickets.column.title",
	"export.tickets.column.state",
	"export.tickets.column.priority",
	"export.tickets.column.category",
	"export.tickets.column.requester",
	"export.tickets.column.assignee",
	"export.tickets.column.created",
	"export.tickets.column.updated",
	"export.tickets.column.sla",
}

// exportTickets downloads every ticket matching the CURRENT list filters, not
// just the visible page (S21). The filters are parsed by the same function the
// list uses, so the file always agrees with the screen it was asked from.
func (h *TicketHandlers) exportTickets(w http.ResponseWriter, r *http.Request) {
	actor := *userFromContext(r.Context())
	f := parseFilters(r)
	// The simplified filter set is part of what those roles SEE: applying it
	// here is what keeps the download equal to their screen.
	if actor.Role == domain.RoleAgent || actor.Role == domain.RoleUser {
		f = simplifiedFilters(f)
	}
	opts, err := h.collectOptions(r)
	if err != nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	query := viewQuery(f, actor)
	if actor.Role == domain.RoleUser {
		// A requester's file is their WHOLE My tickets screen, not one section
		// (issue #425, S1/S8): the held, resolved and closed work the list shows
		// belongs in the download too. The handler names the scope because the
		// server owns it — no query value can pick a section, widen the file to
		// another requester, or narrow it to one state or view.
		query.Section = application.TicketSectionRequesterExport
	}
	names := newExportNames(opts)
	translator := h.metricsTranslator(r)
	header := make([]string, 0, len(ticketExportColumns))
	for _, key := range ticketExportColumns {
		header = append(header, translator.T(key))
	}

	out, err := beginCSV(w, "tickets.csv", header)
	if err != nil {
		return
	}
	// SLA is STAFF-ONLY, exactly as in the list: a requester's export leaves
	// the column empty rather than disclosing a commitment their screen hides.
	staff := actor.Role != domain.RoleUser && h.sla != nil
	chunk := make([]domain.Ticket, 0, exportChunkSize)
	flush := func() error {
		if len(chunk) == 0 {
			return nil
		}
		var slas map[int64]domain.SLAProjection
		if staff {
			var err error
			if slas, err = h.sla.ForTickets(r.Context(), ticketIDs(chunk)); err != nil {
				return err
			}
		}
		for i := range chunk {
			if err := out.Write(ticketExportRow(chunk[i], names, slas, translator)); err != nil {
				return err
			}
		}
		chunk = chunk[:0]
		return nil
	}
	err = h.search.Export(r.Context(), actor, query, func(t domain.Ticket) error {
		chunk = append(chunk, t)
		if len(chunk) < exportChunkSize {
			return nil
		}
		return flush()
	})
	if err == nil {
		err = flush()
	}
	if err != nil {
		// The status and some rows are already on the wire, so this cannot
		// become an error page. Abandoning the stream unflushed is what tells
		// the client the file is incomplete.
		return
	}
	out.Flush()
}

// ticketExportHref is the download URL for the CURRENT list filters. It reuses
// the same value builder the pagination links use, so the file the link asks
// for is exactly the set the screen is showing.
func ticketExportHref(f filterState) string {
	v := ticketFilterValues(f)
	if len(v) == 0 {
		return "/tickets/export.csv"
	}
	return "/tickets/export.csv?" + v.Encode()
}

// metricsExportHref is the download URL for the current metrics filters.
func metricsExportHref(f application.TicketMetricsFilter, groupBy, workloadBy string) string {
	v := url.Values{}
	if !f.Start.IsZero() {
		v.Set("metrics_start", f.Start.UTC().Format("2006-01-02"))
	}
	if !f.End.IsZero() {
		// The stored End is exclusive; the form speaks the inclusive last day.
		v.Set("metrics_end", f.End.UTC().AddDate(0, 0, -1).Format("2006-01-02"))
	}
	if f.DeskID != nil {
		v.Set("metrics_desk_id", strconv.FormatInt(*f.DeskID, 10))
	}
	if f.AgentID != nil {
		v.Set("metrics_agent_id", strconv.FormatInt(*f.AgentID, 10))
	}
	if workloadBy != "" {
		v.Set("metrics_group", workloadBy)
	}
	if groupBy != "" {
		v.Set("metrics_attainment_group", groupBy)
	}
	if len(v) == 0 {
		return "/tickets/metrics/export.csv"
	}
	return "/tickets/metrics/export.csv?" + v.Encode()
}

// exportNames resolves the ids a ticket row carries into the names a reader
// expects, from the option lists the page already loads — the same source the
// list rows use, so a name can never differ between screen and file.
type exportNames struct {
	users      map[int64]string
	categories map[int64]string
}

func newExportNames(opts options) exportNames {
	n := exportNames{users: map[int64]string{}, categories: map[int64]string{}}
	for _, u := range opts.Users {
		n.users[u.ID] = u.Name
	}
	for _, c := range opts.Categories {
		n.categories[c.ID] = c.Name
	}
	return n
}

func (n exportNames) user(id *int64) string {
	if id == nil {
		return ""
	}
	return n.users[*id]
}

func ticketExportRow(t domain.Ticket, names exportNames, slas map[int64]domain.SLAProjection, translator i18n.Translator) []string {
	sla := ""
	if projection, ok := slas[t.ID]; ok && projection.Overall != domain.SLANone {
		sla = translator.T("label.sla." + string(projection.Overall))
	}
	return []string{
		strconv.Itoa(t.Number),
		t.Title,
		translator.T("label.state." + string(t.State)),
		translator.T("label.priority." + string(t.Priority)),
		names.categories[t.CategoryID],
		t.RequesterName,
		names.user(t.UserID),
		formatExportTime(t.CreatedAt),
		formatExportTime(t.UpdatedAt),
		sla,
	}
}

func formatExportTime(at time.Time) string {
	if at.IsZero() {
		return ""
	}
	return at.UTC().Format(exportTimeLayout)
}

// exportMetrics downloads the aggregates the dashboard shows for the selected
// period and filters (S25).
//
// A dashboard is not one table: it is a summary, a weekly series, age buckets,
// workload and SLA attainment. The file therefore uses the long shape a
// spreadsheet pivots easily — section, label, value — instead of pretending
// those sections share columns.
func (h *TicketHandlers) exportMetrics(w http.ResponseWriter, r *http.Request) {
	// metricsData authorizes through the metrics service itself, so a user
	// without the capability never reaches the writer below.
	data, err := h.metricsData(r)
	if err != nil {
		// A download has no page to re-render a refusal into, so the refusal
		// is the response: the same status the dashboard would answer with,
		// as plain text in the instance language.
		status, message := mapErrorFor(r, err)
		http.Error(w, message, status)
		return
	}
	translator := h.metricsTranslator(r)
	out, err := beginCSV(w, "metrics.csv", []string{
		translator.T("export.metrics.column.section"),
		translator.T("export.metrics.column.label"),
		translator.T("export.metrics.column.value"),
	})
	if err != nil {
		return
	}
	for _, row := range metricsExportRows(data.Metrics, translator) {
		if err := out.Write(row); err != nil {
			return
		}
	}
	out.Flush()
}

// metricsExportRows flattens the dashboard into section/label/value rows, in
// the order the screen presents them.
func metricsExportRows(m application.TicketMetrics, translator i18n.Translator) [][]string {
	section := func(key string) string { return translator.T(key) }
	var rows [][]string
	add := func(sectionName, label, value string) {
		rows = append(rows, []string{sectionName, label, value})
	}

	period := section("export.metrics.section.period")
	add(period, translator.T("export.metrics.label.start"), formatExportDate(m.Filter.Start))
	// The stored End is the exclusive next midnight; the screen shows the last
	// day actually covered, so the file reports that same inclusive day.
	add(period, translator.T("export.metrics.label.end"), formatExportDate(m.Filter.End.AddDate(0, 0, -1)))

	summary := section("export.metrics.section.summary")
	for _, entry := range []struct {
		key   string
		value int
	}{
		{"export.metrics.label.pending", m.Pending},
		{"export.metrics.label.unassigned", m.Unassigned},
		{"export.metrics.label.created", m.Created},
		{"export.metrics.label.resolved", m.Resolved},
		{"export.metrics.label.samples", m.Samples},
		{"export.metrics.label.excluded", m.Excluded},
	} {
		add(summary, translator.T(entry.key), strconv.Itoa(entry.value))
	}
	for _, entry := range []struct {
		key      string
		duration time.Duration
	}{
		{"export.metrics.label.mean_resolution", m.MeanDuration},
		{"export.metrics.label.median_resolution", m.Median},
		{"export.metrics.label.p90_resolution", m.P90},
	} {
		add(summary, translator.T(entry.key), formatExportDuration(entry.duration))
	}

	weeks := section("export.metrics.section.weeks")
	for _, w := range m.Weeks {
		label := formatExportDate(w.Start)
		add(weeks, label, strconv.Itoa(w.Resolved))
	}

	ages := section("export.metrics.section.ages")
	for _, b := range m.Ages {
		add(ages, exportBucketLabel(b.Label, b.LabelKey, b.LabelArgs, translator), strconv.Itoa(b.Count))
	}

	histogram := section("export.metrics.section.resolution_times")
	for _, b := range m.Histogram {
		add(histogram, exportBucketLabel(b.Label, b.LabelKey, b.LabelArgs, translator), strconv.Itoa(b.Count))
	}

	workload := section("export.metrics.section.workload")
	for _, wl := range m.Workload {
		add(workload, exportBucketLabel(wl.Label, wl.LabelKey, wl.LabelArgs, translator), strconv.Itoa(wl.Count))
	}

	attainment := section("export.metrics.section.attainment")
	for _, g := range m.Attainment.Groups {
		label := exportBucketLabel(g.Label, g.LabelKey, nil, translator)
		add(attainment, label+" · "+translator.T("export.metrics.label.first_response"), formatExportRate(g.FirstResponse.Rate))
		add(attainment, label+" · "+translator.T("export.metrics.label.resolution"), formatExportRate(g.Resolve.Rate))
	}
	add(attainment, translator.T("export.metrics.label.no_commitment"), strconv.Itoa(m.Attainment.NoCommitment))
	return rows
}

// exportBucketLabel resolves a label the same way the screen does: a chrome
// label names a catalogue entry, while an admin-authored name is data and is
// written verbatim.
func exportBucketLabel(label, key string, args []any, translator i18n.Translator) string {
	if key == "" {
		return label
	}
	if len(args) > 0 {
		return translator.Tf(key, args...)
	}
	return translator.T(key)
}

func formatExportDate(at time.Time) string {
	if at.IsZero() {
		return ""
	}
	return at.UTC().Format("2006-01-02")
}

// formatExportDuration writes a duration as whole hours with one decimal, the
// unit the dashboard itself reports resolution times in.
func formatExportDuration(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	return strconv.FormatFloat(d.Hours(), 'f', 1, 64)
}

// formatExportRate writes an attainment rate as a percentage without a unit
// suffix, so a spreadsheet reads it as a number.
func formatExportRate(rate float64) string {
	return strconv.FormatFloat(rate*100, 'f', 1, 64)
}
