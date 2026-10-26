package httpadapter

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
	"github.com/giulianotesta7/tkt/internal/i18n"
)

// Per-user ticket activity order (issue #438, S1): the Preferences page posts
// `timeline_order=newest|oldest` natively to POST /preferences/timeline-order,
// which answers the same 303 back to /preferences with the shared save toast the
// queue-order and theme forms use. It is a preference of its OWN — the same one
// the Activity header toggle writes — so a value outside the closed set is a 422
// that leaves the stored order untouched, and the route writes ONLY that key.
//
// It is deliberately NOT the queue order: "Newest first" exists in both sets, and
// the two labels are distinguished by their sections and their keys, never by a
// shared field.

// storedTimelineOrder reads the persisted activity order through the store port
// that owns it (never raw SQL: the port already answers the question).
func storedTimelineOrder(t *testing.T, h *harness, userID int64) string {
	t.Helper()
	order, err := h.store.TimelineOrderPreferences().GetTimelineOrder(context.Background(), userID)
	if err != nil {
		t.Fatalf("read stored activity order for user %d: %v", userID, err)
	}
	return order
}

// TestPreferencesTimelineOrderDefaultsToNewestFirst proves the page renders the
// documented default for a user who never chose: both options, the newest-first
// one selected, and no row created by the read.
func TestPreferencesTimelineOrderDefaultsToNewestFirst(t *testing.T) {
	h := newHarness(t)

	body := h.get(t, "/preferences", false).Body.String()

	for _, want := range []string{
		`action="/preferences/timeline-order"`,
		`<select id="timeline_order" name="timeline_order">`,
		`<option value="newest" selected>` + i18n.T(i18n.Default, "preferences.timeline.newest") + `</option>`,
		`<option value="oldest">` + i18n.T(i18n.Default, "preferences.timeline.oldest") + `</option>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the preferences page must render %q, got: %s", want, body)
		}
	}
	// The labels are the two the spec names, in both languages, and they are NOT
	// the queue's own wording: the queue says "Newest first" too, so the section
	// heading and the key are what keep the two apart.
	if got := i18n.T(i18n.Default, "preferences.timeline.newest"); got != "Newest first" {
		t.Errorf("English newest label = %q, want %q", got, "Newest first")
	}
	if got := i18n.T(i18n.Default, "preferences.timeline.oldest"); got != "Oldest first" {
		t.Errorf("English oldest label = %q, want %q", got, "Oldest first")
	}
	if strings.Contains(body, `name="timeline_order" value="priority"`) {
		t.Error("the activity order must not offer the queue's priority or urgency orders")
	}
	if stored := storedTimelineOrder(t, h, h.admin.ID); stored != "" {
		t.Errorf("rendering the page stored the activity order %q, want no row", stored)
	}
}

// TestPreferencesTimelineOrderSavePersistsEveryValue proves both choices round
// trip through the native form: each save answers the redirect with the shared
// save feedback, stores the value for the actor, and re-renders with that option
// selected.
func TestPreferencesTimelineOrderSavePersistsEveryValue(t *testing.T) {
	h := newHarness(t)

	for _, order := range application.AllowedTimelineOrders() {
		rec := h.postForm(t, "/preferences/timeline-order", url.Values{"timeline_order": {order}}, false)
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("POST /preferences/timeline-order order=%q = %d, want %d: %s", order, rec.Code, http.StatusSeeOther, rec.Body.String())
		}
		if loc := rec.Header().Get("Location"); loc != "/preferences" {
			t.Errorf("POST order=%q Location = %q, want /preferences", order, loc)
		}
		if cookie := findCookie(rec, saveFeedbackCookie); cookie == nil || cookie.Value == "" {
			t.Errorf("POST order=%q issued no %s cookie, want the shared save toast", order, saveFeedbackCookie)
		}
		if got := storedTimelineOrder(t, h, h.admin.ID); got != order {
			t.Errorf("after saving %q: stored activity order = %q", order, got)
		}
		body := h.get(t, "/preferences", false).Body.String()
		if !strings.Contains(body, `<option value="`+order+`" selected>`) {
			t.Errorf("after saving %q the page must select it, got: %s", order, body)
		}
	}
}

// TestPreferencesTimelineOrderRejectsUnknownValue proves the closed set: anything
// other than the two documented values re-renders the page with the inline error
// (422), leaves the stored order exactly as it was, and creates no row.
func TestPreferencesTimelineOrderRejectsUnknownValue(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if err := h.store.TimelineOrderPreferences().SetTimelineOrder(ctx, h.admin.ID, application.TimelineOrderOldest); err != nil {
		t.Fatalf("seed oldest order: %v", err)
	}

	for _, value := range []string{"", "Newest", "newest ", "asc", "desc", "oldest first", "priority"} {
		rec := h.postForm(t, "/preferences/timeline-order", url.Values{"timeline_order": {value}}, false)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("POST timeline_order=%q = %d, want %d", value, rec.Code, http.StatusUnprocessableEntity)
			continue
		}
		body := rec.Body.String()
		if !strings.Contains(body, `role="alert"`) {
			t.Errorf("POST timeline_order=%q rendered no error banner, got: %s", value, body)
		}
		if !strings.Contains(body, i18n.T(i18n.Default, "error.invalid_timeline_order")) {
			t.Errorf("POST timeline_order=%q banner must carry %q, got: %s", value, i18n.T(i18n.Default, "error.invalid_timeline_order"), body)
		}
		if got := storedTimelineOrder(t, h, h.admin.ID); got != application.TimelineOrderOldest {
			t.Errorf("POST timeline_order=%q changed the stored order to %q, want %q untouched", value, got, application.TimelineOrderOldest)
		}
	}

	// A rejected submission from a user with NO stored row creates none: the
	// refusal is a validation failure, never a defaulted write.
	agent := seedUserRole(t, h.store, "Order agent", "order-agent@tkt.test", domain.RoleAgent)
	session := seedSession(t, h.store, agent.ID)
	rec := h.postFormAs(t, "/preferences/timeline-order", url.Values{"timeline_order": {"sideways"}}, session.ID)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("rejected POST as a fresh user = %d, want 422: %s", rec.Code, rec.Body.String())
	}
	if stored := storedTimelineOrder(t, h, agent.ID); stored != "" {
		t.Errorf("a rejected submission stored %q for a user who never chose, want no row", stored)
	}
}

// TestPreferencesTimelineOrderIsPerUser proves the page and the stored value are
// keyed by the session: one user's oldest-first choice never renders for another.
func TestPreferencesTimelineOrderIsPerUser(t *testing.T) {
	h := newHarness(t)
	ana := seedUserRole(t, h.store, "Ana Order", "ana-order@tkt.test", domain.RoleAgent)
	bruno := seedUserRole(t, h.store, "Bruno Order", "bruno-order@tkt.test", domain.RoleAgent)
	anaSession := seedSession(t, h.store, ana.ID)
	brunoSession := seedSession(t, h.store, bruno.ID)

	rec := h.postFormAs(t, "/preferences/timeline-order", url.Values{"timeline_order": {application.TimelineOrderOldest}}, anaSession.ID)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST as Ana = %d, want 303: %s", rec.Code, rec.Body.String())
	}

	anaBody := h.getAs(t, "/preferences", anaSession.ID).Body.String()
	brunoBody := h.getAs(t, "/preferences", brunoSession.ID).Body.String()
	if !strings.Contains(anaBody, `<option value="oldest" selected>`) {
		t.Errorf("Ana's page must select her own oldest-first choice, got: %s", anaBody)
	}
	if !strings.Contains(brunoBody, `<option value="newest" selected>`) {
		t.Errorf("Bruno's page must keep the newest-first default, got: %s", brunoBody)
	}
	if stored := storedTimelineOrder(t, h, bruno.ID); stored != "" {
		t.Errorf("Ana's save stored %q for Bruno, want no row", stored)
	}
}

// TestPreferencesTimelineOrderRequiresAuthentication proves the route sits
// behind the session gate like every other POST: an anonymous request is
// redirected to /login before the handler runs, and nothing is stored.
func TestPreferencesTimelineOrderRequiresAuthentication(t *testing.T) {
	h := newHarness(t)

	rec := doRequest(h.mux, h.mw, http.MethodPost, "/preferences/timeline-order", nil)
	wantRedirect(t, rec, http.StatusSeeOther, "/login")
	if stored := storedTimelineOrder(t, h, h.admin.ID); stored != "" {
		t.Errorf("an unauthenticated POST stored %q, want nothing", stored)
	}
}

// TestPreferencesTimelineOrderSaveLeavesSiblingPreferencesAlone proves PRESERVE
// for the four preferences this change sits beside: the queue order, the email
// opt-out, the sidebar state, and the theme keep their own values, their own
// routes and their own stored keys while the activity order changes — and the
// reverse holds, so no save rewrites another preference.
func TestPreferencesTimelineOrderSaveLeavesSiblingPreferencesAlone(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	// Arrange every sibling through its own documented route or use case.
	if rec := h.postForm(t, "/preferences", url.Values{"queue_order": {application.QueueOrderPriority}}, false); rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /preferences = %d, want 303: %s", rec.Code, rec.Body.String())
	}
	if rec := h.postForm(t, "/preferences/email", url.Values{}, false); rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /preferences/email = %d, want 303: %s", rec.Code, rec.Body.String())
	}
	if err := h.store.SidebarPreferences().SetSidebarCollapsed(ctx, h.admin.ID, true); err != nil {
		t.Fatalf("seed collapsed sidebar: %v", err)
	}
	if rec := h.postForm(t, "/preferences/theme", url.Values{"theme": {application.ThemeDark}}, false); rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /preferences/theme = %d, want 303: %s", rec.Code, rec.Body.String())
	}

	if rec := h.postForm(t, "/preferences/timeline-order", url.Values{"timeline_order": {application.TimelineOrderOldest}}, false); rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /preferences/timeline-order = %d, want 303: %s", rec.Code, rec.Body.String())
	}

	if order, err := h.store.PreferencesStore().GetQueueOrder(ctx, h.admin.ID); err != nil || order != application.QueueOrderPriority {
		t.Errorf("queue order = %q (err %v), want %q untouched", order, err, application.QueueOrderPriority)
	}
	if enabled, err := h.store.EmailPreferences().EmailNotificationsEnabled(ctx, h.admin.ID); err != nil || enabled {
		t.Errorf("email enabled = %v (err %v), want the stored opt-out untouched", enabled, err)
	}
	if collapsed, err := h.store.SidebarPreferences().SidebarCollapsed(ctx, h.admin.ID); err != nil || !collapsed {
		t.Errorf("sidebar collapsed = %v (err %v), want it untouched", collapsed, err)
	}
	if theme, err := h.store.ThemePreferences().GetTheme(ctx, h.admin.ID); err != nil || theme != application.ThemeDark {
		t.Errorf("theme = %q (err %v), want it untouched", theme, err)
	}

	// The reverse: the sibling routes still answer what they always answered and
	// leave the activity order where it was.
	if rec := h.postForm(t, "/preferences", url.Values{"queue_order": {application.QueueOrderUrgency}}, false); rec.Code != http.StatusSeeOther {
		t.Fatalf("second POST /preferences = %d, want 303: %s", rec.Code, rec.Body.String())
	}
	if rec := h.postForm(t, "/preferences/theme", url.Values{"theme": {application.ThemeLight}}, false); rec.Code != http.StatusSeeOther {
		t.Fatalf("second POST /preferences/theme = %d, want 303: %s", rec.Code, rec.Body.String())
	}
	if got := storedTimelineOrder(t, h, h.admin.ID); got != application.TimelineOrderOldest {
		t.Errorf("a sibling save changed the activity order to %q, want %q", got, application.TimelineOrderOldest)
	}
	// And the queue itself still opens with the stored default order: the two
	// controls are separate preferences, so the queue keeps its own vocabulary
	// and its own stored value.
	queue := h.get(t, "/tickets", false).Body.String()
	if !strings.Contains(queue, `<option value="urgency" selected>`) {
		t.Errorf("the queue must keep using its own stored order, got: %s", queue)
	}
	if strings.Contains(queue, `<select name="timeline_order"`) {
		t.Error("the activity order must never become the queue's order control")
	}
}
