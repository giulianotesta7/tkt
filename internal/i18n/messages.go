// Package i18n owns the interface message catalogues (issue #335): every
// user-facing string is addressed by a stable key, and each shipped locale
// answers the same key set. Templates and Go code reference keys, never
// literals, so switching the instance language changes copy without touching
// a single surface.
//
// # Key convention
//
// A key is `<area>.<component>.<element>`, lowercase, dot-separated:
//
//   - area names the surface that owns the copy, after the template file stem
//     (base, auth, and later the page or partial it belongs to).
//   - component names the block inside that surface (nav, journey, principles).
//   - element names the slot: title/body/label for prose, a bare component for
//     a one-word control label shared by title and aria-label.
//
// So `base.nav.tickets` is the Tickets rail link (its title and its
// aria-label), and `auth.principles.simple.body` is the sentence under the
// "Simple by design" heading. Keys are never reused across surfaces: a copy
// change to one screen must not move another, and an admin-authored string
// (a department, desk, or category name) never gets a key at all — content is
// not chrome.
//
// # Fallback
//
// A lookup resolves the active locale first, the default catalogue (English)
// second, and a readable label last — never the raw key, because a page that
// prints `base.nav.tickets` to a requester is a defect the catalogue keys were
// introduced to remove. The parity test in this package fails the build when a
// key is missing from a shipped catalogue, so the intermediate step exists for
// a translation gap and the last step only for a key that no catalogue knows.
package i18n

import (
	"embed"
	"encoding/json"
	"io/fs"
	"log"
	"sort"
	"strings"
)

// Locale codes. Two catalogues ship (S7): English is the default an instance
// renders until an administrator chooses, Spanish ships complete.
const (
	English = "en"
	Spanish = "es"

	// Default is the locale a fresh instance renders and the fallback for an
	// unknown or empty code.
	Default = English
)

//go:embed catalogues/*.json
var catalogueFS embed.FS

// catalogue is one locale's messages, keyed by the stable dotted key.
type catalogue map[string]string

// catalogues holds the shipped catalogues keyed by locale code, loaded once at
// startup. Loading is fail-fast: a catalogue that cannot be decoded is a build
// error, not an instance that renders half a language.
var catalogues = loadCatalogues()

func loadCatalogues() map[string]catalogue {
	entries, err := fs.ReadDir(catalogueFS, "catalogues")
	if err != nil {
		panic("i18n: read catalogue directory: " + err.Error())
	}
	loaded := make(map[string]catalogue, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		code := strings.TrimSuffix(entry.Name(), ".json")
		raw, err := catalogueFS.ReadFile("catalogues/" + entry.Name())
		if err != nil {
			panic("i18n: read catalogue " + code + ": " + err.Error())
		}
		var messages catalogue
		if err := json.Unmarshal(raw, &messages); err != nil {
			panic("i18n: decode catalogue " + code + ": " + err.Error())
		}
		loaded[code] = messages
	}
	if _, ok := loaded[Default]; !ok {
		panic("i18n: the default locale " + Default + " has no catalogue")
	}
	return loaded
}

// Supported returns the shipped locale codes in a stable order (English
// first), which is the order an administrator is offered them.
func Supported() []string {
	codes := make([]string, 0, len(catalogues))
	for code := range catalogues {
		codes = append(codes, code)
	}
	sort.Slice(codes, func(i, j int) bool {
		if codes[i] == Default {
			return true
		}
		if codes[j] == Default {
			return false
		}
		return codes[i] < codes[j]
	})
	return codes
}

// Normalize maps any locale code to a shipped one: surrounding space and case
// are ignored, and an unknown or empty code falls back to Default. It is the
// single normalization step, so a persisted value and a request-time value
// always resolve the same way.
func Normalize(code string) string {
	normalized := strings.ToLower(strings.TrimSpace(code))
	if _, ok := catalogues[normalized]; ok {
		return normalized
	}
	return Default
}

// Translator answers the messages of one locale. Use New to build one.
type Translator struct {
	locale   string
	messages catalogue
	fallback catalogue
}

// New returns the Translator for locale. An unknown, empty, or differently
// cased code resolves through Normalize, so a caller never has to validate the
// code before asking for copy.
func New(locale string) Translator {
	code := Normalize(locale)
	return Translator{locale: code, messages: catalogues[code], fallback: catalogues[Default]}
}

// Locale returns the normalized locale this Translator answers for — the value
// a shell declares in its lang attribute.
func (t Translator) Locale() string { return t.locale }

// T returns the message for key. The chain is the active locale, then the
// default catalogue, then a readable label derived from the key's last
// segment; the raw key is never returned.
func (t Translator) T(key string) string {
	if key == "" {
		return ""
	}
	if message, ok := t.messages[key]; ok && message != "" {
		return message
	}
	if message, ok := t.fallback[key]; ok && message != "" {
		if t.locale != Default {
			log.Printf("i18n: key %q is missing from locale %q, falling back to %q", key, t.locale, Default)
		}
		return message
	}
	log.Printf("i18n: key %q is missing from every catalogue", key)
	return readableLabel(key)
}

// T answers one message for locale without holding a Translator: the
// convenience form for Go-side copy, mirrored by the template's t helper.
func T(locale, key string) string { return New(locale).T(key) }

// readableLabel degrades a key that no catalogue carries into a label a
// requester can still read: the last dotted segment, its separators opened
// into spaces, and its first letter capitalized (chrome is sentence case).
func readableLabel(key string) string {
	segment := key
	if i := strings.LastIndex(segment, "."); i >= 0 {
		segment = segment[i+1:]
	}
	label := strings.Join(strings.Fields(strings.NewReplacer("_", " ", "-", " ").Replace(segment)), " ")
	if label == "" {
		return ""
	}
	return strings.ToUpper(label[:1]) + label[1:]
}
