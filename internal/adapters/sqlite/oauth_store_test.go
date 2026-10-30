package sqlite

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// OAuth store tests (issue #493): the MCP switch and its admin history, client
// pruning, single-use codes whose reuse revokes the grant, refresh rotation
// whose reuse revokes the grant, and the live grants a person can see and
// revoke.

const oauthResource = "https://tkt.example.com/mcp"

func oauthHash(c string) string { return strings.Repeat(c, 64) }

func wantNotFound(t *testing.T, what string, err error) {
	t.Helper()
	var nf *domain.NotFoundError
	if !errors.As(err, &nf) {
		t.Fatalf("%s: err = %v, want NotFoundError", what, err)
	}
}

func newOAuthClient(t *testing.T, store application.OAuthStore, clientID string, at time.Time) *domain.OAuthClient {
	t.Helper()
	c := &domain.OAuthClient{ClientID: clientID, Name: "Claude " + clientID,
		RedirectURIs: []string{"https://claude.ai/api/mcp/auth_callback", "http://localhost/callback"}, CreatedAt: at}
	if err := store.CreateClient(context.Background(), c, at.Add(-24*time.Hour)); err != nil {
		t.Fatalf("create client %s: %v", clientID, err)
	}
	return c
}

func newOAuthCode(t *testing.T, store application.OAuthStore, hashChar string, clientID, userID int64, at time.Time) *domain.OAuthCode {
	t.Helper()
	c := &domain.OAuthCode{CodeHash: oauthHash(hashChar), ClientID: clientID, UserID: userID,
		RedirectURI: "https://claude.ai/api/mcp/auth_callback", CodeChallenge: strings.Repeat("x", 43),
		Scopes: []domain.APIKeyScope{domain.ScopeTicketsRead, domain.ScopeCommentsWrite}, Resource: oauthResource,
		CreatedAt: at, ExpiresAt: at.Add(time.Minute)}
	if err := store.CreateCode(context.Background(), c); err != nil {
		t.Fatalf("create code: %v", err)
	}
	return c
}

// newOAuthGrant approves a fresh code and stores the grant with one access and
// one refresh token, as the token exchange does.
func newOAuthGrant(t *testing.T, store application.OAuthStore, hashChar string, clientID, userID int64, at time.Time) (*domain.OAuthGrant, *domain.OAuthToken, *domain.OAuthToken) {
	t.Helper()
	code := newOAuthCode(t, store, hashChar, clientID, userID, at)
	g := &domain.OAuthGrant{UserID: userID, ClientID: clientID, Scopes: code.Scopes, Resource: oauthResource, CreatedAt: at}
	access := &domain.OAuthToken{Kind: domain.OAuthTokenAccess, TokenHash: oauthHash(hashChar + "1")[:64], CreatedAt: at, ExpiresAt: at.Add(time.Hour)}
	refresh := &domain.OAuthToken{Kind: domain.OAuthTokenRefresh, TokenHash: oauthHash(hashChar + "2")[:64], CreatedAt: at, ExpiresAt: at.Add(30 * 24 * time.Hour)}
	if err := store.CreateGrant(context.Background(), g, code.ID, access, refresh); err != nil {
		t.Fatalf("create grant: %v", err)
	}
	return g, access, refresh
}

func TestOAuthStoreMCPSwitch(t *testing.T) {
	ctx := context.Background()
	s := newTestDB(t)
	store := s.OAuthStore()
	root := seedUserRaw(t, s, "Root", "root@example.com", "root")
	if on, err := store.MCPEnabled(ctx); err != nil || on {
		t.Fatalf("default MCPEnabled = %v (err %v), want off", on, err)
	}
	if err := store.SetMCPEnabled(ctx, true, root, testClock); err != nil {
		t.Fatal(err)
	}
	if on, err := store.MCPEnabled(ctx); err != nil || !on {
		t.Fatalf("after enable = %v (err %v)", on, err)
	}
	if err := store.SetMCPEnabled(ctx, false, root, testClock.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if on, err := store.MCPEnabled(ctx); err != nil || on {
		t.Fatalf("after disable = %v (err %v)", on, err)
	}
	want := []string{"mcp_enabled:" + itoa(root) + "::", "mcp_disabled:" + itoa(root) + "::"}
	if got := apiAdminActions(t, s); !reflect.DeepEqual(got, want) {
		t.Fatalf("admin events = %q, want %q", got, want)
	}
	if on, err := s.APIAccessStore().APIEnabled(ctx); err != nil || on {
		t.Fatalf("the MCP switch moved the API switch: %v (err %v)", on, err)
	}
}

func TestOAuthStoreClients(t *testing.T) {
	ctx := context.Background()
	s := newTestDB(t)
	store := s.OAuthStore()
	ana := seedUserRaw(t, s, "Ana", "ana@example.com", "agent")
	old := testClock.Add(-48 * time.Hour)

	c := newOAuthClient(t, store, "client-a", testClock)
	if c.ID == 0 {
		t.Fatal("client id not assigned")
	}
	got, err := store.ClientByClientID(ctx, "client-a")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, c) {
		t.Fatalf("client round trip = %+v, want %+v", got, c)
	}
	_, err = store.ClientByClientID(ctx, "missing")
	wantNotFound(t, "unknown client", err)

	// Old and never approved, with a pending code: pruned with its code.
	abandoned := &domain.OAuthClient{ClientID: "abandoned", Name: "Old", RedirectURIs: []string{"cursor://anysphere.cursor-mcp/oauth/callback"}, CreatedAt: old}
	if err := store.CreateClient(ctx, abandoned, old.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	newOAuthCode(t, store, "a", abandoned.ID, ana, old)
	// Old but approved: kept while its grant names it.
	approved := &domain.OAuthClient{ClientID: "approved", Name: "Cursor", RedirectURIs: []string{"cursor://anysphere.cursor-mcp/oauth/callback"}, CreatedAt: old}
	if err := store.CreateClient(ctx, approved, old.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	newOAuthGrant(t, store, "b", approved.ID, ana, old)

	fresh := &domain.OAuthClient{ClientID: "fresh", Name: "VS Code", RedirectURIs: []string{"http://127.0.0.1:33418"}, CreatedAt: testClock}
	if err := store.CreateClient(ctx, fresh, testClock.Add(-24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	_, err = store.ClientByClientID(ctx, "abandoned")
	wantNotFound(t, "pruned client", err)
	if n := countRows(t, s, `SELECT COUNT(*) FROM oauth_codes WHERE client_id = ?`, abandoned.ID); n != 0 {
		t.Fatalf("pruned client's codes left: %d", n)
	}
	for _, id := range []string{"approved", "client-a", "fresh"} {
		if _, err := store.ClientByClientID(ctx, id); err != nil {
			t.Fatalf("client %s pruned: %v", id, err)
		}
	}

	dup := &domain.OAuthClient{ClientID: "fresh", Name: "Dup", RedirectURIs: []string{"https://x.example/cb"}, CreatedAt: testClock}
	if err := store.CreateClient(ctx, dup, testClock.Add(-24*time.Hour)); err == nil {
		t.Fatal("duplicate client_id stored")
	}
}

func TestOAuthStoreCodes(t *testing.T) {
	ctx := context.Background()
	s := newTestDB(t)
	store := s.OAuthStore()
	ana := seedUserRaw(t, s, "Ana", "ana@example.com", "agent")
	client := newOAuthClient(t, store, "client-a", testClock)

	// Created at testClock: an unused code that expired before a later code is
	// created is deleted then; a spent one stays.
	expired := newOAuthCode(t, store, "a", client.ID, ana, testClock.Add(-10*time.Minute))
	spent := newOAuthCode(t, store, "b", client.ID, ana, testClock.Add(-10*time.Minute))
	if _, err := store.ConsumeCode(ctx, spent.CodeHash, testClock.Add(-10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	code := newOAuthCode(t, store, "c", client.ID, ana, testClock)
	if code.ID == 0 {
		t.Fatal("code id not assigned")
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM oauth_codes WHERE id = ?`, expired.ID); n != 0 {
		t.Fatal("expired unused code kept")
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM oauth_codes WHERE id = ?`, spent.ID); n != 1 {
		t.Fatal("spent code deleted")
	}

	got, err := store.ConsumeCode(ctx, code.CodeHash, testClock.Add(10*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, code) {
		t.Fatalf("consumed code = %+v, want the pre-spend row %+v", got, code)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM oauth_codes WHERE id = ? AND used_at = ?`, code.ID, formatTime(testClock.Add(10*time.Second))); n != 1 {
		t.Fatal("code not spent")
	}
	if _, err := store.ConsumeCode(ctx, code.CodeHash, testClock.Add(20*time.Second)); !errors.Is(err, application.ErrOAuthCodeReused) {
		t.Fatalf("second consume err = %v, want ErrOAuthCodeReused", err)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM oauth_codes WHERE id = ? AND used_at = ?`, code.ID, formatTime(testClock.Add(10*time.Second))); n != 1 {
		t.Fatal("reuse moved used_at")
	}
	_, err = store.ConsumeCode(ctx, oauthHash("f"), testClock)
	wantNotFound(t, "unknown code", err)
}

func TestOAuthStoreCodeReuseRevokesGrant(t *testing.T) {
	ctx := context.Background()
	s := newTestDB(t)
	store := s.OAuthStore()
	ana := seedUserRaw(t, s, "Ana", "ana@example.com", "agent")
	client := newOAuthClient(t, store, "client-a", testClock)
	code := newOAuthCode(t, store, "a", client.ID, ana, testClock)
	if _, err := store.ConsumeCode(ctx, code.CodeHash, testClock); err != nil {
		t.Fatal(err)
	}
	g := &domain.OAuthGrant{UserID: ana, ClientID: client.ID, Scopes: code.Scopes, Resource: oauthResource, CreatedAt: testClock}
	access := &domain.OAuthToken{Kind: domain.OAuthTokenAccess, TokenHash: oauthHash("1"), CreatedAt: testClock, ExpiresAt: testClock.Add(time.Hour)}
	refresh := &domain.OAuthToken{Kind: domain.OAuthTokenRefresh, TokenHash: oauthHash("2"), CreatedAt: testClock, ExpiresAt: testClock.Add(720 * time.Hour)}
	if err := store.CreateGrant(ctx, g, code.ID, access, refresh); err != nil {
		t.Fatal(err)
	}
	if g.ID == 0 || access.ID == 0 || refresh.ID == 0 || access.GrantID != g.ID || refresh.GrantID != g.ID {
		t.Fatalf("ids not assigned: grant=%d access=%+v refresh=%+v", g.ID, access, refresh)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM oauth_codes WHERE id = ? AND grant_id = ?`, code.ID, g.ID); n != 1 {
		t.Fatal("code not linked to its grant")
	}
	for _, tok := range []*domain.OAuthToken{access, refresh} {
		got, err := store.TokenByHash(ctx, tok.TokenHash)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, tok) {
			t.Fatalf("token round trip = %+v, want %+v", got, tok)
		}
	}
	_, err := store.TokenByHash(ctx, oauthHash("9"))
	wantNotFound(t, "unknown token", err)

	got, err := store.GrantByID(ctx, g.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := *g
	want.ClientName = client.Name
	if !reflect.DeepEqual(*got, want) {
		t.Fatalf("grant round trip = %+v, want %+v", *got, want)
	}
	_, err = store.GrantByID(ctx, 99999)
	wantNotFound(t, "unknown grant", err)

	if _, err := store.ConsumeCode(ctx, code.CodeHash, testClock.Add(time.Minute)); !errors.Is(err, application.ErrOAuthCodeReused) {
		t.Fatalf("code reuse err = %v", err)
	}
	got, err = store.GrantByID(ctx, g.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.RevokedAt == nil || !got.RevokedAt.Equal(testClock.Add(time.Minute)) || got.RevokedByUserID != nil {
		t.Fatalf("grant after code reuse: revoked_at=%v by=%v, want revoked by tkt", got.RevokedAt, got.RevokedByUserID)
	}
}

func TestOAuthStoreCreateGrantRefusesServiceUser(t *testing.T) {
	ctx := context.Background()
	s := newTestDB(t)
	store := s.OAuthStore()
	svc := seedServiceUserRaw(t, s, "Bot", "bot@service.invalid", "agent")
	client := newOAuthClient(t, store, "client-a", testClock)
	code := &domain.OAuthCode{CodeHash: oauthHash("a"), ClientID: client.ID, UserID: svc, RedirectURI: "https://claude.ai/cb",
		CodeChallenge: strings.Repeat("x", 43), Scopes: []domain.APIKeyScope{domain.ScopeTicketsRead}, Resource: oauthResource,
		CreatedAt: testClock, ExpiresAt: testClock.Add(time.Minute)}
	if err := store.CreateCode(ctx, code); err == nil || !strings.Contains(err.Error(), "an OAuth grant belongs to a person") {
		t.Fatalf("code for a service user err = %v", err)
	}
	g := &domain.OAuthGrant{UserID: svc, ClientID: client.ID, Scopes: []domain.APIKeyScope{domain.ScopeTicketsRead}, Resource: oauthResource, CreatedAt: testClock}
	if err := store.CreateGrant(ctx, g, 1); err == nil || !strings.Contains(err.Error(), "an OAuth grant belongs to a person") {
		t.Fatalf("grant for a service user err = %v", err)
	}
	if n := countRows(t, s, `SELECT (SELECT COUNT(*) FROM oauth_codes) + (SELECT COUNT(*) FROM oauth_grants)`); n != 0 {
		t.Fatalf("refused rows stored: %d", n)
	}
}

func TestOAuthStoreRotateRefreshToken(t *testing.T) {
	ctx := context.Background()
	s := newTestDB(t)
	store := s.OAuthStore()
	ana := seedUserRaw(t, s, "Ana", "ana@example.com", "agent")
	client := newOAuthClient(t, store, "client-a", testClock)
	g, access, refresh := newOAuthGrant(t, store, "a", client.ID, ana, testClock)

	// Two hours later the first access token has expired.
	at := testClock.Add(2 * time.Hour)
	access2 := &domain.OAuthToken{Kind: domain.OAuthTokenAccess, TokenHash: oauthHash("3"), CreatedAt: at, ExpiresAt: at.Add(time.Hour)}
	refresh2 := &domain.OAuthToken{Kind: domain.OAuthTokenRefresh, TokenHash: oauthHash("4"), CreatedAt: at, ExpiresAt: at.Add(720 * time.Hour)}
	if err := store.RotateRefreshToken(ctx, refresh.ID, at, access2, refresh2); err != nil {
		t.Fatal(err)
	}
	if access2.ID == 0 || refresh2.ID == 0 || access2.GrantID != g.ID || refresh2.GrantID != g.ID {
		t.Fatalf("rotated tokens: %+v %+v", access2, refresh2)
	}
	spent, err := store.TokenByHash(ctx, refresh.TokenHash)
	if err != nil {
		t.Fatal(err)
	}
	if spent.UsedAt == nil || !spent.UsedAt.Equal(at) {
		t.Fatalf("rotated refresh used_at = %v", spent.UsedAt)
	}
	_, err = store.TokenByHash(ctx, access.TokenHash)
	wantNotFound(t, "expired access token after rotation", err)
	if got, err := store.TokenByHash(ctx, refresh2.TokenHash); err != nil || !reflect.DeepEqual(got, refresh2) {
		t.Fatalf("new refresh = %+v (err %v)", got, err)
	}
	if got, err := store.GrantByID(ctx, g.ID); err != nil || got.RevokedAt != nil {
		t.Fatalf("grant after rotation = %+v (err %v)", got, err)
	}

	// Presenting the rotated refresh token again revokes the grant and stores nothing.
	reuseAt := at.Add(time.Minute)
	access3 := &domain.OAuthToken{Kind: domain.OAuthTokenAccess, TokenHash: oauthHash("5"), CreatedAt: reuseAt, ExpiresAt: reuseAt.Add(time.Hour)}
	refresh3 := &domain.OAuthToken{Kind: domain.OAuthTokenRefresh, TokenHash: oauthHash("6"), CreatedAt: reuseAt, ExpiresAt: reuseAt.Add(720 * time.Hour)}
	before := countRows(t, s, `SELECT COUNT(*) FROM oauth_tokens`)
	if err := store.RotateRefreshToken(ctx, refresh.ID, reuseAt, access3, refresh3); !errors.Is(err, application.ErrOAuthRefreshReused) {
		t.Fatalf("reuse err = %v, want ErrOAuthRefreshReused", err)
	}
	if after := countRows(t, s, `SELECT COUNT(*) FROM oauth_tokens`); after != before || access3.ID != 0 || refresh3.ID != 0 {
		t.Fatalf("reuse stored tokens: %d -> %d, ids %d %d", before, after, access3.ID, refresh3.ID)
	}
	got, err := store.GrantByID(ctx, g.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.RevokedAt == nil || !got.RevokedAt.Equal(reuseAt) || got.RevokedByUserID != nil {
		t.Fatalf("grant after reuse: revoked_at=%v by=%v", got.RevokedAt, got.RevokedByUserID)
	}

	// An access token is never rotated.
	g2, access4, _ := newOAuthGrant(t, store, "b", client.ID, ana, testClock)
	if err := store.RotateRefreshToken(ctx, access4.ID, at); !errors.Is(err, application.ErrOAuthRefreshReused) {
		t.Fatalf("rotating an access token err = %v", err)
	}
	if got, err := store.GrantByID(ctx, g2.ID); err != nil || got.RevokedAt == nil {
		t.Fatalf("grant after rotating an access token = %+v (err %v)", got, err)
	}
}

func TestOAuthStoreLiveGrants(t *testing.T) {
	ctx := context.Background()
	s := newTestDB(t)
	store := s.OAuthStore()
	ana := seedUserRaw(t, s, "Ana", "ana@example.com", "agent")
	bruno := seedUserRaw(t, s, "Bruno", "bruno@example.com", "agent")
	claude := newOAuthClient(t, store, "claude", testClock)
	cursor := newOAuthClient(t, store, "cursor", testClock)
	now := testClock.Add(time.Hour)

	older, _, _ := newOAuthGrant(t, store, "a", claude.ID, ana, testClock)
	newer, _, _ := newOAuthGrant(t, store, "b", cursor.ID, ana, testClock.Add(time.Minute))
	revoked, _, _ := newOAuthGrant(t, store, "c", claude.ID, ana, testClock)
	if err := store.RevokeGrant(ctx, revoked.ID, ana, testClock); err != nil {
		t.Fatal(err)
	}
	spent, _, spentRefresh := newOAuthGrant(t, store, "d", claude.ID, ana, testClock)
	if _, err := s.db.Exec(`UPDATE oauth_tokens SET used_at = ? WHERE id = ?`, formatTime(testClock), spentRefresh.ID); err != nil {
		t.Fatal(err)
	}
	expired, _, _ := newOAuthGrant(t, store, "e", claude.ID, ana, testClock)
	if _, err := s.db.Exec(`UPDATE oauth_tokens SET expires_at = ? WHERE grant_id = ? AND kind = 'refresh'`, formatTime(now), expired.ID); err != nil {
		t.Fatal(err)
	}
	brunos, _, _ := newOAuthGrant(t, store, "f", claude.ID, bruno, testClock)
	if err := store.TouchGrant(ctx, newer.ID, now); err != nil {
		t.Fatal(err)
	}

	live, err := store.ListLiveGrants(ctx, ana, now)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, g := range live {
		got = append(got, itoa(g.ID)+":"+g.ClientName)
	}
	want := []string{itoa(newer.ID) + ":" + cursor.Name, itoa(older.ID) + ":" + claude.Name}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("live grants = %q, want %q (excluded revoked=%d spent=%d expired=%d)", got, want, revoked.ID, spent.ID, expired.ID)
	}
	if live[0].LastUsedAt == nil || !live[0].LastUsedAt.Equal(now) || !reflect.DeepEqual(live[0].Scopes, newer.Scopes) || live[0].Resource != oauthResource {
		t.Fatalf("listed grant = %+v", live[0])
	}
	if others, err := store.ListLiveGrants(ctx, bruno, now); err != nil || len(others) != 1 || others[0].ID != brunos.ID {
		t.Fatalf("bruno's live grants = %+v (err %v)", others, err)
	}

	wantNotFound(t, "revoke someone else's grant", store.RevokeGrant(ctx, brunos.ID, ana, now))
	if got, err := store.GrantByID(ctx, brunos.ID); err != nil || got.RevokedAt != nil {
		t.Fatalf("someone else's grant changed: %+v (err %v)", got, err)
	}
	wantNotFound(t, "revoke an already revoked grant", store.RevokeGrant(ctx, revoked.ID, ana, now))
	if got, err := store.GrantByID(ctx, revoked.ID); err != nil || !got.RevokedAt.Equal(testClock) {
		t.Fatalf("revoked grant changed: %+v (err %v)", got, err)
	}
	wantNotFound(t, "revoke an unknown grant", store.RevokeGrant(ctx, 99999, ana, now))

	if err := store.RevokeGrant(ctx, older.ID, ana, now); err != nil {
		t.Fatal(err)
	}
	got2, err := store.GrantByID(ctx, older.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got2.RevokedAt == nil || !got2.RevokedAt.Equal(now) || got2.RevokedByUserID == nil || *got2.RevokedByUserID != ana {
		t.Fatalf("revoked grant: at=%v by=%v", got2.RevokedAt, got2.RevokedByUserID)
	}
	if live, err := store.ListLiveGrants(ctx, ana, now); err != nil || len(live) != 1 || live[0].ID != newer.ID {
		t.Fatalf("live grants after revoke = %+v (err %v)", live, err)
	}
}
