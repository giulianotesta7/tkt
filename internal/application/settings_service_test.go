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
	// Interface-language seams (issue #335): language mirrors the persisted
	// row ("" answers the documented default), and the writer records its
	// calls so a denied or rejected actor can be proven to have persisted
	// nothing.
	language         string
	languageErr      error
	setLanguageErr   error
	setLanguageCalls int
	// holdAutoCloseDays mirrors the hold_auto_close_working_days setting (issue
	// #353), which the settings service tests do not exercise: it answers the
	// documented default (0, the feature off) so the fake keeps satisfying the
	// SettingsStore port.
	holdAutoCloseDays int
	// Ticket-creation seams (issue #391, S1): same shape as the language
	// ones, so a denied or rejected actor can be proven to have persisted
	// nothing.
	ticketCreation        string
	setTicketCreationErr  error
	setTicketCreationCall int
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

func (f *fakeSettingsStore) GetHoldAutoCloseDays(_ context.Context) (int, error) {
	return f.holdAutoCloseDays, nil
}

func (f *fakeSettingsStore) SetHoldAutoCloseDays(_ context.Context, days int) error {
	f.holdAutoCloseDays = days
	return nil
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

// GetInterfaceLanguage mirrors the store contract: an absent row answers the
// documented default.
func (f *fakeSettingsStore) GetInterfaceLanguage(_ context.Context) (string, error) {
	if f.languageErr != nil {
		return "", f.languageErr
	}
	if f.language == "" {
		return DefaultInterfaceLanguage, nil
	}
	return f.language, nil
}

func (f *fakeSettingsStore) SetInterfaceLanguage(_ context.Context, language string) error {
	f.setLanguageCalls++
	if f.setLanguageErr != nil {
		return f.setLanguageErr
	}
	f.language = language
	return nil
}

// GetTicketCreationMode mirrors the store contract: an absent row answers the
// documented default.
func (f *fakeSettingsStore) GetTicketCreationMode(_ context.Context) (string, error) {
	if f.ticketCreation == "" {
		return DefaultTicketCreationMode, nil
	}
	return f.ticketCreation, nil
}

func (f *fakeSettingsStore) SetTicketCreationMode(_ context.Context, mode string) error {
	f.setTicketCreationCall++
	if f.setTicketCreationErr != nil {
		return f.setTicketCreationErr
	}
	f.ticketCreation = mode
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

// --- Ticket creation mode (issue #391, S1) ---

// TestSettingsTicketCreationModeDefaultsToSinglePage pins the documented
// default: an instance that never chose a mode creates on one page, and the
// default needs no persisted row to be true.
func TestSettingsTicketCreationModeDefaultsToSinglePage(t *testing.T) {
	if DefaultTicketCreationMode != TicketCreationSingle {
		t.Fatalf("DefaultTicketCreationMode = %q, want %q", DefaultTicketCreationMode, TicketCreationSingle)
	}
	st := &fakeSettingsStore{}

	got, err := NewSettingsService(st).GetTicketCreationMode(context.Background())
	if err != nil {
		t.Fatalf("get ticket creation mode: %v", err)
	}
	if got != DefaultTicketCreationMode {
		t.Errorf("ticket creation mode = %q, want the default %q", got, DefaultTicketCreationMode)
	}
}

// TestSettingsSetTicketCreationModeRejectsNonAdmin proves the instance-wide
// setting keeps CapManageSettings: a denied actor changes nothing and never
// reaches the store writer.
func TestSettingsSetTicketCreationModeRejectsNonAdmin(t *testing.T) {
	st := &fakeSettingsStore{ticketCreation: TicketCreationSingle}
	svc := NewSettingsService(st)

	err := svc.SetTicketCreationMode(context.Background(), agentActor(), TicketCreationTwoStep)
	var forbidden *domain.ForbiddenError
	if !errors.As(err, &forbidden) {
		t.Fatalf("err = %v, want ForbiddenError", err)
	}
	if st.ticketCreation != TicketCreationSingle {
		t.Errorf("store mutated by denied actor: %q", st.ticketCreation)
	}
	if st.setTicketCreationCall != 0 {
		t.Errorf("store writer called %d times by a denied actor, want 0", st.setTicketCreationCall)
	}
}

// TestSettingsSetTicketCreationModeRejectsUnknownMode proves the service
// validates before the store is reached: anything outside the closed set is
// refused and the stored mode stands.
func TestSettingsSetTicketCreationModeRejectsUnknownMode(t *testing.T) {
	for _, mode := range []string{"wizard", "SINGLE", "", "two-step", "twostep"} {
		t.Run(mode, func(t *testing.T) {
			st := &fakeSettingsStore{ticketCreation: TicketCreationSingle}
			svc := NewSettingsService(st)

			err := svc.SetTicketCreationMode(context.Background(), adminActor(), mode)
			var validation *domain.ValidationError
			if !errors.As(err, &validation) {
				t.Fatalf("err = %v, want ValidationError", err)
			}
			if validation.Field != "ticket_creation_mode" {
				t.Errorf("validation field = %q, want %q", validation.Field, "ticket_creation_mode")
			}
			if st.ticketCreation != TicketCreationSingle {
				t.Errorf("store changed to %q on an invalid mode", st.ticketCreation)
			}
			if st.setTicketCreationCall != 0 {
				t.Errorf("store writer called %d times on invalid input, want 0", st.setTicketCreationCall)
			}
		})
	}
}

// TestSettingsSetTicketCreationModePersistsEveryAllowedMode proves both
// shipped modes round-trip through the store, for an admin and for root.
func TestSettingsSetTicketCreationModePersistsEveryAllowedMode(t *testing.T) {
	allowed := AllowedTicketCreationModes()
	if len(allowed) != 2 || allowed[0] != TicketCreationSingle || allowed[1] != TicketCreationTwoStep {
		t.Fatalf("AllowedTicketCreationModes() = %v, want [%s %s]", allowed, TicketCreationSingle, TicketCreationTwoStep)
	}

	for _, actor := range []struct {
		name string
		user domain.User
	}{{"admin", adminActor()}, {"root", rootActor()}} {
		for _, mode := range allowed {
			t.Run(actor.name+"/"+mode, func(t *testing.T) {
				st := &fakeSettingsStore{ticketCreation: DefaultTicketCreationMode}
				svc := NewSettingsService(st)

				if err := svc.SetTicketCreationMode(context.Background(), actor.user, mode); err != nil {
					t.Fatalf("set: %v", err)
				}
				if st.ticketCreation != mode {
					t.Errorf("stored = %q, want %q", st.ticketCreation, mode)
				}
				got, err := svc.GetTicketCreationMode(context.Background())
				if err != nil {
					t.Fatalf("get: %v", err)
				}
				if got != mode {
					t.Errorf("read back = %q, want %q", got, mode)
				}
			})
		}
	}
}

// --- Interface language (issue #335, S7/S9) ---

// TestSettingsInterfaceLanguageDefaultsToEnglish pins the shipped default: an
// instance that never chose a language renders English, and the default needs
// no persisted row to be true.
func TestSettingsInterfaceLanguageDefaultsToEnglish(t *testing.T) {
	if DefaultInterfaceLanguage != "en" {
		t.Fatalf("DefaultInterfaceLanguage = %q, want %q", DefaultInterfaceLanguage, "en")
	}
	st := &fakeSettingsStore{}

	got, err := NewSettingsService(st).GetInterfaceLanguage(context.Background())
	if err != nil {
		t.Fatalf("get interface language: %v", err)
	}
	if got != DefaultInterfaceLanguage {
		t.Errorf("interface language = %q, want the default %q", got, DefaultInterfaceLanguage)
	}
}

// TestSettingsSetInterfaceLanguageRejectsNonAdmin proves the instance-wide
// setting keeps CapManageSettings: a denied actor changes nothing.
func TestSettingsSetInterfaceLanguageRejectsNonAdmin(t *testing.T) {
	st := &fakeSettingsStore{language: "en"}
	svc := NewSettingsService(st)

	err := svc.SetInterfaceLanguage(context.Background(), agentActor(), "es")
	var forbidden *domain.ForbiddenError
	if !errors.As(err, &forbidden) {
		t.Fatalf("err = %v, want ForbiddenError", err)
	}
	if st.language != "en" {
		t.Errorf("store mutated by denied actor: %q", st.language)
	}
	if st.setLanguageCalls != 0 {
		t.Errorf("store writer called %d times by a denied actor, want 0", st.setLanguageCalls)
	}
}

// TestSettingsSetInterfaceLanguageRejectsUnsupportedLanguage proves the
// service validates before the store is reached: an unknown code, a case
// variant, and an empty value are all refused and the stored language stands.
func TestSettingsSetInterfaceLanguageRejectsUnsupportedLanguage(t *testing.T) {
	for _, language := range []string{"fr", "ES", "english", "", "en-GB"} {
		t.Run(language, func(t *testing.T) {
			st := &fakeSettingsStore{language: "en"}
			svc := NewSettingsService(st)

			err := svc.SetInterfaceLanguage(context.Background(), adminActor(), language)
			var validation *domain.ValidationError
			if !errors.As(err, &validation) {
				t.Fatalf("err = %v, want ValidationError", err)
			}
			if validation.Field != "interface_language" {
				t.Errorf("validation field = %q, want %q", validation.Field, "interface_language")
			}
			if st.language != "en" {
				t.Errorf("store changed to %q on an invalid language", st.language)
			}
			if st.setLanguageCalls != 0 {
				t.Errorf("store writer called %d times on invalid input, want 0", st.setLanguageCalls)
			}
		})
	}
}

// TestSettingsSetInterfaceLanguagePersistsShippedLanguages proves every
// shipped language round-trips through the store, for an admin and for root
// (S9: the selection is persisted).
func TestSettingsSetInterfaceLanguagePersistsShippedLanguages(t *testing.T) {
	shipped := AllowedInterfaceLanguages()
	if len(shipped) != 2 || shipped[0] != InterfaceLanguageEnglish || shipped[1] != InterfaceLanguageSpanish {
		t.Fatalf("AllowedInterfaceLanguages() = %v, want [en es]", shipped)
	}

	for _, actor := range []struct {
		name string
		user domain.User
	}{{"admin", adminActor()}, {"root", rootActor()}} {
		for _, language := range shipped {
			t.Run(actor.name+"/"+language, func(t *testing.T) {
				st := &fakeSettingsStore{language: DefaultInterfaceLanguage}
				svc := NewSettingsService(st)

				if err := svc.SetInterfaceLanguage(context.Background(), actor.user, language); err != nil {
					t.Fatalf("set: %v", err)
				}
				if st.language != language {
					t.Errorf("stored = %q, want %q", st.language, language)
				}
				got, err := svc.GetInterfaceLanguage(context.Background())
				if err != nil {
					t.Fatalf("get: %v", err)
				}
				if got != language {
					t.Errorf("read back = %q, want %q", got, language)
				}
			})
		}
	}
}
