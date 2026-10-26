package httpadapter

import (
	"fmt"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// Issue #438 correction. The timeline's presentation splitter (SplitMentions)
// returns non-overlapping spans, and an earlier version of it also drove the
// mention NOTIFICATION scan, which silently dropped a person named inside a
// longer display name. A managed user's name may contain an "@" (the name
// validation allows it), so "@Operations @Ana" must keep naming BOTH people —
// the baseline contract this test freezes: one mention notification each, one
// subscription each, whichever order the candidates arrive in.
//
// The exhaustive grammar cases live in internal/application/mentions_test.go;
// this journey proves the two effects a person feels, notification and
// subscription, through the real comment service and stores.
func TestTicketTimelineMentionNotificationsKeepTheBaselineScan(t *testing.T) {
	cases := []struct {
		label string
		body  string
		names []string
		want  []string
	}{
		{"nested name", "@Operations @Ana", []string{"Operations @Ana", "Ana"}, []string{"Operations @Ana", "Ana"}},
		{"nested name, other candidate order", "@Operations @Ana", []string{"Ana", "Operations @Ana"}, []string{"Operations @Ana", "Ana"}},
		{"nested accented name", "@工程 @José Núñez!", []string{"工程 @José Núñez", "José Núñez"}, []string{"工程 @José Núñez", "José Núñez"}},
		{"longest name wins", "@Ana Assignee", []string{"Ana", "Ana Assignee"}, []string{"Ana Assignee"}},
		{"longest name wins, other candidate order", "@Ana Assignee", []string{"Ana Assignee", "Ana"}, []string{"Ana Assignee"}},
		{"an at sign inside a word names nobody else", "@Ops@Ana", []string{"Ops@Ana", "Ana"}, []string{"Ops@Ana"}},
		{"a longer word mentions nobody", "é@Ana @José Núñez2", []string{"Ana", "José Núñez"}, nil},
		{"the same person twice is mentioned once", "@Ana and @Ana", []string{"Ana", "Ana"}, []string{"Ana"}},
		{"no at sign mentions nobody", "no mention here", []string{"Ana"}, nil},
		{"punctuation closes one mention and opens the next", "(@Ana), @José Núñez!", []string{"Ana", "José Núñez"}, []string{"Ana", "José Núñez"}},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			h := newHarness(t)
			ticket := h.seedTicket(t, "Mention notifications", nil)
			people := make([]*domain.User, 0, len(tc.names))
			for i, name := range tc.names {
				// Names are the arrange value under test: the managed-user path
				// validates and stores them exactly as written, '@' included.
				person := h.createUser(t, name, fmt.Sprintf("mention-%d@tkt.test", i), "secure-password-438")
				// An administrator can open every ticket, which is what makes the
				// person a mention candidate at all.
				person.Role = domain.RoleAdmin
				if err := h.store.UserStore().Update(t.Context(), person); err != nil {
					t.Fatalf("promote %q: %v", name, err)
				}
				people = append(people, person)
			}
			comment, err := h.comments.Add(t.Context(), *h.admin, ticket.ID, tc.body, "public")
			if err != nil {
				t.Fatalf("comment %q: %v", tc.body, err)
			}

			for _, person := range people {
				notifications, err := h.store.NotificationStore().ListForUser(t.Context(), person.ID, 50)
				if err != nil {
					t.Fatalf("notifications of %q: %v", person.Name, err)
				}
				mentioned := 0
				for _, n := range notifications {
					if n.Kind == domain.NotificationMentioned && n.CommentID != nil && *n.CommentID == comment.ID {
						mentioned++
					}
				}
				following, err := h.store.TicketFollowerStore().IsFollowing(t.Context(), ticket.ID, person.ID)
				if err != nil {
					t.Fatalf("following of %q: %v", person.Name, err)
				}
				want := false
				for _, named := range tc.want {
					if named == person.Name {
						want = true
					}
				}
				if (mentioned == 1) != want || following != want {
					t.Errorf("%q: mentions=%d following=%t, want mentions=%d following=%t for %q",
						person.Name, mentioned, following, map[bool]int{true: 1, false: 0}[want], want, tc.body)
				}
			}
		})
	}
}

// TestTicketTimelineMentionContextIsViewerRelative freezes the presentation half
// (issue #438, S4) on the three pages that can show a comment: an ordinary staff
// page whose candidate list overlaps the reader's own name, a requester's page,
// and a closed ticket where no composer renders at all. The tag and the filled
// chip follow the SAME longest-name match the body is chipped with, so a body
// that names "Ana Assignee" never claims to mention the reader called "Ana".
func TestTicketTimelineMentionContextIsViewerRelative(t *testing.T) {
	cases := []struct {
		label  string
		body   string
		closed bool
		// requester renders the page for the requester instead of for a staff
		// member: no picker data exists at all and no staff write is offered.
		requester bool
		// wantTag is true when the body names the reader as well as the other
		// person, so exactly one card is tagged and one chip is filled.
		wantTag bool
	}{
		{label: "reader name is a prefix of the other name", body: "probe-viewer @Ana Assignee", wantTag: false},
		{label: "requester reads the same body", body: "probe-viewer @Ana and @Ana Assignee", requester: true, wantTag: true},
		{label: "closed ticket with no composer", body: "probe-viewer @Ana and @Ana Assignee", closed: true, wantTag: true},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			h := newHarness(t)
			other := seedUserRole(t, h.store, "Ana Assignee", "ana-assignee-438@tkt.test", domain.RoleAdmin)
			viewer := seedUserRole(t, h.store, "Ana", "ana-viewer-438@tkt.test", domain.RoleAdmin)
			if tc.requester {
				viewer.Role = domain.RoleUser
				if err := h.store.UserStore().Update(t.Context(), viewer); err != nil {
					t.Fatalf("make the reader a requester: %v", err)
				}
			}
			ticket, err := h.tickets.Create(t.Context(), *viewer, application.CreateTicketInput{
				Title: "Viewer mentions", CategoryID: h.bugCategory.ID, Priority: domain.PriorityMedium,
			})
			if err != nil {
				t.Fatalf("create ticket: %v", err)
			}
			if _, err := h.comments.Add(t.Context(), *h.admin, ticket.ID, tc.body, "public"); err != nil {
				t.Fatalf("comment %q: %v", tc.body, err)
			}
			if tc.closed {
				h.seedTransition(t, ticket.ID, domain.StateInProgress, "")
				h.seedTransition(t, ticket.ID, domain.StateResolved, "")
				if _, err := h.tickets.ConfirmResolution(t.Context(), *viewer, ticket.ID); err != nil {
					t.Fatalf("confirm resolution: %v", err)
				}
			}
			_ = other

			session := seedSession(t, h.store, viewer.ID)
			rec := getDetailAs(t, h, ticketPath(ticket.ID), session.ID, true)
			if rec.Code != 200 {
				t.Fatalf("GET %s = %d: %s", ticketPath(ticket.ID), rec.Code, rec.Body.String())
			}
			body := rec.Body.String()
			at := strings.Index(body, "probe-viewer")
			if at < 0 {
				t.Fatalf("the comment must render for the reader, got: %s", body)
			}
			start := strings.LastIndex(body[:at], `<article class="timeline-entry`)
			end := strings.Index(body[at:], `</article>`)
			if start < 0 || end < 0 {
				t.Fatalf("the comment must render as a timeline card, got: %s", body)
			}
			card := body[start : at+end+len(`</article>`)]

			if got := strings.Contains(card, `class="timeline-mentions-you"`); got != tc.wantTag {
				t.Errorf("Mentions you tag = %t, want %t in: %s", got, tc.wantTag, card)
			}
			if got := strings.Contains(card, `class="timeline-mention me">@Ana</span>`); got != tc.wantTag {
				t.Errorf("filled mention of the reader = %t, want %t in: %s", got, tc.wantTag, card)
			}
			if !strings.Contains(card, `class="timeline-mention">@Ana Assignee</span>`) {
				t.Errorf("the other person's mention must stay a soft chip in: %s", card)
			}
			chips := strings.Count(card, `class="timeline-mention"`) + strings.Count(card, `class="timeline-mention me"`)
			wantChips := 1
			if tc.wantTag {
				wantChips = 2
			}
			if chips != wantChips {
				t.Errorf("chips = %d, want %d: a body naming %d people must chip exactly those, got: %s", chips, wantChips, wantChips, card)
			}
		})
	}
}
