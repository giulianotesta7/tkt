package application

import (
	"context"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// Queue-order preference values (issue #210). The set mirrors exactly the
// values the /tickets `sort` parameter accepts, so a stored default can
// always be expressed in the queue's own vocabulary.
const (
	QueueOrderNewest   = "newest"
	QueueOrderPriority = "priority"
	QueueOrderUrgency  = "urgency"
)

// DefaultQueueOrder is the order a user without a stored preference gets,
// and the value every unknown or malformed stored value normalizes to. It is
// the same default the queue has always used (newest first).
const DefaultQueueOrder = QueueOrderNewest

// AllowedQueueOrders returns the closed set of storable queue orders in the
// order the preferences control offers them.
func AllowedQueueOrders() []string {
	return []string{QueueOrderNewest, QueueOrderPriority, QueueOrderUrgency}
}

// IsAllowedQueueOrder reports whether order is inside the closed set. It is
// the single predicate both the write validation and the read normalization
// use, so the two can never disagree.
func IsAllowedQueueOrder(order string) bool {
	for _, allowed := range AllowedQueueOrders() {
		if order == allowed {
			return true
		}
	}
	return false
}

// NormalizeQueueOrder fails closed: any value outside the closed set
// (including the empty string, a hand-edited row, or a value written by a
// future version) becomes DefaultQueueOrder. The queue therefore always has
// a defined order and never renders an unvalidated stored value.
func NormalizeQueueOrder(order string) string {
	if IsAllowedQueueOrder(order) {
		return order
	}
	return DefaultQueueOrder
}

// PreferencesService implements the per-user preference use cases
// (issue #210, issue #362, issue #377). The queue order, the email opt-out, and
// the sidebar state are all personal settings with no capability gate: every
// authenticated user owns exactly one stored value of each, and the handler
// layer is what requires a session. Validation happens BEFORE the store is
// reached (fail closed on an invalid write), and the queue-order read
// normalizes any stored value outside the closed set. The three preferences
// share one table but never one value: each use case below touches only its
// own key.
type PreferencesService struct {
	preferences PreferencesStore
	email       EmailPreferencesStore
	sidebar     SidebarPreferencesStore
}

// NewPreferencesService wires the preference use cases against the store
// ports: the queue order, the email opt-out (issue #362, S5), and the sidebar
// state (issue #377, S2).
func NewPreferencesService(preferences PreferencesStore, email EmailPreferencesStore, sidebar SidebarPreferencesStore) *PreferencesService {
	return &PreferencesService{preferences: preferences, email: email, sidebar: sidebar}
}

// GetDefaultQueueOrder returns the actor's own stored default queue order,
// normalized to the closed set: no row, an empty value, or an unknown value
// all answer DefaultQueueOrder. Only a real storage failure is an error.
func (s *PreferencesService) GetDefaultQueueOrder(ctx context.Context, actor domain.User) (string, error) {
	stored, err := s.preferences.GetQueueOrder(ctx, actor.ID)
	if err != nil {
		return "", err
	}
	return NormalizeQueueOrder(stored), nil
}

// SetDefaultQueueOrder validates the requested order against the closed set
// and persists it for the actor. Anything outside the set is a
// ValidationError and the store is never touched (fail closed on invalid
// input); the value is stored verbatim because it is already inside the set.
func (s *PreferencesService) SetDefaultQueueOrder(ctx context.Context, actor domain.User, order string) error {
	if !IsAllowedQueueOrder(order) {
		return &domain.ValidationError{Field: "queue_order", Message: "invalid queue order", Key: "error.invalid_queue_order"}
	}
	return s.preferences.SetQueueOrder(ctx, actor.ID, order)
}

// GetEmailNotifications returns whether the actor wants notification email
// (issue #362, S5). A user who never saved the preference gets true: email is
// on by default (S14). The key is the actor's own id, so one user can only
// ever read their own value.
func (s *PreferencesService) GetEmailNotifications(ctx context.Context, actor domain.User) (bool, error) {
	return s.email.EmailNotificationsEnabled(ctx, actor.ID)
}

// SetEmailNotifications persists the actor's email choice (issue #362, S14).
// The toggle has no capability gate and no closed set beyond the boolean, so
// there is nothing to validate; like the queue order the key is the actor's
// own id, which is what keeps one user from changing another's value.
func (s *PreferencesService) SetEmailNotifications(ctx context.Context, actor domain.User, enabled bool) error {
	return s.email.SetEmailNotificationsEnabled(ctx, actor.ID, enabled)
}

// GetSidebarCollapsed reports whether the actor's sidebar is collapsed (issue
// #377, S2). The store answers the documented default — an absent row means
// expanded — so a user who never toggled it gets the shell every other page
// already renders. The key is the actor's own id, so one user can only ever
// read their own state.
func (s *PreferencesService) GetSidebarCollapsed(ctx context.Context, actor domain.User) (bool, error) {
	return s.sidebar.SidebarCollapsed(ctx, actor.ID)
}

// SetSidebarCollapsed persists the actor's sidebar state (issue #377, S2). The
// state has no capability gate and no closed set beyond the boolean, so there
// is nothing to validate; the wire vocabulary ("collapsed"/"expanded") is the
// handler's, and the key is the actor's own id, which is what keeps one user
// from changing another's shell.
func (s *PreferencesService) SetSidebarCollapsed(ctx context.Context, actor domain.User, collapsed bool) error {
	return s.sidebar.SetSidebarCollapsed(ctx, actor.ID, collapsed)
}
