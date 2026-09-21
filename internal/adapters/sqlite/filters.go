package sqlite

import (
	"strings"

	"github.com/giulianotesta7/tkt/internal/application"
)

// createdTiebreak is the shared D2 created/id tiebreak: newest first by
// creation with a stable id DESC so page boundaries never overlap.
// created_at is ISO-8601 UTC TEXT (D7), which sorts lexicographically in
// chronological order. Every ordering composes this one fragment so the
// tiebreak cannot drift between paths.
const createdTiebreak = "t.created_at DESC, t.id DESC"

// orderByCreatedDesc is the deterministic list ordering (D2).
const orderByCreatedDesc = "ORDER BY " + createdTiebreak

// orderByPriorityDesc is the D11 priority ordering: critical(4) > high(3) >
// medium(2) > low(1) via the shared CASE fragment, with the created/id
// tiebreak kept so priority-sorted pages stay stable and non-overlapping.
const orderByPriorityDesc = "ORDER BY " + priorityOrderCASE + " DESC, " + createdTiebreak

// priorityOrderCASE ranks priorities for SQL ordering (D11): critical=4,
// high=3, medium=2, low=1. It is the single shared SQL fragment constant in
// the adapter — no schema duplication, CHECK keeps values honest. The
// list/search ports currently order by created_at DESC, id DESC (D2), so no
// live query references the fragment yet; it is the shared constant the
// priority-sort path uses once a sort key exists.
const priorityOrderCASE = "CASE t.priority WHEN 'critical' THEN 4 WHEN 'high' THEN 3 WHEN 'medium' THEN 2 WHEN 'low' THEN 1 ELSE 0 END"

// slaResponseAchieved is the first-response milestone achievement predicate
// (issue #211): a public staff comment exists. It wraps the milestone
// semantics shared with the SLA store (slaFirstResponseWhere) in the EXISTS
// shape an ORDER BY expression needs — never a second copy of the rule.
const slaResponseAchieved = "EXISTS (SELECT 1 FROM comments WHERE ticket_id = t.id AND " + slaFirstResponseWhere + ")"

// slaResolutionAchieved is the first-resolution milestone achievement
// predicate (issue #211): a resolved-state audit transition exists. It wraps
// slaFirstResolvedWhere, the single shared definition of the rule.
const slaResolutionAchieved = "EXISTS (SELECT 1 FROM audit_events WHERE ticket_id = t.id AND " + slaFirstResolvedWhere + ")"

// slaOutstandingDeadlineCASE is the urgency order key (issue #211, PR 4):
// the due instant of the FIRST NOT-ACHIEVED milestone. A ticket that is not
// open has nothing outstanding, so closed work stays last even with an
// already-past due; the migration's DEFAULT is the empty string, which
// counts as absent via NULLIF,
// and NULL (no frozen row, or both milestones achieved) also sorts last.
// Ordering the instants ASC surfaces the ticket that must be worked next.
const slaOutstandingDeadlineCASE = "CASE" +
	" WHEN t.state NOT IN ('new','in_progress') THEN NULL" +
	" WHEN NOT " + slaResponseAchieved + " THEN NULLIF(s.due_first_response_at,'')" +
	" WHEN NOT " + slaResolutionAchieved + " THEN NULLIF(s.due_resolve_at,'')" +
	" ELSE NULL END"

// orderByUrgency is the SLA urgency ordering (issue #211, PR 4): the
// outstanding deadline ASC with nothing-outstanding tickets last, then the
// D11 priority rank DESC and the D2 created/id tiebreak. The priority rank
// is already urgency's tiebreak, so SortByUrgency wins when both flags are
// set.
const orderByUrgency = "ORDER BY " + slaOutstandingDeadlineCASE + " IS NULL, " + slaOutstandingDeadlineCASE + " ASC, " + priorityOrderCASE + " DESC, " + createdTiebreak

// orderBy returns the ORDER BY clause for q: the SLA urgency ordering when
// SortByUrgency is set (its documented precedence), else the D11 priority
// ordering when SortByPriority is set, else the deterministic D2
// newest-first order.
func orderBy(q application.TicketQuery) string {
	if q.SortByUrgency {
		return orderByUrgency
	}
	if q.SortByPriority {
		return orderByPriorityDesc
	}
	return orderByCreatedDesc
}

// listFrom returns the FROM clause for q. The ticket_sla join is injected
// ONLY for the urgency ordering — every other path keeps the bare tickets
// table and is byte-identical to before.
func listFrom(q application.TicketQuery) string {
	if q.SortByUrgency {
		return "tickets t LEFT JOIN ticket_sla s ON s.ticket_id = t.id"
	}
	return "tickets t"
}

// claimableClause is the existing READ-only claim exception: an active run's
// current pinned step is assign_to_desk[claim] on a desk containing actorID.
func claimableClause(actorID int64) (string, []any) {
	return `EXISTS (
		SELECT 1 FROM ticket_workflow_runs r
		JOIN workflow_versions wv ON wv.id = t.workflow_version_id
		WHERE r.ticket_id = t.id AND r.status = 'active'
		  AND wv.category_id = t.category_id
		  AND json_extract(wv.steps_json, '$[' || r.current_step_index || '].type') = 'assign_to_desk'
		  AND json_extract(wv.steps_json, '$[' || r.current_step_index || '].assign_to_desk.strategy') = 'claim'
		  AND CAST(json_extract(wv.steps_json, '$[' || r.current_step_index || '].assign_to_desk.desk_id') AS INTEGER) IN (
			SELECT dm.desk_id FROM desk_members dm WHERE dm.user_id = ?
		)
	)`, []any{actorID}
}

// scopeClause returns the actor-scope WHERE fragment from q (ticket-access
// spec): requester = self for ScopeOwned, assignee = self for ScopeAssigned,
// the full queue for ScopeAll, the agent assignment scope (self OR
// unassigned) for ScopeAssignable, and an impossible predicate for
// ScopeNone — the zero value fails closed, so an unscoped query can never
// leak rows.
func scopeClause(q application.TicketQuery) (string, []any) {
	switch q.Scope {
	case application.ScopeOwned:
		return "t.requester_user_id = ?", []any{q.ActorID}
	case application.ScopeAssigned:
		return "t.user_id = ?", []any{q.ActorID}
	case application.ScopeAssignable:
		return "(t.user_id = ? OR t.user_id IS NULL)", []any{q.ActorID}
	case application.ScopeAssignedOrClaimable:
		claimable, args := claimableClause(q.ActorID)
		return "(t.user_id = ? OR " + claimable + ")", append([]any{q.ActorID}, args...)
	case application.ScopeAll:
		return "", nil
	default:
		return "0 = 1", nil
	}
}

// buildTicketWhere composes the AND filter clauses from q (ticket-search
// spec): actor scope first (the base restriction), then state, priority,
// category, and assigned user. An empty filter set returns only the scope
// restriction — never a plain full-table list. The FTS text clause (0002)
// is added here by the search store.
func buildTicketWhere(q application.TicketQuery) (string, []any) {
	var clauses []string
	var args []any
	if clause, scopeArgs := scopeClause(q); clause != "" {
		clauses = append(clauses, clause)
		args = append(args, scopeArgs...)
	}
	if q.State != nil {
		clauses = append(clauses, "t.state = ?")
		args = append(args, string(*q.State))
	}
	if len(q.States) > 0 {
		states := make([]string, 0, len(q.States))
		for _, state := range q.States {
			states = append(states, "?")
			args = append(args, string(state))
		}
		clauses = append(clauses, "t.state IN ("+strings.Join(states, ",")+")")
	}
	if q.Section == application.TicketSectionPersonal {
		clauses = append(clauses, "t.user_id = ?")
		args = append(args, q.ActorID)
	}
	if q.Section == application.TicketSectionClaimable {
		claimable, claimArgs := claimableClause(q.ActorID)
		clauses = append(clauses, "t.user_id IS NULL", claimable)
		args = append(args, claimArgs...)
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
	// Text clause (FTS5, 0002): tickets whose TITLE rowid matches the
	// D4-tokenized, title-scoped expression, OR whose exact ticket number
	// (TKT-N) matches one of the extracted IDs — the search box scope is
	// ID or title only. Empty text adds no clause — a plain filter list.
	// The clause is shared by list, count, chips, and search queries so
	// pagination boundaries and chip counts always reflect the same
	// filtered set.
	var textOR []string
	if q.Text != "" {
		textOR = append(textOR, "t.id IN (SELECT rowid FROM tickets_fts WHERE tickets_fts MATCH ?)")
		args = append(args, q.Text)
	}
	if len(q.Numbers) > 0 {
		nums := make([]string, 0, len(q.Numbers))
		for _, n := range q.Numbers {
			nums = append(nums, "?")
			args = append(args, n)
		}
		textOR = append(textOR, "t.number IN ("+strings.Join(nums, ",")+")")
	}
	if len(textOR) > 0 {
		clauses = append(clauses, "("+strings.Join(textOR, " OR ")+")")
	}
	if len(clauses) == 0 {
		return "", nil
	}
	return "WHERE " + strings.Join(clauses, " AND "), args
}
