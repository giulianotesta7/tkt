package httpadapter

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
	"github.com/giulianotesta7/tkt/internal/i18n"
)

// Following and mentioning at the HTTP layer (issue #418).
//
// Following is a staff member's own subscription to a ticket's events, so the
// two routes are ordinary detail mutations: HTMX swaps #ticket-detail, and a
// plain form post redirects back to the ticket. Nobody else can reach them —
// an actor whose role cannot work inside a ticket is refused by role, and a
// staff member the ticket is out of reach for receives the ticket's own
// not-found, because the use case applies the same read scope the detail route
// does. Neither answer says whether a ticket exists.
//
// The same read is what fills the detail's follow control and its followers
// row, and the candidate list both mention inputs share: the one list
// application.FollowService.MentionCandidates answers for this ticket.
const (
	// followersShownInRow is how many person marks the Properties row paints
	// before it sums the rest up as a count. Four fit the column beside the
	// label at every width the page supports.
	followersShownInRow = 4
	followRouteName     = "follow"
	unfollowRouteName   = "unfollow"
)

// detailFollow is the header's follow control: the state it shows, the one
// route a click posts to, and the label the button reads as.
type detailFollow struct {
	Following bool
	Action    string
	Label     string
}

// detailFollower is one person in the followers row: the mark, the name, and
// whether the reader is that person, so the row can say "You".
type detailFollower struct {
	Initials string
	Name     string
	You      bool
}

// detailFollowers is the Properties row: everybody following the ticket, the
// marks the collapsed row paints, and how many people they stand for.
type detailFollowers struct {
	People  []detailFollower
	Avatars []detailFollower
	More    int
}

// mentionPickerView is the composer's candidate list, already narrowed to the
// staff who can open this ticket. Both mention inputs render this one list, so
// they can never offer different people.
type mentionPickerView struct {
	Candidates []mentionCandidate
}

// mentionCandidate is one person the comment may name: the display name the
// mention grammar matches, the mark, and the role (with the desks the person
// belongs to) the list reads as a subtitle.
type mentionCandidate struct {
	Name     string
	Initials string
	Subtitle string
}

// registerFollows mounts the follow routes. A handler built without the follow
// service registers nothing, so an unwired handler answers the mux's 404
// instead of panicking on a nil use case.
func (h *TicketHandlers) registerFollows(mux *http.ServeMux) {
	mux.HandleFunc("POST /tickets/{id}/"+followRouteName, h.follow)
	mux.HandleFunc("POST /tickets/{id}/"+unfollowRouteName, h.unfollow)
}

// follow subscribes the actor to the ticket's events.
func (h *TicketHandlers) follow(w http.ResponseWriter, r *http.Request) {
	h.changeSubscription(w, r, true)
}

// unfollow drops the actor's subscription.
func (h *TicketHandlers) unfollow(w http.ResponseWriter, r *http.Request) {
	h.changeSubscription(w, r, false)
}

// changeSubscription applies one follow or unfollow request. A requester is
// refused by role before the ticket is read, so the answer is about the role
// and never about the ticket. Everything else answers like the use case does:
// the ticket's not-found for a staff member it is out of reach for, and a
// re-rendered detail carrying the message for a genuine refusal.
func (h *TicketHandlers) changeSubscription(w http.ResponseWriter, r *http.Request, subscribe bool) {
	id, ok := ticketID(r)
	if !ok {
		httpError(w, r, "error.invalid_ticket_id", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		httpError(w, r, "error.internal_server", http.StatusInternalServerError)
		return
	}
	actor := *userFromContext(r.Context())
	if !application.NewPolicy().Capabilities(actor.Role).Require(application.CapCommentInternal) {
		h.renderDetailError(w, r, id, domain.NewForbiddenError(application.ErrMsgFollowersStaffOnly))
		return
	}
	var err error
	if subscribe {
		err = h.follows.Follow(r.Context(), actor, id)
	} else {
		err = h.follows.Unfollow(r.Context(), actor, id)
	}
	if err != nil {
		h.renderDetailError(w, r, id, err)
		return
	}
	h.afterMutation(w, r, id, "ticket_detail")
}

// followControl builds the header control for one actor's state.
func followControl(ticketID int64, following bool, tr i18n.Translator) *detailFollow {
	if following {
		return &detailFollow{
			Following: true,
			Action:    "/tickets/" + strconv.FormatInt(ticketID, 10) + "/" + unfollowRouteName,
			Label:     tr.T("tickets.follow.following"),
		}
	}
	return &detailFollow{
		Action: "/tickets/" + strconv.FormatInt(ticketID, 10) + "/" + followRouteName,
		Label:  tr.T("tickets.follow.follow"),
	}
}

// followersRow builds the Properties row from the people following the ticket,
// ordered as the store returns them (display name, then id). The reader is
// marked so the row can say "You" beside their own name.
func followersRow(people []domain.User, actor domain.User) *detailFollowers {
	row := &detailFollowers{People: make([]detailFollower, 0, len(people))}
	for _, person := range people {
		row.People = append(row.People, detailFollower{
			Initials: initialsOf(person.Name),
			Name:     person.Name,
			You:      person.ID == actor.ID,
		})
	}
	shown := len(row.People)
	if shown > followersShownInRow {
		shown = followersShownInRow
	}
	row.Avatars = row.People[:shown]
	row.More = len(row.People) - shown
	return row
}

// mentionListFor builds the composer's candidate list. The subtitle is the
// person's role and, when they belong to any, the desks they work on, which is
// what tells two colleagues with the same first name apart.
func mentionListFor(people []domain.User, deskNames map[int64][]string, tr i18n.Translator) *mentionPickerView {
	view := &mentionPickerView{Candidates: make([]mentionCandidate, 0, len(people))}
	for _, person := range people {
		view.Candidates = append(view.Candidates, mentionCandidate{
			Name:     person.Name,
			Initials: initialsOf(person.Name),
			Subtitle: mentionSubtitle(person, deskNames, tr),
		})
	}
	return view
}

// mentionSubtitle renders one candidate's subtitle: the role label, then the
// desks the person belongs to, in the order the desk list carries them.
func mentionSubtitle(person domain.User, deskNames map[int64][]string, tr i18n.Translator) string {
	role := tr.T("label.role." + string(person.Role))
	desks := deskNames[person.ID]
	if len(desks) == 0 {
		return role
	}
	subtitle := role
	for _, name := range desks {
		subtitle += " · " + name
	}
	return subtitle
}

// deskNamesByUser reads which desks each person belongs to, so a candidate can
// say where they work. It is one read of the desk list plus one read per desk,
// which is the whole catalog of desks and not one query per candidate, and it
// only runs when the composer offers mentions.
func (h *TicketHandlers) deskNamesByUser(ctx context.Context) (map[int64][]string, error) {
	names := map[int64][]string{}
	if h.desks == nil {
		return names, nil
	}
	desks, err := h.desks.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, desk := range desks {
		members, err := h.desks.ListMembers(ctx, desk.ID)
		if err != nil {
			return nil, err
		}
		for _, member := range members {
			names[member.ID] = append(names[member.ID], desk.Name)
		}
	}
	return names, nil
}

// followStateFor reads the follow facts one detail render shows: the header
// control and the followers row for an actor who can already open the ticket,
// and nothing at all for a staff member the ticket is out of reach for. A read
// failure other than the ticket's own not-found is reported, because a page
// that silently dropped its controls would hide a real error.
func (h *TicketHandlers) followStateFor(r *http.Request, actor domain.User, ticketID int64) (*detailFollow, *detailFollowers, error) {
	if h.follows == nil {
		return nil, nil, nil
	}
	following, err := h.follows.IsFollowing(r.Context(), actor, ticketID)
	if err != nil {
		var notFound *domain.NotFoundError
		if errors.As(err, &notFound) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	people, err := h.follows.Followers(r.Context(), actor, ticketID)
	if err != nil {
		var notFound *domain.NotFoundError
		if errors.As(err, &notFound) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	return followControl(ticketID, following, i18n.New(requestLocale(r))), followersRow(people, actor), nil
}

// mentionListOf reads the candidate list the composer offers for this ticket.
func (h *TicketHandlers) mentionListOf(r *http.Request, actor domain.User, ticketID int64) (*mentionPickerView, error) {
	if h.follows == nil {
		return nil, nil
	}
	people, err := h.follows.MentionCandidates(r.Context(), actor, ticketID)
	if err != nil {
		return nil, err
	}
	deskNames, err := h.deskNamesByUser(r.Context())
	if err != nil {
		return nil, err
	}
	return mentionListFor(people, deskNames, i18n.New(requestLocale(r))), nil
}
