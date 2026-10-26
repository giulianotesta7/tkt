package httpadapter

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// The timeline's rendering context must not expose either another ticket's
// staff or the staff-only autocomplete to a requester (issue #438, S4).
func TestTicketTimelineMentionContextAccess(t *testing.T) {
	h := newHarness(t)
	owner := seedUserRole(t, h.store, "Owner", "owner-438@test.invalid", domain.RoleUser)
	outsider := seedUserRole(t, h.store, "Outsider", "outsider-438@test.invalid", domain.RoleAgent)
	stranger := seedUserRole(t, h.store, "Stranger", "stranger-438@test.invalid", domain.RoleUser)
	ticket, err := h.tickets.Create(t.Context(), *owner, application.CreateTicketInput{
		Title: "Mention context access", CategoryID: h.bugCategory.ID, Priority: domain.PriorityMedium,
	})
	if err != nil {
		t.Fatal(err)
	}
	svc := application.NewFollowService(h.store.TicketStore(), h.store.UserStore(), h.store.TicketFollowerStore(), h.clock).WithDesks(h.store.DeskStore())
	names, err := svc.MentionNameContext(t.Context(), *owner, ticket.ID)
	if err != nil || !reflect.DeepEqual(names, []string{h.admin.Name}) {
		t.Fatalf("owner names=%v err=%v", names, err)
	}
	for _, tc := range []struct {
		label string
		actor domain.User
		id    int64
	}{
		{"unassigned agent", *outsider, ticket.ID},
		{"other requester", *stranger, ticket.ID},
		{"missing ticket", *owner, 424242},
	} {
		t.Run(tc.label, func(t *testing.T) {
			got, err := svc.MentionNameContext(t.Context(), tc.actor, tc.id)
			_, readErr := h.tickets.GetByID(t.Context(), tc.actor, tc.id)
			if len(got) != 0 || !errors.Is(err, domain.ErrNotFound) || readErr == nil || err.Error() != readErr.Error() {
				t.Fatalf("names=%v error=%v readError=%v", got, err, readErr)
			}
		})
	}
	candidates, err := svc.MentionCandidates(t.Context(), *owner, ticket.ID)
	if len(candidates) != 0 || !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("requester autocomplete expanded: %v %v", candidates, err)
	}
	session := seedSession(t, h.store, owner.ID)
	rec := getDetailAs(t, h, ticketPath(ticket.ID), session.ID, true)
	if rec.Code != 200 || strings.Contains(rec.Body.String(), "data-mentions") || strings.Contains(rec.Body.String(), "data-mention-name") {
		t.Fatalf("requester autocomplete leaked: status=%d", rec.Code)
	}
}
