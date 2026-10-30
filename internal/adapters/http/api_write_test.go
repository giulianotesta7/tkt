package httpadapter

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

func apiWriteObject(t *testing.T, w *httptest.ResponseRecorder, status int) map[string]any {
	t.Helper()
	if w.Code != status || w.Header().Get("Content-Type") != "application/json" || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("response = %d %v %s", w.Code, w.Header(), w.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func apiWriteKey(t *testing.T, f *apiFixture, user *domain.User, scopes ...string) (*domain.APIKey, string) {
	t.Helper()
	key, token, err := f.svc.CreateAPIKey(t.Context(), *f.root, application.CreateAPIKeyInput{ServiceUserID: user.ID, Name: "Write", Scopes: scopes})
	if err != nil {
		t.Fatal(err)
	}
	return key, token
}

// AuditStore does not expose api_key_id; inspect that fact through rawDB.
func apiAuditKey(t *testing.T, f *apiFixture, ticketID, keyID, actorID int64) {
	t.Helper()
	db := f.h.rawDB(t)
	got, valid := scanOneNullableInt(t, db, "SELECT api_key_id FROM audit_events WHERE ticket_id = ? ORDER BY id DESC LIMIT 1", ticketID)
	actor, actorValid := scanOneNullableInt(t, db, "SELECT actor_user_id FROM audit_events WHERE ticket_id = ? ORDER BY id DESC LIMIT 1", ticketID)
	if !valid || got != keyID || !actorValid || actor != actorID {
		t.Fatalf("audit key/actor = %d/%d (valid %v/%v), want %d/%d", got, actor, valid, actorValid, keyID, actorID)
	}
}

func TestAPIWriteCreateAndWorkflow(t *testing.T) {
	f := newAPIFixture(t)
	key, token := apiWriteKey(t, f, f.user, "tickets:write", "tickets:read")
	f.h.publishWorkflow(t, f.h.bugCategory.ID, domain.WorkflowDefinition{
		{Type: domain.StepForm, Form: &domain.FormStep{Actor: domain.FormActorRequester, Fields: []domain.FormField{{Key: "host", Label: "Host", Kind: domain.FieldShortText, Required: true}}}},
		{Type: domain.StepForm, Form: &domain.FormStep{Actor: domain.FormActorRequester, Fields: []domain.FormField{{Key: "next", Label: "Next", Kind: domain.FieldLongText}}}},
	})
	body := fmt.Sprintf(`{"category_id":%d,"title":"API created","description":"Details","priority":"medium"}`, f.h.bugCategory.ID)
	w := f.request("POST", "/api/v1/tickets", "Bearer "+token, body, "application/json")
	out := apiWriteObject(t, w, 201)
	id := int64(out["id"].(float64))
	path := fmt.Sprintf("/api/v1/tickets/%d", id)
	if w.Header().Get("Location") != path || out["assignee"] != nil || out["requester"].(map[string]any)["id"] != float64(f.user.ID) {
		t.Fatalf("created = %#v, location %q", out, w.Header().Get("Location"))
	}
	apiAuditKey(t, f, id, key.ID, f.user.ID)
	if _, present := out["description"]; present {
		t.Fatal("create returned detail instead of summary")
	}
	for _, detail := range []map[string]any{apiObject(t, f.request("GET", path, "Bearer "+token, "", ""))} {
		workflow := detail["workflow"].(map[string]any)
		pending := workflow["pending_step"].(map[string]any)
		if workflow["current_position"] != float64(1) || pending["position"] != float64(1) || pending["kind"] != "form" || len(pending["fields"].([]any)) != 1 {
			t.Fatalf("workflow = %#v", workflow)
		}
	}
	complete := path + "/workflow/steps/1/complete"
	before := f.domainCounts(t)
	for _, bad := range []string{`{"answers":[{"position":0,"values":["a","b"]}]}`, `{"answers":[{"position":0,"values":["a"]},{"position":0,"values":["b"]}]}`, `{"answers":[{"position":2,"values":["a"]}]}`, `{"answers":[]}`, `{"answers":[{"position":-1,"values":["a"]}]}`, `{"solution":"not a manual task"}`} {
		assertAPIProblem(t, f.request("POST", complete, "Bearer "+token, bad, "application/json"), problemValidation, complete)
		if f.domainCounts(t) != before {
			t.Fatal("invalid answers wrote data")
		}
	}
	out = apiObject(t, f.request("POST", complete, "Bearer "+token, `{"answers":[{"position":0,"values":["host01"]}]}`, "application/json"))
	if out["workflow"].(map[string]any)["current_position"] != float64(2) || out["assignee"] != nil {
		t.Fatalf("advanced = %#v", out)
	}
	apiAuditKey(t, f, id, key.ID, f.user.ID)
	before = f.domainCounts(t)
	w = f.request("POST", complete, "Bearer "+token, `{"answers":[{"position":0,"values":["host01"]}]}`, "application/json")
	assertAPIProblem(t, w, problemRejected, complete)
	if !strings.Contains(w.Body.String(), "workflow position conflict") || f.domainCounts(t) != before {
		t.Fatalf("stale completion = %s", w.Body.String())
	}
}

func TestAPIWriteTransitionCommentsAndAudit(t *testing.T) {
	f := newAPIFixture(t)
	ticket := apiOwnTicket(t, f, "Audit subject")
	agent, _ := apiAgent(t, f)
	key, token := apiWriteKey(t, f, agent, "tickets:all", "tickets:write", "comments:internal")
	path := fmt.Sprintf("/api/v1/tickets/%d", ticket.ID)
	out := apiObject(t, f.request("POST", path+"/transition", "Bearer "+token, `{"state":"in_progress"}`, "application/json"))
	if out["state"] != "in_progress" || out["assignee"] != nil {
		t.Fatalf("transition = %#v", out)
	}
	apiAuditKey(t, f, ticket.ID, key.ID, agent.ID)
	before := f.domainCounts(t)
	comment := apiWriteObject(t, f.request("POST", path+"/comments", "Bearer "+token, `{"body":"Staff note","visibility":"internal"}`, "application/json"), 201)
	if comment["author"].(map[string]any)["id"] != float64(agent.ID) || comment["visibility"] != "internal" || comment["body"] != "Staff note" {
		t.Fatalf("comment = %#v", comment)
	}
	after := f.domainCounts(t)
	if after[0] != before[0] || after[1] != before[1]+1 || after[2] != before[2] {
		t.Fatalf("ordinary comment counters %v -> %v", before, after)
	}
	userKey, userToken := apiWriteKey(t, f, f.user, "comments:write")
	before = f.domainCounts(t)
	comment = apiWriteObject(t, f.request("POST", path+"/comments", "Bearer "+userToken, `{"body":"Public reply","visibility":"public"}`, "application/json"), 201)
	if comment["author"].(map[string]any)["id"] != float64(f.user.ID) || f.domainCounts(t)[2] != before[2] {
		t.Fatalf("public comment = %#v", comment)
	}
	if _, err := f.h.tickets.PutOnHold(t.Context(), *f.h.admin, ticket.ID, "Waiting for requester"); err != nil {
		t.Fatal(err)
	}
	apiWriteObject(t, f.request("POST", path+"/comments", "Bearer "+userToken, `{"body":"Resume","visibility":"public"}`, "application/json"), 201)
	apiAuditKey(t, f, ticket.ID, userKey.ID, f.user.ID)
	w := f.h.postForm(t, path[len("/api/v1"):]+"/transition", url.Values{"to": {"resolved"}}, false)
	if w.Code != 303 {
		t.Fatalf("browser transition = %d %s", w.Code, w.Body.String())
	}
	if _, valid := scanOneNullableInt(t, f.h.rawDB(t), "SELECT api_key_id FROM audit_events WHERE ticket_id = ? ORDER BY id DESC LIMIT 1", ticket.ID); valid {
		t.Fatal("browser audit acquired an API key")
	}
}

func TestAPIWriteConfirmReject(t *testing.T) {
	for _, action := range []string{"confirm", "reject"} {
		t.Run(action, func(t *testing.T) {
			f := newAPIFixture(t)
			ticket := apiOwnTicket(t, f, "Resolution")
			f.h.seedTransition(t, ticket.ID, domain.StateInProgress, "")
			f.h.seedTransition(t, ticket.ID, domain.StateResolved, "")
			key, token := apiWriteKey(t, f, f.user, "tickets:write")
			path := fmt.Sprintf("/api/v1/tickets/%d/%s", ticket.ID, action)
			out := apiObject(t, f.request("POST", path, "Bearer "+token, `{}`, "application/json"))
			want := "closed"
			if action == "reject" {
				want = "in_progress"
				if out["workflow"] != nil {
					t.Fatal("rejection did not detach workflow")
				}
			}
			if out["state"] != want || out["assignee"] != nil {
				t.Fatalf("resolution = %#v", out)
			}
			apiAuditKey(t, f, ticket.ID, key.ID, f.user.ID)
		})
	}
}

func TestAPIWriteScopeAndTransportRejections(t *testing.T) {
	for _, endpoint := range []string{"", "/transition", "/comments", "/confirm", "/reject", "/workflow/steps/1/complete"} {
		name := endpoint
		if name == "" {
			name = "create"
		}
		t.Run(name, func(t *testing.T) {
			f := newAPIFixture(t)
			ticket := apiOwnTicket(t, f, "No writes")
			path := fmt.Sprintf("/api/v1/tickets/%d%s", ticket.ID, endpoint)
			if endpoint == "" {
				path = "/api/v1/tickets"
			}
			before := f.domainCounts(t)
			assertAPIProblem(t, f.request("POST", path, "Bearer "+f.token, `{"visibility":"public"}`, "application/json"), problemScopeMissing, path)
			_, token := apiWriteKey(t, f, f.user, "tickets:write", "comments:write")
			for _, tc := range []struct {
				body, media string
				kind        apiProblemKind
			}{{`{`, "application/json", problemBadRequest}, {`{"unknown":true}`, "application/json", problemBadRequest}, {`{}`, "text/plain", problemUnsupportedMedia}} {
				assertAPIProblem(t, f.request("POST", path, "Bearer "+token, tc.body, tc.media), tc.kind, path)
			}
			if f.domainCounts(t) != before {
				t.Fatal("rejected write changed domain counters")
			}
		})
	}
}

func TestAPIWriteAgentCreateWithoutAll(t *testing.T) {
	f := newAPIFixture(t)
	agent, _ := apiAgent(t, f)
	_, token := apiWriteKey(t, f, agent, "tickets:write", "tickets:read")
	body := fmt.Sprintf(`{"category_id":%d,"title":"Agent requester","description":"d","priority":"medium"}`, f.h.bugCategory.ID)
	before := f.domainCounts(t)
	w := f.request("POST", "/api/v1/tickets", "Bearer "+token, body, "application/json")
	out := apiWriteObject(t, w, 201)
	if out["requester"].(map[string]any)["id"] != float64(agent.ID) || out["assignee"] != nil || len(out) != 10 {
		t.Fatalf("summary = %#v", out)
	}
	after := f.domainCounts(t)
	if after != [3]int64{before[0] + 1, before[1], before[2] + 1} {
		t.Fatalf("create counters %v -> %v", before, after)
	}
	path := fmt.Sprintf("/api/v1/tickets/%d", int64(out["id"].(float64)))
	if w.Header().Get("Location") != path {
		t.Fatalf("Location = %q", w.Header().Get("Location"))
	}
	assertAPIProblem(t, f.request("GET", path, "Bearer "+token, "", ""), problemNotFound, path)
	if f.domainCounts(t) != after {
		t.Fatal("rejected detail read wrote domain data")
	}
}

func TestAPIWriteCreateValidationAndServiceAssignment(t *testing.T) {
	f := newAPIFixture(t)
	agent, _ := apiAgent(t, f)
	_, token := apiWriteKey(t, f, agent, "tickets:all", "tickets:write", "tickets:read")
	unpublished, err := f.h.categories.Create(t.Context(), "No workflow")
	if err != nil {
		t.Fatal(err)
	}
	before := f.domainCounts(t)
	for _, tc := range []struct {
		category int64
		priority string
		status   int
	}{{999999, "medium", 404}, {unpublished.ID, "medium", 422}, {f.h.bugCategory.ID, "bogus", 422}} {
		body := fmt.Sprintf(`{"category_id":%d,"title":"Invalid","description":"d","priority":%q}`, tc.category, tc.priority)
		w := f.request("POST", "/api/v1/tickets", "Bearer "+token, body, "application/json")
		if w.Code != tc.status || w.Header().Get("Content-Type") != problemContentType || f.domainCounts(t) != before {
			t.Fatalf("create rejection = %d %s, counts %v -> %v", w.Code, w.Body.String(), before, f.domainCounts(t))
		}
	}
	desk, err := f.h.desks.Create(t.Context(), *f.h.admin, "Claim desk")
	if err != nil {
		t.Fatal(err)
	}
	f.h.publishWorkflow(t, f.h.bugCategory.ID, domain.WorkflowDefinition{{Type: domain.StepAssignToDesk, AssignToDesk: &domain.AssignToDeskStep{DeskID: desk.ID, Strategy: domain.StrategyClaim}}})
	body := fmt.Sprintf(`{"category_id":%d,"title":"Cannot claim","description":"d","priority":"medium"}`, f.h.bugCategory.ID)
	out := apiWriteObject(t, f.request("POST", "/api/v1/tickets", "Bearer "+token, body, "application/json"), 201)
	if out["assignee"] != nil {
		t.Fatalf("service acquired assignment: %#v", out)
	}
	id := int64(out["id"].(float64))
	path := fmt.Sprintf("/api/v1/tickets/%d/workflow/steps/1/complete", id)
	before = f.domainCounts(t)
	w := f.request("POST", path, "Bearer "+token, `{}`, "application/json")
	if w.Code < 400 || w.Header().Get("Content-Type") != problemContentType || f.domainCounts(t) != before {
		t.Fatalf("service claim = %d %s", w.Code, w.Body.String())
	}
	for _, endpoint := range []string{"", "/transition", "/comments", "/confirm", "/reject", "/workflow/steps/1/complete"} {
		path := fmt.Sprintf("/api/v1/tickets/%d%s", id, endpoint)
		if endpoint == "" {
			path = "/api/v1/tickets"
		}
		w := f.request("POST", path, "Bearer "+token, fmt.Sprintf(`{"assignee_id":%d}`, agent.ID), "application/json")
		if w.Code < 400 || f.domainCounts(t) != before {
			t.Fatalf("assignment injection %s = %d %s", path, w.Code, w.Body.String())
		}
	}
	view, err := f.h.tickets.GetByID(t.Context(), *f.h.admin, id)
	if err != nil || view.Ticket.UserID != nil {
		t.Fatalf("service acquired assignment: %+v, %v", view, err)
	}
}

func TestAPIWriteRoleAndTicketScope(t *testing.T) {
	f := newAPIFixture(t)
	ticket := apiOwnTicket(t, f, "Scoped")
	agent, _ := apiAgent(t, f)
	_, limited := apiWriteKey(t, f, agent, "tickets:write", "comments:internal")
	_, userToken := apiWriteKey(t, f, f.user, "tickets:write", "comments:write")
	before := f.domainCounts(t)
	for _, endpoint := range []string{"transition", "comments", "confirm", "reject", "workflow/steps/1/complete"} {
		path := fmt.Sprintf("/api/v1/tickets/%d/%s", ticket.ID, endpoint)
		body := `{}`
		if endpoint == "transition" {
			body = `{"state":"in_progress"}`
		}
		if endpoint == "comments" {
			body = `{"body":"secret","visibility":"internal"}`
		}
		assertAPIProblem(t, f.request("POST", path, "Bearer "+limited, body, "application/json"), problemNotFound, path)
	}
	path := fmt.Sprintf("/api/v1/tickets/%d/comments", ticket.ID)
	assertAPIProblem(t, f.request("POST", path, "Bearer "+userToken, `{"body":"secret","visibility":"internal"}`, "application/json"), problemScopeMissing, path)
	if _, _, err := f.svc.CreateAPIKey(t.Context(), *f.root, application.CreateAPIKeyInput{ServiceUserID: f.user.ID, Name: "Forbidden scope", Scopes: []string{"comments:internal"}}); err == nil {
		t.Fatal("user key accepted internal scope")
	}
	path = fmt.Sprintf("/api/v1/tickets/%d/transition", ticket.ID)
	assertAPIProblem(t, f.request("POST", path, "Bearer "+userToken, `{"state":"in_progress"}`, "application/json"), problemForbidden, path)
	if f.domainCounts(t) != before {
		t.Fatal("scope or role rejection wrote domain data")
	}
}
