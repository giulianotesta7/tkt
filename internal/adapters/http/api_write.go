package httpadapter

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// APIWriteServices uses the same services and atomic workflow apply path as
// the browser. Configure after WithReads and before serving requests.
type APIWriteServices struct {
	Comments *application.CommentService
	Runner   *application.WorkflowRunner
	Workflow application.WorkflowUnitOfWork
}

func (h *APIHandlers) WithWrites(writes APIWriteServices) *APIHandlers {
	h.writes = writes
	h.mux.HandleFunc("POST /api/v1/tickets", h.createTicket)
	h.mux.HandleFunc("POST /api/v1/tickets/{id}/transition", h.transitionTicket)
	h.mux.HandleFunc("POST /api/v1/tickets/{id}/comments", h.addComment)
	h.mux.HandleFunc("POST /api/v1/tickets/{id}/confirm", h.confirmTicket)
	h.mux.HandleFunc("POST /api/v1/tickets/{id}/reject", h.rejectTicket)
	h.mux.HandleFunc("POST /api/v1/tickets/{id}/workflow/steps/{position}/complete", h.completeTicketWorkflow)
	return h
}

func apiTicketID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		writeProblem(w, r, problemNotFound, "")
		return 0, false
	}
	return id, true
}

func (h *APIHandlers) createTicket(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, domain.ScopeTicketsWrite) {
		return
	}
	var in struct {
		CategoryID  int64           `json:"category_id"`
		Title       string          `json:"title"`
		Description string          `json:"description"`
		Priority    domain.Priority `json:"priority"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	ticket, err := h.reads.Tickets.Create(r.Context(), *userFromContext(r.Context()), application.CreateTicketInput{
		CategoryID: in.CategoryID, Title: in.Title, Description: in.Description, Priority: in.Priority,
	})
	if err != nil {
		writeErrorProblem(w, r, err)
		return
	}
	// Creation returns a receipt, not a scoped detail read: an agent service
	// without tickets:all may create but cannot subsequently open the ticket.
	item, err := h.ticketItem(r.Context(), ticket, nil, nil)
	if err != nil {
		writeErrorProblem(w, r, err)
		return
	}
	w.Header().Set("Location", "/api/v1/tickets/"+strconv.FormatInt(ticket.ID, 10))
	writeJSON(w, http.StatusCreated, item)
}

func (h *APIHandlers) transitionTicket(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, domain.ScopeTicketsWrite) {
		return
	}
	var in struct {
		State  domain.State `json:"state"`
		Reason string       `json:"reason"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	id, ok := apiTicketID(w, r)
	if !ok {
		return
	}
	_, err := h.reads.Tickets.Transition(r.Context(), *userFromContext(r.Context()), id, in.State, in.Reason)
	h.finishTicketWrite(w, r, id, err)
}

func (h *APIHandlers) finishTicketWrite(w http.ResponseWriter, r *http.Request, id int64, err error) {
	if errors.Is(err, domain.ErrNotFound) {
		writeProblem(w, r, problemNotFound, "")
		return
	}
	if err != nil {
		writeErrorProblem(w, r, err)
		return
	}
	h.writeTicketDetail(w, r, id, http.StatusOK)
}

func (h *APIHandlers) addComment(w http.ResponseWriter, r *http.Request) {
	// Visibility selects the required scope. Reject a key with neither comment
	// scope before decoding; check the specific scope before any service read.
	p, _ := r.Context().Value(ctxKeyAPIPrincipal{}).(*application.APIPrincipal)
	if p == nil || (!p.HasScope(domain.ScopeCommentsWrite) && !p.HasScope(domain.ScopeCommentsInternal)) {
		requireScope(w, r, domain.ScopeCommentsWrite)
		return
	}
	var in struct {
		Body       string `json:"body"`
		Visibility string `json:"visibility"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	scope := domain.ScopeCommentsWrite
	if strings.TrimSpace(in.Visibility) == string(domain.CommentInternal) {
		scope = domain.ScopeCommentsInternal
	}
	if !requireScope(w, r, scope) {
		return
	}
	id, ok := apiTicketID(w, r)
	if !ok {
		return
	}
	c, err := h.writes.Comments.Add(r.Context(), *userFromContext(r.Context()), id, in.Body, in.Visibility)
	if errors.Is(err, domain.ErrNotFound) {
		writeProblem(w, r, problemNotFound, "")
		return
	}
	if err != nil {
		writeErrorProblem(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, apiComment{ID: c.ID, Author: apiHistoricalActor{ID: c.AuthorUserID, Name: c.Author}, Visibility: string(c.Visibility), Body: c.Body, CreatedAt: apiTimestamp(c.CreatedAt)})
}

func (h *APIHandlers) confirmTicket(w http.ResponseWriter, r *http.Request) {
	h.resolveTicket(w, r, true)
}

func (h *APIHandlers) rejectTicket(w http.ResponseWriter, r *http.Request) {
	h.resolveTicket(w, r, false)
}

func (h *APIHandlers) resolveTicket(w http.ResponseWriter, r *http.Request, confirm bool) {
	if !requireScope(w, r, domain.ScopeTicketsWrite) {
		return
	}
	var in struct{}
	if !decodeJSON(w, r, &in) {
		return
	}
	id, ok := apiTicketID(w, r)
	if !ok {
		return
	}
	actor := *userFromContext(r.Context())
	var err error
	if confirm {
		_, err = h.reads.Tickets.ConfirmResolution(r.Context(), actor, id)
	} else {
		_, err = h.reads.Tickets.RejectResolution(r.Context(), actor, id)
	}
	h.finishTicketWrite(w, r, id, err)
}

func (h *APIHandlers) completeTicketWorkflow(w http.ResponseWriter, r *http.Request) {
	if !requireScope(w, r, domain.ScopeTicketsWrite) {
		return
	}
	var in struct {
		Answers []struct {
			Position int      `json:"position"`
			Values   []string `json:"values"`
		} `json:"answers"`
		Solution *string `json:"solution"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	id, ok := apiTicketID(w, r)
	if !ok {
		return
	}
	actor := *userFromContext(r.Context())
	if _, err := h.reads.Tickets.GetByID(r.Context(), actor, id); err != nil {
		h.finishTicketWrite(w, r, id, err)
		return
	}
	snap, err := h.reads.Runs.GetWorkflowExecution(r.Context(), id)
	if err != nil {
		writeErrorProblem(w, r, err)
		return
	}
	pos, err := strconv.Atoi(r.PathValue("position"))
	if err != nil || pos < 1 || snap == nil || snap.Run == nil || snap.Run.Status != "active" || pos-1 != snap.Run.CurrentStepIndex || pos > len(snap.Workflow) {
		writeErrorProblem(w, r, domain.NewWorkflowPositionConflictError("workflow position conflict"))
		return
	}
	var solution string
	form := url.Values{}
	if in.Solution != nil {
		solution = strings.TrimSpace(*in.Solution)
		form.Set("solution", solution)
	}
	if len(solution) > maxSolutionLength {
		writeErrorProblem(w, r, &domain.ValidationError{Field: "solution", Message: domain.ErrMsgSolutionTooLong, Key: "error.solution_too_long"})
		return
	}
	var raw application.RawPositionalValues
	for _, answer := range in.Answers {
		raw = append(raw, application.RawPositionalValue{Position: answer.Position, Values: answer.Values})
	}
	step := snap.Workflow[pos-1]
	if step.Type == domain.StepForm && step.Form != nil {
		raw, err = validatePositionalAnswers(raw, len(step.Form.Fields))
	} else {
		if len(raw) > 0 {
			form.Set("answer_0", "")
		}
		raw, err = workflowFormAnswers(snap, form)
	}
	if err != nil {
		writeErrorProblem(w, r, err)
		return
	}
	plan, err := h.writes.Runner.PlanComplete(r.Context(), *snap, application.CompleteWorkflowCommand{
		TicketID: id, ActorUserID: actor.ID, ActorName: actor.Name, ExpectedPosition: pos, RawAnswers: raw, Solution: solution,
	})
	if err == nil {
		_, err = h.writes.Workflow.ApplyWorkflowPlan(r.Context(), plan)
	}
	h.finishTicketWrite(w, r, id, err)
}

type apiWorkflow struct {
	CurrentPosition int             `json:"current_position"`
	PendingStep     *apiPendingStep `json:"pending_step"`
}

type apiPendingStep struct {
	Position int            `json:"position"`
	Kind     string         `json:"kind"`
	Fields   []apiFormField `json:"fields"`
}

// Like the browser checklist, only the persisted requester/assignee may act.
// Queue-wide ticket access does not confer identity for completing a step.
// Service users cannot claim; automatic and completed steps expose no action.
func apiWorkflowFor(snap *application.WorkflowExecutionSnapshot, actor domain.User) *apiWorkflow {
	if snap == nil || snap.Run == nil || snap.Ticket == nil {
		return nil
	}
	cur := snap.Run.CurrentStepIndex
	out := &apiWorkflow{CurrentPosition: cur + 1}
	if snap.Run.Status != "active" || cur < 0 || cur >= len(snap.Workflow) || domain.IsClosed(snap.Ticket.State) {
		return out
	}
	step := snap.Workflow[cur]
	ticket := snap.Ticket
	canAct := ticket.UserID != nil && *ticket.UserID == actor.ID
	if step.Type == domain.StepForm && step.Form != nil && step.Form.Actor == domain.FormActorRequester {
		canAct = ticket.RequesterUserID != nil && *ticket.RequesterUserID == actor.ID
	}
	if !canAct || (step.Type != domain.StepForm && step.Type != domain.StepManualTask) {
		return out
	}
	pending := &apiPendingStep{Position: cur + 1, Kind: string(step.Type), Fields: make([]apiFormField, 0)}
	if step.Type == domain.StepForm && step.Form != nil {
		for _, field := range step.Form.Fields {
			pending.Fields = append(pending.Fields, apiFormField{Name: field.Key, Label: field.Label, Type: string(field.Kind), Required: field.Required, Options: append([]string{}, field.Options...)})
		}
	}
	out.PendingStep = pending
	return out
}
