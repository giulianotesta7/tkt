package application

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// TicketView is the detail-page view model (D13): the store ports return
// domain types and the application assembles the view via ref lookups.
// AssignedUser may be inactive — historical display (user-management spec).
// Timeline is the merged presentation stream: comments + audit events in
// one reverse-chronological feed (newest first). Comments and AuditEvents
// stay available as the underlying data contracts.
type TicketView struct {
	Ticket            *domain.Ticket
	Category          *domain.Category
	AssignedUser      *domain.User // nil when the ticket is unassigned
	Comments          []domain.Comment
	AuditEvents       []domain.AuditEvent
	Timeline          []TimelineItem
	WorkflowResponses []WorkflowResponse
}

// TimelineItem is one entry of the merged detail-page timeline: either a
// comment or an audit event (comment-timeline spec: newest-first feed). All
// label fields are plain strings — html/template escapes them exactly once,
// so no raw HTML ever enters this model.
type TimelineItem struct {
	IsComment   bool
	Comment     *domain.Comment
	Event       *domain.AuditEvent
	ActionLabel string
	FieldLabel  string
	FromLabel   string
	ToLabel     string
	// Summary retains the historical capitalized summary contract for callers
	// that consume the view model directly. Narrative is the sentence-case
	// action phrase rendered in the ticket timeline; NarrativeMsg carries the
	// SAME phrase as a catalogue reference (issue #335, S8), so the rendered
	// timeline follows the instance language. Narrative stays the English
	// fallback for a caller that renders the view model directly.
	Summary      string
	Narrative    string
	NarrativeMsg Msg
	ActorLabel   string
	// StateClass carries a validated lifecycle state class for transition
	// events. It never exposes arbitrary audit values (such as user IDs) in
	// markup.
	StateClass string
	// IsWorkflowAssignment marks a contextual workflow_assignment event: the
	// main line keeps the assigned person and desk in the actor-first sentence,
	// and no st-class is derived from the raw ToValue user id.
	IsWorkflowAssignment bool
	// AssignmentPerson/AssignmentDesk are the resolved display labels of the
	// assignment's ToValue user id and DeskID (missing references degrade to
	// "Unknown user"/"Unknown desk").
	AssignmentPerson string
	AssignmentDesk   string
	// AssignmentPersonMsg/AssignmentDeskMsg carry the same labels as catalogue
	// references when the label is chrome (the Unknown user / Unknown desk
	// degrade) and as data when it is a resolved name, so a Spanish timeline
	// follows the instance language without translating a person or a desk.
	AssignmentPersonMsg Msg
	AssignmentDeskMsg   Msg
	// SuppressDetail hides the Reason/Note detail line for legacy and semantic
	// workflow-completion events (workflow_step, manual task, both forms):
	// their answers stay behind the protected answers projection. Transitions,
	// updates, and validated assignment reasons keep their detail visible.
	SuppressDetail bool
	// StepFields carries the pinned form answers joined strictly by the event's
	// persisted step_index (PR10 task 10.2); nil when no compatible context
	// resolves. StepInstruction carries a manual step's pinned instruction.
	// Structural seam only: enrichment lands with task 10.2.
	StepFields      []WorkflowResponseField
	StepInstruction string
	// StepSolution carries the stored manual-task solution (Amendment 2) joined
	// through the same enrichment pass as StepInstruction; empty when none was
	// submitted or for legacy completions. Data-only: rendering lands in WB.
	StepSolution string
	seq          int // original list index: deterministic tie-break
}

// ViewBuilder assembles TicketViews and resolves historical audit references.
// Per-view caches avoid repeating user/category/desk lookups within one
// timeline.
type ViewBuilder struct {
	tickets    TicketStore
	users      UserStore
	categories CategoryStore
	comments   CommentStore
	audits     AuditStore
	desks      DeskStore
	responses  WorkflowResponseStore
}

// NewViewBuilder wires the view assembly against the given ports. The desk
// store is REQUIRED: workflow_assignment timelines resolve their desk labels
// through it, and composition fails closed rather than rendering without it.
// The optional response store preserves legacy composition while
// workflow-enabled callers project answers only after the scoped ticket read
// succeeds.
func NewViewBuilder(tickets TicketStore, users UserStore, categories CategoryStore, comments CommentStore, audits AuditStore, desks DeskStore, responses ...WorkflowResponseStore) *ViewBuilder {
	builder := &ViewBuilder{
		tickets:    tickets,
		users:      users,
		categories: categories,
		comments:   comments,
		audits:     audits,
		desks:      desks,
	}
	if len(responses) > 0 {
		builder.responses = responses[0]
	}
	return builder
}

// TicketView composes the ticket with its category, assigned user (if any),
// chronological comment timeline, and audit history. The read is scoped to
// the actor's ticket access scope carried in q (ticket-access spec): an
// out-of-scope ticket is ErrNotFound, so detail pages never leak.
// includeInternal is the actor's comment-visibility scope (comment-visibility
// spec): false for user-role actors — internal (staff-only) comments are
// excluded at the store boundary AND filtered again before timeline
// composition, so a user-role response can never contain internal content.
func (b *ViewBuilder) TicketView(ctx context.Context, ticketID int64, q TicketQuery, includeInternal bool) (*TicketView, error) {
	t, err := b.tickets.GetByID(ctx, ticketID, q)
	if err != nil {
		return nil, err
	}
	cat, err := b.categories.GetByID(ctx, t.CategoryID)
	if err != nil {
		return nil, err
	}
	view := &TicketView{Ticket: t, Category: cat}
	// The ticket read above is the authorization boundary. Workflow responses
	// are never queried for an out-of-scope ticket or through a weaker endpoint.
	if b.responses != nil {
		responses, err := b.responses.ListWorkflowResponses(ctx, ticketID)
		if err != nil {
			return nil, err
		}
		view.WorkflowResponses = responses
	}
	if t.UserID != nil {
		user, err := b.users.GetByID(ctx, *t.UserID)
		if err != nil {
			return nil, err
		}
		view.AssignedUser = user
	}
	comments, err := b.comments.ListByTicket(ctx, ticketID, includeInternal)
	if err != nil {
		return nil, err
	}
	view.Comments = filterCommentVisibility(comments, includeInternal)
	events, err := b.audits.ListByTicket(ctx, ticketID)
	if err != nil {
		return nil, err
	}
	view.AuditEvents = events
	view.Timeline = mergeTimeline(view.Comments, events)
	if err := b.enrichTimeline(ctx, view); err != nil {
		return nil, err
	}
	return view, nil
}

// filterCommentVisibility is the application-side half of the visibility
// filter (comment-visibility spec: filtering precedes composition). The
// store already excludes internal rows for non-internal actors at the SQL
// boundary; this pass is the defensive second layer, so a store regression
// can never leak internal content into a composed view.
func filterCommentVisibility(comments []domain.Comment, includeInternal bool) []domain.Comment {
	if includeInternal {
		return comments
	}
	out := make([]domain.Comment, 0, len(comments))
	for _, c := range comments {
		if c.Visibility != domain.CommentInternal {
			out = append(out, c)
		}
	}
	return out
}

// enrichTimeline resolves audit identifiers into stable presentation labels.
// The immutable AuditEvents retain their stored IDs; only TimelineItem gains
// display values for the HTTP boundary.
func (b *ViewBuilder) enrichTimeline(ctx context.Context, view *TicketView) error {
	userNames := map[int64]string{}
	if view.AssignedUser != nil {
		userNames[view.AssignedUser.ID] = view.AssignedUser.Name
	}
	categoryNames := map[int64]string{view.Category.ID: view.Category.Name}
	deskNames := map[int64]string{}

	for i := range view.Timeline {
		item := &view.Timeline[i]
		if item.IsComment || item.Event == nil {
			continue
		}
		item.ActionLabel = humanizeIdentifier(item.Event.Action)
		// Audit-log spec: the persisted automatic actor `workflow` renders no
		// actor text at all (never a `Workflow` label); human events keep their
		// attributed names.
		if item.Event.Actor != "workflow" {
			item.ActorLabel = item.Event.Actor
		}
		item.Narrative = eventSummary(item.Event)
		item.NarrativeMsg = eventSummaryMsg(item.Event)
		item.Summary = legacyEventSummary(item.Event)
		if item.Event.Action == domain.ActionTransition && item.Event.ToValue != nil {
			item.StateClass = *item.Event.ToValue
		} else if item.Event.Action == domain.ActionCreated {
			item.StateClass = "new"
		}
		switch item.Event.Action {
		case domain.ActionWorkflowStep, domain.ActionWorkflowManualTask,
			domain.ActionWorkflowRequesterForm, domain.ActionWorkflowAssigneeForm:
			item.SuppressDetail = true
		case domain.ActionHold:
			// A hold event's Note IS the pause reason and the narrative already
			// carries it ("put the ticket on hold: <reason>", issue #352), so the
			// generic "Reason:" line would print it twice.
			item.SuppressDetail = true
		}
		// PR10 task 10.2: a semantic completion event binds its pinned step
		// context strictly through the persisted step_index. Missing, out-of-
		// range, or incompatible contexts leave the safe summary alone; corrupt
		// persisted answers fail closed through the store error.
		switch item.Event.Action {
		case domain.ActionWorkflowManualTask, domain.ActionWorkflowRequesterForm, domain.ActionWorkflowAssigneeForm:
			if item.Event.StepIndex != nil && b.responses != nil {
				stepCtx, err := b.responses.WorkflowStepContext(ctx, view.Ticket.ID, *item.Event.StepIndex)
				if err != nil {
					return err
				}
				item.bindStepContext(stepCtx)
				// Issue #264: a requester rejection detaches the pin by design
				// (D4), so the pinned read above cannot resolve the completion
				// even though its solution row survives. On the pin-independent
				// path, re-read ONLY the stored solution and bind it to this
				// manual completion event: the historical solution stays
				// reachable, while the pinned instruction — genuinely gone with
				// the detached definition — is honestly omitted rather than
				// reconstructed from an unpinned guess.
				if stepCtx == nil && view.Ticket.WorkflowVersionID == nil &&
					item.Event.Action == domain.ActionWorkflowManualTask {
					if reader, ok := b.responses.(manualSolutionReader); ok {
						solution, err := reader.ManualSolution(ctx, view.Ticket.ID, *item.Event.StepIndex)
						if err != nil {
							return err
						}
						item.bindStepContext(&WorkflowStepContext{Kind: "manual", Solution: solution})
					}
				}
			}
		}
		if item.Event.Action == domain.ActionWorkflowAssignment {
			// Contextual assignment: resolve the structured person/desk labels;
			// the template renders the single "Assigned to … · …" main line from
			// these facts and never derives markup from the raw values.
			item.IsWorkflowAssignment = true
			person, err := b.userValueLabel(ctx, item.Event.ToValue, userNames)
			if err != nil {
				return err
			}
			desk, err := b.deskValueLabel(ctx, item.Event.DeskID, deskNames)
			if err != nil {
				return err
			}
			item.AssignmentPerson = person
			item.AssignmentDesk = desk
			item.AssignmentPersonMsg = labelMsg(person)
			item.AssignmentDeskMsg = labelMsg(desk)
			continue
		}
		if item.Event.Field == nil {
			continue
		}

		field := strings.ToLower(strings.TrimSpace(*item.Event.Field))
		item.FieldLabel = auditFieldLabel(field)
		var err error
		item.FromLabel, err = b.auditValueLabel(ctx, field, item.Event.FromValue, userNames, categoryNames)
		if err != nil {
			return err
		}
		item.ToLabel, err = b.auditValueLabel(ctx, field, item.Event.ToValue, userNames, categoryNames)
		if err != nil {
			return err
		}
		item.Narrative = eventSummary(item.Event)
		item.NarrativeMsg = eventSummaryMsg(item.Event)
		item.Summary = legacyEventSummary(item.Event)
		if field == "user" || field == "user_id" {
			if item.ToLabel != "" {
				item.Narrative = "assigned the ticket to " + item.ToLabel
				item.NarrativeMsg = MsgRef("tickets.timeline.narrative.assigned_to", Text(item.ToLabel))
				item.Summary = "Assigned to " + item.ToLabel
			} else {
				item.Narrative = "changed the ticket assignee"
				item.NarrativeMsg = MsgRef("tickets.timeline.narrative.changed_assignee")
				item.Summary = "Changed assignee"
			}
		} else if item.Event.Action == domain.ActionUpdate {
			label := auditFieldLabel(field)
			if item.ToLabel != "" && item.ToLabel != item.FromLabel {
				item.Narrative = "changed " + strings.ToLower(label) + " to " + item.ToLabel
				item.NarrativeMsg = MsgRef("tickets.timeline.narrative.changed_to",
					timelineFieldArg(field), timelineValueArg(field, item.Event.ToValue, item.ToLabel))
				item.Summary = "Changed " + label + " to " + item.ToLabel
			} else {
				item.Narrative = "changed " + strings.ToLower(label)
				item.NarrativeMsg = MsgRef("tickets.timeline.narrative.changed", timelineFieldArg(field))
				item.Summary = "Changed " + label
			}
		}
	}
	return nil
}

// manualSolutionReader is the OPTIONAL, pin-independent half of the response
// store: it resolves one manual completion's stored solution by the ticket and
// the event's exact persisted step index, without ever consulting the pinned
// definition. The timeline uses it ONLY for a detached ticket, so the
// WorkflowStepContextStore contract keeps its documented (nil, nil) degradation
// for a missing pin. A store that cannot answer simply omits the capability and
// the timeline renders the safe summary alone, exactly as before.
type manualSolutionReader interface {
	ManualSolution(ctx context.Context, ticketID int64, stepIndex int) (string, error)
}

// bindStepContext attaches a resolved pinned-step projection when it is
// compatible with the completion event's kind; any missing or incompatible
// context leaves the timeline item as its safe summary alone (nothing is
// fabricated).
func (t *TimelineItem) bindStepContext(stepCtx *WorkflowStepContext) {
	if stepCtx == nil {
		return
	}
	switch t.Event.Action {
	case domain.ActionWorkflowManualTask:
		if stepCtx.Kind == "manual" {
			t.StepInstruction = stepCtx.Instruction
			t.StepSolution = stepCtx.Solution
		}
	case domain.ActionWorkflowRequesterForm:
		if stepCtx.Kind == "form" && stepCtx.FormActor == domain.FormActorRequester {
			t.StepFields = stepCtx.Fields
		}
	case domain.ActionWorkflowAssigneeForm:
		if stepCtx.Kind == "form" && stepCtx.FormActor == domain.FormActorAssignee {
			t.StepFields = stepCtx.Fields
		}
	}
}

// StepCompletionAction returns the semantic workflow audit action that
// completes the given pinned step — the single step-type → action
// correspondence the view layer shares instead of restating it. It mirrors the
// recognition the workflow runner writes and the unit of work enforces:
// workflow_manual_task for a manual task (workflow_runner.go stepAudit),
// workflow_requester_form / workflow_assignee_form for the matching form actor
// (workflow_runner.go:135-139; workflow_uow.go validateWorkflowStepOp), and
// workflow_assignment for an assign_to_desk step of either strategy — a claim
// (workflow_runner.go newClaimOperation) or the automatic least_loaded row
// (workflow_uow.go:1865). The action alone does not attribute a step: callers
// must match the step's expected action against the event, so a contextual
// workflow_assignment completes ONLY an assign_to_desk step.
//
// resolve_ticket and close_ticket return "" deliberately. Their completion is
// an automatic lifecycle transition (workflow_runner.go applyTerminal) whose
// audit row carries NO step_index, so no event can be correlated to them by the
// sealed index; an active run never renders them as done anyway, because a
// terminal step always ends the run. A step whose completing action cannot be
// determined returns "" and is never attributed from a guessed event.
func StepCompletionAction(step domain.WorkflowStep) string {
	switch step.Type {
	case domain.StepForm:
		if step.Form == nil {
			return ""
		}
		if step.Form.Actor == domain.FormActorRequester {
			return domain.ActionWorkflowRequesterForm
		}
		return domain.ActionWorkflowAssigneeForm
	case domain.StepManualTask:
		return domain.ActionWorkflowManualTask
	case domain.StepAssignToDesk:
		return domain.ActionWorkflowAssignment
	default:
		return ""
	}
}

// legacyEventSummary preserves the pre-narrative view-model summary for
// callers outside the HTML presentation boundary.
func legacyEventSummary(e *domain.AuditEvent) string {
	switch e.Action {
	case domain.ActionTransition:
		if e.ToValue != nil {
			if *e.ToValue == "in_progress" && e.FromValue != nil && isClosedState(*e.FromValue) {
				return "Ticket Reopened"
			}
			return "Ticket " + strings.ReplaceAll(*e.ToValue, "_", " ")
		}
		return "Changed state"
	case domain.ActionCreated:
		return "Ticket created"
	case domain.ActionWorkflowAssignment:
		return "Assigned to"
	case domain.ActionWorkflowStep:
		return "Completed step"
	case domain.ActionWorkflowManualTask:
		return "Completed task"
	case domain.ActionWorkflowRequesterForm:
		return "Submitted request details"
	case domain.ActionWorkflowAssigneeForm:
		return "Submitted work details"
	case domain.ActionHold:
		return "Put on hold"
	case domain.ActionResume:
		return "Resumed"
	default:
		if e.Field != nil {
			return "Changed " + auditFieldLabel(strings.ToLower(strings.TrimSpace(*e.Field)))
		}
		return humanizeIdentifier(e.Action)
	}
}

// eventSummary returns the sentence-case action phrase shown after an
// attributed actor. It deliberately never includes the actor itself, so the
// template can omit automatic workflow actors without leaving a separator.
func eventSummary(e *domain.AuditEvent) string {
	switch e.Action {
	case domain.ActionTransition:
		if e.ToValue != nil {
			if *e.ToValue == "in_progress" && e.FromValue != nil && isClosedState(*e.FromValue) {
				return "reopened the ticket"
			}
			return "moved the ticket to " + strings.ReplaceAll(*e.ToValue, "_", " ")
		}
		return "changed the ticket state"
	case domain.ActionCreated:
		return "created the ticket"
	case domain.ActionWorkflowAssignment:
		return "assigned the ticket to"
	case domain.ActionWorkflowStep:
		return "completed the step"
	case domain.ActionWorkflowManualTask:
		return "completed the task"
	case domain.ActionWorkflowRequesterForm:
		return "submitted request details"
	case domain.ActionWorkflowAssigneeForm:
		return "submitted work details"
	case domain.ActionHold:
		if e.Note != nil {
			return "put the ticket on hold: " + *e.Note
		}
		return "put the ticket on hold"
	case domain.ActionResume:
		return "took the ticket off hold"
	default:
		if e.Field != nil {
			return "changed " + strings.ToLower(auditFieldLabel(strings.ToLower(strings.TrimSpace(*e.Field))))
		}
		return strings.ToLower(humanizeIdentifier(e.Action))
	}
}

// eventSummaryMsg is the catalogue form of eventSummary: the same sentence-case
// action phrase, expressed as a message reference so the ticket timeline can be
// rendered in the active instance language (issue #335, S8). It deliberately
// never includes the actor itself, mirroring eventSummary.
func eventSummaryMsg(e *domain.AuditEvent) Msg {
	switch e.Action {
	case domain.ActionTransition:
		if e.ToValue != nil {
			if *e.ToValue == "in_progress" && e.FromValue != nil && isClosedState(*e.FromValue) {
				return MsgRef("tickets.timeline.narrative.reopened")
			}
			return MsgRef("tickets.timeline.narrative.moved_to", timelineStateArg(*e.ToValue))
		}
		return MsgRef("tickets.timeline.narrative.changed_state")
	case domain.ActionCreated:
		return MsgRef("tickets.timeline.narrative.created")
	case domain.ActionWorkflowAssignment:
		return MsgRef("tickets.timeline.narrative.assigned")
	case domain.ActionWorkflowStep:
		return MsgRef("tickets.timeline.narrative.completed_step")
	case domain.ActionWorkflowManualTask:
		return MsgRef("tickets.timeline.narrative.completed_task")
	case domain.ActionWorkflowRequesterForm:
		return MsgRef("tickets.timeline.narrative.submitted_request")
	case domain.ActionWorkflowAssigneeForm:
		return MsgRef("tickets.timeline.narrative.submitted_work")
	case domain.ActionHold:
		if e.Note != nil {
			return MsgRef("tickets.timeline.narrative.on_hold", Text(*e.Note))
		}
		return MsgRef("tickets.timeline.narrative.on_hold_no_reason")
	case domain.ActionResume:
		return MsgRef("tickets.timeline.narrative.resumed")
	default:
		if e.Field != nil {
			return MsgRef("tickets.timeline.narrative.changed", timelineFieldArg(strings.ToLower(strings.TrimSpace(*e.Field))))
		}
		return MsgRef("tickets.timeline.narrative.changed", Text(strings.ToLower(humanizeIdentifier(e.Action))))
	}
}

// labelMsg maps a resolved reference label to a catalogue message: the degrade
// labels (Unknown user / Unknown category / Unknown desk / Unassigned) are
// chrome and follow the instance language, while a resolved name is data and
// renders verbatim. It is the single mapping, so no surface invents a second
// vocabulary for a missing reference (issue #335, S8).
func labelMsg(resolved string) Msg {
	if key := degradeKey(resolved); key != "" {
		return MsgRef(key)
	}
	return Data(resolved)
}

// labelArg is labelMsg for a value interpolated into a composed sentence.
func labelArg(resolved string) Arg {
	if key := degradeKey(resolved); key != "" {
		return Copy(key)
	}
	return Text(resolved)
}

// degradeKey names the catalogue entry for one resolved-reference gap, or ""
// for a resolved value.
func degradeKey(resolved string) string {
	switch resolved {
	case "Unknown user":
		return "tickets.degrade.unknown_user"
	case "Unknown category":
		return "tickets.degrade.unknown_category"
	case "Unknown desk":
		return "tickets.degrade.unknown_desk"
	case "Unassigned":
		return "tickets.degrade.unassigned"
	default:
		return ""
	}
}

// timelineFieldKey names the catalogue key of a changed audit field inside a
// narrative sentence ("changed priority to Low", "cambió la prioridad a
// Baja"). The English copy is the lowercase word the sentence always used, so
// a default instance renders exactly today's bytes. An unrecognized field has
// no key: it interpolates its own lowercased label as data.
func timelineFieldKey(field string) string {
	switch field {
	case "title":
		return "tickets.timeline.field.title"
	case "priority":
		return "tickets.timeline.field.priority"
	case "category", "category_id":
		return "tickets.timeline.field.category"
	case "state":
		return "tickets.timeline.field.state"
	default:
		return ""
	}
}

// timelineFieldArg resolves a changed field into a translated placeholder when
// the catalogue names it, and into the lowercase label the narrative always
// used otherwise ("assigned to" for the user field), so every English sentence
// keeps its historical bytes.
func timelineFieldArg(field string) Arg {
	if key := timelineFieldKey(field); key != "" {
		return Copy(key)
	}
	return Text(strings.ToLower(auditFieldLabel(field)))
}

// timelineValueKey names the catalogue key of a changed field's VALUE when that
// value is chrome rather than data: a priority and a lifecycle state are
// labelled by the catalogue (label.priority.low, label.state.in_progress). A
// title, a category name, a person's name, and every other free value return
// "" and render verbatim (S11).
func timelineValueKey(field, value string) string {
	switch field {
	case "priority":
		switch value {
		case "low", "medium", "high", "critical":
			return "label.priority." + value
		}
	case "state":
		switch value {
		case "new", "in_progress", "resolved", "closed", "cancelled":
			return "label.state." + value
		}
	}
	return ""
}

// timelineValueArg resolves a changed value through the catalogue when it is a
// known priority or state, and keeps the caller's resolved label verbatim
// otherwise. A resolved-reference gap (Unknown user, Unassigned) is chrome and
// resolves through the catalogue too.
func timelineValueArg(field string, value *string, resolved string) Arg {
	if value == nil {
		return Text("")
	}
	if key := timelineValueKey(field, strings.ToLower(strings.TrimSpace(*value))); key != "" {
		return Copy(key)
	}
	return labelArg(resolved)
}

// timelineStateKey names the catalogue key of a lifecycle state inside a
// narrative sentence. The English copy there is lowercase ("moved the ticket
// to in progress"), unlike the standalone badge label, so it is a distinct key
// rather than a reuse of label.state.*. An unknown state has no key and its raw
// value is interpolated instead.
func timelineStateKey(state string) string {
	switch state {
	case string(domain.StateNew):
		return "tickets.timeline.state.new"
	case string(domain.StateInProgress):
		return "tickets.timeline.state.in_progress"
	case string(domain.StateResolved):
		return "tickets.timeline.state.resolved"
	case string(domain.StateClosed):
		return "tickets.timeline.state.closed"
	case string(domain.StateCancelled):
		return "tickets.timeline.state.cancelled"
	default:
		return ""
	}
}

// timelineStateArg resolves a transition target into a translated placeholder
// when the state is known, and into its raw value otherwise, so an unrecognized
// state never renders a raw catalogue key.
func timelineStateArg(state string) Arg {
	if key := timelineStateKey(state); key != "" {
		return Copy(key)
	}
	return Text(state)
}

func isClosedState(s string) bool {
	switch s {
	case string(domain.StateResolved), string(domain.StateClosed):
		return true
	}
	return false
}

func auditFieldLabel(field string) string {
	switch field {
	case "user", "user_id":
		return "Assigned To"
	case "category", "category_id":
		return "Category"
	default:
		return humanizeIdentifier(field)
	}
}

func (b *ViewBuilder) auditValueLabel(ctx context.Context, field string, value *string, userNames, categoryNames map[int64]string) (string, error) {
	if value == nil {
		return "", nil
	}
	raw := *value
	switch field {
	case "user", "user_id":
		return b.userValueLabel(ctx, value, userNames)
	case "category", "category_id":
		id, err := parseAuditID(raw)
		if err != nil {
			return "Unknown category", nil
		}
		if name, ok := categoryNames[id]; ok {
			return name, nil
		}
		category, err := b.categories.GetByID(ctx, id)
		if errors.Is(err, domain.ErrNotFound) {
			return "Unknown category", nil
		}
		if err != nil {
			return "", err
		}
		categoryNames[id] = category.Name
		return category.Name, nil
	case "state", "priority":
		return humanizeIdentifier(raw), nil
	default:
		return raw, nil
	}
}

// userValueLabel resolves an audit user-id value into its display label under
// the existing user/category policy: an empty value reads as Unassigned, an
// unparseable or missing/deleted id degrades to Unknown user, and a renamed or
// inactive user still shows their current stored name (historical display).
func (b *ViewBuilder) userValueLabel(ctx context.Context, value *string, userNames map[int64]string) (string, error) {
	if value == nil {
		return "", nil
	}
	raw := *value
	if raw == "" {
		return "Unassigned", nil
	}
	id, err := parseAuditID(raw)
	if err != nil {
		return "Unknown user", nil
	}
	if name, ok := userNames[id]; ok {
		return name, nil
	}
	user, err := b.users.GetByID(ctx, id)
	if errors.Is(err, domain.ErrNotFound) {
		return "Unknown user", nil
	}
	if err != nil {
		return "", err
	}
	userNames[id] = user.Name
	return user.Name, nil
}

// deskValueLabel resolves an assignment DeskID into its display label. A nil
// id (deleted desk via the ON DELETE SET NULL FK) or a missing/stale id
// degrades to Unknown desk; a live desk shows its current stored name.
func (b *ViewBuilder) deskValueLabel(ctx context.Context, value *int64, deskNames map[int64]string) (string, error) {
	if value == nil {
		return "Unknown desk", nil
	}
	if name, ok := deskNames[*value]; ok {
		return name, nil
	}
	desk, err := b.desks.GetByID(ctx, *value)
	if errors.Is(err, domain.ErrNotFound) {
		return "Unknown desk", nil
	}
	if err != nil {
		return "", err
	}
	if desk == nil {
		return "Unknown desk", nil
	}
	deskNames[*value] = desk.Name
	return desk.Name, nil
}

func parseAuditID(raw string) (int64, error) {
	id, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || id < 1 {
		return 0, strconv.ErrSyntax
	}
	return id, nil
}

func humanizeIdentifier(value string) string {
	words := strings.Fields(strings.NewReplacer("_", " ", "-", " ").Replace(value))
	for i, word := range words {
		words[i] = strings.ToUpper(word[:1]) + strings.ToLower(word[1:])
	}
	return strings.Join(words, " ")
}

// mergeTimeline builds the newest-first merged stream of comments and audit
// events (comment-timeline spec: reverse-chronological, interleaved). Ties
// at the same timestamp are deterministic: comments before events, then the
// later occurrence (higher original index) first — timestamps have second
// precision, so same-second mutations are common and must not jitter
// between renders.
func mergeTimeline(comments []domain.Comment, events []domain.AuditEvent) []TimelineItem {
	items := make([]TimelineItem, 0, len(comments)+len(events))
	for i := range comments {
		items = append(items, TimelineItem{IsComment: true, Comment: &comments[i], seq: i})
	}
	for i := range events {
		items = append(items, TimelineItem{IsComment: false, Event: &events[i], seq: i})
	}
	sort.SliceStable(items, func(i, j int) bool {
		ai, aj := items[i].at(), items[j].at()
		if !ai.Equal(aj) {
			return ai.After(aj)
		}
		if items[i].IsComment != items[j].IsComment {
			return items[i].IsComment // comments before events on ties
		}
		return items[i].seq > items[j].seq
	})
	return items
}

// at returns the entry's timestamp for timeline ordering.
func (t TimelineItem) at() time.Time {
	if t.IsComment {
		return t.Comment.CreatedAt
	}
	return t.Event.CreatedAt
}
