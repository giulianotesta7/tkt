package httpadapter

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
)

// Collapsible sidebar (issue #377, S2): the shell's toggle POSTs
// /preferences/sidebar with `sidebar=collapsed|expanded` and reads the stored
// state back on every page. The route is called from fetch (never a
// navigation), so success is a bodiless 204 and the page is not re-rendered;
// a value outside the two documented states is refused with 422 and changes
// nothing.

// storedSidebarCollapsed reads the persisted sidebar state through the store
// port that owns it (never raw SQL: the port already answers the question).
func storedSidebarCollapsed(t *testing.T, h *harness, userID int64) bool {
	t.Helper()
	collapsed, err := h.store.SidebarPreferences().SidebarCollapsed(context.Background(), userID)
	if err != nil {
		t.Fatalf("read stored sidebar state for user %d: %v", userID, err)
	}
	return collapsed
}

// TestPreferencesSidebarCollapsePersists proves the collapse half of the
// toggle: 204 with NO body (fetch, not a navigation), and the state is stored
// so the next page render can skip the flash.
func TestPreferencesSidebarCollapsePersists(t *testing.T) {
	h := newHarness(t)

	rec := h.postForm(t, "/preferences/sidebar", url.Values{"sidebar": {"collapsed"}}, false)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("POST /preferences/sidebar = %d, want %d: %s", rec.Code, http.StatusNoContent, rec.Body.String())
	}
	if body := rec.Body.String(); body != "" {
		t.Errorf("body = %q, want none (the toggle is a fetch, never a navigation)", body)
	}
	if !storedSidebarCollapsed(t, h, h.admin.ID) {
		t.Error("the collapsed state was not persisted")
	}
}

// TestPreferencesSidebarExpandPersists proves the other half: posting
// "expanded" over a stored collapse restores the expanded shell and still
// answers a bodiless 204.
func TestPreferencesSidebarExpandPersists(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if err := h.store.SidebarPreferences().SetSidebarCollapsed(ctx, h.admin.ID, true); err != nil {
		t.Fatalf("seed collapsed sidebar: %v", err)
	}

	rec := h.postForm(t, "/preferences/sidebar", url.Values{"sidebar": {"expanded"}}, false)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("POST /preferences/sidebar = %d, want %d: %s", rec.Code, http.StatusNoContent, rec.Body.String())
	}
	if body := rec.Body.String(); body != "" {
		t.Errorf("body = %q, want none", body)
	}
	if storedSidebarCollapsed(t, h, h.admin.ID) {
		t.Error("the expanded state was not persisted")
	}
}

// TestPreferencesSidebarRejectsUnknownState proves the closed set: anything
// other than the two documented values is a 422 that leaves the stored state
// untouched, so a hand-crafted request can never invent a third state.
func TestPreferencesSidebarRejectsUnknownState(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if err := h.store.SidebarPreferences().SetSidebarCollapsed(ctx, h.admin.ID, true); err != nil {
		t.Fatalf("seed collapsed sidebar: %v", err)
	}

	for _, value := range []string{"garbage", "", "Collapsed", "collapsed ", "true"} {
		rec := h.postForm(t, "/preferences/sidebar", url.Values{"sidebar": {value}}, false)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("POST sidebar=%q = %d, want %d", value, rec.Code, http.StatusUnprocessableEntity)
			continue
		}
		if body := strings.TrimSpace(rec.Body.String()); body != "Invalid sidebar state" {
			t.Errorf("POST sidebar=%q body = %q, want %q", value, body, "Invalid sidebar state")
		}
		if !storedSidebarCollapsed(t, h, h.admin.ID) {
			t.Errorf("POST sidebar=%q changed the stored state, want it untouched", value)
		}
	}
}

// TestPreferencesSidebarRequiresAuthentication proves the toggle sits behind
// the session gate like every other POST: an anonymous request is redirected
// to /login before the handler runs, and nothing is stored.
func TestPreferencesSidebarRequiresAuthentication(t *testing.T) {
	h := newHarness(t)

	rec := doRequest(h.mux, h.mw, http.MethodPost, "/preferences/sidebar", nil)
	wantRedirect(t, rec, http.StatusSeeOther, "/login")
	if storedSidebarCollapsed(t, h, h.admin.ID) {
		t.Error("an unauthenticated POST must not write the sidebar state")
	}
}

// TestPreferencesExistingSavesKeepTheirBehavior proves the sibling preference
// routes the sidebar toggle now sits beside are unchanged (PRESERVE): the
// email and queue-order forms still answer the same 303 back to /preferences,
// still write only their own key, and leave the sidebar state alone.
func TestPreferencesExistingSavesKeepTheirBehavior(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if err := h.store.SidebarPreferences().SetSidebarCollapsed(ctx, h.admin.ID, true); err != nil {
		t.Fatalf("seed collapsed sidebar: %v", err)
	}

	// An unchecked email checkbox is simply absent from the body: email off.
	rec := h.postForm(t, "/preferences/email", url.Values{}, false)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /preferences/email = %d, want %d: %s", rec.Code, http.StatusSeeOther, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "/preferences" {
		t.Errorf("email Location = %q, want /preferences", loc)
	}
	if storedEmailNotifications(t, h, h.admin.ID) {
		t.Error("the email toggle no longer turns email off")
	}
	if !storedSidebarCollapsed(t, h, h.admin.ID) {
		t.Error("the email save changed the stored sidebar state")
	}

	rec = h.postForm(t, "/preferences", url.Values{"queue_order": {application.QueueOrderPriority}}, false)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /preferences = %d, want %d: %s", rec.Code, http.StatusSeeOther, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "/preferences" {
		t.Errorf("queue-order Location = %q, want /preferences", loc)
	}
	if got := storedQueueOrder(t, h, h.admin.ID); got != application.QueueOrderPriority {
		t.Errorf("stored queue order = %q, want %q", got, application.QueueOrderPriority)
	}
	if !storedSidebarCollapsed(t, h, h.admin.ID) {
		t.Error("the queue-order save changed the stored sidebar state")
	}
}
