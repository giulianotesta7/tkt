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

	create := deskRequest(http.MethodPost, "/desks", url.Values{"name": {"Support"}}, *h.admin)
	created := httptest.NewRecorder()
	mux.ServeHTTP(created, create)
	wantRedirect(t, created, http.StatusSeeOther, "/desks")

	listed := httptest.NewRecorder()
	mux.ServeHTTP(listed, deskRequest(http.MethodGet, "/desks", nil, *h.admin))
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), "Support") {
		t.Fatalf("desk index status/body = %d/%s, want Support", listed.Code, listed.Body.String())
	}

	agent := seedUserRole(t, h.store, "Agent", "agent@tkt.test", domain.RoleAgent)
	desk, err := desks.List(context.Background(), *h.admin)
	if err != nil || len(desk) != 1 {
		t.Fatalf("list stored desk = %+v, %v", desk, err)
	}
	member := httptest.NewRecorder()
	mux.ServeHTTP(member, deskRequest(http.MethodPost, "/desks/"+itoa(desk[0].ID)+"/members", url.Values{"user_id": {itoa(agent.ID)}}, *h.admin))
	wantRedirect(t, member, http.StatusSeeOther, "/desks")
	members, err := desks.ListMembers(context.Background(), *h.admin, desk[0].ID)
	if err != nil || len(members) != 1 || members[0].ID != agent.ID {
		t.Fatalf("stored membership = %+v, %v", members, err)
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
