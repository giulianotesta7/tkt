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

type catalogStore struct{ db *sql.DB }

var _ application.CatalogStore = (*catalogStore)(nil)

func newCatalogStore(db *sql.DB) *catalogStore { return &catalogStore{db: db} }

func (cs *catalogStore) SearchCatalog(ctx context.Context, query string) ([]domain.CatalogCategory, error) {
	pattern := "%" + strings.ToLower(strings.TrimSpace(query)) + "%"
	rows, err := cs.db.QueryContext(ctx, `SELECT c.id, c.name, c.description, c.keywords, c.created_at
		FROM categories c
		WHERE lower(c.name) LIKE ? OR lower(c.description) LIKE ?
		ORDER BY c.name COLLATE NOCASE, c.id`, pattern, pattern)
	if err != nil {
		return nil, fmt.Errorf("sqlite: search catalog: %w", err)
	}
	defer rows.Close()
	var out []domain.CatalogCategory
	for rows.Next() {
		var c domain.CatalogCategory
		var created string
		if err := rows.Scan(&c.ID, &c.Name, &c.Description, &c.Keywords, &created); err != nil {
			return nil, err
		}
		var parseErr error
		c.CreatedAt, parseErr = parseCatalogTime(created)
		if parseErr != nil {
			return nil, parseErr
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func parseCatalogTime(value string) (time.Time, error) { return time.Parse(timeLayout, value) }

func (cs *catalogStore) ListTreeDesks(ctx context.Context) ([]domain.CatalogDesk, error) {
	// Aggregate current publications and open responsibility once for the whole
	// page. Repeated steps count a category once per targeted desk; historical
	// pins matter only to ticket responsibility, never to category counts.
	// Guard every JSON function argument with CASE: json_each exposes strings
	// as unquoted SQL text, and WHERE/AND evaluation order is not guaranteed.
	rows, err := cs.db.QueryContext(ctx, `WITH `+responsibilityPendingSteps+`,
		published_targets AS (
			SELECT DISTINCT cw.category_id,
				json_extract(CASE WHEN step.type='object' THEN step.value ELSE '{}' END, '$.assign_to_desk.desk_id') AS desk_id
			FROM category_workflows cw
			JOIN workflow_versions w ON w.id=cw.current_version_id
			JOIN json_each(w.steps_json) step
			WHERE json_extract(CASE WHEN step.type='object' THEN step.value ELSE '{}' END, '$.type')='assign_to_desk'
			AND json_type(CASE WHEN step.type='object' THEN step.value ELSE '{}' END, '$.assign_to_desk.desk_id')='integer'
		), category_counts AS (
			SELECT desk_id, COUNT(*) AS n FROM published_targets GROUP BY desk_id
		), open_counts AS (
			SELECT `+responsibilityDeskID+` AS desk_id, COUNT(*) AS n
			FROM tickets t LEFT JOIN pending_steps ps ON ps.ticket_id=t.id
			WHERE t.state IN ('new','in_progress','resolved') GROUP BY desk_id
		)
		SELECT ds.id, ds.name, ds.description, ds.created_at,
		COALESCE(cc.n,0), COALESCE(cc.n,0), `+deskActiveAgentCount+`, COALESCE(oc.n,0)
		FROM desks ds
		LEFT JOIN category_counts cc ON cc.desk_id=ds.id
		LEFT JOIN open_counts oc ON oc.desk_id=ds.id
		ORDER BY ds.name COLLATE NOCASE, ds.id`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list tree desks: %w", err)
	}
	defer rows.Close()
	var out []domain.CatalogDesk
	for rows.Next() {
		var d domain.CatalogDesk
		var created string
		if err := rows.Scan(&d.ID, &d.Name, &d.Description, &created, &d.CategoryCount,
			&d.OfferedCategoryCount, &d.AgentCount, &d.OpenTickets); err != nil {
			return nil, err
		}
		var parseErr error
		d.CreatedAt, parseErr = parseCatalogTime(created)
		if parseErr != nil {
			return nil, parseErr
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (cs *catalogStore) ListDefaultSLACategories(ctx context.Context) ([]int64, error) {
	// A category matches the defaults when its materialized matrix has exactly the
	// current default priorities and none of them differs. The comparison reads
	// the CURRENT sla_defaults rows, so an admin who moves the defaults turns every
	// untouched category back to Custom with no row rewritten (migration 0016
	// materializes at creation time only).
	rows, err := cs.db.QueryContext(ctx, `
		SELECT p.category_id
		  FROM sla_policies p JOIN sla_defaults d ON d.priority = p.priority
		 GROUP BY p.category_id
		HAVING COUNT(*) = (SELECT COUNT(*) FROM sla_defaults)
		   AND SUM(CASE WHEN p.first_response_seconds = d.first_response_seconds
		                 AND p.resolve_seconds = d.resolve_seconds THEN 0 ELSE 1 END) = 0
		 ORDER BY p.category_id`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list default sla categories: %w", err)
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
