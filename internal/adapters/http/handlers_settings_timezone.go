package httpadapter

import (
	_ "embed"
	"strings"
	"time"
	_ "time/tzdata" // Suggestions must resolve even when the host has no zone database.

	"github.com/giulianotesta7/tkt/internal/domain"
)

// timezoneCatalogue is the name index of Go 1.26.10's IANA zoneinfo.zip.
// It is a suggestion catalogue, not an allowlist: the existing calendar
// validator remains authoritative for typed and previously stored names.
//
//go:embed timezones.txt
var timezoneCatalogue string

var calendarTimezoneSuggestions = func() []string {
	var zones []string
	for _, name := range strings.Split(timezoneCatalogue, "\n") {
		if name == "" || strings.HasPrefix(name, "#") {
			continue
		}
		loc, err := time.LoadLocation(name)
		if err == nil && domain.LocationIsPortable(loc) {
			zones = append(zones, name)
		}
	}
	return zones
}()
