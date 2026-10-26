package httpadapter

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// Following a ticket and mentioning a colleague (issue #418) at the HTTP layer:
// the header's follow control, the Properties row, the two mention inputs that
// share one candidate list, and the notification the two of them produce.
//
// The rules these tests hold:
//   - a follow control belongs to staff who can already open the ticket, and it
//     answers the ticket's own not-found to anybody else, so neither route says
//     whether a ticket exists;
//   - a requester never receives the follow control, the followers row or a
//     mention input, and both routes refuse;
//   - the candidate list is every OTHER staff member who can open the ticket
//     right now, so a requester, an off-desk agent and the actor are absent;
//   - mentioning somebody notifies them once and subscribes them to the ticket.

// followsFixture arranges a requester-owned ticket assigned to one agent, a
// second agent the ticket is out of reach for, and live sessions for everybody.
type followsFixture struct {
	agent      *domain.User
	agentToken string
	other      *domain.User
	otherToken string
	requester  *domain.User
	reqToken   string
	adminToken string
	ticket     *domain.Ticket
}

func seedFollows(t *testing.T, h *harness) followsFixture {
	t.Helper()
	requester := seedUserRole(t, h.store, "Carol Requester", "carol@tkt.test", domain.RoleUser)
	ticket, err := h.tickets.Create(t.Context(), *requester, application.CreateTicketInput{
		Title:       "VPN drops every hour",
		Description: "I have to reconnect each time.",
		CategoryID:  h.bugCategory.ID,
		Priority:    domain.PriorityHigh,
	})
	if err != nil {
		t.Fatalf("create requester ticket: %v", err)
	}
	agent := seedUserRole(t, h.store, "Dana Agent", "dana@tkt.test", domain.RoleAgent)
	h.assignTicket(t, ticket.ID, agent.ID)
	return followsFixture{
		agent:      agent,
		agentToken: h.sessionFor(t, agent.ID),
		other:      seedUserRole(t, h.store, "Frank Agent", "frank@tkt.test", domain.RoleAgent),
		otherToken: "",
		requester:  requester,
		reqToken:   h.sessionFor(t, requester.ID),
		adminToken: h.adminSession.ID,
		ticket:     ticket,
	}
}

// withOtherSession gives the off-desk agent a live session, lazily: most tests
// do not need to speak as them.
func (f *followsFixture) withOtherSession(t *testing.T, h *harness) string {
	t.Helper()
	if f.otherToken == "" {
		f.otherToken = h.sessionFor(t, f.other.ID)
	}
	return f.otherToken
}

// followPath formats the follow or unfollow route of one ticket.
func followPath(ticketID int64, verb string) string {
	return "/tickets/" + id(ticketID) + "/" + verb
}

// following reports whether the user follows the ticket through the real store
// port, which is the persisted state the routes must write.
func following(t *testing.T, h *harness, ticketID, userID int64) bool {
	t.Helper()
	ok, err := h.store.TicketFollowerStore().IsFollowing(t.Context(), ticketID, userID)
	if err != nil {
		t.Fatalf("read the follow relation: %v", err)
	}
	return ok
}

// hxPostAs runs an HTMX form POST as one session's user: the shape a header
// action submits when a script is present.
func hxPostAs(t *testing.T, h *harness, path string, form url.Values, sessionID string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Cookie", sessionCookie+"="+sessionID)
	req.Header.Set("HX-Request", "true")
	rec := httptest.NewRecorder()
	h.mw.Wrap(h.mux).ServeHTTP(rec, req)
	return rec
}

// TestFollowControlFollowsAndUnfollows proves the header control is one
// subscription a click flips: it renders the current state, both routes write
// it, the swap answers the new state in the button, and the native form still
// posts and redirects back to the ticket.
func TestFollowControlFollowsAndUnfollows(t *testing.T) {
	h := newHarness(t)
	f := seedFollows(t, h)

	detail := getAs(t, h, "/tickets/"+id(f.ticket.ID), f.agentToken)
	if detail.Code != http.StatusOK {
		t.Fatalf("detail status = %d, want 200: %.600s", detail.Code, detail.Body.String())
	}
	body := detail.Body.String()
	for _, want := range []string{
		`class="detail-action detail-follow"`,
		`action="` + followPath(f.ticket.ID, "follow") + `"`,
		`hx-post="` + followPath(f.ticket.ID, "follow") + `"`,
		`hx-target="#ticket-detail" hx-swap="outerHTML"`,
		`aria-pressed="false">` + "Follow" + `<`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the header must offer %q, got: %.1600s", want, body)
		}
	}

	followed := hxPostAs(t, h, followPath(f.ticket.ID, "follow"), url.Values{}, f.agentToken)
	if followed.Code != http.StatusOK {
		t.Fatalf("follow status = %d, want 200: %.600s", followed.Code, followed.Body.String())
	}
	swapped := followed.Body.String()
	for _, want := range []string{
		`aria-pressed="true">` + "Following" + `<`,
		`action="` + followPath(f.ticket.ID, "unfollow") + `"`,
	} {
		if !strings.Contains(swapped, want) {
			t.Errorf("the swap must answer %q, got: %.1600s", want, swapped)
		}
	}
	if !following(t, h, f.ticket.ID, f.agent.ID) {
		t.Error("the follow route left the subscription unwritten")
	}

	// Following twice is the same relation, so a repeated click is not an error.
	if again := hxPostAs(t, h, followPath(f.ticket.ID, "follow"), url.Values{}, f.agentToken); again.Code != http.StatusOK {
		t.Fatalf("repeated follow status = %d, want 200: %.600s", again.Code, again.Body.String())
	}
	if !following(t, h, f.ticket.ID, f.agent.ID) {
		t.Error("a repeated follow dropped the subscription")
	}

	unfollowed := hxPostAs(t, h, followPath(f.ticket.ID, "unfollow"), url.Values{}, f.agentToken)
	if unfollowed.Code != http.StatusOK {
		t.Fatalf("unfollow status = %d, want 200: %.600s", unfollowed.Code, unfollowed.Body.String())
	}
	if !strings.Contains(unfollowed.Body.String(), `aria-pressed="false">`+"Follow"+`<`) {
		t.Errorf("unfollowing must restore the plain control, got: %.1600s", unfollowed.Body.String())
	}
	if following(t, h, f.ticket.ID, f.agent.ID) {
		t.Error("the unfollow route left the subscription in place")
	}

	// The native form fallback (no script) writes the same relation and returns
	// to the ticket.
	native := h.postFormAs(t, followPath(f.ticket.ID, "follow"), url.Values{}, f.agentToken)
	wantRedirect(t, native, http.StatusSeeOther, "/tickets/"+id(f.ticket.ID))
	if !following(t, h, f.ticket.ID, f.agent.ID) {
		t.Error("the native follow form wrote nothing")
	}
}

// TestFollowRoutesAnswerNotFoundOutsideScope proves both routes answer the
// ticket's own not-found to a staff member the ticket is out of reach for, so
// the control can never be used to discover tickets, and neither route writes.
func TestFollowRoutesAnswerNotFoundOutsideScope(t *testing.T) {
	h := newHarness(t)
	f := seedFollows(t, h)
	session := f.withOtherSession(t, h)

	for _, verb := range []string{"follow", "unfollow"} {
		t.Run(verb, func(t *testing.T) {
			rec := h.postFormAs(t, followPath(f.ticket.ID, verb), url.Values{}, session)
			if rec.Code != http.StatusNotFound {
				t.Fatalf("%s status = %d, want 404: %.600s", verb, rec.Code, rec.Body.String())
			}
			if body := rec.Body.String(); strings.Contains(body, f.ticket.Title) {
				t.Errorf("%s leaked the ticket: %.600s", verb, body)
			}
			if following(t, h, f.ticket.ID, f.other.ID) {
				t.Errorf("%s wrote a subscription for an out-of-scope agent", verb)
			}
		})
	}
}

// TestRequesterNeverSeesOrReachesFollowing proves the requester half: no follow
// control, no followers row, no mention input on the page, and both routes
// refuse without writing.
func TestRequesterNeverSeesOrReachesFollowing(t *testing.T) {
	h := newHarness(t)
	f := seedFollows(t, h)

	detail := getAs(t, h, "/tickets/"+id(f.ticket.ID), f.reqToken)
	if detail.Code != http.StatusOK {
		t.Fatalf("requester detail status = %d, want 200: %.600s", detail.Code, detail.Body.String())
	}
	for _, unwanted := range []string{"detail-follow", `class="prop-followers"`, `prop-value prop-followers-none`, "data-mentions", "Type @ to mention"} {
		if body := detail.Body.String(); strings.Contains(body, unwanted) {
			t.Errorf("a requester must never receive %q, got: %.1600s", unwanted, body)
		}
	}

	for _, verb := range []string{"follow", "unfollow"} {
		rec := h.postFormAs(t, followPath(f.ticket.ID, verb), url.Values{}, f.reqToken)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s as requester = %d, want 403: %.600s", verb, rec.Code, rec.Body.String())
		}
		if following(t, h, f.ticket.ID, f.requester.ID) {
			t.Errorf("%s subscribed the requester", verb)
		}
	}
}

// TestMentionCandidatesOfferOnlyEligibleStaff proves the one candidate list
// both inputs share: only staff who can open the ticket right now, never the
// requester, never an off-desk agent, never the actor.
func TestMentionCandidatesOfferOnlyEligibleStaff(t *testing.T) {
	h := newHarness(t)
	f := seedFollows(t, h)

	detail := getAs(t, h, "/tickets/"+id(f.ticket.ID), f.agentToken)
	if detail.Code != http.StatusOK {
		t.Fatalf("detail status = %d, want 200: %.600s", detail.Code, detail.Body.String())
	}
	body := detail.Body.String()
	for _, want := range []string{
		`data-mentions-open`,
		`data-mentions`,
		`data-mention-name="` + h.admin.Name + `"`,
		`Type @ to mention someone who can open this ticket.`,
		`Only staff who can open this ticket`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the composer must offer %q, got: %.2000s", want, body)
		}
	}
	for _, unwanted := range []string{
		`data-mention-name="` + f.requester.Name + `"`,
		`data-mention-name="` + f.other.Name + `"`,
		`data-mention-name="` + f.agent.Name + `"`,
	} {
		if strings.Contains(body, unwanted) {
			t.Errorf("the candidate list must not offer %q, got: %.2000s", unwanted, body)
		}
	}
}

// TestMentionCandidateListEmptyState proves the empty candidate list reads as
// the honest empty state instead of an empty box, and the footer always says
// who may be mentioned.
func TestMentionCandidateListEmptyState(t *testing.T) {
	h := newHarness(t)
	requester := seedUserRole(t, h.store, "Carol Requester", "carol@tkt.test", domain.RoleUser)
	ticket, err := h.tickets.Create(t.Context(), *requester, application.CreateTicketInput{
		Title: "Only staff in the room", Description: "Nobody else.", CategoryID: h.bugCategory.ID, Priority: domain.PriorityMedium,
	})
	if err != nil {
		t.Fatalf("create ticket: %v", err)
	}

	body := h.get(t, "/tickets/"+id(ticket.ID), false).Body.String()
	for _, want := range []string{
		`data-mentions`,
		`Nobody who can open this ticket matches.`,
		`Only staff who can open this ticket`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the empty list must render %q, got: %.2000s", want, body)
		}
	}
}

// TestFollowersRowListsNamesAndYou proves the Properties row: the avatars and
// the count, the names behind the disclosure with "You" beside the actor, and
// the plain empty state when nobody follows.
func TestFollowersRowListsNamesAndYou(t *testing.T) {
	h := newHarness(t)
	f := seedFollows(t, h)

	empty := getAs(t, h, "/tickets/"+id(f.ticket.ID), f.agentToken).Body.String()
	if !strings.Contains(empty, `class="prop-value prop-followers-none">None<`) {
		t.Errorf("nobody following must read as None, got: %.2000s", empty)
	}

	for _, session := range []string{f.agentToken, f.adminToken} {
		if rec := hxPostAs(t, h, followPath(f.ticket.ID, "follow"), url.Values{}, session); rec.Code != http.StatusOK {
			t.Fatalf("follow for the row = %d, want 200: %.600s", rec.Code, rec.Body.String())
		}
	}

	body := getAs(t, h, "/tickets/"+id(f.ticket.ID), f.agentToken).Body.String()
	for _, want := range []string{
		`<details class="prop-followers">`,
		`<summary class="prop-row prop-followers-summary">`,
		`<span class="prop-followers-name">` + h.admin.Name + `</span>`,
		// Ordered by display name, and the actor is marked for themselves.
		`<span class="prop-followers-name">` + f.agent.Name + `</span><span class="prop-followers-you">You</span>`,
		`<span class="prop-followers-count">2</span>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the followers row must render %q, got: %.2400s", want, body)
		}
	}
	if strings.Index(body, `<span class="prop-followers-name">`+h.admin.Name) > strings.Index(body, `<span class="prop-followers-name">`+f.agent.Name) {
		t.Error("the followers list must read in display-name order")
	}
}

// TestMentionCommentNotifiesAndSubscribes is the end-to-end rule of a mention:
// the real comment route parses the @name, tells the person once, and makes
// them a follower in the same submission.
func TestMentionCommentNotifiesAndSubscribes(t *testing.T) {
	h := newHarness(t)
	f := seedFollows(t, h)

	comment := h.postMultipart(t, "/tickets/"+id(f.ticket.ID)+"/comments", url.Values{
		"body":       {"@" + h.admin.Name + " can you confirm the VPN gateway is up?"},
		"visibility": {"public"},
	}, nil, f.agentToken)
	if comment.Code != http.StatusSeeOther {
		t.Fatalf("comment status = %d, want 303: %.600s", comment.Code, comment.Body.String())
	}

	if !following(t, h, f.ticket.ID, h.admin.ID) {
		t.Error("a mention must make the mentioned person a follower")
	}
	if following(t, h, f.ticket.ID, f.agent.ID) {
		t.Error("the commentator must not be subscribed by their own mention")
	}
	panel := getAs(t, h, "/notifications", f.adminToken)
	if panel.Code != http.StatusOK {
		t.Fatalf("panel status = %d, want 200: %.600s", panel.Code, panel.Body.String())
	}
	if !strings.Contains(panel.Body.String(), "Mentioned you in a comment") {
		t.Errorf("the mentioned person must be told once, got: %.1600s", panel.Body.String())
	}
}

// TestFollowSwapKeepsTheComposer proves the whole-detail swap a follow click
// triggers keeps the composer intact: the native multipart submission and both
// preserve handles are in the swapped markup, so a typed draft survives.
func TestFollowSwapKeepsTheComposer(t *testing.T) {
	h := newHarness(t)
	f := seedFollows(t, h)

	swapped := h.postForm(t, followPath(f.ticket.ID, "follow"), url.Values{}, true).Body.String()
	for _, want := range []string{
		`class="card comment-form" method="post" action="/tickets/` + id(f.ticket.ID) + `/comments" enctype="multipart/form-data"`,
		`<textarea name="body" id="comment-body" hx-preserve="true"`,
		`<input id="comment-internal" type="checkbox" name="internal" value="1" hx-preserve="true">`,
	} {
		if !strings.Contains(swapped, want) {
			t.Errorf("the follow swap must keep %q, got: %.2400s", want, swapped)
		}
	}
}

// TestNotificationPanelRendersMentionAndAssignmentSentences proves the panel
// renders the two sentences following introduced: a follower learns who the
// ticket went to, and a mentioned colleague reads that a comment named them.
func TestNotificationPanelRendersMentionAndAssignmentSentences(t *testing.T) {
	h := newHarness(t)
	f := seedFollows(t, h)
	otherToken := f.withOtherSession(t, h)
	_, rootSession := seedRoot(t, h)
	if rec := hxPostAs(t, h, followPath(f.ticket.ID, "follow"), url.Values{}, f.adminToken); rec.Code != http.StatusOK {
		t.Fatalf("follow the ticket = %d, want 200: %.600s", rec.Code, rec.Body.String())
	}

	// Somebody else hands the ticket to the second agent, so the follower is told
	// who holds it now without being the actor of that change.
	reassign := h.postFormAs(t, "/tickets/"+id(f.ticket.ID)+"/assign", url.Values{
		"user_id": {id(f.other.ID)},
		"reason":  {"handing it over"},
	}, rootSession.ID)
	if reassign.Code != http.StatusSeeOther {
		t.Fatalf("reassign status = %d, want 303: %.600s", reassign.Code, reassign.Body.String())
	}
	// The new assignee mentions the follower in a comment.
	comment := h.postMultipart(t, "/tickets/"+id(f.ticket.ID)+"/comments", url.Values{
		"body":       {"@" + h.admin.Name + " I have the context now."},
		"visibility": {"public"},
	}, nil, otherToken)
	if comment.Code != http.StatusSeeOther {
		t.Fatalf("comment status = %d, want 303: %.600s", comment.Code, comment.Body.String())
	}

	panel := getAs(t, h, "/notifications", f.adminToken)
	if panel.Code != http.StatusOK {
		t.Fatalf("panel status = %d, want 200: %.600s", panel.Code, panel.Body.String())
	}
	body := panel.Body.String()
	for _, want := range []string{
		`Assigned to ` + f.other.Name,
		`Mentioned you in a comment`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the panel must render %q, got: %.2400s", want, body)
		}
	}
}
