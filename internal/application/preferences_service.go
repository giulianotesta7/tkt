package application

import (
	"context"
	"errors"

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

// Theme preference values (issue #378, S1). The set is closed: the preferences
// page offers exactly these three, the store holds one of them, and the shell
// turns the two explicit choices into a data-theme attribute.
const (
	ThemeSystem = "system"
	ThemeLight  = "light"
	ThemeDark   = "dark"
)

// DefaultTheme is the theme a user without a stored preference gets, and the
// value every unknown or malformed stored value normalizes to. Following the
// system setting is what a browser already does, so a user who never opened
// Preferences sees what their operating system asks for.
const DefaultTheme = ThemeSystem

// AllowedThemes returns the closed set of storable themes in the order the
// preferences control offers them: the system default first, then the two
// explicit choices.
func AllowedThemes() []string {
	return []string{ThemeSystem, ThemeLight, ThemeDark}
}

// IsAllowedTheme reports whether theme is inside the closed set. It is the
// single predicate both the write validation and the read normalization use,
// so the two can never disagree.
func IsAllowedTheme(theme string) bool {
	for _, allowed := range AllowedThemes() {
		if theme == allowed {
			return true
		}
	}
	return false
}

// NormalizeTheme fails closed: any value outside the closed set (including the
// empty string, a hand-edited row, or a value written by a future version)
// becomes DefaultTheme. The shell therefore always resolves a theme the
// stylesheet defines and never an unvalidated stored value.
func NormalizeTheme(theme string) string {
	if IsAllowedTheme(theme) {
		return theme
	}
	return DefaultTheme
}

// Timeline order preference values (issue #438, S1). The set is closed: the
// Preferences page and the Activity header toggle offer exactly these two, the
// store holds one of them, and the detail turns the choice into one DOM order.
const (
	TimelineOrderNewest = "newest"
	TimelineOrderOldest = "oldest"
)

// DefaultTimelineOrder is the order a user without a stored preference gets,
// and the value every unknown or malformed stored value normalizes to. It is
// the product default the maintainer chose (S1: "Más nuevo arriba"), not a
// presentation accident.
const DefaultTimelineOrder = TimelineOrderNewest

// AllowedTimelineOrders returns the closed set of storable activity orders in
// the order both controls offer them: the default first, then the alternative.
func AllowedTimelineOrders() []string {
	return []string{TimelineOrderNewest, TimelineOrderOldest}
}

// IsAllowedTimelineOrder reports whether order is inside the closed set. It is
// the single predicate both the write validation and the read normalization
// use, so the two can never disagree.
func IsAllowedTimelineOrder(order string) bool {
	for _, allowed := range AllowedTimelineOrders() {
		if order == allowed {
			return true
		}
	}
	return false
}

// NormalizeTimelineOrder fails closed: any value outside the closed set
// (including the empty string, a hand-edited row, or a value written by a
// future version) becomes DefaultTimelineOrder. The detail therefore always
// renders an order it implements and never an unvalidated stored value.
func NormalizeTimelineOrder(order string) string {
	if IsAllowedTimelineOrder(order) {
		return order
	}
	return DefaultTimelineOrder
}

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
// (issue #210, issue #362, issue #377, issue #378, issue #438). The queue order,
// the email opt-out, the sidebar state, the interface theme, and the ticket
// activity order are all personal settings with no capability gate: every
// authenticated user owns exactly one stored value of each, and the handler
// layer is what requires a session. Validation happens BEFORE the store is
// reached (fail closed on an invalid write), and the queue-order, theme, and
// activity-order reads normalize any stored value outside their closed set. The
// five preferences share one table but never one value: each use case below
// touches only its own key.
type PreferencesService struct {
	preferences PreferencesStore
	email       EmailPreferencesStore
	sidebar     SidebarPreferencesStore
	theme       ThemePreferencesStore
	timeline    TimelineOrderPreferencesStore
}

// NewPreferencesService wires the preference use cases against the store
// ports: the queue order, the email opt-out (issue #362, S5), the sidebar
// state (issue #377, S2), and the interface theme (issue #378, S1).
//
// The activity order (issue #438, S1) is a FIFTH view of the same
// user_preferences rows, so it is discovered on the port the queue order already
// arrives through instead of widening the constructor every existing caller
// passes four ports to. The adapter that answers one key of that table answers
// all of them, so a wired deployment gains the preference with no extra call
// site; a store that predates the port keeps the documented newest-first default
// on read and REFUSES a write rather than pretending it was saved (see
// SetTimelineOrder).
func NewPreferencesService(preferences PreferencesStore, email EmailPreferencesStore, sidebar SidebarPreferencesStore, theme ThemePreferencesStore) *PreferencesService {
	svc := &PreferencesService{preferences: preferences, email: email, sidebar: sidebar, theme: theme}
	if timeline, ok := preferences.(TimelineOrderPreferencesStore); ok {
		svc.timeline = timeline
	}
	return svc
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

// GetTheme returns the actor's own stored theme, normalized to the closed set:
// no row, an empty value, or an unknown value all answer DefaultTheme. Only a
// real storage failure is an error. The key is the actor's own id, so one user
// can only ever read their own theme.
func (s *PreferencesService) GetTheme(ctx context.Context, actor domain.User) (string, error) {
	stored, err := s.theme.GetTheme(ctx, actor.ID)
	if err != nil {
		return "", err
	}
	return NormalizeTheme(stored), nil
}

// SetTheme validates the requested theme against the closed set and persists
// it for the actor. Anything outside the set is a ValidationError and the
// store is never touched (fail closed on invalid input); the value is stored
// verbatim because it is already inside the set.
func (s *PreferencesService) SetTheme(ctx context.Context, actor domain.User, theme string) error {
	if !IsAllowedTheme(theme) {
		return &domain.ValidationError{Field: "theme", Message: "invalid theme", Key: "error.invalid_theme"}
	}
	return s.theme.SetTheme(ctx, actor.ID, theme)
}

// errTimelineOrderStoreUnwired is the refusal a write answers when the wired
// store does not answer the activity-order port (issue #438, S1). It is an
// internal wiring fault, never a user mistake: the closed-set validation runs
// first, so this error can only mean the deployment serves a store that predates
// the key. Failing loudly keeps a deployment from telling somebody their choice
// was saved when nothing can store it.
var errTimelineOrderStoreUnwired = errors.New("application: timeline order store is not wired")

// GetTimelineOrder returns the actor's own stored activity order, normalized to
// the closed set: no row, an empty value, or an unknown value all answer
// DefaultTimelineOrder (newest first). A store that does not answer the port
// answers the same default, so the detail always has an order to render. Only a
// real storage failure is an error. The key is the actor's own id, so one user
// can only ever read their own feed order.
func (s *PreferencesService) GetTimelineOrder(ctx context.Context, actor domain.User) (string, error) {
	if s.timeline == nil {
		return DefaultTimelineOrder, nil
	}
	stored, err := s.timeline.GetTimelineOrder(ctx, actor.ID)
	if err != nil {
		return "", err
	}
	return NormalizeTimelineOrder(stored), nil
}

// SetTimelineOrder validates the requested activity order against the closed set
// and persists it for the actor. Anything outside the set is a ValidationError
// and the store is never touched (fail closed on invalid input); the value is
// stored verbatim because it is already inside the set. Both entry points (the
// Preferences select and the Activity header toggle) call exactly this use case,
// so they can never store two different things under one name.
func (s *PreferencesService) SetTimelineOrder(ctx context.Context, actor domain.User, order string) error {
	if !IsAllowedTimelineOrder(order) {
		return &domain.ValidationError{Field: "timeline_order", Message: "invalid timeline order", Key: "error.invalid_timeline_order"}
	}
	if s.timeline == nil {
		return errTimelineOrderStoreUnwired
	}
	return s.timeline.SetTimelineOrder(ctx, actor.ID, order)
}
