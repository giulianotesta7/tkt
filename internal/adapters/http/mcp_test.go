package httpadapter

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// The /mcp tests (issue #493, S7-S9) drive the real go-sdk client over the
// real harness. The client's HTTPClient hands each request to the wrapped
// handler in process, so no socket is opened, and every token is minted
// through the real OAuthService (register, prepare, approve, token) over the
// harness database.

const (
	mcpIssuer   = "https://tkt.example"
	mcpEndpoint = mcpIssuer + "/mcp"
	mcpAllScope = "catalog:read tickets:read tickets:write comments:write comments:internal"
	mcpMetadata = mcpIssuer + "/.well-known/oauth-protected-resource/mcp"
)

type mcpFixture struct {
	h       *harness
	oauth   *application.OAuthService
	access  *application.APIAccessService
	api     *APIHandlers
	mcp     *MCPHandlers
	handler http.Handler
	root    *domain.User
}

func newMCPFixture(t *testing.T) *mcpFixture {
	t.Helper()
	h := newHarness(t)
	s := h.store
	if err := s.EmailSettingsStore().SetEmailSettings(t.Context(), application.EmailSettings{BaseURL: mcpIssuer}); err != nil {
		t.Fatal(err)
	}
	root := seedUserRole(t, s, "Root", "root@tkt.test", domain.RoleRoot)
	oauth := application.NewOAuthService(s.OAuthStore(), s.UserStore(), s.EmailSettingsStore(), h.clock, nil)
	if err := oauth.SetMCPEnabled(t.Context(), *root, true); err != nil {
		t.Fatal(err)
	}
	access := application.NewAPIAccessService(s.APIAccessStore(), s.UserStore(), h.clock, nil)
	follows := application.NewFollowService(s.TicketStore(), s.UserStore(), s.TicketFollowerStore(), h.clock).WithDesks(s.DeskStore())
	api := NewAPIHandlers(access, h.clock).
		WithReads(APIReadServices{Tickets: h.tickets, Search: h.search, Categories: h.categories, Users: h.users, Catalog: application.NewCatalogService(s.CatalogStore(), s.CategoryStore(), h.clock), Workflows: h.workflows, Versions: s.WorkflowVersionStore(), Runs: s.WorkflowRunStore()}).
		WithWrites(APIWriteServices{Comments: h.comments, Runner: application.NewWorkflowRunner(h.clock).WithFollowers(follows), Workflow: s.WorkflowUnitOfWork()})
	m := NewMCPHandlers(oauth, api)
	return &mcpFixture{h: h, oauth: oauth, access: access, api: api, mcp: m, root: root,
		handler: m.Branch(api.Branch(h.mw.Wrap(NewBodyLimitMiddleware().Wrap(h.mux))))}
}

// mint runs the whole authorization code flow for person and answers the
// access token and its grant id.
func (f *mcpFixture) mint(t *testing.T, person *domain.User, scope string) (string, int64) {
	t.Helper()
	ctx := t.Context()
	const redirect = "http://127.0.0.1/callback"
	client, err := f.oauth.RegisterClient(ctx, application.ClientRegistration{ClientName: "Probe", RedirectURIs: []string{redirect}})
	if err != nil {
		t.Fatal(err)
	}
	verifier := strings.Repeat("v", 43)
	sum := sha256.Sum256([]byte(verifier))
	p, err := f.oauth.PrepareAuthorization(ctx, application.AuthorizeRequest{
		ClientID: client.ClientID, RedirectURI: redirect, ResponseType: "code", State: "state",
		CodeChallenge: base64.RawURLEncoding.EncodeToString(sum[:]), CodeChallengeMethod: "S256", Scope: scope,
	})
	if err != nil {
		t.Fatal(err)
	}
	location, err := f.oauth.Approve(ctx, *person, *p)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(location)
	if err != nil || u.Query().Get("code") == "" {
		t.Fatalf("approve redirect %q: %v", location, err)
	}
	resp, err := f.oauth.Token(ctx, application.TokenRequest{GrantType: "authorization_code", Code: u.Query().Get("code"), RedirectURI: redirect, ClientID: client.ClientID, CodeVerifier: verifier})
	if err != nil {
		t.Fatal(err)
	}
	grants, err := f.oauth.ListGrants(ctx, *person)
	if err != nil || len(grants) == 0 {
		t.Fatalf("grants = %v, %v", grants, err)
	}
	return resp.AccessToken, grants[0].ID
}

// inProcess is the client's transport: it serves each request with the
// wrapped handler and decorates it first.
type inProcess struct {
	handler  http.Handler
	decorate func(*http.Request)
}

func (rt inProcess) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	if r.Body == nil {
		r.Body = http.NoBody
	}
	if rt.decorate != nil {
		rt.decorate(r)
	}
	w := httptest.NewRecorder()
	rt.handler.ServeHTTP(w, r)
	res := w.Result()
	res.Request = r
	return res, nil
}

func (f *mcpFixture) connect(t *testing.T, token string, decorate func(*http.Request)) (*mcp.ClientSession, error) {
	t.Helper()
	transport := &mcp.StreamableClientTransport{
		Endpoint:             mcpEndpoint,
		DisableStandaloneSSE: true,
		MaxRetries:           -1,
		HTTPClient: &http.Client{Transport: inProcess{handler: f.handler, decorate: func(r *http.Request) {
			r.Header.Set("Authorization", "Bearer "+token)
			if decorate != nil {
				decorate(r)
			}
		}}},
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "probe", Version: "1"}, nil).Connect(t.Context(), transport, nil)
	if err == nil {
		t.Cleanup(func() { _ = cs.Close() })
	}
	return cs, err
}

func (f *mcpFixture) session(t *testing.T, token string) *mcp.ClientSession {
	t.Helper()
	cs, err := f.connect(t, token, nil)
	if err != nil {
		t.Fatal(err)
	}
	return cs
}

// post sends one raw JSON-RPC body to /mcp.
func (f *mcpFixture) post(auth, origin, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json, text/event-stream")
	if auth != "" {
		r.Header.Set("Authorization", auth)
	}
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	return w
}

func mcpCall(name, args string) string {
	return `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + name + `","arguments":` + args + `}}`
}

// counts are the domain rows plus the grants' last use, so a refusal is
// proven to write nothing at all.
func (f *mcpFixture) counts(t *testing.T) [4]int64 {
	t.Helper()
	db := f.h.rawDB(t)
	return [4]int64{
		scanOneInt(t, db, "SELECT COUNT(*) FROM tickets"), scanOneInt(t, db, "SELECT COUNT(*) FROM comments"),
		scanOneInt(t, db, "SELECT COUNT(*) FROM audit_events"), scanOneInt(t, db, "SELECT COUNT(*) FROM oauth_grants WHERE last_used_at IS NOT NULL"),
	}
}

func callTool(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return res
}

func toolText(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if text, ok := c.(*mcp.TextContent); ok {
			b.WriteString(text.Text)
		}
	}
	return b.String()
}

// toolObject answers a successful tool call's structured content.
func toolObject(t *testing.T, res *mcp.CallToolResult) map[string]any {
	t.Helper()
	if res.IsError {
		t.Fatalf("tool failed: %s", toolText(res))
	}
	out, ok := res.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("structured content = %#v", res.StructuredContent)
	}
	var fromText map[string]any
	if err := json.Unmarshal([]byte(toolText(res)), &fromText); err != nil || len(fromText) != len(out) {
		t.Fatalf("text content %q does not carry the result: %v", toolText(res), err)
	}
	return out
}

// toolProblem asserts a failed tool call reports the problem's title and
// detail as text.
func toolProblem(t *testing.T, res *mcp.CallToolResult, kind apiProblemKind, wants ...string) {
	t.Helper()
	text := toolText(res)
	if !res.IsError || !strings.Contains(text, kind.Title) || !strings.Contains(text, strconv.Itoa(kind.Status)) {
		t.Fatalf("problem = %v %q, want %s", res.IsError, text, kind.Title)
	}
	for _, want := range wants {
		if !strings.Contains(text, want) {
			t.Fatalf("problem %q lacks %q", text, want)
		}
	}
}

func TestMCPChallengesWithoutAValidToken(t *testing.T) {
	f := newMCPFixture(t)
	person := seedUserRole(t, f.h.store, "Pat", "pat@tkt.test", domain.RoleUser)
	token, _ := f.mint(t, person, "")
	counts := f.counts(t)
	discovery := `Bearer resource_metadata="` + mcpMetadata + `", scope="` + mcpAllScope + `"`
	invalid := `Bearer error="invalid_token", resource_metadata="` + mcpMetadata + `", scope="` + mcpAllScope + `"`
	for _, tc := range []struct{ name, auth, want string }{
		{"missing", "", discovery},
		{"scheme", "Basic " + token, discovery},
		{"unknown", "Bearer tkta_" + strings.Repeat("0", 64), invalid},
		{"malformed", "Bearer tkta_short", invalid},
		{"api_key_shape", "Bearer tkt_abc_" + strings.Repeat("0", 64), invalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := f.post(tc.auth, "", mcpCall("whoami", `{}`))
			if w.Code != http.StatusUnauthorized || w.Header().Get("WWW-Authenticate") != tc.want {
				t.Fatalf("response = %d %q %s", w.Code, w.Header().Get("WWW-Authenticate"), w.Body.String())
			}
		})
	}
	if f.counts(t) != counts {
		t.Fatal("a refused token wrote data")
	}
	if err := f.oauth.RevokeGrant(t.Context(), *person, mustGrant(t, f, person)); err != nil {
		t.Fatal(err)
	}
	counts = f.counts(t)
	if w := f.post("Bearer "+token, "", mcpCall("whoami", `{}`)); w.Code != http.StatusUnauthorized || w.Header().Get("WWW-Authenticate") != invalid {
		t.Fatalf("revoked = %d %q", w.Code, w.Header().Get("WWW-Authenticate"))
	}
	if f.counts(t) != counts {
		t.Fatal("a refused token wrote data")
	}
}

func mustGrant(t *testing.T, f *mcpFixture, person *domain.User) int64 {
	t.Helper()
	grants, err := f.oauth.ListGrants(t.Context(), *person)
	if err != nil || len(grants) != 1 {
		t.Fatalf("grants = %v, %v", grants, err)
	}
	return grants[0].ID
}

func TestMCPSwitchOffAnswers404(t *testing.T) {
	f := newMCPFixture(t)
	person := seedUserRole(t, f.h.store, "Pat", "pat@tkt.test", domain.RoleUser)
	token, _ := f.mint(t, person, "")
	if err := f.oauth.SetMCPEnabled(t.Context(), *f.root, false); err != nil {
		t.Fatal(err)
	}
	counts := f.counts(t)
	for _, auth := range []string{"", "Bearer " + token, "Bearer bad"} {
		w := f.post(auth, "https://evil.example", mcpCall("whoami", `{}`))
		if w.Code != http.StatusNotFound || w.Header().Get("WWW-Authenticate") != "" || w.Header().Get("Content-Type") != "application/json" {
			t.Fatalf("switch off = %d %v %s", w.Code, w.Header(), w.Body.String())
		}
	}
	if f.counts(t) != counts {
		t.Fatal("switch-off refusal wrote data")
	}
	// Only the exact path belongs to MCP; the rest still reaches the browser.
	r := httptest.NewRequest(http.MethodGet, "/mcp/other", nil)
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	wantRedirect(t, w, http.StatusSeeOther, "/login")
}

func TestMCPOrigin(t *testing.T) {
	f := newMCPFixture(t)
	person := seedUserRole(t, f.h.store, "Pat", "pat@tkt.test", domain.RoleUser)
	token, _ := f.mint(t, person, "")
	counts := f.counts(t)
	for _, origin := range []string{"https://evil.example", "http://tkt.example", "https://tkt.example:8443", "null"} {
		if w := f.post("Bearer "+token, origin, mcpCall("whoami", `{}`)); w.Code != http.StatusForbidden {
			t.Fatalf("origin %q = %d %s", origin, w.Code, w.Body.String())
		}
	}
	if f.counts(t) != counts {
		t.Fatal("an Origin refusal wrote data")
	}
	if _, err := f.connect(t, token, func(r *http.Request) { r.Header.Set("Origin", "https://evil.example") }); err == nil {
		t.Fatal("client connected from a foreign origin")
	}
	cs, err := f.connect(t, token, func(r *http.Request) { r.Header.Set("Origin", "https://TKT.example") })
	if err != nil {
		t.Fatal(err)
	}
	if me := toolObject(t, callTool(t, cs, "whoami", nil)); me["id"] != float64(person.ID) {
		t.Fatalf("whoami = %#v", me)
	}
}

func TestMCPListsTheTenTools(t *testing.T) {
	f := newMCPFixture(t)
	person := seedUserRole(t, f.h.store, "Pat", "pat@tkt.test", domain.RoleUser)
	token, _ := f.mint(t, person, "")
	res, err := f.session(t, token).ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
		if tool.Description == "" || tool.InputSchema == nil {
			t.Fatalf("tool %s lacks a description or schema", tool.Name)
		}
	}
	slices.Sort(names)
	want := []string{"add_comment", "complete_workflow_step", "confirm_resolution", "create_ticket", "get_ticket", "list_categories", "reject_resolution", "search_tickets", "transition_ticket", "whoami"}
	if !slices.Equal(names, want) {
		t.Fatalf("tools = %v", names)
	}
}

// TestMCPToolContextCarriesThePrincipal pins the go-sdk fact the dispatch
// relies on: in stateless mode a tool handler's context descends from the HTTP
// request's, so the values Branch stamps reach the tool.
func TestMCPToolContextCarriesThePrincipal(t *testing.T) {
	f := newMCPFixture(t)
	person := seedUserRole(t, f.h.store, "Pat", "pat@tkt.test", domain.RoleUser)
	token, grantID := f.mint(t, person, "")
	type seen struct {
		User  int64 `json:"user"`
		Grant int64 `json:"grant"`
		Scope bool  `json:"scope"`
	}
	mcp.AddTool(f.mcp.server, &mcp.Tool{Name: "probe", Description: "context probe"}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, seen, error) {
		var out seen
		if u := userFromContext(ctx); u != nil {
			out.User = u.ID
		}
		out.Grant, _ = application.OAuthGrantIDFrom(ctx)
		p, _ := ctx.Value(ctxKeyAPIPrincipal{}).(*application.APIPrincipal)
		out.Scope = p != nil && p.HasScope(domain.ScopeTicketsRead)
		return nil, out, nil
	})
	got := toolObject(t, callTool(t, f.session(t, token), "probe", nil))
	if got["user"] != float64(person.ID) || got["grant"] != float64(grantID) || got["scope"] != true {
		t.Fatalf("tool context = %#v", got)
	}
}

func TestMCPToolsAsAUser(t *testing.T) {
	f := newMCPFixture(t)
	person := seedUserRole(t, f.h.store, "Pat", "pat@tkt.test", domain.RoleUser)
	access, err := f.h.categories.Create(t.Context(), "Access")
	if err != nil {
		t.Fatal(err)
	}
	f.h.publishWorkflow(t, access.ID, domain.WorkflowDefinition{
		{Type: domain.StepForm, Form: &domain.FormStep{Actor: domain.FormActorRequester, Fields: []domain.FormField{{Key: "host", Label: "Host", Kind: domain.FieldShortText, Required: true}}}},
		{Type: domain.StepManualTask, ManualTask: &domain.ManualTaskStep{Instructions: "handle"}},
	})
	// A user's role drops comments:internal at consent.
	token, _ := f.mint(t, person, "")
	cs := f.session(t, token)

	me := toolObject(t, callTool(t, cs, "whoami", nil))
	if me["id"] != float64(person.ID) || me["name"] != "Pat" || me["role"] != "user" || len(me["scopes"].([]any)) != 4 {
		t.Fatalf("whoami = %#v", me)
	}
	categories := toolObject(t, callTool(t, cs, "list_categories", nil))
	if items := categories["items"].([]any); len(items) != 2 {
		t.Fatalf("categories = %#v", categories)
	}

	created := toolObject(t, callTool(t, cs, "create_ticket", map[string]any{"category_id": access.ID, "title": "Laptop access", "description": "Cannot sign in", "priority": "high"}))
	id := int64(created["id"].(float64))
	if created["requester"].(map[string]any)["id"] != float64(person.ID) || created["title"] != "Laptop access" {
		t.Fatalf("created = %#v", created)
	}
	toolProblem(t, callTool(t, cs, "create_ticket", map[string]any{"category_id": access.ID, "title": " ", "priority": "high"}), problemValidation, "title")

	found := toolObject(t, callTool(t, cs, "search_tickets", map[string]any{"q": "Laptop"}))
	if items := found["items"].([]any); len(items) != 1 || items[0].(map[string]any)["id"] != float64(id) {
		t.Fatalf("search = %#v", found)
	}
	toolProblem(t, callTool(t, cs, "search_tickets", map[string]any{"state": []string{"bogus"}}), problemValidation, "unknown ticket state", "state")

	detail := toolObject(t, callTool(t, cs, "get_ticket", map[string]any{"ticket_id": id}))
	pending := detail["workflow"].(map[string]any)["pending_step"].(map[string]any)
	if detail["description"] != "Cannot sign in" || pending["position"] != float64(1) {
		t.Fatalf("detail = %#v", detail)
	}
	toolProblem(t, callTool(t, cs, "get_ticket", map[string]any{"ticket_id": 999999}), problemNotFound)

	toolProblem(t, callTool(t, cs, "complete_workflow_step", map[string]any{"ticket_id": id, "position": 1, "answers": []map[string]any{}}), problemValidation)
	advanced := toolObject(t, callTool(t, cs, "complete_workflow_step", map[string]any{"ticket_id": id, "position": 1, "answers": []map[string]any{{"position": 0, "values": []string{"host01"}}}}))
	if advanced["workflow"].(map[string]any)["current_position"] != float64(2) {
		t.Fatalf("advanced = %#v", advanced)
	}
	toolProblem(t, callTool(t, cs, "complete_workflow_step", map[string]any{"ticket_id": id, "position": 1, "answers": []map[string]any{{"position": 0, "values": []string{"host01"}}}}), problemRejected, "workflow position conflict")

	comment := toolObject(t, callTool(t, cs, "add_comment", map[string]any{"ticket_id": id, "body": "Second floor"}))
	if comment["visibility"] != "public" || comment["author"].(map[string]any)["id"] != float64(person.ID) {
		t.Fatalf("comment = %#v", comment)
	}
	toolProblem(t, callTool(t, cs, "add_comment", map[string]any{"ticket_id": id, "body": "  "}), problemValidation, "body")

	toolProblem(t, callTool(t, cs, "transition_ticket", map[string]any{"ticket_id": id, "state": "in_progress"}), problemForbidden)
	toolProblem(t, callTool(t, cs, "confirm_resolution", map[string]any{"ticket_id": id}), problemRejected)
	// Someone else's ticket is outside the person's scope.
	toolProblem(t, callTool(t, cs, "reject_resolution", map[string]any{"ticket_id": f.h.seedTicket(t, "Not mine", nil).ID}), problemNotFound)

	for _, action := range []string{"confirm_resolution", "reject_resolution"} {
		ticket, err := f.h.tickets.Create(t.Context(), *person, application.CreateTicketInput{Title: action, CategoryID: f.h.bugCategory.ID, Priority: domain.PriorityMedium})
		if err != nil {
			t.Fatal(err)
		}
		f.h.seedTransition(t, ticket.ID, domain.StateInProgress, "")
		f.h.seedTransition(t, ticket.ID, domain.StateResolved, "")
		out := toolObject(t, callTool(t, cs, action, map[string]any{"ticket_id": ticket.ID}))
		want := map[string]string{"confirm_resolution": "closed", "reject_resolution": "in_progress"}[action]
		if out["state"] != want {
			t.Fatalf("%s = %#v", action, out)
		}
	}
}

func TestMCPToolsAsAnAgent(t *testing.T) {
	f := newMCPFixture(t)
	agent := seedUserRole(t, f.h.store, "Ava", "ava@tkt.test", domain.RoleAgent)
	ticket := f.h.seedTicket(t, "Printer jam", nil)
	f.h.assignTicket(t, ticket.ID, agent.ID)
	token, grantID := f.mint(t, agent, "")
	cs := f.session(t, token)

	if me := toolObject(t, callTool(t, cs, "whoami", nil)); me["role"] != "agent" || len(me["scopes"].([]any)) != 5 {
		t.Fatalf("whoami = %#v", me)
	}
	toolProblem(t, callTool(t, cs, "transition_ticket", map[string]any{"ticket_id": ticket.ID, "state": "closed"}), problemRejected)
	moved := toolObject(t, callTool(t, cs, "transition_ticket", map[string]any{"ticket_id": ticket.ID, "state": "in_progress", "reason": "Looking"}))
	if moved["state"] != "in_progress" {
		t.Fatalf("transition = %#v", moved)
	}
	// S9: the MCP transition is attributed to the person, with the grant as
	// provenance and no API key. AuditStore exposes neither id.
	db := f.h.rawDB(t)
	actor, _ := scanOneNullableInt(t, db, "SELECT actor_user_id FROM audit_events WHERE ticket_id = ? ORDER BY id DESC LIMIT 1", ticket.ID)
	grant, grantSet := scanOneNullableInt(t, db, "SELECT oauth_grant_id FROM audit_events WHERE ticket_id = ? ORDER BY id DESC LIMIT 1", ticket.ID)
	_, keySet := scanOneNullableInt(t, db, "SELECT api_key_id FROM audit_events WHERE ticket_id = ? ORDER BY id DESC LIMIT 1", ticket.ID)
	if actor != agent.ID || !grantSet || grant != grantID || keySet {
		t.Fatalf("audit actor/grant/key = %d/%d(%v)/%v", actor, grant, grantSet, keySet)
	}

	internal := toolObject(t, callTool(t, cs, "add_comment", map[string]any{"ticket_id": ticket.ID, "body": "Staff note", "internal": true}))
	if internal["visibility"] != "internal" || internal["author"].(map[string]any)["id"] != float64(agent.ID) {
		t.Fatalf("internal comment = %#v", internal)
	}
	detail := toolObject(t, callTool(t, cs, "get_ticket", map[string]any{"ticket_id": ticket.ID}))
	if len(detail["comments"].([]any)) != 1 || detail["workflow"].(map[string]any)["pending_step"].(map[string]any)["kind"] != "manual_task" {
		t.Fatalf("detail = %#v", detail)
	}
	toolProblem(t, callTool(t, cs, "complete_workflow_step", map[string]any{"ticket_id": ticket.ID, "position": 2}), problemRejected)
	done := toolObject(t, callTool(t, cs, "complete_workflow_step", map[string]any{"ticket_id": ticket.ID, "position": 1, "solution": "Cleared the tray"}))
	if done["workflow"].(map[string]any)["pending_step"] != nil {
		t.Fatalf("completed = %#v", done)
	}
	// The agent reaches its assigned ticket, but not the requester's actions.
	toolProblem(t, callTool(t, cs, "confirm_resolution", map[string]any{"ticket_id": ticket.ID}), problemForbidden, "only the ticket requester")
	found := toolObject(t, callTool(t, cs, "search_tickets", map[string]any{"state": []string{"in_progress", "resolved"}}))
	if len(found["items"].([]any)) != 1 {
		t.Fatalf("search = %#v", found)
	}
	toolProblem(t, callTool(t, cs, "search_tickets", map[string]any{"cursor": "!"}), problemBadRequest)
}

func TestMCPMissingScopeAsksForStepUp(t *testing.T) {
	f := newMCPFixture(t)
	agent := seedUserRole(t, f.h.store, "Ava", "ava@tkt.test", domain.RoleAgent)
	ticket := f.h.seedTicket(t, "Printer jam", nil)
	f.h.assignTicket(t, ticket.ID, agent.ID)
	token, _ := f.mint(t, agent, "tickets:read comments:write")
	counts := f.counts(t)
	for _, tc := range []struct{ name, args, scope string }{
		{"create_ticket", `{"category_id":1,"title":"x","priority":"low"}`, "tickets:read tickets:write comments:write"},
		{"list_categories", `{}`, "catalog:read tickets:read comments:write"},
		{"add_comment", `{"ticket_id":` + strconv.FormatInt(ticket.ID, 10) + `,"body":"x","internal":true}`, "tickets:read comments:write comments:internal"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := f.post("Bearer "+token, "", mcpCall(tc.name, tc.args))
			want := `Bearer error="insufficient_scope", scope="` + tc.scope + `", resource_metadata="` + mcpMetadata + `"`
			if w.Code != http.StatusForbidden || w.Header().Get("WWW-Authenticate") != want {
				t.Fatalf("step-up = %d %q %s", w.Code, w.Header().Get("WWW-Authenticate"), w.Body.String())
			}
		})
	}
	cs := f.session(t, token)
	if _, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "transition_ticket", Arguments: map[string]any{"ticket_id": ticket.ID, "state": "in_progress"}}); err == nil {
		t.Fatal("a call outside the granted scopes succeeded")
	}
	// The grant's last use is recorded by the accepted token; nothing else.
	if got := f.counts(t); got[0] != counts[0] || got[1] != counts[1] || got[2] != counts[2] {
		t.Fatalf("a step-up refusal wrote data: %v -> %v", counts, got)
	}
	// A scope the grant holds still works on the same token. The refused call
	// closed the go-sdk client session, so this one opens another.
	public := toolObject(t, callTool(t, f.session(t, token), "add_comment", map[string]any{"ticket_id": ticket.ID, "body": "Public"}))
	if public["visibility"] != "public" {
		t.Fatalf("public comment = %#v", public)
	}
}

func TestMCPRateLimitPerGrant(t *testing.T) {
	f := newMCPFixture(t)
	person := seedUserRole(t, f.h.store, "Pat", "pat@tkt.test", domain.RoleUser)
	token, grantID := f.mint(t, person, "")
	other, _ := f.mint(t, person, "")
	// An API key sharing the grant's numeric id keeps its own bucket.
	if err := f.access.SetAPIEnabled(t.Context(), *f.root, true); err != nil {
		t.Fatal(err)
	}
	service, err := f.access.CreateServiceUser(t.Context(), *f.root, "Integration", domain.RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	key, keyToken, err := f.access.CreateAPIKey(t.Context(), *f.root, application.CreateAPIKeyInput{ServiceUserID: service.ID, Name: "Probe", Scopes: []string{"tickets:read"}})
	if err != nil || key.ID != grantID {
		t.Fatalf("key %v (want id %d): %v", key, grantID, err)
	}
	for i := 0; i < 60; i++ {
		if w := f.post("Bearer "+token, "", mcpCall("whoami", `{}`)); w.Code == http.StatusTooManyRequests {
			t.Fatalf("request %d limited", i+1)
		}
	}
	counts := f.counts(t)
	w := f.post("Bearer "+token, "", mcpCall("whoami", `{}`))
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") != "1" {
		t.Fatalf("limit = %d %v %s", w.Code, w.Header(), w.Body.String())
	}
	if f.counts(t) != counts {
		t.Fatal("rate limiting wrote data")
	}
	if me := toolObject(t, callTool(t, f.session(t, other), "whoami", nil)); me["id"] != float64(person.ID) {
		t.Fatal("another grant shared the bucket")
	}
	r := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	r.Header.Set("Authorization", "Bearer "+keyToken)
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("the API key shared the grant's bucket: %d", rec.Code)
	}
	f.h.clock.Advance(time.Minute)
	if w := f.post("Bearer "+token, "", mcpCall("whoami", `{}`)); w.Code == http.StatusTooManyRequests {
		t.Fatal("bucket did not refill")
	}
}

// TestMCPTokenRefusedOnAPI pins S6: an access token is accepted only on /mcp.
// The same handler still answers a service key (preservation).
func TestMCPTokenRefusedOnAPI(t *testing.T) {
	f := newMCPFixture(t)
	person := seedUserRole(t, f.h.store, "Pat", "pat@tkt.test", domain.RoleUser)
	token, _ := f.mint(t, person, "")
	if err := f.access.SetAPIEnabled(t.Context(), *f.root, true); err != nil {
		t.Fatal(err)
	}
	service, err := f.access.CreateServiceUser(t.Context(), *f.root, "Integration", domain.RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	_, keyToken, err := f.access.CreateAPIKey(t.Context(), *f.root, application.CreateAPIKeyInput{ServiceUserID: service.ID, Name: "Probe", Scopes: []string{"tickets:read"}})
	if err != nil {
		t.Fatal(err)
	}
	get := func(auth string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
		r.Header.Set("Authorization", auth)
		w := httptest.NewRecorder()
		f.handler.ServeHTTP(w, r)
		return w
	}
	counts := f.counts(t)
	assertAPIProblem(t, get("Bearer "+token), problemUnauthorized, "/api/v1/me")
	if f.counts(t) != counts {
		t.Fatal("an OAuth token on the API wrote data")
	}
	out := apiObject(t, get("Bearer "+keyToken))
	if out["id"] != float64(service.ID) || out["name"] != "Integration" {
		t.Fatalf("service key me = %#v", out)
	}
	// And the key is not an MCP credential.
	if w := f.post("Bearer "+keyToken, "", mcpCall("whoami", `{}`)); w.Code != http.StatusUnauthorized {
		t.Fatalf("API key on /mcp = %d", w.Code)
	}
}
