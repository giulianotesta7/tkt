package httpadapter

import (
	"crypto/sha256"
	"fmt"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// S1/S2: credential/scope rejections leave SQLite database and WAL bytes intact.
func probeBytes(t *testing.T, f *apiFixture) [2][32]byte {
	t.Helper()
	var out [2][32]byte
	for i, suffix := range []string{"", "-wal"} {
		b, err := os.ReadFile(f.h.dbPath + suffix)
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		out[i] = sha256.Sum256(b)
	}
	return out
}

func TestAPIRegressionBoundary(t *testing.T) {
	f := newAPIFixture(t)
	for _, enabled := range []bool{true, false} {
		if err := f.svc.SetAPIEnabled(t.Context(), *f.root, enabled); err != nil {
			t.Fatal(err)
		}
		kind := problemUnauthorized
		if !enabled {
			kind = problemNotFound
		}
		for _, path := range []string{"/api/v1/me", "/api/v1/me/", "/api/v1/../tickets", "/api//v1/me", "/%61pi/v1/me", "/api%2fv1/me", "/api/v1/%2e%2e/tickets"} {
			for _, method := range []string{"GET", "HEAD", "OPTIONS", "POST"} {
				r := httptest.NewRequest(method, path, nil)
				r.Header.Set("Cookie", sessionCookie+"="+f.h.adminSession.ID)
				before := probeBytes(t, f)
				w := httptest.NewRecorder()
				f.h.handler.ServeHTTP(w, r)
				assertAPIProblem(t, w, kind, r.URL.Path)
				if w.Header().Get("Location") != "" || probeBytes(t, f) != before {
					t.Fatalf("%s %s redirected or wrote", method, path)
				}
			}
		}
	}
}

func TestAPIRegressionScopeBeforeData(t *testing.T) {
	f := newAPIFixture(t)
	agent, _ := apiAgent(t, f)
	_, token := apiWriteKey(t, f, agent, "tickets:all")
	f.request("GET", "/api/v1/me", "Bearer "+token, "", "") // prescribed last_used write precedes snapshot
	for _, tc := range []struct{ method, path, body string }{
		{"GET", "/api/v1/categories", ""}, {"GET", "/api/v1/tickets?state=unknown", ""}, {"GET", "/api/v1/tickets/invalid", ""},
		{"POST", "/api/v1/tickets", "{"}, {"POST", "/api/v1/tickets/invalid/transition", "{"},
		{"POST", "/api/v1/tickets/invalid/comments", "{"}, {"POST", "/api/v1/tickets/invalid/confirm", "{"},
		{"POST", "/api/v1/tickets/invalid/reject", "{"}, {"POST", "/api/v1/tickets/invalid/workflow/steps/1/complete", "{"},
	} {
		before := probeBytes(t, f)
		w := f.request(tc.method, tc.path, "Bearer "+token, tc.body, "text/plain")
		r := httptest.NewRequest(tc.method, tc.path, nil)
		assertAPIProblem(t, w, problemScopeMissing, r.URL.Path)
		if probeBytes(t, f) != before {
			t.Fatalf("%s wrote data", tc.path)
		}
	}
}

// S1/L3/S11: continuously exhausted keys still update last_used once a minute.
func TestAPIRegressionRateTouch(t *testing.T) {
	f := newAPIFixture(t)
	for i := 0; i < 60; i++ {
		if w := f.request("GET", "/api/v1/me", "Bearer "+f.token, "", ""); w.Code != 200 {
			t.Fatal(w.Code)
		}
	}
	key, err := f.h.store.APIAccessStore().APIKeyByPublicID(t.Context(), f.key.PublicID)
	if err != nil {
		t.Fatal(err)
	}
	first := *key.LastUsedAt
	// Two requests per second keep the bucket exhausted while crossing touch interval.
	for i := 0; i < 61; i++ {
		before := probeBytes(t, f)
		w := f.request("GET", "/api/v1/me", "Bearer "+f.token, "", "")
		assertAPIProblem(t, w, problemRateLimited, "/api/v1/me")
		if w.Header().Get("Retry-After") != "1" || probeBytes(t, f) != before {
			t.Fatal("rate refusal changed data or retry")
		}
		f.h.clock.Advance(time.Second)
		if w = f.request("GET", "/api/v1/me", "Bearer "+f.token, "", ""); w.Code != 200 {
			t.Fatal(w.Code)
		}
	}
	key, err = f.h.store.APIAccessStore().APIKeyByPublicID(t.Context(), f.key.PublicID)
	if err != nil || key.LastUsedAt == nil || !key.LastUsedAt.Equal(first.Add(time.Minute)) {
		t.Fatalf("touch=%+v %v", key, err)
	}
}

// S2/S14: an existing all-scope credential must not widen a downgraded service.
func TestAPIRegressionRoleIntersection(t *testing.T) {
	f := newAPIFixture(t)
	ticket := apiOwnTicket(t, f, "Not the agent's request")
	agent, _ := apiAgent(t, f)
	_, token := apiWriteKey(t, f, agent, "catalog:read", "tickets:read", "tickets:write", "comments:write", "comments:internal", "tickets:all")
	agent.Role = domain.RoleUser
	if err := f.h.store.UserStore().Update(t.Context(), agent); err != nil {
		t.Fatal(err)
	}
	f.request("GET", "/api/v1/me", "Bearer "+token, "", "")
	for _, tc := range []struct {
		suffix, body string
		kind         apiProblemKind
	}{
		{"/transition", `{"state":"in_progress"}`, problemForbidden},
		{"/comments", `{"body":"secret","visibility":"internal"}`, problemForbidden},
		{"/comments", `{"body":"public"}`, problemNotFound},
		{"/confirm", `{}`, problemNotFound}, {"/reject", `{}`, problemNotFound},
		{"/workflow/steps/1/complete", `{}`, problemNotFound},
	} {
		path := fmt.Sprintf("/api/v1/tickets/%d%s", ticket.ID, tc.suffix)
		before := probeBytes(t, f)
		assertAPIProblem(t, f.request("POST", path, "Bearer "+token, tc.body, "application/json"), tc.kind, path)
		if probeBytes(t, f) != before {
			t.Fatal("role refusal wrote data")
		}
	}
	list := apiObject(t, f.request("GET", "/api/v1/tickets", "Bearer "+token, "", ""))
	if len(list["items"].([]any)) != 0 {
		t.Fatal("user role inherited tickets:all")
	}
}
