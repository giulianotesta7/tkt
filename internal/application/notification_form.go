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
//
// KNOWN GAP (issue #344 slice B3 report): an assignee form left pending by a
// plan whose least_loaded step MOVES the ticket tells nobody, because the new
// assignee is a fact of the transaction and not of the plan. The row this
// function builds for the pre-plan assignee now travels as a conditional row that
// the unit of work releases only when the routing re-selects that person — so a
// RE-SELECTION does notify the holder (correct), a CLAIM notifies nobody (the
// claimant is the actor), and a REROUTE leaves the newly selected assignee
// uninformed: they learn about the assignment (their "assigned" row) but not that
// an assignee form is waiting on them. Closing that last case means carrying a
// form_waiting TEMPLATE on the operation, filled with the in-transaction selection
// like NotifyAssignee — deliberately NOT done here, because this row is still
// unconfirmed by the maintainer (e6-batch-2 L5) and the fix would extend an
// unconfirmed feature. Documented rather than guessed.
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
