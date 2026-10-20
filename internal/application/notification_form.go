package application

import (
	"time"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// The S10 "a workflow form step now waits on this person" row: an answer to one
// step can advance the run onto a form that is somebody else's turn, and that
// person should learn there is something to do. Isolated from the rest of the
// notification policy in its own file because it is one self-contained row
// (maintainer's decision, e6-batch-2 L20: "Si, a la persona que tiene que actuar
// se le avisa").

// assigneeFormStepIndex reports the zero-based index of the step the plan leaves
// PENDING when that step is an ASSIGNEE form. Such a form's recipient is whoever
// holds the ticket when the transaction ends, which a plan that routes the ticket
// cannot name — the routing operation's template resolves it instead (see
// AssigneeNotification.FormWaitingStepIndex).
func assigneeFormStepIndex(def domain.WorkflowDefinition, cursor int) (int, bool) {
	step, ok := stepAt(def, cursor)
	if !ok || step.Type != domain.StepForm || step.Form == nil || step.Form.Actor != domain.FormActorAssignee {
		return 0, false
	}
	return cursor, true
}

// stepAt returns the definition's step at cursor, or false when the cursor names
// no step (the run ended, which is where a plan that completes the definition
// leaves it).
func stepAt(def domain.WorkflowDefinition, cursor int) (domain.WorkflowStep, bool) {
	if cursor < 0 || cursor >= len(def) {
		return domain.WorkflowStep{}, false
	}
	return def[cursor], true
}

// formWaitingNotifications builds the form_waiting row for the step the plan
// leaves PENDING, addressed to the person that step waits on: the requester for
// a requester form, the assignee for an assignee form. The actor rule and the
// read-access rule apply as everywhere else, so a form waiting on the person who
// just answered it notifies nobody.
//
// The assignee argument is nil whenever the plan ROUTES the ticket, because the
// assignee form's recipient is then a fact of the transaction and not of the
// plan: the routing operation's template names it (AssigneeNotification), and
// passing nil here keeps the row out of the pre-plan assignee's conditional
// block, which would otherwise duplicate it. Every other case — a requester form,
// or an assignee form reached without routing — is decided here.
func formWaitingNotifications(t *domain.Ticket, def domain.WorkflowDefinition, nextCursor int, actorUserID *int64, at time.Time, requester, assignee *NotificationRecipient) []domain.Notification {
	step, ok := stepAt(def, nextCursor)
	if !ok || step.Type != domain.StepForm || step.Form == nil {
		return nil
	}
	var waitingOn *NotificationRecipient
	switch step.Form.Actor {
	case domain.FormActorRequester:
		waitingOn = requester
	case domain.FormActorAssignee:
		waitingOn = assignee
	default:
		// An unknown form actor is a publish-rejected definition; never guess a
		// recipient for it.
		return nil
	}
	b := newNotificationBatch(t, actorUserID, at)
	b.add(domain.NotificationFormWaiting, waitingOn, nil, nil)
	rows := b.list()
	for i := range rows {
		index := nextCursor
		rows[i].StepIndex = &index
	}
	return rows
}
