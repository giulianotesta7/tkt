package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// Instance appearance settings store (0005_instance_settings.sql): the
// migration seeds the default, writes round-trip, and an absent row falls
// back to the same default (fail-open read, service-level validation).

func TestSettingsStoreDefaultSeededByMigration(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()

	got, err := s.SettingsStore().GetInternalCommentBg(ctx)
	if err != nil {
		t.Fatalf("get default: %v", err)
	}
	if got != "#E8EEFF" {
		t.Errorf("default internal comment bg = %q, want %q", got, "#E8EEFF")
	}
}

func TestSettingsStoreRoundTrip(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()

	if err := s.SettingsStore().SetInternalCommentBg(ctx, "#EFE9FB"); err != nil {
		t.Fatalf("set: %v", err)
	}
	got, err := s.SettingsStore().GetInternalCommentBg(ctx)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got != "#EFE9FB" {
		t.Errorf("internal comment bg = %q, want %q", got, "#EFE9FB")
	}
}

func TestSettingsStoreAbsentRowFallsBackToDefault(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()

	// Remove the seeded row: the store must still answer the default.
	if _, err := s.db.ExecContext(ctx, `DELETE FROM settings WHERE key = 'internal_comment_bg'`); err != nil {
		t.Fatalf("delete settings row: %v", err)
	}
	got, err := s.SettingsStore().GetInternalCommentBg(ctx)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got != "#E8EEFF" {
		t.Errorf("internal comment bg = %q, want default %q", got, "#E8EEFF")
	}
}

// SLA settings keys (migration 0013, issue #211): the seeded values read
// back, an absent row falls back to the documented default, and
// sla_enabled parses the stored '0'/'1' text fail-safe.
func TestSettingsStoreSLAKeysSeededByMigration(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()

	// Seeded OFF on purpose: a migration never turns on a customer-facing
	// commitment by itself.
	enabled, err := s.SettingsStore().GetSLAEnabled(ctx)
	if err != nil {
		t.Fatalf("get sla_enabled: %v", err)
	}
	if enabled {
		t.Error("sla_enabled = true, want false (seeded OFF)")
	}

	percent, err := s.SettingsStore().GetSLAWarningPercent(ctx)
	if err != nil {
		t.Fatalf("get sla_warning_percent: %v", err)
	}
	if percent != 80 {
		t.Errorf("sla_warning_percent = %d, want 80", percent)
	}

	// sla_enabled_at is deliberately NOT seeded, so it reads back the zero
	// time: nothing has switched the feature on yet, and inventing an instant
	// would claim an activation that never happened.
	enabledAt, err := s.SettingsStore().GetSLAEnabledAt(ctx)
	if err != nil {
		t.Fatalf("get sla_enabled_at: %v", err)
	}
	if !enabledAt.IsZero() {
		t.Errorf("sla_enabled_at = %v, want the zero time (never seeded)", enabledAt)
	}
}

func TestSettingsStoreSLAKeysAbsentRowFallsBackToDefault(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()

	// Remove the seeded rows: the store must still answer the defaults.
	for _, key := range []string{"sla_enabled", "sla_warning_percent"} {
		if _, err := s.db.ExecContext(ctx, `DELETE FROM settings WHERE key = ?`, key); err != nil {
			t.Fatalf("delete %s: %v", key, err)
		}
	}
	st := s.SettingsStore()
	enabled, err := st.GetSLAEnabled(ctx)
	if err != nil {
		t.Fatalf("get sla_enabled: %v", err)
	}
	if enabled {
		t.Errorf("sla_enabled = true, want disabled fallback")
	}
	percent, err := st.GetSLAWarningPercent(ctx)
	if err != nil {
		t.Fatalf("get sla_warning_percent: %v", err)
	}
	if percent != 80 {
		t.Errorf("sla_warning_percent = %d, want default 80", percent)
	}
	enabledAt, err := st.GetSLAEnabledAt(ctx)
	if err != nil {
		t.Fatalf("get sla_enabled_at: %v", err)
	}
	if !enabledAt.IsZero() {
		t.Errorf("sla_enabled_at = %v, want the zero time", enabledAt)
	}
}

func TestSettingsStoreSLAEnabledParsesBothValues(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()

	for _, tc := range []struct {
		stored string
		want   bool
	}{
		{"0", false},
		{"1", true},
		// Unparseable values fail safe to disabled.
		{"yes", false},
		{"", false},
	} {
		if _, err := s.db.ExecContext(ctx, `UPDATE settings SET value = ? WHERE key = 'sla_enabled'`, tc.stored); err != nil {
			t.Fatalf("store %q: %v", tc.stored, err)
		}
		got, err := s.SettingsStore().GetSLAEnabled(ctx)
		if err != nil {
			t.Fatalf("get with stored %q: %v", tc.stored, err)
		}
		if got != tc.want {
			t.Errorf("stored %q: sla_enabled = %v, want %v", tc.stored, got, tc.want)
		}
	}
}

// SetSLAEnabledState is the ONE writer for the SLA enable flag and its
// activation instant (issue #211). It is a single transaction, so an
// enabled feature can never be persisted without the instant explaining
// it, and the instant records the FIRST enable rather than the latest.
func TestSettingsStoreSetSLAEnabledStateRoundTrip(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	st := s.SettingsStore()

	if err := st.SetSLAEnabledState(ctx, true, testClock); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if got, err := st.GetSLAEnabled(ctx); err != nil || !got {
		t.Fatalf("sla_enabled = %v (err %v), want true", got, err)
	}
	at, err := st.GetSLAEnabledAt(ctx)
	if err != nil {
		t.Fatalf("get instant: %v", err)
	}
	if !at.Equal(testClock) {
		t.Errorf("sla_enabled_at = %v, want %v", at, testClock)
	}
	if at.Location() != time.UTC {
		t.Errorf("sla_enabled_at location = %v, want UTC", at.Location())
	}

	// A second enable must NOT move the instant: it records the FIRST time.
	later := testClock.Add(48 * time.Hour)
	if err := st.SetSLAEnabledState(ctx, true, later); err != nil {
		t.Fatalf("re-enable: %v", err)
	}
	if at, err = st.GetSLAEnabledAt(ctx); err != nil || !at.Equal(testClock) {
		t.Errorf("sla_enabled_at = %v (err %v) after a second enable, want the first %v", at, err, testClock)
	}

	// Disabling flips the flag and preserves the instant.
	if err := st.SetSLAEnabledState(ctx, false, later); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if got, err := st.GetSLAEnabled(ctx); err != nil || got {
		t.Errorf("sla_enabled = %v (err %v) after disable, want false", got, err)
	}
	if at, err = st.GetSLAEnabledAt(ctx); err != nil || !at.Equal(testClock) {
		t.Errorf("sla_enabled_at = %v (err %v) after disable, want the preserved %v", at, err, testClock)
	}
}

// TestSettingsStoreSetSLAEnabledStateRollsBackAtomically proves the flag
// and the instant are ONE unit: aborting the second write must leave the
// flag exactly as it was, never enabled with no instant recorded.
func TestSettingsStoreSetSLAEnabledStateRollsBackAtomically(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	st := s.SettingsStore()

	// A known disabled state with NO recorded instant.
	if _, err := s.db.ExecContext(ctx, `DELETE FROM settings WHERE key = ?`, settingsKeySLAEnabledAt); err != nil {
		t.Fatalf("clear instant: %v", err)
	}
	if err := st.SetSLAEnabledState(ctx, false, testClock); err != nil {
		t.Fatalf("seed disabled: %v", err)
	}

	// Abort the SECOND write so the transaction has to roll the enabled
	// flag back with it.
	if _, err := s.db.ExecContext(ctx, `
		CREATE TRIGGER abort_sla_enabled_at BEFORE INSERT ON settings
		WHEN NEW.key = '`+settingsKeySLAEnabledAt+`'
		BEGIN SELECT RAISE(ABORT, 'boom'); END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}
	t.Cleanup(func() {
		// context.Background(), NOT t.Context(): t.Context() is cancelled
		// during cleanup, so the drop would fail with "context canceled".
		if _, err := s.db.ExecContext(context.Background(), `DROP TRIGGER abort_sla_enabled_at`); err != nil {
			t.Errorf("drop trigger: %v", err)
		}
	})

	if err := st.SetSLAEnabledState(ctx, true, testClock); err == nil {
		t.Fatal("enable succeeded with the instant write aborted, want an error")
	}
	if got, err := st.GetSLAEnabled(ctx); err != nil || got {
		t.Errorf("sla_enabled = %v (err %v) after the rollback, want the original false", got, err)
	}
}

// slaConfigurationMatrix is a valid four-priority panel in canonical order.
func slaConfigurationMatrix() []domain.SLAPolicy {
	return []domain.SLAPolicy{
		{Priority: domain.PriorityCritical, FirstResponseSeconds: 1800, ResolveSeconds: 14400},
		{Priority: domain.PriorityHigh, FirstResponseSeconds: 3600, ResolveSeconds: 28800},
		{Priority: domain.PriorityMedium, FirstResponseSeconds: 14400, ResolveSeconds: 86400},
		{Priority: domain.PriorityLow, FirstResponseSeconds: 28800, ResolveSeconds: 259200},
	}
}

// SetSLAConfiguration is the ONE writer for the whole instance SLA panel.
func TestSettingsStoreSetSLAConfigurationRoundTrip(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	st := s.SettingsStore()
	matrix := slaConfigurationMatrix()
	matrix[0].FirstResponseSeconds = 900

	if err := st.SetSLAConfiguration(ctx, true, testClock, 42, matrix); err != nil {
		t.Fatalf("apply panel: %v", err)
	}
	if got, err := st.GetSLAEnabled(ctx); err != nil || !got {
		t.Errorf("sla_enabled = %v (err %v), want true", got, err)
	}
	if got, err := st.GetSLAWarningPercent(ctx); err != nil || got != 42 {
		t.Errorf("sla_warning_percent = %d (err %v), want 42", got, err)
	}
	if at, err := st.GetSLAEnabledAt(ctx); err != nil || !at.Equal(testClock) {
		t.Errorf("sla_enabled_at = %v (err %v), want %v", at, err, testClock)
	}
	rows, err := s.SLAStore().ListDefaults(ctx)
	if err != nil {
		t.Fatalf("list defaults: %v", err)
	}
	if len(rows) != 4 {
		t.Fatalf("defaults rows = %d, want 4", len(rows))
	}
	if rows[0].Priority != domain.PriorityCritical || rows[0].FirstResponseSeconds != 900 {
		t.Errorf("critical row = (%s, %d), want (critical, 900)", rows[0].Priority, rows[0].FirstResponseSeconds)
	}
}

// TestSettingsStoreSetSLAConfigurationRollsBackAtomically proves the panel is
// ONE unit: aborting the LAST default row must also undo the enable flag and
// the warning percent written before it, so no partial panel survives.
func TestSettingsStoreSetSLAConfigurationRollsBackAtomically(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	st := s.SettingsStore()

	if _, err := s.db.ExecContext(ctx, `
		CREATE TRIGGER abort_sla_default BEFORE INSERT ON sla_defaults
		WHEN NEW.priority = 'low'
		BEGIN SELECT RAISE(ABORT, 'boom'); END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}
	t.Cleanup(func() {
		// context.Background(), NOT t.Context(): t.Context() is cancelled
		// during cleanup, so the drop would fail with "context canceled".
		if _, err := s.db.ExecContext(context.Background(), `DROP TRIGGER abort_sla_default`); err != nil {
			t.Errorf("drop trigger: %v", err)
		}
	})

	matrix := slaConfigurationMatrix()
	matrix[0].FirstResponseSeconds = 900
	if err := st.SetSLAConfiguration(ctx, true, testClock, 42, matrix); err == nil {
		t.Fatal("apply succeeded with the last default row aborted, want an error")
	}

	if got, err := st.GetSLAEnabled(ctx); err != nil || got {
		t.Errorf("sla_enabled = %v (err %v) after the rollback, want the seeded false", got, err)
	}
	if got, err := st.GetSLAWarningPercent(ctx); err != nil || got != 80 {
		t.Errorf("sla_warning_percent = %d (err %v) after the rollback, want the seeded 80", got, err)
	}
	if _, err := st.GetSLAEnabledAt(ctx); err != nil {
		t.Fatalf("get instant: %v", err)
	}
	rows, err := s.SLAStore().ListDefaults(ctx)
	if err != nil {
		t.Fatalf("list defaults: %v", err)
	}
	for _, row := range rows {
		if row.Priority == domain.PriorityCritical && row.FirstResponseSeconds == 900 {
			t.Error("the critical default row changed despite the rollback")
		}
	}
}

func TestSettingsStoreSetSLAWarningPercentRoundTrip(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	st := s.SettingsStore()

	if err := st.SetSLAWarningPercent(ctx, 42); err != nil {
		t.Fatalf("set: %v", err)
	}
	got, err := st.GetSLAWarningPercent(ctx)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got != 42 {
		t.Errorf("sla_warning_percent = %d, want 42", got)
	}
}

// --- SLA working calendar (migration 0016, issue #211) ---

// TestSettingsStoreSLACalendarDefaults proves a fresh database — none of
// the four sla_calendar_* keys written yet — answers the documented
// default: Monday-Friday, 09:00-18:00, UTC.
func TestSettingsStoreSLACalendarDefaults(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()

	got, err := s.SettingsStore().GetSLACalendar(ctx)
	if err != nil {
		t.Fatalf("get calendar: %v", err)
	}
	want := domain.DefaultSLACalendar()
	if len(got.WorkingDays) != len(want.WorkingDays) {
		t.Fatalf("WorkingDays = %v, want %v", got.WorkingDays, want.WorkingDays)
	}
	for i, d := range want.WorkingDays {
		if got.WorkingDays[i] != d {
			t.Errorf("WorkingDays[%d] = %v, want %v", i, got.WorkingDays[i], d)
		}
	}
	if got.StartMinute != 540 || got.EndMinute != 1080 {
		t.Errorf("window = (%d, %d), want (540, 1080)", got.StartMinute, got.EndMinute)
	}
	if got.Location != time.UTC {
		t.Errorf("Location = %v, want UTC", got.Location)
	}
}

// TestSettingsStoreSLACalendarRoundTrip proves the whole calendar —
// including a single-day week, custom window bounds, and a non-UTC IANA
// zone — round-trips through the atomic write and the read.
func TestSettingsStoreSLACalendarRoundTrip(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	st := s.SettingsStore()

	tz, err := time.LoadLocation("America/Argentina/Buenos_Aires")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	want := domain.SLACalendar{
		WorkingDays: []time.Weekday{time.Wednesday, time.Saturday},
		StartMinute: 480, EndMinute: 720,
		Location: tz,
	}
	if err := st.SetSLACalendar(ctx, want); err != nil {
		t.Fatalf("set calendar: %v", err)
	}
	got, err := st.GetSLACalendar(ctx)
	if err != nil {
		t.Fatalf("get calendar: %v", err)
	}
	if len(got.WorkingDays) != len(want.WorkingDays) {
		t.Fatalf("WorkingDays = %v, want %v", got.WorkingDays, want.WorkingDays)
	}
	for i, d := range want.WorkingDays {
		if got.WorkingDays[i] != d {
			t.Errorf("WorkingDays[%d] = %v, want %v", i, got.WorkingDays[i], d)
		}
	}
	if got.StartMinute != want.StartMinute || got.EndMinute != want.EndMinute {
		t.Errorf("window = (%d, %d), want (%d, %d)", got.StartMinute, got.EndMinute, want.StartMinute, want.EndMinute)
	}
	if got.Location.String() != want.Location.String() {
		t.Errorf("Location = %v, want %v", got.Location, want.Location)
	}
}

// TestSettingsStoreSLACalendarAbsentAndUnparseableRowsFallBack proves the
// documented per-key fallback: deleting or corrupting one key falls THAT
// key back to its default while the other keys keep their stored values —
// a calendar read always answers a usable calendar.
func TestSettingsStoreSLACalendarAbsentAndUnparseableRowsFallBack(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	st := s.SettingsStore()

	want := domain.SLACalendar{
		WorkingDays: []time.Weekday{time.Tuesday},
		StartMinute: 600, EndMinute: 900,
		Location: time.UTC,
	}
	if err := st.SetSLACalendar(ctx, want); err != nil {
		t.Fatalf("set calendar: %v", err)
	}

	for _, tc := range []struct {
		name, key, value string
	}{
		// Unparseable values: any malformed entry poisons the whole days
		// value; minutes outside 0..1439 are unparseable as a minute of day.
		{name: "days unparseable", key: settingsKeySLACalendarDays, value: "1,x,3"},
		{name: "days empty", key: settingsKeySLACalendarDays, value: ""},
		{name: "days out of range", key: settingsKeySLACalendarDays, value: "0,9"},
		{name: "start minute unparseable", key: settingsKeySLACalendarStartMinute, value: "soon"},
		{name: "end minute out of range", key: settingsKeySLACalendarEndMinute, value: "1500"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := s.db.ExecContext(ctx, `UPDATE settings SET value = ? WHERE key = ?`, tc.value, tc.key); err != nil {
				t.Fatalf("corrupt %s: %v", tc.key, err)
			}
			got, err := st.GetSLACalendar(ctx)
			if err != nil {
				t.Fatalf("get calendar: %v", err)
			}
			switch tc.key {
			case settingsKeySLACalendarDays:
				// The days key fell back to the documented Mon-Fri default...
				def := domain.DefaultSLACalendar()
				if len(got.WorkingDays) != len(def.WorkingDays) {
					t.Fatalf("WorkingDays = %v, want the default %v", got.WorkingDays, def.WorkingDays)
				}
				// ...while the other keys keep their stored values.
				if got.StartMinute != 600 || got.EndMinute != 900 {
					t.Errorf("window = (%d, %d), want the stored (600, 900)", got.StartMinute, got.EndMinute)
				}
			case settingsKeySLACalendarStartMinute:
				if got.StartMinute != 540 {
					t.Errorf("StartMinute = %d, want the default 540", got.StartMinute)
				}
				if got.EndMinute != 900 {
					t.Errorf("EndMinute = %d, want the stored 900", got.EndMinute)
				}
			case settingsKeySLACalendarEndMinute:
				if got.EndMinute != 1080 {
					t.Errorf("EndMinute = %d, want the default 1080", got.EndMinute)
				}
				if got.StartMinute != 600 {
					t.Errorf("StartMinute = %d, want the stored 600", got.StartMinute)
				}
			}
			if got.Location != time.UTC {
				t.Errorf("Location = %v, want the stored UTC", got.Location)
			}
			// Restore the known state for the next case.
			if err := st.SetSLACalendar(ctx, want); err != nil {
				t.Fatalf("restore calendar: %v", err)
			}
		})
	}

	// A fully absent key set — the rows deleted — falls back everywhere.
	for _, key := range []string{
		settingsKeySLACalendarDays, settingsKeySLACalendarStartMinute,
		settingsKeySLACalendarEndMinute, settingsKeySLACalendarTimezone,
	} {
		if _, err := s.db.ExecContext(ctx, `DELETE FROM settings WHERE key = ?`, key); err != nil {
			t.Fatalf("delete %s: %v", key, err)
		}
	}
	got, err := st.GetSLACalendar(ctx)
	if err != nil {
		t.Fatalf("get calendar with absent rows: %v", err)
	}
	def := domain.DefaultSLACalendar()
	if len(got.WorkingDays) != len(def.WorkingDays) || got.StartMinute != 540 || got.EndMinute != 1080 || got.Location != time.UTC {
		t.Errorf("calendar with absent rows = %+v, want the documented defaults", got)
	}
}

// TestSettingsStoreSLACalendarAssembledInvalidFallsBackToDefault proves the
// ASSEMBLED calendar is validated, not just each key. A valid night window
// whose end row is later deleted falls that key back to the 1080 default,
// which is NOT after its stored 1200 start: the per-key fallbacks compose a
// calendar for which Valid() is false. The read must degrade to the
// documented default instead of handing the freeze path a calendar that
// would mark every new ticket breached at creation.
func TestSettingsStoreSLACalendarAssembledInvalidFallsBackToDefault(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	st := s.SettingsStore()

	night := domain.SLACalendar{
		WorkingDays: []time.Weekday{time.Monday},
		StartMinute: 1200, EndMinute: 1439,
		Location: time.UTC,
	}
	if err := st.SetSLACalendar(ctx, night); err != nil {
		t.Fatalf("set calendar: %v", err)
	}
	// Delete the end row: 1200 >= the 1080 fallback, so the composed
	// calendar is invalid even though every individual key parsed.
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM settings WHERE key = ?`, settingsKeySLACalendarEndMinute); err != nil {
		t.Fatalf("delete %s: %v", settingsKeySLACalendarEndMinute, err)
	}

	got, err := st.GetSLACalendar(ctx)
	if err != nil {
		t.Fatalf("get calendar: %v", err)
	}
	if !got.Valid() {
		t.Fatalf("GetSLACalendar returned an invalid calendar %+v, want a usable one", got)
	}
	def := domain.DefaultSLACalendar()
	if got.StartMinute != def.StartMinute || got.EndMinute != def.EndMinute {
		t.Errorf("window = (%d, %d), want the documented default (%d, %d)",
			got.StartMinute, got.EndMinute, def.StartMinute, def.EndMinute)
	}
}

// TestSettingsStoreSLACalendarUnknownTimezoneFallsBackToUTC proves an
// unknown IANA name falls back to UTC rather than failing the read
// (documented port behavior).
func TestSettingsStoreSLACalendarUnknownTimezoneFallsBackToUTC(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	st := s.SettingsStore()

	for _, name := range []string{"Mars/Olympus", "", "Not/AZone"} {
		if _, err := s.db.ExecContext(ctx,
			`INSERT INTO settings (key, value) VALUES (?, ?)
			 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
			settingsKeySLACalendarTimezone, name); err != nil {
			t.Fatalf("store %q: %v", name, err)
		}
		got, err := st.GetSLACalendar(ctx)
		if err != nil {
			t.Fatalf("get calendar with timezone %q: %v", name, err)
		}
		if got.Location != time.UTC {
			t.Errorf("timezone %q: Location = %v, want the UTC fallback", name, got.Location)
		}
	}
}

// TestSettingsStoreSetSLACalendarRefusesNonPortableZone proves the store
// refuses a zone whose name cannot be read back as the same one. It persists
// the name and resolves it on every read with a UTC fallback, so accepting
// such a zone would shift the whole working window while both the write and
// the read reported success.
func TestSettingsStoreSetSLACalendarRefusesNonPortableZone(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	st := s.SettingsStore()

	// A known NON-default calendar, so "nothing moved" is a real assertion.
	seeded := domain.SLACalendar{
		WorkingDays: []time.Weekday{time.Tuesday},
		StartMinute: 600, EndMinute: 900,
		Location: time.UTC,
	}
	if err := st.SetSLACalendar(ctx, seeded); err != nil {
		t.Fatalf("seed calendar: %v", err)
	}

	for _, tc := range []struct {
		name string
		loc  *time.Location
	}{
		{name: "machine-local", loc: time.Local},
		{name: "ad-hoc fixed zone", loc: time.FixedZone("UTC+3", 3*60*60)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rejected := domain.SLACalendar{
				WorkingDays: []time.Weekday{time.Sunday, time.Monday},
				StartMinute: 60, EndMinute: 120,
				Location: tc.loc,
			}
			if err := st.SetSLACalendar(ctx, rejected); err == nil {
				t.Fatal("set succeeded with a non-portable zone, want an error")
			}
			got, err := st.GetSLACalendar(ctx)
			if err != nil {
				t.Fatalf("get calendar after refusal: %v", err)
			}
			if len(got.WorkingDays) != 1 || got.WorkingDays[0] != time.Tuesday {
				t.Errorf("WorkingDays after refusal = %v, want the seeded [Tuesday]", got.WorkingDays)
			}
			if got.StartMinute != 600 || got.EndMinute != 900 {
				t.Errorf("window after refusal = (%d, %d), want the seeded (600, 900)", got.StartMinute, got.EndMinute)
			}
			if got.Location != time.UTC {
				t.Errorf("Location after refusal = %v, want the seeded UTC", got.Location)
			}
		})
	}
}

// TestSettingsStoreSetSLACalendarRollsBackAtomically proves the calendar
// is ONE unit: aborting the LAST of the four key writes must undo the
// three written before it, so no half-applied calendar survives — a
// partially applied calendar would change when every future ticket is due.
func TestSettingsStoreSetSLACalendarRollsBackAtomically(t *testing.T) {
	s := newTestDB(t)
	ctx := context.Background()
	st := s.SettingsStore()

	// Seed a known NON-default calendar first, so the rollback assertion is
	// meaningful: after the failed write the previous calendar stands.
	seeded := domain.SLACalendar{
		WorkingDays: []time.Weekday{time.Tuesday},
		StartMinute: 600, EndMinute: 900,
		Location: time.UTC,
	}
	if err := st.SetSLACalendar(ctx, seeded); err != nil {
		t.Fatalf("seed calendar: %v", err)
	}

	// Abort the LAST write of the transaction: the timezone key.
	if _, err := s.db.ExecContext(ctx, `
		CREATE TRIGGER abort_sla_calendar_timezone BEFORE INSERT ON settings
		WHEN NEW.key = '`+settingsKeySLACalendarTimezone+`'
		BEGIN SELECT RAISE(ABORT, 'boom'); END`); err != nil {
		t.Fatalf("create trigger: %v", err)
	}
	t.Cleanup(func() {
		// context.Background(), NOT t.Context(): t.Context() is cancelled
		// during cleanup, so the drop would fail with "context canceled".
		if _, err := s.db.ExecContext(context.Background(), `DROP TRIGGER abort_sla_calendar_timezone`); err != nil {
			t.Errorf("drop trigger: %v", err)
		}
	})

	rejected := domain.SLACalendar{
		WorkingDays: []time.Weekday{time.Sunday, time.Monday},
		StartMinute: 60, EndMinute: 120,
		Location: time.UTC,
	}
	if err := st.SetSLACalendar(ctx, rejected); err == nil {
		t.Fatal("set succeeded with the timezone write aborted, want an error")
	}
	got, err := st.GetSLACalendar(ctx)
	if err != nil {
		t.Fatalf("get calendar after rollback: %v", err)
	}
	if len(got.WorkingDays) != 1 || got.WorkingDays[0] != time.Tuesday {
		t.Errorf("WorkingDays after rollback = %v, want the seeded [Tuesday]", got.WorkingDays)
	}
	if got.StartMinute != 600 || got.EndMinute != 900 {
		t.Errorf("window after rollback = (%d, %d), want the seeded (600, 900)", got.StartMinute, got.EndMinute)
	}
	if got.Location != time.UTC {
		t.Errorf("Location after rollback = %v, want UTC", got.Location)
	}
}
