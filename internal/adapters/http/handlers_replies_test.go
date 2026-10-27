package httpadapter

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// Saved replies (issue #418) at the HTTP layer: the composer's picker and the
// "Saved replies" manager screen, over the real harness (real services, real
// database, real renderer, real routes). The behaviour is owned exhaustively
// here; the browser journeys stay in Playwright.
//
// The rules these tests hold:
//   - a personal reply is private to its owner, and somebody else's answers
//     not found rather than forbidden, so the screen leaks no existence;
//   - a desk's shared replies are USED by every member of that desk and
//     MAINTAINED by an administrator, so an agent reads them read-only;
//   - a reply of a desk the actor does not belong to is never offered, and a
//     requester never sees the tool at all;
//   - a rejected text re-renders the editor with the message the instance
//     language selects, and writes nothing.

// savedRepliesFixture arranges one personal reply, one reply shared with the
// desk the agent belongs to, and one shared with a desk they do not belong to.
type savedRepliesFixture struct {
	service     *application.CannedReplyService
	deskID      int64
	otherDeskID int64
	agent       *domain.User
	agentToken  string
	requester   *domain.User
	reqToken    string
	ticket      *domain.Ticket
	mine        *domain.CannedReply
	shared      *domain.CannedReply
	other       *domain.CannedReply
}

func seedSavedReplies(t *testing.T, h *harness) savedRepliesFixture {
	t.Helper()
	catalog := application.NewCatalogService(h.store.CatalogStore(), h.store.CategoryStore(), h.clock)
	department, err := catalog.CreateDepartmentFor(t.Context(), *h.admin, "Operations", "")
	if err != nil {
		t.Fatalf("create department: %v", err)
	}
	desk, err := catalog.CreateDeskFor(t.Context(), *h.admin, department.ID, "Service desk")
	if err != nil {
		t.Fatalf("create service desk: %v", err)
	}
	otherDesk, err := catalog.CreateDeskFor(t.Context(), *h.admin, department.ID, "People desk")
	if err != nil {
		t.Fatalf("create people desk: %v", err)
	}

	agent := seedUserRole(t, h.store, "Dana Agent", "dana@tkt.test", domain.RoleAgent)
	if err := h.store.DeskStore().AddMember(t.Context(), desk.ID, agent.ID, fixedNow); err != nil {
		t.Fatalf("staff service desk: %v", err)
	}
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
	// The agent opens this ticket from their own queue, so the fixture assigns
	// it through the audited path (an unassigned ticket is not in their scope).
	h.assignTicket(t, ticket.ID, agent.ID)

	svc := application.NewCannedReplyService(h.store.CannedReplyStore(), h.store.DeskStore(), h.clock)
	mine, err := svc.CreatePersonal(t.Context(), *agent, "Looking into it",
		"Hi {requester}, thanks for reaching out about {ticket}. I am looking into it now.")
	if err != nil {
		t.Fatalf("create personal reply: %v", err)
	}
	shared, err := svc.CreateForDesk(t.Context(), *h.admin, desk.ID, "Need more details",
		"Hi {requester}, to move {ticket} forward we need a screenshot.")
	if err != nil {
		t.Fatalf("create service desk reply: %v", err)
	}
	hidden, err := svc.CreateForDesk(t.Context(), *h.admin, otherDesk.ID, "Payroll cut-off dates",
		"Hi {requester}, payroll closes on the 20th.")
	if err != nil {
		t.Fatalf("create people desk reply: %v", err)
	}
	return savedRepliesFixture{
		service:     svc,
		deskID:      desk.ID,
		otherDeskID: otherDesk.ID,
		agent:       agent,
		agentToken:  h.sessionFor(t, agent.ID),
		requester:   requester,
		reqToken:    h.sessionFor(t, requester.ID),
		ticket:      ticket,
		mine:        mine,
		shared:      shared,
		other:       hidden,
	}
}

// replyPath formats one manager route for the given ids.
func replyPath(format string, values ...int64) string {
	args := make([]any, len(values))
	for i, value := range values {
		args[i] = value
	}
	return fmt.Sprintf(format, args...)
}

// TestSavedReplyPickerOffersOnlyMemberDeskReplies proves the composer's picker
// reaches the detail page already filled, groups the actor's own replies before
// the desk they belong to, and never offers another desk's reply.
func TestSavedReplyPickerOffersOnlyMemberDeskReplies(t *testing.T) {
	h := newHarness(t)
	f := seedSavedReplies(t, h)

	rec := getAs(t, h, "/tickets/"+id(f.ticket.ID), f.agentToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("detail status = %d, want 200: %.600s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		`data-saved-replies-open`,
		`data-saved-replies`,
		`Mine`,
		`Service desk`,
		// Filled server side: the placeholders never reach the picker.
		`Hi Carol Requester, thanks for reaching out about TKT-1.`,
		`Hi Carol Requester, to move TKT-1 forward we need a screenshot.`,
		`href="/replies"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("picker must render %q, got: %.1200s", want, body)
		}
	}
	for _, unwanted := range []string{"Payroll cut-off dates", "People desk", "{requester}", "{ticket}"} {
		if strings.Contains(body, unwanted) {
			t.Errorf("picker must not render %q, got: %.1200s", unwanted, body)
		}
	}
}

// TestSavedReplyPickerEmptyStateOffersManagement proves the excluded and the
// empty cases: a requester never receives the tool, and a staff member with no
// saved replies reads the empty state and the way to create one.
func TestSavedReplyPickerEmptyStateOffersManagement(t *testing.T) {
	h := newHarness(t)
	f := seedSavedReplies(t, h)
	if err := f.service.DeletePersonal(t.Context(), *f.agent, f.mine.ID); err != nil {
		t.Fatalf("delete the agent's personal reply: %v", err)
	}
	if err := h.store.DeskStore().RemoveMember(t.Context(), f.deskID, f.agent.ID); err != nil {
		t.Fatalf("remove the agent from the service desk: %v", err)
	}

	staff := getAs(t, h, "/tickets/"+id(f.ticket.ID), f.agentToken)
	if body := staff.Body.String(); !strings.Contains(body, `No saved replies yet.`) {
		t.Errorf("an actor with no saved replies must read the empty state, got: %.1200s", body)
	}
	if body := staff.Body.String(); !strings.Contains(body, `href="/replies"`) {
		t.Errorf("the empty state must still offer the manager screen, got: %.1200s", body)
	}

	requester := getAs(t, h, "/tickets/"+id(f.ticket.ID), f.reqToken)
	if requester.Code != http.StatusOK {
		t.Fatalf("requester detail status = %d, want 200", requester.Code)
	}
	if body := requester.Body.String(); strings.Contains(body, "data-saved-replies") {
		t.Errorf("a requester must never receive the saved-reply picker, got: %.1200s", body)
	}
}

// TestSavedReplyRefusalsFollowTheInstanceLanguage proves the two authorization
// refusals read in the instance language, so a Spanish operator never meets an
// English sentence on the new screen.
func TestSavedReplyRefusalsFollowTheInstanceLanguage(t *testing.T) {
	h := newHarness(t)
	f := seedSavedReplies(t, h)
	if err := h.settings.SetInterfaceLanguage(t.Context(), *h.admin, application.InterfaceLanguageSpanish); err != nil {
		t.Fatalf("set interface language: %v", err)
	}

	requester := h.postFormAs(t, "/replies", url.Values{"title": {"Mía"}, "body": {"Texto"}}, f.reqToken)
	if body := requester.Body.String(); !strings.Contains(body, "las respuestas guardadas solo están disponibles para el personal") {
		t.Errorf("the staff-only refusal must read in Spanish, got: %.400s", body)
	}

	agent := h.postFormAs(t, replyPath("/replies/desks/%d/replies/%d/edit", f.deskID, f.shared.ID),
		url.Values{"title": {"Cambiada"}, "body": {"Cambiada"}}, f.agentToken)
	if body := agent.Body.String(); !strings.Contains(body, "solo un administrador puede cambiar las respuestas guardadas de una mesa") {
		t.Errorf("the desk-management refusal must read in Spanish, got: %.400s", body)
	}
}

// TestRequesterIsRefusedOnEverySavedReplyRoute proves the tool is staff-only on
// the routes, not only in the markup: a requester reads a 403 on the screen and
// on every mutation, and every refusal carries the catalogue copy.
func TestRequesterIsRefusedOnEverySavedReplyRoute(t *testing.T) {
	h := newHarness(t)
	f := seedSavedReplies(t, h)

	if rec := getAs(t, h, "/replies", f.reqToken); rec.Code != http.StatusForbidden {
		t.Errorf("GET /replies as a requester = %d, want 403", rec.Code)
	}
	// The rail entry is the screen's other door, and it is gated the same way.
	staff := getAs(t, h, "/tickets/"+id(f.ticket.ID), f.agentToken)
	if !strings.Contains(staff.Body.String(), `href="/replies"`) {
		t.Error("a staff member must reach the manager from the rail")
	}
	requester := getAs(t, h, "/tickets/"+id(f.ticket.ID), f.reqToken)
	if strings.Contains(requester.Body.String(), `href="/replies"`) {
		t.Error("a requester must not reach the manager from the rail")
	}
	for _, tc := range []struct {
		name string
		path string
		form url.Values
	}{
		{"create personal", "/replies", url.Values{"title": {"Mine"}, "body": {"Text"}}},
		{"edit personal", replyPath("/replies/%d/edit", f.mine.ID), url.Values{"title": {"Mine"}, "body": {"Text"}}},
		{"delete personal", replyPath("/replies/%d/delete", f.mine.ID), url.Values{}},
		{"create desk", replyPath("/replies/desks/%d/replies", f.deskID), url.Values{"title": {"Shared"}, "body": {"Text"}}},
		{"edit desk", replyPath("/replies/desks/%d/replies/%d/edit", f.deskID, f.shared.ID), url.Values{"title": {"Shared"}, "body": {"Text"}}},
		{"delete desk", replyPath("/replies/desks/%d/replies/%d/delete", f.deskID, f.shared.ID), url.Values{}},
	} {
		rec := h.postFormAs(t, tc.path, tc.form, f.reqToken)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s as a requester = %d, want 403 (body %.400s)", tc.name, rec.Code, rec.Body.String())
		}
		// One copy for the whole tool, whichever gate refuses first: the
		// catalogue sentence, never a bare status.
		if body := rec.Body.String(); !strings.Contains(body, "saved replies are only available to staff") {
			t.Errorf("%s must carry the staff-only copy, got: %.400s", tc.name, body)
		}
	}

	// Nothing was written or removed by any refusal.
	if stored, err := h.store.CannedReplyStore().GetByID(t.Context(), f.shared.ID); err != nil {
		t.Fatalf("read the desk reply after the refusals: %v", err)
	} else if stored.Title != "Need more details" {
		t.Errorf("a refused request changed the desk reply: %q", stored.Title)
	}
}

// TestAgentReadsDeskRepliesReadOnlyAndAdminMaintainsThem proves the split rule:
// the agent sees the desk's shared replies with the read-only notice and the
// desk-management routes refuse them, while an administrator edits the same
// reply from the same screen.
func TestAgentReadsDeskRepliesReadOnlyAndAdminMaintainsThem(t *testing.T) {
	h := newHarness(t)
	f := seedSavedReplies(t, h)
	deskTab := "/replies?desk=" + id(f.deskID)

	agentTab := getAs(t, h, deskTab, f.agentToken)
	if agentTab.Code != http.StatusOK {
		t.Fatalf("agent desk tab = %d, want 200: %.600s", agentTab.Code, agentTab.Body.String())
	}
	agentBody := agentTab.Body.String()
	for _, want := range []string{
		"Need more details",
		"Read only",
		"Shared with everyone on Service desk. Only an administrator can change them.",
	} {
		if !strings.Contains(agentBody, want) {
			t.Errorf("agent desk tab must render %q, got: %.1200s", want, agentBody)
		}
	}
	for _, unwanted := range []string{
		`saved-replies-new`,
		`&amp;edit=` + strconv.FormatInt(f.shared.ID, 10),
		replyPath(`/replies/desks/%d/replies/%d/delete`, f.deskID, f.shared.ID),
	} {
		if strings.Contains(agentBody, unwanted) {
			t.Errorf("agent desk tab must not offer %q, got: %.1200s", unwanted, agentBody)
		}
	}

	refused := h.postFormAs(t, replyPath("/replies/desks/%d/replies/%d/edit", f.deskID, f.shared.ID),
		url.Values{"title": {"Rewritten"}, "body": {"Rewritten"}}, f.agentToken)
	if refused.Code != http.StatusForbidden {
		t.Fatalf("agent desk edit = %d, want 403 (body %.400s)", refused.Code, refused.Body.String())
	}
	if body := refused.Body.String(); !strings.Contains(body, "only an administrator can change a desk") {
		t.Errorf("agent desk edit must carry the catalogue copy, got: %.400s", body)
	}
	if stored, err := h.store.CannedReplyStore().GetByID(t.Context(), f.shared.ID); err != nil {
		t.Fatalf("read the desk reply after the refusal: %v", err)
	} else if stored.Title != "Need more details" {
		t.Errorf("a refused desk edit changed the reply: %q", stored.Title)
	}

	adminTab := h.get(t, deskTab, false)
	adminBody := adminTab.Body.String()
	for _, want := range []string{
		`saved-replies-new`,
		`edit=` + strconv.FormatInt(f.shared.ID, 10),
		replyPath(`/replies/desks/%d/replies/%d/delete`, f.deskID, f.shared.ID),
		"Everyone on Service desk can insert these replies.",
	} {
		if !strings.Contains(adminBody, want) {
			t.Errorf("admin desk tab must render %q, got: %.1200s", want, adminBody)
		}
	}
	// Opening the editor from the desk tab posts back to the desk route, and it
	// opens on the reply the tab carries.
	adminEditor := h.get(t, deskTab+"&edit="+id(f.shared.ID), false)
	if adminEditor.Code != http.StatusOK {
		t.Fatalf("admin desk editor = %d, want 200: %.400s", adminEditor.Code, adminEditor.Body.String())
	}
	for _, want := range []string{
		`action="` + replyPath("/replies/desks/%d/replies/%d/edit", f.deskID, f.shared.ID) + `"`,
		`id="saved-reply-title" name="title" value="Need more details"`,
	} {
		if !strings.Contains(adminEditor.Body.String(), want) {
			t.Errorf("admin desk editor must render %q, got: %.1200s", want, adminEditor.Body.String())
		}
	}

	edited := h.postForm(t, replyPath("/replies/desks/%d/replies/%d/edit", f.deskID, f.shared.ID),
		url.Values{"title": {"Need a screenshot"}, "body": {"Hi {requester}, send us a screenshot of {ticket}."}}, false)
	if edited.Code != http.StatusSeeOther {
		t.Fatalf("admin desk edit = %d, want 303 (body %.400s)", edited.Code, edited.Body.String())
	}
	if location := edited.Header().Get("Location"); location != deskTab {
		t.Errorf("admin desk edit redirected to %q, want %q", location, deskTab)
	}
	if stored, err := h.store.CannedReplyStore().GetByID(t.Context(), f.shared.ID); err != nil {
		t.Fatalf("read the edited desk reply: %v", err)
	} else if stored.Title != "Need a screenshot" {
		t.Errorf("admin desk edit stored %q, want the submitted title", stored.Title)
	}

	// An agent asking for a desk they do not belong to is refused with the same
	// not-found answer a reply they cannot see gets: the tab is not theirs.
	if rec := getAs(t, h, "/replies?desk="+id(f.otherDeskID), f.agentToken); rec.Code != http.StatusNotFound {
		t.Errorf("agent asking for another desk's tab = %d, want 404", rec.Code)
	}
}

// TestPersonalSavedReplyOfAnotherUserIsNotFound proves a personal reply is
// private: editing or deleting somebody else's answers not found (never
// forbidden, so the refusal reveals no existence) and changes nothing.
func TestPersonalSavedReplyOfAnotherUserIsNotFound(t *testing.T) {
	h := newHarness(t)
	f := seedSavedReplies(t, h)

	for _, tc := range []struct {
		name string
		path string
		form url.Values
	}{
		{"edit", replyPath("/replies/%d/edit", f.mine.ID), url.Values{"title": {"Hijacked"}, "body": {"Hijacked"}}},
		{"delete", replyPath("/replies/%d/delete", f.mine.ID), url.Values{}},
	} {
		// The harness admin is not the owner of the agent's personal reply.
		rec := h.postForm(t, tc.path, tc.form, false)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s another user's personal reply = %d, want 404 (body %.400s)", tc.name, rec.Code, rec.Body.String())
		}
		if body := rec.Body.String(); !strings.Contains(body, "canned reply not found") {
			t.Errorf("%s must answer the not-found copy, got: %.400s", tc.name, body)
		}
	}

	if stored, err := h.store.CannedReplyStore().GetByID(t.Context(), f.mine.ID); err != nil {
		t.Fatalf("read the agent's personal reply: %v", err)
	} else if stored.Title != "Looking into it" || stored.OwnerUserID == nil || *stored.OwnerUserID != f.agent.ID {
		t.Errorf("a refused personal mutation changed the reply: %+v", stored)
	}
}

// TestPersonalSavedRepliesRoundTrip proves an actor's own replies are their own
// to create, open, edit and delete from the first tab, and that the screen keeps
// answering after each step.
func TestPersonalSavedRepliesRoundTrip(t *testing.T) {
	h := newHarness(t)
	f := seedSavedReplies(t, h)

	created := h.postFormAs(t, "/replies", url.Values{"title": {"Closing, no response"}, "body": {"Hi {requester}, we are resolving {ticket}."}}, f.agentToken)
	if created.Code != http.StatusSeeOther {
		t.Fatalf("create = %d, want 303 (body %.400s)", created.Code, created.Body.String())
	}
	if location := created.Header().Get("Location"); location != "/replies" {
		t.Errorf("create redirected to %q, want the first tab", location)
	}
	tab := getAs(t, h, "/replies", f.agentToken)
	if body := tab.Body.String(); !strings.Contains(body, "Closing, no response") {
		t.Errorf("the first tab must list the reply just created, got: %.1200s", body)
	}

	editor := getAs(t, h, "/replies?edit="+id(f.mine.ID), f.agentToken)
	if editor.Code != http.StatusOK {
		t.Fatalf("open editor = %d, want 200: %.400s", editor.Code, editor.Body.String())
	}
	for _, want := range []string{
		`action="` + replyPath("/replies/%d/edit", f.mine.ID) + `"`,
		`id="saved-reply-title" name="title" value="Looking into it"`,
		// The manager edits the text as authored: the placeholders stay.
		`Hi {requester}, thanks for reaching out about {ticket}. I am looking into it now.`,
	} {
		if !strings.Contains(editor.Body.String(), want) {
			t.Errorf("the editor must render %q, got: %.1200s", want, editor.Body.String())
		}
	}

	edited := h.postFormAs(t, replyPath("/replies/%d/edit", f.mine.ID),
		url.Values{"title": {"Still looking"}, "body": {"Hi {requester}, still on {ticket}."}}, f.agentToken)
	if edited.Code != http.StatusSeeOther {
		t.Fatalf("edit = %d, want 303 (body %.400s)", edited.Code, edited.Body.String())
	}
	if stored, err := h.store.CannedReplyStore().GetByID(t.Context(), f.mine.ID); err != nil {
		t.Fatalf("read the edited reply: %v", err)
	} else if stored.Title != "Still looking" || stored.OwnerUserID == nil || *stored.OwnerUserID != f.agent.ID {
		t.Errorf("the edit stored %+v, want the submitted title on the same owner", stored)
	}

	deleted := h.postFormAs(t, replyPath("/replies/%d/delete", f.mine.ID), url.Values{}, f.agentToken)
	if deleted.Code != http.StatusSeeOther {
		t.Fatalf("delete = %d, want 303 (body %.400s)", deleted.Code, deleted.Body.String())
	}
	if _, err := h.store.CannedReplyStore().GetByID(t.Context(), f.mine.ID); err == nil {
		t.Error("the reply still exists after its own delete")
	}
}

// TestSavedReplyValidationReRendersTheEditor proves a rejected text re-renders
// the open editor with the typed values, the localized message, and no write —
// in both shipped languages.
func TestSavedReplyValidationReRendersTheEditor(t *testing.T) {
	for _, tc := range []struct {
		name     string
		language string
		want     string
	}{
		{"english", application.InterfaceLanguageEnglish, "the title must be from 1 to 120 characters"},
		{"spanish", application.InterfaceLanguageSpanish, "el título debe tener entre 1 y 120 caracteres"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			seedSavedReplies(t, h)
			if err := h.settings.SetInterfaceLanguage(t.Context(), *h.admin, tc.language); err != nil {
				t.Fatalf("set interface language: %v", err)
			}

			rec := h.postForm(t, "/replies", url.Values{"title": {"  "}, "body": {"Hi Carol, we are on it."}}, false)
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("rejected title = %d, want 422 (body %.600s)", rec.Code, rec.Body.String())
			}
			body := rec.Body.String()
			if !strings.Contains(body, tc.want) {
				t.Errorf("the re-render must carry %q, got: %.1200s", tc.want, body)
			}
			if !strings.Contains(body, "Hi Carol, we are on it.") {
				t.Errorf("the re-render must keep the typed reply text, got: %.1200s", body)
			}
			if !strings.Contains(body, `class="card saved-replies-editor" method="post" action="/replies"`) {
				t.Errorf("the re-render must reopen the personal editor on its own route, got: %.1200s", body)
			}
			if replies, err := h.store.CannedReplyStore().ListPersonal(t.Context(), h.admin.ID); err != nil {
				t.Fatalf("list the admin's replies: %v", err)
			} else if len(replies) != 0 {
				t.Errorf("a rejected submission wrote %d replies, want 0", len(replies))
			}
		})
	}
}

// TestCommentFormKeepsItsNativeSubmission proves the composer is unchanged by
// the picker: the form still posts natively (method, action, multipart) and the
// draft and the internal toggle keep their hx-preserve handles, which is what
// keeps a typed comment across a swap.
func TestCommentFormKeepsItsNativeSubmission(t *testing.T) {
	h := newHarness(t)
	f := seedSavedReplies(t, h)

	body := h.get(t, "/tickets/"+id(f.ticket.ID), false).Body.String()
	for _, want := range []string{
		`class="card comment-form" method="post" action="/tickets/` + id(f.ticket.ID) + `/comments" enctype="multipart/form-data"`,
		`<textarea name="body" id="comment-body" hx-preserve="true"`,
		`<input id="comment-internal" type="checkbox" name="internal" value="1" hx-preserve="true">`,
		`<button class="btn primary comment-submit" type="submit">`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the composer must keep %q, got: %.1600s", want, body)
		}
	}
}
