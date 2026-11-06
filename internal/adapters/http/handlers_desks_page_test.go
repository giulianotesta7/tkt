package httpadapter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// The /desks page (issue #484, S15): desks are teams, no longer a level of the
// catalog tree, so they get their own administration screen again — a table of
// desks with their agents, the categories that name them and their open
// tickets, a drawer to create, rename and staff a desk, and a delete that is
// refused while a category still names the desk.

func deskRows(t *testing.T, h *harness) int64 {
	t.Helper()
	return scanOneInt(t, h.rawDB(t), `SELECT COUNT(*) FROM desks`)
}

func deskIDNamed(t *testing.T, h *harness, name string) int64 {
	t.Helper()
	return scanOneInt(t, h.rawDB(t), `SELECT id FROM desks WHERE name = ?`, name)
}

func TestDesksPageListsDesksWithTheirFacts(t *testing.T) {
	h := newHarness(t)
	service, err := h.desks.Create(context.Background(), *h.admin, "Service desk")
	if err != nil {
		t.Fatal(err)
	}
	agent := seedUserRole(t, h.store, "Ana Gómez", "ana@tkt.test", domain.RoleAgent)
	if err := h.desks.AddMember(context.Background(), *h.admin, service.ID, agent.ID); err != nil {
		t.Fatal(err)
	}

	rec := h.get(t, "/desks", false)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /desks = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	general := deskIDNamed(t, h, "General")
	for _, want := range []string{
		`<h1 class="command-title">Desks</h1>`,
		`href="/desks/new"`,
		`data-desk-id="` + itoa(service.ID) + `"`,
		`data-desk-id="` + itoa(general) + `"`,
		`>Service desk<`,
		`>General<`,
		// General has no members, so nobody can take its tickets.
		`No agents`,
		`href="/desks" data-tip="Desks" aria-current="page"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("GET /desks missing %q", want)
		}
	}
	// The seeded category has no published assignment targeting General.
	row := body[strings.Index(body, `data-desk-id="`+itoa(general)+`"`):]
	row = row[:strings.Index(row, "</tr>")]
	if !strings.Contains(row, `<td class="desks-count" data-label="Categories">0</td>`) {
		t.Errorf("General row counts a category without a published target: %s", row)
	}
}

func TestDesksPageCreatesRenamesAndDeletes(t *testing.T) {
	h := newHarness(t)
	// Arrange the published workflow reference whose deletion guard this tests.
	referenced := deskIDNamed(t, h, "General")
	h.staff(t, referenced)
	h.publishWorkflow(t, h.bugCategory.ID, domain.WorkflowDefinition{{Type: domain.StepAssignToDesk,
		AssignToDesk: &domain.AssignToDeskStep{DeskID: referenced, Strategy: domain.StrategyClaim}}})
	before := deskRows(t, h)

	created := h.postForm(t, "/desks", url.Values{"name": {"  Identity "}, "description": {"Access team"}}, true)
	if created.Code != http.StatusOK || created.Header().Get("HX-Retarget") != "#categories-background" {
		t.Fatalf("create = %d retarget %q, body %s", created.Code, created.Header().Get("HX-Retarget"), created.Body.String())
	}
	if !strings.Contains(created.Body.String(), `>Identity<`) {
		t.Errorf("the refreshed table does not list the new desk")
	}
	id := deskIDNamed(t, h, "Identity")
	if got := scanOneString(t, h.rawDB(t), `SELECT description FROM desks WHERE id = ?`, id); got != "Access team" {
		t.Errorf("description = %q", got)
	}

	for name, form := range map[string]url.Values{
		"blank name":     {"name": {"  "}},
		"duplicate name": {"name": {"Identity"}},
	} {
		rec := h.postForm(t, "/desks", form, true)
		if rec.Header().Get("HX-Retarget") != "#category-drawer-host" || !strings.Contains(rec.Body.String(), `role="alert"`) {
			t.Errorf("%s: want the drawer with an error, got %d retarget %q", name, rec.Code, rec.Header().Get("HX-Retarget"))
		}
	}
	if got := deskRows(t, h); got != before+1 {
		t.Errorf("desks = %d, want %d: a refused create wrote a row", got, before+1)
	}

	renamed := h.postForm(t, "/desks/"+itoa(id)+"/edit", url.Values{"name": {"Identity & access"}, "description": {""}}, true)
	if renamed.Code != http.StatusOK {
		t.Fatalf("rename = %d %s", renamed.Code, renamed.Body.String())
	}
	if got := scanOneString(t, h.rawDB(t), `SELECT name FROM desks WHERE id = ?`, id); got != "Identity & access" {
		t.Errorf("renamed desk = %q", got)
	}

	general := deskIDNamed(t, h, "General")
	if rec := h.postForm(t, "/desks/"+itoa(general)+"/edit", url.Values{"name": {"General"}, "description": {"Front line"}}, true); rec.Code != http.StatusOK {
		t.Fatalf("edit General = %d", rec.Code)
	}
	if got := scanOneString(t, h.rawDB(t), `SELECT description FROM desks WHERE id = ?`, general); got != "Front line" {
		t.Errorf("General description = %q, want Front line", got)
	}

	refused := h.postForm(t, "/desks/"+itoa(general)+"/delete", url.Values{}, true)
	if !strings.Contains(refused.Body.String(), "it is referenced by workflows or assignment history.") {
		t.Errorf("deleting a desk a published workflow targets: %d %s", refused.Code, refused.Body.String())
	}
	if deskIDNamed(t, h, "General") != general {
		t.Errorf("the refused delete removed General")
	}

	deleted := h.postForm(t, "/desks/"+itoa(id)+"/delete", url.Values{}, true)
	if deleted.Code != http.StatusOK || strings.Contains(deleted.Body.String(), `>Identity &amp; access<`) {
		t.Errorf("delete = %d", deleted.Code)
	}
	if got := scanOneInt(t, h.rawDB(t), `SELECT COUNT(*) FROM desks WHERE id = ?`, id); got != 0 {
		t.Errorf("deleted desk still stored")
	}

	plain := h.postForm(t, "/desks", url.Values{"name": {"Facilities"}}, false)
	wantRedirect(t, plain, http.StatusSeeOther, "/desks")
}

func TestDesksPageDrawerManagesMembersOnTheDesksPage(t *testing.T) {
	h := newHarness(t)
	desk, err := h.desks.Create(context.Background(), *h.admin, "Service desk")
	if err != nil {
		t.Fatal(err)
	}
	agent := seedUserRole(t, h.store, "Ana Gómez", "ana@tkt.test", domain.RoleAgent)

	drawer := h.get(t, "/desks/"+itoa(desk.ID)+"/edit", true)
	body := drawer.Body.String()
	if drawer.Code != http.StatusOK || !strings.Contains(body, `action="/desks/`+itoa(desk.ID)+`/edit"`) || !strings.Contains(body, `data-close-url="/desks"`) {
		t.Fatalf("desk drawer = %d %s", drawer.Code, body)
	}
	if strings.Contains(body, `name="department_id"`) {
		t.Errorf("the desks page drawer still asks for a department")
	}
	if !strings.Contains(body, `<input type="hidden" name="context" value="desks">`) {
		t.Errorf("the member forms do not carry the desks context")
	}

	added := h.postForm(t, "/desks/"+itoa(desk.ID)+"/members", url.Values{"user_id": {itoa(agent.ID)}, "context": {"desks"}}, true)
	if added.Header().Get("HX-Retarget") != "#category-drawer-host" || !strings.Contains(added.Body.String(), `data-close-url="/desks"`) || !strings.Contains(added.Body.String(), "Ana Gómez") {
		t.Errorf("member added: %d %q", added.Code, added.Header().Get("HX-Retarget"))
	}
	plain := h.postForm(t, "/desks/"+itoa(desk.ID)+"/members/"+itoa(agent.ID)+"/delete", url.Values{"context": {"desks"}}, false)
	wantRedirect(t, plain, http.StatusSeeOther, "/desks")
	if n := scanOneInt(t, h.rawDB(t), `SELECT COUNT(*) FROM desk_members WHERE desk_id = ?`, desk.ID); n != 0 {
		t.Errorf("members after removal = %d", n)
	}
}

func TestDesksPageRefusesAgents(t *testing.T) {
	h := newHarness(t)
	agent := seedUserRole(t, h.store, "Agent", "agent@tkt.test", domain.RoleAgent)
	session := seedSession(t, h.store, agent.ID)
	before := deskRows(t, h)
	for _, req := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/desks", nil),
		httptest.NewRequest(http.MethodGet, "/desks/new", nil),
		httptest.NewRequest(http.MethodPost, "/desks", strings.NewReader("name=Sneaky")),
	} {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Cookie", sessionCookie+"="+session.ID)
		rec := httptest.NewRecorder()
		h.mw.Wrap(h.mux).ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s %s as agent = %d, want 403", req.Method, req.URL.Path, rec.Code)
		}
	}
	if got := deskRows(t, h); got != before {
		t.Errorf("an agent request wrote a desk")
	}
}
