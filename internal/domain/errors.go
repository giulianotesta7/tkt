package domain

import (
	"errors"
	"fmt"
)

// English user-facing message constants (D5): single source of truth in the
// domain. The HTTP adapter maps typed errors to status codes, never rewrites
// these messages.
const (
	ErrMsgTransitionNotAllowed      = "transition not allowed"
	ErrMsgReopenReasonRequired      = "a reason is required to reopen the ticket"
	ErrMsgTitleRequired             = "title is required"
	ErrMsgInvalidPriority           = "invalid priority"
	ErrMsgConflictingUserAssignment = "cannot assign and unassign the user at the same time"
	ErrMsgPasswordRequired          = "password is required"
	ErrMsgCommentBodyRequired       = "comment body is required"
	ErrMsgUserNameRequired          = "name is required"
	ErrMsgUserEmailRequired         = "email is required"
	ErrMsgCategoryNameRequired      = "category name is required"
	ErrMsgUserInactive              = "user is inactive"
	ErrMsgUserRoleCannotAssign      = "user role cannot assign tickets"
	ErrMsgAssignTargetRole          = "assignment target must be an agent or above"
	ErrMsgReassignReasonRequired    = "a reason is required to reassign the ticket"
	ErrMsgUserCannotTransition      = "user role cannot transition tickets"
	ErrMsgUserCannotEdit            = "user role cannot edit tickets"
	ErrMsgAssignmentViaAssign       = "assignment changes must use the assign flow"
	ErrMsgBootstrapUnavailable      = "first-user setup is no longer available"
	ErrMsgRootProtected             = "the root account is protected"
)

// Sentinel errors naming the store contract failures (ports.go uses them as
// ErrNotFound/ErrDuplicate/ErrReferenced). The typed errors below carry
// structured data and satisfy errors.Is against these sentinels.
var (
	ErrNotFound             = errors.New("not found")
	ErrDuplicate            = errors.New("duplicate")
	ErrReferenced           = errors.New("referenced")
	ErrBootstrapUnavailable = errors.New("bootstrap unavailable")
	ErrRootProtected        = errors.New("root protected")
)

// ValidationError reports a field-level validation failure (422).
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string { return e.Message }

// InvalidTransitionError reports a move rejected by the transition matrix (422).
type InvalidTransitionError struct {
	From, To State
	Message  string
}

func (e *InvalidTransitionError) Error() string { return e.Message }

func NewInvalidTransitionError(from, to State) *InvalidTransitionError {
	return &InvalidTransitionError{
		From:    from,
		To:      to,
		Message: fmt.Sprintf("%s from %s to %s", ErrMsgTransitionNotAllowed, from, to),
	}
}

// ReopenReasonRequiredError reports a closed reopen without a reason (422).
type ReopenReasonRequiredError struct {
	Message string
}

func (e *ReopenReasonRequiredError) Error() string { return e.Message }

func NewReopenReasonRequiredError() *ReopenReasonRequiredError {
	return &ReopenReasonRequiredError{Message: ErrMsgReopenReasonRequired}
}

// ReassignReasonRequiredError reports an assignment change from person A to
// person B without a reason (422, ticket-access-assignment spec: the initial
// unassigned → person assignment never needs a reason; a reassignment
// ALWAYS does).
type ReassignReasonRequiredError struct {
	Message string
}

func (e *ReassignReasonRequiredError) Error() string { return e.Message }

func NewReassignReasonRequiredError() *ReassignReasonRequiredError {
	return &ReassignReasonRequiredError{Message: ErrMsgReassignReasonRequired}
}

// ForbiddenError reports an action the actor's role is not permitted to
// perform (403): a capability-gated operation the actor may still be able
// to READ — e.g. a user-role owner cannot transition or edit their own
// ticket (ticket-state-machine spec: role user MUST NOT perform
// transitions; design route policy: edit requires an assigned agent or
// admin/root). The check runs at the application boundary BEFORE any store
// mutation, so denied actors change nothing.
type ForbiddenError struct {
	Message string
}

func (e *ForbiddenError) Error() string { return e.Message }

func NewForbiddenError(message string) *ForbiddenError {
	return &ForbiddenError{Message: message}
}

// InvalidPriorityError reports an unsupported priority value (422).
type InvalidPriorityError struct {
	Field   string
	Message string
}

func (e *InvalidPriorityError) Error() string { return e.Message }

// InactiveUserError reports an operation that assigns a deactivated user
// (422, user-management spec). The user's historical assignments stay.
type InactiveUserError struct {
	Field   string
	Message string
}

func (e *InactiveUserError) Error() string { return e.Message }

func NewInactiveUserError(field string) *InactiveUserError {
	return &InactiveUserError{Field: field, Message: ErrMsgUserInactive}
}

// NotFoundError reports a missing entity (404). Kind names the entity
// ("ticket", "user", "category", "session"); ID carries the structured
// identifier for the handler. The message deliberately omits the ID.
type NotFoundError struct {
	Kind string
	ID   any
}

func (e *NotFoundError) Error() string { return fmt.Sprintf("%s not found", e.Kind) }

func (e *NotFoundError) Is(target error) bool { return target == ErrNotFound }

// DuplicateError reports a uniqueness violation (409): the email or name
// already exists.
type DuplicateError struct {
	Kind string
	Name string
}

func (e *DuplicateError) Error() string {
	return fmt.Sprintf("%s %q already exists", e.Kind, e.Name)
}

func (e *DuplicateError) Is(target error) bool { return target == ErrDuplicate }

// ReferencedError reports a delete blocked by references (409): the user is
// assigned to tickets or the category is used by tickets. Deactivation is the
// removal path for such entities.
type ReferencedError struct {
	Kind string
	ID   any
}

func (e *ReferencedError) Error() string {
	return fmt.Sprintf("%s is referenced and cannot be deleted", e.Kind)
}

func (e *ReferencedError) Is(target error) bool { return target == ErrReferenced }

// BootstrapUnavailableError reports that the first-user bootstrap cannot run
// because a user already exists (role-authorization "Concurrent bootstrap",
// user-management "Bootstrap unavailable with users present"). It is the
// deliberate failure of the concurrent /setup loser and of any later visitor
// racing the bootstrap; the setup flow redirects to login instead of
// surfacing an error page.
type BootstrapUnavailableError struct {
	Message string
}

func (e *BootstrapUnavailableError) Error() string { return e.Message }

func NewBootstrapUnavailableError() *BootstrapUnavailableError {
	return &BootstrapUnavailableError{Message: ErrMsgBootstrapUnavailable}
}

func (e *BootstrapUnavailableError) Is(target error) bool {
	return target == ErrBootstrapUnavailable
}

// RootProtectedError reports an action the root invariants forbid
// (role-authorization "Root Invariants"): editing, deactivating, deleting,
// or granting/demoting the root account. No actor — including root itself —
// may perform these actions; the typed error lets the adapter answer 403
// before any store call mutates state.
type RootProtectedError struct {
	Message string
}

func (e *RootProtectedError) Error() string { return e.Message }

func NewRootProtectedError() *RootProtectedError {
	return &RootProtectedError{Message: ErrMsgRootProtected}
}

func (e *RootProtectedError) Is(target error) bool { return target == ErrRootProtected }
