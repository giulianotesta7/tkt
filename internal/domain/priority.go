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
