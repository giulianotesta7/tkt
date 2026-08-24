package httpadapter

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
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
		`class="category-table"`, `<th scope="col">Category</th>`, `<th scope="col">Created</th>`,
		`<th scope="col">Status</th>`, `<th scope="col">Actions</th>`,
		`/categories/` + strconv.FormatInt(category.ID, 10) + `/delete`, `>Delete category</button>`,
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
	desks := application.NewDeskService(h.store.DeskStore(), h.store.UserStore(), h.clock)
	first, err := desks.Create(t.Context(), *h.admin, "First desk")
	if err != nil {
		t.Fatal(err)
	}
	second, err := desks.Create(t.Context(), *h.admin, "Second desk")
	if err != nil {
		t.Fatal(err)
	}
	member := h.createUser(t, "Member", "member-amendment3@tkt.test", "secret")
	if err := desks.AddMember(t.Context(), *h.admin, second.ID, member.ID); err != nil {
		t.Fatal(err)
	}

	rec := h.get(t, "/desks?desk_id="+strconv.FormatInt(second.ID, 10), false)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET selected desk = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`class="desks-layout"`, `class="desk-list"`, `class="desk-detail"`, `Second desk`, `1 member`,
		`<details class="desk-create">`, `name="selected_desk_id" value="` + strconv.FormatInt(second.ID, 10) + `"`,
		`/desks/` + strconv.FormatInt(second.ID, 10) + `/members/` + strconv.FormatInt(member.ID, 10) + `/delete`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("desk master/detail missing %q", want)
		}
	}
	if strings.Contains(body, `<option value="`+strconv.FormatInt(member.ID, 10)+`">Member</option>`) {
		t.Error("selected desk member must not be offered again in add-member select")
	}

	fallback := h.get(t, "/desks?desk_id=999999", false)
	if fallback.Code != http.StatusOK || !strings.Contains(fallback.Body.String(), `data-desk-id="`+strconv.FormatInt(first.ID, 10)+`"`) {
		t.Error("invalid selected desk must fall back to the first desk")
	}

}

func TestAmendment3_DesksSelectionMarksOnlyCurrentLink(t *testing.T) {
	h := newHarness(t)
	desks := application.NewDeskService(h.store.DeskStore(), h.store.UserStore(), h.clock)
	first, err := desks.Create(t.Context(), *h.admin, "First desk")
	if err != nil {
		t.Fatal(err)
	}
	second, err := desks.Create(t.Context(), *h.admin, "Second desk")
	if err != nil {
		t.Fatal(err)
	}

	for _, tt := range []struct {
		name     string
		target   string
		selected int64
	}{
		{name: "default first selection", target: "/desks", selected: first.ID},
		{name: "explicit selection", target: "/desks?desk_id=" + strconv.FormatInt(second.ID, 10), selected: second.ID},
		{name: "invalid selection falls back to first", target: "/desks?desk_id=999999", selected: first.ID},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rec := h.get(t, tt.target, false)
			if rec.Code != http.StatusOK {
				t.Fatalf("GET %s = %d, want 200", tt.target, rec.Code)
			}

			body := rec.Body.String()
			for _, desk := range []struct {
				id        int64
				isCurrent bool
			}{
				{id: first.ID, isCurrent: first.ID == tt.selected},
				{id: second.ID, isCurrent: second.ID == tt.selected},
			} {
				linkPrefix := `data-desk-id="` + strconv.FormatInt(desk.id, 10) + `"`
				linkStart := strings.Index(body, linkPrefix)
				if linkStart == -1 {
					t.Fatalf("desk %d link missing", desk.id)
				}
				linkTail := body[linkStart:]
				linkEnd := strings.Index(linkTail, `</a`)
				if linkEnd == -1 {
					t.Fatalf("desk %d link is not closed", desk.id)
				}
				closingTag := linkTail[linkEnd+len(`</a`):]
				closingBracket := strings.IndexByte(closingTag, '>')
				if closingBracket == -1 || strings.TrimSpace(closingTag[:closingBracket]) != "" {
					t.Fatalf("desk %d link has malformed closing tag", desk.id)
				}
				link := linkTail[:linkEnd]
				if got := strings.Contains(link, `aria-current="page"`); got != desk.isCurrent {
					t.Errorf("desk %d aria-current = %t, want %t", desk.id, got, desk.isCurrent)
				}
			}
			if got := strings.Count(body, `aria-current="page"`); got != 1 {
				t.Errorf("current desk links = %d, want 1", got)
			}
			if !strings.Contains(body, `id="desk-`+strconv.FormatInt(tt.selected, 10)+`-title"`) {
				t.Errorf("selected desk %d detail is missing", tt.selected)
			}
		})
	}
}
