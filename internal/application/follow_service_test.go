package application_test

import (
	"context"
	"errors"
	"testing"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// Following and mentions (issue #418). The two rules under test are the ones
// that must never widen ticket access:
//
//   - only staff who can ALREADY open the ticket may follow it or be mentioned;
//     everybody else is answered exactly like a missing ticket, so neither the
//     follow control nor the mention list reveals that a ticket exists;
//   - the check runs at the moment of the operation, so losing access to the
//     ticket (or to the staff role) ends both without touching a stored row.
//
// The in-memory ticket fake carries no workflow runs or desk memberships, so the
// claim-pending half of an agent's read scope reduces to the assignment here
// (documented in fakes_test.go); the SQLite filters prove the claim half.

// followHarness is the notification fixture: a ticket whose requester is a
// user-role person and whose assignee is an agent, plus the follow use case.
type followHarness struct {
	*notifyHarness
}

func newFollowHarness(t *testing.T) *followHarness {
	t.Helper()
	return &followHarness{notifyHarness: newNotifyHarness(t)}
}

// otherAgent seeds an active agent who is neither the assignee nor otherwise
// connected to the ticket: somebody who cannot open it.
func (h *followHarness) otherAgent(t *testing.T, name string) domain.User {
	t.Helper()
	return h.users.seedRole(name, name+"@example.com", domain.RoleAgent, true)
}

func TestFollowRequiresTicketAccess(t *testing.T) {
	h := newFollowHarness(t)
	ctx := context.Background()
	outsider := h.otherAgent(t, "Bea")

	cases := []struct {
		name  string
		actor domain.User
	}{
		{"an agent who is not assigned the ticket", outsider},
		{"the requester", h.requester},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := h.followSvc.Follow(ctx, tc.actor, h.ticket.ID)
			if !errors.Is(err, domain.ErrNotFound) {
				t.Fatalf("Follow = %v, want a not-found refusal (no existence leak)", err)
			}
			if following, _ := h.followers.IsFollowing(ctx, h.ticket.ID, tc.actor.ID); following {
				t.Fatal("a refused follow stored a follower row")
			}
		})
	}

	t.Run("a missing ticket", func(t *testing.T) {
		if err := h.followSvc.Follow(ctx, h.admin, 4242); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("Follow = %v, want a not-found refusal", err)
		}
	})

	t.Run("staff who can open the ticket", func(t *testing.T) {
		if err := h.followSvc.Follow(ctx, h.admin, h.ticket.ID); err != nil {
			t.Fatalf("Follow (admin): %v", err)
		}
		if following, err := h.followers.IsFollowing(ctx, h.ticket.ID, h.admin.ID); err != nil || !following {
			t.Fatalf("IsFollowing = %v, %v, want true", following, err)
		}
	})
}

func TestFollowAndUnfollowAreIdempotent(t *testing.T) {
	h := newFollowHarness(t)
	ctx := context.Background()

	for i := 0; i < 2; i++ {
		if err := h.followSvc.Follow(ctx, h.assignee, h.ticket.ID); err != nil {
			t.Fatalf("Follow (call %d): %v", i+1, err)
		}
	}
	if got := h.followers.rows[h.ticket.ID]; len(got) != 1 {
		t.Fatalf("follower rows = %v, want the single relation", got)
	}

	for i := 0; i < 2; i++ {
		if err := h.followSvc.Unfollow(ctx, h.assignee, h.ticket.ID); err != nil {
			t.Fatalf("Unfollow (call %d): %v", i+1, err)
		}
	}
	if following, _ := h.followers.IsFollowing(ctx, h.ticket.ID, h.assignee.ID); following {
		t.Fatal("unfollowing twice left a follower row")
	}
}

func TestUnfollowNeedsStaffButSurvivesLosingTheTicket(t *testing.T) {
	h := newFollowHarness(t)
	ctx := context.Background()

	t.Run("a requester cannot unfollow", func(t *testing.T) {
		err := h.followSvc.Unfollow(ctx, h.requester, h.ticket.ID)
		var forbidden *domain.ForbiddenError
		if !errors.As(err, &forbidden) {
			t.Fatalf("Unfollow = %v, want a forbidden refusal", err)
		}
	})

	t.Run("a follower who lost the ticket can still leave", func(t *testing.T) {
		if err := h.followSvc.Follow(ctx, h.assignee, h.ticket.ID); err != nil {
			t.Fatalf("Follow: %v", err)
		}
		// The ticket is reassigned, so the follower can no longer open it.
		h.ticket.UserID = ptr(h.admin.ID)
		if err := h.tickets.Update(ctx, &h.ticket); err != nil {
			t.Fatalf("reassign: %v", err)
		}
		if err := h.followSvc.Follow(ctx, h.assignee, h.ticket.ID); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("Follow after losing access = %v, want a not-found refusal", err)
		}
		if err := h.followSvc.Unfollow(ctx, h.assignee, h.ticket.ID); err != nil {
			t.Fatalf("Unfollow after losing access: %v", err)
		}
		if following, _ := h.followers.IsFollowing(ctx, h.ticket.ID, h.assignee.ID); following {
			t.Fatal("the follower row survived an unfollow")
		}
	})
}

func TestFollowersListIsScopedAndOrdered(t *testing.T) {
	h := newFollowHarness(t)
	ctx := context.Background()
	// An administrator sorts before the assignee by display name: "Ada" < "Ana".
	for _, who := range []domain.User{h.admin, h.assignee} {
		if err := h.followSvc.Follow(ctx, who, h.ticket.ID); err != nil {
			t.Fatalf("Follow(%s): %v", who.Name, err)
		}
	}

	got, err := h.followSvc.Followers(ctx, h.admin, h.ticket.ID)
	if err != nil {
		t.Fatalf("Followers: %v", err)
	}
	if len(got) != 2 || got[0].ID != h.admin.ID || got[1].ID != h.assignee.ID {
		t.Fatalf("followers = %+v, want Ada then Ana", got)
	}
	if got[0].Role != domain.RoleAdmin || got[1].Role != domain.RoleAgent {
		t.Errorf("followers carry roles %q/%q, want admin/agent", got[0].Role, got[1].Role)
	}
	if !got[0].Active || !got[1].Active {
		t.Error("followers do not carry their active state")
	}

	outsider := h.otherAgent(t, "Bea")
	if _, err := h.followSvc.Followers(ctx, outsider, h.ticket.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Followers (out of scope) = %v, want a not-found refusal", err)
	}
	if _, err := h.followSvc.IsFollowing(ctx, outsider, h.ticket.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("IsFollowing (out of scope) = %v, want a not-found refusal", err)
	}
}

func TestMentionCandidatesAreStaffWhoCanOpenTheTicket(t *testing.T) {
	h := newFollowHarness(t)
	ctx := context.Background()
	outsider := h.otherAgent(t, "Bea")
	inactive := h.users.seedRole("Cid", "cid@example.com", domain.RoleAgent, false)
	secondAdmin := h.users.seedRole("Bo", "bo@example.com", domain.RoleAdmin, true)

	got, err := h.followSvc.MentionCandidates(ctx, h.admin, h.ticket.ID)
	if err != nil {
		t.Fatalf("MentionCandidates: %v", err)
	}
	var gotIDs []int64
	for _, u := range got {
		gotIDs = append(gotIDs, u.ID)
	}
	// Ana (the assignee) and Bo (an administrator) can open the ticket; the actor
	// herself, the requester, an unrelated agent and an inactive account are not
	// candidates.
	if len(got) != 2 || got[0].ID != h.assignee.ID || got[1].ID != secondAdmin.ID {
		t.Fatalf("candidates = %+v (ids %v), want Ana then Bo", got, gotIDs)
	}
	for _, excluded := range []domain.User{h.admin, h.requester, outsider, inactive} {
		for _, id := range gotIDs {
			if id == excluded.ID {
				t.Errorf("candidate list includes %s (%s), who must not be mentionable", excluded.Name, excluded.Role)
			}
		}
	}

	if _, err := h.followSvc.MentionCandidates(ctx, h.requester, h.ticket.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("MentionCandidates (requester) = %v, want a not-found refusal", err)
	}
	if _, err := h.followSvc.MentionCandidates(ctx, outsider, h.ticket.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("MentionCandidates (out of scope) = %v, want a not-found refusal", err)
	}
}

// TestFollowedStaffLosesTheSubscriptionWhenDemoted pins the second half of the
// access rule: the relation survives a role change, and the moment the person is
// no longer staff the follow use case treats them like anybody else.
func TestFollowedStaffLosesTheSubscriptionWhenDemoted(t *testing.T) {
	h := newFollowHarness(t)
	ctx := context.Background()
	if err := h.followSvc.Follow(ctx, h.admin, h.ticket.ID); err != nil {
		t.Fatalf("Follow: %v", err)
	}
	h.users.users[h.admin.ID].Role = domain.RoleUser
	// The actor arrives from the session, which reads the stored account: the
	// demotion is visible on the next request.
	demoted, err := h.users.GetByID(ctx, h.admin.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}

	if following, err := h.followSvc.IsFollowing(ctx, *demoted, h.ticket.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("IsFollowing = %v, %v, want a not-found refusal", following, err)
	}
	if _, err := h.followSvc.MentionCandidates(ctx, *demoted, h.ticket.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("MentionCandidates = %v, want a not-found refusal", err)
	}
}

// MentionNameContext is the rendering read the timeline needs (issue #438, S4),
// and it must not become a second way into the ticket. It answers the reader's
// OWN ticket read: the requester reading their ticket learns which staff names a
// comment may carry, an actor the ticket is out of reach for gets the same
// not-found every scoped read answers, and nothing about mentioning is granted —
// MentionCandidates keeps its staff-only refusal for that same requester.
func TestMentionNameContextIsAGatedRenderingRead(t *testing.T) {
	h := newFollowHarness(t)
	ctx := context.Background()
	outsider := h.otherAgent(t, "Bea")

	names, err := h.followSvc.MentionNameContext(ctx, h.requester, h.ticket.ID)
	if err != nil {
		t.Fatalf("MentionNameContext (requester's own ticket): %v", err)
	}
	// The assignee is among the people the requester's comments can name; the
	// reader herself and an unrelated agent never are. (The staff member who is
	// not reading may be: they can open this ticket.)
	var got []string
	got = append(got, names...)
	want := map[string]bool{h.assignee.Name: true}
	for _, name := range got {
		delete(want, name)
	}
	if len(want) != 0 {
		t.Fatalf("name context = %v, want %q among them", names, h.assignee.Name)
	}
	for _, excluded := range []string{h.requester.Name, outsider.Name} {
		for _, name := range got {
			if name == excluded {
				t.Errorf("name context includes %q, which is not a candidate", excluded)
			}
		}
	}

	if _, err := h.followSvc.MentionNameContext(ctx, outsider, h.ticket.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("MentionNameContext (out of scope) = %v, want the scoped not-found", err)
	}
	// Rendering names never authorizes a mention: the requester still cannot ask
	// for the candidate list, which is what the composer and the fan-out use, so
	// a requester writing "@Name" keeps notifying nobody
	// (TestRequesterCommentMentionsNobody pins that half).
	if _, err := h.followSvc.MentionCandidates(ctx, h.requester, h.ticket.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("MentionCandidates (requester) = %v, want the staff-only refusal", err)
	}
}
