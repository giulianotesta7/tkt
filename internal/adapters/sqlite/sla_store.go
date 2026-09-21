package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/giulianotesta7/tkt/internal/application"
	"github.com/giulianotesta7/tkt/internal/domain"
)

// slaStore implements application.SLAStore over the sla_defaults,
// sla_policies, ticket_sla, comments, and audit_events tables (issue
// #211). The global defaults are seeded by migration 0014; a category's
// matrix is materialized by the migration-0016 trigger at category
// creation time, and a ticket's targets are frozen at creation and never
// updated afterwards.
type slaStore struct {
	db *sql.DB
}

var _ application.SLAStore = (*slaStore)(nil)

func newSLAStore(db *sql.DB) *slaStore { return &slaStore{db: db} }

// ticketSLAColumns is the SELECT column list shared by the single-row and
// the batch ticket_sla reads — one source of truth so the two reads
// cannot drift.
const ticketSLAColumns = `first_response_seconds, resolve_seconds, started_at, policy_snapshot_at,
		warn_first_response_at, due_first_response_at, warn_resolve_at, due_resolve_at`

// slaFirstResponseWhere is the first-response predicate shared by the
// single and batch milestone reads — one source of truth so the two
// reads cannot drift: only a PUBLIC comment by provable staff counts, so
// a NULL author_role legacy comment never counts as a staff response.
const slaFirstResponseWhere = `visibility = 'public' AND author_role IN ('agent','admin','root')`

// slaFirstResolvedWhere is the first-resolution predicate shared by the
// single and batch milestone reads — one source of truth so the two
// reads cannot drift.
const slaFirstResolvedWhere = `action = 'transition' AND field = 'state' AND to_value = 'resolved'`

// slaBatchChunkSize bounds the id list of ONE batch SLA read query (issue
// #211): SQLite refuses an IN (...) list with more bound parameters than
// its SQLITE_MAX_VARIABLE_NUMBER limit allows, so a request longer than
// one chunk is answered in chunks of 500 — comfortably inside the
// historical 999 default limit even with the statement's other bound
// parameters, and ONE named constant so both batch reads chunk
// identically.
const slaBatchChunkSize = 500

// ListDefaults returns the global default targets (sla_defaults) in the
// canonical priority order: critical, high, medium, low.
func (ss *slaStore) ListDefaults(ctx context.Context) ([]domain.SLAPolicy, error) {
	rows, err := ss.db.QueryContext(ctx, `
		SELECT priority, first_response_seconds, resolve_seconds FROM sla_defaults
		ORDER BY CASE priority WHEN 'critical' THEN 0 WHEN 'high' THEN 1 WHEN 'medium' THEN 2 ELSE 3 END`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list sla defaults: %w", err)
	}
	defer rows.Close()
	var out []domain.SLAPolicy
	for rows.Next() {
		p, err := scanSLAPolicy(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: scan sla default: %w", err)
		}
		out = append(out, *p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: list sla defaults: %w", err)
	}
	return out, nil
}

// ListByCategory returns one category's materialized matrix (all its
// sla_policies rows) in the canonical priority order. A category without
// rows returns an empty result and a nil error — absence is a state, not
// a failure.
func (ss *slaStore) ListByCategory(ctx context.Context, categoryID int64) ([]domain.SLAPolicy, error) {
	rows, err := ss.db.QueryContext(ctx, `
		SELECT priority, first_response_seconds, resolve_seconds FROM sla_policies
		WHERE category_id = ?
		ORDER BY CASE priority WHEN 'critical' THEN 0 WHEN 'high' THEN 1 WHEN 'medium' THEN 2 ELSE 3 END`, categoryID)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list sla policies: %w", err)
	}
	defer rows.Close()
	var out []domain.SLAPolicy
	for rows.Next() {
		p, err := scanSLAPolicy(rows)
		if err != nil {
			return nil, fmt.Errorf("sqlite: scan sla policy: %w", err)
		}
		out = append(out, *p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: list sla policies: %w", err)
	}
	return out, nil
}

// UpsertDefault writes one sla_defaults row (priority, both targets),
// replacing the targets when the priority already exists — never
// duplicating it. It mirrors the single-row UpsertCategoryTarget.
func (ss *slaStore) UpsertDefault(ctx context.Context, policy domain.SLAPolicy) error {
	tx, err := ss.db.BeginTx(ctx, nil) // _txlock=immediate → BEGIN IMMEDIATE
	if err != nil {
		return fmt.Errorf("sqlite: begin sla default upsert: %w", err)
	}
	if err := upsertDefaultTx(ctx, tx, policy); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlite: commit sla default upsert: %w", err)
	}
	return nil
}

// upsertDefaultTx writes one sla_defaults row inside the CALLER's
// transaction. Exactly one SQL statement, so the batch writers below can
// share it: having this single helper means there is exactly one place
// that writes sla_defaults.
func upsertDefaultTx(ctx context.Context, tx *sql.Tx, policy domain.SLAPolicy) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO sla_defaults (priority, first_response_seconds, resolve_seconds)
		VALUES (?, ?, ?)
		ON CONFLICT(priority) DO UPDATE SET
			first_response_seconds = excluded.first_response_seconds,
			resolve_seconds = excluded.resolve_seconds`,
		policy.Priority, policy.FirstResponseSeconds, policy.ResolveSeconds)
	if err != nil {
		return fmt.Errorf("sqlite: upsert sla default: %w", err)
	}
	return nil
}

// UpsertDefaults writes ALL the given sla_defaults rows in ONE
// transaction — all or nothing: any single failing write rolls back every
// row, leaving the table exactly as it was. A partial default matrix is
// never persisted (the same defect class the appearance settings once
// shipped: one write landing before a later write failed).
func (ss *slaStore) UpsertDefaults(ctx context.Context, policies []domain.SLAPolicy) error {
	tx, err := ss.db.BeginTx(ctx, nil) // _txlock=immediate → BEGIN IMMEDIATE
	if err != nil {
		return fmt.Errorf("sqlite: begin sla defaults upsert: %w", err)
	}
	for _, p := range policies {
		if err := upsertDefaultTx(ctx, tx, p); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlite: commit sla defaults upsert: %w", err)
	}
	return nil
}

// UpsertCategoryTarget writes one matrix row (category, priority, both
// targets), replacing the targets when the (category, priority) pair
// already exists — never duplicating it.
func (ss *slaStore) UpsertCategoryTarget(ctx context.Context, categoryID int64, policy domain.SLAPolicy) error {
	_, err := ss.db.ExecContext(ctx, `
		INSERT INTO sla_policies (category_id, priority, first_response_seconds, resolve_seconds)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(category_id, priority) DO UPDATE SET
			first_response_seconds = excluded.first_response_seconds,
			resolve_seconds = excluded.resolve_seconds`,
		categoryID, policy.Priority, policy.FirstResponseSeconds, policy.ResolveSeconds)
	if err != nil {
		return fmt.Errorf("sqlite: upsert sla policy: %w", err)
	}
	return nil
}

// upsertCategoryTargetTx writes one sla_policies row inside the CALLER's
// transaction. Exactly one SQL statement, so UpsertCategoryTargets can
// share it.
func upsertCategoryTargetTx(ctx context.Context, tx *sql.Tx, categoryID int64, policy domain.SLAPolicy) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO sla_policies (category_id, priority, first_response_seconds, resolve_seconds)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(category_id, priority) DO UPDATE SET
			first_response_seconds = excluded.first_response_seconds,
			resolve_seconds = excluded.resolve_seconds`,
		categoryID, policy.Priority, policy.FirstResponseSeconds, policy.ResolveSeconds)
	if err != nil {
		return fmt.Errorf("sqlite: upsert sla policy: %w", err)
	}
	return nil
}

// UpsertCategoryTargets writes ALL the given rows of ONE category's
// matrix in ONE transaction — all or nothing: any single failing write
// rolls back every row, leaving the category's matrix exactly as it was.
// A category left with a partial matrix would silently change the
// commitments frozen onto future tickets, so a failed matrix edit must
// never land halfway.
func (ss *slaStore) UpsertCategoryTargets(ctx context.Context, categoryID int64, policies []domain.SLAPolicy) error {
	tx, err := ss.db.BeginTx(ctx, nil) // _txlock=immediate → BEGIN IMMEDIATE
	if err != nil {
		return fmt.Errorf("sqlite: begin sla category matrix upsert: %w", err)
	}
	for _, p := range policies {
		if err := upsertCategoryTargetTx(ctx, tx, categoryID, p); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlite: commit sla category matrix upsert: %w", err)
	}
	return nil
}

// TicketSLA returns the commitment frozen onto a ticket, or (nil, nil)
// when the ticket has NO ticket_sla row — the legitimate state for legacy
// tickets and for tickets created while SLA was disabled.
func (ss *slaStore) TicketSLA(ctx context.Context, ticketID int64) (*domain.TicketSLA, error) {
	var sla domain.TicketSLA
	var startedAt, policySnapshotAt, warnFR, dueFR, warnRes, dueRes string
	err := ss.db.QueryRowContext(ctx, `
		SELECT `+ticketSLAColumns+`
		FROM ticket_sla WHERE ticket_id = ?`, ticketID).
		Scan(ticketSLAScanDest(&sla, &startedAt, &policySnapshotAt, &warnFR, &dueFR, &warnRes, &dueRes)...)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("sqlite: get ticket sla: %w", err)
	}
	if err := parseTicketSLA(&sla, startedAt, policySnapshotAt, warnFR, dueFR, warnRes, dueRes); err != nil {
		return nil, fmt.Errorf("sqlite: get ticket sla: %w", err)
	}
	return &sla, nil
}

// ticketSLAScanDest returns the scan destination for ticketSLAColumns, in
// column order — one destination list shared by the single and batch
// reads so the scan cannot drift from the SELECT.
func ticketSLAScanDest(sla *domain.TicketSLA, startedAt, policySnapshotAt, warnFR, dueFR, warnRes, dueRes *string) []any {
	return []any{
		&sla.FirstResponseSeconds, &sla.ResolveSeconds, startedAt, policySnapshotAt,
		warnFR, dueFR, warnRes, dueRes,
	}
}

// parseTicketSLA parses the two TEXT timestamps and the four frozen
// instants of one scanned ticket_sla row into sla. Shared by the single
// and batch reads: one instant parser, no drift.
func parseTicketSLA(sla *domain.TicketSLA, startedAt, policySnapshotAt, warnFR, dueFR, warnRes, dueRes string) error {
	var err error
	if sla.StartedAt, err = time.Parse(timeLayout, startedAt); err != nil {
		return fmt.Errorf("parse ticket sla started_at %q: %w", startedAt, err)
	}
	if sla.PolicySnapshotAt, err = time.Parse(timeLayout, policySnapshotAt); err != nil {
		return fmt.Errorf("parse ticket sla policy_snapshot_at %q: %w", policySnapshotAt, err)
	}
	// The four frozen instants (migration 0017): '' is the pre-0017 legacy
	// marker and reads back as the zero time — "no frozen SLA" for the
	// projection, never a date in year zero.
	if sla.WarnFirstResponseAt, err = parseSLAInstant(warnFR); err != nil {
		return fmt.Errorf("parse ticket sla warn_first_response_at %q: %w", warnFR, err)
	}
	if sla.DueFirstResponseAt, err = parseSLAInstant(dueFR); err != nil {
		return fmt.Errorf("parse ticket sla due_first_response_at %q: %w", dueFR, err)
	}
	if sla.WarnResolveAt, err = parseSLAInstant(warnRes); err != nil {
		return fmt.Errorf("parse ticket sla warn_resolve_at %q: %w", warnRes, err)
	}
	if sla.DueResolveAt, err = parseSLAInstant(dueRes); err != nil {
		return fmt.Errorf("parse ticket sla due_resolve_at %q: %w", dueRes, err)
	}
	return nil
}

// TicketSLAs returns the commitments frozen onto many tickets, keyed by
// ticket id. A ticket with NO row is ABSENT from the map — absence is the
// "no SLA" state, never a zero-value commitment. An empty ticketIDs
// answers an empty map WITHOUT querying (an IN () list is a SQL syntax
// error) and without an error. The id list is chunked (slaBatchChunkSize)
// to stay well inside SQLite's bound-parameter limit, and the result is
// correct across chunks; rows are answered in no particular order — the
// map is keyed by id.
func (ss *slaStore) TicketSLAs(ctx context.Context, ticketIDs []int64) (map[int64]*domain.TicketSLA, error) {
	out := make(map[int64]*domain.TicketSLA, len(ticketIDs))
	if len(ticketIDs) == 0 {
		return out, nil
	}
	for start := 0; start < len(ticketIDs); start += slaBatchChunkSize {
		end := start + slaBatchChunkSize
		if end > len(ticketIDs) {
			end = len(ticketIDs)
		}
		if err := ss.ticketSLAsChunk(ctx, ticketIDs[start:end], out); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// ticketSLAsChunk reads ONE chunk of ids into out, sharing the single
// read's column list and parser.
func (ss *slaStore) ticketSLAsChunk(ctx context.Context, ids []int64, out map[int64]*domain.TicketSLA) error {
	placeholders, args := slaChunkArgs(ids)
	rows, err := ss.db.QueryContext(ctx, `
		SELECT ticket_id, `+ticketSLAColumns+`
		FROM ticket_sla WHERE ticket_id IN (`+placeholders+`)`, args...)
	if err != nil {
		return fmt.Errorf("sqlite: get ticket slas: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		sla := &domain.TicketSLA{}
		var startedAt, policySnapshotAt, warnFR, dueFR, warnRes, dueRes string
		dest := append([]any{&id},
			ticketSLAScanDest(sla, &startedAt, &policySnapshotAt, &warnFR, &dueFR, &warnRes, &dueRes)...)
		if err := rows.Scan(dest...); err != nil {
			return fmt.Errorf("sqlite: scan ticket sla: %w", err)
		}
		if err := parseTicketSLA(sla, startedAt, policySnapshotAt, warnFR, dueFR, warnRes, dueRes); err != nil {
			return fmt.Errorf("sqlite: get ticket slas: %w", err)
		}
		out[id] = sla
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("sqlite: get ticket slas: %w", err)
	}
	return nil
}

// slaChunkArgs renders ONE chunk of ids as its `?, ?, ...` placeholder
// list and bound arguments — the shared IN-list shape of both batch SLA
// reads.
func slaChunkArgs(ids []int64) (string, []any) {
	placeholders := strings.TrimSuffix(strings.Repeat("?, ", len(ids)), ", ")
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	return placeholders, args
}

// InsertTicketSLA freezes a commitment onto one ticket inside its own
// small transaction — the only writer of the row. Creation-time write:
// the row is never updated afterwards.
func (ss *slaStore) InsertTicketSLA(ctx context.Context, ticketID int64, sla domain.TicketSLA) error {
	tx, err := ss.db.BeginTx(ctx, nil) // _txlock=immediate → BEGIN IMMEDIATE
	if err != nil {
		return fmt.Errorf("sqlite: begin ticket sla insert: %w", err)
	}
	if err := insertTicketSLATx(ctx, tx, ticketID, &sla); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlite: commit ticket sla insert: %w", err)
	}
	return nil
}

// insertTicketSLATx writes the frozen SLA row inside the CALLER's
// transaction (ticket creation runs in the ticket unit of work, which
// already owns a tx; this helper is exactly one write, so it must not
// open its own). A nil sla is a no-op: a ticket created while SLA is
// disabled simply has no frozen commitment. Having this single helper
// means there is exactly one place that writes ticket_sla.
func insertTicketSLATx(ctx context.Context, tx *sql.Tx, ticketID int64, sla *domain.TicketSLA) error {
	if sla == nil {
		return nil
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO ticket_sla (ticket_id, first_response_seconds, resolve_seconds, started_at, policy_snapshot_at,
			warn_first_response_at, due_first_response_at, warn_resolve_at, due_resolve_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		ticketID, sla.FirstResponseSeconds, sla.ResolveSeconds,
		formatTime(sla.StartedAt), formatTime(sla.PolicySnapshotAt),
		formatSLAInstant(sla.WarnFirstResponseAt), formatSLAInstant(sla.DueFirstResponseAt),
		formatSLAInstant(sla.WarnResolveAt), formatSLAInstant(sla.DueResolveAt))
	if err != nil {
		return fmt.Errorf("sqlite: insert ticket sla: %w", err)
	}
	return nil
}

// formatSLAInstant stores one frozen warning/due instant, or ” when it
// is the zero time. ” is the migration-0017 legacy marker: every row the
// application writes carries real instants, so only pre-0017 rows (and
// zero-instant commitments) store it, and the store reads it back as the
// zero time — "no frozen SLA" for the projection, never a date in year
// zero.
func formatSLAInstant(t time.Time) any {
	if t.IsZero() {
		return ""
	}
	return formatTime(t)
}

// parseSLAInstant parses one stored frozen instant; ” — the pre-0017
// legacy marker — reads back as the zero time.
func parseSLAInstant(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	return time.Parse(timeLayout, value)
}

// Milestones returns the FIRST public staff response and the FIRST
// resolution instants for one ticket, each nil when it has not happened.
//
// Both are FIRSTS on purpose: the SLA measures the commitment made at
// ticket creation, so it takes the first — this deliberately differs from
// ticket_metrics.go, which takes the LAST resolution (ROW_NUMBER() ...
// ORDER BY created_at DESC) because a report counts each closure, not the
// creation-time promise. The predicates live in the shared
// slaFirstResponseWhere / slaFirstResolvedWhere consts so the single and
// batch reads cannot drift.
func (ss *slaStore) Milestones(ctx context.Context, ticketID int64) (domain.SLAMilestones, error) {
	var firstResponse, firstResolved sql.NullString
	err := ss.db.QueryRowContext(ctx, `
		SELECT
		  (SELECT MIN(created_at) FROM comments
		    WHERE ticket_id = ? AND `+slaFirstResponseWhere+`),
		  (SELECT MIN(created_at) FROM audit_events
		    WHERE ticket_id = ? AND `+slaFirstResolvedWhere+`)`,
		ticketID, ticketID).Scan(&firstResponse, &firstResolved)
	if err != nil {
		return domain.SLAMilestones{}, fmt.Errorf("sqlite: sla milestones: %w", err)
	}
	var m domain.SLAMilestones
	var err2 error
	if m.FirstResponseAt, err2 = parseSLAMilestone(firstResponse, "first response"); err2 != nil {
		return domain.SLAMilestones{}, err2
	}
	if m.FirstResolvedAt, err2 = parseSLAMilestone(firstResolved, "first resolution"); err2 != nil {
		return domain.SLAMilestones{}, err2
	}
	return m, nil
}

// parseSLAMilestone parses one observed milestone MIN(created_at): an
// absent observation (NULL — no qualifying row) reads back nil. Shared by
// the single and batch milestone reads: one parser, no drift.
func parseSLAMilestone(value sql.NullString, label string) (*time.Time, error) {
	if !value.Valid {
		return nil, nil
	}
	t, err := time.Parse(timeLayout, value.String)
	if err != nil {
		return nil, fmt.Errorf("sqlite: parse %s %q: %w", label, value.String, err)
	}
	return &t, nil
}

// MilestonesFor returns the FIRST public staff response and the FIRST
// resolution instants for many tickets, keyed by ticket id, using the
// SAME two milestone predicates as Milestones. A ticket with NEITHER
// observation is ABSENT from the map; a ticket with only one observation
// is PRESENT with the other field nil. An empty ticketIDs answers an
// empty map WITHOUT querying (an IN () list is a SQL syntax error) and
// without an error. The id list is chunked (slaBatchChunkSize) to stay
// well inside SQLite's bound-parameter limit, and the result is correct
// across chunks; rows are answered in no particular order — the map is
// keyed by id.
func (ss *slaStore) MilestonesFor(ctx context.Context, ticketIDs []int64) (map[int64]domain.SLAMilestones, error) {
	out := make(map[int64]domain.SLAMilestones, len(ticketIDs))
	if len(ticketIDs) == 0 {
		return out, nil
	}
	for start := 0; start < len(ticketIDs); start += slaBatchChunkSize {
		end := start + slaBatchChunkSize
		if end > len(ticketIDs) {
			end = len(ticketIDs)
		}
		if err := ss.milestonesForChunk(ctx, ticketIDs[start:end], out); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// milestonesForChunk runs the two milestone predicates over ONE chunk of
// ids — one query each, so the bound parameters of a single statement
// stay inside SQLite's limit — and merges both observations into out, one
// result per ticket.
func (ss *slaStore) milestonesForChunk(ctx context.Context, ids []int64, out map[int64]domain.SLAMilestones) error {
	placeholders, args := slaChunkArgs(ids)
	if err := ss.mergeMilestonesForChunk(ctx, `
		SELECT ticket_id, MIN(created_at) FROM comments
		WHERE ticket_id IN (`+placeholders+`) AND `+slaFirstResponseWhere+`
		GROUP BY ticket_id`, args, "first response",
		func(m *domain.SLAMilestones, at *time.Time) { m.FirstResponseAt = at }, out); err != nil {
		return err
	}
	return ss.mergeMilestonesForChunk(ctx, `
		SELECT ticket_id, MIN(created_at) FROM audit_events
		WHERE ticket_id IN (`+placeholders+`) AND `+slaFirstResolvedWhere+`
		GROUP BY ticket_id`, args, "first resolution",
		func(m *domain.SLAMilestones, at *time.Time) { m.FirstResolvedAt = at }, out)
}

// mergeMilestonesForChunk runs ONE grouped milestone query (ticket_id,
// MIN(created_at)) over a chunk of ids and merges each parsed instant
// into out through setAt, so a ticket with both observations ends up with
// both fields and one with a single observation stays present with the
// other field nil.
func (ss *slaStore) mergeMilestonesForChunk(
	ctx context.Context, query string, args []any, label string,
	setAt func(m *domain.SLAMilestones, at *time.Time),
	out map[int64]domain.SLAMilestones,
) error {
	rows, err := ss.db.QueryContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("sqlite: sla milestones: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var at sql.NullString
		if err := rows.Scan(&id, &at); err != nil {
			return fmt.Errorf("sqlite: scan sla milestones: %w", err)
		}
		parsed, err := parseSLAMilestone(at, label)
		if err != nil {
			return err
		}
		m := out[id]
		setAt(&m, parsed)
		out[id] = m
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("sqlite: sla milestones: %w", err)
	}
	return nil
}

// scanSLAPolicy projects one policy row into a domain.SLAPolicy.
func scanSLAPolicy(scan rowScanner) (*domain.SLAPolicy, error) {
	var p domain.SLAPolicy
	if err := scan.Scan(&p.Priority, &p.FirstResponseSeconds, &p.ResolveSeconds); err != nil {
		return nil, err
	}
	return &p, nil
}
