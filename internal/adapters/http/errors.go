package httpadapter

import (
	"errors"
	"net/http"
	"strings"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
	"github.com/giulianotesta7/tkt/internal/i18n"
)

const (
	agentQueueListTargetID = "agent-ticket-list"
	agentQueueListTarget   = "#agent-ticket-list"
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
		unrunnable       *domain.WorkflowUnrunnableError
		holdConflict     *domain.HoldConflictError
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
	case errors.As(err, &unrunnable):
		// 409, not 500: the request is well formed and the current state of the
		// category cannot route it. Before the typed error existed this arrived as
		// an unwrapped sentinel and fell through to the default arm.
		return 409, unrunnable.Error()
	case errors.As(err, &holdConflict):
		// 409, not 500: the hold change lost its compare-and-set race (issue
		// #352), so the ticket the actor read is no longer the ticket stored.
		// Reloading and retrying is the only recovery, exactly what the copy says.
		return 409, holdConflict.Error()
	case errors.As(err, &badCredentials):
		return 401, application.ErrMsgInvalidCredentials
	default:
		return 500, "Internal server error"
	}
}

// requestLocale resolves the interface language the request middleware stamped
// on the request, defaulting to English for a handler exercised without it (a
// unit test, an isolated call).
func requestLocale(r *http.Request) string {
	if language, ok := interfaceLanguageFrom(r.Context()); ok {
		return i18n.Normalize(language)
	}
	return i18n.Default
}

// httpError writes a plain-text error response whose copy follows the request's
// interface language, so a malformed route identifier on a Spanish instance is
// not an English sentence. Callers name a catalogue key, never a literal.
func httpError(w http.ResponseWriter, r *http.Request, key string, status int) {
	http.Error(w, i18n.T(requestLocale(r), key), status)
}

// errorKey maps a domain or application error's English message to the
// catalogue key of its user-facing copy. The English strings stay the single
// source of the wording (domain constants and application literals); this table
// only names the key at the presentation boundary, so the domain carries no
// locale logic. A message not in the table (an internal detail, or one that
// interpolates values) keeps its English text.
var errorKey = map[string]string{
	domain.ErrMsgTitleRequired:                               "error.title_required",
	domain.ErrMsgInvalidPriority:                             "error.invalid_priority",
	domain.ErrMsgReopenReasonRequired:                        "error.reopen_reason_required",
	domain.ErrMsgReassignReasonRequired:                      "error.reassign_reason_required",
	domain.ErrMsgConflictingUserAssignment:                   "error.conflicting_user_assignment",
	domain.ErrMsgPasswordRequired:                            "error.password_required",
	domain.ErrMsgCommentBodyRequired:                         "error.comment_body_required",
	domain.ErrMsgUserNameRequired:                            "error.name_required",
	domain.ErrMsgUserEmailRequired:                           "error.email_required",
	domain.ErrMsgCategoryNameRequired:                        "error.category_name_required",
	domain.ErrMsgUserInactive:                                "error.user_inactive",
	domain.ErrMsgUserRoleCannotAssign:                        "error.user_role_cannot_assign",
	domain.ErrMsgAssignTargetRole:                            "error.assign_target_role",
	domain.ErrMsgUserCannotTransition:                        "error.user_cannot_transition",
	domain.ErrMsgUserCannotEdit:                              "error.user_cannot_edit",
	domain.ErrMsgAssignmentViaAssign:                         "error.assignment_via_assign",
	domain.ErrMsgBootstrapUnavailable:                        "error.bootstrap_unavailable",
	domain.ErrMsgRootProtected:                               "error.root_protected",
	domain.ErrMsgCommentVisibilityInvalid:                    "error.comment_visibility_invalid",
	domain.ErrMsgUserCannotCommentInternal:                   "error.user_cannot_comment_internal",
	domain.ErrMsgCommentOnClosedTicket:                       "error.comment_on_closed_ticket",
	domain.ErrMsgClosedTicketReadOnly:                        "error.closed_ticket_read_only",
	domain.ErrMsgCategoryWorkflowUnavailable:                 "error.category_workflow_unavailable",
	domain.ErrMsgCreateUnassignedOnly:                        "error.create_unassigned_only",
	domain.ErrMsgSolutionTooLong:                             "error.solution_too_long",
	domain.ErrMsgCannedReplyTitleLength:                      "error.canned_reply_title_length",
	domain.ErrMsgCannedReplyBodyLength:                       "error.canned_reply_body_length",
	domain.ErrMsgCategoryNotRunnable:                         "error.category_not_runnable",
	domain.ErrMsgHoldConflict:                                "error.hold_conflict",
	application.ErrMsgInvalidCredentials:                     "error.invalid_credentials",
	application.ErrMsgCannedRepliesStaffOnly:                 "error.canned_replies_staff_only",
	application.ErrMsgCannedReplyDeskManagement:              "error.canned_reply_desk_management",
	application.ErrMsgFollowersStaffOnly:                     "error.followers_staff_only",
	application.ErrMsgClosureRequiresConfirmation:            "error.closure_requires_confirmation",
	application.ErrMsgNotTicketRequester:                     "error.not_ticket_requester",
	"desk name is required":                                  "error.desk_name_required",
	"category is required":                                   "error.category_required",
	"identifier is required":                                 "error.identifier_required",
	"department is required":                                 "error.department_required",
	"desk members must have agent, admin, or root role":      "error.desk_members_role",
	"invalid queue order":                                    "error.invalid_queue_order",
	"invalid theme":                                          "error.invalid_theme",
	"invalid interface language":                             "error.invalid_interface_language",
	"invalid internal comment background color":              "error.invalid_internal_comment_bg",
	"choose both metrics dates":                              "error.choose_both_metrics_dates",
	"metrics end date must not be before the start date":     "error.metrics_end_before_start",
	"metrics workload group must be agent or desk":           "error.metrics_group_invalid",
	"metrics dates must use YYYY-MM-DD":                      "error.metrics_date_format",
	"workflow must have at least one step":                   "error.workflow_min_steps",
	"workflow must have at most one terminal step":           "error.workflow_max_terminal",
	"resolve_ticket and close_ticket are mutually exclusive": "error.workflow_terminal_exclusive",
	"terminal step must be the final step":                   "error.workflow_terminal_last",
	"unknown workflow action":                                "error.unknown_workflow_action",
	"choose a source category to clone from":                 "error.clone_source_required",
	"the SLA warning percent must be between 1 and 99":       "error.sla_warning_range",
	"the working calendar needs at least one working day, a start minute strictly before its end minute, and a timezone": "error.sla_calendar_shape",
	"the working calendar minutes must be within 0 and 1439":                                                             "error.sla_calendar_minutes",
	"the working calendar timezone must be a portable IANA zone name":                                                    "error.sla_calendar_timezone",
	"invalid category view":                                         "error.invalid_category_view",
	"department is required when a desk is selected":                "error.department_required_for_desk",
	"invalid identifier":                                            "error.invalid_identifier",
	"decision must be confirm or reject":                            "error.invalid_decision",
	"desks cannot be ticket assignees":                              "error.desk_not_assignee",
	"invalid user":                                                  "error.invalid_user",
	"provide a title or a priority":                                 "error.provide_title_or_priority",
	"invalid role":                                                  "error.invalid_role",
	"the working calendar needs a start and end time in HH:MM form": "error.calendar_time_form",
}

// mapErrorFor is mapError with its message resolved through the request's
// interface language. The status table (and mapError's English contract) stays
// untouched; only the copy a user reads changes with the instance language.
func mapErrorFor(r *http.Request, err error) (int, string) {
	status, english := mapError(err)
	// A transition error interpolates its two states, so it is not a fixed
	// table entry. English keeps the domain's own bytes (S13: the raw state
	// identifiers, never a translated label); another locale rebuilds the
	// sentence from the narrative state labels.
	var invalidTrans *domain.InvalidTransitionError
	if errors.As(err, &invalidTrans) {
		locale := requestLocale(r)
		if locale == i18n.Default {
			return status, invalidTrans.Error()
		}
		return status, i18n.New(locale).Tf("error.transition_not_allowed",
			transitionStateLabel(locale, invalidTrans.From),
			transitionStateLabel(locale, invalidTrans.To))
	}
	if key, ok := errorKey[english]; ok {
		return status, i18n.T(requestLocale(r), key)
	}
	// A validation failure that carries structured data interpolates its own
	// sentence (a step position, a priority enum, a field key) instead of a
	// fixed table entry.
	var validation *domain.ValidationError
	if errors.As(err, &validation) && validation.Key != "" {
		return status, validationErrorMessage(i18n.New(requestLocale(r)), validation)
	}
	// The three store-contract failures interpolate the entity kind, which the
	// catalogue labels as chrome (label.entity.*). An entity the catalogue does
	// not name stays verbatim, and the default locale keeps the domain bytes.
	var notFound *domain.NotFoundError
	if errors.As(err, &notFound) {
		return status, localizedEntityError(requestLocale(r), english, "error.not_found", notFound.Kind, "")
	}
	var duplicate *domain.DuplicateError
	if errors.As(err, &duplicate) {
		return status, localizedEntityError(requestLocale(r), english, "error.duplicate", duplicate.Kind, duplicate.Name)
	}
	var referenced *domain.ReferencedError
	if errors.As(err, &referenced) {
		return status, localizedEntityError(requestLocale(r), english, "error.referenced", referenced.Kind, "")
	}
	// The generic 500 body is the same for every unrecognized internal error:
	// follow the instance language so a Spanish instance never answers an
	// English sentence. The default locale keeps the historical bytes.
	if status == http.StatusInternalServerError {
		return status, i18n.T(requestLocale(r), "error.internal_server")
	}
	return status, english
}

// localizedEntityError renders one NotFound/Duplicate/Referenced message in the
// active locale: the entity kind is chrome with a catalogue label
// (label.entity.*), so it follows the instance language, and a kind no
// catalogue names renders verbatim. The default locale keeps the domain's
// English bytes byte for byte (S13).
func localizedEntityError(locale, english, key, kind, name string) string {
	if locale == i18n.Default {
		return english
	}
	tr := i18n.New(locale)
	kindLabel := kind
	if label, ok := tr.Lookup("label.entity." + strings.ReplaceAll(kind, " ", "_")); ok {
		kindLabel = label
	}
	if name == "" {
		return tr.Tf(key, kindLabel)
	}
	return tr.Tf(key, kindLabel, name)
}

// transitionStateLabel resolves one endpoint of a rejected transition through
// the narrative state labels. A state no catalogue names is a value the user
// typed, not chrome: it renders exactly as the domain carries it, so it is
// never re-cased into a label.
func transitionStateLabel(locale string, state domain.State) string {
	if label, ok := i18n.New(locale).Lookup("tickets.timeline.state." + string(state)); ok {
		return label
	}
	return string(state)
}
