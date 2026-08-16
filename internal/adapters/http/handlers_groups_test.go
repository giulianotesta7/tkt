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

func TestGroupHandlersCreateListAndManageMembership(t *testing.T) {
	h := newHarness(t)
	groups := application.NewGroupService(h.store.GroupStore(), h.store.UserStore(), h.clock)
	mux := http.NewServeMux()
	NewGroupHandlers(groups, h.renderer).Register(mux)

	create := groupRequest(http.MethodPost, "/groups", url.Values{"name": {"Support"}}, *h.admin)
	created := httptest.NewRecorder()
	mux.ServeHTTP(created, create)
	wantRedirect(t, created, http.StatusSeeOther, "/groups")

	listed := httptest.NewRecorder()
	mux.ServeHTTP(listed, groupRequest(http.MethodGet, "/groups", nil, *h.admin))
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), "Support") {
		t.Fatalf("group index status/body = %d/%s, want Support", listed.Code, listed.Body.String())
	}

	agent := seedUserRole(t, h.store, "Agent", "agent@tkt.test", domain.RoleAgent)
	group, err := groups.List(context.Background(), *h.admin)
	if err != nil || len(group) != 1 {
		t.Fatalf("list stored group = %+v, %v", group, err)
	}
	member := httptest.NewRecorder()
	mux.ServeHTTP(member, groupRequest(http.MethodPost, "/groups/"+itoa(group[0].ID)+"/members", url.Values{"user_id": {itoa(agent.ID)}}, *h.admin))
	wantRedirect(t, member, http.StatusSeeOther, "/groups")
	members, err := groups.ListMembers(context.Background(), *h.admin, group[0].ID)
	if err != nil || len(members) != 1 || members[0].ID != agent.ID {
		t.Fatalf("stored membership = %+v, %v", members, err)
	}
}

func TestGroupHandlersDenyAgentBeforeRenderingData(t *testing.T) {
	h := newHarness(t)
	groups := application.NewGroupService(h.store.GroupStore(), h.store.UserStore(), h.clock)
	mux := http.NewServeMux()
	NewGroupHandlers(groups, h.renderer).Register(mux)

	agent := seedUserRole(t, h.store, "Agent", "agent@tkt.test", domain.RoleAgent)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, groupRequest(http.MethodGet, "/groups", nil, *agent))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("agent GET /groups = %d, want 403", rec.Code)
	}
}

func TestTicketAssignmentRejectsForgedGroupTarget(t *testing.T) {
	h := newHarness(t)
	groups := application.NewGroupService(h.store.GroupStore(), h.store.UserStore(), h.clock)
	group, err := groups.Create(context.Background(), *h.admin, "Support")
	if err != nil {
		t.Fatal(err)
	}
	ticket := h.seedTicket(t, "Group target must not assign", nil)

	rec := h.postForm(t, "/tickets/"+itoa(ticket.ID)+"/assign", url.Values{"group_id": {itoa(group.ID)}}, false)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("forged group assignment status = %d, want 422", rec.Code)
	}
	stored, err := h.store.TicketStore().GetByID(context.Background(), ticket.ID, application.TicketQuery{Scope: application.ScopeAll})
	if err != nil {
		t.Fatal(err)
	}
	if stored.UserID != nil {
		t.Fatalf("group assignment must not assign a person, got user %d", *stored.UserID)
	}
}

func groupRequest(method, target string, values url.Values, actor domain.User) *http.Request {
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
