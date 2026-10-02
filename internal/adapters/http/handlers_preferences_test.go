package httpadapter

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// Per-user preferences (issue #210): GET/POST /preferences is reachable by
// every authenticated role (admin, agent, requester), an anonymous request
// is bounced to /login, a valid queue order is persisted and becomes the
// list's default, an explicit `sort` still wins, an unknown stored value
// fails closed to the newest-first default, and the requester's own list is
// deliberately unaffected.

// seedStoredQueueOrder writes one (user_id, queue_order) row directly, the
// way a hand-edited or legacy database could, so the read path is exercised
// independently of the write route.
func seedStoredQueueOrder(t *testing.T, h *harness, userID int64, order string) {
	t.Helper()
	if _, err := h.rawDB(t).Exec(
		`INSERT INTO user_preferences (user_id, "key", value) VALUES (?, 'queue_order', ?)
		 ON CONFLICT(user_id, "key") DO UPDATE SET value = excluded.value`,
		userID, order); err != nil {
		t.Fatalf("seed stored queue order: %v", err)
	}
}

// appearsBefore reports whether first occurs before second in body; either
// being absent is a failure of the assertion, not a false positive.
func appearsBefore(body, first, second string) bool {
	i, j := strings.Index(body, first), strings.Index(body, second)
	return i >= 0 && j >= 0 && i < j
}

// TestPreferencesPageHasNoTrailingWhitespace keeps the new page on the same
// whitespace contract the design-system check enforces for every other page:
// no whitespace-only line, no trailing space or tab.
func TestPreferencesPageHasNoTrailingWhitespace(t *testing.T) {
	h := newHarness(t)
	body := h.get(t, "/preferences", false).Body.String()
	for lineNumber, line := range strings.Split(body, "\n") {
		if (line != "" && strings.TrimSpace(line) == "") || strings.HasSuffix(line, " ") || strings.HasSuffix(line, "\t") {
			t.Errorf("preferences line %d has whitespace: %q", lineNumber+1, line)
		}
	}
}

// TestPreferencesRequireAuthentication proves the preferences page is behind
// the session gate (D14): an anonymous GET and POST both land on /login.
func TestPreferencesRequireAuthentication(t *testing.T) {
	h := newHarness(t)

	rec := doRequest(h.mux, h.mw, http.MethodGet, "/preferences", nil)
	wantRedirect(t, rec, http.StatusSeeOther, "/login")

	rec = doRequest(h.mux, h.mw, http.MethodPost, "/preferences", nil)
	wantRedirect(t, rec, http.StatusSeeOther, "/login")
}

// TestPreferencesReachableByEveryAuthenticatedRole proves the page is not
// capability-gated: admin, agent, and requester all render it.
func TestPreferencesReachableByEveryAuthenticatedRole(t *testing.T) {
	h := newHarness(t)

	agent := seedUserRole(t, h.store, "Prefs agent", "prefs-agent@tkt.test", domain.RoleAgent)
	agentSession := seedSession(t, h.store, agent.ID)

	requester, err := h.users.Create(t.Context(), *h.admin, application.CreateUserInput{
		Name: "Prefs requester", Email: "prefs-requester@tkt.test", Password: "secret",
	})
	if err != nil {
		t.Fatalf("create requester: %v", err)
	}
	requesterSession := h.loginCookie(t, requester.Email, "secret")
	if requesterSession == "" {
		t.Fatal("requester login must succeed")
	}

	for _, tc := range []struct {
		name    string
		session string
	}{
		{name: "admin", session: h.adminSession.ID},
		{name: "agent", session: agentSession.ID},
		{name: "user", session: requesterSession},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := doRequest(h.mux, h.mw, http.MethodGet, "/preferences",
				map[string]string{"Cookie": sessionCookie + "=" + tc.session})
			if rec.Code != http.StatusOK {
				t.Fatalf("GET /preferences as %s = %d, want 200: %s", tc.name, rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), "Queue order") {
				t.Errorf("GET /preferences as %s must render the queue order control", tc.name)
			}
		})
	}
}

// TestPreferencesSavePersistsAndListUsesDefault proves the write path, the
// read-back, and the list behavior in one journey: with no preference the
// queue is newest-first, after saving `priority` the queue orders critical
// work first, and an explicit `sort=newest` still beats the stored default.
func TestPreferencesSavePersistsAndListUsesDefault(t *testing.T) {
	h := newHarness(t)

	oldCritical := h.seedTicket(t, "Older critical work", func(in *application.CreateTicketInput) {
		in.Priority = domain.PriorityCritical
	})
	newLow := h.seedTicket(t, "Newer low work", func(in *application.CreateTicketInput) {
		in.Priority = domain.PriorityLow
	})

	if body := h.get(t, "/tickets", false).Body.String(); !appearsBefore(body, newLow.Title, oldCritical.Title) {
		t.Fatalf("no stored preference must keep newest-first order, got: %s", body)
	}

	rec := h.postForm(t, "/preferences", url.Values{"queue_order": {"priority"}}, false)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /preferences = %d, want %d: %s", rec.Code, http.StatusSeeOther, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "/preferences" {
		t.Errorf("Location = %q, want /preferences", loc)
	}

	if body := h.get(t, "/preferences", false).Body.String(); !strings.Contains(body, `value="priority" selected`) {
		t.Errorf("saved order must round-trip into the page, got: %s", body)
	}

	body := h.get(t, "/tickets", false).Body.String()
	if !appearsBefore(body, oldCritical.Title, newLow.Title) {
		t.Errorf("stored priority default must order critical first, got: %s", body)
	}
	if !strings.Contains(body, `value="priority" selected`) {
		t.Errorf("the applied default must mark the order control, got: %s", body)
	}

	explicit := h.get(t, "/tickets?sort=newest", false).Body.String()
	if !appearsBefore(explicit, newLow.Title, oldCritical.Title) {
		t.Errorf("?sort=newest must beat the stored default, got: %s", explicit)
	}

	// Paging to page 2 preserves the active (default) order.
	for i := 0; i < 11; i++ {
		h.seedTicket(t, "Paged ticket "+strconv.Itoa(i), nil)
	}
	paged := h.get(t, "/tickets", false).Body.String()
	if !strings.Contains(paged, "page=2&amp;sort=priority") {
		t.Errorf("the default order must survive paging, got: %s", paged)
	}
}

// TestPreferencesRejectsUnknownOrder proves fail-closed validation: an
// unknown submission re-renders with the error banner and stores nothing.
func TestPreferencesRejectsUnknownOrder(t *testing.T) {
	h := newHarness(t)

	rec := h.postForm(t, "/preferences", url.Values{"queue_order": {"bogus"}}, false)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("POST unknown order = %d, want 422: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `role="alert"`) {
		t.Errorf("rejected save must render an error banner, got: %s", body)
	}
	if !strings.Contains(body, `value="newest" selected`) {
		t.Errorf("rejected save must re-select the stored default, got: %s", body)
	}

	after := h.get(t, "/preferences", false).Body.String()
	if !strings.Contains(after, `value="newest" selected`) {
		t.Errorf("nothing may be stored after a rejection, got: %s", after)
	}
}

// TestPreferencesUnknownStoredValueFailsClosed proves a stored value outside
// the closed set never reaches the query order: the list stays newest-first
// and the page renders the default selection.
func TestPreferencesUnknownStoredValueFailsClosed(t *testing.T) {
	h := newHarness(t)

	oldCritical := h.seedTicket(t, "Stored old critical", func(in *application.CreateTicketInput) {
		in.Priority = domain.PriorityCritical
	})
	newLow := h.seedTicket(t, "Stored new low", func(in *application.CreateTicketInput) {
		in.Priority = domain.PriorityLow
	})
	seedStoredQueueOrder(t, h, h.admin.ID, "bogus")

	if body := h.get(t, "/tickets", false).Body.String(); !appearsBefore(body, newLow.Title, oldCritical.Title) {
		t.Errorf("unknown stored order must fail closed to newest-first, got: %s", body)
	}
	if body := h.get(t, "/preferences", false).Body.String(); !strings.Contains(body, `value="newest" selected`) {
		t.Errorf("unknown stored order must render as the newest default, got: %s", body)
	}
}

// TestPreferencesDoNotAffectRequesterList proves the requester's list stays
// fixed to its newest-first active-work view regardless of the stored
// default: the queue preference is a staff surface.
func TestPreferencesDoNotAffectRequesterList(t *testing.T) {
	h := newHarness(t)

	requester, err := h.users.Create(t.Context(), *h.admin, application.CreateUserInput{
		Name: "Requester", Email: "requester-prefs-list@tkt.test", Password: "secret",
	})
	if err != nil {
		t.Fatalf("create requester: %v", err)
	}
	oldCritical := h.seedTicket(t, "Requester older critical", func(in *application.CreateTicketInput) {
		in.Priority = domain.PriorityCritical
	})
	newLow := h.seedTicket(t, "Requester newer low", func(in *application.CreateTicketInput) {
		in.Priority = domain.PriorityLow
	})
	db := h.rawDB(t)
	for _, tk := range []*domain.Ticket{oldCritical, newLow} {
		if _, err := db.Exec(`UPDATE tickets SET requester_user_id = ? WHERE id = ?`, requester.ID, tk.ID); err != nil {
			t.Fatalf("link requester to ticket %d: %v", tk.ID, err)
		}
	}
	seedStoredQueueOrder(t, h, requester.ID, "priority")

	session := h.loginCookie(t, requester.Email, "secret")
	if session == "" {
		t.Fatal("requester login must succeed")
	}
	body := doRequest(h.mux, h.mw, http.MethodGet, "/tickets",
		map[string]string{"Cookie": sessionCookie + "=" + session}).Body.String()
	if !appearsBefore(body, newLow.Title, oldCritical.Title) {
		t.Errorf("requester list must stay newest-first regardless of the stored default, got: %s", body)
	}
}
