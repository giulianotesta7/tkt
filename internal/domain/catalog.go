package domain

// CatalogCategory is a category with its catalog context: where it sits in the
// folder tree (issue #484). A category names no desk; its workflow steps decide
// which desk is responsible (issue #491).
type CatalogCategory struct {
	Category
	// FolderID is the folder the category sits in, nil at the top level, and
	// FolderPath the folder names from the top level down to it.
	FolderID   *int64
	FolderPath []string
}

// CatalogDesk is a Desk with the facts the desks screen shows about it.
type CatalogDesk struct {
	Desk
	// CategoryCount counts distinct categories whose current published workflow
	// has an assignment step targeting this desk. Drafts and old versions do not count.
	CategoryCount int
	// OfferedCategoryCount uses the same current-publication set as CategoryCount.
	OfferedCategoryCount int
	// AgentCount is the number of members who could actually take a ticket on
	// this desk — active, agent or above — which is the same predicate the
	// workflow publish gate applies before it lets a definition ship. A desk that
	// reports zero here cannot run an assign-to-desk step, so the catalog shows
	// it as needing attention instead of as a healthy desk (issue #387).
	AgentCount int
	// OpenTickets uses the active pinned claim or latest workflow assignment,
	// never category ownership, to count tickets that are still work
	// somebody owes: not closed and not cancelled. It is resolved for every desk
	// in the same read as the rest of the projection.
	OpenTickets int
}
