package httpadapter

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/domain"
)

func TestSaveFeedbackUsesExplicitServerOutcomes(t *testing.T) {
	t.Run("HTMX emits a settled success event with the canonical message", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/settings/appearance", nil)
		req.Header.Set("HX-Request", "true")

		saveFeedback(rec, req, "Appearance saved.", saveFeedbackSuccess)

		got := rec.Header().Get("X-Save-Feedback")
		for _, want := range []string{`"save-feedback"`, `"message":"Saved"`, `"kind":"success"`} {
			if !strings.Contains(got, want) {
				t.Errorf("HTMX feedback header = %q, want %q", got, want)
			}
		}
		if strings.Contains(got, "Appearance") {
			t.Errorf("HTMX feedback header must carry the canonical copy only, got %q", got)
		}
		if cookie := rec.Header().Get("Set-Cookie"); cookie != "" {
			t.Errorf("HTMX feedback must not set a replayable redirect cookie, got %q", cookie)
		}
	})

	t.Run("HTMX keeps the workflow publish message verbatim", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/categories/1/workflow", nil)
		req.Header.Set("HX-Request", "true")

		saveFeedback(rec, req, saveFeedbackPublished, saveFeedbackSuccess)

		if got := rec.Header().Get("X-Save-Feedback"); !strings.Contains(got, `"message":"Published"`) {
			t.Errorf("publish feedback header = %q, want the verbatim Published copy", got)
		}
	})

	t.Run("native redirect carries one signed feedback record with the canonical message", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/settings/appearance", nil)

		saveFeedback(rec, req, "Appearance saved.", saveFeedbackSuccess)

		cookie := rec.Result().Cookies()
		if len(cookie) != 1 || cookie[0].Name != saveFeedbackCookie {
			t.Fatalf("native feedback cookie = %#v, want one %q cookie", cookie, saveFeedbackCookie)
		}
		if strings.Contains(cookie[0].Value, "Appearance") {
			t.Errorf("feedback cookie must not expose the message, got %q", cookie[0].Value)
		}

		followUp := httptest.NewRequest(http.MethodGet, "/settings", nil)
		followUp.AddCookie(cookie[0])
		got := readSaveFeedback(followUp)
		if got.Message != saveFeedbackSaved || got.Kind != saveFeedbackSuccess {
			t.Errorf("readSaveFeedback() = %#v, want signed success with the canonical copy", got)
		}
	})

	t.Run("drawer feedback targets the active drawer", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/settings/desks/1", nil)
		req.Header.Set("HX-Request", "true")

		saveDrawerFeedback(rec, req, saveFeedbackSaved)

		got := rec.Header().Get("X-Save-Feedback")
		for _, want := range []string{`"message":"Saved"`, `"kind":"success"`, `"target":"drawer"`} {
			if !strings.Contains(got, want) {
				t.Errorf("drawer feedback header = %q, want %q", got, want)
			}
		}
	})

	t.Run("consumed native feedback is cleared", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/settings", nil)
		req.AddCookie(&http.Cookie{Name: saveFeedbackCookie, Value: "signed-feedback"})
		rec := httptest.NewRecorder()

		clearSaveFeedbackCookie(rec, req)

		cookies := rec.Result().Cookies()
		if len(cookies) != 1 || cookies[0].Name != saveFeedbackCookie || cookies[0].MaxAge != -1 {
			t.Fatalf("cleared feedback cookie = %#v, want one expired %q cookie", cookies, saveFeedbackCookie)
		}
	})
}

// TestTicketDetailActionsDoNotIssueSaveFeedback pins issue #234's placement
// rule on the ticket-detail actions the maintainer silenced: the inline
// title/priority edit, the assign, the transition, and the resolution
// confirmation all re-render #ticket-detail in place, so the value the actor
// just wrote is already on screen and the toast is a second "ok". The
// response must NOT carry the save-feedback channel (HTMX header for the
// swap, native flash cookie for the redirect). Each removed action is
// asserted together with its in-place result, so the test cannot pass by
// dropping the mutation itself, and the comment path is the kept-surface
// control that must still issue the channel.
func TestTicketDetailActionsDoNotIssueSaveFeedback(t *testing.T) {
	t.Run("inline title edit", func(t *testing.T) {
		h := newHarness(t)
		h.seedTicket(t, "Original title", nil)

		rec := h.postForm(t, "/tickets/1/edit", url.Values{"title": {"Edited title"}}, true)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if got := rec.Header().Get("X-Save-Feedback"); got != "" {
			t.Errorf("title edit must not carry the save-feedback header, got %q", got)
		}
		if !strings.Contains(rec.Body.String(), `value="Edited title"`) {
			t.Errorf("title edit body must show the new title in place, got: %.400s", rec.Body.String())
		}
	})

	t.Run("inline priority edit", func(t *testing.T) {
		h := newHarness(t)
		h.seedTicket(t, "Priority ticket", nil)

		rec := h.postForm(t, "/tickets/1/edit", url.Values{"priority": {"critical"}}, true)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if got := rec.Header().Get("X-Save-Feedback"); got != "" {
			t.Errorf("priority edit must not carry the save-feedback header, got %q", got)
		}
		if !strings.Contains(rec.Body.String(), "selected>Critical</option>") {
			t.Errorf("priority edit body must show Critical selected in place, got: %.400s", rec.Body.String())
		}
	})

	t.Run("assign", func(t *testing.T) {
		h := newHarness(t)
		h.seedTicket(t, "Assign ticket", nil)
		agent := seedUserRole(t, h.store, "Ann Agent", "ann@tkt.test", domain.RoleAgent)

		rec := h.postForm(t, "/tickets/1/assign", url.Values{"user_id": {strconv.FormatInt(agent.ID, 10)}}, true)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if got := rec.Header().Get("X-Save-Feedback"); got != "" {
			t.Errorf("assign must not carry the save-feedback header, got %q", got)
		}
		if !strings.Contains(rec.Body.String(), "Ann Agent") {
			t.Errorf("assign body must show the new assignee in place, got: %.400s", rec.Body.String())
		}
	})

	t.Run("transition", func(t *testing.T) {
		h := newHarness(t)
		h.seedTicket(t, "Transition ticket", nil)

		rec := h.postForm(t, "/tickets/1/transition", url.Values{"to": {"in_progress"}}, true)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if got := rec.Header().Get("X-Save-Feedback"); got != "" {
			t.Errorf("transition must not carry the save-feedback header, got %q", got)
		}
		if !strings.Contains(rec.Body.String(), `<span class="badge in_progress">In Progress</span>`) {
			t.Errorf("transition body must show the new state in place, got: %.400s", rec.Body.String())
		}
	})

	t.Run("resolution confirmation", func(t *testing.T) {
		h := newHarness(t)
		requester := seedUserRole(t, h.store, "Rosa", "rosa@tkt.test", domain.RoleUser)
		tkt := h.seedTicket(t, "Login page down", nil)
		if _, err := h.rawDB(t).Exec(`UPDATE tickets SET requester_user_id = ? WHERE id = ?`, requester.ID, tkt.ID); err != nil {
			t.Fatalf("pin requester: %v", err)
		}
		h.seedTransition(t, tkt.ID, domain.StateInProgress, "")
		h.seedTransition(t, tkt.ID, domain.StateResolved, "")
		sess := seedSession(t, h.store, requester.ID)

		req := httptest.NewRequest(http.MethodPost, "/tickets/1/confirmation", strings.NewReader(url.Values{"decision": {"confirm"}}.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Cookie", sessionCookie+"="+sess.ID)
		req.Header.Set("HX-Request", "true")
		rec := httptest.NewRecorder()
		h.mw.Wrap(h.mux).ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if got := rec.Header().Get("X-Save-Feedback"); got != "" {
			t.Errorf("resolution confirmation must not carry the save-feedback header, got %q", got)
		}
		view, err := h.tickets.GetByID(t.Context(), *h.admin, 1)
		if err != nil {
			t.Fatalf("view: %v", err)
		}
		if view.Ticket.State != domain.StateClosed {
			t.Errorf("state = %q, want closed after the confirmed resolution", view.Ticket.State)
		}
	})

	t.Run("native redirect issues no feedback flash", func(t *testing.T) {
		h := newHarness(t)
		h.seedTicket(t, "Original title", nil)

		rec := h.postForm(t, "/tickets/1/edit", url.Values{"title": {"Edited title"}}, false)

		wantRedirect(t, rec, http.StatusSeeOther, "/tickets/1")
		if got := rec.Header().Get("Set-Cookie"); strings.Contains(got, saveFeedbackCookie+"=") {
			t.Errorf("native ticket-detail mutation must not issue a feedback flash, got %q", got)
		}
	})

	t.Run("comment path keeps the feedback flash", func(t *testing.T) {
		h := newHarness(t)
		h.seedTicket(t, "Comment ticket", nil)

		rec := h.postForm(t, "/tickets/1/comments", url.Values{"body": {"Checking now"}}, false)

		wantRedirect(t, rec, http.StatusSeeOther, "/tickets/1")
		if got := rec.Header().Get("Set-Cookie"); !strings.Contains(got, saveFeedbackCookie+"=") {
			t.Errorf("comment path must keep the feedback flash, got %q", got)
		}
	})
}

// TestDeskDrawerMembershipCarriesNoSaveFeedback pins issue #234's placement
// rule for the desk membership actions: the unified drawer stays open and
// re-renders its member list in place, so the refreshed list is the
// confirmation and the response must not carry the save-feedback channel.
// Each case asserts the member list in place, so the test fails if the
// mutation is dropped rather than only if the toast disappears.
func TestDeskDrawerMembershipCarriesNoSaveFeedback(t *testing.T) {
	seedDesk := func(t *testing.T, h *harness) (*domain.Desk, *domain.User) {
		t.Helper()
		desk, err := h.desks.Create(t.Context(), *h.admin, "Support")
		if err != nil {
			t.Fatalf("create desk: %v", err)
		}
		return desk, seedUserRole(t, h.store, "Desk Member", "desk-member@tkt.test", domain.RoleAgent)
	}
	drawerContext := func(deskID int64) url.Values {
		return url.Values{
			"view":          {"structure"},
			"department_id": {"unassigned"},
			"desk_id":       {strconv.FormatInt(deskID, 10)},
		}
	}

	t.Run("HTMX add member renders the new member without the feedback header", func(t *testing.T) {
		h := newHarness(t)
		desk, agent := seedDesk(t, h)
		form := drawerContext(desk.ID)
		form.Set("user_id", strconv.FormatInt(agent.ID, 10))

		rec := h.postForm(t, "/desks/"+strconv.FormatInt(desk.ID, 10)+"/members", form, true)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if got := rec.Header().Get("X-Save-Feedback"); got != "" {
			t.Errorf("desk member add must not carry the save-feedback header, got %q", got)
		}
		if !strings.Contains(deskMemberList(rec.Body.String()), agent.Name) {
			t.Errorf("desk member add must render the new member in place, got: %.400s", rec.Body.String())
		}
	})

	t.Run("HTMX remove member drops it without the feedback header", func(t *testing.T) {
		h := newHarness(t)
		desk, agent := seedDesk(t, h)
		if err := h.desks.AddMember(t.Context(), *h.admin, desk.ID, agent.ID); err != nil {
			t.Fatalf("add member: %v", err)
		}

		rec := h.postForm(t, "/desks/"+strconv.FormatInt(desk.ID, 10)+"/members/"+strconv.FormatInt(agent.ID, 10)+"/delete", drawerContext(desk.ID), true)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if got := rec.Header().Get("X-Save-Feedback"); got != "" {
			t.Errorf("desk member remove must not carry the save-feedback header, got %q", got)
		}
		if strings.Contains(deskMemberList(rec.Body.String()), agent.Name) {
			t.Errorf("desk member remove must drop the member in place, got: %.400s", rec.Body.String())
		}
	})

	t.Run("native redirect issues no feedback flash", func(t *testing.T) {
		h := newHarness(t)
		desk, agent := seedDesk(t, h)
		form := drawerContext(desk.ID)
		form.Set("user_id", strconv.FormatInt(agent.ID, 10))

		rec := h.postForm(t, "/desks/"+strconv.FormatInt(desk.ID, 10)+"/members", form, false)

		if rec.Code != http.StatusSeeOther {
			t.Fatalf("status = %d, want 303 redirect", rec.Code)
		}
		if got := rec.Header().Get("Set-Cookie"); strings.Contains(got, saveFeedbackCookie+"=") {
			t.Errorf("native desk membership mutation must not issue a feedback flash, got %q", got)
		}
	})
}

// deskMemberList isolates the drawer's member list so a removed member's name
// cannot be confused with the still-present add-member option that lists them.
func deskMemberList(body string) string {
	start := strings.Index(body, `<ul class="desk-member-list">`)
	if start < 0 {
		return body
	}
	list := body[start:]
	if end := strings.Index(list, `</ul>`); end >= 0 {
		list = list[:end]
	}
	return list
}

func TestSaveFeedbackRejectsForgedOrExpiredCookie(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/settings", nil)
	req.AddCookie(&http.Cookie{Name: saveFeedbackCookie, Value: "forged"})

	if got := readSaveFeedback(req); got.Message != "" {
		t.Errorf("forged cookie produced feedback %#v", got)
	}
}

func TestSaveFeedbackRejectsExpiredCookie(t *testing.T) {
	now := time.Now()
	originalNow := saveFeedbackNow
	saveFeedbackNow = func() time.Time { return now }
	t.Cleanup(func() { saveFeedbackNow = originalNow })

	rec := httptest.NewRecorder()
	setSaveFeedbackCookie(rec, saveFeedbackData{Message: "Appearance saved.", Kind: saveFeedbackSuccess})
	cookie := rec.Result().Cookies()[0]
	saveFeedbackNow = func() time.Time { return now.Add(saveFeedbackTTL) }

	req := httptest.NewRequest(http.MethodGet, "/settings", nil)
	req.AddCookie(cookie)
	if got := readSaveFeedback(req); got.Message != "" {
		t.Errorf("expired cookie produced feedback %#v", got)
	}
}
