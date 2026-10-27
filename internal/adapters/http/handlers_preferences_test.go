package httpadapter

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

func TestPreferencesLanguageControl(t *testing.T) {
	h := newHarness(t)
	body := h.get(t, "/preferences", false).Body.String()
	for _, want := range []string{`action="/preferences/language"`, `value="instance" selected`, `value="en"`, `value="es"`} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	for _, language := range []string{"en", "es", "instance"} {
		rec := h.postForm(t, "/preferences/language", url.Values{"language": {language}}, false)
		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/preferences" {
			t.Fatalf("save %q: %d %q", language, rec.Code, rec.Header().Get("Location"))
		}
		if body := h.get(t, "/preferences", false).Body.String(); !strings.Contains(body, `value="`+language+`" selected`) {
			t.Errorf("%q not selected", language)
		}
	}
	if rec := h.postForm(t, "/preferences/theme", url.Values{"theme": {"dark"}}, false); rec.Code != http.StatusSeeOther {
		t.Fatalf("theme save: %d", rec.Code)
	}
	for _, invalid := range []string{"", "bogus", "EN", "es "} {
		rec := h.postForm(t, "/preferences/language", url.Values{"language": {invalid}}, false)
		if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), `value="instance" selected`) || !strings.Contains(rec.Body.String(), "invalid language") {
			t.Errorf("invalid save %q: %d", invalid, rec.Code)
		}
	}
	if body := h.get(t, "/preferences", false).Body.String(); !strings.Contains(body, `value="instance" selected`) || !strings.Contains(body, `value="dark" selected`) {
		t.Error("invalid save changed preferences")
	}
	store := h.store.PreferencesStore().(application.LanguagePreferencesStore)
	if err := store.SetLanguage(t.Context(), h.admin.ID, "future"); err != nil {
		t.Fatal(err)
	}
	if body := h.get(t, "/preferences", false).Body.String(); !strings.Contains(body, `value="instance" selected`) {
		t.Error("invalid stored language did not inherit")
	}
	other := seedUserRole(t, h.store, "Language user", "language-user@tkt.test", domain.RoleAgent)
	if err := store.SetLanguage(t.Context(), other.ID, "es"); err != nil {
		t.Fatal(err)
	}
	if body := h.get(t, "/preferences", false).Body.String(); !strings.Contains(body, `value="instance" selected`) {
		t.Error("another user changed selection")
	}
}

// Per-user preferences (issue #210): GET/POST /preferences is reachable by
// every authenticated role (admin, agent, requester), an anonymous request
// is bounced to /login, a valid queue order is persisted and becomes the
// list's default, an explicit `sort` still wins, an unknown stored value
// fails closed to the newest-first default, and the requester's own list is
// deliberately unaffected.

func TestQueueOrderOnTickets(t *testing.T) {
	h := newHarness(t)
	body := h.get(t, "/tickets", false).Body.String()
	if !strings.Contains(body, `action="/preferences/queue-order"`) {
		t.Error("tickets must offer the default queue order form")
	}
	for _, want := range []string{`<label for="queue-default-order">Default sorting</label>`, `<select id="queue-default-order" name="queue_order">`, `<option value="newest" selected>`, `<noscript>`, `type="submit"`} {
		if !strings.Contains(body, want) {
			t.Errorf("default order control must contain %q", want)
		}
	}
	if strings.Contains(body, `class="queue-default-menu"`) || strings.Contains(body, `type="radio" name="queue_order"`) {
		t.Error("default order must use a select rather than a radio menu")
	}
	if strings.Contains(h.get(t, "/preferences", false).Body.String(), `name="queue_order"`) {
		t.Error("queue order must move out of preferences")
	}
	rec := h.postForm(t, "/preferences/queue-order", url.Values{"queue_order": {"priority"}}, false)
	wantRedirect(t, rec, http.StatusSeeOther, "/tickets")
	if got := storedQueueOrder(t, h, h.admin.ID); got != "priority" {
		t.Errorf("stored queue order = %q, want priority", got)
	}
	body = h.get(t, "/tickets?sort=newest", false).Body.String()
	if !strings.Contains(body, `<option value="priority" selected>`) || !strings.Contains(body, `value="newest" selected`) {
		t.Error("temporary sort must not change the displayed saved default")
	}
	rec = h.postForm(t, "/preferences/queue-order", url.Values{"queue_order": {"bogus"}}, false)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("invalid order status = %d, want 422", rec.Code)
	}
	if got := storedQueueOrder(t, h, h.admin.ID); got != "priority" {
		t.Errorf("invalid order changed stored default to %q", got)
	}
	anon := doRequest(h.mux, h.mw, http.MethodPost, "/preferences/queue-order", nil)
	wantRedirect(t, anon, http.StatusSeeOther, "/login")
}

func TestQueueOrderSavePreservesSafeTicketContext(t *testing.T) {
	h := newHarness(t)
	old := h.seedTicket(t, "Context older critical", func(in *application.CreateTicketInput) {
		in.Priority = domain.PriorityCritical
	})
	newer := h.seedTicket(t, "Context newer low", func(in *application.CreateTicketInput) {
		in.Priority = domain.PriorityLow
	})
	body := h.get(t, "/tickets?q=printer&sort=newest&view=mine&priority=high", false).Body.String()
	if !strings.Contains(body, `name="return"`) || !strings.Contains(body, `sort=newest`) {
		t.Error("form must carry the current ticket context, including explicit newest")
	}
	for _, tc := range []struct{ input, want string }{
		{"/tickets?q=printer&sort=newest&view=mine&priority=high", "/tickets?priority=high&q=printer&sort=newest&view=mine"},
		{"/tickets?q=printer&sort=urgency&page=2", "/tickets?page=2&q=printer&sort=urgency"},
		{"/tickets?assigned_page=2&done_page=3&claimable_page=4", "/tickets?assigned_page=2&claimable_page=4&done_page=3"},
		{"//evil.example/tickets?sort=priority", "/tickets"},
		{"https://evil.example/tickets?q=x", "/tickets"},
		{"/tickets/42?q=x", "/tickets"},
		{"/tickets%0d%0aLocation:%20https://evil.example", "/tickets"},
		{"/tickets?sort=bogus&view=bogus&evil=x", "/tickets?sort=newest"},
		{"/tickets?sort=", "/tickets?sort=newest"},
	} {
		t.Run(tc.input, func(t *testing.T) {
			if tc.want == "/tickets?sort=newest" {
				initial := h.get(t, tc.input, false).Body.String()
				if !strings.Contains(initial, `name="return" value="/tickets?sort=newest"`) {
					t.Error("the native form must preserve the effective explicit sort")
				}
			}
			rec := h.postForm(t, "/preferences/queue-order", url.Values{"queue_order": {"priority"}, "return": {tc.input}}, false)
			wantRedirect(t, rec, http.StatusSeeOther, tc.want)
			if tc.want == "/tickets?sort=newest" {
				body := h.get(t, tc.want, false).Body.String()
				if !strings.Contains(body, `<option value="priority" selected>`) || !strings.Contains(body, `value="newest" selected`) || !appearsBefore(body, newer.Title, old.Title) {
					t.Error("saved priority must not override the explicit newest list order")
				}
			}
		})
	}
}

// failingQueueOrderRead preserves the real store for writes but simulates an
// operational read failure at the preference port.
type failingQueueOrderRead struct{ application.PreferencesStore }

func (failingQueueOrderRead) GetQueueOrder(context.Context, int64) (string, error) {
	return "", errors.New("queue order unavailable")
}

func TestTicketsQueueOrderReadFailurePreservesListAvailability(t *testing.T) {
	h := newHarness(t)
	old := h.seedTicket(t, "Older critical failure test", func(in *application.CreateTicketInput) {
		in.Priority = domain.PriorityCritical
	})
	newer := h.seedTicket(t, "Newer low failure test", func(in *application.CreateTicketInput) {
		in.Priority = domain.PriorityLow
	})
	seedStoredQueueOrder(t, h, h.admin.ID, "urgency")
	preferences := application.NewPreferencesService(failingQueueOrderRead{h.store.PreferencesStore()}, h.store.EmailPreferences(), h.store.SidebarPreferences(), h.store.ThemePreferences())
	mux := http.NewServeMux()
	NewTicketHandlers(h.tickets, h.comments, h.search, h.categories, h.users, h.store.DeskStore(), h.workflows, application.NewWorkflowRunner(h.clock), h.store.WorkflowRunStore(), h.store.WorkflowUnitOfWork(), h.renderer).WithPreferences(preferences).WithClock(h.clock).Register(mux)
	NewPreferencesHandlers(preferences, h.renderer).Register(mux)

	requester := seedUserRole(t, h.store, "Queue requester", "queue-failure@tkt.test", domain.RoleUser)
	session := seedSession(t, h.store, requester.ID)
	for _, tc := range []struct {
		name, target, session string
	}{
		{"staff explicit sort", "/tickets?sort=priority", h.adminSession.ID},
		{"requester", "/tickets", session.ID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := doRequest(mux, h.mw, http.MethodGet, tc.target, map[string]string{"Cookie": sessionCookie + "=" + tc.session})
			if rec.Code != http.StatusOK {
				t.Fatalf("GET %s = %d, want 200: %s", tc.target, rec.Code, rec.Body.String())
			}
			body := rec.Body.String()
			if strings.Contains(body, `action="/preferences/queue-order"`) || strings.Contains(body, `name="queue_order"`) {
				t.Error("unavailable preference or requester list must not offer an unsafe queue order save")
			}
			if tc.name == "staff explicit sort" && (!strings.Contains(body, `value="priority" selected`) || !appearsBefore(body, old.Title, newer.Title)) {
				t.Error("explicit priority sort must still order the tickets")
			}
		})
	}
	req := httptest.NewRequest(http.MethodPost, "/preferences/queue-order", strings.NewReader("queue_order=newest"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Cookie", sessionCookie+"="+h.adminSession.ID)
	failedSave := httptest.NewRecorder()
	h.mw.Wrap(mux).ServeHTTP(failedSave, req)
	if failedSave.Code != http.StatusInternalServerError {
		t.Errorf("save while preference read unavailable = %d, want 500", failedSave.Code)
	}
	if got := storedQueueOrder(t, h, h.admin.ID); got != "urgency" {
		t.Errorf("failed read or save changed stored order to %q", got)
	}
	// Staff requests without an explicit sort already propagated a read failure.
	rec := doRequest(mux, h.mw, http.MethodGet, "/tickets", map[string]string{"Cookie": sessionCookie + "=" + h.adminSession.ID})
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("implicit staff order with unavailable storage = %d, want existing 500", rec.Code)
	}
}

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
		Name: "Prefs requester", Email: "prefs-requester@tkt.test", Password: "s3cret-pass-12",
	})
	if err != nil {
		t.Fatalf("create requester: %v", err)
	}
	requesterSession := h.loginCookie(t, requester.Email, "s3cret-pass-12")
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
			tickets := doRequest(h.mux, h.mw, http.MethodGet, "/tickets", map[string]string{"Cookie": sessionCookie + "=" + tc.session})
			hasMenu := strings.Contains(tickets.Body.String(), `action="/preferences/queue-order"`)
			if hasMenu != (tc.name != "user") {
				t.Errorf("GET /tickets as %s queue order control = %v", tc.name, hasMenu)
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

	if body := h.get(t, "/tickets", false).Body.String(); !strings.Contains(body, `<option value="priority" selected>`) {
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

	after := h.get(t, "/tickets", false).Body.String()
	if !strings.Contains(after, `<option value="newest" selected>`) {
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
	if body := h.get(t, "/tickets", false).Body.String(); !strings.Contains(body, `<option value="newest" selected>`) {
		t.Errorf("unknown stored order must render as the newest default, got: %s", body)
	}
}

// TestPreferencesDoNotAffectRequesterList proves the requester's list stays
// fixed to its newest-first active-work view regardless of the stored
// default: the queue preference is a staff surface.
func TestPreferencesDoNotAffectRequesterList(t *testing.T) {
	h := newHarness(t)

	requester, err := h.users.Create(t.Context(), *h.admin, application.CreateUserInput{
		Name: "Requester", Email: "requester-prefs-list@tkt.test", Password: "s3cret-pass-12",
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

	session := h.loginCookie(t, requester.Email, "s3cret-pass-12")
	if session == "" {
		t.Fatal("requester login must succeed")
	}
	body := doRequest(h.mux, h.mw, http.MethodGet, "/tickets",
		map[string]string{"Cookie": sessionCookie + "=" + session}).Body.String()
	if !appearsBefore(body, newLow.Title, oldCritical.Title) {
		t.Errorf("requester list must stay newest-first regardless of the stored default, got: %s", body)
	}
}
