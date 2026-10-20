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
	"fmt"
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

// Lookup reports whether key has a message in the active locale (or the default
// catalogue), which is how the label helpers decide between a catalogue entry
// and the humanized fallback for a genuinely free identifier.
func (t Translator) Lookup(key string) (string, bool) {
	if message, ok := t.messages[key]; ok && message != "" {
		return message, true
	}
	if message, ok := t.fallback[key]; ok && message != "" {
		return message, true
	}
	return "", false
}

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

// Tf answers the message for key formatted with fmt-style arguments: the
// catalogue carries the sentence and its placeholders (%s, %d), so a Go call
// site interpolates values without owning any copy itself.
func (t Translator) Tf(key string, args ...any) string {
	return fmt.Sprintf(t.T(key), args...)
}

// Tp answers the PLURAL form of key for count, interpolating count into the
// sentence: the sentence's first value is always the count itself, so a plural
// call passes the number once and any additional values after it (for example
// the total in "%d of %d done").
//
// The mechanism is deliberately minimal (one/other): a catalogue adds
// `<key>_one` when its singular reads differently, and omits it when the base
// already does. The active locale's form is used without falling through to
// the default catalogue (so a Spanish key with an `_one` and no `_other` still
// reads its Spanish plural), and the base sentence keeps the documented
// active-then-default fallback. English keeps its historical copy — for
// example users.count stays "%d accounts" even at one — so a default instance
// is unchanged (S13); Spanish adds the singular where it differs.
func (t Translator) Tp(key string, count int, args ...any) string {
	suffix := "_other"
	if count == 1 {
		suffix = "_one"
	}
	values := append([]any{count}, args...)
	if message, ok := t.messages[key+suffix]; ok && message != "" {
		return fmt.Sprintf(message, values...)
	}
	return fmt.Sprintf(t.T(key), values...)
}

// Tp answers one plural message for locale without holding a Translator: the
// convenience form for Go-side copy, mirrored by the template's tp helper.
func Tp(locale, key string, count int, args ...any) string {
	return New(locale).Tp(key, count, args...)
}

// ScriptPrefix namespaces the catalogue entries the browser scripts read. A
// script is served with the prologue Script builds, so the server owns every
// user-facing string the browser renders and the JavaScript keeps no literal
// copy of its own.
const ScriptPrefix = "js."

// Script returns the JavaScript prologue every catalogue-backed browser script
// is served with: the locale's js.* messages as one JSON object plus the
// lookup helper the scripts call (window.tktI18n.t).
//
// The helper mirrors the Go fallback chain: an unknown key degrades to a
// readable label derived from the key's last segment, never to the raw key.
func Script(locale string) string {
	messages := make(map[string]string)
	for key, message := range catalogues[Normalize(locale)] {
		if strings.HasPrefix(key, ScriptPrefix) {
			messages[key] = message
		}
	}
	encoded, err := json.Marshal(messages)
	if err != nil {
		// A map[string]string cannot fail to marshal; a failure would be a
		// programming error, not a runtime condition.
		panic("i18n: encode script catalogue: " + err.Error())
	}
	return scriptPrologueOpen + string(encoded) + scriptPrologueClose
}

// scriptPrologueOpen/Close wrap the encoded catalogue with the lookup helper.
// The helper replaces {name} placeholders and degrades a missing key exactly
// as the Go Translator does.
const (
	scriptPrologueOpen  = `window.tktI18n=(function(){var m=`
	scriptPrologueClose = `;function label(k){var s=String(k).split(".").pop().replace(/[_-]+/g," ");return s.charAt(0).toUpperCase()+s.slice(1);}function t(k,p){var s=Object.prototype.hasOwnProperty.call(m,k)?m[k]:label(k);if(p){for(var n in p){if(Object.prototype.hasOwnProperty.call(p,n)){s=s.split("{"+n+"}").join(p[n]);}}}return s;}return {t:t};})();
`
)

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
