package httpadapter

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// failingSessionStore answers every session lookup with an operational error.
type failingSessionStore struct{ application.SessionStore }

func (failingSessionStore) GetByID(context.Context, string) (*domain.Session, error) {
	return nil, errors.New("session store unavailable")
}

func (failingSessionStore) Delete(context.Context, string) error {
	return errors.New("session store unavailable")
}

// failingUserStore answers every user lookup with an operational error.
type failingUserStore struct{ application.UserStore }

func (failingUserStore) GetByID(context.Context, int64) (*domain.User, error) {
	return nil, errors.New("user store unavailable")
}

// TestMiddlewareNoCookieRedirectsToLogin proves the unauthenticated request
// is redirected 303 to /login (session-protected routes spec).
func TestMiddlewareNoCookieRedirectsToLogin(t *testing.T) {
	s := openTestStore(t)
	seedUser(t, s, "Ana", "ana@example.com")
	mw := NewSessionMiddleware(s.SessionStore(), s.UserStore(), s.SettingsStore())
	mux := http.NewServeMux()
	mux.HandleFunc("GET /tickets", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("protected")) })

	rec := doRequest(mux, mw, http.MethodGet, "/tickets", nil)

	wantRedirect(t, rec, http.StatusSeeOther, "/login")
}

// TestMiddlewareExpiredAndForgedTokenRedirectToLogin proves expired or
// forged tokens are treated as unauthenticated (expired-session spec).
func TestMiddlewareExpiredAndForgedTokenRedirectToLogin(t *testing.T) {
	s := openTestStore(t)
	user := seedUser(t, s, "Ana", "ana@example.com")
	// An expired session row (wall-clock past) — GetByID must treat it as
	// not found (and purge it lazily).
	expired := &domain.Session{ID: "expired-token", UserID: user.ID, ExpiresAt: time.Now().Add(-time.Hour)}
	if err := s.SessionStore().Create(context.Background(), expired); err != nil {
		t.Fatalf("seed expired session: %v", err)
	}
	mw := NewSessionMiddleware(s.SessionStore(), s.UserStore(), s.SettingsStore())
	mux := http.NewServeMux()
	mux.HandleFunc("GET /tickets", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("protected")) })

	for _, tt := range []struct {
		name   string
		cookie string
	}{
		{"expired", "tkt_session=expired-token"},
		{"forged", "tkt_session=never-issued"},
		{"garbage", "tkt_session="},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rec := doRequest(mux, mw, http.MethodGet, "/tickets", map[string]string{"Cookie": tt.cookie})
			wantRedirect(t, rec, http.StatusSeeOther, "/login")
		})
	}
}

// TestMiddlewareEmptyUsersRedirectsToSetup proves the D16 bootstrap gate:
// with an empty users table every protected route redirects 303 to /setup.
func TestMiddlewareEmptyUsersRedirectsToSetup(t *testing.T) {
	s := openTestStore(t)
	mw := NewSessionMiddleware(s.SessionStore(), s.UserStore(), s.SettingsStore())
	mux := http.NewServeMux()
	mux.HandleFunc("GET /tickets", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("protected")) })
	mux.HandleFunc("GET /users", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("users")) })

	for _, target := range []string{"/tickets", "/users"} {
		rec := doRequest(mux, mw, http.MethodGet, target, nil)
		wantRedirect(t, rec, http.StatusSeeOther, "/setup")
	}
}

// TestMiddlewareSetupExemptWithEmptyUsers proves /setup stays reachable when
// the users table is empty (first-user bootstrap flow).
func TestMiddlewareSetupExemptWithEmptyUsers(t *testing.T) {
	s := openTestStore(t)
	mw := NewSessionMiddleware(s.SessionStore(), s.UserStore(), s.SettingsStore())
	mux := http.NewServeMux()
	mux.HandleFunc("GET /setup", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("setup-form")) })

	rec := doRequest(mux, mw, http.MethodGet, "/setup", nil)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (setup must be reachable)", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "setup-form") {
		t.Errorf("body = %q, want the setup stub to run", rec.Body.String())
	}
}

// TestMiddlewareSetupUnavailableWithUsers proves the bootstrap flow is not
// available once at least one user exists (bootstrap-unavailable spec):
// anonymous visitors are directed to login, authed users to tickets.
func TestMiddlewareSetupUnavailableWithUsers(t *testing.T) {
	s := openTestStore(t)
	user := seedUser(t, s, "Ana", "ana@example.com")
	session := seedSession(t, s, user.ID)
	mw := NewSessionMiddleware(s.SessionStore(), s.UserStore(), s.SettingsStore())
	mux := http.NewServeMux()
	mux.HandleFunc("GET /setup", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("setup-form")) })

	t.Run("anonymous to login", func(t *testing.T) {
		rec := doRequest(mux, mw, http.MethodGet, "/setup", nil)
		wantRedirect(t, rec, http.StatusSeeOther, "/login")
	})
	t.Run("authed to tickets", func(t *testing.T) {
		rec := doRequest(mux, mw, http.MethodGet, "/setup", map[string]string{"Cookie": "tkt_session=" + session.ID})
		wantRedirect(t, rec, http.StatusSeeOther, "/tickets")
	})
}

// TestMiddlewareValidSessionReachesHandler proves a valid cookie resolves the
// session and the user flows into the handler via request context.
func TestMiddlewareValidSessionReachesHandler(t *testing.T) {
	s := openTestStore(t)
	user := seedUser(t, s, "Ana", "ana@example.com")
	session := seedSession(t, s, user.ID)
	mw := NewSessionMiddleware(s.SessionStore(), s.UserStore(), s.SettingsStore())
	mux := http.NewServeMux()
	mux.HandleFunc("GET /tickets", func(w http.ResponseWriter, r *http.Request) {
		u := userFromContext(r.Context())
		if u == nil {
			http.Error(w, "no user in context", http.StatusInternalServerError)
			return
		}
		w.Write([]byte("ok:" + u.Name + ":" + u.Email))
	})

	rec := doRequest(mux, mw, http.MethodGet, "/tickets", map[string]string{"Cookie": "tkt_session=" + session.ID})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if got := rec.Body.String(); got != "ok:Ana:ana@example.com" {
		t.Errorf("body = %q, want the session user in context", got)
	}
}

// TestMiddlewareDeactivatedUserSessionKilled proves D14: a deactivated
// user's session is rejected AND the session row is destroyed, so the next
// request is logged out.
func TestMiddlewareDeactivatedUserSessionKilled(t *testing.T) {
	s := openTestStore(t)
	user := seedUser(t, s, "Ana", "ana@example.com")
	session := seedSession(t, s, user.ID)
	// Deactivate through the store (the deactivation handler path is 5.6).
	user.Active = false
	if err := s.UserStore().Update(context.Background(), user); err != nil {
		t.Fatalf("deactivate user: %v", err)
	}
	mw := NewSessionMiddleware(s.SessionStore(), s.UserStore(), s.SettingsStore())
	mux := http.NewServeMux()
	mux.HandleFunc("GET /tickets", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("protected")) })

	rec := doRequest(mux, mw, http.MethodGet, "/tickets", map[string]string{"Cookie": "tkt_session=" + session.ID})

	wantRedirect(t, rec, http.StatusSeeOther, "/login")
	_, err := s.SessionStore().GetByID(context.Background(), session.ID)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("session must be deleted after deactivation, GetByID err = %v (want ErrNotFound)", err)
	}
}

// TestMiddlewareAuthedUserOnLoginRedirectsToTickets proves an authenticated
// user hitting /login is sent to /tickets instead of the login form.
func TestMiddlewareAuthedUserOnLoginRedirectsToTickets(t *testing.T) {
	s := openTestStore(t)
	user := seedUser(t, s, "Ana", "ana@example.com")
	session := seedSession(t, s, user.ID)
	mw := NewSessionMiddleware(s.SessionStore(), s.UserStore(), s.SettingsStore())
	mux := http.NewServeMux()
	mux.HandleFunc("GET /login", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("login-form")) })

	rec := doRequest(mux, mw, http.MethodGet, "/login", map[string]string{"Cookie": "tkt_session=" + session.ID})
	wantRedirect(t, rec, http.StatusSeeOther, "/tickets")

	// Without a session the login form is served.
	anon := doRequest(mux, mw, http.MethodGet, "/login", nil)
	if anon.Code != http.StatusOK || !strings.Contains(anon.Body.String(), "login-form") {
		t.Errorf("anonymous /login must reach the handler, got %d %q", anon.Code, anon.Body.String())
	}
}

// TestMiddlewareHealthzExempt proves /healthz needs no session.
func TestMiddlewareHealthzExempt(t *testing.T) {
	s := openTestStore(t)
	mw := NewSessionMiddleware(s.SessionStore(), s.UserStore(), s.SettingsStore())
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })

	rec := doRequest(mux, mw, http.MethodGet, "/healthz", nil)
	if rec.Code != http.StatusOK || rec.Body.String() != "ok" {
		t.Errorf("healthz = %d %q, want 200 ok", rec.Code, rec.Body.String())
	}
}

// wantHXRedirect asserts the HTMX refusal contract (issue #347): HX-Redirect
// names the page htmx must navigate to, the answer is an empty 204 — never a
// 3xx, which htmx follows and swaps — and nothing else rides along that could
// be injected into the requesting target.
func wantHXRedirect(t *testing.T, rec *httptest.ResponseRecorder, wantTarget string) {
	t.Helper()
	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want %d (htmx must never follow a 3xx here)", rec.Code, http.StatusNoContent)
	}
	if got := rec.Header().Get("HX-Redirect"); got != wantTarget {
		t.Errorf("HX-Redirect = %q, want %q", got, wantTarget)
	}
	if got := rec.Header().Get("Location"); got != "" {
		t.Errorf("Location = %q, want none: a 3xx lets htmx swap the login document", got)
	}
	if body := rec.Body.String(); body != "" {
		t.Errorf("body = %q, want empty: nothing may be swappable", body)
	}
}

// TestMiddlewareHTMXUnauthenticatedRequestsGetHXRedirect proves issue #347: the
// session gate must not answer an HTMX request with a redirect. htmx's XHR
// follows a 3xx transparently and swaps the login document into the requesting
// target (a drawer, a list region, the ticket list), so the refusal has to be
// HX-Redirect — which htmx turns into a full navigation — with no swappable
// body. Every unauthenticated shape of the cookie is covered, on a safe GET and
// on an unsafe POST, for a plain, boosted, and history-restore request (htmx
// marks both of the latter with HX-Request as well).
func TestMiddlewareHTMXUnauthenticatedRequestsGetHXRedirect(t *testing.T) {
	s := openTestStore(t)
	user := seedUser(t, s, "Ana", "ana@example.com")
	expired := &domain.Session{ID: "expired-token", UserID: user.ID, ExpiresAt: time.Now().Add(-time.Hour)}
	if err := s.SessionStore().Create(context.Background(), expired); err != nil {
		t.Fatalf("seed expired session: %v", err)
	}
	mw := NewSessionMiddleware(s.SessionStore(), s.UserStore(), s.SettingsStore())
	mux := http.NewServeMux()
	mux.HandleFunc("GET /tickets", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("protected")) })
	mux.HandleFunc("POST /tickets", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("created")) })

	for _, tt := range []struct {
		name   string
		cookie string
	}{
		{"no cookie", ""},
		{"unknown token", "tkt_session=never-issued"},
		{"expired session", "tkt_session=expired-token"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			for _, hx := range []struct {
				name string
				hdr  map[string]string
			}{
				{"plain", map[string]string{"HX-Request": "true"}},
				// A boosted link replaces the whole page, so a refusal there
				// needs the same full navigation.
				{"boosted", map[string]string{"HX-Request": "true", "HX-Boosted": "true"}},
				// History restore: htmx sets HX-Request too, so one
				// detection covers it and the refusal never embeds login
				// markup in the restored fragment.
				{"history restore", map[string]string{"HX-Request": "true", "HX-History-Restore-Request": "true"}},
			} {
				t.Run(hx.name+" GET", func(t *testing.T) {
					hdr := map[string]string{"Cookie": tt.cookie}
					for k, v := range hx.hdr {
						hdr[k] = v
					}
					rec := doRequest(mux, mw, http.MethodGet, "/tickets", hdr)
					wantHXRedirect(t, rec, "/login")
				})
			}

			// An unsafe HTMX request (a form post) is refused the same way; no
			// Origin header, so the D17 gate lets it through to the session
			// gate under test.
			t.Run("POST", func(t *testing.T) {
				rec := doRequest(mux, mw, http.MethodPost, "/tickets", map[string]string{
					"Cookie":     tt.cookie,
					"HX-Request": "true",
				})
				wantHXRedirect(t, rec, "/login")
			})
		})
	}
}

// TestMiddlewareHTMXEmptyUsersRedirectsToSetup proves the bootstrap gate keeps
// its /setup target for HTMX requests: a fresh instance with no users sends an
// HTMX interaction to /setup with a full navigation, never 303.
func TestMiddlewareHTMXEmptyUsersRedirectsToSetup(t *testing.T) {
	s := openTestStore(t)
	mw := NewSessionMiddleware(s.SessionStore(), s.UserStore(), s.SettingsStore())
	mux := http.NewServeMux()
	mux.HandleFunc("GET /tickets", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("protected")) })
	mux.HandleFunc("GET /users", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("users")) })

	for _, target := range []string{"/tickets", "/users"} {
		t.Run(target, func(t *testing.T) {
			rec := doRequest(mux, mw, http.MethodGet, target, map[string]string{"HX-Request": "true"})
			wantHXRedirect(t, rec, "/setup")
		})
	}
}

// TestMiddlewareHTMXSetupUnavailableRedirectsToLogin proves the two refusals on
// /setup once users exist keep their targets for HTMX requests: an anonymous
// HTMX request goes to /login, while an authenticated one keeps today's
// 303 to /tickets (authenticated redirects are out of #347's scope).
func TestMiddlewareHTMXSetupUnavailableRedirectsToLogin(t *testing.T) {
	s := openTestStore(t)
	user := seedUser(t, s, "Ana", "ana@example.com")
	session := seedSession(t, s, user.ID)
	mw := NewSessionMiddleware(s.SessionStore(), s.UserStore(), s.SettingsStore())
	mux := http.NewServeMux()
	mux.HandleFunc("GET /setup", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("setup-form")) })

	t.Run("anonymous htmx to login", func(t *testing.T) {
		rec := doRequest(mux, mw, http.MethodGet, "/setup", map[string]string{"HX-Request": "true"})
		wantHXRedirect(t, rec, "/login")
	})
	t.Run("authed htmx keeps the tickets redirect", func(t *testing.T) {
		rec := doRequest(mux, mw, http.MethodGet, "/setup", map[string]string{
			"HX-Request": "true",
			"Cookie":     "tkt_session=" + session.ID,
		})
		wantRedirect(t, rec, http.StatusSeeOther, "/tickets")
	})
}

// TestMiddlewareHTMXKilledSessionRedirectsToLogin proves the two branches that
// destroy a session on sight — a vanished user row and a deactivated user
// (D14) — refuse an HTMX request the same way, and still delete the row.
func TestMiddlewareHTMXKilledSessionRedirectsToLogin(t *testing.T) {
	t.Run("deactivated user", func(t *testing.T) {
		s := openTestStore(t)
		user := seedUser(t, s, "Ana", "ana@example.com")
		session := seedSession(t, s, user.ID)
		user.Active = false
		if err := s.UserStore().Update(context.Background(), user); err != nil {
			t.Fatalf("deactivate user: %v", err)
		}
		mw := NewSessionMiddleware(s.SessionStore(), s.UserStore(), s.SettingsStore())
		mux := http.NewServeMux()
		mux.HandleFunc("GET /tickets", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("protected")) })

		rec := doRequest(mux, mw, http.MethodGet, "/tickets", map[string]string{
			"HX-Request": "true",
			"Cookie":     "tkt_session=" + session.ID,
		})
		wantHXRedirect(t, rec, "/login")
		if _, err := s.SessionStore().GetByID(context.Background(), session.ID); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("session must be deleted after deactivation, GetByID err = %v (want ErrNotFound)", err)
		}
	})

	t.Run("vanished user row", func(t *testing.T) {
		dbPath := t.TempDir() + "/app.db"
		s := openTestStoreAt(t, dbPath)
		user := seedUser(t, s, "Ana", "ana@example.com")
		session := seedSession(t, s, user.ID)
		// Remove the user row and leave the session behind: the state the gate
		// must treat as logged out. A second handle enforces no foreign keys,
		// which is the only way that state can exist — the store's Delete drops
		// the sessions in the same transaction, so production reaches this
		// branch through a delete racing an in-flight request.
		db, err := sql.Open("sqlite", "file:"+dbPath)
		if err != nil {
			t.Fatalf("open raw db: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		if _, err := db.Exec(`DELETE FROM users WHERE id = ?`, user.ID); err != nil {
			t.Fatalf("delete user row: %v", err)
		}
		mw := NewSessionMiddleware(s.SessionStore(), s.UserStore(), s.SettingsStore())
		mux := http.NewServeMux()
		mux.HandleFunc("GET /tickets", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("protected")) })

		rec := doRequest(mux, mw, http.MethodGet, "/tickets", map[string]string{
			"HX-Request": "true",
			"Cookie":     "tkt_session=" + session.ID,
		})
		wantHXRedirect(t, rec, "/login")
		if _, err := s.SessionStore().GetByID(context.Background(), session.ID); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("session must be deleted for a vanished user, GetByID err = %v (want ErrNotFound)", err)
		}
	})
}

// TestMiddlewareNonHTMXRefusalKeepsSeeOther is the preserve half of issue #347:
// an ordinary navigation or form post the gate refuses keeps the exact 303 See
// Other it has always answered — same status, same Location, and the same small
// body http.Redirect writes for a GET before the browser follows.
func TestMiddlewareNonHTMXRefusalKeepsSeeOther(t *testing.T) {
	s := openTestStore(t)
	seedUser(t, s, "Ana", "ana@example.com")
	mw := NewSessionMiddleware(s.SessionStore(), s.UserStore(), s.SettingsStore())
	mux := http.NewServeMux()
	mux.HandleFunc("GET /tickets", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("protected")) })
	mux.HandleFunc("POST /tickets", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("created")) })

	t.Run("GET navigation", func(t *testing.T) {
		rec := doRequest(mux, mw, http.MethodGet, "/tickets", nil)
		wantRedirect(t, rec, http.StatusSeeOther, "/login")
		if got := rec.Header().Get("HX-Redirect"); got != "" {
			t.Errorf("HX-Redirect = %q, want none for an ordinary navigation", got)
		}
		if got, want := rec.Body.String(), "<a href=\"/login\">See Other</a>.\n\n"; got != want {
			t.Errorf("body = %q, want %q", got, want)
		}
		if got, want := rec.Header().Get("Content-Type"), "text/html; charset=utf-8"; got != want {
			t.Errorf("Content-Type = %q, want %q", got, want)
		}
	})
	t.Run("POST form post", func(t *testing.T) {
		rec := doRequest(mux, mw, http.MethodPost, "/tickets", nil)
		wantRedirect(t, rec, http.StatusSeeOther, "/login")
		if got := rec.Header().Get("HX-Redirect"); got != "" {
			t.Errorf("HX-Redirect = %q, want none for an ordinary form post", got)
		}
		if body := rec.Body.String(); body != "" {
			t.Errorf("body = %q, want empty for a POST redirect", body)
		}
	})
}

// TestMiddlewareCrossSiteOriginPOSTRejected proves D17: a POST carrying a
// cross-site Origin header is rejected with 403 before any handler runs —
// even on exempt routes (login CSRF).
func TestMiddlewareCrossSiteOriginPOSTRejected(t *testing.T) {
	s := openTestStore(t)
	seedUser(t, s, "Ana", "ana@example.com")
	mw := NewSessionMiddleware(s.SessionStore(), s.UserStore(), s.SettingsStore())
	mux := http.NewServeMux()
	mux.HandleFunc("POST /tickets", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("created")) })
	mux.HandleFunc("POST /login", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("login")) })

	for _, target := range []string{"/tickets", "/login"} {
		t.Run(target, func(t *testing.T) {
			rec := doRequest(mux, mw, http.MethodPost, target, map[string]string{"Origin": "https://evil.example"})
			if rec.Code != http.StatusForbidden {
				t.Errorf("cross-site POST %s status = %d, want 403", target, rec.Code)
			}
		})
	}
	t.Run("malformed origin", func(t *testing.T) {
		rec := doRequest(mux, mw, http.MethodPost, "/tickets", map[string]string{"Origin": ":::not-a-url:::"})
		if rec.Code != http.StatusForbidden {
			t.Errorf("malformed Origin status = %d, want 403", rec.Code)
		}
	})
	// Issue #347 preserve: an HTMX request carrying a cross-site Origin is
	// still refused 403 by the D17 gate, which answers before the session gate
	// and therefore before any HX-Redirect answer exists.
	t.Run("cross-site POST with HX-Request", func(t *testing.T) {
		rec := doRequest(mux, mw, http.MethodPost, "/tickets", map[string]string{
			"Origin":     "https://evil.example",
			"HX-Request": "true",
		})
		if rec.Code != http.StatusForbidden {
			t.Errorf("cross-site HTMX POST status = %d, want 403", rec.Code)
		}
		if got := rec.Header().Get("HX-Redirect"); got != "" {
			t.Errorf("cross-site HTMX POST HX-Redirect = %q, want none (the Origin gate answers first)", got)
		}
	})
}

// TestMiddlewareSameOriginPOSTAllowed proves same-origin and header-less
// POSTs pass the D17 gate.
func TestMiddlewareSameOriginPOSTAllowed(t *testing.T) {
	s := openTestStore(t)
	mw := NewSessionMiddleware(s.SessionStore(), s.UserStore(), s.SettingsStore())
	mux := http.NewServeMux()
	mux.HandleFunc("POST /tickets", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("created")) })

	t.Run("same origin", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "http://tkt.test/tickets", nil)
		req.Header.Set("Origin", "http://tkt.test")
		rec := httptest.NewRecorder()
		mw.Wrap(mux).ServeHTTP(rec, req)
		if rec.Code != http.StatusSeeOther && rec.Code != http.StatusOK {
			t.Errorf("same-origin POST status = %d, want the request to proceed", rec.Code)
		}
	})
	t.Run("no origin header", func(t *testing.T) {
		rec := doRequest(mux, mw, http.MethodPost, "/tickets", nil)
		if rec.Code == http.StatusForbidden {
			t.Errorf("POST without Origin must not be rejected, got 403")
		}
	})
}

// TestMiddlewareCrossSiteOriginUnsafeMethodsRejected proves the D17 gate
// covers every unsafe method, not only POST (#215): PUT, PATCH, and DELETE
// carrying a cross-site Origin are refused 403 before any handler runs,
// while an absent Origin still passes for non-browser clients.
func TestMiddlewareCrossSiteOriginUnsafeMethodsRejected(t *testing.T) {
	s := openTestStore(t)
	user := seedUser(t, s, "Ana", "ana@example.com")
	session := seedSession(t, s, user.ID)
	mw := NewSessionMiddleware(s.SessionStore(), s.UserStore(), s.SettingsStore())
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /tickets/{id}", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("updated")) })
	mux.HandleFunc("PATCH /tickets/{id}", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("patched")) })
	mux.HandleFunc("DELETE /tickets/{id}", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("deleted")) })

	for _, method := range []string{http.MethodPut, http.MethodPatch, http.MethodDelete} {
		t.Run(method+" cross-site refused", func(t *testing.T) {
			rec := doRequest(mux, mw, method, "/tickets/1", map[string]string{
				"Origin": "https://evil.example",
				"Cookie": "tkt_session=" + session.ID,
			})
			if rec.Code != http.StatusForbidden {
				t.Errorf("cross-site %s status = %d, want 403", method, rec.Code)
			}
		})
		t.Run(method+" missing origin proceeds", func(t *testing.T) {
			rec := doRequest(mux, mw, method, "/tickets/1", map[string]string{"Cookie": "tkt_session=" + session.ID})
			if rec.Code != http.StatusOK {
				t.Errorf("%s without Origin status = %d, want 200 (non-browser clients must pass)", method, rec.Code)
			}
		})
	}
	t.Run("malformed origin", func(t *testing.T) {
		rec := doRequest(mux, mw, http.MethodDelete, "/tickets/1", map[string]string{"Origin": ":::not-a-url:::"})
		if rec.Code != http.StatusForbidden {
			t.Errorf("malformed Origin status = %d, want 403", rec.Code)
		}
	})
}

// TestMiddlewareSafeMethodsWithOriginIgnored proves the Origin gate only
// applies to unsafe methods: the safe allow-list (GET, HEAD, OPTIONS, TRACE)
// passes through even with a cross-site Origin header.
func TestMiddlewareSafeMethodsWithOriginIgnored(t *testing.T) {
	s := openTestStore(t)
	seedUser(t, s, "Ana", "ana@example.com")
	mw := NewSessionMiddleware(s.SessionStore(), s.UserStore(), s.SettingsStore())
	mux := http.NewServeMux()
	mux.HandleFunc("GET /login", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("login-form")) })
	mux.HandleFunc("OPTIONS /login", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("options")) })
	mux.HandleFunc("TRACE /login", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("trace")) })

	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace} {
		t.Run(method, func(t *testing.T) {
			rec := doRequest(mux, mw, method, "/login", map[string]string{"Origin": "https://evil.example"})
			if rec.Code != http.StatusOK {
				t.Errorf("%s with cross-site Origin must be ignored, got %d", method, rec.Code)
			}
		})
	}
}

// TestMiddlewareSessionStoreFailure500 proves an operational session-store
// failure answers 500 — never a misleading login redirect for a valid user.
func TestMiddlewareSessionStoreFailure500(t *testing.T) {
	s := openTestStore(t)
	mw := NewSessionMiddleware(failingSessionStore{}, s.UserStore(), s.SettingsStore())
	mux := http.NewServeMux()
	mux.HandleFunc("GET /tickets", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("protected")) })

	rec := doRequest(mux, mw, http.MethodGet, "/tickets", map[string]string{"Cookie": "tkt_session=any"})

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("session-store failure status = %d, want 500", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "" {
		t.Errorf("session-store failure must not redirect, got Location %q", loc)
	}
}

// TestMiddlewareUserStoreFailure500 proves an operational user-store failure
// answers 500 for an authenticated request — never a login redirect.
func TestMiddlewareUserStoreFailure500(t *testing.T) {
	s := openTestStore(t)
	user := seedUser(t, s, "Ana", "ana@example.com")
	session := seedSession(t, s, user.ID)
	mw := NewSessionMiddleware(s.SessionStore(), failingUserStore{}, s.SettingsStore())
	mux := http.NewServeMux()
	mux.HandleFunc("GET /tickets", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("protected")) })

	rec := doRequest(mux, mw, http.MethodGet, "/tickets", map[string]string{"Cookie": "tkt_session=" + session.ID})

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("user-store failure status = %d, want 500", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "" {
		t.Errorf("user-store failure must not redirect, got Location %q", loc)
	}
}

// TestMiddlewareStaticBypass proves vendored static assets load without a
// session: htmx must reach the login/setup pages and every authed page, and
// the middleware must never redirect it.
func TestMiddlewareStaticBypass(t *testing.T) {
	s := openTestStore(t)
	mw := NewSessionMiddleware(s.SessionStore(), s.UserStore(), s.SettingsStore())
	mux := http.NewServeMux()
	RegisterStatic(mux, nil)
	mux.HandleFunc("GET /tickets", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("protected")) })

	rec := doRequest(mux, mw, http.MethodGet, "/static/htmx.min.js", nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("static status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/javascript; charset=utf-8" {
		t.Errorf("Content-Type = %q, want text/javascript; charset=utf-8", ct)
	}
	if body := rec.Body.String(); !strings.Contains(body, "htmx") {
		t.Errorf("static body must contain htmx, got %d bytes", len(body))
	}
}
