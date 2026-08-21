package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

type workflowResponseStore struct{ db *sql.DB }

var _ application.WorkflowResponseStore = (*workflowResponseStore)(nil)

func newWorkflowResponseStore(db *sql.DB) *workflowResponseStore {
	return &workflowResponseStore{db: db}
}

// ListWorkflowResponses decodes persisted answer rows only through the ticket's
// immutable pinned definition. It never reads draft/current definitions, exposes
// raw JSON, or indexes a response slice with persisted step_index values.
func (s *workflowResponseStore) ListWorkflowResponses(ctx context.Context, ticketID int64) ([]application.WorkflowResponse, error) {
	var versionID sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `SELECT workflow_version_id FROM tickets WHERE id=?`, ticketID).Scan(&versionID); err != nil {
		return nil, fmt.Errorf("sqlite: read response workflow pin: %w", err)
	}
	if !versionID.Valid {
		return nil, nil // legacy tickets have no workflow card
	}
	var stepsJSON string
	if err := s.db.QueryRowContext(ctx, `SELECT steps_json FROM workflow_versions WHERE id=?`, versionID.Int64).Scan(&stepsJSON); err != nil {
		return nil, fmt.Errorf("sqlite: read response workflow version: %w", err)
	}
	definition, err := domain.ParseWorkflowDefinition([]byte(stepsJSON))
	if err != nil {
		return nil, fmt.Errorf("sqlite: parse response workflow version: %w", err)
	}
	if issues := definition.Validate(); len(issues) > 0 {
		return nil, fmt.Errorf("sqlite: invalid response workflow version: %v", issues)
	}

	rows, err := s.db.QueryContext(ctx, `SELECT step_index, answers_json, submitted_at
		FROM ticket_form_answers WHERE ticket_id=? ORDER BY step_index ASC`, ticketID)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list workflow responses: %w", err)
	}
	defer rows.Close()

	responses := make([]application.WorkflowResponse, 0)
	seen := make(map[int]struct{}, len(definition))
	for rows.Next() {
		var (
			stepIndex int
			answers   string
			submitted string
		)
		if err := rows.Scan(&stepIndex, &answers, &submitted); err != nil {
			return nil, fmt.Errorf("sqlite: scan workflow response: %w", err)
		}
		// Validate before any indexed access or response accumulation. A corrupt
		// index cannot cause a sparse/unbounded allocation or panic.
		if stepIndex < 0 || stepIndex >= len(definition) {
			return nil, fmt.Errorf("sqlite: workflow response step index %d out of pinned definition bounds", stepIndex)
		}
		if _, duplicate := seen[stepIndex]; duplicate {
			return nil, fmt.Errorf("sqlite: duplicate workflow response step index %d", stepIndex)
		}
		seen[stepIndex] = struct{}{}
		step := definition[stepIndex]
		if step.Type != domain.StepForm || step.Form == nil {
			return nil, fmt.Errorf("sqlite: workflow response step index %d is not a form", stepIndex)
		}
		fields, err := decodeWorkflowResponseFields(step.Form.Fields, []byte(answers))
		if err != nil {
			return nil, fmt.Errorf("sqlite: workflow response step index %d: %w", stepIndex, err)
		}
		at, err := time.Parse(timeLayout, submitted)
		if err != nil {
			return nil, fmt.Errorf("sqlite: parse workflow response timestamp: %w", err)
		}
		responses = append(responses, application.WorkflowResponse{StepIndex: stepIndex, SubmittedAt: at, Fields: fields})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: list workflow responses: %w", err)
	}
	return responses, nil
}

func decodeWorkflowResponseFields(definition []domain.FormField, raw []byte) ([]application.WorkflowResponseField, error) {
	var values []json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(&values); err != nil {
		return nil, errors.New("answers are malformed")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errors.New("answers contain trailing data")
	}
	if len(values) != len(definition) {
		return nil, errors.New("answer count does not match pinned fields")
	}
	fields := make([]application.WorkflowResponseField, 0, len(definition))
	for i, field := range definition {
		value, err := decodeWorkflowResponseValue(field, values[i])
		if err != nil {
			return nil, err
		}
		fields = append(fields, application.WorkflowResponseField{Label: field.Label, Value: value})
	}
	return fields, nil
}

func decodeWorkflowResponseValue(field domain.FormField, raw json.RawMessage) (string, error) {
	switch field.Kind {
	case domain.FieldCheckbox:
		var value bool
		if err := json.Unmarshal(raw, &value); err != nil {
			return "", errors.New("checkbox answer is not a boolean")
		}
		if field.Required && !value {
			return "", errors.New("required checkbox answer is false")
		}
		return strconv.FormatBool(value), nil
	case domain.FieldShortText, domain.FieldLongText, domain.FieldSingleSelect:
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return "", errors.New("text answer is not a string")
		}
		if field.Required && value == "" {
			return "", errors.New("required answer is empty")
		}
		if field.Kind == domain.FieldSingleSelect && value != "" {
			valid := false
			for _, option := range field.Options {
				if value == option {
					valid = true
					break
				}
			}
			if !valid {
				return "", errors.New("single_select answer is outside pinned options")
			}
		}
		return value, nil
	default:
		return "", errors.New("unknown pinned field kind")
	}
}
