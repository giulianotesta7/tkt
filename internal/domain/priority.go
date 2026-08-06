package domain

// Priority of a ticket (ticket-management spec). Rank() is added for
// ordering in the search adapter (D11).
type Priority string

const (
	PriorityBaja    Priority = "baja"
	PriorityMedia   Priority = "media"
	PriorityAlta    Priority = "alta"
	PriorityCritica Priority = "critica"
)

// Rank orders priorities for sorting (D11): critica=4, alta=3, media=2,
// baja=1. Unknown values rank 0 (never persisted — CHECK constraint).
func (p Priority) Rank() int {
	switch p {
	case PriorityCritica:
		return 4
	case PriorityAlta:
		return 3
	case PriorityMedia:
		return 2
	case PriorityBaja:
		return 1
	}
	return 0
}
