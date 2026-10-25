package sqlite

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/giulianotesta7/tkt/internal/application"
)

// Instance password policy (issue #414, S1/S4): the two switches and the
// minimum length live in the keyed settings table, so no migration is needed —
// an absent row reads back the documented default. Written before the port
// exists — compile-error RED.

// passwordPolicyRows counts how many of the three keys are stored, so a test
// can prove the default needs no row and a save writes exactly three.
func passwordPolicyRows(t *testing.T, s *Store) int {
	t.Helper()
	var rows int
	if err := s.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM settings WHERE key IN (?, ?, ?)`,
		settingsKeyPasswordChangeEnabled, settingsKeyPasswordRecoveryEnabled, settingsKeyPasswordMinLength).Scan(&rows); err != nil {
		t.Fatalf("count password settings rows: %v", err)
	}
	return rows
}

// TestPasswordSettingsStoreDefaultsWithoutRows proves a fresh instance needs no
// stored row: both self-service paths are ON and the minimum is 12 (S1, S4).
func TestPasswordSettingsStoreDefaultsWithoutRows(t *testing.T) {
	s := newTestDB(t)

	if got := passwordPolicyRows(t, s); got != 0 {
		t.Fatalf("password settings rows on a fresh database = %d, want 0 (the default needs no row)", got)
	}
	got, err := s.PasswordSettingsStore().GetPasswordSettings(context.Background())
	if err != nil {
		t.Fatalf("get password settings: %v", err)
	}
	want := application.PasswordSettings{ChangeEnabled: true, RecoveryEnabled: true, MinLength: 12}
	if got != want {
		t.Fatalf("password settings = %+v, want the documented default %+v", got, want)
	}
}

// TestPasswordSettingsStoreRoundTrip proves a save persists all three values
// and reads back exactly what an administrator submitted.
func TestPasswordSettingsStoreRoundTrip(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	st := s.PasswordSettingsStore()

	want := application.PasswordSettings{ChangeEnabled: false, RecoveryEnabled: false, MinLength: application.MinPasswordMinLength}
	if err := st.SetPasswordSettings(ctx, want); err != nil {
		t.Fatalf("set password settings: %v", err)
	}
	got, err := st.GetPasswordSettings(ctx)
	if err != nil {
		t.Fatalf("get password settings: %v", err)
	}
	if got != want {
		t.Fatalf("password settings = %+v, want %+v", got, want)
	}
	if rows := passwordPolicyRows(t, s); rows != 3 {
		t.Errorf("password settings rows = %d, want exactly 3 (one per key, never duplicated)", rows)
	}

	// A second save overwrites in place rather than accumulating rows.
	want = application.PasswordSettings{ChangeEnabled: true, RecoveryEnabled: true, MinLength: application.MaxPasswordMinLength}
	if err := st.SetPasswordSettings(ctx, want); err != nil {
		t.Fatalf("set password settings again: %v", err)
	}
	if got, err := st.GetPasswordSettings(ctx); err != nil || got != want {
		t.Fatalf("password settings after the second save = %+v (err %v), want %+v", got, err, want)
	}
	if rows := passwordPolicyRows(t, s); rows != 3 {
		t.Errorf("password settings rows after the second save = %d, want exactly 3", rows)
	}
}

// TestPasswordSettingsStoreReadsFailSafe covers the hand-edited database the
// port documents: an unparseable or out-of-band minimum reads back the default
// 12, and an unparseable switch reads back ON. A stored value the write path
// refuses is never trusted to weaken or block every password surface.
func TestPasswordSettingsStoreReadsFailSafe(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()

	cases := []struct {
		name         string
		stored       map[string]string
		wantChange   bool
		wantRecovery bool
		wantMin      int
	}{
		{
			name:         "minimum is not a number",
			stored:       map[string]string{settingsKeyPasswordMinLength: "twelve"},
			wantChange:   true,
			wantRecovery: true,
			wantMin:      application.DefaultPasswordMinLength,
		},
		{
			name:         "minimum is below the band",
			stored:       map[string]string{settingsKeyPasswordMinLength: "7"},
			wantChange:   true,
			wantRecovery: true,
			wantMin:      application.DefaultPasswordMinLength,
		},
		{
			name:         "minimum is above the band",
			stored:       map[string]string{settingsKeyPasswordMinLength: "65"},
			wantChange:   true,
			wantRecovery: true,
			wantMin:      application.DefaultPasswordMinLength,
		},
		{
			name:         "minimum is empty",
			stored:       map[string]string{settingsKeyPasswordMinLength: ""},
			wantChange:   true,
			wantRecovery: true,
			wantMin:      application.DefaultPasswordMinLength,
		},
		{
			name: "switches are unparseable",
			stored: map[string]string{
				settingsKeyPasswordChangeEnabled:   "yes",
				settingsKeyPasswordRecoveryEnabled: "",
			},
			wantChange:   true,
			wantRecovery: true,
			wantMin:      application.DefaultPasswordMinLength,
		},
		{
			name: "switches are off",
			stored: map[string]string{
				settingsKeyPasswordChangeEnabled:   "0",
				settingsKeyPasswordRecoveryEnabled: "0",
				settingsKeyPasswordMinLength:       "9",
			},
			wantChange:   false,
			wantRecovery: false,
			wantMin:      9,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for key, value := range tc.stored {
				if _, err := s.db.ExecContext(ctx, `
					INSERT INTO settings (key, value) VALUES (?, ?)
					ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value); err != nil {
					t.Fatalf("store %s=%q: %v", key, value, err)
				}
			}
			got, err := s.PasswordSettingsStore().GetPasswordSettings(ctx)
			if err != nil {
				t.Fatalf("get password settings: %v", err)
			}
			want := application.PasswordSettings{ChangeEnabled: tc.wantChange, RecoveryEnabled: tc.wantRecovery, MinLength: tc.wantMin}
			if got != want {
				t.Fatalf("password settings = %+v, want %+v", got, want)
			}
		})
	}
}

// TestPasswordSettingsStoreSetRollsBackAtomically proves the three values are
// ONE unit: aborting the last write leaves the previous policy standing, never
// the switches of a save the minimum never reached.
func TestPasswordSettingsStoreSetRollsBackAtomically(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	st := s.PasswordSettingsStore()

	seeded := application.PasswordSettings{ChangeEnabled: true, RecoveryEnabled: true, MinLength: 12}
	if err := st.SetPasswordSettings(ctx, seeded); err != nil {
		t.Fatalf("seed policy: %v", err)
	}

	// Abort the LAST write of the three so the transaction has to roll the
	// switches back with it.
	if _, err := s.db.ExecContext(ctx, `
		CREATE TRIGGER abort_password_min_length BEFORE INSERT ON settings
		WHEN NEW.key = '`+settingsKeyPasswordMinLength+`'
		BEGIN SELECT RAISE(ABORT, 'boom'); END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}
	t.Cleanup(func() {
		// context.Background(), NOT t.Context(): t.Context() is cancelled during
		// cleanup, so the drop would fail with "context canceled".
		if _, err := s.db.ExecContext(context.Background(), `DROP TRIGGER abort_password_min_length`); err != nil {
			t.Errorf("drop trigger: %v", err)
		}
	})

	err := st.SetPasswordSettings(ctx, application.PasswordSettings{ChangeEnabled: false, RecoveryEnabled: false, MinLength: 8})
	if err == nil {
		t.Fatal("the save succeeded with its last write aborted, want an error")
	}
	if got, getErr := st.GetPasswordSettings(ctx); getErr != nil || got != seeded {
		t.Errorf("password settings after the rollback = %+v (err %v), want the seeded %+v", got, getErr, seeded)
	}
}

// --- One snapshot: the three rows in ONE statement (issue #414 review) ---

// statementCountingConnector wraps the real driver and counts the statements
// issued through it, so a test can prove a read is one query instead of three.
// It forwards every database/sql fast path the driver implements, so nothing
// but the count differs from the production path.
type statementCountingConnector struct {
	inner driver.Driver
	dsn   string
	count *int64
}

func (c statementCountingConnector) Driver() driver.Driver { return c.inner }

func (c statementCountingConnector) Connect(context.Context) (driver.Conn, error) {
	conn, err := c.inner.Open(c.dsn)
	if err != nil {
		return nil, err
	}
	queryer, ok := conn.(driver.QueryerContext)
	if !ok {
		conn.Close()
		return nil, fmt.Errorf("driver connection does not implement QueryerContext")
	}
	execer, ok := conn.(driver.ExecerContext)
	if !ok {
		conn.Close()
		return nil, fmt.Errorf("driver connection does not implement ExecerContext")
	}
	beginner, ok := conn.(driver.ConnBeginTx)
	if !ok {
		conn.Close()
		return nil, fmt.Errorf("driver connection does not implement ConnBeginTx")
	}
	return &statementCountingConn{Conn: conn, queryer: queryer, execer: execer, beginner: beginner, count: c.count}, nil
}

// statementCountingConn counts each QueryContext/ExecContext call.
type statementCountingConn struct {
	driver.Conn
	queryer  driver.QueryerContext
	execer   driver.ExecerContext
	beginner driver.ConnBeginTx
	count    *int64
}

func (c *statementCountingConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	atomic.AddInt64(c.count, 1)
	return c.queryer.QueryContext(ctx, query, args)
}

func (c *statementCountingConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	atomic.AddInt64(c.count, 1)
	return c.execer.ExecContext(ctx, query, args)
}

func (c *statementCountingConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	return c.beginner.BeginTx(ctx, opts)
}

// newStatementCountingTestDB opens the same isolated in-memory database as
// newTestDB, migrated over the real embedded set, through the counting driver.
func newStatementCountingTestDB(t *testing.T) (*Store, *int64) {
	t.Helper()
	dsn := testDSN(t)
	probe, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open probe db: %v", err)
	}
	inner := probe.Driver()
	probe.Close()

	count := new(int64)
	db := sql.OpenDB(statementCountingConnector{inner: inner, dsn: dsn, count: count})
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	s := &Store{db: db}
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate test db: %v", err)
	}
	return s, count
}

// TestPasswordSettingsStoreReadsOneSnapshotInOneQuery pins the fix: the policy
// is read with a SINGLE statement, so a concurrent Set — which commits all
// three rows in one transaction — can never be interleaved between the reads and
// answer the minimum of one save next to the switches of another.
func TestPasswordSettingsStoreReadsOneSnapshotInOneQuery(t *testing.T) {
	s, count := newStatementCountingTestDB(t)
	ctx := context.Background()
	st := s.PasswordSettingsStore()

	if err := st.SetPasswordSettings(ctx, application.PasswordSettings{
		ChangeEnabled: false, RecoveryEnabled: true, MinLength: 30,
	}); err != nil {
		t.Fatalf("set password settings: %v", err)
	}

	before := atomic.LoadInt64(count)
	got, err := st.GetPasswordSettings(ctx)
	if err != nil {
		t.Fatalf("get password settings: %v", err)
	}
	want := application.PasswordSettings{ChangeEnabled: false, RecoveryEnabled: true, MinLength: 30}
	if got != want {
		t.Fatalf("password settings = %+v, want %+v", got, want)
	}
	if queries := atomic.LoadInt64(count) - before; queries != 1 {
		t.Fatalf("GetPasswordSettings issued %d statements, want exactly 1 (one snapshot, never three reads)", queries)
	}
}

// TestPasswordSettingsStoreDefaultAppliesPerMissingRow proves the fail-safe
// fallback is per ROW, not per policy: a stored switch beside two absent rows
// keeps its own value while each missing row answers the documented default.
func TestPasswordSettingsStoreDefaultAppliesPerMissingRow(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()

	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO settings (key, value) VALUES (?, '0')
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, settingsKeyPasswordRecoveryEnabled); err != nil {
		t.Fatalf("store the recovery switch: %v", err)
	}
	if rows := passwordPolicyRows(t, s); rows != 1 {
		t.Fatalf("password settings rows = %d, want 1 (only the stored switch)", rows)
	}

	got, err := s.PasswordSettingsStore().GetPasswordSettings(ctx)
	if err != nil {
		t.Fatalf("get password settings: %v", err)
	}
	want := application.PasswordSettings{
		ChangeEnabled:   true,                                 // absent row -> ON
		RecoveryEnabled: false,                                // stored "0"
		MinLength:       application.DefaultPasswordMinLength, // absent row -> 12
	}
	if got != want {
		t.Fatalf("password settings = %+v, want the stored switch beside per-row defaults %+v", got, want)
	}
}
