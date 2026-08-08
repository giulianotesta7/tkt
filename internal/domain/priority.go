package domain

// Priority of a ticket (ticket-management spec). Rank() is added for
// ordering in the search adapter (D11).
type Priority string

const (
	PriorityLow      Priority = "low"
	PriorityMedium   Priority = "medium"
	PriorityHigh     Priority = "high"
	PriorityCritical Priority = "critical"
)

// Rank orders priorities for sorting (D11): critical=4, high=3, medium=2,
// low=1. Unknown values rank 0 (never persisted — CHECK constraint).
func (p Priority) Rank() int {
	switch p {
	case PriorityCritical:
		return 4
	case PriorityHigh:
		return 3
	case PriorityMedium:
		return 2
	case PriorityLow:
		return 1
	}
	return 0
}

// IsValidPriority reports whether p is one of the four persisted values.
// Exported for the application layer (D5: single source of truth).
func IsValidPriority(p Priority) bool { return isValidPriority(p) }
