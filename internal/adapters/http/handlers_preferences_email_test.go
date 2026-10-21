package httpadapter

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// Preferences email opt-out (issue #362, S5/S14): the Preferences page carries
// a "Receive notifications by email" toggle, checked by default, saved in
// user_preferences. It is one form of its own, posting to its own route, so an
// email save can never rewrite the queue order and a queue-order save can
// never switch email off by simply omitting the checkbox.

// The two exact control renderings, so a test can tell "checked" from
// "unchecked" without depending on attribute order around them.
const (
	emailToggleChecked   = `<input type="checkbox" name="email_notifications" value="1" checked>`
	emailToggleUnchecked = `<input type="checkbox" name="email_notifications" value="1">`
)

// storedEmailNotifications reads the persisted opt-out through the store port
// that owns it (never raw SQL: the port already answers the question).
func storedEmailNotifications(t *testing.T, h *harness, userID int64) bool {
	t.Helper()
	enabled, err := h.store.EmailPreferences().EmailNotificationsEnabled(context.Background(), userID)
	if err != nil {
		t.Fatalf("read stored email preference for user %d: %v", userID, err)
	}
	return enabled
}

// storedQueueOrder reads the persisted queue order through its own port.
func storedQueueOrder(t *testing.T, h *harness, userID int64) string {
	t.Helper()
	order, err := h.store.PreferencesStore().GetQueueOrder(context.Background(), userID)
	if err != nil {
		t.Fatalf("read stored queue order for user %d: %v", userID, err)
	}
	return order
}

// TestPreferencesEmailToggleIsCheckedByDefault proves the S14 clause
// "activado por defecto": a user who never saved the preference opens
// Preferences with the box checked, and opening the page stores nothing.
func TestPreferencesEmailToggleIsCheckedByDefault(t *testing.T) {
	h := newHarness(t)

	body := h.get(t, "/preferences", false).Body.String()
	if !strings.Contains(body, emailToggleChecked) {
		t.Fatalf("the email toggle must render checked by default, got: %s", body)
	}
	if strings.Contains(body, emailToggleUnchecked) {
		t.Errorf("the email toggle must not render unchecked by default, got: %s", body)
	}
	if !strings.Contains(body, `action="/preferences/email"`) {
		t.Errorf("the email toggle must post to its own route, got: %s", body)
	}
	if !storedEmailNotifications(t, h, h.admin.ID) {
		t.Error("rendering the page must leave the preference at its default (on)")
	}
}

// TestPreferencesEmailToggleOffPersistsAndRendersUnchecked proves the write
// path: submitting the box unchecked stores "off", redirects back to the page
// with the shared save feedback, and the page then renders the box unchecked.
func TestPreferencesEmailToggleOffPersistsAndRendersUnchecked(t *testing.T) {
	h := newHarness(t)

	// An unchecked checkbox is simply absent from the form body.
	rec := h.postForm(t, "/preferences/email", url.Values{}, false)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /preferences/email = %d, want %d: %s", rec.Code, http.StatusSeeOther, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "/preferences" {
		t.Errorf("Location = %q, want /preferences", loc)
	}
	if storedEmailNotifications(t, h, h.admin.ID) {
		t.Fatal("turning the toggle off must persist off")
	}
	body := h.get(t, "/preferences", false).Body.String()
	if !strings.Contains(body, emailToggleUnchecked) {
		t.Fatalf("a stored opt-out must render the toggle unchecked, got: %s", body)
	}
	if strings.Contains(body, emailToggleChecked) {
		t.Errorf("a stored opt-out must not render the toggle checked, got: %s", body)
	}
}

// TestPreferencesEmailToggleBackOn proves the reverse direction: the stored
// opt-out is replaced, not duplicated, and the box renders checked again.
func TestPreferencesEmailToggleBackOn(t *testing.T) {
	h := newHarness(t)

	h.postForm(t, "/preferences/email", url.Values{}, false)
	if storedEmailNotifications(t, h, h.admin.ID) {
		t.Fatal("setup: the toggle must be off before turning it back on")
	}

	rec := h.postForm(t, "/preferences/email", url.Values{"email_notifications": {"1"}}, false)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /preferences/email = %d, want %d: %s", rec.Code, http.StatusSeeOther, rec.Body.String())
	}
	if !storedEmailNotifications(t, h, h.admin.ID) {
		t.Fatal("turning the toggle back on must persist on")
	}
	if body := h.get(t, "/preferences", false).Body.String(); !strings.Contains(body, emailToggleChecked) {
		t.Fatalf("a re-enabled preference must render the toggle checked, got: %s", body)
	}
}

// TestPreferencesEmailToggleRequiresAuthentication proves the new route sits
// behind the same session gate as the page: an anonymous POST lands on /login
// and stores nothing.
func TestPreferencesEmailToggleRequiresAuthentication(t *testing.T) {
	h := newHarness(t)

	rec := doRequest(h.mux, h.mw, http.MethodPost, "/preferences/email", nil)
	wantRedirect(t, rec, http.StatusSeeOther, "/login")
	if !storedEmailNotifications(t, h, h.admin.ID) {
		t.Error("an unauthenticated POST must not change the preference")
	}
}

// TestPreferencesEmailToggleOnlyChangesTheActor proves the value is personal:
// an agent's save changes that agent's row only, and the admin's preference
// (and the admin's rendered page) is untouched.
func TestPreferencesEmailToggleOnlyChangesTheActor(t *testing.T) {
	h := newHarness(t)

	agent := seedUserRole(t, h.store, "Ana Agent", "ana-prefs@tkt.test", domain.RoleAgent)
	agentSession := seedSession(t, h.store, agent.ID)

	rec := h.postFormAs(t, "/preferences/email", url.Values{}, agentSession.ID)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /preferences/email as agent = %d, want %d: %s", rec.Code, http.StatusSeeOther, rec.Body.String())
	}

	if storedEmailNotifications(t, h, agent.ID) {
		t.Error("the agent's own opt-out must be stored")
	}
	if !storedEmailNotifications(t, h, h.admin.ID) {
		t.Error("another actor's save must not change the admin's preference")
	}
	adminBody := h.get(t, "/preferences", false).Body.String()
	if !strings.Contains(adminBody, emailToggleChecked) {
		t.Errorf("the admin's page must still render the toggle checked, got: %s", adminBody)
	}
	agentBody := doRequest(h.mux, h.mw, http.MethodGet, "/preferences",
		map[string]string{"Cookie": sessionCookie + "=" + agentSession.ID}).Body.String()
	if !strings.Contains(agentBody, emailToggleUnchecked) {
		t.Errorf("the agent's page must render the toggle unchecked, got: %s", agentBody)
	}
}

// TestPreferencesQueueOrderSaveLeavesEmailAlone proves the two preferences do
// not share a form: the queue-order submission carries no email field and must
// therefore leave a stored opt-out exactly as it is.
func TestPreferencesQueueOrderSaveLeavesEmailAlone(t *testing.T) {
	h := newHarness(t)

	h.postForm(t, "/preferences/email", url.Values{}, false)
	if storedEmailNotifications(t, h, h.admin.ID) {
		t.Fatal("setup: the toggle must be off")
	}

	rec := h.postForm(t, "/preferences", url.Values{"queue_order": {"priority"}}, false)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /preferences = %d, want %d: %s", rec.Code, http.StatusSeeOther, rec.Body.String())
	}

	if storedEmailNotifications(t, h, h.admin.ID) {
		t.Error("a queue-order save must not turn email back on")
	}
	if got := storedQueueOrder(t, h, h.admin.ID); got != "priority" {
		t.Errorf("queue order = %q, want priority", got)
	}
}

// TestPreferencesEmailSaveLeavesQueueOrderAlone proves the reverse: an email
// submission carries no queue-order field and must leave the stored order
// exactly as it is.
func TestPreferencesEmailSaveLeavesQueueOrderAlone(t *testing.T) {
	h := newHarness(t)

	h.postForm(t, "/preferences", url.Values{"queue_order": {"urgency"}}, false)
	if got := storedQueueOrder(t, h, h.admin.ID); got != "urgency" {
		t.Fatalf("setup: queue order = %q, want urgency", got)
	}

	rec := h.postForm(t, "/preferences/email", url.Values{}, false)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /preferences/email = %d, want %d: %s", rec.Code, http.StatusSeeOther, rec.Body.String())
	}

	if got := storedQueueOrder(t, h, h.admin.ID); got != "urgency" {
		t.Errorf("an email save must not rewrite the queue order, got %q", got)
	}
	if body := h.get(t, "/preferences", false).Body.String(); !strings.Contains(body, `value="urgency" selected`) {
		t.Errorf("the page must still show the stored queue order, got: %s", body)
	}
}
