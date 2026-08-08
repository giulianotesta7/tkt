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
)

// Sentinel errors naming the store contract failures (ports.go uses them as
// ErrNotFound/ErrDuplicate/ErrReferenced). The typed errors below carry
// structured data and satisfy errors.Is against these sentinels.
var (
	ErrNotFound   = errors.New("not found")
	ErrDuplicate  = errors.New("duplicate")
	ErrReferenced = errors.New("referenced")
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
