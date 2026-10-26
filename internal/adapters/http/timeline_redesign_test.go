package httpadapter

import (
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// Issue #438 (S2, S3, S5): the activity feed is one card per comment with a
// small typed dot on a slim rail — no avatars, no icons — and the internal
// comment no longer prints a "staff only" label, because its own tint and its
// narrative already say what it is.
func TestTimelineCommentsAreCardsWithTypeDots(t *testing.T) {
	h := newHarness(t)
	tkt := h.seedTicket(t, "Timeline cards", nil)
	if _, err := h.comments.Add(t.Context(), *h.admin, tkt.ID, "public-card-token", "public"); err != nil {
		t.Fatalf("seed public comment: %v", err)
	}
	if _, err := h.comments.Add(t.Context(), *h.admin, tkt.ID, "internal-card-token", "internal"); err != nil {
		t.Fatalf("seed internal comment: %v", err)
	}

	body := h.get(t, ticketPath(tkt.ID), false).Body.String()
	activity := activityRegion(t, body)

	// S2: every comment is its own card, head over body, and neither is grouped
	// with the other.
	if got := strings.Count(activity, `<article class="timeline-entry timeline-comment`); got != 2 {
		t.Fatalf("each comment must be its own card, got %d cards in: %s", got, activity)
	}
	if got := strings.Count(activity, `class="timeline-body"`); got != 2 {
		t.Errorf("every comment card carries exactly one body, got %d in: %s", got, activity)
	}
	internalCardAt := strings.Index(activity, `class="timeline-entry timeline-comment internal`)
	if internalCardAt < 0 {
		t.Fatalf("an internal comment must keep its own card class, got: %s", activity)
	}
	// Each comment owns its own body inside its own card. The ORDER is the
	// reader's preference (issue #438, S1): this reader never chose, so the
	// newest comment — the internal one, written second — renders first, and the
	// public card closes below it. What must not happen is grouping or
	// interleaving: the public body stays inside the card that follows.
	if publicAt := strings.Index(activity, "public-card-token"); publicAt < 0 || publicAt < internalCardAt {
		t.Errorf("each comment must own its content: with newest first the public body must come after the internal card opens, got: %s", activity)
	}
	if internalAt, publicAt := strings.Index(activity, "internal-card-token"), strings.Index(activity, "public-card-token"); internalAt < 0 || publicAt < 0 || publicAt < internalAt {
		t.Errorf("comment cards must not be grouped or reordered, got: %s", activity)
	}

	// S5: the audience label is gone everywhere, and the narrative still says it.
	if strings.Contains(body, "staff only") || strings.Contains(body, "solo el equipo") {
		t.Errorf("the internal comment must not label its audience any more, got: %s", body)
	}
	if strings.Contains(body, `class="timeline-visibility"`) {
		t.Errorf("the internal visibility line must be gone, got: %s", body)
	}
	if !strings.Contains(activity, "added an internal comment") {
		t.Errorf("the internal narrative must stay, got: %s", activity)
	}

	// S3: dots only. Nothing on the rail is a person mark or a glyph.
	for _, absent := range []string{`class="avatar"`, `class="event-icon"`, `class="timeline-glyph"`, `class="person-mark`} {
		if strings.Contains(activity, absent) {
			t.Errorf("the rail must carry typed dots, not %q: %s", absent, activity)
		}
	}

	// S3: the dot grammar and the card shape, measured in the served stylesheet.
	style := extractStyleBlock(t, body)
	for _, want := range []struct{ sel, decl string }{
		{".timeline-entry::before{", "width:7px"},
		{".timeline-entry::before{", "border:1.5px solid var(--faint)"},
		{".timeline-comment::before{", "background:var(--ink)"},
		{".timeline-comment.internal::before{", "color-mix(in srgb,var(--internal-comment-bg)"},
		{".timeline-comment{", "border-radius:12px"},
		{".timeline-event.st-in_progress::before{", "background:var(--amber)"},
		{".timeline-event[data-dot=prio-high]::before{", "background:var(--red)"},
	} {
		if !cssRuleDeclares(style, want.sel, want.decl) {
			t.Errorf("timeline stylesheet must declare %q in %q", want.decl, want.sel)
		}
	}
	// A comment is never indented further than an event: both hang off the rail.
	if strings.Contains(style, ".timeline-entry.timeline-comment{padding-left") {
		t.Error("a comment must not be indented away from the rail")
	}
}

// Issue #438 (S9): Steps is its own card above Activity and the composer, not a
// timeline entry with a dot.
func TestTimelineStepsCardLeavesActivity(t *testing.T) {
	h := newHarness(t)
	tkt, _ := pendingManualFixture(t, h, "Inspect the intake queue")

	body := h.get(t, ticketPath(tkt.ID), false).Body.String()
	stepsAt := strings.Index(body, `id="workflow-pending"`)
	timelineAt := strings.Index(body, `<div id="timeline">`)
	composerAt := strings.Index(body, `class="card comment-form"`)
	descriptionAt := strings.Index(body, `<h2>Description</h2>`)
	if stepsAt < 0 || timelineAt < 0 || composerAt < 0 || descriptionAt < 0 {
		t.Fatalf("the detail must render description, Steps, Activity and composer: %.600s", body)
	}
	if stepsAt < descriptionAt || stepsAt > timelineAt {
		t.Errorf("Steps must be its own card between the description and Activity, got steps=%d timeline=%d in: %s", stepsAt, timelineAt, body)
	}
	if stepsAt > composerAt {
		t.Errorf("Steps must stay above the composer, got steps=%d composer=%d", stepsAt, composerAt)
	}
	if strings.Contains(body[timelineAt:], `id="workflow-pending"`) {
		t.Errorf("the step checklist must leave Activity, got: %s", body[timelineAt:])
	}
	// Its own card carries no rail dot: the wrapper drops the timeline mark.
	style := extractStyleBlock(t, body)
	if !cssRuleDeclares(style, ".detail-steps .workflow-pending::before{", "content:none") {
		t.Error("the Steps card must not draw a timeline dot")
	}
	// The checklist itself is untouched: heading, count and current step control.
	for _, want := range []string{
		`<h3 id="workflow-steps-title">Steps</h3>`,
		`<span class="workflow-checklist-count">0 of 1 done</span>`,
		`action="/tickets/` + strconv.FormatInt(tkt.ID, 10) + `/workflow/steps/1/complete"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the Steps card must keep %q, got: %s", want, body)
		}
	}
}

// Issue #438 (S4): the feed is viewer-relative. Your own comment carries a
// "You" chip and a neutral head tint, a comment that mentions you carries the
// accent head and the "Mentions you" tag with your mention drawn filled, and
// your own events say "You" instead of your name.
func TestTimelineViewerMarkers(t *testing.T) {
	h := newHarness(t)
	tkt := h.seedTicket(t, "Viewer markers", nil)
	beto := seedUserRole(t, h.store, "Beto Ruiz", "beto-viewer@tkt.test", domain.RoleAgent)
	// Beto holds the ticket, so a second person can write on it too.
	h.assignTicket(t, tkt.ID, beto.ID)

	if _, err := h.comments.Add(t.Context(), *h.admin, tkt.ID, "own-marker-token", "public"); err != nil {
		t.Fatalf("seed own comment: %v", err)
	}
	if _, err := h.comments.Add(t.Context(), *beto, tkt.ID, "mention-token please check @"+h.admin.Name+" thanks", "public"); err != nil {
		t.Fatalf("seed mentioning comment: %v", err)
	}
	if _, err := h.comments.Add(t.Context(), *beto, tkt.ID, "plain-beto-token", "public"); err != nil {
		t.Fatalf("seed plain comment: %v", err)
	}
	h.seedTransition(t, tkt.ID, domain.StateInProgress, "")

	body := h.get(t, ticketPath(tkt.ID), true).Body.String()
	activity := activityRegion(t, body)

	own := timelineCard(t, activity, "own-marker-token")
	for _, want := range []string{
		`class="timeline-entry timeline-comment mine"`,
		`<span class="timeline-you">You</span>`,
	} {
		if !strings.Contains(own, want) {
			t.Errorf("your own comment must carry %q, got: %s", want, own)
		}
	}

	mentioning := timelineCard(t, activity, "mention-token")
	for _, want := range []string{
		`class="timeline-entry timeline-comment mentioned"`,
		`<span class="timeline-mentions-you">Mentions you</span>`,
		`<span class="timeline-mention me">@` + h.admin.Name + `</span>`,
	} {
		if !strings.Contains(mentioning, want) {
			t.Errorf("a comment that mentions you must carry %q, got: %s", want, mentioning)
		}
	}
	if strings.Contains(mentioning, " mine") {
		t.Errorf("somebody else's comment is never yours, got: %s", mentioning)
	}

	plain := timelineCard(t, activity, "plain-beto-token")
	for _, absent := range []string{" timeline-comment mine", " mentioned", "timeline-mentions-you", "timeline-mention"} {
		if strings.Contains(plain, absent) {
			t.Errorf("a comment that neither is yours nor mentions you must not carry %q, got: %s", absent, plain)
		}
	}

	// Your own events name you as "You"; the card keeps one head line.
	if !strings.Contains(body, `<strong class="timeline-actor">You</strong> <span class="timeline-action">moved the ticket to in progress</span>`) {
		t.Errorf("your own event must render the actor as You, got: %s", activity)
	}
	// And somebody else's event keeps their name.
	if !strings.Contains(activity, `<strong class="timeline-actor">`+beto.Name+`</strong>`) {
		t.Errorf("another person's event must keep their name, got: %s", activity)
	}
}

// Issue #438 (S4): authorship is decided by the stored author id, never by the
// display-name snapshot. A comment whose name was written as yours stays
// somebody else's comment.
func TestTimelineViewerIdentityUsesUserIDsNotNames(t *testing.T) {
	h := newHarness(t)
	tkt := h.seedTicket(t, "Viewer identity", nil)
	beto := seedUserRole(t, h.store, "Beto Ruiz", "beto-identity@tkt.test", domain.RoleAgent)
	h.assignTicket(t, tkt.ID, beto.ID)
	comment, err := h.comments.Add(t.Context(), *beto, tkt.ID, "identity-token", "public")
	if err != nil {
		t.Fatalf("seed comment: %v", err)
	}
	if _, err := h.rawDB(t).Exec(`UPDATE comments SET author = ? WHERE id = ?`, h.admin.Name, comment.ID); err != nil {
		t.Fatalf("rewrite the author name snapshot: %v", err)
	}

	body := h.get(t, ticketPath(tkt.ID), true).Body.String()
	card := timelineCard(t, activityRegion(t, body), "identity-token")
	if strings.Contains(card, " timeline-comment mine") || strings.Contains(card, "timeline-you") {
		t.Errorf("a same-named comment by another user must not be yours, got: %s", card)
	}
	if !strings.Contains(card, `<strong class="timeline-actor">`+h.admin.Name+`</strong>`) {
		t.Errorf("the stored name snapshot still renders, got: %s", card)
	}
}

// Issue #438 (S4): a chipped mention is data. A hostile body escapes once, and
// a name nobody on the ticket carries stays plain text.
func TestTimelineMentionTextIsEscaped(t *testing.T) {
	h := newHarness(t)
	tkt := h.seedTicket(t, "Escaped mentions", nil)
	if _, err := h.comments.Add(t.Context(), *h.admin, tkt.ID, "escaped-token <img src=x onerror=alert(1)> hola @"+h.admin.Name+" y @Ghost Person", "public"); err != nil {
		t.Fatalf("seed hostile comment: %v", err)
	}

	body := h.get(t, ticketPath(tkt.ID), true).Body.String()
	card := timelineCard(t, activityRegion(t, body), "escaped-token")
	if strings.Contains(card, "<img src=x") {
		t.Errorf("a hostile body must escape exactly once, got: %s", card)
	}
	if !strings.Contains(card, "&lt;img src=x onerror=alert(1)&gt;") {
		t.Errorf("the hostile text must render escaped, got: %s", card)
	}
	if !strings.Contains(card, `<span class="timeline-mention me">@`+h.admin.Name+`</span>`) {
		t.Errorf("your mention must be chipped as data, got: %s", card)
	}
	if strings.Contains(card, "timeline-mention me\">@Ghost") || !strings.Contains(card, "@Ghost Person") {
		t.Errorf("a name nobody carries must stay plain text, got: %s", card)
	}
}

// Issue #438 keeps what the comment card and the event entry could already do:
// the files of a comment render inside its card, and a manual completion keeps
// its task fields. Both are covered deeper elsewhere; this pins the card the
// redesign rebuilt around them.
func TestTimelineCardKeepsAttachments(t *testing.T) {
	h := newHarness(t)
	tkt := h.seedTicket(t, "Card attachments", nil)
	rec := h.postMultipart(t, ticketPath(tkt.ID)+"/comments", url.Values{
		"body":       {"attachment-card-token"},
		"visibility": {"public"},
	}, []multipartFile{{name: "card.png", data: pngFixture}}, h.adminSession.ID)
	wantRedirect(t, rec, 303, ticketPath(tkt.ID))

	body := h.get(t, ticketPath(tkt.ID), true).Body.String()
	card := timelineCard(t, activityRegion(t, body), "attachment-card-token")
	for _, want := range []string{`<ul class="attachments"`, `card.png`, `href="/attachments/`} {
		if !strings.Contains(card, want) {
			t.Errorf("the comment card must keep its attachments, missing %q in: %s", want, card)
		}
	}
}

// activityRegion returns the rendered Activity feed alone, so an assertion about
// a timeline entry can never be satisfied by the stylesheet or another card.
func activityRegion(t *testing.T, body string) string {
	t.Helper()
	start := strings.Index(body, `<div id="timeline">`)
	if start < 0 {
		t.Fatalf("the detail must render Activity, got: %s", body)
	}
	region := body[start:]
	for _, end := range []string{`<aside class="evidence">`, `<div class="evidence">`} {
		if at := strings.Index(region, end); at >= 0 {
			region = region[:at]
			break
		}
	}
	return region
}

// timelineCard returns the one timeline entry that carries marker, so a claim
// about a comment is never satisfied by a sibling entry.
func timelineCard(t *testing.T, activity, marker string) string {
	t.Helper()
	at := strings.Index(activity, marker)
	if at < 0 {
		t.Fatalf("Activity must carry %q, got: %s", marker, activity)
	}
	start := strings.LastIndex(activity[:at], `<article class="timeline-entry`)
	if start < 0 {
		t.Fatalf("%q must sit inside a timeline entry, got: %s", marker, activity)
	}
	end := strings.Index(activity[at:], `</article>`)
	if end < 0 {
		t.Fatalf("the entry carrying %q must close, got: %s", marker, activity)
	}
	return activity[start : at+end+len(`</article>`)]
}
