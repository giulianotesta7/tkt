package sqlite

import (
	"context"
	"testing"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// RED tests for migration 0014 (per-category SLA configuration — issue
// #211): the sla_defaults and sla_policies tables, the priority and
// seconds CHECK constraints, the categories cascade, the exact seeded
// defaults, the seeded settings keys, and the version bookkeeping.
// Written against the schema that does not exist yet: every assertion
// fails until 0014 lands (strict TDD RED).

// slaDefaultSeeds is the exact global default matrix the migration must
// seed, in the canonical priority order (critical, high, medium, low).
var slaDefaultSeeds = []struct {
	priority             domain.Priority
	firstResponseSeconds int
	resolveSeconds       int
}{
	{domain.PriorityCritical, 1800, 14400},
	{domain.PriorityHigh, 3600, 28800},
	{domain.PriorityMedium, 14400, 86400},
	{domain.PriorityLow, 28800, 259200},
}

func TestMigration0014TablesAndColumns(t *testing.T) {
	s := newTestDB(t)

	for _, tc := range []struct {
		table   string
		columns []string
	}{
		{"sla_defaults", []string{"priority", "first_response_seconds", "resolve_seconds"}},
		{"sla_policies", []string{"category_id", "priority", "first_response_seconds", "resolve_seconds"}},
	} {
		var n int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, tc.table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Fatalf("table %s missing after 0014", tc.table)
		}
		for _, col := range tc.columns {
			if !columnExists(t, s, tc.table, col) {
				t.Errorf("%s.%s column missing after 0014", tc.table, col)
			}
		}
	}
}

func TestMigration0014PriorityCheck(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()

	// An out-of-set priority is rejected by the CHECK on sla_defaults.
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO sla_defaults (priority, first_response_seconds, resolve_seconds)
		 VALUES ('urgent', 60, 60)`); err == nil {
		t.Fatal("sla_defaults insert with invalid priority succeeded, want CHECK failure")
	}

	// The same holds on sla_policies (against a real category).
	catID := seedCategory(t, s, "Bugs")
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO sla_policies (category_id, priority, first_response_seconds, resolve_seconds)
		 VALUES (?, 'urgent', 60, 60)`, catID); err == nil {
		t.Fatal("sla_policies insert with invalid priority succeeded, want CHECK failure")
	}
}

func TestMigration0014SecondsCheck(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()

	// Targets below 60 seconds are rejected on both tables.
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO sla_defaults (priority, first_response_seconds, resolve_seconds)
		 VALUES ('high', 59, 60)`); err == nil {
		t.Fatal("sla_defaults insert with first_response_seconds < 60 succeeded, want CHECK failure")
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO sla_defaults (priority, first_response_seconds, resolve_seconds)
		 VALUES ('high', 60, 59)`); err == nil {
		t.Fatal("sla_defaults insert with resolve_seconds < 60 succeeded, want CHECK failure")
	}

	catID := seedCategory(t, s, "Bugs")
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO sla_policies (category_id, priority, first_response_seconds, resolve_seconds)
		 VALUES (?, 'high', 59, 60)`, catID); err == nil {
		t.Fatal("sla_policies insert with first_response_seconds < 60 succeeded, want CHECK failure")
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO sla_policies (category_id, priority, first_response_seconds, resolve_seconds)
		 VALUES (?, 'high', 60, 59)`, catID); err == nil {
		t.Fatal("sla_policies insert with resolve_seconds < 60 succeeded, want CHECK failure")
	}
}

// TestMigration0014CategoryCascade proves the sla_policies FK both exists
// and is declared ON DELETE CASCADE: category_store.go hard-deletes
// categories, so a missing cascade would strand orphan policy rows.
func TestMigration0014CategoryCascade(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()

	var table, from, to, onDelete string
	if err := s.db.QueryRow(`SELECT "table", "from", "to", on_delete FROM pragma_foreign_key_list(?) WHERE "from" = 'category_id'`, "sla_policies").
		Scan(&table, &from, &to, &onDelete); err != nil {
		t.Fatalf("pragma_foreign_key_list(sla_policies): %v", err)
	}
	if table != "categories" || to != "id" {
		t.Errorf("sla_policies FK = (%s, %s, %s), want (categories, category_id, id)", table, from, to)
	}
	if onDelete != "CASCADE" {
		t.Fatalf("sla_policies on_delete = %q, want CASCADE", onDelete)
	}

	// Behavioural proof: a deleted category takes its policy rows with
	// it. The rows come from the migration-0015 trigger (which
	// materializes the default matrix at category creation), so this
	// proves the cascade over trigger-created rows — stronger than rows
	// the test inserted itself. Assert the category actually holds 4
	// materialized rows BEFORE the delete, so the test cannot pass by
	// deleting a category that had no rows to cascade.
	catID := seedCategory(t, s, "Bugs")
	var before int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sla_policies WHERE category_id = ?`, catID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if before != len(slaDefaultSeeds) {
		t.Fatalf("materialized policy rows before delete = %d, want %d (0015 trigger)", before, len(slaDefaultSeeds))
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM categories WHERE id = ?`, catID); err != nil {
		t.Fatalf("delete category: %v", err)
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sla_policies WHERE category_id = ?`, catID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("sla_policies rows after category delete = %d, want 0 (cascade)", n)
	}
}

func TestMigration0014DefaultsSeeded(t *testing.T) {
	s := newTestDB(t)

	rows, err := s.db.Query(`SELECT priority, first_response_seconds, resolve_seconds FROM sla_defaults`)
	if err != nil {
		t.Fatalf("read sla_defaults: %v", err)
	}
	defer rows.Close()
	var got []struct {
		priority             string
		firstResponseSeconds int
		resolveSeconds       int
	}
	for rows.Next() {
		var g struct {
			priority             string
			firstResponseSeconds int
			resolveSeconds       int
		}
		if err := rows.Scan(&g.priority, &g.firstResponseSeconds, &g.resolveSeconds); err != nil {
			t.Fatal(err)
		}
		got = append(got, g)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(got) != len(slaDefaultSeeds) {
		t.Fatalf("sla_defaults rows = %d, want %d", len(got), len(slaDefaultSeeds))
	}
	for i, g := range got {
		want := slaDefaultSeeds[i]
		if g.priority != string(want.priority) || g.firstResponseSeconds != want.firstResponseSeconds || g.resolveSeconds != want.resolveSeconds {
			t.Errorf("sla_defaults[%d] = (%s, %d, %d), want (%s, %d, %d)",
				i, g.priority, g.firstResponseSeconds, g.resolveSeconds,
				want.priority, want.firstResponseSeconds, want.resolveSeconds)
		}
	}
}

func TestMigration0014PoliciesEmpty(t *testing.T) {
	s := newTestDB(t)

	// sla_policies is intentionally empty after the migration: rows are
	// materialized per category at creation time by a later task.
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sla_policies`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("sla_policies rows = %d, want 0 (empty after migration)", n)
	}
}

func TestMigration0014SettingsSeeded(t *testing.T) {
	s := newTestDB(t)

	// sla_enabled seeds OFF on purpose: a migration must never silently
	// turn on a customer-facing commitment for an existing installation.
	var enabled string
	if err := s.db.QueryRow(`SELECT value FROM settings WHERE key = 'sla_enabled'`).Scan(&enabled); err != nil {
		t.Fatalf("read sla_enabled: %v", err)
	}
	if enabled != "0" {
		t.Errorf("sla_enabled = %q, want %q", enabled, "0")
	}

	var warning string
	if err := s.db.QueryRow(`SELECT value FROM settings WHERE key = 'sla_warning_percent'`).Scan(&warning); err != nil {
		t.Fatalf("read sla_warning_percent: %v", err)
	}
	if warning != "80" {
		t.Errorf("sla_warning_percent = %q, want %q", warning, "80")
	}

	// sla_enabled_at is deliberately NOT seeded: it records when the feature
	// was FIRST switched on, and a migration instant would claim an activation
	// that never happened while sla_enabled is '0'.
	var seededInstant int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM settings WHERE key = 'sla_enabled_at'`).Scan(&seededInstant); err != nil {
		t.Fatalf("count sla_enabled_at: %v", err)
	}
	if seededInstant != 0 {
		t.Errorf("sla_enabled_at rows = %d, want 0 (the activation instant is never seeded)", seededInstant)
	}
}

func TestMigration0014VersionBookkeeping(t *testing.T) {
	s := newTestDB(t)
	var applied int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version=13`).Scan(&applied); err != nil {
		t.Fatalf("schema_migrations read: %v", err)
	}
	if applied != 1 {
		t.Fatalf("schema_migrations version=13 rows = %d, want 1", applied)
	}
}
