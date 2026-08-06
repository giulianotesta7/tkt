package domain

import "fmt"

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
