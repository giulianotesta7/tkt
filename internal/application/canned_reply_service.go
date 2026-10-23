package application

import (
	"context"
	"errors"
	"sort"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// Authorization messages for saved replies (issue #418). Both
// refusals are 403: the actor may be able to read the screen, so the answer is
// "not yours to do" rather than "does not exist".
const (
	// ErrMsgCannedRepliesStaffOnly rejects every saved-reply operation for a
	// requester: saved replies are a staff tool, and the capability is
	// the one that already means "this role works inside the ticket".
	ErrMsgCannedRepliesStaffOnly = "saved replies are only available to staff"
	// ErrMsgCannedReplyDeskManagement rejects an agent who tries to change a
	// desk's shared replies: members USE them, desk management MAINTAINS them
	// (the CapManageDesks rule).
	ErrMsgCannedReplyDeskManagement = "only an administrator can change a desk's saved replies"
)

// CannedReplyGroup is one desk's shared replies on either screen — the saved
// replies manager and the composer's picker. DeskName is carried so a renderer
// never has to look a desk up, and Editable says whether THIS actor may change
// the group: true only for a desk listed on the manager screen by an actor
// holding CapManageDesks.
type CannedReplyGroup struct {
	DeskID   int64
	DeskName string
	Editable bool
	Replies  []domain.CannedReply
}

// CannedReplyManagerView is the "Saved replies" screen: the actor's own
// replies, which are always theirs to edit, plus one group per desk they are
// allowed to see on that screen. Bodies are returned as authored, because this
// screen edits them.
type CannedReplyManagerView struct {
	Mine  []domain.CannedReply
	Desks []CannedReplyGroup
}

// CannedReplyPickerView is the composer's saved-reply picker: the replies this
// actor may insert into THIS ticket, grouped exactly like the manager screen but
// narrowed to what they can use, and with the placeholders already filled.
// Every group is read-only — the picker inserts text, it does not edit replies —
// so Editable is always false here.
type CannedReplyPickerView struct {
	Mine  []domain.CannedReply
	Desks []CannedReplyGroup
}

// CannedReplyService implements the saved-reply use cases (issue #418). There
// are two kinds of reply and they are authorized differently:
//
//   - a PERSONAL reply belongs to one person. Any staff member creates, edits
//     and deletes their own; another person's personal reply is indistinguishable
//     from a missing one (NotFound, never Forbidden), so the service leaks no
//     existence.
//   - a DESK reply belongs to one desk and every member of that desk may USE it,
//     while only an actor holding CapManageDesks may change it.
//
// Every operation requires CapCommentInternal: saved replies exist to write
// staff comments, and a requester has no business in either listing.
//
// The service stamps CreatedAt/UpdatedAt from the injected clock and validates
// through domain.CannedReply.Validate, which trims the text, enforces the same
// bounds as the canned_replies CHECK and rejects whitespace-only text that
// SQLite's trim() would accept. The trimmed text is what gets stored.
type CannedReplyService struct {
	replies CannedReplyStore
	desks   DeskStore
	clock   domain.Clock
}

// NewCannedReplyService wires the saved-reply use cases against the given
// ports. desks is read for existence and membership only; nothing here writes
// a desk.
func NewCannedReplyService(replies CannedReplyStore, desks DeskStore, clock domain.Clock) *CannedReplyService {
	return &CannedReplyService{replies: replies, desks: desks, clock: clock}
}

// CreatePersonal stores a reply owned by the acting staff member.
func (s *CannedReplyService) CreatePersonal(ctx context.Context, actor domain.User, title, body string) (*domain.CannedReply, error) {
	if err := requireCannedReplyStaff(actor); err != nil {
		return nil, err
	}
	now := s.clock.Now()
	reply := &domain.CannedReply{OwnerUserID: &actor.ID, Title: title, Body: body, CreatedAt: now, UpdatedAt: now}
	if err := reply.Validate(); err != nil {
		return nil, err
	}
	if err := s.replies.Create(ctx, reply); err != nil {
		return nil, err
	}
	return reply, nil
}

// UpdatePersonal replaces the text of the actor's OWN personal reply. A reply
// owned by somebody else — and a desk reply — answers NotFound, so the refusal
// tells the caller nothing about what exists.
func (s *CannedReplyService) UpdatePersonal(ctx context.Context, actor domain.User, id int64, title, body string) (*domain.CannedReply, error) {
	if err := requireCannedReplyStaff(actor); err != nil {
		return nil, err
	}
	reply, err := s.ownPersonalReply(ctx, actor, id)
	if err != nil {
		return nil, err
	}
	return s.saveText(ctx, reply, title, body)
}

// DeletePersonal removes the actor's OWN personal reply, answering NotFound for
// anything that is not theirs.
func (s *CannedReplyService) DeletePersonal(ctx context.Context, actor domain.User, id int64) error {
	if err := requireCannedReplyStaff(actor); err != nil {
		return err
	}
	if _, err := s.ownPersonalReply(ctx, actor, id); err != nil {
		return err
	}
	return s.replies.Delete(ctx, id)
}

// CreateForDesk stores a shared reply owned by one desk. Only desk management
// may do it, and the desk must exist.
func (s *CannedReplyService) CreateForDesk(ctx context.Context, actor domain.User, deskID int64, title, body string) (*domain.CannedReply, error) {
	if err := requireCannedReplyDeskManagement(actor); err != nil {
		return nil, err
	}
	if _, err := s.desks.GetByID(ctx, deskID); err != nil {
		return nil, err
	}
	now := s.clock.Now()
	reply := &domain.CannedReply{DeskID: &deskID, Title: title, Body: body, CreatedAt: now, UpdatedAt: now}
	if err := reply.Validate(); err != nil {
		return nil, err
	}
	if err := s.replies.Create(ctx, reply); err != nil {
		return nil, err
	}
	return reply, nil
}

// UpdateForDesk replaces the text of one desk's shared reply. Only desk
// management may do it; a personal reply is not a desk reply and answers
// NotFound.
func (s *CannedReplyService) UpdateForDesk(ctx context.Context, actor domain.User, id int64, title, body string) (*domain.CannedReply, error) {
	if err := requireCannedReplyDeskManagement(actor); err != nil {
		return nil, err
	}
	reply, err := s.deskReply(ctx, id)
	if err != nil {
		return nil, err
	}
	// The desk must still exist: a reply cannot outlive its desk (the table
	// cascades), and re-checking here keeps the refusal a NotFound instead of a
	// foreign-key failure from the write.
	if _, err := s.desks.GetByID(ctx, *reply.DeskID); err != nil {
		return nil, err
	}
	return s.saveText(ctx, reply, title, body)
}

// DeleteForDesk removes one desk's shared reply. Only desk management may do
// it; a personal reply answers NotFound.
func (s *CannedReplyService) DeleteForDesk(ctx context.Context, actor domain.User, id int64) error {
	if err := requireCannedReplyDeskManagement(actor); err != nil {
		return err
	}
	if _, err := s.deskReply(ctx, id); err != nil {
		return err
	}
	return s.replies.Delete(ctx, id)
}

// Manager answers the "Saved replies" screen for the actor the caller
// authorised. Mine is the actor's own list, always editable. Desks holds one
// group per desk the actor may see there: an agent sees the desks they are a
// MEMBER of, read-only, and an actor holding CapManageDesks sees every desk,
// editable — which is how the same screen serves "my replies" and "maintain a
// desk's replies" without a second route.
//
// The groups are ordered by desk name (then id) and each group's replies by
// title, so the screen is deterministic.
//
// Simplification: an agent's member desks are resolved by listing the desks and
// keeping the ones whose member list contains the actor. The DeskStore port
// exposes membership per desk and not per person, and the reply read
// (ListUsable) cannot show a desk that has no replies yet — the screen must, so
// an administrator can add the first one. Adding a per-person membership read
// would widen the DeskStore contract for one screen, which saved replies do not
// need.
func (s *CannedReplyService) Manager(ctx context.Context, actor domain.User) (*CannedReplyManagerView, error) {
	if err := requireCannedReplyStaff(actor); err != nil {
		return nil, err
	}
	mine, err := s.replies.ListPersonal(ctx, actor.ID)
	if err != nil {
		return nil, err
	}
	view := &CannedReplyManagerView{Mine: mine, Desks: []CannedReplyGroup{}}

	canManage := NewPolicy().Capabilities(actor.Role).Require(CapManageDesks)
	desks, err := s.desks.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, desk := range desks {
		if !canManage {
			member, err := s.isDeskMember(ctx, desk.ID, actor.ID)
			if err != nil {
				return nil, err
			}
			if !member {
				continue
			}
		}
		replies, err := s.replies.ListByDesk(ctx, desk.ID)
		if err != nil {
			return nil, err
		}
		view.Desks = append(view.Desks, CannedReplyGroup{
			DeskID:   desk.ID,
			DeskName: desk.Name,
			Editable: canManage,
			Replies:  replies,
		})
	}
	sort.SliceStable(view.Desks, func(i, j int) bool {
		if view.Desks[i].DeskName != view.Desks[j].DeskName {
			return view.Desks[i].DeskName < view.Desks[j].DeskName
		}
		return view.Desks[i].DeskID < view.Desks[j].DeskID
	})
	return view, nil
}

// ForTicket answers the composer's picker for one ticket: the actor's personal
// replies and the replies of the desks they belong to, in that order, with
// FillPlaceholders applied against the ticket's requester display name and
// readable number.
//
// Membership is decided by ListUsable, so an actor — an administrator included
// — never receives a reply of a desk they do not belong to. Nothing in this
// listing grants ticket access: the caller has already authorised the ticket.
func (s *CannedReplyService) ForTicket(ctx context.Context, actor domain.User, ticket *domain.Ticket) (*CannedReplyPickerView, error) {
	if err := requireCannedReplyStaff(actor); err != nil {
		return nil, err
	}
	if ticket == nil {
		// A programming guard, not a user-visible refusal: there is no picker
		// without a ticket to fill the placeholders from.
		return nil, errors.New("saved replies need a ticket")
	}
	usable, err := s.replies.ListUsable(ctx, actor.ID)
	if err != nil {
		return nil, err
	}
	view := &CannedReplyPickerView{Mine: []domain.CannedReply{}, Desks: []CannedReplyGroup{}}
	groups := map[int64]int{}
	for _, reply := range usable {
		filled := reply
		filled.Body = domain.FillPlaceholders(reply.Body, ticket.RequesterName, ticket.Number)
		if reply.OwnerUserID != nil {
			view.Mine = append(view.Mine, filled)
			continue
		}
		deskID := *reply.DeskID
		index, seen := groups[deskID]
		if !seen {
			// The store orders desk groups by desk name already; the name is
			// only needed once per group, and a missing desk cannot happen (the
			// reply row cascades with it), so an unresolvable name stays empty
			// rather than failing the whole picker.
			name := ""
			if desk, err := s.desks.GetByID(ctx, deskID); err == nil && desk != nil {
				name = desk.Name
			}
			index = len(view.Desks)
			groups[deskID] = index
			view.Desks = append(view.Desks, CannedReplyGroup{DeskID: deskID, DeskName: name})
		}
		view.Desks[index].Replies = append(view.Desks[index].Replies, filled)
	}
	return view, nil
}

// ownPersonalReply reads id and refuses anything that is not the actor's own
// personal reply with NotFound, so no caller can probe somebody else's replies.
func (s *CannedReplyService) ownPersonalReply(ctx context.Context, actor domain.User, id int64) (*domain.CannedReply, error) {
	reply, err := s.replies.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if reply.OwnerUserID == nil || *reply.OwnerUserID != actor.ID {
		return nil, cannedReplyNotFound(id)
	}
	return reply, nil
}

// deskReply reads id and refuses anything that is not a desk reply with
// NotFound: the personal half of the table is addressed by the personal
// operations only.
func (s *CannedReplyService) deskReply(ctx context.Context, id int64) (*domain.CannedReply, error) {
	reply, err := s.replies.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if reply.DeskID == nil {
		return nil, cannedReplyNotFound(id)
	}
	return reply, nil
}

// saveText replaces the editable text of an already-authorised reply: it
// stamps UpdatedAt from the clock, re-validates through the domain (so an edit
// obeys the same bounds as a creation, whitespace-only text included) and only
// then writes. Ownership is never touched — it is not an editable field.
func (s *CannedReplyService) saveText(ctx context.Context, reply *domain.CannedReply, title, body string) (*domain.CannedReply, error) {
	reply.Title, reply.Body = title, body
	reply.UpdatedAt = s.clock.Now()
	if err := reply.Validate(); err != nil {
		return nil, err
	}
	if err := s.replies.Update(ctx, reply); err != nil {
		return nil, err
	}
	return reply, nil
}

// isDeskMember reports whether userID is a member of deskID.
func (s *CannedReplyService) isDeskMember(ctx context.Context, deskID, userID int64) (bool, error) {
	members, err := s.desks.ListMembers(ctx, deskID)
	if err != nil {
		return false, err
	}
	for _, member := range members {
		if member.ID == userID {
			return true, nil
		}
	}
	return false, nil
}

func cannedReplyNotFound(id int64) *domain.NotFoundError {
	return &domain.NotFoundError{Kind: "canned reply", ID: id}
}

// requireCannedReplyStaff is the gate every saved-reply operation passes: only
// a role that may write internal comments works with saved replies.
func requireCannedReplyStaff(actor domain.User) error {
	if !NewPolicy().Capabilities(actor.Role).Require(CapCommentInternal) {
		return domain.NewForbiddenError(ErrMsgCannedRepliesStaffOnly)
	}
	return nil
}

// requireCannedReplyDeskManagement is the gate of the shared half of the
// rule: members use a desk's replies, desk management maintains them.
func requireCannedReplyDeskManagement(actor domain.User) error {
	if !NewPolicy().Capabilities(actor.Role).Require(CapManageDesks) {
		return domain.NewForbiddenError(ErrMsgCannedReplyDeskManagement)
	}
	return nil
}
