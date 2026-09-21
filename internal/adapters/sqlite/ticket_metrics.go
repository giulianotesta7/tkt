package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

type ticketMetricsStore struct{ db *sql.DB }

var _ application.TicketMetricsStore = (*ticketMetricsStore)(nil)

func newTicketMetricsStore(db *sql.DB) *ticketMetricsStore { return &ticketMetricsStore{db: db} }

// ticketMetricsQuery builds the statement and its argument list for the
// administrative metrics read. It is package-level so the numbered migration
// test can EXPLAIN the exact statement the store runs instead of a replica
// that could drift; extraction must not change the SQL, the argument order,
// the results, or any error behavior.
//
// The same read also carries the SLA attainment inputs (issue #211): the
// ticket's priority and category name, the frozen commitment from a LEFT JOIN
// on ticket_sla (NULL when the ticket has none), and the two measured
// milestones as correlated subqueries. The milestone subqueries reuse the
// slaFirstResponseWhere / slaFirstResolvedWhere predicates and are deliberately
// NOT period-limited: a first response or resolution outside the window still
// counts for a cohort ticket, unlike the resolved CTE above, which is bounded
// because it answers the different "resolved in period" metric.
func ticketMetricsQuery(f application.TicketMetricsFilter) (string, []any) {
	where := []string{"(resolved.ticket_id IS NOT NULL OR t.state IN ('new', 'in_progress') OR (t.created_at >= ? AND t.created_at < ?))"}
	args := []any{formatTime(f.Start), formatTime(f.End), formatTime(f.Start), formatTime(f.End)}
	if f.DeskID != nil {
		where = append(where, "c.desk_id = ?")
		args = append(args, *f.DeskID)
	}
	if f.AgentID != nil {
		where = append(where, "t.user_id = ?")
		args = append(args, *f.AgentID)
	}
	statement := `
		WITH resolved AS (
			SELECT a.ticket_id, a.created_at, a.id,
			ROW_NUMBER() OVER (PARTITION BY a.ticket_id ORDER BY a.created_at DESC, a.id DESC) AS rn
			FROM audit_events a
			WHERE a.action = 'transition' AND a.field = 'state' AND a.to_value = 'resolved'
			AND a.created_at >= ? AND a.created_at < ?
		)
		SELECT t.id, t.created_at, resolved.created_at, t.state,
		c.desk_id, COALESCE(d.name, ''), t.user_id, COALESCE(u.name, ''),
		t.priority, c.name,
		s.ticket_id, ` + ticketSLAColumns + `,
		(SELECT MIN(cm.created_at) FROM comments cm
			WHERE cm.ticket_id = t.id AND ` + slaFirstResponseWhere + `),
		(SELECT MIN(ae.created_at) FROM audit_events ae
			WHERE ae.ticket_id = t.id AND ` + slaFirstResolvedWhere + `)
		FROM tickets t
		JOIN categories c ON c.id = t.category_id
		LEFT JOIN desks d ON d.id = c.desk_id
		LEFT JOIN users u ON u.id = t.user_id
		LEFT JOIN ticket_sla s ON s.ticket_id = t.id
		LEFT JOIN resolved ON resolved.ticket_id = t.id AND resolved.rn = 1
		WHERE ` + strings.Join(where, " AND ") + `
		ORDER BY t.id`
	return statement, args
}

// TicketMetrics selects a ticket once. The window function chooses the final
// audited transition to resolved in the requested interval using timestamp/id;
// current ticket, category and assignee joins deliberately provide current
// attribution. Pending records are included regardless of the selected period.
func (s *ticketMetricsStore) TicketMetrics(ctx context.Context, f application.TicketMetricsFilter) ([]application.TicketMetricsRecord, error) {
	// f.End is already the exclusive UTC boundary produced by the metrics filter
	// normalizer, so the window is used as-is.
	statement, args := ticketMetricsQuery(f)
	rows, err := s.db.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: ticket metrics: %w", err)
	}
	defer rows.Close()
	var out []application.TicketMetricsRecord
	for rows.Next() {
		var r application.TicketMetricsRecord
		var created, resolved sql.NullString
		var state string
		var deskID, agentID, slaTicketID, firstResponseSeconds, resolveSeconds sql.NullInt64
		var startedAt, policySnapshotAt sql.NullString
		var warnFR, dueFR, warnRes, dueRes sql.NullString
		var firstResponse, firstResolved sql.NullString
		if err := rows.Scan(
			&r.TicketID, &created, &resolved, &state, &deskID, &r.DeskName, &agentID, &r.AgentName,
			&r.Priority, &r.CategoryName,
			&slaTicketID, &firstResponseSeconds, &resolveSeconds, &startedAt, &policySnapshotAt,
			&warnFR, &dueFR, &warnRes, &dueRes,
			&firstResponse, &firstResolved,
		); err != nil {
			return nil, fmt.Errorf("sqlite: scan ticket metrics: %w", err)
		}
		if created.Valid {
			r.CreatedAt, _ = time.Parse(timeLayout, created.String)
		}
		if resolved.Valid {
			var err error
			r.ResolvedAt, err = time.Parse(timeLayout, resolved.String)
			if err != nil {
				return nil, fmt.Errorf("sqlite: parse metric resolution time: %w", err)
			}
			r.ResolutionWeek = r.ResolvedAt
		}
		r.CurrentState = domain.State(state)
		if deskID.Valid {
			v := deskID.Int64
			r.DeskID = &v
		}
		if agentID.Valid {
			v := agentID.Int64
			r.AgentID = &v
		}
		// The LEFT JOIN makes every ticket_sla column NULL when the ticket has
		// no frozen row; slaTicketID is the presence marker, and the shared
		// parser reads the same column set as the single and batch SLA reads.
		if slaTicketID.Valid {
			sla := &domain.TicketSLA{
				FirstResponseSeconds: int(firstResponseSeconds.Int64),
				ResolveSeconds:       int(resolveSeconds.Int64),
			}
			if err := parseTicketSLA(sla, startedAt.String, policySnapshotAt.String, warnFR.String, dueFR.String, warnRes.String, dueRes.String); err != nil {
				return nil, fmt.Errorf("sqlite: parse metric ticket sla: %w", err)
			}
			r.SLA = sla
		}
		var err error
		if r.Milestones.FirstResponseAt, err = parseSLAMilestone(firstResponse, "metric first response"); err != nil {
			return nil, err
		}
		if r.Milestones.FirstResolvedAt, err = parseSLAMilestone(firstResolved, "metric first resolution"); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: iterate ticket metrics: %w", err)
	}
	return out, nil
}
