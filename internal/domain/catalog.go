package domain

import "time"

// Department is the top-level organizational grouping in the ticket catalog.
type Department struct {
	ID          int64
	Name        string
	Description string
	CreatedAt   time.Time
}

// CatalogCategory is a category with its resolved hierarchy context.
type CatalogCategory struct {
	Category
	DeskName       string
	DepartmentID   int64
	DepartmentName string
}

// CatalogDepartment is a department and its child/category counts.
type CatalogDepartment struct {
	Department
	DeskCount     int
	CategoryCount int
	// OfferedCategoryCount is the number of the department's categories that
	// have a published version: the requester picker offers only those, while
	// CategoryCount stays the total the admin structure lists.
	OfferedCategoryCount int
}

// CatalogDesk is a Desk with its category count and resolved Department name.
type CatalogDesk struct {
	Desk
	DepartmentID   int64
	CategoryCount  int
	DepartmentName string
	// OfferedCategoryCount is the number of the desk's categories that have a
	// published version: the requester picker offers only those, while
	// CategoryCount stays the total the admin structure lists.
	OfferedCategoryCount int
	// AgentCount is the number of members who could actually take a ticket on
	// this desk — active, agent or above — which is the same predicate the
	// workflow publish gate applies before it lets a definition ship. A desk that
	// reports zero here cannot run an assign-to-desk step, so the catalog tree
	// shows it as needing attention instead of as a healthy desk (issue #387).
	AgentCount int
	// OpenTickets is the number of the desk's tickets that are still work
	// somebody owes: not closed and not cancelled. It is resolved for every desk
	// in the same read as the rest of the projection.
	OpenTickets int
}
