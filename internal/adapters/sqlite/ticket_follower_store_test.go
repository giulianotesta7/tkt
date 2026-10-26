package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// The follower store (issue #418) over the real ticket_followers table of
// migration 0033: one row per (ticket, person), written idempotently, and the
// follower list ordered by display name with the facts the notification policy
// reads.

func TestTicketFollowerStoreFollowsOncePerPerson(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	_, agent, tk := mutationFixture(t, s)
	at := testClock.Add(time.Minute)

	for i := 0; i < 2; i++ {
		if err := s.TicketFollowerStore().Follow(ctx, tk.ID, agent, at); err != nil {
			t.Fatalf("Follow (call %d): %v", i+1, err)
		}
	}
	if following, err := s.TicketFollowerStore().IsFollowing(ctx, tk.ID, agent); err != nil || !following {
		t.Fatalf("IsFollowing = %v, %v, want true", following, err)
	}

	followers, err := s.TicketFollowerStore().ListFollowers(ctx, tk.ID)
	if err != nil {
		t.Fatalf("ListFollowers: %v", err)
	}
	if len(followers) != 1 {
		t.Fatalf("followers = %+v, want the single relation", followers)
	}
	got := followers[0]
	if got.ID != agent || got.Role != domain.RoleAgent || !got.Active {
		t.Errorf("follower = %+v, want the active agent %d with its role", got, agent)
	}
	// The projection carries the notification policy facts only: no email and no
	// password hash travel with a follower list.
	if got.Email != "" || got.PasswordHash != "" {
		t.Errorf("follower list exposes email/password (%q/%q)", got.Email, got.PasswordHash)
	}

	// Following twice keeps the instant of the first follow: the relation did not
	// start over.
	var createdAt string
	if err := s.db.QueryRowContext(ctx,
		`SELECT created_at FROM ticket_followers WHERE ticket_id = ? AND user_id = ?`,
		tk.ID, agent).Scan(&createdAt); err != nil {
		t.Fatalf("read follower row: %v", err)
	}
	if createdAt != formatTime(at) {
		t.Errorf("created_at = %q, want %q", createdAt, formatTime(at))
	}
}

func TestTicketFollowerStoreUnfollowIsIdempotent(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	_, agent, tk := mutationFixture(t, s)

	if err := s.TicketFollowerStore().Follow(ctx, tk.ID, agent, testClock); err != nil {
		t.Fatalf("Follow: %v", err)
	}
	for i := 0; i < 2; i++ {
		if err := s.TicketFollowerStore().Unfollow(ctx, tk.ID, agent); err != nil {
			t.Fatalf("Unfollow (call %d): %v", i+1, err)
		}
	}
	if following, err := s.TicketFollowerStore().IsFollowing(ctx, tk.ID, agent); err != nil || following {
		t.Fatalf("IsFollowing = %v, %v, want false", following, err)
	}
	// Unfollowing what was never followed is not an error either.
	if err := s.TicketFollowerStore().Unfollow(ctx, tk.ID, agent); err != nil {
		t.Fatalf("Unfollow (never followed): %v", err)
	}
}

func TestTicketFollowerStoreListsByNameThenID(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	_, _, tk := mutationFixture(t, s)
	zoe := seedUserRole(t, s, "Zoe", "zoe@followers", true, domain.RoleAgent)
	ana := seedUserRole(t, s, "Ana", "ana@followers", true, domain.RoleAgent)
	bea := seedUserRole(t, s, "Bea", "bea@followers", true, domain.RoleAdmin)

	for _, id := range []int64{zoe, ana, bea} {
		if err := s.TicketFollowerStore().Follow(ctx, tk.ID, id, testClock); err != nil {
			t.Fatalf("Follow(%d): %v", id, err)
		}
	}
	followers, err := s.TicketFollowerStore().ListFollowers(ctx, tk.ID)
	if err != nil {
		t.Fatalf("ListFollowers: %v", err)
	}
	want := []int64{ana, bea, zoe}
	if len(followers) != len(want) {
		t.Fatalf("followers = %+v, want %d", followers, len(want))
	}
	for i, id := range want {
		if followers[i].ID != id {
			t.Errorf("follower %d = %d, want %d (ordered by display name)", i, followers[i].ID, id)
		}
	}
}

func TestTicketFollowerStoreIsScopedToTheTicket(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	_, agent, tk := mutationFixture(t, s)
	otherTicket := seedTicket(t, s, domain.Ticket{Number: 2, Title: "Printer jams", CategoryID: tk.CategoryID,
		Priority: domain.PriorityMedium, State: domain.StateNew, CreatedAt: testClock, UpdatedAt: testClock})

	if err := s.TicketFollowerStore().Follow(ctx, tk.ID, agent, testClock); err != nil {
		t.Fatalf("Follow: %v", err)
	}
	// The same person on another ticket is a different relation.
	if following, err := s.TicketFollowerStore().IsFollowing(ctx, otherTicket.ID, agent); err != nil || following {
		t.Fatalf("IsFollowing on another ticket = %v, %v, want false", following, err)
	}
	otherFollowers, err := s.TicketFollowerStore().ListFollowers(ctx, otherTicket.ID)
	if err != nil {
		t.Fatalf("ListFollowers: %v", err)
	}
	if len(otherFollowers) != 0 {
		t.Fatalf("the other ticket lists %+v, want nobody", otherFollowers)
	}
}
