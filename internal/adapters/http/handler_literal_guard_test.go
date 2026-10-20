package httpadapter

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// literalErrorBody matches a response body written straight from Go copy:
// http.Error with a string literal or a fmt.Sprintf format, which bypasses the
// catalogue and renders English on every instance (issue #335, S18). Bodies go
// through httpError with a catalogue key instead.
var literalErrorBody = regexp.MustCompile(`http\.Error\([^,]+,\s*(?:fmt\.Sprintf\(\s*)?"([^"]*)"`)

// literalErrorBodyAllowlist names the bodies that stay literal on purpose,
// keyed by file and message, each with its reason.
var literalErrorBodyAllowlist = map[string]string{
	"static.go|Internal server error": "embedded asset serving fails only when the binary is broken; no page renders it",
}

// literalErrorBodies returns every literal error body in dir's non-test Go
// files that the allowlist does not name, as "file:line: message".
func literalErrorBodies(t *testing.T, dir string) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatalf("glob %s: %v", dir, err)
	}
	var found []string
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for i, line := range strings.Split(string(body), "\n") {
			for _, match := range literalErrorBody.FindAllStringSubmatch(line, -1) {
				if _, ok := literalErrorBodyAllowlist[filepath.Base(path)+"|"+match[1]]; ok {
					continue
				}
				found = append(found, filepath.Base(path)+":"+strconv.Itoa(i+1)+": "+match[1])
			}
		}
	}
	sort.Strings(found)
	return found
}

// TestHandlersWriteNoLiteralErrorBodies is the handler half of S18's guard:
// the ValidationError scan in internal/i18n cannot see a body written with
// http.Error and an English literal.
func TestHandlersWriteNoLiteralErrorBodies(t *testing.T) {
	for _, hit := range literalErrorBodies(t, ".") {
		t.Errorf("literal error body bypasses the catalogue (use httpError with a key): %s", hit)
	}
}

// TestHandlerLiteralGuardFailsOnANewLiteral proves the guard bites on both
// literal forms, and that the allowlist only covers its own file.
func TestHandlerLiteralGuardFailsOnANewLiteral(t *testing.T) {
	dir := t.TempDir()
	probe := "package httpadapter\n\nfunc probe() {\n" +
		"\thttp.Error(w, \"Something brand new went wrong\", 400)\n" +
		"\thttp.Error(w, fmt.Sprintf(\"bad value %q\", v), 400)\n" +
		"\thttp.Error(w, \"Internal server error\", 500)\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "probe.go"), []byte(probe), 0o600); err != nil {
		t.Fatalf("write probe: %v", err)
	}
	got := literalErrorBodies(t, dir)
	want := []string{
		"probe.go:4: Something brand new went wrong",
		"probe.go:5: bad value %q",
		"probe.go:6: Internal server error",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("guard found %q, want %q", got, want)
	}
}
