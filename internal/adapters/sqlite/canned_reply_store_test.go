package sqlite

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// Canned replies (issue #418): CRUD over the real
// canned_replies table of migration 0033, the ErrNotFound contract, and the
// ListUsable membership rule — a reply of a desk the user does not belong to is
// never returned, and it disappears as soon as the membership does.

func seedCannedReply(t *testing.T, s *Store, reply domain.CannedReply) domain.CannedReply {
	t.Helper()
	if err := newCannedReplyStore(s.db).Create(context.Background(), &reply); err != nil {
		t.Fatalf("seed canned reply %q: %v", reply.Title, err)
	}
	return reply
}

func cannedReplyTitles(replies []domain.CannedReply) []string {
	out := make([]string, 0, len(replies))
	for _, reply := range replies {
		out = append(out, reply.Title)
	}
	return out
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestCannedReplyStoreCRUD(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	store := newCannedReplyStore(s.db)

	ownerID := seedUser(t, s, "Agent", "agent@example.com", true)
	deskID := seedDesk(t, s, "Support")

	personal := &domain.CannedReply{
		OwnerUserID: &ownerID,
		Title:       "Greeting",
		Body:        "Hi {requester}, about {ticket}",
		CreatedAt:   testClock,
		UpdatedAt:   testClock,
	}
	if err := store.Create(ctx, personal); err != nil {
		t.Fatalf("create personal reply: %v", err)
	}
	if personal.ID == 0 {
		t.Fatal("create personal reply did not assign an ID")
	}

	got, err := store.GetByID(ctx, personal.ID)
	if err != nil {
		t.Fatalf("get personal reply: %v", err)
	}
	if got.Title != "Greeting" || got.Body != "Hi {requester}, about {ticket}" ||
		got.OwnerUserID == nil || *got.OwnerUserID != ownerID || got.DeskID != nil ||
		!got.CreatedAt.Equal(testClock) || !got.UpdatedAt.Equal(testClock) {
		t.Fatalf("personal reply round trip = %+v, want the stored row with a nil desk", got)
	}

	shared := &domain.CannedReply{DeskID: &deskID, Title: "Escalation", Body: "Escalating {ticket}", CreatedAt: testClock, UpdatedAt: testClock}
	if err := store.Create(ctx, shared); err != nil {
		t.Fatalf("create desk reply: %v", err)
	}
	got, err = store.GetByID(ctx, shared.ID)
	if err != nil {
		t.Fatalf("get desk reply: %v", err)
	}
	if got.DeskID == nil || *got.DeskID != deskID || got.OwnerUserID != nil {
		t.Fatalf("desk reply round trip = %+v, want the stored row with a nil owner", got)
	}

	// Update rewrites only the editable text and the stamp: ownership is not an
	// editable field of a saved reply, so a store-level Update cannot move a
	// reply between people or desks.
	personalReply, err := store.GetByID(ctx, personal.ID)
	if err != nil {
		t.Fatalf("reload personal reply: %v", err)
	}
	personalReply.Title = "Friendly greeting"
	personalReply.Body = "Hello {requester}"
	personalReply.UpdatedAt = testClock.Add(time.Hour)
	// A stale caller that also hands back a changed owner must not move it.
	personalReply.OwnerUserID = nil
	personalReply.DeskID = &deskID
	if err := store.Update(ctx, personalReply); err != nil {
		t.Fatalf("update reply: %v", err)
	}
	updated, err := store.GetByID(ctx, personal.ID)
	if err != nil {
		t.Fatalf("get updated reply: %v", err)
	}
	if updated.Title != "Friendly greeting" || updated.Body != "Hello {requester}" || !updated.UpdatedAt.Equal(testClock.Add(time.Hour)) {
		t.Fatalf("updated reply = %+v, want the new text and updated_at", updated)
	}
	if updated.OwnerUserID == nil || *updated.OwnerUserID != ownerID || updated.DeskID != nil {
		t.Fatalf("updated reply owner = %+v, want the original owner untouched", updated)
	}

	if err := store.Delete(ctx, personal.ID); err != nil {
		t.Fatalf("delete reply: %v", err)
	}
	if _, err := store.GetByID(ctx, personal.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("get deleted reply = %v, want ErrNotFound", err)
	}
}

func TestCannedReplyStoreMissingIDIsNotFound(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	store := newCannedReplyStore(s.db)

	if _, err := store.GetByID(ctx, 999); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("GetByID missing = %v, want ErrNotFound", err)
	}
	if err := store.Update(ctx, &domain.CannedReply{ID: 999, Title: "Gone", Body: "Gone"}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Update missing = %v, want ErrNotFound", err)
	}
	if err := store.Delete(ctx, 999); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Delete missing = %v, want ErrNotFound", err)
	}
}

func TestCannedReplyStoreRejectsUnknownOwnerAndDesk(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	store := newCannedReplyStore(s.db)

	missingUser := int64(404)
	if err := store.Create(ctx, &domain.CannedReply{OwnerUserID: &missingUser, Title: "Orphan", Body: "Orphan"}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("create with unknown owner = %v, want ErrNotFound", err)
	}
	missingDesk := int64(404)
	if err := store.Create(ctx, &domain.CannedReply{DeskID: &missingDesk, Title: "Orphan", Body: "Orphan"}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("create with unknown desk = %v, want ErrNotFound", err)
	}
}

func TestCannedReplyStoreListsAreScopedAndOrdered(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	store := newCannedReplyStore(s.db)

	agentID := seedUser(t, s, "Agent", "agent@example.com", true)
	otherID := seedUser(t, s, "Other", "other@example.com", true)
	supportID := seedDesk(t, s, "Support")
	salesID := seedDesk(t, s, "Sales")

	seedCannedReply(t, s, domain.CannedReply{OwnerUserID: &agentID, Title: "b-personal", Body: "body", CreatedAt: testClock, UpdatedAt: testClock})
	seedCannedReply(t, s, domain.CannedReply{OwnerUserID: &agentID, Title: "a-personal", Body: "body", CreatedAt: testClock, UpdatedAt: testClock})
	seedCannedReply(t, s, domain.CannedReply{OwnerUserID: &otherID, Title: "other-personal", Body: "body", CreatedAt: testClock, UpdatedAt: testClock})
	seedCannedReply(t, s, domain.CannedReply{DeskID: &supportID, Title: "z-support", Body: "body", CreatedAt: testClock, UpdatedAt: testClock})
	seedCannedReply(t, s, domain.CannedReply{DeskID: &supportID, Title: "a-support", Body: "body", CreatedAt: testClock, UpdatedAt: testClock})
	seedCannedReply(t, s, domain.CannedReply{DeskID: &salesID, Title: "m-sales", Body: "body", CreatedAt: testClock, UpdatedAt: testClock})

	personal, err := store.ListPersonal(ctx, agentID)
	if err != nil {
		t.Fatalf("list personal: %v", err)
	}
	if got, want := cannedReplyTitles(personal), []string{"a-personal", "b-personal"}; !equalStrings(got, want) {
		t.Fatalf("ListPersonal = %v, want %v (own replies only, by title)", got, want)
	}

	deskReplies, err := store.ListByDesk(ctx, supportID)
	if err != nil {
		t.Fatalf("list by desk: %v", err)
	}
	if got, want := cannedReplyTitles(deskReplies), []string{"a-support", "z-support"}; !equalStrings(got, want) {
		t.Fatalf("ListByDesk = %v, want %v", got, want)
	}
}

// TestCannedReplyStoreListUsableFollowsDeskMembership is the membership rule at
// the store boundary: the picker may offer the actor's own replies and the
// replies of the desks the actor is a MEMBER of, and a desk reply is never
// offered outside its membership — not even to another agent of another desk.
func TestCannedReplyStoreListUsableFollowsDeskMembership(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	store := newCannedReplyStore(s.db)
	desks := newDeskStore(s.db)

	agentID := seedUser(t, s, "Agent", "agent@example.com", true)
	// The desks are created so their NAMES order differently from their ids:
	// Zulu is created first (lower id) and must still come last, because
	// ListUsable orders desk groups by desk name.
	zuluID := seedDesk(t, s, "Zulu")
	alphaID := seedDesk(t, s, "Alpha")
	if err := desks.AddMember(ctx, zuluID, agentID, testClock); err != nil {
		t.Fatalf("add Zulu member: %v", err)
	}
	if err := desks.AddMember(ctx, alphaID, agentID, testClock); err != nil {
		t.Fatalf("add Alpha member: %v", err)
	}
	// A desk the agent does NOT belong to.
	secretID := seedDesk(t, s, "Secret")

	seedCannedReply(t, s, domain.CannedReply{OwnerUserID: &agentID, Title: "b-mine", Body: "body", CreatedAt: testClock, UpdatedAt: testClock})
	seedCannedReply(t, s, domain.CannedReply{OwnerUserID: &agentID, Title: "a-mine", Body: "body", CreatedAt: testClock, UpdatedAt: testClock})
	seedCannedReply(t, s, domain.CannedReply{DeskID: &zuluID, Title: "z-zulu", Body: "body", CreatedAt: testClock, UpdatedAt: testClock})
	seedCannedReply(t, s, domain.CannedReply{DeskID: &alphaID, Title: "b-alpha", Body: "body", CreatedAt: testClock, UpdatedAt: testClock})
	seedCannedReply(t, s, domain.CannedReply{DeskID: &alphaID, Title: "a-alpha", Body: "body", CreatedAt: testClock, UpdatedAt: testClock})
	seedCannedReply(t, s, domain.CannedReply{DeskID: &secretID, Title: "a-secret", Body: "body", CreatedAt: testClock, UpdatedAt: testClock})

	usable, err := store.ListUsable(ctx, agentID)
	if err != nil {
		t.Fatalf("list usable: %v", err)
	}
	want := []string{"a-mine", "b-mine", "a-alpha", "b-alpha", "z-zulu"}
	if got := cannedReplyTitles(usable); !equalStrings(got, want) {
		t.Fatalf("ListUsable = %v, want %v (personal first by title, then member desks by desk name, title)", got, want)
	}

	// Losing the Alpha membership removes Alpha's replies from the picker: the
	// grant is the membership row, not a role.
	if err := desks.RemoveMember(ctx, alphaID, agentID); err != nil {
		t.Fatalf("remove Alpha member: %v", err)
	}
	usable, err = store.ListUsable(ctx, agentID)
	if err != nil {
		t.Fatalf("list usable after removal: %v", err)
	}
	if got, want := cannedReplyTitles(usable), []string{"a-mine", "b-mine", "z-zulu"}; !equalStrings(got, want) {
		t.Fatalf("ListUsable after membership removal = %v, want %v", got, want)
	}

	// Another agent sees their own replies and their own desks only: the
	// Secret desk's reply is offered to its member and to nobody else.
	otherID := seedUser(t, s, "Other", "other@example.com", true)
	if err := desks.AddMember(ctx, secretID, otherID, testClock); err != nil {
		t.Fatalf("add Secret member: %v", err)
	}
	usable, err = store.ListUsable(ctx, otherID)
	if err != nil {
		t.Fatalf("list usable for other agent: %v", err)
	}
	if got, want := cannedReplyTitles(usable), []string{"a-secret"}; !equalStrings(got, want) {
		t.Fatalf("ListUsable for the other agent = %v, want %v", got, want)
	}

	// A user who belongs to no desk and owns nothing has an empty library, not
	// an error and not somebody else's replies.
	strangerID := seedUser(t, s, "Stranger", "stranger@example.com", true)
	usable, err = store.ListUsable(ctx, strangerID)
	if err != nil {
		t.Fatalf("list usable for a stranger: %v", err)
	}
	if len(usable) != 0 {
		t.Fatalf("ListUsable for a stranger = %v, want nothing", cannedReplyTitles(usable))
	}
}
