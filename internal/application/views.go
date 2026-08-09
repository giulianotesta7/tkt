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
	Ticket       *domain.Ticket
	Category     *domain.Category
	AssignedUser *domain.User // nil when the ticket is unassigned
	Comments     []domain.Comment
	AuditEvents  []domain.AuditEvent
	Timeline     []TimelineItem
}

// TimelineItem is one entry of the merged detail-page timeline: either a
// comment or an audit event (comment-timeline spec: newest-first feed).
type TimelineItem struct {
	IsComment   bool
	Comment     *domain.Comment
	Event       *domain.AuditEvent
	ActionLabel string
	FieldLabel  string
	FromLabel   string
	ToLabel     string
	seq         int // original list index: deterministic tie-break
}

// ViewBuilder assembles TicketViews and resolves historical audit references.
// Per-view caches avoid repeating user/category lookups within one timeline.
type ViewBuilder struct {
	tickets    TicketStore
	users      UserStore
	categories CategoryStore
	comments   CommentStore
	audits     AuditStore
}

// NewViewBuilder wires the view assembly against the given ports.
func NewViewBuilder(tickets TicketStore, users UserStore, categories CategoryStore, comments CommentStore, audits AuditStore) *ViewBuilder {
	return &ViewBuilder{
		tickets:    tickets,
		users:      users,
		categories: categories,
		comments:   comments,
		audits:     audits,
	}
}

// TicketView composes the ticket with its category, assigned user (if any),
// chronological comment timeline, and audit history.
func (b *ViewBuilder) TicketView(ctx context.Context, ticketID int64) (*TicketView, error) {
	t, err := b.tickets.GetByID(ctx, ticketID)
	if err != nil {
		return nil, err
	}
	cat, err := b.categories.GetByID(ctx, t.CategoryID)
	if err != nil {
		return nil, err
	}
	view := &TicketView{Ticket: t, Category: cat}
	if t.UserID != nil {
		user, err := b.users.GetByID(ctx, *t.UserID)
		if err != nil {
			return nil, err
		}
		view.AssignedUser = user
	}
	comments, err := b.comments.ListByTicket(ctx, ticketID)
	if err != nil {
		return nil, err
	}
	view.Comments = comments
	events, err := b.audits.ListByTicket(ctx, ticketID)
	if err != nil {
		return nil, err
	}
	view.AuditEvents = events
	view.Timeline = mergeTimeline(comments, events)
	if err := b.enrichTimeline(ctx, view); err != nil {
		return nil, err
	}
	return view, nil
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

	for i := range view.Timeline {
		item := &view.Timeline[i]
		if item.IsComment || item.Event == nil {
			continue
		}
		item.ActionLabel = humanizeIdentifier(item.Event.Action)
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
	}
	return nil
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
