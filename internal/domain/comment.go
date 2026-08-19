package domain

import "time"

// CommentVisibility is the audience of a comment (comment-visibility spec):
// public comments are visible to every actor within the ticket's access
// scope; internal comments are staff-only (agent+). Role user SHALL create
// and read only public comments.
type CommentVisibility string

const (
	// CommentPublic is visible to every actor within ticket scope.
	CommentPublic CommentVisibility = "public"
	// CommentInternal is staff-only (agent+); user-role actors must never
	// receive its content.
	CommentInternal CommentVisibility = "internal"
)

// Valid reports whether v is a legal visibility value. Unknown values are
// invalid — authorization fails closed, never silently.
func (v CommentVisibility) Valid() bool {
	return v == CommentPublic || v == CommentInternal
}

// Comment is a single entry on a ticket's chronological, append-only timeline
// (comment-timeline spec). Author is the logged-in user taken from the
// session (D14); the domain never fills it. Visibility is the comment's
// audience (comment-visibility spec): public or internal.
type Comment struct {
	ID         int64
	TicketID   int64
	Author     string
	Body       string
	Visibility CommentVisibility
	CreatedAt  time.Time
}
