package httpadapter

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// APIReadServices reuses the existing authorized use cases and catalog
// projections. Configure it at composition time, before serving requests.
type APIReadServices struct {
	Tickets    *application.TicketService
	Search     *application.SearchService
	Categories *application.CategoryService
	Users      *application.UserService
	Catalog    *application.CatalogService
	Workflows  *application.WorkflowService
	Versions   application.WorkflowVersionStore
	Runs       application.WorkflowRunStore
}

func (h *APIHandlers) WithReads(reads APIReadServices) *APIHandlers {
	h.reads = reads
	h.mux.HandleFunc("GET /api/v1/categories", h.listCategories)
	h.mux.HandleFunc("GET /api/v1/tickets", h.listTickets)
	h.mux.HandleFunc("GET /api/v1/tickets/{id}", h.ticketDetail)
	return h
}

type apiList[T any] struct {
	Items      []T     `json:"items"`
	NextCursor *string `json:"next_cursor"`
}

type apiReference struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type apiFormField struct {
	Name     string   `json:"name"`
	Label    string   `json:"label"`
	Type     string   `json:"type"`
	Required bool     `json:"required"`
	Options  []string `json:"options"`
}

type apiRequesterForm struct {
	Position int            `json:"position"`
	Fields   []apiFormField `json:"fields"`
}

type apiCategory struct {
	ID            int64             `json:"id"`
	Name          string            `json:"name"`
	Desk          string            `json:"desk"`
	Department    string            `json:"department"`
	RequesterForm *apiRequesterForm `json:"requester_form"`
}

func (h *APIHandlers) listCategories(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, domain.ScopeCatalogRead) {
		return
	}
	// Published is not enough: the shared assessment also excludes workflows
	// whose desks no longer have eligible staff. Never use availability alone.
	summaries, err := h.reads.Workflows.ListRequesterSummaries(r.Context())
	if err != nil {
		writeErrorProblem(w, r, err)
		return
	}
	categories, err := h.reads.Catalog.ListAllCategories(r.Context())
	if err != nil {
		writeErrorProblem(w, r, err)
		return
	}
	runnable := make(map[int64]bool, len(summaries))
	for _, s := range summaries {
		runnable[s.CategoryID] = s.Version > 0 && s.CannotRun.Empty()
	}
	out := apiList[apiCategory]{Items: make([]apiCategory, 0)}
	for _, c := range categories {
		if !runnable[c.ID] {
			continue
		}
		published, err := h.reads.Versions.GetCurrentVersion(r.Context(), c.ID)
		if err != nil {
			writeErrorProblem(w, r, err)
			return
		}
		if published == nil {
			continue
		}
		item := apiCategory{ID: c.ID, Name: c.Name, Desk: c.DeskName, Department: c.DepartmentName}
		for index, step := range published.Workflow {
			if step.Type != domain.StepForm || step.Form == nil || step.Form.Actor != domain.FormActorRequester {
				continue
			}
			form := &apiRequesterForm{Position: index + 1, Fields: make([]apiFormField, 0, len(step.Form.Fields))}
			for _, field := range step.Form.Fields {
				form.Fields = append(form.Fields, apiFormField{
					Name: field.Key, Label: field.Label, Type: string(field.Kind), Required: field.Required,
					Options: append([]string{}, field.Options...),
				})
			}
			item.RequesterForm = form
			break
		}
		out.Items = append(out.Items, item)
	}
	writeJSON(w, http.StatusOK, out)
}

type apiTicketItem struct {
	ID        int64         `json:"id"`
	Number    int           `json:"number"`
	Title     string        `json:"title"`
	State     string        `json:"state"`
	Priority  string        `json:"priority"`
	Category  apiReference  `json:"category"`
	Assignee  *apiReference `json:"assignee"`
	Requester *apiReference `json:"requester"`
	CreatedAt string        `json:"created_at"`
	UpdatedAt string        `json:"updated_at"`
}

// Each page has at most ten tickets. Reference lookups use the existing read
// services; detail reuses the category/assignee already assembled by its view.
func (h *APIHandlers) ticketItem(ctx context.Context, t *domain.Ticket, category *domain.Category, assignee *domain.User) (apiTicketItem, error) {
	item := apiTicketItem{ID: t.ID, Number: t.Number, Title: t.Title, State: string(t.State), Priority: string(t.Priority), CreatedAt: apiTimestamp(t.CreatedAt), UpdatedAt: apiTimestamp(t.UpdatedAt)}
	if category == nil {
		var err error
		category, err = h.reads.Categories.GetByID(ctx, t.CategoryID)
		if err != nil {
			return item, err
		}
	}
	item.Category = apiReference{ID: category.ID, Name: category.Name}
	if t.UserID != nil {
		if assignee == nil {
			var err error
			assignee, err = h.reads.Users.GetByID(ctx, *t.UserID)
			if err != nil {
				return item, err
			}
		}
		item.Assignee = &apiReference{ID: assignee.ID, Name: assignee.Name}
	}
	if t.RequesterUserID != nil {
		item.Requester = &apiReference{ID: *t.RequesterUserID, Name: t.RequesterName}
	}
	return item, nil
}

func apiTimestamp(t time.Time) string { return t.UTC().Format(time.RFC3339) }

type apiCursor struct {
	Page int `json:"page"`
}

func encodeAPICursor(page int) string {
	data, _ := json.Marshal(apiCursor{Page: page})
	return base64.RawURLEncoding.EncodeToString(data)
}

func decodeAPICursor(raw string) (int, bool) {
	if len(raw) > 128 {
		return 0, false
	}
	data, err := base64.RawURLEncoding.Strict().DecodeString(raw)
	if err != nil {
		return 0, false
	}
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	var cursor apiCursor
	if err := dec.Decode(&cursor); err != nil || dec.Decode(new(any)) != io.EOF {
		return 0, false
	}
	// Bound both offset multiplication and the next-page calculation.
	maxPage := int(^uint(0)>>1) / application.PageSize
	return cursor.Page, cursor.Page > 0 && cursor.Page < maxPage
}

func (h *APIHandlers) listTickets(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, domain.ScopeTicketsRead) {
		return
	}
	values := r.URL.Query()
	query := application.TicketQuery{Text: values.Get("q")}
	for _, raw := range values["state"] {
		state := domain.State(raw)
		switch state {
		case domain.StateNew, domain.StateInProgress, domain.StateResolved, domain.StateClosed, domain.StateCancelled:
			query.States = append(query.States, state)
		default:
			writeErrorProblem(w, r, &domain.ValidationError{Field: "state", Message: "unknown ticket state", Key: "error.api.state_invalid"})
			return
		}
	}
	page := 1
	if cursors, present := values["cursor"]; present {
		var ok bool
		if len(cursors) == 1 {
			page, ok = decodeAPICursor(cursors[0])
		}
		if !ok {
			writeProblem(w, r, problemBadRequest, "")
			return
		}
	}
	// For user-role service users, SearchService's requester-section rules
	// decide which states are listed, overriding these validated state filters.
	result, err := h.reads.Search.Search(r.Context(), *userFromContext(r.Context()), query, page)
	if err != nil {
		writeErrorProblem(w, r, err)
		return
	}
	out := apiList[apiTicketItem]{Items: make([]apiTicketItem, 0, len(result.Tickets))}
	for i := range result.Tickets {
		item, err := h.ticketItem(r.Context(), &result.Tickets[i], nil, nil)
		if err != nil {
			writeErrorProblem(w, r, err)
			return
		}
		out.Items = append(out.Items, item)
	}
	if page*application.PageSize < result.Total {
		next := encodeAPICursor(page + 1)
		out.NextCursor = &next
	}
	writeJSON(w, http.StatusOK, out)
}

type apiHistoricalActor struct {
	ID   *int64 `json:"id"`
	Name string `json:"name"`
}

type apiComment struct {
	ID         int64              `json:"id"`
	Author     apiHistoricalActor `json:"author"`
	Visibility string             `json:"visibility"`
	Body       string             `json:"body"`
	CreatedAt  string             `json:"created_at"`
}

type apiTimelineEvent struct {
	Action string              `json:"action"`
	Field  *string             `json:"field"`
	From   *string             `json:"from"`
	To     *string             `json:"to"`
	Actor  *apiHistoricalActor `json:"actor"`
	At     string              `json:"at"`
}

type apiTicketDetail struct {
	apiTicketItem
	Description string             `json:"description"`
	Comments    []apiComment       `json:"comments"`
	Timeline    []apiTimelineEvent `json:"timeline"`
	Workflow    *apiWorkflow       `json:"workflow"`
}

func (h *APIHandlers) ticketDetail(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, domain.ScopeTicketsRead) {
		return
	}
	id, ok := apiTicketID(w, r)
	if !ok {
		return
	}
	h.writeTicketDetail(w, r, id, http.StatusOK)
}

// Mutation responses reuse the authorized detail projection without requiring
// tickets:read in addition to the endpoint's write scope.
func (h *APIHandlers) writeTicketDetail(w http.ResponseWriter, r *http.Request, id int64, status int) {
	view, err := h.reads.Tickets.GetByID(r.Context(), *userFromContext(r.Context()), id)
	if errors.Is(err, domain.ErrNotFound) {
		// Out-of-scope, missing and malformed identifiers share one body.
		writeProblem(w, r, problemNotFound, "")
		return
	}
	if err != nil {
		writeErrorProblem(w, r, err)
		return
	}
	item, err := h.ticketItem(r.Context(), view.Ticket, view.Category, view.AssignedUser)
	if err != nil {
		writeErrorProblem(w, r, err)
		return
	}
	out := apiTicketDetail{apiTicketItem: item, Description: view.Ticket.Description, Comments: make([]apiComment, 0, len(view.Comments)), Timeline: make([]apiTimelineEvent, 0)}
	for _, c := range view.Comments {
		out.Comments = append(out.Comments, apiComment{ID: c.ID, Author: apiHistoricalActor{ID: c.AuthorUserID, Name: c.Author}, Visibility: string(c.Visibility), Body: c.Body, CreatedAt: apiTimestamp(c.CreatedAt)})
	}
	// The view's merged stream owns chronology and ties. Comments have their
	// own response member; only audit entries are projected into timeline.
	for _, entry := range view.Timeline {
		if entry.Event == nil {
			continue
		}
		e := entry.Event
		event := apiTimelineEvent{Action: e.Action, Field: e.Field, From: e.FromValue, To: e.ToValue, At: apiTimestamp(e.CreatedAt)}
		if e.ActorUserID != nil || !application.IsAutomaticActor(e.Actor) {
			event.Actor = &apiHistoricalActor{ID: e.ActorUserID, Name: e.Actor}
		}
		out.Timeline = append(out.Timeline, event)
	}
	if view.Ticket.WorkflowVersionID != nil {
		snap, err := h.reads.Runs.GetWorkflowExecution(r.Context(), id)
		if err != nil {
			writeErrorProblem(w, r, err)
			return
		}
		out.Workflow = apiWorkflowFor(snap, *userFromContext(r.Context()))
	}
	writeJSON(w, status, out)
}
