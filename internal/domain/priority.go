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
