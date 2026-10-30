package httpadapter

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// API section on the Settings screen (issue #488, S4/S5/S7): administrators
// manage service users and their keys, only root switches the API on or off,
// and a created key's token is shown once, in the response that created it.

var apiTokenPattern = regexp.MustCompile(`tkt_[0-9a-f]{12}_[0-9a-f]{64}`)

func apiAdminEventCount(t *testing.T, h *harness) int64 {
	t.Helper()
	return scanOneInt(t, h.rawDB(t), `SELECT COUNT(*) FROM api_admin_events`)
}

func TestSettingsAPISectionGating(t *testing.T) {
	h := newHarness(t)
	_, rootSession := seedRoot(t, h)
	agent := seedUserRole(t, h.store, "Agent", "agent@tkt.test", domain.RoleAgent)
	user := seedUserRole(t, h.store, "User", "user@tkt.test", domain.RoleUser)

	adminRec := h.get(t, "/settings/api", false)
	if adminRec.Code != http.StatusOK {
		t.Fatalf("admin GET /settings/api = %d, want 200", adminRec.Code)
	}
	adminBody := adminRec.Body.String()
	for _, want := range []string{
		`href="/settings/api" aria-current="page"`,
		`<h2 class="settings-section-title">API access</h2>`,
		`<p class="api-access-state">Off</p>`,
		"Only root can turn the API on or off.",
		`action="/settings/api/service-users"`,
		"No service users yet.",
	} {
		if !strings.Contains(adminBody, want) {
			t.Errorf("admin API section must contain %q, got: %.2000s", want, adminBody)
		}
	}
	if strings.Contains(adminBody, `action="/settings/api/toggle"`) {
		t.Error("an administrator is offered the root-only API switch")
	}
	assertNoWhitespaceViolations(t, adminBody)

	rootBody := getAs(t, h, "/settings/api", rootSession.ID).Body.String()
	if !strings.Contains(rootBody, `action="/settings/api/toggle"`) || !strings.Contains(rootBody, "Turn on the API") {
		t.Errorf("root must get the API switch, got: %.2000s", rootBody)
	}
	// The rail offers the section to administrators on every settings page.
	if !strings.Contains(h.get(t, "/settings/organization", false).Body.String(), `href="/settings/api"`) {
		t.Error("the settings rail does not offer the API section")
	}

	for _, tc := range []struct {
		name      string
		sessionID string
	}{
		{"agent", seedSession(t, h.store, agent.ID).ID},
		{"user", seedSession(t, h.store, user.ID).ID},
	} {
		if rec := getAs(t, h, "/settings/api", tc.sessionID); rec.Code != http.StatusForbidden {
			t.Errorf("%s GET /settings/api = %d, want 403", tc.name, rec.Code)
		}
		for path, form := range map[string]url.Values{
			"/settings/api/toggle":        {"enabled": {"1"}},
			"/settings/api/service-users": {"name": {"Bot"}, "role": {"user"}},
			"/settings/api/keys":          {"service_user_id": {"1"}, "name": {"K"}, "scopes": {"tickets:read"}},
		} {
			if rec := h.postFormAs(t, path, form, tc.sessionID); rec.Code != http.StatusForbidden {
				t.Errorf("%s POST %s = %d, want 403", tc.name, path, rec.Code)
			}
		}
	}
	if rec := h.postFormAs(t, "/settings/api/toggle", url.Values{"enabled": {"1"}}, h.adminSession.ID); rec.Code != http.StatusForbidden {
		t.Errorf("admin POST /settings/api/toggle = %d, want 403", rec.Code)
	}
	if on, _ := h.store.APIAccessStore().APIEnabled(t.Context()); on {
		t.Error("a refused toggle turned the API on")
	}
	if n := apiAdminEventCount(t, h); n != 0 {
		t.Errorf("refused requests wrote %d admin events", n)
	}

	// A value that is not 0 or 1 is refused, never read as "off".
	if on, _ := h.store.APIAccessStore().APIEnabled(t.Context()); on {
		t.Fatal("precondition: API off")
	}
	for _, values := range [][]string{{"true"}, {""}, {"on"}, nil, {"0", "1"}, {"1", "1"}} {
		if rec := h.postFormAs(t, "/settings/api/toggle", url.Values{"enabled": values}, rootSession.ID); rec.Code != http.StatusBadRequest {
			t.Errorf("toggle enabled=%q = %d, want 400", values, rec.Code)
		}
	}
	if n := apiAdminEventCount(t, h); n != 0 {
		t.Errorf("refused toggles wrote %d admin events", n)
	}

	// Root's switch works.
	wantRedirect(t, h.postFormAs(t, "/settings/api/toggle", url.Values{"enabled": {"1"}}, rootSession.ID), http.StatusSeeOther, "/settings/api")
	if on, _ := h.store.APIAccessStore().APIEnabled(t.Context()); !on {
		t.Error("root's toggle did not turn the API on")
	}
}

func TestSettingsAPITicketsAllOption(t *testing.T) {
	f := newAPIFixture(t)
	page := f.h.get(t, "/settings/api", false).Body.String()
	if strings.Contains(page, `value="tickets:all"`) {
		t.Fatal("user service offered tickets:all")
	}
	if _, err := f.svc.CreateServiceUser(t.Context(), *f.root, "Agent", domain.RoleAgent); err != nil {
		t.Fatal(err)
	}
	page = f.h.get(t, "/settings/api", false).Body.String()
	for _, text := range []string{`value="tickets:all"`, "Read and work every ticket", "A leaked key with this scope can change every ticket."} {
		if strings.Count(page, text) != 1 {
			t.Errorf("want one %q", text)
		}
	}
	assertNoWhitespaceViolations(t, page)
}

func TestSettingsAPIServiceUserAndKeyLifecycle(t *testing.T) {
	h := newHarness(t)
	admin := h.adminSession.ID

	wantRedirect(t, h.postFormAs(t, "/settings/api/service-users", url.Values{"name": {"Triage bot"}, "role": {"agent"}}, admin), http.StatusSeeOther, "/settings/api")
	services, err := h.store.APIAccessStore().ListServiceUsers(t.Context())
	if err != nil || len(services) != 1 {
		t.Fatalf("service users = %+v (err %v), want one", services, err)
	}
	bot := services[0]
	if body := h.get(t, "/users", false).Body.String(); strings.Contains(body, "Triage bot") {
		t.Error("the service user appears on the people screen")
	}

	// A rejected key stores nothing and answers the reason.
	rec := h.postFormAs(t, "/settings/api/keys", url.Values{"service_user_id": {itoa(bot.ID)}, "name": {"Zabbix"}}, admin)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "choose at least one scope") {
		t.Errorf("key without scopes = %d, want 422 with the reason: %.1500s", rec.Code, rec.Body.String())
	}
	if keys, _ := h.store.APIAccessStore().ListAPIKeys(t.Context()); len(keys) != 0 {
		t.Fatalf("a rejected key was stored: %+v", keys)
	}

	rec = h.postFormAs(t, "/settings/api/keys", url.Values{
		"service_user_id": {itoa(bot.ID)}, "name": {"Zabbix"}, "scopes": {"tickets:read", "comments:internal"}, "expires_on": {"2099-01-01"},
	}, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("create key = %d, want 200: %.1500s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("token response Cache-Control = %q, want no-store", got)
	}
	body := rec.Body.String()
	token := apiTokenPattern.FindString(body)
	if token == "" || !strings.Contains(body, "Key Zabbix created") || !strings.Contains(body, "shown only once") {
		t.Fatalf("the created token is not shown: %.2000s", body)
	}
	assertNoWhitespaceViolations(t, body)
	keys, _ := h.store.APIAccessStore().ListAPIKeys(t.Context())
	if len(keys) != 1 || strings.Contains(token, keys[0].SecretHash) || keys[0].ExpiresAt == nil || keys[0].ExpiresAt.Format("2006-01-02") != "2099-01-01" {
		t.Fatalf("stored key = %+v", keys)
	}
	if stored := scanOneInt(t, h.rawDB(t), `SELECT COUNT(*) FROM api_keys WHERE secret_hash = ? OR public_id = ?`, token, token); stored != 0 {
		t.Errorf("the token itself was stored (%d rows)", stored)
	}

	page := h.get(t, "/settings/api", false).Body.String()
	if apiTokenPattern.MatchString(page) {
		t.Error("the token is shown again on a later visit")
	}
	for _, want := range []string{"Zabbix", "<code>tickets:read</code>", "<code>comments:internal</code>", "2099-01-01", "Not used yet", "Revoke Zabbix"} {
		if !strings.Contains(page, want) {
			t.Errorf("API section must list %q, got: %.2500s", want, page)
		}
	}

	wantRedirect(t, h.postFormAs(t, "/settings/api/keys/"+itoa(keys[0].ID)+"/revoke", url.Values{}, admin), http.StatusSeeOther, "/settings/api")
	if keys, _ := h.store.APIAccessStore().ListAPIKeys(t.Context()); keys[0].RevokedAt == nil {
		t.Error("revoke did not revoke the key")
	}

	for _, values := range [][]string{{"false"}, {"1", "0"}} {
		if rec := h.postFormAs(t, "/settings/api/service-users/"+itoa(bot.ID)+"/active", url.Values{"active": values}, admin); rec.Code != http.StatusBadRequest {
			t.Errorf("active=%q = %d, want 400", values, rec.Code)
		}
	}
	if got, _ := h.store.UserStore().GetByID(t.Context(), bot.ID); !got.Active {
		t.Fatal("a refused switch deactivated the service user")
	}
	wantRedirect(t, h.postFormAs(t, "/settings/api/service-users/"+itoa(bot.ID)+"/active", url.Values{"active": {"0"}}, admin), http.StatusSeeOther, "/settings/api")
	if got, _ := h.store.UserStore().GetByID(t.Context(), bot.ID); got.Active {
		t.Error("deactivate left the service user active")
	}
	if page := h.get(t, "/settings/api", false).Body.String(); !strings.Contains(page, "Reactivate Triage bot") || strings.Contains(page, `action="/settings/api/keys"`) {
		t.Errorf("a deactivated service user must offer reactivation and no key form: %.2500s", page)
	}

	// A person cannot be reached through the API screen.
	if rec := h.postFormAs(t, "/settings/api/service-users/"+itoa(h.admin.ID)+"/active", url.Values{"active": {"0"}}, admin); rec.Code != http.StatusNotFound {
		t.Errorf("deactivating a person through the API screen = %d, want 404", rec.Code)
	}
	if got, _ := h.store.UserStore().GetByID(t.Context(), h.admin.ID); !got.Active {
		t.Error("the API screen deactivated a person")
	}
}
