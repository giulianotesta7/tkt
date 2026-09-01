package domain

import "time"

// Category is a managed category referenced by tickets (category-management
// spec). Names are unique.
type Category struct {
	ID          int64
	Name        string
	Description string
	AreaID      int64
	CreatedAt   time.Time
}
