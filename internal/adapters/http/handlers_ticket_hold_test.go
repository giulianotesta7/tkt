package httpadapter

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
	"github.com/giulianotesta7/tkt/internal/i18n"
)

// This file drives the ticket on-hold surface (issue #352, S2/S3/S5/S6/S8)
// through the real HTTP harness: the two mutation routes, the staff and
// requester detail controls, the on-hold chip in the lists, the paused SLA
// milestone and the timeline narrative.

// createTicketFor files a ticket AS the given actor through the real create
// path, so the requester attribution is the same one production writes.
func createTicketFor(t *testing.T, h *harness, actor *domain.User, title string) *domain.Ticket {
	t.Helper()
	tkt, err := h.tickets.Create(context.Background(), *actor, application.CreateTicketInput{
		Title:       title,
		Description: "Test description",
		CategoryID:  h.bugCategory.ID,
		Priority:    domain.PriorityMedium,
	})
	if err != nil {
		t.Fatalf("create ticket %q for %s: %v", title, actor.Email, err)
	}
	return tkt
}

// holdViaService puts the ticket on hold through the audited use case, the
// arrangement every read-surface test needs.
func holdViaService(t *testing.T, h *harness, id int64, reason string) {
	t.Helper()
	if _, err := h.tickets.PutOnHold(context.Background(), *h.admin, id, reason); err != nil {
		t.Fatalf("put ticket %d on hold: %v", id, err)
	}
}

// assertHoldColumns reads the two hold columns straight from the row: they are
// written by exactly one statement (the compare-and-set hold update), so a
// rejected request must leave both untouched.
func assertHoldColumns(t *testing.T, h *harness, id int64, wantOnHold bool, wantReason string) {
	t.Helper()
	db := h.rawDB(t)
	var since sql.NullString
	if err := db.QueryRow(`SELECT on_hold_since FROM tickets WHERE id = ?`, id).Scan(&since); err != nil {
		t.Fatalf("read on_hold_since of ticket %d: %v", id, err)
	}
	if since.Valid != wantOnHold {
		t.Errorf("on_hold_since valid = %t (%q), want %t", since.Valid, since.String, wantOnHold)
	}
	if got := scanOneString(t, db, `SELECT on_hold_reason FROM tickets WHERE id = ?`, id); got != wantReason {
		t.Errorf("on_hold_reason = %q, want %q", got, wantReason)
	}
}

// ticketPath is the detail path of one seeded ticket.
func ticketPath(id int64) string { return "/tickets/" + strconv.FormatInt(id, 10) }

// TestTicketHoldRoutePutsTheTicketOnHold proves POST /tickets/{id}/hold records
// the pause with its mandatory reason and redirects to the detail page.
func TestTicketHoldRoutePutsTheTicketOnHold(t *testing.T) {
	h := newHarness(t)
	tkt := h.seedTicket(t, "Login page down", nil)

	rec := h.postForm(t, ticketPath(tkt.ID)+"/hold", url.Values{"reason": {"waiting for the VPN logs"}}, false)

	wantRedirect(t, rec, http.StatusSeeOther, ticketPath(tkt.ID))
	view, err := h.tickets.GetByID(t.Context(), *h.admin, tkt.ID)
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	if !view.Ticket.IsOnHold() {
		t.Fatal("the ticket must be on hold after the hold route")
	}
	if view.Ticket.OnHoldReason != "waiting for the VPN logs" {
		t.Errorf("on_hold_reason = %q, want %q", view.Ticket.OnHoldReason, "waiting for the VPN logs")
	}
	last := view.AuditEvents[len(view.AuditEvents)-1]
	if last.Action != domain.ActionHold || last.Actor != h.admin.Name {
		t.Errorf("hold event = %+v, want action=hold actor=%q", last, h.admin.Name)
	}
	if last.Note == nil || *last.Note != "waiting for the VPN logs" {
		t.Errorf("hold event note = %v, want the pause reason", last.Note)
	}
	assertHoldColumns(t, h, tkt.ID, true, "waiting for the VPN logs")
}

// TestTicketHoldRouteRejectsABlankReason proves a missing or whitespace-only
// reason is refused 422 with the catalogued copy and writes NOTHING: no hold
// column, no audit row (issue #352 S3: the reason is mandatory).
func TestTicketHoldRouteRejectsABlankReason(t *testing.T) {
	for _, tc := range []struct {
		name string
		form url.Values
	}{
		{name: "missing", form: url.Values{}},
		{name: "whitespace", form: url.Values{"reason": {"   "}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			tkt := h.seedTicket(t, "Login page down", nil)
			before := auditCount(t, h, tkt.ID)

			rec := h.postForm(t, ticketPath(tkt.ID)+"/hold", tc.form, false)

			if rec.Code != http.StatusUnprocessableEntity {
				t.Errorf("status = %d, want 422", rec.Code)
			}
			body := rec.Body.String()
			if !strings.Contains(body, domain.ErrMsgHoldReasonRequired) {
				t.Errorf("re-render must show %q, got: %s", domain.ErrMsgHoldReasonRequired, body)
			}
			// The refusal reopens the hold dialog with the error INSIDE it, so
			// the message is not hidden behind the modal.
			for _, want := range []string{
				`<dialog id="ticket-hold-dialog" aria-labelledby="ticket-hold-dialog-title" aria-describedby="ticket-hold-dialog-copy" data-open-on-load>`,
				`<p class="hold-dialog-error" role="alert">` + domain.ErrMsgHoldReasonRequired + `</p>`,
			} {
				if !strings.Contains(body, want) {
					t.Errorf("re-render must contain %q, got: %s", want, body)
				}
			}
			assertHoldColumns(t, h, tkt.ID, false, "")
			if after := auditCount(t, h, tkt.ID); after != before {
				t.Errorf("audit rows = %d, want %d (a rejected hold writes nothing)", after, before)
			}
		})
	}
}

// TestTicketHoldRouteRejectsATicketAlreadyOnHold proves the second hold of the
// same ticket is refused 422 with the catalogued copy, keeps the original pause
// instant, and appends no audit row.
func TestTicketHoldRouteRejectsATicketAlreadyOnHold(t *testing.T) {
	h := newHarness(t)
	tkt := h.seedTicket(t, "Login page down", nil)
	holdViaService(t, h, tkt.ID, "first pause")
	before := auditCount(t, h, tkt.ID)
	original := *mustTicket(t, h, tkt.ID).OnHoldSince

	rec := h.postForm(t, ticketPath(tkt.ID)+"/hold", url.Values{"reason": {"second pause"}}, false)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), domain.ErrMsgTicketAlreadyOnHold) {
		t.Errorf("re-render must show %q, got: %s", domain.ErrMsgTicketAlreadyOnHold, rec.Body.String())
	}
	got := mustTicket(t, h, tkt.ID)
	if got.OnHoldSince == nil || !got.OnHoldSince.Equal(original) {
		t.Errorf("on_hold_since = %v, want the original pause instant %v", got.OnHoldSince, original)
	}
	if got.OnHoldReason != "first pause" {
		t.Errorf("on_hold_reason = %q, want the original %q", got.OnHoldReason, "first pause")
	}
	if after := auditCount(t, h, tkt.ID); after != before {
		t.Errorf("audit rows = %d, want %d (a rejected hold writes nothing)", after, before)
	}
}

// TestTicketResumeRouteResumesTheHold proves POST /tickets/{id}/resume ends the
// hold and records the resume event.
func TestTicketResumeRouteResumesTheHold(t *testing.T) {
	h := newHarness(t)
	tkt := h.seedTicket(t, "Login page down", nil)
	holdViaService(t, h, tkt.ID, "waiting for the VPN logs")

	rec := h.postForm(t, ticketPath(tkt.ID)+"/resume", url.Values{}, false)

	wantRedirect(t, rec, http.StatusSeeOther, ticketPath(tkt.ID))
	view, err := h.tickets.GetByID(t.Context(), *h.admin, tkt.ID)
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	if view.Ticket.IsOnHold() {
		t.Fatal("the ticket must be off hold after the resume route")
	}
	last := view.AuditEvents[len(view.AuditEvents)-1]
	if last.Action != domain.ActionResume || last.Actor != h.admin.Name {
		t.Errorf("resume event = %+v, want action=resume actor=%q", last, h.admin.Name)
	}
	assertHoldColumns(t, h, tkt.ID, false, "")
}

// TestTicketResumeRouteRejectsATicketNotOnHold proves resuming a ticket that is
// not on hold is refused 422 with the catalogued copy and writes nothing.
func TestTicketResumeRouteRejectsATicketNotOnHold(t *testing.T) {
	h := newHarness(t)
	tkt := h.seedTicket(t, "Login page down", nil)
	before := auditCount(t, h, tkt.ID)

	rec := h.postForm(t, ticketPath(tkt.ID)+"/resume", url.Values{}, false)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), domain.ErrMsgTicketNotOnHold) {
		t.Errorf("re-render must show %q, got: %s", domain.ErrMsgTicketNotOnHold, rec.Body.String())
	}
	assertHoldColumns(t, h, tkt.ID, false, "")
	if after := auditCount(t, h, tkt.ID); after != before {
		t.Errorf("audit rows = %d, want %d (a rejected resume writes nothing)", after, before)
	}
}

// TestTicketHoldRouteIsForbiddenForTheRequester proves a role user cannot hold
// their own ticket: 403 with the catalogued copy and nothing written.
func TestTicketHoldRouteIsForbiddenForTheRequester(t *testing.T) {
	h := newHarness(t)
	requester := seedUserRole(t, h.store, "Rosa", "rosa-hold@tkt.test", domain.RoleUser)
	sess := seedSession(t, h.store, requester.ID)
	tkt := createTicketFor(t, h, requester, "Printer jam")
	before := auditCount(t, h, tkt.ID)

	rec := h.postFormAs(t, ticketPath(tkt.ID)+"/hold", url.Values{"reason": {"hola"}}, sess.ID)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), domain.ErrMsgUserCannotEdit) {
		t.Errorf("re-render must show %q, got: %s", domain.ErrMsgUserCannotEdit, rec.Body.String())
	}
	assertHoldColumns(t, h, tkt.ID, false, "")
	if after := auditCount(t, h, tkt.ID); after != before {
		t.Errorf("audit rows = %d, want %d (a forbidden hold writes nothing)", after, before)
	}
}

// TestTicketHoldRouteIsNotFoundOutOfScope proves an out-of-scope requester gets
// the same 404 the transition route returns, with nothing written.
func TestTicketHoldRouteIsNotFoundOutOfScope(t *testing.T) {
	h := newHarness(t)
	other := seedUserRole(t, h.store, "Beto", "beto-hold@tkt.test", domain.RoleUser)
	sess := seedSession(t, h.store, other.ID)
	tkt := createTicketFor(t, h, h.admin, "Someone else's ticket")
	before := auditCount(t, h, tkt.ID)

	rec := h.postFormAs(t, ticketPath(tkt.ID)+"/hold", url.Values{"reason": {"hola"}}, sess.ID)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
	assertHoldColumns(t, h, tkt.ID, false, "")
	if after := auditCount(t, h, tkt.ID); after != before {
		t.Errorf("audit rows = %d, want %d (an out-of-scope hold writes nothing)", after, before)
	}
}

// TestTicketHoldRouteHXFragment proves the HTMX hold path answers with the
// detail fragment of the freshly held ticket.
func TestTicketHoldRouteHXFragment(t *testing.T) {
	h := newHarness(t)
	tkt := h.seedTicket(t, "Login page down", nil)

	rec := h.postForm(t, ticketPath(tkt.ID)+"/hold", url.Values{"reason": {"waiting for the VPN logs"}}, true)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "<!DOCTYPE html>") {
		t.Errorf("HX hold must return the fragment, got: %s", body)
	}
	if !strings.Contains(body, `action="`+ticketPath(tkt.ID)+`/resume"`) {
		t.Errorf("the fragment of a held ticket must offer Resume, got: %s", body)
	}
}

// holdConflictUoW is an operational-failure seam: UpdateHold refuses with the
// store-contract conflict, so the 409 mapping is proved without a race the
// single-threaded harness cannot produce. Every other method falls through to
// the nil embedded interface, which no hold request reaches.
type holdConflictUoW struct{ application.TicketUnitOfWork }

func (holdConflictUoW) UpdateHold(context.Context, application.HoldWrite) error {
	return &domain.HoldConflictError{TicketID: 1}
}

// newHoldConflictMux wires the ticket routes over a ticket service whose hold
// write always loses the compare-and-set race.
func newHoldConflictMux(h *harness) *http.ServeMux {
	builder := application.NewViewBuilder(h.store.TicketStore(), h.store.UserStore(), h.store.CategoryStore(), h.store.CommentStore(), h.store.AuditStore(), h.store.DeskStore(), h.store.WorkflowResponseStore())
	svc := application.NewTicketService(h.store.TicketStore(), h.store.UserStore(), h.store.CategoryStore(), holdConflictUoW{}, builder, h.clock)
	mux := http.NewServeMux()
	NewTicketHandlers(svc, h.comments, h.search, h.categories, h.users, h.store.DeskStore(), h.workflows, application.NewWorkflowRunner(h.clock), h.store.WorkflowRunStore(), h.store.WorkflowUnitOfWork(), h.renderer).Register(mux)
	return mux
}

// postHoldConflict posts the hold form through the conflict seam.
func postHoldConflict(t *testing.T, h *harness, mux *http.ServeMux, id int64, reason string) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{"reason": {reason}}
	req := httptest.NewRequest(http.MethodPost, ticketPath(id)+"/hold", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Cookie", sessionCookie+"="+h.adminSession.ID)
	rec := httptest.NewRecorder()
	h.mw.Wrap(mux).ServeHTTP(rec, req)
	return rec
}

// TestTicketHoldConflictMapsTo409 proves a lost hold race answers 409 with the
// catalogued copy instead of the generic 500, keeps the reason the agent typed
// in the form, and follows the instance language.
func TestTicketHoldConflictMapsTo409(t *testing.T) {
	h := newHarness(t)
	tkt := h.seedTicket(t, "Login page down", nil)
	mux := newHoldConflictMux(h)

	rec := postHoldConflict(t, h, mux, tkt.ID, "waiting for the VPN logs")

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	// The template escapes the apostrophe in the catalogue copy (' → &#39;), so
	// the assertion reads the sentence without it.
	if !strings.Contains(body, "hold changed meanwhile") {
		t.Errorf("response must show the %q copy, got: %s", domain.ErrMsgHoldConflict, body)
	}
	if strings.Contains(body, domain.ErrMsgClosedTicketReadOnly) || strings.Contains(body, "Internal server error") {
		t.Errorf("409 must not fall through to another error body, got: %s", body)
	}
	if !strings.Contains(body, `id="hold-reason"`) {
		t.Errorf("the refused hold must re-render the reason form, got: %s", body)
	}
	if !strings.Contains(body, ">waiting for the VPN logs</textarea>") {
		t.Errorf("the typed reason must survive the 409 re-render, got: %s", body)
	}

	if err := h.settings.SetInterfaceLanguage(t.Context(), *h.admin, application.InterfaceLanguageSpanish); err != nil {
		t.Fatalf("set interface language: %v", err)
	}
	es := postHoldConflict(t, h, mux, tkt.ID, "esperando los registros")
	if es.Code != http.StatusConflict {
		t.Fatalf("es status = %d, want 409", es.Code)
	}
	if !strings.Contains(es.Body.String(), "la espera del ticket cambió mientras tanto") {
		t.Errorf("es response must render the Spanish 409 copy, got: %s", es.Body.String())
	}
}

// TestTicketDetailStaffHoldForm proves an open ticket offers the agent a hold
// action whose reason is REQUIRED and whose label says the requester reads it
// (issue #352 S8).
func TestTicketDetailStaffHoldForm(t *testing.T) {
	h := newHarness(t)
	tkt := h.seedTicket(t, "Login page down", nil)
	h.seedTransition(t, tkt.ID, domain.StateInProgress, "")

	body := h.get(t, ticketPath(tkt.ID), false).Body.String()

	for _, want := range []string{
		`action="` + ticketPath(tkt.ID) + `/hold"`,
		`hx-post="` + ticketPath(tkt.ID) + `/hold"`,
		`id="hold-reason"`,
		`name="reason"`,
		`required`,
		`Reason (the requester will read it)`,
		`Put on hold`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("staff detail must contain %q, got: %s", want, body)
		}
	}
	if strings.Contains(body, `/resume"`) {
		t.Errorf("a ticket that is not on hold must not offer Resume, got: %s", body)
	}
	if strings.Contains(body, "On hold since") {
		t.Errorf("a ticket that is not on hold must not render the hold banner, got: %s", body)
	}

	// The maintainer asked for a small dialog opened from the SLA section,
	// not a permanent card: the trigger sits in the SLA section and the form
	// lives inside the dialog.
	assertHoldTriggerInSLASection(t, body)
	dialogStart := strings.Index(body, `<dialog id="ticket-hold-dialog"`)
	form := strings.Index(body, `action="`+ticketPath(tkt.ID)+`/hold"`)
	if dialogStart < 0 || form < dialogStart || form > dialogStart+strings.Index(body[dialogStart:], `</dialog>`) {
		t.Errorf("the hold form must live inside #ticket-hold-dialog, got: %s", body)
	}
	if strings.Contains(body, `aria-labelledby="hold-form-title"`) {
		t.Error("the permanent hold card must be gone")
	}
}

// assertHoldTriggerInSLASection proves the "Put on hold" button renders inside
// the SLA property section, after its heading.
func assertHoldTriggerInSLASection(t *testing.T, body string) {
	t.Helper()
	heading := strings.Index(body, `<div class="prop-heading">SLA</div>`)
	trigger := strings.Index(body, `data-hold-open`)
	if heading < 0 || trigger < heading {
		t.Fatalf("the hold trigger must follow the SLA heading (heading=%d trigger=%d), got: %s", heading, trigger, body)
	}
	if next := strings.Index(body[heading:], `<div class="prop-section"`); next >= 0 && trigger > heading+next {
		t.Errorf("the hold trigger must stay inside the SLA section, got: %s", body)
	}
}

// TestTicketDetailHoldTriggerWithFrozenSLA proves the trigger also renders in
// the SLA section when the ticket has a frozen commitment and milestones.
func TestTicketDetailHoldTriggerWithFrozenSLA(t *testing.T) {
	h := newHarness(t)
	form := slaPanelForm("80")
	form.Set("sla_enabled", "1")
	if rec := h.postForm(t, "/settings/sla", form, false); rec.Code != http.StatusSeeOther {
		t.Fatalf("enable SLA: status = %d", rec.Code)
	}
	tkt := h.seedTicket(t, "With SLA", nil)
	body := h.get(t, ticketPath(tkt.ID), false).Body.String()
	if !strings.Contains(body, `data-sla-countdown`) {
		t.Fatalf("the fixture must render SLA milestones, got: %s", body)
	}
	assertHoldTriggerInSLASection(t, body)
}

// TestTicketDetailStaffOnHoldBanner proves a held ticket shows the agent when
// the hold started, the reason, the Resume control, and the on-hold chip, and
// no longer offers the hold form.
func TestTicketDetailStaffOnHoldBanner(t *testing.T) {
	h := newHarness(t)
	tkt := h.seedTicket(t, "Login page down", nil)
	h.seedTransition(t, tkt.ID, domain.StateInProgress, "")
	holdViaService(t, h, tkt.ID, "waiting for the VPN logs")
	held := mustTicket(t, h, tkt.ID)

	body := h.get(t, ticketPath(tkt.ID), false).Body.String()

	for _, want := range []string{
		`class="sla-hold-status"`,
		`On hold since`,
		// The pause instant is a semantic <time> carrying the LOCALIZED date.
		`datetime="` + formatDatetime(*held.OnHoldSince) + `"`,
		`>` + formatMonthDayYear(i18n.New(i18n.English), *held.OnHoldSince) + `</time>`,
		`waiting for the VPN logs`,
		`action="` + ticketPath(tkt.ID) + `/resume"`,
		`hx-post="` + ticketPath(tkt.ID) + `/resume"`,
		`Resume`,
		`<span class="chip">On hold</span>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("staff detail of a held ticket must contain %q, got: %s", want, body)
		}
	}
	for _, absent := range []string{`id="hold-reason"`, `action="` + ticketPath(tkt.ID) + `/hold"`} {
		if strings.Contains(body, absent) {
			t.Errorf("a held ticket must not offer the hold form (%q), got: %s", absent, body)
		}
	}
	// The maintainer moved the staff notice into the SLA section: no banner
	// above the ticket, and the notice and Resume sit after the SLA heading.
	if strings.Contains(body, `class="hold-banner"`) {
		t.Errorf("staff must not get the top banner any more, got: %s", body)
	}
	heading := strings.Index(body, `<div class="prop-heading">SLA</div>`)
	notice := strings.Index(body, `class="sla-hold-status"`)
	if heading < 0 || notice < heading {
		t.Errorf("the on-hold notice must live in the SLA section (heading=%d notice=%d)", heading, notice)
	}
}

// TestTicketDetailRequesterOnHoldBanner proves the requester's own held ticket
// reads "This ticket is waiting for your reply" with the reason (issue #352
// S8) and offers the requester no hold or resume control.
func TestTicketDetailRequesterOnHoldBanner(t *testing.T) {
	h := newHarness(t)
	requester := seedUserRole(t, h.store, "Rosa", "rosa-banner@tkt.test", domain.RoleUser)
	sess := seedSession(t, h.store, requester.ID)
	tkt := createTicketFor(t, h, requester, "Printer jam")
	if _, err := h.tickets.PutOnHold(t.Context(), *h.admin, tkt.ID, "send us a photo of the jammed tray"); err != nil {
		t.Fatalf("put on hold: %v", err)
	}

	body := getDetailAs(t, h, ticketPath(tkt.ID), sess.ID, false).Body.String()

	for _, want := range []string{
		`class="hold-banner"`,
		`This ticket is waiting for your reply`,
		`send us a photo of the jammed tray`,
		`<span class="chip">On hold</span>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("requester detail of a held ticket must contain %q, got: %s", want, body)
		}
	}
	for _, absent := range []string{
		`action="` + ticketPath(tkt.ID) + `/hold"`,
		`action="` + ticketPath(tkt.ID) + `/resume"`,
		`id="hold-reason"`,
		`Put on hold`,
	} {
		if strings.Contains(body, absent) {
			t.Errorf("requester detail must NOT contain %q, got: %s", absent, body)
		}
	}
}

// TestTicketDetailRequesterSeesNoHoldForm proves an open ticket that is not on
// hold offers the requester no hold action at all.
func TestTicketDetailRequesterSeesNoHoldForm(t *testing.T) {
	h := newHarness(t)
	requester := seedUserRole(t, h.store, "Rosa", "rosa-open@tkt.test", domain.RoleUser)
	sess := seedSession(t, h.store, requester.ID)
	tkt := createTicketFor(t, h, requester, "Printer jam")

	body := getDetailAs(t, h, ticketPath(tkt.ID), sess.ID, false).Body.String()

	for _, absent := range []string{`action="` + ticketPath(tkt.ID) + `/hold"`, `id="hold-reason"`, `Put on hold`} {
		if strings.Contains(body, absent) {
			t.Errorf("requester detail must NOT contain %q, got: %s", absent, body)
		}
	}
}

// TestTicketDetailTerminalTicketHasNoHoldControl proves a closed ticket offers
// neither the hold action nor the resume control.
func TestTicketDetailTerminalTicketHasNoHoldControl(t *testing.T) {
	for _, state := range []domain.State{domain.StateResolved, domain.StateCancelled} {
		t.Run(string(state), func(t *testing.T) {
			h := newHarness(t)
			tkt := h.seedTicket(t, "Login page down", nil)
			h.makeLegacy(t, tkt.ID)
			if state == domain.StateResolved {
				h.seedTransition(t, tkt.ID, domain.StateInProgress, "")
			}
			h.seedTransition(t, tkt.ID, state, "")

			body := h.get(t, ticketPath(tkt.ID), false).Body.String()

			for _, absent := range []string{`action="` + ticketPath(tkt.ID) + `/hold"`, `action="` + ticketPath(tkt.ID) + `/resume"`, `id="hold-reason"`, "Put on hold"} {
				if strings.Contains(body, absent) {
					t.Errorf("%s ticket must NOT contain %q, got: %s", state, absent, body)
				}
			}
		})
	}
}

// TestTicketOnHoldChipInLists proves the on-hold chip renders next to the state
// badge in the staff queue AND the requester list.
func TestTicketOnHoldChipInLists(t *testing.T) {
	h := newHarness(t)
	requester := seedUserRole(t, h.store, "Rosa", "rosa-list@tkt.test", domain.RoleUser)
	sess := seedSession(t, h.store, requester.ID)
	tkt := createTicketFor(t, h, requester, "Printer jam")
	holdViaService(t, h, tkt.ID, "waiting for the VPN logs")

	staff := h.get(t, "/tickets", false).Body.String()
	if !strings.Contains(staff, `<span class="chip">On hold</span>`) {
		t.Errorf("the staff queue must chip a held ticket, got: %s", staff)
	}

	own := getDetailAs(t, h, "/tickets", sess.ID, false).Body.String()
	if !strings.Contains(own, `<span class="chip">On hold</span>`) {
		t.Errorf("the requester list must chip a held ticket, got: %s", own)
	}
}

// TestTicketDetailSLAResolvePausesOnHold proves only the resolution milestone
// pauses (issue #352 S2): it renders the paused chip and stops carrying the
// client countdown hook, while the first-response milestone keeps its live
// countdown.
func TestTicketDetailSLAResolvePausesOnHold(t *testing.T) {
	h := newHarness(t)
	form := slaPanelForm("80")
	form.Set("sla_enabled", "1")
	if rec := h.postForm(t, "/settings/sla", form, false); rec.Code != http.StatusSeeOther {
		t.Fatalf("enable SLA: status = %d, want %d", rec.Code, http.StatusSeeOther)
	}
	tkt := h.seedTicket(t, "Freeze me", nil)
	frozen, err := h.store.SLAStore().TicketSLA(t.Context(), tkt.ID)
	if err != nil || frozen == nil {
		t.Fatalf("read frozen commitment: %v (frozen=%v)", err, frozen)
	}
	holdViaService(t, h, tkt.ID, "waiting for the VPN logs")

	body := h.get(t, ticketPath(tkt.ID), false).Body.String()

	if !strings.Contains(body, `<span class="chip">Paused</span>`) {
		t.Errorf("the paused resolution milestone must render the paused chip, got: %s", body)
	}
	if strings.Contains(body, `<time datetime="`+formatDatetime(frozen.DueResolveAt)+`" data-sla-countdown`) {
		t.Errorf("a paused milestone must not keep the live countdown hook, got: %s", body)
	}
	if !strings.Contains(body, `<time datetime="`+formatDatetime(frozen.DueFirstResponseAt)+`" data-sla-countdown`) {
		t.Errorf("the first-response milestone must keep its countdown, got: %s", body)
	}
}

// TestTicketTimelineHoldNarrative proves the two hold actions read as a
// narrative, the hold carrying its reason, on the staff AND the requester
// timeline (issue #352 S3/S8).
func TestTicketTimelineHoldNarrative(t *testing.T) {
	h := newHarness(t)
	requester := seedUserRole(t, h.store, "Rosa", "rosa-narrative@tkt.test", domain.RoleUser)
	sess := seedSession(t, h.store, requester.ID)
	tkt := createTicketFor(t, h, requester, "Printer jam")
	holdViaService(t, h, tkt.ID, "waiting for the VPN logs")

	for _, got := range []struct{ name, body string }{
		{name: "staff", body: h.get(t, ticketPath(tkt.ID), false).Body.String()},
		{name: "requester", body: getDetailAs(t, h, ticketPath(tkt.ID), sess.ID, false).Body.String()},
	} {
		if !strings.Contains(got.body, "put the ticket on hold: waiting for the VPN logs") {
			t.Errorf("%s timeline must narrate the hold with its reason, got: %s", got.name, got.body)
		}
		if strings.Contains(got.body, "Reason: waiting for the VPN logs") {
			t.Errorf("%s timeline must not print the reason twice, got: %s", got.name, got.body)
		}
	}

	if _, err := h.tickets.ResumeFromHold(t.Context(), *h.admin, tkt.ID); err != nil {
		t.Fatalf("resume: %v", err)
	}
	body := h.get(t, ticketPath(tkt.ID), false).Body.String()
	if !strings.Contains(body, "took the ticket off hold") {
		t.Errorf("timeline must narrate the resume, got: %s", body)
	}
}

// mustTicket re-reads one ticket through the scoped read.
func mustTicket(t *testing.T, h *harness, id int64) *domain.Ticket {
	t.Helper()
	view, err := h.tickets.GetByID(context.Background(), *h.admin, id)
	if err != nil {
		t.Fatalf("read ticket %d: %v", id, err)
	}
	return view.Ticket
}

// TestTicketHoldSurfaceInSpanish proves the whole hold surface follows the
// instance language (issue #352 S6): neutral tuteo, the word "ticket", and the
// agreed vocabulary ("En espera", "Poner en espera", "Reanudar", "En pausa",
// "Este ticket espera tu respuesta"). The pause reason is data and renders
// verbatim.
func TestTicketHoldSurfaceInSpanish(t *testing.T) {
	h := newHarness(t)
	if err := h.settings.SetInterfaceLanguage(t.Context(), *h.admin, application.InterfaceLanguageSpanish); err != nil {
		t.Fatalf("set interface language: %v", err)
	}
	requester := seedUserRole(t, h.store, "Rosa", "rosa-es@tkt.test", domain.RoleUser)
	sess := seedSession(t, h.store, requester.ID)
	tkt := createTicketFor(t, h, requester, "Impresora atascada")

	// Open, not on hold: the staff form speaks Spanish.
	staffOpen := h.get(t, ticketPath(tkt.ID), false).Body.String()
	for _, want := range []string{"Poner en espera", "Motivo (el solicitante lo va a leer)", "¿Qué necesitas del solicitante?"} {
		if !strings.Contains(staffOpen, want) {
			t.Errorf("es hold form must contain %q, got: %s", want, staffOpen)
		}
	}

	holdViaService(t, h, tkt.ID, "envíanos una foto de la bandeja")
	staffHeld := h.get(t, ticketPath(tkt.ID), false).Body.String()
	for _, want := range []string{
		"En espera desde",
		"Reanudar",
		`<span class="chip">En espera</span>`,
		"puso el ticket en espera: envíanos una foto de la bandeja",
	} {
		if !strings.Contains(staffHeld, want) {
			t.Errorf("es staff detail of a held ticket must contain %q, got: %s", want, staffHeld)
		}
	}
	// The localized date shape ("6 ago 2026"), not the numeric fallback.
	if !strings.Contains(staffHeld, `</time></p><p class="sla-hold-reason">envíanos una foto de la bandeja`) {
		t.Errorf("es notice must render the localized date before the reason, got: %s", staffHeld)
	}

	requesterBody := getDetailAs(t, h, ticketPath(tkt.ID), sess.ID, false).Body.String()
	for _, want := range []string{"Este ticket espera tu respuesta", "envíanos una foto de la bandeja"} {
		if !strings.Contains(requesterBody, want) {
			t.Errorf("es requester banner must contain %q, got: %s", want, requesterBody)
		}
	}
}

// TestRequesterCommentEndsTheHoldEndToEnd proves S1 through the real comment
// route, service and unit of work: the requester's public reply on a held
// ticket stores the comment, clears both hold columns, and records the resume
// under the requester. The harness runs with SLA disabled, so the resolution
// shift is proven in the application and SQLite layers.
func TestRequesterCommentEndsTheHoldEndToEnd(t *testing.T) {
	h := newHarness(t)
	requester := seedUserRole(t, h.store, "Rosa", "rosa-reply@tkt.test", domain.RoleUser)
	sess := seedSession(t, h.store, requester.ID)
	tkt := createTicketFor(t, h, requester, "Printer jam")
	holdViaService(t, h, tkt.ID, "send us a photo of the jammed tray")
	db := h.rawDB(t)

	rec := h.postFormAs(t, ticketPath(tkt.ID)+"/comments", url.Values{"body": {"photo attached"}}, sess.ID)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("comment status = %d, want 303; body: %s", rec.Code, rec.Body.String())
	}
	assertHoldColumns(t, h, tkt.ID, false, "")
	if n := scanOneInt(t, db, `SELECT COUNT(*) FROM comments WHERE ticket_id = ? AND body = 'photo attached'`, tkt.ID); n != 1 {
		t.Errorf("comments = %d, want 1", n)
	}
	if n := scanOneInt(t, db, `SELECT COUNT(*) FROM audit_events WHERE ticket_id = ? AND action = 'resume' AND actor_user_id = ?`, tkt.ID, requester.ID); n != 1 {
		t.Errorf("resume events by the requester = %d, want 1", n)
	}
}
