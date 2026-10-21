package i18n

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
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
// {{t "area.component.element"}} (also tf/th, and tp for a plural form).
var templateKeyRef = regexp.MustCompile(`\b(?:t|tf|th|tp)\s+"([a-z][a-z0-9_]*(?:\.[a-z0-9_]+)+)"`)

// scriptKeyRef matches a catalogue key referenced from a browser script:
// window.tktI18n.t("js.area.key") (the server ships the messages as the
// script prologue, so a script references a key instead of holding copy).
var scriptKeyRef = regexp.MustCompile(`\bt\(\s*"([a-z][a-z0-9_]*(?:\.[a-z0-9_]+)+)"`)

// templateKeyPrefixRef matches a key BUILT at render time from a literal
// prefix, e.g. {{t (print "categories.kind." .Kind)}}: the prefix names the
// namespace and every catalogue key under it counts as referenced.
var templateKeyPrefixRef = regexp.MustCompile(`\bprint\s+"([a-z][a-z0-9_.]*\.)"`)

// goKeyLiteralRef matches a catalogue key held as a plain Go string literal,
// which is how the presentation layer hands a key to a template field
// (a label, an empty-state message, a badge). Any dotted lowercase literal is
// collected; only keys that exist are consulted, so an unrelated literal such
// as a file name cannot mask a missing reference.
var goKeyLiteralRef = regexp.MustCompile(`"([a-z][a-z0-9_]*(?:\.[a-z0-9_]+)+)"`)

// templatePluralRef matches a plural lookup: {{tp "area.component.element" n}}.
// The base key is a normal reference; its `_one`/`_other` forms are referenced
// by the same call, so coverage expands them below.
var templatePluralRef = regexp.MustCompile(`\btp\s+"([a-z][a-z0-9_]*(?:\.[a-z0-9_]+)+)"`)

// goKeyRef matches a catalogue key referenced from Go: i18n.T(locale, "key"),
// i18n.T("key") is not a form the package offers (a locale is always
// explicit), and a Translator method call T("key") is matched too. Tp is the
// plural form.
var goKeyRef = regexp.MustCompile(`\b(?:T|Tp)\(\s*(?:[A-Za-z0-9_.]+\s*,\s*)?"([a-z][a-z0-9_]*(?:\.[a-z0-9_]+)+)"`)

// nonKeyGoLiterals are key-shaped production Go string literals that are
// deliberately not catalogue keys: capability identifiers and shell file
// names. A literal listed here is allowed to be absent from every catalogue;
// any other key-shaped production literal must resolve, so deleting a key a Go
// field hands to a template (users.empty.none) fails coverage.
var nonKeyGoLiterals = map[string]bool{
	"tickets.create":       true,
	"tickets.assign":       true,
	"tickets.edit":         true,
	"tickets.metrics.view": true,
	"categories.manage":    true,
	"desks.manage":         true,
	"settings.manage":      true,
	"comments.public":      true,
	"comments.internal":    true,
	"users.manage":         true,
	"users.change_role":    true,
	"users.grant_admin":    true,
	"auth.html":            true,
	"base.html":            true,
	"go.mod":               true,
	"smtp.key":             true,
}

// prefixKeySuffixes pins the concrete suffixes each enum-driven template
// prefix reference can produce. A prefix with one surviving arm (e.g.
// categories.kind.department) would otherwise hide a deleted arm, so every
// arm listed here must resolve even when the prefix still matches a sibling.
var prefixKeySuffixes = map[string][]string{
	"categories.kind.":             {"category", "department", "desk"},
	"categories.drawer.help.":      {"category", "department", "desk"},
	"categories.drawer.name_help.": {"category", "department", "desk"},
	"ticket_metrics.group.":        {"agent", "desk"},
	"ticket_metrics.group_by.":     {"priority", "category", "total"},
}

// TestEveryCataloguedKeyIsReferencedAndEveryReferenceIsCatalogued is the
// coverage half of S10: the parity test only proves the catalogues agree with
// each other, so a key that no template and no Go call names would still ship
// as dead copy, and a template or Go call naming a key no catalogue carries
// would render the fallback label in production. Both directions fail here.
//
// The scan is deliberately literal: templates must call t with a quoted key,
// Go code must name a key through T(...), and a browser script reads its copy
// through window.tktI18n.t("js...") — the only namespaces a surface may use.
// A browser script holding an English literal is caught by review, not here:
// its copy now comes from the server prologue.
func TestEveryCataloguedKeyIsReferencedAndEveryReferenceIsCatalogued(t *testing.T) {
	root := filepath.Join("..", "..")
	for _, problem := range referenceCoverage(root, catalogues) {
		t.Error(problem)
	}
}

// TestReferenceCoverageFailsOnDeletedKey proves the two reference kinds the
// scan must not miss: an enum arm named through a Go literal and a template
// prefix (categories.kind.category), and a key handed to a template field as a
// Go string literal (users.empty.none). Each is deleted from BOTH catalogues in
// a copy, and the scan must report the missing key.
func TestReferenceCoverageFailsOnDeletedKey(t *testing.T) {
	root := filepath.Join("..", "..")
	for _, key := range []string{"categories.kind.category", "users.empty.none"} {
		reduced := copyCatalogues(catalogues)
		for locale := range reduced {
			delete(reduced[locale], key)
		}
		problems := referenceCoverage(root, reduced)
		found := false
		for _, problem := range problems {
			if strings.Contains(problem, key) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("deleting %q from every catalogue must fail coverage, got %v", key, problems)
		}
	}
}

// TestScriptsReadOnlyKeysTheirPrologueCarries proves the half of S10 the
// coverage scan cannot see: a browser script is served only the `js.`-prefixed
// entries of the active catalogue, so a script that reads any other key silently
// renders the fallback label. Every key a script reads must be one the prologue
// the server ships with it actually carries.
func TestScriptsReadOnlyKeysTheirPrologueCarries(t *testing.T) {
	scripts, err := filepath.Glob(filepath.Join("..", "..", "web", "templates", "static", "*.js"))
	if err != nil {
		t.Fatalf("glob scripts: %v", err)
	}
	for _, path := range scripts {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for _, match := range scriptKeyRef.FindAllStringSubmatch(string(body), -1) {
			if !strings.HasPrefix(match[1], ScriptPrefix) {
				t.Errorf("%s reads %q, which the %q prologue never carries", filepath.Base(path), match[1], ScriptPrefix)
			}
		}
	}
}

func copyCatalogues(in map[string]catalogue) map[string]catalogue {
	out := make(map[string]catalogue, len(in))
	for locale, messages := range in {
		copied := make(catalogue, len(messages))
		for key, message := range messages {
			copied[key] = message
		}
		out[locale] = copied
	}
	return out
}

// referenceCoverage returns every coverage problem for cats, so the caller can
// run it against the real catalogues and against a deliberately broken copy.
func referenceCoverage(root string, cats map[string]catalogue) []string {
	locales := make([]string, 0, len(cats))
	for locale := range cats {
		locales = append(locales, locale)
	}
	sort.Strings(locales)

	referenced := map[string][]string{}
	literals := map[string][]string{}
	plurals := map[string][]string{}

	collect := func(path, line string, re *regexp.Regexp) {
		for _, match := range re.FindAllStringSubmatch(line, -1) {
			referenced[match[1]] = append(referenced[match[1]], path)
		}
	}
	collectPlural := func(path, line string, re *regexp.Regexp) {
		for _, match := range re.FindAllStringSubmatch(line, -1) {
			plurals[match[1]] = append(plurals[match[1]], path)
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
			if err := scanFile(path, templateKeyRef, collect); err != nil {
				return err
			}
			if err := scanFile(path, templatePluralRef, collectPlural); err != nil {
				return err
			}
			return scanFile(path, templateKeyPrefixRef, collect)
		case ".go":
			if err := scanFile(path, goKeyRef, collect); err != nil {
				return err
			}
			// Test fixtures hold expected messages and fake identifiers, not
			// shipped copy: their literals would invent missing keys
			// (`evil.example`), so the literal-integrity scan reads production
			// sources only.
			if strings.HasSuffix(path, "_test.go") {
				return nil
			}
			return scanFile(path, goKeyLiteralRef, func(path, line string, re *regexp.Regexp) {
				for _, match := range re.FindAllStringSubmatch(line, -1) {
					literals[match[1]] = append(literals[match[1]], path)
				}
			})
		case ".js":
			return scanFile(path, scriptKeyRef, collect)
		}
		return nil
	})
	if err != nil {
		return []string{fmt.Sprintf("walk %s: %v", root, err)}
	}

	// A Go string literal is a key reference only when a catalogue carries it, so
	// it can never invent a missing key in the "catalogued" direction; the
	// literal-integrity check below reports the missing case (deletion).
	for key, places := range literals {
		if _, ok := cats[Default][key]; ok {
			referenced[key] = append(referenced[key], places...)
		}
	}

	// Expand every literal prefix into the catalogue keys it can produce.
	prefixes := map[string][]string{}
	for key, places := range referenced {
		if strings.HasSuffix(key, ".") {
			prefixes[key] = places
		}
	}
	for prefix, places := range prefixes {
		for _, locale := range locales {
			for key := range cats[locale] {
				if strings.HasPrefix(key, prefix) {
					referenced[key] = append(referenced[key], places...)
				}
			}
		}
	}

	// A plural call (tp) references the base sentence AND its one/other forms,
	// so a suffixed form a catalogue ships is not dead copy.
	for base, places := range plurals {
		for _, suffix := range []string{"_one", "_other"} {
			if _, ok := cats[Default][base+suffix]; ok {
				referenced[base+suffix] = append(referenced[base+suffix], places...)
			}
		}
	}

	var problems []string
	for _, locale := range locales {
		for key := range cats[locale] {
			// The label.* namespace is resolved at render time by the label
			// helpers (label.<kind>.<value>), so no surface names those keys
			// textually; the parity test still covers them.
			if strings.HasPrefix(key, "label.") {
				continue
			}
			if len(referenced[key]) == 0 {
				problems = append(problems, fmt.Sprintf("locale %q carries key %q that no template and no Go call references", locale, key))
			}
		}
	}
	for key, places := range referenced {
		if strings.HasSuffix(key, ".") {
			continue // a namespace prefix, expanded above
		}
		for _, locale := range locales {
			if _, ok := cats[locale][key]; !ok {
				if nonKeyGoLiterals[key] {
					continue
				}
				problems = append(problems, fmt.Sprintf("key %q referenced from %v is missing from locale %q", key, places, locale))
			}
		}
	}
	// A referenced prefix must resolve at least one key, and every enum arm it
	// can produce (a surviving sibling must not hide a deleted arm).
	for prefix, places := range prefixes {
		resolved := false
		for _, locale := range locales {
			for key := range cats[locale] {
				if strings.HasPrefix(key, prefix) {
					resolved = true
				}
			}
		}
		if !resolved {
			problems = append(problems, fmt.Sprintf("prefix %q referenced from %v resolves no catalogue key", prefix, places))
		}
		for _, suffix := range prefixKeySuffixes[prefix] {
			if _, ok := cats[Default][prefix+suffix]; !ok {
				problems = append(problems, fmt.Sprintf("prefix %q must resolve %q", prefix, prefix+suffix))
			}
		}
	}
	// Every key-shaped production Go literal must be a catalogue key or a known
	// non-key literal, so deleting a key a Go field hands to a template fails.
	for literal, places := range literals {
		if _, ok := cats[Default][literal]; ok {
			continue
		}
		if nonKeyGoLiterals[literal] {
			continue
		}
		problems = append(problems, fmt.Sprintf("Go literal %q from %v looks like a catalogue key but no catalogue carries it", literal, places))
	}
	return problems
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

// TestSpanishCatalogueUsesNeutralTuteo enforces the recorded tone (L6, "tuteo
// neutro"): the Spanish interface addresses the reader as tú — never voseo, and
// never usted. It has two halves. The marker list catches the voseo forms a
// word list can name, matched as whole words ("los suyos" is not a "sos"). The
// structural half catches the list's blind spot: `abrí` shipped past it once,
// so every word ending in an accented á/é/í must now be a known neutral form.
func TestSpanishCatalogueUsesNeutralTuteo(t *testing.T) {
	markers := []string{
		"vos", "vosotros", "tenés", "necesitás", "podés", "querés", "sabés", "sos", "hacé", "poné",
		"iniciá", "creá", "configurá", "describí", "seleccioná", "escribí", "intentá",
		"asignármelo", "confirmá", "devolvelo", "elegí", "guardá", "cancelá", "agregá",
		"eliminá", "buscá", "usá", "administrá", "actualizá", "reactivá", "desactivá",
		"restablecé", "salís", "salí", "andá", "vení", "mirá", "probá", "abrí", "abrís",
		"cerrá", "volvé", "completá", "revisá", "subí", "decí", "pedí", "seguí",
		"fijate", "hacelo", "callate", "decime", "ponete", "escribime", "dame",
		// Present-tense voseo ending in -s: the structural accented-vowel half
		// never reaches these, so the marker list is their only guard.
		"hablás", "comés", "vivís", "escribís", "venís", "decís", "hacés", "andás",
		"ustedes", "usted",
	}
	words := make([]*regexp.Regexp, 0, len(markers))
	for _, marker := range markers {
		words = append(words, regexp.MustCompile(`(?i)\b`+regexp.QuoteMeta(marker)+`\b`))
	}
	wordRe := regexp.MustCompile(`[\p{L}]+`)
	for key, message := range catalogues[Spanish] {
		for i, word := range words {
			if word.MatchString(message) {
				t.Errorf("es %q uses the voseo/usted marker %q: %q", key, markers[i], message)
			}
		}
		if strings.Contains(message, "vos@") {
			t.Errorf("es %q ships a voseo placeholder: %q", key, message)
		}
		for _, word := range wordRe.FindAllString(message, -1) {
			lower := strings.ToLower(word)
			runes := []rune(lower)
			if len(runes) == 0 {
				continue
			}
			switch runes[len(runes)-1] {
			case 'á', 'é', 'í':
			default:
				continue
			}
			if !voseoAccentedNeutral[lower] {
				t.Errorf("es %q ends the word %q in an accented vowel that is not a known neutral form: %q", key, word, message)
			}
		}
	}
}

// voseoAccentedNeutral lists the accented word-ends that are NOT voseo: neutral
// pronouns and adverbs, first- and third-person forms, and futures the
// catalogue legitimately uses. Every other word ending in á/é/í fails, so a new
// voseo imperative cannot slip through the marker list. Only forms an accented
// word-end check can actually reach belong here: a word ending in s or n can
// never match, so an entry for one would be dead weight.
var voseoAccentedNeutral = map[string]bool{
	"así": true, "aquí": true, "allí": true, "ahí": true, "sí": true, "mí": true,
	"qué": true, "esté": true, "está": true, "será": true,
	"podrá": true, "tendrá": true, "habrá": true, "hará": true, "estará": true,
	"quitará": true, "envié": true,
}

// TestTranslatorPluralForms pins the minimal one/other mechanism: Spanish reads
// its singular form where it differs, and English carries a singular form too:
// #343 fixed the historical "1 accounts" to "1 account" (S4).
func TestTranslatorPluralForms(t *testing.T) {
	en := New(English)
	es := New(Spanish)
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"en accounts one", en.Tp("users.count", 1), "1 account"},
		{"en accounts many", en.Tp("users.count", 3), "3 accounts"},
		{"es accounts one", es.Tp("users.count", 1), "1 cuenta"},
		{"es accounts many", es.Tp("users.count", 3), "3 cuentas"},
		{"es accounts zero", es.Tp("users.count", 0), "0 cuentas"},
		{"en workload one", en.Tp("ticket_metrics.workload.scope", 1), "Current assignment · 1 pending tickets"},
		{"es workload one", es.Tp("ticket_metrics.workload.scope", 1), "Asignación actual · 1 ticket pendiente"},
		{"es workload many", es.Tp("ticket_metrics.workload.scope", 4), "Asignación actual · 4 tickets pendientes"},
		{"en done one", en.Tp("tickets.pending.done_of", 1, 1), "1 of 1 done"},
		{"es done one", es.Tp("tickets.pending.done_of", 1, 1), "1 de 1 completado"},
		{"es done zero", es.Tp("tickets.pending.done_of", 0, 1), "0 de 1 completados"},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("%s = %q, want %q", tc.name, tc.got, tc.want)
		}
	}
	// A key with no plural form falls back to the base sentence with the count
	// interpolated.
	if got := es.Tp("ticket_metrics.excluded", 3); got != "3 excluidos" {
		t.Errorf("Tp on a key without a plural form = %q, want the base sentence", got)
	}
}

// --- Keyless user-visible ValidationError guard (S18) ---

// validationErrorLiteral matches a ValidationError composite literal in a
// production Go source (the domain package's own unqualified form included).
var validationErrorLiteral = regexp.MustCompile(`ValidationError\{`)

// validationKeyField matches the Key field of a ValidationError literal.
var validationKeyField = regexp.MustCompile(`Key:\s*"([^"]+)"`)

// errMsgConstDecl matches a domain/application English message constant, so the
// scan can resolve a Message that names one.
var errMsgConstDecl = regexp.MustCompile(`(?:^|\s)(?:domain\.|application\.)?(ErrMsg\w+)\s*=\s*"((?:[^"\\]|\\.)*)"`)

// validationMessageLiteral matches the first Go string literal inside a Message
// expression, which is enough for fmt.Sprintf("...", args).
var validationMessageLiteral = regexp.MustCompile(`"((?:[^"\\]|\\.)*)"`)

// keylessValidationErrorAllowlist lists the keyless ValidationError literals
// that are INTERNAL: unreachable from any rendered page because they guard a
// programming error, a store recheck, or a workflow definition the publish path
// already validated. Every other keyless literal is user-visible and must carry
// a catalogue Key (S18), so a NEW keyless user-visible error fails this guard.
// The key is `repo-relative/path.go|<resolved English message>`; the value is a
// one-line reason.
var keylessValidationErrorAllowlist = map[string]string{
	"internal/adapters/http/handlers_tickets.go|assign_to_desk requires config":            "handler guard: a published assign_to_desk step always carries config",
	"internal/adapters/sqlite/workflow_uow.go|create plan missing ticket":                  "store recheck: the caller always supplies a planned ticket",
	"internal/adapters/sqlite/workflow_uow.go|assignment target must be an agent or above": "store recheck after the service already validated the assignment target",
	"internal/application/catalog_service.go|category is required":                         "programming guard: no request hands UpdateCategoryFor a nil category",
	"internal/application/catalog_service.go|identifier is required":                       "internal id guard for catalog ids, never a user-authored value",
	"internal/application/workflow_runner.go|workflow definition is empty":                 "definition guard: a published workflow always has at least one step",
	"internal/application/workflow_runner.go|assign_to_desk requires config":               "definition guard: publish rejects an assign_to_desk step without config",
	"internal/application/workflow_runner.go|unknown strategy":                             "definition guard: publish rejects an unknown assignment strategy",
	"internal/application/workflow_runner.go|form requires config":                         "definition guard: publish rejects a form step without fields",
	"internal/application/workflow_runner.go|manual_task requires config":                  "definition guard: publish rejects a manual_task step without instructions",
	"internal/application/workflow_runner.go|unknown step type %q":                         "definition guard: publish rejects an unknown step type",
	"internal/application/workflow_runner.go|unknown form actor":                           "definition guard: publish rejects an unknown form actor",
	"internal/application/workflow_runner.go|unknown terminal step type %q":                "definition guard: publish rejects an unknown terminal step type",
	"internal/application/workflow_runner.go|unknown field kind":                           "definition guard: publish rejects an unknown form field kind",
}

// validationErrorSite is one ValidationError literal found in production source.
type validationErrorSite struct {
	path    string
	line    int
	key     string
	message string
	dynamic bool
}

// TestUserVisibleValidationErrorsCarryACatalogueKey is the S18 guard: every
// non-test ValidationError literal a user can read must name a catalogue key,
// unless it is an explicitly documented INTERNAL guard. It also proves the
// English contract: a keyed static message equals its English catalogue entry
// byte for byte (S13).
func TestUserVisibleValidationErrorsCarryACatalogueKey(t *testing.T) {
	root := filepath.Join("..", "..")
	sites, err := validationErrorSites(root)
	if err != nil {
		t.Fatalf("scan ValidationError literals: %v", err)
	}
	if len(sites) < 30 {
		t.Fatalf("scanned only %d ValidationError literals, want the production set", len(sites))
	}
	seen := map[string]bool{}
	for _, site := range sites {
		if site.key == "" {
			allow := site.path + "|" + site.message
			if _, ok := keylessValidationErrorAllowlist[allow]; !ok {
				t.Errorf("%s:%d: keyless ValidationError %q is not in the INTERNAL allowlist; give it a catalogue key", site.path, site.line, site.message)
			}
			seen[allow] = true
			continue
		}
		if site.dynamic {
			continue
		}
		want, ok := catalogues[English][site.key]
		if !ok {
			t.Errorf("%s:%d: key %q is not in the English catalogue", site.path, site.line, site.key)
			continue
		}
		if want != site.message {
			t.Errorf("%s:%d: English catalogue %q = %q, want the Message bytes %q", site.path, site.line, site.key, want, site.message)
		}
	}
	for allow := range keylessValidationErrorAllowlist {
		if !seen[allow] {
			t.Errorf("stale allowlist entry %q: no keyless ValidationError matches it", allow)
		}
	}
}

// TestKeylessValidationErrorGuardFailsOnANewLiteral proves the detection half
// of the S18 guard on a temp tree (never the real one): the scanner reports a
// new keyless literal by its resolved message, so a future user-visible error
// without a catalogue key cannot slip past the allowlist.
func TestKeylessValidationErrorGuardFailsOnANewLiteral(t *testing.T) {
	dir := t.TempDir()
	source := "package newerr\n\nimport \"github.com/giulianotesta7/tkt/internal/domain\"\n\nfunc f() error {\n\treturn &domain.ValidationError{Field: \"title\", Message: \"a brand new user-visible refusal\"}\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "newerr.go"), []byte(source), 0o600); err != nil {
		t.Fatalf("write temp source: %v", err)
	}
	sites, err := validationErrorSites(dir)
	if err != nil {
		t.Fatalf("scan temp tree: %v", err)
	}
	found := false
	for _, site := range sites {
		if site.message != "a brand new user-visible refusal" {
			continue
		}
		found = true
		if _, ok := keylessValidationErrorAllowlist[site.path+"|"+site.message]; ok {
			t.Fatal("a new literal must never match the INTERNAL allowlist")
		}
	}
	if !found {
		t.Fatalf("the scanner missed the keyless literal, got %+v", sites)
	}
}

// validationErrorSites walks every production Go source under root and returns
// each ValidationError literal it finds, resolving a constant Message where it
// can so the allowlist stays readable.
func validationErrorSites(root string) ([]validationErrorSite, error) {
	consts := errMsgConstants(root)
	var sites []validationErrorSite
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "node_modules", "vendor", "data", "testdata":
				return fs.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		text := string(content)
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		for _, loc := range validationErrorLiteral.FindAllStringIndex(text, -1) {
			lineStart := strings.LastIndexByte(text[:loc[0]], '\n') + 1
			if strings.Contains(text[lineStart:loc[0]], "//") {
				continue // a comment naming the type, not a construction
			}
			block := validationBlock(text[loc[0]:])
			message, dynamic := resolveValidationMessage(validationMessageExpr(block), consts)
			sites = append(sites, validationErrorSite{
				path:    rel,
				line:    1 + strings.Count(text[:loc[0]], "\n"),
				key:     validationKey(block),
				message: message,
				dynamic: dynamic,
			})
		}
		return nil
	})
	return sites, err
}

// validationBlock returns the balanced `{...}` block that starts at the first
// brace in text. Message literals in this repository carry no brace, so a plain
// brace count is exact.
func validationBlock(text string) string {
	open := strings.IndexByte(text, '{')
	if open < 0 {
		return ""
	}
	depth := 0
	for i := open; i < len(text); i++ {
		switch text[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return text[open : i+1]
			}
		}
	}
	return text[open:]
}

// validationKey returns the Key field value of a ValidationError block, or "".
func validationKey(block string) string {
	if m := validationKeyField.FindStringSubmatch(block); m != nil {
		return m[1]
	}
	return ""
}

// validationMessageExpr returns the source expression of the Message field: the
// rest of its line, or the next line when the value sits on one.
func validationMessageExpr(block string) string {
	i := strings.Index(block, "Message:")
	if i < 0 {
		return ""
	}
	rest := block[i+len("Message:"):]
	line := rest
	if j := strings.IndexByte(rest, '\n'); j >= 0 {
		line = rest[:j]
	}
	line = trimFieldValue(line)
	if line != "" {
		return line
	}
	nl := strings.IndexByte(rest, '\n')
	if nl < 0 {
		return ""
	}
	next := rest[nl+1:]
	if j := strings.IndexByte(next, '\n'); j >= 0 {
		next = next[:j]
	}
	return trimFieldValue(next)
}

// trimFieldValue strips a trailing comma, closing brace, or a following Key
// field from one Message expression line.
func trimFieldValue(value string) string {
	value = strings.TrimSpace(value)
	if j := strings.Index(value, ", Key:"); j >= 0 {
		value = strings.TrimSpace(value[:j])
	}
	value = strings.TrimSuffix(value, "}")
	value = strings.TrimSuffix(value, ",")
	return strings.TrimSpace(value)
}

// resolveValidationMessage turns a Message expression into its English bytes
// and reports whether the sentence is interpolated. A constant name resolves
// through consts; a fmt.Sprintf call takes its format string.
func resolveValidationMessage(expr string, consts map[string]string) (string, bool) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return "", false
	}
	if strings.HasPrefix(expr, "fmt.Sprintf(") {
		if m := validationMessageLiteral.FindStringSubmatch(expr); m != nil {
			if value, err := strconv.Unquote(`"` + m[1] + `"`); err == nil {
				return value, true
			}
		}
		return expr, true
	}
	if strings.HasPrefix(expr, `"`) {
		if value, err := strconv.Unquote(expr); err == nil {
			return value, false
		}
		return expr, false
	}
	name := strings.TrimPrefix(strings.TrimPrefix(expr, "domain."), "application.")
	if value, ok := consts[name]; ok {
		return value, false
	}
	return expr, false
}

// errMsgConstants resolves every domain/application English message constant so
// a Message that names one can be reported by its copy.
func errMsgConstants(root string) map[string]string {
	out := map[string]string{}
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "node_modules", "vendor":
				return fs.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		for _, m := range errMsgConstDecl.FindAllStringSubmatch(string(content), -1) {
			if value, uerr := strconv.Unquote(`"` + m[2] + `"`); uerr == nil {
				out[m[1]] = value
			}
		}
		return nil
	})
	return out
}
