package domain

import "time"

// Comment is a single entry on a ticket's chronological, append-only timeline
// (comment-timeline spec). Author is the logged-in user taken from the
// session (D14); the domain never fills it.
type Comment struct {
	ID        int64
	TicketID  int64
	Author    string
	Body      string
	CreatedAt time.Time
}
