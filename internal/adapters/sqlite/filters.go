package sqlite

import (
	"strings"

	"github.com/giulianotesta7/tkt/internal/application"
)

// orderByCreatedDesc is the deterministic list ordering (D2): newest first
// by creation, stable id DESC tiebreaker so page boundaries never overlap.
// created_at is ISO-8601 UTC TEXT (D7), which sorts lexicographically in
// chronological order.
const orderByCreatedDesc = "ORDER BY t.created_at DESC, t.id DESC"

// priorityOrderCASE ranks priorities for SQL ordering (D11): critical=4,
// high=3, medium=2, low=1. It is the single shared SQL fragment constant in
// the adapter — no schema duplication, CHECK keeps values honest. The
// list/search ports currently order by created_at DESC, id DESC (D2), so no
// live query references the fragment yet; it is the shared constant the
// priority-sort path uses once a sort key exists.
const priorityOrderCASE = "CASE t.priority WHEN 'critical' THEN 4 WHEN 'high' THEN 3 WHEN 'medium' THEN 2 WHEN 'low' THEN 1 ELSE 0 END"

// buildTicketWhere composes the AND filter clauses from q (ticket-search
// spec): state, priority, category, and assigned user. An empty filter set
// returns "" with no args — the plain list of all tickets. The FTS text
// clause (0002) is added here by the search store.
func buildTicketWhere(q application.TicketQuery) (string, []any) {
	var clauses []string
	var args []any
	if q.State != nil {
		clauses = append(clauses, "t.state = ?")
		args = append(args, string(*q.State))
	}
	if q.Priority != nil {
		clauses = append(clauses, "t.priority = ?")
		args = append(args, string(*q.Priority))
	}
	if q.CategoryID != nil {
		clauses = append(clauses, "t.category_id = ?")
		args = append(args, *q.CategoryID)
	}
	if q.UserID != nil {
		clauses = append(clauses, "t.user_id = ?")
		args = append(args, *q.UserID)
	}
	// Text clause (FTS5, 0002): tickets whose rowid is matched by the
	// D4-tokenized expression (design "Search / Filters"). Empty text adds
	// no clause — a plain filter list. The clause is shared by list,
	// count, chips, and search queries so pagination boundaries and chip
	// counts always reflect the same filtered set.
	if q.Text != "" {
		clauses = append(clauses, "t.id IN (SELECT rowid FROM tickets_fts WHERE tickets_fts MATCH ?)")
		args = append(args, q.Text)
	}
	if len(clauses) == 0 {
		return "", nil
	}
	return "WHERE " + strings.Join(clauses, " AND "), args
}
