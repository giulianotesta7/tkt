package httpadapter

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

func apiReadToken(t *testing.T, f *apiFixture, user *domain.User, scopes ...string) string {
	t.Helper()
	_, token, err := f.svc.CreateAPIKey(t.Context(), *f.root, application.CreateAPIKeyInput{ServiceUserID: user.ID, Name: "Read", Scopes: scopes})
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func apiObject(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	if w.Code != 200 || w.Header().Get("Content-Type") != "application/json" || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("response = %d %v %s", w.Code, w.Header(), w.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func apiOwnTicket(t *testing.T, f *apiFixture, title string) *domain.Ticket {
	t.Helper()
	ticket, err := f.h.tickets.Create(t.Context(), *f.user, application.CreateTicketInput{Title: title, Description: "API description", CategoryID: f.h.bugCategory.ID, Priority: domain.PriorityMedium})
	if err != nil {
		t.Fatal(err)
	}
	return ticket
}

func apiAgent(t *testing.T, f *apiFixture) (*domain.User, string) {
	t.Helper()
	u, err := f.svc.CreateServiceUser(t.Context(), *f.root, "Agent integration", domain.RoleAgent)
	if err != nil {
		t.Fatal(err)
	}
	return u, apiReadToken(t, f, u, "tickets:read", "catalog:read", "tickets:all")
}

func TestAPIReadCategories(t *testing.T) {
	f := newAPIFixture(t)
	_, agentToken := apiAgent(t, f)
	userToken := apiReadToken(t, f, f.user, "catalog:read")
	fields := []domain.FormField{
		{Key: "summary", Label: "Summary", Kind: domain.FieldShortText, Required: true},
		{Key: "details", Label: "Details", Kind: domain.FieldLongText},
		{Key: "checked", Label: "Checked", Kind: domain.FieldCheckbox},
		{Key: "choice", Label: "Choice", Kind: domain.FieldSingleSelect, Options: []string{"One", "Two"}},
	}
	f.h.publishWorkflow(t, f.h.bugCategory.ID, domain.WorkflowDefinition{
		{Type: domain.StepForm, Form: &domain.FormStep{Actor: domain.FormActorRequester, Fields: fields}},
		{Type: domain.StepForm, Form: &domain.FormStep{Actor: domain.FormActorRequester, Fields: []domain.FormField{{Key: "later", Label: "Later", Kind: domain.FieldLongText}}}},
	})
	noForm, err := f.h.categories.Create(t.Context(), "No form")
	if err != nil {
		t.Fatal(err)
	}
	f.h.publishWorkflow(t, noForm.ID, simpleManualDef())
	if _, err := f.h.categories.Create(t.Context(), "Unpublished"); err != nil {
		t.Fatal(err)
	}
	broken, err := f.h.categories.Create(t.Context(), "Unrunnable")
	if err != nil {
		t.Fatal(err)
	}
	desk, err := f.h.desks.Create(t.Context(), *f.h.admin, "Empty later")
	if err != nil {
		t.Fatal(err)
	}
	f.h.publishWorkflow(t, broken.ID, domain.WorkflowDefinition{{Type: domain.StepAssignToDesk, AssignToDesk: &domain.AssignToDeskStep{DeskID: desk.ID, Strategy: domain.StrategyClaim}}})
	if err := f.h.desks.RemoveMember(t.Context(), *f.h.admin, desk.ID, f.h.admin.ID); err != nil {
		t.Fatal(err)
	}
	before := f.domainCounts(t)
	for _, token := range []string{userToken, agentToken} {
		got := apiObject(t, f.request("GET", "/api/v1/categories", "Bearer "+token, "", ""))
		items := got["items"].([]any)
		if len(items) != 2 || got["next_cursor"] != nil {
			t.Fatalf("categories = %#v", got)
		}
		byID := map[float64]map[string]any{}
		for _, raw := range items {
			item := raw.(map[string]any)
			byID[item["id"].(float64)] = item
		}
		bug := byID[float64(f.h.bugCategory.ID)]
		if bug["name"] != "Bugs" || bug["desk"] != "General" || bug["department"] != "General" || byID[float64(noForm.ID)]["requester_form"] != nil {
			t.Fatalf("category shapes = %#v", items)
		}
		want := map[string]any{"position": float64(1), "fields": []any{
			map[string]any{"name": "summary", "label": "Summary", "type": "short_text", "required": true, "options": []any{}},
			map[string]any{"name": "details", "label": "Details", "type": "long_text", "required": false, "options": []any{}},
			map[string]any{"name": "checked", "label": "Checked", "type": "checkbox", "required": false, "options": []any{}},
			map[string]any{"name": "choice", "label": "Choice", "type": "single_select", "required": false, "options": []any{"One", "Two"}},
		}}
		if !reflect.DeepEqual(bug["requester_form"], want) {
			t.Fatalf("form = %#v", bug["requester_form"])
		}
	}
	if f.domainCounts(t) != before {
		t.Fatal("read changed domain data")
	}
}

func TestAPIReadTicketsAndDetail(t *testing.T) {
	f := newAPIFixture(t)
	agent, agentToken := apiAgent(t, f)
	ticket := apiOwnTicket(t, f, "Own ticket")
	other := f.h.seedTicket(t, "Other requester", nil)
	public, err := f.h.comments.Add(t.Context(), *f.h.admin, ticket.ID, "Public answer", "public")
	if err != nil {
		t.Fatal(err)
	}
	internal, err := f.h.comments.Add(t.Context(), *f.h.admin, ticket.ID, "Internal secret", "internal")
	if err != nil {
		t.Fatal(err)
	}
	before := f.domainCounts(t)
	limited := apiReadToken(t, f, agent, "tickets:read")
	empty := apiObject(t, f.request("GET", "/api/v1/tickets", "Bearer "+limited, "", ""))
	if len(empty["items"].([]any)) != 0 || empty["next_cursor"] != nil {
		t.Fatalf("agent without tickets:all = %#v", empty)
	}
	path := fmt.Sprintf("/api/v1/tickets/%d", ticket.ID)
	assertAPIProblem(t, f.request("GET", path, "Bearer "+limited, "", ""), problemNotFound, path)
	allOnly := apiReadToken(t, f, agent, "tickets:all")
	assertAPIProblem(t, f.request("GET", path, "Bearer "+allOnly, "", ""), problemScopeMissing, path)
	assertAPIProblem(t, f.request("GET", "/api/v1/tickets", "Bearer "+allOnly, "", ""), problemScopeMissing, "/api/v1/tickets")
	filtered := apiObject(t, f.request("GET", "/api/v1/tickets?state=closed&state=cancelled", "Bearer "+agentToken, "", ""))
	if len(filtered["items"].([]any)) != 0 || filtered["next_cursor"] != nil {
		t.Fatalf("agent state filter = %#v", filtered)
	}
	found := apiObject(t, f.request("GET", "/api/v1/tickets?q=Other&state=new&state=in_progress", "Bearer "+agentToken, "", ""))
	if len(found["items"].([]any)) != 1 || found["items"].([]any)[0].(map[string]any)["id"] != float64(other.ID) {
		t.Fatalf("agent search = %#v", found)
	}
	for _, tc := range []struct {
		name, token string
		count       int
		comments    int
	}{{"user", f.token, 1, 1}, {"agent", agentToken, 2, 2}} {
		t.Run(tc.name, func(t *testing.T) {
			list := apiObject(t, f.request("GET", "/api/v1/tickets", "Bearer "+tc.token, "", ""))
			items := list["items"].([]any)
			if len(items) != tc.count || list["next_cursor"] != nil {
				t.Fatalf("list = %#v", list)
			}
			var item map[string]any
			for _, raw := range items {
				row := raw.(map[string]any)
				if row["id"] == float64(ticket.ID) {
					item = row
				}
			}
			want := map[string]any{"id": float64(ticket.ID), "number": float64(ticket.Number), "title": ticket.Title, "state": "new", "priority": "medium", "category": map[string]any{"id": float64(ticket.CategoryID), "name": "Bugs"}, "assignee": nil, "requester": map[string]any{"id": float64(f.user.ID), "name": f.user.Name}, "created_at": ticket.CreatedAt.UTC().Format(time.RFC3339), "updated_at": ticket.UpdatedAt.UTC().Format(time.RFC3339)}
			if !reflect.DeepEqual(item, want) {
				t.Fatalf("item = %#v, want %#v", item, want)
			}
			detail := apiObject(t, f.request("GET", fmt.Sprintf("/api/v1/tickets/%d", ticket.ID), "Bearer "+tc.token, "", ""))
			for key, value := range want {
				if !reflect.DeepEqual(detail[key], value) {
					t.Fatalf("detail %s = %#v", key, detail[key])
				}
			}
			if detail["description"] != "API description" || detail["workflow"] != nil {
				t.Fatalf("detail = %#v", detail)
			}
			comments := detail["comments"].([]any)
			if len(comments) != tc.comments {
				t.Fatalf("comments = %#v", comments)
			}
			for _, raw := range comments {
				c := raw.(map[string]any)
				expected := public
				if c["id"] == float64(internal.ID) {
					expected = internal
				}
				wantComment := map[string]any{"id": float64(expected.ID), "author": map[string]any{"id": float64(f.h.admin.ID), "name": f.h.admin.Name}, "visibility": string(expected.Visibility), "body": expected.Body, "created_at": expected.CreatedAt.UTC().Format(time.RFC3339)}
				if !reflect.DeepEqual(c, wantComment) || (tc.name == "user" && expected == internal) {
					t.Fatalf("comment = %#v", c)
				}
			}
			events := detail["timeline"].([]any)
			if len(events) != 1 {
				t.Fatalf("timeline = %#v", events)
			}
			wantEvent := map[string]any{"action": "created", "field": nil, "from": nil, "to": nil, "actor": map[string]any{"id": float64(f.user.ID), "name": f.user.Name}, "at": ticket.CreatedAt.UTC().Format(time.RFC3339)}
			if !reflect.DeepEqual(events[0], wantEvent) {
				t.Fatalf("event = %#v", events[0])
			}
		})
	}
	for _, id := range []string{strconv.FormatInt(other.ID, 10), "999999", "not-a-number"} {
		path := "/api/v1/tickets/" + id
		w := f.request("GET", path, "Bearer "+f.token, "", "")
		assertAPIProblem(t, w, problemNotFound, path)
		want := fmt.Sprintf("{\"type\":\"urn:tkt:problem:not-found\",\"title\":\"Not found\",\"status\":404,\"instance\":%q}\n", path)
		if w.Body.String() != want {
			t.Fatalf("non-generic not found: %s", w.Body.String())
		}
	}
	if f.domainCounts(t) != before {
		t.Fatal("reads changed domain data")
	}
}

func TestAPIReadPaginationSearchAndRequesterStatePolicy(t *testing.T) {
	f := newAPIFixture(t)
	for i := 0; i < 12; i++ {
		apiOwnTicket(t, f, fmt.Sprintf("Findable ticket %d", i))
	}
	f.h.seedTicket(t, "Other requester", nil)
	before := f.domainCounts(t)
	first := apiObject(t, f.request("GET", "/api/v1/tickets", "Bearer "+f.token, "", ""))
	cursor, ok := first["next_cursor"].(string)
	if !ok || cursor == "" || len(first["items"].([]any)) != 10 {
		t.Fatalf("first page = %#v", first)
	}
	last := apiObject(t, f.request("GET", "/api/v1/tickets?cursor="+cursor, "Bearer "+f.token, "", ""))
	if len(last["items"].([]any)) != 2 || last["next_cursor"] != nil {
		t.Fatalf("last page = %#v", last)
	}
	seen := map[any]bool{}
	for _, page := range []map[string]any{first, last} {
		for _, raw := range page["items"].([]any) {
			id := raw.(map[string]any)["id"]
			if seen[id] {
				t.Fatalf("repeated id %v", id)
			}
			seen[id] = true
		}
	}
	filtered := apiObject(t, f.request("GET", "/api/v1/tickets?q=absentword", "Bearer "+f.token, "", ""))
	if len(filtered["items"].([]any)) != 0 || filtered["next_cursor"] != nil {
		t.Fatalf("search = %#v", filtered)
	}
	// SearchService chooses the requester section's states, not the API input.
	states := apiObject(t, f.request("GET", "/api/v1/tickets?state=closed&state=cancelled", "Bearer "+f.token, "", ""))
	if !reflect.DeepEqual(states, first) {
		t.Fatalf("requester policy changed: %#v", states)
	}
	if f.domainCounts(t) != before {
		t.Fatal("search changed domain data")
	}
}

func TestAPIReadRejectionsAndRouting(t *testing.T) {
	f := newAPIFixture(t)
	noRead := apiReadToken(t, f, f.user, "tickets:write")
	before := f.domainCounts(t)
	for _, path := range []string{"/api/v1/categories", "/api/v1/tickets", "/api/v1/tickets/1"} {
		assertAPIProblem(t, f.request("GET", path, "Bearer "+noRead, "", ""), problemScopeMissing, path)
		w := f.request("POST", path, "Bearer "+f.token, "", "")
		assertAPIProblem(t, w, problemMethodNotAllowed, path)
		if w.Header().Get("Allow") != "GET, HEAD" {
			t.Fatalf("Allow = %q", w.Header().Get("Allow"))
		}
	}
	for _, path := range []string{"/api/v1/unknown", "/api/v1/tickets/1/unknown", "/api/v1//tickets", "/api/v1/tickets/"} {
		assertAPIProblem(t, f.request("GET", path, "Bearer "+f.token, "", ""), problemNotFound, path)
	}
	for _, state := range []string{"bogus", "", "NEW", "new&state=bogus"} {
		w := f.request("GET", "/api/v1/tickets?state="+state, "Bearer "+f.token, "", "")
		assertAPIProblem(t, w, problemValidation, "/api/v1/tickets")
		var p apiProblem
		if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil || len(p.Errors) != 1 || p.Errors[0].Field != "state" || p.Errors[0].Detail == "" {
			t.Fatalf("validation = %+v, %v", p, err)
		}
	}
	for _, raw := range []string{"!", "", "e30", base64.RawURLEncoding.EncodeToString([]byte(`{"page":0}`)), base64.RawURLEncoding.EncodeToString([]byte(`{"page":-1}`)), base64.RawURLEncoding.EncodeToString([]byte(`{"page":1,"extra":true}`)), base64.RawURLEncoding.EncodeToString([]byte(`{"page":9223372036854775807}`))} {
		assertAPIProblem(t, f.request("GET", "/api/v1/tickets?cursor="+raw, "Bearer "+f.token, "", ""), problemBadRequest, "/api/v1/tickets")
	}
	if f.domainCounts(t) != before {
		t.Fatal("rejection changed domain data")
	}
}

func TestAPIMeHeadPreserved(t *testing.T) {
	f := newAPIFixture(t)
	w := f.request("HEAD", "/api/v1/me", "Bearer "+f.token, "", "")
	if w.Code != 200 || w.Body.Len() != 0 || w.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("HEAD = %d %v %s", w.Code, w.Header(), w.Body.String())
	}
}
