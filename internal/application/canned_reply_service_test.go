package application_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// Saved replies (issue #418): staff only, personal replies private to their
// owner, shared replies managed by desk managers and offered only to desk
// MEMBERS, and both screens read from one service.

func cannedReplyAgent(id int64) domain.User { return domain.User{ID: id, Role: domain.RoleAgent} }

func newCannedReplyHarness() (*application.CannedReplyService, *fakeCannedReplyStore, *fakeCannedReplyDesks, *fakeClock) {
	clock := fixedClock()
	desks := newFakeCannedReplyDesks()
	replies := newFakeCannedReplyStore(desks)
	return application.NewCannedReplyService(replies, desks, clock), replies, desks, clock
}

func cannedReplyTitles(replies []domain.CannedReply) []string {
	out := make([]string, 0, len(replies))
	for _, reply := range replies {
		out = append(out, reply.Title)
	}
	return out
}

// requireTitles asserts one grouped listing's titles in order, which is the
// whole point of the documented ordering.
func requireTitles(t *testing.T, what string, replies []domain.CannedReply, want ...string) {
	t.Helper()
	got := cannedReplyTitles(replies)
	if len(got) != len(want) {
		t.Fatalf("%s = %v, want %v", what, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s = %v, want %v", what, got, want)
		}
	}
}

func requireCannedReplyForbidden(t *testing.T, operation string, err error) {
	t.Helper()
	var forbidden *domain.ForbiddenError
	if !errors.As(err, &forbidden) {
		t.Fatalf("%s = %v, want a ForbiddenError", operation, err)
	}
}

func requireCannedReplyNotFound(t *testing.T, operation string, err error) {
	t.Helper()
	var notFound *domain.NotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("%s = %v, want a NotFoundError", operation, err)
	}
}

// TestCannedReplyServiceIsStaffOnly is the audience rule: a saved reply is a
// staff tool, so every operation — including both listings — requires the
// internal-comment capability that only agents, admins and root hold. A
// requester gets the repository's ordinary forbidden error and touches nothing.
func TestCannedReplyServiceIsStaffOnly(t *testing.T) {
	svc, replies, desks, clock := newCannedReplyHarness()
	ctx := context.Background()
	desk := desks.create("Support")
	requester := domain.User{ID: 9, Role: domain.RoleUser}
	ticket := &domain.Ticket{Number: 3, RequesterName: "Ada"}

	personal := replies.seed(domain.CannedReply{OwnerUserID: ptr(requester.ID), Title: "Mine", Body: "body", CreatedAt: clock.Now(), UpdatedAt: clock.Now()})
	shared := replies.seed(domain.CannedReply{DeskID: ptr(desk.ID), Title: "Shared", Body: "body", CreatedAt: clock.Now(), UpdatedAt: clock.Now()})
	before := replies.mutations

	operations := []struct {
		name string
		run  func() error
	}{
		{"create personal", func() error { _, err := svc.CreatePersonal(ctx, requester, "Hi", "body"); return err }},
		{"update personal", func() error { _, err := svc.UpdatePersonal(ctx, requester, personal.ID, "Hi", "body"); return err }},
		{"delete personal", func() error { return svc.DeletePersonal(ctx, requester, personal.ID) }},
		{"create for desk", func() error { _, err := svc.CreateForDesk(ctx, requester, desk.ID, "Hi", "body"); return err }},
		{"update for desk", func() error { _, err := svc.UpdateForDesk(ctx, requester, shared.ID, "Hi", "body"); return err }},
		{"delete for desk", func() error { return svc.DeleteForDesk(ctx, requester, shared.ID) }},
		{"manager listing", func() error { _, err := svc.Manager(ctx, requester); return err }},
		{"picker listing", func() error { _, err := svc.ForTicket(ctx, requester, ticket); return err }},
	}
	for _, operation := range operations {
		requireCannedReplyForbidden(t, operation.name, operation.run())
	}
	if replies.mutations != before {
		t.Fatalf("a denied requester mutated the store %d times", replies.mutations-before)
	}
	stored, err := replies.GetByID(ctx, personal.ID)
	if err != nil {
		t.Fatalf("personal reply vanished: %v", err)
	}
	if stored.Title != "Mine" {
		t.Fatalf("personal reply = %q, want it untouched", stored.Title)
	}
}

// TestCannedReplyServicePersonalRepliesStayWithTheirOwner proves the privacy
// rule twice over: another agent neither edits nor deletes somebody else's
// personal reply, and the answer is NotFound rather than Forbidden, so the
// refusal leaks no existence. The clock stamps CreatedAt/UpdatedAt and the
// trimmed text is what gets stored.
func TestCannedReplyServicePersonalRepliesStayWithTheirOwner(t *testing.T) {
	svc, replies, desks, clock := newCannedReplyHarness()
	ctx := context.Background()
	owner, other := cannedReplyAgent(1), cannedReplyAgent(2)

	created, err := svc.CreatePersonal(ctx, owner, "  Greeting  ", "\n Hi {requester} \n")
	if err != nil {
		t.Fatalf("CreatePersonal: %v", err)
	}
	if created.Title != "Greeting" || created.Body != "Hi {requester}" {
		t.Fatalf("created reply = %q / %q, want the trimmed text", created.Title, created.Body)
	}
	if created.OwnerUserID == nil || *created.OwnerUserID != owner.ID || created.DeskID != nil {
		t.Fatalf("created reply owner = %+v, want the acting agent and no desk", created)
	}
	if !created.CreatedAt.Equal(clock.Now()) || !created.UpdatedAt.Equal(clock.Now()) {
		t.Fatalf("created reply stamps = %v / %v, want the injected clock %v", created.CreatedAt, created.UpdatedAt, clock.Now())
	}
	stored, err := replies.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("stored personal reply: %v", err)
	}
	if stored.Title != "Greeting" || stored.Body != "Hi {requester}" {
		t.Fatalf("stored reply = %q / %q, want the trimmed text persisted", stored.Title, stored.Body)
	}

	before := replies.mutations
	_, err = svc.UpdatePersonal(ctx, other, created.ID, "Hijacked", "Hijacked body")
	requireCannedReplyNotFound(t, "another agent update", err)
	requireCannedReplyNotFound(t, "another agent delete", svc.DeletePersonal(ctx, other, created.ID))
	if replies.mutations != before {
		t.Fatal("a refused cross-owner write reached the store")
	}
	stored, err = replies.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("personal reply vanished after refused writes: %v", err)
	}
	if stored.Title != "Greeting" || stored.Body != "Hi {requester}" {
		t.Fatalf("personal reply after refused writes = %q / %q, want it untouched", stored.Title, stored.Body)
	}

	clock.Advance(time.Hour)
	updated, err := svc.UpdatePersonal(ctx, owner, created.ID, "Friendly greeting", "Hello {ticket}")
	if err != nil {
		t.Fatalf("owner update: %v", err)
	}
	if updated.Title != "Friendly greeting" || updated.Body != "Hello {ticket}" {
		t.Fatalf("updated reply = %q / %q, want the new text", updated.Title, updated.Body)
	}
	if !updated.UpdatedAt.Equal(clock.Now()) || !updated.CreatedAt.Equal(created.CreatedAt) {
		t.Fatalf("updated stamps = %v / %v, want updated_at=%v and created_at kept", updated.UpdatedAt, updated.CreatedAt, clock.Now())
	}

	// A desk reply is not a personal reply, not even for the agent who can use
	// it: personal edits address personal rows only.
	desk := desks.create("Support")
	shared := replies.seed(domain.CannedReply{DeskID: ptr(desk.ID), Title: "Shared", Body: "body", CreatedAt: clock.Now(), UpdatedAt: clock.Now()})
	_, err = svc.UpdatePersonal(ctx, owner, shared.ID, "Hijacked", "Hijacked body")
	requireCannedReplyNotFound(t, "personal update of a desk reply", err)
	requireCannedReplyNotFound(t, "personal delete of a desk reply", svc.DeletePersonal(ctx, owner, shared.ID))

	if err := svc.DeletePersonal(ctx, owner, created.ID); err != nil {
		t.Fatalf("owner delete: %v", err)
	}
	if _, err := replies.GetByID(ctx, created.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("deleted personal reply = %v, want ErrNotFound", err)
	}
}

// TestCannedReplyServiceDeskRepliesNeedDeskManagement is the shared half of the
// rule: an agent uses a desk's replies but never writes them, desk management
// does, the desk must exist, and a personal reply is not addressable as a desk
// reply.
func TestCannedReplyServiceDeskRepliesNeedDeskManagement(t *testing.T) {
	svc, replies, desks, clock := newCannedReplyHarness()
	ctx := context.Background()
	agent := cannedReplyAgent(1)
	admin := domain.User{ID: 2, Role: domain.RoleAdmin}
	root := domain.User{ID: 3, Role: domain.RoleRoot}
	desk := desks.create("Support")
	desks.join(desk.ID, agent.ID)

	before := replies.mutations
	_, err := svc.CreateForDesk(ctx, agent, desk.ID, "Shared", "body")
	requireCannedReplyForbidden(t, "agent create for desk", err)
	if replies.mutations != before {
		t.Fatal("a denied desk create reached the store")
	}

	created, err := svc.CreateForDesk(ctx, admin, desk.ID, "Shared", "Escalating {ticket}")
	if err != nil {
		t.Fatalf("admin create for desk: %v", err)
	}
	if created.DeskID == nil || *created.DeskID != desk.ID || created.OwnerUserID != nil {
		t.Fatalf("created desk reply = %+v, want the desk as its only owner", created)
	}
	if !created.CreatedAt.Equal(clock.Now()) || !created.UpdatedAt.Equal(clock.Now()) {
		t.Fatalf("created desk reply stamps = %v / %v, want the injected clock", created.CreatedAt, created.UpdatedAt)
	}

	clock.Advance(time.Minute)
	updated, err := svc.UpdateForDesk(ctx, root, created.ID, "Shared v2", "Escalating now")
	if err != nil {
		t.Fatalf("root update for desk: %v", err)
	}
	if updated.Title != "Shared v2" || !updated.UpdatedAt.Equal(clock.Now()) {
		t.Fatalf("updated desk reply = %+v, want the new text stamped by the clock", updated)
	}

	_, err = svc.CreateForDesk(ctx, admin, 404, "Shared", "body")
	requireCannedReplyNotFound(t, "create for an unknown desk", err)

	before = replies.mutations
	_, err = svc.UpdateForDesk(ctx, agent, created.ID, "Hijacked", "Hijacked body")
	requireCannedReplyForbidden(t, "agent update for desk", err)
	requireCannedReplyForbidden(t, "agent delete for desk", svc.DeleteForDesk(ctx, agent, created.ID))
	if replies.mutations != before {
		t.Fatal("a denied desk write reached the store")
	}

	personal := replies.seed(domain.CannedReply{OwnerUserID: ptr(agent.ID), Title: "Mine", Body: "body", CreatedAt: clock.Now(), UpdatedAt: clock.Now()})
	_, err = svc.UpdateForDesk(ctx, admin, personal.ID, "Hijacked", "Hijacked body")
	requireCannedReplyNotFound(t, "desk update of a personal reply", err)
	requireCannedReplyNotFound(t, "desk delete of a personal reply", svc.DeleteForDesk(ctx, admin, personal.ID))

	if err := svc.DeleteForDesk(ctx, admin, created.ID); err != nil {
		t.Fatalf("admin delete for desk: %v", err)
	}
	if _, err := replies.GetByID(ctx, created.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("deleted desk reply = %v, want ErrNotFound", err)
	}
}

// TestCannedReplyServiceManager is the manager screen: the actor's own replies
// always editable, plus one group per desk. An agent sees only the desks they
// belong to and cannot edit them; an actor holding desks.manage sees every desk
// and edits every group.
func TestCannedReplyServiceManager(t *testing.T) {
	svc, replies, desks, clock := newCannedReplyHarness()
	ctx := context.Background()
	agent := cannedReplyAgent(1)
	admin := domain.User{ID: 2, Role: domain.RoleAdmin}
	root := domain.User{ID: 3, Role: domain.RoleRoot}
	zulu := desks.create("Zulu")
	alpha := desks.create("Alpha")
	desks.join(zulu.ID, agent.ID)

	seeded := func(reply domain.CannedReply) domain.CannedReply {
		reply.CreatedAt, reply.UpdatedAt = clock.Now(), clock.Now()
		return replies.seed(reply)
	}
	seeded(domain.CannedReply{OwnerUserID: ptr(agent.ID), Title: "p-b", Body: "body"})
	seeded(domain.CannedReply{OwnerUserID: ptr(agent.ID), Title: "p-a", Body: "body"})
	seeded(domain.CannedReply{DeskID: ptr(zulu.ID), Title: "z-only", Body: "body"})
	seeded(domain.CannedReply{DeskID: ptr(alpha.ID), Title: "a-only", Body: "body"})

	agentView, err := svc.Manager(ctx, agent)
	if err != nil {
		t.Fatalf("Manager as agent: %v", err)
	}
	requireTitles(t, "agent Mine", agentView.Mine, "p-a", "p-b")
	if len(agentView.Desks) != 1 {
		t.Fatalf("agent desk groups = %d, want only the desk they belong to", len(agentView.Desks))
	}
	group := agentView.Desks[0]
	if group.DeskID != zulu.ID || group.DeskName != "Zulu" || group.Editable {
		t.Fatalf("agent desk group = %+v, want Zulu, id %d, read-only", group, zulu.ID)
	}
	requireTitles(t, "agent Zulu replies", group.Replies, "z-only")

	for _, manager := range []domain.User{admin, root} {
		view, err := svc.Manager(ctx, manager)
		if err != nil {
			t.Fatalf("Manager as %s: %v", manager.Role, err)
		}
		if len(view.Mine) != 0 {
			t.Fatalf("%s Mine = %v, want the manager's own (empty) list", manager.Role, cannedReplyTitles(view.Mine))
		}
		if len(view.Desks) != 2 {
			t.Fatalf("%s desk groups = %d, want every desk", manager.Role, len(view.Desks))
		}
		// Groups are ordered by desk name, so the screen is stable and readable.
		if view.Desks[0].DeskName != "Alpha" || view.Desks[1].DeskName != "Zulu" {
			t.Fatalf("%s desk order = %q, %q, want Alpha then Zulu", manager.Role, view.Desks[0].DeskName, view.Desks[1].DeskName)
		}
		for _, group := range view.Desks {
			if !group.Editable {
				t.Fatalf("%s group %q must be editable", manager.Role, group.DeskName)
			}
		}
		requireTitles(t, string(manager.Role)+" Alpha replies", view.Desks[0].Replies, "a-only")
	}
}

// TestCannedReplyServicePicker is the composer's picker: the actor's personal
// replies and the replies of the desks they belong to, grouped in that order,
// with the placeholders already filled for the ticket. Even an admin gets no
// desk they are not a member of — a desk reply is never offered outside its
// membership.
func TestCannedReplyServicePicker(t *testing.T) {
	svc, replies, desks, clock := newCannedReplyHarness()
	ctx := context.Background()
	agent := cannedReplyAgent(1)
	admin := domain.User{ID: 2, Role: domain.RoleAdmin}
	alpha := desks.create("Alpha")
	beta := desks.create("Beta")
	desks.join(alpha.ID, agent.ID)
	desks.join(beta.ID, admin.ID)

	seeded := func(reply domain.CannedReply) domain.CannedReply {
		reply.CreatedAt, reply.UpdatedAt = clock.Now(), clock.Now()
		return replies.seed(reply)
	}
	seeded(domain.CannedReply{OwnerUserID: ptr(agent.ID), Title: "mine-filled", Body: "Hi {requester}, about {ticket}"})
	seeded(domain.CannedReply{OwnerUserID: ptr(agent.ID), Title: "mine-unknown", Body: "{greeting}, {ticket}"})
	seeded(domain.CannedReply{DeskID: ptr(alpha.ID), Title: "alpha-filled", Body: "Alpha on {ticket}"})
	seeded(domain.CannedReply{DeskID: ptr(beta.ID), Title: "beta-filled", Body: "Beta on {ticket}"})

	ticket := &domain.Ticket{Number: 42, RequesterName: "Ada Lovelace"}
	view, err := svc.ForTicket(ctx, agent, ticket)
	if err != nil {
		t.Fatalf("ForTicket as agent: %v", err)
	}
	requireTitles(t, "picker Mine", view.Mine, "mine-filled", "mine-unknown")
	if view.Mine[0].Body != "Hi Ada Lovelace, about TKT-42" {
		t.Fatalf("filled body = %q, want the requester name and the readable ticket number", view.Mine[0].Body)
	}
	if view.Mine[1].Body != "{greeting}, TKT-42" {
		t.Fatalf("unknown placeholder body = %q, want it left as typed", view.Mine[1].Body)
	}
	if len(view.Desks) != 1 {
		t.Fatalf("picker desk groups = %d, want only the desks the agent belongs to", len(view.Desks))
	}
	if view.Desks[0].DeskName != "Alpha" || view.Desks[0].Editable {
		t.Fatalf("picker desk group = %+v, want Alpha and not editable (the picker inserts text)", view.Desks[0])
	}
	if view.Desks[0].Replies[0].Body != "Alpha on TKT-42" {
		t.Fatalf("desk reply body = %q, want it filled for the ticket", view.Desks[0].Replies[0].Body)
	}

	// The admin belongs to Beta only, so Beta — and only Beta — is offered.
	adminView, err := svc.ForTicket(ctx, admin, ticket)
	if err != nil {
		t.Fatalf("ForTicket as admin: %v", err)
	}
	if len(adminView.Mine) != 0 {
		t.Fatalf("admin picker Mine = %v, want none", cannedReplyTitles(adminView.Mine))
	}
	if len(adminView.Desks) != 1 || adminView.Desks[0].DeskName != "Beta" {
		t.Fatalf("admin picker desks = %+v, want Beta only (membership, not the capability)", adminView.Desks)
	}
	if adminView.Desks[0].Replies[0].Body != "Beta on TKT-42" {
		t.Fatalf("admin Beta reply body = %q, want it filled for the ticket", adminView.Desks[0].Replies[0].Body)
	}
}

// TestCannedReplyServiceRejectsInvalidText pins the whitespace case at the
// service boundary, which is where it matters: SQLite's length(trim(x)) strips
// SPACES only, so "\n\t " would reach the database. The service refuses it with
// the keyed ValidationError of the domain and stores nothing, for both fields,
// on creation and on update.
func TestCannedReplyServiceRejectsInvalidText(t *testing.T) {
	svc, replies, desks, clock := newCannedReplyHarness()
	ctx := context.Background()
	agent := cannedReplyAgent(1)
	admin := domain.User{ID: 2, Role: domain.RoleAdmin}
	desk := desks.create("Support")
	existing := replies.seed(domain.CannedReply{OwnerUserID: ptr(agent.ID), Title: "Keep", Body: "Keep", CreatedAt: clock.Now(), UpdatedAt: clock.Now()})

	cases := []struct {
		name        string
		title, body string
		field, key  string
		message     string
	}{
		{"whitespace-only title", "\n\t ", "body", "title", "error.canned_reply_title_length", domain.ErrMsgCannedReplyTitleLength},
		{"title over the limit", strings.Repeat("a", domain.MaxCannedReplyTitleRunes+1), "body", "title", "error.canned_reply_title_length", domain.ErrMsgCannedReplyTitleLength},
		{"whitespace-only body", "Title", "\n\t ", "body", "error.canned_reply_body_length", domain.ErrMsgCannedReplyBodyLength},
		{"body over the limit", "Title", strings.Repeat("b", domain.MaxCannedReplyBodyRunes+1), "body", "error.canned_reply_body_length", domain.ErrMsgCannedReplyBodyLength},
	}
	for _, tc := range cases {
		before := replies.mutations
		refusals := map[string]error{}
		_, refusals["personal create"] = svc.CreatePersonal(ctx, agent, tc.title, tc.body)
		_, refusals["desk create"] = svc.CreateForDesk(ctx, admin, desk.ID, tc.title, tc.body)
		_, refusals["personal update"] = svc.UpdatePersonal(ctx, agent, existing.ID, tc.title, tc.body)
		for operation, err := range refusals {
			var validation *domain.ValidationError
			if !errors.As(err, &validation) {
				t.Fatalf("%s (%s) = %v, want a ValidationError", tc.name, operation, err)
			}
			if validation.Field != tc.field || validation.Key != tc.key || validation.Error() != tc.message {
				t.Fatalf("%s (%s) = %+v, want field %q key %q message %q", tc.name, operation, validation, tc.field, tc.key, tc.message)
			}
		}
		if replies.mutations != before {
			t.Fatalf("%s (%s) reached the store", tc.name, "refused writes")
		}
	}
}
