package httpadapter

import (
	"testing"
	"time"

	"github.com/giulianotesta7/tkt/internal/i18n"
)

// TestCardTimestampBytesAreLocaleFrozen pins the card timestamp for every
// month in both locales (issue #335, S12/S13): English bytes stay exactly as
// they were before the catalogue owned month names, and Spanish uses the
// lowercase abbreviations. A zero instant renders nothing.
func TestCardTimestampBytesAreLocaleFrozen(t *testing.T) {
	en, es := i18n.New("en"), i18n.New("es")
	enMonths := []string{"Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"}
	esMonths := []string{"ene", "feb", "mar", "abr", "may", "jun", "jul", "ago", "sep", "oct", "nov", "dic"}
	for i := range enMonths {
		at := time.Date(2026, time.Month(i+1), 15, 9, 30, 0, 0, time.UTC)
		if got := formatClockMonthDay(en, at); got != "09:30 · 15 "+enMonths[i] {
			t.Errorf("en month %d = %q, want %q", i+1, got, "09:30 · 15 "+enMonths[i])
		}
		if got := formatClockMonthDay(es, at); got != "09:30 · 15 "+esMonths[i] {
			t.Errorf("es month %d = %q, want %q", i+1, got, "09:30 · 15 "+esMonths[i])
		}
	}
	if got := formatClockMonthDay(en, time.Time{}); got != "" {
		t.Errorf("zero instant = %q, want empty", got)
	}
}
