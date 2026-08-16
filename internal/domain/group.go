package domain

import "time"

// Group is a named set of agent-plus users. It is deliberately not an
// assignee: ticket assignment persists a person ID only. If group-targeted
// assignment is added later, it must choose the active eligible member with
// the fewest assigned tickets (user-ID tiebreak) and persist that person.
type Group struct {
	ID        int64
	Name      string
	CreatedAt time.Time
}
