// Package references keeps the cross-file pointers in this repository honest.
//
// AGENTS.md and the project skills deliberately point at files instead of
// restating them, so a pointer is the only thing connecting a rule to its
// source. Native review kept reporting those pointers as unverified, first as
// R3-001 and R3-closing-gate-set, then again as R3-001 and R3-002 after the
// duplicated text was removed. Issues #179 and #182 both deferred the machine
// check; this package is it.
//
// The extraction is deliberately narrow. A backticked token is treated as a
// repository path only when it is slash-shaped and not prose, and every skip
// rule below is explicit, so a skip is a decision rather than an accident.
package references

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// RefPattern matches a single backticked token inside one line.
var RefPattern = regexp.MustCompile("`([^`\n]+)`")

// moduleSegment matches a first path segment shaped like a host name. That is
// how a Go module path starts (`modernc.org/sqlite`, `golang.org/x/vuln`) and
// how no repository directory starts.
var moduleSegment = regexp.MustCompile(`^[a-z0-9-]+\.[a-z]{2,}$`)

// NonPathRefs lists slash-shaped tokens that are prose rather than files. Each
// entry carries its reason so a later reader can judge whether it still holds.
// If this map starts collecting obvious paths, the skip rules are too loose.
var NonPathRefs = map[string]string{
	"family/description": "the slash-separated topic_key convention documented in engram-governance",
}

// Ref is one reference found in a source file.
type Ref struct {
	File  string // absolute path of the file that contains it
	Line  int    // 1-based line number
	Token string // the backticked text, before resolution
}

// SkipReason reports why a token is not treated as a repository path, or an
// empty string when it is one.
func SkipReason(token string) string {
	switch {
	case !strings.Contains(token, "/"):
		return "no slash"
	case strings.ContainsAny(token, "* <>{}"):
		return "glob or placeholder"
	case strings.HasPrefix(token, "http://"), strings.HasPrefix(token, "https://"):
		return "URL"
	}
	if _, ok := NonPathRefs[token]; ok {
		return "prose, listed in NonPathRefs"
	}
	if !strings.HasPrefix(token, "./") && !strings.HasPrefix(token, "../") {
		if first, _, found := strings.Cut(token, "/"); found && moduleSegment.MatchString(first) {
			return "module path"
		}
	}
	return ""
}

// RepoRoot returns the repository root, found by walking up from this source
// file until a go.mod appears.
func RepoRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0) // only the source path matters here
	if !ok {
		return "", errors.New("cannot locate the references package source file")
	}
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no go.mod found above %s", filepath.Dir(file))
		}
		dir = parent
	}
}

// Sources returns the files whose references must resolve: the skill index and
// every project skill. Skills are globbed so a new one is covered on arrival
// instead of needing an edit here.
func Sources(root string) ([]string, error) {
	skills, err := filepath.Glob(filepath.Join(root, ".agents", "skills", "*", "SKILL.md"))
	if err != nil {
		return nil, fmt.Errorf("glob skills: %w", err)
	}
	if len(skills) == 0 {
		return nil, fmt.Errorf("no skills found under %s", filepath.Join(root, ".agents", "skills"))
	}
	return append([]string{filepath.Join(root, "AGENTS.md")}, skills...), nil
}

// Refs extracts every path reference from one file, keeping line numbers so a
// failure can point at the exact line.
func Refs(file string, body []byte) []Ref {
	var refs []Ref
	for i, line := range strings.Split(string(body), "\n") {
		for _, match := range RefPattern.FindAllStringSubmatch(line, -1) {
			token := match[1]
			if SkipReason(token) != "" {
				continue
			}
			refs = append(refs, Ref{File: file, Line: i + 1, Token: token})
		}
	}
	return refs
}

// Resolve expands a token to an absolute path. Explicitly relative tokens
// resolve against the file that contains them; everything else resolves against
// the repository root.
func Resolve(root string, ref Ref) string {
	if strings.HasPrefix(ref.Token, "./") || strings.HasPrefix(ref.Token, "../") {
		return filepath.Join(filepath.Dir(ref.File), ref.Token)
	}
	return filepath.Join(root, ref.Token)
}

// Exists reports whether a reference resolves to a real file or directory.
func Exists(root string, ref Ref) bool {
	_, err := os.Stat(Resolve(root, ref))
	return err == nil
}
