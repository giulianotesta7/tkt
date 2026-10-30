package httpadapter

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

type apiFixture struct {
	h          *harness
	svc        *application.APIAccessService
	root, user *domain.User
	key        *domain.APIKey
	token      string
}

func newAPIFixture(t *testing.T) *apiFixture {
	t.Helper()
	h := newHarness(t)
	svc := application.NewAPIAccessService(h.store.APIAccessStore(), h.store.UserStore(), h.clock, nil)
	root := seedUserRole(t, h.store, "Root", "root@tkt.test", domain.RoleRoot)
	if err := svc.SetAPIEnabled(t.Context(), *root, true); err != nil {
		t.Fatal(err)
	}
	u, err := svc.CreateServiceUser(t.Context(), *root, "Integration", domain.RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	key, token, err := svc.CreateAPIKey(t.Context(), *root, application.CreateAPIKeyInput{ServiceUserID: u.ID, Name: "Probe", Scopes: []string{"tickets:read"}})
	if err != nil {
		t.Fatal(err)
	}
	return &apiFixture{h: h, svc: svc, root: root, user: u, key: key, token: token}
}

func (f *apiFixture) request(method, path, auth, body, contentType string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Authorization", auth)
	r.Header.Set("Content-Type", contentType)
	w := httptest.NewRecorder()
	f.h.handler.ServeHTTP(w, r)
	return w
}

func assertAPIProblem(t *testing.T, w *httptest.ResponseRecorder, kind apiProblemKind, path string) {
	t.Helper()
	if w.Code != kind.Status || w.Header().Get("Content-Type") != problemContentType || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("response = %d %v %s", w.Code, w.Header(), w.Body.String())
	}
	var p apiProblem
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	if p.Type != kind.Type || p.Title != kind.Title || p.Status != kind.Status || p.Instance != path {
		t.Fatalf("problem = %+v", p)
	}
}

// Domain counters have no aggregate store port. All individual key state is
// read through APIAccessStore, not raw SQL.
func (f *apiFixture) domainCounts(t *testing.T) [3]int64 {
	t.Helper()
	db := f.h.rawDB(t)
	return [3]int64{scanOneInt(t, db, "SELECT COUNT(*) FROM tickets"), scanOneInt(t, db, "SELECT COUNT(*) FROM comments"), scanOneInt(t, db, "SELECT COUNT(*) FROM audit_events")}
}

func TestAPIMe(t *testing.T) {
	f := newAPIFixture(t)
	w := f.request("GET", "/api/v1/me", "Bearer "+f.token, "", "")
	if w.Code != 200 || w.Header().Get("Content-Type") != "application/json" || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("response = %d %v %s", w.Code, w.Header(), w.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"id": float64(f.user.ID), "name": f.user.Name, "role": string(f.user.Role), "scopes": []any{"tickets:read"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("me = %#v, want %#v", got, want)
	}
	key, err := f.h.store.APIAccessStore().APIKeyByPublicID(t.Context(), f.key.PublicID)
	if err != nil || key.LastUsedAt == nil || !key.LastUsedAt.Equal(f.h.clock.Now().Truncate(time.Second)) {
		t.Fatalf("last use = %+v, %v", key, err)
	}
}

func TestAPICredentialRejections(t *testing.T) {
	for _, name := range []string{"missing", "scheme", "malformed", "extra", "wrong_secret", "revoked", "expired", "inactive", "session_only"} {
		t.Run(name, func(t *testing.T) {
			f := newAPIFixture(t)
			auth := "Bearer " + f.token
			switch name {
			case "missing", "session_only":
				auth = ""
			case "scheme":
				auth = "Basic " + f.token
			case "malformed":
				auth = "Bearer tkt_bad"
			case "extra":
				auth += " extra"
			case "wrong_secret":
				auth = "Bearer tkt_" + f.key.PublicID + "_" + strings.Repeat("0", 64)
			case "revoked":
				if err := f.svc.RevokeAPIKey(t.Context(), *f.root, f.key.ID); err != nil {
					t.Fatal(err)
				}
			case "expired":
				expiry := f.h.clock.Now().Add(time.Minute)
				key, token, err := f.svc.CreateAPIKey(t.Context(), *f.root, application.CreateAPIKeyInput{ServiceUserID: f.user.ID, Name: "Expiring", Scopes: []string{"tickets:read"}, ExpiresAt: &expiry})
				if err != nil {
					t.Fatal(err)
				}
				f.key, f.token = key, token
				auth = "Bearer " + token
				f.h.clock.Advance(time.Minute)
			case "inactive":
				if err := f.svc.SetServiceUserActive(t.Context(), *f.root, f.user.ID, false); err != nil {
					t.Fatal(err)
				}
			}
			before, err := f.h.store.APIAccessStore().APIKeyByPublicID(t.Context(), f.key.PublicID)
			if err != nil {
				t.Fatal(err)
			}
			counts := f.domainCounts(t)
			r := httptest.NewRequest("GET", "/api/v1/me", nil)
			r.Header.Set("Authorization", auth)
			if name == "session_only" {
				r.Header.Set("Cookie", sessionCookie+"="+f.h.adminSession.ID)
			}
			w := httptest.NewRecorder()
			f.h.handler.ServeHTTP(w, r)
			assertAPIProblem(t, w, problemUnauthorized, "/api/v1/me")
			if w.Header().Get("WWW-Authenticate") != "Bearer" {
				t.Fatal("missing Bearer challenge")
			}
			want := "{\"type\":\"urn:tkt:problem:unauthorized\",\"title\":\"Invalid credentials\",\"status\":401,\"instance\":\"/api/v1/me\"}\n"
			if w.Body.String() != want {
				t.Fatalf("non-generic rejection: %s", w.Body.String())
			}
			after, err := f.h.store.APIAccessStore().APIKeyByPublicID(t.Context(), f.key.PublicID)
			if err != nil || !reflect.DeepEqual(before, after) || f.domainCounts(t) != counts {
				t.Fatalf("rejection wrote data: before=%+v after=%+v err=%v", before, after, err)
			}
		})
	}
}

func TestAPIDisabledAndRouting(t *testing.T) {
	f := newAPIFixture(t)
	counts := f.domainCounts(t)
	assertAPIProblem(t, f.request("GET", "/api/missing", "Bearer "+f.token, "", ""), problemNotFound, "/api/missing")
	key, err := f.h.store.APIAccessStore().APIKeyByPublicID(t.Context(), f.key.PublicID)
	if err != nil || key.LastUsedAt == nil {
		t.Fatal("valid credentials on a refused route must still record use", err)
	}
	w := f.request("POST", "/api/v1/me", "Bearer "+f.token, "", "")
	assertAPIProblem(t, w, problemMethodNotAllowed, "/api/v1/me")
	if w.Header().Get("Allow") != "GET, HEAD" {
		t.Fatalf("Allow=%q", w.Header().Get("Allow"))
	}
	if err := f.svc.SetAPIEnabled(t.Context(), *f.root, false); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/v1/me", "/api/missing"} {
		for _, auth := range []string{"", "Bearer " + f.token, "Basic bad"} {
			assertAPIProblem(t, f.request("GET", path, auth, "", ""), problemNotFound, path)
		}
	}
	if f.domainCounts(t) != counts {
		t.Fatal("routing refusal wrote domain data")
	}
}

func TestAPIRateLimit(t *testing.T) {
	f := newAPIFixture(t)
	counts := f.domainCounts(t)
	for i := 0; i < 60; i++ {
		if w := f.request("GET", "/api/v1/me", "Bearer "+f.token, "", ""); w.Code != 200 {
			t.Fatalf("request %d: %d %s", i+1, w.Code, w.Body.String())
		}
	}
	w := f.request("GET", "/api/v1/me", "Bearer "+f.token, "", "")
	assertAPIProblem(t, w, problemRateLimited, "/api/v1/me")
	if w.Header().Get("Retry-After") != "1" {
		t.Fatalf("Retry-After=%q", w.Header().Get("Retry-After"))
	}
	_, token, err := f.svc.CreateAPIKey(t.Context(), *f.root, application.CreateAPIKeyInput{ServiceUserID: f.user.ID, Name: "Other", Scopes: []string{"tickets:read"}})
	if err != nil {
		t.Fatal(err)
	}
	if w := f.request("GET", "/api/v1/me", "Bearer "+token, "", ""); w.Code != 200 {
		t.Fatal("another key shared the bucket")
	}
	f.h.clock.Advance(time.Minute)
	if w := f.request("GET", "/api/v1/me", "Bearer "+f.token, "", ""); w.Code != 200 {
		t.Fatal("bucket did not refill")
	}
	if f.domainCounts(t) != counts {
		t.Fatal("rate limiting wrote domain data")
	}
}

func TestAPIHelpers(t *testing.T) {
	for _, tc := range []struct {
		name, contentType, body string
		scope                   domain.APIKeyScope
		want                    apiProblemKind
	}{
		{"scope", "application/json", `{}`, domain.ScopeTicketsWrite, problemScopeMissing},
		{"media", "text/plain", `{}`, domain.ScopeTicketsRead, problemUnsupportedMedia},
		{"missing_media", "", `{}`, domain.ScopeTicketsRead, problemUnsupportedMedia},
		{"unknown", "application/json", `{"other":1}`, domain.ScopeTicketsRead, problemBadRequest},
		{"trailing", "application/json", `{} {}`, domain.ScopeTicketsRead, problemBadRequest},
		{"garbage", "application/json", `{} x`, domain.ScopeTicketsRead, problemBadRequest},
		{"empty", "application/json", ``, domain.ScopeTicketsRead, problemBadRequest},
		{"broken", "application/json", `{`, domain.ScopeTicketsRead, problemBadRequest},
		{"valid", "application/json; charset=utf-8", `{"name":"ok"}`, domain.ScopeTicketsRead, apiProblemKind{Status: 200}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newAPIFixture(t)
			counts := f.domainCounts(t)
			called := false
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				actor := userFromContext(r.Context())
				id, ok := application.APIKeyIDFrom(r.Context())
				if actor == nil || actor.ID != f.user.ID || !ok || id != f.key.ID {
					t.Fatal("missing actor or audit context")
				}
				if !requireScope(w, r, tc.scope) {
					return
				}
				var in struct {
					Name string `json:"name"`
				}
				if !decodeJSON(w, r, &in) {
					return
				}
				called = true
				writeJSON(w, 200, in)
			})
			f.h.handler = NewAPIHandlers(f.svc, f.h.clock).Wrap(next)
			w := f.request("POST", "/api/probe", "Bearer "+f.token, tc.body, tc.contentType)
			if tc.want.Status == 200 {
				if !called || w.Code != 200 || w.Body.String() != "{\"name\":\"ok\"}\n" || w.Header().Get("Content-Type") != "application/json" || w.Header().Get("Cache-Control") != "no-store" {
					t.Fatalf("success = %d %v %s", w.Code, w.Header(), w.Body.String())
				}
			} else {
				assertAPIProblem(t, w, tc.want, "/api/probe")
				if called {
					t.Fatal("rejected input reached mutation")
				}
			}
			if f.domainCounts(t) != counts {
				t.Fatal("helper refusal wrote domain data")
			}
		})
	}
}

func TestAPIInternalError(t *testing.T) {
	f := newAPIFixture(t)
	if err := f.h.store.Close(); err != nil {
		t.Fatal(err)
	}
	w := f.request("GET", "/api/v1/me", "Bearer "+f.token, "", "")
	assertAPIProblem(t, w, problemInternal, "/api/v1/me")
	if strings.Contains(w.Body.String(), "sql") {
		t.Fatal("internal error leaked")
	}
}

func TestAPIBodyLimit(t *testing.T) {
	f := newAPIFixture(t)
	counts := f.domainCounts(t)
	for _, chunked := range []bool{false, true} {
		r := httptest.NewRequest("POST", "/api/v1/me", strings.NewReader(strings.Repeat("x", maxRequestBodyBytes+1)))
		r.Header.Set("Authorization", "Bearer "+f.token)
		if chunked {
			r.ContentLength = -1
		}
		w := httptest.NewRecorder()
		f.h.handler.ServeHTTP(w, r)
		assertAPIProblem(t, w, apiProblemKind{"urn:tkt:problem:request-too-large", "Request too large", 413}, "/api/v1/me")
	}
	if f.domainCounts(t) != counts {
		t.Fatal("body limit wrote domain data")
	}
}

func TestAPIBypassesSessionAndOrigin(t *testing.T) {
	f := newAPIFixture(t)
	r := httptest.NewRequest("POST", "/api/v1/me", nil)
	r.Header.Set("Authorization", "Bearer "+f.token)
	r.Header.Set("Origin", "https://other.example")
	w := httptest.NewRecorder()
	f.h.handler.ServeHTTP(w, r)
	assertAPIProblem(t, w, problemMethodNotAllowed, "/api/v1/me")
	w = f.request("GET", "/tickets", "Bearer "+f.token, "", "")
	wantRedirect(t, w, http.StatusSeeOther, "/login")
}
