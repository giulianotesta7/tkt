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
// (issue #210). The queue order is a personal setting with no capability
// gate: every authenticated user owns exactly one stored value, and the
// handler layer is what requires a session. Validation happens BEFORE the
// store is reached (fail closed on an invalid write), and the read
// normalizes any stored value outside the closed set.
type PreferencesService struct {
	preferences PreferencesStore
}

// NewPreferencesService wires the preference use cases against the store
// port.
func NewPreferencesService(preferences PreferencesStore) *PreferencesService {
	return &PreferencesService{preferences: preferences}
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
		return &domain.ValidationError{Field: "queue_order", Message: "invalid queue order"}
	}
	return s.preferences.SetQueueOrder(ctx, actor.ID, order)
}
