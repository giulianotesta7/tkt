package application

import (
	"context"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// TicketVisitService implements the "what is new since the last visit" use
// cases (issue #415, specs S1-S3 and S6). It is composition only: the history
// derivation and the same-transaction notification read live in
// TicketVisitStore, and the two rules that are policy rather than storage
// live here — which comments a reader may compare against, and which instant a
// visit is stamped with.
type TicketVisitService struct {
	visits TicketVisitStore
	clock  domain.Clock
}

// NewTicketVisitService wires the use cases against the visit port and the
// injected clock (D7). The clock is read only when a visit is recorded, so one
// stored visit carries one instant.
func NewTicketVisitService(visits TicketVisitStore, clock domain.Clock) *TicketVisitService {
	return &TicketVisitService{visits: visits, clock: clock}
}

// Mark returns one ticket's current history mark. The caller takes it BEFORE
// the authorized ticket read and records the visit only after that read
// succeeds, so anything written while the ticket was being read stays new
// instead of being swallowed by the visit.
func (s *TicketVisitService) Mark(ctx context.Context, ticketID int64) (VisitMark, error) {
	return s.visits.Mark(ctx, ticketID)
}

// RecordVisit stores the reader's visit at ONE clock instant and, in the
// store's same transaction, marks that reader's notifications about the ticket
// read (S6). The actor is the reader: a visit is always the caller's own, so
// no visitor can record a visit for somebody else.
func (s *TicketVisitService) RecordVisit(ctx context.Context, actor domain.User, ticketID int64, mark VisitMark) error {
	return s.visits.RecordVisit(ctx, actor.ID, ticketID, mark, s.clock.Now())
}

// Changed reports which of ticketIDs changed for the actor since their last
// visit. Internal comments count only for a reader whose role holds
// CapCommentInternal (S2), which is the same capability the comment form and
// the timeline already gate on. An empty page answers an empty map without
// reaching the store.
func (s *TicketVisitService) Changed(ctx context.Context, actor domain.User, ticketIDs []int64) (map[int64]bool, error) {
	if len(ticketIDs) == 0 {
		return map[int64]bool{}, nil
	}
	includeInternal := NewPolicy().Capabilities(actor.Role).Require(CapCommentInternal)
	return s.visits.Changed(ctx, actor.ID, ticketIDs, includeInternal)
}
