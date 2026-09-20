package sqlite

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
)

// TestMigration0012 proves the resolved-transition index contract (issue #222):
// audit_events carries idx_audit_resolution with the exact measured column order
// (action, field, to_value, created_at), the version is recorded exactly once,
// and the real administrative metrics read searches that index rather than
// scanning it. The plan assertions deliberately look at the single plan line
// that names the index, never the whole plan text, so unrelated planner output
// is not pinned — but the line must be a SEARCH, because the point of the
// migration is that an unused or merely-scanned index would otherwise be
// invisible.
func TestMigration0012(t *testing.T) {
	s := newTestDB(t)

	// The index exists on audit_events, read from the index pragmas rather than
	// from sqlite_master's SQL text.
	var indexCount int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM pragma_index_list('audit_events') WHERE name = 'idx_audit_resolution'`).Scan(&indexCount); err != nil {
		t.Fatalf("pragma_index_list: %v", err)
	}
	if indexCount != 1 {
		t.Fatalf("idx_audit_resolution on audit_events = %d, want 1", indexCount)
	}

	// The column order is exactly (action, field, to_value, created_at): the
	// three equality predicates lead and the range sits last.
	want := []string{"action", "field", "to_value", "created_at"}
	got := indexColumns(t, s, "idx_audit_resolution")
	if len(got) != len(want) {
		t.Fatalf("idx_audit_resolution columns = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("idx_audit_resolution columns = %v, want %v", got, want)
		}
	}

	// Migration bookkeeping: version 12 applied exactly once.
	var applied int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version=12`).Scan(&applied); err != nil {
		t.Fatalf("schema_migrations read: %v", err)
	}
	if applied != 1 {
		t.Fatalf("schema_migrations version=12 rows = %d, want 1", applied)
	}

	// The point of the migration: the REAL metrics read must use the index.
	// EXPLAIN the statement the store produces via ticketMetricsQuery, so a
	// replica query cannot drift away from production.
	filter := application.TicketMetricsFilter{
		Start: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
	}
	statement, args := ticketMetricsQuery(filter)
	withIndex := explainMetricsPlan(t, s, statement, args)
	t.Logf("EXPLAIN QUERY PLAN with idx_audit_resolution:\n%s", withIndex)
	// Naming the index is not enough: the whole point of the migration is that
	// the three equality predicates are searched rather than walked, so the
	// plan line that names the index must be a SEARCH. Asserting on that line
	// (rather than on the whole plan) tolerates unrelated planner output and a
	// covering-index rephrasing, but not a full index scan.
	var indexLine string
	for _, line := range strings.Split(withIndex, "\n") {
		if strings.Contains(line, "idx_audit_resolution") {
			indexLine = line
			break
		}
	}
	if indexLine == "" {
		t.Fatalf("metrics plan does not use idx_audit_resolution:\n%s", withIndex)
	}
	if !strings.Contains(indexLine, "SEARCH") {
		t.Fatalf("metrics plan names the index but does not search it: %q", indexLine)
	}

	// Without the index the same statement cannot search the three equality
	// predicates and falls back to the pre-migration plan: a scan of
	// idx_audit_ticket. This does not by itself prove the index is what changed
	// the plan — the positive assertion above does that — but it pins the
	// fallback the migration exists to remove, and that the new index is not
	// somehow still referenced.
	if _, err := s.db.ExecContext(context.Background(), `DROP INDEX idx_audit_resolution`); err != nil {
		t.Fatalf("drop index for comparison: %v", err)
	}
	withoutIndex := explainMetricsPlan(t, s, statement, args)
	t.Logf("EXPLAIN QUERY PLAN without idx_audit_resolution:\n%s", withoutIndex)
	if !strings.Contains(withoutIndex, "idx_audit_ticket") || strings.Contains(withoutIndex, "idx_audit_resolution") {
		t.Fatalf("metrics plan without the index = %q, want the pre-migration fallback scanning idx_audit_ticket", withoutIndex)
	}
}

// indexColumns returns the column names of a named index in seqno order, as
// reported by pragma_index_info.
func indexColumns(t *testing.T, s *Store, index string) []string {
	t.Helper()
	rows, err := s.db.Query(`SELECT name FROM pragma_index_info(?) ORDER BY seqno`, index)
	if err != nil {
		t.Fatalf("pragma_index_info(%s): %v", index, err)
	}
	defer rows.Close()
	var columns []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan pragma_index_info(%s): %v", index, err)
		}
		columns = append(columns, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate pragma_index_info(%s): %v", index, err)
	}
	return columns
}

// explainMetricsPlan returns the joined EXPLAIN QUERY PLAN detail lines for the
// given statement and args. Rows are closed before returning so the single
// shared-cache connection is free for the caller's next statement.
func explainMetricsPlan(t *testing.T, s *Store, statement string, args []any) string {
	t.Helper()
	rows, err := s.db.QueryContext(context.Background(), "EXPLAIN QUERY PLAN "+statement, args...)
	if err != nil {
		t.Fatalf("explain query plan: %v", err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatalf("scan explain query plan: %v", err)
		}
		plan = append(plan, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate explain query plan: %v", err)
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("close explain query plan: %v", err)
	}
	return strings.Join(plan, "\n")
}
