package httpadapter

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
	"github.com/giulianotesta7/tkt/internal/i18n"
)

// Preferences → Connected apps (issue #493, S10): a person sees the MCP
// clients they allowed to act as them and revokes them. Every grant is created
// through the real OAuth flow (register → prepare → approve → token), so the
// list and the revocation run over what production stores.

// connectApp runs the whole authorization code flow for person through the
// harness's OAuth service and answers the access token and its grant id.
func connectApp(t *testing.T, h *harness, person *domain.User, clientName, scope string) (string, int64) {
	t.Helper()
	ctx := t.Context()
	const redirect = "http://127.0.0.1/callback"
	client, err := h.oauth.RegisterClient(ctx, application.ClientRegistration{ClientName: clientName, RedirectURIs: []string{redirect}})
	if err != nil {
		t.Fatalf("register client: %v", err)
	}
	p, err := h.oauth.PrepareAuthorization(ctx, application.AuthorizeRequest{
		ClientID: client.ClientID, RedirectURI: redirect, ResponseType: "code", State: "state",
		CodeChallenge: pkceChallenge(oauthVerifier), CodeChallengeMethod: "S256", Scope: scope,
	})
	if err != nil {
		t.Fatalf("prepare authorization: %v", err)
	}
	location, err := h.oauth.Approve(ctx, *person, *p)
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	u, err := url.Parse(location)
	if err != nil || u.Query().Get("code") == "" {
		t.Fatalf("approve redirect %q: %v", location, err)
	}
	resp, err := h.oauth.Token(ctx, application.TokenRequest{GrantType: "authorization_code", Code: u.Query().Get("code"), RedirectURI: redirect, ClientID: client.ClientID, CodeVerifier: oauthVerifier})
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	grants, err := h.oauth.ListGrants(ctx, *person)
	if err != nil || len(grants) == 0 {
		t.Fatalf("grants = %v, %v", grants, err)
	}
	return resp.AccessToken, grants[0].ID
}

func tokenWorks(t *testing.T, h *harness, token string) bool {
	t.Helper()
	_, err := h.oauth.Authenticate(t.Context(), token)
	if err != nil && !errors.Is(err, application.ErrAPIUnauthorized) {
		t.Fatalf("authenticate: %v", err)
	}
	return err == nil
}

// connectedAppsSection cuts the Connected apps section out of the page, or
// answers "" when the page has none.
func connectedAppsSection(body string) string {
	start := strings.Index(body, `<section class="preferences-section" aria-labelledby="connected-apps-title">`)
	if start < 0 {
		return ""
	}
	end := strings.Index(body[start:], "</section>")
	if end < 0 {
		return body[start:]
	}
	return body[start : start+end+len("</section>")]
}

func revokePath(id int64) string {
	return "/preferences/connected-apps/" + strconv.FormatInt(id, 10) + "/revoke"
}

func TestConnectedAppsListsOwnLiveGrants(t *testing.T) {
	h := newHarness(t)
	enableMCP(t, h)
	pat := seedUserRole(t, h.store, "Pat", "pat@tkt.test", domain.RoleUser)
	patSession := seedSession(t, h.store, pat.ID).ID
	olga := seedUserRole(t, h.store, "Olga", "olga@tkt.test", domain.RoleAgent)
	token, grant := connectApp(t, h, pat, `Claude <Code>`, "tickets:read tickets:write")
	connectApp(t, h, olga, "Olga's app", "")

	section := connectedAppsSection(getAs(t, h, "/preferences", patSession).Body.String())
	created := formatMonthDayYear(i18n.New(i18n.Default), fixedNow)
	for _, want := range []string{
		`<h2 id="connected-apps-title">Connected apps</h2>`,
		"Claude &lt;Code&gt;",
		"Read and search the tickets you can see",
		"Create tickets and work on them as you",
		`<time datetime="` + formatDatetime(fixedNow) + `">` + created + `</time>`,
		"Never used",
		`action="` + revokePath(grant) + `"`,
		"Revoke Claude &lt;Code&gt;",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("Connected apps must contain %q, got: %s", want, section)
		}
	}
	for _, unwanted := range []string{"Claude <Code>", "Olga", "See the categories and their forms"} {
		if strings.Contains(section, unwanted) {
			t.Errorf("Connected apps must not contain %q, got: %s", unwanted, section)
		}
	}

	// Using the token stamps the grant.
	if !tokenWorks(t, h, token) {
		t.Fatal("a fresh token does not authenticate")
	}
	section = connectedAppsSection(getAs(t, h, "/preferences", patSession).Body.String())
	if strings.Contains(section, "Never used") || !strings.Contains(section, "Last used") {
		t.Errorf("a used grant must show when it was last used, got: %s", section)
	}
	assertNoWhitespaceViolations(t, getAs(t, h, "/preferences", patSession).Body.String())
}

func TestConnectedAppsRevoke(t *testing.T) {
	h := newHarness(t)
	enableMCP(t, h)
	pat := seedUserRole(t, h.store, "Pat", "pat@tkt.test", domain.RoleUser)
	patSession := seedSession(t, h.store, pat.ID).ID
	olga := seedUserRole(t, h.store, "Olga", "olga@tkt.test", domain.RoleAgent)
	patToken, patGrant := connectApp(t, h, pat, "Pat's app", "")
	olgaToken, olgaGrant := connectApp(t, h, olga, "Olga's app", "")

	// A cross-site submission is refused by the shared Origin gate.
	req := httptest.NewRequest(http.MethodPost, revokePath(patGrant), strings.NewReader(""))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Cookie", sessionCookie+"="+patSession)
	req.Header.Set("Origin", "https://evil.example")
	if rec := h.serve(req); rec.Code != http.StatusForbidden {
		t.Errorf("cross-site revoke = %d, want 403", rec.Code)
	}
	if !tokenWorks(t, h, patToken) {
		t.Fatal("a refused cross-site revoke revoked the grant")
	}

	// Someone else's grant, an unknown one, and a malformed id are not found.
	for _, path := range []string{revokePath(olgaGrant), revokePath(olgaGrant + 1000), "/preferences/connected-apps/abc/revoke", "/preferences/connected-apps/0/revoke"} {
		if rec := h.postFormAs(t, path, url.Values{}, patSession); rec.Code != http.StatusNotFound {
			t.Errorf("POST %s = %d, want 404", path, rec.Code)
		}
	}
	if !tokenWorks(t, h, olgaToken) {
		t.Fatal("Pat revoked Olga's grant")
	}

	wantRedirect(t, h.postFormAs(t, revokePath(patGrant), url.Values{}, patSession), http.StatusSeeOther, "/preferences")
	if tokenWorks(t, h, patToken) {
		t.Error("a revoked grant's token still authenticates")
	}
	if grants, err := h.oauth.ListGrants(t.Context(), *pat); err != nil || len(grants) != 0 {
		t.Errorf("Pat's grants after revoking = %v, %v; want none", grants, err)
	}
	section := connectedAppsSection(getAs(t, h, "/preferences", patSession).Body.String())
	if strings.Contains(section, "Pat&#39;s app") || !strings.Contains(section, "No connected apps yet.") {
		t.Errorf("the revoked grant is still listed: %s", section)
	}
	// Revoking it again finds nothing.
	if rec := h.postFormAs(t, revokePath(patGrant), url.Values{}, patSession); rec.Code != http.StatusNotFound {
		t.Errorf("revoking twice = %d, want 404", rec.Code)
	}
	if !tokenWorks(t, h, olgaToken) {
		t.Error("revoking Pat's grant broke Olga's")
	}
}

func TestConnectedAppsSectionVisibility(t *testing.T) {
	h := newHarness(t)
	pat := seedUserRole(t, h.store, "Pat", "pat@tkt.test", domain.RoleUser)
	patSession := seedSession(t, h.store, pat.ID).ID

	// Switch off and no grant: no section at all.
	if body := getAs(t, h, "/preferences", patSession).Body.String(); connectedAppsSection(body) != "" || strings.Contains(body, "Connected apps") {
		t.Errorf("the section renders while MCP is off and nothing is connected: %.3000s", body)
	}

	// Switch on: the section and its empty state.
	enableMCP(t, h)
	section := connectedAppsSection(getAs(t, h, "/preferences", patSession).Body.String())
	if !strings.Contains(section, "No connected apps yet.") {
		t.Errorf("the empty state is missing: %s", section)
	}

	// A live grant keeps the section after the switch goes off, so it can be revoked.
	_, grant := connectApp(t, h, pat, "Leftover", "")
	root, err := h.store.UserStore().GetByEmail(t.Context(), "root@tkt.test")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.oauth.SetMCPEnabled(t.Context(), *root, false); err != nil {
		t.Fatal(err)
	}
	section = connectedAppsSection(getAs(t, h, "/preferences", patSession).Body.String())
	if !strings.Contains(section, "Leftover") || !strings.Contains(section, `action="`+revokePath(grant)+`"`) {
		t.Errorf("a live grant must stay listed while MCP is off: %s", section)
	}
	wantRedirect(t, h.postFormAs(t, revokePath(grant), url.Values{}, patSession), http.StatusSeeOther, "/preferences")
	if body := getAs(t, h, "/preferences", patSession).Body.String(); connectedAppsSection(body) != "" {
		t.Error("the section stays after the last grant is revoked while MCP is off")
	}
}

// fixtureConnectedAppsData is the preferences page with one connected app: a
// client name that must be escaped, two scopes, used once.
func fixtureConnectedAppsData() preferencesData {
	data := fixturePreferencesPageData()
	data.ConnectedApps = connectedAppsData{Shown: true, Apps: []connectedAppRow{{
		ID:         4,
		ClientName: `Claude <Code>`,
		Scopes: []oauthScopeView{
			{Scope: domain.ScopeTicketsRead, Label: oauthScopeLabels[domain.ScopeTicketsRead]},
			{Scope: domain.ScopeTicketsWrite, Label: oauthScopeLabels[domain.ScopeTicketsWrite]},
		},
		CreatedAt:  goldenT0,
		LastUsedAt: goldenT0.Add(26 * time.Hour),
	}}}
	return data
}

func TestGoldenPreferencesConnectedApps(t *testing.T) {
	goldenFullPage(t, "preferences_connected_apps", renderGolden(t, "preferences", "", fixtureConnectedAppsData(), false))
}

// TestGoldenPreferencesConnectedAppsEmpty freezes the empty state, which shows
// while MCP is on and nothing is connected.
func TestGoldenPreferencesConnectedAppsEmpty(t *testing.T) {
	data := fixturePreferencesPageData()
	data.ConnectedApps = connectedAppsData{Shown: true}
	goldenFile(t, "preferences_connected_apps_empty", connectedAppsSection(renderGolden(t, "preferences", "", data, false)))
}

func TestConnectedAppsSpeakSpanish(t *testing.T) {
	section := connectedAppsSection(renderLocalized(t, "es", "preferences", "", fixtureConnectedAppsData()))
	for _, want := range []string{"Aplicaciones conectadas", "Revocar Claude &lt;Code&gt;", "Último uso"} {
		if !strings.Contains(section, want) {
			t.Errorf("the Spanish section must contain %q, got: %s", want, section)
		}
	}
	assertNoWhitespaceViolations(t, section)
}
