package application_test

import (
	"errors"
	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
	"testing"
)

// S14: the same caller scope reaches Search, Count and Export, not recipients.
func TestAPIRegressionScopeInteractions(t *testing.T) {
	h := newTicketHarness()
	cat := h.categories.seed("Probes")
	owner := h.users.seedRole("Owner", "owner@probe.test", domain.RoleUser, true)
	actor := h.users.seedRole("Caller", "caller@probe.test", domain.RoleAgent, true)
	actor.Kind = domain.UserKindService
	if err := h.users.Update(t.Context(), &actor); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.Create(t.Context(), owner, validCreateInput(cat.ID)); err != nil {
		t.Fatal(err)
	}
	search := application.NewSearchService(h.tickets, &fakeSearchStore{tickets: h.tickets})
	for _, all := range []bool{false, true} {
		ctx := t.Context()
		want := 0
		if all {
			ctx = application.WithTicketScopeAll(ctx)
			want = 1
		}
		n, err := search.Count(ctx, actor, application.TicketQuery{})
		if err != nil || n != want {
			t.Fatalf("count=%d %v", n, err)
		}
		n = 0
		err = search.Export(ctx, actor, application.TicketQuery{}, func(domain.Ticket) error { n++; return nil })
		if err != nil || n != want {
			t.Fatalf("export=%d %v", n, err)
		}
	}
	f := newFanOutHarness(t)
	caller := f.users.seedRole("Integration", "caller@probe.test", domain.RoleAgent, true)
	caller.Kind = domain.UserKindService
	if err := f.users.Update(t.Context(), &caller); err != nil {
		t.Fatal(err)
	}
	outsider := f.users.seedRole("Outside", "outside@probe.test", domain.RoleAgent, true)
	outsider.Kind = domain.UserKindService
	if err := f.users.Update(t.Context(), &outsider); err != nil {
		t.Fatal(err)
	}
	ctx := application.WithTicketScopeAll(t.Context())
	if err := f.followSvc.Follow(ctx, caller, f.ticket.ID); err != nil {
		t.Fatal(err)
	}
	if yes, err := f.followSvc.IsFollowing(ctx, caller, f.ticket.ID); err != nil || !yes {
		t.Fatalf("caller follow=%v %v", yes, err)
	}
	if _, err := f.followSvc.IsFollowing(t.Context(), caller, f.ticket.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unflagged caller=%v", err)
	}
	candidates, err := f.followSvc.MentionCandidates(ctx, caller, f.ticket.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range candidates {
		if u.ID == outsider.ID {
			t.Fatal("recipient inherited caller flag")
		}
	}
	if _, err := f.svc.Add(ctx, caller, f.ticket.ID, "@Ana @Outside check this", "internal"); err != nil {
		t.Fatal(err)
	}
	if len(f.commentTx.notified(f.assignee.ID)) != 1 || len(f.commentTx.notified(outsider.ID)) != 0 {
		t.Fatal("incorrect notification recipients")
	}
	if yes, _ := f.followers.IsFollowing(ctx, f.ticket.ID, outsider.ID); yes {
		t.Fatal("outsider subscribed")
	}
}
