package httpadapter

import (
	"flag"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// Golden-file harness (D7): frozen fixtures render deterministically (fixed
// clock, fixed order) and are compared against testdata/*.golden. Run with
// -update to regenerate, then rerun WITHOUT -update to prove stability.
var update = flag.Bool("update", false, "update golden files")

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

// ---------------------------------------------------------------------------
// Task 5.4 goldens: tickets index/new pages + list fragments with frozen
// fixture instants (D7 — the render path never calls time.Now()).
// ---------------------------------------------------------------------------

var (
	goldenT0 = time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC)
	goldenT1 = time.Date(2026, 8, 6, 10, 30, 0, 0, time.UTC)
)

// fixtureListData builds a frozen list payload: two tickets, two categories,
// one user, and chips derived from fixed counts.
func fixtureListData() listData {
	ana := domain.User{ID: 1, Name: "Ana Torres", Email: "ana@example.com", CreatedAt: goldenT0}
	f := filterState{State: domain.StateNew}
	opts := options{
		States:          listStates,
		Priorities:      listPriorities,
		Categories:      []domain.Category{{ID: 1, Name: "Bugs", CreatedAt: goldenT0}, {ID: 2, Name: "Support", CreatedAt: goldenT0}},
		Users:           []domain.User{ana},
		AssignableUsers: []domain.User{ana},
	}
	tickets := []domain.Ticket{
		{ID: 2, Number: 2, Title: "Printer jam", State: domain.StateInProgress, Priority: domain.PriorityHigh, CreatedAt: goldenT1, UpdatedAt: goldenT1},
		{ID: 1, Number: 1, Title: "Login page down", State: domain.StateNew, Priority: domain.PriorityCritical, CreatedAt: goldenT0, UpdatedAt: goldenT0},
	}
	byState := map[domain.State]int{domain.StateNew: 1, domain.StateInProgress: 1}
	byPriority := map[domain.Priority]int{domain.PriorityHigh: 1, domain.PriorityCritical: 1}
	return listData{
		pageData: pageData{NavActive: "tickets", CurrentUser: ana},
		Filters:  f,
		Options:  opts,
		Tickets:  tickets,
		Total:    2,
		Page:     1,
		Pages:    1,
		PrevHref: "",
		NextHref: "",
		Chips:    buildChips(f, byState, byPriority),
	}
}

// fixtureTicketFormData builds a frozen create-form payload (no error).
func fixtureTicketFormData() ticketFormData {
	ana := domain.User{ID: 1, Name: "Ana Torres", Email: "ana@example.com", CreatedAt: goldenT0}
	opts := options{
		States:          listStates,
		Priorities:      listPriorities,
		Categories:      []domain.Category{{ID: 1, Name: "Bugs", CreatedAt: goldenT0}, {ID: 2, Name: "Support", CreatedAt: goldenT0}},
		Users:           []domain.User{ana},
		AssignableUsers: []domain.User{ana},
	}
	return ticketFormData{
		pageData: pageData{NavActive: "tickets", CurrentUser: ana},
		Values: ticketFormValues{
			Title:          "Login page down",
			Description:    "The login form 500s on submit",
			RequesterName:  "Ana Torres",
			RequesterEmail: "ana@example.com",
			CategoryID:     "1",
			Priority:       domain.PriorityHigh,
		},
		Options: opts,
	}
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

func TestGoldenTicketsIndex(t *testing.T) {
	goldenFile(t, "tickets_index", renderGolden(t, "tickets_index", "", fixtureListData(), false))
}

func TestGoldenTicketsNew(t *testing.T) {
	goldenFile(t, "tickets_new", renderGolden(t, "tickets_new", "", fixtureTicketFormData(), false))
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

func TestGoldenSummaryChips(t *testing.T) {
	goldenFile(t, "summary_chips", renderGolden(t, "tickets_index", "summary_chips", fixtureListData(), true))
}

func TestGoldenStateBadge(t *testing.T) {
	goldenFile(t, "state_badge", renderGolden(t, "tickets_index", "state_badge", domain.StateResolved, true))
}

// ---------------------------------------------------------------------------
// Task 5.5 goldens: detail/edit pages + fragments with frozen fixtures.
// ---------------------------------------------------------------------------

func strptr(s string) *string { return &s }

func fixtureDetailData() detailData {
	ana := domain.User{ID: 1, Name: "Ana Torres", Email: "ana@example.com", CreatedAt: goldenT0}
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
		},
	}
	return detailData{
		pageData: pageData{NavActive: "tickets", CurrentUser: ana},
		View:     view,
		Next:     allowedNext(t.State),
	}
}

func fixtureEditFormData() editFormData {
	ana := domain.User{ID: 1, Name: "Ana Torres", Email: "ana@example.com", CreatedAt: goldenT0}
	opts := options{
		States:          listStates,
		Priorities:      listPriorities,
		Categories:      []domain.Category{{ID: 1, Name: "Bugs", CreatedAt: goldenT0}, {ID: 2, Name: "Support", CreatedAt: goldenT0}},
		Users:           []domain.User{ana},
		AssignableUsers: []domain.User{ana},
	}
	return editFormData{
		pageData: pageData{NavActive: "tickets", CurrentUser: ana},
		TicketID: 1,
		Number:   1,
		Values: ticketFormValues{
			Title:       "Login page down",
			Description: "The login form 500s on submit",
			CategoryID:  "1",
			UserID:      "1",
			Priority:    domain.PriorityHigh,
		},
		Options: opts,
	}
}

func TestGoldenTicketsShow(t *testing.T) {
	goldenFile(t, "tickets_show", renderGolden(t, "tickets_show", "", fixtureDetailData(), false))
}

func TestGoldenTicketsEdit(t *testing.T) {
	goldenFile(t, "tickets_edit", renderGolden(t, "tickets_edit", "", fixtureEditFormData(), false))
}

func TestGoldenTicketDetail(t *testing.T) {
	goldenFile(t, "ticket_detail", renderGolden(t, "tickets_show", "ticket_detail", fixtureDetailData(), true))
}

func TestGoldenTicketEditForm(t *testing.T) {
	goldenFile(t, "ticket_edit_form", renderGolden(t, "tickets_edit", "ticket_edit_form", fixtureEditFormData(), true))
}

func TestGoldenCommentForm(t *testing.T) {
	goldenFile(t, "comment_form", renderGolden(t, "tickets_show", "comment_form", fixtureDetailData(), true))
}

func TestGoldenCommentList(t *testing.T) {
	goldenFile(t, "comment_list", renderGolden(t, "tickets_show", "comment_list", fixtureDetailData(), true))
}

func TestGoldenAuditTimeline(t *testing.T) {
	goldenFile(t, "audit_timeline", renderGolden(t, "tickets_show", "audit_timeline", fixtureDetailData().View.AuditEvents, true))
}

// ---------------------------------------------------------------------------
// Task 5.6 goldens: users/categories pages + forms.
// ---------------------------------------------------------------------------

func fixtureUsersIndexData() usersIndexData {
	ana := domain.User{ID: 1, Name: "Ana Torres", Email: "ana@example.com", Active: true, CreatedAt: goldenT0}
	beto := domain.User{ID: 2, Name: "Beto Ruiz", Email: "beto@example.com", Active: false, CreatedAt: goldenT1}
	return usersIndexData{
		pageData: pageData{NavActive: "users", CurrentUser: ana},
		Users:    []domain.User{ana, beto},
	}
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
	return categoriesIndexData{
		pageData: pageData{NavActive: "categories", CurrentUser: ana},
		Categories: []domain.Category{
			{ID: 1, Name: "Bugs", CreatedAt: goldenT0},
			{ID: 2, Name: "Support", CreatedAt: goldenT1},
		},
	}
}

func fixtureCategoryFormData() categoryFormData {
	ana := domain.User{ID: 1, Name: "Ana Torres", Email: "ana@example.com", Active: true, CreatedAt: goldenT0}
	return categoryFormData{
		pageData:   pageData{NavActive: "categories", CurrentUser: ana},
		CategoryID: 1,
		Name:       "Bugs",
	}
}

func TestGoldenUsersIndex(t *testing.T) {
	goldenFile(t, "users_index", renderGolden(t, "users_index", "", fixtureUsersIndexData(), false))
}

func TestGoldenUsersNew(t *testing.T) {
	goldenFile(t, "users_new", renderGolden(t, "users_new", "", fixtureUserFormData(), false))
}

func TestGoldenUserForm(t *testing.T) {
	goldenFile(t, "user_form", renderGolden(t, "users_new", "user_form", fixtureUserFormData(), true))
}

func TestGoldenCategoriesIndex(t *testing.T) {
	goldenFile(t, "categories_index", renderGolden(t, "categories_index", "", fixtureCategoriesIndexData(), false))
}

func TestGoldenCategoriesNew(t *testing.T) {
	goldenFile(t, "categories_new", renderGolden(t, "categories_new", "", fixtureCategoryFormData(), false))
}

func TestGoldenCategoryForm(t *testing.T) {
	goldenFile(t, "category_form", renderGolden(t, "categories_new", "category_form", fixtureCategoryFormData(), true))
}
