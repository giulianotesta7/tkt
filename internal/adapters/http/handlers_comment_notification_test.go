package httpadapter

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// Issue #344 slice B3 at the HTTP boundary: posting a comment now writes the
// comment AND the notifications the application computed for it in one
// transaction (specs S10 comment rows, S12, S16), wired through cmd/server's
// path — this harness builds the same graph. The POST itself must keep
// answering exactly what it answered before: the same redirect, the same
// status, the same bytes.

// seedCommentNotificationFixture creates a ticket owned by a user-role
// requester and assigned to an agent, so both comment recipients exist.
func seedCommentNotificationFixture(t *testing.T, h *harness) (requester, assignee *domain.User, tkt *domain.Ticket) {
	t.Helper()
	requester = seedUserRole(t, h.store, "Rita", "rita-notify@example.com", domain.RoleUser)
	tkt = seedUserOwnedTicket(t, h, requester, "Comment notifications")
	assignee = seedUserRole(t, h.store, "Ana", "ana-notify@example.com", domain.RoleAgent)
	h.assignTicket(t, tkt.ID, assignee.ID)
	return requester, assignee, tkt
}

// commentPost sends the real form the comment box posts and returns the
// response plus the stored comment id.
func commentPost(t *testing.T, h *harness, ticketID int64, body, visibility string) (*httptest.ResponseRecorder, int64) {
	t.Helper()
	form := url.Values{"body": {body}}
	if visibility != "" {
		form.Set("visibility", visibility)
	}
	rec := h.postForm(t, "/tickets/"+strconv.FormatInt(ticketID, 10)+"/comments", form, false)
	// The comment row is the only row the comment form creates; the handler's
	// redirect proves it committed.
	return rec, scanOneInt(t, h.rawDB(t), `SELECT MAX(id) FROM comments WHERE ticket_id = ?`, ticketID)
}

// TestCommentPostWritesTheRecipientsNotifications proves the public and
// internal S10 comment rows end to end through the real comment service, store,
// and transaction: a public comment reaches the requester and the assignee, an
// internal one reaches only the assignee, and neither ever reaches the author.
func TestCommentPostWritesTheRecipientsNotifications(t *testing.T) {
	h := newHarness(t)
	requester, assignee, tkt := seedCommentNotificationFixture(t, h)

	rec, publicID := commentPost(t, h, tkt.ID, "Deployed to staging", "public")
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("public comment status = %d, want %d", rec.Code, http.StatusSeeOther)
	}
	if loc := rec.Header().Get("Location"); loc != "/tickets/"+strconv.FormatInt(tkt.ID, 10) {
		t.Errorf("redirect = %q, want the ticket detail", loc)
	}
	for _, who := range []*domain.User{requester, assignee} {
		rows := commentNotifications(t, h, who.ID)
		if len(rows) != 1 || rows[0].kind != string(domain.NotificationCommentPublic) {
			t.Fatalf("user %d notifications = %+v, want one comment_public", who.ID, rows)
		}
		if rows[0].commentID != publicID {
			t.Errorf("user %d notification points at comment %d, want %d", who.ID, rows[0].commentID, publicID)
		}
		if rows[0].actorID != h.admin.ID {
			t.Errorf("user %d notification actor = %d, want the commenting admin %d", who.ID, rows[0].actorID, h.admin.ID)
		}
	}
	if rows := commentNotifications(t, h, h.admin.ID); len(rows) != 0 {
		t.Fatalf("the commenting admin has %d notifications, want none: %+v", len(rows), rows)
	}

	// S16: an internal comment reaches the assignee only. The requester's row
	// count must not move, so the panel cannot reveal that it happened.
	rec, internalID := commentPost(t, h, tkt.ID, "Staff only: customer is upset", "internal")
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("internal comment status = %d, want %d", rec.Code, http.StatusSeeOther)
	}
	rows := commentNotifications(t, h, assignee.ID)
	if len(rows) != 2 || rows[0].kind != string(domain.NotificationCommentInternal) {
		t.Fatalf("assignee notifications = %+v, want the internal comment newest first", rows)
	}
	if rows[0].commentID != internalID {
		t.Errorf("assignee internal-comment notification points at %d, want %d", rows[0].commentID, internalID)
	}
	if rows := commentNotifications(t, h, requester.ID); len(rows) != 1 {
		t.Fatalf("requester has %d notifications, want only the public one: %+v", len(rows), rows)
	}
}

// TestCommentPostStoresNoCommentTextInTheNotification proves S15/S16 at the row
// level: the notification carries a kind and the comment's id, and the internal
// comment's body appears nowhere in the notifications table — so no later
// surface can render it to a requester.
func TestCommentPostStoresNoCommentTextInTheNotification(t *testing.T) {
	h := newHarness(t)
	_, _, tkt := seedCommentNotificationFixture(t, h)

	// pi-lens-ignore: go-hardcoded-secrets
	const internalBody = "INTERNAL-ONLY-NOTIFICATION-PROBE"
	if rec, _ := commentPost(t, h, tkt.ID, internalBody, "internal"); rec.Code != http.StatusSeeOther {
		t.Fatalf("internal comment status = %d, want %d", rec.Code, http.StatusSeeOther)
	}

	raw := h.rawDB(t)
	if got := scanOneInt(t, raw, `SELECT COUNT(*) FROM notifications WHERE from_value LIKE '%'||?||'%'
		OR to_value LIKE '%'||?||'%' OR kind LIKE '%'||?||'%'`, internalBody, internalBody, internalBody); got != 0 {
		t.Fatalf("%d notification rows carry the internal comment text, want 0", got)
	}
	if got := scanOneInt(t, raw, `SELECT COUNT(*) FROM notifications WHERE kind LIKE 'comment_%'`); got != 1 {
		t.Fatalf("%d comment notification rows, want exactly the assignee's", got)
	}
	if got := scanOneInt(t, raw, `SELECT COUNT(*) FROM notifications WHERE kind LIKE 'comment_%' AND comment_id IS NULL`); got != 0 {
		t.Fatalf("%d comment notifications without their comment id, want 0", got)
	}
	// The actor is the comment's author, so no notification references a user
	// the comment row does not already reference (users-deletion advisory).
	if got := scanOneInt(t, raw, `SELECT COUNT(*) FROM notifications n JOIN comments c ON c.id = n.comment_id
		WHERE n.kind LIKE 'comment_%' AND n.actor_user_id IS NOT c.author_user_id`); got != 0 {
		t.Fatalf("%d notifications whose actor is not the comment author, want 0", got)
	}
	// The internal body IS stored on the comment row, and only there.
	if got := scanOneInt(t, raw, `SELECT COUNT(*) FROM comments WHERE body = ?`, internalBody); got != 1 {
		t.Fatalf("%d comment rows hold the internal body, want 1", got)
	}
}

// TestCommentPostKeepsItsResponse PRESERVES the comment form's previous
// behavior (S12 is a transaction change, not a UI change): the same status, the
// same redirect, and the same English bytes — the saved feedback, the comment
// body in the timeline, and the HTMX fragment instead of a redirect.
func TestCommentPostKeepsItsResponse(t *testing.T) {
	h := newHarness(t)
	_, _, tkt := seedCommentNotificationFixture(t, h)
	target := "/tickets/" + strconv.FormatInt(tkt.ID, 10) + "/comments"

	full := h.postForm(t, target, url.Values{"body": {"Deployed to staging"}}, false)
	if full.Code != http.StatusSeeOther {
		t.Fatalf("full POST status = %d, want %d", full.Code, http.StatusSeeOther)
	}
	if loc := full.Header().Get("Location"); loc != "/tickets/"+strconv.FormatInt(tkt.ID, 10) {
		t.Errorf("full POST Location = %q, want the ticket detail", loc)
	}
	if body := full.Body.String(); body != "" {
		t.Errorf("full POST body = %q, want the empty redirect body", body)
	}
	// The native redirect carries the same server-issued flash record as before.
	if cookie := findCookie(full, saveFeedbackCookie); cookie == nil {
		t.Error("full POST must set the save-feedback flash cookie")
	}

	hx := h.postForm(t, target, url.Values{"body": {"Second note"}, "HX-Request": {"true"}}, true)
	if hx.Code != http.StatusOK {
		t.Fatalf("HX POST status = %d, want 200", hx.Code)
	}
	if loc := hx.Header().Get("Location"); loc != "" {
		t.Errorf("HX POST Location = %q, want none", loc)
	}
	fragment := hx.Body.String()
	if !strings.Contains(fragment, "Second note") {
		t.Errorf("HX fragment must render the new comment, got: %s", fragment)
	}
	// The HX response carries the same server-issued feedback header as before.
	if got := hx.Header().Get("X-Save-Feedback"); !strings.Contains(got, `"message":"`+saveFeedbackSaved+`"`) {
		t.Errorf("HX feedback header = %q, want the saved outcome", got)
	}

	// A rejected body keeps its 422 and stores nothing.
	bad := h.postForm(t, target, url.Values{"body": {"   "}}, false)
	if bad.Code != http.StatusUnprocessableEntity {
		t.Fatalf("empty-body status = %d, want %d", bad.Code, http.StatusUnprocessableEntity)
	}
	if got := scanOneInt(t, h.rawDB(t), `SELECT COUNT(*) FROM comments WHERE ticket_id = ?`, tkt.ID); got != 2 {
		t.Fatalf("%d stored comments, want the two accepted and none from the rejection", got)
	}
}

// findCookie returns the named response cookie, or nil when absent.
func findCookie(rec *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// commentNotificationRow is one stored COMMENT notification row, read straight
// from the database: slice B4 owns the store port's read surface and no handler
// renders these rows yet. The fixture's assignment writes its own "assigned"
// row (slice B2), so every read here is narrowed to the two comment kinds.
type commentNotificationRow struct {
	kind      string
	commentID int64
	actorID   int64
}

// commentNotifications returns the recipient's stored notifications, newest
// first (created_at DESC, id DESC — the center's order).
func commentNotifications(t *testing.T, h *harness, userID int64) []commentNotificationRow {
	t.Helper()
	raw := h.rawDB(t)
	rows, err := raw.Query(`SELECT kind, comment_id, actor_user_id FROM notifications
		WHERE user_id = ? AND kind IN ('comment_public', 'comment_internal')
		ORDER BY created_at DESC, id DESC`, userID)
	if err != nil {
		t.Fatalf("read notifications: %v", err)
	}
	defer rows.Close()
	var out []commentNotificationRow
	for rows.Next() {
		var r commentNotificationRow
		if err := rows.Scan(&r.kind, &r.commentID, &r.actorID); err != nil {
			t.Fatalf("scan notification: %v", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read notifications: %v", err)
	}
	return out
}
