package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/domain"
)

// fakeSettingsStore records the persisted internal-comment background color
// and fails on demand (settings service test double).
type fakeSettingsStore struct {
	bg  string
	err error
	// SLA write seams (issue #211): each set*Err fails the matching writer
	// before any state changes; each call recorder lets the SLA service
	// tests prove a denied actor touched no store method.
	slaEnabled        bool
	slaWarningPercent int
	slaEnabledAt      time.Time
	defaults          []domain.SLAPolicy
	// Working-calendar seams (issue #211): same fail-on-demand and call
	// recorder pattern as the other SLA writers.
	calendar                  domain.SLACalendar
	setSLACalendarErr         error
	setSLACalendarCalls       int
	setSLAEnabledStateErr     error
	setSLAWarningPercentErr   error
	setSLAConfigurationErr    error
	setSLAEnabledStateCalls   int
	setSLAWarningPercentCalls int
	setSLAConfigurationCalls  int
}

func (f *fakeSettingsStore) GetInternalCommentBg(_ context.Context) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	if f.bg == "" {
		return DefaultInternalCommentBg, nil
	}
	return f.bg, nil
}

func (f *fakeSettingsStore) SetInternalCommentBg(_ context.Context, color string) error {
	if f.err != nil {
		return f.err
	}
	f.bg = color
	return nil
}

// The SLA settings are unused by the settings service today; the fake
// answers the documented defaults so it keeps satisfying the widened
// SettingsStore port.
func (f *fakeSettingsStore) GetSLAEnabled(_ context.Context) (bool, error) {
	return false, nil
}

func (f *fakeSettingsStore) GetSLAWarningPercent(_ context.Context) (int, error) {
	return DefaultSLAWarningPercent, nil
}

func (f *fakeSettingsStore) GetSLAEnabledAt(_ context.Context) (time.Time, error) {
	return time.Time{}, nil
}

func (f *fakeSettingsStore) GetSLACalendar(_ context.Context) (domain.SLACalendar, error) {
	return domain.DefaultSLACalendar(), nil
}

func (f *fakeSettingsStore) SetSLACalendar(_ context.Context, calendar domain.SLACalendar) error {
	f.setSLACalendarCalls++
	if f.setSLACalendarErr != nil {
		return f.setSLACalendarErr
	}
	f.calendar = calendar
	return nil
}

// The three SLA writers record their writes and fail on demand, so the
// SLA service tests can assert both that a write happened and that a
// denied actor produced none. Each set*Err, when non-nil, makes the
// matching writer fail BEFORE any state changes.
// SetSLAEnabledState mirrors the store contract: the flag and the instant
// are one write, and the instant records the FIRST enable.
func (f *fakeSettingsStore) SetSLAEnabledState(_ context.Context, enabled bool, at time.Time) error {
	f.setSLAEnabledStateCalls++
	if f.setSLAEnabledStateErr != nil {
		return f.setSLAEnabledStateErr
	}
	f.slaEnabled = enabled
	if enabled && f.slaEnabledAt.IsZero() {
		f.slaEnabledAt = at
	}
	return nil
}

// SetSLAConfiguration mirrors the store contract: the whole panel is one
// write, so a failure leaves none of it applied.
func (f *fakeSettingsStore) SetSLAConfiguration(_ context.Context, enabled bool, at time.Time, warningPercent int, defaults []domain.SLAPolicy) error {
	f.setSLAConfigurationCalls++
	if f.setSLAConfigurationErr != nil {
		return f.setSLAConfigurationErr
	}
	f.slaEnabled = enabled
	f.slaWarningPercent = warningPercent
	if enabled && f.slaEnabledAt.IsZero() {
		f.slaEnabledAt = at
	}
	f.defaults = defaults
	return nil
}

func (f *fakeSettingsStore) SetSLAWarningPercent(_ context.Context, percent int) error {
	f.setSLAWarningPercentCalls++
	if f.setSLAWarningPercentErr != nil {
		return f.setSLAWarningPercentErr
	}
	f.slaWarningPercent = percent
	return nil
}

func adminActor() domain.User { return domain.User{ID: 1, Name: "Admin", Role: domain.RoleAdmin} }
func agentActor() domain.User { return domain.User{ID: 2, Name: "Agent", Role: domain.RoleAgent} }
func rootActor() domain.User  { return domain.User{ID: 3, Name: "Root", Role: domain.RoleRoot} }

func TestSettingsGetAppearance(t *testing.T) {
	st := &fakeSettingsStore{bg: "#EFE9FB"}
	svc := NewSettingsService(st)

	got, err := svc.GetAppearance(context.Background())
	if err != nil {
		t.Fatalf("get appearance: %v", err)
	}
	if got != "#EFE9FB" {
		t.Errorf("bg = %q, want %q", got, "#EFE9FB")
	}
}

func TestSettingsSetRejectsNonAdmin(t *testing.T) {
	st := &fakeSettingsStore{bg: "#E8EEFF"}
	svc := NewSettingsService(st)

	err := svc.SetInternalCommentBg(context.Background(), agentActor(), "#EFE9FB")
	var forbidden *domain.ForbiddenError
	if !errors.As(err, &forbidden) {
		t.Fatalf("err = %v, want ForbiddenError", err)
	}
	if st.bg != "#E8EEFF" {
		t.Errorf("store mutated by denied actor: %q", st.bg)
	}
}

func TestSettingsSetRejectsInvalidColor(t *testing.T) {
	st := &fakeSettingsStore{bg: "#E8EEFF"}
	svc := NewSettingsService(st)

	err := svc.SetInternalCommentBg(context.Background(), adminActor(), "#123456")
	var validation *domain.ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("err = %v, want ValidationError", err)
	}
	if validation.Field != "internal_comment_bg" {
		t.Errorf("validation field = %q, want %q", validation.Field, "internal_comment_bg")
	}
	if st.bg != "#E8EEFF" {
		t.Errorf("store changed to %q on invalid color", st.bg)
	}
}

func TestSettingsSetPersistsAllowedColors(t *testing.T) {
	for _, color := range AllowedInternalCommentBg() {
		t.Run(color, func(t *testing.T) {
			st := &fakeSettingsStore{}
			svc := NewSettingsService(st)

			if err := svc.SetInternalCommentBg(context.Background(), adminActor(), color); err != nil {
				t.Fatalf("set: %v", err)
			}
			if st.bg != color {
				t.Errorf("stored = %q, want %q", st.bg, color)
			}
		})
	}
}

func TestSettingsSetRootAllowed(t *testing.T) {
	st := &fakeSettingsStore{}
	svc := NewSettingsService(st)

	if err := svc.SetInternalCommentBg(context.Background(), rootActor(), "#EFE9FB"); err != nil {
		t.Fatalf("root set: %v", err)
	}
	if st.bg != "#EFE9FB" {
		t.Errorf("stored = %q, want %q", st.bg, "#EFE9FB")
	}
}
