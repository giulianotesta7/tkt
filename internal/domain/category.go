package domain

import "time"

// Category is a managed category referenced by tickets (category-management
// spec). Names are unique.
type Category struct {
	ID          int64
	Name        string
	Description string
	// Keywords are the words an administrator says requesters use for this
	// category (issue #416, S4): one line, comma separated, exactly as typed and
	// trimmed at the ends. They are a search aid for the local category
	// suggestion engine and are never shown to a requester, so they are not
	// presentation copy. The store persists them verbatim; the application layer
	// owns the bound.
	Keywords  string
	DeskID    int64
	CreatedAt time.Time
}
