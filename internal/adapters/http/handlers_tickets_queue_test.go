package httpadapter

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/domain"
	"github.com/giulianotesta7/tkt/internal/i18n"
)

// TestParseFiltersView pins the queue view vocabulary (issue #382, S1): the
// five views are accepted, anything else selects "all", and each view maps to
// its store predicate.
func TestParseFiltersView(t *testing.T) {
	for _, tc := range []struct {
		query          string
		wantView       string
		wantUnassigned bool
		wantOnHold     bool
		wantStates     int
	}{
		{query: "", wantView: viewAll},
		{query: "?view=all", wantView: viewAll},
		{query: "?view=mine", wantView: viewMine},
		{query: "?view=unassigned", wantView: viewUnassigned, wantUnassigned: true},
		{query: "?view=on_hold", wantView: viewOnHold, wantOnHold: true},
		{query: "?view=resolved", wantView: viewResolved, wantStates: 2},
		{query: "?view=bogus", wantView: viewAll},
	} {
		t.Run(tc.query, func(t *testing.T) {
			f := parseFilters(httptest.NewRequest(http.MethodGet, "/tickets"+tc.query, nil))
			if f.View != tc.wantView {
				t.Fatalf("View = %q, want %q", f.View, tc.wantView)
			}
			q := f.query()
			if q.Unassigned != tc.wantUnassigned || q.OnHold != tc.wantOnHold || len(q.States) != tc.wantStates {
				t.Errorf("query = unassigned %t on_hold %t states %d, want %t %t %d", q.Unassigned, q.OnHold, len(q.States), tc.wantUnassigned, tc.wantOnHold, tc.wantStates)
			}
		})
	}
}

// TestListHrefCarriesTheView proves every generated href keeps the current
// view except the default, so tabs, chips and pagination never lose it.
func TestListHrefCarriesTheView(t *testing.T) {
	if got := listHref(filterState{View: viewUnassigned, Q: "vpn", Sort: sortPriority}, 2); got != "/tickets?page=2&q=vpn&sort=priority&view=unassigned" {
		t.Errorf("href = %q", got)
	}
	if got := listHref(filterState{View: viewAll}, 1); got != "/tickets" {
		t.Errorf("default view must not appear, got %q", got)
	}
}

// TestRelativeTime pins the queue's "Updated" phrasing (issue #382, S8).
func TestRelativeTime(t *testing.T) {
	tr := i18n.New(i18n.English)
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name string
		t    time.Time
		now  time.Time
		want string
	}{
		{"just now", now.Add(-20 * time.Second), now, "just now"},
		{"minutes", now.Add(-5 * time.Minute), now, "5 min ago"},
		{"hours", now.Add(-3 * time.Hour), now, "3 h ago"},
		{"days", now.Add(-2 * 24 * time.Hour), now, "2 d ago"},
		{"older than a week", now.Add(-8 * 24 * time.Hour), now, formatDisplayTime(now.Add(-8 * 24 * time.Hour))},
		{"zero now keeps the absolute instant", now, time.Time{}, formatDisplayTime(now)},
		{"future keeps the absolute instant", now.Add(time.Hour), now, formatDisplayTime(now.Add(time.Hour))},
	} {
		if got := relativeTime(tr, tc.t, tc.now); got != tc.want {
			t.Errorf("%s: relativeTime = %q, want %q", tc.name, got, tc.want)
		}
	}
	if got := relativeTime(i18n.New(i18n.Spanish), now.Add(-5*time.Minute), now); got != "hace 5 min" {
		t.Errorf("spanish = %q", got)
	}
}

// TestQueueTabsCountTheirViews seeds one unassigned, one assigned, one on hold
// and one resolved ticket and proves each tab says how many rows it opens,
// under the other active filters (issue #382, S1).
func TestQueueTabsCountTheirViews(t *testing.T) {
	h := newHarness(t)
	h.seedTicket(t, "Unassigned one", nil)
	assigned := h.seedTicket(t, "Assigned to admin", nil)
	h.assignTicket(t, assigned.ID, h.admin.ID)
	held := h.seedTicket(t, "Held one", nil)
	h.postForm(t, ticketPath(held.ID)+"/hold", url.Values{"reason": {"waiting for the logs"}}, false)
	done := h.seedTicket(t, "Resolved one", nil)
	h.seedTransition(t, done.ID, domain.StateInProgress, "")
	h.seedTransition(t, done.ID, domain.StateResolved, "")

	body := h.get(t, "/tickets", false).Body.String()
	for _, want := range []string{
		`data-view="all" data-count="4" aria-current="page"`,
		`data-view="all"`, `queue-tab-count">4<`,
		`data-view="mine"`, `href="/tickets?view=mine"`,
		`data-view="unassigned"`, `href="/tickets?view=unassigned"`,
		`data-view="on_hold"`, `href="/tickets?view=on_hold"`,
		`data-view="resolved"`, `href="/tickets?view=resolved"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("queue tabs missing %q", want)
		}
	}
	counts := map[string]string{"mine": "1", "unassigned": "3", "on_hold": "1", "resolved": "1"}
	for view, count := range counts {
		if !strings.Contains(body, `data-view="`+view+`"`) || !strings.Contains(body, `href="/tickets?view=`+view+`"`) {
			t.Errorf("tab %s not rendered", view)
		}
		if !strings.Contains(body, `data-view="`+view+`" data-count="`+count+`"`) {
			t.Errorf("tab %s count must be %s: %.300s", view, count, body[strings.Index(body, `data-view="`+view+`"`):])
		}
	}

	// The counts follow the other filters: with a search that matches one
	// ticket, every tab counts within that match and the unassigned view lists it.
	filtered := h.get(t, "/tickets?q=Held&view=unassigned", false).Body.String()
	if !strings.Contains(filtered, `data-view="all" data-count="1"`) || !strings.Contains(filtered, `data-view="unassigned" data-count="1"`) || !strings.Contains(filtered, `data-view="mine" data-count="0"`) {
		t.Errorf("filtered counts must follow the search: %.600s", filtered[strings.Index(filtered, "queue-tabs"):])
	}
	if !strings.Contains(filtered, `href="/tickets?q=Held&amp;view=mine"`) {
		t.Errorf("tab hrefs must keep the active search")
	}
	if !strings.Contains(filtered, "Held one") || strings.Contains(filtered, "Unassigned one") {
		t.Errorf("the unassigned view with a search lists only the matching unassigned ticket")
	}
}

// TestQueueChipsRemoveOneFilterEach proves each active filter renders as a
// chip whose href is the same query without it, that view and sort survive,
// and that an empty query renders no chip and no Clear link (issue #382, S2).
func TestQueueChipsRemoveOneFilterEach(t *testing.T) {
	h := newHarness(t)
	body := h.get(t, "/tickets?q=vpn&state=new&priority=high&category_id="+itoa(h.bugCategory.ID)+"&user_id="+itoa(h.admin.ID)+"&view=unassigned&sort=priority", false).Body.String()
	for _, want := range []string{
		`class="queue-chip"`,
		`href="/tickets?category_id=` + itoa(h.bugCategory.ID) + `&amp;priority=high&amp;sort=priority&amp;state=new&amp;user_id=` + itoa(h.admin.ID) + `&amp;view=unassigned"`, // without q
		`href="/tickets?category_id=` + itoa(h.bugCategory.ID) + `&amp;priority=high&amp;q=vpn&amp;sort=priority&amp;user_id=` + itoa(h.admin.ID) + `&amp;view=unassigned"`,     // without state
		`>Search: vpn<`, `>New<`, `>High<`, `>` + h.bugCategory.Name + `<`, `>` + h.admin.Name + `<`,
		`aria-label="Clear filters"`, `href="/tickets?view=unassigned"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("chips missing %q", want)
		}
	}
	if strings.Count(body, `class="queue-chip"`) != 5 {
		t.Errorf("want 5 chips, got %d", strings.Count(body, `class="queue-chip"`))
	}

	plain := h.get(t, "/tickets", false).Body.String()
	if strings.Contains(plain, `class="queue-chip"`) || strings.Contains(plain, `aria-label="Clear filters"`) {
		t.Errorf("an unfiltered queue renders no chip and no Clear link")
	}
	for _, gone := range []string{`type="submit">Apply`, `>Clear<`} {
		if strings.Contains(plain, gone) {
			t.Errorf("the live filter bar must not render %q", gone)
		}
	}
}

// TestQueueRowsCarryTriageContext proves a row names its category and its
// assignee (or says it is unassigned), and that the SLA column is absent
// while the instance has no SLA and no row carries a commitment (S3).
func TestQueueRowsCarryTriageContext(t *testing.T) {
	h := newHarness(t)
	assigned := h.seedTicket(t, "Assigned to admin", nil)
	h.assignTicket(t, assigned.ID, h.admin.ID)
	h.seedTicket(t, "Nobody yet", nil)

	body := h.get(t, "/tickets", false).Body.String()
	for _, want := range []string{
		`data-label="Category">` + h.bugCategory.Name,
		`class="queue-assignee"`, h.admin.Name,
		`class="queue-assignee queue-unassigned">Unassigned<`,
		`data-label="Ticket"`, `class="queue-requester">` + h.admin.Name,
		`queue-updated" data-label="Updated"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("row missing %q", want)
		}
	}
	if strings.Contains(body, `data-label="SLA"`) || strings.Contains(body, `>SLA</th>`) {
		t.Errorf("the SLA column must be absent while SLA is off and no row has a commitment")
	}
	if !strings.Contains(body, `<tr class="queue-row" data-href="/tickets/`+itoa(assigned.ID)+`">`) {
		t.Errorf("rows must carry their detail href for the row navigation")
	}
	if got := strings.Count(body, ` href="/tickets/`+itoa(assigned.ID)+`"`); got != 1 {
		t.Errorf("one link per row: the title; got %d", got)
	}
}

// TestQueueEmptyStatesOfferAnAction pins the two empty states (S5).
func TestQueueEmptyStatesOfferAnAction(t *testing.T) {
	h := newHarness(t)
	empty := h.get(t, "/tickets", false).Body.String()
	if !strings.Contains(empty, "No tickets yet") || !strings.Contains(empty, `class="btn primary" href="/tickets/new"`) {
		t.Errorf("an empty queue offers New ticket: %.400s", empty[strings.Index(empty, "queue-empty"):])
	}
	h.seedTicket(t, "Something", nil)
	filtered := h.get(t, "/tickets?q=nomatch&view=unassigned", false).Body.String()
	if !strings.Contains(filtered, "No tickets match your filters") || !strings.Contains(filtered, `href="/tickets?view=unassigned" hx-get="/tickets?view=unassigned"`) {
		t.Errorf("a filtered empty queue offers Clear filters keeping the view")
	}
}
