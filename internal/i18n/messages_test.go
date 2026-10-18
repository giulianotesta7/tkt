package i18n

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The interface catalogues (issue #335, S7/S10/S13): both shipped locales
// carry the same key set, a key that is missing never renders raw, and an
// unknown locale falls back to the default one.

// TestEveryCatalogueCarriesTheSameKeys is the parity half of S10: a key
// present in one catalogue and absent in the other is a message that
// silently changes language mid-sentence, so the two key sets must be
// identical and every message must carry real copy.
func TestEveryCatalogueCarriesTheSameKeys(t *testing.T) {
	locales := Supported()
	if len(locales) < 2 {
		t.Fatalf("Supported() = %v, want at least the two shipped locales", locales)
	}
	if _, ok := catalogues[Default]; !ok {
		t.Fatalf("the default locale %q has no catalogue", Default)
	}
	reference := catalogues[Default]
	if len(reference) == 0 {
		t.Fatal("the default catalogue is empty")
	}

	for _, locale := range locales {
		messages, ok := catalogues[locale]
		if !ok {
			t.Fatalf("Supported() named %q but no catalogue was loaded", locale)
		}
		for key := range reference {
			got, ok := messages[key]
			if !ok {
				t.Errorf("locale %q is missing key %q (present in %q)", locale, key, Default)
				continue
			}
			if strings.TrimSpace(got) == "" {
				t.Errorf("locale %q key %q is empty", locale, key)
			}
		}
		for key := range messages {
			if _, ok := reference[key]; !ok {
				t.Errorf("locale %q carries key %q that %q does not", locale, key, Default)
			}
		}
	}
}

// TestCatalogueMessagesAreRenderedVerbatim guards the S13 property from the
// catalogue side: a message is stored exactly as it renders, so a stray
// leading or trailing space, a newline, or an HTML entity cannot leak into
// a page that used to contain a literal.
func TestCatalogueMessagesAreRenderedVerbatim(t *testing.T) {
	for locale, messages := range catalogues {
		for key, message := range messages {
			if strings.TrimSpace(message) != message {
				t.Errorf("locale %q key %q = %q, want no surrounding whitespace", locale, key, message)
			}
			if strings.ContainsAny(message, "\n\r\t") {
				t.Errorf("locale %q key %q = %q, want a single line", locale, key, message)
			}
			if strings.Contains(message, "&") && strings.Contains(message, ";") {
				t.Errorf("locale %q key %q = %q, want unescaped copy (the template escapes it)", locale, key, message)
			}
		}
	}
}

// TestMissingKeyNeverRendersRaw is the rendering half of S10: a key absent
// from every catalogue degrades to a readable label, never to the key
// itself, and never to an empty string that silently deletes a control's
// label.
func TestMissingKeyNeverRendersRaw(t *testing.T) {
	tr := New(Spanish)

	got := tr.T("base.nav.this_key_does_not_exist")
	if got == "" {
		t.Fatal("a missing key rendered an empty string")
	}
	if strings.ContainsAny(got, "._") {
		t.Errorf("missing key rendered raw (%q), want a humanized label", got)
	}
	if got != "This key does not exist" {
		t.Errorf("missing key label = %q, want %q", got, "This key does not exist")
	}
	if tr.T("") != "" {
		t.Errorf("an empty key = %q, want an empty label", tr.T(""))
	}
}

// TestTranslatorFallsBackToTheDefaultCatalogue proves the documented
// fallback chain: the active locale first, the default catalogue second, so
// a translation gap shows English copy instead of a raw key.
func TestTranslatorFallsBackToTheDefaultCatalogue(t *testing.T) {
	tr := Translator{
		locale:   Spanish,
		messages: catalogue{},
		fallback: catalogue{"auth.welcome.title": "Welcome to tkt"},
	}

	if got := tr.T("auth.welcome.title"); got != "Welcome to tkt" {
		t.Errorf("T(missing in es) = %q, want the English copy", got)
	}
}

// TestTranslatorPrefersTheActiveCatalogue proves the first step of the
// chain: a translated key never falls through to English.
func TestTranslatorPrefersTheActiveCatalogue(t *testing.T) {
	tr := Translator{
		locale:   Spanish,
		messages: catalogue{"auth.welcome.title": "Te damos la bienvenida a tkt"},
		fallback: catalogue{"auth.welcome.title": "Welcome to tkt"},
	}

	if got := tr.T("auth.welcome.title"); got != "Te damos la bienvenida a tkt" {
		t.Errorf("T(es) = %q, want the Spanish copy", got)
	}
}

// TestNormalizeAnswersAShippedLocale pins the locale-code normalization the
// renderer relies on: surrounding space and case are ignored, and anything
// unknown falls back to the default locale.
func TestNormalizeAnswersAShippedLocale(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want string
	}{
		{English, English},
		{Spanish, Spanish},
		{" ES ", Spanish},
		{"En", English},
		{"", Default},
		{"fr", Default},
		{"english", Default},
	} {
		if got := Normalize(tc.in); got != tc.want {
			t.Errorf("Normalize(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestTranslatorAnswersShippedCopy proves the two catalogues are actually
// different: a Spanish instance must not render the English string, and the
// package-level T must agree with a Translator built for the locale.
func TestTranslatorAnswersShippedCopy(t *testing.T) {
	en := New(English).T("auth.welcome.title")
	es := New(Spanish).T("auth.welcome.title")
	if en == "" || es == "" {
		t.Fatalf("welcome title = (%q, %q), want copy in both locales", en, es)
	}
	if en == es {
		t.Errorf("welcome title is identical in both locales (%q), want a translation", en)
	}
	if got := T(Spanish, "auth.welcome.title"); got != es {
		t.Errorf("T(es, key) = %q, want %q", got, es)
	}
}

// --- Reference coverage (S10) ---

// templateKeyRef matches a catalogue key referenced from a template:
// {{t "area.component.element"}}.
var templateKeyRef = regexp.MustCompile(`\bt\s+"([a-z][a-z0-9]*(?:\.[a-z0-9]+)+)"`)

// goKeyRef matches a catalogue key referenced from Go: i18n.T(locale, "key"),
// i18n.T("key") is not a form the package offers (a locale is always
// explicit), and a Translator method call T("key") is matched too.
var goKeyRef = regexp.MustCompile(`\bT\(\s*(?:[A-Za-z0-9_.]+\s*,\s*)?"([a-z][a-z0-9]*(?:\.[a-z0-9]+)+)"`)

// TestEveryCataloguedKeyIsReferencedAndEveryReferenceIsCatalogued is the
// coverage half of S10: the parity test only proves the catalogues agree with
// each other, so a key that no template and no Go call names would still ship
// as dead copy, and a template or Go call naming a key no catalogue carries
// would render the fallback label in production. Both directions fail here.
//
// The scan is deliberately literal: templates must call t with a quoted key,
// and Go code must name a key through T(...). JavaScript copy is NOT scanned —
// it is not catalogue-backed yet (a later slice passes the strings in from the
// server).
func TestEveryCataloguedKeyIsReferencedAndEveryReferenceIsCatalogued(t *testing.T) {
	root := filepath.Join("..", "..")
	referenced := map[string][]string{}

	collect := func(path, line string, re *regexp.Regexp) {
		for _, match := range re.FindAllStringSubmatch(line, -1) {
			referenced[match[1]] = append(referenced[match[1]], path)
		}
	}

	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := entry.Name()
		if entry.IsDir() {
			switch name {
			case ".git", "node_modules", "testdata", "data", "vendor", "i18n":
				return fs.SkipDir
			}
			return nil
		}
		switch filepath.Ext(path) {
		case ".html":
			return scanFile(path, templateKeyRef, collect)
		case ".go":
			return scanFile(path, goKeyRef, collect)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}

	for _, locale := range Supported() {
		for key := range catalogues[locale] {
			if len(referenced[key]) == 0 {
				t.Errorf("locale %q carries key %q that no template and no Go call references", locale, key)
			}
		}
	}
	for key, places := range referenced {
		for _, locale := range Supported() {
			if _, ok := catalogues[locale][key]; !ok {
				t.Errorf("key %q referenced from %v is missing from locale %q", key, places, locale)
			}
		}
	}
}

// scanFile runs re over every line of path, handing each match to collect.
func scanFile(path string, re *regexp.Regexp, collect func(path, line string, re *regexp.Regexp)) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	collect(path, string(content), re)
	return nil
}
