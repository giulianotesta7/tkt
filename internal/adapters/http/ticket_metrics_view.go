package httpadapter

import (
	"fmt"
	"html/template"
	"math"
	"strings"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/i18n"
)

// ticketMetricsSummaryView is the dashboard's derived-figure block (issue #393,
// S3). Every field is computed from application.TicketMetrics — the service
// stays the only data source and no query semantics change (S1) — and the
// template only prints it.
type ticketMetricsSummaryView struct {
	// Net is created minus resolved for the selected period; the append sign
	// is the catalogue sentence's, so a negative period reads as one.
	Net int
	// UnassignedShare is the whole percent of the current pending backlog that
	// carries no agent.
	UnassignedShare int
}

// newTicketMetricsSummary derives the dashboard figures from one service
// result. It never reads another source and never introduces a new rule: both
// figures are arithmetic over the service's own counts.
func newTicketMetricsSummary(m application.TicketMetrics) ticketMetricsSummaryView {
	return ticketMetricsSummaryView{
		Net:             m.Created - m.Resolved,
		UnassignedShare: metricsPercent(m.Unassigned, m.Pending),
	}
}

// metricsPercent answers part as a whole percent of whole; a non-positive
// denominator answers zero so no share is ever a division artifact.
func metricsPercent(part, whole int) int {
	if whole <= 0 {
		return 0
	}
	return int(math.Round(float64(part) * 100 / float64(whole)))
}

// Attainment chart geometry. The chart is a fixed-width viewBox the stylesheet
// scales to its card, like the other metrics SVGs: a label column, a milestone
// tag column, the plot, and room for the rate beside each bar.
const (
	metricsAttainWidth = 620
	metricsAttainLabel = 150
	metricsAttainTag   = 28
	metricsAttainRight = 52
	metricsAttainTop   = 8
	metricsAttainRow   = 46
	metricsAttainBar   = 8
)

// metricsBarSegmentWidths splits one milestone's plot width into its met,
// breached and open shares, normalized to the group's ticket count, so the
// three segments of a row always fill exactly the track the chart drew. A group
// with no tickets draws nothing.
func metricsBarSegmentWidths(plotWidth float64, m ticketMetricsAttainmentMilestoneView, tickets int) (met, breached, open float64) {
	if tickets <= 0 || plotWidth <= 0 {
		return 0, 0, 0
	}
	share := func(count int) float64 {
		if count <= 0 {
			return 0
		}
		return plotWidth * float64(count) / float64(tickets)
	}
	return share(m.Met), share(m.Breached), share(m.Open)
}

// metricsAttainmentChart renders the SLA attainment section's inline SVG
// (issue #393, S4): one row per group, two stacked bars per row — first
// response and resolution — each showing the met, breached and open shares of
// that group's tickets with its rate beside it. The same counts stay in the
// section's table view, and the SVG carries a text alternative listing every
// group, so the graphic is never the only reading.
func metricsAttainmentChart(tr i18n.Translator, a ticketMetricsAttainmentView) template.HTML {
	if len(a.Groups) == 0 {
		return ""
	}
	plotX := float64(metricsAttainLabel + metricsAttainTag)
	plotW := float64(metricsAttainWidth - metricsAttainLabel - metricsAttainTag - metricsAttainRight)
	height := metricsAttainTop + len(a.Groups)*metricsAttainRow + 6
	tagFirst := tr.T("ticket_metrics.attainment.tag_first_response")
	tagResolve := tr.T("ticket_metrics.attainment.tag_resolution")

	summaries := make([]string, 0, len(a.Groups))
	for _, g := range a.Groups {
		summaries = append(summaries, tr.Tf("ticket_metrics.attainment.chart_group",
			bucketLabel(tr, g), g.Tickets, g.FirstResponse.Rate, g.Resolve.Rate))
	}
	aria := tr.Tf("ticket_metrics.attainment.chart_aria", strings.Join(summaries, " "))

	var b strings.Builder
	fmt.Fprintf(&b, `<svg class="ticket-metrics-chart ticket-metrics-attainment" viewBox="0 0 %d %d" role="img" aria-label="%s">`,
		metricsAttainWidth, height, template.HTMLEscapeString(aria))
	for i, g := range a.Groups {
		top := float64(metricsAttainTop + i*metricsAttainRow)
		labelY := top + 12
		fmt.Fprintf(&b, `<text x="0" y="%.1f" class="metrics-chart-label metrics-att-group">%s</text>`, labelY, template.HTMLEscapeString(bucketLabel(tr, g)))
		// The count is a catalogue plural held as two explicit keys: the
		// coverage scan reads Go literals, and a singular row must not read
		// "1 tickets".
		countKey := "ticket_metrics.attainment.chart_count_other"
		if g.Tickets == 1 {
			countKey = "ticket_metrics.attainment.chart_count_one"
		}
		fmt.Fprintf(&b, `<text x="%.1f" y="%.1f" text-anchor="end" class="metrics-att-count">%s</text>`, plotX-8, labelY, template.HTMLEscapeString(tr.Tf(countKey, g.Tickets)))
		rows := []struct {
			tag       string
			milestone ticketMetricsAttainmentMilestoneView
		}{
			{tag: tagFirst, milestone: g.FirstResponse},
			{tag: tagResolve, milestone: g.Resolve},
		}
		for j, row := range rows {
			y := top + 18 + float64(j)*(metricsAttainBar+4)
			fmt.Fprintf(&b, `<text x="%d" y="%.1f" class="metrics-att-tag">%s</text>`, metricsAttainLabel, y+float64(metricsAttainBar)-1, template.HTMLEscapeString(row.tag))
			fmt.Fprintf(&b, `<rect x="%.1f" y="%.1f" width="%.1f" height="%d" rx="4" class="metrics-att-track"/>`, plotX, y, plotW, metricsAttainBar)
			met, breached, open := metricsBarSegmentWidths(plotW, row.milestone, g.Tickets)
			x := plotX
			for _, segment := range []struct {
				class string
				width float64
			}{
				{class: "metrics-att-met", width: met},
				{class: "metrics-att-breached", width: breached},
				{class: "metrics-att-open", width: open},
			} {
				if segment.width <= 0 {
					continue
				}
				fmt.Fprintf(&b, `<rect x="%.1f" y="%.1f" width="%.1f" height="%d" rx="4" class="metrics-att-bar %s"/>`, x, y, segment.width, metricsAttainBar, segment.class)
				x += segment.width
			}
			fmt.Fprintf(&b, `<text x="%.1f" y="%.1f" class="metrics-chart-value metrics-att-rate">%s</text>`, plotX+plotW+8, y+float64(metricsAttainBar)-1, template.HTMLEscapeString(row.milestone.Rate))
		}
	}
	b.WriteString(`</svg>`)
	return template.HTML(b.String())
}
