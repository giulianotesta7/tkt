package httpadapter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The ten MCP tools map one to one onto /api/v1 endpoints. Inputs mirror the
// API's bodies and query parameters; a tool only moves the ticket id and the
// workflow position out of the path. A field without omitempty is required in
// the inferred schema.

type mcpNoInput struct{}

type mcpTicketInput struct {
	TicketID int64 `json:"ticket_id" jsonschema:"the ticket id, as returned by search_tickets (not its display number)"`
}

type mcpSearchInput struct {
	Query  string   `json:"q,omitempty" jsonschema:"search ticket titles or a ticket number such as TKT-42"`
	States []string `json:"state,omitempty" jsonschema:"only tickets in these states: new, in_progress, resolved, closed or cancelled"`
	Cursor string   `json:"cursor,omitempty" jsonschema:"next_cursor from the previous page; keep the same filters"`
}

type mcpCreateInput struct {
	CategoryID  int64  `json:"category_id" jsonschema:"a category id from list_categories"`
	Title       string `json:"title" jsonschema:"short summary of the request"`
	Description string `json:"description,omitempty" jsonschema:"details of the request"`
	Priority    string `json:"priority" jsonschema:"low, medium, high or critical"`
}

type mcpAnswer struct {
	Position int      `json:"position" jsonschema:"zero-based index of the field in the step's fields"`
	Values   []string `json:"values" jsonschema:"one value; an exact option for a selection; on or true to check a checkbox"`
}

type mcpCompleteInput struct {
	TicketID int64       `json:"ticket_id" jsonschema:"the ticket id"`
	Position int         `json:"position" jsonschema:"one-based position of the pending step, from get_ticket workflow.pending_step.position"`
	Answers  []mcpAnswer `json:"answers,omitempty" jsonschema:"answers to a form step"`
	Solution *string     `json:"solution,omitempty" jsonschema:"solution of a manual task step, at most 2000 bytes"`
}

type mcpTransitionInput struct {
	TicketID int64  `json:"ticket_id" jsonschema:"the ticket id"`
	State    string `json:"state" jsonschema:"target state: new, in_progress, resolved, closed or cancelled"`
	Reason   string `json:"reason,omitempty" jsonschema:"why; required to reopen a closed ticket"`
}

type mcpCommentInput struct {
	TicketID int64  `json:"ticket_id" jsonschema:"the ticket id"`
	Body     string `json:"body" jsonschema:"the comment text"`
	Internal bool   `json:"internal,omitempty" jsonschema:"true for an internal note only staff can read (agents only)"`
}

func apiTicketPath(id int64, suffix string) string {
	return "/api/v1/tickets/" + strconv.FormatInt(id, 10) + suffix
}

func (h *MCPHandlers) addTools() {
	s := h.server
	mcp.AddTool(s, &mcp.Tool{Name: "whoami", Description: "Answer the signed-in person this client acts as: id, name, role and the granted scopes."},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ mcpNoInput) (*mcp.CallToolResult, any, error) {
			return h.dispatch(ctx, http.MethodGet, "/api/v1/me", nil, nil)
		})
	mcp.AddTool(s, &mcp.Tool{Name: "list_categories", Description: "List the categories a ticket can be created in, each with the requester form the person fills in after creating it."},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ mcpNoInput) (*mcp.CallToolResult, any, error) {
			return h.dispatch(ctx, http.MethodGet, "/api/v1/categories", nil, nil)
		})
	mcp.AddTool(s, &mcp.Tool{Name: "search_tickets", Description: "Search the tickets the person can see, ten per page, newest first. Pass next_cursor to read the next page."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in mcpSearchInput) (*mcp.CallToolResult, any, error) {
			query := url.Values{}
			if in.Query != "" {
				query.Set("q", in.Query)
			}
			for _, state := range in.States {
				query.Add("state", state)
			}
			if in.Cursor != "" {
				query.Set("cursor", in.Cursor)
			}
			return h.dispatch(ctx, http.MethodGet, "/api/v1/tickets", query, nil)
		})
	mcp.AddTool(s, &mcp.Tool{Name: "get_ticket", Description: "Read one ticket: description, comments, timeline and the workflow step the person can complete, if any."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in mcpTicketInput) (*mcp.CallToolResult, any, error) {
			return h.dispatch(ctx, http.MethodGet, apiTicketPath(in.TicketID, ""), nil, nil)
		})
	mcp.AddTool(s, &mcp.Tool{Name: "create_ticket", Description: "Create a ticket as the person, in a category from list_categories. Then read it with get_ticket and complete its pending requester form, if any."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in mcpCreateInput) (*mcp.CallToolResult, any, error) {
			return h.dispatch(ctx, http.MethodPost, "/api/v1/tickets", nil, in)
		})
	mcp.AddTool(s, &mcp.Tool{Name: "complete_workflow_step", Description: "Complete the ticket's pending workflow step: answer a form or finish a manual task. Only the step's requester or assignee can."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in mcpCompleteInput) (*mcp.CallToolResult, any, error) {
			body := struct {
				Answers  []mcpAnswer `json:"answers,omitempty"`
				Solution *string     `json:"solution,omitempty"`
			}{in.Answers, in.Solution}
			return h.dispatch(ctx, http.MethodPost, apiTicketPath(in.TicketID, "/workflow/steps/"+strconv.Itoa(in.Position)+"/complete"), nil, body)
		})
	mcp.AddTool(s, &mcp.Tool{Name: "transition_ticket", Description: "Move a ticket to another state (agents only), following the same state machine as the web interface."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in mcpTransitionInput) (*mcp.CallToolResult, any, error) {
			body := struct {
				State  string `json:"state"`
				Reason string `json:"reason,omitempty"`
			}{in.State, in.Reason}
			return h.dispatch(ctx, http.MethodPost, apiTicketPath(in.TicketID, "/transition"), nil, body)
		})
	mcp.AddTool(s, &mcp.Tool{Name: "add_comment", Description: "Comment on a ticket as the person. Set internal for a note only staff can read."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in mcpCommentInput) (*mcp.CallToolResult, any, error) {
			body := struct {
				Body       string `json:"body"`
				Visibility string `json:"visibility"`
			}{in.Body, "public"}
			if in.Internal {
				body.Visibility = "internal"
			}
			return h.dispatch(ctx, http.MethodPost, apiTicketPath(in.TicketID, "/comments"), nil, body)
		})
	mcp.AddTool(s, &mcp.Tool{Name: "confirm_resolution", Description: "Confirm a resolved ticket is solved and close it. Only its requester can."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in mcpTicketInput) (*mcp.CallToolResult, any, error) {
			return h.dispatch(ctx, http.MethodPost, apiTicketPath(in.TicketID, "/confirm"), nil, struct{}{})
		})
	mcp.AddTool(s, &mcp.Tool{Name: "reject_resolution", Description: "Reject a resolution: the ticket goes back to in progress. Only its requester can."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in mcpTicketInput) (*mcp.CallToolResult, any, error) {
			return h.dispatch(ctx, http.MethodPost, apiTicketPath(in.TicketID, "/reject"), nil, struct{}{})
		})
}

// dispatch runs one /api/v1 request in process. The tool's context descends
// from the /mcp request (the go-sdk connects a stateless session with it), so
// it already carries the person, the principal and the grant; the API mux is
// called directly because ServeHTTP has authenticated. A 2xx answers its JSON
// as the result; a problem answers isError with its text.
func (h *MCPHandlers) dispatch(ctx context.Context, method, path string, query url.Values, body any) (*mcp.CallToolResult, any, error) {
	if userFromContext(ctx) == nil {
		return nil, nil, errors.New("mcp: the tool call carries no person")
	}
	var reader io.Reader = http.NoBody
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, nil, err
		}
		reader = bytes.NewReader(data)
	}
	target := path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	r, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return nil, nil, err
	}
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	w := &toolResponse{header: http.Header{}}
	h.api.ServeHTTP(w, r)
	return w.result(), nil, nil
}

// toolResponse is the in-memory ResponseWriter of a dispatched API call.
type toolResponse struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (w *toolResponse) Header() http.Header { return w.header }

func (w *toolResponse) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}

func (w *toolResponse) Write(p []byte) (int, error) {
	w.WriteHeader(http.StatusOK)
	return w.body.Write(p)
}

func (w *toolResponse) result() *mcp.CallToolResult {
	raw := bytes.TrimSpace(w.body.Bytes())
	if w.status >= 200 && w.status < 300 {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(raw)}}, StructuredContent: json.RawMessage(raw)}
	}
	var p apiProblem
	if json.Unmarshal(raw, &p) != nil || p.Title == "" {
		p = apiProblem{Title: http.StatusText(w.status), Status: w.status}
	}
	var text strings.Builder
	fmt.Fprintf(&text, "%s (%d)", p.Title, p.Status)
	if p.Detail != "" {
		text.WriteString(": " + p.Detail)
	}
	for _, f := range p.Errors {
		fmt.Fprintf(&text, "\n- %s: %s", f.Field, f.Detail)
	}
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: text.String()}}}
}
