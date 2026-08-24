package httpadapter

import (
	"errors"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// mapError translates application/domain errors into the D5 status table.
// The English messages come from the domain constants (single source); the
// adapter never rewrites them. InvalidCredentialsError is the single generic
// 401 (no user enumeration); anything unrecognized degrades to 500 with the
// generic text — internal details never reach the client.
func mapError(err error) (int, string) {
	var (
		validation       *domain.ValidationError
		invalidTrans     *domain.InvalidTransitionError
		reopenReason     *domain.ReopenReasonRequiredError
		reassignReason   *domain.ReassignReasonRequiredError
		inactiveUser     *domain.InactiveUserError
		invalidPriority  *domain.InvalidPriorityError
		notFound         *domain.NotFoundError
		duplicate        *domain.DuplicateError
		referenced       *domain.ReferencedError
		rootProtected    *domain.RootProtectedError
		forbidden        *domain.ForbiddenError
		workflowConflict *domain.WorkflowPositionConflictError
		badCredentials   *application.InvalidCredentialsError
	)
	switch {
	case errors.As(err, &validation):
		return 422, validation.Error()
	case errors.As(err, &invalidTrans):
		return 422, invalidTrans.Error()
	case errors.As(err, &reopenReason):
		return 422, reopenReason.Error()
	case errors.As(err, &reassignReason):
		return 422, reassignReason.Error()
	case errors.As(err, &inactiveUser):
		return 422, inactiveUser.Error()
	case errors.As(err, &invalidPriority):
		return 422, invalidPriority.Error()
	case errors.As(err, &notFound):
		return 404, notFound.Error()
	case errors.As(err, &duplicate):
		return 409, duplicate.Error()
	case errors.As(err, &referenced):
		return 409, referenced.Error()
	case errors.As(err, &rootProtected):
		return 403, rootProtected.Error()
	case errors.As(err, &forbidden):
		return 403, forbidden.Error()
	case errors.As(err, &workflowConflict):
		return 422, workflowConflict.Error()
	case errors.As(err, &badCredentials):
		return 401, application.ErrMsgInvalidCredentials
	default:
		return 500, "Internal server error"
	}
}
