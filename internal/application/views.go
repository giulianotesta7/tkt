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
	// Summary is a compact natural-language line for the activity timeline
	// ("Ticket created", "Moved to In Progress", "Changed Title"). It keeps
	// state changes terse and unobtrusive next to full comments. A workflow
	// assignment carries only the exact prefix "Assigned to"; the template
	// composes the structured person/desk strong pair from the fields below.
	Summary    string
	ActorLabel string
	// IsWorkflowAssignment marks a contextual workflow_assignment event: the
	// main line is the structured "Assigned to person · desk" pair below, and
	// no st-class is derived from the raw ToValue user id.
	IsWorkflowAssignment bool
	// AssignmentPerson/AssignmentDesk are the resolved display labels of the
	// assignment's ToValue user id and DeskID (missing references degrade to
	// "Unknown user"/"Unknown desk").
	AssignmentPerson string
	AssignmentDesk   string
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
		item.Summary = eventSummary(item.Event)
		switch item.Event.Action {
		case domain.ActionWorkflowStep, domain.ActionWorkflowManualTask,
			domain.ActionWorkflowRequesterForm, domain.ActionWorkflowAssigneeForm:
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
		item.Summary = eventSummary(item.Event)
		if field == "user" || field == "user_id" {
			item.Summary = "Changed assignee"
			if item.ToLabel != "" {
				item.Summary = "Assigned to " + item.ToLabel
			}
		} else if item.Event.Action == domain.ActionUpdate {
			label := auditFieldLabel(field)
			if item.ToLabel != "" && item.ToLabel != item.FromLabel {
				item.Summary = "Changed " + label + " to " + item.ToLabel
			} else {
				item.Summary = "Changed " + label
			}
		}
	}
	return nil
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

// eventSummary renders a compact natural-language line for a state-change
// audit event (activity timeline). Transition carries a ToValue target;
// updates carry a Field; creations have neither. Transitions read as a
// contextual outcome statement in a uniform "Ticket …" family ("Ticket
// in progress", "Ticket resolved"); a reopen — from a closed state
// (resolved/closed) back into in_progress — reads as "Ticket Reopened".
func eventSummary(e *domain.AuditEvent) string {
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
		// Structured prefix only: person and desk render from the resolved
		// TimelineItem fields as one "Assigned to <person> · <desk>" line.
		return "Assigned to"
	case domain.ActionWorkflowStep:
		return "Completed step"
	case domain.ActionWorkflowManualTask:
		return "Completed task"
	case domain.ActionWorkflowRequesterForm:
		return "Submitted request details"
	case domain.ActionWorkflowAssigneeForm:
		return "Submitted work details"
	default:
		if e.Field != nil {
			return "Changed " + auditFieldLabel(strings.ToLower(strings.TrimSpace(*e.Field)))
		}
		return humanizeIdentifier(e.Action)
	}
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
