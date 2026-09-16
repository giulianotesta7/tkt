package references

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDocumentedPathsExist fails when AGENTS.md or a project skill points at a
// path that does not exist: a renamed skill, a moved file, a deleted workflow,
// or a typo. It is the machine check that lets the skill index and the skills
// point at their sources instead of restating them, which is what issues #179
// and #182 kept deferring and what the native review kept reporting as an
// unverified reference.
//
// Blind spot, accepted and stated here on purpose: bare filenames with no slash
// (`fakes_test.go`, `go.mod`) and globs (`*_test.go`, `testdata/*.golden`) are
// skipped, because treating every bare name as a path would flag ordinary
// prose. A misspelled top-level directory inside a bare token stays undetected.
func TestDocumentedPathsExist(t *testing.T) {
	root, err := RepoRoot()
	if err != nil {
		t.Fatalf("repo root: %v", err)
	}

	sources, err := Sources(root)
	if err != nil {
		t.Fatalf("sources: %v", err)
	}
	if len(sources) < 2 {
		t.Fatalf("expected the skill index and at least one skill, got %d sources", len(sources))
	}

	// Counting both sides keeps the test honest: without it, a regression in the
	// extraction rules would make this test pass while checking nothing.
	validated := 0
	skips := map[string]int{}

	for _, source := range sources {
		rel, err := filepath.Rel(root, source)
		if err != nil {
			rel = source
		}

		t.Run(rel, func(t *testing.T) {
			body, err := os.ReadFile(source)
			if err != nil {
				t.Fatalf("read %s: %v", rel, err)
			}

			for line := range strings.SplitSeq(string(body), "\n") {
				for _, match := range RefPattern.FindAllStringSubmatch(line, -1) {
					if reason := SkipReason(match[1]); reason != "" {
						skips[reason]++
					}
				}
			}

			refs := Refs(source, body)
			if len(refs) == 0 {
				t.Errorf("%s has no path references: either it lost its content or the extraction is broken", rel)
			}

			for _, ref := range refs {
				validated++
				if !Exists(root, ref) {
					t.Errorf("%s:%d: unresolved reference %q\nresolves to %s\nif this is prose and not a path, add it to NonPathRefs with a reason",
						rel, ref.Line, ref.Token, Resolve(root, ref))
				}
			}
		})
	}

	if validated == 0 {
		t.Fatal("no references were validated: the extraction is broken")
	}

	t.Logf("validated %d references across %d files; skipped %d tokens as non-paths", validated, len(sources), len(skips))
}
