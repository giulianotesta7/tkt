package httpadapter

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/domain"
)

func TestAmendment3_CategoryIndexUsesAccessibleResponsiveTable(t *testing.T) {
	h := newHarness(t)
	category, err := h.categories.Create(t.Context(), "Long category name for a narrow layout")
	if err != nil {
		t.Fatal(err)
	}

	rec := h.get(t, "/categories", false)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /categories = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`class="category-level category-level-categories is-current"`, `<h2 id="structure-categories-title">Categories</h2>`,
		`/categories/` + strconv.FormatInt(category.ID, 10) + `/delete`, `category-menu-button`, `>Delete category</button>`, `Actions for ` + category.Name,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("category index missing %q", want)
		}
	}
}

func TestAmendment3_PinnedClaimUsesEligibleSidebarControl(t *testing.T) {
	h := newHarness(t)
	desk, err := h.desks.Create(t.Context(), *h.admin, "Network")
	if err != nil {
		t.Fatal(err)
	}
	ticket := seedClaimCategory(t, h, desk.ID, domain.StrategyClaim)
	if err := h.desks.AddMember(t.Context(), *h.admin, desk.ID, h.admin.ID); err != nil {
		t.Fatal(err)
	}

	body := h.get(t, "/tickets/"+strconv.FormatInt(ticket.ID, 10), false).Body.String()
	for _, want := range []string{"Desk", "Network", "Assignee", "Assign to me", "/workflow/steps/1/complete"} {
		if !strings.Contains(body, want) {
			t.Errorf("eligible claim sidebar missing %q", want)
		}
	}
	if strings.Contains(body, "Current task") || strings.Contains(body, `id="workflow-pending"`) {
		t.Errorf("claim must not render a current-task form: %.500s", body)
	}

	nonmember := h.createUser(t, "Nonmember", "nonmember-amendment3@tkt.test", "secret")
	session := h.sessionFor(t, nonmember.ID)
	req := httptest.NewRequest(http.MethodGet, "/tickets/"+strconv.FormatInt(ticket.ID, 10), nil)
	req.Header.Set("Cookie", sessionCookie+"="+session)
	rec := httptest.NewRecorder()
	h.mw.Wrap(h.mux).ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("nonmember claim read = %d, want scope denial", rec.Code)
	}
}

func TestAmendment3_DesksMasterDetailSelectionAndMemberOptions(t *testing.T) {
	h := newHarness(t)
	desk, err := h.desks.Create(t.Context(), *h.admin, "Legacy desk")
	if err != nil {
		t.Fatal(err)
	}
	rec := h.get(t, "/desks?desk_id="+strconv.FormatInt(desk.ID, 10), false)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/categories" {
		t.Fatalf("legacy desk page = %d/%q, want redirect to unified catalog", rec.Code, rec.Header().Get("Location"))
	}
}

func TestAmendment3_DesksSelectionMarksOnlyCurrentLink(t *testing.T) {
	h := newHarness(t)
	for _, target := range []string{"/desks", "/desks?desk_id=999999"} {
		t.Run(target, func(t *testing.T) {
			rec := h.get(t, target, false)
			if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/categories" {
				t.Fatalf("GET %s = %d/%q, want redirect to unified catalog", target, rec.Code, rec.Header().Get("Location"))
			}
		})
	}
}
