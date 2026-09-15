package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// Agent queue row-context read model (issue #122): one batched, bounded query
// gathers the candidate facts for a whole page of tickets, and the Go-side
// resolution below applies the read-model rules. No per-ticket N+1, no
// migration, positional placeholders only.

// agentQueueContextQuery joins, for every requested ticket in ONE statement:
// the active run's cursor facts, the pinned immutable definition, the pinned
// claim step's surviving desk name (cd.name — joined via the same
// json_extract path the claimable filter uses), and the LATEST desk-bearing
// audit event's surviving desk name (d.name). The LEFT JOINs degrade every
// absent fact to NULL; Go decides which candidate each row actually uses.
const agentQueueContextQuery = `SELECT t.id, t.user_id, r.status, r.current_step_index, wv.steps_json, cd.name, d.name
FROM tickets t
LEFT JOIN ticket_workflow_runs r ON r.ticket_id = t.id
LEFT JOIN workflow_versions wv ON wv.id = t.workflow_version_id AND wv.category_id = t.category_id
LEFT JOIN desks cd ON cd.id = CAST(json_extract(wv.steps_json, '$[' || r.current_step_index || '].assign_to_desk.desk_id') AS INTEGER)
LEFT JOIN audit_events ae ON ae.id = (
	SELECT a.id FROM audit_events a
	WHERE a.ticket_id = t.id AND a.desk_id IS NOT NULL
	ORDER BY a.id DESC LIMIT 1
)
LEFT JOIN desks d ON d.id = ae.desk_id
WHERE t.id IN (`

// agentQueueContext answers the whole batch with the single bounded query
// above. Empty input returns an empty map WITHOUT querying; an oversized
// batch fails closed (application.MaxAgentQueueContextBatch).
func agentQueueContext(ctx context.Context, db *sql.DB, ticketIDs []int64) (map[int64]application.AgentTicketRowContext, error) {
	rowsOut := make(map[int64]application.AgentTicketRowContext, len(ticketIDs))
	if len(ticketIDs) == 0 {
		return rowsOut, nil
	}
	if len(ticketIDs) > application.MaxAgentQueueContextBatch {
		return nil, fmt.Errorf("sqlite: agent queue context batch of %d exceeds the %d-id bound", len(ticketIDs), application.MaxAgentQueueContextBatch)
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?, ", len(ticketIDs)), ", ")
	args := make([]any, len(ticketIDs))
	for i, id := range ticketIDs {
		args[i] = id
	}
	rows, err := db.QueryContext(ctx, agentQueueContextQuery+placeholders+")", args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: agent queue context: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id        int64
			userID    sql.NullInt64
			status    sql.NullString
			cursor    sql.NullInt64
			stepsJSON sql.NullString
			claimDesk sql.NullString
			auditDesk sql.NullString
		)
		if err := rows.Scan(&id, &userID, &status, &cursor, &stepsJSON, &claimDesk, &auditDesk); err != nil {
			return nil, fmt.Errorf("sqlite: scan agent queue context: %w", err)
		}
		row, err := resolveAgentQueueRow(userID, status, cursor, stepsJSON, claimDesk, auditDesk)
		if err != nil {
			return nil, err
		}
		rowsOut[id] = row
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: agent queue context: %w", err)
	}
	return rowsOut, nil
}

// resolveAgentQueueRow applies the read-model rules to one row's gathered
// facts:
//   - CurrentTask only for an ACTIVE run with a valid cursor and a resolvable
//     pinned step (parse failures fail closed, matching the run store);
//   - Position only when that exact current step is assign_to_desk[claim] on
//     a currently unassigned ticket;
//   - DeskName for an assigned ticket from the latest surviving desk-bearing
//     audit, for an unassigned ticket ONLY from the current pinned claim
//     step — never from category desks or older history.
func resolveAgentQueueRow(userID sql.NullInt64, status sql.NullString, cursor sql.NullInt64, stepsJSON, claimDesk, auditDesk sql.NullString) (application.AgentTicketRowContext, error) {
	row := application.AgentTicketRowContext{}
	assigned := userID.Valid
	if status.Valid && status.String == "active" && cursor.Valid && stepsJSON.Valid {
		definition, err := domain.ParseWorkflowDefinition([]byte(stepsJSON.String))
		if err != nil {
			return application.AgentTicketRowContext{}, fmt.Errorf("sqlite: parse agent queue workflow: %w", err)
		}
		i := int(cursor.Int64)
		if i >= 0 && i < len(definition) {
			step := definition[i]
			row.CurrentTask = agentTaskText(step)
			if !assigned && step.Type == domain.StepAssignToDesk && step.AssignToDesk != nil && step.AssignToDesk.Strategy == domain.StrategyClaim {
				if claimDesk.Valid {
					row.DeskName = claimDesk.String
				}
				position := i + 1
				row.Position = &position
			}
		}
	}
	if assigned {
		row.DeskName = auditDesk.String
	}
	return row, nil
}

// agentTaskText derives the queue row's current-task text from the resolved
// pinned step, following the existing presentation conventions: a manual task
// shows its pinned instruction verbatim, a form shows its pinned field
// labels, and the claim/automatic steps show their operational label. An
// unresolvable step yields "".
func agentTaskText(step domain.WorkflowStep) string {
	switch step.Type {
	case domain.StepManualTask:
		if step.ManualTask == nil {
			return ""
		}
		return step.ManualTask.Instructions
	case domain.StepForm:
		if step.Form == nil {
			return ""
		}
		labels := make([]string, 0, len(step.Form.Fields))
		for _, field := range step.Form.Fields {
			labels = append(labels, field.Label)
		}
		return "Form: " + strings.Join(labels, ", ")
	case domain.StepAssignToDesk:
		if step.AssignToDesk == nil {
			return ""
		}
		if step.AssignToDesk.Strategy == domain.StrategyClaim {
			return "Waiting for claim"
		}
		return "Waiting for automatic desk assignment"
	case domain.StepResolve:
		return "Waiting for resolution"
	case domain.StepClose:
		return "Waiting for closure"
	default:
		return ""
	}
}
