package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// SLA policy store (issue #211): the global defaults read back in the
// canonical priority order, a category matrix upserts and reads, an
// absent matrix is empty without an error, and a frozen ticket SLA round-
// trips all four fields while an unfrozen ticket reads (nil, nil).

func TestSLADefaultsCanonicalOrder(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()

	got, err := s.SLAStore().ListDefaults(ctx)
	if err != nil {
		t.Fatalf("list defaults: %v", err)
	}
	if len(got) != len(slaDefaultSeeds) {
		t.Fatalf("defaults rows = %d, want %d", len(got), len(slaDefaultSeeds))
	}
	for i, p := range got {
		want := slaDefaultSeeds[i]
		if p.Priority != want.priority || p.FirstResponseSeconds != want.firstResponseSeconds || p.ResolveSeconds != want.resolveSeconds {
			t.Errorf("defaults[%d] = %+v, want (%s, %d, %d)",
				i, p, want.priority, want.firstResponseSeconds, want.resolveSeconds)
		}
	}
}

func TestSLACategoryMatrixUpsertAndRead(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	catID := seedCategory(t, s, "Bugs")

	// Materialize a category matrix (custom values, NOT the defaults) and
	// read it back in the canonical priority order.
	for _, p := range []domain.SLAPolicy{
		{Priority: domain.PriorityLow, FirstResponseSeconds: 30000, ResolveSeconds: 200000},
		{Priority: domain.PriorityCritical, FirstResponseSeconds: 900, ResolveSeconds: 7200},
		{Priority: domain.PriorityMedium, FirstResponseSeconds: 7200, ResolveSeconds: 43200},
		{Priority: domain.PriorityHigh, FirstResponseSeconds: 1800, ResolveSeconds: 14400},
	} {
		if err := s.SLAStore().UpsertCategoryTarget(ctx, catID, p); err != nil {
			t.Fatalf("upsert %s: %v", p.Priority, err)
		}
	}
	got, err := s.SLAStore().ListByCategory(ctx, catID)
	if err != nil {
		t.Fatalf("list category matrix: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("category matrix rows = %d, want 4", len(got))
	}
	wantOrder := []domain.Priority{
		domain.PriorityCritical, domain.PriorityHigh, domain.PriorityMedium, domain.PriorityLow,
	}
	for i, p := range got {
		if p.Priority != wantOrder[i] {
			t.Errorf("matrix[%d] priority = %s, want %s (canonical order)", i, p.Priority, wantOrder[i])
		}
	}
	if got[0].FirstResponseSeconds != 900 || got[0].ResolveSeconds != 7200 {
		t.Errorf("matrix[0] targets = (%d, %d), want (900, 7200)", got[0].FirstResponseSeconds, got[0].ResolveSeconds)
	}
}

func TestSLACategoryUpsertUpdatesExistingRow(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	catID := seedCategory(t, s, "Bugs")

	first := domain.SLAPolicy{Priority: domain.PriorityHigh, FirstResponseSeconds: 1800, ResolveSeconds: 14400}
	if err := s.SLAStore().UpsertCategoryTarget(ctx, catID, first); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	second := domain.SLAPolicy{Priority: domain.PriorityHigh, FirstResponseSeconds: 900, ResolveSeconds: 7200}
	if err := s.SLAStore().UpsertCategoryTarget(ctx, catID, second); err != nil {
		t.Fatalf("second upsert: %v", err)
	}

	// The pair exists exactly once, with the REPLACED targets.
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sla_policies WHERE category_id = ? AND priority = 'high'`, catID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("sla_policies (category, high) rows = %d, want 1 (upsert, not duplicate)", n)
	}
	got, err := s.SLAStore().ListByCategory(ctx, catID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	// Migration 0016 materializes the default matrix at category creation,
	// so the category holds exactly one row per priority; the upsert must
	// have REPLACED the high row's targets, not duplicated it.
	if len(got) != 4 {
		t.Fatalf("matrix after re-upsert = %d rows, want 4 (one per priority)", len(got))
	}
	for _, p := range got {
		if p.Priority == domain.PriorityHigh && (p.FirstResponseSeconds != 900 || p.ResolveSeconds != 7200) {
			t.Fatalf("high row after re-upsert = (%d, %d), want (900, 7200)", p.FirstResponseSeconds, p.ResolveSeconds)
		}
	}
}

func TestSLAAbsentCategoryMatrixReturnsEmpty(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()

	// A category with no materialized matrix is a state, not a failure.
	got, err := s.SLAStore().ListByCategory(ctx, 99999)
	if err != nil {
		t.Fatalf("list absent matrix: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("absent matrix rows = %d, want 0", len(got))
	}
}

func TestSLATicketSLARoundTrip(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	ticketID := seedTicketForTimeline(t, s, 1)

	want := domain.TicketSLA{
		FirstResponseSeconds: 1800,
		ResolveSeconds:       14400,
		WarnFirstResponseAt:  testClock.Add(24 * time.Minute),
		DueFirstResponseAt:   testClock.Add(30 * time.Minute),
		WarnResolveAt:        testClock.Add(192 * time.Minute),
		DueResolveAt:         testClock.Add(4 * time.Hour),
		StartedAt:            testClock,
		PolicySnapshotAt:     testClock.Add(30 * time.Second),
	}
	if err := s.SLAStore().InsertTicketSLA(ctx, ticketID, want); err != nil {
		t.Fatalf("insert ticket sla: %v", err)
	}

	got, err := s.SLAStore().TicketSLA(ctx, ticketID)
	if err != nil {
		t.Fatalf("get ticket sla: %v", err)
	}
	if got == nil {
		t.Fatal("ticket sla = nil, want a frozen row")
	}
	if got.FirstResponseSeconds != want.FirstResponseSeconds || got.ResolveSeconds != want.ResolveSeconds {
		t.Errorf("targets = (%d, %d), want (%d, %d)",
			got.FirstResponseSeconds, got.ResolveSeconds, want.FirstResponseSeconds, want.ResolveSeconds)
	}
	if !got.StartedAt.Equal(want.StartedAt) {
		t.Errorf("started_at = %v, want %v", got.StartedAt, want.StartedAt)
	}
	if !got.PolicySnapshotAt.Equal(want.PolicySnapshotAt) {
		t.Errorf("policy_snapshot_at = %v, want %v", got.PolicySnapshotAt, want.PolicySnapshotAt)
	}
	// The four frozen instants (migration 0017) round-trip exactly.
	if !got.WarnFirstResponseAt.Equal(want.WarnFirstResponseAt) {
		t.Errorf("warn_first_response_at = %v, want %v", got.WarnFirstResponseAt, want.WarnFirstResponseAt)
	}
	if !got.DueFirstResponseAt.Equal(want.DueFirstResponseAt) {
		t.Errorf("due_first_response_at = %v, want %v", got.DueFirstResponseAt, want.DueFirstResponseAt)
	}
	if !got.WarnResolveAt.Equal(want.WarnResolveAt) {
		t.Errorf("warn_resolve_at = %v, want %v", got.WarnResolveAt, want.WarnResolveAt)
	}
	if !got.DueResolveAt.Equal(want.DueResolveAt) {
		t.Errorf("due_resolve_at = %v, want %v", got.DueResolveAt, want.DueResolveAt)
	}
	// Instants are stored and read back in UTC.
	if got.DueResolveAt.Location() != time.UTC {
		t.Errorf("due_resolve_at location = %v, want UTC", got.DueResolveAt.Location())
	}
}

// TestSLATicketSLAZeroInstantsStoreLegacyMarker proves a commitment with
// zero instants (a caller that never resolved them against a calendar)
// stores the ” pre-0017 marker rather than a date in year zero, and reads
// back as the zero time — "no frozen SLA" for the projection.
func TestSLATicketSLAZeroInstantsStoreLegacyMarker(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	ticketID := seedTicketForTimeline(t, s, 1)

	want := domain.TicketSLA{
		FirstResponseSeconds: 1800,
		ResolveSeconds:       14400,
		StartedAt:            testClock,
		PolicySnapshotAt:     testClock,
	}
	if err := s.SLAStore().InsertTicketSLA(ctx, ticketID, want); err != nil {
		t.Fatalf("insert ticket sla: %v", err)
	}
	var warnFR, dueFR, warnRes, dueRes string
	if err := s.db.QueryRow(`
		SELECT warn_first_response_at, due_first_response_at, warn_resolve_at, due_resolve_at
		FROM ticket_sla WHERE ticket_id = ?`, ticketID).Scan(&warnFR, &dueFR, &warnRes, &dueRes); err != nil {
		t.Fatal(err)
	}
	if warnFR != "" || dueFR != "" || warnRes != "" || dueRes != "" {
		t.Errorf("zero instants stored = (%q, %q, %q, %q), want all '' (the legacy marker)", warnFR, dueFR, warnRes, dueRes)
	}
	got, err := s.SLAStore().TicketSLA(ctx, ticketID)
	if err != nil {
		t.Fatalf("get ticket sla: %v", err)
	}
	if !got.WarnFirstResponseAt.IsZero() || !got.DueFirstResponseAt.IsZero() ||
		!got.WarnResolveAt.IsZero() || !got.DueResolveAt.IsZero() {
		t.Errorf("zero instants read back non-zero: %+v", got)
	}
}

func TestSLATicketWithoutRowReturnsNilNil(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()

	// No frozen row — a legacy ticket or one created while SLA was
	// disabled — is (nil, nil): absence is a state, not a failure.
	ticketID := seedTicketForTimeline(t, s, 1)
	got, err := s.SLAStore().TicketSLA(ctx, ticketID)
	if err != nil {
		t.Fatalf("get absent ticket sla: %v", err)
	}
	if got != nil {
		t.Errorf("ticket sla = %+v, want nil", got)
	}
}

// seedStaffComment inserts a comment row directly with an explicit
// visibility, author_role, and instant (test arrange for Milestones,
// which reads columns no comment port writes ad hoc).
func seedStaffComment(t *testing.T, s *Store, ticketID int64, visibility, authorRole, body string, at time.Time) {
	t.Helper()
	if _, err := s.db.ExecContext(context.Background(),
		`INSERT INTO comments (ticket_id, author, body, visibility, created_at, author_role)
		 VALUES (?, 'Ada', ?, ?, ?, ?)`,
		ticketID, body, visibility, formatTime(at), nullableStringPtrFor(authorRole)); err != nil {
		t.Fatalf("seed comment %q: %v", body, err)
	}
}

// nullableStringPtrFor adapts an optional author_role to the driver's
// nullable binder: an empty string binds NULL (a legacy comment whose
// authorship is unprovable).
func nullableStringPtrFor(role string) any {
	if role == "" {
		return nil
	}
	return role
}

// seedResolvedTransition inserts a resolved-state audit transition
// directly (test arrange for Milestones).
func seedResolvedTransition(t *testing.T, s *Store, ticketID int64, at time.Time) {
	t.Helper()
	if _, err := s.db.ExecContext(context.Background(),
		`INSERT INTO audit_events (ticket_id, actor, action, field, from_value, to_value, created_at)
		 VALUES (?, 'Ada', 'transition', 'state', 'in_progress', 'resolved', ?)`,
		ticketID, formatTime(at)); err != nil {
		t.Fatalf("seed resolved transition: %v", err)
	}
}

func TestSLAMilestonesBothInstants(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	ticketID := seedTicketForTimeline(t, s, 1)

	seedStaffComment(t, s, ticketID, "public", "agent", "first staff reply", testClock.Add(10*time.Minute))
	seedResolvedTransition(t, s, ticketID, testClock.Add(2*time.Hour))

	got, err := s.SLAStore().Milestones(ctx, ticketID)
	if err != nil {
		t.Fatalf("milestones: %v", err)
	}
	if got.FirstResponseAt == nil || !got.FirstResponseAt.Equal(testClock.Add(10*time.Minute)) {
		t.Errorf("FirstResponseAt = %v, want %v", got.FirstResponseAt, testClock.Add(10*time.Minute))
	}
	if got.FirstResolvedAt == nil || !got.FirstResolvedAt.Equal(testClock.Add(2*time.Hour)) {
		t.Errorf("FirstResolvedAt = %v, want %v", got.FirstResolvedAt, testClock.Add(2*time.Hour))
	}
}

// TestSLAMilestonesFirstResponseExclusions proves the first-response
// query counts only a PUBLIC comment by provable staff: an internal-only
// comment, a requester comment, and a NULL-author_role legacy comment are
// each excluded — unprovable authorship never counts as a staff response.
func TestSLAMilestonesFirstResponseExclusions(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	ticketID := seedTicketForTimeline(t, s, 1)

	seedStaffComment(t, s, ticketID, "internal", "agent", "internal note", testClock.Add(5*time.Minute))
	seedStaffComment(t, s, ticketID, "public", "user", "requester addendum", testClock.Add(6*time.Minute))
	seedStaffComment(t, s, ticketID, "public", "", "legacy unprovable comment", testClock.Add(7*time.Minute))
	// A genuinely qualifying response, later than all of the above.
	seedStaffComment(t, s, ticketID, "public", "admin", "real staff reply", testClock.Add(30*time.Minute))

	got, err := s.SLAStore().Milestones(ctx, ticketID)
	if err != nil {
		t.Fatalf("milestones: %v", err)
	}
	if got.FirstResponseAt == nil {
		t.Fatal("FirstResponseAt = nil, want the public admin comment")
	}
	if !got.FirstResponseAt.Equal(testClock.Add(30 * time.Minute)) {
		t.Errorf("FirstResponseAt = %v, want %v (only the provable public staff comment counts)",
			got.FirstResponseAt, testClock.Add(30*time.Minute))
	}
}

// TestSLAMilestonesFirstResolutionNotLast proves a reopened ticket keeps
// its FIRST resolution instant: the SLA measures the commitment made at
// creation, unlike the metrics read which takes the last.
func TestSLAMilestonesFirstResolutionNotLast(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	ticketID := seedTicketForTimeline(t, s, 1)

	seedResolvedTransition(t, s, ticketID, testClock.Add(2*time.Hour))
	seedResolvedTransition(t, s, ticketID, testClock.Add(20*time.Hour))

	got, err := s.SLAStore().Milestones(ctx, ticketID)
	if err != nil {
		t.Fatalf("milestones: %v", err)
	}
	if got.FirstResolvedAt == nil {
		t.Fatal("FirstResolvedAt = nil, want the first resolution")
	}
	if !got.FirstResolvedAt.Equal(testClock.Add(2 * time.Hour)) {
		t.Errorf("FirstResolvedAt = %v, want %v (the FIRST resolution, not the last)",
			got.FirstResolvedAt, testClock.Add(2*time.Hour))
	}
}

func TestSLAMilestonesNoneYet(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	ticketID := seedTicketForTimeline(t, s, 1)

	// Neither milestone has happened: two nils, absence is a state, not a
	// failure.
	got, err := s.SLAStore().Milestones(ctx, ticketID)
	if err != nil {
		t.Fatalf("milestones: %v", err)
	}
	if got.FirstResponseAt != nil {
		t.Errorf("FirstResponseAt = %v, want nil", got.FirstResponseAt)
	}
	if got.FirstResolvedAt != nil {
		t.Errorf("FirstResolvedAt = %v, want nil", got.FirstResolvedAt)
	}
}

// TestSLADefaultUpsertReplacesNotDuplicates proves UpsertDefault inserts a
// new priority once and REPLACES its targets on a second write, reading
// back through ListDefaults.
func TestSLADefaultUpsertReplacesNotDuplicates(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()

	// The seeded matrix already holds critical; the first upsert REPLACES
	// it, the second confirms the replacement sticks.
	first := domain.SLAPolicy{Priority: domain.PriorityCritical, FirstResponseSeconds: 900, ResolveSeconds: 7200}
	if err := s.SLAStore().UpsertDefault(ctx, first); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	second := domain.SLAPolicy{Priority: domain.PriorityCritical, FirstResponseSeconds: 600, ResolveSeconds: 5400}
	if err := s.SLAStore().UpsertDefault(ctx, second); err != nil {
		t.Fatalf("second upsert: %v", err)
	}

	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sla_defaults WHERE priority = 'critical'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("sla_defaults critical rows = %d, want 1 (upsert, not duplicate)", n)
	}
	got, err := s.SLAStore().ListDefaults(ctx)
	if err != nil {
		t.Fatalf("list defaults: %v", err)
	}
	if len(got) != len(slaDefaultSeeds) {
		t.Fatalf("defaults rows = %d, want %d (one per priority)", len(got), len(slaDefaultSeeds))
	}
	for _, p := range got {
		if p.Priority == domain.PriorityCritical && (p.FirstResponseSeconds != 600 || p.ResolveSeconds != 5400) {
			t.Errorf("critical after re-upsert = (%d, %d), want (600, 5400)", p.FirstResponseSeconds, p.ResolveSeconds)
		}
	}
}

// slaTriggerWhen returns the fixed WHEN clause for an allowlisted
// canonical priority and ok=false for anything else.
func slaTriggerWhen(priority string) (string, bool) {
	switch priority {
	case "low":
		return "WHEN NEW.priority = 'low'", true
	case "medium":
		return "WHEN NEW.priority = 'medium'", true
	case "high":
		return "WHEN NEW.priority = 'high'", true
	case "critical":
		return "WHEN NEW.priority = 'critical'", true
	default:
		return "", false
	}
}

// injectSLADefaultsFailure aborts the sla_defaults write whose NEW row has
// the given priority (test-only trigger). The batch upsert must roll every
// row back when any single write fails. The cleanup uses
// context.Background() on purpose: t.Context() is cancelled during
// cleanup and the drop-trigger query would fail with "context canceled".
func injectSLADefaultsFailure(t *testing.T, s *Store, priority string) {
	t.Helper()
	// SQLite triggers cannot bind parameters, so the priority literal goes
	// into the DDL; the allowlist keeps every statement byte fixed and
	// test-controlled.
	when, ok := slaTriggerWhen(priority)
	if !ok {
		t.Fatalf("inject sla defaults failure: unallowlisted priority %q", priority)
	}
	if _, err := s.db.ExecContext(context.Background(),
		`CREATE TRIGGER trg_test_fail_sla_defaults BEFORE INSERT ON sla_defaults
		 `+when+` BEGIN SELECT RAISE(ABORT, 'injected sla defaults failure'); END`); err != nil {
		t.Fatalf("inject sla defaults failure trigger: %v", err)
	}
	t.Cleanup(func() {
		if _, err := s.db.ExecContext(context.Background(), `DROP TRIGGER trg_test_fail_sla_defaults`); err != nil {
			t.Errorf("drop sla defaults failure trigger: %v", err)
		}
	})
}

// injectSLAPoliciesFailure aborts the sla_policies write whose NEW row has
// the given priority (test-only trigger), same contract as
// injectSLADefaultsFailure.
func injectSLAPoliciesFailure(t *testing.T, s *Store, priority string) {
	t.Helper()
	// Same allowlisted-literal rule as injectSLADefaultsFailure.
	when, ok := slaTriggerWhen(priority)
	if !ok {
		t.Fatalf("inject sla policies failure: unallowlisted priority %q", priority)
	}
	if _, err := s.db.ExecContext(context.Background(),
		`CREATE TRIGGER trg_test_fail_sla_policies BEFORE INSERT ON sla_policies
		 `+when+` BEGIN SELECT RAISE(ABORT, 'injected sla policies failure'); END`); err != nil {
		t.Fatalf("inject sla policies failure trigger: %v", err)
	}
	t.Cleanup(func() {
		if _, err := s.db.ExecContext(context.Background(), `DROP TRIGGER trg_test_fail_sla_policies`); err != nil {
			t.Errorf("drop sla policies failure trigger: %v", err)
		}
	})
}

// slaAtomicityPolicies returns a full four-priority matrix with CUSTOM
// targets, ordered so the THIRD write is 'high' — the priority the atomic
// tests inject their failure trigger on: the first two writes must land in
// the transaction and then be rolled back, proving the rollback is real
// rather than vacuous.
func slaAtomicityPolicies() []domain.SLAPolicy {
	return []domain.SLAPolicy{
		{Priority: domain.PriorityLow, FirstResponseSeconds: 1111, ResolveSeconds: 111111},
		{Priority: domain.PriorityMedium, FirstResponseSeconds: 2222, ResolveSeconds: 222222},
		{Priority: domain.PriorityHigh, FirstResponseSeconds: 3333, ResolveSeconds: 333333},
		{Priority: domain.PriorityCritical, FirstResponseSeconds: 4444, ResolveSeconds: 444444},
	}
}

// assertDefaultsUnchanged proves every sla_defaults row still carries its
// pre-call value after a failed batch upsert.
func assertDefaultsUnchanged(t *testing.T, s *Store, before []domain.SLAPolicy) {
	t.Helper()
	after, err := s.SLAStore().ListDefaults(context.Background())
	if err != nil {
		t.Fatalf("list defaults after failure: %v", err)
	}
	if len(after) != len(before) {
		t.Fatalf("defaults rows after failure = %d, want %d", len(after), len(before))
	}
	for i := range before {
		if after[i] != before[i] {
			t.Errorf("defaults[%d] = %+v, want unchanged %+v (batch write must be all or nothing)",
				i, after[i], before[i])
		}
	}
}

// assertCategoryMatrixUnchanged proves every sla_policies row of the
// category still carries its pre-call value after a failed batch upsert.
func assertCategoryMatrixUnchanged(t *testing.T, s *Store, catID int64, before []domain.SLAPolicy) {
	t.Helper()
	after, err := s.SLAStore().ListByCategory(context.Background(), catID)
	if err != nil {
		t.Fatalf("list category matrix after failure: %v", err)
	}
	if len(after) != len(before) {
		t.Fatalf("category matrix rows after failure = %d, want %d", len(after), len(before))
	}
	for i := range before {
		if after[i] != before[i] {
			t.Errorf("matrix[%d] = %+v, want unchanged %+v (batch write must be all or nothing)",
				i, after[i], before[i])
		}
	}
}

// TestSLAUpsertDefaultsAtomicRollback proves a failing THIRD write of the
// default-matrix batch leaves NONE of the four rows changed: the whole
// batch is one transaction, all or nothing.
func TestSLAUpsertDefaultsAtomicRollback(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()

	before, err := s.SLAStore().ListDefaults(ctx)
	if err != nil {
		t.Fatalf("list defaults before: %v", err)
	}

	// The third write in slaAtomicityPolicies order is high.
	injectSLADefaultsFailure(t, s, string(domain.PriorityHigh))

	err = s.SLAStore().UpsertDefaults(ctx, slaAtomicityPolicies())
	if err == nil {
		t.Fatal("UpsertDefaults succeeded, want the injected third-write failure")
	}
	assertDefaultsUnchanged(t, s, before)
}

// TestSLAUpsertCategoryTargetsAtomicRollback proves a failing THIRD write
// of a category-matrix batch leaves NONE of the four rows changed — a
// category left with two of four reprioritized targets would silently
// change the commitments frozen onto future tickets.
func TestSLAUpsertCategoryTargetsAtomicRollback(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	catID := seedCategory(t, s, "Bugs") // migration 0016 materializes the full matrix

	before, err := s.SLAStore().ListByCategory(ctx, catID)
	if err != nil {
		t.Fatalf("list category matrix before: %v", err)
	}
	if len(before) != 4 {
		t.Fatalf("materialized matrix rows = %d, want 4", len(before))
	}

	// The third write in slaAtomicityPolicies order is high.
	injectSLAPoliciesFailure(t, s, string(domain.PriorityHigh))

	err = s.SLAStore().UpsertCategoryTargets(ctx, catID, slaAtomicityPolicies())
	if err == nil {
		t.Fatal("UpsertCategoryTargets succeeded, want the injected third-write failure")
	}
	assertCategoryMatrixUnchanged(t, s, catID, before)
}

// --- batch SLA reads (issue #211): TicketSLAs and MilestonesFor. The
// single-row reads stay the canonical round-trip tests above; these prove
// the batch keying, absence semantics, chunking, and single/batch parity.

// ticketSLAEqual compares two frozen commitments field by field (the
// domain type carries no Equal method, and comparing pointers would
// compare identities, not values).
func ticketSLAEqual(a, b *domain.TicketSLA) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// milestoneInstantEqual compares two optional observed instants.
func milestoneInstantEqual(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Equal(*b)
}

// milestonesEqual compares two observed-milestone sets field by field.
func milestonesEqual(a, b domain.SLAMilestones) bool {
	return milestoneInstantEqual(a.FirstResponseAt, b.FirstResponseAt) &&
		milestoneInstantEqual(a.FirstResolvedAt, b.FirstResolvedAt)
}

// seedBatchTickets seeds n tickets into ONE category (categories are
// name-unique, so seedTicketForTimeline cannot be called repeatedly
// within one test) and returns their ids in seed order. The category is
// seeded once and reused, because a test may call the helper more than
// once (frozen plus absent tickets).
func seedBatchTickets(t *testing.T, s *Store, n int) []int64 {
	t.Helper()
	var cat int64
	err := s.db.QueryRowContext(context.Background(), `SELECT id FROM categories WHERE name = 'Bugs'`).Scan(&cat)
	if errors.Is(err, sql.ErrNoRows) {
		cat = seedCategory(t, s, "Bugs")
	} else if err != nil {
		t.Fatalf("find batch category: %v", err)
	}
	var startNumber int
	if err := s.db.QueryRowContext(context.Background(), `SELECT COALESCE(MAX(number), 1000) FROM tickets`).Scan(&startNumber); err != nil {
		t.Fatalf("read max ticket number: %v", err)
	}
	ids := make([]int64, 0, n)
	for i := range n {
		ticket := seedTicket(t, s, domain.Ticket{Number: startNumber + 1 + i, Title: "batch sla ticket",
			CategoryID: cat, Priority: domain.PriorityMedium, State: domain.StateNew,
			CreatedAt: testClock, UpdatedAt: testClock})
		ids = append(ids, ticket.ID)
	}
	return ids
}

// seedFrozenTickets seeds n tickets and freezes a distinguishable
// commitment onto each: FirstResponseSeconds/ResolveSeconds encode the
// ticket's position, so a shuffled batch read must still pair every row
// with the right id.
func seedFrozenTickets(t *testing.T, s *Store, n int) []int64 {
	t.Helper()
	ids := seedBatchTickets(t, s, n)
	ctx := context.Background()
	for i, id := range ids {
		sla := domain.TicketSLA{
			FirstResponseSeconds: 1800 + i,
			ResolveSeconds:       14400 + i,
			WarnFirstResponseAt:  testClock.Add(24 * time.Minute),
			DueFirstResponseAt:   testClock.Add(30 * time.Minute),
			WarnResolveAt:        testClock.Add(192 * time.Minute),
			DueResolveAt:         testClock.Add(4 * time.Hour),
			StartedAt:            testClock,
			PolicySnapshotAt:     testClock.Add(30 * time.Second),
		}
		if err := s.SLAStore().InsertTicketSLA(ctx, id, sla); err != nil {
			t.Fatalf("insert ticket sla %d: %v", id, err)
		}
	}
	return ids
}

func TestSLATicketSLAsBatchAbsentAndShuffled(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	ids := seedFrozenTickets(t, s, 3)
	absent := seedBatchTickets(t, s, 1)[0] // a ticket with NO frozen row

	// Shuffled, with the absent id interleaved: the answer is keyed by id,
	// never by input order.
	shuffled := []int64{ids[2], absent, ids[0], ids[1]}
	got, err := s.SLAStore().TicketSLAs(ctx, shuffled)
	if err != nil {
		t.Fatalf("batch ticket slas: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("batch rows = %d, want 3 (one per frozen ticket)", len(got))
	}
	if _, ok := got[absent]; ok {
		t.Errorf("absent ticket present in the map — absence is the \"no SLA\" state, never a zero-value commitment")
	}
	for i, id := range ids {
		g := got[id]
		if g == nil {
			t.Fatalf("ticket %d absent from the map, want its frozen row", id)
		}
		if g.FirstResponseSeconds != 1800+i || g.ResolveSeconds != 14400+i {
			t.Errorf("ticket %d targets = (%d, %d), want (%d, %d) — rows must stay paired with their ids",
				id, g.FirstResponseSeconds, g.ResolveSeconds, 1800+i, 14400+i)
		}
	}
}

func TestSLATicketSLAsEmptyIDsNoQuery(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()

	got, err := s.SLAStore().TicketSLAs(ctx, nil)
	if err != nil {
		t.Fatalf("batch ticket slas (empty): %v", err)
	}
	if len(got) != 0 {
		t.Errorf("batch rows = %d, want 0 for empty ids", len(got))
	}
}

// TestSLATicketSLAsChunking proves a request longer than one
// slaBatchChunkSize chunk still answers EVERY row: 1001 ids span three
// chunks, and the reversed request must return all 1001 rows correctly
// paired with their ids.
func TestSLATicketSLAsChunking(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	ids := seedFrozenTickets(t, s, 1001)

	rev := make([]int64, len(ids))
	for i, id := range ids {
		rev[len(ids)-1-i] = id
	}
	got, err := s.SLAStore().TicketSLAs(ctx, rev)
	if err != nil {
		t.Fatalf("batch ticket slas (1001 ids): %v", err)
	}
	if len(got) != len(ids) {
		t.Fatalf("batch rows = %d, want %d (every chunk must land)", len(got), len(ids))
	}
	for i, id := range ids {
		g := got[id]
		if g == nil {
			t.Fatalf("ticket %d missing after chunked read", id)
		}
		if g.FirstResponseSeconds != 1800+i || g.ResolveSeconds != 14400+i {
			t.Fatalf("ticket %d targets = (%d, %d), want (%d, %d) — chunked rows must stay paired with their ids",
				id, g.FirstResponseSeconds, g.ResolveSeconds, 1800+i, 14400+i)
		}
	}
}

// TestSLATicketSLAsLegacyZeroInstantsMatchSingleRead proves a legacy
// zero-instant row (the pre-0017 ” marker) reads back through the batch
// path exactly as through the single path: zero instants, never a date in
// year zero.
func TestSLATicketSLAsLegacyZeroInstantsMatchSingleRead(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	ticketID := seedTicketForTimeline(t, s, 1)

	want := domain.TicketSLA{
		FirstResponseSeconds: 1800,
		ResolveSeconds:       14400,
		StartedAt:            testClock,
		PolicySnapshotAt:     testClock,
	}
	if err := s.SLAStore().InsertTicketSLA(ctx, ticketID, want); err != nil {
		t.Fatalf("insert legacy ticket sla: %v", err)
	}

	batch, err := s.SLAStore().TicketSLAs(ctx, []int64{ticketID, 9999})
	if err != nil {
		t.Fatalf("batch ticket slas: %v", err)
	}
	got := batch[ticketID]
	if got == nil {
		t.Fatal("legacy row absent from the batch map")
	}
	if _, ok := batch[9999]; ok {
		t.Errorf("unknown id 9999 present in the batch map")
	}
	if !got.WarnFirstResponseAt.IsZero() || !got.DueFirstResponseAt.IsZero() ||
		!got.WarnResolveAt.IsZero() || !got.DueResolveAt.IsZero() {
		t.Errorf("legacy zero instants read back non-zero through the batch path: %+v", got)
	}
	single, err := s.SLAStore().TicketSLA(ctx, ticketID)
	if err != nil {
		t.Fatalf("single ticket sla: %v", err)
	}
	if !ticketSLAEqual(got, single) {
		t.Errorf("batch read %+v disagrees with single read %+v for the same legacy row", got, single)
	}
}

// TestSLABatchReadsAgreeWithSingleReads proves the direct parity contract:
// for the same rows, the batch reads answer exactly what the single reads
// answer.
func TestSLABatchReadsAgreeWithSingleReads(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	ids := seedFrozenTickets(t, s, 2)

	seedStaffComment(t, s, ids[0], "public", "agent", "first staff reply", testClock.Add(10*time.Minute))
	seedResolvedTransition(t, s, ids[0], testClock.Add(2*time.Hour))
	seedStaffComment(t, s, ids[1], "public", "admin", "later reply", testClock.Add(90*time.Minute))

	batchSLAs, err := s.SLAStore().TicketSLAs(ctx, ids)
	if err != nil {
		t.Fatalf("batch ticket slas: %v", err)
	}
	batchMs, err := s.SLAStore().MilestonesFor(ctx, ids)
	if err != nil {
		t.Fatalf("batch milestones: %v", err)
	}
	for i, id := range ids {
		single, err := s.SLAStore().TicketSLA(ctx, id)
		if err != nil {
			t.Fatalf("single ticket sla %d: %v", id, err)
		}
		if !ticketSLAEqual(batchSLAs[id], single) {
			t.Errorf("ticket %d: batch sla %+v disagrees with single %+v", id, batchSLAs[id], single)
		}
		singleMs, err := s.SLAStore().Milestones(ctx, id)
		if err != nil {
			t.Fatalf("single milestones %d: %v", id, err)
		}
		if !milestonesEqual(batchMs[id], singleMs) {
			t.Errorf("ticket %d: batch milestones %+v disagree with single %+v", id, batchMs[id], singleMs)
		}
		_ = i
	}
}

func TestSLAMilestonesForEmptyIDsNoQuery(t *testing.T) {
	s := newTestDB(t)

	got, err := s.SLAStore().MilestonesFor(context.Background(), nil)
	if err != nil {
		t.Fatalf("batch milestones (empty): %v", err)
	}
	if len(got) != 0 {
		t.Errorf("batch rows = %d, want 0 for empty ids", len(got))
	}
}

// TestSLAMilestonesForMixed proves the batch milestone contract: one
// ticket with only a first response, one with only a resolution, one with
// both, and one with neither (absent, not zero-valued) — asked together
// and shuffled.
func TestSLAMilestonesForMixed(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	ids := seedBatchTickets(t, s, 4)
	onlyResponse, onlyResolved, both, neither := ids[0], ids[1], ids[2], ids[3]

	seedStaffComment(t, s, onlyResponse, "public", "agent", "reply", testClock.Add(10*time.Minute))
	seedResolvedTransition(t, s, onlyResolved, testClock.Add(2*time.Hour))
	seedStaffComment(t, s, both, "public", "agent", "reply", testClock.Add(11*time.Minute))
	seedResolvedTransition(t, s, both, testClock.Add(3*time.Hour))

	shuffled := []int64{both, neither, onlyResolved, onlyResponse}
	got, err := s.SLAStore().MilestonesFor(ctx, shuffled)
	if err != nil {
		t.Fatalf("batch milestones: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("batch rows = %d, want 3 (the neither-observed ticket is absent)", len(got))
	}
	if _, ok := got[neither]; ok {
		t.Errorf("neither-observed ticket present in the map — absence is the state, not a zero-value entry")
	}

	if m := got[onlyResponse]; m.FirstResponseAt == nil || !m.FirstResponseAt.Equal(testClock.Add(10*time.Minute)) || m.FirstResolvedAt != nil {
		t.Errorf("only-response ticket = %+v, want FirstResponseAt +10m and FirstResolvedAt nil", m)
	}
	if m := got[onlyResolved]; m.FirstResolvedAt == nil || !m.FirstResolvedAt.Equal(testClock.Add(2*time.Hour)) || m.FirstResponseAt != nil {
		t.Errorf("only-resolved ticket = %+v, want FirstResolvedAt +2h and FirstResponseAt nil", m)
	}
	if m := got[both]; m.FirstResponseAt == nil || !m.FirstResponseAt.Equal(testClock.Add(11*time.Minute)) ||
		m.FirstResolvedAt == nil || !m.FirstResolvedAt.Equal(testClock.Add(3*time.Hour)) {
		t.Errorf("both-observed ticket = %+v, want FirstResponseAt +11m and FirstResolvedAt +3h", m)
	}
}
