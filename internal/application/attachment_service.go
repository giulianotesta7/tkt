package application

import (
	"context"
	"errors"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// AttachmentService serves attachment downloads (issue #366, S3). Writing a
// file is part of creating a ticket or a comment; this service only reads.
type AttachmentService struct {
	tickets     TicketStore
	attachments AttachmentStore
}

// NewAttachmentService builds the download use case.
func NewAttachmentService(tickets TicketStore, attachments AttachmentStore) *AttachmentService {
	return &AttachmentService{tickets: tickets, attachments: attachments}
}

// Download returns one attachment with its data when the actor may have it:
// the ticket must be inside the actor's READ scope (the same scope the ticket
// detail applies), and a file on an internal comment additionally needs the
// capability that reads internal comments. Every refusal — a missing file, a
// ticket out of scope, an internal file for a requester — is the same
// NotFoundError, so a download never reveals that a file exists. Any other
// store failure propagates as itself, never disguised as a missing file.
func (s *AttachmentService) Download(ctx context.Context, actor domain.User, id int64) (*StoredAttachment, error) {
	notFound := &domain.NotFoundError{Kind: "attachment", ID: id}
	a, err := s.attachments.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if _, err := s.tickets.GetByID(ctx, a.TicketID, readQuery(actor, TicketQuery{})); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, notFound
		}
		return nil, err
	}
	if a.Visibility == domain.CommentInternal && !NewPolicy().Capabilities(actor.Role).Require(CapCommentInternal) {
		return nil, notFound
	}
	return a, nil
}
