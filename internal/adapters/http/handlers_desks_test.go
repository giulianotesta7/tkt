package httpadapter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

func TestDeskHandlersCreateListAndManageMembership(t *testing.T) {
	h := newHarness(t)
	desks := application.NewDeskService(h.store.DeskStore(), h.store.UserStore(), h.clock)
	mux := http.NewServeMux()
	NewDeskHandlers(desks, h.renderer).Register(mux)

	createdDesk, err := desks.Create(context.Background(), *h.admin, "Support")
	if err != nil {
		t.Fatalf("create desk through service: %v", err)
	}
	listed := httptest.NewRecorder()
	mux.ServeHTTP(listed, deskRequest(http.MethodGet, "/desks", nil, *h.admin))
	wantRedirect(t, listed, http.StatusSeeOther, "/categories")

	create := httptest.NewRecorder()
	mux.ServeHTTP(create, deskRequest(http.MethodPost, "/desks", url.Values{"name": {"Should not bypass Department"}}, *h.admin))
	if create.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /desks = %d, want method-not-allowed compatibility response", create.Code)
	}

	agent := seedUserRole(t, h.store, "Agent", "agent@tkt.test", domain.RoleAgent)
	deskList, err := desks.List(context.Background(), *h.admin)
	if err != nil || len(deskList) != 2 {
		t.Fatalf("list stored desks = %+v, %v", deskList, err)
	}
	var desk *domain.Desk
	for i := range deskList {
		if deskList[i].ID == createdDesk.ID {
			desk = &deskList[i]
			break
		}
	}
	if desk == nil {
		t.Fatalf("created Support desk missing from %+v", deskList)
	}
	member := httptest.NewRecorder()
	mux.ServeHTTP(member, deskRequest(http.MethodPost, "/desks/"+itoa(desk.ID)+"/members", url.Values{"user_id": {itoa(agent.ID)}}, *h.admin))
	wantRedirect(t, member, http.StatusSeeOther, "/categories?view=structure")
	members, err := desks.ListMembers(context.Background(), *h.admin, desk.ID)
	if err != nil || len(members) != 1 || members[0].ID != agent.ID {
		t.Fatalf("stored membership = %+v, %v", members, err)
	}
}

func TestDeskHandlersHTMXMembershipRendersDrawer(t *testing.T) {
	h := newHarness(t)
	desks := application.NewDeskService(h.store.DeskStore(), h.store.UserStore(), h.clock)
	mux := http.NewServeMux()
	NewDeskHandlers(desks, h.renderer).Register(mux)

	desk, err := desks.Create(context.Background(), *h.admin, "Support")
	if err != nil {
		t.Fatal(err)
	}
	agent := seedUserRole(t, h.store, "Agent", "agent@tkt.test", domain.RoleAgent)
	contextValues := url.Values{"view": {"structure"}, "department_id": {"unassigned"}, "desk_id": {itoa(desk.ID)}}

	add := deskRequest(http.MethodPost, "/desks/"+itoa(desk.ID)+"/members", url.Values{
		"user_id":       {itoa(agent.ID)},
		"view":          {"structure"},
		"department_id": {"unassigned"},
		"desk_id":       {itoa(desk.ID)},
	}, *h.admin)
	add.Header.Set("HX-Request", "true")
	addRec := httptest.NewRecorder()
	mux.ServeHTTP(addRec, add)
	if addRec.Code != http.StatusOK || addRec.Header().Get("HX-Retarget") != "#category-drawer-host" || addRec.Header().Get("HX-Reswap") != "outerHTML" {
		t.Fatalf("HTMX add response = %d/%q/%q", addRec.Code, addRec.Header().Get("HX-Retarget"), addRec.Header().Get("HX-Reswap"))
	}
	if addRec.Header().Get("Location") != "" || !strings.Contains(addRec.Body.String(), agent.Name) {
		t.Fatalf("HTMX add response did not keep drawer context: location=%q body=%s", addRec.Header().Get("Location"), addRec.Body.String())
	}

	remove := deskRequest(http.MethodPost, "/desks/"+itoa(desk.ID)+"/members/"+itoa(agent.ID)+"/delete", contextValues, *h.admin)
	remove.Header.Set("HX-Request", "true")
	removeRec := httptest.NewRecorder()
	mux.ServeHTTP(removeRec, remove)
	if removeRec.Code != http.StatusOK || removeRec.Header().Get("HX-Retarget") != "#category-drawer-host" || removeRec.Header().Get("HX-Reswap") != "outerHTML" {
		t.Fatalf("HTMX remove response = %d/%q/%q", removeRec.Code, removeRec.Header().Get("HX-Retarget"), removeRec.Header().Get("HX-Reswap"))
	}
	memberList := removeRec.Body.String()
	if start := strings.Index(memberList, `<ul class="desk-member-list">`); start >= 0 {
		memberList = memberList[start:]
		if end := strings.Index(memberList, `</ul>`); end >= 0 {
			memberList = memberList[:end]
		}
	}
	if removeRec.Header().Get("Location") != "" || strings.Contains(memberList, agent.Name) {
		t.Fatalf("HTMX remove response did not refresh members: location=%q member list=%s", removeRec.Header().Get("Location"), memberList)
	}
}

func TestDeskHandlersDenyAgentBeforeRenderingData(t *testing.T) {
	h := newHarness(t)
	desks := application.NewDeskService(h.store.DeskStore(), h.store.UserStore(), h.clock)
	mux := http.NewServeMux()
	NewDeskHandlers(desks, h.renderer).Register(mux)

	agent := seedUserRole(t, h.store, "Agent", "agent@tkt.test", domain.RoleAgent)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, deskRequest(http.MethodGet, "/desks", nil, *agent))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("agent GET /desks = %d, want 403", rec.Code)
	}
}

func TestTicketAssignmentRejectsForgedDeskTarget(t *testing.T) {
	h := newHarness(t)
	desks := application.NewDeskService(h.store.DeskStore(), h.store.UserStore(), h.clock)
	desk, err := desks.Create(context.Background(), *h.admin, "Support")
	if err != nil {
		t.Fatal(err)
	}
	ticket := h.seedTicket(t, "Desk target must not assign", nil)

	rec := h.postForm(t, "/tickets/"+itoa(ticket.ID)+"/assign", url.Values{"desk_id": {itoa(desk.ID)}}, false)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("forged desk assignment status = %d, want 422", rec.Code)
	}
	stored, err := h.store.TicketStore().GetByID(context.Background(), ticket.ID, application.TicketQuery{Scope: application.ScopeAll})
	if err != nil {
		t.Fatal(err)
	}
	if stored.UserID != nil {
		t.Fatalf("desk assignment must not assign a person, got user %d", *stored.UserID)
	}
}

func deskRequest(method, target string, values url.Values, actor domain.User) *http.Request {
	var body *strings.Reader
	if values == nil {
		body = strings.NewReader("")
	} else {
		body = strings.NewReader(values.Encode())
	}
	req := httptest.NewRequest(method, target, body)
	if values != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	return req.WithContext(context.WithValue(req.Context(), ctxKeyUser{}, &actor))
}

func itoa(v int64) string { return strconv.FormatInt(v, 10) }
