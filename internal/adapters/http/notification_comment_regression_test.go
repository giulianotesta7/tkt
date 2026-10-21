package httpadapter

import (
	"net/http"
	"net/url"
	"strconv"
	"testing"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// Issue #344 comment regressions through the real HTTP routes, comment service,
// comment unit of work, and notification store:
//   - S16: a person who cannot see internal comments is never told one exists;
//   - S12/S11: a refused comment leaves the comments table, the notifications
//     table, and the full-text index exactly as they were.

// commentAtomicSnapshot is the three-way "nothing changed" evidence for one
// ticket: the comment rows, the notification rows, and the full-text index
// entries carrying the probe term. The index is part of the snapshot because
// migration 0002 keeps it current from triggers that fire INSIDE the comment
// transaction, so a rolled-back insert must leave no trace there either.
type commentAtomicSnapshot struct {
	comments      int64
	notifications int64
	ftsMatches    int64
}

func takeCommentAtomicSnapshot(t *testing.T, h *harness, ticketID int64, marker string) commentAtomicSnapshot {
	t.Helper()
	db := h.rawDB(t)
	return commentAtomicSnapshot{
		comments:      scanOneInt(t, db, `SELECT COUNT(*) FROM comments WHERE ticket_id = ?`, ticketID),
		notifications: scanOneInt(t, db, `SELECT COUNT(*) FROM notifications WHERE ticket_id = ?`, ticketID),
		ftsMatches:    scanOneInt(t, db, `SELECT COUNT(*) FROM tickets_fts WHERE tickets_fts MATCH ?`, marker),
	}
}

// TestRefusedCommentLeavesCommentsNotificationsAndFTSUnchanged proves a refused
// comment writes NOTHING: the requester of a ticket may not post an internal
// comment, and the refusal happens before the comment unit of work, so no
// comment row, no notification row, and no full-text entry can appear.
func TestRefusedCommentLeavesCommentsNotificationsAndFTSUnchanged(t *testing.T) {
	h := newHarness(t)
	requester := seedUserRole(t, h.store, "Rita", "rita-refused@example.com", domain.RoleUser)
	tkt := seedUserOwnedTicket(t, h, requester, "Refused comment")
	agent := seedUserRole(t, h.store, "Ana", "ana-refused@example.com", domain.RoleAgent)
	h.assignTicket(t, tkt.ID, agent.ID)

	// pi-lens-ignore: go-hardcoded-secrets
	const marker = "REFUSEDPROBE"
	before := takeCommentAtomicSnapshot(t, h, tkt.ID, marker)

	rec := h.postFormAs(t, "/tickets/"+strconv.FormatInt(tkt.ID, 10)+"/comments",
		url.Values{"body": {marker + " staff only"}, "visibility": {"internal"}}, h.sessionFor(t, requester.ID))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("internal comment by the requester = %d, want 403 (body %.300s)", rec.Code, rec.Body.String())
	}

	after := takeCommentAtomicSnapshot(t, h, tkt.ID, marker)
	if after != before {
		t.Fatalf("snapshot after the refused comment = %+v, want it unchanged at %+v", after, before)
	}
	if after.ftsMatches != 0 {
		t.Fatalf("refused comment body reached the full-text index (%d matches)", after.ftsMatches)
	}

	// Positive control: an ACCEPTED comment moves the same snapshot, so the
	// helper is sensitive to the writes it must catch.
	if rec := h.postFormAs(t, "/tickets/"+strconv.FormatInt(tkt.ID, 10)+"/comments",
		url.Values{"body": {"Public note " + marker}, "visibility": {"public"}}, h.sessionFor(t, agent.ID)); rec.Code != http.StatusSeeOther {
		t.Fatalf("public comment by the assignee = %d, want 303 (body %.300s)", rec.Code, rec.Body.String())
	}
	accepted := takeCommentAtomicSnapshot(t, h, tkt.ID, marker)
	if accepted.comments != before.comments+1 {
		t.Errorf("comments = %d, want %d", accepted.comments, before.comments+1)
	}
	if accepted.ftsMatches != 1 {
		t.Errorf("fts matches = %d, want the accepted comment indexed once", accepted.ftsMatches)
	}
	if accepted.notifications <= before.notifications {
		t.Errorf("notifications = %d, want the requester's comment_public row added", accepted.notifications)
	}
}

// TestCommentInternalNotifiesNobodyWhenTheAssigneeCannotSeeIt is the S16 rule
// applied to a legacy ticket whose assignee is a user-role person: a user cannot
// read internal comments, so being told one exists would already reveal it.
// Nobody is notified, and the comment itself still persists for the staff.
func TestCommentInternalNotifiesNobodyWhenTheAssigneeCannotSeeIt(t *testing.T) {
	h := newHarness(t)
	requester := seedUserRole(t, h.store, "Rita", "rita-internal-s16@example.com", domain.RoleUser)
	legacyAssignee := seedUserRole(t, h.store, "Uma", "uma-internal-s16@example.com", domain.RoleUser)
	tkt := seedUserOwnedTicket(t, h, requester, "Internal comment leak check")
	// Assignment rules stop a user-role assignee today; a legacy row can still
	// name one, and the notification policy must not depend on the rules holding.
	h.rawDB(t).Exec(`UPDATE tickets SET user_id = ? WHERE id = ?`, legacyAssignee.ID, tkt.ID)

	if rec := h.postForm(t, "/tickets/"+strconv.FormatInt(tkt.ID, 10)+"/comments",
		url.Values{"body": {"Staff only: customer is upset"}, "visibility": {"internal"}}, false); rec.Code != http.StatusSeeOther {
		t.Fatalf("internal comment = %d, want 303 (body %.300s)", rec.Code, rec.Body.String())
	}

	if got := scanOneInt(t, h.rawDB(t), `SELECT COUNT(*) FROM comments WHERE ticket_id = ? AND visibility = 'internal'`, tkt.ID); got != 1 {
		t.Fatalf("internal comments = %d, want the comment to persist for staff", got)
	}
	if got := scanOneInt(t, h.rawDB(t), `SELECT COUNT(*) FROM notifications WHERE ticket_id = ? AND kind IN ('comment_public','comment_internal')`, tkt.ID); got != 0 {
		t.Fatalf("comment notifications = %d, want none (none of the candidates may see an internal comment)", got)
	}
}
