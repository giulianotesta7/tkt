package application

import (
	"time"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// The S10 "a workflow form step now waits on this person" row: an answer to one
// step can advance the run onto a form that is somebody else's turn, and that
// person should learn there is something to do. Isolated from the rest of the
// notification policy in its own file because the maintainer has not confirmed
// this row yet (e6-batch-2 L5) — removing it means deleting this file, its test
// file, and the one call in the runner.

// formWaitingNotifications builds the form_waiting row for the step the plan
// leaves PENDING, addressed to the person that step waits on: the requester for
// a requester form, the assignee for an assignee form. The actor rule and the
// read-access rule apply as everywhere else, so a form waiting on the person who
// just answered it notifies nobody.
func formWaitingNotifications(t *domain.Ticket, def domain.WorkflowDefinition, nextCursor int, actorUserID *int64, at time.Time, requester, assignee *NotificationRecipient) []domain.Notification {
	if t == nil || nextCursor < 0 || nextCursor >= len(def) {
		return nil
	}
	step := def[nextCursor]
	if step.Type != domain.StepForm || step.Form == nil {
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
