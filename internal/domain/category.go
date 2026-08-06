package domain

import "time"

// Category is a managed category referenced by tickets (category-management
// spec). Names are unique.
type Category struct {
	ID        int64
	Name      string
	CreatedAt time.Time
}
