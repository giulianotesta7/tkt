package httpadapter

import (
	"flag"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
	"github.com/giulianotesta7/tkt/internal/i18n"
)

// Golden-file harness (D7): frozen fixtures render deterministically (fixed
// clock, fixed order) and are compared against testdata/*.golden. Run with
// -update to regenerate, then rerun WITHOUT -update to prove stability.
var update = flag.Bool("update", false, "update golden files")

// The shell inlines the shared stylesheet into every full page.
const (
	stylesheetOpen  = "<style>"
	stylesheetClose = "</style>"
)

// splitStylesheet separates a rendered full page from the inline stylesheets it
// embeds, in document order.
//
// Freezing those blocks inside each page snapshot stored the shared ~44KB first
// block twelve times: 528,912 of 617,412 golden bytes (86%) were one block
// duplicated, and a 392-line CSS change rewrote all twelve snapshots — 2,412
// lines, 83% of the diff — as twelve near-identical diffs. The shell emits more
// than one inline block (styles.html closes the shared block and opens a
// categories block gated on {{if .CategoryAssets}}), so this collects EVERY
// block concatenated in document order. The stylesheet is still frozen exactly
// once, in stylesheet.golden, and the page snapshot guards the markup: no block
// is duplicated into a page snapshot.
//
// A full page that renders no stylesheet block fails here rather than silently
// comparing a page without one, so dropping {{template "styles" .}} from the
// shell is caught.
func splitStylesheet(t *testing.T, name, got string) (page, stylesheet string) {
	t.Helper()
	var markup strings.Builder
	rest := got
	blocks := 0
	for {
		start := strings.Index(rest, stylesheetOpen)
		if start < 0 {
			markup.WriteString(rest)
			break
		}
		markup.WriteString(rest[:start])
		block := rest[start:]
		end := strings.Index(block, stylesheetClose)
		if end < 0 {
			t.Fatalf("golden %s: a stylesheet block is not terminated", name)
		}
		end += len(stylesheetClose)
		stylesheet += block[:end]
		blocks++
		rest = block[end:]
	}
	if blocks == 0 {
		t.Fatalf("golden %s: a full page must render the shared stylesheet block", name)
	}
	return markup.String(), stylesheet
}

// goldenFile compares got against testdata/<name>.golden; -update writes it.
func goldenFile(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("update golden %s: %v", path, err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v (regenerate with go test -run TestGolden -update)", path, err)
	}
	if string(want) != got {
		t.Errorf("golden mismatch %s\n--- want ---\n%s\n--- got ---\n%s", name, want, got)
	}
}

// goldenFullPage freezes a full page as two parts: the markup in <name>.golden,
// and the stylesheet it embeds, asserted present here and compared once in
// stylesheet.golden by TestGoldenStylesheet.
func goldenFullPage(t *testing.T, name, got string) {
	t.Helper()
	page, stylesheet := splitStylesheet(t, name, got)
	if stylesheet == "" {
		t.Fatalf("golden %s: the stylesheet block is empty", name)
	}
	goldenFile(t, name, page)
}

// TestGoldenStylesheet freezes every inline stylesheet block the shell emits, in
// document order. The categories page renders the shared block plus the
// categories block gated on {{if .CategoryAssets}}, so this golden guards both;
// every full page embeds the shared block, so a change to it changes the whole
// interface and deserves a single reviewable diff rather than twelve.
func TestGoldenStylesheet(t *testing.T) {
	_, stylesheet := splitStylesheet(t, "stylesheet",
		renderGolden(t, "categories_index", "", fixtureCategoriesIndexData(), false))
	goldenFile(t, "stylesheet", stylesheet)
}

// TestGoldenFullPage freezes the full-page render path (shell + content)
// with a frozen fixture timestamp (D7: the render path never calls
// time.Now()).
func TestGoldenFullPage(t *testing.T) {
	r := mustFixtureRenderer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/fixture", nil)

	r.Render(rec, req, "fixture", "", fixtureData{Title: "Login page down", Time: fixtureTime}, http.StatusOK)

	goldenFile(t, "render_full_page", rec.Body.String())
}

// TestGoldenFragment freezes the HX fragment render path.
func TestGoldenFragment(t *testing.T) {
	r := mustFixtureRenderer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/fixture", nil)
	req.Header.Set("HX-Request", "true")

	r.Render(rec, req, "fixture", "fixture_fragment", fixtureData{Title: "Login page down", Time: fixtureTime}, http.StatusOK)

	goldenFile(t, "render_fragment", rec.Body.String())
}

// Auth goldens intentionally have no snapshots in this RED slice. The future
// presentation implementation must provide and approve them without -update.
func TestGoldenAuthSetup(t *testing.T) {
	goldenFullPage(t, "auth_setup", renderGolden(t, "setup", "", setupData{}, false))
}

func TestGoldenAuthLogin(t *testing.T) {
	goldenFullPage(t, "auth_login", renderGolden(t, "login", "", loginData{}, false))
}

// ---------------------------------------------------------------------------
// Task 5.4 goldens: tickets index/new pages + list fragments with frozen
// fixture instants (D7 — the render path never calls time.Now()).
// ---------------------------------------------------------------------------

var (
	goldenT0 = time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC)
	goldenT1 = time.Date(2026, 8, 6, 10, 30, 0, 0, time.UTC)
)

// fixtureListData builds a frozen list payload with two tickets, two
// categories, and one user.
func fixtureListData() listData {
	ana := domain.User{ID: 1, Name: "Ana Torres", Email: "ana@example.com", Active: true, CreatedAt: goldenT0}
	f := filterState{State: domain.StateNew, Sort: sortNewest}
	opts := options{
		States:          listStates,
		Priorities:      listPriorities,
		Categories:      []domain.Category{{ID: 1, Name: "Bugs", CreatedAt: goldenT0}, {ID: 2, Name: "Support", CreatedAt: goldenT0}},
		Users:           []domain.User{ana},
		AssignableUsers: []domain.User{ana},
	}
	tickets := []ticketRow{
		{
			Ticket: domain.Ticket{ID: 2, Number: 2, Title: "Printer jam", State: domain.StateInProgress, Priority: domain.PriorityHigh, CreatedAt: goldenT1, UpdatedAt: goldenT1},
			SLA:    &slaRow{State: domain.SLAAtRisk},
		},
		{Ticket: domain.Ticket{ID: 1, Number: 1, Title: "Login page down", State: domain.StateNew, Priority: domain.PriorityCritical, CreatedAt: goldenT0, UpdatedAt: goldenT0}},
	}
	return listData{
		pageData:            pageData{NavActive: "tickets", CurrentUser: ana},
		Filters:             f,
		Options:             opts,
		Tickets:             tickets,
		Total:               2,
		Page:                1,
		Pages:               1,
		PrevHref:            "",
		NextHref:            "",
		ShowAdvancedFilters: true,
		ShowSLA:             true,
		ClearHref:           "/tickets",
	}
}

// fixtureTicketFormData builds a frozen create-form payload (no error).
func fixtureTicketFormData() ticketFormData {
	ana := domain.User{ID: 1, Name: "Ana Torres", Email: "ana@example.com", Active: true, CreatedAt: goldenT0}
	opts := options{
		States:          listStates,
		Priorities:      listPriorities,
		Users:           []domain.User{ana},
		AssignableUsers: []domain.User{ana},
	}
	selected := &domain.CatalogCategory{
		Category:       domain.Category{ID: 1, Name: "Bugs", CreatedAt: goldenT0},
		DepartmentName: "Technology",
		DeskName:       "Service desk",
	}
	// The picker's offers, grouped one entry per desk (issue #391, S3). The
	// second row is the shape a published category that cannot run renders: the
	// frozen fixture pins it so the golden carries the disabled option and the
	// reason beside a healthy one.
	offers := []catalogCategoryEntry{
		{CatalogCategory: *selected},
		{
			CatalogCategory: domain.CatalogCategory{
				Category:       domain.Category{ID: 2, Name: "Printers", CreatedAt: goldenT0},
				DepartmentName: "Technology",
				DeskName:       "Service desk",
			},
			Unavailable: true,
			Reason: application.MsgRef("categories.badge.cannot_run",
				application.Nested(application.MsgValues("error.workflow_reason.no_members", "Service desk"))),
		},
	}
	return ticketFormData{
		pageData: pageData{NavActive: "tickets", CurrentUser: ana},
		Selected: selected,
		Values: ticketFormValues{
			Title:       "Login page down",
			Description: "The login form 500s on submit",
			CategoryID:  "1",
			Priority:    domain.PriorityHigh,
		},
		Options:        opts,
		CategoryOffers: offers,
		CategoryGroups: groupCatalogCategories(offers),
	}
}

// fixtureTwoStepTicketFormData is the same form as a service-catalog card opens
// it: the category is fixed and Change returns to the catalog (issue #391, S4).
func fixtureTwoStepTicketFormData() ticketFormData {
	data := fixtureTicketFormData()
	data.CategoryFixed = true
	data.CatalogHref = "/tickets/new"
	return data
}

func renderGolden(t *testing.T, page, fragment string, data any, hx bool) string {
	t.Helper()
	r := NewRenderer()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/golden", nil)
	if hx {
		req.Header.Set("HX-Request", "true")
	}
	r.Render(rec, req, page, fragment, data, http.StatusOK)
	if rec.Code != http.StatusOK {
		t.Fatalf("render %s/%s: status %d body %s", page, fragment, rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

// fixtureTicketMetricsData builds a frozen dashboard payload (issue #393): one
// selected period whose edge week is partial, a pending backlog spread over the
// age buckets and the workload rows, resolved tickets with a verifiable
// duration, and a committed cohort. Every derived block is produced by the same
// Go functions the handler uses, so the snapshot pins the real view model
// rather than a hand-copied one.
func fixtureTicketMetricsData() ticketMetricsDetailData {
	start := time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)
	metrics := application.TicketMetrics{
		Filter:       application.TicketMetricsFilter{Start: start, End: end, WorkloadBy: "agent"},
		Pending:      4,
		Unassigned:   1,
		Created:      9,
		Resolved:     6,
		MeanDuration: 42 * time.Hour,
		Median:       30 * time.Hour,
		P90:          120 * time.Hour,
		Samples:      5,
		Excluded:     1,
		WorkloadBy:   "agent",
		Weeks: []application.TicketMetricsWeek{
			{Start: time.Date(2026, 8, 31, 0, 0, 0, 0, time.UTC), Created: 1, Resolved: 0, Partial: true},
			{Start: time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC), Created: 5, Resolved: 3},
			{Start: time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC), Created: 3, Resolved: 3},
		},
		Ages: []application.TicketMetricsBucket{
			{Label: "0–2 days", LabelKey: "ticket_metrics.age.bucket.0_2", Count: 2},
			{Label: "3–7 days", LabelKey: "ticket_metrics.age.bucket.3_7", Count: 1},
			{Label: "8–14 days", LabelKey: "ticket_metrics.age.bucket.8_14", Count: 0},
			{Label: ">14 days", LabelKey: "ticket_metrics.age.bucket.14_plus", Count: 1},
		},
		Workload: []application.TicketMetricsWorkload{
			{Label: "Ana Torres", Count: 2},
			{Label: "Unassigned", LabelKey: "tickets.degrade.unassigned", Count: 1, Unassigned: true},
			{Label: "Zoe Chen", Count: 1},
		},
		Histogram: []application.TicketMetricsBucket{
			{Label: "0–<5 days", LabelKey: "ticket_metrics.resolution.bin", LabelArgs: []any{"0", "5"}, Count: 4, LowerDays: 0, UpperDays: 5},
			{Label: "5–<10 days", LabelKey: "ticket_metrics.resolution.bin", LabelArgs: []any{"5", "10"}, Count: 1, LowerDays: 5, UpperDays: 10},
		},
		Attainment: application.TicketMetricsAttainment{
			GroupBy:      application.TicketMetricsGroupPriority,
			NoCommitment: 1,
			Groups: []application.TicketMetricsAttainmentGroup{
				{Label: "Critical", LabelKey: "label.priority.critical", Tickets: 2, FirstResponse: application.TicketMetricsMilestoneAttainment{Met: 1, Breached: 0, Open: 1}, Resolve: application.TicketMetricsMilestoneAttainment{Met: 1, Breached: 1}},
				{Label: "High", LabelKey: "label.priority.high", Tickets: 3, FirstResponse: application.TicketMetricsMilestoneAttainment{Met: 2, Breached: 1}, Resolve: application.TicketMetricsMilestoneAttainment{Open: 3}},
			},
		},
	}
	view := attainmentViewFor(metrics.Attainment)
	return ticketMetricsDetailData{
		pageData: pageData{NavActive: "metrics", PageFoundationAssets: true, MetricsAssets: true},
		ticketMetricsData: ticketMetricsData{
			Metrics:                  metrics,
			Summary:                  newTicketMetricsSummary(metrics),
			MeanResolution:           metricsDuration(metrics.MeanDuration),
			MeanResolutionIsDuration: true,
			Attainment:               view,
			AttainmentChart:          metricsAttainmentChart(i18n.New(i18n.Default), view),
			ReturnHref:               "/tickets",
			Desks:                    []domain.Desk{{ID: 1, Name: "Service desk", CreatedAt: goldenT0}},
			Users:                    []domain.User{{ID: 1, Name: "Ana Torres", Email: "ana@example.com", Active: true, CreatedAt: goldenT0}},
		},
		BackHref: "/tickets",
	}
}

// TestGoldenTicketMetricsDashboard freezes the redesigned metrics dashboard
// (issue #393): the five KPI cards, each section's Chart/Table switch with both
// views in the document, and the server-rendered attainment SVG. Update, then
// prove without -update.
func TestGoldenTicketMetricsDashboard(t *testing.T) {
	goldenFullPage(t, "ticket_metrics_dashboard", renderGolden(t, "ticket_metrics_detail", "", fixtureTicketMetricsData(), false))
}

func TestGoldenTicketsIndex(t *testing.T) {
	goldenFullPage(t, "tickets_index", renderGolden(t, "tickets_index", "", fixtureListData(), false))
}

func TestGoldenTicketsIndexUser(t *testing.T) {
	data := fixtureListData()
	data.CurrentUser.Role = domain.RoleUser
	data.ShowAdvancedFilters = false
	data.Filters = filterState{Q: "printer"}
	data.Tickets[0].Description = "Clear the jammed tray, then reload paper."
	// The requester's three sections (issue #411): the on-hold ticket waiting
	// for the reply, the open work, and the resolved+closed pair folded at the
	// end. Sections carry their own totals, and the header counts the open work.
	data.Open = ticketListData{Tickets: []ticketRow{data.Tickets[1]}, Total: 1, Page: 1, Pages: 1, HasActiveQuery: true}
	data.Waiting = ticketListData{Tickets: []ticketRow{data.Tickets[0]}, Total: 1, Page: 1, Pages: 1, HasActiveQuery: true}
	data.Closed = ticketListData{Tickets: []ticketRow{
		{Ticket: domain.Ticket{ID: 3, Number: 3, Title: "Resolved printer request", State: domain.StateResolved, Priority: domain.PriorityLow, CreatedAt: goldenT0, UpdatedAt: goldenT0}},
		{Ticket: domain.Ticket{ID: 4, Number: 4, Title: "Closed printer request", State: domain.StateClosed, Priority: domain.PriorityLow, CreatedAt: goldenT0, UpdatedAt: goldenT1}},
	}, Total: 2, Page: 1, Pages: 1, HasActiveQuery: true}
	data.Total = data.Waiting.Total + data.Open.Total
	goldenFullPage(t, "tickets_index_user", renderGolden(t, "tickets_index", "", data, false))
}

func TestGoldenTicketsIndexAgent(t *testing.T) {
	data := fixtureListData()
	data.CurrentUser.Role = domain.RoleAgent
	data.AgentView = true
	data.Filters = filterState{Q: "printer"}
	data.Tickets[0].RequesterName = "Ana Torres"
	data.Tickets[1].RequesterName = "Ana Torres"
	data.Assigned = ticketListData{Tickets: []ticketRow{
		{Ticket: data.Tickets[0].Ticket, Context: application.AgentTicketRowContext{DeskName: "Service desk"}, SLA: data.Tickets[0].SLA},
		{Ticket: data.Tickets[1].Ticket, Context: application.AgentTicketRowContext{CurrentTask: "Restart the printer spooler"}, SLA: data.Tickets[1].SLA},
	}, Total: 2, Page: 1, Pages: 1}
	data.Claimable = ticketListData{Tickets: []ticketRow{{
		Ticket:  domain.Ticket{ID: 3, Number: 3, Title: "Email bounce", RequesterName: "Ana Torres", State: domain.StateNew, Priority: domain.PriorityMedium, CreatedAt: goldenT1, UpdatedAt: goldenT1},
		Context: application.AgentTicketRowContext{DeskName: "Service desk"},
		SLA:     &slaRow{State: domain.SLAOnTrack},
	}}, Total: 1, Page: 1, Pages: 1}
	data.Total = 3
	goldenFullPage(t, "tickets_index_agent", renderGolden(t, "tickets_index", "", data, false))
}

func TestGoldenTicketsNew(t *testing.T) {
	goldenFullPage(t, "tickets_new", renderGolden(t, "tickets_new", "", fixtureTicketFormData(), false))
}

func TestSelectedTicketFormPresentation(t *testing.T) {
	// One page, no fixed category (issue #391, S3): the category is a native
	// select grouped by desk, so the form works with no script at all.
	body := renderGolden(t, "tickets_new", "", fixtureTicketFormData(), false)
	for _, want := range []string{
		`<h1 class="command-title">Create a ticket</h1>`,
		`<span class="prop-label" id="create-category-label">Category</span>`,
		`<select id="category_id" name="category_id" class="prop-select" required`,
		`<optgroup label="Technology › Service desk">`,
		`<option value="1" selected>Bugs</option>`,
		`href="/tickets">Back to queue</a>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the single-page ticket form must contain %q, got: %s", want, body)
		}
	}
	// S5: a published category that cannot run stays visible, disabled, with its
	// reason, and is never a submittable choice.
	if !strings.Contains(body, `<option value="2" disabled>Printers — Can&#39;t run · Service desk has no active members</option>`) {
		t.Errorf("the picker must keep an unrunnable category disabled with its reason, got: %s", pickerWindow(body))
	}
	// Issue #380: the heading and the form's own fields say what to do, so the
	// "Describe your request." subtitle is gone.
	for _, absent := range []string{"Ticket details", "Back to catalog", "Change category", `class="create-change"`, `href="/tickets/new">Change</a>`, "Describe your request."} {
		if strings.Contains(body, absent) {
			t.Errorf("the single-page form must not contain %q, got: %s", absent, body)
		}
	}
}

// pickerWindow returns the markup around the create form's category picker, so a
// failing assertion shows what was rendered instead of only what was expected.
func pickerWindow(body string) string {
	const marker = `id="category_id"`
	i := strings.Index(body, marker)
	if i < 0 {
		return "(category picker not found)"
	}
	end := i + 900
	if end > len(body) {
		end = len(body)
	}
	return body[i:end]
}

// TestTwoStepTicketFormPresentation pins the form a service-catalog card opens
// (issue #391, S4): the same form with the category fixed and Change returning
// to the catalog.
func TestTwoStepTicketFormPresentation(t *testing.T) {
	body := renderGolden(t, "tickets_new", "", fixtureTwoStepTicketFormData(), false)
	for _, want := range []string{
		`<input type="hidden" name="category_id" value="1">`,
		`<span class="prop-label">Category</span>`,
		`<span>Technology / Service desk / Bugs</span>`,
		`<a href="/tickets/new">Change</a>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the two-step ticket form must contain %q, got: %s", want, body)
		}
	}
	// The picker belongs to the other flow: a card already chose the category.
	if strings.Contains(body, `<select id="category_id"`) {
		t.Errorf("the fixed-category form must not render the picker, got: %s", body)
	}
}

func TestSelectedTicketFormDetailLayoutPresentation(t *testing.T) {
	data := fixtureTicketFormData()
	data.CurrentUser.Name = "Current actor name"
	for _, tc := range []struct {
		name     string
		fragment string
		hx       bool
	}{
		{name: "page"},
		{name: "fragment", fragment: "ticket_form", hx: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := renderGolden(t, "tickets_new", tc.fragment, data, tc.hx)
			for _, want := range []string{
				`class="ticket-create-detail"`,
				`class="command-header ticket-create-command-header"`,
				`class="ticket-create-title-label" for="title">Title</label>`,
				`class="title-input"`,
				`class="cards"`,
				`class="conversation"`,
				`class="card ticket-create-description"`,
				`for="description">Description</label>`,
				`class="comment-dropzone ticket-create-dropzone"`,
				`name="attachments" multiple`,
				`data-dropzone-files`,
				`class="evidence" aria-label="Ticket creation properties"`,
				`class="prop-heading">Properties</div>`,
				`class="prop-label">Requester</span>`,
				`>Current actor name</span>`,
				`<span class="prop-label" id="create-category-label">Category</span>`,
				`<span class="prop-label" id="create-priority-label">Priority</span>`,
				`id="priority" name="priority" class="prop-select"`,
				`<option value="high" selected>High</option>`,
			} {
				if !strings.Contains(body, want) {
					t.Errorf("the ticket creation layout must contain %q, got: %s", want, body)
				}
			}
			// Issue #391, S2: the empty Timeline card and the Assignment section are
			// gone — a ticket has no activity yet and creation assigns nobody.
			for _, absent := range []string{
				`ticket-create-preview`, `ticket-create-layout`,
				`ticket-create-preview-priority`, `Add comment`, `name="body"`,
				`name="requester_`, `name="user_id"`, `name="assignee_id"`,
				`name="to"`, `Move to`, `class="prop-heading">State`,
				`class="card ticket-create-timeline"`, `class="prop-heading">Assignment</div>`,
			} {
				if strings.Contains(body, absent) {
					t.Errorf("the ticket creation layout must not render %q, got: %s", absent, body)
				}
			}
		})
	}

	invalid := fixtureTicketFormData()
	invalid.Values.Priority = domain.Priority("urgent")
	body := renderGolden(t, "tickets_new", "ticket_form", invalid, true)
	if !strings.Contains(body, `<option value="critical">Critical</option>`) {
		t.Errorf("an invalid posted priority must retain the native Critical fallback option, got: %s", body)
	}

	for _, role := range []domain.Role{domain.RoleUser, domain.RoleAgent, domain.RoleAdmin, domain.RoleRoot} {
		data := fixtureTicketFormData()
		data.CurrentUser = domain.User{Name: string(role) + " requester", Role: role}
		body := renderGolden(t, "tickets_new", "ticket_form", data, true)
		if !strings.Contains(body, data.CurrentUser.Name) {
			t.Errorf("%s creation rail must show the authenticated requester, got: %s", role, body)
		}
		for _, absent := range []string{`name="requester_`, `name="user_id"`, `name="assignee_id"`} {
			if strings.Contains(body, absent) {
				t.Errorf("%s creation rail must not render %q, got: %s", role, absent, body)
			}
		}
	}
}

func TestGoldenTicketList(t *testing.T) {
	goldenFile(t, "ticket_list", renderGolden(t, "tickets_index", "ticket_list", fixtureListData(), true))
}

func TestGoldenTicketForm(t *testing.T) {
	goldenFile(t, "ticket_form", renderGolden(t, "tickets_new", "ticket_form", fixtureTicketFormData(), true))
}

func TestGoldenFilterForm(t *testing.T) {
	goldenFile(t, "filter_form", renderGolden(t, "tickets_index", "filter_form", fixtureListData(), true))
}

func TestGoldenPagination(t *testing.T) {
	data := fixtureListData()
	data.Total = 25
	data.Page = 2
	data.Pages = 3
	data.PrevHref = "/tickets?page=1"
	data.NextHref = "/tickets?page=3"
	goldenFile(t, "pagination", renderGolden(t, "tickets_index", "pagination", data, true))
}

func TestGoldenStateBadge(t *testing.T) {
	goldenFile(t, "state_badge", renderGolden(t, "tickets_index", "state_badge", domain.StateResolved, true))
}

// ---------------------------------------------------------------------------
// Task 5.5 goldens: detail/edit pages + fragments with frozen fixtures.
// ---------------------------------------------------------------------------

func strptr(s string) *string { return &s }

// fixtureSLAPanel builds the deterministic frozen projection the detail
// goldens render (issue #211): the first response was MET at goldenT1 and the
// resolution is still pending, AT RISK at the literal projection instant
// 2026-08-07T06:00Z, so the panel exercises an achieved milestone (no
// remaining label), a pending one ("in 4h 0m"), and the dash for a nil
// achieved instant. Literal instants only — the render path never calls
// time.Now() (D7).
func fixtureSLAPanel() *slaPanelView {
	frozen := &domain.TicketSLA{
		FirstResponseSeconds: 14400,
		ResolveSeconds:       86400,
		WarnFirstResponseAt:  goldenT0.Add(3*time.Hour + 12*time.Minute),
		DueFirstResponseAt:   goldenT0.Add(4 * time.Hour),
		WarnResolveAt:        goldenT0.Add(19*time.Hour + 12*time.Minute),
		DueResolveAt:         goldenT0.Add(24 * time.Hour),
		StartedAt:            goldenT0,
		PolicySnapshotAt:     goldenT0,
	}
	achieved := goldenT1
	now := goldenT0.Add(20 * time.Hour)
	return slaPanelFor(i18n.English, domain.ProjectSLA(frozen, domain.SLAMilestones{FirstResponseAt: &achieved}, now))
}

func fixtureDetailData() detailData {
	ana := domain.User{ID: 1, Name: "Ana Torres", Email: "ana@example.com", Active: true, CreatedAt: goldenT0}
	t := &domain.Ticket{
		ID: 1, Number: 1, Title: "Login page down",
		Description:    "The login form 500s on submit",
		RequesterName:  "Ana Torres",
		RequesterEmail: "ana@example.com",
		CategoryID:     1,
		Priority:       domain.PriorityHigh,
		State:          domain.StateInProgress,
		CreatedAt:      goldenT0,
		UpdatedAt:      goldenT1,
	}
	view := &application.TicketView{
		Ticket:       t,
		Category:     &domain.Category{ID: 1, Name: "Bugs", CreatedAt: goldenT0},
		AssignedUser: &ana,
		Comments: []domain.Comment{
			{ID: 1, TicketID: 1, Author: "Ana Torres", Body: "Checking now", CreatedAt: goldenT1},
		},
		AuditEvents: []domain.AuditEvent{
			{TicketID: 1, Actor: "Admin", Action: domain.ActionCreated, CreatedAt: goldenT0},
			{TicketID: 1, Actor: "Admin", Action: domain.ActionTransition, Field: strptr("state"), FromValue: strptr("new"), ToValue: strptr("in_progress"), CreatedAt: goldenT1},
			{TicketID: 1, Actor: "Admin", Action: domain.ActionUpdate, Field: strptr("user"), FromValue: strptr(""), ToValue: strptr("1"), CreatedAt: goldenT1},
		},
	}
	view.Timeline = []application.TimelineItem{
		{IsComment: true, Comment: &view.Comments[0]},
		{Event: &view.AuditEvents[2], Summary: "assigned the ticket to Ana Torres", ActorLabel: "Admin", StateClass: ""},
		{Event: &view.AuditEvents[1], Summary: "moved the ticket to in progress", ActorLabel: "Admin", StateClass: "in_progress"},
		{Event: &view.AuditEvents[0], Summary: "created the ticket", ActorLabel: "Admin", StateClass: "new"},
	}
	opts := options{
		States:          listStates,
		Priorities:      listPriorities,
		Categories:      []domain.Category{*view.Category},
		Users:           []domain.User{ana},
		AssignableUsers: []domain.User{ana},
	}
	return detailData{
		pageData:           pageData{NavActive: "tickets", CurrentUser: ana, SLACountdownAssets: true},
		View:               view,
		Next:               allowedNext(t.State),
		Options:            opts,
		CanCommentInternal: true,
		CanEdit:            true,
		CanAssign:          true,
		CanTransition:      true,
		CanComment:         true, // open ticket: mirrors detailDataFor's !closed
		Values: ticketFormValues{
			Title:       t.Title,
			Description: t.Description,
			CategoryID:  "1",
			UserID:      "1",
			Priority:    t.Priority,
		},
		SLA: fixtureSLAPanel(),
	}
}

// closedDetailData derives a closed-state detail fixture from the open
// fixture: same view/comments, but the ticket is in a closed (read-only)
// state so the detail must hide every mutation control.
func closedDetailData(state domain.State) detailData {
	d := fixtureDetailData()
	now := goldenT0
	d.View.Ticket.State = state
	d.View.Ticket.ResolvedAt = nil
	d.View.Ticket.ClosedAt = nil
	switch state {
	case domain.StateResolved:
		d.View.Ticket.ResolvedAt = &now
	case domain.StateClosed:
		d.View.Ticket.ResolvedAt = &now
		d.View.Ticket.ClosedAt = &now
	}
	d.Next = allowedNext(state)
	d.Closed = true
	d.CanEdit = false
	d.CanAssign = false
	// A closed ticket's only remaining mutation is the reopen transition, and
	// the capability (CapEditTicket) is not gated on the closed state, so a
	// capable actor keeps the reopen action here.
	d.CanTransition = true
	d.CanComment = false // closed ticket: no comment form (issue #55 requester carve-out handled in detailDataFor, not this read-only fixture)
	return d
}

// TestClosedTicketDetailReadOnly proves a closed ticket (resolved/closed/
// cancelled) renders read-only: no comment form, no inline title edit (plain
// <h1>), no priority/assignment pickers — but keeps the read-only metadata
// (Requester, Assignee, Category) and the reopen action. Only resolved/closed
// offer a reopen; cancelled is terminal (closed-ticket read-only spec).
func TestClosedTicketDetailReadOnly(t *testing.T) {
	for _, state := range []domain.State{domain.StateResolved, domain.StateClosed, domain.StateCancelled} {
		t.Run(string(state), func(t *testing.T) {
			body := renderGolden(t, "tickets_show", "ticket_detail", closedDetailData(state), true)

			// Read-only metadata stays visible.
			for _, want := range []string{
				`class="detail-properties"`,
				`class="prop-label">Requester</`,
				`class="prop-label">Category</`,
				`class="prop-label">Assignee</`,
				`>Ana Torres</span>`, // the last assignee name (read-only)
				`<h1 class="command-title">`,
			} {
				if !strings.Contains(body, want) {
					t.Errorf("closed detail must contain %q, got: %s", want, body)
				}
			}

			// Every mutation control is hidden.
			for _, absent := range []string{
				`id="ticket-title"`,      // inline title edit
				`name="title"`,           // title input
				`id="ticket-priority"`,   // priority picker
				`name="priority"`,        // priority form
				`id="assign-user"`,       // assignment picker
				`name="user_id"`,         // assign form
				`class="listbox-button"`, // either picker's opener
				`Add comment`,            // comment form
				`name="body"`,            // comment textarea
				`name="internal"`,        // internal-note switch
			} {
				if strings.Contains(body, absent) {
					t.Errorf("closed detail must NOT contain %q, got: %s", absent, body)
				}
			}

			// Reopen only for resolved/closed, none for cancelled.
			if state == domain.StateCancelled {
				if strings.Contains(body, `data-transition-open="in_progress"`) {
					t.Errorf("cancelled detail must not render a reopen action, got: %s", body)
				}
				if strings.Contains(body, `>Reopen</button>`) {
					t.Errorf("cancelled detail must not render Reopen, got: %s", body)
				}
			} else {
				if !strings.Contains(body, `data-transition-open="in_progress"`) {
					t.Errorf("%s detail must render the reopen action, got: %s", state, body)
				}
				if !strings.Contains(body, `>Reopen</button>`) {
					t.Errorf("%s detail must render Reopen, got: %s", state, body)
				}
			}
		})
	}
}

func TestGoldenTicketsShow(t *testing.T) {
	goldenFullPage(t, "tickets_show", renderGolden(t, "tickets_show", "", fixtureDetailData(), false))
}

func TestGoldenTicketDetail(t *testing.T) {
	goldenFile(t, "ticket_detail", renderGolden(t, "tickets_show", "ticket_detail", fixtureDetailData(), true))
}

func TestGoldenCommentForm(t *testing.T) {
	goldenFile(t, "comment_form", renderGolden(t, "tickets_show", "comment_form", fixtureDetailData(), true))
}

func TestGoldenTimeline(t *testing.T) {
	goldenFile(t, "timeline", renderGolden(t, "tickets_show", "timeline", fixtureDetailData(), true))
}

// ---------------------------------------------------------------------------
// Task 4.3 goldens: requester resolution confirmation (issue #55). The detail
// fixture is a requester-NULL in_progress ticket, so the confirmation panel
// and the resolved comment carve-out stay out of every pre-existing golden.
// ---------------------------------------------------------------------------

// requesterResolvedDetailData builds a requester-owned resolved detail
// fixture: the requester (Beto, role user) owns the ticket, the ticket is
// resolved, and the viewer is a parameter. Mirrors the 4.1/4.2 harness
// shapes (raw-SQL requester pin, real service transitions).
func requesterResolvedDetailData(viewer domain.User) detailData {
	ana := domain.User{ID: 1, Name: "Ana Torres", Email: "ana@example.com", Active: true, CreatedAt: goldenT0}
	beto := domain.User{ID: 2, Name: "Beto Ruiz", Email: "beto@example.com", Active: true, Role: domain.RoleUser, CreatedAt: goldenT0}

	resolvedAt := goldenT1
	t := &domain.Ticket{
		ID: 1, Number: 1, Title: "Login page down",
		Description:     "The login form 500s on submit",
		RequesterName:   beto.Name,
		RequesterEmail:  beto.Email,
		RequesterUserID: &beto.ID,
		UserID:          &ana.ID,
		CategoryID:      1,
		Priority:        domain.PriorityHigh,
		State:           domain.StateResolved,
		ResolvedAt:      &resolvedAt,
		CreatedAt:       goldenT0,
		UpdatedAt:       goldenT1,
	}
	view := &application.TicketView{
		Ticket:       t,
		Category:     &domain.Category{ID: 1, Name: "Bugs", CreatedAt: goldenT0},
		AssignedUser: &ana,
		Comments: []domain.Comment{
			{ID: 1, TicketID: 1, Author: "Ana Torres", Body: "Patched and deployed", CreatedAt: goldenT1},
		},
		AuditEvents: []domain.AuditEvent{
			{TicketID: 1, Actor: beto.Name, Action: domain.ActionCreated, CreatedAt: goldenT0},
			{TicketID: 1, Actor: "Admin", Action: domain.ActionTransition, Field: strptr("state"), FromValue: strptr("new"), ToValue: strptr("in_progress"), CreatedAt: goldenT1},
			{TicketID: 1, Actor: "Admin", Action: domain.ActionTransition, Field: strptr("state"), FromValue: strptr("in_progress"), ToValue: strptr("resolved"), CreatedAt: goldenT1},
		},
	}
	view.Timeline = []application.TimelineItem{
		{IsComment: true, Comment: &view.Comments[0]},
		{Event: &view.AuditEvents[2], ActionLabel: "Transition", FieldLabel: "State", FromLabel: "In Progress", ToLabel: "Resolved"},
		{Event: &view.AuditEvents[1], ActionLabel: "Transition", FieldLabel: "State", FromLabel: "New", ToLabel: "In Progress"},
		{Event: &view.AuditEvents[0], ActionLabel: "Created"},
	}
	requester := isRequester(viewer, t)
	return detailData{
		pageData: pageData{NavActive: "tickets", CurrentUser: viewer},
		View:     view,
		// Mirrors detailDataFor: a role-user viewer is the requester audience
		// (issue #334), so the state badge and back link use their vocabulary.
		RequesterView: viewer.Role == domain.RoleUser,
		Next:          filteredNext(allowedNext(t.State), t),
		Options: options{
			States:          listStates,
			Priorities:      listPriorities,
			Categories:      []domain.Category{*view.Category},
			Users:           []domain.User{ana},
			AssignableUsers: []domain.User{ana},
		},
		CanCommentInternal: viewer.Role != domain.RoleUser,
		Closed:             true,
		// Mirrors detailDataFor: assign needs CapAssignTicket on an open
		// ticket, so a resolved (closed) ticket is read-only for every viewer.
		CanAssign: false,
		// Mirrors detailDataFor: the transition capability is not gated on the
		// closed state, so only a capable viewer keeps the reopen control.
		CanTransition: viewer.Role.AtLeast(domain.RoleAgent),
		CanConfirm:    t.State == domain.StateResolved && requester,
		CanComment:    !domain.IsClosed(t.State) || (t.State == domain.StateResolved && requester),
	}
}

// TestGoldenTicketDetailRequesterResolvedRequesterView freezes the requester's
// view of their own resolved ticket (golden case a): the green confirmation
// panel renders BEFORE the comment form, the comment form is visible (resolved
// carve-out), and no Move-to closed exists for anyone (D7 filtering).
func TestGoldenTicketDetailRequesterResolvedRequesterView(t *testing.T) {
	viewer := domain.User{ID: 2, Role: domain.RoleUser}
	d := requesterResolvedDetailData(viewer)
	body := renderGolden(t, "tickets_show", "ticket_detail", d, true)

	// Confirmation panel content and ordering: it must appear in the
	// conversation column before the comment form.
	for _, want := range []string{
		`class="card resolution-confirmation"`,
		`Resolution confirmation`,
		`Is your issue solved?`,
		`Support marked this ticket as resolved. Confirm the result or return it to the team if you still need help.`,
		`action="/tickets/1/confirmation"`,
		`hx-post="/tickets/1/confirmation"`,
		`name="decision" value="confirm"`,
		`name="decision" value="reject"`,
		`Yes, close ticket`,
		`No, I still need help`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("requester resolved view must contain %q, got: %s", want, body)
		}
	}
	if idx := strings.Index(body, `class="card resolution-confirmation"`); idx >= 0 {
		if form := strings.Index(body, `action="/tickets/1/comments"`); form >= 0 && form < idx {
			t.Errorf("confirmation panel must render BEFORE the comment form\n%s", body)
		}
	}

	// Comment form present (resolved carve-out for the requester).
	for _, want := range []string{`action="/tickets/1/comments"`, `>Reply</button>`} {
		if !strings.Contains(body, want) {
			t.Errorf("requester comment form must render on own resolved ticket, missing %q, got: %s", want, body)
		}
	}

	// No close target for anyone (D7 call-site filter): the requester-owned
	// resolved ticket offers no header transition at all to this actor, and the
	// confirmation panel above the composer is their real path.
	if strings.Contains(body, `name="to" value="closed"`) {
		t.Errorf("requester-owned resolved must not offer a close transition\n%s", body)
	}
	if strings.Contains(body, `action="/tickets/1/transition"`) {
		t.Errorf("a requester owns no transition route action\n%s", body)
	}

	goldenFile(t, "ticket_detail_requester_resolved", body)
}

// TestGoldenTicketDetailRequesterResolvedAgentView freezes the agent's view of
// the same requester-owned resolved ticket (golden case b): reopen only, no
// comment form, and no confirmation panel.
func TestGoldenTicketDetailRequesterResolvedAgentView(t *testing.T) {
	agent := domain.User{ID: 1, Role: domain.RoleAgent}
	d := requesterResolvedDetailData(agent)
	body := renderGolden(t, "tickets_show", "ticket_detail", d, true)

	for _, absent := range []string{
		`class="card resolution-confirmation"`,
		`Is your issue solved?`,
		`/tickets/1/confirmation`,
		`action="/tickets/1/comments"`, // no comment form for non-requesters
		`name="to" value="closed"`,     // closure gate presentation filter
	} {
		if strings.Contains(body, absent) {
			t.Errorf("agent view of requester-owned resolved must NOT contain %q\n%s", absent, body)
		}
	}

	// Reopen stays available to the agent holding the capability (state-machine
	// delta: agent reopen keeps the pin).
	for _, want := range []string{`data-transition-open="in_progress"`, `>Reopen</button>`} {
		if !strings.Contains(body, want) {
			t.Errorf("agent view must keep the reopen target %q\n%s", want, body)
		}
	}

	goldenFile(t, "ticket_detail_agent_resolved", body)
}

// TestGoldenTicketDetailLegacyResolvedAgentView freezes the requester-NULL
// resolved ticket viewed by an agent (golden case c): agent close + reopen
// via Move-to, no confirmation panel, no comment form.
func TestGoldenTicketDetailLegacyResolvedAgentView(t *testing.T) {
	agent := domain.User{ID: 1, Role: domain.RoleAgent}
	d := requesterResolvedDetailData(agent)
	d.View.Ticket.RequesterUserID = nil
	d.Next = allowedNext(d.View.Ticket.State)
	d.CanConfirm = false
	d.CanComment = false
	body := renderGolden(t, "tickets_show", "ticket_detail", d, true)

	for _, absent := range []string{
		`class="card resolution-confirmation"`,
		`/tickets/1/confirmation`,
		`action="/tickets/1/comments"`,
	} {
		if strings.Contains(body, absent) {
			t.Errorf("requester-NULL resolved must NOT contain %q\n%s", absent, body)
		}
	}
	for _, want := range []string{
		`name="to" value="closed"`,
		`data-transition-open="in_progress"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("requester-NULL resolved agent view must offer %q\n%s", want, body)
		}
	}

	goldenFile(t, "ticket_detail_legacy_resolved", body)
}

func TestTicketDetailPresentationContract(t *testing.T) {
	staff := fixtureDetailData()
	staff.CanCommentInternal = true
	staff.CanManageDesks = true
	staff.View.Comments = append(staff.View.Comments, domain.Comment{
		ID:         2,
		TicketID:   staff.View.Ticket.ID,
		Author:     "Operator",
		Body:       "Staff-only note",
		Visibility: domain.CommentInternal,
		CreatedAt:  goldenT0,
	})
	staff.View.Timeline = append([]application.TimelineItem{{
		IsComment: true,
		Comment:   &staff.View.Comments[1],
	}}, staff.View.Timeline...)

	body := renderGolden(t, "tickets_show", "", staff, false)
	for _, want := range []string{
		`class="prop-list"`,
		`class="detail-properties"`,
		`class="prop-heading">Details</`,
		`class="prop-heading">SLA</`,
		`id="ticket-title"`,
		`name="title"`,
		`id="ticket-priority"`,
		`name="priority"`,
		`id="assign-user"`,
		`name="user_id"`,
		`action="/tickets/1/transition"`,
		`name="to"`,
		`id="comment-body"`,
		`name="internal" value="1"`,
		`Internal note`,
		`class="timeline-entry timeline-comment internal"`,
		`--internal-comment-bg`,
		`id="ticket-actions-overflow"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("ticket detail must contain %q, got: %s", want, body)
		}
	}
	if strings.Contains(body, `<details open id="details"`) {
		t.Errorf("variant A must not render the collapsible details cards, got: %s", body)
	}
	if strings.Contains(body, `tkt:ticket-detail:collapsed:v1`) {
		t.Errorf("variant A must not include the localStorage collapse script, got: %s", body)
	}
	if strings.Contains(body, `id="comment-visibility"`) {
		t.Errorf("staff comment form must not render the visibility select, got: %s", body)
	}

	user := fixtureDetailData()
	user.CurrentUser.Role = domain.RoleUser
	user.CanCommentInternal = false
	userBody := renderGolden(t, "tickets_show", "", user, false)
	if !strings.Contains(userBody, `name="visibility" value="public"`) {
		t.Errorf("user comment form must submit public visibility, got: %s", userBody)
	}
	if strings.Contains(userBody, `name="internal"`) || strings.Contains(userBody, `Internal comment`) {
		t.Errorf("user comment form must not render internal controls, got: %s", userBody)
	}
}

func TestLoginPresentationContract(t *testing.T) {
	body := renderGolden(t, "login", "", loginData{}, false)
	if strings.Contains(body, "Use your work email and password.") {
		t.Errorf("login must omit obsolete helper copy, got: %s", body)
	}
	for _, want := range []string{`action="/login"`, `name="email"`, `name="password"`} {
		if !strings.Contains(body, want) {
			t.Errorf("login must retain %q, got: %s", want, body)
		}
	}
}

// ---------------------------------------------------------------------------
// Task 5.6 goldens: users/categories pages + forms.
// ---------------------------------------------------------------------------

func fixtureUsersIndexData() usersIndexData {
	ana := domain.User{ID: 1, Name: "Ana Torres", Email: "ana@example.com", Active: true, CreatedAt: goldenT0}
	beto := domain.User{ID: 2, Name: "Beto Ruiz", Email: "beto@example.com", Active: false, CreatedAt: goldenT1}
	return usersIndexData{pageData: pageData{NavActive: "users", CurrentUser: ana}, Users: []domain.User{ana, beto}}
}

func fixtureUserFormData() userFormData {
	ana := domain.User{ID: 1, Name: "Ana Torres", Email: "ana@example.com", Active: true, CreatedAt: goldenT0}
	return userFormData{
		pageData: pageData{NavActive: "users", CurrentUser: ana},
		UserID:   1,
		Values:   userFormValues{Name: "Ana Torres", Email: "ana@example.com", Active: true},
	}
}

func fixtureCategoriesIndexData() categoriesIndexData {
	ana := domain.User{ID: 1, Name: "Ana Torres", Email: "ana@example.com", Active: true, CreatedAt: goldenT0}
	// The catalog tree (issue #387): the frozen page has to see every level, its
	// per-row health and both native filters' chrome, so the fixture supplies real
	// departments, desks and categories instead of a flat list.
	technology := domain.CatalogDepartment{
		Department: domain.Department{ID: 1, Name: "Technology", Description: "Engineering and platform", CreatedAt: goldenT0},
	}
	support := domain.CatalogDepartment{
		Department: domain.Department{ID: 2, Name: "Support", Description: "Customer support", CreatedAt: goldenT0},
	}
	serviceDesk := domain.CatalogDesk{
		Desk:          domain.Desk{ID: 1, Name: "Service desk", Description: "First-line triage", CreatedAt: goldenT0},
		DepartmentID:  1,
		CategoryCount: 2,
		AgentCount:    3,
		OpenTickets:   4,
	}
	platform := domain.CatalogDesk{
		Desk:         domain.Desk{ID: 2, Name: "Platform", Description: "Infrastructure", CreatedAt: goldenT0},
		DepartmentID: 1,
	}
	helpdesk := domain.CatalogDesk{
		Desk:         domain.Desk{ID: 3, Name: "Helpdesk", Description: "Walk-up support", CreatedAt: goldenT0},
		DepartmentID: 2,
		OpenTickets:  1,
	}
	bugs := domain.Category{ID: 1, Name: "Bugs", Description: "Defects and regressions", DeskID: 1, CreatedAt: goldenT0}
	supportCat := domain.Category{ID: 2, Name: "Support", Description: "Customer questions", DeskID: 1, CreatedAt: goldenT1}
	data := categoriesIndexData{
		pageData:             pageData{NavActive: "categories", CurrentUser: ana},
		SelectedDepartmentID: 1,
		SelectedDeskID:       1,
		SearchClearURL:       "/categories",
		AttentionURL:         "/categories?attention=1",
		Tree: buildCatalogTree(catalogTreeInput{
			Locale:      "en",
			Departments: []domain.CatalogDepartment{technology, support},
			Desks:       []domain.CatalogDesk{serviceDesk, platform, helpdesk},
			Categories:  []domain.Category{bugs, supportCat},
			States: map[int64]catalogCategoryState{
				1: {Badge: application.MsgRef("categories.badge.published", application.Text("1")), Tone: "ok", Published: true},
				2: {Badge: application.MsgRef("categories.badge.draft_only"), Tone: "neutral"},
			},
			OpenTickets:  map[int64]int{1: 3, 2: 1},
			SLADefaults:  map[int64]bool{1: true},
			SLAEnabled:   true,
			DepartmentID: 1,
			DeskID:       1,
		}),
	}
	// The real categories index sets both flags (handlers_categories.go), which
	// is what makes the {{if .CategoryAssets}} block part of the frozen page.
	data.CategoryAssets, data.PageFoundationAssets = true, true
	return data
}

func fixtureCategoryFormData() categoryFormData {
	ana := domain.User{ID: 1, Name: "Ana Torres", Email: "ana@example.com", Active: true, CreatedAt: goldenT0}
	// The real category form is the drawer rendered on the categories index,
	// which sets both flags; the standalone categories_new page mirrors it.
	data := categoryFormData{
		pageData:   pageData{NavActive: "categories", CurrentUser: ana},
		CategoryID: 1,
		Name:       "Bugs",
	}
	data.CategoryAssets, data.PageFoundationAssets = true, true
	return data
}

func TestGoldenUsersIndex(t *testing.T) {
	goldenFullPage(t, "users_index", renderGolden(t, "users_index", "", fixtureUsersIndexData(), false))
}

func TestGoldenUsersNew(t *testing.T) {
	goldenFullPage(t, "users_new", renderGolden(t, "users_new", "", fixtureUserFormData(), false))
}

func TestGoldenUserForm(t *testing.T) {
	goldenFile(t, "user_form", renderGolden(t, "users_new", "user_form", fixtureUserFormData(), true))
}

func TestGoldenCategoriesIndex(t *testing.T) {
	goldenFullPage(t, "categories_index", renderGolden(t, "categories_index", "", fixtureCategoriesIndexData(), false))
}

func TestGoldenCategoriesNew(t *testing.T) {
	goldenFullPage(t, "categories_new", renderGolden(t, "categories_new", "", fixtureCategoryFormData(), false))
}

func TestGoldenCategoryForm(t *testing.T) {
	goldenFile(t, "category_form", renderGolden(t, "categories_new", "category_form", fixtureCategoryFormData(), true))
}

// The settings section fixtures model an admin shell on each section, so they
// carry both management grants: every section is reachable through
// CapManageSettings, and the shell still renders the Users rail link through
// CapManageUsers. Each fixture carries the sub-navigation the handler supplies,
// with the Email entry present because a root is the only actor who sees it —
// except the Email fixture itself, which is root-shaped.
func fixtureSettingsPage() pageData {
	ana := domain.User{ID: 1, Name: "Ana Torres", Email: "ana@example.com", Active: true, CreatedAt: goldenT0}
	return pageData{
		NavActive:         "settings",
		CurrentUser:       ana,
		CanManageUsers:    true,
		CanManageSettings: true,
		SettingsAssets:    true,
		Brand:             goldenBrand(),
		InternalCommentBg: application.DefaultInternalCommentBg,
	}
}

// goldenBrand is the instance identity the frozen shell renders: the name and
// help desk name a default instance answers, with the monogram the shell marks
// it by. It carries no logo, so the golden pins the monogram path.
func goldenBrand() brandData { return brandOf(defaultOrganization()) }

// fixtureSettingsSectionData is the shared settings payload for one section.
func fixtureSettingsSectionData(section string) settingsSectionData {
	return settingsSectionData{
		pageData: fixtureSettingsPage(),
		Section:  section,
		Nav:      settingsNavItems(section, true),
	}
}

func fixtureSettingsOrganizationData() settingsOrganizationData {
	data := settingsOrganizationData{
		settingsSectionData: fixtureSettingsSectionData(settingsSectionOrganization),
		Name:                "Acme Support",
		HelpDesk:            "Service desk",
		SupportEmail:        "support@acme.test",
		Brand:               goldenBrand(),
	}
	return data
}

func fixtureSettingsLanguageData() settingsLanguageData {
	return settingsLanguageData{
		settingsSectionData: fixtureSettingsSectionData(settingsSectionLanguage),
		CurrentLanguage:     application.DefaultInterfaceLanguage,
		Languages:           interfaceLanguageOptions(),
	}
}

// fixtureSettingsTicketsData carries the seeded SLA configuration (migration
// 0013): off, 80 percent, and the seeded default matrix. The automatic hold
// close (issue #353) is OFF on a seeded instance, and the panel renders that
// as an EMPTY field ("empty = off"), never a 0.
func fixtureSettingsTicketsData() settingsTicketsData {
	return settingsTicketsData{
		settingsSectionData: fixtureSettingsSectionData(settingsSectionTickets),
		Current:             application.DefaultInternalCommentBg,
		Colors:              appearanceOptions(),
		SLAGrid:             slaGridData{Rows: slaPolicyRows(seededSLADefaults())},
		SLAWarningPercent:   80,
		HoldAutoCloseDays:   "",
	}
}

// fixtureSettingsCalendarData carries the seeded working calendar (migration
// 0016): Monday-Friday, 09:00-18:00, UTC.
func fixtureSettingsCalendarData() settingsCalendarData {
	calendar := domain.DefaultSLACalendar()
	return settingsCalendarData{
		settingsSectionData: fixtureSettingsSectionData(settingsSectionCalendar),
		CalendarDays:        calendarDayOptions(calendar.WorkingDays),
		CalendarStart:       formatClockMinute(calendar.StartMinute),
		CalendarEnd:         formatClockMinute(calendar.EndMinute),
		CalendarTimezone:    calendar.Location.String(),
	}
}

// fixtureSettingsSigninData models the Sign-in page section with a customized
// panel, so the golden pins the form, the closed-set pickers and the live
// preview markup a script mirrors.
func fixtureSettingsSigninData() settingsSigninData {
	panel := domain.SigninPanel{
		Title:    "Sistemas, a un pedido",
		Subtitle: "Cargá tu pedido y seguí su estado.",
		Content:  domain.SigninContentHighlights,
		Highlights: []domain.SigninHighlight{
			{Icon: domain.SigninIconClock, Title: "Primera respuesta en 4 h", Text: "Los críticos en menos de 30 min."},
			{Icon: domain.SigninIconPhone, Title: "Guardia 24/7", Text: "Interno 4400 fuera de horario."},
		},
		NoticeEnabled: true,
		NoticeKind:    domain.SigninNoticeWarning,
		NoticeTitle:   "Mantenimiento programado",
		NoticeText:    "El sábado de 10 a 12.",
		Background:    domain.SigninBackgroundBrand,
		BrandColor:    "#C8102E",
		SupportPhone:  "Interno 4400",
		SupportHours:  "Lun a vie, 8 a 18",
	}
	values := signinFormValuesFromConfiguration(panel)
	data := settingsSigninData{
		settingsSectionData: fixtureSettingsSectionData(settingsSectionSignin),
		Values:              values,
		Panel:               signinPanelDataFor(application.SigninConfiguration{Panel: panel}, goldenBrand(), "en"),
		HasImage:            true,
		ImageVersion:        "0123456789ab",
	}
	data.Contents, data.Backgrounds, data.NoticeKinds, data.Icons = panelChoices(values)
	return data
}

func TestGoldenSettingsSignin(t *testing.T) {
	goldenFullPage(t, "settings_signin", renderGolden(t, "settings_signin", "", fixtureSettingsSigninData(), false))
}

func fixtureSettingsEmailData() settingsEmailData {
	return settingsEmailData{
		settingsSectionData: fixtureSettingsSectionData(settingsSectionEmail),
		Email: emailPanelFromStored(application.EmailSettings{
			Enabled:     true,
			Host:        "smtp.acme.test",
			Port:        587,
			Encryption:  application.EmailEncryptionSTARTTLS,
			Username:    "notifications",
			PasswordSet: true,
			FromAddress: "tkt@acme.test",
			FromName:    "Acme Support",
			BaseURL:     "https://tkt.acme.test",
		}),
	}
}

// seededSLADefaults is the migration 0013 default matrix, frozen as literals
// so the golden never depends on a store read.
func seededSLADefaults() []domain.SLAPolicy {
	return []domain.SLAPolicy{
		{Priority: domain.PriorityCritical, FirstResponseSeconds: 1800, ResolveSeconds: 14400},
		{Priority: domain.PriorityHigh, FirstResponseSeconds: 3600, ResolveSeconds: 28800},
		{Priority: domain.PriorityMedium, FirstResponseSeconds: 14400, ResolveSeconds: 86400},
		{Priority: domain.PriorityLow, FirstResponseSeconds: 28800, ResolveSeconds: 259200},
	}
}

// The settings surface is one section per URL (issue #383, S1/S2), so it has
// one golden per section instead of the single page snapshot the old surface
// had: a change to the shared layout shows up once per page, and a change to one
// section shows up in that section's snapshot alone.
func TestGoldenSettingsOrganization(t *testing.T) {
	goldenFullPage(t, "settings_organization", renderGolden(t, "settings_organization", "", fixtureSettingsOrganizationData(), false))
}

func TestGoldenSettingsLanguage(t *testing.T) {
	goldenFullPage(t, "settings_language", renderGolden(t, "settings_language", "", fixtureSettingsLanguageData(), false))
}

func TestGoldenSettingsTickets(t *testing.T) {
	goldenFullPage(t, "settings_tickets", renderGolden(t, "settings_tickets", "", fixtureSettingsTicketsData(), false))
}

func TestGoldenSettingsCalendar(t *testing.T) {
	goldenFullPage(t, "settings_calendar", renderGolden(t, "settings_calendar", "", fixtureSettingsCalendarData(), false))
}

func TestGoldenSettingsEmail(t *testing.T) {
	goldenFullPage(t, "settings_email", renderGolden(t, "settings_email", "", fixtureSettingsEmailData(), false))
}

// fixtureCategorySLAData models an admin shell on the category SLA screen
// with a frozen matrix (custom targets, not the seeded defaults) so the
// golden pins the h/m/s decomposition and the divergence marker: critical
// matches the seeded default, the other three diverge from it.
func fixtureCategorySLAData() categorySLAData {
	ana := domain.User{ID: 1, Name: "Ana Torres", Email: "ana@example.com", Active: true, CreatedAt: goldenT0}
	data := categorySLAData{
		pageData:     pageData{NavActive: "categories", CurrentUser: ana, CanManageCategories: true},
		CategoryID:   1,
		CategoryName: "Bugs",
		Grid: slaGridData{Rows: slaPolicyRowsDiverging([]domain.SLAPolicy{
			{Priority: domain.PriorityCritical, FirstResponseSeconds: 1800, ResolveSeconds: 14400},
			{Priority: domain.PriorityHigh, FirstResponseSeconds: 5400, ResolveSeconds: 28800},
			{Priority: domain.PriorityMedium, FirstResponseSeconds: 9000, ResolveSeconds: 57600},
			{Priority: domain.PriorityLow, FirstResponseSeconds: 16200, ResolveSeconds: 172800},
		}, seededSLADefaults())},
	}
	// The real category SLA render sets only PageFoundationAssets
	// (handlers_category_sla.go), unlike the categories index: the SLA page
	// borrows no {{if .CategoryAssets}} rule, so it must not claim that block.
	data.PageFoundationAssets = true
	return data
}

func TestGoldenCategorySLA(t *testing.T) {
	goldenFullPage(t, "category_sla", renderGolden(t, "category_sla", "", fixtureCategorySLAData(), false))
}

// TestGoldenCategoryWorkflow freezes the builder page, which until this
// snapshot was pinned only by E2E plus CSS/string assertions. It follows the
// page-golden convention in this file: a deterministic fixture render through
// renderGolden, no database needed, and goldenFullPage strips the embedded
// stylesheets so the snapshot guards markup alone.
func TestGoldenCategoryWorkflow(t *testing.T) {
	goldenFullPage(t, "category_workflow", renderGolden(t, "category_workflow", "", mobileBuilderPageData(), false))
}

// ---------------------------------------------------------------------------
// Notification center goldens (issue #344, slice B4; specs S14/S15).
// ---------------------------------------------------------------------------

// fixtureNotificationsPanelData freezes the notification panel. It covers every
// kind the vocabulary can store, both actor forms (a named person, "Automatic"
// for a NULL actor, and the degrade label for an actor row that is gone), one
// unread and one read row per group, a row whose ticket is outside the reader's
// reach, and the three day-group shapes. The messages are built by the SAME
// function the handler uses, so the snapshot freezes the catalogue copy AND the
// kind→sentence mapping (S15). Every instant is literal (D7), so a day header
// never depends on the wall clock.
func fixtureNotificationsPanelData() notificationsPanelData {
	ana := domain.User{ID: 1, Name: "Ana Torres", Email: "ana@example.com", Active: true, CreatedAt: goldenT0}
	newState, inProgress := string(domain.StateNew), string(domain.StateInProgress)
	high, medium := string(domain.PriorityHigh), string(domain.PriorityMedium)
	message := func(kind domain.NotificationKind, from, to *string) application.Msg {
		return application.NotificationMessage(domain.Notification{Kind: kind, FromValue: from, ToValue: to})
	}
	assigned := message(domain.NotificationAssigned, nil, nil)
	stateChanged := message(domain.NotificationStateChanged, &newState, &inProgress)
	priorityChanged := message(domain.NotificationPriorityChanged, &medium, &high)
	awaiting := message(domain.NotificationResolvedAwaitingConfirmation, &inProgress, nil)
	confirmed := message(domain.NotificationResolutionConfirmed, nil, nil)
	rejected := message(domain.NotificationResolutionRejected, nil, nil)
	commentPublic := message(domain.NotificationCommentPublic, nil, nil)
	commentInternal := message(domain.NotificationCommentInternal, nil, nil)
	formWaiting := message(domain.NotificationFormWaiting, nil, nil)
	onHold := message(domain.NotificationOnHold, nil, nil)
	holdReminder := message(domain.NotificationHoldReminder, nil, nil)
	holdClosed := message(domain.NotificationHoldClosed, nil, nil)
	automatic := application.MsgRef("notifications.actor.automatic")
	unknown := application.MsgRef("notifications.actor.unknown")
	ana2 := application.Data("Ana Torres")
	marco := application.Data("Marco Ruiz")
	return notificationsPanelData{
		pageData: pageData{NavActive: "notifications", CurrentUser: ana},
		Unread:   7,
		Groups: []notificationGroup{
			{Kind: "today", Date: goldenT0, Rows: []notificationRow{
				// Issue #353: the reminder and the close are automatic, so both
				// carry the "Automatic" actor and their own sentence.
				{ID: 12, TicketID: 12, Number: 15, Title: "Monitor flickers on the dock", Message: holdReminder, Actor: automatic, CreatedAt: goldenT1, Unread: true},
				{ID: 11, TicketID: 11, Number: 14, Title: "Headset microphone is mute", Message: holdClosed, Actor: automatic, CreatedAt: goldenT1, Unread: true},
				{ID: 10, TicketID: 10, Number: 13, Title: "Laptop will not boot", Message: onHold, Actor: marco, CreatedAt: goldenT1, Unread: false},
				{ID: 9, TicketID: 9, Number: 12, Title: "VPN drops every hour", Message: assigned, Actor: automatic, CreatedAt: goldenT1, Unread: true},
				{ID: 8, TicketID: 8, Number: 11, Title: "Invoice export misses August", Message: stateChanged, Actor: marco, CreatedAt: goldenT0, Unread: true},
				{ID: 7, TicketID: 7, Number: 10, Title: "Printer jam on the third floor", Message: commentPublic, Actor: ana2, CreatedAt: goldenT0, Unread: false},
			}},
			{Kind: "yesterday", Date: goldenT0.AddDate(0, 0, -1), Rows: []notificationRow{
				{ID: 6, TicketID: 6, Number: 9, Title: "Onboarding checklist is out of date", Message: awaiting, Actor: marco, CreatedAt: goldenT0.AddDate(0, 0, -1), Unread: true},
				{ID: 5, TicketID: 5, Number: 8, Title: "Badge reader rejects new cards", Message: confirmed, Actor: unknown, CreatedAt: goldenT0.AddDate(0, 0, -1), Unread: false},
				{ID: 4, TicketID: 4, Number: 7, Title: "Laptop battery swells", Message: priorityChanged, Actor: ana2, CreatedAt: goldenT0.AddDate(0, 0, -1), Unread: true},
			}},
			{Kind: "date", Date: goldenT0.AddDate(0, 0, -9), Rows: []notificationRow{
				{ID: 3, TicketID: 3, Number: 6, Title: "Mailbox quota warnings", Message: commentInternal, Actor: marco, CreatedAt: goldenT0.AddDate(0, 0, -9), Unread: true},
				{ID: 2, TicketID: 2, Number: 5, Title: "Software request needs approval", Message: formWaiting, Actor: automatic, CreatedAt: goldenT0.AddDate(0, 0, -9), Unread: false},
				{ID: 1, TicketID: 1, Number: 0, Title: "", Message: rejected, Actor: ana2, CreatedAt: goldenT0.AddDate(0, 0, -9), Unread: false},
			}},
		},
	}
}

// TestGoldenNotifications freezes the panel as a page (GET /notifications).
func TestGoldenNotifications(t *testing.T) {
	goldenFullPage(t, "notifications", renderGolden(t, "notifications", "", fixtureNotificationsPanelData(), false))
}

// TestGoldenNotificationPanel freezes the drawer fragment the bell swaps in: the
// dialog chrome the fragment owns, without a second document around it.
func TestGoldenNotificationPanel(t *testing.T) {
	goldenFile(t, "notification_panel", renderGolden(t, "notifications", "notification_panel", fixtureNotificationsPanelData(), true))
}

// TestGoldenNotificationBell freezes the bell's count fragment: the zero state
// (no badge at all) and the counted state with its accessible name.
func TestGoldenNotificationBell(t *testing.T) {
	goldenFile(t, "notification_bell_zero", renderGolden(t, "notifications", "notification_bell", notificationBell{}, true))
	goldenFile(t, "notification_bell_unread", renderGolden(t, "notifications", "notification_bell", notificationBell{Unread: 3}, true))
}

// ---------------------------------------------------------------------------
// Issue #352 goldens: the ticket on hold. The staff and requester detail carry
// a banner, the requester reads the pause reason, and the resolution milestone
// renders as paused instead of ticking.
// ---------------------------------------------------------------------------

// fixturePausedSLAPanel projects the same frozen commitment the open detail
// fixture uses, with the resolution milestone PAUSED at the literal pause
// instant 2026-08-06T12:00Z (issue #352). The first response stays achieved, so
// the golden also proves the pause leaves it untouched.
func fixturePausedSLAPanel() *slaPanelView {
	frozen := &domain.TicketSLA{
		FirstResponseSeconds: 14400,
		ResolveSeconds:       86400,
		WarnFirstResponseAt:  goldenT0.Add(3*time.Hour + 12*time.Minute),
		DueFirstResponseAt:   goldenT0.Add(4 * time.Hour),
		WarnResolveAt:        goldenT0.Add(19*time.Hour + 12*time.Minute),
		DueResolveAt:         goldenT0.Add(24 * time.Hour),
		StartedAt:            goldenT0,
		PolicySnapshotAt:     goldenT0,
	}
	achieved := goldenT1
	pausedAt := goldenT0.Add(2 * time.Hour)
	now := goldenT0.Add(20 * time.Hour)
	return slaPanelFor(i18n.English, domain.ProjectSLA(frozen, domain.SLAMilestones{
		FirstResponseAt: &achieved,
		ResolvePausedAt: &pausedAt,
	}, now))
}

// fixtureOnHoldStaffDetailData derives the staff on-hold detail from the open
// fixture: the ticket waits for the requester, its resolution milestone is
// paused, and the timeline narrates the hold exactly as enrichTimeline does.
func fixtureOnHoldStaffDetailData() detailData {
	d := fixtureDetailData()
	held := goldenT1
	d.View.Ticket.OnHoldSince = &held
	d.View.Ticket.OnHoldReason = "waiting for the VPN logs"
	holdEvent := domain.AuditEvent{
		TicketID: 1, Actor: "Admin", Action: domain.ActionHold,
		Field: strptr("on_hold"), Note: strptr("waiting for the VPN logs"), CreatedAt: goldenT1,
	}
	d.View.AuditEvents = append(d.View.AuditEvents, holdEvent)
	d.View.Timeline = append([]application.TimelineItem{{
		Event:        &d.View.AuditEvents[len(d.View.AuditEvents)-1],
		ActorLabel:   "Admin",
		Narrative:    "put the ticket on hold: waiting for the VPN logs",
		NarrativeMsg: application.MsgRef("tickets.timeline.narrative.on_hold", application.Text("waiting for the VPN logs")),
		Summary:      "Put on hold",
		// Mirrors enrichTimeline: the narrative carries the reason, so the
		// generic "Reason:" detail line is suppressed.
		SuppressDetail: true,
	}}, d.View.Timeline...)
	d.SLA = fixturePausedSLAPanel()
	return d
}

// fixtureRequesterOnHoldDetailData is the requester's own on-hold ticket
// (issue #352 S8): the banner says the ticket waits for their reply and shows
// the reason, and no staff control is offered.
func fixtureRequesterOnHoldDetailData() detailData {
	ana := domain.User{ID: 1, Name: "Ana Torres", Email: "ana@example.com", Active: true, Role: domain.RoleAgent, CreatedAt: goldenT0}
	beto := domain.User{ID: 2, Name: "Beto Ruiz", Email: "beto@example.com", Active: true, Role: domain.RoleUser, CreatedAt: goldenT0}
	held := goldenT1
	t := &domain.Ticket{
		ID: 1, Number: 1, Title: "Login page down",
		Description:     "The login form 500s on submit",
		RequesterName:   beto.Name,
		RequesterEmail:  beto.Email,
		RequesterUserID: &beto.ID,
		UserID:          &ana.ID,
		CategoryID:      1,
		Priority:        domain.PriorityHigh,
		State:           domain.StateInProgress,
		OnHoldSince:     &held,
		OnHoldReason:    "send us a photo of the jammed tray",
		CreatedAt:       goldenT0,
		UpdatedAt:       goldenT1,
	}
	view := &application.TicketView{
		Ticket:       t,
		Category:     &domain.Category{ID: 1, Name: "Bugs", CreatedAt: goldenT0},
		AssignedUser: &ana,
		Comments: []domain.Comment{
			{ID: 1, TicketID: 1, Author: "Ana Torres", Body: "Can you send a photo?", CreatedAt: goldenT0},
		},
		AuditEvents: []domain.AuditEvent{
			{TicketID: 1, Actor: beto.Name, Action: domain.ActionCreated, CreatedAt: goldenT0},
			{TicketID: 1, Actor: "Admin", Action: domain.ActionHold, Field: strptr("on_hold"), Note: strptr("send us a photo of the jammed tray"), CreatedAt: goldenT1},
		},
	}
	view.Timeline = []application.TimelineItem{
		{
			Event:          &view.AuditEvents[1],
			ActorLabel:     "Admin",
			Narrative:      "put the ticket on hold: send us a photo of the jammed tray",
			NarrativeMsg:   application.MsgRef("tickets.timeline.narrative.on_hold", application.Text("send us a photo of the jammed tray")),
			Summary:        "Put on hold",
			SuppressDetail: true,
		},
		{IsComment: true, Comment: &view.Comments[0]},
		{Event: &view.AuditEvents[0], ActorLabel: beto.Name, Narrative: "created the ticket", NarrativeMsg: application.MsgRef("tickets.timeline.narrative.created"), Summary: "Ticket created"},
	}
	return detailData{
		pageData: pageData{NavActive: "tickets", CurrentUser: beto},
		View:     view,
		// Mirrors detailDataFor for a role-user viewer: requester vocabulary,
		// no hold/resume capability, and the comment form stays available.
		RequesterView:      true,
		Next:               filteredNext(allowedNext(t.State), t),
		Options:            options{States: listStates, Priorities: listPriorities, Categories: []domain.Category{*view.Category}},
		CanCommentInternal: false,
		CanEdit:            false,
		CanAssign:          false,
		CanTransition:      false,
		CanComment:         true,
		Values:             ticketFormValues{Title: t.Title, Description: t.Description, CategoryID: "1", Priority: t.Priority},
	}
}

// TestGoldenTicketDetailOnHold freezes the staff detail of a held ticket: the
// banner names the pause instant and the reason, Resume replaces the hold form,
// the state badge carries the on-hold chip, and the paused resolution milestone
// renders as a chip with no live countdown.
func TestGoldenTicketDetailOnHold(t *testing.T) {
	d := fixtureOnHoldStaffDetailData()
	body := renderGolden(t, "tickets_show", "ticket_detail", d, true)

	for _, want := range []string{
		`class="sla-hold-status"`,
		`On hold since`,
		`datetime="` + formatDatetime(goldenT1) + `"`,
		// The pause instant renders as the LOCALIZED date inside that <time>.
		`>Aug 6, 2026</time>`,
		`waiting for the VPN logs`,
		`action="/tickets/1/resume"`,
		`hx-post="/tickets/1/resume"`,
		`>Resume</button>`,
		`<span class="chip">On hold</span>`,
		`<span class="chip">Paused</span>`,
		`put the ticket on hold: waiting for the VPN logs`,
		// The pause moves the hold controls into the header overflow (S1) and
		// leaves the SLA panel's content in place.
		`id="ticket-actions-overflow"`,
		// First response unchanged: still achieved and labelled.
		`<span class="sla-dot met" aria-hidden="true"></span>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("staff on-hold detail must contain %q, got: %s", want, body)
		}
	}
	for _, absent := range []string{
		`id="hold-reason"`,
		`action="/tickets/1/hold"`,
		// The paused milestone holds a frozen remainder: no live countdown.
		`<time datetime="2026-08-07T10:00:00Z" data-sla-countdown`,
	} {
		if strings.Contains(body, absent) {
			t.Errorf("staff on-hold detail must NOT contain %q, got: %s", absent, body)
		}
	}

	goldenFile(t, "ticket_detail_on_hold", body)
}

// TestGoldenTicketDetailRequesterOnHold freezes the requester's view of their
// own held ticket: the waiting banner with the reason, and no hold or resume
// control anywhere.
func TestGoldenTicketDetailRequesterOnHold(t *testing.T) {
	d := fixtureRequesterOnHoldDetailData()
	body := renderGolden(t, "tickets_show", "ticket_detail", d, true)

	for _, want := range []string{
		`class="hold-banner"`,
		`This ticket is waiting for your reply`,
		`send us a photo of the jammed tray`,
		`<span class="chip">On hold</span>`,
		`put the ticket on hold: send us a photo of the jammed tray`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("requester on-hold detail must contain %q, got: %s", want, body)
		}
	}
	for _, absent := range []string{
		`action="/tickets/1/hold"`,
		`action="/tickets/1/resume"`,
		`id="hold-reason"`,
		`Put on hold`,
		// The requester never receives the SLA projection.
		`class="prop-heading">SLA`,
	} {
		if strings.Contains(body, absent) {
			t.Errorf("requester on-hold detail must NOT contain %q, got: %s", absent, body)
		}
	}

	goldenFile(t, "ticket_detail_requester_on_hold", body)
}

// ---------------------------------------------------------------------------
// Issue #353 goldens: the automatic close of an expired hold. The write stores
// three audit events under the automatic actor (resume, resolve, close), so the
// snapshot proves all three render with NO actor text — the automatic-actor
// convention, never a "System" label — and that the closure says why it closed
// instead of the generic "moved the ticket to closed".
// ---------------------------------------------------------------------------

// fixtureAutomaticHoldCloseDetailData derives the closed detail from the open
// fixture and replaces its timeline with the three events of one hold-expiry
// write (issue #353, S5), as enrichTimeline shapes them in production.
func fixtureAutomaticHoldCloseDetailData() detailData {
	d := closedDetailData(domain.StateClosed)
	write := goldenT0.Add(26 * time.Hour)
	resume := domain.AuditEvent{TicketID: 1, Actor: domain.AutomaticActorSystem, Action: domain.ActionResume, CreatedAt: write}
	resolved := domain.AuditEvent{TicketID: 1, Actor: domain.AutomaticActorSystem, Action: domain.ActionTransition, Field: strptr("state"), FromValue: strptr("in_progress"), ToValue: strptr("resolved"), CreatedAt: write}
	via := domain.ClosureViaHoldExpired
	closed := domain.AuditEvent{TicketID: 1, Actor: domain.AutomaticActorSystem, Action: domain.ActionTransition, Field: strptr("state"), FromValue: strptr("resolved"), ToValue: strptr("closed"), ClosureVia: &via, CreatedAt: write}
	d.View.AuditEvents = []domain.AuditEvent{d.View.AuditEvents[0], resume, resolved, closed}
	// Mirrors enrichTimeline: an automatic actor keeps NO actor label, and the
	// closure carries the hold-expired narrative.
	d.View.Timeline = []application.TimelineItem{
		{IsComment: true, Comment: &d.View.Comments[0]},
		{
			Event:        &d.View.AuditEvents[3],
			Narrative:    "closed the ticket because the requester did not reply",
			NarrativeMsg: application.MsgRef("tickets.timeline.narrative.closed_hold_expired"),
			Summary:      "Ticket closed",
			StateClass:   string(domain.StateClosed),
		},
		{
			Event:        &d.View.AuditEvents[2],
			Narrative:    "moved the ticket to resolved",
			NarrativeMsg: application.MsgRef("tickets.timeline.narrative.moved_to", application.Copy("tickets.timeline.state.resolved")),
			Summary:      "Ticket resolved",
			StateClass:   string(domain.StateResolved),
		},
		{
			Event:        &d.View.AuditEvents[1],
			Narrative:    "took the ticket off hold",
			NarrativeMsg: application.MsgRef("tickets.timeline.narrative.resumed"),
			Summary:      "Resumed",
		},
		{
			Event:        &d.View.AuditEvents[0],
			ActorLabel:   "Admin",
			Narrative:    "created the ticket",
			NarrativeMsg: application.MsgRef("tickets.timeline.narrative.created"),
			Summary:      "Ticket created",
			StateClass:   string(domain.StateNew),
		},
	}
	return d
}

// TestGoldenTicketDetailAutomaticHoldClose freezes the timeline of an
// automatically closed hold: the reason the ticket closed, the three automatic
// events with no actor text, and the human event that keeps its name.
func TestGoldenTicketDetailAutomaticHoldClose(t *testing.T) {
	d := fixtureAutomaticHoldCloseDetailData()
	body := renderGolden(t, "tickets_show", "ticket_detail", d, true)

	for _, want := range []string{
		`<span class="timeline-action">closed the ticket because the requester did not reply</span>`,
		`<span class="timeline-action">moved the ticket to resolved</span>`,
		`<span class="timeline-action">took the ticket off hold</span>`,
		// The one human event of the fixture still names who acted.
		`<strong class="timeline-actor">Admin</strong> <span class="timeline-action">created the ticket</span>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("automatic hold close timeline must contain %q, got: %s", want, body)
		}
	}
	for _, absent := range []string{
		`<strong class="timeline-actor">system</strong>`,
		`<strong class="timeline-actor">System</strong>`,
	} {
		if strings.Contains(body, absent) {
			t.Errorf("automatic events must render no actor text, got %q in: %s", absent, body)
		}
	}

	goldenFile(t, "ticket_detail_hold_closed", body)
}

// ---------------------------------------------------------------------------
// Issue #366 goldens: attachments on the detail. The staff view carries the
// creation file under the description and one file under each comment — the
// public one and the staff-only one. The requester view carries the creation
// file and the public comment's file only, because the handler reads both
// lists with the same includeInternal the comment timeline used.
// ---------------------------------------------------------------------------

// stampGoldenAttachments mirrors the handler's per-comment stamp: it puts the
// files on the timeline entry of one comment.
func stampGoldenAttachments(items []application.TimelineItem, commentID int64, files ...domain.Attachment) {
	for i := range items {
		if items[i].IsComment && items[i].Comment != nil && items[i].Comment.ID == commentID {
			items[i].Attachments = files
		}
	}
}

// fixtureAttachmentStaffDetailData derives the staff detail from the open
// fixture and adds one creation file plus one file per comment.
func fixtureAttachmentStaffDetailData() detailData {
	d := fixtureDetailData()
	d.View.Comments = append(d.View.Comments, domain.Comment{
		ID: 2, TicketID: 1, Author: "Admin", Body: "Staff-only note",
		Visibility: domain.CommentInternal, CreatedAt: goldenT0,
	})
	d.View.Timeline = append([]application.TimelineItem{{
		IsComment: true,
		Comment:   &d.View.Comments[1],
		Attachments: []domain.Attachment{
			{ID: 3, TicketID: 1, FileName: "internal-notes.pdf", ContentType: "application/pdf", Size: 10 << 20},
		},
	}}, d.View.Timeline...)
	// The public comment's file rides its own timeline entry, exactly as the
	// handler stamps it after the scoped read.
	stampGoldenAttachments(d.View.Timeline, 1, domain.Attachment{
		ID: 2, TicketID: 1, FileName: "stack-trace.txt", ContentType: "text/plain; charset=utf-8", Size: 4096,
	})
	d.Attachments = []domain.Attachment{
		{ID: 1, TicketID: 1, FileName: "shot.png", ContentType: "image/png", Size: 1258291},
	}
	return d
}

// fixtureAttachmentRequesterDetailData is the requester's view of a ticket that
// carries a creation file and a public comment's file. The internal comment's
// file is absent from the payload because the store read filtered it, not
// because the template hides it.
func fixtureAttachmentRequesterDetailData() detailData {
	d := requesterResolvedDetailData(domain.User{ID: 2, Role: domain.RoleUser})
	stampGoldenAttachments(d.View.Timeline, 1, domain.Attachment{
		ID: 2, TicketID: 1, FileName: "stack-trace.txt", ContentType: "text/plain; charset=utf-8", Size: 4096,
	})
	d.Attachments = []domain.Attachment{
		{ID: 1, TicketID: 1, FileName: "shot.png", ContentType: "image/png", Size: 1258291},
	}
	return d
}

// TestGoldenTicketDetailWithAttachments freezes the staff detail with the
// creation file and one file per comment: each is a download link carrying the
// original name and the human size, and the creation file sits under the
// description, before the timeline.
func TestGoldenTicketDetailWithAttachments(t *testing.T) {
	body := renderGolden(t, "tickets_show", "ticket_detail", fixtureAttachmentStaffDetailData(), true)

	for _, want := range []string{
		`aria-label="Attachments"`,
		`<a class="attachment-name" href="/attachments/1">shot.png</a> <span class="attachment-size">1.2 MB</span>`,
		`<a class="attachment-name" href="/attachments/2">stack-trace.txt</a> <span class="attachment-size">4 KB</span>`,
		`<a class="attachment-name" href="/attachments/3">internal-notes.pdf</a> <span class="attachment-size">10 MB</span>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("staff detail with attachments must contain %q, got: %s", want, body)
		}
	}
	creation := strings.Index(body, `/attachments/1"`)
	timeline := strings.Index(body, `stack-trace.txt`)
	if creation < 0 || timeline < 0 || creation > timeline {
		t.Errorf("the creation file must render before the comment files\n%s", body)
	}

	goldenFile(t, "ticket_detail_attachments", body)
}

// TestGoldenTicketDetailWithAttachmentsRequester freezes the requester view:
// the creation file and the public comment's file are listed, and the
// staff-only file is nowhere on the page — no name and no link.
func TestGoldenTicketDetailWithAttachmentsRequester(t *testing.T) {
	body := renderGolden(t, "tickets_show", "ticket_detail", fixtureAttachmentRequesterDetailData(), true)

	for _, want := range []string{
		`<a class="attachment-name" href="/attachments/1">shot.png</a> <span class="attachment-size">1.2 MB</span>`,
		`<a class="attachment-name" href="/attachments/2">stack-trace.txt</a> <span class="attachment-size">4 KB</span>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("requester detail with attachments must contain %q, got: %s", want, body)
		}
	}
	for _, absent := range []string{"internal-notes.pdf", `href="/attachments/3"`} {
		if strings.Contains(body, absent) {
			t.Errorf("requester detail must NOT contain the staff-only file %q\n%s", absent, body)
		}
	}

	goldenFile(t, "ticket_detail_attachments_requester", body)
}
